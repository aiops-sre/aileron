package handlers

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
)

// IntelligenceStatsHandler serves aggregate metrics for the intelligence layer.
// Powers the Intelligence tab on the AIOps page and the Intelligence Operations dashboard.
type IntelligenceStatsHandler struct {
	db *sql.DB
}

func NewIntelligenceStatsHandler(db *sql.DB) *IntelligenceStatsHandler {
	return &IntelligenceStatsHandler{db: db}
}

// GetStats handles GET /api/v1/intelligence/stats.
// Returns aggregate counts and recent activity across all intelligence subsystems.
func (h *IntelligenceStatsHandler) GetStats(c *gin.Context) {
	ctx := c.Request.Context()

	type stats struct {
		// Policy engine
		PoliciesTotal   int `json:"policies_total"`
		PoliciesEnabled int `json:"policies_enabled"`

		// Runbook catalog
		RunbooksTotal int `json:"runbooks_total"`

		// KubeSense signals (last 24h)
		KubesenseHealthEvents    int `json:"kubesense_health_events_24h"`
		KubesenseConfigViolations int `json:"kubesense_config_violations_24h"`
		KubesenseForecasts       int `json:"kubesense_forecasts_active"`
		KubesenseAPMSignals      int `json:"kubesense_apm_signals_24h"`
		KubesenseInvResults      int `json:"kubesense_investigation_results"`

		// Gate hooks
		RemediationsProposed int `json:"remediations_proposed"`
		RemediationsApproved int `json:"remediations_approved"`
		RemediationsRejected int `json:"remediations_rejected"`
		RemediationsTotal    int `json:"remediations_total"`

		// Postmortems
		PostmortemsGenerated int `json:"postmortems_generated"`
		PostmortemsLLM       int `json:"postmortems_llm"`

		// OIE investigations (last 7d)
		OIEInvestigationsTotal     int     `json:"oie_investigations_7d"`
		OIEInvestigationsCompleted int     `json:"oie_investigations_completed_7d"`
		OIEAvgConfidence           float64 `json:"oie_avg_confidence"`
	}

	var s stats

	// Policy engine stats
	h.db.QueryRowContext(ctx, `SELECT COUNT(*), COUNT(*) FILTER (WHERE enabled) FROM intelligence_policies`).
		Scan(&s.PoliciesTotal, &s.PoliciesEnabled)

	// Runbook catalog
	h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM investigation_runbooks WHERE enabled = true`).
		Scan(&s.RunbooksTotal)

	// KubeSense signals
	h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM kubesense_health_events WHERE received_at > NOW() - INTERVAL '7 days'`).
		Scan(&s.KubesenseHealthEvents)
	h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM kubesense_config_violations WHERE occurred_at > NOW() - INTERVAL '7 days'`).
		Scan(&s.KubesenseConfigViolations)
	h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM kubesense_forecasts WHERE created_at > NOW() - INTERVAL '7 days'`).
		Scan(&s.KubesenseForecasts)
	h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM kubesense_apm_signals WHERE sampled_at > NOW() - INTERVAL '3 days'`).
		Scan(&s.KubesenseAPMSignals)
	h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM kubesense_investigation_results WHERE completed_at > NOW() - INTERVAL '7 days'`).
		Scan(&s.KubesenseInvResults)

	// Gate hooks
	h.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status = 'proposed'),
			COUNT(*) FILTER (WHERE status = 'approved'),
			COUNT(*) FILTER (WHERE status = 'rejected'),
			COUNT(*)
		FROM remediations_pending
	`).Scan(&s.RemediationsProposed, &s.RemediationsApproved, &s.RemediationsRejected, &s.RemediationsTotal)

	// Postmortems
	h.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE generated_by = 'llm')
		FROM post_mortems
	`).Scan(&s.PostmortemsGenerated, &s.PostmortemsLLM)

	// OIE investigation health (last 7 days)
	// These come from the OIE's own database, not AlertHub's — skip if table doesn't exist
	row := h.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE rca_status = 'completed') as completed,
			COALESCE(AVG(rca_confidence) FILTER (WHERE rca_confidence > 0), 0) as avg_conf
		FROM incidents
		WHERE created_at > NOW() - INTERVAL '7 days'
		  AND rca_status IS NOT NULL
		  AND rca_status != 'none'
	`)
	row.Scan(&s.OIEInvestigationsTotal, &s.OIEInvestigationsCompleted, &s.OIEAvgConfidence)

	c.JSON(http.StatusOK, gin.H{"stats": s})
}

// GetRecentRemediations handles GET /api/v1/intelligence/remediations?status=proposed&limit=10.
// Returns recent remediation proposals across all incidents for the oncall queue.
func (h *IntelligenceStatsHandler) GetRecentRemediations(c *gin.Context) {
	ctx := c.Request.Context()
	status := c.Query("status") // "", "proposed", "approved", "rejected", "executed"
	limit := 20

	rows, err := h.db.QueryContext(ctx, `
		SELECT r.id::text, r.incident_id::text, i.incident_number, i.title,
		       r.proposed_action, r.action_type, r.risk_level, r.status,
		       r.proposed_by, COALESCE(r.approved_by,''), r.created_at
		FROM remediations_pending r
		JOIN incidents i ON i.id = r.incident_id
		WHERE ($1 = '' OR r.status = $1)
		ORDER BY r.created_at DESC
		LIMIT $2
	`, status, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	type row struct {
		ID              string `json:"id"`
		IncidentID      string `json:"incident_id"`
		IncidentNumber  string `json:"incident_number"`
		IncidentTitle   string `json:"incident_title"`
		ProposedAction  string `json:"proposed_action"`
		ActionType      string `json:"action_type"`
		RiskLevel       string `json:"risk_level"`
		Status          string `json:"status"`
		ProposedBy      string `json:"proposed_by"`
		ApprovedBy      string `json:"approved_by,omitempty"`
		CreatedAt       string `json:"created_at"`
	}
	var items []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.ID, &r.IncidentID, &r.IncidentNumber, &r.IncidentTitle,
			&r.ProposedAction, &r.ActionType, &r.RiskLevel, &r.Status,
			&r.ProposedBy, &r.ApprovedBy, &r.CreatedAt); err != nil {
			continue
		}
		items = append(items, r)
	}
	if items == nil {
		items = []row{}
	}
	c.JSON(http.StatusOK, gin.H{"remediations": items, "count": len(items)})
}
