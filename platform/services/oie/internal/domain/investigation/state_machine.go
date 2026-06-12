package investigation

import "fmt"

type stateMachine struct {
	transitions map[Status]map[Status]struct{}
}

var defaultStateMachine = buildStateMachine()

func buildStateMachine() *stateMachine {
	sm := &stateMachine{transitions: make(map[Status]map[Status]struct{})}
	sm.register(StatusPending, StatusRunning)
	sm.register(StatusPending, StatusCancelled)
	sm.register(StatusRunning, StatusWaitingForEvidence)
	sm.register(StatusRunning, StatusFailed)
	sm.register(StatusRunning, StatusCancelled)
	sm.register(StatusWaitingForEvidence, StatusRCAGeneration)
	sm.register(StatusWaitingForEvidence, StatusFailed)
	sm.register(StatusWaitingForEvidence, StatusCancelled)
	sm.register(StatusRCAGeneration, StatusCompleted)
	sm.register(StatusRCAGeneration, StatusFailed)
	sm.register(StatusRCAGeneration, StatusCancelled)
	return sm
}

func (sm *stateMachine) register(from, to Status) {
	if sm.transitions[from] == nil {
		sm.transitions[from] = make(map[Status]struct{})
	}
	sm.transitions[from][to] = struct{}{}
}

// ValidateTransition returns nil if the (from → to) transition is permitted.
func (sm *stateMachine) ValidateTransition(from, to Status) error {
	if from == to {
		return ErrInvalidTransition{From: from, To: to, Reason: "self-transition not permitted"}
	}
	allowed, ok := sm.transitions[from]
	if !ok {
		return ErrInvalidTransition{
			From:   from,
			To:     to,
			Reason: fmt.Sprintf("status %q has no outgoing transitions (terminal state)", from),
		}
	}
	if _, ok := allowed[to]; !ok {
		return ErrInvalidTransition{
			From:   from,
			To:     to,
			Reason: fmt.Sprintf("transition %q → %q is not defined", from, to),
		}
	}
	return nil
}

// AllowedTransitions returns the set of valid next statuses from the given status.
func (sm *stateMachine) AllowedTransitions(from Status) []Status {
	allowed := sm.transitions[from]
	result := make([]Status, 0, len(allowed))
	for s := range allowed {
		result = append(result, s)
	}
	return result
}
