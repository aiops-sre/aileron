package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/aileron-platform/aileron/platform/internal/services/correlation"
	"github.com/aileron-platform/aileron/platform/internal/services/incidents"
	"github.com/aileron-platform/aileron/platform/internal/services/policy"
	"github.com/aileron-platform/aileron/platform/internal/shared/metrics"
	"github.com/aileron-platform/aileron/platform/internal/shared/models"
)

// PostmortemTrigger is a narrow interface so the pipeline can trigger postmortem
// generation on incident resolution without a full import cycle.
type PostmortemTrigger interface {
	GenerateForIncident(ctx context.Context, incidentID uuid.UUID) error
}

// KubeSensePublisher is a narrow interface for publishing investigation requests to KubeSense.
// Implemented by kubesense.Publisher.
type KubeSensePublisher interface {
	RequestInvestigation(ctx context.Context, incidentID, clusterID, namespace, resourceKind, resourceName, severity, alertTitle string) error
}

// TopologySearcher resolves a VM's CloudStack cluster by IP address or BM hostname.
// Implemented by the topology graph cache adapter wired in at startup.
type TopologySearcher interface {
	FindVMByIP(ctx context.Context, ip string) (cluster, zone, kvmHost string, ok bool)
	// FindClustersByHostname returns the distinct K8s cluster names hosted on a
	// bare-metal or VM host identified by hostname (short or FQDN).
	// Used to bridge BM alerts (no k8s.cluster.name label) into cluster-based correlation.
	FindClustersByHostname(ctx context.Context, hostname string) (clusters []string, ok bool)
}

// notificationSender is a minimal interface so the pipeline can fire incident notifications
// without importing the full notifications package (avoids import cycle risk).
type notificationSender interface {
	SendIncidentNotification(ctx context.Context, incidentID uuid.UUID, incidentNumber string, severity, title, description string) error
	// SendRCAUpdateNotification fires a follow-up message when the RCA result is ready.
	SendRCAUpdateNotification(ctx context.Context, incidentID uuid.UUID, incidentNumber, severity, title, rootCause string, confidence float64, band, source string) error
}

// AlertPipelineService runs parallel correlation + weighted aggregation + auto incident creation.
// It receives alerts via a non-blocking channel so webhook handlers return without waiting.
type AlertPipelineService struct {
	parallelEngine  *correlation.ParallelCorrelationEngine
	aggregator      *correlation.CorrelationAggregatorService
	incidentSvc     *incidents.IncidentService
	notificationSvc notificationSender
	db              *sql.DB
	rcaURL         string
	alertCh        chan *models.Alert
	// stagedPipeline, when set, replaces the alertCh dispatch with a 3-stage worker pool.
	stagedPipeline *StagedPipeline
	llmEnricher    *LLMEnricher
	// inflightDedup prevents race conditions when multiple alerts for the same workload
	// arrive simultaneously and all see an empty incident table concurrently.
	inflightDedup sync.Map
	// clusterLocks serializes incident creation per cluster (S23-style race guard).
	// Prevents burst-concurrent alerts from the same cluster each creating their own
	// incident when no incident exists yet — they queue up, and the second+ goroutine
	// finds the incident the first one just created.
	clusterLocks sync.Map
	// bufferPromotionSem limits concurrent buffer-promotion goroutines.
	// Without this cap, evaluateBufferedAlerts launches up to 50 goroutines every 30s,
	// each holding a 30s DB connection — causing goroutine and connection exhaustion
	// under sustained backlog.
	bufferPromotionSem chan struct{}
	// Optional Kafka publishers for downstream topics.
	// If nil, publishing to those topics is silently skipped.
	// oiePublisher fires a fire-and-forget event to the alerthub.incidents topic
	// so the OIE service auto-triggers an investigation for every new incident.
	oiePublisher *AlertKafkaProducer
	normalizedPublisher  *AlertKafkaProducer
	correlationPublisher *AlertKafkaProducer
	// kubeSensePublisher requests parallel evidence-first investigations from KubeSense
	// for critical/high incidents. Results arrive on kubesense.investigations.results.
	kubeSensePublisher KubeSensePublisher
	// postmortemTrigger auto-generates structured postmortems on incident resolution.
	postmortemTrigger PostmortemTrigger
	// policyEngine evaluates intelligence_policies before processing each alert.
	// When set, replaces the hardcoded isKnownTestWorkload check with DB-driven rules.
	policyEngine *policy.PolicyEngine
	// rootCauseEngine is the authoritative first-pass layer called BEFORE scoring.
	// When it returns ATTACH_TO_ROOT or CREATE_ROOT the pipeline acts immediately
	// without running the 4-strategy parallel scoring.
	rootCauseEngine *correlation.RootCauseEngine
	// alertBuffer tracks per-alert correlation state in Redis (no-alert-missed guarantee).
	alertBuffer *correlation.AlertBuffer
	// V2 Correlation Engine components — all optional/nil-safe.
	ontologyEngine   *correlation.OntologyEngine
	recursiveTopoRCA *correlation.RecursiveTopoRCAEngine
	probabilisticRCA *correlation.ProbabilisticRCAEngine
	adaptiveLearning *correlation.AdaptiveLearningEngine
	vectorRepo       *correlation.VectorRepository
	// causalInferenceEngine is the unified CACIE layer. When set, enrichIncidentV2
	// calls it after collecting all sub-engine results to produce the authoritative
	// causal graph and persist it to incident_causal_graphs.
	causalInferenceEngine *correlation.CausalInferenceEngine
	// investigationDAGEngine generates domain-specific investigation playbooks
	// after CACIE determines the root-cause domain.
	investigationDAGEngine *correlation.InvestigationDAGEngine
	// wg tracks background goroutines for graceful drain on shutdown.
	wg sync.WaitGroup
	// stopCh is closed on Drain() to signal long-running background goroutines to exit.
	stopCh chan struct{}
	// svcCtx / svcCancel are set in Start() and cancelled in Drain(). Fire-and-forget
	// goroutines (triggerRCA, enrichIncidentV2, reabsorbOrphaned) use this context so
	// they don't outlive the service on graceful shutdown.
	svcCtx    context.Context
	svcCancel context.CancelFunc
	// topoGraph, when set, enriches host/infra alerts with CloudStack cluster from topology.
	topoGraph TopologySearcher
}

func NewAlertPipelineService(
	parallelEngine *correlation.ParallelCorrelationEngine,
	aggregator *correlation.CorrelationAggregatorService,
	incidentSvc *incidents.IncidentService,
) *AlertPipelineService {
	return &AlertPipelineService{
		parallelEngine:     parallelEngine,
		aggregator:         aggregator,
		incidentSvc:        incidentSvc,
		alertCh:            make(chan *models.Alert, 2000),
		llmEnricher:        NewLLMEnricher(),
		stopCh:             make(chan struct{}),
		bufferPromotionSem: make(chan struct{}, 5),
	}
}

// SetDB wires the database for persisting pipeline correlation results.
func (s *AlertPipelineService) SetDB(db *sql.DB) { s.db = db }

// SetNotificationService wires the notification sender (called after incident creation).
func (s *AlertPipelineService) SetNotificationService(svc notificationSender) {
	s.notificationSvc = svc
}

// SetRCAURL wires the RCA orchestrator URL for triggering investigations.
func (s *AlertPipelineService) SetRCAURL(url string) { s.rcaURL = url }

// SetNormalizedPublisher wires a producer for the normalized-alerts topic.
func (s *AlertPipelineService) SetNormalizedPublisher(p *AlertKafkaProducer) {
	s.normalizedPublisher = p
}

// SetCorrelationPublisher wires a producer for the correlation-results topic.
func (s *AlertPipelineService) SetCorrelationPublisher(p *AlertKafkaProducer) {
	s.correlationPublisher = p
}

// SetOIEPublisher wires the publisher that fires incident events to OIE.
func (s *AlertPipelineService) SetOIEPublisher(p *AlertKafkaProducer) {
	s.oiePublisher = p
}

// SetRootCauseEngine wires the authoritative root cause engine.
// When set, processAlert calls it before the 4-strategy scoring pass.
func (s *AlertPipelineService) SetRootCauseEngine(e *correlation.RootCauseEngine) {
	s.rootCauseEngine = e
}

// SetAlertBuffer wires the Redis-backed alert state tracker.
func (s *AlertPipelineService) SetAlertBuffer(b *correlation.AlertBuffer) {
	s.alertBuffer = b
}

func (s *AlertPipelineService) SetOntologyEngine(e *correlation.OntologyEngine)                   { s.ontologyEngine = e }
func (s *AlertPipelineService) SetRecursiveTopoRCA(e *correlation.RecursiveTopoRCAEngine)           { s.recursiveTopoRCA = e }
func (s *AlertPipelineService) SetProbabilisticRCA(e *correlation.ProbabilisticRCAEngine)           { s.probabilisticRCA = e }
func (s *AlertPipelineService) SetAdaptiveLearning(e *correlation.AdaptiveLearningEngine)           { s.adaptiveLearning = e }
func (s *AlertPipelineService) SetVectorRepository(r *correlation.VectorRepository)                 { s.vectorRepo = r }
func (s *AlertPipelineService) SetTopologyGraph(ts TopologySearcher)                               { s.topoGraph = ts }
func (s *AlertPipelineService) SetCausalInferenceEngine(e *correlation.CausalInferenceEngine)      { s.causalInferenceEngine = e }
func (s *AlertPipelineService) SetInvestigationDAGEngine(e *correlation.InvestigationDAGEngine)    { s.investigationDAGEngine = e }

// SetKubeSensePublisher wires the publisher that fires investigation requests to KubeSense
// when a new critical/high incident is created or merged.
func (s *AlertPipelineService) SetKubeSensePublisher(p KubeSensePublisher) { s.kubeSensePublisher = p }

// SetPostmortemTrigger wires the service that auto-generates postmortems on resolution.
func (s *AlertPipelineService) SetPostmortemTrigger(p PostmortemTrigger) { s.postmortemTrigger = p }

// SetPolicyEngine wires the intelligence policy engine for pre-pipeline suppression.
// When set, EvaluateAlert() is called before any processing — replaces the hardcoded
// isKnownTestWorkload check with DB-driven suppress/skip policies.
func (s *AlertPipelineService) SetPolicyEngine(e *policy.PolicyEngine) { s.policyEngine = e }

// UpdateFromKubeSense merges a KubeSense investigation result into the incident row.
// Implements kubesense.IncidentUpdater — the KubeSense consumer calls this after
// receiving a result on kubesense.investigations.results.
func (s *AlertPipelineService) UpdateFromKubeSense(ctx context.Context, incidentID, grade, rootCause string, confidence float64) error {
	if s.db == nil {
		return nil
	}
	// Only update if KubeSense confidence exceeds the existing rca_confidence.
	// Grade A = 0.92, B = 0.78, etc. — see kubesense.GradeToConfidence.
	_, err := s.db.ExecContext(ctx, `
		UPDATE incidents
		SET
			rca_confidence  = GREATEST(COALESCE(rca_confidence, 0), $1),
			ai_root_cause   = CASE
				WHEN GREATEST(COALESCE(rca_confidence, 0), $1) > COALESCE(rca_confidence, 0)
				THEN COALESCE($2, ai_root_cause)
				ELSE ai_root_cause
			END,
			rca_status      = CASE
				WHEN rca_status IN ('none', 'queued', 'investigating') THEN 'completed'
				ELSE rca_status
			END,
			updated_at      = NOW()
		WHERE id::text = $3
	`, confidence, rootCause, incidentID)
	if err != nil {
		return fmt.Errorf("update incident %s from kubesense: %w", incidentID, err)
	}
	log.Printf("KubeSense result merged: incident=%s grade=%s confidence=%.2f", incidentID, grade, confidence)
	return nil
}

// EnqueueAlert submits an alert for async parallel correlation. Non-blocking.
func (s *AlertPipelineService) EnqueueAlert(alert *models.Alert) {
	metrics.AlertsIngested.WithLabelValues(alert.Source, alert.Status).Inc()
	if s.stagedPipeline != nil {
		s.stagedPipeline.Enqueue(alert)
		return
	}
	select {
	case s.alertCh <- alert:
	default:
		log.Printf("Alert pipeline queue full, dropping alert %s", alert.ID)
	}
}

// TryEnqueueAlert is like EnqueueAlert but returns false when the queue is full.
// Kafka consumers use this to avoid committing offsets on backpressure drops so
// the message is re-delivered after recovery rather than silently lost.
func (s *AlertPipelineService) TryEnqueueAlert(alert *models.Alert) bool {
	if s.stagedPipeline != nil {
		return s.stagedPipeline.Enqueue(alert)
	}
	select {
	case s.alertCh <- alert:
		return true
	default:
		log.Printf("Alert pipeline queue full, rejecting alert %s (Kafka offset not committed)", alert.ID)
		return false
	}
}

// Start processes alerts from the queue until ctx is cancelled.
// It also starts a background loop that promotes BUFFERED alerts to incidents
// once their hold window expires.
func (s *AlertPipelineService) Start(ctx context.Context) {
	log.Printf("Alert pipeline service started (parallel 4-strategy correlation)")
	s.svcCtx, s.svcCancel = context.WithCancel(ctx)
	go s.runBufferPromotionLoop(ctx)
	go s.runSyncMapCleanup(ctx)
	go s.runStaleSweep(ctx)
	if s.stagedPipeline != nil {
		// Alert processing is handled by staged pipeline workers; just wait for shutdown.
		<-ctx.Done()
		log.Printf("Alert pipeline service stopped")
		return
	}
	for {
		select {
		case <-ctx.Done():
			log.Printf("Alert pipeline service stopped")
			return
		case alert := <-s.alertCh:
			s.wg.Add(1)
			go func(a *models.Alert) {
				defer s.wg.Done()
				s.processAlert(ctx, a)
			}(alert)
		}
	}
}

// Drain waits for all in-flight processAlert goroutines to complete.
// Call this after the context is cancelled to ensure clean shutdown.
func (s *AlertPipelineService) Drain() {
	if s.svcCancel != nil {
		s.svcCancel()
	}
	close(s.stopCh)
	s.wg.Wait()
}

// runBufferPromotionLoop ticks every 30 seconds and promotes alerts that sat in
// BUFFERED state past their hold window without being linked to an incident.
func (s *AlertPipelineService) runBufferPromotionLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.evaluateBufferedAlerts(ctx)
		}
	}
}

// runSyncMapCleanup periodically evicts idle entries from clusterLocks so that
// transient per-entity keys (e.g. "rce:<uuid>") do not accumulate indefinitely.
// We intentionally never delete entries — the memory cost is trivial (~100 bytes per
// unique cluster key) and deletion races with concurrent acquireClusterLock calls:
// if cleanup deletes a mutex while another goroutine holds a pointer to it, two
// goroutines end up on different mutex objects for the same cluster key, breaking
// serialization. The goroutine just drains its context and exits cleanly.
func (s *AlertPipelineService) runSyncMapCleanup(ctx context.Context) {
	<-ctx.Done()
}

// evaluateBufferedAlerts finds open alerts that are still unlinked after their hold
// window (60 s) and promotes them to individual incidents so nothing is silently lost.
func (s *AlertPipelineService) evaluateBufferedAlerts(ctx context.Context) {
	if s.db == nil {
		return
	}
	evalCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	rows, err := s.db.QueryContext(evalCtx, `
		SELECT id, title, description, severity, source, source_id,
		       tags, labels, metadata, fingerprint, created_at
		FROM alerts
		WHERE incident_id IS NULL
		  AND status = 'open'
		  AND created_at < NOW() - INTERVAL '60 seconds'
		  AND created_at >= NOW() - INTERVAL '115 minutes'
		LIMIT 50
	`)
	if err != nil {
		log.Printf("evaluateBufferedAlerts: query failed: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var alert models.Alert
		var tagsJSON, labelsJSON, metadataJSON []byte
		if err := rows.Scan(
			&alert.ID, &alert.Title, &alert.Description,
			&alert.Severity, &alert.Source, &alert.SourceID,
			&tagsJSON, &labelsJSON, &metadataJSON,
			&alert.Fingerprint, &alert.CreatedAt,
		); err != nil {
			continue
		}
		json.Unmarshal(tagsJSON, &alert.Tags)
		json.Unmarshal(labelsJSON, &alert.Labels)
		json.Unmarshal(metadataJSON, &alert.Metadata)

		// Skip if already handled (state updated in Redis after DB write).
		if s.alertBuffer != nil {
			state, _ := s.alertBuffer.GetAlertState(evalCtx, alert.ID)
			if state != nil && (state.State == correlation.AlertStateAttached ||
				state.State == correlation.AlertStateIncidentCreated ||
				state.State == correlation.AlertStateSuppressed ||
				state.State == correlation.AlertStateBuffered) {
				continue
			}
		}

		// Re-check DB — a concurrent worker may have linked it while this query ran.
		var linkedID uuid.UUID
		if err := s.db.QueryRowContext(evalCtx,
			`SELECT incident_id FROM alerts WHERE id = $1 AND incident_id IS NOT NULL`, alert.ID,
		).Scan(&linkedID); err == nil && linkedID != uuid.Nil {
			continue
		}

		// Re-process via the full pipeline (RCE 4-strategy scoring) rather than
		// creating an incident with a synthetic FinalScore=0.3. This ensures topology
		// and root-cause checks still apply to buffered alerts at promotion time.
		alertCopy := alert
		s.wg.Add(1)
		s.bufferPromotionSem <- struct{}{}
		go func() {
			defer s.wg.Done()
			defer func() { <-s.bufferPromotionSem }()
			processCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			s.processAlert(processCtx, &alertCopy)
			log.Printf("Buffer promoted: alert=%s re-processed via full pipeline", alertCopy.ID)
		}()
	}
}

func (s *AlertPipelineService) processAlert(ctx context.Context, alert *models.Alert) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Alert pipeline recovered from panic for alert %s: %v", alert.ID, r)
		}
	}()
	// Fill in entity_type and any missing k8s labels before any stage runs.
	enrichEntityLabels(alert)

	// Test workload suppression: known test/debug fixtures that permanently fire
	// real alerts should not generate production incidents. When a policy engine is
	// wired, it evaluates DB-driven intelligence_policies (Sympozium SympoziumPolicy).
	// Falls back to the hardcoded isKnownTestWorkload check for production safety.
	if s.policyEngine != nil {
		namespace := ""
		entityType := ""
		if alert.Labels != nil {
			namespace = alert.Labels["namespace"]
			entityType = alert.Labels["entity_type"]
		}
		decision := s.policyEngine.EvaluateAlert(ctx, alert.Source, alert.Title, alert.Severity, namespace, entityType, alert.Labels)
		switch decision.Action {
		case "suppress", "suppress_alert":
			log.Printf("[pipeline] policy=%q suppressed alert %s title=%q", decision.PolicyName, alert.ID, alert.Title)
			return
		case "suppress_incident":
			// Allow alert through but mark it so createIncident is skipped.
			if alert.Labels == nil {
				alert.Labels = make(map[string]string)
			}
			alert.Labels["_policy_suppress_incident"] = decision.PolicyName
		}
	} else if isKnownTestWorkload(alert) {
		log.Printf("[pipeline] suppressed test workload alert %s title=%q (known test fixture)", alert.ID, alert.Title)
		return
	}
	if s.processAlertResolvedStage(ctx, alert) {
		return
	}
	if s.processAlertRCEStage(ctx, alert) {
		return
	}
	s.processAlertFullStage(ctx, alert)
}

// processAlertResolvedStage handles resolved alerts immediately.
// Returns true if the alert was terminal (resolved) and no further processing is needed.
// Also publishes the normalized alert for non-resolved cases.
func (s *AlertPipelineService) processAlertResolvedStage(ctx context.Context, alert *models.Alert) (terminal bool) {
	if strings.EqualFold(alert.Status, "resolved") {
		s.handleResolvedAlert(ctx, alert)
		return true
	}
	s.publishNormalized(alert)
	return false
}

// processAlertRCEStage runs the authoritative Root Cause Engine before scoring.
// Returns true if the RCE handled the alert (ATTACH or CREATE_ROOT) — no further stages needed.
func (s *AlertPipelineService) processAlertRCEStage(ctx context.Context, alert *models.Alert) (handled bool) {
	// Ensure entity_type and k8s labels are populated for both the staged pipeline path
	// (where processAlert is bypassed) and the legacy non-staged path (idempotent).
	enrichEntityLabels(alert)
	if s.rootCauseEngine == nil {
		return false
	}
	start := time.Now()
	corrAlert := buildCorrAlert(alert)
	rcaDecision, rcaErr := s.rootCauseEngine.Evaluate(ctx, corrAlert)
	if rcaErr != nil || rcaDecision == nil {
		return false
	}
	switch rcaDecision.Action {
	case correlation.RCAActionAttachToRoot:
		s.mergeAlertIntoIncident(ctx, alert, *rcaDecision.RootIncidentID,
			s.emptyResultWithScore(1.0, "root_cause"),
			"Alert Attached (Root Cause Engine)",
			fmt.Sprintf("Alert '%s' (level %s) is a downstream effect of root '%s'. Attached without scoring.",
				alert.Title, rcaDecision.AlertLevel.String(), rcaDecision.RootEntityLabel))
		if s.alertBuffer != nil {
			incID := *rcaDecision.RootIncidentID
			_ = s.alertBuffer.SetAlertState(ctx, correlation.AlertStateRecord{
				AlertID:    alert.ID,
				State:      correlation.AlertStateAttached,
				IncidentID: &incID,
				Reason:     rcaDecision.Reason,
			})
		}
		log.Printf("RCE ATTACH alert=%s incident=%s reason=%s", alert.ID, *rcaDecision.RootIncidentID, rcaDecision.Reason)
		rceAttachResult := s.emptyResultWithScore(1.0, "root_cause_engine")
		rceAttachResult.Reasoning = fmt.Sprintf("RCE attach: %s is downstream of root entity '%s'. %s", alert.Title, rcaDecision.RootEntityLabel, rcaDecision.Reason)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			saveCtx, cancel := context.WithTimeout(s.svcCtx, 10*time.Second)
			defer cancel()
			s.saveCorrelationResult(saveCtx, alert, rceAttachResult, time.Since(start), rcaDecision.RootIncidentID)
		}()
		return true
	case correlation.RCAActionCreateRoot:
		if id, err := s.createRootIncident(ctx, alert, rcaDecision); err == nil && id != uuid.Nil {
			s.suppressDescendantAlerts(ctx, rcaDecision.AffectedEntities, id, rcaDecision.RootEntityLabel)
			go s.reabsorbOrphanedIncidents(s.svcCtx, id, alert, rcaDecision)

			// Use enrichIncidentV2 (not bare triggerRCA) — this classifies ontology,
			// runs CACIE, writes rca_decisions, and traverses topology.
			// Previously triggerRCA was called directly, leaving ontology_domain NULL
			// and rca_decisions empty for 81% of incidents (the entire RCE path).
			rceResult := s.emptyFinalResult()
			rceResult.FinalScore = 1.0
			rceResult.DominantStrategy = "root_cause_engine"
			rceResult.Decision = correlation.DecisionCreateIncident

			var ontoForRCE *correlation.OntologyResult
			if s.ontologyEngine != nil {
				ontoForRCE = s.ontologyEngine.Classify(corrAlert)
			}
			go s.enrichIncidentV2(alert, id, rceResult, ontoForRCE, rcaDecision)

			log.Printf("RCE CREATE_ROOT alert=%s incident=%s entity=%s blast=%d",
				alert.ID, id, rcaDecision.RootEntityLabel, len(rcaDecision.AffectedEntities))
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				saveCtx, cancel := context.WithTimeout(s.svcCtx, 10*time.Second)
				defer cancel()
				s.saveCorrelationResult(saveCtx, alert, rceResult, time.Since(start), &id)
			}()
		}
		return true
	}
	log.Printf("RCE NO_ROOT alert=%s level=%s reason=%q — proceeding to 4-strategy scoring",
		alert.ID, rcaDecision.AlertLevel.String(), rcaDecision.Reason)
	return false
}

// processAlertFullStage runs the 4-strategy parallel scoring pipeline.
// Call only after the resolved and RCE stages have been checked.
func (s *AlertPipelineService) processAlertFullStage(ctx context.Context, alert *models.Alert) {
	start := time.Now()

	// Enrich host/infra alerts (e.g. Dynatrace HOST-type) with CloudStack cluster from topology.
	s.enrichInfraLabels(ctx, alert)

	corrAlert := buildCorrAlert(alert)

	// V2: Ontology classification — run before scoring so domain is available for adaptive weights.
	var ontologyResult *correlation.OntologyResult
	if s.ontologyEngine != nil {
		ontologyResult = s.ontologyEngine.Classify(corrAlert)
	}

	// V2: Per-domain adaptive strategy weights.
	if s.adaptiveLearning != nil && ontologyResult != nil {
		weights := s.adaptiveLearning.GetWeightsForContext(corrAlert)
		s.parallelEngine.SetStrategyWeights(weights)
	}

	// Run all 4 strategies in parallel.
	parallelResult, err := s.parallelEngine.CorrelateAlert(ctx, corrAlert)
	if err != nil {
		log.Printf("Parallel correlation failed for alert %s: %v", alert.ID, err)
		return
	}

	// Aggregate results with weighted scoring.
	finalResult, err := s.aggregator.AggregateCorrelationResults(ctx, corrAlert, parallelResult.StrategyResults)
	if err != nil {
		log.Printf("Aggregation failed for alert %s: %v", alert.ID, err)
		return
	}

	elapsed := time.Since(start).Round(time.Millisecond)
	log.Printf("Alert %s: decision=%s score=%.3f dominant=%s elapsed=%v",
		alert.ID, finalResult.Decision, finalResult.FinalScore,
		finalResult.DominantStrategy, elapsed)
	metrics.CorrelationDecisions.WithLabelValues(string(finalResult.Decision)).Inc()
	metrics.CorrelationDuration.WithLabelValues(string(finalResult.Decision)).Observe(elapsed.Seconds())

	log.Printf("RCA_DECISION alert=%s score=%.3f decision=%s dominant=%s topology_score=%.3f temporal_score=%.3f semantic_score=%.3f reasoning=%q",
		alert.ID,
		finalResult.FinalScore,
		finalResult.Decision,
		finalResult.DominantStrategy,
		getStrategyScore(finalResult.StrategyResults, "topology"),
		getStrategyScore(finalResult.StrategyResults, "temporal"),
		getStrategyScore(finalResult.StrategyResults, "semantic"),
		finalResult.Reasoning)

	if ontologyResult != nil {
		if finalResult.Metadata == nil {
			finalResult.Metadata = make(map[string]interface{})
		}
		finalResult.Metadata["ontology_domain"] = string(ontologyResult.Domain)
		finalResult.Metadata["ontology_class"] = string(ontologyResult.Class)
	}

	// V2: Store alert vector embedding asynchronously (best-effort).
	if s.vectorRepo != nil && ontologyResult != nil {
		go func(a *models.Alert, onto *correlation.OntologyResult) {
			bertURL := "http://alerthub-bert-service.aileron.svc.cluster.local:8765/embed"
			if emb, err := fetchBERTEmbedding(bertURL, a.Title+" "+a.Description); err == nil {
				cluster := ""
				if a.Labels != nil {
					cluster = a.Labels["k8s.cluster.name"]
				}
				_ = s.vectorRepo.StoreEmbedding(s.svcCtx, a.ID, emb,
					string(onto.Domain), string(onto.Class), cluster, a.Severity, "bert-base-uncased")
			}
		}(alert, ontologyResult)
	}

	s.publishCorrelationResult(alert, finalResult, elapsed)

	var incidentID *uuid.UUID

	switch finalResult.Decision {
	case correlation.DecisionCreateIncident:
		if id, err := s.createIncident(ctx, alert, finalResult); err == nil && id != uuid.Nil {
			incidentID = &id
			// enrichIncidentV2 computes topo+hypotheses and fires triggerRCA with full context.
			// Pass nil as firstRCEDecision: we are on the RCE NO_ROOT path here.
			go s.enrichIncidentV2(alert, id, finalResult, ontologyResult, nil)
		}

	case correlation.DecisionMergeIncident:
		id := s.mergeIncident(ctx, alert, finalResult)
		if id != uuid.Nil {
			incidentID = &id
			// Run enrichIncidentV2 for merges too — this runs CACIE, writes rca_decisions,
			// and fires triggerRCA with full go_context. Without this, rca_decisions is
			// never written for merged incidents (the majority path).
			go s.enrichIncidentV2(alert, id, finalResult, ontologyResult, nil)
		} else {
			if id, err := s.createIncident(ctx, alert, finalResult); err == nil && id != uuid.Nil {
				incidentID = &id
				// enrichIncidentV2 computes topo+hypotheses and fires triggerRCA with full context.
				go s.enrichIncidentV2(alert, id, finalResult, ontologyResult, nil)
			}
		}

	case correlation.DecisionMonitor:
		topoScore := 0.0
		if sr, ok := finalResult.StrategyResults["topology"]; ok {
			topoScore = sr.Score
		}
		if topoScore >= 0.5 && finalResult.BestMatch != nil {
			id := s.mergeIncident(ctx, alert, finalResult)
			if id != uuid.Nil {
				incidentID = &id
				go s.triggerRCA(alert, id, finalResult, nil)
				log.Printf("Alert %s (monitortopology-merge) linked to incident %s", alert.ID, id)
				break
			}
		}
		holdWindow := 45 * time.Second
		if alert.Severity == "critical" {
			holdWindow = 15 * time.Second
		}
		s.wg.Add(1)
		fr := *finalResult
		go func() {
			defer s.wg.Done()
			s.deferredIncidentCreation(alert, &fr, holdWindow)
		}()
		log.Printf("Alert %s queued for deferred correlation (score=%.3f, hold=%v)",
			alert.ID, finalResult.FinalScore, holdWindow)

		if s.db != nil {
			var linkedID uuid.UUID
			if err := s.db.QueryRowContext(ctx,
				`SELECT incident_id FROM alerts WHERE id = $1 AND incident_id IS NOT NULL`, alert.ID,
			).Scan(&linkedID); err == nil && linkedID != uuid.Nil && s.incidentSvc != nil {
				s.incidentSvc.AddTimelineEvent(ctx, linkedID, uuid.Nil, "alert_monitoring",
					"Alert Under Monitoring",
					fmt.Sprintf("Alert '%s' received with medium confidence (score %.0f%%). Watching for correlated events.", alert.Title, finalResult.FinalScore*100),
					map[string]interface{}{"alert_id": alert.ID.String(), "score": finalResult.FinalScore},
				)
			}
		}

	case correlation.DecisionDiscard:
		// True discard: below threshold means the signal is too weak to assert any relationship.
		// Do NOT attach to an incident — attaching low-score alerts inflates alert counts with
		// noise and creates split-brain where alert_ids contains alerts whose incident_id FK
		// points at a completely different incident.
		log.Printf("Alert %s discarded (low signal, score=%.3f)", alert.ID, finalResult.FinalScore)
	}

	if s.alertBuffer != nil {
		state := correlation.AlertStateIncidentCreated
		if incidentID == nil {
			state = correlation.AlertStateBuffered
		}
		_ = s.alertBuffer.SetAlertState(ctx, correlation.AlertStateRecord{
			AlertID:    alert.ID,
			State:      state,
			IncidentID: incidentID,
			Reason:     string(finalResult.Decision),
		})
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		saveCtx, cancel := context.WithTimeout(s.svcCtx, 10*time.Second)
		defer cancel()
		s.saveCorrelationResult(saveCtx, alert, finalResult, elapsed, incidentID)
	}()
}

// buildCorrAlert converts a models.Alert to the correlation.Alert type used by all engines.
func buildCorrAlert(alert *models.Alert) *correlation.Alert {
	corrAlert := &correlation.Alert{
		ID:          alert.ID,
		Title:       alert.Title,
		Description: alert.Description,
		Severity:    alert.Severity,
		Source:      alert.Source,
		Tags:        alert.Tags,
		Labels:      alert.Labels,
		Metadata:    alert.Metadata,
		CreatedAt:   alert.CreatedAt,
		Fingerprint: alert.Fingerprint,
	}
	if corrAlert.Fingerprint == "" {
		corrAlert.Fingerprint = alert.ID.String()
	}
	if corrAlert.RootCauseEntity == "" && alert.Labels != nil {
		for _, key := range []string{"rootCauseEntity", "root_cause_entity"} {
			if v := alert.Labels[key]; v != "" {
				corrAlert.RootCauseEntity = v
				break
			}
		}
	}
	return corrAlert
}

// StagedPipeline adapters 

type fastPathAdapter struct{ svc *AlertPipelineService }

func (a *fastPathAdapter) Process(ctx context.Context, alert *models.Alert) (forwarded bool, err error) {
	terminal := a.svc.processAlertResolvedStage(ctx, alert)
	return !terminal, nil
}

type topoPathAdapter struct{ svc *AlertPipelineService }

func (a *topoPathAdapter) Process(ctx context.Context, alert *models.Alert) (matched bool, err error) {
	// Resolved alerts can arrive here via the critical-severity bypass (fastCh full topoCh)
	// or via the fast-worker error path. Handle them the same way as the fast stage does.
	if strings.EqualFold(alert.Status, "resolved") {
		a.svc.handleResolvedAlert(ctx, alert)
		return true, nil
	}
	return a.svc.processAlertRCEStage(ctx, alert), nil
}

type fullCorrelationAdapter struct{ svc *AlertPipelineService }

func (a *fullCorrelationAdapter) Process(ctx context.Context, alert *models.Alert) error {
	// Resolved alerts can arrive here via the topo-worker error path. Never run full
	// correlation on a resolved alert — it creates spurious incidents.
	if strings.EqualFold(alert.Status, "resolved") {
		a.svc.handleResolvedAlert(ctx, alert)
		return nil
	}
	a.svc.processAlertFullStage(ctx, alert)
	return nil
}

// NewStagedPipelineForService wires a StagedPipeline with the three processing stages
// and attaches it to the service so EnqueueAlert uses it automatically.
func NewStagedPipelineForService(svc *AlertPipelineService) *StagedPipeline {
	sp := NewStagedPipeline(
		&fastPathAdapter{svc},
		&topoPathAdapter{svc},
		&fullCorrelationAdapter{svc},
	)
	svc.stagedPipeline = sp
	return sp
}

// createRootIncident creates an incident where this alert is the identified root cause.
// It uses the existing createIncident path but enriches the incident metadata with
// root entity info so downstream alerts can find and attach to it.
func (s *AlertPipelineService) createRootIncident(ctx context.Context, alert *models.Alert, decision *correlation.RCADecision) (uuid.UUID, error) {
	// Serialize on the root entity ID so two concurrent alerts with the same root entity
	// don't both race past the "no incident found" check and each create their own incident.
	if decision.RootEntityID != "" {
		release := s.acquireClusterLock("rce:" + decision.RootEntityID)
		defer release()

		// After acquiring the lock, re-check whether another goroutine just created one.
		if s.db != nil {
			var existingID uuid.UUID
			err := s.db.QueryRowContext(ctx, `
				SELECT id FROM incidents
				WHERE auto_created = TRUE
				  AND status IN ('open','investigating')
				  AND created_at >= NOW() - INTERVAL '2 hours'
				  AND correlation_id = $1
				ORDER BY created_at DESC LIMIT 1
			`, decision.RootEntityID).Scan(&existingID)
			if err == nil && existingID != uuid.Nil {
				s.mergeAlertIntoIncident(ctx, alert, existingID,
					s.emptyResultWithScore(1.0, "root_cause_engine"),
					"Alert Merged (Same Root Cause Entity)",
					fmt.Sprintf("Alert '%s' shares root entity '%s' — merged into in-flight incident.", alert.Title, decision.RootEntityLabel))
				log.Printf("RCE: alert %s merged into in-flight incident %s (root_entity=%s)", alert.ID, existingID, decision.RootEntityID)
				return existingID, nil
			}
		}
	}

	// Build a synthetic FinalCorrelationResult so createIncident has what it needs.
	result := s.emptyFinalResult()
	result.FinalScore = 1.0
	result.DominantStrategy = "root_cause_engine"
	result.Decision = correlation.DecisionCreateIncident

	id, err := s.createIncident(ctx, alert, result, decision.RootEntityID)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, err
	}

	// Belt-and-suspenders: also update davis_ai_analysis with root entity metadata.
	if s.db != nil {
		_, dbErr := s.db.ExecContext(ctx, `
			UPDATE incidents SET
				correlation_id = COALESCE(NULLIF(correlation_id,''), $1),
				davis_ai_analysis = jsonb_set(
					jsonb_set(
						jsonb_set(
							COALESCE(davis_ai_analysis, '{}'::jsonb),
							'{root_entity_id}', to_jsonb($1::text)
						),
						'{root_entity_label}', to_jsonb($2::text)
					),
					'{root_level}', to_jsonb($3::int)
				)
			WHERE id = $4
		`, decision.RootEntityID, decision.RootEntityLabel, int(decision.RootLevel), id)
		if dbErr != nil {
			log.Printf("createRootIncident: failed to set root entity on incident %s: %v", id, dbErr)
		} else {
			log.Printf("createRootIncident: correlation_id=%q set on incident %s", decision.RootEntityID, id)
		}
	}

	if s.alertBuffer != nil {
		incID := id
		_ = s.alertBuffer.SetAlertState(ctx, correlation.AlertStateRecord{
			AlertID:    alert.ID,
			State:      correlation.AlertStateIncidentCreated,
			IncidentID: &incID,
			Reason:     decision.Reason,
		})
	}
	return id, nil
}

// suppressDescendantAlerts marks blast-radius alerts as suppressed in Redis state.
// It does NOT change their DB status — suppression is advisory and stored in Redis
// so the next processAlert call for a matching entity can skip re-creating an incident.
func (s *AlertPipelineService) suppressDescendantAlerts(ctx context.Context, entities []string, rootIncidentID uuid.UUID, rootEntity string) {
	if s.alertBuffer == nil || len(entities) == 0 {
		return
	}
	reason := correlation.SuppressedReason{
		RootIncidentID: rootIncidentID,
		RootEntity:     rootEntity,
		Reason:         "downstream of root cause detected by RootCauseEngine",
		SuppressedAt:   time.Now(),
	}
	for _, entity := range entities {
		if s.db == nil {
			continue
		}
		rows, err := s.db.QueryContext(ctx, `
			SELECT id FROM alerts
			WHERE status = 'open'
			  AND created_at >= NOW() - INTERVAL '2 hours'
			  AND (
			    labels->>'node' = $1 OR labels->>'host.name' = $1
			    OR labels->>'cluster' = $1 OR labels->>'app' = $1
			    OR labels->>'k8s.node.name' = $1
			  )
		`, entity)
		if err != nil {
			continue
		}
		for rows.Next() {
			var alertID uuid.UUID
			if rows.Scan(&alertID) == nil && alertID != uuid.Nil {
				_ = s.alertBuffer.SetSuppressed(ctx, alertID, reason)
			}
		}
		if err := rows.Err(); err != nil {
			log.Printf("[pipeline] scan descendant alerts for suppression: %v", err)
		}
		rows.Close()
	}
}

// reabsorbOrphanedIncidents is called after a BM/high-level root incident is created.
// It finds open incidents created while the root alert was missing (child-first arrival)
// and pulls their alerts into the root incident, closing the orphans.
//
// Only absorbs incidents whose recorded root_level is LOWER than the incoming root
// (pod/node < BM) so peer-level incidents (another BM) are never touched.
func (s *AlertPipelineService) reabsorbOrphanedIncidents(
	ctx context.Context,
	rootIncidentID uuid.UUID,
	rootAlert *models.Alert,
	decision *correlation.RCADecision,
) {
	if s.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var clusters, nodes []string
	if rootAlert.Labels != nil {
		for _, k := range []string{"cluster", "k8s.cluster.name", "k8s.cluster.names", "cloudstack_cluster"} {
			if v := rootAlert.Labels[k]; v != "" {
				for _, c := range strings.Split(v, ",") {
					if c = strings.TrimSpace(c); c != "" {
						clusters = append(clusters, c)
					}
				}
			}
		}
		for _, k := range []string{"k8s.node.name", "node", "kvm_host"} {
			if v := rootAlert.Labels[k]; v != "" {
				nodes = append(nodes, v)
			}
		}
	}
	for _, entity := range decision.AffectedEntities {
		nodes = append(nodes, entity)
	}
	if len(clusters) == 0 && len(nodes) == 0 {
		return
	}
	if clusters == nil {
		clusters = []string{}
	}
	if nodes == nil {
		nodes = []string{}
	}

	orphanRows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT i.id, i.alert_ids::text,
		       COALESCE((i.davis_ai_analysis->>'root_level')::int, -1) AS root_lvl
		FROM incidents i
		WHERE i.auto_created = TRUE
		  AND i.status IN ('open', 'investigating')
		  AND i.id <> $1
		  AND i.created_at >= NOW() - INTERVAL '2 hours'
		  AND EXISTS (
		    SELECT 1 FROM alerts a
		    WHERE a.id::text = ANY(SELECT jsonb_array_elements_text(i.alert_ids))
		      AND (
		        a.labels->>'cluster'             = ANY($2::text[])
		        OR a.labels->>'k8s.cluster.name' = ANY($2::text[])
		        OR a.labels->>'k8s.node.name'    = ANY($3::text[])
		        OR a.labels->>'node'             = ANY($3::text[])
		        OR a.labels->>'host.name'        = ANY($3::text[])
		      )
		  )
		ORDER BY i.created_at ASC
	`, rootIncidentID, pq.Array(clusters), pq.Array(nodes))
	if err != nil {
		log.Printf("[reabsorb] query orphans root=%s: %v", rootIncidentID, err)
		return
	}
	defer orphanRows.Close()

	type orphanRec struct {
		id       uuid.UUID
		alertIDs string
		rootLvl  int
	}
	var orphans []orphanRec
	for orphanRows.Next() {
		var o orphanRec
		if err := orphanRows.Scan(&o.id, &o.alertIDs, &o.rootLvl); err == nil {
			orphans = append(orphans, o)
		}
	}
	if err := orphanRows.Err(); err != nil {
		log.Printf("[pipeline] scan orphan incidents: %v", err)
	}
	orphanRows.Close()

	if len(orphans) == 0 {
		return
	}

	rootLevel := int(decision.RootLevel)
	absorbed := 0
	for _, o := range orphans {
		if o.rootLvl >= rootLevel && o.rootLvl != -1 {
			continue // don't absorb peer or higher-level incidents
		}
		// Re-parent all alerts from the orphan to the root incident.
		if _, err := s.db.ExecContext(ctx, `
			UPDATE alerts SET incident_id = $1, correlation_id = $2 WHERE incident_id = $3
		`, rootIncidentID, decision.RootEntityID, o.id); err != nil {
			log.Printf("[reabsorb] re-parent alerts orphan=%s root=%s: %v", o.id, rootIncidentID, err)
			continue
		}
		// Merge alert_ids into root incident.
		if _, err := s.db.ExecContext(ctx, `
			UPDATE incidents SET
				alert_ids  = (SELECT jsonb_agg(DISTINCT elem) FROM (
					SELECT jsonb_array_elements_text(alert_ids) AS elem FROM incidents WHERE id = $1
					UNION ALL
					SELECT jsonb_array_elements_text($2::jsonb)
				) sub),
				updated_at = NOW()
			WHERE id = $1
		`, rootIncidentID, o.alertIDs); err != nil {
			log.Printf("[reabsorb] merge alert_ids root=%s: %v", rootIncidentID, err)
		}
		// Close the orphan.
		if _, err := s.db.ExecContext(ctx, `
			UPDATE incidents SET status='resolved', resolved_at=NOW(),
			    resolution_notes='Reabsorbed into root incident after late BM alert arrival',
			    updated_at=NOW()
			WHERE id = $1
		`, o.id); err != nil {
			log.Printf("[reabsorb] close orphan=%s: %v", o.id, err)
			continue
		}
		log.Printf("Reabsorbed orphan incident %s into root %s (entity=%s)",
			o.id, rootIncidentID, decision.RootEntityLabel)
		absorbed++
	}
	if absorbed > 0 {
		log.Printf("Reabsorption done: %d orphans root incident %s (entity=%s)",
			absorbed, rootIncidentID, decision.RootEntityLabel)
	}
}

// emptyResultWithScore builds a minimal FinalCorrelationResult for RCE-driven paths.
// fetchIncidentMeta returns (number, severity, title) for notification purposes.
func (s *AlertPipelineService) fetchIncidentMeta(ctx context.Context, incidentID uuid.UUID) (number, severity, title string) {
	if s.db == nil {
		return "", "", ""
	}
	qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_ = s.db.QueryRowContext(qctx,
		`SELECT COALESCE(incident_number,''), COALESCE(severity,''), COALESCE(title,'') FROM incidents WHERE id=$1`,
		incidentID).Scan(&number, &severity, &title)
	return
}

func (s *AlertPipelineService) emptyResultWithScore(score float64, strategy string) *correlation.FinalCorrelationResult {	return &correlation.FinalCorrelationResult{
		FinalScore:       score,
		DominantStrategy: strategy,
		Decision:         correlation.DecisionMergeIncident,
	}
}

// emptyFinalResult builds a zero-value FinalCorrelationResult.
func (s *AlertPipelineService) emptyFinalResult() *correlation.FinalCorrelationResult {
	return &correlation.FinalCorrelationResult{}
}

// deferredIncidentCreation holds an alert for the given window then creates or merges
// an incident if the alert is still unlinked.  This is the storm-grouping path: alerts
// that arrive in a burst from a single root-cause failure wait here so the highest-signal
// alert (usually the BM/node failure) can open the incident first; descendants then merge.
func (s *AlertPipelineService) deferredIncidentCreation(
	alert *models.Alert,
	result *correlation.FinalCorrelationResult,
	holdWindow time.Duration,
) {
	select {
	case <-time.After(holdWindow):
	case <-s.stopCh:
		return // service shutting down — abandon deferred creation
	}
	// Use a bounded context for all post-hold work so DB operations don't outlive
	// the K8s SIGKILL grace period after shutdown.
	ctx, ctxCancel := context.WithTimeout(s.svcCtx, 25*time.Second)
	defer ctxCancel()

	// Check if the alert was already linked to an incident while we waited.
	if s.db != nil {
		var existingID uuid.UUID
		err := s.db.QueryRowContext(ctx,
			`SELECT incident_id FROM alerts WHERE id = $1 AND incident_id IS NOT NULL`,
			alert.ID,
		).Scan(&existingID)
		if err == nil && existingID != uuid.Nil {
			log.Printf("Alert %s linked to incident %s during hold window", alert.ID, existingID)
			return
		}
	}

	// Re-run aggregation with the now-populated incident table to pick up any
	// incidents that opened while we were waiting.
	corrAlert := &correlation.Alert{
		ID:          alert.ID,
		Title:       alert.Title,
		Description: alert.Description,
		Severity:    alert.Severity,
		Source:      alert.Source,
		Tags:        alert.Tags,
		Labels:      alert.Labels,
		Metadata:    alert.Metadata,
		Fingerprint: alert.Fingerprint,
	}
	if corrAlert.Fingerprint == "" {
		corrAlert.Fingerprint = alert.ID.String()
	}

	// Refresh strategy scores against the current topology/incident state.
	// The original scores from ~45 seconds ago have stale temporal decay and
	// miss incidents that opened during the hold window.
	freshStrategyResults := result.StrategyResults // fallback: use original if re-run fails
	if s.parallelEngine != nil {
		if freshResult, err := s.parallelEngine.CorrelateAlert(ctx, corrAlert); err == nil {
			freshStrategyResults = freshResult.StrategyResults
		} else {
			log.Printf("deferred: re-run correlation failed for alert %s: %v — using original scores", alert.ID, err)
		}
	}

	reResult, err := s.aggregator.AggregateCorrelationResults(ctx, corrAlert, freshStrategyResults)
	if err == nil && reResult.Decision == correlation.DecisionMergeIncident && reResult.BestMatch != nil {
		id := s.mergeIncident(ctx, alert, reResult)
		if id != uuid.Nil {
			log.Printf("Alert %s deferred-merged into incident %s (re-eval after %v)", alert.ID, id, holdWindow)
			s.saveCorrelationResult(ctx, alert, reResult, 0, &id)
			return
		}
	}

	// Last-resort check before creating: use findOpenIncidentForAlert which does a
	// broader cluster + title-key match. Catches storms where the re-evaluation still
	// returns Monitor because the first incident was just created milliseconds ago.
	if existingID := s.findOpenIncidentForAlert(ctx, alert); existingID != uuid.Nil {
		s.attachAlertToIncident(ctx, alert, existingID, result,
			"Alert Merged (Deferred Storm)",
			fmt.Sprintf("Alert '%s' (score %.0f%%) merged after hold window — same cluster/node as existing incident.", alert.Title, result.FinalScore*100))
		s.saveCorrelationResult(ctx, alert, result, 0, &existingID)
		log.Printf("Alert %s merged into incident %s via findOpenIncident after %v hold", alert.ID, existingID, holdWindow)
		return
	}

	// Still no match — create a new incident now.
	if id, err := s.createIncident(ctx, alert, result); err == nil && id != uuid.Nil {
		log.Printf("Alert %s created incident %s after %v hold (score=%.3f)",
			alert.ID, id, holdWindow, result.FinalScore)
		go s.triggerRCA(alert, id, result, nil)
		// Update the existing monitor PCR row with the incident that was eventually created.
		// Using UPDATE instead of a second INSERT prevents a duplicate row with
		// decision=monitor+incident_id, which was previously misread as a UUID leak.
		if s.db != nil {
			if _, err := s.db.ExecContext(ctx, `
				UPDATE pipeline_correlation_results
				SET incident_id = $1
				WHERE id = (
					SELECT id FROM pipeline_correlation_results
					WHERE alert_id = $2
					  AND decision = 'monitor'
					  AND incident_id IS NULL
					ORDER BY processed_at DESC
					LIMIT 1
				)
			`, id, alert.ID); err != nil {
				log.Printf("[pipeline] update monitor result incident_id alert=%s: %v", alert.ID, err)
			}
		}
	}
}

// saveCorrelationResult writes the pipeline decision to pipeline_correlation_results.
func (s *AlertPipelineService) saveCorrelationResult(
	ctx context.Context,
	alert *models.Alert,
	result *correlation.FinalCorrelationResult,
	elapsed time.Duration,
	incidentID *uuid.UUID,
) {
	if s.db == nil {
		return
	}

	var semanticScore, temporalScore, topologyScore, rulesScore float64
	if result.StrategyResults != nil {
		if sr, ok := result.StrategyResults["semantic"]; ok {
			semanticScore = sr.Score
		}
		if sr, ok := result.StrategyResults["temporal"]; ok {
			temporalScore = sr.Score
		}
		if sr, ok := result.StrategyResults["topology"]; ok {
			topologyScore = sr.Score
		}
		if sr, ok := result.StrategyResults["rules"]; ok {
			rulesScore = sr.Score
		}
	}

	matchedNodeLabel := ""
	rootCauseLabel := ""
	aiRootCause := ""
	if result.StrategyResults != nil {
		if topoSR, ok := result.StrategyResults["topology"]; ok && topoSR.Details != nil {
			if v, ok := topoSR.Details["matched_label"].(string); ok {
				matchedNodeLabel = v
			}
			if v, ok := topoSR.Details["root_cause"].(string); ok {
				rootCauseLabel = v
			}
		}
	}

	// V2: extract ontology and blast-radius fields stamped into metadata.
	domain := ""
	ontologyClass := ""
	blastRadiusCount := 0
	if result.Metadata != nil {
		if v, ok := result.Metadata["ai_root_cause"].(string); ok {
			aiRootCause = v
		}
		if v, ok := result.Metadata["ontology_domain"].(string); ok {
			domain = v
		}
		if v, ok := result.Metadata["ontology_class"].(string); ok {
			ontologyClass = v
		}
		if v, ok := result.Metadata["blast_radius_count"].(int); ok {
			blastRadiusCount = v
		}
	}
	// topo_root_entity mirrors root_cause_label when topology strategy ran.
	topoRootEntity := rootCauseLabel

	// V2: build operator-grade explainability report.
	var explanationJSON []byte
	corrAlert := &correlation.Alert{
		ID:          alert.ID,
		Title:       alert.Title,
		Description: alert.Description,
		Severity:    alert.Severity,
		Source:      alert.Source,
		Labels:      alert.Labels,
		Metadata:    alert.Metadata,
	}
	var ontoResult *correlation.OntologyResult
	if domain != "" {
		ontoResult = &correlation.OntologyResult{
			Domain: correlation.FailureDomain(domain),
			Class:  correlation.CanonicalFailureClass(ontologyClass),
		}
	}
	if report := correlation.GenerateExplainabilityReport(corrAlert, result, nil, nil, ontoResult); report != nil {
		explanationJSON, _ = report.ToJSON()
	}

	var incidentIDParam interface{}
	if incidentID != nil && *incidentID != uuid.Nil {
		incidentIDParam = *incidentID
	}

	var explanationParam interface{}
	if len(explanationJSON) > 0 {
		explanationParam = explanationJSON
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO pipeline_correlation_results (
			alert_id, incident_id, alert_title, alert_source, alert_severity,
			decision, final_score, dominant_strategy,
			semantic_score, temporal_score, topology_score, rules_score,
			reasoning, ai_root_cause, matched_node_label, root_cause_label,
			domain, ontology_class, topo_root_entity, blast_radius_count, explanation_json,
			elapsed_ms, processed_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,NOW())
	`,
		alert.ID, incidentIDParam, alert.Title, alert.Source, alert.Severity,
		string(result.Decision), result.FinalScore, result.DominantStrategy,
		semanticScore, temporalScore, topologyScore, rulesScore,
		result.Reasoning, aiRootCause, matchedNodeLabel, rootCauseLabel,
		domain, ontologyClass, topoRootEntity, blastRadiusCount, explanationParam,
		elapsed.Milliseconds(),
	)
	if err != nil {
		log.Printf("Failed to save correlation result for alert %s: %v", alert.ID, err)
	}
}

// normalizedAlertMsg is published to normalized-alerts after an alert is ingested.
type normalizedAlertMsg struct {
	ID           string                 `json:"id"`
	Title        string                 `json:"title"`
	Description  string                 `json:"description"`
	Severity     string                 `json:"severity"`
	Status       string                 `json:"status"`
	Source       string                 `json:"source"`
	SourceID     string                 `json:"source_id"`
	Fingerprint  string                 `json:"fingerprint"`
	Labels       map[string]string      `json:"labels,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
	NormalizedAt string                 `json:"normalized_at"`
}

// correlationResultMsg is published to correlation-results after correlation completes.
type correlationResultMsg struct {
	AlertID          string  `json:"alert_id"`
	Decision         string  `json:"decision"`
	Score            float64 `json:"score"`
	DominantStrategy string  `json:"dominant_strategy"`
	Reasoning        string  `json:"reasoning,omitempty"`
	IncidentID       string  `json:"incident_id,omitempty"`
	ProcessedAt      string  `json:"processed_at"`
	ElapsedMs        int64   `json:"elapsed_ms"`
}

func (s *AlertPipelineService) publishNormalized(alert *models.Alert) {
	if s.normalizedPublisher == nil {
		return
	}
	msg := normalizedAlertMsg{
		ID:           alert.ID.String(),
		Title:        alert.Title,
		Description:  alert.Description,
		Severity:     alert.Severity,
		Status:       alert.Status,
		Source:       alert.Source,
		SourceID:     alert.SourceID,
		Fingerprint:  alert.Fingerprint,
		Labels:       alert.Labels,
		Metadata:     alert.Metadata,
		NormalizedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := s.normalizedPublisher.PublishToTopic(NormalizedAlertsTopic, msg); err != nil {
		log.Printf("Failed to publish to %s for alert %s: %v", NormalizedAlertsTopic, alert.ID, err)
	}
}

func (s *AlertPipelineService) publishCorrelationResult(alert *models.Alert, result *correlation.FinalCorrelationResult, elapsed time.Duration) {
	if s.correlationPublisher == nil {
		return
	}
	msg := correlationResultMsg{
		AlertID:          alert.ID.String(),
		Decision:         string(result.Decision),
		Score:            result.FinalScore,
		DominantStrategy: result.DominantStrategy,
		Reasoning:        result.Reasoning,
		ProcessedAt:      time.Now().UTC().Format(time.RFC3339),
		ElapsedMs:        elapsed.Milliseconds(),
	}
	if result.BestMatch != nil {
		msg.IncidentID = result.BestMatch.ID.String()
	}
	if err := s.correlationPublisher.PublishToTopic(CorrelationResultsTopic, msg); err != nil {
		log.Printf("Failed to publish to %s for alert %s: %v", CorrelationResultsTopic, alert.ID, err)
	}
}

// createIncident builds and persists an auto-created incident; returns the new incident ID.
// It runs several dedup checks in order of specificity before inserting a new row.
// An optional correlationID can be passed as the first variadic arg to set it atomically at INSERT.
func (s *AlertPipelineService) createIncident(ctx context.Context, alert *models.Alert, result *correlation.FinalCorrelationResult, correlationID ...string) (uuid.UUID, error) {
	if s.incidentSvc == nil {
		return uuid.Nil, nil
	}

	// Respect suppress_incident policy decision set in processAlert.
	// When EvaluateAlert() returned suppress_incident, we marked the alert label
	// so the expensive dedup path is never entered.
	if alert.Labels != nil {
		if policyName, blocked := alert.Labels["_policy_suppress_incident"]; blocked {
			log.Printf("[pipeline] createIncident blocked by policy=%q for alert %s", policyName, alert.ID)
			return uuid.Nil, nil
		}
	}

	// In-flight dedup: prevent race conditions when multiple identical alerts 
	// arrive simultaneously (e.g., Dynatrace fan-out) before any incident row exists.
	dedupKey := alertDedupKey(alert)
	_, alreadyRunning := s.inflightDedup.LoadOrStore(dedupKey, true)
	if !alreadyRunning {
		// We acquired the lock; release it when this function returns regardless of outcome.
		defer s.inflightDedup.Delete(dedupKey)
	}
	if alreadyRunning {
		// Another goroutine is creating an incident for this dedup key.
		// Poll for up to 3s so we can merge rather than create a duplicate incident.
		// Use a ticker + context select to avoid blocking when ctx is cancelled.
		titleKey := extractTitleKey(alert.Title)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		deadline := time.After(3 * time.Second)
		for {
			select {
			case <-ctx.Done():
				return uuid.Nil, ctx.Err()
			case <-deadline:
				log.Printf("Alert %s in-flight wait timed out — proceeding to create", alert.ID)
				goto afterWait
			case <-ticker.C:
				if s.db == nil {
					continue
				}
				var existingID uuid.UUID
				err := s.db.QueryRowContext(ctx, `
					SELECT id FROM incidents
					WHERE auto_created = TRUE
					  AND status IN ('open','investigating')
					  AND created_at >= NOW() - INTERVAL '5 minutes'
					  AND title ILIKE '%' || $1 || '%'
					  AND source = $2
					ORDER BY created_at DESC LIMIT 1
				`, titleKey, alert.Source).Scan(&existingID)
				if err == nil && existingID != uuid.Nil {
					s.mergeAlertIntoIncident(ctx, alert, existingID, result,
						"Alert Merged (Same Alert Burst)",
						fmt.Sprintf("Alert '%s' merged during concurrent incident creation.", alert.Title))
					return existingID, nil
				}
			}
		}
	afterWait:
		// C2: After the polling window, do one final DB check before creating a new
		// incident.  The goroutine that held the in-flight key may have committed its
		// INSERT between the last poll tick and the deadline — we don't want to create
		// a duplicate.
		if s.db != nil {
			titleKey2 := extractTitleKey(alert.Title)
			var recheck uuid.UUID
			if s.db.QueryRowContext(ctx, `
				SELECT id FROM incidents
				WHERE auto_created = TRUE
				  AND status IN ('open','investigating')
				  AND created_at >= NOW() - INTERVAL '10 seconds'
				  AND title ILIKE '%' || $1 || '%'
				  AND source = $2
				ORDER BY created_at DESC LIMIT 1
			`, titleKey2, alert.Source).Scan(&recheck) == nil && recheck != uuid.Nil {
				s.mergeAlertIntoIncident(ctx, alert, recheck, result,
					"Alert Merged (Post-Wait Dedup)",
					fmt.Sprintf("Alert '%s' merged into in-flight incident.", alert.Title))
				return recheck, nil
			}
		}
	}

	// Per-cluster serialization: prevents S23-style burst races 
	// When a new cluster has no open incident, 40 concurrent goroutines can all
	// pass cascade checks simultaneously and each create their own incident.
	// Holding this mutex ensures only one goroutine runs the cascade checks+create
	// at a time per cluster; subsequent goroutines find the just-created incident.
	// E2: non-clustered alerts (empty cluster key) use source as the lock scope so
	// they are still serialized and don't bypass the mutex entirely.
	clusterKey := extractLabelFromAlert(alert, "cluster", "kubernetes_cluster", "k8s.cluster.name")
	if clusterKey == "" {
		clusterKey = "source:" + alert.Source
	}
	release := s.acquireClusterLock(clusterKey)
	defer release()

	// Flapping host/VM recurrence: reopen recently-resolved incident 
	// Dynatrace host problems close and re-fire as new source_ids, so the 5-min
	// dedup above never sees the previous incident (it's resolved). Check whether
	// the same host had a resolved incident in the last 4 hours and reopen it.
	if s.db != nil {
		hostKey := extractLabelFromAlert(alert, "host", "host.name", "hostname")
		if hostKey == "" {
			hostKey = extractLabelFromAlert(alert, "instance")
		}
		if hostKey != "" {
			titleKey := extractTitleKey(alert.Title)
			var resolvedID uuid.UUID
			resolvedErr := s.db.QueryRowContext(ctx, `
				SELECT id FROM incidents
				WHERE auto_created = TRUE
				  AND status IN ('resolved','closed')
				  AND resolved_at >= NOW() - INTERVAL '4 hours'
				  AND title ILIKE '%' || $1 || '%'
				  AND source = $2
				  AND (topology_path ILIKE '%' || $3 || '%' OR description ILIKE '%' || $3 || '%')
				ORDER BY resolved_at DESC LIMIT 1
			`, titleKey, alert.Source, hostKey).Scan(&resolvedID)
			if resolvedErr == nil && resolvedID != uuid.Nil {
				if _, err := s.db.ExecContext(ctx, `
					UPDATE incidents
					SET status = 'open', resolved_at = NULL, updated_at = NOW(),
					    recurrence_count = COALESCE(recurrence_count, 0) + 1
					WHERE id = $1
				`, resolvedID); err != nil {
					log.Printf("[pipeline] reopen flapping host incident=%s: %v", resolvedID, err)
				}
				s.attachAlertToIncident(ctx, alert, resolvedID, result,
					"Alert Merged (Flapping — Reopened)",
					fmt.Sprintf("Alert '%s' on host '%s' matched a recently-resolved incident — reopened (recurrence).", alert.Title, hostKey))
				log.Printf("Alert %s reopened resolved incident %s (flapping host=%s)", alert.ID, resolvedID, hostKey)
				return resolvedID, nil
			}
		}

		// K8s workload flapping: same cluster+namespace+workload with a resolved incident in 4h.
		workload := extractLabelFromAlert(alert, "deployment", "workload", "app", "k8s.workload.name", "k8s.deployment.name")
		namespace := extractLabelFromAlert(alert, "namespace", "kubernetes_namespace", "k8s.namespace.name")
		cluster := extractLabelFromAlert(alert, "cluster", "kubernetes_cluster", "k8s.cluster.name")
		if workload != "" && cluster != "" {
			titleKey := extractTitleKey(alert.Title)
			var wlResolvedID uuid.UUID
			wlResolvedErr := s.db.QueryRowContext(ctx, `
				SELECT id FROM incidents
				WHERE auto_created = TRUE
				  AND status IN ('resolved','closed')
				  AND resolved_at >= NOW() - INTERVAL '4 hours'
				  AND topology_path ILIKE '%' || $1 || '%'
				  AND topology_path ILIKE '%' || $2 || '%'
				  AND ($3 = '' OR topology_path ILIKE '%' || $3 || '%')
				  AND (source IS NULL OR source = '' OR source = $4)
				  AND (title ILIKE '%' || $5 || '%' OR description ILIKE '%' || $5 || '%')
				ORDER BY resolved_at DESC LIMIT 1
			`, cluster, workload, namespace, alert.Source, titleKey).Scan(&wlResolvedID)
			if wlResolvedErr == nil && wlResolvedID != uuid.Nil {
				if _, err := s.db.ExecContext(ctx, `
					UPDATE incidents
					SET status = 'open', resolved_at = NULL, updated_at = NOW(),
					    recurrence_count = COALESCE(recurrence_count, 0) + 1
					WHERE id = $1
				`, wlResolvedID); err != nil {
					log.Printf("[pipeline] reopen flapping workload incident=%s: %v", wlResolvedID, err)
				}
				s.attachAlertToIncident(ctx, alert, wlResolvedID, result,
					"Alert Merged (Flapping — Reopened)",
					fmt.Sprintf("Alert '%s' for workload '%s/%s' in cluster '%s' matched a recently-resolved incident — reopened (recurrence).", alert.Title, namespace, workload, cluster))
				log.Printf("Alert %s reopened resolved incident %s (flapping workload=%s/%s/%s)", alert.ID, wlResolvedID, cluster, namespace, workload)
				return wlResolvedID, nil
			}
		}
	}

	// Tight dedup: same title prefix + same source within 5 min 
	// Primary race-condition guard for burst storms (same alert, possibly different severities).
	// Severity intentionally excluded: Dynatrace sends the same event at critical+high
	// simultaneously; grouping them is correct even across severity levels.
	if s.db != nil {
		titleKey := extractTitleKey(alert.Title)
		burstCluster := extractLabelFromAlert(alert, "cluster", "k8s.cluster.name", "kubernetes_cluster")
		var raceDedupID uuid.UUID
		raceErr := s.db.QueryRowContext(ctx, `
			SELECT id FROM incidents
			WHERE auto_created = TRUE
			  AND status IN ('open','investigating')
			  AND created_at >= NOW() - INTERVAL '5 minutes'
			  AND title ILIKE '%' || $1 || '%'
			  AND source = $2
			  AND ($3 = '' OR topology_path IS NULL OR topology_path = '' OR topology_path ILIKE '%' || $3 || '%')
			ORDER BY created_at DESC LIMIT 1
		`, titleKey, alert.Source, burstCluster).Scan(&raceDedupID)
		if raceErr == nil && raceDedupID != uuid.Nil {
			s.mergeAlertIntoIncident(ctx, alert, raceDedupID, result, "Alert Merged (Same Alert Burst)",
				fmt.Sprintf("Alert '%s' arrived in same burst window — merged into existing incident.", alert.Title))
			log.Printf("Alert %s merged into burst-dedup incident %s (5min window)", alert.ID, raceDedupID)
			return raceDedupID, nil
		}
	}

	// Root-cause-entity dedup: same RCE node within the same cluster (2-hour window) 
	// The correlation engine identifies the root cause entity (e.g. a node) independently
	// for each alert. When two different alerts (e.g. CPU saturation + memory saturation)
	// share the same root cause node in the same cluster they are the same incident, even
	// though their titles differ and no single dedup key covers both.
	// This fires BEFORE the cluster+domain check so the stricter entity match wins.
	if s.db != nil && result != nil {
		// Extract root cause entity from the correlation result: check topology strategy
		// details first, then metadata (populated by root_cause_engine strategy).
		rceEntity := ""
		if result.StrategyResults != nil {
			if topoSR, ok := result.StrategyResults["topology"]; ok && topoSR != nil && topoSR.Details != nil {
				if v, ok := topoSR.Details["root_cause"].(string); ok {
					rceEntity = v
				}
			}
		}
		if rceEntity == "" && result.Metadata != nil {
			if v, ok := result.Metadata["root_cause_entity"].(string); ok {
				rceEntity = v
			}
		}
		// Also check the correlationID arg (set to decision.RootEntityID by createRootIncident)
		if rceEntity == "" && len(correlationID) > 0 && correlationID[0] != "" {
			rceEntity = correlationID[0]
		}

		if rceEntity != "" {
			cluster := extractLabelFromAlert(alert, "cluster", "k8s.cluster.name", "kubernetes_cluster")
			rceFamily := entityTypeFamily(alert)
			var rceIncidentID uuid.UUID
			rceErr := s.db.QueryRowContext(ctx, `
				SELECT i.id FROM incidents i
				WHERE i.auto_created = TRUE
				  AND i.status IN ('open','investigating')
				  AND i.created_at >= NOW() - INTERVAL '2 hours'
				  AND (
				    i.correlation_id = $1
				    OR EXISTS (
				      SELECT 1 FROM pipeline_correlation_results pcr
				      WHERE pcr.incident_id = i.id
				        AND (pcr.topo_root_entity = $1 OR pcr.root_cause_label = $1)
				    )
				  )
				  AND ($2 = '' OR i.topology_path ILIKE '%' || $2 || '%'
				                 OR i.description ILIKE '%' || $2 || '%'
				                 OR i.title ILIKE '%' || $2 || '%')
				ORDER BY i.created_at DESC LIMIT 1
			`, rceEntity, cluster).Scan(&rceIncidentID)
			if rceErr == nil && rceIncidentID != uuid.Nil {
				// Family guard: a node that is both a k8s_node and a VM host appears as the
				// same RCE entity string in alerts of both families. Without this guard a vm
				// alert from "example-cluster-worker-01" would merge into a k8s incident that
				// used the same node name as its root cause entity.
				familyOK := rceFamily == ""
				if !familyOK && s.db != nil {
					var famCount int
					switch rceFamily {
					case "k8s":
						_ = s.db.QueryRowContext(ctx,
							`SELECT COUNT(*) FROM alerts WHERE incident_id = $1 AND labels->>'entity_type' LIKE 'k8s_%'`,
							rceIncidentID).Scan(&famCount)
					case "vm":
						_ = s.db.QueryRowContext(ctx,
							`SELECT COUNT(*) FROM alerts WHERE incident_id = $1 AND labels->>'entity_type' = ANY(ARRAY['vm','host'])`,
							rceIncidentID).Scan(&famCount)
					case "netapp":
						_ = s.db.QueryRowContext(ctx,
							`SELECT COUNT(*) FROM alerts WHERE incident_id = $1 AND labels->>'entity_type' LIKE 'netapp_%'`,
							rceIncidentID).Scan(&famCount)
					}
					familyOK = famCount > 0
				}
				if familyOK {
					s.mergeAlertIntoIncident(ctx, alert, rceIncidentID, result,
						"Alert Merged (Same Root Cause Entity)",
						fmt.Sprintf("Alert '%s' shares root cause entity '%s' with existing incident.", alert.Title, rceEntity))
					log.Printf("Alert %s merged into existing incident %s (root_cause_entity=%s)", alert.ID, rceIncidentID, rceEntity)
					return rceIncidentID, nil
				}
			}
		}
	}

	// Entity-ID dedup: same Dynatrace/external entity within 2-hour window 
	// Dynatrace sends entity_id in metadata (e.g. "k8preview01-rno/gitshift-prod/deploy/visualdiff-wf").
	// Two alerts for the same entity are almost always the same root cause.
	// Cluster-scoped (D1): only match incidents whose alerts share the same cluster/UID
	// to prevent cross-cluster false correlations.
	if s.db != nil && alert.Metadata != nil {
		if entityID, _ := alert.Metadata["entity_id"].(string); entityID != "" {
			clusterScope := ""
			if alert.Labels != nil {
				if uid := alert.Labels["k8s.cluster.uid"]; uid != "" {
					clusterScope = uid
				} else if cn := alert.Labels["cluster"]; cn != "" {
					clusterScope = cn
				}
			}
			incomingFamily := entityTypeFamily(alert)
			var entityIncidentID uuid.UUID
			entityErr := s.db.QueryRowContext(ctx, `
				SELECT i.id FROM incidents i
				WHERE i.auto_created = TRUE
				  AND i.status IN ('open','investigating')
				  AND i.created_at >= NOW() - INTERVAL '2 hours'
				  AND ($3 != 'k8s' OR COALESCE(i.topology_path,'') NOT LIKE 'h:%')
				  AND EXISTS (
				    SELECT 1 FROM alerts a
				    WHERE a.incident_id = i.id
				      AND a.metadata->>'entity_id' = $1
				      AND ($2 = '' OR a.labels->>'cluster' = $2 OR a.labels->>'k8s.cluster.uid' = $2)
				  )
				ORDER BY i.created_at DESC LIMIT 1
			`, entityID, clusterScope, incomingFamily).Scan(&entityIncidentID)
			if entityErr == nil && entityIncidentID != uuid.Nil {
				s.mergeAlertIntoIncident(ctx, alert, entityIncidentID, result,
					"Alert Merged (Same Entity)",
					fmt.Sprintf("Alert '%s' matched same entity '%s' within 2h.", alert.Title, entityID))
				log.Printf("Alert %s merged into existing incident %s (entity_id=%s)", alert.ID, entityIncidentID, entityID)
				return entityIncidentID, nil
			}
		}
	}
	// K8s cross-type cascade: workload alert open node incident (same cluster UID) 
	// When a k8s_workload alert (e.g. "Not all pods ready") arrives for cluster UID X and
	// there is an OPEN incident for a k8s_node alert in the same cluster within the last
	// hour, the pod failure is almost certainly caused by node pressure.  Merge them.
	// Uses k8s.cluster.uid (not cluster name) to avoid cross-cluster false positives.
	if s.db != nil && alert.Labels != nil {
		entityType := strings.ToLower(alert.Labels["entity_type"])
		clusterUID := alert.Labels["k8s.cluster.uid"]
		workload := extractLabelFromAlert(alert, "k8s.workload.name", "workload", "deployment", "app")
		if clusterUID != "" && workload != "" && (entityType == "k8s_workload" || entityType == "cloud_application") {
			var crossTypeID uuid.UUID
			crossTypeErr := s.db.QueryRowContext(ctx, `
				SELECT i.id FROM incidents i
				JOIN alerts a ON a.incident_id = i.id
				WHERE i.auto_created = TRUE
				  AND i.status IN ('open','investigating')
				  AND i.created_at >= NOW() - INTERVAL '1 hour'
				  AND a.labels->>'k8s.cluster.uid' = $1
				  AND a.labels->>'entity_type' IN ('k8s_node','kubernetes_node')
				ORDER BY i.created_at ASC LIMIT 1
			`, clusterUID).Scan(&crossTypeID)
			if crossTypeErr == nil && crossTypeID != uuid.Nil {
				cluster := extractLabelFromAlert(alert, "cluster", "k8s.cluster.name", "kubernetes_cluster")
				s.mergeAlertIntoIncident(ctx, alert, crossTypeID, result,
					"Alert Merged (K8s Cross-Type Cascade)",
					fmt.Sprintf("Alert '%s' (workload '%s') merged — node pressure in cluster '%s' is causing pod failures.",
						alert.Title, workload, cluster))
				log.Printf("Alert %s merged into node incident %s (k8s cross-type: cluster_uid=%s workload=%s)",
					alert.ID, crossTypeID, clusterUID, workload)
				return crossTypeID, nil
			}
		}
	}

	// Same-cluster workload storm: k8s_workload alert open workload incident (same cluster) 
	// Multiple workload failures in the same cluster within a short window are likely a cluster-wide
	// event (node pressure, network issue, control plane problem).  Merge into the earliest open
	// workload incident for the cluster rather than creating hundreds of separate incidents.
	// Also handles NetApp storage entities grouped by netapp_cluster label.
	if s.db != nil && alert.Labels != nil {
		entityType := strings.ToLower(alert.Labels["entity_type"])
		clusterUID := alert.Labels["k8s.cluster.uid"]
		netappCluster := alert.Labels["netapp_cluster"]

		// K8s entity storm: group by k8s.cluster.uid
		if clusterUID != "" && (entityType == "k8s_workload" || entityType == "cloud_application" || entityType == "k8s_node" || entityType == "k8s_cluster") {
			stormRCE := alert.RootCauseEntity
			if stormRCE == "" && alert.Labels != nil {
				stormRCE = alert.Labels["root_cause_entity"]
			}
			var sameClusterID uuid.UUID
			var sameClusterErr error
			if entityType == "k8s_node" || entityType == "k8s_cluster" {
				// Node alerts are DISTINCT per node — only merge when the same root entity (node)
				// already owns an open incident. Cluster-wide grouping would incorrectly merge
				// independent node failures (e.g. z1-06 into z3-13 incident).
				if stormRCE != "" {
					sameClusterErr = s.db.QueryRowContext(ctx, `
						SELECT i.id FROM incidents i
						WHERE i.auto_created = TRUE
						  AND i.status IN ('open','investigating')
						  AND i.created_at >= NOW() - INTERVAL '1 hour'
						  AND i.topology_path NOT LIKE 'h:%'
						  AND i.correlation_id = $1
						ORDER BY i.created_at ASC LIMIT 1
					`, stormRCE).Scan(&sameClusterID)
				}
			} else {
				// Workload alerts: cluster-wide grouping (many pod failures 1 cluster incident).
				sameClusterErr = s.db.QueryRowContext(ctx, `
					SELECT i.id FROM incidents i
					JOIN alerts a ON a.incident_id = i.id
					WHERE i.auto_created = TRUE
					  AND i.status IN ('open','investigating')
					  AND i.created_at >= NOW() - INTERVAL '1 hour'
					  AND a.labels->>'k8s.cluster.uid' = $1
					  AND a.labels->>'entity_type' = $2
					  AND i.topology_path NOT LIKE 'h:%'
					ORDER BY i.created_at ASC LIMIT 1
				`, clusterUID, entityType).Scan(&sameClusterID)
			}
			if sameClusterErr == nil && sameClusterID != uuid.Nil {
				cluster := extractLabelFromAlert(alert, "cluster", "k8s.cluster.name")
				s.mergeAlertIntoIncident(ctx, alert, sameClusterID, result,
					"Alert Merged (Same-Cluster Storm)",
					fmt.Sprintf("Alert '%s' merged into cluster '%s' incident — multiple %s alerts indicate cluster-wide issue.",
						alert.Title, cluster, entityType))
				log.Printf("Alert %s merged into cluster incident %s (same-cluster storm: cluster_uid=%s type=%s)",
					alert.ID, sameClusterID, clusterUID, entityType)
				return sameClusterID, nil
			}
		}

		// NetApp storage storm: group by netapp_cluster + entity_type within 2 hours.
		// Covers: netapp_aggregate, netapp_cluster, netapp_node, netapp_svm alerts
		// that fire simultaneously across multiple entities on the same storage cluster.
		// 2-hour window matches disk rebuild / aggregate rebalancing event durations.
		if netappCluster != "" && strings.HasPrefix(entityType, "netapp_") {
			var netappStormID uuid.UUID
			netappStormErr := s.db.QueryRowContext(ctx, `
				SELECT i.id FROM incidents i
				JOIN alerts a ON a.incident_id = i.id
				WHERE i.auto_created = TRUE
				  AND i.status IN ('open','investigating')
				  AND i.created_at >= NOW() - INTERVAL '2 hours'
				  AND a.labels->>'netapp_cluster' = $1
				  AND a.labels->>'entity_type' LIKE 'netapp_%'
				ORDER BY i.created_at ASC LIMIT 1
			`, netappCluster).Scan(&netappStormID)
			if netappStormErr == nil && netappStormID != uuid.Nil {
				s.mergeAlertIntoIncident(ctx, alert, netappStormID, result,
					"Alert Merged (NetApp Storage Storm)",
					fmt.Sprintf("Alert '%s' merged — multiple NetApp alerts on cluster '%s' indicate storage-wide issue.",
						alert.Title, netappCluster))
				log.Printf("Alert %s merged into NetApp incident %s (cluster=%s type=%s)",
					alert.ID, netappStormID, netappCluster, entityType)
				return netappStormID, nil
			}
		}
	}

	// Cluster+domain dedup: same cluster + problem domain (1-hour window) 
	// Uses k8s.cluster.uid (exact JSONB match) when available — prevents cross-cluster
	// false positives from substring matching on cluster names that share a common suffix
	// (e.g. mps-sandbox-rno vs mps-nonprod-rno both containing "rno").
	// Entity family isolation (k8s / vm / netapp) ensures vm alerts never land in k8s
	// incidents and vice versa, eliminating the cross-infra over-grouping seen in #2597.
	if s.db != nil {
		cluster := extractLabelFromAlert(alert, "cluster", "k8s.cluster.name", "kubernetes_cluster")
		clusterUID := ""
		if alert.Labels != nil {
			clusterUID = alert.Labels["k8s.cluster.uid"]
		}
		problemDomain := extractProblemDomainFromTitle(alert.Title + " " + alert.Description)
		family := entityTypeFamily(alert)
		rceCluster := alert.RootCauseEntity
		if rceCluster == "" && alert.Labels != nil {
			rceCluster = alert.Labels["root_cause_entity"]
		}

		if problemDomain != "" && (clusterUID != "" || cluster != "") {
			var clusterIncidentID uuid.UUID
			var clusterErr error

			if clusterUID != "" {
				// Preferred: exact cluster UID via JSONB — no substring ambiguity.
				clusterErr = s.db.QueryRowContext(ctx, `
					SELECT i.id FROM incidents i
					WHERE i.auto_created = TRUE
					  AND i.status IN ('open','investigating')
					  AND i.created_at >= NOW() - INTERVAL '1 hour'
					  AND EXISTS (
					    SELECT 1 FROM alerts a
					    WHERE a.incident_id = i.id
					      AND a.labels->>'k8s.cluster.uid' = $1
					  )
					  AND (
					    i.title ILIKE '%' || $2 || '%'
					    OR i.description ILIKE '%' || $2 || '%'
					  )
					  AND (COALESCE(i.correlation_id,'') = '' OR i.correlation_id = $3)
					ORDER BY i.created_at DESC LIMIT 1
				`, clusterUID, problemDomain, rceCluster).Scan(&clusterIncidentID)
			} else if cluster != "" {
				// Fallback: cluster name text match + entity family guard.
				// Family guard prevents vm alerts from landing in k8s incidents.
				var familyFilter string
				switch family {
				case "k8s":
					familyFilter = "k8s_%"
				case "vm":
					familyFilter = "vm"
				case "netapp":
					familyFilter = "netapp_%"
				}

				if familyFilter != "" {
					clusterErr = s.db.QueryRowContext(ctx, `
						SELECT i.id FROM incidents i
						WHERE i.auto_created = TRUE
						  AND i.status IN ('open','investigating')
						  AND i.created_at >= NOW() - INTERVAL '1 hour'
						  AND (
						    i.topology_path ILIKE '%' || $1 || '%'
						    OR i.title ILIKE '%' || $1 || '%'
						  )
						  AND (
						    i.title ILIKE '%' || $2 || '%'
						    OR i.description ILIKE '%' || $2 || '%'
						  )
						  AND EXISTS (
						    SELECT 1 FROM alerts a
						    WHERE a.incident_id = i.id
						      AND a.labels->>'entity_type' LIKE $4
						  )
						  AND (COALESCE(i.correlation_id,'') = '' OR i.correlation_id = $3)
						ORDER BY i.created_at DESC LIMIT 1
					`, cluster, problemDomain, rceCluster, familyFilter).Scan(&clusterIncidentID)
				} else {
					// Unknown family — still match but within the tighter 1-hour window.
					clusterErr = s.db.QueryRowContext(ctx, `
						SELECT i.id FROM incidents i
						WHERE i.auto_created = TRUE
						  AND i.status IN ('open','investigating')
						  AND i.created_at >= NOW() - INTERVAL '1 hour'
						  AND (
						    i.topology_path ILIKE '%' || $1 || '%'
						    OR i.title ILIKE '%' || $1 || '%'
						  )
						  AND (
						    i.title ILIKE '%' || $2 || '%'
						    OR i.description ILIKE '%' || $2 || '%'
						  )
						  AND (COALESCE(i.correlation_id,'') = '' OR i.correlation_id = $3)
						ORDER BY i.created_at DESC LIMIT 1
					`, cluster, problemDomain, rceCluster).Scan(&clusterIncidentID)
				}
			}

			if clusterErr == nil && clusterIncidentID != uuid.Nil {
				s.mergeAlertIntoIncident(ctx, alert, clusterIncidentID, result,
					"Alert Merged",
					fmt.Sprintf("Alert '%s' (severity: %s) merged — same cluster '%s', same problem domain '%s'.",
						alert.Title, alert.Severity, cluster, problemDomain))
				log.Printf("Alert %s merged into existing incident %s (cluster=%s uid=%s domain=%s, 1hr window)",
					alert.ID, clusterIncidentID, cluster, clusterUID, problemDomain)
				return clusterIncidentID, nil
			}
		}
	}

	// Cluster cascade dedup: same cluster + 30-min window (any source) 
	// Catches node-down storm: host unavailable node not ready pods not ready are
	// DIFFERENT domains / sources but ONE root cause.
	// Skip for k8s alerts that carry a cluster UID — those are already handled by the
	// precise UID-based storm checks above, and running a text-match cascade on top
	// risks cross-cluster false merges between clusters sharing a name suffix.
	// Entity family guard prevents vm cascades from landing in k8s incidents.
	if s.db != nil {
		cascadeClusterUID := ""
		if alert.Labels != nil {
			cascadeClusterUID = alert.Labels["k8s.cluster.uid"]
		}
		var cluster string
		if cascadeClusterUID == "" {
			cluster = extractLabelFromAlert(alert, "cluster", "kubernetes_cluster", "k8s.cluster.name")
			if cluster == "" {
				cluster = extractClusterFromText(alert.Title + " " + alert.Description)
			}
		}
		cascadeRCE := alert.RootCauseEntity
		if cascadeRCE == "" && alert.Labels != nil {
			cascadeRCE = alert.Labels["root_cause_entity"]
		}
		cascadeFamily := entityTypeFamily(alert)

		if cluster != "" {
			var cascadeID uuid.UUID
			cascadeErr := s.db.QueryRowContext(ctx, `
				SELECT i.id FROM incidents i
				WHERE i.auto_created = TRUE
				  AND i.status IN ('open','investigating')
				  AND i.created_at >= NOW() - INTERVAL '30 minutes'
				  AND (
				    i.topology_path ILIKE '%' || $1 || '%'
				    OR i.title ILIKE '%' || $1 || '%'
				    OR i.description ILIKE '%' || $1 || '%'
				  )
				  AND (COALESCE(i.correlation_id, '') = '' OR i.correlation_id = $2)
				ORDER BY i.created_at ASC
				LIMIT 1
			`, cluster, cascadeRCE).Scan(&cascadeID)
			if cascadeErr == nil && cascadeID != uuid.Nil {
				// Family guard: only merge when the matched incident contains at least one
				// alert from the same infra family. A vm cascade must not land in a k8s
				// incident opened by a different physical cluster in the same name prefix.
				familyOK := cascadeFamily == ""
				if !familyOK {
					var familyCount int
					switch cascadeFamily {
					case "k8s":
						_ = s.db.QueryRowContext(ctx,
							`SELECT COUNT(*) FROM alerts WHERE incident_id = $1 AND labels->>'entity_type' LIKE 'k8s_%'`,
							cascadeID).Scan(&familyCount)
					case "vm":
						_ = s.db.QueryRowContext(ctx,
							`SELECT COUNT(*) FROM alerts WHERE incident_id = $1 AND labels->>'entity_type' = ANY(ARRAY['vm','host'])`,
							cascadeID).Scan(&familyCount)
					case "netapp":
						_ = s.db.QueryRowContext(ctx,
							`SELECT COUNT(*) FROM alerts WHERE incident_id = $1 AND labels->>'entity_type' LIKE 'netapp_%'`,
							cascadeID).Scan(&familyCount)
					}
					familyOK = familyCount > 0
				}
				if familyOK {
					s.mergeAlertIntoIncident(ctx, alert, cascadeID, result,
						"Alert Merged (Cluster Cascade)",
						fmt.Sprintf("Alert '%s' (severity: %s) — infrastructure cascade in cluster '%s'. Node failure causes host/pod/ingress alerts across different sources.",
							alert.Title, alert.Severity, cluster))
					log.Printf("Alert %s merged into incident %s (cascade: cluster=%s, 30min window)", alert.ID, cascadeID, cluster)
					return cascadeID, nil
				}
			}
		}
	}

	// Deduplication: causal infrastructure hierarchy (nodepod, BMnode, BMpod) 
	// If this alert carries a node or host label, find any open incident from the same
	// cluster whose topology_path already contains that node/host — within 2 hours.
	// This is what lets "Pod not running on worker-1" merge into "Node worker-1 not ready".
	if s.db != nil {
		nodeLabel := extractLabelFromAlert(alert, "node", "kubernetes_node", "k8s.node.name", "nodename")
		hostLabel := extractLabelFromAlert(alert, "host.name", "hostname", "host", "instance", "dt.entity.host")
		cluster := extractLabelFromAlert(alert, "cluster", "kubernetes_cluster", "k8s.cluster.name")
		infraKey := nodeLabel
		if infraKey == "" {
			infraKey = hostLabel
		}
		if infraKey != "" && cluster != "" {
			var causalID uuid.UUID
			causalErr := s.db.QueryRowContext(ctx, `
				SELECT id FROM incidents
				WHERE auto_created = TRUE
				  AND status IN ('open','investigating')
				  AND created_at >= NOW() - INTERVAL '2 hours'
				  AND topology_path ILIKE '%' || $1 || '%'
				  AND topology_path ILIKE '%' || $2 || '%'
				ORDER BY created_at ASC
				LIMIT 1
			`, cluster, infraKey).Scan(&causalID)
			if causalErr == nil && causalID != uuid.Nil {
				s.mergeAlertIntoIncident(ctx, alert, causalID, result,
					"Alert Merged (Infrastructure Cascade)",
					fmt.Sprintf("Alert '%s' (severity: %s) merged — infrastructure cascade on node '%s' in cluster '%s'.",
						alert.Title, alert.Severity, infraKey, cluster))
				log.Printf("Alert %s merged into incident %s (causal infra cascade: node=%s cluster=%s)", alert.ID, causalID, infraKey, cluster)
				return causalID, nil
			}
		}
	}

	// Topology-cache lookup: find the K8s node for a rescheduled pod 
	// When a node fails, K8s reschedules pods to other nodes within seconds.
	// Subsequent pod/workload alerts carry the NEW node in their labels, not the
	// failed one. Use the last DB topology snapshot to find what node the pod/workload
	// WAS on, then merge into the node-failure incident from the same cluster.
	if s.db != nil {
		workload := extractLabelFromAlert(alert, "deployment", "workload", "app", "k8s.workload.name", "k8s.deployment.name")
		namespace := extractLabelFromAlert(alert, "namespace", "kubernetes_namespace", "k8s.namespace.name")
		cluster := extractLabelFromAlert(alert, "cluster", "kubernetes_cluster", "k8s.cluster.name")
		if cluster == "" {
			cluster = extractClusterFromText(alert.Title + " " + alert.Description)
		}
		if workload != "" && cluster != "" {
			// Find the most recent snapshot for this region/cluster and extract the node
			// that hosts this workload/namespace, then check open incidents for that node.
			var historicNode string
			_ = s.db.QueryRowContext(ctx, `
				SELECT node_name FROM (
				    SELECT rel->>'source_label' AS node_name
				    FROM topology_snapshots ts,
				         jsonb_array_elements(ts.snapshot_data->'relationships') rel
				    WHERE rel->>'type' = 'runs_on'
				      AND (rel->>'target_label' ILIKE '%' || $1 || '%'
				           OR rel->>'target_label' ILIKE '%' || $2 || '%')
				      AND ts.created_at >= NOW() - INTERVAL '10 minutes'
				    UNION ALL
				    SELECT layer_node->>'name' AS node_name
				    FROM topology_snapshots ts,
				         jsonb_array_elements(ts.snapshot_data->'layers'->'kubernetes'->'nodes') layer_node
				    WHERE layer_node->>'workloads' ILIKE '%' || $1 || '%'
				      AND ts.created_at >= NOW() - INTERVAL '10 minutes'
				    LIMIT 1
				) q LIMIT 1
			`, workload, namespace).Scan(&historicNode)

			if historicNode != "" {
				var topoMatchID uuid.UUID
				_ = s.db.QueryRowContext(ctx, `
					SELECT id FROM incidents
					WHERE auto_created = TRUE
					  AND status IN ('open','investigating')
					  AND created_at >= NOW() - INTERVAL '1 hour'
					  AND (topology_path ILIKE '%' || $1 || '%'
					       OR topology_path ILIKE '%' || $2 || '%')
					ORDER BY created_at ASC LIMIT 1
				`, historicNode, cluster).Scan(&topoMatchID)
				if topoMatchID != uuid.Nil {
					s.mergeAlertIntoIncident(ctx, alert, topoMatchID, result,
						"Alert Merged (Topology Cache)",
						fmt.Sprintf("Alert '%s' merged via topology cache: workload '%s' was on node '%s' before rescheduling.",
							alert.Title, workload, historicNode))
					log.Printf("Alert %s merged into incident %s (topo-cache: workload=%s was-on-node=%s)", alert.ID, topoMatchID, workload, historicNode)
					return topoMatchID, nil
				}
			}
		}
	}

	// Deduplication: check for existing open incident with same fingerprint 
	if s.db != nil && alert.Fingerprint != "" {
		var existingID uuid.UUID
		err := s.db.QueryRowContext(ctx, `
			SELECT id FROM incidents
			WHERE auto_created = TRUE
			  AND status IN ('open','investigating')
			  AND (
			    -- same fingerprint stored in correlation_id
			    correlation_id = $1
			    OR
			    -- same title+severity+source created within last 30 min
			    (title LIKE $2 AND severity = $3 AND source = $4
			     AND created_at >= NOW() - INTERVAL '30 minutes')
			  )
			ORDER BY created_at DESC
			LIMIT 1
		`, alert.Fingerprint, "%"+alert.Title+"%", alert.Severity, alert.Source).Scan(&existingID)

		if err == nil && existingID != uuid.Nil {
			s.mergeAlertIntoIncident(ctx, alert, existingID, result,
				"Alert Merged (Duplicate Pattern)",
				fmt.Sprintf("Alert '%s' matched an existing incident by fingerprint/title within 30 min.", alert.Title))
			log.Printf("Alert %s merged into existing incident %s (same pattern, within 30min)", alert.ID, existingID)
			return existingID, nil
		}
	}

	// Deduplication: topology-based merge within 2-hour window 
	// Extract topology labels early so we can use them for both this check and
	// the incident construction below.
	var earlyMatchedLabel, earlyRootCauseLabel string
	if s.db != nil && result.StrategyResults != nil {
		if topoSR, ok := result.StrategyResults["topology"]; ok && topoSR.Details != nil {
			if l, ok := topoSR.Details["matched_label"].(string); ok {
				earlyMatchedLabel = l
			}
			if rc, ok := topoSR.Details["root_cause"].(string); ok {
				earlyRootCauseLabel = rc
			}
		}
		if earlyMatchedLabel != "" {
			// Scope the topology-path match to the same cluster when a UID is available.
			// Without this, two alerts from different clusters can share a topology label
			// (e.g. same node name) and get merged into the same incident.
			topoClusterUID := ""
			topoClusterName := ""
			if alert.Labels != nil {
				topoClusterUID = alert.Labels["k8s.cluster.uid"]
				topoClusterName = extractLabelFromAlert(alert, "cluster", "k8s.cluster.name", "kubernetes_cluster")
			}
			var topoExistingID uuid.UUID
			topoErr := s.db.QueryRowContext(ctx, `
				SELECT i.id FROM incidents i
				WHERE i.auto_created = TRUE
				  AND i.status IN ('open','investigating')
				  AND i.created_at >= NOW() - INTERVAL '2 hours'
				  AND (
				    i.topology_path LIKE '%' || $1 || '%'
				    OR i.topology_path LIKE '%' || $2 || '%'
				  )
				  AND (
				    -- k8s with UID: exact UID match via alert labels JSONB
				    ($3 != '' AND EXISTS (
				        SELECT 1 FROM alerts a
				        WHERE a.incident_id = i.id
				          AND a.labels->>'k8s.cluster.uid' = $3
				    ))
				    -- cluster name fallback
				    OR ($3 = '' AND $4 != '' AND (
				        i.topology_path ILIKE '%' || $4 || '%'
				        OR EXISTS (
				            SELECT 1 FROM alerts a
				            WHERE a.incident_id = i.id
				              AND (a.labels->>'cluster' = $4 OR a.labels->>'k8s.cluster.name' = $4)
				        )
				    ))
				    -- no cluster context: match without filter (non-k8s alerts)
				    OR ($3 = '' AND $4 = '')
				  )
				ORDER BY i.created_at DESC
				LIMIT 1
			`, earlyMatchedLabel, earlyRootCauseLabel, topoClusterUID, topoClusterName).Scan(&topoExistingID)

			if topoErr == nil && topoExistingID != uuid.Nil {
				s.mergeAlertIntoIncident(ctx, alert, topoExistingID, result,
					"Alert Merged (Topology Match)",
					fmt.Sprintf("Alert '%s' matched topology path within 2-hour window.", alert.Title))
				log.Printf("Alert %s merged into existing incident %s (topology match within 2hr)", alert.ID, topoExistingID)
				return topoExistingID, nil
			}
		}
	}

	// Extract topology context 
	var topoCtx *correlation.TopoIncidentContext
	strategyScores := map[string]float64{}

	if result.StrategyResults != nil {
		for strat, sr := range result.StrategyResults {
			strategyScores[strat] = sr.Score
		}
		if topoSR, ok := result.StrategyResults["topology"]; ok && topoSR.Details != nil {
			topo := &correlation.TopoIncidentContext{}
			if id, ok := topoSR.Details["matched_node_id"].(string); ok {
				topo.MatchedNodeID = id
			}
			if t, ok := topoSR.Details["matched_node_type"].(string); ok {
				topo.MatchedNodeType = t
			}
			if l, ok := topoSR.Details["matched_label"].(string); ok {
				topo.MatchedLabel = l
			}
			if rc, ok := topoSR.Details["root_cause"].(string); ok {
				topo.RootCauseLabel = rc
			}
			if rct, ok := topoSR.Details["root_cause_type"].(string); ok {
				topo.RootCauseType = rct
			}
			if r, ok := topoSR.Details["reasoning"].(string); ok {
				topo.TopologyPath = r
			}
			if topo.MatchedNodeID != "" || topo.MatchedLabel != "" {
				topoCtx = topo
			}
		}
	}

	title := correlation.BuildIncidentTitle(alert.Title, topoCtx)
	description := correlation.BuildIncidentDescription(alert.Title, alert.Severity, strategyScores, topoCtx)

	// LLM root-cause enrichment (best-effort)
	var matchedLabel, matchedNodeType, rootCauseLabel, topologyPath string
	if topoCtx != nil {
		matchedLabel = topoCtx.MatchedLabel
		matchedNodeType = topoCtx.MatchedNodeType
		rootCauseLabel = topoCtx.RootCauseLabel
		topologyPath = topoCtx.TopologyPath
	}

	// Ensure topology_path always contains the cluster/node/namespace/workload so the
	// 6-hour and causal deduplication queries can match subsequent alerts from the same infra.
	// Critically: node label must be in the path so pod alerts can match a node incident.
	if topologyPath == "" {
		cluster := extractLabelFromAlert(alert, "cluster", "k8s.cluster.name", "kubernetes_cluster")
		// Fall back to extracting cluster name from alert title text
		if cluster == "" {
			cluster = extractClusterFromText(alert.Title + " " + alert.Description)
		}
		namespace := extractLabelFromAlert(alert, "namespace", "k8s.namespace.name", "kubernetes_namespace")
		node := extractLabelFromAlert(alert, "node", "k8s.node.name", "kubernetes_node", "nodename")
		workload := extractLabelFromAlert(alert, "workload", "k8s.workload.name", "deployment", "app")
		workloadKind := extractLabelFromAlert(alert, "k8s.workload.kind")
		host := extractLabelFromAlert(alert, "host.name", "hostname", "host", "dt.entity.host")
		if cluster != "" {
			parts := []string{cluster}
			if node != "" {
				parts = append(parts, node)
			}
			if namespace != "" {
				parts = append(parts, namespace)
			}
			if workload != "" {
				if workloadKind != "" {
					parts = append(parts, workloadKind+"/"+workload)
				} else {
					parts = append(parts, workload)
				}
			}
			topologyPath = strings.Join(parts, "/")
		} else if host != "" {
			topologyPath = "h:" + host
		}
	}
	aiRCA := s.llmEnricher.GenerateRCA(ctx,
		alert.Title, alert.Severity,
		matchedLabel, matchedNodeType, rootCauseLabel, topologyPath,
		strategyScores,
	)

	// Store RCA in metadata so saveCorrelationResult can pick it up.
	if result.Metadata == nil {
		result.Metadata = map[string]interface{}{}
	}
	result.Metadata["ai_root_cause"] = aiRCA

	// Extract blast radius node labels from topology strategy details.
	var blastRadiusNodes []string
	if result.StrategyResults != nil {
		if topoSR, ok := result.StrategyResults["topology"]; ok && topoSR.Details != nil {
			if nodes, ok := topoSR.Details["blast_radius_nodes"].([]string); ok {
				blastRadiusNodes = nodes
			}
		}
	}
	// Fallback: if topology correlator found nothing, derive blast radius from alert labels.
	if len(blastRadiusNodes) == 0 && alert.Labels != nil {
		seen := map[string]bool{}
		for _, key := range []string{"k8s.workload.name", "k8s.namespace.name", "k8s.cluster.name", "host.name", "k8s.node.name"} {
			if v := alert.Labels[key]; v != "" && !seen[v] {
				blastRadiusNodes = append(blastRadiusNodes, v)
				seen[v] = true
			}
		}
	}

	// Determine correlation method (dominant strategy name)
	correlationMethod := result.DominantStrategy
	if correlationMethod == "" {
		correlationMethod = "multi_strategy"
	}

	rcaStatus := "none"
	if s.rcaURL != "" {
		rcaStatus = "queued"
	}

	corrID := ""
	if len(correlationID) > 0 {
		corrID = correlationID[0]
	}

	incident := &incidents.Incident{
		ID:                    uuid.New(),
		Title:                 title,
		Description:           description,
		Severity:              alert.Severity,
		Status:                "open",
		Priority:              severityToPriority(alert.Severity),
		AlertIDs:              []uuid.UUID{alert.ID},
		AIRootCause:           aiRCA,
		BlastRadius:           blastRadiusNodes,
		TopologyPath:          topologyPath,
		CorrelationMethod:     correlationMethod,
		DominantStrategy:      result.DominantStrategy,
		RCAStatus:             rcaStatus,
		CorrelationConfidence: result.FinalScore,
		CorrelationID:         corrID,
	}

	if err := s.incidentSvc.InternalCreateIncident(ctx, incident); err != nil {
		log.Printf("Failed to create incident for alert %s: %v", alert.ID, err)
		return uuid.Nil, err
	}
	// Backfill incident_id on the root alert so it's queryable from the alerts table.
	if s.db != nil {
		if _, err := s.db.ExecContext(ctx, `UPDATE alerts SET incident_id = $1 WHERE id = $2`, incident.ID, alert.ID); err != nil {
			log.Printf("[pipeline] backfill incident_id alert=%s incident=%s: %v", alert.ID, incident.ID, err)
		}
		// Race guard: RESOLVED may have arrived while we were creating the incident.
		// Now that incident_id is persisted the open-count query can see this alert.
		var alertStatus string
		_ = s.db.QueryRowContext(ctx, `SELECT status FROM alerts WHERE id = $1`, alert.ID).Scan(&alertStatus)
		if alertStatus == "resolved" || alertStatus == "closed" {
			log.Printf("[pipeline] post-create race: alert %s already %s — closing incident %s", alert.ID, alertStatus, incident.ID)
			s.HandleAlertResolved(ctx, alert.ID, incident.ID)
		}
	}
	log.Printf("Created incident %s '%s' for alert %s (score=%.3f dominant=%s)",
		incident.ID, incident.Title, alert.ID, result.FinalScore, result.DominantStrategy)
	metrics.IncidentsCreated.Inc()
	// Fire-and-forget notification — never blocks the pipeline.
	if s.notificationSvc != nil {
		go func() {
			nCtx, cancel := context.WithTimeout(s.svcCtx, 15*time.Second)
			defer cancel()
			if err := s.notificationSvc.SendIncidentNotification(
				nCtx, incident.ID, incident.IncidentNumber,
				incident.Severity, incident.Title, incident.Description,
			); err != nil {
				log.Printf("[pipeline] notification for incident %s failed: %v", incident.ID, err)
			}
		}()
	}
	// Fire-and-forget OIE trigger — publishes to alerthub.incidents so OIE
	// auto-starts an investigation for every new incident.
	// Severity filtering is handled by the OIE consumer's autoInvestigateSeverities
	// config — do NOT add a second severity gate here; it defeats OIE config.
	if s.oiePublisher != nil {
		go func() {
			evt := map[string]interface{}{
				"type":            "incident.created",
				"incident_id":     incident.ID,
				"incident_number": incident.IncidentNumber,
				"severity":        incident.Severity,
				"started_at":      incident.CreatedAt,
				// Pass entity context so OIE can route to the correct K8s/CloudStack fetcher
				// without needing a separate EIRS resolution call.
				"topology_path":  incident.TopologyPath,
				"correlation_id": incident.CorrelationID,
			}
			if pubErr := s.oiePublisher.PublishToTopic("alerthub.incidents", evt); pubErr != nil {
				log.Printf("[pipeline] OIE publish for incident %s failed: %v", incident.ID, pubErr)
			}
		}()
	}
	// Request parallel KubeSense investigation — runs independently of OIE.
	// KubeSense will use its own in-cluster K8s informers for real-time evidence
	// and report back on kubesense.investigations.results.
	if s.kubeSensePublisher != nil && (incident.Severity == "critical" || incident.Severity == "high") {
		go func() {
			cluster := ""
			namespace := ""
			resourceKind := ""
			resourceName := ""
			if alert.Labels != nil {
				cluster = alert.Labels["cluster"]
				if cluster == "" {
					cluster = alert.Labels["kubernetes_cluster"]
				}
				namespace = alert.Labels["namespace"]
				resourceKind = alert.Labels["entity_type"]
				resourceName = alert.Labels["app"]
				if resourceName == "" {
					resourceName = alert.Labels["service"]
				}
			}
			if cluster == "" {
				cluster = "default"
			}
			if err := s.kubeSensePublisher.RequestInvestigation(
				s.svcCtx,
				incident.ID.String(), cluster, namespace, resourceKind, resourceName,
				incident.Severity, incident.Title,
			); err != nil {
				log.Printf("[pipeline] KubeSense request for incident %s failed: %v", incident.ID, err)
			}
		}()
	}
	return incident.ID, nil
}

// mergeIncident records the merge and returns the target incident ID.
func (s *AlertPipelineService) mergeIncident(ctx context.Context, alert *models.Alert, result *correlation.FinalCorrelationResult) uuid.UUID {
	// C5: guard against nil BestMatch or zero UUID — both cause a no-op UPDATE.
	if result.BestMatch == nil || result.BestMatch.ID == uuid.Nil {
		id, err := s.createIncident(ctx, alert, result)
		if err != nil {
			log.Printf("mergeIncident: fallback createIncident failed for alert %s: %v", alert.ID, err)
		}
		return id
	}
	s.mergeAlertIntoIncident(ctx, alert, result.BestMatch.ID, result,
		"Alert Merged",
		fmt.Sprintf("Alert '%s' (severity: %s, score: %.0f%%) correlated into this incident.",
			alert.Title, alert.Severity, result.FinalScore*100))
	log.Printf("Alert %s merged into incident %s (score=%.3f)", alert.ID, result.BestMatch.ID, result.FinalScore)

	// Fire OIE trigger for merged incidents — alert may have escalated severity
	// or added new topology context. The OIE consumer deduplicates via the
	// idempotency key (incident_id+idempotency_key) so re-triggering is safe.
	if s.oiePublisher != nil {
		incidentID := result.BestMatch.ID
		go func() {
			evt := map[string]interface{}{
				"type":           "incident.escalated",
				"incident_id":    incidentID,
				"severity":       alert.Severity,
				"topology_path":  alert.Labels["topology_path"],
				"correlation_id": alert.Labels["correlation_id"],
			}
			if pubErr := s.oiePublisher.PublishToTopic("alerthub.incidents", evt); pubErr != nil {
				log.Printf("[pipeline] OIE escalate publish for incident %s failed: %v", incidentID, pubErr)
			}
		}()
	}

	return result.BestMatch.ID
}

// HandleAlertResolved checks whether all correlated alerts for an incident are resolved;
// if so, it auto-resolves the incident.
func (s *AlertPipelineService) HandleAlertResolved(ctx context.Context, alertID uuid.UUID, incidentID uuid.UUID) {
	if s.db == nil {
		return
	}

	// Add per-alert resolved event to the timeline.
	if s.incidentSvc != nil {
		var alertTitle, alertSeverity string
		_ = s.db.QueryRowContext(ctx, `SELECT title, severity FROM alerts WHERE id = $1`, alertID).
			Scan(&alertTitle, &alertSeverity)
		if alertTitle != "" {
			s.incidentSvc.AddTimelineEvent(ctx, incidentID, uuid.Nil, "alert_resolved",
				"Alert Resolved",
				fmt.Sprintf("Alert '%s' (severity: %s) has been resolved.", alertTitle, alertSeverity),
				map[string]interface{}{"alert_id": alertID.String(), "severity": alertSeverity},
			)
		}
	}

	// Count open alerts still linked to the incident using the incident_id FK
	// (authoritative — set by attachAlertToIncident / mergeAlertIntoIncident).
	// Fall back to alert_ids JSONB for any legacy rows where FK was never written.
	// No time-window filter: a fixed ±30 s window caused false auto-closes when
	// correlated alerts were created more than 30 s apart (G3).
	var openCount int
	s.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT a.id) FROM alerts a
		WHERE a.status NOT IN ('resolved', 'closed')
		  AND (
		    a.incident_id = $1
		    OR a.id = ANY(
		        SELECT jsonb_array_elements_text(COALESCE(alert_ids, '[]'::jsonb))::uuid
		        FROM incidents WHERE id = $1
		    )
		  )
	`, incidentID).Scan(&openCount)

	if openCount == 0 {
		if _, err := s.db.ExecContext(ctx, `
			UPDATE incidents
			SET status = 'resolved', resolved_at = NOW(), updated_at = NOW(),
			    resolution_notes = 'Auto-resolved: all correlated alerts resolved'
			WHERE id = $1 AND status IN ('open', 'investigating', 'acknowledged')
		`, incidentID); err != nil {
			log.Printf("[pipeline] auto-resolve incident=%s: %v", incidentID, err)
		}
		log.Printf("Incident %s auto-resolved (all correlated alerts resolved)", incidentID)
		metrics.IncidentsAutoResolved.WithLabelValues("all_alerts_resolved").Inc()
		if s.incidentSvc != nil {
			s.incidentSvc.AddTimelineEvent(ctx, incidentID, uuid.Nil, "auto_resolved",
				"Incident Auto-Resolved",
				"All correlated alerts have been resolved. Incident auto-closed.", nil)
		}
		// Auto-generate postmortem for resolved incidents (Aurora pattern).
		if s.postmortemTrigger != nil {
			go func(id uuid.UUID) {
				if err := s.postmortemTrigger.GenerateForIncident(context.Background(), id); err != nil {
					log.Printf("[pipeline] postmortem generation for incident %s failed: %v", id, err)
				}
			}(incidentID)
		}
	} else {
		log.Printf("Incident %s: alert %s resolved, %d open alerts remain", incidentID, alertID, openCount)
	}
}

// handleResolvedAlert is called when an incoming alert already has status=resolved.
// It finds the linked incident (by alert ID or title match) and propagates the resolution.
func (s *AlertPipelineService) handleResolvedAlert(ctx context.Context, alert *models.Alert) {
	if s.db == nil {
		return
	}

		// Mark the alert resolved in DB.
		if _, err := s.db.ExecContext(ctx, `UPDATE alerts SET status = 'resolved', resolved_at = NOW() WHERE id = $1`, alert.ID); err != nil {
			log.Printf("[pipeline] mark alert resolved alert=%s: %v", alert.ID, err)
		}

	// Find the incident this alert belongs to.
	var incidentID uuid.UUID
	err := s.db.QueryRowContext(ctx,
		`SELECT incident_id FROM alerts WHERE id = $1 AND incident_id IS NOT NULL`, alert.ID,
	).Scan(&incidentID)
	if err != nil || incidentID == uuid.Nil {
		// Secondary lookup: find the incident linked to any other alert with the same
		// source_id. Handles the race where RESOLVED arrives before the pipeline finishes
		// linking the OPEN event's alert record (different UUID, same problem).
		if alert.Source != "" && alert.SourceID != "" {
			_ = s.db.QueryRowContext(ctx, `
				SELECT incident_id FROM alerts
				WHERE source = $1 AND source_id = $2 AND incident_id IS NOT NULL
				ORDER BY updated_at DESC LIMIT 1
			`, alert.Source, alert.SourceID).Scan(&incidentID)
		}
	}
	if err != nil || incidentID == uuid.Nil {
		// Alert not yet linked — try to find a matching open incident by title+source.
		// Include a cluster constraint (via topology_path or alert label JOIN) to avoid
		// linking a resolved alert from cluster-A to an open incident from cluster-B
		// that happens to share the same title key (e.g. "Not all pods ready").
		resolvedCluster := extractLabelFromAlert(alert, "cluster", "k8s.cluster.name", "kubernetes_cluster")
		// If no cluster label, skip the broad title-match (too noisy across clusters) but
		// still try a direct source_id join against open incidents before giving up.
		if resolvedCluster == "" {
			if alert.Source != "" && alert.SourceID != "" {
				_ = s.db.QueryRowContext(ctx, `
					SELECT i.id FROM incidents i
					JOIN alerts a ON a.incident_id = i.id
					WHERE i.status IN ('open','investigating','acknowledged')
					  AND a.source = $1 AND a.source_id = $2
					ORDER BY i.created_at DESC LIMIT 1
				`, alert.Source, alert.SourceID).Scan(&incidentID)
			}
			if incidentID == uuid.Nil {
				log.Printf("Resolved alert %s (%s) has no cluster or incident link — nothing to update", alert.ID, alert.Title)
				return
			}
			// Found via source_id — backfill the link and fall through to HandleAlertResolved.
			if _, err := s.db.ExecContext(ctx, `UPDATE alerts SET incident_id = $1 WHERE id = $2`, incidentID, alert.ID); err != nil {
				log.Printf("[pipeline] retroactive link (no-cluster) alert=%s incident=%s: %v", alert.ID, incidentID, err)
			}
			log.Printf("Resolved alert %s (%s) updating incident %s (source_id fallback)", alert.ID, alert.Title, incidentID)
			s.HandleAlertResolved(ctx, alert.ID, incidentID)
			return
		}
		// NOTE: incidents table has no source or topology_path columns — filter via
		// EXISTS on the incident's alert_ids to match source and cluster together.
		err = s.db.QueryRowContext(ctx, `
			SELECT i.id FROM incidents i
			WHERE i.auto_created = TRUE
			  AND i.status IN ('open','investigating')
			  AND i.created_at >= NOW() - INTERVAL '24 hours'
			  AND (i.title ILIKE '%' || $1 || '%' OR i.description ILIKE '%' || $1 || '%')
			  AND EXISTS (
			      SELECT 1 FROM alerts a2
			      WHERE a2.id::text = ANY(SELECT jsonb_array_elements_text(i.alert_ids))
			        AND a2.source = $2
			        AND (a2.labels->>'cluster' = $3 OR a2.labels->>'k8s.cluster.name' = $3)
			  )
			ORDER BY i.created_at DESC LIMIT 1
		`, extractTitleKey(alert.Title), alert.Source, resolvedCluster).Scan(&incidentID)
		if err != nil || incidentID == uuid.Nil {
			log.Printf("Resolved alert %s (%s) has no linked incident — nothing to update", alert.ID, alert.Title)
			return
		}
		// Link the alert retroactively.
		if _, err := s.db.ExecContext(ctx, `UPDATE alerts SET incident_id = $1 WHERE id = $2`, incidentID, alert.ID); err != nil {
			log.Printf("[pipeline] retroactive incident_id link alert=%s incident=%s: %v", alert.ID, incidentID, err)
		}
		alertIDJSON := `["` + alert.ID.String() + `"]`
		if _, err := s.db.ExecContext(ctx, `
			UPDATE incidents
			SET alert_ids = COALESCE(alert_ids, '[]'::jsonb) || $1::jsonb,
			    updated_at = NOW()
			WHERE id = $2
			  AND NOT (COALESCE(alert_ids, '[]'::jsonb) @> $1::jsonb)
		`, alertIDJSON, incidentID); err != nil {
			log.Printf("[pipeline] retroactive alert_ids append alert=%s incident=%s: %v", alert.ID, incidentID, err)
		}
	}

	log.Printf("Resolved alert %s (%s) updating incident %s", alert.ID, alert.Title, incidentID)
	s.HandleAlertResolved(ctx, alert.ID, incidentID)
}

// findOpenIncidentForAlert finds an open incident for the same cluster+workload as this alert.
// Used by the discard path to attach low-signal alerts to existing relevant incidents.
func (s *AlertPipelineService) findOpenIncidentForAlert(ctx context.Context, alert *models.Alert) uuid.UUID {
	if s.db == nil {
		return uuid.Nil
	}
	cluster := extractLabelFromAlert(alert, "cluster", "k8s.cluster.name", "kubernetes_cluster")
	titleKey := extractTitleKey(alert.Title)

	// Try cluster + title key match first — check topology_path AND alert labels JSONB.
	if cluster != "" {
		var id uuid.UUID
		err := s.db.QueryRowContext(ctx, `
			SELECT i.id FROM incidents i
			WHERE i.auto_created = TRUE
			  AND i.status IN ('open','investigating')
			  AND i.created_at >= NOW() - INTERVAL '1 hour'
			  AND (
			    i.topology_path ILIKE '%' || $1 || '%'
			    OR EXISTS (
			        SELECT 1 FROM alerts a
			        WHERE a.id::text = ANY(SELECT jsonb_array_elements_text(i.alert_ids))
			          AND (a.labels->>'cluster' = $1 OR a.labels->>'k8s.cluster.name' = $1)
			    )
			  )
			  AND (i.title ILIKE '%' || $2 || '%' OR i.description ILIKE '%' || $2 || '%')
			ORDER BY i.created_at DESC LIMIT 1
		`, cluster, titleKey).Scan(&id)
		if err == nil && id != uuid.Nil {
			return id
		}
		// Broader: same cluster + same problem domain.
		domain := extractProblemDomainFromTitle(alert.Title + " " + alert.Description)
		if domain != "" {
			err = s.db.QueryRowContext(ctx, `
				SELECT i.id FROM incidents i
				WHERE i.auto_created = TRUE
				  AND i.status IN ('open','investigating')
				  AND i.created_at >= NOW() - INTERVAL '1 hour'
				  AND (
				    i.topology_path ILIKE '%' || $1 || '%'
				    OR EXISTS (
				        SELECT 1 FROM alerts a
				        WHERE a.id::text = ANY(SELECT jsonb_array_elements_text(i.alert_ids))
				          AND (a.labels->>'cluster' = $1 OR a.labels->>'k8s.cluster.name' = $1)
				    )
				  )
				  AND (i.title ILIKE '%' || $2 || '%' OR i.description ILIKE '%' || $2 || '%')
				ORDER BY i.created_at DESC LIMIT 1
			`, cluster, domain).Scan(&id)
			if err == nil && id != uuid.Nil {
				return id
			}
		}

		// Cascade storm window: cluster-only match within 30 min, no title restriction.
		// Catches node-down pod-not-ready cascades where titles are completely different.
		// Pick the OLDEST incident in the cluster (the root cause opened first).
		// Guard: only match incidents whose correlation_id is blank (catch-all) or matches
		// this alert's rootCauseEntity — prevents scoring-path workload alerts from
		// incorrectly merging into an unrelated node-down incident (S7-class false merges).
		cascadeRCE := extractLabelFromAlert(alert, "root_cause_entity", "rootCauseEntity")
		err = s.db.QueryRowContext(ctx, `
			SELECT i.id FROM incidents i
			WHERE i.auto_created = TRUE
			  AND i.status IN ('open','investigating')
			  AND i.created_at >= NOW() - INTERVAL '30 minutes'
			  AND (
			    i.topology_path ILIKE '%' || $1 || '%'
			    OR EXISTS (
			        SELECT 1 FROM alerts a
			        WHERE a.id::text = ANY(SELECT jsonb_array_elements_text(i.alert_ids))
			          AND (a.labels->>'cluster' = $1 OR a.labels->>'k8s.cluster.name' = $1)
			    )
			  )
			  AND (COALESCE(i.correlation_id, '') = '' OR i.correlation_id = $2)
			ORDER BY i.created_at ASC LIMIT 1
		`, cluster, cascadeRCE).Scan(&id)
		if err == nil && id != uuid.Nil {
			return id
		}
	}
	return uuid.Nil
}

// attachAlertToIncident links an alert to an existing incident and adds a timeline event.
func (s *AlertPipelineService) attachAlertToIncident(
	ctx context.Context,
	alert *models.Alert,
	incidentID uuid.UUID,
	result *correlation.FinalCorrelationResult,
	eventTitle, eventDesc string,
) {
	s.mergeAlertIntoIncident(ctx, alert, incidentID, result, eventTitle, eventDesc)
}

// mergeAlertIntoIncident is the shared low-level helper for all merge/attach paths.
// It also performs root switching: if the incoming alert has a higher InfraLevel than
// the current root entity recorded on the incident, the incident's root metadata is
// promoted to reflect the new higher-level cause.
func (s *AlertPipelineService) mergeAlertIntoIncident(
	ctx context.Context,
	alert *models.Alert,
	incidentID uuid.UUID,
	result *correlation.FinalCorrelationResult,
	eventTitle, eventDesc string,
) {
	if s.db == nil {
		return
	}
	// Use @> containment guard so the same alert is never appended twice.
	// This prevents the duplicate-IDs problem where concurrent processAlert goroutines
	// or a retry path calls mergeAlertIntoIncident more than once for the same alert.
	alertIDJSON := `["` + alert.ID.String() + `"]`

	// Both UPDATEs must succeed or fail atomically; a partial commit leaves the
	// incident claiming an alert_id that the alert row doesn't agree with.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("[pipeline] mergeAlertIntoIncident: begin tx alert=%s incident=%s: %v", alert.ID, incidentID, err)
		return
	}
	defer func() {
		if p := recover(); p != nil {
			tx.Rollback()
			panic(p)
		}
	}()

	if _, err := tx.ExecContext(ctx, `
		UPDATE incidents
		SET alert_ids             = COALESCE(alert_ids, '[]'::jsonb) || $1::jsonb,
		    correlation_confidence = GREATEST(COALESCE(correlation_confidence, 0), $2),
		    updated_at            = NOW()
		WHERE id = $3
		  AND NOT (COALESCE(alert_ids, '[]'::jsonb) @> $1::jsonb)
	`, alertIDJSON, result.FinalScore, incidentID); err != nil {
		tx.Rollback()
		log.Printf("[pipeline] merge alert_ids alert=%s incident=%s: %v", alert.ID, incidentID, err)
		return
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE alerts a
		SET incident_id              = $1,
		    auto_created_incident_id = $1,
		    correlation_confidence   = $2,
		    correlation_id           = COALESCE(NULLIF(a.correlation_id, ''),
		                                        (SELECT COALESCE(NULLIF(i.correlation_id,''), i.id::text)
		                                         FROM incidents i WHERE i.id = $1))
		WHERE a.id = $3
		  AND (a.incident_id IS NULL OR a.incident_id = $1)
	`, incidentID, result.FinalScore, alert.ID); err != nil {
		tx.Rollback()
		log.Printf("[pipeline] link alert to incident alert=%s incident=%s: %v", alert.ID, incidentID, err)
		return
	}

	// Check whether the alert UPDATE actually fired. If incident_id IS NOT NULL and
	// points at a DIFFERENT incident, the WHERE guard above affected 0 rows — that means
	// we already appended the alert UUID to this incident's alert_ids (step 1) but the
	// alert FK stays with its real owner. Roll back to avoid creating a split-brain entry.
	//
	// We detect this by re-reading incident_id inside the same transaction; if it still
	// doesn't match, roll back both steps atomically.
	var currentIncidentID *uuid.UUID
	_ = tx.QueryRowContext(ctx, `SELECT incident_id FROM alerts WHERE id = $1`, alert.ID).Scan(&currentIncidentID)
	if currentIncidentID == nil || *currentIncidentID != incidentID {
		tx.Rollback()
		log.Printf("[pipeline] mergeAlertIntoIncident: alert %s already owned by %v — rolled back alert_ids append", alert.ID, currentIncidentID)
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("[pipeline] mergeAlertIntoIncident commit alert=%s incident=%s: %v", alert.ID, incidentID, err)
		return
	}

	// Race guard: RESOLVED may have arrived while this merge tx was in-flight.
	// Now that incident_id is persisted, trigger the open-count check immediately
	// so the incident is closed rather than left open with a resolved alert inside.
	var alertStatus string
	_ = s.db.QueryRowContext(ctx, `SELECT status FROM alerts WHERE id = $1`, alert.ID).Scan(&alertStatus)
	if alertStatus == "resolved" || alertStatus == "closed" {
		log.Printf("[pipeline] post-merge race: alert %s already %s — closing incident %s", alert.ID, alertStatus, incidentID)
		s.HandleAlertResolved(ctx, alert.ID, incidentID)
	}

	// Root switching: promote incident root when this alert is at a higher InfraLevel.
	s.maybePromoteToRoot(ctx, alert, incidentID)

	if s.incidentSvc != nil {
		metadata := map[string]interface{}{
			"alert_id": alert.ID.String(),
			"severity": alert.Severity,
			"source":   alert.Source,
			"score":    result.FinalScore,
			"strategy": result.DominantStrategy,
		}
		s.incidentSvc.AddTimelineEvent(ctx, incidentID, uuid.Nil,
			"alert_added", eventTitle, eventDesc, metadata)
	}
}

// maybePromoteToRoot updates an incident's root entity metadata when the incoming
// alert is at a higher InfraLevel than the currently recorded root.
// This handles the case where a Pod alert arrives first (level 1) and later a Node
// alert (level 2) or BM alert (level 5) arrives and merges — the incident root
// should reflect the highest-level (most causal) entity.
func (s *AlertPipelineService) maybePromoteToRoot(ctx context.Context, alert *models.Alert, incidentID uuid.UUID) {
	alertLevel := alertInfraLevel(alert)
	if alertLevel == correlation.InfraLevelUnknown {
		return
	}

	var currentLevelRaw sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT (davis_ai_analysis->>'root_level')::int FROM incidents WHERE id = $1`, incidentID,
	).Scan(&currentLevelRaw)
	if err != nil {
		return
	}

	currentLevel := correlation.InfraLevel(0)
	if currentLevelRaw.Valid {
		currentLevel = correlation.InfraLevel(currentLevelRaw.Int64)
	}

	if int(alertLevel) <= int(currentLevel) {
		return // incoming alert is not higher — no promotion needed
	}

	entityID := ""
	entityLabel := ""
	if alert.Labels != nil {
		// Prefer the explicit root cause entity ID, then fall back to Dynatrace entity tags.
		for _, k := range []string{"root_cause_entity_id", "dt.entity.host", "dt.entity.kubernetes_node", "entity_id"} {
			if v, ok := alert.Labels[k]; ok && v != "" {
				entityID = v
				break
			}
		}
		// Pick the most semantically meaningful label for the root entity.
		for _, k := range []string{"root_cause_entity", "host.name", "k8s.node.name", "node"} {
			if v, ok := alert.Labels[k]; ok && v != "" {
				entityLabel = v
				break
			}
		}
	}
	if entityLabel == "" {
		entityLabel = alert.Title
	}

	if _, err := s.db.ExecContext(ctx, `
		UPDATE incidents SET
			correlation_id = COALESCE(NULLIF(correlation_id,''), $1),
			davis_ai_analysis = jsonb_set(
				jsonb_set(
					jsonb_set(
						COALESCE(davis_ai_analysis, '{}'::jsonb),
						'{root_entity_id}', to_jsonb($1::text)
					),
					'{root_entity_label}', to_jsonb($2::text)
				),
				'{root_level}', to_jsonb($3::int)
			)
		WHERE id = $4
	`, entityID, entityLabel, int(alertLevel), incidentID); err != nil {
		log.Printf("[pipeline] promote root entity incident=%s: %v", incidentID, err)
	}

	log.Printf("⬆Root promoted: incident=%s entity=%q level=%s (was %s)",
		incidentID, entityLabel, alertLevel.String(), currentLevel.String())
}

// enrichEntityLabels fills in missing entity classification labels from whatever
// structural data the alert already carries.  It handles two cases:
//
//  1. Alerts where the normalizer extracted k8s label values (cluster, workload,
//     node, etc.) but did not write back entity_type — this happens for some
//     Dynatrace payloads that lack AffectedEntities.
//
//  2. Alerts stored by the pre-V2 webhook handler whose labels are sparse
//     (source/problem_id/environment only) but whose description text contains
//     "k8s.X.name: value" lines (Dynatrace ProblemDetailsText format).
//
// The function mutates alert.Labels in-place and is safe to call on every alert.
// Sweep functions extracted to sweep.go
