#!/usr/bin/env bash
# sim_inverted_arrival.sh — test out-of-order alert arrival against the pipeline
#
# In a real outage Dynatrace sometimes detects pod/node failures BEFORE it
# identifies the underlying BM cause.  These tests send alerts in reverse order
# to verify whether the pipeline correlates them correctly.
#
# Real topology used (from live Neo4j — same chain as sim_mdn_alert_storm.sh):
#   BM  : iapps-100-67-86-31  (CloudStack-MDN)
#   VM  : mps-mondev-mdn-worker-01  (100.67.76.80)
#   Node: mps-mondev-mdn-worker-01  (cluster mps-mondev-mdn)
#   Pods: variantgen-quip-api / alerthub-cosign / a2a-server (monitoring-dev)
#
# IMPORTANT — pod/node alerts in these tests deliberately omit the BM as
# rootCauseEntity (Dynatrace hasn't done its root analysis yet).  Each alert
# carries its OWN entity as the root.  The BM alert arrives last.
#
# Scenarios:
#   A  Pods → Node → BM, BM within 20s of first alert  (within hold window)
#      ✅ Expected: 1 incident — hold window re-eval merges into BM incident
#
#   B  Pods → Node → BM, BM after 90s                  (after hold window)
#      ❌ Expected gap: pod incidents already created → BM creates 2nd incident
#         → pipeline ends up with 2+ incidents (known limitation)
#
#   C  Node → Pods → BM, BM within 20s                 (node-first, fast BM)
#      ✅ Expected: 1 incident — node creates incident, pods attach, BM promotes root
#
#   D  Node → Pods → BM, BM after 90s                  (node-first, slow BM)
#      ⚠️  Node creates incident, pods attach during hold. BM arrives 90s later.
#         BM should attach/promote via cluster-storm match if labels align.
#
# Usage:
#   bash sim_inverted_arrival.sh              # all scenarios
#   bash sim_inverted_arrival.sh A            # single scenario

set -uo pipefail

ENDPOINT="https://aileron.example.com/api/v1/webhooks/dynatrace"
API_KEY="${API_KEY:-ah_c76ddeaa86c74818ac84997c0f2a8174eaba3bf0fdab295132acb8aba2238e5f}"
NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
TS=$(date +%s)

# ── Real topology constants ───────────────────────────────────────────────────
BM_NAME="iapps-100-67-86-31"
BM_FQDN="${BM_NAME}.example.com"
BM_ENTITY_ID="cloudstack-host-CloudStack-MDN-${BM_NAME}"
BM_DT_ID="HOST-${BM_ENTITY_ID}"

VM_NAME="mps-mondev-mdn-worker-01"
VM_IP="100.67.76.80"
VM_UUID="c7ab24f9-711a-4deb-9fed-e3c09b871a8f"
VM_DT_ID="HOST-cloudstack-vm-${VM_UUID}"

NODE_NAME="mps-mondev-mdn-worker-01"
NODE_DT_ID="KUBERNETES_NODE-k8s-node-mps-mondev-mdn-${NODE_NAME}"

CLUSTER="mps-mondev-mdn"
CLUSTER_UID="00a07750-e556-443e-89d9-80341edb472d"

# ── Helpers ───────────────────────────────────────────────────────────────────
post() {
  local label="$1" id="$2" payload="$3"
  printf "  → %-60s " "[$label] ($id)..."
  local code
  code=$(curl -sk -o /dev/null -w "%{http_code}" -X POST "$ENDPOINT" \
    -H "Content-Type: application/json" \
    -H "X-API-Key: ${API_KEY}" \
    -d "$payload")
  echo "HTTP $code"
}

section() {
  echo ""
  echo "══════════════════════════════════════════════════════════════"
  echo "  $1"
  echo "══════════════════════════════════════════════════════════════"
}

pause() { echo "  waiting ${1}s..."; sleep "$1"; }

dbcheck() {
  local tag="$1"
  echo ""
  echo "--- DB: incidents [${tag}] ---"
  kubectl exec -n aileron postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT '  [' || status || '] alerts=' || jsonb_array_length(alert_ids) ||
           '  corr=' || COALESCE(LEFT(correlation_id,35),'NULL') ||
           '  title=' || LEFT(title,45)
    FROM incidents
    WHERE auto_created=true AND updated_at > NOW()-INTERVAL '5 minutes'
    ORDER BY created_at DESC LIMIT 8;" 2>/dev/null | grep -v "^$"

  echo ""
  echo "--- DB: alert correlation_ids [${tag}] ---"
  kubectl exec -n aileron postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT '  ' || LEFT(title,42) || '  corr=' || COALESCE(LEFT(correlation_id,32),'NULL')
    FROM alerts
    WHERE source='dynatrace' AND created_at > NOW()-INTERVAL '5 minutes'
    ORDER BY created_at;" 2>/dev/null | grep -v "^$"
}

count_incidents_for() {
  kubectl exec -n aileron postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT COUNT(*) FROM incidents
    WHERE auto_created=true AND status IN ('open','investigating')
      AND correlation_id='${1}'
      AND updated_at > NOW()-INTERVAL '6 minutes';" 2>/dev/null | tr -d ' \n'
}

count_all_incidents_since() {
  kubectl exec -n aileron postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT COUNT(*) FROM incidents
    WHERE auto_created=true AND status IN ('open','investigating')
      AND updated_at > NOW()-INTERVAL '${1:-5} minutes';" 2>/dev/null | tr -d ' \n'
}

check_pass() {
  if [[ "$2" == "$3" ]]; then echo "  PASS  $1: $2"
  else echo "  FAIL  $1: expected=$3 got=$2"; fi
}

result_log() {
  echo ""
  echo "--- Pipeline log [last 2 min] ---"
  kubectl logs -n aileron -l app=alerthub-backend --since=2m 2>/dev/null \
    | grep -E "RCE |⬆️|⏳|⏰|🎯|CREATE_ROOT|ATTACH_TO_ROOT|promoted|deferred|Buffer" \
    | grep -v "^$" | tail -20
}

RUN_ALL=true
SELECTED=()
if [[ $# -gt 0 ]]; then
  RUN_ALL=false
  SELECTED=("$@")
fi
should_run() { $RUN_ALL && return 0; for s in "${SELECTED[@]}"; do [[ "$s" == "$1" ]] && return 0; done; return 1; }

# ── Pod alert payload builder (self-referencing rootCauseEntity — no BM yet) ─
# $1=pid $2=workload $3=ns
pod_payload_no_bm() {
  local pid="$1" workload="$2" ns="$3"
  cat <<EOF
{
  "state":"OPEN",
  "problemId":"${pid}",
  "problemTitle":"Not all pods ready — ${workload} in ${ns}",
  "impactLevel":"SERVICE",
  "severity":"AVAILABILITY",
  "status":"OPEN",
  "startTime":"${NOW}",
  "rootCauseEntity":{
    "entityId":"KUBERNETES_WORKLOAD-${workload}",
    "entityName":"${workload}",
    "entityType":"KUBERNETES_WORKLOAD"
  },
  "impactedEntities":[{
    "entityId":"KUBERNETES_WORKLOAD-${workload}",
    "entityName":"${workload}",
    "entityType":"KUBERNETES_WORKLOAD"
  }],
  "problemDetails":"OPEN Problem ${pid}\nKubernetes workload\n${workload}\n\nNot all pods ready\nDeployment ${workload} has 0/1 ready pods.\nk8s.workload.name: ${workload}\nk8s.workload.kind: Deployment\nk8s.namespace.name: ${ns}\nk8s.node.name: ${NODE_NAME}\nk8s.cluster.name: ${CLUSTER}\nk8s.cluster.uid: ${CLUSTER_UID}\n",
  "customProperties":{
    "k8s.workload.name":"${workload}",
    "k8s.workload.kind":"Deployment",
    "k8s.namespace.name":"${ns}",
    "k8s.node.name":"${NODE_NAME}",
    "k8s.cluster.name":"${CLUSTER}",
    "k8s.cluster.uid":"${CLUSTER_UID}",
    "environment":"ADC"
  }
}
EOF
}

# ── Node alert payload (self-referencing rootCauseEntity — no BM yet) ─────────
node_payload_no_bm() {
  local pid="$1"
  cat <<EOF
{
  "state":"OPEN",
  "problemId":"${pid}",
  "problemTitle":"Kubernetes node ${NODE_NAME} is NotReady",
  "impactLevel":"INFRASTRUCTURE",
  "severity":"AVAILABILITY",
  "status":"OPEN",
  "startTime":"${NOW}",
  "rootCauseEntity":{
    "entityId":"${NODE_DT_ID}",
    "entityName":"${NODE_NAME}",
    "entityType":"KUBERNETES_NODE"
  },
  "impactedEntities":[{
    "entityId":"${NODE_DT_ID}",
    "entityName":"${NODE_NAME}",
    "entityType":"KUBERNETES_NODE"
  }],
  "problemDetails":"OPEN Problem ${pid}\nKubernetes node\n${NODE_NAME}\n\nKubernetes node is not ready\nNode ${NODE_NAME} transitioned to NotReady state.\nk8s.node.name: ${NODE_NAME}\nk8s.cluster.name: ${CLUSTER}\nk8s.cluster.uid: ${CLUSTER_UID}\n",
  "customProperties":{
    "k8s.node.name":"${NODE_NAME}",
    "k8s.cluster.name":"${CLUSTER}",
    "k8s.cluster.uid":"${CLUSTER_UID}",
    "environment":"ADC"
  }
}
EOF
}

# ── BM alert payload (carries its own entity as root) ─────────────────────────
bm_payload() {
  local pid="$1"
  cat <<EOF
{
  "state":"OPEN",
  "problemId":"${pid}",
  "problemTitle":"Cloudstack_BareMetal - System Memory Utilization is High",
  "impactLevel":"INFRASTRUCTURE",
  "severity":"PERFORMANCE",
  "status":"OPEN",
  "startTime":"${NOW}",
  "rootCauseEntity":{
    "entityId":"${BM_DT_ID}",
    "entityName":"${BM_NAME}",
    "entityType":"HOST"
  },
  "impactedEntities":[{
    "entityId":"${BM_DT_ID}",
    "entityName":"${BM_FQDN}",
    "entityType":"HOST"
  }],
  "problemDetails":"OPEN Problem ${pid}\nHost\n${BM_FQDN}\n\nCloudstack_BareMetal - System Memory Utilization is High\nMemory utilization at 94% on KVM host ${BM_FQDN}\nhost.name: ${BM_FQDN}\n",
  "Tags":"[Environment]bm:true, ProcessType:libvertd, [Environment]dc:mdn, [Environment]kvm:true",
  "customProperties":{
    "host.name":"${BM_FQDN}",
    "environment":"ADC",
    "impacted_entity":"${BM_NAME}"
  }
}
EOF
}

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO A — Pods first → Node → BM arrives within 20s (inside hold window)
#
# Pipeline behavior:
#   Pod alerts → RCE NO_ROOT → DecisionMonitor → hold(45s) goroutine starts
#   Node alert → RCE NO_ROOT → DecisionMonitor → hold(45s) goroutine starts
#   BM arrives at ~20s → RCE NO_ROOT (no incident yet) → CREATE_ROOT or scoring
#   Hold goroutines wake at 45s → re-evaluate → find BM incident → merge ✅
#
# Expected: 1 incident, correlation_id=iapps-100-67-86-31
# ══════════════════════════════════════════════════════════════════════════════
if should_run A; then
section "Scenario A: Pods → Node → BM within 20s  [expect: 1 incident]"
SA="A-${TS}"

post "Pod: variantgen-quip-api (self-root)" "P-A-POD1-${SA}" "$(pod_payload_no_bm "P-A-POD1-${SA}" "variantgen-quip-api" "variantgen")"
pause 2
post "Pod: alerthub-cosign (self-root)"     "P-A-POD2-${SA}" "$(pod_payload_no_bm "P-A-POD2-${SA}" "alerthub-cosign" "monitoring-dev")"
pause 2
post "Pod: a2a-server (self-root)"          "P-A-POD3-${SA}" "$(pod_payload_no_bm "P-A-POD3-${SA}" "a2a-server" "monitoring-dev")"
pause 2
post "Node: NotReady (self-root)"           "P-A-NODE-${SA}" "$(node_payload_no_bm "P-A-NODE-${SA}")"

echo "  [BM arriving 12s after first pod — inside hold window]"
pause 12
post "BM: memory high (BM root, LATE)"      "P-A-BM-${SA}" "$(bm_payload "P-A-BM-${SA}")"

echo "  [waiting 55s for hold-window goroutines to re-evaluate and merge]"
pause 55
result_log
dbcheck "A-after-merge"

echo ""
echo "--- Assertions ---"
n=$(count_incidents_for "${BM_NAME}")
check_pass "A: single incident under BM root" "$n" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO B — Pods first → Node → BM arrives after 90s (after hold window)
#
# Pipeline behavior:
#   Pod alerts → hold(45s) → no BM incident yet → each creates own incident
#   Node alert → hold(45s) → no BM incident → creates own incident
#   BM arrives at 90s → CREATE_ROOT (new incident, corr=iapps-100-67-86-31)
#   suppressDescendantAlerts: marks Redis state for node, but pod incidents
#   already have incident_id set — NOT re-routed
#
# ❌ Expected gap: 2–5 separate incidents (pod + node + BM)
#    This is the known pipeline limitation: no late-root re-absorption
# ══════════════════════════════════════════════════════════════════════════════
if should_run B; then
section "Scenario B: Pods → Node → BM after 90s  [expect gap: multiple incidents]"
SB="B-${TS}"

# Record baseline incident count before sending
BEFORE=$(count_all_incidents_since 1)

post "Pod: variantgen-quip-api (self-root)" "P-B-POD1-${SB}" "$(pod_payload_no_bm "P-B-POD1-${SB}" "variantgen-quip-api" "variantgen")"
pause 2
post "Pod: alerthub-cosign (self-root)"     "P-B-POD2-${SB}" "$(pod_payload_no_bm "P-B-POD2-${SB}" "alerthub-cosign" "monitoring-dev")"
pause 2
post "Pod: a2a-server (self-root)"          "P-B-POD3-${SB}" "$(pod_payload_no_bm "P-B-POD3-${SB}" "a2a-server" "monitoring-dev")"
pause 2
post "Node: NotReady (self-root)"           "P-B-NODE-${SB}" "$(node_payload_no_bm "P-B-NODE-${SB}")"

echo "  [waiting 80s for hold window to expire — pods will create own incidents]"
pause 80
dbcheck "B-before-bm"

echo "  [BM arriving NOW — 86s after first pod, well after hold window]"
post "BM: memory high (BM root, VERY LATE)" "P-B-BM-${SB}" "$(bm_payload "P-B-BM-${SB}")"
pause 15
result_log
dbcheck "B-after-bm"

echo ""
echo "--- Assertions (gap demonstration) ---"
n_bm=$(count_incidents_for "${BM_NAME}")
n_node=$(count_incidents_for "${CLUSTER}/${CLUSTER}:kubernetes node is notready" 2>/dev/null || echo "?")
AFTER=$(count_all_incidents_since 3)
total_new=$(( AFTER - BEFORE ))
echo "  INFO  BM incident created: ${n_bm}"
echo "  INFO  Total new incidents in last 3 min: ~${total_new}"
if [[ "$total_new" -gt 2 ]]; then
  echo "  GAP   Multiple incidents created — BM did NOT re-absorb orphaned pod/node incidents"
  echo "        This is expected pipeline behavior: late root cannot reclaim existing incidents"
else
  echo "  INFO  Storm consolidated better than expected (temporal/cluster scoring may have helped)"
fi
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO C — Node first → Pods → BM within 20s  (node opens incident, fast BM)
#
# Pipeline behavior:
#   Node alert → RCE NO_ROOT → scoring → creates incident (corr=cluster:node-notready)
#   Pods → RCE NO_ROOT → scoring → merge into node incident via cluster match
#   BM arrives at ~18s → RCE NO_ROOT → scoring → cluster-storm match → attaches to
#   node incident → maybePromoteToRoot promotes incident root from node to BM ✅
#
# Expected: 1 incident, root promoted to BM
# ══════════════════════════════════════════════════════════════════════════════
if should_run C; then
section "Scenario C: Node → Pods → BM within 20s  [expect: 1 incident, root promoted]"
SC="C-${TS}"

post "Node: NotReady (self-root, FIRST)"    "P-C-NODE-${SC}" "$(node_payload_no_bm "P-C-NODE-${SC}")"
pause 3
post "Pod: variantgen-quip-api (self-root)" "P-C-POD1-${SC}" "$(pod_payload_no_bm "P-C-POD1-${SC}" "variantgen-quip-api" "variantgen")"
pause 2
post "Pod: alerthub-cosign (self-root)"     "P-C-POD2-${SC}" "$(pod_payload_no_bm "P-C-POD2-${SC}" "alerthub-cosign" "monitoring-dev")"
pause 2
post "Pod: a2a-server (self-root)"          "P-C-POD3-${SC}" "$(pod_payload_no_bm "P-C-POD3-${SC}" "a2a-server" "monitoring-dev")"

echo "  [BM arriving ~10s after node — inside hold window]"
pause 10
post "BM: memory high (BM root, LATE)"      "P-C-BM-${SC}" "$(bm_payload "P-C-BM-${SC}")"

echo "  [waiting 55s for hold window + root promotion to complete]"
pause 55
result_log
dbcheck "C-after-merge"

echo ""
echo "--- Assertions ---"
# Expect 1 incident for BM root OR the node incident with BM promoted as root
n_bm=$(count_incidents_for "${BM_NAME}")
echo "  INFO  Incident under BM corr_id: ${n_bm}"
echo "  INFO  (Also check if node incident got root promoted to BM in davis_ai_analysis)"
kubectl exec -n aileron postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT '  incident=' || id || ' corr=' || COALESCE(LEFT(correlation_id,35),'NULL') ||
         ' alerts=' || jsonb_array_length(alert_ids) ||
         ' root_entity=' || COALESCE(davis_ai_analysis->>'root_entity_label','?') ||
         ' root_level=' || COALESCE(davis_ai_analysis->>'root_level','?')
  FROM incidents
  WHERE auto_created=true AND updated_at > NOW()-INTERVAL '4 minutes'
  ORDER BY created_at DESC LIMIT 4;" 2>/dev/null | grep -v "^$"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO D — Node first → Pods → BM after 90s  (node-first, slow BM)
#
# Pipeline behavior:
#   Node alert → creates incident (within 45s hold if no other incident, else scoring)
#   Pods → merge into node incident via cluster match
#   BM arrives 90s later → tries to merge via cluster-storm match →
#   maybePromoteToRoot IF it attaches → promotes to BM root level
#   OR: creates its own incident if RCE/scoring can't find the node incident
#
# ⚠️  Uncertain outcome — depends on whether cluster-storm match window (30 min) fires
# ══════════════════════════════════════════════════════════════════════════════
if should_run D; then
section "Scenario D: Node → Pods → BM after 90s  [uncertain: promote or split]"
SD="D-${TS}"

post "Node: NotReady (self-root, FIRST)"    "P-D-NODE-${SD}" "$(node_payload_no_bm "P-D-NODE-${SD}")"
pause 3
post "Pod: variantgen-quip-api (self-root)" "P-D-POD1-${SD}" "$(pod_payload_no_bm "P-D-POD1-${SD}" "variantgen-quip-api" "variantgen")"
pause 2
post "Pod: alerthub-cosign (self-root)"     "P-D-POD2-${SD}" "$(pod_payload_no_bm "P-D-POD2-${SD}" "alerthub-cosign" "monitoring-dev")"
pause 2
post "Pod: a2a-server (self-root)"          "P-D-POD3-${SD}" "$(pod_payload_no_bm "P-D-POD3-${SD}" "a2a-server" "monitoring-dev")"

echo "  [waiting 80s — node incident will be created, pods merged into it]"
pause 80
dbcheck "D-before-bm"

echo "  [BM arriving NOW — 87s after node, after all hold windows expired]"
post "BM: memory high (BM root, VERY LATE)" "P-D-BM-${SD}" "$(bm_payload "P-D-BM-${SD}")"
pause 15
result_log
dbcheck "D-after-bm"

echo ""
echo "--- Assertions ---"
n_bm=$(count_incidents_for "${BM_NAME}")
echo "  INFO  Incident under BM corr_id: ${n_bm}"
echo "  INFO  Checking if BM attached to existing node incident (root promoted):"
kubectl exec -n aileron postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT '  incident=' || id || ' corr=' || COALESCE(LEFT(correlation_id,35),'NULL') ||
         ' alerts=' || jsonb_array_length(alert_ids) ||
         ' root_entity=' || COALESCE(davis_ai_analysis->>'root_entity_label','?') ||
         ' root_level=' || COALESCE(davis_ai_analysis->>'root_level','?')
  FROM incidents
  WHERE auto_created=true AND updated_at > NOW()-INTERVAL '5 minutes'
  ORDER BY jsonb_array_length(alert_ids) DESC LIMIT 5;" 2>/dev/null | grep -v "^$"
fi

echo ""
echo "══════════════════════════════════════════════════════════════"
echo "  SUMMARY — Inverted arrival test complete"
echo "══════════════════════════════════════════════════════════════"
echo ""
echo "  Scenario A (pods+node, BM fast 20s):  should merge into 1 incident"
echo "  Scenario B (pods+node, BM slow 90s):  known gap — likely 2-5 incidents"
echo "  Scenario C (node+pods, BM fast 20s):  should merge, root promoted to BM"
echo "  Scenario D (node+pods, BM slow 90s):  BM may attach+promote or create new"
echo ""
echo "  Fix for B: Re-absorption logic when CREATE_ROOT fires — reclaim existing"
echo "  incidents in the same cluster/blast-radius window and merge them."
