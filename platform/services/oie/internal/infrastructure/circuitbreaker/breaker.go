package circuitbreaker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	failureThreshold = 3
	successThreshold = 2
	openDuration     = 60 * time.Second
	halfOpenDuration = 30 * time.Second
	localCacheTTL    = 5 * time.Second
)

type State string

const (
	StateClosed   State = "closed"
	StateOpen     State = "open"
	StateHalfOpen State = "half_open"
)

type localEntry struct {
	state     State
	expiresAt time.Time
}

// CircuitBreaker is shared across all concurrent investigations.
// Primary state lives in Redis (cross-pod coordination).
// Local map is a TTL cache to avoid Redis round-trip on every Allow() call.
// When Redis is unavailable, local state provides degraded protection.
type CircuitBreaker struct {
	redis  *redis.Client
	logger *slog.Logger

	localMu    sync.RWMutex
	localCache map[string]*localEntry
}

// NewCircuitBreaker constructs a CircuitBreaker.
// redis may be nil — the circuit breaker falls back to local-only mode.
func NewCircuitBreaker(redisClient *redis.Client, logger *slog.Logger) *CircuitBreaker {
	return &CircuitBreaker{
		redis:      redisClient,
		logger:     logger,
		localCache: make(map[string]*localEntry),
	}
}

// Allow returns true if the given source is allowed to be called.
func (cb *CircuitBreaker) Allow(sourceName string) bool {
	// Check local cache first (avoids Redis round-trip on hot path).
	cb.localMu.RLock()
	entry, ok := cb.localCache[sourceName]
	cb.localMu.RUnlock()

	if ok && time.Now().Before(entry.expiresAt) {
		return entry.state != StateOpen
	}

	// Cache miss or expired — query Redis.
	state := cb.getStateFromRedis(sourceName)

	// Update local cache.
	cb.localMu.Lock()
	cb.localCache[sourceName] = &localEntry{
		state:     state,
		expiresAt: time.Now().Add(localCacheTTL),
	}
	cb.localMu.Unlock()

	return state != StateOpen
}

// RecordSuccess records a successful call to a source.
func (cb *CircuitBreaker) RecordSuccess(sourceName string) {
	ctx := context.Background()

	if cb.redis == nil {
		cb.updateLocalOnSuccess(sourceName)
		return
	}

	stateKey := redisStateKey(sourceName)
	currentState, err := cb.redis.Get(ctx, stateKey).Result()
	if err != nil && err != redis.Nil {
		cb.updateLocalOnSuccess(sourceName)
		return
	}

	if State(currentState) == StateHalfOpen {
		succKey := redisSuccessKey(sourceName)
		count, _ := cb.redis.Incr(ctx, succKey).Result()
		cb.redis.Expire(ctx, succKey, halfOpenDuration)

		if count >= successThreshold {
			// Enough successes in half-open → close the circuit.
			cb.redis.Del(ctx, stateKey)
			cb.redis.Del(ctx, succKey)
			cb.invalidateLocalCache(sourceName)
			cb.logger.Info("circuit closed — source recovered", "source", sourceName)
		}
	}

	// Clear failure counter on any success.
	cb.redis.Del(ctx, redisFailureKey(sourceName))
	cb.updateLocalOnSuccess(sourceName)
}

// RecordFailure records a failed call to a source.
func (cb *CircuitBreaker) RecordFailure(sourceName string) {
	ctx := context.Background()

	if cb.redis == nil {
		cb.updateLocalOnFailure(sourceName)
		return
	}

	failKey := redisFailureKey(sourceName)
	count, err := cb.redis.Incr(ctx, failKey).Result()
	if err != nil {
		cb.updateLocalOnFailure(sourceName)
		return
	}
	cb.redis.Expire(ctx, failKey, 30*time.Second)

	if count >= failureThreshold {
		stateKey := redisStateKey(sourceName)
		cb.redis.Set(ctx, stateKey, string(StateOpen), openDuration)
		cb.redis.Del(ctx, failKey)
		cb.invalidateLocalCache(sourceName)
		cb.logger.Warn("circuit opened — source has repeated failures",
			"source", sourceName, "failures", count)
	}
}

func (cb *CircuitBreaker) getStateFromRedis(sourceName string) State {
	if cb.redis == nil {
		return cb.getLocalState(sourceName)
	}

	ctx := context.Background()
	stateKey := redisStateKey(sourceName)
	val, err := cb.redis.Get(ctx, stateKey).Result()
	if err == redis.Nil {
		return StateClosed
	}
	if err != nil {
		// Redis unavailable — fall back to local state.
		cb.logger.Warn("redis unavailable for circuit breaker — using local state",
			"source", sourceName, "error", err)
		return cb.getLocalState(sourceName)
	}

	state := State(val)

	// Check if OPEN should transition to HALF_OPEN based on TTL.
	if state == StateOpen {
		ttl, _ := cb.redis.TTL(ctx, stateKey).Result()
		if ttl < halfOpenDuration {
			cb.redis.Set(ctx, stateKey, string(StateHalfOpen), halfOpenDuration)
			return StateHalfOpen
		}
	}

	return state
}

// ── Local fallback state (used when Redis is unavailable) ────────────────────

type localFailureEntry struct {
	failures  int
	openUntil time.Time
}

var (
	localFailureMu    sync.Mutex
	localFailureState = make(map[string]*localFailureEntry)
)

func (cb *CircuitBreaker) getLocalState(sourceName string) State {
	localFailureMu.Lock()
	defer localFailureMu.Unlock()
	s, ok := localFailureState[sourceName]
	if !ok {
		return StateClosed
	}
	if s.failures >= failureThreshold && time.Now().Before(s.openUntil) {
		return StateOpen
	}
	return StateClosed
}

func (cb *CircuitBreaker) updateLocalOnFailure(sourceName string) {
	localFailureMu.Lock()
	defer localFailureMu.Unlock()
	s := localFailureState[sourceName]
	if s == nil {
		s = &localFailureEntry{}
		localFailureState[sourceName] = s
	}
	s.failures++
	if s.failures >= failureThreshold {
		s.openUntil = time.Now().Add(openDuration)
	}
	cb.invalidateLocalCache(sourceName)
}

func (cb *CircuitBreaker) updateLocalOnSuccess(sourceName string) {
	localFailureMu.Lock()
	defer localFailureMu.Unlock()
	delete(localFailureState, sourceName)
	cb.invalidateLocalCache(sourceName)
}

func (cb *CircuitBreaker) invalidateLocalCache(sourceName string) {
	cb.localMu.Lock()
	delete(cb.localCache, sourceName)
	cb.localMu.Unlock()
}

func redisStateKey(sourceName string) string {
	return fmt.Sprintf("oie:circuit:%s:state", sourceName)
}

func redisFailureKey(sourceName string) string {
	return fmt.Sprintf("oie:circuit:%s:failures", sourceName)
}

func redisSuccessKey(sourceName string) string {
	return fmt.Sprintf("oie:circuit:%s:successes", sourceName)
}
