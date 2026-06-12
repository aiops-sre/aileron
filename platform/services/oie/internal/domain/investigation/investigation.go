package investigation

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Status represents the lifecycle state of an investigation.
type Status string

const (
	StatusPending            Status = "PENDING"
	StatusRunning            Status = "RUNNING"
	StatusWaitingForEvidence Status = "WAITING_FOR_EVIDENCE"
	StatusRCAGeneration      Status = "RCA_GENERATION"
	StatusCompleted          Status = "COMPLETED"
	StatusFailed             Status = "FAILED"
	StatusCancelled          Status = "CANCELLED"
)

// IsTerminal returns true if no further state transitions are possible.
func (s Status) IsTerminal() bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusCancelled
}

// Investigation is the aggregate root for the investigation lifecycle.
// All mutations go through domain methods to enforce invariants.
type Investigation struct {
	ID               uuid.UUID
	IncidentID       uuid.UUID
	IncidentNumber   string
	IdempotencyKey   string
	SourceMessageKey string

	Status       Status
	StatusReason string

	RootEntityID   *uuid.UUID
	RootEntityType *string
	FailureClass   *string
	Domain         *string
	Severity       string
	PlaybookID     string
	// TopologyPath and CorrelationID from AlertHub, used to derive entity context
	// without requiring a separate EIRS call.
	TopologyPath  string
	CorrelationID string

	IncidentStartedAt  time.Time
	TriggeredAt        time.Time
	StartedAt          *time.Time
	EvidenceCompleteAt *time.Time
	CompletedAt        *time.Time

	TimeBudgetMs int
	ElapsedMs    *int

	LockedBy             *string
	LockAcquiredAt       *time.Time
	LockExpiresAt        *time.Time
	RecoveryAttemptCount int
	LastRecoveryAt       *time.Time

	HypothesesGenerated int
	HypothesesRejected  int
	EvidenceGathered    int
	EvidenceSources     []string

	RootCauseHypothesisID *uuid.UUID
	Confidence            *float64
	ConfidenceBand        *string
	RootCauseSummary      *string
	CausalChain           json.RawMessage
	BlastRadiusSummary    json.RawMessage

	Narrative            *string
	NarrativeModel       *string
	NarrativeGeneratedAt *time.Time
	// CitationsJSON stores the evidence citation refs from the narrator as a JSON array.
	// Exposed in the investigation API response for the frontend to render.
	CitationsJSON json.RawMessage

	ReportVersion              int
	LateEvidenceWindowClosesAt *time.Time

	FeedbackStatus       string
	FeedbackBy           *uuid.UUID
	FeedbackAt           *time.Time
	FeedbackNotes        *string
	ActualRootCause      *string
	ActualHypothesisType *string

	DeletedAt *time.Time

	CreatedAt time.Time
	UpdatedAt time.Time

	domainEvents []DomainEvent
}

// NewInvestigation creates a new Investigation in PENDING state.
func NewInvestigation(
	incidentID uuid.UUID,
	incidentNumber string,
	idempotencyKey string,
	sourceMessageKey string,
	severity string,
	incidentStartedAt time.Time,
	timeBudgetMs int,
) (*Investigation, error) {
	if incidentID == uuid.Nil {
		return nil, ErrInvalidInput{Field: "incident_id", Reason: "must not be nil"}
	}
	if idempotencyKey == "" {
		return nil, ErrInvalidInput{Field: "idempotency_key", Reason: "must not be empty"}
	}
	if severity == "" {
		return nil, ErrInvalidInput{Field: "severity", Reason: "must not be empty"}
	}
	if timeBudgetMs <= 0 {
		return nil, ErrInvalidInput{Field: "time_budget_ms", Reason: "must be positive"}
	}

	now := time.Now().UTC()
	inv := &Investigation{
		ID:                uuid.New(),
		IncidentID:        incidentID,
		IncidentNumber:    incidentNumber,
		IdempotencyKey:    idempotencyKey,
		SourceMessageKey:  sourceMessageKey,
		Status:            StatusPending,
		Severity:          severity,
		IncidentStartedAt: incidentStartedAt,
		TriggeredAt:       now,
		TimeBudgetMs:      timeBudgetMs,
		ReportVersion:     1,
		FeedbackStatus:    "no_feedback",
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	inv.raise(InvestigationStartedEvent{
		InvestigationID: inv.ID,
		IncidentID:      inv.IncidentID,
		Severity:        inv.Severity,
		EventTime:  now,
	})

	return inv, nil
}

// TransitionTo moves the investigation to the given status.
func (inv *Investigation) TransitionTo(status Status, reason string) error {
	if err := defaultStateMachine.ValidateTransition(inv.Status, status); err != nil {
		return err
	}

	prev := inv.Status
	now := time.Now().UTC()
	inv.Status = status
	inv.StatusReason = reason
	inv.UpdatedAt = now

	switch status {
	case StatusRunning:
		inv.StartedAt = &now
	case StatusRCAGeneration:
		inv.EvidenceCompleteAt = &now
	case StatusCompleted:
		inv.CompletedAt = &now
		window := now.Add(5 * time.Minute)
		inv.LateEvidenceWindowClosesAt = &window
		elapsed := int(now.Sub(inv.TriggeredAt).Milliseconds())
		inv.ElapsedMs = &elapsed
	case StatusFailed, StatusCancelled:
		inv.CompletedAt = &now
		elapsed := int(now.Sub(inv.TriggeredAt).Milliseconds())
		inv.ElapsedMs = &elapsed
	}

	inv.raise(StatusChangedEvent{
		InvestigationID: inv.ID,
		IncidentID:      inv.IncidentID,
		PreviousStatus:  prev,
		CurrentStatus:   status,
		Reason:          reason,
		EventTime:  now,
	})

	return nil
}

// SetEntityContext attaches the EIRS-resolved entity context.
func (inv *Investigation) SetEntityContext(
	rootEntityID uuid.UUID,
	rootEntityType string,
	failureClass string,
	domain string,
	playbookID string,
) {
	inv.RootEntityID = &rootEntityID
	inv.RootEntityType = &rootEntityType
	inv.FailureClass = &failureClass
	inv.Domain = &domain
	inv.PlaybookID = playbookID
	inv.UpdatedAt = time.Now().UTC()
}

// SetRCAResult attaches the completed RCA. Only valid when status is RCA_GENERATION.
func (inv *Investigation) SetRCAResult(
	hypothesisID uuid.UUID,
	confidence float64,
	confidenceBand string,
	summary string,
	causalChain json.RawMessage,
	blastRadius json.RawMessage,
	evidenceCount int,
	evidenceSources []string,
	hypothesesGenerated int,
	hypothesesRejected int,
) error {
	if inv.Status != StatusRCAGeneration {
		return ErrInvalidTransition{From: inv.Status, To: StatusCompleted}
	}

	inv.RootCauseHypothesisID = &hypothesisID
	inv.Confidence = &confidence
	inv.ConfidenceBand = &confidenceBand
	inv.RootCauseSummary = &summary
	inv.CausalChain = causalChain
	inv.BlastRadiusSummary = blastRadius
	inv.EvidenceGathered = evidenceCount
	inv.EvidenceSources = evidenceSources
	inv.HypothesesGenerated = hypothesesGenerated
	inv.HypothesesRejected = hypothesesRejected
	inv.UpdatedAt = time.Now().UTC()

	return nil
}

// SetNarrative attaches the LLM-generated narrative.
func (inv *Investigation) SetNarrative(narrative string, model string) {
	now := time.Now().UTC()
	inv.Narrative = &narrative
	inv.NarrativeModel = &model
	inv.NarrativeGeneratedAt = &now
	inv.UpdatedAt = now
}

// IncrementReportVersion bumps the version when late evidence triggers re-scoring.
func (inv *Investigation) IncrementReportVersion() {
	inv.ReportVersion++
	inv.UpdatedAt = time.Now().UTC()

	inv.raise(ReportVersionUpdatedEvent{
		InvestigationID: inv.ID,
		IncidentID:      inv.IncidentID,
		Version:         inv.ReportVersion,
		EventTime:  inv.UpdatedAt,
	})
}

// IsWithinLateEvidenceWindow returns true if late evidence can still trigger re-scoring.
func (inv *Investigation) IsWithinLateEvidenceWindow() bool {
	if inv.LateEvidenceWindowClosesAt == nil {
		return false
	}
	return time.Now().UTC().Before(*inv.LateEvidenceWindowClosesAt)
}

// PullDomainEvents returns accumulated domain events and clears the internal slice.
func (inv *Investigation) PullDomainEvents() []DomainEvent {
	events := make([]DomainEvent, len(inv.domainEvents))
	copy(events, inv.domainEvents)
	inv.domainEvents = inv.domainEvents[:0]
	return events
}

func (inv *Investigation) raise(e DomainEvent) {
	inv.domainEvents = append(inv.domainEvents, e)
}
