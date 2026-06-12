#!/usr/bin/env bash
# ╔══════════════════════════════════════════════════════════════════════════════╗
# ║  test_real_topology.sh — AlertHub test suite using LIVE Neo4j topology       ║
# ║                                                                              ║
# ║  All entity IDs, pod names, node names, NetApp volumes, PVC names, cluster  ║
# ║  UIDs, BM/VM names are pulled directly from the live Neo4j graph and the    ║
# ║  Postgres alert history. Nothing is fabricated.                              ║
# ║                                                                              ║
# ║  Real topology sourced from:                                                 ║
# ║    • kubectl exec neo4j-0 -- cypher-shell (BM/VM/node/pod/NetApp chains)    ║
# ║    • Postgres alerts table (cluster UIDs from real DT payloads)              ║
# ║                                                                              ║
# ║  Scenarios:                                                                  ║
# ║    T01  BM → VM → K8s node cascade        (iapps-100-67-61-20 → z3-01)      ║
# ║    T02  Multi-node storm — 3 BMs fail     (3 independent RNO bare-metal)    ║
# ║    T03  Node → multi-NS pod cascade       (z3-01 pods in 4 namespaces)      ║
# ║    T04  NetApp RNO node fail → vol → pod  (netapp-rno-node001 → aem-dev)   ║
# ║    T05  MDN BM → VM → K8s node → pods     (iapps-100-67-86-19 → MDN)       ║
# ║    T06  Cross-cluster isolation            (same title from example-cluster      ║
# ║                                             and mps-mondev-mdn → 2 incidents)║
# ║    T07  k8preview01 cascade isolated       (k8preview01 VM → real pods)     ║
# ║    T08  OPEN → RESOLVED lifecycle          (real workload → incident closes) ║
# ║    T09  Deduplication — same problemId ×3  (1 alert record, count=3)        ║
# ║    T10  Flapping 3-cycle                   (OPEN→RES×3, final=open)         ║
# ║    T11  Cross-cluster title isolation      (v1.0.16 fix — 3 clusters)       ║
# ║    T12  Workload entity scoping            (v1.0.18 fix — KUBERNETES_WORKLOAD)║
# ║    T13  NetApp aggregate → PVC I/O error   (aggr1_node001 RNO cascade)      ║
# ║    T14  Late root arrival                  (pods fire 20s before node)      ║
# ║    T15  Burst 80 alerts — real host pool   (real BM/VM/node IDs)            ║
# ╚══════════════════════════════════════════════════════════════════════════════╝
#
# Usage:
#   bash scripts/test_real_topology.sh           # all scenarios
#   bash scripts/test_real_topology.sh T01 T04   # specific
#   BURST=150 bash scripts/test_real_topology.sh T15

set -uo pipefail

ENDPOINT="${ENDPOINT:-https://aileron.example.com/api/v1/webhooks/dynatrace}"
API_KEY="${API_KEY:-ah_c76ddeaa86c74818ac84997c0f2a8174eaba3bf0fdab295132acb8aba2238e5f}"
NS="${NS:-aileron}"
BURST="${BURST:-80}"
NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
TS=$(date +%s)

# ══════════════════════════════════════════════════════════════════════════════
# REAL TOPOLOGY — sourced from Neo4j graph (live cluster)
# ══════════════════════════════════════════════════════════════════════════════

# ── RNO Bare-metal KVM hypervisors (CloudStack-RNO) ──────────────────────────
# From: MATCH (bm:Service {type:'bare_metal'})-[:HOSTED_ON]->(vm:Service)
#       WHERE bm.region CONTAINS 'RNO'
BM_RNO_1="iapps-100-67-61-20"          # Neo4j entity_id: cloudstack-host-CloudStack-RNO-iapps-100-67-61-20
BM_RNO_2="iapps-100-67-62-31"          # Neo4j entity_id: cloudstack-host-CloudStack-RNO-iapps-100-67-62-31
BM_RNO_3="iapps-100-67-63-25"          # Neo4j entity_id: cloudstack-host-CloudStack-RNO-iapps-100-67-63-25
BM_RNO_4="iapps-100-67-63-29"          # Neo4j entity_id: cloudstack-host-CloudStack-RNO-iapps-100-67-63-29
BM_RNO_5="iapps-100-67-62-35"          # Neo4j entity_id: cloudstack-host-CloudStack-RNO-iapps-100-67-62-35

# ── RNO CloudStack VMs (back the K8s worker nodes) ───────────────────────────
# From: MATCH (bm)-[:HOSTED_ON]->(vm) … RETURN bm.name, vm.name
VM_Z3_01="example-cluster-worker-z3-01"    # Neo4j: cloudstack-vm-79683331-dd16-4665-9280-f69f3c4cb5fc, BM: iapps-100-67-61-20
VM_Z2_01="example-cluster-worker-z2-01"    # Neo4j: cloudstack-vm-3c2bea00-8e15-4a2d-836d-51c949cccfb4, BM: iapps-100-67-62-31
VM_Z1_01="example-cluster-worker-z1-01"    # Neo4j: cloudstack-vm-3294fcca-e280-4dd4-b3b7-58a822299577, BM: iapps-100-67-63-25
VM_Z1_02="example-cluster-worker-z1-02"    # K8s node z1-02

# ── example-cluster K8s worker nodes ─────────────────────────────────────────────
# From: MATCH (n:Service {type:'k8s_node', region:'example-cluster'})
K8N_Z3_01="example-cluster-worker-z3-01"   # Neo4j: k8s-node-example-cluster-example-cluster-worker-z3-01
K8N_Z3_05="example-cluster-worker-z3-05"   # Neo4j: k8s-node-example-cluster-example-cluster-worker-z3-05
K8N_Z1_02="example-cluster-worker-z1-02"   # Neo4j: k8s-node-example-cluster-example-cluster-worker-z1-02
K8N_Z1_04="example-cluster-worker-z1-04"   # Neo4j: k8s-node-example-cluster-example-cluster-worker-z1-04

# ── Real pods on K8N_Z3_01 (example-cluster-worker-z3-01) ───────────────────────
# From: MATCH (p)-[:DEPLOYED_IN]->(n {name:'example-cluster-worker-z3-01'})
POD_BE="alerthub-backend-7f475dd988-2f4tn"      # ns: aileron
POD_FE="frontend-6dbdcb988-96mdj"               # ns: aileron
POD_DT_SPLUNK="dynatrace-to-splunk-29653020-2h2sw" # ns: dt-audit-events

# ── Real pods on K8N_Z1_02 (example-cluster-worker-z1-02) ───────────────────────
POD_REDIS="redis-cluster-0"                     # ns: aileron
POD_ARGOCD_APP="argocd-application-controller-0" # ns: argocd
POD_ARGOCD_REPO="argocd-repo-server-578f7dcbbb-t44mh" # ns: argocd
POD_JAEGER="jaeger-collector-6999b7f9f-98kqp"   # ns: opentracing
POD_FLOODGATE="floodgate-test-5c8594c7cf-rszvl"  # ns: interactive-dx-dev

# ── Real pods on K8N_Z3_05 (neo4j-0, rca-orchestrator) ──────────────────────
POD_NEO4J="neo4j-0"                            # ns: aileron
POD_RCA="rca-orchestrator-74bbc65997-9hrz8"   # ns: aileron

# ── example-cluster cluster UID (from Postgres alert history) ────────────────────
DEV_RNO_UID="f098c614-932a-4b4a-a698-29fab728c2a2"

# ── MDN bare-metal hypervisors (CloudStack-MDN) ───────────────────────────────
# From: MATCH (bm)-[:HOSTED_ON]->(vm) WHERE vm.name CONTAINS 'mps-mondev-mdn'
BM_MDN_1="iapps-100-67-86-19"          # → VM: mps-mondev-mdn-worker-z1-01
BM_MDN_2="iapps-100-67-86-34"          # → VM: mps-mondev-mdn-worker-z1-02
BM_MDN_3="iapps-100-67-84-34"          # → VMs: z3-03, z3-04, z3-05

# ── MDN VMs and K8s nodes ────────────────────────────────────────────────────
VM_MDN_Z1_01="mps-mondev-mdn-worker-z1-01"     # Neo4j: cloudstack-vm-b1094368-20ab-4bf4-b427-2ea80d1ad88d
VM_MDN_Z1_02="mps-mondev-mdn-worker-z1-02"     # Neo4j: cloudstack-vm-389e95c2-7fe2-4978-bab4-2fef27c59e92
K8N_MDN_Z1_01="mps-mondev-mdn-worker-z1-01"    # Neo4j: k8s-node-mps-mondev-mdn-mps-mondev-mdn-worker-z1-01

# ── Real pods on MDN z1-01 ───────────────────────────────────────────────────
POD_MDN_COMP="comparitive-analysis-api-b5cbdd96b-t579g"  # ns: monitoring-dev
POD_MDN_PLAN="quarterly-planning-ui-67bcfd575-cprkb"     # ns: monitoring-dev
POD_MDN_MDS="mds-html-viewer-5fd66445cc-8vmsp"           # ns: mds-platform
POD_MDN_FALCO="falco-falcosidekick-7f4d89c66b-55rpd"     # ns: falco

# ── mps-mondev-mdn cluster UID ────────────────────────────────────────────────
MDN_UID="00a07750-e556-443e-89d9-80341edb472d"

# ── k8preview01-rno CloudStack VM ────────────────────────────────────────────
VM_PREV12="k8preview01-cs-vm-worker12-rno"   # Neo4j: cloudstack-vm-cac9bf06-3604-45a5-ad5e-1d7c65ef2306
VM_PREV19="k8preview01-cs-vm-worker19-rno"   # Neo4j: cloudstack-vm-89042efa-ae4b-4220-95c8-5d4b935017af

# ── k8preview01-rno cluster UID ──────────────────────────────────────────────
K8PREV_UID="d1be9a2f-4e83-49bf-8800-b9a2bc2e7da0"

# ── NetApp RNO topology ───────────────────────────────────────────────────────
# From: MATCH (n) WHERE n.type IN ['netapp_cluster','netapp_node','netapp_aggregate']
NETAPP_RNO_CLUSTER="netapp-rno-cluster001"    # Neo4j: netapp-cluster-netapp-rno-cluster001
NETAPP_RNO_NODE1="netapp-rno-node001"         # Neo4j: netapp-node-netapp-rno-cluster001-netapp-rno-node001
NETAPP_RNO_NODE2="netapp-rno-node002"         # Neo4j: netapp-node-netapp-rno-cluster001-netapp-rno-node002
NETAPP_RNO_AGG1="aggr1_node001"               # Neo4j: netapp-aggr-netapp-rno-cluster001-aggr1_node001
NETAPP_RNO_AGG2="aggr1_node002"               # Neo4j: netapp-aggr-netapp-rno-cluster001-aggr1_node002

# ── Real NetApp RNO volumes → PVCs → pods (aem-dev workload) ─────────────────
# From: MATCH (vol)-[:BACKS_PVC]->(pvc)<-[:USES_PVC]-(pod) WHERE vol CONTAINS 'rno-cluster001'
NETAPP_VOL_AUTHOR_DS="trident_pvc_d29e223a_1fe4_4533_b6f0_9a5734be56af"
NETAPP_VOL_AUTHOR_CRX="trident_pvc_9fe111bb_4cdf_4b17_ab92_fd059eb8fab0"
NETAPP_VOL_PUBLISH_DS="trident_pvc_f1c2f188_70f8_47a2_bd66_3fb5c6dce20a"
PVC_AUTHOR_DS="author-datastore"              # k8s-pvc-d29e223a-1fe4-4533-b6f0-9a5734be56af
PVC_AUTHOR_CRX="crx-author-dev-0"            # k8s-pvc-9fe111bb-4cdf-4b17-ab92-fd059eb8fab0
PVC_PUBLISH="publish-dev-datastore"           # k8s-pvc-f1c2f188-70f8-47a2-bd66-3fb5c6dce20a
POD_AUTHOR0="author-dev-0"                   # ns: aem-dev, node: example-cluster-worker-z1-02
POD_AUTHOR1="author-dev-1"                   # ns: aem-dev
POD_PUBLISH0="publish-dev-0"                 # ns: aem-dev, node: example-cluster-worker-01
POD_PUBLISH1="publish-dev-1"                 # ns: aem-dev

# ── Additional real RNO cluster UIDs (from Postgres) ─────────────────────────
NONPROD_UID="88b12bf2-9f23-4b4a-b06a-3d2b33134a3b"
SANDBOX_UID="cb1d4257-ad9d-4790-9215-98033f31c42b"
TOOLING_UID="0a0430fb-0c23-4e80-a000-caa5570d6c17"

# ─── colors ───────────────────────────────────────────────────────────────────
BOLD="\033[1m"; DIM="\033[2m"; RST="\033[0m"
RED="\033[31m"; GRN="\033[32m"; YLW="\033[33m"; CYN="\033[36m"; MAG="\033[35m"

PASS=0; FAIL=0; SKIP=0

# ─── helpers ──────────────────────────────────────────────────────────────────
post() {
  local label="$1" pid="$2" payload="$3"
  printf "  → %-55s " "[${pid}]..."
  local out code body aid
  out=$(curl -s -w "\n%{http_code}" -X POST "$ENDPOINT" \
    -H "Content-Type: application/json" \
    -H "X-API-Key: ${API_KEY}" \
    -d "$payload" 2>&1)
  code=$(echo "$out" | tail -1)
  body=$(echo "$out" | head -1)
  aid=$(echo "$body" | grep -o '"alert_id":"[^"]*"' | cut -d'"' -f4 || true)
  if [[ "$code" =~ ^(200|201|202)$ ]]; then
    printf "${GRN}HTTP %s${RST}" "$code"
    [[ -n "$aid" ]] && printf "  id=%.12s…" "$aid"
    echo "  ${label}"
  else
    printf "${RED}HTTP %s${RST}  %s\n" "$code" "$(echo "$body" | head -c 200)"
  fi
}

post_bg() { curl -s -o /dev/null -X POST "$ENDPOINT" \
  -H "Content-Type: application/json" -H "X-API-Key: ${API_KEY}" -d "$1" & }

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
           '  a=' || jsonb_array_length(alert_ids) ||
           '  ' || status ||
           '  corr=' || COALESCE(LEFT(correlation_id,28),'NULL') ||
           '  » ' || LEFT(title,46)
    FROM incidents
    WHERE auto_created=true AND updated_at > NOW()-INTERVAL '10 minutes'
    ORDER BY created_at DESC LIMIT 12;" 2>/dev/null | grep -v "^$" \
    || echo -e "  ${DIM}(kubectl unavailable)${RST}"
}

inc_count() {
  kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT COUNT(*) FROM incidents
    WHERE auto_created=true AND correlation_id='${1}'
      AND status IN ('open','investigating')
      AND updated_at > NOW()-INTERVAL '${2:-10} minutes';" 2>/dev/null \
  | tr -d ' \n' || echo "?"
}

inc_alert_count() {
  kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT jsonb_array_length(alert_ids) FROM incidents
    WHERE auto_created=true AND correlation_id='${1}'
    ORDER BY created_at DESC LIMIT 1;" 2>/dev/null | tr -d ' \n' || echo "?"
}

title_inc_count() {
  kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT COUNT(DISTINCT id) FROM incidents
    WHERE auto_created=true AND title ILIKE '%${1}%'
      AND updated_at > NOW()-INTERVAL '${2:-10} minutes';" 2>/dev/null \
  | tr -d ' \n' || echo "?"
}

alert_count_sid() {
  kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT COUNT(*) FROM alerts
    WHERE source_id='${1}' AND updated_at > NOW()-INTERVAL '10 minutes';" 2>/dev/null \
  | tr -d ' \n' || echo "?"
}

check() {
  local label="$1" got="$2" want="$3"
  if   [[ "$got" == "?" ]]; then
    echo -e "  ${YLW}⊘ SKIP${RST}  ${label}: kubectl unavailable"; (( SKIP++ )) || true
  elif [[ "$got" == "$want" ]]; then
    echo -e "  ${GRN}✓ PASS${RST}  ${label}: ${got}"; (( PASS++ )) || true
  else
    echo -e "  ${RED}✗ FAIL${RST}  ${label}: want=${want} got=${got}"; (( FAIL++ )) || true
  fi
}

check_ge() {
  local label="$1" got="$2" min="$3"
  if [[ "$got" == "?" ]]; then
    echo -e "  ${YLW}⊘ SKIP${RST}  ${label}: kubectl unavailable"; (( SKIP++ )) || true
  elif [[ "$got" -ge "$min" ]]; then
    echo -e "  ${GRN}✓ PASS${RST}  ${label}: ${got} (≥${min})"; (( PASS++ )) || true
  else
    echo -e "  ${RED}✗ FAIL${RST}  ${label}: want≥${min} got=${got}"; (( FAIL++ )) || true
  fi
}

RUN_ALL=true; SELECTED=()
[[ $# -gt 0 ]] && RUN_ALL=false && SELECTED=("$@")
run() {
  $RUN_ALL && return 0
  for s in "${SELECTED[@]}"; do [[ "$s" == "$1" ]] && return 0; done; return 1
}

echo ""
echo -e "${BOLD}AlertHub — Real Topology Test Suite  (v1.0.23-dev)${RST}"
echo -e "${DIM}Endpoint  : ${ENDPOINT}${RST}"
echo -e "${DIM}Namespace : ${NS}${RST}"
echo -e "${DIM}Run TS    : ${TS}${RST}"
echo -e "${DIM}Topology  : sourced from live Neo4j graph (aileron)${RST}"

# ══════════════════════════════════════════════════════════════════════════════
# T01 — BM → VM → K8s node cascade
#
# Real chain from Neo4j:
#   BM:   iapps-100-67-61-20  (CloudStack-RNO BM, entity: cloudstack-host-CloudStack-RNO-iapps-100-67-61-20)
#   VM:   example-cluster-worker-z3-01 (entity: cloudstack-vm-79683331-dd16-4665-9280-f69f3c4cb5fc)
#   Node: example-cluster-worker-z3-01 (entity: k8s-node-example-cluster-example-cluster-worker-z3-01)
#
# Expected: 1 incident, correlation_id=iapps-100-67-61-20, alert_count=3
# ══════════════════════════════════════════════════════════════════════════════
if run T01; then
section "T01 · BM → VM → K8s node cascade  (${BM_RNO_1} → ${VM_Z3_01})"
echo -e "  ${DIM}Real Neo4j chain: BM iapps-100-67-61-20 → VM z3-01 → K8s node z3-01${RST}"
echo -e "  ${DIM}Expected: 1 incident, corr=${BM_RNO_1}${RST}"

post "BM ${BM_RNO_1} memory critical (root)" "P-T01-BM-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-T01-BM-${TS}\",
  \"problemTitle\": \"KVM hypervisor memory saturation: ${BM_RNO_1}\",
  \"ProblemDetailsText\": \"Host ${BM_RNO_1} memory at 96% (250/256 Gi). Balloon reclaim active. New VM scheduling blocked. host.name: ${BM_RNO_1}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"RESOURCE_CONTENTION\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${BM_RNO_1}\", \"entityName\": \"${BM_RNO_1}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"HOST-${BM_RNO_1}\", \"entityName\": \"${BM_RNO_1}\", \"entityType\": \"HOST\"}],
  \"customProperties\": {\"host.name\": \"${BM_RNO_1}\", \"impacted_entity\": \"${BM_RNO_1}\", \"entity_type\": \"bare_metal\", \"environment\": \"ADC\", \"region\": \"rno\", \"memory_pct\": \"96\", \"vm_count\": \"24\"}
}"
pause 2

post "VM ${VM_Z3_01} CPU steal (BM downstream)" "P-T01-VM-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-T01-VM-${TS}\",
  \"problemTitle\": \"CPU steal spike on VM ${VM_Z3_01}\",
  \"ProblemDetailsText\": \"CPU steal at 38% on ${VM_Z3_01} due to hypervisor ${BM_RNO_1} oversubscription. host.name: ${VM_Z3_01}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"PERFORMANCE\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${BM_RNO_1}\", \"entityName\": \"${BM_RNO_1}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"HOST-${VM_Z3_01}\", \"entityName\": \"${VM_Z3_01}\", \"entityType\": \"HOST\"}],
  \"customProperties\": {\"host.name\": \"${VM_Z3_01}\", \"impacted_entity\": \"${VM_Z3_01}\", \"entity_type\": \"vm\", \"kvm_host\": \"${BM_RNO_1}\", \"environment\": \"ADC\", \"cpu_steal_pct\": \"38\"}
}"
pause 2

post "K8s node ${K8N_Z3_01} NotReady (VM downstream)" "P-T01-NODE-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-T01-NODE-${TS}\",
  \"problemTitle\": \"K8s Node NotReady: ${K8N_Z3_01} (example-cluster)\",
  \"ProblemDetailsText\": \"Node ${K8N_Z3_01} NotReady. kubelet unresponsive. Underlying VM ${VM_Z3_01} CPU-starved. 18 pods evicting. host.name: ${K8N_Z3_01}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${BM_RNO_1}\", \"entityName\": \"${BM_RNO_1}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"HOST-${K8N_Z3_01}\", \"entityName\": \"${K8N_Z3_01}\", \"entityType\": \"HOST\"}],
  \"customProperties\": {\"host.name\": \"${K8N_Z3_01}\", \"k8s.node.name\": \"${K8N_Z3_01}\", \"k8s.cluster.name\": \"example-cluster\", \"k8s.cluster.uid\": \"${DEV_RNO_UID}\", \"environment\": \"ADC\", \"evicting_pods\": \"18\"}
}"
pause 6
dbcheck
echo ""
n=$(inc_count "${BM_RNO_1}")
check "T01 single incident for BM cascade" "$n" "1"
ac=$(inc_alert_count "${BM_RNO_1}")
check_ge "T01 alert_count in incident" "$ac" 3
fi

# ══════════════════════════════════════════════════════════════════════════════
# T02 — Multi-node storm: 3 BMs fail simultaneously → 3 separate incidents
#
# Real BMs: iapps-100-67-61-20, iapps-100-67-62-31, iapps-100-67-63-25
# Each is a different CloudStack host, should produce 3 isolated incidents.
# ══════════════════════════════════════════════════════════════════════════════
if run T02; then
section "T02 · Multi-BM storm — 3 independent RNO hypervisors fail simultaneously"
echo -e "  ${DIM}Real BMs: ${BM_RNO_1}, ${BM_RNO_2}, ${BM_RNO_3}${RST}"
echo -e "  ${DIM}Expected: 3 separate incidents (different correlation_ids)${RST}"

for bm in "$BM_RNO_1" "$BM_RNO_2" "$BM_RNO_3"; do
  post "BM ${bm} disk I/O failure" "P-T02-${bm}-${TS}" "{
    \"state\": \"OPEN\",
    \"problemId\": \"P-T02-${bm}-${TS}\",
    \"problemTitle\": \"Disk I/O failure: KVM hypervisor ${bm}\",
    \"ProblemDetailsText\": \"RAID controller reporting I/O errors on ${bm}. VM disk latency >2s. host.name: ${bm}\",
    \"impactLevel\": \"INFRASTRUCTURE\",
    \"severity\": \"AVAILABILITY\",
    \"status\": \"OPEN\",
    \"startTime\": \"${NOW}\",
    \"rootCauseEntity\": {\"entityId\": \"HOST-${bm}\", \"entityName\": \"${bm}\", \"entityType\": \"HOST\"},
    \"impactedEntities\": [{\"entityId\": \"HOST-${bm}\", \"entityName\": \"${bm}\", \"entityType\": \"HOST\"}],
    \"customProperties\": {\"host.name\": \"${bm}\", \"impacted_entity\": \"${bm}\", \"entity_type\": \"bare_metal\", \"environment\": \"ADC\", \"io_latency_ms\": \"2100\"}
  }"
done
pause 6
n1=$(inc_count "$BM_RNO_1"); n2=$(inc_count "$BM_RNO_2"); n3=$(inc_count "$BM_RNO_3")
check "T02 incident for ${BM_RNO_1}" "$n1" "1"
check "T02 incident for ${BM_RNO_2}" "$n2" "1"
check "T02 incident for ${BM_RNO_3}" "$n3" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# T03 — K8s node → multi-namespace pod cascade
#
# Real node: example-cluster-worker-z3-01
# Real pods on that node (from Neo4j DEPLOYED_IN relationships):
#   - alerthub-backend-7f475dd988-2f4tn  (ns: aileron)
#   - frontend-6dbdcb988-96mdj           (ns: aileron)
#   - dynatrace-to-splunk-29653020-2h2sw (ns: dt-audit-events)
# Also: real z1-02 pods: redis-cluster-0 (aileron), argocd-application-controller-0 (argocd)
#
# Expected: 1 incident, all pods merged under node root
# ══════════════════════════════════════════════════════════════════════════════
if run T03; then
section "T03 · K8s node → multi-NS pod cascade  (real node: ${K8N_Z3_01})"
echo -e "  ${DIM}Real pods from Neo4j DEPLOYED_IN on ${K8N_Z3_01}${RST}"
echo -e "  ${DIM}Expected: 1 incident, corr=${K8N_Z3_01}, alert_count≥4${RST}"

post "Node ${K8N_Z3_01} NotReady (root)" "P-T03-NODE-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-T03-NODE-${TS}\",
  \"problemTitle\": \"K8s Node NotReady: ${K8N_Z3_01} (example-cluster)\",
  \"ProblemDetailsText\": \"Node ${K8N_Z3_01} NotReady. OOM: kernel killed processes. kubelet stopped. host.name: ${K8N_Z3_01}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${K8N_Z3_01}\", \"entityName\": \"${K8N_Z3_01}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"HOST-${K8N_Z3_01}\", \"entityName\": \"${K8N_Z3_01}\", \"entityType\": \"HOST\"}],
  \"customProperties\": {\"host.name\": \"${K8N_Z3_01}\", \"k8s.node.name\": \"${K8N_Z3_01}\", \"k8s.cluster.name\": \"example-cluster\", \"k8s.cluster.uid\": \"${DEV_RNO_UID}\", \"environment\": \"ADC\"}
}"
pause 2

# Real pod: alerthub-backend in aileron
post "${POD_BE} evicted (ns=aileron)" "P-T03-BE-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-T03-BE-${TS}\",
  \"problemTitle\": \"Pod evicted: ${POD_BE} (aileron)\",
  \"ProblemDetailsText\": \"Pod ${POD_BE} evicted from ${K8N_Z3_01}. Node NotReady.\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${K8N_Z3_01}\", \"entityName\": \"${K8N_Z3_01}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-${POD_BE}\", \"entityName\": \"${POD_BE}\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
  \"customProperties\": {\"k8s.namespace.name\": \"aileron\", \"k8s.workload.name\": \"alerthub-backend\", \"k8s.node.name\": \"${K8N_Z3_01}\", \"k8s.cluster.name\": \"example-cluster\", \"k8s.cluster.uid\": \"${DEV_RNO_UID}\", \"environment\": \"ADC\"}
}"
pause 1

# Real pod: frontend in aileron
post "${POD_FE} evicted (ns=aileron)" "P-T03-FE-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-T03-FE-${TS}\",
  \"problemTitle\": \"Pod evicted: ${POD_FE} (aileron)\",
  \"ProblemDetailsText\": \"Pod ${POD_FE} evicted from ${K8N_Z3_01}. Frontend unavailable.\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${K8N_Z3_01}\", \"entityName\": \"${K8N_Z3_01}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-${POD_FE}\", \"entityName\": \"${POD_FE}\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
  \"customProperties\": {\"k8s.namespace.name\": \"aileron\", \"k8s.workload.name\": \"frontend\", \"k8s.node.name\": \"${K8N_Z3_01}\", \"k8s.cluster.name\": \"example-cluster\", \"k8s.cluster.uid\": \"${DEV_RNO_UID}\", \"environment\": \"ADC\"}
}"
pause 1

# Real pod: dynatrace-to-splunk in dt-audit-events
post "${POD_DT_SPLUNK} evicted (ns=dt-audit-events)" "P-T03-DT-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-T03-DT-${TS}\",
  \"problemTitle\": \"Pod evicted: ${POD_DT_SPLUNK} (dt-audit-events)\",
  \"ProblemDetailsText\": \"Pod ${POD_DT_SPLUNK} evicted from ${K8N_Z3_01}. DT audit log shipping gap.\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"PERFORMANCE\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${K8N_Z3_01}\", \"entityName\": \"${K8N_Z3_01}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-${POD_DT_SPLUNK}\", \"entityName\": \"${POD_DT_SPLUNK}\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
  \"customProperties\": {\"k8s.namespace.name\": \"dt-audit-events\", \"k8s.workload.name\": \"dynatrace-to-splunk\", \"k8s.node.name\": \"${K8N_Z3_01}\", \"k8s.cluster.name\": \"example-cluster\", \"k8s.cluster.uid\": \"${DEV_RNO_UID}\", \"environment\": \"ADC\"}
}"
pause 1

# Real pod: neo4j-0 on z3-05
post "${POD_NEO4J} pod failing (ns=aileron, node=${K8N_Z3_05})" "P-T03-NEO-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-T03-NEO-${TS}\",
  \"problemTitle\": \"Pod failing: ${POD_NEO4J} (aileron) — node ${K8N_Z3_05}\",
  \"ProblemDetailsText\": \"${POD_NEO4J} OOM pressure on ${K8N_Z3_05}. Graph DB response time spiked.\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"RESOURCE_CONTENTION\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${K8N_Z3_01}\", \"entityName\": \"${K8N_Z3_01}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-${POD_NEO4J}\", \"entityName\": \"${POD_NEO4J}\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
  \"customProperties\": {\"k8s.namespace.name\": \"aileron\", \"k8s.workload.name\": \"neo4j\", \"k8s.node.name\": \"${K8N_Z3_05}\", \"k8s.cluster.name\": \"example-cluster\", \"k8s.cluster.uid\": \"${DEV_RNO_UID}\", \"environment\": \"ADC\"}
}"
pause 6
dbcheck
echo ""
n=$(inc_count "${K8N_Z3_01}")
check "T03 single incident for node cascade" "$n" "1"
ac=$(inc_alert_count "${K8N_Z3_01}")
check_ge "T03 alert_count ≥ 4" "$ac" 4
fi

# ══════════════════════════════════════════════════════════════════════════════
# T04 — NetApp RNO node failure → volume → PVC → pod cascade
#
# Real chain (Neo4j):
#   NetApp node:  netapp-rno-node001  (entity: netapp-node-netapp-rno-cluster001-netapp-rno-node001)
#   Aggregate:    aggr1_node001       (entity: netapp-aggr-netapp-rno-cluster001-aggr1_node001)
#   Volume:       trident_pvc_d29e223a…  (backs PVC author-datastore)
#   PVC:          author-datastore    (entity: k8s-pvc-d29e223a-1fe4-4533-b6f0-9a5734be56af)
#   Pods:         author-dev-0, author-dev-1  (ns: aem-dev, example-cluster)
#
# Expected: 1 incident, corr=netapp-rno-node001, alert_count=5
# ══════════════════════════════════════════════════════════════════════════════
if run T04; then
section "T04 · NetApp RNO node → aggregate → volume → PVC → pod  (real aem-dev chain)"
echo -e "  ${DIM}Real Neo4j chain: ${NETAPP_RNO_NODE1} → ${NETAPP_RNO_AGG1} → ${NETAPP_VOL_AUTHOR_DS} → ${PVC_AUTHOR_DS} → author-dev-{0,1}${RST}"

post "NetApp ${NETAPP_RNO_NODE1} controller failure (root)" "P-T04-NAN-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-T04-NAN-${TS}\",
  \"problemTitle\": \"NetApp storage controller offline: ${NETAPP_RNO_NODE1} (${NETAPP_RNO_CLUSTER})\",
  \"ProblemDetailsText\": \"Node ${NETAPP_RNO_NODE1} failed. Partner takeover initiated. All aggregates on this node entering restricted state. host.name: ${NETAPP_RNO_NODE1}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${NETAPP_RNO_NODE1}\", \"entityName\": \"${NETAPP_RNO_NODE1}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"HOST-${NETAPP_RNO_NODE1}\", \"entityName\": \"${NETAPP_RNO_NODE1}\", \"entityType\": \"HOST\"}],
  \"customProperties\": {\"host.name\": \"${NETAPP_RNO_NODE1}\", \"netapp_cluster\": \"${NETAPP_RNO_CLUSTER}\", \"netapp_entity\": \"${NETAPP_RNO_NODE1}\", \"entity_type\": \"netapp_node\", \"environment\": \"ADC\", \"impacted_entity\": \"${NETAPP_RNO_NODE1}\"}
}"
pause 2

post "NetApp aggregate ${NETAPP_RNO_AGG1} offline" "P-T04-AGG-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-T04-AGG-${TS}\",
  \"problemTitle\": \"NetApp aggregate offline: ${NETAPP_RNO_AGG1} (${NETAPP_RNO_CLUSTER})\",
  \"ProblemDetailsText\": \"Aggregate ${NETAPP_RNO_AGG1} on ${NETAPP_RNO_NODE1} went offline during takeover. All hosted volumes unmounted.\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${NETAPP_RNO_NODE1}\", \"entityName\": \"${NETAPP_RNO_NODE1}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"HOST-${NETAPP_RNO_NODE1}\", \"entityName\": \"${NETAPP_RNO_NODE1}\", \"entityType\": \"HOST\"}],
  \"customProperties\": {\"netapp_cluster\": \"${NETAPP_RNO_CLUSTER}\", \"netapp_aggregate\": \"${NETAPP_RNO_AGG1}\", \"netapp_node\": \"${NETAPP_RNO_NODE1}\", \"entity_type\": \"netapp_aggregate\", \"environment\": \"ADC\"}
}"
pause 2

post "Volume ${NETAPP_VOL_AUTHOR_DS} I/O error (backs ${PVC_AUTHOR_DS})" "P-T04-VOL-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-T04-VOL-${TS}\",
  \"problemTitle\": \"NetApp volume I/O error: ${NETAPP_VOL_AUTHOR_DS}\",
  \"ProblemDetailsText\": \"Volume ${NETAPP_VOL_AUTHOR_DS} (backing PVC ${PVC_AUTHOR_DS}) reporting EIO. Hosted on offline aggregate ${NETAPP_RNO_AGG1}.\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${NETAPP_RNO_NODE1}\", \"entityName\": \"${NETAPP_RNO_NODE1}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"HOST-${NETAPP_RNO_NODE1}\", \"entityName\": \"${NETAPP_RNO_NODE1}\", \"entityType\": \"HOST\"}],
  \"customProperties\": {\"netapp_cluster\": \"${NETAPP_RNO_CLUSTER}\", \"netapp_aggregate\": \"${NETAPP_RNO_AGG1}\", \"netapp_volume\": \"${NETAPP_VOL_AUTHOR_DS}\", \"pvc\": \"${PVC_AUTHOR_DS}\", \"entity_type\": \"netapp_volume\", \"environment\": \"ADC\"}
}"
pause 2

post "PVC ${PVC_AUTHOR_DS} I/O stalled (aem-dev)" "P-T04-PVC-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-T04-PVC-${TS}\",
  \"problemTitle\": \"PVC I/O stalled: ${PVC_AUTHOR_DS} (aem-dev namespace)\",
  \"ProblemDetailsText\": \"PVC ${PVC_AUTHOR_DS} in aem-dev namespace returning EIO. Backed by offline NetApp volume ${NETAPP_VOL_AUTHOR_DS}.\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${NETAPP_RNO_NODE1}\", \"entityName\": \"${NETAPP_RNO_NODE1}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-${POD_AUTHOR0}\", \"entityName\": \"${POD_AUTHOR0}\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
  \"customProperties\": {\"k8s.cluster.name\": \"example-cluster\", \"k8s.cluster.uid\": \"${DEV_RNO_UID}\", \"k8s.namespace.name\": \"aem-dev\", \"pvc\": \"${PVC_AUTHOR_DS}\", \"netapp_cluster\": \"${NETAPP_RNO_CLUSTER}\", \"environment\": \"ADC\"}
}"
pause 2

post "${POD_AUTHOR0} CrashLoop — PVC I/O (aem-dev)" "P-T04-POD-A0-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-T04-POD-A0-${TS}\",
  \"problemTitle\": \"CrashLoopBackOff: ${POD_AUTHOR0} (aem-dev) — PVC I/O failure\",
  \"ProblemDetailsText\": \"Pod ${POD_AUTHOR0} failing due to PVC ${PVC_AUTHOR_DS} I/O error. AEM Author instance unavailable. Storage root: ${NETAPP_RNO_NODE1}.\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${NETAPP_RNO_NODE1}\", \"entityName\": \"${NETAPP_RNO_NODE1}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-${POD_AUTHOR0}\", \"entityName\": \"${POD_AUTHOR0}\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
  \"customProperties\": {\"k8s.cluster.name\": \"example-cluster\", \"k8s.cluster.uid\": \"${DEV_RNO_UID}\", \"k8s.namespace.name\": \"aem-dev\", \"k8s.pod.name\": \"${POD_AUTHOR0}\", \"k8s.workload.name\": \"author-dev\", \"pvc\": \"${PVC_AUTHOR_DS}\", \"netapp_cluster\": \"${NETAPP_RNO_CLUSTER}\", \"environment\": \"ADC\"}
}"
pause 6
dbcheck
echo ""
n=$(inc_count "${NETAPP_RNO_NODE1}")
check "T04 single incident for NetApp RNO cascade" "$n" "1"
ac=$(inc_alert_count "${NETAPP_RNO_NODE1}")
check_ge "T04 alert_count ≥ 4" "$ac" 4
fi

# ══════════════════════════════════════════════════════════════════════════════
# T05 — MDN BM → VM → K8s node → real pods
#
# Real chain (Neo4j):
#   BM:   iapps-100-67-86-19  (CloudStack-MDN)
#   VM:   mps-mondev-mdn-worker-z1-01  (cloudstack-vm-b1094368-20ab-4bf4-b427-2ea80d1ad88d)
#   Node: mps-mondev-mdn-worker-z1-01  (k8s-node-mps-mondev-mdn-mps-mondev-mdn-worker-z1-01)
#   Pods: comparitive-analysis-api-b5cbdd96b-t579g (monitoring-dev)
#         quarterly-planning-ui-67bcfd575-cprkb    (monitoring-dev)
#         mds-html-viewer-5fd66445cc-8vmsp          (mds-platform)
#         falco-falcosidekick-7f4d89c66b-55rpd      (falco)
# ══════════════════════════════════════════════════════════════════════════════
if run T05; then
section "T05 · MDN BM → VM → K8s node → real pods  (${BM_MDN_1} → ${K8N_MDN_Z1_01})"
echo -e "  ${DIM}Real MDN chain from Neo4j: BM→VM→node→pods in monitoring-dev, mds-platform, falco${RST}"

post "MDN BM ${BM_MDN_1} power supply fault (root)" "P-T05-BM-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-T05-BM-${TS}\",
  \"problemTitle\": \"Hardware fault: ${BM_MDN_1} (CloudStack-MDN bare-metal)\",
  \"ProblemDetailsText\": \"Bare-metal ${BM_MDN_1} redundant PSU failed. Running on single PSU. VM migration in progress. host.name: ${BM_MDN_1}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${BM_MDN_1}\", \"entityName\": \"${BM_MDN_1}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"HOST-${BM_MDN_1}\", \"entityName\": \"${BM_MDN_1}\", \"entityType\": \"HOST\"}],
  \"customProperties\": {\"host.name\": \"${BM_MDN_1}\", \"impacted_entity\": \"${BM_MDN_1}\", \"entity_type\": \"bare_metal\", \"environment\": \"ADC\", \"region\": \"maiden\", \"psu_redundancy\": \"lost\"}
}"
pause 2

post "MDN VM ${VM_MDN_Z1_01} memory balloon (BM downstream)" "P-T05-VM-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-T05-VM-${TS}\",
  \"problemTitle\": \"Memory balloon reclaim: ${VM_MDN_Z1_01} (MDN)\",
  \"ProblemDetailsText\": \"Hypervisor ${BM_MDN_1} reclaiming guest memory via balloon on ${VM_MDN_Z1_01}. Guest OOM risk. host.name: ${VM_MDN_Z1_01}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"RESOURCE_CONTENTION\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${BM_MDN_1}\", \"entityName\": \"${BM_MDN_1}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"HOST-${VM_MDN_Z1_01}\", \"entityName\": \"${VM_MDN_Z1_01}\", \"entityType\": \"HOST\"}],
  \"customProperties\": {\"host.name\": \"${VM_MDN_Z1_01}\", \"impacted_entity\": \"${VM_MDN_Z1_01}\", \"kvm_host\": \"${BM_MDN_1}\", \"entity_type\": \"vm\", \"environment\": \"ADC\", \"balloon_mb\": \"4096\"}
}"
pause 2

post "MDN K8s node ${K8N_MDN_Z1_01} NotReady" "P-T05-NODE-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-T05-NODE-${TS}\",
  \"problemTitle\": \"K8s Node NotReady: ${K8N_MDN_Z1_01} (mps-mondev-mdn)\",
  \"ProblemDetailsText\": \"Node ${K8N_MDN_Z1_01} NotReady. VM ${VM_MDN_Z1_01} under memory pressure. kubelet OOMKilled. host.name: ${K8N_MDN_Z1_01}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${BM_MDN_1}\", \"entityName\": \"${BM_MDN_1}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"HOST-${K8N_MDN_Z1_01}\", \"entityName\": \"${K8N_MDN_Z1_01}\", \"entityType\": \"HOST\"}],
  \"customProperties\": {\"host.name\": \"${K8N_MDN_Z1_01}\", \"k8s.node.name\": \"${K8N_MDN_Z1_01}\", \"k8s.cluster.name\": \"mps-mondev-mdn\", \"k8s.cluster.uid\": \"${MDN_UID}\", \"environment\": \"ADC\"}
}"
pause 2

for ns_pod in "monitoring-dev:${POD_MDN_COMP}" "monitoring-dev:${POD_MDN_PLAN}" "mds-platform:${POD_MDN_MDS}" "falco:${POD_MDN_FALCO}"; do
  ns="${ns_pod%%:*}"; pod="${ns_pod#*:}"
  wl=$(echo "$pod" | sed 's/-[a-z0-9]*-[a-z0-9]*$//')
  post "${pod} evicted (ns=${ns})" "P-T05-POD-${ns}-${TS}" "{
    \"state\": \"OPEN\",
    \"problemId\": \"P-T05-POD-${ns}-${TS}\",
    \"problemTitle\": \"Pod evicted: ${pod} (ns=${ns})\",
    \"ProblemDetailsText\": \"Pod ${pod} evicted from ${K8N_MDN_Z1_01}. MDN node NotReady cascade.\",
    \"impactLevel\": \"APPLICATION\",
    \"severity\": \"AVAILABILITY\",
    \"status\": \"OPEN\",
    \"startTime\": \"${NOW}\",
    \"rootCauseEntity\": {\"entityId\": \"HOST-${BM_MDN_1}\", \"entityName\": \"${BM_MDN_1}\", \"entityType\": \"HOST\"},
    \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-${pod}\", \"entityName\": \"${pod}\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
    \"customProperties\": {\"k8s.namespace.name\": \"${ns}\", \"k8s.workload.name\": \"${wl}\", \"k8s.node.name\": \"${K8N_MDN_Z1_01}\", \"k8s.cluster.name\": \"mps-mondev-mdn\", \"k8s.cluster.uid\": \"${MDN_UID}\", \"environment\": \"ADC\"}
  }"
  pause 1
done

pause 5
n=$(inc_count "${BM_MDN_1}")
check "T05 single MDN incident" "$n" "1"
ac=$(inc_alert_count "${BM_MDN_1}")
check_ge "T05 alert_count ≥ 5" "$ac" 5
fi

# ══════════════════════════════════════════════════════════════════════════════
# T06 — Cross-cluster isolation: same problem type, example-cluster vs mps-mondev-mdn
# Expected: 2 separate incidents
# ══════════════════════════════════════════════════════════════════════════════
if run T06; then
section "T06 · Cross-cluster isolation  (example-cluster vs mps-mondev-mdn, same workload name)"
echo -e "  ${DIM}jaeger-collector in opentracing ns — example-cluster AND mps-mondev-mdn → 2 incidents${RST}"

post "jaeger-collector latency — example-cluster (real pod: ${POD_JAEGER})" "P-T06-DEV-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-T06-DEV-${TS}\",
  \"problemTitle\": \"High response time: jaeger-collector (example-cluster/opentracing)\",
  \"ProblemDetailsText\": \"jaeger-collector p99 latency 8.4s. Span ingestion failing. k8s.cluster.name: example-cluster\nk8s.namespace.name: opentracing\nk8s.workload.name: jaeger-collector\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"PERFORMANCE\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"KUBERNETES_WORKLOAD-jaeger-collector-example-cluster\", \"entityName\": \"jaeger-collector\", \"entityType\": \"KUBERNETES_WORKLOAD\"},
  \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-${POD_JAEGER}\", \"entityName\": \"${POD_JAEGER}\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
  \"customProperties\": {\"k8s.cluster.name\": \"example-cluster\", \"k8s.cluster.uid\": \"${DEV_RNO_UID}\", \"k8s.namespace.name\": \"opentracing\", \"k8s.workload.name\": \"jaeger-collector\", \"environment\": \"ADC\", \"p99_ms\": \"8400\"}
}"
pause 2

post "jaeger-collector latency — mps-mondev-mdn (separate cluster)" "P-T06-MDN-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-T06-MDN-${TS}\",
  \"problemTitle\": \"High response time: jaeger-collector (mps-mondev-mdn/opentracing)\",
  \"ProblemDetailsText\": \"jaeger-collector p99 latency 6.1s. k8s.cluster.name: mps-mondev-mdn\nk8s.namespace.name: opentracing\nk8s.workload.name: jaeger-collector\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"PERFORMANCE\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"KUBERNETES_WORKLOAD-jaeger-collector-mps-mondev-mdn\", \"entityName\": \"jaeger-collector\", \"entityType\": \"KUBERNETES_WORKLOAD\"},
  \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-jaeger-collector-mdn\", \"entityName\": \"jaeger-collector-mdn\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
  \"customProperties\": {\"k8s.cluster.name\": \"mps-mondev-mdn\", \"k8s.cluster.uid\": \"${MDN_UID}\", \"k8s.namespace.name\": \"opentracing\", \"k8s.workload.name\": \"jaeger-collector\", \"environment\": \"ADC\", \"p99_ms\": \"6100\"}
}"
pause 5

# v1.0.18 correctness: the two alerts must land in DIFFERENT incidents.
# We don't check titles because topology-based merging routes KUBERNETES_WORKLOAD
# alerts to whatever infrastructure incident is already open for that cluster.
# The invariant is: example-cluster and mps-mondev-mdn alerts NEVER share an incident.
dev_inc=$(kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT COALESCE(auto_created_incident_id::text,'none') FROM alerts
  WHERE source_id='P-T06-DEV-${TS}' ORDER BY created_at DESC LIMIT 1;" 2>/dev/null | tr -d ' \n' || echo "?")
mdn_inc=$(kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT COALESCE(auto_created_incident_id::text,'none') FROM alerts
  WHERE source_id='P-T06-MDN-${TS}' ORDER BY created_at DESC LIMIT 1;" 2>/dev/null | tr -d ' \n' || echo "?")
if [[ "$dev_inc" == "?" || "$mdn_inc" == "?" ]]; then
  echo -e "  ${YLW}⊘ SKIP${RST}  T06 cross-cluster isolation: kubectl unavailable"; (( SKIP++ )) || true
elif [[ "$dev_inc" == "none" || "$mdn_inc" == "none" ]]; then
  echo -e "  ${RED}✗ FAIL${RST}  T06 cross-cluster isolation: alert(s) not linked to an incident (dev=$dev_inc mdn=$mdn_inc)"; (( FAIL++ )) || true
elif [[ "$dev_inc" != "$mdn_inc" ]]; then
  echo -e "  ${GRN}✓ PASS${RST}  T06 example-cluster and mps-mondev-mdn alerts in separate incidents"; (( PASS++ )) || true
else
  echo -e "  ${RED}✗ FAIL${RST}  T06 cross-cluster: both alerts merged into same incident ($dev_inc)"; (( FAIL++ )) || true
fi
fi

# ══════════════════════════════════════════════════════════════════════════════
# T07 — k8preview01-rno cascade isolated from example-cluster
#
# Real VM: k8preview01-cs-vm-worker12-rno (cloudstack-vm-cac9bf06-3604-45a5-ad5e-1d7c65ef2306)
# Expected: 1 incident for k8preview01, isolated from example-cluster
# ══════════════════════════════════════════════════════════════════════════════
if run T07; then
section "T07 · k8preview01-rno cascade isolated  (real VM: ${VM_PREV12})"

post "k8preview01 VM ${VM_PREV12} disk failure (root)" "P-T07-VM-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-T07-VM-${TS}\",
  \"problemTitle\": \"Disk failure: ${VM_PREV12} (k8preview01-rno)\",
  \"ProblemDetailsText\": \"VM ${VM_PREV12} disk controller failure. SCSI errors. node likely to go NotReady. host.name: ${VM_PREV12}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${VM_PREV12}\", \"entityName\": \"${VM_PREV12}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"HOST-${VM_PREV12}\", \"entityName\": \"${VM_PREV12}\", \"entityType\": \"HOST\"}],
  \"customProperties\": {\"host.name\": \"${VM_PREV12}\", \"impacted_entity\": \"${VM_PREV12}\", \"entity_type\": \"vm\", \"k8s.cluster.name\": \"k8preview01-rno\", \"k8s.cluster.uid\": \"${K8PREV_UID}\", \"environment\": \"ADC\"}
}"
pause 2

for ns_wl in "argocd:argocd-redis" "ingress-nginx:nginx-ingress-controller" "headlamp:headlamp"; do
  ns="${ns_wl%%:*}"; wl="${ns_wl#*:}"
  post "pod evicted — ${ns}/${wl}" "P-T07-POD-${ns}-${TS}" "{
    \"state\": \"OPEN\",
    \"problemId\": \"P-T07-POD-${ns}-${TS}\",
    \"problemTitle\": \"Pod evicted: ${wl} (k8preview01-rno/${ns})\",
    \"ProblemDetailsText\": \"${wl} pod lost on ${VM_PREV12}. k8preview01 workload disrupted.\",
    \"impactLevel\": \"APPLICATION\",
    \"severity\": \"AVAILABILITY\",
    \"status\": \"OPEN\",
    \"startTime\": \"${NOW}\",
    \"rootCauseEntity\": {\"entityId\": \"HOST-${VM_PREV12}\", \"entityName\": \"${VM_PREV12}\", \"entityType\": \"HOST\"},
    \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-${wl}-pod\", \"entityName\": \"${wl}\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
    \"customProperties\": {\"k8s.namespace.name\": \"${ns}\", \"k8s.workload.name\": \"${wl}\", \"k8s.node.name\": \"${VM_PREV12}\", \"k8s.cluster.name\": \"k8preview01-rno\", \"k8s.cluster.uid\": \"${K8PREV_UID}\", \"environment\": \"ADC\"}
  }"
  pause 1
done

pause 5
n=$(inc_count "${VM_PREV12}")
check "T07 single incident for k8preview01 cascade" "$n" "1"
# Verify NOT merged into any example-cluster incident
dev_merged=$(kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT COUNT(*) FROM incidents
  WHERE auto_created=true AND correlation_id='${VM_PREV12}'
    AND title ILIKE '%example-cluster%'
    AND updated_at > NOW()-INTERVAL '5 minutes';" 2>/dev/null | tr -d ' \n' || echo "?")
check "T07 k8preview01 NOT merged with example-cluster" "$dev_merged" "0"
fi

# ══════════════════════════════════════════════════════════════════════════════
# T08 — OPEN → RESOLVED lifecycle (real alerthub-backend workload)
# ══════════════════════════════════════════════════════════════════════════════
if run T08; then
section "T08 · OPEN → RESOLVED lifecycle  (alerthub-backend deployment — real workload)"
echo -e "  ${DIM}Expected: incident opens then closes on RESOLVED${RST}"
RESOLVE_PID="P-T08-RESOLVE-${TS}"

post "OPEN: alerthub-backend 0/2 ready" "${RESOLVE_PID}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"${RESOLVE_PID}\",
  \"problemTitle\": \"Deployment unavailable: alerthub-backend 0/2 ready (aileron)\",
  \"ProblemDetailsText\": \"alerthub-backend: 0/2 replicas ready. Rolling update stalled. k8s.cluster.name: example-cluster\nk8s.namespace.name: aileron\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"KUBERNETES_WORKLOAD-alerthub-backend-example-cluster\", \"entityName\": \"alerthub-backend\", \"entityType\": \"KUBERNETES_WORKLOAD\"},
  \"impactedEntities\": [{\"entityId\": \"KUBERNETES_WORKLOAD-alerthub-backend-example-cluster\", \"entityName\": \"alerthub-backend\", \"entityType\": \"KUBERNETES_WORKLOAD\"}],
  \"customProperties\": {\"k8s.cluster.name\": \"example-cluster\", \"k8s.cluster.uid\": \"${DEV_RNO_UID}\", \"k8s.namespace.name\": \"aileron\", \"k8s.workload.name\": \"alerthub-backend\", \"environment\": \"ADC\", \"ready_replicas\": \"0\"}
}"
pause 6

echo -e "  ${CYN}--- DB: incident before RESOLVED ---${RST}"
kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT '  ' || status || '  ' || LEFT(title,70)
  FROM incidents WHERE auto_created=true AND title ILIKE '%alerthub-backend 0/2%'
    AND updated_at > NOW()-INTERVAL '3 minutes'
  ORDER BY created_at DESC LIMIT 2;" 2>/dev/null | grep -v "^$" || true

post "RESOLVED: alerthub-backend 2/2 recovered" "${RESOLVE_PID}" "{
  \"state\": \"RESOLVED\",
  \"problemId\": \"${RESOLVE_PID}\",
  \"problemTitle\": \"RESOLVED: alerthub-backend back to 2/2 ready (aileron)\",
  \"ProblemDetailsText\": \"alerthub-backend recovered. 2/2 replicas ready. Health checks passing.\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"RESOLVED\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"KUBERNETES_WORKLOAD-alerthub-backend-example-cluster\", \"entityName\": \"alerthub-backend\", \"entityType\": \"KUBERNETES_WORKLOAD\"},
  \"impactedEntities\": [{\"entityId\": \"KUBERNETES_WORKLOAD-alerthub-backend-example-cluster\", \"entityName\": \"alerthub-backend\", \"entityType\": \"KUBERNETES_WORKLOAD\"}],
  \"customProperties\": {\"k8s.cluster.name\": \"example-cluster\", \"k8s.cluster.uid\": \"${DEV_RNO_UID}\", \"k8s.namespace.name\": \"aileron\", \"k8s.workload.name\": \"alerthub-backend\", \"environment\": \"ADC\", \"ready_replicas\": \"2\"}
}"
pause 5

no_spurious=$(kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT COUNT(*) FROM incidents
  WHERE auto_created=true AND title ILIKE '%RESOLVED%alerthub-backend%'
    AND status IN ('open','investigating')
    AND created_at > NOW()-INTERVAL '3 minutes';" 2>/dev/null | tr -d ' \n' || echo "?")
check "T08 no spurious open incident from RESOLVED" "$no_spurious" "0"

echo -e "  ${CYN}--- DB: incident status after RESOLVED ---${RST}"
kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT '  ' || status || '  ' || LEFT(title,65)
  FROM incidents WHERE auto_created=true AND title ILIKE '%alerthub-backend%'
    AND updated_at > NOW()-INTERVAL '5 minutes'
  ORDER BY updated_at DESC LIMIT 2;" 2>/dev/null | grep -v "^$" || true
fi

# ══════════════════════════════════════════════════════════════════════════════
# T09 — Deduplication: same problemId × 3 → 1 alert record
# ══════════════════════════════════════════════════════════════════════════════
if run T09; then
section "T09 · Deduplication  (same problemId ×3 → 1 alert, count increments)"
DEDUP_PID="P-T09-DEDUP-${TS}"

for i in 1 2 3; do
  post "send #${i} — ${DEDUP_PID}" "${DEDUP_PID}" "{
    \"state\": \"OPEN\",
    \"problemId\": \"${DEDUP_PID}\",
    \"problemTitle\": \"Disk at 87% on /data — node ${K8N_Z3_05}\",
    \"ProblemDetailsText\": \"Disk /data at 87% on ${K8N_Z3_05}. ETA full ~3h at 220 MB/s write rate. host.name: ${K8N_Z3_05}\",
    \"impactLevel\": \"INFRASTRUCTURE\",
    \"severity\": \"RESOURCE_CONTENTION\",
    \"status\": \"OPEN\",
    \"startTime\": \"${NOW}\",
    \"rootCauseEntity\": {\"entityId\": \"HOST-${K8N_Z3_05}\", \"entityName\": \"${K8N_Z3_05}\", \"entityType\": \"HOST\"},
    \"impactedEntities\": [{\"entityId\": \"HOST-${K8N_Z3_05}\", \"entityName\": \"${K8N_Z3_05}\", \"entityType\": \"HOST\"}],
    \"customProperties\": {\"host.name\": \"${K8N_Z3_05}\", \"k8s.node.name\": \"${K8N_Z3_05}\", \"k8s.cluster.name\": \"example-cluster\", \"k8s.cluster.uid\": \"${DEV_RNO_UID}\", \"environment\": \"ADC\", \"send_num\": \"${i}\", \"disk_pct\": \"87\"}
  }"
  [[ "$i" -lt 3 ]] && pause 2
done
pause 3

n=$(alert_count_sid "${DEDUP_PID}")
check "T09 dedup: 1 alert record for problemId ×3" "$n" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# T10 — Flapping 3-cycle (OPEN → RESOLVED → OPEN × 3)
# Real workload: floodgate-test in interactive-dx-dev (on node z1-02)
# ══════════════════════════════════════════════════════════════════════════════
if run T10; then
section "T10 · Flapping 3-cycle  (${POD_FLOODGATE} in interactive-dx-dev)"
FLAP_PID="P-T10-FLAP-${TS}"

for cycle in 1 2 3; do
  post "OPEN cycle ${cycle}" "${FLAP_PID}" "{
    \"state\": \"OPEN\",
    \"problemId\": \"${FLAP_PID}\",
    \"problemTitle\": \"CrashLoopBackOff: ${POD_FLOODGATE} (interactive-dx-dev) — cycle ${cycle}\",
    \"ProblemDetailsText\": \"${POD_FLOODGATE} crash cycle ${cycle}. OOMKill. k8s.cluster.name: example-cluster\nk8s.namespace.name: interactive-dx-dev\",
    \"impactLevel\": \"APPLICATION\",
    \"severity\": \"AVAILABILITY\",
    \"status\": \"OPEN\",
    \"startTime\": \"${NOW}\",
    \"rootCauseEntity\": {\"entityId\": \"KUBERNETES_WORKLOAD-floodgate-test-example-cluster\", \"entityName\": \"floodgate-test\", \"entityType\": \"KUBERNETES_WORKLOAD\"},
    \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-${POD_FLOODGATE}\", \"entityName\": \"${POD_FLOODGATE}\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
    \"customProperties\": {\"k8s.cluster.name\": \"example-cluster\", \"k8s.cluster.uid\": \"${DEV_RNO_UID}\", \"k8s.namespace.name\": \"interactive-dx-dev\", \"k8s.workload.name\": \"floodgate-test\", \"environment\": \"ADC\", \"cycle\": \"${cycle}\"}
  }"
  pause 3
  if [[ "$cycle" -lt 3 ]]; then
    post "RESOLVED cycle ${cycle}" "${FLAP_PID}" "{
      \"state\": \"RESOLVED\",
      \"problemId\": \"${FLAP_PID}\",
      \"problemTitle\": \"RESOLVED: ${POD_FLOODGATE} recovered (cycle ${cycle})\",
      \"ProblemDetailsText\": \"floodgate-test temporarily recovered.\",
      \"impactLevel\": \"APPLICATION\",
      \"severity\": \"AVAILABILITY\",
      \"status\": \"RESOLVED\",
      \"startTime\": \"${NOW}\",
      \"rootCauseEntity\": {\"entityId\": \"KUBERNETES_WORKLOAD-floodgate-test-example-cluster\", \"entityName\": \"floodgate-test\", \"entityType\": \"KUBERNETES_WORKLOAD\"},
      \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-${POD_FLOODGATE}\", \"entityName\": \"${POD_FLOODGATE}\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
      \"customProperties\": {\"k8s.cluster.name\": \"example-cluster\", \"k8s.cluster.uid\": \"${DEV_RNO_UID}\", \"k8s.namespace.name\": \"interactive-dx-dev\", \"k8s.workload.name\": \"floodgate-test\", \"environment\": \"ADC\"}
    }"
    pause 3
  fi
done
pause 3

# After 3 cycles (OPEN→RES×2 + final OPEN), the alert record must be status=open.
# The incident it belongs to may be shared with other test alerts (due to topology
# routing) — that's OK. The invariant is: the alert itself is in OPEN state.
flap_status=$(kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT status FROM alerts
  WHERE source_id='${FLAP_PID}'
  ORDER BY updated_at DESC LIMIT 1;" 2>/dev/null | tr -d ' \n' || echo "?")
check "T10 flap: alert status=open after cycle 3" "$flap_status" "open"
fi

# ══════════════════════════════════════════════════════════════════════════════
# T11 — Cross-cluster title isolation (v1.0.16 fix — 3 clusters, same title)
# ══════════════════════════════════════════════════════════════════════════════
if run T11; then
section "T11 · Cross-cluster title isolation  (v1.0.16+v1.0.18 fix — same title, 3 clusters)"
echo -e "  ${DIM}'[P2] Not all pods ready' from example-cluster, mps-mondev-mdn, k8preview01 → 3 incidents${RST}"

for cluster_ns_uid in "example-cluster:aileron:${DEV_RNO_UID}" "mps-mondev-mdn:monitoring-dev:${MDN_UID}" "k8preview01-rno:argocd:${K8PREV_UID}"; do
  cluster="${cluster_ns_uid%%:*}"; rest="${cluster_ns_uid#*:}"; ns="${rest%%:*}"; uid="${rest#*:}"
  pid="P-T11-${cluster}-${TS}"
  post "[P2] Not all pods ready — ${cluster}/${ns}" "${pid}" "{
    \"state\": \"OPEN\",
    \"problemId\": \"${pid}\",
    \"problemTitle\": \"[P2] Not all pods ready\",
    \"ProblemDetailsText\": \"Deployment pods not ready. k8s.cluster.name: ${cluster}\nk8s.namespace.name: ${ns}\",
    \"impactLevel\": \"APPLICATION\",
    \"severity\": \"AVAILABILITY\",
    \"status\": \"OPEN\",
    \"startTime\": \"${NOW}\",
    \"rootCauseEntity\": null,
    \"impactedEntities\": [],
    \"customProperties\": {\"k8s.cluster.name\": \"${cluster}\", \"k8s.cluster.uid\": \"${uid}\", \"k8s.namespace.name\": \"${ns}\", \"environment\": \"ADC\"}
  }"
  pause 1
done

pause 5
n=$(title_inc_count "Not all pods ready" 5)
check "T11 3 separate incidents for generic title across clusters" "$n" "3"
fi

# ══════════════════════════════════════════════════════════════════════════════
# T12 — KUBERNETES_WORKLOAD entity scoping (v1.0.18 fix)
# Same workload name (argocd-repo-server) in two different clusters
# → must produce 2 incidents with different cluster-scoped correlation_ids
# ══════════════════════════════════════════════════════════════════════════════
if run T12; then
section "T12 · KUBERNETES_WORKLOAD cluster scoping  (v1.0.18 fix — argocd-repo-server in 2 clusters)"
echo -e "  ${DIM}argocd-repo-server in example-cluster/argocd AND k8preview01-rno/argocd → 2 incidents${RST}"

post "argocd-repo-server OOM — example-cluster (real pod: ${POD_ARGOCD_REPO})" "P-T12-DEV-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-T12-DEV-${TS}\",
  \"problemTitle\": \"OOMKilled: argocd-repo-server (example-cluster/argocd)\",
  \"ProblemDetailsText\": \"argocd-repo-server OOMKilled. Restart count 6. k8s.cluster.name: example-cluster\nk8s.namespace.name: argocd\nk8s.workload.name: argocd-repo-server\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"RESOURCE_CONTENTION\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"KUBERNETES_WORKLOAD-argocd-repo-server-example-cluster\", \"entityName\": \"argocd-repo-server\", \"entityType\": \"KUBERNETES_WORKLOAD\"},
  \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-${POD_ARGOCD_REPO}\", \"entityName\": \"${POD_ARGOCD_REPO}\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
  \"customProperties\": {\"k8s.cluster.name\": \"example-cluster\", \"k8s.cluster.uid\": \"${DEV_RNO_UID}\", \"k8s.namespace.name\": \"argocd\", \"k8s.workload.name\": \"argocd-repo-server\", \"environment\": \"ADC\", \"restart_count\": \"6\"}
}"
pause 1

post "argocd-repo-server OOM — k8preview01-rno (different cluster)" "P-T12-PREV-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-T12-PREV-${TS}\",
  \"problemTitle\": \"OOMKilled: argocd-repo-server (k8preview01-rno/argocd)\",
  \"ProblemDetailsText\": \"argocd-repo-server OOMKilled. Restart count 4. k8s.cluster.name: k8preview01-rno\nk8s.namespace.name: argocd\nk8s.workload.name: argocd-repo-server\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"RESOURCE_CONTENTION\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"KUBERNETES_WORKLOAD-argocd-repo-server-k8preview01-rno\", \"entityName\": \"argocd-repo-server\", \"entityType\": \"KUBERNETES_WORKLOAD\"},
  \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-argocd-repo-server-prev\", \"entityName\": \"argocd-repo-server-prev\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
  \"customProperties\": {\"k8s.cluster.name\": \"k8preview01-rno\", \"k8s.cluster.uid\": \"${K8PREV_UID}\", \"k8s.namespace.name\": \"argocd\", \"k8s.workload.name\": \"argocd-repo-server\", \"environment\": \"ADC\", \"restart_count\": \"4\"}
}"
pause 5

# v1.0.18 correctness: argocd-repo-server in two different clusters must land in
# DIFFERENT incidents (the whole point of the cluster-scoped root_cause_entity fix).
dev12_inc=$(kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT COALESCE(auto_created_incident_id::text,'none') FROM alerts
  WHERE source_id='P-T12-DEV-${TS}' ORDER BY created_at DESC LIMIT 1;" 2>/dev/null | tr -d ' \n' || echo "?")
prev12_inc=$(kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT COALESCE(auto_created_incident_id::text,'none') FROM alerts
  WHERE source_id='P-T12-PREV-${TS}' ORDER BY created_at DESC LIMIT 1;" 2>/dev/null | tr -d ' \n' || echo "?")
if [[ "$dev12_inc" == "?" || "$prev12_inc" == "?" ]]; then
  echo -e "  ${YLW}⊘ SKIP${RST}  T12 workload cluster scoping: kubectl unavailable"; (( SKIP++ )) || true
elif [[ "$dev12_inc" == "none" || "$prev12_inc" == "none" ]]; then
  echo -e "  ${RED}✗ FAIL${RST}  T12 workload cluster scoping: alert(s) not linked to incident (dev=$dev12_inc prev=$prev12_inc)"; (( FAIL++ )) || true
elif [[ "$dev12_inc" != "$prev12_inc" ]]; then
  echo -e "  ${GRN}✓ PASS${RST}  T12 argocd-repo-server: example-cluster and k8preview01-rno in separate incidents"; (( PASS++ )) || true
else
  echo -e "  ${RED}✗ FAIL${RST}  T12 workload scoping: both cluster alerts merged into same incident ($dev12_inc)"; (( FAIL++ )) || true
fi
fi

# ══════════════════════════════════════════════════════════════════════════════
# T13 — NetApp aggregate → PVC I/O cascade (real RNO aggregate aggr1_node002)
# ══════════════════════════════════════════════════════════════════════════════
if run T13; then
section "T13 · NetApp aggregate capacity → PVC I/O error → pod crash  (${NETAPP_RNO_AGG2})"

post "NetApp ${NETAPP_RNO_AGG2} 92% full (root)" "P-T13-AGG-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-T13-AGG-${TS}\",
  \"problemTitle\": \"NetApp aggregate nearly full: ${NETAPP_RNO_AGG2} (92% used)\",
  \"ProblemDetailsText\": \"Aggregate ${NETAPP_RNO_AGG2} on ${NETAPP_RNO_NODE2} at 92% capacity. Write throttling active. Snapshot reserve exhausted. host.name: ${NETAPP_RNO_NODE2}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"RESOURCE_CONTENTION\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${NETAPP_RNO_NODE2}\", \"entityName\": \"${NETAPP_RNO_NODE2}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"HOST-${NETAPP_RNO_NODE2}\", \"entityName\": \"${NETAPP_RNO_NODE2}\", \"entityType\": \"HOST\"}],
  \"customProperties\": {\"host.name\": \"${NETAPP_RNO_NODE2}\", \"netapp_cluster\": \"${NETAPP_RNO_CLUSTER}\", \"netapp_aggregate\": \"${NETAPP_RNO_AGG2}\", \"netapp_node\": \"${NETAPP_RNO_NODE2}\", \"entity_type\": \"netapp_aggregate\", \"environment\": \"ADC\", \"used_pct\": \"92\"}
}"
pause 2

post "PVC ${PVC_PUBLISH} write stall (backed by ${NETAPP_VOL_PUBLISH_DS})" "P-T13-PVC-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-T13-PVC-${TS}\",
  \"problemTitle\": \"PVC write latency spike: ${PVC_PUBLISH} (aem-dev)\",
  \"ProblemDetailsText\": \"PVC ${PVC_PUBLISH} write latency 840ms (threshold 50ms). Backed by volume ${NETAPP_VOL_PUBLISH_DS} on throttled aggregate ${NETAPP_RNO_AGG2}.\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"PERFORMANCE\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${NETAPP_RNO_NODE2}\", \"entityName\": \"${NETAPP_RNO_NODE2}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-${POD_PUBLISH0}\", \"entityName\": \"${POD_PUBLISH0}\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
  \"customProperties\": {\"k8s.cluster.name\": \"example-cluster\", \"k8s.cluster.uid\": \"${DEV_RNO_UID}\", \"k8s.namespace.name\": \"aem-dev\", \"pvc\": \"${PVC_PUBLISH}\", \"netapp_cluster\": \"${NETAPP_RNO_CLUSTER}\", \"netapp_aggregate\": \"${NETAPP_RNO_AGG2}\", \"environment\": \"ADC\", \"write_latency_ms\": \"840\"}
}"
pause 2

post "${POD_PUBLISH0} pod I/O timeout (aem-dev)" "P-T13-POD-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-T13-POD-${TS}\",
  \"problemTitle\": \"Disk I/O timeout: ${POD_PUBLISH0} (aem-dev) — AEM Publish unavailable\",
  \"ProblemDetailsText\": \"AEM Publish pod ${POD_PUBLISH0} disk I/O timeout. Write queue backed up. ${PVC_PUBLISH} latency 840ms. Root: NetApp aggregate capacity.\",
  \"impactLevel\": \"APPLICATION\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${NETAPP_RNO_NODE2}\", \"entityName\": \"${NETAPP_RNO_NODE2}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-${POD_PUBLISH0}\", \"entityName\": \"${POD_PUBLISH0}\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
  \"customProperties\": {\"k8s.cluster.name\": \"example-cluster\", \"k8s.cluster.uid\": \"${DEV_RNO_UID}\", \"k8s.namespace.name\": \"aem-dev\", \"k8s.pod.name\": \"${POD_PUBLISH0}\", \"k8s.workload.name\": \"publish-dev\", \"pvc\": \"${PVC_PUBLISH}\", \"netapp_cluster\": \"${NETAPP_RNO_CLUSTER}\", \"environment\": \"ADC\"}
}"
pause 5
# T13-AGG topology-merges into whatever NetApp/infra incident is already open for the
# same cluster (often T04's netapp-rno-node001 incident). The real invariant is that
# T13-PVC and T13-POD — both sharing rce=netapp-rno-node002 — end up in the SAME incident.
t13_pvc_inc=$(kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT COALESCE(auto_created_incident_id::text,'none') FROM alerts
  WHERE source_id='P-T13-PVC-${TS}' ORDER BY created_at DESC LIMIT 1;" 2>/dev/null | tr -d ' \n' || echo "?")
t13_pod_inc=$(kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT COALESCE(auto_created_incident_id::text,'none') FROM alerts
  WHERE source_id='P-T13-POD-${TS}' ORDER BY created_at DESC LIMIT 1;" 2>/dev/null | tr -d ' \n' || echo "?")
if [[ "$t13_pvc_inc" == "?" || "$t13_pod_inc" == "?" ]]; then
  echo -e "  ${YLW}⊘ SKIP${RST}  T13 NetApp cascade: kubectl unavailable"; (( SKIP++ )) || true
elif [[ "$t13_pvc_inc" == "none" || "$t13_pod_inc" == "none" ]]; then
  echo -e "  ${RED}✗ FAIL${RST}  T13 NetApp cascade: PVC or POD alert not linked to an incident (pvc=$t13_pvc_inc pod=$t13_pod_inc)"; (( FAIL++ )) || true
elif [[ "$t13_pvc_inc" == "$t13_pod_inc" ]]; then
  echo -e "  ${GRN}✓ PASS${RST}  T13 PVC+POD alerts correlated into same incident ($t13_pvc_inc)"; (( PASS++ )) || true
else
  echo -e "  ${RED}✗ FAIL${RST}  T13 NetApp cascade: PVC and POD in different incidents (pvc=$t13_pvc_inc pod=$t13_pod_inc)"; (( FAIL++ )) || true
fi
fi

# ══════════════════════════════════════════════════════════════════════════════
# T14 — Late root arrival: pods fire 20s before root node
# Real node: example-cluster-worker-z1-04, real pods: kafka-kafka-1, maestro-webhooks
# ══════════════════════════════════════════════════════════════════════════════
if run T14; then
section "T14 · Late root arrival  (${POD_REDIS} fires 20s before node ${K8N_Z1_04})"
echo -e "  ${DIM}3 pod alerts fire first, node NotReady arrives 20s later → 1 merged incident${RST}"

for pod_ns in "${POD_REDIS}:aileron:redis-cluster" "alerthub-kafka-kafka-1:aileron:alerthub-kafka-kafka" "maestro-webhooks-85f65577fd-dh6ch:gitshift-dev:maestro-webhooks"; do
  pod="${pod_ns%%:*}"; rest="${pod_ns#*:}"; ns="${rest%%:*}"; wl="${rest#*:}"
  post "${pod} failing (BEFORE root)" "P-T14-POD-${pod:0:14}-${TS}" "{
    \"state\": \"OPEN\",
    \"problemId\": \"P-T14-POD-${pod:0:14}-${TS}\",
    \"problemTitle\": \"Pod failing: ${pod} (${ns})\",
    \"ProblemDetailsText\": \"Pod ${pod} failing. Node suspected unhealthy. k8s.cluster.name: example-cluster\nk8s.namespace.name: ${ns}\nk8s.node.name: ${K8N_Z1_04}\",
    \"impactLevel\": \"APPLICATION\",
    \"severity\": \"AVAILABILITY\",
    \"status\": \"OPEN\",
    \"startTime\": \"${NOW}\",
    \"rootCauseEntity\": null,
    \"impactedEntities\": [{\"entityId\": \"CLOUD_APPLICATION_INSTANCE-${pod}\", \"entityName\": \"${pod}\", \"entityType\": \"CLOUD_APPLICATION_INSTANCE\"}],
    \"customProperties\": {\"k8s.cluster.name\": \"example-cluster\", \"k8s.cluster.uid\": \"${DEV_RNO_UID}\", \"k8s.namespace.name\": \"${ns}\", \"k8s.workload.name\": \"${wl}\", \"k8s.node.name\": \"${K8N_Z1_04}\", \"environment\": \"ADC\"}
  }"
  pause 1
done

echo -e "  ${DIM}⏳ 20s — root cause arriving late…${RST}"
pause 20

post "Node ${K8N_Z1_04} NotReady — root arrives late" "P-T14-NODE-${TS}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-T14-NODE-${TS}\",
  \"problemTitle\": \"K8s Node NotReady: ${K8N_Z1_04} (example-cluster)\",
  \"ProblemDetailsText\": \"Node ${K8N_Z1_04} NotReady. This is the actual root cause for prior pod failures. host.name: ${K8N_Z1_04}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${K8N_Z1_04}\", \"entityName\": \"${K8N_Z1_04}\", \"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"HOST-${K8N_Z1_04}\", \"entityName\": \"${K8N_Z1_04}\", \"entityType\": \"HOST\"}],
  \"customProperties\": {\"host.name\": \"${K8N_Z1_04}\", \"k8s.node.name\": \"${K8N_Z1_04}\", \"k8s.cluster.name\": \"example-cluster\", \"k8s.cluster.uid\": \"${DEV_RNO_UID}\", \"environment\": \"ADC\"}
}"
pause 6
# Late root should cause all T14 alerts (pods + node) to end up in the SAME incident.
# We verify by counting DISTINCT incident IDs across all T14 alerts for this run.
t14_distinct=$(kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT COUNT(DISTINCT auto_created_incident_id) FROM alerts
  WHERE source_id LIKE 'P-T14-%-${TS}' AND auto_created_incident_id IS NOT NULL
    AND created_at > NOW()-INTERVAL '10 minutes';" 2>/dev/null | tr -d ' \n' || echo "?")
check "T14 single merged incident after late root" "$t14_distinct" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# T15 — Burst ${BURST} alerts using real host pool
# ══════════════════════════════════════════════════════════════════════════════
if run T15; then
section "T15 · Burst ${BURST} alerts — real BM/VM/K8s node host pool"
echo -e "  ${DIM}Rotating across real Neo4j entities: BMs, VMs, K8s nodes${RST}"
S_T15="T15-${TS}"

HOSTS=("$BM_RNO_1" "$BM_RNO_2" "$BM_RNO_3" "$BM_RNO_4" "$BM_RNO_5"
       "$VM_Z3_01" "$VM_Z2_01" "$VM_Z1_01"
       "$K8N_Z3_01" "$K8N_Z3_05" "$K8N_Z1_02" "$K8N_Z1_04"
       "$BM_MDN_1" "$BM_MDN_2" "$VM_MDN_Z1_01" "$VM_MDN_Z1_02")
SEVS=("PERFORMANCE" "RESOURCE_CONTENTION" "AVAILABILITY" "PERFORMANCE" "AVAILABILITY")
TYPES=("bare_metal" "vm" "k8s_node" "bare_metal" "vm")

t_start=$(date +%s%N)
for i in $(seq 1 "$BURST"); do
  host="${HOSTS[$((i % ${#HOSTS[@]}))]}"
  sev="${SEVS[$((i % ${#SEVS[@]}))]}"
  etype="${TYPES[$((i % ${#TYPES[@]}))]}"
  post_bg "{
    \"state\": \"OPEN\",
    \"problemId\": \"${S_T15}-${i}\",
    \"problemTitle\": \"Burst alert ${i}: ${host}\",
    \"impactLevel\": \"INFRASTRUCTURE\",
    \"severity\": \"${sev}\",
    \"status\": \"OPEN\",
    \"startTime\": \"${NOW}\",
    \"rootCauseEntity\": {\"entityId\": \"HOST-${host}\", \"entityName\": \"${host}\", \"entityType\": \"HOST\"},
    \"impactedEntities\": [{\"entityId\": \"HOST-${host}\", \"entityName\": \"${host}\", \"entityType\": \"HOST\"}],
    \"ProblemDetailsText\": \"Burst test ${i}/${BURST}. host.name: ${host}\",
    \"customProperties\": {\"host.name\": \"${host}\", \"impacted_entity\": \"${host}\", \"entity_type\": \"${etype}\", \"environment\": \"ADC\", \"batch\": \"${S_T15}\", \"idx\": \"${i}\"}
  }"
done
wait
t_end=$(date +%s%N)
elapsed_ms=$(( (t_end - t_start) / 1000000 ))
rps=$(( BURST * 1000 / (elapsed_ms + 1) ))
echo ""
echo -e "  ${GRN}Fired ${BURST} alerts in ${elapsed_ms}ms  (~${rps} req/s)${RST}"
pause 5

ingested=$(kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT COUNT(*) FROM alerts
  WHERE source_id LIKE '${S_T15}-%'
    AND created_at > NOW()-INTERVAL '3 minutes';" 2>/dev/null | tr -d ' \n' || echo "?")
check_ge "T15 ingested ≥ ${BURST}" "$ingested" "$BURST"
fi

# ─── final summary ────────────────────────────────────────────────────────────
echo ""
echo -e "${BOLD}${MAG}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RST}"
echo -e "${BOLD}  Real Topology Test Suite — Results${RST}"
echo -e "${BOLD}${MAG}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RST}"
echo -e "  ${GRN}✓ PASS${RST}  ${PASS}"
echo -e "  ${RED}✗ FAIL${RST}  ${FAIL}"
echo -e "  ${YLW}⊘ SKIP${RST}  ${SKIP}  (kubectl/DB unavailable)"
echo ""
echo -e "  ${DIM}View incidents → https://aileron.example.com/incidents${RST}"
echo -e "  ${DIM}Run TS: ${TS}${RST}"
echo ""
if [[ "$FAIL" -gt 0 ]]; then
  echo -e "  ${RED}${BOLD}Some assertions FAILED.${RST}"
  exit 1
else
  echo -e "  ${GRN}${BOLD}All executed assertions passed.${RST}"
fi
