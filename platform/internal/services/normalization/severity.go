package normalization

import "strings"

// MapSeverity maps any source-specific severity string to the canonical Severity enum.
// It is intentionally broad so new sources rarely need custom mapping.
func MapSeverity(raw string) Severity {
	switch strings.ToLower(strings.TrimSpace(raw)) {

	// Generic critical synonyms
	case "critical", "p1", "1", "fatal", "disaster", "emergency", "severe",
		"crit", "highest", "blocker":
		return SeverityCritical

	// Dynatrace impact-level critical
	case "application", "service":
		return SeverityCritical

	// Generic high synonyms
	case "high", "p2", "2", "major", "error", "err", "page",
		"infrastructure", "availability":
		return SeverityHigh

	// Grafana
	case "alerting":
		return SeverityHigh

	// Generic medium synonyms
	case "medium", "p3", "3", "moderate", "warning", "warn":
		return SeverityMedium

	// Generic low synonyms
	case "low", "p4", "4", "minor", "performance", "resource", "event":
		return SeverityLow

	// Info
	case "info", "informational", "notice", "debug", "trace",
		"ok", "normal", "resolved":
		return SeverityInfo

	default:
		return SeverityMedium
	}
}

// MapStatus normalises any source-specific status string to StatusFiring / StatusResolved.
func MapStatus(raw string) Status {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "resolved", "ok", "recovery", "closed", "done", "normal",
		"inactive", "no data", "nodata", "firing=false":
		return StatusResolved
	default:
		return StatusFiring
	}
}
