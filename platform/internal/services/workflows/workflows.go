package workflows

import (
	"github.com/aileron-platform/aileron/platform/internal/shared/interfaces"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrWorkflowNotFound = errors.New("workflow not found")
	ErrInvalidTrigger   = errors.New("invalid trigger configuration")
	ErrActionFailed     = errors.New("workflow action failed")
)

// WorkflowEngine handles workflow automation
type WorkflowEngine struct {
	db        *sql.DB
	executors map[string]ActionExecutor
	running   map[string]*WorkflowExecution
	mu        sync.RWMutex
}

// NewWorkflowEngine creates a new workflow engine
func NewWorkflowEngine(db *sql.DB) *WorkflowEngine {
	engine := &WorkflowEngine{
		db:        db,
		executors: make(map[string]ActionExecutor),
		running:   make(map[string]*WorkflowExecution),
	}

	// Register built-in action executors
	engine.RegisterExecutor("http", &HTTPActionExecutor{})
	engine.RegisterExecutor("email", &EmailActionExecutor{})
	engine.RegisterExecutor("slack", &SlackActionExecutor{})
	engine.RegisterExecutor("pagerduty", &PagerDutyActionExecutor{})
	engine.RegisterExecutor("webhook", &WebhookActionExecutor{})
	engine.RegisterExecutor("create_incident", &CreateIncidentActionExecutor{db: db})
	engine.RegisterExecutor("update_alert", &UpdateAlertActionExecutor{db: db})
	engine.RegisterExecutor("assign_alert", &AssignAlertActionExecutor{db: db})
	engine.RegisterExecutor("resolve_alert", &ResolveAlertActionExecutor{db: db})
	engine.RegisterExecutor("wait", &WaitActionExecutor{})
	engine.RegisterExecutor("condition", &ConditionActionExecutor{})

	return engine
}

// Workflow represents an automation workflow
type Workflow struct {
	ID          uuid.UUID              `json:"id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Triggers    []WorkflowTrigger      `json:"triggers"`
	Steps       []WorkflowStep         `json:"steps"`
	Enabled     bool                   `json:"enabled"`
	Metadata    map[string]interface{} `json:"metadata"`
	CreatedBy   uuid.UUID              `json:"created_by"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
	Version     int                    `json:"version"`
	Tags        []string               `json:"tags"`
}

// WorkflowTrigger defines when a workflow should be executed
type WorkflowTrigger struct {
	Type       string                 `json:"type"` // "alert", "incident", "schedule", "webhook", "manual"
	Conditions []TriggerCondition     `json:"conditions"`
	Schedule   *ScheduleConfig        `json:"schedule,omitempty"`
	Metadata   map[string]interface{} `json:"metadata"`
}

// TriggerCondition represents a condition for workflow execution
type TriggerCondition struct {
	Field    string      `json:"field"`
	Operator string      `json:"operator"`
	Value    interface{} `json:"value"`
}

// ScheduleConfig defines schedule-based triggers
type ScheduleConfig struct {
	Type     string `json:"type"` // "cron", "interval"
	Value    string `json:"value"`
	Timezone string `json:"timezone"`
}

// WorkflowStep represents a single step in a workflow
type WorkflowStep struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Type      string                 `json:"type"` // "action", "condition", "parallel", "sequential"
	Action    *WorkflowAction        `json:"action,omitempty"`
	Condition *StepCondition         `json:"condition,omitempty"`
	Steps     []WorkflowStep         `json:"steps,omitempty"` // For parallel/sequential steps
	OnSuccess *string                `json:"on_success,omitempty"`
	OnFailure *string                `json:"on_failure,omitempty"`
	Retry     *RetryConfig           `json:"retry,omitempty"`
	Timeout   *time.Duration         `json:"timeout,omitempty"`
	DependsOn []string               `json:"depends_on,omitempty"`
	Metadata  map[string]interface{} `json:"metadata"`
}

// WorkflowAction represents an action to be executed
type WorkflowAction struct {
	Type       string                 `json:"type"`
	Parameters map[string]interface{} `json:"parameters"`
	Outputs    map[string]string      `json:"outputs,omitempty"`
}

// StepCondition represents a condition within a step
type StepCondition struct {
	Expression string                 `json:"expression"`
	Variables  map[string]interface{} `json:"variables"`
}

// RetryConfig defines retry behavior for failed steps
type RetryConfig struct {
	MaxAttempts int           `json:"max_attempts"`
	Delay       time.Duration `json:"delay"`
	BackoffType string        `json:"backoff_type"` // "fixed", "exponential", "linear"
}

// WorkflowExecution represents a running workflow instance
type WorkflowExecution struct {
	ID           uuid.UUID              `json:"id"`
	WorkflowID   uuid.UUID              `json:"workflow_id"`
	Status       string                 `json:"status"` // "running", "completed", "failed", "cancelled"
	TriggerEvent map[string]interface{} `json:"trigger_event"`
	Context      map[string]interface{} `json:"context"`
	StepResults  map[string]StepResult  `json:"step_results"`
	StartedAt    time.Time              `json:"started_at"`
	CompletedAt  *time.Time             `json:"completed_at,omitempty"`
	Error        string                 `json:"error,omitempty"`
	Logs         []ExecutionLog         `json:"logs"`
}

// StepResult represents the result of executing a workflow step
type StepResult struct {
	Status      string                 `json:"status"` // "success", "failure", "skipped"
	Output      map[string]interface{} `json:"output"`
	Error       string                 `json:"error,omitempty"`
	StartedAt   time.Time              `json:"started_at"`
	CompletedAt time.Time              `json:"completed_at"`
	Duration    time.Duration          `json:"duration"`
	Attempts    int                    `json:"attempts"`
}

// ExecutionLog represents a log entry during workflow execution
type ExecutionLog struct {
	Timestamp time.Time              `json:"timestamp"`
	Level     string                 `json:"level"` // "info", "warn", "error", "debug"
	Message   string                 `json:"message"`
	StepID    string                 `json:"step_id,omitempty"`
	Data      map[string]interface{} `json:"data,omitempty"`
}

// ActionExecutor interface for workflow actions
type ActionExecutor interface {
	Execute(ctx context.Context, action *WorkflowAction, context map[string]interface{}) (map[string]interface{}, error)
	Validate(action *WorkflowAction) error
}

// RegisterExecutor registers an action executor
func (we *WorkflowEngine) RegisterExecutor(actionType string, executor ActionExecutor) {
	we.executors[actionType] = executor
}

// CreateWorkflow creates a new workflow
func (we *WorkflowEngine) CreateWorkflow(ctx context.Context, workflow *Workflow) error {
	workflow.ID = uuid.New()
	workflow.CreatedAt = time.Now()
	workflow.UpdatedAt = time.Now()
	workflow.Version = 1

	// Validate workflow
	if err := we.validateWorkflow(workflow); err != nil {
		return err
	}

	// Marshal JSON fields
	triggersJSON, _ := json.Marshal(workflow.Triggers)
	stepsJSON, _ := json.Marshal(workflow.Steps)
	metadataJSON, _ := json.Marshal(workflow.Metadata)
	tagsJSON, _ := json.Marshal(workflow.Tags)

	query := `
		INSERT INTO workflows (
			id, name, description, triggers, steps, enabled, metadata, 
			created_by, created_at, updated_at, version, tags
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`

	_, err := we.db.ExecContext(ctx, query,
		workflow.ID, workflow.Name, workflow.Description, triggersJSON, stepsJSON,
		workflow.Enabled, metadataJSON, workflow.CreatedBy, workflow.CreatedAt,
		workflow.UpdatedAt, workflow.Version, tagsJSON,
	)

	return err
}

// ExecuteWorkflow executes a workflow based on a trigger event
func (we *WorkflowEngine) ExecuteWorkflow(ctx context.Context, workflowID uuid.UUID, triggerEvent map[string]interface{}) (*WorkflowExecution, error) {
	// Get workflow
	workflow, err := we.GetWorkflow(ctx, workflowID)
	if err != nil {
		return nil, err
	}

	if !workflow.Enabled {
		return nil, fmt.Errorf("workflow %s is disabled", workflowID)
	}

	// Create execution
	execution := &WorkflowExecution{
		ID:           uuid.New(),
		WorkflowID:   workflowID,
		Status:       "running",
		TriggerEvent: triggerEvent,
		Context:      make(map[string]interface{}),
		StepResults:  make(map[string]StepResult),
		StartedAt:    time.Now(),
		Logs:         []ExecutionLog{},
	}

	// Initialize context with trigger event data
	execution.Context["trigger"] = triggerEvent
	execution.Context["workflow"] = map[string]interface{}{
		"id":   workflowID,
		"name": workflow.Name,
	}

	// Store execution
	we.mu.Lock()
	we.running[execution.ID.String()] = execution
	we.mu.Unlock()

	// Store in database
	if err := we.storeExecution(ctx, execution); err != nil {
		log.Printf("Failed to store workflow execution: %v", err)
	}

	// Execute workflow asynchronously
	go we.executeWorkflowAsync(ctx, workflow, execution)

	return execution, nil
}

// executeWorkflowAsync executes a workflow asynchronously
func (we *WorkflowEngine) executeWorkflowAsync(ctx context.Context, workflow *Workflow, execution *WorkflowExecution) {
	defer func() {
		we.mu.Lock()
		delete(we.running, execution.ID.String())
		we.mu.Unlock()

		// Update execution in database
		we.updateExecution(ctx, execution)
	}()

	we.addLog(execution, "info", "Starting workflow execution", "", nil)

	// Execute steps sequentially
	for i, step := range workflow.Steps {
		stepCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()

		if step.Timeout != nil {
			stepCtx, cancel = context.WithTimeout(ctx, *step.Timeout)
			defer cancel()
		}

		stepResult := we.executeStep(stepCtx, &step, execution)
		execution.StepResults[step.ID] = stepResult

		we.addLog(execution, "info", fmt.Sprintf("Completed step %d: %s", i+1, step.Name), step.ID, map[string]interface{}{
			"status": stepResult.Status,
		})

		// Handle step failure
		if stepResult.Status == "failure" {
			if step.OnFailure != nil {
				// Jump to failure step
				if failureStep := we.findStep(workflow.Steps, *step.OnFailure); failureStep != nil {
					we.executeStep(stepCtx, failureStep, execution)
				}
			}
			execution.Status = "failed"
			execution.Error = stepResult.Error
			return
		}

		// Update context with step outputs
		if stepResult.Output != nil {
			execution.Context[fmt.Sprintf("step_%s", step.ID)] = stepResult.Output
		}
	}

	execution.Status = "completed"
	now := time.Now()
	execution.CompletedAt = &now
	we.addLog(execution, "info", "Workflow execution completed successfully", "", nil)
}

// executeStep executes a single workflow step
func (we *WorkflowEngine) executeStep(ctx context.Context, step *WorkflowStep, execution *WorkflowExecution) StepResult {
	startTime := time.Now()
	result := StepResult{
		Status:    "success",
		Output:    make(map[string]interface{}),
		StartedAt: startTime,
		Attempts:  1,
	}

	defer func() {
		result.CompletedAt = time.Now()
		result.Duration = result.CompletedAt.Sub(result.StartedAt)
	}()

	// Check dependencies
	if len(step.DependsOn) > 0 {
		for _, depID := range step.DependsOn {
			if depResult, exists := execution.StepResults[depID]; !exists || depResult.Status != "success" {
				result.Status = "skipped"
				result.Error = fmt.Sprintf("Dependency %s not satisfied", depID)
				return result
			}
		}
	}

	// Execute step based on type
	switch step.Type {
	case "action":
		return we.executeAction(ctx, step.Action, execution, step)
	case "condition":
		return we.executeCondition(ctx, step.Condition, execution, step)
	case "parallel":
		return we.executeParallel(ctx, step.Steps, execution, step)
	case "sequential":
		return we.executeSequential(ctx, step.Steps, execution, step)
	default:
		result.Status = "failure"
		result.Error = fmt.Sprintf("Unknown step type: %s", step.Type)
		return result
	}
}

// executeAction executes a workflow action
func (we *WorkflowEngine) executeAction(ctx context.Context, action *WorkflowAction, execution *WorkflowExecution, step *WorkflowStep) StepResult {
	result := StepResult{
		Status:    "success",
		Output:    make(map[string]interface{}),
		StartedAt: time.Now(),
		Attempts:  1,
	}

	executor, exists := we.executors[action.Type]
	if !exists {
		result.Status = "failure"
		result.Error = fmt.Sprintf("No executor found for action type: %s", action.Type)
		return result
	}

	// Execute with retry if configured
	maxAttempts := 1
	if step.Retry != nil {
		maxAttempts = step.Retry.MaxAttempts
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		result.Attempts = attempt

		output, err := executor.Execute(ctx, action, execution.Context)
		if err == nil {
			result.Output = output
			result.Status = "success"
			return result
		}

		lastErr = err
		we.addLog(execution, "warn", fmt.Sprintf("Action attempt %d failed: %v", attempt, err), step.ID, nil)

		// Wait before retry (except on last attempt)
		if attempt < maxAttempts && step.Retry != nil {
			delay := step.Retry.Delay
			switch step.Retry.BackoffType {
			case "exponential":
				delay = time.Duration(int64(delay) * int64(attempt*attempt))
			case "linear":
				delay = time.Duration(int64(delay) * int64(attempt))
			}
			time.Sleep(delay)
		}
	}

	result.Status = "failure"
	result.Error = lastErr.Error()
	return result
}

// executeCondition executes a condition step
func (we *WorkflowEngine) executeCondition(ctx context.Context, condition *StepCondition, execution *WorkflowExecution, step *WorkflowStep) StepResult {
	result := StepResult{
		Status:    "success",
		Output:    make(map[string]interface{}),
		StartedAt: time.Now(),
		Attempts:  1,
	}

	// Simple expression evaluation (in production, use a proper expression engine)
	conditionResult := we.evaluateExpression(condition.Expression, execution.Context, condition.Variables)
	result.Output["result"] = conditionResult

	if !conditionResult {
		result.Status = "skipped"
	}

	return result
}

// executeParallel executes multiple steps in parallel
func (we *WorkflowEngine) executeParallel(ctx context.Context, steps []WorkflowStep, execution *WorkflowExecution, parentStep *WorkflowStep) StepResult {
	result := StepResult{
		Status:    "success",
		Output:    make(map[string]interface{}),
		StartedAt: time.Now(),
		Attempts:  1,
	}

	var wg sync.WaitGroup
	results := make([]StepResult, len(steps))

	for i, step := range steps {
		wg.Add(1)
		go func(index int, s WorkflowStep) {
			defer wg.Done()
			results[index] = we.executeStep(ctx, &s, execution)
		}(i, step)
	}

	wg.Wait()

	// Check if any step failed
	for i, stepResult := range results {
		result.Output[fmt.Sprintf("step_%d", i)] = stepResult.Output
		if stepResult.Status == "failure" {
			result.Status = "failure"
			result.Error = stepResult.Error
		}
	}

	return result
}

// executeSequential executes multiple steps sequentially
func (we *WorkflowEngine) executeSequential(ctx context.Context, steps []WorkflowStep, execution *WorkflowExecution, parentStep *WorkflowStep) StepResult {
	result := StepResult{
		Status:    "success",
		Output:    make(map[string]interface{}),
		StartedAt: time.Now(),
		Attempts:  1,
	}

	for i, step := range steps {
		stepResult := we.executeStep(ctx, &step, execution)
		result.Output[fmt.Sprintf("step_%d", i)] = stepResult.Output

		if stepResult.Status == "failure" {
			result.Status = "failure"
			result.Error = stepResult.Error
			return result
		}
	}

	return result
}

// GetWorkflow retrieves a workflow by ID
func (we *WorkflowEngine) GetWorkflow(ctx context.Context, workflowID uuid.UUID) (*Workflow, error) {
	workflow := &Workflow{}

	query := `
		SELECT id, name, description, triggers, steps, enabled, metadata,
		       created_by, created_at, updated_at, version, tags
		FROM workflows
		WHERE id = $1
	`

	var triggersJSON, stepsJSON, metadataJSON, tagsJSON []byte

	err := we.db.QueryRowContext(ctx, query, workflowID).Scan(
		&workflow.ID, &workflow.Name, &workflow.Description, &triggersJSON, &stepsJSON,
		&workflow.Enabled, &metadataJSON, &workflow.CreatedBy, &workflow.CreatedAt,
		&workflow.UpdatedAt, &workflow.Version, &tagsJSON,
	)

	if err == sql.ErrNoRows {
		return nil, ErrWorkflowNotFound
	}
	if err != nil {
		return nil, err
	}

	// Unmarshal JSON fields
	json.Unmarshal(triggersJSON, &workflow.Triggers)
	json.Unmarshal(stepsJSON, &workflow.Steps)
	json.Unmarshal(metadataJSON, &workflow.Metadata)
	json.Unmarshal(tagsJSON, &workflow.Tags)

	return workflow, nil
}

// Helper functions

func (we *WorkflowEngine) validateWorkflow(workflow *Workflow) error {
	if workflow.Name == "" {
		return errors.New("workflow name is required")
	}

	if len(workflow.Steps) == 0 {
		return errors.New("workflow must have at least one step")
	}

	// Validate each step
	for _, step := range workflow.Steps {
		if err := we.validateStep(&step); err != nil {
			return fmt.Errorf("invalid step %s: %v", step.ID, err)
		}
	}

	return nil
}

func (we *WorkflowEngine) validateStep(step *WorkflowStep) error {
	if step.ID == "" {
		return errors.New("step ID is required")
	}

	if step.Type == "" {
		return errors.New("step type is required")
	}

	if step.Type == "action" && step.Action == nil {
		return errors.New("action step requires action configuration")
	}

	if step.Action != nil {
		executor, exists := we.executors[step.Action.Type]
		if !exists {
			return fmt.Errorf("no executor found for action type: %s", step.Action.Type)
		}

		if err := executor.Validate(step.Action); err != nil {
			return fmt.Errorf("action validation failed: %v", err)
		}
	}

	return nil
}

func (we *WorkflowEngine) addLog(execution *WorkflowExecution, level, message, stepID string, data map[string]interface{}) {
	log := ExecutionLog{
		Timestamp: time.Now(),
		Level:     level,
		Message:   message,
		StepID:    stepID,
		Data:      data,
	}

	execution.Logs = append(execution.Logs, log)
}

func (we *WorkflowEngine) findStep(steps []WorkflowStep, stepID string) *WorkflowStep {
	for _, step := range steps {
		if step.ID == stepID {
			return &step
		}
		// Recursively search in nested steps
		if len(step.Steps) > 0 {
			if found := we.findStep(step.Steps, stepID); found != nil {
				return found
			}
		}
	}
	return nil
}

func (we *WorkflowEngine) evaluateExpression(expression string, context map[string]interface{}, variables map[string]interface{}) bool {
	// Simple expression evaluation - in production, use a proper expression engine like govaluate
	// For now, just return true for demonstration
	return true
}

func (we *WorkflowEngine) storeExecution(ctx context.Context, execution *WorkflowExecution) error {
	triggerEventJSON, _ := json.Marshal(execution.TriggerEvent)
	contextJSON, _ := json.Marshal(execution.Context)
	stepResultsJSON, _ := json.Marshal(execution.StepResults)
	logsJSON, _ := json.Marshal(execution.Logs)

	query := `
		INSERT INTO workflow_executions (
			id, workflow_id, status, trigger_event, context, step_results, 
			started_at, completed_at, error, logs
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`

	_, err := we.db.ExecContext(ctx, query,
		execution.ID, execution.WorkflowID, execution.Status, triggerEventJSON,
		contextJSON, stepResultsJSON, execution.StartedAt, execution.CompletedAt,
		execution.Error, logsJSON,
	)

	return err
}

// Interface implementation methods for interfaces.WorkflowService

// ExecuteWorkflowByID executes a workflow by string ID (interface method)
func (we *WorkflowEngine) ExecuteWorkflowByID(ctx context.Context, workflowID string, inputs map[string]interface{}) (*interfaces.WorkflowResult, error) {
	// Convert string ID to UUID
	workflowUUID, err := uuid.Parse(workflowID)
	if err != nil {
		return nil, fmt.Errorf("invalid workflow ID format: %w", err)
	}

	// Execute using existing method
	execution, err := we.ExecuteWorkflow(ctx, workflowUUID, inputs)
	if err != nil {
		return nil, err
	}

	// Convert WorkflowExecution to WorkflowResult
	result := &interfaces.WorkflowResult{
		ExecutionID: execution.ID.String(),
		Status:      execution.Status,
		Results:     make(map[string]interface{}),
		StartedAt:   execution.StartedAt.Format(time.RFC3339),
	}

	// Add step results to results
	for stepID, stepResult := range execution.StepResults {
		result.Results[stepID] = stepResult.Output
	}

	if execution.CompletedAt != nil {
		result.CompletedAt = execution.CompletedAt.Format(time.RFC3339)
	}

	return result, nil
}

// GetWorkflowStatus gets workflow execution status (interface method)
func (we *WorkflowEngine) GetWorkflowStatus(ctx context.Context, executionID string) (*interfaces.WorkflowStatus, error) {
	// Check if execution is currently running
	we.mu.RLock()
	if execution, exists := we.running[executionID]; exists {
		we.mu.RUnlock()

		// Calculate progress
		totalSteps := len(execution.StepResults)
		completedSteps := 0
		for _, result := range execution.StepResults {
			if result.Status != "running" {
				completedSteps++
			}
		}

		progress := 0.0
		if totalSteps > 0 {
			progress = float64(completedSteps) / float64(totalSteps)
		}

		// Get current step
		currentStep := ""
		for stepID, result := range execution.StepResults {
			if result.Status == "running" {
				currentStep = stepID
				break
			}
		}

		return &interfaces.WorkflowStatus{
			ExecutionID:  executionID,
			WorkflowID:   execution.WorkflowID.String(),
			Status:       execution.Status,
			CurrentStep:  currentStep,
			Progress:     progress,
			ErrorMessage: execution.Error,
			Metadata: map[string]interface{}{
				"total_steps":     totalSteps,
				"completed_steps": completedSteps,
				"started_at":      execution.StartedAt.Format(time.RFC3339),
			},
		}, nil
	}
	we.mu.RUnlock()

	// Check database for completed execution
	executionUUID, err := uuid.Parse(executionID)
	if err != nil {
		return nil, fmt.Errorf("invalid execution ID format: %w", err)
	}

	query := `
		SELECT workflow_id, status, step_results, started_at, completed_at, error
		FROM workflow_executions
		WHERE id = $1
	`

	var workflowID uuid.UUID
	var status string
	var stepResultsJSON []byte
	var startedAt time.Time
	var completedAt *time.Time
	var errorMsg sql.NullString

	err = we.db.QueryRowContext(ctx, query, executionUUID).Scan(
		&workflowID, &status, &stepResultsJSON, &startedAt, &completedAt, &errorMsg,
	)
	if err != nil {
		return nil, err
	}

	// Parse step results for progress calculation
	var stepResults map[string]StepResult
	if len(stepResultsJSON) > 0 {
		json.Unmarshal(stepResultsJSON, &stepResults)
	}

	totalSteps := len(stepResults)
	completedSteps := 0
	for _, result := range stepResults {
		if result.Status != "running" {
			completedSteps++
		}
	}

	progress := 1.0 // Assume complete if retrieved from database
	if totalSteps > 0 && status == "running" {
		progress = float64(completedSteps) / float64(totalSteps)
	}

	workflowStatus := &interfaces.WorkflowStatus{
		ExecutionID:  executionID,
		WorkflowID:   workflowID.String(),
		Status:       status,
		Progress:     progress,
		ErrorMessage: errorMsg.String,
		Metadata: map[string]interface{}{
			"total_steps":     totalSteps,
			"completed_steps": completedSteps,
			"started_at":      startedAt.Format(time.RFC3339),
		},
	}

	if completedAt != nil {
		workflowStatus.Metadata["completed_at"] = completedAt.Format(time.RFC3339)
	}

	return workflowStatus, nil
}

// ListActiveWorkflows returns currently running workflows
func (we *WorkflowEngine) ListActiveWorkflows() map[string]*WorkflowExecution {
	we.mu.RLock()
	defer we.mu.RUnlock()

	// Create a copy to avoid concurrent access issues
	active := make(map[string]*WorkflowExecution)
	for id, execution := range we.running {
		active[id] = execution
	}

	return active
}

// CancelWorkflow cancels a running workflow
func (we *WorkflowEngine) CancelWorkflow(ctx context.Context, executionID string) error {
	we.mu.Lock()
	defer we.mu.Unlock()

	if execution, exists := we.running[executionID]; exists {
		execution.Status = "cancelled"
		execution.Error = "Cancelled by user"
		now := time.Now()
		execution.CompletedAt = &now

		// Remove from running list
		delete(we.running, executionID)

		// Update in database
		return we.updateExecution(ctx, execution)
	}

	return errors.New("workflow execution not found or already completed")
}

// GetWorkflowMetrics returns workflow engine performance metrics
func (we *WorkflowEngine) GetWorkflowMetrics(ctx context.Context) (map[string]interface{}, error) {
	metrics := make(map[string]interface{})

	// Running workflows
	we.mu.RLock()
	metrics["active_workflows"] = len(we.running)
	we.mu.RUnlock()

	// Total executions in last 24 hours
	var total, successful, failed int
	we.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM workflow_executions
		WHERE started_at >= NOW() - INTERVAL '24 hours'
	`).Scan(&total)
	metrics["total_executions_24h"] = total

	we.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM workflow_executions
		WHERE started_at >= NOW() - INTERVAL '24 hours' AND status = 'completed'
	`).Scan(&successful)
	metrics["successful_executions_24h"] = successful

	we.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM workflow_executions
		WHERE started_at >= NOW() - INTERVAL '24 hours' AND status = 'failed'
	`).Scan(&failed)
	metrics["failed_executions_24h"] = failed

	// Success rate
	if total > 0 {
		metrics["success_rate"] = float64(successful) / float64(total) * 100
	} else {
		metrics["success_rate"] = 100.0
	}

	// Average execution time
	var avgDuration sql.NullFloat64
	we.db.QueryRowContext(ctx, `
		SELECT AVG(EXTRACT(EPOCH FROM (completed_at - started_at)))
		FROM workflow_executions
		WHERE started_at >= NOW() - INTERVAL '24 hours'
		AND completed_at IS NOT NULL
	`).Scan(&avgDuration)

	if avgDuration.Valid {
		metrics["avg_execution_time_seconds"] = avgDuration.Float64
	} else {
		metrics["avg_execution_time_seconds"] = 0
	}

	return metrics, nil
}

func (we *WorkflowEngine) updateExecution(ctx context.Context, execution *WorkflowExecution) error {
	contextJSON, _ := json.Marshal(execution.Context)
	stepResultsJSON, _ := json.Marshal(execution.StepResults)
	logsJSON, _ := json.Marshal(execution.Logs)

	query := `
		UPDATE workflow_executions 
		SET status = $1, context = $2, step_results = $3, completed_at = $4, error = $5, logs = $6
		WHERE id = $7
	`

	_, err := we.db.ExecContext(ctx, query,
		execution.Status, contextJSON, stepResultsJSON, execution.CompletedAt,
		execution.Error, logsJSON, execution.ID,
	)

	return err
}
