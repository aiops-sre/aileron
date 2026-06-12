package kafka

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/IBM/sarama"
)

// EventPublisher publishes investigation domain events to a Kafka topic.
// Implements application/investigation.EventPublisher.
type EventPublisher struct {
	producer sarama.SyncProducer
	topic    string
	logger   *slog.Logger
}

// NewEventPublisher creates a Kafka-backed event publisher.
func NewEventPublisher(brokers []string, topic string, logger *slog.Logger) (*EventPublisher, error) {
	cfg := defaultSaramaConfig()
	producer, err := sarama.NewSyncProducer(brokers, cfg)
	if err != nil {
		return nil, fmt.Errorf("creating kafka sync producer: %w", err)
	}
	return &EventPublisher{producer: producer, topic: topic, logger: logger}, nil
}

// PublishInvestigationEvent sends a domain event to Kafka.
func (p *EventPublisher) PublishInvestigationEvent(ctx context.Context, eventType string, payload []byte) error {
	msg := &sarama.ProducerMessage{
		Topic: p.topic,
		Key:   sarama.StringEncoder(eventType),
		Value: sarama.ByteEncoder(payload),
	}
	if _, _, err := p.producer.SendMessage(msg); err != nil {
		return fmt.Errorf("publishing investigation event %q: %w", eventType, err)
	}
	return nil
}

// Close shuts down the producer.
func (p *EventPublisher) Close() error {
	return p.producer.Close()
}
