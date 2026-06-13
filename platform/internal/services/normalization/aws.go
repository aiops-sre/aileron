package normalization

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// AWSCloudWatchNormalizer
// ---------------------------------------------------------------------------

// AWSCloudWatchNormalizer handles AWS CloudWatch alarm webhooks delivered directly
// (not via SNS envelope) using the "aws_cloudwatch" source tag.
//
// Expected payload shape:
//
//	{
//	  "AlarmName":        "...",
//	  "AlarmDescription": "...",
//	  "NewStateValue":    "ALARM" | "OK" | "INSUFFICIENT_DATA",
//	  "NewStateReason":   "...",
//	  "StateChangeTime":  "<RFC3339>",
//	  "Region":           "us-east-1",
//	  "AWSAccountId":     "123456789012",
//	  "AlarmArn":         "arn:aws:cloudwatch:...",
//	  "Trigger": {
//	    "MetricName":  "CPUUtilization",
//	    "Namespace":   "AWS/EC2",
//	    "Dimensions": [{"name":"InstanceId","value":"i-0abc123"}]
//	  }
//	}
type AWSCloudWatchNormalizer struct{}

func (AWSCloudWatchNormalizer) Source() string { return "aws_cloudwatch" }

func (AWSCloudWatchNormalizer) CanHandle(raw map[string]interface{}) bool {
	_, ok := raw["AlarmName"]
	return ok
}

func (n AWSCloudWatchNormalizer) Normalize(raw map[string]interface{}) (*NormalizedAlert, error) {
	alarmName := strField(raw, "AlarmName")
	alarmDesc := strField(raw, "AlarmDescription")
	newState := strField(raw, "NewStateValue")
	stateReason := strField(raw, "NewStateReason")
	region := strField(raw, "Region")
	accountID := strField(raw, "AWSAccountId")
	alarmArn := strField(raw, "AlarmArn")

	// Pull Trigger sub-object
	var trigger map[string]interface{}
	if t, ok := raw["Trigger"].(map[string]interface{}); ok {
		trigger = t
	}

	metricName := strField(trigger, "MetricName")
	namespace := strField(trigger, "Namespace")

	// First Dimension value → EntityName
	entityName := n.firstDimensionValue(trigger)

	title := alarmName
	if title == "" {
		title = "CloudWatch Alarm"
	}
	description := coalesce(alarmDesc, stateReason, metricName)

	status := n.mapStatus(newState)
	severity := n.severityFromNamespace(namespace)

	entityType := n.entityTypeFromNamespace(namespace)

	// EntityID: namespace/entityName or namespace/alarmName as fallback
	entityID := namespace + "/" + coalesce(entityName, alarmName)

	var firedAt time.Time
	if s := strField(raw, "StateChangeTime"); s != "" {
		firedAt, _ = time.Parse(time.RFC3339, s)
		if firedAt.IsZero() {
			firedAt, _ = time.Parse("2006-01-02T15:04:05.000+0000", s)
		}
	}
	if firedAt.IsZero() {
		firedAt = time.Now()
	}

	var resolvedAt *time.Time
	if strings.ToUpper(newState) == "OK" || strings.ToUpper(newState) == "INSUFFICIENT_DATA" {
		t := firedAt
		resolvedAt = &t
	}

	labels := map[string]string{
		"aws_account": accountID,
		"alarm_arn":   alarmArn,
		"metric":      metricName,
	}
	setLabel(labels, "source", "aws_cloudwatch")
	setLabel(labels, "region", region)
	setLabel(labels, "namespace", namespace)
	setLabel(labels, "alarm_name", alarmName)
	setLabel(labels, "new_state", newState)

	meta := map[string]interface{}{
		"alarm_name":    alarmName,
		"new_state":     newState,
		"state_reason":  stateReason,
		"namespace":     namespace,
		"metric_name":   metricName,
		"aws_account_id": accountID,
		"alarm_arn":     alarmArn,
	}
	if trigger != nil {
		meta["trigger"] = trigger
	}

	fp := FingerprintFromSourceID("aws_cloudwatch", alarmName)
	if fp == "" {
		fp = Fingerprint(entityID, metricName, title)
	}

	return &NormalizedAlert{
		SourceID:    alarmName,
		Source:      "aws_cloudwatch",
		SourceURL:   fmt.Sprintf("https://console.aws.amazon.com/cloudwatch/home?region=%s#alarmsV2:alarm/%s", region, alarmName),
		Title:       title,
		Description: description,
		Severity:    severity,
		Status:      status,
		EntityID:    entityID,
		EntityType:  entityType,
		EntityName:  coalesce(entityName, alarmName),
		Region:      region,
		MetricName:  metricName,
		Fingerprint: fp,
		FiredAt:     firedAt,
		ResolvedAt:  resolvedAt,
		Labels:      labels,
		Metadata:    meta,
		Raw:         raw,
	}, nil
}

func (AWSCloudWatchNormalizer) mapStatus(state string) Status {
	switch strings.ToUpper(state) {
	case "OK", "INSUFFICIENT_DATA":
		return StatusResolved
	default:
		return StatusFiring
	}
}

func (AWSCloudWatchNormalizer) severityFromNamespace(ns string) Severity {
	switch strings.ToUpper(ns) {
	case "AWS/RDS", "AWS/AURORA", "AWS/DOCDB":
		return SeverityHigh
	case "AWS/EC2", "AWS/EKS", "AWS/ECS", "AWS/LAMBDA":
		return SeverityMedium
	case "AWS/ELB", "AWS/ALB", "AWS/NLB", "AWS/ELBV2":
		return SeverityMedium
	case "AWS/APIGATEWAY", "AWS/APPSYNC":
		return SeverityMedium
	case "AWS/S3", "AWS/DYNAMODB", "AWS/ELASTICACHE":
		return SeverityMedium
	default:
		return SeverityMedium
	}
}

func (AWSCloudWatchNormalizer) entityTypeFromNamespace(ns string) string {
	switch strings.ToUpper(ns) {
	case "AWS/EC2":
		return "ec2_instance"
	case "AWS/EKS":
		return "k8s_node"
	case "AWS/RDS", "AWS/AURORA", "AWS/DOCDB":
		return "rds_instance"
	case "AWS/LAMBDA":
		return "lambda_function"
	case "AWS/ELB", "AWS/ALB", "AWS/NLB", "AWS/ELBV2":
		return "load_balancer"
	case "AWS/ECS":
		return "ecs_service"
	case "AWS/S3":
		return "s3_bucket"
	case "AWS/DYNAMODB":
		return "dynamodb_table"
	case "AWS/ELASTICACHE":
		return "elasticache_cluster"
	default:
		return "aws_resource"
	}
}

// firstDimensionValue returns the Value of the first entry in Trigger.Dimensions.
func (AWSCloudWatchNormalizer) firstDimensionValue(trigger map[string]interface{}) string {
	if trigger == nil {
		return ""
	}
	dims, _ := trigger["Dimensions"].([]interface{})
	if len(dims) == 0 {
		return ""
	}
	if m, ok := dims[0].(map[string]interface{}); ok {
		if v, _ := m["value"].(string); v != "" {
			return v
		}
		if v, _ := m["Value"].(string); v != "" {
			return v
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// AWSGuardDutyNormalizer
// ---------------------------------------------------------------------------

// AWSGuardDutyNormalizer handles AWS GuardDuty findings delivered via EventBridge.
//
// Expected payload shape (EventBridge event envelope):
//
//	{
//	  "source":      "aws.guardduty",
//	  "detail-type": "GuardDuty Finding",
//	  "detail": {
//	    "findings": [{
//	      "Title":       "...",
//	      "Description": "...",
//	      "Severity":    7.5,
//	      "Region":      "us-east-1",
//	      "AccountId":   "123456789012",
//	      "Type":        "Recon:EC2/PortProbeUnprotectedPort",
//	      "Resource": {
//	        "ResourceType":     "Instance",
//	        "InstanceDetails":  {"InstanceId": "i-0abc123"}
//	      }
//	    }]
//	  }
//	}
type AWSGuardDutyNormalizer struct{}

func (AWSGuardDutyNormalizer) Source() string { return "aws_guardduty" }

func (AWSGuardDutyNormalizer) CanHandle(raw map[string]interface{}) bool {
	if dt, _ := raw["detail-type"].(string); dt == "GuardDuty Finding" {
		return true
	}
	if src, _ := raw["source"].(string); src == "aws.guardduty" {
		return true
	}
	return false
}

func (n AWSGuardDutyNormalizer) Normalize(raw map[string]interface{}) (*NormalizedAlert, error) {
	finding := n.extractFinding(raw)

	title := strField(finding, "Title")
	if title == "" {
		title = "GuardDuty Finding"
	}
	description := strField(finding, "Description")
	region := strField(finding, "Region")
	accountID := strField(finding, "AccountId")
	findingType := strField(finding, "Type")
	findingID := strField(finding, "Id")

	severity := n.mapSeverity(finding)

	// Resource details
	resourceType, instanceID := n.extractResource(finding)
	entityType := n.entityTypeFromResource(resourceType)

	entityName := coalesce(instanceID, findingType)
	entityID := fmt.Sprintf("aws_guardduty/%s/%s", accountID, coalesce(instanceID, findingID, findingType))

	labels := map[string]string{
		"finding_type": findingType,
		"aws_account":  accountID,
	}
	setLabel(labels, "source", "aws_guardduty")
	setLabel(labels, "region", region)
	setLabel(labels, "resource_type", resourceType)
	setLabel(labels, "instance_id", instanceID)

	meta := map[string]interface{}{
		"finding_type": findingType,
		"finding_id":   findingID,
		"account_id":   accountID,
		"region":       region,
		"resource_type": resourceType,
	}
	if finding != nil {
		meta["raw_finding"] = finding
	}

	fp := FingerprintFromSourceID("aws_guardduty", coalesce(findingID, findingType+"/"+instanceID))
	if fp == "" {
		fp = Fingerprint(entityID, findingType, title)
	}

	return &NormalizedAlert{
		SourceID:    coalesce(findingID, findingType),
		Source:      "aws_guardduty",
		SourceURL:   fmt.Sprintf("https://console.aws.amazon.com/guardduty/home?region=%s#/findings", region),
		Title:       title,
		Description: description,
		Severity:    severity,
		Status:      StatusFiring,
		EntityID:    entityID,
		EntityType:  entityType,
		EntityName:  entityName,
		Region:      region,
		Fingerprint: fp,
		FiredAt:     time.Now(),
		Labels:      labels,
		Metadata:    meta,
		Raw:         raw,
	}, nil
}

// extractFinding digs the first entry out of detail.findings[].
func (AWSGuardDutyNormalizer) extractFinding(raw map[string]interface{}) map[string]interface{} {
	detail, _ := raw["detail"].(map[string]interface{})
	if detail == nil {
		return raw
	}
	findings, _ := detail["findings"].([]interface{})
	if len(findings) > 0 {
		if f, ok := findings[0].(map[string]interface{}); ok {
			return f
		}
	}
	// Fallback: detail may contain the finding directly (some integrations)
	return detail
}

func (AWSGuardDutyNormalizer) mapSeverity(finding map[string]interface{}) Severity {
	var sev float64
	switch v := finding["Severity"].(type) {
	case float64:
		sev = v
	case int:
		sev = float64(v)
	case int64:
		sev = float64(v)
	}
	switch {
	case sev >= 7.0:
		return SeverityCritical
	case sev >= 4.0:
		return SeverityHigh
	case sev >= 2.0:
		return SeverityMedium
	default:
		return SeverityLow
	}
}

func (AWSGuardDutyNormalizer) extractResource(finding map[string]interface{}) (resourceType, instanceID string) {
	res, _ := finding["Resource"].(map[string]interface{})
	if res == nil {
		return "", ""
	}
	resourceType, _ = res["ResourceType"].(string)
	if inst, ok := res["InstanceDetails"].(map[string]interface{}); ok {
		instanceID, _ = inst["InstanceId"].(string)
	}
	if instanceID == "" {
		if s3, ok := res["S3BucketDetails"].([]interface{}); ok && len(s3) > 0 {
			if b, ok := s3[0].(map[string]interface{}); ok {
				instanceID, _ = b["Name"].(string)
			}
		}
	}
	return resourceType, instanceID
}

func (AWSGuardDutyNormalizer) entityTypeFromResource(resourceType string) string {
	switch resourceType {
	case "Instance":
		return "ec2_instance"
	case "S3Bucket":
		return "s3_bucket"
	case "AccessKey":
		return "iam_access_key"
	case "EKSCluster":
		return "k8s_cluster"
	case "Lambda":
		return "lambda_function"
	case "RDSDBInstance":
		return "rds_instance"
	case "Container":
		return "container"
	default:
		return "aws_resource"
	}
}

// ---------------------------------------------------------------------------
// AWSEventBridgeNormalizer
// ---------------------------------------------------------------------------

// AWSEventBridgeNormalizer handles generic EventBridge events from AWS services
// (excluding GuardDuty, which has its own normalizer).
//
// Expected payload shape:
//
//	{
//	  "source":      "aws.ec2",
//	  "detail-type": "EC2 Instance State-change Notification",
//	  "region":      "us-east-1",
//	  "account":     "123456789012",
//	  "id":          "abc-123",
//	  "detail":      { ... }
//	}
type AWSEventBridgeNormalizer struct{}

func (AWSEventBridgeNormalizer) Source() string { return "aws_eventbridge" }

func (AWSEventBridgeNormalizer) CanHandle(raw map[string]interface{}) bool {
	src, _ := raw["source"].(string)
	return strings.HasPrefix(src, "aws.") && src != "aws.guardduty"
}

func (n AWSEventBridgeNormalizer) Normalize(raw map[string]interface{}) (*NormalizedAlert, error) {
	source := strField(raw, "source")
	detailType := strField(raw, "detail-type")
	region := strField(raw, "region")
	account := strField(raw, "account")
	eventID := strField(raw, "id")
	timeStr := strField(raw, "time")

	title := detailType
	if title == "" {
		title = source + " Event"
	}

	description := n.detailToJSON(raw["detail"])

	var firedAt time.Time
	if timeStr != "" {
		firedAt, _ = time.Parse(time.RFC3339, timeStr)
	}
	if firedAt.IsZero() {
		firedAt = time.Now()
	}

	entityID := fmt.Sprintf("%s/%s/%s", source, region, coalesce(eventID, detailType))

	labels := map[string]string{
		"source":      source,
		"detail_type": detailType,
	}
	setLabel(labels, "region", region)
	setLabel(labels, "aws_account", account)
	setLabel(labels, "event_id", eventID)

	meta := map[string]interface{}{
		"source":      source,
		"detail_type": detailType,
		"region":      region,
		"account":     account,
		"event_id":    eventID,
	}
	if raw["detail"] != nil {
		meta["detail"] = raw["detail"]
	}

	fp := FingerprintFromSourceID("aws_eventbridge", coalesce(eventID, source+"/"+detailType))
	if fp == "" {
		fp = Fingerprint(entityID, "", title)
	}

	return &NormalizedAlert{
		SourceID:    coalesce(eventID, source),
		Source:      "aws_eventbridge",
		Title:       title,
		Description: description,
		Severity:    SeverityInfo,
		Status:      StatusFiring,
		EntityID:    entityID,
		EntityType:  "aws_resource",
		EntityName:  coalesce(detailType, source),
		Region:      region,
		Fingerprint: fp,
		FiredAt:     firedAt,
		Labels:      labels,
		Metadata:    meta,
		Raw:         raw,
	}, nil
}

// detailToJSON serialises the EventBridge detail sub-object to a JSON string
// for use as Description.  Falls back gracefully on nil or non-serialisable input.
func (AWSEventBridgeNormalizer) detailToJSON(detail interface{}) string {
	if detail == nil {
		return ""
	}
	b, err := json.Marshal(detail)
	if err != nil {
		return fmt.Sprintf("%v", detail)
	}
	return string(b)
}
