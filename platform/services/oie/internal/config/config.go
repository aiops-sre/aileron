package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all OIE service configuration.
type Config struct {
	Port                        string
	DatabaseURL                 string
	DBMaxOpenConns              int
	DBMaxIdleConns              int
	DBConnMaxLifetime           time.Duration
	KafkaBrokers                []string
	KafkaConsumerGroupID        string
	KafkaDLQTopic               string
	KafkaInvestigationsTopic    string
	AutoInvestigateSeverities   []string
	PodID                       string
	MaxConcurrentInvestigations int
	InvestigationTimeBudgetMs   int
	JaegerEndpoint              string
	ServiceName                 string
	ServiceVersion              string
	Environment                 string
	// Redis (circuit breaker)
	RedisAddr     string
	RedisPassword string
	// External service URLs
	EIRSBaseURL    string
	OKGBaseURL     string
	// AlertHubBaseURL is the AlertHub backend URL for RCA result writeback.
	// When set, OIE posts the winning hypothesis and confidence back to the
	// incident after investigation completes, so rca_status updates without
	// requiring an operator to open the incident in the UI.
	AlertHubBaseURL string
	OllamaBaseURL  string
	OllamaModel    string
	// Per-role model routing (Aurora/LLMFit pattern):
	// Each investigation task uses the model best suited for it.
	// Falls back to OllamaModel when not set.
	OllamaModelTriage    string // fast model for alert triage / evidence scoring
	OllamaModelRCA       string // quality model for hypothesis-based RCA synthesis
	OllamaModelNarrative string // model for human-readable narrative generation
	// K8s kubeconfigs directory
	KubeconfigsDir string
	// NetApp ONTAP clusters (JSON array from NETAPP_CLUSTERS env var)
	// Each entry: {"cluster":"<ip>","name":"<name>","region":"<mdn|rno>"}
	NetAppClustersJSON string
	NetAppUser         string
	NetAppPassword     string
}

// Load reads all configuration from environment variables.
// Panics on missing required variables so misconfiguration is caught at startup.
func Load() (*Config, error) {
	cfg := &Config{
		Port:                        envOrDefault("OIE_PORT", "8080"),
		DatabaseURL:                 mustEnv("OIE_DATABASE_URL"),
		DBMaxOpenConns:              envInt("OIE_DB_MAX_OPEN_CONNS", 30),
		DBMaxIdleConns:              envInt("OIE_DB_MAX_IDLE_CONNS", 5),
		DBConnMaxLifetime:           envDuration("OIE_DB_CONN_MAX_LIFETIME", 30*time.Minute),
		KafkaBrokers:                envStringSlice("OIE_KAFKA_BROKERS", "localhost:9092"),
		KafkaConsumerGroupID:        envOrDefault("OIE_KAFKA_CONSUMER_GROUP", "oie-investigation-consumer"),
		KafkaDLQTopic:               envOrDefault("OIE_KAFKA_DLQ_TOPIC", "oie.investigations.dlq"),
		KafkaInvestigationsTopic:    envOrDefault("OIE_KAFKA_INVESTIGATIONS_TOPIC", "oie.investigations"),
		AutoInvestigateSeverities:   envStringSlice("OIE_AUTO_INVESTIGATE_SEVERITIES", "critical,high"),
		PodID:                       envOrDefault("POD_NAME", "oie-local") + "-" + envOrDefault("POD_UID", "unknown"),
		MaxConcurrentInvestigations: envInt("OIE_MAX_CONCURRENT_INVESTIGATIONS", 20),
		InvestigationTimeBudgetMs:   envInt("OIE_INVESTIGATION_TIME_BUDGET_MS", 45000),
		JaegerEndpoint:              envOrDefault("JAEGER_COLLECTOR_ENDPOINT", ""),
		ServiceName:                 envOrDefault("OTEL_SERVICE_NAME", "oie"),
		ServiceVersion:              envOrDefault("OIE_VERSION", "dev"),
		Environment:                 envOrDefault("OIE_ENVIRONMENT", "development"),
		RedisAddr:                   envOrDefault("OIE_REDIS_ADDR", "redis-cluster.aileron.svc.cluster.local:6379"),
		RedisPassword:               envOrDefault("OIE_REDIS_PASSWORD", ""),
		EIRSBaseURL:                 envOrDefault("OIE_EIRS_BASE_URL", ""),
		OKGBaseURL:                  envOrDefault("OIE_OKG_BASE_URL", ""),
		// Use OKGBaseURL as AlertHub callback when no separate URL is configured.
		AlertHubBaseURL:             envOrDefault("OIE_ALERTHUB_BASE_URL", envOrDefault("OIE_OKG_BASE_URL", "")),
		OllamaBaseURL:               envOrDefault("OIE_OLLAMA_BASE_URL", "http://ollama.aileron.svc.cluster.local:11434"),
		OllamaModel:                 envOrDefault("OIE_OLLAMA_MODEL", "qwen2.5:3b"),
		OllamaModelTriage:           envOrDefault("OIE_OLLAMA_MODEL_TRIAGE", envOrDefault("OIE_OLLAMA_MODEL", "qwen2.5:3b")),
		OllamaModelRCA:              envOrDefault("OIE_OLLAMA_MODEL_RCA", envOrDefault("OIE_OLLAMA_MODEL", "qwen2.5:3b")),
		OllamaModelNarrative:        envOrDefault("OIE_OLLAMA_MODEL_NARRATIVE", envOrDefault("OIE_OLLAMA_MODEL", "qwen2.5:3b")),
		KubeconfigsDir:              envOrDefault("OIE_KUBECONFIGS_DIR", "/etc/kubeconfigs"),
		NetAppClustersJSON:          envOrDefault("NETAPP_CLUSTERS", ""),
		NetAppUser:                  envOrDefault("NETAPP_USER", "harvest-user"),
		NetAppPassword:              envOrDefault("NETAPP_PASSWORD", ""),
	}
	return cfg, nil
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic(fmt.Sprintf("required environment variable %q is not set", key))
	}
	return v
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func envStringSlice(key, def string) []string {
	v := envOrDefault(key, def)
	parts := strings.Split(v, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
