// Package handler implements the ValidatingAdmissionWebhook request handler.
// Evaluates incoming Kubernetes manifests using KubeSense's intelligence stack
// before they are admitted to the cluster.
//
// Validation layers (all non-blocking by default, configurable to deny):
//   1. Config rules — health probes, resource limits, image tags, single replicas
//   2. Security rules — CIS benchmarks, RBAC, container security contexts
//   3. Change risk scoring — historical incident pattern matching
//
// Market gap: OPA/Gatekeeper and Kyverno validate against static policies.
// KubeSense validates against HISTORICAL INCIDENTS — "this pattern caused 3
// outages in the past 90 days."
package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"

	"github.com/aileron-platform/aileron/agent/services/core/internal/config"
	"github.com/aileron-platform/aileron/agent/services/core/internal/risk"
	"github.com/aileron-platform/aileron/agent/services/core/internal/security"
)

// Config controls admission webhook behavior.
type Config struct {
	// DenyOnCriticalSecurity rejects manifests with critical security findings.
	// Default: false (warn only) — set to true after validating no false positives.
	DenyOnCriticalSecurity bool

	// DenyOnHighRisk rejects manifests scored HIGH or CRITICAL by the risk engine.
	// Default: false (warn only) — set to true after baselining risk scores.
	DenyOnHighRisk bool

	// DryRun always admits but attaches findings as Kubernetes warnings.
	// Use during initial rollout to measure impact without blocking deployments.
	DryRun bool

	// RiskEngine is the historical incident risk scorer.
	// If nil, risk scoring is skipped.
	RiskEngine *risk.Engine
}

// Finding is a normalized policy or risk issue.
type Finding struct {
	Source   string // "config" | "security" | "risk"
	RuleID   string
	Severity string // "critical" | "high" | "medium" | "low"
	Message  string
	CISRef   string
}

// Handler handles POST /validate requests from the kube-apiserver.
type Handler struct {
	cfg   Config
	rules []config.Rule
}

// NewHandler creates the admission webhook handler.
func NewHandler(cfg Config) *Handler {
	return &Handler{
		cfg:   cfg,
		rules: config.AllRules(),
	}
}

// ServeHTTP handles ValidatingAdmissionWebhook HTTP requests.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	var review admissionv1.AdmissionReview
	if err := json.Unmarshal(body, &review); err != nil || review.Request == nil {
		http.Error(w, "invalid admission review", http.StatusBadRequest)
		return
	}

	review.Response = h.admit(review.Request)
	review.Response.UID = review.Request.UID

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(review); err != nil {
		log.Printf("webhook: encode response: %v", err)
	}
}

// admit performs the full validation pipeline for one admission request.
func (h *Handler) admit(req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	start := time.Now()

	// DELETE operations: no object to validate, always admit.
	if req.Operation == admissionv1.Delete {
		return buildResponse(req.UID, true, nil, "")
	}
	// Subresource operations (scale, status): not relevant for manifest validation.
	if req.SubResource != "" {
		return buildResponse(req.UID, true, nil, "")
	}

	var findings []Finding

	// ── Layer 1: Config validation ──────────────────────────────────────────
	obj, err := decodeKind(req.Object.Raw, req.Kind.Kind)
	if err != nil {
		// Parsing failure → fail-open with warning (never block on parse error)
		log.Printf("webhook: parse %s %s/%s: %v", req.Kind.Kind, req.Namespace, req.Name, err)
		return buildResponse(req.UID, true,
			[]string{"KubeSense: could not parse manifest for validation: " + err.Error()}, "")
	}

	if obj != nil {
		ctx := &config.ValidationContext{ClusterID: "admission"}
		for _, rule := range h.rules {
			for _, v := range rule.Check(obj, ctx) {
				findings = append(findings, Finding{
					Source:   "config",
					RuleID:   v.RuleID,
					Severity: v.Severity,
					Message:  v.Message,
				})
			}
		}
	}

	// ── Layer 2: Security analysis ──────────────────────────────────────────
	findings = append(findings, h.analyzeSecurityFindings(obj, req)...)

	// ── Layer 3: Change risk scoring ────────────────────────────────────────
	var riskScore *risk.Score
	if h.cfg.RiskEngine != nil {
		s := h.cfg.RiskEngine.Score(risk.ChangeInput{
			ResourceKind: req.Kind.Kind,
			Namespace:    req.Namespace,
			Name:         req.Name,
			ChangeType:   operationToChangeType(req.Operation, req.Kind.Kind),
			Actor:        req.UserInfo.Username,
			Timestamp:    time.Now(),
		})
		riskScore = &s
	}

	log.Printf("webhook: %s %s/%s findings=%d risk=%s elapsed=%dms",
		req.Kind.Kind, req.Namespace, req.Name,
		len(findings), levelStr(riskScore), time.Since(start).Milliseconds())

	// ── Dry-run: always admit, surface everything as warnings ─────────────
	if h.cfg.DryRun {
		return buildResponse(req.UID, true, buildWarnings("[DRY-RUN] ", findings, riskScore), "")
	}

	// ── Deny on critical security finding ────────────────────────────────
	if h.cfg.DenyOnCriticalSecurity {
		for _, f := range findings {
			if f.Source == "security" && f.Severity == "critical" {
				return buildResponse(req.UID, false, nil, criticalDenyMsg(f, findings, riskScore))
			}
		}
	}

	// ── Deny on high/critical risk score ─────────────────────────────────
	if h.cfg.DenyOnHighRisk && riskScore != nil {
		if riskScore.Level == risk.LevelHigh || riskScore.Level == risk.LevelCritical {
			return buildResponse(req.UID, false, nil, riskDenyMsg(riskScore, findings))
		}
	}

	// Admit — attach non-blocking warnings for high/critical findings
	return buildResponse(req.UID, true, buildWarnings("", findings, riskScore), "")
}

// analyzeSecurityFindings runs pod and container security checks on the object.
func (h *Handler) analyzeSecurityFindings(obj any, req *admissionv1.AdmissionRequest) []Finding {
	var findings []Finding
	if obj == nil {
		return nil
	}

	var pod *corev1.Pod
	switch o := obj.(type) {
	case *corev1.Pod:
		pod = o
	case *appsv1.Deployment:
		if len(o.Spec.Template.Spec.Containers) > 0 {
			pod = &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      o.Name,
					Namespace: o.Namespace,
					Labels:    o.Spec.Template.Labels,
				},
				Spec: o.Spec.Template.Spec,
			}
		}
	case *appsv1.StatefulSet:
		if len(o.Spec.Template.Spec.Containers) > 0 {
			pod = &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      o.Name,
					Namespace: o.Namespace,
					Labels:    o.Spec.Template.Labels,
				},
				Spec: o.Spec.Template.Spec,
			}
		}
	case *appsv1.DaemonSet:
		if len(o.Spec.Template.Spec.Containers) > 0 {
			pod = &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      o.Name,
					Namespace: o.Namespace,
					Labels:    o.Spec.Template.Labels,
				},
				Spec: o.Spec.Template.Spec,
			}
		}
	}

	if pod != nil {
		for _, f := range security.AnalyzePodSecurity(pod) {
			findings = append(findings, Finding{
				Source:   "security",
				RuleID:   f.RuleID,
				Severity: string(f.Severity),
				Message:  f.Description,
				CISRef:   f.CISRef,
			})
		}
		for _, f := range security.AnalyzeSecretExposure(pod) {
			findings = append(findings, Finding{
				Source:   "security",
				RuleID:   f.RuleID,
				Severity: string(f.Severity),
				Message:  f.Description,
			})
		}
	}
	return findings
}

// ─── Response construction ────────────────────────────────────────────────────

func buildResponse(
	uid k8stypes.UID,
	allowed bool,
	warnings []string,
	denyMsg string,
) *admissionv1.AdmissionResponse {
	resp := &admissionv1.AdmissionResponse{
		UID:      uid,
		Allowed:  allowed,
		Warnings: warnings,
	}
	if !allowed && denyMsg != "" {
		resp.Result = &metav1.Status{
			Code:    http.StatusForbidden,
			Message: denyMsg,
		}
	}
	return resp
}

func buildWarnings(prefix string, findings []Finding, score *risk.Score) []string {
	var w []string
	for _, f := range findings {
		if f.Severity == "critical" || f.Severity == "high" {
			msg := fmt.Sprintf("KubeSense %s[%s/%s] %s", prefix, f.Source, f.RuleID, f.Message)
			if f.CISRef != "" {
				msg += " (" + f.CISRef + ")"
			}
			w = append(w, msg)
		}
	}
	if score != nil && (score.Level == risk.LevelHigh || score.Level == risk.LevelCritical) {
		w = append(w, fmt.Sprintf("KubeSense %s[risk/%s] %s", prefix, string(score.Level), score.Summary))
	}
	return w
}

func criticalDenyMsg(critical Finding, all []Finding, score *risk.Score) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "KubeSense DENIED: critical security finding [%s]\n%s",
		critical.RuleID, critical.Message)
	if critical.CISRef != "" {
		fmt.Fprintf(&sb, "\nCIS reference: %s", critical.CISRef)
	}
	fmt.Fprintf(&sb, "\n\nAll findings (%d total):", len(all))
	for _, f := range all {
		fmt.Fprintf(&sb, "\n  [%s/%s] %s", f.Severity, f.RuleID, f.Message)
	}
	if score != nil {
		fmt.Fprintf(&sb, "\n\nRisk score: %s (%.0f%%)", string(score.Level), score.Raw*100)
	}
	return sb.String()
}

func riskDenyMsg(score *risk.Score, findings []Finding) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "KubeSense DENIED: %s risk deployment blocked (%.0f%%)\n%s",
		strings.ToUpper(string(score.Level)), score.Raw*100, score.Summary)
	fmt.Fprintf(&sb, "\n\nRisk factors:")
	for _, f := range score.Factors {
		if f.Score > 0.55 {
			fmt.Fprintf(&sb, "\n  • %s (%.0f%%): %s", f.Name, f.Score*100, f.Description)
		}
	}
	if len(score.SimilarIncidents) > 0 {
		fmt.Fprintf(&sb, "\n\nSimilar historical incidents:")
		for _, inc := range score.SimilarIncidents {
			fmt.Fprintf(&sb, "\n  • %s in %s — %.0f%% similar",
				inc.FailureMode, inc.Namespace, inc.Similarity*100)
		}
	}
	if len(findings) > 0 {
		fmt.Fprintf(&sb, "\n\nAdditional policy findings: %d", len(findings))
	}
	return sb.String()
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func decodeKind(raw []byte, kind string) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	switch kind {
	case "Deployment":
		var obj appsv1.Deployment
		return &obj, json.Unmarshal(raw, &obj)
	case "StatefulSet":
		var obj appsv1.StatefulSet
		return &obj, json.Unmarshal(raw, &obj)
	case "DaemonSet":
		var obj appsv1.DaemonSet
		return &obj, json.Unmarshal(raw, &obj)
	case "Pod":
		var obj corev1.Pod
		return &obj, json.Unmarshal(raw, &obj)
	default:
		return nil, nil
	}
}

func operationToChangeType(op admissionv1.Operation, kind string) risk.ChangeType {
	if op == admissionv1.Delete {
		return risk.ChangeDelete
	}
	if op == admissionv1.Create {
		return risk.ChangeNew
	}
	switch kind {
	case "Deployment", "StatefulSet", "DaemonSet":
		return risk.ChangeImageUpdate
	case "ConfigMap":
		return risk.ChangeConfigUpdate
	case "Secret":
		return risk.ChangeSecretRotate
	default:
		return risk.ChangeConfigUpdate
	}
}

func levelStr(s *risk.Score) string {
	if s == nil {
		return "unscored"
	}
	return string(s.Level)
}
