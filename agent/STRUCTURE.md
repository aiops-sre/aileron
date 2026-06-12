# Kubernetes Intelligence Platform — Repository Structure
# Uses existing Strimzi Kafka cluster (same as AlertHub)

kubesense/
│
├── go.mod                              # Module root: kubesense
│                                       # Dependencies: IBM/sarama, k8s.io/client-go
│
├── pkg/
│   └── events/
│       └── events.go                  # ✅ Shared event types, topics, severity
│
├── proto/
│   └── intelligence/v1/
│       └── intelligence.proto         # ✅ Full gRPC contract (13 RPCs)
│
├── services/
│   │
│   ├── agent/                         # kubesense-agent
│   │   ├── cmd/agent/
│   │   │   └── main.go                # ✅ Entry point, informer wiring
│   │   └── internal/
│   │       ├── informers/
│   │       │   └── factory.go         # ✅ SharedInformerFactory, 25 resource types
│   │       ├── topology/
│   │       │   └── graph.go           # ✅ In-memory graph, all edge types
│   │       ├── health/
│   │       │   └── detector.go        # ✅ Pod/node health detection
│   │       └── publisher/
│   │           └── kafka.go           # ✅ Kafka publisher (IBM/sarama, same as AlertHub)
│   │
│   ├── collector/                     # kubesense-collector (TODO)
│   │   ├── cmd/collector/main.go
│   │   └── internal/
│   │       ├── ingestion/             # Kafka consumer (IBM/sarama)
│   │       └── registry/              # cluster and agent registry
│   │
│   ├── core/                          # kubesense-core
│   │   ├── cmd/core/main.go
│   │   └── internal/
│   │       ├── rca/
│   │       │   └── engine.go          # ✅ Evidence-first RCA engine
│   │       ├── topology/              # Neo4j topology writer (TODO)
│   │       ├── config/
│   │       │   └── validator.go       # ✅ 9 configuration validation rules
│   │       └── forecast/
│   │           └── engine.go          # ✅ Linear regression forecasting
│   │
│   ├── api/                           # kubesense-api (TODO)
│   │   ├── cmd/api/main.go
│   │   └── internal/handlers/
│   │
│   └── llm/                           # kubesense-llm (TODO)
│       └── cmd/llm/main.go
│
├── helm/
│   ├── kubesense-agent/
│   │   ├── Chart.yaml                 # ✅
│   │   ├── values.yaml                # ✅ kafkaBrokers, clusterID, localBufferMax
│   │   └── templates/
│   │       ├── _helpers.tpl           # ✅
│   │       ├── deployment.yaml        # ✅ KAFKA_BROKERS env var
│   │       └── clusterrole.yaml       # ✅ Minimal RBAC (read-only)
│   └── kubesense-hub/
│       └── values.yaml                # ✅ Hub services, references Strimzi Kafka
│
└── deployments/
    └── kafka/
        └── kafka-topics.yaml          # ✅ 9 Strimzi KafkaTopic CRDs
                                       #    kubesense.events.*, kubesense.investigations.*
                                       #    kubesense.forecasts, kubesense.config.violations
                                       #    All on existing alerthub-kafka cluster

# Messaging: Strimzi Kafka (existing) — bootstrap:
#   alerthub-kafka-kafka-bootstrap.aileron.svc.cluster.local:9092
#
# Library: IBM/sarama — same as AlertHub (kafka_producer.go, kafka_consumer.go)
#
# Full design documentation:
#   /docs/kip/KubeSense-ARCHITECTURE.md
