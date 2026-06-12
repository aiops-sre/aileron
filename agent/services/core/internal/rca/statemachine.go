// Package rca — Algorithm 5: Alert State Machine with flap detection and hysteresis.
//
// Implements a per-(entityID, metric) state machine that suppresses noise via:
//   - Pending hysteresis: anomaly must persist >= PendingFor before firing.
//   - Resolved hysteresis: quiet period >= ResolvedFor before returning to OK.
//   - Flap detection: FlapCount > FlapThreshold within FlapWindow → Suppressed.
package rca

import (
	"fmt"
	"sync"
	"time"
)

// AlertState represents the lifecycle state of an alert for a given entity/metric.
type AlertState int

const (
	AlertStateOK         AlertState = iota // No anomaly; baseline quiet.
	AlertStatePending                      // Anomaly observed; waiting for PendingFor to elapse.
	AlertStateFiring                       // Anomaly confirmed and active.
	AlertStateResolved                     // Anomaly cleared; waiting for ResolvedFor to elapse.
	AlertStateSuppressed                   // Flap rate exceeded FlapThreshold; transitions muted.
)

func (s AlertState) String() string {
	switch s {
	case AlertStateOK:
		return "OK"
	case AlertStatePending:
		return "Pending"
	case AlertStateFiring:
		return "Firing"
	case AlertStateResolved:
		return "Resolved"
	case AlertStateSuppressed:
		return "Suppressed"
	default:
		return "Unknown"
	}
}

// TransitionType classifies what kind of state change just occurred.
type TransitionType int

const (
	TransitionNone     TransitionType = iota // No externally significant transition.
	TransitionFire                           // OK/Pending → Firing (first fire or after CLEAR).
	TransitionRefire                         // Resolved → Firing (re-anomaly within quiet window).
	TransitionResolve                        // Firing → Resolved.
	TransitionSuppress                       // Pending → Suppressed (flap rate exceeded).
	TransitionClear                          // Resolved → OK (quiet period satisfied).
)

func (t TransitionType) String() string {
	switch t {
	case TransitionNone:
		return "NONE"
	case TransitionFire:
		return "FIRE"
	case TransitionRefire:
		return "REFIRE"
	case TransitionResolve:
		return "RESOLVE"
	case TransitionSuppress:
		return "SUPPRESS"
	case TransitionClear:
		return "CLEAR"
	default:
		return "UNKNOWN"
	}
}

// AlertTransition is the result of a single Evaluate call when a noteworthy
// state change occurs.
type AlertTransition struct {
	Type       TransitionType
	FiredAt    time.Time
	ResolvedAt time.Time
	FlapCount  int
}

// Default tuning constants.
const (
	defaultPendingFor    = 2 * time.Minute
	defaultResolvedFor   = 5 * time.Minute
	defaultFlapThreshold = 4
	defaultFlapWindow    = 15 * time.Minute
)

// AlertStateMachine tracks the alert lifecycle for a single (entityID, metric)
// pair. It is NOT safe for concurrent use; callers must synchronize externally
// or use StateMachineRegistry which holds a per-machine mutex.
type AlertStateMachine struct {
	EntityID string
	Metric   string

	State AlertState

	// Hysteresis durations.
	PendingFor  time.Duration
	ResolvedFor time.Duration

	// Flap detection.
	FlapThreshold  int
	FlapWindow     time.Duration
	FlapWindowStart time.Time
	FlapCount      int

	// Timing.
	StateChangedAt time.Time

	// Internal bookkeeping: time of most recent FIRE for transition metadata.
	lastFiredAt time.Time
}

// NewAlertStateMachine creates a machine with production-tuned defaults.
func NewAlertStateMachine(entityID, metric string) *AlertStateMachine {
	return &AlertStateMachine{
		EntityID:      entityID,
		Metric:        metric,
		State:         AlertStateOK,
		PendingFor:    defaultPendingFor,
		ResolvedFor:   defaultResolvedFor,
		FlapThreshold: defaultFlapThreshold,
		FlapWindow:    defaultFlapWindow,
	}
}

// Evaluate advances the state machine given whether an anomaly is currently
// detected at time now. It returns a non-nil *AlertTransition only when the
// transition has external significance (FIRE, REFIRE, RESOLVE, SUPPRESS,
// CLEAR). Returning nil (or TransitionNone) means "no action needed".
func (m *AlertStateMachine) Evaluate(isAnomaly bool, now time.Time) *AlertTransition {
	switch m.State {

	// ── OK ────────────────────────────────────────────────────────────────────
	case AlertStateOK:
		if isAnomaly {
			m.State = AlertStatePending
			m.StateChangedAt = now
		}
		return nil

	// ── Pending ───────────────────────────────────────────────────────────────
	case AlertStatePending:
		if !isAnomaly {
			// Anomaly cleared before the pending window elapsed — back to quiet.
			m.State = AlertStateOK
			m.StateChangedAt = now
			return nil
		}
		// Anomaly persists; check whether PendingFor has elapsed.
		if now.Sub(m.StateChangedAt) < m.PendingFor {
			return nil // Still within pending window; wait.
		}
		// PendingFor elapsed — check flap rate before firing.
		m.incrementFlapCount(now)
		if m.FlapCount > m.FlapThreshold {
			m.State = AlertStateSuppressed
			m.StateChangedAt = now
			return &AlertTransition{
				Type:      TransitionSuppress,
				FiredAt:   now,
				FlapCount: m.FlapCount,
			}
		}
		// Confirmed fire.
		m.State = AlertStateFiring
		m.StateChangedAt = now
		m.lastFiredAt = now
		return &AlertTransition{
			Type:      TransitionFire,
			FiredAt:   now,
			FlapCount: m.FlapCount,
		}

	// ── Firing ────────────────────────────────────────────────────────────────
	case AlertStateFiring:
		if !isAnomaly {
			m.State = AlertStateResolved
			m.StateChangedAt = now
			return &AlertTransition{
				Type:       TransitionResolve,
				FiredAt:    m.lastFiredAt,
				ResolvedAt: now,
				FlapCount:  m.FlapCount,
			}
		}
		return nil // Still firing; no new transition.

	// ── Resolved ──────────────────────────────────────────────────────────────
	case AlertStateResolved:
		if isAnomaly {
			// Re-anomaly before quiet window: REFIRE immediately (no pending wait).
			m.incrementFlapCount(now)
			m.State = AlertStateFiring
			m.StateChangedAt = now
			m.lastFiredAt = now
			return &AlertTransition{
				Type:      TransitionRefire,
				FiredAt:   now,
				FlapCount: m.FlapCount,
			}
		}
		// Quiet: check whether ResolvedFor has elapsed.
		if now.Sub(m.StateChangedAt) >= m.ResolvedFor {
			m.State = AlertStateOK
			m.StateChangedAt = now
			return &AlertTransition{
				Type:       TransitionClear,
				ResolvedAt: now,
				FlapCount:  m.FlapCount,
			}
		}
		return nil

	// ── Suppressed ────────────────────────────────────────────────────────────
	case AlertStateSuppressed:
		// Decay flap count at FlapWindow boundary.
		if !m.FlapWindowStart.IsZero() && now.Sub(m.FlapWindowStart) >= m.FlapWindow {
			m.FlapCount = 0
			m.FlapWindowStart = time.Time{}
		}
		if !isAnomaly && m.FlapCount == 0 {
			m.State = AlertStateOK
			m.StateChangedAt = now
		}
		return nil
	}

	return nil
}

// incrementFlapCount advances the flap counter, resetting the window when it
// has expired.
func (m *AlertStateMachine) incrementFlapCount(now time.Time) {
	if m.FlapWindowStart.IsZero() || now.Sub(m.FlapWindowStart) >= m.FlapWindow {
		// Start a fresh flap window.
		m.FlapWindowStart = now
		m.FlapCount = 0
	}
	m.FlapCount++
}

// ── Registry ──────────────────────────────────────────────────────────────────

// StateMachineRegistry manages a collection of AlertStateMachine instances
// keyed by "entityID:metric". It is safe for concurrent use.
type StateMachineRegistry struct {
	mu       sync.Mutex
	machines map[string]*AlertStateMachine
}

// NewStateMachineRegistry creates an empty, ready-to-use registry.
func NewStateMachineRegistry() *StateMachineRegistry {
	return &StateMachineRegistry{
		machines: make(map[string]*AlertStateMachine),
	}
}

func registryKey(entityID, metric string) string {
	return fmt.Sprintf("%s:%s", entityID, metric)
}

// Evaluate retrieves (or lazily creates) the machine for (entityID, metric)
// and delegates to AlertStateMachine.Evaluate.
func (r *StateMachineRegistry) Evaluate(entityID, metric string, isAnomaly bool, now time.Time) *AlertTransition {
	key := registryKey(entityID, metric)
	r.mu.Lock()
	m, ok := r.machines[key]
	if !ok {
		m = NewAlertStateMachine(entityID, metric)
		r.machines[key] = m
	}
	r.mu.Unlock()

	// Per-machine operations are serialised via the registry mutex above; for
	// high-throughput registries a per-machine mutex would be preferable, but
	// correctness is identical either way.
	r.mu.Lock()
	defer r.mu.Unlock()
	return m.Evaluate(isAnomaly, now)
}

// Prune removes machines that have been idle (in OK state with no flap history)
// for longer than maxIdleAge.
func (r *StateMachineRegistry) Prune(maxIdleAge time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for key, m := range r.machines {
		idle := m.State == AlertStateOK &&
			m.FlapCount == 0 &&
			!m.StateChangedAt.IsZero() &&
			now.Sub(m.StateChangedAt) > maxIdleAge
		if idle {
			delete(r.machines, key)
		}
	}
}

// ActiveFiringCount returns the number of machines currently in the Firing state.
func (r *StateMachineRegistry) ActiveFiringCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, m := range r.machines {
		if m.State == AlertStateFiring {
			n++
		}
	}
	return n
}
