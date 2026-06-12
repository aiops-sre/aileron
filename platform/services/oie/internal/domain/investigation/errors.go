package investigation

import "fmt"

// ErrInvalidTransition is returned when a state transition is not permitted.
type ErrInvalidTransition struct {
	From   Status
	To     Status
	Reason string
}

func (e ErrInvalidTransition) Error() string {
	return fmt.Sprintf("invalid transition %q → %q: %s", e.From, e.To, e.Reason)
}

// ErrInvestigationNotFound is returned when an investigation cannot be located.
type ErrInvestigationNotFound struct {
	ID string
}

func (e ErrInvestigationNotFound) Error() string {
	if e.ID == "" {
		return "investigation not found"
	}
	return fmt.Sprintf("investigation not found: %s", e.ID)
}

// ErrDuplicateInvestigation is returned when an investigation for the same
// idempotency key already exists.
type ErrDuplicateInvestigation struct {
	IdempotencyKey string
	ExistingID     string
}

func (e ErrDuplicateInvestigation) Error() string {
	return fmt.Sprintf("investigation already exists for key %q (id: %s)", e.IdempotencyKey, e.ExistingID)
}

// ErrInvalidInput is returned when a field fails domain validation.
type ErrInvalidInput struct {
	Field  string
	Reason string
}

func (e ErrInvalidInput) Error() string {
	return fmt.Sprintf("invalid input: field %q %s", e.Field, e.Reason)
}

// ErrLockNotAcquired is returned when a distributed lock cannot be obtained.
type ErrLockNotAcquired struct {
	InvestigationID string
	HeldBy          string
}

func (e ErrLockNotAcquired) Error() string {
	return fmt.Sprintf("lock for investigation %s already held by %s", e.InvestigationID, e.HeldBy)
}
