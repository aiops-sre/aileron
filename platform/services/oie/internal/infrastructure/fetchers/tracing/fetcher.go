package tracing

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
	defaultJaegerURL  = ""
	defaultTempoURL   = ""
	tracingHTTPTimeout = 8 * time.Second
	traceLookback     = "30m"
	traceLimit        = 5
)

// TracingFetcher queries Jaeger or Tempo for failed traces relevant to the
// investigation entity. It attempts Jaeger first; if JAEGER_URL is not set,
// it falls back to Tempo via TEMPO_URL.
type TracingFetcher struct {
	jaegerURL  string
	tempoURL   string
	httpClient *http.Client
}

// New constructs a TracingFetcher using the JAEGER_URL and TEMPO_URL environment
// variables. Either or both may be set; at runtime the fetcher tries whichever
// is configured.
func New() *TracingFetcher {
	jaeger := os.Getenv("JAEGER_URL")
	tempo := os.Getenv("TEMPO_URL")
	return &TracingFetcher{
		jaegerURL: jaeger,
		tempoURL:  tempo,
		httpClient: &http.Client{
			Timeout: tracingHTTPTimeout,
		},
	}
}

// SetJaegerURL overrides the Jaeger base URL (useful in tests).
func (f *TracingFetcher) SetJaegerURL(u string) { f.jaegerURL = u }

// SetTempoURL overrides the Tempo base URL (useful in tests).
func (f *TracingFetcher) SetTempoURL(u string) { f.tempoURL = u }

// ID implements EvidenceFetcher.
func (f *TracingFetcher) ID() fetchers.FetcherID { return fetchers.FetcherID("distributed_traces") }

// DependsOn implements EvidenceFetcher — no upstream dependencies.
func (f *TracingFetcher) DependsOn() []fetchers.FetcherID { return nil }

// SourceName implements EvidenceFetcher.
func (f *TracingFetcher) SourceName() string { return "tracing" }

// FetchHistorical queries Jaeger and/or Tempo for failed traces in the 30 minutes
// leading up to req.IncidentStartAt and converts them into Evidence entries.
func (f *TracingFetcher) FetchHistorical(ctx context.Context, req *fetchers.FetchRequest) (*fetchers.FetchResult, error) {
	start := time.Now()

	svc := f.extractServiceName(req)
	if svc == "" {
		return &fetchers.FetchResult{
			Status:     domain.FetchMissing,
			DurationMs: msSince(start),
		}, nil
	}

	var allTraces []failedTrace

	// Try Jaeger first.
	if f.jaegerURL != "" {
		traces, err := f.queryJaeger(ctx, svc)
		if err == nil {
			allTraces = append(allTraces, traces...)
		}
	}

	// Try Tempo (always attempt if configured; provides complementary data).
	if f.tempoURL != "" {
		traces, err := f.queryTempo(ctx, svc)
		if err == nil {
			allTraces = append(allTraces, traces...)
		}
	}

	if len(allTraces) == 0 {
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
		if parsed, err := uuid.Parse(req.RunID); err == nil {
			runID = parsed
		}
	}

	evidences := make([]*domain.Evidence, 0, len(allTraces))
	for i, t := range allTraces {
		desc := fmt.Sprintf("Failed trace: %s, duration: %dms", t.TraceID, t.DurationMs)

		idKey := fmt.Sprintf("%s::distributed_traces::%s::%s::trace_%d", invID, svc, t.TraceID, i)
		payload, _ := json.Marshal(map[string]interface{}{
			"trace_id":    t.TraceID,
			"service":     svc,
			"duration_ms": t.DurationMs,
			"source":      t.Source,
			"spans":       t.SpanCount,
		})

		ev := domain.NewEvidence(
			invID,
			runID,
			string(f.ID()),
			idKey,
			domain.TypeKubeSenseAPMRegression,
			f.SourceName(),
			domain.RoleContext,
			0.60,
			0.85,
			desc,
			domain.TemporalHistorical,
			payload,
		)
		evidences = append(evidences, ev)
	}

	return &fetchers.FetchResult{
		Evidence:   evidences,
		Status:     domain.FetchSuccess,
		DurationMs: msSince(start),
	}, nil
}

// ── internal helpers ───────────────────────────────────────────────────────────

// extractServiceName derives the service name from the request.
// Uses K8sResourceName (mapped to service) from EntityProfile first, then falls
// back to the last segment of TopologyPath.
func (f *TracingFetcher) extractServiceName(req *fetchers.FetchRequest) string {
	if req.EntityProfile != nil && req.EntityProfile.ResourceName != "" {
		return req.EntityProfile.ResourceName
	}
	if req.Investigation != nil && req.Investigation.TopologyPath != "" {
		segments := strings.Split(strings.Trim(req.Investigation.TopologyPath, "/"), "/")
		if len(segments) > 0 {
			return segments[len(segments)-1]
		}
	}
	return ""
}

// failedTrace is an internal representation of a failed trace from either backend.
type failedTrace struct {
	TraceID    string
	DurationMs int64
	SpanCount  int
	Source     string // "jaeger" or "tempo"
}

// ── Jaeger ────────────────────────────────────────────────────────────────────

// jaegerTracesResponse is a minimal unmarshal target for the Jaeger /api/traces response.
type jaegerTracesResponse struct {
	Data []struct {
		TraceID string `json:"traceID"`
		Spans   []struct {
			SpanID        string `json:"spanID"`
			Duration      int64  `json:"duration"` // microseconds
			Tags          []struct {
				Key   string      `json:"key"`
				Value interface{} `json:"value"`
			} `json:"tags"`
		} `json:"spans"`
	} `json:"data"`
}

// queryJaeger fetches failed traces from a Jaeger instance.
// GET {jaegerURL}/api/traces?service=<svc>&limit=5&tags={"error":"true"}&lookback=30m
func (f *TracingFetcher) queryJaeger(ctx context.Context, svc string) ([]failedTrace, error) {
	tagsJSON := `{"error":"true"}`
	params := url.Values{}
	params.Set("service", svc)
	params.Set("limit", fmt.Sprintf("%d", traceLimit))
	params.Set("tags", tagsJSON)
	params.Set("lookback", traceLookback)

	endpoint := fmt.Sprintf("%s/api/traces?%s", strings.TrimRight(f.jaegerURL, "/"), params.Encode())

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("tracing/jaeger: building request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")

	resp, err := f.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("tracing/jaeger: GET traces: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("tracing/jaeger: reading response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tracing/jaeger: unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var parsed jaegerTracesResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("tracing/jaeger: parsing response: %w", err)
	}

	var out []failedTrace
	for _, t := range parsed.Data {
		// Sum span durations to compute total trace duration in milliseconds.
		var totalMicros int64
		for _, s := range t.Spans {
			totalMicros += s.Duration
		}
		out = append(out, failedTrace{
			TraceID:    t.TraceID,
			DurationMs: totalMicros / 1000,
			SpanCount:  len(t.Spans),
			Source:     "jaeger",
		})
	}
	return out, nil
}

// ── Tempo ─────────────────────────────────────────────────────────────────────

// tempoSearchResponse is a minimal unmarshal target for the Tempo /api/search response.
type tempoSearchResponse struct {
	Traces []struct {
		TraceID         string `json:"traceID"`
		DurationMs      int64  `json:"durationMs"`
		SpanSets        []struct{ Spans []interface{} `json:"spans"` } `json:"spanSets"`
		SpanSet         struct{ Spans []interface{} `json:"spans"` } `json:"spanSet"`
	} `json:"traces"`
}

// queryTempo fetches failed traces from a Grafana Tempo instance.
// GET {tempoURL}/api/search?tags=service.name%3D{svc}%20status%3Derror&limit=5
func (f *TracingFetcher) queryTempo(ctx context.Context, svc string) ([]failedTrace, error) {
	tagStr := fmt.Sprintf("service.name=%s status=error", svc)
	params := url.Values{}
	params.Set("tags", tagStr)
	params.Set("limit", fmt.Sprintf("%d", traceLimit))

	endpoint := fmt.Sprintf("%s/api/search?%s", strings.TrimRight(f.tempoURL, "/"), params.Encode())

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("tracing/tempo: building request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")

	resp, err := f.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("tracing/tempo: GET search: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("tracing/tempo: reading response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tracing/tempo: unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var parsed tempoSearchResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("tracing/tempo: parsing response: %w", err)
	}

	var out []failedTrace
	for _, t := range parsed.Traces {
		// Estimate span count from spanSet or spanSets.
		spanCount := 0
		if len(t.SpanSets) > 0 {
			for _, ss := range t.SpanSets {
				spanCount += len(ss.Spans)
			}
		} else {
			spanCount = len(t.SpanSet.Spans)
		}
		out = append(out, failedTrace{
			TraceID:    t.TraceID,
			DurationMs: t.DurationMs,
			SpanCount:  spanCount,
			Source:     "tempo",
		})
	}
	return out, nil
}

// msSince returns the elapsed time in milliseconds since t.
func msSince(t time.Time) int64 {
	return time.Since(t).Milliseconds()
}
