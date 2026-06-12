package pipeline

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/IBM/sarama"
)

// Topic names — must match the Strimzi KafkaTopic CRDs deployed in the cluster.
const DefaultAlertsTopic = "raw-alerts"
const NormalizedAlertsTopic = "normalized-alerts"
const CorrelationResultsTopic = "correlation-results"

// AlertKafkaProducer implements the handlers.KafkaProducer interface and publishes
// alert JSON to the raw-alerts Kafka topic.
type AlertKafkaProducer struct {
	producer sarama.SyncProducer
	topic    string
}

// NewAlertKafkaProducer creates a sync Kafka producer.
// Returns an error if the broker is unreachable — callers should handle this gracefully.
func NewAlertKafkaProducer(brokers []string, topic string) (*AlertKafkaProducer, error) {
	cfg := sarama.NewConfig()
	cfg.Producer.RequiredAcks = sarama.WaitForLocal
	cfg.Producer.Return.Successes = true
	cfg.Producer.Retry.Max = 3
	cfg.Version = sarama.V3_3_1_0
	cfg.Net.DialTimeout = 5 * time.Second
	cfg.Net.ReadTimeout = 10 * time.Second
	cfg.Net.WriteTimeout = 10 * time.Second

	producer, err := sarama.NewSyncProducer(brokers, cfg)
	if err != nil {
		return nil, fmt.Errorf("kafka producer init failed (brokers=%s): %w",
			strings.Join(brokers, ","), err)
	}
	return &AlertKafkaProducer{producer: producer, topic: topic}, nil
}

// PublishToRawAlerts satisfies the handlers.KafkaProducer interface.
func (p *AlertKafkaProducer) PublishToRawAlerts(alert interface{}) error {
	data, err := json.Marshal(alert)
	if err != nil {
		return fmt.Errorf("failed to marshal alert: %w", err)
	}
	_, _, err = p.producer.SendMessage(&sarama.ProducerMessage{
		Topic: p.topic,
		Value: sarama.ByteEncoder(data),
	})
	return err
}

// PublishToTopic publishes a message to an arbitrary topic.
func (p *AlertKafkaProducer) PublishToTopic(topic string, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}
	_, _, err = p.producer.SendMessage(&sarama.ProducerMessage{
		Topic: topic,
		Value: sarama.ByteEncoder(data),
	})
	return err
}

func (p *AlertKafkaProducer) Close() error {
	return p.producer.Close()
}
