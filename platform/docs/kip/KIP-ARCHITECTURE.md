# Kubernetes Intelligence Platform (KIP)
# Complete Architecture & Implementation Design

---

## 1. DESIGN PHILOSOPHY

This platform is not a monitoring tool. It is not a dashboard product. It is not
an alert aggregator. It is a reasoning engine that understands Kubernetes cluster
state as deeply as a senior SRE — and can explain what happened, why, and what to do.

**Three laws:**
1. Every conclusion must cite evidence. No LLM-first reasoning.
2. The topology graph is the ground truth. All analysis starts there.
3. Push over pull. The system reacts to changes, not polls for them.

---

## 2. MESSAGING INFRASTRUCTURE: KAFKA (STRIMZI — EXISTING)

### Decision: Use the existing Strimzi-managed Kafka cluster

AlertHub already runs Strimzi Kafka at:
`alerthub-kafka-kafka-bootstrap.aileron.svc.cluster.local:9092`

KIP uses the same cluster with a separate `kip.` topic prefix. No new messaging
infrastructure. No NATS. No second broker to operate.

**Multi-cluster handling:** Since there is no NATS leaf node, multi-cluster isolation
is achieved via the Kafka message key. Every event uses `cluster_id` as the key,
which guarantees that all events from one cluster land on the same partition and
are processed in order. Consumers filter by `cluster_id` in the message payload.

**Offline buffering:** The agent maintains an in-memory circular buffer (configurable,
default 5,000 events). Events are drained to Kafka when connectivity is restored.
For environments requiring persistent local buffering, this can be replaced with a
local SQLite-backed queue — same drain logic, durable storage.

### Kafka Topics (Strimzi KafkaTopic CRDs)

```
kip.events.topology          # topology changes — key: cluster_id
kip.events.workloads         # deployment/pod/sts/ds lifecycle — key: cluster_id
kip.events.health            # pod crashes, node issues — key: cluster_id
kip.events.config            # configmap/secret/rbac changes — key: cluster_id
kip.events.storage           # pvc/pv state changes — key: cluster_id
kip.events.network           # service/ingress/netpol changes — key: cluster_id

kip.investigations.requests  # AlertHub → KIP investigation trigger
kip.investigations.results   # KIP → AlertHub RCA results

kip.forecasts                # forecasting output
kip.config.violations        # configuration validation findings
```

### Topic Configuration

All topics use `IBM/sarama` (same library as AlertHub) with:
- 6 partitions for event topics (supports 6 parallel consumers)
- 3 replicas matching Strimzi cluster replication factor
- `min.insync.replicas: 2` for durability
- 7-day retention for event topics; 30-day for change/investigation topics
- `cluster_id` as message key for intra-cluster ordering

KafkaTopic CRDs: `deployments/kafka/kafka-topics.yaml`

---

## 3. REPOSITORY STRUCTURE

```
k8s-intelligence-platform/
├── services/
│   ├── agent/                    # k8s-intelligence-agent (Go)
│   │   ├── cmd/agent/main.go
│   │   ├── internal/
│   │   │   ├── informers/        # client-go informer setup
│   │   │   ├── topology/         # local topology graph builder
│   │   │   ├── publisher/        # Kafka publisher (IBM/sarama)
│   │   │   └── health/           # health detector
│   │   └── go.mod
│   │
│   ├── collector/                # k8s-intelligence-collector (Go)
│   │   ├── cmd/collector/main.go
│   │   └── internal/
│   │       ├── ingestion/        # Kafka consumer (IBM/sarama), event parsing
│   │       ├── normalization/    # event schema normalization
│   │       ├── registry/         # cluster and agent registry
│   │       └── routing/          # route events to core
│   │
│   ├── core/                     # k8s-intelligence-core (Go)
│   │   ├── cmd/core/main.go
│   │   └── internal/
│   │       ├── topology/         # Neo4j topology graph engine
│   │       ├── rca/              # root cause engine
│   │       ├── dependency/       # dependency graph builder
│   │       ├── change/           # change intelligence
│   │       ├── config/           # configuration intelligence
│   │       ├── forecast/         # forecasting engine
│   │       └── knowledge/        # knowledge graph
│   │
│   ├── api/                      # k8s-intelligence-api (Go)
│   │   ├── cmd/api/main.go
│   │   └── internal/
│   │       ├── handlers/         # HTTP handlers
│   │       ├── grpc/             # gRPC server
│   │   │   ├── alerthub/         # AlertHub integration
│   │   │   └── search/           # resource search
│   │   └── go.mod
│   │
│   └── llm/                      # k8s-intelligence-llm (Go)
│       ├── cmd/llm/main.go
│       ├── internal/
│       │   ├── grounding/        # evidence grounding before LLM
│       │   ├── templates/        # prompt templates
│       │   └── ollama/           # Ollama client
│       └── go.mod
│
├── proto/                        # Protobuf definitions
│   ├── intelligence/v1/
│   │   ├── intelligence.proto    # Core service contract
│   │   ├── events.proto          # Event schemas
│   │   ├── topology.proto        # Topology types
│   │   └── rca.proto             # RCA types
│   └── buf.yaml
│
├── pkg/                          # Shared libraries
│   ├── events/                   # Event type definitions
│   ├── topology/                 # Shared topology structs
│   ├── k8s/                      # K8s helpers
│   └── kafka/                    # Kafka/sarama client wrapper
│
├── helm/
│   ├── kip-agent/                # Deployed per cluster
│   └── kip-hub/                  # Deployed once (hub cluster)
│
├── deployments/
│   ├── kafka/                    # Strimzi KafkaTopic CRDs
│   ├── neo4j/                    # Neo4j manifests
│   └── postgres/                 # PostgreSQL + TimescaleDB
│
└── docs/
    ├── architecture/
    └── runbooks/
```

---

## 4. EVENT SCHEMA

### Core Event Envelope

```go
// pkg/events/event.go

package events

import "time"

type IntelligenceEvent struct {
    // Identity
    ID        string    `json:"id"`         // UUID v7 (time-ordered)
    Timestamp time.Time `json:"timestamp"`  // event occurrence time
    ReceivedAt time.Time `json:"received_at,omitempty"`

    // Source
    ClusterID   string `json:"cluster_id"`
    AgentID     string `json:"agent_id"`
    AgentVersion string `json:"agent_version"`

    // Classification
    Type     EventType     `json:"type"`
    Severity EventSeverity `json:"severity"`

    // Subject
    Resource ResourceRef `json:"resource"`

    // Content
    OldState json.RawMessage `json:"old_state,omitempty"` // previous resource spec
    NewState json.RawMessage `json:"new_state,omitempty"` // current resource spec
    Diff     json.RawMessage `json:"diff,omitempty"`       // structured diff

    // Context
    TriggeredBy string            `json:"triggered_by,omitempty"` // user/controller/hpa
    Labels      map[string]string `json:"labels,omitempty"`
    Annotations map[string]string `json:"annotations,omitempty"`
}

type EventType string

const (
    // Resource lifecycle
    EventResourceCreated EventType = "resource.created"
    EventResourceUpdated EventType = "resource.updated"
    EventResourceDeleted EventType = "resource.deleted"

    // Workload health
    EventPodCrashLoopBackOff  EventType = "health.pod.crashloopbackoff"
    EventPodOOMKilled          EventType = "health.pod.oomkilled"
    EventPodImagePullError     EventType = "health.pod.imagepull_error"
    EventPodPending            EventType = "health.pod.pending"
    EventPodEvicted            EventType = "health.pod.evicted"
    EventDeploymentDegraded    EventType = "health.deployment.degraded"
    EventStatefulSetDegraded   EventType = "health.statefulset.degraded"

    // Node health
    EventNodeNotReady          EventType = "health.node.not_ready"
    EventNodeDiskPressure      EventType = "health.node.disk_pressure"
    EventNodeMemoryPressure    EventType = "health.node.memory_pressure"
    EventNodeCPUPressure       EventType = "health.node.cpu_pressure"
    EventNodeCordon            EventType = "health.node.cordoned"
    EventNodeDrain             EventType = "health.node.drained"

    // Storage
    EventPVCPending            EventType = "storage.pvc.pending"
    EventPVCLost               EventType = "storage.pvc.lost"
    EventPVCNearFull           EventType = "storage.pvc.near_full"

    // Change
    EventDeploymentRollout     EventType = "change.deployment.rollout"
    EventConfigMapChanged      EventType = "change.configmap.updated"
    EventSecretRotated         EventType = "change.secret.rotated"
    EventRBACChanged           EventType = "change.rbac.updated"
    EventScalingEvent          EventType = "change.hpa.scaled"

    // Configuration violations
    EventConfigViolation       EventType = "config.violation"

    // Topology
    EventTopologyChanged       EventType = "topology.changed"

    // Forecast
    EventForecastAlert         EventType = "forecast.threshold_approaching"
)

type ResourceRef struct {
    APIVersion string `json:"api_version"`
    Kind       string `json:"kind"`
    Namespace  string `json:"namespace,omitempty"`
    Name       string `json:"name"`
    UID        string `json:"uid"`
    Labels     map[string]string `json:"labels,omitempty"`
}
```

---

## 5. AGENT ARCHITECTURE

### 5.1 Informer Setup

```go
// services/agent/internal/informers/factory.go

package informers

import (
    "context"
    "time"

    "k8s.io/client-go/informers"
    "k8s.io/client-go/kubernetes"
)

const defaultResync = 30 * time.Second

type WatchedResources struct {
    factory informers.SharedInformerFactory
}

func NewWatchedResources(client kubernetes.Interface) *WatchedResources {
    factory := informers.NewSharedInformerFactory(client, defaultResync)
    return &WatchedResources{factory: factory}
}

func (w *WatchedResources) RegisterAll(handlers ResourceEventHandlers) {
    // Core workloads
    w.factory.Core().V1().Pods().Informer().AddEventHandler(handlers.Pods)
    w.factory.Core().V1().Nodes().Informer().AddEventHandler(handlers.Nodes)
    w.factory.Apps().V1().Deployments().Informer().AddEventHandler(handlers.Deployments)
    w.factory.Apps().V1().StatefulSets().Informer().AddEventHandler(handlers.StatefulSets)
    w.factory.Apps().V1().DaemonSets().Informer().AddEventHandler(handlers.DaemonSets)
    w.factory.Apps().V1().ReplicaSets().Informer().AddEventHandler(handlers.ReplicaSets)
    w.factory.Batch().V1().Jobs().Informer().AddEventHandler(handlers.Jobs)
    w.factory.Batch().V1().CronJobs().Informer().AddEventHandler(handlers.CronJobs)

    // Networking
    w.factory.Core().V1().Services().Informer().AddEventHandler(handlers.Services)
    w.factory.Discovery().V1().EndpointSlices().Informer().AddEventHandler(handlers.EndpointSlices)
    w.factory.Networking().V1().Ingresses().Informer().AddEventHandler(handlers.Ingresses)
    w.factory.Networking().V1().NetworkPolicies().Informer().AddEventHandler(handlers.NetworkPolicies)

    // Storage
    w.factory.Core().V1().PersistentVolumeClaims().Informer().AddEventHandler(handlers.PVCs)
    w.factory.Core().V1().PersistentVolumes().Informer().AddEventHandler(handlers.PVs)
    w.factory.Storage().V1().StorageClasses().Informer().AddEventHandler(handlers.StorageClasses)

    // Configuration
    w.factory.Core().V1().ConfigMaps().Informer().AddEventHandler(handlers.ConfigMaps)
    // Secrets: watch metadata only — never cache secret data
    w.factory.Core().V1().Secrets().Informer().AddEventHandler(handlers.Secrets)
    w.factory.Core().V1().Namespaces().Informer().AddEventHandler(handlers.Namespaces)
    w.factory.Core().V1().ServiceAccounts().Informer().AddEventHandler(handlers.ServiceAccounts)

    // RBAC
    w.factory.Rbac().V1().Roles().Informer().AddEventHandler(handlers.Roles)
    w.factory.Rbac().V1().RoleBindings().Informer().AddEventHandler(handlers.RoleBindings)
    w.factory.Rbac().V1().ClusterRoles().Informer().AddEventHandler(handlers.ClusterRoles)
    w.factory.Rbac().V1().ClusterRoleBindings().Informer().AddEventHandler(handlers.ClusterRoleBindings)

    // Autoscaling
    w.factory.Autoscaling().V2().HorizontalPodAutoscalers().Informer().AddEventHandler(handlers.HPAs)

    // Events (K8s native events — distinct from KIP events)
    w.factory.Core().V1().Events().Informer().AddEventHandler(handlers.K8sEvents)
}

func (w *WatchedResources) Start(ctx context.Context) {
    w.factory.Start(ctx.Done())
    w.factory.WaitForCacheSync(ctx.Done())
}
```

### 5.2 Topology Builder (in-memory, per agent)

```go
// services/agent/internal/topology/graph.go

package topology

import (
    "sync"
    corev1 "k8s.io/api/core/v1"
    appsv1 "k8s.io/api/apps/v1"
)

// LocalGraph is the agent's in-memory view of the cluster topology.
// It is rebuilt from informer cache on startup and kept current via events.
type LocalGraph struct {
    mu    sync.RWMutex
    nodes map[string]*TopologyNode   // key: kind/namespace/name
    edges []TopologyEdge
}

type TopologyNode struct {
    ID         string
    Kind       string
    Namespace  string
    Name       string
    UID        string
    Labels     map[string]string
    Status     string
    // Kind-specific data
    Spec       interface{}
}

type TopologyEdge struct {
    SourceID string
    TargetID string
    Type     EdgeType
}

type EdgeType string

const (
    EdgeRunsOn        EdgeType = "RUNS_ON"        // Pod → Node
    EdgeManagedBy     EdgeType = "MANAGED_BY"     // Pod → Deployment/StatefulSet
    EdgeSelectedBy    EdgeType = "SELECTED_BY"    // Pod → Service (via selector match)
    EdgeRoutesTo      EdgeType = "ROUTES_TO"      // Ingress → Service
    EdgeMountsPVC     EdgeType = "MOUNTS_PVC"     // Pod → PVC
    EdgeBoundTo       EdgeType = "BOUND_TO"       // PVC → PV
    EdgeUsesConfigMap EdgeType = "USES_CONFIGMAP" // Deployment → ConfigMap
    EdgeUsesSecret    EdgeType = "USES_SECRET"    // Deployment → Secret
    EdgeUsesAccount   EdgeType = "USES_SA"        // Pod → ServiceAccount
    EdgeScaledBy      EdgeType = "SCALED_BY"      // Deployment → HPA
    EdgeInNamespace   EdgeType = "IN_NAMESPACE"   // Resource → Namespace
)

// OnPodUpdate keeps topology current when a pod changes.
func (g *LocalGraph) OnPodUpdate(pod *corev1.Pod) {
    g.mu.Lock()
    defer g.mu.Unlock()

    nodeID := nodeKey("Pod", pod.Namespace, pod.Name)
    g.nodes[nodeID] = &TopologyNode{
        ID:        nodeID,
        Kind:      "Pod",
        Namespace: pod.Namespace,
        Name:      pod.Name,
        UID:       string(pod.UID),
        Labels:    pod.Labels,
        Status:    string(pod.Status.Phase),
    }

    // Edge: Pod → Node
    if pod.Spec.NodeName != "" {
        g.upsertEdge(nodeID, nodeKey("Node", "", pod.Spec.NodeName), EdgeRunsOn)
    }

    // Edge: Pod → owner (Deployment/StatefulSet/DaemonSet via OwnerReferences)
    for _, ref := range pod.OwnerReferences {
        g.upsertEdge(nodeID, nodeKey(ref.Kind, pod.Namespace, ref.Name), EdgeManagedBy)
    }

    // Edge: Pod → ServiceAccount
    if pod.Spec.ServiceAccountName != "" {
        g.upsertEdge(nodeID, nodeKey("ServiceAccount", pod.Namespace, pod.Spec.ServiceAccountName), EdgeUsesAccount)
    }

    // Edges: Pod → PVC (volumes)
    for _, vol := range pod.Spec.Volumes {
        if vol.PersistentVolumeClaim != nil {
            g.upsertEdge(nodeID, nodeKey("PVC", pod.Namespace, vol.PersistentVolumeClaim.ClaimName), EdgeMountsPVC)
        }
        if vol.ConfigMap != nil {
            g.upsertEdge(nodeID, nodeKey("ConfigMap", pod.Namespace, vol.ConfigMap.Name), EdgeUsesConfigMap)
        }
        if vol.Secret != nil {
            g.upsertEdge(nodeID, nodeKey("Secret", pod.Namespace, vol.Secret.SecretName), EdgeUsesSecret)
        }
    }

    // Edges: container env → ConfigMap/Secret refs
    for _, container := range pod.Spec.Containers {
        for _, env := range container.EnvFrom {
            if env.ConfigMapRef != nil {
                g.upsertEdge(nodeID, nodeKey("ConfigMap", pod.Namespace, env.ConfigMapRef.Name), EdgeUsesConfigMap)
            }
            if env.SecretRef != nil {
                g.upsertEdge(nodeID, nodeKey("Secret", pod.Namespace, env.SecretRef.Name), EdgeUsesSecret)
            }
        }
    }
}

// ComputeServiceToPodEdges resolves service selector → matching pods.
// Called after both services and pods are synced.
func (g *LocalGraph) ComputeServiceToPodEdges(svc *corev1.Service, allPods []*corev1.Pod) {
    if svc.Spec.Selector == nil { return }
    g.mu.Lock()
    defer g.mu.Unlock()

    svcID := nodeKey("Service", svc.Namespace, svc.Name)
    for _, pod := range allPods {
        if pod.Namespace != svc.Namespace { continue }
        if labelsMatch(svc.Spec.Selector, pod.Labels) {
            g.upsertEdge(svcID, nodeKey("Pod", pod.Namespace, pod.Name), EdgeSelectedBy)
        }
    }
}

func nodeKey(kind, namespace, name string) string {
    if namespace == "" {
        return kind + "/" + name
    }
    return kind + "/" + namespace + "/" + name
}

func labelsMatch(selector, labels map[string]string) bool {
    for k, v := range selector {
        if labels[k] != v { return false }
    }
    return true
}
```

### 5.3 Event Publisher

Uses the existing Strimzi Kafka cluster. See the full implementation in
`services/agent/internal/publisher/kafka.go`.

Key design points:
- Uses `IBM/sarama` — same library as AlertHub
- `cluster_id` as the Kafka message key → all events from one cluster land on
  the same partition, preserving intra-cluster event ordering
- In-memory circular buffer (default 5,000 events) when Kafka is unavailable
- Background goroutine drains the buffer every 15 seconds
- `V2_8_0_0` protocol version — matches the Strimzi cluster version

### 5.4 Health Detector

```go
// services/agent/internal/health/detector.go

package health

import (
    "context"
    corev1 "k8s.io/api/core/v1"
    "k8s-intelligence/pkg/events"
)

type Detector struct {
    publisher Publisher
}

// OnPodUpdate evaluates a pod's health and publishes events when problems detected.
func (d *Detector) OnPodUpdate(ctx context.Context, pod *corev1.Pod) {
    for _, cs := range pod.Status.ContainerStatuses {
        if cs.State.Waiting != nil {
            switch cs.State.Waiting.Reason {
            case "CrashLoopBackOff":
                d.publisher.Publish(ctx, &events.IntelligenceEvent{
                    Type:     events.EventPodCrashLoopBackOff,
                    Severity: events.SeverityCritical,
                    Resource: resourceRef(pod),
                    NewState: marshalPodStatus(pod),
                    Annotations: map[string]string{
                        "container":    cs.Name,
                        "restart_count": fmt.Sprintf("%d", cs.RestartCount),
                        "reason":       cs.State.Waiting.Message,
                    },
                })
            case "OOMKilled":
                d.publisher.Publish(ctx, &events.IntelligenceEvent{
                    Type:     events.EventPodOOMKilled,
                    Severity: events.SeverityCritical,
                    Resource: resourceRef(pod),
                    Annotations: map[string]string{
                        "container":        cs.Name,
                        "memory_limit":     containerMemoryLimit(pod, cs.Name),
                    },
                })
            case "ImagePullBackOff", "ErrImagePull":
                d.publisher.Publish(ctx, &events.IntelligenceEvent{
                    Type:     events.EventPodImagePullError,
                    Severity: events.SeverityHigh,
                    Resource: resourceRef(pod),
                    Annotations: map[string]string{
                        "container": cs.Name,
                        "image":     cs.Image,
                        "message":   cs.State.Waiting.Message,
                    },
                })
            }
        }
    }
}

// OnNodeUpdate detects node conditions.
func (d *Detector) OnNodeUpdate(ctx context.Context, node *corev1.Node) {
    for _, cond := range node.Status.Conditions {
        switch cond.Type {
        case corev1.NodeReady:
            if cond.Status == corev1.ConditionFalse || cond.Status == corev1.ConditionUnknown {
                d.publisher.Publish(ctx, &events.IntelligenceEvent{
                    Type:     events.EventNodeNotReady,
                    Severity: events.SeverityCritical,
                    Resource: nodeRef(node),
                    Annotations: map[string]string{
                        "reason":  cond.Reason,
                        "message": cond.Message,
                    },
                })
            }
        case corev1.NodeDiskPressure:
            if cond.Status == corev1.ConditionTrue {
                d.publisher.Publish(ctx, &events.IntelligenceEvent{
                    Type:     events.EventNodeDiskPressure,
                    Severity: events.SeverityHigh,
                    Resource: nodeRef(node),
                })
            }
        case corev1.NodeMemoryPressure:
            if cond.Status == corev1.ConditionTrue {
                d.publisher.Publish(ctx, &events.IntelligenceEvent{
                    Type:     events.EventNodeMemoryPressure,
                    Severity: events.SeverityHigh,
                    Resource: nodeRef(node),
                })
            }
        }
    }
}
```

---

## 6. NEO4J GRAPH SCHEMA

### Node Labels and Properties

```cypher
// Cluster
(:Cluster {
  id: STRING,           // unique cluster identifier
  name: STRING,
  version: STRING,      // K8s version
  region: STRING,
  cloud_provider: STRING,
  last_seen: DATETIME
})

// Node (K8s Node)
(:KNode {
  cluster_id: STRING,
  name: STRING,
  uid: STRING,
  status: STRING,       // Ready | NotReady | Unknown
  roles: LIST<STRING>,  // control-plane, worker, etcd
  os_image: STRING,
  kernel_version: STRING,
  container_runtime: STRING,
  allocatable_cpu: STRING,
  allocatable_memory: STRING,
  labels: MAP,
  taints: LIST<MAP>
})

// Namespace
(:Namespace {
  cluster_id: STRING,
  name: STRING,
  phase: STRING,
  labels: MAP
})

// Pod
(:Pod {
  cluster_id: STRING,
  namespace: STRING,
  name: STRING,
  uid: STRING,
  phase: STRING,        // Pending | Running | Succeeded | Failed | Unknown
  status: STRING,
  node_name: STRING,
  service_account: STRING,
  restart_count: INTEGER,
  labels: MAP,
  created_at: DATETIME
})

// Container
(:Container {
  cluster_id: STRING,
  pod_uid: STRING,
  name: STRING,
  image: STRING,
  image_id: STRING,
  ready: BOOLEAN,
  restart_count: INTEGER,
  cpu_request: STRING,
  cpu_limit: STRING,
  memory_request: STRING,
  memory_limit: STRING
})

// Deployment
(:Deployment {
  cluster_id: STRING,
  namespace: STRING,
  name: STRING,
  uid: STRING,
  replicas_desired: INTEGER,
  replicas_ready: INTEGER,
  replicas_available: INTEGER,
  strategy: STRING,
  labels: MAP,
  selector: MAP
})

// StatefulSet
(:StatefulSet {
  cluster_id: STRING,
  namespace: STRING,
  name: STRING,
  uid: STRING,
  replicas: INTEGER,
  ready_replicas: INTEGER,
  service_name: STRING,
  labels: MAP
})

// Service
(:Service {
  cluster_id: STRING,
  namespace: STRING,
  name: STRING,
  uid: STRING,
  type: STRING,         // ClusterIP | NodePort | LoadBalancer | ExternalName
  cluster_ip: STRING,
  selector: MAP,
  ports: LIST<MAP>
})

// Ingress
(:Ingress {
  cluster_id: STRING,
  namespace: STRING,
  name: STRING,
  uid: STRING,
  class: STRING,
  hosts: LIST<STRING>,
  rules: LIST<MAP>
})

// PVC
(:PVC {
  cluster_id: STRING,
  namespace: STRING,
  name: STRING,
  uid: STRING,
  storage_class: STRING,
  access_modes: LIST<STRING>,
  capacity: STRING,
  phase: STRING,        // Pending | Bound | Lost | Released
  volume_name: STRING
})

// PV
(:PV {
  cluster_id: STRING,
  name: STRING,
  uid: STRING,
  storage_class: STRING,
  capacity: STRING,
  access_modes: LIST<STRING>,
  reclaim_policy: STRING,
  phase: STRING,
  driver: STRING        // csi.netapp.io | etc
})

// ConfigMap
(:ConfigMap {
  cluster_id: STRING,
  namespace: STRING,
  name: STRING,
  uid: STRING,
  data_keys: LIST<STRING>  // key names only, never values
})

// Secret
(:Secret {
  cluster_id: STRING,
  namespace: STRING,
  name: STRING,
  uid: STRING,
  type: STRING,
  data_keys: LIST<STRING>  // key names only, NEVER values
})

// ServiceAccount
(:ServiceAccount {
  cluster_id: STRING,
  namespace: STRING,
  name: STRING,
  uid: STRING
})

// HPA
(:HPA {
  cluster_id: STRING,
  namespace: STRING,
  name: STRING,
  uid: STRING,
  min_replicas: INTEGER,
  max_replicas: INTEGER,
  current_replicas: INTEGER,
  target_ref_kind: STRING,
  target_ref_name: STRING,
  metrics: LIST<MAP>
})

// Application (derived from label app.kubernetes.io/name)
(:Application {
  cluster_id: STRING,
  name: STRING,
  version: STRING,
  team: STRING,
  part_of: STRING       // app.kubernetes.io/part-of
})
```

### Relationships

```cypher
// Infrastructure
(:Pod)-[:RUNS_ON {since: DATETIME}]->(:KNode)
(:Pod)-[:IN_NAMESPACE]->(:Namespace)
(:Pod)-[:MANAGED_BY {controller: STRING}]->(:Deployment)
(:Pod)-[:MANAGED_BY {controller: STRING}]->(:StatefulSet)
(:Pod)-[:MANAGED_BY {controller: STRING}]->(:DaemonSet)
(:Container)-[:PART_OF]->(:Pod)

// Network
(:Service)-[:SELECTS {match_type: STRING}]->(:Pod)
(:Ingress)-[:ROUTES_TO {path: STRING, port: INTEGER}]->(:Service)

// Storage
(:Pod)-[:MOUNTS_PVC {mount_path: STRING, read_only: BOOLEAN}]->(:PVC)
(:PVC)-[:BOUND_TO {binding_mode: STRING}]->(:PV)

// Configuration
(:Pod)-[:USES_CONFIGMAP {as: STRING, key: STRING}]->(:ConfigMap)
(:Pod)-[:USES_SECRET {as: STRING, key: STRING}]->(:Secret)
(:Pod)-[:RUNS_AS]->(:ServiceAccount)

// Autoscaling
(:Deployment)-[:SCALED_BY]->(:HPA)
(:StatefulSet)-[:SCALED_BY]->(:HPA)

// Application topology (derived)
(:Application)-[:CALLS {protocol: STRING, port: INTEGER, confidence: FLOAT}]->(:Application)
(:Application)-[:USES_DATABASE {type: STRING, confidence: FLOAT}]->(:StatefulSet)
(:Pod)-[:INSTANCE_OF]->(:Application)

// Cluster membership
(:KNode)-[:IN_CLUSTER]->(:Cluster)
(:Namespace)-[:IN_CLUSTER]->(:Cluster)
```

### Key Queries

```cypher
// Get full blast radius for a failing pod
MATCH (root:Pod {name: $pod_name, namespace: $ns, cluster_id: $cluster_id})
MATCH path = (root)-[:MANAGED_BY|RUNS_ON|MOUNTS_PVC|BOUND_TO|USES_CONFIGMAP*1..5]->(n)
RETURN path

// Find all pods depending on a configmap
MATCH (cm:ConfigMap {name: $cm_name, namespace: $ns})
MATCH (pod:Pod)-[:USES_CONFIGMAP]->(cm)
RETURN pod

// Find root cause upstream chain for a degraded deployment
MATCH (dep:Deployment {name: $name, namespace: $ns, cluster_id: $cluster_id})
MATCH (pod:Pod)-[:MANAGED_BY]->(dep)
OPTIONAL MATCH (pod)-[:RUNS_ON]->(node:KNode)
OPTIONAL MATCH (pod)-[:MOUNTS_PVC]->(pvc:PVC)-[:BOUND_TO]->(pv:PV)
RETURN dep, pod, node, pvc, pv

// Find service → pod selector mismatches
MATCH (svc:Service {cluster_id: $cluster_id})
WHERE NOT (svc)-[:SELECTS]->(:Pod)
  AND svc.selector IS NOT NULL
  AND svc.type <> 'ExternalName'
RETURN svc

// Application dependency chain
MATCH path = (app:Application {name: $app_name})-[:CALLS*1..6]->(dep)
RETURN path ORDER BY length(path)
```

---

## 7. DATABASE SCHEMAS

### PostgreSQL (kip-db)

```sql
-- Cluster registry
CREATE TABLE clusters (
    id              UUID PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    display_name    TEXT,
    region          TEXT,
    cloud_provider  TEXT,
    k8s_version     TEXT,
    agent_version   TEXT,
    status          TEXT NOT NULL DEFAULT 'unknown', -- active | degraded | offline
    last_heartbeat  TIMESTAMPTZ,
    registered_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    metadata        JSONB DEFAULT '{}'
);

-- Agent registry
CREATE TABLE agents (
    id              UUID PRIMARY KEY,
    cluster_id      UUID NOT NULL REFERENCES clusters(id),
    version         TEXT NOT NULL,
    node_name       TEXT,          -- which node it runs on
    status          TEXT NOT NULL DEFAULT 'unknown',
    last_heartbeat  TIMESTAMPTZ,
    config          JSONB DEFAULT '{}',
    registered_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Resource snapshots (periodic full state)
CREATE TABLE resource_snapshots (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id      UUID NOT NULL REFERENCES clusters(id),
    resource_type   TEXT NOT NULL,  -- Pod | Deployment | Node | ...
    namespace       TEXT,
    name            TEXT NOT NULL,
    uid             TEXT,
    spec_hash       TEXT,           -- SHA256 of spec — detect changes
    spec            JSONB NOT NULL,
    status          JSONB,
    labels          JSONB,
    captured_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    INDEX idx_snapshots_cluster_resource (cluster_id, resource_type),
    INDEX idx_snapshots_lookup (cluster_id, resource_type, namespace, name)
);

-- Change records
CREATE TABLE change_records (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id      UUID NOT NULL REFERENCES clusters(id),
    change_type     TEXT NOT NULL,
    -- 'deployment_rollout' | 'config_update' | 'scale_event' | 'rbac_change' |
    -- 'secret_rotation' | 'node_join' | 'node_leave' | 'hpa_scale' | 'git_push'
    resource_type   TEXT NOT NULL,
    namespace       TEXT,
    resource_name   TEXT NOT NULL,
    old_spec_hash   TEXT,
    new_spec_hash   TEXT,
    diff            JSONB,          -- structured diff
    actor           TEXT,           -- user/controller who made the change
    source          TEXT,           -- informer | argocd | helm | github
    occurred_at     TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    INDEX idx_changes_cluster_time (cluster_id, occurred_at DESC),
    INDEX idx_changes_resource (cluster_id, resource_type, resource_name)
);

-- Investigation records
CREATE TABLE investigations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id      UUID NOT NULL REFERENCES clusters(id),
    incident_id     TEXT,           -- AlertHub incident ID
    trigger_type    TEXT NOT NULL,  -- 'alert' | 'manual' | 'automatic'
    status          TEXT NOT NULL DEFAULT 'pending',
    -- pending | running | completed | failed
    started_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at    TIMESTAMPTZ,
    duration_ms     INTEGER,

    -- Input
    affected_resources  JSONB,
    alert_context       JSONB,

    -- Output
    rca_result      JSONB,          -- root cause, confidence, evidence
    dependency_chain JSONB,
    recent_changes  JSONB,
    recommendations JSONB,
    llm_summary     TEXT,           -- evidence-grounded LLM narrative
    evidence_grade  TEXT,           -- A/B/C/D/F

    error           TEXT
);

-- RCA hypotheses (granular, per investigation)
CREATE TABLE rca_hypotheses (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    investigation_id UUID NOT NULL REFERENCES investigations(id),
    rank            INTEGER NOT NULL,
    entity_id       TEXT NOT NULL,
    entity_label    TEXT NOT NULL,
    entity_kind     TEXT NOT NULL,
    confidence      FLOAT NOT NULL,
    evidence        JSONB NOT NULL,
    rejected        BOOLEAN NOT NULL DEFAULT FALSE,
    rejection_reason TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    INDEX idx_hyp_investigation (investigation_id, rank)
);

-- Configuration validation findings
CREATE TABLE config_violations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id      UUID NOT NULL REFERENCES clusters(id),
    rule_id         TEXT NOT NULL,
    severity        TEXT NOT NULL,  -- critical | high | medium | low
    resource_type   TEXT NOT NULL,
    namespace       TEXT,
    resource_name   TEXT NOT NULL,
    message         TEXT NOT NULL,
    details         JSONB,
    first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at     TIMESTAMPTZ,
    resolved        BOOLEAN NOT NULL DEFAULT FALSE,
    UNIQUE (cluster_id, rule_id, resource_type, namespace, resource_name)
);

-- Forecasts
CREATE TABLE forecasts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id      UUID NOT NULL REFERENCES clusters(id),
    forecast_type   TEXT NOT NULL,
    -- 'pvc_exhaustion' | 'node_cpu' | 'node_memory' | 'cert_expiry' | 'pod_eviction'
    resource_type   TEXT NOT NULL,
    namespace       TEXT,
    resource_name   TEXT NOT NULL,
    current_value   FLOAT,
    threshold       FLOAT,
    predicted_breach TIMESTAMPTZ,
    confidence_interval_lower TIMESTAMPTZ,
    confidence_interval_upper TIMESTAMPTZ,
    trend_slope     FLOAT,          -- units per day
    data_points     INTEGER,
    computed_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    INDEX idx_forecasts_cluster (cluster_id, forecast_type),
    INDEX idx_forecasts_breach (predicted_breach)
);
```

### TimescaleDB (kip-metrics) — Resource Trends

```sql
-- Enable TimescaleDB
CREATE EXTENSION IF NOT EXISTS timescaledb;

-- Resource utilization timeseries
CREATE TABLE resource_metrics (
    time            TIMESTAMPTZ NOT NULL,
    cluster_id      UUID NOT NULL,
    namespace       TEXT,
    resource_name   TEXT NOT NULL,
    resource_kind   TEXT NOT NULL,  -- Pod | Node | PVC | Container
    metric_name     TEXT NOT NULL,
    -- 'cpu_usage_cores' | 'memory_usage_bytes' | 'pvc_used_bytes' |
    -- 'pvc_capacity_bytes' | 'node_allocatable_cpu' | 'restart_count'
    value           FLOAT NOT NULL,
    labels          JSONB
);
SELECT create_hypertable('resource_metrics', 'time', chunk_time_interval => INTERVAL '1 day');
CREATE INDEX ON resource_metrics (cluster_id, resource_name, metric_name, time DESC);

-- Cluster capacity trends
CREATE TABLE cluster_capacity (
    time                    TIMESTAMPTZ NOT NULL,
    cluster_id              UUID NOT NULL,
    total_nodes             INTEGER,
    ready_nodes             INTEGER,
    total_cpu_cores         FLOAT,
    allocatable_cpu_cores   FLOAT,
    requested_cpu_cores     FLOAT,
    total_memory_bytes      BIGINT,
    allocatable_memory_bytes BIGINT,
    requested_memory_bytes  BIGINT,
    total_pods              INTEGER,
    running_pods            INTEGER,
    total_pvcs              INTEGER,
    bound_pvcs              INTEGER
);
SELECT create_hypertable('cluster_capacity', 'time', chunk_time_interval => INTERVAL '1 hour');

-- PVC growth tracking (for exhaustion forecasting)
CREATE TABLE pvc_usage (
    time            TIMESTAMPTZ NOT NULL,
    cluster_id      UUID NOT NULL,
    namespace       TEXT NOT NULL,
    pvc_name        TEXT NOT NULL,
    used_bytes      BIGINT NOT NULL,
    capacity_bytes  BIGINT NOT NULL,
    utilization_pct FLOAT NOT NULL
);
SELECT create_hypertable('pvc_usage', 'time', chunk_time_interval => INTERVAL '6 hours');
CREATE INDEX ON pvc_usage (cluster_id, pvc_name, time DESC);
```

---

## 8. gRPC CONTRACTS

```protobuf
// proto/intelligence/v1/intelligence.proto
syntax = "proto3";
package intelligence.v1;
import "google/protobuf/timestamp.proto";

service IntelligenceService {
    // Core investigation — called by AlertHub
    rpc InvestigateIncident(InvestigationRequest) returns (InvestigationResponse);

    // Topology
    rpc GetClusterTopology(TopologyRequest) returns (TopologyResponse);
    rpc GetDependencyChain(DependencyRequest) returns (DependencyResponse);
    rpc GetBlastRadius(BlastRadiusRequest) returns (BlastRadiusResponse);

    // RCA
    rpc GetInvestigation(GetInvestigationRequest) returns (InvestigationResponse);
    rpc StreamInvestigationProgress(GetInvestigationRequest) returns (stream InvestigationUpdate);

    // Change intelligence
    rpc GetChangeHistory(ChangeHistoryRequest) returns (ChangeHistoryResponse);
    rpc CorrelateChanges(ChangeCorrelationRequest) returns (ChangeCorrelationResponse);

    // Configuration intelligence
    rpc ValidateConfiguration(ConfigValidationRequest) returns (ConfigValidationResponse);
    rpc GetConfigViolations(ConfigViolationsRequest) returns (ConfigViolationsResponse);

    // Forecasting
    rpc GetForecasts(ForecastRequest) returns (ForecastResponse);

    // Resource lookup
    rpc SearchResources(SearchRequest) returns (SearchResponse);

    // Events
    rpc StreamClusterEvents(EventStreamRequest) returns (stream ClusterEvent);
}

message InvestigationRequest {
    string incident_id       = 1;  // AlertHub incident ID
    string cluster_id        = 2;
    repeated ResourceRef affected_resources = 3;
    google.protobuf.Timestamp incident_time  = 4;
    map<string, string> alert_context        = 5;
    bool   async             = 6;  // if true, return immediately with investigation_id
}

message InvestigationResponse {
    string investigation_id  = 1;
    InvestigationStatus status = 2;
    RCAResult rca            = 3;
    repeated Evidence evidence           = 4;
    repeated Hypothesis rejected_hypotheses = 5;
    DependencyChain dependency_chain     = 6;
    repeated ChangeRecord recent_changes = 7;
    repeated RecommendedAction actions   = 8;
    float confidence                     = 9;
    string summary                       = 10; // LLM, evidence-grounded
    string evidence_grade                = 11; // A/B/C/D/F
}

message RCAResult {
    string entity_id         = 1;
    string entity_kind       = 2;
    string entity_namespace  = 3;
    string entity_name       = 4;
    float confidence         = 5;
    string confidence_band   = 6;  // HIGH | MEDIUM | LOW | VERY_LOW
    repeated string evidence_sources = 7;
    string failure_mode      = 8;  // "OOMKilled" | "CrashLoopBackOff" | ...
    string failure_domain    = 9;  // "compute" | "storage" | "network" | ...
}

message Evidence {
    string type              = 1;  // "k8s_event" | "metric" | "log" | "change" | "topology"
    string source            = 2;
    string description       = 3;
    float strength           = 4;  // 0-1
    google.protobuf.Timestamp occurred_at = 5;
    map<string, string> data = 6;
}

message Hypothesis {
    string entity_id         = 1;
    string entity_label      = 2;
    float confidence         = 3;
    string rejected_reason   = 4;
    repeated Evidence supporting_evidence = 5;
    repeated Evidence refuting_evidence   = 6;
}

message DependencyChain {
    repeated DependencyNode nodes = 1;
    repeated DependencyEdge edges = 2;
    int32 depth                   = 3;
}

message ChangeRecord {
    string change_type       = 1;
    string resource_kind     = 2;
    string namespace         = 3;
    string resource_name     = 4;
    string actor             = 5;
    google.protobuf.Timestamp occurred_at = 6;
    int64  seconds_before_incident = 7;
    float  correlation_score = 8;  // how likely this change caused the incident
}

message ResourceRef {
    string kind      = 1;
    string namespace = 2;
    string name      = 3;
    string uid       = 4;
}

enum InvestigationStatus {
    INVESTIGATION_STATUS_UNSPECIFIED = 0;
    INVESTIGATION_STATUS_PENDING     = 1;
    INVESTIGATION_STATUS_RUNNING     = 2;
    INVESTIGATION_STATUS_COMPLETED   = 3;
    INVESTIGATION_STATUS_FAILED      = 4;
}
```

---

## 9. REST API (k8s-intelligence-api)

```
# Cluster management
GET    /api/v1/clusters                           # list all clusters
GET    /api/v1/clusters/:cluster_id               # cluster overview
GET    /api/v1/clusters/:cluster_id/health        # health summary

# Topology
GET    /api/v1/clusters/:cluster_id/topology      # full topology graph
GET    /api/v1/clusters/:cluster_id/topology/nodes/:kind/:namespace/:name
GET    /api/v1/clusters/:cluster_id/topology/dependencies/:kind/:namespace/:name
GET    /api/v1/clusters/:cluster_id/topology/blast-radius/:kind/:namespace/:name

# Investigations (AlertHub calls these)
POST   /api/v1/investigations                     # trigger investigation
GET    /api/v1/investigations/:id                 # get result
GET    /api/v1/investigations/:id/stream          # SSE stream for async

# RCA
GET    /api/v1/clusters/:cluster_id/rca/:investigation_id

# Changes
GET    /api/v1/clusters/:cluster_id/changes       # ?since=&until=&resource=
GET    /api/v1/clusters/:cluster_id/changes/correlate # correlate with incident time

# Configuration intelligence
GET    /api/v1/clusters/:cluster_id/config/violations
POST   /api/v1/clusters/:cluster_id/config/validate  # validate a manifest

# Forecasting
GET    /api/v1/clusters/:cluster_id/forecasts
GET    /api/v1/clusters/:cluster_id/forecasts/pvc
GET    /api/v1/clusters/:cluster_id/forecasts/capacity

# Resource search
GET    /api/v1/search?q=&cluster=&kind=&namespace=

# Events (for AlertHub incident context)
GET    /api/v1/clusters/:cluster_id/events?since=&resource=
```

---

## 10. RCA ENGINE

```go
// services/core/internal/rca/engine.go

package rca

import (
    "context"
    "sort"
    "time"
)

type Engine struct {
    neo4j     Neo4jClient
    changes   ChangeStore
    metrics   MetricsClient
    events    EventsClient
}

type InvestigationRequest struct {
    ClusterID          string
    AffectedResources  []ResourceRef
    IncidentTime       time.Time
    AlertContext       map[string]string
}

type InvestigationResult struct {
    RootCause          *Hypothesis
    AllHypotheses      []*Hypothesis
    RejectedHypotheses []*Hypothesis
    Evidence           []Evidence
    DependencyChain    *DependencyChain
    RecentChanges      []ChangeRecord
    Confidence         float64
}

func (e *Engine) Investigate(ctx context.Context, req InvestigationRequest) (*InvestigationResult, error) {
    result := &InvestigationResult{}

    // Step 1: Get topology snapshot at incident time from Neo4j.
    // This is critical: use the topology AS IT WAS, not as it is now.
    topology, err := e.neo4j.GetTopologySnapshot(ctx, req.ClusterID, req.IncidentTime)
    if err != nil {
        return nil, err
    }

    // Step 2: Find the epicenter nodes (what was directly alerting).
    epicenters := e.identifyEpicenters(topology, req.AffectedResources)

    // Step 3: Traverse upstream dependencies from epicenters.
    // BFS upward: Pod → Deployment → ConfigMap/Secret/PVC → PV
    depChain := e.traverseUpstream(topology, epicenters, 8)
    result.DependencyChain = depChain

    // Step 4: Gather evidence for every node in the dependency chain.
    // Evidence sources: K8s events, metrics, recent changes.
    evidence := e.gatherEvidence(ctx, depChain, req.IncidentTime)
    result.Evidence = evidence

    // Step 5: Get recent changes in the lookback window (6 hours before incident).
    lookback := req.IncidentTime.Add(-6 * time.Hour)
    changes, err := e.changes.GetRange(ctx, req.ClusterID, lookback, req.IncidentTime)
    if err == nil {
        result.RecentChanges = changes
    }

    // Step 6: Build hypotheses — one per node in the dependency chain.
    hypotheses := e.buildHypotheses(depChain, evidence, changes, req.IncidentTime)

    // Step 7: Score each hypothesis.
    // Scoring factors:
    //   - Temporal proximity to first symptom (closer = higher score)
    //   - Number of supporting evidence items
    //   - Evidence strength (K8s event > metric anomaly > topology proximity)
    //   - Infra level (BM/Node > VM > Pod — upstream failures cause downstream symptoms)
    //   - Change correlation (recent change to this resource = strong positive signal)
    e.scoreHypotheses(hypotheses, evidence, changes, req.IncidentTime)

    // Step 8: Reject hypotheses with insufficient evidence.
    confirmed, rejected := e.filterHypotheses(hypotheses)
    result.RejectedHypotheses = rejected

    // Step 9: Sort confirmed hypotheses by score descending.
    sort.Slice(confirmed, func(i, j int) bool {
        return confirmed[i].Confidence > confirmed[j].Confidence
    })
    result.AllHypotheses = confirmed
    if len(confirmed) > 0 {
        result.RootCause = confirmed[0]
        result.Confidence = confirmed[0].Confidence
    }

    return result, nil
}

func (e *Engine) scoreHypotheses(
    hyps []*Hypothesis,
    evidence []Evidence,
    changes []ChangeRecord,
    incidentTime time.Time,
) {
    for _, h := range hyps {
        score := 0.0

        // Score: K8s events directly on this resource
        for _, ev := range evidence {
            if ev.ResourceRef == h.EntityID && ev.Type == "k8s_event" {
                score += 0.30 * ev.Strength
            }
        }

        // Score: Recent change to this resource
        for _, ch := range changes {
            if ch.ResourceName == h.EntityName && ch.ResourceKind == h.EntityKind {
                lag := incidentTime.Sub(ch.OccurredAt).Seconds()
                if lag > 0 && lag < 3600 {
                    // Change within 1 hour before incident: strong signal
                    changeFactor := 1.0 - (lag / 3600)
                    score += 0.40 * changeFactor
                }
            }
        }

        // Score: Infrastructure level prior
        // Bare metal and node failures cause downstream cascades
        switch h.EntityKind {
        case "Node":
            score *= 1.40
        case "PersistentVolume", "StorageClass":
            score *= 1.30
        case "StatefulSet":
            score *= 1.20
        case "Deployment":
            score *= 1.10
        case "Pod":
            score *= 0.85 // pods are usually symptoms, not causes
        case "ConfigMap", "Secret":
            score *= 1.15 // config errors are often root causes
        }

        h.Confidence = clamp(score, 0, 1)
    }
}

func (e *Engine) filterHypotheses(hyps []*Hypothesis) (confirmed, rejected []*Hypothesis) {
    for _, h := range hyps {
        if h.Confidence < 0.15 {
            h.RejectedReason = fmt.Sprintf("insufficient confidence (%.3f < 0.15)", h.Confidence)
            rejected = append(rejected, h)
        } else if len(h.SupportingEvidence) == 0 {
            h.RejectedReason = "no supporting evidence found"
            rejected = append(rejected, h)
        } else {
            confirmed = append(confirmed, h)
        }
    }
    return
}

func clamp(v, min, max float64) float64 {
    if v < min { return min }
    if v > max { return max }
    return v
}
```

---

## 11. CONFIGURATION INTELLIGENCE

```go
// services/core/internal/config/validator.go

package config

import (
    "fmt"
    corev1 "k8s.io/api/core/v1"
    appsv1 "k8s.io/api/apps/v1"
)

type Violation struct {
    RuleID       string
    Severity     string // critical | high | medium | low
    ResourceKind string
    Namespace    string
    ResourceName string
    Message      string
    Remediation  string
}

var validationRules = []Rule{
    {
        ID:       "PROBE_MISSING_READINESS",
        Severity: "high",
        Check: func(obj interface{}) []Violation {
            pod, ok := extractPodSpec(obj)
            if !ok { return nil }
            var violations []Violation
            for _, c := range pod.Spec.Containers {
                if c.ReadinessProbe == nil {
                    violations = append(violations, Violation{
                        RuleID:      "PROBE_MISSING_READINESS",
                        Severity:    "high",
                        Message:     fmt.Sprintf("container %q has no readinessProbe — service may receive traffic before ready", c.Name),
                        Remediation: "Add a readinessProbe with appropriate httpGet, tcpSocket, or exec handler",
                    })
                }
            }
            return violations
        },
    },
    {
        ID:       "PROBE_MISSING_LIVENESS",
        Severity: "medium",
        Check: func(obj interface{}) []Violation {
            // Similar to readiness
        },
    },
    {
        ID:       "RESOURCE_LIMITS_MISSING",
        Severity: "high",
        Check: func(obj interface{}) []Violation {
            pod, ok := extractPodSpec(obj)
            if !ok { return nil }
            var violations []Violation
            for _, c := range pod.Spec.Containers {
                if c.Resources.Limits == nil {
                    violations = append(violations, Violation{
                        RuleID:      "RESOURCE_LIMITS_MISSING",
                        Severity:    "high",
                        Message:     fmt.Sprintf("container %q has no resource limits — unbounded CPU/memory risk", c.Name),
                        Remediation: "Set resources.limits.cpu and resources.limits.memory",
                    })
                }
            }
            return violations
        },
    },
    {
        ID:       "IMAGE_LATEST_TAG",
        Severity: "medium",
        Check: func(obj interface{}) []Violation {
            pod, ok := extractPodSpec(obj)
            if !ok { return nil }
            var violations []Violation
            for _, c := range pod.Spec.Containers {
                if strings.HasSuffix(c.Image, ":latest") || !strings.Contains(c.Image, ":") {
                    violations = append(violations, Violation{
                        RuleID:      "IMAGE_LATEST_TAG",
                        Severity:    "medium",
                        Message:     fmt.Sprintf("container %q uses :latest tag — breaks reproducibility and rollbacks", c.Name),
                        Remediation: "Pin image to a specific digest or semantic version tag",
                    })
                }
            }
            return violations
        },
    },
    {
        ID:       "SINGLE_REPLICA_NO_PDB",
        Severity: "high",
        Check: func(obj interface{}) []Violation {
            dep, ok := obj.(*appsv1.Deployment)
            if !ok { return nil }
            if dep.Spec.Replicas != nil && *dep.Spec.Replicas == 1 {
                return []Violation{{
                    RuleID:      "SINGLE_REPLICA_NO_PDB",
                    Severity:    "high",
                    Message:     fmt.Sprintf("deployment %q has 1 replica — node drain will cause downtime", dep.Name),
                    Remediation: "Increase replicas to ≥2 and add a PodDisruptionBudget",
                }}
            }
            return nil
        },
    },
    {
        ID:       "HPA_NO_RESOURCE_REQUESTS",
        Severity: "critical",
        // HPA based on CPU/memory requires requests to be set —
        // otherwise HPA cannot compute utilization percentage.
        Check: func(obj interface{}) []Violation { /* ... */ },
    },
    {
        ID:       "SELECTOR_MISMATCH",
        Severity: "critical",
        // Service selector matches zero pods in the topology graph.
        // This check runs against the topology graph, not just the manifest.
        Check: func(obj interface{}) []Violation { /* ... */ },
    },
    {
        ID:       "SECRET_REF_NOT_FOUND",
        Severity: "critical",
        // Pod references a secret that doesn't exist in the namespace.
        Check: func(obj interface{}) []Violation { /* ... */ },
    },
    {
        ID:       "CONFIGMAP_REF_NOT_FOUND",
        Severity: "critical",
        Check: func(obj interface{}) []Violation { /* ... */ },
    },
    {
        ID:       "RBAC_WILDCARD_RESOURCES",
        Severity: "high",
        // ClusterRole uses resources: ["*"] — overly broad
        Check: func(obj interface{}) []Violation { /* ... */ },
    },
    {
        ID:       "NETPOL_ALLOW_ALL",
        Severity: "high",
        // NetworkPolicy with empty podSelector + empty ingress = allow all
        Check: func(obj interface{}) []Violation { /* ... */ },
    },
    {
        ID:       "PVC_NOT_BOUND",
        Severity: "critical",
        // PVC in Pending state — pod will not start
        Check: func(obj interface{}) []Violation { /* ... */ },
    },
}
```

---

## 12. FORECASTING ENGINE

```go
// services/core/internal/forecast/engine.go

package forecast

import (
    "context"
    "math"
    "time"
)

type Engine struct {
    metrics MetricsStore
}

type ForecastResult struct {
    Target           ForecastTarget
    ResourceName     string
    Namespace        string
    ClusterID        string
    CurrentValue     float64
    Threshold        float64    // fill level at which to alert (e.g. 0.85 for 85%)
    PredictedBreach  time.Time  // when current_value will cross threshold
    ConfidenceLow    time.Time
    ConfidenceHigh   time.Time
    TrendSlopePerDay float64
    DataPoints       int
    Confidence       float64    // model fit quality 0-1
}

// ForecastPVCExhaustion uses linear regression over 30-day usage history.
// Returns nil if insufficient data.
func (e *Engine) ForecastPVCExhaustion(ctx context.Context, clusterID, namespace, pvcName string) (*ForecastResult, error) {
    // Fetch last 30 days of hourly samples
    samples, err := e.metrics.GetPVCUsage(ctx, clusterID, namespace, pvcName, 30*24*time.Hour)
    if err != nil || len(samples) < 24 {
        return nil, fmt.Errorf("insufficient data: need 24+ samples, have %d", len(samples))
    }

    // Linear regression: y = slope * x + intercept
    slope, intercept, r2 := linearRegression(samples)

    // Project when usage hits threshold (85%)
    capacity := samples[len(samples)-1].Capacity
    threshold := capacity * 0.85
    currentUsage := samples[len(samples)-1].Used

    if slope <= 0 {
        // Not growing — no exhaustion forecast
        return &ForecastResult{
            CurrentValue:    currentUsage / capacity,
            Threshold:       0.85,
            TrendSlopePerDay: slope,
            DataPoints:      len(samples),
            Confidence:      r2,
        }, nil
    }

    // Days until threshold: (threshold - current) / slope
    daysUntilBreach := (threshold - currentUsage) / slope
    breach := time.Now().Add(time.Duration(daysUntilBreach * 24) * time.Hour)

    // Confidence interval: use R² to estimate uncertainty
    uncertainty := (1 - r2) * daysUntilBreach * 0.5
    return &ForecastResult{
        Target:           ForecastPVCExhaustion,
        ClusterID:        clusterID,
        Namespace:        namespace,
        ResourceName:     pvcName,
        CurrentValue:     currentUsage / capacity,
        Threshold:        0.85,
        PredictedBreach:  breach,
        ConfidenceLow:    breach.Add(-time.Duration(uncertainty * 24) * time.Hour),
        ConfidenceHigh:   breach.Add(time.Duration(uncertainty * 24) * time.Hour),
        TrendSlopePerDay: slope / capacity * 100, // % per day
        DataPoints:       len(samples),
        Confidence:       r2,
    }, nil
}

// ForecastNodeExhaustion predicts when a node will exhaust allocatable CPU or memory.
func (e *Engine) ForecastNodeExhaustion(ctx context.Context, clusterID, nodeName, metric string) (*ForecastResult, error) {
    // Same linear regression approach with node capacity as ceiling
    samples, _ := e.metrics.GetNodeMetric(ctx, clusterID, nodeName, metric, 14*24*time.Hour)
    if len(samples) < 12 { return nil, nil }
    // ... same regression logic
    return nil, nil
}

// ForecastCertificateExpiry is deterministic — no regression needed.
func (e *Engine) ForecastCertificateExpiry(ctx context.Context, clusterID string) ([]ForecastResult, error) {
    // Scan secrets of type kubernetes.io/tls
    // Parse NotAfter from TLS cert data
    // Return sorted by expiry asc
    return nil, nil
}

func linearRegression(samples []MetricSample) (slope, intercept, r2 float64) {
    n := float64(len(samples))
    var sumX, sumY, sumXY, sumX2 float64
    t0 := samples[0].Time.Unix()

    for _, s := range samples {
        x := float64(s.Time.Unix()-t0) / 86400 // days since first sample
        y := s.Used
        sumX += x
        sumY += y
        sumXY += x * y
        sumX2 += x * x
    }

    slope = (n*sumXY - sumX*sumY) / (n*sumX2 - sumX*sumX)
    intercept = (sumY - slope*sumX) / n

    // R² — coefficient of determination
    yMean := sumY / n
    var ssTot, ssRes float64
    for _, s := range samples {
        x := float64(s.Time.Unix()-t0) / 86400
        predicted := slope*x + intercept
        ssTot += math.Pow(s.Used-yMean, 2)
        ssRes += math.Pow(s.Used-predicted, 2)
    }
    if ssTot > 0 {
        r2 = 1 - ssRes/ssTot
    }
    return
}
```

---

## 13. ALERTHUB INTEGRATION

### AlertHub → KIP Investigation Flow

```go
// services/api/internal/alerthub/handler.go

package alerthub

// AlertHub calls this endpoint when an incident needs K8s investigation.
// POST /api/v1/investigations
type TriggerInvestigationRequest struct {
    IncidentID         string            `json:"incident_id"`
    ClusterID          string            `json:"cluster_id"`
    AffectedResources  []ResourceRef     `json:"affected_resources"`
    IncidentTime       time.Time         `json:"incident_time"`
    AlertContext       map[string]string `json:"alert_context"`
    // Alert metadata from Dynatrace/Prometheus
    // includes: alert.title, alert.severity, alert.source_id
}

type TriggerInvestigationResponse struct {
    InvestigationID string `json:"investigation_id"`
    Status          string `json:"status"`
    // If async=false: full result included immediately
    Result *InvestigationResult `json:"result,omitempty"`
}
```

The integration flow in AlertHub is:

1. Incident created in `alert_pipeline.go`
2. `enrichIncidentV2` calls the KIP API: `POST /api/v1/investigations`
3. KIP returns `investigation_id` immediately (async)
4. AlertHub stores `rca_investigation_id` on the incident
5. KIP publishes result to `kip.investigations.results` Kafka topic
6. AlertHub collector subscribes and updates the incident with KIP's RCA

The AlertHub `triggerRCA` function is the integration point — replace or augment it
to call the KIP API when a KIP instance is configured.

---

## 14. LLM SERVICE (Evidence-Grounded Only)

```go
// services/llm/internal/grounding/grounder.go

package grounding

// LLM NEVER makes decisions. It only explains structured evidence.
// The grounding layer builds a structured context and validates
// that the LLM output cites the evidence it was given.

type EvidenceGrounder struct {
    ollamaClient OllamaClient
}

type GroundedExplanation struct {
    Narrative      string   `json:"narrative"`
    EvidenceGrade  string   `json:"evidence_grade"` // A/B/C/D/F
    CitedEvidence  []string `json:"cited_evidence"`
    MissingEvidence []string `json:"missing_evidence"`
}

func (g *EvidenceGrounder) ExplainInvestigation(
    ctx context.Context,
    result *InvestigationResult,
) (*GroundedExplanation, error) {
    // Grade the evidence before calling LLM
    grade := gradeEvidence(result)
    if grade == "F" {
        return &GroundedExplanation{
            Narrative:       "Insufficient evidence for explanation.",
            EvidenceGrade:   "F",
            MissingEvidence: result.MissingEvidence,
        }, nil
    }

    // Build a structured prompt — LLM narrates facts, does not reason
    prompt := buildGroundedPrompt(result)

    response, err := g.ollamaClient.Generate(ctx, OllamaRequest{
        Model:  "qwen2.5:3b",
        Prompt: prompt,
        Options: OllamaOptions{Temperature: 0.1, NumPredict: 400},
    })
    if err != nil {
        return nil, err
    }

    return &GroundedExplanation{
        Narrative:     strings.TrimSpace(response.Response),
        EvidenceGrade: grade,
    }, nil
}

func buildGroundedPrompt(result *InvestigationResult) string {
    var sb strings.Builder
    sb.WriteString("You are an SRE assistant. Summarize the following Kubernetes investigation findings in 4-5 sentences.\n")
    sb.WriteString("RULES: Only use facts from the evidence below. Do not add speculation. Begin each claim with its evidence source in [brackets].\n\n")

    if result.RootCause != nil {
        sb.WriteString(fmt.Sprintf("ROOT CAUSE: %s (%s) — confidence %.0f%%\n",
            result.RootCause.EntityLabel, result.RootCause.EntityKind,
            result.RootCause.Confidence*100))
    }

    sb.WriteString("\nEVIDENCE:\n")
    for _, ev := range result.Evidence {
        if ev.Strength >= 0.5 {
            sb.WriteString(fmt.Sprintf("  [%s] %s\n", ev.Type, ev.Description))
        }
    }

    if len(result.RecentChanges) > 0 {
        sb.WriteString("\nRECENT CHANGES (before incident):\n")
        for _, ch := range result.RecentChanges {
            sb.WriteString(fmt.Sprintf("  [change] %s %s/%s by %s (%d minutes before)\n",
                ch.ChangeType, ch.ResourceKind, ch.ResourceName,
                ch.Actor, ch.SecondsBeforeIncident/60))
        }
    }

    if len(result.RejectedHypotheses) > 0 {
        sb.WriteString("\nRULED OUT:\n")
        for _, h := range result.RejectedHypotheses[:min(3, len(result.RejectedHypotheses))] {
            sb.WriteString(fmt.Sprintf("  %s — %s\n", h.EntityLabel, h.RejectedReason))
        }
    }

    sb.WriteString("\nWrite a 4-5 sentence technical summary. Start each sentence with the evidence source in brackets.")
    return sb.String()
}
```

---

## 15. HELM CHARTS

### kip-agent (deployed per cluster)

```yaml
# helm/kip-agent/values.yaml
replicaCount: 1

image:
  repository: registry.example.com/kip-agent
  tag: "latest"
  pullPolicy: IfNotPresent

clusterID: ""       # REQUIRED: unique identifier for this cluster
# Strimzi Kafka bootstrap — reuses the AlertHub Kafka cluster
kafkaBrokers: "alerthub-kafka-kafka-bootstrap.aileron.svc.cluster.local:9092"
agentID: ""         # auto-generated if empty

# Max events buffered locally when Kafka is temporarily unavailable
localBufferMax: 5000

resources:
  requests:
    cpu: 50m
    memory: 64Mi
  limits:
    cpu: 200m
    memory: 256Mi

watchNamespaces: []
excludeNamespaces:
  - kube-system
  - kube-public
  - kube-node-lease

secretsMetadataOnly: true
resyncPeriod: 30s

serviceAccount:
  create: true
  name: kip-agent

rbac:
  create: true

tolerations:
  - operator: Exists

nodeSelector: {}
```

```yaml
# helm/kip-agent/templates/clusterrole.yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: {{ include "kip-agent.fullname" . }}
rules:
  # Core resources — list and watch only
  - apiGroups: [""]
    resources:
      - pods
      - nodes
      - namespaces
      - services
      - endpoints
      - persistentvolumeclaims
      - persistentvolumes
      - configmaps
      - events
      - serviceaccounts
    verbs: ["list", "watch", "get"]
  # Secrets: metadata only
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["list", "watch"]
  # Apps
  - apiGroups: ["apps"]
    resources:
      - deployments
      - statefulsets
      - daemonsets
      - replicasets
    verbs: ["list", "watch", "get"]
  # Batch
  - apiGroups: ["batch"]
    resources: ["jobs", "cronjobs"]
    verbs: ["list", "watch", "get"]
  # Networking
  - apiGroups: ["networking.k8s.io"]
    resources: ["ingresses", "networkpolicies"]
    verbs: ["list", "watch", "get"]
  - apiGroups: ["discovery.k8s.io"]
    resources: ["endpointslices"]
    verbs: ["list", "watch", "get"]
  # Storage
  - apiGroups: ["storage.k8s.io"]
    resources: ["storageclasses", "volumeattachments"]
    verbs: ["list", "watch", "get"]
  # RBAC
  - apiGroups: ["rbac.authorization.k8s.io"]
    resources:
      - roles
      - rolebindings
      - clusterroles
      - clusterrolebindings
    verbs: ["list", "watch", "get"]
  # Autoscaling
  - apiGroups: ["autoscaling"]
    resources: ["horizontalpodautoscalers"]
    verbs: ["list", "watch", "get"]
```

### kip-hub (deployed once)

```yaml
# helm/kip-hub/values.yaml
collector:
  replicaCount: 2
  image:
    repository: registry.example.com/kip-collector
    tag: "latest"

core:
  replicaCount: 2
  image:
    repository: registry.example.com/kip-core
    tag: "latest"

api:
  replicaCount: 2
  image:
    repository: registry.example.com/kip-api
    tag: "latest"
  service:
    type: ClusterIP
    port: 8080
  grpc:
    port: 9090

llm:
  replicaCount: 1
  image:
    repository: registry.example.com/kip-llm
    tag: "latest"
  ollamaURL: "http://ollama.aileron.svc.cluster.local:11434"
  model: "qwen2.5:3b"

# Kafka: uses the existing Strimzi cluster — no new Kafka deployment in kip-hub
kafka:
  bootstrapServers: "alerthub-kafka-kafka-bootstrap.aileron.svc.cluster.local:9092"
  # KafkaTopic CRDs applied from deployments/kafka/kafka-topics.yaml

neo4j:
  enabled: true
  neo4j:
    password: ""    # set via secret

postgresql:
  enabled: true
  auth:
    database: kipdb
    username: kip
    password: ""    # set via secret

timescaledb:
  enabled: true
  auth:
    database: kipmetrics

redis:
  enabled: true
  architecture: replication
  sentinel:
    enabled: true
```

---

## 16. SECURITY MODEL

### Agent Permissions
- ClusterRole: list/watch/get only — zero write permissions
- Secrets: metadata only (no data values ever leave the cluster)
- No exec, no port-forward, no create/update/delete
- ServiceAccount namespaced to kip-system

### Multi-Cluster Identity
- Each cluster agent has a unique JWT signed by the KIP hub's CA
- JWT contains: cluster_id, agent_id, issued_at, expiry (24h)
- Hub validates agent identity on every Kafka connection (SASL/SCRAM or mTLS via Strimzi)
- Credentials stored in Kubernetes Secrets, mounted as files
- Auto-rotation: agent requests new JWT 1h before expiry

### Network Security
- Agent → Kafka broker: TLS + SASL/SCRAM (Strimzi KafkaUser CRD)
- Strimzi encrypts broker-to-broker replication via TLS
- Internal services: gRPC with mutual TLS (cert-manager issues certs)
- No sensitive data (secret values, credentials) transmitted

### Data Classification
- Secret values: NEVER transmitted, NEVER stored
- Secret metadata (name, type, key names): transmitted and stored
- ConfigMap keys: transmitted and stored; values optionally excluded
- Metrics: transmitted and stored in TimescaleDB
- Topology: transmitted and stored in Neo4j

---

## 17. MULTI-CLUSTER DESIGN

```
Production Cluster A (RNO)
  ┌──────────────────────────────┐
  │  kip-agent                   │
  │  kip-agent          │──────────────────┐
  └──────────────────────────────┘                  │
                                                     │
Production Cluster B (MDN)                           │
  ┌──────────────────────────────┐                  │
  │  kip-agent                   │                  │
  │  kip-agent          │──────────────────┤
  └──────────────────────────────┘                  │
                                                     ▼
Dev Cluster                             KIP Hub (management cluster)
  ┌──────────────────────────────┐    ┌─────────────────────────────────┐
  │  kip-agent                   │    │  Strimzi Kafka (existing AlertHub cluster) │
  │  kip-agent          │───▶│  kip-collector (2 replicas)      │
  └──────────────────────────────┘    │  kip-core      (2 replicas)      │
                                      │  kip-api       (2 replicas)      │
                                      │  kip-llm       (1 replica)       │
                                      │  Neo4j (cluster)                  │
                                      │  PostgreSQL + TimescaleDB         │
                                      │  Redis Sentinel                   │
                                      └─────────────────────────────────┘
                                                     │
                                                     ▼
                                            AlertHub
                                      (existing platform)
```

### Multi-Cluster with Strimzi Kafka
- All clusters publish to the same Kafka topics — `cluster_id` as message key
- Consumers filter by `cluster_id` in the message payload
- No separate topics per cluster: topic count stays constant regardless of cluster count
- In-agent circular buffer handles network partition; events delivered after reconnect
- Consumer group `kip-collector` processes events from all clusters concurrently

---

## 18. HA DESIGN

### kip-collector
- Stateless; 2 replicas minimum
- Kafka consumer group ensures each event processed exactly once
- Horizontal pod autoscaler on CPU

### kip-core
- RCA Engine: stateless computation against Neo4j/PostgreSQL
- 2 replicas; work claimed via PostgreSQL advisory locks
- Investigation results cached in Redis for 1h

### kip-api
- Stateless; 2+ replicas behind Kubernetes Service
- gRPC to kip-core; HTTP/REST to external callers
- Redis for rate limiting and caching

### Neo4j
- Causal cluster: 3 core members + optional read replicas
- KIP core writes to leader, reads from any replica
- Topology updates are write-heavy during startup, read-heavy during investigation

### Strimzi Kafka (existing)
- Already deployed and managed by Strimzi operator
- 3 Kafka broker replicas with topic replication factor 3
- No new Kafka infrastructure required

---

## 19. MVP IMPLEMENTATION PLAN (30 days)

### Week 1: Foundation
- [ ] Repository setup, Go modules, proto definitions
- [ ] Apply Strimzi KafkaTopic CRDs: `kubectl apply -f deployments/kafka/kafka-topics.yaml`
- [ ] PostgreSQL + TimescaleDB deployment
- [ ] Neo4j single instance (cluster in week 3)
- [ ] kip-agent: informer setup for Pods, Nodes, Deployments, Services
- [ ] kip-agent: basic topology builder (Pod→Node, Pod→Deployment, Service→Pod)
- [ ] kip-agent: Kafka publisher (IBM/sarama) with in-memory buffer
- [ ] kip-collector: Kafka consumer group (IBM/sarama), event normalization, cluster registry

### Week 2: Intelligence Core
- [ ] kip-core: Neo4j topology writer (consume from Kafka `kip.events.topology`, write graph)
- [ ] kip-core: basic RCA engine (topology traversal + K8s event correlation)
- [ ] kip-agent: health detection (CrashLoopBackOff, OOMKilled, NodeNotReady)
- [ ] kip-agent: complete informer coverage (all resources listed in design)
- [ ] kip-core: change record storage (detect spec changes via hash comparison)

### Week 3: API + Integration
- [ ] kip-api: REST API (investigations, topology, changes)
- [ ] kip-api: gRPC server
- [ ] AlertHub integration: `POST /api/v1/investigations` endpoint
- [ ] AlertHub: wire KIP call into `enrichIncidentV2` alongside existing CACIE
- [ ] kip-collector: multi-cluster support (filter by cluster_id in Kafka message key)
- [ ] Neo4j: upgrade to 3-node causal cluster

### Week 4: Forecasting + Config Intelligence
- [ ] kip-core: forecasting engine (PVC, node capacity)
- [ ] kip-core: configuration validator (15 rules)
- [ ] kip-llm: evidence-grounded LLM summary (Ollama)
- [ ] Helm charts: kip-agent + kip-hub finalized
- [ ] End-to-end test: incident in AlertHub → KIP investigation → RCA result

---

## 20. PRODUCTION IMPLEMENTATION PLAN (90 days)

### Month 1 (MVP above)

### Month 2: Depth
- [ ] Change intelligence: ArgoCD + Helm webhook integration
- [ ] Change intelligence: GitHub webhook (commit → change record)
- [ ] TimescaleDB: resource metrics collection from Prometheus remote-write
- [ ] kip-core: evidence enrichment (metrics + logs correlation in RCA)
- [ ] Configuration validator: 30+ rules, RBAC analysis
- [ ] kip-agent: PVC usage collection (exec into pods or metrics-server)
- [ ] Neo4j: temporal snapshots (topology at a point in time for RCA accuracy)
- [ ] kip-api: search endpoint (find resources across clusters)
- [ ] AlertHub UI: Cluster Explorer component (uses `/topology` API)
- [ ] AlertHub UI: Investigation Timeline component

### Month 3: Enterprise Grade
- [ ] Multi-cluster: verify all clusters publishing to shared Strimzi Kafka, kip-collector consuming all
- [ ] Security: JWT rotation, mTLS between all services
- [ ] Forecasting: complete set (PVC, node CPU, memory, cert expiry, pod eviction)
- [ ] kip-core: dependency discovery (traffic-based service graph from OTel traces)
- [ ] AlertHub UI: Dependency Graph component (D3 or react-flow)
- [ ] AlertHub UI: RCA View with evidence chains
- [ ] AlertHub UI: Configuration Intelligence panel
- [ ] AlertHub UI: Forecasting Dashboard
- [ ] Runbooks: investigation guide, cluster onboarding, agent deployment
- [ ] Load testing: 10 clusters, 500 nodes, 5000 pods sustained
- [ ] Chaos testing: network partition, Neo4j failover, Kafka broker failure

---

## 21. ALERTHUB UI COMPONENTS

KIP exposes APIs. AlertHub frontend consumes them. No separate UI.

### New frontend components (React/TypeScript):

```
frontend/src/components/kip/
├── ClusterExplorer.tsx          # cluster list + health + capacity summary
├── TopologyGraph.tsx            # Neo4j → D3/react-flow visualization
├── DependencyGraph.tsx          # application dependency graph
├── InvestigationTimeline.tsx    # step-by-step investigation with evidence
├── RCAView.tsx                  # root cause + confidence + evidence chain
├── ConfigIntelligence.tsx       # violations list with remediation guidance
├── ChangeIntelligence.tsx       # change timeline correlated with incidents
└── ForecastDashboard.tsx        # PVC/capacity/cert expiry forecasts
```

### API calls from each component:

```
ClusterExplorer        → GET /api/v1/clusters
TopologyGraph          → GET /api/v1/clusters/:id/topology
DependencyGraph        → GET /api/v1/clusters/:id/topology/dependencies/:kind/:ns/:name
InvestigationTimeline  → GET /api/v1/investigations/:id
RCAView                → GET /api/v1/investigations/:id (rca field)
ConfigIntelligence     → GET /api/v1/clusters/:id/config/violations
ChangeIntelligence     → GET /api/v1/clusters/:id/changes
ForecastDashboard      → GET /api/v1/clusters/:id/forecasts
```

---

## 22. COMPARISON: THIS vs DYNATRACE DAVIS vs DATADOG WATCHDOG

| Capability | KIP | Dynatrace Davis | Datadog Watchdog |
|---|---|---|---|
| Kubernetes native | Yes (informers, no agent overhead) | OneAgent (heavy) | Agent (moderate) |
| Evidence-first RCA | Yes | Deterministic chains | Correlation only |
| Topology depth | Neo4j graph, all K8s resources | Smartscape | Service Map |
| Configuration validation | Built-in, 30+ rules | Limited | Limited |
| Forecasting | PVC + capacity + cert | Capacity planning | Forecasting |
| Multi-cluster | First-class, Kafka key-based | Davis + multi-env | Multiple orgs |
| Open source stack | Yes | Proprietary | Proprietary |
| LLM role | Narrator only | Davis AI (decisions) | Watchdog (decisions) |
| AlertHub integration | Native | Webhook only | Webhook only |
| Offline buffering | In-memory circular buffer + retry | Agent buffer | Agent buffer |
| Secret safety | Metadata only | Full access | Full access |

KIP's advantage is not in features. It is in transparency: every conclusion is
backed by traceable evidence, every hypothesis that was rejected is shown to the
operator, and the LLM never makes a decision.
