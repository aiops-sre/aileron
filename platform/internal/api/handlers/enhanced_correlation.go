package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/aileron-platform/aileron/platform/internal/services/correlation"
)

// EnhancedCorrelationHandler handles AI-powered correlation with fallback to traditional methods
type EnhancedCorrelationHandler struct {
	correlationEngine *correlation.CorrelationEngine
	aiCorrelationURL  string
	vectorServiceURL  string
	httpClient        *http.Client
	db                *sql.DB
}

// NewEnhancedCorrelationHandler creates a new enhanced correlation handler
func NewEnhancedCorrelationHandler(correlationEngine *correlation.CorrelationEngine, db *sql.DB) *EnhancedCorrelationHandler {
	return &EnhancedCorrelationHandler{
		correlationEngine: correlationEngine,
		aiCorrelationURL:  getEnv("AI_CORRELATION_URL", "http://ai-correlation-engine:8002"),
		vectorServiceURL:  getEnv("VECTOR_SERVICE_URL", "http://vector-embedding-service:8001"),
		db:                db,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// AI service data structures
type AICorrelationRequest struct {
	Alert              AIAlertData          `json:"alert"`
	CandidateIncidents []AIIncidentData     `json:"candidate_incidents"`
	Context            AICorrelationContext `json:"context,omitempty"`
	TrackAnalytics     bool                 `json:"track_analytics"`
}

type AIAlertData struct {
	ID          string                 `json:"id"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Severity    string                 `json:"severity"`
	Source      string                 `json:"source"`
	ServiceName string                 `json:"service_name"`
	Timestamp   time.Time              `json:"timestamp"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

type AIIncidentData struct {
	ID               string    `json:"id"`
	Title            string    `json:"title"`
	Description      string    `json:"description"`
	Severity         string    `json:"severity"`
	Status           string    `json:"status"`
	AffectedServices []string  `json:"affected_services"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type AICorrelationContext struct {
	Region            string `json:"region,omitempty"`
	Environment       string `json:"environment,omitempty"`
	TimeWindowMinutes int    `json:"time_window_minutes"`
	MaxIncidents      int    `json:"max_incidents"`
	IncludeResolved   bool   `json:"include_resolved"`
}

type AICorrelationResponse struct {
	CorrelationResult AICorrelationResult `json:"correlation_result"`
	ProcessingTimeMS  float64             `json:"processing_time_ms"`
	StrategiesUsed    []string            `json:"strategies_used"`
}

type AICorrelationResult struct {
	IsCorrelated      bool                   `json:"is_correlated"`
	IncidentID        string                 `json:"incident_id"`
	Confidence        float64                `json:"confidence"`
	StrategyScores    map[string]float64     `json:"strategy_scores"`
	DominantStrategy  string                 `json:"dominant_strategy"`
	Reasoning         string                 `json:"reasoning"`
	ConfidenceDetails map[string]interface{} `json:"confidence_details"`
}

// CorrelateAlertWithAI uses the AI Correlation Engine for intelligent correlation
func (h *EnhancedCorrelationHandler) CorrelateAlertWithAI(c *gin.Context) {
	alertIDStr := c.Param("id")
	alertID, err := uuid.Parse(alertIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid alert ID"})
		return
	}

	// Get alert data from request
	var alertReq struct {
		Title       string                 `json:"title" binding:"required"`
		Description string                 `json:"description"`
		Severity    string                 `json:"severity" binding:"required"`
		Source      string                 `json:"source" binding:"required"`
		ServiceName string                 `json:"service_name"`
		Timestamp   time.Time              `json:"timestamp"`
		Metadata    map[string]interface{} `json:"metadata"`
	}

	if err := c.ShouldBindJSON(&alertReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}

	// Set default timestamp if not provided
	if alertReq.Timestamp.IsZero() {
		alertReq.Timestamp = time.Now().UTC()
	}

	// Prepare AI correlation request
	aiAlert := AIAlertData{
		ID:          alertID.String(),
		Title:       alertReq.Title,
		Description: alertReq.Description,
		Severity:    alertReq.Severity,
		Source:      alertReq.Source,
		ServiceName: alertReq.ServiceName,
		Timestamp:   alertReq.Timestamp,
		Metadata:    alertReq.Metadata,
	}

	// Get candidate incidents from database
	candidateIncidents, err := h.getCandidateIncidents(c.Request.Context(), alertReq.ServiceName, alertReq.Timestamp)
	if err != nil {
		log.Printf("ERROR: Failed to get candidate incidents: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve candidate incidents"})
		return
	}

	// Prepare correlation context
	correlationContext := AICorrelationContext{
		Region:            getString(alertReq.Metadata, "region", ""),
		Environment:       getString(alertReq.Metadata, "environment", "production"),
		TimeWindowMinutes: 30,
		MaxIncidents:      10,
		IncludeResolved:   false,
	}

	correlationReq := AICorrelationRequest{
		Alert:              aiAlert,
		CandidateIncidents: candidateIncidents,
		Context:            correlationContext,
		TrackAnalytics:     true,
	}

	// Call AI Correlation Engine
	log.Printf("Calling AI Correlation Engine for alert: %s", alertReq.Title)
	aiResponse, err := h.callAICorrelationService(c.Request.Context(), correlationReq)
	if err != nil {
		log.Printf("ERROR: AI Correlation Engine call failed: %v", err)

		// Fallback to traditional correlation
		log.Printf("Falling back to traditional correlation engine...")
		fallbackAlert := &correlation.Alert{
			ID:          alertID,
			Title:       alertReq.Title,
			Description: alertReq.Description,
			Severity:    alertReq.Severity,
			Source:      alertReq.Source,
			SourceID:    alertID.String(),
			Tags:        []string{},
			Labels:      make(map[string]string),
			Metadata:    alertReq.Metadata,
			CreatedAt:   alertReq.Timestamp,
		}

		// Add service name to metadata for fallback
		if fallbackAlert.Metadata == nil {
			fallbackAlert.Metadata = make(map[string]interface{})
		}
		if alertReq.ServiceName != "" {
			fallbackAlert.Metadata["service_name"] = alertReq.ServiceName
		}

		fallbackResult, fallbackErr := h.correlationEngine.CorrelateAlert(c.Request.Context(), fallbackAlert)

		if fallbackErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Both AI and traditional correlation failed"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"correlation_method": "traditional_fallback",
				"result":             fallbackResult,
				"ai_service_error":   err.Error(),
			},
		})
		return
	}

	// Store AI correlation result in database for analytics
	err = h.storeAICorrelationResult(c.Request.Context(), alertID, aiResponse)
	if err != nil {
		log.Printf("WARNING: Failed to store AI correlation result: %v", err)
	}

	log.Printf("AI Correlation completed: confidence=%.2f, correlated=%v",
		aiResponse.CorrelationResult.Confidence, aiResponse.CorrelationResult.IsCorrelated)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"correlation_method": "ai_powered",
			"result":             aiResponse.CorrelationResult,
			"processing_time_ms": aiResponse.ProcessingTimeMS,
			"strategies_used":    aiResponse.StrategiesUsed,
			"ai_reasoning":       aiResponse.CorrelationResult.Reasoning,
		},
	})
}

// GetAICorrelationStrategies returns available AI correlation strategies
func (h *EnhancedCorrelationHandler) GetAICorrelationStrategies(c *gin.Context) {
	log.Printf("Fetching AI correlation strategies...")

	resp, err := h.httpClient.Get(h.aiCorrelationURL + "/strategies")
	if err != nil {
		log.Printf("ERROR: Failed to get AI strategies: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI Correlation Engine unavailable"})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI service returned error"})
		return
	}

	var strategiesResponse map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&strategiesResponse); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse AI response"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    strategiesResponse,
	})
}

// UpdateAICorrelationWeights updates correlation strategy weights
func (h *EnhancedCorrelationHandler) UpdateAICorrelationWeights(c *gin.Context) {
	var weightReq struct {
		Weights   map[string]float64 `json:"weights" binding:"required"`
		UpdatedBy string             `json:"updated_by" binding:"required"`
		Reason    string             `json:"reason"`
	}

	if err := c.ShouldBindJSON(&weightReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	// Validate weights sum to approximately 1.0
	total := 0.0
	for _, weight := range weightReq.Weights {
		total += weight
	}
	if total < 0.95 || total > 1.05 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Weights must sum to approximately 1.0"})
		return
	}

	// Call AI Correlation Engine to update weights
	requestBody, err := json.Marshal(weightReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to marshal request"})
		return
	}

	req, err := http.NewRequest("PUT", h.aiCorrelationURL+"/strategies/weights", bytes.NewBuffer(requestBody))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create request"})
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		log.Printf("ERROR: Failed to update AI strategy weights: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI Correlation Engine unavailable"})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "AI service rejected weight update"})
		return
	}

	var updateResponse map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&updateResponse); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse AI response"})
		return
	}

	log.Printf("AI correlation weights updated by %s", weightReq.UpdatedBy)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "AI correlation weights updated successfully",
		"data":    updateResponse,
	})
}

// SubmitCorrelationFeedback submits feedback to improve AI correlation accuracy
func (h *EnhancedCorrelationHandler) SubmitCorrelationFeedback(c *gin.Context) {
	var feedbackReq struct {
		CorrelationID      string    `json:"correlation_id" binding:"required"`
		AlertID            string    `json:"alert_id" binding:"required"`
		IncidentID         string    `json:"incident_id"`
		CorrectCorrelation bool      `json:"correct_correlation"`
		ExpectedIncidentID string    `json:"expected_incident_id"`
		ConfidenceRating   float64   `json:"confidence_rating" binding:"required,min=0,max=1"`
		FeedbackNotes      string    `json:"feedback_notes"`
		SubmittedBy        string    `json:"submitted_by" binding:"required"`
		SubmittedAt        time.Time `json:"submitted_at"`
	}

	if err := c.ShouldBindJSON(&feedbackReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	// Set timestamp if not provided
	if feedbackReq.SubmittedAt.IsZero() {
		feedbackReq.SubmittedAt = time.Now().UTC()
	}

	// Call AI Correlation Engine feedback endpoint
	requestBody, err := json.Marshal(feedbackReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to marshal feedback"})
		return
	}

	resp, err := h.httpClient.Post(h.aiCorrelationURL+"/feedback",
		"application/json", bytes.NewBuffer(requestBody))
	if err != nil {
		log.Printf("ERROR: Failed to submit AI correlation feedback: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI Correlation Engine unavailable"})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "AI service rejected feedback"})
		return
	}

	log.Printf("AI correlation feedback submitted by %s for correlation %s",
		feedbackReq.SubmittedBy, feedbackReq.CorrelationID)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Feedback submitted successfully - AI will improve from this input",
	})
}

// GetAICorrelationAnalytics returns AI correlation performance metrics
func (h *EnhancedCorrelationHandler) GetAICorrelationAnalytics(c *gin.Context) {
	timeframe := c.DefaultQuery("timeframe", "24h")

	resp, err := h.httpClient.Get(h.aiCorrelationURL + "/analytics/performance?timeframe=" + timeframe)
	if err != nil {
		log.Printf("ERROR: Failed to get AI analytics: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI Correlation Engine unavailable"})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI service error"})
		return
	}

	var analytics map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&analytics); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse AI analytics"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    analytics,
	})
}

// TestVectorSimilarity tests semantic similarity between two texts
func (h *EnhancedCorrelationHandler) TestVectorSimilarity(c *gin.Context) {
	var similarityReq struct {
		Text1     string `json:"text1" binding:"required"`
		Text2     string `json:"text2" binding:"required"`
		Normalize bool   `json:"normalize"`
	}

	if err := c.ShouldBindJSON(&similarityReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	// Call Vector Embedding Service
	requestBody, _ := json.Marshal(map[string]interface{}{
		"text1":     similarityReq.Text1,
		"text2":     similarityReq.Text2,
		"normalize": similarityReq.Normalize,
	})

	resp, err := h.httpClient.Post(h.vectorServiceURL+"/similarity",
		"application/json", bytes.NewBuffer(requestBody))
	if err != nil {
		log.Printf("ERROR: Vector service call failed: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Vector Embedding Service unavailable"})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Vector service error"})
		return
	}

	var similarityResponse map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&similarityResponse); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse vector response"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    similarityResponse,
	})
}

// Traditional correlation methods (backward compatibility)
func (h *EnhancedCorrelationHandler) GetAlertCorrelation(c *gin.Context) {
	alertIDStr := c.Param("id")
	alertID, err := uuid.Parse(alertIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid alert ID"})
		return
	}

	result, err := h.correlationEngine.GetCorrelationResult(c.Request.Context(), alertID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if result == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "No correlation data found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

// CorrelateAlert triggers traditional correlation analysis
func (h *EnhancedCorrelationHandler) CorrelateAlert(c *gin.Context) {
	alertIDStr := c.Param("id")
	alertID, err := uuid.Parse(alertIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid alert ID"})
		return
	}

	// Get alert from request body
	var alertReq struct {
		Title       string                 `json:"title"`
		Description string                 `json:"description"`
		Severity    string                 `json:"severity"`
		Source      string                 `json:"source"`
		SourceID    string                 `json:"source_id"`
		Tags        []string               `json:"tags"`
		Labels      map[string]string      `json:"labels"`
		Metadata    map[string]interface{} `json:"metadata"`
	}

	if err := c.ShouldBindJSON(&alertReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	// Create correlation alert object
	alert := &correlation.Alert{
		ID:          alertID,
		Title:       alertReq.Title,
		Description: alertReq.Description,
		Severity:    alertReq.Severity,
		Source:      alertReq.Source,
		SourceID:    alertReq.SourceID,
		Tags:        alertReq.Tags,
		Labels:      alertReq.Labels,
		Metadata:    alertReq.Metadata,
		CreatedAt:   time.Now().UTC(),
	}

	result, err := h.correlationEngine.CorrelateAlert(c.Request.Context(), alert)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

// Helper functions

func (h *EnhancedCorrelationHandler) callAICorrelationService(ctx context.Context, req AICorrelationRequest) (*AICorrelationResponse, error) {
	requestBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal correlation request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", h.aiCorrelationURL+"/correlate", bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := h.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("AI correlation service call failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("AI correlation service returned status %d", resp.StatusCode)
	}

	var aiResponse AICorrelationResponse
	if err := json.NewDecoder(resp.Body).Decode(&aiResponse); err != nil {
		return nil, fmt.Errorf("failed to decode AI correlation response: %w", err)
	}

	return &aiResponse, nil
}

func (h *EnhancedCorrelationHandler) getCandidateIncidents(ctx context.Context, serviceName string, alertTime time.Time) ([]AIIncidentData, error) {
	// Query recent incidents that might be related
	timeWindow := alertTime.Add(-1 * time.Hour) // Look back 1 hour

	query := `
		SELECT id, title, description, severity, status, affected_services, created_at, updated_at
		FROM incidents 
		WHERE status IN ('open', 'investigating', 'acknowledged')
		  AND updated_at >= $1
		  AND ($2 = '' OR $2 = ANY(affected_services) OR affected_services && ARRAY[$2])
		ORDER BY updated_at DESC
		LIMIT 10
	`

	rows, err := h.db.QueryContext(ctx, query, timeWindow, serviceName)
	if err != nil {
		return nil, fmt.Errorf("failed to query candidate incidents: %w", err)
	}
	defer rows.Close()

	var candidates []AIIncidentData
	for rows.Next() {
		var incident AIIncidentData
		var affectedServicesJSON []byte
		var description sql.NullString

		err := rows.Scan(
			&incident.ID, &incident.Title, &description, &incident.Severity,
			&incident.Status, &affectedServicesJSON, &incident.CreatedAt, &incident.UpdatedAt,
		)
		if err != nil {
			continue
		}

		incident.Description = description.String
		json.Unmarshal(affectedServicesJSON, &incident.AffectedServices)
		candidates = append(candidates, incident)
	}

	return candidates, nil
}

func (h *EnhancedCorrelationHandler) storeAICorrelationResult(ctx context.Context, alertID uuid.UUID, result *AICorrelationResponse) error {
	// Store correlation result in database for analytics and auditing
	strategyScoresJSON, _ := json.Marshal(result.CorrelationResult.StrategyScores)
	strategiesUsedJSON, _ := json.Marshal(result.StrategiesUsed)
	confidenceDetailsJSON, _ := json.Marshal(result.CorrelationResult.ConfidenceDetails)

	var incidentID *string
	if result.CorrelationResult.IncidentID != "" {
		incidentID = &result.CorrelationResult.IncidentID
	}

	query := `
		INSERT INTO ai_correlation_results 
		(id, alert_id, incident_id, confidence, strategies_used, strategy_scores, 
		 dominant_strategy, reasoning, confidence_details, processing_time_ms, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (alert_id) DO UPDATE SET
			incident_id = EXCLUDED.incident_id,
			confidence = EXCLUDED.confidence,
			strategies_used = EXCLUDED.strategies_used,
			strategy_scores = EXCLUDED.strategy_scores,
			dominant_strategy = EXCLUDED.dominant_strategy,
			reasoning = EXCLUDED.reasoning,
			confidence_details = EXCLUDED.confidence_details,
			processing_time_ms = EXCLUDED.processing_time_ms,
			updated_at = NOW()
	`

	_, err := h.db.ExecContext(ctx, query,
		uuid.New(),
		alertID,
		incidentID,
		result.CorrelationResult.Confidence,
		strategiesUsedJSON,
		strategyScoresJSON,
		result.CorrelationResult.DominantStrategy,
		result.CorrelationResult.Reasoning,
		confidenceDetailsJSON,
		result.ProcessingTimeMS,
		time.Now().UTC(),
	)

	return err
}

// RegisterRoutes registers enhanced correlation routes
func (h *EnhancedCorrelationHandler) RegisterRoutes(router *gin.RouterGroup) {
	correlation := router.Group("/correlation")
	{
		// Traditional correlation endpoints (backward compatibility)
		correlation.GET("/alert/:id", h.GetAlertCorrelation)
		correlation.POST("/alert/:id/correlate", h.CorrelateAlert)

		// NEW: AI-powered correlation endpoints
		correlation.POST("/ai/alert/:id/correlate", h.CorrelateAlertWithAI)
		correlation.GET("/ai/strategies", h.GetAICorrelationStrategies)
		correlation.PUT("/ai/strategies/weights", h.UpdateAICorrelationWeights)
		correlation.POST("/ai/feedback", h.SubmitCorrelationFeedback)
		correlation.GET("/ai/analytics", h.GetAICorrelationAnalytics)

		// Vector similarity testing endpoint
		correlation.POST("/ai/similarity/test", h.TestVectorSimilarity)

		// Health check for AI services
		correlation.GET("/ai/health", h.CheckAIServicesHealth)
	}
}

// CheckAIServicesHealth checks the health of AI correlation services
func (h *EnhancedCorrelationHandler) CheckAIServicesHealth(c *gin.Context) {
	healthStatus := make(map[string]interface{})

	// Check Vector Embedding Service
	vectorResp, err := h.httpClient.Get(h.vectorServiceURL + "/health")
	if err != nil {
		healthStatus["vector_service"] = map[string]interface{}{
			"status": "unhealthy",
			"error":  err.Error(),
		}
	} else {
		defer vectorResp.Body.Close()
		if vectorResp.StatusCode == http.StatusOK {
			var vectorHealth map[string]interface{}
			json.NewDecoder(vectorResp.Body).Decode(&vectorHealth)
			healthStatus["vector_service"] = vectorHealth
		} else {
			healthStatus["vector_service"] = map[string]interface{}{
				"status": "unhealthy",
				"code":   vectorResp.StatusCode,
			}
		}
	}

	// Check AI Correlation Engine
	corrResp, err := h.httpClient.Get(h.aiCorrelationURL + "/health")
	if err != nil {
		healthStatus["correlation_service"] = map[string]interface{}{
			"status": "unhealthy",
			"error":  err.Error(),
		}
	} else {
		defer corrResp.Body.Close()
		if corrResp.StatusCode == http.StatusOK {
			var corrHealth map[string]interface{}
			json.NewDecoder(corrResp.Body).Decode(&corrHealth)
			healthStatus["correlation_service"] = corrHealth
		} else {
			healthStatus["correlation_service"] = map[string]interface{}{
				"status": "unhealthy",
				"code":   corrResp.StatusCode,
			}
		}
	}

	// Determine overall health
	overallHealthy := true
	for _, service := range healthStatus {
		if serviceMap, ok := service.(map[string]interface{}); ok {
			if status, exists := serviceMap["status"]; exists && status != "healthy" {
				overallHealthy = false
				break
			}
		}
	}

	responseStatus := http.StatusOK
	if !overallHealthy {
		responseStatus = http.StatusServiceUnavailable
	}

	c.JSON(responseStatus, gin.H{
		"success":               overallHealthy,
		"overall_status":        map[string]bool{"healthy": overallHealthy}[fmt.Sprint(overallHealthy)],
		"services":              healthStatus,
		"ai_features_available": overallHealthy,
	})
}

// Utility functions
func getString(m map[string]interface{}, key, defaultValue string) string {
	if m == nil {
		return defaultValue
	}
	if val, ok := m[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return defaultValue
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
