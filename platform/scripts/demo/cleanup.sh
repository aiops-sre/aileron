#!/usr/bin/env bash
# cleanup.sh — remove auto-created demo incidents and their alerts
#
# Usage:
#   bash scripts/demo/cleanup.sh              # delete all auto-created incidents
#   bash scripts/demo/cleanup.sh --dry-run    # show what would be deleted
#   bash scripts/demo/cleanup.sh --hours 1    # only delete incidents from last N hours

set -uo pipefail

DRY=${DRY:-0}
HOURS=24
NS=aileron

BOLD="\033[1m"; GRN="\033[32m"; YLW="\033[33m"; RED="\033[31m"; RST="\033[0m"

for arg in "$@"; do
  case $arg in
    --dry-run) DRY=1 ;;
    --hours)   shift; HOURS="$1" ;;
  esac
done

db() {
  local pod
  pod=$(kubectl get pods -n "$NS" --no-headers 2>/dev/null \
    | grep "alerthub-backend.*Running" | head -1 | awk '{print $1}')
  [[ -z "$pod" ]] && echo "(db unavailable)" && return
  kubectl exec -n "$NS" "$pod" -- \
    psql "postgresql://alerthub:pg-AIOps-Secure-2024-Prod@postgres-primary.${NS}.svc.cluster.local:5432/alerthub" \
    -t -c "$1" 2>/dev/null | grep -v "^$"
}

echo -e "${BOLD}AlertHub Demo Cleanup${RST}"
echo ""

echo "Incidents that would be removed (auto_created, last ${HOURS}h):"
db "SELECT 'INC-' || incident_number || ' [' || severity || '] ' || LEFT(title,50) AS incident
    FROM incidents
    WHERE auto_created = TRUE
      AND created_at >= NOW() - INTERVAL '${HOURS} hours'
    ORDER BY created_at DESC LIMIT 50;" | head -30

COUNT=$(db "SELECT COUNT(*) FROM incidents WHERE auto_created=TRUE AND created_at >= NOW() - INTERVAL '${HOURS} hours';" | tr -d ' \n')
echo ""
echo -e "  ${YLW}Total: ${COUNT} incidents to remove${RST}"
echo ""

if [[ "$DRY" == "1" ]]; then
  echo -e "  ${YLW}(dry run — nothing deleted)${RST}"
  exit 0
fi

printf "  ${RED}Delete these ${COUNT} incidents and their alerts? [y/N] ${RST}"
read -r CONFIRM
[[ "$CONFIRM" != "y" && "$CONFIRM" != "Y" ]] && echo "Aborted." && exit 0

echo ""
echo "Deleting incident_timeline rows..."
db "DELETE FROM incident_timeline WHERE incident_id IN (
      SELECT id FROM incidents WHERE auto_created=TRUE AND created_at >= NOW() - INTERVAL '${HOURS} hours'
    );"

echo "Unlinking alerts from incidents..."
db "UPDATE alerts SET incident_id = NULL WHERE incident_id IN (
      SELECT id FROM incidents WHERE auto_created=TRUE AND created_at >= NOW() - INTERVAL '${HOURS} hours'
    );"

echo "Deleting demo alerts (source_id starts with P-D)..."
db "DELETE FROM alerts WHERE source_id ~ '^P-D[0-9]+' AND created_at >= NOW() - INTERVAL '${HOURS} hours';"

echo "Deleting incidents..."
DELETED=$(db "DELETE FROM incidents WHERE auto_created=TRUE AND created_at >= NOW() - INTERVAL '${HOURS} hours' RETURNING id;" | wc -l | tr -d ' ')
echo ""
echo -e "  ${GRN}✓ Deleted ${DELETED} incidents${RST}"
echo ""
