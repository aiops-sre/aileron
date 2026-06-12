// Package ingestion consumes IntelligenceEvents from all kubesense.events.* Kafka topics.
// One consumer group ensures each event is processed exactly once across all collector replicas.
package ingestion

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	"github.com/IBM/sarama"
	"github.com/aileron-platform/aileron/agent/pkg/events"
)

// EventProcessor handles a parsed IntelligenceEvent.
type EventProcessor interface {
	Process(ctx context.Context, ev *events.IntelligenceEvent) error
}

// Consumer subscribes to all kubesense.events.* topics and dispatches to processors.
type Consumer struct {
	brokers    []string
	processors []EventProcessor
}

// NewConsumer creates a multi-topic Kafka consumer.
func NewConsumer(brokers []string, processors ...EventProcessor) *Consumer {
	return &Consumer{brokers: brokers, processors: processors}
}

// Topics returns all agent event topics the collector subscribes to.
var Topics = []string{
	"kubesense.events.topology",
	"kubesense.events.workloads",
	"kubesense.events.health",
	"kubesense.events.config",
	"kubesense.events.storage",
	"kubesense.events.network",
}

// Start blocks until ctx is cancelled, consuming from all agent event topics.
func (c *Consumer) Start(ctx context.Context) {
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V2_8_0_0
	cfg.Consumer.Group.Rebalance.Strategy = sarama.BalanceStrategyRoundRobin
	cfg.Consumer.Offsets.Initial = sarama.OffsetNewest
	cfg.Consumer.Return.Errors = true

	group, err := sarama.NewConsumerGroup(c.brokers, "kubesense-collector", cfg)
	if err != nil {
		log.Printf("kubesense-collector: Kafka connect failed (brokers=%s): %v",
			strings.Join(c.brokers, ","), err)
		return
	}
	defer group.Close()

	log.Printf("kubesense-collector: consuming topics=%v brokers=%v",
		Topics, strings.Join(c.brokers, ","))

	handler := &consumerGroupHandler{processors: c.processors}
	for {
		if err := group.Consume(ctx, Topics, handler); err != nil {
			log.Printf("kubesense-collector: consume error: %v", err)
		}
		if ctx.Err() != nil {
			return
		}
	}
}

type consumerGroupHandler struct {
	processors []EventProcessor
}

func (h *consumerGroupHandler) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (h *consumerGroupHandler) Cleanup(sarama.ConsumerGroupSession) error { return nil }

func (h *consumerGroupHandler) ConsumeClaim(
	sess sarama.ConsumerGroupSession,
	claim sarama.ConsumerGroupClaim,
) error {
	for msg := range claim.Messages() {
		var ev events.IntelligenceEvent
		if err := json.Unmarshal(msg.Value, &ev); err != nil {
			log.Printf("kubesense-collector: parse error topic=%s offset=%d: %v",
				msg.Topic, msg.Offset, err)
			sess.MarkMessage(msg, "")
			continue
		}

		for _, p := range h.processors {
			if err := p.Process(sess.Context(), &ev); err != nil {
				log.Printf("kubesense-collector: processor error event_id=%s type=%s: %v",
					ev.ID, ev.Type, err)
			}
		}
		sess.MarkMessage(msg, "")
	}
	return nil
}
