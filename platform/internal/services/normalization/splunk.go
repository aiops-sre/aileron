package normalization

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// SplunkNormalizer handles Splunk Webhook Alert Action payloads.
//
// Splunk alerts are structurally unstructured: the key signal lives in _raw
// (the original log line) and a set of extracted fields that vary per search.
// The normalizer applies three passes:
//  1. Structured fields (search_name, result.title, result.severity)
//  2. Key=value extraction from _raw using heuristic patterns
//  3. Entity extraction from any text found (title + description + _raw)
type SplunkNormalizer struct{}

func (SplunkNormalizer) Source() string { return "splunk" }

func (SplunkNormalizer) CanHandle(raw map[string]interface{}) bool {
	_, hasSid := raw["sid"]
	_, hasSearchName := raw["search_name"]
	return hasSid || hasSearchName
}

var (
	splunkKVPattern = regexp.MustCompile(`(\w[\w.\-]*)=(?:"([^"]*)"|([\S]+))`)
	splunkSeverityRe = regexp.MustCompile(`(?i)\b(critical|high|medium|low|warning|error|info)\b`)
)

func (n SplunkNormalizer) Normalize(raw map[string]interface{}) (*NormalizedAlert, error) {
	result := splunkResult(raw)
	rawLog := strings.TrimSpace(result["_raw"])

	// Title 
	title := coalesce(
		result["title"],
		strField(raw, "search_name"),
		n.extractTitleFromRaw(rawLog),
	)
	if title == "" {
		title = "Splunk Alert"
	}
	if len(title) > 200 {
		title = title[:200] + "…"
	}

	// Description 
	description := coalesce(
		result["description"],
		strField(raw, "message"),
		rawLog,
	)

	// Severity 
	rawSev := coalesce(
		strField(raw, "severity"),
		result["severity"],
		n.extractSeverityFromText(title+" "+description+" "+rawLog),
	)
	severity := MapSeverity(rawSev)

	// Status 
	rawStatus := coalesce(strField(raw, "status"), result["status"])
	status := MapStatus(rawStatus)

	// Extract KV pairs from _raw for entity context 
	kvFields := n.extractKVFromRaw(rawLog)
	for k, v := range result {
		if _, exists := kvFields[k]; !exists {
			kvFields[k] = v
		}
	}

	// Entity extraction 
	entityLabels := make(map[string]string)
	for k, v := range kvFields {
		entityLabels[k] = v
	}
	// Splunk often uses "host" as the server that generated the event.
	if h := coalesce(result["host"], kvFields["host"], kvFields["hostname"]); h != "" {
		entityLabels["host"] = h
		entityLabels["host.name"] = h
	}

	entityFromLabels := ExtractFromLabels(entityLabels)
	entityFromText := ExtractFromText(title + " " + description + " " + rawLog)
	entity := MergeEntityInfo(entityFromLabels, entityFromText)

	// Fingerprint 
	sid := strField(raw, "sid")
	fp := FingerprintFromSourceID("splunk", sid)
	if fp == "" {
		fp = Fingerprint(entity.EntityID, "", title)
	}

	// Timestamp 
	firedAt := time.Now()
	if ts := coalesce(result["_time"], strField(raw, "timestamp")); ts != "" {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			firedAt = t
		}
	}

	// Labels 
	labels := map[string]string{
		"source":      "splunk",
		"search_name": strField(raw, "search_name"),
		"owner":       strField(raw, "owner"),
		"app":         strField(raw, "app"),
		"sid":         sid,
		"sourcetype":  result["sourcetype"],
		"index":       result["index"],
		"host":        coalesce(result["host"], entity.Host),
	}
	setLabel(labels, "cluster", entity.Cluster)
	setLabel(labels, "k8s.cluster.name", entity.Cluster)
	setLabel(labels, "namespace", entity.Namespace)
	setLabel(labels, "node", entity.Node)
	setLabel(labels, "service", entity.Service)
	setLabel(labels, "workload", entity.Workload)

	// Carry any structured KV fields that look useful
	for k, v := range kvFields {
		if v != "" && labels[k] == "" {
			labels[k] = v
		}
	}

	meta := map[string]interface{}{
		"splunk_sid":          sid,
		"splunk_search_name":  strField(raw, "search_name"),
		"splunk_results_link": strField(raw, "results_link"),
		"splunk_app":          strField(raw, "app"),
		"splunk_owner":        strField(raw, "owner"),
		"raw_event":           rawLog,
	}
	if extra, ok := raw["metadata"].(map[string]interface{}); ok {
		for k, v := range extra {
			meta[k] = v
		}
	}

	return &NormalizedAlert{
		SourceID:    sid,
		Source:      "splunk",
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
		Fingerprint: fp,
		FiredAt:     firedAt,
		Labels:      labels,
		Metadata:    meta,
		Raw:         raw,
	}, nil
}

// splunkResult extracts the "result" sub-object and flattens it to map[string]string.
func splunkResult(raw map[string]interface{}) map[string]string {
	out := map[string]string{}
	result, ok := raw["result"]
	if !ok {
		return out
	}
	switch r := result.(type) {
	case map[string]interface{}:
		for k, v := range r {
			out[k] = strFromAny(v)
		}
	case string:
		// Some Splunk setups serialize result as a JSON string.
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(r), &m); err == nil {
			for k, v := range m {
				out[k] = strFromAny(v)
			}
		}
	}
	return out
}

// extractKVFromRaw parses key=value and key="value" pairs from a raw Splunk log line.
func (SplunkNormalizer) extractKVFromRaw(raw string) map[string]string {
	out := map[string]string{}
	if raw == "" {
		return out
	}
	matches := splunkKVPattern.FindAllStringSubmatch(raw, -1)
	for _, m := range matches {
		key := m[1]
		val := coalesce(m[2], m[3])
		if key != "" && val != "" {
			out[key] = val
		}
	}
	return out
}

// extractSeverityFromText finds the first severity keyword in free-form text.
func (SplunkNormalizer) extractSeverityFromText(text string) string {
	if m := splunkSeverityRe.FindString(text); m != "" {
		return m
	}
	return ""
}

// extractTitleFromRaw takes the first meaningful non-timestamp token sequence.
func (SplunkNormalizer) extractTitleFromRaw(raw string) string {
	if raw == "" {
		return ""
	}
	// Strip common timestamp prefix patterns like "2024-01-15T10:00:00Z "
	noTS := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}[Z\s]*`).ReplaceAllString(raw, "")
	noTS = strings.TrimSpace(noTS)
	if len(noTS) > 150 {
		noTS = noTS[:150] + "…"
	}
	return noTS
}
