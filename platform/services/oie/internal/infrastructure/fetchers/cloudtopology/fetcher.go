package cloudtopology

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	domain "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/evidence"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/fetchers"
)

// CloudTopologyFetcher queries the Aileron topology resolve API to enrich
// evidence with cloud resource context. It works for any cloud provider whose
// resources are registered in the topology graph (AWS EC2, GCP GCE, Azure VM,
// AliCloud ECS, etc.).
type CloudTopologyFetcher struct {
	alerthubURL string
	httpClient  *http.Client
}

// New returns a CloudTopologyFetcher that calls the given alerthub base URL.
// An 8-second HTTP timeout is applied to every topology resolve call.
func New(alerthubURL string) *CloudTopologyFetcher {
	return &CloudTopologyFetcher{
		alerthubURL: strings.TrimRight(alerthubURL, "/"),
		httpClient:  &http.Client{Timeout: 8 * time.Second},
	}
}

func (f *CloudTopologyFetcher) ID() fetchers.FetcherID          { return "cloud_topology_context" }
func (f *CloudTopologyFetcher) DependsOn() []fetchers.FetcherID { return nil }
func (f *CloudTopologyFetcher) SourceName() string              { return "cloud_topology" }

// topologyResponse is the partial shape returned by
// GET /api/v1/topology/resolve?topology_path=<path>.
// Only fields relevant to cloud context are decoded.
type topologyResponse struct {
	// Node identity
	NodeID      string `json:"node_id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	EntityType  string `json:"entity_type"`

	// Cloud fields — present when the node represents a cloud resource.
	Provider     string `json:"provider"`
	Region       string `json:"region"`
	AccountID    string `json:"account_id"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Status       string `json:"status"`

	// Kubernetes association — present when the VM is a K8s node.
	K8sClusterRef string `json:"k8s_cluster_ref"`
	K8sNodeName   string `json:"k8s_node_name"`
}

// FetchHistorical resolves cloud resource context for the investigation entity
// by calling the topology API. Returns FetchMissing when:
//   - the topology path cannot be determined, or
//   - the resolved node has no cloud fields (provider/region/resource_type).
func (f *CloudTopologyFetcher) FetchHistorical(ctx context.Context, req *fetchers.FetchRequest) (*fetchers.FetchResult, error) {
	topologyPath := resolveTopologyPath(req)
	if topologyPath == "" {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}

	node, err := f.resolveTopology(ctx, topologyPath)
	if err != nil {
		return &fetchers.FetchResult{Status: domain.FetchError, FetchError: err}, nil
	}

	// Guard: only proceed if the resolved node has cloud context.
	if node.Provider == "" || node.Region == "" || node.ResourceType == "" {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}

	// Prefer display_name, fall back to name, then node_id.
	name := node.DisplayName
	if name == "" {
		name = node.Name
	}
	if name == "" {
		name = node.NodeID
	}

	status := node.Status
	if status == "" {
		status = "unknown"
	}

	description := fmt.Sprintf(
		"Cloud resource: %s/%s/%s/%s — status: %s",
		node.Provider, node.Region, node.ResourceType, name, status,
	)

	payload, _ := json.Marshal(domain.CloudContextPayload{
		Provider:      node.Provider,
		Region:        node.Region,
		AccountID:     node.AccountID,
		ResourceType:  node.ResourceType,
		ResourceID:    node.ResourceID,
		Name:          name,
		Status:        status,
		K8sClusterRef: node.K8sClusterRef,
		TopologyPath:  topologyPath,
	})

	gapSecs := int(time.Since(req.IncidentStartAt).Seconds())

	ev := &domain.Evidence{
		EvidenceType:       domain.TypeCloudContext,
		Source:             "cloud_topology",
		Role:               domain.RoleContext,
		Weight:             0.55,
		EvidenceConfidence: 0.90,
		TemporalMode:       domain.TemporalCurrent,
		TemporalGapSecs:    &gapSecs,
		Description:        description,
		Payload:            payload,
		GatheredAt:         time.Now().UTC(),
		FetchStatus:        domain.FetchSuccess,
		CreatedAt:          time.Now().UTC(),
	}

	outputData := map[string]any{
		"provider":      node.Provider,
		"region":        node.Region,
		"resource_type": node.ResourceType,
		"resource_id":   node.ResourceID,
		"status":        status,
	}

	// Expose K8s cluster association so downstream fetchers can use it.
	if node.K8sClusterRef != "" {
		outputData["k8s_cluster_ref"] = node.K8sClusterRef
	}
	if node.K8sNodeName != "" {
		outputData["k8s_node_name"] = node.K8sNodeName
	}

	return &fetchers.FetchResult{
		Evidence:   []*domain.Evidence{ev},
		OutputData: outputData,
		Status:     domain.FetchSuccess,
	}, nil
}

// resolveTopology calls GET /api/v1/topology/resolve?topology_path=<path>
// and decodes the response into a topologyResponse.
func (f *CloudTopologyFetcher) resolveTopology(ctx context.Context, topologyPath string) (*topologyResponse, error) {
	rawURL := fmt.Sprintf(
		"%s/api/v1/topology/resolve?topology_path=%s",
		f.alerthubURL,
		url.QueryEscape(topologyPath),
	)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building topology resolve request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")

	resp, err := f.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("topology resolve call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// Topology path not registered — treat as missing, not an error.
		return nil, fmt.Errorf("topology path not found: %s", topologyPath)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("topology resolve returned HTTP %d for path %s", resp.StatusCode, topologyPath)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading topology resolve response: %w", err)
	}

	var node topologyResponse
	if err := json.Unmarshal(body, &node); err != nil {
		return nil, fmt.Errorf("parsing topology resolve response: %w", err)
	}
	return &node, nil
}

// resolveTopologyPath determines the topology path to look up.
// Priority:
//  1. Investigation.TopologyPath (set by the alert ingestion pipeline).
//  2. EntityProfile.GraphNodeID (set by EIRS fetcher).
//  3. EntityProfile.CanonicalEntityID as a last resort.
func resolveTopologyPath(req *fetchers.FetchRequest) string {
	if req.Investigation != nil && req.Investigation.TopologyPath != "" {
		return req.Investigation.TopologyPath
	}
	if req.EntityProfile != nil {
		if req.EntityProfile.GraphNodeID != "" {
			return req.EntityProfile.GraphNodeID
		}
		if req.EntityProfile.CanonicalEntityID != "" {
			return req.EntityProfile.CanonicalEntityID
		}
	}
	return ""
}
