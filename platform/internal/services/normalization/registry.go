package normalization

import (
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aileron-platform/aileron/platform/internal/shared/models"
)

// Registry holds all registered normalizers and handles source-based dispatch.
// It is the single entry-point used by webhook handlers.
type Registry struct {
	bySource map[string]AlertNormalizer
	ordered  []AlertNormalizer // for CanHandle auto-detection (order matters)
	fallback AlertNormalizer
}

// NewRegistry creates a registry pre-loaded with all built-in normalizers.
func NewRegistry() *Registry {
	r := &Registry{
		bySource: make(map[string]AlertNormalizer),
		fallback: GenericFallbackNormalizer{},
	}
	r.Register(DynatraceNormalizer{})
	r.Register(PrometheusNormalizer{})
	r.Register(GrafanaNormalizer{})
	r.Register(SplunkNormalizer{})
	r.Register(DatadogNormalizer{})
	r.Register(NewRelicNormalizer{})
	r.Register(CloudWatchNormalizer{})
	r.Register(AWSCloudWatchNormalizer{})
	r.Register(AWSGuardDutyNormalizer{})
	r.Register(AWSEventBridgeNormalizer{})
	r.Register(GCPMonitoringNormalizer{})
	r.Register(GCPSecurityCommandCenterNormalizer{})
	r.Register(AzureMonitorNormalizer{})
	r.Register(AzureSentinelNormalizer{})
	r.Register(AzureServiceHealthNormalizer{})
	r.Register(AliCloudCMSNormalizer{})
	r.Register(OpsGenieNormalizer{})
	return r
}

// Register adds a normalizer to the registry.  Registrations override by source name.
func (r *Registry) Register(n AlertNormalizer) {
	r.bySource[n.Source()] = n
	r.ordered = append(r.ordered, n)
}

// Normalize selects the best normalizer for the given source and raw payload,
// then runs it.  Falls back to auto-detection if source is unknown, then to
// GenericFallbackNormalizer as last resort.  Never returns nil, never panics.
func (r *Registry) Normalize(source string, raw map[string]interface{}) (*NormalizedAlert, error) {
	if raw == nil {
		raw = map[string]interface{}{}
	}

	// 1. Exact source match.
	if n, ok := r.bySource[strings.ToLower(source)]; ok {
		result, err := safeNormalize(n, raw)
		if err != nil {
			log.Printf("normalization[%s] error: %v — falling back to generic", source, err)
			return r.fallback.Normalize(raw)
		}
		return result, nil
	}

	// 2. Auto-detect by payload structure.
	for _, n := range r.ordered {
		if n.CanHandle(raw) {
			result, err := safeNormalize(n, raw)
			if err == nil {
				return result, nil
			}
		}
	}

	// 3. Generic fallback — always succeeds.
	return r.fallback.Normalize(raw)
}

// ToAlert converts a NormalizedAlert into the models.Alert that the pipeline consumes.
// It guarantees all pipeline-critical fields are non-empty and valid.
func ToAlert(n *NormalizedAlert) *models.Alert {
	now := time.Now()
	firedAt := n.FiredAt
	if firedAt.IsZero() {
		firedAt = now
	}

	// Unified labels: normalizer-extracted + canonical dimension aliases 
	labels := make(map[string]string)
	for k, v := range n.Labels {
		labels[k] = v
	}
	setLabelIfMissing(labels, "cluster", n.Cluster)
	setLabelIfMissing(labels, "k8s.cluster.name", n.Cluster)
	setLabelIfMissing(labels, "namespace", n.Namespace)
	setLabelIfMissing(labels, "k8s.namespace.name", n.Namespace)
	setLabelIfMissing(labels, "service", n.Service)
	setLabelIfMissing(labels, "node", n.Node)
	setLabelIfMissing(labels, "k8s.node.name", n.Node)
	setLabelIfMissing(labels, "host.name", n.Host)
	setLabelIfMissing(labels, "workload", n.Workload)
	setLabelIfMissing(labels, "k8s.workload.name", n.Workload)
	setLabelIfMissing(labels, "region", n.Region)
	setLabelIfMissing(labels, "environment", n.Environment)
	setLabelIfMissing(labels, "root_cause_entity", n.RootCauseEntity)
	setLabelIfMissing(labels, "root_cause_entity_id", n.RootCauseEntityID)
	setLabelIfMissing(labels, "entity_type", n.EntityType)

	// Metadata: normalizer meta + raw payload preserved for audit 
	meta := make(map[string]interface{})
	for k, v := range n.Metadata {
		meta[k] = v
	}
	if n.MetricName != "" {
		meta["metric_name"] = n.MetricName
		meta["metric_value"] = n.MetricValue
	}
	meta["normalized_source"] = n.Source
	meta["entity_id"] = n.EntityID
	meta["entity_name"] = n.EntityName

	// Fingerprint 
	fp := n.Fingerprint
	if fp == "" {
		fp = Fingerprint(n.EntityID, n.MetricName, n.Title)
	}

	// Severity guard 
	severity := string(n.Severity)
	if severity == "" {
		severity = "medium"
	}

	// Status 
	status := string(n.Status)
	if status == "" {
		status = "open"
	}

	// Tags: a curated subset of labels surfaced for quick filtering 
	tags := buildTags(n)

	alert := &models.Alert{
		ID:              uuid.New(),
		Title:           n.Title,
		Description:     n.Description,
		Severity:        severity,
		Status:          status,
		Source:          n.Source,
		SourceID:        n.SourceID,
		SourceURL:       n.SourceURL,
		Labels:          labels,
		Metadata:        meta,
		Fingerprint:     fp,
		EntityID:        n.EntityID,
		EntityType:      n.EntityType,
		RootCauseEntity: n.RootCauseEntity,
		Region:          n.Region,
		Tags:            tags,
		Count:           1,
		CreatedAt:       firedAt,
		UpdatedAt:       now,
		FirstSeenAt:     firedAt,
		LastSeenAt:      firedAt,
	}
	alert.UpdateActiveStatus()
	return alert
}

// private helpers 

func setLabelIfMissing(m map[string]string, key, val string) {
	if val != "" && m[key] == "" {
		m[key] = val
	}
}

func buildTags(n *NormalizedAlert) []string {
	tags := make([]string, 0, 6)
	addTag := func(k, v string) {
		if v != "" {
			tags = append(tags, k+":"+v)
		}
	}
	addTag("source", n.Source)
	addTag("cluster", n.Cluster)
	addTag("namespace", n.Namespace)
	addTag("service", n.Service)
	addTag("entity_type", n.EntityType)
	addTag("severity", string(n.Severity))
	return tags
}

// safeNormalize wraps a normalizer call in a recover() so a panicking normalizer
// does not crash the ingestion path.
func safeNormalize(n AlertNormalizer, raw map[string]interface{}) (result *NormalizedAlert, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("normalizer[%s] panicked: %v", n.Source(), r)
			result = nil
			err = nil
		}
	}()
	return n.Normalize(raw)
}
