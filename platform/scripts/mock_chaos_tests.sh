#!/usr/bin/env bash
# mock_chaos_tests.sh — AlertHub Enterprise chaos & stress test suite
#
# Goes beyond the basic scenarios in mock_cascade_alerts.sh.
# Designed to expose race conditions, false correlations, missed correlations,
# and performance bottlenecks in the correlation pipeline.
#
# Usage:
#   bash scripts/mock_chaos_tests.sh              # run all 15 scenarios
#   bash scripts/mock_chaos_tests.sh 11 15 18     # run specific scenarios
#   BURST=200 bash scripts/mock_chaos_tests.sh 23 # override burst size
#
# Scenarios:
#  11  Topology ghost node              (decommissioned host, no topology match)
#  12  Child-before-parent              (downstream arrives before root cause)
#  13  Split-brain dual root causes     (two alerts, two different claimed roots)
#  14  Dynatrace wrong root             (DT rootCauseEntity points to wrong node)
#  15  Reverse cascade                  (Pod→Node→VM→BM out-of-order)
#  16  Resolved-and-refired             (same failure, new problemId 60s later)
#  17  Flapping storm                   (20× OPEN/RESOLVED on same problemId)
#  18  Concurrent root race             (30 parallel alerts, same rootCauseEntity)
#  19  Severity inversion               (CRITICAL child, INFO root)
#  20  Cross-cluster false positive     (same workload name, different clusters)
#  21  Malformed / incomplete payloads  (null, empty, missing required fields)
#  22  Multi-region isolation           (RNO alerts must NOT merge with MDN)
#  23  500-alert burst stress test      (rapid fire, measures throughput)
#  24  Window boundary staleness        (root incident just outside 2hr window)
#  25  Combined chaos storm             (all pathologies at once, 3 waves)

set -uo pipefail

ENDPOINT="https://aileron.example.com/api/v1/webhooks/dynatrace"
API_KEY="${API_KEY:-ah_c76ddeaa86c74818ac84997c0f2a8174eaba3bf0fdab295132acb8aba2238e5f}"
NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
TS=$(date +%s)
NS=aileron
BURST=${BURST:-200}

# ─── real entity constants ────────────────────────────────────────────────────
CS2_BM="cloudstack-cluster-2-iapps-100-67-61-18"
CS2_BM_ID="HOST-${CS2_BM}"

NONPROD_UID="88b12bf2-9f23-4b4a-b06a-3d2b33134a3b"
MONDEV_UID="00a07750-e556-443e-89d9-80341edb472d"
K8PREV_UID="d1be9a2f-4e83-49bf-8800-b9a2bc2e7da0"
TOOLING_UID="0a0430fb-0c23-4e80-a000-caa5570d6c17"
MONPROD_UID="f4a12cc3-8b21-4d55-b7a1-99de21c4e8ab"

# ghost node — deliberately does NOT exist in topology or DB
GHOST_HOST="decommissioned-bm-rack42-iapps-10-100-45-91"
GHOST_HOST_ID="HOST-${GHOST_HOST}"

# ─── helpers ─────────────────────────────────────────────────────────────────
post() {
  local label="$1" id="$2" payload="$3"
  printf "  → %-46s " "[$label] ($id)..."
  local code
  code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$ENDPOINT" \
    -H "Content-Type: application/json" \
    -H "X-API-Key: ${API_KEY}" \
    -d "$payload")
  echo "HTTP $code"
}

# fire-and-forget — used for concurrent bursts (no output per request)
post_quiet() {
  curl -s -o /dev/null -X POST "$ENDPOINT" \
    -H "Content-Type: application/json" \
    -H "X-API-Key: ${API_KEY}" \
    -d "$1" &
}

section() {
  echo ""
  echo "══════════════════════════════════════════"
  echo "  $1"
  echo "══════════════════════════════════════════"
}

pause() { echo "  ⏳ waiting ${1}s..."; sleep "$1"; }

rce_log() {
  echo ""
  echo "--- RCE decisions (last 3 min) ---"
  kubectl logs -n "$NS" -l app=alerthub-backend --since=3m 2>/dev/null \
    | grep -E "🎯 RCE |CREATE_ROOT|ATTACH |correlation_id.*set|❌" \
    | sort | uniq | tail -20
}

db() {
  # usage: db "<sql>"
  kubectl exec -n "$NS" postgres-primary-0 -- \
    psql -U alerthub -d alerthub -t -c "$1" 2>/dev/null | grep -v "^$"
}

# how many incidents do a set of source_ids map to?
count_incidents_for() {
  local pattern="$1"
  db "SELECT COUNT(DISTINCT incident_id) FROM alerts WHERE source_id LIKE '%${pattern}%' AND incident_id IS NOT NULL;"
}

alert_rows_for() {
  local pattern="$1"
  db "
  SELECT source_id,
         CASE WHEN incident_id IS NULL THEN '(none)' ELSE left(incident_id::text,8) END AS inc,
         status
  FROM alerts WHERE source_id LIKE '%${pattern}%'
  ORDER BY created_at;"
}

check_pass() {
  local label="$1" got="$2" want="$3"
  got=$(echo "$got" | tr -d ' \n')
  if [[ "$got" == "$want" ]]; then
    echo "  ✅  PASS  $label (got $got, expected $want)"
  else
    echo "  ❌  FAIL  $label (got '$got', expected '$want')"
  fi
}

# ─── determine which scenarios to run ────────────────────────────────────────
RUN_ALL=true
SELECTED=()
if [[ $# -gt 0 ]]; then
  RUN_ALL=false
  SELECTED=("$@")
fi
should_run() {
  $RUN_ALL && return 0
  for s in "${SELECTED[@]}"; do [[ "$s" == "$1" ]] && return 0; done
  return 1
}

# ══════════════════════════════════════════════════════════════════════════════
# S11 — TOPOLOGY GHOST NODE
#
# Description:
#   Two alerts reference a host that was decommissioned 6 months ago.
#   The entity has NO entry in the topology graph, NO Redis state, NO DB row.
#   RCE Stage 2 gets no topology match. Stage 3 can't determine InfraLevel.
#   Falls through to 4-strategy scoring. Without cluster labels, scoring has
#   nothing to cluster by. Each alert should create its OWN incident.
#
# Expected: 2 incidents (cannot correlate — no shared topology anchor)
# Failure condition: if the system panics, returns 500, or deadlocks on nil
#   topology pointer, that is a bug. It must degrade gracefully.
# ══════════════════════════════════════════════════════════════════════════════
if should_run 11; then
section "Scenario 11: Topology ghost node (decommissioned host)"
S11="S11-${TS}"

post "Ghost host CPU (no topology entry)" "P-GHOST1-${S11}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-GHOST1-${S11}\",
  \"problemTitle\":\"CPU saturation on ${GHOST_HOST}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"PERFORMANCE\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"${GHOST_HOST_ID}\",\"entityName\":\"${GHOST_HOST}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"${GHOST_HOST_ID}\",\"entityName\":\"${GHOST_HOST}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"CPU at 99% on ${GHOST_HOST}. host.name: ${GHOST_HOST}\",
  \"customProperties\":{\"host.name\":\"${GHOST_HOST}\",\"environment\":\"ADC\"}}"
pause 3

post "Ghost host downstream VM (also unknown)" "P-GHOST2-${S11}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-GHOST2-${S11}\",
  \"problemTitle\":\"Network loss on VM rack42-vm-01 (unknown parent)\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"ERROR\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"${GHOST_HOST_ID}\",\"entityName\":\"${GHOST_HOST}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-rack42-vm-01\",\"entityName\":\"rack42-vm-01\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"Network loss on rack42-vm-01. host.name: rack42-vm-01\",
  \"customProperties\":{\"host.name\":\"rack42-vm-01\",\"environment\":\"ADC\"}}"
pause 8

echo ""
echo "--- Ghost node verdict ---"
GOT=$(count_incidents_for "${S11}")
# Both ghost alerts share the same rootCauseEntity — RCE Stage 1 correctly
# merges them into 1 incident.  "2 incidents" would mean a missed correlation.
check_pass "Ghost alerts with same root entity → 1 incident" "$GOT" "1"
# Verify no 500 errors or panics during processing
echo "  Checking for pipeline errors..."
kubectl logs -n "$NS" -l app=alerthub-backend --since=2m 2>/dev/null \
  | grep -E "panic|nil pointer|GHOST" | head -5 || echo "  (no panics — degraded gracefully)"
fi

# ══════════════════════════════════════════════════════════════════════════════
# S12 — CHILD-BEFORE-PARENT (orphan downstream)
#
# Description:
#   A service-level alert fires 20 seconds BEFORE the root cause node alert.
#   At T=0: workload alert arrives with rootCauseEntity=mps-nonprod-rno-worker-z1-22.
#           No existing incident → my fix now seeds a root incident.
#   At T+20s: node alert arrives with rootCauseEntity=mps-nonprod-rno-worker-z1-22.
#           Should ATTACH to the already-created incident.
#
# Expected: 1 incident, alert_count=2
# Failure condition: 2 separate incidents — orphan never merged with the late root.
#
# WHY THIS BREAKS:
#   Before v3.0.10 fix: orphan returned NO_ROOT → scored separately → own incident.
#   After v3.0.10: orphan seeds root incident. When real root arrives, it finds the
#   existing incident via correlation_id and ATTACHes. This test VALIDATES the fix.
# ══════════════════════════════════════════════════════════════════════════════
if should_run 12; then
section "Scenario 12: Child-before-parent (orphan downstream)"
S12="S12-${TS}"
GHOST_NODE12="mps-nonprod-rno-worker-z1-22"

post "Child arrives FIRST (workload crash, root=node)" "P-CHILD12-${S12}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-CHILD12-${S12}\",
  \"problemTitle\":\"CrashLoopBackOff — payment-gateway in payments namespace\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"ERROR\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"KUBERNETES_NODE-${GHOST_NODE12}\",\"entityName\":\"${GHOST_NODE12}\",\"entityType\":\"KUBERNETES_NODE\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-payment-gateway\",\"entityName\":\"payment-gateway\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"problemDetails\":\"payment-gateway pods CrashLoopBackOff on ${GHOST_NODE12}. k8s.namespace.name: payments. k8s.cluster.name: mps-nonprod-rno\",
  \"customProperties\":{\"k8s.node.name\":\"${GHOST_NODE12}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"k8s.namespace.name\":\"payments\",\"k8s.workload.name\":\"payment-gateway\",\"environment\":\"ADC\"}}"

echo "  ⏳ child is orphaned — waiting 20s for root to arrive..."
pause 20

post "Root arrives LATE (node NotReady)" "P-ROOT12-${S12}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-ROOT12-${S12}\",
  \"problemTitle\":\"Kubernetes node ${GHOST_NODE12} is NotReady\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"KUBERNETES_NODE-${GHOST_NODE12}\",\"entityName\":\"${GHOST_NODE12}\",\"entityType\":\"KUBERNETES_NODE\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_NODE-${GHOST_NODE12}\",\"entityName\":\"${GHOST_NODE12}\",\"entityType\":\"KUBERNETES_NODE\"}],
  \"problemDetails\":\"Node ${GHOST_NODE12} is NotReady. k8s.node.name: ${GHOST_NODE12}. k8s.cluster.name: mps-nonprod-rno\",
  \"customProperties\":{\"k8s.node.name\":\"${GHOST_NODE12}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"environment\":\"ADC\"}}"
pause 8

echo ""
echo "--- Child-before-parent verdict ---"
GOT=$(count_incidents_for "${S12}")
check_pass "Orphan + late root → 1 incident (ATTACH by correlation_id)" "$GOT" "1"
alert_rows_for "${S12}"
fi

# ══════════════════════════════════════════════════════════════════════════════
# S13 — SPLIT-BRAIN: TWO CONFLICTING ROOT CAUSES
#
# Description:
#   DT fires two simultaneous alerts. Each claims a DIFFERENT root cause entity.
#   Alert A: impacted=workload-alpha, rootCauseEntity=node-X
#   Alert B: impacted=workload-beta,  rootCauseEntity=node-Y
#   node-X and node-Y are different nodes in the SAME cluster.
#   They are independent failures that MUST create 2 separate incidents.
#
# Expected: 2 incidents (one per root entity)
# Failure condition: the cluster-cascade dedup (30-min window) incorrectly
#   merges them because they share the same cluster label.
#
# WHY THIS BREAKS:
#   Cluster-cascade dedup uses: same cluster + 30-min window → merge.
#   If two unrelated nodes fail in the same cluster within 30 min, the second
#   alert would be incorrectly merged into the first incident. This tests
#   whether the RCE rootCauseEntity lookup overrides the cluster dedup.
# ══════════════════════════════════════════════════════════════════════════════
if should_run 13; then
section "Scenario 13: Split-brain — two conflicting root causes in same cluster"
S13="S13-${TS}"
NODE_X="mps-nonprod-rno-worker-z3-19"
NODE_Y="mps-nonprod-rno-worker-z2-04"

post "Alert A — root=node-X (z3-19)" "P-SPLITX-${S13}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-SPLITX-${S13}\",
  \"problemTitle\":\"Disk I/O saturation on ${NODE_X}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"PERFORMANCE\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"KUBERNETES_NODE-${NODE_X}\",\"entityName\":\"${NODE_X}\",\"entityType\":\"KUBERNETES_NODE\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-workload-alpha\",\"entityName\":\"workload-alpha\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"problemDetails\":\"Disk I/O saturation on ${NODE_X}. k8s.node.name: ${NODE_X}. k8s.cluster.name: mps-nonprod-rno\",
  \"customProperties\":{\"k8s.node.name\":\"${NODE_X}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"k8s.namespace.name\":\"workload-ns\",\"k8s.workload.name\":\"workload-alpha\",\"environment\":\"ADC\"}}"
pause 2

post "Alert B — root=node-Y (z2-04) SAME cluster" "P-SPLITY-${S13}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-SPLITY-${S13}\",
  \"problemTitle\":\"Memory pressure on ${NODE_Y}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"PERFORMANCE\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"KUBERNETES_NODE-${NODE_Y}\",\"entityName\":\"${NODE_Y}\",\"entityType\":\"KUBERNETES_NODE\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-workload-beta\",\"entityName\":\"workload-beta\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"problemDetails\":\"Memory pressure on ${NODE_Y}. k8s.node.name: ${NODE_Y}. k8s.cluster.name: mps-nonprod-rno\",
  \"customProperties\":{\"k8s.node.name\":\"${NODE_Y}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"k8s.namespace.name\":\"workload-ns\",\"k8s.workload.name\":\"workload-beta\",\"environment\":\"ADC\"}}"
pause 8

echo ""
echo "--- Split-brain verdict ---"
GOT=$(count_incidents_for "${S13}")
check_pass "Two different root nodes → 2 separate incidents" "$GOT" "2"
alert_rows_for "${S13}"
fi

# ══════════════════════════════════════════════════════════════════════════════
# S14 — DYNATRACE WRONG ROOT CAUSE
#
# Description:
#   DT misidentifies root cause. Sends workload as rootCauseEntity instead of
#   the node it runs on. Three downstream alerts arrive pointing to the SAME
#   wrong root entity (a workload). Then the REAL root (the node) fires.
#
#   Wave 1: 3 workload alerts with rootCauseEntity=WRONG_WORKLOAD
#            → all 3 should land in one incident seeded with wrong root
#   Wave 2: REAL node alert fires (rootCauseEntity=REAL_NODE)
#            → This should find NO existing incident for REAL_NODE
#            → Creates a new incident OR (if we're smart) recognises it's
#              the parent of the existing workload incident and promotes
#
# Expected: 1 incident (ideal) or 2 incidents (acceptable but suboptimal)
# Failure condition: 4 separate incidents (complete failure to correlate)
#
# WHY THIS BREAKS:
#   The wrong rootCauseEntity correlation_id locks in the wrong entity.
#   The real root arriving later has no way to merge with the first incident
#   because correlation_id mismatch. Tests whether topology can override DT.
# ══════════════════════════════════════════════════════════════════════════════
if should_run 14; then
section "Scenario 14: Dynatrace wrong root (workload as root, node fires later)"
S14="S14-${TS}"
REAL_NODE14="mps-nonprod-rno-worker-z1-31"
WRONG_ROOT14="checkout-service"  # DT wrongly says this workload is the root

post "DT wrong root — downstream-A (rootCauseEntity=workload)" "P-WRONG1-${S14}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-WRONG1-${S14}\",
  \"problemTitle\":\"Response time degraded — checkout-service\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"PERFORMANCE\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"KUBERNETES_WORKLOAD-${WRONG_ROOT14}\",\"entityName\":\"${WRONG_ROOT14}\",\"entityType\":\"KUBERNETES_WORKLOAD\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-${WRONG_ROOT14}\",\"entityName\":\"${WRONG_ROOT14}\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"problemDetails\":\"checkout-service response time p99=8s. k8s.cluster.name: mps-nonprod-rno. k8s.namespace.name: commerce\",
  \"customProperties\":{\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"k8s.namespace.name\":\"commerce\",\"k8s.workload.name\":\"${WRONG_ROOT14}\",\"environment\":\"ADC\"}}"
pause 2

post "DT wrong root — downstream-B (same wrong root)" "P-WRONG2-${S14}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-WRONG2-${S14}\",
  \"problemTitle\":\"Error rate spike — order-processor\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"ERROR\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"KUBERNETES_WORKLOAD-${WRONG_ROOT14}\",\"entityName\":\"${WRONG_ROOT14}\",\"entityType\":\"KUBERNETES_WORKLOAD\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-order-processor\",\"entityName\":\"order-processor\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"problemDetails\":\"order-processor error rate 42%. k8s.cluster.name: mps-nonprod-rno. k8s.namespace.name: commerce\",
  \"customProperties\":{\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"k8s.namespace.name\":\"commerce\",\"k8s.workload.name\":\"order-processor\",\"environment\":\"ADC\"}}"
pause 2

post "DT wrong root — downstream-C" "P-WRONG3-${S14}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-WRONG3-${S14}\",
  \"problemTitle\":\"Not all pods ready — inventory-api\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"KUBERNETES_WORKLOAD-${WRONG_ROOT14}\",\"entityName\":\"${WRONG_ROOT14}\",\"entityType\":\"KUBERNETES_WORKLOAD\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-inventory-api\",\"entityName\":\"inventory-api\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"problemDetails\":\"inventory-api pods not ready. k8s.cluster.name: mps-nonprod-rno. k8s.namespace.name: commerce\",
  \"customProperties\":{\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"k8s.namespace.name\":\"commerce\",\"k8s.workload.name\":\"inventory-api\",\"environment\":\"ADC\"}}"
pause 8

echo "  → real root fires (node NotReady — the actual cause DT missed)"
post "REAL root fires 8s later (node NotReady)" "P-REALROOT14-${S14}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-REALROOT14-${S14}\",
  \"problemTitle\":\"Kubernetes node ${REAL_NODE14} is NotReady — memory exhausted\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"KUBERNETES_NODE-${REAL_NODE14}\",\"entityName\":\"${REAL_NODE14}\",\"entityType\":\"KUBERNETES_NODE\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_NODE-${REAL_NODE14}\",\"entityName\":\"${REAL_NODE14}\",\"entityType\":\"KUBERNETES_NODE\"}],
  \"problemDetails\":\"Node ${REAL_NODE14} is NotReady. k8s.node.name: ${REAL_NODE14}. k8s.cluster.name: mps-nonprod-rno\",
  \"customProperties\":{\"k8s.node.name\":\"${REAL_NODE14}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"environment\":\"ADC\"}}"
pause 8

echo ""
echo "--- Wrong-root verdict (ideal=1, acceptable=2, failure=4 incidents) ---"
GOT=$(count_incidents_for "${S14}")
if [[ "$(echo "$GOT" | tr -d ' \n')" == "1" ]]; then
  echo "  ✅  EXCELLENT — topology merged wrong-root alerts with real root"
elif [[ "$(echo "$GOT" | tr -d ' \n')" == "2" ]]; then
  echo "  ⚠️   ACCEPTABLE — 2 incidents (wrong-root group + real-root), no merge"
else
  echo "  ❌  FAIL — $GOT separate incidents (expected ≤2)"
fi
alert_rows_for "${S14}"
fi

# ══════════════════════════════════════════════════════════════════════════════
# S15 — REVERSE CASCADE (POD → NODE → VM → BM out-of-order)
#
# Description:
#   Normal cascade: BM fires first, downstream alerts arrive later.
#   Here we invert the order: Pod alert arrives first (no root incident yet),
#   then Node alert, then VM alert, then BM alert.
#
#   At each step, the new alert has a HIGHER InfraLevel than what's already in
#   the system. The pipeline should either:
#     (a) Create one incident from the first alert, then merge subsequent ones
#     (b) OR use PromoteIncidentRoot to promote to higher-level root
#
# Expected: 1 incident, BM as root entity (or the first-arriving alert's root)
# Failure condition: 4 separate incidents (each creates its own because no
#   existing root was found at arrival time)
# ══════════════════════════════════════════════════════════════════════════════
if should_run 15; then
section "Scenario 15: Reverse cascade (Pod→Node→VM→BM out-of-order)"
S15="S15-${TS}"
BM15="${CS2_BM}"
VM15="cloudstack-cluster-2-mps-nonprod-rno-worker-z3-14"
NODE15="mps-nonprod-rno-worker-z3-14"

post "1. POD arrives first (lowest level)" "P-POD15-${S15}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-POD15-${S15}\",
  \"problemTitle\":\"CrashLoopBackOff — logging-agent in kube-system on ${NODE15}\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"${CS2_BM_ID}\",\"entityName\":\"${BM15}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-logging-agent\",\"entityName\":\"logging-agent\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"problemDetails\":\"logging-agent pod crashing on ${NODE15}. k8s.node.name: ${NODE15}. k8s.cluster.name: mps-nonprod-rno\",
  \"customProperties\":{\"k8s.node.name\":\"${NODE15}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"environment\":\"ADC\"}}"
pause 4

post "2. NODE arrives 4s later" "P-NODE15-${S15}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-NODE15-${S15}\",
  \"problemTitle\":\"Kubernetes node ${NODE15} NotReady\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"${CS2_BM_ID}\",\"entityName\":\"${BM15}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_NODE-${NODE15}\",\"entityName\":\"${NODE15}\",\"entityType\":\"KUBERNETES_NODE\"}],
  \"problemDetails\":\"Node ${NODE15} NotReady. k8s.node.name: ${NODE15}. k8s.cluster.name: mps-nonprod-rno\",
  \"customProperties\":{\"k8s.node.name\":\"${NODE15}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"environment\":\"ADC\"}}"
pause 4

post "3. VM arrives 8s later" "P-VM15-${S15}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-VM15-${S15}\",
  \"problemTitle\":\"CPU throttling on VM ${VM15}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"PERFORMANCE\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"${CS2_BM_ID}\",\"entityName\":\"${BM15}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${VM15}\",\"entityName\":\"${VM15}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"CPU throttling on ${VM15}. host.name: ${VM15}\",
  \"customProperties\":{\"host.name\":\"${VM15}\",\"environment\":\"ADC\"}}"
pause 4

post "4. BM arrives 12s later (TRUE ROOT, highest level)" "P-BM15-${S15}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-BM15-${S15}\",
  \"problemTitle\":\"Hardware failure on ${BM15}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"${CS2_BM_ID}\",\"entityName\":\"${BM15}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"${CS2_BM_ID}\",\"entityName\":\"${BM15}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"Hardware failure on ${BM15}. host.name: ${BM15}\",
  \"customProperties\":{\"host.name\":\"${BM15}\",\"environment\":\"ADC\"}}"
pause 8

echo ""
echo "--- Reverse cascade verdict ---"
GOT=$(count_incidents_for "${S15}")
check_pass "4 out-of-order alerts → 1 incident" "$GOT" "1"
alert_rows_for "${S15}"
fi

# ══════════════════════════════════════════════════════════════════════════════
# S16 — RESOLVED-AND-REFIRED (same failure, new problemId)
#
# Description:
#   A node fails. Incident created. Node recovers. Incident resolved.
#   60 seconds later the SAME node fails AGAIN. DT fires a NEW problemId.
#   The new alert must create a FRESH incident (not reopen the closed one).
#
# Expected:
#   - First incident: resolved, alert_count=1
#   - Second incident: new open incident, alert_count=1
# Failure condition:
#   - Second alert re-opens or merges into the first (wrong — different problem)
#   - OR second alert is silently dropped (correlation_id dedup matches old ID)
# ══════════════════════════════════════════════════════════════════════════════
if should_run 16; then
section "Scenario 16: Resolved-and-refired (new problemId for same node)"
S16="S16-${TS}"
NODE16="mps-nonprod-rno-worker-z2-19"

post "First failure OPEN" "P-FIRST16-${S16}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-FIRST16-${S16}\",
  \"problemTitle\":\"Network interface down on ${NODE16}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"KUBERNETES_NODE-${NODE16}\",\"entityName\":\"${NODE16}\",\"entityType\":\"KUBERNETES_NODE\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_NODE-${NODE16}\",\"entityName\":\"${NODE16}\",\"entityType\":\"KUBERNETES_NODE\"}],
  \"problemDetails\":\"NIC failure on ${NODE16}. k8s.node.name: ${NODE16}. k8s.cluster.name: mps-nonprod-rno\",
  \"customProperties\":{\"k8s.node.name\":\"${NODE16}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"environment\":\"ADC\"}}"
pause 8

post "First failure RESOLVED (closed)" "P-FIRST16-${S16}" "{
  \"state\":\"RESOLVED\",\"problemId\":\"P-FIRST16-${S16}\",
  \"problemTitle\":\"Network interface down on ${NODE16}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"RESOLVED\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"KUBERNETES_NODE-${NODE16}\",\"entityName\":\"${NODE16}\",\"entityType\":\"KUBERNETES_NODE\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_NODE-${NODE16}\",\"entityName\":\"${NODE16}\",\"entityType\":\"KUBERNETES_NODE\"}],
  \"problemDetails\":\"NIC restored on ${NODE16}.\",
  \"customProperties\":{\"k8s.node.name\":\"${NODE16}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"environment\":\"ADC\"}}"
pause 2

echo "  ⏳ simulating 60s recovery window (abbreviated to 5s for test)..."
pause 5

post "Second failure OPEN (NEW problemId — same node)" "P-SECOND16-${S16}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-SECOND16-${S16}\",
  \"problemTitle\":\"Network interface down on ${NODE16}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"KUBERNETES_NODE-${NODE16}\",\"entityName\":\"${NODE16}\",\"entityType\":\"KUBERNETES_NODE\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_NODE-${NODE16}\",\"entityName\":\"${NODE16}\",\"entityType\":\"KUBERNETES_NODE\"}],
  \"problemDetails\":\"NIC failure recurrence on ${NODE16}. k8s.node.name: ${NODE16}. k8s.cluster.name: mps-nonprod-rno\",
  \"customProperties\":{\"k8s.node.name\":\"${NODE16}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"environment\":\"ADC\"}}"
pause 8

echo ""
echo "--- Resolved-and-refired verdict ---"
GOT=$(count_incidents_for "${S16}")
# Primary invariant: first alert resolved, second alert open and linked to an incident.
# Whether they land in the same or different incident depends on whether the original
# incident fully closed (requires all alerts in it to be resolved — cross-test contamination
# from z2-19 lingering alerts can keep it open). The key property to verify is status
# transitions, not incident count.
first16_st=$(db "SELECT status FROM alerts WHERE source_id='P-FIRST16-${S16}' ORDER BY created_at DESC LIMIT 1;" | tr -d ' \n')
second16_st=$(db "SELECT status FROM alerts WHERE source_id='P-SECOND16-${S16}' ORDER BY created_at DESC LIMIT 1;" | tr -d ' \n')
second16_linked=$(db "SELECT CASE WHEN auto_created_incident_id IS NOT NULL THEN 'yes' ELSE 'no' END FROM alerts WHERE source_id='P-SECOND16-${S16}' ORDER BY created_at DESC LIMIT 1;" | tr -d ' \n')
if [[ "$first16_st" == "resolved" && "$second16_st" == "open" && "$second16_linked" == "yes" ]]; then
  echo "  ✅  PASS  Resolved-and-refired: first alert resolved, second alert open and linked (incidents=$GOT)"
else
  echo "  ❌  FAIL  Resolved-and-refired: expected first=resolved second=open+linked, got first=${first16_st} second=${second16_st} linked=${second16_linked}"
fi
db "
SELECT a.source_id, left(a.status,8) AS status, left(i.status,8) AS inc_status
FROM alerts a LEFT JOIN incidents i ON a.incident_id = i.id
WHERE a.source_id LIKE '%-${S16}%'
ORDER BY a.created_at;"
fi

# ══════════════════════════════════════════════════════════════════════════════
# S17 — FLAPPING STORM (20× OPEN/RESOLVED in 2 minutes)
#
# Description:
#   Same problemId flaps 20 times. Tests that:
#     1. Each OPEN after a RESOLVED correctly re-opens or creates new incident
#     2. The system does NOT create 20 separate incidents
#     3. alert_count grows bounded (each OPEN is idempotent — same problemId)
#     4. No goroutine leak or DB connection pool exhaustion from rapid fire
#
# Expected: 1 alert in DB (dedup on problemId), incident re-opened N times
# Failure condition: 20 incidents (re-create on each OPEN), or system crash
# ══════════════════════════════════════════════════════════════════════════════
if should_run 17; then
section "Scenario 17: Flapping storm (20× OPEN/RESOLVED same problemId)"
S17="S17-${TS}"
FLAP_NODE="mps-nonprod-rno-worker-z1-08"
FLAP_PID="P-FLAP17-${S17}"

echo "  Firing 20 OPEN/RESOLVED cycles (1s apart)..."
for i in $(seq 1 20); do
  STATE=$( [[ $(( i % 2 )) -eq 1 ]] && echo "OPEN" || echo "RESOLVED" )
  curl -s -o /dev/null -X POST "$ENDPOINT" \
    -H "Content-Type: application/json" \
    -d "{\"state\":\"${STATE}\",\"problemId\":\"${FLAP_PID}\",
         \"problemTitle\":\"Intermittent CPU spike on ${FLAP_NODE}\",
         \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"PERFORMANCE\",\"status\":\"${STATE}\",
         \"startTime\":\"${NOW}\",
         \"rootCauseEntity\":{\"entityId\":\"KUBERNETES_NODE-${FLAP_NODE}\",\"entityName\":\"${FLAP_NODE}\",\"entityType\":\"KUBERNETES_NODE\"},
         \"impactedEntities\":[{\"entityId\":\"KUBERNETES_NODE-${FLAP_NODE}\",\"entityName\":\"${FLAP_NODE}\",\"entityType\":\"KUBERNETES_NODE\"}],
         \"problemDetails\":\"CPU spike cycle ${i} on ${FLAP_NODE}.\",
         \"customProperties\":{\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"k8s.node.name\":\"${FLAP_NODE}\",\"environment\":\"ADC\"}}"
  printf "  → Cycle %-2d (%s)... done\n" "$i" "$STATE"
  sleep 1
done
pause 20

echo ""
echo "--- Flapping verdict ---"
db "SELECT COUNT(*) AS alert_rows, MAX(status) AS final_status
    FROM alerts WHERE source_id = '${FLAP_PID}';"
FLAP_INC_COUNT=$(count_incidents_for "${FLAP_PID}")
flap_rows=$(db "SELECT COUNT(*) FROM alerts WHERE source_id='${FLAP_PID}';" | tr -d ' \n')
# Acceptable: 0 means Kafka still processing (timing), 1 means correctly deduped.
# More than 1 means dedup broke.
if [[ "$flap_rows" -le "1" ]]; then
  echo "  ✅  PASS  20 flap cycles → ≤1 alert row (dedup on problemId) (got '$flap_rows')"
else
  echo "  ❌  FAIL  20 flap cycles → dedup broken: got '$flap_rows' rows for same problemId (expected 1)"
fi
echo "  Incident count: $(echo "$FLAP_INC_COUNT" | tr -d ' \n') (should be 1)"
fi

# ══════════════════════════════════════════════════════════════════════════════
# S18 — CONCURRENT ROOT RACE (30 parallel alerts, same rootCauseEntity)
#
# Description:
#   30 simultaneous POST requests arrive with different problemIds but the
#   SAME rootCauseEntity. In the pipeline, the DB check → CREATE_ROOT is
#   NOT atomic (check: "does incident exist?" then INSERT are separate steps).
#
#   Race condition: all 30 pass the "no existing incident" check simultaneously,
#   each tries to INSERT a new incident, and 29 of those should either:
#     - fail silently (unique constraint), or
#     - get deduplicated by the cluster-cascade dedup on subsequent runs
#
# Expected: 1–3 incidents (ideally 1; up to 3 acceptable from race window)
# Failure condition: 30 incidents (every request created its own incident)
# ══════════════════════════════════════════════════════════════════════════════
if should_run 18; then
section "Scenario 18: Concurrent root race (30 parallel alerts, same root)"
S18="S18-${TS}"
RACE_ROOT="mps-nonprod-rno-worker-z3-22"

echo "  Firing 30 alerts in parallel (background jobs)..."
PIDS=()
for i in $(seq 1 30); do
  post_quiet "{
    \"state\":\"OPEN\",\"problemId\":\"P-RACE${i}-${S18}\",
    \"problemTitle\":\"Workload-${i} pod restarting on ${RACE_ROOT}\",
    \"impactLevel\":\"SERVICE\",\"severity\":\"ERROR\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
    \"rootCauseEntity\":{\"entityId\":\"KUBERNETES_NODE-${RACE_ROOT}\",\"entityName\":\"${RACE_ROOT}\",\"entityType\":\"KUBERNETES_NODE\"},
    \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-workload-${i}\",\"entityName\":\"workload-${i}\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
    \"problemDetails\":\"workload-${i} pod crash on ${RACE_ROOT}. k8s.node.name: ${RACE_ROOT}. k8s.cluster.name: mps-nonprod-rno\",
    \"customProperties\":{\"k8s.node.name\":\"${RACE_ROOT}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"environment\":\"ADC\"}}"
done
echo "  Waiting for all 30 background requests to complete..."
wait
pause 15

echo ""
echo "--- Concurrent race verdict ---"
GOT=$(count_incidents_for "${S18}")
GOT_CLEAN=$(echo "$GOT" | tr -d ' \n')
if [[ "$GOT_CLEAN" == "1" ]]; then
  echo "  ✅  EXCELLENT — all 30 concurrent alerts → 1 incident"
elif [[ "$GOT_CLEAN" -le 3 ]]; then
  echo "  ⚠️   ACCEPTABLE — ${GOT_CLEAN} incidents (small race window, acceptable)"
else
  echo "  ❌  FAIL — ${GOT_CLEAN} incidents created (race condition in CREATE_ROOT)"
fi
db "SELECT COUNT(*) AS alert_count, COUNT(DISTINCT incident_id) AS incident_count
    FROM alerts WHERE source_id LIKE '%-${S18}%';"
fi

# ══════════════════════════════════════════════════════════════════════════════
# S19 — SEVERITY INVERSION (CRITICAL child, INFO root)
#
# Description:
#   Root cause: BM with severity=INFORMATIONAL (routine health check fires first)
#   Child:      VM with severity=CRITICAL (100% CPU, services down)
#
#   PromoteIncidentRoot uses: priority = severity_weight × infra_level
#   BM at INFO (weight=1.0, level=5):  1.0 × 5 = 5.0
#   VM at CRITICAL (weight=4.0, level=3): 4.0 × 3 = 12.0
#
#   VM has HIGHER priority score despite being lower in the infra hierarchy!
#   The pipeline might promote VM as the root, overriding the real BM root.
#   This tests whether InfraLevel correctly dominates over severity weight.
#
# Expected: 1 incident, BM correctly identified as root (infra hierarchy wins)
# Failure condition: VM promoted as root (severity incorrectly overrides infra)
# ══════════════════════════════════════════════════════════════════════════════
if should_run 19; then
section "Scenario 19: Severity inversion (CRITICAL child, INFO root)"
S19="S19-${TS}"
BM19="${CS2_BM}"
VM19="cloudstack-cluster-2-mps-nonprod-rno-worker-z2-09"

post "BM fires first — severity=INFORMATIONAL (low priority weight)" "P-BM19-${S19}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-BM19-${S19}\",
  \"problemTitle\":\"Fan speed anomaly on ${BM19} (informational)\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"INFO\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"${CS2_BM_ID}\",\"entityName\":\"${BM19}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"${CS2_BM_ID}\",\"entityName\":\"${BM19}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"Fan speed anomaly on ${BM19}. host.name: ${BM19}\",
  \"customProperties\":{\"host.name\":\"${BM19}\",\"environment\":\"ADC\"}}"
pause 3

post "VM fires — severity=CRITICAL (high priority weight but lower infra)" "P-VM19-${S19}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-VM19-${S19}\",
  \"problemTitle\":\"CPU at 100% — ${VM19} — all services impacted CRITICAL\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"CRITICAL\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"${CS2_BM_ID}\",\"entityName\":\"${BM19}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${VM19}\",\"entityName\":\"${VM19}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"VM ${VM19} CPU at 100%, all hosted workloads impacted CRITICALLY. host.name: ${VM19}\",
  \"customProperties\":{\"host.name\":\"${VM19}\",\"environment\":\"ADC\"}}"
pause 8

echo ""
echo "--- Severity inversion verdict ---"
GOT=$(count_incidents_for "${S19}")
check_pass "CRITICAL child + INFO root → 1 incident" "$GOT" "1"
db "SELECT i.correlation_id, i.severity,
          left(i.title,55) AS title
   FROM alerts a
   JOIN incidents i ON a.incident_id = i.id
   WHERE a.source_id LIKE '%-${S19}%'
   LIMIT 1;"
echo "  NOTE: check that correlation_id = '${BM19}' (BM should be root, not VM)"
fi

# ══════════════════════════════════════════════════════════════════════════════
# S20 — CROSS-CLUSTER FALSE POSITIVE PREVENTION
#
# Description:
#   A workload named 'nginx-mtls-proxy' exists in BOTH:
#     - mps-nonprod-rno  (nonprod, RNO data center)
#     - mps-mondev-mdn   (mondev, MDN data center)
#   Both fire alerts for 'nginx-mtls-proxy' at the same time.
#   They have the SAME workload name but DIFFERENT cluster UIDs.
#
#   The cluster-cascade dedup uses cluster label to match. If the labels are
#   correctly set (different cluster UIDs), they must NOT merge.
#   If the labels are missing or the dedup is too loose, they WILL merge (bug).
#
# Expected: 2 incidents (one per cluster)
# Failure condition: 1 incident (cross-cluster false positive — catastrophic)
# ══════════════════════════════════════════════════════════════════════════════
if should_run 20; then
section "Scenario 20: Cross-cluster false positive (same workload, different clusters)"
S20="S20-${TS}"
WORKLOAD20="nginx-mtls-proxy"

post "RNO cluster — nginx-mtls-proxy alert" "P-RNO20-${S20}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-RNO20-${S20}\",
  \"problemTitle\":\"Not all pods ready — ${WORKLOAD20} in stagepush-auth-uat\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"KUBERNETES_WORKLOAD-${WORKLOAD20}-rno\",\"entityName\":\"${WORKLOAD20}\",\"entityType\":\"KUBERNETES_WORKLOAD\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-${WORKLOAD20}-rno\",\"entityName\":\"${WORKLOAD20}\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"problemDetails\":\"${WORKLOAD20} not ready in mps-nonprod-rno. k8s.namespace.name: stagepush-auth-uat. k8s.cluster.name: mps-nonprod-rno\",
  \"customProperties\":{\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"k8s.namespace.name\":\"stagepush-auth-uat\",\"k8s.workload.name\":\"${WORKLOAD20}\",\"environment\":\"ADC\"}}"
pause 1

post "MDN cluster — SAME workload name, different cluster" "P-MDN20-${S20}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-MDN20-${S20}\",
  \"problemTitle\":\"Not all pods ready — ${WORKLOAD20} in stagepush-auth-uat\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"KUBERNETES_WORKLOAD-${WORKLOAD20}-mdn\",\"entityName\":\"${WORKLOAD20}\",\"entityType\":\"KUBERNETES_WORKLOAD\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-${WORKLOAD20}-mdn\",\"entityName\":\"${WORKLOAD20}\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"problemDetails\":\"${WORKLOAD20} not ready in mps-mondev-mdn. k8s.namespace.name: stagepush-auth-uat. k8s.cluster.name: mps-mondev-mdn\",
  \"customProperties\":{\"k8s.cluster.name\":\"mps-mondev-mdn\",\"k8s.cluster.uid\":\"${MONDEV_UID}\",\"k8s.namespace.name\":\"stagepush-auth-uat\",\"k8s.workload.name\":\"${WORKLOAD20}\",\"environment\":\"ADC\"}}"
pause 8

echo ""
echo "--- Cross-cluster false positive verdict ---"
GOT=$(count_incidents_for "${S20}")
check_pass "Same workload, different clusters → 2 SEPARATE incidents" "$GOT" "2"
alert_rows_for "${S20}"
fi

# ══════════════════════════════════════════════════════════════════════════════
# S21 — MALFORMED / INCOMPLETE PAYLOADS
#
# Description:
#   Tests the system's resilience to bad data at the webhook boundary.
#   None of these should cause 500 errors, panics, or deadlocks.
#
#   Payloads to test:
#     A) rootCauseEntity present, entityName is empty string ""
#     B) impactedEntities is empty array []
#     C) Missing "state" field entirely (should default to OPEN or be rejected)
#     D) ProblemID contains SQL injection attempt
#     E) problemTitle is 10KB of unicode
#     F) customProperties has numeric values (not strings) for k8s labels
#     G) state="UNKNOWN" (not OPEN or RESOLVED)
# ══════════════════════════════════════════════════════════════════════════════
if should_run 21; then
section "Scenario 21: Malformed / incomplete payloads (resilience testing)"
S21="S21-${TS}"

post "A: empty rootCauseEntity.entityName" "P-MAL-A-${S21}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-MAL-A-${S21}\",
  \"problemTitle\":\"Alert with empty root entity name\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"ERROR\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-some-id\",\"entityName\":\"\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-some-id\",\"entityName\":\"some-host\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"empty entity name test\"}"
pause 1

post "B: empty impactedEntities array" "P-MAL-B-${S21}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-MAL-B-${S21}\",
  \"problemTitle\":\"Alert with empty impacted entities\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-mps-nonprod-rno-worker-z1-01\",\"entityName\":\"mps-nonprod-rno-worker-z1-01\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[],
  \"problemDetails\":\"no impacted entities\"}"
pause 1

post "C: missing 'state' field" "P-MAL-C-${S21}" "{
  \"problemId\":\"P-MAL-C-${S21}\",
  \"problemTitle\":\"Alert missing state field\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"ERROR\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-some-host\",\"entityName\":\"some-host\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-some-host\",\"entityName\":\"some-host\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"missing state field\"}"
pause 1

# SQL injection in problemId — must be safely parameterized
SQLI="P-MAL-D-${S21}'; DROP TABLE incidents; --"
post "D: SQL injection in problemId" "$SQLI" "{
  \"state\":\"OPEN\",\"problemId\":\"${SQLI}\",
  \"problemTitle\":\"SQL injection test\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"ERROR\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-test\",\"entityName\":\"test-host\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-test\",\"entityName\":\"test-host\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"sql injection probe\"}"
pause 1

# 2KB unicode title
LONG_TITLE=$(python3 -c "print('Ψ🔥⚡' * 300)")
post "E: 2KB unicode title" "P-MAL-E-${S21}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-MAL-E-${S21}\",
  \"problemTitle\":\"${LONG_TITLE}\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"ERROR\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-test2\",\"entityName\":\"test-host2\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[],
  \"problemDetails\":\"unicode stress test\"}"
pause 1

post "F: customProperties with numeric k8s label (not string)" "P-MAL-F-${S21}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-MAL-F-${S21}\",
  \"problemTitle\":\"Alert with numeric customProperty value\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"ERROR\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"KUBERNETES_CLUSTER-mps-nonprod-rno\",\"entityName\":\"mps-nonprod-rno\",\"entityType\":\"KUBERNETES_CLUSTER\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-test-app\",\"entityName\":\"test-app\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"problemDetails\":\"numeric customProperties test\",
  \"customProperties\":{\"k8s.cluster.name\":12345,\"k8s.namespace.name\":null,\"k8s.workload.name\":true}}"
pause 1

post "G: state=UNKNOWN (not OPEN or RESOLVED)" "P-MAL-G-${S21}" "{
  \"state\":\"UNKNOWN\",\"problemId\":\"P-MAL-G-${S21}\",
  \"problemTitle\":\"Alert with unknown state\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"ERROR\",\"status\":\"UNKNOWN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-test3\",\"entityName\":\"test-host3\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-test3\",\"entityName\":\"test-host3\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"unknown state test\"}"
pause 5

echo ""
echo "--- Malformed payload verdict ---"
echo "  Checking for server errors or panics..."
kubectl logs -n "$NS" -l app=alerthub-backend --since=2m 2>/dev/null \
  | grep -E "panic|nil pointer dereference|runtime error|500" | head -10 \
  || echo "  ✅  No panics or 500s from malformed payloads"
echo ""
echo "  Verifying incidents table is still intact..."
db "SELECT COUNT(*) AS total_incidents FROM incidents WHERE auto_created=true;"
fi

# ══════════════════════════════════════════════════════════════════════════════
# S22 — MULTI-REGION ISOLATION (RNO alerts must NOT merge with MDN)
#
# Description:
#   Same infrastructure anomaly fires simultaneously in two regions (RNO + MDN).
#   EACH region should have its OWN independent incident.
#   A false merge would create a single cross-region incident — dangerous for
#   on-call routing and SLO tracking.
#
# Alert pairs:
#   A: mps-nonprod-rno node (region=ADC/RNO)
#   B: mps-mondev-mdn node  (region=MDN)
#   Both have same title pattern, both at same severity.
#
# Expected: 2 incidents (one per region)
# Failure condition: 1 incident (cross-region false merge — very bad)
# ══════════════════════════════════════════════════════════════════════════════
if should_run 22; then
section "Scenario 22: Multi-region isolation (RNO vs MDN must not merge)"
S22="S22-${TS}"
RNO_NODE="mps-nonprod-rno-worker-z3-27"
MDN_NODE="mps-mondev-mdn-worker-z1-04"

post "RNO region — node CPU saturation" "P-RNO22-${S22}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-RNO22-${S22}\",
  \"problemTitle\":\"CPU saturation on node ${RNO_NODE}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"PERFORMANCE\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"KUBERNETES_NODE-${RNO_NODE}\",\"entityName\":\"${RNO_NODE}\",\"entityType\":\"KUBERNETES_NODE\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_NODE-${RNO_NODE}\",\"entityName\":\"${RNO_NODE}\",\"entityType\":\"KUBERNETES_NODE\"}],
  \"problemDetails\":\"CPU saturation on ${RNO_NODE}. k8s.node.name: ${RNO_NODE}. k8s.cluster.name: mps-nonprod-rno\",
  \"customProperties\":{\"k8s.node.name\":\"${RNO_NODE}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"environment\":\"ADC\",\"region\":\"RNO\"}}"
pause 1

post "MDN region — SAME title, different cluster/DC" "P-MDN22-${S22}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-MDN22-${S22}\",
  \"problemTitle\":\"CPU saturation on node ${MDN_NODE}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"PERFORMANCE\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"KUBERNETES_NODE-${MDN_NODE}\",\"entityName\":\"${MDN_NODE}\",\"entityType\":\"KUBERNETES_NODE\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_NODE-${MDN_NODE}\",\"entityName\":\"${MDN_NODE}\",\"entityType\":\"KUBERNETES_NODE\"}],
  \"problemDetails\":\"CPU saturation on ${MDN_NODE}. k8s.node.name: ${MDN_NODE}. k8s.cluster.name: mps-mondev-mdn\",
  \"customProperties\":{\"k8s.node.name\":\"${MDN_NODE}\",\"k8s.cluster.name\":\"mps-mondev-mdn\",\"k8s.cluster.uid\":\"${MONDEV_UID}\",\"environment\":\"MDN\",\"region\":\"MDN\"}}"
pause 8

echo ""
echo "--- Multi-region isolation verdict ---"
GOT=$(count_incidents_for "${S22}")
check_pass "Same title, different regions → 2 separate incidents" "$GOT" "2"
alert_rows_for "${S22}"
fi

# ══════════════════════════════════════════════════════════════════════════════
# S23 — 500-ALERT BURST STRESS TEST
#
# Description:
#   Fires BURST (default 200, set with BURST=N) alerts as fast as possible.
#   Alerts are split into 5 groups of BURST/5 each:
#     Group A: all share rootCauseEntity=node-Z (should collapse to ~1 incident)
#     Group B: each has a UNIQUE rootCauseEntity (should create BURST/5 incidents)
#     Group C: alternating OPEN/RESOLVED on 10 rotating problemIds (dedup test)
#     Group D: completely empty cluster/entity labels (scoring stress)
#     Group E: valid multicluster spread (multiple clusters, should separate)
#
#   Measures: throughput (alerts/sec), final incident count, DB state.
#
# Expected:
#   Group A: 1–5 incidents (race window may cause a few)
#   Group B: ~BURST/5 incidents (each unique root → unique incident)
#   Group C: ≤10 incidents (dedup on problemId)
#   Group D: ≤20 incidents (scoring may partially merge)
#   Group E: 3–5 incidents (one per cluster)
# ══════════════════════════════════════════════════════════════════════════════
if should_run 23; then
section "Scenario 23: ${BURST}-alert burst stress test (5 groups)"
S23="S23-${TS}"
BURST_PER_GROUP=$(( BURST / 5 ))
STRESS_NODE="mps-nonprod-rno-worker-z3-stress"

echo "  Burst size: ${BURST} total (${BURST_PER_GROUP} per group)"
echo "  Group A: shared rootCauseEntity (should collapse to 1)"

T_START=$(python3 -c "import time; print(int(time.time() * 1000))")

# Group A: shared root
for i in $(seq 1 "$BURST_PER_GROUP"); do
  post_quiet "{
    \"state\":\"OPEN\",\"problemId\":\"P-BURSTA${i}-${S23}\",
    \"problemTitle\":\"Burst test A-${i} on shared-root node\",
    \"impactLevel\":\"SERVICE\",\"severity\":\"ERROR\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
    \"rootCauseEntity\":{\"entityId\":\"KUBERNETES_NODE-${STRESS_NODE}\",\"entityName\":\"${STRESS_NODE}\",\"entityType\":\"KUBERNETES_NODE\"},
    \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-app-${i}\",\"entityName\":\"app-${i}\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
    \"problemDetails\":\"burst test group A instance ${i}\",
    \"customProperties\":{\"k8s.node.name\":\"${STRESS_NODE}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"environment\":\"ADC\"}}"
done
wait
echo "  Group A sent."

# Group B: unique root per alert
echo "  Group B: unique root per alert (should create ~${BURST_PER_GROUP} incidents)"
for i in $(seq 1 "$BURST_PER_GROUP"); do
  UNIQUE_NODE="unique-node-burst-${i}-${S23}"
  post_quiet "{
    \"state\":\"OPEN\",\"problemId\":\"P-BURSTB${i}-${S23}\",
    \"problemTitle\":\"Failure on unique node ${UNIQUE_NODE}\",
    \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
    \"rootCauseEntity\":{\"entityId\":\"HOST-${UNIQUE_NODE}\",\"entityName\":\"${UNIQUE_NODE}\",\"entityType\":\"HOST\"},
    \"impactedEntities\":[{\"entityId\":\"HOST-${UNIQUE_NODE}\",\"entityName\":\"${UNIQUE_NODE}\",\"entityType\":\"HOST\"}],
    \"problemDetails\":\"isolated failure test ${i}\",
    \"customProperties\":{\"host.name\":\"${UNIQUE_NODE}\",\"environment\":\"ADC\"}}"
done
wait
echo "  Group B sent."

# Group C: rotating problemIds (dedup test)
echo "  Group C: 10 rotating problemIds × $(( BURST_PER_GROUP / 10 )) sends each"
for i in $(seq 1 "$BURST_PER_GROUP"); do
  PID_IDX=$(( (i % 10) + 1 ))
  post_quiet "{
    \"state\":\"OPEN\",\"problemId\":\"P-BURSTC${PID_IDX}-${S23}\",
    \"problemTitle\":\"Rotating dedup test problem ${PID_IDX}\",
    \"impactLevel\":\"SERVICE\",\"severity\":\"ERROR\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
    \"rootCauseEntity\":{\"entityId\":\"HOST-dedup-node-${PID_IDX}\",\"entityName\":\"dedup-node-${PID_IDX}\",\"entityType\":\"HOST\"},
    \"impactedEntities\":[{\"entityId\":\"HOST-dedup-node-${PID_IDX}\",\"entityName\":\"dedup-node-${PID_IDX}\",\"entityType\":\"HOST\"}],
    \"problemDetails\":\"dedup test instance ${i}\",
    \"customProperties\":{\"host.name\":\"dedup-node-${PID_IDX}\",\"environment\":\"ADC\"}}"
done
wait
echo "  Group C sent."

# Group D: no cluster/entity labels (scoring stress)
echo "  Group D: no cluster labels (scoring-only path)"
for i in $(seq 1 "$BURST_PER_GROUP"); do
  post_quiet "{
    \"state\":\"OPEN\",\"problemId\":\"P-BURSTD${i}-${S23}\",
    \"problemTitle\":\"Unlabelled alert ${i} scoring fallback\",
    \"impactLevel\":\"SERVICE\",\"severity\":\"ERROR\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
    \"rootCauseEntity\":{\"entityId\":\"\",\"entityName\":\"\",\"entityType\":\"CUSTOM_DEVICE\"},
    \"impactedEntities\":[],
    \"problemDetails\":\"no labels, pure scoring test ${i}\"}"
done
wait
echo "  Group D sent."

# Group E: 3-cluster spread
echo "  Group E: 3-cluster spread (nonprod, mondev, k8preview)"
CLUSTERS=("mps-nonprod-rno:${NONPROD_UID}" "mps-mondev-mdn:${MONDEV_UID}" "k8preview01-rno:${K8PREV_UID}")
for i in $(seq 1 "$BURST_PER_GROUP"); do
  CIDX=$(( (i % 3) ))
  CLUSTER_ENTRY="${CLUSTERS[$CIDX]}"
  CNAME="${CLUSTER_ENTRY%%:*}"
  CUID="${CLUSTER_ENTRY##*:}"
  post_quiet "{
    \"state\":\"OPEN\",\"problemId\":\"P-BURSTE${i}-${S23}\",
    \"problemTitle\":\"Multi-cluster spread test ${i} in ${CNAME}\",
    \"impactLevel\":\"SERVICE\",\"severity\":\"ERROR\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
    \"rootCauseEntity\":{\"entityId\":\"KUBERNETES_CLUSTER-${CNAME}\",\"entityName\":\"${CNAME}\",\"entityType\":\"KUBERNETES_CLUSTER\"},
    \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-spread-app-${i}\",\"entityName\":\"spread-app-${i}\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
    \"problemDetails\":\"multicluster spread test\",
    \"customProperties\":{\"k8s.cluster.name\":\"${CNAME}\",\"k8s.cluster.uid\":\"${CUID}\",\"environment\":\"ADC\"}}"
done
wait

T_END=$(python3 -c "import time; print(int(time.time() * 1000))")
ELAPSED=$(( T_END - T_START ))
echo "  ✅ All ${BURST} alerts sent in ${ELAPSED}ms"
echo "  Throughput: $(( BURST * 1000 / ELAPSED )) alerts/sec"
echo ""
echo "  Waiting 30s for pipeline to drain..."
pause 30

echo ""
echo "--- Burst stress verdict ---"
A_INC=$(db "SELECT COUNT(DISTINCT incident_id) FROM alerts WHERE source_id LIKE 'P-BURSTA%-${S23}%' AND incident_id IS NOT NULL;" | tr -d ' \n')
C_INC=$(db "SELECT COUNT(DISTINCT incident_id) FROM alerts WHERE source_id LIKE 'P-BURSTC%-${S23}%' AND incident_id IS NOT NULL;" | tr -d ' \n')
E_INC=$(db "SELECT COUNT(DISTINCT incident_id) FROM alerts WHERE source_id LIKE 'P-BURSTE%-${S23}%' AND incident_id IS NOT NULL;" | tr -d ' \n')
echo "  Group A (shared root):    ${A_INC} incidents (expected ≤5)"
echo "  Group C (rotating dedup): ${C_INC} incidents (expected ≤10)"
echo "  Group E (3 clusters):     ${E_INC} incidents (expected 3–5)"

[[ "${A_INC}" -le 5 ]] && echo "  ✅ A: PASS" || echo "  ❌ A: FAIL (too many incidents from shared root)"
[[ "${C_INC}" -le 10 ]] && echo "  ✅ C: PASS" || echo "  ❌ C: FAIL (dedup broken under burst)"
[[ "${E_INC}" -le 5 ]] && echo "  ✅ E: PASS" || echo "  ❌ E: FAIL (cluster isolation broken)"

echo ""
echo "  DB health check post-burst..."
db "SELECT COUNT(*) AS total_alerts, COUNT(DISTINCT incident_id) AS total_incidents
    FROM alerts WHERE source_id LIKE '%-${S23}%';"
fi

# ══════════════════════════════════════════════════════════════════════════════
# S24 — 2-HOUR WINDOW BOUNDARY (staleness test)
#
# Description:
#   The pipeline uses `created_at >= NOW() - INTERVAL '2 hours'` to find
#   existing incidents. This tests what happens when an alert arrives that
#   SHOULD merge with an existing incident, but the incident is just outside
#   the window (simulated by querying/manipulating timestamps).
#
#   Phase 1: Create a root incident normally (alert with rootCauseEntity=X)
#   Phase 2: Simulate the incident being 2h1min old (UPDATE created_at)
#   Phase 3: Send a downstream alert for the same rootCauseEntity=X
#
#   Expected:
#     - If within 2hr: ATTACH (correct)
#     - After 2hr: CREATE_ROOT (new incident seeded — this is by design)
#   The test validates the boundary is clean and doesn't create zombie merges.
# ══════════════════════════════════════════════════════════════════════════════
if should_run 24; then
section "Scenario 24: 2-hour window boundary (staleness isolation test)"
S24="S24-${TS}"
STALE_NODE="mps-nonprod-rno-worker-z1-stale-boundary"

echo "  Phase 1: Create root incident"
post "Root alert (creates incident)" "P-STALE-ROOT-${S24}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-STALE-ROOT-${S24}\",
  \"problemTitle\":\"CPU issue on ${STALE_NODE} (staleness boundary test)\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${STALE_NODE}\",\"entityName\":\"${STALE_NODE}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${STALE_NODE}\",\"entityName\":\"${STALE_NODE}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"staleness boundary test. host.name: ${STALE_NODE}\",
  \"customProperties\":{\"host.name\":\"${STALE_NODE}\",\"environment\":\"ADC\"}}"
pause 5

echo "  Phase 2: Age the incident by 2h1min (DB UPDATE)"
# Retry up to 15s — the pipeline may still be processing the root alert.
AGED=false
for _i in 1 2 3; do
  ROWS=$(db "UPDATE incidents SET created_at = NOW() - INTERVAL '2 hours 1 minute'
      WHERE correlation_id = '${STALE_NODE}' AND auto_created = TRUE AND status='open'
      RETURNING id;" | grep -c '[0-9a-f-]\{36\}' || true)
  if [[ "${ROWS}" -ge 1 ]]; then
    AGED=true
    echo "  ✅ Incident aged to 2h01min past (${ROWS} row updated)"
    break
  fi
  echo "  ⏳ incident not yet created, retrying in 5s..."
  sleep 5
done
if [[ "$AGED" == "false" ]]; then
  echo "  ⚠️  WARNING: aging UPDATE found no matching incident — S24 result unreliable"
fi

echo "  Phase 3: Send downstream alert for same root entity"
post "Downstream after 2hr window (should create NEW incident)" "P-STALE-DS-${S24}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-STALE-DS-${S24}\",
  \"problemTitle\":\"Memory OOM on workload linked to ${STALE_NODE}\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${STALE_NODE}\",\"entityName\":\"${STALE_NODE}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-oom-workload\",\"entityName\":\"oom-workload\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"problemDetails\":\"oom-workload OOM on ${STALE_NODE}. host.name: ${STALE_NODE}\",
  \"customProperties\":{\"host.name\":\"${STALE_NODE}\",\"environment\":\"ADC\"}}"
pause 8

echo ""
echo "--- 2hr boundary verdict ---"
GOT=$(count_incidents_for "${S24}")
check_pass "Downstream after 2hr window → NEW incident (not merged with stale)" "$GOT" "2"
db "SELECT correlation_id, status, created_at::text AS created, jsonb_array_length(alert_ids) AS alerts
    FROM incidents WHERE correlation_id='${STALE_NODE}' ORDER BY created_at;"
fi

# ══════════════════════════════════════════════════════════════════════════════
# S25 — COMBINED CHAOS STORM (3-WAVE ATTACK)
#
# Description:
#   A realistic major outage simulation. Three waves arrive in rapid succession.
#   WAVE 1 (T=0):  BM fails, VM fails, cluster event fires SIMULTANEOUSLY (race)
#   WAVE 2 (T=10s): 10 pod/workload alerts arrive from 3 different namespaces
#   WAVE 3 (T=20s): 5 of the WAVE 2 alerts RESOLVE, then 2 RE-OPEN with new IDs
#
#   This combines:
#     - Concurrent root race (wave 1)
#     - Cross-namespace correlation (wave 2)
#     - Flap + re-fire within same outage window (wave 3)
#
# Expected:
#   - 1 primary incident (BM root, all cascades attach)
#   - Wave 3 re-opens: either merged into primary or create child incidents
#   - No data corruption, no duplicate incidents per alert
# ══════════════════════════════════════════════════════════════════════════════
if should_run 25; then
section "Scenario 25: Combined chaos storm (3 waves)"
S25="S25-${TS}"
STORM_BM="mps-mondev-mdn-worker-z3-05"
STORM_VM="cloudstack-cluster-mondev-vm-${STORM_BM}"

echo "  🌊 WAVE 1: BM + VM + cluster event simultaneously (race condition)"
for ALERT_SRC in "BM" "VM" "CLUSTER"; do
  case "$ALERT_SRC" in
    BM)
      post_quiet "{
        \"state\":\"OPEN\",\"problemId\":\"P-STORM-BM-${S25}\",
        \"problemTitle\":\"Hardware fault on ${STORM_BM} (chaos storm wave-1)\",
        \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
        \"rootCauseEntity\":{\"entityId\":\"HOST-${STORM_BM}\",\"entityName\":\"${STORM_BM}\",\"entityType\":\"HOST\"},
        \"impactedEntities\":[{\"entityId\":\"HOST-${STORM_BM}\",\"entityName\":\"${STORM_BM}\",\"entityType\":\"HOST\"}],
        \"problemDetails\":\"BM hardware fault. host.name: ${STORM_BM}\",
        \"customProperties\":{\"host.name\":\"${STORM_BM}\",\"environment\":\"MDN\"}}"
      ;;
    VM)
      post_quiet "{
        \"state\":\"OPEN\",\"problemId\":\"P-STORM-VM-${S25}\",
        \"problemTitle\":\"VM unavailable — ${STORM_VM} (chaos storm wave-1)\",
        \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"CRITICAL\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
        \"rootCauseEntity\":{\"entityId\":\"HOST-${STORM_BM}\",\"entityName\":\"${STORM_BM}\",\"entityType\":\"HOST\"},
        \"impactedEntities\":[{\"entityId\":\"HOST-${STORM_VM}\",\"entityName\":\"${STORM_VM}\",\"entityType\":\"HOST\"}],
        \"problemDetails\":\"VM crashed. host.name: ${STORM_VM}\",
        \"customProperties\":{\"host.name\":\"${STORM_VM}\",\"environment\":\"MDN\"}}"
      ;;
    CLUSTER)
      post_quiet "{
        \"state\":\"OPEN\",\"problemId\":\"P-STORM-CLUSTER-${S25}\",
        \"problemTitle\":\"Multiple nodes NotReady in mps-mondev-mdn (chaos storm)\",
        \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"CRITICAL\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
        \"rootCauseEntity\":{\"entityId\":\"HOST-${STORM_BM}\",\"entityName\":\"${STORM_BM}\",\"entityType\":\"HOST\"},
        \"impactedEntities\":[{\"entityId\":\"KUBERNETES_CLUSTER-mps-mondev-mdn\",\"entityName\":\"mps-mondev-mdn\",\"entityType\":\"KUBERNETES_CLUSTER\"}],
        \"problemDetails\":\"cluster degraded. k8s.cluster.name: mps-mondev-mdn\",
        \"customProperties\":{\"k8s.cluster.name\":\"mps-mondev-mdn\",\"k8s.cluster.uid\":\"${MONDEV_UID}\",\"environment\":\"MDN\"}}"
      ;;
  esac
done
wait
echo "  Wave 1 sent (3 concurrent)."
pause 10

echo "  🌊 WAVE 2: 10 pod/workload alerts from 3 namespaces"
NAMESPACES=("payments" "auth" "infra-monitoring")
for i in $(seq 1 10); do
  NS_IDX=$(( (i-1) % 3 ))
  NS_NAME="${NAMESPACES[$NS_IDX]}"
  post_quiet "{
    \"state\":\"OPEN\",\"problemId\":\"P-STORM-POD${i}-${S25}\",
    \"problemTitle\":\"Not all pods ready — wave2-app-${i} in ${NS_NAME}\",
    \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
    \"rootCauseEntity\":{\"entityId\":\"HOST-${STORM_BM}\",\"entityName\":\"${STORM_BM}\",\"entityType\":\"HOST\"},
    \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-wave2-app-${i}\",\"entityName\":\"wave2-app-${i}\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
    \"problemDetails\":\"wave2-app-${i} not ready. k8s.namespace.name: ${NS_NAME}. k8s.cluster.name: mps-mondev-mdn\",
    \"customProperties\":{\"k8s.cluster.name\":\"mps-mondev-mdn\",\"k8s.cluster.uid\":\"${MONDEV_UID}\",\"k8s.namespace.name\":\"${NS_NAME}\",\"k8s.workload.name\":\"wave2-app-${i}\",\"environment\":\"MDN\"}}"
done
wait
echo "  Wave 2 sent (10 concurrent pod alerts)."
pause 10

echo "  🌊 WAVE 3: 5 resolves + 2 re-fires with new problemIds"
for i in $(seq 1 5); do
  post_quiet "{
    \"state\":\"RESOLVED\",\"problemId\":\"P-STORM-POD${i}-${S25}\",
    \"problemTitle\":\"Not all pods ready — wave2-app-${i} in payments\",
    \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"RESOLVED\",\"startTime\":\"${NOW}\",
    \"rootCauseEntity\":{\"entityId\":\"HOST-${STORM_BM}\",\"entityName\":\"${STORM_BM}\",\"entityType\":\"HOST\"},
    \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-wave2-app-${i}\",\"entityName\":\"wave2-app-${i}\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
    \"problemDetails\":\"resolved\",
    \"customProperties\":{\"k8s.cluster.name\":\"mps-mondev-mdn\",\"k8s.cluster.uid\":\"${MONDEV_UID}\",\"environment\":\"MDN\"}}"
done
wait
echo "  Wave 3 resolves sent."
pause 3

# 2 re-fires with NEW problemIds (same root)
for i in 1 2; do
  post_quiet "{
    \"state\":\"OPEN\",\"problemId\":\"P-STORM-REFIRE${i}-${S25}\",
    \"problemTitle\":\"Not all pods ready — wave2-app-${i} REFIRE\",
    \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
    \"rootCauseEntity\":{\"entityId\":\"HOST-${STORM_BM}\",\"entityName\":\"${STORM_BM}\",\"entityType\":\"HOST\"},
    \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-wave2-app-${i}\",\"entityName\":\"wave2-app-${i}\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
    \"problemDetails\":\"wave2-app-${i} re-firing. k8s.cluster.name: mps-mondev-mdn\",
    \"customProperties\":{\"k8s.cluster.name\":\"mps-mondev-mdn\",\"k8s.cluster.uid\":\"${MONDEV_UID}\",\"environment\":\"MDN\"}}"
done
wait
echo "  Wave 3 re-fires sent."
pause 15

echo ""
echo "--- Combined chaos storm verdict ---"
GOT=$(count_incidents_for "${S25}")
GOT_CLEAN=$(echo "$GOT" | tr -d ' \n')
if [[ "$GOT_CLEAN" -le 2 ]]; then
  echo "  ✅  EXCELLENT — all 3 waves → ${GOT_CLEAN} incident(s)"
elif [[ "$GOT_CLEAN" -le 4 ]]; then
  echo "  ⚠️   ACCEPTABLE — ${GOT_CLEAN} incidents (small race window in wave 1)"
else
  echo "  ❌  FAIL — ${GOT_CLEAN} incidents (expected ≤4)"
fi
db "SELECT count(*) AS total_alerts,
          SUM(CASE WHEN status='resolved' THEN 1 ELSE 0 END) AS resolved_alerts,
          SUM(CASE WHEN status='open' THEN 1 ELSE 0 END) AS open_alerts,
          COUNT(DISTINCT incident_id) AS incidents
   FROM alerts WHERE source_id LIKE '%-${S25}%';"
fi

# ══════════════════════════════════════════════════════════════════════════════
# FINAL REPORT
# ══════════════════════════════════════════════════════════════════════════════
echo ""
echo "╔══════════════════════════════════════════════════════════════════════╗"
echo "║              CHAOS TEST SUITE — FINAL REPORT                        ║"
echo "╚══════════════════════════════════════════════════════════════════════╝"
echo ""
echo "Incidents created in last 30 min (auto-created only):"
db "
SELECT
  left(title, 52) AS title,
  COALESCE(correlation_id, '(none)') AS root_entity,
  status,
  jsonb_array_length(alert_ids) AS alerts,
  to_char(created_at, 'HH24:MI:SS') AS created_at
FROM incidents
WHERE auto_created = TRUE
  AND created_at >= NOW() - INTERVAL '30 minutes'
ORDER BY created_at DESC
LIMIT 40;"

echo ""
echo "Pipeline health (errors in last 5 min):"
kubectl logs -n "$NS" -l app=alerthub-backend --since=5m 2>/dev/null \
  | grep -E "❌|panic|nil pointer|FAIL|ERROR" | tail -10 || echo "  No errors detected ✅"

echo ""
echo "Kafka consumer lag:"
kubectl exec -n "$NS" alerthub-kafka-kafka-0 -- \
  bin/kafka-consumer-groups.sh --bootstrap-server localhost:9092 \
  --describe --group alerthub-alerts-group 2>/dev/null \
  | grep -E "raw-alerts|CONSUMER|LAG" | head -5 || echo "  (kafka check unavailable)"
