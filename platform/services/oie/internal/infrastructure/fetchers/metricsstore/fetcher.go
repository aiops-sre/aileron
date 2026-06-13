package metricsstore

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	domain "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/evidence"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/fetchers"
)

const (
	metricsHTTPTimeout   = 10 * time.Second
	cpuThreshold         = 80.0  // percent
	memoryThreshold      = 85.0  // percent
	errorRateThreshold   = 0.01  // requests/s
)

// MetricsStoreFetcher queries a long-term metrics store (Thanos, VictoriaMetrics,
// Cortex, or Mimir) for SLI metrics at the time the incident started.
// It evaluates CPU utilisation, memory utilisation, and error rate and emits
// supporting evidence when any threshold is exceeded.
type MetricsStoreFetcher struct {
	storeURL   string
	httpClient *http.Client
}

// New constructs a MetricsStoreFetcher. It resolves the store URL from the
// following environment variables in priority order:
//
//	METRICS_STORE_URL → THANOS_URL → VICTORIAMETRICS_URL → CORTEX_URL
func New() *MetricsStoreFetcher {
	storeURL := resolveStoreURL()
	return &MetricsStoreFetcher{
		storeURL: storeURL,
		httpClient: &http.Client{
			Timeout: metricsHTTPTimeout,
		},
	}
}

// SetURL overrides the metrics store base URL (useful in tests).
func (f *MetricsStoreFetcher) SetURL(u string) { f.storeURL = u }

// ID implements EvidenceFetcher.
func (f *MetricsStoreFetcher) ID() fetchers.FetcherID { return fetchers.FetcherID("long_term_metrics") }

// DependsOn implements EvidenceFetcher — no upstream dependencies.
func (f *MetricsStoreFetcher) DependsOn() []fetchers.FetcherID { return nil }

// SourceName implements EvidenceFetcher.
func (f *MetricsStoreFetcher) SourceName() string { return "metrics_store" }

// FetchHistorical queries the long-term metrics store for SLI metrics at
// req.IncidentStartAt and emits supporting Evidence when thresholds are exceeded.
func (f *MetricsStoreFetcher) FetchHistorical(ctx context.Context, req *fetchers.FetchRequest) (*fetchers.FetchResult, error) {
	start := time.Now()

	if f.storeURL == "" {
		return &fetchers.FetchResult{
			Status:     domain.FetchMissing,
			DurationMs: msSince(start),
		}, nil
	}

	ns, pod := f.extractEntityCoords(req)
	if ns == "" {
		return &fetchers.FetchResult{
			Status:     domain.FetchMissing,
			DurationMs: msSince(start),
		}, nil
	}

	queryTime := req.IncidentStartAt
	if queryTime.IsZero() {
		queryTime = time.Now().UTC()
	}
	ts := fmt.Sprintf("%d", queryTime.Unix())

	cpuPct, err := f.queryCPU(ctx, ns, pod, ts)
	if err != nil {
		cpuPct = -1
	}

	memPct, err := f.queryMemory(ctx, ns, pod, ts)
	if err != nil {
		memPct = -1
	}

	errorRate, err := f.queryErrorRate(ctx, ns, pod, ts)
	if err != nil {
		errorRate = -1
	}

	// Nothing to report if all queries failed.
	if cpuPct < 0 && memPct < 0 && errorRate < 0 {
		return &fetchers.FetchResult{
			Status:     domain.FetchError,
			FetchError: fmt.Errorf("metricsstore: all metric queries failed for ns=%s pod=%s", ns, pod),
			DurationMs: msSince(start),
		}, nil
	}

	// Check whether any threshold is breached.
	cpuBreached := cpuPct >= cpuThreshold
	memBreached := memPct >= memoryThreshold
	errBreached := errorRate >= errorRateThreshold

	if !cpuBreached && !memBreached && !errBreached {
		return &fetchers.FetchResult{
			Status:     domain.FetchMissing,
			DurationMs: msSince(start),
		}, nil
	}

	invID := uuid.Nil
	runID := uuid.Nil
	if req.Investigation != nil {
		invID = req.Investigation.ID
	}
	if req.RunID != "" {
		if parsed, err2 := uuid.Parse(req.RunID); err2 == nil {
			runID = parsed
		}
	}

	desc := buildDescription(cpuPct, memPct, errorRate)
	idKey := fmt.Sprintf("%s::long_term_metrics::%s::%s::sli", invID, ns, pod)

	payload, _ := json.Marshal(map[string]interface{}{
		"namespace":      ns,
		"pod":            pod,
		"cpu_pct":        cpuPct,
		"memory_pct":     memPct,
		"error_rate_rps": errorRate,
		"query_time":     queryTime.Format(time.RFC3339),
	})

	ev := domain.NewEvidence(
		invID,
		runID,
		string(f.ID()),
		idKey,
		domain.TypeKubeSenseAnomaly,
		f.SourceName(),
		domain.RoleSupports,
		0.65,
		0.88,
		desc,
		domain.TemporalHistorical,
		payload,
	)

	return &fetchers.FetchResult{
		Evidence:   []*domain.Evidence{ev},
		Status:     domain.FetchSuccess,
		DurationMs: msSince(start),
	}, nil
}

// ── internal helpers ───────────────────────────────────────────────────────────

// extractEntityCoords derives namespace and pod name from the request.
func (f *MetricsStoreFetcher) extractEntityCoords(req *fetchers.FetchRequest) (ns, pod string) {
	if req.EntityProfile != nil {
		ns = req.EntityProfile.K8sNamespace
		pod = req.EntityProfile.ResourceName
	}
	if req.Investigation != nil && req.Investigation.TopologyPath != "" {
		segments := strings.Split(strings.Trim(req.Investigation.TopologyPath, "/"), "/")
		if ns == "" && len(segments) >= 2 {
			ns = segments[1]
		}
		if pod == "" && len(segments) > 0 {
			pod = segments[len(segments)-1]
		}
	}
	return ns, pod
}

// promQueryResult is a minimal unmarshal target for the Prometheus-compatible
// instant query response (/api/v1/query).
type promQueryResult struct {
	Data struct {
		Result []struct {
			Value []interface{} `json:"value"` // [timestamp, "value_string"]
		} `json:"result"`
	} `json:"data"`
}

// instantQuery performs a Prometheus-compatible instant query and returns the
// first scalar result value. Returns -1.0 and an error if no result is found.
func (f *MetricsStoreFetcher) instantQuery(ctx context.Context, promql, ts string) (float64, error) {
	params := url.Values{}
	params.Set("query", promql)
	params.Set("time", ts)

	endpoint := fmt.Sprintf("%s/api/v1/query?%s", strings.TrimRight(f.storeURL, "/"), params.Encode())

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return -1, fmt.Errorf("metricsstore: building request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")

	resp, err := f.httpClient.Do(httpReq)
	if err != nil {
		return -1, fmt.Errorf("metricsstore: GET query: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return -1, fmt.Errorf("metricsstore: reading response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return -1, fmt.Errorf("metricsstore: unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var parsed promQueryResult
	if err := json.Unmarshal(body, &parsed); err != nil {
		return -1, fmt.Errorf("metricsstore: parsing response: %w", err)
	}

	if len(parsed.Data.Result) == 0 {
		return -1, fmt.Errorf("metricsstore: no result for query %q", promql)
	}
	vals := parsed.Data.Result[0].Value
	if len(vals) < 2 {
		return -1, fmt.Errorf("metricsstore: unexpected value format")
	}
	valStr, ok := vals[1].(string)
	if !ok {
		return -1, fmt.Errorf("metricsstore: value is not a string")
	}

	var val float64
	if _, scanErr := fmt.Sscanf(valStr, "%f", &val); scanErr != nil {
		return -1, fmt.Errorf("metricsstore: parsing float %q: %w", valStr, scanErr)
	}
	return val, nil
}

// queryCPU returns the average CPU utilisation percentage for the pod.
func (f *MetricsStoreFetcher) queryCPU(ctx context.Context, ns, pod, ts string) (float64, error) {
	podSelector := pod
	if podSelector == "" {
		podSelector = ".*"
	}
	promql := fmt.Sprintf(
		`avg(rate(container_cpu_usage_seconds_total{namespace=%q,pod=~%q}[5m]))*100`,
		ns, podSelector+".*",
	)
	return f.instantQuery(ctx, promql, ts)
}

// queryMemory returns the average memory utilisation percentage for the pod.
func (f *MetricsStoreFetcher) queryMemory(ctx context.Context, ns, pod, ts string) (float64, error) {
	podSelector := pod
	if podSelector == "" {
		podSelector = ".*"
	}
	promql := fmt.Sprintf(
		`avg(container_memory_working_set_bytes{namespace=%q,pod=~%q} / container_spec_memory_limit_bytes{namespace=%q,pod=~%q} > 0)*100`,
		ns, podSelector+".*", ns, podSelector+".*",
	)
	return f.instantQuery(ctx, promql, ts)
}

// queryErrorRate returns the per-second error rate for the pod via HTTP 5xx signals.
func (f *MetricsStoreFetcher) queryErrorRate(ctx context.Context, ns, pod, ts string) (float64, error) {
	podSelector := pod
	if podSelector == "" {
		podSelector = ".*"
	}
	promql := fmt.Sprintf(
		`sum(rate(http_requests_total{namespace=%q,pod=~%q,status=~"5.."}[5m]))`,
		ns, podSelector+".*",
	)
	return f.instantQuery(ctx, promql, ts)
}

// buildDescription produces a human-readable summary of the metrics at incident time.
func buildDescription(cpuPct, memPct, errorRate float64) string {
	parts := []string{"Metrics at incident start:"}
	if cpuPct >= 0 {
		parts = append(parts, fmt.Sprintf("CPU=%.1f%%", cpuPct))
	}
	if memPct >= 0 {
		parts = append(parts, fmt.Sprintf("Memory=%.1f%%", memPct))
	}
	if errorRate >= 0 {
		parts = append(parts, fmt.Sprintf("Error rate=%.4f/s", errorRate))
	}
	return strings.Join(parts, " ")
}

// resolveStoreURL picks the metrics store URL from environment variables in
// priority order: METRICS_STORE_URL, THANOS_URL, VICTORIAMETRICS_URL, CORTEX_URL.
func resolveStoreURL() string {
	for _, envVar := range []string{
		"METRICS_STORE_URL",
		"THANOS_URL",
		"VICTORIAMETRICS_URL",
		"CORTEX_URL",
	} {
		if v := os.Getenv(envVar); v != "" {
			return v
		}
	}
	return ""
}

// msSince returns the elapsed time in milliseconds since t.
func msSince(t time.Time) int64 {
	return time.Since(t).Milliseconds()
}
