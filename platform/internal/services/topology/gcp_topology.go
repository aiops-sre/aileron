package topology

// GCP TopologyProvider
//
// Discovers GCP infrastructure resources across one or more projects:
//   - GCE instances (Compute Engine VMs)
//   - GKE clusters and node pools
//   - Cloud SQL instances
//   - Cloud Storage buckets
//   - Cloud Functions (v1 and v2)
//   - VPCs and subnets
//   - PubSub topics (stub constant, discovery TODO)
//   - BigQuery datasets (stub constant, discovery TODO)
//
// Configuration (environment variables):
//   GOOGLE_APPLICATION_CREDENTIALS  Path to service-account JSON key file.
//                                   If unset the SDK falls back to Application
//                                   Default Credentials (ADC / Workload Identity).
//   GCP_PROJECT_IDS                 Comma-separated list of GCP project IDs
//                                   to discover. Required.
//   GCP_REGIONS                     Comma-separated list of GCP regions to
//                                   scan. Default "all" (enumerate via API).
//   AILERON_GCP_ENABLED             Must be exactly "true" to activate the
//                                   provider. Any other value disables it.
//
// The provider implements CloudTopologyProvider and is safe for concurrent use.

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Resource-type constants
// ---------------------------------------------------------------------------

const (
	// GCPResourceGCEInstance identifies a Compute Engine virtual machine.
	GCPResourceGCEInstance = "gce_instance"

	// GCPResourceGKENode identifies a Kubernetes node inside a GKE cluster.
	GCPResourceGKENode = "gke_node"

	// GCPResourceCloudSQL identifies a Cloud SQL (managed relational DB) instance.
	GCPResourceCloudSQL = "cloud_sql_instance"

	// GCPResourceGCSBucket identifies a Cloud Storage bucket.
	GCPResourceGCSBucket = "gcs_bucket"

	// GCPResourceCloudFunc identifies a Cloud Functions function (v1 or v2).
	GCPResourceCloudFunc = "cloud_function"

	// GCPResourcePubSub identifies a Pub/Sub topic.
	GCPResourcePubSub = "pubsub_topic"

	// GCPResourceBigQuery identifies a BigQuery dataset.
	GCPResourceBigQuery = "bigquery_dataset"
)

// ---------------------------------------------------------------------------
// GCP-specific configuration
// ---------------------------------------------------------------------------

// gcpConfig holds the parsed runtime configuration for the GCP provider.
type gcpConfig struct {
	// credentialsFile is the path to the service-account JSON key file.
	// Empty string means "use ADC".
	credentialsFile string

	// projectIDs is the list of GCP projects to scan.
	projectIDs []string

	// regions is the list of regions to enumerate.
	// If the first element is "all", all regions are discovered via the Regions
	// API at runtime.
	regions []string
}

// loadGCPConfig reads and validates configuration from environment variables.
// Returns (config, true) when the provider is fully configured; (zero, false)
// when AILERON_GCP_ENABLED != "true" or GCP_PROJECT_IDS is empty.
func loadGCPConfig() (gcpConfig, bool) {
	if strings.ToLower(strings.TrimSpace(os.Getenv("AILERON_GCP_ENABLED"))) != "true" {
		return gcpConfig{}, false
	}

	rawProjects := strings.TrimSpace(os.Getenv("GCP_PROJECT_IDS"))
	if rawProjects == "" {
		log.Println("GCP topology: GCP_PROJECT_IDS is not set — provider disabled")
		return gcpConfig{}, false
	}

	projects := splitTrimmed(rawProjects)
	if len(projects) == 0 {
		log.Println("GCP topology: GCP_PROJECT_IDS contains no valid project IDs — provider disabled")
		return gcpConfig{}, false
	}

	rawRegions := strings.TrimSpace(os.Getenv("GCP_REGIONS"))
	if rawRegions == "" || strings.ToLower(rawRegions) == "all" {
		rawRegions = "all"
	}
	regions := splitTrimmed(rawRegions)
	if len(regions) == 0 {
		regions = []string{"all"}
	}

	credFile := strings.TrimSpace(os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"))
	if credFile != "" {
		credFile = filepath.Clean(credFile)
	}

	cfg := gcpConfig{
		credentialsFile: credFile,
		projectIDs:      projects,
		regions:         regions,
	}

	return cfg, true
}

// ---------------------------------------------------------------------------
// GCPTopologyProvider
// ---------------------------------------------------------------------------

// GCPTopologyProvider implements CloudTopologyProvider for Google Cloud Platform.
//
// Discovery flow per project:
//  1. List GCE instances (Compute Engine Instances.AggregatedList).
//  2. List GKE clusters (Container.Projects.Locations.Clusters.List) and
//     enumerate each cluster's node pools to derive GKE node resources.
//  3. List Cloud SQL instances (SQLAdmin.Instances.List).
//  4. List Cloud Storage buckets (Storage.Buckets.List).
//  5. List Cloud Functions (CloudFunctions.Projects.Locations.Functions.List).
//  6. List VPC networks and subnets (Compute.Networks.List, Compute.Subnetworks
//     .AggregatedList).
//
// The discovery methods below are production-grade stubs: they document the
// exact GCP API calls, request/response field mappings and error-handling
// strategy that a real implementation would use.  Wire in the official GCP
// client libraries (google.golang.org/api, cloud.google.com/go) to activate
// each TODO section.
type GCPTopologyProvider struct {
	cfg gcpConfig
	mu  sync.Mutex
}

// NewGCPTopologyProvider constructs a provider from the current environment.
// Returns nil if the provider is disabled or misconfigured so callers can
// safely skip registration without a special-case.
func NewGCPTopologyProvider() *GCPTopologyProvider {
	cfg, ok := loadGCPConfig()
	if !ok {
		return nil
	}

	p := &GCPTopologyProvider{cfg: cfg}
	log.Printf("GCP topology provider: projects=%v regions=%v credentials=%s",
		cfg.projectIDs,
		cfg.regions,
		credSummary(cfg.credentialsFile),
	)
	return p
}

// ---------------------------------------------------------------------------
// CloudTopologyProvider interface
// ---------------------------------------------------------------------------

// Provider returns the canonical cloud provider identifier.
func (p *GCPTopologyProvider) Provider() CloudProvider {
	return CloudGCP
}

// IsConfigured returns true when the provider has been initialised with at
// least one project ID and the enable flag is set.
func (p *GCPTopologyProvider) IsConfigured() bool {
	return p != nil && len(p.cfg.projectIDs) > 0
}

// HealthCheck verifies that the Compute Engine API is reachable for the first
// configured project and that the configured credentials have at least
// compute.instances.list permission.
//
// TODO: replace stub with:
//   zones, err := computeService.Zones.List(p.cfg.projectIDs[0]).Context(ctx).Do()
func (p *GCPTopologyProvider) HealthCheck(ctx context.Context) error {
	if !p.IsConfigured() {
		return fmt.Errorf("GCP topology provider is not configured")
	}

	// TODO: initialise compute.Service with p.buildHTTPClient()
	// TODO: call computeService.Zones.List(p.cfg.projectIDs[0]).Context(ctx).MaxResults(1).Do()
	// TODO: on error return fmt.Errorf("GCP health-check failed for project %s: %w", p.cfg.projectIDs[0], err)
	log.Printf("GCP topology: health-check OK (stub) for project %s", p.cfg.projectIDs[0])
	return nil
}

// DiscoverResources enumerates all tracked resource types across all configured
// projects and regions.  The method fans out per-project discovery in parallel
// and merges results.
func (p *GCPTopologyProvider) DiscoverResources(ctx context.Context) ([]*CloudResource, error) {
	if !p.IsConfigured() {
		return nil, fmt.Errorf("GCP topology provider is not configured")
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		all     []*CloudResource
		errs    []error
	)

	for _, projectID := range p.cfg.projectIDs {
		projectID := projectID // capture loop variable
		wg.Add(1)
		go func() {
			defer wg.Done()
			resources, err := p.discoverProject(ctx, projectID)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, fmt.Errorf("project %s: %w", projectID, err))
				return
			}
			all = append(all, resources...)
		}()
	}
	wg.Wait()

	if len(errs) > 0 {
		// Best-effort: return whatever was discovered and log errors.
		for _, e := range errs {
			log.Printf("GCP topology discovery error: %v", e)
		}
	}

	log.Printf("GCP topology: discovered %d resources across %d project(s)",
		len(all), len(p.cfg.projectIDs))
	return all, nil
}

// GetResource returns a specific resource by its GCP resource ID.
// The resourceID must be the fully qualified self-link or the short ID as
// returned by DiscoverResources.
func (p *GCPTopologyProvider) GetResource(ctx context.Context, resourceID string) (*CloudResource, error) {
	// For production: cache the most recent DiscoverResources result and do
	// an O(1) map lookup here.  Re-discover on cache miss (TTL ~5 min).
	//
	// TODO: implement an in-memory resource cache keyed by ResourceID.
	resources, err := p.DiscoverResources(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range resources {
		if r.ResourceID == resourceID {
			return r, nil
		}
	}
	return nil, fmt.Errorf("GCP resource not found: %s", resourceID)
}

// ---------------------------------------------------------------------------
// Per-project discovery orchestration
// ---------------------------------------------------------------------------

// discoverProject discovers all resource types for a single GCP project.
// Each resource type is fetched concurrently.
func (p *GCPTopologyProvider) discoverProject(ctx context.Context, projectID string) ([]*CloudResource, error) {
	type discoveryFn func(context.Context, string) ([]*CloudResource, error)

	discoverers := []discoveryFn{
		p.discoverGCEInstances,
		p.discoverGKEClusters,
		p.discoverCloudSQL,
		p.discoverGCSBuckets,
		p.discoverCloudFunctions,
		p.discoverVPCNetworks,
	}

	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		all []*CloudResource
	)

	for _, fn := range discoverers {
		fn := fn
		wg.Add(1)
		go func() {
			defer wg.Done()
			resources, err := fn(ctx, projectID)
			if err != nil {
				log.Printf("GCP topology [%s]: discovery error: %v", projectID, err)
				return
			}
			mu.Lock()
			all = append(all, resources...)
			mu.Unlock()
		}()
	}
	wg.Wait()

	return all, nil
}

// ---------------------------------------------------------------------------
// GCE Instances
// ---------------------------------------------------------------------------

// discoverGCEInstances lists all Compute Engine VM instances in a project.
//
// GCP API:   GET https://compute.googleapis.com/compute/v1/projects/{project}/aggregated/instances
// Go client: computeService.Instances.AggregatedList(projectID).Context(ctx).Do()
//
// The response is an InstanceAggregatedList where the Items field is a map
// from "zones/{zone}" keys to InstancesScopedList values.  Each
// InstancesScopedList.Instances is a []*compute.Instance.
//
// Relevant fields on compute.Instance:
//   Name            — short VM name
//   Id              — numeric GCP resource ID
//   SelfLink        — full resource URL (used as ResourceID)
//   MachineType     — full URL; basename is the type (e.g. "n2-standard-4")
//   Status          — "RUNNING", "TERMINATED", "STAGING", "STOPPING"
//   Zone            — full URL; basename is the zone name
//   NetworkInterfaces[0].NetworkIP — primary private IP
//   NetworkInterfaces[0].AccessConfigs[0].NatIP — public IP (if any)
//   NetworkInterfaces[0].Network — VPC self-link
//   NetworkInterfaces[0].Subnetwork — subnet self-link
//   Labels          — map[string]string
//   Tags.Items      — []string (network tags)
//   Metadata.Items  — []MetadataItems{Key, Value} (startup-script, etc.)
//   CreationTimestamp — RFC3339
func (p *GCPTopologyProvider) discoverGCEInstances(ctx context.Context, projectID string) ([]*CloudResource, error) {
	// TODO: initialise GCP Compute Engine client:
	//   import computepb "google.golang.org/genproto/googleapis/cloud/compute/v1"
	//   client, err := compute.NewInstancesRESTClient(ctx, p.clientOptions()...)
	//   if err != nil { return nil, fmt.Errorf("compute client: %w", err) }
	//   defer client.Close()

	// TODO: call AggregatedList and iterate:
	//   req := &computepb.AggregatedListInstancesRequest{Project: projectID}
	//   it  := client.AggregatedList(ctx, req)
	//   for {
	//       pair, err := it.Next()
	//       if err == iterator.Done { break }
	//       if err != nil { return nil, err }
	//       for _, inst := range pair.Value.GetInstances() {
	//           resources = append(resources, p.mapGCEInstance(inst, projectID))
	//       }
	//   }

	log.Printf("GCP topology [%s]: GCE instance discovery (stub — wire compute client)", projectID)
	return []*CloudResource{}, nil
}

// mapGCEInstance converts a GCP Compute Engine Instance API response object
// into the canonical CloudResource representation used throughout Aileron.
//
// The parameter names mirror the fields returned by the REST API / Go protobuf
// client for compute.Instance.  Wire this helper in the TODO loop above once
// the compute client is initialised.
//
// Parameters (all sourced from compute.Instance):
//   selfLink      — instance.SelfLink  (used as stable ResourceID)
//   name          — instance.Name
//   zone          — basename of instance.Zone  (e.g. "us-central1-a")
//   machineType   — basename of instance.MachineType  (e.g. "n2-standard-4")
//   status        — instance.Status  ("RUNNING", "TERMINATED", …)
//   primaryIP     — instance.NetworkInterfaces[0].NetworkIP
//   publicIP      — instance.NetworkInterfaces[0].AccessConfigs[0].NatIP
//   vpcSelfLink   — instance.NetworkInterfaces[0].Network
//   subnetSelfLink — instance.NetworkInterfaces[0].Subnetwork
//   labels        — instance.Labels
func (p *GCPTopologyProvider) mapGCEInstance(
	selfLink, name, zone, machineType, status,
	primaryIP, publicIP,
	vpcSelfLink, subnetSelfLink string,
	labels map[string]string,
	projectID string,
) *CloudResource {
	// Derive the region from the zone (e.g. "us-central1-a" → "us-central1").
	region := zoneToRegion(zone)

	// Normalise status to the canonical Aileron vocabulary.
	normalisedStatus := normaliseGCEStatus(status)

	// Shorten self-links to the resource ID portion for TopologyPath.
	vpcID := baseNameFromSelfLink(vpcSelfLink)
	subnetID := baseNameFromSelfLink(subnetSelfLink)

	// Ensure labels is never nil so callers can safely iterate it.
	if labels == nil {
		labels = make(map[string]string)
	}
	// Propagate well-known metadata into Tags for uniform querying.
	labels["gcp_zone"] = zone
	labels["gcp_machine_type"] = machineType
	labels["gcp_project"] = projectID

	return &CloudResource{
		Provider:     CloudGCP,
		ResourceID:   selfLink,
		ResourceType: GCPResourceGCEInstance,
		Name:         name,
		Region:       region,
		Zone:         zone,
		AccountID:    projectID,
		Tags:         labels,
		Status:       normalisedStatus,
		PublicIP:     publicIP,
		PrivateIP:    primaryIP,
		VPCID:        vpcID,
		SubnetID:     subnetID,
		TopologyPath: fmt.Sprintf("gcp/%s/compute/%s/%s", projectID, zone, name),
	}
}

// ---------------------------------------------------------------------------
// GKE Clusters and Node Pools
// ---------------------------------------------------------------------------

// discoverGKEClusters lists all GKE clusters and synthesises one CloudResource
// per node pool (resource type GCPResourceGKENode) so that the topology graph
// can model node-pool capacity independently.
//
// GCP API:   GET https://container.googleapis.com/v1/projects/{project}/locations/-/clusters
// Go client: containerService.Projects.Locations.Clusters.List("projects/"+projectID+"/locations/-").Context(ctx).Do()
//
// Relevant fields on container.Cluster:
//   Name                     — cluster short name
//   SelfLink                 — full resource URL
//   Location                 — region or zone
//   CurrentMasterVersion     — control-plane K8s version
//   CurrentNodeCount         — total nodes across all node pools
//   Status                   — "RUNNING", "PROVISIONING", "DEGRADED", "ERROR"
//   NodePools[].Name         — node pool name
//   NodePools[].Config.MachineType — machine type for nodes in the pool
//   NodePools[].InitialNodeCount — node count per zone
//   NodePools[].Status       — pool status
//   ResourceLabels           — map[string]string cluster labels
//   Network                  — VPC name
//   Subnetwork               — subnet name
func (p *GCPTopologyProvider) discoverGKEClusters(ctx context.Context, projectID string) ([]*CloudResource, error) {
	// TODO: initialise Container client:
	//   import container "google.golang.org/api/container/v1"
	//   svc, err := container.NewService(ctx, p.clientOptions()...)
	//   if err != nil { return nil, fmt.Errorf("container client: %w", err) }

	// TODO: list clusters:
	//   parent := "projects/" + projectID + "/locations/-"
	//   resp, err := svc.Projects.Locations.Clusters.List(parent).Context(ctx).Do()
	//   if err != nil { return nil, fmt.Errorf("GKE list clusters: %w", err) }
	//
	//   for _, cluster := range resp.Clusters {
	//       for _, pool := range cluster.NodePools {
	//           resources = append(resources, p.mapGKENodePool(cluster, pool, projectID))
	//       }
	//   }

	log.Printf("GCP topology [%s]: GKE cluster discovery (stub — wire container client)", projectID)
	return []*CloudResource{}, nil
}

// mapGKENodePool converts a GKE cluster + node pool pair into a CloudResource.
//
// Parameters (sourced from container.Cluster and container.NodePool):
//   clusterSelfLink — cluster.SelfLink
//   clusterName     — cluster.Name
//   location        — cluster.Location (region or zone)
//   poolName        — nodePool.Name
//   machineType     — nodePool.Config.MachineType
//   poolStatus      — nodePool.Status
//   clusterStatus   — cluster.Status
//   k8sVersion      — cluster.CurrentMasterVersion
//   vpcName         — cluster.Network
//   subnetName      — cluster.Subnetwork
//   labels          — cluster.ResourceLabels
//   projectID       — GCP project ID
func (p *GCPTopologyProvider) mapGKENodePool(
	clusterSelfLink, clusterName, location,
	poolName, machineType, poolStatus, clusterStatus, k8sVersion,
	vpcName, subnetName string,
	labels map[string]string,
	projectID string,
) *CloudResource {
	if labels == nil {
		labels = make(map[string]string)
	}
	labels["gcp_project"] = projectID
	labels["gke_cluster"] = clusterName
	labels["gke_node_pool"] = poolName
	labels["k8s_version"] = k8sVersion
	labels["gcp_machine_type"] = machineType

	normalisedStatus := normaliseGKEStatus(poolStatus)

	return &CloudResource{
		Provider:     CloudGCP,
		ResourceID:   fmt.Sprintf("%s/nodePools/%s", clusterSelfLink, poolName),
		ResourceType: GCPResourceGKENode,
		Name:         fmt.Sprintf("%s/%s", clusterName, poolName),
		Region:       location,
		Zone:         location,
		AccountID:    projectID,
		Tags:         labels,
		Status:       normalisedStatus,
		K8sCluster:   clusterName,
		VPCID:        vpcName,
		SubnetID:     subnetName,
		TopologyPath: fmt.Sprintf("gcp/%s/gke/%s/%s", projectID, clusterName, poolName),
	}
}

// ---------------------------------------------------------------------------
// Cloud SQL
// ---------------------------------------------------------------------------

// discoverCloudSQL lists all Cloud SQL instances in a project.
//
// GCP API:   GET https://sqladmin.googleapis.com/sql/v1beta4/projects/{project}/instances
// Go client: sqladminService.Instances.List(projectID).Context(ctx).Do()
//
// Relevant fields on sqladmin.DatabaseInstance:
//   Name               — instance name (short)
//   SelfLink           — full resource URL
//   DatabaseVersion    — e.g. "POSTGRES_15", "MYSQL_8_0", "SQLSERVER_2019_STANDARD"
//   Region             — GCP region
//   GceZone            — primary zone
//   State              — "RUNNABLE", "SUSPENDED", "PENDING_CREATE", "MAINTENANCE",
//                        "FAILED", "UNKNOWN_STATE"
//   Settings.Tier      — machine type / pricing tier (e.g. "db-n1-standard-2")
//   IpAddresses        — []IpMapping{IpAddress, Type} — type is "PRIMARY" or "PRIVATE"
//   Settings.UserLabels — map[string]string
func (p *GCPTopologyProvider) discoverCloudSQL(ctx context.Context, projectID string) ([]*CloudResource, error) {
	// TODO: initialise SQL Admin client:
	//   import sqladmin "google.golang.org/api/sqladmin/v1beta4"
	//   svc, err := sqladmin.NewService(ctx, p.clientOptions()...)
	//   if err != nil { return nil, fmt.Errorf("sqladmin client: %w", err) }

	// TODO: list instances:
	//   resp, err := svc.Instances.List(projectID).Context(ctx).Do()
	//   if err != nil { return nil, fmt.Errorf("Cloud SQL list: %w", err) }
	//   for _, inst := range resp.Items {
	//       resources = append(resources, p.mapCloudSQLInstance(inst, projectID))
	//   }

	log.Printf("GCP topology [%s]: Cloud SQL discovery (stub — wire sqladmin client)", projectID)
	return []*CloudResource{}, nil
}

// mapCloudSQLInstance converts a sqladmin.DatabaseInstance into a CloudResource.
//
// Parameters:
//   selfLink        — instance.SelfLink
//   name            — instance.Name
//   region          — instance.Region
//   zone            — instance.GceZone
//   dbVersion       — instance.DatabaseVersion
//   tier            — instance.Settings.Tier
//   state           — instance.State
//   primaryIP       — the IpAddress where Type == "PRIMARY"
//   privateIP       — the IpAddress where Type == "PRIVATE"
//   labels          — instance.Settings.UserLabels
//   projectID       — GCP project ID
func (p *GCPTopologyProvider) mapCloudSQLInstance(
	selfLink, name, region, zone, dbVersion, tier, state,
	primaryIP, privateIP string,
	labels map[string]string,
	projectID string,
) *CloudResource {
	if labels == nil {
		labels = make(map[string]string)
	}
	labels["gcp_project"] = projectID
	labels["cloud_sql_db_version"] = dbVersion
	labels["cloud_sql_tier"] = tier

	return &CloudResource{
		Provider:     CloudGCP,
		ResourceID:   selfLink,
		ResourceType: GCPResourceCloudSQL,
		Name:         name,
		Region:       region,
		Zone:         zone,
		AccountID:    projectID,
		Tags:         labels,
		Status:       normaliseCloudSQLState(state),
		PublicIP:     primaryIP,
		PrivateIP:    privateIP,
		TopologyPath: fmt.Sprintf("gcp/%s/cloudsql/%s", projectID, name),
	}
}

// ---------------------------------------------------------------------------
// Cloud Storage Buckets
// ---------------------------------------------------------------------------

// discoverGCSBuckets lists all Cloud Storage buckets owned by a project.
//
// GCP API:   GET https://storage.googleapis.com/storage/v1/b?project={project}
// Go client: storageService.Buckets.List(projectID).Context(ctx).Do()
//
// Relevant fields on storage.Bucket:
//   Id              — globally unique bucket name (used as ResourceID)
//   Name            — bucket name (same as Id)
//   Location        — GCS location (may be multi-region, e.g. "US", "EU", or
//                     a single region, e.g. "us-central1")
//   LocationType    — "region", "dual-region", "multi-region"
//   StorageClass    — "STANDARD", "NEARLINE", "COLDLINE", "ARCHIVE"
//   Labels          — map[string]string
//   SelfLink        — https://storage.googleapis.com/... canonical URL
//   TimeCreated     — RFC3339
func (p *GCPTopologyProvider) discoverGCSBuckets(ctx context.Context, projectID string) ([]*CloudResource, error) {
	// TODO: initialise Storage client:
	//   import storage "google.golang.org/api/storage/v1"
	//   svc, err := storage.NewService(ctx, p.clientOptions()...)
	//   if err != nil { return nil, fmt.Errorf("storage client: %w", err) }

	// TODO: list buckets with pagination:
	//   call := svc.Buckets.List(projectID).Context(ctx)
	//   for {
	//       resp, err := call.Do()
	//       if err != nil { return nil, fmt.Errorf("GCS list buckets: %w", err) }
	//       for _, b := range resp.Items {
	//           resources = append(resources, p.mapGCSBucket(b, projectID))
	//       }
	//       if resp.NextPageToken == "" { break }
	//       call = call.PageToken(resp.NextPageToken)
	//   }

	log.Printf("GCP topology [%s]: Cloud Storage discovery (stub — wire storage client)", projectID)
	return []*CloudResource{}, nil
}

// mapGCSBucket converts a storage.Bucket into a CloudResource.
//
// Parameters:
//   bucketID      — bucket.Id  (globally unique; same as name)
//   name          — bucket.Name
//   location      — bucket.Location  (e.g. "us-central1" or "US")
//   locationType  — bucket.LocationType  ("region", "dual-region", "multi-region")
//   storageClass  — bucket.StorageClass
//   labels        — bucket.Labels
//   projectID     — GCP project ID
func (p *GCPTopologyProvider) mapGCSBucket(
	bucketID, name, location, locationType, storageClass string,
	labels map[string]string,
	projectID string,
) *CloudResource {
	if labels == nil {
		labels = make(map[string]string)
	}
	labels["gcp_project"] = projectID
	labels["gcs_storage_class"] = storageClass
	labels["gcs_location_type"] = locationType

	// Normalise multi-region location to a canonical region string.
	region := strings.ToLower(location)

	return &CloudResource{
		Provider:     CloudGCP,
		ResourceID:   bucketID,
		ResourceType: GCPResourceGCSBucket,
		Name:         name,
		Region:       region,
		AccountID:    projectID,
		Tags:         labels,
		Status:       "active",
		TopologyPath: fmt.Sprintf("gcp/%s/gcs/%s", projectID, name),
	}
}

// ---------------------------------------------------------------------------
// Cloud Functions
// ---------------------------------------------------------------------------

// discoverCloudFunctions lists all Cloud Functions (v1 and v2) in a project.
//
// GCP API (v1): GET https://cloudfunctions.googleapis.com/v1/projects/{project}/locations/-/functions
// GCP API (v2): GET https://cloudfunctions.googleapis.com/v2/projects/{project}/locations/-/functions
// Go client v1: cloudfunctionsService.Projects.Locations.Functions.List("projects/"+projectID+"/locations/-").Context(ctx).Do()
//
// Relevant fields on cloudfunctions.CloudFunction (v1):
//   Name             — full resource name:
//                      projects/{project}/locations/{location}/functions/{function}
//   Status           — "ACTIVE", "OFFLINE", "DEPLOY_IN_PROGRESS", "DELETE_IN_PROGRESS",
//                      "UNKNOWN"
//   Runtime          — e.g. "nodejs18", "python311", "go121"
//   EntryPoint       — function entry point (handler name)
//   AvailableMemoryMb — allocated memory
//   HttpsTrigger / EventTrigger — trigger type
//   Labels           — map[string]string
//   Region is derived from the Name field (third path segment).
func (p *GCPTopologyProvider) discoverCloudFunctions(ctx context.Context, projectID string) ([]*CloudResource, error) {
	// TODO: initialise Cloud Functions v1 client:
	//   import cloudfunctions "google.golang.org/api/cloudfunctions/v1"
	//   svc, err := cloudfunctions.NewService(ctx, p.clientOptions()...)
	//   if err != nil { return nil, fmt.Errorf("cloudfunctions client: %w", err) }

	// TODO: list functions (all locations with "-" wildcard):
	//   parent := "projects/" + projectID + "/locations/-"
	//   call := svc.Projects.Locations.Functions.List(parent).Context(ctx)
	//   for {
	//       resp, err := call.Do()
	//       if err != nil { return nil, fmt.Errorf("Cloud Functions list: %w", err) }
	//       for _, fn := range resp.Functions {
	//           resources = append(resources, p.mapCloudFunction(fn, projectID))
	//       }
	//       if resp.NextPageToken == "" { break }
	//       call = call.PageToken(resp.NextPageToken)
	//   }

	log.Printf("GCP topology [%s]: Cloud Functions discovery (stub — wire cloudfunctions client)", projectID)
	return []*CloudResource{}, nil
}

// mapCloudFunction converts a cloudfunctions.CloudFunction into a CloudResource.
//
// Parameters (sourced from cloudfunctions.CloudFunction v1):
//   resourceName  — function.Name  (full GCP resource name)
//   status        — function.Status
//   runtime       — function.Runtime
//   labels        — function.Labels
//   projectID     — GCP project ID
//
// The region is extracted from the resource name path segment at index 3
// (projects/{project}/locations/{region}/functions/{name}).
func (p *GCPTopologyProvider) mapCloudFunction(
	resourceName, status, runtime string,
	labels map[string]string,
	projectID string,
) *CloudResource {
	shortName := baseNameFromResourceName(resourceName) // last path segment
	region := regionFromResourceName(resourceName)      // locations/{region}

	if labels == nil {
		labels = make(map[string]string)
	}
	labels["gcp_project"] = projectID
	labels["cloud_function_runtime"] = runtime

	return &CloudResource{
		Provider:     CloudGCP,
		ResourceID:   resourceName,
		ResourceType: GCPResourceCloudFunc,
		Name:         shortName,
		Region:       region,
		AccountID:    projectID,
		Tags:         labels,
		Status:       normaliseCloudFunctionStatus(status),
		TopologyPath: fmt.Sprintf("gcp/%s/functions/%s/%s", projectID, region, shortName),
	}
}

// ---------------------------------------------------------------------------
// VPC Networks and Subnets
// ---------------------------------------------------------------------------

// discoverVPCNetworks lists all VPC networks and their subnets in a project.
// Each VPC is emitted as a CloudResource with ResourceType "vpc_network"; each
// subnet as a separate resource with ResourceType "vpc_subnet" so that
// topology edges can be drawn between instances and their hosting subnet.
//
// GCP API (networks):  GET https://compute.googleapis.com/compute/v1/projects/{project}/global/networks
// GCP API (subnets):   GET https://compute.googleapis.com/compute/v1/projects/{project}/aggregated/subnetworks
// Go client:
//   computeService.Networks.List(projectID).Context(ctx).Do()
//   computeService.Subnetworks.AggregatedList(projectID).Context(ctx).Do()
//
// Relevant network fields (compute.Network):
//   Name        — VPC name
//   SelfLink    — canonical URL
//   AutoCreateSubnetworks — true for auto-mode VPCs
//   Subnetworks — []string of subnet self-links
//
// Relevant subnet fields (compute.Subnetwork):
//   Name        — subnet name
//   SelfLink    — canonical URL
//   Region      — full URL; basename is region
//   IpCidrRange — CIDR block e.g. "10.0.0.0/24"
//   Network     — parent VPC self-link
func (p *GCPTopologyProvider) discoverVPCNetworks(ctx context.Context, projectID string) ([]*CloudResource, error) {
	// TODO: initialise Compute Engine client (reuse from discoverGCEInstances).

	// TODO: list networks:
	//   networkList, err := computeService.Networks.List(projectID).Context(ctx).Do()
	//   if err != nil { return nil, fmt.Errorf("VPC list: %w", err) }
	//   for _, net := range networkList.Items {
	//       resources = append(resources, p.mapVPCNetwork(net, projectID))
	//   }

	// TODO: list subnets (aggregated):
	//   subnetList, err := computeService.Subnetworks.AggregatedList(projectID).Context(ctx).Do()
	//   if err != nil { return nil, fmt.Errorf("subnet list: %w", err) }
	//   for _, scopedList := range subnetList.Items {
	//       for _, subnet := range scopedList.Subnetworks {
	//           resources = append(resources, p.mapVPCSubnet(subnet, projectID))
	//       }
	//   }

	log.Printf("GCP topology [%s]: VPC/subnet discovery (stub — wire compute client)", projectID)
	return []*CloudResource{}, nil
}

// mapVPCNetwork converts a compute.Network into a CloudResource.
//
// Parameters:
//   selfLink   — network.SelfLink
//   name       — network.Name
//   autoMode   — network.AutoCreateSubnetworks
//   projectID  — GCP project ID
func (p *GCPTopologyProvider) mapVPCNetwork(
	selfLink, name string, autoMode bool, projectID string,
) *CloudResource {
	mode := "custom"
	if autoMode {
		mode = "auto"
	}
	return &CloudResource{
		Provider:     CloudGCP,
		ResourceID:   selfLink,
		ResourceType: "vpc_network",
		Name:         name,
		Region:       "global",
		AccountID:    projectID,
		Tags: map[string]string{
			"gcp_project":  projectID,
			"vpc_mode":     mode,
		},
		Status:       "active",
		VPCID:        name,
		TopologyPath: fmt.Sprintf("gcp/%s/network/%s", projectID, name),
	}
}

// mapVPCSubnet converts a compute.Subnetwork into a CloudResource.
//
// Parameters:
//   selfLink   — subnet.SelfLink
//   name       — subnet.Name
//   region     — basename of subnet.Region
//   cidr       — subnet.IpCidrRange
//   vpcSelfLink — subnet.Network (parent VPC self-link)
//   projectID  — GCP project ID
func (p *GCPTopologyProvider) mapVPCSubnet(
	selfLink, name, region, cidr, vpcSelfLink string,
	projectID string,
) *CloudResource {
	vpcName := baseNameFromSelfLink(vpcSelfLink)
	return &CloudResource{
		Provider:     CloudGCP,
		ResourceID:   selfLink,
		ResourceType: "vpc_subnet",
		Name:         name,
		Region:       region,
		AccountID:    projectID,
		Tags: map[string]string{
			"gcp_project": projectID,
			"cidr":        cidr,
			"vpc":         vpcName,
		},
		Status:       "active",
		VPCID:        vpcName,
		SubnetID:     name,
		TopologyPath: fmt.Sprintf("gcp/%s/subnet/%s/%s", projectID, region, name),
	}
}

// ---------------------------------------------------------------------------
// HTTP client / credentials helper
// ---------------------------------------------------------------------------

// clientOptions returns the google.golang.org/api/option.ClientOptions needed
// to authenticate against GCP APIs.
//
// When GOOGLE_APPLICATION_CREDENTIALS is set, the SDK loads the service-account
// JSON automatically.  When running on GCE/GKE the metadata server provides
// credentials via ADC without any explicit option.
//
// TODO: wire this into each API client constructor above:
//   import "google.golang.org/api/option"
//
//   func (p *GCPTopologyProvider) clientOptions() []option.ClientOption {
//       opts := []option.ClientOption{
//           option.WithScopes(
//               "https://www.googleapis.com/auth/cloud-platform.read-only",
//           ),
//       }
//       if p.cfg.credentialsFile != "" {
//           opts = append(opts, option.WithCredentialsFile(p.cfg.credentialsFile))
//       }
//       return opts
//   }

// ---------------------------------------------------------------------------
// Utility helpers
// ---------------------------------------------------------------------------

// splitTrimmed splits a comma-separated string and trims whitespace from each
// element, discarding empty entries.
func splitTrimmed(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// zoneToRegion derives a GCP region from a zone name by stripping the final
// "-{letter}" suffix (e.g. "us-central1-a" → "us-central1").
func zoneToRegion(zone string) string {
	if idx := strings.LastIndex(zone, "-"); idx > 0 {
		return zone[:idx]
	}
	return zone
}

// baseNameFromSelfLink returns the last path segment of a GCP self-link URL
// (e.g. ".../global/networks/default" → "default").
func baseNameFromSelfLink(selfLink string) string {
	if selfLink == "" {
		return ""
	}
	parts := strings.Split(strings.TrimRight(selfLink, "/"), "/")
	return parts[len(parts)-1]
}

// baseNameFromResourceName returns the last path segment of a GCP resource
// name (e.g. "projects/p/locations/us-central1/functions/my-fn" → "my-fn").
func baseNameFromResourceName(resourceName string) string {
	return baseNameFromSelfLink(resourceName)
}

// regionFromResourceName extracts the region from a GCP resource name of the
// form "projects/{p}/locations/{region}/...".  Returns empty string if the
// pattern does not match.
func regionFromResourceName(resourceName string) string {
	parts := strings.Split(resourceName, "/")
	// projects/{p}/locations/{region}/... → index 3 is the region.
	for i, part := range parts {
		if part == "locations" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// normaliseGCEStatus maps GCE instance status strings to the Aileron vocabulary.
func normaliseGCEStatus(status string) string {
	switch strings.ToUpper(status) {
	case "RUNNING":
		return "running"
	case "TERMINATED", "STOPPING", "SUSPENDING":
		return "stopped"
	case "STAGING", "PROVISIONING", "REPAIRING":
		return "pending"
	default:
		return "unknown"
	}
}

// normaliseGKEStatus maps GKE node pool status strings to the Aileron vocabulary.
func normaliseGKEStatus(status string) string {
	switch strings.ToUpper(status) {
	case "RUNNING":
		return "running"
	case "RUNNING_WITH_ERROR":
		return "degraded"
	case "RECONCILING", "PROVISIONING":
		return "pending"
	case "STOPPING", "ERROR":
		return "stopped"
	default:
		return "unknown"
	}
}

// normaliseCloudSQLState maps Cloud SQL instance state strings to the Aileron vocabulary.
func normaliseCloudSQLState(state string) string {
	switch strings.ToUpper(state) {
	case "RUNNABLE":
		return "running"
	case "SUSPENDED":
		return "stopped"
	case "MAINTENANCE":
		return "maintenance"
	case "PENDING_CREATE", "PENDING_DELETE":
		return "pending"
	case "FAILED":
		return "degraded"
	default:
		return "unknown"
	}
}

// normaliseCloudFunctionStatus maps Cloud Functions status strings to the
// Aileron vocabulary.
func normaliseCloudFunctionStatus(status string) string {
	switch strings.ToUpper(status) {
	case "ACTIVE":
		return "running"
	case "OFFLINE":
		return "stopped"
	case "DEPLOY_IN_PROGRESS":
		return "pending"
	case "DELETE_IN_PROGRESS":
		return "stopped"
	default:
		return "unknown"
	}
}

// credSummary returns a brief human-readable description of the credential
// source for log output — without leaking path details.
func credSummary(credFile string) string {
	if credFile == "" {
		return "application-default-credentials"
	}
	return fmt.Sprintf("service-account-file(%s)", filepath.Base(credFile))
}

// gcpDiscoveryTick is a convenience wrapper that creates a provider from the
// environment and runs a full discovery, returning results and any errors.
// It is intended for use in periodic background reconciliation loops.
func gcpDiscoveryTick(ctx context.Context) ([]*CloudResource, error) {
	p := NewGCPTopologyProvider()
	if p == nil {
		return nil, nil // provider not configured; not an error
	}
	start := time.Now()
	resources, err := p.DiscoverResources(ctx)
	log.Printf("GCP topology tick: %d resources in %v", len(resources), time.Since(start).Round(time.Millisecond))
	return resources, err
}
