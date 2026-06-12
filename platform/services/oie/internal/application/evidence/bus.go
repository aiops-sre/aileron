package evidence

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"database/sql"

	"github.com/google/uuid"
	appinv "github.com/aileron-platform/aileron/platform/services/oie/internal/application/investigation"
	domain_evidence "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/evidence"
	domain_inv "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/investigation"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/circuitbreaker"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/fetchers"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/shared/metrics"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/shared/tracing"
	"go.opentelemetry.io/otel/attribute"
)

// Bus implements application/investigation.EvidenceBus.
// It selects a playbook, executes fetchers as a DAG, and persists all evidence.
type Bus struct {
	dag      *DAG
	playbook *PlaybookRegistry
	repo     domain_evidence.Repository
	cb       *circuitbreaker.CircuitBreaker
	logger   *slog.Logger
	// db is optional — when set, enriches EntityProfile with K8s node and
	// CloudStack VM lookups from the Postgres topology tables.
	db *sql.DB
	// alertHubBaseURL is the AlertHub backend base URL for topology resolution.
	// When set, enrichedEntityProfile calls GET /api/v1/topology/resolve to use
	// AlertHub's Neo4j + Redis topology cache for accurate entity context.
	// This is the primary fix for hallucinated RCAs caused by empty entity profiles.
	alertHubBaseURL string
	httpClient      *http.Client
}

// NewBus constructs the EvidenceBus with all dependencies.
func NewBus(
	fetcherMap map[fetchers.FetcherID]fetchers.EvidenceFetcher,
	playbook *PlaybookRegistry,
	repo domain_evidence.Repository,
	cb *circuitbreaker.CircuitBreaker,
	logger *slog.Logger,
) *Bus {
	return &Bus{
		dag:      NewDAG(fetcherMap, cb, logger),
		playbook: playbook,
		repo:     repo,
		cb:       cb,
		logger:   logger,
	}
}

// SetDB wires an optional Postgres connection for topology-based entity enrichment.
// When set, FetchHistorical will look up the K8s node running a workload and the
// CloudStack VM hosting that node, enabling infrastructure hypothesis scoring.
func (b *Bus) SetDB(db *sql.DB) { b.db = db }

// SetAlertHubURL wires the AlertHub backend URL so enrichedEntityProfile can call
// GET /api/v1/topology/resolve?topology_path=... to use AlertHub's live Neo4j +
// Redis topology cache for accurate entity resolution (pod → node → VM chain).
// This is the primary fix for empty entity profiles that caused hallucinated RCA.
func (b *Bus) SetAlertHubURL(u string) {
	b.alertHubBaseURL = u
	b.httpClient = &http.Client{Timeout: 3 * time.Second}
}

// Execute implements appinv.EvidenceBus.
// It selects the playbook for the investigation, runs all fetchers as a DAG,
// persists evidence to the DB, and returns a summary.
func (b *Bus) Execute(ctx context.Context, req *appinv.EvidenceRequest) (*appinv.EvidenceResult, error) {
	ctx, span := tracing.Tracer().Start(ctx, "evidence.bus.execute")
	defer span.End()

	span.SetAttributes(
		attribute.String("investigation.id", req.InvestigationID.String()),
		attribute.String("playbook.id", req.PlaybookID),
	)

	// Resolve which fetchers to run based on the playbook.
	fetcherIDs := b.playbook.FetchersFor(req.PlaybookID)
	if len(fetcherIDs) == 0 {
		b.logger.WarnContext(ctx, "no fetchers found for playbook",
			"playbook_id", req.PlaybookID,
			"investigation_id", req.InvestigationID,
		)
		fetcherIDs = b.playbook.FetchersFor("PB-DEFAULT-001")
	}

	// Build the investigation proxy for fetchers.
	inv := &domain_inv.Investigation{
		ID:                req.InvestigationID,
		IncidentID:        req.IncidentID,
		PlaybookID:        req.PlaybookID,
		Severity:          req.Severity,
		IncidentStartedAt: req.IncidentStartAt,
	}
	if req.RootEntityID != nil {
		inv.RootEntityID = req.RootEntityID
	}
	if req.RootEntityType != "" {
		inv.RootEntityType = &req.RootEntityType
	}
	if req.FailureClass != "" {
		inv.FailureClass = &req.FailureClass
	}
	if req.Domain != "" {
		inv.Domain = &req.Domain
	}

	runID := uuid.New()

	fetchReq := &fetchers.FetchRequest{
		Investigation:   inv,
		RunID:           runID.String(),
		IncidentStartAt: req.IncidentStartAt,
		// Build EntityProfile from topology_path/correlation_id if EIRS is not configured.
		// This allows K8s and CloudStack fetchers to run without a separate EIRS call.
		EntityProfile: b.enrichedEntityProfile(ctx, req.TopologyPath, req.CorrelationID),
	}

	// Budget: leave 5 seconds for persistence after fetchers complete.
	budget := time.Duration(req.BudgetMs)*time.Millisecond - 5*time.Second
	if budget < 5*time.Second {
		budget = 5 * time.Second
	}
	budgetCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	start := time.Now()

	// Execute all fetchers as a DAG.
	allEvidence, timedOut := b.dag.Execute(budgetCtx, fetchReq, fetcherIDs)

	elapsed := time.Since(start)
	b.logger.InfoContext(ctx, "evidence gathering complete",
		"investigation_id", req.InvestigationID,
		"evidence_count", len(allEvidence),
		"elapsed_ms", elapsed.Milliseconds(),
		"timed_out", timedOut,
	)

	// Set RunID and InvestigationID on all evidence before persistence.
	for i := range allEvidence {
		allEvidence[i].InvestigationID = req.InvestigationID
		allEvidence[i].RunID = runID
	}

	// Persist all evidence in a single batch upsert.
	if err := b.repo.BulkUpsert(ctx, allEvidence); err != nil {
		b.logger.ErrorContext(ctx, "failed to persist evidence",
			"investigation_id", req.InvestigationID, "error", err)
		// Continue — we have in-memory evidence even if persistence failed.
	}

	// Collect unique source names.
	sourceSet := make(map[string]struct{})
	for _, ev := range allEvidence {
		if ev.FetchStatus == domain_evidence.FetchSuccess {
			sourceSet[ev.Source] = struct{}{}
		}
	}
	sources := make([]string, 0, len(sourceSet))
	for s := range sourceSet {
		sources = append(sources, s)
	}

	metrics.InvestigationDuration.WithLabelValues(req.PlaybookID, "evidence_gathered").
		Observe(elapsed.Seconds())

	return &appinv.EvidenceResult{
		EvidenceCount:   len(allEvidence),
		EvidenceSources: sources,
		TimedOut:        timedOut,
	}, nil
}

// enrichedEntityProfile builds an EntityProfile from topology_path and optionally
// enriches it with live topology data via AlertHub's Neo4j + Redis cache, then falls
// back to Postgres topology tables.
func (b *Bus) enrichedEntityProfile(ctx context.Context, topologyPath, correlationID string) *fetchers.EntityProfile {
	profile := buildEntityProfileFromTopologyPath(topologyPath, correlationID)
	if profile == nil {
		return profile
	}

	// ── Primary: AlertHub topology resolve API (Neo4j + Redis cache) ─────────
	// Uses the live topology graph which has accurate pod→node→VM chains built
	// from real cluster state. This is the main fix for empty entity profiles.
	if b.alertHubBaseURL != "" && b.httpClient != nil && topologyPath != "" {
		b.resolveViaTopologyCache(ctx, topologyPath, profile)
	}

	// ── Fallback: Postgres topology tables ────────────────────────────────────
	// Only used if the topology cache didn't resolve the VM/node chain.
	if b.db != nil && profile.K8sNamespace != "" && profile.ResourceName != "" && profile.CloudStackVMName == "" {
		var vmName, nodeName string
		_ = b.db.QueryRowContext(ctx, `
			SELECT vm.name, node.name
			FROM infrastructure_dependency_edges dep1
			JOIN topology_entities node ON node.external_id = dep1.target_id
			  AND node.entity_type = 'k8s_node'
			JOIN infrastructure_dependency_edges dep2 ON dep2.source_id = node.external_id
			  AND dep2.edge_type = 'HOSTED_ON'
			JOIN topology_entities vm ON vm.external_id = dep2.target_id
			  AND vm.entity_type IN ('vm', 'cloudstack_vm', 'virtual_machine')
			JOIN topology_entities workload ON workload.external_id = dep1.source_id
			  AND workload.cluster_name = $1
			  AND (workload.name LIKE $2 OR workload.labels->>'namespace' = $3)
			WHERE dep1.edge_type = 'DEPLOYED_IN'
			LIMIT 1
		`, profile.ClusterRef, profile.ResourceName+"%", profile.K8sNamespace).Scan(&vmName, &nodeName)

		if vmName != "" {
			profile.CloudStackVMName = vmName
			profile.K8sNodeName = nodeName
			b.logger.DebugContext(ctx, "topology enrichment (postgres): found VM for workload",
				"workload", profile.ResourceName, "k8s_node", nodeName, "vm", vmName)
		}
	}
	return profile
}

// resolveViaTopologyCache calls AlertHub's /api/v1/topology/resolve endpoint which
// uses the live Neo4j + Redis topology cache to return the entity's pod→node→VM chain.
func (b *Bus) resolveViaTopologyCache(ctx context.Context, topologyPath string, profile *fetchers.EntityProfile) {
	endpoint := b.alertHubBaseURL + "/api/v1/topology/resolve?topology_path=" + url.QueryEscape(topologyPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return
	}
	resp, err := b.httpClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var resolved struct {
		ClusterID        string `json:"cluster_id"`
		Namespace        string `json:"namespace"`
		ResourceName     string `json:"resource_name"`
		ResourceKind     string `json:"resource_kind"`
		K8sNodeName      string `json:"k8s_node_name"`
		CloudStackVMName string `json:"cloudstack_vm_name"`
		Status           string `json:"status"` // running|stopped|degraded|unknown
		Health           string `json:"health"`  // healthy|degraded|unhealthy|unknown
		ParentID         string `json:"parent_id"`
	}
	if err := json.Unmarshal(body, &resolved); err != nil {
		return
	}

	// Apply the enriched fields — only override when the topology cache found something.
	if resolved.ClusterID != "" && profile.ClusterRef == "" {
		profile.ClusterRef = resolved.ClusterID
	}
	if resolved.Namespace != "" && profile.K8sNamespace == "" {
		profile.K8sNamespace = resolved.Namespace
	}
	if resolved.ResourceName != "" && profile.ResourceName == "" {
		profile.ResourceName = resolved.ResourceName
	}
	if resolved.K8sNodeName != "" {
		profile.K8sNodeName = resolved.K8sNodeName
	}
	if resolved.CloudStackVMName != "" {
		profile.CloudStackVMName = resolved.CloudStackVMName
	}
	b.logger.DebugContext(ctx, "topology cache resolved entity",
		"topology_path", topologyPath,
		"cluster", resolved.ClusterID,
		"namespace", resolved.Namespace,
		"resource", resolved.ResourceName,
		"node", resolved.K8sNodeName,
		"vm", resolved.CloudStackVMName,
		"health", resolved.Health,
	)
}

// buildEntityProfileFromTopologyPath derives an EntityProfile from AlertHub's
// topology_path and correlation_id without a separate EIRS API call.
//
// topology_path formats:
//   "cluster/namespace:issue"                          → cluster, namespace
//   "cluster/namespace/deployment/workload-name"       → cluster, namespace, workload
//   "h:hostname.domain.com"                            → hostname (bare metal)
//   "cluster:issue-description"                        → cluster only
//
// This gives K8s and CloudStack fetchers enough context to query the right cluster.
func buildEntityProfileFromTopologyPath(topologyPath, correlationID string) *fetchers.EntityProfile {
	if topologyPath == "" && correlationID == "" {
		return nil
	}

	profile := &fetchers.EntityProfile{}

	// Parse topology_path
	src := topologyPath
	if src == "" {
		src = correlationID
	}

	// Bare metal: "h:hostname"
	if len(src) > 2 && src[0:2] == "h:" {
		profile.K8sNodeName = src[2:]
		profile.EntityType = "bare_metal"
		return profile
	}

	// K8s format: "cluster/namespace:issue" or "cluster/namespace/kind/name"
	// or "cluster:issue"
	slashIdx := findByte(src, '/')
	if slashIdx > 0 {
		profile.ClusterRef = src[:slashIdx]
		rest := src[slashIdx+1:]
		// Extract namespace
		slashIdx2 := findByte(rest, '/')
		colonIdx := findByte(rest, ':')
		if slashIdx2 > 0 && (colonIdx < 0 || slashIdx2 < colonIdx) {
			profile.K8sNamespace = rest[:slashIdx2]
			rest2 := rest[slashIdx2+1:]
			// Extract workload name (last segment before colon)
			slashIdx3 := findLastByte(rest2, '/')
			colonIdx3 := findByte(rest2, ':')
			if slashIdx3 > 0 {
				// "kind/name" or "kind/name:issue"
				name := rest2[slashIdx3+1:]
				if c := findByte(name, ':'); c > 0 {
					name = name[:c]
				}
				profile.ResourceName = name
			} else if colonIdx3 > 0 {
				profile.ResourceName = rest2[:colonIdx3]
			} else {
				profile.ResourceName = rest2
			}
		} else if colonIdx > 0 {
			profile.K8sNamespace = rest[:colonIdx]
		} else {
			profile.K8sNamespace = rest
		}
		profile.EntityType = "k8s_workload"
	} else if colonIdx := findByte(src, ':'); colonIdx > 0 {
		// "cluster:issue"
		profile.ClusterRef = src[:colonIdx]
		profile.EntityType = "k8s_cluster"
	}

	// If we couldn't extract a cluster, the profile is not useful
	if profile.ClusterRef == "" && profile.K8sNodeName == "" {
		return nil
	}
	return profile
}

func findByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func findLastByte(s string, b byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// persistEvidenceBatch writes evidence in parallel-safe goroutines.
// Evidence pieces from concurrent fetchers may arrive simultaneously.
type evidenceBatch struct {
	mu       sync.Mutex
	evidence []*domain_evidence.Evidence
}

func (b *evidenceBatch) add(ev ...*domain_evidence.Evidence) {
	b.mu.Lock()
	b.evidence = append(b.evidence, ev...)
	b.mu.Unlock()
}

func (b *evidenceBatch) all() []*domain_evidence.Evidence {
	b.mu.Lock()
	defer b.mu.Unlock()
	result := make([]*domain_evidence.Evidence, len(b.evidence))
	copy(result, b.evidence)
	return result
}
