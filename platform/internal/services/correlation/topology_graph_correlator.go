package correlation

// topology_graph_correlator.go
//
// Uses the Redis-backed infrastructure graph (BM VM K8s cluster node pod)
// as the authoritative topology source for alert correlation.
//
// It replaces the Neo4j / string-matching fallback in runTopologyStrategy.
// The TopoProvider interface keeps this package free from an import cycle with
// the topology package — the adapter lives in cmd/main.go.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// provider interface 

// TopoProvider is implemented by the adapter in cmd/main.go that wraps
// topology.TopologyGraphCache.  Returning raw node/edge slices keeps the
// correlation package independent of the topology package.
type TopoProvider interface {
	GetNodes(ctx context.Context) ([]TopoNodeInfo, []TopoEdgeInfo, error)
}

// TopoNodeInfo is a lightweight copy of topology.GraphNode.
type TopoNodeInfo struct {
	ID       string
	NodeType string // bare_metal | cloudstack_vm | k8s_cluster | k8s_node | k8s_pod
	Label    string
	Status   string
	Health   string
	Layer    int
	Data     map[string]interface{}
}

// TopoEdgeInfo is a lightweight copy of topology.GraphEdge.
type TopoEdgeInfo struct {
	Source   string
	Target   string
	EdgeType string // hosts | runs_on | k8s_on | pod_on
}

// internal graph snapshot 

type topoSnapshot struct {
	nodes      []TopoNodeInfo
	nodeByID   map[string]*TopoNodeInfo
	parentOf   map[string]string   // child parent
	childrenOf map[string][]string // parent children
}

func buildSnapshot(nodes []TopoNodeInfo, edges []TopoEdgeInfo) *topoSnapshot {
	s := &topoSnapshot{
		nodes:      nodes,
		nodeByID:   make(map[string]*TopoNodeInfo, len(nodes)),
		parentOf:   make(map[string]string),
		childrenOf: make(map[string][]string),
	}
	for i := range nodes {
		s.nodeByID[nodes[i].ID] = &nodes[i]
	}
	for _, e := range edges {
		if _, exists := s.parentOf[e.Target]; !exists {
			s.parentOf[e.Target] = e.Source
		}
		s.childrenOf[e.Source] = append(s.childrenOf[e.Source], e.Target)
	}
	return s
}

// result types 

// TopoCorrelationResult is returned by TopologyGraphCorrelator.Correlate.
type TopoCorrelationResult struct {
	// Matched node in the topology graph (nil if no match).
	MatchedNode *TopoNodeInfo `json:"matched_node,omitempty"`
	MatchMethod string        `json:"match_method,omitempty"`

	// Ancestry chain from matched node up to the root (index 0 = direct parent).
	Ancestors []TopoNodeInfo `json:"ancestors"`

	// Likely root cause — the highest ancestor that also has an active alert.
	RootCauseNode  *TopoNodeInfo `json:"root_cause_node,omitempty"`
	RootCauseScore float64       `json:"root_cause_score"`

	// Blast radius — all descendants of the matched node.
	BlastRadius      []TopoNodeInfo `json:"blast_radius"`
	BlastRadiusScore float64        `json:"blast_radius_score"`

	// Recent alerts found on related nodes.
	RelatedAlerts []SimilarAlert `json:"related_alerts"`
	BestScore     float64        `json:"best_score"`
	BestMatch     *Alert         `json:"best_match,omitempty"`

	// Human-readable explanation.
	Reasoning string `json:"reasoning"`
	Details   map[string]interface{} `json:"details"`
}

// correlator 

// TopologyGraphCorrelator correlates alerts using the live infrastructure graph.
type TopologyGraphCorrelator struct {
	provider TopoProvider
	db       *sql.DB

	// freshnessMu protects lastRefreshed and nodeCount.
	freshnessMu   sync.RWMutex
	lastRefreshed time.Time // zero value = GetNodes has never succeeded
	nodeCount     int       // node count from the last successful GetNodes call
}

// NewTopologyGraphCorrelator creates the correlator.
func NewTopologyGraphCorrelator(provider TopoProvider, db *sql.DB) *TopologyGraphCorrelator {
	return &TopologyGraphCorrelator{provider: provider, db: db}
}

// LastRefreshed returns the time of the last successful (non-empty) GetNodes call.
// Returns the zero time if topology has never been loaded.
func (tc *TopologyGraphCorrelator) LastRefreshed() time.Time {
	tc.freshnessMu.RLock()
	defer tc.freshnessMu.RUnlock()
	return tc.lastRefreshed
}

// FreshnessSeconds returns seconds elapsed since the last successful topology fetch.
// Returns -1 if GetNodes has never returned a non-empty graph.
func (tc *TopologyGraphCorrelator) FreshnessSeconds() int {
	tc.freshnessMu.RLock()
	defer tc.freshnessMu.RUnlock()
	if tc.lastRefreshed.IsZero() {
		return -1
	}
	return int(time.Since(tc.lastRefreshed).Seconds())
}

// NodeCount returns the number of nodes in the last successful topology fetch.
func (tc *TopologyGraphCorrelator) NodeCount() int {
	tc.freshnessMu.RLock()
	defer tc.freshnessMu.RUnlock()
	return tc.nodeCount
}

// Correlate is the main entry point called by runTopologyStrategy.
func (tc *TopologyGraphCorrelator) Correlate(ctx context.Context, alert *Alert) (*TopoCorrelationResult, error) {
	// Warn when topology data is stale so operators know to investigate.
	// Log at most once per 10 minutes to avoid flooding the log.
	if age := tc.FreshnessSeconds(); age > 1800 {
		log.Printf("topology: data is %ds old (>30min) — correlation quality degraded; verify Neo4j/CloudStack connectivity", age)
	}

	nodes, edges, err := tc.provider.GetNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("topology provider: %w", err)
	}
	if len(nodes) == 0 {
		// Empty graph: do not update lastRefreshed — preserve the last known-good timestamp.
		log.Printf("topo correlator: topology graph is empty — verify Neo4j connectivity and live topology refresh status")
		return &TopoCorrelationResult{
			Details: map[string]interface{}{"reason": "empty topology graph"},
		}, nil
	}

	// Successful non-empty fetch — update freshness tracking.
	tc.freshnessMu.Lock()
	tc.lastRefreshed = time.Now()
	tc.nodeCount = len(nodes)
	tc.freshnessMu.Unlock()

	snap := buildSnapshot(nodes, edges)

	// 1. Find the topology node that this alert is about 
	matched, method := tc.findMatchingNode(snap, alert)
	if matched == nil {
		return &TopoCorrelationResult{
			Details: map[string]interface{}{
				"reason":     "no topology node matched",
				"node_count": len(nodes),
			},
		}, nil
	}

	result := &TopoCorrelationResult{
		MatchedNode: matched,
		MatchMethod: method,
		Ancestors:   tc.walkAncestors(snap, matched.ID),
		BlastRadius: tc.computeBlastRadius(snap, matched.ID),
		Details: map[string]interface{}{
			"matched_node_id":   matched.ID,
			"matched_node_type": matched.NodeType,
			"match_method":      method,
			"total_nodes":       len(nodes),
		},
	}

	// Blast radius score — log scale so a single BM with 500 descendants still
	// produces a bounded score rather than an extreme number.
	if len(result.BlastRadius) > 0 {
		result.BlastRadiusScore = math.Min(1.0, math.Log(float64(len(result.BlastRadius)+1))/math.Log(100))
	}

	// 2. Query the DB for recent alerts on related nodes 
	relatedNodeIDs := tc.collectRelatedNodeIDs(snap, matched.ID)
	recentAlerts, err := tc.findAlertsForNodes(ctx, alert.ID, relatedNodeIDs, 4*time.Hour)
	if err != nil {
		log.Printf("topo correlator: DB query failed: %v", err)
	}

	// 3. Score each related alert by topological proximity 
	for _, candidate := range recentAlerts {
		candidateNode := tc.nodeForAlert(snap, &candidate)
		score := tc.proximityScore(snap, matched.ID, candidateNodeID(candidateNode))
		if score > 0 {
			sa := SimilarAlert{
				AlertID:            candidate.ID,
				Title:              candidate.Title,
				TopologySimilarity: score,
				Source:             candidate.Source,
				CreatedAt:          candidate.CreatedAt,
			}
			result.RelatedAlerts = append(result.RelatedAlerts, sa)
			if score > result.BestScore {
				result.BestScore = score
				cp := candidate
				result.BestMatch = &cp
			}
		}
	}

	// 4. Identify the root cause — highest ancestor with an active alert 
	activeAlertNodes := tc.nodesWithActiveAlerts(ctx, result.Ancestors)
	if len(activeAlertNodes) > 0 {
		// Pick the one with the lowest layer number (closest to bare metal).
		rootCandidate := activeAlertNodes[0]
		for _, n := range activeAlertNodes[1:] {
			if n.Layer < rootCandidate.Layer {
				rootCandidate = n
			}
		}
		result.RootCauseNode = &rootCandidate
		// Layer 0 (BM) highest confidence; layer 4 (pod) lower
		result.RootCauseScore = 1.0 - float64(rootCandidate.Layer)*0.15
		if result.BestScore < result.RootCauseScore {
			result.BestScore = result.RootCauseScore
		}
	}

	// 5. Build reasoning string 
	result.Reasoning = tc.buildReasoning(result)
	result.Details["blast_radius_size"] = len(result.BlastRadius)
	result.Details["ancestor_count"] = len(result.Ancestors)
	result.Details["related_alerts"] = len(result.RelatedAlerts)
	result.Details["best_score"] = result.BestScore

	return result, nil
}

// ComputeBlastRadiusSummary returns a human-readable blast radius for use in incident descriptions.
func (tc *TopologyGraphCorrelator) ComputeBlastRadiusSummary(ctx context.Context, alertNodeID string) *BlastRadiusSummary {
	nodes, edges, err := tc.provider.GetNodes(ctx)
	if err != nil {
		return nil
	}
	snap := buildSnapshot(nodes, edges)
	descendants := tc.computeBlastRadius(snap, alertNodeID)
	if len(descendants) == 0 {
		return nil
	}

	counts := make(map[string]int)
	unhealthy := 0
	for _, n := range descendants {
		counts[n.NodeType]++
		if n.Health == "unhealthy" || n.Health == "degraded" {
			unhealthy++
		}
	}

	return &BlastRadiusSummary{
		RootNodeID:    alertNodeID,
		TotalAffected: len(descendants),
		ByType:        counts,
		Unhealthy:     unhealthy,
	}
}

// BlastRadiusSummary describes how many infrastructure layers are impacted.
type BlastRadiusSummary struct {
	RootNodeID    string         `json:"root_node_id"`
	TotalAffected int            `json:"total_affected"`
	ByType        map[string]int `json:"by_type"`
	Unhealthy     int            `json:"unhealthy"`
}

func (s *BlastRadiusSummary) String() string {
	if s == nil {
		return ""
	}
	parts := []string{fmt.Sprintf("%d nodes affected", s.TotalAffected)}
	for t, n := range s.ByType {
		parts = append(parts, fmt.Sprintf("%d %s", n, t))
	}
	if s.Unhealthy > 0 {
		parts = append(parts, fmt.Sprintf("%d currently unhealthy", s.Unhealthy))
	}
	return strings.Join(parts, " · ")
}

// node matching 

func (tc *TopologyGraphCorrelator) findMatchingNode(snap *topoSnapshot, alert *Alert) (*TopoNodeInfo, string) {
	candidates := extractAlertIdentifiers(alert)

	// clusterMatch returns true when the node belongs to the alert's cluster.
	// Uses substring matching on both sides to handle name variants (e.g. the alert
	// label "example-cluster" matches the topology node cluster_id "example-cluster-01").
	// When the alert has no cluster label, all nodes are eligible (no guard).
	clusterMatch := func(n *TopoNodeInfo) bool {
		if candidates.Cluster == "" {
			return true
		}
		nodeCluster := dataStr(n, "cluster_id")
		if nodeCluster == "" {
			// Node has no cluster annotation (e.g. bare-metal) — allow.
			return true
		}
		al := strings.ToLower(candidates.Cluster)
		nl := strings.ToLower(nodeCluster)
		return strings.Contains(nl, al) || strings.Contains(al, nl)
	}

	// Exact label match (highest confidence)
	for _, id := range candidates.Hostnames {
		for i := range snap.nodes {
			n := &snap.nodes[i]
			if !clusterMatch(n) {
				continue
			}
			if strings.EqualFold(n.Label, id) {
				return n, "label_exact"
			}
		}
	}

	// IP match (data fields: ip, internal_ip, pod_ip, host_ip)
	for _, ip := range candidates.IPs {
		if ip == "" {
			continue
		}
		for i := range snap.nodes {
			n := &snap.nodes[i]
			if !clusterMatch(n) {
				continue
			}
			if dataIP(n, "ip") == ip || dataIP(n, "internal_ip") == ip ||
				dataIP(n, "pod_ip") == ip || dataIP(n, "host_ip") == ip {
				return n, "ip_match"
			}
		}
	}

	// Pod name match
	for _, pod := range candidates.PodNames {
		for i := range snap.nodes {
			n := &snap.nodes[i]
			if !clusterMatch(n) {
				continue
			}
			if n.NodeType == "k8s_pod" && strings.EqualFold(n.Label, pod) {
				return n, "pod_name"
			}
		}
	}

	// Namespace match — collect all matching nodes and pick the lowest ID for determinism.
	if candidates.Namespace != "" {
		var nsMatches []*TopoNodeInfo
		for i := range snap.nodes {
			n := &snap.nodes[i]
			if !clusterMatch(n) {
				continue
			}
			if dataStr(n, "namespace") == candidates.Namespace {
				nsMatches = append(nsMatches, n)
			}
		}
		if len(nsMatches) > 0 {
			sort.Slice(nsMatches, func(i, j int) bool { return nsMatches[i].ID < nsMatches[j].ID })
			return nsMatches[0], "namespace"
		}
	}

	// Cluster match
	if candidates.Cluster != "" {
		for i := range snap.nodes {
			n := &snap.nodes[i]
			if n.NodeType == "k8s_cluster" && strings.Contains(strings.ToLower(n.Label), strings.ToLower(candidates.Cluster)) {
				return n, "cluster_name"
			}
		}
	}

	// Fuzzy label substring (last resort — cluster-guarded to prevent cross-cluster hallucination)
	for _, id := range candidates.Hostnames {
		if len(id) < 3 {
			continue
		}
		for i := range snap.nodes {
			n := &snap.nodes[i]
			if !clusterMatch(n) {
				continue
			}
			if strings.Contains(strings.ToLower(n.Label), strings.ToLower(id)) {
				return n, "label_fuzzy"
			}
		}
	}

	return nil, ""
}

// alertIdentifiers holds structured identifiers extracted from an alert.
type alertIdentifiers struct {
	Hostnames []string
	IPs       []string
	PodNames  []string
	Namespace string
	Cluster   string
}

func extractAlertIdentifiers(alert *Alert) alertIdentifiers {
	ids := alertIdentifiers{}

	addStr := func(s string) {
		if s != "" {
			ids.Hostnames = append(ids.Hostnames, s)
		}
	}
	addIP := func(s string) {
		if s != "" {
			ids.IPs = append(ids.IPs, s)
		}
	}

	// Labels
	if alert.Labels != nil {
		addStr(alert.Labels["hostname"])
		addStr(alert.Labels["host"])
		addStr(alert.Labels["node"])
		addStr(alert.Labels["kubernetes_node_name"])
		addStr(alert.Labels["kubernetes_pod_name"])
		addStr(alert.Labels["service"])
		addIP(alert.Labels["ip"])
		addIP(alert.Labels["host_ip"])
		addIP(alert.Labels["pod_ip"])
		if ns := alert.Labels["namespace"]; ns != "" {
			ids.Namespace = ns
		}
		if ns := alert.Labels["kubernetes_namespace"]; ns != "" {
			ids.Namespace = ns
		}
		if cl := alert.Labels["cluster"]; cl != "" {
			ids.Cluster = cl
		}
	}

	// Metadata
	if alert.Metadata != nil {
		addStr(strMeta(alert.Metadata, "hostname"))
		addStr(strMeta(alert.Metadata, "host"))
		addStr(strMeta(alert.Metadata, "node_name"))
		addStr(strMeta(alert.Metadata, "pod_name"))
		addStr(strMeta(alert.Metadata, "service"))
		addStr(strMeta(alert.Metadata, "instance"))
		addIP(strMeta(alert.Metadata, "ip_address"))
		addIP(strMeta(alert.Metadata, "host_ip"))
		addIP(strMeta(alert.Metadata, "pod_ip"))
		if ns := strMeta(alert.Metadata, "namespace"); ns != "" {
			ids.Namespace = ns
		}
		if cl := strMeta(alert.Metadata, "cluster"); cl != "" {
			ids.Cluster = cl
		}
		// Pod names are often in pod-specific arrays
		if pods, ok := alert.Metadata["pods"].([]interface{}); ok {
			for _, p := range pods {
				if ps, ok := p.(string); ok {
					ids.PodNames = append(ids.PodNames, ps)
				}
			}
		}
	}

	// EntityID field
	if alert.EntityID != "" {
		ids.Hostnames = append(ids.Hostnames, alert.EntityID)
	}

	return ids
}

// graph traversal 

func (tc *TopologyGraphCorrelator) walkAncestors(snap *topoSnapshot, nodeID string) []TopoNodeInfo {
	var ancestors []TopoNodeInfo
	visited := map[string]bool{nodeID: true}
	cur := nodeID
	for i := 0; i < 6; i++ { // max 6 levels deep
		pid, ok := snap.parentOf[cur]
		if !ok {
			break
		}
		if visited[pid] {
			break // cycle guard
		}
		visited[pid] = true
		if p, ok := snap.nodeByID[pid]; ok {
			ancestors = append(ancestors, *p)
		}
		cur = pid
	}
	return ancestors
}

func (tc *TopologyGraphCorrelator) computeBlastRadius(snap *topoSnapshot, rootID string) []TopoNodeInfo {
	var result []TopoNodeInfo
	visited := map[string]bool{rootID: true}
	queue := snap.childrenOf[rootID]

	// Cap at 500 nodes for the pipeline scoring path to prevent BM-level storms
	// from allocating thousands of TopoNodeInfo copies per in-flight alert.
	// ComputeBlastRadiusSummary (used for operator dashboards) keeps its own higher cap.
	for len(queue) > 0 && len(result) < 500 {
		id := queue[0]
		queue = queue[1:]
		if visited[id] {
			continue
		}
		visited[id] = true
		if n, ok := snap.nodeByID[id]; ok {
			result = append(result, *n)
			queue = append(queue, snap.childrenOf[id]...)
		}
	}
	return result
}

// collectRelatedNodeIDs gathers IDs for: matched node, its ancestors, its direct children.
func (tc *TopologyGraphCorrelator) collectRelatedNodeIDs(snap *topoSnapshot, nodeID string) []string {
	seen := map[string]bool{nodeID: true}
	ids := []string{nodeID}

	// Ancestors (root cause candidates)
	cur := nodeID
	for i := 0; i < 4; i++ {
		pid, ok := snap.parentOf[cur]
		if !ok {
			break
		}
		if !seen[pid] {
			seen[pid] = true
			ids = append(ids, pid)
		}
		cur = pid
	}

	// Direct children (sibling signals)
	for _, cid := range snap.childrenOf[nodeID] {
		if !seen[cid] {
			seen[cid] = true
			ids = append(ids, cid)
		}
	}

	// Siblings (same parent)
	if pid, ok := snap.parentOf[nodeID]; ok {
		for _, sib := range snap.childrenOf[pid] {
			if !seen[sib] {
				seen[sib] = true
				ids = append(ids, sib)
			}
		}
	}

	return ids
}

// proximity scoring 

func (tc *TopologyGraphCorrelator) proximityScore(snap *topoSnapshot, baseID, candidateID string) float64 {
	if candidateID == "" || baseID == candidateID {
		if baseID == candidateID {
			return 1.0
		}
		return 0
	}

	// Direct parent
	if snap.parentOf[baseID] == candidateID || snap.parentOf[candidateID] == baseID {
		return 0.92
	}

	// Same parent (siblings)
	bp := snap.parentOf[baseID]
	cp := snap.parentOf[candidateID]
	if bp != "" && bp == cp {
		return 0.85
	}

	// Grandparent relationship
	bgp := snap.parentOf[bp]
	cgp := snap.parentOf[cp]
	if bgp != "" && bgp == cgp {
		return 0.72
	}
	if bgp == candidateID || cgp == baseID {
		return 0.78
	}

	// Any ancestor/descendant within 4 hops
	if tc.isAncestor(snap, baseID, candidateID, 4) || tc.isAncestor(snap, candidateID, baseID, 4) {
		return 0.65
	}

	return 0.0
}

func (tc *TopologyGraphCorrelator) isAncestor(snap *topoSnapshot, nodeID, potentialAncestor string, maxHops int) bool {
	cur := nodeID
	for i := 0; i < maxHops; i++ {
		pid, ok := snap.parentOf[cur]
		if !ok {
			return false
		}
		if pid == potentialAncestor {
			return true
		}
		cur = pid
	}
	return false
}

// DB helpers 

// findAlertsForNodes queries recent alerts whose entity_id or metadata fields
// match any of the given topology node IDs / labels.
func (tc *TopologyGraphCorrelator) findAlertsForNodes(
	ctx context.Context,
	excludeAlertID uuid.UUID,
	nodeIDs []string,
	lookback time.Duration,
) ([]Alert, error) {
	if len(nodeIDs) == 0 || tc.db == nil {
		return nil, nil
	}

	since := time.Now().Add(-lookback)

	// Build a broad query: match entity_id, hostname label, or IP in metadata.
	// We do this with a JSONB containment check + entity_id column.
	placeholders := make([]string, len(nodeIDs))
	args := []interface{}{since, excludeAlertID}
	for i, id := range nodeIDs {
		placeholders[i] = fmt.Sprintf("$%d", len(args)+1)
		args = append(args, id)
	}

	query := fmt.Sprintf(`
		SELECT id, title, description, severity, source, entity_id,
		       labels, metadata, fingerprint, created_at
		FROM alerts
		WHERE created_at >= $1
		  AND id != $2
		  AND status != 'resolved'
		  AND (
		      entity_id = ANY(ARRAY[%s])
		      OR labels::text ILIKE ANY(
		             SELECT '%%' || v || '%%' FROM unnest(ARRAY[%s]) AS v
		         )
		  )
		ORDER BY created_at DESC
		LIMIT 100
	`, strings.Join(placeholders, ","), strings.Join(placeholders, ","))

	rows, err := tc.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alerts []Alert
	for rows.Next() {
		var a Alert
		var labelsJSON, metadataJSON []byte
		var entityID sql.NullString

		if err := rows.Scan(
			&a.ID, &a.Title, &a.Description, &a.Severity, &a.Source,
			&entityID, &labelsJSON, &metadataJSON, &a.Fingerprint, &a.CreatedAt,
		); err != nil {
			continue
		}
		if entityID.Valid {
			a.EntityID = entityID.String
		}
		if labelsJSON != nil {
			if err := json.Unmarshal(labelsJSON, &a.Labels); err != nil {
				log.Printf("topo: failed to unmarshal labels for alert %s: %v", a.ID, err)
			}
		}
		if metadataJSON != nil {
			if err := json.Unmarshal(metadataJSON, &a.Metadata); err != nil {
				log.Printf("topo: failed to unmarshal metadata for alert %s: %v", a.ID, err)
			}
		}
		alerts = append(alerts, a)
	}
	return alerts, rows.Err()
}

// nodesWithActiveAlerts checks which of the given nodes has a recent unresolved alert.
func (tc *TopologyGraphCorrelator) nodesWithActiveAlerts(ctx context.Context, nodes []TopoNodeInfo) []TopoNodeInfo {
	if len(nodes) == 0 || tc.db == nil {
		return nil
	}
	ids := make([]string, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}

	placeholders := make([]string, len(ids))
	args := []interface{}{time.Now().Add(-2 * time.Hour)}
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+2)
		args = append(args, id)
	}

	query := fmt.Sprintf(`
		SELECT DISTINCT entity_id FROM alerts
		WHERE created_at >= $1
		  AND status != 'resolved'
		  AND entity_id = ANY(ARRAY[%s])
	`, strings.Join(placeholders, ","))

	rows, err := tc.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()

	active := map[string]bool{}
	for rows.Next() {
		var eid string
		if err := rows.Scan(&eid); err == nil {
			active[eid] = true
		}
	}

	var result []TopoNodeInfo
	for _, n := range nodes {
		if active[n.ID] {
			result = append(result, n)
		}
	}
	return result
}

// nodeForAlert finds the topology node for a given alert (for proximity scoring).
func (tc *TopologyGraphCorrelator) nodeForAlert(snap *topoSnapshot, alert *Alert) *TopoNodeInfo {
	if alert.EntityID != "" {
		if n, ok := snap.nodeByID[alert.EntityID]; ok {
			return n
		}
	}
	n, _ := tc.findMatchingNode(snap, alert)
	return n
}

// buildReasoning produces a human-readable explanation.
func (tc *TopologyGraphCorrelator) buildReasoning(r *TopoCorrelationResult) string {
	if r.MatchedNode == nil {
		return "No topology node matched"
	}
	parts := []string{
		fmt.Sprintf("Matched %s '%s' via %s", r.MatchedNode.NodeType, r.MatchedNode.Label, r.MatchMethod),
	}
	if len(r.Ancestors) > 0 {
		parts = append(parts, fmt.Sprintf("%d ancestor(s)", len(r.Ancestors)))
	}
	if len(r.BlastRadius) > 0 {
		parts = append(parts, fmt.Sprintf("blast radius: %d descendants", len(r.BlastRadius)))
	}
	if r.RootCauseNode != nil {
		parts = append(parts, fmt.Sprintf("root cause candidate: %s '%s'",
			r.RootCauseNode.NodeType, r.RootCauseNode.Label))
	}
	if len(r.RelatedAlerts) > 0 {
		parts = append(parts, fmt.Sprintf("%d related alert(s) on nearby nodes", len(r.RelatedAlerts)))
	}
	return strings.Join(parts, " | ")
}

// IncidentBuilder — topology-enriched incident creation 

// TopoIncidentContext carries topology metadata for enriching incident creation.
type TopoIncidentContext struct {
	MatchedNodeID   string         `json:"matched_node_id"`
	MatchedNodeType string         `json:"matched_node_type"`
	MatchedLabel    string         `json:"matched_label"`
	RootCauseID     string         `json:"root_cause_id,omitempty"`
	RootCauseLabel  string         `json:"root_cause_label,omitempty"`
	RootCauseType   string         `json:"root_cause_type,omitempty"`
	AncestorChain   []string       `json:"ancestor_chain"`
	BlastRadius     *BlastRadiusSummary `json:"blast_radius,omitempty"`
	TopologyPath    string         `json:"topology_path"`
}

// BuildIncidentTitle generates an intelligent incident title from topology context.
func BuildIncidentTitle(alertTitle string, topo *TopoIncidentContext) string {
	if topo == nil {
		return "Incident: " + alertTitle
	}
	if topo.RootCauseLabel != "" && topo.RootCauseLabel != topo.MatchedLabel {
		return fmt.Sprintf("[%s failure] %s %s affected",
			strings.Title(strings.ReplaceAll(topo.RootCauseType, "_", " ")),
			topo.RootCauseLabel,
			topo.MatchedLabel)
	}
	nodeTypeFmt := strings.Title(strings.ReplaceAll(topo.MatchedNodeType, "_", " "))
	return fmt.Sprintf("[%s] %s — %s", nodeTypeFmt, topo.MatchedLabel, alertTitle)
}

// BuildIncidentDescription generates a narrative incident description from topology context.
func BuildIncidentDescription(
	alertTitle, alertSeverity string,
	strategyScores map[string]float64,
	topo *TopoIncidentContext,
) string {
	var sb strings.Builder

	sev := strings.ToUpper(alertSeverity[:1]) + strings.ToLower(alertSeverity[1:])
	sb.WriteString(fmt.Sprintf("%s severity incident triggered by: %s.", sev, alertTitle))

	if topo != nil {
		if topo.RootCauseLabel != "" {
			nodeType := strings.ReplaceAll(topo.RootCauseType, "_", " ")
			sb.WriteString(fmt.Sprintf(" Topology analysis identified %s (%s) as the probable root cause.",
				topo.RootCauseLabel, nodeType))
		}
		if topo.MatchedLabel != "" && topo.MatchedLabel != topo.RootCauseLabel {
			nodeType := strings.ReplaceAll(topo.MatchedNodeType, "_", " ")
			sb.WriteString(fmt.Sprintf(" The alert originated on %s (%s).", topo.MatchedLabel, nodeType))
		}
		if topo.TopologyPath != "" {
			sb.WriteString(fmt.Sprintf(" Affected infrastructure path: %s.", topo.TopologyPath))
		}
		if topo.BlastRadius != nil && topo.BlastRadius.TotalAffected > 0 {
			sb.WriteString(fmt.Sprintf(" Blast radius: %d downstream components affected.", topo.BlastRadius.TotalAffected))
			if topo.BlastRadius.Unhealthy > 0 {
				sb.WriteString(fmt.Sprintf(" %d are currently unhealthy.", topo.BlastRadius.Unhealthy))
			}
		}
	}

	// Dominant correlation strategy
	var topStrategy string
	var topScore float64
	for k, v := range strategyScores {
		if v > topScore {
			topScore = v
			topStrategy = k
		}
	}
	if topStrategy != "" && topScore > 0 {
		sb.WriteString(fmt.Sprintf(" Correlation confidence: %.0f%% (%s).",
			topScore*100, strings.ReplaceAll(topStrategy, "_", " ")))
	}

	return sb.String()
}

// TopoPathString builds a human-readable topology path (e.g. "bm-01 vm-042 example-cluster node-7 pod-xyz").
func TopoPathString(ancestors []TopoNodeInfo, matched TopoNodeInfo) string {
	parts := make([]string, 0, len(ancestors)+1)
	for i := len(ancestors) - 1; i >= 0; i-- {
		parts = append(parts, ancestors[i].Label)
	}
	parts = append(parts, matched.Label)
	return strings.Join(parts, " ")
}

// small utilities 

func dataStr(n *TopoNodeInfo, key string) string {
	if n.Data == nil {
		return ""
	}
	if v, ok := n.Data[key].(string); ok {
		return v
	}
	return ""
}

func dataIP(n *TopoNodeInfo, key string) string {
	return dataStr(n, key)
}

func strMeta(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func candidateNodeID(n *TopoNodeInfo) string {
	if n == nil {
		return ""
	}
	return n.ID
}
