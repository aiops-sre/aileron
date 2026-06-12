#!/usr/bin/env bash
# test_dynatrace_webhook.sh — real-data Dynatrace webhook test suite
#
# Sends mock Dynatrace problem payloads to /api/v1/webhooks/dynatrace.
# Uses actual MPS infra entity names, cluster UIDs, and topology.
#
# Usage:
#   bash scripts/test_dynatrace_webhook.sh              # all scenarios
#   bash scripts/test_dynatrace_webhook.sh 1            # one scenario
#   bash scripts/test_dynatrace_webhook.sh 1 3 5 9      # specific set
#   LOCAL=1 bash scripts/test_dynatrace_webhook.sh      # port-forward (localhost:3000)
#
# Scenarios:
#   1   Smoke               — single HOST CPU problem, verify HTTP 200 + alert_id
#   2   BM → VM cascade     — KVM host OOM → VM CPU throttle → K8s node NotReady
#   3   K8s node cascade    — node NotReady → 4 workloads across 3 namespaces
#   4   Service degradation — DB connection exhaustion → API 503 → frontend 502
#   5   RESOLVED lifecycle  — OPEN then RESOLVED closes linked incident
#   6   Deduplication       — same ProblemID sent 3× → 1 alert record
#   7   Re-open flap        — OPEN → RESOLVED → OPEN on same ProblemID
#   8   Cross-cluster       — nonprod-rno failure and k8preview01 failure isolated
#   9   No rootCauseEntity  — pure label-based scoring fallback path
#  10   MDN DC cascade      — mps-mondev-mdn bare-metal host → cluster condition
#  11   Application latency — SERVICE/APPLICATION latency chain with custom props
#  12   Burst (25 alerts)   — rapid-fire stress to measure ingest throughput

set -uo pipefail

# ─── config ───────────────────────────────────────────────────────────────────
LOCAL=${LOCAL:-0}
if [[ "$LOCAL" == "1" ]]; then
  BASE="http://localhost:3000"
else
  BASE="https://aileron.example.com"
fi
ENDPOINT="${BASE}/api/v1/webhooks/dynatrace"
API_KEY="ah_c76ddeaa86c74818ac84997c0f2a8174eaba3bf0fdab295132acb8aba2238e5f"
NS="aileron"
NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
TS=$(date +%s)

# ─── real MPS infrastructure entities ────────────────────────────────────────
# CloudStack bare-metal KVM hypervisors
BM1="cloudstack-cluster-2-iapps-100-67-61-18"
BM2="cloudstack-cluster-2-iapps-100-67-61-31"
BM3="cloudstack-cluster-2-iapps-100-67-61-45"

# VMs running on BM1
VM1="cloudstack-cluster-2-mps-nonprod-rno-worker-z3-08"
VM2="cloudstack-cluster-2-mps-nonprod-rno-worker-z3-13"

# K8s worker nodes (correspond to VMs above)
K8N_Z3_08="mps-nonprod-rno-worker-z3-08"
K8N_Z3_13="mps-nonprod-rno-worker-z3-13"
K8N_Z1_06="mps-nonprod-rno-worker-z1-06"
K8N_Z2_01="mps-nonprod-rno-worker-z2-01"
K8N_Z2_07="mps-nonprod-rno-worker-z2-07"
K8N_Z2_11="mps-nonprod-rno-worker-z2-11"

# k8preview01-rno cluster node
K8N_PREV="k8preview01-rno-worker-z1-01"

# mps-mondev-mdn bare metal (maiden DC)
MDN_BM="mps-mondev-mdn-worker-z3-05"

# Cluster UIDs
NONPROD_UID="88b12bf2-9f23-4b4a-b06a-3d2b33134a3b"
MONDEV_UID="00a07750-e556-443e-89d9-80341edb472d"
K8PREV_UID="d1be9a2f-4e83-49bf-8800-b9a2bc2e7da0"
TOOLING_UID="0a0430fb-0c23-4e80-a000-caa5570d6c17"

# ─── colour codes ─────────────────────────────────────────────────────────────
BOLD="\033[1m"; DIM="\033[2m"; RST="\033[0m"
RED="\033[31m"; GRN="\033[32m"; YLW="\033[33m"; CYN="\033[36m"

# ─── helpers ──────────────────────────────────────────────────────────────────
post() {
  local label="$1" problem_id="$2" payload="$3"
  printf "  → %-52s " "[${problem_id}]..."
  local out http_code body alert_id
  out=$(curl -s -w "\n%{http_code}" -X POST "$ENDPOINT" \
    -H "Content-Type: application/json" \
    -H "X-API-Key: ${API_KEY}" \
    -d "$payload" 2>&1)
  http_code=$(echo "$out" | tail -1)
  body=$(echo "$out" | head -1)
  alert_id=$(echo "$body" | grep -o '"alert_id":"[^"]*"' | cut -d'"' -f4 2>/dev/null || true)
  if [[ "$http_code" =~ ^(200|201|202)$ ]]; then
    printf "${GRN}HTTP %s${RST}" "$http_code"
    [[ -n "$alert_id" ]] && printf "  id=%.14s…" "$alert_id"
    echo "  ${label}"
  else
    printf "${RED}HTTP %s${RST}  %s\n" "$http_code" "$(echo "$body" | head -c 200)"
  fi
}

post_quiet() {
  curl -s -o /dev/null -X POST "$ENDPOINT" \
    -H "Content-Type: application/json" \
    -H "X-API-Key: ${API_KEY}" \
    -d "$1" &
}

section() {
  echo ""
  echo -e "${BOLD}══════════════════════════════════════════════════════════${RST}"
  echo -e "${BOLD}  $1${RST}"
  echo -e "${BOLD}══════════════════════════════════════════════════════════${RST}"
}

pause() { echo -e "  ${DIM}⏳ ${1}s…${RST}"; sleep "$1"; }

dbcheck() {
  echo ""
  echo -e "  ${CYN}--- DB: auto-created incidents (last 10 min) ---${RST}"
  kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT '  INC ' || LEFT(id::text,8) ||
           '  alerts=' || jsonb_array_length(alert_ids) ||
           '  ' || status ||
           '  corr=' || COALESCE(LEFT(correlation_id,22),'NULL') ||
           '  » ' || LEFT(title,50)
    FROM incidents
    WHERE auto_created=true AND updated_at > NOW()-INTERVAL '10 minutes'
    ORDER BY created_at DESC LIMIT 10;" 2>/dev/null \
  | grep -v "^$" || echo -e "  ${DIM}(kubectl unavailable — skipping DB check)${RST}"
}

logs() {
  echo -e "  ${CYN}--- Backend logs (last 60s, correlation decisions) ---${RST}"
  kubectl logs -n "$NS" -l app=alerthub-backend --since=60s 2>/dev/null \
  | grep -E "RCE alert=|CREATE_ROOT|ATTACH_TO_ROOT|correlation_id.*set|incident.*created|resolved" \
  | head -15 || echo -e "  ${DIM}(kubectl unavailable)${RST}"
}

check() {
  local label="$1" got="$2" want="$3"
  if [[ "$got" == "$want" ]]; then
    echo -e "  ${GRN}✓ PASS${RST}  ${label}: ${got}"
  else
    echo -e "  ${RED}✗ FAIL${RST}  ${label}: want=${want} got=${got}"
  fi
}

incident_count() {
  kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT COUNT(*) FROM incidents
    WHERE auto_created=true AND correlation_id='$1'
      AND status IN ('open','investigating')
      AND updated_at > NOW()-INTERVAL '10 minutes';" 2>/dev/null | tr -d ' \n' || echo "?"
}

alert_count() {
  kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT COUNT(*) FROM alerts
    WHERE source_id='$1' AND updated_at > NOW()-INTERVAL '10 minutes';" 2>/dev/null | tr -d ' \n' || echo "?"
}

# ─── scenario selector ────────────────────────────────────────────────────────
RUN_ALL=true; SELECTED=()
[[ $# -gt 0 ]] && RUN_ALL=false && SELECTED=("$@")
run() { $RUN_ALL && return 0; for s in "${SELECTED[@]}"; do [[ "$s" == "$1" ]] && return 0; done; return 1; }

echo ""
echo -e "${BOLD}AlertHub — Dynatrace Webhook Test Suite${RST}"
echo -e "${DIM}Endpoint : ${ENDPOINT}${RST}"
echo -e "${DIM}API Key  : ${API_KEY:0:24}…${RST}"
echo -e "${DIM}Run TS   : ${TS}${RST}"

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 1 — Smoke: single HOST CPU problem
# Verifies the webhook accepts a well-formed payload and returns 200 with alert_id.
# ══════════════════════════════════════════════════════════════════════════════
if run 1; then
section "Scenario 1 · Smoke — single HOST CPU saturation"
echo -e "  ${DIM}Expected: HTTP 200/201, alert_id in response body${RST}"

post "BM1 CPU critical (smoke)" "P-SMOKE-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-SMOKE-${TS}\",
  \"problemTitle\": \"CPU saturation on KVM hypervisor ${BM1}\",
  \"ProblemDetailsText\": \"CPU utilisation at 99% for 5 consecutive minutes on ${BM1}. 48 active VMs at risk of runqueue starvation. host.name: ${BM1}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"PERFORMANCE\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${BM1}\",
    \"entityName\": \"${BM1}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"HOST-${BM1}\",
    \"entityName\": \"${BM1}\",
    \"entityType\": \"HOST\"
  }],
  \"customProperties\": {
    \"host.name\": \"${BM1}\",
    \"impacted_entity\": \"${BM1}\",
    \"environment\": \"ADC\",
    \"dt.entity.host\": \"HOST-${BM1}\"
  },
  \"ManagementZone\": \"CloudStack-RNO\",
  \"EntityTags\": [\"kvm\", \"cloudstack-cluster-2\", \"rno\", \"smoke-test\"]
}"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 2 — BM → VM → K8s node cascade (full CloudStack stack)
# Root:         BM1 (KVM hypervisor) memory critical
# Downstream 1: VM1 (on BM1) CPU throttled
# Downstream 2: K8s node z3-08 (backed by VM1) goes NotReady
# Expected:     1 incident, correlation_id=BM1, alert_count=3
# ══════════════════════════════════════════════════════════════════════════════
if run 2; then
section "Scenario 2 · BM → VM → K8s node cascade  (root=${BM1})"
echo -e "  ${DIM}Expected: 1 incident, correlation_id=${BM1}, alert_count=3${RST}"
S2="S2-${TS}"

post "BM1 memory at 97% (root)" "P-BM-ROOT-${S2}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-BM-ROOT-${S2}\",
  \"problemTitle\": \"Memory saturation — KVM host ${BM1}\",
  \"ProblemDetailsText\": \"Memory allocation at 97% (255/256 Gi). Hypervisor balloon reclaim active. New VM scheduling blocked. host.name: ${BM1}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"RESOURCE_CONTENTION\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${BM1}\",
    \"entityName\": \"${BM1}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"HOST-${BM1}\",
    \"entityName\": \"${BM1}\",
    \"entityType\": \"HOST\"
  }],
  \"customProperties\": {
    \"host.name\": \"${BM1}\",
    \"impacted_entity\": \"${BM1}\",
    \"environment\": \"ADC\",
    \"dt.entity.host\": \"HOST-${BM1}\"
  },
  \"ManagementZone\": \"CloudStack-RNO\",
  \"EntityTags\": [\"kvm\", \"cloudstack-cluster-2\", \"memory-critical\"]
}"
pause 3

post "VM1 CPU steal 38% (downstream)" "P-VM1-CPU-${S2}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-VM1-CPU-${S2}\",
  \"problemTitle\": \"CPU steal on VM ${VM1}\",
  \"ProblemDetailsText\": \"CPU steal time 38% on ${VM1} due to hypervisor oversubscription on ${BM1}. Runqueue depth 52. host.name: ${VM1}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"PERFORMANCE\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${BM1}\",
    \"entityName\": \"${BM1}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"HOST-${VM1}\",
    \"entityName\": \"${VM1}\",
    \"entityType\": \"HOST\"
  }],
  \"customProperties\": {
    \"host.name\": \"${VM1}\",
    \"impacted_entity\": \"${VM1}\",
    \"environment\": \"ADC\"
  },
  \"EntityTags\": [\"vm\", \"cpu-steal\", \"cloudstack-cluster-2\"]
}"
pause 3

post "K8s node z3-08 NotReady (downstream)" "P-NODE-Z308-${S2}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-NODE-Z308-${S2}\",
  \"problemTitle\": \"Kubernetes node ${K8N_Z3_08} is NotReady\",
  \"ProblemDetailsText\": \"Node ${K8N_Z3_08} has entered NotReady state. kubelet unresponsive for 240s. 23 pods evicting. k8s.node.name: ${K8N_Z3_08}. k8s.cluster.name: mps-nonprod-rno\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${BM1}\",
    \"entityName\": \"${BM1}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_NODE-${K8N_Z3_08}\",
    \"entityName\": \"${K8N_Z3_08}\",
    \"entityType\": \"KUBERNETES_NODE\"
  }],
  \"customProperties\": {
    \"k8s.node.name\": \"${K8N_Z3_08}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"host.name\": \"${VM1}\",
    \"environment\": \"ADC\"
  },
  \"ManagementZone\": \"K8s-mps-nonprod-rno\",
  \"EntityTags\": [\"kubernetes\", \"node\", \"notready\"]
}"

pause 6
dbcheck
echo ""
n=$(incident_count "${BM1}")
check "S2 incident_count(corr=${BM1})" "$n" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 3 — K8s node NotReady → 4 workload cascades (3 namespaces)
# Root:         mps-nonprod-rno-worker-z3-13 memory exhaustion
# Downstream:   ingress-nginx (DaemonSet), coredns (kube-system),
#               dex (dex ns), nginx-mtls-proxy (stagepush-auth-uat ns)
# Expected:     1 incident, alert_count=5
# ══════════════════════════════════════════════════════════════════════════════
if run 3; then
section "Scenario 3 · K8s node → 4 workloads across 3 namespaces  (root=${K8N_Z3_13})"
echo -e "  ${DIM}Expected: 1 incident, correlation_id=${K8N_Z3_13}, alert_count=5${RST}"
S3="S3-${TS}"

post "Node z3-13 memory OOM (root)" "P-NODE-Z313-${S3}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-NODE-Z313-${S3}\",
  \"problemTitle\": \"Memory saturation on Kubernetes node ${K8N_Z3_13}\",
  \"ProblemDetailsText\": \"Node OOMKiller triggered on ${K8N_Z3_13}. Memory at 99%. 18 pods pending eviction. host.name: ${K8N_Z3_13}. k8s.node.name: ${K8N_Z3_13}. k8s.cluster.name: mps-nonprod-rno\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"RESOURCE_CONTENTION\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${K8N_Z3_13}\",
    \"entityName\": \"${K8N_Z3_13}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"HOST-${K8N_Z3_13}\",
    \"entityName\": \"${K8N_Z3_13}\",
    \"entityType\": \"HOST\"
  }],
  \"customProperties\": {
    \"host.name\": \"${K8N_Z3_13}\",
    \"k8s.node.name\": \"${K8N_Z3_13}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"impacted_entity\": \"${K8N_Z3_13}\",
    \"environment\": \"ADC\"
  },
  \"EntityTags\": [\"kubernetes\", \"node\", \"oom\", \"mps-nonprod-rno\"]
}"
pause 3

post "ingress-nginx DaemonSet degraded" "P-INGRESS-${S3}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-INGRESS-${S3}\",
  \"problemTitle\": \"Not all pods ready — ingress-nginx-controller in ingress-nginx on ${K8N_Z3_13}\",
  \"ProblemDetailsText\": \"DaemonSet pod ingress-nginx-controller evicted from ${K8N_Z3_13}. Node removed from LB pool. k8s.node.name: ${K8N_Z3_13}. k8s.namespace.name: ingress-nginx\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${K8N_Z3_13}\",
    \"entityName\": \"${K8N_Z3_13}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-ingress-nginx-controller\",
    \"entityName\": \"ingress-nginx-controller\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.node.name\": \"${K8N_Z3_13}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"ingress-nginx\",
    \"k8s.workload.name\": \"ingress-nginx-controller\",
    \"k8s.workload.kind\": \"daemonset\",
    \"environment\": \"ADC\"
  }
}"
pause 3

post "coredns deployment degraded (kube-system)" "P-COREDNS-${S3}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-COREDNS-${S3}\",
  \"problemTitle\": \"Backoff event — coredns in kube-system on ${K8N_Z3_13}\",
  \"ProblemDetailsText\": \"coredns pod CrashLoopBackOff on ${K8N_Z3_13}. DNS resolution degraded cluster-wide. k8s.namespace.name: kube-system. k8s.cluster.name: mps-nonprod-rno\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${K8N_Z3_13}\",
    \"entityName\": \"${K8N_Z3_13}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-coredns\",
    \"entityName\": \"coredns\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.node.name\": \"${K8N_Z3_13}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"kube-system\",
    \"k8s.workload.name\": \"coredns\",
    \"k8s.workload.kind\": \"deployment\",
    \"environment\": \"ADC\"
  }
}"
pause 3

post "dex pod evicted (dex ns)" "P-DEX-${S3}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-DEX-${S3}\",
  \"problemTitle\": \"Not all pods ready — dex in dex namespace on ${K8N_Z3_13}\",
  \"ProblemDetailsText\": \"dex OIDC provider pod evicted from ${K8N_Z3_13} due to memory pressure. SSO authentication intermittent. k8s.namespace.name: dex\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${K8N_Z3_13}\",
    \"entityName\": \"${K8N_Z3_13}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-dex\",
    \"entityName\": \"dex\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.node.name\": \"${K8N_Z3_13}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"dex\",
    \"k8s.workload.name\": \"dex\",
    \"k8s.workload.kind\": \"deployment\",
    \"environment\": \"ADC\"
  }
}"
pause 3

post "nginx-mtls-proxy-push-proxy OOMKilled (stagepush-auth-uat)" "P-MTLS-${S3}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-MTLS-${S3}\",
  \"problemTitle\": \"Not all pods ready — nginx-mtls-proxy-push-proxy in stagepush-auth-uat\",
  \"ProblemDetailsText\": \"nginx-mtls-proxy-push-proxy OOMKilled on ${K8N_Z3_13}. Push auth proxy unavailable. k8s.namespace.name: stagepush-auth-uat. k8s.cluster.name: mps-nonprod-rno\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${K8N_Z3_13}\",
    \"entityName\": \"${K8N_Z3_13}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-nginx-mtls-proxy-push-proxy\",
    \"entityName\": \"nginx-mtls-proxy-push-proxy\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.node.name\": \"${K8N_Z3_13}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"stagepush-auth-uat\",
    \"k8s.workload.name\": \"nginx-mtls-proxy-push-proxy\",
    \"k8s.workload.kind\": \"deployment\",
    \"environment\": \"ADC\"
  }
}"

pause 6
dbcheck
echo ""
n=$(incident_count "${K8N_Z3_13}")
check "S3 incident_count(corr=${K8N_Z3_13})" "$n" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 4 — Service degradation chain
# PostgreSQL connection exhaustion → alerthub-backend 503 → frontend 502
# All three linked to same infrastructure cause in aileron namespace
# ══════════════════════════════════════════════════════════════════════════════
if run 4; then
section "Scenario 4 · Service degradation chain  (DB → API → Frontend)"
echo -e "  ${DIM}Expected: 1 incident, alert_count=3, correlation through app topology${RST}"
S4="S4-${TS}"

post "PostgreSQL max_connections hit (root)" "P-PG-CONNS-${S4}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-PG-CONNS-${S4}\",
  \"problemTitle\": \"PostgreSQL connection pool exhausted — alerthub-postgres\",
  \"ProblemDetailsText\": \"PostgreSQL has reached max_connections=200. Current=199, wait_queue=87. Query p99 latency 14.2s. Application requests timing out at DB layer.\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"SERVICE-postgres-primary-0\",
    \"entityName\": \"postgres-primary-0\",
    \"entityType\": \"SERVICE\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"SERVICE-postgres-primary-0\",
    \"entityName\": \"postgres-primary-0\",
    \"entityType\": \"SERVICE\"
  }],
  \"customProperties\": {
    \"k8s.cluster.name\": \"example-cluster\",
    \"k8s.namespace.name\": \"aileron\",
    \"k8s.workload.name\": \"postgres-primary\",
    \"k8s.workload.kind\": \"statefulset\",
    \"impacted_entity\": \"postgres-primary-0\",
    \"environment\": \"ADC\",
    \"max_connections\": \"200\",
    \"current_connections\": \"199\",
    \"wait_queue\": \"87\"
  },
  \"EntityTags\": [\"postgres\", \"database\", \"aileron\"]
}"
pause 3

post "alerthub-backend 503 94% error rate (downstream)" "P-BE-503-${S4}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-BE-503-${S4}\",
  \"problemTitle\": \"HTTP 503 Service Unavailable — alerthub-backend error rate 94%\",
  \"ProblemDetailsText\": \"alerthub-backend returning 503 for 94% of requests. Root cause: DB connection exhaustion. 2/2 replicas degraded. Queue depth 1204.\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"SERVICE-postgres-primary-0\",
    \"entityName\": \"postgres-primary-0\",
    \"entityType\": \"SERVICE\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"SERVICE-alerthub-backend\",
    \"entityName\": \"alerthub-backend\",
    \"entityType\": \"SERVICE\"
  }],
  \"customProperties\": {
    \"k8s.cluster.name\": \"example-cluster\",
    \"k8s.namespace.name\": \"aileron\",
    \"k8s.workload.name\": \"alerthub-backend\",
    \"k8s.workload.kind\": \"deployment\",
    \"impacted_entity\": \"alerthub-backend\",
    \"environment\": \"ADC\",
    \"error_rate_pct\": \"94\",
    \"ready_pods\": \"0\",
    \"total_pods\": \"2\"
  },
  \"EntityTags\": [\"backend\", \"http-503\", \"aileron\"]
}"
pause 3

post "Frontend auth 502 (downstream)" "P-FE-502-${S4}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-FE-502-${S4}\",
  \"problemTitle\": \"Response time degradation — alerthub-frontend (502 auth failures)\",
  \"ProblemDetailsText\": \"alerthub-frontend returning 502 for all auth-required pages. JWT validation calls to backend timing out after 30s. ~240 users affected.\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"SERVICE-postgres-primary-0\",
    \"entityName\": \"postgres-primary-0\",
    \"entityType\": \"SERVICE\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"APPLICATION-alerthub-frontend\",
    \"entityName\": \"alerthub-frontend\",
    \"entityType\": \"APPLICATION\"
  }],
  \"customProperties\": {
    \"k8s.cluster.name\": \"example-cluster\",
    \"k8s.namespace.name\": \"aileron\",
    \"k8s.workload.name\": \"frontend\",
    \"k8s.workload.kind\": \"deployment\",
    \"impacted_entity\": \"alerthub-frontend\",
    \"environment\": \"ADC\",
    \"http_status\": \"502\",
    \"auth_timeout_ms\": \"30000\",
    \"affected_users\": \"240\"
  },
  \"EntityTags\": [\"frontend\", \"auth\", \"502\", \"aileron\"]
}"

pause 6
dbcheck
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 5 — OPEN → RESOLVED lifecycle
# Verifies alert status transitions and incident auto-close/resolve.
# ══════════════════════════════════════════════════════════════════════════════
if run 5; then
section "Scenario 5 · OPEN → RESOLVED lifecycle (disk pressure on ${K8N_Z2_01})"
echo -e "  ${DIM}Expected: incident opens on OPEN, auto-resolves after RESOLVED${RST}"
S5="S5-${TS}"

post "OPEN — disk pressure on z2-01" "P-DISK-${S5}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-DISK-${S5}\",
  \"problemTitle\": \"Disk pressure on Kubernetes node ${K8N_Z2_01}\",
  \"ProblemDetailsText\": \"Disk usage at 87% on ${K8N_Z2_01}. ImageFS: 91%. Pod eviction threshold breached. k8s.node.name: ${K8N_Z2_01}. k8s.cluster.name: mps-nonprod-rno\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"RESOURCE_CONTENTION\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${K8N_Z2_01}\",
    \"entityName\": \"${K8N_Z2_01}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"HOST-${K8N_Z2_01}\",
    \"entityName\": \"${K8N_Z2_01}\",
    \"entityType\": \"HOST\"
  }],
  \"customProperties\": {
    \"host.name\": \"${K8N_Z2_01}\",
    \"k8s.node.name\": \"${K8N_Z2_01}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"impacted_entity\": \"${K8N_Z2_01}\",
    \"environment\": \"ADC\"
  },
  \"EntityTags\": [\"disk\", \"node\", \"pressure\"]
}"

echo -e "  ${DIM}⏳ 8s before sending RESOLVED…${RST}"
pause 8

echo ""
echo -e "  ${CYN}--- Incident before RESOLVED ---${RST}"
kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT '  id=' || LEFT(id::text,8) || '  status=' || status || '  title=' || LEFT(title,60)
  FROM incidents WHERE auto_created=true AND created_at>NOW()-INTERVAL '3 minutes'
  ORDER BY created_at DESC LIMIT 1;" 2>/dev/null | grep -v "^$" \
  || echo -e "  ${DIM}(kubectl unavailable)${RST}"

post "RESOLVED — disk pressure cleared" "P-DISK-${S5}" "{
  \"state\": \"RESOLVED\",
  \"problemId\": \"P-DISK-${S5}\",
  \"problemTitle\": \"Disk pressure on Kubernetes node ${K8N_Z2_01}\",
  \"ProblemDetailsText\": \"Disk pressure on ${K8N_Z2_01} cleared after image GC. Usage now at 61%.\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"RESOURCE_CONTENTION\",
  \"status\": \"RESOLVED\",
  \"startTime\": \"${NOW}\",
  \"endTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${K8N_Z2_01}\",
    \"entityName\": \"${K8N_Z2_01}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"HOST-${K8N_Z2_01}\",
    \"entityName\": \"${K8N_Z2_01}\",
    \"entityType\": \"HOST\"
  }],
  \"customProperties\": {
    \"host.name\": \"${K8N_Z2_01}\",
    \"k8s.node.name\": \"${K8N_Z2_01}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"environment\": \"ADC\"
  }
}"

pause 5
echo ""
echo -e "  ${CYN}--- Incident after RESOLVED (should be resolved/closed) ---${RST}"
kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT '  id=' || LEFT(id::text,8) || '  status=' || status ||
         '  resolved=' || COALESCE(resolved_at::text,'NULL') || '  title=' || LEFT(title,55)
  FROM incidents WHERE auto_created=true AND created_at>NOW()-INTERVAL '5 minutes'
  ORDER BY created_at DESC LIMIT 3;" 2>/dev/null | grep -v "^$" \
  || echo -e "  ${DIM}(kubectl unavailable)${RST}"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 6 — Deduplication: same ProblemID sent 3 times
# Expected: 1 alert record in DB (subsequent sends update, not create)
# ══════════════════════════════════════════════════════════════════════════════
if run 6; then
section "Scenario 6 · Deduplication — same ProblemID sent 3×"
echo -e "  ${DIM}Expected: HTTP 200 all 3 times, but only 1 alert row in DB${RST}"
DEDUP_ID="P-DEDUP-${TS}"
DEDUP_PAYLOAD="{
  \"state\": \"OPEN\",
  \"problemId\": \"${DEDUP_ID}\",
  \"problemTitle\": \"CPU-request saturation on node mps-nonprod-rno-worker-z2-04\",
  \"ProblemDetailsText\": \"CPU requests exceed 90% on mps-nonprod-rno-worker-z2-04. host.name: mps-nonprod-rno-worker-z2-04\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"PERFORMANCE\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-mps-nonprod-rno-worker-z2-04\",
    \"entityName\": \"mps-nonprod-rno-worker-z2-04\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"HOST-mps-nonprod-rno-worker-z2-04\",
    \"entityName\": \"mps-nonprod-rno-worker-z2-04\",
    \"entityType\": \"HOST\"
  }],
  \"customProperties\": {
    \"host.name\": \"mps-nonprod-rno-worker-z2-04\",
    \"k8s.node.name\": \"mps-nonprod-rno-worker-z2-04\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"environment\": \"ADC\"
  }
}"

post "Send #1 (first occurrence)" "${DEDUP_ID}" "$DEDUP_PAYLOAD"
pause 1
post "Send #2 (duplicate)" "${DEDUP_ID}" "$DEDUP_PAYLOAD"
pause 1
post "Send #3 (duplicate again)" "${DEDUP_ID}" "$DEDUP_PAYLOAD"

pause 5
echo ""
echo -e "  ${CYN}--- DB: should be exactly 1 row ---${RST}"
kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT '  count=' || COUNT(*) || '  last_updated=' || MAX(updated_at)::time(0)
  FROM alerts WHERE source_id='${DEDUP_ID}';" 2>/dev/null | grep -v "^$" \
  || echo -e "  ${DIM}(kubectl unavailable)${RST}"
n=$(alert_count "${DEDUP_ID}")
check "S6 alert record count for ${DEDUP_ID}" "$n" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 7 — Re-open / flapping: OPEN → RESOLVED → OPEN same ProblemID
# Tests that the system handles state transitions correctly.
# ══════════════════════════════════════════════════════════════════════════════
if run 7; then
section "Scenario 7 · Flapping alert — OPEN → RESOLVED → OPEN"
echo -e "  ${DIM}Node: ${K8N_Z2_07} | Expected: incident re-opened or new incident created${RST}"
S7="S7-${TS}"

post "OPEN — network unreachable on z2-07" "P-FLAP-${S7}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-FLAP-${S7}\",
  \"problemTitle\": \"Network problem on Kubernetes node ${K8N_Z2_07}\",
  \"ProblemDetailsText\": \"Network unreachable on ${K8N_Z2_07}. Packet loss 100% on bond0. k8s.node.name: ${K8N_Z2_07}. k8s.cluster.name: mps-nonprod-rno\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${K8N_Z2_07}\",
    \"entityName\": \"${K8N_Z2_07}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"HOST-${K8N_Z2_07}\",
    \"entityName\": \"${K8N_Z2_07}\",
    \"entityType\": \"HOST\"
  }],
  \"customProperties\": {
    \"host.name\": \"${K8N_Z2_07}\",
    \"k8s.node.name\": \"${K8N_Z2_07}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"impacted_entity\": \"${K8N_Z2_07}\",
    \"environment\": \"ADC\"
  }
}"
pause 6

post "RESOLVED — network recovered" "P-FLAP-${S7}" "{
  \"state\": \"RESOLVED\",
  \"problemId\": \"P-FLAP-${S7}\",
  \"problemTitle\": \"Network problem on Kubernetes node ${K8N_Z2_07}\",
  \"ProblemDetailsText\": \"Network recovered on ${K8N_Z2_07}. Bond interface re-established.\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"RESOLVED\",
  \"startTime\": \"${NOW}\",
  \"endTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${K8N_Z2_07}\",
    \"entityName\": \"${K8N_Z2_07}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"HOST-${K8N_Z2_07}\",
    \"entityName\": \"${K8N_Z2_07}\",
    \"entityType\": \"HOST\"
  }],
  \"customProperties\": {
    \"host.name\": \"${K8N_Z2_07}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"environment\": \"ADC\"
  }
}"
pause 4

post "OPEN again — network flap recurred" "P-FLAP-${S7}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-FLAP-${S7}\",
  \"problemTitle\": \"Network problem on Kubernetes node ${K8N_Z2_07} (recurrence)\",
  \"ProblemDetailsText\": \"Network problem recurred on ${K8N_Z2_07}. Intermittent packet loss — bond0 toggling. k8s.node.name: ${K8N_Z2_07}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${K8N_Z2_07}\",
    \"entityName\": \"${K8N_Z2_07}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"HOST-${K8N_Z2_07}\",
    \"entityName\": \"${K8N_Z2_07}\",
    \"entityType\": \"HOST\"
  }],
  \"customProperties\": {
    \"host.name\": \"${K8N_Z2_07}\",
    \"k8s.node.name\": \"${K8N_Z2_07}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"impacted_entity\": \"${K8N_Z2_07}\",
    \"environment\": \"ADC\"
  }
}"

pause 5
echo ""
echo -e "  ${CYN}--- Incidents for P-FLAP-${S7} ---${RST}"
kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT '  INC ' || LEFT(i.id::text,8) || '  ' || i.status || '  ' || i.title
  FROM incidents i JOIN alerts a ON a.incident_id = i.id
  WHERE a.source_id='P-FLAP-${S7}'
  ORDER BY i.created_at;" 2>/dev/null | grep -v "^$" \
  || echo -e "  ${DIM}(kubectl unavailable)${RST}"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 8 — Cross-cluster isolation
# Two simultaneous node failures on different clusters must NOT be merged.
# Cluster A: mps-nonprod-rno  (worker z1-06)
# Cluster B: k8preview01-rno  (worker z1-01, different UID)
# Expected: 2 separate incidents
# ══════════════════════════════════════════════════════════════════════════════
if run 8; then
section "Scenario 8 · Cross-cluster isolation — nonprod-rno vs k8preview01-rno"
echo -e "  ${DIM}Expected: 2 separate incidents (different cluster UIDs)${RST}"
S8="S8-${TS}"

post "nonprod-rno z1-06 memory saturation" "P-A8-${S8}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-A8-${S8}\",
  \"problemTitle\": \"Memory saturation on node ${K8N_Z1_06}\",
  \"ProblemDetailsText\": \"Memory at 93% on ${K8N_Z1_06}. OOMKiller active. host.name: ${K8N_Z1_06}. k8s.node.name: ${K8N_Z1_06}. k8s.cluster.name: mps-nonprod-rno\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"RESOURCE_CONTENTION\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${K8N_Z1_06}\",
    \"entityName\": \"${K8N_Z1_06}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"HOST-${K8N_Z1_06}\",
    \"entityName\": \"${K8N_Z1_06}\",
    \"entityType\": \"HOST\"
  }],
  \"customProperties\": {
    \"host.name\": \"${K8N_Z1_06}\",
    \"k8s.node.name\": \"${K8N_Z1_06}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"impacted_entity\": \"${K8N_Z1_06}\",
    \"environment\": \"ADC\"
  }
}"
pause 2

post "nonprod-rno — nginx-mtls-proxy-stage-proxy OOMKilled" "P-A8b-${S8}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-A8b-${S8}\",
  \"problemTitle\": \"Not all pods ready — nginx-mtls-proxy-stage-proxy in stagepush-auth-uat\",
  \"ProblemDetailsText\": \"nginx-mtls-proxy-stage-proxy OOMKilled on ${K8N_Z1_06}. k8s.namespace.name: stagepush-auth-uat. k8s.cluster.name: mps-nonprod-rno\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${K8N_Z1_06}\",
    \"entityName\": \"${K8N_Z1_06}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-nginx-mtls-proxy-stage-proxy\",
    \"entityName\": \"nginx-mtls-proxy-stage-proxy\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.node.name\": \"${K8N_Z1_06}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"stagepush-auth-uat\",
    \"k8s.workload.name\": \"nginx-mtls-proxy-stage-proxy\",
    \"environment\": \"ADC\"
  }
}"
pause 2

post "k8preview01-rno — aqua-scan CrashLoopBackOff (different cluster)" "P-B8-${S8}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-B8-${S8}\",
  \"problemTitle\": \"Backoff event — aqua-scan deployment in aqua-scan namespace\",
  \"ProblemDetailsText\": \"aqua-scan pods in CrashLoopBackOff. k8s.namespace.name: aqua-scan. k8s.cluster.name: k8preview01-rno\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"ERROR\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"KUBERNETES_CLUSTER-k8preview01-rno\",
    \"entityName\": \"k8preview01-rno\",
    \"entityType\": \"KUBERNETES_CLUSTER\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-aqua-scan\",
    \"entityName\": \"aqua-scan\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.cluster.name\": \"k8preview01-rno\",
    \"k8s.cluster.uid\": \"${K8PREV_UID}\",
    \"k8s.namespace.name\": \"aqua-scan\",
    \"k8s.workload.name\": \"aqua-scan\",
    \"k8s.workload.kind\": \"deployment\",
    \"impacted_entity\": \"k8preview01-rno\",
    \"environment\": \"ADC\"
  }
}"
pause 2

post "k8preview01-rno — dm2-api CrashLoopBackOff" "P-B8b-${S8}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-B8b-${S8}\",
  \"problemTitle\": \"Backoff event — dm2-api deployment in dm2api-dev\",
  \"ProblemDetailsText\": \"dm2-api pods in CrashLoopBackOff. k8s.namespace.name: dm2api-dev. k8s.cluster.name: k8preview01-rno\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"ERROR\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"KUBERNETES_CLUSTER-k8preview01-rno\",
    \"entityName\": \"k8preview01-rno\",
    \"entityType\": \"KUBERNETES_CLUSTER\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-dm2-api\",
    \"entityName\": \"dm2-api\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.cluster.name\": \"k8preview01-rno\",
    \"k8s.cluster.uid\": \"${K8PREV_UID}\",
    \"k8s.namespace.name\": \"dm2api-dev\",
    \"k8s.workload.name\": \"dm2-api\",
    \"k8s.workload.kind\": \"deployment\",
    \"environment\": \"ADC\"
  }
}"

pause 6
dbcheck
echo ""
na=$(incident_count "${K8N_Z1_06}")
nb=$(incident_count "k8preview01-rno")
check "S8 incident_count(nonprod-rno corr=${K8N_Z1_06})" "$na" "1"
check "S8 incident_count(k8preview01-rno)" "$nb" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 9 — No rootCauseEntity: pure label/scoring fallback
# Two workloads in the same namespace/cluster, no rootCauseEntity field.
# Expected: both processed via scoring path, ≤1 incident.
# ══════════════════════════════════════════════════════════════════════════════
if run 9; then
section "Scenario 9 · No rootCauseEntity — scoring/label fallback"
echo -e "  ${DIM}Expected: alerts processed, no crash, ≥1 incident via scoring path${RST}"
S9="S9-${TS}"

post "Workload alert (no rootCauseEntity) — push-proxy" "P-NOROOT1-${S9}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-NOROOT1-${S9}\",
  \"problemTitle\": \"Not all pods ready — nginx-mtls-proxy-push-proxy in stagepush-auth-uat\",
  \"ProblemDetailsText\": \"nginx-mtls-proxy-push-proxy pods not ready. k8s.namespace.name: stagepush-auth-uat. k8s.cluster.name: mps-nonprod-rno\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-nginx-mtls-proxy-push-proxy\",
    \"entityName\": \"nginx-mtls-proxy-push-proxy\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"stagepush-auth-uat\",
    \"k8s.workload.name\": \"nginx-mtls-proxy-push-proxy\",
    \"k8s.workload.kind\": \"deployment\",
    \"environment\": \"ADC\"
  },
  \"EntityTags\": [\"no-root-cause-entity\", \"scoring-path\"]
}"
pause 3

post "Workload alert (no rootCauseEntity) — stage-proxy same ns" "P-NOROOT2-${S9}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-NOROOT2-${S9}\",
  \"problemTitle\": \"Not all pods ready — nginx-mtls-proxy-stage-proxy in stagepush-auth-uat\",
  \"ProblemDetailsText\": \"nginx-mtls-proxy-stage-proxy pods not ready. k8s.namespace.name: stagepush-auth-uat. k8s.cluster.name: mps-nonprod-rno\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-nginx-mtls-proxy-stage-proxy\",
    \"entityName\": \"nginx-mtls-proxy-stage-proxy\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"stagepush-auth-uat\",
    \"k8s.workload.name\": \"nginx-mtls-proxy-stage-proxy\",
    \"k8s.workload.kind\": \"deployment\",
    \"environment\": \"ADC\"
  },
  \"EntityTags\": [\"no-root-cause-entity\", \"scoring-path\"]
}"

pause 6
dbcheck
s9_inc=$(kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT COUNT(DISTINCT incident_id) FROM alerts
  WHERE source_id IN ('P-NOROOT1-${S9}','P-NOROOT2-${S9}') AND incident_id IS NOT NULL;" 2>/dev/null | tr -d ' \n' || echo "?")
check "S9 alerts processed into ≥1 incident" \
  "$([[ ${s9_inc:-0} -ge 1 ]] && echo yes || echo no)" "yes"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 10 — MDN (maiden) DC: bare-metal node → cluster condition
# Different DC from RNO — must NOT be merged with any rno incidents.
# Root:        mps-mondev-mdn-worker-z3-05 (kubeadm cluster)
# Downstream:  mps-mondev-mdn cluster NotReady condition
# Expected:    1 incident, DC=maiden, isolated from rno incidents
# ══════════════════════════════════════════════════════════════════════════════
if run 10; then
section "Scenario 10 · MDN bare-metal cascade  (${MDN_BM})"
echo -e "  ${DIM}Expected: 1 incident in maiden DC, isolated from any rno incidents${RST}"
S10="S10-${TS}"

post "MDN BM memory saturation (root)" "P-MDN-ROOT-${S10}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-MDN-ROOT-${S10}\",
  \"problemTitle\": \"Memory saturation on ${MDN_BM}\",
  \"ProblemDetailsText\": \"Memory at 96% on ${MDN_BM}. OOMKiller triggered. Node backing kubeadm cluster mps-mondev-mdn. host.name: ${MDN_BM}. k8s.node.name: ${MDN_BM}. k8s.cluster.name: mps-mondev-mdn\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"RESOURCE_CONTENTION\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${MDN_BM}\",
    \"entityName\": \"${MDN_BM}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"HOST-${MDN_BM}\",
    \"entityName\": \"${MDN_BM}\",
    \"entityType\": \"HOST\"
  }],
  \"customProperties\": {
    \"host.name\": \"${MDN_BM}\",
    \"k8s.node.name\": \"${MDN_BM}\",
    \"k8s.cluster.name\": \"mps-mondev-mdn\",
    \"k8s.cluster.uid\": \"${MONDEV_UID}\",
    \"impacted_entity\": \"${MDN_BM}\",
    \"environment\": \"ADC\"
  },
  \"ManagementZone\": \"Kubeadm-MDN\",
  \"EntityTags\": [\"kubeadm\", \"mdn\", \"bare-metal\"]
}"
pause 3

post "MDN cluster NotReady condition (downstream)" "P-MDN-CLUSTER-${S10}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-MDN-CLUSTER-${S10}\",
  \"problemTitle\": \"Kubernetes Worker Node in Not Ready condition — mps-mondev-mdn\",
  \"ProblemDetailsText\": \"Node ${MDN_BM} NotReady in kubeadm cluster mps-mondev-mdn. k8s.cluster.name: mps-mondev-mdn\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${MDN_BM}\",
    \"entityName\": \"${MDN_BM}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_CLUSTER-mps-mondev-mdn\",
    \"entityName\": \"mps-mondev-mdn\",
    \"entityType\": \"KUBERNETES_CLUSTER\"
  }],
  \"customProperties\": {
    \"k8s.cluster.name\": \"mps-mondev-mdn\",
    \"k8s.cluster.uid\": \"${MONDEV_UID}\",
    \"environment\": \"ADC\"
  }
}"
pause 3

post "MDN — mondev-agent DaemonSet degraded (downstream)" "P-MDN-DS-${S10}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-MDN-DS-${S10}\",
  \"problemTitle\": \"Not all pods ready — mondev-agent in monitoring on ${MDN_BM}\",
  \"ProblemDetailsText\": \"mondev-agent DaemonSet pod evicted from ${MDN_BM} due to memory pressure. k8s.namespace.name: monitoring. k8s.cluster.name: mps-mondev-mdn\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${MDN_BM}\",
    \"entityName\": \"${MDN_BM}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-mondev-agent\",
    \"entityName\": \"mondev-agent\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.node.name\": \"${MDN_BM}\",
    \"k8s.cluster.name\": \"mps-mondev-mdn\",
    \"k8s.cluster.uid\": \"${MONDEV_UID}\",
    \"k8s.namespace.name\": \"monitoring\",
    \"k8s.workload.name\": \"mondev-agent\",
    \"k8s.workload.kind\": \"daemonset\",
    \"environment\": \"ADC\"
  }
}"

pause 6
dbcheck
n=$(incident_count "${MDN_BM}")
check "S10 incident_count(corr=${MDN_BM})" "$n" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 11 — Application response time degradation (SERVICE/APPLICATION types)
# Uses APPLICATION and SERVICE entityTypes — richer than HOST/K8s scenarios.
# Simulates Dynatrace application performance monitoring (APM) alerts.
# ══════════════════════════════════════════════════════════════════════════════
if run 11; then
section "Scenario 11 · Application latency chain  (APM-style alerts)"
echo -e "  ${DIM}SERVICE → APPLICATION cascade, full Dynatrace APM payload format${RST}"
S11="S11-${TS}"

post "Response time degradation — alerthub API (root)" "P-APM-RT-${S11}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-APM-RT-${S11}\",
  \"problemTitle\": \"Response time degradation — AlertHub API /api/v1/incidents\",
  \"ProblemDetailsText\": \"p99 response time for /api/v1/incidents endpoint increased from 45ms to 8200ms. Baseline breach factor: 182x. Failure rate: 31%.\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"PERFORMANCE\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"SERVICE-alerthub-api\",
    \"entityName\": \"AlertHub API\",
    \"entityType\": \"SERVICE\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"SERVICE-alerthub-api\",
    \"entityName\": \"AlertHub API\",
    \"entityType\": \"SERVICE\"
  }],
  \"customProperties\": {
    \"k8s.cluster.name\": \"example-cluster\",
    \"k8s.namespace.name\": \"aileron\",
    \"k8s.workload.name\": \"alerthub-backend\",
    \"impacted_entity\": \"alerthub-api\",
    \"environment\": \"ADC\",
    \"endpoint\": \"/api/v1/incidents\",
    \"p99_ms\": \"8200\",
    \"baseline_ms\": \"45\",
    \"failure_rate_pct\": \"31\"
  },
  \"ManagementZone\": \"AlertHub-Prod\",
  \"EntityTags\": [\"apm\", \"response-time\", \"api\", \"aileron\"]
}"
pause 3

post "Failure rate spike — alerthub-frontend (downstream)" "P-APM-FR-${S11}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-APM-FR-${S11}\",
  \"problemTitle\": \"Failure rate increase — alerthub-frontend\",
  \"ProblemDetailsText\": \"Frontend JavaScript error rate increased 18x. Uncaught errors: 440/min. Users experiencing blank incident lists and auth redirect loops.\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"ERROR\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"SERVICE-alerthub-api\",
    \"entityName\": \"AlertHub API\",
    \"entityType\": \"SERVICE\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"APPLICATION-alerthub-frontend\",
    \"entityName\": \"alerthub-frontend\",
    \"entityType\": \"APPLICATION\"
  }],
  \"customProperties\": {
    \"k8s.cluster.name\": \"example-cluster\",
    \"k8s.namespace.name\": \"aileron\",
    \"k8s.workload.name\": \"frontend\",
    \"impacted_entity\": \"alerthub-frontend\",
    \"environment\": \"ADC\",
    \"error_rate_per_min\": \"440\",
    \"baseline_per_min\": \"24\",
    \"increase_factor\": \"18\"
  },
  \"EntityTags\": [\"apm\", \"frontend\", \"failure-rate\"]
}"
pause 3

post "DB slow queries — postgres response time (underlying cause)" "P-APM-DB-${S11}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-APM-DB-${S11}\",
  \"problemTitle\": \"Response time degradation — PostgreSQL alerthub (slow queries)\",
  \"ProblemDetailsText\": \"PostgreSQL query execution time for incidents table elevated. Seq scans on alerts (12M rows). Missing index on correlation_id column. p99 query: 6.8s.\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"PERFORMANCE\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"SERVICE-postgres-primary-0\",
    \"entityName\": \"postgres-primary-0\",
    \"entityType\": \"SERVICE\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"SERVICE-postgres-primary-0\",
    \"entityName\": \"postgres-primary-0\",
    \"entityType\": \"SERVICE\"
  }],
  \"customProperties\": {
    \"k8s.cluster.name\": \"example-cluster\",
    \"k8s.namespace.name\": \"aileron\",
    \"k8s.workload.name\": \"postgres-primary\",
    \"impacted_entity\": \"postgres-primary-0\",
    \"environment\": \"ADC\",
    \"p99_query_ms\": \"6800\",
    \"table\": \"alerts\",
    \"row_count\": \"12000000\",
    \"scan_type\": \"sequential\"
  },
  \"EntityTags\": [\"postgres\", \"slow-query\", \"apm\"]
}"

pause 6
dbcheck
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 12 — Burst: 25 rapid-fire alerts across multiple hosts/clusters
# Measures webhook ingest throughput. Uses post_quiet (fire-and-forget).
# ══════════════════════════════════════════════════════════════════════════════
if run 12; then
section "Scenario 12 · Burst — 25 alerts rapid-fire"
echo -e "  ${DIM}Expected: all 25 ingested, measure elapsed time + alerts/s${RST}"
S12="S12-${TS}"
HOSTS=("${BM1}" "${BM2}" "${BM3}" "${K8N_Z3_08}" "${K8N_Z3_13}" "${K8N_Z1_06}" "${K8N_Z2_01}" "${MDN_BM}")
SEVS=("PERFORMANCE" "AVAILABILITY" "ERROR" "RESOURCE_CONTENTION" "AVAILABILITY" "PERFORMANCE" "ERROR" "RESOURCE_CONTENTION")
TYPES=("HOST" "HOST" "HOST" "KUBERNETES_NODE" "KUBERNETES_NODE" "HOST" "HOST" "HOST")
CLUSTERS=("mps-nonprod-rno" "mps-nonprod-rno" "mps-nonprod-rno" "mps-nonprod-rno" "mps-nonprod-rno" "mps-nonprod-rno" "mps-nonprod-rno" "mps-mondev-mdn")
UIDS=("${NONPROD_UID}" "${NONPROD_UID}" "${NONPROD_UID}" "${NONPROD_UID}" "${NONPROD_UID}" "${NONPROD_UID}" "${NONPROD_UID}" "${MONDEV_UID}")

t_start=$(date +%s%N 2>/dev/null || date +%s)
for i in $(seq 1 25); do
  idx=$(( (i-1) % ${#HOSTS[@]} ))
  host="${HOSTS[$idx]}"
  sev="${SEVS[$idx]}"
  etype="${TYPES[$idx]}"
  cluster="${CLUSTERS[$idx]}"
  uid="${UIDS[$idx]}"
  post_quiet "{
    \"state\": \"OPEN\",
    \"problemId\": \"P-BURST-${S12}-${i}\",
    \"problemTitle\": \"Burst alert #${i}: ${sev} on ${host}\",
    \"ProblemDetailsText\": \"Synthetic burst test alert ${i}/25 for host ${host}. host.name: ${host}\",
    \"impactLevel\": \"INFRASTRUCTURE\",
    \"severity\": \"${sev}\",
    \"status\": \"OPEN\",
    \"startTime\": \"${NOW}\",
    \"rootCauseEntity\": {\"entityId\": \"HOST-${host}\", \"entityName\": \"${host}\", \"entityType\": \"${etype}\"},
    \"impactedEntities\": [{\"entityId\": \"HOST-${host}\", \"entityName\": \"${host}\", \"entityType\": \"${etype}\"}],
    \"customProperties\": {
      \"host.name\": \"${host}\",
      \"k8s.cluster.name\": \"${cluster}\",
      \"k8s.cluster.uid\": \"${uid}\",
      \"environment\": \"ADC\",
      \"burst_index\": \"${i}\",
      \"burst_batch\": \"${S12}\"
    },
    \"EntityTags\": [\"burst\", \"batch-${S12}\"]
  }"
done
wait
t_end=$(date +%s%N 2>/dev/null || date +%s)
# compute elapsed in ms if nanosecond precision available, else seconds
if [[ "$t_start" =~ [0-9]{12,} ]]; then
  elapsed_ms=$(( (t_end - t_start) / 1000000 ))
  rps=$(( 25 * 1000 / (elapsed_ms + 1) ))
  echo ""
  echo -e "  ${GRN}Fired 25 alerts in ${elapsed_ms}ms  (~${rps} req/s)${RST}"
else
  elapsed_s=$(( t_end - t_start ))
  echo ""
  echo -e "  ${GRN}Fired 25 alerts in ~${elapsed_s}s${RST}"
fi

pause 4
echo ""
echo -e "  ${CYN}--- DB: ingested count for burst batch ${S12} ---${RST}"
kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT '  ingested=' || COUNT(*) || '  expected=25'
  FROM alerts
  WHERE labels->>'burst_batch' = '${S12}'
     OR source_id LIKE 'P-BURST-${S12}-%'
     AND created_at > NOW()-INTERVAL '5 minutes';" 2>/dev/null | grep -v "^$" \
  || echo -e "  ${DIM}(kubectl unavailable)${RST}"
fi

# ─── final summary ────────────────────────────────────────────────────────────
echo ""
echo -e "${BOLD}══════════════════════════════════════════════════════════${RST}"
echo -e "${BOLD}  Run complete — incidents last 15 minutes${RST}"
echo -e "${BOLD}══════════════════════════════════════════════════════════${RST}"
kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -c "
  SELECT
    LEFT(title,50)         AS title,
    status,
    jsonb_array_length(alert_ids) AS alerts,
    LEFT(COALESCE(correlation_id,''),28) AS corr_id,
    created_at::time(0)    AS created
  FROM incidents
  WHERE auto_created = true
    AND created_at > NOW() - INTERVAL '15 minutes'
  ORDER BY created_at DESC;" 2>/dev/null \
|| echo -e "  ${DIM}View incidents at: ${BASE}/incidents${RST}"
echo ""
