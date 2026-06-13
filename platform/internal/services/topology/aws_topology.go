package topology

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

// ============================================================================
// AWS RESOURCE TYPE CONSTANTS
// ============================================================================

const (
	AWSResourceEC2      = "ec2_instance"
	AWSResourceEKS      = "eks_node"
	AWSResourceRDS      = "rds_instance"
	AWSResourceLambda   = "lambda_function"
	AWSResourceELB      = "load_balancer"
	AWSResourceS3       = "s3_bucket"
	AWSResourceSQS      = "sqs_queue"
	AWSResourceDynamoDB = "dynamodb_table"
)

// ============================================================================
// CONFIG
// ============================================================================

// awsConfig holds the resolved provider configuration.  It is populated once
// in NewAWSTopologyProvider from environment variables so the rest of the code
// does not need to call os.Getenv repeatedly.
type awsConfig struct {
	accessKeyID     string
	secretAccessKey string
	sessionToken    string // optional — present for assumed-role / STS credentials
	defaultRegion   string
	regions         []string // derived from AWS_REGIONS or defaultRegion
	accountFilter   string   // optional — when set, only resources in this account are returned
	enabled         bool     // reflects AILERON_AWS_ENABLED == "true" && accessKeyID != ""
}

// ============================================================================
// PROVIDER
// ============================================================================

// AWSTopologyProvider discovers AWS resources across one or more regions and
// converts them into CloudResource slices suitable for ingestion into the
// Aileron infrastructure topology graph.
//
// It implements the CloudTopologyProvider interface defined in cloud_provider.go.
//
// API calls are intentionally left as TODOs — the production implementation
// should integrate the aws-sdk-go-v2 module once it is added to go.mod.  The
// mapping helpers (mapEC2Instance, mapEKSCluster, …) show the exact field
// translations so they are ready to wire up without further design work.
type AWSTopologyProvider struct {
	cfg awsConfig
}

// Compile-time assertion: AWSTopologyProvider must satisfy CloudTopologyProvider.
var _ CloudTopologyProvider = (*AWSTopologyProvider)(nil)

// NewAWSTopologyProvider reads AWS configuration from environment variables and
// returns a fully initialised (but not yet connected) provider.
//
// Recognised environment variables:
//   - AILERON_AWS_ENABLED        — must be "true" to enable discovery
//   - AWS_ACCESS_KEY_ID          — required when enabled
//   - AWS_SECRET_ACCESS_KEY      — required when enabled
//   - AWS_SESSION_TOKEN          — optional, for STS / assumed-role credentials
//   - AWS_DEFAULT_REGION         — default "us-east-1"
//   - AWS_REGIONS                — comma-separated list of regions to discover;
//     defaults to AWS_DEFAULT_REGION when absent
//   - AWS_ACCOUNT_FILTER         — optional 12-digit account ID filter
func NewAWSTopologyProvider() *AWSTopologyProvider {
	defaultRegion := os.Getenv("AWS_DEFAULT_REGION")
	if defaultRegion == "" {
		defaultRegion = "us-east-1"
	}

	regionsEnv := os.Getenv("AWS_REGIONS")
	var regions []string
	if regionsEnv != "" {
		for _, r := range strings.Split(regionsEnv, ",") {
			r = strings.TrimSpace(r)
			if r != "" {
				regions = append(regions, r)
			}
		}
	}
	if len(regions) == 0 {
		regions = []string{defaultRegion}
	}

	accessKeyID := os.Getenv("AWS_ACCESS_KEY_ID")
	enabled := os.Getenv("AILERON_AWS_ENABLED") == "true" && accessKeyID != ""

	return &AWSTopologyProvider{
		cfg: awsConfig{
			accessKeyID:     accessKeyID,
			secretAccessKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
			sessionToken:    os.Getenv("AWS_SESSION_TOKEN"),
			defaultRegion:   defaultRegion,
			regions:         regions,
			accountFilter:   os.Getenv("AWS_ACCOUNT_FILTER"),
			enabled:         enabled,
		},
	}
}

// ============================================================================
// CloudTopologyProvider interface implementation
// ============================================================================

// Provider returns the CloudAWS provider identifier.
func (p *AWSTopologyProvider) Provider() CloudProvider {
	return CloudAWS
}

// IsConfigured returns true when the provider has been enabled and the minimum
// required credentials are present.  Callers should check this before
// scheduling a discovery run.
func (p *AWSTopologyProvider) IsConfigured() bool {
	return os.Getenv("AILERON_AWS_ENABLED") == "true" && os.Getenv("AWS_ACCESS_KEY_ID") != ""
}

// DiscoverResources queries all supported AWS services across all configured
// regions and returns a deduplicated slice of *CloudResource.
//
// Context cancellation is respected: each per-region, per-service iteration
// checks ctx.Err() before proceeding so the caller can abort cleanly.
func (p *AWSTopologyProvider) DiscoverResources(ctx context.Context) ([]*CloudResource, error) {
	if !p.IsConfigured() {
		return nil, fmt.Errorf("aws topology provider: not configured (set AILERON_AWS_ENABLED=true and AWS_ACCESS_KEY_ID)")
	}

	var all []*CloudResource

	for _, region := range p.cfg.regions {
		if err := ctx.Err(); err != nil {
			return all, fmt.Errorf("aws topology discovery cancelled: %w", err)
		}

		log.Printf("AWS topology discovery: region=%s", region)

		resources, err := p.discoverRegion(ctx, region)
		if err != nil {
			// Log per-region failures but continue with remaining regions so a
			// single unreachable region does not abort the whole discovery run.
			log.Printf("AWS topology discovery: region=%s error: %v", region, err)
			continue
		}

		all = append(all, resources...)
	}

	return all, nil
}

// GetResource returns a specific AWS resource by its provider-native ID.
// It searches all configured regions until the resource is found.
func (p *AWSTopologyProvider) GetResource(ctx context.Context, resourceID string) (*CloudResource, error) {
	if !p.IsConfigured() {
		return nil, fmt.Errorf("aws topology provider: not configured")
	}

	resources, err := p.DiscoverResources(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range resources {
		if r.ResourceID == resourceID {
			return r, nil
		}
	}
	return nil, nil
}

// HealthCheck verifies that the AWS credentials are present and returns nil
// when the provider is reachable.
//
// TODO: integrate aws-sdk-go-v2 — call sts.GetCallerIdentity to validate credentials.
func (p *AWSTopologyProvider) HealthCheck(_ context.Context) error {
	if !p.IsConfigured() {
		return fmt.Errorf("aws provider not configured: AILERON_AWS_ENABLED=%q AWS_ACCESS_KEY_ID present=%v",
			os.Getenv("AILERON_AWS_ENABLED"), os.Getenv("AWS_ACCESS_KEY_ID") != "")
	}
	// TODO: integrate aws-sdk-go-v2 or call AWS REST API directly
	// cfg, _ := awsconfig.LoadDefaultConfig(ctx, ...)
	// stsClient := sts.NewFromConfig(cfg)
	// _, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	// return err
	return nil
}

// ============================================================================
// INTERNAL: Regions accessor
// ============================================================================

// Regions returns the list of AWS regions that will be scanned.
func (p *AWSTopologyProvider) Regions() []string {
	return p.cfg.regions
}

// ============================================================================
// INTERNAL: Per-service discovery stubs
// ============================================================================

// discoverRegion runs discovery for all supported resource classes in a single
// AWS region and aggregates the results.
func (p *AWSTopologyProvider) discoverRegion(ctx context.Context, region string) ([]*CloudResource, error) {
	var resources []*CloudResource

	type discoveryFn func(ctx context.Context, region string) ([]*CloudResource, error)

	phases := []struct {
		name string
		fn   discoveryFn
	}{
		{"EC2 instances", p.discoverEC2},
		{"EKS clusters", p.discoverEKS},
		{"RDS instances", p.discoverRDS},
		{"Lambda functions", p.discoverLambda},
		{"ELB/ALB load balancers", p.discoverELB},
		{"VPCs and Subnets", p.discoverVPC},
	}

	for _, phase := range phases {
		if err := ctx.Err(); err != nil {
			return resources, fmt.Errorf("cancelled before %s: %w", phase.name, err)
		}

		items, err := phase.fn(ctx, region)
		if err != nil {
			log.Printf("AWS topology: region=%s phase=%q error: %v (skipping)", region, phase.name, err)
			continue
		}
		resources = append(resources, items...)
	}

	return resources, nil
}

// discoverEC2 discovers EC2 instances in region via DescribeInstances.
func (p *AWSTopologyProvider) discoverEC2(ctx context.Context, region string) ([]*CloudResource, error) {
	log.Printf("AWS EC2: discovering instances in %s", region)

	// TODO: integrate aws-sdk-go-v2 or call AWS REST API directly
	// Example (aws-sdk-go-v2):
	//   cfg, _ := awsconfig.LoadDefaultConfig(ctx,
	//       awsconfig.WithRegion(region),
	//       awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
	//           p.cfg.accessKeyID, p.cfg.secretAccessKey, p.cfg.sessionToken)))
	//   client := ec2.NewFromConfig(cfg)
	//   paginator := ec2.NewDescribeInstancesPaginator(client, &ec2.DescribeInstancesInput{})
	//   for paginator.HasMorePages() {
	//       page, err := paginator.NextPage(ctx)
	//       ...
	//       for _, reservation := range page.Reservations {
	//           for _, instance := range reservation.Instances {
	//               resources = append(resources, p.mapEC2Instance(instance, region))
	//           }
	//       }
	//   }

	return []*CloudResource{}, nil
}

// discoverEKS discovers EKS clusters in region via ListClusters + DescribeCluster.
func (p *AWSTopologyProvider) discoverEKS(ctx context.Context, region string) ([]*CloudResource, error) {
	log.Printf("AWS EKS: discovering clusters in %s", region)

	// TODO: integrate aws-sdk-go-v2 or call AWS REST API directly
	// Example:
	//   client := eks.NewFromConfig(cfg)
	//   list, _ := client.ListClusters(ctx, &eks.ListClustersInput{})
	//   for _, clusterName := range list.Clusters {
	//       desc, _ := client.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: &clusterName})
	//       resources = append(resources, p.mapEKSCluster(desc.Cluster, region))
	//   }

	return []*CloudResource{}, nil
}

// discoverRDS discovers RDS instances in region via DescribeDBInstances.
func (p *AWSTopologyProvider) discoverRDS(ctx context.Context, region string) ([]*CloudResource, error) {
	log.Printf("AWS RDS: discovering DB instances in %s", region)

	// TODO: integrate aws-sdk-go-v2 or call AWS REST API directly
	// Example:
	//   client := rds.NewFromConfig(cfg)
	//   paginator := rds.NewDescribeDBInstancesPaginator(client, &rds.DescribeDBInstancesInput{})
	//   for paginator.HasMorePages() {
	//       page, _ := paginator.NextPage(ctx)
	//       for _, db := range page.DBInstances {
	//           resources = append(resources, p.mapRDSInstance(db, region))
	//       }
	//   }

	return []*CloudResource{}, nil
}

// discoverLambda discovers Lambda functions in region via ListFunctions.
func (p *AWSTopologyProvider) discoverLambda(ctx context.Context, region string) ([]*CloudResource, error) {
	log.Printf("AWS Lambda: discovering functions in %s", region)

	// TODO: integrate aws-sdk-go-v2 or call AWS REST API directly
	// Example:
	//   client := lambda.NewFromConfig(cfg)
	//   paginator := lambda.NewListFunctionsPaginator(client, &lambda.ListFunctionsInput{})
	//   for paginator.HasMorePages() {
	//       page, _ := paginator.NextPage(ctx)
	//       for _, fn := range page.Functions {
	//           resources = append(resources, p.mapLambdaFunction(fn, region))
	//       }
	//   }

	return []*CloudResource{}, nil
}

// discoverELB discovers Classic ELBs and ALBs/NLBs in region via DescribeLoadBalancers.
func (p *AWSTopologyProvider) discoverELB(ctx context.Context, region string) ([]*CloudResource, error) {
	log.Printf("AWS ELB/ALB: discovering load balancers in %s", region)

	// TODO: integrate aws-sdk-go-v2 or call AWS REST API directly
	// Classic ELB (elb v1):
	//   client := elasticloadbalancing.NewFromConfig(cfg)
	//   out, _ := client.DescribeLoadBalancers(ctx, &elasticloadbalancing.DescribeLoadBalancersInput{})
	// ALB/NLB (elbv2):
	//   client2 := elasticloadbalancingv2.NewFromConfig(cfg)
	//   paginator := elasticloadbalancingv2.NewDescribeLoadBalancersPaginator(client2, ...)
	//   for paginator.HasMorePages() { ... p.mapLoadBalancer(lb, region) }

	return []*CloudResource{}, nil
}

// discoverVPC discovers VPCs and their subnets in region via DescribeVpcs / DescribeSubnets.
func (p *AWSTopologyProvider) discoverVPC(ctx context.Context, region string) ([]*CloudResource, error) {
	log.Printf("AWS VPC: discovering VPCs and subnets in %s", region)

	// TODO: integrate aws-sdk-go-v2 or call AWS REST API directly
	// Example:
	//   client := ec2.NewFromConfig(cfg)
	//   vpcs, _ := client.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{})
	//   for _, vpc := range vpcs.Vpcs {
	//       resources = append(resources, p.mapVPC(vpc, region))
	//   }
	//   subnets, _ := client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{})
	//   for _, subnet := range subnets.Subnets {
	//       resources = append(resources, p.mapSubnet(subnet, region))
	//   }

	return []*CloudResource{}, nil
}

// ============================================================================
// MAPPING HELPERS
//
// These helpers document the exact field translations from AWS SDK response
// types to CloudResource.  They are ready to wire up once the SDK calls above
// are implemented — replace the rawXxx stand-in types with the real SDK types
// from aws-sdk-go-v2 when it is added to go.mod.
// ============================================================================

// rawEC2Instance is a stand-in for types.Instance from aws-sdk-go-v2/service/ec2.
// Replace with the real type when the SDK is added to go.mod.
type rawEC2Instance struct {
	InstanceId         string
	InstanceType       string
	State              struct{ Name string }
	PublicIpAddress    string
	PrivateIpAddress   string
	VpcId              string
	SubnetId           string
	ImageId            string
	LaunchTime         time.Time
	Tags               []rawAWSTag
	IamInstanceProfile *struct{ Arn string }
}

// mapEC2Instance converts an EC2 DescribeInstances response item to a *CloudResource.
func (p *AWSTopologyProvider) mapEC2Instance(inst rawEC2Instance, region string) *CloudResource {
	tags := rawTagsToMap(inst.Tags)
	name := tags["Name"]
	if name == "" {
		name = inst.InstanceId
	}

	return &CloudResource{
		Provider:     CloudAWS,
		ResourceID:   inst.InstanceId,
		ResourceType: AWSResourceEC2,
		Name:         name,
		Region:       region,
		AccountID:    p.cfg.accountFilter,
		Status:       normaliseEC2State(inst.State.Name),
		PublicIP:     inst.PublicIpAddress,
		PrivateIP:    inst.PrivateIpAddress,
		VPCID:        inst.VpcId,
		SubnetID:     inst.SubnetId,
		Tags:         tags,
		TopologyPath: fmt.Sprintf("aws/%s/ec2/%s", region, inst.InstanceId),
	}
}

// rawEKSCluster is a stand-in for types.Cluster from aws-sdk-go-v2/service/eks.
type rawEKSCluster struct {
	Name               string
	Arn                string
	Status             string
	Version            string
	Endpoint           string
	RoleArn            string
	ResourcesVpcConfig struct {
		VpcId     string
		SubnetIds []string
	}
	Tags      map[string]string
	CreatedAt time.Time
}

// mapEKSCluster converts an EKS DescribeCluster response to a *CloudResource.
func (p *AWSTopologyProvider) mapEKSCluster(cluster rawEKSCluster, region string) *CloudResource {
	return &CloudResource{
		Provider:     CloudAWS,
		ResourceID:   cluster.Name,
		ResourceType: AWSResourceEKS,
		Name:         cluster.Name,
		Region:       region,
		AccountID:    p.cfg.accountFilter,
		Status:       normaliseEKSStatus(cluster.Status),
		VPCID:        cluster.ResourcesVpcConfig.VpcId,
		Tags:         cluster.Tags,
		TopologyPath: fmt.Sprintf("aws/%s/eks/%s", region, cluster.Name),
	}
}

// rawRDSInstance is a stand-in for types.DBInstance from aws-sdk-go-v2/service/rds.
type rawRDSInstance struct {
	DBInstanceIdentifier string
	DBInstanceArn        string
	DBInstanceStatus     string
	Engine               string
	EngineVersion        string
	DBInstanceClass      string
	MultiAZ              bool
	StorageType          string
	AllocatedStorage     int32
	DBName               string
	Endpoint             struct {
		Address string
		Port    int32
	}
	DBSubnetGroup      struct{ VpcId string }
	MasterUsername     string
	InstanceCreateTime time.Time
	TagList            []rawAWSTag
}

// mapRDSInstance converts an RDS DescribeDBInstances item to a *CloudResource.
func (p *AWSTopologyProvider) mapRDSInstance(db rawRDSInstance, region string) *CloudResource {
	tags := rawTagsToMap(db.TagList)
	name := tags["Name"]
	if name == "" {
		name = db.DBInstanceIdentifier
	}

	return &CloudResource{
		Provider:     CloudAWS,
		ResourceID:   db.DBInstanceIdentifier,
		ResourceType: AWSResourceRDS,
		Name:         name,
		Region:       region,
		AccountID:    p.cfg.accountFilter,
		Status:       normaliseRDSStatus(db.DBInstanceStatus),
		VPCID:        db.DBSubnetGroup.VpcId,
		Tags:         tags,
		TopologyPath: fmt.Sprintf("aws/%s/rds/%s", region, db.DBInstanceIdentifier),
	}
}

// rawLambdaFunction is a stand-in for types.FunctionConfiguration from aws-sdk-go-v2/service/lambda.
type rawLambdaFunction struct {
	FunctionName string
	FunctionArn  string
	Runtime      string
	Handler      string
	Role         string
	MemorySize   int32
	Timeout      int32
	CodeSize     int64
	Description  string
	LastModified string
	VpcConfig    *struct {
		VpcId     string
		SubnetIds []string
	}
}

// mapLambdaFunction converts a Lambda ListFunctions item to a *CloudResource.
func (p *AWSTopologyProvider) mapLambdaFunction(fn rawLambdaFunction, region string) *CloudResource {
	vpcID := ""
	if fn.VpcConfig != nil {
		vpcID = fn.VpcConfig.VpcId
	}

	return &CloudResource{
		Provider:     CloudAWS,
		ResourceID:   fn.FunctionName,
		ResourceType: AWSResourceLambda,
		Name:         fn.FunctionName,
		Region:       region,
		AccountID:    p.cfg.accountFilter,
		Status:       "active",
		VPCID:        vpcID,
		Tags:         map[string]string{},
		TopologyPath: fmt.Sprintf("aws/%s/lambda/%s", region, fn.FunctionName),
	}
}

// rawLoadBalancer is a stand-in for types.LoadBalancer from
// aws-sdk-go-v2/service/elasticloadbalancingv2.
type rawLoadBalancer struct {
	LoadBalancerArn   string
	LoadBalancerName  string
	DNSName           string
	Type              string // "application", "network", "gateway"
	Scheme            string // "internet-facing", "internal"
	State             struct{ Code string }
	VpcId             string
	AvailabilityZones []struct {
		ZoneName string
		SubnetId string
	}
	CreatedTime time.Time
}

// mapLoadBalancer converts an ELBv2 DescribeLoadBalancers item to a *CloudResource.
func (p *AWSTopologyProvider) mapLoadBalancer(lb rawLoadBalancer, region string) *CloudResource {
	return &CloudResource{
		Provider:     CloudAWS,
		ResourceID:   lb.LoadBalancerArn,
		ResourceType: AWSResourceELB,
		Name:         lb.LoadBalancerName,
		Region:       region,
		AccountID:    p.cfg.accountFilter,
		Status:       normaliseELBState(lb.State.Code),
		VPCID:        lb.VpcId,
		Tags:         map[string]string{},
		TopologyPath: fmt.Sprintf("aws/%s/elb/%s", region, lb.LoadBalancerName),
	}
}

// rawVPC is a stand-in for types.Vpc from aws-sdk-go-v2/service/ec2.
type rawVPC struct {
	VpcId     string
	CidrBlock string
	State     string
	IsDefault bool
	OwnerId   string
	Tags      []rawAWSTag
}

// mapVPC converts an EC2 DescribeVpcs item to a *CloudResource.
func (p *AWSTopologyProvider) mapVPC(vpc rawVPC, region string) *CloudResource {
	tags := rawTagsToMap(vpc.Tags)
	name := tags["Name"]
	if name == "" {
		name = vpc.VpcId
	}
	if vpc.IsDefault {
		name = fmt.Sprintf("%s (default)", name)
	}

	return &CloudResource{
		Provider:     CloudAWS,
		ResourceID:   vpc.VpcId,
		ResourceType: "vpc",
		Name:         name,
		Region:       region,
		AccountID:    vpc.OwnerId,
		Status:       vpc.State, // "available", "pending"
		Tags:         tags,
		TopologyPath: fmt.Sprintf("aws/%s/vpc/%s", region, vpc.VpcId),
	}
}

// rawSubnet is a stand-in for types.Subnet from aws-sdk-go-v2/service/ec2.
type rawSubnet struct {
	SubnetId                string
	VpcId                   string
	CidrBlock               string
	AvailabilityZone        string
	State                   string
	AvailableIpAddressCount int32
	DefaultForAz            bool
	Tags                    []rawAWSTag
}

// mapSubnet converts an EC2 DescribeSubnets item to a *CloudResource.
func (p *AWSTopologyProvider) mapSubnet(subnet rawSubnet, region string) *CloudResource {
	tags := rawTagsToMap(subnet.Tags)
	name := tags["Name"]
	if name == "" {
		name = subnet.SubnetId
	}

	return &CloudResource{
		Provider:     CloudAWS,
		ResourceID:   subnet.SubnetId,
		ResourceType: "subnet",
		Name:         name,
		Region:       region,
		Zone:         subnet.AvailabilityZone,
		Status:       subnet.State,
		VPCID:        subnet.VpcId,
		Tags:         tags,
		TopologyPath: fmt.Sprintf("aws/%s/subnet/%s", region, subnet.SubnetId),
	}
}

// ============================================================================
// PRIVATE UTILITIES
// ============================================================================

// rawAWSTag is the common Key/Value tag shape used by EC2 and RDS response types.
// Replace with the real SDK tag types when aws-sdk-go-v2 is added to go.mod.
type rawAWSTag struct {
	Key   string
	Value string
}

// rawTagsToMap converts a []rawAWSTag to a plain map[string]string.
func rawTagsToMap(rawTags []rawAWSTag) map[string]string {
	tags := make(map[string]string, len(rawTags))
	for _, t := range rawTags {
		if t.Key != "" {
			tags[t.Key] = t.Value
		}
	}
	return tags
}

// normaliseEC2State maps EC2 instance state names to a common status vocabulary.
func normaliseEC2State(state string) string {
	switch strings.ToLower(state) {
	case "running":
		return "running"
	case "stopped", "stopping":
		return "stopped"
	case "terminated", "shutting-down":
		return "terminated"
	case "pending":
		return "pending"
	default:
		return state
	}
}

// normaliseEKSStatus maps EKS cluster status values to a common vocabulary.
func normaliseEKSStatus(status string) string {
	switch strings.ToUpper(status) {
	case "ACTIVE":
		return "active"
	case "CREATING":
		return "provisioning"
	case "DELETING":
		return "deleting"
	case "FAILED":
		return "failed"
	case "UPDATING":
		return "updating"
	default:
		return strings.ToLower(status)
	}
}

// normaliseRDSStatus maps RDS DB instance status values to a common vocabulary.
func normaliseRDSStatus(status string) string {
	switch strings.ToLower(status) {
	case "available":
		return "available"
	case "stopped":
		return "stopped"
	case "starting", "rebooting", "modifying", "backing-up", "creating":
		return "transitioning"
	case "deleting", "deleted":
		return "deleted"
	case "failed", "incompatible-parameters", "incompatible-restore":
		return "failed"
	default:
		return status
	}
}

// normaliseELBState maps ELBv2 load balancer state codes to a common vocabulary.
func normaliseELBState(code string) string {
	switch strings.ToLower(code) {
	case "active":
		return "active"
	case "provisioning":
		return "provisioning"
	case "active_impaired":
		return "degraded"
	case "failed":
		return "failed"
	default:
		return code
	}
}
