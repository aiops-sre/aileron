#!/usr/bin/env bash
# ╔══════════════════════════════════════════════════════════════════════════════╗
# ║  mock_alerts.sh — AlertHub Dynatrace-webhook test suite                     ║
# ║  POST https://aileron.example.com/api/v1/webhooks/dynatrace      ║
# ╚══════════════════════════════════════════════════════════════════════════════╝
#
# Usage:
#   bash scripts/mock_alerts.sh                 # run all 10 scenarios
#   bash scripts/mock_alerts.sh 1               # single scenario
#   bash scripts/mock_alerts.sh 1 3 5           # pick scenarios
#   BURST=100 bash scripts/mock_alerts.sh 9     # override burst count
#
# Scenarios:
#   1  Smoke test           — 1 critical alert, verify 200 + alert_id
#   2  BM → VM cascade      — KVM host root, 2 VM downstreams → 1 incident
#   3  K8s node cascade     — node → pod×3 across namespaces (mps-nonprod-rno)
#   4  Memory storm         — 3 KVM hosts simultaneously → 3 separate incidents
#   5  Network partition    — rno↔iad BGP loss → Redis lag → Postgres WAL stall
#   6  Service degradation  — DB exhaustion → backend 503 → frontend 502 chain
#   7  Dedup test           — same problemId sent twice → 1 alert, count=2
#   8  Auto-resolve         — OPEN then RESOLVED → incident auto-closes
#   9  Burst stress         — BURST alerts fire-and-forget, measure throughput
#  10  Cross-region isolation — rno alert and maiden alert → 2 separate incidents

set -uo pipefail

# ─── config ───────────────────────────────────────────────────────────────────
ENDPOINT="https://aileron.example.com/api/v1/webhooks/dynatrace"
API_KEY="ah_c76ddeaa86c74818ac84997c0f2a8174eaba3bf0fdab295132acb8aba2238e5f"
NS=aileron
BURST=${BURST:-50}
NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
TS=$(date +%s)

# ─── real entity constants (MPS production infra) ─────────────────────────────
BM1="cloudstack-cluster-2-iapps-100-67-61-18"
BM2="cloudstack-cluster-2-iapps-100-67-61-31"
BM3="cloudstack-cluster-2-iapps-100-67-61-45"

VM1="cloudstack-cluster-2-mps-nonprod-rno-worker-z3-08"
VM2="cloudstack-cluster-2-mps-nonprod-rno-worker-z3-13"

K8N1="mps-nonprod-rno-worker-z3-08"
K8N2="mps-nonprod-rno-worker-z3-13"
K8N3="mps-nonprod-rno-worker-z1-06"
K8N4="mps-nonprod-rno-worker-z2-01"

NONPROD_UID="88b12bf2-9f23-4b4a-b06a-3d2b33134a3b"
MONDEV_UID="00a07750-e556-443e-89d9-80341edb472d"

MDN_BM="mps-mondev-mdn-worker-z3-05"

# ─── color codes ──────────────────────────────────────────────────────────────
BOLD="\033[1m"; DIM="\033[2m"; RST="\033[0m"
RED="\033[31m"; GRN="\033[32m"; YLW="\033[33m"; BLU="\033[34m"; CYN="\033[36m"

# ─── helpers ──────────────────────────────────────────────────────────────────
post() {
  local label="$1" payload="$2"
  printf "  → %-50s " "${label}..."
  local out http_code body
  out=$(curl -s -w "\n%{http_code}" -X POST "$ENDPOINT" \
    -H "Content-Type: application/json" \
    -H "X-API-Key: ${API_KEY}" \
    -d "$payload")
  http_code=$(printf '%s' "$out" | tail -1)
  body=$(printf '%s' "$out" | head -1)
  if [[ "$http_code" == "200" || "$http_code" == "201" ]]; then
    local alert_id; alert_id=$(printf '%s' "$body" | grep -o '"alert_id":"[^"]*"' | cut -d'"' -f4 || true)
    printf "${GRN}HTTP %s${RST}" "$http_code"
    [[ -n "$alert_id" ]] && printf "  id=%.12s…" "$alert_id"
    echo ""
  else
    printf "${RED}HTTP %s${RST}  %s\n" "$http_code" "$(printf '%s' "$body" | head -c 140)"
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
  echo -e "${BOLD}══════════════════════════════════════════════════════${RST}"
  echo -e "${BOLD}  $1${RST}"
  echo -e "${BOLD}══════════════════════════════════════════════════════${RST}"
}

pause() { echo -e "  ${DIM}⏳ waiting ${1}s…${RST}"; sleep "$1"; }

dbcheck() {
  echo ""
  echo -e "  ${CYN}--- DB: recent auto-created incidents (last 5m) ---${RST}"
  kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT '  INC  alerts=' || jsonb_array_length(alert_ids) ||
           '  status=' || status ||
           '  corr=' || COALESCE(LEFT(correlation_id,22),'NULL') ||
           '  » ' || LEFT(title,52)
    FROM incidents
    WHERE auto_created=true AND updated_at > NOW()-INTERVAL '5 minutes'
    ORDER BY created_at DESC LIMIT 8;" 2>/dev/null | grep -v "^$" | grep -v "^--" || echo "  (kubectl unavailable)"
}

incident_count_for() {
  kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT COUNT(*) FROM incidents
    WHERE auto_created=true AND correlation_id='$1'
      AND status IN ('open','investigating')
      AND updated_at > NOW()-INTERVAL '5 minutes';" 2>/dev/null | tr -d ' \n' || echo "?"
}

alert_count_for() {
  kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT COUNT(*) FROM alerts
    WHERE source_id='$1' AND updated_at > NOW()-INTERVAL '5 minutes';" 2>/dev/null | tr -d ' \n' || echo "?"
}

check() {
  local label="$1" got="$2" want="$3"
  if [[ "$got" == "$want" ]]; then
    echo -e "  ${GRN}✓ PASS${RST}  ${label}: ${got}"
  else
    echo -e "  ${RED}✗ FAIL${RST}  ${label}: expected=${want} got=${got}"
  fi
}

# ─── scenario selector ────────────────────────────────────────────────────────
RUN_ALL=true; SELECTED=()
[[ $# -gt 0 ]] && RUN_ALL=false && SELECTED=("$@")
run() { $RUN_ALL && return 0; for s in "${SELECTED[@]}"; do [[ "$s" == "$1" ]] && return 0; done; return 1; }

echo -e "${BOLD}AlertHub — Dynatrace Webhook Test Suite${RST}"
echo -e "${DIM}Endpoint : ${ENDPOINT}${RST}"
echo -e "${DIM}API Key  : ${API_KEY:0:22}…${RST}"
echo ""

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 1 — Smoke test: single critical alert
# ══════════════════════════════════════════════════════════════════════════════
if run 1; then
section "Scenario 1: Smoke test — single critical alert"
echo -e "  ${DIM}Expected: HTTP 200, alert_id in response${RST}"

post "CPU saturation on ${BM1}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"SMOKE-${TS}\",
  \"problemTitle\": \"High CPU load on bare metal host ${BM1}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"PERFORMANCE\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${BM1}\", \"entityName\": \"${BM1}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"HOST-${BM1}\", \"entityName\": \"${BM1}\", \"entityType\": \"HOST\"}],
  \"problemDetails\": \"CPU at 99% sustained 5 min. host.name: ${BM1}\",
  \"customProperties\": {\"host.name\": \"${BM1}\", \"impacted_entity\": \"${BM1}\", \"environment\": \"ADC\", \"cpu_pct\": \"99\", \"vm_count\": \"48\"}
}"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 2 — BM → VM cascade
# Root:       BM1 KVM host memory critical
# Downstream: VM1 CPU throttling, VM2 memory balloon
# Expected:   1 incident, correlation_id=BM1, alert_count ≥ 3
# ══════════════════════════════════════════════════════════════════════════════
if run 2; then
section "Scenario 2: BM → VM cascade  (root=${BM1})"
echo -e "  ${DIM}Expected: 1 incident, correlation_id=${BM1}, alert_count ≥ 3${RST}"
S2="S2-${TS}"

post "BM1 memory critical (root)" "{
  \"state\": \"OPEN\",
  \"problemId\": \"${S2}-bm-root\",
  \"problemTitle\": \"KVM host memory at 97% — ${BM1}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"RESOURCE_CONTENTION\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${BM1}\", \"entityName\": \"${BM1}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"HOST-${BM1}\", \"entityName\": \"${BM1}\", \"entityType\": \"HOST\"}],
  \"problemDetails\": \"Host memory at 97% (255/256 Gi). VM scheduling blocked. host.name: ${BM1}\",
  \"customProperties\": {\"host.name\": \"${BM1}\", \"impacted_entity\": \"${BM1}\", \"environment\": \"ADC\", \"memory_used_pct\": \"97\", \"vm_count\": \"48\"}
}"
pause 2

post "VM1 CPU throttling (downstream)" "{
  \"state\": \"OPEN\",
  \"problemId\": \"${S2}-vm1-cpu\",
  \"problemTitle\": \"CPU throttling on VM ${VM1}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"PERFORMANCE\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${BM1}\", \"entityName\": \"${BM1}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"HOST-${VM1}\", \"entityName\": \"${VM1}\", \"entityType\": \"HOST\"}],
  \"problemDetails\": \"CPU steal 34% on ${VM1} due to host oversubscription. host.name: ${VM1}\",
  \"customProperties\": {\"host.name\": \"${VM1}\", \"impacted_entity\": \"${VM1}\", \"kvm_host\": \"${BM1}\", \"environment\": \"ADC\", \"cpu_steal_pct\": \"34\"}
}"
pause 2

post "VM2 memory balloon (downstream)" "{
  \"state\": \"OPEN\",
  \"problemId\": \"${S2}-vm2-mem\",
  \"problemTitle\": \"Memory balloon reclaim on VM ${VM2}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"RESOURCE_CONTENTION\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${BM1}\", \"entityName\": \"${BM1}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"HOST-${VM2}\", \"entityName\": \"${VM2}\", \"entityType\": \"HOST\"}],
  \"problemDetails\": \"Hypervisor reclaiming guest memory via balloon on ${VM2}. Guest OOM risk. host.name: ${VM2}\",
  \"customProperties\": {\"host.name\": \"${VM2}\", \"impacted_entity\": \"${VM2}\", \"kvm_host\": \"${BM1}\", \"environment\": \"ADC\", \"balloon_mb\": \"8192\"}
}"
pause 4
dbcheck
echo ""
n=$(incident_count_for "${BM1}")
check "S2 incident_count(corr=${BM1})" "$n" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 3 — K8s node → Pod cascade across 3 namespaces
# Root:       K8N1 NotReady
# Downstream: pod in dex, ingress-nginx, monitoring
# Expected:   1 incident, alert_count=4
# ══════════════════════════════════════════════════════════════════════════════
if run 3; then
section "Scenario 3: K8s node → Pod cascade  (cluster=mps-nonprod-rno)"
echo -e "  ${DIM}Expected: 1 incident, alert_count=4${RST}"
S3="S3-${TS}"

post "K8s node NotReady (root)" "{
  \"state\": \"OPEN\",
  \"problemId\": \"${S3}-node-root\",
  \"problemTitle\": \"K8s Node NotReady: ${K8N1}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${K8N1}\", \"entityName\": \"${K8N1}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"HOST-${K8N1}\", \"entityName\": \"${K8N1}\", \"entityType\": \"HOST\"}],
  \"problemDetails\": \"Node ${K8N1} NotReady. kubelet unresponsive. 23 pods evicting. host.name: ${K8N1}\",
  \"customProperties\": {\"host.name\": \"${K8N1}\", \"k8s.node.name\": \"${K8N1}\", \"k8s.cluster.name\": \"mps-nonprod-rno\", \"k8s.cluster.uid\": \"${NONPROD_UID}\", \"impacted_entity\": \"${K8N1}\", \"environment\": \"ADC\"}
}"
pause 2

post "dex pod evicted (downstream)" "{
  \"state\": \"OPEN\",
  \"problemId\": \"${S3}-pod-dex\",
  \"problemTitle\": \"Pod evicted: dex-7d4f9b8c6-vk2px (ns=dex)\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${K8N1}\", \"entityName\": \"${K8N1}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-dex-7d4f9b8c6-vk2px\", \"entityName\": \"dex-7d4f9b8c6-vk2px\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
  \"problemDetails\": \"Pod dex-7d4f9b8c6-vk2px evicted from ${K8N1}. SSO auth intermittently unavailable.\",
  \"customProperties\": {\"k8s.namespace.name\": \"dex\", \"k8s.workload.name\": \"dex\", \"k8s.node.name\": \"${K8N1}\", \"k8s.cluster.name\": \"mps-nonprod-rno\", \"k8s.cluster.uid\": \"${NONPROD_UID}\", \"environment\": \"ADC\"}
}"
pause 1

post "ingress-nginx DaemonSet pod evicted (downstream)" "{
  \"state\": \"OPEN\",
  \"problemId\": \"${S3}-pod-ingress\",
  \"problemTitle\": \"DaemonSet pod evicted: ingress-nginx-controller on ${K8N1}\",
  \"impactLevel\": \"SERVICES\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${K8N1}\", \"entityName\": \"${K8N1}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-ingress-nginx-controller-${K8N1}\", \"entityName\": \"ingress-nginx-controller\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
  \"problemDetails\": \"ingress-nginx-controller DaemonSet pod lost on ${K8N1}. Node removed from LB pool.\",
  \"customProperties\": {\"k8s.namespace.name\": \"ingress-nginx\", \"k8s.workload.name\": \"ingress-nginx-controller\", \"k8s.workload.kind\": \"DaemonSet\", \"k8s.node.name\": \"${K8N1}\", \"k8s.cluster.uid\": \"${NONPROD_UID}\", \"environment\": \"ADC\"}
}"
pause 1

post "prometheus-k8s pod evicted (downstream)" "{
  \"state\": \"OPEN\",
  \"problemId\": \"${S3}-pod-prom\",
  \"problemTitle\": \"StatefulSet pod evicted: prometheus-k8s-1 (ns=monitoring)\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"PERFORMANCE\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${K8N1}\", \"entityName\": \"${K8N1}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-prometheus-k8s-1\", \"entityName\": \"prometheus-k8s-1\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
  \"problemDetails\": \"prometheus-k8s-1 evicted. Monitoring gap for zone z3 nodes.\",
  \"customProperties\": {\"k8s.namespace.name\": \"monitoring\", \"k8s.workload.name\": \"prometheus-k8s\", \"k8s.node.name\": \"${K8N1}\", \"k8s.cluster.uid\": \"${NONPROD_UID}\", \"environment\": \"ADC\"}
}"
pause 4
dbcheck
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 4 — Memory storm: 3 KVM hosts simultaneously
# Expected: 3 separate incidents (different hosts, different correlation IDs)
# ══════════════════════════════════════════════════════════════════════════════
if run 4; then
section "Scenario 4: Memory storm — 3 KVM hosts simultaneously"
echo -e "  ${DIM}Expected: 3 separate incidents (independent roots)${RST}"
S4="S4-${TS}"

for host in "$BM1" "$BM2" "$BM3"; do
  post "${host} memory critical" "{
    \"state\": \"OPEN\",
    \"problemId\": \"${S4}-${host}-mem\",
    \"problemTitle\": \"KVM host memory critical: ${host}\",
    \"impactLevel\": \"INFRASTRUCTURE\",
    \"severity\": \"RESOURCE_CONTENTION\",
    \"status\": \"OPEN\",
    \"startTime\": \"${NOW}\",
    \"rootCauseEntity\": {\"entityId\": \"HOST-${host}\", \"entityName\": \"${host}\", \"entityType\": \"HOST\"},
    \"impactedEntities\": [{\"entityId\": \"HOST-${host}\", \"entityName\": \"${host}\", \"entityType\": \"HOST\"}],
    \"problemDetails\": \"Memory utilisation 92%. Balloon reclaim active. host.name: ${host}\",
    \"customProperties\": {\"host.name\": \"${host}\", \"impacted_entity\": \"${host}\", \"environment\": \"ADC\", \"memory_used_pct\": \"92\"}
  }"
done
pause 4
dbcheck
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 5 — Network partition: rno ↔ iad
# ══════════════════════════════════════════════════════════════════════════════
if run 5; then
section "Scenario 5: Network partition — rno ↔ iad cross-region link"
echo -e "  ${DIM}Expected: 1 incident, BGP root + 2 downstream service impacts${RST}"
S5="S5-${TS}"

post "BGP peer loss rno→iad (root)" "{
  \"state\": \"OPEN\",
  \"problemId\": \"${S5}-bgp-root\",
  \"problemTitle\": \"BGP peer loss: rno-core-spine-01 → iad-border-01\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-rno-core-spine-01\", \"entityName\": \"rno-core-spine-01\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"HOST-rno-core-spine-01\", \"entityName\": \"rno-core-spine-01\", \"entityType\": \"HOST\"}],
  \"problemDetails\": \"BGP session down 3m. Packet loss primary 100%, secondary 44%. host.name: rno-core-spine-01\",
  \"customProperties\": {\"host.name\": \"rno-core-spine-01\", \"impacted_entity\": \"rno-core-spine-01\", \"environment\": \"ADC\", \"src_region\": \"rno\", \"dst_region\": \"iad\", \"packet_loss_pct\": \"100\"}
}"
pause 2

post "Redis replication lag (downstream)" "{
  \"state\": \"OPEN\",
  \"problemId\": \"${S5}-redis-lag\",
  \"problemTitle\": \"Redis cross-region replication lag 45s (rno→iad)\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"PERFORMANCE\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-rno-core-spine-01\", \"entityName\": \"rno-core-spine-01\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"SERVICE-redis-cluster-rno\", \"entityName\": \"redis-cluster-rno\", \"entityType\": \"SERVICE\"}],
  \"problemDetails\": \"Redis replication lag 45s. Session state stale in iad. Failover would cause session loss.\",
  \"customProperties\": {\"k8s.namespace.name\": \"aileron\", \"k8s.workload.name\": \"redis-cluster\", \"environment\": \"ADC\", \"replication_lag_seconds\": \"45\"}
}"
pause 2

post "Postgres WAL shipping stalled (downstream)" "{
  \"state\": \"OPEN\",
  \"problemId\": \"${S5}-pg-wal\",
  \"problemTitle\": \"PostgreSQL WAL shipping stalled: rno→iad standby (RPO breached)\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-rno-core-spine-01\", \"entityName\": \"rno-core-spine-01\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"SERVICE-postgres-primary-0\", \"entityName\": \"postgres-primary-0\", \"entityType\": \"SERVICE\"}],
  \"problemDetails\": \"WAL shipping stalled 4m. iad standby 4min behind. DR failover would lose data.\",
  \"customProperties\": {\"k8s.namespace.name\": \"aileron\", \"k8s.workload.name\": \"postgres\", \"environment\": \"ADC\", \"wal_lag_seconds\": \"240\", \"rpo_breach\": \"true\"}
}"
pause 4
dbcheck
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 6 — Service degradation chain: DB → API → Frontend
# ══════════════════════════════════════════════════════════════════════════════
if run 6; then
section "Scenario 6: Service degradation chain  (DB → API → Frontend)"
echo -e "  ${DIM}Expected: 1 incident, 3 alerts correlated through aileron${RST}"
S6="S6-${TS}"

post "PostgreSQL connections exhausted (root)" "{
  \"state\": \"OPEN\",
  \"problemId\": \"${S6}-pg-conns\",
  \"problemTitle\": \"PostgreSQL connection pool exhausted: alerthub-postgres (199/200)\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"RESOURCE_CONTENTION\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"SERVICE-alerthub-postgres\", \"entityName\": \"alerthub-postgres\", \"entityType\": \"SERVICE\"},
  \"impactedEntities\": [{\"entityId\": \"SERVICE-alerthub-postgres\", \"entityName\": \"alerthub-postgres\", \"entityType\": \"SERVICE\"}],
  \"problemDetails\": \"max_connections=200 reached. Wait queue=87. p99 latency=14.2s.\",
  \"customProperties\": {\"k8s.namespace.name\": \"aileron\", \"k8s.workload.name\": \"postgres\", \"environment\": \"ADC\", \"current_connections\": \"199\", \"max_connections\": \"200\", \"wait_queue\": \"87\"}
}"
pause 2

post "alerthub-backend HTTP 503 (downstream)" "{
  \"state\": \"OPEN\",
  \"problemId\": \"${S6}-be-503\",
  \"problemTitle\": \"HTTP 503 Service Unavailable: alerthub-backend (error rate 94%)\",
  \"impactLevel\": \"SERVICES\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"SERVICE-alerthub-postgres\", \"entityName\": \"alerthub-postgres\", \"entityType\": \"SERVICE\"},
  \"impactedEntities\": [{\"entityId\": \"SERVICE-alerthub-backend\", \"entityName\": \"alerthub-backend\", \"entityType\": \"SERVICE\"}],
  \"problemDetails\": \"94% of requests returning 503. Root: DB connection exhaustion. 0/2 pods healthy.\",
  \"customProperties\": {\"k8s.namespace.name\": \"aileron\", \"k8s.workload.name\": \"alerthub-backend\", \"environment\": \"ADC\", \"error_rate_pct\": \"94\", \"ready_pods\": \"0\"}
}"
pause 2

post "Frontend 502 auth failing (downstream)" "{
  \"state\": \"OPEN\",
  \"problemId\": \"${S6}-fe-502\",
  \"problemTitle\": \"Frontend HTTP 502: JWT validation timeout — users logged out\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"SERVICE-alerthub-postgres\", \"entityName\": \"alerthub-postgres\", \"entityType\": \"SERVICE\"},
  \"impactedEntities\": [{\"entityId\": \"SERVICE-alerthub-frontend\", \"entityName\": \"alerthub-frontend\", \"entityType\": \"SERVICE\"}],
  \"problemDetails\": \"All auth-required pages returning 502. JWT validation calls timing out after 30s. ~240 users affected.\",
  \"customProperties\": {\"k8s.namespace.name\": \"aileron\", \"k8s.workload.name\": \"frontend\", \"environment\": \"ADC\", \"auth_timeout_ms\": \"30000\", \"affected_users\": \"240\"}
}"
pause 4
dbcheck
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 7 — Deduplication: same problemId sent twice
# Expected: 1 alert record, alert.count incremented to 2
# ══════════════════════════════════════════════════════════════════════════════
if run 7; then
section "Scenario 7: Deduplication — same problemId sent twice"
echo -e "  ${DIM}Expected: 1 alert record, count=2 (deduped by problemId/fingerprint)${RST}"
DEDUP_ID="DEDUP-${TS}"

for i in 1 2; do
  post "send #${i} — problemId=${DEDUP_ID}" "{
    \"state\": \"OPEN\",
    \"problemId\": \"${DEDUP_ID}\",
    \"problemTitle\": \"Disk at 89% on /data — ${BM1}\",
    \"impactLevel\": \"INFRASTRUCTURE\",
    \"severity\": \"RESOURCE_CONTENTION\",
    \"status\": \"OPEN\",
    \"startTime\": \"${NOW}\",
    \"rootCauseEntity\": {\"entityId\": \"HOST-${BM1}\", \"entityName\": \"${BM1}\", \"entityType\": \"HOST\"},
    \"impactedEntities\": [{\"entityId\": \"HOST-${BM1}\", \"entityName\": \"${BM1}\", \"entityType\": \"HOST\"}],
    \"problemDetails\": \"Disk /data at 89%. ETA full 2h at 340 MB/s write rate. host.name: ${BM1}\",
    \"customProperties\": {\"host.name\": \"${BM1}\", \"impacted_entity\": \"${BM1}\", \"environment\": \"ADC\", \"disk_pct\": \"89\", \"send_number\": \"${i}\"}
  }"
  [[ "$i" == "1" ]] && pause 2
done
pause 2
n=$(alert_count_for "${DEDUP_ID}")
check "S7 alert record count for problemId=${DEDUP_ID}" "$n" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 8 — OPEN then RESOLVED → incident auto-close
# ══════════════════════════════════════════════════════════════════════════════
if run 8; then
section "Scenario 8: OPEN → RESOLVED — incident auto-close"
echo -e "  ${DIM}Expected: incident opens, then status → resolved after RESOLVED alert${RST}"
S8="S8-${TS}"
RESOLVE_ID="${S8}-resolve-me"

post "OPEN: alerthub-backend 0/2 replicas" "{
  \"state\": \"OPEN\",
  \"problemId\": \"${RESOLVE_ID}\",
  \"problemTitle\": \"Deployment unavailable: alerthub-backend (0/2 ready)\",
  \"impactLevel\": \"SERVICES\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"SERVICE-alerthub-backend\", \"entityName\": \"alerthub-backend\", \"entityType\": \"SERVICE\"},
  \"impactedEntities\": [{\"entityId\": \"SERVICE-alerthub-backend\", \"entityName\": \"alerthub-backend\", \"entityType\": \"SERVICE\"}],
  \"problemDetails\": \"alerthub-backend: 0/2 replicas ready. Rolling update may have failed.\",
  \"customProperties\": {\"k8s.namespace.name\": \"aileron\", \"k8s.workload.name\": \"alerthub-backend\", \"environment\": \"ADC\", \"ready_replicas\": \"0\", \"desired_replicas\": \"2\"}
}"
echo -e "  ${DIM}waiting 5s before sending RESOLVED…${RST}"
pause 5

post "RESOLVED: deployment recovered 2/2" "{
  \"state\": \"RESOLVED\",
  \"problemId\": \"${RESOLVE_ID}\",
  \"problemTitle\": \"RESOLVED: alerthub-backend back to 2/2 ready\",
  \"impactLevel\": \"SERVICES\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"RESOLVED\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"SERVICE-alerthub-backend\", \"entityName\": \"alerthub-backend\", \"entityType\": \"SERVICE\"},
  \"impactedEntities\": [{\"entityId\": \"SERVICE-alerthub-backend\", \"entityName\": \"alerthub-backend\", \"entityType\": \"SERVICE\"}],
  \"problemDetails\": \"alerthub-backend recovered. 2/2 replicas ready. Health checks passing.\",
  \"customProperties\": {\"k8s.namespace.name\": \"aileron\", \"k8s.workload.name\": \"alerthub-backend\", \"environment\": \"ADC\", \"ready_replicas\": \"2\", \"desired_replicas\": \"2\"}
}"
pause 3
echo ""
echo -e "  ${CYN}--- DB: incident status for ${S8} ---${RST}"
kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT '  status=' || status || '  title=' || LEFT(title,60)
  FROM incidents
  WHERE auto_created=true AND title ILIKE '%alerthub-backend%'
    AND updated_at > NOW()-INTERVAL '5 minutes'
  ORDER BY created_at DESC LIMIT 3;" 2>/dev/null | grep -v "^$" || echo "  (kubectl unavailable)"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 9 — Burst stress test
# ══════════════════════════════════════════════════════════════════════════════
if run 9; then
section "Scenario 9: Burst stress test — ${BURST} concurrent alerts"
echo -e "  ${DIM}Firing ${BURST} fire-and-forget alerts, then measuring throughput${RST}"
S9="S9-${TS}"

HOSTS=("$BM1" "$BM2" "$BM3" "$K8N1" "$K8N2" "$K8N3" "$K8N4")
SEVS=("PERFORMANCE" "RESOURCE_CONTENTION" "AVAILABILITY" "PERFORMANCE" "RESOURCE_CONTENTION" "PERFORMANCE" "CUSTOM_ALERT")
t_start=$(date +%s%N)

for i in $(seq 1 "$BURST"); do
  host="${HOSTS[$((i % ${#HOSTS[@]}))]}"
  sev="${SEVS[$((i % ${#SEVS[@]}))]}"
  post_quiet "{
    \"state\": \"OPEN\",
    \"problemId\": \"${S9}-burst-${i}\",
    \"problemTitle\": \"Burst stress alert #${i}: ${host}\",
    \"impactLevel\": \"INFRASTRUCTURE\",
    \"severity\": \"${sev}\",
    \"status\": \"OPEN\",
    \"startTime\": \"${NOW}\",
    \"rootCauseEntity\": {\"entityId\": \"HOST-${host}\", \"entityName\": \"${host}\", \"entityType\": \"HOST\"},
    \"impactedEntities\": [{\"entityId\": \"HOST-${host}\", \"entityName\": \"${host}\", \"entityType\": \"HOST\"}],
    \"problemDetails\": \"Synthetic burst test ${i}/${BURST}. host.name: ${host}\",
    \"customProperties\": {\"host.name\": \"${host}\", \"impacted_entity\": \"${host}\", \"environment\": \"ADC\", \"batch\": \"${S9}\", \"index\": \"${i}\"}
  }"
done
wait
t_end=$(date +%s%N)
elapsed_ms=$(( (t_end - t_start) / 1000000 ))
rps=$(( BURST * 1000 / (elapsed_ms + 1) ))
echo ""
echo -e "  ${GRN}Fired ${BURST} alerts in ${elapsed_ms}ms  (~${rps} req/s)${RST}"
pause 3
echo ""
echo -e "  ${CYN}--- DB: ingested count for batch ${S9} ---${RST}"
kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT '  ingested=' || COUNT(*) || '  expected=${BURST}'
  FROM alerts
  WHERE source_id LIKE '${S9}-%'
    AND created_at > NOW()-INTERVAL '5 minutes';" 2>/dev/null | grep -v "^$" || echo "  (kubectl unavailable)"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 10 — Cross-region isolation: rno vs maiden
# Same failure type, different regions → 2 separate incidents
# ══════════════════════════════════════════════════════════════════════════════
if run 10; then
section "Scenario 10: Cross-region isolation — rno vs maiden"
echo -e "  ${DIM}Same alert type, different regions. Expected: 2 separate incidents${RST}"
S10="S10-${TS}"

post "CPU spike — rno/${BM1}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"${S10}-rno\",
  \"problemTitle\": \"CPU spike on ${BM1} (rno)\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"PERFORMANCE\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${BM1}\", \"entityName\": \"${BM1}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"HOST-${BM1}\", \"entityName\": \"${BM1}\", \"entityType\": \"HOST\"}],
  \"problemDetails\": \"CPU 91% on ${BM1}. host.name: ${BM1}\",
  \"customProperties\": {\"host.name\": \"${BM1}\", \"impacted_entity\": \"${BM1}\", \"environment\": \"ADC\", \"region\": \"rno\"}
}"
pause 2

post "CPU spike — maiden/${MDN_BM}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"${S10}-mdn\",
  \"problemTitle\": \"CPU spike on ${MDN_BM} (maiden)\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"PERFORMANCE\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${MDN_BM}\", \"entityName\": \"${MDN_BM}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"HOST-${MDN_BM}\", \"entityName\": \"${MDN_BM}\", \"entityType\": \"HOST\"}],
  \"problemDetails\": \"CPU 91% on ${MDN_BM}. host.name: ${MDN_BM}\",
  \"customProperties\": {\"host.name\": \"${MDN_BM}\", \"impacted_entity\": \"${MDN_BM}\", \"environment\": \"ADC\", \"region\": \"maiden\"}
}"
pause 4
dbcheck
echo ""
rno_n=$(incident_count_for "${BM1}")
mdn_n=$(incident_count_for "${MDN_BM}")
check "S10 rno incident exists"    "$rno_n" "1"
check "S10 maiden incident exists" "$mdn_n" "1"
fi

# ─── summary ──────────────────────────────────────────────────────────────────
echo ""
echo -e "${BOLD}══════════════════════════════════════════════════════${RST}"
echo -e "${BOLD}  Done.${RST}"
echo -e "  ${DIM}View incidents → https://aileron.example.com/incidents${RST}"
echo ""
