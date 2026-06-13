package normalization

import (
	"strings"
	"time"
)

// ─── GCP Cloud Monitoring ────────────────────────────────────────────────────

// GCPMonitoringNormalizer handles GCP Cloud Monitoring incident notifications.
// Payloads are delivered via Pub/Sub push or HTTP webhook from a notification
// channel of type "pubsub" or "webhook".  The top-level object always contains
// an "incident" key with a nested "state" field.
type GCPMonitoringNormalizer struct{}

func (GCPMonitoringNormalizer) Source() string { return "gcp_monitoring" }

func (GCPMonitoringNormalizer) CanHandle(raw map[string]interface{}) bool {
	inc, ok := raw["incident"].(map[string]interface{})
	if !ok {
		return false
	}
	_, hasState := inc["state"]
	return hasState
}

func (n GCPMonitoringNormalizer) Normalize(raw map[string]interface{}) (*NormalizedAlert, error) {
	inc, _ := raw["incident"].(map[string]interface{})
	if inc == nil {
		inc = map[string]interface{}{}
	}

	incidentID := strField(inc, "incident_id")
	state := strField(inc, "state")         // "open" | "closed"
	summary := strField(inc, "summary")
	incURL := strField(inc, "url")
	policyName := strField(inc, "policy_name")
	gcpSeverity := strField(inc, "severity")

	// Nested condition
	var conditionName string
	if cond, ok := inc["condition"].(map[string]interface{}); ok {
		conditionName = strField(cond, "name")
	}

	// Nested metric
	var metricType string
	if metric, ok := inc["metric"].(map[string]interface{}); ok {
		metricType = strField(metric, "type")
	}

	// Nested resource
	var resourceType string
	resourceLabels := map[string]string{}
	if res, ok := inc["resource"].(map[string]interface{}); ok {
		resourceType = strField(res, "type")
		if rl, ok := res["labels"].(map[string]interface{}); ok {
			for k, v := range rl {
				if s, ok := v.(string); ok && s != "" {
					resourceLabels[k] = s
				}
			}
		}
	}

	// Title
	title := coalesce(summary, conditionName, policyName, "GCP Cloud Monitoring Incident")
	if len(title) > 200 {
		title = title[:200] + "…"
	}

	description := coalesce(summary, conditionName)

	severity := n.mapSeverity(gcpSeverity)
	status := n.mapState(state)

	// FiredAt from started_at (unix timestamp)
	var firedAt time.Time
	switch v := inc["started_at"].(type) {
	case float64:
		firedAt = time.Unix(int64(v), 0)
	case int64:
		firedAt = time.Unix(v, 0)
	}
	if firedAt.IsZero() {
		firedAt = time.Now()
	}

	var resolvedAt *time.Time
	if status == StatusResolved {
		t := firedAt
		resolvedAt = &t
	}

	// Entity mapping
	entityType := n.entityTypeFromResource(resourceType)
	entityName := n.entityNameFromLabels(resourceType, resourceLabels)
	region := n.regionFromLabels(resourceLabels)
	projectID := resourceLabels["project_id"]

	fp := FingerprintFromSourceID("gcp_monitoring", incidentID)
	if fp == "" {
		fp = Fingerprint(entityName, metricType, title)
	}

	labels := map[string]string{
		"source": "gcp_monitoring",
	}
	setLabel(labels, "gcp_project", projectID)
	setLabel(labels, "metric_type", metricType)
	setLabel(labels, "resource_type", resourceType)
	setLabel(labels, "policy_name", policyName)
	setLabel(labels, "region", region)
	for k, v := range resourceLabels {
		setLabel(labels, k, v)
	}

	meta := map[string]interface{}{
		"incident_id":    incidentID,
		"state":          state,
		"policy_name":    policyName,
		"condition_name": conditionName,
		"metric_type":    metricType,
		"resource_type":  resourceType,
		"resource_labels": resourceLabels,
	}
	if incURL != "" {
		meta["url"] = incURL
	}

	return &NormalizedAlert{
		SourceID:    incidentID,
		Source:      "gcp_monitoring",
		SourceURL:   incURL,
		Title:       title,
		Description: description,
		Severity:    severity,
		Status:      status,
		EntityType:  entityType,
		EntityName:  entityName,
		EntityID:    entityName,
		Region:      region,
		MetricName:  metricType,
		Fingerprint: fp,
		FiredAt:     firedAt,
		ResolvedAt:  resolvedAt,
		Labels:      labels,
		Metadata:    meta,
		Raw:         raw,
	}, nil
}

func (GCPMonitoringNormalizer) mapState(state string) Status {
	if strings.ToLower(state) == "closed" {
		return StatusResolved
	}
	return StatusFiring
}

func (GCPMonitoringNormalizer) mapSeverity(s string) Severity {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "CRITICAL":
		return SeverityCritical
	case "ERROR":
		return SeverityHigh
	case "WARNING":
		return SeverityMedium
	case "NOTICE":
		return SeverityLow
	default:
		return SeverityMedium
	}
}

// entityTypeFromResource maps a GCP monitored-resource type to the canonical entity type.
func (GCPMonitoringNormalizer) entityTypeFromResource(resourceType string) string {
	switch resourceType {
	case "gce_instance":
		return "gce_instance"
	case "k8s_container", "k8s_pod":
		return "k8s_pod"
	case "k8s_node":
		return "k8s_node"
	case "k8s_cluster":
		return "k8s_cluster"
	case "cloud_sql_database":
		return "cloud_sql"
	case "gcs_bucket":
		return "gcs_bucket"
	case "cloud_run_revision":
		return "cloud_run"
	case "pubsub_subscription", "pubsub_topic":
		return "pubsub"
	default:
		if resourceType != "" {
			return resourceType
		}
		return "gcp_resource"
	}
}

// entityNameFromLabels picks the most meaningful entity name from resource labels.
func (GCPMonitoringNormalizer) entityNameFromLabels(resourceType string, labels map[string]string) string {
	switch resourceType {
	case "gce_instance":
		return coalesce(labels["instance_id"], labels["instance_name"])
	case "k8s_container":
		return coalesce(labels["container_name"], labels["pod_name"])
	case "k8s_pod":
		return coalesce(labels["pod_name"], labels["container_name"])
	case "k8s_node":
		return labels["node_name"]
	case "k8s_cluster":
		return labels["cluster_name"]
	case "cloud_sql_database":
		return coalesce(labels["database_id"], labels["instance_id"])
	case "gcs_bucket":
		return labels["bucket_name"]
	case "cloud_run_revision":
		return coalesce(labels["revision_name"], labels["service_name"])
	}
	// Generic fallback: try common label names in priority order.
	return coalesce(
		labels["instance_id"],
		labels["container_name"],
		labels["database_id"],
		labels["bucket_name"],
		labels["cluster_name"],
		labels["node_name"],
		labels["project_id"],
	)
}

// regionFromLabels derives a region string from resource labels.
// GCP zones follow the pattern "<region>-<letter>" (e.g. "us-central1-a").
func (GCPMonitoringNormalizer) regionFromLabels(labels map[string]string) string {
	if loc := labels["location"]; loc != "" {
		return loc
	}
	if zone := labels["zone"]; zone != "" {
		// Strip trailing zone suffix: "us-central1-a" → "us-central1"
		if idx := strings.LastIndex(zone, "-"); idx > 0 {
			return zone[:idx]
		}
		return zone
	}
	return labels["region"]
}

// ─── GCP Security Command Center ────────────────────────────────────────────

// GCPSecurityCommandCenterNormalizer handles SCC finding notifications.
// Notifications arrive via Pub/Sub and contain a "finding" object with a
// "category" field.  Alternatively, the outer envelope may expose a
// "notificationConfigName" field.
type GCPSecurityCommandCenterNormalizer struct{}

func (GCPSecurityCommandCenterNormalizer) Source() string { return "gcp_scc" }

func (GCPSecurityCommandCenterNormalizer) CanHandle(raw map[string]interface{}) bool {
	if _, ok := raw["notificationConfigName"]; ok {
		return true
	}
	finding, ok := raw["finding"].(map[string]interface{})
	if !ok {
		return false
	}
	_, hasCategory := finding["category"]
	return hasCategory
}

func (n GCPSecurityCommandCenterNormalizer) Normalize(raw map[string]interface{}) (*NormalizedAlert, error) {
	finding, _ := raw["finding"].(map[string]interface{})
	if finding == nil {
		finding = map[string]interface{}{}
	}

	findingName := strField(finding, "name")
	category := strField(finding, "category")
	gcpSeverity := strField(finding, "severity")   // CRITICAL | HIGH | MEDIUM | LOW
	state := strField(finding, "state")             // ACTIVE | INACTIVE
	createTime := strField(finding, "createTime")
	resourceName := strField(finding, "resourceName")
	description := strField(finding, "description")

	title := coalesce(category, findingName, "GCP Security Finding")
	if len(title) > 200 {
		title = title[:200] + "…"
	}

	severity := n.mapSeverity(gcpSeverity)
	status := n.mapState(state)

	// FiredAt from createTime (RFC3339)
	var firedAt time.Time
	if createTime != "" {
		firedAt, _ = time.Parse(time.RFC3339Nano, createTime)
		if firedAt.IsZero() {
			firedAt, _ = time.Parse(time.RFC3339, createTime)
		}
	}
	if firedAt.IsZero() {
		firedAt = time.Now()
	}

	var resolvedAt *time.Time
	if status == StatusResolved {
		t := firedAt
		resolvedAt = &t
	}

	entityName := n.entityNameFromResource(resourceName)
	entityType := n.entityTypeFromResource(resourceName)

	// Source ID: last segment of the finding name (projects/.../findings/<id>)
	sourceID := findingName
	if idx := strings.LastIndex(findingName, "/"); idx >= 0 {
		sourceID = findingName[idx+1:]
	}

	fp := FingerprintFromSourceID("gcp_scc", sourceID)
	if fp == "" {
		fp = Fingerprint(entityName, category, title)
	}

	labels := map[string]string{
		"source": "gcp_scc",
	}
	setLabel(labels, "finding_category", category)
	setLabel(labels, "finding_name", findingName)
	setLabel(labels, "resource_name", resourceName)
	setLabel(labels, "entity_type", entityType)

	// Project from notificationConfigName or resourceName
	if proj := n.projectFromConfig(raw); proj != "" {
		setLabel(labels, "gcp_project", proj)
	}

	meta := map[string]interface{}{
		"finding_name":  findingName,
		"category":      category,
		"severity":      gcpSeverity,
		"state":         state,
		"resource_name": resourceName,
		"create_time":   createTime,
	}
	if cfgName, ok := raw["notificationConfigName"].(string); ok {
		meta["notification_config_name"] = cfgName
	}

	return &NormalizedAlert{
		SourceID:    sourceID,
		Source:      "gcp_scc",
		Title:       title,
		Description: description,
		Severity:    severity,
		Status:      status,
		EntityType:  entityType,
		EntityName:  entityName,
		EntityID:    entityName,
		Fingerprint: fp,
		FiredAt:     firedAt,
		ResolvedAt:  resolvedAt,
		Labels:      labels,
		Metadata:    meta,
		Raw:         raw,
	}, nil
}

func (GCPSecurityCommandCenterNormalizer) mapSeverity(s string) Severity {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "CRITICAL":
		return SeverityCritical
	case "HIGH":
		return SeverityHigh
	case "MEDIUM":
		return SeverityMedium
	case "LOW":
		return SeverityLow
	default:
		return SeverityMedium
	}
}

func (GCPSecurityCommandCenterNormalizer) mapState(state string) Status {
	if strings.ToUpper(state) == "INACTIVE" {
		return StatusResolved
	}
	return StatusFiring
}

// entityNameFromResource extracts the last meaningful path segment from a GCP
// resource name, e.g.:
//
//	"//compute.googleapis.com/projects/my-proj/zones/us-central1-a/instances/my-vm"
//	→ "my-vm"
func (GCPSecurityCommandCenterNormalizer) entityNameFromResource(resourceName string) string {
	if resourceName == "" {
		return ""
	}
	// Strip scheme prefix "//compute.googleapis.com"
	path := resourceName
	if idx := strings.Index(path, "/projects/"); idx >= 0 {
		path = path[idx:]
	}
	// Last non-empty segment
	parts := strings.Split(strings.TrimRight(path, "/"), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] != "" {
			return parts[i]
		}
	}
	return resourceName
}

// entityTypeFromResource infers an entity type from the resource path segments.
// It looks for well-known collection names.
func (GCPSecurityCommandCenterNormalizer) entityTypeFromResource(resourceName string) string {
	lower := strings.ToLower(resourceName)
	switch {
	case strings.Contains(lower, "/instances/"):
		return "gce_instance"
	case strings.Contains(lower, "/clusters/"):
		return "k8s_cluster"
	case strings.Contains(lower, "/buckets/"):
		return "gcs_bucket"
	case strings.Contains(lower, "/databases/"):
		return "cloud_sql"
	case strings.Contains(lower, "/topics/") || strings.Contains(lower, "/subscriptions/"):
		return "pubsub"
	case strings.Contains(lower, "/functions/"):
		return "cloud_function"
	case strings.Contains(lower, "/services/"):
		return "cloud_run"
	default:
		return "gcp_resource"
	}
}

// projectFromConfig attempts to extract a GCP project ID from the
// notificationConfigName field, which follows the pattern:
// "organizations/<org>/notificationConfigs/<cfg>" or
// "projects/<project>/notificationConfigs/<cfg>".
func (GCPSecurityCommandCenterNormalizer) projectFromConfig(raw map[string]interface{}) string {
	cfgName, _ := raw["notificationConfigName"].(string)
	if cfgName == "" {
		return ""
	}
	parts := strings.Split(cfgName, "/")
	for i, p := range parts {
		if p == "projects" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}
