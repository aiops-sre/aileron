package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// LLMInfraHandler handles LLM config, alert sources, infra, and correlation config endpoints
type LLMInfraHandler struct {
	DB            *sql.DB
	LLMServiceURL string
}

// NewLLMInfraHandler creates a new LLMInfraHandler
func NewLLMInfraHandler(db *sql.DB) *LLMInfraHandler {
	llmURL := os.Getenv("LLM_SERVICE_URL")
	if llmURL == "" {
		llmURL = "http://ollama.aileron.svc.cluster.local:11434"
	}
	return &LLMInfraHandler{DB: db, LLMServiceURL: llmURL}
}

// ============================================================================
// LLM Config CRUD
// ============================================================================

// GetLLMConfigs returns all LLM configs
// GET /api/v1/admin/llm/configs
func (h *LLMInfraHandler) GetLLMConfigs(c *gin.Context) {
	rows, err := h.DB.QueryContext(c.Request.Context(), `
		SELECT id, name, provider, model_name, endpoint_url,
		       max_tokens, temperature, enabled,
		       use_for_rca, use_for_correlation, use_for_remediation, use_for_summarization,
		       system_prompt, created_at, updated_at
		FROM llm_configs
		ORDER BY created_at DESC
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to query llm_configs: %v", err)})
		return
	}
	defer rows.Close()

	type LLMConfig struct {
		ID                  string     `json:"id"`
		Name                string     `json:"name"`
		Provider            string     `json:"provider"`
		ModelName           string     `json:"model_name"`
		EndpointURL         string     `json:"endpoint_url"`
		MaxTokens           int        `json:"max_tokens"`
		Temperature         float64    `json:"temperature"`
		Enabled             bool       `json:"enabled"`
		UseForRCA           bool       `json:"use_for_rca"`
		UseForCorrelation   bool       `json:"use_for_correlation"`
		UseForRemediation   bool       `json:"use_for_remediation"`
		UseForSummarization bool       `json:"use_for_summarization"`
		SystemPrompt        *string    `json:"system_prompt"`
		CreatedAt           time.Time  `json:"created_at"`
		UpdatedAt           time.Time  `json:"updated_at"`
	}

	configs := make([]LLMConfig, 0)
	for rows.Next() {
		var cfg LLMConfig
		if err := rows.Scan(
			&cfg.ID, &cfg.Name, &cfg.Provider, &cfg.ModelName, &cfg.EndpointURL,
			&cfg.MaxTokens, &cfg.Temperature, &cfg.Enabled,
			&cfg.UseForRCA, &cfg.UseForCorrelation, &cfg.UseForRemediation, &cfg.UseForSummarization,
			&cfg.SystemPrompt, &cfg.CreatedAt, &cfg.UpdatedAt,
		); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to scan llm_config row: %v", err)})
			return
		}
		configs = append(configs, cfg)
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": configs, "count": len(configs)})
}

// CreateLLMConfig creates a new LLM config
// POST /api/v1/admin/llm/configs
func (h *LLMInfraHandler) CreateLLMConfig(c *gin.Context) {
	var req struct {
		Name                string  `json:"name" binding:"required"`
		Provider            string  `json:"provider"`
		ModelName           string  `json:"model_name"`
		EndpointURL         string  `json:"endpoint_url"`
		APIKey              *string `json:"api_key"`
		MaxTokens           *int    `json:"max_tokens"`
		Temperature         *float64 `json:"temperature"`
		Enabled             *bool   `json:"enabled"`
		UseForRCA           *bool   `json:"use_for_rca"`
		UseForCorrelation   *bool   `json:"use_for_correlation"`
		UseForRemediation   *bool   `json:"use_for_remediation"`
		UseForSummarization *bool   `json:"use_for_summarization"`
		SystemPrompt        *string `json:"system_prompt"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Set defaults
	if req.Provider == "" {
		req.Provider = "ollama"
	}
	if req.ModelName == "" {
		req.ModelName = "phi3:mini"
	}
	if req.EndpointURL == "" {
		req.EndpointURL = "http://ollama:11434"
	}
	maxTokens := 2048
	if req.MaxTokens != nil {
		maxTokens = *req.MaxTokens
	}
	temperature := 0.1
	if req.Temperature != nil {
		temperature = *req.Temperature
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	useRCA := true
	if req.UseForRCA != nil {
		useRCA = *req.UseForRCA
	}
	useCorr := true
	if req.UseForCorrelation != nil {
		useCorr = *req.UseForCorrelation
	}
	useRem := true
	if req.UseForRemediation != nil {
		useRem = *req.UseForRemediation
	}
	useSum := true
	if req.UseForSummarization != nil {
		useSum = *req.UseForSummarization
	}

	id := uuid.New().String()
	now := time.Now()

	_, err := h.DB.ExecContext(c.Request.Context(), `
		INSERT INTO llm_configs
		    (id, name, provider, model_name, endpoint_url, api_key,
		     max_tokens, temperature, enabled,
		     use_for_rca, use_for_correlation, use_for_remediation, use_for_summarization,
		     system_prompt, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
	`, id, req.Name, req.Provider, req.ModelName, req.EndpointURL, req.APIKey,
		maxTokens, temperature, enabled,
		useRCA, useCorr, useRem, useSum,
		req.SystemPrompt, now, now)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to create llm_config: %v", err)})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": id, "message": "LLM config created"})
}

// UpdateLLMConfig updates an existing LLM config
// PUT /api/v1/admin/llm/configs/:id
func (h *LLMInfraHandler) UpdateLLMConfig(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id is required"})
		return
	}

	var req struct {
		Name                *string  `json:"name"`
		Provider            *string  `json:"provider"`
		ModelName           *string  `json:"model_name"`
		EndpointURL         *string  `json:"endpoint_url"`
		APIKey              *string  `json:"api_key"`
		MaxTokens           *int     `json:"max_tokens"`
		Temperature         *float64 `json:"temperature"`
		Enabled             *bool    `json:"enabled"`
		UseForRCA           *bool    `json:"use_for_rca"`
		UseForCorrelation   *bool    `json:"use_for_correlation"`
		UseForRemediation   *bool    `json:"use_for_remediation"`
		UseForSummarization *bool    `json:"use_for_summarization"`
		SystemPrompt        *string  `json:"system_prompt"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result, err := h.DB.ExecContext(c.Request.Context(), `
		UPDATE llm_configs SET
		    name                 = COALESCE($2, name),
		    provider             = COALESCE($3, provider),
		    model_name           = COALESCE($4, model_name),
		    endpoint_url         = COALESCE($5, endpoint_url),
		    api_key              = COALESCE($6, api_key),
		    max_tokens           = COALESCE($7, max_tokens),
		    temperature          = COALESCE($8, temperature),
		    enabled              = COALESCE($9, enabled),
		    use_for_rca          = COALESCE($10, use_for_rca),
		    use_for_correlation  = COALESCE($11, use_for_correlation),
		    use_for_remediation  = COALESCE($12, use_for_remediation),
		    use_for_summarization = COALESCE($13, use_for_summarization),
		    system_prompt        = COALESCE($14, system_prompt),
		    updated_at           = NOW()
		WHERE id = $1
	`, id, req.Name, req.Provider, req.ModelName, req.EndpointURL, req.APIKey,
		req.MaxTokens, req.Temperature, req.Enabled,
		req.UseForRCA, req.UseForCorrelation, req.UseForRemediation, req.UseForSummarization,
		req.SystemPrompt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to update llm_config: %v", err)})
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "llm config not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "LLM config updated"})
}

// DeleteLLMConfig deletes an LLM config
// DELETE /api/v1/admin/llm/configs/:id
func (h *LLMInfraHandler) DeleteLLMConfig(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id is required"})
		return
	}

	result, err := h.DB.ExecContext(c.Request.Context(), `DELETE FROM llm_configs WHERE id = $1`, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to delete llm_config: %v", err)})
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "llm config not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "LLM config deleted"})
}

// TestLLMConfig tests connectivity to an LLM endpoint
// POST /api/v1/admin/llm/test
func (h *LLMInfraHandler) TestLLMConfig(c *gin.Context) {
	var req struct {
		EndpointURL string `json:"endpoint_url" binding:"required"`
		ModelName   string `json:"model_name"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.ModelName == "" {
		req.ModelName = "phi3:mini"
	}

	testURL := fmt.Sprintf("%s/api/tags", req.EndpointURL)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(testURL)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"error":   fmt.Sprintf("connection failed: %v", err),
		})
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	c.JSON(http.StatusOK, gin.H{
		"success":     resp.StatusCode == http.StatusOK,
		"status_code": resp.StatusCode,
		"response":    string(body),
	})
}

// LLMStatus checks ollama connectivity by hitting /api/tags (no text generation).
// GET /api/v1/admin/llm/status
func (h *LLMInfraHandler) LLMStatus(c *gin.Context) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(h.LLMServiceURL + "/api/tags")
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"data":    gin.H{"connected": false, "error": err.Error()},
		})
		return
	}
	defer resp.Body.Close()

	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	json.NewDecoder(resp.Body).Decode(&tags)

	model := "phi3:mini"
	for _, m := range tags.Models {
		if m.Name != "" {
			model = m.Name
			break
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"connected": true, "model": model, "models": tags.Models},
	})
}

// QueryLLM sends a prompt to ollama and returns the response.
// POST /api/v1/admin/llm/query
func (h *LLMInfraHandler) QueryLLM(c *gin.Context) {
	var req struct {
		Query   string `json:"query"`
		AlertID string `json:"alert_id"`
		Context string `json:"context"`
		Model   string `json:"model"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	model := req.Model
	if model == "" {
		model = "phi3:mini"
	}

	prompt := req.Query
	if req.Context != "" {
		prompt = req.Context + "\n\n" + prompt
	}
	if req.AlertID != "" {
		prompt = "Alert ID: " + req.AlertID + "\n" + prompt
	}

	ollamaReq := map[string]interface{}{
		"model":  model,
		"prompt": prompt,
		"stream": false,
	}

	body, _ := json.Marshal(ollamaReq)
	// Use context.Background() with an explicit timeout — not c.Request.Context() — so the
	// ollama generation is not cancelled if the HTTP client disconnects mid-wait.
	ollamaCtx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ollamaCtx, http.MethodPost,
		h.LLMServiceURL+"/api/generate", bytes.NewBuffer(body))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build request"})
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("LLM service unreachable: %v", err)})
		return
	}
	defer resp.Body.Close()

	var ollamaResp struct {
		Response string `json:"response"`
		Model    string `json:"model"`
		Done     bool   `json:"done"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to parse LLM response"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"response": ollamaResp.Response,
			"model":    ollamaResp.Model,
		},
	})
}

// ============================================================================
// Alert Sources CRUD
// ============================================================================

// GetAlertSources returns all alert sources
// GET /api/v1/admin/alert-sources
func (h *LLMInfraHandler) GetAlertSources(c *gin.Context) {
	rows, err := h.DB.QueryContext(c.Request.Context(), `
		SELECT id, name, source_type, display_name, endpoint_url,
		       enabled, polling_interval_seconds, last_poll_at, last_poll_status,
		       alerts_received_total, created_at, updated_at
		FROM alert_sources
		ORDER BY created_at DESC
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to query alert_sources: %v", err)})
		return
	}
	defer rows.Close()

	type AlertSource struct {
		ID                    string     `json:"id"`
		Name                  string     `json:"name"`
		SourceType            string     `json:"source_type"`
		DisplayName           *string    `json:"display_name"`
		EndpointURL           *string    `json:"endpoint_url"`
		Enabled               bool       `json:"enabled"`
		PollingIntervalSeconds int       `json:"polling_interval_seconds"`
		LastPollAt            *time.Time `json:"last_poll_at"`
		LastPollStatus        string     `json:"last_poll_status"`
		AlertsReceivedTotal   int        `json:"alerts_received_total"`
		CreatedAt             time.Time  `json:"created_at"`
		UpdatedAt             time.Time  `json:"updated_at"`
	}

	sources := make([]AlertSource, 0)
	for rows.Next() {
		var s AlertSource
		if err := rows.Scan(
			&s.ID, &s.Name, &s.SourceType, &s.DisplayName, &s.EndpointURL,
			&s.Enabled, &s.PollingIntervalSeconds, &s.LastPollAt, &s.LastPollStatus,
			&s.AlertsReceivedTotal, &s.CreatedAt, &s.UpdatedAt,
		); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to scan alert_source row: %v", err)})
			return
		}
		sources = append(sources, s)
	}

	c.JSON(http.StatusOK, gin.H{"data": sources, "count": len(sources)})
}

// CreateAlertSource creates a new alert source
// POST /api/v1/admin/alert-sources
func (h *LLMInfraHandler) CreateAlertSource(c *gin.Context) {
	var req struct {
		Name                   string          `json:"name" binding:"required"`
		SourceType             string          `json:"source_type" binding:"required"`
		DisplayName            *string         `json:"display_name"`
		EndpointURL            *string         `json:"endpoint_url"`
		APIKey                 *string         `json:"api_key"`
		Username               *string         `json:"username"`
		Password               *string         `json:"password"`
		WebhookSecret          *string         `json:"webhook_secret"`
		ExtraConfig            json.RawMessage `json:"extra_config"`
		Enabled                *bool           `json:"enabled"`
		PollingIntervalSeconds *int            `json:"polling_interval_seconds"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	pollingInterval := 60
	if req.PollingIntervalSeconds != nil {
		pollingInterval = *req.PollingIntervalSeconds
	}
	extraConfig := json.RawMessage(`{}`)
	if len(req.ExtraConfig) > 0 {
		extraConfig = req.ExtraConfig
	}

	id := uuid.New().String()
	now := time.Now()

	_, err := h.DB.ExecContext(c.Request.Context(), `
		INSERT INTO alert_sources
		    (id, name, source_type, display_name, endpoint_url,
		     api_key, username, password, webhook_secret,
		     extra_config, enabled, polling_interval_seconds,
		     last_poll_status, alerts_received_total, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'pending',0,$13,$14)
	`, id, req.Name, req.SourceType, req.DisplayName, req.EndpointURL,
		req.APIKey, req.Username, req.Password, req.WebhookSecret,
		extraConfig, enabled, pollingInterval, now, now)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to create alert_source: %v", err)})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": id, "message": "Alert source created"})
}

// UpdateAlertSource updates an alert source
// PUT /api/v1/admin/alert-sources/:id
func (h *LLMInfraHandler) UpdateAlertSource(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id is required"})
		return
	}

	var req struct {
		Name                   *string         `json:"name"`
		SourceType             *string         `json:"source_type"`
		DisplayName            *string         `json:"display_name"`
		EndpointURL            *string         `json:"endpoint_url"`
		APIKey                 *string         `json:"api_key"`
		Username               *string         `json:"username"`
		Password               *string         `json:"password"`
		WebhookSecret          *string         `json:"webhook_secret"`
		ExtraConfig            json.RawMessage `json:"extra_config"`
		Enabled                *bool           `json:"enabled"`
		PollingIntervalSeconds *int            `json:"polling_interval_seconds"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var extraConfigArg interface{}
	if len(req.ExtraConfig) > 0 {
		extraConfigArg = req.ExtraConfig
	}

	result, err := h.DB.ExecContext(c.Request.Context(), `
		UPDATE alert_sources SET
		    name                     = COALESCE($2, name),
		    source_type              = COALESCE($3, source_type),
		    display_name             = COALESCE($4, display_name),
		    endpoint_url             = COALESCE($5, endpoint_url),
		    api_key                  = COALESCE($6, api_key),
		    username                 = COALESCE($7, username),
		    password                 = COALESCE($8, password),
		    webhook_secret           = COALESCE($9, webhook_secret),
		    extra_config             = COALESCE($10, extra_config),
		    enabled                  = COALESCE($11, enabled),
		    polling_interval_seconds = COALESCE($12, polling_interval_seconds),
		    updated_at               = NOW()
		WHERE id = $1
	`, id, req.Name, req.SourceType, req.DisplayName, req.EndpointURL,
		req.APIKey, req.Username, req.Password, req.WebhookSecret,
		extraConfigArg, req.Enabled, req.PollingIntervalSeconds)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to update alert_source: %v", err)})
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "alert source not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Alert source updated"})
}

// DeleteAlertSource deletes an alert source
// DELETE /api/v1/admin/alert-sources/:id
func (h *LLMInfraHandler) DeleteAlertSource(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id is required"})
		return
	}

	result, err := h.DB.ExecContext(c.Request.Context(), `DELETE FROM alert_sources WHERE id = $1`, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to delete alert_source: %v", err)})
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "alert source not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Alert source deleted"})
}

// ============================================================================
// Infrastructure Regions
// ============================================================================

// GetInfraRegions returns all infrastructure regions
// GET /api/v1/admin/infra/regions
func (h *LLMInfraHandler) GetInfraRegions(c *gin.Context) {
	rows, err := h.DB.QueryContext(c.Request.Context(), `
		SELECT id, name, display_name, location, region_type, bm_count, enabled, created_at
		FROM infra_regions
		ORDER BY name
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to query infra_regions: %v", err)})
		return
	}
	defer rows.Close()

	type InfraRegion struct {
		ID          string    `json:"id"`
		Name        string    `json:"name"`
		DisplayName *string   `json:"display_name"`
		Location    *string   `json:"location"`
		RegionType  string    `json:"region_type"`
		BMCount     int       `json:"bm_count"`
		Enabled     bool      `json:"enabled"`
		CreatedAt   time.Time `json:"created_at"`
	}

	regions := make([]InfraRegion, 0)
	for rows.Next() {
		var r InfraRegion
		if err := rows.Scan(
			&r.ID, &r.Name, &r.DisplayName, &r.Location, &r.RegionType,
			&r.BMCount, &r.Enabled, &r.CreatedAt,
		); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to scan infra_region row: %v", err)})
			return
		}
		regions = append(regions, r)
	}

	c.JSON(http.StatusOK, gin.H{"data": regions, "count": len(regions)})
}

// CreateInfraRegion creates a new infrastructure region
// POST /api/v1/admin/infra/regions
func (h *LLMInfraHandler) CreateInfraRegion(c *gin.Context) {
	var req struct {
		Name        string  `json:"name" binding:"required"`
		DisplayName *string `json:"display_name"`
		Location    *string `json:"location"`
		RegionType  *string `json:"region_type"`
		BMCount     *int    `json:"bm_count"`
		Enabled     *bool   `json:"enabled"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	regionType := "datacenter"
	if req.RegionType != nil {
		regionType = *req.RegionType
	}
	bmCount := 0
	if req.BMCount != nil {
		bmCount = *req.BMCount
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	id := uuid.New().String()
	now := time.Now()

	_, err := h.DB.ExecContext(c.Request.Context(), `
		INSERT INTO infra_regions (id, name, display_name, location, region_type, bm_count, enabled, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
	`, id, req.Name, req.DisplayName, req.Location, regionType, bmCount, enabled, now)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to create infra_region: %v", err)})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": id, "message": "Infra region created"})
}

// ============================================================================
// CloudStack Config CRUD
// ============================================================================

// GetCloudStackConfigs returns all CloudStack configs
// GET /api/v1/admin/infra/cloudstack
func (h *LLMInfraHandler) GetCloudStackConfigs(c *gin.Context) {
	rows, err := h.DB.QueryContext(c.Request.Context(), `
		SELECT id, region_id, name, api_url, zone_id,
		       enabled, last_sync, sync_status, vm_count, created_at, updated_at
		FROM cloudstack_configs
		ORDER BY created_at DESC
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to query cloudstack_configs: %v", err)})
		return
	}
	defer rows.Close()

	type CloudStackConfig struct {
		ID         string     `json:"id"`
		RegionID   *string    `json:"region_id"`
		Name       string     `json:"name"`
		APIURL     string     `json:"api_url"`
		ZoneID     *string    `json:"zone_id"`
		Enabled    bool       `json:"enabled"`
		LastSync   *time.Time `json:"last_sync"`
		SyncStatus string     `json:"sync_status"`
		VMCount    int        `json:"vm_count"`
		CreatedAt  time.Time  `json:"created_at"`
		UpdatedAt  time.Time  `json:"updated_at"`
	}

	configs := make([]CloudStackConfig, 0)
	for rows.Next() {
		var cfg CloudStackConfig
		if err := rows.Scan(
			&cfg.ID, &cfg.RegionID, &cfg.Name, &cfg.APIURL, &cfg.ZoneID,
			&cfg.Enabled, &cfg.LastSync, &cfg.SyncStatus, &cfg.VMCount,
			&cfg.CreatedAt, &cfg.UpdatedAt,
		); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to scan cloudstack_config row: %v", err)})
			return
		}
		configs = append(configs, cfg)
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": configs, "count": len(configs)})
}

// CreateCloudStackConfig creates a new CloudStack config
// POST /api/v1/admin/infra/cloudstack
func (h *LLMInfraHandler) CreateCloudStackConfig(c *gin.Context) {
	var req struct {
		RegionID  *string `json:"region_id"`
		Name      string  `json:"name" binding:"required"`
		APIURL    string  `json:"api_url" binding:"required"`
		APIKey    string  `json:"api_key" binding:"required"`
		SecretKey string  `json:"secret_key" binding:"required"`
		ZoneID    *string `json:"zone_id"`
		Enabled   *bool   `json:"enabled"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	id := uuid.New().String()
	now := time.Now()

	_, err := h.DB.ExecContext(c.Request.Context(), `
		INSERT INTO cloudstack_configs
		    (id, region_id, name, api_url, api_key, secret_key, zone_id,
		     enabled, sync_status, vm_count, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'pending',0,$9,$10)
	`, id, req.RegionID, req.Name, req.APIURL, req.APIKey, req.SecretKey, req.ZoneID,
		enabled, now, now)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to create cloudstack_config: %v", err)})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": id, "message": "CloudStack config created"})
}

// UpdateCloudStackConfig updates a CloudStack config
// PUT /api/v1/admin/infra/cloudstack/:id
func (h *LLMInfraHandler) UpdateCloudStackConfig(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id is required"})
		return
	}

	var req struct {
		RegionID  *string `json:"region_id"`
		Name      *string `json:"name"`
		APIURL    *string `json:"api_url"`
		APIKey    *string `json:"api_key"`
		SecretKey *string `json:"secret_key"`
		ZoneID    *string `json:"zone_id"`
		Enabled   *bool   `json:"enabled"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result, err := h.DB.ExecContext(c.Request.Context(), `
		UPDATE cloudstack_configs SET
		    region_id   = COALESCE($2, region_id),
		    name        = COALESCE($3, name),
		    api_url     = COALESCE($4, api_url),
		    api_key     = COALESCE($5, api_key),
		    secret_key  = COALESCE($6, secret_key),
		    zone_id     = COALESCE($7, zone_id),
		    enabled     = COALESCE($8, enabled),
		    updated_at  = NOW()
		WHERE id = $1
	`, id, req.RegionID, req.Name, req.APIURL, req.APIKey, req.SecretKey, req.ZoneID, req.Enabled)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to update cloudstack_config: %v", err)})
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "CloudStack config not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "CloudStack config updated"})
}

// DeleteCloudStackConfig deletes a CloudStack config
// DELETE /api/v1/admin/infra/cloudstack/:id
func (h *LLMInfraHandler) DeleteCloudStackConfig(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id is required"})
		return
	}

	result, err := h.DB.ExecContext(c.Request.Context(), `DELETE FROM cloudstack_configs WHERE id = $1`, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to delete cloudstack_config: %v", err)})
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "CloudStack config not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "CloudStack config deleted"})
}

// ============================================================================
// NetApp Config CRUD
// ============================================================================

// GetNetAppConfigs returns all NetApp configs
// GET /api/v1/admin/infra/netapp
func (h *LLMInfraHandler) GetNetAppConfigs(c *gin.Context) {
	rows, err := h.DB.QueryContext(c.Request.Context(), `
		SELECT id, region_id, name, management_url, cluster_name,
		       enabled, last_sync, total_capacity_tb, used_capacity_tb,
		       created_at, updated_at
		FROM netapp_configs
		ORDER BY created_at DESC
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to query netapp_configs: %v", err)})
		return
	}
	defer rows.Close()

	type NetAppConfig struct {
		ID              string     `json:"id"`
		RegionID        *string    `json:"region_id"`
		Name            string     `json:"name"`
		ManagementURL   string     `json:"management_url"`
		ClusterName     *string    `json:"cluster_name"`
		Enabled         bool       `json:"enabled"`
		LastSync        *time.Time `json:"last_sync"`
		TotalCapacityTB float64    `json:"total_capacity_tb"`
		UsedCapacityTB  float64    `json:"used_capacity_tb"`
		CreatedAt       time.Time  `json:"created_at"`
		UpdatedAt       time.Time  `json:"updated_at"`
	}

	configs := make([]NetAppConfig, 0)
	for rows.Next() {
		var cfg NetAppConfig
		if err := rows.Scan(
			&cfg.ID, &cfg.RegionID, &cfg.Name, &cfg.ManagementURL, &cfg.ClusterName,
			&cfg.Enabled, &cfg.LastSync, &cfg.TotalCapacityTB, &cfg.UsedCapacityTB,
			&cfg.CreatedAt, &cfg.UpdatedAt,
		); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to scan netapp_config row: %v", err)})
			return
		}
		configs = append(configs, cfg)
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": configs, "count": len(configs)})
}

// CreateNetAppConfig creates a new NetApp config
// POST /api/v1/admin/infra/netapp
func (h *LLMInfraHandler) CreateNetAppConfig(c *gin.Context) {
	var req struct {
		RegionID      *string `json:"region_id"`
		Name          string  `json:"name" binding:"required"`
		ManagementURL string  `json:"management_url" binding:"required"`
		Username      string  `json:"username" binding:"required"`
		Password      string  `json:"password" binding:"required"`
		ClusterName   *string `json:"cluster_name"`
		Enabled       *bool   `json:"enabled"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	id := uuid.New().String()
	now := time.Now()

	_, err := h.DB.ExecContext(c.Request.Context(), `
		INSERT INTO netapp_configs
		    (id, region_id, name, management_url, username, password,
		     cluster_name, enabled, total_capacity_tb, used_capacity_tb,
		     created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,0,0,$9,$10)
	`, id, req.RegionID, req.Name, req.ManagementURL, req.Username, req.Password,
		req.ClusterName, enabled, now, now)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to create netapp_config: %v", err)})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": id, "message": "NetApp config created"})
}

// UpdateNetAppConfig updates a NetApp config
// PUT /api/v1/admin/infra/netapp/:id
func (h *LLMInfraHandler) UpdateNetAppConfig(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id is required"})
		return
	}

	var req struct {
		RegionID        *string  `json:"region_id"`
		Name            *string  `json:"name"`
		ManagementURL   *string  `json:"management_url"`
		Username        *string  `json:"username"`
		Password        *string  `json:"password"`
		ClusterName     *string  `json:"cluster_name"`
		Enabled         *bool    `json:"enabled"`
		TotalCapacityTB *float64 `json:"total_capacity_tb"`
		UsedCapacityTB  *float64 `json:"used_capacity_tb"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result, err := h.DB.ExecContext(c.Request.Context(), `
		UPDATE netapp_configs SET
		    region_id         = COALESCE($2, region_id),
		    name              = COALESCE($3, name),
		    management_url    = COALESCE($4, management_url),
		    username          = COALESCE($5, username),
		    password          = COALESCE($6, password),
		    cluster_name      = COALESCE($7, cluster_name),
		    enabled           = COALESCE($8, enabled),
		    total_capacity_tb = COALESCE($9, total_capacity_tb),
		    used_capacity_tb  = COALESCE($10, used_capacity_tb),
		    updated_at        = NOW()
		WHERE id = $1
	`, id, req.RegionID, req.Name, req.ManagementURL, req.Username, req.Password,
		req.ClusterName, req.Enabled, req.TotalCapacityTB, req.UsedCapacityTB)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to update netapp_config: %v", err)})
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "NetApp config not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "NetApp config updated"})
}

// ============================================================================
// Correlation Engine Config
// ============================================================================

// GetCorrelationConfig returns all correlation engine config entries
// GET /api/v1/admin/correlation/config
func (h *LLMInfraHandler) GetCorrelationConfig(c *gin.Context) {
	rows, err := h.DB.QueryContext(c.Request.Context(), `
		SELECT id, config_key, config_value, description, updated_by, updated_at
		FROM correlation_engine_config
		ORDER BY config_key
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to query correlation_engine_config: %v", err)})
		return
	}
	defer rows.Close()

	type CorrelationConfigEntry struct {
		ID          string          `json:"id"`
		ConfigKey   string          `json:"config_key"`
		ConfigValue json.RawMessage `json:"config_value"`
		Description *string         `json:"description"`
		UpdatedBy   *string         `json:"updated_by"`
		UpdatedAt   time.Time       `json:"updated_at"`
	}

	entries := make([]CorrelationConfigEntry, 0)
	for rows.Next() {
		var e CorrelationConfigEntry
		if err := rows.Scan(
			&e.ID, &e.ConfigKey, &e.ConfigValue, &e.Description, &e.UpdatedBy, &e.UpdatedAt,
		); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to scan correlation_config row: %v", err)})
			return
		}
		entries = append(entries, e)
	}

	c.JSON(http.StatusOK, gin.H{"data": entries, "count": len(entries)})
}

// UpdateCorrelationConfig upserts a correlation engine config key
// PUT /api/v1/admin/correlation/config
func (h *LLMInfraHandler) UpdateCorrelationConfig(c *gin.Context) {
	// Get the calling user's ID from context (set by auth middleware)
	var userID *string
	if uid, exists := c.Get("user_id"); exists {
		if s, ok := uid.(string); ok {
			userID = &s
		}
	}

	var req struct {
		ConfigKey   string          `json:"config_key" binding:"required"`
		ConfigValue json.RawMessage `json:"config_value" binding:"required"`
		Description *string         `json:"description"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate JSON
	if !json.Valid(req.ConfigValue) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "config_value must be valid JSON"})
		return
	}

	_, err := h.DB.ExecContext(c.Request.Context(), `
		INSERT INTO correlation_engine_config (id, config_key, config_value, description, updated_by, updated_at)
		VALUES (uuid_generate_v4(), $1, $2, $3, $4::uuid, NOW())
		ON CONFLICT (config_key) DO UPDATE SET
		    config_value = EXCLUDED.config_value,
		    description  = COALESCE(EXCLUDED.description, correlation_engine_config.description),
		    updated_by   = EXCLUDED.updated_by,
		    updated_at   = NOW()
	`, req.ConfigKey, req.ConfigValue, req.Description, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to upsert correlation config: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Correlation config updated", "config_key": req.ConfigKey})
}
