package normalization

import (
	"strings"
	"time"
)

// ─── AzureMonitorNormalizer ───────────────────────────────────────────────────

// AzureMonitorNormalizer handles Azure Monitor metric/log alert webhooks in the
// Azure Monitor Common Alert Schema and the legacy AzureMonitorMetricAlert format.
type AzureMonitorNormalizer struct{}

func (AzureMonitorNormalizer) Source() string { return "azure_monitor" }

func (AzureMonitorNormalizer) CanHandle(raw map[string]interface{}) bool {
	if schemaID, _ := raw["schemaId"].(string); schemaID == "AzureMonitorMetricAlert" ||
		schemaID == "AzureMonitorCommonAlertSchema" {
		return true
	}
	if data, ok := raw["data"].(map[string]interface{}); ok {
		if _, hasEssentials := data["essentials"]; hasEssentials {
			return true
		}
	}
	return false
}

func (n AzureMonitorNormalizer) Normalize(raw map[string]interface{}) (*NormalizedAlert, error) {
	essentials := azNestedMap(raw, "data", "essentials")

	alertID := strField(essentials, "alertId")
	alertRule := strField(essentials, "alertRule")
	signalType := strField(essentials, "signalType")
	monitorCondition := strField(essentials, "monitorCondition")
	description := strField(essentials, "description", "configurationItems")

	title := coalesce(alertRule, alertID, "Azure Monitor Alert")

	// Timestamps
	var firedAt time.Time
	var resolvedAt *time.Time

	if s := strField(essentials, "firedDateTime"); s != "" {
		firedAt, _ = time.Parse(time.RFC3339, s)
	}
	if firedAt.IsZero() {
		firedAt = time.Now()
	}
	if s := strField(essentials, "resolvedDateTime"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			resolvedAt = &t
		}
	}

	// Severity: Sev0–Sev4
	severity := n.mapSeverity(strField(essentials, "severity"))

	// Status: monitorCondition "Fired" | "Resolved"
	status := StatusFiring
	if strings.EqualFold(monitorCondition, "Resolved") {
		status = StatusResolved
		if resolvedAt == nil {
			t := time.Now()
			resolvedAt = &t
		}
	}

	// alertTargetIDs[]
	targetIDs := azStringSlice(essentials, "alertTargetIDs")
	firstTarget := ""
	if len(targetIDs) > 0 {
		firstTarget = targetIDs[0]
	}

	entityType := n.inferEntityType(firstTarget)
	subscriptionID := azExtractSubscriptionID(firstTarget)
	region := azExtractRegion(firstTarget, subscriptionID)

	// EntityID from first target path
	entityID := firstTarget
	entityName := firstTarget
	if entityID == "" {
		entityID = alertRule
		entityName = alertRule
	}

	fp := FingerprintFromSourceID("azure_monitor", alertID)
	if fp == "" {
		fp = Fingerprint(entityID, signalType, title)
	}

	labels := map[string]string{
		"source": "azure_monitor",
	}
	setLabel(labels, "alert_rule", alertRule)
	setLabel(labels, "signal_type", signalType)
	setLabel(labels, "subscription_id", subscriptionID)
	setLabel(labels, "monitor_condition", monitorCondition)

	meta := map[string]interface{}{
		"alert_id":          alertID,
		"alert_rule":        alertRule,
		"signal_type":       signalType,
		"monitor_condition": monitorCondition,
		"severity_raw":      strField(essentials, "severity"),
	}
	if len(targetIDs) > 0 {
		meta["alert_target_ids"] = targetIDs
	}

	return &NormalizedAlert{
		SourceID:    alertID,
		Source:      "azure_monitor",
		Title:       title,
		Description: description,
		Severity:    severity,
		Status:      status,
		EntityID:    entityID,
		EntityType:  entityType,
		EntityName:  entityName,
		Region:      region,
		Fingerprint: fp,
		FiredAt:     firedAt,
		ResolvedAt:  resolvedAt,
		Labels:      labels,
		Metadata:    meta,
		Raw:         raw,
	}, nil
}

// mapSeverity converts Azure "Sev0"–"Sev4" to canonical Severity.
func (AzureMonitorNormalizer) mapSeverity(sev string) Severity {
	switch strings.ToLower(strings.TrimSpace(sev)) {
	case "sev0":
		return SeverityCritical
	case "sev1":
		return SeverityCritical
	case "sev2":
		return SeverityHigh
	case "sev3":
		return SeverityMedium
	case "sev4":
		return SeverityLow
	default:
		return SeverityMedium
	}
}

// inferEntityType inspects the Azure resource path for well-known provider segments.
func (AzureMonitorNormalizer) inferEntityType(resourceID string) string {
	lower := strings.ToLower(resourceID)
	switch {
	case strings.Contains(lower, "/virtualmachines/"):
		return "azure_vm"
	case strings.Contains(lower, "/managedclusters/"):
		return "k8s_node"
	case strings.Contains(lower, "/servers/"):
		return "azure_sql"
	default:
		if resourceID != "" {
			return "azure_resource"
		}
		return "azure_resource"
	}
}

// azExtractSubscriptionID parses /subscriptions/<id>/... from a resource ID.
func azExtractSubscriptionID(resourceID string) string {
	parts := strings.Split(resourceID, "/")
	for i, p := range parts {
		if strings.EqualFold(p, "subscriptions") && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// azExtractRegion tries to find a location segment in the resource ID.
// Azure resource IDs do not embed location; we return the subscription ID as a
// region hint when no explicit location token is present.
func azExtractRegion(resourceID, subscriptionID string) string {
	// Some alert payloads include "location" in the path for resources like
	// /subscriptions/.../locations/<region>/...
	parts := strings.Split(resourceID, "/")
	for i, p := range parts {
		if strings.EqualFold(p, "locations") && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	// Fall back to subscription ID as a coarse identifier
	return subscriptionID
}

// ─── AzureSentinelNormalizer ──────────────────────────────────────────────────

// AzureSentinelNormalizer handles Microsoft Sentinel incident webhook payloads.
type AzureSentinelNormalizer struct{}

func (AzureSentinelNormalizer) Source() string { return "azure_sentinel" }

func (AzureSentinelNormalizer) CanHandle(raw map[string]interface{}) bool {
	_, hasAlertType := raw["alertType"]
	_, hasWorkspaceID := raw["workspaceId"]
	if hasAlertType && hasWorkspaceID {
		return true
	}
	if prod, _ := raw["ProductName"].(string); prod == "Azure Sentinel" {
		return true
	}
	return false
}

func (n AzureSentinelNormalizer) Normalize(raw map[string]interface{}) (*NormalizedAlert, error) {
	displayName := strField(raw, "AlertDisplayName", "alertDisplayName", "Title")
	description := strField(raw, "Description", "description")
	severityRaw := strField(raw, "Severity", "severity")
	statusRaw := strField(raw, "Status", "status")
	workspaceID := strField(raw, "WorkspaceId", "workspaceId")
	startTimeStr := strField(raw, "StartTimeUtc", "startTimeUtc")
	endTimeStr := strField(raw, "EndTimeUtc", "endTimeUtc")
	alertID := strField(raw, "SystemAlertId", "systemAlertId", "id", "alertType")

	title := coalesce(displayName, alertID, "Azure Sentinel Alert")

	severity := n.mapSeverity(severityRaw)
	status := n.mapStatus(statusRaw)

	var firedAt time.Time
	if startTimeStr != "" {
		firedAt, _ = time.Parse(time.RFC3339, startTimeStr)
	}
	if firedAt.IsZero() {
		firedAt = time.Now()
	}

	var resolvedAt *time.Time
	if status == StatusResolved {
		if endTimeStr != "" {
			if t, err := time.Parse(time.RFC3339, endTimeStr); err == nil {
				resolvedAt = &t
			}
		}
		if resolvedAt == nil {
			t := time.Now()
			resolvedAt = &t
		}
	}

	fp := FingerprintFromSourceID("azure_sentinel", alertID)
	if fp == "" {
		fp = Fingerprint(workspaceID, "", title)
	}

	labels := map[string]string{
		"source":  "azure_sentinel",
		"product": "azure_sentinel",
	}
	setLabel(labels, "workspace_id", workspaceID)
	setLabel(labels, "severity_raw", severityRaw)
	setLabel(labels, "status_raw", statusRaw)

	meta := map[string]interface{}{
		"alert_display_name": displayName,
		"severity":           severityRaw,
		"status":             statusRaw,
		"workspace_id":       workspaceID,
		"start_time_utc":     startTimeStr,
		"end_time_utc":       endTimeStr,
	}

	entityID := coalesce(alertID, workspaceID)
	entityName := coalesce(displayName, alertID)

	return &NormalizedAlert{
		SourceID:    alertID,
		Source:      "azure_sentinel",
		Title:       title,
		Description: description,
		Severity:    severity,
		Status:      status,
		EntityID:    entityID,
		EntityType:  "azure_sentinel_incident",
		EntityName:  entityName,
		Fingerprint: fp,
		FiredAt:     firedAt,
		ResolvedAt:  resolvedAt,
		Labels:      labels,
		Metadata:    meta,
		Raw:         raw,
	}, nil
}

// mapSeverity maps Sentinel severity strings to canonical Severity.
func (AzureSentinelNormalizer) mapSeverity(sev string) Severity {
	switch strings.ToLower(strings.TrimSpace(sev)) {
	case "high":
		return SeverityHigh
	case "medium":
		return SeverityMedium
	case "low":
		return SeverityLow
	case "informational":
		return SeverityInfo
	default:
		return SeverityMedium
	}
}

// mapStatus maps Sentinel status strings to canonical Status.
func (AzureSentinelNormalizer) mapStatus(status string) Status {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "closed":
		return StatusResolved
	default:
		// "New", "InProgress", and anything else are considered active.
		return StatusFiring
	}
}

// ─── AzureServiceHealthNormalizer ─────────────────────────────────────────────

// AzureServiceHealthNormalizer handles Azure Service Health alert webhooks
// delivered via Azure Monitor Action Groups (Common Alert Schema).
type AzureServiceHealthNormalizer struct{}

func (AzureServiceHealthNormalizer) Source() string { return "azure_service_health" }

func (AzureServiceHealthNormalizer) CanHandle(raw map[string]interface{}) bool {
	essentials := azNestedMap(raw, "data", "essentials")
	return strings.EqualFold(strField(essentials, "monitoringService"), "ServiceHealth")
}

func (n AzureServiceHealthNormalizer) Normalize(raw map[string]interface{}) (*NormalizedAlert, error) {
	essentials := azNestedMap(raw, "data", "essentials")
	alertContext := azNestedMap(raw, "data", "alertContext")
	properties := azNestedMap(alertContext, "properties")

	alertID := strField(essentials, "alertId")
	alertRule := strField(essentials, "alertRule")
	firedDateTime := strField(essentials, "firedDateTime")

	shTitle := strField(properties, "title")
	communication := strField(properties, "communication")
	stage := strField(properties, "stage")
	trackingID := strField(properties, "trackingId")

	title := coalesce(shTitle, alertRule, alertID, "Azure Service Health Alert")
	description := coalesce(communication, strField(essentials, "description"))

	severity := n.severityFromStage(stage)

	// Status: Resolved stage → resolved, everything else → firing
	status := StatusFiring
	if strings.EqualFold(stage, "Resolved") {
		status = StatusResolved
	}

	var firedAt time.Time
	if firedDateTime != "" {
		firedAt, _ = time.Parse(time.RFC3339, firedDateTime)
	}
	if firedAt.IsZero() {
		firedAt = time.Now()
	}

	var resolvedAt *time.Time
	if status == StatusResolved {
		t := firedAt
		if s := strField(essentials, "resolvedDateTime"); s != "" {
			if t2, err := time.Parse(time.RFC3339, s); err == nil {
				t = t2
			}
		}
		resolvedAt = &t
	}

	// Impacted services
	impactedServices := azStringSlice(properties, "impactedServices")

	fp := FingerprintFromSourceID("azure_service_health", coalesce(trackingID, alertID))
	if fp == "" {
		fp = Fingerprint("azure_service", stage, title)
	}

	subscriptionID := azExtractSubscriptionID(strField(essentials, "alertId"))
	region := strField(essentials, "region")

	labels := map[string]string{
		"source": "azure_service_health",
	}
	setLabel(labels, "tracking_id", trackingID)
	setLabel(labels, "stage", stage)
	setLabel(labels, "subscription_id", subscriptionID)
	setLabel(labels, "region", region)

	meta := map[string]interface{}{
		"alert_id":          alertID,
		"alert_rule":        alertRule,
		"stage":             stage,
		"tracking_id":       trackingID,
		"communication":     communication,
		"fired_date_time":   firedDateTime,
		"impacted_services": impactedServices,
	}

	return &NormalizedAlert{
		SourceID:    coalesce(trackingID, alertID),
		Source:      "azure_service_health",
		Title:       title,
		Description: description,
		Severity:    severity,
		Status:      status,
		EntityID:    coalesce(trackingID, alertID),
		EntityType:  "azure_service",
		EntityName:  coalesce(shTitle, alertRule),
		Region:      region,
		Fingerprint: fp,
		FiredAt:     firedAt,
		ResolvedAt:  resolvedAt,
		Labels:      labels,
		Metadata:    meta,
		Raw:         raw,
	}, nil
}

// severityFromStage maps Service Health lifecycle stage to canonical Severity.
func (AzureServiceHealthNormalizer) severityFromStage(stage string) Severity {
	switch strings.ToLower(strings.TrimSpace(stage)) {
	case "active":
		return SeverityHigh
	case "resolved":
		return SeverityInfo
	case "planned":
		return SeverityLow
	default:
		return SeverityMedium
	}
}

// ─── package-level Azure helpers ──────────────────────────────────────────────

// azNestedMap navigates a chain of map[string]interface{} keys and returns the
// leaf map.  Returns an empty (non-nil) map when any step is missing.
func azNestedMap(m map[string]interface{}, keys ...string) map[string]interface{} {
	cur := m
	for _, k := range keys {
		next, ok := cur[k].(map[string]interface{})
		if !ok {
			return map[string]interface{}{}
		}
		cur = next
	}
	return cur
}

// azStringSlice extracts a []string from a []interface{} value stored at key.
func azStringSlice(m map[string]interface{}, key string) []string {
	raw, ok := m[key].([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}
