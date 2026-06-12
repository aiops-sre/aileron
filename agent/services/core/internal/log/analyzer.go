// Package log implements Kubernetes log intelligence.
// Detects error patterns, clusters similar log lines, identifies log spikes,
// and correlates log anomalies with deployment events.
package log

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// LogLevel classifies a log entry's severity.
type LogLevel string

const (
	LevelError   LogLevel = "ERROR"
	LevelWarn    LogLevel = "WARN"
	LevelInfo    LogLevel = "INFO"
	LevelDebug   LogLevel = "DEBUG"
	LevelFatal   LogLevel = "FATAL"
	LevelPanic   LogLevel = "PANIC"
	LevelUnknown LogLevel = "UNKNOWN"
)

// LogEntry is a single parsed log line.
type LogEntry struct {
	Timestamp   time.Time `json:"timestamp"`
	Level       LogLevel  `json:"level"`
	Message     string    `json:"message"`
	PodName     string    `json:"pod_name"`
	Namespace   string    `json:"namespace"`
	ContainerName string  `json:"container_name"`
	// Template: normalized message with variable parts replaced by placeholders
	Template    string    `json:"template,omitempty"`
}

// ErrorPattern is a recurring error template with frequency statistics.
type ErrorPattern struct {
	Template      string    `json:"template"`       // normalized pattern
	Namespace     string    `json:"namespace"`
	ContainerName string    `json:"container_name"`
	FirstSeen     time.Time `json:"first_seen"`
	LastSeen      time.Time `json:"last_seen"`
	Count         int64     `json:"count"`
	CountLastHour int64     `json:"count_last_hour"`
	// Rate change: positive = increasing, negative = decreasing
	RateChangePercent float64 `json:"rate_change_percent"`
	// Example log lines (sampled)
	Examples      []string  `json:"examples"`
	Severity      string    `json:"severity"` // critical|high|medium
}

// LogAnomaly is a detected anomaly in log patterns.
type LogAnomaly struct {
	Type        string    `json:"type"`
	// "error_spike" | "new_error_pattern" | "error_rate_regression" | "panic_detected"
	Namespace   string    `json:"namespace"`
	PodName     string    `json:"pod_name"`
	DetectedAt  time.Time `json:"detected_at"`
	Pattern     *ErrorPattern `json:"pattern,omitempty"`
	Description string    `json:"description"`
	Severity    string    `json:"severity"`
}

// ============================================================
// Log Template Extraction (token-based normalization)
// ============================================================

// Common variable patterns to replace with placeholders
var templateReplacements = []struct {
	pattern     *regexp.Regexp
	placeholder string
}{
	{regexp.MustCompile(`\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`), "<UUID>"},
	{regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}(?::\d+)?\b`), "<IP>"},
	{regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?\b`), "<TIMESTAMP>"},
	{regexp.MustCompile(`\b0x[0-9a-fA-F]+\b`), "<HEX>"},
	{regexp.MustCompile(`\b\d{5,}\b`), "<NUM>"},              // long numbers
	{regexp.MustCompile(`"[^"]{20,}"`), `"<STRING>"`},        // long quoted strings
	{regexp.MustCompile(`/(?:[a-zA-Z0-9_.-]+/){2,}[^\s]*`), "<PATH>"}, // file paths
}

// ExtractTemplate normalizes a log message into a reusable pattern.
// "Connection to 192.168.1.5:5432 failed after 3000ms" →
// "Connection to <IP> failed after <NUM>ms"
func ExtractTemplate(message string) string {
	t := message
	for _, r := range templateReplacements {
		t = r.pattern.ReplaceAllString(t, r.placeholder)
	}
	// Trim to first 200 chars for use as map key
	if len(t) > 200 {
		t = t[:200]
	}
	return strings.TrimSpace(t)
}

// DetectLogLevel attempts to extract the log level from a line.
func DetectLogLevel(line string) LogLevel {
	upper := strings.ToUpper(line)
	for _, lvl := range []LogLevel{LevelFatal, LevelPanic, LevelError, LevelWarn, LevelInfo, LevelDebug} {
		if strings.Contains(upper, string(lvl)) {
			return lvl
		}
	}
	return LevelUnknown
}

// ============================================================
// Error Pattern Tracker
// ============================================================

// PatternTracker maintains frequency statistics for log error patterns.
// Thread-safe. Suitable for streaming log ingestion.
type PatternTracker struct {
	mu         sync.RWMutex
	patterns   map[string]*patternState // key: namespace/container/template
	windowSize time.Duration
}

type patternState struct {
	*ErrorPattern
	recentTimestamps []time.Time // sliding window for rate calculation
}

// NewPatternTracker creates a tracker with a given analysis window.
func NewPatternTracker(window time.Duration) *PatternTracker {
	p := &PatternTracker{
		patterns:   make(map[string]*patternState),
		windowSize: window,
	}
	go p.gcLoop()
	return p
}

// Record observes one log entry. Returns an anomaly if the pattern is new
// or if the rate has spiked significantly.
func (p *PatternTracker) Record(entry *LogEntry) *LogAnomaly {
	if entry.Level != LevelError && entry.Level != LevelFatal &&
		entry.Level != LevelPanic && entry.Level != LevelWarn {
		return nil
	}

	template := ExtractTemplate(entry.Message)
	key := entry.Namespace + "/" + entry.ContainerName + "/" + template

	p.mu.Lock()
	state, exists := p.patterns[key]
	if !exists {
		state = &patternState{
			ErrorPattern: &ErrorPattern{
				Template:      template,
				Namespace:     entry.Namespace,
				ContainerName: entry.ContainerName,
				FirstSeen:     entry.Timestamp,
				LastSeen:      entry.Timestamp,
				Examples:      []string{entry.Message},
				Severity:      severityFromLevel(entry.Level),
			},
		}
		p.patterns[key] = state
	}
	state.Count++
	state.LastSeen = entry.Timestamp
	state.recentTimestamps = append(state.recentTimestamps, entry.Timestamp)
	if len(state.Examples) < 3 {
		state.Examples = append(state.Examples, entry.Message)
	}
	p.mu.Unlock()

	// New FATAL/PANIC pattern is always an anomaly
	if !exists && (entry.Level == LevelFatal || entry.Level == LevelPanic) {
		return &LogAnomaly{
			Type: "panic_detected", Namespace: entry.Namespace,
			PodName: entry.PodName, DetectedAt: entry.Timestamp,
			Pattern:     state.ErrorPattern,
			Description: fmt.Sprintf("PANIC/FATAL in %s/%s: %s", entry.Namespace, entry.ContainerName, template),
			Severity:    "critical",
		}
	}

	// New error pattern
	if !exists {
		return &LogAnomaly{
			Type: "new_error_pattern", Namespace: entry.Namespace,
			PodName: entry.PodName, DetectedAt: entry.Timestamp,
			Pattern:     state.ErrorPattern,
			Description: fmt.Sprintf("New error pattern in %s/%s: %s", entry.Namespace, entry.ContainerName, template),
			Severity:    "medium",
		}
	}

	return nil
}

// GetTopPatterns returns the N most frequent error patterns.
func (p *PatternTracker) GetTopPatterns(n int) []*ErrorPattern {
	p.mu.RLock()
	defer p.mu.RUnlock()

	patterns := make([]*ErrorPattern, 0, len(p.patterns))
	cutoff := time.Now().Add(-p.windowSize)

	for _, s := range p.patterns {
		// Count occurrences in the window
		var windowCount int64
		for _, ts := range s.recentTimestamps {
			if ts.After(cutoff) {
				windowCount++
			}
		}
		ep := *s.ErrorPattern
		ep.CountLastHour = windowCount
		patterns = append(patterns, &ep)
	}

	sort.Slice(patterns, func(i, j int) bool {
		return patterns[i].CountLastHour > patterns[j].CountLastHour
	})

	if n > len(patterns) { n = len(patterns) }
	return patterns[:n]
}

// gcLoop periodically prunes old timestamps from pattern windows.
func (p *PatternTracker) gcLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-p.windowSize)
		p.mu.Lock()
		for _, s := range p.patterns {
			fresh := s.recentTimestamps[:0]
			for _, ts := range s.recentTimestamps {
				if ts.After(cutoff) {
					fresh = append(fresh, ts)
				}
			}
			s.recentTimestamps = fresh
		}
		p.mu.Unlock()
	}
}

// ============================================================
// Log Spike Detector
// ============================================================

// LogSpikeDetector uses EWMA to detect unusual bursts in log volume.
// Separate from the anomaly detector — this operates on log counts per minute.
type LogSpikeDetector struct {
	mu       sync.Mutex
	alpha    float64
	mean     float64
	variance float64
	n        int
}

// NewLogSpikeDetector creates a detector. alpha=0.2 is suitable for log rates.
func NewLogSpikeDetector(alpha float64) *LogSpikeDetector {
	return &LogSpikeDetector{alpha: alpha}
}

// Observe records the count of error logs in the latest 1-minute bucket.
// Returns true if the count is a spike (>3σ above baseline).
func (d *LogSpikeDetector) Observe(count float64) (isSpike bool, zScore float64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.n++
	if d.n == 1 {
		d.mean = count
		return false, 0
	}
	prev := d.mean
	d.mean = d.alpha*count + (1-d.alpha)*d.mean
	diff := count - prev
	d.variance = d.alpha*diff*diff + (1-d.alpha)*d.variance
	stdDev := math.Sqrt(d.variance)
	if stdDev < 0.1 || d.n < 20 {
		return false, 0
	}
	zScore = (count - d.mean) / stdDev
	return zScore > 3.0, zScore
}

// ============================================================
// Log Intelligence Report
// ============================================================

// LogIntelligenceReport summarizes log findings for a namespace.
type LogIntelligenceReport struct {
	ClusterID   string         `json:"cluster_id"`
	Namespace   string         `json:"namespace"`
	Period      string         `json:"period"`
	ComputedAt  time.Time      `json:"computed_at"`

	// Top error patterns by frequency
	TopErrors   []*ErrorPattern `json:"top_errors"`

	// Active anomalies
	Anomalies   []*LogAnomaly  `json:"anomalies"`

	// Summary counts
	TotalErrors     int64 `json:"total_errors"`
	UniquePatterns  int   `json:"unique_patterns"`
	NewPatterns     int   `json:"new_patterns_last_hour"`
	PanicCount      int   `json:"panic_fatal_count"`

	// Error rate trend
	ErrorRateTrend string `json:"error_rate_trend"` // increasing | stable | decreasing
}

func severityFromLevel(l LogLevel) string {
	switch l {
	case LevelFatal, LevelPanic:
		return "critical"
	case LevelError:
		return "high"
	case LevelWarn:
		return "medium"
	default:
		return "low"
	}
}
