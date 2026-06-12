#!/usr/bin/env bash
# test_all_entity_types.sh — comprehensive entity-type + correlation coverage
#
# Tests EVERY supported entity type with real topology data.
# Each scenario explains WHAT is being tested and WHY alerts should correlate
# (or stay separate).  Designed to catch regressions across the full pipeline:
#   Dynatrace normalizer → RootCauseEngine (RCE) → ParallelCorrelationEngine
#   → CorrelationAggregatorService → EvolutionEngine → CACIE
#
# ── ENTITY TYPES COVERED ──────────────────────────────────────────────────────
#
#  Type              Dynatrace entityType            InfraLevel  Family
#  ---------------   -------------------------       ----------  -------
#  bare_metal        HOST + entity_type=bare_metal        5      vm
#  kvm               HOST + entity_type=kvm               4      vm
#  vm                HOST + entity_type=vm                3      vm
#  k8s_node          KUBERNETES_NODE                      2      k8s
#  k8s_workload      KUBERNETES_WORKLOAD                  1      k8s
#  k8s_cluster       KUBERNETES_CLUSTER                   2      k8s
#  netapp_cluster    entity_type=netapp_cluster           –      netapp
#  netapp_aggregate  entity_type=netapp_aggregate         –      netapp
#  cloud_application CLOUD_APPLICATION                    –      app
#  process           PROCESS_GROUP_INSTANCE               –      vm
#
# ── REAL TOPOLOGY (pulled from Postgres + kubectl) ────────────────────────────
#
#  BM1  cloudstack-cluster-2-iapps-100-67-61-18          (bare_metal, RNO DC)
#  BM2  cloudstack-cluster-2-iapps-100-67-61-31          (bare_metal, RNO DC)
#  BM_MDN  mps-mondev-mdn-worker-z3-05                   (bare_metal, MDN DC)
#  VM1  cloudstack-cluster-2-mps-nonprod-rno-worker-z3-08 → node z3-08
#  VM2  cloudstack-cluster-2-mps-nonprod-rno-worker-z3-13 → node z3-13
#
#  Clusters:
#    mps-nonprod-rno  UID=88b12bf2-9f23-4b4a-b06a-3d2b33134a3b
#    mps-mondev-mdn   UID=00a07750-e556-443e-89d9-80341edb472d
#    k8preview01-rno  UID=d1be9a2f-4e83-49bf-8800-b9a2bc2e7da0
#
#  NetApp:  netapp-mdn-cluster001 (MDN datacenter)
#
#  Ghost BM (decommissioned, still in DB):
#    decommissioned-bm-rack42-iapps-10-100-45-91
#
# ── SCENARIOS ─────────────────────────────────────────────────────────────────
#
#   1  Full BM→VM→Node→Workloads cascade (6-alert, InfraLevel 5→3→2→1)
#   2  KVM hypervisor failure → two VMs (InfraLevel=4 root)
#   3  VM-only root (no BM parent, VM IS root — InfraLevel 3 Stage-3)
#   4  K8s node root only (no VM/BM parent alert — node IS root)
#   5  NetApp cluster → aggregate fan-out (netapp entity family)
#   6  NetApp + VM simultaneously → MUST be 2 separate incidents (family guard)
#   7  CLOUD_APPLICATION service cascade (APM, no infra alerts)
#   8  PROCESS_GROUP_INSTANCE crash → workload error (process entity type)
#   9  Cross-cluster isolation: same workload in 2 clusters → 2 incidents
#  10  Ghost/decommissioned entity alert (isolated from live alerts)
#  11  Late-arriving root cause (downstreams first → EvolutionEngine merge)
#  12  Oscillation guard: cascade → resolve → re-send (5-min convergence guard)
#  13  Full resolution: BM→workloads cascade then all RESOLVED
#  14  Multi-DC: RNO BM + MDN BM simultaneously → 2 separate incidents
#
# ── USAGE ─────────────────────────────────────────────────────────────────────
#   bash scripts/test_all_entity_types.sh           # all scenarios
#   bash scripts/test_all_entity_types.sh 1         # single scenario
#   bash scripts/test_all_entity_types.sh 1 5 9     # multiple specific
#   DRYRUN=1 bash scripts/test_all_entity_types.sh  # print payloads, no send

set -uo pipefail

ENDPOINT="https://aileron.example.com/api/v1/webhooks/dynatrace"
API_KEY="ah_c76ddeaa86c74818ac84997c0f2a8174eaba3bf0fdab295132acb8aba2238e5f"
NS="aileron"
NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
TS=$(date +%s)
DRYRUN=${DRYRUN:-0}

# ── real entity constants ─────────────────────────────────────────────────────
# CloudStack BM hypervisors (bare_metal InfraLevel=5)
BM1="cloudstack-cluster-2-iapps-100-67-61-18"
BM2="cloudstack-cluster-2-iapps-100-67-61-31"
BM_MDN="mps-mondev-mdn-worker-z3-05"           # kubeadm bare-metal, MDN DC
GHOST_BM="decommissioned-bm-rack42-iapps-10-100-45-91"  # in DB, decommissioned

# CloudStack VMs (vm InfraLevel=3, back the K8s nodes)
VM1="cloudstack-cluster-2-mps-nonprod-rno-worker-z3-08"
VM2="cloudstack-cluster-2-mps-nonprod-rno-worker-z3-13"
VM_KVM="cloudstack-cluster-2-mps-nonprod-rno-worker-z1-06"  # under BM2

# K8s nodes
NODE1="mps-nonprod-rno-worker-z3-08"     # backed by VM1 on BM1
NODE2="mps-nonprod-rno-worker-z3-13"     # backed by VM2
NODE_MDN="mps-mondev-mdn-worker-z3-05"  # kubeadm node (same name as BM_MDN)

# Cluster UIDs (used for JSONB exact match in k8s storm dedup)
NONPROD_UID="88b12bf2-9f23-4b4a-b06a-3d2b33134a3b"
MONDEV_UID="00a07750-e556-443e-89d9-80341edb472d"
K8PREV_UID="d1be9a2f-4e83-49bf-8800-b9a2bc2e7da0"

# NetApp (MDN datacenter)
NETAPP_CLUSTER="netapp-mdn-cluster001"

# ── colour/output helpers ─────────────────────────────────────────────────────
BOLD="\033[1m"; DIM="\033[2m"; RST="\033[0m"
RED="\033[31m"; GRN="\033[32m"; YLW="\033[33m"; CYN="\033[36m"; MAG="\033[35m"

section() {
  echo ""
  echo -e "${BOLD}${CYN}══════════════════════════════════════════════════════════${RST}"
  echo -e "${BOLD}  $1${RST}"
  echo -e "${BOLD}${CYN}══════════════════════════════════════════════════════════${RST}"
}
info()  { echo -e "  ${DIM}$*${RST}"; }
why()   { echo -e "  ${YLW}WHY: $*${RST}"; }
pause() { echo -e "  ${DIM}⏳ ${1}s…${RST}"; sleep "$1"; }

post() {
  local label="$1" pid="$2" payload="$3"
  if [[ "$DRYRUN" == "1" ]]; then
    echo -e "  ${DIM}[DRYRUN] ${label}${RST}"
    echo "$payload" | python3 -m json.tool 2>/dev/null | head -8
    return
  fi
  printf "  → %-58s " "$label"
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
    echo ""
  else
    printf "${RED}HTTP %s${RST}  %s\n" "$http_code" "$(echo "$body" | head -c 200)"
  fi
}

dbcheck() {
  echo ""
  echo -e "  ${CYN}┌─ DB: auto-created incidents (last 15 min) ─────────────┐${RST}"
  kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT '  ' || LEFT(id::text,8) ||
           '  alerts=' || LPAD(jsonb_array_length(alert_ids)::text,3) ||
           '  ' || RPAD(status,14) ||
           '  corr=' || LEFT(COALESCE(correlation_id,'NULL'),28) ||
           '  » ' || LEFT(title,40)
    FROM incidents
    WHERE auto_created=true AND updated_at > NOW()-INTERVAL '15 minutes'
    ORDER BY created_at DESC LIMIT 14;" 2>/dev/null | grep -v "^$" \
    || echo -e "  ${DIM}(kubectl unavailable — check https://aileron.example.com/incidents)${RST}"
  echo -e "  ${CYN}└──────────────────────────────────────────────────────────┘${RST}"
}

logs() {
  echo ""
  echo -e "  ${CYN}┌─ RCE decisions (last 60s) ──────────────────────────────┐${RST}"
  kubectl logs -n "$NS" -l app=alerthub-backend --since=60s 2>/dev/null \
  | grep -E "RCE alert=|ATTACH_TO_ROOT|CREATE_ROOT|correlation_id.*set|incident.*auto|entityFamily|infraLevel" \
  | head -20 || echo -e "  ${DIM}(kubectl unavailable)${RST}"
  echo -e "  ${CYN}└──────────────────────────────────────────────────────────┘${RST}"
}

incident_count() {
  kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT COUNT(*) FROM incidents
    WHERE auto_created=true AND correlation_id='$1'
      AND status IN ('open','investigating','acknowledged')
      AND updated_at > NOW()-INTERVAL '15 minutes';" 2>/dev/null | tr -d ' \n' || echo "?"
}

alert_incident_count() {
  kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT COUNT(DISTINCT incident_id) FROM alerts
    WHERE source_id IN ($1) AND incident_id IS NOT NULL;" 2>/dev/null | tr -d ' \n' || echo "?"
}

check() {
  local label="$1" got="$2" want="$3"
  if [[ "$got" == "$want" ]]; then
    echo -e "  ${GRN}✓ PASS${RST}  ${label}: ${got}"
  else
    echo -e "  ${RED}✗ FAIL${RST}  ${label}: want=${want} got=${got}"
  fi
}

# ── scenario selector ─────────────────────────────────────────────────────────
RUN_ALL=true
SELECTED=()
if [[ $# -gt 0 ]]; then
  RUN_ALL=false; SELECTED=("$@")
fi
should_run() {
  $RUN_ALL && return 0
  for s in "${SELECTED[@]}"; do [[ "$s" == "$1" ]] && return 0; done
  return 1
}

echo ""
echo -e "${BOLD}AlertHub — All Entity Types Correlation Test Suite${RST}"
echo -e "${DIM}Endpoint : ${ENDPOINT}${RST}"
echo -e "${DIM}Timestamp: ${TS}  Date: $(date)${RST}"
[[ "$DRYRUN" == "1" ]] && echo -e "${YLW}[DRY RUN — payloads printed, nothing sent]${RST}"

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 1 — Full BM→VM→Node→Workloads cascade
#
# WHAT: The textbook CloudStack failure chain. Physical BM hypervisor loses
#   power → VM (K8s backing host) is force-terminated → K8s node goes NotReady
#   → 4 different workloads across 4 namespaces are evicted.
#
# HOW CORRELATION WORKS:
#   All 6 alerts carry rootCauseEntity.entityId = HOST-BM1.
#   ① RCE Stage 1: Dynatrace rootCauseEntity label = BM1 → CREATE_ROOT (1 incident)
#   ② All downstream alerts: RCE matches rootCauseEntity=BM1 → ATTACH_TO_ROOT
#   ③ CACIE: BM1 gets trust×1.00 (InfraLevel=5 bare_metal), others dampened
#   Expected: 1 incident, 6 alerts, correlation_id=BM1
# ══════════════════════════════════════════════════════════════════════════════
if should_run 1; then
section "Scenario 1 [bare_metal→vm→k8s_node→k8s_workload] Full BM cascade (6 alerts)"
info "BM1   : ${BM1} (bare_metal, InfraLevel=5)"
info "VM1   : ${VM1}  (vm, InfraLevel=3)"
info "Node1 : ${NODE1} (k8s_node, cluster=mps-nonprod-rno)"
why "rootCauseEntity=BM1 on all alerts → RCE Stage 1 creates 1 incident, all attach"
S1="S1-${TS}"

# 1/6 — Bare metal power fault (ROOT)
# entity_type=bare_metal in customProperties → InfraLevel=5, highest trust in CACIE
post "1/6 [bare_metal] BM1 power fault (ROOT)" "P-S1-BM-${S1}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S1-BM-${S1}\",
  \"problemTitle\": \"Host or monitoring unavailable — ${BM1}\",
  \"ProblemDetailsText\": \"KVM hypervisor ${BM1} is unreachable. IPMI shows power fault. All hosted VMs at risk. host.name: ${BM1}\",
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
    \"entityId\": \"HOST-${BM1}\",
    \"entityName\": \"${BM1}\",
    \"entityType\": \"HOST\"
  }],
  \"customProperties\": {
    \"host.name\": \"${BM1}\",
    \"entity_type\": \"bare_metal\",
    \"impacted_entity\": \"${BM1}\",
    \"environment\": \"ADC\",
    \"datacenter\": \"rno\"
  },
  \"EntityTags\": [\"bare-metal\", \"cloudstack-cluster-2\", \"rno\"]
}"
pause 3

# 2/6 — VM on that BM becomes unreachable (downstream)
# entity_type=vm → InfraLevel=3; rootCauseEntity still points to BM1
post "2/6 [vm] VM1 unreachable (downstream)" "P-S1-VM-${S1}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S1-VM-${S1}\",
  \"problemTitle\": \"Host or monitoring unavailable — ${VM1}\",
  \"ProblemDetailsText\": \"VM ${VM1} is unresponsive. Parent BM ${BM1} is down. Forceful VM termination. host.name: ${VM1}\",
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
    \"entityId\": \"HOST-${VM1}\",
    \"entityName\": \"${VM1}\",
    \"entityType\": \"HOST\"
  }],
  \"customProperties\": {
    \"host.name\": \"${VM1}\",
    \"entity_type\": \"vm\",
    \"impacted_entity\": \"${VM1}\",
    \"environment\": \"ADC\"
  }
}"
pause 3

# 3/6 — K8s node NotReady (downstream)
# KUBERNETES_NODE entityType → k8s_node entity, InfraLevel=2
# k8s.cluster.uid is required for k8s storm grouping
post "3/6 [k8s_node] Node1 NotReady (downstream)" "P-S1-NODE-${S1}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S1-NODE-${S1}\",
  \"problemTitle\": \"Kubernetes node ${NODE1} is NotReady\",
  \"ProblemDetailsText\": \"Node ${NODE1} entered NotReady state. kubelet unresponsive — VM ${VM1} terminated. k8s.node.name: ${NODE1}. k8s.cluster.name: mps-nonprod-rno\",
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
    \"entityId\": \"KUBERNETES_NODE-${NODE1}\",
    \"entityName\": \"${NODE1}\",
    \"entityType\": \"KUBERNETES_NODE\"
  }],
  \"customProperties\": {
    \"host.name\": \"${VM1}\",
    \"k8s.node.name\": \"${NODE1}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"environment\": \"ADC\"
  }
}"
pause 3

# 4/6 — dex OIDC pod evicted (k8s_workload, InfraLevel=1)
post "4/6 [k8s_workload] dex/dex evicted (downstream)" "P-S1-DEX-${S1}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S1-DEX-${S1}\",
  \"problemTitle\": \"Not all pods ready — dex in namespace dex\",
  \"ProblemDetailsText\": \"Pod dex-565647688-wd4qz evicted from ${NODE1}. OIDC/SSO authentication impaired. k8s.namespace.name: dex. k8s.workload.name: dex. k8s.cluster.name: mps-nonprod-rno\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${BM1}\",
    \"entityName\": \"${BM1}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-dex\",
    \"entityName\": \"dex\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.node.name\": \"${NODE1}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"dex\",
    \"k8s.workload.name\": \"dex\",
    \"k8s.workload.kind\": \"deployment\",
    \"pod.name\": \"dex-565647688-wd4qz\",
    \"environment\": \"ADC\"
  }
}"
pause 2

# 5/6 — ingress-nginx DaemonSet pod lost
post "5/6 [k8s_workload] ingress-nginx DaemonSet pod lost" "P-S1-INGRESS-${S1}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S1-INGRESS-${S1}\",
  \"problemTitle\": \"Not all pods ready — ingress-nginx-controller in ingress-nginx\",
  \"ProblemDetailsText\": \"DaemonSet pod ingress-nginx-controller-5ffg4 lost on ${NODE1}. Node removed from LB pool. k8s.namespace.name: ingress-nginx. k8s.workload.kind: daemonset\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${BM1}\",
    \"entityName\": \"${BM1}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-ingress-nginx-controller\",
    \"entityName\": \"ingress-nginx-controller\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.node.name\": \"${NODE1}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"ingress-nginx\",
    \"k8s.workload.name\": \"ingress-nginx-controller\",
    \"k8s.workload.kind\": \"daemonset\",
    \"pod.name\": \"ingress-nginx-controller-5ffg4\",
    \"environment\": \"ADC\"
  }
}"
pause 2

# 6/6 — ArgoCD repo-server evicted
post "6/6 [k8s_workload] argocd-repo-server evicted" "P-S1-ARGOCD-${S1}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S1-ARGOCD-${S1}\",
  \"problemTitle\": \"Not all pods ready — argocd-repo-server in argocd\",
  \"ProblemDetailsText\": \"Pod argocd-repo-server-5d956d6cbf-v4xnh evicted from ${NODE1}. GitOps deployments paused. k8s.namespace.name: argocd. k8s.cluster.name: mps-nonprod-rno\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${BM1}\",
    \"entityName\": \"${BM1}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-argocd-repo-server\",
    \"entityName\": \"argocd-repo-server\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.node.name\": \"${NODE1}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"argocd\",
    \"k8s.workload.name\": \"argocd-repo-server\",
    \"k8s.workload.kind\": \"deployment\",
    \"pod.name\": \"argocd-repo-server-5d956d6cbf-v4xnh\",
    \"environment\": \"ADC\"
  }
}"

pause 8; logs; dbcheck
echo ""
n=$(incident_count "${BM1}")
check "S1: exactly 1 incident with correlation_id=${BM1}" "$n" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 2 — KVM hypervisor failure → two VMs (InfraLevel=4)
#
# WHAT: BM2 is the KVM hypervisor. Dynatrace fires on the KVM layer first,
#   then two VMs hosted on it become unreachable.
#
# HOW CORRELATION WORKS:
#   entity_type=kvm in customProperties → InfraLevel=4 (below BM=5, above VM=3)
#   ① RCE Stage 1: rootCauseEntity=BM2 → CREATE_ROOT
#   ② Both VM alerts: rootCauseEntity=BM2 → ATTACH_TO_ROOT
#   ③ CACIE: BM2 gets trust×0.92 (InfraLevelKVM dampening)
#   NOTE: If you also had a bare_metal alert for BM2 it would get trust×1.00
#   Expected: 1 incident, 3 alerts, correlation_id=BM2
# ══════════════════════════════════════════════════════════════════════════════
if should_run 2; then
section "Scenario 2 [kvm→vm→vm] KVM hypervisor failure → two VMs"
info "BM2   : ${BM2} (entity_type=kvm, InfraLevel=4)"
info "VM_KVM: ${VM_KVM} (entity_type=vm, InfraLevel=3)"
why "KVM level=4 gets trust×0.92 in CACIE; still wins over VM-level alerts"
S2="S2-${TS}"

# 1/3 — KVM hypervisor CPU saturation (ROOT, entity_type=kvm)
post "1/3 [kvm] BM2 KVM hypervisor overloaded (ROOT)" "P-S2-KVM-${S2}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S2-KVM-${S2}\",
  \"problemTitle\": \"High CPU load on KVM hypervisor — ${BM2}\",
  \"ProblemDetailsText\": \"KVM hypervisor ${BM2} at 99% CPU. vCPU steal time causing VM stalls. All hosted VMs impacted. host.name: ${BM2}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"PERFORMANCE\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${BM2}\",
    \"entityName\": \"${BM2}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"HOST-${BM2}\",
    \"entityName\": \"${BM2}\",
    \"entityType\": \"HOST\"
  }],
  \"customProperties\": {
    \"host.name\": \"${BM2}\",
    \"entity_type\": \"kvm\",
    \"impacted_entity\": \"${BM2}\",
    \"environment\": \"ADC\",
    \"datacenter\": \"rno\"
  },
  \"EntityTags\": [\"kvm\", \"cloudstack-cluster-2\", \"rno\"]
}"
pause 3

# 2/3 — First VM CPU throttled (downstream)
post "2/3 [vm] VM_KVM CPU throttled (downstream)" "P-S2-VM1-${S2}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S2-VM1-${S2}\",
  \"problemTitle\": \"CPU throttling on VM — ${VM_KVM}\",
  \"ProblemDetailsText\": \"VM ${VM_KVM} experiencing severe CPU throttle. vCPU steal from hypervisor ${BM2}. host.name: ${VM_KVM}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"PERFORMANCE\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${BM2}\",
    \"entityName\": \"${BM2}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"HOST-${VM_KVM}\",
    \"entityName\": \"${VM_KVM}\",
    \"entityType\": \"HOST\"
  }],
  \"customProperties\": {
    \"host.name\": \"${VM_KVM}\",
    \"entity_type\": \"vm\",
    \"impacted_entity\": \"${VM_KVM}\",
    \"environment\": \"ADC\"
  }
}"
pause 3

# 3/3 — Second VM on same BM unreachable (downstream)
post "3/3 [vm] VM2 unreachable from BM2 (downstream)" "P-S2-VM2-${S2}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S2-VM2-${S2}\",
  \"problemTitle\": \"Host or monitoring unavailable — ${VM2}\",
  \"ProblemDetailsText\": \"VM ${VM2} unresponsive. KVM host ${BM2} CPU saturation causing QEMU lockup. host.name: ${VM2}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${BM2}\",
    \"entityName\": \"${BM2}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"HOST-${VM2}\",
    \"entityName\": \"${VM2}\",
    \"entityType\": \"HOST\"
  }],
  \"customProperties\": {
    \"host.name\": \"${VM2}\",
    \"entity_type\": \"vm\",
    \"impacted_entity\": \"${VM2}\",
    \"environment\": \"ADC\"
  }
}"

pause 7; logs; dbcheck
echo ""
n=$(incident_count "${BM2}")
check "S2: 1 incident correlated to KVM root ${BM2}" "$n" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 3 — VM-only root (RCE Stage 3: VM IS root, no BM parent alert)
#
# WHAT: A VM alerts fire but there is NO accompanying BM alert. The VM alert
#   does NOT carry a rootCauseEntity pointing to a higher-level entity.
#   The VM itself is the root of failure.
#
# HOW CORRELATION WORKS:
#   ① RCE Stage 1: rootCauseEntity=VM2 (same as impactedEntity) → CREATE_ROOT
#   ② The VM IS the root because its entity_type=vm (InfraLevel=3) and
#      rootCauseEntity == impactedEntity (self-reference)
#   ③ RCE Stage 3 would trigger here: VM-level or higher with no higher ancestor
#   Downstream workload alert attaches via same rootCauseEntity=VM2
#   Expected: 1 incident, 2 alerts, correlation_id=VM2
# ══════════════════════════════════════════════════════════════════════════════
if should_run 3; then
section "Scenario 3 [vm-as-root] VM root with no BM parent alert"
info "VM2  : ${VM2} (entity_type=vm — self-root, InfraLevel=3)"
info "Node2: ${NODE2} (downstream)"
why "No BM alert present → VM becomes root via Stage 3 (VM-level or higher)"
S3="S3-${TS}"

post "1/2 [vm-root] VM2 network fault — self-root" "P-S3-VM-${S3}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S3-VM-${S3}\",
  \"problemTitle\": \"Network problem on VM — ${VM2}\",
  \"ProblemDetailsText\": \"VM ${VM2} lost network connectivity. NIC driver crash detected. No BM-level alerts observed. host.name: ${VM2}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${VM2}\",
    \"entityName\": \"${VM2}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"HOST-${VM2}\",
    \"entityName\": \"${VM2}\",
    \"entityType\": \"HOST\"
  }],
  \"customProperties\": {
    \"host.name\": \"${VM2}\",
    \"entity_type\": \"vm\",
    \"impacted_entity\": \"${VM2}\",
    \"environment\": \"ADC\"
  }
}"
pause 3

post "2/2 [k8s_node] Node2 NotReady from VM2 failure" "P-S3-NODE-${S3}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S3-NODE-${S3}\",
  \"problemTitle\": \"Kubernetes node ${NODE2} is NotReady\",
  \"ProblemDetailsText\": \"Node ${NODE2} NotReady. Backing VM ${VM2} lost network. k8s.node.name: ${NODE2}. k8s.cluster.name: mps-nonprod-rno\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${VM2}\",
    \"entityName\": \"${VM2}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_NODE-${NODE2}\",
    \"entityName\": \"${NODE2}\",
    \"entityType\": \"KUBERNETES_NODE\"
  }],
  \"customProperties\": {
    \"host.name\": \"${VM2}\",
    \"k8s.node.name\": \"${NODE2}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"environment\": \"ADC\"
  }
}"

pause 7; dbcheck
echo ""
n=$(incident_count "${VM2}")
check "S3: 1 incident with VM2 as root" "$n" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 4 — K8s node root only (RCE Stage 3: node IS root)
#
# WHAT: A K8s node alerts fire with rootCauseEntity pointing to itself.
#   No VM or BM alert precedes it. The node IS the root cause (e.g. kubelet
#   crash, kernel OOP, disk failure on node itself).
#
# HOW CORRELATION WORKS:
#   ① rootCauseEntity.entityName = NODE1 (same as impacted node)
#   ② RCE Stage 3: KUBERNETES_NODE is at infra level, no higher parent
#      → node becomes root → CREATE_ROOT
#   ③ Workload alert with same rootCauseEntity=NODE1 → ATTACH_TO_ROOT
#   Expected: 1 incident, 3 alerts, correlation_id=NODE1
# ══════════════════════════════════════════════════════════════════════════════
if should_run 4; then
section "Scenario 4 [k8s_node-as-root] Node root with no VM/BM parent"
info "Node1  : ${NODE1} (self-root, InfraLevel=2)"
info "Cluster: mps-nonprod-rno UID=${NONPROD_UID:0:8}…"
why "No VM/BM alert exists → k8s_node becomes root via RCE Stage 3"
S4="S4-${TS}"

post "1/3 [k8s_node-root] Node1 kubelet crash (self-root)" "P-S4-NODE-${S4}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S4-NODE-${S4}\",
  \"problemTitle\": \"Kubernetes node ${NODE1} kubelet crash\",
  \"ProblemDetailsText\": \"kubelet process on ${NODE1} terminated unexpectedly. OOM killer triggered. No underlying VM/BM alert detected. k8s.node.name: ${NODE1}. k8s.cluster.name: mps-nonprod-rno. host.name: ${NODE1}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"KUBERNETES_NODE-${NODE1}\",
    \"entityName\": \"${NODE1}\",
    \"entityType\": \"KUBERNETES_NODE\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_NODE-${NODE1}\",
    \"entityName\": \"${NODE1}\",
    \"entityType\": \"KUBERNETES_NODE\"
  }],
  \"customProperties\": {
    \"host.name\": \"${NODE1}\",
    \"k8s.node.name\": \"${NODE1}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"impacted_entity\": \"${NODE1}\",
    \"environment\": \"ADC\"
  }
}"
pause 3

post "2/3 [k8s_workload] coredns evicted from Node1" "P-S4-CORE-${S4}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S4-CORE-${S4}\",
  \"problemTitle\": \"Not all pods ready — coredns in kube-system\",
  \"ProblemDetailsText\": \"coredns pod evicted from ${NODE1} due to kubelet failure. DNS resolution impaired across cluster. k8s.namespace.name: kube-system. k8s.workload.name: coredns\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"KUBERNETES_NODE-${NODE1}\",
    \"entityName\": \"${NODE1}\",
    \"entityType\": \"KUBERNETES_NODE\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-coredns\",
    \"entityName\": \"coredns\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.node.name\": \"${NODE1}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"kube-system\",
    \"k8s.workload.name\": \"coredns\",
    \"k8s.workload.kind\": \"deployment\",
    \"environment\": \"ADC\"
  }
}"
pause 3

post "3/3 [k8s_workload] jaeger-collector evicted from Node1" "P-S4-JAEGER-${S4}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S4-JAEGER-${S4}\",
  \"problemTitle\": \"Not all pods ready — jaeger-collector in opentracing\",
  \"ProblemDetailsText\": \"jaeger-collector-5f8667b898-nstrx evicted from ${NODE1}. Distributed tracing collection impaired for nonprod-rno. k8s.namespace.name: opentracing\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"KUBERNETES_NODE-${NODE1}\",
    \"entityName\": \"${NODE1}\",
    \"entityType\": \"KUBERNETES_NODE\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-jaeger-collector\",
    \"entityName\": \"jaeger-collector\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.node.name\": \"${NODE1}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"opentracing\",
    \"k8s.workload.name\": \"jaeger-collector\",
    \"k8s.workload.kind\": \"deployment\",
    \"pod.name\": \"jaeger-collector-5f8667b898-nstrx\",
    \"environment\": \"ADC\"
  }
}"

pause 7; dbcheck
echo ""
n=$(incident_count "${NODE1}")
check "S4: 1 incident with node ${NODE1} as root" "$n" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 5 — NetApp cluster → aggregate fan-out
#
# WHAT: NetApp storage cluster failure propagates to individual aggregates.
#   This tests the netapp entity family — these alerts should NEVER mix
#   with k8s or vm family alerts even if timestamps overlap.
#
# HOW CORRELATION WORKS:
#   ① entity_type=netapp_cluster → entityTypeFamily=netapp
#   ② entity_type=netapp_aggregate → entityTypeFamily=netapp (same family)
#   ③ All 3 aggregate alerts carry rootCauseEntity=netapp-mdn-cluster001
#   ④ RCE Stage 1: rootCauseEntity → CREATE_ROOT, aggregates ATTACH_TO_ROOT
#   ⑤ entityTypeFamily guard PREVENTS these from merging with any k8s/vm alerts
#   Expected: 1 incident, 4 alerts, correlation_id=netapp-mdn-cluster001
# ══════════════════════════════════════════════════════════════════════════════
if should_run 5; then
section "Scenario 5 [netapp_cluster→netapp_aggregate] NetApp storage cluster fan-out"
info "NetApp cluster: ${NETAPP_CLUSTER} (MDN datacenter)"
info "Aggregates:     aggr_rca_ssd_01, aggr_rca_hdd_02, aggr_mps_ssd_03"
why "netapp family never merges with k8s/vm; rootCauseEntity fans 3 aggregates into 1 incident"
S5="S5-${TS}"

# 1/4 — NetApp cluster health degraded (ROOT)
post "1/4 [netapp_cluster] ${NETAPP_CLUSTER} cluster health degraded (ROOT)" "P-S5-CL-${S5}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S5-CL-${S5}\",
  \"problemTitle\": \"NetApp cluster ${NETAPP_CLUSTER} health degraded\",
  \"ProblemDetailsText\": \"Storage cluster ${NETAPP_CLUSTER} reporting DEGRADED health. SFO negotiation failed between controller A and B. entity_type: netapp_cluster. host.name: ${NETAPP_CLUSTER}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"NETAPP_CLUSTER-${NETAPP_CLUSTER}\",
    \"entityName\": \"${NETAPP_CLUSTER}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"NETAPP_CLUSTER-${NETAPP_CLUSTER}\",
    \"entityName\": \"${NETAPP_CLUSTER}\",
    \"entityType\": \"HOST\"
  }],
  \"customProperties\": {
    \"host.name\": \"${NETAPP_CLUSTER}\",
    \"entity_type\": \"netapp_cluster\",
    \"impacted_entity\": \"${NETAPP_CLUSTER}\",
    \"environment\": \"MDN\",
    \"datacenter\": \"mdn\"
  },
  \"EntityTags\": [\"netapp\", \"storage\", \"mdn\"]
}"
pause 3

# 2/4 — First aggregate I/O errors (downstream)
post "2/4 [netapp_aggregate] aggr_rca_ssd_01 I/O errors" "P-S5-AGG1-${S5}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S5-AGG1-${S5}\",
  \"problemTitle\": \"NetApp aggregate aggr_rca_ssd_01 I/O errors\",
  \"ProblemDetailsText\": \"Aggregate aggr_rca_ssd_01 on ${NETAPP_CLUSTER} reporting disk I/O errors. SSD shelf connectivity issue traced to controller failover. entity_type: netapp_aggregate. host.name: ${NETAPP_CLUSTER}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"ERROR\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"NETAPP_CLUSTER-${NETAPP_CLUSTER}\",
    \"entityName\": \"${NETAPP_CLUSTER}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"NETAPP_AGGREGATE-aggr_rca_ssd_01\",
    \"entityName\": \"aggr_rca_ssd_01\",
    \"entityType\": \"HOST\"
  }],
  \"customProperties\": {
    \"host.name\": \"${NETAPP_CLUSTER}\",
    \"entity_type\": \"netapp_aggregate\",
    \"aggregate.name\": \"aggr_rca_ssd_01\",
    \"impacted_entity\": \"aggr_rca_ssd_01\",
    \"environment\": \"MDN\"
  }
}"
pause 2

# 3/4 — Second aggregate offline
post "3/4 [netapp_aggregate] aggr_rca_hdd_02 offline" "P-S5-AGG2-${S5}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S5-AGG2-${S5}\",
  \"problemTitle\": \"NetApp aggregate aggr_rca_hdd_02 offline\",
  \"ProblemDetailsText\": \"Aggregate aggr_rca_hdd_02 on ${NETAPP_CLUSTER} offline. HDD shelf lost path to controller. Volumes on this aggregate are inaccessible. entity_type: netapp_aggregate\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"NETAPP_CLUSTER-${NETAPP_CLUSTER}\",
    \"entityName\": \"${NETAPP_CLUSTER}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"NETAPP_AGGREGATE-aggr_rca_hdd_02\",
    \"entityName\": \"aggr_rca_hdd_02\",
    \"entityType\": \"HOST\"
  }],
  \"customProperties\": {
    \"host.name\": \"${NETAPP_CLUSTER}\",
    \"entity_type\": \"netapp_aggregate\",
    \"aggregate.name\": \"aggr_rca_hdd_02\",
    \"impacted_entity\": \"aggr_rca_hdd_02\",
    \"environment\": \"MDN\"
  }
}"
pause 2

# 4/4 — Third aggregate volume errors
post "4/4 [netapp_aggregate] aggr_mps_ssd_03 volume errors" "P-S5-AGG3-${S5}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S5-AGG3-${S5}\",
  \"problemTitle\": \"NetApp aggregate aggr_mps_ssd_03 volume errors\",
  \"ProblemDetailsText\": \"Aggregate aggr_mps_ssd_03 on ${NETAPP_CLUSTER} reporting volume mount errors. NFS export list stale. VMs with PVCs on this aggregate see I/O timeouts. entity_type: netapp_aggregate\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"ERROR\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"NETAPP_CLUSTER-${NETAPP_CLUSTER}\",
    \"entityName\": \"${NETAPP_CLUSTER}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"NETAPP_AGGREGATE-aggr_mps_ssd_03\",
    \"entityName\": \"aggr_mps_ssd_03\",
    \"entityType\": \"HOST\"
  }],
  \"customProperties\": {
    \"host.name\": \"${NETAPP_CLUSTER}\",
    \"entity_type\": \"netapp_aggregate\",
    \"aggregate.name\": \"aggr_mps_ssd_03\",
    \"impacted_entity\": \"aggr_mps_ssd_03\",
    \"environment\": \"MDN\"
  }
}"

pause 8; logs; dbcheck
echo ""
n=$(incident_count "${NETAPP_CLUSTER}")
check "S5: 1 incident for NetApp cluster root" "$n" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 6 — NetApp + K8s VM simultaneously → MUST produce 2 incidents
#
# WHAT: NetApp storage I/O error and a K8s workload failure fire at the SAME
#   time. The k8s workload is affected by storage (indirectly related), but
#   they must NOT be merged into 1 incident.
#
# HOW CORRELATION WORKS (WHY THEY STAY SEPARATE):
#   ① NetApp alert: entityTypeFamily=netapp
#   ② K8s workload alert: entityTypeFamily=k8s
#   ③ entityTypeFamily() isolation guard: different families → NEVER merge
#   ④ Each creates its own separate incident
#   ⑤ This is a critical regression test — any merge here = bug
#   Expected: 2 separate incidents, one per family
# ══════════════════════════════════════════════════════════════════════════════
if should_run 6; then
section "Scenario 6 [ISOLATION] NetApp + K8s simultaneously → MUST be 2 incidents"
info "netapp_aggregate on ${NETAPP_CLUSTER} fires at same time as k8s_workload alert"
why "CRITICAL: entityTypeFamily() guard must prevent cross-family merge"
echo -e "  ${RED}If this produces 1 incident = REGRESSION in family isolation guard${RST}"
S6="S6-${TS}"

# NetApp aggregate alert (netapp family)
post "1/2 [netapp_aggregate] Storage I/O timeout (netapp family)" "P-S6-NETAPP-${S6}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S6-NETAPP-${S6}\",
  \"problemTitle\": \"NetApp aggregate aggr_rca_ssd_01 latency spike\",
  \"ProblemDetailsText\": \"Aggregate aggr_rca_ssd_01 on ${NETAPP_CLUSTER} showing >50ms read latency. NFS clients timing out. entity_type: netapp_aggregate. host.name: ${NETAPP_CLUSTER}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"PERFORMANCE\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"NETAPP_AGG-aggr_rca_ssd_01\",
    \"entityName\": \"aggr_rca_ssd_01\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"NETAPP_AGG-aggr_rca_ssd_01\",
    \"entityName\": \"aggr_rca_ssd_01\",
    \"entityType\": \"HOST\"
  }],
  \"customProperties\": {
    \"host.name\": \"${NETAPP_CLUSTER}\",
    \"entity_type\": \"netapp_aggregate\",
    \"aggregate.name\": \"aggr_rca_ssd_01\",
    \"impacted_entity\": \"aggr_rca_ssd_01\",
    \"environment\": \"MDN\"
  }
}"
pause 2

# K8s workload alert (k8s family) — fires at same time
post "2/2 [k8s_workload] rca-service I/O wait (k8s family)" "P-S6-K8S-${S6}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S6-K8S-${S6}\",
  \"problemTitle\": \"Not all pods ready — rca-service in aileron-agent\",
  \"ProblemDetailsText\": \"rca-service pods in CrashLoopBackOff. Storage I/O wait causing JVM GC pauses. k8s.namespace.name: aileron-agent. k8s.workload.name: rca-service. k8s.cluster.name: mps-nonprod-rno\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"KUBERNETES_WORKLOAD-rca-service\",
    \"entityName\": \"rca-service\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-rca-service\",
    \"entityName\": \"rca-service\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"aileron-agent\",
    \"k8s.workload.name\": \"rca-service\",
    \"k8s.workload.kind\": \"deployment\",
    \"environment\": \"ADC\"
  }
}"

pause 8; dbcheck
echo ""
n_netapp=$(incident_count "aggr_rca_ssd_01")
n_k8s=$(incident_count "rca-service")
check "S6 ISOLATION: netapp incident exists" "$n_netapp" "1"
check "S6 ISOLATION: k8s incident exists" "$n_k8s" "1"
echo -e "  ${MAG}Verify above: should be 2 rows in DB, NOT 1${RST}"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 7 — CLOUD_APPLICATION service cascade (APM, no infra)
#
# WHAT: Dynatrace CLOUD_APPLICATION entities (APM-instrumented services)
#   alert at the application layer — no infrastructure involved.
#   A payment-gateway latency spike cascades to geo-service timeout
#   which causes rca-service upstream errors.
#
# HOW CORRELATION WORKS:
#   ① entityType=CLOUD_APPLICATION → entity_type=cloud_application
#   ② rootCauseEntity=payment-gateway on all 3 alerts → all ATTACH_TO_ROOT
#   ③ Correlation is semantic (shared root) + temporal (< 2-hour window)
#   ④ No infra-level dampening (cloud_application has default 0.70)
#   Expected: 1 incident, 3 alerts, application-level service chain
# ══════════════════════════════════════════════════════════════════════════════
if should_run 7; then
section "Scenario 7 [cloud_application] APM service cascade — no infra"
info "payment-gateway → geo-service → rca-service (service dependency chain)"
why "CLOUD_APPLICATION entities correlate by shared rootCauseEntity at APM level"
S7="S7-${TS}"

# 1/3 — payment-gateway response time spike (ROOT)
post "1/3 [cloud_application] payment-gateway latency spike (ROOT)" "P-S7-PG-${S7}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S7-PG-${S7}\",
  \"problemTitle\": \"Response time degradation — payment-gateway\",
  \"ProblemDetailsText\": \"CLOUD_APPLICATION payment-gateway p99 latency at 8.2s (threshold 2s). Database connection pool exhausted. Downstream services timing out.\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"PERFORMANCE\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"CLOUD_APPLICATION-payment-gateway\",
    \"entityName\": \"payment-gateway\",
    \"entityType\": \"CLOUD_APPLICATION\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"CLOUD_APPLICATION-payment-gateway\",
    \"entityName\": \"payment-gateway\",
    \"entityType\": \"CLOUD_APPLICATION\"
  }],
  \"customProperties\": {
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"payments\",
    \"k8s.workload.name\": \"payment-gateway\",
    \"k8s.workload.kind\": \"deployment\",
    \"impacted_entity\": \"payment-gateway\",
    \"environment\": \"ADC\"
  }
}"
pause 3

# 2/3 — geo-service timeout (downstream, different namespace)
post "2/3 [cloud_application] geo-service timeout (downstream)" "P-S7-GEO-${S7}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S7-GEO-${S7}\",
  \"problemTitle\": \"Failure rate increase — geo-service in geo-service-perf\",
  \"ProblemDetailsText\": \"CLOUD_APPLICATION geo-service error rate 34%. Upstream payment-gateway calls timing out causing cascading 504s. k8s.namespace.name: geo-service-perf\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"ERROR\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"CLOUD_APPLICATION-payment-gateway\",
    \"entityName\": \"payment-gateway\",
    \"entityType\": \"CLOUD_APPLICATION\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"CLOUD_APPLICATION-geo-service\",
    \"entityName\": \"geo-service\",
    \"entityType\": \"CLOUD_APPLICATION\"
  }],
  \"customProperties\": {
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"geo-service-perf\",
    \"k8s.workload.name\": \"geo-service\",
    \"k8s.workload.kind\": \"deployment\",
    \"environment\": \"ADC\"
  }
}"
pause 3

# 3/3 — rca-service upstream errors (downstream)
post "3/3 [cloud_application] rca-service upstream errors (downstream)" "P-S7-RCA-${S7}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S7-RCA-${S7}\",
  \"problemTitle\": \"Failure rate increase — rca-service in aileron-agent\",
  \"ProblemDetailsText\": \"CLOUD_APPLICATION rca-service upstream call failure rate 67%. Root traced to payment-gateway pool exhaustion propagating through geo-service. k8s.namespace.name: aileron-agent\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"ERROR\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"CLOUD_APPLICATION-payment-gateway\",
    \"entityName\": \"payment-gateway\",
    \"entityType\": \"CLOUD_APPLICATION\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"CLOUD_APPLICATION-rca-service\",
    \"entityName\": \"rca-service\",
    \"entityType\": \"CLOUD_APPLICATION\"
  }],
  \"customProperties\": {
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"aileron-agent\",
    \"k8s.workload.name\": \"rca-service\",
    \"k8s.workload.kind\": \"deployment\",
    \"environment\": \"ADC\"
  }
}"

pause 8; logs; dbcheck
echo ""
n=$(incident_count "payment-gateway")
check "S7: 1 APM incident for payment-gateway cascade" "$n" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 8 — PROCESS_GROUP_INSTANCE crash → workload error
#
# WHAT: A specific OS process (JVM / java process) crashes on a K8s node.
#   Dynatrace monitors it as PROCESS_GROUP_INSTANCE. The workload depending
#   on that process then alerts as a K8s workload failure.
#
# HOW CORRELATION WORKS:
#   ① entityType=PROCESS_GROUP_INSTANCE → entity_type=process
#   ② Process crash fires on NODE2 → rootCauseEntity=the process
#   ③ K8s workload crash on same node links via shared host.name/k8s.node.name
#   ④ Temporal correlation: both within 2-hour window, same cluster
#   Expected: alerts processed and correlated, 1 incident with process root
# ══════════════════════════════════════════════════════════════════════════════
if should_run 8; then
section "Scenario 8 [process] PROCESS_GROUP_INSTANCE crash → workload failure"
info "Process: java-payment-service on ${NODE2}"
info "Workload: payment-gateway in payments namespace"
why "PROCESS_GROUP_INSTANCE entity_type; temporal+semantic correlation to workload"
S8="S8-${TS}"

# 1/2 — Java process crash (process entity type, ROOT)
post "1/2 [process] java-payment-service JVM crash (ROOT)" "P-S8-PROC-${S8}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S8-PROC-${S8}\",
  \"problemTitle\": \"Process crash — java-payment-service on ${NODE2}\",
  \"ProblemDetailsText\": \"PROCESS_GROUP_INSTANCE java-payment-service (PID 48291) on ${NODE2} crashed with OutOfMemoryError. JVM heap exhausted. Process group: payment-service-jvm. host.name: ${NODE2}\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"PROCESS_GROUP_INSTANCE-java-payment-service\",
    \"entityName\": \"java-payment-service\",
    \"entityType\": \"PROCESS_GROUP_INSTANCE\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"PROCESS_GROUP_INSTANCE-java-payment-service\",
    \"entityName\": \"java-payment-service\",
    \"entityType\": \"PROCESS_GROUP_INSTANCE\"
  }],
  \"customProperties\": {
    \"host.name\": \"${NODE2}\",
    \"k8s.node.name\": \"${NODE2}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"entity_type\": \"process\",
    \"process.name\": \"java-payment-service\",
    \"impacted_entity\": \"java-payment-service\",
    \"environment\": \"ADC\"
  }
}"
pause 3

# 2/2 — K8s workload failure on same node (downstream)
post "2/2 [k8s_workload] payment-gateway pod OOMKilled (downstream)" "P-S8-WL-${S8}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S8-WL-${S8}\",
  \"problemTitle\": \"Not all pods ready — payment-gateway in payments\",
  \"ProblemDetailsText\": \"payment-gateway pod OOMKilled on ${NODE2}. JVM process java-payment-service exceeded memory limits. k8s.namespace.name: payments. k8s.workload.name: payment-gateway. host.name: ${NODE2}\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"PROCESS_GROUP_INSTANCE-java-payment-service\",
    \"entityName\": \"java-payment-service\",
    \"entityType\": \"PROCESS_GROUP_INSTANCE\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-payment-gateway\",
    \"entityName\": \"payment-gateway\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"host.name\": \"${NODE2}\",
    \"k8s.node.name\": \"${NODE2}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"payments\",
    \"k8s.workload.name\": \"payment-gateway\",
    \"k8s.workload.kind\": \"deployment\",
    \"environment\": \"ADC\"
  }
}"

pause 7; dbcheck
echo ""
n=$(incident_count "java-payment-service")
check "S8: 1 incident for process crash root" "$n" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 9 — Cross-cluster isolation: same workload name in 2 clusters
#
# WHAT: The workload "coredns" exists in BOTH mps-nonprod-rno AND k8preview01-rno
#   clusters. Both fire simultaneously. They MUST produce 2 separate incidents.
#
# HOW CORRELATION WORKS (WHY THEY STAY SEPARATE):
#   ① Alert A: k8s.cluster.uid=NONPROD_UID
#   ② Alert B: k8s.cluster.uid=K8PREV_UID
#   ③ k8s storm grouping uses EXACT JSONB match on k8s.cluster.uid
#      (not ILIKE on cluster name — this was explicitly fixed)
#   ④ Different cluster UIDs → different correlation buckets → 2 incidents
#   Expected: 2 incidents, one per cluster UID
# ══════════════════════════════════════════════════════════════════════════════
if should_run 9; then
section "Scenario 9 [ISOLATION] Cross-cluster: same workload name, 2 cluster UIDs → 2 incidents"
info "coredns in mps-nonprod-rno (UID=${NONPROD_UID:0:8}…)"
info "coredns in k8preview01-rno (UID=${K8PREV_UID:0:8}…)"
why "CRITICAL: k8s.cluster.uid exact JSONB match prevents cross-cluster merge"
echo -e "  ${RED}If this produces 1 incident = regression in cluster UID isolation${RST}"
S9="S9-${TS}"

# nonprod-rno coredns — adds downstream workload from same cluster to make it meaningful
post "1/4 [k8s_node] Node z2-17 NotReady (nonprod-rno)" "P-S9-NPNODE-${S9}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S9-NPNODE-${S9}\",
  \"problemTitle\": \"Kubernetes node mps-nonprod-rno-worker-z2-17 is NotReady\",
  \"ProblemDetailsText\": \"Node mps-nonprod-rno-worker-z2-17 NotReady. k8s.node.name: mps-nonprod-rno-worker-z2-17. k8s.cluster.name: mps-nonprod-rno. host.name: mps-nonprod-rno-worker-z2-17\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-mps-nonprod-rno-worker-z2-17\",
    \"entityName\": \"mps-nonprod-rno-worker-z2-17\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{\"entityId\": \"KUBERNETES_NODE-mps-nonprod-rno-worker-z2-17\",\"entityName\": \"mps-nonprod-rno-worker-z2-17\",\"entityType\": \"KUBERNETES_NODE\"}],
  \"customProperties\": {
    \"host.name\": \"mps-nonprod-rno-worker-z2-17\",
    \"k8s.node.name\": \"mps-nonprod-rno-worker-z2-17\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"impacted_entity\": \"mps-nonprod-rno-worker-z2-17\",
    \"environment\": \"ADC\"
  }
}"
pause 2

post "2/4 [k8s_workload] coredns evicted (nonprod-rno)" "P-S9-CORE-NP-${S9}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S9-CORE-NP-${S9}\",
  \"problemTitle\": \"Not all pods ready — coredns in kube-system (nonprod-rno)\",
  \"ProblemDetailsText\": \"coredns pod evicted from mps-nonprod-rno-worker-z2-17. k8s.namespace.name: kube-system. k8s.cluster.name: mps-nonprod-rno\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-mps-nonprod-rno-worker-z2-17\",
    \"entityName\": \"mps-nonprod-rno-worker-z2-17\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{\"entityId\": \"KUBERNETES_WORKLOAD-coredns\",\"entityName\": \"coredns\",\"entityType\": \"KUBERNETES_WORKLOAD\"}],
  \"customProperties\": {
    \"k8s.node.name\": \"mps-nonprod-rno-worker-z2-17\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"kube-system\",
    \"k8s.workload.name\": \"coredns\",
    \"k8s.workload.kind\": \"deployment\",
    \"environment\": \"ADC\"
  }
}"
pause 2

# k8preview01-rno coredns — DIFFERENT cluster UID
post "3/4 [k8s_node] k8preview01-rno node NotReady (k8preview01-rno)" "P-S9-PVNODE-${S9}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S9-PVNODE-${S9}\",
  \"problemTitle\": \"Kubernetes node k8preview01-cs-vm-worker06-rno is NotReady\",
  \"ProblemDetailsText\": \"Node k8preview01-cs-vm-worker06-rno NotReady. k8s.cluster.name: k8preview01-rno. host.name: k8preview01-cs-vm-worker06-rno\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-k8preview01-cs-vm-worker06-rno\",
    \"entityName\": \"k8preview01-cs-vm-worker06-rno\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{\"entityId\": \"KUBERNETES_NODE-k8preview01-cs-vm-worker06-rno\",\"entityName\": \"k8preview01-cs-vm-worker06-rno\",\"entityType\": \"KUBERNETES_NODE\"}],
  \"customProperties\": {
    \"host.name\": \"k8preview01-cs-vm-worker06-rno\",
    \"k8s.node.name\": \"k8preview01-cs-vm-worker06-rno\",
    \"k8s.cluster.name\": \"k8preview01-rno\",
    \"k8s.cluster.uid\": \"${K8PREV_UID}\",
    \"impacted_entity\": \"k8preview01-cs-vm-worker06-rno\",
    \"environment\": \"ADC\"
  }
}"
pause 2

post "4/4 [k8s_workload] coredns evicted (k8preview01-rno — DIFFERENT cluster!)" "P-S9-CORE-PV-${S9}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S9-CORE-PV-${S9}\",
  \"problemTitle\": \"Not all pods ready — coredns in kube-system (k8preview01-rno)\",
  \"ProblemDetailsText\": \"coredns pod evicted from k8preview01-cs-vm-worker06-rno. k8s.namespace.name: kube-system. k8s.cluster.name: k8preview01-rno. DIFFERENT cluster from nonprod-rno.\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-k8preview01-cs-vm-worker06-rno\",
    \"entityName\": \"k8preview01-cs-vm-worker06-rno\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{\"entityId\": \"KUBERNETES_WORKLOAD-coredns\",\"entityName\": \"coredns\",\"entityType\": \"KUBERNETES_WORKLOAD\"}],
  \"customProperties\": {
    \"k8s.node.name\": \"k8preview01-cs-vm-worker06-rno\",
    \"k8s.cluster.name\": \"k8preview01-rno\",
    \"k8s.cluster.uid\": \"${K8PREV_UID}\",
    \"k8s.namespace.name\": \"kube-system\",
    \"k8s.workload.name\": \"coredns\",
    \"k8s.workload.kind\": \"deployment\",
    \"environment\": \"ADC\"
  }
}"

pause 8; dbcheck
echo ""
n_np=$(incident_count "mps-nonprod-rno-worker-z2-17")
n_pv=$(incident_count "k8preview01-cs-vm-worker06-rno")
check "S9 nonprod-rno incident (UID=${NONPROD_UID:0:8}…)" "$n_np" "1"
check "S9 k8preview01-rno incident (UID=${K8PREV_UID:0:8}…)" "$n_pv" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 10 — Ghost/decommissioned entity alert isolation
#
# WHAT: An alert fires for a known-decommissioned BM that still exists in the
#   topology DB. A live BM alert fires at the same time.
#   The ghost BM alert must NOT attach to the live BM incident.
#
# HOW CORRELATION WORKS (WHY THEY STAY SEPARATE):
#   ① Ghost BM rootCauseEntity = GHOST_BM (distinct entity ID from BM1)
#   ② Live BM rootCauseEntity = BM1 (distinct entity ID)
#   ③ Different rootCauseEntity values → different correlation_id → 2 incidents
#   ④ Even though timestamps overlap and entity types match (both bare_metal),
#      the RCE correlation key is the entity name, not the type
#   Expected: 2 separate incidents (ghost gets its own isolated incident)
# ══════════════════════════════════════════════════════════════════════════════
if should_run 10; then
section "Scenario 10 [ghost-entity] Decommissioned BM alert isolation"
info "Ghost BM: ${GHOST_BM} (decommissioned, still in topology DB)"
info "Live BM:  ${BM1} (active, used in scenario 1)"
why "Different rootCauseEntity names → different correlation_id → 2 incidents, no cross-merge"
S10="S10-${TS}"

# Ghost BM alert (should create its own isolated incident)
post "1/2 [bare_metal] GHOST BM alert (decommissioned rack42)" "P-S10-GHOST-${S10}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S10-GHOST-${S10}\",
  \"problemTitle\": \"Host or monitoring unavailable — ${GHOST_BM}\",
  \"ProblemDetailsText\": \"Stale monitoring agent on decommissioned host ${GHOST_BM} firing alerts. Rack 42 was decommissioned 2024-11-15 but Dynatrace configuration not cleaned up. host.name: ${GHOST_BM}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${GHOST_BM}\",
    \"entityName\": \"${GHOST_BM}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"HOST-${GHOST_BM}\",
    \"entityName\": \"${GHOST_BM}\",
    \"entityType\": \"HOST\"
  }],
  \"customProperties\": {
    \"host.name\": \"${GHOST_BM}\",
    \"entity_type\": \"bare_metal\",
    \"impacted_entity\": \"${GHOST_BM}\",
    \"environment\": \"ADC\",
    \"datacenter\": \"rno\"
  }
}"
pause 3

# Live BM alert for BM1 at same time — must stay separate
post "2/2 [bare_metal] BM1 live alert (active host, same entity type)" "P-S10-LIVE-${S10}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S10-LIVE-${S10}\",
  \"problemTitle\": \"CPU saturation on host — ${BM1}\",
  \"ProblemDetailsText\": \"Active BM host ${BM1} at 92% CPU. vCPU scheduling delays observed. host.name: ${BM1}\",
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
    \"entity_type\": \"bare_metal\",
    \"impacted_entity\": \"${BM1}\",
    \"environment\": \"ADC\"
  }
}"

pause 7; dbcheck
echo ""
n_ghost=$(incident_count "${GHOST_BM}")
n_live=$(incident_count "${BM1}")
check "S10: ghost BM has own isolated incident" "$n_ghost" "1"
check "S10: live BM1 has own incident (not merged with ghost)" "$n_live" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 11 — Late-arriving root cause (downstreams first, root 15s later)
#
# WHAT: Workload alerts arrive FIRST, each creating their own small incidents.
#   The true root (BM alert with rootCauseEntity=BM1) arrives 15s later.
#   The EvolutionEngine should detect the new root and merge the scattered
#   downstream incidents under it.
#
# HOW CORRELATION WORKS:
#   ① T+0s: 3 workload alerts arrive, each without rootCauseEntity → each may
#      create its own incident (or small group)
#   ② T+15s: BM root alert arrives with rootCauseEntity=BM1
#      → EvolutionEngine evaluateMerges() fires within 30s
#      → scattered incidents with matching labels merge under BM1's incident
#   ③ After merge: 1 incident with all 4 alerts
#   Expected: final state = 1 incident for correlation_id=BM1 with 4+ alerts
# ══════════════════════════════════════════════════════════════════════════════
if should_run 11; then
section "Scenario 11 [late-root] Downstream workloads first, BM root arrives 15s later"
info "Step 1: 3 workload alerts → may create scattered incidents"
info "Step 2: BM root arrives 15s later → EvolutionEngine merges them"
why "Tests EvolutionEngine merge path: delayed root causes consolidation of scattered incidents"
S11="S11-${TS}"

echo ""
echo -e "  ${YLW}Phase 1: Sending downstream alerts (no root context yet)…${RST}"

post "1/4 [k8s_workload] dex pods failing — no root yet" "P-S11-DEX-${S11}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S11-DEX-${S11}\",
  \"problemTitle\": \"Not all pods ready — dex in dex namespace\",
  \"ProblemDetailsText\": \"dex pods entering CrashLoopBackOff. k8s.namespace.name: dex. k8s.cluster.name: mps-nonprod-rno. k8s.node.name: ${NODE1}\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"impactedEntities\": [{\"entityId\": \"KUBERNETES_WORKLOAD-dex\",\"entityName\": \"dex\",\"entityType\": \"KUBERNETES_WORKLOAD\"}],
  \"customProperties\": {
    \"k8s.node.name\": \"${NODE1}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"dex\",
    \"k8s.workload.name\": \"dex\",
    \"k8s.workload.kind\": \"deployment\",
    \"environment\": \"ADC\"
  }
}"
pause 2

post "2/4 [k8s_workload] argocd-repo-server evicted — no root yet" "P-S11-ARGOCD-${S11}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S11-ARGOCD-${S11}\",
  \"problemTitle\": \"Not all pods ready — argocd-repo-server in argocd\",
  \"ProblemDetailsText\": \"argocd-repo-server pod evicted. k8s.namespace.name: argocd. k8s.cluster.name: mps-nonprod-rno. k8s.node.name: ${NODE1}\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"impactedEntities\": [{\"entityId\": \"KUBERNETES_WORKLOAD-argocd-repo-server\",\"entityName\": \"argocd-repo-server\",\"entityType\": \"KUBERNETES_WORKLOAD\"}],
  \"customProperties\": {
    \"k8s.node.name\": \"${NODE1}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"argocd\",
    \"k8s.workload.name\": \"argocd-repo-server\",
    \"k8s.workload.kind\": \"deployment\",
    \"environment\": \"ADC\"
  }
}"
pause 2

post "3/4 [k8s_node] ${NODE1} NotReady — no root yet" "P-S11-NODE-${S11}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S11-NODE-${S11}\",
  \"problemTitle\": \"Kubernetes node ${NODE1} is NotReady\",
  \"ProblemDetailsText\": \"Node ${NODE1} entered NotReady state. Cause unknown yet. k8s.node.name: ${NODE1}. k8s.cluster.name: mps-nonprod-rno\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"impactedEntities\": [{\"entityId\": \"KUBERNETES_NODE-${NODE1}\",\"entityName\": \"${NODE1}\",\"entityType\": \"KUBERNETES_NODE\"}],
  \"customProperties\": {
    \"host.name\": \"${VM1}\",
    \"k8s.node.name\": \"${NODE1}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"environment\": \"ADC\"
  }
}"

echo ""
echo -e "  ${YLW}Phase 2: Simulating detection delay (15s)…${RST}"
echo -e "  ${DIM}Scattered incidents may exist right now — EvolutionEngine will merge after root arrives${RST}"
pause 15

echo ""
echo -e "  ${YLW}Phase 2: BM root alert now arrives (late root cause identified)…${RST}"

post "4/4 [bare_metal] BM1 power fault (LATE ROOT — 15s delay)" "P-S11-BM-${S11}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S11-BM-${S11}\",
  \"problemTitle\": \"Host or monitoring unavailable — ${BM1} (late detection)\",
  \"ProblemDetailsText\": \"KVM hypervisor ${BM1} power fault confirmed by IPMI. This is the root cause of the node/workload failures observed 15s ago. host.name: ${BM1}\",
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
    \"entityId\": \"HOST-${BM1}\",
    \"entityName\": \"${BM1}\",
    \"entityType\": \"HOST\"
  }],
  \"customProperties\": {
    \"host.name\": \"${BM1}\",
    \"entity_type\": \"bare_metal\",
    \"impacted_entity\": \"${BM1}\",
    \"environment\": \"ADC\"
  }
}"

echo ""
echo -e "  ${DIM}Waiting 35s for EvolutionEngine to run and merge scattered incidents…${RST}"
pause 35
logs; dbcheck
echo ""
echo -e "  ${YLW}Expected: scattered downstream incidents merged under BM1 root${RST}"
n=$(incident_count "${BM1}")
check "S11: BM1 incident exists after late-root merge" "$n" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 12 — Oscillation guard (cascade → resolve → re-send same cascade)
#
# WHAT: Sends a full BM→workload cascade, resolves all alerts, then immediately
#   sends the exact same cascade again (within 5 minutes). The EvolutionEngine
#   recentMerges convergence guard should prevent the survivor incident from
#   being immediately re-merged with the new incidents.
#
# HOW CORRELATION WORKS:
#   ① First cascade: 1 incident created and survivor-merged
#   ② RESOLVED sent: incident resolves
#   ③ Second cascade within 5 min: new OPEN alerts arrive
#   ④ New BM root creates NEW incident
#   ⑤ recentMerges guard: survivor from step 1 is NOT re-merged into new incident
#      within the 5-minute cooldown window
#   Expected: 2nd cascade creates a separate fresh incident, no oscillation
# ══════════════════════════════════════════════════════════════════════════════
if should_run 12; then
section "Scenario 12 [convergence-guard] Cascade → resolve → re-send (oscillation test)"
info "BM2: ${BM2} (node on BM2 cascade)"
why "recentMerges map prevents survivor re-merge within 5-min cooldown after RESOLVED"
S12="S12-${TS}"
S12_ROOT="${BM_MDN}"  # use MDN BM to avoid collision with other scenarios

echo ""
echo -e "  ${YLW}Phase 1: First cascade…${RST}"

post "1/3 [bare_metal] MDN BM power fault (1st cascade)" "P-S12-BM1-${S12}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S12-BM1-${S12}\",
  \"problemTitle\": \"Host or monitoring unavailable — ${S12_ROOT} (oscillation test round 1)\",
  \"ProblemDetailsText\": \"BM ${S12_ROOT} power fault. host.name: ${S12_ROOT}\",
  \"impactLevel\": \"INFRASTRUCTURE\",\"severity\": \"AVAILABILITY\",\"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${S12_ROOT}\",\"entityName\": \"${S12_ROOT}\",\"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"HOST-${S12_ROOT}\",\"entityName\": \"${S12_ROOT}\",\"entityType\": \"HOST\"}],
  \"customProperties\": {
    \"host.name\": \"${S12_ROOT}\",\"entity_type\": \"bare_metal\",
    \"impacted_entity\": \"${S12_ROOT}\",\"environment\": \"MDN\"
  }
}"
pause 3

post "2/3 [k8s_workload] MDN workload evicted (1st cascade)" "P-S12-WL1-${S12}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S12-WL1-${S12}\",
  \"problemTitle\": \"Not all pods ready — mondev-app in mps-mondev-mdn (oscillation test)\",
  \"ProblemDetailsText\": \"mondev-app pod evicted from ${NODE_MDN}. k8s.namespace.name: mps-dev. k8s.cluster.name: mps-mondev-mdn\",
  \"impactLevel\": \"SERVICE\",\"severity\": \"AVAILABILITY\",\"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${S12_ROOT}\",\"entityName\": \"${S12_ROOT}\",\"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"KUBERNETES_WORKLOAD-mondev-app\",\"entityName\": \"mondev-app\",\"entityType\": \"KUBERNETES_WORKLOAD\"}],
  \"customProperties\": {
    \"k8s.node.name\": \"${NODE_MDN}\",
    \"k8s.cluster.name\": \"mps-mondev-mdn\",
    \"k8s.cluster.uid\": \"${MONDEV_UID}\",
    \"k8s.namespace.name\": \"mps-dev\",
    \"k8s.workload.name\": \"mondev-app\",
    \"k8s.workload.kind\": \"deployment\",
    \"environment\": \"MDN\"
  }
}"
pause 6

echo ""
echo -e "  ${YLW}Phase 1 complete — resolving all alerts…${RST}"

post "RESOLVED — BM (1st cascade)" "P-S12-BM1-${S12}" "{
  \"state\": \"RESOLVED\",\"problemId\": \"P-S12-BM1-${S12}\",
  \"problemTitle\": \"RESOLVED: ${S12_ROOT} power restored\",
  \"ProblemDetailsText\": \"Power restored. Infrastructure recovered.\",
  \"impactLevel\": \"INFRASTRUCTURE\",\"severity\": \"AVAILABILITY\",\"status\": \"RESOLVED\",
  \"startTime\": \"${NOW}\",\"endTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${S12_ROOT}\",\"entityName\": \"${S12_ROOT}\",\"entityType\": \"HOST\"},
  \"impactedEntities\": [],
  \"customProperties\": {\"host.name\": \"${S12_ROOT}\",\"entity_type\": \"bare_metal\",\"environment\": \"MDN\"}
}"
pause 5

echo ""
echo -e "  ${YLW}Phase 2: Same cascade immediately re-fired (within 5-min convergence window)…${RST}"
echo -e "  ${DIM}recentMerges guard should prevent oscillation${RST}"

post "3/3 [bare_metal] MDN BM power fault (2nd cascade — same entity)" "P-S12-BM2-${S12}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-S12-BM2-${S12}\",
  \"problemTitle\": \"Host or monitoring unavailable — ${S12_ROOT} (oscillation test round 2)\",
  \"ProblemDetailsText\": \"BM ${S12_ROOT} power fault again — transient issue. host.name: ${S12_ROOT}\",
  \"impactLevel\": \"INFRASTRUCTURE\",\"severity\": \"AVAILABILITY\",\"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {\"entityId\": \"HOST-${S12_ROOT}\",\"entityName\": \"${S12_ROOT}\",\"entityType\": \"HOST\"},
  \"impactedEntities\": [{\"entityId\": \"HOST-${S12_ROOT}\",\"entityName\": \"${S12_ROOT}\",\"entityType\": \"HOST\"}],
  \"customProperties\": {
    \"host.name\": \"${S12_ROOT}\",\"entity_type\": \"bare_metal\",
    \"impacted_entity\": \"${S12_ROOT}\",\"environment\": \"MDN\"
  }
}"

pause 10; dbcheck
echo ""
echo -e "  ${YLW}Checking: 2nd cascade should create NEW incident, no merge with resolved 1st${RST}"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 13 — Full RESOLVED chain (BM cascade → all RESOLVED)
#
# WHAT: Sends a complete 5-alert BM→VM→node→2-workloads cascade, waits for
#   1 incident to be created, then resolves all alerts. The incident should
#   automatically transition to resolved/closed.
#
# HOW RESOLUTION WORKS:
#   ① All 5 alerts OPEN → 1 incident created (or verified from prior runs)
#   ② All 5 alerts RESOLVED within seconds
#   ③ incident_resolver background job checks: all alerts resolved?
#      → incident status transitions to 'resolved' with resolved_at timestamp
#   Expected: incident.status=resolved, resolved_at IS NOT NULL
# ══════════════════════════════════════════════════════════════════════════════
if should_run 13; then
section "Scenario 13 [resolution] Full cascade then RESOLVED propagation"
info "BM: ${BM2}, VM2: ${VM2}, Node2: ${NODE2}"
why "Tests incident auto-resolution when all constituent alerts are resolved"
S13="S13-${TS}"

echo ""
echo -e "  ${YLW}Phase 1: OPEN cascade…${RST}"

for i in 1 2 3 4 5; do
  case $i in
    1) post "OPEN 1/5 [bare_metal] BM2 power fault" "P-S13-BM-${S13}" "{
      \"state\":\"OPEN\",\"problemId\":\"P-S13-BM-${S13}\",
      \"problemTitle\":\"Host unavailable — ${BM2}\",
      \"ProblemDetailsText\":\"${BM2} power fault. host.name: ${BM2}\",
      \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
      \"rootCauseEntity\":{\"entityId\":\"HOST-${BM2}\",\"entityName\":\"${BM2}\",\"entityType\":\"HOST\"},
      \"impactedEntities\":[{\"entityId\":\"HOST-${BM2}\",\"entityName\":\"${BM2}\",\"entityType\":\"HOST\"}],
      \"customProperties\":{\"host.name\":\"${BM2}\",\"entity_type\":\"bare_metal\",\"impacted_entity\":\"${BM2}\",\"environment\":\"ADC\"}
    }"; pause 2 ;;
    2) post "OPEN 2/5 [vm] VM2 unreachable" "P-S13-VM-${S13}" "{
      \"state\":\"OPEN\",\"problemId\":\"P-S13-VM-${S13}\",
      \"problemTitle\":\"Host unavailable — ${VM2}\",
      \"ProblemDetailsText\":\"VM ${VM2} unresponsive. host.name: ${VM2}\",
      \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
      \"rootCauseEntity\":{\"entityId\":\"HOST-${BM2}\",\"entityName\":\"${BM2}\",\"entityType\":\"HOST\"},
      \"impactedEntities\":[{\"entityId\":\"HOST-${VM2}\",\"entityName\":\"${VM2}\",\"entityType\":\"HOST\"}],
      \"customProperties\":{\"host.name\":\"${VM2}\",\"entity_type\":\"vm\",\"environment\":\"ADC\"}
    }"; pause 2 ;;
    3) post "OPEN 3/5 [k8s_node] Node2 NotReady" "P-S13-NODE-${S13}" "{
      \"state\":\"OPEN\",\"problemId\":\"P-S13-NODE-${S13}\",
      \"problemTitle\":\"K8s node ${NODE2} NotReady\",
      \"ProblemDetailsText\":\"Node ${NODE2} NotReady. k8s.node.name: ${NODE2}. k8s.cluster.name: mps-nonprod-rno\",
      \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
      \"rootCauseEntity\":{\"entityId\":\"HOST-${BM2}\",\"entityName\":\"${BM2}\",\"entityType\":\"HOST\"},
      \"impactedEntities\":[{\"entityId\":\"KUBERNETES_NODE-${NODE2}\",\"entityName\":\"${NODE2}\",\"entityType\":\"KUBERNETES_NODE\"}],
      \"customProperties\":{\"host.name\":\"${VM2}\",\"k8s.node.name\":\"${NODE2}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"environment\":\"ADC\"}
    }"; pause 2 ;;
    4) post "OPEN 4/5 [k8s_workload] geo-service evicted" "P-S13-GEO-${S13}" "{
      \"state\":\"OPEN\",\"problemId\":\"P-S13-GEO-${S13}\",
      \"problemTitle\":\"Not all pods ready — geo-service in geo-service-perf\",
      \"ProblemDetailsText\":\"geo-service-78fc45969-qtlxb evicted from ${NODE2}. k8s.namespace.name: geo-service-perf\",
      \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
      \"rootCauseEntity\":{\"entityId\":\"HOST-${BM2}\",\"entityName\":\"${BM2}\",\"entityType\":\"HOST\"},
      \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-geo-service\",\"entityName\":\"geo-service\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
      \"customProperties\":{\"k8s.node.name\":\"${NODE2}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"k8s.namespace.name\":\"geo-service-perf\",\"k8s.workload.name\":\"geo-service\",\"environment\":\"ADC\"}
    }"; pause 2 ;;
    5) post "OPEN 5/5 [k8s_workload] coredns evicted" "P-S13-CORE-${S13}" "{
      \"state\":\"OPEN\",\"problemId\":\"P-S13-CORE-${S13}\",
      \"problemTitle\":\"Not all pods ready — coredns in kube-system\",
      \"ProblemDetailsText\":\"coredns evicted from ${NODE2}. k8s.namespace.name: kube-system. k8s.cluster.name: mps-nonprod-rno\",
      \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
      \"rootCauseEntity\":{\"entityId\":\"HOST-${BM2}\",\"entityName\":\"${BM2}\",\"entityType\":\"HOST\"},
      \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-coredns\",\"entityName\":\"coredns\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
      \"customProperties\":{\"k8s.node.name\":\"${NODE2}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"k8s.namespace.name\":\"kube-system\",\"k8s.workload.name\":\"coredns\",\"environment\":\"ADC\"}
    }"; pause 2 ;;
  esac
done

pause 6; dbcheck
echo ""
echo -e "  ${YLW}Phase 2: Resolving all 5 alerts now…${RST}"

for pid_suffix in "BM" "VM" "NODE" "GEO" "CORE"; do
  post "RESOLVED P-S13-${pid_suffix}-${S13}" "P-S13-${pid_suffix}-${S13}" "{
    \"state\":\"RESOLVED\",\"problemId\":\"P-S13-${pid_suffix}-${S13}\",
    \"problemTitle\":\"RESOLVED: ${pid_suffix} alert\",
    \"ProblemDetailsText\":\"Infrastructure recovered.\",
    \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"RESOLVED\",
    \"startTime\":\"${NOW}\",\"endTime\":\"${NOW}\",
    \"rootCauseEntity\":{\"entityId\":\"HOST-${BM2}\",\"entityName\":\"${BM2}\",\"entityType\":\"HOST\"},
    \"impactedEntities\":[],
    \"customProperties\":{\"host.name\":\"${BM2}\",\"entity_type\":\"bare_metal\",\"environment\":\"ADC\"}
  }"
done

pause 8; dbcheck
echo ""
echo -e "  ${YLW}Checking incident.status transitioned to resolved…${RST}"
kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
  SELECT id, status, resolved_at IS NOT NULL as has_resolved_at,
         jsonb_array_length(alert_ids) as alert_cnt, LEFT(title,50)
  FROM incidents WHERE auto_created=true AND correlation_id='${BM2}'
    AND updated_at > NOW()-INTERVAL '15 minutes'
  ORDER BY created_at DESC LIMIT 2;" 2>/dev/null \
  || echo -e "  ${DIM}(kubectl unavailable)${RST}"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 14 — Multi-DC: RNO BM + MDN BM simultaneously → 2 incidents
#
# WHAT: Two different bare-metal hosts in two different datacenters (RNO and MDN)
#   fail at the same time. Each has downstream workload alerts.
#   Must produce exactly 2 separate incidents, never 1.
#
# HOW CORRELATION WORKS (WHY THEY STAY SEPARATE):
#   ① RNO: rootCauseEntity=BM1 (cloudstack-cluster-2-iapps-100-67-61-18)
#   ② MDN: rootCauseEntity=BM_MDN (mps-mondev-mdn-worker-z3-05)
#   ③ Different rootCauseEntity → different correlation_id → 2 incidents
#   ④ Even same entity_type (bare_metal), same timestamp → still separate
#   ⑤ k8s workloads differ by k8s.cluster.uid (NONPROD vs MONDEV)
#   Expected: 2 incidents, 1 per DC
# ══════════════════════════════════════════════════════════════════════════════
if should_run 14; then
section "Scenario 14 [multi-DC] RNO BM + MDN BM simultaneously → 2 incidents"
info "RNO: ${BM1} (cloudstack-cluster-2, nonprod-rno cluster)"
info "MDN: ${BM_MDN} (kubeadm, mps-mondev-mdn cluster)"
why "Different rootCauseEntity + different k8s.cluster.uid → no cross-DC merge possible"
S14="S14-${TS}"

# RNO DC — BM1 and downstream workload
post "1/4 [bare_metal] RNO BM1 power fault" "P-S14-RNO-BM-${S14}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-S14-RNO-BM-${S14}\",
  \"problemTitle\":\"Host unavailable — ${BM1} (RNO DC)\",
  \"ProblemDetailsText\":\"${BM1} power fault in RNO datacenter. host.name: ${BM1}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${BM1}\",\"entityName\":\"${BM1}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${BM1}\",\"entityName\":\"${BM1}\",\"entityType\":\"HOST\"}],
  \"customProperties\":{\"host.name\":\"${BM1}\",\"entity_type\":\"bare_metal\",\"impacted_entity\":\"${BM1}\",\"datacenter\":\"rno\",\"environment\":\"ADC\"}
}"
pause 2

post "2/4 [k8s_workload] RNO aem-qa evicted (RNO DC)" "P-S14-RNO-WL-${S14}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-S14-RNO-WL-${S14}\",
  \"problemTitle\":\"Not all pods ready — dispatcher-publish-preview in aem-qa (RNO)\",
  \"ProblemDetailsText\":\"StatefulSet pod dispatcher-publish-preview-1 lost on ${NODE1}. k8s.namespace.name: aem-qa. k8s.cluster.name: mps-nonprod-rno\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${BM1}\",\"entityName\":\"${BM1}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-dispatcher-publish-preview\",\"entityName\":\"dispatcher-publish-preview\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"customProperties\":{\"k8s.node.name\":\"${NODE1}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"k8s.namespace.name\":\"aem-qa\",\"k8s.workload.name\":\"dispatcher-publish-preview\",\"k8s.workload.kind\":\"statefulset\",\"datacenter\":\"rno\",\"environment\":\"ADC\"}
}"
pause 3

# MDN DC — BM_MDN and downstream workload (fires simultaneously)
post "3/4 [bare_metal] MDN BM power fault" "P-S14-MDN-BM-${S14}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-S14-MDN-BM-${S14}\",
  \"problemTitle\":\"Host unavailable — ${BM_MDN} (MDN DC)\",
  \"ProblemDetailsText\":\"${BM_MDN} disk failure in MDN datacenter. host.name: ${BM_MDN}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${BM_MDN}\",\"entityName\":\"${BM_MDN}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${BM_MDN}\",\"entityName\":\"${BM_MDN}\",\"entityType\":\"HOST\"}],
  \"customProperties\":{\"host.name\":\"${BM_MDN}\",\"entity_type\":\"bare_metal\",\"impacted_entity\":\"${BM_MDN}\",\"datacenter\":\"mdn\",\"environment\":\"MDN\"}
}"
pause 2

post "4/4 [k8s_workload] MDN mondev-app evicted (MDN DC)" "P-S14-MDN-WL-${S14}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-S14-MDN-WL-${S14}\",
  \"problemTitle\":\"Not all pods ready — mondev-app in mps-dev (MDN)\",
  \"ProblemDetailsText\":\"mondev-app pod evicted from ${NODE_MDN}. k8s.namespace.name: mps-dev. k8s.cluster.name: mps-mondev-mdn. DIFFERENT DC from RNO.\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${BM_MDN}\",\"entityName\":\"${BM_MDN}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-mondev-app\",\"entityName\":\"mondev-app\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"customProperties\":{\"k8s.node.name\":\"${NODE_MDN}\",\"k8s.cluster.name\":\"mps-mondev-mdn\",\"k8s.cluster.uid\":\"${MONDEV_UID}\",\"k8s.namespace.name\":\"mps-dev\",\"k8s.workload.name\":\"mondev-app\",\"k8s.workload.kind\":\"deployment\",\"datacenter\":\"mdn\",\"environment\":\"MDN\"}
}"

pause 8; logs; dbcheck
echo ""
n_rno=$(incident_count "${BM1}")
n_mdn=$(incident_count "${BM_MDN}")
check "S14: RNO incident (root=${BM1})" "$n_rno" "1"
check "S14: MDN incident (root=${BM_MDN})" "$n_mdn" "1"
fi

# ── final summary ─────────────────────────────────────────────────────────────
echo ""
echo -e "${BOLD}${CYN}══════════════════════════════════════════════════════════${RST}"
echo -e "${BOLD}  Final Summary — Incidents last 15 minutes${RST}"
echo -e "${BOLD}${CYN}══════════════════════════════════════════════════════════${RST}"
kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -c "
  SELECT
    LEFT(COALESCE(correlation_id,'?'),30)    AS correlation_id,
    status,
    jsonb_array_length(alert_ids)            AS alerts,
    created_at::time(0)                      AS at,
    LEFT(title,42)                           AS title
  FROM incidents
  WHERE auto_created = true
    AND created_at > NOW() - INTERVAL '15 minutes'
  ORDER BY created_at DESC
  LIMIT 20;" 2>/dev/null \
  || echo -e "  ${DIM}View incidents: https://aileron.example.com/incidents${RST}"

echo ""
echo -e "${DIM}To re-run a single scenario:  bash scripts/test_all_entity_types.sh <N>${RST}"
echo -e "${DIM}Dry run all:                  DRYRUN=1 bash scripts/test_all_entity_types.sh${RST}"
echo ""
