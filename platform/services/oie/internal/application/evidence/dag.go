package evidence

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	domain_evidence "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/evidence"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/circuitbreaker"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/fetchers"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/shared/metrics"
)

// DAG executes evidence fetchers respecting their declared dependencies.
// Fetchers with no unmet dependencies run concurrently.
// Fetchers that declare DependsOn() wait until all dependencies complete.
// The approach is channel-based: a completion channel drives scheduling.
type DAG struct {
	fetchers map[fetchers.FetcherID]fetchers.EvidenceFetcher
	cb       *circuitbreaker.CircuitBreaker
	logger   *slog.Logger
}

// NewDAG constructs a DAG with the given fetcher registry.
func NewDAG(
	fetcherMap map[fetchers.FetcherID]fetchers.EvidenceFetcher,
	cb *circuitbreaker.CircuitBreaker,
	logger *slog.Logger,
) *DAG {
	return &DAG{fetchers: fetcherMap, cb: cb, logger: logger}
}

// Execute runs all requested fetchers and returns all gathered evidence.
// Returns (evidence, timedOut).
// When the context deadline is reached, in-flight fetchers are cancelled
// and partial results are returned.
func (d *DAG) Execute(ctx context.Context, req *fetchers.FetchRequest, ids []fetchers.FetcherID) ([]*domain_evidence.Evidence, bool) {
	if len(ids) == 0 {
		return nil, false
	}

	type fetcherResult struct {
		id     fetchers.FetcherID
		result *fetchers.FetchResult
	}

	results := make(map[fetchers.FetcherID]*fetchers.FetchResult, len(ids))
	var resultsMu sync.Mutex

	launched := make(map[fetchers.FetcherID]bool, len(ids))
	completed := make(chan fetchers.FetcherID, len(ids))
	var wg sync.WaitGroup

	launch := func(id fetchers.FetcherID) {
		f, ok := d.fetchers[id]
		if !ok {
			d.logger.Warn("fetcher not registered", "fetcher_id", id)
			resultsMu.Lock()
			results[id] = &fetchers.FetchResult{Status: domain_evidence.FetchMissing}
			resultsMu.Unlock()
			completed <- id
			return
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { completed <- id }()

			// Circuit breaker check.
			if !d.cb.Allow(f.SourceName()) {
				d.logger.Info("circuit open for source — skipping fetcher",
					"fetcher_id", id, "source", f.SourceName())
				resultsMu.Lock()
				results[id] = &fetchers.FetchResult{
					Status: domain_evidence.FetchCircuitOpen,
					Evidence: []*domain_evidence.Evidence{
						domain_evidence.NewCircuitOpenEvidence(
							req.Investigation.ID,
							parseRunID(req.RunID),
							string(id),
							f.SourceName(),
						),
					},
				}
				resultsMu.Unlock()
				metrics.EvidenceFetchTotal.WithLabelValues(string(id), "circuit_open").Inc()
				return
			}

			// Build the request with upstream results injected.
			resultsMu.Lock()
			reqCopy := *req
			reqCopy.UpstreamResults = make(map[fetchers.FetcherID]*fetchers.FetchResult, len(f.DependsOn()))
			for _, dep := range f.DependsOn() {
				if r, ok := results[dep]; ok {
					reqCopy.UpstreamResults[dep] = r
				}
			}
			resultsMu.Unlock()

			start := time.Now()
			result, err := f.FetchHistorical(ctx, &reqCopy)
			elapsed := time.Since(start)

			if err != nil {
				d.cb.RecordFailure(f.SourceName())
				result = &fetchers.FetchResult{Status: domain_evidence.FetchError, FetchError: err}
				metrics.EvidenceFetchTotal.WithLabelValues(string(id), "error").Inc()
			} else {
				d.cb.RecordSuccess(f.SourceName())
				metrics.EvidenceFetchTotal.WithLabelValues(string(id), "success").Inc()
			}

			if result == nil {
				result = &fetchers.FetchResult{Status: domain_evidence.FetchMissing}
			}
			result.DurationMs = elapsed.Milliseconds()

			// Tag each evidence piece with timing info and ensure ID is set.
			for _, ev := range result.Evidence {
				if ev.ID == (uuid.UUID{}) {
					ev.ID = uuid.New()
				}
				ev.FetcherID = string(id)
				ev.FetchDurationMs = int(elapsed.Milliseconds())
				gapSecs := int(time.Since(req.IncidentStartAt).Seconds())
				ev.TemporalGapSecs = &gapSecs
			}

			metrics.EvidenceFetchDuration.WithLabelValues(string(id), string(result.Status)).
				Observe(elapsed.Seconds())

			resultsMu.Lock()
			results[id] = result
			resultsMu.Unlock()
		}()
	}

	// Initial scheduling pass: launch fetchers with no unmet dependencies.
	schedule := func() {
		for _, id := range ids {
			if launched[id] {
				continue
			}
			f, ok := d.fetchers[id]
			if !ok {
				launched[id] = true
				launch(id)
				continue
			}
			allReady := true
			for _, dep := range f.DependsOn() {
				resultsMu.Lock()
				_, done := results[dep]
				resultsMu.Unlock()
				if !done {
					allReady = false
					break
				}
			}
			if allReady {
				launched[id] = true
				launch(id)
			}
		}
	}

	schedule()

	remaining := len(ids)
	timedOut := false
	for remaining > 0 {
		select {
		case <-ctx.Done():
			timedOut = true
			// Mark all not-yet-completed fetchers as timed out.
			resultsMu.Lock()
			for _, id := range ids {
				if _, done := results[id]; !done {
					results[id] = &fetchers.FetchResult{Status: domain_evidence.FetchTimeout}
					metrics.EvidenceFetchTotal.WithLabelValues(string(id), "timeout").Inc()
				}
			}
			resultsMu.Unlock()
			// Wait for any in-flight goroutines to exit (context already cancelled).
			wg.Wait()
			goto done
		case completedID := <-completed:
			remaining--
			_ = completedID
			schedule() // Check if new fetchers can now start.
		}
	}
done:

	// Collect all evidence from all results.
	var allEvidence []*domain_evidence.Evidence
	resultsMu.Lock()
	for _, result := range results {
		allEvidence = append(allEvidence, result.Evidence...)
	}
	resultsMu.Unlock()

	return allEvidence, timedOut
}

func parseRunID(s string) (id [16]byte) {
	// uuid.Parse returns an array; we do a best-effort parse here.
	for i := 0; i < 16 && i < len(s); i++ {
		id[i] = s[i]
	}
	return
}
