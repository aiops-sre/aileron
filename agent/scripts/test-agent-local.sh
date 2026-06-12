#!/bin/bash
# test-agent-local.sh — Run the KubeSense agent locally against the dev cluster
# Usage: bash test-agent-local.sh
#
# Requires:
#   - kubectl context pointing at example-cluster-01 (current context)
#   - Go 1.22+
#   - kubectl port-forward to reach Kafka from local machine

set -euo pipefail

CLUSTER_ID="${CLUSTER_ID:-example-cluster-01}"
KAFKA_BROKERS="${KAFKA_BROKERS:-localhost:9093}"  # port-forwarded
KUBECONFIG="${KUBECONFIG:-$HOME/.kube/config}"

echo "=== KubeSense Agent Local Test ==="
echo "Cluster ID : $CLUSTER_ID"
echo "Kafka      : $KAFKA_BROKERS"
echo ""

# ── Step 1: Port-forward Kafka bootstrap ─────────────────────────────────────
echo "Step 1: Port-forwarding Kafka bootstrap..."
kubectl port-forward -n aileron svc/alerthub-kafka-kafka-bootstrap 9093:9092 &
PFWD_PID=$!
trap "kill $PFWD_PID 2>/dev/null; echo 'Cleaned up port-forward'" EXIT
sleep 3

# Verify Kafka is reachable
echo "Checking Kafka connectivity..."
if ! nc -zw3 localhost 9093 2>/dev/null; then
    echo "ERROR: Cannot reach Kafka on localhost:9093 — check port-forward"
    exit 1
fi
echo "Kafka reachable ✓"
echo ""

# ── Step 2: Build the agent ───────────────────────────────────────────────────
echo "Step 2: Building kubesense-agent..."
cd "$(dirname "$0")"

go build -o /tmp/kubesense-agent-test ./services/agent/cmd/agent/
echo "Build successful ✓"
echo ""

# ── Step 3: Run the agent for 60 seconds ─────────────────────────────────────
echo "Step 3: Running agent (60s) — watching cluster $CLUSTER_ID..."
echo "Press Ctrl+C to stop early."
echo ""

KAFKA_BROKERS="$KAFKA_BROKERS" \
CLUSTER_ID="$CLUSTER_ID" \
/tmp/kubesense-agent-test \
    --cluster-id="$CLUSTER_ID" \
    --kafka-brokers="$KAFKA_BROKERS" \
    --kubeconfig="$KUBECONFIG" \
    --buffer-max=100 \
    --health-addr=":18081" &

AGENT_PID=$!
trap "kill $PFWD_PID $AGENT_PID 2>/dev/null; echo 'Cleaned up'" EXIT

sleep 5

# ── Step 4: Check health endpoints ───────────────────────────────────────────
echo ""
echo "Step 4: Checking agent health endpoints..."
LIVENESS=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:18081/healthz)
READINESS=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:18081/readyz)
echo "  /healthz → $LIVENESS  (expect 200)"
echo "  /readyz  → $READINESS (200=ready, 503=still syncing)"
echo ""

sleep 15

# ── Step 5: Consume from a KubeSense topic ─────────────────────────────────────────
echo "Step 5: Consuming from kubesense.events.workloads (10s)..."
kubectl -n aileron exec -it alerthub-kafka-kafka-0 -- \
    /opt/kafka/bin/kafka-console-consumer.sh \
    --bootstrap-server alerthub-kafka-kafka-bootstrap.aileron.svc.cluster.local:9092 \
    --topic kubesense.events.workloads \
    --from-beginning \
    --max-messages 10 \
    --timeout-ms 10000 2>/dev/null || echo "(No messages yet — agent still syncing)"

echo ""
echo "Step 6: Consuming from kubesense.events.health (5s)..."
kubectl -n aileron exec -it alerthub-kafka-kafka-0 -- \
    /opt/kafka/bin/kafka-console-consumer.sh \
    --bootstrap-server alerthub-kafka-kafka-bootstrap.aileron.svc.cluster.local:9092 \
    --topic kubesense.events.health \
    --from-beginning \
    --max-messages 5 \
    --timeout-ms 5000 2>/dev/null || echo "(No health events — cluster may be healthy)"

echo ""
echo "=== Test complete. Kill agent now... ==="
kill $AGENT_PID 2>/dev/null || true
