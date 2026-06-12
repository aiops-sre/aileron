-- Critical Missing Tables Migration
-- Fix for immediate production deployment blocker
-- Version: critical_fixes_v1.0.0

-- Apply the missing enterprise tables if they don't exist
DO $$ 
BEGIN
    -- Check and create infrastructure_correlations table
    IF NOT EXISTS (SELECT FROM pg_tables WHERE schemaname = 'public' AND tablename = 'infrastructure_correlations') THEN
        CREATE TABLE infrastructure_correlations (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            source_entity_id VARCHAR(255) NOT NULL,
            target_entity_id VARCHAR(255) NOT NULL,
            entity_type VARCHAR(100) NOT NULL,
            correlation_type VARCHAR(100) NOT NULL,
            relationship_strength DECIMAL(3,2) DEFAULT 0.0,
            dependency_direction VARCHAR(20) DEFAULT 'bidirectional',
            infrastructure_layer VARCHAR(50) NOT NULL,
            cluster_id VARCHAR(255),
            namespace_name VARCHAR(255),
            service_name VARCHAR(255),
            correlation_data JSONB DEFAULT '{}',
            discovered_method VARCHAR(100) NOT NULL,
            confidence_score DECIMAL(3,2) DEFAULT 0.0,
            last_validated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
            validation_count INTEGER DEFAULT 0,
            is_active BOOLEAN DEFAULT TRUE,
            metadata JSONB DEFAULT '{}',
            created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
            
            CHECK (relationship_strength >= 0.0 AND relationship_strength <= 1.0),
            CHECK (confidence_score >= 0.0 AND confidence_score <= 1.0),
            CHECK (dependency_direction IN ('upstream', 'downstream', 'bidirectional'))
        );
        
        CREATE INDEX IF NOT EXISTS idx_infrastructure_correlations_source ON infrastructure_correlations(source_entity_id);
        CREATE INDEX IF NOT EXISTS idx_infrastructure_correlations_target ON infrastructure_correlations(target_entity_id);
        CREATE INDEX IF NOT EXISTS idx_infrastructure_correlations_type ON infrastructure_correlations(entity_type);
        CREATE INDEX IF NOT EXISTS idx_infrastructure_correlations_active ON infrastructure_correlations(is_active) WHERE is_active = TRUE;
        
        RAISE NOTICE 'Created infrastructure_correlations table';
    END IF;

    -- Check and create enhanced_correlation_rules table
    IF NOT EXISTS (SELECT FROM pg_tables WHERE schemaname = 'public' AND tablename = 'enhanced_correlation_rules') THEN
        CREATE TABLE enhanced_correlation_rules (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            name VARCHAR(255) NOT NULL UNIQUE,
            description TEXT,
            rule_type VARCHAR(100) NOT NULL DEFAULT 'ml_enhanced',
            conditions JSONB NOT NULL DEFAULT '[]',
            actions JSONB NOT NULL DEFAULT '[]',
            ml_model_config JSONB DEFAULT '{}',
            infrastructure_mapping JSONB DEFAULT '{}',
            priority INTEGER DEFAULT 0,
            confidence_threshold DECIMAL(3,2) DEFAULT 0.70,
            enabled BOOLEAN DEFAULT TRUE,
            auto_learning BOOLEAN DEFAULT FALSE,
            performance_metrics JSONB DEFAULT '{}',
            last_trained_at TIMESTAMP WITH TIME ZONE,
            training_data_size INTEGER DEFAULT 0,
            success_rate DECIMAL(5,4) DEFAULT 0.0,
            metadata JSONB DEFAULT '{}',
            created_by UUID REFERENCES users(id) ON DELETE SET NULL,
            updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
            created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
            
            CHECK (confidence_threshold >= 0.0 AND confidence_threshold <= 1.0),
            CHECK (success_rate >= 0.0 AND success_rate <= 1.0)
        );
        
        CREATE INDEX IF NOT EXISTS idx_enhanced_correlation_rules_enabled ON enhanced_correlation_rules(enabled) WHERE enabled = TRUE;
        CREATE INDEX IF NOT EXISTS idx_enhanced_correlation_rules_priority ON enhanced_correlation_rules(priority DESC);
        
        RAISE NOTICE 'Created enhanced_correlation_rules table';
    END IF;

    -- Check and create user_expertise table
    IF NOT EXISTS (SELECT FROM pg_tables WHERE schemaname = 'public' AND tablename = 'user_expertise') THEN
        CREATE TABLE user_expertise (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
            expertise_type VARCHAR(100) NOT NULL,
            expertise_area VARCHAR(255) NOT NULL,
            expertise_level VARCHAR(50) NOT NULL DEFAULT 'intermediate',
            confidence_score DECIMAL(3,2) DEFAULT 0.5,
            technologies JSONB DEFAULT '[]',
            services JSONB DEFAULT '[]',
            infrastructure_components JSONB DEFAULT '[]',
            resolution_count INTEGER DEFAULT 0,
            avg_resolution_time_minutes INTEGER DEFAULT 0,
            success_rate DECIMAL(5,4) DEFAULT 0.0,
            escalation_rate DECIMAL(5,4) DEFAULT 0.0,
            auto_detected BOOLEAN DEFAULT FALSE,
            last_activity_date TIMESTAMP WITH TIME ZONE,
            skill_validation_date TIMESTAMP WITH TIME ZONE,
            validated_by UUID REFERENCES users(id) ON DELETE SET NULL,
            preferred_alert_types JSONB DEFAULT '[]',
            availability_hours JSONB DEFAULT '{}',
            max_concurrent_alerts INTEGER DEFAULT 5,
            notification_preferences JSONB DEFAULT '{}',
            metadata JSONB DEFAULT '{}',
            created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
            
            UNIQUE(user_id, expertise_area, expertise_type),
            CHECK (confidence_score >= 0.0 AND confidence_score <= 1.0),
            CHECK (success_rate >= 0.0 AND success_rate <= 1.0),
            CHECK (escalation_rate >= 0.0 AND escalation_rate <= 1.0),
            CHECK (expertise_level IN ('beginner', 'intermediate', 'advanced', 'expert'))
        );
        
        CREATE INDEX IF NOT EXISTS idx_user_expertise_user_id ON user_expertise(user_id);
        CREATE INDEX IF NOT EXISTS idx_user_expertise_area ON user_expertise(expertise_area);
        CREATE INDEX IF NOT EXISTS idx_user_expertise_technologies ON user_expertise USING gin(technologies);
        
        RAISE NOTICE 'Created user_expertise table';
    END IF;

    -- Create teams table if it doesn't exist (required by oncall_schedules)
    IF NOT EXISTS (SELECT FROM pg_tables WHERE schemaname = 'public' AND tablename = 'teams') THEN
        CREATE TABLE teams (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            name VARCHAR(255) NOT NULL UNIQUE,
            description TEXT,
            team_type VARCHAR(100) DEFAULT 'engineering',
            parent_team_id UUID REFERENCES teams(id) ON DELETE SET NULL,
            manager_id UUID REFERENCES users(id) ON DELETE SET NULL,
            primary_services JSONB DEFAULT '[]',
            secondary_services JSONB DEFAULT '[]',
            expertise_areas JSONB DEFAULT '[]',
            escalation_contacts JSONB DEFAULT '[]',
            avg_response_time_minutes INTEGER DEFAULT 0,
            resolution_rate DECIMAL(5,4) DEFAULT 0.0,
            alert_volume_last_30d INTEGER DEFAULT 0,
            team_size INTEGER DEFAULT 0,
            notification_channels JSONB DEFAULT '{}',
            working_hours JSONB DEFAULT '{}',
            sla_targets JSONB DEFAULT '{}',
            is_active BOOLEAN DEFAULT TRUE,
            metadata JSONB DEFAULT '{}',
            created_by UUID REFERENCES users(id) ON DELETE SET NULL,
            created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
        );
        
        CREATE INDEX IF NOT EXISTS idx_teams_active ON teams(is_active) WHERE is_active = TRUE;
        
        -- Insert default teams for immediate use
        INSERT INTO teams (name, description, team_type, expertise_areas) VALUES
        ('SRE Team', 'Site Reliability Engineering team', 'sre', '["kubernetes", "monitoring", "infrastructure"]'),
        ('DevOps Team', 'Development Operations team', 'devops', '["ci-cd", "deployment", "automation"]')
        ON CONFLICT (name) DO NOTHING;
        
        RAISE NOTICE 'Created teams table with default teams';
    END IF;

    -- Update oncall_schedules table - create if doesn't exist, or add missing columns
    IF NOT EXISTS (SELECT FROM pg_tables WHERE schemaname = 'public' AND tablename = 'oncall_schedules') THEN
        -- Create new table with all required fields
        CREATE TABLE oncall_schedules (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            name VARCHAR(255) NOT NULL UNIQUE,
            description TEXT,
            team_id UUID REFERENCES teams(id) ON DELETE CASCADE,
            schedule_type VARCHAR(50) NOT NULL DEFAULT 'rotation',
            rotation_config JSONB DEFAULT '{}',
            timezone VARCHAR(100) DEFAULT 'UTC',
            effective_start_date TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            effective_end_date TIMESTAMP WITH TIME ZONE,
            ai_optimized BOOLEAN DEFAULT FALSE,
            optimization_criteria JSONB DEFAULT '{}',
            last_optimized_at TIMESTAMP WITH TIME ZONE,
            alert_routing_rules JSONB DEFAULT '[]',
            escalation_policy JSONB DEFAULT '{}',
            override_policies JSONB DEFAULT '[]',
            avg_response_time_minutes INTEGER DEFAULT 0,
            coverage_percentage DECIMAL(5,2) DEFAULT 100.0,
            alert_volume_last_30d INTEGER DEFAULT 0,
            resolution_rate DECIMAL(5,4) DEFAULT 0.0,
            is_active BOOLEAN DEFAULT TRUE,
            auto_generated BOOLEAN DEFAULT FALSE,
            approval_required BOOLEAN DEFAULT TRUE,
            approved_by UUID REFERENCES users(id) ON DELETE SET NULL,
            approved_at TIMESTAMP WITH TIME ZONE,
            metadata JSONB DEFAULT '{}',
            created_by UUID REFERENCES users(id) ON DELETE SET NULL,
            created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
            
            CHECK (coverage_percentage >= 0.0 AND coverage_percentage <= 100.0),
            CHECK (resolution_rate >= 0.0 AND resolution_rate <= 1.0)
        );
        
        CREATE INDEX IF NOT EXISTS idx_oncall_schedules_active ON oncall_schedules(is_active) WHERE is_active = TRUE;
        CREATE INDEX IF NOT EXISTS idx_oncall_schedules_team ON oncall_schedules(team_id);
        
        RAISE NOTICE 'Created enhanced oncall_schedules table';
    ELSE
        -- Add missing columns to existing table
        ALTER TABLE oncall_schedules ADD COLUMN IF NOT EXISTS team_id UUID REFERENCES teams(id) ON DELETE CASCADE;
        ALTER TABLE oncall_schedules ADD COLUMN IF NOT EXISTS schedule_type VARCHAR(50) DEFAULT 'rotation';
        ALTER TABLE oncall_schedules ADD COLUMN IF NOT EXISTS effective_start_date TIMESTAMP WITH TIME ZONE DEFAULT NOW();
        ALTER TABLE oncall_schedules ADD COLUMN IF NOT EXISTS ai_optimized BOOLEAN DEFAULT FALSE;
        ALTER TABLE oncall_schedules ADD COLUMN IF NOT EXISTS metadata JSONB DEFAULT '{}';
        
        RAISE NOTICE 'Updated existing oncall_schedules table with missing columns';
    END IF;

    -- Add performance indexes that are critical for production
    CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_alerts_status_created_at 
        ON alerts(status, created_at DESC) WHERE status IN ('open', 'acknowledged');
    
    CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_alerts_fingerprint_active 
        ON alerts(fingerprint) WHERE fingerprint IS NOT NULL AND status != 'resolved';
    
    CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_incidents_status_priority 
        ON incidents(status, priority, created_at DESC) WHERE status IN ('open', 'investigating');

    RAISE NOTICE 'Applied critical performance indexes';

    RAISE NOTICE 'Critical missing tables migration completed successfully';
END $$;