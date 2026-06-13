package normalization

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// KeptnNormalizer — Keptn CloudEvent payloads
// ─────────────────────────────────────────────────────────────────────────────

// KeptnNormalizer handles Keptn CloudEvents delivered as HTTP webhook payloads.
// It understands the sh.keptn.* type namespace and Keptn-specific data fields.
type KeptnNormalizer struct{}

func (KeptnNormalizer) Source() string { return "keptn" }

func (KeptnNormalizer) CanHandle(raw map[string]interface{}) bool {
	if t, ok := raw["type"].(string); ok && strings.HasPrefix(t, "sh.keptn.") {
		return true
	}
	if src, ok := raw["source"].(string); ok && strings.Contains(src, "keptn") {
		return true
	}
	return false
}

func (KeptnNormalizer) Normalize(raw map[string]interface{}) (*NormalizedAlert, error) {
	data := mapField(raw, "data")

	project := strField(data, "project")
	stage := strField(data, "stage")
	service := strField(data, "service")
	result := strField(data, "result")
	keptnStatus := strField(data, "status")
	message := strField(data, "message")
	eventType := strField(raw, "type")

	// Title
	title := fmt.Sprintf("Keptn: %s / %s / %s", project, stage, service)
	if eventType != "" {
		title = fmt.Sprintf("Keptn %s — %s/%s/%s", eventType, project, stage, service)
	}
	description := message
	if description == "" {
		description = fmt.Sprintf("Keptn event %s for service %s in stage %s (project %s)", eventType, service, stage, project)
	}

	// Severity mapping: data.status "errored" overrides data.result
	var severity Severity
	switch strings.ToLower(result) {
	case "fail":
		severity = SeverityHigh
	case "warning":
		severity = SeverityMedium
	case "pass":
		severity = SeverityInfo
	default:
		severity = SeverityMedium
	}
	if strings.ToLower(keptnStatus) == "errored" {
		severity = SeverityCritical
	}

	// Status mapping
	var status Status
	switch strings.ToLower(result) {
	case "fail":
		status = StatusFiring
	case "pass":
		status = StatusResolved
	default:
		status = StatusFiring
	}
	if strings.ToLower(keptnStatus) == "errored" {
		status = StatusFiring
	}

	// Timestamps
	firedAt := parseRFC3339(strField(raw, "time"))
	if firedAt.IsZero() {
		firedAt = time.Now()
	}

	// Fingerprint
	sourceID := strField(raw, "id")
	fingerprint := FingerprintFromSourceID("keptn", sourceID)
	if fingerprint == "" {
		fingerprint = Fingerprint("keptn/"+service, eventType, title)
	}

	labels := map[string]string{
		"keptn_project": project,
		"keptn_stage":   stage,
		"keptn_service": service,
		"event_type":    eventType,
	}

	meta := map[string]interface{}{
		"keptn_result": result,
		"keptn_status": keptnStatus,
		"event_id":     sourceID,
		"event_type":   eventType,
	}

	return &NormalizedAlert{
		SourceID:    sourceID,
		Source:      "keptn",
		SourceURL:   strField(raw, "source"),
		Title:       title,
		Description: description,
		Severity:    severity,
		Status:      status,
		EntityType:  "k8s_service",
		EntityName:  service,
		EntityID:    entityKey("", stage, "svc", service),
		Service:     service,
		Fingerprint: fingerprint,
		FiredAt:     firedAt,
		Labels:      labels,
		Metadata:    meta,
		Raw:         raw,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// CloudEventsNormalizer — generic CloudEvents v1.0 envelope
// ─────────────────────────────────────────────────────────────────────────────

// CloudEventsNormalizer handles the CloudEvents v1.0 specification envelope.
// It intentionally excludes Keptn events (handled by KeptnNormalizer) to avoid
// double-processing.
type CloudEventsNormalizer struct{}

func (CloudEventsNormalizer) Source() string { return "cloudevents" }

func (CloudEventsNormalizer) CanHandle(raw map[string]interface{}) bool {
	_, hasSpec := raw["specversion"]
	_, hasType := raw["type"]
	_, hasSrc := raw["source"]
	if !hasSpec || !hasType || !hasSrc {
		return false
	}
	// Exclude Keptn to avoid conflict — KeptnNormalizer is more specific.
	if t, ok := raw["type"].(string); ok && strings.HasPrefix(t, "sh.keptn.") {
		return false
	}
	if src, ok := raw["source"].(string); ok && strings.Contains(src, "keptn") {
		return false
	}
	return true
}

func (CloudEventsNormalizer) Normalize(raw map[string]interface{}) (*NormalizedAlert, error) {
	specversion := strField(raw, "specversion")
	ceID := strField(raw, "id")
	ceSource := strField(raw, "source")
	ceType := strField(raw, "type")
	ceSubject := strField(raw, "subject")
	ceTime := strField(raw, "time")

	title := ceType
	if title == "" {
		title = "CloudEvent"
	}
	description := fmt.Sprintf("CloudEvent from %s: %s", ceSource, ceType)

	// Inspect data payload for severity and status hints.
	var severity Severity
	var status Status

	dataRaw := raw["data"]
	var dataMap map[string]interface{}
	switch d := dataRaw.(type) {
	case map[string]interface{}:
		dataMap = d
	case string:
		_ = json.Unmarshal([]byte(d), &dataMap)
	}

	if dataMap != nil {
		severity = MapSeverity(coalesce(
			strField(dataMap, "severity"),
			strField(dataMap, "level"),
			strField(dataMap, "priority"),
		))
	} else {
		severity = SeverityMedium
	}

	// Status: scan data fields for firing/resolved hints.
	status = StatusFiring
	if dataMap != nil {
		for _, key := range []string{"status", "state", "action"} {
			v := strings.ToLower(strField(dataMap, key))
			if strings.Contains(v, "fire") || strings.Contains(v, "open") {
				status = StatusFiring
				break
			}
			if strings.Contains(v, "resolve") || strings.Contains(v, "close") {
				status = StatusResolved
				break
			}
		}
	}

	// EntityName: prefer subject, fall back to source.
	entityName := ceSubject
	if entityName == "" {
		entityName = ceSource
	}

	// Timestamps
	firedAt := parseRFC3339(ceTime)
	if firedAt.IsZero() {
		firedAt = time.Now()
	}

	// Fingerprint
	fingerprint := FingerprintFromSourceID("cloudevents", ceID)
	if fingerprint == "" {
		fingerprint = Fingerprint(entityName, ceType, title)
	}

	labels := map[string]string{
		"ce_specversion": specversion,
		"ce_type":        ceType,
		"ce_source":      ceSource,
		"ce_id":          ceID,
	}

	meta := map[string]interface{}{
		"ce_specversion": specversion,
		"ce_id":          ceID,
		"ce_source":      ceSource,
		"ce_type":        ceType,
		"ce_subject":     ceSubject,
		"ce_time":        ceTime,
	}

	return &NormalizedAlert{
		SourceID:    ceID,
		Source:      "cloudevents",
		SourceURL:   ceSource,
		Title:       title,
		Description: description,
		Severity:    severity,
		Status:      status,
		EntityType:  "service",
		EntityName:  entityName,
		EntityID:    entityKey("", "", "ce", entityName),
		Fingerprint: fingerprint,
		FiredAt:     firedAt,
		Labels:      labels,
		Metadata:    meta,
		Raw:         raw,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// NATSNormalizer — NATS messages forwarded via HTTP bridge
// ─────────────────────────────────────────────────────────────────────────────

// NATSNormalizer handles NATS messages delivered through an HTTP bridge.
// The bridge wraps the original NATS message under a "data" key and exposes
// the NATS subject and optional reply-to headers.
type NATSNormalizer struct{}

func (NATSNormalizer) Source() string { return "nats" }

func (NATSNormalizer) CanHandle(raw map[string]interface{}) bool {
	_, hasSubject := raw["subject"]
	_, hasReply := raw["reply"]
	_, hasNats := raw["_nats"]
	return hasSubject && (hasReply || hasNats)
}

func (NATSNormalizer) Normalize(raw map[string]interface{}) (*NormalizedAlert, error) {
	subject := strField(raw, "subject")

	data := mapField(raw, "data")

	// Title is the NATS subject.
	title := subject
	if title == "" {
		title = "NATS Message"
	}

	// Description from data payload.
	description := strField(data, "message", "description", "msg", "text")
	if description == "" {
		description = fmt.Sprintf("NATS message received on subject: %s", subject)
	}

	// Severity: look in data for common keys.
	severity := MapSeverity(coalesce(
		strField(data, "severity"),
		strField(data, "level"),
		strField(data, "priority"),
	))

	// Status
	status := MapStatus(strField(data, "status", "state"))

	// EntityName: last segment of the NATS subject.
	entityName := subject
	if parts := strings.Split(subject, "."); len(parts) > 0 {
		entityName = parts[len(parts)-1]
	}

	// Timestamps
	firedAt := parseRFC3339(strField(data, "time", "timestamp", "fired_at"))
	if firedAt.IsZero() {
		firedAt = time.Now()
	}

	// Fingerprint
	fingerprint := FingerprintFromSourceID("nats", subject+"|"+strField(data, "id"))
	if fingerprint == "" {
		fingerprint = Fingerprint(entityName, subject, title)
	}

	labels := map[string]string{
		"nats_subject": subject,
	}

	meta := map[string]interface{}{
		"nats_subject": subject,
		"nats_reply":   strField(raw, "reply"),
	}

	return &NormalizedAlert{
		SourceID:    strField(data, "id"),
		Source:      "nats",
		Title:       title,
		Description: description,
		Severity:    severity,
		Status:      status,
		EntityType:  "service",
		EntityName:  entityName,
		EntityID:    entityKey("", "", "nats", entityName),
		Fingerprint: fingerprint,
		FiredAt:     firedAt,
		Labels:      labels,
		Metadata:    meta,
		Raw:         raw,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// FluxCDNormalizer — Flux CD notification controller events
// ─────────────────────────────────────────────────────────────────────────────

// FluxCDNormalizer handles events emitted by the Flux CD notification controller.
// These follow a Kubernetes event structure with involvedObject, reason, and message.
type FluxCDNormalizer struct{}

func (FluxCDNormalizer) Source() string { return "fluxcd" }

func (FluxCDNormalizer) CanHandle(raw map[string]interface{}) bool {
	_, hasInvolved := raw["involvedObject"]
	if !hasInvolved {
		return false
	}
	if rc, ok := raw["reportingController"].(string); ok && strings.Contains(rc, "flux") {
		return true
	}
	if reason, ok := raw["reason"].(string); ok && reason == "ReconciliationFailed" {
		return true
	}
	return false
}

func (FluxCDNormalizer) Normalize(raw map[string]interface{}) (*NormalizedAlert, error) {
	involved := mapField(raw, "involvedObject")
	kind := strField(involved, "kind")
	name := strField(involved, "name")
	namespace := strField(involved, "namespace")
	reason := strField(raw, "reason")
	message := strField(raw, "message")

	title := fmt.Sprintf("FluxCD %s: %s/%s", reason, kind, name)
	if namespace != "" {
		title = fmt.Sprintf("FluxCD %s: %s/%s/%s", reason, namespace, kind, name)
	}
	description := message
	if description == "" {
		description = fmt.Sprintf("Flux CD reconciliation event %s on %s %s in namespace %s", reason, kind, name, namespace)
	}

	// Severity + Status based on reason
	var severity Severity
	var status Status
	switch reason {
	case "ReconciliationFailed", "ValidationFailed":
		severity = SeverityHigh
		status = StatusFiring
	case "Progressing":
		severity = SeverityInfo
		status = StatusFiring
	case "ReconciliationSucceeded":
		severity = SeverityInfo
		status = StatusResolved
	default:
		severity = SeverityMedium
		status = StatusFiring
	}

	// EntityType from Kubernetes Kind
	var entityType string
	switch kind {
	case "HelmRelease":
		entityType = "helm_release"
	case "Kustomization":
		entityType = "kustomization"
	case "GitRepository":
		entityType = "git_repository"
	default:
		entityType = "flux_resource"
	}

	// Timestamps
	firedAt := parseRFC3339(strField(raw, "eventTime", "firstTimestamp", "lastTimestamp"))
	if firedAt.IsZero() {
		firedAt = time.Now()
	}

	// Fingerprint
	sourceID := strField(raw, "metadata.uid", "uid")
	fingerprint := FingerprintFromSourceID("fluxcd", sourceID)
	if fingerprint == "" {
		fingerprint = Fingerprint(namespace+"/"+kind+"/"+name, reason, title)
	}

	labels := map[string]string{
		"flux_kind": kind,
		"namespace": namespace,
		"reason":    reason,
	}

	meta := map[string]interface{}{
		"flux_kind":            kind,
		"flux_name":            name,
		"flux_namespace":       namespace,
		"reason":               reason,
		"reporting_controller": strField(raw, "reportingController"),
		"reporting_instance":   strField(raw, "reportingInstance"),
	}

	return &NormalizedAlert{
		SourceID:    sourceID,
		Source:      "fluxcd",
		Title:       title,
		Description: description,
		Severity:    severity,
		Status:      status,
		EntityType:  entityType,
		EntityName:  name,
		EntityID:    entityKey("", namespace, strings.ToLower(kind), name),
		Namespace:   namespace,
		Fingerprint: fingerprint,
		FiredAt:     firedAt,
		Labels:      labels,
		Metadata:    meta,
		Raw:         raw,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// shared helpers (file-local, not re-exported — prometheus.go owns the originals)
// ─────────────────────────────────────────────────────────────────────────────

// mapField returns a nested map[string]interface{} value or an empty map.
func mapField(m map[string]interface{}, key string) map[string]interface{} {
	if v, ok := m[key].(map[string]interface{}); ok {
		return v
	}
	return map[string]interface{}{}
}

// parseRFC3339 attempts to parse a time string; returns zero time on failure.
func parseRFC3339(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return time.Time{}
		}
	}
	return t
}

// Ensure imports are used.
var _ = json.Marshal
var _ = fmt.Sprintf
var _ = strings.Contains
