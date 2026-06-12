package evidence

import "fmt"

// ErrEvidenceNotFound is returned when an evidence record cannot be located.
type ErrEvidenceNotFound struct {
	ID string
}

func (e ErrEvidenceNotFound) Error() string {
	return fmt.Sprintf("evidence not found: %s", e.ID)
}
