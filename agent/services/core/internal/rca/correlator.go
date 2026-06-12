// Package rca — correlation engine orchestrator.
//
// Wires together the sliding buffer, rule engine, pattern miner, and incident
// lifecycle manager into a single entry point. Call Run() in a goroutine.
// The API service calls Feed() for every new event from the Kafka consumer.
// When an incident transitions to Active, publishes to kubesense.correlation.incident-context
// so the kubesense-llm narrator service can generate an evidence-grounded narrative.
package rca

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"

	"github.com/IBM/sarama"
)

// CorrelatorEngine is the top-level orchestrator.
// Create one per API service instance and call Run + Feed.
type CorrelatorEngine struct {
	buf           *Buffer
	rules         *RuleEngine
	miner         *PatternMiner
	lifecycle     *LifecycleManager
	metrics       *RCAMetrics
	db            *sql.DB
	narrator      sarama.SyncProducer // publishes to kubesense.correlation.incident-context

	// Algorithm 2 — Adaptive Baseline (Holt-Winters)
	baseline *BaselineRegistry
	// Algorithm 5 — Alert State Machine with flap suppression
	stateMachines *StateMachineRegistry
	// Algorithm 3 — Multi-Signal Union-Find grouper
	grouper *MultiSignalGrouper
	// Algorithm 4 — Topology-anchored RCA scoring
	topoRCA *TopoRCAEngine
	// Algorithm 6 — Change correlation RCA
	changeCorrel *ChangeCorrelator

	// flapSuppressed counts events dropped by state machine flap detection.
	flapSuppressed int64
}

// NewCorrelatorEngine creates a fully-wired correlator engine backed by the
// given PostgreSQL database.  Pass nil db to run in-memory only (no persistence).
func NewCorrelatorEngine(db *sql.DB) *CorrelatorEngine {
	buf := NewBuffer(defaultBufferWindow)
	metrics := NewRCAMetrics()
	rules := NewRuleEngine(db, buf, 30*time.Second)
	miner := NewPatternMiner(buf, db, defaultMinerConfig())
	lifecycle := NewLifecycleManager(db, metrics)

	// Algorithm 2 — Adaptive Baseline
	baseline := NewBaselineRegistry()

	// Algorithm 5 — Alert State Machine (flap suppression + hysteresis)
	stateMachines := NewStateMachineRegistry()

	// Algorithm 3 — Multi-Signal Union-Find grouper (15-min window, no graph calls)
	grouper := NewMultiSignalGrouper(nil, defaultBufferWindow)

	// Algorithm 4 — Topology-anchored RCA (static scoring, no live Neo4j)
	topoRCA := NewTopoRCAEngine(nil, db)

	// Algorithm 6 — Change correlation (2-hour lookback)
	changeCorrel := NewChangeCorrelator(db)

	return &CorrelatorEngine{
		buf:           buf,
		rules:         rules,
		miner:         miner,
		lifecycle:     lifecycle,
		metrics:       metrics,
		db:            db,
		baseline:      baseline,
		stateMachines: stateMachines,
		grouper:       grouper,
		topoRCA:       topoRCA,
		changeCorrel:  changeCorrel,
	}
}

// SetNarratorProducer wires a Kafka producer so the engine publishes to
// kubesense.correlation.incident-context when an incident transitions to Active.
// The kubesense-llm service consumes this topic and produces LLM narratives.
func (ce *CorrelatorEngine) SetNarratorProducer(p sarama.SyncProducer) {
	ce.narrator = p
}

// InitSchema creates all required tables in KubeSense's PostgreSQL.
// Call once on startup before Run.
func (ce *CorrelatorEngine) InitSchema(ctx context.Context) {
	if ce.db == nil {
		return
	}
	if err := EnsureRuleSchema(ctx, ce.db); err != nil {
		log.Printf("rca-correlator: rule schema init: %v", err)
	}
	if err := EnsureIncidentSchema(ctx, ce.db); err != nil {
		log.Printf("rca-correlator: incident schema init: %v", err)
	}
}

// Run starts the background goroutines (rule loader, pattern miner, lifecycle manager).
// Blocks until ctx is cancelled. Call in a goroutine.
func (ce *CorrelatorEngine) Run(ctx context.Context) {
	// Load rules on startup
	if err := ce.rules.LoadRules(ctx); err != nil {
		log.Printf("rca-correlator: initial rule load failed: %v", err)
	}
	log.Printf("rca-correlator: started — %d rules loaded", ce.rules.RuleCount())

	// Seed buffer from recent DB events (last 15 min) so rules fire immediately
	ce.seedBufferFromDB(ctx)

	// Background workers
	go ce.miner.Run(ctx)
	go ce.lifecycle.Run(ctx)
	go ce.ruleReloadLoop(ctx)

	<-ctx.Done()
	log.Println("rca-correlator: stopping")
}

// Feed adds a cluster event to the correlation buffer and evaluates all rules.
// Call this for every event received from the Kafka consumer.
func (ce *CorrelatorEngine) Feed(ctx context.Context, entry BufferEntry) {
	// Refresh rules if stale
	ce.rules.EnsureLoaded(ctx)

	// Add to sliding window buffer
	ce.buf.Add(entry)

	// Emit signal received metric
	if ce.metrics != nil {
		ce.metrics.SignalsReceived.WithLabelValues(entry.EventType).Inc()
	}

	// ── Algorithm 5: State-machine flap suppression ───────────────────────────
	// Treat every incoming event as an anomaly signal for its resource+event-type
	// pair. If the machine says SUPPRESS (flapping) we drop the event entirely.
	now := time.Now()
	transition := ce.stateMachines.Evaluate(entry.Scope.ResourceName, entry.EventType, true, now)
	if transition != nil && transition.Type == TransitionSuppress {
		atomic.AddInt64(&ce.flapSuppressed, 1)
		return // flap suppression working — discard noisy event
	}

	// ── Algorithm 3: Multi-Signal grouper ────────────────────────────────────
	// Feed into the Union-Find grouper to build/update incident groups.
	ce.grouper.Ingest(AlertSignal{
		ID:         entry.Scope.ClusterID + ":" + entry.Scope.ResourceName + ":" + entry.EventType + ":" + fmt.Sprintf("%d", now.UnixNano()),
		EntityID:   entry.Scope.ResourceName,
		EntityType: entry.Scope.ResourceKind,
		EventType:  entry.EventType,
		Namespace:  entry.Scope.Namespace,
		Severity:   entry.Severity,
		NodeName:   entry.Scope.NodeName,
		FiredAt:    now,
	})

	// ── Rule-based correlator (existing behaviour kept intact) ────────────────
	result := ce.rules.Evaluate(entry)
	if !result.Fired {
		return
	}

	// Rule fired — hand off to lifecycle manager; capture whether a new incident was created
	prevCount := len(ce.lifecycle.ListActive())
	ce.lifecycle.OnEvent(ctx, entry, result)
	newCount := len(ce.lifecycle.ListActive())

	// If a new incident just went Active, publish to kubesense.correlation.incident-context
	// so the kubesense-llm narrator service can generate an LLM narrative.
	if ce.narrator != nil && newCount > prevCount {
		ce.publishNarratorContext(ctx, entry, result)
	}
}

// ActiveIncidents returns all non-Resolved incidents for a cluster.
func (ce *CorrelatorEngine) ActiveIncidents(clusterID string) []*Incident {
	if clusterID == "" {
		return ce.lifecycle.ListActive()
	}
	return ce.lifecycle.ListByCluster(clusterID)
}

// BufferLen returns the current number of entries in the sliding window.
func (ce *CorrelatorEngine) BufferLen() int { return ce.buf.Len() }

// RuleCount returns the number of loaded correlation rules.
func (ce *CorrelatorEngine) RuleCount() int { return ce.rules.RuleCount() }

// TrackedPatterns returns the number of co-occurrence patterns being mined.
func (ce *CorrelatorEngine) TrackedPatterns() int { return ce.miner.TrackedPatterns() }

// BaselineModelCount returns the number of active Holt-Winters baseline models.
func (ce *CorrelatorEngine) BaselineModelCount() int {
	if ce.baseline == nil {
		return 0
	}
	ce.baseline.mu.RLock()
	defer ce.baseline.mu.RUnlock()
	return len(ce.baseline.models)
}

// FlapSuppressedCount returns the cumulative number of events suppressed by
// the state machine flap detector since startup.
func (ce *CorrelatorEngine) FlapSuppressedCount() int64 {
	return atomic.LoadInt64(&ce.flapSuppressed)
}

// IncidentGroupCount returns the number of open incident groups in the grouper.
func (ce *CorrelatorEngine) IncidentGroupCount() int {
	if ce.grouper == nil {
		return 0
	}
	ce.grouper.mu.Lock()
	defer ce.grouper.mu.Unlock()
	return len(ce.grouper.groups)
}

// ─── Background helpers ───────────────────────────────────────────────────────

// ruleReloadLoop ensures rules are refreshed from DB every 30s even when no
// events arrive (so manually added/edited rules take effect promptly).
func (ce *CorrelatorEngine) ruleReloadLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := ce.rules.LoadRules(ctx); err != nil {
				log.Printf("rca-correlator: rule reload: %v", err)
			}
		}
	}
}

// seedBufferFromDB pre-populates the buffer with recent health events from the DB
// so rules can fire immediately on startup without waiting 15 minutes.
func (ce *CorrelatorEngine) seedBufferFromDB(ctx context.Context) {
	if ce.db == nil {
		return
	}
	rows, err := ce.db.QueryContext(ctx, `
		SELECT event_type, severity, COALESCE(namespace,''), COALESCE(resource_kind,''),
		       COALESCE(resource_name,''), cluster_id, occurred_at
		FROM kubesense_health_events
		WHERE occurred_at > NOW() - INTERVAL '15 minutes'
		ORDER BY occurred_at ASC
		LIMIT 5000
	`)
	if err != nil {
		return
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var e BufferEntry
		var occurredAt time.Time
		if err := rows.Scan(&e.EventType, &e.Severity,
			&e.Scope.Namespace, &e.Scope.ResourceKind, &e.Scope.ResourceName,
			&e.Scope.ClusterID, &occurredAt); err != nil {
			continue
		}
		// Derive pod/node name from resource_kind + name
		if e.Scope.ResourceKind == "Pod" {
			e.Scope.PodName = e.Scope.ResourceName
		} else if e.Scope.ResourceKind == "Node" {
			e.Scope.NodeName = e.Scope.ResourceName
		}
		e.AddedAt = occurredAt
		ce.buf.Add(e)
		count++
	}
	if count > 0 {
		log.Printf("rca-correlator: seeded buffer with %d events from last 15 min", count)
	}
}

// publishNarratorContext publishes a context payload to kubesense.correlation.incident-context
// so the kubesense-llm service can generate a narrative for the new incident.
func (ce *CorrelatorEngine) publishNarratorContext(ctx context.Context, trigger BufferEntry, result CorrelationResult) {
	_ = ctx // reserved for future use
	// Build a context payload the LLM service understands.
	// Include the last 20 buffer entries matching the same scope as context signals.
	entries := ce.buf.Snapshot()
	type signal struct {
		Source       string    `json:"source"`
		Type         string    `json:"type"`
		Description  string    `json:"description"`
		Strength     float64   `json:"strength"`
		ResourceKind string    `json:"resource_kind"`
		ResourceName string    `json:"resource_name"`
		Namespace    string    `json:"namespace"`
		Timestamp    time.Time `json:"timestamp"`
	}
	var signals []signal
	for _, e := range entries {
		if len(signals) >= 20 { break }
		if ScopeMatches(trigger, e, result.Scope) {
			signals = append(signals, signal{
				Source:       "kubesense",
				Type:         e.EventType,
				Description:  fmt.Sprintf("%s on %s/%s", e.EventType, e.Scope.Namespace, e.Scope.ResourceName),
				Strength:     0.8,
				ResourceKind: e.Scope.ResourceKind,
				ResourceName: e.Scope.ResourceName,
				Namespace:    e.Scope.Namespace,
				Timestamp:    e.AddedAt,
			})
		}
	}

	fp := IncidentFingerprint(trigger.Scope.Namespace, trigger.Scope.ResourceKind, trigger.Scope.ResourceName, result.Scope)
	payload := map[string]interface{}{
		"incident_id":    "inc-" + fp,
		"cluster_id":     trigger.Scope.ClusterID,
		"title":          result.Summary,
		"detected_at":    time.Now(),
		"signals":        signals,
		"evidence_grade": gradeFromRuleScope(result.Scope),
		"confidence":     0.75,
	}
	body, _ := json.Marshal(payload)
	_, _, err := ce.narrator.SendMessage(&sarama.ProducerMessage{
		Topic: "kubesense.correlation.incident-context",
		Key:   sarama.StringEncoder(fp),
		Value: sarama.ByteEncoder(body),
	})
	if err != nil {
		log.Printf("rca-correlator: narrator publish failed: %v", err)
	}
}

func gradeFromRuleScope(scope string) string {
	switch scope {
	case "sameNode": return "A"
	case "samePod":  return "B"
	default:         return "C"
	}
}

// NewNarratorProducer creates a Kafka producer for the narrator topic.
func NewNarratorProducer(brokers string) sarama.SyncProducer {
	cfg := sarama.NewConfig()
	cfg.Producer.Return.Successes = true
	cfg.Producer.RequiredAcks = sarama.WaitForAll
	cfg.Producer.Retry.Max = 3
	p, err := sarama.NewSyncProducer(strings.Split(brokers, ","), cfg)
	if err != nil {
		log.Printf("rca-correlator: narrator producer unavailable: %v", err)
		return nil
	}
	return p
}
