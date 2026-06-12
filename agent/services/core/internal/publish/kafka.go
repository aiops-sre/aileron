// Package publish provides a Kafka publisher for the core intelligence services.
// The correlation engine, RCA engine, and other core services need to publish
// results to Kafka so the LLM narrator, AlertHub, and other consumers can act.
// This package implements a shared, retrying publisher with structured envelope.
package publish

import (
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/IBM/sarama"
)

// Publisher is a Kafka producer for core intelligence events.
type Publisher struct {
	producer sarama.SyncProducer
}

// NewPublisher creates a Kafka publisher.
func NewPublisher(brokers []string) (*Publisher, error) {
	cfg := sarama.NewConfig()
	cfg.Producer.Return.Successes = true
	cfg.Producer.RequiredAcks = sarama.WaitForAll
	cfg.Producer.Retry.Max = 5
	cfg.Producer.Retry.Backoff = 100 * time.Millisecond
	cfg.Producer.Compression = sarama.CompressionSnappy
	prod, err := sarama.NewSyncProducer(brokers, cfg)
	if err != nil {
		return nil, err
	}
	return &Publisher{producer: prod}, nil
}

// Publish sends a JSON payload to a Kafka topic with the given key.
// Non-fatal: logs on error but does not return it to callers.
func (p *Publisher) Publish(topic, key string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("publish: marshal %s: %v", topic, err)
		return
	}
	_, _, err = p.producer.SendMessage(&sarama.ProducerMessage{
		Topic: topic,
		Key:   sarama.StringEncoder(key),
		Value: sarama.ByteEncoder(data),
	})
	if err != nil {
		log.Printf("publish: kafka %s: %v", topic, err)
	}
}

// Close shuts down the producer cleanly.
func (p *Publisher) Close() {
	if p.producer != nil {
		p.producer.Close()
	}
}

// NoopPublisher is used when Kafka is unavailable (test / dev).
type NoopPublisher struct{}

func (n *NoopPublisher) Publish(topic, key string, payload any) {}
func (n *NoopPublisher) Close()                                  {}

// NewFromBrokerString parses a comma-separated broker list and returns a publisher.
// Falls back to NoopPublisher if brokers is empty.
func NewFromBrokerString(brokerStr string) *Publisher {
	if brokerStr == "" {
		return nil
	}
	brokers := strings.Split(brokerStr, ",")
	pub, err := NewPublisher(brokers)
	if err != nil {
		log.Printf("publish: kafka unavailable (%v) — publishing disabled", err)
		return nil
	}
	return pub
}

// Kafka topic constants used by core services.
const (
	TopicIncidentContext    = "kubesense.correlation.incident-context"
	TopicLLMNarratives      = "kubesense.llm.narratives"
	TopicRiskScores         = "kubesense.risk.scores"
	TopicFingerprintMatches = "kubesense.fingerprints.matches"
	TopicPlaybooks          = "kubesense.playbooks"
	TopicGitOpsChanges      = "kubesense.gitops.changes"
	TopicWebhookDecisions   = "kubesense.webhook.decisions"
	TopicToilEvents         = "kubesense.toil.events"
	TopicDriftDetected      = "kubesense.drift.detected"
	TopicPostmortems        = "kubesense.postmortems"
	TopicChaosScores        = "kubesense.chaos.scores"
	TopicRemediationOutcomes = "kubesense.remediation.outcomes"
	// Intelligence signal topics — consumed by AlertHub OIE evidence bus
	TopicAPMGoldenSignals  = "kubesense.apm.golden-signals"
	TopicAPMRegressions    = "kubesense.apm.regressions"
	TopicConfigViolations  = "kubesense.config.violations"
	TopicForecasts         = "kubesense.forecasts"
	TopicAnomalies         = "kubesense.anomalies"
)
