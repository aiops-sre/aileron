package eirs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	domain "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/evidence"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/fetchers"
)

// EntityContextFetcher resolves the investigation's root entity via the EIRS API.
// It populates req.EntityProfile so downstream fetchers (K8s, CloudStack) know
// which cluster/node/VM to query without having to resolve the entity themselves.
type EntityContextFetcher struct {
	eirsBaseURL string
	httpClient  *http.Client
}

func NewEntityContextFetcher(eirsBaseURL string) *EntityContextFetcher {
	return &EntityContextFetcher{
		eirsBaseURL: eirsBaseURL,
		httpClient:  &http.Client{Timeout: 8 * time.Second},
	}
}

func (f *EntityContextFetcher) ID() fetchers.FetcherID          { return "eirs_entity_context" }
func (f *EntityContextFetcher) DependsOn() []fetchers.FetcherID { return nil }
func (f *EntityContextFetcher) SourceName() string              { return "eirs" }

func (f *EntityContextFetcher) FetchHistorical(ctx context.Context, req *fetchers.FetchRequest) (*fetchers.FetchResult, error) {
	inv := req.Investigation
	if inv == nil || inv.RootEntityID == nil {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}
	if f.eirsBaseURL == "" {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}

	url := fmt.Sprintf("%s/api/v1/entities/%s", f.eirsBaseURL, inv.RootEntityID)

	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return &fetchers.FetchResult{Status: domain.FetchError, FetchError: err}, nil
	}
	httpReq.Header.Set("Accept", "application/json")

	resp, err := f.httpClient.Do(httpReq)
	if err != nil {
		return &fetchers.FetchResult{Status: domain.FetchError, FetchError: err}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return &fetchers.FetchResult{Status: domain.FetchMissing}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return &fetchers.FetchResult{
			Status:     domain.FetchError,
			FetchError: fmt.Errorf("EIRS returned HTTP %d", resp.StatusCode),
		}, nil
	}

	body, _ := io.ReadAll(resp.Body)

	var entity struct {
		ID          string `json:"id"`
		EntityType  string `json:"entity_type"`
		DisplayName string `json:"display_name"`
		ClusterRef  string `json:"cluster_ref"`
		NamespaceRef string `json:"namespace_ref"`
		GraphNodeID string `json:"graph_node_id"`
		SourceIDs   map[string]string `json:"source_ids"`
		// Source-specific IDs
		K8sNodeName      string `json:"k8s_node_name"`
		K8sNamespace     string `json:"k8s_namespace"`
		CloudStackVMID   string `json:"cloudstack_vm_id"`
		CloudStackVMName string `json:"cloudstack_vm_name"`
	}
	if err := json.Unmarshal(body, &entity); err != nil {
		return &fetchers.FetchResult{Status: domain.FetchError, FetchError: err}, nil
	}

	// Populate the shared EntityProfile for downstream fetchers.
	req.EntityProfile = &fetchers.EntityProfile{
		CanonicalEntityID: entity.ID,
		EntityType:        entity.EntityType,
		DisplayName:       entity.DisplayName,
		ClusterRef:        entity.ClusterRef,
		NamespaceRef:      entity.NamespaceRef,
		GraphNodeID:       entity.GraphNodeID,
		K8sNodeName:       entity.K8sNodeName,
		K8sNamespace:      entity.K8sNamespace,
		CloudStackVMID:    entity.CloudStackVMID,
		CloudStackVMName:  entity.CloudStackVMName,
	}

	// Derive resource name from display name or K8s node name.
	if req.EntityProfile.K8sNodeName != "" {
		req.EntityProfile.ResourceName = entity.K8sNodeName
	} else {
		req.EntityProfile.ResourceName = entity.DisplayName
	}

	payload, _ := json.Marshal(domain.EntityContextPayload{
		CanonicalEntityID: entity.ID,
		EntityType:        entity.EntityType,
		DisplayName:       entity.DisplayName,
		ClusterRef:        entity.ClusterRef,
		NamespaceRef:      entity.NamespaceRef,
		GraphNodeID:       entity.GraphNodeID,
		ResolutionMethod:  "api",
		Confidence:        1.0,
		SourceIDs:         entity.SourceIDs,
	})

	ev := &domain.Evidence{
		EvidenceType:       domain.TypeEntityContext,
		Source:             "eirs",
		Role:               domain.RoleContext,
		Weight:             0.0,
		TemporalMode:       domain.TemporalCurrent,
		Description:        fmt.Sprintf("EIRS resolved entity: %s (%s) in cluster %s", entity.DisplayName, entity.EntityType, entity.ClusterRef),
		Payload:            payload,
		GatheredAt:         time.Now().UTC(),
		EvidenceConfidence: 1.0,
		FetchStatus:        domain.FetchSuccess,
		CreatedAt:          time.Now().UTC(),
	}

	return &fetchers.FetchResult{
		Evidence: []*domain.Evidence{ev},
		OutputData: map[string]any{
			"entity_type":  entity.EntityType,
			"cluster_ref":  entity.ClusterRef,
			"graph_node_id": entity.GraphNodeID,
			"k8s_node_name": entity.K8sNodeName,
		},
		Status: domain.FetchSuccess,
	}, nil
}
