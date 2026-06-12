package investigation

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Repository defines all persistence operations for the Investigation aggregate.
type Repository interface {
	Create(ctx context.Context, inv *Investigation) error
	GetByID(ctx context.Context, id uuid.UUID) (*Investigation, error)
	GetByIncidentID(ctx context.Context, incidentID uuid.UUID) (*Investigation, error)
	GetByIdempotencyKey(ctx context.Context, key string) (*Investigation, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, expectedCurrent Status, next Status, reason string) error
	Update(ctx context.Context, inv *Investigation) error
	ListActive(ctx context.Context) ([]*Investigation, error)
	ListOrphaned(ctx context.Context, staleAfter time.Duration) ([]*Investigation, error)
	AcquireLock(ctx context.Context, id uuid.UUID, holderID string, ttl time.Duration) (bool, error)
	RenewLock(ctx context.Context, id uuid.UUID, holderID string, ttl time.Duration) (bool, error)
	ReleaseLock(ctx context.Context, id uuid.UUID, holderID string) error
	SaveEvent(ctx context.Context, event *PersistedEvent) error
	GetEvents(ctx context.Context, investigationID uuid.UUID, afterSeq int64) ([]*PersistedEvent, error)
	UpsertRetryCount(ctx context.Context, messageKey string) (int, error)
	GetRetryCount(ctx context.Context, messageKey string) (int, error)
	DeleteRetryCount(ctx context.Context, messageKey string) error
}

// PersistedEvent is the storage model for investigation_events.
type PersistedEvent struct {
	ID              uuid.UUID
	InvestigationID uuid.UUID
	SequenceNum     int64
	EventType       string
	Payload         []byte
	CreatedAt       time.Time
}
