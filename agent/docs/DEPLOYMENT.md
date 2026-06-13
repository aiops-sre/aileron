# KubeSense Deployment Guide

This guide covers deploying the full KubeSense stack from scratch on the `example-cluster` cluster. The target environment is the `aileron-agent` namespace; adjust cluster IDs and namespace names for other environments.

---

## Step 0: Prerequisites

Verify you have the following before starting:

| Requirement | Check |
|---|---|
| `kubectl` access to the target cluster | `kubectl cluster-info` |
| ArgoCD CLI (`argocd`) installed | `argocd version` |
| Strimzi Kafka operator running | `kubectl get pods -n aileron \| grep kafka` |
| ArgoCD `helm-revision-v1.0` CMP plugin installed | `argocd app list` (any existing app should sync) |
| `buildkit` namespace exists | `kubectl get ns buildkit` |
| Go 1.22+ (for local builds only) | `go version` |

---

## Step 1: Create Kafka Topics

KubeSense needs 18 Strimzi `KafkaTopic` CRDs in the `aileron` namespace (same namespace as the AlertHub Kafka cluster). Apply them in order:

```bash
# Core topics: events, investigations, forecasts, config violations
kubectl apply -f deployments/kafka/kafka-topics.yaml -n aileron

# Extended topics: APM, anomaly, security, cost, SLO, log
kubectl apply -f deployments/kafka/kafka-topics-extended.yaml -n aileron

# Intelligence topics: LLM, risk scores, fingerprints, playbooks, gitops
kubectl apply -f deployments/kafka/kafka-topics-intelligence.yaml -n aileron

# SRE topics: toil, drift, chaos scores, postmortems, noise budget, on-call routing
kubectl apply -f deployments/kafka/kafka-topics-sre.yaml -n aileron
```

Verify the topics were created:

```bash
kubectl get kafkatopics -n aileron | grep kubesense
```

Expected output includes 18+ topics beginning with `kubesense.`.

If Strimzi reports `NotReady` on any topic, check the Strimzi topic operator logs:

```bash
kubectl logs -n aileron \
  $(kubectl get pod -n aileron -l strimzi.io/name=alerthub-kafka-entity-operator -o name) \
  -c topic-operator
```

---

## Step 2: Deploy PostgreSQL and Neo4j

```bash
# Deploy Neo4j (topology graph)
kubectl apply -f deployments/databases/neo4j.yaml -n aileron-agent

# Deploy PostgreSQL (cluster registry + health events)
kubectl apply -f deployments/databases/postgres.yaml -n aileron-agent
```

Wait for both to become ready:

```bash
kubectl rollout status deployment/kubesense-neo4j -n aileron-agent
kubectl rollout status deployment/kubesense-postgres -n aileron-agent
```

### Create the PostgreSQL schema

Once PostgreSQL is running, apply the schema:

```bash
# kubesense_clusters — tracks registered clusters, last heartbeat
# kubesense_health_events — health state changes written by EventPersister
# kubesense_changes — resource change events for RCA lookback
kubectl exec -n aileron-agent deploy/kubesense-postgres -- \
  psql -U kubesense kubesense -c "
    CREATE TABLE IF NOT EXISTS kubesense_clusters (
      id             TEXT        PRIMARY KEY,
      first_seen     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
      last_heartbeat TIMESTAMPTZ NOT NULL DEFAULT NOW(),
      last_agent_id  TEXT,
      agent_version  TEXT,
      node_count     INTEGER     NOT NULL DEFAULT 0,
      status         TEXT        NOT NULL DEFAULT 'active'
    );

    CREATE TABLE IF NOT EXISTS kubesense_health_events (
      id          BIGSERIAL   PRIMARY KEY,
      cluster_id  TEXT        NOT NULL,
      event_type  TEXT        NOT NULL,
      resource_kind TEXT      NOT NULL,
      namespace   TEXT,
      resource_name TEXT      NOT NULL,
      severity    TEXT        NOT NULL,
      description TEXT,
      occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
    );
    CREATE INDEX IF NOT EXISTS idx_health_events_cluster_time
      ON kubesense_health_events (cluster_id, occurred_at DESC);

    CREATE TABLE IF NOT EXISTS kubesense_changes (
      id              BIGSERIAL   PRIMARY KEY,
      cluster_id      TEXT        NOT NULL,
      change_type     TEXT        NOT NULL,
      resource_kind   TEXT        NOT NULL,
      namespace       TEXT,
      resource_name   TEXT        NOT NULL,
      actor           TEXT,
      occurred_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
    );
    CREATE INDEX IF NOT EXISTS idx_changes_cluster_time
      ON kubesense_changes (cluster_id, occurred_at DESC);
  "
```

---

## Step 3: Create Required Secrets

### 3a. SecretsManager secrets (database credentials)

KubeSense services read credentials via the SecretsManager secret injection system. Create the secret in the `aileron-agent` namespace:

```bash
kubectl create secret generic kubesense-secrets_manager \
  --namespace aileron-agent \
  --from-literal=DATABASE_URL="postgres://kubesense:<password>@kubesense-postgres.aileron-agent.svc.cluster.local:5432/kubesense?sslmode=disable" \
  --from-literal=NEO4J_PASSWORD="<neo4j-password>"
```

### 3b. Buildkit secrets

```bash
# GitLab PAT for source clone (must have read_repository access to aiops-sre/KubeSense)
kubectl create secret generic interactive-git-token \
  --namespace buildkit \
  --from-literal=token=<gitlab-personal-access-token>

# JFrog Artifactory pull secret — copy from aileron-agent namespace if it already exists
kubectl get secret jfrog-dockers-access -n aileron-agent -o json | \
  jq 'del(.metadata.namespace,.metadata.resourceVersion,.metadata.uid,.metadata.creationTimestamp)' | \
  kubectl apply -n buildkit -f -
```

### 3c. JFrog pull secret in aileron-agent (for Deployments)

If not already present:

```bash
kubectl create secret docker-registry jfrog-dockers-access \
  --namespace aileron-agent \
  --docker-server=ghcr.io/aileron-platform \
  --docker-username=<username> \
  --docker-password=<password>
```

---

## Step 4: Register KubeSense Repository in ArgoCD

The `github.com/aileron-platform` host uses an internal CA. Register with certificate verification skipped:

```bash
argocd repo add \
  https://github.com/aileron-platform/aileron.git \
  --username <service-account> \
  --password <token> \
  --insecure-skip-server-verification \
  --name kubesense
```

Verify:

```bash
argocd repo list | grep KubeSense
```

Expected: `https://github.com/aileron-platform/aileron.git  Successful`

---

## Step 5: Apply ArgoCD Application CRs

```bash
kubectl apply -f deployments/argocd-apps.yaml -n argocd
```

This creates two ArgoCD `Application` CRs:

| Application | Chart | Services deployed |
|---|---|---|
| `kubesense-hub` | `helm/kip-hub` | kubesense-collector, kubesense-core, kubesense-api |
| `kubesense-agent` | `helm/kip-agent` | kubesense-agent |

Both use `ENV=example-cluster`, which merges `helm/kip-hub/values-example-cluster.yaml` (sets `build.enabled: true`) on top of the default `values.yaml`.

Because `build.enabled: true`, ArgoCD will run **Buildkit PreSync jobs** before applying Deployment manifests. These build all four service images from `main` and push them to `ghcr.io/aileron-platform/aileron-admins/`.

Watch the sync progress:

```bash
argocd app sync kubesense-hub
argocd app wait kubesense-hub --health --timeout 600

argocd app sync kubesense-agent
argocd app wait kubesense-agent --health --timeout 300
```

Watch the Buildkit jobs during the build:

```bash
kubectl get jobs -n buildkit | grep kubesense
kubectl logs -n buildkit -l app.kubernetes.io/name=kubesense-core -f
```

---

## Step 6: Verify Deployments

```bash
kubectl get deployments -n aileron-agent | grep kubesense
```

Expected:

```
NAME                   READY   UP-TO-DATE   AVAILABLE
kubesense-agent        1/1     1            1
kubesense-collector    2/2     2            2
kubesense-core         2/2     2            2
kubesense-api          1/1     1            1
kubesense-neo4j        1/1     1            1
kubesense-postgres     1/1     1            1
```

Check individual service health:

```bash
# Agent — should show informer syncs and Kafka publish logs
kubectl logs -n aileron-agent -l app=kubesense-agent --tail=50

# Collector — should show Kafka consume + Neo4j/PostgreSQL writes
kubectl logs -n aileron-agent -l app=kubesense-collector --tail=50

# Core — should show investigation consumer ready
kubectl logs -n aileron-agent -l app=kubesense-core --tail=50 | grep "investigation consumer"

# API — should show background signal publisher started
kubectl logs -n aileron-agent -l app=kubesense-api --tail=50 | grep "signal publisher"
```

Check health endpoints:

```bash
for svc in kubesense-core kubesense-api; do
  echo "=== $svc ==="
  kubectl exec -n aileron-agent deploy/$svc -- wget -qO- http://localhost:8080/healthz
  echo ""
done
```

---

## Step 7: Configure AlertHub Connection

### 7a. Set KAFKA_BROKERS in kubesense services

Verify the broker address in `helm/kip-hub/values.yaml`. The default is correct for the `example-cluster` cluster:

```yaml
kafkaBrokers: "alerthub-kafka-kafka-bootstrap.aileron.svc.cluster.local:9092"
```

If deploying on a different cluster, update `values-<env>.yaml`:

```yaml
agent:
  kafkaBrokers: "alerthub-kafka-kafka-bootstrap.<kafka-namespace>.svc.cluster.local:9092"
collector:
  kafkaBrokers: "alerthub-kafka-kafka-bootstrap.<kafka-namespace>.svc.cluster.local:9092"
core:
  kafkaBrokers: "alerthub-kafka-kafka-bootstrap.<kafka-namespace>.svc.cluster.local:9092"
```

### 7b. Verify AlertHub is publishing investigation requests

On the AlertHub side, ensure the `kubesense` integration is enabled and `KUBESENSE_KAFKA_BROKERS` points to the same bootstrap server. AlertHub will begin publishing to `kubesense.investigations.requests` when a new incident is created with cluster context.

### 7c. Test the investigation pipeline manually

```bash
# Produce a test investigation request
kubectl exec -n aileron \
  $(kubectl get pod -n aileron -l strimzi.io/name=alerthub-kafka-kafka -o name | head -1) \
  -- bin/kafka-console-producer.sh \
  --bootstrap-server localhost:9092 \
  --topic kubesense.investigations.requests \
  --property "parse.key=true" \
  --property "key.separator=|" <<'EOF'
TEST-001|{"id":"req-test-001","requested_at":"2026-06-08T14:00:00Z","incident_id":"TEST-001","cluster_id":"example-cluster","severity":"high","alert_title":"Test investigation"}
EOF

# Watch core process it
kubectl logs -n aileron-agent -l app=kubesense-core -f | grep "TEST-001"

# Check the result was published
kubectl exec -n aileron \
  $(kubectl get pod -n aileron -l strimzi.io/name=alerthub-kafka-kafka -o name | head -1) \
  -- bin/kafka-console-consumer.sh \
  --bootstrap-server localhost:9092 \
  --topic kubesense.investigations.results \
  --from-beginning --max-messages 1
```

---

## Step 8: Verify Data Flowing

### Check cluster registry

```bash
kubectl exec -n aileron-agent deploy/kubesense-postgres -- \
  psql -U kubesense kubesense -c \
  "SELECT id, last_heartbeat, agent_version, node_count, status FROM kubesense_clusters ORDER BY last_heartbeat DESC;"
```

Expected: one row per monitored cluster, with `last_heartbeat` updated within the last few minutes.

### Check health events

```bash
kubectl exec -n aileron-agent deploy/kubesense-postgres -- \
  psql -U kubesense kubesense -c \
  "SELECT cluster_id, event_type, resource_kind, namespace, resource_name, severity, occurred_at
   FROM kubesense_health_events
   ORDER BY occurred_at DESC LIMIT 20;"
```

### Check Neo4j topology

```bash
kubectl exec -n aileron-agent deploy/kubesense-neo4j -- \
  cypher-shell -u neo4j -p <password> \
  "MATCH (n) WHERE n.cluster_id = 'example-cluster' RETURN n.kind AS kind, count(n) AS cnt ORDER BY cnt DESC LIMIT 20;"
```

Expected: rows for Pod, Deployment, Service, ConfigMap, Node, etc.

### Check chaos scores are flowing to Kafka

```bash
kubectl exec -n aileron \
  $(kubectl get pod -n aileron -l strimzi.io/name=alerthub-kafka-kafka -o name | head -1) \
  -- bin/kafka-console-consumer.sh \
  --bootstrap-server localhost:9092 \
  --topic kubesense.chaos.scores \
  --from-beginning --max-messages 1
```

### Check APM signals are flowing

```bash
kubectl exec -n aileron \
  $(kubectl get pod -n aileron -l strimzi.io/name=alerthub-kafka-kafka -o name | head -1) \
  -- bin/kafka-console-consumer.sh \
  --bootstrap-server localhost:9092 \
  --topic kubesense.apm.golden-signals \
  --from-beginning --max-messages 5
```

---

## Step 9: Deploy the Web IDE (Optional)

```bash
kubectl apply -f deployments/dev/kubesense-ui.yaml -n aileron-agent
kubectl rollout status deployment/kubesense-ui -n aileron-agent
```

Access via the Ingress URL or port-forward:

```bash
kubectl port-forward -n aileron-agent svc/kubesense-ui 3000:80
# Open http://localhost:3000
```

---

## Step 10: Deploy the Admission Webhook (Optional)

The admission webhook scores manifests with pre-deploy risk scoring before they hit the API server. Start in dry-run mode:

```bash
kubectl apply -f deployments/webhook/webhook-config.yaml -n aileron-agent
kubectl rollout status deployment/kubesense-webhook -n aileron-agent

# Verify it's intercepting admissions (dry-run mode — always admits, attaches warnings)
kubectl logs -n aileron-agent -l app=kubesense-webhook -f
```

When you're confident in the scoring, enable enforcement:

```bash
kubectl set env deployment/kubesense-webhook \
  DENY_ON_CRITICAL_SECURITY=true \
  DENY_ON_HIGH_RISK=false \
  DRY_RUN=false \
  -n aileron-agent
```

---

## Upgrading

To deploy a new version, push to `main`. ArgoCD detects the revision change (via the `helm-revision-v1.0` CMP plugin, which embeds the current git SHA in values), runs the Buildkit PreSync jobs to rebuild images, and rolls the Deployments.

To force an immediate re-sync:

```bash
argocd app sync kubesense-hub --force
argocd app sync kubesense-agent --force
```

To pin to a specific git revision (for testing):

```bash
argocd app set kubesense-hub --revision <commit-sha>
argocd app sync kubesense-hub
```

---

## Uninstalling

```bash
# Remove ArgoCD apps (with cascade delete — removes all managed resources)
argocd app delete kubesense-hub --cascade
argocd app delete kubesense-agent --cascade

# Remove Kafka topics
kubectl delete -f deployments/kafka/kafka-topics-sre.yaml -n aileron
kubectl delete -f deployments/kafka/kafka-topics-intelligence.yaml -n aileron
kubectl delete -f deployments/kafka/kafka-topics-extended.yaml -n aileron
kubectl delete -f deployments/kafka/kafka-topics.yaml -n aileron

# Remove PostgreSQL schema (WARNING: drops all KubeSense data)
kubectl exec -n aileron-agent deploy/kubesense-postgres -- \
  psql -U kubesense kubesense -c \
  "DROP TABLE IF EXISTS kubesense_clusters, kubesense_health_events, kubesense_changes CASCADE;"
```
