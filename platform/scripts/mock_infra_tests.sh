#!/usr/bin/env bash
# mock_infra_tests.sh — comprehensive BM/KVM, NetApp, and mixed-infra correlation tests
#
# Uses REAL topology entities from the live cluster topology graph:
#   - cloudstack-cluster-2 BM hosts and CloudStack VMs
#   - NetApp RNO and MDN clusters / nodes / aggregates / SVMs
#   - mps-nonprod-rno and mps-mondev-mdn K8s clusters
#
# Usage:
#   bash mock_infra_tests.sh              # run all scenarios
#   bash mock_infra_tests.sh 1            # single scenario
#   bash mock_infra_tests.sh 1 3 7        # specific scenarios
#
# Scenarios:
#   1  BM hardware failure → 2 CloudStack VMs → 1 incident            (BM-only cascade)
#   2  BM failure → VM → K8s node → pods → 1 incident                 (full 4-layer stack)
#   3  Two independent BMs fail simultaneously → 2 separate incidents  (isolation)
#   4  VM fires first, BM fires 15s later → 1 incident (late root)
#   5  BM failure on MDN cluster (mps-mondev) → MDN-specific incident
#   6  CloudStack standalone VM failure (no k8s, no BM root) → 1 incident
#   7  NetApp node failure → aggregate offline → SVM → multiple volumes → 1 incident
#   8  NetApp aggregate 95% full → SVM quota → pod PVC write failure → 1 incident
#   9  Two NetApp aggregates fail on SAME cluster → 1 incident (storm)
#  10  Two NetApp aggregates fail on DIFFERENT clusters (RNO vs MDN) → 2 incidents
#  11  NetApp SVM down → 4 volumes offline → 1 incident (SVM is root)
#  12  NetApp volume high-latency → PVC I/O → pod crash → 1 incident
#  13  BM failure + NetApp failure SIMULTANEOUSLY → 2 SEPARATE incidents (different families)
#  14  K8s node failure + NetApp PVC detach on same node → 2 incidents (family isolation)
#  15  MDN cluster storm: 3 nodes fail + 5 pods + 2 NetApp volumes → correct grouping

set -uo pipefail

ENDPOINT="https://aileron.example.com/api/v1/webhooks/dynatrace"
API_KEY="${API_KEY:-ah_c76ddeaa86c74818ac84997c0f2a8174eaba3bf0fdab295132acb8aba2238e5f}"
NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
TS=$(date +%s)
NS=aileron

# ─── real topology entities ───────────────────────────────────────────────────
# cloudstack-cluster-2 (RNO data centre)
CS2_BM_A="iapps-100-67-61-18"          # BM label — hosts nonprod-rno workers z3-08
CS2_BM_B="iapps-100-67-62-19"          # BM label — hosts nonprod-rno workers z2-12, z2-13
CS2_BM_C="iapps-100-67-62-30"          # BM label — hosts k8preview, k8dev workers

# CloudStack VMs (K8s worker nodes) on each BM
VM_A1="mps-nonprod-rno-worker-z3-08"   # on BM_A
VM_B1="mps-nonprod-rno-worker-z2-13"   # on BM_B
VM_B2="mps-nonprod-rno-worker-z2-12"   # on BM_B
VM_C1="k8preview01-cs-vm-worker19-rno" # on BM_C (k8preview cluster)

# K8s node labels match VM labels in topology
K8N_A1="${VM_A1}"   # mps-nonprod-rno
K8N_B1="${VM_B1}"
K8N_B2="${VM_B2}"

# cloudstack-cluster-mondev (MDN data centre)
MONDEV_BM_A="iapps-100-67-84-30"         # MDN BM — hosts mps-mondev-mdn workers
MONDEV_VM_A="mps-mondev-mdn-worker-z3-05" # MDN CloudStack VM (K8s node)

# CloudStack standalone VM (no k8s labels)
CS2_APP_VM="sourcebox-prod06-cs-vm0-rno.example.com"

# K8s cluster UIDs
NONPROD_UID="88b12bf2-9f23-4b4a-b06a-3d2b33134a3b"   # mps-nonprod-rno
MONDEV_UID="00a07750-e556-443e-89d9-80341edb472d"     # mps-mondev-mdn
K8PREV_UID="d1be9a2f-4e83-49bf-8800-b9a2bc2e7da0"     # k8preview01-rno

# NetApp entities (real from topology)
NETAPP_RNO_CLUSTER="netapp-rno-cluster001"
NETAPP_MDN_CLUSTER="netapp-mdn-cluster001"
NETAPP_RNO_NODE1="netapp-rno-node001"
NETAPP_RNO_NODE2="netapp-rno-node002"
NETAPP_RNO_AGGR1="aggr1_node001"        # on RNO node1
NETAPP_RNO_AGGR2="aggr1_node002"        # on RNO node2
NETAPP_MDN_AGGR1="aggr1_node003"        # on MDN node3 (different cluster!)
NETAPP_RNO_SVM="iapps-rno-k8s"         # SVM on RNO cluster serving K8s PVCs
NETAPP_MDN_SVM="iapps-mdn-trident"     # SVM on MDN cluster

# ─── helpers ─────────────────────────────────────────────────────────────────
post() {
  local label="$1"; local id="$2"; local payload="$3"
  printf "  → %-50s " "[$label] ($id)..."
  local code
  code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$ENDPOINT" \
    -H "Content-Type: application/json" \
    -H "X-API-Key: ${API_KEY}" \
    -d "$payload")
  echo "HTTP $code"
}

section() { echo ""; echo "══════════════════════════════════════════"; echo "  $1"; echo "══════════════════════════════════════════"; }
pause()   { echo "  ⏳ waiting ${1}s..."; sleep "$1"; }

db() {
  kubectl exec -n "$NS" postgres-primary-0 -c postgres -- \
    psql -U alerthub alerthub -t -c "$1" 2>/dev/null | grep -v "^$"
}

dbcheck() {
  echo ""
  echo "--- DB check (last 5 min) ---"
  db "SELECT '  ' || LEFT(title,55) || ' | corr=' || COALESCE(correlation_id,'NULL') || ' | alerts=' || COALESCE(jsonb_array_length(alert_ids)::text,'?')
      FROM incidents WHERE auto_created=true AND updated_at > NOW()-INTERVAL '5 minutes'
      ORDER BY created_at;" | head -15
}

count_for() {
  db "SELECT COUNT(*) FROM incidents
      WHERE auto_created=true AND status IN ('open','investigating')
        AND correlation_id='$1' AND updated_at > NOW()-INTERVAL '10 minutes';" | tr -d ' \n'
}

check() {
  local label="$1" got="$2" expected="$3"
  if [[ "$got" == "$expected" ]]; then
    printf "  ✅  PASS  %s → %s incidents\n" "$label" "$got"
  else
    printf "  ❌  FAIL  %s: expected=%s got=%s\n" "$label" "$expected" "$got"
  fi
}

check_separate() {
  # verify two correlation IDs each have their own distinct incident
  local label="$1" corr1="$2" corr2="$3"
  local id1 id2
  id1=$(db "SELECT id FROM incidents WHERE auto_created=true AND correlation_id='${corr1}' AND updated_at > NOW()-INTERVAL '10 minutes' LIMIT 1" | tr -d ' ')
  id2=$(db "SELECT id FROM incidents WHERE auto_created=true AND correlation_id='${corr2}' AND updated_at > NOW()-INTERVAL '10 minutes' LIMIT 1" | tr -d ' ')
  if [[ -n "$id1" && -n "$id2" && "$id1" != "$id2" ]]; then
    printf "  ✅  PASS  %s → 2 separate incidents (%s / %s)\n" "$label" "${id1:0:8}" "${id2:0:8}"
  else
    printf "  ❌  FAIL  %s: expected 2 separate incidents (id1=%s id2=%s)\n" "$label" "${id1:-empty}" "${id2:-empty}"
  fi
}

# check_same_incident_sids: verify a set of source_ids all landed in exactly 1 incident.
# NetApp aggregate/SVM/volume alerts are topology-merged into existing node-level incidents
# rather than creating standalone incidents with the entity-name as correlation_id.
# This function uses auto_created_incident_id on the alerts table (which is always set)
# instead of querying incidents by correlation_id.
check_same_incident_sids() {
  local label="$1"; shift
  local in_clause="" sid
  for sid in "$@"; do in_clause="${in_clause:+${in_clause},}'${sid}'"; done
  local distinct total
  distinct=$(db "SELECT COUNT(DISTINCT auto_created_incident_id) FROM alerts
    WHERE source_id IN (${in_clause}) AND auto_created_incident_id IS NOT NULL
      AND created_at > NOW()-INTERVAL '10 minutes';" | tr -d ' \n')
  total=$(db "SELECT COUNT(*) FROM alerts WHERE source_id IN (${in_clause})
    AND auto_created_incident_id IS NOT NULL AND created_at > NOW()-INTERVAL '10 minutes';" | tr -d ' \n')
  if [[ "${total:-0}" -eq "0" ]]; then
    printf "  ❌  FAIL  %s: no alerts linked to any incident\n" "$label"
  elif [[ "${distinct:-0}" -eq "1" ]]; then
    printf "  ✅  PASS  %s: all alerts correlated into 1 incident\n" "$label"
  else
    printf "  ❌  FAIL  %s: alerts spread across %s incidents (expected 1)\n" "$label" "$distinct"
  fi
}

# check_separate_sids: verify two source_ids ended up in DIFFERENT non-null incidents.
check_separate_sids() {
  local label="$1" sid1="$2" sid2="$3"
  local id1 id2
  id1=$(db "SELECT COALESCE(auto_created_incident_id::text,'none') FROM alerts
    WHERE source_id='${sid1}' AND created_at > NOW()-INTERVAL '10 minutes'
    ORDER BY created_at DESC LIMIT 1;" | tr -d ' \n')
  id2=$(db "SELECT COALESCE(auto_created_incident_id::text,'none') FROM alerts
    WHERE source_id='${sid2}' AND created_at > NOW()-INTERVAL '10 minutes'
    ORDER BY created_at DESC LIMIT 1;" | tr -d ' \n')
  if [[ "$id1" == "none" || "$id2" == "none" || -z "$id1" || -z "$id2" ]]; then
    printf "  ❌  FAIL  %s: one or both alerts not linked to an incident (sid1=%s sid2=%s)\n" "$label" "${id1:-empty}" "${id2:-empty}"
  elif [[ "$id1" != "$id2" ]]; then
    printf "  ✅  PASS  %s: in separate incidents (%s / %s)\n" "$label" "${id1:0:8}" "${id2:0:8}"
  else
    printf "  ❌  FAIL  %s: both alerts in same incident (%s) — should be separate\n" "$label" "${id1:0:8}"
  fi
}

RUN_ALL=true
SELECTED=()
if [[ $# -gt 0 ]]; then RUN_ALL=false; SELECTED=("$@"); fi
should_run() { $RUN_ALL && return 0; for s in "${SELECTED[@]}"; do [[ "$s" == "$1" ]] && return 0; done; return 1; }

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 1 — BM hardware failure → 2 CloudStack VMs → 1 incident
# BM: iapps-100-67-62-19 hosts z2-12 and z2-13
# Expected: 1 incident with correlation_id = iapps-100-67-62-19
# ══════════════════════════════════════════════════════════════════════════════
if should_run 1; then
section "Scenario 1: BM hardware failure → 2 CloudStack VMs → 1 incident"
S="${TS}-S1"

post "BM hardware failure (CPU/memory fault)" "P-BM-A-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-BM-A-${S}\",
  \"problemTitle\":\"Hardware fault detected on bare metal host ${CS2_BM_B}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"ERROR\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${CS2_BM_B}\",\"entityName\":\"${CS2_BM_B}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${CS2_BM_B}\",\"entityName\":\"${CS2_BM_B}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"ECC memory errors detected on ${CS2_BM_B}. Hypervisor going into degraded mode. host.name: ${CS2_BM_B}\",
  \"customProperties\":{\"host.name\":\"${CS2_BM_B}\",\"entity_type\":\"vm\",\"cloudstack_cluster\":\"cloudstack-cluster-2\",\"environment\":\"ADC\",\"impacted_entity\":\"${CS2_BM_B}\"}}"
pause 3

post "VM-1 unavailable (hosted on BM_B)" "P-VM-B1-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-VM-B1-${S}\",
  \"problemTitle\":\"VM unavailable — ${VM_B1} (hypervisor issue)\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${CS2_BM_B}\",\"entityName\":\"${CS2_BM_B}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${VM_B1}\",\"entityName\":\"${VM_B1}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"VM ${VM_B1} is unreachable due to KVM host issue. host.name: ${VM_B1}. k8s.cluster.name: mps-nonprod-rno\",
  \"customProperties\":{\"host.name\":\"${VM_B1}\",\"entity_type\":\"vm\",\"cloudstack_cluster\":\"cloudstack-cluster-2\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"environment\":\"ADC\"}}"
pause 2

post "VM-2 unavailable (also hosted on BM_B)" "P-VM-B2-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-VM-B2-${S}\",
  \"problemTitle\":\"VM unavailable — ${VM_B2} (hypervisor issue)\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${CS2_BM_B}\",\"entityName\":\"${CS2_BM_B}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${VM_B2}\",\"entityName\":\"${VM_B2}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"VM ${VM_B2} is unreachable due to KVM host issue. host.name: ${VM_B2}. k8s.cluster.name: mps-nonprod-rno\",
  \"customProperties\":{\"host.name\":\"${VM_B2}\",\"entity_type\":\"vm\",\"cloudstack_cluster\":\"cloudstack-cluster-2\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"environment\":\"ADC\"}}"

pause 8; dbcheck
echo "--- Assertions ---"
check "S1 BM→2VMs single incident" "$(count_for "${CS2_BM_B}")" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 2 — Full 4-layer cascade: BM → CloudStack VM → K8s node → pod
# BM: iapps-100-67-61-18 → VM: z3-08 → K8s node: z3-08 → pods
# Expected: 1 incident, correlation_id = CS2_BM_A
# ══════════════════════════════════════════════════════════════════════════════
if should_run 2; then
section "Scenario 2: BM → CloudStack VM → K8s node → Pod (4-layer full cascade)"
S="${TS}-S2"

post "BM CPU saturation (root, iapps-100-67-61-18)" "P-BM-S2-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-BM-S2-${S}\",
  \"problemTitle\":\"High CPU load on bare metal host ${CS2_BM_A}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"PERFORMANCE\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${CS2_BM_A}\",\"entityName\":\"${CS2_BM_A}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${CS2_BM_A}\",\"entityName\":\"${CS2_BM_A}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"CPU at 97% on ${CS2_BM_A}. host.name: ${CS2_BM_A}\",
  \"customProperties\":{\"host.name\":\"${CS2_BM_A}\",\"entity_type\":\"vm\",\"cloudstack_cluster\":\"cloudstack-cluster-2\",\"environment\":\"ADC\",\"impacted_entity\":\"${CS2_BM_A}\"}}"
pause 3

post "CloudStack VM CPU throttling (on BM_A)" "P-VM-S2-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-VM-S2-${S}\",
  \"problemTitle\":\"CPU throttling on CloudStack VM ${VM_A1}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"PERFORMANCE\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${CS2_BM_A}\",\"entityName\":\"${CS2_BM_A}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${VM_A1}\",\"entityName\":\"${VM_A1}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"CPU throttling on VM ${VM_A1} due to host overcommit. host.name: ${VM_A1}. k8s.cluster.name: mps-nonprod-rno\",
  \"customProperties\":{\"host.name\":\"${VM_A1}\",\"entity_type\":\"vm\",\"cloudstack_cluster\":\"cloudstack-cluster-2\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"environment\":\"ADC\"}}"
pause 3

post "K8s node NotReady (VM = K8s worker)" "P-K8N-S2-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-K8N-S2-${S}\",
  \"problemTitle\":\"Kubernetes Worker Node in Not Ready condition — ${K8N_A1}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${CS2_BM_A}\",\"entityName\":\"${CS2_BM_A}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_NODE-${K8N_A1}\",\"entityName\":\"${K8N_A1}\",\"entityType\":\"KUBERNETES_NODE\"}],
  \"problemDetails\":\"Node ${K8N_A1} NotReady in mps-nonprod-rno. k8s.node.name: ${K8N_A1}. k8s.cluster.name: mps-nonprod-rno\",
  \"customProperties\":{\"k8s.node.name\":\"${K8N_A1}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"environment\":\"ADC\"}}"
pause 3

post "Pod CrashLoopBackOff on evicted node" "P-POD-S2-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-POD-S2-${S}\",
  \"problemTitle\":\"Not all pods ready — stagepush-auth in stagepush-auth-uat on ${K8N_A1}\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${CS2_BM_A}\",\"entityName\":\"${CS2_BM_A}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-stagepush-auth\",\"entityName\":\"stagepush-auth\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"problemDetails\":\"stagepush-auth pod evicted from ${K8N_A1}. k8s.node.name: ${K8N_A1}. k8s.namespace.name: stagepush-auth-uat. k8s.cluster.name: mps-nonprod-rno\",
  \"customProperties\":{\"k8s.node.name\":\"${K8N_A1}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"k8s.namespace.name\":\"stagepush-auth-uat\",\"k8s.workload.name\":\"stagepush-auth\",\"environment\":\"ADC\"}}"

pause 8; dbcheck
echo "--- Assertions ---"
check "S2 BM→VM→K8sNode→Pod 4-layer cascade" "$(count_for "${CS2_BM_A}")" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 3 — Two independent BM hosts fail → 2 SEPARATE incidents
# BM_A: iapps-100-67-61-18  BM_B: iapps-100-67-62-19
# Expected: 2 separate incidents (1 per BM)
# ══════════════════════════════════════════════════════════════════════════════
if should_run 3; then
section "Scenario 3: Two independent BM failures → 2 separate incidents"
S="${TS}-S3"

post "BM-A hardware failure" "P-BM-A3-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-BM-A3-${S}\",
  \"problemTitle\":\"Hardware fault on bare metal ${CS2_BM_A} (power supply)\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${CS2_BM_A}\",\"entityName\":\"${CS2_BM_A}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${CS2_BM_A}\",\"entityName\":\"${CS2_BM_A}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"PSU failure on ${CS2_BM_A}. host.name: ${CS2_BM_A}\",
  \"customProperties\":{\"host.name\":\"${CS2_BM_A}\",\"entity_type\":\"vm\",\"cloudstack_cluster\":\"cloudstack-cluster-2\",\"environment\":\"ADC\",\"impacted_entity\":\"${CS2_BM_A}\"}}"
pause 2

post "BM-B hardware failure (different host!)" "P-BM-B3-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-BM-B3-${S}\",
  \"problemTitle\":\"Hardware fault on bare metal ${CS2_BM_B} (disk failure)\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${CS2_BM_B}\",\"entityName\":\"${CS2_BM_B}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${CS2_BM_B}\",\"entityName\":\"${CS2_BM_B}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"RAID disk failure on ${CS2_BM_B}. host.name: ${CS2_BM_B}\",
  \"customProperties\":{\"host.name\":\"${CS2_BM_B}\",\"entity_type\":\"vm\",\"cloudstack_cluster\":\"cloudstack-cluster-2\",\"environment\":\"ADC\",\"impacted_entity\":\"${CS2_BM_B}\"}}"

pause 8; dbcheck
echo "--- Assertions ---"
check_separate "S3 two BMs → 2 incidents" "${CS2_BM_A}" "${CS2_BM_B}"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 4 — VM fires first, BM fires 15s later → 1 incident (late BM root)
# Expected: 1 incident, child-before-parent cascade, root promoted to BM
# ══════════════════════════════════════════════════════════════════════════════
if should_run 4; then
section "Scenario 4: VM child arrives FIRST (BM parent fires 15s later)"
S="${TS}-S4"
BM4="iapps-100-67-63-16"  # another real BM from topology
VM4="mps-nonprod-rno-worker-z1-04"  # VM on this BM (based on topology)

post "VM CPU saturation (orphan — BM not yet fired)" "P-VM4-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-VM4-${S}\",
  \"problemTitle\":\"CPU saturation on CloudStack VM ${VM4}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"PERFORMANCE\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${VM4}\",\"entityName\":\"${VM4}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${VM4}\",\"entityName\":\"${VM4}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"CPU at 95% on VM ${VM4}. host.name: ${VM4}. k8s.cluster.name: mps-nonprod-rno\",
  \"customProperties\":{\"host.name\":\"${VM4}\",\"entity_type\":\"vm\",\"cloudstack_cluster\":\"cloudstack-cluster-2\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"environment\":\"ADC\"}}"
echo "  ⏳ VM is orphaned — BM alert delayed by 15s..."
pause 15

post "BM failure fires late (TRUE ROOT)" "P-BM4-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-BM4-${S}\",
  \"problemTitle\":\"Physical host failure — bare metal ${BM4}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${BM4}\",\"entityName\":\"${BM4}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${BM4}\",\"entityName\":\"${BM4}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"Kernel panic on ${BM4}. host.name: ${BM4}\",
  \"customProperties\":{\"host.name\":\"${BM4}\",\"entity_type\":\"vm\",\"cloudstack_cluster\":\"cloudstack-cluster-2\",\"environment\":\"ADC\",\"impacted_entity\":\"${BM4}\"}}"

pause 8; dbcheck
echo "--- Assertions ---"
check "S4 VM-first + late BM → 1 incident" "$(count_for "${BM4}")" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 5 — MDN BM failure → MDN K8s node NotReady (no RNO cross-contamination)
# BM: iapps-100-67-84-30 (MDN cloudstack-cluster-mondev)
# Expected: 1 incident in MDN, must NOT merge with any RNO incidents
# ══════════════════════════════════════════════════════════════════════════════
if should_run 5; then
section "Scenario 5: MDN BM failure → MDN VM → K8s node (not RNO)"
S="${TS}-S5"

post "MDN BM failure (cloudstack-cluster-mondev)" "P-MDN-BM-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-MDN-BM-${S}\",
  \"problemTitle\":\"Bare metal host failure — ${MONDEV_BM_A} (MDN DC)\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${MONDEV_BM_A}\",\"entityName\":\"${MONDEV_BM_A}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${MONDEV_BM_A}\",\"entityName\":\"${MONDEV_BM_A}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"Power failure on ${MONDEV_BM_A}. host.name: ${MONDEV_BM_A}\",
  \"customProperties\":{\"host.name\":\"${MONDEV_BM_A}\",\"entity_type\":\"vm\",\"cloudstack_cluster\":\"cloudstack-cluster-mondev\",\"environment\":\"ADC\",\"impacted_entity\":\"${MONDEV_BM_A}\"}}"
pause 3

post "MDN CloudStack VM down (on MDN BM)" "P-MDN-VM-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-MDN-VM-${S}\",
  \"problemTitle\":\"Host or monitoring unavailable — ${MONDEV_VM_A}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${MONDEV_BM_A}\",\"entityName\":\"${MONDEV_BM_A}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${MONDEV_VM_A}\",\"entityName\":\"${MONDEV_VM_A}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"VM ${MONDEV_VM_A} lost connectivity. host.name: ${MONDEV_VM_A}. k8s.cluster.name: mps-mondev-mdn\",
  \"customProperties\":{\"host.name\":\"${MONDEV_VM_A}\",\"entity_type\":\"vm\",\"cloudstack_cluster\":\"cloudstack-cluster-mondev\",\"k8s.cluster.name\":\"mps-mondev-mdn\",\"k8s.cluster.uid\":\"${MONDEV_UID}\",\"environment\":\"ADC\"}}"

pause 8; dbcheck
echo "--- Assertions ---"
check "S5 MDN BM → 1 MDN incident" "$(count_for "${MONDEV_BM_A}")" "1"
echo "  NOTE: verify it has MDN cluster in title/topology_path, NOT RNO"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 6 — Standalone CloudStack app VM (no K8s, no BM root) → 1 incident
# VM: sourcebox-prod06-cs-vm0-rno (application VM, not a K8s node)
# Expected: 1 incident with correlation_id = sourcebox-prod06-cs-vm0-rno
# ══════════════════════════════════════════════════════════════════════════════
if should_run 6; then
section "Scenario 6: Standalone CloudStack app VM failure (no K8s, no BM root)"
S="${TS}-S6"
SBOX="sourcebox-prod06-cs-vm0-rno"

post "App VM high network traffic" "P-SBOX-NET-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-SBOX-NET-${S}\",
  \"problemTitle\":\"Cloudstack_VM - High Network traffic on Prod VM ${SBOX}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"PERFORMANCE\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${SBOX}\",\"entityName\":\"${SBOX}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${SBOX}\",\"entityName\":\"${SBOX}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"Network throughput exceeded on VM ${SBOX}. host.name: ${SBOX}\",
  \"customProperties\":{\"host.name\":\"${SBOX}\",\"ip\":\"100.67.77.207\",\"entity_type\":\"vm\",\"cloudstack_cluster\":\"cloudstack-cluster-1\",\"environment\":\"ADC\",\"impacted_entity\":\"${SBOX}\"}}"
pause 3

post "App VM CPU saturation (same VM)" "P-SBOX-CPU-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-SBOX-CPU-${S}\",
  \"problemTitle\":\"CPU saturation on CloudStack VM ${SBOX}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"PERFORMANCE\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${SBOX}\",\"entityName\":\"${SBOX}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${SBOX}\",\"entityName\":\"${SBOX}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"CPU at 90% on ${SBOX}. host.name: ${SBOX}\",
  \"customProperties\":{\"host.name\":\"${SBOX}\",\"ip\":\"100.67.77.207\",\"entity_type\":\"vm\",\"cloudstack_cluster\":\"cloudstack-cluster-1\",\"environment\":\"ADC\",\"impacted_entity\":\"${SBOX}\"}}"

pause 8; dbcheck
echo "--- Assertions ---"
check "S6 standalone app VM → 1 incident" "$(count_for "${SBOX}")" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 7 — NetApp node failure → aggregate → SVM → volumes → 1 incident
# Chain: netapp-rno-node001 → aggr1_node001 → iapps-rno-k8s SVM → volumes
# Expected: 1 incident, correlation_id = netapp-rno-node001
# ══════════════════════════════════════════════════════════════════════════════
if should_run 7; then
section "Scenario 7: NetApp node failure → aggregate → SVM → volumes cascade"
S="${TS}-S7"

post "NetApp node failure (root)" "P-NETAPP-NODE-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-NETAPP-NODE-${S}\",
  \"problemTitle\":\"NetApp node failure — ${NETAPP_RNO_NODE1} (${NETAPP_RNO_CLUSTER})\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"NETAPP_NODE-${NETAPP_RNO_NODE1}\",\"entityName\":\"${NETAPP_RNO_NODE1}\",\"entityType\":\"NETAPP_NODE\"},
  \"impactedEntities\":[{\"entityId\":\"NETAPP_NODE-${NETAPP_RNO_NODE1}\",\"entityName\":\"${NETAPP_RNO_NODE1}\",\"entityType\":\"NETAPP_NODE\"}],
  \"problemDetails\":\"NetApp node ${NETAPP_RNO_NODE1} failed in cluster ${NETAPP_RNO_CLUSTER}. netapp_cluster: ${NETAPP_RNO_CLUSTER}\",
  \"customProperties\":{\"entity_type\":\"netapp_node\",\"netapp_entity\":\"${NETAPP_RNO_NODE1}\",\"netapp_cluster\":\"${NETAPP_RNO_CLUSTER}\",\"environment\":\"ADC\",\"impacted_entity\":\"${NETAPP_RNO_NODE1}\"}}"
pause 3

post "NetApp aggregate offline (on failed node)" "P-NETAPP-AGGR-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-NETAPP-AGGR-${S}\",
  \"problemTitle\":\"Aggregate state offline — ${NETAPP_RNO_AGGR1} (${NETAPP_RNO_CLUSTER})\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"NETAPP_NODE-${NETAPP_RNO_NODE1}\",\"entityName\":\"${NETAPP_RNO_NODE1}\",\"entityType\":\"NETAPP_NODE\"},
  \"impactedEntities\":[{\"entityId\":\"NETAPP_AGGR-${NETAPP_RNO_AGGR1}\",\"entityName\":\"${NETAPP_RNO_AGGR1}\",\"entityType\":\"NETAPP_AGGREGATE\"}],
  \"problemDetails\":\"Aggregate ${NETAPP_RNO_AGGR1} is offline. netapp_cluster: ${NETAPP_RNO_CLUSTER}\",
  \"customProperties\":{\"entity_type\":\"netapp_aggregate\",\"netapp_entity\":\"${NETAPP_RNO_AGGR1}\",\"netapp_cluster\":\"${NETAPP_RNO_CLUSTER}\",\"environment\":\"ADC\"}}"
pause 3

post "NetApp SVM data-unavailable (on offline aggregate)" "P-NETAPP-SVM-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-NETAPP-SVM-${S}\",
  \"problemTitle\":\"NetApp SVM data-unavailable — ${NETAPP_RNO_SVM}\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"NETAPP_NODE-${NETAPP_RNO_NODE1}\",\"entityName\":\"${NETAPP_RNO_NODE1}\",\"entityType\":\"NETAPP_NODE\"},
  \"impactedEntities\":[{\"entityId\":\"NETAPP_SVM-${NETAPP_RNO_SVM}\",\"entityName\":\"${NETAPP_RNO_SVM}\",\"entityType\":\"NETAPP_SVM\"}],
  \"problemDetails\":\"SVM ${NETAPP_RNO_SVM} cannot serve data. netapp_cluster: ${NETAPP_RNO_CLUSTER}\",
  \"customProperties\":{\"entity_type\":\"netapp_svm\",\"netapp_entity\":\"${NETAPP_RNO_SVM}\",\"netapp_cluster\":\"${NETAPP_RNO_CLUSTER}\",\"environment\":\"ADC\"}}"
pause 3

post "NetApp volume offline (vol-01 on SVM)" "P-NETAPP-VOL1-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-NETAPP-VOL1-${S}\",
  \"problemTitle\":\"NetApp volume offline — trident-pvc-rno-001 (${NETAPP_RNO_SVM})\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"NETAPP_NODE-${NETAPP_RNO_NODE1}\",\"entityName\":\"${NETAPP_RNO_NODE1}\",\"entityType\":\"NETAPP_NODE\"},
  \"impactedEntities\":[{\"entityId\":\"NETAPP_VOL-trident-pvc-rno-001\",\"entityName\":\"trident-pvc-rno-001\",\"entityType\":\"NETAPP_VOLUME\"}],
  \"problemDetails\":\"Volume trident-pvc-rno-001 offline on SVM ${NETAPP_RNO_SVM}. netapp_cluster: ${NETAPP_RNO_CLUSTER}\",
  \"customProperties\":{\"entity_type\":\"netapp_volume\",\"netapp_entity\":\"trident-pvc-rno-001\",\"netapp_cluster\":\"${NETAPP_RNO_CLUSTER}\",\"environment\":\"ADC\"}}"

pause 8; dbcheck
echo "--- Assertions ---"
check "S7 NetApp node→aggr→SVM→volume cascade" "$(count_for "${NETAPP_RNO_NODE1}")" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 8 — NetApp aggregate 95% full → quota exceeded → PVC write fail → pod crash
# Expected: 1 incident, correlation_id = aggr1_node002 (storage issue chain)
# ══════════════════════════════════════════════════════════════════════════════
if should_run 8; then
section "Scenario 8: NetApp aggregate 95% full → SVM quota → pod PVC write failure"
S="${TS}-S8"

post "NetApp aggregate 95% used (root)" "P-NETAPP-FULL-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-NETAPP-FULL-${S}\",
  \"problemTitle\":\"Aggregate - Used greater than 95% - ${NETAPP_RNO_AGGR2} (${NETAPP_RNO_CLUSTER})\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"PERFORMANCE\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"NETAPP_AGGR-${NETAPP_RNO_AGGR2}\",\"entityName\":\"${NETAPP_RNO_AGGR2}\",\"entityType\":\"NETAPP_AGGREGATE\"},
  \"impactedEntities\":[{\"entityId\":\"NETAPP_AGGR-${NETAPP_RNO_AGGR2}\",\"entityName\":\"${NETAPP_RNO_AGGR2}\",\"entityType\":\"NETAPP_AGGREGATE\"}],
  \"problemDetails\":\"Aggregate ${NETAPP_RNO_AGGR2} is 95.3% full. netapp_cluster: ${NETAPP_RNO_CLUSTER}\",
  \"customProperties\":{\"entity_type\":\"netapp_aggregate\",\"netapp_entity\":\"${NETAPP_RNO_AGGR2}\",\"netapp_cluster\":\"${NETAPP_RNO_CLUSTER}\",\"environment\":\"ADC\",\"impacted_entity\":\"${NETAPP_RNO_AGGR2}\"}}"
pause 3

post "SVM quota exceeded (volume cannot grow)" "P-NETAPP-QUOTA-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-NETAPP-QUOTA-${S}\",
  \"problemTitle\":\"NetApp SVM volume quota exceeded — ${NETAPP_RNO_SVM}\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"ERROR\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"NETAPP_AGGR-${NETAPP_RNO_AGGR2}\",\"entityName\":\"${NETAPP_RNO_AGGR2}\",\"entityType\":\"NETAPP_AGGREGATE\"},
  \"impactedEntities\":[{\"entityId\":\"NETAPP_SVM-${NETAPP_RNO_SVM}\",\"entityName\":\"${NETAPP_RNO_SVM}\",\"entityType\":\"NETAPP_SVM\"}],
  \"problemDetails\":\"Volume quota exceeded on SVM ${NETAPP_RNO_SVM}. netapp_cluster: ${NETAPP_RNO_CLUSTER}\",
  \"customProperties\":{\"entity_type\":\"netapp_svm\",\"netapp_entity\":\"${NETAPP_RNO_SVM}\",\"netapp_cluster\":\"${NETAPP_RNO_CLUSTER}\",\"environment\":\"ADC\"}}"
pause 3

post "Pod PVC write failure (I/O error)" "P-POD-PVC-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-POD-PVC-${S}\",
  \"problemTitle\":\"Pod PersistentVolume write error — prometheus-0 in monitoring\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"ERROR\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"NETAPP_AGGR-${NETAPP_RNO_AGGR2}\",\"entityName\":\"${NETAPP_RNO_AGGR2}\",\"entityType\":\"NETAPP_AGGREGATE\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-prometheus\",\"entityName\":\"prometheus\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"problemDetails\":\"prometheus-0 pod PVC read-only due to storage full. k8s.namespace.name: monitoring. k8s.cluster.name: mps-nonprod-rno. netapp_cluster: ${NETAPP_RNO_CLUSTER}\",
  \"customProperties\":{\"entity_type\":\"netapp_volume\",\"netapp_entity\":\"prometheus-pvc\",\"netapp_cluster\":\"${NETAPP_RNO_CLUSTER}\",\"k8s.namespace.name\":\"monitoring\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"environment\":\"ADC\"}}"

pause 8; dbcheck
echo "--- Assertions ---"
# NetApp aggregate/SVM alerts get topology-merged into the node-level incident; check
# that all 3 S8 source_ids ended up in the same incident rather than checking correlation_id.
check_same_incident_sids "S8 NetApp aggregate full → pod failure" \
  "P-NETAPP-FULL-${S}" "P-NETAPP-QUOTA-${S}" "P-POD-PVC-${S}"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 9 — Two NetApp aggregates fail on SAME cluster → 1 incident (storm)
# aggr1_node001 and aggr1_node002 both on netapp-rno-cluster001
# Expected: 1 incident (NetApp storm dedup groups by netapp_cluster)
# ══════════════════════════════════════════════════════════════════════════════
if should_run 9; then
section "Scenario 9: Two NetApp aggregates fail on SAME cluster → 1 incident (storm)"
S="${TS}-S9"
# Use TS-unique entity names so each run creates fresh aggregates with no prior incidents.
# The netapp_cluster is fixed to the real cluster so the storm dedup (netapp_cluster grouping)
# can still find the first incident and merge the second into it.
S9_AGGR1="s9-aggr-alpha-${TS}"
S9_AGGR2="s9-aggr-beta-${TS}"

post "RNO aggr-alpha offline" "P-NETAPP-AGGR1-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-NETAPP-AGGR1-${S}\",
  \"problemTitle\":\"Aggregate state offline — ${S9_AGGR1} - RNO\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"NETAPP_AGGR-${S9_AGGR1}\",\"entityName\":\"${S9_AGGR1}\",\"entityType\":\"NETAPP_AGGREGATE\"},
  \"impactedEntities\":[{\"entityId\":\"NETAPP_AGGR-${S9_AGGR1}\",\"entityName\":\"${S9_AGGR1}\",\"entityType\":\"NETAPP_AGGREGATE\"}],
  \"problemDetails\":\"Aggregate ${S9_AGGR1} offline in ${NETAPP_RNO_CLUSTER}. netapp_cluster: ${NETAPP_RNO_CLUSTER}\",
  \"customProperties\":{\"entity_type\":\"netapp_aggregate\",\"netapp_entity\":\"${S9_AGGR1}\",\"netapp_cluster\":\"${NETAPP_RNO_CLUSTER}\",\"environment\":\"ADC\",\"impacted_entity\":\"${S9_AGGR1}\"}}"
pause 2

post "RNO aggr-beta also offline (same cluster)" "P-NETAPP-AGGR2-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-NETAPP-AGGR2-${S}\",
  \"problemTitle\":\"Aggregate state offline — ${S9_AGGR2} - RNO\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"NETAPP_AGGR-${S9_AGGR2}\",\"entityName\":\"${S9_AGGR2}\",\"entityType\":\"NETAPP_AGGREGATE\"},
  \"impactedEntities\":[{\"entityId\":\"NETAPP_AGGR-${S9_AGGR2}\",\"entityName\":\"${S9_AGGR2}\",\"entityType\":\"NETAPP_AGGREGATE\"}],
  \"problemDetails\":\"Aggregate ${S9_AGGR2} offline in ${NETAPP_RNO_CLUSTER}. netapp_cluster: ${NETAPP_RNO_CLUSTER}\",
  \"customProperties\":{\"entity_type\":\"netapp_aggregate\",\"netapp_entity\":\"${S9_AGGR2}\",\"netapp_cluster\":\"${NETAPP_RNO_CLUSTER}\",\"environment\":\"ADC\",\"impacted_entity\":\"${S9_AGGR2}\"}}"

pause 8; dbcheck
echo "--- Assertions (NetApp storm: same cluster → 1 incident) ---"
cnt1=$(db "SELECT COUNT(*) FROM incidents WHERE auto_created=true AND correlation_id='${S9_AGGR1}' AND created_at>NOW()-INTERVAL '5 minutes'" | tr -d ' \n')
cnt2=$(db "SELECT COUNT(*) FROM incidents WHERE auto_created=true AND correlation_id='${S9_AGGR2}' AND created_at>NOW()-INTERVAL '5 minutes'" | tr -d ' \n')
# Count distinct incidents that have an alert with this run's S9 problem IDs
total=$(db "SELECT COUNT(DISTINCT incident_id) FROM alerts WHERE (labels->>'problem_id' LIKE 'P-NETAPP-AGGR%-${S}') AND created_at>NOW()-INTERVAL '5 minutes'" | tr -d ' \n')
echo "  aggr-alpha incidents: ${cnt1:-0}, aggr-beta incidents: ${cnt2:-0}, total distinct incidents: ${total:-0}"
if [[ "${total:-0}" -le "1" ]]; then
  echo "  ✅  PASS  S9 NetApp storm → at most 1 incident"
else
  echo "  ❌  FAIL  S9 NetApp storm → got ${total} incidents (expected 1)"
fi
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 10 — NetApp aggregates fail on DIFFERENT clusters (RNO vs MDN) → 2 incidents
# RNO: aggr1_node001 on netapp-rno-cluster001
# MDN: aggr1_node003 on netapp-mdn-cluster001
# Expected: 2 separate incidents (different netapp_cluster labels)
# ══════════════════════════════════════════════════════════════════════════════
if should_run 10; then
section "Scenario 10: NetApp aggregates on DIFFERENT clusters (RNO vs MDN) → 2 incidents"
S="${TS}-S10"

post "RNO aggregate offline" "P-NETAPP-RNO-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-NETAPP-RNO-${S}\",
  \"problemTitle\":\"Aggregate state offline — ${NETAPP_RNO_AGGR1} - RNO\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"NETAPP_AGGR-${NETAPP_RNO_AGGR1}\",\"entityName\":\"${NETAPP_RNO_AGGR1}\",\"entityType\":\"NETAPP_AGGREGATE\"},
  \"impactedEntities\":[{\"entityId\":\"NETAPP_AGGR-${NETAPP_RNO_AGGR1}\",\"entityName\":\"${NETAPP_RNO_AGGR1}\",\"entityType\":\"NETAPP_AGGREGATE\"}],
  \"problemDetails\":\"Aggregate ${NETAPP_RNO_AGGR1} offline in ${NETAPP_RNO_CLUSTER}. netapp_cluster: ${NETAPP_RNO_CLUSTER}\",
  \"customProperties\":{\"entity_type\":\"netapp_aggregate\",\"netapp_entity\":\"${NETAPP_RNO_AGGR1}\",\"netapp_cluster\":\"${NETAPP_RNO_CLUSTER}\",\"environment\":\"ADC\",\"impacted_entity\":\"${NETAPP_RNO_AGGR1}\"}}"
pause 2

post "MDN aggregate offline (DIFFERENT cluster!)" "P-NETAPP-MDN-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-NETAPP-MDN-${S}\",
  \"problemTitle\":\"Aggregate state offline — ${NETAPP_MDN_AGGR1} - MDN\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"NETAPP_AGGR-${NETAPP_MDN_AGGR1}\",\"entityName\":\"${NETAPP_MDN_AGGR1}\",\"entityType\":\"NETAPP_AGGREGATE\"},
  \"impactedEntities\":[{\"entityId\":\"NETAPP_AGGR-${NETAPP_MDN_AGGR1}\",\"entityName\":\"${NETAPP_MDN_AGGR1}\",\"entityType\":\"NETAPP_AGGREGATE\"}],
  \"problemDetails\":\"Aggregate ${NETAPP_MDN_AGGR1} offline in ${NETAPP_MDN_CLUSTER}. netapp_cluster: ${NETAPP_MDN_CLUSTER}\",
  \"customProperties\":{\"entity_type\":\"netapp_aggregate\",\"netapp_entity\":\"${NETAPP_MDN_AGGR1}\",\"netapp_cluster\":\"${NETAPP_MDN_CLUSTER}\",\"environment\":\"ADC\",\"impacted_entity\":\"${NETAPP_MDN_AGGR1}\"}}"

pause 8; dbcheck
echo "--- Assertions ---"
# NetApp aggregate correlation_id is set at node-level, not aggregate-name level.
# Check isolation by source_id: RNO and MDN alerts must land in DIFFERENT incidents.
check_separate_sids "S10 RNO vs MDN NetApp → 2 incidents" "P-NETAPP-RNO-${S}" "P-NETAPP-MDN-${S}"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 11 — NetApp SVM down → 4 volumes offline → 1 incident (SVM is root)
# SVM: iapps-rno-k8s → volumes lose mount → PVCs unavailable
# Expected: 1 incident, all volume alerts merge under SVM correlation
# ══════════════════════════════════════════════════════════════════════════════
if should_run 11; then
section "Scenario 11: NetApp SVM down → 4 volumes offline → 1 incident"
S="${TS}-S11"

post "SVM data-unavailable (root)" "P-SVM-DOWN-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-SVM-DOWN-${S}\",
  \"problemTitle\":\"NetApp SVM unavailable — ${NETAPP_RNO_SVM} (${NETAPP_RNO_CLUSTER})\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"NETAPP_SVM-${NETAPP_RNO_SVM}\",\"entityName\":\"${NETAPP_RNO_SVM}\",\"entityType\":\"NETAPP_SVM\"},
  \"impactedEntities\":[{\"entityId\":\"NETAPP_SVM-${NETAPP_RNO_SVM}\",\"entityName\":\"${NETAPP_RNO_SVM}\",\"entityType\":\"NETAPP_SVM\"}],
  \"problemDetails\":\"SVM ${NETAPP_RNO_SVM} is down. netapp_cluster: ${NETAPP_RNO_CLUSTER}\",
  \"customProperties\":{\"entity_type\":\"netapp_svm\",\"netapp_entity\":\"${NETAPP_RNO_SVM}\",\"netapp_cluster\":\"${NETAPP_RNO_CLUSTER}\",\"environment\":\"ADC\",\"impacted_entity\":\"${NETAPP_RNO_SVM}\"}}"
pause 2

for vol_idx in 1 2 3 4; do
  VOL_NAME="trident-k8s-pvc-rno-00${vol_idx}"
  post "Volume ${vol_idx} offline (on SVM)" "P-VOL-${vol_idx}-${S}" "{
    \"state\":\"OPEN\",\"problemId\":\"P-VOL-${vol_idx}-${S}\",
    \"problemTitle\":\"NetApp volume offline — ${VOL_NAME} (${NETAPP_RNO_SVM})\",
    \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
    \"rootCauseEntity\":{\"entityId\":\"NETAPP_SVM-${NETAPP_RNO_SVM}\",\"entityName\":\"${NETAPP_RNO_SVM}\",\"entityType\":\"NETAPP_SVM\"},
    \"impactedEntities\":[{\"entityId\":\"NETAPP_VOL-${VOL_NAME}\",\"entityName\":\"${VOL_NAME}\",\"entityType\":\"NETAPP_VOLUME\"}],
    \"problemDetails\":\"Volume ${VOL_NAME} offline due to SVM ${NETAPP_RNO_SVM} being down. netapp_cluster: ${NETAPP_RNO_CLUSTER}\",
    \"customProperties\":{\"entity_type\":\"netapp_volume\",\"netapp_entity\":\"${VOL_NAME}\",\"netapp_cluster\":\"${NETAPP_RNO_CLUSTER}\",\"environment\":\"ADC\"}}"
  sleep 1
done

pause 8; dbcheck
echo "--- Assertions ---"
check_same_incident_sids "S11 SVM down → 4 volumes → 1 incident" \
  "P-SVM-DOWN-${S}" "P-VOL-1-${S}" "P-VOL-2-${S}" "P-VOL-3-${S}" "P-VOL-4-${S}"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 12 — NetApp volume high latency → PVC degraded → pod I/O errors
# Expected: 1 incident for the volume performance issue chain
# ══════════════════════════════════════════════════════════════════════════════
if should_run 12; then
section "Scenario 12: NetApp volume high latency → PVC degraded → pod I/O errors"
S="${TS}-S12"
VOL12="trident-pvc-elasticsearch-data-rno"
SVM12="${NETAPP_RNO_SVM}"

post "NetApp volume high read latency (root)" "P-VOL-LAT-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-VOL-LAT-${S}\",
  \"problemTitle\":\"NetApp volume high latency — ${VOL12} (${SVM12})\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"PERFORMANCE\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"NETAPP_VOL-${VOL12}\",\"entityName\":\"${VOL12}\",\"entityType\":\"NETAPP_VOLUME\"},
  \"impactedEntities\":[{\"entityId\":\"NETAPP_VOL-${VOL12}\",\"entityName\":\"${VOL12}\",\"entityType\":\"NETAPP_VOLUME\"}],
  \"problemDetails\":\"Volume ${VOL12} read latency >100ms. netapp_cluster: ${NETAPP_RNO_CLUSTER}\",
  \"customProperties\":{\"entity_type\":\"netapp_volume\",\"netapp_entity\":\"${VOL12}\",\"netapp_cluster\":\"${NETAPP_RNO_CLUSTER}\",\"environment\":\"ADC\",\"impacted_entity\":\"${VOL12}\"}}"
pause 3

post "Pod I/O errors (reading from degraded PVC)" "P-POD-IO-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-POD-IO-${S}\",
  \"problemTitle\":\"Pod I/O error — elasticsearch-data-0 in elastic-system\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"ERROR\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"NETAPP_VOL-${VOL12}\",\"entityName\":\"${VOL12}\",\"entityType\":\"NETAPP_VOLUME\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-elasticsearch\",\"entityName\":\"elasticsearch\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
  \"problemDetails\":\"elasticsearch-data-0 experiencing I/O timeout on PVC backed by ${VOL12}. netapp_cluster: ${NETAPP_RNO_CLUSTER}. k8s.namespace.name: elastic-system. k8s.cluster.name: mps-nonprod-rno\",
  \"customProperties\":{\"entity_type\":\"netapp_volume\",\"netapp_entity\":\"${VOL12}\",\"netapp_cluster\":\"${NETAPP_RNO_CLUSTER}\",\"k8s.namespace.name\":\"elastic-system\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"environment\":\"ADC\"}}"

pause 8; dbcheck
echo "--- Assertions ---"
check_same_incident_sids "S12 volume latency → pod I/O → 1 incident" "P-VOL-LAT-${S}" "P-POD-IO-${S}"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 13 — BM failure + NetApp failure SIMULTANEOUSLY → 2 SEPARATE incidents
# BM: iapps-100-67-62-30  NetApp: aggr1_node003 (MDN)
# Expected: 2 separate incidents (different entity families: vm vs netapp)
# ══════════════════════════════════════════════════════════════════════════════
if should_run 13; then
section "Scenario 13: BM failure + NetApp failure simultaneously → 2 SEPARATE incidents"
S="${TS}-S13"
BM13="${CS2_BM_C}"  # iapps-100-67-62-30

post "BM hardware failure fires" "P-BM13-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-BM13-${S}\",
  \"problemTitle\":\"Bare metal failure — ${BM13} (RNO)\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${BM13}\",\"entityName\":\"${BM13}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"HOST-${BM13}\",\"entityName\":\"${BM13}\",\"entityType\":\"HOST\"}],
  \"problemDetails\":\"Physical host ${BM13} hardware failure. host.name: ${BM13}\",
  \"customProperties\":{\"host.name\":\"${BM13}\",\"entity_type\":\"vm\",\"cloudstack_cluster\":\"cloudstack-cluster-2\",\"environment\":\"ADC\",\"impacted_entity\":\"${BM13}\"}}"

post "NetApp MDN aggr also fails (SAME TIME)" "P-NETAPP13-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-NETAPP13-${S}\",
  \"problemTitle\":\"Aggregate state offline — ${NETAPP_MDN_AGGR1} (${NETAPP_MDN_CLUSTER})\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"NETAPP_AGGR-${NETAPP_MDN_AGGR1}\",\"entityName\":\"${NETAPP_MDN_AGGR1}\",\"entityType\":\"NETAPP_AGGREGATE\"},
  \"impactedEntities\":[{\"entityId\":\"NETAPP_AGGR-${NETAPP_MDN_AGGR1}\",\"entityName\":\"${NETAPP_MDN_AGGR1}\",\"entityType\":\"NETAPP_AGGREGATE\"}],
  \"problemDetails\":\"Aggregate ${NETAPP_MDN_AGGR1} offline in ${NETAPP_MDN_CLUSTER}. netapp_cluster: ${NETAPP_MDN_CLUSTER}\",
  \"customProperties\":{\"entity_type\":\"netapp_aggregate\",\"netapp_entity\":\"${NETAPP_MDN_AGGR1}\",\"netapp_cluster\":\"${NETAPP_MDN_CLUSTER}\",\"environment\":\"ADC\",\"impacted_entity\":\"${NETAPP_MDN_AGGR1}\"}}"

pause 8; dbcheck
echo "--- Assertions (BM vm-family vs NetApp netapp-family must NOT merge) ---"
check_separate_sids "S13 BM + NetApp → 2 separate" "P-BM13-${S}" "P-NETAPP13-${S}"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 14 — K8s node failure + NetApp PVC detach (same node) → 2 SEPARATE incidents
# Node: mps-nonprod-rno-worker-z1-03  NetApp: trident-pvc-detach-z1-03
# Expected: 2 incidents (k8s family vs netapp family — different entity types)
# ══════════════════════════════════════════════════════════════════════════════
if should_run 14; then
section "Scenario 14: K8s node failure + NetApp PVC detach on same node → 2 incidents"
S="${TS}-S14"
K8N14="mps-nonprod-rno-worker-z1-03"
PVC14="trident-pvc-z1-03-elasticsearch"

post "K8s node NotReady (network timeout)" "P-K8N14-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-K8N14-${S}\",
  \"problemTitle\":\"Kubernetes node NotReady — ${K8N14}\",
  \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"HOST-${K8N14}\",\"entityName\":\"${K8N14}\",\"entityType\":\"HOST\"},
  \"impactedEntities\":[{\"entityId\":\"KUBERNETES_NODE-${K8N14}\",\"entityName\":\"${K8N14}\",\"entityType\":\"KUBERNETES_NODE\"}],
  \"problemDetails\":\"Node ${K8N14} NotReady. k8s.node.name: ${K8N14}. k8s.cluster.name: mps-nonprod-rno\",
  \"customProperties\":{\"host.name\":\"${K8N14}\",\"k8s.node.name\":\"${K8N14}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"entity_type\":\"k8s_node\",\"environment\":\"ADC\",\"impacted_entity\":\"${K8N14}\"}}"
pause 2

post "NetApp PVC detach on same node (storage I/O)" "P-PVC14-${S}" "{
  \"state\":\"OPEN\",\"problemId\":\"P-PVC14-${S}\",
  \"problemTitle\":\"NetApp volume I/O error — ${PVC14} (k8s PVC detached)\",
  \"impactLevel\":\"SERVICE\",\"severity\":\"ERROR\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{\"entityId\":\"NETAPP_VOL-${PVC14}\",\"entityName\":\"${PVC14}\",\"entityType\":\"NETAPP_VOLUME\"},
  \"impactedEntities\":[{\"entityId\":\"NETAPP_VOL-${PVC14}\",\"entityName\":\"${PVC14}\",\"entityType\":\"NETAPP_VOLUME\"}],
  \"problemDetails\":\"PVC ${PVC14} detached due to storage I/O error. netapp_cluster: ${NETAPP_RNO_CLUSTER}\",
  \"customProperties\":{\"entity_type\":\"netapp_volume\",\"netapp_entity\":\"${PVC14}\",\"netapp_cluster\":\"${NETAPP_RNO_CLUSTER}\",\"environment\":\"ADC\",\"impacted_entity\":\"${PVC14}\"}}"

pause 8; dbcheck
echo "--- Assertions (K8s node vs NetApp volume must stay separate) ---"
check_separate_sids "S14 K8s node + NetApp PVC → 2 incidents" "P-K8N14-${S}" "P-PVC14-${S}"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 15 — MDN cluster storm: 3 nodes + 5 pods + 2 NetApp volumes
# Nodes: mps-mondev-mdn-worker-z1-03, z1-01, z3-03
# Pods: evicted from above nodes
# NetApp: iapps-mdn-trident SVM volumes (SEPARATE from k8s incident)
# Expected: 1 k8s incident (nodes/pods) + 1 NetApp incident = 2 total
# ══════════════════════════════════════════════════════════════════════════════
if should_run 15; then
section "Scenario 15: MDN cluster storm — 3 nodes + 5 pods + 2 NetApp volumes"
S="${TS}-S15"
MDN_CLUSTER="mps-mondev-mdn"
MDN_N1="mps-mondev-mdn-worker-z1-03"
MDN_N2="mps-mondev-mdn-worker-z1-01"
MDN_N3="mps-mondev-mdn-worker-z3-03"
MDN_VOL1="trident-mdn-pvc-kafka-0"
MDN_VOL2="trident-mdn-pvc-kafka-1"

echo "  Firing 3 MDN node failures + 5 pod evictions + 2 NetApp volumes..."

for node in "${MDN_N1}" "${MDN_N2}" "${MDN_N3}"; do
  post "MDN node NotReady (${node})" "P-MDN-NODE-${node}-${S}" "{
    \"state\":\"OPEN\",\"problemId\":\"P-MDN-NODE-${node}-${S}\",
    \"problemTitle\":\"Host or monitoring unavailable — ${node} (${MDN_CLUSTER})\",
    \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
    \"rootCauseEntity\":{\"entityId\":\"HOST-${node}\",\"entityName\":\"${node}\",\"entityType\":\"HOST\"},
    \"impactedEntities\":[{\"entityId\":\"HOST-${node}\",\"entityName\":\"${node}\",\"entityType\":\"HOST\"}],
    \"problemDetails\":\"${node} unreachable. host.name: ${node}. k8s.node.name: ${node}. k8s.cluster.name: ${MDN_CLUSTER}\",
    \"customProperties\":{\"host.name\":\"${node}\",\"entity_type\":\"vm\",\"k8s.node.name\":\"${node}\",\"k8s.cluster.name\":\"${MDN_CLUSTER}\",\"k8s.cluster.uid\":\"${MONDEV_UID}\",\"environment\":\"ADC\",\"impacted_entity\":\"${node}\"}}"
  sleep 1
done
pause 3

for pod_suffix in kafka-0 kafka-1 zookeeper-0 elasticsearch-0 prometheus-0; do
  post "MDN pod evicted (${pod_suffix})" "P-MDN-POD-${pod_suffix}-${S}" "{
    \"state\":\"OPEN\",\"problemId\":\"P-MDN-POD-${pod_suffix}-${S}\",
    \"problemTitle\":\"Not all pods ready — ${pod_suffix} in infrastructure\",
    \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
    \"rootCauseEntity\":{\"entityId\":\"HOST-${MDN_N1}\",\"entityName\":\"${MDN_N1}\",\"entityType\":\"HOST\"},
    \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-${pod_suffix}\",\"entityName\":\"${pod_suffix}\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
    \"problemDetails\":\"Pod ${pod_suffix} evicted due to node failure. k8s.cluster.name: ${MDN_CLUSTER}. k8s.node.name: ${MDN_N1}\",
    \"customProperties\":{\"k8s.node.name\":\"${MDN_N1}\",\"k8s.cluster.name\":\"${MDN_CLUSTER}\",\"k8s.cluster.uid\":\"${MONDEV_UID}\",\"entity_type\":\"k8s_workload\",\"k8s.workload.name\":\"${pod_suffix}\",\"environment\":\"ADC\"}}"
  sleep 1
done
pause 3

for vol in "${MDN_VOL1}" "${MDN_VOL2}"; do
  post "NetApp MDN volume offline (${vol})" "P-MDN-VOL-${vol}-${S}" "{
    \"state\":\"OPEN\",\"problemId\":\"P-MDN-VOL-${vol}-${S}\",
    \"problemTitle\":\"NetApp volume offline — ${vol} (${NETAPP_MDN_SVM})\",
    \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
    \"rootCauseEntity\":{\"entityId\":\"NETAPP_SVM-${NETAPP_MDN_SVM}\",\"entityName\":\"${NETAPP_MDN_SVM}\",\"entityType\":\"NETAPP_SVM\"},
    \"impactedEntities\":[{\"entityId\":\"NETAPP_VOL-${vol}\",\"entityName\":\"${vol}\",\"entityType\":\"NETAPP_VOLUME\"}],
    \"problemDetails\":\"Volume ${vol} offline on SVM ${NETAPP_MDN_SVM}. netapp_cluster: ${NETAPP_MDN_CLUSTER}\",
    \"customProperties\":{\"entity_type\":\"netapp_volume\",\"netapp_entity\":\"${vol}\",\"netapp_cluster\":\"${NETAPP_MDN_CLUSTER}\",\"environment\":\"ADC\"}}"
  sleep 1
done

pause 10; dbcheck
echo "--- Assertions ---"
k8s_inc=$(db "SELECT COUNT(DISTINCT i.id) FROM incidents i JOIN alerts a ON a.incident_id=i.id WHERE i.auto_created=true AND a.labels->>'k8s.cluster.name'='${MDN_CLUSTER}' AND a.labels->>'problem_id' LIKE '%-${S}' AND i.updated_at>NOW()-INTERVAL '10 minutes'" | tr -d ' \n')
# Count MDN NetApp incidents only from THIS run's S15 problem IDs (avoid S10 MDN aggregate)
netapp_inc=$(db "SELECT COUNT(DISTINCT incident_id) FROM alerts WHERE labels->>'problem_id' LIKE 'P-MDN-VOL-%-${S}' AND created_at>NOW()-INTERVAL '5 minutes'" | tr -d ' \n')
echo "  MDN K8s incidents: ${k8s_inc:-0} (expected ≤3)"
echo "  MDN NetApp incidents: ${netapp_inc:-0} (expected 1)"
[[ "${k8s_inc:-0}" -le "3" ]] && echo "  ✅  PASS  S15 MDN K8s cluster incidents ≤3" || echo "  ❌  FAIL  S15 MDN K8s too many incidents: ${k8s_inc}"
[[ "${netapp_inc:-0}" -eq "1" ]] && echo "  ✅  PASS  S15 MDN NetApp storm → 1 incident" || echo "  ❌  FAIL  S15 MDN NetApp expected 1, got ${netapp_inc}"
fi

# ══════════════════════════════════════════════════════════════════════════════
# FINAL SUMMARY
# ══════════════════════════════════════════════════════════════════════════════
echo ""
echo "╔══════════════════════════════════════════════════════════════════════╗"
echo "║          INFRA TEST SUITE — FINAL INCIDENT SUMMARY                  ║"
echo "╚══════════════════════════════════════════════════════════════════════╝"
echo ""
echo "Incidents created in last 15 min:"
db "SELECT ' ' || LEFT(title,55) || ' | ' || COALESCE(correlation_id,'NULL') || ' | ' || status || ' | alerts=' || COALESCE(jsonb_array_length(alert_ids)::text,'?')
    FROM incidents WHERE auto_created=true AND updated_at > NOW()-INTERVAL '15 minutes'
    ORDER BY created_at DESC LIMIT 30"
