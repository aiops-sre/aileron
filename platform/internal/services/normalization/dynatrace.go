package normalization

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// Title-normalization regexes — strip dynamic parts so "[P1] HC - Jaeger Trace Availability Incident"
// and "[P2] HC - Jaeger Trace Availability Alert" both collapse to "jaeger trace availability".
var (
	reSeverityTag         = regexp.MustCompile(`(?i)^\s*\[P[1-4]\]\s*`)
	reHCPrefix            = regexp.MustCompile(`(?i)^HC\s*-\s*`)
	reProblemIDSuffix     = regexp.MustCompile(`(?i)\s*\(P-[0-9]+\)\s*$`)
	reIncidentAlertSuffix = regexp.MustCompile(`(?i)\s+(incident|alert|problem)\s*$`)
)

// reHostIP matches the Dynatrace host-alert description line:
//
//	"some-host.domain.com : 10.0.0.1"  (with or without the IP part)
var reHostIP = regexp.MustCompile(`^(\S+)(?:\s+:\s+(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}))?\s*$`)

// DynatraceNormalizer handles Dynatrace problem notification payloads.
// It consolidates all the label/entity extraction logic that was previously
// scattered across WebhookHandler.extractLabels() and related helpers.
//
// Supported payload formats:
//   - Dynatrace SaaS/Managed problem notification webhook
//   - Dynatrace Davis AI analysis payloads
//   - Custom HTTP integration with customProperties block
//   - Jaeger / health-check alerts where rootCauseEntity is null and the
//     affected entity is an environment-level DT ID (ENVIRONMENT-xxx)
type DynatraceNormalizer struct{}

func (DynatraceNormalizer) Source() string { return "dynatrace" }

func (DynatraceNormalizer) CanHandle(raw map[string]interface{}) bool {
	_, hasProblemID := raw["ProblemID"]
	_, hasProblemTitle := raw["ProblemTitle"]
	_, hasProblemId2 := raw["problemId"]
	return hasProblemID || hasProblemTitle || hasProblemId2
}

func (n DynatraceNormalizer) Normalize(raw map[string]interface{}) (*NormalizedAlert, error) {
	problemID := dtField(raw, "ProblemID", "problemId", "id")
	title := dtField(raw, "ProblemTitle", "problemTitle", "title")
	details := dtField(raw, "ProblemDetailsText", "ProblemDetails", "problemDetails", "description")
	state := dtField(raw, "State", "state", "status")
	impactLevel := dtField(raw, "ImpactLevel", "impactLevel", "severity")
	sourceURL := dtField(raw, "ProblemURL", "problemURL", "url")

	if title == "" {
		title = "Dynatrace Problem " + problemID
	}

	severity := MapSeverity(coalesce(
		dtField(raw, "ProblemSeverity", "problemSeverity"),
		impactLevel,
	))
	status := MapStatus(state)

	// Entity extraction 
	labels := n.extractLabels(raw, details)
	entityFromLabels := ExtractFromLabels(labels)
	entityFromText := ExtractFromText(details)
	entity := MergeEntityInfo(entityFromLabels, entityFromText)

	// Sync resolved entity fields back to labels 
	// entity.EntityType / Workload / Node / Cluster / Namespace are computed by
	// resolve() from whatever labels+text we extracted above.  Write them back
	// so the correlation pipeline can read them via alert.Labels["entity_type"]
	// etc., regardless of whether AffectedEntities was present in the payload.
	if entity.EntityType != "" {
		setLabel(labels, "entity_type", entity.EntityType)
	}
	if entity.Workload != "" {
		setLabel(labels, "workload", entity.Workload)
		setLabel(labels, "k8s.workload.name", entity.Workload)
	}
	if entity.Node != "" {
		setLabel(labels, "node", entity.Node)
		setLabel(labels, "k8s.node.name", entity.Node)
	}
	if entity.Cluster != "" {
		setLabel(labels, "cluster", entity.Cluster)
		setLabel(labels, "k8s.cluster.name", entity.Cluster)
	}
	if entity.Namespace != "" {
		setLabel(labels, "namespace", entity.Namespace)
		setLabel(labels, "k8s.namespace.name", entity.Namespace)
	}
	if entity.Host != "" {
		setLabel(labels, "host", entity.Host)
	}

	// Root-cause entity 
	rceName, rceID := n.extractRootCauseEntity(raw, labels)
	if rceName != "" {
		// Kubernetes-scoped entity types (workloads, services, applications) are
		// NOT globally unique — "trident-csi" exists in every cluster. Scope the
		// grouping key by cluster/namespace so alerts from different clusters never
		// share a correlation_id.  HOST- entities are excluded because MPS host
		// names already encode the cluster (e.g. cloudstack-cluster-2-iapps-…).
		if isK8sScopedEntityID(rceID) {
			cluster := coalesce(labels["cluster"], labels["k8s.cluster.name"])
			ns := coalesce(labels["namespace"], labels["k8s.namespace.name"])
			switch {
			case cluster != "" && ns != "":
				rceName = cluster + "/" + ns + ":" + rceName
			case cluster != "":
				rceName = cluster + ":" + rceName
			}
		}
		labels["root_cause_entity"] = rceName
		labels["root_cause_entity_id"] = rceID
	}
	impactedName, impactedType := n.extractImpactedEntity(raw)
	if impactedName != "" {
		labels["impacted_entity"] = impactedName
		labels["impacted_entity_type"] = impactedType
	}

	// Stable grouping key for bare/null-entity alerts 
	// When rootCauseEntity is absent AND ProblemDetailsJSON carries no correlationId
	// (very old DT versions, custom HTTP integrations with minimal payload), derive
	// a stable root_cause_entity from the normalized title so repeated firings of
	// the same check land in the same incident.
	// NOTE: this is intentionally last-resort — rankedEvents[0].correlationId
	// (set in extractRootCauseEntity above) is the preferred grouping key because
	// it differentiates between different problem types even when titles look similar.
	//
	// IMPORTANT: scope the fallback key by cluster+namespace so that generic problem
	// titles like "not all pods ready" don't merge unrelated workloads across clusters.
	// Without scoping, every workload on every cluster firing the same DT problem type
	// would land in one incident.
	if rceName == "" {
		if norm := normalizeTitleForFingerprint(title); norm != "" {
			cluster := coalesce(labels["cluster"], labels["k8s.cluster.name"])
			ns := coalesce(labels["namespace"], labels["k8s.namespace.name"])
			switch {
			case cluster != "" && ns != "":
				rceName = cluster + "/" + ns + ":" + norm
			case cluster != "":
				rceName = cluster + ":" + norm
			default:
				rceName = norm
			}
			labels["root_cause_entity"] = rceName
		}
	}

	// Fingerprint — MUST be entity-based for long-duration infrastructure alerts
	// (PVC full, node pressure, certificate expiry) where DT closes and reopens
	// a problem with a new ProblemID. Using ProblemID as the dedup key creates a new
	// AlertHub incident for each DT re-open of the same underlying condition.
	// Entity-based fingerprinting ensures the same PVC/node creates the same fingerprint
	// regardless of DT problem lifecycle, so AlertHub reopens the existing incident.
	entityID := entity.EntityID
	if entityID == "" {
		clusterKey := coalesce(labels["cluster"], labels["k8s.cluster.name"])
		nsKey := coalesce(labels["namespace"], labels["k8s.namespace.name"])
		if clusterKey != "" && nsKey != "" {
			entityID = clusterKey + "/" + nsKey
		} else if clusterKey != "" {
			entityID = clusterKey
		}
	}

	// For storage/resource/certificate alerts that recur on the same entity:
	// use an entity-scoped fingerprint so repeated DT problem IDs for the SAME
	// underlying condition merge into the same AlertHub incident.
	var fp string
	isLongDurationAlert := strings.Contains(strings.ToLower(title), "disk space") ||
		strings.Contains(strings.ToLower(title), "pvc") ||
		strings.Contains(strings.ToLower(title), "memory pressure") ||
		strings.Contains(strings.ToLower(title), "cpu.*saturation") ||
		strings.Contains(strings.ToLower(title), "aggregate state") ||
		strings.Contains(strings.ToLower(title), "svm state") ||
		strings.Contains(strings.ToLower(title), "certificate") ||
		strings.Contains(strings.ToLower(title), "secret") && strings.Contains(strings.ToLower(title), "expir")

	if isLongDurationAlert && entityID != "" {
		// Entity-scoped: same PVC/node/aggregate = same fingerprint regardless of DT problem ID.
		fp = Fingerprint(entityID, impactLevel, normalizeTitleForFingerprint(title))
	} else if problemID != "" {
		// Default: problem-ID-scoped for short-duration transient alerts.
		fp = FingerprintFromSourceID("dynatrace", problemID)
	}
	if fp == "" {
		fp = Fingerprint(entityID, impactLevel, title)
	}

	// Timestamps 
	firedAt := time.Now()
	if s := dtField(raw, "StartTime", "startTime", "timestamp"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			firedAt = t
		}
	}

	meta := n.buildMetadata(raw, impactLevel, state)

	return &NormalizedAlert{
		SourceID:          problemID,
		Source:            "dynatrace",
		SourceURL:         sourceURL,
		Title:             title,
		Description:       details,
		Severity:          severity,
		Status:            status,
		EntityID:          entity.EntityID,
		EntityType:        entity.EntityType,
		EntityName:        entity.EntityName,
		Cluster:           entity.Cluster,
		Namespace:         entity.Namespace,
		Node:              entity.Node,
		Host:              entity.Host,
		Service:           entity.Service,
		Workload:          entity.Workload,
		RootCauseEntity:   rceName,
		RootCauseEntityID: rceID,
		Fingerprint:       fp,
		FiredAt:           firedAt,
		Labels:            labels,
		Metadata:          meta,
		Raw:               raw,
	}, nil
}

// extractLabels builds the unified label map from all Dynatrace payload locations.
func (DynatraceNormalizer) extractLabels(payload map[string]interface{}, description string) map[string]string {
	labels := map[string]string{"source": "dynatrace"}

	for _, kv := range []struct{ payloadKey, label string }{
		{"ProblemID", "problem_id"},
		{"problemId", "problem_id"},
		{"ImpactLevel", "impact_level"},
		{"impactLevel", "impact_level"},
		{"Environment", "environment"},
		{"environment", "environment"},
	} {
		if v := dtField(payload, kv.payloadKey); v != "" && labels[kv.label] == "" {
			labels[kv.label] = v
		}
	}

	// AffectedEntities[0] entity type + name + dt.entity.* labels for InfraLevel classification
	if entities, ok := payload["AffectedEntities"].([]interface{}); ok && len(entities) > 0 {
		if e, ok := entities[0].(map[string]interface{}); ok {
			entityType := dtField(e, "entityType")
			if entityType != "" {
				labels["entity_type"] = entityType
			}
			if en := dtField(e, "entityName"); en != "" {
				labels["entity_name"] = en
			}
			// Populate dt.entity.* so alertInfraLevel can classify the alert (BM/Node/VM/Pod)
			// without requiring a topology lookup.  The key is the Dynatrace MEI (e.g. HOST-abc123).
			if entityID := dtField(e, "entityId", "EntityID"); entityID != "" {
				switch strings.ToUpper(entityType) {
				case "HOST":
					setLabel(labels, "dt.entity.host", entityID)
				case "KUBERNETES_NODE":
					setLabel(labels, "dt.entity.kubernetes_node", entityID)
				case "KUBERNETES_CLUSTER":
					setLabel(labels, "dt.entity.kubernetes_cluster", entityID)
				case "CLOUD_APPLICATION", "KUBERNETES_WORKLOAD":
					setLabel(labels, "dt.entity.cloud_application", entityID)
				}
			}
		}
	}

	// K8s labels embedded in description text (line-by-line "k8s.X.name: value")
	for _, line := range strings.Split(description, "\n") {
		line = strings.TrimSpace(line)
		for _, kv := range []struct{ prefix, label, canonical string }{
			{"k8s.cluster.name:", "cluster", "k8s.cluster.name"},
			{"k8s.namespace.name:", "namespace", "k8s.namespace.name"},
			{"k8s.node.name:", "node", "k8s.node.name"},
			{"k8s.workload.name:", "workload", "k8s.workload.name"},
			{"k8s.workload.kind:", "k8s.workload.kind", ""},
			{"k8s.deployment.name:", "deployment", ""},
			{"k8s.cluster.uid:", "cluster_uid", "k8s.cluster.uid"},
			{"host.name:", "host.name", ""},
		} {
			if strings.HasPrefix(line, kv.prefix) {
				val := strings.TrimSpace(strings.TrimPrefix(line, kv.prefix))
				if val == "" || IsDynatraceEntityID(val) {
					continue
				}
				setLabel(labels, kv.label, val)
				if kv.canonical != "" {
					setLabel(labels, kv.canonical, val)
				}
			}
		}
	}

	// HOST entity: parse "hostname : ip" from the Dynatrace host-alert description
	// format so that the topology correlation engine can match co-cluster VMs.
	// Only runs when no host label has already been extracted from structured fields.
	if labels["host"] == "" && labels["host.name"] == "" {
		if hn, ip := parseHostFromDescription(description); hn != "" {
			setLabel(labels, "host", hn)
			if ip != "" {
				setLabel(labels, "ip", ip)
			}
		}
	}

	// Dynatrace section-header entity parser:
	// Recognises "EntityTypeLine\nEntityNameLine" patterns in ProblemDetailsText.
	//
	// Kubernetes workload entity_type=k8s_workload, k8s.workload.name
	// Kubernetes node     entity_type=k8s_node,     k8s.node.name
	// NetApp OnTap *      entity_type=netapp_{type}, netapp_entity, netapp_cluster
	//
	// Format: header line (e.g. "Kubernetes workload"), then next non-blank line is name.
	parseDescriptionEntitySections(description, labels)

	// customProperties block (Dynatrace HTTP integration carries k8s labels here)
	for _, cpKey := range []string{"customProperties", "CustomProperties"} {
		if cp, ok := payload[cpKey].(map[string]interface{}); ok {
			for k, v := range cp {
				s, ok := v.(string)
				if !ok || s == "" {
					continue
				}
				switch k {
				case "k8s.cluster.name":
					setLabel(labels, "cluster", s)
					setLabel(labels, "k8s.cluster.name", s)
				case "k8s.namespace.name":
					setLabel(labels, "namespace", s)
					setLabel(labels, "k8s.namespace.name", s)
				case "k8s.node.name":
					setLabel(labels, "node", s)
					setLabel(labels, "k8s.node.name", s)
				case "k8s.workload.name":
					setLabel(labels, "workload", s)
					setLabel(labels, "k8s.workload.name", s)
				case "k8s.cluster.uid":
					setLabel(labels, "cluster_uid", s)
					setLabel(labels, "k8s.cluster.uid", s)
				case "k8s.workload.kind":
					setLabel(labels, "k8s.workload.kind", s)
				case "host.name":
					setLabel(labels, "host.name", s)
				case "environment":
					setLabel(labels, "environment", s)
				// NetApp storage entity labels
				case "entity_type":
					setLabel(labels, "entity_type", s)
				case "netapp_entity":
					setLabel(labels, "netapp_entity", s)
				case "netapp_cluster":
					setLabel(labels, "netapp_cluster", s)
				case "impacted_entity":
					setLabel(labels, "impacted_entity", s)
				}
			}
			break
		}
	}

	// ProblemDetailsJSON: extract k8s/service labels from rankedEvents[0].customProperties.
	// Dynatrace embeds these for Jaeger, health-check, and other synthetic-monitor alerts
	// where AffectedEntities points to an ENVIRONMENT entity rather than a real host/pod.
	_, _, pdCustomProps := parseProblemDetailsJSON(payload)
	for k, v := range pdCustomProps {
		if v == "" || IsDynatraceEntityID(v) {
			continue
		}
		switch k {
		case "k8s.cluster.name":
			setLabel(labels, "cluster", v)
			setLabel(labels, "k8s.cluster.name", v)
		case "k8s.namespace.name":
			setLabel(labels, "namespace", v)
			setLabel(labels, "k8s.namespace.name", v)
		case "k8s.node.name":
			setLabel(labels, "node", v)
			setLabel(labels, "k8s.node.name", v)
		case "k8s.workload.name":
			setLabel(labels, "workload", v)
			setLabel(labels, "k8s.workload.name", v)
		case "dt.service.name", "service.name":
			setLabel(labels, "service", v)
		}
	}

	return labels
}

func (DynatraceNormalizer) extractRootCauseEntity(payload map[string]interface{}, labels map[string]string) (name, id string) {
	for _, key := range []string{"RootCauseEntity", "rootCauseEntity"} {
		if rce, ok := payload[key].(map[string]interface{}); ok {
			n := coalesce(dtField(rce, "entityName", "EntityName"), dtField(rce, "name"))
			i := dtField(rce, "entityId", "EntityID", "id")
			// Only accept the rootCauseEntity when backed by a real Dynatrace entity ID.
			// Without a valid entity ID the entityName is a generic problem-type description
			// (e.g. "not all pods ready") that would merge unrelated workloads/clusters into
			// one incident. Fall through to the correlationId-based path in that case.
			if n != "" && IsDynatraceEntityID(i) {
				name = n
				id = i
				return
			}
		}
	}
	// Fall back to pre-populated label — only when backed by a real entity ID.
	if n := labels["root_cause_entity"]; n != "" {
		if i := labels["root_cause_entity_id"]; IsDynatraceEntityID(i) {
			name = n
			id = i
			return
		}
	}

	// Fallback: ProblemDetailsJSON rankedEvents[0].correlationId.
	// Dynatrace embeds this stable hash for health-check and synthetic-monitor alerts
	// (e.g. Jaeger) where rootCauseEntity is null.  The correlationId is consistent
	// across every firing of the same problem type, so alerts of the same Jaeger
	// check correlate into one incident while DIFFERENT Jaeger checks (different
	// correlationIds) remain separate incidents.
	//
	// NOTE: correlationId values like "nginx-mtls-proxy" are workload names, not
	// globally unique IDs. Prefix with "dt-hc:" to prevent false merges with real
	// infrastructure entities that share the same name.
	pdCorrID, pdEntityName, _ := parseProblemDetailsJSON(payload)
	if pdCorrID != "" {
		name = "dt-hc:" + pdCorrID
		id = "dt-hc:" + pdCorrID
		return
	}
	// Last resort: rankedEvents[0].entityName when it's a human-readable name.
	if pdEntityName != "" && !IsDynatraceEntityID(pdEntityName) {
		name = pdEntityName
		return
	}
	return
}

func (DynatraceNormalizer) extractImpactedEntity(payload map[string]interface{}) (name, entityType string) {
	for _, key := range []string{"ImpactedEntities", "impactedEntities"} {
		if entities, ok := payload[key].([]interface{}); ok && len(entities) > 0 {
			if e, ok := entities[0].(map[string]interface{}); ok {
				name = dtField(e, "entityName", "EntityName")
				entityType = dtField(e, "entityType", "EntityType")
				return
			}
		}
	}
	return
}

func (DynatraceNormalizer) buildMetadata(raw map[string]interface{}, impactLevel, state string) map[string]interface{} {
	m := map[string]interface{}{
		"impact_level": impactLevel,
		"state":        state,
		"problem_id":   dtField(raw, "ProblemID", "problemId"),
	}
	for _, key := range []string{"ImpactLevel", "impactLevel", "ImpactedEntities",
		"AffectedEntities", "RootCauseEntity", "Tags", "ProblemURL", "EventType", "eventType"} {
		if v, ok := raw[key]; ok {
			m[strings.ToLower(key)] = v
		}
	}
	return m
}

func dtField(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// parseHostFromDescription extracts hostname and IP from Dynatrace host-alert descriptions.
// Handles the format produced by the Dynatrace "Host" entity type:
//
//	Host
//	some-host.domain.com : 10.0.0.1
//
//	Alert title
func parseHostFromDescription(description string) (hostname, ip string) {
	lines := strings.Split(description, "\n")
	for i, line := range lines {
		if !strings.EqualFold(strings.TrimSpace(line), "host") {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			next := strings.TrimSpace(lines[j])
			if next == "" {
				continue
			}
			m := reHostIP.FindStringSubmatch(next)
			if m == nil {
				break
			}
			hostname = m[1]
			ip = m[2]
			return
		}
	}
	return
}

// parseDescriptionEntitySections extracts entity identity from Dynatrace ProblemDetailsText
// section headers.  Dynatrace formats the affected entity as:
//
//	{EntityTypeHeader}
//	{EntityTypeName} {EntityInstanceName}
//
// Recognised header label mappings:
//   - "Kubernetes workload" k8s.workload.name / entity_type=k8s_workload
//   - "Kubernetes node"     k8s.node.name    / entity_type=k8s_node
//   - "Kubernetes cluster"  cluster          / entity_type=k8s_cluster
//   - "Process"             process_name     / entity_type=process
//   - "NetApp OnTap *"      netapp_entity     / entity_type=netapp_{type}
//     • "OnTap Cluster …"  also sets netapp_cluster
func parseDescriptionEntitySections(description string, labels map[string]string) {
	lines := strings.Split(description, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)

		switch {
		case lower == "kubernetes workload":
			if name := nextNonEmpty(lines, i+1); name != "" && !IsDynatraceEntityID(name) {
				setLabel(labels, "workload", name)
				setLabel(labels, "k8s.workload.name", name)
			}
		case lower == "kubernetes node":
			if name := nextNonEmpty(lines, i+1); name != "" && !IsDynatraceEntityID(name) {
				setLabel(labels, "node", name)
				setLabel(labels, "k8s.node.name", name)
			}
		case lower == "kubernetes cluster":
			if name := nextNonEmpty(lines, i+1); name != "" && !IsDynatraceEntityID(name) {
				setLabel(labels, "cluster", name)
				setLabel(labels, "k8s.cluster.name", name)
				setLabel(labels, "entity_type", "k8s_cluster")
			}
		case lower == "process":
			if name := nextNonEmpty(lines, i+1); name != "" && !IsDynatraceEntityID(name) {
				setLabel(labels, "process_name", name)
				if labels["entity_type"] == "" {
					setLabel(labels, "entity_type", "process")
				}
			}
		case strings.HasPrefix(lower, "netapp ontap "):
			netappType := strings.TrimPrefix(lower, "netapp ontap ")
			entityLabel := "netapp_" + strings.ReplaceAll(netappType, " ", "_")
			if raw := nextNonEmpty(lines, i+1); raw != "" && !IsDynatraceEntityID(raw) {
				// Strip leading "OnTap {Type} " prefix to get the instance name.
				// e.g. "OnTap Cluster netapp-mdn-cluster001" "netapp-mdn-cluster001"
				//      "OnTap Aggregate aggr1_node001"       "aggr1_node001"
				instanceName := raw
				prefix := "ontap " + netappType + " "
				if idx := strings.Index(strings.ToLower(raw), prefix); idx >= 0 {
					instanceName = strings.TrimSpace(raw[idx+len(prefix):])
				}
				setLabel(labels, "netapp_entity", instanceName)
				setLabel(labels, "entity_type", entityLabel)
				// For cluster entities, also set a generic "cluster" key so the pipeline's
				// cluster-based storm grouping can include NetApp alerts.
				if netappType == "cluster" {
					setLabel(labels, "netapp_cluster", instanceName)
					setLabel(labels, "cluster", instanceName)
				} else if labels["netapp_cluster"] == "" {
					// Non-cluster entities: synthesise a cluster key from entity type so that
					// same-type storm grouping applies ("netapp_aggregate", "netapp_volume" etc.)
					setLabel(labels, "netapp_cluster", entityLabel)
				}
			}
		}
	}
}

// nextNonEmpty returns the first non-blank line starting at index start.
func nextNonEmpty(lines []string, start int) string {
	for i := start; i < len(lines) && i < start+3; i++ {
		if v := strings.TrimSpace(lines[i]); v != "" {
			return v
		}
	}
	return ""
}

// parseProblemDetailsJSON decodes the DT ProblemDetailsJSON embedded string and
// returns the first ranked event's correlationId, entityName, and customProperties.
// This is the primary source of stable grouping keys for Jaeger / HC / synthetic
// alerts where AffectedEntities points to an ENVIRONMENT entity.
//
// Payload shape (abbreviated):
//
//	ProblemDetailsJSON: "{\"rankedEvents\":[{\"correlationId\":\"dad3c8aa\",
//	    \"entityName\":\"jaeger-all-in-one\",
//	    \"customProperties\":{\"k8s.namespace.name\":\"monitoring\",...}}]}"
func parseProblemDetailsJSON(payload map[string]interface{}) (correlationID, entityName string, customProps map[string]string) {
	customProps = map[string]string{}
	raw := dtField(payload, "ProblemDetailsJSON", "problemDetailsJson")
	if raw == "" {
		return
	}
	var details map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &details); err != nil {
		return
	}
	events, ok := details["rankedEvents"].([]interface{})
	if !ok || len(events) == 0 {
		return
	}
	event, ok := events[0].(map[string]interface{})
	if !ok {
		return
	}
	correlationID = dtField(event, "correlationId")
	entityName = dtField(event, "entityName")
	if cp, ok := event["customProperties"].(map[string]interface{}); ok {
		for k, v := range cp {
			if s, ok := v.(string); ok && s != "" {
				customProps[k] = s
			}
		}
	}
	return
}

// normalizeTitleForFingerprint strips dynamic/noisy parts from a DT alert title
// producing a stable grouping key used as last-resort root_cause_entity.
//
//	"[P1] HC - Jaeger Trace Availability Incident" "jaeger trace availability"
//	"[P2] HC - api-gateway Alert"                  "api-gateway"
//	"Response time degraded (P-12345)"             "response time degraded"
func normalizeTitleForFingerprint(title string) string {
	t := reSeverityTag.ReplaceAllString(title, "")
	t = reHCPrefix.ReplaceAllString(t, "")
	t = reProblemIDSuffix.ReplaceAllString(t, "")
	t = reIncidentAlertSuffix.ReplaceAllString(t, "")
	return strings.ToLower(strings.Join(strings.Fields(t), " "))
}

// isK8sScopedEntityID returns true for Dynatrace entity ID prefixes that
// correspond to namespace-scoped Kubernetes resources. Names for these entities
// are NOT globally unique (e.g. "trident-csi" exists in every cluster), so
// callers must scope the grouping key by cluster/namespace before using the
// entity name as a correlation identifier.
//
// HOST- is intentionally excluded: MPS host names already encode the cluster
// (e.g. "cloudstack-cluster-2-iapps-100-67-61-18") and are globally unique.
func isK8sScopedEntityID(entityID string) bool {
	for _, pfx := range []string{
		"KUBERNETES_WORKLOAD-",
		"KUBERNETES_SERVICE-",
		"CLOUD_APPLICATION-",
		"CLOUD_APPLICATION_INSTANCE-",
		"CLOUD_APPLICATION_NAMESPACE-",
		"APPLICATION-",
		"SERVICE-",
	} {
		if strings.HasPrefix(entityID, pfx) {
			return true
		}
	}
	return false
}
