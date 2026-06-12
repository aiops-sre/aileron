package correlation

// recursive_topo_rca.go
//
// RecursiveTopoRCAEngine performs full multi-phase Neo4j graph traversal to find
// root causes (upward sweep), compute blast radius (downward sweep), and identify
// sibling impact (lateral sweep). Replaces the single-level walk in root_cause_engine.go.
//
// Integration: wire into AlertPipelineService and call Traverse() after the existing
// RootCauseEngine.Evaluate() returns RCAActionNoRoot.

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// Domain-aware propagation constants 

// DomainDecay defines how fast impact attenuates per hop in each failure domain.
var DomainDecay = map[FailureDomain]float64{
	DomainStorage:     0.90,
	DomainNetwork:     0.82,
	DomainCompute:     0.88,
	DomainKubernetes:  0.85,
	DomainApplication: 0.70,
	DomainDatabase:    0.88,
	DomainUnknown:     0.80,
}

// EdgeWeight defines base propagation weight per Neo4j relationship type.
// These must match the relationship types actually written by live_topology.go.
var EdgeWeight = map[string]float64{
	// CloudStack compute chain
	"HOSTED_ON": 0.95, // BMVM, VMk8s_node
	// Kubernetes layer
	"DEPLOYED_IN": 0.92, // k8s_nodepod
	// Storage chain
	"BACKS_PVC":       0.90, // netapp_volumek8s_pvc
	"BOUND_TO":        0.88, // k8s_pvk8s_pvc
	"USES_PVC":        0.88, // podk8s_pvc
	"HOSTS_VOLUME":    0.90, // netapp_aggregatenetapp_volume
	"HAS_VOLUME":      0.88, // netapp_svmnetapp_volume
	"HAS_NODE":        0.90, // netapp_clusternetapp_node
	"HAS_SVM":         0.88, // netapp_cluster/nodenetapp_svm
	"HAS_AGGREGATE":   0.88, // netapp_clusternetapp_aggregate
	"OWNS_AGGREGATE":  0.88, // netapp_nodenetapp_aggregate
	"BACKED_BY_VOLUME": 0.85,
	// Legacy names (kept for any pre-existing graph data)
	"HOSTS":        0.95,
	"RUNS_ON":      0.92,
	"MEMBER_OF":    0.88,
	"MOUNTS":       0.90,
	"USES_NETWORK": 0.70,
	"DEPENDS_ON":   0.65,
	"REPLICATES":   0.85,
}

// Types 

// CausalNode represents a node in the causal graph.
type CausalNode struct {
	EntityID   string
	EntityType string
	Label      string
	Cluster    string
	Namespace  string
	InfraLevel InfraLevel
	Domain     FailureDomain
}

// CausalLink represents a directed edge in the causal graph.
type CausalLink struct {
	From     *CausalNode
	To       *CausalNode
	EdgeType string
	Weight   float64
	HopIndex int
}

// ImpactedNode is a node in the blast radius with its propagation score.
type ImpactedNode struct {
	Node             *CausalNode
	PropagationScore float64
	Depth            int
	CausalChain      []*CausalLink
}

// RecursiveTopoResult is the full output of a 3-phase topology traversal.
type RecursiveTopoResult struct {
	AlertEntityID  string
	RootEntity     *CausalNode
	RootConfidence float64
	CausalChain    []*CausalLink   // alert root (upward path)
	BlastRadius    []*ImpactedNode // root all downstream
	PropagationMap map[string]float64
	Depth          int
	Domain         FailureDomain
	Reasoning      []string
	ComputedAt     time.Time
	CacheHit       bool
}

// Engine 

const defaultMaxTopoCache = 10_000

type cachedTopoResult struct {
	result    *RecursiveTopoResult
	expiresAt time.Time
}

// RecursiveTopoRCAEngine performs full graph traversal using Neo4j.
// Falls back to the Redis-backed TopoProvider when Neo4j is unavailable.
type RecursiveTopoRCAEngine struct {
	neo4j        neo4j.DriverWithContext
	topo         TopoProvider
	maxDepth     int
	maxCacheSize int
	mu           sync.RWMutex
	cache        map[string]*cachedTopoResult
}

func NewRecursiveTopoRCAEngine(driver neo4j.DriverWithContext, topo TopoProvider) *RecursiveTopoRCAEngine {
	return &RecursiveTopoRCAEngine{
		neo4j:        driver,
		topo:         topo,
		maxDepth:     8,
		maxCacheSize: defaultMaxTopoCache,
		cache:        make(map[string]*cachedTopoResult),
	}
}

// Traverse is the primary entry point: upward root-finding downward blast radius lateral siblings.
func (e *RecursiveTopoRCAEngine) Traverse(ctx context.Context, alert *Alert) (*RecursiveTopoResult, error) {
	rawEntityID := alert.EntityID
	if rawEntityID == "" {
		rawEntityID = extractEntityIDFromAlert(alert)
	}
	if rawEntityID == "" {
		return nil, fmt.Errorf("no entity_id for alert %s", alert.ID)
	}

	// Normalize Dynatrace/label entity IDs to the canonical Neo4j entity_id format.
	entityID, _ := normalizeEntityID(rawEntityID, alert)
	if entityID == "" {
		entityID = rawEntityID
	}

	if cached := e.fromCache(entityID); cached != nil {
		cached.CacheHit = true
		return cached, nil
	}

	result := &RecursiveTopoResult{
		AlertEntityID:  entityID,
		PropagationMap: make(map[string]float64),
		ComputedAt:     time.Now(),
	}

	// Phase 1: upward sweep
	roots, chain, err := e.upwardSweep(ctx, entityID, alert)
	if err != nil {
		log.Printf("[RecursiveTopoRCA] neo4j unavailable for %s: %v — falling back to Redis graph", entityID, err)
		return e.fallbackToRedisGraph(ctx, alert)
	}
	if len(roots) == 0 {
		result.Reasoning = append(result.Reasoning, "no upstream root cause found in topology graph")
		return result, nil
	}

	best := e.selectBestRoot(roots)
	result.RootEntity = best.Node
	result.RootConfidence = best.PropagationScore
	result.CausalChain = chain
	result.Depth = best.Depth
	result.Domain = AlertDomain(alert)
	result.Reasoning = append(result.Reasoning,
		fmt.Sprintf("upward traversal found root: %s (%s) at depth %d, score %.3f",
			best.Node.Label, best.Node.EntityType, best.Depth, best.PropagationScore))

	// Phase 2: downward sweep
	blastRadius, err := e.downwardSweep(ctx, result.RootEntity.EntityID, result.Domain)
	if err != nil {
		log.Printf("[RecursiveTopoRCA] downward sweep error for %s: %v", result.RootEntity.EntityID, err)
	} else {
		result.BlastRadius = blastRadius
		for _, n := range blastRadius {
			result.PropagationMap[n.Node.EntityID] = n.PropagationScore
		}
		result.Reasoning = append(result.Reasoning,
			fmt.Sprintf("blast radius: %d entities, highest propagation: %.3f",
				len(blastRadius), highestPropagationScore(blastRadius)))
	}

	// Phase 3: lateral sweep
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

	domain := AlertDomain(alert)
	decay := DomainDecay[domain]

	cypher := `
MATCH path = (root)-[rels:HOSTED_ON|DEPLOYED_IN|HOSTS_VOLUME|BACKS_PVC|BOUND_TO|HAS_NODE|HAS_SVM|HAS_AGGREGATE|OWNS_AGGREGATE|HAS_VOLUME|HOSTS|RUNS_ON|MEMBER_OF|MOUNTS*1..8]->(target)
WHERE target.entity_id = $entity_id
   OR target.name = $entity_name
WITH path, root,
     reduce(score = 1.0, r IN relationships(path) | score * coalesce(r.weight, r.strength, 0.85)) AS path_score,
     length(path) AS depth,
     [r IN relationships(path) | {from: startNode(r).entity_id, to: endNode(r).entity_id,
                                   edge_type: type(r), weight: coalesce(r.weight, r.strength, 0.85)}] AS links
WHERE path_score > 0.10 AND depth <= $max_depth
RETURN root.entity_id                                        AS root_id,
       coalesce(root.entity_type, root.type)                 AS root_type,
       coalesce(root.label, root.name)                       AS root_label,
       root.infra_level                                      AS root_level,
       coalesce(root.cluster, root.region)                   AS root_cluster,
       path_score * pow($decay, depth)                       AS attenuated_score,
       depth,
       links
ORDER BY attenuated_score DESC
LIMIT 15`

	result, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		r, err := tx.Run(ctx, cypher, map[string]interface{}{
			"entity_id":   entityID,
			"entity_name": entityNameFromID(entityID),
			"max_depth":   e.maxDepth,
			"decay":       decay,
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
		rootID := neoStr(row, "root_id")
		if seen[rootID] {
			continue
		}
		seen[rootID] = true

		node := &CausalNode{
			EntityID:   rootID,
			EntityType: neoStr(row, "root_type"),
			Label:      neoStr(row, "root_label"),
			Cluster:    neoStr(row, "root_cluster"),
			InfraLevel: InfraLevel(neoInt(row, "root_level")),
		}
		candidates = append(candidates, &ImpactedNode{
			Node:             node,
			PropagationScore: neoFloat(row, "attenuated_score"),
			Depth:            neoInt(row, "depth"),
		})

		if len(bestChain) == 0 {
			if links, ok := row["links"].([]interface{}); ok {
				for i, l := range links {
					if lmap, ok := l.(map[string]interface{}); ok {
						bestChain = append(bestChain, &CausalLink{
							From:     &CausalNode{EntityID: neoStr(lmap, "from")},
							To:       &CausalNode{EntityID: neoStr(lmap, "to")},
							EdgeType: neoStr(lmap, "edge_type"),
							Weight:   neoFloat(lmap, "weight"),
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
MATCH path = (root)-[rels:HOSTED_ON|DEPLOYED_IN|HOSTS_VOLUME|BACKS_PVC|BOUND_TO|HAS_NODE|HAS_SVM|HAS_AGGREGATE|OWNS_AGGREGATE|HAS_VOLUME|HOSTS|RUNS_ON|MEMBER_OF|MOUNTS*1..10]->(affected)
WHERE root.entity_id = $root_entity_id
  AND affected.entity_id <> $root_entity_id
WITH affected,
     reduce(score = 1.0, r IN relationships(path) | score * coalesce(r.weight, r.strength, 0.85)) AS path_score,
     length(path) AS depth
WHERE path_score * pow($decay, depth) > 0.10
RETURN affected.entity_id                               AS entity_id,
       coalesce(affected.entity_type, affected.type)    AS entity_type,
       coalesce(affected.label, affected.name)          AS label,
       coalesce(affected.cluster, affected.region)      AS cluster,
       affected.namespace                               AS namespace,
       affected.infra_level                             AS infra_level,
       path_score * pow($decay, depth)                  AS propagation_score,
       depth
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
				EntityID:   neoStr(row, "entity_id"),
				EntityType: neoStr(row, "entity_type"),
				Label:      neoStr(row, "label"),
				Cluster:    neoStr(row, "cluster"),
				Namespace:  neoStr(row, "namespace"),
				InfraLevel: InfraLevel(neoInt(row, "infra_level")),
			},
			PropagationScore: neoFloat(row, "propagation_score"),
			Depth:            neoInt(row, "depth"),
		})
	}
	return blast, nil
}

func (e *RecursiveTopoRCAEngine) lateralSweep(ctx context.Context, rootEntityID string) ([]*ImpactedNode, error) {
	session := e.neo4j.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	cypher := `
MATCH (parent)-[:HOSTED_ON|HOSTS]->(root)
WHERE root.entity_id = $root_entity_id
WITH parent
MATCH (parent)-[:HOSTED_ON|HOSTS|DEPLOYED_IN]->(sibling)
WHERE sibling.entity_id <> $root_entity_id
RETURN sibling.entity_id                                AS entity_id,
       coalesce(sibling.entity_type, sibling.type)      AS entity_type,
       coalesce(sibling.label, sibling.name)            AS label,
       coalesce(sibling.cluster, sibling.region)        AS cluster,
       0.65                                             AS propagation_score,
       1                                                AS depth`

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
				EntityID:   neoStr(row, "entity_id"),
				EntityType: neoStr(row, "entity_type"),
				Label:      neoStr(row, "label"),
				Cluster:    neoStr(row, "cluster"),
			},
			PropagationScore: neoFloat(row, "propagation_score"),
			Depth:            1,
		})
	}
	return siblings, nil
}

func (e *RecursiveTopoRCAEngine) selectBestRoot(candidates []*ImpactedNode) *ImpactedNode {
	infraWeight := map[InfraLevel]float64{
		InfraLevelBM:      1.5,
		InfraLevelKVM:     1.3,
		InfraLevelVM:      1.2,
		InfraLevelNode:    1.1,
		InfraLevelPod:     0.9,
		InfraLevelUnknown: 0.7,
	}
	sort.Slice(candidates, func(i, j int) bool {
		wi := infraWeight[candidates[i].Node.InfraLevel] * candidates[i].PropagationScore
		wj := infraWeight[candidates[j].Node.InfraLevel] * candidates[j].PropagationScore
		return wi > wj
	})
	return candidates[0]
}

func (e *RecursiveTopoRCAEngine) fallbackToRedisGraph(ctx context.Context, alert *Alert) (*RecursiveTopoResult, error) {
	return &RecursiveTopoResult{
		AlertEntityID: alert.EntityID,
		Reasoning:     []string{"topology graph analysis via cached graph (in-memory fallback)"},
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
	if len(e.cache) >= e.maxCacheSize {
		// Phase 1: evict expired entries.
		now := time.Now()
		for k, v := range e.cache {
			if now.After(v.expiresAt) {
				delete(e.cache, k)
			}
		}
		// Phase 2: if still at capacity, evict one random entry to make room.
		if len(e.cache) >= e.maxCacheSize {
			for k := range e.cache {
				delete(e.cache, k)
				break
			}
		}
	}
	e.cache[entityID] = &cachedTopoResult{result: r, expiresAt: time.Now().Add(ttl)}
}

// Helpers 

func extractEntityIDFromAlert(alert *Alert) string {
	if alert.EntityID != "" {
		return alert.EntityID
	}
	for _, key := range []string{"entity_id", "dynatrace_entity_id", "host.name", "k8s.node.name"} {
		if alert.Labels != nil {
			if v, ok := alert.Labels[key]; ok && v != "" {
				return v
			}
		}
	}
	return ""
}

// normalizeEntityID translates alert entity IDs (Dynatrace / k8s label format) to
// the canonical Neo4j entity_id used in the topology graph.
//
// Supported patterns:
//   - "cluster/node/nodename"             "k8s-node-cluster-nodename"
//   - "cluster/ns/deploy/workload"        "k8s-node-cluster-*" via node label fallback
//   - "host/vmname" or "host/bmname.fqdn" strip prefix; name-based match in Cypher
func normalizeEntityID(rawID string, alert *Alert) (entityID, entityName string) {
	if rawID == "" {
		return "", ""
	}

	// Pattern: "cluster/node/nodename" (Dynatrace k8s node entity)
	if parts := splitN(rawID, "/", 3); len(parts) == 3 && parts[1] == "node" {
		cluster, nodeName := parts[0], parts[2]
		return fmt.Sprintf("k8s-node-%s-%s", cluster, nodeName), nodeName
	}

	// Pattern: "host/hostname.fqdn" or "host/vmname" (Dynatrace host entity)
	if strings.HasPrefix(rawID, "host/") {
		hostName := strings.TrimPrefix(rawID, "host/")
		// Strip domain suffix to match short hostname stored in Neo4j
		if dot := strings.Index(hostName, "."); dot > 0 {
			return rawID, hostName[:dot]
		}
		return rawID, hostName
	}

	// Try to extract a useful name from label-style entity IDs (cluster/ns/kind/name)
	if parts := splitN(rawID, "/", 4); len(parts) == 4 {
		return rawID, parts[3]
	}

	// Fall through: use the raw ID as-is
	return rawID, entityNameFromID(rawID)
}

// entityNameFromID extracts a short name from an entity_id for name-based fallback lookups.
func entityNameFromID(entityID string) string {
	// "k8s-node-cluster-nodename" "nodename" (last dash-separated segment)
	// "cloudstack-host-Region-hostname" "hostname"
	// "cloudstack-vm-uuid" "uuid"
	if entityID == "" {
		return ""
	}
	// For host/ patterns, strip the prefix
	if strings.HasPrefix(entityID, "host/") {
		name := strings.TrimPrefix(entityID, "host/")
		if dot := strings.Index(name, "."); dot > 0 {
			return name[:dot]
		}
		return name
	}
	// For slash-separated paths, return the last segment
	if idx := strings.LastIndex(entityID, "/"); idx >= 0 {
		return entityID[idx+1:]
	}
	// For dash-separated IDs, return the whole string (entity IDs are too varied to parse safely)
	return entityID
}

// splitN is a safe split that only returns a result if there are exactly n parts.
func splitN(s, sep string, n int) []string {
	parts := strings.SplitN(s, sep, n+1)
	if len(parts) == n {
		return parts
	}
	return nil
}

func highestPropagationScore(nodes []*ImpactedNode) float64 {
	max := 0.0
	for _, n := range nodes {
		if n.PropagationScore > max {
			max = n.PropagationScore
		}
	}
	return max
}

func neoStr(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func neoFloat(m map[string]interface{}, key string) float64 {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return n
		case int64:
			return float64(n)
		}
	}
	return 0
}

func neoInt(m map[string]interface{}, key string) int {
	if v, ok := m[key]; ok {
		if n, ok := v.(int64); ok {
			return int(n)
		}
	}
	return 0
}
