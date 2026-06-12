package unit_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	domain "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/investigation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStateMachine_ValidTransitions(t *testing.T) {
	tests := []struct {
		from domain.Status
		to   domain.Status
	}{
		{domain.StatusPending, domain.StatusRunning},
		{domain.StatusPending, domain.StatusCancelled},
		{domain.StatusRunning, domain.StatusWaitingForEvidence},
		{domain.StatusRunning, domain.StatusFailed},
		{domain.StatusRunning, domain.StatusCancelled},
		{domain.StatusWaitingForEvidence, domain.StatusRCAGeneration},
		{domain.StatusWaitingForEvidence, domain.StatusFailed},
		{domain.StatusWaitingForEvidence, domain.StatusCancelled},
		{domain.StatusRCAGeneration, domain.StatusCompleted},
		{domain.StatusRCAGeneration, domain.StatusFailed},
		{domain.StatusRCAGeneration, domain.StatusCancelled},
	}

	for _, tc := range tests {
		t.Run(string(tc.from)+"→"+string(tc.to), func(t *testing.T) {
			inv := buildInvestigationAt(t, tc.from)
			err := inv.TransitionTo(tc.to, "test")
			require.NoError(t, err)
			assert.Equal(t, tc.to, inv.Status)
		})
	}
}

func TestStateMachine_InvalidTransitions(t *testing.T) {
	tests := []struct {
		from domain.Status
		to   domain.Status
	}{
		{domain.StatusPending, domain.StatusWaitingForEvidence},
		{domain.StatusPending, domain.StatusCompleted},
		{domain.StatusPending, domain.StatusFailed},
		{domain.StatusRunning, domain.StatusCompleted},
		{domain.StatusRunning, domain.StatusPending},
		{domain.StatusCompleted, domain.StatusRunning},
		{domain.StatusCompleted, domain.StatusFailed},
		{domain.StatusFailed, domain.StatusRunning},
		{domain.StatusCancelled, domain.StatusRunning},
	}

	for _, tc := range tests {
		t.Run(string(tc.from)+"→"+string(tc.to), func(t *testing.T) {
			inv := buildInvestigationAt(t, tc.from)
			err := inv.TransitionTo(tc.to, "invalid")
			require.Error(t, err)
			var transErr domain.ErrInvalidTransition
			assert.ErrorAs(t, err, &transErr)
			assert.Equal(t, tc.from, transErr.From)
			assert.Equal(t, tc.to, transErr.To)
		})
	}
}

func TestStateMachine_SelfTransitionRejected(t *testing.T) {
	inv := buildInvestigationAt(t, domain.StatusPending)
	err := inv.TransitionTo(domain.StatusPending, "self")
	require.Error(t, err)
	var transErr domain.ErrInvalidTransition
	assert.ErrorAs(t, err, &transErr)
}

func TestStateMachine_TerminalStates(t *testing.T) {
	assert.True(t, domain.StatusCompleted.IsTerminal())
	assert.True(t, domain.StatusFailed.IsTerminal())
	assert.True(t, domain.StatusCancelled.IsTerminal())
	assert.False(t, domain.StatusPending.IsTerminal())
	assert.False(t, domain.StatusRunning.IsTerminal())
	assert.False(t, domain.StatusWaitingForEvidence.IsTerminal())
	assert.False(t, domain.StatusRCAGeneration.IsTerminal())
}

func TestDomainEvents_RaisedOnTransition(t *testing.T) {
	// Build directly — do NOT use buildInvestigationAt which clears events.
	inv, err := domain.NewInvestigation(
		uuid.New(), "INC-001", uuid.NewString(), "",
		"high", time.Now().UTC(), 45000,
	)
	require.NoError(t, err)

	// Construction raises InvestigationStartedEvent.
	events := inv.PullDomainEvents()
	require.Len(t, events, 1)
	assert.Equal(t, "investigation.started", events[0].EventType())

	// Transition raises StatusChangedEvent.
	require.NoError(t, inv.TransitionTo(domain.StatusRunning, "test"))
	events = inv.PullDomainEvents()
	require.Len(t, events, 1)
	assert.Equal(t, "investigation.status_changed", events[0].EventType())

	// Pull clears the slice.
	assert.Empty(t, inv.PullDomainEvents())
}

func TestNewInvestigation_ValidationErrors(t *testing.T) {
	tests := []struct {
		name           string
		incidentID     uuid.UUID
		idempotencyKey string
		severity       string
		timeBudgetMs   int
		expectField    string
	}{
		{"nil incident id", uuid.Nil, "key", "high", 45000, "incident_id"},
		{"empty idempotency key", uuid.New(), "", "high", 45000, "idempotency_key"},
		{"empty severity", uuid.New(), "key", "", 45000, "severity"},
		{"zero budget", uuid.New(), "key", "high", 0, "time_budget_ms"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := domain.NewInvestigation(
				tc.incidentID, "INC-001", tc.idempotencyKey, "",
				tc.severity, time.Now().UTC(), tc.timeBudgetMs,
			)
			require.Error(t, err)
			var inputErr domain.ErrInvalidInput
			require.ErrorAs(t, err, &inputErr)
			assert.Equal(t, tc.expectField, inputErr.Field)
		})
	}
}

func TestInvestigation_SetEntityContext(t *testing.T) {
	inv := buildInvestigationAt(t, domain.StatusPending)
	require.Nil(t, inv.RootEntityType)

	entityID := uuid.New()
	inv.SetEntityContext(entityID, "k8s_node", "NotReady", "kubernetes", "PB-K8S-001")

	require.NotNil(t, inv.RootEntityID)
	assert.Equal(t, entityID, *inv.RootEntityID)
	assert.Equal(t, "k8s_node", *inv.RootEntityType)
	assert.Equal(t, "NotReady", *inv.FailureClass)
	assert.Equal(t, "PB-K8S-001", inv.PlaybookID)
}

func TestInvestigation_LateEvidenceWindow(t *testing.T) {
	inv := buildInvestigationAt(t, domain.StatusRCAGeneration)
	assert.False(t, inv.IsWithinLateEvidenceWindow())

	require.NoError(t, inv.TransitionTo(domain.StatusCompleted, "done"))
	assert.True(t, inv.IsWithinLateEvidenceWindow())
	assert.NotNil(t, inv.LateEvidenceWindowClosesAt)
}

func TestInvestigation_CompletedAt_SetOnTerminalTransition(t *testing.T) {
	// COMPLETED requires going through the full path.
	t.Run("COMPLETED", func(t *testing.T) {
		inv := buildInvestigationAt(t, domain.StatusRCAGeneration)
		require.NoError(t, inv.TransitionTo(domain.StatusCompleted, "test"))
		assert.NotNil(t, inv.CompletedAt)
		assert.NotNil(t, inv.ElapsedMs)
	})
	// FAILED and CANCELLED can happen from RUNNING directly.
	for _, terminal := range []domain.Status{domain.StatusFailed, domain.StatusCancelled} {
		status := terminal
		t.Run(string(status), func(t *testing.T) {
			inv := buildInvestigationAt(t, domain.StatusRunning)
			require.NoError(t, inv.TransitionTo(status, "test"))
			assert.NotNil(t, inv.CompletedAt)
			assert.NotNil(t, inv.ElapsedMs)
		})
	}
}

// ── Helpers ────────────────────────────────────────────────────────────────────

func buildInvestigationAt(t *testing.T, status domain.Status) *domain.Investigation {
	t.Helper()
	inv, err := domain.NewInvestigation(
		uuid.New(), "INC-001",
		uuid.NewString(), "",
		"high", time.Now().UTC(), 45000,
	)
	require.NoError(t, err)
	inv.PullDomainEvents() // Clear construction events.

	path := transitionPath(status)
	for _, next := range path {
		require.NoError(t, inv.TransitionTo(next, "setup"))
		inv.PullDomainEvents()
	}
	return inv
}

func transitionPath(target domain.Status) []domain.Status {
	paths := map[domain.Status][]domain.Status{
		domain.StatusRunning:             {domain.StatusRunning},
		domain.StatusWaitingForEvidence:  {domain.StatusRunning, domain.StatusWaitingForEvidence},
		domain.StatusRCAGeneration:       {domain.StatusRunning, domain.StatusWaitingForEvidence, domain.StatusRCAGeneration},
		domain.StatusCompleted:           {domain.StatusRunning, domain.StatusWaitingForEvidence, domain.StatusRCAGeneration, domain.StatusCompleted},
		domain.StatusFailed:              {domain.StatusRunning, domain.StatusFailed},
		domain.StatusCancelled:           {domain.StatusCancelled},
		domain.StatusPending:             {},
	}
	return paths[target]
}
