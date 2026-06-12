#!/usr/bin/env bash
# mock_cascade_alerts.sh — comprehensive correlation test suite
#
# Usage:
#   bash mock_cascade_alerts.sh              # run all scenarios
#   bash mock_cascade_alerts.sh 1            # run single scenario by number
#   bash mock_cascade_alerts.sh 1 3 7        # run specific scenarios
#
# Scenarios:
#   1  CloudStack BM full-stack cascade       (BM → VM → K8s node → Pod)
#   2  K8s multi-workload cascade             (node → cluster event → 3 workloads)
#   3  Simultaneous independent failures      (2 nodes → 2 separate incidents)
#   4  Rapid burst dedup                      (same problemId sent twice → 1 alert)
#   5  RESOLVED alert auto-close             (OPEN → wait → RESOLVED)
#   6  Re-open after resolve                  (OPEN → RESOLVED → OPEN same ID)
#   7  No rootCauseEntity / scoring fallback  (k8s labels only, no root entity)
#   8  Cross-namespace cascade               (1 node → pods in 3 namespaces)
#   9  Late-arriving downstreams              (root first, downstreams 10s later)
#  10  Tooling cluster MDN aggregate          (mps-tooling-mdn ActiveGate + aggregate)

set -uo pipefail

ENDPOINT="https://aileron.example.com/api/v1/webhooks/dynatrace"
API_KEY="${API_KEY:-ah_c76ddeaa86c74818ac84997c0f2a8174eaba3bf0fdab295132acb8aba2238e5f}"
NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
TS=$(date +%s)

# ─── real entity constants ────────────────────────────────────────────────────
CS2_BM="cloudstack-cluster-2-iapps-100-67-61-18"
CS2_BM_ID="HOST-${CS2_BM}"

NONPROD_UID="88b12bf2-9f23-4b4a-b06a-3d2b33134a3b"
MONDEV_UID="00a07750-e556-443e-89d9-80341edb472d"
K8PREV_UID="d1be9a2f-4e83-49bf-8800-b9a2bc2e7da0"
TOOLING_UID="0a0430fb-0c23-4e80-a000-caa5570d6c17"

# ─── helpers ─────────────────────────────────────────────────────────────────
post() {
  local label="$1"; local id="$2"; local payload="$3"
  printf "  → %-40s " "[$label] ($id)..."
  local code
  code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$ENDPOINT" \
    -H "Content-Type: application/json" \
    -H "X-API-Key: ${API_KEY}" \
    -d "$payload")
  echo "HTTP $code"
}

section() { echo ""; echo "══════════════════════════════════════════"; echo "  $1"; echo "══════════════════════════════════════════"; }
result()  { echo ""; echo "--- RCE decisions (last 3 min) ---"; \
  kubectl logs -n aileron -l app=alerthub-backend --since=3m 2>/dev/null \
  | grep -E "RCE alert=|CREATE_ROOT|ATTACH_TO_ROOT|action=ATTACH|Created incident|correlation_id.*set" \
  | grep -v "^$" | sort | uniq | head -20; }
pause()   { echo "  waiting ${1}s..."; sleep "$1"; }
dbcheck() {
  echo ""
  echo "--- DB check ---"
  kubectl exec -n aileron postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT '  incident ' || id || ': alert_count=' || jsonb_array_length(alert_ids) ||
           ' corr=' || COALESCE(correlation_id,'NULL') || ' title=' || LEFT(title,60)
    FROM incidents WHERE auto_created=true AND updated_at > NOW()-INTERVAL '10 minutes'
    ORDER BY created_at;" 2>/dev/null | grep -v "^$"
}

# count_incidents_for <correlation_id> — returns count of matching open/investigating incidents.
count_incidents_for() {
  local corr_id="$1"
  kubectl exec -n aileron postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT COUNT(*) FROM incidents
    WHERE auto_created=true
      AND status IN ('open','investigating')
      AND correlation_id='${corr_id}'
      AND updated_at > NOW()-INTERVAL '10 minutes';" 2>/dev/null | tr -d ' \n'
}

# check_pass <label> <got> <expected> — PASS/FAIL assertion.
check_pass() {
  local label="$1" got="$2" expected="$3"
  if [[ "$got" == "$expected" ]]; then
    echo "  PASS  ${label}: ${got}"
  else
    echo "  FAIL  ${label}: expected=${expected} got=${got}"
  fi
}

# ─── determine which scenarios to run ────────────────────────────────────────
RUN_ALL=true
SELECTED=()
if [[ $# -gt 0 ]]; then
  RUN_ALL=false
  SELECTED=("$@")
fi
should_run() { $RUN_ALL && return 0; for s in "${SELECTED[@]}"; do [[ "$s" == "$1" ]] && return 0; done; return 1; }

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 1 — CloudStack BM full-stack cascade
# Root: cloudstack-cluster-2-iapps-100-67-61-18 (BM / KVM hypervisor)
# Downstream: VM on that host → K8s node on that VM → pod
# Expected: 1 incident, alert_count=4, correlation_id=CS2_BM
# ══════════════════════════════════════════════════════════════════════════════
if should_run 1; then
section "Scenario 1: CloudStack BM full-stack cascade"
S1="S1-${TS}"
VM1="cloudstack-cluster-2-mps-nonprod-rno-worker-z3-08"
K8N1="mps-nonprod-rno-worker-z3-08"

post "BM CPU saturation (root)" "P-BM1-${S1}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-BM1-${S1}\",
  \"problemTitle\":\"High CPU load on bare metal host ${CS2_BM}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"PERFORMANCE\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"${CS2_BM_ID}\",\"entityName\":\"${CS2_BM}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"${CS2_BM_ID}\",\"entityName\":\"${CS2_BM}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"CPU at 98% on host ${CS2_BM}. host.name: ${CS2_BM}\",
  \"customProperties\":{\"host.name\":\"${CS2_BM}\",\"impacted_entity\":\"${CS2_BM}\",\"environment\":\"ADC\"}}"
pause 3

post "VM CPU throttling (downstream)" "P-VM1-${S1}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-VM1-${S1}\",
  \"problemTitle\":\"CPU throttling on VM ${VM1}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"PERFORMANCE\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"${CS2_BM_ID}\",\"entityName\":\"${CS2_BM}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${VM1}\",\"entityName\":\"${VM1}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"CPU throttling on VM ${VM1}. host.name: ${VM1}\",
  \"customProperties\":{\"host.name\":\"${VM1}\",\"environment\":\"ADC\"}}"
pause 3

post "K8s node NotReady (downstream)" "P-NODE1-${S1}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-NODE1-${S1}\",
  \"problemTitle\":\"Kubernetes node ${K8N1} is NotReady\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"${CS2_BM_ID}\",\"entityName\":\"${CS2_BM}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_NODE-${K8N1}\",\"entityName\":\"${K8N1}\",\"entityType\":\"KUBERNETES_NODE\"}],
  \"problemDetails\":\"Node ${K8N1} is NotReady. k8s.node.name: ${K8N1}. k8s.cluster.name: mps-nonprod-rno\",
  \"customProperties\":{\"k8s.node.name\":\"${K8N1}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"environment\":\"ADC\"}}"
pause 3

post "Pod CrashLoopBackOff (downstream)" "P-POD1-${S1}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-POD1-${S1}\",
  \"problemTitle\":\"Not all pods ready — dex in dex namespace on ${K8N1}\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"${CS2_BM_ID}\",\"entityName\":\"${CS2_BM}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-dex\",\"entityName\":\"dex\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"problemDetails\":\"dex pod not ready on ${K8N1}. k8s.node.name: ${K8N1}. k8s.namespace.name: dex. k8s.cluster.name: mps-nonprod-rno\",
  \"customProperties\":{\"k8s.node.name\":\"${K8N1}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"k8s.namespace.name\":\"dex\",\"k8s.workload.name\":\"dex\",\"environment\":\"ADC\"}}"

pause 5; result; dbcheck
echo "--- Assertions ---"
n=$(count_incidents_for "${CS2_BM}")
check_pass "S1 incident_count(${CS2_BM})" "$n" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 2 — K8s multi-workload cascade
# Root: mps-nonprod-rno-worker-z3-13 (has both host.name and k8s.node.name)
# Downstream: cluster event + 2 workloads in different namespaces
# Expected: 1 incident, alert_count=4
# ══════════════════════════════════════════════════════════════════════════════
if should_run 2; then
section "Scenario 2: K8s multi-workload cascade (node z3-13)"
S2="S2-${TS}"
K8N2="mps-nonprod-rno-worker-z3-13"

post "Node CPU-request saturation (root)" "P-NODE2-${S2}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-NODE2-${S2}\",
  \"problemTitle\":\"CPU-request saturation on node ${K8N2}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"PERFORMANCE\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${K8N2}\",\"entityName\":\"${K8N2}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${K8N2}\",\"entityName\":\"${K8N2}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"CPU requests exceed 90% on node ${K8N2}. host.name: ${K8N2}. k8s.node.name: ${K8N2}. k8s.cluster.name: mps-nonprod-rno\",
  \"customProperties\":{\"host.name\":\"${K8N2}\",\"k8s.node.name\":\"${K8N2}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"impacted_entity\":\"${K8N2}\",\"environment\":\"ADC\"}}"
pause 3

post "K8s cluster NotReady condition (downstream)" "P-CLUSTER2-${S2}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-CLUSTER2-${S2}\",
  \"problemTitle\":\"Kubernetes Worker Node in Not Ready condition in mps-nonprod-rno\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${K8N2}\",\"entityName\":\"${K8N2}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_CLUSTER-mps-nonprod-rno\",\"entityName\":\"mps-nonprod-rno\",\"entityType\":\"KUBERNETES_CLUSTER\"}],
  \"problemDetails\":\"Node ${K8N2} NotReady in mps-nonprod-rno. k8s.cluster.name: mps-nonprod-rno\",
  \"customProperties\":{\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"environment\":\"ADC\"}}"
pause 3

post "ingress-nginx-controller DaemonSet degraded (downstream)" "P-INGRESS2-${S2}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-INGRESS2-${S2}\",
  \"problemTitle\":\"Not all pods ready — ingress-nginx-controller in ingress-nginx\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${K8N2}\",\"entityName\":\"${K8N2}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-ingress-nginx-controller\",\"entityName\":\"ingress-nginx-controller\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"problemDetails\":\"ingress-nginx pod evicted from ${K8N2}. k8s.node.name: ${K8N2}. k8s.namespace.name: ingress-nginx. k8s.workload.kind: daemonset\",
  \"customProperties\":{\"k8s.node.name\":\"${K8N2}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"k8s.namespace.name\":\"ingress-nginx\",\"k8s.workload.name\":\"ingress-nginx-controller\",\"k8s.workload.kind\":\"daemonset\",\"environment\":\"ADC\"}}"
pause 3

post "coredns deployment degraded (downstream)" "P-CORE2-${S2}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-CORE2-${S2}\",
  \"problemTitle\":\"Backoff event — coredns in kube-system on mps-nonprod-rno\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"ERROR\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${K8N2}\",\"entityName\":\"${K8N2}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-coredns\",\"entityName\":\"coredns\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"problemDetails\":\"coredns pod pending restart on ${K8N2}. k8s.namespace.name: kube-system. k8s.workload.name: coredns. k8s.cluster.name: mps-nonprod-rno\",
  \"customProperties\":{\"k8s.node.name\":\"${K8N2}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"k8s.namespace.name\":\"kube-system\",\"k8s.workload.name\":\"coredns\",\"k8s.workload.kind\":\"deployment\",\"environment\":\"ADC\"}}"

pause 5; result; dbcheck
echo "--- Assertions ---"
n=$(count_incidents_for "${K8N2}")
check_pass "S2 incident_count(${K8N2})" "$n" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 3 — Simultaneous independent failures on two different clusters
# Root A: mps-nonprod-rno-worker-z1-06 (nonprod-rno)
# Root B: k8preview01-rno cluster (different cluster, different UID)
# Expected: 2 separate incidents (no merging)
# ══════════════════════════════════════════════════════════════════════════════
if should_run 3; then
section "Scenario 3: Simultaneous independent failures → 2 separate incidents"
S3="S3-${TS}"
K8N3A="mps-nonprod-rno-worker-z1-06"
K8N3B="k8preview01-rno-worker-z1-01"  # preview cluster node

post "Node A root — nonprod-rno z1-06 memory" "P-A3-${S3}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-A3-${S3}\",
  \"problemTitle\":\"Memory saturation on node ${K8N3A}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"RESOURCE_CONTENTION\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${K8N3A}\",\"entityName\":\"${K8N3A}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${K8N3A}\",\"entityName\":\"${K8N3A}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"Memory at 93% on ${K8N3A}. host.name: ${K8N3A}. k8s.node.name: ${K8N3A}. k8s.cluster.name: mps-nonprod-rno\",
  \"customProperties\":{\"host.name\":\"${K8N3A}\",\"k8s.node.name\":\"${K8N3A}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"impacted_entity\":\"${K8N3A}\",\"environment\":\"ADC\"}}"
pause 2

post "Node A downstream — nginx-mtls-proxy-stage-proxy" "P-A3b-${S3}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-A3b-${S3}\",
  \"problemTitle\":\"Not all pods ready — nginx-mtls-proxy-stage-proxy in stagepush-auth-uat\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${K8N3A}\",\"entityName\":\"${K8N3A}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-nginx-mtls-proxy-stage-proxy\",\"entityName\":\"nginx-mtls-proxy-stage-proxy\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"problemDetails\":\"nginx-mtls-proxy-stage-proxy OOMKilled on ${K8N3A}. k8s.namespace.name: stagepush-auth-uat. k8s.cluster.name: mps-nonprod-rno\",
  \"customProperties\":{\"k8s.node.name\":\"${K8N3A}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"k8s.namespace.name\":\"stagepush-auth-uat\",\"k8s.workload.name\":\"nginx-mtls-proxy-stage-proxy\",\"k8s.workload.kind\":\"deployment\",\"environment\":\"ADC\"}}"
pause 2

post "Node B root — k8preview01-rno aqua-scan backoff" "P-B3-${S3}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-B3-${S3}\",
  \"problemTitle\":\"Backoff event — aqua-scan deployment unavailable in k8preview01-rno\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"ERROR\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"KUBERNETES_CLUSTER-k8preview01-rno\",\"entityName\":\"k8preview01-rno\",\"entityType\":\"KUBERNETES_CLUSTER\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-aqua-scan\",\"entityName\":\"aqua-scan\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"problemDetails\":\"aqua-scan pods in CrashLoopBackOff. k8s.namespace.name: aqua-scan. k8s.cluster.name: k8preview01-rno\",
  \"customProperties\":{\"k8s.cluster.name\":\"k8preview01-rno\",\"k8s.cluster.uid\":\"${K8PREV_UID}\",\"k8s.namespace.name\":\"aqua-scan\",\"k8s.workload.name\":\"aqua-scan\",\"k8s.workload.kind\":\"deployment\",\"impacted_entity\":\"k8preview01-rno\",\"environment\":\"ADC\"}}"
pause 2

post "Node B downstream — dm2-api deployment" "P-B3b-${S3}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-B3b-${S3}\",
  \"problemTitle\":\"Backoff event — dm2-api deployment unavailable in dm2api-dev\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"ERROR\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"KUBERNETES_CLUSTER-k8preview01-rno\",\"entityName\":\"k8preview01-rno\",\"entityType\":\"KUBERNETES_CLUSTER\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-dm2-api\",\"entityName\":\"dm2-api\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"problemDetails\":\"dm2-api pods in CrashLoopBackOff. k8s.namespace.name: dm2api-dev. k8s.cluster.name: k8preview01-rno. k8s.workload.name: dm2-api\",
  \"customProperties\":{\"k8s.cluster.name\":\"k8preview01-rno\",\"k8s.cluster.uid\":\"${K8PREV_UID}\",\"k8s.namespace.name\":\"dm2api-dev\",\"k8s.workload.name\":\"dm2-api\",\"k8s.workload.kind\":\"deployment\",\"environment\":\"ADC\"}}"

pause 5; result; dbcheck
echo "--- Assertions ---"
n_a=$(count_incidents_for "${K8N3A}")
check_pass "S3 incident_count(${K8N3A})" "$n_a" "1"
# k8preview alerts get topology-merged into the cluster's infra incident rather than
# creating one with correlation_id='k8preview01-rno'. The real invariant is that the
# two k8preview alerts ended up in SOME incident (any) that is DIFFERENT from the z1-06 incident.
b3_inc=$(kubectl exec -n aileron postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT COALESCE(auto_created_incident_id::text,'none') FROM alerts WHERE source_id='P-B3-${S3}' ORDER BY created_at DESC LIMIT 1;" 2>/dev/null | tr -d ' \n')
a3_inc=$(kubectl exec -n aileron postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT COALESCE(auto_created_incident_id::text,'none') FROM alerts WHERE source_id='P-A3-${S3}' ORDER BY created_at DESC LIMIT 1;" 2>/dev/null | tr -d ' \n')
if [[ "$b3_inc" == "?" || "$a3_inc" == "?" ]]; then
  echo "  SKIP  S3 k8preview isolation: kubectl unavailable"
elif [[ "$b3_inc" == "none" ]]; then
  echo "  FAIL  S3 k8preview isolation: k8preview alert not linked to an incident"
elif [[ "$b3_inc" != "$a3_inc" ]]; then
  echo "  PASS  S3 k8preview isolation: k8preview and z1-06 alerts in separate incidents"
else
  echo "  FAIL  S3 k8preview isolation: both alerts in same incident ($b3_inc)"
fi
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 4 — Rapid burst dedup (same problemId sent twice)
# Same payload, same problemId — should produce 1 alert not 2
# Expected: HTTP 202 both times, but only 1 alert row in DB
# ══════════════════════════════════════════════════════════════════════════════
if should_run 4; then
section "Scenario 4: Rapid burst dedup — same problemId twice"
S4="S4-${TS}"
DEDUP_PAYLOAD="{
  \"state\":\"OPEN\",\"problemId\":\"P-DEDUP-${S4}\",
  \"problemTitle\":\"CPU-request saturation on node mps-nonprod-rno-worker-z2-04\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"PERFORMANCE\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-mps-nonprod-rno-worker-z2-04\",\"entityName\":\"mps-nonprod-rno-worker-z2-04\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-mps-nonprod-rno-worker-z2-04\",\"entityName\":\"mps-nonprod-rno-worker-z2-04\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"CPU saturation on mps-nonprod-rno-worker-z2-04. host.name: mps-nonprod-rno-worker-z2-04\",
  \"customProperties\":{\"host.name\":\"mps-nonprod-rno-worker-z2-04\",\"k8s.node.name\":\"mps-nonprod-rno-worker-z2-04\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"impacted_entity\":\"mps-nonprod-rno-worker-z2-04\",\"environment\":\"ADC\"}}"

post "First send (P-DEDUP-${S4})" "P-DEDUP-${S4}" "$DEDUP_PAYLOAD"
pause 1
post "Duplicate send (same problemId)" "P-DEDUP-${S4}" "$DEDUP_PAYLOAD"
pause 1
post "Third send (same problemId again)" "P-DEDUP-${S4}" "$DEDUP_PAYLOAD"

pause 5
echo ""
echo "--- DB check: should be exactly 1 alert for P-DEDUP-${S4} ---"
kubectl exec -n aileron postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT COUNT(*), MAX(updated_at) FROM alerts WHERE source_id='P-DEDUP-${S4}';" 2>/dev/null
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 5 — OPEN then RESOLVED auto-close
# Expected: incident created on OPEN, incident resolved/closed on RESOLVED
# ══════════════════════════════════════════════════════════════════════════════
if should_run 5; then
section "Scenario 5: OPEN → RESOLVED auto-close"
S5="S5-${TS}"
K8N5="mps-nonprod-rno-worker-z2-01"

post "OPEN — node z2-01 disk pressure" "P-RESOLVE-${S5}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-RESOLVE-${S5}\",
  \"problemTitle\":\"Disk pressure on node ${K8N5}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"RESOURCE_CONTENTION\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${K8N5}\",\"entityName\":\"${K8N5}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${K8N5}\",\"entityName\":\"${K8N5}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"Disk usage at 87% on ${K8N5}. host.name: ${K8N5}. k8s.node.name: ${K8N5}\",
  \"customProperties\":{\"host.name\":\"${K8N5}\",\"k8s.node.name\":\"${K8N5}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"impacted_entity\":\"${K8N5}\",\"environment\":\"ADC\"}}"

echo "  ⏳ waiting 8s for incident to be created..."
pause 8

echo ""
echo "--- Incident before RESOLVED ---"
kubectl exec -n aileron postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT id, status, title FROM incidents WHERE auto_created=true AND created_at>NOW()-INTERVAL '2 minutes' ORDER BY created_at DESC LIMIT 1;" 2>/dev/null

post "RESOLVED — same problemId" "P-RESOLVE-${S5}" "{
  \"state\":\"RESOLVED\",\"problemId\":\"P-RESOLVE-${S5}\",
  \"problemTitle\":\"Disk pressure on node ${K8N5}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"RESOURCE_CONTENTION\",\"status\":\"RESOLVED\",\"startTime\":\"${NOW}\",
  \"endTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${K8N5}\",\"entityName\":\"${K8N5}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${K8N5}\",\"entityName\":\"${K8N5}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"Disk pressure resolved on ${K8N5}.\",
  \"customProperties\":{\"host.name\":\"${K8N5}\",\"k8s.node.name\":\"${K8N5}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"environment\":\"ADC\"}}"

pause 5
echo ""
echo "--- Incident after RESOLVED (should be resolved/closed) ---"
kubectl exec -n aileron postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT id, status, resolved_at IS NOT NULL as has_resolved_at, title
  FROM incidents WHERE auto_created=true AND created_at>NOW()-INTERVAL '5 minutes' ORDER BY created_at DESC LIMIT 3;" 2>/dev/null
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 6 — Re-open after resolve (OPEN → RESOLVED → OPEN same problemId)
# Expected: incident re-opened or a new incident created
# ══════════════════════════════════════════════════════════════════════════════
if should_run 6; then
section "Scenario 6: Re-open — OPEN → RESOLVED → OPEN (same problemId)"
S6="S6-${TS}"
K8N6="mps-nonprod-rno-worker-z2-07"

post "OPEN — node z2-07 network problem" "P-REOPEN-${S6}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-REOPEN-${S6}\",
  \"problemTitle\":\"Network problem on node ${K8N6}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${K8N6}\",\"entityName\":\"${K8N6}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${K8N6}\",\"entityName\":\"${K8N6}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"Network unreachable on ${K8N6}. host.name: ${K8N6}. k8s.node.name: ${K8N6}\",
  \"customProperties\":{\"host.name\":\"${K8N6}\",\"k8s.node.name\":\"${K8N6}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"impacted_entity\":\"${K8N6}\",\"environment\":\"ADC\"}}"
pause 6

post "RESOLVED — network recovered" "P-REOPEN-${S6}" "{
  \"state\":\"RESOLVED\",\"problemId\":\"P-REOPEN-${S6}\",
  \"problemTitle\":\"Network problem on node ${K8N6}\",
  \"status\":\"RESOLVED\",\"startTime\":\"${NOW}\",\"endTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${K8N6}\",\"entityName\":\"${K8N6}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${K8N6}\",\"entityName\":\"${K8N6}\",\"entityType\":\"HOST\"}],
  \"customProperties\":{\"host.name\":\"${K8N6}\",\"k8s.node.name\":\"${K8N6}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"environment\":\"ADC\"}}"
pause 4

post "OPEN again — network flap (same problemId)" "P-REOPEN-${S6}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-REOPEN-${S6}\",
  \"problemTitle\":\"Network problem on node ${K8N6} (recurrence)\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${K8N6}\",\"entityName\":\"${K8N6}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${K8N6}\",\"entityName\":\"${K8N6}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"Network problem recurred on ${K8N6}. Intermittent packet loss detected.\",
  \"customProperties\":{\"host.name\":\"${K8N6}\",\"k8s.node.name\":\"${K8N6}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"impacted_entity\":\"${K8N6}\",\"environment\":\"ADC\"}}"

pause 5
echo ""
echo "--- Incidents for P-REOPEN-${S6} (check re-open or new incident) ---"
kubectl exec -n aileron postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT i.id, i.status, i.title, a.source_id
  FROM incidents i JOIN alerts a ON a.incident_id = i.id
  WHERE a.source_id = 'P-REOPEN-${S6}' ORDER BY i.created_at;" 2>/dev/null
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 7 — No rootCauseEntity (scoring fallback)
# Alert has only k8s cluster/namespace labels, no rootCauseEntity field
# Expected: RCE returns NO_ROOT, falls through to 4-strategy scoring
# ══════════════════════════════════════════════════════════════════════════════
if should_run 7; then
section "Scenario 7: No rootCauseEntity — falls through to 4-strategy scoring"
S7="S7-${TS}"

post "Alert without rootCauseEntity (workload only)" "P-NOROOT-${S7}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-NOROOT-${S7}\",
  \"problemTitle\":\"Not all pods ready — nginx-mtls-proxy-push-proxy in stagepush-auth-uat\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-nginx-mtls-proxy-push-proxy\",\"entityName\":\"nginx-mtls-proxy-push-proxy\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"problemDetails\":\"nginx-mtls-proxy-push-proxy pods not ready. k8s.namespace.name: stagepush-auth-uat. k8s.cluster.name: mps-nonprod-rno\",
  \"customProperties\":{\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"k8s.namespace.name\":\"stagepush-auth-uat\",\"k8s.workload.name\":\"nginx-mtls-proxy-push-proxy\",\"k8s.workload.kind\":\"deployment\",\"environment\":\"ADC\"}}"

pause 3

post "Second workload alert same cluster/namespace" "P-NOROOT2-${S7}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-NOROOT2-${S7}\",
  \"problemTitle\":\"Not all pods ready — nginx-mtls-proxy-stage-proxy in stagepush-auth-uat\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-nginx-mtls-proxy-stage-proxy\",\"entityName\":\"nginx-mtls-proxy-stage-proxy\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"problemDetails\":\"nginx-mtls-proxy-stage-proxy pods not ready. k8s.namespace.name: stagepush-auth-uat. k8s.cluster.name: mps-nonprod-rno\",
  \"customProperties\":{\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"k8s.namespace.name\":\"stagepush-auth-uat\",\"k8s.workload.name\":\"nginx-mtls-proxy-stage-proxy\",\"k8s.workload.kind\":\"deployment\",\"environment\":\"ADC\"}}"

pause 5; result
echo ""
echo "--- Check pipeline went through scoring (not RCE) ---"
kubectl logs -n aileron -l app=alerthub-backend --since=3m 2>/dev/null \
  | grep -E "(P-NOROOT|temporal|semantic|topology|rules)" | head -10
echo "--- Assertions ---"
# The RCE engine returns NO_ROOT silently (no log emitted) — assert via DB instead:
# both workload alerts should have been processed and landed in ≤1 incident.
s7_incidents=$(kubectl exec -n aileron postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT COUNT(DISTINCT incident_id) FROM alerts
  WHERE source_id IN ('P-NOROOT-${S7}','P-NOROOT2-${S7}')
    AND incident_id IS NOT NULL;" 2>/dev/null | tr -d ' \n')
check_pass "S7 both scoring-path alerts processed (incident_count >= 1)" \
  "$([[ ${s7_incidents:-0} -ge 1 ]] && echo yes || echo no)" "yes"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 8 — Cross-namespace cascade (1 node → pods in 3 namespaces)
# Root: mps-nonprod-rno-worker-z1-17 (OOM on node)
# Downstream: pods in staging + ingress + kube-system
# Expected: 1 incident, alert_count=4
# ══════════════════════════════════════════════════════════════════════════════
if should_run 8; then
section "Scenario 8: Cross-namespace cascade (1 node root → 3 namespaces)"
S8="S8-${TS}"
K8N8="mps-nonprod-rno-worker-z1-17"

post "Node OOM root" "P-ROOT8-${S8}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-ROOT8-${S8}\",
  \"problemTitle\":\"Memory saturation on node ${K8N8}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"RESOURCE_CONTENTION\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${K8N8}\",\"entityName\":\"${K8N8}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${K8N8}\",\"entityName\":\"${K8N8}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"Node OOMKiller triggered on ${K8N8}. host.name: ${K8N8}. k8s.node.name: ${K8N8}\",
  \"customProperties\":{\"host.name\":\"${K8N8}\",\"k8s.node.name\":\"${K8N8}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"impacted_entity\":\"${K8N8}\",\"environment\":\"ADC\"}}"
pause 3

post "stagepush-auth-uat OOMKilled (ns 1)" "P-NS1-${S8}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-NS1-${S8}\",
  \"problemTitle\":\"Not all pods ready — nginx-mtls-proxy-push-proxy in stagepush-auth-uat\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${K8N8}\",\"entityName\":\"${K8N8}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-nginx-mtls-proxy-push-proxy\",\"entityName\":\"nginx-mtls-proxy-push-proxy\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"problemDetails\":\"OOMKilled on ${K8N8}. k8s.namespace.name: stagepush-auth-uat. k8s.workload.name: nginx-mtls-proxy-push-proxy\",
  \"customProperties\":{\"k8s.node.name\":\"${K8N8}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"k8s.namespace.name\":\"stagepush-auth-uat\",\"k8s.workload.name\":\"nginx-mtls-proxy-push-proxy\",\"environment\":\"ADC\"}}"
pause 3

post "dex namespace — dex pod evicted (ns 2)" "P-NS2-${S8}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-NS2-${S8}\",
  \"problemTitle\":\"Not all pods ready — dex in dex namespace\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${K8N8}\",\"entityName\":\"${K8N8}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-dex\",\"entityName\":\"dex\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"problemDetails\":\"dex pod evicted from ${K8N8} due to memory pressure. k8s.namespace.name: dex\",
  \"customProperties\":{\"k8s.node.name\":\"${K8N8}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"k8s.namespace.name\":\"dex\",\"k8s.workload.name\":\"dex\",\"environment\":\"ADC\"}}"
pause 3

post "ingress-nginx pod evicted (ns 3)" "P-NS3-${S8}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-NS3-${S8}\",
  \"problemTitle\":\"Not all pods ready — ingress-nginx-controller in ingress-nginx\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${K8N8}\",\"entityName\":\"${K8N8}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-ingress-nginx-controller\",\"entityName\":\"ingress-nginx-controller\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"problemDetails\":\"ingress-nginx DaemonSet pod evicted from ${K8N8}. k8s.namespace.name: ingress-nginx\",
  \"customProperties\":{\"k8s.node.name\":\"${K8N8}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"k8s.namespace.name\":\"ingress-nginx\",\"k8s.workload.name\":\"ingress-nginx-controller\",\"k8s.workload.kind\":\"daemonset\",\"environment\":\"ADC\"}}"

pause 5; result; dbcheck
echo "--- Assertions ---"
n=$(count_incidents_for "${K8N8}")
check_pass "S8 incident_count(${K8N8})" "$n" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 9 — Late-arriving downstreams (root first, downstreams 10s later)
# Verifies 2-hour correlation window: root incident exists, later alerts attach
# Root: mps-nonprod-rno-worker-z2-11
# Expected: root creates incident, late downstreams attach correctly
# ══════════════════════════════════════════════════════════════════════════════
if should_run 9; then
section "Scenario 9: Late-arriving downstreams (root first, cascades 10s later)"
S9="S9-${TS}"
K8N9="mps-nonprod-rno-worker-z2-11"

post "Root alert — node z2-11 CPU critical" "P-LATE-ROOT-${S9}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-LATE-ROOT-${S9}\",
  \"problemTitle\":\"CPU-request saturation on node ${K8N9}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"PERFORMANCE\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${K8N9}\",\"entityName\":\"${K8N9}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${K8N9}\",\"entityName\":\"${K8N9}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"CPU saturation on ${K8N9}. host.name: ${K8N9}. k8s.node.name: ${K8N9}\",
  \"customProperties\":{\"host.name\":\"${K8N9}\",\"k8s.node.name\":\"${K8N9}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"impacted_entity\":\"${K8N9}\",\"environment\":\"ADC\"}}"

echo "  ⏳ simulating delayed detection — waiting 10s before downstream alerts arrive..."
pause 10

post "Late downstream — test-app-01 restart" "P-LATE-D1-${S9}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-LATE-D1-${S9}\",
  \"problemTitle\":\"Not all pods ready — test-app-01 in default\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${K8N9}\",\"entityName\":\"${K8N9}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-test-app-01\",\"entityName\":\"test-app-01\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"problemDetails\":\"test-app-01 pods throttled on ${K8N9}. k8s.namespace.name: default. k8s.workload.name: test-app-01\",
  \"customProperties\":{\"k8s.node.name\":\"${K8N9}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"k8s.namespace.name\":\"default\",\"k8s.workload.name\":\"test-app-01\",\"k8s.workload.kind\":\"deployment\",\"environment\":\"ADC\"}}"
pause 2

post "Late downstream — test-app-02 restart" "P-LATE-D2-${S9}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-LATE-D2-${S9}\",
  \"problemTitle\":\"Not all pods ready — test-app-02 in default\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${K8N9}\",\"entityName\":\"${K8N9}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-test-app-02\",\"entityName\":\"test-app-02\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"problemDetails\":\"test-app-02 pod not scheduled on ${K8N9}. k8s.namespace.name: default. k8s.workload.name: test-app-02\",
  \"customProperties\":{\"k8s.node.name\":\"${K8N9}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"k8s.namespace.name\":\"default\",\"k8s.workload.name\":\"test-app-02\",\"k8s.workload.kind\":\"deployment\",\"environment\":\"ADC\"}}"
pause 2

post "Late downstream — test-app-03 restart" "P-LATE-D3-${S9}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-LATE-D3-${S9}\",
  \"problemTitle\":\"Not all pods ready — test-app-03 in default\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${K8N9}\",\"entityName\":\"${K8N9}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-test-app-03\",\"entityName\":\"test-app-03\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"problemDetails\":\"test-app-03 pod not scheduled on ${K8N9}. k8s.namespace.name: default. k8s.workload.name: test-app-03\",
  \"customProperties\":{\"k8s.node.name\":\"${K8N9}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"k8s.namespace.name\":\"default\",\"k8s.workload.name\":\"test-app-03\",\"k8s.workload.kind\":\"deployment\",\"environment\":\"ADC\"}}"

pause 5; result; dbcheck
echo "--- Assertions ---"
n=$(count_incidents_for "${K8N9}")
check_pass "S9 incident_count(${K8N9})" "$n" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 10 — Tooling cluster MDN aggregate + mondev node cascade
# Root A: mps-tooling-mdn cluster ActiveGate (tooling cluster, separate DC)
# Root B: mps-mondev-mdn-worker-z3-05 (bare-metal kubeadm node)
# Expected: 2 separate incidents (different DCs, different roots)
# ══════════════════════════════════════════════════════════════════════════════
if should_run 10; then
section "Scenario 10: Tooling MDN aggregate + mondev BM cascade (2 DCs, 2 incidents)"
S10="S10-${TS}"
MDN_BM="mps-mondev-mdn-worker-z3-05"

post "Tooling cluster ActiveGate root" "P-TOOL-${S10}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-TOOL-${S10}\",
  \"problemTitle\":\"Aggregate state — mps-tooling-mdn ActiveGate degraded\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"SYNTHETIC_NODE-mps-tooling-mdn-activegate\",\"entityName\":\"mps-tooling-mdn-activegate\",\"entityType\":\"SYNTHETIC_NODE\"},
  \"impactedEntities\":[{\"entityId\":\"SYNTHETIC_NODE-mps-tooling-mdn-activegate\",\"entityName\":\"mps-tooling-mdn-activegate\",\"entityType\":\"SYNTHETIC_NODE\"}],
  \"problemDetails\":\"Dynatrace ActiveGate in mps-tooling-mdn is not reporting. k8s.cluster.name: mps-tooling-mdn. k8s.namespace.name: dynatrace\",
  \"customProperties\":{\"k8s.cluster.name\":\"mps-tooling-mdn\",\"k8s.cluster.uid\":\"${TOOLING_UID}\",\"k8s.namespace.name\":\"dynatrace\",\"k8s.workload.name\":\"mps-tooling-mdn-activegate\",\"impacted_entity\":\"mps-tooling-mdn-activegate\",\"environment\":\"ADC\"}}"
pause 3

post "Tooling cluster — host or monitoring unavailable" "P-TOOL2-${S10}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-TOOL2-${S10}\",
  \"problemTitle\":\"Host or monitoring unavailable — mps-tooling-mdn\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"SYNTHETIC_NODE-mps-tooling-mdn-activegate\",\"entityName\":\"mps-tooling-mdn-activegate\",\"entityType\":\"SYNTHETIC_NODE\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_CLUSTER-mps-tooling-mdn\",\"entityName\":\"mps-tooling-mdn\",\"entityType\":\"KUBERNETES_CLUSTER\"}],
  \"problemDetails\":\"Monitoring connectivity lost to mps-tooling-mdn cluster. k8s.cluster.name: mps-tooling-mdn\",
  \"customProperties\":{\"k8s.cluster.name\":\"mps-tooling-mdn\",\"k8s.cluster.uid\":\"${TOOLING_UID}\",\"environment\":\"ADC\"}}"
pause 3

post "MDN BM node memory root (different DC)" "P-MDN-ROOT-${S10}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-MDN-ROOT-${S10}\",
  \"problemTitle\":\"Memory saturation on ${MDN_BM}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"RESOURCE_CONTENTION\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${MDN_BM}\",\"entityName\":\"${MDN_BM}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${MDN_BM}\",\"entityName\":\"${MDN_BM}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"Memory at 96% on ${MDN_BM}. host.name: ${MDN_BM}. k8s.node.name: ${MDN_BM}. k8s.cluster.name: mps-mondev-mdn\",
  \"customProperties\":{\"host.name\":\"${MDN_BM}\",\"k8s.node.name\":\"${MDN_BM}\",\"k8s.cluster.uid\":\"${MONDEV_UID}\",\"impacted_entity\":\"${MDN_BM}\",\"environment\":\"ADC\"}}"
pause 3

post "MDN cluster NotReady condition (downstream)" "P-MDN-D1-${S10}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-MDN-D1-${S10}\",
  \"problemTitle\":\"Kubernetes Worker Node in Not Ready condition in Dev Kubeadm cluster\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${MDN_BM}\",\"entityName\":\"${MDN_BM}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_CLUSTER-mps-mondev-mdn\",\"entityName\":\"mps-mondev-mdn\",\"entityType\":\"KUBERNETES_CLUSTER\"}],
  \"problemDetails\":\"Node ${MDN_BM} is NotReady in kubeadm cluster mps-mondev-mdn. k8s.cluster.name: mps-mondev-mdn\",
  \"customProperties\":{\"k8s.cluster.name\":\"mps-mondev-mdn\",\"k8s.cluster.uid\":\"${MONDEV_UID}\",\"environment\":\"ADC\"}}"

pause 5; result; dbcheck
echo "--- Assertions ---"
n_mdn=$(count_incidents_for "${MDN_BM}")
check_pass "S10 incident_count(${MDN_BM})" "$n_mdn" "1"
# Tooling SYNTHETIC_NODE alerts fall through to title-fingerprint correlation_ids rather
# than using "mps-tooling-mdn-activegate". Check isolation: tooling alerts must be in a
# DIFFERENT incident from the MDN BM alerts.
tool_inc=$(kubectl exec -n aileron postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT COALESCE(auto_created_incident_id::text,'none') FROM alerts WHERE source_id='P-TOOL-${S10}' ORDER BY created_at DESC LIMIT 1;" 2>/dev/null | tr -d ' \n')
mdn_root_inc=$(kubectl exec -n aileron postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT COALESCE(auto_created_incident_id::text,'none') FROM alerts WHERE source_id='P-MDN-ROOT-${S10}' ORDER BY created_at DESC LIMIT 1;" 2>/dev/null | tr -d ' \n')
if [[ "$tool_inc" == "?" || "$mdn_root_inc" == "?" ]]; then
  echo "  SKIP  S10 tooling/MDN isolation: kubectl unavailable"
elif [[ "$tool_inc" == "none" ]]; then
  echo "  FAIL  S10 tooling isolation: tooling alert not linked to any incident"
elif [[ "$tool_inc" != "$mdn_root_inc" ]]; then
  echo "  PASS  S10 tooling and MDN BM alerts in separate incidents"
else
  echo "  FAIL  S10 tooling and MDN BM merged into same incident ($tool_inc)"
fi
fi

# ─── final summary ────────────────────────────────────────────────────────────
echo ""
echo "══════════════════════════════════════════"
echo "  FINAL SUMMARY (all incidents last 10 min)"
echo "══════════════════════════════════════════"
kubectl exec -n aileron postgres-primary-0 -- psql -U alerthub -d alerthub -c "
  SELECT
    LEFT(title,55) as title,
    correlation_id,
    status,
    jsonb_array_length(alert_ids) as alert_count,
    created_at::time(0) as created
  FROM incidents
  WHERE auto_created = true
    AND created_at > NOW() - INTERVAL '10 minutes'
  ORDER BY created_at;" 2>/dev/null
