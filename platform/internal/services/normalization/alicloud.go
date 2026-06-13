package normalization

import (
	"strconv"
	"strings"
	"time"
)

// AliCloudCMSNormalizer handles Alibaba Cloud Monitor Service (CMS) alert webhook payloads.
//
// CMS fires one alert object per POST with fields like alertName, alertState,
// namespace, metricName, instanceName, resourceId, regionId, dimensions,
// curValue, expression, level, ruleId, and a unix-ms timestamp string.
type AliCloudCMSNormalizer struct{}

func (AliCloudCMSNormalizer) Source() string { return "alicloud_cms" }

func (AliCloudCMSNormalizer) CanHandle(raw map[string]interface{}) bool {
	_, hasAlertName := raw["alertName"]
	if !hasAlertName {
		return false
	}
	// Discriminate from other sources: namespace contains "acs_" OR metricName
	// is present without a schemaId (which would indicate Dynatrace/New Relic).
	if ns, ok := raw["namespace"].(string); ok && strings.Contains(ns, "acs_") {
		return true
	}
	_, hasMetric := raw["metricName"]
	_, hasSchemaID := raw["schemaId"]
	return hasMetric && !hasSchemaID
}

func (n AliCloudCMSNormalizer) Normalize(raw map[string]interface{}) (*NormalizedAlert, error) {
	alertName := strField(raw, "alertName")
	alertState := strField(raw, "alertState") // ALARM | OK | INSUFFICIENT_DATA
	curValue := strField(raw, "curValue")
	metricName := strField(raw, "metricName")
	namespace := strField(raw, "namespace")   // e.g. acs_ecs_dashboard
	instanceName := strField(raw, "instanceName")
	resourceID := strField(raw, "resourceId")
	regionID := strField(raw, "regionId")
	expression := strField(raw, "expression")
	level := strField(raw, "level")   // CRITICAL | WARNING | INFO
	ruleID := strField(raw, "ruleId")

	title := alertName
	if title == "" {
		title = "AliCloud CMS Alert"
	}

	description := coalesce(expression, metricName)
	if curValue != "" {
		if description != "" {
			description = description + " (current: " + curValue + ")"
		} else {
			description = "current value: " + curValue
		}
	}

	severity := n.mapLevel(level)
	status := n.mapState(alertState)

	entityType := n.entityTypeFromNamespace(namespace)
	entityName := coalesce(instanceName, resourceID)

	labels := map[string]string{
		"source":    "alicloud_cms",
		"namespace": namespace,
		"metric":    metricName,
		"rule_id":   ruleID,
	}
	setLabel(labels, "instance_name", instanceName)
	setLabel(labels, "resource_id", resourceID)
	setLabel(labels, "region", regionID)
	setLabel(labels, "alert_state", alertState)
	setLabel(labels, "level", level)

	// Dimensions: may be a string "key=value,key=value" or a map.
	dimLabels := n.extractDimensions(raw)
	for k, v := range dimLabels {
		if labels[k] == "" {
			labels[k] = v
		}
	}

	entity := MergeEntityInfo(
		ExtractFromLabels(dimLabels),
		ExtractFromText(title+" "+description),
	)
	if entity.EntityType == "" {
		entity.EntityType = entityType
	}
	if entity.EntityName == "" {
		entity.EntityName = entityName
	}
	if entity.EntityID == "" && entityName != "" {
		entity.EntityID = entityType + "/" + entityName
	}

	fp := FingerprintFromSourceID("alicloud_cms", ruleID)
	if fp == "" {
		fp = FingerprintFromSourceID("alicloud_cms", alertName)
	}
	if fp == "" {
		fp = Fingerprint(entity.EntityID, metricName, title)
	}

	firedAt := n.parseTimestamp(strField(raw, "timestamp"))
	if firedAt.IsZero() {
		firedAt = time.Now()
	}

	var resolvedAt *time.Time
	if strings.ToUpper(alertState) == "OK" || strings.ToUpper(alertState) == "INSUFFICIENT_DATA" {
		t := firedAt
		resolvedAt = &t
	}

	meta := map[string]interface{}{
		"alert_name":    alertName,
		"alert_state":   alertState,
		"metric_name":   metricName,
		"namespace":     namespace,
		"instance_name": instanceName,
		"resource_id":   resourceID,
		"region_id":     regionID,
		"level":         level,
		"rule_id":       ruleID,
		"cur_value":     curValue,
		"expression":    expression,
	}
	if v, ok := raw["dimensions"]; ok {
		meta["dimensions"] = v
	}

	return &NormalizedAlert{
		SourceID:    coalesce(ruleID, alertName),
		Source:      "alicloud_cms",
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
		Region:      regionID,
		MetricName:  metricName,
		Fingerprint: fp,
		FiredAt:     firedAt,
		ResolvedAt:  resolvedAt,
		Labels:      labels,
		Metadata:    meta,
		Raw:         raw,
	}, nil
}

// mapState converts CMS alertState to canonical Status.
func (AliCloudCMSNormalizer) mapState(state string) Status {
	switch strings.ToUpper(state) {
	case "OK", "INSUFFICIENT_DATA":
		return StatusResolved
	default: // "ALARM" and anything unrecognised
		return StatusFiring
	}
}

// mapLevel converts CMS level to canonical Severity.
func (AliCloudCMSNormalizer) mapLevel(level string) Severity {
	switch strings.ToUpper(level) {
	case "CRITICAL":
		return SeverityCritical
	case "WARN", "WARNING":
		return SeverityMedium
	case "INFO":
		return SeverityInfo
	default:
		return SeverityMedium
	}
}

// entityTypeFromNamespace maps AliCloud CMS namespace prefixes to entity types.
func (AliCloudCMSNormalizer) entityTypeFromNamespace(ns string) string {
	switch {
	case strings.HasPrefix(ns, "acs_ecs_dashboard"):
		return "ecs_instance"
	case strings.HasPrefix(ns, "acs_k8s"):
		return "k8s_node"
	case strings.HasPrefix(ns, "acs_rds_dashboard"):
		return "rds_instance"
	case strings.HasPrefix(ns, "acs_slb_dashboard"):
		return "load_balancer"
	default:
		return "alicloud_resource"
	}
}

// extractDimensions parses the dimensions field which can be a flat string
// ("instanceId=i-xxx,userId=123") or a map[string]interface{}.
func (AliCloudCMSNormalizer) extractDimensions(raw map[string]interface{}) map[string]string {
	out := make(map[string]string)
	dims, ok := raw["dimensions"]
	if !ok {
		return out
	}
	switch d := dims.(type) {
	case map[string]interface{}:
		for k, v := range d {
			out[strings.ToLower(k)] = strFromAny(v)
		}
	case string:
		// "key=value,key=value" or "key=value;key=value"
		for _, pair := range strings.FieldsFunc(d, func(r rune) bool { return r == ',' || r == ';' }) {
			parts := strings.SplitN(strings.TrimSpace(pair), "=", 2)
			if len(parts) == 2 && parts[0] != "" {
				out[strings.ToLower(parts[0])] = parts[1]
			}
		}
	}
	return out
}

// parseTimestamp handles unix millisecond strings from the CMS timestamp field.
func (AliCloudCMSNormalizer) parseTimestamp(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	ms, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		// Try RFC3339 as a fallback
		if t, err2 := time.Parse(time.RFC3339, s); err2 == nil {
			return t
		}
		return time.Time{}
	}
	return time.Unix(ms/1000, (ms%1000)*int64(time.Millisecond))
}

// AliCloudCMSPayloadToAlerts normalises the AliCloud CMS webhook envelope.
// CMS delivers one alert event per POST.
func AliCloudCMSPayloadToAlerts(payload map[string]interface{}) []map[string]interface{} {
	return []map[string]interface{}{payload}
}
