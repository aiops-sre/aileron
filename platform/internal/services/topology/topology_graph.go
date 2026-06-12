package topology

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// ============================================================================
// INFRA GRAPH — unified node/edge model for the intelligent topology UI
// Nodes: bare_metal | cloudstack_vm | k8s_cluster | k8s_node | k8s_pod
// Edges: hosts | runs_on | k8s_on | pod_on
// Redis cache key: topology:infra_graph:v2, TTL 2 minutes
// ============================================================================

// GraphNode is the React-Flow-compatible node shape.
type GraphNode struct {
	ID       string                 `json:"id"`
	NodeType string                 `json:"node_type"` // bare_metal|cloudstack_vm|k8s_cluster|k8s_node|k8s_pod
	Label    string                 `json:"label"`
	Status   string                 `json:"status"` // running|stopped|degraded|unknown
	Health   string                 `json:"health"` // healthy|degraded|unhealthy|unknown
	Layer    int                    `json:"layer"`  // 0=BM 1=VM 2=K8s cluster 3=K8s node 4=Pod
	ParentID string                 `json:"parent_id,omitempty"`
	Data     map[string]interface{} `json:"data"`
}

// GraphEdge is a directed relationship between two nodes.
type GraphEdge struct {
	ID       string `json:"id"`
	Source   string `json:"source"`
	Target   string `json:"target"`
	EdgeType string `json:"edge_type"` // hosts|runs_on|k8s_on|pod_on
	Label    string `json:"label,omitempty"`
	Animated bool   `json:"animated"`
}

// SourceSyncInfo tracks when a data source last successfully synced.
type SourceSyncInfo struct {
	Name      string    `json:"name"`
	Type      string    `json:"type"` // "cloudstack" | "k8s"
	LastSync  time.Time `json:"last_sync"`
	NodeCount int       `json:"node_count"`
	IsStale   bool      `json:"is_stale,omitempty"`
}

// InfraGraph is the full graph payload served to the frontend.
type InfraGraph struct {
	Nodes           []GraphNode          `json:"nodes"`
	Edges           []GraphEdge          `json:"edges"`
	Stats           InfraGraphStats      `json:"stats"`
	LayerStats      map[string]LayerStat `json:"layer_stats"`
	CachedAt        time.Time            `json:"cached_at"`
	CacheAgeSeconds int64                `json:"cache_age_seconds"`
	// Per-source sync info for the "last updated" display.
	Sources []SourceSyncInfo `json:"sources,omitempty"`
	// Staleness fields — set when CloudStack API is unreachable and last-known data is served.
	IsDataStale bool      `json:"is_data_stale,omitempty"`
	StaleReason string    `json:"stale_reason,omitempty"`
	CSLastSync  time.Time `json:"cs_last_sync,omitempty"`
	// Building is true when the graph is being built for the first time.
	// Clients should poll every few seconds until this is false.
	Building bool `json:"building,omitempty"`
}

// InfraGraphStats aggregates counts for the header.
type InfraGraphStats struct {
	TotalNodes int `json:"total_nodes"`
	TotalEdges int `json:"total_edges"`
}

// LayerStat holds per-layer health breakdown.
type LayerStat struct {
	Count     int `json:"count"`
	Healthy   int `json:"healthy"`
	Degraded  int `json:"degraded"`
	Unhealthy int `json:"unhealthy"`
	Unknown   int `json:"unknown"`
}

// ExpandResult is returned when a user clicks a node to see its children.
type ExpandResult struct {
	ParentID string      `json:"parent_id"`
	Children []GraphNode `json:"children"`
	Edges    []GraphEdge `json:"edges"`
}

// InfraSearchResult is a single search hit with context.
type InfraSearchResult struct {
	Node     GraphNode   `json:"node"`
	Parents  []GraphNode `json:"parents"`
	Children []GraphNode `json:"children"`
}

// InfraSearchResponse wraps search hits.
type InfraSearchResponse struct {
	Query   string              `json:"query"`
	Total   int                 `json:"total"`
	Results []InfraSearchResult `json:"results"`
}

// ============================================================================
// REDIS-BACKED TOPOLOGY GRAPH CACHE
// ============================================================================

// RedisLike is the minimal cache interface we need.
type RedisLike interface {
	Set(key string, value interface{}, ttl time.Duration) error
	Get(key string, dest interface{}) error
}

// TopologyGraphCache manages building, caching, and serving the infrastructure graph.
type TopologyGraphCache struct {
	infraSvc *InfraTopologyService
	redis    RedisLike

	// In-memory fallback
	mu           sync.RWMutex
	memGraph     *InfraGraph
	cachedAt     time.Time
	csLastSyncAt time.Time // when CloudStack data was last successfully fetched

	// rebuilding is 1 while an async cold-start rebuild goroutine is running,
	// preventing duplicate concurrent rebuilds.
	rebuilding int32

	stopChan chan struct{}
}

const (
	infraGraphCacheKey = "topology:infra_graph:v2"
	graphCacheTTL      = 5 * time.Minute
	graphRefreshEvery  = 5 * time.Minute
	// Maximum time to wait for K8s initial discovery before building the first graph.
	k8sWarmupMaxWait = 90 * time.Second
)

// NewTopologyGraphCache creates the cache; call Start to begin background refresh.
func NewTopologyGraphCache(infraSvc *InfraTopologyService, redis RedisLike) *TopologyGraphCache {
	return &TopologyGraphCache{
		infraSvc: infraSvc,
		redis:    redis,
		stopChan: make(chan struct{}),
	}
}

// Start waits for K8s initial discovery to complete (up to 90s), builds the first
// graph, then refreshes every 5 minutes.
func (c *TopologyGraphCache) Start(ctx context.Context) {
	go func() {
		// Wait until the K8s topology service has completed its first discovery pass
		// (i.e. GetCachedResults returns at least one cluster) OR k8sWarmupMaxWait
		// elapses — whichever comes first.  A hardcoded 30s sleep wasn't enough when
		// all clusters run in parallel with a 45s per-cluster timeout.
		if c.infraSvc != nil && c.infraSvc.k8sService != nil {
			deadline := time.Now().Add(k8sWarmupMaxWait)
			for time.Now().Before(deadline) {
				if len(c.infraSvc.k8sService.GetCachedResults()) > 0 {
					break
				}
				select {
				case <-time.After(3 * time.Second):
				case <-ctx.Done():
					return
				case <-c.stopChan:
					return
				}
			}
		}

		c.rebuild(ctx)
		ticker := time.NewTicker(graphRefreshEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.rebuild(ctx)
			case <-c.stopChan:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Stop terminates the background goroutine.
func (c *TopologyGraphCache) Stop() {
	select {
	case <-c.stopChan:
	default:
		close(c.stopChan)
	}
}

// HealthStatus returns a health summary of the topology graph cache.
// Used by the detailed health endpoint to report topology freshness.
func (c *TopologyGraphCache) HealthStatus() map[string]interface{} {
	c.mu.RLock()
	graph := c.memGraph
	cachedAt := c.cachedAt
	c.mu.RUnlock()

	result := map[string]interface{}{
		"last_rebuild": nil,
		"node_count":   0,
		"edge_count":   0,
		"is_stale":     true,
		"stale_reason": "no graph built yet",
		"age_seconds":  int64(-1),
	}

	if graph == nil {
		return result
	}

	ageSeconds := int64(time.Since(cachedAt).Seconds())
	isStale := ageSeconds > int64(graphRefreshEvery.Seconds()*2) || graph.IsDataStale

	staleReason := ""
	if graph.IsDataStale {
		staleReason = graph.StaleReason
	} else if ageSeconds > int64(graphRefreshEvery.Seconds()*2) {
		staleReason = "cache age exceeds 2x refresh interval"
	}

	result["last_rebuild"] = cachedAt.Format(time.RFC3339)
	result["node_count"] = len(graph.Nodes)
	result["edge_count"] = len(graph.Edges)
	result["is_stale"] = isStale
	result["stale_reason"] = staleReason
	result["age_seconds"] = ageSeconds

	return result
}

// rebuild fetches live data, builds the graph, and persists it to Redis + memory.
func (c *TopologyGraphCache) rebuild(ctx context.Context) {
	start := time.Now()
	log.Printf("Infra graph cache: rebuilding...")

	buildCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	graph, err := c.infraSvc.BuildInfraGraph(buildCtx)
	if err != nil {
		log.Printf("Infra graph rebuild failed: %v", err)
		return
	}

	newK8s := countNodeType(graph, "k8s_cluster")
	newBM := countNodeType(graph, "bare_metal")
	newVM := countNodeType(graph, "cloudstack_vm")
	hasCloudStackData := newBM > 0 || newVM > 0

	c.mu.RLock()
	oldGraph := c.memGraph
	c.mu.RUnlock()

	if oldGraph != nil {
		// Guard: K8s cluster count regressed significantly K8s still warming up.
		oldK8s := countNodeType(oldGraph, "k8s_cluster")
		if (oldK8s > 1 && newK8s < oldK8s/2) || (newK8s == 0 && oldK8s > 0) {
			log.Printf("Infra graph rebuild produced only %d K8s clusters (had %d) — K8s discovery still warming up, keeping previous graph", newK8s, oldK8s)
			return
		}

		// Guard: CloudStack data vanished CS API likely unreachable.
		// Keep the old graph and mark it stale rather than showing empty BM/VMs.
		oldBM := countNodeType(oldGraph, "bare_metal")
		oldVM := countNodeType(oldGraph, "cloudstack_vm")
		if (oldBM > 0 || oldVM > 0) && !hasCloudStackData {
			log.Printf("CloudStack returned 0 nodes (had %d BM + %d VMs) — CS API may be unreachable, keeping previous graph as stale", oldBM, oldVM)
			c.mu.Lock()
			if c.memGraph != nil {
				c.memGraph.IsDataStale = true
				c.memGraph.StaleReason = "CloudStack API unreachable — showing last known data"
				c.memGraph.CSLastSync = c.csLastSyncAt
			}
			c.mu.Unlock()
			return
		}
	}

	// Don't write a zero-cluster, zero-CloudStack graph on the first build —
	// K8s service hasn't finished its initial discovery yet.  The Start() loop will retry.
	// But if CloudStack data is present (BM/VMs discovered), write the graph regardless
	// of K8s cluster count so CloudStack topology shows up immediately.
	if newK8s == 0 && !hasCloudStackData && c.infraSvc != nil && c.infraSvc.k8sService != nil {
		log.Printf("Infra graph: 0 K8s clusters and 0 CloudStack nodes on first build — still discovering, will retry")
		return
	}

	// Record CS sync time when we have fresh CloudStack data.
	if hasCloudStackData {
		c.mu.Lock()
		c.csLastSyncAt = time.Now()
		c.mu.Unlock()
	}

	// Stamp graph with staleness metadata before persisting.
	c.mu.RLock()
	csSync := c.csLastSyncAt
	c.mu.RUnlock()
	graph.IsDataStale = false
	graph.StaleReason = ""
	if hasCloudStackData {
		graph.CSLastSync = csSync
	}

	if c.redis != nil {
		if err := c.redis.Set(infraGraphCacheKey, graph, graphCacheTTL+time.Minute); err != nil {
			log.Printf("Failed to store infra graph in Redis: %v", err)
		}
	}

	c.mu.Lock()
	c.memGraph = graph
	c.cachedAt = time.Now()
	c.mu.Unlock()

	log.Printf("Infra graph cache rebuilt: %d nodes, %d edges in %v",
		len(graph.Nodes), len(graph.Edges), time.Since(start).Round(time.Millisecond))
}

// GetGraph returns the cached graph, rebuilding synchronously on cold start.
func (c *TopologyGraphCache) GetGraph(ctx context.Context) (*InfraGraph, error) {
	if c.redis != nil {
		var g InfraGraph
		if err := c.redis.Get(infraGraphCacheKey, &g); err == nil {
			// Skip a stale Redis graph that has 0 K8s clusters when the K8s service
			// already has results — this happens on pod restarts when the first rebuild
			// ran before K8s discovery finished.
			redisClusters := countNodeType(&g, "k8s_cluster")
			k8sReady := c.infraSvc != nil && c.infraSvc.k8sService != nil && len(c.infraSvc.k8sService.GetCachedResults()) > 0
			if redisClusters == 0 && k8sReady {
				log.Printf("Infra graph: skipping Redis cache (0 k8s_cluster nodes, K8s now has results) — rebuilding")
			} else {
				// Seed in-memory cache from Redis so the regression guard in rebuild()
				// can compare against a meaningful baseline even after a fresh pod start.
				c.mu.Lock()
				if c.memGraph == nil {
					c.memGraph = &g
					c.cachedAt = g.CachedAt
				}
				c.mu.Unlock()
				g.CacheAgeSeconds = int64(time.Since(g.CachedAt).Seconds())
				return &g, nil
			}
		}
	}

	c.mu.RLock()
	cached := c.memGraph
	cachedAt := c.cachedAt
	c.mu.RUnlock()

	if cached != nil {
		out := *cached
		out.CacheAgeSeconds = int64(time.Since(cachedAt).Seconds())
		return &out, nil
	}

	log.Printf("Infra graph cache cold — starting async rebuild")
	if atomic.CompareAndSwapInt32(&c.rebuilding, 0, 1) {
		go func() {
			defer atomic.StoreInt32(&c.rebuilding, 0)
			buildCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			c.rebuild(buildCtx)
		}()
	}
	return &InfraGraph{
		Building:   true,
		Nodes:      []GraphNode{},
		Edges:      []GraphEdge{},
		LayerStats: map[string]LayerStat{},
		CachedAt:   time.Now(),
	}, nil
}

// ExpandNode returns all direct children of a node plus the edges to them.
func (c *TopologyGraphCache) ExpandNode(ctx context.Context, nodeID string) (*ExpandResult, error) {
	graph, err := c.GetGraph(ctx)
	if err != nil {
		return nil, err
	}

	nodeByID := make(map[string]GraphNode, len(graph.Nodes))
	for _, n := range graph.Nodes {
		nodeByID[n.ID] = n
	}

	result := &ExpandResult{
		ParentID: nodeID,
		Children: []GraphNode{},
		Edges:    []GraphEdge{},
	}

	for _, e := range graph.Edges {
		if e.Source == nodeID {
			result.Edges = append(result.Edges, e)
			if n, ok := nodeByID[e.Target]; ok {
				result.Children = append(result.Children, n)
			}
		}
	}

	return result, nil
}

// Search does a case-insensitive substring match and returns matching nodes
// with their immediate context (parent chain + first 20 children).
func (c *TopologyGraphCache) Search(ctx context.Context, query string) (*InfraSearchResponse, error) {
	graph, err := c.GetGraph(ctx)
	if err != nil {
		return nil, err
	}

	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return &InfraSearchResponse{Query: query, Total: 0, Results: []InfraSearchResult{}}, nil
	}

	nodeByID := make(map[string]GraphNode, len(graph.Nodes))
	for _, n := range graph.Nodes {
		nodeByID[n.ID] = n
	}

	childrenOf := make(map[string][]string)
	parentOf := make(map[string]string)
	for _, e := range graph.Edges {
		childrenOf[e.Source] = append(childrenOf[e.Source], e.Target)
		if _, exists := parentOf[e.Target]; !exists {
			parentOf[e.Target] = e.Source
		}
	}

	matchNode := func(n GraphNode) bool {
		if strings.Contains(strings.ToLower(n.Label), q) {
			return true
		}
		if strings.Contains(strings.ToLower(n.NodeType), q) {
			return true
		}
		if data, err2 := json.Marshal(n.Data); err2 == nil {
			if strings.Contains(strings.ToLower(string(data)), q) {
				return true
			}
		}
		return false
	}

	var results []InfraSearchResult
	seen := make(map[string]bool)

	for _, n := range graph.Nodes {
		if !matchNode(n) || seen[n.ID] {
			continue
		}
		seen[n.ID] = true

		sr := InfraSearchResult{Node: n, Parents: []GraphNode{}, Children: []GraphNode{}}

		// Walk up 2 levels of parents for context.
		cur := n.ID
		for i := 0; i < 2; i++ {
			pid, ok := parentOf[cur]
			if !ok {
				break
			}
			if p, ok := nodeByID[pid]; ok {
				sr.Parents = append([]GraphNode{p}, sr.Parents...)
			}
			cur = pid
		}

		// Add first 20 children.
		for i, cid := range childrenOf[n.ID] {
			if i >= 20 {
				break
			}
			if child, ok := nodeByID[cid]; ok {
				sr.Children = append(sr.Children, child)
			}
		}

		results = append(results, sr)
		if len(results) >= 50 {
			break
		}
	}

	if results == nil {
		results = []InfraSearchResult{}
	}
	return &InfraSearchResponse{Query: query, Total: len(results), Results: results}, nil
}

// ============================================================================
// GRAPH BUILDER — combines CloudStack + K8s data into the unified graph
// ============================================================================

// BuildInfraGraph assembles the full node/edge graph from all discovery layers.
func (s *InfraTopologyService) BuildInfraGraph(ctx context.Context) (*InfraGraph, error) {
	fullTopo, err := s.ForceRealTopologyDiscovery(ctx, "all")
	if err != nil {
		return nil, err
	}

	nodes := make([]GraphNode, 0, 2000)
	edges := make([]GraphEdge, 0, 4000)

	ls := map[string]*LayerStat{
		"bare_metal":        {},
		"cloudstack_vm":     {},
		"k8s_cluster":       {},
		"k8s_node":          {},
		"k8s_pod":           {},
		"k8s_pvc":           {},
		"k8s_pv":            {},
		"netapp_cluster":    {},
		"netapp_node":       {},
		"netapp_aggregate":  {},
		"netapp_svm":        {},
		"netapp_volume":     {},
		"netapp_s3_bucket":  {},
	}
	inc := func(layer, health string) {
		s := ls[layer]
		if s == nil {
			return
		}
		s.Count++
		switch health {
		case "healthy":
			s.Healthy++
		case "degraded":
			s.Degraded++
		case "unhealthy":
			s.Unhealthy++
		default:
			s.Unknown++
		}
	}

	eid := 0
	edge := func(src, tgt, eType, label string, anim bool) {
		eid++
		edges = append(edges, GraphEdge{
			ID: fmt.Sprintf("e%d", eid), Source: src, Target: tgt,
			EdgeType: eType, Label: label, Animated: anim,
		})
	}

	var sources []SourceSyncInfo
	var k8sSources []SourceSyncInfo

	// Bare Metal 
	bmVMCount := make(map[string]int)
	for _, vm := range fullTopo.Layers[LayerCloudStack] {
		if vm.ParentID != "" {
			bmVMCount[vm.ParentID]++
		}
	}

	for _, bm := range fullTopo.Layers[LayerBareMetal] {
		nodes = append(nodes, GraphNode{
			ID: bm.ID, NodeType: "bare_metal", Label: bm.Name,
			Status: bm.Status, Health: bm.HealthStatus, Layer: 0,
			Data: map[string]interface{}{
				"type": bm.Type, "ip": bm.Properties["ip_address"],
				"cluster": bm.Properties["cluster"], "zone": bm.Properties["zone"],
				"cpu_cores": bm.Properties["cpu_cores"],
				"memory_mb": bm.Properties["memory_total_mb"],
				"vm_count":  bmVMCount[bm.ID], "region": bm.Region,
				"cloudstack_cluster": bm.Properties["cloudstack_cluster"],
			},
		})
		inc("bare_metal", bm.HealthStatus)
	}

	// CloudStack VMs 
	vmIPIndex := make(map[string]string) // ip vmID
	for _, vm := range fullTopo.Layers[LayerCloudStack] {
		nodes = append(nodes, GraphNode{
			ID: vm.ID, NodeType: "cloudstack_vm", Label: vm.Name,
			Status: vm.Status, Health: vm.HealthStatus, Layer: 1, ParentID: vm.ParentID,
			Data: map[string]interface{}{
				"kvm_host": vm.Properties["kvm_host"], "ip": vm.Properties["ip_address"],
				"zone": vm.Properties["zone"], "template": vm.Properties["template"],
				"vcpu": vm.Properties["vcpu"], "memory_mb": vm.Properties["memory_mb"],
				"service_offering": vm.Properties["service_offering"], "region": vm.Region,
				"cloudstack_cluster": vm.Properties["cloudstack_cluster"],
			},
		})
		inc("cloudstack_vm", vm.HealthStatus)
		if vm.ParentID != "" {
			edge(vm.ParentID, vm.ID, "hosts", "Hosts", false)
		}
		if ip, ok := vm.Properties["ip_address"].(string); ok && ip != "" {
			vmIPIndex[ip] = vm.ID
		}
	}

	// Record CloudStack as a data source entry (one per CS cluster if available).
	s.csLastGoodMu.RLock()
	csAt := s.csLastGoodAt
	s.csLastGoodMu.RUnlock()
	if !csAt.IsZero() {
		sources = append(sources, SourceSyncInfo{
			Name:      "CloudStack",
			Type:      "cloudstack",
			LastSync:  csAt,
			NodeCount: len(fullTopo.Layers[LayerCloudStack]),
		})
	}

	// K8s 
	if s.k8sService == nil {
		goto done
	}

	for _, cluster := range s.k8sService.GetCachedResults() {
		clID := "k8s-cluster:" + cluster.ClusterName
		h := "healthy"
		if cluster.Summary.ReadyNodes < cluster.Summary.TotalNodes {
			h = "degraded"
		}
		nodes = append(nodes, GraphNode{
			ID: clID, NodeType: "k8s_cluster", Label: cluster.ClusterName,
			Status: "running", Health: h, Layer: 2,
			Data: map[string]interface{}{
				"cluster":       cluster.ClusterName, "environment": cluster.Environment,
				"region":        cluster.Region, "node_count": cluster.Summary.TotalNodes,
				"pod_count":     cluster.Summary.TotalPods, "ns_count": cluster.Summary.NamespaceCount,
				"ready_nodes":   cluster.Summary.ReadyNodes,
				"discovered_at": cluster.DiscoveredAt,
			},
		})
		inc("k8s_cluster", h)
		k8sSources = append(k8sSources, SourceSyncInfo{
			Name:      cluster.ClusterName,
			Type:      "k8s",
			LastSync:  cluster.DiscoveredAt,
			NodeCount: len(cluster.Nodes),
		})

		for _, kn := range cluster.Nodes {
			knID := "k8s-node:" + cluster.ClusterName + ":" + kn.Name
			knHealth := "healthy"
			if !kn.Ready {
				knHealth = "unhealthy"
			}
			internalIP := kn.Labels["internal-ip"]
			vmID := vmIPIndex[internalIP]

			nodes = append(nodes, GraphNode{
				ID: knID, NodeType: "k8s_node", Label: kn.Name,
				Status: kn.Status, Health: knHealth, Layer: 3, ParentID: clID,
				Data: map[string]interface{}{
					"cluster":     cluster.ClusterName, "version": kn.Version,
					"os":          kn.OS, "roles": kn.Roles, "ready": kn.Ready,
					"schedulable": kn.Schedulable, "internal_ip": internalIP, "vm_id": vmID,
				},
			})
			inc("k8s_node", knHealth)
			edge(clID, knID, "k8s_on", "Node", false)
			if vmID != "" {
				edge(vmID, knID, "runs_on", "Runs K8s", false)
			}

			podCount := 0
			for _, pod := range cluster.Pods {
				if pod.NodeName != kn.Name {
					continue
				}
				if podCount >= 100 {
					break
				}
				podID := "k8s-pod:" + cluster.ClusterName + ":" + pod.Namespace + ":" + pod.Name
				ph := "healthy"
				if pod.Phase != "Running" && pod.Phase != "Succeeded" {
					ph = "degraded"
				}
				if !pod.Ready && pod.Phase == "Running" {
					ph = "unhealthy"
				}
				nodes = append(nodes, GraphNode{
					ID: podID, NodeType: "k8s_pod", Label: pod.Name,
					Status: pod.Phase, Health: ph, Layer: 4, ParentID: knID,
					Data: map[string]interface{}{
						"namespace": pod.Namespace, "node": pod.NodeName,
						"phase":     pod.Phase, "pod_ip": pod.PodIP,
						"host_ip":   pod.HostIP, "ready": pod.Ready,
						"restarts":  pod.Restarts, "cluster": cluster.ClusterName,
					},
				})
				inc("k8s_pod", ph)
				edge(knID, podID, "pod_on", "Pod", false)
				podCount++
			}
		}
	}

done:
	// NetApp storage topology from Neo4j 
	s.mu.RLock()
	neo4jDrv := s.neo4jDriver
	s.mu.RUnlock()

	if neo4jDrv != nil {
		storageNodes, storageEdges, err := queryNetAppGraph(ctx, neo4jDrv)
		if err != nil {
			log.Printf("NetApp graph query failed: %v (continuing)", err)
		} else {
			nodes = append(nodes, storageNodes...)
			edges = append(edges, storageEdges...)
			for _, n := range storageNodes {
				inc(n.NodeType, n.Health) // use actual health, not hardcoded "healthy"
			}
		}
	}

	layerStatsOut := make(map[string]LayerStat, len(ls))
	for k, v := range ls {
		layerStatsOut[k] = *v
	}
	allSources := append(sources, k8sSources...)

	return &InfraGraph{
		Nodes:      nodes,
		Edges:      edges,
		Stats:      InfraGraphStats{TotalNodes: len(nodes), TotalEdges: len(edges)},
		LayerStats: layerStatsOut,
		Sources:    allSources,
		CachedAt:   time.Now(),
	}, nil
}

// countNodeType counts nodes of a given type in a graph.
func countNodeType(g *InfraGraph, nodeType string) int {
	n := 0
	for _, node := range g.Nodes {
		if node.NodeType == nodeType {
			n++
		}
	}
	return n
}

// queryNetAppGraph fetches NetApp nodes + K8s PV/PVC nodes and their relationships
// from Neo4j and returns them as GraphNode / GraphEdge slices for InfraGraph.
func queryNetAppGraph(ctx context.Context, driver neo4j.DriverWithContext) ([]GraphNode, []GraphEdge, error) {
	session := driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	layerByType := map[string]int{
		"netapp_cluster":    5,
		"netapp_node":       6,
		"netapp_aggregate":  7,
		"netapp_svm":        7,
		"netapp_volume":     8,
		"netapp_s3_bucket":  8,
		"k8s_pv":            9,
		"k8s_pvc":           9,
	}

	// Nodes 
	nodeResult, err := session.Run(ctx, `
		MATCH (n:Service)
		WHERE n.type IN ['netapp_cluster','netapp_node','netapp_aggregate','netapp_svm','netapp_volume','netapp_s3_bucket','k8s_pvc','k8s_pv']
		RETURN n.entity_id AS id, n.name AS name, n.type AS ntype,
		       n.region AS region, n.datacenter AS dc, n.cluster AS cluster,
		       n.state AS state, n.model AS model, n.space_total_gb AS total_gb,
		       n.space_used_gb AS used_gb, n.space_percent AS pct,
		       n.volume_type AS vol_type, n.svm AS svm, n.is_pvc AS is_pvc,
		       n.phase AS phase, n.namespace AS ns, n.pvc_uid AS pvc_uid,
		       n.pvc_name AS pvc_name, n.storage_class AS sc,
		       n.bucket_uuid AS bucket_uuid, n.backing_volume AS backing_vol
	`, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("neo4j storage nodes: %w", err)
	}

	var nodes []GraphNode

	for nodeResult.Next(ctx) {
		rec := nodeResult.Record()
		getString := func(key string) string {
			v, _ := rec.Get(key)
			if v == nil {
				return ""
			}
			return fmt.Sprintf("%v", v)
		}
		getInt := func(key string) int64 {
			v, _ := rec.Get(key)
			if v == nil {
				return 0
			}
			switch n := v.(type) {
			case int64:
				return n
			case float64:
				return int64(n)
			}
			return 0
		}

		id := getString("id")
		if id == "" {
			continue
		}
		ntype := getString("ntype")
		layer := layerByType[ntype]

		// Health calculation varies by node type.
		var health string
		switch ntype {
		case "k8s_pvc", "k8s_pv":
			phase := getString("phase")
			switch phase {
			case "Bound", "Available":
				health = "healthy"
			case "Pending", "Released":
				health = "degraded"
			case "Failed":
				health = "unhealthy"
			default:
				if phase == "" {
					health = "healthy"
				} else {
					health = "unknown"
				}
			}
		case "netapp_s3_bucket":
			pct := getInt("pct")
			if pct >= 95 {
				health = "unhealthy"
			} else if pct >= 85 {
				health = "degraded"
			} else {
				health = "healthy"
			}
		default:
			health = "healthy"
			state := getString("state")
			if state != "" && state != "online" && state != "up" && state != "running" {
				health = "degraded"
			}
		}

		status := getString("state")
		if status == "" {
			status = getString("phase")
		}

		nodes = append(nodes, GraphNode{
			ID:       id,
			NodeType: ntype,
			Label:    getString("name"),
			Status:   status,
			Health:   health,
			Layer:    layer,
			Data: map[string]interface{}{
				"cluster":        getString("cluster"),
				"region":         getString("region"),
				"datacenter":     getString("dc"),
				"state":          status,
				"model":          getString("model"),
				"space_total_gb": getInt("total_gb"),
				"space_used_gb":  getInt("used_gb"),
				"space_percent":  getInt("pct"),
				"volume_type":    getString("vol_type"),
				"svm":            getString("svm"),
				"is_pvc":         getString("is_pvc") == "true",
				"phase":          getString("phase"),
				"namespace":      getString("ns"),
				"pvc_uid":        getString("pvc_uid"),
				"pvc_name":       getString("pvc_name"),
				"storage_class":  getString("sc"),
				"bucket_uuid":    getString("bucket_uuid"),
				"backing_volume": getString("backing_vol"),
			},
		})
	}
	if err := nodeResult.Err(); err != nil {
		return nil, nil, fmt.Errorf("neo4j storage nodes iteration: %w", err)
	}

	// Relationships 
	relResult, err := session.Run(ctx, `
		MATCH (a:Service)-[r]->(b:Service)
		WHERE (a.type IN ['netapp_cluster','netapp_node','netapp_aggregate','netapp_svm','netapp_volume','netapp_s3_bucket','k8s_pv','k8s_pvc']
		   AND b.type IN ['netapp_cluster','netapp_node','netapp_aggregate','netapp_svm','netapp_volume','netapp_s3_bucket','k8s_pvc','k8s_pv'])
		RETURN a.entity_id AS src, b.entity_id AS tgt, type(r) AS rel
	`, nil)
	if err != nil {
		return nodes, nil, fmt.Errorf("neo4j storage rels: %w", err)
	}

	relLabelByType := map[string]string{
		"HAS_NODE":          "Node",
		"OWNS_AGGREGATE":    "Owns",
		"HOSTS_VOLUME":      "Hosts",
		"HAS_SVM":           "SVM",
		"HAS_VOLUME":        "Volume",
		"BACKS_PVC":         "Backs PVC",
		"BOUND_TO":          "Bound",
		"HAS_AGGREGATE":     "Aggregate",
		"USES_PVC":          "Uses",
		"HAS_BUCKET":        "Bucket",
		"BACKED_BY_VOLUME":  "Backed by",
	}

	var edges []GraphEdge
	eid := 10000
	for relResult.Next(ctx) {
		rec := relResult.Record()
		src, _ := rec.Get("src")
		tgt, _ := rec.Get("tgt")
		rel, _ := rec.Get("rel")
		if src == nil || tgt == nil || rel == nil {
			continue
		}
		eid++
		relStr := fmt.Sprintf("%v", rel)
		label := relLabelByType[relStr]
		if label == "" {
			label = relStr
		}
		edges = append(edges, GraphEdge{
			ID:       fmt.Sprintf("storage-e%d", eid),
			Source:   fmt.Sprintf("%v", src),
			Target:   fmt.Sprintf("%v", tgt),
			EdgeType: strings.ToLower(relStr),
			Label:    label,
			Animated: relStr == "BACKS_PVC" || relStr == "USES_PVC" || relStr == "HAS_BUCKET",
		})
	}
	if err := relResult.Err(); err != nil {
		log.Printf("Storage relationship iteration error: %v", err)
	}

	log.Printf("   storage graph: %d nodes, %d edges fetched from Neo4j", len(nodes), len(edges))
	return nodes, edges, nil
}
