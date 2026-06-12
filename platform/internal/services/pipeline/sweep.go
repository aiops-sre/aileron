package pipeline

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"

	"github.com/aileron-platform/aileron/platform/internal/shared/metrics"
)

func (s *AlertPipelineService) runStaleSweep(ctx context.Context) {
	if s.db == nil {
		return
	}
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	// Run immediately on startup so stuck investigating incidents are reset quickly.
	s.sweepStaleAlerts(ctx)
	s.sweepStaleIncidents(ctx)
	s.sweepStuckRCAInvestigations(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweepStaleAlerts(ctx)
			s.sweepStaleIncidents(ctx)
			s.sweepStuckRCAInvestigations(ctx)
		}
	}
}

// sweepStaleAlerts marks open alerts as resolved when they have not been updated
// in more than 24 hours and then triggers incident auto-resolution.
// Fingerprint alerts (source_id LIKE 'fp:%') use a shorter 4h window keyed on
// created_at — their updated_at is bumped on every Kafka replay so the 24h guard
// never fires, causing them to stay open indefinitely and corrupt incident groupings.
func (s *AlertPipelineService) sweepStaleAlerts(ctx context.Context) {
	// Phase 1: fp: alerts older than 4h (by created_at, not updated_at).
	fpRows, err := s.db.QueryContext(ctx, `
		SELECT id, incident_id
		FROM alerts
		WHERE status = 'open'
		  AND source_id LIKE 'fp:%'
		  AND created_at < NOW() - INTERVAL '4 hours'
		LIMIT 200
	`)
	if err != nil {
		log.Printf("[stale-sweep] query fp alerts: %v", err)
	} else {
		defer fpRows.Close()
		type row struct {
			alertID    uuid.UUID
			incidentID uuid.NullUUID
		}
		var fpStale []row
		for fpRows.Next() {
			var r row
			if err := fpRows.Scan(&r.alertID, &r.incidentID); err == nil {
				fpStale = append(fpStale, r)
			}
		}
		if len(fpStale) > 0 {
			log.Printf("[stale-sweep] auto-resolving %d stale fp: alerts (open > 4h)", len(fpStale))
			for _, r := range fpStale {
				if _, err := s.db.ExecContext(ctx,
					`UPDATE alerts SET status='resolved', resolved_at=NOW(), updated_at=NOW(),
					 resolution_notes='Auto-resolved by stale-sweep: fingerprint alert open > 4h without DT RESOLVED'
					 WHERE id=$1`, r.alertID,
				); err != nil {
					log.Printf("[stale-sweep] resolve fp alert=%s: %v", r.alertID, err)
					continue
				}
				log.Printf("[stale-sweep] fp: alert %s auto-resolved (stale)", r.alertID)
				metrics.AlertsAutoResolved.Inc()
				if r.incidentID.Valid && r.incidentID.UUID != uuid.Nil {
					s.HandleAlertResolved(ctx, r.alertID, r.incidentID.UUID)
				}
			}
		}
	}

	// Phase 2: all other open alerts with no update in 24h.
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, incident_id
		FROM alerts
		WHERE status = 'open'
		  AND source_id NOT LIKE 'fp:%'
		  AND updated_at < NOW() - INTERVAL '24 hours'
		LIMIT 200
	`)
	if err != nil {
		log.Printf("[stale-sweep] query alerts: %v", err)
		return
	}
	defer rows.Close()

	type row struct {
		alertID    uuid.UUID
		incidentID uuid.NullUUID
	}
	var stale []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.alertID, &r.incidentID); err == nil {
			stale = append(stale, r)
		}
	}
	if len(stale) == 0 {
		return
	}
	log.Printf("[stale-sweep] auto-resolving %d stale open alerts (no update > 24h)", len(stale))
	for _, r := range stale {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE alerts SET status='resolved', resolved_at=NOW(), updated_at=NOW(),
			 resolution_notes='Auto-resolved by stale-sweep: no update in 24h'
			 WHERE id=$1`, r.alertID,
		); err != nil {
			log.Printf("[stale-sweep] resolve alert=%s: %v", r.alertID, err)
			continue
		}
		log.Printf("[stale-sweep] alert %s auto-resolved (stale)", r.alertID)
		metrics.AlertsAutoResolved.Inc()
		if r.incidentID.Valid && r.incidentID.UUID != uuid.Nil {
			s.HandleAlertResolved(ctx, r.alertID, r.incidentID.UUID)
		}
	}
}

// sweepStaleIncidents resolves incidents that have no open alerts but are still
// marked open — this catches any HandleAlertResolved call that was lost.
func (s *AlertPipelineService) sweepStaleIncidents(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT i.id FROM incidents i
		WHERE i.status IN ('open','investigating')
		  AND i.created_at < NOW() - INTERVAL '1 hour'
		  AND NOT EXISTS (
		    SELECT 1 FROM alerts a
		    WHERE a.incident_id = i.id AND a.status != 'resolved'
		  )
		  AND NOT EXISTS (
		    SELECT 1 FROM alerts a
		    WHERE a.id = ANY(
		        SELECT jsonb_array_elements_text(COALESCE(i.alert_ids,'[]'::jsonb))::uuid
		    ) AND a.status != 'resolved'
		  )
		  AND jsonb_array_length(COALESCE(i.alert_ids,'[]'::jsonb)) > 0
		LIMIT 100
	`)
	if err != nil {
		log.Printf("[stale-sweep] query incidents: %v", err)
		return
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return
	}
	log.Printf("[stale-sweep] auto-resolving %d incidents with all alerts resolved", len(ids))
	for _, id := range ids {
		if _, err := s.db.ExecContext(ctx, `
			UPDATE incidents
			SET status='resolved', resolved_at=NOW(), updated_at=NOW(),
			    resolution_notes='Auto-resolved by stale-sweep: all alerts resolved'
			WHERE id=$1 AND status IN ('open','investigating','acknowledged')
		`, id); err != nil {
			log.Printf("[stale-sweep] resolve incident=%s: %v", id, err)
			continue
		}
		log.Printf("[stale-sweep] incident %s auto-resolved (all alerts resolved)", id)
		metrics.IncidentsAutoResolved.WithLabelValues("stale_sweep").Inc()
		if s.incidentSvc != nil {
			s.incidentSvc.AddTimelineEvent(ctx, id, uuid.Nil, "auto_resolved",
				"Incident Auto-Resolved (Sweep)",
				"All correlated alerts have been resolved. Incident closed by stale sweep.", nil)
		}
	}
}

// sweepStuckRCAInvestigations handles two cases for incidents in rca_status='investigating'
// that have not received an OIE callback within 15 minutes:
//
//   1. rca_confidence > 0.5: CACIE already found a confident root cause.
//      The OIE never confirmed it, but the result is good — mark 'completed'.
//
//   2. rca_confidence <= 0.5: No confident result from either CACIE or OIE.
//      Mark 'failed' and replace stale placeholder text with a clear error message.
func (s *AlertPipelineService) sweepStuckRCAInvestigations(ctx context.Context) {
	if s.db == nil {
		return
	}
	// Case 1: CACIE found a confident result — promote to completed.
	r1, err := s.db.ExecContext(ctx, `
		UPDATE incidents
		SET rca_status = 'completed',
		    updated_at = NOW()
		WHERE rca_status = 'investigating'
		  AND rca_confidence > 0.5
		  AND ai_root_cause IS NOT NULL
		  AND ai_root_cause NOT LIKE 'RCA in progress%'
		  AND ai_root_cause NOT LIKE '%OIE evidence%'
		  AND ai_root_cause NOT LIKE '%investigating root cause%'
		  AND updated_at < NOW() - INTERVAL '15 minutes'
	`)
	if err != nil {
		log.Printf("[stale-sweep] stuck-rca promote: %v", err)
	} else if n, _ := r1.RowsAffected(); n > 0 {
		log.Printf("[stale-sweep] promoted %d incident(s) from investigating to completed (CACIE confidence > 0.5)", n)
	}

	// Case 2: No confident result — mark failed, clear stale placeholder.
	r2, err := s.db.ExecContext(ctx, `
		UPDATE incidents
		SET rca_status    = 'failed',
		    ai_root_cause = CASE
		        WHEN ai_root_cause LIKE 'RCA in progress%'
		          OR ai_root_cause LIKE '%OIE evidence gathering%'
		          OR ai_root_cause LIKE '%investigating root cause%'
		          OR ai_root_cause LIKE 'Preliminary:%OIE running%'
		        THEN 'OIE investigation did not complete — insufficient evidence to determine root cause.'
		        ELSE ai_root_cause
		    END,
		    updated_at = NOW()
		WHERE rca_status = 'investigating'
		  AND (rca_confidence IS NULL OR rca_confidence <= 0.5)
		  AND updated_at < NOW() - INTERVAL '15 minutes'
	`)
	if err != nil {
		log.Printf("[stale-sweep] stuck-rca fail: %v", err)
		return
	}
	if n, _ := r2.RowsAffected(); n > 0 {
		log.Printf("[stale-sweep] marked %d incident(s) failed (no confident RCA in 15min)", n)
		metrics.IncidentsAutoResolved.WithLabelValues("stuck_rca_reset").Inc()
	}
}
