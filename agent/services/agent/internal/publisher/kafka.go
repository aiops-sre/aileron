// Package publisher sends IntelligenceEvents to Kafka using IBM/sarama.
// Uses the same Strimzi-managed Kafka cluster as AlertHub — no separate messaging
// infrastructure required. Topics are namespaced with kip. prefix to avoid
// conflicts with AlertHub's raw-alerts / normalized-alerts / correlation-results topics.
package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/IBM/sarama"
	"github.com/google/uuid"
	"github.com/aileron-platform/aileron/agent/pkg/events"
)

// KubeSense topic names — must match Strimzi KafkaTopic CRDs deployed in the cluster.
const (
	TopicTopology    = "kubesense.events.topology"
	TopicHealth      = "kubesense.events.health"
	TopicWorkloads   = "kubesense.events.workloads"
	TopicConfig      = "kubesense.events.config"
	TopicStorage     = "kubesense.events.storage"
	TopicNetwork     = "kubesense.events.network"
	TopicInvestReqs  = "kubesense.investigations.requests"
	TopicInvestRes   = "kubesense.investigations.results"
	TopicForecasts   = "kubesense.forecasts"
	TopicViolations  = "kubesense.config.violations"
)

// topicFor maps an event type to its Kafka topic.
// All events from a given cluster share one topic; cluster_id is the message key
// so consumers can filter and partition by cluster without per-cluster topics.
func topicFor(t events.EventType) string {
	switch {
	case hasPrefix(t, "health."):
		return TopicHealth
	case hasPrefix(t, "storage."):
		return TopicStorage
	case hasPrefix(t, "config."):
		return TopicConfig
	case hasPrefix(t, "topology."):
		return TopicTopology
	default:
		// resource.* and change.* go to workloads
		return TopicWorkloads
	}
}

func hasPrefix(t events.EventType, prefix string) bool {
	return strings.HasPrefix(string(t), prefix)
}

// Publisher sends intelligence events to Kafka.
type Publisher struct {
	producer  sarama.SyncProducer
	clusterID string
	agentID   string
	version   string

	// Local buffer for events when Kafka is temporarily unavailable.
	mu     sync.Mutex
	buffer []*events.IntelligenceEvent
	bufMax int
}

// New creates a Kafka publisher.
// brokers is the Strimzi bootstrap address, e.g.:
//   "alerthub-kafka-kafka-bootstrap.aileron.svc.cluster.local:9092"
func New(brokers []string, clusterID, agentID, version string, bufMax int) (*Publisher, error) {
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V2_8_0_0
	cfg.Producer.RequiredAcks = sarama.WaitForLocal // acks from partition leader
	cfg.Producer.Return.Successes = true
	cfg.Producer.Retry.Max = 3
	cfg.Producer.Retry.Backoff = 100 * time.Millisecond
	cfg.Net.DialTimeout = 5 * time.Second
	cfg.Net.ReadTimeout = 10 * time.Second
	cfg.Net.WriteTimeout = 10 * time.Second

	producer, err := sarama.NewSyncProducer(brokers, cfg)
	if err != nil {
		return nil, fmt.Errorf("kip publisher: kafka connect failed (brokers=%s): %w",
			strings.Join(brokers, ","), err)
	}

	p := &Publisher{
		producer:  producer,
		clusterID: clusterID,
		agentID:   agentID,
		version:   version,
		bufMax:    bufMax,
	}
	go p.drainBuffer()
	log.Printf("kubesense-agent publisher: connected to Kafka brokers=%s cluster=%s", strings.Join(brokers, ","), clusterID)
	return p, nil
}

// Publish sends a single intelligence event to the appropriate Kafka topic.
// The cluster_id is used as the message key so all events from the same cluster
// land on the same partition — preserving event ordering within a cluster.
func (p *Publisher) Publish(_ context.Context, ev *events.IntelligenceEvent) error {
	if ev.ID == "" {
		ev.ID = uuid.New().String()
	}
	ev.Timestamp = time.Now().UTC()
	ev.ClusterID = p.clusterID
	ev.AgentID = p.agentID
	ev.AgentVersion = p.version

	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal event type=%s: %w", ev.Type, err)
	}

	topic := topicFor(ev.Type)
	msg := &sarama.ProducerMessage{
		Topic: topic,
		Key:   sarama.StringEncoder(p.clusterID), // partition by cluster
		Value: sarama.ByteEncoder(data),
	}

	_, _, err = p.producer.SendMessage(msg)
	if err != nil {
		log.Printf("kubesense-agent: kafka publish failed topic=%s cluster=%s: %v — buffering",
			topic, p.clusterID, err)
		p.bufferEvent(ev)
		return nil // don't propagate — buffered locally
	}
	return nil
}

// Close shuts down the producer gracefully.
func (p *Publisher) Close() error {
	return p.producer.Close()
}

// bufferEvent enqueues an event for later retry. Drops the oldest if buffer is full.
func (p *Publisher) bufferEvent(ev *events.IntelligenceEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.buffer) >= p.bufMax {
		p.buffer = p.buffer[1:] // drop oldest
	}
	p.buffer = append(p.buffer, ev)
}

// drainBuffer periodically retries buffered events.
func (p *Publisher) drainBuffer() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		p.mu.Lock()
		if len(p.buffer) == 0 {
			p.mu.Unlock()
			continue
		}
		batch := make([]*events.IntelligenceEvent, len(p.buffer))
		copy(batch, p.buffer)
		p.mu.Unlock()

		delivered := 0
		for _, ev := range batch {
			data, _ := json.Marshal(ev)
			msg := &sarama.ProducerMessage{
				Topic: topicFor(ev.Type),
				Key:   sarama.StringEncoder(p.clusterID),
				Value: sarama.ByteEncoder(data),
			}
			if _, _, err := p.producer.SendMessage(msg); err != nil {
				break // Kafka still unavailable
			}
			delivered++
		}

		if delivered > 0 {
			p.mu.Lock()
			p.buffer = p.buffer[delivered:]
			p.mu.Unlock()
			log.Printf("kubesense-agent: drained %d buffered events to Kafka", delivered)
		}
	}
}
