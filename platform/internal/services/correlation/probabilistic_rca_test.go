package correlation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// ---------------------------------------------------------------------------
// hypothesisKey — pure logic, no Redis needed
// ---------------------------------------------------------------------------

func TestHypothesisKey_Format(t *testing.T) {
	incID := "550e8400-e29b-41d4-a716-446655440000"
	entityID := "redis-cluster"
	key := hypothesisKey(incID, entityID)

	want := "rca:hyp:550e8400-e29b-41d4-a716-446655440000:redis-cluster"
	if key != want {
		t.Errorf("hypothesisKey = %q, want %q", key, want)
	}

	// Must have exactly 4 colon-separated segments so scoped SCAN works.
	parts := strings.SplitN(key, ":", 5)
	if len(parts) != 4 {
		t.Errorf("hypothesisKey has %d colon segments, want 4 (rca:hyp:{incID}:{entityID})", len(parts))
	}
	if parts[0] != "rca" || parts[1] != "hyp" {
		t.Errorf("key prefix wrong: %q", key)
	}
}

func TestHypothesisKey_DifferentIncidents_DifferentKeys(t *testing.T) {
	incA := uuid.New().String()
	incB := uuid.New().String()
	entity := "host-1"

	kA := hypothesisKey(incA, entity)
	kB := hypothesisKey(incB, entity)

	if kA == kB {
		t.Errorf("same entity under different incidents produced identical keys: %q", kA)
	}
	if !strings.HasPrefix(kA, "rca:hyp:"+incA+":") {
		t.Errorf("key %q does not start with expected prefix rca:hyp:%s:", kA, incA)
	}
}

func TestHypothesisKeyUniqueness(t *testing.T) {
	incID := uuid.New().String()
	entities := []string{"bare-metal-1", "kvm-host-2", "vm-3", "k8s-node-4", "redis-cache"}
	keys := make(map[string]bool)
	for _, e := range entities {
		k := hypothesisKey(incID, e)
		if keys[k] {
			t.Errorf("duplicate key %q for entity %q", k, e)
		}
		keys[k] = true
	}
}

// ---------------------------------------------------------------------------
// hypothesisKeyTTL — pure logic
// ---------------------------------------------------------------------------

func TestHypothesisKeyTTL_Is72Hours(t *testing.T) {
	if hypothesisKeyTTL != 72*time.Hour {
		t.Errorf("hypothesisKeyTTL = %v, want 72h — incidents can be open longer than 2h", hypothesisKeyTTL)
	}
}

// ---------------------------------------------------------------------------
// Guard tests — verify early-return paths with nil rdb (no Redis call).
// If a guard is missing the test panics on nil pointer dereference.
// ---------------------------------------------------------------------------

func TestPersistHypotheses_NilIncidentID_NoRedisCall(t *testing.T) {
	// nil rdb: any Redis call panics. The nil-incidentID guard must fire first.
	p := &ProbabilisticRCAEngine{rdb: nil}
	ctx := context.Background()

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panicked on nil incidentID — nil guard is missing: %v", r)
		}
	}()
	p.persistHypotheses(ctx, uuid.Nil, []*RCAHypothesis{
		{EntityID: "some-entity", RawConfidence: 0.8},
	})
}

func TestPersistHypotheses_EmptyEntityID_NoRedisCall(t *testing.T) {
	// nil rdb: any Redis call panics. The empty-EntityID guard must fire first.
	p := &ProbabilisticRCAEngine{rdb: nil}
	ctx := context.Background()

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panicked on empty EntityID — guard is missing: %v", r)
		}
	}()
	p.persistHypotheses(ctx, uuid.New(), []*RCAHypothesis{
		{EntityID: "", RawConfidence: 0.9},
	})
}

func TestLoadPriorHypotheses_NilIncidentID_NoRedisCall(t *testing.T) {
	// nil rdb: any Redis call panics. The nil-incidentID guard must fire first.
	p := &ProbabilisticRCAEngine{rdb: nil}
	ctx := context.Background()

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panicked on nil incidentID — guard is missing: %v", r)
		}
	}()

	loaded, err := p.loadPriorHypotheses(ctx, uuid.Nil)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if loaded != nil {
		t.Errorf("expected nil slice, got %v", loaded)
	}
}

// ---------------------------------------------------------------------------
// Redis integration tests — require TEST_REDIS_URL=host:port
// Run with: TEST_REDIS_URL=localhost:6379 go test ./... -run TestRedis
// ---------------------------------------------------------------------------

func requireTestRedis(t *testing.T) *ProbabilisticRCAEngine {
	t.Helper()
	addr := os.Getenv("TEST_REDIS_URL")
	if addr == "" {
		t.Skip("skipping Redis integration test: set TEST_REDIS_URL=host:port to enable")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	// Verify connectivity.
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Skipf("skipping: Redis at %s unreachable: %v", addr, err)
	}
	return &ProbabilisticRCAEngine{rdb: rdb}
}

func cleanupIncidentKeys(t *testing.T, p *ProbabilisticRCAEngine, incID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	pattern := fmt.Sprintf("rca:hyp:%s:*", incID.String())
	var cursor uint64
	for {
		keys, next, err := p.rdb.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			break
		}
		if len(keys) > 0 {
			p.rdb.Del(ctx, keys...)
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
}

func TestRedis_PersistHypotheses_OneKeyPerEntity(t *testing.T) {
	p := requireTestRedis(t)
	ctx := context.Background()
	incID := uuid.New()
	t.Cleanup(func() { cleanupIncidentKeys(t, p, incID) })

	hyps := []*RCAHypothesis{
		{EntityID: "entity-A", RawConfidence: 0.7},
		{EntityID: "entity-B", RawConfidence: 0.5},
	}
	p.persistHypotheses(ctx, incID, hyps)

	for _, tc := range []struct{ entity string; wantConf float64 }{
		{"entity-A", 0.7},
		{"entity-B", 0.5},
	} {
		key := hypothesisKey(incID.String(), tc.entity)
		val, err := p.rdb.Get(ctx, key).Result()
		if err != nil {
			t.Errorf("key %q not found: %v", key, err)
			continue
		}
		// Each value must be a single RCAHypothesis (not an array).
		var h RCAHypothesis
		if err := json.Unmarshal([]byte(val), &h); err != nil {
			t.Errorf("unmarshal %q: %v", key, err)
			continue
		}
		if h.EntityID != tc.entity {
			t.Errorf("EntityID = %q, want %q", h.EntityID, tc.entity)
		}
	}
}

func TestRedis_LoadPriorHypotheses_SameIncident(t *testing.T) {
	p := requireTestRedis(t)
	ctx := context.Background()
	incID := uuid.New()
	t.Cleanup(func() { cleanupIncidentKeys(t, p, incID) })

	p.persistHypotheses(ctx, incID, []*RCAHypothesis{
		{EntityID: "postgres-primary", RawConfidence: 0.75},
	})

	loaded, err := p.loadPriorHypotheses(ctx, incID)
	if err != nil {
		t.Fatalf("loadPriorHypotheses: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d hypotheses, want 1", len(loaded))
	}
	if loaded[0].EntityID != "postgres-primary" {
		t.Errorf("EntityID = %q, want postgres-primary", loaded[0].EntityID)
	}
}

// TestRedis_CrossIncidentIsolation is the core regression test for the contamination bug.
// It must pass for every future change to this file.
func TestRedis_CrossIncidentIsolation(t *testing.T) {
	p := requireTestRedis(t)
	ctx := context.Background()
	incidentA := uuid.New()
	incidentB := uuid.New()
	t.Cleanup(func() {
		cleanupIncidentKeys(t, p, incidentA)
		cleanupIncidentKeys(t, p, incidentB)
	})

	// High-confidence hypothesis stored under incident A.
	p.persistHypotheses(ctx, incidentA, []*RCAHypothesis{
		{EntityID: "redis-cluster", RawConfidence: 0.95},
	})

	// Incident B must see zero hypotheses — the contamination bug returns them.
	loadedB, err := p.loadPriorHypotheses(ctx, incidentB)
	if err != nil {
		t.Fatalf("loadPriorHypotheses incidentB: %v", err)
	}
	if len(loadedB) != 0 {
		t.Errorf("CROSS-INCIDENT CONTAMINATION: incidentB loaded %d hypotheses from incidentA",
			len(loadedB))
		for _, h := range loadedB {
			t.Logf("  leaked: EntityID=%s RawConfidence=%.3f", h.EntityID, h.RawConfidence)
		}
	}

	// Incident A still sees its own hypothesis.
	loadedA, err := p.loadPriorHypotheses(ctx, incidentA)
	if err != nil {
		t.Fatalf("loadPriorHypotheses incidentA: %v", err)
	}
	if len(loadedA) != 1 || loadedA[0].EntityID != "redis-cluster" {
		t.Errorf("incidentA lost its own hypothesis: loaded=%v", loadedA)
	}
}

func TestRedis_PersistHypotheses_NilIncident_WritesNothing(t *testing.T) {
	p := requireTestRedis(t)
	ctx := context.Background()

	before, _ := p.rdb.DBSize(ctx).Result()
	p.persistHypotheses(ctx, uuid.Nil, []*RCAHypothesis{
		{EntityID: "some-entity", RawConfidence: 0.8},
	})
	after, _ := p.rdb.DBSize(ctx).Result()

	if after-before != 0 {
		t.Errorf("nil incidentID wrote %d keys, want 0", after-before)
	}
}

func TestRedis_LoadPriorHypotheses_MultipleEntities(t *testing.T) {
	p := requireTestRedis(t)
	ctx := context.Background()
	incID := uuid.New()
	t.Cleanup(func() { cleanupIncidentKeys(t, p, incID) })

	entities := []string{"host-1", "vm-2", "pod-3", "redis-4"}
	var hyps []*RCAHypothesis
	for _, e := range entities {
		hyps = append(hyps, &RCAHypothesis{EntityID: e, RawConfidence: 0.6})
	}
	p.persistHypotheses(ctx, incID, hyps)

	loaded, err := p.loadPriorHypotheses(ctx, incID)
	if err != nil {
		t.Fatalf("loadPriorHypotheses: %v", err)
	}
	if len(loaded) != len(entities) {
		t.Errorf("loaded %d hypotheses, want %d", len(loaded), len(entities))
	}
	loadedIDs := make(map[string]bool)
	for _, h := range loaded {
		loadedIDs[h.EntityID] = true
	}
	for _, e := range entities {
		if !loadedIDs[e] {
			t.Errorf("entity %q missing from loaded hypotheses", e)
		}
	}
}
