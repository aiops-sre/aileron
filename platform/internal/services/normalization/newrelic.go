package normalization

import (
	"fmt"
	"strings"
	"time"
)

// NewRelicNormalizer handles New Relic alert webhook payloads.
// Supports both legacy v2 notifications and newer NerdGraph-based formats.
type NewRelicNormalizer struct{}

func (NewRelicNormalizer) Source() string { return "newrelic" }

func (NewRelicNormalizer) CanHandle(raw map[string]interface{}) bool {
	_, hasCondition := raw["condition_name"]
	_, hasPolicy := raw["policy_name"]
	_, hasCurrentState := raw["current_state"]
	_, hasIncidentID := raw["incident_id"]
	return hasCondition || hasPolicy || hasCurrentState || hasIncidentID
}

func (n NewRelicNormalizer) Normalize(raw map[string]interface{}) (*NormalizedAlert, error) {
	conditionName := strField(raw, "condition_name")
	policyName := strField(raw, "policy_name")

	title := coalesce(conditionName, strField(raw, "name", "title"))
	if title == "" {
		title = coalesce(policyName, "New Relic Alert")
	} else if policyName != "" && policyName != title {
		title = policyName + ": " + title
	}

	description := coalesce(
		strField(raw, "details", "description", "message", "runbook_url"),
	)

	currentState := strField(raw, "current_state") // open | acknowledged | closed

	severity := n.mapSeverity(raw)
	status := n.mapStatus(currentState)

	incidentID := strField(raw, "incident_id", "id")
	if incidentID == "" {
		incidentID = fmt.Sprintf("nr-%d", time.Now().UnixNano())
	}

	sourceURL := strField(raw, "incident_url", "url")
	entityName := strField(raw, "entity.name", "entity_name")
	entityType := strField(raw, "entity.type", "entity_type")

	labels := make(map[string]string)
	labels["source"] = "newrelic"
	setLabel(labels, "condition_name", conditionName)
	setLabel(labels, "policy_name", policyName)
	setLabel(labels, "current_state", currentState)
	setLabel(labels, "entity_type", entityType)

	// New Relic may include facets / tag data
	if facets, ok := raw["facets"].(map[string]interface{}); ok {
		for k, v := range facets {
			if s, ok := v.(string); ok {
				labels["facet_"+k] = s
			}
		}
	}

	entity := MergeEntityInfo(
		ExtractFromLabels(labels),
		ExtractFromText(title+" "+description+" "+entityName),
	)
	if entity.EntityName == "" {
		entity.EntityName = entityName
	}
	if entity.EntityType == "" {
		entity.EntityType = n.canonicalEntityType(entityType)
	}

	fp := FingerprintFromSourceID("newrelic", incidentID)
	if fp == "" {
		fp = Fingerprint(entity.EntityID, conditionName, title)
	}

	var firedAt time.Time
	if ts, ok := raw["timestamp"].(float64); ok && ts > 0 {
		firedAt = time.Unix(int64(ts), 0)
	}
	if firedAt.IsZero() {
		if s := strField(raw, "opened_at", "timestamp_utc"); s != "" {
			firedAt, _ = time.Parse(time.RFC3339, s)
		}
	}
	if firedAt.IsZero() {
		firedAt = time.Now()
	}

	var resolvedAt *time.Time
	if strings.ToLower(currentState) == "closed" {
		if s := strField(raw, "closed_at"); s != "" {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				resolvedAt = &t
			}
		}
		if resolvedAt == nil {
			now := time.Now()
			resolvedAt = &now
		}
	}

	setLabel(labels, "cluster", entity.Cluster)
	setLabel(labels, "namespace", entity.Namespace)
	setLabel(labels, "service", entity.Service)

	meta := map[string]interface{}{
		"incident_id":      incidentID,
		"condition_name":   conditionName,
		"policy_name":      policyName,
		"current_state":    currentState,
		"entity_name":      entityName,
		"entity_type":      entityType,
	}
	if v, ok := raw["open_violations_count_critical"]; ok {
		meta["open_violations_critical"] = v
	}
	if v, ok := raw["open_violations_count_warning"]; ok {
		meta["open_violations_warning"] = v
	}

	return &NormalizedAlert{
		SourceID:    incidentID,
		Source:      "newrelic",
		SourceURL:   sourceURL,
		Title:       title,
		Description: description,
		Severity:    severity,
		Status:      status,
		EntityID:    entity.EntityID,
		EntityType:  entity.EntityType,
		EntityName:  entity.EntityName,
		Cluster:     entity.Cluster,
		Namespace:   entity.Namespace,
		Node:        entity.Node,
		Host:        entity.Host,
		Service:     entity.Service,
		Workload:    entity.Workload,
		Fingerprint: fp,
		FiredAt:     firedAt,
		ResolvedAt:  resolvedAt,
		Labels:      labels,
		Metadata:    meta,
		Raw:         raw,
	}, nil
}

func (NewRelicNormalizer) mapSeverity(raw map[string]interface{}) Severity {
	// Check explicit priority/severity first
	s := strField(raw, "priority", "severity", "condition_type")
	if s != "" {
		return MapSeverity(s)
	}
	// Infer from violation counts
	if v, ok := raw["open_violations_count_critical"].(float64); ok && v > 0 {
		return SeverityCritical
	}
	if v, ok := raw["open_violations_count_warning"].(float64); ok && v > 0 {
		return SeverityMedium
	}
	return SeverityMedium
}

func (NewRelicNormalizer) mapStatus(currentState string) Status {
	switch strings.ToLower(currentState) {
	case "closed", "resolved":
		return StatusResolved
	default:
		return StatusFiring
	}
}

func (NewRelicNormalizer) canonicalEntityType(nr string) string {
	switch strings.ToLower(nr) {
	case "application", "apm_application_entity":
		return "application"
	case "host", "infrastructure_host_entity":
		return "vm"
	case "kubernetes_pod_entity":
		return "k8s_pod"
	case "kubernetes_node_entity":
		return "k8s_node"
	default:
		return "service"
	}
}

// NewRelicPayloadToAlerts normalises the New Relic webhook envelope.
// New Relic sends one alert per POST.
func NewRelicPayloadToAlerts(payload map[string]interface{}) []map[string]interface{} {
	return []map[string]interface{}{payload}
}
