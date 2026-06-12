package kafka

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/IBM/sarama"
	"github.com/google/uuid"
	appinv "github.com/aileron-platform/aileron/platform/services/oie/internal/application/investigation"
	domain "github.com/aileron-platform/aileron/platform/services/oie/internal/domain/investigation"
	"github.com/aileron-platform/aileron/platform/services/oie/internal/shared/metrics"
)

// httpClient is a thin wrapper so we can mock in tests and avoid import cycle with net/http.
type httpClient struct {
	c *http.Client
}

const (
	maxRetries         = 3
	dlqTopic           = "oie.investigations.dlq"
	investigationTopic = "alerthub.incidents"
)

// IncidentEvent is the Kafka message payload from AlertHub's incident topic.
type IncidentEvent struct {
	Type           string     `json:"type"`
	IncidentID     uuid.UUID  `json:"incident_id"`
	IncidentNumber string     `json:"incident_number"`
	Severity       string     `json:"severity"`
	StartedAt      time.Time  `json:"started_at"`
	RootEntityID   *uuid.UUID `json:"root_entity_id,omitempty"`
	RootEntityType *string    `json:"root_entity_type,omitempty"`
	FailureClass   *string    `json:"failure_class,omitempty"`
	Domain         *string    `json:"domain,omitempty"`
	PlaybookID     string     `json:"playbook_id,omitempty"`
	// Entity context extracted from AlertHub's topology_path and correlation_id.
	// These allow OIE to route to the correct K8s cluster / CloudStack fetcher
	// without needing a separate EIRS resolution call.
	TopologyPath  string `json:"topology_path,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
}

// ConsumerConfig holds all configuration for the Kafka consumer.
type ConsumerConfig struct {
	Brokers                   []string
	ConsumerGroupID           string
	AutoInvestigateSeverities []string
	SaramaConfig              *sarama.Config
	// PolicyEvalURL is the AlertHub backend base URL for policy evaluation.
	// When set, OIE calls POST <PolicyEvalURL>/api/v1/intelligence-policies/evaluate
	// before starting an investigation to respect skip_rca policies.
	PolicyEvalURL string
}

// Consumer is a Kafka consumer group member that processes incident events.
type Consumer struct {
	manager                   appinv.Service
	repo                      domain.Repository
	producer                  sarama.SyncProducer
	logger                    *slog.Logger
	group                     sarama.ConsumerGroup
	autoInvestigateSeverities map[string]struct{}
	// policyEvalURL is the AlertHub backend URL for the policy evaluate endpoint.
	// When set, OIE calls POST <policyEvalURL>/api/v1/intelligence-policies/evaluate
	// before starting an investigation to respect skip_rca policies.
	policyEvalURL string
	httpClient    *httpClient
}

// NewConsumer constructs and connects the consumer group.
func NewConsumer(
	manager appinv.Service,
	repo domain.Repository,
	cfg ConsumerConfig,
	logger *slog.Logger,
) (*Consumer, error) {
	saramaCfg := cfg.SaramaConfig
	if saramaCfg == nil {
		saramaCfg = defaultSaramaConfig()
	}

	group, err := sarama.NewConsumerGroup(cfg.Brokers, cfg.ConsumerGroupID, saramaCfg)
	if err != nil {
		return nil, fmt.Errorf("creating kafka consumer group: %w", err)
	}

	producer, err := sarama.NewSyncProducer(cfg.Brokers, saramaCfg)
	if err != nil {
		_ = group.Close()
		return nil, fmt.Errorf("creating kafka dlq producer: %w", err)
	}

	severities := make(map[string]struct{}, len(cfg.AutoInvestigateSeverities))
	for _, s := range cfg.AutoInvestigateSeverities {
		severities[toLower(s)] = struct{}{}
	}
	if len(severities) == 0 {
		severities["critical"] = struct{}{}
		severities["high"] = struct{}{}
	}

	return &Consumer{
		manager:                   manager,
		repo:                      repo,
		producer:                  producer,
		logger:                    logger,
		group:                     group,
		autoInvestigateSeverities: severities,
		policyEvalURL:             cfg.PolicyEvalURL,
		httpClient:                &httpClient{c: &http.Client{Timeout: 2 * time.Second}},
	}, nil
}

// defaultSaramaConfig returns a production-grade Sarama configuration.
// Auto-commit is explicitly disabled to prevent silent message loss.
func defaultSaramaConfig() *sarama.Config {
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V2_8_1_0
	cfg.Consumer.Offsets.AutoCommit.Enable = false
	// OffsetOldest: process all unread messages from the topic, not just new ones.
	// OffsetNewest caused the OIE consumer to miss all messages published before
	// the consumer group was first created (which was after incidents were already
	// in alerthub.incidents). Using OffsetOldest ensures existing incidents are
	// investigated. Sarama consumer group tracking prevents re-processing.
	cfg.Consumer.Offsets.Initial = sarama.OffsetOldest
	cfg.Consumer.Group.Session.Timeout = 30 * time.Second
	cfg.Consumer.Group.Heartbeat.Interval = 10 * time.Second
	cfg.Consumer.Group.Rebalance.GroupStrategies = []sarama.BalanceStrategy{sarama.BalanceStrategyRoundRobin}
	cfg.Consumer.Fetch.Default = 1 * 1024 * 1024
	cfg.Consumer.Fetch.Max = 10 * 1024 * 1024
	cfg.Consumer.MaxProcessingTime = 50 * time.Second
	cfg.Net.DialTimeout = 10 * time.Second
	cfg.Net.ReadTimeout = 30 * time.Second
	cfg.Net.WriteTimeout = 30 * time.Second
	cfg.Producer.Return.Successes = true
	cfg.Producer.Return.Errors = true
	cfg.Producer.RequiredAcks = sarama.WaitForAll
	cfg.Producer.Retry.Max = 5
	return cfg
}

// Run starts consuming and blocks until ctx is cancelled.
func (c *Consumer) Run(ctx context.Context) error {
	handler := &consumerGroupHandler{consumer: c}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := c.group.Consume(ctx, []string{investigationTopic}, handler); err != nil {
			c.logger.ErrorContext(ctx, "consumer group error", "error", err)
		}
	}
}

// Close shuts down the consumer group and DLQ producer gracefully.
func (c *Consumer) Close() error {
	var errs []string
	if err := c.group.Close(); err != nil {
		errs = append(errs, fmt.Sprintf("consumer group: %v", err))
	}
	if err := c.producer.Close(); err != nil {
		errs = append(errs, fmt.Sprintf("dlq producer: %v", err))
	}
	if len(errs) > 0 {
		return fmt.Errorf("closing consumer: %s", joinStrings(errs, "; "))
	}
	return nil
}

type consumerGroupHandler struct{ consumer *Consumer }

func (h *consumerGroupHandler) Setup(_ sarama.ConsumerGroupSession) error   { return nil }
func (h *consumerGroupHandler) Cleanup(_ sarama.ConsumerGroupSession) error { return nil }

func (h *consumerGroupHandler) ConsumeClaim(
	session sarama.ConsumerGroupSession,
	claim sarama.ConsumerGroupClaim,
) error {
	for msg := range claim.Messages() {
		idempotencyKey := fmt.Sprintf("%s:%d:%d", msg.Topic, msg.Partition, msg.Offset)
		if err := h.consumer.processMessage(session.Context(), msg, idempotencyKey); err != nil {
			h.consumer.logger.ErrorContext(session.Context(), "message processing failed",
				"topic", msg.Topic, "partition", msg.Partition,
				"offset", msg.Offset, "error", err)
			continue // Do not mark — will be redelivered.
		}
		session.MarkMessage(msg, "") // Mark offset for commit.
		session.Commit()             // Flush to Kafka (AutoCommit is disabled).
	}
	return nil
}

func (c *Consumer) processMessage(ctx context.Context, msg *sarama.ConsumerMessage, idempotencyKey string) error {
	msgKey := string(msg.Key)
	if msgKey == "" {
		msgKey = idempotencyKey
	}

	retryCount, err := c.repo.GetRetryCount(ctx, msgKey)
	if err != nil {
		return fmt.Errorf("getting retry count: %w", err)
	}
	if retryCount >= maxRetries {
		c.sendToDLQ(msg, msgKey, fmt.Sprintf("exceeded max retries (%d)", maxRetries))
		_ = c.repo.DeleteRetryCount(ctx, msgKey)
		metrics.DLQMessagesTotal.Inc()
		return nil
	}

	var event IncidentEvent
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		c.sendToDLQ(msg, msgKey, fmt.Sprintf("unmarshal error: %v", err))
		metrics.DLQMessagesTotal.Inc()
		return nil
	}

	if _, ok := c.autoInvestigateSeverities[toLower(event.Severity)]; !ok {
		return nil
	}
	if event.Type != "incident.created" && event.Type != "incident.escalated" {
		return nil
	}

	// Policy check: skip_rca policy from AlertHub's intelligence_policies table.
	// Calls the AlertHub backend's evaluate endpoint so OIE respects the same
	// policies as the pipeline without duplicating DB access.
	if c.policyEvalURL != "" {
		if skipRCA := c.evaluateSkipRCAPolicy(ctx, event); skipRCA {
			c.logger.InfoContext(ctx, "skip_rca policy matched — investigation suppressed",
				"incident_id", event.IncidentID, "severity", event.Severity)
			return nil
		}
	}

	req := &appinv.StartRequest{
		IncidentID:        event.IncidentID,
		IncidentNumber:    event.IncidentNumber,
		IdempotencyKey:    idempotencyKey,
		SourceMessageKey:  msgKey,
		Severity:          event.Severity,
		IncidentStartedAt: event.StartedAt,
		RootEntityID:      event.RootEntityID,
		RootEntityType:    event.RootEntityType,
		FailureClass:      event.FailureClass,
		Domain:            event.Domain,
		PlaybookID:        event.PlaybookID,
	}
	// If no explicit entity context, derive playbook and cluster from topology_path.
	// topology_path format: "cluster-name/namespace:title" or "h:hostname"
	if req.PlaybookID == "" && event.TopologyPath != "" {
		req.PlaybookID = inferPlaybookFromTopologyPath(event.TopologyPath)
	}
	if req.Domain == nil && event.CorrelationID != "" {
		domain := inferDomainFromCorrelationID(event.CorrelationID)
		req.Domain = &domain
	}

	if _, startErr := c.manager.Start(ctx, req); startErr != nil {
		newCount, upsertErr := c.repo.UpsertRetryCount(ctx, msgKey)
		if upsertErr != nil {
			c.logger.ErrorContext(ctx, "failed to increment retry count",
				"message_key", msgKey, "error", upsertErr)
		}
		c.logger.WarnContext(ctx, "investigation start failed",
			"incident_id", event.IncidentID,
			"retry_count", newCount,
			"error", startErr,
		)
		return startErr
	}

	_ = c.repo.DeleteRetryCount(ctx, msgKey)
	metrics.KafkaMessagesProcessed.WithLabelValues("success").Inc()
	return nil
}

func (c *Consumer) sendToDLQ(original *sarama.ConsumerMessage, messageKey string, reason string) {
	headers := []sarama.RecordHeader{
		{Key: []byte("oie_original_topic"), Value: []byte(original.Topic)},
		{Key: []byte("oie_original_partition"), Value: []byte(fmt.Sprintf("%d", original.Partition))},
		{Key: []byte("oie_original_offset"), Value: []byte(fmt.Sprintf("%d", original.Offset))},
		{Key: []byte("oie_failure_reason"), Value: []byte(reason)},
		{Key: []byte("oie_failed_at"), Value: []byte(time.Now().UTC().Format(time.RFC3339))},
	}
	dlqMsg := &sarama.ProducerMessage{
		Topic:   dlqTopic,
		Key:     sarama.StringEncoder(messageKey),
		Value:   sarama.ByteEncoder(original.Value),
		Headers: headers,
	}
	if _, _, err := c.producer.SendMessage(dlqMsg); err != nil {
		c.logger.Error("failed to send message to DLQ",
			"message_key", messageKey, "reason", reason, "error", err)
	}
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			b[i] = s[i] + 32
		} else {
			b[i] = s[i]
		}
	}
	return string(b)
}

func joinStrings(ss []string, sep string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}

// inferPlaybookFromTopologyPath derives a playbook ID from AlertHub's topology_path.
// topology_path format examples:
//   "example-cluster/kube-system:not all pods ready"  → PB-K8S-001
//   "h:bm-host-42"                                → PB-INF-001
//   "example-cluster:cpu-request saturation on node"  → PB-K8S-001
func inferPlaybookFromTopologyPath(path string) string {
	lower := toLower(path)
	switch {
	case contains(lower, "not all pods ready") || contains(lower, "notready") || contains(lower, "node in not ready"):
		return "PB-K8S-001"
	case contains(lower, "crashloop") || contains(lower, "crash loop"):
		return "PB-K8S-002"
	case contains(lower, "imagepull") || contains(lower, "image pull"):
		return "PB-K8S-003"
	case contains(lower, "pvc") && contains(lower, "disk"):
		return "PB-K8S-004"
	case contains(lower, "out-of-memory") || contains(lower, "oom") || contains(lower, "memory kills"):
		return "PB-K8S-002"
	case contains(lower, "cpu") && contains(lower, "saturation"):
		return "PB-K8S-001"
	case lower[0:2] == "h:":
		return "PB-INF-001"
	default:
		return "PB-DEFAULT-001"
	}
}

// inferDomainFromCorrelationID derives the operational domain from AlertHub's correlation_id.
// correlation_id format: "cluster-name/namespace:issue-description"
func inferDomainFromCorrelationID(correlationID string) string {
	lower := toLower(correlationID)
	switch {
	case contains(lower, "storage") || contains(lower, "pvc") || contains(lower, "disk"):
		return "storage"
	case contains(lower, "network") || contains(lower, "dns") || contains(lower, "connectivity"):
		return "network"
	case contains(lower, "database") || contains(lower, "postgres") || contains(lower, "mysql"):
		return "database"
	default:
		return "kubernetes"
	}
}

func contains(s, substr string) bool {
	if len(substr) > len(s) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}


// evaluateSkipRCAPolicy calls the AlertHub policy evaluate endpoint and returns true
// if the incident should NOT be investigated (skip_rca policy matched).
// Fail-open: any network error or non-200 response returns false (allow investigation).
func (c *Consumer) evaluateSkipRCAPolicy(ctx context.Context, event IncidentEvent) bool {
	payload, _ := json.Marshal(map[string]interface{}{
		"severity":   event.Severity,
		"source":     "incident",
		"title":      event.IncidentID.String(),
		"entity_type": func() string {
			if event.RootEntityType != nil {
				return *event.RootEntityType
			}
			return ""
		}(),
		"labels": map[string]string{},
	})
	url := c.policyEvalURL + "/api/v1/intelligence-policies/evaluate"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.c.Do(req)
	if err != nil || resp == nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Action     string `json:"action"`
		PolicyName string `json:"policy_name"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return false
	}
	if result.Action == "skip_rca" {
		c.logger.InfoContext(ctx, "skip_rca policy from AlertHub",
			"policy", result.PolicyName, "incident_id", event.IncidentID)
		return true
	}
	return false
}
