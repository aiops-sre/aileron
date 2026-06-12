package investigation

import (
	"time"

	"github.com/google/uuid"
)

// DomainEvent is implemented by all investigation domain events.
type DomainEvent interface {
	EventType() string
	AggregateID() uuid.UUID
	OccurredAt() time.Time
}

// InvestigationStartedEvent is raised when a new investigation is created.
type InvestigationStartedEvent struct {
	InvestigationID uuid.UUID
	IncidentID      uuid.UUID
	Severity        string
	EventTime       time.Time
}

func (e InvestigationStartedEvent) EventType() string      { return "investigation.started" }
func (e InvestigationStartedEvent) AggregateID() uuid.UUID { return e.InvestigationID }
func (e InvestigationStartedEvent) OccurredAt() time.Time  { return e.EventTime }

// StatusChangedEvent is raised on every state transition.
type StatusChangedEvent struct {
	InvestigationID uuid.UUID
	IncidentID      uuid.UUID
	PreviousStatus  Status
	CurrentStatus   Status
	Reason          string
	EventTime       time.Time
}

func (e StatusChangedEvent) EventType() string      { return "investigation.status_changed" }
func (e StatusChangedEvent) AggregateID() uuid.UUID { return e.InvestigationID }
func (e StatusChangedEvent) OccurredAt() time.Time  { return e.EventTime }

// RCACompletedEvent is raised when the final RCA report is ready.
type RCACompletedEvent struct {
	InvestigationID uuid.UUID
	IncidentID      uuid.UUID
	Confidence      float64
	ConfidenceBand  string
	ReportVersion   int
	EventTime       time.Time
}

func (e RCACompletedEvent) EventType() string      { return "investigation.rca_completed" }
func (e RCACompletedEvent) AggregateID() uuid.UUID { return e.InvestigationID }
func (e RCACompletedEvent) OccurredAt() time.Time  { return e.EventTime }

// ReportVersionUpdatedEvent is raised when late evidence triggers a re-score.
type ReportVersionUpdatedEvent struct {
	InvestigationID uuid.UUID
	IncidentID      uuid.UUID
	Version         int
	EventTime       time.Time
}

func (e ReportVersionUpdatedEvent) EventType() string      { return "investigation.report_updated" }
func (e ReportVersionUpdatedEvent) AggregateID() uuid.UUID { return e.InvestigationID }
func (e ReportVersionUpdatedEvent) OccurredAt() time.Time  { return e.EventTime }
