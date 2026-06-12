#!/usr/bin/env bash
# sim_mdn_alert_storm.sh — MDN alert storm simulation using LIVE topology
#
# Mirrors the real RNO BM alert storm (iapps-100-67-62-32 → k8dev01 cascade)
# but targets a different host in MDN so the correlation engine handles it fresh.
#
# Real topology sourced from Neo4j (kubectl exec neo4j-0):
#   BM  : cloudstack-host-CloudStack-MDN-iapps-100-67-86-31
#   VM  : cloudstack-vm-c7ab24f9-711a-4deb-9fed-e3c09b871a8f  (mps-mondev-mdn-worker-01, 100.67.76.80)
#   Node: k8s-node-mps-mondev-mdn-mps-mondev-mdn-worker-01    (cluster: mps-mondev-mdn)
#   Pods: variantgen-quip-api / alerthub-cosign / a2a-server / jenkins-alertmanager / milvus-proxy / argus-ui
#
# Scenarios:
#   1  BM → VM → node → 6 pods cascade  (expect 1 incident, 9 alerts)
#   2  Second independent BM failure     (expect 2nd separate incident)
#   3  RESOLVED lifecycle on storm 1     (expect incident to auto-close)
#
# Usage:
#   bash sim_mdn_alert_storm.sh              # all scenarios
#   bash sim_mdn_alert_storm.sh 1            # single scenario
#   API_KEY=<key> bash sim_mdn_alert_storm.sh

set -uo pipefail

ENDPOINT="https://aileron.example.com/api/v1/webhooks/dynatrace"
API_KEY="${API_KEY:-ah_c76ddeaa86c74818ac84997c0f2a8174eaba3bf0fdab295132acb8aba2238e5f}"
NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
TS=$(date +%s)

# ── Real topology constants (from live Neo4j graph) ───────────────────────────

# Primary BM root (MDN CloudStack host)
BM1_NAME="iapps-100-67-86-31"
BM1_FQDN="${BM1_NAME}.example.com"
BM1_ENTITY_ID="cloudstack-host-CloudStack-MDN-${BM1_NAME}"
BM1_DT_ID="HOST-${BM1_ENTITY_ID}"

# VM on BM1 (Dynatrace sees KVM VMs as HOST entities)
VM1_NAME="mps-mondev-mdn-worker-01"
VM1_UUID="c7ab24f9-711a-4deb-9fed-e3c09b871a8f"
VM1_IP="100.67.76.80"
VM1_ENTITY_ID="cloudstack-vm-${VM1_UUID}"
VM1_DT_ID="HOST-${VM1_ENTITY_ID}"

# K8s node (same hostname as VM in CloudStack-provisioned clusters)
K8N1_NAME="mps-mondev-mdn-worker-01"
K8N1_ENTITY_ID="k8s-node-mps-mondev-mdn-${K8N1_NAME}"
K8N1_DT_ID="KUBERNETES_NODE-${K8N1_ENTITY_ID}"
CLUSTER1="mps-mondev-mdn"
CLUSTER1_UID="00a07750-e556-443e-89d9-80341edb472d"

# Second independent BM (for isolation test)
BM2_NAME="iapps-100-67-86-133"
BM2_ENTITY_ID="cloudstack-host-CloudStack-MDN-${BM2_NAME}"
BM2_DT_ID="HOST-${BM2_ENTITY_ID}"
VM2_NAME="mps-mondev-mdn-worker-02"
VM2_UUID="cdb3048c-ceb0-4fcd-804a-b5588a88d6b7"
VM2_ENTITY_ID="cloudstack-vm-${VM2_UUID}"
VM2_DT_ID="HOST-${VM2_ENTITY_ID}"
K8N2_NAME="mps-mondev-mdn-worker-02"
K8N2_ENTITY_ID="k8s-node-mps-mondev-mdn-${K8N2_NAME}"
K8N2_DT_ID="KUBERNETES_NODE-${K8N2_ENTITY_ID}"

# ── Helpers ───────────────────────────────────────────────────────────────────

post() {
  local label="$1" id="$2" payload="$3"
  printf "  → %-52s " "[$label] ($id)..."
  local code
  code=$(curl -sk -o /dev/null -w "%{http_code}" -X POST "$ENDPOINT" \
    -H "Content-Type: application/json" \
    -H "X-API-Key: ${API_KEY}" \
    -d "$payload")
  echo "HTTP $code"
}

section() {
  echo ""
  echo "══════════════════════════════════════════════════════"
  echo "  $1"
  echo "══════════════════════════════════════════════════════"
}

pause() { echo "  waiting ${1}s..."; sleep "$1"; }

result() {
  echo ""
  echo "--- Pipeline decisions (last 4 min) ---"
  kubectl logs -n aileron -l app=alerthub-backend --since=4m 2>/dev/null \
    | grep -E "RCE alert=|CREATE_ROOT|ATTACH_TO_ROOT|action=ATTACH|Created incident|correlation_id.*set|auto-close|RESOLVED" \
    | grep -v "^$" | sort | uniq | tail -25
}

dbcheck() {
  echo ""
  echo "--- DB: incidents created in last 10 min ---"
  kubectl exec -n aileron postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT '  [' || status || '] ' || id || '  alerts=' || jsonb_array_length(alert_ids) ||
           '  corr=' || COALESCE(correlation_id,'NULL') || '  title=' || LEFT(title,55)
    FROM incidents
    WHERE auto_created=true AND updated_at > NOW()-INTERVAL '10 minutes'
    ORDER BY created_at DESC;" 2>/dev/null | grep -v "^$"

  echo ""
  echo "--- DB: alerts correlated in last 10 min ---"
  kubectl exec -n aileron postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT '  ' || LEFT(title,50) || '  corr=' || COALESCE(correlation_id,'NULL') || '  src_id=' || COALESCE(source_id,'')
    FROM alerts
    WHERE source='dynatrace' AND created_at > NOW()-INTERVAL '10 minutes'
    ORDER BY created_at;" 2>/dev/null | grep -v "^$"
}

count_incidents_for() {
  kubectl exec -n aileron postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT COUNT(*) FROM incidents
    WHERE auto_created=true AND status IN ('open','investigating')
      AND correlation_id='${1}'
      AND updated_at > NOW()-INTERVAL '10 minutes';" 2>/dev/null | tr -d ' \n'
}

check_pass() {
  local label="$1" got="$2" expected="$3"
  if [[ "$got" == "$expected" ]]; then
    echo "  PASS  ${label}: ${got}"
  else
    echo "  FAIL  ${label}: expected=${expected} got=${got}"
  fi
}

RUN_ALL=true
SELECTED=()
if [[ $# -gt 0 ]]; then
  RUN_ALL=false
  SELECTED=("$@")
fi
should_run() { $RUN_ALL && return 0; for s in "${SELECTED[@]}"; do [[ "$s" == "$1" ]] && return 0; done; return 1; }

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 1 — MDN BM → VM → K8s node → 6 pods cascade
#
# Root:       iapps-100-67-86-31 (MDN KVM bare-metal, CloudStack-MDN)
# VM:         mps-mondev-mdn-worker-01 (100.67.76.80)
# K8s node:   mps-mondev-mdn-worker-01 (cluster: mps-mondev-mdn)
# Pods:       variantgen-quip-api, alerthub-cosign, a2a-server,
#             jenkins-alertmanager, milvus-proxy, argus-ui
#
# Expected:   1 incident, alert_count=9, correlation_id=iapps-100-67-86-31
# ══════════════════════════════════════════════════════════════════════════════
if should_run 1; then
section "Scenario 1: MDN BM ${BM1_NAME} → VM → node → 6 pods"
S1="S1-${TS}"

# 1a. BM — system memory high (mirrors real iapps-100-67-62-32 alert format exactly)
post "BM memory saturation [root]" "P-SIM1-BM-${S1}" "{
  \"state\":\"OPEN\",
  \"problemId\":\"P-SIM1-BM-${S1}\",
  \"problemTitle\":\"Cloudstack_BareMetal - System Memory Utilization is High\",
  \"impactLevel\":\"INFRASTRUCTURE\",
  \"severity\":\"PERFORMANCE\",
  \"status\":\"OPEN\",
  \"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{
    \"entityId\":\"${BM1_DT_ID}\",
    \"entityName\":\"${BM1_NAME}\",
    \"entityType\":\"HOST\"
  },
  \"impactedEntities\":[{
    \"entityId\":\"${BM1_DT_ID}\",
    \"entityName\":\"${BM1_NAME}\",
    \"entityType\":\"HOST\"
  }],
  \"affectedEntities\":[{
    \"entityId\":\"${BM1_DT_ID}\",
    \"entityName\":\"${BM1_NAME}\",
    \"entityType\":\"HOST\"
  }],
  \"problemDetails\":\"OPEN Problem P-SIM1-BM-${S1}\nHost\n${BM1_FQDN}\n\nCloudstack_BareMetal - System Memory Utilization is High\nMemory utilization at 94% on KVM host ${BM1_FQDN}\nhost.name: ${BM1_FQDN}\n\",
  \"Tags\":\"[Environment]bm:true, ProcessType:libvertd, [Environment]dc:mdn, [Environment]kvm:true, ProcessType:csagent, [Environment]env:prod\",
  \"customProperties\":{
    \"host.name\":\"${BM1_FQDN}\",
    \"environment\":\"ADC\",
    \"impacted_entity\":\"${BM1_NAME}\"
  }
}"
pause 4

# 1b. VM — host monitoring unavailable (mirrors real P-26055902 format)
post "VM host unavailable [downstream]" "P-SIM1-VM-${S1}" "{
  \"state\":\"OPEN\",
  \"problemId\":\"P-SIM1-VM-${S1}\",
  \"problemTitle\":\"Host or monitoring unavailable\",
  \"impactLevel\":\"INFRASTRUCTURE\",
  \"severity\":\"AVAILABILITY\",
  \"status\":\"OPEN\",
  \"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{
    \"entityId\":\"${BM1_DT_ID}\",
    \"entityName\":\"${BM1_NAME}\",
    \"entityType\":\"HOST\"
  },
  \"impactedEntities\":[{
    \"entityId\":\"${VM1_DT_ID}\",
    \"entityName\":\"${VM1_NAME} : ${VM1_IP}\",
    \"entityType\":\"HOST\"
  }],
  \"affectedEntities\":[{
    \"entityId\":\"${VM1_DT_ID}\",
    \"entityName\":\"${VM1_NAME} : ${VM1_IP}\",
    \"entityType\":\"HOST\"
  }],
  \"problemDetails\":\"OPEN Problem P-SIM1-VM-${S1}\nHost\n${VM1_NAME} : ${VM1_IP}\n\nHost or monitoring unavailable\nHost or monitoring unavailable due to connectivity issues or server outage\nhost.name: ${VM1_NAME}\nk8s.cluster.name: ${CLUSTER1}\nk8s.cluster.uid: ${CLUSTER1_UID}\n\",
  \"Tags\":\"[Environment]env:prod, HostType:tier2-dev, [Environment]dc:mdn\",
  \"customProperties\":{
    \"host.name\":\"${VM1_NAME}\",
    \"environment\":\"ADC\",
    \"k8s.cluster.name\":\"${CLUSTER1}\",
    \"k8s.cluster.uid\":\"${CLUSTER1_UID}\"
  }
}"
pause 4

# 1c. K8s node — NotReady
post "K8s node NotReady [downstream]" "P-SIM1-NODE-${S1}" "{
  \"state\":\"OPEN\",
  \"problemId\":\"P-SIM1-NODE-${S1}\",
  \"problemTitle\":\"Kubernetes node ${K8N1_NAME} is NotReady\",
  \"impactLevel\":\"INFRASTRUCTURE\",
  \"severity\":\"AVAILABILITY\",
  \"status\":\"OPEN\",
  \"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{
    \"entityId\":\"${BM1_DT_ID}\",
    \"entityName\":\"${BM1_NAME}\",
    \"entityType\":\"HOST\"
  },
  \"impactedEntities\":[{
    \"entityId\":\"${K8N1_DT_ID}\",
    \"entityName\":\"${K8N1_NAME}\",
    \"entityType\":\"KUBERNETES_NODE\"
  }],
  \"affectedEntities\":[{
    \"entityId\":\"${K8N1_DT_ID}\",
    \"entityName\":\"${K8N1_NAME}\",
    \"entityType\":\"KUBERNETES_NODE\"
  }],
  \"problemDetails\":\"OPEN Problem P-SIM1-NODE-${S1}\nKubernetes node\n${K8N1_NAME}\n\nKubernetes node is not ready\nNode ${K8N1_NAME} transitioned to NotReady state.\nk8s.node.name: ${K8N1_NAME}\nk8s.cluster.name: ${CLUSTER1}\nk8s.cluster.uid: ${CLUSTER1_UID}\n\",
  \"customProperties\":{
    \"k8s.node.name\":\"${K8N1_NAME}\",
    \"k8s.cluster.name\":\"${CLUSTER1}\",
    \"k8s.cluster.uid\":\"${CLUSTER1_UID}\",
    \"environment\":\"ADC\"
  }
}"
pause 3

# 1d–1i. 6 pod failures across 4 namespaces (real pods from Neo4j topology)

post "Pod: variantgen-quip-api [variantgen]" "P-SIM1-POD1-${S1}" "{
  \"state\":\"OPEN\",
  \"problemId\":\"P-SIM1-POD1-${S1}\",
  \"problemTitle\":\"Not all pods ready — variantgen-quip-api in variantgen\",
  \"impactLevel\":\"SERVICE\",
  \"severity\":\"AVAILABILITY\",
  \"status\":\"OPEN\",
  \"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{
    \"entityId\":\"${BM1_DT_ID}\",
    \"entityName\":\"${BM1_NAME}\",
    \"entityType\":\"HOST\"
  },
  \"impactedEntities\":[{
    \"entityId\":\"KUBERNETES_WORKLOAD-variantgen-quip-api\",
    \"entityName\":\"variantgen-quip-api\",
    \"entityType\":\"KUBERNETES_WORKLOAD\"
  }],
  \"problemDetails\":\"OPEN Problem P-SIM1-POD1-${S1}\nKubernetes workload\nvariantgen-quip-api\n\nNot all pods ready\nDeployment variantgen-quip-api has 0/1 ready pods.\nk8s.workload.name: variantgen-quip-api\nk8s.workload.kind: Deployment\nk8s.namespace.name: variantgen\nk8s.node.name: ${K8N1_NAME}\nk8s.cluster.name: ${CLUSTER1}\nk8s.cluster.uid: ${CLUSTER1_UID}\n\",
  \"customProperties\":{
    \"k8s.workload.name\":\"variantgen-quip-api\",
    \"k8s.workload.kind\":\"Deployment\",
    \"k8s.namespace.name\":\"variantgen\",
    \"k8s.node.name\":\"${K8N1_NAME}\",
    \"k8s.cluster.name\":\"${CLUSTER1}\",
    \"k8s.cluster.uid\":\"${CLUSTER1_UID}\",
    \"environment\":\"ADC\"
  }
}"
pause 2

post "Pod: alerthub-cosign [monitoring-dev]" "P-SIM1-POD2-${S1}" "{
  \"state\":\"OPEN\",
  \"problemId\":\"P-SIM1-POD2-${S1}\",
  \"problemTitle\":\"Not all pods ready — alerthub-cosign in monitoring-dev\",
  \"impactLevel\":\"SERVICE\",
  \"severity\":\"AVAILABILITY\",
  \"status\":\"OPEN\",
  \"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{
    \"entityId\":\"${BM1_DT_ID}\",
    \"entityName\":\"${BM1_NAME}\",
    \"entityType\":\"HOST\"
  },
  \"impactedEntities\":[{
    \"entityId\":\"KUBERNETES_WORKLOAD-alerthub-cosign\",
    \"entityName\":\"alerthub-cosign\",
    \"entityType\":\"KUBERNETES_WORKLOAD\"
  }],
  \"problemDetails\":\"OPEN Problem P-SIM1-POD2-${S1}\nKubernetes workload\nalerthub-cosign\n\nNot all pods ready\nDeployment alerthub-cosign has 0/1 ready pods.\nk8s.workload.name: alerthub-cosign\nk8s.workload.kind: Deployment\nk8s.namespace.name: monitoring-dev\nk8s.node.name: ${K8N1_NAME}\nk8s.cluster.name: ${CLUSTER1}\nk8s.cluster.uid: ${CLUSTER1_UID}\n\",
  \"customProperties\":{
    \"k8s.workload.name\":\"alerthub-cosign\",
    \"k8s.workload.kind\":\"Deployment\",
    \"k8s.namespace.name\":\"monitoring-dev\",
    \"k8s.node.name\":\"${K8N1_NAME}\",
    \"k8s.cluster.name\":\"${CLUSTER1}\",
    \"k8s.cluster.uid\":\"${CLUSTER1_UID}\",
    \"environment\":\"ADC\"
  }
}"
pause 2

post "Pod: a2a-server [monitoring-dev]" "P-SIM1-POD3-${S1}" "{
  \"state\":\"OPEN\",
  \"problemId\":\"P-SIM1-POD3-${S1}\",
  \"problemTitle\":\"Not all pods ready — a2a-server in monitoring-dev\",
  \"impactLevel\":\"SERVICE\",
  \"severity\":\"AVAILABILITY\",
  \"status\":\"OPEN\",
  \"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{
    \"entityId\":\"${BM1_DT_ID}\",
    \"entityName\":\"${BM1_NAME}\",
    \"entityType\":\"HOST\"
  },
  \"impactedEntities\":[{
    \"entityId\":\"KUBERNETES_WORKLOAD-a2a-server\",
    \"entityName\":\"a2a-server\",
    \"entityType\":\"KUBERNETES_WORKLOAD\"
  }],
  \"problemDetails\":\"OPEN Problem P-SIM1-POD3-${S1}\nKubernetes workload\na2a-server\n\nNot all pods ready\nDeployment a2a-server has 0/2 ready pods.\nk8s.workload.name: a2a-server\nk8s.workload.kind: Deployment\nk8s.namespace.name: monitoring-dev\nk8s.node.name: ${K8N1_NAME}\nk8s.cluster.name: ${CLUSTER1}\nk8s.cluster.uid: ${CLUSTER1_UID}\n\",
  \"customProperties\":{
    \"k8s.workload.name\":\"a2a-server\",
    \"k8s.workload.kind\":\"Deployment\",
    \"k8s.namespace.name\":\"monitoring-dev\",
    \"k8s.node.name\":\"${K8N1_NAME}\",
    \"k8s.cluster.name\":\"${CLUSTER1}\",
    \"k8s.cluster.uid\":\"${CLUSTER1_UID}\",
    \"environment\":\"ADC\"
  }
}"
pause 2

post "Pod: jenkins-alertmanager [monitoring-dev]" "P-SIM1-POD4-${S1}" "{
  \"state\":\"OPEN\",
  \"problemId\":\"P-SIM1-POD4-${S1}\",
  \"problemTitle\":\"Not all pods ready — jenkins-alertmanager in monitoring-dev\",
  \"impactLevel\":\"SERVICE\",
  \"severity\":\"AVAILABILITY\",
  \"status\":\"OPEN\",
  \"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{
    \"entityId\":\"${BM1_DT_ID}\",
    \"entityName\":\"${BM1_NAME}\",
    \"entityType\":\"HOST\"
  },
  \"impactedEntities\":[{
    \"entityId\":\"KUBERNETES_WORKLOAD-jenkins-alertmanager\",
    \"entityName\":\"jenkins-alertmanager\",
    \"entityType\":\"KUBERNETES_WORKLOAD\"
  }],
  \"problemDetails\":\"OPEN Problem P-SIM1-POD4-${S1}\nKubernetes workload\njenkins-alertmanager\n\nNot all pods ready\nDeployment jenkins-alertmanager has 0/1 ready pods.\nk8s.workload.name: jenkins-alertmanager\nk8s.workload.kind: Deployment\nk8s.namespace.name: monitoring-dev\nk8s.node.name: ${K8N1_NAME}\nk8s.cluster.name: ${CLUSTER1}\nk8s.cluster.uid: ${CLUSTER1_UID}\n\",
  \"customProperties\":{
    \"k8s.workload.name\":\"jenkins-alertmanager\",
    \"k8s.workload.kind\":\"Deployment\",
    \"k8s.namespace.name\":\"monitoring-dev\",
    \"k8s.node.name\":\"${K8N1_NAME}\",
    \"k8s.cluster.name\":\"${CLUSTER1}\",
    \"k8s.cluster.uid\":\"${CLUSTER1_UID}\",
    \"environment\":\"ADC\"
  }
}"
pause 2

post "Pod: milvus-proxy [milvus]" "P-SIM1-POD5-${S1}" "{
  \"state\":\"OPEN\",
  \"problemId\":\"P-SIM1-POD5-${S1}\",
  \"problemTitle\":\"Not all pods ready — milvus-proxy in milvus\",
  \"impactLevel\":\"SERVICE\",
  \"severity\":\"AVAILABILITY\",
  \"status\":\"OPEN\",
  \"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{
    \"entityId\":\"${BM1_DT_ID}\",
    \"entityName\":\"${BM1_NAME}\",
    \"entityType\":\"HOST\"
  },
  \"impactedEntities\":[{
    \"entityId\":\"KUBERNETES_WORKLOAD-milvus-proxy\",
    \"entityName\":\"milvus-proxy\",
    \"entityType\":\"KUBERNETES_WORKLOAD\"
  }],
  \"problemDetails\":\"OPEN Problem P-SIM1-POD5-${S1}\nKubernetes workload\nmilvus-proxy\n\nNot all pods ready\nDeployment milvus-proxy has 0/3 ready pods.\nk8s.workload.name: milvus-proxy\nk8s.workload.kind: Deployment\nk8s.namespace.name: milvus\nk8s.node.name: ${K8N1_NAME}\nk8s.cluster.name: ${CLUSTER1}\nk8s.cluster.uid: ${CLUSTER1_UID}\n\",
  \"customProperties\":{
    \"k8s.workload.name\":\"milvus-proxy\",
    \"k8s.workload.kind\":\"Deployment\",
    \"k8s.namespace.name\":\"milvus\",
    \"k8s.node.name\":\"${K8N1_NAME}\",
    \"k8s.cluster.name\":\"${CLUSTER1}\",
    \"k8s.cluster.uid\":\"${CLUSTER1_UID}\",
    \"environment\":\"ADC\"
  }
}"
pause 2

post "Pod: argus-ui [monitoring-dev]" "P-SIM1-POD6-${S1}" "{
  \"state\":\"OPEN\",
  \"problemId\":\"P-SIM1-POD6-${S1}\",
  \"problemTitle\":\"Not all pods ready — argus-ui in monitoring-dev\",
  \"impactLevel\":\"SERVICE\",
  \"severity\":\"AVAILABILITY\",
  \"status\":\"OPEN\",
  \"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{
    \"entityId\":\"${BM1_DT_ID}\",
    \"entityName\":\"${BM1_NAME}\",
    \"entityType\":\"HOST\"
  },
  \"impactedEntities\":[{
    \"entityId\":\"KUBERNETES_WORKLOAD-argus-ui\",
    \"entityName\":\"argus-ui\",
    \"entityType\":\"KUBERNETES_WORKLOAD\"
  }],
  \"problemDetails\":\"OPEN Problem P-SIM1-POD6-${S1}\nKubernetes workload\nargus-ui\n\nNot all pods ready\nDeployment argus-ui has 0/1 ready pods.\nk8s.workload.name: argus-ui\nk8s.workload.kind: Deployment\nk8s.namespace.name: monitoring-dev\nk8s.node.name: ${K8N1_NAME}\nk8s.cluster.name: ${CLUSTER1}\nk8s.cluster.uid: ${CLUSTER1_UID}\n\",
  \"customProperties\":{
    \"k8s.workload.name\":\"argus-ui\",
    \"k8s.workload.kind\":\"Deployment\",
    \"k8s.namespace.name\":\"monitoring-dev\",
    \"k8s.node.name\":\"${K8N1_NAME}\",
    \"k8s.cluster.name\":\"${CLUSTER1}\",
    \"k8s.cluster.uid\":\"${CLUSTER1_UID}\",
    \"environment\":\"ADC\"
  }
}"

pause 8
result
dbcheck
echo ""
echo "--- Assertions ---"
n=$(count_incidents_for "${BM1_NAME}")
check_pass "S1 single incident for root ${BM1_NAME}" "$n" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 2 — Second independent BM (iapps-100-67-86-133) fails
#
# Must create a SEPARATE incident — different correlation_id
# Expected: 2 total incidents, each with their own correlation_id
# ══════════════════════════════════════════════════════════════════════════════
if should_run 2; then
section "Scenario 2: independent BM ${BM2_NAME} → VM → node (2nd incident)"
S2="S2-${TS}"

post "BM2 CPU saturation [root]" "P-SIM2-BM-${S2}" "{
  \"state\":\"OPEN\",
  \"problemId\":\"P-SIM2-BM-${S2}\",
  \"problemTitle\":\"Cloudstack_BareMetal - System CPU Utilization is High\",
  \"impactLevel\":\"INFRASTRUCTURE\",
  \"severity\":\"PERFORMANCE\",
  \"status\":\"OPEN\",
  \"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{
    \"entityId\":\"${BM2_DT_ID}\",
    \"entityName\":\"${BM2_NAME}\",
    \"entityType\":\"HOST\"
  },
  \"impactedEntities\":[{
    \"entityId\":\"${BM2_DT_ID}\",
    \"entityName\":\"${BM2_NAME}\",
    \"entityType\":\"HOST\"
  }],
  \"problemDetails\":\"OPEN Problem P-SIM2-BM-${S2}\nHost\n${BM2_NAME}.example.com\n\nCloudstack_BareMetal - System CPU Utilization is High\nCPU utilization at 97% on KVM host ${BM2_NAME}.\nhost.name: ${BM2_NAME}.example.com\n\",
  \"Tags\":\"[Environment]bm:true, ProcessType:libvertd, [Environment]dc:mdn, [Environment]kvm:true\",
  \"customProperties\":{
    \"host.name\":\"${BM2_NAME}.example.com\",
    \"environment\":\"ADC\",
    \"impacted_entity\":\"${BM2_NAME}\"
  }
}"
pause 4

post "VM2 host unavailable [downstream]" "P-SIM2-VM-${S2}" "{
  \"state\":\"OPEN\",
  \"problemId\":\"P-SIM2-VM-${S2}\",
  \"problemTitle\":\"Host or monitoring unavailable\",
  \"impactLevel\":\"INFRASTRUCTURE\",
  \"severity\":\"AVAILABILITY\",
  \"status\":\"OPEN\",
  \"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{
    \"entityId\":\"${BM2_DT_ID}\",
    \"entityName\":\"${BM2_NAME}\",
    \"entityType\":\"HOST\"
  },
  \"impactedEntities\":[{
    \"entityId\":\"${VM2_DT_ID}\",
    \"entityName\":\"${VM2_NAME}\",
    \"entityType\":\"HOST\"
  }],
  \"problemDetails\":\"OPEN Problem P-SIM2-VM-${S2}\nHost\n${VM2_NAME}\n\nHost or monitoring unavailable\nHost or monitoring unavailable due to connectivity issues or server outage\nhost.name: ${VM2_NAME}\nk8s.cluster.name: ${CLUSTER1}\nk8s.cluster.uid: ${CLUSTER1_UID}\n\",
  \"customProperties\":{
    \"host.name\":\"${VM2_NAME}\",
    \"environment\":\"ADC\",
    \"k8s.cluster.name\":\"${CLUSTER1}\",
    \"k8s.cluster.uid\":\"${CLUSTER1_UID}\"
  }
}"
pause 4

post "K8s node2 NotReady [downstream]" "P-SIM2-NODE-${S2}" "{
  \"state\":\"OPEN\",
  \"problemId\":\"P-SIM2-NODE-${S2}\",
  \"problemTitle\":\"Kubernetes node ${K8N2_NAME} is NotReady\",
  \"impactLevel\":\"INFRASTRUCTURE\",
  \"severity\":\"AVAILABILITY\",
  \"status\":\"OPEN\",
  \"startTime\":\"${NOW}\",
  \"rootCauseEntity\":{
    \"entityId\":\"${BM2_DT_ID}\",
    \"entityName\":\"${BM2_NAME}\",
    \"entityType\":\"HOST\"
  },
  \"impactedEntities\":[{
    \"entityId\":\"${K8N2_DT_ID}\",
    \"entityName\":\"${K8N2_NAME}\",
    \"entityType\":\"KUBERNETES_NODE\"
  }],
  \"problemDetails\":\"OPEN Problem P-SIM2-NODE-${S2}\nKubernetes node\n${K8N2_NAME}\n\nKubernetes node is not ready\nk8s.node.name: ${K8N2_NAME}\nk8s.cluster.name: ${CLUSTER1}\nk8s.cluster.uid: ${CLUSTER1_UID}\n\",
  \"customProperties\":{
    \"k8s.node.name\":\"${K8N2_NAME}\",
    \"k8s.cluster.name\":\"${CLUSTER1}\",
    \"k8s.cluster.uid\":\"${CLUSTER1_UID}\",
    \"environment\":\"ADC\"
  }
}"

pause 8
result
dbcheck
echo ""
echo "--- Assertions ---"
n1=$(count_incidents_for "${BM1_NAME}")
n2=$(count_incidents_for "${BM2_NAME}")
check_pass "S2 incident for BM1 ${BM1_NAME} still exists" "$n1" "1"
check_pass "S2 separate incident for BM2 ${BM2_NAME}" "$n2" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# SCENARIO 3 — RESOLVED lifecycle: scenario 1 alerts resolve → incident closes
# ══════════════════════════════════════════════════════════════════════════════
if should_run 3; then
section "Scenario 3: RESOLVED — BM1 storm clears, incident auto-closes"

if [[ -z "${S1:-}" ]]; then
  echo "  NOTE: run scenario 1 first to populate S1. Skipping resolve."
else
  for pid in "P-SIM1-BM-${S1}" "P-SIM1-VM-${S1}" "P-SIM1-NODE-${S1}" \
             "P-SIM1-POD1-${S1}" "P-SIM1-POD2-${S1}" "P-SIM1-POD3-${S1}" \
             "P-SIM1-POD4-${S1}" "P-SIM1-POD5-${S1}" "P-SIM1-POD6-${S1}"; do
    post "RESOLVED ${pid}" "${pid}" "{
      \"state\":\"RESOLVED\",
      \"problemId\":\"${pid}\",
      \"problemTitle\":\"Resolved\",
      \"impactLevel\":\"INFRASTRUCTURE\",
      \"severity\":\"AVAILABILITY\",
      \"status\":\"RESOLVED\",
      \"startTime\":\"${NOW}\",
      \"rootCauseEntity\":{
        \"entityId\":\"${BM1_DT_ID}\",
        \"entityName\":\"${BM1_NAME}\",
        \"entityType\":\"HOST\"
      },
      \"impactedEntities\":[],
      \"problemDetails\":\"Problem ${pid} resolved.\",
      \"customProperties\":{\"environment\":\"ADC\"}
    }"
    sleep 1
  done

  pause 10
  result
  echo ""
  echo "--- DB: incident status after RESOLVED ---"
  kubectl exec -n aileron postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT '  [' || status || '] corr=' || COALESCE(correlation_id,'NULL') || ' alerts=' || jsonb_array_length(alert_ids) || ' title=' || LEFT(title,50)
    FROM incidents
    WHERE auto_created=true
      AND correlation_id='${BM1_NAME}'
    ORDER BY updated_at DESC LIMIT 3;" 2>/dev/null | grep -v "^$"
fi
fi
