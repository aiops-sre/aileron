package correlation

// alert_state_machine.go
//
// Guarantees that every alert is accounted for — no alert is silently dropped.
// Every alert processed by the pipeline MUST end up with one of:
//   - IncidentID  (ATTACHED or INCIDENT_CREATED)
//   - BufferID    (BUFFERED — held for burst/evolution window)
//   - SuppressedReason (SUPPRESSED — downstream of a detected root cause)
//   - DEDUPED     (exact duplicate, counter incremented on existing record)
//
// State is persisted in Redis with a 2-hour TTL.
// The AlertBuffer also provides deduplication keyed by entity_id + metric_type.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// AlertState 

// AlertState tracks the correlation lifecycle of a single alert.
type AlertState string

const (
	AlertStateNew             AlertState = "NEW"              // received, not yet processed
	AlertStateDeduped         AlertState = "DEDUPED"          // exact duplicate; counter incremented
	AlertStateBuffered        AlertState = "BUFFERED"         // held pending more signals
	AlertStateAttached        AlertState = "ATTACHED"         // merged into an existing incident
	AlertStateSuppressed      AlertState = "SUPPRESSED"       // downstream of a detected root cause
	AlertStateIncidentCreated AlertState = "INCIDENT_CREATED" // created its own incident
)

// DeterministicResult 

// DeterministicResult is the guaranteed output per alert.
// Exactly one of IncidentID, BufferID, or SuppressedReason will be non-nil
// (or State==DEDUPED for exact duplicates).
type DeterministicResult struct {
	AlertID          uuid.UUID         `json:"alert_id"`
	State            AlertState        `json:"state"`
	Action           string            `json:"action"` // CREATE | MERGE | BUFFER | SUPPRESS | DEDUPE
	IncidentID       *uuid.UUID        `json:"incident_id,omitempty"`
	BufferID         *string           `json:"buffer_id,omitempty"`
	SuppressedReason *SuppressedReason `json:"suppressed_reason,omitempty"`
	Reason           string            `json:"reason"`
}

// AlertStateRecord 

// AlertStateRecord is the per-alert record stored in Redis.
type AlertStateRecord struct {
	AlertID    uuid.UUID  `json:"alert_id"`
	State      AlertState `json:"state"`
	IncidentID *uuid.UUID `json:"incident_id,omitempty"`
	BufferID   *string    `json:"buffer_id,omitempty"`
	Reason     string     `json:"reason"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// Redis key constants 

const (
	alertStateKeyPrefix = "alert:state:"
	bufferKeyPrefix     = "alert:buffer:"
	dedupKeyPrefix      = "alert:dedup:"
	defaultBufferTTL    = 30 * time.Minute
	defaultStateTTL     = 2 * time.Hour
)

// AlertBuffer 

// AlertBuffer manages alert state, deduplication, and burst buffering via Redis.
// All methods are no-op safe when Redis is nil (degrades to stateless operation).
type AlertBuffer struct {
	redis     *redis.Client
	bufferTTL time.Duration
	stateTTL  time.Duration
}

// NewAlertBuffer creates an AlertBuffer backed by Redis.
func NewAlertBuffer(r *redis.Client) *AlertBuffer {
	return &AlertBuffer{
		redis:     r,
		bufferTTL: defaultBufferTTL,
		stateTTL:  defaultStateTTL,
	}
}

// SetAlertState persists the current correlation state for an alert.
func (ab *AlertBuffer) SetAlertState(ctx context.Context, record AlertStateRecord) error {
	if ab.redis == nil {
		return nil
	}
	record.UpdatedAt = time.Now()
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return ab.redis.Set(ctx, alertStateKeyPrefix+record.AlertID.String(), data, ab.stateTTL).Err()
}

// GetAlertState retrieves the current correlation state for an alert.
// Returns (nil, nil) when the key does not exist or Redis is nil.
func (ab *AlertBuffer) GetAlertState(ctx context.Context, alertID uuid.UUID) (*AlertStateRecord, error) {
	if ab.redis == nil {
		return nil, nil
	}
	data, err := ab.redis.Get(ctx, alertStateKeyPrefix+alertID.String()).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record AlertStateRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

// DeduplicationKey returns a stable Redis key for an alert scoped by cluster + namespace + entity + metric.
// Including cluster and namespace prevents cross-cluster false deduplication when different clusters
// share the same entity name or entity ID (e.g., both have a node named "worker-1").
func DeduplicationKey(alert *Alert) string {
	entityID := alertEntityID(alert)
	metricType := alertMetricType(alert)
	if entityID == "" {
		entityID = alert.Source + ":" + alert.Fingerprint
	}

	cluster := ""
	namespace := ""
	if alert.Labels != nil {
		for _, k := range []string{"cluster", "k8s.cluster.name", "kubernetes_cluster"} {
			if v, ok := alert.Labels[k]; ok && v != "" {
				cluster = v
				break
			}
		}
		for _, k := range []string{"namespace", "k8s.namespace.name", "kubernetes_namespace"} {
			if v, ok := alert.Labels[k]; ok && v != "" {
				namespace = v
				break
			}
		}
	}

	var key strings.Builder
	key.WriteString(dedupKeyPrefix)
	if cluster != "" {
		key.WriteString(cluster)
		key.WriteByte(':')
	}
	if namespace != "" {
		key.WriteString(namespace)
		key.WriteByte(':')
	}
	key.WriteString(entityID)
	key.WriteByte(':')
	key.WriteString(metricType)
	return key.String()
}

// IncrementDedup atomically increments the dedup counter for an alert.
// Returns the new count; count > 1 means it is a duplicate.
func (ab *AlertBuffer) IncrementDedup(ctx context.Context, alert *Alert) (int64, error) {
	if ab.redis == nil {
		return 1, nil
	}
	key := DeduplicationKey(alert)
	count, err := ab.redis.Incr(ctx, key).Result()
	if err != nil {
		return 1, err
	}
	if count == 1 {
		// Set TTL only on first occurrence so the window resets when the alert stops firing.
		ab.redis.Expire(ctx, key, defaultBufferTTL)
	}
	return count, nil
}

// BufferAlert adds an alert to a named buffer group for burst/evolution windowing.
// bufferKey is typically entity_id or cluster/domain combination.
// Up to bufferTTL (30 min) of alerts are retained per group.
func (ab *AlertBuffer) BufferAlert(ctx context.Context, alert *Alert, bufferKey string) error {
	if ab.redis == nil {
		return nil
	}
	data, err := json.Marshal(alert)
	if err != nil {
		return err
	}
	pipe := ab.redis.Pipeline()
	pipe.RPush(ctx, bufferKeyPrefix+bufferKey, data)
	pipe.Expire(ctx, bufferKeyPrefix+bufferKey, ab.bufferTTL)
	_, err = pipe.Exec(ctx)
	return err
}

// GetBufferedAlerts retrieves all alerts held in a buffer group.
func (ab *AlertBuffer) GetBufferedAlerts(ctx context.Context, bufferKey string) ([]*Alert, error) {
	if ab.redis == nil {
		return nil, nil
	}
	items, err := ab.redis.LRange(ctx, bufferKeyPrefix+bufferKey, 0, -1).Result()
	if err != nil {
		return nil, err
	}
	alerts := make([]*Alert, 0, len(items))
	for _, item := range items {
		var a Alert
		if err := json.Unmarshal([]byte(item), &a); err == nil {
			alerts = append(alerts, &a)
		}
	}
	return alerts, nil
}

// SetSuppressed marks an alert as suppressed by the root cause engine.
func (ab *AlertBuffer) SetSuppressed(ctx context.Context, alertID uuid.UUID, reason SuppressedReason) error {
	return ab.SetAlertState(ctx, AlertStateRecord{
		AlertID:   alertID,
		State:     AlertStateSuppressed,
		Reason:    fmt.Sprintf("suppressed: root=%s incident=%s", reason.RootEntity, reason.RootIncidentID),
		UpdatedAt: time.Now(),
	})
}

// private helpers 

// alertEntityID derives a stable entity identifier from alert labels.
// Priority: EntityID field dt.entity.* labels cluster/node/namespace composite.
func alertEntityID(alert *Alert) string {
	if alert.EntityID != "" {
		return alert.EntityID
	}
	if alert.Labels != nil {
		for _, k := range []string{
			"dt.entity.host", "dt.entity.kubernetes_cluster",
			"dt.entity.kubernetes_node", "dt.entity.kubernetes_workload",
			"entity_id", "entityId",
		} {
			if v, ok := alert.Labels[k]; ok && v != "" {
				return v
			}
		}
	}
	// Composite fallback: cluster + node + namespace
	var parts []string
	if alert.Labels != nil {
		for _, k := range []string{"cluster", "node", "namespace"} {
			if v, ok := alert.Labels[k]; ok && v != "" {
				parts = append(parts, v)
			}
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "/")
	}
	return ""
}

// alertMetricType classifies the alert into a problem domain for dedup grouping.
func alertMetricType(alert *Alert) string {
	text := strings.ToLower(alert.Title + " " + alert.Description)
	switch {
	case strings.Contains(text, "cpu") || strings.Contains(text, "throttl"):
		return "cpu"
	case strings.Contains(text, "memory") || strings.Contains(text, "oom") || strings.Contains(text, "heap"):
		return "memory"
	case strings.Contains(text, "disk") || strings.Contains(text, "storage"):
		return "disk"
	case strings.Contains(text, "network") || strings.Contains(text, "latency") || strings.Contains(text, "timeout"):
		return "network"
	case strings.Contains(text, "restart") || strings.Contains(text, "crash") || strings.Contains(text, "backoff"):
		return "restart"
	case strings.Contains(text, "not ready") || strings.Contains(text, "unavailable") || strings.Contains(text, "down"):
		return "availability"
	}
	return "general"
}
