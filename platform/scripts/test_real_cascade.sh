#!/usr/bin/env bash
# test_real_cascade.sh — real-topology cascade simulation
#
# Uses ACTUAL nodes and pods pulled live from k8s clusters.
# Simulates a CloudStack bare-metal host going down and the full
# domino of VMs → K8s nodes → real workload pods that follows.
#
# Topology used:
#   BM   cloudstack-cluster-2-iapps-100-67-61-18
#    └── VM  cloudstack-cluster-2-mps-nonprod-rno-worker-z3-08 (10.127.200.163)
#         └── K8s node  mps-nonprod-rno-worker-z3-08
#              ├── dex/dex-565647688-wd4qz
#              ├── ingress-nginx/ingress-nginx-controller-5ffg4
#              ├── aem-qa/dispatcher-publish-preview-1
#              ├── aem-uat/author-uat-1
#              ├── argocd/argocd-repo-server-5d956d6cbf-v4xnh
#              ├── geo-service-perf/geo-service-78fc45969-qtlxb
#              ├── geo-service-qe/geo-service-669cf6f8f9-cndgx
#              ├── opentracing/jaeger-collector-5f8667b898-nstrx
#              └── interactive-dx-uat/maestro-webhooks-677698dc4b-p9xr6
#
#   k8preview01-rno cluster (second cascade, separate incident):
#   Node  k8preview01-cs-vm-worker12-rno (10.127.203.215)
#              ├── ac-films-feature/ac-films-66ffd4b8bb-kwpmr
#              ├── anim-guide-v3/anim-guide-8b9f8fb67-wdl8j
#              ├── argocd/argocd-redis-6d68b7d767-ht4wg
#              ├── ingress-nginx/nginx-ingress-controller-lgg4m
#              ├── frontier-dev/frontier-5d5c8cb67d-9ss7t
#              ├── dndportal-dev/portal-api-6898ddf48-tsmg6
#              └── stormy-stg/stormy-backend-8cc6747cd-fjlzm
#
# Usage:
#   bash scripts/test_real_cascade.sh              # both cascades
#   bash scripts/test_real_cascade.sh nonprod      # nonprod-rno cascade only
#   bash scripts/test_real_cascade.sh preview      # k8preview01 cascade only
#   bash scripts/test_real_cascade.sh resolved     # send RESOLVED for all alerts above
#   DRYRUN=1 bash scripts/test_real_cascade.sh     # print payloads without sending

set -uo pipefail

ENDPOINT="https://aileron.example.com/api/v1/webhooks/dynatrace"
API_KEY="ah_c76ddeaa86c74818ac84997c0f2a8174eaba3bf0fdab295132acb8aba2238e5f"
NS="aileron"
NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
TS=$(date +%s)
DRYRUN=${DRYRUN:-0}

# ─── real entity constants ────────────────────────────────────────────────────
# CloudStack BM hypervisor
BM="cloudstack-cluster-2-iapps-100-67-61-18"
BM_IP="100.67.61.18"

# VM on that BM (backs the K8s node below)
VM="cloudstack-cluster-2-mps-nonprod-rno-worker-z3-08"
VM_IP="10.127.200.163"

# K8s node (mps-nonprod-rno cluster)
NODE="mps-nonprod-rno-worker-z3-08"
NODE_IP="10.127.200.163"
NONPROD_UID="88b12bf2-9f23-4b4a-b06a-3d2b33134a3b"

# k8preview01-rno node (CloudStack VM-backed)
PREV_NODE="k8preview01-cs-vm-worker12-rno"
PREV_NODE_IP="10.127.203.215"
K8PREV_UID="d1be9a2f-4e83-49bf-8800-b9a2bc2e7da0"

# ─── colour helpers ───────────────────────────────────────────────────────────
BOLD="\033[1m"; DIM="\033[2m"; RST="\033[0m"
RED="\033[31m"; GRN="\033[32m"; CYN="\033[36m"

section() {
  echo ""
  echo -e "${BOLD}══════════════════════════════════════════════════════════${RST}"
  echo -e "${BOLD}  $1${RST}"
  echo -e "${BOLD}══════════════════════════════════════════════════════════${RST}"
}
pause() { echo -e "  ${DIM}⏳ ${1}s…${RST}"; sleep "$1"; }

post() {
  local label="$1" pid="$2" payload="$3"
  if [[ "$DRYRUN" == "1" ]]; then
    echo -e "  ${DIM}[DRYRUN] $label${RST}"
    echo "$payload" | python3 -m json.tool 2>/dev/null | head -6
    return
  fi
  printf "  → %-55s " "$label"
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
    echo ""
  else
    printf "${RED}HTTP %s${RST}  %s\n" "$http_code" "$(echo "$body" | head -c 200)"
  fi
}

dbcheck() {
  echo ""
  echo -e "  ${CYN}--- DB: auto-created incidents (last 15 min) ---${RST}"
  kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT '  INC ' || LEFT(id::text,8) ||
           '  alerts=' || jsonb_array_length(alert_ids) ||
           '  ' || status ||
           '  corr=' || COALESCE(LEFT(correlation_id,26),'NULL') ||
           '  » ' || LEFT(title,48)
    FROM incidents
    WHERE auto_created=true AND updated_at > NOW()-INTERVAL '15 minutes'
    ORDER BY created_at DESC LIMIT 12;" 2>/dev/null | grep -v "^$" \
    || echo -e "  ${DIM}(kubectl unavailable)${RST}"
}

logs() {
  echo ""
  echo -e "  ${CYN}--- Correlation log (last 90s) ---${RST}"
  kubectl logs -n "$NS" -l app=alerthub-backend --since=90s 2>/dev/null \
  | grep -E "RCE alert=|ATTACH_TO_ROOT|CREATE_ROOT|correlation_id.*set|incident.*auto" \
  | head -20 || echo -e "  ${DIM}(kubectl unavailable)${RST}"
}

incident_count() {
  kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -t -c "
    SELECT COUNT(*) FROM incidents
    WHERE auto_created=true AND correlation_id='$1'
      AND status IN ('open','investigating')
      AND updated_at > NOW()-INTERVAL '15 minutes';" 2>/dev/null | tr -d ' \n' || echo "?"
}

check() {
  local label="$1" got="$2" want="$3"
  if [[ "$got" == "$want" ]]; then
    echo -e "  ${GRN}✓ PASS${RST}  ${label}: ${got}"
  else
    echo -e "  ${RED}✗ FAIL${RST}  ${label}: want=${want} got=${got}"
  fi
}

RUN_NONPROD=true
RUN_PREVIEW=true
RUN_RESOLVED=false
if [[ $# -gt 0 ]]; then
  case "$1" in
    nonprod)  RUN_PREVIEW=false ;;
    preview)  RUN_NONPROD=false ;;
    resolved) RUN_NONPROD=false; RUN_PREVIEW=false; RUN_RESOLVED=true ;;
  esac
fi

echo ""
echo -e "${BOLD}AlertHub — Real Topology Cascade Simulation${RST}"
echo -e "${DIM}Endpoint : ${ENDPOINT}${RST}"
echo -e "${DIM}Timestamp: ${TS}${RST}"
[[ "$DRYRUN" == "1" ]] && echo -e "${DIM}[DRY RUN — not sending]${RST}"

# ══════════════════════════════════════════════════════════════════════════════
# CASCADE A — mps-nonprod-rno: BM → VM → K8s node → real workloads
#
# Real BM host cloudstack-cluster-2-iapps-100-67-61-18 loses power.
# The VM cloudstack-cluster-2-mps-nonprod-rno-worker-z3-08 (backing K8s node
# mps-nonprod-rno-worker-z3-08) is terminated. All 9 workload pods on that
# node get evicted and fire NOT-ALL-PODS-READY alerts.
#
# Expected: 1 incident, correlation_id=cloudstack-cluster-2-iapps-100-67-61-18
# ══════════════════════════════════════════════════════════════════════════════
if $RUN_NONPROD; then
section "Cascade A — nonprod-rno: BM down → VM terminated → K8s node → 9 workloads"
echo -e "  ${DIM}BM     : ${BM} (${BM_IP})${RST}"
echo -e "  ${DIM}VM     : ${VM} (${VM_IP})${RST}"
echo -e "  ${DIM}Node   : ${NODE} (${NODE_IP})${RST}"
echo -e "  ${DIM}Cluster: mps-nonprod-rno (${NONPROD_UID:0:8}…)${RST}"
CA="CA-${TS}"

# ── 1. BM host goes down (root cause) ────────────────────────────────────────
post "1/11 BM power failure — ${BM}" "P-CA-BM-${CA}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-CA-BM-${CA}\",
  \"problemTitle\": \"Host or monitoring unavailable — ${BM}\",
  \"ProblemDetailsText\": \"KVM hypervisor ${BM} (${BM_IP}) is unreachable. All hosted VMs are at risk. IPMI shows power fault. host.name: ${BM}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${BM}\",
    \"entityName\": \"${BM}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"HOST-${BM}\",
    \"entityName\": \"${BM}\",
    \"entityType\": \"HOST\"
  }],
  \"customProperties\": {
    \"host.name\": \"${BM}\",
    \"impacted_entity\": \"${BM}\",
    \"environment\": \"ADC\",
    \"dt.entity.host\": \"HOST-${BM}\",
    \"host_ip\": \"${BM_IP}\"
  },
  \"ManagementZone\": \"CloudStack-RNO\",
  \"EntityTags\": [\"kvm\", \"cloudstack-cluster-2\", \"rno\", \"bare-metal\"]
}"
pause 3

# ── 2. VM on that BM becomes unreachable ─────────────────────────────────────
post "2/11 VM unreachable — ${VM}" "P-CA-VM-${CA}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-CA-VM-${CA}\",
  \"problemTitle\": \"Host or monitoring unavailable — ${VM}\",
  \"ProblemDetailsText\": \"VM ${VM} (${VM_IP}) is unresponsive. Host ${BM} is down. Forceful VM termination detected. host.name: ${VM}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${BM}\",
    \"entityName\": \"${BM}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"HOST-${VM}\",
    \"entityName\": \"${VM}\",
    \"entityType\": \"HOST\"
  }],
  \"customProperties\": {
    \"host.name\": \"${VM}\",
    \"impacted_entity\": \"${VM}\",
    \"environment\": \"ADC\",
    \"host_ip\": \"${VM_IP}\"
  },
  \"EntityTags\": [\"vm\", \"cloudstack-cluster-2\", \"unavailable\"]
}"
pause 3

# ── 3. K8s node goes NotReady ─────────────────────────────────────────────────
post "3/11 K8s node NotReady — ${NODE}" "P-CA-NODE-${CA}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-CA-NODE-${CA}\",
  \"problemTitle\": \"Kubernetes node ${NODE} is NotReady\",
  \"ProblemDetailsText\": \"Node ${NODE} (${NODE_IP}) entered NotReady state. kubelet unresponsive — underlying VM ${VM} terminated. k8s.node.name: ${NODE}. k8s.cluster.name: mps-nonprod-rno. host.name: ${VM}\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${BM}\",
    \"entityName\": \"${BM}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_NODE-${NODE}\",
    \"entityName\": \"${NODE}\",
    \"entityType\": \"KUBERNETES_NODE\"
  }],
  \"customProperties\": {
    \"host.name\": \"${VM}\",
    \"k8s.node.name\": \"${NODE}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"environment\": \"ADC\"
  },
  \"ManagementZone\": \"K8s-mps-nonprod-rno\",
  \"EntityTags\": [\"kubernetes\", \"node\", \"notready\", \"mps-nonprod-rno\"]
}"
pause 3

# ── 4-11: real pods evicted ────────────────────────────────────────────────────
# Pods pulled live from: kubectl get pods --context oidc08@mps-nonprod-rno
# --field-selector spec.nodeName=mps-nonprod-rno-worker-z3-08

post "4/11 dex OIDC provider evicted (dex ns)" "P-CA-DEX-${CA}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-CA-DEX-${CA}\",
  \"problemTitle\": \"Not all pods ready — dex in namespace dex\",
  \"ProblemDetailsText\": \"Pod dex-565647688-wd4qz evicted from ${NODE} (node NotReady). OIDC/SSO authentication will be unavailable until pod reschedules. k8s.namespace.name: dex. k8s.workload.name: dex. k8s.cluster.name: mps-nonprod-rno\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${BM}\",
    \"entityName\": \"${BM}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-dex\",
    \"entityName\": \"dex\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.node.name\": \"${NODE}\",
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

post "5/11 ingress-nginx DaemonSet pod lost (ingress-nginx ns)" "P-CA-INGRESS-${CA}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-CA-INGRESS-${CA}\",
  \"problemTitle\": \"Not all pods ready — ingress-nginx-controller in ingress-nginx\",
  \"ProblemDetailsText\": \"DaemonSet pod ingress-nginx-controller-5ffg4 lost on ${NODE}. Node removed from LB backend pool. Traffic to workloads on z3-08 is disrupted. k8s.namespace.name: ingress-nginx\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${BM}\",
    \"entityName\": \"${BM}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-ingress-nginx-controller\",
    \"entityName\": \"ingress-nginx-controller\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.node.name\": \"${NODE}\",
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

post "6/11 AEM QA dispatcher evicted (aem-qa ns)" "P-CA-AEMQA-${CA}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-CA-AEMQA-${CA}\",
  \"problemTitle\": \"Not all pods ready — dispatcher-publish-preview in aem-qa\",
  \"ProblemDetailsText\": \"StatefulSet pod dispatcher-publish-preview-1 lost on ${NODE}. AEM QA publish-preview dispatcher unavailable. Content preview broken for QA environment. k8s.namespace.name: aem-qa\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${BM}\",
    \"entityName\": \"${BM}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-dispatcher-publish-preview\",
    \"entityName\": \"dispatcher-publish-preview\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.node.name\": \"${NODE}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"aem-qa\",
    \"k8s.workload.name\": \"dispatcher-publish-preview\",
    \"k8s.workload.kind\": \"statefulset\",
    \"pod.name\": \"dispatcher-publish-preview-1\",
    \"environment\": \"ADC\"
  }
}"
pause 2

post "7/11 AEM UAT author pod lost (aem-uat ns)" "P-CA-AEMUAT-${CA}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-CA-AEMUAT-${CA}\",
  \"problemTitle\": \"Not all pods ready — author-uat in aem-uat\",
  \"ProblemDetailsText\": \"StatefulSet pod author-uat-1 terminated on ${NODE}. AEM UAT authoring instance offline. Content authors cannot publish. k8s.namespace.name: aem-uat. k8s.workload.kind: statefulset\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${BM}\",
    \"entityName\": \"${BM}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-author-uat\",
    \"entityName\": \"author-uat\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.node.name\": \"${NODE}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"aem-uat\",
    \"k8s.workload.name\": \"author-uat\",
    \"k8s.workload.kind\": \"statefulset\",
    \"pod.name\": \"author-uat-1\",
    \"environment\": \"ADC\"
  }
}"
pause 2

post "8/11 ArgoCD repo-server pod evicted (argocd ns)" "P-CA-ARGOCD-${CA}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-CA-ARGOCD-${CA}\",
  \"problemTitle\": \"Not all pods ready — argocd-repo-server in argocd\",
  \"ProblemDetailsText\": \"Pod argocd-repo-server-5d956d6cbf-v4xnh evicted from ${NODE}. ArgoCD repository sync operations failing. GitOps deployments are paused until pod reschedules. k8s.namespace.name: argocd\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${BM}\",
    \"entityName\": \"${BM}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-argocd-repo-server\",
    \"entityName\": \"argocd-repo-server\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.node.name\": \"${NODE}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"argocd\",
    \"k8s.workload.name\": \"argocd-repo-server\",
    \"k8s.workload.kind\": \"deployment\",
    \"pod.name\": \"argocd-repo-server-5d956d6cbf-v4xnh\",
    \"environment\": \"ADC\"
  }
}"
pause 2

post "9/11 geo-service-perf evicted (geo-service-perf ns)" "P-CA-GEOPERF-${CA}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-CA-GEOPERF-${CA}\",
  \"problemTitle\": \"Not all pods ready — geo-service in geo-service-perf\",
  \"ProblemDetailsText\": \"Pod geo-service-78fc45969-qtlxb evicted from ${NODE}. Geo-service performance environment unavailable. Geolocation API calls will fail until rescheduled. k8s.namespace.name: geo-service-perf\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${BM}\",
    \"entityName\": \"${BM}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-geo-service\",
    \"entityName\": \"geo-service\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.node.name\": \"${NODE}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"geo-service-perf\",
    \"k8s.workload.name\": \"geo-service\",
    \"k8s.workload.kind\": \"deployment\",
    \"pod.name\": \"geo-service-78fc45969-qtlxb\",
    \"environment\": \"ADC\"
  }
}"
pause 2

post "10/11 geo-service-qe evicted (geo-service-qe ns)" "P-CA-GEOQE-${CA}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-CA-GEOQE-${CA}\",
  \"problemTitle\": \"Not all pods ready — geo-service in geo-service-qe\",
  \"ProblemDetailsText\": \"Pod geo-service-669cf6f8f9-cndgx evicted from ${NODE}. Geo-service QE environment down. k8s.namespace.name: geo-service-qe. k8s.cluster.name: mps-nonprod-rno\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${BM}\",
    \"entityName\": \"${BM}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-geo-service\",
    \"entityName\": \"geo-service\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.node.name\": \"${NODE}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"geo-service-qe\",
    \"k8s.workload.name\": \"geo-service\",
    \"k8s.workload.kind\": \"deployment\",
    \"pod.name\": \"geo-service-669cf6f8f9-cndgx\",
    \"environment\": \"ADC\"
  }
}"
pause 2

post "11/11 Jaeger collector evicted (opentracing ns)" "P-CA-JAEGER-${CA}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-CA-JAEGER-${CA}\",
  \"problemTitle\": \"Not all pods ready — jaeger-collector in opentracing\",
  \"ProblemDetailsText\": \"Pod jaeger-collector-5f8667b898-nstrx evicted from ${NODE}. Distributed tracing collection for nonprod-rno is impaired. Traces from z3 zone workloads are being dropped. k8s.namespace.name: opentracing\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${BM}\",
    \"entityName\": \"${BM}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-jaeger-collector\",
    \"entityName\": \"jaeger-collector\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.node.name\": \"${NODE}\",
    \"k8s.cluster.name\": \"mps-nonprod-rno\",
    \"k8s.cluster.uid\": \"${NONPROD_UID}\",
    \"k8s.namespace.name\": \"opentracing\",
    \"k8s.workload.name\": \"jaeger-collector\",
    \"k8s.workload.kind\": \"deployment\",
    \"pod.name\": \"jaeger-collector-5f8667b898-nstrx\",
    \"environment\": \"ADC\"
  }
}"

pause 8
logs
dbcheck
echo ""
n=$(incident_count "${BM}")
check "Cascade A: 1 incident correlated to root ${BM}" "$n" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# CASCADE B — k8preview01-rno: CloudStack VM node down → 7 real workloads
#
# Node k8preview01-cs-vm-worker12-rno (10.127.203.215) goes NotReady.
# Its backing CloudStack VM is terminated (separate cluster, separate incident).
# 7 real running pods are evicted.
#
# Expected: 1 incident, separate from Cascade A (different cluster UID)
# ══════════════════════════════════════════════════════════════════════════════
if $RUN_PREVIEW; then
section "Cascade B — k8preview01-rno: VM node down → 7 real workloads"
echo -e "  ${DIM}Node   : ${PREV_NODE} (${PREV_NODE_IP})${RST}"
echo -e "  ${DIM}Cluster: k8preview01-rno (${K8PREV_UID:0:8}…)${RST}"
CB="CB-${TS}"

post "1/8 Node NotReady — ${PREV_NODE}" "P-CB-NODE-${CB}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-CB-NODE-${CB}\",
  \"problemTitle\": \"Kubernetes node ${PREV_NODE} is NotReady\",
  \"ProblemDetailsText\": \"Node ${PREV_NODE} (${PREV_NODE_IP}) entered NotReady state. kubelet stopped responding. CloudStack VM backing this node is unresponsive. k8s.node.name: ${PREV_NODE}. k8s.cluster.name: k8preview01-rno\",
  \"impactLevel\": \"INFRASTRUCTURE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${PREV_NODE}\",
    \"entityName\": \"${PREV_NODE}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_NODE-${PREV_NODE}\",
    \"entityName\": \"${PREV_NODE}\",
    \"entityType\": \"KUBERNETES_NODE\"
  }],
  \"customProperties\": {
    \"host.name\": \"${PREV_NODE}\",
    \"k8s.node.name\": \"${PREV_NODE}\",
    \"k8s.cluster.name\": \"k8preview01-rno\",
    \"k8s.cluster.uid\": \"${K8PREV_UID}\",
    \"impacted_entity\": \"${PREV_NODE}\",
    \"environment\": \"ADC\"
  },
  \"EntityTags\": [\"kubernetes\", \"node\", \"notready\", \"k8preview01-rno\"]
}"
pause 3

post "2/8 ac-films feature build evicted (ac-films-feature ns)" "P-CB-FILMS-${CB}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-CB-FILMS-${CB}\",
  \"problemTitle\": \"Not all pods ready — ac-films in ac-films-feature\",
  \"ProblemDetailsText\": \"Pod ac-films-66ffd4b8bb-kwpmr evicted from ${PREV_NODE}. AC Films feature environment is down. Preview builds unavailable. k8s.namespace.name: ac-films-feature. k8s.cluster.name: k8preview01-rno\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${PREV_NODE}\",
    \"entityName\": \"${PREV_NODE}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-ac-films\",
    \"entityName\": \"ac-films\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.node.name\": \"${PREV_NODE}\",
    \"k8s.cluster.name\": \"k8preview01-rno\",
    \"k8s.cluster.uid\": \"${K8PREV_UID}\",
    \"k8s.namespace.name\": \"ac-films-feature\",
    \"k8s.workload.name\": \"ac-films\",
    \"k8s.workload.kind\": \"deployment\",
    \"pod.name\": \"ac-films-66ffd4b8bb-kwpmr\",
    \"environment\": \"ADC\"
  }
}"
pause 2

post "3/8 anim-guide v3 evicted (anim-guide-v3 ns)" "P-CB-ANIM-${CB}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-CB-ANIM-${CB}\",
  \"problemTitle\": \"Not all pods ready — anim-guide in anim-guide-v3\",
  \"ProblemDetailsText\": \"Pod anim-guide-8b9f8fb67-wdl8j evicted from ${PREV_NODE}. Animation Guide v3 preview environment offline. k8s.namespace.name: anim-guide-v3. k8s.cluster.name: k8preview01-rno\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${PREV_NODE}\",
    \"entityName\": \"${PREV_NODE}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-anim-guide\",
    \"entityName\": \"anim-guide\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.node.name\": \"${PREV_NODE}\",
    \"k8s.cluster.name\": \"k8preview01-rno\",
    \"k8s.cluster.uid\": \"${K8PREV_UID}\",
    \"k8s.namespace.name\": \"anim-guide-v3\",
    \"k8s.workload.name\": \"anim-guide\",
    \"k8s.workload.kind\": \"deployment\",
    \"pod.name\": \"anim-guide-8b9f8fb67-wdl8j\",
    \"environment\": \"ADC\"
  }
}"
pause 2

post "4/8 ArgoCD Redis evicted (argocd ns)" "P-CB-REDIS-${CB}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-CB-REDIS-${CB}\",
  \"problemTitle\": \"Not all pods ready — argocd-redis in argocd\",
  \"ProblemDetailsText\": \"Pod argocd-redis-6d68b7d767-ht4wg lost on ${PREV_NODE}. ArgoCD cache layer unavailable — cluster operations degraded. k8s.namespace.name: argocd. k8s.cluster.name: k8preview01-rno\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${PREV_NODE}\",
    \"entityName\": \"${PREV_NODE}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-argocd-redis\",
    \"entityName\": \"argocd-redis\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.node.name\": \"${PREV_NODE}\",
    \"k8s.cluster.name\": \"k8preview01-rno\",
    \"k8s.cluster.uid\": \"${K8PREV_UID}\",
    \"k8s.namespace.name\": \"argocd\",
    \"k8s.workload.name\": \"argocd-redis\",
    \"k8s.workload.kind\": \"deployment\",
    \"pod.name\": \"argocd-redis-6d68b7d767-ht4wg\",
    \"environment\": \"ADC\"
  }
}"
pause 2

post "5/8 ingress-nginx DaemonSet lost (ingress-nginx ns)" "P-CB-INGRESS-${CB}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-CB-INGRESS-${CB}\",
  \"problemTitle\": \"Not all pods ready — nginx-ingress-controller in ingress-nginx\",
  \"ProblemDetailsText\": \"DaemonSet pod nginx-ingress-controller-lgg4m lost on ${PREV_NODE}. Node removed from LB pool. Traffic routing for preview environments on this node degraded. k8s.namespace.name: ingress-nginx\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${PREV_NODE}\",
    \"entityName\": \"${PREV_NODE}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-nginx-ingress-controller\",
    \"entityName\": \"nginx-ingress-controller\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.node.name\": \"${PREV_NODE}\",
    \"k8s.cluster.name\": \"k8preview01-rno\",
    \"k8s.cluster.uid\": \"${K8PREV_UID}\",
    \"k8s.namespace.name\": \"ingress-nginx\",
    \"k8s.workload.name\": \"nginx-ingress-controller\",
    \"k8s.workload.kind\": \"daemonset\",
    \"pod.name\": \"nginx-ingress-controller-lgg4m\",
    \"environment\": \"ADC\"
  }
}"
pause 2

post "6/8 frontier-dev pod evicted (frontier-dev ns)" "P-CB-FRONTIER-${CB}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-CB-FRONTIER-${CB}\",
  \"problemTitle\": \"Not all pods ready — frontier in frontier-dev\",
  \"ProblemDetailsText\": \"Pod frontier-5d5c8cb67d-9ss7t evicted from ${PREV_NODE}. Frontier dev environment offline. k8s.namespace.name: frontier-dev. k8s.cluster.name: k8preview01-rno\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${PREV_NODE}\",
    \"entityName\": \"${PREV_NODE}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-frontier\",
    \"entityName\": \"frontier\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.node.name\": \"${PREV_NODE}\",
    \"k8s.cluster.name\": \"k8preview01-rno\",
    \"k8s.cluster.uid\": \"${K8PREV_UID}\",
    \"k8s.namespace.name\": \"frontier-dev\",
    \"k8s.workload.name\": \"frontier\",
    \"k8s.workload.kind\": \"deployment\",
    \"pod.name\": \"frontier-5d5c8cb67d-9ss7t\",
    \"environment\": \"ADC\"
  }
}"
pause 2

post "7/8 dnd-portal API evicted (dndportal-dev ns)" "P-CB-DND-${CB}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-CB-DND-${CB}\",
  \"problemTitle\": \"Not all pods ready — portal-api in dndportal-dev\",
  \"ProblemDetailsText\": \"Pod portal-api-6898ddf48-tsmg6 lost on ${PREV_NODE}. DnD portal dev API unavailable. k8s.namespace.name: dndportal-dev. k8s.cluster.name: k8preview01-rno\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${PREV_NODE}\",
    \"entityName\": \"${PREV_NODE}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-portal-api\",
    \"entityName\": \"portal-api\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.node.name\": \"${PREV_NODE}\",
    \"k8s.cluster.name\": \"k8preview01-rno\",
    \"k8s.cluster.uid\": \"${K8PREV_UID}\",
    \"k8s.namespace.name\": \"dndportal-dev\",
    \"k8s.workload.name\": \"portal-api\",
    \"k8s.workload.kind\": \"deployment\",
    \"pod.name\": \"portal-api-6898ddf48-tsmg6\",
    \"environment\": \"ADC\"
  }
}"
pause 2

post "8/8 stormy-stg backend evicted (stormy-stg ns)" "P-CB-STORMY-${CB}" "{
  \"state\": \"OPEN\",
  \"problemId\": \"P-CB-STORMY-${CB}\",
  \"problemTitle\": \"Not all pods ready — stormy-backend in stormy-stg\",
  \"ProblemDetailsText\": \"Pod stormy-backend-8cc6747cd-fjlzm evicted from ${PREV_NODE}. Stormy staging backend offline. k8s.namespace.name: stormy-stg. k8s.cluster.name: k8preview01-rno\",
  \"impactLevel\": \"SERVICE\",
  \"severity\": \"AVAILABILITY\",
  \"status\": \"OPEN\",
  \"startTime\": \"${NOW}\",
  \"rootCauseEntity\": {
    \"entityId\": \"HOST-${PREV_NODE}\",
    \"entityName\": \"${PREV_NODE}\",
    \"entityType\": \"HOST\"
  },
  \"impactedEntities\": [{
    \"entityId\": \"KUBERNETES_WORKLOAD-stormy-backend\",
    \"entityName\": \"stormy-backend\",
    \"entityType\": \"KUBERNETES_WORKLOAD\"
  }],
  \"customProperties\": {
    \"k8s.node.name\": \"${PREV_NODE}\",
    \"k8s.cluster.name\": \"k8preview01-rno\",
    \"k8s.cluster.uid\": \"${K8PREV_UID}\",
    \"k8s.namespace.name\": \"stormy-stg\",
    \"k8s.workload.name\": \"stormy-backend\",
    \"k8s.workload.kind\": \"deployment\",
    \"pod.name\": \"stormy-backend-8cc6747cd-fjlzm\",
    \"environment\": \"ADC\"
  }
}"

pause 8
logs
dbcheck
echo ""
n=$(incident_count "${PREV_NODE}")
check "Cascade B: 1 incident correlated to root ${PREV_NODE}" "$n" "1"
fi

# ══════════════════════════════════════════════════════════════════════════════
# RESOLVED — send RESOLVED for all Cascade A and B alerts
# Pass 'resolved' as argument to run only this section.
# ══════════════════════════════════════════════════════════════════════════════
if $RUN_RESOLVED; then
section "RESOLVED — sending RESOLVED for all Cascade A+B alert IDs"
echo -e "  ${DIM}Requires the same TS used during OPEN (export TS=<value>)${RST}"
echo -e "  ${DIM}Current TS: ${TS} — set TS=<original> if re-running resolution${RST}"

CA_IDS=("P-CA-BM" "P-CA-VM" "P-CA-NODE" "P-CA-DEX" "P-CA-INGRESS" "P-CA-AEMQA" "P-CA-AEMUAT" "P-CA-ARGOCD" "P-CA-GEOPERF" "P-CA-GEOQE" "P-CA-JAEGER")
for pid_prefix in "${CA_IDS[@]}"; do
  pid="${pid_prefix}-CA-${TS}"
  post "RESOLVED ${pid}" "$pid" "{
    \"state\": \"RESOLVED\",
    \"problemId\": \"${pid}\",
    \"problemTitle\": \"RESOLVED: ${pid_prefix}\",
    \"ProblemDetailsText\": \"Problem resolved — infrastructure recovered.\",
    \"impactLevel\": \"INFRASTRUCTURE\",
    \"severity\": \"AVAILABILITY\",
    \"status\": \"RESOLVED\",
    \"startTime\": \"${NOW}\",
    \"endTime\": \"${NOW}\",
    \"rootCauseEntity\": {
      \"entityId\": \"HOST-${BM}\",
      \"entityName\": \"${BM}\",
      \"entityType\": \"HOST\"
    },
    \"impactedEntities\": [],
    \"customProperties\": {
      \"host.name\": \"${BM}\",
      \"k8s.cluster.name\": \"mps-nonprod-rno\",
      \"k8s.cluster.uid\": \"${NONPROD_UID}\",
      \"environment\": \"ADC\"
    }
  }"
done

CB_IDS=("P-CB-NODE" "P-CB-FILMS" "P-CB-ANIM" "P-CB-REDIS" "P-CB-INGRESS" "P-CB-FRONTIER" "P-CB-DND" "P-CB-STORMY")
for pid_prefix in "${CB_IDS[@]}"; do
  pid="${pid_prefix}-CB-${TS}"
  post "RESOLVED ${pid}" "$pid" "{
    \"state\": \"RESOLVED\",
    \"problemId\": \"${pid}\",
    \"problemTitle\": \"RESOLVED: ${pid_prefix}\",
    \"ProblemDetailsText\": \"Problem resolved — node recovered.\",
    \"impactLevel\": \"INFRASTRUCTURE\",
    \"severity\": \"AVAILABILITY\",
    \"status\": \"RESOLVED\",
    \"startTime\": \"${NOW}\",
    \"endTime\": \"${NOW}\",
    \"rootCauseEntity\": {
      \"entityId\": \"HOST-${PREV_NODE}\",
      \"entityName\": \"${PREV_NODE}\",
      \"entityType\": \"HOST\"
    },
    \"impactedEntities\": [],
    \"customProperties\": {
      \"host.name\": \"${PREV_NODE}\",
      \"k8s.cluster.name\": \"k8preview01-rno\",
      \"k8s.cluster.uid\": \"${K8PREV_UID}\",
      \"environment\": \"ADC\"
    }
  }"
done

pause 5
dbcheck
fi

# ─── final summary ────────────────────────────────────────────────────────────
echo ""
echo -e "${BOLD}══════════════════════════════════════════════════════════${RST}"
echo -e "${BOLD}  Summary — incidents last 15 minutes${RST}"
echo -e "${BOLD}══════════════════════════════════════════════════════════${RST}"
kubectl exec -n "$NS" postgres-primary-0 -- psql -U alerthub -d alerthub -c "
  SELECT
    LEFT(title,52)                                  AS title,
    status,
    jsonb_array_length(alert_ids)                   AS alerts,
    LEFT(COALESCE(correlation_id,''),30)            AS correlation_id,
    created_at::time(0)                             AS at
  FROM incidents
  WHERE auto_created = true
    AND created_at > NOW() - INTERVAL '15 minutes'
  ORDER BY created_at DESC;" 2>/dev/null \
  || echo -e "  ${DIM}View at: https://aileron.example.com/incidents${RST}"
echo ""
echo -e "  ${DIM}To resolve all: TS=${TS} bash scripts/test_real_cascade.sh resolved${RST}"
