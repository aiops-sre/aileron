package topology

import "context"

// CloudProvider identifies a supported cloud platform.
type CloudProvider string

const (
	CloudAWS      CloudProvider = "aws"
	CloudGCP      CloudProvider = "gcp"
	CloudAzure    CloudProvider = "azure"
	CloudAliCloud CloudProvider = "alicloud"
)

// CloudResource represents a discovered cloud infrastructure resource.
type CloudResource struct {
	Provider     CloudProvider
	ResourceID   string
	ResourceType string // e.g. AWSResourceEC2, "gce_instance", "azure_vm"
	Name         string
	Region       string
	Zone         string
	AccountID    string // AWS account, GCP project, Azure subscription, AliCloud account
	Tags         map[string]string
	Status       string // "running", "stopped", "degraded", etc.
	PublicIP     string
	PrivateIP    string
	// K8s cluster association
	K8sCluster string
	K8sNode    string
	// Parent resources
	VPCID    string
	SubnetID string
	// Computed fields
	TopologyPath string // e.g. "aws/us-east-1/ec2/i-123abc"
}

// CloudTopologyProvider fetches topology from a cloud provider's API.
type CloudTopologyProvider interface {
	Provider() CloudProvider
	IsConfigured() bool // returns false if credentials are missing
	// DiscoverResources returns all cloud resources in the configured scope.
	DiscoverResources(ctx context.Context) ([]*CloudResource, error)
	// GetResource returns a specific resource by ID.
	GetResource(ctx context.Context, resourceID string) (*CloudResource, error)
	// HealthCheck returns nil if the cloud API is reachable.
	HealthCheck(ctx context.Context) error
}

// CloudTopologyRegistry manages multiple cloud providers.
type CloudTopologyRegistry struct {
	providers []CloudTopologyProvider
}

func NewCloudTopologyRegistry() *CloudTopologyRegistry {
	return &CloudTopologyRegistry{}
}

func (r *CloudTopologyRegistry) Register(p CloudTopologyProvider) {
	r.providers = append(r.providers, p)
}

func (r *CloudTopologyRegistry) ConfiguredProviders() []CloudTopologyProvider {
	var out []CloudTopologyProvider
	for _, p := range r.providers {
		if p.IsConfigured() {
			out = append(out, p)
		}
	}
	return out
}

func (r *CloudTopologyRegistry) DiscoverAll(ctx context.Context) ([]*CloudResource, error) {
	var all []*CloudResource
	for _, p := range r.ConfiguredProviders() {
		res, err := p.DiscoverResources(ctx)
		if err != nil {
			continue // best-effort: skip failing providers
		}
		all = append(all, res...)
	}
	return all, nil
}

func (r *CloudTopologyRegistry) FindByID(ctx context.Context, resourceID string) (*CloudResource, error) {
	for _, p := range r.ConfiguredProviders() {
		if r, err := p.GetResource(ctx, resourceID); err == nil && r != nil {
			return r, nil
		}
	}
	return nil, nil
}
