// Package kafkapub provides a Kafka publisher accessible to all services in the
// kubesense module. It wraps services/core/internal/publish with a public API.
package kafkapub

import (
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/IBM/sarama"
)

// Publisher is a Kafka producer for intelligence events.
type Publisher struct {
	producer sarama.SyncProducer
}

// New creates a Kafka publisher from a comma-separated broker string.
// Returns nil if brokers is empty or connection fails (fail-open callers).
func New(brokerStr string) *Publisher {
	if brokerStr == "" {
		return nil
	}
	brokers := strings.Split(brokerStr, ",")
	cfg := sarama.NewConfig()
	cfg.Producer.Return.Successes = true
	cfg.Producer.RequiredAcks = sarama.WaitForAll
	cfg.Producer.Retry.Max = 5
	cfg.Producer.Retry.Backoff = 100 * time.Millisecond
	cfg.Producer.Compression = sarama.CompressionSnappy
	prod, err := sarama.NewSyncProducer(brokers, cfg)
	if err != nil {
		log.Printf("kafkapub: connect failed (brokers=%s): %v — publishing disabled", brokerStr, err)
		return nil
	}
	return &Publisher{producer: prod}
}

// Publish sends a JSON payload to a Kafka topic with the given key.
// Non-fatal: logs on error but does not return it.
func (p *Publisher) Publish(topic, key string, payload any) {
	if p == nil {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("kafkapub: marshal %s: %v", topic, err)
		return
	}
	_, _, err = p.producer.SendMessage(&sarama.ProducerMessage{
		Topic: topic,
		Key:   sarama.StringEncoder(key),
		Value: sarama.ByteEncoder(data),
	})
	if err != nil {
		log.Printf("kafkapub: send %s: %v", topic, err)
	}
}

// Close shuts down the producer.
func (p *Publisher) Close() {
	if p != nil && p.producer != nil {
		p.producer.Close()
	}
}

// Topic constants shared across all KubeSense services.
const (
	TopicChaosScores       = "kubesense.chaos.scores"
	TopicGitOpsChanges     = "kubesense.gitops.changes"
	TopicAPMGoldenSignals  = "kubesense.apm.golden-signals"
	TopicConfigViolations  = "kubesense.config.violations"
	TopicForecasts         = "kubesense.forecasts"
	TopicAnomalies         = "kubesense.anomalies"
	TopicDriftDetected     = "kubesense.drift.detected"
	TopicPostmortems       = "kubesense.postmortems"
	TopicToilEvents        = "kubesense.toil.events"
	TopicInvResults        = "kubesense.investigations.results"
)
