package config

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// ConfigValue represents a configuration value with metadata
type ConfigValue struct {
	Key         string                 `json:"key" db:"key"`
	Value       interface{}            `json:"value" db:"value"`
	Type        string                 `json:"type" db:"type"`
	Environment string                 `json:"environment" db:"environment"`
	Service     string                 `json:"service" db:"service"`
	Encrypted   bool                   `json:"encrypted" db:"encrypted"`
	Version     int                    `json:"version" db:"version"`
	CreatedAt   time.Time              `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at" db:"updated_at"`
	CreatedBy   string                 `json:"created_by" db:"created_by"`
	UpdatedBy   string                 `json:"updated_by" db:"updated_by"`
	Metadata    map[string]interface{} `json:"metadata" db:"metadata"`
}

// ConfigChangeEvent represents a configuration change
type ConfigChangeEvent struct {
	Key         string      `json:"key"`
	OldValue    interface{} `json:"old_value"`
	NewValue    interface{} `json:"new_value"`
	Environment string      `json:"environment"`
	Service     string      `json:"service"`
	ChangedBy   string      `json:"changed_by"`
	Timestamp   time.Time   `json:"timestamp"`
}

// ConfigWatcher defines configuration change watching interface
type ConfigWatcher interface {
	OnChange(event *ConfigChangeEvent)
}

// ConfigManager manages centralized configuration
type ConfigManager struct {
	db          *sql.DB
	cache       map[string]*ConfigValue
	watchers    map[string][]ConfigWatcher
	mu          sync.RWMutex
	environment string
	service     string
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewConfigManager creates a new configuration manager
func NewConfigManager(db *sql.DB, environment, service string) (*ConfigManager, error) {
	ctx, cancel := context.WithCancel(context.Background())

	cm := &ConfigManager{
		db:          db,
		cache:       make(map[string]*ConfigValue),
		watchers:    make(map[string][]ConfigWatcher),
		environment: environment,
		service:     service,
		ctx:         ctx,
		cancel:      cancel,
	}

	// Initialize database schema
	if err := cm.initSchema(); err != nil {
		return nil, fmt.Errorf("failed to initialize config schema: %v", err)
	}

	// Load configuration into cache
	if err := cm.loadCache(); err != nil {
		return nil, fmt.Errorf("failed to load configuration cache: %v", err)
	}

	// Start configuration watcher
	go cm.watchForChanges()

	log.Printf("Configuration manager initialized for %s/%s", environment, service)
	return cm, nil
}

// initSchema creates the configuration tables
func (cm *ConfigManager) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS config_values (
		key TEXT NOT NULL,
		value JSONB NOT NULL,
		type TEXT NOT NULL DEFAULT 'string',
		environment TEXT NOT NULL DEFAULT 'default',
		service TEXT NOT NULL DEFAULT 'global',
		encrypted BOOLEAN NOT NULL DEFAULT FALSE,
		version INTEGER NOT NULL DEFAULT 1,
		created_at TIMESTAMP NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
		created_by TEXT NOT NULL DEFAULT 'system',
		updated_by TEXT NOT NULL DEFAULT 'system',
		metadata JSONB DEFAULT '{}',
		PRIMARY KEY (key, environment, service)
	);

	CREATE INDEX IF NOT EXISTS idx_config_values_env_service ON config_values(environment, service);
	CREATE INDEX IF NOT EXISTS idx_config_values_updated_at ON config_values(updated_at);

	CREATE TABLE IF NOT EXISTS config_history (
		id SERIAL PRIMARY KEY,
		key TEXT NOT NULL,
		old_value JSONB,
		new_value JSONB NOT NULL,
		environment TEXT NOT NULL,
		service TEXT NOT NULL,
		changed_by TEXT NOT NULL,
		change_type TEXT NOT NULL,
		timestamp TIMESTAMP NOT NULL DEFAULT NOW(),
		metadata JSONB DEFAULT '{}'
	);

	CREATE INDEX IF NOT EXISTS idx_config_history_key ON config_history(key);
	CREATE INDEX IF NOT EXISTS idx_config_history_timestamp ON config_history(timestamp);
	`

	_, err := cm.db.Exec(schema)
	return err
}

// loadCache loads all configuration values into memory cache
func (cm *ConfigManager) loadCache() error {
	query := `
		SELECT key, value, type, environment, service, encrypted, version, 
			   created_at, updated_at, created_by, updated_by, 
			   COALESCE(metadata, '{}') as metadata
		FROM config_values 
		WHERE (environment = $1 OR environment = 'global') 
		  AND (service = $2 OR service = 'global')
		ORDER BY environment DESC, service DESC
	`

	rows, err := cm.db.Query(query, cm.environment, cm.service)
	if err != nil {
		return err
	}
	defer rows.Close()

	cm.mu.Lock()
	defer cm.mu.Unlock()

	for rows.Next() {
		var config ConfigValue
		var valueJSON, metadataJSON []byte

		err := rows.Scan(
			&config.Key, &valueJSON, &config.Type, &config.Environment,
			&config.Service, &config.Encrypted, &config.Version,
			&config.CreatedAt, &config.UpdatedAt, &config.CreatedBy,
			&config.UpdatedBy, &metadataJSON,
		)
		if err != nil {
			return err
		}

		// Parse value JSON
		if err := json.Unmarshal(valueJSON, &config.Value); err != nil {
			return fmt.Errorf("failed to parse config value for %s: %v", config.Key, err)
		}

		// Parse metadata JSON
		if err := json.Unmarshal(metadataJSON, &config.Metadata); err != nil {
			return fmt.Errorf("failed to parse config metadata for %s: %v", config.Key, err)
		}

		// Use key as cache key (most specific config wins)
		cacheKey := fmt.Sprintf("%s:%s:%s", config.Key, config.Environment, config.Service)
		cm.cache[cacheKey] = &config

		// Also cache with simple key for easy lookup
		if _, exists := cm.cache[config.Key]; !exists {
			cm.cache[config.Key] = &config
		}
	}

	log.Printf("Loaded %d configuration values into cache", len(cm.cache))
	return rows.Err()
}

// Get retrieves a configuration value
func (cm *ConfigManager) Get(key string) (interface{}, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	// Try specific key first
	specificKey := fmt.Sprintf("%s:%s:%s", key, cm.environment, cm.service)
	if config, exists := cm.cache[specificKey]; exists {
		return config.Value, true
	}

	// Fallback to simple key
	if config, exists := cm.cache[key]; exists {
		return config.Value, true
	}

	return nil, false
}

// GetString retrieves a string configuration value
func (cm *ConfigManager) GetString(key string, defaultValue string) string {
	if value, exists := cm.Get(key); exists {
		if str, ok := value.(string); ok {
			return str
		}
	}
	return defaultValue
}

// GetInt retrieves an integer configuration value
func (cm *ConfigManager) GetInt(key string, defaultValue int) int {
	if value, exists := cm.Get(key); exists {
		switch v := value.(type) {
		case int:
			return v
		case float64:
			return int(v)
		case string:
			// Try to parse string as int
			if i, err := fmt.Sscanf(v, "%d", &defaultValue); err == nil && i == 1 {
				return defaultValue
			}
		}
	}
	return defaultValue
}

// GetBool retrieves a boolean configuration value
func (cm *ConfigManager) GetBool(key string, defaultValue bool) bool {
	if value, exists := cm.Get(key); exists {
		if b, ok := value.(bool); ok {
			return b
		}
		if str, ok := value.(string); ok {
			return str == "true" || str == "1" || str == "yes"
		}
	}
	return defaultValue
}

// GetDuration retrieves a duration configuration value
func (cm *ConfigManager) GetDuration(key string, defaultValue time.Duration) time.Duration {
	if value, exists := cm.Get(key); exists {
		if str, ok := value.(string); ok {
			if duration, err := time.ParseDuration(str); err == nil {
				return duration
			}
		}
	}
	return defaultValue
}

// Set sets a configuration value
func (cm *ConfigManager) Set(key string, value interface{}, updatedBy string) error {
	return cm.SetWithMetadata(key, value, updatedBy, nil)
}

// SetWithMetadata sets a configuration value with metadata
func (cm *ConfigManager) SetWithMetadata(key string, value interface{}, updatedBy string, metadata map[string]interface{}) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Get current value for history
	var oldValue interface{}
	if existing, exists := cm.cache[key]; exists {
		oldValue = existing.Value
	}

	// Determine value type
	valueType := "string"
	switch value.(type) {
	case bool:
		valueType = "boolean"
	case int, int64, float64:
		valueType = "number"
	case map[string]interface{}, []interface{}:
		valueType = "json"
	}

	if metadata == nil {
		metadata = make(map[string]interface{})
	}

	// Marshal value and metadata
	valueJSON, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal config value: %v", err)
	}

	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal config metadata: %v", err)
	}

	// Upsert configuration value
	upsertQuery := `
		INSERT INTO config_values (key, value, type, environment, service, updated_by, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (key, environment, service) 
		DO UPDATE SET 
			value = EXCLUDED.value,
			type = EXCLUDED.type,
			updated_by = EXCLUDED.updated_by,
			updated_at = NOW(),
			version = config_values.version + 1,
			metadata = EXCLUDED.metadata
	`

	_, err = cm.db.Exec(upsertQuery, key, valueJSON, valueType, cm.environment, cm.service, updatedBy, metadataJSON)
	if err != nil {
		return fmt.Errorf("failed to upsert config value: %v", err)
	}

	// Record history
	historyQuery := `
		INSERT INTO config_history (key, old_value, new_value, environment, service, changed_by, change_type)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`

	oldValueJSON, _ := json.Marshal(oldValue)
	changeType := "update"
	if oldValue == nil {
		changeType = "create"
	}

	_, err = cm.db.Exec(historyQuery, key, oldValueJSON, valueJSON, cm.environment, cm.service, updatedBy, changeType)
	if err != nil {
		log.Printf("Failed to record config history: %v", err)
	}

	// Update cache
	config := &ConfigValue{
		Key:         key,
		Value:       value,
		Type:        valueType,
		Environment: cm.environment,
		Service:     cm.service,
		Version:     1, // Will be updated by trigger
		UpdatedAt:   time.Now(),
		UpdatedBy:   updatedBy,
		Metadata:    metadata,
	}

	cm.cache[key] = config

	// Notify watchers
	event := &ConfigChangeEvent{
		Key:         key,
		OldValue:    oldValue,
		NewValue:    value,
		Environment: cm.environment,
		Service:     cm.service,
		ChangedBy:   updatedBy,
		Timestamp:   time.Now(),
	}

	cm.notifyWatchers(key, event)

	log.Printf("Configuration updated: %s = %v", key, value)
	return nil
}

// Delete removes a configuration value
func (cm *ConfigManager) Delete(key string, deletedBy string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Get current value for history
	var oldValue interface{}
	if existing, exists := cm.cache[key]; exists {
		oldValue = existing.Value
	}

	// Delete from database
	deleteQuery := `
		DELETE FROM config_values 
		WHERE key = $1 AND environment = $2 AND service = $3
	`

	_, err := cm.db.Exec(deleteQuery, key, cm.environment, cm.service)
	if err != nil {
		return fmt.Errorf("failed to delete config value: %v", err)
	}

	// Record history
	historyQuery := `
		INSERT INTO config_history (key, old_value, new_value, environment, service, changed_by, change_type)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`

	oldValueJSON, _ := json.Marshal(oldValue)
	_, err = cm.db.Exec(historyQuery, key, oldValueJSON, nil, cm.environment, cm.service, deletedBy, "delete")
	if err != nil {
		log.Printf("Failed to record config deletion history: %v", err)
	}

	// Remove from cache
	delete(cm.cache, key)

	// Notify watchers
	event := &ConfigChangeEvent{
		Key:         key,
		OldValue:    oldValue,
		NewValue:    nil,
		Environment: cm.environment,
		Service:     cm.service,
		ChangedBy:   deletedBy,
		Timestamp:   time.Now(),
	}

	cm.notifyWatchers(key, event)

	log.Printf("Configuration deleted: %s", key)
	return nil
}

// Watch registers a watcher for configuration changes
func (cm *ConfigManager) Watch(keyPattern string, watcher ConfigWatcher) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.watchers[keyPattern] == nil {
		cm.watchers[keyPattern] = make([]ConfigWatcher, 0)
	}
	cm.watchers[keyPattern] = append(cm.watchers[keyPattern], watcher)
}

// notifyWatchers notifies all watchers of configuration changes
func (cm *ConfigManager) notifyWatchers(key string, event *ConfigChangeEvent) {
	for pattern, watchers := range cm.watchers {
		// Simple pattern matching (could be enhanced with regex)
		if pattern == "*" || pattern == key {
			for _, watcher := range watchers {
				go watcher.OnChange(event)
			}
		}
	}
}

// watchForChanges watches for external configuration changes
func (cm *ConfigManager) watchForChanges() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Reload cache to pick up external changes
			if err := cm.loadCache(); err != nil {
				log.Printf("Failed to reload config cache: %v", err)
			}
		case <-cm.ctx.Done():
			return
		}
	}
}

// GetAllConfigs returns all configuration values
func (cm *ConfigManager) GetAllConfigs() map[string]*ConfigValue {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	result := make(map[string]*ConfigValue)
	for key, config := range cm.cache {
		result[key] = config
	}
	return result
}

// LoadFromYAML loads configuration from YAML file
func (cm *ConfigManager) LoadFromYAML(filename string, updatedBy string) error {
	data := make(map[string]interface{})

	// Read and parse YAML file (implementation would read from file system)
	// For now, this is a placeholder
	log.Printf("Loading configuration from YAML: %s", filename)

	for key, value := range data {
		if err := cm.Set(key, value, updatedBy); err != nil {
			return fmt.Errorf("failed to set config %s: %v", key, err)
		}
	}

	return nil
}

// ExportToYAML exports configuration to YAML format
func (cm *ConfigManager) ExportToYAML() ([]byte, error) {
	configs := cm.GetAllConfigs()

	data := make(map[string]interface{})
	for key, config := range configs {
		data[key] = config.Value
	}

	return yaml.Marshal(data)
}

// Close closes the configuration manager
func (cm *ConfigManager) Close() {
	cm.cancel()
	log.Printf("Configuration manager closed")
}
