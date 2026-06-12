package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/aileron-platform/aileron/platform/internal/shared/monitoring"
	"github.com/aileron-platform/aileron/platform/internal/shared/resource"
)

var (
	ErrConfigNotFound         = errors.New("configuration not found")
	ErrConfigValidationFailed = errors.New("configuration validation failed")
	ErrConfigLoadFailed       = errors.New("configuration load failed")
	ErrInvalidEnvironment     = errors.New("invalid environment")
)

// Environment represents deployment environments
type Environment string

const (
	EnvironmentProduction  Environment = "production"
	EnvironmentStaging     Environment = "staging"
	EnvironmentDevelopment Environment = "development"
	EnvironmentTesting     Environment = "testing"
)

// Region represents deployment regions for dual-region setup
type Region string

const (
	RegionReno   Region = "reno"
	RegionMaiden Region = "maiden"
)

// AlertHubConfig is the main configuration structure for the correlation engine
type AlertHubConfig struct {
	// Application metadata
	ApplicationName string      `json:"application_name" env:"APP_NAME" default:"alerthub-correlation-engine"`
	Version         string      `json:"version" env:"APP_VERSION" default:"2.0.0"`
	Environment     Environment `json:"environment" env:"ENVIRONMENT" default:"production" validate:"required,oneof=production staging development testing"`
	Region          Region      `json:"region" env:"DEPLOYMENT_REGION" default:"reno" validate:"required,oneof=reno maiden"`

	// Server configuration
	Server ServerConfig `json:"server"`

	// Database configuration
	Database DatabaseConfig `json:"database"`

	// Resource management
	Resources resource.ResourceConfig `json:"resources"`

	// Monitoring configuration
	Monitoring monitoring.MonitoringConfig `json:"monitoring"`

	// External integrations
	Integrations IntegrationsConfig `json:"integrations"`

	// Correlation engine settings
	Correlation CorrelationConfig `json:"correlation"`

	// Security settings
	Security SecurityConfig `json:"security"`

	// Enterprise features
	Enterprise EnterpriseConfig `json:"enterprise"`
}

// ServerConfig holds HTTP server configuration
type ServerConfig struct {
	Host         string        `json:"host" env:"SERVER_HOST" default:"0.0.0.0"`
	Port         int           `json:"port" env:"SERVER_PORT" default:"8080" validate:"min=1024,max=65535"`
	ReadTimeout  time.Duration `json:"read_timeout" env:"SERVER_READ_TIMEOUT" default:"30s"`
	WriteTimeout time.Duration `json:"write_timeout" env:"SERVER_WRITE_TIMEOUT" default:"30s"`
	IdleTimeout  time.Duration `json:"idle_timeout" env:"SERVER_IDLE_TIMEOUT" default:"120s"`
	TLSEnabled   bool          `json:"tls_enabled" env:"TLS_ENABLED" default:"false"`
	TLSCertFile  string        `json:"tls_cert_file" env:"TLS_CERT_FILE" default:""`
	TLSKeyFile   string        `json:"tls_key_file" env:"TLS_KEY_FILE" default:""`
}

// DatabaseConfig holds database configuration
type DatabaseConfig struct {
	URL                string        `json:"url" env:"DATABASE_URL" validate:"required"`
	MaxOpenConnections int           `json:"max_open_connections" env:"DB_MAX_OPEN_CONNS" default:"25" validate:"min=10,max=100"`
	MaxIdleConnections int           `json:"max_idle_connections" env:"DB_MAX_IDLE_CONNS" default:"10" validate:"min=5,max=50"`
	ConnMaxLifetime    time.Duration `json:"conn_max_lifetime" env:"DB_CONN_MAX_LIFETIME" default:"1h"`
	ConnMaxIdleTime    time.Duration `json:"conn_max_idle_time" env:"DB_CONN_MAX_IDLE_TIME" default:"10m"`
	QueryTimeout       time.Duration `json:"query_timeout" env:"DB_QUERY_TIMEOUT" default:"30s"`
	SlowQueryThreshold time.Duration `json:"slow_query_threshold" env:"DB_SLOW_QUERY_THRESHOLD" default:"1s"`
	EnableLogging      bool          `json:"enable_logging" env:"DB_ENABLE_LOGGING" default:"false"`
}

// IntegrationsConfig holds external integration settings
type IntegrationsConfig struct {
	// Dynatrace integration
	Dynatrace DynatraceConfig `json:"dynatrace"`

	// Jenkins integration
	Jenkins JenkinsConfig `json:"jenkins"`

	// Kubernetes integration
	Kubernetes KubernetesConfig `json:"kubernetes"`

	// RBAC integration
	RBAC RBACConfig `json:"rbac"`
}

// DynatraceConfig holds Dynatrace integration configuration
type DynatraceConfig struct {
	Enabled           bool          `json:"enabled" env:"DYNATRACE_ENABLED" default:"true"`
	URL               string        `json:"url" env:"DYNATRACE_URL" validate:"url"`
	APIToken          string        `json:"api_token" env:"DYNATRACE_API_TOKEN" sensitive:"true"`
	WebhookEnabled    bool          `json:"webhook_enabled" env:"DYNATRACE_WEBHOOK_ENABLED" default:"true"`
	TopologyDiscovery bool          `json:"topology_discovery" env:"DYNATRACE_TOPOLOGY_DISCOVERY" default:"true"`
	PollInterval      time.Duration `json:"poll_interval" env:"DYNATRACE_POLL_INTERVAL" default:"5m"`
	TimeoutSeconds    int           `json:"timeout_seconds" env:"DYNATRACE_TIMEOUT" default:"30" validate:"min=5,max=300"`
}

// JenkinsConfig holds Jenkins integration configuration
type JenkinsConfig struct {
	Enabled        bool   `json:"enabled" env:"JENKINS_ENABLED" default:"true"`
	URL            string `json:"url" env:"JENKINS_URL" validate:"url"`
	Username       string `json:"username" env:"JENKINS_USERNAME"`
	APIToken       string `json:"api_token" env:"JENKINS_API_TOKEN" sensitive:"true"`
	WebhookEnabled bool   `json:"webhook_enabled" env:"JENKINS_WEBHOOK_ENABLED" default:"true"`
	TimeoutSeconds int    `json:"timeout_seconds" env:"JENKINS_TIMEOUT" default:"30" validate:"min=5,max=300"`
}

// KubernetesConfig holds Kubernetes integration configuration
type KubernetesConfig struct {
	Enabled             bool     `json:"enabled" env:"K8S_ENABLED" default:"true"`
	ConfigPath          string   `json:"config_path" env:"K8S_CONFIG_PATH" default:""`
	InCluster           bool     `json:"in_cluster" env:"K8S_IN_CLUSTER" default:"false"`
	Namespaces          []string `json:"namespaces" env:"K8S_NAMESPACES" default:"default,kube-system"`
	TopologyDiscovery   bool     `json:"topology_discovery" env:"K8S_TOPOLOGY_DISCOVERY" default:"true"`
	ResourceMonitoring  bool     `json:"resource_monitoring" env:"K8S_RESOURCE_MONITORING" default:"true"`
	PollIntervalSeconds int      `json:"poll_interval_seconds" env:"K8S_POLL_INTERVAL" default:"60" validate:"min=30,max=600"`
}

// RBACConfig holds role-based access control configuration
type RBACConfig struct {
	Enabled            bool          `json:"enabled" env:"RBAC_ENABLED" default:"true"`
	Provider           string        `json:"provider" env:"RBAC_PROVIDER" default:"internal" validate:"oneof=internal ldap oauth2"`
	LDAPServer         string        `json:"ldap_server" env:"LDAP_SERVER" default:""`
	LDAPBaseDN         string        `json:"ldap_base_dn" env:"LDAP_BASE_DN" default:""`
	OAuth2Provider     string        `json:"oauth2_provider" env:"OAUTH2_PROVIDER" default:""`
	OAuth2ClientID     string        `json:"oauth2_client_id" env:"OAUTH2_CLIENT_ID" default:""`
	OAuth2ClientSecret string        `json:"oauth2_client_secret" env:"OAUTH2_CLIENT_SECRET" sensitive:"true"`
	SessionTimeout     time.Duration `json:"session_timeout" env:"SESSION_TIMEOUT" default:"24h"`
}

// CorrelationConfig holds correlation engine configuration
type CorrelationConfig struct {
	EngineType           string        `json:"engine_type" env:"CORRELATION_ENGINE_TYPE" default:"unified" validate:"oneof=unified legacy"`
	SimilarityThreshold  float64       `json:"similarity_threshold" env:"CORRELATION_SIMILARITY_THRESHOLD" default:"0.7" validate:"min=0.1,max=1.0"`
	DeduplicationWindow  time.Duration `json:"deduplication_window" env:"CORRELATION_DEDUP_WINDOW" default:"30m"`
	MaxCandidateAlerts   int           `json:"max_candidate_alerts" env:"CORRELATION_MAX_CANDIDATES" default:"100" validate:"min=10,max=1000"`
	EnableAI             bool          `json:"enable_ai" env:"CORRELATION_ENABLE_AI" default:"true"`
	EnableTopology       bool          `json:"enable_topology" env:"CORRELATION_ENABLE_TOPOLOGY" default:"true"`
	EnableTemporal       bool          `json:"enable_temporal" env:"CORRELATION_ENABLE_TEMPORAL" default:"true"`
	AutoIncidentCreation bool          `json:"auto_incident_creation" env:"CORRELATION_AUTO_INCIDENT" default:"true"`

	// Advanced settings
	SemanticWeight float64 `json:"semantic_weight" env:"CORRELATION_SEMANTIC_WEIGHT" default:"0.4" validate:"min=0.0,max=1.0"`
	TopologyWeight float64 `json:"topology_weight" env:"CORRELATION_TOPOLOGY_WEIGHT" default:"0.3" validate:"min=0.0,max=1.0"`
	TemporalWeight float64 `json:"temporal_weight" env:"CORRELATION_TEMPORAL_WEIGHT" default:"0.2" validate:"min=0.0,max=1.0"`
	RuleWeight     float64 `json:"rule_weight" env:"CORRELATION_RULE_WEIGHT" default:"0.1" validate:"min=0.0,max=1.0"`
}

// SecurityConfig holds security configuration
type SecurityConfig struct {
	EnableHTTPS            bool          `json:"enable_https" env:"SECURITY_ENABLE_HTTPS" default:"false"`
	EnableRateLimiting     bool          `json:"enable_rate_limiting" env:"SECURITY_ENABLE_RATE_LIMITING" default:"true"`
	EnableCORS             bool          `json:"enable_cors" env:"SECURITY_ENABLE_CORS" default:"true"`
	AllowedOrigins         []string      `json:"allowed_origins" env:"SECURITY_ALLOWED_ORIGINS" default:"*"`
	JWTSecret              string        `json:"jwt_secret" env:"JWT_SECRET" sensitive:"true" validate:"min=32"`
	JWTExpiration          time.Duration `json:"jwt_expiration" env:"JWT_EXPIRATION" default:"24h"`
	EnableAuditLogging     bool          `json:"enable_audit_logging" env:"SECURITY_AUDIT_LOGGING" default:"true"`
	PasswordMinLength      int           `json:"password_min_length" env:"SECURITY_PASSWORD_MIN_LENGTH" default:"8" validate:"min=6,max=128"`
	PasswordRequireSpecial bool          `json:"password_require_special" env:"SECURITY_PASSWORD_REQUIRE_SPECIAL" default:"true"`
}

// EnterpriseConfig holds enterprise-specific configuration
type EnterpriseConfig struct {
	MaxServersSupported    int           `json:"max_servers_supported" env:"ENTERPRISE_MAX_SERVERS" default:"150" validate:"min=100,max=1000"`
	DualRegionEnabled      bool          `json:"dual_region_enabled" env:"ENTERPRISE_DUAL_REGION" default:"true"`
	ZeroDowntimeDeployment bool          `json:"zero_downtime_deployment" env:"ENTERPRISE_ZERO_DOWNTIME" default:"true"`
	EnterpriseSupport      bool          `json:"enterprise_support" env:"ENTERPRISE_SUPPORT" default:"true"`
	SLATarget              time.Duration `json:"sla_target" env:"ENTERPRISE_SLA_TARGET" default:"4h"`

	// Compliance and governance
	ComplianceMode    bool `json:"compliance_mode" env:"ENTERPRISE_COMPLIANCE_MODE" default:"false"`
	DataRetentionDays int  `json:"data_retention_days" env:"ENTERPRISE_DATA_RETENTION" default:"90" validate:"min=30,max=2555"`
	BackupEnabled     bool `json:"backup_enabled" env:"ENTERPRISE_BACKUP_ENABLED" default:"true"`
	DisasterRecovery  bool `json:"disaster_recovery" env:"ENTERPRISE_DR_ENABLED" default:"true"`

	// Enterprise integrations
	ServiceNowEnabled bool `json:"servicenow_enabled" env:"ENTERPRISE_SERVICENOW_ENABLED" default:"false"`
	PagerDutyEnabled  bool `json:"pagerduty_enabled" env:"ENTERPRISE_PAGERDUTY_ENABLED" default:"true"`
	SlackEnabled      bool `json:"slack_enabled" env:"ENTERPRISE_SLACK_ENABLED" default:"true"`
}

// ConfigManager manages application configuration with validation and hot-reload
type ConfigManager struct {
	config      *AlertHubConfig
	configPath  string
	environment Environment
	validators  map[string]ValidationRule
	watchers    []ConfigWatcher
	hotReload   bool
}

// ValidationRule represents a configuration validation rule
type ValidationRule struct {
	Field     string
	Required  bool
	MinValue  interface{}
	MaxValue  interface{}
	Options   []string
	Pattern   string
	Validator func(interface{}) error
}

// ConfigWatcher watches for configuration changes
type ConfigWatcher interface {
	OnConfigChanged(config *AlertHubConfig) error
}

// NewConfigManager creates a new configuration manager
func NewConfigManager(environment Environment, configPath string) *ConfigManager {
	cm := &ConfigManager{
		environment: environment,
		configPath:  configPath,
		validators:  make(map[string]ValidationRule),
		watchers:    make([]ConfigWatcher, 0),
		hotReload:   false, // Disabled by default for production safety
	}

	// Initialize validation rules
	cm.initializeValidationRules()

	return cm
}

// LoadConfig loads configuration from multiple sources with precedence:
// 1. Environment variables (highest priority)
// 2. Configuration files
// 3. Default values (lowest priority)
func (cm *ConfigManager) LoadConfig() (*AlertHubConfig, error) {
	// Start with default configuration
	config := cm.getDefaultConfig()

	// Load from configuration file if it exists
	if cm.configPath != "" && fileExists(cm.configPath) {
		if err := cm.loadFromFile(config, cm.configPath); err != nil {
			return nil, fmt.Errorf("failed to load config from file %s: %w", cm.configPath, err)
		}
	}

	// Override with environment variables
	if err := cm.loadFromEnvironment(config); err != nil {
		return nil, fmt.Errorf("failed to load config from environment: %w", err)
	}

	// Apply environment-specific overrides
	cm.applyEnvironmentOverrides(config)

	// Validate configuration
	if err := cm.validateConfig(config); err != nil {
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}

	cm.config = config
	return config, nil
}

func (cm *ConfigManager) getDefaultConfig() *AlertHubConfig {
	return &AlertHubConfig{
		ApplicationName: "alerthub-correlation-engine",
		Version:         "2.0.0",
		Environment:     cm.environment,
		Region:          RegionReno,

		Server: ServerConfig{
			Host:         "0.0.0.0",
			Port:         8080,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
			IdleTimeout:  120 * time.Second,
			TLSEnabled:   false,
		},

		Database: DatabaseConfig{
			MaxOpenConnections: 25,
			MaxIdleConnections: 10,
			ConnMaxLifetime:    60 * time.Minute,
			ConnMaxIdleTime:    10 * time.Minute,
			QueryTimeout:       30 * time.Second,
			SlowQueryThreshold: 1 * time.Second,
			EnableLogging:      cm.environment != EnvironmentProduction,
		},

		Resources:  resource.GetDefaultEnterpriseConfig(),
		Monitoring: monitoring.GetDefaultMonitoringConfig(),

		Integrations: IntegrationsConfig{
			Dynatrace: DynatraceConfig{
				Enabled:           true,
				WebhookEnabled:    true,
				TopologyDiscovery: true,
				PollInterval:      5 * time.Minute,
				TimeoutSeconds:    30,
			},
			Jenkins: JenkinsConfig{
				Enabled:        true,
				WebhookEnabled: true,
				TimeoutSeconds: 30,
			},
			Kubernetes: KubernetesConfig{
				Enabled:             true,
				InCluster:           false,
				Namespaces:          []string{"default", "kube-system"},
				TopologyDiscovery:   true,
				ResourceMonitoring:  true,
				PollIntervalSeconds: 60,
			},
			RBAC: RBACConfig{
				Enabled:        true,
				Provider:       "internal",
				SessionTimeout: 24 * time.Hour,
			},
		},

		Correlation: CorrelationConfig{
			EngineType:           "unified",
			SimilarityThreshold:  0.7,
			DeduplicationWindow:  30 * time.Minute,
			MaxCandidateAlerts:   100,
			EnableAI:             true,
			EnableTopology:       true,
			EnableTemporal:       true,
			AutoIncidentCreation: true,
			SemanticWeight:       0.4,
			TopologyWeight:       0.3,
			TemporalWeight:       0.2,
			RuleWeight:           0.1,
		},

		Security: SecurityConfig{
			EnableHTTPS:            false,
			EnableRateLimiting:     true,
			EnableCORS:             true,
			AllowedOrigins:         []string{"*"},
			JWTExpiration:          24 * time.Hour,
			EnableAuditLogging:     true,
			PasswordMinLength:      8,
			PasswordRequireSpecial: true,
		},

		Enterprise: EnterpriseConfig{
			MaxServersSupported:    150,
			DualRegionEnabled:      true,
			ZeroDowntimeDeployment: true,
			EnterpriseSupport:      true,
			SLATarget:              4 * time.Hour,
			ComplianceMode:         false,
			DataRetentionDays:      90,
			BackupEnabled:          true,
			DisasterRecovery:       true,
			ServiceNowEnabled:      false,
			PagerDutyEnabled:       true,
			SlackEnabled:           true,
		},
	}
}

func (cm *ConfigManager) loadFromFile(config *AlertHubConfig, filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	// Support both JSON and YAML files
	if strings.HasSuffix(filePath, ".json") {
		return json.Unmarshal(data, config)
	}

	// For YAML, would use a YAML parser
	// For now, assume JSON
	return json.Unmarshal(data, config)
}

func (cm *ConfigManager) loadFromEnvironment(config *AlertHubConfig) error {
	return cm.populateFromEnv(reflect.ValueOf(config).Elem(), reflect.TypeOf(config).Elem())
}

func (cm *ConfigManager) populateFromEnv(value reflect.Value, structType reflect.Type) error {
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		fieldType := structType.Field(i)

		// Skip unexported fields
		if !field.CanSet() {
			continue
		}

		// Get environment variable name from tag
		envTag := fieldType.Tag.Get("env")
		defaultTag := fieldType.Tag.Get("default")

		if envTag != "" {
			envValue := os.Getenv(envTag)
			if envValue != "" {
				if err := cm.setFieldFromString(field, envValue); err != nil {
					return fmt.Errorf("failed to set field %s from env %s: %w", fieldType.Name, envTag, err)
				}
				continue
			}
		}

		// Apply default values if not set by environment
		if defaultTag != "" && cm.isZeroValue(field) {
			if err := cm.setFieldFromString(field, defaultTag); err != nil {
				return fmt.Errorf("failed to set default value for field %s: %w", fieldType.Name, err)
			}
		}

		// Recursively handle nested structs
		if field.Kind() == reflect.Struct {
			if err := cm.populateFromEnv(field, field.Type()); err != nil {
				return err
			}
		}
	}

	return nil
}

func (cm *ConfigManager) setFieldFromString(field reflect.Value, value string) error {
	switch field.Kind() {
	case reflect.String:
		field.SetString(value)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if field.Type().String() == "time.Duration" {
			duration, err := time.ParseDuration(value)
			if err != nil {
				return err
			}
			field.SetInt(int64(duration))
		} else {
			intValue, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return err
			}
			field.SetInt(intValue)
		}
	case reflect.Float32, reflect.Float64:
		floatValue, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return err
		}
		field.SetFloat(floatValue)
	case reflect.Bool:
		boolValue, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		field.SetBool(boolValue)
	case reflect.Slice:
		if field.Type().Elem().Kind() == reflect.String {
			// Handle comma-separated string slices
			values := strings.Split(value, ",")
			slice := reflect.MakeSlice(field.Type(), len(values), len(values))
			for i, v := range values {
				slice.Index(i).SetString(strings.TrimSpace(v))
			}
			field.Set(slice)
		}
	default:
		return fmt.Errorf("unsupported field type: %s", field.Kind())
	}

	return nil
}

func (cm *ConfigManager) isZeroValue(field reflect.Value) bool {
	return field.Interface() == reflect.Zero(field.Type()).Interface()
}

func (cm *ConfigManager) applyEnvironmentOverrides(config *AlertHubConfig) {
	switch config.Environment {
	case EnvironmentDevelopment:
		// Development overrides
		config.Database.EnableLogging = true
		config.Security.EnableHTTPS = false
		config.Monitoring.EnableDetailedMetrics = true
		config.Resources.MaxMemoryMB = 512 // Lower memory limit for dev

	case EnvironmentStaging:
		// Staging overrides
		config.Database.EnableLogging = true
		config.Security.EnableHTTPS = true
		config.Enterprise.ComplianceMode = false

	case EnvironmentProduction:
		// Production overrides
		config.Database.EnableLogging = false
		config.Security.EnableHTTPS = true
		config.Enterprise.ComplianceMode = true
		config.Monitoring.HealthCheckInterval = 30 * time.Second

	case EnvironmentTesting:
		// Testing overrides
		config.Database.EnableLogging = true
		config.Security.EnableHTTPS = false
		config.Resources.MaxMemoryMB = 256            // Minimal for testing
		config.Integrations.Dynatrace.Enabled = false // Disable external deps
		config.Integrations.Jenkins.Enabled = false
	}
}

// Configuration validation
func (cm *ConfigManager) initializeValidationRules() {
	// Add validation rules for critical configuration fields
	cm.validators["database.url"] = ValidationRule{
		Field:    "database.url",
		Required: true,
		Validator: func(value interface{}) error {
			url, ok := value.(string)
			if !ok || url == "" {
				return errors.New("database URL is required")
			}
			if !strings.HasPrefix(url, "postgres://") && !strings.HasPrefix(url, "postgresql://") {
				return errors.New("database URL must be a PostgreSQL connection string")
			}
			return nil
		},
	}

	cm.validators["enterprise.max_servers"] = ValidationRule{
		Field:    "enterprise.max_servers_supported",
		Required: true,
		MinValue: 100,
		MaxValue: 1000,
		Validator: func(value interface{}) error {
			servers, ok := value.(int)
			if !ok || servers < 100 {
				return errors.New("enterprise deployment requires minimum 100 servers")
			}
			return nil
		},
	}

	cm.validators["correlation.weights"] = ValidationRule{
		Field: "correlation.weights_sum",
		Validator: func(value interface{}) error {
			config, ok := value.(*AlertHubConfig)
			if !ok {
				return errors.New("invalid config type")
			}

			totalWeight := config.Correlation.SemanticWeight +
				config.Correlation.TopologyWeight +
				config.Correlation.TemporalWeight +
				config.Correlation.RuleWeight

			if totalWeight > 1.1 || totalWeight < 0.9 {
				return fmt.Errorf("correlation weights must sum to ~1.0, got %.2f", totalWeight)
			}
			return nil
		},
	}
}

func (cm *ConfigManager) validateConfig(config *AlertHubConfig) error {
	// Validate using reflection and struct tags
	if err := cm.validateStructTags(reflect.ValueOf(config).Elem(), reflect.TypeOf(config).Elem(), ""); err != nil {
		return err
	}

	// Run custom validators
	for _, rule := range cm.validators {
		if rule.Validator != nil {
			var value interface{}
			if rule.Field == "correlation.weights_sum" {
				value = config
			} else {
				value = cm.getFieldValue(config, rule.Field)
			}

			if err := rule.Validator(value); err != nil {
				return fmt.Errorf("validation failed for %s: %w", rule.Field, err)
			}
		}
	}

	// Enterprise-specific validations
	if err := cm.validateEnterpriseConfig(&config.Enterprise); err != nil {
		return fmt.Errorf("enterprise configuration validation failed: %w", err)
	}

	return nil
}

func (cm *ConfigManager) validateStructTags(value reflect.Value, structType reflect.Type, prefix string) error {
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		fieldType := structType.Field(i)

		fieldName := prefix + strings.ToLower(fieldType.Name)
		validateTag := fieldType.Tag.Get("validate")

		if validateTag != "" {
			if err := cm.validateFieldWithTag(field, fieldName, validateTag); err != nil {
				return err
			}
		}

		// Recursively validate nested structs
		if field.Kind() == reflect.Struct {
			nestedPrefix := fieldName + "."
			if err := cm.validateStructTags(field, field.Type(), nestedPrefix); err != nil {
				return err
			}
		}
	}

	return nil
}

func (cm *ConfigManager) validateFieldWithTag(field reflect.Value, fieldName, validateTag string) error {
	rules := strings.Split(validateTag, ",")

	for _, rule := range rules {
		parts := strings.Split(rule, "=")
		ruleName := strings.TrimSpace(parts[0])

		switch ruleName {
		case "required":
			if cm.isZeroValue(field) {
				return fmt.Errorf("field %s is required", fieldName)
			}
		case "min":
			if len(parts) > 1 {
				minVal, err := strconv.ParseFloat(parts[1], 64)
				if err != nil {
					return err
				}
				if err := cm.validateMin(field, minVal, fieldName); err != nil {
					return err
				}
			}
		case "max":
			if len(parts) > 1 {
				maxVal, err := strconv.ParseFloat(parts[1], 64)
				if err != nil {
					return err
				}
				if err := cm.validateMax(field, maxVal, fieldName); err != nil {
					return err
				}
			}
		case "oneof":
			if len(parts) > 1 {
				options := strings.Split(parts[1], " ")
				if err := cm.validateOneOf(field, options, fieldName); err != nil {
					return err
				}
			}
		case "url":
			if field.Kind() == reflect.String && field.String() != "" {
				url := field.String()
				if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
					return fmt.Errorf("field %s must be a valid URL", fieldName)
				}
			}
		}
	}

	return nil
}

func (cm *ConfigManager) validateMin(field reflect.Value, minVal float64, fieldName string) error {
	switch field.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if float64(field.Int()) < minVal {
			return fmt.Errorf("field %s must be >= %.0f", fieldName, minVal)
		}
	case reflect.Float32, reflect.Float64:
		if field.Float() < minVal {
			return fmt.Errorf("field %s must be >= %.2f", fieldName, minVal)
		}
	}
	return nil
}

func (cm *ConfigManager) validateMax(field reflect.Value, maxVal float64, fieldName string) error {
	switch field.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if float64(field.Int()) > maxVal {
			return fmt.Errorf("field %s must be <= %.0f", fieldName, maxVal)
		}
	case reflect.Float32, reflect.Float64:
		if field.Float() > maxVal {
			return fmt.Errorf("field %s must be <= %.2f", fieldName, maxVal)
		}
	}
	return nil
}

func (cm *ConfigManager) validateOneOf(field reflect.Value, options []string, fieldName string) error {
	if field.Kind() != reflect.String {
		return nil
	}

	value := field.String()
	for _, option := range options {
		if value == strings.TrimSpace(option) {
			return nil
		}
	}

	return fmt.Errorf("field %s must be one of: %s", fieldName, strings.Join(options, ", "))
}

func (cm *ConfigManager) validateEnterpriseConfig(enterprise *EnterpriseConfig) error {
	if enterprise.MaxServersSupported < 100 {
		return errors.New("enterprise deployment requires minimum 100 servers")
	}

	if enterprise.DataRetentionDays < 30 {
		return errors.New("data retention must be at least 30 days for compliance")
	}

	if cm.environment == EnvironmentProduction {
		if !enterprise.BackupEnabled {
			return errors.New("backups must be enabled in production")
		}
		if !enterprise.DisasterRecovery {
			return errors.New("disaster recovery must be enabled in production")
		}
	}

	return nil
}

func (cm *ConfigManager) getFieldValue(config *AlertHubConfig, fieldPath string) interface{} {
	// Simple field path resolution for validation
	// In production, would use a more sophisticated path resolver
	switch fieldPath {
	case "database.url":
		return config.Database.URL
	case "enterprise.max_servers_supported":
		return config.Enterprise.MaxServersSupported
	default:
		return nil
	}
}

// GetConfig returns the current configuration
func (cm *ConfigManager) GetConfig() *AlertHubConfig {
	return cm.config
}

// ValidateAndGetConfig loads and validates configuration, returning enterprise-ready config
func (cm *ConfigManager) ValidateAndGetConfig() (*AlertHubConfig, error) {
	config, err := cm.LoadConfig()
	if err != nil {
		return nil, err
	}

	// Additional enterprise validation
	if config.Environment == EnvironmentProduction {
		if config.Database.URL == "" {
			return nil, errors.New("DATABASE_URL is required for production deployment")
		}

		if config.Security.JWTSecret == "" {
			return nil, errors.New("JWT_SECRET is required for production deployment")
		}

		if config.Enterprise.MaxServersSupported < 100 {
			return nil, errors.New("production deployment requires minimum 100 servers support")
		}
	}

	return config, nil
}

// SaveConfig saves configuration to file
func (cm *ConfigManager) SaveConfig(config *AlertHubConfig, filePath string) error {
	// Mask sensitive fields before saving
	configCopy := cm.maskSensitiveFields(config)

	data, err := json.MarshalIndent(configCopy, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filePath, data, 0644)
}

func (cm *ConfigManager) maskSensitiveFields(config *AlertHubConfig) *AlertHubConfig {
	// Create a copy and mask sensitive fields
	configCopy := *config

	if configCopy.Integrations.Dynatrace.APIToken != "" {
		configCopy.Integrations.Dynatrace.APIToken = "***MASKED***"
	}
	if configCopy.Integrations.Jenkins.APIToken != "" {
		configCopy.Integrations.Jenkins.APIToken = "***MASKED***"
	}
	if configCopy.Integrations.RBAC.OAuth2ClientSecret != "" {
		configCopy.Integrations.RBAC.OAuth2ClientSecret = "***MASKED***"
	}
	if configCopy.Security.JWTSecret != "" {
		configCopy.Security.JWTSecret = "***MASKED***"
	}

	return &configCopy
}

// PrintConfiguration logs the configuration summary at startup (sensitive fields masked).
func (cm *ConfigManager) PrintConfiguration(config *AlertHubConfig) {
	log.Printf("config: %s v%s env=%s region=%s",
		config.ApplicationName, config.Version, config.Environment, config.Region)
	log.Printf("config: db max_conns=%d lifetime=%s query_timeout=%s",
		config.Database.MaxOpenConnections, config.Database.ConnMaxLifetime, config.Database.QueryTimeout)
	log.Printf("config: resources mem=%dMB alert_rps=%d api_rps=%d",
		config.Resources.MaxMemoryMB, config.Resources.AlertCreationRPS, config.Resources.APIRequestsRPS)
	log.Printf("config: correlation engine=%s threshold=%.2f ai=%v auto_incidents=%v",
		config.Correlation.EngineType, config.Correlation.SimilarityThreshold,
		config.Correlation.EnableAI, config.Correlation.AutoIncidentCreation)
	log.Printf("config: integrations dynatrace=%v jenkins=%v k8s=%v rbac=%v(%s)",
		config.Integrations.Dynatrace.Enabled, config.Integrations.Jenkins.Enabled,
		config.Integrations.Kubernetes.Enabled, config.Integrations.RBAC.Enabled,
		config.Integrations.RBAC.Provider)
}

// Utility functions
func fileExists(filename string) bool {
	_, err := os.Stat(filename)
	return !os.IsNotExist(err)
}

// GetEnvironmentFromString converts string to Environment enum
func GetEnvironmentFromString(env string) (Environment, error) {
	switch strings.ToLower(env) {
	case "production", "prod":
		return EnvironmentProduction, nil
	case "staging", "stage":
		return EnvironmentStaging, nil
	case "development", "dev":
		return EnvironmentDevelopment, nil
	case "testing", "test":
		return EnvironmentTesting, nil
	default:
		return "", fmt.Errorf("unknown environment: %s", env)
	}
}

// GetRegionFromString converts string to Region enum
func GetRegionFromString(region string) (Region, error) {
	switch strings.ToLower(region) {
	case "reno":
		return RegionReno, nil
	case "maiden":
		return RegionMaiden, nil
	default:
		return "", fmt.Errorf("unknown region: %s (supported: reno, maiden)", region)
	}
}
