package hypothesis

// HypothesisTemplate defines the operational knowledge for one failure mode.
// Templates are the compiled SRE expertise about what causes what.
type HypothesisTemplate struct {
	// ID is the canonical template identifier (e.g. "HT-K8S-002").
	ID string
	// Type is the machine-readable name used in scoring and learning records.
	Type string
	// Name is the human-readable title (e.g. "CloudStack VM Failure").
	Name        string
	Description string

	// Scope: which investigations generate this hypothesis.
	ApplicableEntityTypes    []string // k8s_node, k8s_pod, virtual_machine, etc.
	ApplicableFailureClasses []string // NotReady, CrashLoopBackOff, etc.
	ApplicableDomains        []string // kubernetes, infrastructure, storage, application

	// BasePrior is the starting confidence (0-1) before any evidence is applied.
	// Reflects how common this failure mode is in the target environment.
	BasePrior float64

	// RequiredEvidence: if any required evidence is MISSING (entity not in source),
	// apply a confidence penalty. If the fetch timed out or errored, no penalty.
	RequiredEvidence []EvidenceSpec

	// SupportingEvidence: when present, boosts confidence via independence-complement
	// (or max-within-group for correlated evidence).
	SupportingEvidence []EvidenceSpec

	// ContradictingEvidence: when present, reduces confidence.
	// Temporal dampening is applied for CURRENT-mode evidence with large gaps.
	ContradictingEvidence []EvidenceSpec

	// HardRejectionEvidenceTypes lists evidence types whose presence hard-rejects
	// this hypothesis (confidence set to 0.0, status=REJECTED).
	// ONLY valid for logically necessary exclusions (e.g. exit code 137 ≠ exit code 1).
	// NOT used for current-state evidence — temporal dampening handles that.
	HardRejectionEvidenceTypes []string

	// MinSupportingEvidence: if fewer than this many supporting evidence pieces
	// are present and the confidence is below 0.45, status=INSUFFICIENT_EVIDENCE.
	MinSupportingEvidence int

	// DefaultActions are shown in the recommendation panel when this hypothesis wins.
	DefaultActions []ActionSpec
}

// EvidenceSpec ties an evidence type to its scoring weight and role metadata.
type EvidenceSpec struct {
	// EvidenceType matches domain/evidence.EvidenceType constants.
	EvidenceType string
	// Weight is the base contribution to the confidence score (0-1).
	Weight float64
	// Required: if true and evidence is MISSING, apply confidence penalty.
	Required bool
	// EvidenceGroup: if non-empty, this evidence belongs to a correlated group.
	// Within a group, only the max-weight evidence is applied (prevents double-counting).
	EvidenceGroup string
	// Description for explainability in the UI.
	Description string
}

// ActionSpec defines a recommended remediation action.
type ActionSpec struct {
	Title             string
	Steps             []string
	Urgency           string // "immediate" | "high" | "medium" | "low"
	EstimatedMinutes  int
	// SafetyLevel classifies the risk of executing this action.
	SafetyLevel string // "SAFE" | "CAUTION" | "DANGEROUS"
	// ConfirmationMessage is shown for DANGEROUS actions before execution.
	ConfirmationMessage string
}
