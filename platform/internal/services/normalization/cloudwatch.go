package normalization

import (
	"encoding/json"
	"strings"
	"time"
)

// CloudWatchNormalizer handles AWS CloudWatch alarm webhooks.
//
// CloudWatch alarms reach AlertHub via SNS HTTP subscription.  The outer
// envelope is an SNS notification; the actual alarm detail is in the Message
// field, which is a JSON-encoded string.
type CloudWatchNormalizer struct{}

func (CloudWatchNormalizer) Source() string { return "cloudwatch" }

func (CloudWatchNormalizer) CanHandle(raw map[string]interface{}) bool {
	_, hasAlarmName := raw["AlarmName"]
	_, hasNewState := raw["NewStateValue"]
	// SNS outer envelope
	msgType, _ := raw["Type"].(string)
	subject, _ := raw["Subject"].(string)
	return hasAlarmName || hasNewState ||
		msgType == "Notification" ||
		strings.HasPrefix(subject, "ALARM:") ||
		strings.HasPrefix(subject, "OK:")
}

func (n CloudWatchNormalizer) Normalize(raw map[string]interface{}) (*NormalizedAlert, error) {
	alarm := n.unwrapSNS(raw)

	alarmName := strField(alarm, "AlarmName")
	alarmDesc := strField(alarm, "AlarmDescription")
	newState := strField(alarm, "NewStateValue")     // ALARM | OK | INSUFFICIENT_DATA
	stateReason := strField(alarm, "NewStateReason") // human-readable reason
	metricName := strField(alarm, "MetricName")
	namespace := strField(alarm, "Namespace") // e.g. AWS/EC2, AWS/RDS
	region := strField(alarm, "Region", "AWSAccountId")

	title := alarmName
	if title == "" {
		title = "CloudWatch Alarm"
	}

	description := coalesce(alarmDesc, stateReason, metricName)

	severity := n.severityFromState(newState)
	status := n.mapStatus(newState)

	// Dimensions: [{name: "InstanceId", value: "i-xxxxx"}, ...]
	dimLabels := extractCWDimensions(alarm)

	entity := MergeEntityInfo(
		ExtractFromLabels(dimLabels),
		ExtractFromText(title+" "+description+" "+namespace),
	)

	fp := FingerprintFromSourceID("cloudwatch", alarmName)
	if fp == "" {
		fp = Fingerprint(entity.EntityID, metricName, title)
	}

	var firedAt time.Time
	if s := strField(alarm, "StateChangeTime"); s != "" {
		// CloudWatch uses RFC3339 with millis: "2022-01-01T00:00:00.000+0000"
		firedAt, _ = time.Parse("2006-01-02T15:04:05.000+0000", s)
		if firedAt.IsZero() {
			firedAt, _ = time.Parse(time.RFC3339, s)
		}
	}
	if firedAt.IsZero() {
		firedAt = time.Now()
	}

	var resolvedAt *time.Time
	if strings.ToUpper(newState) == "OK" {
		t := firedAt
		resolvedAt = &t
	}

	// Infer service from AWS namespace
	service := n.serviceFromNamespace(namespace)
	if entity.Service == "" {
		entity.Service = service
	}

	labels := make(map[string]string)
	for k, v := range dimLabels {
		labels[k] = v
	}
	labels["source"] = "cloudwatch"
	setLabel(labels, "alarm_name", alarmName)
	setLabel(labels, "namespace", namespace)
	setLabel(labels, "metric_name", metricName)
	setLabel(labels, "new_state", newState)
	setLabel(labels, "region", region)
	setLabel(labels, "cluster", entity.Cluster)
	setLabel(labels, "service", entity.Service)

	meta := map[string]interface{}{
		"alarm_name":    alarmName,
		"metric_name":   metricName,
		"namespace":     namespace,
		"new_state":     newState,
		"state_reason":  stateReason,
		"region":        region,
	}
	if v, ok := alarm["Trigger"]; ok {
		meta["trigger"] = v
	}
	if v, ok := alarm["AWSAccountId"]; ok {
		meta["aws_account_id"] = v
	}

	return &NormalizedAlert{
		SourceID:    alarmName,
		Source:      "cloudwatch",
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
		MetricName:  metricName,
		Region:      region,
		Fingerprint: fp,
		FiredAt:     firedAt,
		ResolvedAt:  resolvedAt,
		Labels:      labels,
		Metadata:    meta,
		Raw:         raw,
	}, nil
}

// unwrapSNS extracts the inner CloudWatch alarm object from an SNS notification
// envelope.  If the payload is already a plain alarm object, it is returned as-is.
func (CloudWatchNormalizer) unwrapSNS(raw map[string]interface{}) map[string]interface{} {
	msg, _ := raw["Message"].(string)
	if msg == "" {
		return raw
	}
	var inner map[string]interface{}
	if err := json.Unmarshal([]byte(msg), &inner); err != nil {
		return raw
	}
	// Verify it looks like a CloudWatch alarm
	if _, ok := inner["AlarmName"]; ok {
		return inner
	}
	return raw
}

func (CloudWatchNormalizer) severityFromState(state string) Severity {
	switch strings.ToUpper(state) {
	case "ALARM":
		return SeverityHigh
	case "INSUFFICIENT_DATA":
		return SeverityMedium
	case "OK":
		return SeverityInfo
	default:
		return SeverityMedium
	}
}

func (CloudWatchNormalizer) mapStatus(state string) Status {
	if strings.ToUpper(state) == "OK" {
		return StatusResolved
	}
	return StatusFiring
}

func (CloudWatchNormalizer) serviceFromNamespace(ns string) string {
	parts := strings.SplitN(ns, "/", 2)
	if len(parts) == 2 {
		return strings.ToLower(parts[1]) // "EC2" "ec2"
	}
	return strings.ToLower(ns)
}

func extractCWDimensions(alarm map[string]interface{}) map[string]string {
	result := make(map[string]string)

	// Direct trigger dimensions: {"Dimensions":[{"name":"InstanceId","value":"i-xxx"}]}
	dims, _ := alarm["Dimensions"].([]interface{})
	if len(dims) == 0 {
		// Also check nested Trigger.Dimensions
		if trigger, ok := alarm["Trigger"].(map[string]interface{}); ok {
			dims, _ = trigger["Dimensions"].([]interface{})
		}
	}
	for _, d := range dims {
		m, ok := d.(map[string]interface{})
		if !ok {
			continue
		}
		k, _ := m["name"].(string)
		v, _ := m["value"].(string)
		if k == "" {
			k, _ = m["Name"].(string)
			v, _ = m["Value"].(string)
		}
		if k != "" && v != "" {
			result[strings.ToLower(k)] = v
		}
	}
	return result
}

// CloudWatchPayloadToAlerts normalises the CloudWatch/SNS webhook envelope.
// SNS delivers one alarm event per POST.
func CloudWatchPayloadToAlerts(payload map[string]interface{}) []map[string]interface{} {
	return []map[string]interface{}{payload}
}
