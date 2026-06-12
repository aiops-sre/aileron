package correlation

// root_cause_engine.go
//
// Authoritative first-pass layer that determines whether an alert is a root cause
// or a downstream symptom — WITHOUT relying on weighted scoring.
//
// The pipeline calls RootCauseEngine.Evaluate BEFORE the 4-strategy parallel run.
// If it returns ATTACH_TO_ROOT or CREATE_ROOT the pipeline must act immediately
// and skip scoring entirely.  Only RCAActionNoRoot falls through to scoring.

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// InfraLevel 

// InfraLevel defines the infrastructure hierarchy.
// Higher value = higher in the stack = stronger root-cause candidate.
// priority = severity_weight * InfraLevel
type InfraLevel int

const (
	InfraLevelUnknown InfraLevel = 0
	InfraLevelPod     InfraLevel = 1 // Kubernetes Pod / container
	InfraLevelNode    InfraLevel = 2 // Kubernetes Node
	InfraLevelVM      InfraLevel = 3 // Virtual Machine (CloudStack / KVM guest)
	InfraLevelKVM     InfraLevel = 4 // KVM Hypervisor / bare-metal acting as hypervisor
	InfraLevelBM      InfraLevel = 5 // Physical Bare Metal
)

func (l InfraLevel) String() string {
	switch l {
	case InfraLevelBM:
		return "bare_metal"
	case InfraLevelKVM:
		return "kvm"
	case InfraLevelVM:
		return "vm"
	case InfraLevelNode:
		return "k8s_node"
	case InfraLevelPod:
		return "k8s_pod"
	}
	return "unknown"
}

// nodeTypeToInfraLevel maps topology node types from TopoNodeInfo to InfraLevel.
// TopoNodeInfo.Layer: 0=BM, 1=VM/CloudStack, 2=K8s cluster, 3=K8s node, 4=Pod.
func nodeTypeToInfraLevel(nodeType string, layer int) InfraLevel {
	switch strings.ToLower(nodeType) {
	case "bare_metal":
		return InfraLevelBM
	case "cloudstack_vm", "vm":
		return InfraLevelVM
	case "kvm", "hypervisor":
		return InfraLevelKVM
	case "k8s_cluster", "kubernetes_cluster":
		// Cluster-level alerts sit at the VM tier for priority purposes.
		return InfraLevelVM
	case "k8s_node", "kubernetes_node":
		return InfraLevelNode
	case "k8s_pod", "kubernetes_pod":
		return InfraLevelPod
	// NetApp storage: cluster-wide failures rank at KVM level (cascade to every PVC/pod on them).
	case "netapp_cluster", "netapp_controller", "storage_controller", "netapp":
		return InfraLevelKVM
	// Per-SVM / per-aggregate: rank at VM level.
	case "netapp_svm", "netapp_aggregate", "netapp_node":
		return InfraLevelVM
	// Per-volume / per-LUN: rank at Node level.
	case "netapp_volume", "netapp_lun", "netapp_disk":
		return InfraLevelNode
	}
	// Layer-based fallback when NodeType is non-standard.
	switch layer {
	case 0:
		return InfraLevelBM
	case 1:
		return InfraLevelVM
	case 2:
		return InfraLevelVM
	case 3:
		return InfraLevelNode
	case 4:
		return InfraLevelPod
	}
	return InfraLevelUnknown
}

// RCAAction / RCADecision 

// RCAAction is the authoritative directive returned by RootCauseEngine.Evaluate.
type RCAAction string

const (
	// RCAActionAttachToRoot: this alert is a downstream effect — attach to existing root incident.
	RCAActionAttachToRoot RCAAction = "ATTACH_TO_ROOT"
	// RCAActionCreateRoot: this alert IS the root cause — create / own the incident.
	RCAActionCreateRoot RCAAction = "CREATE_ROOT"
	// RCAActionNoRoot: root cause indeterminate — fall through to 4-strategy scoring.
	RCAActionNoRoot RCAAction = "NO_ROOT"
)

// RCADecision is the authoritative output of RootCauseEngine.Evaluate.
// When Action != RCAActionNoRoot the pipeline MUST respect it without further scoring.
type RCADecision struct {
	Action           RCAAction
	// RootIncidentID is populated only for ATTACH_TO_ROOT.
	RootIncidentID   *uuid.UUID
	RootEntityID     string     // entity ID of the identified root cause node
	RootEntityLabel  string     // human-readable label (for topology_path / metadata)
	RootLevel        InfraLevel // InfraLevel of the root cause entity
	AlertLevel       InfraLevel // InfraLevel of the incoming alert itself
	AffectedEntities []string   // blast-radius entity labels (for suppression)
	Reason           string
	IsDynatraceRoot  bool // true when dt rootCauseEntity label was the source
}

// RootCauseEngine 

// RootCauseEngine is the authoritative first-pass layer before scoring.
// Wire it into AlertPipelineService.SetRootCauseEngine.
type RootCauseEngine struct {
	topoCorrelator *TopologyGraphCorrelator
	db             *sql.DB
	redis          *redis.Client // optional, for future caching
}

// NewRootCauseEngine creates the engine.  topoCorrelator may be nil (engine degrades gracefully).
func NewRootCauseEngine(topo *TopologyGraphCorrelator, db *sql.DB, r *redis.Client) *RootCauseEngine {
	return &RootCauseEngine{topoCorrelator: topo, db: db, redis: r}
}

// Evaluate runs three deterministic stages in order.
// Stages short-circuit as soon as a non-NO_ROOT decision is reached.
//
//  Stage 1 – Dynatrace rootCauseEntity label (highest trust)
//  Stage 2 – Topology graph: does a higher-level ancestor have an open incident?
//  Stage 3 – Is this alert itself the root (high infra level, has descendants)?
func (rce *RootCauseEngine) Evaluate(ctx context.Context, alert *Alert) (*RCADecision, error) {
	// Stage 1: Dynatrace rootCauseEntity 
	if dtRoot := rce.extractDynatraceRootCause(alert); dtRoot != "" {
		decision, err := rce.resolveDynatraceRoot(ctx, alert, dtRoot)
		if err == nil && decision.Action != RCAActionNoRoot {
			log.Printf("RCE alert=%s action=%s dt_root=%s", alert.ID, decision.Action, dtRoot)
			return decision, nil
		}
	}

	// Stage 2 & 3: Topology graph traversal 
	if rce.topoCorrelator == nil {
		return &RCADecision{Action: RCAActionNoRoot, Reason: "no topology correlator wired"}, nil
	}

	topoResult, err := rce.topoCorrelator.Correlate(ctx, alert)
	if err != nil || topoResult == nil || topoResult.MatchedNode == nil {
		return &RCADecision{Action: RCAActionNoRoot, Reason: "no topology match for alert entity"}, nil
	}

	alertLevel := nodeTypeToInfraLevel(topoResult.MatchedNode.NodeType, topoResult.MatchedNode.Layer)
	blastLabels := blastRadiusLabels(topoResult.BlastRadius)

	// Stage 2: Higher-level ancestor already has an open incident attach 
	if topoResult.RootCauseNode != nil {
		rootLevel := nodeTypeToInfraLevel(topoResult.RootCauseNode.NodeType, topoResult.RootCauseNode.Layer)
		if rootLevel > alertLevel {
			incidentID := rce.findOpenIncidentForEntity(ctx,
				topoResult.RootCauseNode.Label, topoResult.RootCauseNode.ID)
			if incidentID != uuid.Nil {
				reason := fmt.Sprintf("topology root=%s (level %d) has open incident; alert level=%d",
					topoResult.RootCauseNode.Label, rootLevel, alertLevel)
				log.Printf("RCE alert=%s action=ATTACH root_incident=%s %s", alert.ID, incidentID, reason)
				return &RCADecision{
					Action:           RCAActionAttachToRoot,
					RootIncidentID:   &incidentID,
					RootEntityID:     topoResult.RootCauseNode.ID,
					RootEntityLabel:  topoResult.RootCauseNode.Label,
					RootLevel:        rootLevel,
					AlertLevel:       alertLevel,
					AffectedEntities: blastLabels,
					Reason:           reason,
				}, nil
			}
		}
	}

	// Stage 3: This alert is the root (VM-level or higher + has blast radius) 
	if alertLevel >= InfraLevelVM && len(topoResult.BlastRadius) > 0 {
		reason := fmt.Sprintf("alert is at infra level %d (%s) with %d downstream nodes in blast radius",
			alertLevel, alertLevel.String(), len(topoResult.BlastRadius))
		log.Printf("RCE alert=%s action=CREATE_ROOT entity=%s level=%d blast=%d",
			alert.ID, topoResult.MatchedNode.Label, alertLevel, len(topoResult.BlastRadius))
		return &RCADecision{
			Action:           RCAActionCreateRoot,
			RootEntityID:     topoResult.MatchedNode.ID,
			RootEntityLabel:  topoResult.MatchedNode.Label,
			RootLevel:        alertLevel,
			AlertLevel:       alertLevel,
			AffectedEntities: blastLabels,
			Reason:           reason,
		}, nil
	}

	return &RCADecision{
		Action:     RCAActionNoRoot,
		AlertLevel: alertLevel,
		Reason:     "no deterministic root cause identified; falling through to 4-strategy scoring",
	}, nil
}

// Priority helpers (topology-driven root promotion) 

// Priority computes the combined priority score for root-promotion decisions.
//
//	priority = severity_weight × infra_level
//
// Higher priority = more likely to be the true root cause.
func Priority(severity string, level InfraLevel) float64 {
	var w float64
	switch strings.ToLower(severity) {
	case "critical":
		w = 4.0
	case "high":
		w = 3.0
	case "medium":
		w = 2.0
	default:
		w = 1.0
	}
	return w * float64(level)
}

// PromoteIncidentRoot returns true when a new alert should become the root of
// an existing incident (replaces the current root due to higher priority).
func PromoteIncidentRoot(currentRootLevel InfraLevel, currentRootSeverity string, newLevel InfraLevel, newSeverity string) bool {
	return Priority(newSeverity, newLevel) > Priority(currentRootSeverity, currentRootLevel)
}

// SuppressedReason 

// SuppressedReason is stored in the alert state when the suppression engine fires.
type SuppressedReason struct {
	RootIncidentID uuid.UUID `json:"root_incident_id"`
	RootEntity     string    `json:"root_entity"`
	Reason         string    `json:"reason"`
	SuppressedAt   time.Time `json:"suppressed_at"`
}

// private helpers 

func (rce *RootCauseEngine) extractDynatraceRootCause(alert *Alert) string {
	// Pre-extracted field populated during Dynatrace webhook normalisation.
	if alert.RootCauseEntity != "" {
		return alert.RootCauseEntity
	}
	if alert.Labels != nil {
		for _, key := range []string{"rootCauseEntity", "root_cause_entity", "dt.root_cause_entity"} {
			if v, ok := alert.Labels[key]; ok && v != "" {
				return v
			}
		}
	}
	if alert.Metadata != nil {
		for _, key := range []string{"rootCauseEntity", "root_cause_entity"} {
			if v, ok := alert.Metadata[key].(string); ok && v != "" {
				return v
			}
		}
	}
	return ""
}

func (rce *RootCauseEngine) resolveDynatraceRoot(ctx context.Context, alert *Alert, rootEntityName string) (*RCADecision, error) {
	if rce.db == nil {
		return &RCADecision{Action: RCAActionNoRoot}, nil
	}

	// Also resolve the entity ID label for better matching.
	rootEntityID := rootEntityName
	if alert.Labels != nil {
		if id := alert.Labels["root_cause_entity_id"]; id != "" {
			rootEntityID = id
		}
	}

	// Extract cluster label to scope lookups — prevents cross-cluster false positives
	// when two clusters have a workload/node with the same name.
	cluster := ""
	if alert.Labels != nil {
		for _, k := range []string{"cluster", "k8s.cluster.name", "kubernetes_cluster"} {
			if v := alert.Labels[k]; v != "" {
				cluster = v
				break
			}
		}
	}

	// Is there already an open incident that owns this root entity?
	// Two lookup paths depending on whether we have a cluster label:
	//
	//  Cluster-aware path: require the incident's topology_path/title/description to
	//    contain the cluster name — prevents cross-cluster false positives (S20).
	//
	//  Cluster-less path: CloudStack / bare-metal alerts may arrive without a k8s
	//    cluster label (e.g. S15 VM/BM alerts) but should still cascade onto an existing
	//    cluster-validated incident. We allow the match only when the target incident's
	//    topology_path does NOT start with 'h:' — the 'h:' prefix means the incident was
	//    itself created by a cluster-less bare-host alert (ghost entity, S11) and cannot
	//    act as a valid cascade anchor.
	var incidentID uuid.UUID
	var err error
	if cluster != "" {
		// Cluster-aware path: the outer block scopes to this cluster; the inner block
		// matches by entity identity.
		//
		// Outer scope has one special bypass: bare-host incidents (topology_path LIKE 'h:%')
		// will never contain the k8s cluster name in their path because they were created by
		// cluster-less BM/VM alerts.  We allow a direct corr_id match ONLY for those — this
		// is how BMVMNode cascades land in the same incident (S15).
		//
		// We intentionally do NOT bypass the cluster scope for incidents whose topology_path
		// already contains a cluster name — that would let a workload in cluster-A false-match
		// an incident in cluster-B when both share the same entity NAME (S20 cross-cluster FP).
		err = rce.db.QueryRowContext(ctx, `
			SELECT i.id FROM incidents i
			WHERE i.auto_created = TRUE
			  AND i.status IN ('open', 'investigating')
			  AND i.created_at >= NOW() - INTERVAL '2 hours'
			  AND (
			    i.topology_path ILIKE '%' || $3 || '%'
			    OR i.title ILIKE '%' || $3 || '%'
			    OR i.description ILIKE '%' || $3 || '%'
			    OR (i.topology_path LIKE 'h:%'
			        AND (i.correlation_id = $1 OR i.correlation_id = $2))
			  )
			  AND (
			    i.correlation_id = $1
			    OR i.correlation_id = $2
			    OR i.davis_ai_analysis->>'root_entity_id' = $1
			    OR i.davis_ai_analysis->>'root_entity_id' = $2
			    OR EXISTS (
			      SELECT 1 FROM alerts a
			      WHERE a.id::text = ANY(SELECT jsonb_array_elements_text(i.alert_ids))
			        AND a.labels->>'root_cause_entity' = $1
			        AND a.labels->>'impacted_entity' = $1
			    )
			  )
			ORDER BY i.created_at ASC LIMIT 1
		`, rootEntityName, rootEntityID, cluster).Scan(&incidentID)
	} else {
		// No k8s cluster label (bare-metal / CloudStack alerts).  Trust the correlation_id
		// match directly — do NOT restrict by topology_path prefix.  The old
		// `topology_path NOT LIKE 'h:%'` guard was meant to block ghost-entity incidents
		// but incorrectly excluded legitimate BM incidents (which also carry an 'h:' prefix)
		// from acting as cascade anchors, causing S1/S15/S19-style split incidents.
		err = rce.db.QueryRowContext(ctx, `
			SELECT i.id FROM incidents i
			WHERE i.auto_created = TRUE
			  AND i.status IN ('open', 'investigating')
			  AND i.created_at >= NOW() - INTERVAL '2 hours'
			  AND (
			    i.correlation_id = $1
			    OR i.correlation_id = $2
			    OR i.davis_ai_analysis->>'root_entity_id' = $1
			    OR i.davis_ai_analysis->>'root_entity_id' = $2
			  )
			ORDER BY i.created_at ASC LIMIT 1
		`, rootEntityName, rootEntityID).Scan(&incidentID)
	}

	if err == nil && incidentID != uuid.Nil {
		return &RCADecision{
			Action:          RCAActionAttachToRoot,
			RootIncidentID:  &incidentID,
			RootEntityID:    rootEntityName,
			RootEntityLabel: rootEntityName,
			IsDynatraceRoot: true,
			Reason:          fmt.Sprintf("dt root entity %s maps to open incident %s", rootEntityName, incidentID),
		}, nil
	}

	// No open incident yet — seed root incident from this alert regardless of whether
	// it is the root node or a downstream effect. Subsequent alerts with the same
	// rootCauseEntity will ATTACH to the incident created here.
	return &RCADecision{
		Action:          RCAActionCreateRoot,
		RootEntityID:    rootEntityName,
		RootEntityLabel: rootEntityName,
		IsDynatraceRoot: true,
		Reason:          fmt.Sprintf("dt identifies root entity %s; seeding incident", rootEntityName),
	}, nil
}

// findOpenIncidentForEntity finds the oldest open incident linked to a specific topology entity
// (by label in topology_path or root_entity_id in metadata).
func (rce *RootCauseEngine) findOpenIncidentForEntity(ctx context.Context, entityLabel, entityID string) uuid.UUID {
	if rce.db == nil {
		return uuid.Nil
	}
	var id uuid.UUID
	_ = rce.db.QueryRowContext(ctx, `
		SELECT id FROM incidents
		WHERE auto_created = TRUE
		  AND status IN ('open', 'investigating')
		  AND created_at >= NOW() - INTERVAL '2 hours'
		  AND (
		    correlation_id = $2
		    OR davis_ai_analysis->>'root_entity_id' = $2
		    OR davis_ai_analysis->>'root_entity_label' ILIKE '%' || $1 || '%'
		  )
		ORDER BY created_at ASC LIMIT 1
	`, entityLabel, entityID).Scan(&id)
	return id
}

// blastRadiusLabels extracts display labels from the blast-radius node slice.
func blastRadiusLabels(nodes []TopoNodeInfo) []string {
	labels := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if n.Label != "" {
			labels = append(labels, n.Label)
		}
	}
	return labels
}
