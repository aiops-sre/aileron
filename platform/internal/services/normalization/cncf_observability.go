package normalization

import (
	"fmt"
	"strings"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// helpers shared across this file
// ─────────────────────────────────────────────────────────────────────────────

// firstAlert returns the first element of an alerts[] array as a map, or nil.
func firstAlert(raw map[string]interface{}) map[string]interface{} {
	arr, _ := raw["alerts"].([]interface{})
	if len(arr) == 0 {
		return nil
	}
	m, _ := arr[0].(map[string]interface{})
	return m
}

// promAlertLabels extracts the labels sub-map from a single Prometheus-format alert map.
func promAlertLabels(alert map[string]interface{}) map[string]string {
	return stringMapFrom(alert, "labels")
}

// normalizePromCompatAlert converts one alert from an Alertmanager-compatible
// envelope (alerts[N]) into a NormalizedAlert, injecting the overridden source name
// and any extra fields set by the caller.
func normalizePromCompatAlert(
	raw map[string]interface{}, // the full envelope payload
	sourceName string,
	extraLabels map[string]string,
	extraMeta map[string]interface{},
) (*NormalizedAlert, error) {
	alerts, _ := raw["alerts"].([]interface{})
	if len(alerts) == 0 {
		return nil, fmt.Errorf("%s normalizer: no alerts in payload", sourceName)
	}
	alertMap, ok := alerts[0].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("%s normalizer: first alert is not a map", sourceName)
	}

	labels := stringMapFrom(alertMap, "labels")
	annotations := stringMapFrom(alertMap, "annotations")

	alertname := labels["alertname"]
	if alertname == "" {
		alertname = sourceName + " Alert"
	}

	title := coalesce(annotations["summary"], annotations["message"], alertname)
	description := coalesce(annotations["description"], annotations["message"], annotations["runbook_url"])

	status := MapStatus(strField(alertMap, "status"))
	severity := MapSeverity(coalesce(labels["severity"], labels["priority"], "medium"))

	fingerprint := strField(alertMap, "fingerprint")
	if fingerprint == "" {
		fingerprint = FingerprintFromSourceID(sourceName, alertname+"|"+labels["instance"])
	}

	var firedAt time.Time
	if s := strField(alertMap, "startsAt"); s != "" {
		firedAt = parseRFC3339(s)
	}
	if firedAt.IsZero() {
		firedAt = time.Now()
	}

	var resolvedAt *time.Time
	if s := strField(alertMap, "endsAt"); s != "" && !strings.HasPrefix(s, "0001") {
		t := parseRFC3339(s)
		if !t.IsZero() {
			resolvedAt = &t
		}
	}

	entityFromLabels := ExtractFromLabels(labels)
	entityFromText := ExtractFromText(title + " " + description)
	entity := MergeEntityInfo(entityFromLabels, entityFromText)

	// Merge all labels + extras.
	allLabels := make(map[string]string)
	for k, v := range labels {
		allLabels[k] = v
	}
	setLabel(allLabels, "cluster", entity.Cluster)
	setLabel(allLabels, "namespace", entity.Namespace)
	setLabel(allLabels, "node", entity.Node)
	setLabel(allLabels, "workload", entity.Workload)
	for k, v := range extraLabels {
		if allLabels[k] == "" {
			allLabels[k] = v
		}
	}

	meta := map[string]interface{}{
		"starts_at":     strField(alertMap, "startsAt"),
		"ends_at":       strField(alertMap, "endsAt"),
		"generator_url": strField(alertMap, "generatorURL"),
		"group_key":     strField(raw, "groupKey"),
		"receiver":      strField(raw, "receiver"),
	}
	for k, v := range annotations {
		meta["annotation_"+k] = v
	}
	for k, v := range extraMeta {
		meta[k] = v
	}

	return &NormalizedAlert{
		SourceID:    fingerprint,
		Source:      sourceName,
		SourceURL:   strField(alertMap, "generatorURL"),
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
		MetricName:  alertname,
		Fingerprint: fingerprint,
		FiredAt:     firedAt,
		ResolvedAt:  resolvedAt,
		Labels:      allLabels,
		Metadata:    meta,
		Raw:         raw,
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// ThanosNormalizer — Thanos ruler / Alertmanager webhook
// ─────────────────────────────────────────────────────────────────────────────

// ThanosNormalizer handles Alertmanager-compatible webhook payloads sent by the
// Thanos ruler component.  It is distinguished from plain Prometheus payloads by
// the presence of externalLabels.thanos_cluster or a receiver name starting with
// "thanos".
type ThanosNormalizer struct{}

func (ThanosNormalizer) Source() string { return "thanos" }

func (ThanosNormalizer) CanHandle(raw map[string]interface{}) bool {
	_, hasAlerts := raw["alerts"]
	if !hasAlerts {
		return false
	}
	if extLabels, ok := raw["externalLabels"].(map[string]interface{}); ok {
		if _, has := extLabels["thanos_cluster"]; has {
			return true
		}
	}
	if recv, ok := raw["receiver"].(string); ok && strings.HasPrefix(recv, "thanos") {
		return true
	}
	return false
}

func (ThanosNormalizer) Normalize(raw map[string]interface{}) (*NormalizedAlert, error) {
	// Pull thanos_cluster from externalLabels.
	cluster := ""
	if extLabels, ok := raw["externalLabels"].(map[string]interface{}); ok {
		if c, ok := extLabels["thanos_cluster"].(string); ok {
			cluster = c
		}
	}

	first := firstAlert(raw)
	thanosCluster := cluster
	if thanosCluster == "" && first != nil {
		thanosCluster = promAlertLabels(first)["thanos_cluster"]
	}

	extraLabels := map[string]string{}
	if thanosCluster != "" {
		extraLabels["thanos_cluster"] = thanosCluster
	}

	extraMeta := map[string]interface{}{
		"thanos_cluster": thanosCluster,
	}

	n, err := normalizePromCompatAlert(raw, "thanos", extraLabels, extraMeta)
	if err != nil {
		return nil, err
	}

	// Override Cluster with the Thanos-specific label when available.
	if thanosCluster != "" && n.Cluster == "" {
		n.Cluster = thanosCluster
	}
	return n, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// VictoriaMetricsNormalizer — VictoriaMetrics vmalert webhook
// ─────────────────────────────────────────────────────────────────────────────

// VictoriaMetricsNormalizer handles Alertmanager-compatible webhook payloads
// emitted by vmalert (VictoriaMetrics alerting engine).  Distinguished by a
// receiver name containing "victoria" or by the presence of exported_job labels.
type VictoriaMetricsNormalizer struct{}

func (VictoriaMetricsNormalizer) Source() string { return "victoriametrics" }

func (VictoriaMetricsNormalizer) CanHandle(raw map[string]interface{}) bool {
	_, hasAlerts := raw["alerts"]
	if !hasAlerts {
		return false
	}
	if recv, ok := raw["receiver"].(string); ok && strings.Contains(recv, "victoria") {
		return true
	}
	first := firstAlert(raw)
	if first != nil {
		lbls := promAlertLabels(first)
		if _, has := lbls["exported_job"]; has {
			return true
		}
	}
	return false
}

func (VictoriaMetricsNormalizer) Normalize(raw map[string]interface{}) (*NormalizedAlert, error) {
	first := firstAlert(raw)
	var exportedJob string
	if first != nil {
		exportedJob = promAlertLabels(first)["exported_job"]
	}

	extraLabels := map[string]string{}
	if exportedJob != "" {
		extraLabels["exported_job"] = exportedJob
	}
	extraMeta := map[string]interface{}{
		"vm_exported_job": exportedJob,
		"receiver":        strField(raw, "receiver"),
	}

	return normalizePromCompatAlert(raw, "victoriametrics", extraLabels, extraMeta)
}

// ─────────────────────────────────────────────────────────────────────────────
// LokiAlertNormalizer — Loki alerting rules webhook
// ─────────────────────────────────────────────────────────────────────────────

// LokiAlertNormalizer handles Alertmanager-compatible webhook payloads generated
// by Loki alerting rules forwarded through Grafana Alertmanager.  Distinguished
// by a receiver containing "loki" or by the presence of grafana_folder labels.
type LokiAlertNormalizer struct{}

func (LokiAlertNormalizer) Source() string { return "loki" }

func (LokiAlertNormalizer) CanHandle(raw map[string]interface{}) bool {
	_, hasAlerts := raw["alerts"]
	if !hasAlerts {
		return false
	}
	if recv, ok := raw["receiver"].(string); ok && strings.Contains(recv, "loki") {
		return true
	}
	first := firstAlert(raw)
	if first != nil {
		lbls := promAlertLabels(first)
		if _, has := lbls["grafana_folder"]; has {
			return true
		}
	}
	return false
}

func (LokiAlertNormalizer) Normalize(raw map[string]interface{}) (*NormalizedAlert, error) {
	first := firstAlert(raw)
	var podName, namespaceName, grafanaFolder string
	if first != nil {
		lbls := promAlertLabels(first)
		podName = lbls["pod"]
		namespaceName = lbls["namespace"]
		grafanaFolder = lbls["grafana_folder"]
	}

	extraLabels := map[string]string{}
	if grafanaFolder != "" {
		extraLabels["grafana_folder"] = grafanaFolder
	}
	extraMeta := map[string]interface{}{
		"loki_pod":            podName,
		"loki_namespace":      namespaceName,
		"loki_grafana_folder": grafanaFolder,
	}

	n, err := normalizePromCompatAlert(raw, "loki", extraLabels, extraMeta)
	if err != nil {
		return nil, err
	}

	// Override entity name with pod if available.
	if podName != "" && n.EntityName == "" {
		n.EntityName = podName
		n.EntityType = "k8s_pod"
	}
	// Override namespace from Loki labels when entity extraction missed it.
	if namespaceName != "" && n.Namespace == "" {
		n.Namespace = namespaceName
	}

	return n, nil
}

// Ensure imports are used.
var _ = fmt.Sprintf
var _ = time.Now
