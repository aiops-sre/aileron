package normalization

import "time"

// GenericFallbackNormalizer handles any payload that no specific normalizer claims.
// It applies best-effort extraction using common field names across tools.
// It never returns an error; at worst it produces an alert titled "Unknown Alert".
type GenericFallbackNormalizer struct{}

func (GenericFallbackNormalizer) Source() string { return "generic" }

// CanHandle always returns false — it is only invoked explicitly as a fallback.
func (GenericFallbackNormalizer) CanHandle(_ map[string]interface{}) bool { return false }

func (GenericFallbackNormalizer) Normalize(raw map[string]interface{}) (*NormalizedAlert, error) {
	title := coalesce(
		strField(raw, "title", "summary", "alertname", "name", "event_name",
			"message", "alert_name", "check", "check_name"),
	)
	if title == "" {
		title = "Unknown Alert"
	}
	if len(title) > 200 {
		title = title[:200] + "…"
	}

	description := coalesce(
		strField(raw, "description", "message", "details", "body", "text",
			"summary", "annotations.description"),
	)

	severity := MapSeverity(coalesce(
		strField(raw, "severity", "priority", "level", "criticalness",
			"importance", "urgency", "impact"),
	))
	status := MapStatus(coalesce(
		strField(raw, "status", "state", "condition", "resolution"),
	))

	sourceID := coalesce(
		strField(raw, "id", "event_id", "alert_id", "fingerprint",
			"source_id", "external_id", "uid"),
	)
	source := coalesce(
		strField(raw, "source", "integration", "tool", "monitor", "system"),
		"generic",
	)

	labelsRaw := stringMapFrom(raw, "labels")
	entity := MergeEntityInfo(
		ExtractFromLabels(labelsRaw),
		ExtractFromText(title+" "+description),
	)

	fp := FingerprintFromSourceID(source, sourceID)
	if fp == "" {
		fp = Fingerprint(entity.EntityID, "", title)
	}

	var firedAt time.Time
	for _, key := range []string{"timestamp", "time", "fired_at", "started_at", "created_at", "event_time"} {
		if s := strField(raw, key); s != "" {
			if t, err := tryParseTime(s); err == nil {
				firedAt = t
				break
			}
		}
	}
	if firedAt.IsZero() {
		firedAt = time.Now()
	}

	labels := map[string]string{"source": source}
	for k, v := range labelsRaw {
		labels[k] = v
	}
	setLabel(labels, "cluster", entity.Cluster)
	setLabel(labels, "namespace", entity.Namespace)
	setLabel(labels, "node", entity.Node)
	setLabel(labels, "service", entity.Service)

	return &NormalizedAlert{
		SourceID:    sourceID,
		Source:      source,
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
		Metadata:    raw,
		Raw:         raw,
	}, nil
}

func tryParseTime(s string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, nil // no matching format
}
