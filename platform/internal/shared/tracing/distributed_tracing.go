package tracing

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ====================================================================================
// DISTRIBUTED TRACING - Track requests across services
// ====================================================================================

// TraceContext holds tracing information
type TraceContext struct {
	TraceID  string            `json:"trace_id"`
	SpanID   string            `json:"span_id"`
	ParentID string            `json:"parent_id,omitempty"`
	Baggage  map[string]string `json:"baggage,omitempty"`
}

// Span represents a unit of work in a trace
type Span struct {
	TraceID       string                 `json:"trace_id"`
	SpanID        string                 `json:"span_id"`
	ParentID      string                 `json:"parent_id,omitempty"`
	OperationName string                 `json:"operation_name"`
	ServiceName   string                 `json:"service_name"`
	StartTime     time.Time              `json:"start_time"`
	EndTime       *time.Time             `json:"end_time,omitempty"`
	Duration      *time.Duration         `json:"duration,omitempty"`
	Tags          map[string]interface{} `json:"tags"`
	Logs          []SpanLog              `json:"logs"`
	Status        SpanStatus             `json:"status"`
	mu            sync.RWMutex
}

// SpanLog represents a log entry within a span
type SpanLog struct {
	Timestamp time.Time              `json:"timestamp"`
	Level     string                 `json:"level"`
	Message   string                 `json:"message"`
	Fields    map[string]interface{} `json:"fields,omitempty"`
}

// SpanStatus represents the status of a span
type SpanStatus struct {
	Code    int    `json:"code"` // 0=OK, 1=Cancelled, 2=Unknown, 3=InvalidArgument, etc.
	Message string `json:"message,omitempty"`
}

// Tracer manages distributed tracing
type Tracer struct {
	serviceName string
	config      *TracingConfig
	spans       map[string]*Span
	exporters   []SpanExporter
	sampler     Sampler
	mu          sync.RWMutex
}

// TracingConfig defines tracing configuration
type TracingConfig struct {
	ServiceName     string           `json:"service_name"`
	SamplingRate    float64          `json:"sampling_rate" default:"0.1"`
	MaxSpans        int              `json:"max_spans" default:"10000"`
	SpanTimeout     time.Duration    `json:"span_timeout" default:"5m"`
	BatchSize       int              `json:"batch_size" default:"100"`
	BatchTimeout    time.Duration    `json:"batch_timeout" default:"5s"`
	EnableMetrics   bool             `json:"enable_metrics" default:"true"`
	ExporterConfigs []ExporterConfig `json:"exporters"`
}

// ExporterConfig defines span exporter configuration
type ExporterConfig struct {
	Type     string                 `json:"type"` // "jaeger", "zipkin", "console", "otlp"
	Endpoint string                 `json:"endpoint,omitempty"`
	Config   map[string]interface{} `json:"config,omitempty"`
}

// SpanExporter exports spans to external systems
type SpanExporter interface {
	Export(spans []*Span) error
	Shutdown(ctx context.Context) error
}

// Sampler decides whether to sample a trace
type Sampler interface {
	ShouldSample(traceID string, operationName string) bool
}

// NewTracer creates a new distributed tracer
func NewTracer(config *TracingConfig) *Tracer {
	if config == nil {
		config = GetDefaultTracingConfig()
	}

	tracer := &Tracer{
		serviceName: config.ServiceName,
		config:      config,
		spans:       make(map[string]*Span),
		exporters:   make([]SpanExporter, 0),
		sampler:     NewProbabilitySampler(config.SamplingRate),
	}

	// Initialize exporters
	for _, exporterConfig := range config.ExporterConfigs {
		exporter := tracer.createExporter(exporterConfig)
		if exporter != nil {
			tracer.exporters = append(tracer.exporters, exporter)
		}
	}

	// Start background span processor
	go tracer.processSpans()

	return tracer
}

// StartSpan starts a new span
func (t *Tracer) StartSpan(ctx context.Context, operationName string, opts ...SpanOption) (context.Context, *Span) {
	var traceID, parentID string

	// Get trace context from incoming context
	if traceCtx := GetTraceContext(ctx); traceCtx != nil {
		traceID = traceCtx.TraceID
		parentID = traceCtx.SpanID
	} else {
		traceID = generateTraceID()
	}

	// Check sampling decision
	if !t.sampler.ShouldSample(traceID, operationName) {
		return ctx, nil // Return nil span for unsampled traces
	}

	span := &Span{
		TraceID:       traceID,
		SpanID:        generateSpanID(),
		ParentID:      parentID,
		OperationName: operationName,
		ServiceName:   t.serviceName,
		StartTime:     time.Now(),
		Tags:          make(map[string]interface{}),
		Logs:          make([]SpanLog, 0),
		Status:        SpanStatus{Code: 0},
	}

	// Apply options
	for _, opt := range opts {
		opt(span)
	}

	// Store span
	t.mu.Lock()
	t.spans[span.SpanID] = span
	t.mu.Unlock()

	// Create new context with span
	newCtx := WithSpan(ctx, span)

	return newCtx, span
}

// FinishSpan finishes a span
func (t *Tracer) FinishSpan(span *Span) {
	if span == nil {
		return
	}

	span.mu.Lock()
	endTime := time.Now()
	span.EndTime = &endTime
	duration := endTime.Sub(span.StartTime)
	span.Duration = &duration
	span.mu.Unlock()

	// Export span
	go t.exportSpan(span)

	// Remove from active spans
	t.mu.Lock()
	delete(t.spans, span.SpanID)
	t.mu.Unlock()
}

// SpanOption configures a span
type SpanOption func(*Span)

// WithTags adds tags to a span
func WithTags(tags map[string]interface{}) SpanOption {
	return func(span *Span) {
		span.mu.Lock()
		defer span.mu.Unlock()
		for k, v := range tags {
			span.Tags[k] = v
		}
	}
}

// WithTag adds a single tag to a span
func WithTag(key string, value interface{}) SpanOption {
	return func(span *Span) {
		span.mu.Lock()
		defer span.mu.Unlock()
		span.Tags[key] = value
	}
}

// Span methods
func (s *Span) SetTag(key string, value interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Tags[key] = value
}

func (s *Span) SetStatus(code int, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Status = SpanStatus{Code: code, Message: message}
}

func (s *Span) LogFields(level, message string, fields map[string]interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Logs = append(s.Logs, SpanLog{
		Timestamp: time.Now(),
		Level:     level,
		Message:   message,
		Fields:    fields,
	})
}

func (s *Span) LogEvent(message string) {
	s.LogFields("info", message, nil)
}

func (s *Span) LogError(err error) {
	s.SetStatus(2, err.Error())
	s.LogFields("error", "Error occurred", map[string]interface{}{
		"error.message": err.Error(),
		"error.type":    fmt.Sprintf("%T", err),
	})
}

// Context utilities
type traceContextKey struct{}

func WithTraceContext(ctx context.Context, traceCtx *TraceContext) context.Context {
	return context.WithValue(ctx, traceContextKey{}, traceCtx)
}

func GetTraceContext(ctx context.Context) *TraceContext {
	if traceCtx, ok := ctx.Value(traceContextKey{}).(*TraceContext); ok {
		return traceCtx
	}
	return nil
}

func WithSpan(ctx context.Context, span *Span) context.Context {
	traceCtx := &TraceContext{
		TraceID: span.TraceID,
		SpanID:  span.SpanID,
	}
	return WithTraceContext(ctx, traceCtx)
}

func GetSpan(ctx context.Context) *Span {
	// This would typically return the active span from context
	// For simplicity, we'll return nil here
	return nil
}

// HTTP Tracing Middleware
func (t *Tracer) HTTPMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract trace context from headers
			ctx := r.Context()
			traceID := r.Header.Get("X-Trace-ID")
			spanID := r.Header.Get("X-Span-ID")

			if traceID != "" && spanID != "" {
				traceCtx := &TraceContext{
					TraceID: traceID,
					SpanID:  spanID,
				}
				ctx = WithTraceContext(ctx, traceCtx)
			}

			// Start new span for this request
			ctx, span := t.StartSpan(ctx, fmt.Sprintf("%s %s", r.Method, r.URL.Path),
				WithTag("http.method", r.Method),
				WithTag("http.url", r.URL.String()),
				WithTag("http.remote_addr", r.RemoteAddr),
				WithTag("user_agent", r.UserAgent()),
			)

			if span != nil {
				defer t.FinishSpan(span)

				// Add trace headers to response
				w.Header().Set("X-Trace-ID", span.TraceID)
				w.Header().Set("X-Span-ID", span.SpanID)

				// Wrap response writer to capture status code
				ww := &responseWriter{ResponseWriter: w, statusCode: 200}

				// Process request
				next.ServeHTTP(ww, r.WithContext(ctx))

				// Set span tags based on response
				span.SetTag("http.status_code", ww.statusCode)
				if ww.statusCode >= 400 {
					span.SetStatus(2, fmt.Sprintf("HTTP %d", ww.statusCode))
				}
			} else {
				next.ServeHTTP(w, r.WithContext(ctx))
			}
		})
	}
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// Sampling implementations
type ProbabilitySampler struct {
	rate float64
}

func NewProbabilitySampler(rate float64) *ProbabilitySampler {
	return &ProbabilitySampler{rate: rate}
}

func (ps *ProbabilitySampler) ShouldSample(traceID string, operationName string) bool {
	// Simple hash-based sampling for consistent decisions
	hash := simpleHash(traceID)
	return float64(hash%100)/100.0 < ps.rate
}

func simpleHash(s string) uint32 {
	var hash uint32 = 2166136261
	for _, c := range s {
		hash ^= uint32(c)
		hash *= 16777619
	}
	return hash
}

// Exporter implementations
func (t *Tracer) createExporter(config ExporterConfig) SpanExporter {
	switch config.Type {
	case "console":
		return &ConsoleExporter{}
	case "jaeger":
		return &JaegerExporter{endpoint: config.Endpoint}
	case "zipkin":
		return &ZipkinExporter{endpoint: config.Endpoint}
	default:
		return nil
	}
}

type ConsoleExporter struct{}

func (ce *ConsoleExporter) Export(spans []*Span) error {
	for _, span := range spans {
		data, _ := json.MarshalIndent(span, "", "  ")
		log.Printf("trace: %s", data)
	}
	return nil
}

func (ce *ConsoleExporter) Shutdown(ctx context.Context) error {
	return nil
}

type JaegerExporter struct {
	endpoint string
}

func (je *JaegerExporter) Export(spans []*Span) error {
	// Implementation would send spans to Jaeger
	return nil
}

func (je *JaegerExporter) Shutdown(ctx context.Context) error {
	return nil
}

type ZipkinExporter struct {
	endpoint string
}

func (ze *ZipkinExporter) Export(spans []*Span) error {
	// Implementation would send spans to Zipkin
	return nil
}

func (ze *ZipkinExporter) Shutdown(ctx context.Context) error {
	return nil
}

// Background processing
func (t *Tracer) processSpans() {
	ticker := time.NewTicker(t.config.BatchTimeout)
	defer ticker.Stop()

	batch := make([]*Span, 0, t.config.BatchSize)

	for range ticker.C {
		// Collect finished spans
		t.mu.RLock()
		for _, span := range t.spans {
			if span.EndTime != nil {
				batch = append(batch, span)
				if len(batch) >= t.config.BatchSize {
					break
				}
			}
		}
		t.mu.RUnlock()

		// Export batch if not empty
		if len(batch) > 0 {
			t.exportBatch(batch)
			batch = batch[:0] // Reset batch
		}
	}
}

func (t *Tracer) exportSpan(span *Span) {
	t.exportBatch([]*Span{span})
}

func (t *Tracer) exportBatch(spans []*Span) {
	for _, exporter := range t.exporters {
		go func(exp SpanExporter, spanBatch []*Span) {
			exp.Export(spanBatch)
		}(exporter, spans)
	}
}

// Utility functions
func generateTraceID() string {
	return uuid.New().String()
}

func generateSpanID() string {
	return uuid.New().String()[:16]
}

func GetDefaultTracingConfig() *TracingConfig {
	return &TracingConfig{
		ServiceName:   "alerthub",
		SamplingRate:  0.1,
		MaxSpans:      10000,
		SpanTimeout:   5 * time.Minute,
		BatchSize:     100,
		BatchTimeout:  5 * time.Second,
		EnableMetrics: true,
		ExporterConfigs: []ExporterConfig{
			{
				Type: "console",
			},
		},
	}
}

// ====================================================================================
// PERFORMANCE MONITORING - System performance tracking
// ====================================================================================

// PerformanceMonitor tracks system performance metrics
type PerformanceMonitor struct {
	config     *PerformanceConfig
	collectors map[string]MetricCollector
	metrics    map[string]*PerformanceMetric
	mu         sync.RWMutex
	stopChan   chan struct{}
	isRunning  bool
}

// PerformanceConfig defines performance monitoring configuration
type PerformanceConfig struct {
	CollectionInterval       time.Duration      `json:"collection_interval" default:"30s"`
	RetentionPeriod          time.Duration      `json:"retention_period" default:"24h"`
	EnableCPUProfiling       bool               `json:"enable_cpu_profiling" default:"false"`
	EnableMemProfiling       bool               `json:"enable_mem_profiling" default:"false"`
	EnableGoroutineProfiling bool               `json:"enable_goroutine_profiling" default:"false"`
	AlertThresholds          map[string]float64 `json:"alert_thresholds"`
}

// PerformanceMetric represents a performance metric
type PerformanceMetric struct {
	Name      string                 `json:"name"`
	Value     float64                `json:"value"`
	Unit      string                 `json:"unit"`
	Timestamp time.Time              `json:"timestamp"`
	Tags      map[string]string      `json:"tags,omitempty"`
	History   []PerformanceDataPoint `json:"history,omitempty"`
}

// PerformanceDataPoint represents a historical data point
type PerformanceDataPoint struct {
	Value     float64   `json:"value"`
	Timestamp time.Time `json:"timestamp"`
}

// MetricCollector interface for collecting metrics
type MetricCollector interface {
	Collect() (*PerformanceMetric, error)
	GetName() string
}

// NewPerformanceMonitor creates a new performance monitor
func NewPerformanceMonitor(config *PerformanceConfig) *PerformanceMonitor {
	if config == nil {
		config = GetDefaultPerformanceConfig()
	}

	pm := &PerformanceMonitor{
		config:     config,
		collectors: make(map[string]MetricCollector),
		metrics:    make(map[string]*PerformanceMetric),
		stopChan:   make(chan struct{}),
	}

	// Register default collectors
	pm.registerDefaultCollectors()

	return pm
}

// registerDefaultCollectors registers built-in metric collectors
func (pm *PerformanceMonitor) registerDefaultCollectors() {
	pm.RegisterCollector(&CPUCollector{})
	pm.RegisterCollector(&MemoryCollector{})
	pm.RegisterCollector(&GoroutineCollector{})
	pm.RegisterCollector(&GCCollector{})
}

// RegisterCollector registers a metric collector
func (pm *PerformanceMonitor) RegisterCollector(collector MetricCollector) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.collectors[collector.GetName()] = collector
}

// Start begins performance monitoring
func (pm *PerformanceMonitor) Start() {
	pm.mu.Lock()
	if pm.isRunning {
		pm.mu.Unlock()
		return
	}
	pm.isRunning = true
	pm.mu.Unlock()

	ticker := time.NewTicker(pm.config.CollectionInterval)
	defer ticker.Stop()

	// Collect initial metrics
	pm.collectMetrics()

	for {
		select {
		case <-ticker.C:
			pm.collectMetrics()
		case <-pm.stopChan:
			pm.mu.Lock()
			pm.isRunning = false
			pm.mu.Unlock()
			return
		}
	}
}

// Stop stops performance monitoring
func (pm *PerformanceMonitor) Stop() {
	pm.mu.RLock()
	if !pm.isRunning {
		pm.mu.RUnlock()
		return
	}
	pm.mu.RUnlock()

	close(pm.stopChan)
}

// collectMetrics collects metrics from all registered collectors
func (pm *PerformanceMonitor) collectMetrics() {
	pm.mu.RLock()
	collectors := make(map[string]MetricCollector)
	for name, collector := range pm.collectors {
		collectors[name] = collector
	}
	pm.mu.RUnlock()

	for _, collector := range collectors {
		go func(c MetricCollector) {
			if metric, err := c.Collect(); err == nil {
				pm.storeMetric(metric)
				pm.checkAlerts(metric)
			}
		}(collector)
	}
}

// storeMetric stores a metric with history
func (pm *PerformanceMonitor) storeMetric(metric *PerformanceMetric) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Get existing metric or create new one
	existing, exists := pm.metrics[metric.Name]
	if !exists {
		pm.metrics[metric.Name] = metric
		return
	}

	// Add to history
	dataPoint := PerformanceDataPoint{
		Value:     metric.Value,
		Timestamp: metric.Timestamp,
	}

	existing.History = append(existing.History, dataPoint)

	// Limit history size based on retention period
	cutoff := time.Now().Add(-pm.config.RetentionPeriod)
	for i, point := range existing.History {
		if point.Timestamp.After(cutoff) {
			existing.History = existing.History[i:]
			break
		}
	}

	// Update current value
	existing.Value = metric.Value
	existing.Timestamp = metric.Timestamp
}

// checkAlerts checks if metrics exceed alert thresholds
func (pm *PerformanceMonitor) checkAlerts(metric *PerformanceMetric) {
	if threshold, exists := pm.config.AlertThresholds[metric.Name]; exists {
		if metric.Value > threshold {
			log.Printf("perf: %s=%.2f exceeds threshold %.2f", metric.Name, metric.Value, threshold)
		}
	}
}

// GetMetrics returns current performance metrics
func (pm *PerformanceMonitor) GetMetrics() map[string]*PerformanceMetric {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	// Return copy of metrics
	result := make(map[string]*PerformanceMetric)
	for name, metric := range pm.metrics {
		result[name] = &PerformanceMetric{
			Name:      metric.Name,
			Value:     metric.Value,
			Unit:      metric.Unit,
			Timestamp: metric.Timestamp,
			Tags:      metric.Tags,
			History:   metric.History,
		}
	}

	return result
}

// Metric collectors
type CPUCollector struct{}

func (cc *CPUCollector) Collect() (*PerformanceMetric, error) {
	// This would collect actual CPU metrics
	// For now, return mock data
	return &PerformanceMetric{
		Name:      "cpu_usage_percent",
		Value:     45.2,
		Unit:      "percent",
		Timestamp: time.Now(),
		Tags:      map[string]string{"collector": "cpu"},
	}, nil
}

func (cc *CPUCollector) GetName() string {
	return "cpu_usage"
}

type MemoryCollector struct{}

func (mc *MemoryCollector) Collect() (*PerformanceMetric, error) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return &PerformanceMetric{
		Name:      "memory_usage_bytes",
		Value:     float64(m.Alloc),
		Unit:      "bytes",
		Timestamp: time.Now(),
		Tags:      map[string]string{"collector": "memory"},
	}, nil
}

func (mc *MemoryCollector) GetName() string {
	return "memory_usage"
}

type GoroutineCollector struct{}

func (gc *GoroutineCollector) Collect() (*PerformanceMetric, error) {
	return &PerformanceMetric{
		Name:      "goroutines_count",
		Value:     float64(runtime.NumGoroutine()),
		Unit:      "count",
		Timestamp: time.Now(),
		Tags:      map[string]string{"collector": "goroutine"},
	}, nil
}

func (gc *GoroutineCollector) GetName() string {
	return "goroutines"
}

type GCCollector struct{}

func (gcc *GCCollector) Collect() (*PerformanceMetric, error) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return &PerformanceMetric{
		Name:      "gc_pause_ns",
		Value:     float64(m.PauseNs[(m.NumGC+255)%256]),
		Unit:      "nanoseconds",
		Timestamp: time.Now(),
		Tags:      map[string]string{"collector": "gc"},
	}, nil
}

func (gcc *GCCollector) GetName() string {
	return "gc_pause"
}

// GetDefaultPerformanceConfig returns default performance monitoring configuration
func GetDefaultPerformanceConfig() *PerformanceConfig {
	return &PerformanceConfig{
		CollectionInterval:       30 * time.Second,
		RetentionPeriod:          24 * time.Hour,
		EnableCPUProfiling:       false,
		EnableMemProfiling:       false,
		EnableGoroutineProfiling: false,
		AlertThresholds: map[string]float64{
			"cpu_usage_percent":  80.0,
			"memory_usage_bytes": 1024 * 1024 * 1024, // 1GB
			"goroutines_count":   10000,
		},
	}
}
