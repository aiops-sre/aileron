package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// MCPHandler implements a Model Context Protocol (MCP) server over HTTP/SSE.
// This exposes AlertHub incidents and investigations to MCP clients such as
// Claude Desktop, Cursor, and Windsurf.
//
// Protocol: JSON-RPC 2.0 (https://spec.modelcontextprotocol.io)
// Endpoint: POST /api/v1/mcp  (JSON-RPC over HTTP)
//
// Available tools:
//   - list_incidents       — list open/recent incidents with filters
//   - get_incident         — get full incident details + RCA
//   - get_rca_decisions    — get CACIE/OIE RCA decisions for an incident
//   - search_incidents     — text search across incident titles and RCA
type MCPHandler struct {
	db *sql.DB
}

func NewMCPHandler(db *sql.DB) *MCPHandler {
	return &MCPHandler{db: db}
}

// ── JSON-RPC types ─────────────────────────────────────────────────────────────

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ── MCP protocol types ─────────────────────────────────────────────────────────

type mcpTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema"`
}

type mcpToolResult struct {
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError,omitempty"`
}

type mcpContent struct {
	Type string `json:"type"` // "text" | "image"
	Text string `json:"text,omitempty"`
}

// ── Tool input schemas ─────────────────────────────────────────────────────────

var mcpTools = []mcpTool{
	{
		Name:        "list_incidents",
		Description: "List AlertHub incidents. Filters: status (open/investigating/resolved), severity (critical/high/medium/low), limit (default 20).",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"status":   map[string]string{"type": "string", "description": "Filter by status: open, investigating, resolved, acknowledged"},
				"severity": map[string]string{"type": "string", "description": "Filter by severity: critical, high, medium, low"},
				"limit":    map[string]interface{}{"type": "integer", "description": "Max results (default 20, max 50)"},
			},
		},
	},
	{
		Name:        "get_incident",
		Description: "Get full details of an AlertHub incident by ID or incident number, including RCA status, confidence, and root cause.",
		InputSchema: map[string]interface{}{
			"type":     "object",
			"required": []string{"id"},
			"properties": map[string]interface{}{
				"id": map[string]string{"type": "string", "description": "Incident UUID or incident number (e.g. INC-001)"},
			},
		},
	},
	{
		Name:        "get_rca_decisions",
		Description: "Get RCA decisions (CACIE + OIE investigation results) for an incident. Returns root cause, confidence, and evidence summary.",
		InputSchema: map[string]interface{}{
			"type":     "object",
			"required": []string{"incident_id"},
			"properties": map[string]interface{}{
				"incident_id": map[string]string{"type": "string", "description": "Incident UUID"},
			},
		},
	},
	{
		Name:        "search_incidents",
		Description: "Full-text search across AlertHub incident titles, descriptions, and RCA text. Returns matching incidents with relevance context.",
		InputSchema: map[string]interface{}{
			"type":     "object",
			"required": []string{"query"},
			"properties": map[string]interface{}{
				"query":  map[string]string{"type": "string", "description": "Search text"},
				"limit":  map[string]interface{}{"type": "integer", "description": "Max results (default 10)"},
				"status": map[string]string{"type": "string", "description": "Filter by status (optional)"},
			},
		},
	},
	{
		Name:        "get_postmortem",
		Description: "Get the auto-generated postmortem for a resolved incident. Returns impact, root cause, lessons learned, action items, and timeline.",
		InputSchema: map[string]interface{}{
			"type":     "object",
			"required": []string{"incident_id"},
			"properties": map[string]interface{}{
				"incident_id": map[string]string{"type": "string", "description": "Incident UUID"},
			},
		},
	},
	{
		Name:        "list_runbooks",
		Description: "List investigation runbooks from the AlertHub skill catalog. Filter by domain (k8s, netapp, cloudstack) or entity_type (pod, node, pvc).",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"domain":      map[string]string{"type": "string", "description": "Filter by domain: k8s, netapp, cloudstack"},
				"entity_type": map[string]string{"type": "string", "description": "Filter by entity type: pod, node, pvc, service"},
			},
		},
	},
	{
		Name:        "propose_remediation",
		Description: "Propose a remediation action for an incident. Requires oncall approval before execution (gate hook pattern).",
		InputSchema: map[string]interface{}{
			"type":     "object",
			"required": []string{"incident_id", "proposed_action"},
			"properties": map[string]interface{}{
				"incident_id":     map[string]string{"type": "string", "description": "Incident UUID"},
				"proposed_action": map[string]string{"type": "string", "description": "Describe the remediation action"},
				"risk_level":      map[string]string{"type": "string", "description": "Risk level: low, medium, high"},
				"action_type":     map[string]string{"type": "string", "description": "Type: restart_pod, scale_up, config_change, manual"},
			},
		},
	},
}

// Handle is the main JSON-RPC dispatch handler for POST /api/v1/mcp.
func (h *MCPHandler) Handle(c *gin.Context) {
	var req rpcRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, rpcResponse{
			JSONRPC: "2.0",
			Error:   &rpcError{Code: -32700, Message: "parse error: " + err.Error()},
		})
		return
	}

	var result interface{}
	var rpcErr *rpcError

	switch req.Method {
	case "initialize":
		result = map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools": map[string]bool{"listChanged": false},
			},
			"serverInfo": map[string]string{
				"name":    "alerthub-mcp",
				"version": "1.0.0",
			},
		}

	case "tools/list":
		result = map[string]interface{}{"tools": mcpTools}

	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			rpcErr = &rpcError{Code: -32602, Message: "invalid params"}
			break
		}
		toolResult, err := h.dispatchTool(c, params.Name, params.Arguments)
		if err != nil {
			result = mcpToolResult{
				Content: []mcpContent{{Type: "text", Text: "Error: " + err.Error()}},
				IsError: true,
			}
		} else {
			result = toolResult
		}

	case "ping":
		result = map[string]string{"status": "ok"}

	default:
		rpcErr = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}

	c.JSON(http.StatusOK, rpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  result,
		Error:   rpcErr,
	})
}

func (h *MCPHandler) dispatchTool(c *gin.Context, name string, args json.RawMessage) (*mcpToolResult, error) {
	ctx := c.Request.Context()
	var argsMap map[string]interface{}
	_ = json.Unmarshal(args, &argsMap)

	getString := func(key, def string) string {
		if v, ok := argsMap[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return def
	}
	getInt := func(key string, def int) int {
		if v, ok := argsMap[key]; ok {
			switch n := v.(type) {
			case float64:
				return int(n)
			case int:
				return n
			}
		}
		return def
	}

	switch name {
	case "list_incidents":
		status := getString("status", "")
		severity := getString("severity", "")
		limit := getInt("limit", 20)
		if limit > 50 {
			limit = 50
		}

		query := `
			SELECT id, incident_number, title, severity, status, rca_status,
			       COALESCE(rca_confidence, 0), COALESCE(ai_root_cause, ''),
			       created_at, COALESCE(resolved_at::text, '')
			FROM incidents
			WHERE ($1 = '' OR status = $1)
			  AND ($2 = '' OR severity = $2)
			ORDER BY created_at DESC
			LIMIT $3`
		rows, err := h.db.QueryContext(ctx, query, status, severity, limit)
		if err != nil {
			return nil, fmt.Errorf("query incidents: %w", err)
		}
		defer rows.Close()

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("AlertHub Incidents (status=%q severity=%q limit=%d)\n\n", status, severity, limit))
		count := 0
		for rows.Next() {
			var id, number, title, sev, st, rcaStatus string
			var rcaConf float64
			var rcaText, createdAt, resolvedAt string
			if err := rows.Scan(&id, &number, &title, &sev, &st, &rcaStatus, &rcaConf, &rcaText, &createdAt, &resolvedAt); err != nil {
				continue
			}
			sb.WriteString(fmt.Sprintf("• %s (%s) — %s [%s/%s] rca=%s(%.0f%%)\n",
				number, id[:8], title, sev, st, rcaStatus, rcaConf*100))
			if rcaText != "" {
				sb.WriteString(fmt.Sprintf("  RCA: %s\n", rcaText[:mcpMin(100, len(rcaText))]))
			}
			sb.WriteString(fmt.Sprintf("  Created: %s\n", createdAt))
			count++
		}
		if count == 0 {
			sb.WriteString("No incidents found matching the filters.\n")
		}
		return &mcpToolResult{Content: []mcpContent{{Type: "text", Text: sb.String()}}}, nil

	case "get_incident":
		id := getString("id", "")
		if id == "" {
			return nil, fmt.Errorf("id is required")
		}

		var incidentID, number, title, sev, status, rcaStatus string
		var rcaConf float64
		var rcaText, createdAt, resolvedAt, topologyPath, correlationID string
		err := h.db.QueryRowContext(ctx, `
			SELECT id::text, incident_number, title, severity, status, COALESCE(rca_status,'none'),
			       COALESCE(rca_confidence, 0), COALESCE(ai_root_cause, ''),
			       created_at::text, COALESCE(resolved_at::text, ''),
			       COALESCE(topology_path, ''), COALESCE(correlation_id::text, '')
			FROM incidents
			WHERE id::text = $1 OR incident_number = $1
			ORDER BY created_at DESC LIMIT 1
		`, id).Scan(&incidentID, &number, &title, &sev, &status, &rcaStatus,
			&rcaConf, &rcaText, &createdAt, &resolvedAt, &topologyPath, &correlationID)
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("incident %q not found", id)
		}
		if err != nil {
			return nil, fmt.Errorf("query incident: %w", err)
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Incident: %s (%s)\n", number, incidentID))
		sb.WriteString(fmt.Sprintf("Title: %s\n", title))
		sb.WriteString(fmt.Sprintf("Severity: %s | Status: %s\n", sev, status))
		sb.WriteString(fmt.Sprintf("RCA Status: %s | Confidence: %.0f%%\n", rcaStatus, rcaConf*100))
		if rcaText != "" {
			sb.WriteString(fmt.Sprintf("Root Cause: %s\n", rcaText))
		}
		sb.WriteString(fmt.Sprintf("Created: %s\n", createdAt))
		if resolvedAt != "" {
			sb.WriteString(fmt.Sprintf("Resolved: %s\n", resolvedAt))
		}
		if topologyPath != "" {
			sb.WriteString(fmt.Sprintf("Topology: %s\n", topologyPath))
		}

		// Fetch correlated alerts count.
		var alertCount int
		_ = h.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM alerts WHERE incident_id::text = $1`, incidentID).Scan(&alertCount)
		sb.WriteString(fmt.Sprintf("Correlated Alerts: %d\n", alertCount))

		return &mcpToolResult{Content: []mcpContent{{Type: "text", Text: sb.String()}}}, nil

	case "get_rca_decisions":
		incidentID := getString("incident_id", "")
		if incidentID == "" {
			return nil, fmt.Errorf("incident_id is required")
		}

		rows, err := h.db.QueryContext(ctx, `
			SELECT decision_type, root_entity_id, root_entity_type, root_cause_domain,
			       final_confidence, root_cause_description, COALESCE(evidence_count, 0),
			       created_at::text
			FROM rca_decisions
			WHERE incident_id::text = $1
			ORDER BY final_confidence DESC
			LIMIT 5
		`, incidentID)
		if err != nil {
			return nil, fmt.Errorf("query rca_decisions: %w", err)
		}
		defer rows.Close()

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("RCA Decisions for incident %s\n\n", incidentID))
		count := 0
		for rows.Next() {
			var decType, entityID, entityType, domain, description, createdAt string
			var confidence float64
			var evidenceCount int
			if err := rows.Scan(&decType, &entityID, &entityType, &domain, &confidence, &description, &evidenceCount, &createdAt); err != nil {
				continue
			}
			sb.WriteString(fmt.Sprintf("Decision [%s]: %s/%s confidence=%.0f%% evidence=%d\n",
				decType, entityType, entityID, confidence*100, evidenceCount))
			sb.WriteString(fmt.Sprintf("  Domain: %s\n", domain))
			if description != "" {
				sb.WriteString(fmt.Sprintf("  RCA: %s\n", description[:mcpMin(200, len(description))]))
			}
			sb.WriteString(fmt.Sprintf("  At: %s\n\n", createdAt))
			count++
		}
		if count == 0 {
			sb.WriteString("No RCA decisions found. Investigation may not have completed yet.\n")
		}
		return &mcpToolResult{Content: []mcpContent{{Type: "text", Text: sb.String()}}}, nil

	case "search_incidents":
		query := getString("query", "")
		if query == "" {
			return nil, fmt.Errorf("query is required")
		}
		limit := getInt("limit", 10)
		status := getString("status", "")
		if limit > 20 {
			limit = 20
		}
		pattern := "%" + query + "%"

		rows, err := h.db.QueryContext(ctx, `
			SELECT id::text, incident_number, title, severity, status,
			       COALESCE(rca_confidence, 0), COALESCE(ai_root_cause, ''),
			       created_at::text
			FROM incidents
			WHERE (title ILIKE $1 OR COALESCE(ai_root_cause,'') ILIKE $1)
			  AND ($2 = '' OR status = $2)
			ORDER BY created_at DESC
			LIMIT $3
		`, pattern, status, limit)
		if err != nil {
			return nil, fmt.Errorf("search incidents: %w", err)
		}
		defer rows.Close()

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Search results for %q:\n\n", query))
		count := 0
		for rows.Next() {
			var id, number, title, sev, st, rcaText, createdAt string
			var rcaConf float64
			if err := rows.Scan(&id, &number, &title, &sev, &st, &rcaConf, &rcaText, &createdAt); err != nil {
				continue
			}
			sb.WriteString(fmt.Sprintf("• %s — %s [%s/%s] rca=%.0f%%\n", number, title, sev, st, rcaConf*100))
			if rcaText != "" {
				sb.WriteString(fmt.Sprintf("  %s\n", rcaText[:mcpMin(120, len(rcaText))]))
			}
			count++
		}
		if count == 0 {
			sb.WriteString("No incidents found matching the search query.\n")
		}
		return &mcpToolResult{Content: []mcpContent{{Type: "text", Text: sb.String()}}}, nil

	case "get_postmortem":
		incidentID := getString("incident_id", "")
		if incidentID == "" {
			return nil, fmt.Errorf("incident_id is required")
		}
		var timelineJSON []byte
		err := h.db.QueryRowContext(ctx,
			`SELECT COALESCE(timeline, '{}') FROM post_mortems WHERE incident_id::text = $1 ORDER BY created_at DESC LIMIT 1`,
			incidentID).Scan(&timelineJSON)
		if err == sql.ErrNoRows {
			return &mcpToolResult{
				Content: []mcpContent{{Type: "text", Text: "No postmortem available for incident " + incidentID + ". Incident may not be resolved yet."}},
			}, nil
		}
		if err != nil {
			return nil, fmt.Errorf("fetch postmortem: %w", err)
		}
		return &mcpToolResult{Content: []mcpContent{{Type: "text", Text: string(timelineJSON)}}}, nil

	case "list_runbooks":
		domain := getString("domain", "")
		entityType := getString("entity_type", "")
		rows, err := h.db.QueryContext(ctx, `
			SELECT name, domain, entity_type, failure_class, content, source
			FROM investigation_runbooks
			WHERE ($1 = '' OR domain = $1 OR domain = '')
			  AND ($2 = '' OR entity_type = $2 OR entity_type = '')
			  AND enabled = true
			ORDER BY domain, entity_type, failure_class
			LIMIT 20
		`, domain, entityType)
		if err != nil {
			return nil, fmt.Errorf("list runbooks: %w", err)
		}
		defer rows.Close()
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Investigation Runbooks (domain=%q entity_type=%q):\n\n", domain, entityType))
		count := 0
		for rows.Next() {
			var name, dom, et, fc, content, source string
			if err := rows.Scan(&name, &dom, &et, &fc, &content, &source); err != nil {
				continue
			}
			sb.WriteString(fmt.Sprintf("## %s [%s]\n", name, source))
			if dom != "" || et != "" || fc != "" {
				sb.WriteString(fmt.Sprintf("Scope: domain=%s entity_type=%s failure_class=%s\n", dom, et, fc))
			}
			excerpt := content
			if len(excerpt) > 300 {
				excerpt = excerpt[:300] + "..."
			}
			sb.WriteString(excerpt + "\n\n")
			count++
		}
		if count == 0 {
			sb.WriteString("No runbooks found for the given filters.\n")
		}
		return &mcpToolResult{Content: []mcpContent{{Type: "text", Text: sb.String()}}}, nil

	case "propose_remediation":
		incidentID := getString("incident_id", "")
		action := getString("proposed_action", "")
		if incidentID == "" || action == "" {
			return nil, fmt.Errorf("incident_id and proposed_action are required")
		}
		riskLevel := getString("risk_level", "medium")
		actionType := getString("action_type", "manual")
		id := time.Now().UnixNano() // simple temp ID placeholder
		_, err := h.db.ExecContext(ctx, `
			INSERT INTO remediations_pending
				(id, incident_id, proposed_action, action_type, risk_level, status, proposed_by, created_at, updated_at)
			VALUES (uuid_generate_v4(), $1::uuid, $2, $3, $4, 'proposed', 'mcp-agent', NOW(), NOW())
		`, incidentID, action, actionType, riskLevel)
		if err != nil {
			return nil, fmt.Errorf("propose remediation: %w", err)
		}
		return &mcpToolResult{Content: []mcpContent{{Type: "text", Text: fmt.Sprintf("Remediation proposed for incident %s (id hint: %d). Awaiting oncall approval.", incidentID, id)}}}, nil

	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

func mcpMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// MCPManifest returns the MCP server manifest at GET /api/v1/mcp.
// This allows MCP clients to discover available tools.
func (h *MCPHandler) Manifest(c *gin.Context) {
	c.JSON(http.StatusOK, map[string]interface{}{
		"name":        "alerthub-mcp",
		"version":     "1.0.0",
		"description": "AlertHub AIOps platform MCP server — query incidents, RCA decisions, and investigations",
		"tools":       mcpTools,
		"updated_at":  time.Now().UTC(),
	})
}
