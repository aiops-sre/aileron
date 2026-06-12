package okg

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	domain "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/evidence"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/fetchers"
)

// ChangesFetcher fetches change correlations from the OKG service.
// It queries OKG for changes that preceded this incident and have been
// scored with a causality estimate.
type ChangesFetcher struct {
	okgBaseURL string
	httpClient *http.Client
}

func NewChangesFetcher(okgBaseURL string) *ChangesFetcher {
	return &ChangesFetcher{
		okgBaseURL: okgBaseURL,
		httpClient: &http.Client{Timeout: 8 * time.Second},
	}
}

func (f *ChangesFetcher) ID() fetchers.FetcherID          { return "okg_changes" }
func (f *ChangesFetcher) DependsOn() []fetchers.FetcherID { return nil }
func (f *ChangesFetcher) SourceName() string              { return "okg" }

func (f *ChangesFetcher) FetchHistorical(ctx context.Context, req *fetchers.FetchRequest) (*fetchers.FetchResult, error) {
	if f.okgBaseURL == "" {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}

	url := fmt.Sprintf("%s/api/v1/incidents/%s/changes?lookback_minutes=120&min_causality_score=0.40",
		f.okgBaseURL, req.Investigation.IncidentID)

	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return &fetchers.FetchResult{Status: domain.FetchError, FetchError: err}, nil
	}
	httpReq.Header.Set("Accept", "application/json")

	resp, err := f.httpClient.Do(httpReq)
	if err != nil {
		return &fetchers.FetchResult{Status: domain.FetchError, FetchError: err}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return &fetchers.FetchResult{
			Status:     domain.FetchError,
			FetchError: fmt.Errorf("OKG returned HTTP %d", resp.StatusCode),
		}, nil
	}

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Changes []struct {
			ChangeID       string    `json:"change_id"`
			ChangeType     string    `json:"change_type"`
			Title          string    `json:"title"`
			Service        string    `json:"service"`
			FromVersion    string    `json:"from_version"`
			ToVersion      string    `json:"to_version"`
			AuthorDisplay  string    `json:"author_display"`
			OccurredAt     time.Time `json:"occurred_at"`
			DeltaMinutes   int       `json:"delta_minutes"`
			CausalityScore float64   `json:"causality_score"`
			RiskLevel      string    `json:"risk_level"`
			SourceURL      string    `json:"source_url"`
			Status         string    `json:"status"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return &fetchers.FetchResult{Status: domain.FetchError, FetchError: err}, nil
	}

	var evidence []*domain.Evidence
	for _, c := range result.Changes {
		payload, _ := json.Marshal(domain.ChangeEvidencePayload{
			ChangeID:       c.ChangeID,
			ChangeType:     c.ChangeType,
			Title:          c.Title,
			Service:        c.Service,
			FromVersion:    c.FromVersion,
			ToVersion:      c.ToVersion,
			AuthorDisplay:  c.AuthorDisplay,
			OccurredAt:     c.OccurredAt,
			DeltaMinutes:   c.DeltaMinutes,
			CausalityScore: c.CausalityScore,
			RiskLevel:      c.RiskLevel,
			SourceURL:      c.SourceURL,
		})

		evType := domain.TypeChangeDeployment
		if c.ChangeType == "config_change" {
			evType = domain.TypeChangeConfig
		}

		role := domain.RoleContext
		weight := 0.0
		group := "deployment_signals"

		if c.CausalityScore >= 0.70 {
			role = domain.RoleSupports
			weight = min64(0.90, c.CausalityScore)
		} else if c.CausalityScore >= 0.45 {
			role = domain.RoleSupports
			weight = c.CausalityScore
		}

		ev := &domain.Evidence{
			EvidenceType:       evType,
			Source:             "okg",
			Role:               role,
			Weight:             weight,
			EvidenceGroup:      &group,
			TemporalMode:       domain.TemporalHistorical,
			AsOfTime:           &c.OccurredAt,
			OccurredAt:         &c.OccurredAt,
			Description:        fmt.Sprintf("%s '%s' deployed %d minutes before incident (causality: %.0f%%)", c.ChangeType, c.Title, c.DeltaMinutes, c.CausalityScore*100),
			Payload:            payload,
			GatheredAt:         time.Now().UTC(),
			EvidenceConfidence: 0.85,
			FetchStatus:        domain.FetchSuccess,
			CreatedAt:          time.Now().UTC(),
		}
		evidence = append(evidence, ev)
	}

	return &fetchers.FetchResult{Evidence: evidence, Status: domain.FetchSuccess}, nil
}

// SimilarIncidentsFetcher fetches similar historical incidents from OKG.
type SimilarIncidentsFetcher struct {
	okgBaseURL string
	httpClient *http.Client
}

func NewSimilarIncidentsFetcher(okgBaseURL string) *SimilarIncidentsFetcher {
	return &SimilarIncidentsFetcher{
		okgBaseURL: okgBaseURL,
		httpClient: &http.Client{Timeout: 8 * time.Second},
	}
}

func (f *SimilarIncidentsFetcher) ID() fetchers.FetcherID          { return "okg_similar_incidents" }
func (f *SimilarIncidentsFetcher) DependsOn() []fetchers.FetcherID { return nil }
func (f *SimilarIncidentsFetcher) SourceName() string              { return "okg" }

func (f *SimilarIncidentsFetcher) FetchHistorical(ctx context.Context, req *fetchers.FetchRequest) (*fetchers.FetchResult, error) {
	if f.okgBaseURL == "" {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}

	url := fmt.Sprintf("%s/api/v1/incidents/%s/similar?limit=5&min_similarity=0.65",
		f.okgBaseURL, req.Investigation.IncidentID)

	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return &fetchers.FetchResult{Status: domain.FetchError, FetchError: err}, nil
	}
	httpReq.Header.Set("Accept", "application/json")

	resp, err := f.httpClient.Do(httpReq)
	if err != nil {
		return &fetchers.FetchResult{Status: domain.FetchError, FetchError: err}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return &fetchers.FetchResult{Status: domain.FetchError,
			FetchError: fmt.Errorf("OKG returned HTTP %d", resp.StatusCode)}, nil
	}

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Incidents []struct {
			IncidentID     string    `json:"incident_id"`
			IncidentNumber string    `json:"incident_number"`
			Title          string    `json:"title"`
			Similarity     float64   `json:"similarity"`
			RootCause      string    `json:"root_cause"`
			HypothesisType string    `json:"hypothesis_type"`
			MTTRSeconds    int       `json:"mttr_seconds"`
			ResolvedWith   string    `json:"resolved_with"`
			OccurredAt     time.Time `json:"occurred_at"`
		} `json:"incidents"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return &fetchers.FetchResult{Status: domain.FetchError, FetchError: err}, nil
	}

	var evidence []*domain.Evidence
	for _, inc := range result.Incidents {
		payload, _ := json.Marshal(domain.SimilarIncidentPayload{
			IncidentID:     inc.IncidentID,
			IncidentNumber: inc.IncidentNumber,
			Title:          inc.Title,
			Similarity:     inc.Similarity,
			RootCause:      inc.RootCause,
			HypothesisType: inc.HypothesisType,
			MTTRSeconds:    inc.MTTRSeconds,
			ResolvedWith:   inc.ResolvedWith,
			OccurredAt:     inc.OccurredAt,
		})

		ev := &domain.Evidence{
			EvidenceType:       domain.TypeSimilarIncident,
			Source:             "okg",
			Role:               domain.RoleContext,
			Weight:             inc.Similarity * 0.60,
			TemporalMode:       domain.TemporalHistorical,
			OccurredAt:         &inc.OccurredAt,
			Description:        fmt.Sprintf("Similar incident %s (%.0f%% similar): %s — resolved with %s", inc.IncidentNumber, inc.Similarity*100, inc.Title, inc.ResolvedWith),
			Payload:            payload,
			GatheredAt:         time.Now().UTC(),
			EvidenceConfidence: 0.65,
			FetchStatus:        domain.FetchSuccess,
			CreatedAt:          time.Now().UTC(),
		}
		evidence = append(evidence, ev)
	}

	return &fetchers.FetchResult{Evidence: evidence, Status: domain.FetchSuccess}, nil
}

func min64(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
