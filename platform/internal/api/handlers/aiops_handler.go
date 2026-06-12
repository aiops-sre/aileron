package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AIOpsHandler serves the AIOps dashboard APIs — pipeline results, stats, service health.
type AIOpsHandler struct {
	db *sql.DB
}

func NewAIOpsHandler(db *sql.DB) *AIOpsHandler {
	return &AIOpsHandler{db: db}
}

// PipelineResult is the per-decision row returned to the frontend.
type PipelineResult struct {
	ID               string          `json:"id"`
	AlertID          string          `json:"alert_id"`
	IncidentID       *string         `json:"incident_id,omitempty"`
	AlertTitle       string          `json:"alert_title"`
	AlertSource      string          `json:"alert_source"`
	AlertSeverity    string          `json:"alert_severity"`
	AlertStatus      string          `json:"alert_status"`
	AlertDescription string          `json:"alert_description,omitempty"`
	Cluster          string          `json:"cluster,omitempty"`
	Namespace        string          `json:"namespace,omitempty"`
	Workload         string          `json:"workload,omitempty"`
	IncidentNumber   string          `json:"incident_number,omitempty"`
	Decision         string          `json:"decision"`
	FinalScore       float64         `json:"final_score"`
	DominantStrategy string          `json:"dominant_strategy"`
	SemanticScore    float64         `json:"semantic_score"`
	TemporalScore    float64         `json:"temporal_score"`
	TopologyScore    float64         `json:"topology_score"`
	RulesScore       float64         `json:"rules_score"`
	Reasoning        string          `json:"reasoning"`
	AIRootCause      string          `json:"ai_root_cause"`
	MatchedNodeLabel string          `json:"matched_node_label"`
	RootCauseLabel   string          `json:"root_cause_label"`
	ElapsedMs        int64           `json:"elapsed_ms"`
	ProcessedAt      string          `json:"processed_at"`
	// V2 fields
	Domain           string          `json:"domain,omitempty"`
	OntologyClass    string          `json:"ontology_class,omitempty"`
	TopoRootEntity   string          `json:"topo_root_entity,omitempty"`
	BlastRadiusCount int             `json:"blast_radius_count,omitempty"`
	RCAHypotheses    json.RawMessage `json:"rca_hypotheses,omitempty"`
	ExplanationJSON  json.RawMessage `json:"explanation_json,omitempty"`
}

// GetPipelineResults returns recent correlation decisions with per-row strategy scores.
// GET /api/v1/correlation/pipeline/results
func (h *AIOpsHandler) GetPipelineResults(c *gin.Context) {
	limit := 100
	if l := c.Query("limit"); l != "" {
		if n, _ := strconv.Atoi(l); n > 0 && n <= 500 {
			limit = n
		}
	}
	hours := 24
	if hh := c.Query("hours"); hh != "" {
		if n, _ := strconv.Atoi(hh); n > 0 {
			hours = n
		}
	}
	decision := c.Query("decision")
	source := c.Query("source")

	query := `
		SELECT pcr.id, pcr.alert_id, pcr.incident_id, pcr.alert_title, pcr.alert_source, pcr.alert_severity,
		       pcr.decision, pcr.final_score, pcr.dominant_strategy,
		       pcr.semantic_score, pcr.temporal_score, pcr.topology_score, pcr.rules_score,
		       pcr.reasoning, pcr.ai_root_cause, pcr.matched_node_label, pcr.root_cause_label,
		       pcr.elapsed_ms, pcr.processed_at,
		       COALESCE(a.description, '')          AS alert_description,
		       COALESCE(
		           NULLIF(a.labels->>'cluster', ''),
		           NULLIF(a.labels->>'k8s.cluster.name', ''),
		           NULLIF((regexp_match(a.description, 'k8s\.cluster\.name:\s*(\S+)'))[1], ''),
		           ''
		       )                                    AS cluster,
		       COALESCE(
		           NULLIF(a.labels->>'namespace', ''),
		           NULLIF(a.labels->>'k8s.namespace.name', ''),
		           NULLIF((regexp_match(a.description, 'k8s\.namespace\.name:\s*(\S+)'))[1], ''),
		           ''
		       )                                    AS namespace,
		       COALESCE(
		           NULLIF(a.labels->>'deployment', ''),
		           NULLIF(a.labels->>'app', ''),
		           NULLIF(a.labels->>'workload', ''),
		           NULLIF(a.labels->>'k8s.workload.name', ''),
		           NULLIF((regexp_match(a.description, 'k8s\.workload\.name:\s*(\S+)'))[1], ''),
		           ''
		       )                                    AS workload,
		       COALESCE(i.incident_number, '')       AS incident_number,
		       COALESCE(a.status, 'unknown')          AS alert_status,
		       COALESCE(pcr.domain, '')               AS domain,
		       COALESCE(pcr.ontology_class, '')        AS ontology_class,
		       COALESCE(pcr.topo_root_entity, '')      AS topo_root_entity,
		       COALESCE(pcr.blast_radius_count, 0)     AS blast_radius_count,
		       pcr.rca_hypotheses,
		       pcr.explanation_json
		FROM pipeline_correlation_results pcr
		LEFT JOIN alerts a ON pcr.alert_id = a.id
		LEFT JOIN incidents i ON pcr.incident_id = i.id
		WHERE pcr.processed_at >= NOW() - ($1 * INTERVAL '1 hour')
	`
	args := []interface{}{hours}
	idx := 2
	if decision != "" {
		query += fmt.Sprintf(" AND pcr.decision = $%d", idx)
		args = append(args, decision)
		idx++
	}
	if source != "" {
		query += fmt.Sprintf(" AND pcr.alert_source ILIKE $%d", idx)
		args = append(args, "%"+source+"%")
		idx++
	}
	query += fmt.Sprintf(" ORDER BY pcr.processed_at DESC LIMIT $%d", idx)
	args = append(args, limit)

	rows, err := h.db.QueryContext(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	defer rows.Close()

	results := make([]PipelineResult, 0)
	for rows.Next() {
		var r PipelineResult
		var incidentID sql.NullString
		var processedAt sql.NullTime
		var rcaHypotheses, explanationJSON []byte
		if err := rows.Scan(
			&r.ID, &r.AlertID, &incidentID, &r.AlertTitle, &r.AlertSource, &r.AlertSeverity,
			&r.Decision, &r.FinalScore, &r.DominantStrategy,
			&r.SemanticScore, &r.TemporalScore, &r.TopologyScore, &r.RulesScore,
			&r.Reasoning, &r.AIRootCause, &r.MatchedNodeLabel, &r.RootCauseLabel,
			&r.ElapsedMs, &processedAt,
			&r.AlertDescription, &r.Cluster, &r.Namespace, &r.Workload, &r.IncidentNumber,
			&r.AlertStatus,
			&r.Domain, &r.OntologyClass, &r.TopoRootEntity, &r.BlastRadiusCount,
			&rcaHypotheses, &explanationJSON,
		); err != nil {
			continue
		}
		if incidentID.Valid {
			r.IncidentID = &incidentID.String
		}
		if processedAt.Valid {
			r.ProcessedAt = processedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
		}
		if len(rcaHypotheses) > 0 && string(rcaHypotheses) != "null" {
			r.RCAHypotheses = rcaHypotheses
		}
		if len(explanationJSON) > 0 && string(explanationJSON) != "null" {
			r.ExplanationJSON = explanationJSON
		}
		results = append(results, r)
	}

	// Stats aggregation
	statsRow := h.db.QueryRowContext(c.Request.Context(), `
		SELECT
			COUNT(*) as total,
			SUM(CASE WHEN decision='create_incident' THEN 1 ELSE 0 END) as created,
			SUM(CASE WHEN decision='merge_incident'  THEN 1 ELSE 0 END) as merged,
			SUM(CASE WHEN decision='monitor'         THEN 1 ELSE 0 END) as monitored,
			SUM(CASE WHEN decision='discard'         THEN 1 ELSE 0 END) as discarded,
			COALESCE(ROUND(AVG(final_score)::numeric, 3), 0)            as avg_score,
			COALESCE(ROUND(AVG(elapsed_ms)::numeric, 0), 0)             as avg_elapsed_ms
		FROM pipeline_correlation_results
		WHERE processed_at >= NOW() - ($1 * INTERVAL '1 hour')
	`, hours)
	var total, created, merged, monitored, discarded int
	var avgScore, avgElapsedMs float64
	statsRow.Scan(&total, &created, &merged, &monitored, &discarded, &avgScore, &avgElapsedMs)

	// Strategy dominance
	stratRows, _ := h.db.QueryContext(c.Request.Context(), `
		SELECT dominant_strategy, COUNT(*) AS cnt
		FROM pipeline_correlation_results
		WHERE processed_at >= NOW() - ($1 * INTERVAL '1 hour')
		  AND dominant_strategy != ''
		GROUP BY dominant_strategy ORDER BY cnt DESC
	`, hours)
	byStrategy := map[string]int{}
	if stratRows != nil {
		defer stratRows.Close()
		for stratRows.Next() {
			var s string
			var n int
			stratRows.Scan(&s, &n)
			byStrategy[s] = n
		}
	}

	// Source breakdown
	srcRows, _ := h.db.QueryContext(c.Request.Context(), `
		SELECT alert_source, COUNT(*) AS cnt
		FROM pipeline_correlation_results
		WHERE processed_at >= NOW() - ($1 * INTERVAL '1 hour')
		GROUP BY alert_source ORDER BY cnt DESC LIMIT 20
	`, hours)
	bySource := map[string]int{}
	if srcRows != nil {
		defer srcRows.Close()
		for srcRows.Next() {
			var src string
			var n int
			srcRows.Scan(&src, &n)
			bySource[src] = n
		}
	}

	noiseReduction := 0.0
	if total > 0 {
		noiseReduction = math.Round(float64(merged+monitored+discarded)/float64(total)*100*10) / 10
	}

	c.JSON(http.StatusOK, gin.H{
		"results": results,
		"stats": gin.H{
			"total_processed":        total,
			"total_incidents_created": created,
			"total_merged":           merged,
			"total_monitored":        monitored,
			"total_discarded":        discarded,
			"avg_score":              avgScore,
			"avg_elapsed_ms":         avgElapsedMs,
			"by_strategy":            byStrategy,
			"by_source":              bySource,
			"noise_reduction_rate":   noiseReduction,
		},
	})
}

// GetAIOPSDashboard returns a consolidated snapshot for the AIOps overview tab.
// GET /api/v1/aiops/dashboard
func (h *AIOpsHandler) GetAIOPSDashboard(c *gin.Context) {
	hours := 24
	if hh := c.Query("hours"); hh != "" {
		if n, _ := strconv.Atoi(hh); n > 0 {
			hours = n
		}
	}

	// Pipeline summary (last N hours)
	row := h.db.QueryRowContext(c.Request.Context(), `
		SELECT
			COUNT(*) AS total,
			SUM(CASE WHEN decision='create_incident' THEN 1 ELSE 0 END) AS created,
			SUM(CASE WHEN decision='merge_incident'  THEN 1 ELSE 0 END) AS merged,
			SUM(CASE WHEN decision='monitor'         THEN 1 ELSE 0 END) AS monitored,
			SUM(CASE WHEN decision='discard'         THEN 1 ELSE 0 END) AS discarded,
			COALESCE(ROUND(AVG(final_score)::numeric,3),0)               AS avg_score,
			COALESCE(ROUND(AVG(elapsed_ms)::numeric,0),0)                AS avg_ms
		FROM pipeline_correlation_results
		WHERE processed_at >= NOW() - ($1 * INTERVAL '1 hour')
	`, hours)
	var total, created, merged, monitored, discarded int
	var avgScore, avgMs float64
	row.Scan(&total, &created, &merged, &monitored, &discarded, &avgScore, &avgMs)

	noiseReduction := 0.0
	if total > 0 {
		noiseReduction = math.Round(float64(merged+monitored+discarded)/float64(total)*100*10) / 10
	}

	// Hourly activity (last 24h buckets for sparkline)
	activityRows, _ := h.db.QueryContext(c.Request.Context(), `
		SELECT DATE_TRUNC('hour', processed_at) AS hour, COUNT(*) AS cnt
		FROM pipeline_correlation_results
		WHERE processed_at >= NOW() - INTERVAL '24 hours'
		GROUP BY hour ORDER BY hour
	`)
	type HourBucket struct {
		Hour  string `json:"hour"`
		Count int    `json:"count"`
	}
	activity := make([]HourBucket, 0)
	if activityRows != nil {
		defer activityRows.Close()
		for activityRows.Next() {
			var b HourBucket
			var t sql.NullTime
			activityRows.Scan(&t, &b.Count)
			if t.Valid {
				b.Hour = t.Time.UTC().Format("15:04")
			}
			activity = append(activity, b)
		}
	}

	// Recent auto-created incidents
	incRows, _ := h.db.QueryContext(c.Request.Context(), `
		SELECT id, title, severity, status, created_at
		FROM incidents
		WHERE auto_created = TRUE
		  AND created_at >= NOW() - ($1 * INTERVAL '1 hour')
		ORDER BY created_at DESC LIMIT 10
	`, hours)
	type RecentIncident struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Severity  string `json:"severity"`
		Status    string `json:"status"`
		CreatedAt string `json:"created_at"`
	}
	recentIncidents := make([]RecentIncident, 0)
	if incRows != nil {
		defer incRows.Close()
		for incRows.Next() {
			var ri RecentIncident
			var createdAt sql.NullTime
			incRows.Scan(&ri.ID, &ri.Title, &ri.Severity, &ri.Status, &createdAt)
			if createdAt.Valid {
				ri.CreatedAt = createdAt.Time.UTC().Format("2006-01-02T15:04:05Z")
			}
			recentIncidents = append(recentIncidents, ri)
		}
	}

	// Strategy dominance
	stratRows, _ := h.db.QueryContext(c.Request.Context(), `
		SELECT dominant_strategy, COUNT(*) AS cnt
		FROM pipeline_correlation_results
		WHERE processed_at >= NOW() - ($1 * INTERVAL '1 hour')
		  AND dominant_strategy != ''
		GROUP BY dominant_strategy ORDER BY cnt DESC
	`, hours)
	byStrategy := map[string]int{}
	if stratRows != nil {
		defer stratRows.Close()
		for stratRows.Next() {
			var s string
			var n int
			stratRows.Scan(&s, &n)
			byStrategy[s] = n
		}
	}

	// Domain distribution (V2 ontology)
	domainRows, _ := h.db.QueryContext(c.Request.Context(), `
		SELECT COALESCE(NULLIF(domain,''), 'unknown') AS domain, COUNT(*) AS cnt
		FROM pipeline_correlation_results
		WHERE processed_at >= NOW() - ($1 * INTERVAL '1 hour')
		GROUP BY domain ORDER BY cnt DESC LIMIT 20
	`, hours)
	byDomain := map[string]int{}
	if domainRows != nil {
		defer domainRows.Close()
		for domainRows.Next() {
			var d string
			var n int
			domainRows.Scan(&d, &n)
			byDomain[d] = n
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"pipeline": gin.H{
			"total_processed":         total,
			"total_incidents_created": created,
			"total_merged":            merged,
			"total_monitored":         monitored,
			"total_discarded":         discarded,
			"avg_score":               avgScore,
			"avg_elapsed_ms":          avgMs,
			"noise_reduction_rate":    noiseReduction,
			"by_strategy":             byStrategy,
			"by_domain":               byDomain,
		},
		"hourly_activity":  activity,
		"recent_incidents": recentIncidents,
	})
}

// ProcessMonitoredAlerts re-evaluates every monitor-state alert that is still open/firing
// and has no incident linked. For each, it tries to find an open incident to merge into;
// if none fits, it creates a new incident.
// POST /api/v1/correlation/pipeline/process-monitored
func (h *AIOpsHandler) ProcessMonitoredAlerts(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "DB unavailable"})
		return
	}

	ctx := c.Request.Context()

	// Find monitor-state correlation results whose alert is still active (not resolved/closed).
	rows, err := h.db.QueryContext(ctx, `
		SELECT DISTINCT ON (pcr.alert_id)
		    pcr.id           AS pcr_id,
		    pcr.alert_id,
		    a.title, a.description, a.severity, a.source,
		    a.fingerprint, a.labels, a.metadata, a.status AS alert_status
		FROM pipeline_correlation_results pcr
		JOIN alerts a ON a.id = pcr.alert_id
		WHERE pcr.decision = 'monitor'
		  AND pcr.incident_id IS NULL
		  AND a.status NOT IN ('resolved', 'closed')
		ORDER BY pcr.alert_id, pcr.processed_at DESC
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "internal error"})
		return
	}
	defer rows.Close()

	type candidate struct {
		pcrID       string
		alertID     uuid.UUID
		title, desc, severity, source, fingerprint string
		alertStatus string
		labels      map[string]string
	}
	var candidates []candidate
	for rows.Next() {
		var can candidate
		var alertIDStr, labelsJSON, metaJSON string
		if err := rows.Scan(
			&can.pcrID, &alertIDStr,
			&can.title, &can.desc, &can.severity, &can.source,
			&can.fingerprint, &labelsJSON, &metaJSON, &can.alertStatus,
		); err != nil {
			continue
		}
		can.alertID, _ = uuid.Parse(alertIDStr)
		// Deserialize labels so merge logic can use cluster/entity context.
		if labelsJSON != "" {
			_ = json.Unmarshal([]byte(labelsJSON), &can.labels)
		}
		if can.labels == nil {
			can.labels = map[string]string{}
		}
		candidates = append(candidates, can)
	}
	rows.Close()

	if len(candidates) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": true, "processed": 0, "message": "No open monitor-state alerts found"})
		return
	}

	merged, created, skipped := 0, 0, 0
	for _, can := range candidates {
		cluster := can.labels["cluster"]
		if cluster == "" {
			cluster = can.labels["k8s.cluster.name"]
		}
		rootEntity := can.labels["root_cause_entity"]

		// Find the best matching open incident using cluster + entity context.
		// Precedence:
		//   1. Same cluster AND same root entity (tightest match)
		//   2. Same cluster AND title overlap (loose same-cluster grouping)
		//   3. Title substring match only (cross-cluster, title must overlap well)
		// Source+severity alone is NOT sufficient — it caused all predictions to
		// collapse into one giant incident.
		var bestIncidentID uuid.UUID
		var bestNumber string
		titleKey := can.title
		if len(titleKey) > 40 {
			titleKey = titleKey[:40]
		}
		err := h.db.QueryRowContext(ctx, `
			SELECT i.id, i.incident_number
			FROM incidents i
			WHERE i.status IN ('open','investigating','acknowledged')
			  AND i.created_at >= NOW() - INTERVAL '2 hours'
			  AND (
			      -- Tier 1: same cluster + same root entity (highest confidence)
			      ($4 <> '' AND $5 <> '' AND EXISTS (
			          SELECT 1 FROM alerts a2
			          WHERE a2.id::text = ANY(SELECT jsonb_array_elements_text(i.alert_ids))
			            AND (a2.labels->>'cluster' = $4 OR a2.labels->>'k8s.cluster.name' = $4)
			            AND a2.labels->>'root_cause_entity' = $5
			            AND a2.status NOT IN ('resolved','closed')
			      ))
			      OR
			      -- Tier 2: same cluster + title keyword overlap
			      ($4 <> '' AND i.title ILIKE '%' || $1 || '%' AND EXISTS (
			          SELECT 1 FROM alerts a2
			          WHERE a2.id::text = ANY(SELECT jsonb_array_elements_text(i.alert_ids))
			            AND (a2.labels->>'cluster' = $4 OR a2.labels->>'k8s.cluster.name' = $4)
			            AND a2.status NOT IN ('resolved','closed')
			      ))
			      OR
			      -- Tier 3: title match + same source + same severity (no cluster context)
			      ($4 = '' AND i.title ILIKE '%' || $1 || '%'
			       AND i.severity = $3 AND i.source = $2)
			  )
			ORDER BY
			  -- prefer tier 1 (cluster + entity match) over tier 2/3
			  CASE WHEN $5 <> '' AND EXISTS (
			      SELECT 1 FROM alerts a2
			      WHERE a2.id::text = ANY(SELECT jsonb_array_elements_text(i.alert_ids))
			        AND a2.labels->>'root_cause_entity' = $5
			  ) THEN 0 ELSE 1 END,
			  i.created_at DESC
			LIMIT 1
		`, titleKey, can.source, can.severity, cluster, rootEntity).Scan(&bestIncidentID, &bestNumber)

		if err == nil && bestIncidentID != uuid.Nil {
			// Link alert to existing incident — but only if the alert is not already owned
			// by a different incident. The alerts UPDATE is guarded by incident_id IS NULL,
			// so if it affects 0 rows the alert belongs elsewhere; don't cross-contaminate
			// the target incident's alert_ids (that creates the split-brain where alert_ids
			// contains alert UUIDs whose incident_id FK points at a completely different incident).
			mergeResult, mergeErr := h.db.ExecContext(ctx, `
				UPDATE alerts SET incident_id = $1, updated_at = NOW() WHERE id = $2 AND incident_id IS NULL
			`, bestIncidentID, can.alertID)
			if mergeErr != nil {
				log.Printf("ProcessMonitored: alert update failed alert=%s: %v", can.alertID, mergeErr)
				skipped++
				continue
			}
			rowsAffected, _ := mergeResult.RowsAffected()
			if rowsAffected == 0 {
				// Alert already owned by another incident — skip entirely to avoid split-brain.
				log.Printf("ProcessMonitored: alert %s already owned by another incident — skipped", can.alertID)
				skipped++
				continue
			}
			_, appendErr := h.db.ExecContext(ctx, `
				UPDATE incidents
				SET alert_ids = CASE
				    WHEN alert_ids ? $1 THEN alert_ids
				    ELSE alert_ids || jsonb_build_array($1)
				    END,
				    updated_at = NOW()
				WHERE id = $2
			`, can.alertID.String(), bestIncidentID)
			_, pcrErr := h.db.ExecContext(ctx, `
				UPDATE pipeline_correlation_results
				SET decision = 'merge_incident', incident_id = $1
				WHERE id = $2
			`, bestIncidentID, can.pcrID)
			if appendErr == nil && pcrErr == nil {
				log.Printf("ProcessMonitored: alert %s merged into incident %s (%s)", can.alertID, bestIncidentID, bestNumber)
				merged++
			} else {
				skipped++
			}
		} else {
			// No matching incident — create one.
			newID := uuid.New()
			_, createErr := h.db.ExecContext(ctx, `
				INSERT INTO incidents (
				    id, incident_number, title, description, severity, status, priority,
				    alert_ids, auto_created, started_at, created_at, updated_at
				) VALUES (
				    $1,
				    'INC-' || LPAD(nextval('incident_number_seq')::text, 6, '0'),
				    $2, $3, $4, 'open', 'medium',
				    jsonb_build_array($5::text),
				    true, NOW(), NOW(), NOW()
				)
			`, newID, can.title, can.desc, can.severity, can.alertID.String())
			if createErr != nil {
				log.Printf("ProcessMonitored: failed to create incident for alert %s: %v", can.alertID, createErr)
				skipped++
				continue
			}
			_, _ = h.db.ExecContext(ctx, `UPDATE alerts SET incident_id = $1, updated_at = NOW() WHERE id = $2`, newID, can.alertID)
			_, _ = h.db.ExecContext(ctx, `UPDATE pipeline_correlation_results SET decision = 'create_incident', incident_id = $1 WHERE id = $2`, newID, can.pcrID)
			log.Printf("ProcessMonitored: created incident %s for alert %s (%s)", newID, can.alertID, can.title)
			created++
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"total":     len(candidates),
		"merged":    merged,
		"created":   created,
		"skipped":   skipped,
		"message":   fmt.Sprintf("Processed %d monitor-state alerts: %d merged, %d new incidents, %d skipped", len(candidates), merged, created, skipped),
	})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
