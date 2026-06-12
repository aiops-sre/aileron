// Package rca — auto-detection pattern miner.
//
// Implements RCA-Operator's auto-detect loop: every 60s, mine the correlation
// buffer for co-occurring event pairs (by scope), accumulate frequency counts,
// and auto-generate correlation rules once a pattern crosses the threshold.
//
// Key differences from RCA-Operator:
//   - Rules are written to PostgreSQL instead of CRDs
//   - Pattern records are in-memory only (reset on restart)
//   - Analysis interval and thresholds are configurable
package rca

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"sync"
	"time"
)

// MinerConfig controls the auto-detection behaviour.
type MinerConfig struct {
	AnalysisInterval time.Duration // how often to mine the buffer (default 60s)
	MinOccurrences   int           // ticks needed before a rule is created (default 5)
	MinTimeSpan      time.Duration // first-to-last span needed (default 10m)
	MaxAutoRules     int           // cap on auto-generated rules (default 20)
	ExpiryDuration   time.Duration // rule expires if pattern goes quiet (default 1h)
	AutoRulePriority int           // priority for auto-created rules (default 30)
}

func defaultMinerConfig() MinerConfig {
	return MinerConfig{
		AnalysisInterval: 60 * time.Second,
		MinOccurrences:   5,
		MinTimeSpan:      10 * time.Minute,
		MaxAutoRules:     20,
		ExpiryDuration:   1 * time.Hour,
		AutoRulePriority: 30,
	}
}

// EventPair is a co-occurring event-type combination observed in the buffer.
type EventPair struct {
	TriggerType   string // lexicographically smaller
	ConditionType string // lexicographically larger
	Scope         string // samePod | sameNode | sameNamespace
}

func (p EventPair) key() string {
	return p.TriggerType + "::" + p.ConditionType + "::" + p.Scope
}

// patternRecord tracks observation frequency for one EventPair.
type patternRecord struct {
	Pair        EventPair
	FirstSeen   time.Time
	LastSeen    time.Time
	Occurrences int    // incremented once per analysis tick
	RuleName    string // name of auto-created rule; empty = not yet created
}

// PatternMiner mines the correlation buffer and auto-generates rules.
type PatternMiner struct {
	buf     *Buffer
	db      *sql.DB
	cfg     MinerConfig
	mu      sync.Mutex
	records map[string]*patternRecord
}

// NewPatternMiner creates a miner backed by the given buffer and DB.
func NewPatternMiner(buf *Buffer, db *sql.DB, cfg MinerConfig) *PatternMiner {
	if cfg.AnalysisInterval == 0 {
		cfg = defaultMinerConfig()
	}
	return &PatternMiner{buf: buf, db: db, cfg: cfg, records: make(map[string]*patternRecord)}
}

// Run starts the mining loop; blocks until ctx is cancelled.
// Register this as a goroutine in the API service.
func (m *PatternMiner) Run(ctx context.Context) {
	ticker := time.NewTicker(m.cfg.AnalysisInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.tick(ctx)
		}
	}
}

// TrackedPatterns returns the number of pattern records currently tracked.
func (m *PatternMiner) TrackedPatterns() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.records)
}

// ─── Internal tick ────────────────────────────────────────────────────────────

func (m *PatternMiner) tick(ctx context.Context) {
	entries := m.buf.Snapshot()
	if len(entries) == 0 {
		return
	}

	// 1. Mine co-occurring event pairs from the buffer snapshot
	pairs := minePatterns(entries)

	// 2. Record each pair in the accumulator (once per tick, not per event)
	now := time.Now()
	m.mu.Lock()
	for _, pair := range pairs {
		k := pair.key()
		rec, ok := m.records[k]
		if !ok {
			rec = &patternRecord{Pair: pair, FirstSeen: now}
			m.records[k] = rec
		}
		rec.LastSeen = now
		rec.Occurrences++
	}
	m.mu.Unlock()

	// 3. Create / update rules for patterns that crossed the threshold
	if m.db != nil {
		m.createReadyRules(ctx, now)
		m.expireStaleRules(ctx, now)
	}

	// 4. Prune records for patterns that have gone completely quiet
	m.pruneStale(now)
}

// minePatterns finds all co-occurring (trigger, condition) event pairs within
// the same scope grouping. Implements RCA-Operator's MinePatterns algorithm.
func minePatterns(entries []BufferEntry) []EventPair {
	// Group entries by scope key — separate groups for pod, node, namespace
	podGroups := make(map[string]map[string]bool)  // ns:pod → set of event types
	nodeGroups := make(map[string]map[string]bool)  // nodeName → set of event types
	nsGroups := make(map[string]map[string]bool)    // namespace → set of event types

	for _, e := range entries {
		et := e.EventType
		// Skip lifecycle events
		if et == "resource.created" || et == "resource.deleted" {
			continue
		}

		switch {
		case IsPodEventType(et) && e.Scope.Namespace != "" && e.Scope.PodName != "":
			k := e.Scope.Namespace + ":" + e.Scope.PodName
			if podGroups[k] == nil {
				podGroups[k] = make(map[string]bool)
			}
			podGroups[k][et] = true

		case IsNodeEventType(et) && e.Scope.NodeName != "":
			k := e.Scope.NodeName
			if nodeGroups[k] == nil {
				nodeGroups[k] = make(map[string]bool)
			}
			nodeGroups[k][et] = true

		case IsWorkloadEventType(et) && e.Scope.Namespace != "":
			k := e.Scope.Namespace
			if nsGroups[k] == nil {
				nsGroups[k] = make(map[string]bool)
			}
			nsGroups[k][et] = true
		}
	}

	seen := make(map[string]bool)
	var pairs []EventPair
	emitPairs(podGroups, "samePod", seen, &pairs)
	emitPairs(nodeGroups, "sameNode", seen, &pairs)
	emitPairs(nsGroups, "sameNamespace", seen, &pairs)
	return pairs
}

// emitPairs produces canonical (lexicographic) pairs from each scope group.
func emitPairs(groups map[string]map[string]bool, scope string, seen map[string]bool, out *[]EventPair) {
	for _, typeSet := range groups {
		if len(typeSet) < 2 {
			continue // single-type group — no co-occurrence to mine
		}
		types := make([]string, 0, len(typeSet))
		for t := range typeSet {
			types = append(types, t)
		}
		sort.Strings(types) // canonical order

		for i := 0; i < len(types); i++ {
			for j := i + 1; j < len(types); j++ {
				p := EventPair{TriggerType: types[i], ConditionType: types[j], Scope: scope}
				k := p.key()
				if !seen[k] {
					seen[k] = true
					*out = append(*out, p)
				}
			}
		}
	}
}

// createReadyRules writes a correlation rule for each pattern that crossed the threshold.
func (m *PatternMiner) createReadyRules(ctx context.Context, now time.Time) {
	// Count existing auto-generated rules to enforce MaxAutoRules
	var existingCount int
	if m.db != nil {
		_ = m.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM kubesense_correlation_rules WHERE auto_generated = TRUE AND (expires_at IS NULL OR expires_at > NOW())`).
			Scan(&existingCount)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, rec := range m.records {
		// Check thresholds
		if rec.Occurrences < m.cfg.MinOccurrences {
			continue
		}
		if rec.LastSeen.Sub(rec.FirstSeen) < m.cfg.MinTimeSpan {
			continue
		}
		// Skip if rule already created and still fresh
		if rec.RuleName != "" {
			// Just update data_points and last_seen on existing rule
			if m.db != nil {
				_ = UpsertAutoRule(ctx, m.db, rec.RuleName,
					rec.Pair.TriggerType, rec.Pair.ConditionType, rec.Pair.Scope,
					autoIncidentType(rec.Pair), autoSeverity(rec.Pair.Scope),
					autoSummary(rec.Pair),
					rec.Occurrences, rec.FirstSeen, rec.LastSeen)
			}
			continue
		}
		// New rule — check cap
		if existingCount >= m.cfg.MaxAutoRules {
			continue
		}

		ruleName := fmt.Sprintf("auto.%s.%s.%s",
			sanitizeName(rec.Pair.TriggerType),
			sanitizeName(rec.Pair.ConditionType),
			rec.Pair.Scope)

		if m.db != nil {
			if err := UpsertAutoRule(ctx, m.db, ruleName,
				rec.Pair.TriggerType, rec.Pair.ConditionType, rec.Pair.Scope,
				autoIncidentType(rec.Pair), autoSeverity(rec.Pair.Scope),
				autoSummary(rec.Pair),
				rec.Occurrences, rec.FirstSeen, rec.LastSeen); err == nil {
				rec.RuleName = ruleName
				existingCount++
			}
		}
	}
}

// expireStaleRules deletes auto-generated rules whose patterns have gone quiet.
func (m *PatternMiner) expireStaleRules(ctx context.Context, now time.Time) {
	if m.db == nil {
		return
	}
	_, _ = m.db.ExecContext(ctx, `
		DELETE FROM kubesense_correlation_rules
		WHERE auto_generated = TRUE AND expires_at IS NOT NULL AND expires_at < $1
	`, now)
}

// pruneStale removes pattern records that have not been seen for ExpiryDuration.
func (m *PatternMiner) pruneStale(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := now.Add(-m.cfg.ExpiryDuration)
	for k, rec := range m.records {
		if rec.LastSeen.Before(cutoff) {
			delete(m.records, k)
		}
	}
}

// ─── Auto-rule naming helpers ─────────────────────────────────────────────────

func autoIncidentType(p EventPair) string {
	return fmt.Sprintf("Auto.%s+%s", classifyEvent(p.TriggerType), classifyEvent(p.ConditionType))
}

func autoSeverity(scope string) string {
	switch scope {
	case "sameNode":
		return "P1" // node-level issues are always severe
	case "samePod":
		return "P2"
	default:
		return "P3"
	}
}

func autoSummary(p EventPair) string {
	return fmt.Sprintf("Auto-detected: %s co-occurring with %s (%s scope) — {{.Namespace}}/{{.Name}}",
		p.TriggerType, p.ConditionType, p.Scope)
}

func classifyEvent(eventType string) string {
	switch {
	case eventType == "health.pod.crashloopbackoff":
		return "CrashLoop"
	case eventType == "health.pod.oomkilled":
		return "OOMKilled"
	case eventType == "health.node.not_ready":
		return "NodeNotReady"
	case eventType == "health.node.disk_pressure":
		return "DiskPressure"
	case eventType == "health.node.memory_pressure":
		return "MemoryPressure"
	case eventType == "storage.pvc.near_full":
		return "PVCNearFull"
	default:
		parts := splitDots(eventType)
		if len(parts) >= 3 {
			return toCamel(parts[2])
		}
		return toCamel(eventType)
	}
}

func sanitizeName(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			out = append(out, c)
		} else if c == '.' || c == '_' || c == '-' {
			out = append(out, '-')
		}
	}
	return string(out)
}

func splitDots(s string) []string {
	var parts []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '.' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return parts
}

func toCamel(s string) string {
	if len(s) == 0 {
		return s
	}
	b := []byte(s)
	b[0] = upper(b[0])
	return string(b)
}

func upper(c byte) byte {
	if c >= 'a' && c <= 'z' {
		return c - 32
	}
	return c
}
