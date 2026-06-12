// Package playbook implements auto playbook generation from resolved incidents.
// Clusters similar past incidents by failure mode and resource kind, then
// synthesizes the most-effective remediation steps into a reusable runbook.
//
// Market gap: Runbook automation (PagerDuty, OpsGenie) requires manual authoring.
// KubeSense generates runbooks automatically from the OutcomeStore — no human
// needs to write them. Runbooks improve as more incidents are resolved.
package playbook

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// Step is one ordered step in a playbook.
type Step struct {
	Order           int     `json:"order"`
	ActionType      string  `json:"action_type"`
	Command         string  `json:"command,omitempty"`
	Description     string  `json:"description"`
	Rationale       string  `json:"rationale"`
	ExpectedOutcome string  `json:"expected_outcome"`
	IsReversible    bool    `json:"is_reversible"`
	RollbackCmd     string  `json:"rollback_command,omitempty"`
	SuccessRate     float64 `json:"success_rate"`
	SampleCount     int     `json:"sample_count"`
}

// Playbook is an auto-generated runbook for a class of incidents.
type Playbook struct {
	ID                string    `json:"id"`
	Title             string    `json:"title"`
	FailureMode       string    `json:"failure_mode"`
	ResourceKind      string    `json:"resource_kind"`
	TriggerConditions []string  `json:"trigger_conditions"`
	Steps             []Step    `json:"steps"`
	OverallSuccessRate float64  `json:"overall_success_rate"`
	DataPoints        int       `json:"data_points"` // number of resolved incidents this is based on
	GeneratedAt       time.Time `json:"generated_at"`
	LastUpdatedAt     time.Time `json:"last_updated_at"`
}

// ResolvedIncident is an incident with its complete resolution history.
// Feed these to the generator to build and improve playbooks over time.
type ResolvedIncident struct {
	IncidentID   string
	FailureMode  string // "CrashLoopBackOff" | "OOMKilled" | "NodeNotReady" | etc.
	ResourceKind string
	Namespace    string
	ClusterID    string
	OccurredAt   time.Time
	ResolvedAt   time.Time
	Actions      []TakenAction
}

// TakenAction is one remediation action that was taken during incident resolution.
type TakenAction struct {
	ActionType   string
	Command      string
	Description  string
	IsReversible bool
	RollbackCmd  string
	WasEffective bool // did resolution happen after this action?
	ExecutedAt   time.Time
}

// Generator builds and maintains auto-generated playbooks from resolved incidents.
type Generator struct {
	mu        sync.RWMutex
	incidents []ResolvedIncident
	playbooks map[string]*Playbook // key: failureMode/resourceKind
}

// NewGenerator creates an empty playbook generator.
func NewGenerator() *Generator {
	return &Generator{
		playbooks: make(map[string]*Playbook),
	}
}

// RecordResolvedIncident adds a resolved incident to the generator's corpus.
// The generator rebuilds affected playbooks immediately.
func (g *Generator) RecordResolvedIncident(inc ResolvedIncident) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.incidents = append(g.incidents, inc)
	g.rebuildPlaybook(inc.FailureMode, inc.ResourceKind)
}

// GetPlaybook returns the current playbook for a given failure mode and resource kind.
// Returns nil if no playbook exists (insufficient data — fewer than 2 incidents).
func (g *Generator) GetPlaybook(failureMode, resourceKind string) *Playbook {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.playbooks[playbookKey(failureMode, resourceKind)]
}

// GetAllPlaybooks returns all generated playbooks ordered by data points descending.
func (g *Generator) GetAllPlaybooks() []*Playbook {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var pbs []*Playbook
	for _, pb := range g.playbooks {
		pbs = append(pbs, pb)
	}
	sort.Slice(pbs, func(i, j int) bool {
		return pbs[i].DataPoints > pbs[j].DataPoints
	})
	return pbs
}

// FindPlaybook searches for a playbook that matches the given failure mode.
// Falls back to partial matching if no exact match exists.
func (g *Generator) FindPlaybook(failureMode, resourceKind string) *Playbook {
	// Exact match
	if pb := g.GetPlaybook(failureMode, resourceKind); pb != nil {
		return pb
	}
	// Failure mode only (any resource kind)
	if pb := g.GetPlaybook(failureMode, ""); pb != nil {
		return pb
	}
	// Fuzzy: normalize failure mode
	normalized := normalizeFailureMode(failureMode)
	g.mu.RLock()
	defer g.mu.RUnlock()
	for _, pb := range g.playbooks {
		if normalizeFailureMode(pb.FailureMode) == normalized {
			return pb
		}
	}
	return nil
}

// ─── Playbook construction ────────────────────────────────────────────────────

func (g *Generator) rebuildPlaybook(failureMode, resourceKind string) {
	// Collect all resolved incidents for this failure class
	var matching []ResolvedIncident
	for _, inc := range g.incidents {
		if inc.FailureMode == failureMode &&
			(resourceKind == "" || inc.ResourceKind == resourceKind) {
			matching = append(matching, inc)
		}
	}

	// Require at least 2 incidents to generate a meaningful playbook
	if len(matching) < 2 {
		return
	}

	pb := buildPlaybookFromIncidents(failureMode, resourceKind, matching)
	if pb != nil {
		g.playbooks[playbookKey(failureMode, resourceKind)] = pb
	}
}

func buildPlaybookFromIncidents(
	failureMode, resourceKind string,
	incidents []ResolvedIncident,
) *Playbook {
	// Aggregate action effectiveness across all incidents
	type actionStats struct {
		ActionType  string
		Commands    map[string]int // command → frequency
		Description string
		IsReversible bool
		RollbackCmd  string
		EffectiveCount int
		TotalCount     int
		ExecutionOrder []int // position (1,2,3...) when this action was taken
	}
	stats := map[string]*actionStats{}

	for _, inc := range incidents {
		for i, act := range inc.Actions {
			key := act.ActionType
			if _, ok := stats[key]; !ok {
				stats[key] = &actionStats{
					ActionType:  act.ActionType,
					Commands:    make(map[string]int),
					Description: act.Description,
					IsReversible: act.IsReversible,
					RollbackCmd:  act.RollbackCmd,
				}
			}
			s := stats[key]
			s.TotalCount++
			s.ExecutionOrder = append(s.ExecutionOrder, i+1)
			if act.Command != "" {
				s.Commands[act.Command]++
			}
			if act.WasEffective {
				s.EffectiveCount++
			}
		}
	}

	// Convert to steps, filter to actions that appeared in ≥25% of incidents
	minFreq := math.Max(2, float64(len(incidents))*0.25)
	var steps []Step
	for _, s := range stats {
		if float64(s.TotalCount) < minFreq {
			continue
		}
		successRate := 0.0
		if s.TotalCount > 0 {
			successRate = float64(s.EffectiveCount) / float64(s.TotalCount)
		}
		// Use the most common command variant
		bestCmd := mostFrequent(s.Commands)
		steps = append(steps, Step{
			ActionType:      s.ActionType,
			Command:         bestCmd,
			Description:     s.Description,
			Rationale:       fmt.Sprintf("Effective in %.0f%% of %d similar incidents", successRate*100, s.TotalCount),
			ExpectedOutcome: expectedOutcome(s.ActionType),
			IsReversible:    s.IsReversible,
			RollbackCmd:     s.RollbackCmd,
			SuccessRate:     successRate,
			SampleCount:     s.TotalCount,
		})
	}

	if len(steps) == 0 {
		return nil
	}

	// Order steps by: (1) success rate descending, (2) median execution order
	sort.Slice(steps, func(i, j int) bool {
		if math.Abs(steps[i].SuccessRate-steps[j].SuccessRate) > 0.10 {
			return steps[i].SuccessRate > steps[j].SuccessRate
		}
		return steps[i].Order < steps[j].Order
	})
	for i := range steps {
		steps[i].Order = i + 1
	}

	// Overall playbook success rate = fraction of incidents that were resolved
	resolvedCount := 0
	for _, inc := range incidents {
		if !inc.ResolvedAt.IsZero() {
			resolvedCount++
		}
	}
	overallSuccess := float64(resolvedCount) / float64(len(incidents))

	return &Playbook{
		ID:                playbookKey(failureMode, resourceKind),
		Title:             buildTitle(failureMode, resourceKind),
		FailureMode:       failureMode,
		ResourceKind:      resourceKind,
		TriggerConditions: triggerConditions(failureMode, resourceKind),
		Steps:             steps,
		OverallSuccessRate: overallSuccess,
		DataPoints:        len(incidents),
		GeneratedAt:       time.Now(),
		LastUpdatedAt:     time.Now(),
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func playbookKey(failureMode, resourceKind string) string {
	if resourceKind == "" {
		return strings.ToLower(failureMode)
	}
	return strings.ToLower(failureMode + "/" + resourceKind)
}

func buildTitle(failureMode, resourceKind string) string {
	if resourceKind == "" {
		return "Runbook: " + failureMode
	}
	return fmt.Sprintf("Runbook: %s → %s", resourceKind, failureMode)
}

func triggerConditions(failureMode, resourceKind string) []string {
	conditions := []string{
		fmt.Sprintf("Failure mode: %s", failureMode),
	}
	if resourceKind != "" {
		conditions = append(conditions, "Resource kind: "+resourceKind)
	}
	// Common trigger conditions by failure mode
	switch strings.ToLower(failureMode) {
	case "crashloopbackoff":
		conditions = append(conditions, "Pod restart count > 3", "Back-off restarting failed container")
	case "oomkilled":
		conditions = append(conditions, "Exit code 137 (OOMKilled)", "Memory limit exceeded")
	case "nodenotready":
		conditions = append(conditions, "Node condition: Ready=False", "Kubelet not responding")
	case "imagepullbackoff":
		conditions = append(conditions, "Image pull failed", "ErrImagePull or ImagePullBackOff")
	case "pendingtoomlong":
		conditions = append(conditions, "Pod in Pending state > 5 minutes", "Insufficient resources or PVC unbound")
	}
	return conditions
}

func expectedOutcome(actionType string) string {
	switch actionType {
	case "rollback":
		return "Service restored to last known good state"
	case "scale_up":
		return "Resource pressure relieved, pod scheduling succeeds"
	case "restart":
		return "Pod restarts with fresh process state"
	case "increase_resources":
		return "OOMKill eliminated, pod stays Running"
	case "investigate_node":
		return "Root cause identified for node failure"
	case "drain_node":
		return "Pods rescheduled to healthy nodes"
	case "delete_stuck_pod":
		return "Pod recreated by controller with clean state"
	case "config_revert":
		return "Configuration error eliminated"
	default:
		return "Issue resolved"
	}
}

func mostFrequent(m map[string]int) string {
	best := ""
	bestCount := 0
	for k, v := range m {
		if v > bestCount || (v == bestCount && k < best) {
			best = k
			bestCount = v
		}
	}
	return best
}

func normalizeFailureMode(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, "-", "")
	return s
}
