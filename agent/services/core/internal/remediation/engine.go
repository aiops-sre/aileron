// Package remediation generates evidence-based remediation recommendations.
// Every suggestion cites the specific evidence that motivated it.
// Tracks historical outcomes so confidence scores improve over time.
package remediation

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// ActionType classifies the kind of remediation.
type ActionType string

const (
	ActionRollback          ActionType = "rollback"
	ActionScaleUp           ActionType = "scale_up"
	ActionScaleDown         ActionType = "scale_down"
	ActionRestart           ActionType = "restart"
	ActionConfigRevert      ActionType = "config_revert"
	ActionInvestigateNode   ActionType = "investigate_node"
	ActionDrainNode         ActionType = "drain_node"
	ActionDeleteStuckPod    ActionType = "delete_stuck_pod"
	ActionFlushConnPool     ActionType = "flush_connection_pool"
	ActionIncreaseResources ActionType = "increase_resources"
	ActionEscalate          ActionType = "escalate"
)

// Recommendation is one suggested remediation action.
type Recommendation struct {
	ID          string     `json:"id"`
	Type        ActionType `json:"type"`
	Priority    int        `json:"priority"` // 1 = highest, execute first
	Target      string     `json:"target"`   // resource name
	Namespace   string     `json:"namespace"`
	ClusterID   string     `json:"cluster_id"`

	// What to do
	Title       string `json:"title"`
	Description string `json:"description"`
	Command     string `json:"command,omitempty"` // ready-to-run kubectl command

	// Why (evidence chain)
	Rationale   string   `json:"rationale"`
	EvidenceCitations []string `json:"evidence_citations"` // specific evidence items cited

	// Risk assessment
	BlastRadius   string  `json:"blast_radius"`   // none | pod | service | cluster
	IsReversible  bool    `json:"is_reversible"`
	RollbackCmd   string  `json:"rollback_command,omitempty"`

	// Confidence: historical success rate for this action type in similar situations
	Confidence    float64 `json:"confidence"`    // 0-1
	SuccessRate   float64 `json:"success_rate"`  // from historical outcomes
	SampleCount   int     `json:"sample_count"`  // how many past executions inform this

	// Estimated impact
	ExpectedResolutionMins int `json:"expected_resolution_minutes"`
}

// HistoricalOutcome records whether a past recommendation resolved the incident.
type HistoricalOutcome struct {
	ActionType    ActionType
	TargetKind    string
	Namespace     string
	ExecutedAt    time.Time
	Resolved      bool    // did the incident resolve after this action?
	ResolutionMins int
}

// OutcomeStore maintains historical success rates per action type.
type OutcomeStore struct {
	mu       sync.RWMutex
	outcomes map[ActionType][]HistoricalOutcome
}

// NewOutcomeStore creates an empty store.
func NewOutcomeStore() *OutcomeStore {
	return &OutcomeStore{outcomes: make(map[ActionType][]HistoricalOutcome)}
}

// Record adds an outcome.
func (s *OutcomeStore) Record(o HistoricalOutcome) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outcomes[o.ActionType] = append(s.outcomes[o.ActionType], o)
	// Keep last 500 per action type
	if len(s.outcomes[o.ActionType]) > 500 {
		s.outcomes[o.ActionType] = s.outcomes[o.ActionType][1:]
	}
}

// SuccessRate returns the historical success rate and sample count for an action type.
func (s *OutcomeStore) SuccessRate(t ActionType) (rate float64, count int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	outcomes := s.outcomes[t]
	if len(outcomes) == 0 {
		return 0.70, 0 // default prior: 70% when no data
	}
	successes := 0
	for _, o := range outcomes {
		if o.Resolved { successes++ }
	}
	return float64(successes) / float64(len(outcomes)), len(outcomes)
}

// MedianResolutionMins returns the median time to resolution for an action type.
func (s *OutcomeStore) MedianResolutionMins(t ActionType) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var times []int
	for _, o := range s.outcomes[t] {
		if o.Resolved && o.ResolutionMins > 0 {
			times = append(times, o.ResolutionMins)
		}
	}
	if len(times) == 0 { return 10 } // default
	sort.Ints(times)
	return times[len(times)/2]
}

// Engine generates remediation recommendations.
type Engine struct {
	outcomes *OutcomeStore
}

// NewEngine creates a remediation engine.
func NewEngine(outcomes *OutcomeStore) *Engine {
	return &Engine{outcomes: outcomes}
}

// ForCrashLoopBackOff generates recommendations for a CrashLoopBackOff pod.
func (e *Engine) ForCrashLoopBackOff(
	ctx context.Context,
	clusterID, namespace, podName, deploymentName string,
	restartCount int,
	recentDeployment bool,
	oomKilled bool,
) []Recommendation {
	var recs []Recommendation

	// If OOM killed: increase memory limits
	if oomKilled {
		rate, count := e.outcomes.SuccessRate(ActionIncreaseResources)
		recs = append(recs, Recommendation{
			Type: ActionIncreaseResources, Priority: 1,
			Target: deploymentName, Namespace: namespace, ClusterID: clusterID,
			Title:       "Increase memory limits — container was OOM killed",
			Description: fmt.Sprintf("Pod %q was OOM killed after %d restarts. Memory limit is too low.", podName, restartCount),
			Command:     fmt.Sprintf("kubectl set resources deployment/%s --limits=memory=2Gi -n %s", deploymentName, namespace),
			Rationale:   fmt.Sprintf("Container OOM kill detected. Restart count: %d.", restartCount),
			EvidenceCitations: []string{
				fmt.Sprintf("k8s_event: OOMKilled on pod %s (restart_count=%d)", podName, restartCount),
			},
			BlastRadius: "pod", IsReversible: true,
			RollbackCmd: fmt.Sprintf("kubectl set resources deployment/%s --limits=memory=512Mi -n %s", deploymentName, namespace),
			Confidence: rate, SuccessRate: rate, SampleCount: count,
			ExpectedResolutionMins: e.outcomes.MedianResolutionMins(ActionIncreaseResources),
		})
	}

	// If recent deployment: rollback
	if recentDeployment {
		rate, count := e.outcomes.SuccessRate(ActionRollback)
		recs = append(recs, Recommendation{
			Type: ActionRollback, Priority: 1,
			Target: deploymentName, Namespace: namespace, ClusterID: clusterID,
			Title:       "Roll back deployment — crash started after rollout",
			Description: fmt.Sprintf("CrashLoopBackOff began shortly after deployment %q was updated.", deploymentName),
			Command:     fmt.Sprintf("kubectl rollout undo deployment/%s -n %s", deploymentName, namespace),
			Rationale:   "Temporal correlation: pod began crashing within 10 minutes of deployment rollout.",
			EvidenceCitations: []string{
				fmt.Sprintf("change: deployment/%s rolled out recently", deploymentName),
				fmt.Sprintf("k8s_event: CrashLoopBackOff on pod %s (restart_count=%d)", podName, restartCount),
			},
			BlastRadius: "service", IsReversible: true,
			RollbackCmd: fmt.Sprintf("kubectl rollout redo deployment/%s -n %s", deploymentName, namespace),
			Confidence: rate, SuccessRate: rate, SampleCount: count,
			ExpectedResolutionMins: e.outcomes.MedianResolutionMins(ActionRollback),
		})
	}

	// Always: examine logs for root cause
	rate, count := e.outcomes.SuccessRate(ActionInvestigateNode)
	recs = append(recs, Recommendation{
		Type: ActionEscalate, Priority: len(recs) + 1,
		Target: podName, Namespace: namespace, ClusterID: clusterID,
		Title:       "Check container logs for crash reason",
		Description: "Examine logs from previous container instance to identify the crash cause.",
		Command:     fmt.Sprintf("kubectl logs %s -n %s --previous --tail=100", podName, namespace),
		Rationale:   "Previous container log reveals the specific error that caused the crash.",
		BlastRadius: "none", IsReversible: true,
		Confidence: 0.95, SuccessRate: rate, SampleCount: count,
		ExpectedResolutionMins: 5,
	})
	_ = count

	return recs
}

// ForNodeNotReady generates recommendations for a NotReady node.
func (e *Engine) ForNodeNotReady(ctx context.Context, clusterID, nodeName, reason string, podCount int) []Recommendation {
	var recs []Recommendation
	rate, count := e.outcomes.SuccessRate(ActionInvestigateNode)

	// First: investigate
	recs = append(recs, Recommendation{
		Type: ActionInvestigateNode, Priority: 1,
		Target: nodeName, Namespace: "", ClusterID: clusterID,
		Title:       "Investigate node condition",
		Description: fmt.Sprintf("Node %q is NotReady: %s. Examine node conditions and recent events.", nodeName, reason),
		Command:     fmt.Sprintf("kubectl describe node %s | grep -A 20 'Conditions:'", nodeName),
		Rationale:   fmt.Sprintf("Node transitioned to NotReady: %s. %d pods are affected.", reason, podCount),
		EvidenceCitations: []string{
			fmt.Sprintf("k8s_event: NodeNotReady on %s (reason: %s)", nodeName, reason),
		},
		BlastRadius: "cluster", IsReversible: false,
		Confidence: 0.92, SuccessRate: rate, SampleCount: count,
		ExpectedResolutionMins: 5,
	})

	// Cordon and drain if node has many pods
	if podCount > 0 {
		drainRate, drainCount := e.outcomes.SuccessRate(ActionDrainNode)
		recs = append(recs, Recommendation{
			Type: ActionDrainNode, Priority: 2,
			Target: nodeName, Namespace: "", ClusterID: clusterID,
			Title:       "Cordon and drain node",
			Description: fmt.Sprintf("Node %q has %d running pods. Drain to reschedule them on healthy nodes.", nodeName, podCount),
			Command:     fmt.Sprintf("kubectl cordon %s && kubectl drain %s --ignore-daemonsets --delete-emptydir-data", nodeName, nodeName),
			Rationale:   fmt.Sprintf("Node NotReady with %d pods. Draining prevents extended service disruption.", podCount),
			RollbackCmd: fmt.Sprintf("kubectl uncordon %s", nodeName),
			BlastRadius: "cluster", IsReversible: true,
			Confidence: drainRate, SuccessRate: drainRate, SampleCount: drainCount,
			ExpectedResolutionMins: e.outcomes.MedianResolutionMins(ActionDrainNode),
		})
	}

	return recs
}

// ForHighErrorRate generates recommendations for a service with elevated error rate.
func (e *Engine) ForHighErrorRate(
	ctx context.Context,
	clusterID, namespace, serviceName string,
	errorRate float64,
	recentChange bool,
	changeResourceName string,
) []Recommendation {
	var recs []Recommendation

	if recentChange {
		rate, count := e.outcomes.SuccessRate(ActionRollback)
		recs = append(recs, Recommendation{
			Type: ActionRollback, Priority: 1,
			Target: changeResourceName, Namespace: namespace, ClusterID: clusterID,
			Title:       fmt.Sprintf("Roll back — error rate %.1f%% started after deployment", errorRate*100),
			Description: fmt.Sprintf("Service %q error rate is %.1f%% (baseline <1%%). Recent deployment to %q correlates.", serviceName, errorRate*100, changeResourceName),
			Command:     fmt.Sprintf("kubectl rollout undo deployment/%s -n %s", changeResourceName, namespace),
			Rationale:   fmt.Sprintf("Error rate increased %.0f%% within 15 minutes of deployment.", errorRate*100),
			EvidenceCitations: []string{
				fmt.Sprintf("metric: error_rate=%.3f (%.0fx above baseline)", errorRate, errorRate/0.01),
				fmt.Sprintf("change: deployment/%s recently updated", changeResourceName),
			},
			BlastRadius: "service", IsReversible: true,
			RollbackCmd: fmt.Sprintf("kubectl rollout redo deployment/%s -n %s", changeResourceName, namespace),
			Confidence: rate, SuccessRate: rate, SampleCount: count,
			ExpectedResolutionMins: e.outcomes.MedianResolutionMins(ActionRollback),
		})
	}

	// Check upstream dependencies
	recs = append(recs, Recommendation{
		Type: ActionInvestigateNode, Priority: len(recs) + 1,
		Target: serviceName, Namespace: namespace, ClusterID: clusterID,
		Title:       "Check upstream dependencies for failures",
		Description: fmt.Sprintf("Verify that services called by %q are healthy.", serviceName),
		Command:     fmt.Sprintf("kubectl get pods -n %s | grep -v Running", namespace),
		Rationale:   "High error rates are often caused by a failing upstream dependency.",
		BlastRadius: "none", IsReversible: false,
		Confidence: 0.75, SuccessRate: 0.75, SampleCount: 0,
		ExpectedResolutionMins: 5,
	})

	return recs
}
