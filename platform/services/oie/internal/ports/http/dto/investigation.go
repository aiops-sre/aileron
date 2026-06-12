package dto

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	domain "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/investigation"
)

type StartInvestigationRequest struct {
	IncidentID        uuid.UUID  `json:"incident_id" binding:"required"`
	IncidentNumber    string     `json:"incident_number"`
	IdempotencyKey    string     `json:"idempotency_key" binding:"required"`
	Severity          string     `json:"severity" binding:"required"`
	IncidentStartedAt time.Time  `json:"incident_started_at" binding:"required"`
	RootEntityID      *uuid.UUID `json:"root_entity_id,omitempty"`
	RootEntityType    *string    `json:"root_entity_type,omitempty"`
	FailureClass      *string    `json:"failure_class,omitempty"`
	Domain            *string    `json:"domain,omitempty"`
	PlaybookID        string     `json:"playbook_id,omitempty"`
	// Entity context from AlertHub topology_path — used without EIRS to route K8s fetchers.
	TopologyPath  string `json:"topology_path,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
}

type StartInvestigationResponse struct {
	InvestigationID uuid.UUID `json:"investigation_id"`
	Status          string    `json:"status"`
	AlreadyExisted  bool      `json:"already_existed"`
}

type InvestigationResponse struct {
	ID             uuid.UUID     `json:"id"`
	IncidentID     uuid.UUID     `json:"incident_id"`
	IncidentNumber string        `json:"incident_number"`
	Status         string        `json:"status"`
	StatusReason   string        `json:"status_reason,omitempty"`
	Severity       string        `json:"severity"`
	PlaybookID     string        `json:"playbook_id"`
	RootEntityType *string       `json:"root_entity_type,omitempty"`
	FailureClass   *string       `json:"failure_class,omitempty"`
	Domain         *string       `json:"domain,omitempty"`
	TimeBudgetMs   int           `json:"time_budget_ms"`
	ElapsedMs      *int          `json:"elapsed_ms,omitempty"`
	TriggeredAt    time.Time     `json:"triggered_at"`
	StartedAt      *time.Time    `json:"started_at,omitempty"`
	CompletedAt    *time.Time    `json:"completed_at,omitempty"`
	ReportVersion  int           `json:"report_version"`
	RCA            *RCAResult    `json:"rca,omitempty"`
	Progress       *ProgressInfo `json:"progress,omitempty"`
}

type RCAResult struct {
	Confidence          float64       `json:"confidence"`
	ConfidenceBand      string        `json:"confidence_band"`
	Summary             string        `json:"summary"`
	Narrative           *string       `json:"narrative,omitempty"`
	HypothesesGenerated int           `json:"hypotheses_generated"`
	HypothesesRejected  int           `json:"hypotheses_rejected"`
	EvidenceGathered    int           `json:"evidence_gathered"`
	EvidenceSources     []string      `json:"evidence_sources"`
	Citations           []CitationRef `json:"citations,omitempty"`
}

type CitationRef struct {
	Source       string `json:"source"`
	EvidenceType string `json:"evidence_type"`
	Description  string `json:"description"`
}

type ProgressInfo struct {
	ElapsedMs    int     `json:"elapsed_ms"`
	BudgetMs     int     `json:"budget_ms"`
	ProgressPct  float64 `json:"progress_pct"`
	CurrentPhase string  `json:"current_phase"`
}

type CancelInvestigationRequest struct {
	Reason string `json:"reason" binding:"required"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
	TraceID string `json:"trace_id,omitempty"`
}

type InvestigationEventResponse struct {
	SequenceNum int64     `json:"sequence_num"`
	EventType   string    `json:"event_type"`
	Payload     any       `json:"payload"`
	CreatedAt   time.Time `json:"created_at"`
}

// FromDomain converts an Investigation aggregate to its HTTP response.
func FromDomain(inv *domain.Investigation) *InvestigationResponse {
	resp := &InvestigationResponse{
		ID:             inv.ID,
		IncidentID:     inv.IncidentID,
		IncidentNumber: inv.IncidentNumber,
		Status:         string(inv.Status),
		StatusReason:   inv.StatusReason,
		Severity:       inv.Severity,
		PlaybookID:     inv.PlaybookID,
		RootEntityType: inv.RootEntityType,
		FailureClass:   inv.FailureClass,
		Domain:         inv.Domain,
		TimeBudgetMs:   inv.TimeBudgetMs,
		ElapsedMs:      inv.ElapsedMs,
		TriggeredAt:    inv.TriggeredAt,
		StartedAt:      inv.StartedAt,
		CompletedAt:    inv.CompletedAt,
		ReportVersion:  inv.ReportVersion,
	}

	if inv.Status == domain.StatusCompleted && inv.Confidence != nil {
		resp.RCA = &RCAResult{
			Confidence:          *inv.Confidence,
			ConfidenceBand:      derefString(inv.ConfidenceBand),
			Summary:             derefString(inv.RootCauseSummary),
			Narrative:           inv.Narrative,
			HypothesesGenerated: inv.HypothesesGenerated,
			HypothesesRejected:  inv.HypothesesRejected,
			EvidenceGathered:    inv.EvidenceGathered,
			EvidenceSources:     inv.EvidenceSources,
		}
		// Populate citation refs from the stored JSON blob.
		if len(inv.CitationsJSON) > 0 {
			var cits []CitationRef
			if err := json.Unmarshal(inv.CitationsJSON, &cits); err == nil {
				resp.RCA.Citations = cits
			}
		}
	}

	if !inv.Status.IsTerminal() && inv.StartedAt != nil {
		elapsed := int(time.Since(*inv.StartedAt).Milliseconds())
		pct := float64(elapsed) / float64(inv.TimeBudgetMs) * 100
		if pct > 100 {
			pct = 100
		}
		resp.Progress = &ProgressInfo{
			ElapsedMs:    elapsed,
			BudgetMs:     inv.TimeBudgetMs,
			ProgressPct:  pct,
			CurrentPhase: phaseFromStatus(inv.Status),
		}
	}

	return resp
}

func phaseFromStatus(s domain.Status) string {
	switch s {
	case domain.StatusPending:
		return "queued"
	case domain.StatusRunning:
		return "initializing"
	case domain.StatusWaitingForEvidence:
		return "gathering_evidence"
	case domain.StatusRCAGeneration:
		return "generating_rca"
	default:
		return string(s)
	}
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
