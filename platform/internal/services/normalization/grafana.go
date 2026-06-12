package normalization

import (
	"strings"
	"time"
)

// GrafanaNormalizer handles both Grafana legacy (v8 unified panel alert) and
// v9+ unified alerting webhook formats.
//
// Legacy format: single object with title, ruleId, state, evalMatches[], message
// Unified format: { alerts: [{...}, ...] } — caller should use GrafanaPayloadToAlerts.
type GrafanaNormalizer struct{}

func (GrafanaNormalizer) Source() string { return "grafana" }

func (GrafanaNormalizer) CanHandle(raw map[string]interface{}) bool {
	_, hasState := raw["state"]
	_, hasRuleID := raw["ruleId"]
	_, hasRuleName := raw["ruleName"]
	_, hasAlerts := raw["alerts"] // unified alerting envelope
	return hasState || hasRuleID || hasRuleName || hasAlerts
}

func (n GrafanaNormalizer) Normalize(raw map[string]interface{}) (*NormalizedAlert, error) {
	title := coalesce(
		strField(raw, "title", "ruleName", "name"),
	)
	if title == "" {
		title = "Grafana Alert"
	}

	description := coalesce(
		strField(raw, "message", "summary", "description"),
	)

	state := strField(raw, "state", "status")
	severity := MapSeverity(coalesce(
		strField(raw, "severity", "priority"),
		n.severityFromState(state),
	))
	status := MapStatus(state)

	ruleID := strField(raw, "ruleId", "alertRuleUID", "uid")
	ruleURL := strField(raw, "ruleUrl", "ruleURL", "url")
	dashboardID := strField(raw, "dashboardId", "dashboardUID")

	// Labels from Grafana unified alerting (v9+)
	labelsRaw := stringMapFrom(raw, "labels")
	annotationsRaw := stringMapFrom(raw, "annotations")

	if description == "" {
		description = coalesce(annotationsRaw["summary"], annotationsRaw["description"])
	}

	entity := MergeEntityInfo(
		ExtractFromLabels(labelsRaw),
		ExtractFromText(title+" "+description),
	)

	fp := FingerprintFromSourceID("grafana", ruleID)
	if fp == "" {
		fp = Fingerprint(entity.EntityID, "", title)
	}

	var firedAt time.Time
	if s := strField(raw, "startsAt"); s != "" {
		firedAt, _ = time.Parse(time.RFC3339, s)
	}
	if firedAt.IsZero() {
		firedAt = time.Now()
	}

	var resolvedAt *time.Time
	if s := strField(raw, "endsAt"); s != "" && !strings.HasPrefix(s, "0001") {
		if t, err := time.Parse(time.RFC3339, s); err == nil && !t.IsZero() {
			resolvedAt = &t
		}
	}

	labels := make(map[string]string)
	for k, v := range labelsRaw {
		labels[k] = v
	}
	labels["source"] = "grafana"
	setLabel(labels, "rule_id", ruleID)
	setLabel(labels, "dashboard_id", dashboardID)
	setLabel(labels, "cluster", entity.Cluster)
	setLabel(labels, "k8s.cluster.name", entity.Cluster)
	setLabel(labels, "namespace", entity.Namespace)
	setLabel(labels, "node", entity.Node)
	setLabel(labels, "service", entity.Service)

	meta := map[string]interface{}{
		"dashboard_id": dashboardID,
		"panel_id":     strField(raw, "panelId"),
		"rule_id":      ruleID,
		"state":        state,
	}
	if em, ok := raw["evalMatches"]; ok {
		meta["eval_matches"] = em
	}
	for k, v := range annotationsRaw {
		meta["annotation_"+k] = v
	}

	return &NormalizedAlert{
		SourceID:    ruleID,
		Source:      "grafana",
		SourceURL:   ruleURL,
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

// GrafanaPayloadToAlerts splits the v9+ unified alerting envelope (alerts:[]) into
// per-alert maps that GrafanaNormalizer.Normalize() can consume.
func GrafanaPayloadToAlerts(payload map[string]interface{}) []map[string]interface{} {
	rawAlerts, _ := payload["alerts"].([]interface{})
	if len(rawAlerts) == 0 {
		// Legacy single-object format — return as-is.
		return []map[string]interface{}{payload}
	}
	out := make([]map[string]interface{}, 0, len(rawAlerts))
	for _, a := range rawAlerts {
		m, ok := a.(map[string]interface{})
		if !ok {
			continue
		}
		out = append(out, m)
	}
	return out
}

func (GrafanaNormalizer) severityFromState(state string) string {
	switch strings.ToLower(state) {
	case "alerting", "firing":
		return "high"
	case "pending":
		return "medium"
	case "ok", "normal", "resolved":
		return "info"
	default:
		return "medium"
	}
}
