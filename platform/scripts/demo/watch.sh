#!/usr/bin/env bash
# watch.sh — live-watch the pipeline while demos run
#
# Run this in a second terminal pane to see real-time correlation decisions.
#
# Usage:
#   bash scripts/demo/watch.sh          # watch pipeline logs
#   bash scripts/demo/watch.sh db       # live DB query every 3s
#   bash scripts/demo/watch.sh both     # two tmux panes (if tmux available)

MODE="${1:-logs}"
NS=aileron

db_watch() {
  while true; do
    clear
    printf "\033[1m  AlertHub — Live Incidents (auto-refresh 3s)\033[0m\n\n"
    pod=$(kubectl get pods -n "$NS" --no-headers 2>/dev/null \
      | grep "alerthub-backend.*Running" | head -1 | awk '{print $1}')
    if [[ -n "$pod" ]]; then
      kubectl exec -n "$NS" "$pod" -- \
        psql "postgresql://alerthub:pg-AIOps-Secure-2024-Prod@postgres-primary.${NS}.svc.cluster.local:5432/alerthub" \
        -c "SELECT
              'INC-' || incident_number AS number,
              severity,
              status,
              jsonb_array_length(COALESCE(alert_ids,'[]'::jsonb)) AS alerts,
              rca_status,
              to_char(created_at,'HH24:MI:SS') AS created,
              LEFT(COALESCE(blast_radius::text,'no blast radius'),35) AS blast_radius,
              LEFT(title,50) AS title
            FROM incidents
            WHERE auto_created = TRUE
              AND created_at >= NOW() - INTERVAL '30 minutes'
            ORDER BY created_at DESC LIMIT 15;" 2>/dev/null
    fi
    sleep 3
  done
}

case "$MODE" in
  db)
    db_watch
    ;;
  both)
    if command -v tmux &>/dev/null; then
      tmux new-session -d -s ahdemo 2>/dev/null || true
      tmux split-window -h -t ahdemo
      tmux send-keys -t ahdemo:0.0 "bash $(realpath "$0") logs" Enter
      tmux send-keys -t ahdemo:0.1 "bash $(realpath "$0") db" Enter
      tmux attach -t ahdemo
    else
      echo "tmux not available — run in two separate terminals:"
      echo "  Terminal 1: bash scripts/demo/watch.sh logs"
      echo "  Terminal 2: bash scripts/demo/watch.sh db"
    fi
    ;;
  logs|*)
    kubectl logs -n "$NS" -l app=alerthub-backend -f 2>/dev/null \
      | grep --line-buffered -E \
          "🎯 RCE|✅ Created incident|🔗 Alert .* merged|🔄 Updating|in-flight|entity_id dedup|correlation_id|❌ Failed|📥 Dynatrace|🆕 Creating"
    ;;
esac
