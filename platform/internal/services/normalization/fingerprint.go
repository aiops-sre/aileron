package normalization

import (
	"fmt"
	"hash/fnv"
	"strings"
)

// Fingerprint builds a stable, 16-hex-char deduplication key from the three
// most-stable alert dimensions: the entity being affected, the metric/check
// firing, and the normalised alert title.
//
// Same problem same fingerprint, regardless of which source fired it.
func Fingerprint(entityID, metricName, title string) string {
	h := fnv.New64a()
	key := strings.Join([]string{
		strings.ToLower(strings.TrimSpace(entityID)),
		strings.ToLower(strings.TrimSpace(metricName)),
		strings.ToLower(strings.TrimSpace(title)),
	}, "|")
	_, _ = h.Write([]byte(key))
	return fmt.Sprintf("%016x", h.Sum64())
}

// FingerprintFromSourceID returns a source-namespaced fingerprint for external IDs.
// Used when the source provides its own stable identifier (e.g., Dynatrace problemId,
// Prometheus fingerprint field, Splunk SID).
func FingerprintFromSourceID(source, sourceID string) string {
	if sourceID == "" {
		return ""
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(source + "|" + sourceID))
	return fmt.Sprintf("%016x", h.Sum64())
}
