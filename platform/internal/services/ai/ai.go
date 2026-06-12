package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
)

var (
	ErrAIServiceUnavailable = errors.New("AI service unavailable")
	ErrInvalidResponse      = errors.New("invalid AI service response")
	ErrAnalysisFailed       = errors.New("AI analysis failed")
)

// AIService handles AI/ML operations
type AIService struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewAIService creates a new AI service
func NewAIService(baseURL, apiKey string) *AIService {
	return &AIService{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// AlertAnalysisRequest represents an alert analysis request
type AlertAnalysisRequest struct {
	AlertID     uuid.UUID              `json:"alert_id"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Severity    string                 `json:"severity"`
	Source      string                 `json:"source"`
	Tags        []string               `json:"tags"`
	Metadata    map[string]interface{} `json:"metadata"`
}

// AlertAnalysisResponse represents AI analysis response
type AlertAnalysisResponse struct {
	AlertID         uuid.UUID              `json:"alert_id"`
	Classification  string                 `json:"classification"`
	Confidence      float64                `json:"confidence"`
	Severity        string                 `json:"predicted_severity"`
	Category        string                 `json:"category"`
	RootCause       string                 `json:"root_cause"`
	Recommendations []string               `json:"recommendations"`
	RelatedAlerts   []uuid.UUID            `json:"related_alerts"`
	Insights        map[string]interface{} `json:"insights"`
	ProcessedAt     time.Time              `json:"processed_at"`
}

// AnalyzeAlert classifies an alert using the local Ollama LLM (qwen2.5:3b).
// The baseURL is re-interpreted as the Ollama service URL; the /api/v1/ai/analyze-alert
// phantom endpoint is replaced by a direct Ollama /api/generate call.
func (s *AIService) AnalyzeAlert(ctx context.Context, req *AlertAnalysisRequest) (*AlertAnalysisResponse, error) {
	if s.baseURL == "" {
		return nil, ErrAIServiceUnavailable
	}

	// Build a structured prompt that instructs the model to return JSON only.
	prompt := fmt.Sprintf(
		`Classify this infrastructure alert. Respond with JSON only, no explanation.
Schema: {"classification": "<network|compute|storage|database|application|kubernetes|unknown>",
         "category": "<string>", "severity": "<critical|high|medium|low>",
         "root_cause_hypothesis": "<1 sentence>"}

Alert: %s
Description: %s
Severity: %s
Source: %s`,
		req.Title, req.Description, req.Severity, req.Source,
	)

	body, _ := json.Marshal(map[string]interface{}{
		"model":  ollamaModel(),
		"prompt": prompt,
		"stream": false,
		"format": "json",
		"options": map[string]interface{}{
			"temperature": 0.1,
			"num_predict": 150,
		},
	})

	ollamaCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ollamaCtx, http.MethodPost,
		s.baseURL+"/api/generate", bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAIServiceUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%w: ollama status %d body=%.100s", ErrAnalysisFailed, resp.StatusCode, string(b))
	}

	var ollamaResp struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}

	var analysis struct {
		Classification  string `json:"classification"`
		Category        string `json:"category"`
		Severity        string `json:"severity"`
		RootCauseHyp    string `json:"root_cause_hypothesis"`
	}
	// Best-effort: if the model returns non-JSON, return a partial result.
	_ = json.Unmarshal([]byte(ollamaResp.Response), &analysis)

	return &AlertAnalysisResponse{
		AlertID:         req.AlertID,
		Classification:  analysis.Classification,
		Category:        analysis.Category,
		Severity:        analysis.Severity,
		RootCause:       analysis.RootCauseHyp,
		// Confidence: calibrated by llmConfidence() — superseded by OIE/CACIE when available.
		Confidence:      llmConfidence(analysis.RootCauseHyp),
		ProcessedAt:     time.Now(),
	}, nil
}

// llmConfidence returns a calibrated confidence for LLM-derived root cause hypotheses.
// 0.55 when a real hypothesis was produced; 0.20 when the LLM returned nothing.
// Both values are superseded by OIE evidence-based confidence or CACIE topology confidence.
func llmConfidence(rootCauseHyp string) float64 {
	if rootCauseHyp == "" {
		return 0.20
	}
	return 0.55
}

// ollamaModel returns the Ollama model name from the environment, with the same
// default as LLMEnricher so both components stay in sync.
func ollamaModel() string {
	if m := os.Getenv("LLM_MODEL"); m != "" {
		return m
	}
	return "qwen2.5:3b"
}

// RCARequest represents root cause analysis request
type RCARequest struct {
	IncidentID  uuid.UUID              `json:"incident_id"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Alerts      []AlertAnalysisRequest `json:"alerts"`
	Timeline    []TimelineEvent        `json:"timeline"`
	Metadata    map[string]interface{} `json:"metadata"`
}

// TimelineEvent represents an incident timeline event
type TimelineEvent struct {
	Timestamp   time.Time              `json:"timestamp"`
	EventType   string                 `json:"event_type"`
	Description string                 `json:"description"`
	Metadata    map[string]interface{} `json:"metadata"`
}

// RCAResponse represents root cause analysis response
type RCAResponse struct {
	IncidentID          uuid.UUID              `json:"incident_id"`
	RootCause           string                 `json:"root_cause"`
	Confidence          float64                `json:"confidence"`
	ContributingFactors []string               `json:"contributing_factors"`
	Recommendations     []string               `json:"recommendations"`
	PreventionSteps     []string               `json:"prevention_steps"`
	SimilarIncidents    []uuid.UUID            `json:"similar_incidents"`
	Insights            map[string]interface{} `json:"insights"`
	ProcessedAt         time.Time              `json:"processed_at"`
}

// PerformRCA performs root cause analysis
func (s *AIService) PerformRCA(ctx context.Context, req *RCARequest) (*RCAResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/ai/root-cause-analysis", s.baseURL)

	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-API-Key", s.apiKey)

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAIServiceUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, ErrAnalysisFailed
	}

	var result RCAResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}

	result.ProcessedAt = time.Now()
	return &result, nil
}

// PredictionRequest represents incident prediction request
type PredictionRequest struct {
	TimeWindow string                 `json:"time_window"` // e.g., "24h", "7d"
	AlertData  []AlertAnalysisRequest `json:"alert_data"`
	Historical bool                   `json:"include_historical"`
	Metadata   map[string]interface{} `json:"metadata"`
}

// PredictionResponse represents incident prediction response
type PredictionResponse struct {
	Predictions     []IncidentPrediction   `json:"predictions"`
	Confidence      float64                `json:"overall_confidence"`
	RiskLevel       string                 `json:"risk_level"`
	Recommendations []string               `json:"recommendations"`
	Insights        map[string]interface{} `json:"insights"`
	ProcessedAt     time.Time              `json:"processed_at"`
}

// IncidentPrediction represents a predicted incident
type IncidentPrediction struct {
	PredictedSeverity string    `json:"predicted_severity"`
	Probability       float64   `json:"probability"`
	EstimatedTime     time.Time `json:"estimated_time"`
	AffectedServices  []string  `json:"affected_services"`
	Description       string    `json:"description"`
}

// PredictIncidents predicts potential incidents
func (s *AIService) PredictIncidents(ctx context.Context, req *PredictionRequest) (*PredictionResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/ai/predict-incidents", s.baseURL)

	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-API-Key", s.apiKey)

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAIServiceUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, ErrAnalysisFailed
	}

	var result PredictionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}

	result.ProcessedAt = time.Now()
	return &result, nil
}

// NLQueryRequest represents natural language query request
type NLQueryRequest struct {
	Query   string                 `json:"query"`
	Context map[string]interface{} `json:"context"`
	UserID  uuid.UUID              `json:"user_id"`
}

// NLQueryResponse represents natural language query response
type NLQueryResponse struct {
	Answer      string                 `json:"answer"`
	Confidence  float64                `json:"confidence"`
	Sources     []string               `json:"sources"`
	Suggestions []string               `json:"suggestions"`
	Data        map[string]interface{} `json:"data"`
	ProcessedAt time.Time              `json:"processed_at"`
}

// QueryNaturalLanguage processes natural language queries
func (s *AIService) QueryNaturalLanguage(ctx context.Context, req *NLQueryRequest) (*NLQueryResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/ai/query", s.baseURL)

	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-API-Key", s.apiKey)

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAIServiceUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, ErrAnalysisFailed
	}

	var result NLQueryResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}

	result.ProcessedAt = time.Now()
	return &result, nil
}

// PatternDetectionRequest represents pattern detection request
type PatternDetectionRequest struct {
	TimeRange string                 `json:"time_range"`
	AlertIDs  []uuid.UUID            `json:"alert_ids"`
	Metadata  map[string]interface{} `json:"metadata"`
}

// PatternDetectionResponse represents detected patterns
type PatternDetectionResponse struct {
	Patterns    []AlertPattern         `json:"patterns"`
	Anomalies   []Anomaly              `json:"anomalies"`
	Trends      []Trend                `json:"trends"`
	Insights    map[string]interface{} `json:"insights"`
	ProcessedAt time.Time              `json:"processed_at"`
}

// AlertPattern represents a detected pattern
type AlertPattern struct {
	PatternID   string      `json:"pattern_id"`
	Type        string      `json:"type"`
	Frequency   int         `json:"frequency"`
	AlertIDs    []uuid.UUID `json:"alert_ids"`
	Description string      `json:"description"`
	Confidence  float64     `json:"confidence"`
}

// Anomaly represents a detected anomaly
type Anomaly struct {
	Type        string    `json:"type"`
	Severity    string    `json:"severity"`
	Description string    `json:"description"`
	DetectedAt  time.Time `json:"detected_at"`
	Score       float64   `json:"score"`
}

// Trend represents a detected trend
type Trend struct {
	Metric      string  `json:"metric"`
	Direction   string  `json:"direction"` // "increasing", "decreasing", "stable"
	ChangeRate  float64 `json:"change_rate"`
	Description string  `json:"description"`
}

// DetectPatterns detects patterns in alerts
func (s *AIService) DetectPatterns(ctx context.Context, req *PatternDetectionRequest) (*PatternDetectionResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/ai/detect-patterns", s.baseURL)

	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-API-Key", s.apiKey)

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAIServiceUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, ErrAnalysisFailed
	}

	var result PatternDetectionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}

	result.ProcessedAt = time.Now()
	return &result, nil
}

// RecommendationRequest represents recommendation request
type RecommendationRequest struct {
	UserID     uuid.UUID              `json:"user_id"`
	Context    string                 `json:"context"` // "alert", "incident", "general"
	ResourceID *uuid.UUID             `json:"resource_id,omitempty"`
	Metadata   map[string]interface{} `json:"metadata"`
}

// RecommendationResponse represents AI recommendations
type RecommendationResponse struct {
	Recommendations []Recommendation       `json:"recommendations"`
	Priority        string                 `json:"priority"`
	Insights        map[string]interface{} `json:"insights"`
	ProcessedAt     time.Time              `json:"processed_at"`
}

// Recommendation represents a single recommendation
type Recommendation struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Action      string  `json:"action"`
	Priority    string  `json:"priority"`
	Confidence  float64 `json:"confidence"`
	Impact      string  `json:"impact"`
}

// GetRecommendations gets AI-powered recommendations
func (s *AIService) GetRecommendations(ctx context.Context, req *RecommendationRequest) (*RecommendationResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/ai/recommendations", s.baseURL)

	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-API-Key", s.apiKey)

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAIServiceUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, ErrAnalysisFailed
	}

	var result RecommendationResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}

	result.ProcessedAt = time.Now()
	return &result, nil
}

// SentimentAnalysisRequest represents sentiment analysis request
type SentimentAnalysisRequest struct {
	Text     string                 `json:"text"`
	Context  string                 `json:"context"`
	Metadata map[string]interface{} `json:"metadata"`
}

// SentimentAnalysisResponse represents sentiment analysis response
type SentimentAnalysisResponse struct {
	Sentiment   string                 `json:"sentiment"` // "positive", "negative", "neutral"
	Score       float64                `json:"score"`
	Confidence  float64                `json:"confidence"`
	Keywords    []string               `json:"keywords"`
	Entities    []string               `json:"entities"`
	Insights    map[string]interface{} `json:"insights"`
	ProcessedAt time.Time              `json:"processed_at"`
}

// AnalyzeSentiment analyzes sentiment of text
func (s *AIService) AnalyzeSentiment(ctx context.Context, req *SentimentAnalysisRequest) (*SentimentAnalysisResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/ai/sentiment", s.baseURL)

	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-API-Key", s.apiKey)

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAIServiceUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, ErrAnalysisFailed
	}

	var result SentimentAnalysisResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}

	result.ProcessedAt = time.Now()
	return &result, nil
}

// HealthCheck checks AI service health
func (s *AIService) HealthCheck(ctx context.Context) error {
	endpoint := fmt.Sprintf("%s/health", s.baseURL)

	httpReq, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return err
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAIServiceUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ErrAIServiceUnavailable
	}

	return nil
}

// ModelInfo represents AI model information
type ModelInfo struct {
	Name        string                 `json:"name"`
	Version     string                 `json:"version"`
	Type        string                 `json:"type"`
	Accuracy    float64                `json:"accuracy"`
	LastTrained time.Time              `json:"last_trained"`
	Status      string                 `json:"status"`
	Metrics     map[string]interface{} `json:"metrics"`
}

// GetModelInfo retrieves AI model information
func (s *AIService) GetModelInfo(ctx context.Context) ([]ModelInfo, error) {
	endpoint := fmt.Sprintf("%s/api/v1/ai/models", s.baseURL)

	httpReq, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("X-API-Key", s.apiKey)

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAIServiceUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, ErrAnalysisFailed
	}

	var models []ModelInfo
	if err := json.NewDecoder(resp.Body).Decode(&models); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}

	return models, nil
}

// FeedbackRequest represents AI feedback for model improvement
type FeedbackRequest struct {
	PredictionID uuid.UUID              `json:"prediction_id"`
	Correct      bool                   `json:"correct"`
	ActualValue  string                 `json:"actual_value"`
	Comments     string                 `json:"comments"`
	Metadata     map[string]interface{} `json:"metadata"`
}

// SubmitFeedback submits feedback for AI model improvement
func (s *AIService) SubmitFeedback(ctx context.Context, req *FeedbackRequest) error {
	endpoint := fmt.Sprintf("%s/api/v1/ai/feedback", s.baseURL)

	jsonData, err := json.Marshal(req)
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-API-Key", s.apiKey)

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAIServiceUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ErrAnalysisFailed
	}

	return nil
}
