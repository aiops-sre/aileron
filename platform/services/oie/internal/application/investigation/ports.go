package investigation

import (
	"context"
	"time"

	"github.com/google/uuid"
	domain "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/investigation"
)

// EvidenceRequest is sent to the EvidenceBus when evidence gathering begins.
type EvidenceRequest struct {
	InvestigationID uuid.UUID
	IncidentID      uuid.UUID
	RootEntityID    *uuid.UUID
	RootEntityType  string
	FailureClass    string
	Domain          string
	Severity        string
	PlaybookID      string
	IncidentStartAt time.Time
	BudgetMs        int
	// TopologyPath and CorrelationID come from AlertHub and allow the Evidence Bus
	// to build an EntityProfile without needing a separate EIRS resolution call.
	// Format: "cluster-name/namespace:issue" or "h:hostname"
	TopologyPath  string
	CorrelationID string
}

// EvidenceResult is returned from the EvidenceBus after all fetchers complete.
type EvidenceResult struct {
	EvidenceCount   int
	EvidenceSources []string
	TimedOut        bool
}

// EvidenceBus executes evidence gathering for an investigation.
// Implemented by the Evidence module (Module 2).
type EvidenceBus interface {
	Execute(ctx context.Context, req *EvidenceRequest) (*EvidenceResult, error)
}

// HypothesisRequest is sent to the HypothesisEngine after evidence is gathered.
type HypothesisRequest struct {
	InvestigationID uuid.UUID
	IncidentID      uuid.UUID
	RootEntityType  string
	FailureClass    string
	Domain          string
	Severity        string
	PlaybookID      string
}

// HypothesisResult is returned from the HypothesisEngine.
type HypothesisResult struct {
	Generated int
	Rejected  int
}

// HypothesisEngine evaluates hypotheses against gathered evidence.
// Implemented by the Hypothesis module (Module 3).
type HypothesisEngine interface {
	Evaluate(ctx context.Context, req *HypothesisRequest) (*HypothesisResult, error)
}

// RCARequest is sent to the RCAGenerator after hypothesis evaluation.
type RCARequest struct {
	InvestigationID   uuid.UUID
	IncidentID        uuid.UUID
	Domain            string
	// IncidentStartedAt is passed so the RCA generator can build accurate timelines.
	IncidentStartedAt time.Time
	// TopologyPath enables blast-radius lookup via the AlertHub Neo4j endpoint.
	// Format: "cluster/namespace/kind/name" or "cluster/namespace" from AlertHub.
	TopologyPath string
}

// RCAResult is the output of the RCA generation phase.
type RCAResult struct {
	WinningHypothesisID uuid.UUID
	Confidence          float64
	ConfidenceBand      string
	Summary             string
	CausalChain         []byte
	BlastRadius         []byte
	Narrative           string
	NarrativeModel      string
	// Citations are evidence source references extracted alongside the narrative.
	// Frontend renders these as inline citation chips below the RCA text.
	Citations []CitationRef
	// FullReportJSON is the serialized domain_rca.Report for detailed API retrieval.
	FullReportJSON []byte
}

// CitationRef is a reference to a specific evidence source cited in the narrative.
type CitationRef struct {
	Source       string `json:"source"`
	EvidenceType string `json:"evidence_type"`
	Description  string `json:"description"`
}

// RCAGenerator produces the final RCA report from scored hypotheses.
// Implemented by the RCA module (Module 4).
type RCAGenerator interface {
	Generate(ctx context.Context, req *RCARequest) (*RCAResult, error)
}

// EventPublisher publishes domain events to Kafka after DB commit.
type EventPublisher interface {
	PublishInvestigationEvent(ctx context.Context, eventType string, payload []byte) error
}

// Service is the application-level interface for the investigation manager.
type Service interface {
	Start(ctx context.Context, req *StartRequest) (*StartResponse, error)
	Cancel(ctx context.Context, id uuid.UUID, reason string) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Investigation, error)
	GetByIncidentID(ctx context.Context, incidentID uuid.UUID) (*domain.Investigation, error)
	ListActive(ctx context.Context) ([]*domain.Investigation, error)
	RecoverOrphaned(ctx context.Context) (int, error)
}

// StartRequest carries everything needed to trigger a new investigation.
type StartRequest struct {
	IncidentID        uuid.UUID
	IncidentNumber    string
	IdempotencyKey    string
	SourceMessageKey  string
	Severity          string
	IncidentStartedAt time.Time
	RootEntityID      *uuid.UUID
	RootEntityType    *string
	FailureClass      *string
	Domain            *string
	PlaybookID        string
	// TopologyPath and CorrelationID from AlertHub for direct entity context derivation.
	TopologyPath  string
	CorrelationID string
}

// StartResponse contains the result of a start request.
type StartResponse struct {
	InvestigationID uuid.UUID
	Status          domain.Status
	AlreadyExisted  bool
}

// NoOpEvidenceBus is the default implementation used until Module 2 is wired.
type NoOpEvidenceBus struct{}

func (n *NoOpEvidenceBus) Execute(_ context.Context, _ *EvidenceRequest) (*EvidenceResult, error) {
	return &EvidenceResult{EvidenceCount: 0, EvidenceSources: []string{}, TimedOut: false}, nil
}

// NoOpHypothesisEngine returns empty results until Module 3 is wired.
type NoOpHypothesisEngine struct{}

func (n *NoOpHypothesisEngine) Evaluate(_ context.Context, _ *HypothesisRequest) (*HypothesisResult, error) {
	return &HypothesisResult{Generated: 0, Rejected: 0}, nil
}

// NoOpRCAGenerator returns a minimal result until Module 4 is wired.
type NoOpRCAGenerator struct{}

func (n *NoOpRCAGenerator) Generate(_ context.Context, _ *RCARequest) (*RCAResult, error) {
	return &RCAResult{
		WinningHypothesisID: uuid.New(),
		Confidence:          0.0,
		ConfidenceBand:      "INSUFFICIENT_EVIDENCE",
		Summary:             "RCA module not yet initialized",
		Narrative:           "Investigation completed. RCA generation module is pending deployment.",
		NarrativeModel:      "none",
	}, nil
}
