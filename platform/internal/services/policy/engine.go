package policy

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"
)

// PolicyEngine evaluates intelligence_policies against alerts and incidents
// (Sympozium SympoziumPolicy pattern). Replaces the ad-hoc hardcoded suppression
// logic scattered across the pipeline: isKnownTestWorkload, cluster locks,
// severity family guards, and alert dedup filters.
//
// Policy types:
//   - suppress_alert     — drop the alert before pipeline processing
//   - suppress_incident  — prevent incident creation for matching alerts
//   - skip_rca           — skip RCA investigation for matching incidents
//   - require_approval   — require gate-hook approval before any action
//   - auto_resolve       — immediately resolve matching incidents
//
// Conditions (JSON):
//   - {"source": "prometheus", "title_contains": "liveness-fail"}
//   - {"severity": "low", "source": "dynatrace"}
//   - {"label_key": "environment", "label_value": "dev"}
//   - {"namespace_prefix": "tmp-", "entity_type": "pod"}
type PolicyEngine struct {
	db      *sql.DB
	cache   []policyRow
	cacheMu sync.RWMutex
	cacheAt time.Time
	cacheTTL time.Duration
}

type policyRow struct {
	ID         string
	Name       string
	PolicyType string
	Condition  policyCondition
	Action     string
	Enabled    bool
	Priority   int
}

type policyCondition struct {
	Source          string `json:"source,omitempty"`
	Severity        string `json:"severity,omitempty"`
	TitleContains   string `json:"title_contains,omitempty"`
	NamespacePrefix string `json:"namespace_prefix,omitempty"`
	EntityType      string `json:"entity_type,omitempty"`
	LabelKey        string `json:"label_key,omitempty"`
	LabelValue      string `json:"label_value,omitempty"`
	ClusterRef      string `json:"cluster_ref,omitempty"`
}

// PolicyDecision is the result of evaluating all policies against a signal.
type PolicyDecision struct {
	Action     string // "allow", "suppress", "skip_rca", "require_approval", "auto_resolve"
	PolicyName string // first matching policy name, empty when "allow"
	PolicyID   string
}

func NewPolicyEngine(db *sql.DB) *PolicyEngine {
	return &PolicyEngine{
		db:       db,
		cacheTTL: 5 * time.Minute,
	}
}

// EvaluateAlert evaluates all enabled policies against an alert signal.
func (e *PolicyEngine) EvaluateAlert(ctx context.Context, source, title, severity, namespace, entityType string, labels map[string]string) PolicyDecision {
	policies := e.loadCached(ctx)
	for _, p := range policies {
		if !p.Enabled {
			continue
		}
		if matches(p.Condition, source, title, severity, namespace, entityType, labels) {
			action := "suppress"
			if p.PolicyType == "require_approval" {
				action = "require_approval"
			} else if p.PolicyType == "auto_resolve" {
				action = "auto_resolve"
			} else if p.PolicyType == "suppress_incident" {
				action = "suppress_incident"
			} else if p.PolicyType == "skip_rca" {
				action = "skip_rca"
			}
			return PolicyDecision{Action: action, PolicyName: p.Name, PolicyID: p.ID}
		}
	}

	// Built-in hardcoded rules (migrated from isKnownTestWorkload / pipeline guards).
	// These are the last resort — DB policies take priority above.
	if isKnownTestWorkload(title, labels) {
		return PolicyDecision{Action: "suppress", PolicyName: "builtin:test-workload-filter"}
	}
	return PolicyDecision{Action: "allow"}
}

// EvaluateIncident evaluates skip_rca and require_approval policies against an incident.
func (e *PolicyEngine) EvaluateIncident(ctx context.Context, severity, topologyPath string, labels map[string]string) PolicyDecision {
	policies := e.loadCached(ctx)
	for _, p := range policies {
		if !p.Enabled {
			continue
		}
		if p.PolicyType != "skip_rca" && p.PolicyType != "require_approval" && p.PolicyType != "auto_resolve" {
			continue
		}
		if matches(p.Condition, "", "", severity, extractNamespace(topologyPath), "", labels) {
			action := p.PolicyType
			return PolicyDecision{Action: action, PolicyName: p.Name, PolicyID: p.ID}
		}
	}
	return PolicyDecision{Action: "allow"}
}

// loadCached returns the policy list from cache, refreshing when expired.
func (e *PolicyEngine) loadCached(ctx context.Context) []policyRow {
	e.cacheMu.RLock()
	if time.Since(e.cacheAt) < e.cacheTTL && e.cache != nil {
		policies := e.cache
		e.cacheMu.RUnlock()
		return policies
	}
	e.cacheMu.RUnlock()

	// Refresh cache.
	e.cacheMu.Lock()
	defer e.cacheMu.Unlock()
	if time.Since(e.cacheAt) < e.cacheTTL && e.cache != nil {
		return e.cache // double-checked
	}

	if e.db == nil {
		return nil
	}
	rows, err := e.db.QueryContext(ctx, `
		SELECT id::text, name, policy_type, condition_json, COALESCE(action,'suppress'),
		       enabled, COALESCE(priority, 50)
		FROM intelligence_policies
		WHERE enabled = true
		ORDER BY priority DESC, created_at
		LIMIT 500
	`)
	if err != nil {
		log.Printf("policy engine: load failed: %v", err)
		return e.cache
	}
	defer rows.Close()

	var policies []policyRow
	for rows.Next() {
		var p policyRow
		var condJSON string
		if err := rows.Scan(&p.ID, &p.Name, &p.PolicyType, &condJSON, &p.Action, &p.Enabled, &p.Priority); err != nil {
			continue
		}
		_ = json.Unmarshal([]byte(condJSON), &p.Condition)
		policies = append(policies, p)
	}
	e.cache = policies
	e.cacheAt = time.Now()
	return policies
}

// Invalidate forces a cache refresh on the next call.
func (e *PolicyEngine) Invalidate() {
	e.cacheMu.Lock()
	e.cacheAt = time.Time{}
	e.cacheMu.Unlock()
}

func matches(c policyCondition, source, title, severity, namespace, entityType string, labels map[string]string) bool {
	if c.Source != "" && !strings.EqualFold(c.Source, source) {
		return false
	}
	if c.Severity != "" && !strings.EqualFold(c.Severity, severity) {
		return false
	}
	if c.TitleContains != "" && !strings.Contains(strings.ToLower(title), strings.ToLower(c.TitleContains)) {
		return false
	}
	if c.NamespacePrefix != "" && !strings.HasPrefix(namespace, c.NamespacePrefix) {
		return false
	}
	if c.EntityType != "" && !strings.EqualFold(c.EntityType, entityType) {
		return false
	}
	if c.LabelKey != "" {
		val, ok := labels[c.LabelKey]
		if !ok {
			return false
		}
		if c.LabelValue != "" && !strings.EqualFold(val, c.LabelValue) {
			return false
		}
	}
	return true
}

// isKnownTestWorkload replicates the built-in suppression from the pipeline
// as the last-resort fallback when no DB policy matches.
func isKnownTestWorkload(title string, labels map[string]string) bool {
	lower := strings.ToLower(title)
	testPatterns := []string{"liveness-fail", "tmp-debug", "rca-claude-cli", "test-pod", "load-test"}
	for _, p := range testPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	if labels != nil {
		if env, ok := labels["environment"]; ok && (env == "test" || env == "dev" || env == "local") {
			return true
		}
	}
	return false
}

func extractNamespace(topologyPath string) string {
	if topologyPath == "" {
		return ""
	}
	parts := strings.Split(topologyPath, "/")
	if len(parts) >= 1 {
		return parts[0]
	}
	return ""
}
