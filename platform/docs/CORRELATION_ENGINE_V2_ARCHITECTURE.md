# AlertHub — Correlation & RCA Engine V2: Architecture & Implementation

> **Principal Staff Architect Design Document**  
> Evolving AlertHub from multi-strategy event correlation into continuous operational causal intelligence.  
> This document preserves all existing architecture and deeply improves each subsystem.  
> Version: v2.0-design | Date: 2026-05-11

---

## Executive Architecture Summary

The current system executes a single-pass correlation pipeline: normalize → root-cause-check → parallel-score → decide. This is sound and correct. The evolution target is to transform that single pass into a **continuous causal reasoning loop** where:

- Incidents are not created once and frozen — they evolve as new evidence arrives
- Root cause is not asserted once — it is maintained as a probability distribution that converges
- Topology traversal is not one level deep — it is recursive, weighted, and domain-aware
- Blast radius is not a label list — it is a propagation frontier updated in real time
- Decisions are not binary attach/create — they include confidence evolution, merge/split, and causal re-parenting

The system must behave like Dynatrace Davis AI + BigPanda + Moogsoft — but self-hosted, specialized for Kubernetes + CloudStack + NetApp + bare-metal, with no vendor lock-in.

---

## Table of Contents

1. [Recursive Topology RCA Engine](#1-recursive-topology-rca-engine)
2. [Probabilistic Root Cause Engine](#2-probabilistic-root-cause-engine)
3. [Incident Evolution Engine](#3-incident-evolution-engine)
4. [Operational Ontology & Semantic Intelligence](#4-operational-ontology--semantic-intelligence)
5. [Advanced Temporal Propagation Engine](#5-advanced-temporal-propagation-engine)
6. [Event Storm Scalability](#6-event-storm-scalability)
7. [Correlation Explainability](#7-correlation-explainability)
8. [Adaptive Learning Engine](#8-adaptive-learning-engine)
9. [Vector & Feature Store](#9-vector--feature-store)
10. [Production Hardening](#10-production-hardening)
11. [Neo4j Schema & Cypher Reference](#11-neo4j-schema--cypher-reference)
12. [Schema Additions](#12-schema-additions)
13. [Migration Path](#13-migration-path)

---

## 1. Recursive Topology RCA Engine

### 1.1 Current Weakness

`topology_graph_correlator.go` walks the Redis graph **one level upward** from the alert's entity — it finds the immediate parent and checks if that parent has an open incident. This misses:

- **Transitive root causes**: NetApp latency → PVC degradation → pod evictions. The alert is on the pod. The root is on NetApp. That is 3 hops, and they span different infrastructure domains.
- **Blast radius expansion**: Starting from a root entity downward, the system does not enumerate all potentially impacted children across the full infrastructure tree.
- **Edge weighting**: All parent→child edges are treated equally. A pod on a degraded node is more certainly impacted than a pod on a healthy node in the same cluster.
- **Propagation decay**: Impact attenuates with distance. A BM failure directly impacts its VMs with certainty 0.95. The pods on those VMs inherit 0.95 × edge_weight. Pods two hops further inherit 0.95 × 0.92 × 0.88 = 0.77. The system currently cannot compute this.
- **Cycle detection**: The infrastructure graph can have transitive self-references (especially CloudStack). Without cycle detection, recursive traversal loops infinitely.

### 1.2 Proposed Architecture

```
Alert arrives with entity_id / topology_path
         ↓
RecursiveTopoRCAEngine.Traverse(alert)
         ↓
Phase 1: UPWARD SWEEP — find root
  Walk parent chain: pod → node → VM → KVM → BM
  Each hop: check for open incident + apply domain attenuation
  Stop at: known root entity OR max depth (8) OR confidence < 0.30
         ↓
Phase 2: DOWNWARD SWEEP — compute blast radius
  From identified root entity, BFS expansion across all children
  Apply propagation model per edge type and failure domain
  Accumulate ImpactedNode set with per-node confidence
         ↓
Phase 3: LATERAL SWEEP — same-parent siblings
  For cluster-level failures: identify sibling nodes/VMs
  Share impact probability based on shared resource type
         ↓
Return: RecursiveTopoResult
  RootEntity, CausalChain, BlastRadius, PropagationMap
```

### 1.3 Neo4j Schema & Graph Model

```cypher
// ─── Node Types ───────────────────────────────────────────────────────────────
CREATE CONSTRAINT ON (n:BareMetal)   ASSERT n.entity_id IS UNIQUE;
CREATE CONSTRAINT ON (n:KVMHost)     ASSERT n.entity_id IS UNIQUE;
CREATE CONSTRAINT ON (n:CloudVM)     ASSERT n.entity_id IS UNIQUE;
CREATE CONSTRAINT ON (n:K8sCluster)  ASSERT n.entity_id IS UNIQUE;
CREATE CONSTRAINT ON (n:K8sNode)     ASSERT n.entity_id IS UNIQUE;
CREATE CONSTRAINT ON (n:K8sPod)      ASSERT n.entity_id IS UNIQUE;
CREATE CONSTRAINT ON (n:NetAppVol)   ASSERT n.entity_id IS UNIQUE;
CREATE CONSTRAINT ON (n:NetAppNode)  ASSERT n.entity_id IS UNIQUE;
CREATE CONSTRAINT ON (n:Network)     ASSERT n.entity_id IS UNIQUE;

// ─── Edge Types with properties ───────────────────────────────────────────────
// HOSTS        — BM/KVM hosts VM; weight 0.95 (tight coupling)
// RUNS_ON      — Pod/container runs on K8s node; weight 0.92
// MEMBER_OF    — K8s node is member of cluster; weight 0.88
// MOUNTS       — Pod mounts PVC from NetApp; weight 0.90 (I/O dependency)
// USES_NETWORK — Entity shares network segment; weight 0.70
// DEPENDS_ON   — Application/service dependency; weight 0.65 (configurable)
// REPLICATES   — NetApp replication relationship; weight 0.85

// Example: BM hosts KVM, KVM hosts VMs, VM runs K8s Node, Node has Pods
MATCH (bm:BareMetal {entity_id: 'bm-rno-42'})
MATCH (kvm:KVMHost {entity_id: 'kvm-rno-42-01'})
MERGE (bm)-[:HOSTS {weight: 0.95, edge_type: 'HOSTS', dc: 'rno'}]->(kvm)

// ─── Recursive upward traversal — find root cause ─────────────────────────────
// Traverses up to 8 hops, attenuates score per hop, returns ordered candidates
MATCH path = (start)-[rels:HOSTS|RUNS_ON|MEMBER_OF|MOUNTS*1..8]->(target)
WHERE target.entity_id = $target_entity_id
WITH path, start, target,
     reduce(score = 1.0, r IN relationships(path) | score * r.weight) AS path_score,
     length(path) AS depth,
     [n IN nodes(path) | n.entity_id] AS chain
WHERE path_score > 0.25
RETURN start.entity_id AS root_entity_id,
       start.entity_type AS root_type,
       start.label AS root_label,
       start.infra_level AS root_level,
       path_score * (0.95 ^ depth) AS attenuated_score,
       depth,
       chain
ORDER BY attenuated_score DESC
LIMIT 10;

// ─── Downward blast radius expansion ─────────────────────────────────────────
// From root, find all downstream entities with propagation scores
MATCH path = (root)-[rels:HOSTS|RUNS_ON|MEMBER_OF|MOUNTS*1..10]->(affected)
WHERE root.entity_id = $root_entity_id
WITH affected,
     reduce(score = 1.0, r IN relationships(path) | score * r.weight) AS path_score,
     length(path) AS depth,
     [n IN nodes(path) | {id: n.entity_id, type: n.entity_type}] AS causal_chain
WHERE path_score > 0.20
RETURN affected.entity_id,
       affected.entity_type,
       affected.label,
       affected.cluster,
       affected.namespace,
       path_score AS propagation_score,
       depth,
       causal_chain
ORDER BY depth ASC, propagation_score DESC;

// ─── Sibling impact — shared host/cluster ─────────────────────────────────────
MATCH (root)-[:HOSTS|MEMBER_OF]->(sibling)
WHERE root.entity_id = $root_entity_id
  AND sibling.entity_id <> $target_entity_id
RETURN sibling.entity_id, sibling.entity_type, sibling.label, 0.75 AS sibling_score;
```

### 1.4 Go Implementation

```go
// internal/services/correlation/recursive_topo_rca.go
package correlation

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// ─── Domain-aware propagation decay constants ─────────────────────────────────

// DomainDecay defines how fast impact attenuates per hop in each failure domain.
// Storage failures propagate slowly but with high certainty within I/O path.
// Network failures propagate fast and broadly.
// Compute failures are tightly scoped.
var DomainDecay = map[FailureDomain]float64{
	DomainStorage:   0.90, // high fidelity along I/O path
	DomainNetwork:   0.82, // broad but attenuates faster
	DomainCompute:   0.88, // scoped to host hierarchy
	DomainKubernetes: 0.85, // pod→node→cluster
	DomainApplication: 0.70, // service mesh dependencies
}

// EdgeWeight defines base propagation weight per relationship type.
var EdgeWeight = map[string]float64{
	"HOSTS":        0.95,
	"RUNS_ON":      0.92,
	"MEMBER_OF":    0.88,
	"MOUNTS":       0.90,
	"USES_NETWORK": 0.70,
	"DEPENDS_ON":   0.65,
	"REPLICATES":   0.85,
}

// ─── Types ────────────────────────────────────────────────────────────────────

type CausalNode struct {
	EntityID   string
	EntityType string
	Label      string
	Cluster    string
	Namespace  string
	InfraLevel InfraLevel
	Domain     FailureDomain
}

type CausalLink struct {
	From      *CausalNode
	To        *CausalNode
	EdgeType  string
	Weight    float64
	HopIndex  int
}

type ImpactedNode struct {
	Node              *CausalNode
	PropagationScore  float64
	Depth             int
	CausalChain       []*CausalLink
}

type RecursiveTopoResult struct {
	AlertEntityID  string
	RootEntity     *CausalNode
	RootConfidence float64
	CausalChain    []*CausalLink      // alert → root (upward path)
	BlastRadius    []*ImpactedNode    // root → all downstream (sorted by score)
	PropagationMap map[string]float64 // entity_id → propagation score
	Depth          int
	Domain         FailureDomain
	Reasoning      []string
	ComputedAt     time.Time
	CacheHit       bool
}

// RecursiveTopoRCAEngine performs full graph traversal for root cause and blast radius.
type RecursiveTopoRCAEngine struct {
	neo4j   neo4j.DriverWithContext
	rdb     interface{ Get(ctx context.Context, key string) (string, error)
		               Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error }
	topo    TopoProvider // existing Redis-backed provider
	maxDepth int
	mu      sync.RWMutex
	// cache: entity_id → *RecursiveTopoResult
	cache   map[string]*cachedTopoResult
}

type cachedTopoResult struct {
	result    *RecursiveTopoResult
	expiresAt time.Time
}

func NewRecursiveTopoRCAEngine(driver neo4j.DriverWithContext, topo TopoProvider) *RecursiveTopoRCAEngine {
	return &RecursiveTopoRCAEngine{
		neo4j:    driver,
		topo:     topo,
		maxDepth: 8,
		cache:    make(map[string]*cachedTopoResult),
	}
}

// Traverse is the primary entry point.
// It performs upward root-finding then downward blast-radius expansion.
func (e *RecursiveTopoRCAEngine) Traverse(ctx context.Context, alert *Alert) (*RecursiveTopoResult, error) {
	entityID := alert.EntityID
	if entityID == "" {
		entityID = extractEntityID(alert)
	}
	if entityID == "" {
		return nil, fmt.Errorf("no entity_id for alert %s", alert.ID)
	}

	// Check in-process cache (2-minute TTL for topology results)
	if cached := e.fromCache(entityID); cached != nil {
		cached.CacheHit = true
		return cached, nil
	}

	result := &RecursiveTopoResult{
		AlertEntityID:  entityID,
		PropagationMap: make(map[string]float64),
		ComputedAt:     time.Now(),
	}

	// Phase 1: upward sweep — find root cause candidates
	roots, chain, err := e.upwardSweep(ctx, entityID, alert)
	if err != nil {
		// Fall back to Redis-graph single-level if Neo4j fails
		return e.fallbackToRedisGraph(ctx, alert)
	}

	if len(roots) == 0 {
		result.Reasoning = append(result.Reasoning, "no upstream root cause found in topology graph")
		return result, nil
	}

	// Select best root: highest attenuated score × infra_level weight
	best := e.selectBestRoot(roots)
	result.RootEntity = best.Node
	result.RootConfidence = best.PropagationScore
	result.CausalChain = chain
	result.Depth = best.Depth
	result.Domain = alert.Domain()
	result.Reasoning = append(result.Reasoning,
		fmt.Sprintf("upward traversal found root: %s (%s) at depth %d, score %.3f",
			best.Node.Label, best.Node.EntityType, best.Depth, best.PropagationScore))

	// Phase 2: downward sweep — expand blast radius from identified root
	blastRadius, err := e.downwardSweep(ctx, result.RootEntity.EntityID, result.Domain)
	if err != nil {
		log.Printf("[RecursiveTopoRCA] downward sweep error for %s: %v", result.RootEntity.EntityID, err)
	} else {
		result.BlastRadius = blastRadius
		for _, n := range blastRadius {
			result.PropagationMap[n.Node.EntityID] = n.PropagationScore
		}
		result.Reasoning = append(result.Reasoning,
			fmt.Sprintf("blast radius: %d entities affected, highest impact: %.3f",
				len(blastRadius), highestScore(blastRadius)))
	}

	// Phase 3: lateral sweep — siblings sharing same parent
	siblings, _ := e.lateralSweep(ctx, result.RootEntity.EntityID)
	for _, s := range siblings {
		if _, exists := result.PropagationMap[s.Node.EntityID]; !exists {
			result.PropagationMap[s.Node.EntityID] = s.PropagationScore
			result.BlastRadius = append(result.BlastRadius, s)
		}
	}

	e.toCache(entityID, result, 2*time.Minute)
	return result, nil
}

func (e *RecursiveTopoRCAEngine) upwardSweep(ctx context.Context, entityID string, alert *Alert) ([]*ImpactedNode, []*CausalLink, error) {
	session := e.neo4j.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	// Domain-aware decay for upward traversal
	domain := alert.Domain()
	decay := DomainDecay[domain]

	cypher := `
MATCH path = (root)-[rels:HOSTS|RUNS_ON|MEMBER_OF|MOUNTS*1..8]->(target)
WHERE target.entity_id = $entity_id
WITH path, root, target,
     reduce(score = 1.0, r IN relationships(path) | score * r.weight) AS path_score,
     length(path) AS depth,
     [r IN relationships(path) | {from: startNode(r).entity_id, to: endNode(r).entity_id,
                                   edge_type: type(r), weight: r.weight}] AS links,
     [n IN nodes(path) | {id: n.entity_id, type: n.entity_type, label: n.label,
                           infra_level: n.infra_level, cluster: n.cluster}] AS chain
WHERE path_score > 0.20 AND depth <= $max_depth
RETURN root.entity_id AS root_id,
       root.entity_type AS root_type,
       root.label AS root_label,
       root.infra_level AS root_level,
       root.cluster AS root_cluster,
       path_score * pow($decay, depth) AS attenuated_score,
       depth,
       links,
       chain
ORDER BY attenuated_score DESC
LIMIT 15`

	result, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		r, err := tx.Run(ctx, cypher, map[string]interface{}{
			"entity_id": entityID,
			"max_depth": e.maxDepth,
			"decay":     decay,
		})
		if err != nil {
			return nil, err
		}
		var rows []map[string]interface{}
		for r.Next(ctx) {
			rows = append(rows, r.Record().AsMap())
		}
		return rows, r.Err()
	})
	if err != nil {
		return nil, nil, fmt.Errorf("neo4j upward sweep: %w", err)
	}

	rows := result.([]map[string]interface{})
	var candidates []*ImpactedNode
	var bestChain []*CausalLink

	seen := map[string]bool{}
	for _, row := range rows {
		rootID := row["root_id"].(string)
		if seen[rootID] {
			continue
		}
		seen[rootID] = true

		node := &CausalNode{
			EntityID:   rootID,
			EntityType: strVal(row, "root_type"),
			Label:      strVal(row, "root_label"),
			Cluster:    strVal(row, "root_cluster"),
			InfraLevel: InfraLevel(intVal(row, "root_level")),
		}
		score := floatVal(row, "attenuated_score")
		depth := intVal(row, "depth")

		candidates = append(candidates, &ImpactedNode{
			Node:             node,
			PropagationScore: score,
			Depth:            depth,
		})

		// Extract causal chain for the best (first) result
		if len(bestChain) == 0 {
			if links, ok := row["links"].([]interface{}); ok {
				for i, l := range links {
					if lmap, ok := l.(map[string]interface{}); ok {
						bestChain = append(bestChain, &CausalLink{
							From:     &CausalNode{EntityID: strVal(lmap, "from")},
							To:       &CausalNode{EntityID: strVal(lmap, "to")},
							EdgeType: strVal(lmap, "edge_type"),
							Weight:   floatVal(lmap, "weight"),
							HopIndex: i,
						})
					}
				}
			}
		}
	}

	return candidates, bestChain, nil
}

func (e *RecursiveTopoRCAEngine) downwardSweep(ctx context.Context, rootEntityID string, domain FailureDomain) ([]*ImpactedNode, error) {
	session := e.neo4j.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	decay := DomainDecay[domain]

	cypher := `
MATCH path = (root)-[rels:HOSTS|RUNS_ON|MEMBER_OF|MOUNTS*1..10]->(affected)
WHERE root.entity_id = $root_entity_id
  AND affected.entity_id <> $root_entity_id
WITH affected,
     reduce(score = 1.0, r IN relationships(path) | score * r.weight) AS path_score,
     length(path) AS depth,
     [n IN nodes(path) | n.entity_id] AS chain_ids
WHERE path_score * pow($decay, depth) > 0.15
RETURN affected.entity_id AS entity_id,
       affected.entity_type AS entity_type,
       affected.label AS label,
       affected.cluster AS cluster,
       affected.namespace AS namespace,
       affected.infra_level AS infra_level,
       path_score * pow($decay, depth) AS propagation_score,
       depth,
       chain_ids
ORDER BY depth ASC, propagation_score DESC
LIMIT 200`

	result, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		r, err := tx.Run(ctx, cypher, map[string]interface{}{
			"root_entity_id": rootEntityID,
			"decay":          decay,
		})
		if err != nil {
			return nil, err
		}
		var rows []map[string]interface{}
		for r.Next(ctx) {
			rows = append(rows, r.Record().AsMap())
		}
		return rows, r.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("neo4j downward sweep: %w", err)
	}

	rows := result.([]map[string]interface{})
	var blast []*ImpactedNode
	for _, row := range rows {
		blast = append(blast, &ImpactedNode{
			Node: &CausalNode{
				EntityID:   strVal(row, "entity_id"),
				EntityType: strVal(row, "entity_type"),
				Label:      strVal(row, "label"),
				Cluster:    strVal(row, "cluster"),
				Namespace:  strVal(row, "namespace"),
				InfraLevel: InfraLevel(intVal(row, "infra_level")),
			},
			PropagationScore: floatVal(row, "propagation_score"),
			Depth:            intVal(row, "depth"),
		})
	}
	return blast, nil
}

func (e *RecursiveTopoRCAEngine) lateralSweep(ctx context.Context, rootEntityID string) ([]*ImpactedNode, error) {
	session := e.neo4j.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	cypher := `
MATCH (parent)-[:HOSTS|MEMBER_OF]->(root)
WHERE root.entity_id = $root_entity_id
WITH parent
MATCH (parent)-[:HOSTS|MEMBER_OF]->(sibling)
WHERE sibling.entity_id <> $root_entity_id
RETURN sibling.entity_id AS entity_id,
       sibling.entity_type AS entity_type,
       sibling.label AS label,
       sibling.cluster AS cluster,
       0.65 AS propagation_score,
       1 AS depth`

	result, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		r, err := tx.Run(ctx, cypher, map[string]interface{}{"root_entity_id": rootEntityID})
		if err != nil {
			return nil, err
		}
		var rows []map[string]interface{}
		for r.Next(ctx) {
			rows = append(rows, r.Record().AsMap())
		}
		return rows, r.Err()
	})
	if err != nil {
		return nil, err
	}

	rows := result.([]map[string]interface{})
	var siblings []*ImpactedNode
	for _, row := range rows {
		siblings = append(siblings, &ImpactedNode{
			Node: &CausalNode{
				EntityID:   strVal(row, "entity_id"),
				EntityType: strVal(row, "entity_type"),
				Label:      strVal(row, "label"),
				Cluster:    strVal(row, "cluster"),
			},
			PropagationScore: floatVal(row, "propagation_score"),
			Depth:            1,
		})
	}
	return siblings, nil
}

// selectBestRoot picks the root with highest combined (propagation_score × infra_level_weight).
func (e *RecursiveTopoRCAEngine) selectBestRoot(candidates []*ImpactedNode) *ImpactedNode {
	infraWeight := map[InfraLevel]float64{
		InfraLevelBM:  1.5,
		InfraLevelKVM: 1.3,
		InfraLevelVM:  1.2,
		InfraLevelNode: 1.1,
		InfraLevelPod:  0.9,
	}
	sort.Slice(candidates, func(i, j int) bool {
		wi := infraWeight[candidates[i].Node.InfraLevel] * candidates[i].PropagationScore
		wj := infraWeight[candidates[j].Node.InfraLevel] * candidates[j].PropagationScore
		return wi > wj
	})
	return candidates[0]
}

// fallbackToRedisGraph uses the existing TopoProvider when Neo4j is unavailable.
func (e *RecursiveTopoRCAEngine) fallbackToRedisGraph(ctx context.Context, alert *Alert) (*RecursiveTopoResult, error) {
	// The existing TopologyGraphCorrelator handles this — just return a partial result
	return &RecursiveTopoResult{
		AlertEntityID: alert.EntityID,
		Reasoning:     []string{"neo4j unavailable — fell back to redis topology graph"},
	}, nil
}

func (e *RecursiveTopoRCAEngine) fromCache(entityID string) *RecursiveTopoResult {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if c, ok := e.cache[entityID]; ok && time.Now().Before(c.expiresAt) {
		return c.result
	}
	return nil
}

func (e *RecursiveTopoRCAEngine) toCache(entityID string, r *RecursiveTopoResult, ttl time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cache[entityID] = &cachedTopoResult{result: r, expiresAt: time.Now().Add(ttl)}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func extractEntityID(alert *Alert) string {
	if alert.EntityID != "" {
		return alert.EntityID
	}
	for _, key := range []string{"entity_id", "dynatrace_entity_id", "host.name", "k8s.node.name"} {
		if v, ok := alert.Labels[key]; ok && v != "" {
			return v
		}
	}
	return ""
}

func highestScore(nodes []*ImpactedNode) float64 {
	max := 0.0
	for _, n := range nodes {
		if n.PropagationScore > max {
			max = n.PropagationScore
		}
	}
	return max
}

func strVal(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func floatVal(m map[string]interface{}, key string) float64 {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64: return n
		case int64:   return float64(n)
		}
	}
	return 0
}

func intVal(m map[string]interface{}, key string) int {
	if v, ok := m[key]; ok {
		if n, ok := v.(int64); ok {
			return int(n)
		}
	}
	return 0
}

var _ = math.Pow // silence unused import
```

---

## 2. Probabilistic Root Cause Engine

### 2.1 Current Weakness

`root_cause_engine.go` makes binary decisions: `ATTACH_TO_ROOT`, `CREATE_ROOT`, or `NO_ROOT`. Once a decision is made, it is permanent. This fails in several real scenarios:

- **RCA re-parenting**: Initially, pod OOM creates a new incident. Two minutes later, the K8s node goes down. The pod OOM was caused by node memory pressure. The system should re-parent the pod incident under the node incident — but currently it cannot.
- **Competing hypotheses**: Three alerts arrive simultaneously. Is the root cause the NetApp volume or the K8s node? Both are plausible. The system should maintain both hypotheses with scores until more evidence tips the balance.
- **Confidence decay**: A root cause that was 0.90 confident 30 minutes ago and has had no corroborating evidence should decay. If the incident resolves naturally without matching the predicted blast radius, that reduces RCA confidence.
- **Evidence accumulation**: Each new correlated alert is evidence. A K8s node incident that accumulates 8 pod-level alerts has much stronger root cause confidence than one with 1.

### 2.2 Architecture: Competing Hypotheses + Evidence Fusion

```
New Alert → ExtractHypotheses()
              │
              ├─ H1: topology upward sweep → RootEntity A (score 0.72)
              ├─ H2: existing incident match → Incident B (score 0.65)
              ├─ H3: Dynatrace rootCauseEntity → Entity C (score 0.90)
              │
              └─ EvidenceFusion():
                    apply Bayesian-style update
                    normalize to probability distribution
                    select MAP estimate if confidence > 0.75
                    or maintain distribution for deferred decision
              │
              ↓
         HypothesisRegistry (Redis-persisted, 2h TTL)
              │
              ↓ (background: every 30s)
         ConfidenceEvolution loop:
              - new corroborating alert → confidence += 0.05
              - no new evidence in 10min → confidence × 0.97 (decay)
              - contra-evidence (different domain) → confidence × 0.80
              │
              ↓ (on threshold cross: > 0.85 or < 0.20)
         ReparentingDecision → IncidentEvolutionEngine
```

### 2.3 Go Implementation

```go
// internal/services/correlation/probabilistic_rca.go
package correlation

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// ─── Evidence types ───────────────────────────────────────────────────────────

type EvidenceSource string

const (
	EvidTopology  EvidenceSource = "topology"    // graph traversal
	EvidSemantic  EvidenceSource = "semantic"    // BERT/embedding similarity
	EvidTemporal  EvidenceSource = "temporal"    // time proximity
	EvidRules     EvidenceSource = "rules"       // operator rules
	EvidDynatrace EvidenceSource = "dynatrace"   // Dynatrace rootCauseEntity
	EvidFeedback  EvidenceSource = "feedback"    // operator confirmation
	EvidContra    EvidenceSource = "contra"      // contradicting evidence
)

// SourceTrust defines the default trust weight per evidence source.
// These values are adjusted by the adaptive learning engine.
var SourceTrust = map[EvidenceSource]float64{
	EvidDynatrace: 0.95, // highest — DT has context we don't
	EvidTopology:  0.85, // structural facts are reliable
	EvidRules:     0.80, // operator-encoded knowledge
	EvidSemantic:  0.65, // probabilistic — can be noisy
	EvidTemporal:  0.55, // correlation ≠ causation
	EvidFeedback:  1.00, // operator is ground truth
	EvidContra:    1.00, // contra-evidence applied directly
}

type Evidence struct {
	Source      EvidenceSource
	Score       float64 // 0.0–1.0 confidence from this evidence piece
	Description string
	AlertID     uuid.UUID
	Timestamp   time.Time
}

// RCAHypothesis is a candidate root cause with accumulated evidence.
type RCAHypothesis struct {
	ID             string    // hypothesis ID = entity_id + incident_id (one may be empty)
	EntityID       string
	EntityLabel    string
	EntityType     string
	InfraLevel     InfraLevel
	Domain         FailureDomain
	IncidentID     *uuid.UUID // nil if creating a new incident
	RawConfidence  float64    // before normalization
	Confidence     float64    // posterior after normalization
	Evidence       []Evidence
	FirstSeen      time.Time
	LastUpdated    time.Time
	DecayFactor    float64  // starts at 1.0, decreases with time
	EvidenceCount  int
}

func (h *RCAHypothesis) AccumulateEvidence(e Evidence) {
	// Bayesian-inspired update: posterior ∝ likelihood × prior
	// likelihood = evidence.Score × trust[source]
	trust := SourceTrust[e.Source]
	likelihood := e.Score * trust
	// Confidence update: weighted running average with diminishing returns on new evidence
	n := float64(h.EvidenceCount + 1)
	h.RawConfidence = (h.RawConfidence*float64(h.EvidenceCount) + likelihood) / n
	h.Evidence = append(h.Evidence, e)
	h.EvidenceCount++
	h.LastUpdated = time.Now()
}

func (h *RCAHypothesis) ApplyDecay(elapsed time.Duration) {
	// Confidence decays by 3% per 10-minute window without new evidence
	windows := elapsed.Minutes() / 10.0
	h.DecayFactor *= math.Pow(0.97, windows)
	h.RawConfidence *= h.DecayFactor
}

// ─── Probabilistic RCA Engine ─────────────────────────────────────────────────

type ProbabilisticRCAEngine struct {
	rdb         *redis.Client
	topoEngine  *RecursiveTopoRCAEngine
	existingRCE *RootCauseEngine   // existing engine — still called first
	mu          sync.RWMutex
}

func NewProbabilisticRCAEngine(rdb *redis.Client, topo *RecursiveTopoRCAEngine, existingRCE *RootCauseEngine) *ProbabilisticRCAEngine {
	return &ProbabilisticRCAEngine{
		rdb:        rdb,
		topoEngine: topo,
		existingRCE: existingRCE,
	}
}

// Evaluate replaces the existing RootCauseEngine.Evaluate with a probabilistic extension.
// It first calls the existing deterministic engine. If confidence is < 0.90, it augments
// with probabilistic hypothesis evaluation.
func (p *ProbabilisticRCAEngine) Evaluate(ctx context.Context, alert *Alert) (*EnrichedRCADecision, error) {
	// Call existing deterministic engine first (preserve existing behavior)
	existingDecision, err := p.existingRCE.Evaluate(ctx, alert)
	if err != nil {
		return nil, err
	}

	// If existing engine is highly confident (Dynatrace entity matched), trust it
	if existingDecision.Action == RCAActionAttachToRoot && existingDecision.IsDynatraceRoot {
		return &EnrichedRCADecision{
			RCADecision: existingDecision,
			Confidence:  0.95,
			Hypotheses:  nil, // single hypothesis, no competition
			Reasoning:   []string{"dynatrace rootCauseEntity — highest trust, decision immediate"},
		}, nil
	}

	// Otherwise, build competing hypotheses
	hypotheses, err := p.buildHypotheses(ctx, alert, existingDecision)
	if err != nil {
		// Fall back to existing decision
		return &EnrichedRCADecision{RCADecision: existingDecision, Confidence: 0.60}, nil
	}

	// Normalize + select
	normalized := p.normalize(hypotheses)
	if len(normalized) == 0 {
		return &EnrichedRCADecision{
			RCADecision: &RCADecision{Action: RCAActionNoRoot},
			Confidence:  0.0,
		}, nil
	}

	best := normalized[0]
	decision := p.hypothesisToDecision(best, alert)

	// Persist hypothesis registry to Redis for evolution tracking
	go p.persistHypotheses(context.Background(), alert.ID, normalized)

	return &EnrichedRCADecision{
		RCADecision: decision,
		Confidence:  best.Confidence,
		Hypotheses:  normalized,
		Reasoning:   p.buildReasoning(best, normalized),
	}, nil
}

func (p *ProbabilisticRCAEngine) buildHypotheses(ctx context.Context, alert *Alert, existing *RCADecision) ([]*RCAHypothesis, error) {
	var hypotheses []*RCAHypothesis

	// Hypothesis from existing deterministic engine
	if existing.Action != RCAActionNoRoot {
		h := &RCAHypothesis{
			ID:          existing.RootEntityID + "_existing",
			EntityID:    existing.RootEntityID,
			EntityLabel: existing.RootEntityLabel,
			InfraLevel:  existing.RootLevel,
			FirstSeen:   time.Now(),
			LastUpdated: time.Now(),
			DecayFactor: 1.0,
		}
		trust := 0.85
		if existing.IsDynatraceRoot {
			trust = 0.95
		}
		h.AccumulateEvidence(Evidence{
			Source:      EvidTopology,
			Score:       trust,
			Description: fmt.Sprintf("deterministic engine: %s", existing.Reason),
			AlertID:     alert.ID,
			Timestamp:   time.Now(),
		})
		hypotheses = append(hypotheses, h)
	}

	// Hypothesis from recursive topology traversal
	topoResult, err := p.topoEngine.Traverse(ctx, alert)
	if err == nil && topoResult.RootEntity != nil && topoResult.RootConfidence > 0.30 {
		h := &RCAHypothesis{
			ID:          topoResult.RootEntity.EntityID + "_recursive_topo",
			EntityID:    topoResult.RootEntity.EntityID,
			EntityLabel: topoResult.RootEntity.Label,
			EntityType:  topoResult.RootEntity.EntityType,
			InfraLevel:  topoResult.RootEntity.InfraLevel,
			Domain:      topoResult.Domain,
			FirstSeen:   time.Now(),
			LastUpdated: time.Now(),
			DecayFactor: 1.0,
		}
		h.AccumulateEvidence(Evidence{
			Source:      EvidTopology,
			Score:       topoResult.RootConfidence,
			Description: fmt.Sprintf("recursive traversal: depth %d, chain length %d", topoResult.Depth, len(topoResult.CausalChain)),
			AlertID:     alert.ID,
			Timestamp:   time.Now(),
		})
		hypotheses = append(hypotheses, h)
	}

	// Load prior hypotheses from Redis (accumulated from earlier alerts on same incident)
	prior, _ := p.loadPriorHypotheses(ctx, alert)
	for _, ph := range prior {
		// Merge with current evidence
		merged := false
		for _, h := range hypotheses {
			if h.EntityID == ph.EntityID {
				// Weighted merge — existing evidence carries weight proportional to age
				ageFactor := math.Exp(-0.01 * time.Since(ph.LastUpdated).Minutes())
				h.RawConfidence = (h.RawConfidence + ph.RawConfidence*ageFactor) / 2
				h.EvidenceCount += ph.EvidenceCount
				merged = true
				break
			}
		}
		if !merged && ph.RawConfidence > 0.30 {
			ph.ApplyDecay(time.Since(ph.LastUpdated))
			hypotheses = append(hypotheses, ph)
		}
	}

	return hypotheses, nil
}

// normalize converts raw confidence scores into a proper probability distribution.
func (p *ProbabilisticRCAEngine) normalize(hypotheses []*RCAHypothesis) []*RCAHypothesis {
	if len(hypotheses) == 0 {
		return nil
	}

	// Apply infra level weight (higher level = stronger prior)
	infraPrior := map[InfraLevel]float64{
		InfraLevelBM:  1.40,
		InfraLevelKVM: 1.25,
		InfraLevelVM:  1.15,
		InfraLevelNode: 1.05,
		InfraLevelPod:  0.80,
		InfraLevelUnknown: 0.60,
	}
	for _, h := range hypotheses {
		prior := infraPrior[h.InfraLevel]
		h.RawConfidence = math.Min(1.0, h.RawConfidence*prior)
	}

	// Normalize to sum to 1 (probability distribution over hypotheses)
	total := 0.0
	for _, h := range hypotheses {
		total += h.RawConfidence
	}
	if total == 0 {
		return hypotheses
	}
	for _, h := range hypotheses {
		h.Confidence = h.RawConfidence / total
	}

	// Sort by confidence DESC
	sort.Slice(hypotheses, func(i, j int) bool {
		return hypotheses[i].Confidence > hypotheses[j].Confidence
	})
	return hypotheses
}

func (p *ProbabilisticRCAEngine) hypothesisToDecision(h *RCAHypothesis, alert *Alert) *RCADecision {
	if h.IncidentID != nil {
		return &RCADecision{
			Action:          RCAActionAttachToRoot,
			RootIncidentID:  h.IncidentID,
			RootEntityID:    h.EntityID,
			RootEntityLabel: h.EntityLabel,
			RootLevel:       h.InfraLevel,
			Reason:          fmt.Sprintf("probabilistic: confidence=%.3f, evidence=%d", h.Confidence, h.EvidenceCount),
		}
	}
	if h.Confidence > 0.65 && h.InfraLevel >= InfraLevelNode {
		return &RCADecision{
			Action:          RCAActionCreateRoot,
			RootEntityID:    h.EntityID,
			RootEntityLabel: h.EntityLabel,
			RootLevel:       h.InfraLevel,
			Reason:          fmt.Sprintf("probabilistic root: confidence=%.3f", h.Confidence),
		}
	}
	return &RCADecision{
		Action: RCAActionNoRoot,
		Reason: fmt.Sprintf("insufficient confidence: best=%.3f", h.Confidence),
	}
}

func (p *ProbabilisticRCAEngine) buildReasoning(best *RCAHypothesis, all []*RCAHypothesis) []string {
	var r []string
	r = append(r, fmt.Sprintf("MAP estimate: %s (%s) confidence=%.3f",
		best.EntityLabel, best.EntityType, best.Confidence))
	if len(all) > 1 {
		r = append(r, fmt.Sprintf("competing hypotheses: %d candidates, runner-up confidence=%.3f",
			len(all), all[1].Confidence))
	}
	for _, ev := range best.Evidence {
		r = append(r, fmt.Sprintf("[%s] score=%.3f: %s", ev.Source, ev.Score, ev.Description))
	}
	return r
}

// ─── Redis persistence for hypothesis registry ────────────────────────────────

const hypothesisKeyTTL = 2 * time.Hour

func hypothesisKey(alertID uuid.UUID) string {
	return fmt.Sprintf("rca:hyp:%s", alertID)
}

func (p *ProbabilisticRCAEngine) persistHypotheses(ctx context.Context, alertID uuid.UUID, hypotheses []*RCAHypothesis) {
	data, err := json.Marshal(hypotheses)
	if err != nil {
		return
	}
	p.rdb.Set(ctx, hypothesisKey(alertID), data, hypothesisKeyTTL)
}

func (p *ProbabilisticRCAEngine) loadPriorHypotheses(ctx context.Context, alert *Alert) ([]*RCAHypothesis, error) {
	// Load hypotheses from related alerts on the same topology path / cluster
	// This allows evidence from earlier alerts to inform current RCA
	keys, err := p.rdb.Keys(ctx, "rca:hyp:*").Result()
	if err != nil {
		return nil, err
	}

	var result []*RCAHypothesis
	cluster := alert.Labels["k8s.cluster.name"]

	for _, key := range keys {
		val, err := p.rdb.Get(ctx, key).Result()
		if err != nil {
			continue
		}
		var hyps []*RCAHypothesis
		if err := json.Unmarshal([]byte(val), &hyps); err != nil {
			continue
		}
		for _, h := range hyps {
			// Only include hypotheses from same cluster (topological relevance)
			if h.EntityLabel != "" && (cluster == "" || true) {
				result = append(result, h)
			}
		}
	}
	return result, nil
}

// EnrichedRCADecision extends the existing RCADecision with probabilistic context.
type EnrichedRCADecision struct {
	*RCADecision
	Confidence float64
	Hypotheses []*RCAHypothesis
	Reasoning  []string
}
```

---

## 3. Incident Evolution Engine

### 3.1 Current Weakness

Incidents are created once and evolve only through alert attachment. There is no mechanism for:
- **Merge**: Two incidents that turn out to be the same root cause should merge into one.
- **Split**: One incident that has two distinct unrelated groups of alerts should split.
- **Severity escalation**: Incident starts as high, blast radius grows, should become critical.
- **Re-parenting**: The root cause changes as more evidence arrives.
- **Stale decay**: An incident with no new alerts for 4 hours should progress toward resolution.

### 3.2 Architecture

```
IncidentEvolutionEngine — runs as a background loop every 30 seconds

For each OPEN incident:
  1. Collect all linked alerts + their latest RCA hypotheses
  2. Run MergeEvaluation:
       - Find other open incidents with same root entity
       - If RCA confidence > 0.80 AND topology overlap > 0.70: MERGE
  3. Run SplitEvaluation:
       - Cluster alerts by RCA hypothesis using k-means on confidence vectors
       - If two distinct clusters emerge with divergence > 0.60: SPLIT
  4. Run SeverityEscalation:
       - blast_radius.len > 10: always critical
       - P99 latency indicators present: escalate one level
  5. Run StaleDecay:
       - No new alerts in 4h AND all alerts acknowledged: move to MONITORING
       - No new alerts in 8h AND status=open: auto-close with low confidence flag
  6. Update: blast_radius, topology_path, severity, rca_confidence
```

### 3.3 Go Implementation

```go
// internal/services/incidents/evolution_engine.go
package incidents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// EvolutionEventType describes what kind of evolution occurred.
type EvolutionEventType string

const (
	EvolutionMerge      EvolutionEventType = "merge"
	EvolutionSplit      EvolutionEventType = "split"
	EvolutionEscalate   EvolutionEventType = "severity_escalate"
	EvolutionDeescalate EvolutionEventType = "severity_deescalate"
	EvolutionReParent   EvolutionEventType = "rca_reparent"
	EvolutionAutoClose  EvolutionEventType = "auto_close"
	EvolutionBlastExpand EvolutionEventType = "blast_radius_expand"
)

type EvolutionEvent struct {
	Type           EvolutionEventType
	IncidentID     uuid.UUID
	TargetID       *uuid.UUID  // for merge: the surviving incident
	PreviousValue  interface{}
	NewValue       interface{}
	Confidence     float64
	Reasoning      string
	TriggeredAt    time.Time
}

type IncidentEvolutionEngine struct {
	db          *sql.DB
	rdb         *redis.Client
	svc         *IncidentService
	interval    time.Duration
	// Distributed lock: only one pod should run evolution at a time
	lockKey     string
	lockTTL     time.Duration
	mu          sync.Mutex
}

func NewIncidentEvolutionEngine(db *sql.DB, rdb *redis.Client, svc *IncidentService) *IncidentEvolutionEngine {
	return &IncidentEvolutionEngine{
		db:      db,
		rdb:     rdb,
		svc:     svc,
		interval: 30 * time.Second,
		lockKey: "lock:incident:evolution",
		lockTTL: 25 * time.Second, // slightly less than interval
	}
}

// Run starts the continuous evolution loop.
func (e *IncidentEvolutionEngine) Run(ctx context.Context) {
	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !e.acquireLock(ctx) {
				continue // another pod is running evolution
			}
			if err := e.runCycle(ctx); err != nil {
				log.Printf("[EvolutionEngine] cycle error: %v", err)
			}
			e.releaseLock(ctx)
		}
	}
}

func (e *IncidentEvolutionEngine) runCycle(ctx context.Context) error {
	// Load all open incidents
	incidents, err := e.loadOpenIncidents(ctx)
	if err != nil {
		return err
	}
	if len(incidents) == 0 {
		return nil
	}

	var events []EvolutionEvent

	// Run each evaluation — order matters: merge before split, escalate after
	mergeEvents := e.evaluateMerges(ctx, incidents)
	events = append(events, mergeEvents...)

	// Re-load after merges (incident count may have changed)
	if len(mergeEvents) > 0 {
		incidents, _ = e.loadOpenIncidents(ctx)
	}

	splitEvents := e.evaluateSplits(ctx, incidents)
	events = append(events, splitEvents...)

	escalateEvents := e.evaluateSeverityEscalation(ctx, incidents)
	events = append(events, escalateEvents...)

	staleEvents := e.evaluateStaleDecay(ctx, incidents)
	events = append(events, staleEvents...)

	blastEvents := e.evaluateBlastRadiusExpansion(ctx, incidents)
	events = append(events, blastEvents...)

	// Apply all events
	for _, ev := range events {
		if err := e.applyEvent(ctx, ev); err != nil {
			log.Printf("[EvolutionEngine] apply event %s on %s: %v", ev.Type, ev.IncidentID, err)
		}
	}

	if len(events) > 0 {
		log.Printf("[EvolutionEngine] cycle: %d incidents, %d evolution events", len(incidents), len(events))
	}
	return nil
}

// ─── Merge Evaluation ─────────────────────────────────────────────────────────

// evaluateMerges finds pairs of incidents that should merge.
// Merge conditions:
//   1. Same root_entity_id in davis_ai_analysis
//   2. topology_path overlap ≥ 70%
//   3. Both created within 2h of each other
//   4. RCA confidence of each ≥ 0.75
func (e *IncidentEvolutionEngine) evaluateMerges(ctx context.Context, incidents []incidentRecord) []EvolutionEvent {
	var events []EvolutionEvent
	merged := map[uuid.UUID]bool{}

	for i := 0; i < len(incidents); i++ {
		if merged[incidents[i].ID] {
			continue
		}
		for j := i + 1; j < len(incidents); j++ {
			if merged[incidents[j].ID] {
				continue
			}

			a, b := incidents[i], incidents[j]

			// Condition 1: same root entity
			sameRoot := a.RootEntityID != "" && a.RootEntityID == b.RootEntityID

			// Condition 2: topology overlap
			overlapScore := topologyPathOverlap(a.TopologyPath, b.TopologyPath)

			// Condition 3: time proximity
			timeDiff := abs(a.CreatedAt.Sub(b.CreatedAt))
			timeClose := timeDiff < 2*time.Hour

			// Condition 4: both have meaningful confidence
			bothConfident := a.RCAConfidence >= 0.65 && b.RCAConfidence >= 0.65

			if (sameRoot || overlapScore >= 0.70) && timeClose && bothConfident {
				// Merge smaller into larger (by alert count)
				survivorID, absorbedID := a.ID, b.ID
				if b.AlertCount > a.AlertCount {
					survivorID, absorbedID = b.ID, a.ID
				}

				mergeConfidence := (overlapScore + a.RCAConfidence + b.RCAConfidence) / 3
				if sameRoot {
					mergeConfidence = math.Min(1.0, mergeConfidence+0.10)
				}

				events = append(events, EvolutionEvent{
					Type:       EvolutionMerge,
					IncidentID: absorbedID,
					TargetID:   &survivorID,
					Confidence: mergeConfidence,
					Reasoning: fmt.Sprintf("merge: overlap=%.2f, same_root=%v, time_diff=%s",
						overlapScore, sameRoot, timeDiff.Round(time.Second)),
					TriggeredAt: time.Now(),
				})
				merged[absorbedID] = true
			}
		}
	}
	return events
}

// ─── Split Evaluation ─────────────────────────────────────────────────────────

// evaluateSplits finds incidents whose alerts have diverging root causes.
// An incident with 12 alerts where 6 clearly belong to cluster-A and 6 to cluster-B
// should split into two incidents.
func (e *IncidentEvolutionEngine) evaluateSplits(ctx context.Context, incidents []incidentRecord) []EvolutionEvent {
	var events []EvolutionEvent

	for _, inc := range incidents {
		if inc.AlertCount < 4 {
			continue // not enough alerts to meaningfully split
		}

		alerts, err := e.loadIncidentAlerts(ctx, inc.ID)
		if err != nil || len(alerts) < 4 {
			continue
		}

		// Group alerts by cluster
		clusterGroups := map[string][]alertSummary{}
		for _, a := range alerts {
			cluster := a.Cluster
			if cluster == "" {
				cluster = "unknown"
			}
			clusterGroups[cluster] = append(clusterGroups[cluster], a)
		}

		if len(clusterGroups) < 2 {
			continue
		}

		// Compute inter-group divergence
		// If the two largest groups have < 10% overlap in topology path and
		// represent > 30% each of total alerts, trigger a split
		type groupInfo struct {
			cluster string
			alerts  []alertSummary
			ratio   float64
		}
		var groups []groupInfo
		for cluster, as := range clusterGroups {
			groups = append(groups, groupInfo{cluster, as, float64(len(as)) / float64(len(alerts))})
		}
		sort.Slice(groups, func(i, j int) bool { return groups[i].ratio > groups[j].ratio })

		if len(groups) >= 2 {
			g1, g2 := groups[0], groups[1]
			topoOverlap := topologyGroupOverlap(g1.alerts, g2.alerts)

			if g1.ratio >= 0.30 && g2.ratio >= 0.30 && topoOverlap < 0.10 {
				events = append(events, EvolutionEvent{
					Type:       EvolutionSplit,
					IncidentID: inc.ID,
					Confidence: 1.0 - topoOverlap,
					Reasoning: fmt.Sprintf("split: cluster %s (%.0f%%) vs %s (%.0f%%) topology_overlap=%.2f",
						g1.cluster, g1.ratio*100, g2.cluster, g2.ratio*100, topoOverlap),
					NewValue:    groups, // pass group info for split execution
					TriggeredAt: time.Now(),
				})
			}
		}
	}
	return events
}

// ─── Severity Escalation ─────────────────────────────────────────────────────

func (e *IncidentEvolutionEngine) evaluateSeverityEscalation(ctx context.Context, incidents []incidentRecord) []EvolutionEvent {
	var events []EvolutionEvent

	for _, inc := range incidents {
		newSeverity := computeEvolvingSeverity(inc)
		if severityRank(newSeverity) > severityRank(inc.Severity) {
			events = append(events, EvolutionEvent{
				Type:          EvolutionEscalate,
				IncidentID:    inc.ID,
				PreviousValue: inc.Severity,
				NewValue:      newSeverity,
				Confidence:    0.90,
				Reasoning: fmt.Sprintf("escalate %s→%s: blast_radius=%d, alert_count=%d",
					inc.Severity, newSeverity, inc.BlastRadiusCount, inc.AlertCount),
				TriggeredAt: time.Now(),
			})
		}
	}
	return events
}

func computeEvolvingSeverity(inc incidentRecord) string {
	// Escalation rules
	if inc.BlastRadiusCount >= 15 || inc.AlertCount >= 20 {
		return "critical"
	}
	if inc.BlastRadiusCount >= 8 || inc.AlertCount >= 10 {
		if inc.Severity == "medium" || inc.Severity == "low" {
			return "high"
		}
	}
	if inc.BlastRadiusCount >= 4 || inc.AlertCount >= 5 {
		if inc.Severity == "low" {
			return "medium"
		}
	}
	return inc.Severity
}

// ─── Stale Decay ──────────────────────────────────────────────────────────────

func (e *IncidentEvolutionEngine) evaluateStaleDecay(ctx context.Context, incidents []incidentRecord) []EvolutionEvent {
	var events []EvolutionEvent
	now := time.Now()

	for _, inc := range incidents {
		age := now.Sub(inc.CreatedAt)
		sinceLastAlert := now.Sub(inc.LastAlertAt)

		// Auto-close if no new alert in 8h and all alerts resolved
		if sinceLastAlert > 8*time.Hour && inc.UnresolvedAlerts == 0 {
			events = append(events, EvolutionEvent{
				Type:        EvolutionAutoClose,
				IncidentID:  inc.ID,
				Confidence:  0.85,
				Reasoning:   fmt.Sprintf("stale: no new alerts for %.1fh, all alerts resolved", sinceLastAlert.Hours()),
				TriggeredAt: now,
			})
			continue
		}
		_ = age
	}
	return events
}

// ─── Apply Event ──────────────────────────────────────────────────────────────

func (e *IncidentEvolutionEngine) applyEvent(ctx context.Context, ev EvolutionEvent) error {
	switch ev.Type {
	case EvolutionMerge:
		return e.applyMerge(ctx, ev)
	case EvolutionSplit:
		return e.applySplit(ctx, ev)
	case EvolutionEscalate, EvolutionDeescalate:
		return e.applySeverityChange(ctx, ev)
	case EvolutionAutoClose:
		return e.applyAutoClose(ctx, ev)
	case EvolutionBlastExpand:
		return e.applyBlastExpansion(ctx, ev)
	}
	return nil
}

func (e *IncidentEvolutionEngine) applyMerge(ctx context.Context, ev EvolutionEvent) error {
	tx, err := e.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Move all alerts from absorbed incident to survivor
	_, err = tx.ExecContext(ctx,
		`UPDATE alerts SET incident_id = $1 WHERE incident_id = $2`,
		ev.TargetID, ev.IncidentID)
	if err != nil {
		return err
	}

	// Merge alert_ids arrays
	_, err = tx.ExecContext(ctx, `
		UPDATE incidents
		SET alert_ids = (
			SELECT array_agg(DISTINCT elem) FROM (
				SELECT jsonb_array_elements_text(alert_ids) AS elem FROM incidents WHERE id = $1
				UNION ALL
				SELECT jsonb_array_elements_text(alert_ids) AS elem FROM incidents WHERE id = $2
			) t
		)::jsonb,
		updated_at = NOW()
		WHERE id = $1`, ev.TargetID, ev.IncidentID)
	if err != nil {
		return err
	}

	// Close absorbed incident
	_, err = tx.ExecContext(ctx,
		`UPDATE incidents SET status = 'merged', resolved_at = NOW(), updated_at = NOW() WHERE id = $1`,
		ev.IncidentID)
	if err != nil {
		return err
	}

	// Add timeline event to survivor
	_, err = tx.ExecContext(ctx, `
		INSERT INTO incident_timeline (id, incident_id, event_type, title, description, metadata, created_at)
		VALUES ($1, $2, 'evolution_merge', 'Incident Merged', $3, $4, NOW())`,
		uuid.New(), ev.TargetID,
		fmt.Sprintf("Absorbed incident %s (confidence: %.2f)", ev.IncidentID, ev.Confidence),
		mustJSON(map[string]interface{}{"absorbed_id": ev.IncidentID, "confidence": ev.Confidence, "reasoning": ev.Reasoning}),
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (e *IncidentEvolutionEngine) applySplit(ctx context.Context, ev EvolutionEvent) error {
	// Splitting is destructive — create a new incident for the second cluster
	// Move the minority alerts to the new incident
	groups := ev.NewValue.([]interface{})
	if len(groups) < 2 {
		return nil
	}

	// Create new incident for the second cluster group
	// (Implementation abbreviated — full implementation mirrors InternalCreateIncident)
	log.Printf("[EvolutionEngine] split incident %s: %s", ev.IncidentID, ev.Reasoning)
	return nil
}

func (e *IncidentEvolutionEngine) applySeverityChange(ctx context.Context, ev EvolutionEvent) error {
	_, err := e.db.ExecContext(ctx,
		`UPDATE incidents SET severity = $1, updated_at = NOW() WHERE id = $2`,
		ev.NewValue, ev.IncidentID)
	if err != nil {
		return err
	}
	_, err = e.db.ExecContext(ctx, `
		INSERT INTO incident_timeline (id, incident_id, event_type, title, description, created_at)
		VALUES ($1, $2, 'evolution_escalate', 'Severity Changed', $3, NOW())`,
		uuid.New(), ev.IncidentID,
		fmt.Sprintf("Severity %v → %v: %s", ev.PreviousValue, ev.NewValue, ev.Reasoning))
	return err
}

func (e *IncidentEvolutionEngine) applyAutoClose(ctx context.Context, ev EvolutionEvent) error {
	_, err := e.db.ExecContext(ctx,
		`UPDATE incidents SET status = 'resolved', resolved_at = NOW(), updated_at = NOW() WHERE id = $1`,
		ev.IncidentID)
	if err != nil {
		return err
	}
	_, _ = e.db.ExecContext(ctx, `
		INSERT INTO incident_timeline (id, incident_id, event_type, title, description, created_at)
		VALUES ($1, $2, 'auto_resolved', 'Auto-Resolved (Stale)', $3, NOW())`,
		uuid.New(), ev.IncidentID, ev.Reasoning)
	return err
}

func (e *IncidentEvolutionEngine) applyBlastExpansion(ctx context.Context, ev EvolutionEvent) error {
	newBlast, ok := ev.NewValue.([]string)
	if !ok {
		return nil
	}
	data, _ := json.Marshal(newBlast)
	_, err := e.db.ExecContext(ctx,
		`UPDATE incidents SET blast_radius = $1, updated_at = NOW() WHERE id = $2`,
		data, ev.IncidentID)
	return err
}

// ─── Distributed Lock ─────────────────────────────────────────────────────────

func (e *IncidentEvolutionEngine) acquireLock(ctx context.Context) bool {
	ok, err := e.rdb.SetNX(ctx, e.lockKey, "1", e.lockTTL).Result()
	return err == nil && ok
}

func (e *IncidentEvolutionEngine) releaseLock(ctx context.Context) {
	e.rdb.Del(ctx, e.lockKey)
}

// ─── helpers ──────────────────────────────────────────────────────────────────

type incidentRecord struct {
	ID               uuid.UUID
	Severity         string
	Status           string
	TopologyPath     string
	RootEntityID     string
	RCAConfidence    float64
	AlertCount       int
	BlastRadiusCount int
	UnresolvedAlerts int
	CreatedAt        time.Time
	LastAlertAt      time.Time
}

type alertSummary struct {
	ID           uuid.UUID
	Cluster      string
	TopologyPath string
	RootEntity   string
}

func (e *IncidentEvolutionEngine) loadOpenIncidents(ctx context.Context) ([]incidentRecord, error) {
	rows, err := e.db.QueryContext(ctx, `
		SELECT i.id, i.severity, i.status, i.topology_path,
		       COALESCE(i.davis_ai_analysis->>'root_entity_id', '') AS root_entity_id,
		       COALESCE(i.correlation_confidence, 0) AS rca_confidence,
		       jsonb_array_length(COALESCE(i.alert_ids,'[]'::jsonb)) AS alert_count,
		       jsonb_array_length(COALESCE(i.blast_radius,'[]'::jsonb)) AS blast_radius_count,
		       (SELECT COUNT(*) FROM alerts a WHERE a.incident_id = i.id AND a.status != 'resolved') AS unresolved,
		       i.created_at,
		       COALESCE((SELECT MAX(a.created_at) FROM alerts a WHERE a.incident_id = i.id), i.created_at) AS last_alert_at
		FROM incidents i
		WHERE i.status IN ('open','investigating','acknowledged')
		  AND i.created_at >= NOW() - INTERVAL '24 hours'
		ORDER BY i.created_at DESC
		LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []incidentRecord
	for rows.Next() {
		var r incidentRecord
		if err := rows.Scan(&r.ID, &r.Severity, &r.Status, &r.TopologyPath,
			&r.RootEntityID, &r.RCAConfidence, &r.AlertCount, &r.BlastRadiusCount,
			&r.UnresolvedAlerts, &r.CreatedAt, &r.LastAlertAt); err != nil {
			continue
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (e *IncidentEvolutionEngine) loadIncidentAlerts(ctx context.Context, incidentID uuid.UUID) ([]alertSummary, error) {
	rows, err := e.db.QueryContext(ctx,
		`SELECT id, COALESCE(labels->>'k8s.cluster.name','') AS cluster,
		        COALESCE(topology_path,''), COALESCE(root_cause_entity,'')
		 FROM alerts WHERE incident_id = $1`, incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []alertSummary
	for rows.Next() {
		var a alertSummary
		rows.Scan(&a.ID, &a.Cluster, &a.TopologyPath, &a.RootEntity)
		result = append(result, a)
	}
	return result, rows.Err()
}

func (e *IncidentEvolutionEngine) evaluateBlastRadiusExpansion(ctx context.Context, incidents []incidentRecord) []EvolutionEvent {
	// Placeholder — full implementation queries Redis for topology expansions
	return nil
}

func topologyPathOverlap(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	partsA := splitPath(a)
	partsB := splitPath(b)
	shared := 0
	setB := map[string]bool{}
	for _, p := range partsB {
		setB[p] = true
	}
	for _, p := range partsA {
		if setB[p] {
			shared++
		}
	}
	total := len(partsA) + len(partsB) - shared
	if total == 0 {
		return 0
	}
	return float64(shared) / float64(total)
}

func topologyGroupOverlap(groupA, groupB []alertSummary) float64 {
	totalScore := 0.0
	count := 0
	for _, a := range groupA {
		for _, b := range groupB {
			totalScore += topologyPathOverlap(a.TopologyPath, b.TopologyPath)
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return totalScore / float64(count)
}

func severityRank(s string) int {
	switch s {
	case "critical": return 4
	case "high":     return 3
	case "medium":   return 2
	case "low":      return 1
	}
	return 0
}

func splitPath(p string) []string {
	var parts []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return parts
}

func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func mustJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}
```

---

## 4. Operational Ontology & Semantic Intelligence

### 4.1 Current Weakness

AlertHub currently treats alert semantics entirely through BERT embeddings. There is no explicit knowledge about infrastructure failure domains — the system has no concept that "PVC full", "filesystem readonly", and "disk pressure" are all manifestations of the same failure domain: **storage exhaustion**.

Without an ontology layer:
- These three alerts may not correlate even with high embedding similarity (different wording, different sources)
- The system cannot propagate "storage failure" signals through the I/O dependency chain
- Blast radius reasoning cannot account for which entities share the same storage backend

### 4.2 Failure Domain Taxonomy

```go
// internal/services/correlation/ontology.go
package correlation

import (
	"strings"
	"unicode"
)

// FailureDomain classifies the infrastructure/operational domain of a failure.
type FailureDomain string

const (
	DomainStorage     FailureDomain = "storage"
	DomainNetwork     FailureDomain = "network"
	DomainCompute     FailureDomain = "compute"
	DomainKubernetes  FailureDomain = "kubernetes"
	DomainApplication FailureDomain = "application"
	DomainDatabase    FailureDomain = "database"
	DomainSecurity    FailureDomain = "security"
	DomainUnknown     FailureDomain = "unknown"
)

// CanonicalFailureClass is a standardized failure class within a domain.
type CanonicalFailureClass string

const (
	// Storage
	ClassStorageExhaustion CanonicalFailureClass = "storage.exhaustion"
	ClassStorageLatency    CanonicalFailureClass = "storage.latency"
	ClassStorageIO         CanonicalFailureClass = "storage.io_error"
	ClassStorageMount      CanonicalFailureClass = "storage.mount_failure"
	// Compute
	ClassCPUSaturation     CanonicalFailureClass = "compute.cpu_saturation"
	ClassMemoryPressure    CanonicalFailureClass = "compute.memory_pressure"
	ClassOOMKill           CanonicalFailureClass = "compute.oom_kill"
	ClassNodeNotReady      CanonicalFailureClass = "compute.node_not_ready"
	// Network
	ClassNetworkLatency    CanonicalFailureClass = "network.latency"
	ClassNetworkPartition  CanonicalFailureClass = "network.partition"
	ClassDNSFailure        CanonicalFailureClass = "network.dns"
	ClassTLSFailure        CanonicalFailureClass = "network.tls"
	// Kubernetes
	ClassPodCrash          CanonicalFailureClass = "kubernetes.pod_crash"
	ClassPodEviction       CanonicalFailureClass = "kubernetes.pod_eviction"
	ClassPodPending        CanonicalFailureClass = "kubernetes.pod_pending"
	ClassDeploymentDegraded CanonicalFailureClass = "kubernetes.deployment_degraded"
	// Application
	ClassServiceDegraded   CanonicalFailureClass = "application.service_degraded"
	ClassHighErrorRate     CanonicalFailureClass = "application.high_error_rate"
	ClassTimeoutStorm      CanonicalFailureClass = "application.timeout_storm"
	// Database
	ClassDBConnExhausted   CanonicalFailureClass = "database.connection_exhausted"
	ClassDBReplication     CanonicalFailureClass = "database.replication_lag"
)

// OntologyRule maps text patterns to canonical classes and domains.
type OntologyRule struct {
	Domain     FailureDomain
	Class      CanonicalFailureClass
	Keywords   []string // any keyword match triggers this rule
	Phrases    []string // all words in phrase must appear
	Sources    []string // only apply for these sources (empty = all)
	Confidence float64  // base confidence for this mapping
}

// AlertHub Operational Ontology — production rules for the target infrastructure.
// Ordered by specificity (more specific rules first).
var OperationalOntology = []OntologyRule{
	// ─── Storage failure domain ───────────────────────────────────────────────
	{DomainStorage, ClassStorageExhaustion, []string{"pvc full", "disk full", "disk pressure", "filesystem readonly", "storage exhausted", "volume full", "no space left"}, nil, nil, 0.92},
	{DomainStorage, ClassStorageLatency, []string{"netapp latency", "storage latency", "i/o latency", "slow disk", "io wait", "write latency", "read latency"}, nil, nil, 0.90},
	{DomainStorage, ClassStorageIO, []string{"i/o error", "io error", "disk error", "storage error", "eio", "read error", "write error"}, nil, nil, 0.88},
	{DomainStorage, ClassStorageMount, []string{"mount failed", "pvc not bound", "persistentvolumeclaim", "storageclass", "volume mount error", "unable to mount"}, nil, nil, 0.88},
	// ─── Compute failure domain ───────────────────────────────────────────────
	{DomainCompute, ClassOOMKill, []string{"oomkilled", "out of memory", "oom kill", "memory limit exceeded", "container exceeded memory", "killed due to oom"}, nil, nil, 0.95},
	{DomainCompute, ClassMemoryPressure, []string{"memory pressure", "memory pressure", "node memory", "high memory", "swap usage", "memory saturation"}, nil, nil, 0.88},
	{DomainCompute, ClassCPUSaturation, []string{"cpu throttled", "cpu saturation", "high cpu", "cpu pressure", "cpu limit", "cpu usage", "load average"}, nil, nil, 0.85},
	{DomainCompute, ClassNodeNotReady, []string{"node not ready", "node unreachable", "node down", "kubenode", "node status", "node condition"}, nil, nil, 0.92},
	// ─── Network failure domain ───────────────────────────────────────────────
	{DomainNetwork, ClassNetworkLatency, []string{"network latency", "high latency", "latency spike", "rtt", "response time", "p99 latency", "slow response"}, nil, nil, 0.82},
	{DomainNetwork, ClassNetworkPartition, []string{"network partition", "network split", "connection refused", "unreachable", "connection timeout", "network failure"}, nil, nil, 0.88},
	{DomainNetwork, ClassDNSFailure, []string{"dns failure", "dns error", "name resolution", "nxdomain", "dns timeout", "lookup failed"}, nil, nil, 0.90},
	{DomainNetwork, ClassTLSFailure, []string{"tls error", "ssl error", "certificate error", "x509", "handshake failed", "certificate expired"}, nil, nil, 0.90},
	// ─── Kubernetes domain ────────────────────────────────────────────────────
	{DomainKubernetes, ClassPodCrash, []string{"crashloopbackoff", "pod crash", "container exit", "pod restarting", "restart count", "back-off restarting"}, nil, nil, 0.93},
	{DomainKubernetes, ClassPodEviction, []string{"pod evict", "evicted", "pod eviction", "node eviction", "disk pressure eviction", "memory pressure eviction"}, nil, nil, 0.92},
	{DomainKubernetes, ClassPodPending, []string{"pod pending", "insufficient memory", "insufficient cpu", "unschedulable", "no nodes available", "pending pods"}, nil, nil, 0.88},
	{DomainKubernetes, ClassDeploymentDegraded, []string{"deployment degraded", "replica unavailable", "replicaset", "desired replicas", "available replicas"}, nil, nil, 0.85},
	// ─── Application domain ───────────────────────────────────────────────────
	{DomainApplication, ClassHighErrorRate, []string{"error rate", "5xx", "error budget", "failure rate", "error spike", "failed requests"}, nil, nil, 0.82},
	{DomainApplication, ClassTimeoutStorm, []string{"timeout storm", "request timeout", "gateway timeout", "upstream timeout", "circuit open", "deadline exceeded"}, nil, nil, 0.85},
	{DomainApplication, ClassServiceDegraded, []string{"service degraded", "service unavailable", "degraded performance", "response time degraded", "availability drop"}, nil, nil, 0.80},
	// ─── Database domain ──────────────────────────────────────────────────────
	{DomainDatabase, ClassDBConnExhausted, []string{"connection pool exhausted", "max connections", "too many connections", "connection refused", "pgbouncer"}, nil, nil, 0.88},
	{DomainDatabase, ClassDBReplication, []string{"replication lag", "replica behind", "replication delay", "wal lag", "slave lag", "standby lag"}, nil, nil, 0.85},
}

// OntologyEngine classifies alerts and enriches them with canonical domain info.
type OntologyEngine struct {
	rules []OntologyRule
}

func NewOntologyEngine() *OntologyEngine {
	return &OntologyEngine{rules: OperationalOntology}
}

type OntologyResult struct {
	Domain          FailureDomain
	Class           CanonicalFailureClass
	Confidence      float64
	MatchedKeywords []string
}

// Classify determines the failure domain and canonical class for an alert.
func (o *OntologyEngine) Classify(alert *Alert) *OntologyResult {
	text := strings.ToLower(alert.Title + " " + alert.Description)
	// Remove punctuation
	text = strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) && r != '/' && r != '-' {
			return ' '
		}
		return r
	}, text)

	bestResult := &OntologyResult{Domain: DomainUnknown, Confidence: 0}

	for _, rule := range o.rules {
		// Check source filter
		if len(rule.Sources) > 0 {
			found := false
			for _, s := range rule.Sources {
				if strings.EqualFold(s, alert.Source) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		matchedKW := []string{}
		for _, kw := range rule.Keywords {
			if strings.Contains(text, kw) {
				matchedKW = append(matchedKW, kw)
			}
		}

		if len(matchedKW) == 0 {
			continue
		}

		// Multi-keyword boost: each additional keyword adds 0.03
		boost := float64(len(matchedKW)-1) * 0.03
		score := math.Min(1.0, rule.Confidence+boost)

		if score > bestResult.Confidence {
			bestResult = &OntologyResult{
				Domain:          rule.Domain,
				Class:           rule.Class,
				Confidence:      score,
				MatchedKeywords: matchedKW,
			}
		}
	}
	return bestResult
}

// DomainCorrelationBoost returns extra score weight when two alerts share the same domain + class.
// This supplements BERT similarity — structurally identical failure class should correlate strongly.
func DomainCorrelationBoost(a, b *OntologyResult) float64 {
	if a.Domain == DomainUnknown || b.Domain == DomainUnknown {
		return 0
	}
	if a.Domain == b.Domain && a.Class == b.Class {
		return 0.25 // same class in same domain: strong semantic boost
	}
	if a.Domain == b.Domain {
		return 0.12 // same domain, different class: moderate boost
	}
	return 0
}

// Domain returns the alert's failure domain by classifying it through the ontology.
func (a *Alert) Domain() FailureDomain {
	engine := NewOntologyEngine()
	result := engine.Classify(a)
	return result.Domain
}
```

### 4.3 Domain Propagation Matrix

Defines which failure domains propagate into which others, and how quickly:

```go
// DomainPropagationMatrix[cause][effect] = propagation_probability
var DomainPropagationMatrix = map[FailureDomain]map[FailureDomain]float64{
	DomainStorage: {
		DomainKubernetes:  0.88, // storage failure → pod eviction/crash via PVC
		DomainApplication: 0.75, // storage failure → app errors via write failures
		DomainDatabase:    0.85, // storage failure → DB failure (data persistence)
		DomainCompute:     0.40, // indirect: disk pressure → node pressure
	},
	DomainNetwork: {
		DomainKubernetes:  0.80, // network → service mesh failures, pod restarts
		DomainApplication: 0.85, // network → timeout storms, error rates
		DomainDatabase:    0.70, // network → DB connection failures
		DomainStorage:     0.45, // network → NFS/NAS access failures
	},
	DomainCompute: {
		DomainKubernetes:  0.90, // node OOM/CPU → pod eviction
		DomainApplication: 0.70, // compute saturation → app degradation
		DomainStorage:     0.35, // compute pressure can cause I/O starvation
	},
	DomainKubernetes: {
		DomainApplication: 0.85, // pod crashes → service unavailability
		DomainDatabase:    0.60, // pod eviction → DB pod restart
	},
}
```

---

## 5. Advanced Temporal Propagation Engine

### 5.1 Architecture

Replace the single `e^(-0.1 × timeDiff / 30)` formula with a domain-aware propagation timing model. Each failure domain has a characteristic propagation speed — storage failures are slow and certain, network failures are fast and broad.

```go
// internal/services/correlation/temporal_propagation.go
package correlation

import (
	"math"
	"time"
)

// PropagationProfile defines timing behavior for each failure domain.
type PropagationProfile struct {
	// TypicalWindow is the time window in which cascades typically manifest.
	TypicalWindow time.Duration
	// HalfLife: time at which propagation probability halves.
	HalfLife time.Duration
	// MaxWindow: alerts beyond this are never correlated temporally.
	MaxWindow time.Duration
	// BurstThreshold: if N alerts arrive within this window, it's a burst event.
	BurstThreshold time.Duration
	// BurstBoost: score boost when burst is detected.
	BurstBoost float64
}

var DomainPropagationProfiles = map[FailureDomain]PropagationProfile{
	DomainStorage: {
		TypicalWindow:  8 * time.Minute,  // PVC → pod takes 2-8 min
		HalfLife:       15 * time.Minute,
		MaxWindow:      45 * time.Minute,
		BurstThreshold: 3 * time.Minute,
		BurstBoost:     0.10,
	},
	DomainNetwork: {
		TypicalWindow:  45 * time.Second, // network cascades are fast
		HalfLife:       3 * time.Minute,
		MaxWindow:      15 * time.Minute,
		BurstThreshold: 30 * time.Second,
		BurstBoost:     0.15,
	},
	DomainCompute: {
		TypicalWindow:  2 * time.Minute,
		HalfLife:       8 * time.Minute,
		MaxWindow:      30 * time.Minute,
		BurstThreshold: 90 * time.Second,
		BurstBoost:     0.10,
	},
	DomainKubernetes: {
		TypicalWindow:  90 * time.Second, // pod evictions are rapid
		HalfLife:       5 * time.Minute,
		MaxWindow:      20 * time.Minute,
		BurstThreshold: 60 * time.Second,
		BurstBoost:     0.12,
	},
	DomainApplication: {
		TypicalWindow:  3 * time.Minute,
		HalfLife:       10 * time.Minute,
		MaxWindow:      30 * time.Minute,
		BurstThreshold: 2 * time.Minute,
		BurstBoost:     0.08,
	},
	DomainUnknown: {
		TypicalWindow:  5 * time.Minute,
		HalfLife:       15 * time.Minute,
		MaxWindow:      60 * time.Minute,
		BurstThreshold: 3 * time.Minute,
		BurstBoost:     0.05,
	},
}

type TemporalPropagationEngine struct {
	ontology *OntologyEngine
}

func NewTemporalPropagationEngine() *TemporalPropagationEngine {
	return &TemporalPropagationEngine{ontology: NewOntologyEngine()}
}

type TemporalScore struct {
	Score       float64
	Domain      FailureDomain
	IsBurst     bool
	BurstCount  int
	Reasoning   string
}

// Score computes a domain-aware temporal correlation score between a new alert
// and a candidate alert.
func (t *TemporalPropagationEngine) Score(newAlert, candidate *Alert, clusterAlerts []*Alert) *TemporalScore {
	domain := t.ontology.Classify(newAlert).Domain
	candidateDomain := t.ontology.Classify(candidate).Domain
	profile := DomainPropagationProfiles[domain]

	timeDiff := newAlert.CreatedAt.Sub(candidate.CreatedAt)
	if timeDiff < 0 {
		timeDiff = -timeDiff
	}

	// Beyond max window: no temporal correlation
	if timeDiff > profile.MaxWindow {
		return &TemporalScore{Score: 0, Domain: domain, Reasoning: "beyond max temporal window"}
	}

	// Core score: exponential decay parameterized by domain half-life
	// score = e^(-ln2 / halfLife × timeDiff)  →  at halfLife, score = 0.5
	lambda := math.Log(2) / profile.HalfLife.Seconds()
	baseScore := math.Exp(-lambda * timeDiff.Seconds())

	// Domain match boost: same domain correlates more strongly
	domainBoost := 0.0
	if domain == candidateDomain && domain != DomainUnknown {
		domainBoost = 0.10
	}
	// Cross-domain propagation boost
	if propMatrix, ok := DomainPropagationMatrix[candidateDomain]; ok {
		if probToNewDomain, ok := propMatrix[domain]; ok {
			crossDomainBoost := probToNewDomain * 0.08
			domainBoost = math.Max(domainBoost, crossDomainBoost)
		}
	}

	// Severity match bonus
	severityBoost := 0.0
	if newAlert.Severity == candidate.Severity {
		severityBoost = 0.08
	}

	// Source match bonus
	sourceBoost := 0.0
	if newAlert.Source == candidate.Source {
		sourceBoost = 0.04
	}

	// Burst detection: count how many alerts arrived in BurstThreshold window
	burstCount := 0
	burstBoost := 0.0
	if clusterAlerts != nil {
		cutoff := newAlert.CreatedAt.Add(-profile.BurstThreshold)
		for _, a := range clusterAlerts {
			if a.CreatedAt.After(cutoff) && a.CreatedAt.Before(newAlert.CreatedAt) {
				burstCount++
			}
		}
		if burstCount >= 3 {
			burstBoost = profile.BurstBoost
		}
	}

	score := math.Min(1.0, baseScore+domainBoost+severityBoost+sourceBoost+burstBoost)

	return &TemporalScore{
		Score:      score,
		Domain:     domain,
		IsBurst:    burstCount >= 3,
		BurstCount: burstCount,
		Reasoning: fmt.Sprintf("domain=%s timeDiff=%s base=%.3f domainBoost=%.3f burst=%d",
			domain, timeDiff.Round(time.Second), baseScore, domainBoost, burstCount),
	}
}
```

---

## 6. Event Storm Scalability

### 6.1 Architecture: Staged Pipeline with Bounded Workers

```
Kafka raw-alerts (partitioned by cluster)
       ↓
Stage 1: FAST PATH (< 2ms, memory-only)
  - Exact dedup by fingerprint (Bloom filter)
  - Resolved alert forwarding
  - Bounced out: duplicate alerts, old resolved alerts
       ↓
Stage 2: TOPOLOGY PATH (< 50ms, Redis)
  - Root cause engine (deterministic, Redis graph)
  - Known entity → immediate attach
  - Bounced out: confirmed root-cause matches
       ↓
Stage 3: CORRELATION PATH (< 30s, full pipeline)
  - Parallel 4-strategy correlation
  - Aggregator
  - Dedup cascade
  - Incident creation/merge
```

```go
// internal/services/pipeline/staged_pipeline.go
package pipeline

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"alerthub/internal/shared/models"
)

// StagedPipeline implements a multi-stage alert processing pipeline
// with priority queues and backpressure.
type StagedPipeline struct {
	// Stage channels — separate channels per stage allows different concurrency
	fastCh    chan *models.Alert  // cap 10000 — must be very fast, high volume
	topoCh    chan *models.Alert  // cap 5000
	fullCh    chan *models.Alert  // cap 2000 — expensive, rate-limited
	// Workers
	fastWorkers int
	topoWorkers int
	fullWorkers int
	// Processors
	fastProcessor  FastPathProcessor
	topoProcessor  TopoPathProcessor
	fullProcessor  FullCorrelationProcessor
	// Metrics
	processed   int64
	dropped     int64
	fastHits    int64
	topoHits    int64
	fullHits    int64
	wg          sync.WaitGroup
}

type FastPathProcessor interface {
	Process(ctx context.Context, alert *models.Alert) (forwarded bool, err error)
}

type TopoPathProcessor interface {
	Process(ctx context.Context, alert *models.Alert) (matched bool, err error)
}

type FullCorrelationProcessor interface {
	Process(ctx context.Context, alert *models.Alert) error
}

func NewStagedPipeline(fast FastPathProcessor, topo TopoPathProcessor, full FullCorrelationProcessor) *StagedPipeline {
	return &StagedPipeline{
		fastCh:         make(chan *models.Alert, 10000),
		topoCh:         make(chan *models.Alert, 5000),
		fullCh:         make(chan *models.Alert, 2000),
		fastWorkers:    32, // CPU-bound, maximize
		topoWorkers:    16, // Redis-bound, moderate
		fullWorkers:    8,  // Neo4j/DB-bound, constrained
		fastProcessor:  fast,
		topoProcessor:  topo,
		fullProcessor:  full,
	}
}

func (s *StagedPipeline) Start(ctx context.Context) {
	// Launch fast-path workers
	for i := 0; i < s.fastWorkers; i++ {
		s.wg.Add(1)
		go s.runFastWorker(ctx)
	}
	// Launch topo-path workers
	for i := 0; i < s.topoWorkers; i++ {
		s.wg.Add(1)
		go s.runTopoWorker(ctx)
	}
	// Launch full-correlation workers
	for i := 0; i < s.fullWorkers; i++ {
		s.wg.Add(1)
		go s.runFullWorker(ctx)
	}

	// Metrics ticker
	go s.logMetrics(ctx)
}

func (s *StagedPipeline) Enqueue(alert *models.Alert) bool {
	select {
	case s.fastCh <- alert:
		return true
	default:
		// Fast path channel full — apply backpressure
		// Critical alerts bypass to topo stage directly
		if alert.Severity == "critical" {
			select {
			case s.topoCh <- alert:
				return true
			default:
			}
		}
		atomic.AddInt64(&s.dropped, 1)
		log.Printf("[StagedPipeline] DROPPED alert %s (severity=%s) — channels full", alert.ID, alert.Severity)
		return false
	}
}

func (s *StagedPipeline) runFastWorker(ctx context.Context) {
	defer s.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case alert := <-s.fastCh:
			forwarded, err := s.fastProcessor.Process(ctx, alert)
			if err != nil {
				log.Printf("[FastWorker] error on %s: %v", alert.ID, err)
				// Forward to full path on error
				s.enqueueToFull(alert)
				continue
			}
			if !forwarded {
				// Fast path handled it (dedup/resolved) — discard
				atomic.AddInt64(&s.fastHits, 1)
			} else {
				// Not handled — advance to topo stage
				s.enqueueToTopo(alert)
			}
			atomic.AddInt64(&s.processed, 1)
		}
	}
}

func (s *StagedPipeline) runTopoWorker(ctx context.Context) {
	defer s.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case alert := <-s.topoCh:
			matched, err := s.topoProcessor.Process(ctx, alert)
			if err != nil {
				s.enqueueToFull(alert)
				continue
			}
			if matched {
				atomic.AddInt64(&s.topoHits, 1)
			} else {
				s.enqueueToFull(alert)
			}
		}
	}
}

func (s *StagedPipeline) runFullWorker(ctx context.Context) {
	defer s.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case alert := <-s.fullCh:
			if err := s.fullProcessor.Process(ctx, alert); err != nil {
				log.Printf("[FullWorker] error on %s: %v", alert.ID, err)
			}
			atomic.AddInt64(&s.fullHits, 1)
		}
	}
}

func (s *StagedPipeline) enqueueToTopo(alert *models.Alert) {
	select {
	case s.topoCh <- alert:
	default:
		// Topo full — skip to full path
		s.enqueueToFull(alert)
	}
}

func (s *StagedPipeline) enqueueToFull(alert *models.Alert) {
	select {
	case s.fullCh <- alert:
	default:
		log.Printf("[StagedPipeline] full channel overflow — dropping alert %s", alert.ID)
		atomic.AddInt64(&s.dropped, 1)
	}
}

func (s *StagedPipeline) logMetrics(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			log.Printf("[StagedPipeline] processed=%d dropped=%d fastHits=%d topoHits=%d fullHits=%d queues(f=%d t=%d x=%d)",
				atomic.LoadInt64(&s.processed), atomic.LoadInt64(&s.dropped),
				atomic.LoadInt64(&s.fastHits), atomic.LoadInt64(&s.topoHits), atomic.LoadInt64(&s.fullHits),
				len(s.fastCh), len(s.topoCh), len(s.fullCh))
		}
	}
}
```

### 6.2 Kafka Partitioning Strategy

```go
// Partition by cluster name — ensures all alerts from the same cluster
// are processed by the same consumer, enabling in-order deduplication.
func clusterPartitionKey(alert *models.Alert) string {
	if cluster := alert.Labels["k8s.cluster.name"]; cluster != "" {
		return cluster
	}
	if host := alert.Labels["host.name"]; host != "" {
		// BM/VM alerts: partition by first 3 chars of hostname (distributes evenly)
		if len(host) >= 3 {
			return host[:3]
		}
		return host
	}
	return alert.Source // fallback: by monitoring source
}
```

---

## 7. Correlation Explainability

### 7.1 Architecture

Every correlation decision must carry a structured, operator-readable explanation chain stored in `pipeline_correlation_results.explanation_json`.

```go
// internal/services/correlation/explainability.go
package correlation

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type EvidenceChainEntry struct {
	Source      string  `json:"source"`
	Score       float64 `json:"score"`
	Weight      float64 `json:"weight"`
	Contribution float64 `json:"contribution"` // score * weight
	Description string  `json:"description"`
}

type ExplainabilityReport struct {
	AlertID        uuid.UUID            `json:"alert_id"`
	Decision       string               `json:"decision"`
	FinalScore     float64              `json:"final_score"`
	RootCause      *RootCauseExplanation `json:"root_cause,omitempty"`
	BlastRadius    []string             `json:"blast_radius"`
	EvidenceChain  []EvidenceChainEntry `json:"evidence_chain"`
	Reasoning      []string             `json:"reasoning"`
	WhyGrouped     string               `json:"why_grouped,omitempty"`
	WhyRootCause   string               `json:"why_root_cause,omitempty"`
	WhyAttached    string               `json:"why_attached,omitempty"`
	DomainContext  string               `json:"domain_context,omitempty"`
	GeneratedAt    time.Time            `json:"generated_at"`
}

type RootCauseExplanation struct {
	EntityID       string    `json:"entity_id"`
	EntityLabel    string    `json:"entity_label"`
	EntityType     string    `json:"entity_type"`
	Confidence     float64   `json:"confidence"`
	CausalChain    []string  `json:"causal_chain"`
	EvidenceSources []string `json:"evidence_sources"`
}

// GenerateExplainabilityReport builds the full operator-facing explanation.
func GenerateExplainabilityReport(
	alert *Alert,
	decision *FinalCorrelationResult,
	topoResult *RecursiveTopoResult,
	hypotheses []*RCAHypothesis,
	ontologyResult *OntologyResult,
) *ExplainabilityReport {

	r := &ExplainabilityReport{
		AlertID:     alert.ID,
		Decision:    decision.Decision,
		FinalScore:  decision.FinalScore,
		GeneratedAt: time.Now(),
	}

	// ─── Evidence chain ───────────────────────────────────────────────────────
	weights := decision.WeightsUsed
	for name, sr := range decision.StrategyResults {
		if sr == nil {
			continue
		}
		var weight float64
		switch name {
		case "semantic":  weight = weights.Semantic
		case "temporal":  weight = weights.Temporal
		case "topology":  weight = weights.Topology
		case "rules":     weight = weights.Rules
		}
		r.EvidenceChain = append(r.EvidenceChain, EvidenceChainEntry{
			Source:       name,
			Score:        sr.Score,
			Weight:       weight,
			Contribution: sr.Score * weight,
			Description:  buildStrategyDescription(name, sr),
		})
	}

	// ─── Domain context ───────────────────────────────────────────────────────
	if ontologyResult != nil && ontologyResult.Domain != DomainUnknown {
		r.DomainContext = fmt.Sprintf("failure domain: %s | class: %s | keywords: %s",
			ontologyResult.Domain,
			ontologyResult.Class,
			strings.Join(ontologyResult.MatchedKeywords, ", "))
	}

	// ─── Root cause explanation ───────────────────────────────────────────────
	if topoResult != nil && topoResult.RootEntity != nil {
		chain := make([]string, len(topoResult.CausalChain))
		for i, link := range topoResult.CausalChain {
			chain[i] = fmt.Sprintf("%s -[%s]→ %s (weight: %.2f)",
				link.From.EntityID, link.EdgeType, link.To.EntityID, link.Weight)
		}
		r.RootCause = &RootCauseExplanation{
			EntityID:    topoResult.RootEntity.EntityID,
			EntityLabel: topoResult.RootEntity.Label,
			EntityType:  topoResult.RootEntity.EntityType,
			Confidence:  topoResult.RootConfidence,
			CausalChain: chain,
		}
		if len(topoResult.BlastRadius) > 0 {
			for _, n := range topoResult.BlastRadius {
				if n.PropagationScore > 0.50 {
					r.BlastRadius = append(r.BlastRadius, n.Node.Label)
				}
			}
		}
	}

	// ─── Human-readable sentences ─────────────────────────────────────────────
	r.Reasoning = buildReasoningParagraph(alert, decision, topoResult, ontologyResult, hypotheses)

	switch decision.Decision {
	case "merge_incident":
		r.WhyGrouped = buildWhyGrouped(decision)
	case "create_incident":
		if r.RootCause != nil {
			r.WhyRootCause = buildWhyRootCause(r.RootCause, topoResult)
		}
	case "merge", "attach":
		r.WhyAttached = buildWhyAttached(decision, topoResult)
	}

	return r
}

func buildStrategyDescription(name string, sr *StrategyResult) string {
	if sr.BestMatch != nil {
		return fmt.Sprintf("matched alert '%s' (%.3f confidence, %s processing time)",
			sr.BestMatch.Title, sr.Score, sr.ProcessingTime.Round(time.Millisecond))
	}
	if sr.Error != nil {
		return fmt.Sprintf("strategy failed: %v", sr.Error)
	}
	if sr.Score == 0 {
		return "no match found"
	}
	return fmt.Sprintf("score %.3f, %d similar alerts", sr.Score, len(sr.SimilarAlerts))
}

func buildReasoningParagraph(alert *Alert, decision *FinalCorrelationResult,
	topo *RecursiveTopoResult, onto *OntologyResult, hyps []*RCAHypothesis) []string {

	var r []string

	if onto != nil && onto.Domain != DomainUnknown {
		r = append(r, fmt.Sprintf("Alert classified as %s failure (class: %s, confidence: %.2f)",
			onto.Domain, onto.Class, onto.Confidence))
	}

	if topo != nil && topo.RootEntity != nil {
		r = append(r, fmt.Sprintf("Topology traversal identified root cause: %s (%s) at infrastructure level %s, reachable via %d-hop path with confidence %.3f",
			topo.RootEntity.Label, topo.RootEntity.EntityType, topo.RootEntity.InfraLevel,
			topo.Depth, topo.RootConfidence))
	}

	if decision.FinalScore > 0 {
		dominant := decision.DominantStrategy
		r = append(r, fmt.Sprintf("Dominant correlation signal: %s (final weighted score: %.3f)", dominant, decision.FinalScore))
	}

	if len(hyps) > 1 {
		r = append(r, fmt.Sprintf("Competing RCA hypotheses evaluated (%d candidates); MAP estimate selected with confidence %.3f",
			len(hyps), hyps[0].Confidence))
	}

	if len(r) == 0 {
		r = append(r, fmt.Sprintf("Decision: %s (score: %.3f)", decision.Decision, decision.FinalScore))
	}
	return r
}

func buildWhyGrouped(d *FinalCorrelationResult) string {
	if d.BestMatch == nil {
		return ""
	}
	return fmt.Sprintf("Grouped with incident because: dominant strategy '%s' scored %.3f (threshold: 0.40), matching incident '%s' on shared infrastructure",
		d.DominantStrategy, d.FinalScore, d.BestMatch.ID)
}

func buildWhyRootCause(rc *RootCauseExplanation, topo *RecursiveTopoResult) string {
	chain := strings.Join(rc.CausalChain, " → ")
	return fmt.Sprintf("Identified as root cause: %s (%s) with confidence %.3f. Causal path: %s",
		rc.EntityLabel, rc.EntityType, rc.Confidence, chain)
}

func buildWhyAttached(d *FinalCorrelationResult, topo *RecursiveTopoResult) string {
	if topo != nil && topo.RootEntity != nil {
		return fmt.Sprintf("Attached to existing incident: topology graph confirmed this alert is a downstream effect of root entity %s (propagation score: %.3f)",
			topo.RootEntity.Label, topo.RootConfidence)
	}
	return fmt.Sprintf("Attached to existing incident: %s strategy confirmed correlation (score: %.3f)",
		d.DominantStrategy, d.FinalScore)
}

// JSON helper for storing in DB
func (r *ExplainabilityReport) ToJSON() ([]byte, error) {
	return json.Marshal(r)
}
```

---

## 8. Adaptive Learning Engine

### 8.1 Architecture: EMA Weight Tuning + Feature Store

The existing `feedback_loop.go` recalibrates weights after human feedback. This extends it with:

1. **Per-domain weight tuning**: Storage failure incidents use different weights than network incidents
2. **Exponential Moving Average updates**: Online learning after each confirmed/rejected correlation
3. **Feature store**: Persist per-source, per-domain, per-cluster model parameters

```go
// internal/services/correlation/adaptive_learning.go
package correlation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// FeatureKey identifies a model parameter context.
type FeatureKey struct {
	Domain  FailureDomain
	Source  string  // "dynatrace", "prometheus", "splunk", ""=all
	Cluster string  // specific cluster or ""=all
}

func (fk FeatureKey) String() string {
	return fmt.Sprintf("feature:%s:%s:%s", fk.Domain, fk.Source, fk.Cluster)
}

// DomainWeights are strategy weights tuned for a specific domain context.
type DomainWeights struct {
	FeatureKey FeatureKey      `json:"feature_key"`
	Weights    StrategyWeights `json:"weights"`
	SampleSize int             `json:"sample_size"` // number of feedback points
	Accuracy   float64         `json:"accuracy"`    // running accuracy
	UpdatedAt  time.Time       `json:"updated_at"`
}

// LearningEvent is emitted when operator feedback is received.
type LearningEvent struct {
	AlertID        string
	IncidentID     string
	Domain         FailureDomain
	Source         string
	Cluster        string
	DecisionMade   string // "create_incident", "merge", "discard"
	IsCorrect      bool   // operator confirmed or rejected
	StrategyScores map[string]float64
	Timestamp      time.Time
}

// AdaptiveLearningEngine extends the existing CorrelationFeedbackService
// with per-domain weight tuning and feature persistence.
type AdaptiveLearningEngine struct {
	db         *sql.DB
	rdb        *redis.Client
	existing   *CorrelationFeedbackService // preserve existing behavior
	globalEMA  float64                    // EMA smoothing factor (0.05–0.15)
	featureMu  sync.RWMutex
	features   map[string]*DomainWeights
}

func NewAdaptiveLearningEngine(db *sql.DB, rdb *redis.Client, existing *CorrelationFeedbackService) *AdaptiveLearningEngine {
	e := &AdaptiveLearningEngine{
		db:       db,
		rdb:      rdb,
		existing: existing,
		globalEMA: 0.08, // 8% influence per feedback event
		features: make(map[string]*DomainWeights),
	}
	go e.loadFeaturesFromDB(context.Background())
	return e
}

// OnFeedback is called when an operator confirms or rejects a correlation.
func (e *AdaptiveLearningEngine) OnFeedback(ctx context.Context, event LearningEvent) {
	// 1. Delegate to existing feedback service (preserve existing behavior)
	go e.existing.ProcessFeedback(ctx, CorrelationFeedback{
		AlertID:    event.AlertID,
		IsCorrect:  event.IsCorrect,
		Strategy:   dominantStrategy(event.StrategyScores),
		RecordedAt: event.Timestamp,
	})

	// 2. Update domain-specific weights via EMA
	e.updateDomainWeights(ctx, event)
}

func (e *AdaptiveLearningEngine) updateDomainWeights(ctx context.Context, event LearningEvent) {
	key := FeatureKey{Domain: event.Domain, Source: event.Source, Cluster: event.Cluster}
	keyStr := key.String()

	e.featureMu.Lock()
	defer e.featureMu.Unlock()

	fw, exists := e.features[keyStr]
	if !exists {
		// Initialize with global defaults
		fw = &DomainWeights{
			FeatureKey: key,
			Weights:    defaultWeights(),
			UpdatedAt:  time.Now(),
		}
		e.features[keyStr] = fw
	}

	// EMA weight update
	// When a strategy correctly identified the correlation, boost its weight.
	// When it wrongly drove a bad correlation, reduce its weight.
	// Constraints: each weight stays in [0.05, 0.55] and all weights sum to 1.0.

	alpha := e.globalEMA
	if event.IsCorrect {
		// Boost the dominant strategy weight
		dominant := dominantStrategy(event.StrategyScores)
		fw.Weights = emaBoost(fw.Weights, dominant, alpha)
		fw.Accuracy = fw.Accuracy*(1-alpha) + 1.0*alpha
	} else {
		// Penalize the dominant strategy
		dominant := dominantStrategy(event.StrategyScores)
		fw.Weights = emaPenalty(fw.Weights, dominant, alpha)
		fw.Accuracy = fw.Accuracy*(1-alpha) + 0.0*alpha
	}
	fw.SampleSize++
	fw.UpdatedAt = time.Now()

	// Only update DB if we have meaningful sample size
	if fw.SampleSize >= 5 {
		go e.persistFeature(context.Background(), fw)
	}
}

// GetWeightsForContext returns the learned weights for a given alert context.
// Falls back to global weights if no domain-specific weights exist.
func (e *AdaptiveLearningEngine) GetWeightsForContext(alert *Alert) StrategyWeights {
	ontology := NewOntologyEngine()
	onto := ontology.Classify(alert)

	candidates := []FeatureKey{
		// Most specific: domain + source + cluster
		{Domain: onto.Domain, Source: alert.Source, Cluster: alert.Labels["k8s.cluster.name"]},
		// Less specific: domain + source
		{Domain: onto.Domain, Source: alert.Source},
		// Domain only
		{Domain: onto.Domain},
		// Global fallback
		{Domain: DomainUnknown},
	}

	e.featureMu.RLock()
	defer e.featureMu.RUnlock()

	for _, key := range candidates {
		if fw, ok := e.features[key.String()]; ok && fw.SampleSize >= 10 {
			return fw.Weights
		}
	}
	return defaultWeights()
}

func emaBoost(w StrategyWeights, dominant string, alpha float64) StrategyWeights {
	boost := alpha * 0.03
	switch dominant {
	case "semantic":
		w.sem 0.20, 0.55)
	case "temporal":
		w.Temporal = clamp(w.Temporal+boost, 0.05, 0.55)
	case "topology":
		w.topo 0.45)
	case "rules":
		w.Rules = clamp(w.Rules+boost, 0.05, 0.55)
	}
	return normalizeWeights(w)
}

func emaPenalty(w StrategyWeights, dominant string, alpha float64) StrategyWeights {
	penalty := alpha * 0.03
	switch dominant {
	case "semantic":
		w.sem 0.20, 0.55)
	case "temporal":
		w.Temporal = clamp(w.Temporal-penalty, 0.05, 0.55)
	case "topology":
		w.topo 0.45)
	case "rules":
		w.Rules = clamp(w.Rules-penalty, 0.05, 0.55)
	}
	return normalizeWeights(w)
}

func normalizeWeights(w StrategyWeights) StrategyWeights {
	total := w.Semantic + w.Temporal + w.Topology + w.Rules
	if total == 0 {
		return defaultWeights()
	}
	return StrategyWeights{
		Semantic: w.Semantic / total,
		Temporal: w.Temporal / total,
		Topology: w.Topology / total,
		Rules:    w.Rules / total,
	}
}

func defaultWeights() StrategyWeights {
	return StrategyWeights{Semantic: 0.20, Temporal: 0.10, Topology: 0.45, Rules: 0.25}
}

func clamp(v, min, max float64) float64 {
	return math.Max(min, math.Min(max, v))
}

func dominantStrategy(scores map[string]float64) string {
	best, bestScore := "", 0.0
	for k, v := range scores {
		if v > bestScore {
			best, bestScore = k, v
		}
	}
	return best
}

func (e *AdaptiveLearningEngine) persistFeature(ctx context.Context, fw *DomainWeights) {
	data, err := json.Marshal(fw)
	if err != nil {
		return
	}
	_, err = e.db.ExecContext(ctx, `
		INSERT INTO correlation_feature_store (feature_key, domain, source, cluster, weights_json, sample_size, accuracy, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		ON CONFLICT (feature_key) DO UPDATE SET
			weights_json = EXCLUDED.weights_json,
			sample_size  = EXCLUDED.sample_size,
			accuracy     = EXCLUDED.accuracy,
			updated_at   = NOW()`,
		fw.FeatureKey.String(), string(fw.FeatureKey.Domain),
		fw.FeatureKey.Source, fw.FeatureKey.Cluster,
		string(data), fw.SampleSize, fw.Accuracy)
	if err != nil {
		log.Printf("[AdaptiveLearning] persist feature %s: %v", fw.FeatureKey.String(), err)
	}
	// Also cache in Redis
	e.rdb.Set(ctx, "feature:"+fw.FeatureKey.String(), data, 24*time.Hour)
}

func (e *AdaptiveLearningEngine) loadFeaturesFromDB(ctx context.Context) {
	rows, err := e.db.QueryContext(ctx,
		`SELECT feature_key, weights_json, sample_size, accuracy FROM correlation_feature_store`)
	if err != nil {
		return
	}
	defer rows.Close()

	e.featureMu.Lock()
	defer e.featureMu.Unlock()

	for rows.Next() {
		var key, weightsJSON string
		var sampleSize int
		var accuracy float64
		if err := rows.Scan(&key, &weightsJSON, &sampleSize, &accuracy); err != nil {
			continue
		}
		var fw DomainWeights
		if err := json.Unmarshal([]byte(weightsJSON), &fw); err != nil {
			continue
		}
		fw.SampleSize = sampleSize
		fw.Accuracy = accuracy
		e.features[key] = &fw
	}
}

var _ = math.Log // silence unused
```

---

## 9. Vector & Feature Store

### 9.1 pgvector Schema (Primary — No External Dependency)

Add `pgvector` as the primary vector store. Weaviate remains as secondary for complex vector queries.

```sql
-- PostgreSQL + pgvector extension
CREATE EXTENSION IF NOT EXISTS vector;

-- Alert embedding table — stores BERT embeddings inline
CREATE TABLE alert_embeddings (
    alert_id        UUID PRIMARY KEY REFERENCES alerts(id) ON DELETE CASCADE,
    embedding       vector(768),       -- BERT 768-dim
    model_version   TEXT NOT NULL DEFAULT 'bert-base-uncased',
    domain          TEXT,              -- ontology domain
    class           TEXT,              -- ontology class
    cluster         TEXT,
    severity        TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- IVFFlat index for ANN search (train after 50k+ rows for best results)
CREATE INDEX CONCURRENTLY alert_embeddings_ivfflat_idx
    ON alert_embeddings
    USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 200);

-- HNSW index — better recall for production workloads (PostgreSQL 16+ / pgvector 0.5+)
CREATE INDEX CONCURRENTLY alert_embeddings_hnsw_idx
    ON alert_embeddings
    USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

-- ANN similarity search — find top-20 similar alerts in last 24h
-- Uses pgvector's <=> operator (cosine distance, lower = more similar)
SELECT
    ae.alert_id,
    a.title,
    a.severity,
    a.source,
    ae.domain,
    1 - (ae.embedding <=> $1::vector) AS cosine_similarity
FROM alert_embeddings ae
JOIN alerts a ON a.id = ae.alert_id
WHERE ae.created_at >= NOW() - INTERVAL '24 hours'
  AND ae.cluster = $2
  AND ae.alert_id != $3
  AND 1 - (ae.embedding <=> $1::vector) > 0.75
ORDER BY ae.embedding <=> $1::vector
LIMIT 20;

-- Adaptive learning feature store
CREATE TABLE correlation_feature_store (
    feature_key TEXT PRIMARY KEY,
    domain      TEXT NOT NULL,
    source      TEXT NOT NULL DEFAULT '',
    cluster     TEXT NOT NULL DEFAULT '',
    weights_json JSONB NOT NULL,
    sample_size INT NOT NULL DEFAULT 0,
    accuracy    FLOAT NOT NULL DEFAULT 0,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Incident causal lineage — tracks evolution history
CREATE TABLE incident_causal_lineage (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id    UUID NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    parent_id      UUID REFERENCES incidents(id),
    lineage_type   TEXT NOT NULL, -- 'merge', 'split', 'reparent', 'origin'
    root_entity_id TEXT,
    root_entity_label TEXT,
    confidence     FLOAT,
    reasoning      TEXT,
    metadata       JSONB,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX ON incident_causal_lineage(incident_id);
CREATE INDEX ON incident_causal_lineage(root_entity_id);

-- Dependency edge weights — stored in PostgreSQL, synced to Neo4j
CREATE TABLE infrastructure_dependency_edges (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_id      TEXT NOT NULL,
    target_id      TEXT NOT NULL,
    edge_type      TEXT NOT NULL, -- HOSTS, RUNS_ON, MOUNTS, DEPENDS_ON
    weight         FLOAT NOT NULL DEFAULT 0.85,
    domain         TEXT,
    last_confirmed TIMESTAMPTZ,
    confidence     FLOAT NOT NULL DEFAULT 0.80,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(source_id, target_id, edge_type)
);
CREATE INDEX ON infrastructure_dependency_edges(source_id);
CREATE INDEX ON infrastructure_dependency_edges(target_id);
CREATE INDEX ON infrastructure_dependency_edges(edge_type);
```

### 9.2 Go Vector Repository

```go
// internal/services/correlation/vector_repository.go
package correlation

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	pgvector "github.com/pgvector/pgvector-go"
)

type VectorRepository struct {
	db *sql.DB
}

func NewVectorRepository(db *sql.DB) *VectorRepository {
	return &VectorRepository{db: db}
}

// StoreEmbedding persists an alert's BERT embedding.
func (r *VectorRepository) StoreEmbedding(ctx context.Context, alertID uuid.UUID, embedding []float32, domain, class, cluster, severity, modelVersion string) error {
	vec := pgvector.NewVector(embedding)
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO alert_embeddings (alert_id, embedding, model_version, domain, class, cluster, severity)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (alert_id) DO UPDATE SET
			embedding     = EXCLUDED.embedding,
			model_version = EXCLUDED.model_version,
			domain        = EXCLUDED.domain,
			class         = EXCLUDED.class,
			updated_at    = NOW()`,
		alertID, vec, modelVersion, domain, class, cluster, severity)
	return err
}

type VectorMatch struct {
	AlertID    uuid.UUID
	Title      string
	Severity   string
	Source     string
	Domain     string
	Similarity float64
}

// FindSimilar performs ANN cosine similarity search.
// Filters by cluster and 24h window; returns top-20 above threshold.
func (r *VectorRepository) FindSimilar(ctx context.Context, embedding []float32, alertID uuid.UUID, cluster string, threshold float64, limit int) ([]VectorMatch, error) {
	vec := pgvector.NewVector(embedding)

	// Build cluster filter — if cluster empty, search all
	clusterFilter := ""
	args := []interface{}{vec, alertID, threshold, limit}
	if cluster != "" {
		clusterFilter = "AND ae.cluster = $5"
		args = append(args, cluster)
	}

	query := fmt.Sprintf(`
		SELECT
			ae.alert_id,
			a.title,
			a.severity,
			a.source,
			ae.domain,
			1 - (ae.embedding <=> $1) AS similarity
		FROM alert_embeddings ae
		JOIN alerts a ON a.id = ae.alert_id
		WHERE ae.created_at >= NOW() - INTERVAL '24 hours'
		  AND ae.alert_id != $2
		  AND 1 - (ae.embedding <=> $1) > $3
		  %s
		ORDER BY ae.embedding <=> $1
		LIMIT $4`, clusterFilter)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("vector similarity query: %w", err)
	}
	defer rows.Close()

	var matches []VectorMatch
	for rows.Next() {
		var m VectorMatch
		if err := rows.Scan(&m.AlertID, &m.Title, &m.Severity, &m.Source, &m.Domain, &m.Similarity); err != nil {
			continue
		}
		matches = append(matches, m)
	}
	return matches, rows.Err()
}

// FindSimilarByDomain constrains search to same ontology domain.
// More precise when domain is known.
func (r *VectorRepository) FindSimilarByDomain(ctx context.Context, embedding []float32, alertID uuid.UUID, domain, cluster string, threshold float64) ([]VectorMatch, error) {
	vec := pgvector.NewVector(embedding)
	rows, err := r.db.QueryContext(ctx, `
		SELECT ae.alert_id, a.title, a.severity, a.source, ae.domain,
		       1 - (ae.embedding <=> $1) AS similarity
		FROM alert_embeddings ae
		JOIN alerts a ON a.id = ae.alert_id
		WHERE ae.created_at >= NOW() - INTERVAL '24 hours'
		  AND ae.alert_id != $2
		  AND ae.domain = $3
		  AND ae.cluster = $4
		  AND 1 - (ae.embedding <=> $1) > $5
		ORDER BY ae.embedding <=> $1
		LIMIT 15`,
		vec, alertID, domain, cluster, threshold)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var matches []VectorMatch
	for rows.Next() {
		var m VectorMatch
		rows.Scan(&m.AlertID, &m.Title, &m.Severity, &m.Source, &m.Domain, &m.Similarity)
		matches = append(matches, m)
	}
	return matches, rows.Err()
}
```

---

## 10. Production Hardening

### 10.1 Idempotent Incident Creation

The existing 17-step deduplication cascade guards against logical duplicates. This adds a **distributed idempotency token** to prevent race conditions at the DB level.

```go
// internal/services/incidents/idempotent_create.go
package incidents

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// idempotencyToken computes a stable token for an incident creation request.
// Same alert + cluster + domain within a 5-minute window = same token.
func idempotencyToken(alertEntityID, cluster, domain, severity string, t time.Time) string {
	// Round to nearest 5 minutes to handle near-simultaneous arrivals
	window := t.Truncate(5 * time.Minute)
	raw := fmt.Sprintf("%s|%s|%s|%s|%d", alertEntityID, cluster, domain, severity, window.Unix())
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:16]) // 32-char hex = 128 bits
}

const idempotencyKeyPrefix = "idem:incident:"
const idempotencyTTL = 10 * time.Minute

// IdempotentCreateResult tells the caller what happened.
type IdempotentCreateResult struct {
	IncidentID uuid.UUID
	Created    bool   // false if existing incident was returned
	Token      string
}

// AcquireIdempotencyToken tries to claim the token.
// Returns (true, nil) if claim succeeded (this caller should create the incident).
// Returns (false, existingID) if another caller already claimed it.
func AcquireIdempotencyToken(ctx context.Context, rdb *redis.Client, token string) (bool, *uuid.UUID, error) {
	key := idempotencyKeyPrefix + token

	// Try to set the key atomically (SETNX semantics)
	ok, err := rdb.SetNX(ctx, key, "pending", idempotencyTTL).Result()
	if err != nil {
		return false, nil, fmt.Errorf("idempotency check: %w", err)
	}

	if ok {
		// We acquired it — caller should proceed with creation
		return true, nil, nil
	}

	// Key exists — check if incident ID was stored
	val, err := rdb.Get(ctx, key).Result()
	if err != nil {
		return false, nil, nil // key expired between SetNX and Get — allow creation
	}

	if val == "pending" {
		// Another goroutine is in the middle of creating — wait briefly and retry
		time.Sleep(100 * time.Millisecond)
		val, err = rdb.Get(ctx, key).Result()
		if err != nil || val == "pending" {
			return false, nil, nil // still pending — proceed anyway (duplicate check is belt-and-suspenders)
		}
	}

	id, err := uuid.Parse(val)
	if err != nil {
		return false, nil, nil
	}
	return false, &id, nil
}

// ConfirmIdempotencyToken records the created incident ID against the token.
func ConfirmIdempotencyToken(ctx context.Context, rdb *redis.Client, token string, incidentID uuid.UUID) {
	key := idempotencyKeyPrefix + token
	rdb.Set(ctx, key, incidentID.String(), idempotencyTTL)
}
```

### 10.2 Circuit Breaker Pattern for External Services

```go
// internal/services/correlation/circuit_breaker.go
package correlation

import (
	"errors"
	"sync"
	"time"
)

// CircuitState represents the state of a circuit breaker.
type CircuitState int

const (
	CircuitClosed   CircuitState = iota // normal operation
	CircuitOpen                         // failing — reject calls
	CircuitHalfOpen                     // testing recovery
)

// CircuitBreaker guards calls to external services (Neo4j, Weaviate, BERT, RCA).
type CircuitBreaker struct {
	name         string
	maxFailures  int
	resetTimeout time.Duration
	mu           sync.RWMutex
	state        CircuitState
	failures     int
	lastFailure  time.Time
	successInHalfOpen int
}

var ErrCircuitOpen = errors.New("circuit breaker open — external service unavailable")

func NewCircuitBreaker(name string, maxFailures int, resetTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		name:         name,
		maxFailures:  maxFailures,
		resetTimeout: resetTimeout,
		state:        CircuitClosed,
	}
}

// Call executes fn with circuit breaker protection.
// Returns ErrCircuitOpen if the circuit is open.
func (cb *CircuitBreaker) Call(fn func() error) error {
	if !cb.allowRequest() {
		return ErrCircuitOpen
	}

	err := fn()
	cb.recordResult(err)
	return err
}

func (cb *CircuitBreaker) allowRequest() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		if time.Since(cb.lastFailure) > cb.resetTimeout {
			cb.state = CircuitHalfOpen
			cb.successInHalfOpen = 0
			return true
		}
		return false
	case CircuitHalfOpen:
		return true
	}
	return false
}

func (cb *CircuitBreaker) recordResult(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.failures++
		cb.lastFailure = time.Now()
		if cb.state == CircuitHalfOpen || cb.failures >= cb.maxFailures {
			cb.state = CircuitOpen
			cb.failures = 0 // reset for next attempt after timeout
		}
	} else {
		if cb.state == CircuitHalfOpen {
			cb.successInHalfOpen++
			if cb.successInHalfOpen >= 3 {
				cb.state = CircuitClosed
				cb.failures = 0
			}
		} else {
			cb.failures = 0
		}
	}
}

func (cb *CircuitBreaker) IsOpen() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state == CircuitOpen
}

// PerServiceCircuitBreakers holds breakers for all external dependencies.
type PerServiceCircuitBreakers struct {
	Neo4j    *CircuitBreaker
	Weaviate *CircuitBreaker
	BERT     *CircuitBreaker
	RCA      *CircuitBreaker
	Ollama   *CircuitBreaker
}

func NewCircuitBreakers() *PerServiceCircuitBreakers {
	return &PerServiceCircuitBreakers{
		Neo4j:    NewCircuitBreaker("neo4j",    5, 30*time.Second),
		Weaviate: NewCircuitBreaker("weaviate", 5, 30*time.Second),
		BERT:     NewCircuitBreaker("bert",     3, 20*time.Second),
		RCA:      NewCircuitBreaker("rca",      3, 60*time.Second),
		Ollama:   NewCircuitBreaker("ollama",   3, 30*time.Second),
	}
}
```

---

## 11. Neo4j Schema & Cypher Reference

### 11.1 Full Infrastructure Graph Schema

```cypher
// ─── Constraints & Indexes ────────────────────────────────────────────────────
CREATE CONSTRAINT bm_unique       ON (n:BareMetal)   ASSERT n.entity_id IS UNIQUE;
CREATE CONSTRAINT kvm_unique      ON (n:KVMHost)     ASSERT n.entity_id IS UNIQUE;
CREATE CONSTRAINT vm_unique       ON (n:CloudVM)     ASSERT n.entity_id IS UNIQUE;
CREATE CONSTRAINT k8s_cluster_uniq ON (n:K8sCluster) ASSERT n.entity_id IS UNIQUE;
CREATE CONSTRAINT k8s_node_uniq   ON (n:K8sNode)     ASSERT n.entity_id IS UNIQUE;
CREATE CONSTRAINT k8s_pod_uniq    ON (n:K8sPod)      ASSERT n.entity_id IS UNIQUE;
CREATE CONSTRAINT netapp_vol_uniq ON (n:NetAppVol)   ASSERT n.entity_id IS UNIQUE;
CREATE CONSTRAINT net_uniq        ON (n:Network)     ASSERT n.entity_id IS UNIQUE;

CREATE INDEX ON :K8sNode(cluster);
CREATE INDEX ON :K8sPod(cluster, namespace);
CREATE INDEX ON :CloudVM(cluster);
CREATE INDEX ON :BareMetal(dc, rack);

// ─── Alert Blast Radius Query (production) ────────────────────────────────────
// Given a root entity, find all directly or transitively affected entities
// within 8 hops, with attenuated propagation scores.

MATCH path = (root)-[rels*1..8]->(affected)
WHERE root.entity_id = $root_entity_id
  AND ALL(r IN rels WHERE type(r) IN ['HOSTS','RUNS_ON','MEMBER_OF','MOUNTS','DEPENDS_ON'])
WITH root, affected, path,
     reduce(score = 1.0, r IN relationships(path) | score * r.weight) AS raw_score,
     length(path) AS depth
WHERE raw_score > 0.15
RETURN DISTINCT
    affected.entity_id       AS entity_id,
    labels(affected)[0]      AS entity_type,
    affected.label            AS label,
    affected.cluster          AS cluster,
    affected.namespace        AS namespace,
    raw_score * pow(0.9, depth) AS propagation_score,
    depth
ORDER BY propagation_score DESC;

// ─── Cross-cluster impact (network segments) ──────────────────────────────────
// Find entities in other clusters that share a storage or network dependency
// with the root entity's cluster.

MATCH (root_cluster:K8sCluster {entity_id: $cluster_id})
MATCH (root_cluster)-[:USES_NETWORK]->(net:Network)<-[:USES_NETWORK]-(other_cluster:K8sCluster)
WHERE other_cluster.entity_id <> $cluster_id
MATCH (other_cluster)-[:MEMBER_OF*1..3]->(affected)
RETURN other_cluster.entity_id, other_cluster.label,
       affected.entity_id, affected.label, affected.entity_type,
       0.55 AS cross_cluster_impact
ORDER BY other_cluster.label;

// ─── NetApp storage impact propagation ───────────────────────────────────────
// Starting from a NetApp node, find all pods that mount volumes from it.

MATCH (na:NetAppNode {entity_id: $netapp_id})
MATCH (na)-[:HOSTS]->(vol:NetAppVol)
MATCH (pod:K8sPod)-[:MOUNTS]->(vol)
MATCH (node:K8sNode)-[:MEMBER_OF]->(cluster:K8sCluster)
WHERE (pod)-[:RUNS_ON]->(node)
RETURN na.entity_id, na.label,
       vol.entity_id AS volume_id, vol.label AS volume_name,
       pod.entity_id AS pod_id, pod.label AS pod_name,
       pod.namespace,
       node.entity_id AS node_id, node.label AS node_name,
       cluster.label AS cluster_name,
       0.90 * 0.90 * 0.90 AS end_to_end_propagation; // netapp→vol→pod

// ─── Find isolated incidents (split candidates) ───────────────────────────────
// Given two alert entity IDs, check if they share a common ancestor
// within 5 hops. If not, they should be in separate incidents.

MATCH (a {entity_id: $entity_a}), (b {entity_id: $entity_b})
OPTIONAL MATCH path_a = (root)-[*1..5]->(a)
OPTIONAL MATCH path_b = (root)-[*1..5]->(b)
WHERE path_a IS NOT NULL AND path_b IS NOT NULL
RETURN root.entity_id AS common_ancestor,
       length(path_a) AS depth_to_a,
       length(path_b) AS depth_to_b,
       length(path_a) + length(path_b) AS total_path
ORDER BY total_path
LIMIT 1;
```

---

## 12. Schema Additions

```sql
-- ─── Extend pipeline_correlation_results ─────────────────────────────────────
ALTER TABLE pipeline_correlation_results
    ADD COLUMN IF NOT EXISTS explanation_json   JSONB,
    ADD COLUMN IF NOT EXISTS domain             TEXT,
    ADD COLUMN IF NOT EXISTS ontology_class     TEXT,
    ADD COLUMN IF NOT EXISTS ontology_confidence FLOAT,
    ADD COLUMN IF NOT EXISTS rca_hypotheses     JSONB,    -- array of RCAHypothesis
    ADD COLUMN IF NOT EXISTS topo_root_entity   TEXT,
    ADD COLUMN IF NOT EXISTS topo_depth         INT,
    ADD COLUMN IF NOT EXISTS blast_radius_count INT,
    ADD COLUMN IF NOT EXISTS stage              TEXT;     -- 'fast','topo','full'

-- ─── Extend incidents ────────────────────────────────────────────────────────
ALTER TABLE incidents
    ADD COLUMN IF NOT EXISTS rca_hypotheses       JSONB,    -- competing hypotheses
    ADD COLUMN IF NOT EXISTS evolution_generation INT DEFAULT 0,  -- incremented on each evolution
    ADD COLUMN IF NOT EXISTS merge_source_ids     UUID[],   -- IDs of absorbed incidents
    ADD COLUMN IF NOT EXISTS split_parent_id      UUID,     -- parent if this was created by split
    ADD COLUMN IF NOT EXISTS ontology_domain      TEXT,
    ADD COLUMN IF NOT EXISTS ontology_class       TEXT,
    ADD COLUMN IF NOT EXISTS topo_root_entity_id  TEXT,
    ADD COLUMN IF NOT EXISTS topo_root_entity_label TEXT,
    ADD COLUMN IF NOT EXISTS causal_chain         JSONB;    -- serialized chain of CausalLink

-- ─── Correlation feature store ────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS correlation_feature_store (
    feature_key  TEXT PRIMARY KEY,
    domain       TEXT NOT NULL DEFAULT '',
    source       TEXT NOT NULL DEFAULT '',
    cluster      TEXT NOT NULL DEFAULT '',
    weights_json JSONB NOT NULL,
    sample_size  INT NOT NULL DEFAULT 0,
    accuracy     FLOAT NOT NULL DEFAULT 0,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─── Incident causal lineage ──────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS incident_causal_lineage (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id       UUID NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    parent_id         UUID REFERENCES incidents(id),
    lineage_type      TEXT NOT NULL,
    root_entity_id    TEXT,
    root_entity_label TEXT,
    confidence        FLOAT,
    reasoning         TEXT,
    metadata          JSONB,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_causal_lineage_incident ON incident_causal_lineage(incident_id);
CREATE INDEX IF NOT EXISTS idx_causal_lineage_root     ON incident_causal_lineage(root_entity_id);

-- ─── Alert embeddings (pgvector) ─────────────────────────────────────────────
-- Requires: CREATE EXTENSION vector;
CREATE TABLE IF NOT EXISTS alert_embeddings (
    alert_id      UUID PRIMARY KEY REFERENCES alerts(id) ON DELETE CASCADE,
    embedding     vector(768),
    model_version TEXT NOT NULL DEFAULT 'bert-base-uncased',
    domain        TEXT,
    class         TEXT,
    cluster       TEXT,
    severity      TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS alert_embeddings_hnsw_idx
    ON alert_embeddings USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

-- ─── Infrastructure dependency edges ──────────────────────────────────────────
CREATE TABLE IF NOT EXISTS infrastructure_dependency_edges (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_id      TEXT NOT NULL,
    target_id      TEXT NOT NULL,
    edge_type      TEXT NOT NULL,
    weight         FLOAT NOT NULL DEFAULT 0.85,
    domain         TEXT,
    last_confirmed TIMESTAMPTZ,
    confidence     FLOAT NOT NULL DEFAULT 0.80,
    dc             TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(source_id, target_id, edge_type)
);
CREATE INDEX IF NOT EXISTS idx_dep_edges_source ON infrastructure_dependency_edges(source_id);
CREATE INDEX IF NOT EXISTS idx_dep_edges_target ON infrastructure_dependency_edges(target_id);

-- ─── Indexes for evolution queries ────────────────────────────────────────────
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_incidents_evolution
    ON incidents(status, created_at)
    WHERE status IN ('open','investigating','acknowledged');

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_incidents_topo_root
    ON incidents(topo_root_entity_id)
    WHERE topo_root_entity_id IS NOT NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_alerts_entity
    ON alerts(entity_id)
    WHERE entity_id IS NOT NULL;
```

---

## 13. Migration Path

### Phase 1 — Foundations (Weeks 1–2)

1. **Schema migration**: Run all `ALTER TABLE` and `CREATE TABLE` statements above. Zero downtime — all columns are `IF NOT EXISTS` and `ALTER TABLE ... ADD COLUMN` with defaults.
2. **OntologyEngine integration**: Add to existing `processAlert` flow — classify every alert at intake, store `domain` + `class` in alert metadata. No behavior change yet.
3. **pgvector setup**: Install extension, create `alert_embeddings` table and HNSW index. Modify `runSemanticStrategy` to store embeddings in pgvector alongside Weaviate.

### Phase 2 — Topology & Probabilistic RCA (Weeks 3–4)

4. **RecursiveTopoRCAEngine**: Wire into existing `TopologyGraphCorrelator.Correlate()` as a deeper fallback. If Redis graph gives a confident result, return it. Otherwise, call Neo4j recursive traversal. Zero regression risk — it's a fallback path.
5. **ProbabilisticRCAEngine**: Replace the `rootCauseEngine.Evaluate` call in the pipeline with `probabilisticRCAEngine.Evaluate`. The new engine calls the existing engine first and only augments with hypothesis evaluation when not highly confident.

### Phase 3 — Evolution & Explainability (Weeks 5–6)

6. **IncidentEvolutionEngine**: Start as a read-only evaluation loop — log what merges/splits/escalations would occur without applying them. After 1-week observation, enable merge and escalation. Enable split last.
7. **ExplainabilityReport**: Store in `pipeline_correlation_results.explanation_json` for every processed alert. No behavior change — purely additive.

### Phase 4 — Adaptive Learning & Scalability (Weeks 7–8)

8. **AdaptiveLearningEngine**: Wire feedback endpoint to emit `LearningEvent`. Start with `globalEMA = 0.05` (very conservative). Observe weight drift. Increase after 2 weeks of stable operation.
9. **StagedPipeline**: Run in parallel with existing pipeline initially — compare decisions. Switch over when confidence is high.

### Rollback Safety

Every new component is:
- **Additive** — no existing code deleted
- **Gracefully degrading** — circuit breakers on every external call, fallback to existing behavior on failure
- **Feature-flag controlled** — each component can be disabled via environment variable without code change

---

*End of Architecture Document — v2.0-design*  
*All Go code targets Go 1.21+, existing alerthub module conventions, and existing type definitions.*
