package loki

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
	defaultLokiURL = "http://loki:3100"
	httpTimeout    = 8 * time.Second
	lookback       = 30 * time.Minute
	logLineLimit   = 10
	logLineMaxLen  = 200
)

// LokiFetcher queries a Loki instance for error/exception log lines relevant
// to the investigation entity.
type LokiFetcher struct {
	lokiURL    string
	httpClient *http.Client
}

// New constructs a LokiFetcher using the LOKI_URL environment variable.
// Falls back to "http://loki:3100" when the variable is unset.
func New() *LokiFetcher {
	u := os.Getenv("LOKI_URL")
	if u == "" {
		u = defaultLokiURL
	}
	return &LokiFetcher{
		lokiURL: u,
		httpClient: &http.Client{
			Timeout: httpTimeout,
		},
	}
}

// SetURL overrides the Loki base URL (useful in tests).
func (f *LokiFetcher) SetURL(u string) {
	f.lokiURL = u
}

// ID implements EvidenceFetcher.
func (f *LokiFetcher) ID() fetchers.FetcherID { return fetchers.FetcherID("loki_logs") }

// DependsOn implements EvidenceFetcher — no upstream dependencies.
func (f *LokiFetcher) DependsOn() []fetchers.FetcherID { return nil }

// SourceName implements EvidenceFetcher.
func (f *LokiFetcher) SourceName() string { return "loki" }

// FetchHistorical queries Loki for error log lines in the 30 minutes leading
// up to req.IncidentStartAt and converts them into Evidence entries.
func (f *LokiFetcher) FetchHistorical(ctx context.Context, req *fetchers.FetchRequest) (*fetchers.FetchResult, error) {
	start := time.Now()

	ns, pod := f.extractEntityCoords(req)
	if ns == "" {
		return &fetchers.FetchResult{
			Status:     domain.FetchMissing,
			DurationMs: msSince(start),
		}, nil
	}

	lines, err := f.queryLoki(ctx, ns, pod, req.IncidentStartAt)
	if err != nil {
		return &fetchers.FetchResult{
			Status:     domain.FetchError,
			FetchError: err,
			DurationMs: msSince(start),
		}, nil
	}

	if len(lines) == 0 {
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

	evidences := make([]*domain.Evidence, 0, len(lines))
	for i, line := range lines {
		truncated := line
		if len(truncated) > logLineMaxLen {
			truncated = truncated[:logLineMaxLen]
		}
		desc := "Log: " + truncated

		idKey := fmt.Sprintf("%s::loki_logs::%s::%s::log_%d", invID, ns, pod, i)
		payload, _ := json.Marshal(map[string]string{
			"namespace": ns,
			"pod":       pod,
			"line":      line,
		})

		ev := domain.NewEvidence(
			invID,
			runID,
			string(f.ID()),
			idKey,
			domain.TypeK8sPodLog,
			f.SourceName(),
			domain.RoleContext,
			0.50,
			0.80,
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

// ── internal helpers ──────────────────────────────────────────────────────────

// extractEntityCoords derives namespace and pod name from the request.
// Namespace is read from EntityProfile first, then TopologyPath.
// Pod is read from EntityProfile first, then the last segment of TopologyPath.
func (f *LokiFetcher) extractEntityCoords(req *fetchers.FetchRequest) (ns, pod string) {
	if req.EntityProfile != nil {
		ns = req.EntityProfile.K8sNamespace
		pod = req.EntityProfile.ResourceName
	}

	if req.Investigation != nil && req.Investigation.TopologyPath != "" {
		segments := strings.Split(strings.Trim(req.Investigation.TopologyPath, "/"), "/")

		if ns == "" {
			// Topology paths commonly take the form  cluster/namespace/kind/name.
			// Treat the second segment (index 1) as the namespace when present.
			if len(segments) >= 2 {
				ns = segments[1]
			}
		}

		if pod == "" && len(segments) > 0 {
			pod = segments[len(segments)-1]
		}
	}

	return ns, pod
}

// lokiQueryRangeResponse is a minimal unmarshal target for the Loki query_range API.
type lokiQueryRangeResponse struct {
	Data struct {
		Result []struct {
			Stream map[string]string `json:"stream"`
			Values [][]string        `json:"values"` // [[timestamp_ns, line], ...]
		} `json:"result"`
	} `json:"data"`
}

// queryLoki performs a Loki query_range request and returns the matched log lines.
func (f *LokiFetcher) queryLoki(ctx context.Context, ns, pod string, incidentAt time.Time) ([]string, error) {
	endTime := incidentAt
	if endTime.IsZero() {
		endTime = time.Now().UTC()
	}
	startTime := endTime.Add(-lookback)

	var logql string
	if pod != "" {
		logql = fmt.Sprintf(`{namespace=%q,pod=~%q} |~ "error|ERROR|exception|FATAL"`, ns, pod+".*")
	} else {
		logql = fmt.Sprintf(`{namespace=%q} |~ "error|ERROR|exception|FATAL"`, ns)
	}

	params := url.Values{}
	params.Set("query", logql)
	params.Set("start", fmt.Sprintf("%d", startTime.UnixNano()))
	params.Set("end", fmt.Sprintf("%d", endTime.UnixNano()))
	params.Set("limit", fmt.Sprintf("%d", logLineLimit))

	endpoint := fmt.Sprintf("%s/loki/api/v1/query_range?%s", strings.TrimRight(f.lokiURL, "/"), params.Encode())

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("loki: building request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")

	resp, err := f.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("loki: GET query_range: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("loki: reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("loki: unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var parsed lokiQueryRangeResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("loki: parsing response: %w", err)
	}

	var lines []string
	for _, stream := range parsed.Data.Result {
		for _, kv := range stream.Values {
			if len(kv) >= 2 {
				lines = append(lines, kv[1])
			}
		}
	}
	return lines, nil
}

// msSince returns the elapsed time in milliseconds since t.
func msSince(t time.Time) int64 {
	return time.Since(t).Milliseconds()
}
