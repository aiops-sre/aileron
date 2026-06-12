package normalization

import "time"

// Severity is the canonical severity enum.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
	SeverityInfo     Severity = "info"
)

// Status is the canonical alert status enum.
type Status string

const (
	StatusFiring   Status = "open"
	StatusResolved Status = "resolved"
)

// NormalizedAlert is the intermediate, source-agnostic representation of an alert.
// Every normalizer produces one; the registry converts it to models.Alert for the pipeline.
type NormalizedAlert struct {
	// Identity 
	SourceID  string // External ID: problemId, fingerprint, sid, ruleId …
	Source    string // Canonical source name: "dynatrace", "prometheus", …
	SourceURL string

	// Content 
	Title       string
	Description string
	Severity    Severity
	Status      Status

	// Primary affected entity 
	EntityID   string // Stable, topology-joinable key  (cluster/ns/kind/name)
	EntityType string // "k8s_pod" | "k8s_node" | "vm" | "bare_metal" | "service" | "application"
	EntityName string // Human-readable name

	// Infrastructure dimensions (filled by normalizer + enricher) 
	Cluster     string
	Namespace   string
	Service     string
	Node        string
	Host        string
	Workload    string
	Region      string
	Environment string

	// Root-cause attribution (Dynatrace + generic) 
	RootCauseEntity   string // entityName of the root cause
	RootCauseEntityID string // entityId  of the root cause

	// Metric context 
	MetricName  string
	MetricValue float64

	// Deduplication 
	Fingerprint string // set by normalizer; Fingerprint() used as fallback

	// Timestamps 
	FiredAt    time.Time
	ResolvedAt *time.Time

	// Carry-through labels: all extracted k/v for pipeline enrichment 
	Labels map[string]string

	// Source-specific metadata (for timeline / audit) 
	Metadata map[string]interface{}

	// Original payload (preserved for replay / debugging) 
	Raw map[string]interface{}
}

// AlertNormalizer converts a raw webhook payload into a NormalizedAlert.
type AlertNormalizer interface {
	// Source returns the canonical source name handled by this normalizer.
	Source() string
	// CanHandle returns true when this normalizer recognises the payload structure.
	// Used for auto-detection when the caller does not know the source upfront.
	CanHandle(raw map[string]interface{}) bool
	// Normalize performs the full extraction and mapping.  It must never panic
	// and must return a best-effort result even for partially-malformed payloads.
	Normalize(raw map[string]interface{}) (*NormalizedAlert, error)
}
