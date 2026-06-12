package topology

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// ============================================================================
// MULTI-LAYER INFRASTRUCTURE TOPOLOGY SYSTEM
// Handles: BM CloudStack VM K8s Applications across regions
// This complements the existing service-level topology
// ============================================================================

// InfraTopologyService manages infrastructure topology discovery and mapping
type InfraTopologyService struct {
	DB           *sql.DB // Public for handlers to access
	discoveryMgr *InfraDiscoveryManager
	k8sService   *KubernetesTopologyService
	cacheTTL     time.Duration
	mu           sync.RWMutex

	// Last-good CloudStack discovery results — used as fallback when CS API is unreachable.
	csLastGoodMu    sync.RWMutex
	csLastGoodNodes []InfraNode
	csLastGoodAt    time.Time

	// Neo4j driver for querying topology data written by LiveTopologyFetcher.
	neo4jDriver neo4j.DriverWithContext

	// readOnly is true when the backing Postgres is a streaming standby (DR site).
	// All write operations are skipped when true.
	readOnly bool
}

// NewInfraTopologyService creates a new infrastructure topology service
func NewInfraTopologyService(db *sql.DB) *InfraTopologyService {
	svc := &InfraTopologyService{
		DB:           db,
		discoveryMgr: NewInfraDiscoveryManager(),
		cacheTTL:     5 * time.Minute,
	}
	// Detect standby mode once at startup.
	var inRecovery bool
	if err := db.QueryRow(`SELECT pg_is_in_recovery()`).Scan(&inRecovery); err == nil && inRecovery {
		svc.readOnly = true
		log.Printf("topology: Postgres is a read-only standby — topology writes disabled (DR mode)")
	}
	return svc
}

// SetK8sTopologyService wires the K8s topology service for live cluster data.
func (s *InfraTopologyService) SetK8sTopologyService(svc *KubernetesTopologyService) {
	s.k8sService = svc
}

// SetNeo4jDriver wires the Neo4j driver so BuildInfraGraph can query NetApp and other
// topology data written directly to Neo4j by LiveTopologyFetcher.
func (s *InfraTopologyService) SetNeo4jDriver(driver neo4j.DriverWithContext) {
	s.mu.Lock()
	s.neo4jDriver = driver
	s.mu.Unlock()
}

// ============================================================================
// INFRASTRUCTURE LAYERS
// ============================================================================

// InfrastructureLayer represents different layers in the stack
type InfrastructureLayer string

const (
	LayerBareMetal   InfrastructureLayer = "bare_metal"
	LayerCloudStack  InfrastructureLayer = "cloudstack"
	LayerVM          InfrastructureLayer = "virtual_machine"
	LayerKubernetes  InfrastructureLayer = "kubernetes"
	LayerApplication InfrastructureLayer = "application"
	LayerNetwork     InfrastructureLayer = "network"
	LayerStorage     InfrastructureLayer = "storage"
)

// Region represents a data center region
type Region struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Location  string    `json:"location"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}

// ============================================================================
// NODE TYPES
// ============================================================================

// InfraNode represents any infrastructure component
type InfraNode struct {
	ID            string                 `json:"id"`
	Name          string                 `json:"name"`
	Type          string                 `json:"type"` // e.g., "bare_metal_server", "vm", "k8s_pod"
	Layer         InfrastructureLayer    `json:"layer"`
	Region        string                 `json:"region"`
	ParentID      string                 `json:"parent_id,omitempty"`    // Parent node in hierarchy
	Properties    map[string]interface{} `json:"properties"`             // Custom properties
	Labels        map[string]string      `json:"labels,omitempty"`       // Labels/tags
	Status        string                 `json:"status"`                 // running, stopped, unknown
	HealthStatus  string                 `json:"health_status"`          // healthy, degraded, unhealthy
	Dependencies  []string               `json:"dependencies,omitempty"` // IDs of nodes this depends on
	Dependents    []string               `json:"dependents,omitempty"`   // IDs of nodes that depend on this
	LastDiscovery time.Time              `json:"last_discovery"`
	CreatedAt     time.Time              `json:"created_at"`
	UpdatedAt     time.Time              `json:"updated_at"`
}

// BareMetal represents physical servers
type BareMetal struct {
	NodeID      string             `json:"node_id"`
	Hostname    string             `json:"hostname"`
	IPAddress   string             `json:"ip_address"`
	MACAddress  string             `json:"mac_address"`
	CPU         CPUInfo            `json:"cpu"`
	Memory      MemoryInfo         `json:"memory"`
	Storage     []StorageInfo      `json:"storage"`
	Network     []NetworkInterface `json:"network"`
	Location    string             `json:"location"`     // Rack location
	PowerStatus string             `json:"power_status"` // on, off
	IPMI        IPMIInfo           `json:"ipmi,omitempty"`
}

// CloudStackInstance represents CloudStack virtual machines
type CloudStackInstance struct {
	NodeID          string     `json:"node_id"`
	InstanceID      string     `json:"instance_id"`
	InstanceName    string     `json:"instance_name"`
	HostID          string     `json:"host_id"`       // CloudStack host ID
	BareMetalNodeID string     `json:"bare_metal_id"` // Physical server hosting this
	Zone            string     `json:"zone"`
	Template        string     `json:"template"`
	ServiceOffering string     `json:"service_offering"`
	State           string     `json:"state"`
	IPAddress       string     `json:"ip_address"`
	VCPU            int        `json:"vcpu"`
	Memory          int64      `json:"memory_mb"`
	Disk            []DiskInfo `json:"disks"`
	Networks        []string   `json:"networks"`
}

// VirtualMachine represents VMs (K8s workers and standalone)
type VirtualMachine struct {
	NodeID        string                 `json:"node_id"`
	Hostname      string                 `json:"hostname"`
	VMType        string                 `json:"vm_type"`       // "k8s_worker", "standalone", "control_plane"
	CloudStackID  string                 `json:"cloudstack_id"` // Parent CloudStack instance
	IPAddress     string                 `json:"ip_address"`
	OSFamily      string                 `json:"os_family"`
	OSVersion     string                 `json:"os_version"`
	KernelVersion string                 `json:"kernel_version"`
	VCPU          int                    `json:"vcpu"`
	Memory        int64                  `json:"memory_mb"`
	Disk          int64                  `json:"disk_gb"`
	K8sClusterID  string                 `json:"k8s_cluster_id,omitempty"`
	Services      []string               `json:"services,omitempty"`
	Metadata      map[string]interface{} `json:"metadata"`
}

// KubernetesCluster represents a K8s cluster
type KubernetesCluster struct {
	NodeID          string                 `json:"node_id"`
	ClusterName     string                 `json:"cluster_name"`
	ClusterID       string                 `json:"cluster_id"`
	Version         string                 `json:"version"`
	Region          string                 `json:"region"`
	Provider        string                 `json:"provider"` // "self-hosted", "eks", "gke", etc.
	APIServerURL    string                 `json:"api_server_url"`
	ControlPlaneVMs []string               `json:"control_plane_vms"`
	WorkerVMs       []string               `json:"worker_vms"`
	NodeCount       int                    `json:"node_count"`
	PodCount        int                    `json:"pod_count"`
	NamespaceCount  int                    `json:"namespace_count"`
	ServiceCount    int                    `json:"service_count"`
	IngressCount    int                    `json:"ingress_count"`
	NetworkPolicy   string                 `json:"network_policy,omitempty"`
	StorageClasses  []string               `json:"storage_classes"`
	Metadata        map[string]interface{} `json:"metadata"`
}

// Application represents applications running on K8s or VMs
type Application struct {
	NodeID         string            `json:"node_id"`
	AppName        string            `json:"app_name"`
	AppType        string            `json:"app_type"` // "k8s", "vm", "container"
	Namespace      string            `json:"namespace,omitempty"`
	ClusterID      string            `json:"cluster_id,omitempty"`
	Version        string            `json:"version"`
	Replicas       int               `json:"replicas"`
	Endpoints      []Endpoint        `json:"endpoints"`
	Dependencies   []AppDependency   `json:"dependencies"`
	ResourceUsage  ResourceUsage     `json:"resource_usage"`
	HealthEndpoint string            `json:"health_endpoint,omitempty"`
	Labels         map[string]string `json:"labels"`
}

// ============================================================================
// SUPPORTING TYPES
// ============================================================================

type CPUInfo struct {
	Cores   int     `json:"cores"`
	Threads int     `json:"threads"`
	Model   string  `json:"model"`
	Speed   string  `json:"speed"`
	Usage   float64 `json:"usage_percent"`
}

type MemoryInfo struct {
	Total int64   `json:"total_mb"`
	Used  int64   `json:"used_mb"`
	Free  int64   `json:"free_mb"`
	Usage float64 `json:"usage_percent"`
}

type StorageInfo struct {
	Device     string `json:"device"`
	Type       string `json:"type"` // SSD, HDD, NVMe
	Size       int64  `json:"size_gb"`
	Used       int64  `json:"used_gb"`
	MountPoint string `json:"mount_point,omitempty"`
}

type NetworkInterface struct {
	Name       string `json:"name"`
	IPAddress  string `json:"ip_address"`
	MACAddress string `json:"mac_address"`
	Speed      string `json:"speed"`
	Status     string `json:"status"`
}

type IPMIInfo struct {
	IPAddress string `json:"ip_address"`
	Username  string `json:"username"`
	Enabled   bool   `json:"enabled"`
}

type DiskInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size_gb"`
	Type string `json:"type"`
}

type Endpoint struct {
	URL      string `json:"url"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Internal bool   `json:"internal"`
}

type AppDependency struct {
	TargetApp string `json:"target_app"`
	Type      string `json:"type"` // "api", "database", "message_queue", etc.
	Critical  bool   `json:"critical"`
	LatencyMs int    `json:"latency_ms,omitempty"`
}

type ResourceUsage struct {
	CPU     float64 `json:"cpu_percent"`
	Memory  float64 `json:"memory_percent"`
	Disk    float64 `json:"disk_percent"`
	Network float64 `json:"network_mbps"`
}

// ============================================================================
// TOPOLOGY DISCOVERY
// ============================================================================

// InfraDiscoveryManager handles multi-layer discovery
type InfraDiscoveryManager struct {
	discoverers map[InfrastructureLayer]Discoverer
	mu          sync.RWMutex
}

// Discoverer interface for layer-specific discovery
type Discoverer interface {
	Discover(ctx context.Context, region string) ([]InfraNode, error)
	GetName() string
	GetLayer() InfrastructureLayer
}

// NewInfraDiscoveryManager creates a new discovery manager
func NewInfraDiscoveryManager() *InfraDiscoveryManager {
	dm := &InfraDiscoveryManager{
		discoverers: make(map[InfrastructureLayer]Discoverer),
	}

	// Only register essential discoverers initially
	// Real discoverers will be registered when configuration is loaded
	dm.RegisterDiscoverer(&BareMetalDiscoverer{})
	dm.RegisterDiscoverer(&VMDiscoverer{})
	dm.RegisterDiscoverer(&ApplicationDiscoverer{})

	// Do NOT register mock CloudStack and K8s discoverers by default
	// They will be registered as fallbacks only if real ones fail

	log.Println("Infrastructure discoverers initialized (real APIs will be configured from database/environment)")

	return dm
}

// RegisterDiscoverer registers a discoverer for a specific layer
func (dm *InfraDiscoveryManager) RegisterDiscoverer(d Discoverer) {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	// Check if we're replacing an existing discoverer
	if existing, exists := dm.discoverers[d.GetLayer()]; exists {
		log.Printf("Replacing discoverer for layer %s: %s %s",
			d.GetLayer(), existing.GetName(), d.GetName())
	}

	dm.discoverers[d.GetLayer()] = d
	log.Printf("Registered discoverer: %s for layer %s", d.GetName(), d.GetLayer())
}

// DiscoverAll discovers all layers in parallel across regions
func (dm *InfraDiscoveryManager) DiscoverAll(ctx context.Context, regions []string) (map[InfrastructureLayer][]InfraNode, error) {
	results := make(map[InfrastructureLayer][]InfraNode)
	var mu sync.Mutex
	var wg sync.WaitGroup
	errors := make([]error, 0)

	for layer, discoverer := range dm.discoverers {
		for _, region := range regions {
			wg.Add(1)
			go func(l InfrastructureLayer, d Discoverer, r string) {
				defer wg.Done()

				nodes, err := d.Discover(ctx, r)
				if err != nil {
					mu.Lock()
					errors = append(errors, fmt.Errorf("layer %s region %s: %w", l, r, err))
					mu.Unlock()
					return
				}

				mu.Lock()
				results[l] = append(results[l], nodes...)
				mu.Unlock()

				log.Printf("Discovered %d nodes in layer %s, region %s", len(nodes), l, r)
			}(layer, discoverer, region)
		}
	}

	wg.Wait()

	if len(errors) > 0 {
		log.Printf("Discovery completed with %d errors", len(errors))
	}

	return results, nil
}

// ============================================================================
// TOPOLOGY QUERIES
// ============================================================================

// GetFullInfraTopology gets complete topology with all relationships
func (s *InfraTopologyService) GetFullInfraTopology(ctx context.Context, region string) (*FullTopology, error) {
	topology := &FullTopology{
		Region:        region,
		Timestamp:     time.Now(),
		Layers:        make(map[InfrastructureLayer][]InfraNode),
		Relationships: make([]Relationship, 0),
	}

	// PRIORITY: Load real discoverers first, fallback to mock only if real fails
	s.configureRealDiscoverers(ctx)

	// Add debug logging to see which discoverers are configured
	log.Printf("Current discoverers configured:")
	for layer, discoverer := range s.discoveryMgr.discoverers {
		log.Printf("   %s: %s", layer, discoverer.GetName())
	}

	// Discover all layers - real discoverers will be used first
	discovered, err := s.discoveryMgr.DiscoverAll(ctx, []string{region})
	if err != nil {
		log.Printf("Discovery failed for region %s: %v", region, err)
		return nil, err
	}

	topology.Layers = discovered

	// Build relationships
	topology.Relationships = s.buildRelationships(discovered)

	// Calculate statistics
	topology.Stats = s.calculateStats(discovered)

	return topology, nil
}

// configureRealDiscoverers loads config and FORCES real API usage (no fallback to mock)
func (s *InfraTopologyService) configureRealDiscoverers(ctx context.Context) {
	log.Printf("FORCING REAL DISCOVERER CONFIGURATION...")

	// DEBUG: First check what's actually in the database
	var configJSON []byte
	query := `SELECT config FROM topology_config WHERE id = 'default' LIMIT 1`
	err := s.DB.QueryRowContext(ctx, query).Scan(&configJSON)

	if err != nil {
		log.Printf("CRITICAL: No topology config found in database: %v", err)
		log.Printf("SOLUTION: Configure CloudStack APIs in SRE Command Center UI")
		return
	}

	log.Printf("Found topology config in database: %d bytes", len(configJSON))
	log.Printf("Raw config JSON: %s", string(configJSON))

	var config map[string]interface{}
	if err := json.Unmarshal(configJSON, &config); err != nil {
		log.Printf("CRITICAL: Failed to parse topology config: %v", err)
		log.Printf("Raw JSON that failed: %s", string(configJSON))
		return
	}

	// Debug: Show all config keys
	configKeys := make([]string, 0, len(config))
	for k := range config {
		configKeys = append(configKeys, k)
	}
	log.Printf("Config keys found: %v", configKeys)

	// FORCE CloudStack configuration
	cloudstackRaw, cloudstackExists := config["cloudstack"]
	log.Printf("CloudStack key exists: %v", cloudstackExists)
	log.Printf("CloudStack type: %T", cloudstackRaw)

	if cloudstackExists {
		if cloudstackConfigs, ok := cloudstackRaw.([]interface{}); ok {
			log.Printf("CloudStack configs array found: %d configurations", len(cloudstackConfigs))

			if len(cloudstackConfigs) == 0 {
				log.Printf("CloudStack configs array is EMPTY")
				log.Printf("Please add CloudStack configurations in SRE Command Center")
				return
			}

			// Process each CloudStack configuration
			validConfigs := make([]map[string]interface{}, 0)

			for i, csConfigRaw := range cloudstackConfigs {
				log.Printf("Processing CloudStack config %d...", i+1)
				log.Printf("Config %d type: %T", i+1, csConfigRaw)

				if cs, ok := csConfigRaw.(map[string]interface{}); ok {
					// Extract all possible field variations
					endpoint := extractStringField(cs, []string{"endpoint", "apiEndpoint", "url", "baseUrl"})
					apiKey := extractStringField(cs, []string{"apiKey", "api_key", "key"})
					secretKey := extractStringField(cs, []string{"secretKey", "secret_key", "secret"})
					clusterName := extractStringField(cs, []string{"clusterName", "cluster_name", "name"})

					if clusterName == "" {
						clusterName = fmt.Sprintf("cloudstack-cluster-%d", i+1)
					}

					log.Printf("Config %d details:", i+1)
					log.Printf("   Endpoint: '%s'", endpoint)
					log.Printf("   Cluster Name: '%s'", clusterName)
					log.Printf("   API Key: present=%v, length=%d", (apiKey != ""), len(apiKey))
					log.Printf("   Secret Key: present=%v, length=%d", (secretKey != ""), len(secretKey))

					if endpoint != "" && apiKey != "" && secretKey != "" {
						cs["clusterName"] = clusterName // Ensure cluster name is set
						validConfigs = append(validConfigs, cs)
						log.Printf("CloudStack config %d VALID", i+1)
					} else {
						log.Printf("CloudStack config %d INVALID - missing required fields", i+1)
						if endpoint == "" {
							log.Printf("   Missing: endpoint")
						}
						if apiKey == "" {
							log.Printf("   Missing: apiKey")
						}
						if secretKey == "" {
							log.Printf("   Missing: secretKey")
						}
					}
				} else {
					log.Printf("CloudStack config %d is not a valid object: %T", i+1, csConfigRaw)
				}
			}

			if len(validConfigs) > 0 {
				log.Printf("Creating multi-CloudStack discoverer with %d valid configs", len(validConfigs))

				// Create multi-CloudStack discoverer
				multiCSDiscoverer := NewMultiCloudStackDiscoverer(validConfigs)

				// FORCE register (replace any existing)
				s.discoveryMgr.RegisterDiscoverer(multiCSDiscoverer)
				log.Printf("FORCE REGISTERED MULTI-CloudStack discoverer: %d clusters", len(validConfigs))

				// Test connection immediately
				testCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()

				log.Printf("Testing CloudStack connections...")
				testNodes, testErr := multiCSDiscoverer.Discover(testCtx, "reno")
				if testErr != nil {
					log.Printf("CloudStack API test failed: %v", testErr)
				} else {
					log.Printf("CloudStack API test SUCCESS: %d nodes discovered", len(testNodes))
				}

			} else {
				log.Printf("CRITICAL: All CloudStack configurations are INVALID")
				log.Printf("Please check CloudStack API credentials in SRE Command Center")
			}
		} else {
			log.Printf("CloudStack config is not an array: %T", cloudstackRaw)
			log.Printf("Expected array of CloudStack configurations")
		}
	} else {
		log.Printf("CRITICAL: No 'cloudstack' key found in configuration")
		log.Printf("Please configure CloudStack in SRE Command Center UI")
	}

	// FORCE K8s configuration from database
	log.Printf("Configuring K8s clusters from database...")
	if err := s.configureK8sFromDatabase(ctx); err != nil {
		log.Printf("K8s configuration failed: %v", err)
	}
}

// configureK8sFromDatabase loads K8s configurations from database and creates real discoverers
func (s *InfraTopologyService) configureK8sFromDatabase(ctx context.Context) error {
	// Query k8s_cluster_configs table for enabled clusters
	query := `
		SELECT id, cluster_name, display_name, api_server, region, environment,
		       version, readonly_mode, discovery_enabled, labels, metadata
		FROM k8s_cluster_configs
		WHERE discovery_enabled = true
		ORDER BY region, cluster_name
	`

	rows, err := s.DB.QueryContext(ctx, query)
	if err != nil {
		log.Printf("Failed to query k8s_cluster_configs: %v", err)
		return err
	}
	defer rows.Close()

	k8sCount := 0
	for rows.Next() {
		var id, clusterName, displayName, apiServer, region, environment, version string
		var readonlyMode, discoveryEnabled bool
		var labelsJSON, metadataJSON []byte

		err := rows.Scan(&id, &clusterName, &displayName, &apiServer, &region,
			&environment, &version, &readonlyMode, &discoveryEnabled,
			&labelsJSON, &metadataJSON)
		if err != nil {
			log.Printf("Error scanning K8s config: %v", err)
			continue
		}

		log.Printf("Found K8s cluster: %s (%s) - %s", clusterName, region, apiServer)

		// Create real K8s discoverer for this cluster
		if err := s.createRealK8sDiscoverer(ctx, clusterName, apiServer); err != nil {
			log.Printf("Failed to create K8s discoverer for %s: %v", clusterName, err)
		} else {
			log.Printf("Created real K8s discoverer for: %s", clusterName)
			k8sCount++
		}
	}

	if k8sCount == 0 {
		log.Printf("No enabled K8s clusters found in database")
		log.Printf("Configure K8s clusters in SRE Command Center Topology page")
		return fmt.Errorf("no K8s clusters configured")
	}

	log.Printf("Configured %d real K8s discoverers from database", k8sCount)
	return nil
}

// createRealK8sDiscoverer creates a real K8s discoverer for a cluster
func (s *InfraTopologyService) createRealK8sDiscoverer(ctx context.Context, clusterName, apiServer string) error {
	// Try to get token from environment or kubeconfig
	token := os.Getenv(fmt.Sprintf("K8S_%s_TOKEN", strings.ToUpper(strings.Replace(clusterName, "-", "_", -1))))
	kubeconfig := os.Getenv(fmt.Sprintf("KUBECONFIG_%s", strings.ToUpper(strings.Replace(clusterName, "-", "_", -1))))

	var kubeconfigBytes []byte
	if kubeconfig != "" {
		if data, err := ioutil.ReadFile(kubeconfig); err == nil {
			kubeconfigBytes = data
		}
	}

	// Create real K8s discoverer
	realK8sDiscoverer, err := NewRealKubernetesDiscoverer(apiServer, token, kubeconfigBytes)
	if err != nil {
		return fmt.Errorf("failed to create real K8s discoverer: %v", err)
	}

	// Register the discoverer (this will replace the mock one)
	s.discoveryMgr.RegisterDiscoverer(realK8sDiscoverer)

	return nil
}

// extractStringField extracts string field with multiple possible names
func extractStringField(config map[string]interface{}, fieldNames []string) string {
	for _, fieldName := range fieldNames {
		if value, exists := config[fieldName]; exists {
			if str, ok := value.(string); ok {
				return str
			}
		}
	}
	return ""
}

// loadCloudStackConfigs reads CloudStack configs from cloudstack_configs table (Admin page),
// falling back to the legacy topology_config JSON blob. Returns configs in the format
// expected by NewMultiCloudStackDiscoverer: {"endpoint", "apiKey", "secretKey", ...}
func (s *InfraTopologyService) loadCloudStackConfigs(ctx context.Context) []map[string]interface{} {
	// Primary: cloudstack_configs table (created via Admin Infrastructure page)
	rows, err := s.DB.QueryContext(ctx, `
		SELECT name, api_url, api_key, secret_key, zone_id
		FROM cloudstack_configs
		WHERE enabled = true
	`)
	if err == nil {
		defer rows.Close()
		var configs []map[string]interface{}
		for rows.Next() {
			var name, apiURL, apiKey, secretKey string
			var zoneID sql.NullString
			if err := rows.Scan(&name, &apiURL, &apiKey, &secretKey, &zoneID); err != nil {
				continue
			}
			cfg := map[string]interface{}{
				"endpoint":  apiURL,
				"apiKey":    apiKey,
				"secretKey": secretKey,
				"name":      name,
			}
			if zoneID.Valid {
				cfg["zone"] = zoneID.String
			}
			configs = append(configs, cfg)
		}
		if len(configs) > 0 {
			log.Printf("CloudStack: loaded %d config(s) from cloudstack_configs table", len(configs))
			return configs
		}
	}

	// Fallback: legacy topology_config JSON blob (topology config page)
	var configJSON []byte
	if err := s.DB.QueryRowContext(ctx, `SELECT config FROM topology_config WHERE id = 'default' LIMIT 1`).Scan(&configJSON); err != nil {
		log.Printf("No CloudStack configs found in cloudstack_configs or topology_config")
		return nil
	}
	var config map[string]interface{}
	if json.Unmarshal(configJSON, &config) != nil {
		return nil
	}
	csArr, ok := config["cloudstack"].([]interface{})
	if !ok || len(csArr) == 0 {
		return nil
	}
	var configs []map[string]interface{}
	for _, item := range csArr {
		if c, ok := item.(map[string]interface{}); ok {
			ep, _ := c["endpoint"].(string)
			ak, _ := c["apiKey"].(string)
			sk, _ := c["secretKey"].(string)
			if ep != "" && ak != "" && sk != "" {
				configs = append(configs, c)
			}
		}
	}
	if len(configs) > 0 {
		log.Printf("CloudStack: loaded %d config(s) from legacy topology_config", len(configs))
	}
	return configs
}

// ForceRealTopologyDiscovery discovers all 5 infrastructure layers using live APIs.
// Layer chain: BM (KVM hosts) VM (CloudStack VMs with state) K8s nodes Pods/Services
func (s *InfraTopologyService) ForceRealTopologyDiscovery(ctx context.Context, region string) (*FullTopology, error) {
	log.Printf("Using ONLY configured CloudStack/K8s APIs, NO MOCK DATA")

	topology := &FullTopology{
		Region:        region,
		Timestamp:     time.Now(),
		Layers:        make(map[InfrastructureLayer][]InfraNode),
		Relationships: make([]Relationship, 0),
	}
	for _, l := range []InfrastructureLayer{LayerBareMetal, LayerCloudStack, LayerVM, LayerKubernetes, LayerApplication} {
		topology.Layers[l] = []InfraNode{}
	}

	// Phase 1: CloudStack KVM hosts + VMs 
	// Primary source: cloudstack_configs table (Admin Infrastructure page)
	// Fallback: topology_config JSON blob (legacy topology config page)
	vmByIP := make(map[string]string) // ip vmNode.ID

	csConfigs := s.loadCloudStackConfigs(ctx)
	if len(csConfigs) > 0 {
		csNodes, err := NewMultiCloudStackDiscoverer(csConfigs).Discover(ctx, region)
		if err != nil || len(csNodes) == 0 {
			// CS API unreachable — fall back to last known good nodes.
			s.csLastGoodMu.RLock()
			lastGood := s.csLastGoodNodes
			lastGoodAt := s.csLastGoodAt
			s.csLastGoodMu.RUnlock()
			if len(lastGood) > 0 {
				log.Printf("CloudStack discovery failed (%v) — using last known data from %v", err, lastGoodAt.Format(time.RFC3339))
				csNodes = lastGood
			} else if err != nil {
				log.Printf("CloudStack discovery error: %v (no fallback data available)", err)
			}
		}
		for _, node := range csNodes {
			switch node.Layer {
			case LayerBareMetal:
				topology.Layers[LayerBareMetal] = append(topology.Layers[LayerBareMetal], node)
			case LayerCloudStack:
				topology.Layers[LayerCloudStack] = append(topology.Layers[LayerCloudStack], node)
				vmNode := node
				vmNode.Layer = LayerVM
				vmNode.Type = "virtual_machine"
				topology.Layers[LayerVM] = append(topology.Layers[LayerVM], vmNode)
				if ip, ok := node.Properties["ip_address"].(string); ok && ip != "" {
					vmByIP[ip] = node.ID
				}
			}
		}
		log.Printf("CloudStack: %d KVM hosts, %d VMs",
			len(topology.Layers[LayerBareMetal]), len(topology.Layers[LayerCloudStack]))

		// Only update sync_status and save last-good data when we have a fresh successful fetch.
		if len(topology.Layers[LayerCloudStack]) > 0 {
			vmCount := len(topology.Layers[LayerCloudStack])
			if !s.readOnly {
				_, _ = s.DB.ExecContext(ctx, `
					UPDATE cloudstack_configs
					SET sync_status = 'active', vm_count = $1, last_sync = NOW()
					WHERE enabled = true
				`, vmCount)
			}
			// Persist as last-good for future fallback.
			s.csLastGoodMu.Lock()
			s.csLastGoodNodes = csNodes
			s.csLastGoodAt = time.Now()
			s.csLastGoodMu.Unlock()
		}
	}

	// Phase 2: Kubernetes nodes + pods 
	if s.k8sService != nil {
		// Use cached results if fresh (< 6 minutes) so the topology page
		// responds instantly after the first background discovery completes.
		k8sResults := s.k8sService.GetCachedResults()
		if len(k8sResults) == 0 {
			// Cache is cold — run discovery synchronously (first request only).
			log.Printf("K8s cache cold — running live discovery (may be slow)")
			var err error
			k8sResults, err = s.k8sService.DiscoverTopology(ctx, "")
			if err != nil {
				log.Printf("K8s topology discovery error: %v (continuing)", err)
			}
		} else {
			log.Printf("K8s: serving %d cluster(s) from cache", len(k8sResults))
		}

		now := time.Now()
		for _, clusterResult := range k8sResults {
				clusterID := clusterResult.ClusterName

				// K8s nodes LayerKubernetes
				for _, knode := range clusterResult.Nodes {
					nodeID := fmt.Sprintf("k8s-node-%s-%s", clusterID, knode.Name)
					parentID := ""
					// Link to VM by InternalIP if available
					if ip, ok := knode.Labels["internal-ip"]; ok && ip != "" {
						if vid, exists := vmByIP[ip]; exists {
							parentID = vid
						}
					}
					infraNode := InfraNode{
						ID:       nodeID,
						Name:     knode.Name,
						Type:     "k8s_node",
						Layer:    LayerKubernetes,
						Region:   region,
						ParentID: parentID,
						Status:   strings.ToLower(knode.Status),
						HealthStatus: func() string {
							if knode.Ready {
								return "healthy"
							}
							return "unhealthy"
						}(),
						Properties: map[string]interface{}{
							"cluster":   clusterID,
							"version":   knode.Version,
							"os":        knode.OS,
							"roles":     knode.Roles,
							"ready":     knode.Ready,
							"schedulable": knode.Schedulable,
						},
						Labels: map[string]string{
							"cluster": clusterID,
						},
						Dependencies:  []string{},
						Dependents:    []string{},
						LastDiscovery: now,
						CreatedAt:     now,
						UpdatedAt:     now,
					}
					topology.Layers[LayerKubernetes] = append(topology.Layers[LayerKubernetes], infraNode)
				}

				// Pods LayerApplication
				for _, pod := range clusterResult.Pods {
					podID := fmt.Sprintf("k8s-pod-%s-%s-%s", clusterID, pod.Namespace, pod.Name)
					nodeParentID := fmt.Sprintf("k8s-node-%s-%s", clusterID, pod.NodeName)
					health := "healthy"
					if !pod.Ready || pod.Phase != "Running" {
						health = "degraded"
					}
					infraNode := InfraNode{
						ID:           podID,
						Name:         fmt.Sprintf("%s/%s", pod.Namespace, pod.Name),
						Type:         "k8s_pod",
						Layer:        LayerApplication,
						Region:       region,
						ParentID:     nodeParentID,
						Status:       strings.ToLower(pod.Phase),
						HealthStatus: health,
						Properties: map[string]interface{}{
							"cluster":   clusterID,
							"namespace": pod.Namespace,
							"node":      pod.NodeName,
							"pod_ip":    pod.PodIP,
							"restarts":  pod.Restarts,
							"ready":     pod.Ready,
						},
						Labels: map[string]string{
							"cluster":   clusterID,
							"namespace": pod.Namespace,
						},
						Dependencies:  []string{nodeParentID},
						Dependents:    []string{},
						LastDiscovery: now,
						CreatedAt:     now,
						UpdatedAt:     now,
					}
					topology.Layers[LayerApplication] = append(topology.Layers[LayerApplication], infraNode)
				}
			}
			log.Printf("K8s: %d nodes, %d pods across %d clusters",
				len(topology.Layers[LayerKubernetes]),
				len(topology.Layers[LayerApplication]),
				len(k8sResults))
	}

	// Phase 3: relationships + stats 
	topology.Relationships = s.buildRelationships(topology.Layers)
	topology.Stats = s.calculateStats(topology.Layers)

	log.Printf("FORCE REAL DISCOVERY COMPLETE: %d total nodes (BM=%d CS=%d VM=%d K8s=%d App=%d)",
		topology.Stats.TotalNodes,
		len(topology.Layers[LayerBareMetal]),
		len(topology.Layers[LayerCloudStack]),
		len(topology.Layers[LayerVM]),
		len(topology.Layers[LayerKubernetes]),
		len(topology.Layers[LayerApplication]))

	// Persist snapshot asynchronously so callers don't wait.
	go func() {
		snapCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.SaveInfraTopologySnapshot(snapCtx, topology); err != nil {
			log.Printf("topology snapshot save: %v", err)
		}
	}()

	return topology, nil
}

// Helper function to get map keys
func getKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// FullTopology represents complete infrastructure topology
type FullTopology struct {
	Region        string                              `json:"region"`
	Timestamp     time.Time                           `json:"timestamp"`
	Layers        map[InfrastructureLayer][]InfraNode `json:"layers"`
	Relationships []Relationship                      `json:"relationships"`
	Stats         TopologyStats                       `json:"stats"`
}

// Relationship represents a connection between nodes
type Relationship struct {
	SourceID  string `json:"source_id"`
	TargetID  string `json:"target_id"`
	Type      string `json:"type"`      // "hosts", "runs_on", "depends_on", "connects_to"
	Direction string `json:"direction"` // "up", "down", "lateral"
	Strength  string `json:"strength"`  // "strong", "weak"
	Critical  bool   `json:"critical"`  // True if failure impacts
}

// TopologyStats provides topology statistics
type TopologyStats struct {
	TotalNodes         int                         `json:"total_nodes"`
	NodesByLayer       map[InfrastructureLayer]int `json:"nodes_by_layer"`
	NodesByStatus      map[string]int              `json:"nodes_by_status"`
	NodesByHealth      map[string]int              `json:"nodes_by_health"`
	TotalRelationships int                         `json:"total_relationships"`
}

// buildRelationships constructs relationships between all nodes
func (s *InfraTopologyService) buildRelationships(discovered map[InfrastructureLayer][]InfraNode) []Relationship {
	relationships := make([]Relationship, 0)

	// Build parent-child relationships
	for _, nodes := range discovered {
		for _, node := range nodes {
			if node.ParentID != "" {
				relationships = append(relationships, Relationship{
					SourceID:  node.ID,
					TargetID:  node.ParentID,
					Type:      "runs_on",
					Direction: "up",
					Strength:  "strong",
					Critical:  true,
				})
			}

			// Build dependency relationships
			for _, depID := range node.Dependencies {
				relationships = append(relationships, Relationship{
					SourceID:  node.ID,
					TargetID:  depID,
					Type:      "depends_on",
					Direction: "lateral",
					Strength:  "strong",
					Critical:  true,
				})
			}
		}
	}

	return relationships
}

// calculateStats computes topology statistics
func (s *InfraTopologyService) calculateStats(discovered map[InfrastructureLayer][]InfraNode) TopologyStats {
	stats := TopologyStats{
		NodesByLayer:  make(map[InfrastructureLayer]int),
		NodesByStatus: make(map[string]int),
		NodesByHealth: make(map[string]int),
	}

	for layer, nodes := range discovered {
		stats.NodesByLayer[layer] = len(nodes)
		stats.TotalNodes += len(nodes)

		for _, node := range nodes {
			stats.NodesByStatus[node.Status]++
			stats.NodesByHealth[node.HealthStatus]++
		}
	}

	return stats
}

// ============================================================================
// DEPENDENCY ANALYSIS
// ============================================================================

// DependencyChain represents a complete dependency tree
type DependencyChain struct {
	RootNode     string            `json:"root_node"`
	Dependencies []DependencyLevel `json:"dependencies"`
	TotalDepth   int               `json:"total_depth"`
	Timestamp    time.Time         `json:"timestamp"`
}

// DependencyLevel represents dependencies at a specific level
type DependencyLevel struct {
	Level int         `json:"level"`
	Nodes []InfraNode `json:"nodes"`
}

// GetInfraDependencyChain returns the complete dependency tree for nodeID by
// recursively walking infrastructure_dependency_edges up to 5 levels deep.
func (s *InfraTopologyService) GetInfraDependencyChain(ctx context.Context, nodeID string) (*DependencyChain, error) {
	chain := &DependencyChain{
		RootNode:     nodeID,
		Dependencies: make([]DependencyLevel, 0),
		Timestamp:    time.Now(),
	}

	if s.DB == nil {
		return chain, nil
	}

	// Recursive CTE: walk downstream dependencies (source → target) up to depth 5.
	rows, err := s.DB.QueryContext(ctx, `
		WITH RECURSIVE dep_tree AS (
			SELECT source_id, target_id, edge_type, 1 AS depth
			FROM infrastructure_dependency_edges
			WHERE source_id = $1
			UNION ALL
			SELECT e.source_id, e.target_id, e.edge_type, dt.depth + 1
			FROM infrastructure_dependency_edges e
			JOIN dep_tree dt ON e.source_id = dt.target_id
			WHERE dt.depth < 5
		)
		SELECT DISTINCT target_id, depth, edge_type
		FROM dep_tree
		ORDER BY depth, target_id
	`, nodeID)
	if err != nil {
		return chain, nil // non-fatal: return empty chain
	}
	defer rows.Close()

	byLevel := make(map[int][]InfraNode)
	maxDepth := 0
	for rows.Next() {
		var targetID, edgeType string
		var depth int
		if err := rows.Scan(&targetID, &depth, &edgeType); err != nil {
			continue
		}
		if depth > maxDepth {
			maxDepth = depth
		}
		byLevel[depth] = append(byLevel[depth], InfraNode{
			ID:   targetID,
			Type: edgeType,
		})
	}

	for lvl := 1; lvl <= maxDepth; lvl++ {
		if nodes, ok := byLevel[lvl]; ok {
			chain.Dependencies = append(chain.Dependencies, DependencyLevel{
				Level: lvl,
				Nodes: nodes,
			})
		}
	}
	chain.TotalDepth = maxDepth

	return chain, nil
}

// ============================================================================
// IMPACT ANALYSIS
// ============================================================================

// CalculateInfraBlastRadius returns the estimated impact if nodeID fails by
// counting all nodes that depend on it (direct or transitively, up to depth 5).
func (s *InfraTopologyService) CalculateInfraBlastRadius(ctx context.Context, nodeID string) (*BlastRadius, error) {
	blast := &BlastRadius{
		SourceNode:    nodeID,
		AffectedNodes: make([]AffectedNode, 0),
		Timestamp:     time.Now(),
	}

	if s.DB == nil {
		return blast, nil
	}

	// Traverse reverse edges (target → source) to find everything that depends on nodeID.
	rows, err := s.DB.QueryContext(ctx, `
		WITH RECURSIVE impact_tree AS (
			SELECT source_id, target_id, edge_type, 1 AS depth
			FROM infrastructure_dependency_edges
			WHERE target_id = $1
			UNION ALL
			SELECT e.source_id, e.target_id, e.edge_type, it.depth + 1
			FROM infrastructure_dependency_edges e
			JOIN impact_tree it ON e.target_id = it.source_id
			WHERE it.depth < 5
		)
		SELECT DISTINCT source_id, depth, edge_type
		FROM impact_tree
		ORDER BY depth, source_id
	`, nodeID)
	if err != nil {
		return blast, nil
	}
	defer rows.Close()

	seen := make(map[string]bool)
	criticalCount := 0
	for rows.Next() {
		var sourceID, edgeType string
		var depth int
		if err := rows.Scan(&sourceID, &depth, &edgeType); err != nil {
			continue
		}
		if seen[sourceID] {
			continue
		}
		seen[sourceID] = true

		impact := "cascading"
		if depth == 1 {
			impact = "direct"
		}
		critical := edgeType == "HOSTED_ON" || edgeType == "RUNS_ON" || depth == 1
		if critical {
			criticalCount++
		}
		blast.AffectedNodes = append(blast.AffectedNodes, AffectedNode{
			NodeID:   sourceID,
			NodeName: sourceID,
			Layer:    edgeType,
			Impact:   impact,
			Critical: critical,
		})
	}

	blast.TotalAffected = len(blast.AffectedNodes)
	blast.CriticalNodes = criticalCount
	switch {
	case blast.TotalAffected == 0:
		blast.EstimatedImpact = "none"
	case blast.TotalAffected < 5:
		blast.EstimatedImpact = "low"
	case blast.TotalAffected < 15:
		blast.EstimatedImpact = "medium"
	case blast.TotalAffected < 50:
		blast.EstimatedImpact = "high"
	default:
		blast.EstimatedImpact = "critical"
	}

	return blast, nil
}

// BlastRadius represents potential impact of node failure
type BlastRadius struct {
	SourceNode      string         `json:"source_node"`
	AffectedNodes   []AffectedNode `json:"affected_nodes"`
	TotalAffected   int            `json:"total_affected"`
	CriticalNodes   int            `json:"critical_nodes"`
	EstimatedImpact string         `json:"estimated_impact"` // "low", "medium", "high", "critical"
	Timestamp       time.Time      `json:"timestamp"`
}

// AffectedNode represents a node that would be impacted
type AffectedNode struct {
	NodeID   string `json:"node_id"`
	NodeName string `json:"node_name"`
	Layer    string `json:"layer"`
	Impact   string `json:"impact"` // "direct", "cascading"
	Critical bool   `json:"critical"`
}

// ============================================================================
// PERSISTENCE
// ============================================================================

// SaveInfraTopologySnapshot saves current topology to database.
// Skips silently when running against a read-only streaming standby (DR site).
func (s *InfraTopologyService) SaveInfraTopologySnapshot(ctx context.Context, topology *FullTopology) error {
	if s.readOnly {
		return nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Serialize topology
	topologyJSON, err := json.Marshal(topology)
	if err != nil {
		return err
	}

	// UPSERT: keep exactly one row per region (latest wins).
	// A unique index on region must exist; add via migration if missing.
	query := `
		INSERT INTO topology_snapshots (id, region, snapshot_data, node_count, created_at)
		VALUES (gen_random_uuid(), $1, $2, $3, $4)
		ON CONFLICT (region)
		DO UPDATE SET
			snapshot_data = EXCLUDED.snapshot_data,
			node_count    = EXCLUDED.node_count,
			created_at    = EXCLUDED.created_at
	`

	_, err = tx.ExecContext(ctx, query, topology.Region, topologyJSON, topology.Stats.TotalNodes, topology.Timestamp)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// GetLatestInfraSnapshot retrieves most recent topology snapshot for a region
func (s *InfraTopologyService) GetLatestInfraSnapshot(ctx context.Context, region string) (*FullTopology, error) {
	query := `
		SELECT snapshot_data
		FROM topology_snapshots
		WHERE region = $1
		ORDER BY created_at DESC
		LIMIT 1
	`

	var topologyJSON []byte
	err := s.DB.QueryRowContext(ctx, query, region).Scan(&topologyJSON)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no topology snapshot found for region %s", region)
		}
		return nil, err
	}

	var topology FullTopology
	if err := json.Unmarshal(topologyJSON, &topology); err != nil {
		return nil, err
	}

	return &topology, nil
}
