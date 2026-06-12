package fetchers

import (
	"context"
	"time"

	domain_evidence "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/evidence"
	domain_inv "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/investigation"
)

// FetcherID uniquely identifies a fetcher within the DAG.
type FetcherID string

// FetchRequest is the input to every fetcher.
type FetchRequest struct {
	Investigation   *domain_inv.Investigation
	RunID           string // unique per investigation run
	IncidentStartAt time.Time
	// EntityProfile is the EIRS-resolved entity context.
	// Set by the EIRS fetcher; downstream fetchers read from UpstreamResults.
	EntityProfile *EntityProfile
	// UpstreamResults holds results from declared dependencies (DependsOn).
	// Populated by the DAG executor before calling this fetcher.
	UpstreamResults map[FetcherID]*FetchResult
}

// EntityProfile is the EIRS-resolved canonical entity context.
type EntityProfile struct {
	CanonicalEntityID string
	EntityType        string
	DisplayName       string
	ClusterRef        string
	NamespaceRef      string
	ResourceName      string
	GraphNodeID       string
	// Source-specific IDs for API lookups.
	K8sNodeName        string
	K8sNamespace       string
	CloudStackVMID     string
	CloudStackVMName   string
	CloudStackHostID   string
}

// FetchResult is the output from every fetcher.
type FetchResult struct {
	Evidence []*domain_evidence.Evidence
	// OutputData carries intermediate data for downstream fetchers.
	// Example: CloudStack VM fetcher puts "host_id" here for the host fetcher.
	OutputData  map[string]any
	Status      domain_evidence.FetchStatus
	DurationMs  int64
	FetchError  error
}

// EvidenceFetcher is the interface every evidence source implements.
// Fetchers declare their dependencies; the DAG executor resolves execution order.
type EvidenceFetcher interface {
	// ID returns the unique fetcher identifier within the DAG.
	ID() FetcherID
	// DependsOn returns the IDs of fetchers that must complete before this one runs.
	// Empty slice means this fetcher can start immediately.
	DependsOn() []FetcherID
	// SourceName identifies the external system for circuit breaker keying.
	SourceName() string
	// FetchHistorical gathers evidence valid at req.IncidentStartAt.
	// This is the primary method called during hypothesis validation.
	FetchHistorical(ctx context.Context, req *FetchRequest) (*FetchResult, error)
}

// UpstreamString extracts a string value from an upstream fetcher's OutputData.
func (r *FetchRequest) UpstreamString(fetcherID FetcherID, key string) string {
	result, ok := r.UpstreamResults[fetcherID]
	if !ok || result == nil || result.OutputData == nil {
		return ""
	}
	v, _ := result.OutputData[key].(string)
	return v
}
