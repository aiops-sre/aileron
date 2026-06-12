package normalization

import (
	"fmt"
	"log"
	"strings"
	"time"
)

// PrometheusNormalizer handles per-alert sub-objects from an Alertmanager webhook.
// The caller iterates payload.alerts[] and passes each element as raw.
//
// Expected keys: labels{}, annotations{}, status, fingerprint, startsAt, endsAt, generatorURL
type PrometheusNormalizer struct{}

func (PrometheusNormalizer) Source() string { return "prometheus" }

func (PrometheusNormalizer) CanHandle(raw map[string]interface{}) bool {
	_, hasLabels := raw["labels"]
	_, hasAnnotations := raw["annotations"]
	_, hasFingerprint := raw["fingerprint"]
	return hasLabels && (hasAnnotations || hasFingerprint)
}

func (PrometheusNormalizer) Normalize(raw map[string]interface{}) (*NormalizedAlert, error) {
	labels := stringMapFrom(raw, "labels")
	annotations := stringMapFrom(raw, "annotations")

	alertname := labels["alertname"]
	if alertname == "" {
		alertname = "Prometheus Alert"
	}

	title := coalesce(annotations["summary"], annotations["message"], alertname)
	description := coalesce(annotations["description"], annotations["message"], annotations["runbook_url"])

	status := MapStatus(strField(raw, "status"))
	severity := MapSeverity(coalesce(labels["severity"], labels["priority"], "medium"))

	fingerprint := strField(raw, "fingerprint")
	if fingerprint == "" {
		fingerprint = FingerprintFromSourceID("prometheus", alertname+"|"+labels["instance"])
	}

	var firedAt time.Time
	if s := strField(raw, "startsAt"); s != "" {
		var err error
		firedAt, err = time.Parse(time.RFC3339, s)
		if err != nil {
			log.Printf("prometheus normalization: failed to parse startsAt %q: %v — using now()", s, err)
		}
	}
	if firedAt.IsZero() {
		firedAt = time.Now()
	}

	var resolvedAt *time.Time
	if s := strField(raw, "endsAt"); s != "" && !strings.HasPrefix(s, "0001") {
		t, err := time.Parse(time.RFC3339, s)
		if err == nil && !t.IsZero() {
			resolvedAt = &t
		}
	}

	entityFromLabels := ExtractFromLabels(labels)
	entityFromText := ExtractFromText(title + " " + description)
	entity := MergeEntityInfo(entityFromLabels, entityFromText)

	// Metric context: alertname encodes the metric check name.
	metricName := alertname
	metricValue := float64(0)

	// Carry ALL prometheus labels through so the pipeline can use them.
	allLabels := make(map[string]string)
	for k, v := range labels {
		allLabels[k] = v
	}
	setLabel(allLabels, "cluster", entity.Cluster)
	setLabel(allLabels, "k8s.cluster.name", entity.Cluster)
	setLabel(allLabels, "namespace", entity.Namespace)
	setLabel(allLabels, "k8s.namespace.name", entity.Namespace)
	setLabel(allLabels, "node", entity.Node)
	setLabel(allLabels, "k8s.node.name", entity.Node)
	setLabel(allLabels, "workload", entity.Workload)

	meta := map[string]interface{}{
		"starts_at":     strField(raw, "startsAt"),
		"ends_at":       strField(raw, "endsAt"),
		"generator_url": strField(raw, "generatorURL"),
		"group_key":     strField(raw, "groupKey"),
	}
	for k, v := range annotations {
		meta["annotation_"+k] = v
	}

	return &NormalizedAlert{
		SourceID:    fingerprint,
		Source:      "prometheus",
		SourceURL:   strField(raw, "generatorURL"),
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
		MetricName:  metricName,
		MetricValue: metricValue,
		Fingerprint: fingerprint,
		FiredAt:     firedAt,
		ResolvedAt:  resolvedAt,
		Labels:      allLabels,
		Metadata:    meta,
		Raw:         raw,
	}, nil
}

// PrometheusPayloadToAlerts converts the Alertmanager envelope into individual
// per-alert raw maps that PrometheusNormalizer.Normalize() can consume.
func PrometheusPayloadToAlerts(payload map[string]interface{}) []map[string]interface{} {
	rawAlerts, _ := payload["alerts"].([]interface{})
	out := make([]map[string]interface{}, 0, len(rawAlerts))
	for _, a := range rawAlerts {
		m, ok := a.(map[string]interface{})
		if !ok {
			continue
		}
		// Carry groupKey down so normalizer can use it for correlation.
		if gk := strField(payload, "groupKey"); gk != "" {
			m["groupKey"] = gk
		}
		out = append(out, m)
	}
	return out
}

// helpers 

func strField(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func stringMapFrom(m map[string]interface{}, key string) map[string]string {
	out := map[string]string{}
	sub, ok := m[key].(map[string]interface{})
	if !ok {
		return out
	}
	for k, v := range sub {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

func coalesce(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func setLabel(m map[string]string, key, val string) {
	if val != "" && m[key] == "" {
		m[key] = val
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// strFromAny attempts a string conversion from interface{}.
func strFromAny(v interface{}) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprintf("%v", t)
	}
}
