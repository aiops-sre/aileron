package kubesense

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/IBM/sarama"
	"github.com/google/uuid"
)

// Publisher publishes investigation requests to the kubesense.investigations.requests topic,
// triggering KubeSense's evidence-first RCA for the given incident.
type Publisher struct {
	producer sarama.SyncProducer
}

// NewPublisher creates a Kafka sync producer for the KubeSense investigation request topic.
// Returns nil, error if Kafka is unreachable — callers should treat nil as investigations disabled.
func NewPublisher(brokers []string) (*Publisher, error) {
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
		return nil, fmt.Errorf("kubesense publisher (brokers=%s): %w", strings.Join(brokers, ","), err)
	}
	return &Publisher{producer: producer}, nil
}

// RequestInvestigation publishes a KubeSense investigation request for an open incident.
// KubeSense will run its evidence-first analysis and publish results back on
// kubesense.investigations.results, which AlertHub's consumer will merge into rca_decisions.
func (p *Publisher) RequestInvestigation(
	ctx context.Context,
	incidentID, clusterID, namespace, resourceKind, resourceName, severity, alertTitle string,
) error {
	req := InvestigationRequest{
		ID:            uuid.New().String(),
		RequestedAt:   time.Now().UTC(),
		IncidentID:    incidentID,
		ClusterID:     clusterID,
		Namespace:     namespace,
		ResourceKind:  resourceKind,
		ResourceName:  resourceName,
		Severity:      severity,
		AlertTitle:    alertTitle,
		CallbackTopic: TopicInvResults,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal investigation request: %w", err)
	}
	_, _, err = p.producer.SendMessage(&sarama.ProducerMessage{
		Topic: TopicInvRequests,
		Key:   sarama.StringEncoder(incidentID), // partition by incident for ordering
		Value: sarama.ByteEncoder(data),
	})
	if err != nil {
		return fmt.Errorf("publish investigation request incident=%s: %w", incidentID, err)
	}
	log.Printf("KubeSense: investigation requested incident=%s cluster=%s resource=%s/%s",
		incidentID, clusterID, resourceKind, resourceName)
	return nil
}

func (p *Publisher) Close() error { return p.producer.Close() }
