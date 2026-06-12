package normalization

import (
	"fmt"
	"strings"
	"time"
)

// DatadogNormalizer handles Datadog alert webhook payloads.
// Datadog sends a flat JSON object for each alert event.
type DatadogNormalizer struct{}

func (DatadogNormalizer) Source() string { return "datadog" }

func (DatadogNormalizer) CanHandle(raw map[string]interface{}) bool {
	_, hasTitle := raw["alert_title"]
	_, hasAggrKey := raw["aggreg_key"]
	_, hasAlertType := raw["alert_type"]
	eventType, _ := raw["event_type"].(string)
	return hasTitle || hasAggrKey || hasAlertType ||
		strings.HasPrefix(eventType, "monitor")
}

func (n DatadogNormalizer) Normalize(raw map[string]interface{}) (*NormalizedAlert, error) {
	title := coalesce(strField(raw, "alert_title", "title", "name"))
	if title == "" {
		title = "Datadog Alert"
	}

	description := coalesce(strField(raw, "body", "message", "text"))

	alertType := strField(raw, "alert_type")   // error | warning | info | success
	alertStatus := strField(raw, "alert_status") // alert | ok | warning | no data

	severity := MapSeverity(coalesce(alertType, alertStatus))
	status := n.mapStatus(alertStatus, alertType)

	sourceID := coalesce(strField(raw, "aggreg_key", "id", "event_id"))
	if sourceID == "" {
		sourceID = fmt.Sprintf("dd-%d", time.Now().UnixNano())
	}

	sourceURL := strField(raw, "url", "event_url")
	host := strField(raw, "host", "hostname")
	metricName := strField(raw, "metric", "check")

	// Datadog tags: ["key:value", "key2:value2", ...]
	labelsFromTags := extractDatadogTags(raw)

	entity := MergeEntityInfo(
		ExtractFromLabels(labelsFromTags),
		ExtractFromText(title+" "+description+" "+host),
	)
	if entity.Host == "" && host != "" {
		entity.Host = host
	}

	fp := FingerprintFromSourceID("datadog", sourceID)
	if fp == "" {
		fp = Fingerprint(entity.EntityID, metricName, title)
	}

	var firedAt time.Time
	if ts, ok := raw["date_happened"].(float64); ok && ts > 0 {
		firedAt = time.Unix(int64(ts), 0)
	}
	if firedAt.IsZero() {
		firedAt = time.Now()
	}

	labels := make(map[string]string)
	for k, v := range labelsFromTags {
		labels[k] = v
	}
	labels["source"] = "datadog"
	setLabel(labels, "alert_type", alertType)
	setLabel(labels, "alert_status", alertStatus)
	setLabel(labels, "host", host)
	setLabel(labels, "cluster", entity.Cluster)
	setLabel(labels, "namespace", entity.Namespace)
	setLabel(labels, "service", entity.Service)

	meta := map[string]interface{}{
		"aggreg_key":   sourceID,
		"alert_type":   alertType,
		"alert_status": alertStatus,
		"host":         host,
		"metric":       metricName,
	}
	if orgID, ok := raw["org_id"]; ok {
		meta["org_id"] = orgID
	}
	if monitorID, ok := raw["monitor_id"]; ok {
		meta["monitor_id"] = monitorID
	}

	return &NormalizedAlert{
		SourceID:    sourceID,
		Source:      "datadog",
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
		Host:        coalesce(entity.Host, host),
		Service:     entity.Service,
		Workload:    entity.Workload,
		MetricName:  metricName,
		Fingerprint: fp,
		FiredAt:     firedAt,
		Labels:      labels,
		Metadata:    meta,
		Raw:         raw,
	}, nil
}

func (DatadogNormalizer) mapStatus(alertStatus, alertType string) Status {
	combined := strings.ToLower(alertStatus + " " + alertType)
	if strings.Contains(combined, "ok") ||
		strings.Contains(combined, "success") ||
		strings.Contains(combined, "recovery") ||
		strings.Contains(combined, "resolved") {
		return StatusResolved
	}
	return StatusFiring
}

func extractDatadogTags(raw map[string]interface{}) map[string]string {
	result := make(map[string]string)
	tags, _ := raw["tags"].([]interface{})
	for _, t := range tags {
		s, ok := t.(string)
		if !ok {
			continue
		}
		parts := strings.SplitN(s, ":", 2)
		if len(parts) == 2 {
			result[parts[0]] = parts[1]
		}
	}
	return result
}

// DatadogPayloadToAlerts normalises the Datadog webhook envelope to per-alert maps.
// Datadog sends one alert per POST, so this always returns a single-element slice.
func DatadogPayloadToAlerts(payload map[string]interface{}) []map[string]interface{} {
	return []map[string]interface{}{payload}
}
