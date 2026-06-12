package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	pq "github.com/lib/pq"

	"github.com/aileron-platform/aileron/platform/internal/services/correlation"
	"github.com/aileron-platform/aileron/platform/internal/shared/metrics"
	"github.com/aileron-platform/aileron/platform/internal/shared/models"
)

func (s *AlertPipelineService) triggerRCA(alert *models.Alert, incidentID uuid.UUID, result *correlation.FinalCorrelationResult, rcaCtx *RCAInvestigationContext) {
	if s.rcaURL == "" {
		return
	}

	// Guard: skip re-trigger if an investigation is already in progress or completed.
	// This prevents duplicate investigations from concurrent pipeline paths and guards
	// against rca_status regression from 'completed' back to 'investigating'.
	if s.db != nil {
		checkCtx, checkCancel := context.WithTimeout(s.svcCtx, 3*time.Second)
		var currentStatus string
		if err := s.db.QueryRowContext(checkCtx, `SELECT COALESCE(rca_status,'none') FROM incidents WHERE id = $1`, incidentID).Scan(&currentStatus); err == nil {
			if currentStatus == "investigating" || currentStatus == "completed" || currentStatus == "failed" {
				log.Printf("Skipping RCA trigger for incident %s (already %s)", incidentID, currentStatus)
				checkCancel()
				metrics.RCARequests.WithLabelValues("skipped").Inc()
				return
			}
		}
		checkCancel()
	}

	namespace := ""
	cluster := ""
	service := ""
	if alert.Labels != nil {
		namespace = alert.Labels["namespace"]
		cluster = alert.Labels["cluster"]
		service = alert.Labels["service"]
		if service == "" {
			service = alert.Labels["app"]
		}
	}

	// If no caller-supplied context, build a minimal one from correlation result alone.
	if rcaCtx == nil {
		rcaCtx = BuildRCAContext(result, nil, nil, nil, nil)
	}

	body := map[string]interface{}{
		"alert_id":    alert.ID.String(),
		"alert_title": alert.Title,
		"alert_body": map[string]interface{}{
			"description": alert.Description,
			"severity":    alert.Severity,
			"source":      alert.Source,
			"labels":      alert.Labels,
			"metadata":    alert.Metadata,
		},
		"severity":    alert.Severity,
		"incident_id": incidentID.String(),
		"namespace":   namespace,
		"cluster":     cluster,
		"service":     service,
		"go_context":  rcaCtx,
	}

	payload, _ := json.Marshal(body)
	ctx, cancel := context.WithTimeout(s.svcCtx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/api/v1/investigations", s.rcaURL),
		bytes.NewReader(payload),
	)
	if err != nil {
		log.Printf("RCA trigger: failed to build request for incident %s: %v", incidentID, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("RCA trigger: HTTP error for incident %s: %v", incidentID, err)
		metrics.RCARequests.WithLabelValues("error").Inc()
		return
	}
	defer resp.Body.Close()

	var respBody struct {
		InvestigationID string `json:"investigation_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil || respBody.InvestigationID == "" {
		log.Printf("RCA trigger: bad response for incident %s (status=%d)", incidentID, resp.StatusCode)
		metrics.RCARequests.WithLabelValues("error").Inc()
		return
	}

	log.Printf("RCA investigation %s started for incident %s", respBody.InvestigationID, incidentID)
	metrics.RCARequests.WithLabelValues("ok").Inc()

	// Store investigation ID. Only transition to 'investigating' if the incident has not
	// already reached a terminal state ('completed' or 'failed') to prevent regressions.
	if s.db != nil {
		if _, err := s.db.ExecContext(ctx, `
			UPDATE incidents
			SET rca_investigation_id = $1, rca_status = 'investigating', updated_at = NOW()
			WHERE id = $2
			  AND rca_status NOT IN ('completed', 'failed')
		`, respBody.InvestigationID, incidentID); err != nil {
			log.Printf("[pipeline] store rca investigation_id incident=%s: %v", incidentID, err)
		}
	}
}

// alertDedupKey generates a short in-memory dedup key for the in-flight race guard.
func alertDedupKey(alert *models.Alert) string {
	cluster := extractLabelFromAlert(alert, "cluster", "kubernetes_cluster", "k8s.cluster.name")
	return extractTitleKey(alert.Title) + "|" + alert.Source + "|" + alert.Severity + "|" + cluster
}

// acquireClusterLock serializes createIncident calls for the same cluster so burst-concurrent
// alerts don't all race past the "no incident found" check and each create their own incident.
// Returns a release func that must be called (via defer) when the critical section ends.
func (s *AlertPipelineService) acquireClusterLock(cluster string) func() {
	val, _ := s.clusterLocks.LoadOrStore(cluster, &sync.Mutex{})
	mu := val.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// extractTitleKey returns the first ~50 chars of a lowercase title, stripped of common noise words.
// Used for fuzzy title-based dedup across the dedup queries.
func extractTitleKey(title string) string {
	t := strings.ToLower(strings.TrimSpace(title))
	// Remove leading severity-like tokens (e.g. "[ALERT]", "[HIGH]")
	for _, prefix := range []string{"[alert]", "[warning]", "[critical]", "[high]", "[medium]", "[low]", "alert:", "warning:"} {
		t = strings.TrimPrefix(t, prefix)
	}
	t = strings.TrimSpace(t)
	if utf8.RuneCountInString(t) > 50 {
		runes := []rune(t)
		t = string(runes[:50])
	}
	return t
}

// enrichInfraLabels looks up the CloudStack cluster for Dynatrace host/infra alerts
// whose description contained a parseable IP but no cluster label.
// The cluster label is required for topology correlation to score > 0.
// It also handles BM/VM alerts that carry only a hostname (no IP), which is the
// common case for Cloudstack_BareMetal alerts — these need k8s.cluster.name stamped
// so the cascade-storm cluster match in findOpenIncidentForAlert can find pod/node incidents.
func (s *AlertPipelineService) enrichInfraLabels(ctx context.Context, alert *models.Alert) {
	if s.topoGraph == nil || alert.Labels == nil {
		return
	}
	// Skip if already enriched with a cluster label.
	if alert.Labels["cluster"] != "" || alert.Labels["k8s.cluster.name"] != "" || alert.Labels["cloudstack_cluster"] != "" {
		return
	}

	// Path 1: IP-based lookup (VM alerts from DT that carry an IP).
	if ip := alert.Labels["ip"]; ip != "" {
		cluster, zone, kvmHost, found := s.topoGraph.FindVMByIP(ctx, ip)
		if found {
			s.applyInfraEnrichment(ctx, alert, []string{cluster}, zone, kvmHost)
			return
		}
	}

	// Path 2: Hostname-based lookup (BM/VM alerts that carry host.name but no IP).
	// Strip FQDN suffix to get the short hostname stored in Neo4j.
	hostname := alert.Labels["host.name"]
	if hostname == "" {
		hostname = alert.Labels["host"]
	}
	if hostname == "" {
		return
	}
	shortName := hostname
	if dot := strings.Index(hostname, "."); dot > 0 {
		shortName = hostname[:dot]
	}
	clusters, found := s.topoGraph.FindClustersByHostname(ctx, shortName)
	if !found || len(clusters) == 0 {
		return
	}
	s.applyInfraEnrichment(ctx, alert, clusters, "", "")
}

// applyInfraEnrichment stamps cluster (and optional zone/kvmHost) labels onto an alert
// and persists the updated labels to DB so subsequent pipeline stages see them.
func (s *AlertPipelineService) applyInfraEnrichment(ctx context.Context, alert *models.Alert, clusters []string, zone, kvmHost string) {
	if len(clusters) == 0 {
		return
	}
	// Use the first cluster as the primary label; all clusters stored for blast-radius matching.
	primary := clusters[0]
	if primary != "" {
		alert.Labels["cluster"] = primary
		alert.Labels["k8s.cluster.name"] = primary
		alert.Labels["cloudstack_cluster"] = primary
	}
	if len(clusters) > 1 {
		alert.Labels["k8s.cluster.names"] = strings.Join(clusters, ",")
	}
	if zone != "" {
		alert.Labels["zone"] = zone
	}
	if kvmHost != "" {
		alert.Labels["kvm_host"] = kvmHost
	}
	if s.db != nil {
		if labelsJSON, err := json.Marshal(alert.Labels); err == nil {
			if _, execErr := s.db.ExecContext(ctx, `UPDATE alerts SET labels = $1 WHERE id = $2`, labelsJSON, alert.ID); execErr != nil {
				log.Printf("[pipeline] persist enriched labels alert=%s: %v", alert.ID, execErr)
			}
		}
	}
	log.Printf("Topology enriched alert %s: host=%s clusters=%v zone=%s",
		alert.ID, alert.Labels["host.name"], clusters, zone)
}

// extractLabelFromAlert extracts a label value from alert labels, metadata, or description text
func extractLabelFromAlert(alert *models.Alert, keys ...string) string {
	for _, k := range keys {
		if alert.Labels != nil {
			if v, ok := alert.Labels[k]; ok && v != "" {
				return v
			}
		}
		if alert.Metadata != nil {
			if v, ok := alert.Metadata[k].(string); ok && v != "" {
				return v
			}
		}
	}
	// Fallback: parse K8s-style "key: value" lines from description (Dynatrace format)
	if alert.Description != "" {
		for _, k := range keys {
			if v := parseDescriptionLabelFromText(alert.Description, k); v != "" {
				return v
			}
		}
	}
	return ""
}

// parseDescriptionLabelFromText extracts a "key: value" entry from multi-line description text.
func parseDescriptionLabelFromText(description, key string) string {
	keyColon := key + ":"
	for _, line := range strings.Split(description, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, keyColon) {
			val := strings.TrimSpace(strings.TrimPrefix(line, keyColon))
			if val != "" {
				return val
			}
		}
	}
	return ""
}

// extractProblemDomainFromTitle extracts problem type keyword from alert text
func extractProblemDomainFromTitle(text string) string {
	t := strings.ToLower(text)
	switch {
	case strings.Contains(t, "cpu") || strings.Contains(t, "throttl") || strings.Contains(t, "saturation"):
		return "cpu"
	case strings.Contains(t, "memory") || strings.Contains(t, "oom") || strings.Contains(t, "heap"):
		return "memory"
	case strings.Contains(t, "disk") || strings.Contains(t, "storage") || strings.Contains(t, "iops"):
		return "disk"
	case strings.Contains(t, "network") || strings.Contains(t, "latency") || strings.Contains(t, "timeout"):
		return "network"
	case strings.Contains(t, "pod") || strings.Contains(t, "container") || strings.Contains(t, "crash") || strings.Contains(t, "restart") || strings.Contains(t, "oomkill"):
		return "pod"
	case strings.Contains(t, "node") && (strings.Contains(t, "not ready") || strings.Contains(t, "pressure")):
		return "node"
	case strings.Contains(t, "unavailable") || strings.Contains(t, "monitoring unavailable") || strings.Contains(t, "unreachable"):
		return "host"
	}
	return ""
}

// extractClusterFromText extracts a cluster name embedded in alert title/description text.
// Examples:
//   "Worker Node Not Ready in Prod Kubeadm cluster" "kubeadm"
//   "Not all Pods ready on Ingress on Kubeadm Cluster" "kubeadm"
func extractClusterFromText(text string) string {
	skip := map[string]bool{
		"the": true, "a": true, "in": true, "on": true, "at": true, "of": true,
		"prod": true, "nonprod": true, "dev": true, "staging": true, "stg": true,
		"k8s": true, "kubernetes": true,
	}
	words := strings.Fields(strings.ToLower(text))
	for i, w := range words {
		if w == "cluster" && i > 0 {
			prev := strings.TrimRight(words[i-1], ".,:")
			if !skip[prev] && len(prev) > 2 {
				return prev
			}
			if i > 1 {
				prev2 := strings.TrimRight(words[i-2], ".,:")
				if !skip[prev2] && len(prev2) > 2 {
					return prev2
				}
			}
		}
	}
	return ""
}


// V2 enrichment

// enrichIncidentV2 runs after a new incident is created. It calls the V2 engines
// to populate ontology_domain, rca_hypotheses, topo_root_entity_id, causal_chain,
// and rca_decisions on the incident row.
//
// ProbabilisticRCA and RecursiveTopoRCA run in PARALLEL — they are independent.
// CACIE fuses both results into the authoritative root cause and writes it
// immediately to incidents.ai_root_cause so operators don't wait for the LLM.
//
// firstRCEDecision is the decision already computed by processAlertRCEStage.
// Passing it here avoids a second Evaluate call that could produce a conflicting
// result if topology state changed during the processing hold window.
func (s *AlertPipelineService) enrichIncidentV2(
	alert *models.Alert,
	incidentID uuid.UUID,
	result *correlation.FinalCorrelationResult,
	onto *correlation.OntologyResult,
	firstRCEDecision *correlation.RCADecision,
) {
	if s.db == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("enrichIncidentV2 recovered from panic for incident %s: %v", incidentID, r)
		}
	}()
	// 60s total: parallel engine phase gets ~25s each, CACIE gets ~10s, remainder for writes.
	ctx, cancel := context.WithTimeout(s.svcCtx, 60*time.Second)
	defer cancel()

	corrAlert := &correlation.Alert{
		ID: alert.ID, Title: alert.Title, Description: alert.Description,
		Severity: alert.Severity, Source: alert.Source,
		Labels: alert.Labels, Metadata: alert.Metadata,
	}

	// 1. Ontology domain — already available, write immediately.
	if onto != nil && onto.Domain != "" {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE incidents SET ontology_domain=$1 WHERE id=$2`,
			string(onto.Domain), incidentID); err != nil {
			log.Printf("V2 enrich: ontology_domain update failed: %v", err)
		}
	}

	// 2+3. ProbabilisticRCA and RecursiveTopoRCA run in PARALLEL.
	//       They are fully independent — no shared state, no ordering dependency.
	var (
		hypotheses []*correlation.RCAHypothesis
		topoResult *correlation.RecursiveTopoResult
		wg         sync.WaitGroup
		hypoMu     sync.Mutex // protects hypotheses write
		topoMu     sync.Mutex // protects topoResult write
	)

	if s.probabilisticRCA != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			probCtx, probCancel := context.WithTimeout(ctx, 25*time.Second)
			defer probCancel()
			decision, err := s.probabilisticRCA.Evaluate(probCtx, corrAlert, incidentID)
			if err != nil {
				log.Printf("enrichV2: ProbabilisticRCA error incident=%s: %v", incidentID, err)
				return
			}
			if decision == nil || len(decision.Hypotheses) == 0 {
				return
			}
			hypoMu.Lock()
			hypotheses = decision.Hypotheses
			hypoMu.Unlock()
			hypsJSON, _ := json.Marshal(decision.Hypotheses)
			if _, dbErr := s.db.ExecContext(ctx,
				`UPDATE incidents SET rca_hypotheses=$1 WHERE id=$2`,
				hypsJSON, incidentID); dbErr != nil {
				log.Printf("enrichV2: rca_hypotheses write failed incident=%s: %v", incidentID, dbErr)
			}
		}()
	}

	if s.recursiveTopoRCA != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			topoCtx, topoCancel := context.WithTimeout(ctx, 25*time.Second)
			defer topoCancel()
			tr, err := s.recursiveTopoRCA.Traverse(topoCtx, corrAlert)
			if err != nil {
				log.Printf("enrichV2: RecursiveTopoRCA error incident=%s: %v", incidentID, err)
				return
			}
			if tr == nil {
				return
			}
			topoMu.Lock()
			topoResult = tr
			topoMu.Unlock()

			rootID := ""
			if tr.RootEntity != nil {
				rootID = tr.RootEntity.EntityID
			}
			chainJSON, _ := json.Marshal(tr.CausalChain)
			// Structured blast radius: store entity type + infra level + score so the
			// incident page can render the full BM → VM → K8s node → pod hierarchy.
			type blastEntry struct {
				EntityID         string  `json:"entity_id"`
				Label            string  `json:"label"`
				EntityType       string  `json:"entity_type"`
				InfraLevel       string  `json:"infra_level"`
				Cluster          string  `json:"cluster,omitempty"`
				Namespace        string  `json:"namespace,omitempty"`
				PropagationScore float64 `json:"propagation_score"`
				Depth            int     `json:"depth"`
			}
			var blastDetails []blastEntry
			var blastLabels []string
			for _, n := range tr.BlastRadius {
				if n.Node == nil {
					continue
				}
				blastLabels = append(blastLabels, n.Node.Label)
				blastDetails = append(blastDetails, blastEntry{
					EntityID:         n.Node.EntityID,
					Label:            n.Node.Label,
					EntityType:       n.Node.EntityType,
					InfraLevel:       n.Node.InfraLevel.String(),
					Cluster:          n.Node.Cluster,
					Namespace:        n.Node.Namespace,
					PropagationScore: n.PropagationScore,
					Depth:            n.Depth,
				})
			}
			blastDetailsJSON, _ := json.Marshal(blastDetails)
			blastLabelsJSON, _ := json.Marshal(blastLabels)
			if blastLabelsJSON == nil {
				blastLabelsJSON = []byte("[]")
			}
			// Always overwrite blast_radius with the deep topology result —
			// the shallow correlation-time value (derived from alert labels) is
			// replaced by the actual Neo4j traversal output.
			if _, dbErr := s.db.ExecContext(ctx,
				`UPDATE incidents
				 SET topo_root_entity_id    = $1,
				     causal_chain           = $2,
				     blast_radius           = $3::jsonb,
				     blast_radius_details   = $4::jsonb
				 WHERE id = $5`,
				rootID, chainJSON, blastLabelsJSON, blastDetailsJSON, incidentID); dbErr != nil {
				log.Printf("enrichV2: topo RCA write failed incident=%s: %v", incidentID, dbErr)
			}
			if len(blastLabels) > 0 && s.alertBuffer != nil {
				rootLabel := rootID
				if tr.RootEntity != nil && tr.RootEntity.Label != "" {
					rootLabel = tr.RootEntity.Label
				}
				s.suppressDescendantAlerts(ctx, blastLabels, incidentID, rootLabel)
				log.Printf("V2 suppressed %d blast-radius entities for incident %s (root=%s)",
					len(blastLabels), incidentID, rootLabel)
			}
		}()
	}

	// Wait for both engines before proceeding to fusion.
	wg.Wait()

	// 4. Regenerate full explainability report with both results now available.
	if report := correlation.GenerateExplainabilityReport(corrAlert, result, topoResult, hypotheses, onto); report != nil {
		if reportJSON, err := report.ToJSON(); err == nil {
			s.db.ExecContext(ctx, `
				UPDATE pipeline_correlation_results SET explanation_json=$1
				WHERE id = (SELECT id FROM pipeline_correlation_results WHERE alert_id=$2 ORDER BY processed_at DESC LIMIT 1)`,
				reportJSON, alert.ID)
		}
	}

	// 5. CACIE — authoritative causal inference, fuses both engine outputs.
	var cacieResult *correlation.CausalInferenceResult
	if s.causalInferenceEngine != nil {
		var err error
		cacieResult, err = s.causalInferenceEngine.Infer(
			ctx, corrAlert, incidentID, firstRCEDecision, topoResult, hypotheses,
		)
		if err != nil {
			log.Printf("CACIE inference error for incident %s: %v", incidentID, err)
		}
	}

	// 5a. Write authoritative root cause to incidents immediately — operators see
	//     this in the UI without waiting for the LLM orchestrator to complete.
	//     TAUTOLOGY GUARD: when the root entity label semantically matches the alert
	//     title (e.g. "out-of-memory kills" entity for an OOM alert), CACIE has only
	//     confirmed the symptom, not the cause. Set rca_status=investigating so OIE
	//     has the chance to produce a better hypothesis before operators act on it.
	isTautologicalRCA := false
	if cacieResult != nil && cacieResult.RootEntity != nil {
		rootLabelLower := strings.ToLower(cacieResult.RootEntity.Label)
		alertTitleLower := strings.ToLower(alert.Title)
		// Only defer when the identified root entity IS the alerted symptom with near-zero confidence.
		// Guard is intentionally narrow — broad matching caused most incidents to never get an RCA.
		// A high-confidence CACIE result (≥0.65) is always used directly even if it matches a symptom.
		isLowConfidence := cacieResult.RootConfidence < 0.65
		if isLowConfidence {
			for _, symptomPhrase := range []string{
				"out-of-memory", "memory kill",
				"no pod ready", "host.*unavailable",
			} {
				base := strings.Split(symptomPhrase, ".*")[0]
				if strings.Contains(rootLabelLower, base) && strings.Contains(alertTitleLower, base) {
					isTautologicalRCA = true
					log.Printf("[CACIE] tautological RCA (low confidence %.2f) for incident %s: entity=%q matches alert=%q — deferring to OIE",
						cacieResult.RootConfidence, incidentID, cacieResult.RootEntity.Label, alert.Title)
					break
				}
			}
		}
	}

	if !isTautologicalRCA && cacieResult != nil && cacieResult.RootEntity != nil && cacieResult.RootEntity.Label != "" {
		rootLabel := cacieResult.RootEntity.Label
		rootConf := cacieResult.RootConfidence
		band := rcaConfidenceBand(rootConf)
		rootCauseText := fmt.Sprintf("[%s] %s (confidence: %.0f%%)", band, rootLabel, rootConf*100)
		s.db.ExecContext(ctx, `
			UPDATE incidents
			SET ai_root_cause = $1,
			    rca_confidence = $2
			WHERE id = $3`,
			rootCauseText, rootConf, incidentID)

		// Notify operators via Slack that a preliminary RCA is ready.
		if s.notificationSvc != nil {
			incNum, sev, incTitle := s.fetchIncidentMeta(ctx, incidentID)
			go func() {
				_ = s.notificationSvc.SendRCAUpdateNotification(
					s.svcCtx, incidentID, incNum, sev, incTitle,
					rootCauseText, rootConf, band, "CACIE",
				)
			}()
		}

		// 5b. Persist ranked hypotheses to rca_decisions for audit + operator UI.
		s.persistRCADecision(ctx, incidentID, cacieResult, hypotheses)
	} else if isTautologicalRCA && cacieResult != nil {
		// Tautological RCA: CACIE identified the symptom entity as the cause with low confidence.
		// Write a preliminary cause from the correlation result (so operators see something), but
		// mark it as needing OIE confirmation. OIE will overwrite this if it finds a better cause.
		rootLabel := cacieResult.RootEntity.Label
		s.db.ExecContext(ctx, `
			UPDATE incidents
			SET ai_root_cause = $1, rca_status = 'investigating', rca_confidence = 0.0
			WHERE id = $2`,
			fmt.Sprintf("Preliminary: %s (low confidence — OIE investigation running for deeper analysis)", rootLabel),
			incidentID)
	} else if topoResult != nil && topoResult.RootEntity != nil {
		// CACIE unavailable but topology traversal found a root — use it directly.
		rootLabel := topoResult.RootEntity.Label
		rootConf := topoResult.RootConfidence
		band := rcaConfidenceBand(rootConf)
		s.db.ExecContext(ctx, `
			UPDATE incidents
			SET ai_root_cause = $1,
			    rca_confidence = $2
			WHERE id = $3`,
			fmt.Sprintf("[%s] %s (topology traversal, confidence: %.0f%%)", band, rootLabel, rootConf*100),
			rootConf, incidentID)
	}

	// 6. Investigation DAG.
	if s.investigationDAGEngine != nil {
		domain := correlation.DomainUnknown
		rootEntityLabel := incidentID.String()
		if cacieResult != nil && cacieResult.Domain != "" {
			domain = cacieResult.Domain
		} else if onto != nil {
			domain = onto.Domain
		}
		if cacieResult != nil && cacieResult.RootEntity != nil && cacieResult.RootEntity.Label != "" {
			rootEntityLabel = cacieResult.RootEntity.Label
		} else if topoResult != nil && topoResult.RootEntity != nil && topoResult.RootEntity.Label != "" {
			rootEntityLabel = topoResult.RootEntity.Label
		}
		dag := s.investigationDAGEngine.GenerateDAG(domain, rootEntityLabel, incidentID.String())
		log.Printf("investigation DAG generated for incident %s: domain=%s root=%s steps=%d",
			incidentID, domain, rootEntityLabel, len(dag.Steps))
		stepsJSON, _ := json.Marshal(dag.Steps)
		s.db.ExecContext(ctx, `
			INSERT INTO incident_investigation_dags
				(incident_id, domain, root_entity, steps, generated_at, updated_at)
			VALUES ($1, $2, $3, $4, NOW(), NOW())
			ON CONFLICT (incident_id) DO UPDATE SET
				domain       = EXCLUDED.domain,
				root_entity  = EXCLUDED.root_entity,
				steps        = EXCLUDED.steps,
				updated_at   = NOW()`,
			incidentID, string(domain), rootEntityLabel, stepsJSON)
	}

	// 7. Trigger RCA orchestrator with full context.
	rcaCtx := BuildRCAContext(result, topoResult, hypotheses, onto, cacieResult)
	s.triggerRCA(alert, incidentID, result, rcaCtx)
}

// rcaConfidenceBand converts a 0-1 confidence float to a human-readable band label.
func rcaConfidenceBand(c float64) string {
	switch {
	case c >= 0.85:
		return "HIGH"
	case c >= 0.65:
		return "MEDIUM"
	case c >= 0.40:
		return "LOW"
	default:
		return "VERY_LOW"
	}
}

// persistRCADecision writes ranked hypotheses and the authoritative root cause
// from CACIE into the rca_decisions table. Called immediately after CACIE so
// the decision is available to the GET /incidents/:id/rca-decisions endpoint
// without waiting for the LLM orchestrator.
func (s *AlertPipelineService) persistRCADecision(
	ctx context.Context,
	incidentID uuid.UUID,
	cacieResult *correlation.CausalInferenceResult,
	hypotheses []*correlation.RCAHypothesis,
) {
	if s.db == nil || cacieResult == nil {
		return
	}

	type hypRecord struct {
		Rank           int      `json:"rank"`
		EntityID       string   `json:"entity_id"`
		EntityLabel    string   `json:"entity_label"`
		EntityType     string   `json:"entity_type"`
		Confidence     float64  `json:"confidence"`
		EvidenceSummary []string `json:"evidence_summary"`
		RejectedReason *string  `json:"rejected_reason,omitempty"`
	}

	var hypsForDB []hypRecord
	for i, h := range hypotheses {
		var summaries []string
		for _, e := range h.Evidence {
			summaries = append(summaries, fmt.Sprintf("[%s] %.2f: %s", e.Source, e.Score, e.Description))
		}
		rec := hypRecord{
			Rank:            i + 1,
			EntityID:        h.EntityID,
			EntityLabel:     h.EntityLabel,
			EntityType:      h.EntityType,
			Confidence:      h.Confidence,
			EvidenceSummary: summaries,
		}
		if i > 0 && len(hypotheses) > 0 {
			r := fmt.Sprintf("outscored by rank-1 (%.3f vs %.3f)", h.Confidence, hypotheses[0].Confidence)
			rec.RejectedReason = &r
		}
		hypsForDB = append(hypsForDB, rec)
	}
	hypJSON, _ := json.Marshal(hypsForDB)

	rootEntityID := ""
	rootEntityLabel := ""
	rootEntityType := ""
	if cacieResult.RootEntity != nil {
		rootEntityID = cacieResult.RootEntity.EntityID
		rootEntityLabel = cacieResult.RootEntity.Label
		rootEntityType = cacieResult.RootEntity.EntityType
	}

	band := rcaConfidenceBand(cacieResult.RootConfidence)

	var sources []string
	for _, h := range hypotheses {
		seen := map[string]bool{}
		for _, e := range h.Evidence {
			if !seen[string(e.Source)] {
				sources = append(sources, string(e.Source))
				seen[string(e.Source)] = true
			}
		}
		break // only sources from the top hypothesis
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO rca_decisions
			(incident_id, root_entity_id, root_entity_label, root_entity_type,
			 confidence, confidence_band, hypotheses, evidence_sources, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
		ON CONFLICT (incident_id) DO UPDATE SET
			root_entity_id    = EXCLUDED.root_entity_id,
			root_entity_label = EXCLUDED.root_entity_label,
			root_entity_type  = EXCLUDED.root_entity_type,
			confidence        = EXCLUDED.confidence,
			confidence_band   = EXCLUDED.confidence_band,
			hypotheses        = EXCLUDED.hypotheses,
			evidence_sources  = EXCLUDED.evidence_sources`,
		incidentID, rootEntityID, rootEntityLabel, rootEntityType,
		cacieResult.RootConfidence, band, hypJSON, pq.Array(sources),
	)
	if err != nil {
		log.Printf("persistRCADecision: write failed incident=%s: %v", incidentID, err)
	} else {
		log.Printf("RCA persisted: incident=%s root=%q confidence=%.3f band=%s hypotheses=%d",
			incidentID, rootEntityLabel, cacieResult.RootConfidence, band, len(hypsForDB))
	}
}

// fetchBERTEmbedding calls the BERT service to get a text embedding.
func fetchBERTEmbedding(bertURL, text string) ([]float32, error) {
	body, _ := json.Marshal(map[string]string{"text": text})
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Post(bertURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Embedding, nil
}

// getStrategyScore safely extracts a strategy's score from the results map.
// Returns 0 when the strategy is missing, errored, or nil.
func getStrategyScore(results map[string]*correlation.StrategyResult, name string) float64 {
	if r, ok := results[name]; ok && r != nil && r.Error == nil {
		return r.Score
	}
	return 0
}

// runStaleSweep runs hourly and auto-resolves:
//  1. Alerts open for > 24 h with no update (source never sent RESOLVED).
//  2. Incidents whose every linked alert is now resolved (safety-net for any
//     HandleAlertResolved call that was dropped due to a Kafka gap or restart).

