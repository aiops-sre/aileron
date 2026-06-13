package normalization

import (
	"strings"
	"time"
)

// OpsGenieNormalizer handles OpsGenie webhook payloads.
//
// OpsGenie fires a POST for each alert lifecycle action (Create, Acknowledge,
// Close, etc.).  The payload wraps the alert inside an "alert" sub-object.
type OpsGenieNormalizer struct{}

func (OpsGenieNormalizer) Source() string { return "opsgenie" }

func (OpsGenieNormalizer) CanHandle(raw map[string]interface{}) bool {
	if alert, ok := raw["alert"].(map[string]interface{}); ok {
		if _, hasID := alert["alertId"]; hasID {
			return true
		}
	}
	action, _ := raw["action"].(string)
	switch action {
	case "Create", "Acknowledge", "Close":
		return true
	}
	return false
}

func (n OpsGenieNormalizer) Normalize(raw map[string]interface{}) (*NormalizedAlert, error) {
	alert := n.alertObject(raw)

	alertID := strField(alert, "alertId")
	message := strField(alert, "message")
	description := strField(alert, "description")
	priority := strField(alert, "priority") // P1..P5
	status := strField(alert, "status")     // open | closed | acked
	source := strField(alert, "source")

	action := strField(raw, "action") // Create | Acknowledge | Close

	title := message
	if title == "" {
		title = "OpsGenie Alert"
	}

	severity := n.mapPriority(priority)
	alertStatus := n.mapStatus(coalesce(status, n.statusFromAction(action)))

	// Tags: []interface{} of strings
	tags := n.extractTags(alert)

	// Teams: []interface{} of string or {"name": "..."}
	teams := n.extractTeams(alert)

	labels := map[string]string{
		"source":      "opsgenie",
		"opsgenie_id": alertID,
		"tags":        strings.Join(tags, ","),
	}
	setLabel(labels, "priority", priority)
	setLabel(labels, "action", action)
	setLabel(labels, "opsgenie_source", source)
	if len(teams) > 0 {
		labels["teams"] = strings.Join(teams, ",")
	}

	entity := MergeEntityInfo(
		ExtractFromLabels(labels),
		ExtractFromText(title+" "+description),
	)

	fp := FingerprintFromSourceID("opsgenie", alertID)
	if fp == "" {
		fp = Fingerprint(entity.EntityID, "", title)
	}

	firedAt := n.parseTime(strField(alert, "createdAt"))
	if firedAt.IsZero() {
		firedAt = time.Now()
	}

	var resolvedAt *time.Time
	if alertStatus == StatusResolved {
		if t := n.parseTime(strField(alert, "updatedAt")); !t.IsZero() {
			resolvedAt = &t
		} else {
			now := time.Now()
			resolvedAt = &now
		}
	}

	setLabel(labels, "cluster", entity.Cluster)
	setLabel(labels, "namespace", entity.Namespace)
	setLabel(labels, "node", entity.Node)
	setLabel(labels, "service", entity.Service)

	meta := map[string]interface{}{
		"alert_id":    alertID,
		"priority":    priority,
		"status":      status,
		"action":      action,
		"tags":        tags,
		"teams":       teams,
		"source":      source,
		"created_at":  strField(alert, "createdAt"),
		"updated_at":  strField(alert, "updatedAt"),
	}
	if tinyID, ok := alert["tinyId"]; ok {
		meta["tiny_id"] = tinyID
	}
	if responders, ok := alert["responders"]; ok {
		meta["responders"] = responders
	}

	return &NormalizedAlert{
		SourceID:    alertID,
		Source:      "opsgenie",
		Title:       title,
		Description: description,
		Severity:    severity,
		Status:      alertStatus,
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

// alertObject safely returns the "alert" sub-object or raw itself as a fallback.
func (OpsGenieNormalizer) alertObject(raw map[string]interface{}) map[string]interface{} {
	if a, ok := raw["alert"].(map[string]interface{}); ok {
		return a
	}
	return raw
}

// mapPriority maps OpsGenie P1–P5 to canonical Severity.
func (OpsGenieNormalizer) mapPriority(p string) Severity {
	switch strings.ToUpper(strings.TrimSpace(p)) {
	case "P1":
		return SeverityCritical
	case "P2":
		return SeverityHigh
	case "P3":
		return SeverityMedium
	case "P4":
		return SeverityLow
	case "P5":
		return SeverityInfo
	default:
		return SeverityMedium
	}
}

// mapStatus maps OpsGenie status to canonical Status.
func (OpsGenieNormalizer) mapStatus(s string) Status {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "closed", "acked":
		return StatusResolved
	default: // "open" and anything unrecognised
		return StatusFiring
	}
}

// statusFromAction infers a status string from the webhook action name.
func (OpsGenieNormalizer) statusFromAction(action string) string {
	switch action {
	case "Close":
		return "closed"
	case "Acknowledge":
		return "acked"
	default:
		return "open"
	}
}

// extractTags converts the tags field ([]interface{} of strings) to []string.
func (OpsGenieNormalizer) extractTags(alert map[string]interface{}) []string {
	raw, _ := alert["tags"].([]interface{})
	out := make([]string, 0, len(raw))
	for _, t := range raw {
		if s, ok := t.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// extractTeams converts the teams field ([]interface{} of strings or objects) to []string.
func (OpsGenieNormalizer) extractTeams(alert map[string]interface{}) []string {
	raw, _ := alert["teams"].([]interface{})
	out := make([]string, 0, len(raw))
	for _, t := range raw {
		switch v := t.(type) {
		case string:
			if v != "" {
				out = append(out, v)
			}
		case map[string]interface{}:
			if name, ok := v["name"].(string); ok && name != "" {
				out = append(out, name)
			}
		}
	}
	return out
}

// parseTime attempts RFC3339 and unix-ms numeric parsing for OpsGenie timestamp fields.
func (OpsGenieNormalizer) parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	return time.Time{}
}

// OpsGeniePayloadToAlerts normalises the OpsGenie webhook envelope.
// OpsGenie delivers one alert event per POST.
func OpsGeniePayloadToAlerts(payload map[string]interface{}) []map[string]interface{} {
	return []map[string]interface{}{payload}
}
