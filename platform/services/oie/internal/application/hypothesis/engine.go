package hypothesis

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	appinv "github.com/aileron-platform/aileron/platform/services/oie/internal/application/investigation"
	domain_ev "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/evidence"
	domain_hyp "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/hypothesis"
	hyp_templates "github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/hypothesis_templates"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/shared/metrics"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/shared/tracing"
	"go.opentelemetry.io/otel/attribute"
)

// Engine implements appinv.HypothesisEngine.
// It selects applicable templates, scores each against the gathered evidence,
// persists the hypotheses, and returns a summary.
type Engine struct {
	templates  map[string]*domain_hyp.HypothesisTemplate // keyed by Type
	repo       domain_hyp.Repository
	evidenceRepo domain_ev.Repository
	scorer     *Scorer
	logger     *slog.Logger
}

// NewEngine constructs the HypothesisEngine with all required dependencies.
func NewEngine(
	repo domain_hyp.Repository,
	evidenceRepo domain_ev.Repository,
	logger *slog.Logger,
) *Engine {
	// Load built-in templates and index by Type.
	templates := make(map[string]*domain_hyp.HypothesisTemplate)
	for _, t := range hyp_templates.All() {
		templates[t.Type] = t
	}
	return &Engine{
		templates:    templates,
		repo:         repo,
		evidenceRepo: evidenceRepo,
		scorer:       &Scorer{},
		logger:       logger,
	}
}

// Evaluate implements appinv.HypothesisEngine.
// Selects applicable templates, scores each, persists, and returns counts.
func (e *Engine) Evaluate(ctx context.Context, req *appinv.HypothesisRequest) (*appinv.HypothesisResult, error) {
	ctx, span := tracing.Tracer().Start(ctx, "hypothesis.engine.evaluate")
	defer span.End()

	span.SetAttributes(
		attribute.String("investigation.id", req.InvestigationID.String()),
		attribute.String("playbook.id", req.PlaybookID),
	)

	// Load all evidence for this investigation (latest run only).
	latestRunID, err := e.evidenceRepo.GetLatestRunID(ctx, req.InvestigationID)
	if err != nil {
		return nil, fmt.Errorf("getting latest run id: %w", err)
	}

	var evidence []*domain_ev.Evidence
	if latestRunID != uuid.Nil {
		evidence, err = e.evidenceRepo.GetByInvestigation(ctx, req.InvestigationID, &latestRunID)
		if err != nil {
			return nil, fmt.Errorf("loading evidence: %w", err)
		}
	}

	e.logger.InfoContext(ctx, "hypothesis evaluation starting",
		"investigation_id", req.InvestigationID,
		"evidence_count", len(evidence),
		"playbook_id", req.PlaybookID,
	)

	// Resolve which hypothesis types apply to this playbook.
	applicableTypes, ok := hyp_templates.ByPlaybook[req.PlaybookID]
	if !ok || len(applicableTypes) == 0 {
		applicableTypes = hyp_templates.ByPlaybook["PB-DEFAULT-001"]
	}

	// Also include any types applicable by entity_type + failure_class
	// regardless of playbook (catches edge cases where playbook is generic).
	typeSet := make(map[string]struct{}, len(applicableTypes))
	for _, t := range applicableTypes {
		typeSet[t] = struct{}{}
	}
	for _, tmpl := range e.templates {
		if matchesScope(tmpl, req.RootEntityType, req.FailureClass, req.Domain) {
			typeSet[tmpl.Type] = struct{}{}
		}
	}

	// Score each applicable hypothesis template.
	now := time.Now().UTC()
	hypotheses := make([]*domain_hyp.Hypothesis, 0, len(typeSet))

	for hypType := range typeSet {
		tmpl, ok := e.templates[hypType]
		if !ok {
			e.logger.WarnContext(ctx, "hypothesis template not found", "type", hypType)
			continue
		}

		scoreResult := e.scorer.Score(tmpl, evidence)

		h := &domain_hyp.Hypothesis{
			ID:              uuid.New(),
			InvestigationID: req.InvestigationID,
			Type:            tmpl.Type,
			Title:           tmpl.Name,
			Description:     tmpl.Description,
			Status:          scoreResult.Status,
			Confidence:      scoreResult.Confidence,
			Band:            scoreResult.Band,

			BaseScore:              scoreResult.BaseScore,
			EvidenceBoost:          scoreResult.EvidenceBoost,
			ContradictionPenalty:   scoreResult.ContradictionPenalty,
			TopologyAdjustment:     0.0, // Set by Module 4 (RCA Generator)
			HistoricalAdjustment:   0.0, // Set by calibration job

			SupportingEvidenceCount:    scoreResult.SupportingCount,
			ContradictingEvidenceCount: scoreResult.ContradictingCount,
			MissingRequiredEvidence:    scoreResult.MissingRequired,

			RejectionReason: scoreResult.RejectionReason,

			CreatedAt: now,
			UpdatedAt: now,
		}

		if scoreResult.Status == domain_hyp.StatusRejected {
			e.logger.DebugContext(ctx, "hypothesis rejected",
				"type", hypType,
				"reason", derefString(scoreResult.RejectionReason),
			)
			metrics.HypothesisRejected.WithLabelValues(hypType, "hard_rejection").Inc()
		} else if scoreResult.Status == domain_hyp.StatusInsufficientEvidence {
			metrics.HypothesisRejected.WithLabelValues(hypType, "insufficient_evidence").Inc()
		}

		metrics.HypothesisConfidence.WithLabelValues(hypType, string(scoreResult.Status)).Observe(scoreResult.Confidence)

		hypotheses = append(hypotheses, h)
	}

	// Rank all hypotheses: active first, then by confidence descending.
	RankHypotheses(hypotheses)

	// Count rejections.
	rejected := 0
	for _, h := range hypotheses {
		if h.Status == domain_hyp.StatusRejected || h.Status == domain_hyp.StatusInsufficientEvidence {
			rejected++
		}
	}

	// Persist all hypotheses in one batch.
	if err := e.repo.BulkCreate(ctx, hypotheses); err != nil {
		e.logger.ErrorContext(ctx, "failed to persist hypotheses",
			"investigation_id", req.InvestigationID, "error", err)
		// Non-fatal: we have in-memory results.
	}

	winner := WinnerFrom(hypotheses)
	if winner != nil {
		e.logger.InfoContext(ctx, "hypothesis evaluation complete",
			"investigation_id", req.InvestigationID,
			"winner_type", winner.Type,
			"winner_confidence", fmt.Sprintf("%.2f", winner.Confidence),
			"winner_band", winner.Band,
			"total", len(hypotheses),
			"rejected", rejected,
		)
	} else {
		e.logger.WarnContext(ctx, "hypothesis evaluation: no winner",
			"investigation_id", req.InvestigationID,
			"total", len(hypotheses),
			"rejected", rejected,
		)
	}

	return &appinv.HypothesisResult{
		Generated: len(hypotheses),
		Rejected:  rejected,
	}, nil
}

// matchesScope returns true when a template is applicable to the given scope.
func matchesScope(tmpl *domain_hyp.HypothesisTemplate, entityType, failureClass, domain string) bool {
	if entityType != "" {
		for _, t := range tmpl.ApplicableEntityTypes {
			if t == entityType {
				goto checkFailureClass
			}
		}
		return false
	}
checkFailureClass:
	if failureClass != "" && len(tmpl.ApplicableFailureClasses) > 0 {
		for _, fc := range tmpl.ApplicableFailureClasses {
			if fc == failureClass {
				return true
			}
		}
		return false
	}
	if domain != "" && len(tmpl.ApplicableDomains) > 0 {
		for _, d := range tmpl.ApplicableDomains {
			if d == domain {
				return true
			}
		}
	}
	return entityType != ""
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
