package rca

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	domain_ev "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/evidence"
	domain_hyp "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/hypothesis"
	domain_rca "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/rca"
)

// CausalChainBuilder constructs the causal chain for an investigation.
// It queries the EIRS API for the entity graph and combines it with
// evidence observations to build a trigger → root_cause → symptom chain.
type CausalChainBuilder struct {
	eirsBaseURL string
	httpClient  *http.Client
}

func NewCausalChainBuilder(eirsBaseURL string) *CausalChainBuilder {
	return &CausalChainBuilder{
		eirsBaseURL: eirsBaseURL,
		httpClient:  &http.Client{Timeout: 8 * time.Second},
	}
}

// Build constructs a CausalChain for the winning hypothesis.
// It uses:
//  1. The winning hypothesis's implicated change (trigger node).
//  2. The root entity from EIRS (root_cause node).
//  3. EIRS parent entities (ancestor chain: e.g. pod → node → VM → BM).
//  4. Evidence observations with timestamps (symptom nodes).
func (b *CausalChainBuilder) Build(
	ctx context.Context,
	winner *domain_hyp.Hypothesis,
	evidence []*domain_ev.Evidence,
	rootEntityID string,
	incidentStartAt time.Time,
) domain_rca.CausalChain {
	var nodes []domain_rca.CausalNode

	// ── Node 1: Trigger (change that initiated the failure) ───────────────────
	// Extract the highest-confidence change event from evidence.
	if triggerNode := extractTriggerNode(evidence, incidentStartAt); triggerNode != nil {
		nodes = append(nodes, *triggerNode)
	}

	// ── Node 2: Root cause entity ─────────────────────────────────────────────
	if rootEntityID != "" && b.eirsBaseURL != "" {
		if entityNode := b.fetchEntityNode(ctx, rootEntityID, domain_rca.RoleRootCause); entityNode != nil {
			nodes = append(nodes, *entityNode)
		}
	}

	// ── Node 3: Parent entities (infrastructure ancestry) ────────────────────
	// Walk up the EIRS topology to find what the root entity runs on.
	if rootEntityID != "" && b.eirsBaseURL != "" {
		parents := b.fetchParentChain(ctx, rootEntityID, 3) // max 3 hops up
		for _, p := range parents {
			p.Role = domain_rca.RoleContext
			nodes = append(nodes, p)
		}
	}

	// ── Node 4: Symptom nodes from evidence ───────────────────────────────────
	// K8s events and pod exit codes are timestamped symptoms.
	for _, ev := range evidence {
		if ev.OccurredAt == nil {
			continue
		}
		if ev.Role != domain_ev.RoleSupports {
			continue
		}
		switch ev.EvidenceType {
		case domain_ev.TypeK8sNodeEvent,
			domain_ev.TypeK8sNodeNotReady,
			domain_ev.TypeK8sPodEvent,
			domain_ev.TypeK8sPodEventCrashLoop,
			domain_ev.TypeK8sPodEventStorage,
			domain_ev.TypeK8sPodEventImage,
			domain_ev.TypeK8sPodEventFailed,
			domain_ev.TypeK8sPodEventEviction,
			domain_ev.TypeK8sPodEventPending,
			domain_ev.TypeK8sOOMKill,
			domain_ev.TypeNetAppAggregateState,
			domain_ev.TypeNetAppSVMState:
			nodes = append(nodes, domain_rca.CausalNode{
				Role:        domain_rca.RoleSymptom,
				OccurredAt:  ev.OccurredAt,
				Description: ev.Description,
			})
		}
	}

	// Sort nodes chronologically (trigger → root → symptoms).
	sortCausalNodes(nodes)

	return domain_rca.CausalChain{Nodes: nodes}
}

// BuildTimeline constructs the pre-incident timeline from evidence.
func (b *CausalChainBuilder) BuildTimeline(
	evidence []*domain_ev.Evidence,
	incidentStartAt time.Time,
) []domain_rca.TimelineEvent {
	var events []domain_rca.TimelineEvent

	for _, ev := range evidence {
		if ev.OccurredAt == nil {
			continue
		}
		gap := ev.OccurredAt.Sub(incidentStartAt)
		// Only include events within [-30min, +10min] of the incident.
		if gap < -30*time.Minute || gap > 10*time.Minute {
			continue
		}

		category := categoryForEvidenceType(ev.EvidenceType)
		significance := significanceForEvidence(ev)

		events = append(events, domain_rca.TimelineEvent{
			OccurredAt:   *ev.OccurredAt,
			Category:     category,
			EventType:    string(ev.EvidenceType),
			Source:       ev.Source,
			Description:  ev.Description,
			Significance: significance,
		})
	}

	return domain_rca.BuildTimeline(events, incidentStartAt)
}

// ── EIRS API queries ──────────────────────────────────────────────────────────

func (b *CausalChainBuilder) fetchEntityNode(ctx context.Context, entityID string, role domain_rca.CausalNodeRole) *domain_rca.CausalNode {
	url := fmt.Sprintf("%s/api/v1/entities/%s", b.eirsBaseURL, entityID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Accept", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var entity struct {
		ID          string `json:"id"`
		EntityType  string `json:"entity_type"`
		DisplayName string `json:"display_name"`
		GraphNodeID string `json:"graph_node_id"`
	}
	if err := json.Unmarshal(body, &entity); err != nil {
		return nil
	}

	return &domain_rca.CausalNode{
		EntityID:    entity.ID,
		EntityType:  entity.EntityType,
		EntityName:  entity.DisplayName,
		GraphNodeID: entity.GraphNodeID,
		Role:        role,
		Description: fmt.Sprintf("%s %s", entity.EntityType, entity.DisplayName),
	}
}

// fetchParentChain walks up the EIRS topology graph to find parent entities.
// Returns at most maxHops parent nodes (e.g. k8s_node → virtual_machine → bare_metal).
func (b *CausalChainBuilder) fetchParentChain(ctx context.Context, entityID string, maxHops int) []domain_rca.CausalNode {
	var parents []domain_rca.CausalNode
	current := entityID

	for hop := 0; hop < maxHops; hop++ {
		url := fmt.Sprintf("%s/api/v1/entities/%s/relationships?direction=out&type=RUNS_ON,HOSTED_BY,PART_OF&depth=1",
			b.eirsBaseURL, current)
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			break
		}
		req.Header.Set("Accept", "application/json")

		resp, err := b.httpClient.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			break
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		var result struct {
			Entities []struct {
				ID          string `json:"id"`
				EntityType  string `json:"entity_type"`
				DisplayName string `json:"display_name"`
			} `json:"entities"`
		}
		if err := json.Unmarshal(body, &result); err != nil || len(result.Entities) == 0 {
			break
		}

		parent := result.Entities[0]
		parents = append(parents, domain_rca.CausalNode{
			EntityID:    parent.ID,
			EntityType:  parent.EntityType,
			EntityName:  parent.DisplayName,
			Role:        domain_rca.RoleContext,
			Description: fmt.Sprintf("hosted on %s %s", parent.EntityType, parent.DisplayName),
		})
		current = parent.ID
	}

	return parents
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// extractTriggerNode finds the highest-causality change event in evidence.
func extractTriggerNode(evidence []*domain_ev.Evidence, incidentStartAt time.Time) *domain_rca.CausalNode {
	var best *domain_ev.Evidence
	bestWeight := 0.0

	for _, ev := range evidence {
		if ev.EvidenceType != domain_ev.TypeChangeDeployment &&
			ev.EvidenceType != domain_ev.TypeChangeConfig &&
			ev.EvidenceType != domain_ev.TypeOKGCausalityScore {
			continue
		}
		if ev.Role != domain_ev.RoleSupports {
			continue
		}
		if ev.EffectiveWeight() > bestWeight {
			bestWeight = ev.EffectiveWeight()
			best = ev
		}
	}

	if best == nil || bestWeight < 0.40 {
		return nil
	}

	var payload struct {
		ChangeID      string `json:"change_id"`
		ChangeType    string `json:"change_type"`
		Title         string `json:"title"`
		AuthorDisplay string `json:"author_display"`
		DeltaMinutes  int    `json:"delta_minutes"`
	}
	json.Unmarshal(best.Payload, &payload)

	node := &domain_rca.CausalNode{
		ChangeID:    payload.ChangeID,
		ChangeType:  payload.ChangeType,
		ChangeTitle: payload.Title,
		ChangedBy:   payload.AuthorDisplay,
		Role:        domain_rca.RoleTrigger,
		Description: best.Description,
	}
	if payload.DeltaMinutes != 0 {
		d := payload.DeltaMinutes
		node.DeltaMinutes = &d
		t := incidentStartAt.Add(-time.Duration(d) * time.Minute)
		node.OccurredAt = &t
	} else if best.OccurredAt != nil {
		node.OccurredAt = best.OccurredAt
	}

	return node
}

func sortCausalNodes(nodes []domain_rca.CausalNode) {
	// Simple insertion sort by occurrence time.
	// Nil times go last.
	n := len(nodes)
	for i := 1; i < n; i++ {
		for j := i; j > 0; j-- {
			ti := nodeTime(nodes[j])
			tj := nodeTime(nodes[j-1])
			if ti != nil && tj != nil && ti.Before(*tj) {
				nodes[j], nodes[j-1] = nodes[j-1], nodes[j]
			} else if ti != nil && tj == nil {
				nodes[j], nodes[j-1] = nodes[j-1], nodes[j]
			} else {
				break
			}
		}
	}
}

func nodeTime(n domain_rca.CausalNode) *time.Time {
	return n.OccurredAt
}

func categoryForEvidenceType(evType domain_ev.EvidenceType) string {
	switch {
	case startsWith(string(evType), "change_") || string(evType) == string(domain_ev.TypeOKGCausalityScore):
		return "change"
	case string(evType) == string(domain_ev.TypeSimilarIncident):
		return "historical"
	default:
		return "k8s_event"
	}
}

func significanceForEvidence(ev *domain_ev.Evidence) string {
	switch {
	case ev.Weight >= 0.85:
		return "high"
	case ev.Weight >= 0.65:
		return "medium"
	default:
		return "low"
	}
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
