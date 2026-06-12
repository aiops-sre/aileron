package config

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/google/uuid"
)

var (
	ErrConfigNotFound = errors.New("configuration not found")
)

// SystemConfig represents system configuration
type SystemConfig struct {
	ID        uuid.UUID `json:"id"`
	Category  string    `json:"category"` // auth, ldap, saml, oauth, smtp, slack, etc.
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	IsSecret  bool      `json:"is_secret"`
	UpdatedBy uuid.UUID `json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
}

// LDAPConfig represents LDAP configuration
type LDAPConfig struct {
	Enabled      bool              `json:"enabled"`
	Server       string            `json:"server"`
	Port         int               `json:"port"`
	BaseDN       string            `json:"base_dn"`
	BindDN       string            `json:"bind_dn"`
	BindPassword string            `json:"bind_password"`
	UserFilter   string            `json:"user_filter"`
	UseTLS       bool              `json:"use_tls"`
	GroupMapping map[string]string `json:"group_mapping"`
}

// SAMLConfig represents SAML configuration
type SAMLConfig struct {
	Enabled     bool   `json:"enabled"`
	EntityID    string `json:"entity_id"`
	MetadataURL string `json:"metadata_url"`
	Certificate string `json:"certificate"`
	PrivateKey  string `json:"private_key"`
}

// OAuthConfig represents OAuth configuration
type OAuthConfig struct {
	Enabled      bool     `json:"enabled"`
	Provider     string   `json:"provider"`
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	RedirectURL  string   `json:"redirect_url"`
	Scopes       []string `json:"scopes"`
}

// SMTPConfig represents SMTP configuration
type SMTPConfig struct {
	Enabled  bool   `json:"enabled"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
	UseTLS   bool   `json:"use_tls"`
}

// SlackConfig represents Slack configuration
type SlackConfig struct {
	Enabled    bool   `json:"enabled"`
	WebhookURL string `json:"webhook_url"`
	Channel    string `json:"channel"`
	BotToken   string `json:"bot_token"`
}

// ConfigService handles system configuration
type ConfigService struct {
	db *sql.DB
}

// NewConfigService creates a new config service
func NewConfigService(db *sql.DB) *ConfigService {
	return &ConfigService{db: db}
}

// GetConfig retrieves a configuration value
func (s *ConfigService) GetConfig(ctx context.Context, category, key string) (*SystemConfig, error) {
	config := &SystemConfig{}

	query := "SELECT id, category, key, value, is_secret, updated_by, updated_at FROM system_config WHERE category = $1 AND key = $2"

	err := s.db.QueryRowContext(ctx, query, category, key).Scan(
		&config.ID, &config.Category, &config.Key, &config.Value,
		&config.IsSecret, &config.UpdatedBy, &config.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, ErrConfigNotFound
	}
	if err != nil {
		return nil, err
	}

	return config, nil
}

// SetConfig sets a configuration value
func (s *ConfigService) SetConfig(ctx context.Context, category, key, value string, isSecret bool, userID uuid.UUID) error {
	query := `
		INSERT INTO system_config (id, category, key, value, is_secret, updated_by, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (category, key) DO UPDATE
		SET value = $4, is_secret = $5, updated_by = $6, updated_at = $7
	`

	_, err := s.db.ExecContext(ctx, query,
		uuid.New(), category, key, value, isSecret, userID, time.Now(),
	)

	return err
}

// GetLDAPConfig retrieves LDAP configuration
func (s *ConfigService) GetLDAPConfig(ctx context.Context) (*LDAPConfig, error) {
	config := &LDAPConfig{}

	// Get all LDAP configs
	rows, err := s.db.QueryContext(ctx, "SELECT key, value FROM system_config WHERE category = 'ldap'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	configMap := make(map[string]string)
	for rows.Next() {
		var key, value string
		rows.Scan(&key, &value)
		configMap[key] = value
	}

	// Parse into struct
	if enabled, ok := configMap["enabled"]; ok {
		config.Enabled = enabled == "true"
	}
	config.Server = configMap["server"]
	config.BaseDN = configMap["base_dn"]
	config.BindDN = configMap["bind_dn"]
	config.BindPassword = configMap["bind_password"]
	config.UserFilter = configMap["user_filter"]
	config.UseTLS = configMap["use_tls"] == "true"

	return config, nil
}

// SaveLDAPConfig saves LDAP configuration
func (s *ConfigService) SaveLDAPConfig(ctx context.Context, config *LDAPConfig, userID uuid.UUID) error {
	// Save each config value
	configs := map[string]string{
		"enabled":       fmt.Sprintf("%v", config.Enabled),
		"server":        config.Server,
		"port":          fmt.Sprintf("%d", config.Port),
		"base_dn":       config.BaseDN,
		"bind_dn":       config.BindDN,
		"bind_password": config.BindPassword,
		"user_filter":   config.UserFilter,
		"use_tls":       fmt.Sprintf("%v", config.UseTLS),
	}

	for key, value := range configs {
		isSecret := key == "bind_password"
		err := s.SetConfig(ctx, "ldap", key, value, isSecret, userID)
		if err != nil {
			return err
		}
	}

	return nil
}

// GetSAMLConfig retrieves SAML configuration
func (s *ConfigService) GetSAMLConfig(ctx context.Context) (*SAMLConfig, error) {
	config := &SAMLConfig{}

	rows, err := s.db.QueryContext(ctx, "SELECT key, value FROM system_config WHERE category = 'saml'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	configMap := make(map[string]string)
	for rows.Next() {
		var key, value string
		rows.Scan(&key, &value)
		configMap[key] = value
	}

	config.Enabled = configMap["enabled"] == "true"
	config.EntityID = configMap["entity_id"]
	config.MetadataURL = configMap["metadata_url"]
	config.Certificate = configMap["certificate"]
	config.PrivateKey = configMap["private_key"]

	return config, nil
}

// SaveSAMLConfig saves SAML configuration
func (s *ConfigService) SaveSAMLConfig(ctx context.Context, config *SAMLConfig, userID uuid.UUID) error {
	configs := map[string]string{
		"enabled":      fmt.Sprintf("%v", config.Enabled),
		"entity_id":    config.EntityID,
		"metadata_url": config.MetadataURL,
		"certificate":  config.Certificate,
		"private_key":  config.PrivateKey,
	}

	for key, value := range configs {
		isSecret := key == "private_key"
		err := s.SetConfig(ctx, "saml", key, value, isSecret, userID)
		if err != nil {
			return err
		}
	}

	return nil
}

// TestLDAPConnection tests LDAP connection
func (s *ConfigService) TestLDAPConnection(ctx context.Context, config *LDAPConfig) error {
	if config.Server == "" {
		return errors.New("LDAP server not configured")
	}

	// Construct server address
	addr := fmt.Sprintf("%s:%d", config.Server, config.Port)

	var l *ldap.Conn
	var err error

	// Connect with or without TLS
	if config.UseTLS {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: false,
			ServerName:         config.Server,
		}
		l, err = ldap.DialTLS("tcp", addr, tlsConfig)
	} else {
		l, err = ldap.Dial("tcp", addr)
	}

	if err != nil {
		return fmt.Errorf("failed to connect to LDAP server: %v", err)
	}
	defer l.Close()

	// Test bind with provided credentials
	if config.BindDN != "" && config.BindPassword != "" {
		err = l.Bind(config.BindDN, config.BindPassword)
		if err != nil {
			return fmt.Errorf("LDAP bind failed: %v", err)
		}
	}

	// Try a simple search to verify connection
	searchRequest := ldap.NewSearchRequest(
		config.BaseDN,
		ldap.ScopeBaseObject,
		ldap.NeverDerefAliases,
		0, 0, false,
		"(objectClass=*)",
		[]string{"dn"},
		nil,
	)

	_, err = l.Search(searchRequest)
	if err != nil {
		return fmt.Errorf("LDAP search test failed: %v", err)
	}

	return nil
}

// TestSAMLConnection tests SAML connection
func (s *ConfigService) TestSAMLConnection(ctx context.Context, config *SAMLConfig) error {
	if config.MetadataURL == "" {
		return errors.New("SAML metadata URL not configured")
	}

	// Try to fetch metadata
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Get(config.MetadataURL)
	if err != nil {
		return fmt.Errorf("failed to fetch SAML metadata: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("SAML metadata endpoint returned status %d", resp.StatusCode)
	}

	// Basic validation of metadata (should be XML)
	contentType := resp.Header.Get("Content-Type")
	if contentType != "application/xml" && contentType != "text/xml" && contentType != "application/samlmetadata+xml" {
		return fmt.Errorf("SAML metadata has unexpected content type: %s", contentType)
	}

	return nil
}

// AIConfig represents AI configuration
type AIConfig struct {
	Enabled     bool     `json:"enabled"`
	Provider    string   `json:"provider"` // openai, anthropic, azure, etc.
	APIKey      string   `json:"api_key"`
	Endpoint    string   `json:"endpoint"`
	Model       string   `json:"model"`
	MaxTokens   int      `json:"max_tokens"`
	Temperature float64  `json:"temperature"`
	Features    []string `json:"features"` // enabled features like classification, recommendations, etc.
}

// GeneralConfig represents general system configuration
type GeneralConfig struct {
	SystemName      string `json:"system_name"`
	SessionTimeout  int    `json:"session_timeout"` // hours
	AutoRefresh     int    `json:"auto_refresh"`    // seconds
	MFARequired     bool   `json:"mfa_required"`
	MaintenanceMode bool   `json:"maintenance_mode"`
	DefaultTimezone string `json:"default_timezone"`
}

// GetOAuthConfig retrieves OAuth configuration
func (s *ConfigService) GetOAuthConfig(ctx context.Context) (*OAuthConfig, error) {
	config := &OAuthConfig{}

	rows, err := s.db.QueryContext(ctx, "SELECT key, value FROM system_config WHERE category = 'oauth'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	configMap := make(map[string]string)
	for rows.Next() {
		var key, value string
		rows.Scan(&key, &value)
		configMap[key] = value
	}

	config.Enabled = configMap["enabled"] == "true"
	config.Provider = configMap["provider"]
	config.ClientID = configMap["client_id"]
	config.ClientSecret = configMap["client_secret"]
	config.RedirectURL = configMap["redirect_url"]

	return config, nil
}

// SaveOAuthConfig saves OAuth configuration
func (s *ConfigService) SaveOAuthConfig(ctx context.Context, config *OAuthConfig, userID uuid.UUID) error {
	configs := map[string]string{
		"enabled":       fmt.Sprintf("%v", config.Enabled),
		"provider":      config.Provider,
		"client_id":     config.ClientID,
		"client_secret": config.ClientSecret,
		"redirect_url":  config.RedirectURL,
	}

	for key, value := range configs {
		isSecret := key == "client_secret"
		err := s.SetConfig(ctx, "oauth", key, value, isSecret, userID)
		if err != nil {
			return err
		}
	}

	return nil
}

// GetSMTPConfig retrieves SMTP configuration
func (s *ConfigService) GetSMTPConfig(ctx context.Context) (*SMTPConfig, error) {
	config := &SMTPConfig{}

	rows, err := s.db.QueryContext(ctx, "SELECT key, value FROM system_config WHERE category = 'smtp'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	configMap := make(map[string]string)
	for rows.Next() {
		var key, value string
		rows.Scan(&key, &value)
		configMap[key] = value
	}

	config.Enabled = configMap["enabled"] == "true"
	config.Host = configMap["host"]
	if port, err := strconv.Atoi(configMap["port"]); err == nil {
		config.Port = port
	}
	config.Username = configMap["username"]
	config.Password = configMap["password"]
	config.From = configMap["from"]
	config.UseTLS = configMap["use_tls"] == "true"

	return config, nil
}

// SaveSMTPConfig saves SMTP configuration
func (s *ConfigService) SaveSMTPConfig(ctx context.Context, config *SMTPConfig, userID uuid.UUID) error {
	configs := map[string]string{
		"enabled":  fmt.Sprintf("%v", config.Enabled),
		"host":     config.Host,
		"port":     fmt.Sprintf("%d", config.Port),
		"username": config.Username,
		"password": config.Password,
		"from":     config.From,
		"use_tls":  fmt.Sprintf("%v", config.UseTLS),
	}

	for key, value := range configs {
		isSecret := key == "password"
		err := s.SetConfig(ctx, "smtp", key, value, isSecret, userID)
		if err != nil {
			return err
		}
	}

	return nil
}

// GetSlackConfig retrieves Slack configuration
func (s *ConfigService) GetSlackConfig(ctx context.Context) (*SlackConfig, error) {
	config := &SlackConfig{}

	rows, err := s.db.QueryContext(ctx, "SELECT key, value FROM system_config WHERE category = 'slack'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	configMap := make(map[string]string)
	for rows.Next() {
		var key, value string
		rows.Scan(&key, &value)
		configMap[key] = value
	}

	config.Enabled = configMap["enabled"] == "true"
	config.WebhookURL = configMap["webhook_url"]
	config.Channel = configMap["channel"]
	config.BotToken = configMap["bot_token"]

	return config, nil
}

// SaveSlackConfig saves Slack configuration
func (s *ConfigService) SaveSlackConfig(ctx context.Context, config *SlackConfig, userID uuid.UUID) error {
	configs := map[string]string{
		"enabled":     fmt.Sprintf("%v", config.Enabled),
		"webhook_url": config.WebhookURL,
		"channel":     config.Channel,
		"bot_token":   config.BotToken,
	}

	for key, value := range configs {
		isSecret := key == "bot_token"
		err := s.SetConfig(ctx, "slack", key, value, isSecret, userID)
		if err != nil {
			return err
		}
	}

	return nil
}

// GetAIConfig retrieves AI configuration
func (s *ConfigService) GetAIConfig(ctx context.Context) (*AIConfig, error) {
	config := &AIConfig{}

	rows, err := s.db.QueryContext(ctx, "SELECT key, value FROM system_config WHERE category = 'ai'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	configMap := make(map[string]string)
	for rows.Next() {
		var key, value string
		rows.Scan(&key, &value)
		configMap[key] = value
	}

	config.Enabled = configMap["enabled"] == "true"
	config.Provider = configMap["provider"]
	config.APIKey = configMap["api_key"]
	config.Endpoint = configMap["endpoint"]
	config.Model = configMap["model"]
	if maxTokens, err := strconv.Atoi(configMap["max_tokens"]); err == nil {
		config.MaxTokens = maxTokens
	}
	if temp, err := strconv.ParseFloat(configMap["temperature"], 64); err == nil {
		config.Temperature = temp
	}

	// Parse features array from JSON
	if featuresJSON := configMap["features"]; featuresJSON != "" {
		var features []string
		if err := json.Unmarshal([]byte(featuresJSON), &features); err == nil {
			config.Features = features
		}
	}

	return config, nil
}

// SaveAIConfig saves AI configuration
func (s *ConfigService) SaveAIConfig(ctx context.Context, config *AIConfig, userID uuid.UUID) error {
	// Serialize features array to JSON
	featuresJSON := "[]"
	if len(config.Features) > 0 {
		if data, err := json.Marshal(config.Features); err == nil {
			featuresJSON = string(data)
		}
	}

	configs := map[string]string{
		"enabled":     fmt.Sprintf("%v", config.Enabled),
		"provider":    config.Provider,
		"api_key":     config.APIKey,
		"endpoint":    config.Endpoint,
		"model":       config.Model,
		"max_tokens":  fmt.Sprintf("%d", config.MaxTokens),
		"temperature": fmt.Sprintf("%.2f", config.Temperature),
		"features":    featuresJSON,
	}

	for key, value := range configs {
		isSecret := key == "api_key"
		err := s.SetConfig(ctx, "ai", key, value, isSecret, userID)
		if err != nil {
			return err
		}
	}

	return nil
}

// GetGeneralConfig retrieves general system configuration
func (s *ConfigService) GetGeneralConfig(ctx context.Context) (*GeneralConfig, error) {
	config := &GeneralConfig{}

	rows, err := s.db.QueryContext(ctx, "SELECT key, value FROM system_config WHERE category = 'general'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	configMap := make(map[string]string)
	for rows.Next() {
		var key, value string
		rows.Scan(&key, &value)
		configMap[key] = value
	}

	config.SystemName = configMap["system_name"]
	if timeout, err := strconv.Atoi(configMap["session_timeout"]); err == nil {
		config.SessionTimeout = timeout
	}
	if refresh, err := strconv.Atoi(configMap["auto_refresh"]); err == nil {
		config.AutoRefresh = refresh
	}
	config.MFARequired = configMap["mfa_required"] == "true"
	config.MaintenanceMode = configMap["maintenance_mode"] == "true"
	config.DefaultTimezone = configMap["default_timezone"]

	return config, nil
}

// SaveGeneralConfig saves general system configuration
func (s *ConfigService) SaveGeneralConfig(ctx context.Context, config *GeneralConfig, userID uuid.UUID) error {
	configs := map[string]string{
		"system_name":      config.SystemName,
		"session_timeout":  fmt.Sprintf("%d", config.SessionTimeout),
		"auto_refresh":     fmt.Sprintf("%d", config.AutoRefresh),
		"mfa_required":     fmt.Sprintf("%v", config.MFARequired),
		"maintenance_mode": fmt.Sprintf("%v", config.MaintenanceMode),
		"default_timezone": config.DefaultTimezone,
	}

	for key, value := range configs {
		err := s.SetConfig(ctx, "general", key, value, false, userID)
		if err != nil {
			return err
		}
	}

	return nil
}
