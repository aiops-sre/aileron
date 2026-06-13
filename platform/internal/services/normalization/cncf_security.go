package normalization

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ─── Falco ───────────────────────────────────────────────────────────────────

// FalcoNormalizer handles Falco runtime-security webhook payloads.
//
// Expected keys: rule, priority, output, output_fields, hostname, time
type FalcoNormalizer struct{}

func (FalcoNormalizer) Source() string { return "falco" }

func (FalcoNormalizer) CanHandle(raw map[string]interface{}) bool {
	_, hasRule := raw["rule"]
	_, hasPriority := raw["priority"]
	_, hasOutputFields := raw["output_fields"]
	return hasRule && (hasPriority || hasOutputFields)
}

func (FalcoNormalizer) Normalize(raw map[string]interface{}) (*NormalizedAlert, error) {
	rule, _ := raw["rule"].(string)
	priority, _ := raw["priority"].(string)
	output, _ := raw["output"].(string)
	hostname, _ := raw["hostname"].(string)

	outputFields := map[string]interface{}{}
	if of, ok := raw["output_fields"].(map[string]interface{}); ok {
		outputFields = of
	}

	strOf := func(key string) string {
		if v, ok := outputFields[key].(string); ok {
			return v
		}
		return ""
	}

	podName := strOf("k8s.pod.name")
	namespace := strOf("k8s.ns.name")
	containerName := strOf("container.name")

	// Title: use rule name; Description: use output message.
	title := rule
	if title == "" {
		title = "Falco Alert"
	}
	description := output

	// Priority → Severity mapping.
	var severity Severity
	switch strings.ToLower(priority) {
	case "emergency", "alert", "critical":
		severity = SeverityCritical
	case "error":
		severity = SeverityHigh
	case "warning":
		severity = SeverityMedium
	case "notice", "informational":
		severity = SeverityLow
	case "debug":
		severity = SeverityInfo
	default:
		severity = SeverityMedium
	}

	// EntityType and EntityName.
	var entityType, entityName string
	if podName != "" {
		entityType = "k8s_pod"
		entityName = podName
	} else {
		entityType = "host"
		entityName = hostname
	}

	// OccurredAt: parse RFC3339 "time" field.
	firedAt := time.Now()
	if ts, ok := raw["time"].(string); ok && ts != "" {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			firedAt = t
		}
	}

	labels := map[string]string{
		"falco_rule": rule,
		"container":  containerName,
		"namespace":  namespace,
	}

	fingerprint := FingerprintFromSourceID("falco", fmt.Sprintf("%s|%s|%s", rule, entityName, namespace))

	return &NormalizedAlert{
		SourceID:    fingerprint,
		Source:      "falco",
		Title:       title,
		Description: description,
		Severity:    severity,
		Status:      StatusFiring,
		EntityType:  entityType,
		EntityName:  entityName,
		Namespace:   namespace,
		Host:        hostname,
		Fingerprint: fingerprint,
		FiredAt:     firedAt,
		Labels:      labels,
		Metadata: map[string]interface{}{
			"priority":      priority,
			"rule":          rule,
			"output_fields": outputFields,
		},
		Raw: raw,
	}, nil
}

// ─── Kyverno ─────────────────────────────────────────────────────────────────

// KyvernoNormalizer handles Kyverno policy violation events delivered as
// Kubernetes Event objects.
//
// Expected keys: reason, message, involvedObject{name, namespace, kind}
type KyvernoNormalizer struct{}

func (KyvernoNormalizer) Source() string { return "kyverno" }

func (KyvernoNormalizer) CanHandle(raw map[string]interface{}) bool {
	_, hasReason := raw["reason"]
	_, hasMessage := raw["message"]
	_, hasInvolved := raw["involvedObject"]
	return hasReason && hasMessage && hasInvolved
}

func (KyvernoNormalizer) Normalize(raw map[string]interface{}) (*NormalizedAlert, error) {
	reason, _ := raw["reason"].(string)
	message, _ := raw["message"].(string)

	involvedObject := map[string]interface{}{}
	if io, ok := raw["involvedObject"].(map[string]interface{}); ok {
		involvedObject = io
	}

	objName, _ := involvedObject["name"].(string)
	objNamespace, _ := involvedObject["namespace"].(string)
	objKind, _ := involvedObject["kind"].(string)

	// reason → status + severity.
	var status Status
	var severity Severity
	switch reason {
	case "PolicyViolation":
		status = StatusFiring
		severity = SeverityHigh
	case "PolicySkipped":
		status = StatusResolved
		severity = SeverityInfo
	default:
		status = StatusFiring
		severity = SeverityMedium
	}

	// kind → canonical entity type.
	entityType := kyvernoKindToEntityType(objKind)

	// Extract policy name: first 50 chars of message as a stable label.
	policyLabel := message
	if len(policyLabel) > 50 {
		policyLabel = policyLabel[:50]
	}

	title := fmt.Sprintf("Kyverno %s: %s/%s", reason, objKind, objName)
	if title == "" {
		title = "Kyverno Policy Event"
	}

	firedAt := time.Now()
	if ts, ok := raw["lastTimestamp"].(string); ok && ts != "" {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			firedAt = t
		}
	}

	labels := map[string]string{
		"kyverno_policy": policyLabel,
		"kind":           objKind,
	}

	fingerprint := FingerprintFromSourceID("kyverno", fmt.Sprintf("%s|%s|%s|%s", reason, objKind, objName, objNamespace))

	return &NormalizedAlert{
		SourceID:    fingerprint,
		Source:      "kyverno",
		Title:       title,
		Description: message,
		Severity:    severity,
		Status:      status,
		EntityType:  entityType,
		EntityName:  objName,
		Namespace:   objNamespace,
		Fingerprint: fingerprint,
		FiredAt:     firedAt,
		Labels:      labels,
		Metadata: map[string]interface{}{
			"reason":          reason,
			"involved_object": involvedObject,
		},
		Raw: raw,
	}, nil
}

// kyvernoKindToEntityType maps a Kubernetes Kind string to the canonical entity
// type used throughout the normalization pipeline.
func kyvernoKindToEntityType(kind string) string {
	switch strings.ToLower(kind) {
	case "pod":
		return "k8s_pod"
	case "deployment", "replicaset", "statefulset", "daemonset":
		return "k8s_deployment"
	case "node":
		return "k8s_node"
	default:
		return "k8s_resource"
	}
}

// ─── OPA Gatekeeper ──────────────────────────────────────────────────────────

// OPAGatekeeperNormalizer handles OPA Gatekeeper audit result payloads.
//
// Expected shape:
//
//	{
//	  "apiVersion": "constraints.gatekeeper.sh/v1beta1",
//	  "kind":       "AuditResult",            (optional)
//	  "metadata":   {"name": "require-labels"},
//	  "violations": [
//	    {
//	      "resource":          {"name": "...", "namespace": "...", "kind": "..."},
//	      "message":           "...",
//	      "enforcementAction": "deny"|"warn"|"dryrun"
//	    }
//	  ]
//	}
type OPAGatekeeperNormalizer struct{}

func (OPAGatekeeperNormalizer) Source() string { return "opa_gatekeeper" }

func (OPAGatekeeperNormalizer) CanHandle(raw map[string]interface{}) bool {
	if apiVer, ok := raw["apiVersion"].(string); ok {
		if strings.Contains(apiVer, "gatekeeper") {
			return true
		}
	}
	if kind, ok := raw["kind"].(string); ok && kind == "AuditResult" {
		return true
	}
	return false
}

func (OPAGatekeeperNormalizer) Normalize(raw map[string]interface{}) (*NormalizedAlert, error) {
	// metadata.name → constraint name used in title.
	constraintName := ""
	if meta, ok := raw["metadata"].(map[string]interface{}); ok {
		if n, ok := meta["name"].(string); ok {
			constraintName = n
		}
	}

	title := "Constraint violation: " + constraintName
	if constraintName == "" {
		title = "OPA Gatekeeper Constraint Violation"
	}

	// violations array.
	violations := []map[string]interface{}{}
	if rawViolations, ok := raw["violations"].([]interface{}); ok {
		for _, v := range rawViolations {
			if vm, ok := v.(map[string]interface{}); ok {
				violations = append(violations, vm)
			}
		}
	}

	// Extract first violation fields for the primary alert dimensions.
	var (
		firstMessage    string
		firstEntityName string
		firstEntityType string
		firstNamespace  string
		enforcementAction string
	)

	if len(violations) > 0 {
		first := violations[0]
		firstMessage, _ = first["message"].(string)
		enforcementAction, _ = first["enforcementAction"].(string)

		if res, ok := first["resource"].(map[string]interface{}); ok {
			firstEntityName, _ = res["name"].(string)
			firstNamespace, _ = res["namespace"].(string)
			if kind, ok := res["kind"].(string); ok {
				firstEntityType = gatekeeperKindToEntityType(kind)
			}
		}
	}

	// enforcementAction → severity.
	var severity Severity
	switch strings.ToLower(enforcementAction) {
	case "deny":
		severity = SeverityCritical
	case "warn":
		severity = SeverityHigh
	case "dryrun":
		severity = SeverityLow
	default:
		severity = SeverityMedium
	}

	labels := map[string]string{
		"enforcement":     enforcementAction,
		"violation_count": strconv.Itoa(len(violations)),
	}

	firedAt := time.Now()

	fingerprint := FingerprintFromSourceID("opa_gatekeeper", fmt.Sprintf("%s|%s|%s", constraintName, firstEntityName, firstNamespace))

	return &NormalizedAlert{
		SourceID:    fingerprint,
		Source:      "opa_gatekeeper",
		Title:       title,
		Description: firstMessage,
		Severity:    severity,
		Status:      StatusFiring,
		EntityType:  firstEntityType,
		EntityName:  firstEntityName,
		Namespace:   firstNamespace,
		Fingerprint: fingerprint,
		FiredAt:     firedAt,
		Labels:      labels,
		Metadata: map[string]interface{}{
			"constraint_name":    constraintName,
			"violation_count":    len(violations),
			"enforcement_action": enforcementAction,
		},
		Raw: raw,
	}, nil
}

// gatekeeperKindToEntityType maps a Kubernetes Kind string to the canonical
// entity type used throughout the normalization pipeline.
func gatekeeperKindToEntityType(kind string) string {
	switch strings.ToLower(kind) {
	case "pod":
		return "k8s_pod"
	case "deployment", "replicaset", "statefulset", "daemonset":
		return "k8s_deployment"
	case "node":
		return "k8s_node"
	default:
		return "k8s_resource"
	}
}
