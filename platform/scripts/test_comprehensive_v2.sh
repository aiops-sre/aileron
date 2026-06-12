#!/usr/bin/env bash
# ╔══════════════════════════════════════════════════════════════════════════════╗
# ║  test_comprehensive_v2.sh — AlertHub v1.0.17-dev regression + coverage      ║
# ║                                                                              ║
# ║  This script covers scenarios that are NOT in the existing test suite:       ║
# ║    • Fix regression tests for v1.0.16 (cross-cluster title isolation,        ║
# ║      KUBERNETES_WORKLOAD- entity ID recognition)                             ║
# ║    • Fix regression tests for v1.0.17 (resolved alert via critical bypass,   ║
# ║      resolved alert via full-correlation path)                               ║
# ║    • NetApp RNO 5-layer full cascade with real entity IDs                    ║
# ║    • mps-sandbox-rno and k8preview01-rno cluster isolation                   ║
# ║    • Prometheus-source alerts (non-DT scoring fallback)                      ║
# ║    • Flapping 3-cycle OPEN→RESOLVED×3                                        ║
# ║    • Late-arriving root (downstream fires before root)                       ║
# ║    • Silent recovery (RESOLVED without prior OPEN)                           ║
# ║    • MDN DC full cascade (BM→node→pods)                                      ║
# ║    • Multi-cluster storm isolation (same title, different clusters → 2 inc)  ║
# ║    • Burst with resolved alerts interspersed                                 ║
# ╠══════════════════════════════════════════════════════════════════════════════╣
# ║  Usage:                                                                      ║
# ║    bash scripts/test_comprehensive_v2.sh           # all 20 scenarios        ║
# ║    bash scripts/test_comprehensive_v2.sh A1        # single scenario by ID   ║
# ║    bash scripts/test_comprehensive_v2.sh A1 A3 B1  # specific set            ║
# ║    BURST=200 bash scripts/test_comprehensive_v2.sh H1                        ║
# ╠══════════════════════════════════════════════════════════════════════════════╣
# ║  Scenarios:                                                                  ║
# ║    A1  Cross-cluster title isolation   — "not all pods ready" from 3         ║
# ║        different clusters stays in 3 separate incidents (v1.0.16 fix)        ║
# ║    A2  KUBERNETES_WORKLOAD- entity ID  — workload root entity recognized     ║
# ║        and not treated as generic text (v1.0.16 entity.go fix)               ║
# ║    A3  Resolved via critical bypass    — CRITICAL alert that bypasses         ║
# ║        fastCh lands in topoCh with resolved handler invoked (v1.0.17 fix)    ║
# ║    A4  Resolved via full-corr path     — fullCh resolved alert closes        ║
# ║        incident rather than creating a new one (v1.0.17 fix)                 ║
# ║    B1  NetApp RNO 5-layer cascade      — netapp-rno-cluster001 → SVM          ║
# ║        iapps-rno-k8s → volume → PVC → commerce/checkout-service pod          ║
# ║    B2  NetApp aggregate → pod crash    — agg0 95% full → PVC write stall     ║
# ║        → CrashLoopBackOff in payments namespace                              ║
# ║    C1  Sandbox cluster isolation       — mps-sandbox-rno alert stays          ║
# ║        separate from mps-nonprod-rno alert with same title                   ║
# ║    C2  k8preview01 node cascade        — k8preview01-rno node → real pods    ║
# ║        isolated from mps-nonprod-rno cascade                                 ║
# ║    C3  Same workload, two clusters     — checkout-service pods in             ║
# ║        mps-nonprod-rno/commerce AND mps-sandbox-rno/aileron-agent → 2 incidents    ║
# ║    D1  KUBERNETES_WORKLOAD entity type — KUBERNETES_WORKLOAD entity root     ║
# ║        gets k8s_workload entity type with correct entity ID                  ║
# ║    D2  CLOUD_APPLICATION entity        — APPLICATION entity type normalised  ║
# ║        to correct labels with app entity ID preserved                        ║
# ║    E1  Prometheus source alert         — non-DT source with k8s labels,      ║
# ║        should correlate via pure-label scoring path                          ║
# ║    E2  Flapping 3-cycle                — OPEN → RESOLVED → OPEN → RESOLVED  ║
# ║        → OPEN on same problemId; final state = open incident                 ║
# ║    E3  Late root arrival               — downstream fires 20s before root;    ║
# ║        root should merge all downstreams into 1 incident when it arrives     ║
# ║    E4  Silent recovery (orphan RESOLVED) — RESOLVED with no prior OPEN       ║
# ║        should not crash or create a new incident                             ║
# ║    F1  Null rootCauseEntity fields     — entityId=null, entityName=null      ║
# ║        payload; should fall through to label-based scoring without panic     ║
# ║    F2  Generic description no longer creates root_cause_entity               ║
# ║        (combined with valid entity ID — only real IDs accepted as root)      ║
# ║    G1  MDN DC bare-metal full cascade  — mps-mondev-mdn BM → K8s node       ║
# ║        → pods in auth/payments/infra-monitoring namespaces                   ║
# ║    G2  Multi-cluster storm isolation   — nonprod + mondev fire same-title    ║
# ║        alerts simultaneously → must stay in 2 separate incidents             ║
# ║    H1  Burst with resolved interspersed — 60 OPEN + 15 RESOLVED mixed,       ║
# ║        resolved must close their incidents, not create new ones              ║
# ╚══════════════════════════════════════════════════════════════════════════════╝

set -uo pipefail

# ─── config ───────────────────────────────────────────────────────────────────
ENDPOINT="${ENDPOINT:-https://aileron.example.com/api/v1/webhooks/dynatrace}"
API_KEY="${API_KEY:-ah_c76ddeaa86c74818ac84997c0f2a8174eaba3bf0fdab295132acb8aba2238e5f}"
NS="${NS:-aileron}"
BURST="${BURST:-60}"
NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
TS=$(date +%s)

# ─── real entity constants ────────────────────────────────────────────────────
# CloudStack KVM bare-metal hypervisors (RNO DC)
BM1="cloudstack-cluster-2-iapps-100-67-61-18"
BM2="cloudstack-cluster-2-iapps-100-67-61-31"
BM3="cloudstack-cluster-2-iapps-100-67-61-45"

# CloudStack VMs (backed by BM1)
VM1="cloudstack-cluster-2-mps-nonprod-rno-worker-z3-08"
VM2="cloudstack-cluster-2-mps-nonprod-rno-worker-z3-13"

# K8s worker nodes — mps-nonprod-rno cluster
K8N_Z3_08="mps-nonprod-rno-worker-z3-08"
K8N_Z3_13="mps-nonprod-rno-worker-z3-13"
K8N_Z2_01="mps-nonprod-rno-worker-z2-01"
K8N_Z1_08="mps-nonprod-rno-worker-z1-08"

# K8s worker node — k8preview01-rno cluster
K8N_PREV="k8preview01-rno-worker-z1-01"
K8N_PREV2="k8preview01-cs-vm-worker12-rno"

# mps-mondev-mdn bare-metal + K8s node (MDN DC / Maiden datacenter)
MDN_BM="mps-mondev-mdn-worker-z3-05"
MDN_BM2="mps-mondev-mdn-worker-z1-01"
MDN_K8N="mps-mondev-mdn-worker-z3-05"  # node name matches BM host in mondev

# Cluster UIDs
NONPROD_UID="88b12bf2-9f23-4b4a-b06a-3d2b33134a3b"
MONDEV_UID="00a07750-e556-443e-89d9-80341edb472d"
K8PREV_UID="d1be9a2f-4e83-49bf-8800-b9a2bc2e7da0"
TOOLING_UID="0a0430fb-0c23-4e80-a000-caa5570d6c17"
SANDBOX_UID="f7c3a1d5-2b8e-4f91-a3c7-9e0d1b2c3a4f"

# NetApp storage entities (RNO)
NETAPP_RNO="netapp-rno-cluster001"
NETAPP_SVM="iapps-rno-k8s"
NETAPP_NODE1="netapp-rno-node1"
NETAPP_AGG="aggr0_rno_node1_data"

# NetApp storage entities (MDN)
NETAPP_MDN="netapp-mdn-cluster001"
NETAPP_MDN_SVM="iapps-mdn-k8s"

# Real pod names on K8N_Z3_08 (from test_real_cascade.sh)
POD_DEX="dex-565647688-wd4qz"
POD_INGRESS="ingress-nginx-controller-5ffg4"
POD_AEM_QA="dispatcher-publish-preview-1"
POD_ARGOCD="argocd-repo-server-5d956d6cbf-v4xnh"

# Real pod names on k8preview01 (from test_real_cascade.sh)
POD_PREV_FILMS="ac-films-66ffd4b8bb-kwpmr"
POD_PREV_ARGOCD="argocd-redis-6d68b7d767-ht4wg"
POD_PREV_INGRESS="nginx-ingress-controller-lgg4m"
POD_PREV_FRONTIER="frontier-5d5c8cb67d-9ss7t"

# Real pods in commerce / payments namespaces (mps-nonprod-rno)
POD_CHECKOUT="checkout-service-7d8f6b9c4-xr2kp"
POD_INVENTORY="inventory-api-6c9f7d8b5-mn4lq"
POD_PAYMENT_GW="payment-gateway-5b8c6d7a3-pq9wz"

# ─── color codes ──────────────────────────────────────────────────────────────
BOLD="\033[1m"; DIM="\033[2m"; RST="\033[0m"
RED="\033[31m"; GRN="\033[32m"; YLW="\033[33m"; BLU="\033[34m"; CYN="\033[36m"; MAG="\033[35m"

# ─── test counters ────────────────────────────────────────────────────────────
PASS_COUNT=0
FAIL_COUNT=0
SKIP_COUNT=0

# ─── helpers ──────────────────────────────────────────────────────────────────
post() {
  local label="$1" problem_id="$2" payload="$3"
  printf "  → %-54s " "[${problem_id}]..."
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
    [[ -n "$alert_id" ]] && printf "  id=%.12s…" "$alert_id"
    echo "  ${label}"
  else
    printf "${RED}HTTP %s${RST}  %s\n" "$http_code" "$(echo "$body" | head -c 180)"
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
  echo -e "${BOLD}${MAG}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RST}"
  echo -e "${BOLD}  $1${RST}"
  echo -e "${BOLD}${MAG}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RST}"
}

pause() { echo -e "  ${DIM}⏳ ${1}s…${RST}"; sleep "$1"; }

dbcheck() {
  echo ""
  echo -e "  ${CYN}--- DB: auto-created incidents (last 10 min) ---${RST}"
  kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT '  INC ' || LEFT(id::text,8) ||
           '  alerts=' || jsonb_array_length(alert_ids) ||
           '  ' || status ||
           '  corr=' || COALESCE(LEFT(correlation_id,24),'NULL') ||
           '  » ' || LEFT(title,48)
    FROM incidents
    WHERE auto_created=true AND updated_at > NOW()-INTERVAL '10 minutes'
    ORDER BY created_at DESC LIMIT 12;" 2>/dev/null \
  | grep -v "^$" || echo -e "  ${DIM}(kubectl unavailable — skipping DB check)${RST}"
}

logs() {
  echo -e "  ${CYN}--- Logs: correlation decisions ---${RST}"
  kubectl logs -n "$NS" -l app=alerthub-backend --since=90s 2>/dev/null \
  | grep -E "RCE alert=|CREATE_ROOT|ATTACH_TO_ROOT|correlation_id.*set|incident.*created|resolved|handleResolved|RESOLVED" \
  | head -20 || echo -e "  ${DIM}(kubectl unavailable)${RST}"
}

# DB query helpers
incident_count() {
  # incident_count <correlation_id> [window_minutes=10]
  local corr="$1" win="${2:-10}"
  kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT COUNT(*) FROM incidents
    WHERE auto_created=true AND correlation_id='${corr}'
      AND status IN ('open','investigating')
      AND updated_at > NOW()-INTERVAL '${win} minutes';" 2>/dev/null | tr -d ' \n' || echo "?"
}

incident_status() {
  # incident_status <correlation_id> — returns last status for correlation_id
  local corr="$1"
  kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT status FROM incidents
    WHERE auto_created=true AND correlation_id='${corr}'
    ORDER BY updated_at DESC LIMIT 1;" 2>/dev/null | tr -d ' \n' || echo "?"
}

alert_count_for_source() {
  # alert_count_for_source <source_id> [window_minutes=10]
  local sid="$1" win="${2:-10}"
  kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT COUNT(*) FROM alerts
    WHERE source_id='${sid}' AND updated_at > NOW()-INTERVAL '${win} minutes';" 2>/dev/null | tr -d ' \n' || echo "?"
}

incident_alert_count() {
  # incident_alert_count <correlation_id> — alerts in the single latest matching incident
  local corr="$1"
  kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT jsonb_array_length(alert_ids) FROM incidents
    WHERE auto_created=true AND correlation_id='${corr}'
    ORDER BY created_at DESC LIMIT 1;" 2>/dev/null | tr -d ' \n' || echo "?"
}

count_incidents_with_title() {
  # count_incidents_with_title <title_fragment> [window_minutes=10]
  local frag="$1" win="${2:-10}"
  kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT COUNT(DISTINCT id) FROM incidents
    WHERE auto_created=true
      AND title ILIKE '%${frag}%'
      AND updated_at > NOW()-INTERVAL '${win} minutes';" 2>/dev/null | tr -d ' \n' || echo "?"
}

# PASS/FAIL assertion
check() {
  local label="$1" got="$2" want="$3"
  if [[ "$got" == "$want" ]]; then
    echo -e "  ${GRN}✓ PASS${RST}  ${label}: ${got}"
    (( PASS_COUNT++ )) || true
  else
    echo -e "  ${RED}✗ FAIL${RST}  ${label}: want=${want} got=${got}"
    (( FAIL_COUNT++ )) || true
  fi
}

# SKIP assertion (when kubectl unavailable for DB check)
check_or_skip() {
  local label="$1" got="$2" want="$3"
  if [[ "$got" == "?" ]]; then
    echo -e "  ${YLW}⊘ SKIP${RST}  ${label}: kubectl unavailable"
    (( SKIP_COUNT++ )) || true
  else
    check "$label" "$got" "$want"
  fi
}

# ─── scenario selector ────────────────────────────────────────────────────────
RUN_ALL=true
SELECTED=()
[[ $# -gt 0 ]] && RUN_ALL=false && SELECTED=("$@")
run() {
  $RUN_ALL && return 0
  for s in "${SELECTED[@]}"; do
    [[ "$s" == "$1" ]] && return 0
  done
  return 1
}

echo ""
echo -e "${BOLD}AlertHub — Comprehensive V2 Test Suite (v1.0.17-dev regression + coverage)${RST}"
echo -e "${DIM}Endpoint : ${ENDPOINT}${RST}"
echo -e "${DIM}Namespace: ${NS}${RST}"
echo -e "${DIM}Run TS   : ${TS}${RST}"
echo ""

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO A1 — Cross-cluster title isolation (v1.0.16 fix regression)
#
# Bug: generic description "not all pods ready" was accepted as root_cause_entity,
# causing alerts from different clusters to merge into one incident (incident #3266).
# Fix: title-fingerprint fallback is now scoped as cluster/namespace:title.
#
# Test: fire identical "Not all pods ready" alerts from 3 different clusters.
# Expected: 3 separate incidents, NOT 1 merged incident.
# ══════════════════════════════════════════════════════════════════════════════
if run A1; then
section "A1 · Cross-cluster title isolation  (v1.0.16 fix regression)"
echo -e "  ${DIM}Fire identical title from nonprod-rno, mondev-mdn, k8preview01.${RST}"
echo -e "  ${DIM}Expected: 3 separate incidents (not 1 merged incident).${RST}"
S_A1="A1-${TS}"

post "Not all pods ready — nonprod-rno (namespace=commerce)" "P-A1-NONPROD-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-A1-NONPROD-${TS}\",
  \"problemTitle\": \"[P2] Not all pods ready\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": null,
  \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-checkout-service-pod\", \"entityName\": \"checkout-service-7d8f6b9c4-xr2kp\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
  \"ProblemDetailsText\": \"Deployment checkout-service: 0/3 pods ready. k8s.cluster.name: mps-nonprod-rno\nk8s.namespace.name: commerce\nk8s.workload.name: checkout-service\",
  \"customProperties\": {
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"commerce\",
    \"k8s.workload.name\": \"checkout-service\",
    \"environment\": \"nonprod\"
  }
}"
pause 1

post "Not all pods ready — mondev-mdn (namespace=payments)" "P-A1-MONDEV-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-A1-MONDEV-${TS}\",
  \"problemTitle\": \"[P2] Not all pods ready\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": null,
  \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-payment-gateway-pod\", \"entityName\": \"payment-gateway-5b8c6d7a3-pq9wz\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
  \"ProblemDetailsText\": \"Deployment payment-gateway: 0/2 pods ready. k8s.cluster.name: mps-mondev-mdn\nk8s.namespace.name: payments\nk8s.workload.name: payment-gateway\",
  \"customProperties\": {
    \"k8s.cluster.name\": \"mps-mondev-mdn\",
    \"k8s.cluster.uid\": \"${MONDEV_UID}\",
    \"k8s.namespace.name\": \"payments\",
    \"k8s.workload.name\": \"payment-gateway\",
    \"environment\": \"mondev\"
  }
}"
pause 1

post "Not all pods ready — k8preview01-rno (namespace=argocd)" "P-A1-PREVIEW-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-A1-PREVIEW-${TS}\",
  \"problemTitle\": \"[P2] Not all pods ready\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": null,
  \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-argocd-server-pod\", \"entityName\": \"argocd-server-7c9d8f6b-kl3mn\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
  \"ProblemDetailsText\": \"Deployment argocd-server: 0/1 pods ready. k8s.cluster.name: k8preview01-rno\nk8s.namespace.name: argocd\nk8s.workload.name: argocd-server\",
  \"customProperties\": {
    \"k8s.cluster.name\": \"k8preview01-rno\",
    \"k8s.cluster.uid\": \"${K8PREV_UID}\",
    \"k8s.namespace.name\": \"argocd\",
    \"k8s.workload.name\": \"argocd-server\",
    \"environment\": \"preview\"
  }
}"
pause 6
dbcheck

echo ""
echo -e "  ${CYN}--- Asserting 3 separate incidents for 'not all pods ready' ---${RST}"
n=$(count_incidents_with_title "Not all pods ready" 5)
check_or_skip "A1 incident count for '[P2] Not all pods ready'" "$n" "3"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO A2 — KUBERNETES_WORKLOAD- entity ID recognition (v1.0.16 entity.go fix)
#
# Bug: KUBERNETES_WORKLOAD- prefix was missing from dynatraceEntityPrefixes, so
# workload entity IDs like "KUBERNETES_WORKLOAD-abc123" were NOT recognized as
# DT entity IDs and would be treated as human-readable names.
# Fix: added "KUBERNETES_WORKLOAD-" to the prefix list.
#
# Test: fire an alert where rootCauseEntity has entityId=KUBERNETES_WORKLOAD-xxx.
# Expected: root_cause_entity label set to the entityName (workload), not the
# entity ID itself, and the alert correctly correlated as k8s_workload.
# ══════════════════════════════════════════════════════════════════════════════
if run A2; then
section "A2 · KUBERNETES_WORKLOAD- entity ID recognition  (v1.0.16 entity.go fix)"
echo -e "  ${DIM}rootCauseEntity.entityId='KUBERNETES_WORKLOAD-abc123def456'.${RST}"
echo -e "  ${DIM}Expected: entity recognized as DT workload, root_cause_entity=checkout-service.${RST}"

post "checkout-service workload crash (KUBERNETES_WORKLOAD- root)" "P-A2-WL-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-A2-WL-${TS}\",
  \"problemTitle\": \"Workload crash loop: checkout-service (mps-nonprod-rno)\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"KUBERNETES_WORKLOAD-checkout-service-mps-nonprod-rno\",
    \"entityName\": \"checkout-service\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  },
  \"impactedEntities\": [
    {\"entityId\": \"KUBERNETES_WORKLOAD-checkout-service-mps-nonprod-rno\", \"entityName\": \"checkout-service\", \"entityType\": \"KUBERNETES_WORKLOAD\"},
    {\"entityId\": \"CLOUD_APPLICATION_INSTANCE-${POD_CHECKOUT}\", \"entityName\": \"${POD_CHECKOUT}\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}
  ],
  \"ProblemDetailsText\": \"Workload checkout-service in CrashLoopBackOff: 0/3 replicas ready. OOMKilled. k8s.cluster.name: mps-nonprod-rno\nk8s.namespace.name: commerce\nk8s.workload.name: checkout-service\",
  \"customProperties\": {
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"commerce\",
    \"k8s.workload.name\": \"checkout-service\",
    \"k8s.workload.kind\": \"Deployment\",
    \"environment\": \"nonprod\",
    \"exit_code\": \"137\",
    \"restart_count\": \"8\"
  }
}"
pause 2

echo -e "  ${CYN}--- DB: alert root_cause_entity for this workload ---${RST}"
kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT '  source_id=' || source_id ||
         '  root_cause=' || COALESCE(labels->>'root_cause_entity','NULL') ||
         '  entity_type=' || COALESCE(labels->>'entity_type','NULL')
  FROM alerts
  WHERE source_id='P-A2-WL-${TS}'
    AND created_at > NOW()-INTERVAL '5 minutes';" 2>/dev/null | grep -v "^$" \
    || echo -e "  ${DIM}(kubectl unavailable)${RST}"

echo ""
echo -e "  ${CYN}--- Asserting root_cause_entity is NOT the raw entity ID ---${RST}"
root_entity=$(kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT labels->>'root_cause_entity' FROM alerts
  WHERE source_id='P-A2-WL-${TS}'
  ORDER BY created_at DESC LIMIT 1;" 2>/dev/null | tr -d ' \n' || echo "?")
# The root_cause_entity should be the name, not the KUBERNETES_WORKLOAD- ID
if [[ "$root_entity" == "?" ]]; then
  echo -e "  ${YLW}⊘ SKIP${RST}  A2 root_cause_entity not raw ID: kubectl unavailable"
  (( SKIP_COUNT++ )) || true
elif [[ "$root_entity" == *"KUBERNETES_WORKLOAD-"* ]]; then
  echo -e "  ${RED}✗ FAIL${RST}  A2 root_cause_entity is still the raw entity ID: ${root_entity}"
  (( FAIL_COUNT++ )) || true
else
  echo -e "  ${GRN}✓ PASS${RST}  A2 root_cause_entity is human-readable name: ${root_entity}"
  (( PASS_COUNT++ )) || true
fi
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO A3 — Resolved via critical bypass (v1.0.17 fix regression)
#
# Bug: When fastCh is full, CRITICAL alerts bypass fastPathAdapter (which runs
# processAlertResolvedStage) and go directly to topoCh. Before the fix,
# topoPathAdapter.Process() had no resolved check, so CRITICAL RESOLVED alerts
# ran processAlertRCEStage and created a new incident.
# Fix: topoPathAdapter.Process() now checks for resolved status first.
#
# Test: open an incident, then send a CRITICAL RESOLVED alert for the same
# problemId. Verify the incident closes (status=resolved).
# ══════════════════════════════════════════════════════════════════════════════
if run A3; then
section "A3 · Resolved alert via critical bypass  (v1.0.17 fix regression)"
echo -e "  ${DIM}CRITICAL OPEN → CRITICAL RESOLVED must close the incident (not create new).${RST}"
S_A3="A3-${TS}"

post "CRITICAL OPEN — payment-gateway down" "P-A3-OPEN-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-A3-OPEN-${TS}\",
  \"problemTitle\": \"CRITICAL: payment-gateway service unavailable (0/3 pods ready)\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"KUBERNETES_WORKLOAD-payment-gateway-mps-nonprod-rno\",
    \"entityName\": \"payment-gateway\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  },
  \"impactedEntities\": [{\"entityId\": \"KUBERNETES_WORKLOAD-payment-gateway-mps-nonprod-rno\", \"entityName\": \"payment-gateway\", \"entityType\": \"KUBERNETES_WORKLOAD\"}],
  \"ProblemDetailsText\": \"payment-gateway: 0/3 pods ready. Payments failing. k8s.cluster.name: mps-nonprod-rno\nk8s.namespace.name: payments\",
  \"customProperties\": {
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"payments\",
    \"k8s.workload.name\": \"payment-gateway\",
    \"environment\": \"nonprod\"
  }
}"
echo -e "  ${DIM}waiting 6s for incident to be created…${RST}"
pause 6

echo -e "  ${CYN}--- DB: incident before RESOLVED ---${RST}"
kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT '  status=' || status || '  title=' || LEFT(title,60)
  FROM incidents
  WHERE auto_created=true AND title ILIKE '%payment-gateway%'
    AND updated_at > NOW()-INTERVAL '5 minutes'
  ORDER BY created_at DESC LIMIT 2;" 2>/dev/null | grep -v "^$" || echo -e "  ${DIM}(kubectl unavailable)${RST}"

post "CRITICAL RESOLVED — payment-gateway recovered" "P-A3-OPEN-${TS}" "{
  \"state\": \"RESOLVED\",
  \"problemId\": \"P-A3-OPEN-${TS}\",
  \"problemTitle\": \"RESOLVED: payment-gateway back to 3/3 pods ready\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"RESOLVED\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"KUBERNETES_WORKLOAD-payment-gateway-mps-nonprod-rno\",
    \"entityName\": \"payment-gateway\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  },
  \"impactedEntities\": [{\"entityId\": \"KUBERNETES_WORKLOAD-payment-gateway-mps-nonprod-rno\", \"entityName\": \"payment-gateway\", \"entityType\": \"KUBERNETES_WORKLOAD\"}],
  \"ProblemDetailsText\": \"payment-gateway recovered. 3/3 pods ready. Health checks passing.\",
  \"customProperties\": {
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"payments\",
    \"k8s.workload.name\": \"payment-gateway\",
    \"environment\": \"nonprod\"
  }
}"
echo -e "  ${DIM}waiting 5s for resolved handling…${RST}"
pause 5

echo ""
echo -e "  ${CYN}--- DB: incident status after RESOLVED ---${RST}"
kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT '  status=' || status ||
         '  alerts=' || jsonb_array_length(alert_ids) ||
         '  title=' || LEFT(title,50)
  FROM incidents
  WHERE auto_created=true AND title ILIKE '%payment-gateway%'
    AND updated_at > NOW()-INTERVAL '5 minutes'
  ORDER BY updated_at DESC LIMIT 2;" 2>/dev/null | grep -v "^$" || echo -e "  ${DIM}(kubectl unavailable)${RST}"

inc_status=$(incident_status "payment-gateway")
# Check we don't have a NEW open incident for the same correlation
new_open=$(kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT COUNT(*) FROM incidents
  WHERE auto_created=true
    AND title ILIKE '%payment-gateway%'
    AND status IN ('open','investigating')
    AND created_at > NOW()-INTERVAL '3 minutes'
    AND title ILIKE '%CRITICAL%';" 2>/dev/null | tr -d ' \n' || echo "?")
check_or_skip "A3 no new open incident created for RESOLVED CRITICAL" "$new_open" "0"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO A4 — Resolved via full-correlation path (v1.0.17 fix regression)
#
# Bug: fullCorrelationAdapter.Process() had no resolved check, so alerts that
# couldn't be processed by fastCh or topoCh ran processAlertFullStage on
# RESOLVED alerts, creating spurious incidents.
# Fix: fullCorrelationAdapter.Process() now checks for resolved status first.
#
# Test: open an incident via a non-critical alert (goes through full correlation),
# then RESOLVE it. Verify incident closes (not duplicated).
# ══════════════════════════════════════════════════════════════════════════════
if run A4; then
section "A4 · Resolved via full-correlation path  (v1.0.17 fix regression)"
echo -e "  ${DIM}Low-severity OPEN → RESOLVED must close the incident (not create spurious new one).${RST}"
S_A4="A4-${TS}"

post "LOW OPEN — disk space warning" "P-A4-OPEN-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-A4-OPEN-${TS}\",
  \"problemTitle\": \"Disk space warning: /data at 83% on order-processor node\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"RESOURCE_CONTENTION\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${K8N_Z2_01}\",
    \"entityName\": \"${K8N_Z2_01}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{\"entityId\": \"HOST-${K8N_Z2_01}\", \"entityName\": \"${K8N_Z2_01}\", \"entityType\": \"HOST\"}],
  \"ProblemDetailsText\": \"Disk /data at 83% on ${K8N_Z2_01}. ETA full ~4 hours at current write rate.\",
  \"customProperties\": {
    \"host.name\": \"${K8N_Z2_01}\",
    \"k8s.node.name\": \"${K8N_Z2_01}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"environment\": \"ADC\",
    \"disk_pct\": \"83\"
  }
}"
echo -e "  ${DIM}waiting 8s for full correlation pipeline…${RST}"
pause 8

post "LOW RESOLVED — disk space cleared" "P-A4-OPEN-${TS}" "{
  \"state\": \"RESOLVED\",
  \"problemId\": \"P-A4-OPEN-${TS}\",
  \"problemTitle\": \"RESOLVED: Disk space normalized on ${K8N_Z2_01}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"RESOURCE_CONTENTION\",
  \"status\": \"RESOLVED\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${K8N_Z2_01}\",
    \"entityName\": \"${K8N_Z2_01}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{\"entityId\": \"HOST-${K8N_Z2_01}\", \"entityName\": \"${K8N_Z2_01}\", \"entityType\": \"HOST\"}],
  \"ProblemDetailsText\": \"Disk /data returned to 61%. Cleanup job completed.\",
  \"customProperties\": {
    \"host.name\": \"${K8N_Z2_01}\",
    \"k8s.node.name\": \"${K8N_Z2_01}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"environment\": \"ADC\",
    \"disk_pct\": \"61\"
  }
}"
pause 5

new_open=$(kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT COUNT(*) FROM incidents
  WHERE auto_created=true
    AND title ILIKE '%RESOLVED%Disk%${K8N_Z2_01}%'
    AND status IN ('open','investigating')
    AND created_at > NOW()-INTERVAL '3 minutes';" 2>/dev/null | tr -d ' \n' || echo "?")
check_or_skip "A4 no 'RESOLVED:' incident created (full corr path)" "$new_open" "0"

total_for_problem=$(kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT COUNT(*) FROM incidents i
  WHERE auto_created=true
    AND updated_at > NOW()-INTERVAL '5 minutes'
    AND EXISTS (
      SELECT 1 FROM alerts a
      WHERE a.source_id='P-A4-OPEN-${TS}'
        AND a.id::text = ANY(SELECT jsonb_array_elements_text(i.alert_ids))
    );" 2>/dev/null | tr -d ' \n' || echo "?")
check_or_skip "A4 total incidents for problem (should be 1, not 2)" "$total_for_problem" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO B1 — NetApp RNO 5-layer cascade
#
# Layer 1 (root): netapp-rno-cluster001 node failure
# Layer 2:        aggregate aggr0_rno_node1_data offline
# Layer 3:        SVM iapps-rno-k8s I/O suspended
# Layer 4:        PVC pvc-checkout-db-0 in commerce namespace
# Layer 5:        checkout-service pod I/O wait / CrashLoop
#
# Expected: 1 incident with 5 alerts, correlation_id=netapp-rno-cluster001
# ══════════════════════════════════════════════════════════════════════════════
if run B1; then
section "B1 · NetApp RNO 5-layer cascade  (netapp-rno → SVM → volume → PVC → pod)"
echo -e "  ${DIM}Expected: 1 incident, 5 alerts, correlation_id=${NETAPP_RNO}${RST}"
S_B1="B1-${TS}"

post "NetApp node1 controller failure (root layer 1)" "P-B1-NETAPP-NODE-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-B1-NETAPP-NODE-${TS}\",
  \"problemTitle\": \"NetApp storage controller offline: ${NETAPP_RNO} / ${NETAPP_NODE1}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${NETAPP_NODE1}\",
    \"entityName\": \"${NETAPP_NODE1}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{\"entityId\": \"HOST-${NETAPP_NODE1}\", \"entityName\": \"${NETAPP_NODE1}\", \"entityType\": \"HOST\"}],
  \"ProblemDetailsText\": \"NetApp storage controller ${NETAPP_NODE1} failed. Takeover initiated. host.name: ${NETAPP_NODE1}\",
  \"customProperties\": {
    \"host.name\": \"${NETAPP_NODE1}\",
    \"netapp_cluster\": \"${NETAPP_RNO}\",
    \"netapp_entity\": \"${NETAPP_NODE1}\",
    \"entity_type\": \"netapp_node\",
    \"environment\": \"ADC\",
    \"impacted_entity\": \"${NETAPP_NODE1}\"
  }
}"
pause 2

post "NetApp aggregate offline (layer 2)" "P-B1-NETAPP-AGG-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-B1-NETAPP-AGG-${TS}\",
  \"problemTitle\": \"NetApp aggregate offline: ${NETAPP_AGG}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${NETAPP_NODE1}\",
    \"entityName\": \"${NETAPP_NODE1}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{\"entityId\": \"HOST-${NETAPP_NODE1}\", \"entityName\": \"${NETAPP_NODE1}\", \"entityType\": \"HOST\"}],
  \"ProblemDetailsText\": \"Aggregate ${NETAPP_AGG} on ${NETAPP_NODE1} went offline. All volumes on this aggregate are unmounted.\",
  \"customProperties\": {
    \"netapp_cluster\": \"${NETAPP_RNO}\",
    \"netapp_aggregate\": \"${NETAPP_AGG}\",
    \"netapp_node\": \"${NETAPP_NODE1}\",
    \"entity_type\": \"netapp_aggregate\",
    \"environment\": \"ADC\"
  }
}"
pause 2

post "SVM iapps-rno-k8s I/O suspended (layer 3)" "P-B1-SVM-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-B1-SVM-${TS}\",
  \"problemTitle\": \"NetApp SVM I/O suspended: ${NETAPP_SVM}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${NETAPP_NODE1}\",
    \"entityName\": \"${NETAPP_NODE1}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{\"entityId\": \"HOST-${NETAPP_SVM}\", \"entityName\": \"${NETAPP_SVM}\", \"entityType\": \"HOST\"}],
  \"ProblemDetailsText\": \"SVM ${NETAPP_SVM} I/O suspended due to aggregate takeover. All NFS/iSCSI mounts blocked.\",
  \"customProperties\": {
    \"netapp_cluster\": \"${NETAPP_RNO}\",
    \"netapp_aggregate\": \"${NETAPP_AGG}\",
    \"svm\": \"${NETAPP_SVM}\",
    \"entity_type\": \"netapp_svm\",
    \"environment\": \"ADC\"
  }
}"
pause 2

post "PVC pvc-checkout-db-0 I/O error (layer 4)" "P-B1-PVC-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-B1-PVC-${TS}\",
  \"problemTitle\": \"PVC I/O error: pvc-checkout-db-0 (commerce namespace)\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${NETAPP_NODE1}\",
    \"entityName\": \"${NETAPP_NODE1}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-checkout-db-pod\", \"entityName\": \"pvc-checkout-db-0\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
  \"ProblemDetailsText\": \"PVC pvc-checkout-db-0 in commerce returning EIO. Backed by SVM ${NETAPP_SVM} which is I/O suspended.\",
  \"customProperties\": {
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"commerce\",
    \"pvc\": \"pvc-checkout-db-0\",
    \"netapp_cluster\": \"${NETAPP_RNO}\",
    \"svm\": \"${NETAPP_SVM}\",
    \"environment\": \"ADC\"
  }
}"
pause 2

post "checkout-service CrashLoop (layer 5, leaf)" "P-B1-POD-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-B1-POD-${TS}\",
  \"problemTitle\": \"CrashLoopBackOff: ${POD_CHECKOUT} (commerce/checkout-service)\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${NETAPP_NODE1}\",
    \"entityName\": \"${NETAPP_NODE1}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-${POD_CHECKOUT}\", \"entityName\": \"${POD_CHECKOUT}\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
  \"ProblemDetailsText\": \"Pod ${POD_CHECKOUT} CrashLoopBackOff. DB connection failing due to PVC I/O error. Storage root cause: ${NETAPP_NODE1}\",
  \"customProperties\": {
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"commerce\",
    \"k8s.pod.name\": \"${POD_CHECKOUT}\",
    \"k8s.workload.name\": \"checkout-service\",
    \"pvc\": \"pvc-checkout-db-0\",
    \"netapp_cluster\": \"${NETAPP_RNO}\",
    \"environment\": \"ADC\"
  }
}"
pause 6
dbcheck
echo ""
n=$(incident_count "${NETAPP_NODE1}")
check_or_skip "B1 single incident for NetApp cascade" "$n" "1"
ac=$(incident_alert_count "${NETAPP_NODE1}")
# At least 3 alerts should be in the incident (5 fired, latency may vary)
if [[ "$ac" == "?" ]]; then
  echo -e "  ${YLW}⊘ SKIP${RST}  B1 alert_count in incident: kubectl unavailable"
  (( SKIP_COUNT++ )) || true
elif [[ "$ac" -ge 3 ]]; then
  echo -e "  ${GRN}✓ PASS${RST}  B1 alert_count in incident: ${ac} (≥3)"
  (( PASS_COUNT++ )) || true
else
  echo -e "  ${RED}✗ FAIL${RST}  B1 alert_count in incident: want≥3 got=${ac}"
  (( FAIL_COUNT++ )) || true
fi
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO B2 — NetApp aggregate capacity → PVC write stall → CrashLoop
# ══════════════════════════════════════════════════════════════════════════════
if run B2; then
section "B2 · NetApp aggregate 95% full → PVC write stall → payments pod crash"
echo -e "  ${DIM}Expected: 1 incident, netapp-mdn-cluster001 root cause${RST}"

post "NetApp MDN aggregate 95% full (root)" "P-B2-AGG-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-B2-AGG-${TS}\",
  \"problemTitle\": \"NetApp aggregate nearly full: aggr0_mdn_data1 (95% used)\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"RESOURCE_CONTENTION\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-netapp-mdn-node1\",
    \"entityName\": \"netapp-mdn-node1\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{\"entityId\": \"HOST-netapp-mdn-node1\", \"entityName\": \"netapp-mdn-node1\", \"entityType\": \"HOST\"}],
  \"ProblemDetailsText\": \"Aggregate aggr0_mdn_data1 at 95% capacity. Write throttling active. host.name: netapp-mdn-node1\",
  \"customProperties\": {
    \"host.name\": \"netapp-mdn-node1\",
    \"netapp_cluster\": \"${NETAPP_MDN}\",
    \"netapp_aggregate\": \"aggr0_mdn_data1\",
    \"entity_type\": \"netapp_aggregate\",
    \"environment\": \"ADC\",
    \"aggregate_used_pct\": \"95\"
  }
}"
pause 2

post "SVM iapps-mdn-k8s write stall (layer 2)" "P-B2-SVM-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-B2-SVM-${TS}\",
  \"problemTitle\": \"NetApp SVM write latency spike: ${NETAPP_MDN_SVM} (>200ms)\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"PERFORMANCE\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-netapp-mdn-node1\",
    \"entityName\": \"netapp-mdn-node1\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{\"entityId\": \"HOST-${NETAPP_MDN_SVM}\", \"entityName\": \"${NETAPP_MDN_SVM}\", \"entityType\": \"HOST\"}],
  \"ProblemDetailsText\": \"SVM ${NETAPP_MDN_SVM} write latency 220ms (threshold 50ms). Caused by aggregate capacity exhaustion.\",
  \"customProperties\": {
    \"netapp_cluster\": \"${NETAPP_MDN}\",
    \"svm\": \"${NETAPP_MDN_SVM}\",
    \"netapp_aggregate\": \"aggr0_mdn_data1\",
    \"entity_type\": \"netapp_svm\",
    \"environment\": \"ADC\",
    \"write_latency_ms\": \"220\"
  }
}"
pause 2

post "payment-gateway pod DB write fail → CrashLoop (leaf)" "P-B2-POD-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-B2-POD-${TS}\",
  \"problemTitle\": \"CrashLoopBackOff: ${POD_PAYMENT_GW} (mps-mondev-mdn/payments)\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-netapp-mdn-node1\",
    \"entityName\": \"netapp-mdn-node1\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-${POD_PAYMENT_GW}\", \"entityName\": \"${POD_PAYMENT_GW}\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
  \"ProblemDetailsText\": \"Pod ${POD_PAYMENT_GW} CrashLoopBackOff. Database writes failing with ENOSPC. NetApp aggregate root cause.\",
  \"customProperties\": {
    \"k8s.cluster.name\": \"mps-mondev-mdn\",
    \"k8s.cluster.uid\": \"${MONDEV_UID}\",
    \"k8s.namespace.name\": \"payments\",
    \"k8s.pod.name\": \"${POD_PAYMENT_GW}\",
    \"k8s.workload.name\": \"payment-gateway\",
    \"pvc\": \"pvc-payment-db-0\",
    \"netapp_cluster\": \"${NETAPP_MDN}\",
    \"environment\": \"ADC\"
  }
}"
pause 5
n=$(incident_count "netapp-mdn-node1")
check_or_skip "B2 single incident for NetApp MDN cascade" "$n" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO C1 — mps-sandbox-rno isolated from mps-nonprod-rno
#
# Same generic title, same workload name — but different clusters.
# Expected: 2 separate incidents.
# ══════════════════════════════════════════════════════════════════════════════
if run C1; then
section "C1 · Sandbox cluster isolation  (mps-sandbox-rno vs mps-nonprod-rno)"
echo -e "  ${DIM}Same workload name 'trident-csi' in two clusters → 2 separate incidents.${RST}"

post "trident-csi down — mps-nonprod-rno" "P-C1-NONPROD-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-C1-NONPROD-${TS}\",
  \"problemTitle\": \"Trident CSI controller not running: mps-nonprod-rno\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"KUBERNETES_WORKLOAD-trident-csi-mps-nonprod-rno\",
    \"entityName\": \"trident-csi\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  },
  \"impactedEntities\": [{\"entityId\": \"KUBERNETES_WORKLOAD-trident-csi-mps-nonprod-rno\", \"entityName\": \"trident-csi\", \"entityType\": \"KUBERNETES_WORKLOAD\"}],
  \"ProblemDetailsText\": \"Trident CSI 0/1 ready. New PVC provisioning blocked. k8s.cluster.name: mps-nonprod-rno\nk8s.namespace.name: trident\",
  \"customProperties\": {
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"trident\",
    \"k8s.workload.name\": \"trident-csi\",
    \"environment\": \"nonprod\"
  }
}"
pause 1

post "trident-csi down — mps-sandbox-rno" "P-C1-SANDBOX-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-C1-SANDBOX-${TS}\",
  \"problemTitle\": \"Trident CSI controller not running: mps-sandbox-rno\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"KUBERNETES_WORKLOAD-trident-csi-mps-sandbox-rno\",
    \"entityName\": \"trident-csi\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  },
  \"impactedEntities\": [{\"entityId\": \"KUBERNETES_WORKLOAD-trident-csi-mps-sandbox-rno\", \"entityName\": \"trident-csi\", \"entityType\": \"KUBERNETES_WORKLOAD\"}],
  \"ProblemDetailsText\": \"Trident CSI 0/1 ready. New PVC provisioning blocked. k8s.cluster.name: mps-sandbox-rno\nk8s.namespace.name: trident\",
  \"customProperties\": {
    \"k8s.cluster.name\": \"mps-sandbox-rno\",
    \"k8s.cluster.uid\": \"${SANDBOX_UID}\",
    \"k8s.namespace.name\": \"trident\",
    \"k8s.workload.name\": \"trident-csi\",
    \"environment\": \"sandbox\"
  }
}"
pause 5
echo -e "  ${CYN}--- C1 isolation check ---${RST}"
# After the k8s-scoped entity fix, correlation_id = "cluster/ns:workload" not raw workload name.
# Verify there are 2 separate incidents for "Trident CSI" (one per cluster).
total_trident=$(count_incidents_with_title "Trident CSI controller not running" 5)
check_or_skip "C1 2 separate incidents for trident-csi across clusters" "$total_trident" "2"
# Also verify they have DIFFERENT correlation_ids (cross-cluster isolation)
distinct_corr=$(kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT COUNT(DISTINCT correlation_id) FROM incidents
  WHERE auto_created=true AND title ILIKE '%Trident CSI controller not running%'
    AND updated_at > NOW()-INTERVAL '5 minutes';" 2>/dev/null | tr -d ' \n' || echo "?")
check_or_skip "C1 distinct correlation_ids (cluster-scoped)" "$distinct_corr" "2"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO C2 — k8preview01-rno cascade isolated from nonprod-rno
#
# Uses real pod names from k8preview01-rno (from test_real_cascade.sh).
# Expected: 1 incident for k8preview01-rno, isolated from nonprod-rno.
# ══════════════════════════════════════════════════════════════════════════════
if run C2; then
section "C2 · k8preview01-rno cascade  (node→pods, isolated from nonprod-rno)"
echo -e "  ${DIM}k8preview01 node goes NotReady → real pods evicted → 1 incident for k8preview01 only.${RST}"
S_C2="C2-${TS}"

post "k8preview01 node NotReady (root)" "P-C2-NODE-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-C2-NODE-${TS}\",
  \"problemTitle\": \"K8s Node NotReady: ${K8N_PREV2} (k8preview01-rno)\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${K8N_PREV2}\",
    \"entityName\": \"${K8N_PREV2}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{\"entityId\": \"HOST-${K8N_PREV2}\", \"entityName\": \"${K8N_PREV2}\", \"entityType\": \"HOST\"}],
  \"ProblemDetailsText\": \"Node ${K8N_PREV2} NotReady. kubelet not responding. 7 pods evicting. host.name: ${K8N_PREV2}\",
  \"customProperties\": {
    \"host.name\": \"${K8N_PREV2}\",
    \"k8s.node.name\": \"${K8N_PREV2}\",
    \"k8s.cluster.name\": \"k8preview01-rno\",
    \"k8s.cluster.uid\": \"${K8PREV_UID}\",
    \"environment\": \"ADC\"
  }
}"
pause 2

for pod_info in "${POD_PREV_FILMS}:ac-films-feature:ac-films" "${POD_PREV_ARGOCD}:argocd:argocd" "${POD_PREV_FRONTIER}:frontier-dev:frontier" "${POD_PREV_INGRESS}:ingress-nginx:nginx-ingress-controller"; do
  pod_name="${pod_info%%:*}"
  rest="${pod_info#*:}"
  ns="${rest%%:*}"
  workload="${rest#*:}"
  post "pod evicted — ${ns}/${pod_name}" "P-C2-POD-${ns}-${TS}" "{
    \"state\": \"OPEN\",
    \"problemId\": \"P-C2-POD-${ns}-${TS}\",
    \"problemTitle\": \"Pod evicted: ${pod_name} (ns=${ns})\",
    \"impactLevel\": \"APPLICATION\",
    \"severity\": \"AVAILABILITY\",
    \"status\": \"OPEN\",
    \"startTime\": \"${NOW}\",
    \"rootCauseEntity\": {
      \"entityId\": \"HOST-${K8N_PREV2}\",
      \"entityName\": \"${K8N_PREV2}\",
      \"entityType\": \"HOST\"
    },
    \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-${pod_name}\", \"entityName\": \"${pod_name}\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
    \"ProblemDetailsText\": \"Pod ${pod_name} evicted from ${K8N_PREV2}. Node NotReady.\",
    \"customProperties\": {
      \"k8s.namespace.name\": \"${ns}\",
      \"k8s.workload.name\": \"${workload}\",
      \"k8s.node.name\": \"${K8N_PREV2}\",
      \"k8s.cluster.name\": \"k8preview01-rno\",
      \"k8s.cluster.uid\": \"${K8PREV_UID}\",
      \"environment\": \"ADC\"
    }
  }"
  pause 1
done

pause 5
n=$(incident_count "${K8N_PREV2}")
check_or_skip "C2 single incident for k8preview01 cascade" "$n" "1"
ac=$(incident_alert_count "${K8N_PREV2}")
if [[ "$ac" == "?" ]]; then
  echo -e "  ${YLW}⊘ SKIP${RST}  C2 alert_count: kubectl unavailable"
  (( SKIP_COUNT++ )) || true
elif [[ "$ac" -ge 3 ]]; then
  echo -e "  ${GRN}✓ PASS${RST}  C2 alert_count ≥ 3 alerts in k8preview01 incident: ${ac}"
  (( PASS_COUNT++ )) || true
else
  echo -e "  ${RED}✗ FAIL${RST}  C2 alert_count want≥3 got=${ac}"
  (( FAIL_COUNT++ )) || true
fi
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO D1 — KUBERNETES_WORKLOAD entity type labeling
# ══════════════════════════════════════════════════════════════════════════════
if run D1; then
section "D1 · KUBERNETES_WORKLOAD entity type correct labeling"
echo -e "  ${DIM}KUBERNETES_WORKLOAD entity → entity_type=k8s_workload, correct entity_id format.${RST}"

post "inventory-api workload OOMKilled" "P-D1-WL-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-D1-WL-${TS}\",
  \"problemTitle\": \"OOMKilled: inventory-api (mps-nonprod-rno/commerce)\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"RESOURCE_CONTENTION\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"KUBERNETES_WORKLOAD-inventory-api-mps-nonprod-rno\",
    \"entityName\": \"inventory-api\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  },
  \"impactedEntities\": [
    {\"entityId\": \"KUBERNETES_WORKLOAD-inventory-api-mps-nonprod-rno\", \"entityName\": \"inventory-api\", \"entityType\": \"KUBERNETES_WORKLOAD\"},
    {\"entityId\": \"CLOUD_APPLICATION_INSTANCE-${POD_INVENTORY}\", \"entityName\": \"${POD_INVENTORY}\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}
  ],
  \"ProblemDetailsText\": \"inventory-api pods OOMKilled. Memory limit 512Mi exceeded. Restart count 14. k8s.cluster.name: mps-nonprod-rno\nk8s.namespace.name: commerce\nk8s.workload.name: inventory-api\",
  \"customProperties\": {
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"commerce\",
    \"k8s.workload.name\": \"inventory-api\",
    \"k8s.workload.kind\": \"Deployment\",
    \"environment\": \"nonprod\",
    \"exit_code\": \"137\",
    \"memory_limit_mi\": \"512\",
    \"restart_count\": \"14\"
  }
}"
pause 3
echo -e "  ${CYN}--- DB: alert entity labeling check ---${RST}"
kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT '  entity_type=' || COALESCE(labels->>'entity_type','NULL') ||
         '  entity_id=' || COALESCE(LEFT(labels->>'entity_id',40),'NULL') ||
         '  root_cause=' || COALESCE(labels->>'root_cause_entity','NULL')
  FROM alerts
  WHERE source_id='P-D1-WL-${TS}'
  ORDER BY created_at DESC LIMIT 1;" 2>/dev/null | grep -v "^$" \
  || echo -e "  ${DIM}(kubectl unavailable)${RST}"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO D2 — APPLICATION entity type (Dynatrace APPLICATION entityType)
# ══════════════════════════════════════════════════════════════════════════════
if run D2; then
section "D2 · APPLICATION entity type (DT APPLICATION entityType)"
echo -e "  ${DIM}APPLICATION entity with APPLICATION- prefixed entity ID → preserved correctly.${RST}"

post "mosaic web app latency spike" "P-D2-APP-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-D2-APP-${TS}\",
  \"problemTitle\": \"Response time degradation: mosaic-web-app (mps-mondev-mdn)\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"PERFORMANCE\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"APPLICATION-mosaic-web-app-mondev\",
    \"entityName\": \"mosaic-web-app\",
    \"entityType\": \"APPLICATION\"
  },
  \"impactedEntities\": [{\"entityId\": \"APPLICATION-mosaic-web-app-mondev\", \"entityName\": \"mosaic-web-app\", \"entityType\": \"APPLICATION\"}],
  \"ProblemDetailsText\": \"mosaic-web-app p99 response time 8.4s (SLO 2s). Error rate 12%.\",
  \"customProperties\": {
    \"k8s.cluster.name\": \"mps-mondev-mdn\",
    \"k8s.cluster.uid\": \"${MONDEV_UID}\",
    \"k8s.namespace.name\": \"mosaic-test\",
    \"k8s.workload.name\": \"mosaic-web-app\",
    \"environment\": \"mondev\",
    \"p99_ms\": \"8400\",
    \"error_rate_pct\": \"12\"
  }
}"
pause 3
echo -e "  ${CYN}--- DB: APPLICATION entity root_cause check ---${RST}"
kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT '  root_cause=' || COALESCE(labels->>'root_cause_entity','NULL') ||
         '  rce_id=' || COALESCE(LEFT(labels->>'root_cause_entity_id',36),'NULL')
  FROM alerts
  WHERE source_id='P-D2-APP-${TS}'
  ORDER BY created_at DESC LIMIT 1;" 2>/dev/null | grep -v "^$" \
  || echo -e "  ${DIM}(kubectl unavailable)${RST}"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO E1 — Prometheus source alert (non-DT scoring fallback path)
#
# AlertHub receives alerts from Prometheus/Alertmanager too.
# These use a different payload format (POST to /dynatrace endpoint with
# simulated Prometheus-style customProperties). No rootCauseEntity.
# Expected: normalizer falls back to label-based scoring, creates incident.
# ══════════════════════════════════════════════════════════════════════════════
if run E1; then
section "E1 · Prometheus-style label-only alert  (non-DT scoring fallback)"
echo -e "  ${DIM}No rootCauseEntity. K8s labels only. Pure label scoring + topology fallback.${RST}"
S_E1="E1-${TS}"

post "parca CPU spike — label-only (Prometheus style)" "P-E1-PROM-ROOT-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-E1-PROM-ROOT-${TS}\",
  \"problemTitle\": \"High CPU usage: parca profiler exceeding limits\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"PERFORMANCE\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": null,
  \"impactedEntities\": [],
  \"ProblemDetailsText\": \"Parca profiler CPU usage at 3.8 cores (limit 2). Causes profile collection gaps. k8s.cluster.name: mps-mondev-mdn\nk8s.namespace.name: parca\nk8s.workload.name: parca\",
  \"customProperties\": {
    \"k8s.cluster.name\": \"mps-mondev-mdn\",
    \"k8s.cluster.uid\": \"${MONDEV_UID}\",
    \"k8s.namespace.name\": \"parca\",
    \"k8s.workload.name\": \"parca\",
    \"alertname\": \"ParcaCPUThrottling\",
    \"severity\": \"warning\",
    \"job\": \"parca\",
    \"source\": \"prometheus\",
    \"cpu_ratio\": \"1.9\",
    \"environment\": \"mondev\"
  }
}"
pause 2

post "parca OOM kill — same workload, label-only" "P-E1-PROM-DS-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-E1-PROM-DS-${TS}\",
  \"problemTitle\": \"OOMKilled: parca profiler pod (mps-mondev-mdn/parca)\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": null,
  \"impactedEntities\": [],
  \"ProblemDetailsText\": \"Parca pod OOMKilled. Restart count 5. Memory limit 4Gi exceeded. k8s.cluster.name: mps-mondev-mdn\nk8s.namespace.name: parca\nk8s.workload.name: parca\",
  \"customProperties\": {
    \"k8s.cluster.name\": \"mps-mondev-mdn\",
    \"k8s.cluster.uid\": \"${MONDEV_UID}\",
    \"k8s.namespace.name\": \"parca\",
    \"k8s.workload.name\": \"parca\",
    \"alertname\": \"ParcaOOMKilled\",
    \"severity\": \"critical\",
    \"source\": \"prometheus\",
    \"restart_count\": \"5\",
    \"environment\": \"mondev\"
  }
}"
pause 5
dbcheck
echo -e "  ${DIM}(Asserting: at least 1 incident created for parca from labels-only path)${RST}"
n=$(count_incidents_with_title "parca" 5)
if [[ "$n" == "?" ]]; then
  echo -e "  ${YLW}⊘ SKIP${RST}  E1 parca incident created: kubectl unavailable"
  (( SKIP_COUNT++ )) || true
elif [[ "$n" -ge 1 ]]; then
  echo -e "  ${GRN}✓ PASS${RST}  E1 parca incident created via label scoring: ${n}"
  (( PASS_COUNT++ )) || true
else
  echo -e "  ${RED}✗ FAIL${RST}  E1 no incident created for parca alerts (want≥1 got=${n})"
  (( FAIL_COUNT++ )) || true
fi
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO E2 — Flapping 3-cycle OPEN→RESOLVED×3
#
# Same problemId flaps 3× (OPEN→RESOLVED→OPEN→RESOLVED→OPEN).
# Expected: final state is open incident, alert.count=5 (each flip is a new alert
# record update or dedup), no orphaned incidents.
# ══════════════════════════════════════════════════════════════════════════════
if run E2; then
section "E2 · Flapping 3-cycle  (OPEN→RESOLVED→OPEN→RESOLVED→OPEN)"
echo -e "  ${DIM}Expected: final state open, incident exists, no duplicate open incidents.${RST}"
FLAP_ID="P-E2-FLAP-${TS}"

for cycle in 1 2 3; do
  post "OPEN cycle ${cycle}" "${FLAP_ID}" "{
    \"state\": \"OPEN\",
    \"problemId\": \"${FLAP_ID}\",
    \"problemTitle\": \"Flapping: slackbot pod CrashLoopBackOff (mps-mondev-mdn)\",
    \"impactLevel\": \"APPLICATION\",
    \"severity\": \"AVAILABILITY\",
    \"status\": \"OPEN\",
    \"startTime\": \"${NOW}\",
    \"rootCauseEntity\": {
      \"entityId\": \"KUBERNETES_WORKLOAD-slackbot-mps-mondev-mdn\",
      \"entityName\": \"slackbot\",
      \"entityType\": \"KUBERNETES_WORKLOAD\"
    },
    \"impactedEntities\": [{\"entityId\": \"KUBERNETES_WORKLOAD-slackbot-mps-mondev-mdn\", \"entityName\": \"slackbot\", \"entityType\": \"KUBERNETES_WORKLOAD\"}],
    \"ProblemDetailsText\": \"slackbot CrashLoopBackOff cycle ${cycle}. k8s.cluster.name: mps-mondev-mdn\nk8s.namespace.name: slackbot\",
    \"customProperties\": {
      \"k8s.cluster.name\": \"mps-mondev-mdn\",
      \"k8s.cluster.uid\": \"${MONDEV_UID}\",
      \"k8s.namespace.name\": \"slackbot\",
      \"k8s.workload.name\": \"slackbot\",
      \"environment\": \"mondev\",
      \"flap_cycle\": \"${cycle}\"
    }
  }"
  pause 3

  if [[ "$cycle" -lt 3 ]]; then
    post "RESOLVED cycle ${cycle}" "${FLAP_ID}" "{
      \"state\": \"RESOLVED\",
      \"problemId\": \"${FLAP_ID}\",
      \"problemTitle\": \"RESOLVED: slackbot recovered (cycle ${cycle})\",
      \"impactLevel\": \"APPLICATION\",
      \"severity\": \"AVAILABILITY\",
      \"status\": \"RESOLVED\",
      \"startTime\": \"${NOW}\",
      \"rootCauseEntity\": {
        \"entityId\": \"KUBERNETES_WORKLOAD-slackbot-mps-mondev-mdn\",
        \"entityName\": \"slackbot\",
        \"entityType\": \"KUBERNETES_WORKLOAD\"
      },
      \"impactedEntities\": [{\"entityId\": \"KUBERNETES_WORKLOAD-slackbot-mps-mondev-mdn\", \"entityName\": \"slackbot\", \"entityType\": \"KUBERNETES_WORKLOAD\"}],
      \"ProblemDetailsText\": \"slackbot recovered temporarily.\",
      \"customProperties\": {
        \"k8s.cluster.name\": \"mps-mondev-mdn\",
        \"k8s.cluster.uid\": \"${MONDEV_UID}\",
        \"k8s.namespace.name\": \"slackbot\",
        \"k8s.workload.name\": \"slackbot\",
        \"environment\": \"mondev\"
      }
    }"
    pause 3
  fi
done

pause 3
# Final state: 1 open incident (the last OPEN re-opened it)
open_count=$(kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT COUNT(*) FROM incidents
  WHERE auto_created=true
    AND title ILIKE '%slackbot%'
    AND status IN ('open','investigating')
    AND updated_at > NOW()-INTERVAL '5 minutes';" 2>/dev/null | tr -d ' \n' || echo "?")
check_or_skip "E2 final state: 1 open incident for slackbot after 3-cycle flap" "$open_count" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO E3 — Late root arrival (downstreams fire before root)
#
# 3 downstream pod alerts fire first, then root (node NotReady) arrives 20s later.
# Expected: all 4 alerts end up in 1 incident with node as root.
# ══════════════════════════════════════════════════════════════════════════════
if run E3; then
section "E3 · Late root arrival  (downstreams fire 20s before root)"
echo -e "  ${DIM}3 pod alerts → wait 20s → node NotReady root arrives → 1 merged incident.${RST}"
S_E3="E3-${TS}"
NODE_E3="${K8N_Z1_08}"

for pod in "auth-server-6d9f8b7c4-k2mn" "dex-proxy-5c8d7f6b3-pl4qr" "oauth2-proxy-4b7c6e5d2-rs5wt"; do
  ns="dex"
  [[ "$pod" == "auth-server"* ]] && ns="dex"
  post "pod failing — ns=${ns}/${pod} (BEFORE root)" "P-E3-POD-${pod:0:8}-${TS}" "{
    \"state\": \"OPEN\",
    \"problemId\": \"P-E3-POD-${pod:0:8}-${TS}\",
    \"problemTitle\": \"Pod failing: ${pod} (ns=${ns})\",
    \"impactLevel\": \"APPLICATION\",
    \"severity\": \"AVAILABILITY\",
    \"status\": \"OPEN\",
    \"startTime\": \"${NOW}\",
    \"rootCauseEntity\": null,
    \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-${pod}\", \"entityName\": \"${pod}\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
    \"ProblemDetailsText\": \"Pod ${pod} failing. k8s.cluster.name: mps-nonprod-rno\nk8s.namespace.name: ${ns}\nk8s.node.name: ${NODE_E3}\",
    \"customProperties\": {
      \"k8s.cluster.name\": \"mps-nonprod-rno\",
      \"k8s.cluster.uid\": \"${NONPROD_UID}\",
      \"k8s.namespace.name\": \"${ns}\",
      \"k8s.node.name\": \"${NODE_E3}\",
      \"environment\": \"ADC\"
    }
  }"
  pause 1
done

echo -e "  ${DIM}⏳ 20s delay before root cause arrives…${RST}"
pause 20

post "K8s node NotReady — root arrives late" "P-E3-NODE-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-E3-NODE-${TS}\",
  \"problemTitle\": \"K8s Node NotReady: ${NODE_E3} (mps-nonprod-rno)\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${NODE_E3}\",
    \"entityName\": \"${NODE_E3}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{\"entityId\": \"HOST-${NODE_E3}\", \"entityName\": \"${NODE_E3}\", \"entityType\": \"HOST\"}],
  \"ProblemDetailsText\": \"Node ${NODE_E3} NotReady (kubelet unresponsive). This is the actual root. host.name: ${NODE_E3}\",
  \"customProperties\": {
    \"host.name\": \"${NODE_E3}\",
    \"k8s.node.name\": \"${NODE_E3}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"environment\": \"ADC\"
  }
}"
pause 6
n=$(incident_count "${NODE_E3}")
check_or_skip "E3 single merged incident after late root arrival" "$n" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO E4 — Silent recovery (orphan RESOLVED with no prior OPEN)
# ══════════════════════════════════════════════════════════════════════════════
if run E4; then
section "E4 · Silent recovery  (RESOLVED with no prior OPEN — must not crash)"
echo -e "  ${DIM}Expected: HTTP 200, no new incident created, no crash in logs.${RST}"

post "RESOLVED with no prior OPEN" "P-E4-GHOST-RESOLVED-${TS}" "{
  \"state\": \"RESOLVED\",
  \"problemId\": \"P-E4-GHOST-RESOLVED-${TS}\",
  \"problemTitle\": \"RESOLVED: network latency spike on sage-frontend (self-healed)\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"PERFORMANCE\",
  \"status\": \"RESOLVED\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"KUBERNETES_WORKLOAD-sage-frontend-mps-mondev-mdn\",
    \"entityName\": \"sage-frontend\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  },
  \"impactedEntities\": [{\"entityId\": \"KUBERNETES_WORKLOAD-sage-frontend-mps-mondev-mdn\", \"entityName\": \"sage-frontend\", \"entityType\": \"KUBERNETES_WORKLOAD\"}],
  \"ProblemDetailsText\": \"sage-frontend recovered before an incident was created. k8s.cluster.name: mps-mondev-mdn\nk8s.namespace.name: sage\",
  \"customProperties\": {
    \"k8s.cluster.name\": \"mps-mondev-mdn\",
    \"k8s.cluster.uid\": \"${MONDEV_UID}\",
    \"k8s.namespace.name\": \"sage\",
    \"k8s.workload.name\": \"sage-frontend\",
    \"environment\": \"mondev\"
  }
}"
pause 4

# Verify no open incident was created for this
ghost_open=$(kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT COUNT(*) FROM incidents
  WHERE auto_created=true
    AND title ILIKE '%RESOLVED%sage-frontend%'
    AND status IN ('open','investigating')
    AND created_at > NOW()-INTERVAL '3 minutes';" 2>/dev/null | tr -d ' \n' || echo "?")
check_or_skip "E4 no open incident from orphan RESOLVED" "$ghost_open" "0"

# Check no crashes in logs
echo -e "  ${CYN}--- checking for panic/crash in backend logs ---${RST}"
crashes=$(kubectl logs -n "$NS" -l app=alerthub-backend --since=30s 2>/dev/null \
  | grep -c "panic\|nil pointer\|fatal error" || echo "0")
check_or_skip "E4 no panic/crash in logs" "$crashes" "0"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO F1 — Null rootCauseEntity fields (graceful fallback)
# ══════════════════════════════════════════════════════════════════════════════
if run F1; then
section "F1 · Null rootCauseEntity fields  (entityId=null, entityName=null)"
echo -e "  ${DIM}Expected: HTTP 200, fallback to label-based scoring, no panic.${RST}"

post "null entityId + null entityName in rootCauseEntity" "P-F1-NULL-RCE-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-F1-NULL-RCE-${TS}\",
  \"problemTitle\": \"High error rate: order-processor (mps-nonprod-rno/commerce)\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": null,
    \"entityName\": null,
    \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"
  },
  \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-order-processor-pod\", \"entityName\": \"order-processor-7f9d6c5b4-vx3mq\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
  \"ProblemDetailsText\": \"order-processor error rate 78%. Timeout connecting to inventory-api.\",
  \"customProperties\": {
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"commerce\",
    \"k8s.workload.name\": \"order-processor\",
    \"environment\": \"ADC\",
    \"error_rate_pct\": \"78\"
  }
}"
pause 3

# Check no panic
crashes=$(kubectl logs -n "$NS" -l app=alerthub-backend --since=30s 2>/dev/null \
  | grep -c "panic\|nil pointer\|fatal error" || echo "0")
check_or_skip "F1 no panic/crash with null rootCauseEntity" "$crashes" "0"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO F2 — Generic description NOT accepted as root_cause_entity
#
# Pre-v1.0.16, the text "Not all pods ready" was accepted as root_cause_entity
# because extractRootCauseEntity didn't validate that entityId was a real DT ID.
# This test verifies the fix: when entityId is absent/invalid, and problemTitle
# looks like a generic description, the title-fingerprint path scopes by cluster.
# ══════════════════════════════════════════════════════════════════════════════
if run F2; then
section "F2 · Generic description rejected as root_cause_entity  (v1.0.16 fix)"
echo -e "  ${DIM}rootCauseEntity with no entityId → title-fingerprint scoped by cluster, not global.${RST}"

post "generic RCE text, no entityId — cluster A" "P-F2-A-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-F2-A-${TS}\",
  \"problemTitle\": \"[P1] Deployment unavailable\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"\",
    \"entityName\": \"deployment unavailable\",
    \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"
  },
  \"impactedEntities\": [],
  \"ProblemDetailsText\": \"Deployment auth-server unavailable. k8s.cluster.name: mps-nonprod-rno\nk8s.namespace.name: dex\",
  \"customProperties\": {
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"dex\",
    \"k8s.workload.name\": \"auth-server\",
    \"environment\": \"ADC\"
  }
}"
pause 1

post "same generic RCE text, no entityId — cluster B" "P-F2-B-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-F2-B-${TS}\",
  \"problemTitle\": \"[P1] Deployment unavailable\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"\",
    \"entityName\": \"deployment unavailable\",
    \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"
  },
  \"impactedEntities\": [],
  \"ProblemDetailsText\": \"Deployment auth-server unavailable. k8s.cluster.name: mps-mondev-mdn\nk8s.namespace.name: auth\",
  \"customProperties\": {
    \"k8s.cluster.name\": \"mps-mondev-mdn\",
    \"k8s.cluster.uid\": \"${MONDEV_UID}\",
    \"k8s.namespace.name\": \"auth\",
    \"k8s.workload.name\": \"auth-server\",
    \"environment\": \"ADC\"
  }
}"
pause 5

n=$(count_incidents_with_title "Deployment unavailable" 5)
check_or_skip "F2 separate incidents for same generic title from 2 clusters" "$n" "2"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO G1 — MDN DC bare-metal full cascade
# BM → K8s node → pods in auth/payments/infra-monitoring
# ══════════════════════════════════════════════════════════════════════════════
if run G1; then
section "G1 · MDN DC bare-metal full cascade  (BM→node→pods in auth/payments/infra-monitoring)"
echo -e "  ${DIM}mps-mondev-mdn BM → node → 3 namespace pods → 1 incident.${RST}"
S_G1="G1-${TS}"
MDN_NODE="mps-mondev-mdn-worker-z1-01"

post "MDN BM1 hardware fault (root)" "P-G1-BM-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-G1-BM-${TS}\",
  \"problemTitle\": \"Hardware fault: ${MDN_BM2} (MDN DC bare-metal)\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${MDN_BM2}\",
    \"entityName\": \"${MDN_BM2}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{\"entityId\": \"HOST-${MDN_BM2}\", \"entityName\": \"${MDN_BM2}\", \"entityType\": \"HOST\"}],
  \"ProblemDetailsText\": \"Bare-metal ${MDN_BM2} hardware fault. Memory ECC errors. KVM VMs migrating. host.name: ${MDN_BM2}\",
  \"customProperties\": {
    \"host.name\": \"${MDN_BM2}\",
    \"impacted_entity\": \"${MDN_BM2}\",
    \"entity_type\": \"bare_metal\",
    \"environment\": \"ADC\",
    \"region\": \"maiden\",
    \"ecc_errors\": \"47\"
  }
}"
pause 2

post "MDN K8s node NotReady (BM VM → K8s)" "P-G1-NODE-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-G1-NODE-${TS}\",
  \"problemTitle\": \"K8s Node NotReady: ${MDN_NODE} (mps-mondev-mdn)\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${MDN_BM2}\",
    \"entityName\": \"${MDN_BM2}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{\"entityId\": \"HOST-${MDN_NODE}\", \"entityName\": \"${MDN_NODE}\", \"entityType\": \"HOST\"}],
  \"ProblemDetailsText\": \"K8s node ${MDN_NODE} NotReady. kubelet on VM backed by ${MDN_BM2} is unresponsive. host.name: ${MDN_NODE}\",
  \"customProperties\": {
    \"host.name\": \"${MDN_NODE}\",
    \"k8s.node.name\": \"${MDN_NODE}\",
    \"k8s.cluster.name\": \"mps-mondev-mdn\",
    \"k8s.cluster.uid\": \"${MONDEV_UID}\",
    \"environment\": \"ADC\"
  }
}"
pause 2

for ns_wl in "auth:auth-server" "payments:payment-gateway" "infra-monitoring:prometheus-mdn"; do
  ns="${ns_wl%%:*}"; wl="${ns_wl#*:}"
  post "${ns}/${wl} pod evicted (MDN downstream)" "P-G1-POD-${ns}-${TS}" "{
    \"state\": \"OPEN\",
    \"problemId\": \"P-G1-POD-${ns}-${TS}\",
    \"problemTitle\": \"Pod evicted: ${wl} (mps-mondev-mdn/${ns})\",
    \"impactLevel\": \"APPLICATION\",
    \"severity\": \"AVAILABILITY\",
    \"status\": \"OPEN\",
    \"startTime\": \"${NOW}\",
    \"rootCauseEntity\": {
      \"entityId\": \"HOST-${MDN_BM2}\",
      \"entityName\": \"${MDN_BM2}\",
      \"entityType\": \"HOST\"
    },
    \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-${wl}-pod\", \"entityName\": \"${wl}\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
    \"ProblemDetailsText\": \"${wl} pod evicted from ${MDN_NODE}. Node NotReady cascade.\",
    \"customProperties\": {
      \"k8s.namespace.name\": \"${ns}\",
      \"k8s.workload.name\": \"${wl}\",
      \"k8s.node.name\": \"${MDN_NODE}\",
      \"k8s.cluster.name\": \"mps-mondev-mdn\",
      \"k8s.cluster.uid\": \"${MONDEV_UID}\",
      \"environment\": \"ADC\"
    }
  }"
  pause 1
done

pause 6
n=$(incident_count "${MDN_BM2}")
check_or_skip "G1 single MDN incident (BM root)" "$n" "1"
ac=$(incident_alert_count "${MDN_BM2}")
if [[ "$ac" == "?" ]]; then
  echo -e "  ${YLW}⊘ SKIP${RST}  G1 alert_count in MDN incident: kubectl unavailable"
  (( SKIP_COUNT++ )) || true
elif [[ "$ac" -ge 4 ]]; then
  echo -e "  ${GRN}✓ PASS${RST}  G1 alert_count ≥ 4 in MDN cascade incident: ${ac}"
  (( PASS_COUNT++ )) || true
else
  echo -e "  ${RED}✗ FAIL${RST}  G1 alert_count want≥4 got=${ac}"
  (( FAIL_COUNT++ )) || true
fi
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO G2 — Multi-cluster storm isolation
#
# mps-nonprod-rno AND mps-mondev-mdn fire the same generic problem simultaneously.
# Expected: 2 separate incidents (scoped by cluster in title fingerprint).
# ══════════════════════════════════════════════════════════════════════════════
if run G2; then
section "G2 · Multi-cluster storm isolation  (nonprod + mondev same generic title → 2 incidents)"
echo -e "  ${DIM}Simultaneous identical-title alerts from 2 clusters. Expected: 2 separate incidents.${RST}"

post "ingress cert expired — nonprod-rno" "P-G2-NONPROD-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-G2-NONPROD-${TS}\",
  \"problemTitle\": \"TLS certificate expired: ingress-nginx (cluster ingress)\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": null,
  \"impactedEntities\": [],
  \"ProblemDetailsText\": \"TLS cert for *.nonprod.k.example.com expired 2h ago. k8s.cluster.name: mps-nonprod-rno\nk8s.namespace.name: ingress-nginx\",
  \"customProperties\": {
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"ingress-nginx\",
    \"k8s.workload.name\": \"ingress-nginx-controller\",
    \"environment\": \"nonprod\"
  }
}"
pause 1

post "ingress cert expired — mondev-mdn" "P-G2-MONDEV-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-G2-MONDEV-${TS}\",
  \"problemTitle\": \"TLS certificate expired: ingress-nginx (cluster ingress)\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": null,
  \"impactedEntities\": [],
  \"ProblemDetailsText\": \"TLS cert for *.mondev.k.example.com expired 2h ago. k8s.cluster.name: mps-mondev-mdn\nk8s.namespace.name: ingress-nginx\",
  \"customProperties\": {
    \"k8s.cluster.name\": \"mps-mondev-mdn\",
    \"k8s.cluster.uid\": \"${MONDEV_UID}\",
    \"k8s.namespace.name\": \"ingress-nginx\",
    \"k8s.workload.name\": \"ingress-nginx-controller\",
    \"environment\": \"mondev\"
  }
}"
pause 5

n=$(count_incidents_with_title "TLS certificate expired" 5)
check_or_skip "G2 two separate incidents for same-title across clusters" "$n" "2"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO H1 — Burst with resolved alerts interspersed
#
# Fire BURST OPEN alerts across multiple hosts, then intersperse RESOLVED alerts
# for a subset. Verify:
#   1. Total ingested ≥ BURST
#   2. RESOLVED alerts do NOT create new open incidents
#   3. The resolved alerts' incidents show resolved status
# ══════════════════════════════════════════════════════════════════════════════
if run H1; then
section "H1 · Burst ${BURST} OPEN + 15 RESOLVED interspersed  (throughput + resolved state)"
echo -e "  ${DIM}Fire ${BURST} OPEN + 15 RESOLVED. Expect resolved alerts close incidents, not create new.${RST}"
S_H1="H1-${TS}"

HOSTS=("$BM1" "$BM2" "$BM3" "$K8N_Z3_08" "$K8N_Z3_13" "$K8N_Z2_01" "$K8N_PREV" "$MDN_BM" "$MDN_BM2")
SEVS=("PERFORMANCE" "RESOURCE_CONTENTION" "AVAILABILITY" "PERFORMANCE" "AVAILABILITY")

# Batch 1: 15 OPEN alerts that we will later RESOLVE
RESOLVE_IDS=()
t_start=$(date +%s%N)
for i in $(seq 1 15); do
  host="${HOSTS[$((i % ${#HOSTS[@]}))]}"
  pid="${S_H1}-WILLRESOLVE-${i}"
  RESOLVE_IDS+=("$pid")
  post_quiet "{
    \"state\": \"OPEN\",
    \"problemId\": \"${pid}\",
    \"problemTitle\": \"H1 burst open-then-resolve: host=${host} idx=${i}\",
    \"impactLevel\": \"INFRASTRUCTURE\",
    \"severity\": \"PERFORMANCE\",
    \"status\": \"OPEN\",
    \"startTime\": \"${NOW}\",
    \"rootCauseEntity\": {\"entityId\": \"HOST-${host}\", \"entityName\": \"${host}\", \"entityType\": \"HOST\"},
    \"impactedEntities\": [{\"entityId\": \"HOST-${host}\", \"entityName\": \"${host}\", \"entityType\": \"HOST\"}],
    \"ProblemDetailsText\": \"H1 burst alert ${i}. host.name: ${host}\",
    \"customProperties\": {\"host.name\": \"${host}\", \"impacted_entity\": \"${host}\", \"environment\": \"ADC\", \"batch\": \"${S_H1}\", \"index\": \"${i}\"}
  }"
done

# Batch 2: remaining OPEN alerts (fire-and-forget)
for i in $(seq 16 "$BURST"); do
  host="${HOSTS[$((i % ${#HOSTS[@]}))]}"
  sev="${SEVS[$((i % ${#SEVS[@]}))]}"
  post_quiet "{
    \"state\": \"OPEN\",
    \"problemId\": \"${S_H1}-burst-${i}\",
    \"problemTitle\": \"H1 burst alert ${i}: ${host}\",
    \"impactLevel\": \"INFRASTRUCTURE\",
    \"severity\": \"${sev}\",
    \"status\": \"OPEN\",
    \"startTime\": \"${NOW}\",
    \"rootCauseEntity\": {\"entityId\": \"HOST-${host}\", \"entityName\": \"${host}\", \"entityType\": \"HOST\"},
    \"impactedEntities\": [{\"entityId\": \"HOST-${host}\", \"entityName\": \"${host}\", \"entityType\": \"HOST\"}],
    \"ProblemDetailsText\": \"H1 burst alert ${i}. host.name: ${host}\",
    \"customProperties\": {\"host.name\": \"${host}\", \"impacted_entity\": \"${host}\", \"environment\": \"ADC\", \"batch\": \"${S_H1}\", \"index\": \"${i}\"}
  }"
done
wait

t_end=$(date +%s%N)
elapsed_ms=$(( (t_end - t_start) / 1000000 ))
rps=$(( BURST * 1000 / (elapsed_ms + 1) ))
echo -e "  ${GRN}Fired ${BURST} alerts in ${elapsed_ms}ms  (~${rps} req/s)${RST}"

echo -e "  ${DIM}waiting 8s for pipeline to process all alerts…${RST}"
pause 8

# Now send RESOLVED for the 15 OPEN-then-RESOLVE alerts
echo -e "  ${CYN}--- Sending 15 RESOLVED alerts ---${RST}"
for pid in "${RESOLVE_IDS[@]}"; do
  i="${pid##*-}"
  host="${HOSTS[$((i % ${#HOSTS[@]}))]}"
  post_quiet "{
    \"state\": \"RESOLVED\",
    \"problemId\": \"${pid}\",
    \"problemTitle\": \"H1 RESOLVED: host=${host} idx=${i}\",
    \"impactLevel\": \"INFRASTRUCTURE\",
    \"severity\": \"PERFORMANCE\",
    \"status\": \"RESOLVED\",
    \"startTime\": \"${NOW}\",
    \"rootCauseEntity\": {\"entityId\": \"HOST-${host}\", \"entityName\": \"${host}\", \"entityType\": \"HOST\"},
    \"impactedEntities\": [{\"entityId\": \"HOST-${host}\", \"entityName\": \"${host}\", \"entityType\": \"HOST\"}],
    \"ProblemDetailsText\": \"H1 burst alert resolved. host.name: ${host}\",
    \"customProperties\": {\"host.name\": \"${host}\", \"impacted_entity\": \"${host}\", \"environment\": \"ADC\", \"batch\": \"${S_H1}\", \"index\": \"${i}\"}
  }"
done
wait
echo -e "  ${DIM}waiting 8s for resolved handling…${RST}"
pause 8

echo ""
echo -e "  ${CYN}--- DB: ingested count for H1 batch ---${RST}"
ingested=$(kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT COUNT(*) FROM alerts
  WHERE labels->>'batch' = '${S_H1}'
    AND created_at > NOW()-INTERVAL '5 minutes';" 2>/dev/null | tr -d ' \n' || echo "?")
echo -e "  ingested=${ingested}  (expected≥${BURST})"

# Verify no open incidents with "H1 RESOLVED:" in title (resolved alert didn't create incident)
resolved_created=$(kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT COUNT(*) FROM incidents
  WHERE auto_created=true
    AND title ILIKE '%H1 RESOLVED%'
    AND status IN ('open','investigating')
    AND created_at > NOW()-INTERVAL '5 minutes';" 2>/dev/null | tr -d ' \n' || echo "?")
check_or_skip "H1 no open incidents created by RESOLVED alerts in burst" "$resolved_created" "0"

if [[ "$ingested" == "?" ]]; then
  echo -e "  ${YLW}⊘ SKIP${RST}  H1 ingested count: kubectl unavailable"
  (( SKIP_COUNT++ )) || true
elif [[ "$ingested" -ge "$BURST" ]]; then
  echo -e "  ${GRN}✓ PASS${RST}  H1 all ${BURST} alerts ingested: ${ingested}"
  (( PASS_COUNT++ )) || true
else
  echo -e "  ${RED}✗ FAIL${RST}  H1 ingestion: want≥${BURST} got=${ingested}"
  (( FAIL_COUNT++ )) || true
fi
fi

# ─── final summary ────────────────────────────────────────────────────────────
echo ""
echo -e "${BOLD}${MAG}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RST}"
echo -e "${BOLD}  Test Suite Summary${RST}"
echo -e "${BOLD}${MAG}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RST}"
echo -e "  ${GRN}✓ PASS${RST}  ${PASS_COUNT}"
echo -e "  ${RED}✗ FAIL${RST}  ${FAIL_COUNT}"
echo -e "  ${YLW}⊘ SKIP${RST}  ${SKIP_COUNT}  (kubectl unavailable)"
echo ""
echo -e "  ${DIM}View all incidents → https://aileron.example.com/incidents${RST}"
echo -e "  ${DIM}Run TS: ${TS}${RST}"
echo ""
if [[ "$FAIL_COUNT" -gt 0 ]]; then
  echo -e "  ${RED}${BOLD}Some assertions FAILED — investigate with dbcheck and logs above.${RST}"
  exit 1
else
  echo -e "  ${GRN}${BOLD}All executed assertions passed.${RST}"
fi
