//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	domain "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/investigation"
	pgInvestigation "github.com/aileron-platform/aileron/platform/services/oie/internal/infrastructure/postgres/investigation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping integration test")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	require.NoError(t, db.Ping())
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestRepository_CreateAndGetByID(t *testing.T) {
	db := openTestDB(t)
	repo := pgInvestigation.NewRepository(db)
	ctx := context.Background()

	inv, err := domain.NewInvestigation(
		uuid.New(), "INC-001", uuid.NewString(), "", "critical",
		time.Now().UTC(), 45000,
	)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, inv))

	fetched, err := repo.GetByID(ctx, inv.ID)
	require.NoError(t, err)
	assert.Equal(t, inv.ID, fetched.ID)
	assert.Equal(t, inv.IncidentID, fetched.IncidentID)
	assert.Equal(t, domain.StatusPending, fetched.Status)
	assert.Equal(t, "critical", fetched.Severity)
}

func TestRepository_Create_DuplicateIdempotencyKey(t *testing.T) {
	db := openTestDB(t)
	repo := pgInvestigation.NewRepository(db)
	ctx := context.Background()

	key := uuid.NewString()
	incidentID := uuid.New()

	inv1, err := domain.NewInvestigation(incidentID, "INC-002", key, "", "high", time.Now().UTC(), 45000)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, inv1))

	inv2, err := domain.NewInvestigation(incidentID, "INC-002", key, "", "high", time.Now().UTC(), 45000)
	require.NoError(t, err)

	err = repo.Create(ctx, inv2)
	require.Error(t, err)
	var dupErr domain.ErrDuplicateInvestigation
	assert.ErrorAs(t, err, &dupErr)
}

func TestRepository_UpdateStatus_OptimisticLocking(t *testing.T) {
	db := openTestDB(t)
	repo := pgInvestigation.NewRepository(db)
	ctx := context.Background()

	inv, _ := domain.NewInvestigation(uuid.New(), "INC-003", uuid.NewString(), "", "high", time.Now().UTC(), 45000)
	require.NoError(t, repo.Create(ctx, inv))

	// Valid transition.
	err := repo.UpdateStatus(ctx, inv.ID, domain.StatusPending, domain.StatusRunning, "started")
	require.NoError(t, err)

	// Wrong expected current status — optimistic lock fails.
	err = repo.UpdateStatus(ctx, inv.ID, domain.StatusPending, domain.StatusRunning, "duplicate")
	require.Error(t, err)
	var transErr domain.ErrInvalidTransition
	assert.ErrorAs(t, err, &transErr)
}

func TestRepository_AcquireRenewReleaseLock(t *testing.T) {
	db := openTestDB(t)
	repo := pgInvestigation.NewRepository(db)
	ctx := context.Background()

	inv, _ := domain.NewInvestigation(uuid.New(), "INC-004", uuid.NewString(), "", "high", time.Now().UTC(), 45000)
	require.NoError(t, repo.Create(ctx, inv))

	acquired, err := repo.AcquireLock(ctx, inv.ID, "pod-a", 2*time.Minute)
	require.NoError(t, err)
	assert.True(t, acquired)

	acquired2, err := repo.AcquireLock(ctx, inv.ID, "pod-b", 2*time.Minute)
	require.NoError(t, err)
	assert.False(t, acquired2, "pod-b must not steal lock held by pod-a")

	renewed, err := repo.RenewLock(ctx, inv.ID, "pod-a", 2*time.Minute)
	require.NoError(t, err)
	assert.True(t, renewed)

	renewed2, err := repo.RenewLock(ctx, inv.ID, "pod-b", 2*time.Minute)
	require.NoError(t, err)
	assert.False(t, renewed2)

	require.NoError(t, repo.ReleaseLock(ctx, inv.ID, "pod-a"))

	acquired3, err := repo.AcquireLock(ctx, inv.ID, "pod-b", 2*time.Minute)
	require.NoError(t, err)
	assert.True(t, acquired3)
}

func TestRepository_RetryCount(t *testing.T) {
	db := openTestDB(t)
	repo := pgInvestigation.NewRepository(db)
	ctx := context.Background()

	key := "kafka:test:" + uuid.NewString()

	count, err := repo.GetRetryCount(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	for i := 1; i <= 3; i++ {
		n, err := repo.UpsertRetryCount(ctx, key)
		require.NoError(t, err)
		assert.Equal(t, i, n)
	}

	require.NoError(t, repo.DeleteRetryCount(ctx, key))
	count, err = repo.GetRetryCount(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestRepository_GetByIdempotencyKey_NotFound(t *testing.T) {
	db := openTestDB(t)
	repo := pgInvestigation.NewRepository(db)
	ctx := context.Background()

	_, err := repo.GetByIdempotencyKey(ctx, "nonexistent-"+uuid.NewString())
	require.Error(t, err)
	var notFoundErr domain.ErrInvestigationNotFound
	assert.ErrorAs(t, err, &notFoundErr)
}

func TestRepository_ListOrphaned(t *testing.T) {
	db := openTestDB(t)
	repo := pgInvestigation.NewRepository(db)
	ctx := context.Background()

	inv, _ := domain.NewInvestigation(uuid.New(), "INC-005", uuid.NewString(), "", "high", time.Now().UTC(), 45000)
	require.NoError(t, repo.Create(ctx, inv))

	// Force orphaned state.
	_, err := db.ExecContext(ctx, `
		UPDATE investigations
		SET triggered_at='`+time.Now().UTC().Add(-10*time.Minute).Format(time.RFC3339)+`',
		    status='RUNNING',
		    lock_expires_at='`+time.Now().UTC().Add(-5*time.Minute).Format(time.RFC3339)+`'
		WHERE id=$1`, inv.ID)
	require.NoError(t, err)

	orphans, err := repo.ListOrphaned(ctx, 3*time.Minute)
	require.NoError(t, err)

	found := false
	for _, o := range orphans {
		if o.ID == inv.ID {
			found = true
			break
		}
	}
	assert.True(t, found, "orphaned investigation should be listed")
}
