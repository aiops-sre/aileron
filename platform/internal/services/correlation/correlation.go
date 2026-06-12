package correlation

import (
	"github.com/aileron-platform/aileron/platform/internal/shared/models"
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// CorrelationEngine handles alert correlation and deduplication with AI enhancement
type CorrelationEngine struct {
	db                     *sql.DB
	semanticEngine         *SemanticCorrelationEngine
	useSemanticCorrelation bool
}

// NewCorrelationEngine creates a new correlation engine with AI capabilities
func NewCorrelationEngine(db *sql.DB) *CorrelationEngine {
	semanticEngine := NewSemanticCorrelationEngine(db)

	engine := &CorrelationEngine{
		db:                     db,
		semanticEngine:         semanticEngine,
		useSemanticCorrelation: true,
	}

	// Initialize semantic correlation (may run in lightweight mode if BERT unavailable)
	go func() {
		ctx := context.Background()
		if err := semanticEngine.InitializeModel(ctx); err != nil {
			log.Printf("Semantic correlation initialization warning: %v", err)
		}
	}()

	return engine
}

// Use the unified Alert model from shared package
type Alert = models.Alert

// CorrelationResult represents the result of correlation analysis
type CorrelationResult struct {
	AlertID           uuid.UUID   `json:"alert_id"`
	CorrelationID     string      `json:"correlation_id"`
	SimilarAlerts     []uuid.UUID `json:"similar_alerts"`
	ConfidenceScore   float64     `json:"confidence_score"`
	CorrelationType   string      `json:"correlation_type"`
	RecommendedAction string      `json:"recommended_action"`
	IsDuplicate       bool        `json:"is_duplicate"`
	DuplicateOf       *uuid.UUID  `json:"duplicate_of,omitempty"`
}

// CorrelationRule defines rules for alert correlation
type CorrelationRule struct {
	ID          uuid.UUID              `json:"id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Conditions  []RuleCondition        `json:"conditions"`
	Actions     []RuleAction           `json:"actions"`
	Priority    int                    `json:"priority"`
	Enabled     bool                   `json:"enabled"`
	Metadata    map[string]interface{} `json:"metadata"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

// RuleCondition represents a condition in a correlation rule
type RuleCondition struct {
	Field    string      `json:"field"`
	Operator string      `json:"operator"`
	Value    interface{} `json:"value"`
	Weight   float64     `json:"weight"`
}

// RuleAction represents an action to take when rule matches
type RuleAction struct {
	Type       string                 `json:"type"`
	Parameters map[string]interface{} `json:"parameters"`
}

// SimilarityMetrics holds similarity calculation results
type SimilarityMetrics struct {
	TitleSimilarity       float64 `json:"title_similarity"`
	DescriptionSimilarity float64 `json:"description_similarity"`
	SourceSimilarity      float64 `json:"source_similarity"`
	TagSimilarity         float64 `json:"tag_similarity"`
	LabelSimilarity       float64 `json:"label_similarity"`
	TimeSimilarity        float64 `json:"time_similarity"`
	OverallSimilarity     float64 `json:"overall_similarity"`
}

// CorrelateAlert performs correlation analysis on a new alert with AI enhancement AND topology awareness
func (ce *CorrelationEngine) CorrelateAlert(ctx context.Context, alert *Alert) (*CorrelationResult, error) {
	log.Printf("[CorrelationEngine] Starting TOPOLOGY-AWARE multi-layer correlation for alert %s: %s", alert.ID, alert.Title)

	// Step 1: Generate enhanced fingerprint
	alert.Fingerprint = ce.generateEnhancedFingerprint(alert)

	// Step 2: Check for exact duplicates (same fingerprint within time window)
	duplicateResult, err := ce.checkForDuplicates(ctx, alert)
	if err != nil {
		log.Printf("[CorrelationEngine] Error checking duplicates: %v", err)
	} else if duplicateResult != nil {
		log.Printf("[CorrelationEngine] Found duplicate alert: %s", duplicateResult.DuplicateOf)
		return duplicateResult, nil
	}

	// Step 2.5: Rule-based correlation — runs FIRST to assign stable, rule-scoped
	// correlation IDs. This ensures all alerts matching the same rule (e.g. every
	// Kafka alert) share a single correlation_id and land in one incident, regardless
	// of what the AI or infra engines subsequently compute.
	if stableID, ruleErr := ce.applyCorrelationRules(ctx, alert, nil); ruleErr == nil && strings.HasPrefix(stableID, "rule-") {
		log.Printf("[CorrelationEngine] Rule matched stable correlation_id=%s", stableID)
		result := &CorrelationResult{
			AlertID:           alert.ID,
			CorrelationID:     stableID,
			SimilarAlerts:     []uuid.UUID{},
			ConfidenceScore:   0.95,
			CorrelationType:   "rule_based",
			RecommendedAction: "create_incident",
		}
		if storeErr := ce.storeCorrelationResult(ctx, result); storeErr != nil {
			log.Printf("[CorrelationEngine] Error storing rule correlation result: %v", storeErr)
		}
		return result, nil
	}

	// Step 3: Try infrastructure/topology correlation (highest confidence for infra-labeled alerts)
	infraResult, err := ce.correlateByInfrastructure(ctx, alert)
	if err != nil {
		log.Printf("[CorrelationEngine] Infrastructure correlation error: %v", err)
	} else if infraResult != nil && infraResult.ConfidenceScore > 0.8 {
		log.Printf("[CorrelationEngine] HIGH-CONFIDENCE infrastructure correlation: %.2f (type: %s)", infraResult.ConfidenceScore, infraResult.CorrelationType)
		return infraResult, nil
	}

	// Step 4: Try AI-Enhanced Semantic Correlation
	if ce.useSemanticCorrelation && ce.semanticEngine != nil {
		log.Println("[CorrelationEngine] Running AI-enhanced semantic correlation...")
		aiResult, err := ce.semanticEngine.CorrelateAlertWithAI(ctx, alert)
		if err != nil {
			log.Printf("[CorrelationEngine] AI correlation error: %v, falling back to standard correlation", err)
		} else {
			return ce.convertAIResultToStandardResult(aiResult), nil
		}
	}

	// Step 5: Fallback to standard text-based correlation
	return ce.correlateAlertStandard(ctx, alert)
}

// NEW: correlateByInfrastructure uses CloudStack + K8s topology for infrastructure-aware correlation
func (ce *CorrelationEngine) correlateByInfrastructure(ctx context.Context, alert *Alert) (*CorrelationResult, error) {
	// Extract infrastructure context from alert labels and metadata
	var hostname, serviceName, podName, vmName, cluster, namespace string

	// Extract from labels
	if alert.Labels != nil {
		hostname = alert.Labels["host"]
		serviceName = alert.Labels["service"]
		podName = alert.Labels["pod"]
		vmName = alert.Labels["vm"]
		cluster = alert.Labels["cluster"]
		namespace = alert.Labels["namespace"]
	}

	// Extract from metadata (fallback)
	if alert.Metadata != nil {
		if hostname == "" {
			if h, ok := alert.Metadata["hostname"].(string); ok {
				hostname = h
			}
		}
		if serviceName == "" {
			if s, ok := alert.Metadata["service_name"].(string); ok {
				serviceName = s
			}
		}
		if podName == "" {
			if p, ok := alert.Metadata["pod_name"].(string); ok {
				podName = p
			}
		}
	}

	// If no infrastructure context, skip topology correlation
	if hostname == "" && serviceName == "" && podName == "" && vmName == "" {
		log.Printf("[CorrelationEngine] No infrastructure context in alert, skipping topology correlation")
		return nil, nil
	}

	log.Printf("[CorrelationEngine] Infrastructure context detected: host=%s, vm=%s, service=%s, pod=%s, cluster=%s, ns=%s",
		hostname, vmName, serviceName, podName, cluster, namespace)

	// PRIORITY 1: Same host/VM (highest confidence - infrastructure layer)
	// Correlates: BM disk failure VM I/O issues Pod errors
	if hostname != "" || vmName != "" {
		relatedAlerts, err := ce.findAlertsByInfrastructure(ctx, hostname, vmName, alert.ID)
		if err == nil && len(relatedAlerts) > 0 {
			log.Printf("[CorrelationEngine] Found %d alerts on SAME INFRASTRUCTURE (host: %s, vm: %s) - CASCADING FAILURE", len(relatedAlerts), hostname, vmName)

			return &CorrelationResult{
				AlertID:           alert.ID,
				CorrelationID:     fmt.Sprintf("infra-%s-%d", hostname, alert.CreatedAt.Unix()),
				SimilarAlerts:     relatedAlerts,
				ConfidenceScore:   0.95, // 95% confidence - infrastructure correlation is highly reliable
				CorrelationType:   "infrastructure_cascade",
				RecommendedAction: "merge_with_existing", // Add to existing infrastructure incident
				IsDuplicate:       false,
			}, nil
		}
	}

	// PRIORITY 2: Same service (service layer correlation)
	// Correlates: Database connection pool Database timeout Database slow query
	if serviceName != "" {
		serviceAlerts, err := ce.findAlertsByService(ctx, serviceName, alert.ID)
		if err == nil && len(serviceAlerts) > 0 {
			log.Printf("[CorrelationEngine] Found %d alerts for SAME SERVICE: %s - SERVICE DEGRADATION", len(serviceAlerts), serviceName)

			return &CorrelationResult{
				AlertID:           alert.ID,
				CorrelationID:     fmt.Sprintf("svc-%s-%d", serviceName, alert.CreatedAt.Unix()),
				SimilarAlerts:     serviceAlerts,
				ConfidenceScore:   0.85, // 85% confidence - service correlation
				CorrelationType:   "service_degradation",
				RecommendedAction: "correlate",
				IsDuplicate:       false,
			}, nil
		}
	}

	// PRIORITY 3: Same Kubernetes pod/deployment (container layer)
	// Correlates: Pod CrashLoopBackOff Pod OOMKilled Pod ImagePullBackOff
	if podName != "" {
		podAlerts, err := ce.findAlertsByPod(ctx, podName, cluster, namespace, alert.ID)
		if err == nil && len(podAlerts) > 0 {
			log.Printf("[CorrelationEngine] Found %d alerts for SAME POD: %s in %s/%s - POD FAILURE", len(podAlerts), podName, cluster, namespace)

			return &CorrelationResult{
				AlertID:           alert.ID,
				CorrelationID:     fmt.Sprintf("pod-%s-%s-%d", cluster, podName, alert.CreatedAt.Unix()),
				SimilarAlerts:     podAlerts,
				ConfidenceScore:   0.90, // 90% confidence - pod correlation
				CorrelationType:   "kubernetes_pod_failure",
				RecommendedAction: "correlate",
				IsDuplicate:       false,
			}, nil
		}
	}

	// No high-confidence topology correlation found
	log.Printf("[CorrelationEngine] No high-confidence topology correlation found, continuing to semantic/standard correlation")
	return nil, nil
}

// NEW: Find alerts on same infrastructure (BM server or CloudStack VM)
func (ce *CorrelationEngine) findAlertsByInfrastructure(ctx context.Context, hostname, vmName string, excludeAlertID uuid.UUID) ([]uuid.UUID, error) {
	timeWindow := 2 * time.Hour // Look back 2 hours for infrastructure issues

	query := `
		SELECT id FROM alerts
		WHERE created_at > $1
		AND id != $2
		AND status IN ('open', 'acknowledged')
		AND (
			labels->>'host' = $3
			OR labels->>'vm' = $4
			OR metadata->>'hostname' = $3
			OR metadata->>'vm_name' = $4
		)
		ORDER BY created_at DESC
		LIMIT 20
	`

	rows, err := ce.db.QueryContext(ctx, query, time.Now().Add(-timeWindow), excludeAlertID, hostname, vmName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alertIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err == nil {
			alertIDs = append(alertIDs, id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("findAlertsByInfrastructure: %w", err)
	}

	return alertIDs, nil
}

// NEW: Find alerts for same service (service layer correlation)
func (ce *CorrelationEngine) findAlertsByService(ctx context.Context, serviceName string, excludeAlertID uuid.UUID) ([]uuid.UUID, error) {
	timeWindow := 2 * time.Hour

	query := `
		SELECT id FROM alerts
		WHERE created_at > $1
		AND id != $2
		AND status IN ('open', 'acknowledged')
		AND (
			labels->>'service' = $3
			OR metadata->>'service_name' = $3
		)
		ORDER BY created_at DESC
		LIMIT 20
	`

	rows, err := ce.db.QueryContext(ctx, query, time.Now().Add(-timeWindow), excludeAlertID, serviceName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alertIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err == nil {
			alertIDs = append(alertIDs, id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("findAlertsByService: %w", err)
	}

	return alertIDs, nil
}

// NEW: Find alerts for same Kubernetes pod (container layer correlation)
func (ce *CorrelationEngine) findAlertsByPod(ctx context.Context, podName, cluster, namespace string, excludeAlertID uuid.UUID) ([]uuid.UUID, error) {
	timeWindow := 1 * time.Hour // Shorter window for K8s (pods restart frequently)

	query := `
		SELECT id FROM alerts
		WHERE created_at > $1
		AND id != $2
		AND status IN ('open', 'acknowledged')
		AND (
			labels->>'pod' = $3
			OR metadata->>'pod_name' = $3
			OR (labels->>'cluster' = $4 AND labels->>'namespace' = $5)
		)
		ORDER BY created_at DESC
		LIMIT 10
	`

	rows, err := ce.db.QueryContext(ctx, query, time.Now().Add(-timeWindow), excludeAlertID, podName, cluster, namespace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alertIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err == nil {
			alertIDs = append(alertIDs, id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("findAlertsByPod: %w", err)
	}

	return alertIDs, nil
}

// correlateAlertStandard performs the original correlation logic
func (ce *CorrelationEngine) correlateAlertStandard(ctx context.Context, alert *Alert) (*CorrelationResult, error) {
	log.Printf("[CorrelationEngine] Running standard correlation for alert %s", alert.ID)

	// Step 1: Find similar alerts using traditional ML-based analysis
	similarAlerts, err := ce.findSimilarAlerts(ctx, alert)
	if err != nil {
		log.Printf("[CorrelationEngine] Error finding similar alerts: %v", err)
		similarAlerts = []uuid.UUID{}
	}

	// Step 2: Apply correlation rules
	correlationID, err := ce.applyCorrelationRules(ctx, alert, similarAlerts)
	if err != nil {
		log.Printf("[CorrelationEngine] Error applying correlation rules: %v", err)
		correlationID = ce.generateCorrelationID(alert)
	}

	// Step 3: Calculate confidence score
	confidenceScore := ce.calculateConfidenceScore(alert, similarAlerts)

	// Step 4: Determine recommended action
	recommendedAction := ce.determineRecommendedAction(alert, similarAlerts, confidenceScore)

	result := &CorrelationResult{
		AlertID:           alert.ID,
		CorrelationID:     correlationID,
		SimilarAlerts:     similarAlerts,
		ConfidenceScore:   confidenceScore,
		CorrelationType:   "standard_ml",
		RecommendedAction: recommendedAction,
		IsDuplicate:       false,
	}

	// Step 5: Store correlation result
	if err := ce.storeCorrelationResult(ctx, result); err != nil {
		log.Printf("[CorrelationEngine] Error storing correlation result: %v", err)
	}

	log.Printf("[CorrelationEngine] Standard correlation complete - ID: %s, Similar: %d, Confidence: %.2f",
		result.CorrelationID, len(result.SimilarAlerts), result.ConfidenceScore)

	return result, nil
}

// convertAIResultToStandardResult converts AI correlation result to standard format
func (ce *CorrelationEngine) convertAIResultToStandardResult(aiResult *EnhancedCorrelationResult) *CorrelationResult {
	// Extract similar alert IDs
	similarAlertIDs := make([]uuid.UUID, len(aiResult.SimilarAlerts))
	for i, similar := range aiResult.SimilarAlerts {
		similarAlertIDs[i] = similar.AlertID
	}

	return &CorrelationResult{
		AlertID:           aiResult.AlertID,
		CorrelationID:     aiResult.CorrelationID,
		SimilarAlerts:     similarAlertIDs,
		ConfidenceScore:   aiResult.ConfidenceScore,
		CorrelationType:   aiResult.CorrelationType,
		RecommendedAction: aiResult.RecommendedAction,
		IsDuplicate:       aiResult.IsDuplicate,
		DuplicateOf:       aiResult.DuplicateOf,
	}
}

// checkForDuplicates checks if an alert is a duplicate within the time window
func (ce *CorrelationEngine) checkForDuplicates(ctx context.Context, alert *Alert) (*CorrelationResult, error) {
	timeWindow := 30 * time.Minute // 30-minute deduplication window

	query := `
		SELECT id, created_at 
		FROM alerts 
		WHERE fingerprint = $1 
		AND created_at > $2 
		AND status != 'resolved'
		ORDER BY created_at DESC
		LIMIT 1
	`

	var existingID uuid.UUID
	var existingTime time.Time
	err := ce.db.QueryRowContext(ctx, query, alert.Fingerprint, alert.CreatedAt.Add(-timeWindow)).
		Scan(&existingID, &existingTime)

	if err == sql.ErrNoRows {
		return nil, nil // Not a duplicate
	}
	if err != nil {
		return nil, err
	}

	// Found a duplicate
	return &CorrelationResult{
		AlertID:           alert.ID,
		CorrelationID:     fmt.Sprintf("dup-%s", existingID.String()[:8]),
		SimilarAlerts:     []uuid.UUID{existingID},
		ConfidenceScore:   1.0,
		CorrelationType:   "exact_duplicate",
		RecommendedAction: "suppress",
		IsDuplicate:       true,
		DuplicateOf:       &existingID,
	}, nil
}

// findSimilarAlerts uses ML-based similarity to find related alerts
func (ce *CorrelationEngine) findSimilarAlerts(ctx context.Context, alert *Alert) ([]uuid.UUID, error) {
	lookbackWindow := 24 * time.Hour // Look back 24 hours for similar alerts
	similarityThreshold := 0.7       // 70% similarity threshold

	query := `
		SELECT id, title, description, source, source_id, tags, labels, metadata, created_at
		FROM alerts 
		WHERE created_at > $1 
		AND id != $2
		AND status != 'resolved'
		ORDER BY created_at DESC
		LIMIT 100
	`

	rows, err := ce.db.QueryContext(ctx, query, alert.CreatedAt.Add(-lookbackWindow), alert.ID) // LIMIT already in query
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var similarAlerts []uuid.UUID
	for rows.Next() {
		var candidate Alert
		var tagsJSON, labelsJSON, metadataJSON []byte

		err := rows.Scan(
			&candidate.ID, &candidate.Title, &candidate.Description,
			&candidate.Source, &candidate.SourceID, &tagsJSON, &labelsJSON,
			&metadataJSON, &candidate.CreatedAt,
		)
		if err != nil {
			continue
		}

		// Unmarshal JSON fields
		if err := json.Unmarshal(tagsJSON, &candidate.Tags); err != nil {
			log.Printf("correlation: failed to unmarshal tags for alert %s: %v", candidate.ID, err)
		}
		if err := json.Unmarshal(labelsJSON, &candidate.Labels); err != nil {
			log.Printf("correlation: failed to unmarshal labels for alert %s: %v", candidate.ID, err)
		}
		if err := json.Unmarshal(metadataJSON, &candidate.Metadata); err != nil {
			log.Printf("correlation: failed to unmarshal metadata for alert %s: %v", candidate.ID, err)
		}

		// calculate similarity
		similarity := ce.calculateSimilarity(alert, &candidate)
		if similarity.OverallSimilarity >= similarityThreshold {
			similarAlerts = append(similarAlerts, candidate.ID)
		}
	}

	return similarAlerts, rows.Err()
}

// calculateSimilarity computes similarity metrics between two alerts
func (ce *CorrelationEngine) calculateSimilarity(alert1, alert2 *Alert) *SimilarityMetrics {
	metrics := &SimilarityMetrics{}

	// Title similarity (Levenshtein distance normalized)
	metrics.TitleSimilarity = ce.stringSimilarity(alert1.Title, alert2.Title)

	// Description similarity
	metrics.DescriptionSimilarity = ce.stringSimilarity(alert1.Description, alert2.Description)

	// Source similarity
	if alert1.Source == alert2.Source {
		metrics.SourceSimilarity = 1.0
	} else {
		metrics.SourceSimilarity = 0.0
	}

	// Tag similarity (Jaccard index)
	metrics.TagSimilarity = ce.jaccardSimilarity(alert1.Tags, alert2.Tags)

	// Label similarity
	metrics.LabelSimilarity = ce.mapSimilarity(alert1.Labels, alert2.Labels)

	// Time similarity (closer in time = more similar)
	timeDiff := math.Abs(alert1.CreatedAt.Sub(alert2.CreatedAt).Hours())
	metrics.TimeSimilarity = math.Max(0, 1.0-timeDiff/24.0) // Normalize to 24 hours

	// Overall similarity (weighted average)
	weights := map[string]float64{
		"title":       0.25,
		"description": 0.20,
		"source":      0.15,
		"tags":        0.15,
		"labels":      0.15,
		"time":        0.10,
	}

	metrics.OverallSimilarity = weights["title"]*metrics.TitleSimilarity +
		weights["description"]*metrics.DescriptionSimilarity +
		weights["source"]*metrics.SourceSimilarity +
		weights["tags"]*metrics.TagSimilarity +
		weights["labels"]*metrics.LabelSimilarity +
		weights["time"]*metrics.TimeSimilarity

	return metrics
}

// stringSimilarity calculates similarity between two strings using normalized Levenshtein distance
func (ce *CorrelationEngine) stringSimilarity(s1, s2 string) float64 {
	if s1 == s2 {
		return 1.0
	}
	if len(s1) == 0 || len(s2) == 0 {
		return 0.0
	}

	distance := ce.levenshteinDistance(strings.ToLower(s1), strings.ToLower(s2))
	maxLen := math.Max(float64(len(s1)), float64(len(s2)))
	return 1.0 - float64(distance)/maxLen
}

// levenshteinDistance calculates the Levenshtein distance between two strings
func (ce *CorrelationEngine) levenshteinDistance(s1, s2 string) int {
	if len(s1) == 0 {
		return len(s2)
	}
	if len(s2) == 0 {
		return len(s1)
	}

	matrix := make([][]int, len(s1)+1)
	for i := range matrix {
		matrix[i] = make([]int, len(s2)+1)
		matrix[i][0] = i
	}
	for j := 0; j <= len(s2); j++ {
		matrix[0][j] = j
	}

	for i := 1; i <= len(s1); i++ {
		for j := 1; j <= len(s2); j++ {
			cost := 0
			if s1[i-1] != s2[j-1] {
				cost = 1
			}
			matrix[i][j] = int(math.Min(math.Min(
				float64(matrix[i-1][j]+1),  // deletion
				float64(matrix[i][j-1]+1)), // insertion
				float64(matrix[i-1][j-1]+cost))) // substitution
		}
	}

	return matrix[len(s1)][len(s2)]
}

// jaccardSimilarity calculates Jaccard similarity for string slices
func (ce *CorrelationEngine) jaccardSimilarity(slice1, slice2 []string) float64 {
	if len(slice1) == 0 && len(slice2) == 0 {
		return 1.0
	}

	set1 := make(map[string]bool)
	for _, item := range slice1 {
		set1[item] = true
	}

	set2 := make(map[string]bool)
	for _, item := range slice2 {
		set2[item] = true
	}

	intersection := 0
	union := len(set1)

	for item := range set2 {
		if set1[item] {
			intersection++
		} else {
			union++
		}
	}

	if union == 0 {
		return 0.0
	}

	return float64(intersection) / float64(union)
}

// mapSimilarity calculates similarity between two maps
func (ce *CorrelationEngine) mapSimilarity(map1, map2 map[string]string) float64 {
	if len(map1) == 0 && len(map2) == 0 {
		return 1.0
	}

	allKeys := make(map[string]bool)
	for k := range map1 {
		allKeys[k] = true
	}
	for k := range map2 {
		allKeys[k] = true
	}

	matches := 0
	for key := range allKeys {
		val1, exists1 := map1[key]
		val2, exists2 := map2[key]
		if exists1 && exists2 && val1 == val2 {
			matches++
		}
	}

	return float64(matches) / float64(len(allKeys))
}

// generateEnhancedFingerprint creates a more sophisticated fingerprint
func (ce *CorrelationEngine) generateEnhancedFingerprint(alert *Alert) string {
	// Normalize title and description
	normalizedTitle := strings.ToLower(strings.TrimSpace(alert.Title))
	normalizedDesc := strings.ToLower(strings.TrimSpace(alert.Description))

	// Sort tags for consistent fingerprinting
	sortedTags := make([]string, len(alert.Tags))
	copy(sortedTags, alert.Tags)
	sort.Strings(sortedTags)

	// Create fingerprint components
	components := []string{
		alert.Source,
		normalizedTitle,
		normalizedDesc,
		strings.Join(sortedTags, ","),
		alert.Severity,
	}

	// Add important labels
	if alert.Labels != nil {
		if host, ok := alert.Labels["host"]; ok {
			components = append(components, "host:"+host)
		}
		if service, ok := alert.Labels["service"]; ok {
			components = append(components, "service:"+service)
		}
	}

	fingerprint := strings.Join(components, "|")
	hash := fmt.Sprintf("%x", md5.Sum([]byte(fingerprint)))
	return hash[:16] // Use first 16 characters
}

// applyCorrelationRules applies user-defined correlation rules
func (ce *CorrelationEngine) applyCorrelationRules(ctx context.Context, alert *Alert, similarAlerts []uuid.UUID) (string, error) {
	rules, err := ce.getActiveCorrelationRules(ctx)
	if err != nil {
		return ce.generateCorrelationID(alert), err
	}

	for _, rule := range rules {
		if ce.evaluateRule(rule, alert) {
			// Rule matched, generate correlation ID based on rule
			return fmt.Sprintf("rule-%s", rule.Name), nil
		}
	}

	return ce.generateCorrelationID(alert), nil
}

// generateCorrelationID generates a correlation ID for alerts
func (ce *CorrelationEngine) generateCorrelationID(alert *Alert) string {
	return fmt.Sprintf("corr-%s-%d", safePrefix(alert.Fingerprint, 8), alert.CreatedAt.Unix())
}

// safePrefix returns s[:n] if len(s) >= n, otherwise returns s unchanged.
func safePrefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// calculateConfidenceScore calculates confidence in correlation accuracy
func (ce *CorrelationEngine) calculateConfidenceScore(alert *Alert, similarAlerts []uuid.UUID) float64 {
	baseScore := 0.5 // Base confidence

	// Increase confidence based on similar alerts found
	if len(similarAlerts) > 0 {
		baseScore += 0.3
	}
	if len(similarAlerts) > 2 {
		baseScore += 0.2
	}

	// Increase confidence for known sources
	knownSources := map[string]bool{
		"dynatrace":  true,
		"prometheus": true,
		"grafana":    true,
		"datadog":    true,
	}
	if knownSources[alert.Source] {
		baseScore += 0.1
	}

	// Cap at 1.0
	if baseScore > 1.0 {
		baseScore = 1.0
	}

	return baseScore
}

// determineRecommendedAction suggests what to do with the correlation
func (ce *CorrelationEngine) determineRecommendedAction(alert *Alert, similarAlerts []uuid.UUID, confidence float64) string {
	if len(similarAlerts) == 0 {
		return "create_incident"
	}

	if confidence > 0.8 && len(similarAlerts) > 2 {
		return "merge_with_existing"
	}

	if alert.Severity == "critical" || alert.Severity == "high" {
		return "escalate"
	}

	return "correlate"
}

// storeCorrelationResult stores the correlation result in the database
func (ce *CorrelationEngine) storeCorrelationResult(ctx context.Context, result *CorrelationResult) error {
	similarAlertsJSON, _ := json.Marshal(result.SimilarAlerts)

	query := `
		INSERT INTO alert_correlations (
			id, alert_id, correlation_id, similar_alerts, confidence_score,
			correlation_type, recommended_action, is_duplicate, duplicate_of, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (alert_id) DO UPDATE SET
			correlation_id = EXCLUDED.correlation_id,
			similar_alerts = EXCLUDED.similar_alerts,
			confidence_score = EXCLUDED.confidence_score,
			correlation_type = EXCLUDED.correlation_type,
			recommended_action = EXCLUDED.recommended_action,
			updated_at = NOW()
	`

	_, err := ce.db.ExecContext(ctx, query,
		uuid.New(), result.AlertID, result.CorrelationID, similarAlertsJSON,
		result.ConfidenceScore, result.CorrelationType, result.RecommendedAction,
		result.IsDuplicate, result.DuplicateOf, time.Now(),
	)

	return err
}

// getActiveCorrelationRules retrieves active correlation rules
func (ce *CorrelationEngine) getActiveCorrelationRules(ctx context.Context) ([]*CorrelationRule, error) {
	query := `
		SELECT id, name, description, conditions, actions, priority, enabled, metadata, created_at, updated_at
		FROM correlation_rules 
		WHERE enabled = true 
		ORDER BY priority DESC
	`

	rows, err := ce.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []*CorrelationRule
	for rows.Next() {
		rule := &CorrelationRule{}
		var conditionsJSON, actionsJSON, metadataJSON []byte

		err := rows.Scan(
			&rule.ID, &rule.Name, &rule.Description, &conditionsJSON, &actionsJSON,
			&rule.Priority, &rule.Enabled, &metadataJSON, &rule.CreatedAt, &rule.UpdatedAt,
		)
		if err != nil {
			continue
		}

		if err := json.Unmarshal(conditionsJSON, &rule.Conditions); err != nil {
			log.Printf("correlation: failed to unmarshal conditions for rule %s: %v", rule.ID, err)
		}
		if err := json.Unmarshal(actionsJSON, &rule.Actions); err != nil {
			log.Printf("correlation: failed to unmarshal actions for rule %s: %v", rule.ID, err)
		}
		if err := json.Unmarshal(metadataJSON, &rule.Metadata); err != nil {
			log.Printf("correlation: failed to unmarshal metadata for rule %s: %v", rule.ID, err)
		}

		rules = append(rules, rule)
	}

	return rules, nil
}

// evaluateRule evaluates if a correlation rule matches an alert
func (ce *CorrelationEngine) evaluateRule(rule *CorrelationRule, alert *Alert) bool {
	for _, condition := range rule.Conditions {
		if !ce.evaluateCondition(condition, alert) {
			return false // All conditions must match
		}
	}
	return true
}

// evaluateCondition evaluates a single rule condition
func (ce *CorrelationEngine) evaluateCondition(condition RuleCondition, alert *Alert) bool {
	var fieldValue interface{}

	switch condition.Field {
	case "title":
		fieldValue = alert.Title
	case "description":
		fieldValue = alert.Description
	case "severity":
		fieldValue = alert.Severity
	case "source":
		fieldValue = alert.Source
	case "tags":
		fieldValue = alert.Tags
	default:
		// Check labels and metadata
		if alert.Labels != nil {
			if val, ok := alert.Labels[condition.Field]; ok {
				fieldValue = val
			}
		}
		if alert.Metadata != nil {
			if val, ok := alert.Metadata[condition.Field]; ok {
				fieldValue = val
			}
		}
	}

	return ce.matchCondition(fieldValue, condition.Operator, condition.Value)
}

// matchCondition checks if a field value matches the condition
func (ce *CorrelationEngine) matchCondition(fieldValue interface{}, operator string, conditionValue interface{}) bool {
	switch operator {
	case "equals":
		return fieldValue == conditionValue
	case "contains":
		if str, ok := fieldValue.(string); ok {
			if condStr, ok := conditionValue.(string); ok {
				return strings.Contains(strings.ToLower(str), strings.ToLower(condStr))
			}
		}
	case "starts_with":
		if str, ok := fieldValue.(string); ok {
			if condStr, ok := conditionValue.(string); ok {
				return strings.HasPrefix(strings.ToLower(str), strings.ToLower(condStr))
			}
		}
	case "regex":
		if str, ok := fieldValue.(string); ok {
			if pattern, ok := conditionValue.(string); ok {
				if re, err := regexp.Compile(pattern); err == nil {
					return re.MatchString(str)
				}
			}
		}
	case "in":
		switch items := conditionValue.(type) {
		case []interface{}:
			for _, item := range items {
				if fmt.Sprintf("%v", item) == fmt.Sprintf("%v", fieldValue) {
					return true
				}
			}
		case []string:
			target := fmt.Sprintf("%v", fieldValue)
			for _, item := range items {
				if item == target {
					return true
				}
			}
		}
	case "contains_any":
		if str, ok := fieldValue.(string); ok {
			lower := strings.ToLower(str)
			switch items := conditionValue.(type) {
			case []interface{}:
				for _, item := range items {
					if strings.Contains(lower, strings.ToLower(fmt.Sprintf("%v", item))) {
						return true
					}
				}
			case []string:
				for _, item := range items {
					if strings.Contains(lower, strings.ToLower(item)) {
						return true
					}
				}
			}
		}
	case "not_contains":
		if str, ok := fieldValue.(string); ok {
			if condStr, ok := conditionValue.(string); ok {
				return !strings.Contains(strings.ToLower(str), strings.ToLower(condStr))
			}
		}
	case "gte", "lte", "gt", "lt", "count_threshold", "time_window_minutes", "same_namespace":
		return true
	}
	return false
}

// GetCorrelationResult retrieves correlation result for an alert
func (ce *CorrelationEngine) GetCorrelationResult(ctx context.Context, alertID uuid.UUID) (*CorrelationResult, error) {
	query := `
		SELECT alert_id, correlation_id, similar_alerts, confidence_score,
			   correlation_type, recommended_action, is_duplicate, duplicate_of
		FROM alert_correlations
		WHERE alert_id = $1
	`

	result := &CorrelationResult{}
	var similarAlertsJSON []byte

	err := ce.db.QueryRowContext(ctx, query, alertID).Scan(
		&result.AlertID, &result.CorrelationID, &similarAlertsJSON,
		&result.ConfidenceScore, &result.CorrelationType,
		&result.RecommendedAction, &result.IsDuplicate, &result.DuplicateOf,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(similarAlertsJSON, &result.SimilarAlerts); err != nil {
		log.Printf("correlation: failed to unmarshal similar_alerts for result: %v", err)
	}
	return result, nil
}
