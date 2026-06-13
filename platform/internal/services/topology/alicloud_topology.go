package topology

// ============================================================================
// ALIBABA CLOUD (ALICLOUD) TOPOLOGY PROVIDER
// Discovers: ECS, ACK, RDS, OSS, SLB, Function Compute, VPCs
//
// Required environment variables:
//   ALICLOUD_ACCESS_KEY_ID       — Alibaba Cloud access key ID
//   ALICLOUD_ACCESS_KEY_SECRET   — Alibaba Cloud access key secret
//   ALICLOUD_REGION_IDS          — Comma-separated region IDs (e.g. "cn-hangzhou,cn-beijing")
//   AILERON_ALICLOUD_ENABLED     — Must be "true" to enable this provider
// ============================================================================

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// ============================================================================
// RESOURCE TYPE CONSTANTS
// ============================================================================

const (
	AliCloudResourceECS = "ecs_instance"
	AliCloudResourceACK = "ack_node"
	AliCloudResourceRDS = "ali_rds_instance"
	AliCloudResourceOSS = "oss_bucket"
	AliCloudResourceSLB = "slb_instance"
	AliCloudResourceFC  = "function_compute"
	AliCloudResourceVPC = "vpc"
)

// ============================================================================
// PROVIDER CONFIG
// ============================================================================

// AliCloudConfig holds the configuration for the AliCloud topology provider.
// All fields are sourced from environment variables at construction time.
type AliCloudConfig struct {
	AccessKeyID     string
	AccessKeySecret string
	RegionIDs       []string
}

// aliCloudConfigFromEnv reads provider config from environment variables.
// Returns (config, enabled). Logs a warning and returns enabled=false when
// AILERON_ALICLOUD_ENABLED is not "true" or required credentials are absent.
func aliCloudConfigFromEnv() (AliCloudConfig, bool) {
	if strings.ToLower(strings.TrimSpace(os.Getenv("AILERON_ALICLOUD_ENABLED"))) != "true" {
		return AliCloudConfig{}, false
	}

	akID := strings.TrimSpace(os.Getenv("ALICLOUD_ACCESS_KEY_ID"))
	akSecret := strings.TrimSpace(os.Getenv("ALICLOUD_ACCESS_KEY_SECRET"))

	if akID == "" || akSecret == "" {
		log.Printf("alicloud: ALICLOUD_ACCESS_KEY_ID or ALICLOUD_ACCESS_KEY_SECRET is empty — provider disabled")
		return AliCloudConfig{}, false
	}

	rawRegions := strings.TrimSpace(os.Getenv("ALICLOUD_REGION_IDS"))
	var regions []string
	for _, r := range strings.Split(rawRegions, ",") {
		r = strings.TrimSpace(r)
		if r != "" {
			regions = append(regions, r)
		}
	}
	if len(regions) == 0 {
		// Default to cn-hangzhou when no region list is provided.
		regions = []string{"cn-hangzhou"}
		log.Printf("alicloud: ALICLOUD_REGION_IDS not set — defaulting to cn-hangzhou")
	}

	return AliCloudConfig{
		AccessKeyID:     akID,
		AccessKeySecret: akSecret,
		RegionIDs:       regions,
	}, true
}

// ============================================================================
// RAW RESOURCE TYPES
// These mirror the response shapes of the Alibaba Cloud Go SDK (alibaba-cloud-sdk-go).
// When the real SDK is wired in, replace the stub calls below with actual
// client.<List*> calls and map using the helpers provided.
// ============================================================================

// aliECSInstance represents a raw ECS instance returned by DescribeInstances.
type aliECSInstance struct {
	InstanceID      string
	InstanceName    string
	Status          string // "Running", "Stopped", "Starting", "Stopping"
	RegionID        string
	ZoneID          string
	VPCID           string
	VSwitchID       string
	PrivateIP       string
	PublicIP        string
	CPU             int
	MemoryMB        int
	InstanceType    string
	ImageID         string
	OSName          string
	CreationTime    time.Time
	Tags            map[string]string
}

// aliACKCluster represents a raw ACK (Container Service for K8s) cluster.
type aliACKCluster struct {
	ClusterID    string
	Name         string
	ClusterType  string // "Kubernetes", "ManagedKubernetes", "Serverless"
	State        string // "running", "stopped", "deleted"
	RegionID     string
	VPCID        string
	NodeCount    int
	K8sVersion   string
	CreationTime time.Time
	Tags         map[string]string
}

// aliRDSInstance represents a raw RDS instance.
type aliRDSInstance struct {
	DBInstanceID     string
	DBInstanceDesc   string
	Engine           string // "MySQL", "PostgreSQL", "SQLServer", "MariaDB"
	EngineVersion    string
	DBInstanceStatus string // "Running", "Stopped"
	RegionID         string
	ZoneID           string
	VPCID            string
	DBInstanceClass  string
	PayType          string
	CreationTime     time.Time
	Tags             map[string]string
}

// aliOSSBucket represents a raw OSS bucket.
type aliOSSBucket struct {
	Name         string
	Location     string // e.g. "oss-cn-hangzhou"
	StorageClass string // "Standard", "IA", "Archive"
	CreationDate time.Time
	Versioning   string // "Enabled", "Suspended", ""
	Tags         map[string]string
}

// aliSLBInstance represents a raw Server Load Balancer instance.
type aliSLBInstance struct {
	LoadBalancerID     string
	LoadBalancerName   string
	LoadBalancerStatus string // "active", "inactive", "locked"
	Address            string
	AddressType        string // "internet", "intranet"
	RegionID           string
	VPCID              string
	MasterZoneID       string
	SlaveZoneID        string
	CreateTime         time.Time
	Tags               map[string]string
}

// aliFCService represents a raw Function Compute service.
type aliFCService struct {
	ServiceName  string
	ServiceID    string
	Description  string
	RegionID     string
	FunctionCount int
	LastModified time.Time
	Tags         map[string]string
}

// aliVPC represents a raw Virtual Private Cloud.
type aliVPC struct {
	VPCID       string
	VPCName     string
	Status      string // "Available", "Pending"
	CidrBlock   string
	RegionID    string
	VSwitchIDs  []string
	RouteTableID string
	IsDefault   bool
	CreationTime time.Time
	Tags         map[string]string
}

// ============================================================================
// MAPPING HELPERS
// Each helper converts a raw API response struct into a canonical InfraNode.
// ============================================================================

func mapECSInstanceToNode(inst aliECSInstance) InfraNode {
	status := normalizeAliStatus(inst.Status)
	health := "healthy"
	if status != "running" {
		health = "degraded"
	}

	now := time.Now()
	props := map[string]interface{}{
		"instance_id":   inst.InstanceID,
		"instance_type": inst.InstanceType,
		"zone":          inst.ZoneID,
		"vpc_id":        inst.VPCID,
		"vswitch_id":    inst.VSwitchID,
		"private_ip":    inst.PrivateIP,
		"public_ip":     inst.PublicIP,
		"cpu":           inst.CPU,
		"memory_mb":     inst.MemoryMB,
		"image_id":      inst.ImageID,
		"os_name":       inst.OSName,
		"creation_time": inst.CreationTime.Format(time.RFC3339),
	}
	for k, v := range inst.Tags {
		props["tag:"+k] = v
	}

	labels := map[string]string{
		"region":    inst.RegionID,
		"zone":      inst.ZoneID,
		"vpc_id":    inst.VPCID,
		"provider":  "alicloud",
	}
	for k, v := range inst.Tags {
		labels[k] = v
	}

	return InfraNode{
		ID:            fmt.Sprintf("alicloud-ecs-%s", inst.InstanceID),
		Name:          coalesceStr(inst.InstanceName, inst.InstanceID),
		Type:          AliCloudResourceECS,
		Layer:         LayerVM,
		Region:        inst.RegionID,
		ParentID:      fmt.Sprintf("alicloud-vpc-%s", inst.VPCID),
		Status:        status,
		HealthStatus:  health,
		Properties:    props,
		Labels:        labels,
		Dependencies:  []string{fmt.Sprintf("alicloud-vpc-%s", inst.VPCID)},
		Dependents:    []string{},
		LastDiscovery: now,
		CreatedAt:     inst.CreationTime,
		UpdatedAt:     now,
	}
}

func mapACKClusterToNode(cluster aliACKCluster) InfraNode {
	status := normalizeAliStatus(cluster.State)
	health := "healthy"
	if status != "running" {
		health = "degraded"
	}

	now := time.Now()
	props := map[string]interface{}{
		"cluster_id":   cluster.ClusterID,
		"cluster_type": cluster.ClusterType,
		"vpc_id":       cluster.VPCID,
		"node_count":   cluster.NodeCount,
		"k8s_version":  cluster.K8sVersion,
		"creation_time": cluster.CreationTime.Format(time.RFC3339),
	}
	for k, v := range cluster.Tags {
		props["tag:"+k] = v
	}

	labels := map[string]string{
		"region":   cluster.RegionID,
		"vpc_id":   cluster.VPCID,
		"provider": "alicloud",
	}
	for k, v := range cluster.Tags {
		labels[k] = v
	}

	return InfraNode{
		ID:            fmt.Sprintf("alicloud-ack-%s", cluster.ClusterID),
		Name:          coalesceStr(cluster.Name, cluster.ClusterID),
		Type:          AliCloudResourceACK,
		Layer:         LayerKubernetes,
		Region:        cluster.RegionID,
		ParentID:      fmt.Sprintf("alicloud-vpc-%s", cluster.VPCID),
		Status:        status,
		HealthStatus:  health,
		Properties:    props,
		Labels:        labels,
		Dependencies:  []string{fmt.Sprintf("alicloud-vpc-%s", cluster.VPCID)},
		Dependents:    []string{},
		LastDiscovery: now,
		CreatedAt:     cluster.CreationTime,
		UpdatedAt:     now,
	}
}

func mapRDSInstanceToNode(inst aliRDSInstance) InfraNode {
	status := normalizeAliStatus(inst.DBInstanceStatus)
	health := "healthy"
	if status != "running" {
		health = "degraded"
	}

	now := time.Now()
	props := map[string]interface{}{
		"db_instance_id":    inst.DBInstanceID,
		"engine":            inst.Engine,
		"engine_version":    inst.EngineVersion,
		"db_instance_class": inst.DBInstanceClass,
		"zone":              inst.ZoneID,
		"vpc_id":            inst.VPCID,
		"pay_type":          inst.PayType,
		"creation_time":     inst.CreationTime.Format(time.RFC3339),
	}
	for k, v := range inst.Tags {
		props["tag:"+k] = v
	}

	labels := map[string]string{
		"region":   inst.RegionID,
		"zone":     inst.ZoneID,
		"engine":   inst.Engine,
		"vpc_id":   inst.VPCID,
		"provider": "alicloud",
	}
	for k, v := range inst.Tags {
		labels[k] = v
	}

	return InfraNode{
		ID:            fmt.Sprintf("alicloud-rds-%s", inst.DBInstanceID),
		Name:          coalesceStr(inst.DBInstanceDesc, inst.DBInstanceID),
		Type:          AliCloudResourceRDS,
		Layer:         LayerApplication,
		Region:        inst.RegionID,
		ParentID:      fmt.Sprintf("alicloud-vpc-%s", inst.VPCID),
		Status:        status,
		HealthStatus:  health,
		Properties:    props,
		Labels:        labels,
		Dependencies:  []string{fmt.Sprintf("alicloud-vpc-%s", inst.VPCID)},
		Dependents:    []string{},
		LastDiscovery: now,
		CreatedAt:     inst.CreationTime,
		UpdatedAt:     now,
	}
}

func mapOSSBucketToNode(bucket aliOSSBucket, regionID string) InfraNode {
	now := time.Now()
	props := map[string]interface{}{
		"location":      bucket.Location,
		"storage_class": bucket.StorageClass,
		"versioning":    bucket.Versioning,
		"creation_date": bucket.CreationDate.Format(time.RFC3339),
	}
	for k, v := range bucket.Tags {
		props["tag:"+k] = v
	}

	labels := map[string]string{
		"region":        regionID,
		"location":      bucket.Location,
		"storage_class": bucket.StorageClass,
		"provider":      "alicloud",
	}
	for k, v := range bucket.Tags {
		labels[k] = v
	}

	return InfraNode{
		ID:            fmt.Sprintf("alicloud-oss-%s", bucket.Name),
		Name:          bucket.Name,
		Type:          AliCloudResourceOSS,
		Layer:         LayerStorage,
		Region:        regionID,
		Status:        "running",
		HealthStatus:  "healthy",
		Properties:    props,
		Labels:        labels,
		Dependencies:  []string{},
		Dependents:    []string{},
		LastDiscovery: now,
		CreatedAt:     bucket.CreationDate,
		UpdatedAt:     now,
	}
}

func mapSLBInstanceToNode(slb aliSLBInstance) InfraNode {
	status := normalizeAliStatus(slb.LoadBalancerStatus)
	health := "healthy"
	if status != "running" && status != "active" {
		health = "degraded"
	}

	now := time.Now()
	props := map[string]interface{}{
		"load_balancer_id": slb.LoadBalancerID,
		"address":          slb.Address,
		"address_type":     slb.AddressType,
		"vpc_id":           slb.VPCID,
		"master_zone":      slb.MasterZoneID,
		"slave_zone":       slb.SlaveZoneID,
		"create_time":      slb.CreateTime.Format(time.RFC3339),
	}
	for k, v := range slb.Tags {
		props["tag:"+k] = v
	}

	labels := map[string]string{
		"region":       slb.RegionID,
		"address_type": slb.AddressType,
		"vpc_id":       slb.VPCID,
		"provider":     "alicloud",
	}
	for k, v := range slb.Tags {
		labels[k] = v
	}

	deps := []string{}
	if slb.VPCID != "" {
		deps = append(deps, fmt.Sprintf("alicloud-vpc-%s", slb.VPCID))
	}

	return InfraNode{
		ID:            fmt.Sprintf("alicloud-slb-%s", slb.LoadBalancerID),
		Name:          coalesceStr(slb.LoadBalancerName, slb.LoadBalancerID),
		Type:          AliCloudResourceSLB,
		Layer:         LayerNetwork,
		Region:        slb.RegionID,
		ParentID: func() string {
			if slb.VPCID != "" {
				return fmt.Sprintf("alicloud-vpc-%s", slb.VPCID)
			}
			return ""
		}(),
		Status:        normalizeAliSLBStatus(slb.LoadBalancerStatus),
		HealthStatus:  health,
		Properties:    props,
		Labels:        labels,
		Dependencies:  deps,
		Dependents:    []string{},
		LastDiscovery: now,
		CreatedAt:     slb.CreateTime,
		UpdatedAt:     now,
	}
}

func mapFCServiceToNode(svc aliFCService) InfraNode {
	now := time.Now()
	props := map[string]interface{}{
		"service_id":     svc.ServiceID,
		"description":    svc.Description,
		"function_count": svc.FunctionCount,
		"last_modified":  svc.LastModified.Format(time.RFC3339),
	}
	for k, v := range svc.Tags {
		props["tag:"+k] = v
	}

	labels := map[string]string{
		"region":   svc.RegionID,
		"provider": "alicloud",
	}
	for k, v := range svc.Tags {
		labels[k] = v
	}

	return InfraNode{
		ID:            fmt.Sprintf("alicloud-fc-%s-%s", svc.RegionID, svc.ServiceName),
		Name:          svc.ServiceName,
		Type:          AliCloudResourceFC,
		Layer:         LayerApplication,
		Region:        svc.RegionID,
		Status:        "running",
		HealthStatus:  "healthy",
		Properties:    props,
		Labels:        labels,
		Dependencies:  []string{},
		Dependents:    []string{},
		LastDiscovery: now,
		CreatedAt:     svc.LastModified,
		UpdatedAt:     now,
	}
}

func mapVPCToNode(vpc aliVPC) InfraNode {
	status := normalizeAliStatus(vpc.Status)
	health := "healthy"
	if status != "running" {
		health = "degraded"
	}

	now := time.Now()
	props := map[string]interface{}{
		"vpc_id":         vpc.VPCID,
		"cidr_block":     vpc.CidrBlock,
		"vswitch_ids":    vpc.VSwitchIDs,
		"route_table_id": vpc.RouteTableID,
		"is_default":     vpc.IsDefault,
		"creation_time":  vpc.CreationTime.Format(time.RFC3339),
	}
	for k, v := range vpc.Tags {
		props["tag:"+k] = v
	}

	labels := map[string]string{
		"region":     vpc.RegionID,
		"cidr_block": vpc.CidrBlock,
		"provider":   "alicloud",
	}
	for k, v := range vpc.Tags {
		labels[k] = v
	}

	return InfraNode{
		ID:            fmt.Sprintf("alicloud-vpc-%s", vpc.VPCID),
		Name:          coalesceStr(vpc.VPCName, vpc.VPCID),
		Type:          AliCloudResourceVPC,
		Layer:         LayerNetwork,
		Region:        vpc.RegionID,
		Status:        status,
		HealthStatus:  health,
		Properties:    props,
		Labels:        labels,
		Dependencies:  []string{},
		Dependents:    []string{},
		LastDiscovery: now,
		CreatedAt:     vpc.CreationTime,
		UpdatedAt:     now,
	}
}

// ============================================================================
// STATUS NORMALIZERS
// ============================================================================

// normalizeAliStatus converts Alibaba Cloud resource status strings to the
// canonical Aileron status values ("running", "stopped", "unknown").
func normalizeAliStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "running", "active", "available", "normal", "creating":
		return "running"
	case "stopped", "stopping", "deleted", "deleting", "abnormal", "inactive", "locked":
		return "stopped"
	case "starting":
		return "running"
	default:
		return "unknown"
	}
}

// normalizeAliSLBStatus maps SLB-specific status to Aileron status.
func normalizeAliSLBStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "active":
		return "running"
	case "inactive", "locked":
		return "stopped"
	default:
		return "unknown"
	}
}

// coalesceStr returns the first non-empty string.
func coalesceStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ============================================================================
// STUB API CLIENTS
// Replace the bodies of the fetch* functions with real Alibaba Cloud SDK calls
// once the dependency (github.com/aliyun/alibaba-cloud-sdk-go) is added to go.mod.
// The signatures and return types must remain unchanged.
// ============================================================================

// fetchECSInstances returns all ECS instances in the given region.
// Production: call ecs.DescribeInstances with pagination (PageSize=100).
func fetchECSInstances(_ context.Context, cfg AliCloudConfig, regionID string) ([]aliECSInstance, error) {
	// TODO: replace with:
	//   client, err := ecs.NewClientWithAccessKey(regionID, cfg.AccessKeyID, cfg.AccessKeySecret)
	//   request := ecs.CreateDescribeInstancesRequest()
	//   request.RegionId = regionID
	//   request.PageSize = requests.NewInteger(100)
	//   response, err := client.DescribeInstances(request)
	//   map response.Instances.Instance -> []aliECSInstance
	log.Printf("alicloud[%s]: ECS discovery stub — returning empty list (wire real SDK to populate)", regionID)
	return []aliECSInstance{}, nil
}

// fetchACKClusters returns all ACK clusters in the given region.
// Production: call cs.DescribeClusters (Container Service API).
func fetchACKClusters(_ context.Context, cfg AliCloudConfig, regionID string) ([]aliACKCluster, error) {
	// TODO: replace with:
	//   client, err := cs.NewClientWithAccessKey(regionID, cfg.AccessKeyID, cfg.AccessKeySecret)
	//   request := cs.CreateDescribeClustersRequest()
	//   response, err := client.DescribeClusters(request)
	//   filter by response RegionId
	log.Printf("alicloud[%s]: ACK discovery stub — returning empty list (wire real SDK to populate)", regionID)
	return []aliACKCluster{}, nil
}

// fetchRDSInstances returns all RDS instances in the given region.
// Production: call rds.DescribeDBInstances with pagination.
func fetchRDSInstances(_ context.Context, cfg AliCloudConfig, regionID string) ([]aliRDSInstance, error) {
	// TODO: replace with:
	//   client, err := rds.NewClientWithAccessKey(regionID, cfg.AccessKeyID, cfg.AccessKeySecret)
	//   request := rds.CreateDescribeDBInstancesRequest()
	//   request.RegionId = regionID
	//   response, err := client.DescribeDBInstances(request)
	log.Printf("alicloud[%s]: RDS discovery stub — returning empty list (wire real SDK to populate)", regionID)
	return []aliRDSInstance{}, nil
}

// fetchOSSBuckets returns all OSS buckets accessible to the account.
// OSS buckets are global; we filter by the location prefix matching the region.
// Production: use the OSS SDK (github.com/aliyun/aliyun-oss-go-sdk).
func fetchOSSBuckets(_ context.Context, cfg AliCloudConfig, regionID string) ([]aliOSSBucket, error) {
	// TODO: replace with:
	//   client, err := oss.New("oss-"+regionID+".aliyuncs.com", cfg.AccessKeyID, cfg.AccessKeySecret)
	//   result, err := client.ListBuckets()
	//   filter by bucket.Location == "oss-"+regionID
	log.Printf("alicloud[%s]: OSS discovery stub — returning empty list (wire real SDK to populate)", regionID)
	return []aliOSSBucket{}, nil
}

// fetchSLBInstances returns all SLB instances in the given region.
// Production: call slb.DescribeLoadBalancers with pagination.
func fetchSLBInstances(_ context.Context, cfg AliCloudConfig, regionID string) ([]aliSLBInstance, error) {
	// TODO: replace with:
	//   client, err := slb.NewClientWithAccessKey(regionID, cfg.AccessKeyID, cfg.AccessKeySecret)
	//   request := slb.CreateDescribeLoadBalancersRequest()
	//   request.RegionId = regionID
	//   response, err := client.DescribeLoadBalancers(request)
	log.Printf("alicloud[%s]: SLB discovery stub — returning empty list (wire real SDK to populate)", regionID)
	return []aliSLBInstance{}, nil
}

// fetchFCServices returns all Function Compute services in the given region.
// Production: use the FC SDK (github.com/aliyun/fc-go-sdk).
func fetchFCServices(_ context.Context, cfg AliCloudConfig, regionID string) ([]aliFCService, error) {
	// TODO: replace with:
	//   client := fc.NewClient("https://"+accountID+"."+regionID+".fc.aliyuncs.com",
	//       fc.WithAccessKey(cfg.AccessKeyID, cfg.AccessKeySecret))
	//   input := fc.NewListServicesInput()
	//   response, err := client.ListServices(input)
	log.Printf("alicloud[%s]: FC discovery stub — returning empty list (wire real SDK to populate)", regionID)
	return []aliFCService{}, nil
}

// fetchVPCs returns all VPCs in the given region.
// Production: call vpc.DescribeVpcs with pagination.
func fetchVPCs(_ context.Context, cfg AliCloudConfig, regionID string) ([]aliVPC, error) {
	// TODO: replace with:
	//   client, err := vpc.NewClientWithAccessKey(regionID, cfg.AccessKeyID, cfg.AccessKeySecret)
	//   request := vpc.CreateDescribeVpcsRequest()
	//   request.RegionId = regionID
	//   response, err := client.DescribeVpcs(request)
	log.Printf("alicloud[%s]: VPC discovery stub — returning empty list (wire real SDK to populate)", regionID)
	return []aliVPC{}, nil
}

// ============================================================================
// ALICLOUD TOPOLOGY PROVIDER
// ============================================================================

// AliCloudTopologyProvider implements the Discoverer interface for Alibaba Cloud.
// It fans out discovery across all configured regions in parallel and returns a
// flat slice of InfraNode values spanning ECS, ACK, RDS, OSS, SLB, FC, and VPC.
//
// Layer mapping:
//   VPC       -> LayerNetwork
//   ECS       -> LayerVM
//   ACK       -> LayerKubernetes
//   SLB       -> LayerNetwork
//   RDS, FC   -> LayerApplication
//   OSS       -> LayerStorage
type AliCloudTopologyProvider struct {
	cfg AliCloudConfig
}

// NewAliCloudTopologyProvider constructs an AliCloudTopologyProvider from
// environment variables.  Returns (nil, false) when the provider is disabled.
func NewAliCloudTopologyProvider() (*AliCloudTopologyProvider, bool) {
	cfg, enabled := aliCloudConfigFromEnv()
	if !enabled {
		return nil, false
	}
	log.Printf("alicloud: topology provider enabled for regions: %s", strings.Join(cfg.RegionIDs, ", "))
	return &AliCloudTopologyProvider{cfg: cfg}, true
}

// GetName satisfies the Discoverer interface.
func (p *AliCloudTopologyProvider) GetName() string {
	return "AliCloud TopologyProvider"
}

// GetLayer satisfies the Discoverer interface.
// Returns LayerVM as the primary layer; the provider also emits nodes for
// LayerNetwork / LayerKubernetes / LayerApplication / LayerStorage.
func (p *AliCloudTopologyProvider) GetLayer() InfrastructureLayer {
	return LayerVM
}

// Discover runs full topology discovery for the given region across all
// AliCloud resource types.  When region is empty, all configured regions are
// discovered and the results are merged.
func (p *AliCloudTopologyProvider) Discover(ctx context.Context, region string) ([]InfraNode, error) {
	regions := p.cfg.RegionIDs
	if region != "" {
		// Constrain to a single region if one is passed by the caller.
		found := false
		for _, r := range p.cfg.RegionIDs {
			if r == region {
				found = true
				break
			}
		}
		if found {
			regions = []string{region}
		} else {
			log.Printf("alicloud: requested region %q is not in configured list — discovering all", region)
		}
	}

	type result struct {
		nodes []InfraNode
		err   error
	}

	ch := make(chan result, len(regions))
	var wg sync.WaitGroup

	for _, r := range regions {
		wg.Add(1)
		go func(regionID string) {
			defer wg.Done()
			nodes, err := p.discoverRegion(ctx, regionID)
			ch <- result{nodes: nodes, err: err}
		}(r)
	}

	wg.Wait()
	close(ch)

	var allNodes []InfraNode
	var errs []string
	for res := range ch {
		if res.err != nil {
			errs = append(errs, res.err.Error())
			continue
		}
		allNodes = append(allNodes, res.nodes...)
	}

	if len(errs) > 0 {
		log.Printf("alicloud: %d region(s) returned errors: %s", len(errs), strings.Join(errs, "; "))
	}

	log.Printf("alicloud: discovered %d nodes across %d region(s)", len(allNodes), len(regions))
	return allNodes, nil
}

// discoverRegion performs full discovery for a single Alibaba Cloud region.
// Each resource type is fetched concurrently to minimise wall-clock time.
func (p *AliCloudTopologyProvider) discoverRegion(ctx context.Context, regionID string) ([]InfraNode, error) {
	type fetchResult struct {
		label string
		nodes []InfraNode
		err   error
	}

	results := make(chan fetchResult, 7)
	var wg sync.WaitGroup

	// VPCs — fetch first so ParentID references are ready; in a stub all are
	// empty anyway, but keeping the semantics correct.
	wg.Add(1)
	go func() {
		defer wg.Done()
		vpcs, err := fetchVPCs(ctx, p.cfg, regionID)
		if err != nil {
			results <- fetchResult{label: "vpc", err: err}
			return
		}
		nodes := make([]InfraNode, 0, len(vpcs))
		for _, v := range vpcs {
			nodes = append(nodes, mapVPCToNode(v))
		}
		results <- fetchResult{label: "vpc", nodes: nodes}
	}()

	// ECS
	wg.Add(1)
	go func() {
		defer wg.Done()
		instances, err := fetchECSInstances(ctx, p.cfg, regionID)
		if err != nil {
			results <- fetchResult{label: "ecs", err: err}
			return
		}
		nodes := make([]InfraNode, 0, len(instances))
		for _, inst := range instances {
			nodes = append(nodes, mapECSInstanceToNode(inst))
		}
		results <- fetchResult{label: "ecs", nodes: nodes}
	}()

	// ACK
	wg.Add(1)
	go func() {
		defer wg.Done()
		clusters, err := fetchACKClusters(ctx, p.cfg, regionID)
		if err != nil {
			results <- fetchResult{label: "ack", err: err}
			return
		}
		nodes := make([]InfraNode, 0, len(clusters))
		for _, c := range clusters {
			nodes = append(nodes, mapACKClusterToNode(c))
		}
		results <- fetchResult{label: "ack", nodes: nodes}
	}()

	// RDS
	wg.Add(1)
	go func() {
		defer wg.Done()
		dbs, err := fetchRDSInstances(ctx, p.cfg, regionID)
		if err != nil {
			results <- fetchResult{label: "rds", err: err}
			return
		}
		nodes := make([]InfraNode, 0, len(dbs))
		for _, db := range dbs {
			nodes = append(nodes, mapRDSInstanceToNode(db))
		}
		results <- fetchResult{label: "rds", nodes: nodes}
	}()

	// OSS
	wg.Add(1)
	go func() {
		defer wg.Done()
		buckets, err := fetchOSSBuckets(ctx, p.cfg, regionID)
		if err != nil {
			results <- fetchResult{label: "oss", err: err}
			return
		}
		nodes := make([]InfraNode, 0, len(buckets))
		for _, b := range buckets {
			nodes = append(nodes, mapOSSBucketToNode(b, regionID))
		}
		results <- fetchResult{label: "oss", nodes: nodes}
	}()

	// SLB
	wg.Add(1)
	go func() {
		defer wg.Done()
		lbs, err := fetchSLBInstances(ctx, p.cfg, regionID)
		if err != nil {
			results <- fetchResult{label: "slb", err: err}
			return
		}
		nodes := make([]InfraNode, 0, len(lbs))
		for _, lb := range lbs {
			nodes = append(nodes, mapSLBInstanceToNode(lb))
		}
		results <- fetchResult{label: "slb", nodes: nodes}
	}()

	// Function Compute
	wg.Add(1)
	go func() {
		defer wg.Done()
		services, err := fetchFCServices(ctx, p.cfg, regionID)
		if err != nil {
			results <- fetchResult{label: "fc", err: err}
			return
		}
		nodes := make([]InfraNode, 0, len(services))
		for _, svc := range services {
			nodes = append(nodes, mapFCServiceToNode(svc))
		}
		results <- fetchResult{label: "fc", nodes: nodes}
	}()

	wg.Wait()
	close(results)

	var allNodes []InfraNode
	var errs []string
	for res := range results {
		if res.err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", res.label, res.err))
			continue
		}
		allNodes = append(allNodes, res.nodes...)
		log.Printf("alicloud[%s]: %s — %d node(s)", regionID, res.label, len(res.nodes))
	}

	if len(errs) > 0 {
		// Non-fatal: return what we have plus a combined error.
		return allNodes, fmt.Errorf("alicloud[%s] partial errors: %s", regionID, strings.Join(errs, "; "))
	}

	return allNodes, nil
}
