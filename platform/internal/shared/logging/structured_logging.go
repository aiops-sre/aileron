package logging

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ====================================================================================
// STRUCTURED LOGGING SYSTEM - Comprehensive logging with correlation
// ====================================================================================

// LogLevel represents the severity of a log entry
type LogLevel string

const (
	LevelTrace LogLevel = "trace"
	LevelDebug LogLevel = "debug"
	LevelInfo  LogLevel = "info"
	LevelWarn  LogLevel = "warn"
	LevelError LogLevel = "error"
	LevelFatal LogLevel = "fatal"
	LevelPanic LogLevel = "panic"
)

// LogEntry represents a structured log entry
type LogEntry struct {
	ID        string                 `json:"id"`
	Timestamp time.Time              `json:"timestamp"`
	Level     LogLevel               `json:"level"`
	Message   string                 `json:"message"`
	Service   string                 `json:"service"`
	Component string                 `json:"component,omitempty"`
	TraceID   string                 `json:"trace_id,omitempty"`
	SpanID    string                 `json:"span_id,omitempty"`
	UserID    string                 `json:"user_id,omitempty"`
	RequestID string                 `json:"request_id,omitempty"`
	Fields    map[string]interface{} `json:"fields,omitempty"`
	Error     *ErrorDetails          `json:"error,omitempty"`
	Source    *SourceLocation        `json:"source,omitempty"`
	Duration  *time.Duration         `json:"duration,omitempty"`
	Tags      []string               `json:"tags,omitempty"`
}

// ErrorDetails provides structured error information
type ErrorDetails struct {
	Type    string                 `json:"type"`
	Message string                 `json:"message"`
	Stack   string                 `json:"stack,omitempty"`
	Code    string                 `json:"code,omitempty"`
	Details map[string]interface{} `json:"details,omitempty"`
	Cause   *ErrorDetails          `json:"cause,omitempty"`
}

// SourceLocation provides source code location information
type SourceLocation struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Function string `json:"function"`
}

// Logger interface for structured logging
type Logger interface {
	Trace(msg string, fields ...Field)
	Debug(msg string, fields ...Field)
	Info(msg string, fields ...Field)
	Warn(msg string, fields ...Field)
	Error(msg string, fields ...Field)
	Fatal(msg string, fields ...Field)
	Panic(msg string, fields ...Field)

	With(fields ...Field) Logger
	WithComponent(component string) Logger
	WithTraceID(traceID string) Logger
	WithUserID(userID string) Logger
	WithRequestID(requestID string) Logger
}

// Field represents a structured logging field
type Field struct {
	Key   string
	Value interface{}
}

// StructuredLogger implements comprehensive structured logging
type StructuredLogger struct {
	config    *LogConfig
	outputs   []LogOutput
	context   map[string]interface{}
	service   string
	component string
	mu        sync.RWMutex
	hooks     []LogHook
}

// LogConfig defines logging configuration
type LogConfig struct {
	Level            LogLevel        `json:"level" default:"info"`
	Service          string          `json:"service" default:"alerthub"`
	Format           string          `json:"format" default:"json"` // "json", "text"
	EnableCaller     bool            `json:"enable_caller" default:"true"`
	EnableStackTrace bool            `json:"enable_stack_trace" default:"true"`
	TimeFormat       string          `json:"time_format" default:"2006-01-02T15:04:05.000Z07:00"`
	Outputs          []OutputConfig  `json:"outputs"`
	BufferSize       int             `json:"buffer_size" default:"1000"`
	FlushInterval    time.Duration   `json:"flush_interval" default:"5s"`
	SamplingConfig   *SamplingConfig `json:"sampling,omitempty"`
}

// OutputConfig defines log output configuration
type OutputConfig struct {
	Type       string                 `json:"type"` // "console", "file", "redis", "elasticsearch"
	Level      LogLevel               `json:"level,omitempty"`
	Format     string                 `json:"format,omitempty"`
	Path       string                 `json:"path,omitempty"`
	MaxSize    int64                  `json:"max_size,omitempty"`
	MaxBackups int                    `json:"max_backups,omitempty"`
	Compress   bool                   `json:"compress,omitempty"`
	Config     map[string]interface{} `json:"config,omitempty"`
}

// SamplingConfig defines log sampling configuration
type SamplingConfig struct {
	Enabled    bool `json:"enabled"`
	Threshold  int  `json:"threshold"`  // Sample after this many logs per second
	Rate       int  `json:"rate"`       // Sample every Nth log after threshold
	Thereafter int  `json:"thereafter"` // Sample every Nth log after initial rate
}

// LogOutput interface for different log destinations
type LogOutput interface {
	Write(entry *LogEntry) error
	Flush() error
	Close() error
	GetLevel() LogLevel
}

// LogHook interface for log entry processing
type LogHook interface {
	Process(entry *LogEntry) *LogEntry
	Levels() []LogLevel
}

// NewStructuredLogger creates a new structured logger
func NewStructuredLogger(config *LogConfig) *StructuredLogger {
	if config == nil {
		config = GetDefaultLogConfig()
	}

	logger := &StructuredLogger{
		config:  config,
		outputs: make([]LogOutput, 0),
		context: make(map[string]interface{}),
		service: config.Service,
		hooks:   make([]LogHook, 0),
	}

	// Initialize outputs
	for _, outputConfig := range config.Outputs {
		output := logger.createOutput(outputConfig)
		if output != nil {
			logger.outputs = append(logger.outputs, output)
		}
	}

	// Add default console output if none specified
	if len(logger.outputs) == 0 {
		logger.outputs = append(logger.outputs, NewConsoleOutput(config.Level, config.Format))
	}

	return logger
}

// createOutput creates a log output based on configuration
func (l *StructuredLogger) createOutput(config OutputConfig) LogOutput {
	switch config.Type {
	case "console":
		return NewConsoleOutput(config.Level, config.Format)
	case "file":
		return NewFileOutput(config)
	case "redis":
		return NewRedisOutput(config)
	case "elasticsearch":
		return NewElasticsearchOutput(config)
	default:
		return nil
	}
}

// log writes a log entry with the specified level
func (l *StructuredLogger) log(level LogLevel, msg string, fields []Field) {
	// Check if level is enabled
	if !l.isLevelEnabled(level) {
		return
	}

	// Create log entry
	entry := &LogEntry{
		ID:        uuid.New().String(),
		Timestamp: time.Now(),
		Level:     level,
		Message:   msg,
		Service:   l.service,
		Component: l.component,
		Fields:    make(map[string]interface{}),
	}

	// Add context fields
	l.mu.RLock()
	for key, value := range l.context {
		entry.Fields[key] = value
	}
	l.mu.RUnlock()

	// Add provided fields
	for _, field := range fields {
		entry.Fields[field.Key] = field.Value
	}

	// Add source location if enabled
	if l.config.EnableCaller {
		entry.Source = l.getSourceLocation(2)
	}

	// Process hooks
	for _, hook := range l.hooks {
		if l.hookApplies(hook, level) {
			entry = hook.Process(entry)
			if entry == nil {
				return // Hook filtered out the entry
			}
		}
	}

	// Write to outputs
	for _, output := range l.outputs {
		if l.shouldWriteToOutput(output, level) {
			go func(o LogOutput, e *LogEntry) {
				o.Write(e)
			}(output, entry)
		}
	}
}

// Logging methods implementation
func (l *StructuredLogger) Trace(msg string, fields ...Field) { l.log(LevelTrace, msg, fields) }
func (l *StructuredLogger) Debug(msg string, fields ...Field) { l.log(LevelDebug, msg, fields) }
func (l *StructuredLogger) Info(msg string, fields ...Field)  { l.log(LevelInfo, msg, fields) }
func (l *StructuredLogger) Warn(msg string, fields ...Field)  { l.log(LevelWarn, msg, fields) }
func (l *StructuredLogger) Error(msg string, fields ...Field) { l.log(LevelError, msg, fields) }
func (l *StructuredLogger) Fatal(msg string, fields ...Field) {
	l.log(LevelFatal, msg, fields)
	l.Flush()
	os.Exit(1)
}
func (l *StructuredLogger) Panic(msg string, fields ...Field) {
	l.log(LevelPanic, msg, fields)
	panic(msg)
}

// With creates a new logger with additional fields
func (l *StructuredLogger) With(fields ...Field) Logger {
	newContext := make(map[string]interface{})

	l.mu.RLock()
	for key, value := range l.context {
		newContext[key] = value
	}
	l.mu.RUnlock()

	for _, field := range fields {
		newContext[field.Key] = field.Value
	}

	return &StructuredLogger{
		config:    l.config,
		outputs:   l.outputs,
		context:   newContext,
		service:   l.service,
		component: l.component,
		hooks:     l.hooks,
	}
}

// WithComponent creates a logger with component context
func (l *StructuredLogger) WithComponent(component string) Logger {
	return &StructuredLogger{
		config:    l.config,
		outputs:   l.outputs,
		context:   l.context,
		service:   l.service,
		component: component,
		hooks:     l.hooks,
	}
}

// WithTraceID adds trace ID to logger context
func (l *StructuredLogger) WithTraceID(traceID string) Logger {
	return l.With(Field{Key: "trace_id", Value: traceID})
}

// WithUserID adds user ID to logger context
func (l *StructuredLogger) WithUserID(userID string) Logger {
	return l.With(Field{Key: "user_id", Value: userID})
}

// WithRequestID adds request ID to logger context
func (l *StructuredLogger) WithRequestID(requestID string) Logger {
	return l.With(Field{Key: "request_id", Value: requestID})
}

// Utility methods
func (l *StructuredLogger) isLevelEnabled(level LogLevel) bool {
	return l.getLevelPriority(level) >= l.getLevelPriority(l.config.Level)
}

func (l *StructuredLogger) getLevelPriority(level LogLevel) int {
	priorities := map[LogLevel]int{
		LevelTrace: 0,
		LevelDebug: 1,
		LevelInfo:  2,
		LevelWarn:  3,
		LevelError: 4,
		LevelFatal: 5,
		LevelPanic: 6,
	}
	return priorities[level]
}

func (l *StructuredLogger) shouldWriteToOutput(output LogOutput, level LogLevel) bool {
	return l.getLevelPriority(level) >= l.getLevelPriority(output.GetLevel())
}

func (l *StructuredLogger) hookApplies(hook LogHook, level LogLevel) bool {
	for _, hookLevel := range hook.Levels() {
		if hookLevel == level {
			return true
		}
	}
	return false
}

func (l *StructuredLogger) getSourceLocation(skip int) *SourceLocation {
	_, file, line, ok := runtime.Caller(skip + 1)
	if !ok {
		return nil
	}

	pc, _, _, _ := runtime.Caller(skip + 1)
	fn := runtime.FuncForPC(pc)

	return &SourceLocation{
		File:     file,
		Line:     line,
		Function: fn.Name(),
	}
}

// AddHook adds a log hook
func (l *StructuredLogger) AddHook(hook LogHook) {
	l.hooks = append(l.hooks, hook)
}

// Flush flushes all outputs
func (l *StructuredLogger) Flush() {
	for _, output := range l.outputs {
		output.Flush()
	}
}

// Close closes all outputs
func (l *StructuredLogger) Close() {
	for _, output := range l.outputs {
		output.Close()
	}
}

// ====================================================================================
// LOG OUTPUTS
// ====================================================================================

// ConsoleOutput writes logs to console
type ConsoleOutput struct {
	level  LogLevel
	format string
	writer io.Writer
}

func NewConsoleOutput(level LogLevel, format string) *ConsoleOutput {
	return &ConsoleOutput{
		level:  level,
		format: format,
		writer: os.Stdout,
	}
}

func (co *ConsoleOutput) Write(entry *LogEntry) error {
	var output string

	if co.format == "json" {
		data, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		output = string(data) + "\n"
	} else {
		// Text format
		output = fmt.Sprintf("[%s] %s [%s] %s",
			entry.Timestamp.Format("2006-01-02 15:04:05"),
			entry.Level,
			entry.Service,
			entry.Message,
		)

		if len(entry.Fields) > 0 {
			fields, _ := json.Marshal(entry.Fields)
			output += " " + string(fields)
		}

		output += "\n"
	}

	_, err := co.writer.Write([]byte(output))
	return err
}

func (co *ConsoleOutput) Flush() error       { return nil }
func (co *ConsoleOutput) Close() error       { return nil }
func (co *ConsoleOutput) GetLevel() LogLevel { return co.level }

// FileOutput writes logs to files
type FileOutput struct {
	config OutputConfig
	level  LogLevel
	file   *os.File
	mu     sync.Mutex
}

func NewFileOutput(config OutputConfig) *FileOutput {
	fo := &FileOutput{
		config: config,
		level:  config.Level,
	}

	// Open file
	file, err := os.OpenFile(config.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil
	}

	fo.file = file
	return fo
}

func (fo *FileOutput) Write(entry *LogEntry) error {
	fo.mu.Lock()
	defer fo.mu.Unlock()

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	_, err = fo.file.Write(append(data, '\n'))
	return err
}

func (fo *FileOutput) Flush() error {
	fo.mu.Lock()
	defer fo.mu.Unlock()
	return fo.file.Sync()
}

func (fo *FileOutput) Close() error {
	fo.mu.Lock()
	defer fo.mu.Unlock()
	return fo.file.Close()
}

func (fo *FileOutput) GetLevel() LogLevel { return fo.level }

// RedisOutput writes logs to Redis
type RedisOutput struct {
	config OutputConfig
	level  LogLevel
	// Redis client would be initialized here
}

func NewRedisOutput(config OutputConfig) *RedisOutput {
	return &RedisOutput{
		config: config,
		level:  config.Level,
	}
}

func (ro *RedisOutput) Write(entry *LogEntry) error {
	// Implementation would send log to Redis
	return nil
}

func (ro *RedisOutput) Flush() error       { return nil }
func (ro *RedisOutput) Close() error       { return nil }
func (ro *RedisOutput) GetLevel() LogLevel { return ro.level }

// ElasticsearchOutput writes logs to Elasticsearch
type ElasticsearchOutput struct {
	config OutputConfig
	level  LogLevel
}

func NewElasticsearchOutput(config OutputConfig) *ElasticsearchOutput {
	return &ElasticsearchOutput{
		config: config,
		level:  config.Level,
	}
}

func (eo *ElasticsearchOutput) Write(entry *LogEntry) error {
	// Implementation would send log to Elasticsearch
	return nil
}

func (eo *ElasticsearchOutput) Flush() error       { return nil }
func (eo *ElasticsearchOutput) Close() error       { return nil }
func (eo *ElasticsearchOutput) GetLevel() LogLevel { return eo.level }

// ====================================================================================
// LOG HOOKS
// ====================================================================================

// PerformanceHook adds performance metrics to logs
type PerformanceHook struct{}

func (ph *PerformanceHook) Process(entry *LogEntry) *LogEntry {
	if entry.Duration != nil {
		entry.Fields["performance"] = map[string]interface{}{
			"duration_ms": float64(entry.Duration.Nanoseconds()) / 1e6,
			"slow":        *entry.Duration > 1*time.Second,
		}
	}
	return entry
}

func (ph *PerformanceHook) Levels() []LogLevel {
	return []LogLevel{LevelInfo, LevelWarn, LevelError}
}

// SecurityHook adds security context to logs
type SecurityHook struct{}

func (sh *SecurityHook) Process(entry *LogEntry) *LogEntry {
	if userID, exists := entry.Fields["user_id"]; exists {
		entry.Fields["security"] = map[string]interface{}{
			"authenticated": userID != nil && userID != "",
			"user_id":       userID,
		}
	}
	return entry
}

func (sh *SecurityHook) Levels() []LogLevel {
	return []LogLevel{LevelInfo, LevelWarn, LevelError, LevelFatal}
}

// ====================================================================================
// CONVENIENCE FUNCTIONS
// ====================================================================================

// F creates a logging field
func F(key string, value interface{}) Field {
	return Field{Key: key, Value: value}
}

// Error creates an error field
func Error(err error) Field {
	return Field{Key: "error", Value: err.Error()}
}

// Duration creates a duration field
func Duration(key string, d time.Duration) Field {
	return Field{Key: key, Value: d.String()}
}

// GetDefaultLogConfig returns default logging configuration
func GetDefaultLogConfig() *LogConfig {
	return &LogConfig{
		Level:            LevelInfo,
		Service:          "alerthub",
		Format:           "json",
		EnableCaller:     true,
		EnableStackTrace: true,
		TimeFormat:       time.RFC3339,
		BufferSize:       1000,
		FlushInterval:    5 * time.Second,
		Outputs: []OutputConfig{
			{
				Type:   "console",
				Level:  LevelInfo,
				Format: "json",
			},
		},
	}
}

// ====================================================================================
// GLOBAL LOGGER
// ====================================================================================

var (
	globalLogger Logger
	once         sync.Once
)

// GetLogger returns the global logger instance
func GetLogger() Logger {
	once.Do(func() {
		globalLogger = NewStructuredLogger(GetDefaultLogConfig())
	})
	return globalLogger
}

// SetLogger sets the global logger instance
func SetLogger(logger Logger) {
	globalLogger = logger
}

// Global logging functions
func Trace(msg string, fields ...Field) { GetLogger().Trace(msg, fields...) }
func Debug(msg string, fields ...Field) { GetLogger().Debug(msg, fields...) }
func Info(msg string, fields ...Field)  { GetLogger().Info(msg, fields...) }
func Warn(msg string, fields ...Field)  { GetLogger().Warn(msg, fields...) }
func Err(msg string, fields ...Field)   { GetLogger().Error(msg, fields...) }
func Fatal(msg string, fields ...Field) { GetLogger().Fatal(msg, fields...) }
func Panic(msg string, fields ...Field) { GetLogger().Panic(msg, fields...) }
