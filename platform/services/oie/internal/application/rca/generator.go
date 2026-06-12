package rca

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	appinv "github.com/aileron-platform/aileron/platform/services/oie/internal/application/investigation"
	domain_ev "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/evidence"
	domain_hyp "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/hypothesis"
	domain_rca "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/rca"
	hyp_templates "github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/hypothesis_templates"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/shared/metrics"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/shared/tracing"
	"go.opentelemetry.io/otel/attribute"
)

// Generator implements appinv.RCAGenerator.
// It reads the scored hypotheses, picks the winner, builds the causal chain,
// fetches the blast radius from the AlertHub Neo4j endpoint, generates a grounded
// narrative, and returns the complete RCA result.
type Generator struct {
	hypothesisRepo   domain_hyp.Repository
	evidenceRepo     domain_ev.Repository
	causalBuilder    *CausalChainBuilder
	narrator         *Narrator
	alertHubBaseURL  string
	httpClient       *http.Client
	logger           *slog.Logger
}

// NewGenerator constructs the RCA Generator with all dependencies.
func NewGenerator(
	hypothesisRepo domain_hyp.Repository,
	evidenceRepo domain_ev.Repository,
	causalBuilder *CausalChainBuilder,
	narrator *Narrator,
	alertHubBaseURL string,
	logger *slog.Logger,
) *Generator {
	return &Generator{
		hypothesisRepo:  hypothesisRepo,
		evidenceRepo:    evidenceRepo,
		causalBuilder:   causalBuilder,
		narrator:        narrator,
		alertHubBaseURL: alertHubBaseURL,
		httpClient:      &http.Client{Timeout: 8 * time.Second},
		logger:         logger,
	}
}

// Generate implements appinv.RCAGenerator.
// Called by the Manager after the HypothesisEngine has scored and ranked all hypotheses.
func (g *Generator) Generate(ctx context.Context, req *appinv.RCARequest) (*appinv.RCAResult, error) {
	ctx, span := tracing.Tracer().Start(ctx, "rca.generator.generate")
	defer span.End()

	span.SetAttributes(
		attribute.String("investigation.id", req.InvestigationID.String()),
		attribute.String("domain", req.Domain),
	)

	start := time.Now()

	// ── Step 1: Load all hypotheses and evidence ──────────────────────────────
	hypotheses, err := g.hypothesisRepo.GetByInvestigation(ctx, req.InvestigationID)
	if err != nil {
		return nil, fmt.Errorf("loading hypotheses: %w", err)
	}

	latestRunID, err := g.evidenceRepo.GetLatestRunID(ctx, req.InvestigationID)
	if err != nil {
		return nil, fmt.Errorf("getting latest run id: %w", err)
	}

	var evidence []*domain_ev.Evidence
	if latestRunID != uuid.Nil {
		evidence, err = g.evidenceRepo.GetByInvestigation(ctx, req.InvestigationID, &latestRunID)
		if err != nil {
			return nil, fmt.Errorf("loading evidence: %w", err)
		}
	}

	// ── Step 2: Select the winner ─────────────────────────────────────────────
	winner, err := g.hypothesisRepo.GetWinner(ctx, req.InvestigationID)
	if err != nil {
		return nil, fmt.Errorf("getting winner hypothesis: %w", err)
	}
	if winner == nil {
		// No winner: return an INSUFFICIENT_EVIDENCE result.
		return g.insufficientEvidenceResult(req, hypotheses, start), nil
	}

	span.SetAttributes(
		attribute.String("winner.type", winner.Type),
		attribute.Float64("winner.confidence", winner.Confidence),
	)

	// ── Step 3: Build causal chain ────────────────────────────────────────────
	// Get the root entity ID from the evidence (EIRS entity context fetcher).
	rootEntityID := extractRootEntityID(evidence)

	causalChain := g.causalBuilder.Build(ctx, winner, evidence, rootEntityID, req.IncidentStartedAt)
	causalChainJSON, _ := json.Marshal(causalChain)

	// ── Step 4: Fetch blast radius from AlertHub Neo4j endpoint ──────────────
	// Uses GET /api/v1/topology/blast-radius?topology_path=... (replaces OKG).
	// Falls back to nil when AlertHub URL is not configured or the query returns no data.
	var blastRadiusSummary *domain_rca.BlastRadiusSummary
	blastTarget := req.TopologyPath
	if blastTarget == "" {
		blastTarget = rootEntityID
	}
	if blastTarget != "" && g.alertHubBaseURL != "" {
		blastRadiusSummary = g.fetchBlastRadius(ctx, blastTarget)
	}
	blastRadiusJSON, _ := json.Marshal(blastRadiusSummary)

	// ── Step 5: Build pre-incident timeline ───────────────────────────────────
	incidentStartAt := req.IncidentStartedAt
	timeline := g.causalBuilder.BuildTimeline(evidence, incidentStartAt)

	// ── Step 6: Generate narrative with citations (evidence-gated LLM) ──────────
	narrative, narrativeModel, citationRefs := g.narrator.GenerateWithCitations(
		ctx,
		winner,
		hypotheses,
		evidence,
		req.Domain, // Used as "incident title" fallback when the incident title isn't in context.
	)

	// ── Step 7: Build rejected hypotheses summary ─────────────────────────────
	rejected := make([]domain_rca.RejectedHypothesisSummary, 0)
	for _, h := range hypotheses {
		if h.ID == winner.ID {
			continue
		}
		rh := domain_rca.RejectedHypothesisSummary{
			Type:       h.Type,
			Title:      h.Title,
			Status:     string(h.Status),
			Confidence: h.Confidence,
		}
		if h.RejectionReason != nil {
			rh.RejectionReason = *h.RejectionReason
		}
		rejected = append(rejected, rh)
	}

	// ── Step 8: Build recommended actions from winning template ───────────────
	actions := buildActions(winner.Type)

	elapsed := time.Since(start)
	metrics.InvestigationDuration.WithLabelValues(winner.Type, "rca_generated").Observe(elapsed.Seconds())

	g.logger.InfoContext(ctx, "rca generation complete",
		"investigation_id", req.InvestigationID,
		"winner", winner.Type,
		"confidence", fmt.Sprintf("%.2f", winner.Confidence),
		"band", winner.Band,
		"causal_chain_nodes", len(causalChain.Nodes),
		"narrative_model", narrativeModel,
		"elapsed_ms", elapsed.Milliseconds(),
	)

	// ── Compose the final result ───────────────────────────────────────────────
	report := &domain_rca.Report{
		InvestigationID:       req.InvestigationID,
		IncidentID:            req.IncidentID,
		GeneratedAt:           time.Now().UTC(),
		ElapsedMs:             int(elapsed.Milliseconds()),
		WinningHypothesisID:   winner.ID,
		WinningHypothesisType: winner.Type,
		Confidence:            winner.Confidence,
		ConfidenceBand:        winner.Band,
		Summary:               narrativeSummaryFor(winner),
		RejectedHypotheses:    rejected,
		CausalChain:           causalChain,
		BlastRadius:           blastRadiusSummary,
		Narrative:             narrative,
		NarrativeModel:        narrativeModel,
		NarrativeAt:           time.Now().UTC(),
		PreIncidentTimeline:   timeline,
		RecommendedActions:    actions,
	}

	reportJSON, _ := json.Marshal(report)

	// Convert narrator CitationRef → appinv.CitationRef.
	citations := make([]appinv.CitationRef, 0, len(citationRefs))
	for _, c := range citationRefs {
		citations = append(citations, appinv.CitationRef{
			Source:       c.Source,
			EvidenceType: c.EvidenceType,
			Description:  c.Description,
		})
	}

	return &appinv.RCAResult{
		WinningHypothesisID: winner.ID,
		Confidence:          winner.Confidence,
		ConfidenceBand:      string(winner.Band),
		Summary:             report.Summary,
		CausalChain:         causalChainJSON,
		BlastRadius:         blastRadiusJSON,
		Narrative:           narrative,
		NarrativeModel:      narrativeModel,
		Citations:           citations,
		// Attach the full report JSON for the investigation's metadata field.
		FullReportJSON: reportJSON,
	}, nil
}

// ── Blast radius via AlertHub Neo4j endpoint ─────────────────────────────────
// Replaces the undeployed OKG service. AlertHub exposes this via its own Neo4j driver.

func (g *Generator) fetchBlastRadius(ctx context.Context, topologyPath string) *domain_rca.BlastRadiusSummary {
	url := fmt.Sprintf("%s/api/v1/topology/blast-radius?topology_path=%s", g.alertHubBaseURL, topologyPath)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Accept", "application/json")

	resp, err := g.httpClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var summary domain_rca.BlastRadiusSummary
	if err := json.Unmarshal(body, &summary); err != nil {
		return nil
	}
	return &summary
}

// ── Insufficient evidence fallback ────────────────────────────────────────────

func (g *Generator) insufficientEvidenceResult(
	req *appinv.RCARequest,
	hypotheses []*domain_hyp.Hypothesis,
	start time.Time,
) *appinv.RCAResult {
	// Find the highest-confidence hypothesis even if it didn't meet the winner threshold.
	var bestHyp *domain_hyp.Hypothesis
	for _, h := range hypotheses {
		if bestHyp == nil || h.Confidence > bestHyp.Confidence {
			bestHyp = h
		}
	}

	if bestHyp == nil {
		return &appinv.RCAResult{
			WinningHypothesisID: uuid.New(),
			Confidence:          0.0,
			ConfidenceBand:      "INSUFFICIENT_EVIDENCE",
			Summary:             "Investigation completed. Insufficient evidence to determine root cause.",
			Narrative: "Investigation completed with insufficient evidence to determine root cause. " +
				"Manual operator investigation is required.",
			NarrativeModel: "template",
		}
	}

	return &appinv.RCAResult{
		WinningHypothesisID: bestHyp.ID,
		Confidence:          bestHyp.Confidence,
		ConfidenceBand:      "INSUFFICIENT_EVIDENCE",
		Summary: fmt.Sprintf("Most likely: %s (confidence %.0f%%). Insufficient evidence for automated conclusion.",
			bestHyp.Title, bestHyp.Confidence*100),
		Narrative: fmt.Sprintf("Investigation completed. The most consistent explanation is: %s "+
			"(confidence: %.0f%%), however sufficient evidence was not gathered to confirm this automatically. "+
			"Missing evidence: %v. Manual investigation is required.",
			bestHyp.Title, bestHyp.Confidence*100, bestHyp.MissingRequiredEvidence),
		NarrativeModel: "template",
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// extractRootEntityID finds the EIRS-resolved entity ID from evidence.
func extractRootEntityID(evidence []*domain_ev.Evidence) string {
	for _, ev := range evidence {
		if ev.EvidenceType != domain_ev.TypeEntityContext {
			continue
		}
		var payload struct {
			CanonicalEntityID string `json:"canonical_entity_id"`
		}
		if err := json.Unmarshal(ev.Payload, &payload); err == nil {
			return payload.CanonicalEntityID
		}
	}
	return ""
}

// narrativeSummaryFor generates a brief (1-sentence) machine-generated summary
// for the winning hypothesis. Used as fallback when LLM is not called.
func narrativeSummaryFor(winner *domain_hyp.Hypothesis) string {
	return fmt.Sprintf("%s — %s (confidence: %.0f%%)",
		winner.Title, winner.Description, winner.Confidence*100)
}

// buildActions extracts recommended actions from the winning hypothesis template.
func buildActions(hypothesisType string) []domain_rca.RecommendedAction {
	for _, tmpl := range hyp_templates.All() {
		if tmpl.Type != hypothesisType {
			continue
		}
		actions := make([]domain_rca.RecommendedAction, 0, len(tmpl.DefaultActions))
		for _, a := range tmpl.DefaultActions {
			actions = append(actions, domain_rca.RecommendedAction{
				Title:               a.Title,
				Steps:               a.Steps,
				Urgency:             a.Urgency,
				EstimatedMinutes:    a.EstimatedMinutes,
				SafetyLevel:         a.SafetyLevel,
				ConfirmationMessage: a.ConfirmationMessage,
			})
		}
		return actions
	}
	return nil
}
