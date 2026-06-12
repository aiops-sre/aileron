package topology

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	goredis "github.com/redis/go-redis/v9"
	"gopkg.in/yaml.v3"
)

const (
	redisKeyLastRefresh = "topology:last_refresh_ts"
	redisLastRefreshTTL = 15 * time.Minute

	// Legacy combined lock — kept so in-flight locks from old pods are recognised
	// during a rolling deploy; new code uses per-phase keys below.
	redisKeyRefreshLock = "topology:refresh_lock"
	redisRefreshLockTTL = 5 * time.Minute

	// Per-phase distributed lock keys.  TTL must exceed the phase's worst-case
	// duration so a crashed pod doesn't hold the lock forever.
	lockKeyK8s    = "topology:lock:k8s"
	lockTTLK8s    = 4 * time.Minute
	lockKeyCS     = "topology:lock:cloudstack"
	lockTTLCS     = 5 * time.Minute
	lockKeyNetApp = "topology:lock:netapp"
	lockTTLNetApp = 12 * time.Minute
)

// LiveTopologyFetcher refreshes the Neo4j topology graph on a background schedule
// and tracks the last-refresh timestamp in Redis so all replicas stay in sync.
// BMVMK8s-nodePod/Service topology is written to Neo4j; correlation reads
// directly from Neo4j without blocking on a live API call.
type LiveTopologyFetcher struct {
	db          *sql.DB
	neo4jDriver neo4j.DriverWithContext
	httpClient  *http.Client

	mu          sync.RWMutex
	lastRefresh time.Time
	redisClient *goredis.Client // optional — used for cross-replica freshness

	// podID uniquely identifies this replica for distributed lock ownership.
	// Defaults to the OS hostname (pod name in k8s).
	podID string

	// NetApp ONTAP cluster configurations (set via SetNetAppClusters).
	netappClusters []NetAppClusterConfig
	netappUser     string
	netappPassword string
}

// NewLiveTopologyFetcher creates a new fetcher using NEO4J_URL / NEO4J_USERNAME / NEO4J_PASSWORD env-vars.
func NewLiveTopologyFetcher(db *sql.DB, neo4jDriver neo4j.DriverWithContext) *LiveTopologyFetcher {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = fmt.Sprintf("unknown-%d", time.Now().UnixNano())
	}
	return &LiveTopologyFetcher{
		db:    db,
		neo4jDriver: neo4jDriver,
		podID: hostname,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: os.Getenv("INTERNAL_TLS_INSECURE") == "true"}, //nolint:gosec
			},
		},
	}
}

// SetRedisClient wires an optional Redis client for cross-replica freshness tracking.
func (f *LiveTopologyFetcher) SetRedisClient(rc *goredis.Client) {
	f.mu.Lock()
	f.redisClient = rc
	f.mu.Unlock()
}

// IsFresh reports whether a topology refresh happened within maxAge.
// Checks the in-process timestamp first; falls back to Redis for cross-replica awareness.
func (f *LiveTopologyFetcher) IsFresh(maxAge time.Duration) bool {
	f.mu.RLock()
	local := f.lastRefresh
	rc := f.redisClient
	f.mu.RUnlock()

	if !local.IsZero() && time.Since(local) < maxAge {
		return true
	}
	if rc == nil {
		return false
	}
	// Check Redis for a refresh done by another replica.
	ts, err := rc.Get(context.Background(), redisKeyLastRefresh).Int64()
	if err != nil {
		return false
	}
	return time.Since(time.Unix(ts, 0)) < maxAge
}

// acquireNamedLock tries to claim a named distributed lock in Redis.
// Returns (true, releaseFn) if acquired, (false, nil) if another replica holds it.
// When Redis is unavailable the lock is skipped and the caller proceeds anyway.
func (f *LiveTopologyFetcher) acquireNamedLock(ctx context.Context, key string, ttl time.Duration) (bool, func()) {
	f.mu.RLock()
	rc := f.redisClient
	podID := f.podID
	f.mu.RUnlock()

	if rc == nil {
		return true, func() {}
	}

	ok, err := rc.SetNX(ctx, key, podID, ttl).Result()
	if err != nil {
		log.Printf("topology: could not acquire lock %s (Redis error: %v) — proceeding anyway", key, err)
		return true, func() {}
	}
	if !ok {
		return false, nil
	}

	release := func() {
		luaScript := `
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
else
  return 0
end`
		if err := rc.Eval(context.Background(), luaScript, []string{key}, podID).Err(); err != nil {
			log.Printf("topology: failed to release lock %s: %v", key, err)
		}
	}
	return true, release
}

// acquireRefreshLock is the legacy combined-refresh lock, kept for backward
// compatibility during rolling deploys.
func (f *LiveTopologyFetcher) acquireRefreshLock(ctx context.Context) (bool, func()) {
	return f.acquireNamedLock(ctx, redisKeyRefreshLock, redisRefreshLockTTL)
}

// StartBackgroundRefresh starts three independent per-phase goroutines with
// volatility-tuned intervals derived from base:
//
//	K8s        — base (5 min)   pods/PVCs change with every deploy
//	CloudStack — base×3 (15 min) VMs move infrequently
//	NetApp     — base×6 (30 min) volumes/buckets almost never change between provisions
//
// Each phase owns its own distributed Redis lock so a slow NetApp refresh never
// delays K8s updates.  A per-phase startup jitter (interval/20) prevents
// thundering-herd when all replicas restart simultaneously.
func (f *LiveTopologyFetcher) StartBackgroundRefresh(ctx context.Context, base time.Duration) {
	type phase struct {
		name     string
		fn       func(context.Context) error
		interval time.Duration
		timeout  time.Duration
		lockKey  string
		lockTTL  time.Duration
		postHook func() // optional; runs in a goroutine after a successful refresh
	}

	phases := []phase{
		{
			name:     "k8s",
			fn:       f.refreshK8sTopology,
			interval: base,
			timeout:  base - 30*time.Second,
			lockKey:  lockKeyK8s,
			lockTTL:  lockTTLK8s,
			// Sync Neo4j nodes/edges to Postgres after every K8s refresh so the Go
			// RCA engines (RecursiveTopoRCAEngine, ProbabilisticRCAEngine, OIE evidence bus)
			// can traverse the topology without a separate Neo4j connection.
			postHook: func() {
				syncCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
				defer cancel()
				if err := f.syncNeo4jToPostgres(syncCtx); err != nil {
					log.Printf("[topology] Neo4j→Postgres sync failed: %v", err)
				}
			},
		},
		{
			name:     "cloudstack",
			fn:       f.refreshCloudStackTopology,
			interval: base * 3,
			timeout:  3 * time.Minute,
			lockKey:  lockKeyCS,
			lockTTL:  lockTTLCS,
		},
		{
			name:     "netapp",
			fn:       f.refreshNetAppTopology,
			interval: base * 6,
			timeout:  35 * time.Minute,
			lockKey:  lockKeyNetApp,
			lockTTL:  lockTTLNetApp,
			postHook: func() {
				// Wait for Neo4j session pool to settle after the bulk volume writes,
				// then wire BACKS_PVC relationships for Trident RWX subordinate PVCs.
				time.Sleep(30 * time.Second)
				linkCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				if err := f.upsertRWXBacksLinks(linkCtx); err != nil {
					log.Printf("RWX BACKS_PVC reconciliation failed: %v", err)
				}
			},
		},
	}

	for _, p := range phases {
		go f.runPhaseLoop(ctx, p.name, p.fn, p.interval, p.timeout, p.lockKey, p.lockTTL, p.postHook)
	}
}

// runPhaseLoop runs a single topology phase on its own ticker.  A random
// startup jitter (interval/20) staggers replicas without significantly
// delaying initial data population.
func (f *LiveTopologyFetcher) runPhaseLoop(
	ctx context.Context,
	name string,
	fn func(context.Context) error,
	interval, timeout time.Duration,
	lockKey string,
	lockTTL time.Duration,
	postHook func(),
) {
	jitter := time.Duration(rand.Int63n(int64(interval / 20))) //nolint:gosec
	log.Printf("topology/%s: first run in %s (interval: %v, timeout: %v)",
		name, jitter.Round(time.Second), interval, timeout)

	select {
	case <-ctx.Done():
		return
	case <-time.After(jitter):
	}

	run := func() {
		phaseCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		if acquired, release := f.acquireNamedLock(phaseCtx, lockKey, lockTTL); acquired {
			start := time.Now()
			if err := fn(phaseCtx); err != nil {
				log.Printf("topology/%s refresh failed after %s: %v",
					name, time.Since(start).Round(time.Second), err)
			}
			if postHook != nil {
				go postHook()
			}
			f.markRefreshed()
			release()
		} else {
			log.Printf("topology/%s: skipping — another replica holds the lock", name)
		}
	}

	run()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Printf("topology/%s stopped", name)
			return
		case <-ticker.C:
			run()
		}
	}
}

// markRefreshed updates the in-process and Redis last-refresh timestamps.
func (f *LiveTopologyFetcher) markRefreshed() {
	now := time.Now()
	f.mu.Lock()
	f.lastRefresh = now
	rc := f.redisClient
	f.mu.Unlock()

	if rc != nil {
		writeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := rc.Set(writeCtx, redisKeyLastRefresh, now.Unix(), redisLastRefreshTTL).Err(); err != nil {
			log.Printf("topology: failed to write last_refresh_ts to Redis: %v", err)
		}
	}
}

// RefreshTopology runs all three phases sequentially and wires RWX back-links.
// Used for manual / API-triggered refreshes; background loops call individual
// phase functions via runPhaseLoop instead.
func (f *LiveTopologyFetcher) RefreshTopology(ctx context.Context) error {
	if f.neo4jDriver == nil {
		return fmt.Errorf("Neo4j driver not available")
	}

	// Phase 1: CloudStack — BM VM topology
	if err := f.refreshCloudStackTopology(ctx); err != nil {
		log.Printf("CloudStack topology refresh failed: %v (continuing)", err)
	}

	// Phase 2: K8s — VM/node Pod/Service topology
	if err := f.refreshK8sTopology(ctx); err != nil {
		log.Printf("K8s topology refresh failed: %v (continuing)", err)
	}

	// Phase 3: NetApp ONTAP — storage cluster aggregate volume PVC topology
	if err := f.refreshNetAppTopology(ctx); err != nil {
		log.Printf("NetApp topology refresh failed: %v (continuing)", err)
	}

	// Phase 4: Wire BACKS_PVC for Trident RWX subordinate PVCs that share a
	// parent volume — these have no dedicated ONTAP volume of their own.
	// Runs in a background goroutine so the session pool has time to settle
	// after the concurrent NetApp writes in Phase 3.
	go func() {
		time.Sleep(30 * time.Second)
		linkCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := f.upsertRWXBacksLinks(linkCtx); err != nil {
			log.Printf("RWX BACKS_PVC reconciliation failed: %v", err)
		}
	}()

	f.markRefreshed()
	log.Printf("Topology refresh complete at %s (Neo4j updated)", time.Now().Format(time.RFC3339))

	// Sync Neo4j entities → Postgres topology_entities so the Go RCA engines
	// (RecursiveTopoRCAEngine, ProbabilisticRCAEngine) can traverse the topology
	// without a separate Neo4j connection. Runs async so it doesn't block the caller.
	if f.db != nil {
		go func() {
			syncCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			if err := f.syncNeo4jToPostgres(syncCtx); err != nil {
				log.Printf("[topology] Neo4j→Postgres sync failed: %v", err)
			}
		}()
	}
	return nil
}

// syncNeo4jToPostgres replicates key Neo4j nodes and edges into the Postgres
// topology_entities and infrastructure_dependency_edges tables.  The Go RCA
// engines (RecursiveTopoRCAEngine, ProbabilisticRCAEngine) query these tables
// because Neo4j is optional and the Postgres path is always available.
func (f *LiveTopologyFetcher) syncNeo4jToPostgres(ctx context.Context) error {
	if f.neo4jDriver == nil || f.db == nil {
		return nil
	}
	start := time.Now()

	session := f.neo4jDriver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	// Pull all nodes with their key properties.
	nodeResult, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		result, err := tx.Run(ctx, `
			MATCH (n)
			WHERE n.uid IS NOT NULL
			RETURN
				n.uid        AS uid,
				COALESCE(n.entity_type, n.type, labels(n)[0]) AS entity_type,
				COALESCE(n.name, n.uid)                       AS name,
				COALESCE(n.cluster, '')                        AS cluster,
				COALESCE(n.namespace, '')                      AS namespace,
				COALESCE(n.infra_level, '')                    AS infra_level,
				COALESCE(n.source, 'neo4j')                   AS source
			LIMIT 50000
		`, nil)
		if err != nil {
			return nil, err
		}
		return result.Collect(ctx)
	})
	if err != nil {
		return fmt.Errorf("neo4j node read: %w", err)
	}
	nodes, _ := nodeResult.([]*neo4j.Record)

	// Upsert nodes into topology_entities.
	nodeCount := 0
	for _, rec := range nodes {
		uid, _ := rec.Get("uid")
		entType, _ := rec.Get("entity_type")
		name, _ := rec.Get("name")
		cluster, _ := rec.Get("cluster")
		ns, _ := rec.Get("namespace")
		infrLvl, _ := rec.Get("infra_level")
		src, _ := rec.Get("source")

		labelsJSON := fmt.Sprintf(`{"namespace":%q,"infra_level":%q}`,
			fmt.Sprintf("%v", ns), fmt.Sprintf("%v", infrLvl))

		_, err := f.db.ExecContext(ctx, `
			INSERT INTO topology_entities
				(id, external_id, entity_type, name, cluster_name, source,
				 labels, last_synced_at, created_at, updated_at)
			VALUES
				(gen_random_uuid(), $1, $2, $3, $4, $5, $6::jsonb, NOW(), NOW(), NOW())
			ON CONFLICT (source, external_id) DO UPDATE SET
				entity_type    = EXCLUDED.entity_type,
				name           = EXCLUDED.name,
				cluster_name   = EXCLUDED.cluster_name,
				labels         = EXCLUDED.labels,
				last_synced_at = NOW(),
				updated_at     = NOW()
		`,
			fmt.Sprintf("%v", uid),
			fmt.Sprintf("%v", entType),
			fmt.Sprintf("%v", name),
			fmt.Sprintf("%v", cluster),
			fmt.Sprintf("%v", src),
			labelsJSON,
		)
		if err == nil {
			nodeCount++
		}
	}

	// Pull edges and upsert into infrastructure_dependency_edges.
	edgeResult, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		result, err := tx.Run(ctx, `
			MATCH (a)-[r]->(b)
			WHERE a.uid IS NOT NULL AND b.uid IS NOT NULL
			RETURN
				a.uid AS src_id,
				b.uid AS tgt_id,
				type(r) AS edge_type,
				COALESCE(r.weight, 0.85) AS weight,
				COALESCE(a.cluster, '') AS domain
			LIMIT 100000
		`, nil)
		if err != nil {
			return nil, err
		}
		return result.Collect(ctx)
	})
	if err != nil {
		return fmt.Errorf("neo4j edge read: %w", err)
	}
	edges, _ := edgeResult.([]*neo4j.Record)

	edgeCount := 0
	for _, rec := range edges {
		srcID, _ := rec.Get("src_id")
		tgtID, _ := rec.Get("tgt_id")
		edgeType, _ := rec.Get("edge_type")
		weight, _ := rec.Get("weight")
		domain, _ := rec.Get("domain")

		w, _ := weight.(float64)
		_, err := f.db.ExecContext(ctx, `
			INSERT INTO infrastructure_dependency_edges
				(id, source_id, target_id, edge_type, weight, domain, confidence, created_at)
			VALUES
				(gen_random_uuid(), $1, $2, $3, $4, $5, 0.90, NOW())
			ON CONFLICT (source_id, target_id, edge_type) DO UPDATE SET
				weight        = EXCLUDED.weight,
				last_confirmed= NOW()
		`,
			fmt.Sprintf("%v", srcID),
			fmt.Sprintf("%v", tgtID),
			fmt.Sprintf("%v", edgeType),
			w,
			fmt.Sprintf("%v", domain),
		)
		if err == nil {
			edgeCount++
		}
	}

	log.Printf("[topology] Neo4j→Postgres sync: %d nodes, %d edges in %s",
		nodeCount, edgeCount, time.Since(start).Round(time.Millisecond))
	return nil
}

// CloudStack 

type csConfig struct {
	ID        string
	Name      string
	APIURL    string
	APIKey    string
	SecretKey string
	ZoneID    sql.NullString
}

func (f *LiveTopologyFetcher) refreshCloudStackTopology(ctx context.Context) error {
	rows, err := f.db.QueryContext(ctx, `
		SELECT id, name, api_url, api_key, secret_key, zone_id
		FROM cloudstack_configs
		WHERE enabled = true
	`)
	if err != nil {
		return fmt.Errorf("query cloudstack_configs: %w", err)
	}
	defer rows.Close()

	var configs []csConfig
	for rows.Next() {
		var c csConfig
		if err := rows.Scan(&c.ID, &c.Name, &c.APIURL, &c.APIKey, &c.SecretKey, &c.ZoneID); err != nil {
			continue
		}
		configs = append(configs, c)
	}

	if len(configs) == 0 {
		log.Printf("No enabled CloudStack configs found")
		return nil
	}

	for _, cfg := range configs {
		client := NewCloudStackClient(cfg.APIURL, cfg.APIKey, cfg.SecretKey)

		// Get bare metal hosts
		hosts, err := client.ListHosts(ctx)
		if err != nil {
			log.Printf("CloudStack ListHosts failed for %s: %v", cfg.Name, err)
		} else {
			for _, host := range hosts {
				node := cloudStackHostToNode(host, cfg.Name)
				if err := f.upsertNode(ctx, node); err != nil {
					log.Printf("upsertNode BM %s: %v", node.EntityID, err)
				}
			}
		}

		// Get VMs and wire HOSTED_ON relationships to their KVM hosts
		vms, err := client.ListVirtualMachines(ctx)
		if err != nil {
			log.Printf("CloudStack ListVMs failed for %s: %v", cfg.Name, err)
			continue
		}
		for _, vm := range vms {
			vmNode := cloudStackVMToNode(vm, cfg.Name)
			// Derive the VM's primary IP from its first NIC.
			vmIP := ""
			if len(vm.NICs) > 0 {
				vmIP = vm.NICs[0].IPAddress
			}
			if err := f.upsertNodeWithProps(ctx, vmNode, map[string]interface{}{
				"ip_address": vmIP,
				"kvm_host":   vm.HostName,
			}); err != nil {
				log.Printf("upsertNode VM %s: %v", vmNode.EntityID, err)
				continue
			}
			// BM -HOSTED_ON-> VM (parentchild direction, consistent with traversal)
			if vm.HostName != "" {
				hostEntityID := fmt.Sprintf("cloudstack-host-%s-%s", cfg.Name, vm.HostName)
				_ = f.upsertRelationship(ctx, hostEntityID, vmNode.EntityID, "HOSTED_ON", 0.95)
			}
		}
	}

	return nil
}

type topoNode struct {
	EntityID    string
	Name        string
	Type        string
	Environment string
	Region      string
}

func cloudStackHostToNode(host CloudStackHost, region string) topoNode {
	return topoNode{
		EntityID:    fmt.Sprintf("cloudstack-host-%s-%s", region, host.Name),
		Name:        host.Name,
		Type:        "bare_metal",
		Environment: "production",
		Region:      region,
	}
}

func cloudStackVMToNode(vm CloudStackVM, region string) topoNode {
	return topoNode{
		EntityID:    fmt.Sprintf("cloudstack-vm-%s", vm.ID),
		Name:        vm.Name,
		Type:        "vm",
		Environment: "production",
		Region:      region,
	}
}

// Kubernetes 

// Kubernetes 

// minimalKubeconfig is a minimal subset of the kubeconfig format we parse.
type minimalKubeconfig struct {
	Clusters []struct {
		Name    string `yaml:"name"`
		Cluster struct {
			Server                   string `yaml:"server"`
			CertificateAuthorityData string `yaml:"certificate-authority-data"`
		} `yaml:"cluster"`
	} `yaml:"clusters"`
	Users []struct {
		Name string `yaml:"name"`
		User struct {
			Token string `yaml:"token"`
		} `yaml:"user"`
	} `yaml:"users"`
	CurrentContext string `yaml:"current-context"`
	Contexts       []struct {
		Name    string `yaml:"name"`
		Context struct {
			Cluster   string `yaml:"cluster"`
			User      string `yaml:"user"`
			Namespace string `yaml:"namespace"`
		} `yaml:"context"`
	} `yaml:"contexts"`
}

// parsedCluster is the extracted connection info from a kubeconfig.
type parsedCluster struct {
	name      string
	serverURL string
	token     string
	caCert    []byte
}

func parseKubeconfig(data []byte) (*parsedCluster, error) {
	var kc minimalKubeconfig
	if err := yaml.Unmarshal(data, &kc); err != nil {
		return nil, fmt.Errorf("parse kubeconfig: %w", err)
	}

	if len(kc.Clusters) == 0 || len(kc.Users) == 0 {
		return nil, fmt.Errorf("kubeconfig has no clusters or users")
	}

	clusterName := kc.Clusters[0].Name
	server := kc.Clusters[0].Cluster.Server
	token := kc.Users[0].User.Token
	caB64 := kc.Clusters[0].Cluster.CertificateAuthorityData

	var caCert []byte
	if caB64 != "" {
		var err error
		caCert, err = base64.StdEncoding.DecodeString(caB64)
		if err != nil {
			return nil, fmt.Errorf("decode CA cert: %w", err)
		}
	}

	return &parsedCluster{
		name:      clusterName,
		serverURL: server,
		token:     token,
		caCert:    caCert,
	}, nil
}

// buildK8sHTTPClient builds an HTTP client for a cluster, using CA cert if available.
func buildK8sHTTPClient(caCert []byte) *http.Client {
	transport := &http.Transport{}
	if len(caCert) > 0 {
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(caCert)
		transport.TLSClientConfig = &tls.Config{RootCAs: pool}
	} else {
		// No CA cert available; require explicit opt-in via INTERNAL_TLS_INSECURE env var
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: os.Getenv("INTERNAL_TLS_INSECURE") == "true"} //nolint:gosec
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: transport}
}

type k8sClusterConfig struct {
	ID                  string
	Name                string
	Environment         string
	Region              string
	APIServerURL        string
	ServiceAccountToken string
	CACertData          sql.NullString
	Namespace           sql.NullString
}

func (f *LiveTopologyFetcher) refreshK8sTopology(ctx context.Context) error {
	// Priority 1: read kubeconfig files from mounted secret directory.
	kubeconfigsDir := strings.TrimSpace(os.Getenv("KUBECONFIGS_DIR"))
	if kubeconfigsDir == "" {
		kubeconfigsDir = "/etc/kubeconfigs"
	}

	kubeconfigs, err := loadKubeconfigDir(kubeconfigsDir)
	if err == nil && len(kubeconfigs) > 0 {
		log.Printf("K8s topology: using %d kubeconfigs from %s", len(kubeconfigs), kubeconfigsDir)
		// Cap at 4 concurrent cluster goroutines so we don't exhaust the Neo4j session pool.
		sem := make(chan struct{}, 4)
		var wg sync.WaitGroup
		for _, kc := range kubeconfigs {
			wg.Add(1)
			sem <- struct{}{}
			go func(kc *parsedCluster) {
				defer wg.Done()
				defer func() { <-sem }()
				// Each cluster gets its own 8-minute timeout so a slow cluster
				// cannot starve others regardless of parent deadline.
				clusterCtx, cancel := context.WithTimeout(ctx, 8*time.Minute)
				defer cancel()
				if err := f.refreshClusterFromKubeconfig(clusterCtx, kc); err != nil {
					log.Printf("K8s cluster %s topology refresh failed: %v", kc.name, err)
				}
			}(kc)
		}
		wg.Wait()
		return nil
	}

	// Priority 2: fall back to DB-stored configs.
	return f.refreshK8sTopologyFromDB(ctx)
}

// LoadKubeconfigDir reads all .yaml/.yml kubeconfig files from dir and returns parsed clusters.
// Exported so other topology services in this package can reuse the same logic.
func LoadKubeconfigDir(dir string) ([]*parsedCluster, error) {
	return loadKubeconfigDir(dir)
}

func loadKubeconfigDir(dir string) ([]*parsedCluster, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var clusters []*parsedCluster
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			log.Printf("read kubeconfig %s: %v", name, err)
			continue
		}
		cluster, err := parseKubeconfig(data)
		if err != nil {
			log.Printf("parse kubeconfig %s: %v", name, err)
			continue
		}
		// Use filename (without extension) as cluster name if not set or generic
		if cluster.name == "" || cluster.name == "cluster" || cluster.name == "default" || cluster.name == "kubernetes" {
			cluster.name = strings.TrimSuffix(strings.TrimSuffix(name, ".yaml"), ".yml")
		}
		// Skip any kubeconfig that has no token. In-cluster kubeconfigs
		// (kubernetes.default.svc) are also skipped because the same cluster
		// is already represented by its real external-URL entry (e.g. example-cluster.yaml).
		if cluster.token == "" {
			log.Printf("Skipping kubeconfig %s (no token — in-cluster OIDC kubeconfigs create duplicates)", name)
			continue
		}
		clusters = append(clusters, cluster)
	}
	return clusters, nil
}

func (f *LiveTopologyFetcher) refreshClusterFromKubeconfig(ctx context.Context, cluster *parsedCluster) error {
	httpClient := buildK8sHTTPClient(cluster.caCert)

	doRequest := func(path string) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cluster.serverURL, "/")+path, nil)
		if err != nil {
			return nil, err
		}
		if cluster.token != "" {
			req.Header.Set("Authorization", "Bearer "+cluster.token)
		}
		req.Header.Set("Accept", "application/json")
		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("K8s API %s returned %d", path, resp.StatusCode)
		}
		return io.ReadAll(resp.Body)
	}

	return f.populateK8sNodesFromAPI(ctx, cluster.name, doRequest)
}

func (f *LiveTopologyFetcher) refreshK8sTopologyFromDB(ctx context.Context) error {
	rows, err := f.db.QueryContext(ctx, `
		SELECT id, name, environment, region, api_server_url,
		       service_account_token, ca_cert_data, namespace
		FROM k8s_cluster_configs
		WHERE enabled = true
	`)
	if err != nil {
		return fmt.Errorf("query k8s_cluster_configs: %w", err)
	}
	defer rows.Close()

	var clusters []k8sClusterConfig
	for rows.Next() {
		var c k8sClusterConfig
		if err := rows.Scan(&c.ID, &c.Name, &c.Environment, &c.Region,
			&c.APIServerURL, &c.ServiceAccountToken, &c.CACertData, &c.Namespace); err != nil {
			continue
		}
		clusters = append(clusters, c)
	}

	if len(clusters) == 0 {
		log.Printf("No enabled K8s cluster configs found in DB")
		return nil
	}

	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, cluster := range clusters {
		wg.Add(1)
		sem <- struct{}{}
		go func(cluster k8sClusterConfig) {
			defer wg.Done()
			defer func() { <-sem }()
			var caCert []byte
			if cluster.CACertData.Valid && cluster.CACertData.String != "" {
				if decoded, err := base64.StdEncoding.DecodeString(cluster.CACertData.String); err == nil {
					caCert = decoded
				} else {
					caCert = []byte(cluster.CACertData.String)
				}
			}
			httpClient := buildK8sHTTPClient(caCert)
			token := cluster.ServiceAccountToken

			clusterCtx, cancel := context.WithTimeout(ctx, 8*time.Minute)
			defer cancel()

			doRequest := func(path string) ([]byte, error) {
				req, err := http.NewRequestWithContext(clusterCtx, http.MethodGet, strings.TrimRight(cluster.APIServerURL, "/")+path, nil)
				if err != nil {
					return nil, err
				}
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("Accept", "application/json")
				resp, err := httpClient.Do(req)
				if err != nil {
					return nil, err
				}
				defer resp.Body.Close()
				if resp.StatusCode >= 400 {
					return nil, fmt.Errorf("K8s API %s returned %d", path, resp.StatusCode)
				}
				return io.ReadAll(resp.Body)
			}

			if err := f.populateK8sNodesFromAPI(clusterCtx, cluster.Name, doRequest); err != nil {
				log.Printf("K8s cluster %s (DB): %v", cluster.Name, err)
			}
		}(cluster)
	}
	wg.Wait()
	return nil
}

// Neo4j helpers 

// infraLevelForType maps a node type string to a numeric infra layer (0=BM … 9=PVC).
func infraLevelForType(t string) int {
	switch t {
	case "bare_metal":
		return 0
	case "vm":
		return 1
	case "k8s_cluster":
		return 2
	case "k8s_node":
		return 3
	case "k8s_pod", "k8s_service":
		return 4
	case "netapp_cluster":
		return 5
	case "netapp_node":
		return 6
	case "netapp_aggregate", "netapp_svm":
		return 7
	case "netapp_volume", "netapp_s3_bucket", "k8s_pv", "k8s_pvc":
		return 9
	default:
		return -1
	}
}

func (f *LiveTopologyFetcher) upsertNode(ctx context.Context, node topoNode) error {
	session := f.neo4jDriver.NewSession(ctx, neo4j.SessionConfig{})
	defer session.Close(ctx)

	_, err := session.Run(ctx, `
		MERGE (s:Service {entity_id: $entityId})
		SET s.name        = $name,
		    s.type        = $type,
		    s.entity_type = $type,
		    s.label       = $name,
		    s.cluster     = $cluster,
		    s.environment = $environment,
		    s.region      = $region,
		    s.infra_level = $infraLevel,
		    s.last_seen   = datetime()
	`, map[string]interface{}{
		"entityId":    node.EntityID,
		"name":        node.Name,
		"type":        node.Type,
		"cluster":     node.Region,
		"environment": node.Environment,
		"region":      node.Region,
		"infraLevel":  infraLevelForType(node.Type),
	})
	return err
}

func (f *LiveTopologyFetcher) upsertRelationship(ctx context.Context, fromEntityID, toEntityID, relType string, strength float64) error {
	session := f.neo4jDriver.NewSession(ctx, neo4j.SessionConfig{})
	defer session.Close(ctx)

	query := fmt.Sprintf(`
		MATCH (from:Service {entity_id: $fromEntity})
		MATCH (to:Service   {entity_id: $toEntity})
		MERGE (from)-[r:%s]->(to)
		SET r.weight       = $strength,
		    r.strength     = $strength,
		    r.last_updated = datetime()
	`, relType)

	_, err := session.Run(ctx, query, map[string]interface{}{
		"fromEntity": fromEntityID,
		"toEntity":   toEntityID,
		"strength":   strength,
	})
	return err
}

// linkVMToK8sNode finds the CloudStack VM whose ip_address matches the given InternalIP
// and creates a VM -HOSTED_ON-> k8s_node relationship (parentchild direction).
// Silently no-ops if no VM is found — the CloudStack phase may not have run yet.
func (f *LiveTopologyFetcher) linkVMToK8sNode(ctx context.Context, ip, k8sNodeEntityID string) error {
	session := f.neo4jDriver.NewSession(ctx, neo4j.SessionConfig{})
	defer session.Close(ctx)
	_, err := session.Run(ctx, `
		MATCH (vm:Service {type: 'vm', ip_address: $ip})
		MATCH (knode:Service {entity_id: $nodeEntityID})
		MERGE (vm)-[r:HOSTED_ON]->(knode)
		SET r.weight = 0.95, r.strength = 0.95, r.last_updated = datetime()
	`, map[string]interface{}{"ip": ip, "nodeEntityID": k8sNodeEntityID})
	return err
}

// K8s API population 

// populateK8sNodesFromAPI fetches nodes, pods, and services from a K8s cluster
// via the provided doRequest function and upserts them into Neo4j.
func (f *LiveTopologyFetcher) populateK8sNodesFromAPI(ctx context.Context, clusterName string, doRequest func(string) ([]byte, error)) error {
	// Nodes 
	nodesBody, err := doRequest("/api/v1/nodes")
	if err != nil {
		log.Printf("K8s nodes fetch failed for %s: %v", clusterName, err)
	} else {
		var nodeList struct {
			Items []struct {
				Metadata struct {
					Name   string            `json:"name"`
					Labels map[string]string `json:"labels"`
				} `json:"metadata"`
				Status struct {
					Addresses []struct {
						Type    string `json:"type"`
						Address string `json:"address"`
					} `json:"addresses"`
				} `json:"status"`
			} `json:"items"`
		}
		if err := json.Unmarshal(nodesBody, &nodeList); err == nil {
			for _, item := range nodeList.Items {
				nodeEntityID := fmt.Sprintf("k8s-node-%s-%s", clusterName, item.Metadata.Name)
				node := topoNode{
					EntityID:    nodeEntityID,
					Name:        item.Metadata.Name,
					Type:        "k8s_node",
					Environment: "production",
					Region:      clusterName,
				}
				if upsertErr := f.upsertNodeWithProps(ctx, node, map[string]interface{}{
					"cluster": clusterName,
				}); upsertErr != nil {
					log.Printf("upsertNode k8s-node %s: %v", nodeEntityID, upsertErr)
					continue
				}
				// Wire VM -HOSTED_ON-> k8s_node by matching the node's InternalIP to a
				// CloudStack VM's stored ip_address property (parentchild direction).
				for _, addr := range item.Status.Addresses {
					if addr.Type == "InternalIP" && addr.Address != "" {
						_ = f.linkVMToK8sNode(ctx, addr.Address, nodeEntityID)
					}
				}
			}
		}
	}

	// PersistentVolumes / PVCs 
	// Build a namespace/name pvcUID map so pods can link to their PVCs.
	pvcByNsName := make(map[string]string) // key: "namespace/claimName"
	{
		pvBody, err := doRequest("/api/v1/persistentvolumes")
		if err != nil {
			log.Printf("K8s PV fetch failed for %s: %v", clusterName, err)
		} else {
			var pvList struct {
				Items []struct {
					Metadata struct {
						Name string `json:"name"`
					} `json:"metadata"`
					Spec struct {
						StorageClassName string `json:"storageClassName"`
						ClaimRef         *struct {
							Namespace string `json:"namespace"`
							Name      string `json:"name"`
							UID       string `json:"uid"`
						} `json:"claimRef"`
					} `json:"spec"`
					Status struct {
						Phase string `json:"phase"`
					} `json:"status"`
				} `json:"items"`
			}
			if err := json.Unmarshal(pvBody, &pvList); err == nil {
				for _, pv := range pvList.Items {
					pvEntityID := fmt.Sprintf("k8s-pv-%s-%s", clusterName, pv.Metadata.Name)
					pvProps := map[string]interface{}{
						"storage_class": pv.Spec.StorageClassName,
						"phase":         pv.Status.Phase,
						"cluster":       clusterName,
					}
					_ = f.upsertNodeWithProps(ctx, topoNode{
						EntityID:    pvEntityID,
						Name:        pv.Metadata.Name,
						Type:        "k8s_pv",
						Environment: "production",
						Region:      clusterName,
					}, pvProps)

					if pv.Spec.ClaimRef == nil || pv.Spec.ClaimRef.UID == "" {
						continue
					}
					cr := pv.Spec.ClaimRef
					pvcEntityID := "k8s-pvc-" + cr.UID
					pvcProps := map[string]interface{}{
						"pvc_uid":       cr.UID,
						"pvc_name":      cr.Name,
						"namespace":     cr.Namespace,
						"storage_class": pv.Spec.StorageClassName,
						"phase":         pv.Status.Phase,
						"cluster":       clusterName,
					}
					_ = f.upsertNodeWithProps(ctx, topoNode{
						EntityID:    pvcEntityID,
						Name:        cr.Name,
						Type:        "k8s_pvc",
						Environment: "production",
						Region:      clusterName,
					}, pvcProps)

					// pv BOUND_TO> pvc
					_ = f.upsertRelationship(ctx, pvEntityID, pvcEntityID, "BOUND_TO", 1.0)

					pvcByNsName[cr.Namespace+"/"+cr.Name] = cr.UID
				}
				log.Printf("   k8s %s: upserted %d PV/PVC pairs", clusterName, len(pvcByNsName))
			}
		}
	}

	// Pods 
	podsBody, err := doRequest("/api/v1/pods")
	if err != nil {
		log.Printf("K8s pods fetch failed for %s: %v", clusterName, err)
	} else {
		var podList struct {
			Items []struct {
				Metadata struct {
					Name      string            `json:"name"`
					Namespace string            `json:"namespace"`
					Labels    map[string]string `json:"labels"`
				} `json:"metadata"`
				Spec struct {
					NodeName string `json:"nodeName"`
					Volumes  []struct {
						PersistentVolumeClaim *struct {
							ClaimName string `json:"claimName"`
						} `json:"persistentVolumeClaim"`
					} `json:"volumes"`
				} `json:"spec"`
			} `json:"items"`
		}
		if err := json.Unmarshal(podsBody, &podList); err == nil {
			for _, item := range podList.Items {
				podEntityID := fmt.Sprintf("k8s-pod-%s-%s-%s", clusterName, item.Metadata.Namespace, item.Metadata.Name)
				node := topoNode{
					EntityID:    podEntityID,
					Name:        item.Metadata.Name,
					Type:        "k8s_pod",
					Environment: "production",
					Region:      clusterName,
				}
				if upsertErr := f.upsertNodeWithProps(ctx, node, map[string]interface{}{
					"cluster":   clusterName,
					"namespace": item.Metadata.Namespace,
				}); upsertErr != nil {
					log.Printf("upsertNode k8s-pod %s: %v", podEntityID, upsertErr)
					continue
				}
				if item.Spec.NodeName != "" {
					nodeEntityID := fmt.Sprintf("k8s-node-%s-%s", clusterName, item.Spec.NodeName)
					// k8s_node -DEPLOYED_IN-> pod (parentchild, consistent traversal direction)
					_ = f.upsertRelationship(ctx, nodeEntityID, podEntityID, "DEPLOYED_IN", 0.92)
				}
				// Wire pod PVC relationships for any mounted persistent volumes.
				for _, vol := range item.Spec.Volumes {
					if vol.PersistentVolumeClaim == nil || vol.PersistentVolumeClaim.ClaimName == "" {
						continue
					}
					mapKey := item.Metadata.Namespace + "/" + vol.PersistentVolumeClaim.ClaimName
					if pvcUID, ok := pvcByNsName[mapKey]; ok {
						pvcEntityID := "k8s-pvc-" + pvcUID
						_ = f.upsertRelationship(ctx, podEntityID, pvcEntityID, "USES_PVC", 0.95)
					}
				}
			}
		}
	}

	// Services 
	svcBody, err := doRequest("/api/v1/services")
	if err != nil {
		log.Printf("K8s services fetch failed for %s: %v", clusterName, err)
	} else {
		var svcList struct {
			Items []struct {
				Metadata struct {
					Name      string            `json:"name"`
					Namespace string            `json:"namespace"`
					Labels    map[string]string `json:"labels"`
				} `json:"metadata"`
			} `json:"items"`
		}
		if err := json.Unmarshal(svcBody, &svcList); err == nil {
			for _, item := range svcList.Items {
				svcEntityID := fmt.Sprintf("k8s-svc-%s-%s-%s", clusterName, item.Metadata.Namespace, item.Metadata.Name)
				node := topoNode{
					EntityID:    svcEntityID,
					Name:        item.Metadata.Name,
					Type:        "k8s_service",
					Environment: "production",
					Region:      clusterName,
				}
				if upsertErr := f.upsertNode(ctx, node); upsertErr != nil {
					log.Printf("upsertNode k8s-svc %s: %v", svcEntityID, upsertErr)
				}
			}
		}
	}

	// Trident RWX subordinate enrichment 
	// Trident RWX subordinate PVCs have internalName="" in the PV spec because
	// they share the parent's NFS export. Query TridentVolume CRDs to get the
	// real ONTAP volume name and store it so the NetApp phase can link BACKS_PVC.
	enrichTridentRWXSubordinates(ctx, clusterName, doRequest, f)

	// Ingest recent Deployment changes as Neo4j Change nodes.
	_ = f.ingestK8sDeploymentChanges(ctx, clusterName, doRequest)

	// Ingest certificate/secret expiry as topology entities so the OKG endpoint
	// can return them as change candidates and the ontology engine can classify
	// "certificate expiry" / "secret expiry" incidents with proper context.
	_ = f.ingestK8sSecretExpiry(ctx, clusterName, doRequest)

	log.Printf("K8s topology refreshed for cluster %s", clusterName)
	return nil
}

// ingestK8sSecretExpiry writes K8s Secrets nearing expiry as topology_entities
// (entity_type='k8s_secret') so that secret-expiry incidents can be correlated to
// the actual secret and namespace rather than producing blank RCAs.
func (f *LiveTopologyFetcher) ingestK8sSecretExpiry(ctx context.Context, clusterName string, doRequest func(string) ([]byte, error)) error {
	if f.db == nil {
		return nil
	}

	// Query Warning events for secret expiry — DT fires on these events.
	eventsBody, err := doRequest("/api/v1/events?fieldSelector=type=Warning,reason=SecretExpiry&limit=50")
	if err != nil {
		// Try broader field selector in case reason varies
		eventsBody, err = doRequest("/api/v1/events?fieldSelector=type=Warning&limit=100")
		if err != nil {
			return nil
		}
	}

	var eventList struct {
		Items []struct {
			Metadata struct {
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			InvolvedObject struct {
				Kind      string `json:"kind"`
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"involvedObject"`
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"items"`
	}
	if err := json.Unmarshal(eventsBody, &eventList); err != nil {
		return nil
	}

	written := 0
	for _, ev := range eventList.Items {
		// Filter to secret/certificate expiry events
		if ev.InvolvedObject.Kind != "Secret" {
			continue
		}
		if !strings.Contains(strings.ToLower(ev.Reason+ev.Message), "expir") &&
			!strings.Contains(strings.ToLower(ev.Reason+ev.Message), "renew") &&
			!strings.Contains(strings.ToLower(ev.Reason+ev.Message), "certif") {
			continue
		}
		ns := ev.InvolvedObject.Namespace
		if ns == "" {
			ns = ev.Metadata.Namespace
		}
		entityID := fmt.Sprintf("k8s-secret-%s-%s-%s", clusterName, ns, ev.InvolvedObject.Name)
		_, err := f.db.ExecContext(ctx, `
			INSERT INTO topology_entities
				(id, external_id, entity_type, name, cluster_name, source,
				 labels, last_synced_at, created_at, updated_at)
			VALUES
				(gen_random_uuid(), $1, 'k8s_secret', $2, $3, 'kubernetes',
				 $4::jsonb, NOW(), NOW(), NOW())
			ON CONFLICT (source, external_id) DO UPDATE SET
				name           = EXCLUDED.name,
				cluster_name   = EXCLUDED.cluster_name,
				labels         = EXCLUDED.labels,
				last_synced_at = NOW(),
				updated_at     = NOW()
		`,
			entityID,
			fmt.Sprintf("%s/%s", ns, ev.InvolvedObject.Name),
			clusterName,
			fmt.Sprintf(`{"namespace":%q,"reason":%q,"message":%q}`, ns, ev.Reason, ev.Message),
		)
		if err == nil {
			written++
		}
	}
	if written > 0 {
		log.Printf("[topology] K8s secret entities: wrote %d for cluster %s", written, clusterName)
	}
	return nil
}

// ingestK8sDeploymentChanges writes recent K8s Deployment rollout events to Neo4j as
// Change nodes. This provides the change intelligence data that the OIE OKG fetcher
// queries via MATCH (c:Change) — without it, deployment_introduced_regression and
// config_change_regression hypotheses can never activate.
func (f *LiveTopologyFetcher) ingestK8sDeploymentChanges(ctx context.Context, clusterName string, doRequest func(string) ([]byte, error)) error {
	if f.neo4jDriver == nil {
		return nil
	}

	// Fetch events of type "Normal" with reason "ScalingReplicaSet" or "Progressing"
	// across all namespaces — these indicate a new deployment rollout.
	eventsBody, err := doRequest("/api/v1/events?fieldSelector=type=Normal,reason=ScalingReplicaSet&limit=100")
	if err != nil {
		// Events API failure is non-fatal — cluster may restrict this.
		return nil
	}

	var eventList struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			InvolvedObject struct {
				Kind      string `json:"kind"`
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"involvedObject"`
			Reason        string `json:"reason"`
			Message       string `json:"message"`
			LastTimestamp string `json:"lastTimestamp"`
			Source        struct {
				Component string `json:"component"`
			} `json:"source"`
		} `json:"items"`
	}
	if err := json.Unmarshal(eventsBody, &eventList); err != nil {
		return nil
	}

	session := f.neo4jDriver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	written := 0
	for _, ev := range eventList.Items {
		if ev.InvolvedObject.Kind != "ReplicaSet" && ev.InvolvedObject.Kind != "Deployment" {
			continue
		}
		// Derive the deployment name from the ReplicaSet name (strip the trailing hash).
		deployName := ev.InvolvedObject.Name
		if ev.InvolvedObject.Kind == "ReplicaSet" {
			// ReplicaSet names are "<deployment>-<hash>" — strip the last segment.
			parts := strings.Split(deployName, "-")
			if len(parts) > 1 {
				deployName = strings.Join(parts[:len(parts)-1], "-")
			}
		}
		changeUID := fmt.Sprintf("change-%s-%s-%s-%s", clusterName, ev.InvolvedObject.Namespace, deployName, ev.LastTimestamp)
		_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
			return tx.Run(ctx, `
				MERGE (c:Change {uid: $uid})
				ON CREATE SET
					c.uid         = $uid,
					c.timestamp   = $timestamp,
					c.service     = $service,
					c.namespace   = $namespace,
					c.cluster     = $cluster,
					c.author      = $author,
					c.description = $description,
					c.change_type = 'deployment',
					c.source      = 'k8s_events'
				ON MATCH SET
					c.description = $description
			`,
				map[string]interface{}{
					"uid":         changeUID,
					"timestamp":   ev.LastTimestamp,
					"service":     deployName,
					"namespace":   ev.InvolvedObject.Namespace,
					"cluster":     clusterName,
					"author":      ev.Source.Component,
					"description": ev.Message,
				},
			)
		})
		if err == nil {
			written++
		}
	}
	if written > 0 {
		log.Printf("[topology] K8s Change nodes: wrote %d deployment events for cluster %s", written, clusterName)
	}
	return nil
}

// enrichTridentRWXSubordinates fetches TridentVolume CRDs from the cluster and,
// for each subordinate PVC (internalName != PV name), stores the real ONTAP
// volume name as trident_internal_name on the Neo4j PVC node.
func enrichTridentRWXSubordinates(
	ctx context.Context,
	clusterName string,
	doRequest func(string) ([]byte, error),
	f *LiveTopologyFetcher,
) {
	body, err := doRequest("/apis/trident.netapp.io/v1/namespaces/trident/tridentvolumes")
	if err != nil {
		log.Printf("Trident CRDs not accessible on %s (skipping RWX subordinate enrichment): %v", clusterName, err)
		return
	}

	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Config struct {
				InternalName      string `json:"internalName"`
				ShareSourceVolume string `json:"shareSourceVolume"`
				AccessInfo        struct {
					NfsPath string `json:"nfsPath"`
				} `json:"accessInformation"`
			} `json:"config"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		log.Printf("Trident CRD parse on %s: %v", clusterName, err)
		return
	}

	enriched := 0
	for _, item := range list.Items {
		pvName := item.Metadata.Name
		internalName := item.Config.InternalName

		// Derive the parent ONTAP volume name.
		// Normal volumes: internalName == "trident_pvc_<uid_with_underscores>" — skip.
		// Subordinate RWX volumes: internalName is empty; use shareSourceVolume instead.
		if internalName == "" {
			src := item.Config.ShareSourceVolume // e.g. "pvc-ea461db6-..."
			if src == "" {
				continue
			}
			// Convert share source PV name to the Trident ONTAP volume name format.
			internalName = "trident_pvc_" + strings.ReplaceAll(strings.TrimPrefix(src, "pvc-"), "-", "_")
		} else {
			// Skip normal volumes whose internalName matches the expected pattern.
			expectedInternal := "trident_pvc_" + strings.ReplaceAll(strings.TrimPrefix(pvName, "pvc-"), "-", "_")
			if internalName == expectedInternal {
				continue
			}
		}

		// Store the real ONTAP volume name on the PVC node so that
		// upsertRWXBacksLinks (run after NetApp refresh) can wire BACKS_PVC.
		pvcUID := strings.TrimPrefix(pvName, "pvc-")
		if err := f.upsertPVCTridentInternalName(ctx, pvcUID, internalName); err != nil {
			log.Printf("enrichTridentRWX upsert %s: %v", pvName, err)
			continue
		}
		enriched++
	}
	if enriched > 0 {
		log.Printf("   %s: enriched %d RWX subordinate PVCs with trident_internal_name", clusterName, enriched)
	}
}

// upsertPVCTridentInternalName stores the real ONTAP volume name on a k8s_pvc node.
func (f *LiveTopologyFetcher) upsertPVCTridentInternalName(ctx context.Context, pvcUID, internalName string) error {
	session := f.neo4jDriver.NewSession(ctx, neo4j.SessionConfig{})
	defer session.Close(ctx)
	_, err := session.Run(ctx, `
		MATCH (pvc:Service {type: 'k8s_pvc', pvc_uid: $uid})
		SET pvc.trident_internal_name = $internalName
	`, map[string]interface{}{"uid": pvcUID, "internalName": internalName})
	return err
}

// upsertRWXBacksLinks creates BACKS_PVC relationships from NetApp volumes to
// RWX subordinate PVCs that share the volume via Trident's RWX mechanism.
func (f *LiveTopologyFetcher) upsertRWXBacksLinks(ctx context.Context) error {
	session := f.neo4jDriver.NewSession(ctx, neo4j.SessionConfig{})
	defer session.Close(ctx)
	result, err := session.Run(ctx, `
		MATCH (pvc:Service {type: 'k8s_pvc'})
		WHERE pvc.trident_internal_name IS NOT NULL
		MATCH (vol:Service {type: 'netapp_volume', name: pvc.trident_internal_name})
		MERGE (vol)-[r:BACKS_PVC]->(pvc)
		SET r.strength = 0.9, r.via_rwx_share = true, r.last_updated = datetime()
		RETURN count(r) AS linked
	`, nil)
	if err != nil {
		return err
	}
	if result.Next(ctx) {
		if n, _ := result.Record().Get("linked"); n != nil {
			if count, ok := n.(int64); ok && count > 0 {
				log.Printf("   RWX BACKS_PVC: linked %d subordinate PVCs to parent NetApp volumes", count)
			}
		}
	}
	return result.Err()
}
