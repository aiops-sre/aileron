package topology

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// KubernetesTopologyService provides K8s topology discovery using service account configs
type KubernetesTopologyService struct {
	db              *sql.DB
	clusters        map[string]*KubernetesClusterConfig
	clients         map[string]kubernetes.Interface
	mu              sync.RWMutex
	refreshInterval time.Duration
	stopChan        chan struct{}

	// Cached results from last successful background discovery.
	cachedResults   []KubernetesTopologyResult
	cacheMu         sync.RWMutex
	lastCacheUpdate time.Time

	// readOnly is true when Postgres is a streaming standby — skips all writes.
	readOnly bool
}

// KubernetesClusterConfig represents a cluster configuration
type KubernetesClusterConfig struct {
	ID                  uuid.UUID `json:"id"`
	Name                string    `json:"name"`
	Environment         string    `json:"environment"`
	Region              string    `json:"region"`
	APIServerURL        string    `json:"api_server_url"`
	ServiceAccountToken string    `json:"service_account_token"`
	CACertData          string    `json:"ca_cert_data"`
	Namespace           string    `json:"namespace"`
	Enabled             bool      `json:"enabled"`
	LastSync            time.Time `json:"last_sync"`
	SyncStatus          string    `json:"sync_status"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// KubernetesTopologyResult represents discovered topology from K8s
type KubernetesTopologyResult struct {
	ClusterName           string                     `json:"cluster_name"`
	Environment           string                     `json:"environment"`
	Region                string                     `json:"region"`
	Nodes                 []K8sNode                  `json:"nodes"`
	Namespaces            []K8sNamespace             `json:"namespaces"`
	Services              []K8sService               `json:"services"`
	Deployments           []K8sDeployment            `json:"deployments"`
	Pods                  []K8sPod                   `json:"pods"`
	Ingresses             []K8sIngress               `json:"ingresses"`
	PersistentVolumes     []K8sPersistentVolume      `json:"persistent_volumes"`
	PersistentVolumeClaims []K8sPersistentVolumeClaim `json:"persistent_volume_claims"`
	Dependencies          []K8sDependency            `json:"dependencies"`
	DiscoveredAt          time.Time                  `json:"discovered_at"`
	Summary               K8sClusterSummary          `json:"summary"`
}

// K8s resource types
type K8sNodeCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

type K8sNode struct {
	Name        string             `json:"name"`
	Status      string             `json:"status"`
	Roles       []string           `json:"roles"`
	Version     string             `json:"version"`
	OS          string             `json:"os"`
	Labels      map[string]string  `json:"labels"`
	Ready       bool               `json:"ready"`
	Schedulable bool               `json:"schedulable"`
	InternalIP  string             `json:"internal_ip,omitempty"`
	Conditions  []K8sNodeCondition `json:"conditions,omitempty"`
	Capacity    map[string]string  `json:"capacity,omitempty"`
}

type K8sNamespace struct {
	Name      string            `json:"name"`
	Status    string            `json:"status"`
	Labels    map[string]string `json:"labels"`
	CreatedAt time.Time         `json:"created_at"`
}

type K8sService struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Type        string            `json:"type"`
	ClusterIP   string            `json:"cluster_ip"`
	ExternalIPs []string          `json:"external_ips"`
	Ports       []K8sServicePort  `json:"ports"`
	Selector    map[string]string `json:"selector"`
	Labels      map[string]string `json:"labels"`
	CreatedAt   time.Time         `json:"created_at"`
}

type K8sServicePort struct {
	Name       string `json:"name"`
	Port       int32  `json:"port"`
	TargetPort string `json:"target_port"`
	Protocol   string `json:"protocol"`
}

type K8sDeployment struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace"`
	Replicas          int32             `json:"replicas"`
	ReadyReplicas     int32             `json:"ready_replicas"`
	AvailableReplicas int32             `json:"available_replicas"`
	Labels            map[string]string `json:"labels"`
	Selector          map[string]string `json:"selector"`
	Containers        []K8sContainer    `json:"containers"`
	CreatedAt         time.Time         `json:"created_at"`
}

type K8sPod struct {
	Name       string            `json:"name"`
	Namespace  string            `json:"namespace"`
	NodeName   string            `json:"node_name"`
	Phase      string            `json:"phase"`
	PodIP      string            `json:"pod_ip"`
	HostIP     string            `json:"host_ip"`
	Labels     map[string]string `json:"labels"`
	Containers []K8sContainer    `json:"containers"`
	Ready      bool              `json:"ready"`
	Restarts   int32             `json:"restarts"`
	CreatedAt  time.Time         `json:"created_at"`
}

type K8sContainer struct {
	Name      string             `json:"name"`
	Image     string             `json:"image"`
	Ports     []K8sContainerPort `json:"ports"`
	Resources K8sResources       `json:"resources"`
	Ready     bool               `json:"ready"`
}

type K8sContainerPort struct {
	Name          string `json:"name"`
	ContainerPort int32  `json:"container_port"`
	Protocol      string `json:"protocol"`
}

type K8sResources struct {
	Requests map[string]string `json:"requests"`
	Limits   map[string]string `json:"limits"`
}

type K8sIngress struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Rules     []K8sIngressRule  `json:"rules"`
	TLS       []K8sIngressTLS   `json:"tls"`
	Labels    map[string]string `json:"labels"`
	CreatedAt time.Time         `json:"created_at"`
}

type K8sIngressRule struct {
	Host  string           `json:"host"`
	Paths []K8sIngressPath `json:"paths"`
}

type K8sIngressPath struct {
	Path        string `json:"path"`
	PathType    string `json:"path_type"`
	ServiceName string `json:"service_name"`
	ServicePort int32  `json:"service_port"`
}

type K8sIngressTLS struct {
	Hosts      []string `json:"hosts"`
	SecretName string   `json:"secret_name"`
}

type K8sDependency struct {
	SourceType   string `json:"source_type"`
	SourceName   string `json:"source_name"`
	TargetType   string `json:"target_type"`
	TargetName   string `json:"target_name"`
	Relationship string `json:"relationship"`
	Namespace    string `json:"namespace"`
}

type K8sEventObject struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

type K8sEvent struct {
	Type           string         `json:"type"`
	Reason         string         `json:"reason"`
	Message        string         `json:"message"`
	InvolvedObject K8sEventObject `json:"involved_object"`
	LastTimestamp  time.Time      `json:"last_timestamp"`
	FirstTimestamp time.Time      `json:"first_timestamp"`
	Count          int32          `json:"count"`
	Namespace      string         `json:"namespace"`
}

type K8sClusterSummary struct {
	TotalNodes         int     `json:"total_nodes"`
	ReadyNodes         int     `json:"ready_nodes"`
	TotalPods          int     `json:"total_pods"`
	RunningPods        int     `json:"running_pods"`
	TotalServices      int     `json:"total_services"`
	TotalDeployments   int     `json:"total_deployments"`
	HealthyDeployments int     `json:"healthy_deployments"`
	TotalIngresses     int     `json:"total_ingresses"`
	NamespaceCount     int     `json:"namespace_count"`
	HealthScore        float64 `json:"health_score"`
	// Storage metrics
	TotalPVCs      int     `json:"total_pvcs"`
	BoundPVCs      int     `json:"bound_pvcs"`
	PendingPVCs    int     `json:"pending_pvcs"`
	TotalStorageGB float64 `json:"total_storage_gb"`
	StorageHealth  string  `json:"storage_health"` // healthy / warning / critical
}

type K8sPersistentVolume struct {
	Name          string   `json:"name"`
	CapacityGB    float64  `json:"capacity_gb"`
	AccessModes   []string `json:"access_modes"`
	ReclaimPolicy string   `json:"reclaim_policy"`
	Status        string   `json:"status"` // Available/Bound/Released/Failed
	StorageClass  string   `json:"storage_class"`
	ClaimRef      string   `json:"claim_ref"` // "namespace/name" of bound PVC
	VolumeMode    string   `json:"volume_mode"`
}

type K8sPersistentVolumeClaim struct {
	Name         string   `json:"name"`
	Namespace    string   `json:"namespace"`
	Status       string   `json:"status"` // Bound/Pending/Lost
	VolumeName   string   `json:"volume_name"`
	RequestedGB  float64  `json:"requested_gb"`
	CapacityGB   float64  `json:"capacity_gb"` // actual from bound PV
	AccessModes  []string `json:"access_modes"`
	StorageClass string   `json:"storage_class"`
	VolumeMode   string   `json:"volume_mode"`
}

// NewKubernetesTopologyService creates a new K8s topology service
func NewKubernetesTopologyService(db *sql.DB) *KubernetesTopologyService {
	svc := &KubernetesTopologyService{
		db:              db,
		clusters:        make(map[string]*KubernetesClusterConfig),
		clients:         make(map[string]kubernetes.Interface),
		refreshInterval: 5 * time.Minute,
		stopChan:        make(chan struct{}),
	}
	var inRecovery bool
	if err := db.QueryRow(`SELECT pg_is_in_recovery()`).Scan(&inRecovery); err == nil && inRecovery {
		svc.readOnly = true
	}
	return svc
}

// Initialize loads cluster configurations and starts background discovery
func (kts *KubernetesTopologyService) Initialize(ctx context.Context) error {
	log.Printf("Initializing Kubernetes Topology Service")

	// Load cluster configurations from database
	if err := kts.loadClusterConfigurations(ctx); err != nil {
		log.Printf("Failed to load cluster configurations: %v", err)
		return err
	}

	// Initialize K8s clients for each cluster
	if err := kts.initializeClusterClients(); err != nil {
		log.Printf("Failed to initialize cluster clients: %v", err)
		return err
	}

	// Start background discovery — run immediately, then on interval.
	go kts.startBackgroundDiscovery(ctx)

	log.Printf("Kubernetes Topology Service initialized with %d clusters", len(kts.clusters))
	return nil
}

// AddClusterConfiguration adds a new cluster configuration
func (kts *KubernetesTopologyService) AddClusterConfiguration(ctx context.Context, config *KubernetesClusterConfig) error {
	kts.mu.Lock()
	defer kts.mu.Unlock()

	// Validate configuration
	if err := kts.validateClusterConfig(config); err != nil {
		return fmt.Errorf("invalid cluster configuration: %w", err)
	}

	// Test connectivity before adding
	client, err := kts.createKubernetesClient(config)
	if err != nil {
		return fmt.Errorf("failed to create K8s client: %w", err)
	}

	// Test cluster connection
	if err := kts.testClusterConnection(ctx, client); err != nil {
		return fmt.Errorf("cluster connectivity test failed: %w", err)
	}

	// Save to database
	if err := kts.saveClusterConfiguration(ctx, config); err != nil {
		return fmt.Errorf("failed to save cluster configuration: %w", err)
	}

	// Add to active clusters
	kts.clusters[config.Name] = config
	kts.clients[config.Name] = client

	log.Printf("Added K8s cluster configuration: %s", config.Name)
	return nil
}

// RemoveClusterConfiguration removes a cluster configuration
func (kts *KubernetesTopologyService) RemoveClusterConfiguration(ctx context.Context, clusterName string) error {
	kts.mu.Lock()
	defer kts.mu.Unlock()

	if _, exists := kts.clusters[clusterName]; !exists {
		return fmt.Errorf("cluster configuration not found: %s", clusterName)
	}

	// Remove from database
	if err := kts.deleteClusterConfiguration(ctx, clusterName); err != nil {
		return fmt.Errorf("failed to delete cluster configuration: %w", err)
	}

	// Remove from active clusters
	delete(kts.clusters, clusterName)
	delete(kts.clients, clusterName)

	log.Printf("Removed K8s cluster configuration: %s", clusterName)
	return nil
}

// GetCachedResults returns the last successfully discovered topology results immediately.
// Returns nil if no cache is available yet.
func (kts *KubernetesTopologyService) GetCachedResults() []KubernetesTopologyResult {
	kts.cacheMu.RLock()
	defer kts.cacheMu.RUnlock()
	return kts.cachedResults
}

// DiscoverTopology discovers topology for all clusters or a specific one.
// Clusters are queried in parallel with a per-cluster timeout of 15 seconds.
func (kts *KubernetesTopologyService) DiscoverTopology(ctx context.Context, clusterName string) ([]KubernetesTopologyResult, error) {
	// If a specific cluster is requested but not in memory, try loading from DB first.
	if clusterName != "" {
		kts.mu.RLock()
		_, inMemory := kts.clusters[clusterName]
		kts.mu.RUnlock()
		if !inMemory && kts.db != nil {
			dbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			kts.syncNewClustersFromDB(dbCtx)
			cancel()
		}
	}

	kts.mu.RLock()
	defer kts.mu.RUnlock()

	var targetClusters map[string]*KubernetesClusterConfig

	if clusterName != "" {
		if cluster, exists := kts.clusters[clusterName]; exists && cluster.Enabled {
			targetClusters = map[string]*KubernetesClusterConfig{clusterName: cluster}
		} else {
			return nil, fmt.Errorf("cluster not found or disabled: %s", clusterName)
		}
	} else {
		targetClusters = make(map[string]*KubernetesClusterConfig)
		for name, cluster := range kts.clusters {
			if cluster.Enabled {
				targetClusters[name] = cluster
			}
		}
	}

	type clusterResult struct {
		result *KubernetesTopologyResult
		name   string
		err    error
	}

	resultCh := make(chan clusterResult, len(targetClusters))
	var wg sync.WaitGroup

	for name, cluster := range targetClusters {
		client, exists := kts.clients[name]
		if !exists {
			log.Printf("No client available for cluster: %s", name)
			continue
		}

		wg.Add(1)
		go func(clName string, cl *KubernetesClusterConfig, c kubernetes.Interface) {
			defer wg.Done()
			// Per-cluster context with a hard timeout so a slow/unreachable cluster
			// doesn't block the whole discovery.
			clCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
			defer cancel()

			r, err := kts.discoverClusterTopology(clCtx, cl, c)
			resultCh <- clusterResult{result: r, name: clName, err: err}
		}(name, cluster, client)
	}

	// Close channel after all goroutines finish.
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	var results []KubernetesTopologyResult
	for cr := range resultCh {
		if cr.err != nil {
			log.Printf("K8s cluster %s discovery failed: %v", cr.name, cr.err)
			kts.updateClusterSyncStatus(ctx, cr.name, "error", cr.err.Error())
			continue
		}
		if cr.result.Summary.TotalNodes > 0 || cr.result.Summary.TotalPods > 0 {
			log.Printf("K8s cluster %s: %d nodes, %d pods, %d namespaces",
				cr.name, cr.result.Summary.TotalNodes, cr.result.Summary.TotalPods, cr.result.Summary.NamespaceCount)
		} else {
			log.Printf("K8s cluster %s: 0 nodes / 0 pods (check SA permissions)", cr.name)
		}
		results = append(results, *cr.result)
		kts.updateClusterSyncStatus(ctx, cr.name, "success", "")
	}

	// Update cache: merge with existing results so a temporarily unreachable cluster
	// keeps its stale data rather than disappearing from the topology page.
	if len(results) > 0 {
		kts.cacheMu.Lock()
		merged := make(map[string]KubernetesTopologyResult, len(kts.cachedResults)+len(results))
		for _, r := range kts.cachedResults {
			merged[r.ClusterName] = r
		}
		for _, r := range results {
			merged[r.ClusterName] = r // overwrite with fresh data
		}
		kts.cachedResults = make([]KubernetesTopologyResult, 0, len(merged))
		for _, r := range merged {
			kts.cachedResults = append(kts.cachedResults, r)
		}
		kts.lastCacheUpdate = time.Now()
		kts.cacheMu.Unlock()
	}

	return results, nil
}

// GetClusterConfigurations returns all cluster configurations, including any
// clusters added to the DB after the service was initialized.
func (kts *KubernetesTopologyService) GetClusterConfigurations() []*KubernetesClusterConfig {
	if kts.db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		kts.syncNewClustersFromDB(ctx)
	}

	kts.mu.RLock()
	defer kts.mu.RUnlock()

	configs := make([]*KubernetesClusterConfig, 0, len(kts.clusters))
	for _, cluster := range kts.clusters {
		configs = append(configs, cluster)
	}
	return configs
}

// syncNewClustersFromDB loads any clusters present in the DB that are not yet
// in the in-memory map (e.g. clusters added via the admin UI after startup).
func (kts *KubernetesTopologyService) syncNewClustersFromDB(ctx context.Context) {
	query := `
		SELECT id, name, environment, region, api_server_url, service_account_token,
		       ca_cert_data, namespace, enabled, last_sync, sync_status, created_at, updated_at
		FROM k8s_cluster_configs
		WHERE enabled = true
		ORDER BY name
	`
	rows, err := kts.db.QueryContext(ctx, query)
	if err != nil {
		return
	}
	defer rows.Close()

	// Build URL index from existing in-memory clusters to detect server-level duplicates
	// (e.g. secret has cluster named "default" and DB has the same server as "example-cluster").
	kts.mu.RLock()
	existingURLs := make(map[string]string, len(kts.clusters))
	for n, cfg := range kts.clusters {
		if u := strings.TrimRight(cfg.APIServerURL, "/"); u != "" {
			existingURLs[u] = n
		}
	}
	kts.mu.RUnlock()

	type row struct {
		cfg *KubernetesClusterConfig
	}
	var newRows []row

	for rows.Next() {
		config := &KubernetesClusterConfig{}
		var lastSyncNull sql.NullTime
		if err := rows.Scan(
			&config.ID, &config.Name, &config.Environment, &config.Region,
			&config.APIServerURL, &config.ServiceAccountToken, &config.CACertData,
			&config.Namespace, &config.Enabled, &lastSyncNull, &config.SyncStatus,
			&config.CreatedAt, &config.UpdatedAt,
		); err != nil {
			continue
		}
		if lastSyncNull.Valid {
			config.LastSync = lastSyncNull.Time
		}

		kts.mu.RLock()
		_, nameExists := kts.clusters[config.Name]
		kts.mu.RUnlock()
		if nameExists {
			continue
		}

		// Skip if another in-memory cluster already uses the same API server URL.
		normalizedURL := strings.TrimRight(config.APIServerURL, "/")
		if existing, urlExists := existingURLs[normalizedURL]; urlExists {
			log.Printf("K8s topology: skipping DB cluster %s — same API server as %s", config.Name, existing)
			continue
		}

		newRows = append(newRows, row{cfg: config})
	}

	for _, r := range newRows {
		client, clientErr := kts.createKubernetesClient(r.cfg)
		kts.mu.Lock()
		kts.clusters[r.cfg.Name] = r.cfg
		if clientErr == nil {
			kts.clients[r.cfg.Name] = client
		}
		kts.mu.Unlock()
		log.Printf("K8s topology: loaded new cluster from DB: %s", r.cfg.Name)
	}
}

// Private methods

func (kts *KubernetesTopologyService) loadClusterConfigurations(ctx context.Context) error {
	// Priority 1: kubeconfig files mounted from the k8s-topology-builder-kubeconfigs secret.
	kubeconfigsDir := strings.TrimSpace(os.Getenv("KUBECONFIGS_DIR"))
	if kubeconfigsDir == "" {
		kubeconfigsDir = "/etc/kubeconfigs"
	}
	kubeconfigs, err := LoadKubeconfigDir(kubeconfigsDir)
	if err == nil && len(kubeconfigs) > 0 {
		log.Printf("K8s topology service: loading %d clusters from %s", len(kubeconfigs), kubeconfigsDir)
		for _, kc := range kubeconfigs {
			caCertStr := ""
			if len(kc.caCert) > 0 {
				caCertStr = base64.StdEncoding.EncodeToString(kc.caCert)
			}
			cfg := &KubernetesClusterConfig{
				ID:                  uuid.New(),
				Name:                kc.name,
				Environment:         "production",
				Region:              "reno",
				APIServerURL:        kc.serverURL,
				ServiceAccountToken: kc.token,
				CACertData:          caCertStr,
				Enabled:             true,
				SyncStatus:          "pending",
				CreatedAt:           time.Now(),
				UpdatedAt:           time.Now(),
			}
			kts.clusters[kc.name] = cfg
		}
		log.Printf("Loaded %d K8s cluster configurations from kubeconfigs dir", len(kts.clusters))
	}

	// Priority 2: Read the k8s-topology-builder-kubeconfigs secret directly via K8s API.
	// This picks up all clusters even when the secret is not volume-mounted.
	secretName := strings.TrimSpace(os.Getenv("KUBECONFIGS_SECRET_NAME"))
	if secretName == "" {
		secretName = "k8s-topology-builder-kubeconfigs"
	}
	secretNamespace := strings.TrimSpace(os.Getenv("KUBECONFIGS_SECRET_NAMESPACE"))
	if secretNamespace == "" {
		secretNamespace = "aileron"
	}
	secretClusters, secretErr := kts.loadKubeconfigsFromSecret(ctx, secretName, secretNamespace)
	if secretErr != nil {
		log.Printf("K8s topology: could not read secret %s/%s: %v (normal outside cluster)", secretNamespace, secretName, secretErr)
	} else {
		for _, kc := range secretClusters {
			if _, exists := kts.clusters[kc.name]; exists {
				continue // already loaded from mounted dir
			}
			caCertStr := ""
			if len(kc.caCert) > 0 {
				caCertStr = base64.StdEncoding.EncodeToString(kc.caCert)
			}
			kts.clusters[kc.name] = &KubernetesClusterConfig{
				ID:                  uuid.New(),
				Name:                kc.name,
				Environment:         "production",
				Region:              "reno",
				APIServerURL:        kc.serverURL,
				ServiceAccountToken: kc.token,
				CACertData:          caCertStr,
				Enabled:             true,
				SyncStatus:          "pending",
				CreatedAt:           time.Now(),
				UpdatedAt:           time.Now(),
			}
		}
		if len(secretClusters) > 0 {
			log.Printf("K8s topology: loaded %d cluster(s) from secret %s/%s (total now: %d)", len(secretClusters), secretNamespace, secretName, len(kts.clusters))
		}
	}

	if len(kts.clusters) > 0 {
		return nil
	}

	// Priority 3: DB-stored configs (fallback when neither mounted dir nor secret is available).
	return kts.loadClusterConfigurationsFromDB(ctx)
}

// loadKubeconfigsFromSecret reads kubeconfig data from a Kubernetes secret via
// the in-cluster API and parses each value as a kubeconfig file.
func (kts *KubernetesTopologyService) loadKubeconfigsFromSecret(ctx context.Context, secretName, namespace string) ([]*parsedCluster, error) {
	inClusterCfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("not in cluster: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(inClusterCfg)
	if err != nil {
		return nil, fmt.Errorf("create clientset: %w", err)
	}
	secret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get secret: %w", err)
	}

	var clusters []*parsedCluster
	for key, val := range secret.Data {
		if !strings.HasSuffix(key, ".yaml") && !strings.HasSuffix(key, ".yml") {
			continue
		}
		cluster, parseErr := parseKubeconfig(val)
		if parseErr != nil {
			log.Printf("K8s topology: parse kubeconfig key %s from secret: %v", key, parseErr)
			continue
		}
		if cluster.name == "" || cluster.name == "cluster" || cluster.name == "default" || cluster.name == "kubernetes" {
			cluster.name = strings.TrimSuffix(strings.TrimSuffix(key, ".yaml"), ".yml")
		}
		// Skip any kubeconfig that has no token. In-cluster kubeconfigs
		// (kubernetes.default.svc) are also skipped because the same cluster
		// is already represented by its real external-URL entry (e.g. example-cluster.yaml).
		if cluster.token == "" {
			log.Printf("K8s topology: skipping secret key %s (no token — in-cluster OIDC kubeconfigs create duplicates)", key)
			continue
		}
		clusters = append(clusters, cluster)
	}
	return clusters, nil
}

func (kts *KubernetesTopologyService) loadClusterConfigurationsFromDB(ctx context.Context) error {
	// Check if the table exists first (backward compatibility)
	var exists bool
	checkQuery := `
		SELECT EXISTS (
			SELECT FROM information_schema.tables
			WHERE table_schema = 'public'
			AND table_name = 'k8s_cluster_configs'
		)
	`

	err := kts.db.QueryRowContext(ctx, checkQuery).Scan(&exists)
	if err != nil {
		log.Printf("Failed to check if k8s_cluster_configs table exists: %v", err)
		return nil // Graceful degradation
	}

	if !exists {
		log.Printf("k8s_cluster_configs table doesn't exist yet - skipping K8s topology service initialization")
		log.Printf("To enable K8s topology: Apply database/migrations/k8s_topology_config.sql")
		return nil // Graceful degradation - don't crash
	}

	query := `
		SELECT id, name, environment, region, api_server_url, service_account_token,
		       ca_cert_data, namespace, enabled, last_sync, sync_status, created_at, updated_at
		FROM k8s_cluster_configs
		WHERE enabled = true
		ORDER BY name
	`

	rows, err := kts.db.QueryContext(ctx, query)
	if err != nil {
		log.Printf("Failed to load cluster configurations: %v", err)
		return nil // Graceful degradation
	}
	defer rows.Close()

	for rows.Next() {
		config := &KubernetesClusterConfig{}
		var lastSyncNull sql.NullTime

		err := rows.Scan(
			&config.ID, &config.Name, &config.Environment, &config.Region,
			&config.APIServerURL, &config.ServiceAccountToken, &config.CACertData,
			&config.Namespace, &config.Enabled, &lastSyncNull, &config.SyncStatus,
			&config.CreatedAt, &config.UpdatedAt,
		)
		if err != nil {
			log.Printf("Failed to scan cluster config: %v", err)
			continue
		}

		if lastSyncNull.Valid {
			config.LastSync = lastSyncNull.Time
		}

		kts.clusters[config.Name] = config
	}

	log.Printf("Loaded %d K8s cluster configurations", len(kts.clusters))
	return nil
}

func (kts *KubernetesTopologyService) initializeClusterClients() error {
	for name, cluster := range kts.clusters {
		client, err := kts.createKubernetesClient(cluster)
		if err != nil {
			log.Printf("Failed to create client for cluster %s: %v", name, err)
			continue
		}

		kts.clients[name] = client
		log.Printf("Initialized client for cluster: %s", name)
	}

	return nil
}

func (kts *KubernetesTopologyService) createKubernetesClient(config *KubernetesClusterConfig) (kubernetes.Interface, error) {
	// For the in-cluster endpoint (kubernetes.default.svc), use the pod's mounted
	// service-account token instead of the (often empty) kubeconfig token.
	if strings.Contains(config.APIServerURL, "kubernetes.default") || config.ServiceAccountToken == "" {
		inClusterCfg, err := rest.InClusterConfig()
		if err == nil {
			log.Printf("cluster %s: using in-cluster credentials", config.Name)
			clientset, err := kubernetes.NewForConfig(inClusterCfg)
			if err != nil {
				return nil, fmt.Errorf("in-cluster client creation failed: %w", err)
			}
			return clientset, nil
		}
		log.Printf("cluster %s: in-cluster config unavailable (%v), falling back to kubeconfig token", config.Name, err)
		// If not in-cluster (e.g. local dev), fall through to token-based auth.
	}

	var caData []byte
	if config.CACertData != "" {
		if decoded, err := base64.StdEncoding.DecodeString(config.CACertData); err == nil {
			caData = decoded
		} else {
			caData = []byte(config.CACertData)
		}
	}

	tlsCfg := rest.TLSClientConfig{}
	if len(caData) > 0 {
		tlsCfg.CAData = caData
	} else {
		tlsCfg.Insecure = true
	}

	restConfig := &rest.Config{
		Host:        config.APIServerURL,
		BearerToken: config.ServiceAccountToken,
		// Use InsecureSkipVerify for all kubeconfig-based clients: these are
		// internal corporate clusters, bearer-token authenticated, and some
		// kubeconfigs carry stale or self-signed CA certs.
		TLSClientConfig: rest.TLSClientConfig{Insecure: true},
		Proxy:           corporateProxyForRequest,
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}
	return clientset, nil
}

// corporateProxyForRequest routes external K8s API traffic through the Apple
// corporate proxy while bypassing it for in-cluster / RFC-1918 addresses.
// Only used if HTTPS_PROXY env var is set; otherwise returns nil (direct connection).
func corporateProxyForRequest(req *http.Request) (*url.URL, error) {
	host := req.URL.Hostname()

	// Always bypass proxy for in-cluster and local addresses.
	noProxyHosts := []string{
		"kubernetes.default.svc",
		"kubernetes.default",
		"localhost",
		"127.0.0.1",
	}
	for _, h := range noProxyHosts {
		if host == h {
			return nil, nil
		}
	}
	noProxySuffixes := []string{
		".svc.cluster.local",
		".cluster.local",
		".svc",
	}
	for _, s := range noProxySuffixes {
		if strings.HasSuffix(host, s) {
			return nil, nil
		}
	}
	// Respect HTTPS_PROXY env var if set by an admin.
	if p := os.Getenv("HTTPS_PROXY"); p != "" {
		return url.Parse(p)
	}
	if p := os.Getenv("https_proxy"); p != "" {
		return url.Parse(p)
	}
	// No proxy configured — use direct connection.
	return nil, nil
}

func (kts *KubernetesTopologyService) testClusterConnection(ctx context.Context, client kubernetes.Interface) error {
	// Test connection by getting server version
	_, err := client.Discovery().ServerVersion()
	if err != nil {
		return fmt.Errorf("failed to connect to cluster: %w", err)
	}

	return nil
}

func (kts *KubernetesTopologyService) validateClusterConfig(config *KubernetesClusterConfig) error {
	if config.Name == "" {
		return fmt.Errorf("cluster name is required")
	}

	if config.APIServerURL == "" {
		return fmt.Errorf("API server URL is required")
	}

	if config.ServiceAccountToken == "" {
		return fmt.Errorf("service account token is required")
	}

	return nil
}

func (kts *KubernetesTopologyService) discoverClusterTopology(ctx context.Context, config *KubernetesClusterConfig, client kubernetes.Interface) (*KubernetesTopologyResult, error) {
	result := &KubernetesTopologyResult{
		ClusterName:  config.Name,
		Environment:  config.Environment,
		Region:       config.Region,
		DiscoveredAt: time.Now(),
	}

	// Nodes (cluster-wide; falls back to inferring from pods if forbidden)
	nodes, err := kts.discoverNodes(ctx, client)
	if err != nil {
		log.Printf("cluster %s: nodes list failed (%v) — will infer from pods", config.Name, err)
	} else {
		result.Nodes = nodes
	}

	// Namespaces 
	namespaces, err := kts.discoverNamespaces(ctx, client)
	if err != nil {
		log.Printf("cluster %s: namespace list failed: %v", config.Name, err)
	} else {
		result.Namespaces = namespaces
	}

	// Services 
	services, err := kts.discoverServices(ctx, client)
	if err != nil {
		log.Printf("cluster %s: services list failed: %v", config.Name, err)
	} else {
		result.Services = services
	}

	// Deployments 
	deployments, err := kts.discoverDeployments(ctx, client)
	if err != nil {
		log.Printf("cluster %s: deployments list failed: %v", config.Name, err)
	} else {
		result.Deployments = deployments
	}

	// Pods 
	pods, err := kts.discoverPods(ctx, client)
	if err != nil {
		log.Printf("cluster %s: pods list failed: %v", config.Name, err)
	} else {
		result.Pods = pods
	}

	// Infer nodes from pod hostnames if cluster-wide list was denied 
	if len(result.Nodes) == 0 && len(result.Pods) > 0 {
		nodeSet := make(map[string]bool)
		for _, pod := range result.Pods {
			if pod.NodeName != "" {
				nodeSet[pod.NodeName] = true
			}
		}
		for nodeName := range nodeSet {
			result.Nodes = append(result.Nodes, K8sNode{
				Name:        nodeName,
				Status:      "Ready",
				Ready:       true,
				Schedulable: true,
				Roles:       []string{"worker"},
				Labels:      map[string]string{},
			})
		}
		if len(result.Nodes) > 0 {
			log.Printf("cluster %s: inferred %d nodes from pod hostnames", config.Name, len(result.Nodes))
		}
	}

	// Ingresses 
	ingresses, err := kts.discoverIngresses(ctx, client)
	if err != nil {
		log.Printf("cluster %s: ingresses list failed: %v", config.Name, err)
	} else {
		result.Ingresses = ingresses
	}

	// PersistentVolumes 
	pvs, err := kts.discoverPersistentVolumes(ctx, client)
	if err != nil {
		log.Printf("cluster %s: PV list failed: %v", config.Name, err)
	} else {
		result.PersistentVolumes = pvs
	}

	// PersistentVolumeClaims 
	pvcs, err := kts.discoverPersistentVolumeClaims(ctx, client)
	if err != nil {
		log.Printf("cluster %s: PVC list failed: %v", config.Name, err)
	} else {
		result.PersistentVolumeClaims = pvcs
	}

	result.Dependencies = kts.generateDependencies(result)
	result.Summary = kts.calculateClusterSummary(result)

	return result, nil
}

func (kts *KubernetesTopologyService) discoverNodes(ctx context.Context, client kubernetes.Interface) ([]K8sNode, error) {
	nodeList, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var nodes []K8sNode
	for _, node := range nodeList.Items {
		k8sNode := K8sNode{
			Name:        node.Name,
			Labels:      node.Labels,
			Schedulable: !node.Spec.Unschedulable,
		}
		if k8sNode.Labels == nil {
			k8sNode.Labels = make(map[string]string)
		}

		// Extract node status and all conditions
		k8sNode.Ready = false
		for _, condition := range node.Status.Conditions {
			k8sNode.Conditions = append(k8sNode.Conditions, K8sNodeCondition{
				Type:    string(condition.Type),
				Status:  string(condition.Status),
				Message: condition.Message,
				Reason:  condition.Reason,
			})
			if condition.Type == "Ready" && condition.Status == "True" {
				k8sNode.Ready = true
				k8sNode.Status = "Ready"
			}
		}
		if !k8sNode.Ready {
			k8sNode.Status = "NotReady"
		}

		// Extract version and OS
		if node.Status.NodeInfo.KubeletVersion != "" {
			k8sNode.Version = node.Status.NodeInfo.KubeletVersion
		}
		if node.Status.NodeInfo.OSImage != "" {
			k8sNode.OS = node.Status.NodeInfo.OSImage
		}

		// Extract roles
		k8sNode.Roles = []string{}
		for label := range node.Labels {
			if label == "node-role.kubernetes.io/master" || label == "node-role.kubernetes.io/control-plane" {
				k8sNode.Roles = append(k8sNode.Roles, "master")
			} else if label == "node-role.kubernetes.io/worker" {
				k8sNode.Roles = append(k8sNode.Roles, "worker")
			}
		}
		if len(k8sNode.Roles) == 0 {
			k8sNode.Roles = append(k8sNode.Roles, "worker")
		}

		// Store addresses in labels and InternalIP field
		for _, addr := range node.Status.Addresses {
			if addr.Type == "InternalIP" {
				k8sNode.Labels["internal-ip"] = addr.Address
				k8sNode.InternalIP = addr.Address
			}
			if addr.Type == "ExternalIP" {
				k8sNode.Labels["external-ip"] = addr.Address
			}
		}

		// Extract capacity
		k8sNode.Capacity = map[string]string{
			"cpu":    node.Status.Capacity.Cpu().String(),
			"memory": node.Status.Capacity.Memory().String(),
			"pods":   node.Status.Capacity.Pods().String(),
		}

		nodes = append(nodes, k8sNode)
	}

	return nodes, nil
}

func (kts *KubernetesTopologyService) discoverNamespaces(ctx context.Context, client kubernetes.Interface) ([]K8sNamespace, error) {
	nsList, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var namespaces []K8sNamespace
	for _, ns := range nsList.Items {
		namespaces = append(namespaces, K8sNamespace{
			Name:      ns.Name,
			Status:    string(ns.Status.Phase),
			Labels:    ns.Labels,
			CreatedAt: ns.CreationTimestamp.Time,
		})
	}

	return namespaces, nil
}

func (kts *KubernetesTopologyService) discoverServices(ctx context.Context, client kubernetes.Interface) ([]K8sService, error) {
	serviceList, err := client.CoreV1().Services("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var services []K8sService
	for _, svc := range serviceList.Items {
		k8sService := K8sService{
			Name:      svc.Name,
			Namespace: svc.Namespace,
			Type:      string(svc.Spec.Type),
			ClusterIP: svc.Spec.ClusterIP,
			Selector:  svc.Spec.Selector,
			Labels:    svc.Labels,
			CreatedAt: svc.CreationTimestamp.Time,
		}

		// Extract external IPs
		for _, ip := range svc.Spec.ExternalIPs {
			k8sService.ExternalIPs = append(k8sService.ExternalIPs, ip)
		}

		// Extract ports
		for _, port := range svc.Spec.Ports {
			k8sService.Ports = append(k8sService.Ports, K8sServicePort{
				Name:       port.Name,
				Port:       port.Port,
				TargetPort: port.TargetPort.String(),
				Protocol:   string(port.Protocol),
			})
		}

		services = append(services, k8sService)
	}

	return services, nil
}

func (kts *KubernetesTopologyService) discoverDeployments(ctx context.Context, client kubernetes.Interface) ([]K8sDeployment, error) {
	deploymentList, err := client.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var deployments []K8sDeployment
	for _, deploy := range deploymentList.Items {
		k8sDeployment := K8sDeployment{
			Name:              deploy.Name,
			Namespace:         deploy.Namespace,
			Replicas:          *deploy.Spec.Replicas,
			ReadyReplicas:     deploy.Status.ReadyReplicas,
			AvailableReplicas: deploy.Status.AvailableReplicas,
			Labels:            deploy.Labels,
			Selector:          deploy.Spec.Selector.MatchLabels,
			CreatedAt:         deploy.CreationTimestamp.Time,
		}

		// Extract containers
		for _, container := range deploy.Spec.Template.Spec.Containers {
			k8sContainer := K8sContainer{
				Name:  container.Name,
				Image: container.Image,
			}

			// Extract container ports
			for _, port := range container.Ports {
				k8sContainer.Ports = append(k8sContainer.Ports, K8sContainerPort{
					Name:          port.Name,
					ContainerPort: port.ContainerPort,
					Protocol:      string(port.Protocol),
				})
			}

			// Extract resources
			if container.Resources.Requests != nil {
				k8sContainer.Resources.Requests = make(map[string]string)
				for k, v := range container.Resources.Requests {
					k8sContainer.Resources.Requests[string(k)] = v.String()
				}
			}
			if container.Resources.Limits != nil {
				k8sContainer.Resources.Limits = make(map[string]string)
				for k, v := range container.Resources.Limits {
					k8sContainer.Resources.Limits[string(k)] = v.String()
				}
			}

			k8sDeployment.Containers = append(k8sDeployment.Containers, k8sContainer)
		}

		deployments = append(deployments, k8sDeployment)
	}

	return deployments, nil
}

func (kts *KubernetesTopologyService) discoverPods(ctx context.Context, client kubernetes.Interface) ([]K8sPod, error) {
	podList, err := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var pods []K8sPod
	for _, pod := range podList.Items {
		k8sPod := K8sPod{
			Name:      pod.Name,
			Namespace: pod.Namespace,
			NodeName:  pod.Spec.NodeName,
			Phase:     string(pod.Status.Phase),
			PodIP:     pod.Status.PodIP,
			HostIP:    pod.Status.HostIP,
			Labels:    pod.Labels,
			Ready:     true,
			CreatedAt: pod.CreationTimestamp.Time,
		}

		// Check if pod is ready
		for _, condition := range pod.Status.Conditions {
			if condition.Type == "Ready" && condition.Status != "True" {
				k8sPod.Ready = false
				break
			}
		}

		// Calculate restarts
		for _, containerStatus := range pod.Status.ContainerStatuses {
			k8sPod.Restarts += containerStatus.RestartCount
		}

		// Extract containers
		for i, container := range pod.Spec.Containers {
			k8sContainer := K8sContainer{
				Name:  container.Name,
				Image: container.Image,
			}

			// Check container readiness
			if i < len(pod.Status.ContainerStatuses) {
				k8sContainer.Ready = pod.Status.ContainerStatuses[i].Ready
			}

			// Extract container ports
			for _, port := range container.Ports {
				k8sContainer.Ports = append(k8sContainer.Ports, K8sContainerPort{
					Name:          port.Name,
					ContainerPort: port.ContainerPort,
					Protocol:      string(port.Protocol),
				})
			}

			k8sPod.Containers = append(k8sPod.Containers, k8sContainer)
		}

		pods = append(pods, k8sPod)
	}

	return pods, nil
}

func (kts *KubernetesTopologyService) discoverIngresses(ctx context.Context, client kubernetes.Interface) ([]K8sIngress, error) {
	ingressList, err := client.NetworkingV1().Ingresses("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var ingresses []K8sIngress
	for _, ingress := range ingressList.Items {
		k8sIngress := K8sIngress{
			Name:      ingress.Name,
			Namespace: ingress.Namespace,
			Labels:    ingress.Labels,
			CreatedAt: ingress.CreationTimestamp.Time,
		}

		// Extract rules
		for _, rule := range ingress.Spec.Rules {
			k8sRule := K8sIngressRule{
				Host: rule.Host,
			}

			if rule.HTTP != nil {
				for _, path := range rule.HTTP.Paths {
					k8sPath := K8sIngressPath{
						Path:     path.Path,
						PathType: string(*path.PathType),
					}

					if path.Backend.Service != nil {
						k8sPath.ServiceName = path.Backend.Service.Name
						if path.Backend.Service.Port.Number != 0 {
							k8sPath.ServicePort = path.Backend.Service.Port.Number
						}
					}

					k8sRule.Paths = append(k8sRule.Paths, k8sPath)
				}
			}

			k8sIngress.Rules = append(k8sIngress.Rules, k8sRule)
		}

		// Extract TLS
		for _, tls := range ingress.Spec.TLS {
			k8sIngress.TLS = append(k8sIngress.TLS, K8sIngressTLS{
				Hosts:      tls.Hosts,
				SecretName: tls.SecretName,
			})
		}

		ingresses = append(ingresses, k8sIngress)
	}

	return ingresses, nil
}

func (kts *KubernetesTopologyService) discoverPersistentVolumes(ctx context.Context, client kubernetes.Interface) ([]K8sPersistentVolume, error) {
	pvList, err := client.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	pvs := make([]K8sPersistentVolume, 0, len(pvList.Items))
	for _, pv := range pvList.Items {
		var capGB float64
		if q, ok := pv.Spec.Capacity[corev1.ResourceStorage]; ok {
			capGB = float64(q.Value()) / (1024 * 1024 * 1024)
		}

		modes := make([]string, 0, len(pv.Spec.AccessModes))
		for _, m := range pv.Spec.AccessModes {
			modes = append(modes, string(m))
		}

		claimRef := ""
		if pv.Spec.ClaimRef != nil {
			claimRef = pv.Spec.ClaimRef.Namespace + "/" + pv.Spec.ClaimRef.Name
		}

		vm := "Filesystem"
		if pv.Spec.VolumeMode != nil {
			vm = string(*pv.Spec.VolumeMode)
		}

		pvs = append(pvs, K8sPersistentVolume{
			Name:          pv.Name,
			CapacityGB:    capGB,
			AccessModes:   modes,
			ReclaimPolicy: string(pv.Spec.PersistentVolumeReclaimPolicy),
			Status:        string(pv.Status.Phase),
			StorageClass:  pv.Spec.StorageClassName,
			ClaimRef:      claimRef,
			VolumeMode:    vm,
		})
	}
	return pvs, nil
}

func (kts *KubernetesTopologyService) discoverPersistentVolumeClaims(ctx context.Context, client kubernetes.Interface) ([]K8sPersistentVolumeClaim, error) {
	pvcList, err := client.CoreV1().PersistentVolumeClaims("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	pvcs := make([]K8sPersistentVolumeClaim, 0, len(pvcList.Items))
	for _, pvc := range pvcList.Items {
		var reqGB, capGB float64
		if q, ok := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
			reqGB = float64(q.Value()) / (1024 * 1024 * 1024)
		}
		if q, ok := pvc.Status.Capacity[corev1.ResourceStorage]; ok {
			capGB = float64(q.Value()) / (1024 * 1024 * 1024)
		}

		modes := make([]string, 0, len(pvc.Spec.AccessModes))
		for _, m := range pvc.Spec.AccessModes {
			modes = append(modes, string(m))
		}

		sc := ""
		if pvc.Spec.StorageClassName != nil {
			sc = *pvc.Spec.StorageClassName
		}

		vm := "Filesystem"
		if pvc.Spec.VolumeMode != nil {
			vm = string(*pvc.Spec.VolumeMode)
		}

		pvcs = append(pvcs, K8sPersistentVolumeClaim{
			Name:         pvc.Name,
			Namespace:    pvc.Namespace,
			Status:       string(pvc.Status.Phase),
			VolumeName:   pvc.Spec.VolumeName,
			RequestedGB:  reqGB,
			CapacityGB:   capGB,
			AccessModes:  modes,
			StorageClass: sc,
			VolumeMode:   vm,
		})
	}
	return pvcs, nil
}

func (kts *KubernetesTopologyService) generateDependencies(result *KubernetesTopologyResult) []K8sDependency {
	var dependencies []K8sDependency

	// Service to Pod dependencies
	for _, service := range result.Services {
		for _, pod := range result.Pods {
			if service.Namespace == pod.Namespace && kts.labelsMatch(service.Selector, pod.Labels) {
				dependencies = append(dependencies, K8sDependency{
					SourceType:   "Service",
					SourceName:   service.Name,
					TargetType:   "Pod",
					TargetName:   pod.Name,
					Relationship: "selects",
					Namespace:    service.Namespace,
				})
			}
		}
	}

	// Ingress to Service dependencies
	for _, ingress := range result.Ingresses {
		for _, rule := range ingress.Rules {
			for _, path := range rule.Paths {
				if path.ServiceName != "" {
					dependencies = append(dependencies, K8sDependency{
						SourceType:   "Ingress",
						SourceName:   ingress.Name,
						TargetType:   "Service",
						TargetName:   path.ServiceName,
						Relationship: "routes_to",
						Namespace:    ingress.Namespace,
					})
				}
			}
		}
	}

	return dependencies
}

func (kts *KubernetesTopologyService) calculateClusterSummary(result *KubernetesTopologyResult) K8sClusterSummary {
	summary := K8sClusterSummary{
		TotalNodes:       len(result.Nodes),
		TotalPods:        len(result.Pods),
		TotalServices:    len(result.Services),
		TotalDeployments: len(result.Deployments),
		TotalIngresses:   len(result.Ingresses),
		NamespaceCount:   len(result.Namespaces),
	}

	// Count ready nodes
	for _, node := range result.Nodes {
		if node.Ready {
			summary.ReadyNodes++
		}
	}

	// Count running pods
	for _, pod := range result.Pods {
		if pod.Phase == "Running" && pod.Ready {
			summary.RunningPods++
		}
	}

	// Count healthy deployments
	for _, deploy := range result.Deployments {
		if deploy.ReadyReplicas >= deploy.Replicas && deploy.Replicas > 0 {
			summary.HealthyDeployments++
		}
	}

	// Calculate health score
	nodeHealth := float64(summary.ReadyNodes) / float64(max(summary.TotalNodes, 1))
	podHealth := float64(summary.RunningPods) / float64(max(summary.TotalPods, 1))
	summary.HealthScore = (nodeHealth + podHealth) / 2.0

	// Storage metrics
	summary.TotalPVCs = len(result.PersistentVolumeClaims)
	for _, pvc := range result.PersistentVolumeClaims {
		switch pvc.Status {
		case "Bound":
			summary.BoundPVCs++
			summary.TotalStorageGB += pvc.CapacityGB
		case "Pending":
			summary.PendingPVCs++
		}
	}
	switch {
	case summary.PendingPVCs > 0 && summary.TotalPVCs > 0 && float64(summary.PendingPVCs)/float64(summary.TotalPVCs) > 0.2:
		summary.StorageHealth = "critical"
	case summary.PendingPVCs > 0:
		summary.StorageHealth = "warning"
	default:
		summary.StorageHealth = "healthy"
	}

	return summary
}

func (kts *KubernetesTopologyService) labelsMatch(selector, labels map[string]string) bool {
	if len(selector) == 0 {
		return false
	}

	for key, value := range selector {
		if labels[key] != value {
			return false
		}
	}

	return true
}

func (kts *KubernetesTopologyService) saveClusterConfiguration(ctx context.Context, config *KubernetesClusterConfig) error {
	if kts.readOnly {
		return nil
	}
	if config.ID == uuid.Nil {
		config.ID = uuid.New()
	}
	config.CreatedAt = time.Now()
	config.UpdatedAt = time.Now()

	query := `
		INSERT INTO k8s_cluster_configs (
			id, name, environment, region, api_server_url, service_account_token,
			ca_cert_data, namespace, enabled, sync_status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (name) DO UPDATE SET
			environment = EXCLUDED.environment,
			region = EXCLUDED.region,
			api_server_url = EXCLUDED.api_server_url,
			service_account_token = EXCLUDED.service_account_token,
			ca_cert_data = EXCLUDED.ca_cert_data,
			namespace = EXCLUDED.namespace,
			enabled = EXCLUDED.enabled,
			updated_at = EXCLUDED.updated_at
	`

	_, err := kts.db.ExecContext(ctx, query,
		config.ID, config.Name, config.Environment, config.Region,
		config.APIServerURL, config.ServiceAccountToken, config.CACertData,
		config.Namespace, config.Enabled, "pending", config.CreatedAt, config.UpdatedAt,
	)

	return err
}

func (kts *KubernetesTopologyService) deleteClusterConfiguration(ctx context.Context, clusterName string) error {
	if kts.readOnly {
		return nil
	}
	query := `DELETE FROM k8s_cluster_configs WHERE name = $1`
	_, err := kts.db.ExecContext(ctx, query, clusterName)
	return err
}

func (kts *KubernetesTopologyService) updateClusterSyncStatus(ctx context.Context, clusterName, status, errorMsg string) {
	if kts.readOnly {
		return
	}
	query := `
		UPDATE k8s_cluster_configs 
		SET last_sync = NOW(), sync_status = $1, sync_error = $2, updated_at = NOW()
		WHERE name = $3
	`

	kts.db.ExecContext(ctx, query, status, errorMsg, clusterName)
}

func (kts *KubernetesTopologyService) startBackgroundDiscovery(ctx context.Context) {
	ticker := time.NewTicker(kts.refreshInterval)
	defer ticker.Stop()

	log.Printf("Starting background K8s topology discovery (interval: %v)", kts.refreshInterval)

	// Run discovery immediately on startup so the cache is warm for the first request.
	results, err := kts.DiscoverTopology(ctx, "")
	if err != nil {
		log.Printf("Initial K8s topology discovery failed: %v", err)
	} else {
		totalNodes, totalPods := 0, 0
		for _, r := range results {
			totalNodes += r.Summary.TotalNodes
			totalPods += r.Summary.TotalPods
		}
		log.Printf("Initial K8s topology discovery: %d nodes, %d pods across %d clusters",
			totalNodes, totalPods, len(results))
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-kts.stopChan:
			return
		case <-ticker.C:
			results, err := kts.DiscoverTopology(ctx, "")
			if err != nil {
				log.Printf("Background topology discovery failed: %v", err)
			} else {
				log.Printf("Background topology discovery completed (%d clusters)", len(results))
			}
		}
	}
}

// getClient returns the kubernetes.Interface for a named cluster (read-lock held by caller is NOT required — this acquires its own read-lock).
func (kts *KubernetesTopologyService) getClient(clusterName string) (kubernetes.Interface, error) {
	kts.mu.RLock()
	defer kts.mu.RUnlock()
	client, exists := kts.clients[clusterName]
	if !exists {
		return nil, fmt.Errorf("no client for cluster: %s", clusterName)
	}
	return client, nil
}

func (kts *KubernetesTopologyService) GetPodLogs(ctx context.Context, clusterName, namespace, podName, container string, tailLines int) (string, error) {
	client, err := kts.getClient(clusterName)
	if err != nil {
		return "", fmt.Errorf("cluster not found: %s", clusterName)
	}
	tail := int64(tailLines)
	opts := &corev1.PodLogOptions{TailLines: &tail}
	if container != "" {
		opts.Container = container
	}
	req := client.CoreV1().Pods(namespace).GetLogs(podName, opts)
	result := req.Do(ctx)
	body, err := result.Raw()
	if err != nil {
		return "", fmt.Errorf("failed to get logs: %w", err)
	}
	return string(body), nil
}

// GetClusterEvents fetches Kubernetes events for a cluster, optionally filtered by namespace and time.
func (kts *KubernetesTopologyService) GetClusterEvents(ctx context.Context, clusterName, namespace string, sinceMinutes int) ([]K8sEvent, error) {
	client, err := kts.getClient(clusterName)
	if err != nil {
		return nil, fmt.Errorf("cluster not found: %s", clusterName)
	}

	ns := namespace
	if ns == "" {
		ns = "" // empty string = all namespaces in CoreV1().Events()
	}

	eventList, err := client.CoreV1().Events(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list events: %w", err)
	}

	cutoff := time.Time{}
	if sinceMinutes > 0 {
		cutoff = time.Now().Add(-time.Duration(sinceMinutes) * time.Minute)
	}

	var events []K8sEvent
	for _, e := range eventList.Items {
		ts := e.LastTimestamp.Time
		if ts.IsZero() {
			ts = e.EventTime.Time
		}
		// Only apply cutoff filter when we have a valid timestamp — zero-time events
		// (e.g. newer events.k8s.io/v1 that only populate EventTime) must not be
		// silently dropped by a false "before cutoff" comparison.
		if !cutoff.IsZero() && !ts.IsZero() && ts.Before(cutoff) {
			continue
		}
		events = append(events, K8sEvent{
			Type:    e.Type,
			Reason:  e.Reason,
			Message: e.Message,
			InvolvedObject: K8sEventObject{
				Kind:      e.InvolvedObject.Kind,
				Name:      e.InvolvedObject.Name,
				Namespace: e.InvolvedObject.Namespace,
			},
			LastTimestamp:  ts,
			FirstTimestamp: e.FirstTimestamp.Time,
			Count:          e.Count,
			Namespace:      e.Namespace,
		})
	}
	return events, nil
}

func (kts *KubernetesTopologyService) Stop() {
	close(kts.stopChan)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
