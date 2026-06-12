package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	_ "github.com/lib/pq"
)

// InitializeDatabase initializes the database schema and default data
func InitializeDatabase(db *sql.DB) error {
	log.Println("Initializing database schema...")

	ctx := context.Background()

	// Run migrations in order
	migrations := []string{
		// Enable UUID extension
		`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`,

		// Create roles table
		`CREATE TABLE IF NOT EXISTS roles (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			name VARCHAR(100) UNIQUE NOT NULL,
			description TEXT,
			is_system_role BOOLEAN DEFAULT false,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)`,

		// Create permissions table
		`CREATE TABLE IF NOT EXISTS permissions (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			name VARCHAR(100) UNIQUE NOT NULL,
			resource VARCHAR(100) NOT NULL,
			action VARCHAR(50) NOT NULL,
			created_at TIMESTAMP DEFAULT NOW()
		)`,
		// Add description column if it doesn't exist
		`ALTER TABLE permissions ADD COLUMN IF NOT EXISTS description TEXT`,

		// Create role_permissions table
		`CREATE TABLE IF NOT EXISTS role_permissions (
			role_id UUID REFERENCES roles(id) ON DELETE CASCADE,
			permission_id UUID REFERENCES permissions(id) ON DELETE CASCADE,
			created_at TIMESTAMP DEFAULT NOW(),
			PRIMARY KEY (role_id, permission_id)
		)`,

		// Create users table
		`CREATE TABLE IF NOT EXISTS users (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			email VARCHAR(255) UNIQUE NOT NULL,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)`,

		// Drop old columns if they exist that might have NOT NULL constraints
		`ALTER TABLE users DROP COLUMN IF EXISTS name`,
		`ALTER TABLE users DROP COLUMN IF EXISTS provider`,
		`ALTER TABLE users DROP COLUMN IF EXISTS provider_id`,

		// Add missing columns to users table
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS username VARCHAR(255)`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS password_hash VARCHAR(255)`,

		// Add unique constraint on username if it doesn't exist
		`DO $$
		BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_constraint
				WHERE conname = 'users_username_key'
			) THEN
				ALTER TABLE users ADD CONSTRAINT users_username_key UNIQUE (username);
			END IF;
		END $$`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS role_id UUID REFERENCES roles(id)`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS full_name VARCHAR(255)`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS is_active BOOLEAN DEFAULT true`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS is_verified BOOLEAN DEFAULT true`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS mfa_enabled BOOLEAN DEFAULT false`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS login_count INTEGER DEFAULT 0`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS failed_login_attempts INTEGER DEFAULT 0`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS locked_until TIMESTAMP`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS last_login TIMESTAMP`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS avatar_url TEXT`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS phone VARCHAR(50)`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS timezone VARCHAR(100) DEFAULT 'UTC'`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS preferences JSONB DEFAULT '{}'`,

		// Add MAS authentication columns
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS mas_enabled BOOLEAN DEFAULT false`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS mas_username VARCHAR(255)`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS last_mas_sync TIMESTAMP`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS role VARCHAR(100)`,

		// Add OAuth token columns for AI Assistant integration
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS oauth_id_token TEXT`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS oauth_token_updated_at TIMESTAMP`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS ai_auto_connect BOOLEAN DEFAULT false`,

		// Add Floodgate hybrid authentication columns
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS oauth_source VARCHAR(50)`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS vpn_ip VARCHAR(45)`,

		// Add indexes for Floodgate hybrid auth
		`CREATE INDEX IF NOT EXISTS idx_users_oauth_source ON users(oauth_source) WHERE oauth_source IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_users_oauth_token_updated ON users(oauth_token_updated_at) WHERE oauth_id_token IS NOT NULL`,

		// Create user_mas_groups table for MAS group tracking
		`CREATE TABLE IF NOT EXISTS user_mas_groups (
			user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			mas_group VARCHAR(255) NOT NULL,
			synced_at TIMESTAMP NOT NULL DEFAULT NOW(),
			PRIMARY KEY (user_id, mas_group)
		)`,

		// Create MAS settings table
		`CREATE TABLE IF NOT EXISTS mas_settings (
			key VARCHAR(100) PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TIMESTAMP DEFAULT NOW()
		)`,

		// Create user_sessions table
		`CREATE TABLE IF NOT EXISTS user_sessions (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			user_id UUID REFERENCES users(id) ON DELETE CASCADE,
			token_hash VARCHAR(255),
			refresh_token_hash VARCHAR(255),
			ip_address VARCHAR(100),
			user_agent TEXT,
			expires_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT NOW()
		)`,

		// Create audit_logs table
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			user_id UUID REFERENCES users(id) ON DELETE SET NULL,
			action VARCHAR(100) NOT NULL,
			resource_type VARCHAR(100),
			resource_id UUID,
			old_value JSONB,
			new_value JSONB,
			details JSONB,
			ip_address VARCHAR(100),
			user_agent TEXT,
			status VARCHAR(50),
			error_message TEXT,
			created_at TIMESTAMP DEFAULT NOW()
		)`,
		// Add missing columns to audit_logs
		`ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS old_value JSONB`,
		`ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS new_value JSONB`,
		`ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS status VARCHAR(50)`,
		`ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS error_message TEXT`,

		// Create integration_configs table
		`CREATE TABLE IF NOT EXISTS integration_configs (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			type VARCHAR(50) UNIQUE NOT NULL,
			name VARCHAR(255),
			enabled BOOLEAN DEFAULT false,
			config JSONB NOT NULL DEFAULT '{}',
			credentials JSONB DEFAULT '{}',
			last_sync TIMESTAMP,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)`,

		// Create incidents table
		`CREATE TABLE IF NOT EXISTS incidents (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			incident_number VARCHAR(50) UNIQUE NOT NULL,
			title VARCHAR(500) NOT NULL,
			description TEXT,
			severity VARCHAR(20) NOT NULL,
			status VARCHAR(50) NOT NULL DEFAULT 'open',
			source VARCHAR(100),
			external_id VARCHAR(255),
			external_url TEXT,
			affected_services TEXT,
			assigned_to UUID REFERENCES users(id),
			started_at TIMESTAMP,
			acknowledged_at TIMESTAMP,
			resolved_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)`,

		// Create alerts table
		`CREATE TABLE IF NOT EXISTS alerts (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			title VARCHAR(500) NOT NULL,
			description TEXT,
			message TEXT,
			severity VARCHAR(20) NOT NULL,
			status VARCHAR(50) NOT NULL DEFAULT 'open',
			source VARCHAR(100),
			external_id VARCHAR(255),
			tags JSONB DEFAULT '[]',
			ai_classification VARCHAR(100),
			ai_analysis JSONB,
			assigned_to UUID REFERENCES users(id),
			incident_id UUID REFERENCES incidents(id),
			acknowledged_at TIMESTAMP,
			resolved_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)`,

		// Add missing columns to alerts table
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS source_id VARCHAR(255)`,
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS source_url TEXT`,
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS labels JSONB DEFAULT '{}'`,
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS metadata JSONB DEFAULT '{}'`,
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS created_by UUID REFERENCES users(id)`,
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS team_id UUID`,
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS ai_confidence FLOAT DEFAULT 0`,
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS fingerprint VARCHAR(255)`,
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS count INTEGER DEFAULT 1`,
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS first_seen_at TIMESTAMP DEFAULT NOW()`,
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMP DEFAULT NOW()`,
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS acknowledged_by UUID REFERENCES users(id)`,
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS resolved_by UUID REFERENCES users(id)`,
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS resolution_notes TEXT`,
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS resolution_type VARCHAR(50) DEFAULT 'manual'`,

		// Prom-dashboard inspired features
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS linked_alerts JSONB DEFAULT NULL`,
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS linked_to JSONB DEFAULT NULL`,
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS maintenance_status INTEGER DEFAULT 0`,
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS maintenance_start_time TIMESTAMP DEFAULT NULL`,
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS maintenance_end_time TIMESTAMP DEFAULT NULL`,
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS maintenance_comment TEXT DEFAULT NULL`,
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS is_alert_active BOOLEAN DEFAULT TRUE`,
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS sla_met_responsetime BOOLEAN DEFAULT FALSE`,
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS sla_met_resolutiontime BOOLEAN DEFAULT FALSE`,
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS first_email_timestamp TIMESTAMP DEFAULT NULL`,
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS recent_email_timestamp TIMESTAMP DEFAULT NULL`,

		// Create maintenance_windows table
		`CREATE TABLE IF NOT EXISTS maintenance_windows (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			alert_id UUID REFERENCES alerts(id) ON DELETE CASCADE,
			url_name TEXT,
			alertname TEXT,
			start_time TIMESTAMP NOT NULL,
			end_time TIMESTAMP,
			maintenance_status INTEGER DEFAULT 1,
			created_by UUID REFERENCES users(id),
			comment TEXT,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)`,

		// Create alert_comments table
		`CREATE TABLE IF NOT EXISTS alert_comments (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			alert_id UUID REFERENCES alerts(id) ON DELETE CASCADE,
			user_id UUID REFERENCES users(id),
			comment TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT NOW()
		)`,

		// Create alert_history table
		`CREATE TABLE IF NOT EXISTS alert_history (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			alert_id UUID REFERENCES alerts(id) ON DELETE CASCADE,
			user_id UUID REFERENCES users(id),
			action VARCHAR(100) NOT NULL,
			old_value JSONB,
			new_value JSONB,
			created_at TIMESTAMP DEFAULT NOW()
		)`,

		// Indexes for prom-dashboard features
		`CREATE INDEX IF NOT EXISTS idx_alerts_is_active ON alerts(is_alert_active)`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_maintenance ON alerts(maintenance_status) WHERE maintenance_status = 1`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_linked ON alerts USING GIN (linked_alerts) WHERE linked_alerts IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_sla_response ON alerts(sla_met_responsetime) WHERE sla_met_responsetime = FALSE`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_sla_resolution ON alerts(sla_met_resolutiontime) WHERE sla_met_resolutiontime = FALSE`,
		`CREATE INDEX IF NOT EXISTS idx_maintenance_alert ON maintenance_windows(alert_id)`,
		`CREATE INDEX IF NOT EXISTS idx_maintenance_status ON maintenance_windows(maintenance_status)`,
		`CREATE INDEX IF NOT EXISTS idx_alert_comments_alert ON alert_comments(alert_id)`,
		`CREATE INDEX IF NOT EXISTS idx_alert_history_alert ON alert_history(alert_id, created_at DESC)`,

		// Create AI chat sessions table
		`CREATE TABLE IF NOT EXISTS ai_chat_sessions (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			user_id UUID REFERENCES users(id) ON DELETE CASCADE,
			model VARCHAR(255) NOT NULL,
			title VARCHAR(500),
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)`,

		// Create AI chat messages table
		`CREATE TABLE IF NOT EXISTS ai_chat_messages (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			session_id UUID REFERENCES ai_chat_sessions(id) ON DELETE CASCADE,
			role VARCHAR(50) NOT NULL,
			content TEXT NOT NULL,
			tokens_used INTEGER DEFAULT 0,
			created_at TIMESTAMP DEFAULT NOW()
		)`,

		// Create indexes for AI chat tables
		`CREATE INDEX IF NOT EXISTS idx_ai_chat_sessions_user_id ON ai_chat_sessions(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_ai_chat_messages_session_id ON ai_chat_messages(session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_ai_chat_sessions_updated_at ON ai_chat_sessions(updated_at DESC)`,

		// Create SAML configuration table
		`CREATE TABLE IF NOT EXISTS saml_config (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			enabled BOOLEAN DEFAULT false,
			entity_id VARCHAR(500),
			sso_url VARCHAR(500),
			certificate TEXT,
			private_key TEXT,
			idp_metadata_url VARCHAR(500),
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)`,

		// Create oncall_schedules table
		`CREATE TABLE IF NOT EXISTS oncall_schedules (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			user_id UUID REFERENCES users(id) ON DELETE CASCADE,
			role VARCHAR(100),
			start_time TIMESTAMP NOT NULL,
			end_time TIMESTAMP NOT NULL,
			priority INTEGER DEFAULT 1,
			created_at TIMESTAMP DEFAULT NOW()
		)`,

		// Create indexes for performance
		`CREATE INDEX IF NOT EXISTS idx_alerts_created_at ON alerts(created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_status ON alerts(status)`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_severity ON alerts(severity)`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_source ON alerts(source)`,
		`CREATE INDEX IF NOT EXISTS idx_incidents_created_at ON incidents(created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_incidents_status ON incidents(status)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at ON audit_logs(created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_logs_user_id ON audit_logs(user_id)`,

		// Topology configuration table
		`CREATE TABLE IF NOT EXISTS topology_config (
			id TEXT PRIMARY KEY DEFAULT 'default',
			config JSONB NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW()
		)`,

		// Insert default topology config
		`INSERT INTO topology_config (id, config, created_at, updated_at)
		VALUES (
			'default',
			'{"k8s_clusters":[],"cloudstack":[],"ipmi":[],"discovery_interval":"5m","real_time_discovery":true}'::jsonb,
			NOW(),
			NOW()
		)
		ON CONFLICT (id) DO NOTHING`,

		// Topology config index
		`CREATE INDEX IF NOT EXISTS idx_topology_config_updated_at ON topology_config(updated_at)`,

		// Infrastructure correlations table
		`CREATE TABLE IF NOT EXISTS infrastructure_correlations (
			id TEXT PRIMARY KEY,
			correlation_data JSONB NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT NOW()
		)`,

		// Add root cause to alerts
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS root_cause JSONB DEFAULT NULL`,
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS correlation_id TEXT`,

		// Indexes for correlations
		`CREATE INDEX IF NOT EXISTS idx_alerts_correlation ON alerts(correlation_id) WHERE correlation_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_root_cause ON alerts USING GIN (root_cause) WHERE root_cause IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_infra_corr_created ON infrastructure_correlations(created_at)`,

		// Alert correlation tables
		`CREATE TABLE IF NOT EXISTS alert_correlations (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			alert_id UUID NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
			correlation_id VARCHAR(255) NOT NULL,
			similar_alerts JSONB DEFAULT '[]',
			confidence_score DECIMAL(3,2) DEFAULT 0.0,
			correlation_type VARCHAR(50) NOT NULL DEFAULT 'ml_similarity',
			is_duplicate BOOLEAN DEFAULT FALSE,
			duplicate_of UUID REFERENCES alerts(id) ON DELETE SET NULL,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW(),
			UNIQUE(alert_id)
		)`,

		// Correlation rules table
		`CREATE TABLE IF NOT EXISTS correlation_rules (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			name VARCHAR(255) NOT NULL,
			description TEXT,
			conditions JSONB NOT NULL DEFAULT '[]',
			actions JSONB NOT NULL DEFAULT '[]',
			priority INTEGER DEFAULT 0,
			enabled BOOLEAN DEFAULT TRUE,
			created_by UUID REFERENCES users(id) ON DELETE SET NULL,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW(),
			UNIQUE(name)
		)`,

		// Workflow engine tables
		`CREATE TABLE IF NOT EXISTS workflows (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			name VARCHAR(255) NOT NULL,
			description TEXT,
			workflow_yaml TEXT NOT NULL,
			enabled BOOLEAN DEFAULT TRUE,
			triggers JSONB DEFAULT '[]',
			created_by UUID REFERENCES users(id) ON DELETE SET NULL,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW(),
			UNIQUE(name)
		)`,

		`CREATE TABLE IF NOT EXISTS workflow_executions (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			workflow_id UUID REFERENCES workflows(id) ON DELETE CASCADE,
			status VARCHAR(50) NOT NULL DEFAULT 'running',
			trigger_data JSONB,
			execution_log TEXT,
			started_at TIMESTAMP DEFAULT NOW(),
			completed_at TIMESTAMP,
			error_message TEXT
		)`,

		// Integrations table
		`CREATE TABLE IF NOT EXISTS integrations (
			id TEXT PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			type VARCHAR(100) NOT NULL,
			enabled BOOLEAN DEFAULT TRUE,
			status VARCHAR(50) DEFAULT 'disconnected',
			config JSONB DEFAULT '{}',
			last_sync TIMESTAMP,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)`,

		// Indexes for new tables
		`CREATE INDEX IF NOT EXISTS idx_workflows_enabled ON workflows(enabled) WHERE enabled = TRUE`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_executions_workflow_id ON workflow_executions(workflow_id)`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_executions_status ON workflow_executions(status)`,
		`CREATE INDEX IF NOT EXISTS idx_integrations_enabled ON integrations(enabled) WHERE enabled = TRUE`,

		// Missing tables for all features
		// System config table
		`CREATE TABLE IF NOT EXISTS system_config (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			category VARCHAR(50) NOT NULL,
			key VARCHAR(100) NOT NULL,
			value TEXT NOT NULL,
			is_secret BOOLEAN DEFAULT FALSE,
			updated_by UUID REFERENCES users(id),
			updated_at TIMESTAMP DEFAULT NOW(),
			UNIQUE(category, key)
		)`,

		// Topology snapshots
		`CREATE TABLE IF NOT EXISTS topology_snapshots (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			region VARCHAR(50) NOT NULL,
			snapshot_data JSONB NOT NULL,
			node_count INTEGER DEFAULT 0,
			created_at TIMESTAMP DEFAULT NOW()
		)`,

		// Services and topology
		`CREATE TABLE IF NOT EXISTS services (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			name VARCHAR(255) NOT NULL,
			display_name VARCHAR(255),
			type VARCHAR(50) NOT NULL,
			status VARCHAR(50) NOT NULL DEFAULT 'unknown',
			description TEXT,
			version VARCHAR(50),
			environment VARCHAR(50),
			owner VARCHAR(100),
			owner_email VARCHAR(255),
			repository TEXT,
			documentation TEXT,
			runbook_url TEXT,
			dashboard_url TEXT,
			tags JSONB DEFAULT '[]',
			labels JSONB DEFAULT '{}',
			metadata JSONB DEFAULT '{}',
			health_endpoint TEXT,
			sla JSONB,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW(),
			UNIQUE(name, environment)
		)`,

		`CREATE TABLE IF NOT EXISTS service_dependencies (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			from_service_id UUID REFERENCES services(id) ON DELETE CASCADE,
			to_service_id UUID REFERENCES services(id) ON DELETE CASCADE,
			dependency_type VARCHAR(50) NOT NULL DEFAULT 'soft',
			description TEXT,
			protocol VARCHAR(50),
			port INTEGER,
			endpoint TEXT,
			health_endpoint TEXT,
			retry_policy JSONB,
			timeout_seconds INTEGER DEFAULT 30,
			circuit_breaker BOOLEAN DEFAULT FALSE,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS service_status_history (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			service_id UUID REFERENCES services(id) ON DELETE CASCADE,
			status VARCHAR(50) NOT NULL,
			reason TEXT,
			created_at TIMESTAMP DEFAULT NOW()
		)`,

		// Incident timeline
		`CREATE TABLE IF NOT EXISTS incident_timeline (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			incident_id UUID REFERENCES incidents(id) ON DELETE CASCADE,
			user_id UUID REFERENCES users(id),
			event_type VARCHAR(50) NOT NULL,
			title VARCHAR(255),
			description TEXT,
			metadata JSONB,
			created_at TIMESTAMP DEFAULT NOW()
		)`,

		// Notification system
		`CREATE TABLE IF NOT EXISTS notification_channels (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			name VARCHAR(255) NOT NULL,
			type VARCHAR(50) NOT NULL,
			config JSONB NOT NULL,
			is_active BOOLEAN DEFAULT TRUE,
			created_by UUID REFERENCES users(id),
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS notification_rules (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			name VARCHAR(255) NOT NULL,
			conditions JSONB NOT NULL,
			channels TEXT[],
			is_active BOOLEAN DEFAULT TRUE,
			created_at TIMESTAMP DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS notification_log (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			channel_id UUID REFERENCES notification_channels(id),
			alert_id UUID REFERENCES alerts(id),
			incident_id UUID REFERENCES incidents(id),
			recipient VARCHAR(255),
			status VARCHAR(50) DEFAULT 'pending',
			error_message TEXT,
			sent_at TIMESTAMP DEFAULT NOW()
		)`,

		// OnCall system
		`CREATE TABLE IF NOT EXISTS oncall_shifts (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			schedule_id UUID,
			user_id UUID REFERENCES users(id) ON DELETE CASCADE,
			start_time TIMESTAMP NOT NULL,
			end_time TIMESTAMP NOT NULL,
			is_override BOOLEAN DEFAULT FALSE,
			created_at TIMESTAMP DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS slos (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			service VARCHAR(255) NOT NULL,
			target_availability DECIMAL(5,2) DEFAULT 99.9,
			target_latency_ms INTEGER DEFAULT 1000,
			target_error_rate DECIMAL(5,2) DEFAULT 1.0,
			measurement_window VARCHAR(50) DEFAULT '30d',
			is_active BOOLEAN DEFAULT TRUE,
			created_by UUID REFERENCES users(id),
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)`,

		// SRE tracking tables
		`CREATE TABLE IF NOT EXISTS change_requests (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			title VARCHAR(255) NOT NULL,
			description TEXT,
			type VARCHAR(50),
			risk VARCHAR(20),
			status VARCHAR(50) DEFAULT 'pending',
			services TEXT[],
			impacted_systems TEXT[],
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS cost_tracking (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			incident_id UUID REFERENCES incidents(id),
			downtime_minutes INTEGER,
			affected_users INTEGER,
			revenue_impact DECIMAL(12,2),
			created_at TIMESTAMP DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS toil_tracking (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			task VARCHAR(255) NOT NULL,
			frequency VARCHAR(50),
			time_per_execution INTEGER,
			total_time_per_month INTEGER,
			automation_potential INTEGER,
			created_at TIMESTAMP DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS post_mortems (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			incident_id UUID REFERENCES incidents(id),
			title VARCHAR(255) NOT NULL,
			summary TEXT,
			impact TEXT,
			root_cause TEXT,
			timeline JSONB,
			created_at TIMESTAMP DEFAULT NOW()
		)`,

		// Auth providers table
		`CREATE TABLE IF NOT EXISTS auth_providers (
			id TEXT PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			type VARCHAR(50) NOT NULL,
			provider VARCHAR(50) NOT NULL,
			enabled BOOLEAN DEFAULT TRUE,
			config JSONB NOT NULL,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)`,

		// User expertise for AI auto-assignment
		`CREATE TABLE IF NOT EXISTS user_expertise (
			user_id UUID REFERENCES users(id) ON DELETE CASCADE,
			expertise_area VARCHAR(100) NOT NULL,
			proficiency_level INTEGER DEFAULT 1,
			created_at TIMESTAMP DEFAULT NOW(),
			PRIMARY KEY (user_id, expertise_area)
		)`,

		// Add username to alert_comments
		`ALTER TABLE alert_comments ADD COLUMN IF NOT EXISTS username VARCHAR(255)`,

		// Indexes for all new tables
		`CREATE INDEX IF NOT EXISTS idx_incident_timeline_incident ON incident_timeline(incident_id)`,
		`CREATE INDEX IF NOT EXISTS idx_notification_log_alert ON notification_log(alert_id)`,
		`CREATE INDEX IF NOT EXISTS idx_oncall_shifts_user ON oncall_shifts(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_services_type ON services(type)`,
		`CREATE INDEX IF NOT EXISTS idx_services_status ON services(status)`,

		// === DAVIS AI CORRELATION SYSTEM TABLES ===
		// K8s cluster configurations table with all required fields
		`CREATE TABLE IF NOT EXISTS k8s_cluster_configs (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			name VARCHAR(255) NOT NULL UNIQUE,
			cluster_name VARCHAR(255),
			display_name VARCHAR(255),
			environment VARCHAR(100) NOT NULL DEFAULT 'production',
			region VARCHAR(100) NOT NULL DEFAULT 'default',
			api_server_url VARCHAR(500) NOT NULL,
			api_server VARCHAR(500), -- Alias for api_server_url
			service_account_token TEXT NOT NULL,
			ca_cert_data TEXT,
			namespace VARCHAR(100) DEFAULT 'default',
			enabled BOOLEAN DEFAULT TRUE,
			last_sync TIMESTAMP WITH TIME ZONE,
			sync_status VARCHAR(50) DEFAULT 'pending',
			sync_error TEXT,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			-- Additional fields needed by K8s topology handlers
			version VARCHAR(100) DEFAULT '1.0.0',
			node_count INTEGER DEFAULT 0,
			namespace_count INTEGER DEFAULT 0,
			pod_count INTEGER DEFAULT 0,
			service_count INTEGER DEFAULT 0,
			health_score DECIMAL(5,4) DEFAULT 1.0,
			status VARCHAR(50) DEFAULT 'unknown',
			kubeconfig TEXT,
			token TEXT,
			readonly_mode BOOLEAN DEFAULT TRUE,
			discovery_enabled BOOLEAN DEFAULT TRUE,
			labels JSONB DEFAULT '{}',
			metadata JSONB DEFAULT '{}',
			last_discovery TIMESTAMP WITH TIME ZONE
		)`,

		// Add missing columns to existing k8s_cluster_configs table
		`ALTER TABLE k8s_cluster_configs ADD COLUMN IF NOT EXISTS cluster_name VARCHAR(255)`,
		`ALTER TABLE k8s_cluster_configs ADD COLUMN IF NOT EXISTS display_name VARCHAR(255)`,
		`ALTER TABLE k8s_cluster_configs ADD COLUMN IF NOT EXISTS api_server VARCHAR(500)`,
		`ALTER TABLE k8s_cluster_configs ADD COLUMN IF NOT EXISTS version VARCHAR(100) DEFAULT '1.0.0'`,
		`ALTER TABLE k8s_cluster_configs ADD COLUMN IF NOT EXISTS node_count INTEGER DEFAULT 0`,
		`ALTER TABLE k8s_cluster_configs ADD COLUMN IF NOT EXISTS namespace_count INTEGER DEFAULT 0`,
		`ALTER TABLE k8s_cluster_configs ADD COLUMN IF NOT EXISTS pod_count INTEGER DEFAULT 0`,
		`ALTER TABLE k8s_cluster_configs ADD COLUMN IF NOT EXISTS service_count INTEGER DEFAULT 0`,
		`ALTER TABLE k8s_cluster_configs ADD COLUMN IF NOT EXISTS health_score DECIMAL(5,4) DEFAULT 1.0`,
		`ALTER TABLE k8s_cluster_configs ADD COLUMN IF NOT EXISTS status VARCHAR(50) DEFAULT 'unknown'`,
		`ALTER TABLE k8s_cluster_configs ADD COLUMN IF NOT EXISTS kubeconfig TEXT`,
		`ALTER TABLE k8s_cluster_configs ADD COLUMN IF NOT EXISTS token TEXT`,
		`ALTER TABLE k8s_cluster_configs ADD COLUMN IF NOT EXISTS readonly_mode BOOLEAN DEFAULT TRUE`,
		`ALTER TABLE k8s_cluster_configs ADD COLUMN IF NOT EXISTS discovery_enabled BOOLEAN DEFAULT TRUE`,
		`ALTER TABLE k8s_cluster_configs ADD COLUMN IF NOT EXISTS labels JSONB DEFAULT '{}'`,
		`ALTER TABLE k8s_cluster_configs ADD COLUMN IF NOT EXISTS metadata JSONB DEFAULT '{}'`,
		`ALTER TABLE k8s_cluster_configs ADD COLUMN IF NOT EXISTS last_discovery TIMESTAMP WITH TIME ZONE`,

		// Fix NULL values in existing records
		`UPDATE k8s_cluster_configs SET cluster_name = name WHERE cluster_name IS NULL`,
		`UPDATE k8s_cluster_configs SET display_name = COALESCE(display_name, cluster_name, name) WHERE display_name IS NULL`,
		`UPDATE k8s_cluster_configs SET api_server = COALESCE(api_server, api_server_url) WHERE api_server IS NULL`,

		// Add missing unique constraint
		`DO $$
		BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_constraint
				WHERE conname = 'k8s_cluster_configs_cluster_name_region_key'
			) THEN
				ALTER TABLE k8s_cluster_configs
				ADD CONSTRAINT k8s_cluster_configs_cluster_name_region_key
				UNIQUE (cluster_name, region);
			END IF;
		EXCEPTION WHEN duplicate_table THEN
			-- Constraint already exists, ignore
		END $$`,

		// Davis AI correlations table
		`CREATE TABLE IF NOT EXISTS davis_ai_correlations (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			alert_id UUID NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
			correlation_id VARCHAR(255) NOT NULL,
			correlation_type VARCHAR(100) NOT NULL,
			confidence_score DECIMAL(5,4) NOT NULL,
			correlation_result JSONB NOT NULL DEFAULT '{}',
			processing_time_ms BIGINT DEFAULT 0,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			UNIQUE(alert_id)
		)`,

		// Historical patterns table for learning engine
		`CREATE TABLE IF NOT EXISTS historical_patterns (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			pattern_id VARCHAR(255) NOT NULL,
			pattern_type VARCHAR(100) NOT NULL,
			alert_source VARCHAR(100) NOT NULL,
			alert_title TEXT,
			alert_description TEXT,
			frequency INTEGER DEFAULT 1,
			confidence DECIMAL(5,4) DEFAULT 0.0,
			typical_resolution TEXT,
			avg_resolution_time_seconds BIGINT DEFAULT 0,
			last_seen TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			UNIQUE(pattern_id, alert_source)
		)`,

		// Ollama configuration table
		`CREATE TABLE IF NOT EXISTS ollama_config (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			service_url VARCHAR(255) NOT NULL DEFAULT 'http://localhost:11434',
			model_name VARCHAR(100) NOT NULL DEFAULT 'llama2',
			enabled BOOLEAN DEFAULT TRUE,
			last_health_check TIMESTAMP WITH TIME ZONE,
			health_status VARCHAR(50) DEFAULT 'unknown',
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,

		// K8s topology snapshots table
		`CREATE TABLE IF NOT EXISTS k8s_topology_snapshots (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			cluster_name VARCHAR(255) NOT NULL,
			environment VARCHAR(100) NOT NULL,
			region VARCHAR(100) NOT NULL,
			topology_data JSONB NOT NULL,
			discovery_summary JSONB NOT NULL DEFAULT '{}',
			health_score DECIMAL(5,4) DEFAULT 0.0,
			total_nodes INTEGER DEFAULT 0,
			ready_nodes INTEGER DEFAULT 0,
			total_pods INTEGER DEFAULT 0,
			running_pods INTEGER DEFAULT 0,
			total_services INTEGER DEFAULT 0,
			discovered_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,

		// Add Davis AI correlation fields to existing tables
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS davis_correlation_id VARCHAR(255)`,
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS auto_created_incident_id UUID REFERENCES incidents(id) ON DELETE SET NULL`,
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS correlation_confidence DECIMAL(5,4) DEFAULT 0.0`,
		`ALTER TABLE alerts ADD COLUMN IF NOT EXISTS enhanced_fingerprint VARCHAR(32)`,

		// Add Davis AI fields to incidents table
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS auto_created BOOLEAN DEFAULT FALSE`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS correlation_type VARCHAR(100)`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS davis_ai_analysis JSONB DEFAULT '{}'`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS priority VARCHAR(50)`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS impact VARCHAR(50)`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS urgency VARCHAR(50)`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS team_id UUID`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS alert_ids JSONB DEFAULT '[]'`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS related_incident_ids JSONB DEFAULT '[]'`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS correlation_id VARCHAR(255)`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS affected_services JSONB DEFAULT '[]'`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS workflow_executions JSONB DEFAULT '[]'`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS similar_incidents JSONB DEFAULT '[]'`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS auto_assignment JSONB DEFAULT NULL`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS escalation_policy JSONB DEFAULT NULL`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS business_impact JSONB DEFAULT NULL`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS communication_plan JSONB DEFAULT NULL`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS impact_analysis JSONB DEFAULT NULL`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS created_by UUID REFERENCES users(id)`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS ai_insights JSONB DEFAULT NULL`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS ai_root_cause TEXT`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS ai_recommendations JSONB DEFAULT '[]'`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS resolution_notes TEXT`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS post_mortem_url TEXT`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS detected_at TIMESTAMP`,

		// Indexes for Davis AI correlation system
		`CREATE INDEX IF NOT EXISTS idx_davis_correlations_alert_id ON davis_ai_correlations(alert_id)`,
		`CREATE INDEX IF NOT EXISTS idx_davis_correlations_correlation_id ON davis_ai_correlations(correlation_id)`,
		`CREATE INDEX IF NOT EXISTS idx_davis_correlations_confidence ON davis_ai_correlations(confidence_score DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_davis_correlations_created_at ON davis_ai_correlations(created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_historical_patterns_source ON historical_patterns(alert_source)`,
		`CREATE INDEX IF NOT EXISTS idx_historical_patterns_type ON historical_patterns(pattern_type)`,
		`CREATE INDEX IF NOT EXISTS idx_historical_patterns_confidence ON historical_patterns(confidence DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_historical_patterns_frequency ON historical_patterns(frequency DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_k8s_cluster_configs_name ON k8s_cluster_configs(name)`,
		`CREATE INDEX IF NOT EXISTS idx_k8s_cluster_configs_enabled ON k8s_cluster_configs(enabled) WHERE enabled = TRUE`,
		`CREATE INDEX IF NOT EXISTS idx_k8s_cluster_configs_sync_status ON k8s_cluster_configs(sync_status)`,
		`CREATE INDEX IF NOT EXISTS idx_k8s_topology_snapshots_cluster ON k8s_topology_snapshots(cluster_name)`,
		`CREATE INDEX IF NOT EXISTS idx_k8s_topology_snapshots_discovered_at ON k8s_topology_snapshots(discovered_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_davis_correlation ON alerts(davis_correlation_id) WHERE davis_correlation_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_auto_incident ON alerts(auto_created_incident_id) WHERE auto_created_incident_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_correlation_confidence ON alerts(correlation_confidence DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_enhanced_fingerprint ON alerts(enhanced_fingerprint) WHERE enhanced_fingerprint IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_incidents_auto_created ON incidents(auto_created) WHERE auto_created = TRUE`,
		`CREATE INDEX IF NOT EXISTS idx_incidents_correlation_type ON incidents(correlation_type) WHERE correlation_type IS NOT NULL`,

		// Insert default Ollama configuration
		`INSERT INTO ollama_config (service_url, model_name, enabled)
		VALUES ('http://localhost:11434', 'llama2', TRUE)
		ON CONFLICT DO NOTHING`,

		// Insert sample historical patterns for testing
		`INSERT INTO historical_patterns (pattern_id, pattern_type, alert_source, alert_title, frequency, confidence, typical_resolution, avg_resolution_time_seconds) VALUES
		('db-connection-pattern', 'recurring', 'dynatrace', 'Database connection timeout', 15, 0.85, 'Restart database connection pool', 900),
		('memory-leak-pattern', 'cascade', 'prometheus', 'High memory usage', 8, 0.78, 'Restart application service', 1800),
		('network-timeout-pattern', 'seasonal', 'grafana', 'Network timeout detected', 12, 0.72, 'Check network infrastructure', 600),
		('disk-space-pattern', 'recurring', 'zabbix', 'Disk space critical', 20, 0.90, 'Clean up temporary files', 300)
		ON CONFLICT (pattern_id, alert_source) DO NOTHING`,

		// Create trigger functions for updating timestamps
		`CREATE OR REPLACE FUNCTION update_davis_updated_at_column()
		RETURNS TRIGGER AS $$
		BEGIN
			NEW.updated_at = NOW();
			RETURN NEW;
		END;
		$$ language 'plpgsql'`,

		// Create triggers for Davis AI tables (using DO blocks for PostgreSQL compatibility)
		`DO $$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_davis_ai_correlations_updated_at') THEN
				CREATE TRIGGER update_davis_ai_correlations_updated_at
					BEFORE UPDATE ON davis_ai_correlations
					FOR EACH ROW EXECUTE FUNCTION update_davis_updated_at_column();
			END IF;
		END $$`,

		`DO $$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_historical_patterns_updated_at') THEN
				CREATE TRIGGER update_historical_patterns_updated_at
					BEFORE UPDATE ON historical_patterns
					FOR EACH ROW EXECUTE FUNCTION update_davis_updated_at_column();
			END IF;
		END $$`,

		`DO $$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_ollama_config_updated_at') THEN
				CREATE TRIGGER update_ollama_config_updated_at
					BEFORE UPDATE ON ollama_config
					FOR EACH ROW EXECUTE FUNCTION update_davis_updated_at_column();
			END IF;
		END $$`,

		`DO $$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'update_k8s_cluster_configs_updated_at') THEN
				CREATE TRIGGER update_k8s_cluster_configs_updated_at
					BEFORE UPDATE ON k8s_cluster_configs
					FOR EACH ROW EXECUTE FUNCTION update_davis_updated_at_column();
			END IF;
		END $$`,

		// Generic OAuth 2.0 provider configuration
		`CREATE TABLE IF NOT EXISTS oauth2_providers (
			id                  UUID         PRIMARY KEY DEFAULT uuid_generate_v4(),
			name                VARCHAR(64)  UNIQUE NOT NULL,
			display_name        VARCHAR(128) NOT NULL,
			client_id           VARCHAR(256) NOT NULL,
			client_secret       VARCHAR(512) NOT NULL,
			auth_url            VARCHAR(512) NOT NULL,
			token_url           VARCHAR(512) NOT NULL,
			userinfo_url        VARCHAR(512) NOT NULL DEFAULT '',
			scopes              TEXT[]       NOT NULL DEFAULT '{}',
			icon_url            VARCHAR(512) NOT NULL DEFAULT '',
			enabled             BOOLEAN      NOT NULL DEFAULT true,
			auto_provision      BOOLEAN      NOT NULL DEFAULT true,
			default_role        VARCHAR(64)  NOT NULL DEFAULT 'viewer',
			email_domain_filter VARCHAR(256) NOT NULL DEFAULT '',
			created_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
			updated_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW()
		)`,

		`CREATE INDEX IF NOT EXISTS idx_oauth2_providers_enabled ON oauth2_providers(enabled) WHERE enabled = true`,

		`CREATE OR REPLACE FUNCTION update_oauth2_providers_updated_at()
		RETURNS TRIGGER AS $$
		BEGIN
			NEW.updated_at = NOW();
			RETURN NEW;
		END;
		$$ language 'plpgsql'`,

		`DO $$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_oauth2_providers_updated_at') THEN
				CREATE TRIGGER trg_oauth2_providers_updated_at
					BEFORE UPDATE ON oauth2_providers
					FOR EACH ROW EXECUTE FUNCTION update_oauth2_providers_updated_at();
			END IF;
		END $$`,

		// Incident auto-numbering sequence
		`CREATE SEQUENCE IF NOT EXISTS incident_number_seq START 1000`,
		`ALTER TABLE incidents ALTER COLUMN incident_number SET DEFAULT nextval('incident_number_seq')::TEXT`,

		// Add recommended_action to alert_correlations (or correlation_results if view already exists)
		`DO $$
		BEGIN
			IF EXISTS (SELECT 1 FROM pg_tables WHERE schemaname='public' AND tablename='alert_correlations') THEN
				ALTER TABLE alert_correlations ADD COLUMN IF NOT EXISTS recommended_action VARCHAR(100) DEFAULT 'monitor';
			END IF;
			IF EXISTS (SELECT 1 FROM pg_tables WHERE schemaname='public' AND tablename='correlation_results') THEN
				ALTER TABLE correlation_results ADD COLUMN IF NOT EXISTS recommended_action VARCHAR(100) DEFAULT 'monitor';
			END IF;
		END $$`,

		// Deduplication rules table
		`CREATE TABLE IF NOT EXISTS deduplication_rules (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			name VARCHAR(255) NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			field VARCHAR(100) NOT NULL,
			match_type VARCHAR(50) NOT NULL DEFAULT 'exact',
			pattern TEXT NOT NULL DEFAULT '',
			time_window_seconds INTEGER DEFAULT 300,
			enabled BOOLEAN DEFAULT TRUE,
			priority INTEGER DEFAULT 0,
			dedup_count INTEGER DEFAULT 0,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW(),
			UNIQUE(name)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_dedup_rules_enabled ON deduplication_rules(enabled) WHERE enabled = TRUE`,
		`CREATE INDEX IF NOT EXISTS idx_dedup_rules_field ON deduplication_rules(field)`,

		// Correlation feedback loop: operator verdicts adaptive weight recalibration
		`CREATE TABLE IF NOT EXISTS correlation_feedback (
			id                UUID         PRIMARY KEY DEFAULT uuid_generate_v4(),
			alert_id          UUID         NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
			incident_id       UUID         REFERENCES incidents(id) ON DELETE SET NULL,
			correlation_id    VARCHAR(255) NOT NULL DEFAULT '',
			feedback_type     VARCHAR(50)  NOT NULL,
			dominant_strategy VARCHAR(100) NOT NULL DEFAULT '',
			strategy_scores   JSONB        NOT NULL DEFAULT '{}',
			operator_id       UUID         REFERENCES users(id) ON DELETE SET NULL,
			notes             TEXT         NOT NULL DEFAULT '',
			created_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_corr_feedback_alert_id       ON correlation_feedback(alert_id)`,
		`CREATE INDEX IF NOT EXISTS idx_corr_feedback_incident_id     ON correlation_feedback(incident_id) WHERE incident_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_corr_feedback_type            ON correlation_feedback(feedback_type)`,
		`CREATE INDEX IF NOT EXISTS idx_corr_feedback_strategy        ON correlation_feedback(dominant_strategy)`,
		`CREATE INDEX IF NOT EXISTS idx_corr_feedback_created_at      ON correlation_feedback(created_at DESC)`,

		// Weight history audit log
		`CREATE TABLE IF NOT EXISTS correlation_weight_history (
			id         UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
			weights    JSONB       NOT NULL,
			reason     TEXT        NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_corr_weight_history_created_at ON correlation_weight_history(created_at DESC)`,

		// Pipeline correlation results: persists every correlation decision for AIOps visibility
		`CREATE TABLE IF NOT EXISTS pipeline_correlation_results (
			id                 UUID         PRIMARY KEY DEFAULT uuid_generate_v4(),
			alert_id           UUID         REFERENCES alerts(id) ON DELETE CASCADE,
			incident_id        UUID         REFERENCES incidents(id) ON DELETE SET NULL,
			alert_title        VARCHAR(500) NOT NULL DEFAULT '',
			alert_source       VARCHAR(100) NOT NULL DEFAULT '',
			alert_severity     VARCHAR(50)  NOT NULL DEFAULT '',
			decision           VARCHAR(50)  NOT NULL,
			final_score        FLOAT        NOT NULL DEFAULT 0,
			dominant_strategy  VARCHAR(100) NOT NULL DEFAULT '',
			semantic_score     FLOAT        NOT NULL DEFAULT 0,
			temporal_score     FLOAT        NOT NULL DEFAULT 0,
			topology_score     FLOAT        NOT NULL DEFAULT 0,
			rules_score        FLOAT        NOT NULL DEFAULT 0,
			reasoning          TEXT         NOT NULL DEFAULT '',
			ai_root_cause      TEXT         NOT NULL DEFAULT '',
			matched_node_label VARCHAR(255) NOT NULL DEFAULT '',
			root_cause_label   VARCHAR(255) NOT NULL DEFAULT '',
			elapsed_ms         BIGINT       NOT NULL DEFAULT 0,
			processed_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_pcr_alert_id       ON pipeline_correlation_results(alert_id)`,
		`CREATE INDEX IF NOT EXISTS idx_pcr_incident_id    ON pipeline_correlation_results(incident_id) WHERE incident_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_pcr_decision       ON pipeline_correlation_results(decision)`,
		`CREATE INDEX IF NOT EXISTS idx_pcr_source         ON pipeline_correlation_results(alert_source)`,
		`CREATE INDEX IF NOT EXISTS idx_pcr_processed_at   ON pipeline_correlation_results(processed_at DESC)`,

		// Add columns that InternalCreateIncident/GetIncident reference but were
		// never included in earlier migrations — safe to run repeatedly.
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS timeline   JSONB        DEFAULT '[]'::jsonb`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS closed_at  TIMESTAMP`,

		// DS-LDAP group role mappings (configurable via Admin UI without restart)
		`CREATE TABLE IF NOT EXISTS ldap_group_role_mappings (
			id          UUID         PRIMARY KEY DEFAULT uuid_generate_v4(),
			ldap_group  VARCHAR(255) NOT NULL UNIQUE,
			role_id     UUID         NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
			created_by  UUID         REFERENCES users(id) ON DELETE SET NULL,
			created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
			updated_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ldap_group_mappings_group   ON ldap_group_role_mappings(ldap_group)`,
		`CREATE INDEX IF NOT EXISTS idx_ldap_group_mappings_role_id ON ldap_group_role_mappings(role_id)`,

		// CloudStack configuration table
		`CREATE TABLE IF NOT EXISTS cloudstack_configs (
			id              UUID         PRIMARY KEY DEFAULT uuid_generate_v4(),
			region_id       UUID,
			name            VARCHAR(255) NOT NULL UNIQUE,
			api_url         VARCHAR(500) NOT NULL,
			api_key         TEXT         NOT NULL,
			secret_key      TEXT         NOT NULL,
			zone_id         VARCHAR(255),
			enabled         BOOLEAN      NOT NULL DEFAULT TRUE,
			sync_status     VARCHAR(50)  NOT NULL DEFAULT 'pending',
			vm_count        INTEGER      NOT NULL DEFAULT 0,
			last_sync       TIMESTAMPTZ,
			created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
			updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cloudstack_configs_enabled ON cloudstack_configs(enabled)`,

		// LLM / AI model configuration table (used by Admin AI & LLM section)
		`CREATE TABLE IF NOT EXISTS llm_configs (
			id                   UUID         PRIMARY KEY DEFAULT uuid_generate_v4(),
			name                 VARCHAR(255) NOT NULL UNIQUE,
			provider             VARCHAR(100) NOT NULL DEFAULT 'openai',
			model_name           VARCHAR(255) NOT NULL,
			endpoint_url         VARCHAR(500) NOT NULL DEFAULT '',
			api_key              TEXT         NOT NULL DEFAULT '',
			max_tokens           INTEGER      NOT NULL DEFAULT 4096,
			temperature          DECIMAL(4,3) NOT NULL DEFAULT 0.7,
			enabled              BOOLEAN      NOT NULL DEFAULT TRUE,
			use_for_rca          BOOLEAN      NOT NULL DEFAULT FALSE,
			use_for_correlation  BOOLEAN      NOT NULL DEFAULT FALSE,
			use_for_remediation  BOOLEAN      NOT NULL DEFAULT FALSE,
			use_for_summarization BOOLEAN     NOT NULL DEFAULT FALSE,
			system_prompt        TEXT,
			created_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
			updated_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW()
		)`,

		// Alert sources table (used by Admin Alert Sources section)
		`CREATE TABLE IF NOT EXISTS alert_sources (
			id                       UUID         PRIMARY KEY DEFAULT uuid_generate_v4(),
			name                     VARCHAR(255) NOT NULL UNIQUE,
			source_type              VARCHAR(100) NOT NULL,
			display_name             VARCHAR(255),
			endpoint_url             VARCHAR(500),
			api_key                  TEXT,
			enabled                  BOOLEAN      NOT NULL DEFAULT TRUE,
			polling_interval_seconds INTEGER      NOT NULL DEFAULT 60,
			last_poll_at             TIMESTAMPTZ,
			last_poll_status         VARCHAR(50)  NOT NULL DEFAULT 'pending',
			alerts_received_total    INTEGER      NOT NULL DEFAULT 0,
			config                   JSONB        NOT NULL DEFAULT '{}',
			created_at               TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
			updated_at               TIMESTAMPTZ  NOT NULL DEFAULT NOW()
		)`,

		// Infrastructure regions table (used by Admin Infrastructure)
		`CREATE TABLE IF NOT EXISTS infra_regions (
			id           UUID         PRIMARY KEY DEFAULT uuid_generate_v4(),
			name         VARCHAR(255) NOT NULL UNIQUE,
			display_name VARCHAR(255),
			location     VARCHAR(255),
			region_type  VARCHAR(50)  NOT NULL DEFAULT 'datacenter',
			bm_count     INTEGER      NOT NULL DEFAULT 0,
			enabled      BOOLEAN      NOT NULL DEFAULT TRUE,
			created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
			updated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
		)`,

		// NetApp configuration table (used by Admin Infrastructure)
		`CREATE TABLE IF NOT EXISTS netapp_configs (
			id          UUID         PRIMARY KEY DEFAULT uuid_generate_v4(),
			name        VARCHAR(255) NOT NULL UNIQUE,
			api_url     VARCHAR(500) NOT NULL,
			api_key     TEXT         NOT NULL DEFAULT '',
			username    VARCHAR(255),
			enabled     BOOLEAN      NOT NULL DEFAULT TRUE,
			sync_status VARCHAR(50)  NOT NULL DEFAULT 'pending',
			last_sync   TIMESTAMPTZ,
			created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
			updated_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
		)`,

		// Correlation engine configuration table
		`CREATE TABLE IF NOT EXISTS correlation_engine_config (
			id          UUID         PRIMARY KEY DEFAULT uuid_generate_v4(),
			config_key  VARCHAR(255) NOT NULL UNIQUE,
			config_value TEXT        NOT NULL,
			description TEXT,
			updated_by  UUID         REFERENCES users(id) ON DELETE SET NULL,
			updated_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
		)`,
		`INSERT INTO correlation_engine_config (config_key, config_value, description)
		 VALUES
		   ('time_window_seconds',  '300',    'Correlation time window in seconds'),
		   ('max_group_size',       '50',     'Maximum alerts per correlation group'),
		   ('similarity_threshold', '0.75',   'Minimum similarity score for correlation'),
		   ('enable_ml_correlation','true',   'Enable ML-based correlation engine'),
		   ('enable_rule_correlation','true', 'Enable rule-based correlation engine')
		 ON CONFLICT (config_key) DO NOTHING`,

		// Webhook API keys table — stores SHA-256 hashes of issued keys
		`CREATE TABLE IF NOT EXISTS webhook_api_keys (
			id          UUID         PRIMARY KEY DEFAULT uuid_generate_v4(),
			name        VARCHAR(255) NOT NULL,
			key_hash    VARCHAR(64)  NOT NULL UNIQUE,
			user_id     UUID         REFERENCES users(id) ON DELETE CASCADE,
			enabled     BOOLEAN      NOT NULL DEFAULT TRUE,
			last_used_at TIMESTAMPTZ,
			created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_webhook_api_keys_hash ON webhook_api_keys(key_hash) WHERE enabled = TRUE`,

		// V2 Correlation Engine schema additions 

		// Extend pipeline_correlation_results with explainability and ontology columns
		`ALTER TABLE pipeline_correlation_results ADD COLUMN IF NOT EXISTS explanation_json JSONB`,
		`ALTER TABLE pipeline_correlation_results ADD COLUMN IF NOT EXISTS domain TEXT`,
		`ALTER TABLE pipeline_correlation_results ADD COLUMN IF NOT EXISTS ontology_class TEXT`,
		`ALTER TABLE pipeline_correlation_results ADD COLUMN IF NOT EXISTS rca_hypotheses JSONB`,
		`ALTER TABLE pipeline_correlation_results ADD COLUMN IF NOT EXISTS topo_root_entity TEXT`,
		`ALTER TABLE pipeline_correlation_results ADD COLUMN IF NOT EXISTS blast_radius_count INT`,

		// Extend incidents with V2 evolution and causal lineage columns
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS rca_hypotheses JSONB`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS evolution_generation INT DEFAULT 0`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS merge_source_ids UUID[]`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS ontology_domain TEXT`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS topo_root_entity_id TEXT`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS causal_chain JSONB`,

		// Adaptive learning feature store
		`CREATE TABLE IF NOT EXISTS correlation_feature_store (
			feature_key TEXT PRIMARY KEY,
			domain      TEXT NOT NULL,
			source      TEXT NOT NULL DEFAULT '',
			cluster     TEXT NOT NULL DEFAULT '',
			weights_json JSONB NOT NULL,
			sample_size INT NOT NULL DEFAULT 0,
			accuracy    FLOAT NOT NULL DEFAULT 0,
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,

		// Incident causal lineage — tracks merge/split/reparent history
		`CREATE TABLE IF NOT EXISTS incident_causal_lineage (
			id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			incident_id      UUID NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
			parent_id        UUID REFERENCES incidents(id),
			lineage_type     TEXT NOT NULL,
			root_entity_id   TEXT,
			root_entity_label TEXT,
			confidence       FLOAT,
			reasoning        TEXT,
			metadata         JSONB,
			created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_icl_incident_id ON incident_causal_lineage(incident_id)`,
		`CREATE INDEX IF NOT EXISTS idx_icl_root_entity ON incident_causal_lineage(root_entity_id)`,

		// Infrastructure dependency edges (PostgreSQL-side, synced to Neo4j)
		`CREATE TABLE IF NOT EXISTS infrastructure_dependency_edges (
			id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			source_id      TEXT NOT NULL,
			target_id      TEXT NOT NULL,
			edge_type      TEXT NOT NULL,
			weight         FLOAT NOT NULL DEFAULT 0.85,
			domain         TEXT,
			last_confirmed TIMESTAMPTZ,
			confidence     FLOAT NOT NULL DEFAULT 0.80,
			created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(source_id, target_id, edge_type)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ide_source ON infrastructure_dependency_edges(source_id)`,
		`CREATE INDEX IF NOT EXISTS idx_ide_target ON infrastructure_dependency_edges(target_id)`,
		`CREATE INDEX IF NOT EXISTS idx_ide_edge_type ON infrastructure_dependency_edges(edge_type)`,

		// Causal lineage tables (CACIE engine output store) 

		// causal_relationships: persistent entity-to-entity causal link store.
		// Upsert-friendly via unique index on (source, target, type, edge).
		`CREATE TABLE IF NOT EXISTS causal_relationships (
			id                 UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
			source_entity_id   TEXT        NOT NULL,
			target_entity_id   TEXT        NOT NULL,
			relationship_type  TEXT        NOT NULL CHECK (relationship_type IN ('root_cause','downstream','sibling','correlated')),
			edge_type          TEXT        NOT NULL DEFAULT 'HOSTS',
			confidence_score   FLOAT       NOT NULL CHECK (confidence_score BETWEEN 0 AND 1),
			propagation_score  FLOAT       CHECK (propagation_score BETWEEN 0 AND 1),
			incident_id        UUID        REFERENCES incidents(id) ON DELETE SET NULL,
			alert_id           UUID        REFERENCES alerts(id)   ON DELETE SET NULL,
			domain             TEXT,
			infra_level_source INT,
			infra_level_target INT,
			hop_index          INT         NOT NULL DEFAULT 0,
			first_seen_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			last_confirmed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			observation_count  INT         NOT NULL DEFAULT 1
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cr_source   ON causal_relationships(source_entity_id)`,
		`CREATE INDEX IF NOT EXISTS idx_cr_target   ON causal_relationships(target_entity_id)`,
		`CREATE INDEX IF NOT EXISTS idx_cr_incident ON causal_relationships(incident_id) WHERE incident_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_cr_type     ON causal_relationships(relationship_type)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_cr_unique_edge
		    ON causal_relationships(source_entity_id, target_entity_id, relationship_type, edge_type)`,

		// propagation_paths: records the full ordered rootleaf causal chain per incident.
		`CREATE TABLE IF NOT EXISTS propagation_paths (
			id                       UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
			incident_id              UUID        REFERENCES incidents(id) ON DELETE CASCADE,
			root_entity_id           TEXT        NOT NULL,
			leaf_entity_id           TEXT        NOT NULL,
			path_entities            TEXT[]      NOT NULL,
			path_edge_types          TEXT[]      NOT NULL DEFAULT '{}',
			total_propagation_score  FLOAT       NOT NULL,
			path_length              INT         NOT NULL,
			domain                   TEXT,
			computed_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_pp_incident ON propagation_paths(incident_id)`,
		`CREATE INDEX IF NOT EXISTS idx_pp_root     ON propagation_paths(root_entity_id)`,

		// incident_causal_graphs: authoritative causal snapshot per incident (survives Redis TTL).
		`CREATE TABLE IF NOT EXISTS incident_causal_graphs (
			incident_id          UUID        PRIMARY KEY REFERENCES incidents(id) ON DELETE CASCADE,
			root_entity_id       TEXT,
			root_entity_label    TEXT,
			root_entity_type     TEXT,
			root_infra_level     INT,
			root_confidence      FLOAT,
			domain               TEXT,
			blast_radius         JSONB       NOT NULL DEFAULT '[]',
			hypotheses           JSONB       NOT NULL DEFAULT '[]',
			causal_chain         JSONB       NOT NULL DEFAULT '[]',
			propagation_map      JSONB       NOT NULL DEFAULT '{}',
			suppressed_entities  TEXT[]      NOT NULL DEFAULT '{}',
			reasoning            TEXT[]      NOT NULL DEFAULT '{}',
			engine_version       TEXT        NOT NULL DEFAULT 'v2',
			computed_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_icg_root_entity ON incident_causal_graphs(root_entity_id) WHERE root_entity_id IS NOT NULL`,

		// causal_pattern_templates: knowledge base of known failure cascade patterns.
		`CREATE TABLE IF NOT EXISTS causal_pattern_templates (
			id                   UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
			name                 TEXT        NOT NULL UNIQUE,
			description          TEXT,
			trigger_domain       TEXT        NOT NULL,
			trigger_entity_types TEXT[]      NOT NULL,
			cascade_pattern      JSONB       NOT NULL,
			confidence_boost     FLOAT       NOT NULL DEFAULT 0.10 CHECK (confidence_boost BETWEEN 0 AND 0.50),
			observation_count    INT         NOT NULL DEFAULT 0,
			last_matched_at      TIMESTAMPTZ,
			is_active            BOOLEAN     NOT NULL DEFAULT true,
			created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cpt_domain ON causal_pattern_templates(trigger_domain) WHERE is_active = true`,

		// Seed well-known cascade patterns
		`INSERT INTO causal_pattern_templates (name, description, trigger_domain, trigger_entity_types, cascade_pattern, confidence_boost)
		 VALUES
		   ('bm_node_pod_cascade','Bare-metal failure K8s node not ready pod evictions','compute',
		    ARRAY['bare_metal','host'],
		    '{"cascade":[{"from":"bare_metal","to":"k8s_node","edge":"HOSTS","weight":0.95},{"from":"k8s_node","to":"k8s_pod","edge":"RUNS_ON","weight":0.90}]}',0.15),
		   ('kvm_vm_node_cascade','KVM hypervisor overload VM degradation K8s node pressure','compute',
		    ARRAY['kvm','hypervisor'],
		    '{"cascade":[{"from":"kvm","to":"cloudstack_vm","edge":"HOSTS","weight":0.92},{"from":"cloudstack_vm","to":"k8s_node","edge":"HOSTS","weight":0.90}]}',0.13),
		   ('storage_iops_app_cascade','NetApp IOPS saturation pod IO latency application timeouts','storage',
		    ARRAY['netapp_aggregate','netapp_node','netapp_svm'],
		    '{"cascade":[{"from":"netapp_aggregate","to":"k8s_pod","edge":"MOUNTS","weight":0.88},{"from":"k8s_pod","to":"cloud_application","edge":"RUNS_ON","weight":0.82}]}',0.12),
		   ('network_saturation_cascade','Network link saturation host unreachable K8s node pressure','network',
		    ARRAY['network_interface','host'],
		    '{"cascade":[{"from":"network_interface","to":"host","edge":"MEMBER_OF","weight":0.85},{"from":"host","to":"k8s_node","edge":"HOSTS","weight":0.90}]}',0.10),
		   ('cluster_control_plane_cascade','K8s control plane failure all workloads degraded','kubernetes',
		    ARRAY['k8s_cluster','kubernetes_cluster'],
		    '{"cascade":[{"from":"k8s_cluster","to":"k8s_node","edge":"MEMBER_OF","weight":0.95},{"from":"k8s_node","to":"k8s_pod","edge":"RUNS_ON","weight":0.92}]}',0.14)
		 ON CONFLICT (name) DO NOTHING`,

		// Extend incidents with blast-radius tracking and source column
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS recurrence_count     INT     NOT NULL DEFAULT 0`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS source               TEXT`,
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS suppressed_entity_ids TEXT[] NOT NULL DEFAULT '{}'`,

		// Optimistic locking: version counter for idempotent concurrent evolution engine updates.
		// CAS pattern: UPDATE incidents SET ..., version = version+1 WHERE id=$1 AND version=$2
		`ALTER TABLE incidents ADD COLUMN IF NOT EXISTS version INT NOT NULL DEFAULT 0`,

		// Investigation DAG persistence: stores generated playbook steps per incident.
		`CREATE TABLE IF NOT EXISTS incident_investigation_dags (
			id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
			incident_id    UUID        NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
			domain         TEXT        NOT NULL DEFAULT 'unknown',
			root_entity    TEXT        NOT NULL DEFAULT '',
			steps          JSONB       NOT NULL DEFAULT '[]',
			generated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE (incident_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_investigation_dags_incident ON incident_investigation_dags(incident_id)`,

		// A1/A3: Deduplicate existing (source, source_id) rows before creating the unique index.
		// Keeps the most-recent open/acknowledged alert per pair; deletes true duplicates only.
		`DELETE FROM alerts a1
		 USING alerts a2
		 WHERE a1.source   = a2.source
		   AND a1.source_id = a2.source_id
		   AND a1.source_id IS NOT NULL
		   AND a1.source_id != ''
		   AND a1.id != a2.id
		   AND a1.created_at < a2.created_at`,

		// Unique partial index — only covers rows with a non-empty source_id (webhook alerts).
		// Manual alerts (source_id = '' or NULL) are not constrained.
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_alerts_source_source_id
		 ON alerts(source, source_id)
		 WHERE source_id IS NOT NULL AND source_id != ''`,

		// topology_snapshots: enforce one row per region so SaveInfraTopologySnapshot
		// can UPSERT instead of INSERT, capping the table at (number of regions) rows
		// instead of growing ~1.4 MB per write indefinitely.
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_topology_snapshots_region
		 ON topology_snapshots(region)`,

		// Workflow engine schema alignment 
		// The workflow engine expects steps/metadata/version/tags columns that
		// were absent from the original schema (which only had workflow_yaml).
		`ALTER TABLE workflows ALTER COLUMN workflow_yaml DROP NOT NULL`,
		`ALTER TABLE workflows ADD COLUMN IF NOT EXISTS steps    JSONB NOT NULL DEFAULT '[]'`,
		`ALTER TABLE workflows ADD COLUMN IF NOT EXISTS metadata JSONB DEFAULT '{}'`,
		`ALTER TABLE workflows ADD COLUMN IF NOT EXISTS version  INT   NOT NULL DEFAULT 1`,
		`ALTER TABLE workflows ADD COLUMN IF NOT EXISTS tags     JSONB DEFAULT '[]'`,

		// workflow_executions: engine stores rich execution state in these columns.
		`ALTER TABLE workflow_executions ADD COLUMN IF NOT EXISTS trigger_event JSONB`,
		`ALTER TABLE workflow_executions ADD COLUMN IF NOT EXISTS context       JSONB`,
		`ALTER TABLE workflow_executions ADD COLUMN IF NOT EXISTS step_results  JSONB`,
		`ALTER TABLE workflow_executions ADD COLUMN IF NOT EXISTS error         TEXT`,
		`ALTER TABLE workflow_executions ADD COLUMN IF NOT EXISTS logs          JSONB`,

		// investigation_dags: operator step-progress tracking.
		// step_states is a JSONB map of {stepID: "pending"|"in_progress"|"done"|"skipped"}.
		// Added after initial table creation — IF NOT EXISTS guards re-runs.
		`ALTER TABLE incident_investigation_dags
		 ADD COLUMN IF NOT EXISTS step_states JSONB NOT NULL DEFAULT '{}'`,

		// blast_radius_details: structured topology blast radius from RecursiveTopoRCA.
		// Stores entity_id, label, entity_type, infra_level, cluster, namespace, propagation_score
		// for each node so the incident page can render the BM→VM→K8s→pod hierarchy.
		`ALTER TABLE incidents
		 ADD COLUMN IF NOT EXISTS blast_radius_details JSONB`,

		// rca_decisions: persistent audit trail for ranked RCA hypotheses per incident.
		// Replaces ephemeral Redis state for incident investigation and postmortem review.
		`CREATE TABLE IF NOT EXISTS rca_decisions (
			id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
			incident_id         UUID        NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
			root_entity_id      TEXT        NOT NULL DEFAULT '',
			root_entity_label   TEXT        NOT NULL DEFAULT '',
			root_entity_type    TEXT        NOT NULL DEFAULT '',
			confidence          FLOAT       NOT NULL DEFAULT 0,
			confidence_band     TEXT        NOT NULL DEFAULT 'VERY_LOW',
			hypotheses          JSONB       NOT NULL DEFAULT '[]',
			reasoning           TEXT[]      NOT NULL DEFAULT '{}',
			evidence_sources    TEXT[]      NOT NULL DEFAULT '{}',
			created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			operator_verdict    TEXT,
			actual_root_cause   TEXT,
			verdict_at          TIMESTAMPTZ,
			UNIQUE (incident_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_rca_decisions_incident
		 ON rca_decisions(incident_id)`,
		`CREATE INDEX IF NOT EXISTS idx_rca_decisions_created
		 ON rca_decisions(created_at DESC)`,
	}

	for i, migration := range migrations {
		log.Printf("  Running migration %d/%d...", i+1, len(migrations))
		if _, err := db.ExecContext(ctx, migration); err != nil {
			return fmt.Errorf("migration %d failed: %v", i+1, err)
		}
	}

	log.Println("Schema migrations complete")

	// Insert default data
	if err := insertDefaultData(db); err != nil {
		return fmt.Errorf("failed to insert default data: %v", err)
	}

	log.Println("Database initialization complete")
	return nil
}

func insertDefaultData(db *sql.DB) error {
	ctx := context.Background()

	// Insert admin role
	var adminRoleID string
	err := db.QueryRowContext(ctx, `
		INSERT INTO roles (name, description, is_system_role)
		VALUES ('admin', 'Full system access', true)
		ON CONFLICT (name) DO UPDATE SET description = EXCLUDED.description
		RETURNING id
	`).Scan(&adminRoleID)

	if err != nil {
		return err
	}

	log.Printf("  Admin role ID: %s", adminRoleID)

	// Insert basic permissions - ALL permissions for complete admin access
	permissions := []struct {
		name, resource, action string
	}{
		// User management
		{"users.view", "users", "view"},
		{"users.create", "users", "create"},
		{"users.update", "users", "update"},
		{"users.delete", "users", "delete"},
		// Role management
		{"roles.view", "roles", "view"},
		{"roles.create", "roles", "create"},
		{"roles.update", "roles", "update"},
		{"roles.delete", "roles", "delete"},
		// Alert operations
		{"alerts.view", "alerts", "view"},
		{"alerts.create", "alerts", "create"},
		{"alerts.update", "alerts", "update"},
		{"alerts.assign", "alerts", "assign"},
		{"alerts.resolve", "alerts", "resolve"},
		{"alerts.delete", "alerts", "delete"},
		{"alerts.acknowledge", "alerts", "acknowledge"},
		// Incident operations
		{"incidents.view", "incidents", "view"},
		{"incidents.create", "incidents", "create"},
		{"incidents.update", "incidents", "update"},
		{"incidents.resolve", "incidents", "resolve"},
		{"incidents.delete", "incidents", "delete"},
		// Maintenance windows
		{"maintenance.view", "maintenance", "view"},
		{"maintenance.create", "maintenance", "create"},
		{"maintenance.update", "maintenance", "update"},
		{"maintenance.delete", "maintenance", "delete"},
		// Workflows / automations
		{"workflows.view", "workflows", "view"},
		{"workflows.create", "workflows", "create"},
		{"workflows.update", "workflows", "update"},
		{"workflows.delete", "workflows", "delete"},
		{"workflows.execute", "workflows", "execute"},
		// Correlations / AIOps
		{"correlations.view", "correlations", "view"},
		{"correlations.manage", "correlations", "manage"},
		{"aiops.view", "aiops", "view"},
		{"aiops.configure", "aiops", "configure"},
		// Topology / infrastructure
		{"topology.view", "topology", "view"},
		{"topology.manage", "topology", "manage"},
		// On-call schedules
		{"oncall.view", "oncall", "view"},
		{"oncall.manage", "oncall", "manage"},
		// Analytics
		{"analytics.view", "analytics", "view"},
		{"analytics.export", "analytics", "export"},
		// Integrations
		{"integrations.view", "integrations", "view"},
		{"integrations.manage", "integrations", "manage"},
		// AI assistant
		{"ai.use", "ai", "use"},
		{"ai.configure", "ai", "configure"},
		// System / admin
		{"audit.view", "audit", "view"},
		{"system.view", "system", "view"},
		{"system.configure", "system", "configure"},
	}

	// Build a nameid map for permission assignment
	permIDs := make(map[string]string, len(permissions))
	for _, perm := range permissions {
		var permID string
		db.QueryRowContext(ctx, `
			INSERT INTO permissions (name, resource, action)
			VALUES ($1, $2, $3)
			ON CONFLICT (name) DO UPDATE SET resource = EXCLUDED.resource
			RETURNING id
		`, perm.name, perm.resource, perm.action).Scan(&permID)
		if permID != "" {
			permIDs[perm.name] = permID
		}

		// Assign every permission to admin role
		db.ExecContext(ctx, `
			INSERT INTO role_permissions (role_id, permission_id)
			VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, adminRoleID, permID)
	}

	// Operator role (SRE / on-call engineers) 
	var operatorRoleID string
	db.QueryRowContext(ctx, `
		INSERT INTO roles (name, description, is_system_role)
		VALUES ('operator', 'SRE and on-call engineers — full alert/incident operations', true)
		ON CONFLICT (name) DO UPDATE SET description = EXCLUDED.description
		RETURNING id
	`).Scan(&operatorRoleID)

	operatorPerms := []string{
		"alerts.view", "alerts.create", "alerts.update", "alerts.assign",
		"alerts.resolve", "alerts.acknowledge",
		"incidents.view", "incidents.create", "incidents.update", "incidents.resolve",
		"maintenance.view", "maintenance.create", "maintenance.update",
		"workflows.view", "workflows.execute",
		"correlations.view", "aiops.view",
		"topology.view",
		"oncall.view",
		"analytics.view",
		"integrations.view",
		"ai.use",
		"audit.view",
		"system.view",
	}
	for _, name := range operatorPerms {
		if pid, ok := permIDs[name]; ok && operatorRoleID != "" {
			db.ExecContext(ctx, `
				INSERT INTO role_permissions (role_id, permission_id)
				VALUES ($1, $2)
				ON CONFLICT DO NOTHING
			`, operatorRoleID, pid)
		}
	}

	// Viewer role (read-only) 
	var viewerRoleID string
	db.QueryRowContext(ctx, `
		INSERT INTO roles (name, description, is_system_role)
		VALUES ('viewer', 'Read-only access to alerts, incidents, and analytics', true)
		ON CONFLICT (name) DO UPDATE SET description = EXCLUDED.description
		RETURNING id
	`).Scan(&viewerRoleID)

	viewerPerms := []string{
		"alerts.view",
		"incidents.view",
		"maintenance.view",
		"workflows.view",
		"correlations.view",
		"aiops.view",
		"topology.view",
		"oncall.view",
		"analytics.view",
		"integrations.view",
		"system.view",
	}
	for _, name := range viewerPerms {
		if pid, ok := permIDs[name]; ok && viewerRoleID != "" {
			db.ExecContext(ctx, `
				INSERT INTO role_permissions (role_id, permission_id)
				VALUES ($1, $2)
				ON CONFLICT DO NOTHING
			`, viewerRoleID, pid)
		}
	}

	log.Println("  Roles (admin, operator, viewer) and permissions seeded")

	// ── Post-seed schema improvements (idempotent — safe to re-run) ──────────

	improvements := []string{
		// api_request_log: audit middleware logs every request. Missing table floods
		// logs with 'pq: relation "api_request_log" does not exist' every ~10s.
		`CREATE TABLE IF NOT EXISTS api_request_log (
			id           BIGSERIAL PRIMARY KEY,
			request_id   VARCHAR(128),
			user_id      UUID,
			api_key_id   UUID,
			method       VARCHAR(10),
			path         TEXT,
			query        TEXT,
			status_code  INTEGER,
			latency_ms   BIGINT,
			ip_address   INET,
			user_agent   TEXT,
			error_msg    TEXT,
			logged_at    TIMESTAMP DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_api_request_log_user ON api_request_log(user_id, logged_at DESC) WHERE user_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_api_request_log_path ON api_request_log(path, logged_at DESC)`,
		// Prune old audit rows — keep last 30 days only
		`DELETE FROM api_request_log WHERE logged_at < NOW() - INTERVAL '30 days'`,
		// rca_investigations: promote structured columns out of the opaque JSONB
		// blob so status, domain and confidence are queryable without JSON extraction.
		`ALTER TABLE rca_investigations ADD COLUMN IF NOT EXISTS status VARCHAR(50) DEFAULT 'completed'`,
		`ALTER TABLE rca_investigations ADD COLUMN IF NOT EXISTS domain VARCHAR(100)`,
		`ALTER TABLE rca_investigations ADD COLUMN IF NOT EXISTS root_cause_summary TEXT`,
		`ALTER TABLE rca_investigations ADD COLUMN IF NOT EXISTS confidence FLOAT DEFAULT 0`,
		`ALTER TABLE rca_investigations ADD COLUMN IF NOT EXISTS phase VARCHAR(50)`,

		// Backfill structured columns from JSONB for existing rows.
		`UPDATE rca_investigations
		 SET
		   status             = COALESCE(data->>'status', 'completed'),
		   domain             = data->'go_context'->>'domain',
		   root_cause_summary = data->'root_causes'->0->>'description',
		   confidence         = COALESCE((data->'root_causes'->0->>'confidence')::float, 0),
		   phase              = data->>'phase'
		 WHERE status IS NULL OR status = 'completed'`,

		// Indexes on promoted columns.
		`CREATE INDEX IF NOT EXISTS idx_rca_inv_status     ON rca_investigations(status)`,
		`CREATE INDEX IF NOT EXISTS idx_rca_inv_domain     ON rca_investigations(domain) WHERE domain IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_rca_inv_created    ON rca_investigations(created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_rca_inv_confidence ON rca_investigations(confidence DESC) WHERE confidence > 0`,

		// alerts: updated_at indexed for sweep queries, incident_id for merge path.
		`CREATE INDEX IF NOT EXISTS idx_alerts_updated_at   ON alerts(updated_at DESC) WHERE status != 'resolved'`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_incident_id  ON alerts(incident_id) WHERE incident_id IS NOT NULL`,

		// incidents: resolved_at for SLA queries.
		`CREATE INDEX IF NOT EXISTS idx_incidents_resolved_at ON incidents(resolved_at) WHERE resolved_at IS NOT NULL`,

		// notification_log: compound index for channel history queries.
		`CREATE INDEX IF NOT EXISTS idx_notification_log_channel_sent ON notification_log(channel_id, sent_at DESC)`,

		// correlation_feature_store: domain+source for adaptive weight lookups.
		`CREATE INDEX IF NOT EXISTS idx_feature_store_domain_source ON correlation_feature_store(domain, source)`,

		// High-churn tables: aggressive autovacuum to prevent dead-tuple bloat.
		`ALTER TABLE rca_investigations SET (autovacuum_vacuum_scale_factor = 0.02, autovacuum_analyze_scale_factor = 0.01)`,
		`ALTER TABLE alerts             SET (autovacuum_vacuum_scale_factor = 0.02, autovacuum_analyze_scale_factor = 0.01)`,
		`ALTER TABLE incidents          SET (autovacuum_vacuum_scale_factor = 0.02, autovacuum_analyze_scale_factor = 0.01)`,

		// system_config: UNIQUE constraint on (category, key) required for LDAP
		// config UPSERT in ConfigHandler.UpdateLDAPConfig.
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_system_config_category_key ON system_config(category, key)`,

		// topology_entities: entity_type index for RCA engine traversals.
		// Previously caused 73K sequential scans vs 2 index scans (36,000:1 ratio).
		`CREATE INDEX IF NOT EXISTS idx_topology_entities_entity_type ON topology_entities(entity_type) WHERE entity_type IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_topology_entities_cluster_type ON topology_entities(cluster_name, entity_type)`,
		`CREATE INDEX IF NOT EXISTS idx_topology_entities_external_source ON topology_entities(source, external_id)`,
		`CREATE INDEX IF NOT EXISTS idx_infra_edges_source_type ON infrastructure_dependency_edges(source_id, edge_type)`,

		// Clean expired sessions (safe one-time pruning).
		`DELETE FROM user_sessions WHERE expires_at < NOW() - INTERVAL '7 days'`,
	}

	for _, sql := range improvements {
		if _, err := db.ExecContext(ctx, sql); err != nil {
			// Non-fatal: log and continue.  Some statements fail on older schemas
			// (e.g. table doesn't exist yet in fresh test DBs) — never block startup.
			log.Printf("  [migration] warning: %v", err)
		}
	}
	log.Println("  Post-seed schema improvements applied")

	// ── KubeSense intelligence tables (idempotent — safe to re-run) ──────────
	// These tables are written by the KubeSense Kafka consumer and read by
	// the OIE evidence bus and CACIE temporal engine.
	kubeSenseTables := []string{
		// kubesense_health_events — stores health.pod.* and health.node.* events
		// (CrashLoopBackOff, OOMKilled, ImagePullError, NodeNotReady, etc.)
		`CREATE TABLE IF NOT EXISTS kubesense_health_events (
			id           VARCHAR(64) PRIMARY KEY,
			cluster_id   VARCHAR(128) NOT NULL,
			event_type   VARCHAR(100) NOT NULL,
			severity     VARCHAR(20)  NOT NULL,
			resource_kind VARCHAR(64),
			namespace    VARCHAR(255),
			resource_name VARCHAR(255),
			resource_uid VARCHAR(64),
			payload      JSONB,
			occurred_at  TIMESTAMP NOT NULL,
			received_at  TIMESTAMP DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ks_health_cluster_type ON kubesense_health_events(cluster_id, event_type)`,
		`CREATE INDEX IF NOT EXISTS idx_ks_health_ns_name ON kubesense_health_events(namespace, resource_name) WHERE namespace IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_ks_health_occurred ON kubesense_health_events(occurred_at DESC)`,
		// Composite index for cluster_id + occurred_at queries (the main health tab filter).
		// Without this, filtering by cluster_id on a 17M-row table then sorting by occurred_at
		// results in a slow sequential scan, causing the /db/health endpoint to return 500.
		`CREATE INDEX IF NOT EXISTS idx_ks_health_cluster_occurred ON kubesense_health_events(cluster_id, occurred_at DESC)`,

		// kubesense_config_violations — pre-computed rule violations (missing probes, image:latest, etc.)
		`CREATE TABLE IF NOT EXISTS kubesense_config_violations (
			id            VARCHAR(64) PRIMARY KEY,
			cluster_id    VARCHAR(128) NOT NULL,
			rule_id       VARCHAR(64)  NOT NULL,
			severity      VARCHAR(20)  NOT NULL,
			resource_kind VARCHAR(64),
			namespace     VARCHAR(255),
			resource_name VARCHAR(255),
			message       TEXT,
			remediation   TEXT,
			occurred_at   TIMESTAMP NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ks_violations_ns_name ON kubesense_config_violations(namespace, resource_name)`,
		`CREATE INDEX IF NOT EXISTS idx_ks_violations_occurred ON kubesense_config_violations(occurred_at DESC)`,

		// kubesense_forecasts — PVC/node exhaustion/cert expiry predictions
		`CREATE TABLE IF NOT EXISTS kubesense_forecasts (
			id               VARCHAR(64) PRIMARY KEY,
			cluster_id       VARCHAR(128) NOT NULL,
			target           VARCHAR(64)  NOT NULL,
			resource_kind    VARCHAR(64),
			namespace        VARCHAR(255),
			resource_name    VARCHAR(255),
			current_value    DOUBLE PRECISION,
			threshold        DOUBLE PRECISION,
			predicted_breach TIMESTAMP,
			trend_per_day    DOUBLE PRECISION,
			model_confidence DOUBLE PRECISION,
			payload          JSONB,
			created_at       TIMESTAMP NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ks_forecasts_ns_name ON kubesense_forecasts(namespace, resource_name)`,
		`CREATE INDEX IF NOT EXISTS idx_ks_forecasts_breach ON kubesense_forecasts(predicted_breach) WHERE predicted_breach IS NOT NULL`,

		// kubesense_apm_signals — golden signals (rate, errors, duration, saturation)
		`CREATE TABLE IF NOT EXISTS kubesense_apm_signals (
			id             VARCHAR(64) PRIMARY KEY,
			cluster_id     VARCHAR(128) NOT NULL,
			namespace      VARCHAR(255),
			service_name   VARCHAR(255),
			request_rate   DOUBLE PRECISION,
			error_rate     DOUBLE PRECISION,
			latency_p99_ms DOUBLE PRECISION,
			saturation     DOUBLE PRECISION,
			sampled_at     TIMESTAMP NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ks_apm_ns_svc ON kubesense_apm_signals(namespace, service_name)`,
		`CREATE INDEX IF NOT EXISTS idx_ks_apm_sampled ON kubesense_apm_signals(sampled_at DESC)`,

		// kubesense_investigation_results — KubeSense RCA results for incidents
		`CREATE TABLE IF NOT EXISTS kubesense_investigation_results (
			id             VARCHAR(64) PRIMARY KEY,
			incident_id    UUID,
			cluster_id     VARCHAR(128),
			grade          VARCHAR(2),
			confidence     DOUBLE PRECISION,
			root_cause     TEXT,
			summary        TEXT,
			evidence_count INTEGER,
			payload        JSONB,
			completed_at   TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ks_inv_results_incident ON kubesense_investigation_results(incident_id) WHERE incident_id IS NOT NULL`,

		// Retain KubeSense data for 14 days — aggressive cleanup to prevent table bloat.
		// (Not a DDL statement — run as a one-time cleanup at startup.)
		`DELETE FROM kubesense_health_events     WHERE received_at < NOW() - INTERVAL '14 days'`,
		`DELETE FROM kubesense_apm_signals       WHERE sampled_at  < NOW() - INTERVAL '3 days'`,
		`DELETE FROM kubesense_config_violations WHERE occurred_at < NOW() - INTERVAL '30 days'`,
		`DELETE FROM kubesense_forecasts         WHERE created_at  < NOW() - INTERVAL '14 days'`,
	}

	for _, sql := range kubeSenseTables {
		if _, err := db.ExecContext(ctx, sql); err != nil {
			log.Printf("  [migration] kubesense: %v", err)
		}
	}
	log.Println("  KubeSense intelligence tables ready")

	// ── Postmortem schema extensions (idempotent) ──────────────────────────────
	pmExtensions := []string{
		// Unique constraint so GenerateForIncident can upsert safely.
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_post_mortems_incident_id ON post_mortems(incident_id) WHERE incident_id IS NOT NULL`,
		// Add timeline JSONB column for structured postmortem content storage.
		`ALTER TABLE post_mortems ADD COLUMN IF NOT EXISTS generated_by VARCHAR(20) DEFAULT 'template'`,
		`ALTER TABLE post_mortems ADD COLUMN IF NOT EXISTS rca_confidence DOUBLE PRECISION`,
		`ALTER TABLE post_mortems ADD COLUMN IF NOT EXISTS contributing_factors TEXT[]`,
	}
	for _, sql := range pmExtensions {
		if _, err := db.ExecContext(ctx, sql); err != nil {
			log.Printf("  [migration] postmortem extension: %v", err)
		}
	}
	log.Println("  Postmortem schema extensions applied")

	// ── Gate hooks + Skill catalog + Policy engine tables ─────────────────────
	gateTables := []string{
		// remediations_pending: gate hook table (Sympozium pattern)
		`CREATE TABLE IF NOT EXISTS remediations_pending (
			id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			incident_id      UUID REFERENCES incidents(id) ON DELETE CASCADE,
			proposed_action  TEXT NOT NULL,
			action_type      VARCHAR(50) DEFAULT 'manual',
			risk_level       VARCHAR(20) DEFAULT 'medium',
			status           VARCHAR(30) DEFAULT 'proposed',
			proposed_by      VARCHAR(100),
			approved_by      VARCHAR(100),
			rejection_reason TEXT,
			executed_at      TIMESTAMP,
			created_at       TIMESTAMP DEFAULT NOW(),
			updated_at       TIMESTAMP DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_remediations_incident ON remediations_pending(incident_id)`,
		`CREATE INDEX IF NOT EXISTS idx_remediations_status ON remediations_pending(status) WHERE status = 'proposed'`,

		// investigation_runbooks: SkillCatalog (HolmesGPT pattern)
		`CREATE TABLE IF NOT EXISTS investigation_runbooks (
			id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			name          VARCHAR(255) NOT NULL,
			domain        VARCHAR(100) DEFAULT '',
			entity_type   VARCHAR(100) DEFAULT '',
			failure_class VARCHAR(100) DEFAULT '',
			content       TEXT NOT NULL,
			source        VARCHAR(50) DEFAULT 'operator',
			enabled       BOOLEAN DEFAULT true,
			created_at    TIMESTAMP DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_runbooks_domain_entity ON investigation_runbooks(domain, entity_type) WHERE enabled = true`,

		// intelligence_policies: SympoziumPolicy table (replaces hardcoded suppressions)
		`CREATE TABLE IF NOT EXISTS intelligence_policies (
			id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			name           VARCHAR(255) NOT NULL UNIQUE,
			description    TEXT,
			policy_type    VARCHAR(50) NOT NULL,
			condition_json JSONB NOT NULL DEFAULT '{}',
			action         VARCHAR(50) DEFAULT 'suppress',
			enabled        BOOLEAN DEFAULT true,
			priority       INTEGER DEFAULT 50,
			created_at     TIMESTAMP DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_policies_enabled_priority ON intelligence_policies(priority DESC) WHERE enabled = true`,

		// Seed built-in policies that replace the isKnownTestWorkload hardcoded check.
		`INSERT INTO intelligence_policies (name, description, policy_type, condition_json, action, priority)
		 VALUES
		   ('suppress-liveness-fail', 'Suppress liveness probe failure test alerts', 'suppress_alert',
		    '{"title_contains": "liveness-fail"}', 'suppress', 80),
		   ('suppress-tmp-debug', 'Suppress debug/temporary pod alerts', 'suppress_alert',
		    '{"title_contains": "tmp-debug"}', 'suppress', 80),
		   ('suppress-rca-cli', 'Suppress RCA CLI test workloads', 'suppress_alert',
		    '{"title_contains": "rca-claude-cli"}', 'suppress', 80),
		   ('skip-rca-dev-namespace', 'Skip RCA for dev namespace incidents', 'skip_rca',
		    '{"label_key": "environment", "label_value": "dev"}', 'skip_rca', 60)
		 ON CONFLICT (name) DO NOTHING`,

		// Seed default runbooks for common K8s failure patterns.
		`INSERT INTO investigation_runbooks (name, domain, entity_type, failure_class, content, source)
		 VALUES
		   ('CrashLoopBackOff Runbook', 'k8s', 'pod', 'CrashLoopBackOff',
		    'Steps: 1) kubectl logs <pod> --previous to check exit reason. 2) Check readiness/liveness probes. 3) Verify ConfigMap/Secret references. 4) Check resource limits — OOMKilled shows as exit code 137. 5) If image issue: kubectl describe pod <pod> for ImagePullBackOff details.',
		    'system'),
		   ('OOMKilled Runbook', 'k8s', 'pod', 'OOMKilled',
		    'Steps: 1) Check current memory limits with kubectl get pod -o yaml. 2) Review memory usage in Grafana over last 1h. 3) Increase memory limit by 2x as temporary fix. 4) Identify memory leak: check for unbounded caches, connection pools, or large in-memory queues.',
		    'system'),
		   ('ImagePullBackOff Runbook', 'k8s', 'pod', 'ImagePullBackOff',
		    'Steps: 1) Verify image tag exists: docker pull <image>. 2) Check imagePullSecret exists in namespace. 3) Verify JFrog/registry credentials are not expired. 4) Check if image digest changed — use @sha256 pin for stability.',
		    'system'),
		   ('PVC Full Runbook', 'k8s', 'pvc', 'StorageFull',
		    'Steps: 1) kubectl exec into pod to identify large files (du -sh /data/*). 2) Check PVC expansion support: kubectl get storageclass. 3) If NetApp: check aggregate utilization via NetApp ONTAP. 4) Immediate: delete old logs/temp files. Long-term: set up log rotation.',
		    'system'),
		   ('NodeNotReady Runbook', 'k8s', 'node', 'NotReady',
		    'Steps: 1) ssh to node and check kubelet: systemctl status kubelet. 2) Check disk pressure: df -h. 3) Check memory: free -m. 4) Check CNI: ls /opt/cni/bin. 5) If CSI issue: check DT OneAgent is not blocking pod init containers.',
		    'system')
		 ON CONFLICT DO NOTHING`,
	}

	for _, sql := range gateTables {
		if _, err := db.ExecContext(ctx, sql); err != nil {
			log.Printf("  [migration] gate/skill/policy: %v", err)
		}
	}
	log.Println("  Gate hooks, skill catalog, and policy tables ready")

	return nil
}
