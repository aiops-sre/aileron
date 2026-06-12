package rca

import (
	"time"

	"github.com/google/uuid"
	domain_hyp "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/hypothesis"
)

// ── Causal Chain ──────────────────────────────────────────────────────────────

// CausalNodeRole classifies an entity's position in the causal chain.
type CausalNodeRole string

const (
	RoleTrigger      CausalNodeRole = "trigger"       // The change/event that initiated the failure
	RoleRootCause    CausalNodeRole = "root_cause"     // The entity that actually failed
	RoleProximate    CausalNodeRole = "proximate"      // The entity that showed the primary symptom
	RoleSymptom      CausalNodeRole = "symptom"        // Downstream entities affected
	RoleContext      CausalNodeRole = "context"        // Background context (not causal)
)

// CausalNode is one entity or event in the causal chain.
type CausalNode struct {
	// For entities (K8s nodes, VMs, storage volumes):
	EntityID   string `json:"entity_id,omitempty"`
	EntityType string `json:"entity_type,omitempty"`
	EntityName string `json:"entity_name,omitempty"`
	GraphNodeID string `json:"graph_node_id,omitempty"`

	// For change events:
	ChangeID      string `json:"change_id,omitempty"`
	ChangeType    string `json:"change_type,omitempty"`
	ChangeTitle   string `json:"change_title,omitempty"`
	ChangedBy     string `json:"changed_by,omitempty"`

	Role        CausalNodeRole `json:"role"`
	OccurredAt  *time.Time     `json:"occurred_at,omitempty"`
	Description string         `json:"description"`

	// DeltaMinutes is set for trigger nodes: minutes before incident start.
	DeltaMinutes *int `json:"delta_minutes,omitempty"`
}

// CausalChain is the ordered sequence of events and entities leading to the incident.
type CausalChain struct {
	Nodes []CausalNode `json:"nodes"`
}

// ── Blast Radius ──────────────────────────────────────────────────────────────

// BlastRadiusTier groups affected entities by probability of impact.
type BlastRadiusTier struct {
	Label       string               `json:"label"` // "CRITICAL" | "HIGH" | "MEDIUM" | "LOW"
	MinProb     float64              `json:"min_prob"`
	Entities    []BlastRadiusEntity  `json:"entities"`
}

// BlastRadiusEntity is one entity in the blast radius.
type BlastRadiusEntity struct {
	EntityID        string  `json:"entity_id"`
	EntityType      string  `json:"entity_type"`
	EntityName      string  `json:"entity_name"`
	ImpactProb      float64 `json:"impact_probability"`
	Depth           int     `json:"depth"`
	OwnerTeam       string  `json:"owner_team,omitempty"`
	OnCallPerson    string  `json:"on_call_person,omitempty"`
}

// BlastRadiusSummary is the full blast radius result.
type BlastRadiusSummary struct {
	RootEntityID   string            `json:"root_entity_id"`
	TotalAffected  int               `json:"total_affected"`
	Tiers          []BlastRadiusTier `json:"tiers"`
	ComputedAt     time.Time         `json:"computed_at"`
	FromCache      bool              `json:"from_cache"`
}

// ── RCA Report ────────────────────────────────────────────────────────────────

// Report is the full structured RCA output for an investigation.
// This is assembled by the RCA Generator and stored in the investigations table.
type Report struct {
	InvestigationID uuid.UUID `json:"investigation_id"`
	IncidentID      uuid.UUID `json:"incident_id"`
	GeneratedAt     time.Time `json:"generated_at"`
	ElapsedMs       int       `json:"elapsed_ms"`

	// Primary conclusion.
	WinningHypothesisID   uuid.UUID                  `json:"winning_hypothesis_id"`
	WinningHypothesisType string                     `json:"winning_hypothesis_type"`
	Confidence            float64                    `json:"confidence"`
	ConfidenceBand        domain_hyp.ConfidenceBand  `json:"confidence_band"`
	Summary               string                     `json:"summary"`

	// Rejected hypotheses summary (for operator understanding).
	RejectedHypotheses []RejectedHypothesisSummary `json:"rejected_hypotheses"`

	// Causal chain from trigger through root cause to symptom.
	CausalChain CausalChain `json:"causal_chain"`

	// Blast radius computed by OKG.
	BlastRadius *BlastRadiusSummary `json:"blast_radius,omitempty"`

	// LLM-generated narrative (evidence-gated at confidence >= 0.65).
	Narrative      string    `json:"narrative"`
	NarrativeModel string    `json:"narrative_model"`
	NarrativeAt    time.Time `json:"narrative_generated_at"`

	// Pre-incident timeline of events and changes.
	PreIncidentTimeline []TimelineEvent `json:"pre_incident_timeline"`

	// Recommended actions from the winning hypothesis template.
	RecommendedActions []RecommendedAction `json:"recommended_actions"`
}

// RejectedHypothesisSummary is a compact summary of a non-winning hypothesis.
type RejectedHypothesisSummary struct {
	Type            string  `json:"type"`
	Title           string  `json:"title"`
	Status          string  `json:"status"`
	Confidence      float64 `json:"confidence"`
	RejectionReason string  `json:"rejection_reason,omitempty"`
}

// RecommendedAction is one operator action extracted from the winning template.
type RecommendedAction struct {
	Title               string   `json:"title"`
	Steps               []string `json:"steps"`
	Urgency             string   `json:"urgency"`
	EstimatedMinutes    int      `json:"estimated_minutes,omitempty"`
	SafetyLevel         string   `json:"safety_level"`
	ConfirmationMessage string   `json:"confirmation_message,omitempty"`
}
