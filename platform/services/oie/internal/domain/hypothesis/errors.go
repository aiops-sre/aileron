package hypothesis

import "fmt"

// ErrHypothesisNotFound is returned when a hypothesis cannot be located.
type ErrHypothesisNotFound struct {
	ID string
}

func (e ErrHypothesisNotFound) Error() string {
	return fmt.Sprintf("hypothesis not found: %s", e.ID)
}
