package investigation

import (
	"context"
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	domain "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/investigation"
	pastinvestigations_pkg "github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/fetchers/pastinvestigations"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/shared/metrics"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/shared/tracing"
	"go.opentelemetry.io/otel/attribute"
)

const (
	defaultTimeBudgetMs  = 45_000
	lockTTL              = 2 * time.Minute
	lockRenewInterval    = 30 * time.Second
	recoveryInterval     = 90 * time.Second
	orphanThreshold      = 3 * time.Minute
	maxOrphanAge         = 30 * time.Minute
	defaultMaxConcurrent = 20
)

// ManagerConfig holds all Manager configuration.
type ManagerConfig struct {
	PodID         string
	TimeBudgetMs  int
	MaxConcurrent int
}

// Manager is the concrete implementation of Service.
type Manager struct {
	repo             domain.Repository
	evidenceBus      EvidenceBus
	hypothesisEngine HypothesisEngine
	rcaGenerator     RCAGenerator
	publisher        EventPublisher
	logger           *slog.Logger
	podID            string
	timeBudgetMs     int
	semaphore        chan struct{}
	mu               sync.Mutex
	db               *sql.DB // for embedding storage — may be nil
	// alertHubBaseURL is the AlertHub backend base URL for RCA result writeback.
	// When set, the manager POSTs the winning hypothesis and confidence to
	// PATCH /api/v1/incidents/:id/rca-result so operators see OIE results
	// immediately without opening the incident in the UI.
	alertHubBaseURL string
	// ollamaBaseURL is the Ollama endpoint for generating semantic embeddings.
	// When set, completed investigations get a 768-dim nomic-embed-text embedding
	// stored in rca_investigations.embedding so the past-investigations fetcher
	// can use pgvector cosine similarity for much better recall.
	ollamaBaseURL string
	// seenCalls tracks (fetcher_id, params_hash) pairs already called within
	// an active investigation — HolmesGPT repeat-call prevention pattern.
	// Prevents infinite loops when the evidence bus or hypothesis engine
	// re-triggers the same fetch with identical parameters.
	seenCalls sync.Map // key: "invID:fetcherID:paramsHash" → struct{}{}
}

// SetAlertHubURL wires the AlertHub backend URL for RCA result writeback.
func (m *Manager) SetAlertHubURL(url string) { m.alertHubBaseURL = url }

// SetDB wires the AlertHub PostgreSQL connection for embedding storage.
func (m *Manager) SetDB(db *sql.DB) { m.db = db }

// SetOllamaURL wires the Ollama endpoint for semantic embedding generation.
// When set, a nomic-embed-text embedding is generated for each completed
// investigation and stored in rca_investigations.embedding for pgvector search.
func (m *Manager) SetOllamaURL(url string) { m.ollamaBaseURL = url }

// IsFetchCallSeen returns true if the given (invID, fetcherID, paramsHash) combination
// has already been executed in this process lifetime (HolmesGPT repeat-call guard).
func (m *Manager) IsFetchCallSeen(invID, fetcherID, paramsHash string) bool {
	key := invID + ":" + fetcherID + ":" + paramsHash
	_, seen := m.seenCalls.Load(key)
	return seen
}

// MarkFetchCallSeen registers a (invID, fetcherID, paramsHash) as having been called.
func (m *Manager) MarkFetchCallSeen(invID, fetcherID, paramsHash string) {
	key := invID + ":" + fetcherID + ":" + paramsHash
	m.seenCalls.Store(key, struct{}{})
}

// clearSeenCalls removes all repeat-call prevention entries for a completed investigation.
// Called at the end of runInvestigation to prevent unbounded map growth.
func (m *Manager) clearSeenCalls(invID string) {
	prefix := invID + ":"
	m.seenCalls.Range(func(k, _ any) bool {
		if key, ok := k.(string); ok && len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			m.seenCalls.Delete(k)
		}
		return true
	})
}

// NewManager constructs a Manager with all required dependencies.
func NewManager(
	repo domain.Repository,
	evidenceBus EvidenceBus,
	hypothesisEngine HypothesisEngine,
	rcaGenerator RCAGenerator,
	publisher EventPublisher,
	logger *slog.Logger,
	cfg ManagerConfig,
) *Manager {
	if repo == nil {
		panic("oie manager: repository is required")
	}
	if evidenceBus == nil {
		panic("oie manager: evidence bus is required")
	}
	if hypothesisEngine == nil {
		panic("oie manager: hypothesis engine is required")
	}
	if rcaGenerator == nil {
		panic("oie manager: rca generator is required")
	}
	if publisher == nil {
		panic("oie manager: event publisher is required")
	}
	if logger == nil {
		panic("oie manager: logger is required")
	}

	maxConcurrent := cfg.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = defaultMaxConcurrent
	}
	timeBudget := cfg.TimeBudgetMs
	if timeBudget <= 0 {
		timeBudget = defaultTimeBudgetMs
	}

	return &Manager{
		repo:             repo,
		evidenceBus:      evidenceBus,
		hypothesisEngine: hypothesisEngine,
		rcaGenerator:     rcaGenerator,
		publisher:        publisher,
		logger:           logger,
		podID:            cfg.PodID,
		timeBudgetMs:     timeBudget,
		semaphore:        make(chan struct{}, maxConcurrent),
	}
}

// Start creates and begins an investigation for the given incident.
// Idempotent: returns the existing investigation if the idempotency key already exists.
func (m *Manager) Start(ctx context.Context, req *StartRequest) (*StartResponse, error) {
	ctx, span := tracing.Tracer().Start(ctx, "investigation.manager.start")
	defer span.End()

	span.SetAttributes(
		attribute.String("incident.id", req.IncidentID.String()),
		attribute.String("idempotency.key", req.IdempotencyKey),
		attribute.String("severity", req.Severity),
	)

	// Check idempotency before creating.
	existing, err := m.repo.GetByIdempotencyKey(ctx, req.IdempotencyKey)
	if err == nil && existing != nil {
		m.logger.InfoContext(ctx, "duplicate start request",
			"idempotency_key", req.IdempotencyKey,
			"existing_investigation_id", existing.ID,
		)
		metrics.DuplicateInvestigationsPrevented.Inc()
		return &StartResponse{
			InvestigationID: existing.ID,
			Status:          existing.Status,
			AlreadyExisted:  true,
		}, nil
	}
	if err != nil {
		if _, isNotFound := err.(domain.ErrInvestigationNotFound); !isNotFound {
			return nil, fmt.Errorf("checking idempotency key: %w", err)
		}
	}

	inv, err := domain.NewInvestigation(
		req.IncidentID,
		req.IncidentNumber,
		req.IdempotencyKey,
		req.SourceMessageKey,
		req.Severity,
		req.IncidentStartedAt,
		m.timeBudgetMs,
	)
	if err != nil {
		return nil, fmt.Errorf("creating investigation: %w", err)
	}

	playbookID := req.PlaybookID
	if playbookID == "" {
		playbookID = resolvePlaybookID(req.RootEntityType, req.FailureClass)
	}
	if req.RootEntityID != nil {
		inv.SetEntityContext(
			*req.RootEntityID,
			derefString(req.RootEntityType),
			derefString(req.FailureClass),
			derefString(req.Domain),
			playbookID,
		)
	} else {
		// No RootEntityID yet (topology_path-only mode — EIRS not deployed).
		// Still apply playbook, domain, and failure class so evidence bus and
		// hypothesis engine use the correct configuration.
		inv.PlaybookID = playbookID
		if req.Domain != nil {
			inv.Domain = req.Domain
		}
		if req.FailureClass != nil {
			inv.FailureClass = req.FailureClass
		}
	}

	// Always carry topology context so the evidence bus can derive entity info
	// from AlertHub's topology_path/correlation_id without needing EIRS.
	if req.TopologyPath != "" {
		inv.TopologyPath = req.TopologyPath
	}
	if req.CorrelationID != "" {
		inv.CorrelationID = req.CorrelationID
	}

	if err := m.repo.Create(ctx, inv); err != nil {
		if isDuplicateKeyError(err) {
			winner, fetchErr := m.repo.GetByIdempotencyKey(ctx, req.IdempotencyKey)
			if fetchErr != nil {
				return nil, fmt.Errorf("resolving duplicate after race: %w", fetchErr)
			}
			metrics.DuplicateInvestigationsPrevented.Inc()
			return &StartResponse{
				InvestigationID: winner.ID,
				Status:          winner.Status,
				AlreadyExisted:  true,
			}, nil
		}
		return nil, fmt.Errorf("persisting investigation: %w", err)
	}

	m.dispatchEvents(ctx, inv)

	m.logger.InfoContext(ctx, "investigation created",
		"investigation_id", inv.ID,
		"incident_id", inv.IncidentID,
		"severity", inv.Severity,
	)
	metrics.InvestigationsStarted.WithLabelValues(inv.PlaybookID, inv.Severity).Inc()

	// Dispatch to worker pool (non-blocking).
	// If semaphore is full, investigation stays PENDING; recovery loop picks it up.
	select {
	case m.semaphore <- struct{}{}:
		go func() {
			defer func() { <-m.semaphore }()
			metrics.SemaphoreUtilization.Set(float64(len(m.semaphore)))
			m.runInvestigation(inv.ID)
			metrics.SemaphoreUtilization.Set(float64(len(m.semaphore)))
		}()
	default:
		m.logger.WarnContext(ctx, "semaphore full — investigation will be recovered",
			"investigation_id", inv.ID,
		)
		metrics.SemaphoreFullTotal.Inc()
	}

	return &StartResponse{
		InvestigationID: inv.ID,
		Status:          inv.Status,
		AlreadyExisted:  false,
	}, nil
}

// Cancel transitions an investigation to CANCELLED.
func (m *Manager) Cancel(ctx context.Context, id uuid.UUID, reason string) error {
	ctx, span := tracing.Tracer().Start(ctx, "investigation.manager.cancel")
	defer span.End()

	inv, err := m.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if inv.Status.IsTerminal() {
		return nil
	}

	prevStatus := inv.Status
	if err := inv.TransitionTo(domain.StatusCancelled, reason); err != nil {
		return fmt.Errorf("cancelling investigation: %w", err)
	}
	if err := m.repo.UpdateStatus(ctx, inv.ID, prevStatus, domain.StatusCancelled, reason); err != nil {
		return fmt.Errorf("persisting cancellation: %w", err)
	}

	m.dispatchEvents(ctx, inv)
	m.logger.InfoContext(ctx, "investigation cancelled", "investigation_id", id, "reason", reason)
	return nil
}

// GetByID returns the investigation with the given ID.
func (m *Manager) GetByID(ctx context.Context, id uuid.UUID) (*domain.Investigation, error) {
	return m.repo.GetByID(ctx, id)
}

// GetByIncidentID returns the most recent active investigation for an incident.
func (m *Manager) GetByIncidentID(ctx context.Context, incidentID uuid.UUID) (*domain.Investigation, error) {
	return m.repo.GetByIncidentID(ctx, incidentID)
}

// ListActive returns all investigations in non-terminal states.
func (m *Manager) ListActive(ctx context.Context) ([]*domain.Investigation, error) {
	return m.repo.ListActive(ctx)
}

// RecoverOrphaned finds investigations whose lock has expired and re-queues them.
func (m *Manager) RecoverOrphaned(ctx context.Context) (int, error) {
	ctx, span := tracing.Tracer().Start(ctx, "investigation.manager.recover_orphaned")
	defer span.End()

	orphans, err := m.repo.ListOrphaned(ctx, orphanThreshold)
	if err != nil {
		return 0, fmt.Errorf("listing orphaned investigations: %w", err)
	}

	recovered := 0
	for _, inv := range orphans {
		if time.Since(inv.TriggeredAt) > maxOrphanAge {
			m.logger.WarnContext(ctx, "marking stale orphan as failed",
				"investigation_id", inv.ID,
				"age", time.Since(inv.TriggeredAt),
			)
			if err := m.repo.UpdateStatus(ctx, inv.ID, inv.Status, domain.StatusFailed,
				"exceeded maximum orphan age"); err != nil {
				m.logger.ErrorContext(ctx, "failed to mark orphan as failed",
					"investigation_id", inv.ID, "error", err)
			}
			metrics.OrphanedInvestigationsFailed.Inc()
			continue
		}

		acquired, err := m.repo.AcquireLock(ctx, inv.ID, m.podID, lockTTL)
		if err != nil || !acquired {
			continue
		}

		if err := m.repo.UpdateStatus(ctx, inv.ID, inv.Status, domain.StatusPending,
			fmt.Sprintf("recovered by pod %s (attempt %d)", m.podID, inv.RecoveryAttemptCount+1)); err != nil {
			m.logger.ErrorContext(ctx, "failed to reset orphan to pending",
				"investigation_id", inv.ID, "error", err)
			continue
		}

		select {
		case m.semaphore <- struct{}{}:
			go func(id uuid.UUID) {
				defer func() { <-m.semaphore }()
				m.runInvestigation(id)
			}(inv.ID)
			recovered++
			metrics.OrphanedInvestigationsRecovered.Inc()
		default:
			_ = m.repo.ReleaseLock(ctx, inv.ID, m.podID)
		}
	}

	if recovered > 0 {
		m.logger.InfoContext(ctx, "orphaned investigations recovered", "count", recovered)
	}
	return recovered, nil
}

// StartRecoveryLoop begins the background goroutine that periodically recovers orphaned investigations.
func (m *Manager) StartRecoveryLoop(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(recoveryInterval)
		defer ticker.Stop()
		if _, err := m.RecoverOrphaned(ctx); err != nil {
			m.logger.ErrorContext(ctx, "initial orphan recovery failed", "error", err)
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := m.RecoverOrphaned(ctx); err != nil {
					m.logger.ErrorContext(ctx, "periodic orphan recovery failed", "error", err)
				}
			}
		}
	}()
}

// runInvestigation executes the full investigation lifecycle.
// Must be called inside a goroutine that holds a semaphore slot.
func (m *Manager) runInvestigation(invID uuid.UUID) {
	ctx := context.Background()
	ctx, span := tracing.Tracer().Start(ctx, "investigation.manager.run")
	defer span.End()
	span.SetAttributes(attribute.String("investigation.id", invID.String()))

	logger := m.logger.With("investigation_id", invID)

	inv, err := m.repo.GetByID(ctx, invID)
	if err != nil {
		logger.ErrorContext(ctx, "failed to load investigation", "error", err)
		return
	}

	acquired, err := m.repo.AcquireLock(ctx, invID, m.podID, lockTTL)
	if err != nil || !acquired {
		logger.WarnContext(ctx, "could not acquire lock — another pod may be running this investigation")
		return
	}
	defer func() { _ = m.repo.ReleaseLock(context.Background(), invID, m.podID) }()

	renewCtx, cancelRenew := context.WithCancel(ctx)
	defer cancelRenew()
	go m.renewLockLoop(renewCtx, invID, logger)

	budgetCtx, cancelBudget := context.WithTimeout(ctx, time.Duration(inv.TimeBudgetMs)*time.Millisecond)
	defer cancelBudget()

	startTime := time.Now()
	logger.InfoContext(ctx, "investigation starting",
		"severity", inv.Severity, "playbook_id", inv.PlaybookID)

	// PENDING → RUNNING
	if err := m.transition(ctx, inv, domain.StatusRunning, "investigation started"); err != nil {
		return
	}

	// RUNNING → WAITING_FOR_EVIDENCE
	if err := m.transition(ctx, inv, domain.StatusWaitingForEvidence, "evidence gathering started"); err != nil {
		return
	}

	evidenceResult, err := m.gatherEvidence(budgetCtx, inv)
	if err != nil {
		logger.ErrorContext(ctx, "evidence gathering failed", "error", err)
		m.failInvestigation(ctx, inv, fmt.Sprintf("evidence gathering error: %v", err))
		return
	}

	inv.EvidenceGathered = evidenceResult.EvidenceCount
	inv.EvidenceSources = evidenceResult.EvidenceSources

	// WAITING_FOR_EVIDENCE → RCA_GENERATION
	if err := m.transition(ctx, inv, domain.StatusRCAGeneration, "evidence gathering complete"); err != nil {
		return
	}

	hypothesisResult, err := m.evaluateHypotheses(budgetCtx, inv)
	if err != nil {
		logger.ErrorContext(ctx, "hypothesis evaluation failed", "error", err)
		m.failInvestigation(ctx, inv, fmt.Sprintf("hypothesis evaluation error: %v", err))
		return
	}

	rcaResult, err := m.generateRCA(budgetCtx, inv)
	if err != nil {
		logger.ErrorContext(ctx, "rca generation failed", "error", err)
		m.failInvestigation(ctx, inv, fmt.Sprintf("rca generation error: %v", err))
		return
	}

	// Confidence-gated retry (KubeSentinel pattern): if confidence < 0.65 and there
	// is still time budget remaining, re-gather evidence once and regenerate the RCA.
	// The fresh evidence window may surface signals that were not yet propagated.
	if rcaResult.Confidence < 0.65 && budgetCtx.Err() == nil {
		logger.InfoContext(ctx, "low-confidence RCA — retrying evidence collection",
			"confidence", fmt.Sprintf("%.2f", rcaResult.Confidence))
		retryEvidence, retryErr := m.gatherEvidence(budgetCtx, inv)
		if retryErr == nil && retryEvidence.EvidenceCount > evidenceResult.EvidenceCount {
			inv.EvidenceGathered = retryEvidence.EvidenceCount
			inv.EvidenceSources = retryEvidence.EvidenceSources
			if retried, retryRCAErr := m.generateRCA(budgetCtx, inv); retryRCAErr == nil &&
				retried.Confidence > rcaResult.Confidence {
				logger.InfoContext(ctx, "retry improved confidence",
					"before", fmt.Sprintf("%.2f", rcaResult.Confidence),
					"after", fmt.Sprintf("%.2f", retried.Confidence))
				rcaResult = retried
			}
		}
	}

	// Ensure CausalChain and BlastRadius are valid JSON before persisting.
	// A nil or empty slice would produce an invalid json.RawMessage and cause
	// "pq: invalid input syntax for type json" on INSERT/UPDATE.
	causalChainJSON := safeJSONField(rcaResult.CausalChain)
	blastRadiusJSON := safeJSONField(rcaResult.BlastRadius)

	if err := inv.SetRCAResult(
		rcaResult.WinningHypothesisID,
		rcaResult.Confidence,
		rcaResult.ConfidenceBand,
		rcaResult.Summary,
		causalChainJSON,
		blastRadiusJSON,
		evidenceResult.EvidenceCount,
		evidenceResult.EvidenceSources,
		hypothesisResult.Generated,
		hypothesisResult.Rejected,
	); err != nil {
		m.failInvestigation(ctx, inv, fmt.Sprintf("setting rca result: %v", err))
		return
	}

	if rcaResult.Narrative != "" {
		inv.SetNarrative(rcaResult.Narrative, rcaResult.NarrativeModel)
	}

	// Store citation refs alongside narrative for frontend rendering.
	if len(rcaResult.Citations) > 0 {
		if citJSON, err := json.Marshal(rcaResult.Citations); err == nil {
			inv.CitationsJSON = json.RawMessage(citJSON)
		}
	}

	// RCA_GENERATION → COMPLETED
	if err := m.transition(ctx, inv, domain.StatusCompleted, "rca generation complete"); err != nil {
		return
	}

	// Persist the complete result atomically.
	if err := m.repo.Update(ctx, inv); err != nil {
		logger.ErrorContext(ctx, "failed to persist completed investigation", "error", err)
		return
	}

	m.dispatchEvents(ctx, inv)
	m.publishRCACompleted(ctx, inv)

	// Generate semantic embedding for this investigation so future similar incidents
	// can find it via pgvector cosine similarity (nomic-embed-text, 768 dims).
	// The embedding is stored async so it doesn't block the investigation pipeline.
	if m.ollamaBaseURL != "" && inv.RootCauseSummary != nil {
		go func() {
			embCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			text := inv.IncidentID.String()
			if inv.RootCauseSummary != nil { text = *inv.RootCauseSummary }
			if inv.TopologyPath != "" { text += " path:" + inv.TopologyPath }
			pastinvestigations_pkg.GenerateAndStoreEmbedding(embCtx, m.db, m.ollamaBaseURL, inv.ID.String(), text)
		}()
	}

	// Clear repeat-call guard entries for this investigation — prevents unbounded growth.
	m.clearSeenCalls(invID.String())

	elapsed := time.Since(startTime)
	logger.InfoContext(ctx, "investigation completed",
		"elapsed_ms", elapsed.Milliseconds(),
		"confidence", rcaResult.Confidence,
		"confidence_band", rcaResult.ConfidenceBand,
	)

	metrics.InvestigationDuration.WithLabelValues(inv.PlaybookID, "completed").Observe(elapsed.Seconds())
	metrics.InvestigationsCompleted.WithLabelValues(inv.PlaybookID, rcaResult.ConfidenceBand, "completed").Inc()
}

func (m *Manager) gatherEvidence(ctx context.Context, inv *domain.Investigation) (*EvidenceResult, error) {
	req := &EvidenceRequest{
		InvestigationID: inv.ID,
		IncidentID:      inv.IncidentID,
		Severity:        inv.Severity,
		PlaybookID:      inv.PlaybookID,
		IncidentStartAt: inv.IncidentStartedAt,
		BudgetMs:        inv.TimeBudgetMs,
		TopologyPath:    inv.TopologyPath,
		CorrelationID:   inv.CorrelationID,
	}
	if inv.RootEntityID != nil {
		req.RootEntityID = inv.RootEntityID
	}
	if inv.RootEntityType != nil {
		req.RootEntityType = *inv.RootEntityType
	}
	if inv.FailureClass != nil {
		req.FailureClass = *inv.FailureClass
	}
	if inv.Domain != nil {
		req.Domain = *inv.Domain
	}
	return m.evidenceBus.Execute(ctx, req)
}

func (m *Manager) evaluateHypotheses(ctx context.Context, inv *domain.Investigation) (*HypothesisResult, error) {
	req := &HypothesisRequest{
		InvestigationID: inv.ID,
		IncidentID:      inv.IncidentID,
		Severity:        inv.Severity,
		PlaybookID:      inv.PlaybookID,
	}
	if inv.RootEntityType != nil {
		req.RootEntityType = *inv.RootEntityType
	}
	if inv.FailureClass != nil {
		req.FailureClass = *inv.FailureClass
	}
	if inv.Domain != nil {
		req.Domain = *inv.Domain
	}
	return m.hypothesisEngine.Evaluate(ctx, req)
}

func (m *Manager) generateRCA(ctx context.Context, inv *domain.Investigation) (*RCAResult, error) {
	req := &RCARequest{
		InvestigationID:   inv.ID,
		IncidentID:        inv.IncidentID,
		IncidentStartedAt: inv.IncidentStartedAt,
		TopologyPath:      inv.TopologyPath,
	}
	if inv.Domain != nil {
		req.Domain = *inv.Domain
	}
	return m.rcaGenerator.Generate(ctx, req)
}

// transition applies a state transition, capturing prevStatus BEFORE calling TransitionTo.
// This is the correct pattern — prevStatus must be captured before the transition mutates inv.Status.
func (m *Manager) transition(ctx context.Context, inv *domain.Investigation, next domain.Status, reason string) error {
	prevStatus := inv.Status // Capture BEFORE transition.

	if err := inv.TransitionTo(next, reason); err != nil {
		m.logger.ErrorContext(ctx, "invalid state transition",
			"investigation_id", inv.ID, "from", prevStatus, "to", next, "error", err)
		m.failInvestigation(ctx, inv, fmt.Sprintf("state transition error: %v", err))
		return err
	}

	if err := m.repo.UpdateStatus(ctx, inv.ID, prevStatus, next, reason); err != nil {
		m.logger.ErrorContext(ctx, "failed to persist status transition",
			"investigation_id", inv.ID, "status", next, "error", err)
		return err
	}

	m.dispatchEvents(ctx, inv)
	return nil
}

func (m *Manager) failInvestigation(ctx context.Context, inv *domain.Investigation, reason string) {
	if inv.Status.IsTerminal() {
		return
	}
	prevStatus := inv.Status
	if err := inv.TransitionTo(domain.StatusFailed, reason); err != nil {
		m.logger.ErrorContext(ctx, "could not transition to FAILED",
			"investigation_id", inv.ID, "error", err)
		return
	}
	if err := m.repo.UpdateStatus(ctx, inv.ID, prevStatus, domain.StatusFailed, reason); err != nil {
		m.logger.ErrorContext(ctx, "could not persist FAILED state",
			"investigation_id", inv.ID, "error", err)
	}
	m.dispatchEvents(ctx, inv)
	metrics.InvestigationsCompleted.WithLabelValues(inv.PlaybookID, "none", "failed").Inc()
}

func (m *Manager) renewLockLoop(ctx context.Context, invID uuid.UUID, logger *slog.Logger) {
	ticker := time.NewTicker(lockRenewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			renewed, err := m.repo.RenewLock(ctx, invID, m.podID, lockTTL)
			if err != nil {
				logger.ErrorContext(ctx, "lock renewal error", "error", err)
				return
			}
			if !renewed {
				logger.WarnContext(ctx, "lock lost — another pod may have taken over this investigation")
				return
			}
		}
	}
}

func (m *Manager) dispatchEvents(ctx context.Context, inv *domain.Investigation) {
	events := inv.PullDomainEvents()
	for _, ev := range events {
		payload, err := json.Marshal(map[string]interface{}{
			"event_type":       ev.EventType(),
			"investigation_id": ev.AggregateID(),
			"occurred_at":      ev.OccurredAt(),
		})
		if err != nil {
			m.logger.ErrorContext(ctx, "failed to marshal domain event",
				"event_type", ev.EventType(), "error", err)
			continue
		}

		persisted := &domain.PersistedEvent{
			ID:              uuid.New(),
			InvestigationID: ev.AggregateID(),
			EventType:       ev.EventType(),
			Payload:         payload,
		}
		if saveErr := m.repo.SaveEvent(ctx, persisted); saveErr != nil {
			m.logger.ErrorContext(ctx, "failed to save domain event",
				"event_type", ev.EventType(), "error", saveErr)
		}

		if pubErr := m.publisher.PublishInvestigationEvent(ctx, ev.EventType(), payload); pubErr != nil {
			m.logger.WarnContext(ctx, "failed to publish domain event to Kafka",
				"event_type", ev.EventType(), "error", pubErr)
		}
	}
}

func (m *Manager) publishRCACompleted(ctx context.Context, inv *domain.Investigation) {
	if inv.Confidence == nil {
		return
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"investigation_id": inv.ID,
		"incident_id":      inv.IncidentID,
		"confidence":       *inv.Confidence,
		"confidence_band":  derefString(inv.ConfidenceBand),
		"report_version":   inv.ReportVersion,
	})
	if err := m.publisher.PublishInvestigationEvent(ctx, "investigation.rca_completed", payload); err != nil {
		m.logger.WarnContext(ctx, "failed to publish rca_completed event", "error", err)
	}

	// Writeback: update the AlertHub incident directly so operators see the OIE result
	// immediately without opening the incident. This prevents incidents from staying
	// permanently in rca_status='investigating' when the tautological guard deferred to OIE.
	if m.alertHubBaseURL != "" && inv.IncidentID != uuid.Nil {
		go m.writebackToAlertHub(inv)
	}
}

// writebackToAlertHub calls the AlertHub backend to persist the OIE RCA result
// onto the incident row, updating rca_confidence, rca_status, and ai_root_cause.
func (m *Manager) writebackToAlertHub(inv *domain.Investigation) {
	if inv.Confidence == nil || inv.RootCauseSummary == nil {
		return
	}
	body, _ := json.Marshal(map[string]interface{}{
		"investigation_id": inv.ID.String(),
		"confidence":       *inv.Confidence,
		"confidence_band":  derefString(inv.ConfidenceBand),
		"root_cause":       derefString(inv.RootCauseSummary),
		"evidence_count":   inv.EvidenceGathered,
		"hypotheses_generated": inv.HypothesesGenerated,
		"hypotheses_rejected":  inv.HypothesesRejected,
	})
	url := fmt.Sprintf("%s/api/v1/incidents/%s/oie-result", m.alertHubBaseURL, inv.IncidentID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		m.logger.Warn("OIE→AlertHub writeback failed", "error", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 300 {
		m.logger.Info("OIE result written back to AlertHub",
			"incident_id", inv.IncidentID,
			"confidence", fmt.Sprintf("%.2f", *inv.Confidence),
			"band", derefString(inv.ConfidenceBand))
	}
}

func resolvePlaybookID(entityType *string, failureClass *string) string {
	et := derefString(entityType)
	fc := derefString(failureClass)
	switch {
	case et == "k8s_node" && fc == "NotReady":
		return "PB-K8S-001"
	case et == "k8s_pod" && fc == "CrashLoopBackOff":
		return "PB-K8S-002"
	case et == "k8s_pod" && (fc == "ImagePullBackOff" || fc == "ErrImagePull"):
		return "PB-K8S-003"
	case et == "k8s_pvc" && fc == "PVCFull":
		return "PB-K8S-004"
	case et == "k8s_pvc" && fc == "PVFailure":
		return "PB-K8S-005"
	case et == "virtual_machine":
		return "PB-INF-002"
	case et == "bare_metal":
		return "PB-INF-001"
	case et == "storage_volume":
		return "PB-INF-003"
	default:
		return "PB-DEFAULT-001"
	}
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, substr := range []string{"duplicate key", "unique constraint", "23505"} {
		if containsSubstring(msg, substr) {
			return true
		}
	}
	return false
}

func containsSubstring(s, sub string) bool {
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// safeJSONField ensures a []byte is valid JSON for PostgreSQL json/jsonb columns.
// If b is nil, empty, or contains only null bytes it returns []byte("null"),
// which is the canonical SQL NULL representation in JSON and avoids
// "pq: invalid input syntax for type json" errors.
func safeJSONField(b []byte) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage("null")
	}
	// Strip any embedded null bytes that would make the JSON unparseable.
	clean := bytes.ReplaceAll(b, []byte{0}, []byte{})
	if len(clean) == 0 || !json.Valid(clean) {
		return json.RawMessage("null")
	}
	return json.RawMessage(clean)
}
