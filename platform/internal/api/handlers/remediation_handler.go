package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RemediationHandler manages the remediations_pending gate (Sympozium gate hook pattern).
// Before any automated remediation executes, it enters this table with status=proposed.
// An oncall engineer approves or rejects it. Only approved remediations run.
//
// Flow: proposed → (notify oncall via Slack) → approved | rejected → (if approved) executing → executed
type RemediationHandler struct {
	db             *sql.DB
	slackWebhookURL string // INTELLIGENCE_SLACK_WEBHOOK env var — oncall notification on propose
}

func NewRemediationHandler(db *sql.DB) *RemediationHandler {
	return &RemediationHandler{
		db:              db,
		slackWebhookURL: os.Getenv("INTELLIGENCE_SLACK_WEBHOOK"),
	}
}

type remediationRow struct {
	ID              string    `json:"id"`
	IncidentID      string    `json:"incident_id"`
	ProposedAction  string    `json:"proposed_action"`
	ActionType      string    `json:"action_type"` // "restart_pod", "scale_up", "config_change", "manual"
	RiskLevel       string    `json:"risk_level"`  // "low", "medium", "high"
	Status          string    `json:"status"`      // "proposed", "approved", "rejected", "executing", "executed", "failed"
	ProposedBy      string    `json:"proposed_by"` // "oie", "postmortem", "operator"
	ApprovedBy      string    `json:"approved_by,omitempty"`
	RejectionReason string    `json:"rejection_reason,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// ListRemediations handles GET /incidents/:id/remediations.
func (h *RemediationHandler) ListRemediations(c *gin.Context) {
	incidentID := c.Param("id")
	rows, err := h.db.QueryContext(c.Request.Context(), `
		SELECT id::text, incident_id::text, proposed_action, action_type, risk_level,
		       status, proposed_by, COALESCE(approved_by,''), COALESCE(rejection_reason,''),
		       created_at, updated_at
		FROM remediations_pending
		WHERE incident_id::text = $1
		ORDER BY created_at DESC
	`, incidentID)
	if err != nil {
		log.Printf("ListRemediations: db query error incident=%s: %v", incidentID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	var items []remediationRow
	for rows.Next() {
		var r remediationRow
		if err := rows.Scan(&r.ID, &r.IncidentID, &r.ProposedAction, &r.ActionType, &r.RiskLevel,
			&r.Status, &r.ProposedBy, &r.ApprovedBy, &r.RejectionReason, &r.CreatedAt, &r.UpdatedAt); err != nil {
			continue
		}
		items = append(items, r)
	}
	if items == nil {
		items = []remediationRow{}
	}
	c.JSON(http.StatusOK, gin.H{"remediations": items})
}

// ProposeRemediation handles POST /incidents/:id/remediations.
// Called by OIE, postmortem service, or operators to propose an action.
func (h *RemediationHandler) ProposeRemediation(c *gin.Context) {
	incidentID := c.Param("id")
	var body struct {
		ProposedAction string `json:"proposed_action" binding:"required"`
		ActionType     string `json:"action_type"`
		RiskLevel      string `json:"risk_level"`
		ProposedBy     string `json:"proposed_by"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if body.ActionType == "" {
		body.ActionType = "manual"
	}
	if body.RiskLevel == "" {
		body.RiskLevel = "medium"
	}
	if body.ProposedBy == "" {
		body.ProposedBy = "operator"
	}
	id := uuid.New()
	_, err := h.db.ExecContext(c.Request.Context(), `
		INSERT INTO remediations_pending
			(id, incident_id, proposed_action, action_type, risk_level, status, proposed_by, created_at, updated_at)
		VALUES ($1, $2::uuid, $3, $4, $5, 'proposed', $6, NOW(), NOW())
	`, id, incidentID, body.ProposedAction, body.ActionType, body.RiskLevel, body.ProposedBy)
	if err != nil {
		log.Printf("ProposeRemediation: db error incident=%s: %v", incidentID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	// Notify oncall via Slack so the gate doesn't silently wait (Sympozium gate hook pattern).
	if h.slackWebhookURL != "" {
		go h.notifySlack(incidentID, id.String(), body.ProposedAction, body.RiskLevel, body.ProposedBy)
	}

	c.JSON(http.StatusCreated, gin.H{"id": id, "status": "proposed", "message": "remediation proposed — awaiting oncall approval"})
}

// ApproveRemediation handles POST /incidents/:id/remediations/:rid/approve.
func (h *RemediationHandler) ApproveRemediation(c *gin.Context) {
	rid := c.Param("rid")
	approvedBy := c.GetString("user_id")
	if approvedBy == "" {
		approvedBy = "operator"
	}
	res, err := h.db.ExecContext(c.Request.Context(), `
		UPDATE remediations_pending
		SET status = 'approved', approved_by = $1, updated_at = NOW()
		WHERE id::text = $2 AND status = 'proposed'
	`, approvedBy, rid)
	if err != nil {
		log.Printf("ApproveRemediation: db error rid=%s: %v", rid, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "remediation not found or already actioned"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "approved", "approved_by": approvedBy})
}

// RejectRemediation handles POST /incidents/:id/remediations/:rid/reject.
func (h *RemediationHandler) RejectRemediation(c *gin.Context) {
	rid := c.Param("rid")
	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&body)
	rejectedBy := c.GetString("user_id")
	if rejectedBy == "" {
		rejectedBy = "operator"
	}
	res, err := h.db.ExecContext(c.Request.Context(), `
		UPDATE remediations_pending
		SET status = 'rejected', approved_by = $1, rejection_reason = $2, updated_at = NOW()
		WHERE id::text = $3 AND status = 'proposed'
	`, rejectedBy, body.Reason, rid)
	if err != nil {
		log.Printf("RejectRemediation: db error rid=%s: %v", rid, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "remediation not found or already actioned"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "rejected"})
}

// notifySlack sends a Slack webhook notification when a remediation is proposed.
// This completes the gate hook flow: proposed → oncall sees Slack alert → approve/reject in UI.
func (h *RemediationHandler) notifySlack(incidentID, remediationID, action, riskLevel, proposedBy string) {
	riskEmoji := ":white_circle:"
	switch riskLevel {
	case "high":
		riskEmoji = ":red_circle:"
	case "medium":
		riskEmoji = ":large_yellow_circle:"
	case "low":
		riskEmoji = ":large_green_circle:"
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"text": fmt.Sprintf("%s *Remediation Proposed — Gate Approval Required*", riskEmoji),
		"blocks": []map[string]interface{}{
			{
				"type": "header",
				"text": map[string]string{"type": "plain_text", "text": fmt.Sprintf("%s Remediation Proposed", riskEmoji)},
			},
			{
				"type": "section",
				"fields": []map[string]string{
					{"type": "mrkdwn", "text": fmt.Sprintf("*Action:*\n%s", action)},
					{"type": "mrkdwn", "text": fmt.Sprintf("*Risk:* %s\n*Proposed by:* %s", riskLevel, proposedBy)},
				},
			},
			{
				"type": "section",
				"text": map[string]string{
					"type": "mrkdwn",
					"text": fmt.Sprintf("*Incident:* %s\n*Remediation ID:* `%s`\n\nApprove or reject in AlertHub: _Incidents → incident → Actions tab_",
						incidentID, remediationID),
				},
			},
		},
	})
	resp, err := http.Post(h.slackWebhookURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		log.Printf("remediation slack notify failed: %v", err)
		return
	}
	resp.Body.Close()
}
