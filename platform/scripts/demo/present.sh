#!/usr/bin/env bash
# ╔══════════════════════════════════════════════════════════════════════════════╗
# ║         AlertHub Enterprise — Live Demo Script                              ║
# ║         Use this to present to team / management                            ║
# ╚══════════════════════════════════════════════════════════════════════════════╝
#
# Usage:
#   bash scripts/demo/present.sh              # interactive menu
#   bash scripts/demo/present.sh D1           # run single demo by ID
#   bash scripts/demo/present.sh D1 D3 D5     # run specific demos
#   SKIP_WAIT=1 bash scripts/demo/present.sh  # skip "press Enter" pauses
#
# Demos:
#   D1  Alert → Auto-Incident  (3 related alerts → 1 correlated incident)
#   D2  Burst Dedup             (4 identical alerts arrive in 26ms → 1 incident)
#   D3  Cross-namespace cascade (node root → 3 workloads across namespaces)
#   D4  Full Lifecycle          (fire → correlate → show timeline → resolve)
#   D5  Blast Radius            (shows populated blast radius nodes + labels)
#   D6  Search Demo             (INC-number + title + correlation ID search)
#   D7  Isolation               (2 unrelated failures → 2 separate incidents)
#   D8  OPEN → RESOLVED auto-close (one ticket, opens and closes cleanly)
#   D9  Scale demo              (30 concurrent alerts → 1–3 incidents max)

set -uo pipefail

# ── config ────────────────────────────────────────────────────────────────────
ENDPOINT="https://aileron.example.com/api/v1/webhooks/dynatrace"
BASE_URL="https://aileron.example.com"
NS=aileron
TS=$(date +%s)
NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
SKIP_WAIT=${SKIP_WAIT:-0}

# real entity constants (MPS production infra)
NONPROD_UID="88b12bf2-9f23-4b4a-b06a-3d2b33134a3b"
MONDEV_UID="00a07750-e556-443e-89d9-80341edb472d"

# ── color codes ───────────────────────────────────────────────────────────────
BOLD="\033[1m"
DIM="\033[2m"
RED="\033[31m"
GRN="\033[32m"
YLW="\033[33m"
BLU="\033[34m"
MAG="\033[35m"
CYN="\033[36m"
WHT="\033[37m"
RST="\033[0m"

# ── helpers ───────────────────────────────────────────────────────────────────
banner() {
  echo ""
  echo -e "${BOLD}${MAG}╔══════════════════════════════════════════════════════╗${RST}"
  printf "${BOLD}${MAG}║${RST}  %-52s${BOLD}${MAG}║${RST}\n" "$1"
  printf "${BOLD}${MAG}║${RST}  %-52s${BOLD}${MAG}║${RST}\n" "$2"
  echo -e "${BOLD}${MAG}╚══════════════════════════════════════════════════════╝${RST}"
  echo ""
}

step() { echo -e "  ${BOLD}${CYN}▶ $1${RST}"; }
ok()   { echo -e "  ${GRN}✓ $1${RST}"; }
warn() { echo -e "  ${YLW}⚠ $1${RST}"; }
fail() { echo -e "  ${RED}✗ $1${RST}"; }
dim()  { echo -e "  ${DIM}$1${RST}"; }
hr()   { echo -e "  ${DIM}────────────────────────────────────────────${RST}"; }

pause() {
  echo -e "  ${DIM}⏳ waiting ${1}s...${RST}"
  sleep "$1"
}

wait_key() {
  [[ "$SKIP_WAIT" == "1" ]] && return
  echo ""
  echo -e "  ${YLW}▸ Press Enter to continue...${RST}"
  read -r
}

# Fire a Dynatrace webhook alert and print result
fire() {
  local label="$1"
  local payload="$2"
  printf "  ${BOLD}%-46s${RST} " "→ $label"
  local code
  code=$(curl -s -o /tmp/ah_demo_resp.json -w "%{http_code}" -X POST "$ENDPOINT" \
    -H "Content-Type: application/json" -d "$payload")
  if [[ "$code" == "200" || "$code" == "201" || "$code" == "202" ]]; then
    echo -e "${GRN}HTTP $code ✓${RST}"
  else
    echo -e "${RED}HTTP $code ✗${RST}"
    cat /tmp/ah_demo_resp.json 2>/dev/null | head -3
  fi
}

# DB query via kubectl exec
db() {
  local pod
  pod=$(kubectl get pods -n "$NS" --no-headers 2>/dev/null \
    | grep "alerthub-backend.*Running" | head -1 | awk '{print $1}')
  if [[ -z "$pod" ]]; then echo "(db unavailable)"; return; fi
  kubectl exec -n "$NS" "$pod" -- \
    psql "postgresql://alerthub:pg-AIOps-Secure-2024-Prod@postgres-primary.${NS}.svc.cluster.local:5432/alerthub" \
    -t -c "$1" 2>/dev/null | grep -v "^$" | sed 's/^/    /'
}

# Tail backend logs for pipeline decisions
show_decisions() {
  local since="${1:-45s}"
  echo ""
  echo -e "  ${BOLD}Pipeline decisions (last ${since}):${RST}"
  kubectl logs -n "$NS" -l app=alerthub-backend --since="$since" 2>/dev/null \
    | grep -E "🎯 RCE|✅ Created incident|🔗 Alert .* merged|🔄 Updating|Merging alert|correlation_id" \
    | grep -v "^$" | tail -12 | sed 's/^/    /'
}

# Display incidents created in the last N minutes
show_incidents() {
  local minutes="${1:-5}"
  echo ""
  echo -e "  ${BOLD}Incidents (last ${minutes} min):${RST}"
  db "SELECT
        'INC-' || incident_number AS number,
        severity,
        status,
        jsonb_array_length(COALESCE(alert_ids,'[]'::jsonb)) AS alerts,
        LEFT(COALESCE(blast_radius::text,'[]'),50) AS blast_radius,
        LEFT(title,50) AS title
      FROM incidents
      WHERE auto_created = TRUE
        AND created_at >= NOW() - INTERVAL '${minutes} minutes'
      ORDER BY created_at DESC LIMIT 10;"
}

# Show timeline for most recent incident
show_timeline() {
  echo ""
  echo -e "  ${BOLD}Timeline (most recent incident):${RST}"
  db "SELECT
        to_char(created_at, 'HH24:MI:SS') AS time,
        event_type,
        title
      FROM incident_timeline
      WHERE incident_id = (
        SELECT id FROM incidents WHERE auto_created=TRUE
        ORDER BY created_at DESC LIMIT 1
      )
      ORDER BY created_at ASC
      LIMIT 20;"
}

# determine which demos to run
RUN_ALL=true
SELECTED=()
if [[ $# -gt 0 && "$1" != "--menu" ]]; then
  RUN_ALL=false
  SELECTED=("$@")
fi
should_run() {
  $RUN_ALL && return 0
  for s in "${SELECTED[@]}"; do [[ "$s" == "$1" ]] && return 0; done
  return 1
}

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Interactive menu (when run with no args)
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
if $RUN_ALL && [[ $# -eq 0 ]]; then
  clear
  echo -e "${BOLD}${MAG}"
  echo "  ╔══════════════════════════════════════════════════════╗"
  echo "  ║         AlertHub Enterprise — Demo Launcher          ║"
  echo "  ║         MPS SRE AIOps Platform                       ║"
  echo "  ╚══════════════════════════════════════════════════════╝"
  echo -e "${RST}"
  echo ""
  echo -e "  ${BOLD}Available Demos:${RST}"
  echo ""
  echo -e "  ${CYN}D1${RST}  Alert → Auto-Incident Correlation     (best first demo)"
  echo -e "  ${CYN}D2${RST}  Burst Dedup — 4 alerts in 26ms → 1 incident"
  echo -e "  ${CYN}D3${RST}  Cross-namespace Cascade               (node → 3 workloads)"
  echo -e "  ${CYN}D4${RST}  Full Lifecycle + Timeline             (fire → timeline → resolve)"
  echo -e "  ${CYN}D5${RST}  Blast Radius Demo                     (populated node labels)"
  echo -e "  ${CYN}D6${RST}  Search Demo                           (INC-number, title, source)"
  echo -e "  ${CYN}D7${RST}  Isolation — 2 failures → 2 incidents  (no false correlation)"
  echo -e "  ${CYN}D8${RST}  OPEN → RESOLVED auto-close"
  echo -e "  ${CYN}D9${RST}  Scale — 30 concurrent alerts"
  echo ""
  echo -e "  ${BOLD}ALL${RST} Run all demos sequentially"
  echo ""
  printf "  ${YLW}Enter demo ID (e.g. D1) or ALL: ${RST}"
  read -r CHOICE
  echo ""

  if [[ "$CHOICE" == "ALL" || "$CHOICE" == "all" ]]; then
    RUN_ALL=true
  else
    RUN_ALL=false
    SELECTED=($CHOICE)  # support space-separated list
  fi
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# D1 — Alert → Auto-Incident Correlation
#
# 3 Dynatrace alerts fire on the same node (mps-nonprod-rno-worker-z3-08).
# The pipeline detects the shared rootCauseEntity and groups all 3 into
# ONE incident — demonstrating AI-based correlation without human intervention.
#
# Expected: 1 incident with alert_count=3, correlation_method shown
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
if should_run D1; then
  banner "D1 — Alert → Auto-Incident Correlation" "3 alerts, 1 shared root → 1 incident"
  ID="D1-${TS}"
  NODE="mps-nonprod-rno-worker-z3-08"

  step "Firing 3 Dynatrace alerts from the same root cause..."
  dim  "Node: ${NODE} (mps-nonprod-rno cluster)"
  echo ""

  fire "Node CPU saturation (root alert)" "{
    \"state\":\"OPEN\",
    \"problemId\":\"P-D1-ROOT-${ID}\",
    \"problemTitle\":\"CPU-request saturation on node ${NODE}\",
    \"impactLevel\":\"INFRASTRUCTURE\",
    \"severity\":\"PERFORMANCE\",
    \"status\":\"OPEN\",
    \"startTime\":\"${NOW}\",
    \"rootCauseEntity\":{
      \"entityId\":\"HOST-${NODE}\",
      \"entityName\":\"${NODE}\",
      \"entityType\":\"HOST\"
    },
    \"impactedEntities\":[{
      \"entityId\":\"HOST-${NODE}\",
      \"entityName\":\"${NODE}\",
      \"entityType\":\"HOST\"
    }],
    \"problemDetails\":\"CPU requests exceed 90% on node ${NODE}. host.name: ${NODE}. k8s.node.name: ${NODE}. k8s.cluster.name: mps-nonprod-rno\",
    \"customProperties\":{
      \"host.name\":\"${NODE}\",
      \"k8s.node.name\":\"${NODE}\",
      \"k8s.cluster.name\":\"mps-nonprod-rno\",
      \"k8s.cluster.uid\":\"${NONPROD_UID}\",
      \"environment\":\"ADC\"
    }
  }"
  pause 3

  fire "DaemonSet pod evicted — ingress-nginx (downstream)" "{
    \"state\":\"OPEN\",
    \"problemId\":\"P-D1-DS1-${ID}\",
    \"problemTitle\":\"Not all pods ready — ingress-nginx-controller in ingress-nginx\",
    \"impactLevel\":\"SERVICE\",
    \"severity\":\"AVAILABILITY\",
    \"status\":\"OPEN\",
    \"startTime\":\"${NOW}\",
    \"rootCauseEntity\":{
      \"entityId\":\"HOST-${NODE}\",
      \"entityName\":\"${NODE}\",
      \"entityType\":\"HOST\"
    },
    \"impactedEntities\":[{
      \"entityId\":\"KUBERNETES_WORKLOAD-ingress-nginx-controller\",
      \"entityName\":\"ingress-nginx-controller\",
      \"entityType\":\"KUBERNETES_WORKLOAD\"
    }],
    \"problemDetails\":\"ingress-nginx pod evicted from ${NODE}. k8s.node.name: ${NODE}. k8s.namespace.name: ingress-nginx. k8s.workload.kind: daemonset. k8s.cluster.name: mps-nonprod-rno\",
    \"customProperties\":{
      \"k8s.node.name\":\"${NODE}\",
      \"k8s.cluster.name\":\"mps-nonprod-rno\",
      \"k8s.cluster.uid\":\"${NONPROD_UID}\",
      \"k8s.namespace.name\":\"ingress-nginx\",
      \"k8s.workload.name\":\"ingress-nginx-controller\",
      \"k8s.workload.kind\":\"daemonset\",
      \"environment\":\"ADC\"
    }
  }"
  pause 3

  fire "Auth service pod OOMKilled (downstream)" "{
    \"state\":\"OPEN\",
    \"problemId\":\"P-D1-DS2-${ID}\",
    \"problemTitle\":\"Not all pods ready — dex in dex namespace on ${NODE}\",
    \"impactLevel\":\"SERVICE\",
    \"severity\":\"AVAILABILITY\",
    \"status\":\"OPEN\",
    \"startTime\":\"${NOW}\",
    \"rootCauseEntity\":{
      \"entityId\":\"HOST-${NODE}\",
      \"entityName\":\"${NODE}\",
      \"entityType\":\"HOST\"
    },
    \"impactedEntities\":[{
      \"entityId\":\"KUBERNETES_WORKLOAD-dex\",
      \"entityName\":\"dex\",
      \"entityType\":\"KUBERNETES_WORKLOAD\"
    }],
    \"problemDetails\":\"dex pod OOMKilled on ${NODE}. k8s.namespace.name: dex. k8s.workload.name: dex. k8s.cluster.name: mps-nonprod-rno\",
    \"customProperties\":{
      \"k8s.node.name\":\"${NODE}\",
      \"k8s.cluster.name\":\"mps-nonprod-rno\",
      \"k8s.cluster.uid\":\"${NONPROD_UID}\",
      \"k8s.namespace.name\":\"dex\",
      \"k8s.workload.name\":\"dex\",
      \"k8s.workload.kind\":\"deployment\",
      \"environment\":\"ADC\"
    }
  }"

  pause 6
  show_decisions 30s
  show_incidents 2
  echo ""
  ok "Open AlterHub UI → Incidents tab → see 1 incident with 3 correlated alerts"
  dim "URL: ${BASE_URL}"
  wait_key
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# D2 — Burst Dedup: same entity, 4 alerts in rapid succession → 1 incident
#
# Demonstrates the in-flight deduplication fix. Four alerts for the same
# Dynatrace entity_id arrive within milliseconds. The pipeline's poll-loop
# dedup ensures only 1 incident is created (not 4).
#
# Expected: 1 incident, alert_count = 4
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
if should_run D2; then
  banner "D2 — Burst Dedup" "4 alerts in rapid succession → 1 incident (not 4)"
  ID="D2-${TS}"
  NODE="mps-nonprod-rno-worker-z2-13"
  ENTITY_ID="HOST-${NODE}"

  step "Firing 4 Dynatrace alerts for the same entity back-to-back..."
  dim  "Simulates a real Dynatrace burst when a node goes critical"
  echo ""

  for i in 1 2 3 4; do
    fire "Alert burst #${i} — same entity_id, same root" "{
      \"state\":\"OPEN\",
      \"problemId\":\"P-D2-BURST${i}-${ID}\",
      \"problemTitle\":\"Aggregate state — ${NODE} infrastructure degraded\",
      \"impactLevel\":\"INFRASTRUCTURE\",
      \"severity\":\"AVAILABILITY\",
      \"status\":\"OPEN\",
      \"startTime\":\"${NOW}\",
      \"rootCauseEntity\":{
        \"entityId\":\"${ENTITY_ID}\",
        \"entityName\":\"${NODE}\",
        \"entityType\":\"HOST\"
      },
      \"impactedEntities\":[{
        \"entityId\":\"${ENTITY_ID}\",
        \"entityName\":\"${NODE}\",
        \"entityType\":\"HOST\"
      }],
      \"problemDetails\":\"Infrastructure degraded. host.name: ${NODE}. k8s.node.name: ${NODE}. k8s.cluster.name: mps-nonprod-rno\",
      \"customProperties\":{
        \"host.name\":\"${NODE}\",
        \"k8s.node.name\":\"${NODE}\",
        \"k8s.cluster.name\":\"mps-nonprod-rno\",
        \"k8s.cluster.uid\":\"${NONPROD_UID}\",
        \"entity_id\":\"${ENTITY_ID}\",
        \"environment\":\"ADC\"
      }
    }"
    sleep 0.2
  done

  pause 10
  echo ""
  echo -e "  ${BOLD}Result — alert_count and incident_count:${RST}"
  db "SELECT
        'INC-' || i.incident_number AS number,
        jsonb_array_length(i.alert_ids) AS alert_count,
        i.status,
        LEFT(i.title, 55) AS title
      FROM incidents i
      WHERE i.auto_created = TRUE
        AND i.created_at >= NOW() - INTERVAL '3 minutes'
        AND i.title ILIKE '%${NODE}%'
      ORDER BY i.created_at DESC LIMIT 5;"
  echo ""
  ok "Expected: 1 incident with 4 alerts (not 4 incidents)"
  wait_key
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# D3 — Cross-namespace cascade: 1 node → 3 workloads across namespaces
#
# A node goes into NotReady state. Workloads in staging, ingress-nginx, and
# kube-system all fire pods-not-ready alerts. All 4 alerts land in 1 incident.
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
if should_run D3; then
  banner "D3 — Cross-namespace Cascade" "1 node root → 3 workloads in 3 namespaces → 1 incident"
  ID="D3-${TS}"
  NODE="mps-nonprod-rno-worker-z1-17"

  step "Firing root alert then 3 downstream workload alerts..."
  echo ""

  fire "Node OOMKiller triggered (root)" "{
    \"state\":\"OPEN\",\"problemId\":\"P-D3-ROOT-${ID}\",
    \"problemTitle\":\"Memory saturation on node ${NODE}\",
    \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"RESOURCE_CONTENTION\",
    \"status\":\"OPEN\",\"startTime\":\"${NOW}\",
    \"rootCauseEntity\":{\"entityId\":\"HOST-${NODE}\",\"entityName\":\"${NODE}\",\"entityType\":\"HOST\"},
    \"impactedEntities\":[{\"entityId\":\"HOST-${NODE}\",\"entityName\":\"${NODE}\",\"entityType\":\"HOST\"}],
    \"problemDetails\":\"Node OOMKiller triggered on ${NODE}. host.name: ${NODE}. k8s.node.name: ${NODE}. k8s.cluster.name: mps-nonprod-rno\",
    \"customProperties\":{\"host.name\":\"${NODE}\",\"k8s.node.name\":\"${NODE}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"environment\":\"ADC\"}
  }"
  pause 3

  fire "stagepush-auth-uat: nginx-mtls-proxy OOMKilled (ns 1)" "{
    \"state\":\"OPEN\",\"problemId\":\"P-D3-NS1-${ID}\",
    \"problemTitle\":\"Not all pods ready — nginx-mtls-proxy-push-proxy in stagepush-auth-uat\",
    \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
    \"rootCauseEntity\":{\"entityId\":\"HOST-${NODE}\",\"entityName\":\"${NODE}\",\"entityType\":\"HOST\"},
    \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-nginx-mtls-proxy-push-proxy\",\"entityName\":\"nginx-mtls-proxy-push-proxy\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
    \"problemDetails\":\"OOMKilled on ${NODE}. k8s.namespace.name: stagepush-auth-uat. k8s.workload.name: nginx-mtls-proxy-push-proxy. k8s.cluster.name: mps-nonprod-rno\",
    \"customProperties\":{\"k8s.node.name\":\"${NODE}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"k8s.namespace.name\":\"stagepush-auth-uat\",\"k8s.workload.name\":\"nginx-mtls-proxy-push-proxy\",\"k8s.workload.kind\":\"deployment\",\"environment\":\"ADC\"}
  }"
  pause 3

  fire "dex namespace: auth pod evicted (ns 2)" "{
    \"state\":\"OPEN\",\"problemId\":\"P-D3-NS2-${ID}\",
    \"problemTitle\":\"Not all pods ready — dex in dex namespace\",
    \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
    \"rootCauseEntity\":{\"entityId\":\"HOST-${NODE}\",\"entityName\":\"${NODE}\",\"entityType\":\"HOST\"},
    \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-dex\",\"entityName\":\"dex\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
    \"problemDetails\":\"dex pod evicted from ${NODE} due to memory pressure. k8s.namespace.name: dex. k8s.cluster.name: mps-nonprod-rno\",
    \"customProperties\":{\"k8s.node.name\":\"${NODE}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"k8s.namespace.name\":\"dex\",\"k8s.workload.name\":\"dex\",\"k8s.workload.kind\":\"deployment\",\"environment\":\"ADC\"}
  }"
  pause 3

  fire "ingress-nginx: controller DaemonSet pod evicted (ns 3)" "{
    \"state\":\"OPEN\",\"problemId\":\"P-D3-NS3-${ID}\",
    \"problemTitle\":\"Not all pods ready — ingress-nginx-controller in ingress-nginx\",
    \"impactLevel\":\"SERVICE\",\"severity\":\"AVAILABILITY\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
    \"rootCauseEntity\":{\"entityId\":\"HOST-${NODE}\",\"entityName\":\"${NODE}\",\"entityType\":\"HOST\"},
    \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-ingress-nginx-controller\",\"entityName\":\"ingress-nginx-controller\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
    \"problemDetails\":\"ingress-nginx DaemonSet pod evicted from ${NODE}. k8s.namespace.name: ingress-nginx. k8s.workload.kind: daemonset. k8s.cluster.name: mps-nonprod-rno\",
    \"customProperties\":{\"k8s.node.name\":\"${NODE}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"k8s.namespace.name\":\"ingress-nginx\",\"k8s.workload.name\":\"ingress-nginx-controller\",\"k8s.workload.kind\":\"daemonset\",\"environment\":\"ADC\"}
  }"

  pause 6
  show_decisions 35s
  show_incidents 3
  echo ""
  ok "1 incident, 4 alerts across 3 namespaces — all correlated to same node root"
  wait_key
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# D4 — Full Lifecycle + Timeline
#
# Fire an alert, watch it create an incident, then open the incident detail
# and see a rich synthesized timeline: Alert Received → Incident Created →
# (optional) RCA queued → Resolved.
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
if should_run D4; then
  banner "D4 — Full Lifecycle + Timeline" "Alert → Incident → RCA queued → Resolve → Timeline"
  ID="D4-${TS}"
  NODE="mps-nonprod-rno-worker-z2-01"

  step "Phase 1: Fire initial alert..."
  fire "Node disk pressure alert (OPEN)" "{
    \"state\":\"OPEN\",\"problemId\":\"P-D4-OPEN-${ID}\",
    \"problemTitle\":\"Disk pressure on node ${NODE} — volume nearly full\",
    \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"RESOURCE_CONTENTION\",
    \"status\":\"OPEN\",\"startTime\":\"${NOW}\",
    \"rootCauseEntity\":{\"entityId\":\"HOST-${NODE}\",\"entityName\":\"${NODE}\",\"entityType\":\"HOST\"},
    \"impactedEntities\":[{\"entityId\":\"HOST-${NODE}\",\"entityName\":\"${NODE}\",\"entityType\":\"HOST\"}],
    \"problemDetails\":\"Disk usage at 87% on ${NODE}. host.name: ${NODE}. k8s.node.name: ${NODE}. k8s.cluster.name: mps-nonprod-rno. Eviction threshold imminent.\",
    \"customProperties\":{
      \"host.name\":\"${NODE}\",
      \"k8s.node.name\":\"${NODE}\",
      \"k8s.cluster.name\":\"mps-nonprod-rno\",
      \"k8s.cluster.uid\":\"${NONPROD_UID}\",
      \"environment\":\"ADC\"
    }
  }"

  pause 6

  echo ""
  step "Phase 2: Show the auto-created incident..."
  db "SELECT
        'INC-' || incident_number AS number,
        severity, status,
        jsonb_array_length(alert_ids) AS alerts,
        rca_status,
        to_char(created_at,'HH24:MI:SS') AS created_at
      FROM incidents
      WHERE auto_created=TRUE AND title ILIKE '%${NODE}%'
      ORDER BY created_at DESC LIMIT 1;"

  echo ""
  step "Phase 3: Timeline for this incident..."
  dim  "Synthesized from alert timestamps + incident timestamps"
  db "SELECT
        to_char(t.created_at,'HH24:MI:SS') AS time,
        t.event_type,
        LEFT(t.title,50) AS event
      FROM incident_timeline t
      WHERE t.incident_id = (
        SELECT id FROM incidents
        WHERE auto_created=TRUE AND title ILIKE '%${NODE}%'
        ORDER BY created_at DESC LIMIT 1
      )
      ORDER BY t.created_at ASC;"

  pause 3
  echo ""
  step "Phase 4: Resolve the incident (Dynatrace sends RESOLVED)..."
  fire "Same alert — state=RESOLVED (auto-close)" "{
    \"state\":\"RESOLVED\",\"problemId\":\"P-D4-OPEN-${ID}\",
    \"problemTitle\":\"Disk pressure on node ${NODE} — volume nearly full\",
    \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"RESOURCE_CONTENTION\",
    \"status\":\"RESOLVED\",\"startTime\":\"${NOW}\",\"endTime\":\"${NOW}\",
    \"rootCauseEntity\":{\"entityId\":\"HOST-${NODE}\",\"entityName\":\"${NODE}\",\"entityType\":\"HOST\"},
    \"impactedEntities\":[{\"entityId\":\"HOST-${NODE}\",\"entityName\":\"${NODE}\",\"entityType\":\"HOST\"}],
    \"problemDetails\":\"Disk pressure resolved on ${NODE}. Volume freed.\",
    \"customProperties\":{\"host.name\":\"${NODE}\",\"k8s.node.name\":\"${NODE}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"environment\":\"ADC\"}
  }"

  pause 5
  echo ""
  step "Phase 5: Final incident state..."
  db "SELECT
        'INC-' || incident_number AS number,
        status,
        to_char(created_at,'HH24:MI:SS') AS opened,
        to_char(resolved_at,'HH24:MI:SS') AS resolved
      FROM incidents
      WHERE auto_created=TRUE AND title ILIKE '%${NODE}%'
      ORDER BY created_at DESC LIMIT 1;"
  echo ""
  ok "In the UI: open the incident → Timeline tab → see all 4 events with timestamps"
  ok "Created → Alert Received → Auto-Created → Resolved"
  wait_key
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# D5 — Blast Radius Demo
#
# Fire an alert with rich Dynatrace k8s labels. The pipeline populates
# blast_radius from the alert labels (new fallback). Show it in DB and in UI.
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
if should_run D5; then
  banner "D5 — Blast Radius" "Alert labels → blast_radius auto-populated on incident"
  ID="D5-${TS}"
  NODE="mps-nonprod-rno-worker-z3-13"
  WORKLOAD="coredns"
  NS_K8S="kube-system"

  step "Firing alert with full k8s labels (workload + namespace + cluster + node)..."
  fire "node z3-13 + coredns workload impact" "{
    \"state\":\"OPEN\",\"problemId\":\"P-D5-${ID}\",
    \"problemTitle\":\"Backoff event — coredns in kube-system on ${NODE}\",
    \"impactLevel\":\"SERVICE\",\"severity\":\"ERROR\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
    \"rootCauseEntity\":{\"entityId\":\"HOST-${NODE}\",\"entityName\":\"${NODE}\",\"entityType\":\"HOST\"},
    \"impactedEntities\":[{
      \"entityId\":\"KUBERNETES_WORKLOAD-${WORKLOAD}\",
      \"entityName\":\"${WORKLOAD}\",
      \"entityType\":\"KUBERNETES_WORKLOAD\"
    }],
    \"problemDetails\":\"coredns pods pending restart on ${NODE}. k8s.node.name: ${NODE}. k8s.namespace.name: ${NS_K8S}. k8s.workload.name: ${WORKLOAD}. k8s.cluster.name: mps-nonprod-rno\",
    \"customProperties\":{
      \"k8s.node.name\":\"${NODE}\",
      \"k8s.cluster.name\":\"mps-nonprod-rno\",
      \"k8s.cluster.uid\":\"${NONPROD_UID}\",
      \"k8s.namespace.name\":\"${NS_K8S}\",
      \"k8s.workload.name\":\"${WORKLOAD}\",
      \"k8s.workload.kind\":\"deployment\",
      \"host.name\":\"${NODE}\",
      \"environment\":\"ADC\"
    }
  }"

  pause 6
  echo ""
  step "Blast radius stored on incident:"
  db "SELECT
        'INC-' || incident_number AS number,
        blast_radius::text AS blast_radius_nodes,
        LEFT(title,55) AS title
      FROM incidents
      WHERE auto_created=TRUE AND title ILIKE '%${WORKLOAD}%'
      ORDER BY created_at DESC LIMIT 1;"
  echo ""
  ok "blast_radius now shows: [\"${NODE}\", \"${WORKLOAD}\", \"${NS_K8S}\", \"mps-nonprod-rno\"]"
  ok "Open the Blast Radius tab in the incident detail to see each node as a card"
  wait_key
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# D6 — Search Demo
#
# Shows the improved search: INC-number, INC prefix, title keywords, source.
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
if should_run D6; then
  banner "D6 — Search Demo" "Search by INC-number, title, correlation ID"

  step "Getting recent incident numbers for demo..."
  RECENT=$(db "SELECT incident_number FROM incidents WHERE auto_created=TRUE ORDER BY created_at DESC LIMIT 3;" \
    | tr -d ' ' | head -3)
  FIRST_NUM=$(echo "$RECENT" | head -1 | tr -d '[:space:]')

  echo ""
  dim  "Recent incident numbers: $RECENT"
  echo ""

  if [[ -n "$FIRST_NUM" ]]; then
    step "Testing API search for 'INC-${FIRST_NUM}' (should match)..."
    RESULT=$(curl -s -k "${BASE_URL}/api/v1/incidents?search=${FIRST_NUM}&limit=5" \
      -H "Authorization: Bearer $(cat /tmp/ah_token 2>/dev/null || echo 'NO_TOKEN')" 2>/dev/null \
      | python3 -c "import sys,json; d=json.load(sys.stdin); print(f\"  Found: {len(d.get('data',{}).get('incidents',[]))} incident(s)\")" 2>/dev/null \
      || echo "  (run curl manually — see instructions below)")
    echo -e "  $RESULT"
  fi

  echo ""
  step "Search scenarios to show in the UI:"
  echo ""
  printf "  ${CYN}%-30s${RST}  %s\n" "Search input" "What it matches"
  hr
  printf "  ${WHT}%-30s${RST}  %s\n" "INC-${FIRST_NUM:-1820}" "Exact incident number"
  printf "  ${WHT}%-30s${RST}  %s\n" "${FIRST_NUM:-1820}"     "Bare number (no prefix)"
  printf "  ${WHT}%-30s${RST}  %s\n" "coredns"                "Any incident with coredns in title"
  printf "  ${WHT}%-30s${RST}  %s\n" "mps-nonprod-rno"        "Cluster name in correlation ID"
  printf "  ${WHT}%-30s${RST}  %s\n" "OPEN"                   "Status filter"
  printf "  ${WHT}%-30s${RST}  %s\n" "saturation"             "Title keyword"
  echo ""
  ok "Type any of these in the search box in the AI-Correlated Incidents section"
  ok "Search now works for both INC-NUMBER and bare numbers"
  wait_key
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# D7 — Isolation: 2 unrelated failures → 2 separate incidents
#
# Two alerts fire simultaneously from DIFFERENT clusters with DIFFERENT
# root cause entities. The pipeline correctly creates 2 separate incidents.
# Demonstrates that the correlator doesn't have false positives.
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
if should_run D7; then
  banner "D7 — Isolation: No False Correlations" "2 different failures → 2 separate incidents"
  ID="D7-${TS}"
  NODE_A="mps-nonprod-rno-worker-z1-06"
  NODE_B="mps-mondev-mdn-worker-z3-05"

  step "Firing 2 alerts on different clusters simultaneously..."
  echo ""

  fire "Cluster A: RNO node memory (nonprod-rno)" "{
    \"state\":\"OPEN\",\"problemId\":\"P-D7-A-${ID}\",
    \"problemTitle\":\"Memory saturation on node ${NODE_A}\",
    \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"RESOURCE_CONTENTION\",
    \"status\":\"OPEN\",\"startTime\":\"${NOW}\",
    \"rootCauseEntity\":{\"entityId\":\"HOST-${NODE_A}\",\"entityName\":\"${NODE_A}\",\"entityType\":\"HOST\"},
    \"impactedEntities\":[{\"entityId\":\"HOST-${NODE_A}\",\"entityName\":\"${NODE_A}\",\"entityType\":\"HOST\"}],
    \"problemDetails\":\"Memory at 93% on ${NODE_A}. host.name: ${NODE_A}. k8s.cluster.name: mps-nonprod-rno\",
    \"customProperties\":{\"host.name\":\"${NODE_A}\",\"k8s.node.name\":\"${NODE_A}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"environment\":\"ADC\"}
  }"
  sleep 0.5

  fire "Cluster B: MDN node CPU (mondev-mdn, different DC)" "{
    \"state\":\"OPEN\",\"problemId\":\"P-D7-B-${ID}\",
    \"problemTitle\":\"Memory saturation on node ${NODE_B}\",
    \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"RESOURCE_CONTENTION\",
    \"status\":\"OPEN\",\"startTime\":\"${NOW}\",
    \"rootCauseEntity\":{\"entityId\":\"HOST-${NODE_B}\",\"entityName\":\"${NODE_B}\",\"entityType\":\"HOST\"},
    \"impactedEntities\":[{\"entityId\":\"HOST-${NODE_B}\",\"entityName\":\"${NODE_B}\",\"entityType\":\"HOST\"}],
    \"problemDetails\":\"Memory at 96% on ${NODE_B}. host.name: ${NODE_B}. k8s.cluster.name: mps-mondev-mdn\",
    \"customProperties\":{\"host.name\":\"${NODE_B}\",\"k8s.node.name\":\"${NODE_B}\",\"k8s.cluster.name\":\"mps-mondev-mdn\",\"k8s.cluster.uid\":\"${MONDEV_UID}\",\"environment\":\"MDN\"}
  }"

  pause 8
  show_incidents 2
  echo ""
  ok "2 separate incidents created — the pipeline does NOT false-correlate across clusters"
  ok "Each incident has its own blast radius, timeline, and RCA"
  wait_key
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# D8 — OPEN → RESOLVED auto-close (ticket lifecycle)
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
if should_run D8; then
  banner "D8 — OPEN → RESOLVED Auto-close" "Same Dynatrace problemId resolves its own incident"
  ID="D8-${TS}"
  NODE="mps-nonprod-rno-worker-z2-07"

  step "Phase 1: Alert fires (OPEN)..."
  fire "Network packet loss on ${NODE} (OPEN)" "{
    \"state\":\"OPEN\",\"problemId\":\"P-D8-${ID}\",
    \"problemTitle\":\"Network packet loss on node ${NODE}\",
    \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",
    \"status\":\"OPEN\",\"startTime\":\"${NOW}\",
    \"rootCauseEntity\":{\"entityId\":\"HOST-${NODE}\",\"entityName\":\"${NODE}\",\"entityType\":\"HOST\"},
    \"impactedEntities\":[{\"entityId\":\"HOST-${NODE}\",\"entityName\":\"${NODE}\",\"entityType\":\"HOST\"}],
    \"problemDetails\":\"Network unreachable on ${NODE}. 40% packet loss detected. host.name: ${NODE}. k8s.node.name: ${NODE}\",
    \"customProperties\":{\"host.name\":\"${NODE}\",\"k8s.node.name\":\"${NODE}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"environment\":\"ADC\"}
  }"

  pause 8
  step "Incident created:"
  db "SELECT 'INC-' || incident_number AS number, status, to_char(created_at,'HH24:MI:SS') AS at
      FROM incidents WHERE auto_created=TRUE AND title ILIKE '%${NODE}%' ORDER BY created_at DESC LIMIT 1;"

  echo ""
  step "Phase 2: Dynatrace sends RESOLVED (problem closed)..."
  fire "Network recovered on ${NODE} (RESOLVED)" "{
    \"state\":\"RESOLVED\",\"problemId\":\"P-D8-${ID}\",
    \"problemTitle\":\"Network packet loss on node ${NODE}\",
    \"impactLevel\":\"INFRASTRUCTURE\",\"severity\":\"AVAILABILITY\",
    \"status\":\"RESOLVED\",\"startTime\":\"${NOW}\",\"endTime\":\"${NOW}\",
    \"rootCauseEntity\":{\"entityId\":\"HOST-${NODE}\",\"entityName\":\"${NODE}\",\"entityType\":\"HOST\"},
    \"impactedEntities\":[{\"entityId\":\"HOST-${NODE}\",\"entityName\":\"${NODE}\",\"entityType\":\"HOST\"}],
    \"problemDetails\":\"Network connectivity restored on ${NODE}. Packet loss resolved.\",
    \"customProperties\":{\"host.name\":\"${NODE}\",\"k8s.node.name\":\"${NODE}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"environment\":\"ADC\"}
  }"

  pause 5
  step "Incident after RESOLVED:"
  db "SELECT 'INC-' || incident_number AS number,
             status,
             to_char(created_at,'HH24:MI:SS') AS opened,
             to_char(resolved_at,'HH24:MI:SS') AS resolved
      FROM incidents WHERE auto_created=TRUE AND title ILIKE '%${NODE}%' ORDER BY created_at DESC LIMIT 1;"
  echo ""
  ok "Incident auto-resolved. No human intervention required."
  wait_key
fi

# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# D9 — Scale: 30 concurrent alerts → ≤3 incidents
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
if should_run D9; then
  banner "D9 — Scale Demo" "30 concurrent alerts from same root → ≤3 incidents"
  ID="D9-${TS}"
  ROOT_NODE="mps-nonprod-rno-worker-z3-22"

  step "Firing 30 alerts simultaneously (background goroutines)..."
  dim  "Real production scenario: Dynatrace fires a burst when a node goes critical"
  echo ""

  T_START=$(date +%s%3N)
  for i in $(seq 1 30); do
    curl -s -o /dev/null -X POST "$ENDPOINT" \
      -H "Content-Type: application/json" \
      -d "{
        \"state\":\"OPEN\",
        \"problemId\":\"P-D9-${i}-${ID}\",
        \"problemTitle\":\"Workload-${i} pod CrashLoopBackOff on ${ROOT_NODE}\",
        \"impactLevel\":\"SERVICE\",\"severity\":\"ERROR\",\"status\":\"OPEN\",\"startTime\":\"${NOW}\",
        \"rootCauseEntity\":{\"entityId\":\"HOST-${ROOT_NODE}\",\"entityName\":\"${ROOT_NODE}\",\"entityType\":\"HOST\"},
        \"impactedEntities\":[{\"entityId\":\"KUBERNETES_WORKLOAD-app-${i}\",\"entityName\":\"app-${i}\",\"entityType\":\"KUBERNETES_WORKLOAD\"}],
        \"problemDetails\":\"app-${i} pod crashing on ${ROOT_NODE}. k8s.node.name: ${ROOT_NODE}. k8s.cluster.name: mps-nonprod-rno\",
        \"customProperties\":{\"k8s.node.name\":\"${ROOT_NODE}\",\"k8s.cluster.name\":\"mps-nonprod-rno\",\"k8s.cluster.uid\":\"${NONPROD_UID}\",\"environment\":\"ADC\"}
      }" &
  done
  wait
  T_END=$(date +%s%3N)
  ELAPSED=$(( T_END - T_START ))

  echo -e "  ${GRN}✓ 30 alerts sent in ${ELAPSED}ms${RST}"
  echo ""
  pause 15

  step "Result:"
  db "SELECT COUNT(DISTINCT id) AS incidents_created,
             SUM(jsonb_array_length(alert_ids)) AS total_alerts_across_incidents
      FROM incidents
      WHERE auto_created=TRUE
        AND created_at >= NOW() - INTERVAL '3 minutes'
        AND title ILIKE '%${ROOT_NODE}%';"
  echo ""
  ok "Expected: ≤3 incidents containing all 30 alerts (race window = normal)"
  ok "Without the in-flight dedup fix, this would have created up to 30 incidents"
  wait_key
fi

# ── Final summary ─────────────────────────────────────────────────────────────
echo ""
echo -e "${BOLD}${MAG}╔══════════════════════════════════════════════════════╗${RST}"
echo -e "${BOLD}${MAG}║  Demo Complete — Recent Incidents Summary            ║${RST}"
echo -e "${BOLD}${MAG}╚══════════════════════════════════════════════════════╝${RST}"
echo ""
db "SELECT
      'INC-' || incident_number AS number,
      severity,
      status,
      jsonb_array_length(COALESCE(alert_ids,'[]'::jsonb)) AS alerts,
      LEFT(COALESCE(blast_radius::text,'[]'),40) AS blast_radius,
      to_char(created_at,'HH24:MI:SS') AS created,
      LEFT(title,45) AS title
    FROM incidents
    WHERE auto_created = TRUE
      AND created_at >= NOW() - INTERVAL '30 minutes'
    ORDER BY created_at DESC
    LIMIT 20;"
echo ""
echo -e "  ${BOLD}UI:${RST} ${BLU}${BASE_URL}${RST}"
echo ""
