-- Security Audit Trails and Compliance Monitoring
-- Phase 4 Priority 2: Production Security Hardening

-- Security Events Audit Table
CREATE TABLE IF NOT EXISTS security_events (
    id BIGSERIAL PRIMARY KEY,
    event_id VARCHAR(255) UNIQUE NOT NULL,
    event_type VARCHAR(50) NOT NULL, -- AUTH, AUTHZ, ENCRYPTION, CERT, SCAN, THREAT
    severity VARCHAR(20) NOT NULL, -- LOW, MEDIUM, HIGH, CRITICAL
    user_id VARCHAR(255),
    service_id VARCHAR(255),
    resource VARCHAR(500),
    action VARCHAR(100) NOT NULL,
    result VARCHAR(50) NOT NULL, -- SUCCESS, FAILURE, ERROR
    timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ip_address INET,
    user_agent TEXT,
    details JSONB,
    session_id VARCHAR(255),
    correlation_id VARCHAR(255),
    geographic_location JSONB,
    risk_score INTEGER DEFAULT 0,
    compliance_tags TEXT[],
    remediation_status VARCHAR(50) DEFAULT 'NONE', -- NONE, PENDING, COMPLETED, FAILED
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Authentication Attempts Audit Table
CREATE TABLE IF NOT EXISTS auth_attempts (
    id BIGSERIAL PRIMARY KEY,
    attempt_id VARCHAR(255) UNIQUE NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    service_id VARCHAR(255),
    auth_method VARCHAR(50) NOT NULL, -- PASSWORD, CERTIFICATE, TOKEN, MFA, OAUTH, SAML
    success BOOLEAN NOT NULL,
    failure_code VARCHAR(100),
    failure_reason TEXT,
    ip_address INET NOT NULL,
    user_agent TEXT,
    timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    mfa_required BOOLEAN DEFAULT FALSE,
    mfa_completed BOOLEAN DEFAULT FALSE,
    mfa_method VARCHAR(50), -- TOTP, SMS, EMAIL, HARDWARE_TOKEN
    session_duration_seconds INTEGER,
    device_fingerprint VARCHAR(500),
    geographic_location JSONB,
    risk_factors TEXT[],
    compliance_policies TEXT[],
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Authorization Checks Audit Table
CREATE TABLE IF NOT EXISTS authorization_checks (
    id BIGSERIAL PRIMARY KEY,
    check_id VARCHAR(255) UNIQUE NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    service_id VARCHAR(255),
    resource VARCHAR(500) NOT NULL,
    action VARCHAR(100) NOT NULL,
    allowed BOOLEAN NOT NULL,
    reason TEXT,
    timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    context JSONB,
    policy_evaluated TEXT[],
    decision_time_ms INTEGER,
    token_id VARCHAR(255),
    session_id VARCHAR(255),
    request_id VARCHAR(255),
    compliance_check_results JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Data Access Audit Table
CREATE TABLE IF NOT EXISTS data_access_logs (
    id BIGSERIAL PRIMARY KEY,
    access_id VARCHAR(255) UNIQUE NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    service_id VARCHAR(255),
    data_type VARCHAR(100) NOT NULL, -- ALERT, INCIDENT, USER_DATA, CONFIG, SENSITIVE
    operation VARCHAR(50) NOT NULL, -- READ, WRITE, UPDATE, DELETE, EXPORT
    resource_path VARCHAR(500) NOT NULL,
    record_count INTEGER DEFAULT 0,
    data_size_bytes BIGINT DEFAULT 0,
    success BOOLEAN NOT NULL,
    timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ip_address INET,
    encryption_used BOOLEAN DEFAULT FALSE,
    retention_policy VARCHAR(100),
    classification_level VARCHAR(50), -- PUBLIC, INTERNAL, CONFIDENTIAL, RESTRICTED
    purpose TEXT,
    legal_basis VARCHAR(100), -- GDPR compliance
    data_subject_id VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Vulnerability Findings Table
CREATE TABLE IF NOT EXISTS vulnerability_findings (
    id BIGSERIAL PRIMARY KEY,
    finding_id VARCHAR(255) UNIQUE NOT NULL,
    scan_id VARCHAR(255) NOT NULL,
    vulnerability_type VARCHAR(100) NOT NULL, -- NETWORK, CONTAINER, CODE, CONFIG, CERTIFICATE
    severity VARCHAR(20) NOT NULL, -- CRITICAL, HIGH, MEDIUM, LOW
    cve_id VARCHAR(50),
    cvss_score DECIMAL(3,1),
    title TEXT NOT NULL,
    description TEXT NOT NULL,
    affected_component TEXT NOT NULL,
    discovered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    status VARCHAR(50) DEFAULT 'OPEN', -- OPEN, INVESTIGATING, PATCHED, MITIGATED, FALSE_POSITIVE
    remediation_steps TEXT,
    remediation_deadline TIMESTAMPTZ,
    remediation_completed_at TIMESTAMPTZ,
    risk_score INTEGER DEFAULT 0,
    compliance_impact TEXT[],
    business_impact TEXT,
    technical_details JSONB,
    evidence JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Security Incidents Table
CREATE TABLE IF NOT EXISTS security_incidents (
    id BIGSERIAL PRIMARY KEY,
    incident_id VARCHAR(255) UNIQUE NOT NULL,
    incident_type VARCHAR(100) NOT NULL, -- BREACH, ATTACK, MALWARE, INSIDER_THREAT, COMPLIANCE_VIOLATION
    severity VARCHAR(20) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'DETECTED', -- DETECTED, INVESTIGATING, CONTAINED, RESOLVED, CLOSED
    title TEXT NOT NULL,
    description TEXT NOT NULL,
    detected_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reported_by VARCHAR(255),
    assigned_to VARCHAR(255),
    affected_systems TEXT[],
    affected_users TEXT[],
    attack_vector TEXT,
    indicators_of_compromise TEXT[],
    timeline JSONB,
    impact_assessment TEXT,
    containment_actions TEXT[],
    recovery_actions TEXT[],
    lessons_learned TEXT,
    compliance_notifications JSONB,
    regulatory_reporting_required BOOLEAN DEFAULT FALSE,
    estimated_cost DECIMAL(15,2),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Compliance Frameworks Table
CREATE TABLE IF NOT EXISTS compliance_frameworks (
    id BIGSERIAL PRIMARY KEY,
    framework_id VARCHAR(255) UNIQUE NOT NULL,
    name VARCHAR(200) NOT NULL, -- SOX, PCI_DSS, GDPR, HIPAA, SOC2, ISO27001
    version VARCHAR(50),
    description TEXT,
    applicable_services TEXT[],
    requirements JSONB,
    assessment_frequency VARCHAR(50), -- MONTHLY, QUARTERLY, ANNUALLY
    last_assessment TIMESTAMPTZ,
    next_assessment TIMESTAMPTZ,
    compliance_status VARCHAR(50) DEFAULT 'UNKNOWN', -- COMPLIANT, NON_COMPLIANT, PARTIALLY_COMPLIANT
    compliance_score INTEGER DEFAULT 0,
    findings JSONB,
    remediation_plan TEXT,
    responsible_party VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Compliance Policies Table
CREATE TABLE IF NOT EXISTS compliance_policies (
    id BIGSERIAL PRIMARY KEY,
    policy_id VARCHAR(255) UNIQUE NOT NULL,
    framework_id VARCHAR(255) REFERENCES compliance_frameworks(framework_id),
    policy_name VARCHAR(200) NOT NULL,
    policy_type VARCHAR(100) NOT NULL, -- ACCESS_CONTROL, DATA_PROTECTION, AUDIT, INCIDENT_RESPONSE
    description TEXT NOT NULL,
    implementation_details JSONB,
    enforcement_level VARCHAR(50) NOT NULL, -- MANDATORY, RECOMMENDED, OPTIONAL
    automated_checks JSONB,
    manual_checks JSONB,
    violation_consequences TEXT,
    last_review TIMESTAMPTZ,
    next_review TIMESTAMPTZ,
    owner VARCHAR(255) NOT NULL,
    approver VARCHAR(255),
    status VARCHAR(50) DEFAULT 'ACTIVE', -- ACTIVE, DRAFT, DEPRECATED, SUSPENDED
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Compliance Violations Table
CREATE TABLE IF NOT EXISTS compliance_violations (
    id BIGSERIAL PRIMARY KEY,
    violation_id VARCHAR(255) UNIQUE NOT NULL,
    policy_id VARCHAR(255) REFERENCES compliance_policies(policy_id),
    framework_id VARCHAR(255) REFERENCES compliance_frameworks(framework_id),
    violation_type VARCHAR(100) NOT NULL,
    severity VARCHAR(20) NOT NULL,
    description TEXT NOT NULL,
    detected_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    detected_by VARCHAR(100), -- AUTOMATED_SCAN, MANUAL_REVIEW, USER_REPORT
    affected_systems TEXT[],
    affected_users TEXT[],
    evidence JSONB,
    root_cause TEXT,
    corrective_actions TEXT[],
    preventive_actions TEXT[],
    status VARCHAR(50) DEFAULT 'OPEN', -- OPEN, INVESTIGATING, REMEDIATED, CLOSED
    assigned_to VARCHAR(255),
    due_date TIMESTAMPTZ,
    resolved_at TIMESTAMPTZ,
    business_impact TEXT,
    regulatory_impact TEXT,
    notification_required BOOLEAN DEFAULT FALSE,
    notifications_sent JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Audit Trails Configuration Table
CREATE TABLE IF NOT EXISTS audit_configuration (
    id BIGSERIAL PRIMARY KEY,
    config_key VARCHAR(255) UNIQUE NOT NULL,
    config_value JSONB NOT NULL,
    description TEXT,
    category VARCHAR(100) NOT NULL, -- RETENTION, ENCRYPTION, MONITORING, ALERTING
    is_active BOOLEAN DEFAULT TRUE,
    last_modified TIMESTAMPTZ DEFAULT NOW(),
    modified_by VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Indexes for Performance
CREATE INDEX IF NOT EXISTS idx_security_events_timestamp ON security_events(timestamp);
CREATE INDEX IF NOT EXISTS idx_security_events_user_id ON security_events(user_id);
CREATE INDEX IF NOT EXISTS idx_security_events_event_type ON security_events(event_type);
CREATE INDEX IF NOT EXISTS idx_security_events_severity ON security_events(severity);
CREATE INDEX IF NOT EXISTS idx_security_events_result ON security_events(result);

CREATE INDEX IF NOT EXISTS idx_auth_attempts_timestamp ON auth_attempts(timestamp);
CREATE INDEX IF NOT EXISTS idx_auth_attempts_user_id ON auth_attempts(user_id);
CREATE INDEX IF NOT EXISTS idx_auth_attempts_ip_address ON auth_attempts(ip_address);
CREATE INDEX IF NOT EXISTS idx_auth_attempts_success ON auth_attempts(success);

CREATE INDEX IF NOT EXISTS idx_authorization_checks_timestamp ON authorization_checks(timestamp);
CREATE INDEX IF NOT EXISTS idx_authorization_checks_user_id ON authorization_checks(user_id);
CREATE INDEX IF NOT EXISTS idx_authorization_checks_resource ON authorization_checks(resource);
CREATE INDEX IF NOT EXISTS idx_authorization_checks_allowed ON authorization_checks(allowed);

CREATE INDEX IF NOT EXISTS idx_data_access_logs_timestamp ON data_access_logs(timestamp);
CREATE INDEX IF NOT EXISTS idx_data_access_logs_user_id ON data_access_logs(user_id);
CREATE INDEX IF NOT EXISTS idx_data_access_logs_operation ON data_access_logs(operation);
CREATE INDEX IF NOT EXISTS idx_data_access_logs_data_type ON data_access_logs(data_type);

CREATE INDEX IF NOT EXISTS idx_vulnerability_findings_severity ON vulnerability_findings(severity);
CREATE INDEX IF NOT EXISTS idx_vulnerability_findings_status ON vulnerability_findings(status);
CREATE INDEX IF NOT EXISTS idx_vulnerability_findings_discovered_at ON vulnerability_findings(discovered_at);

CREATE INDEX IF NOT EXISTS idx_security_incidents_severity ON security_incidents(severity);
CREATE INDEX IF NOT EXISTS idx_security_incidents_status ON security_incidents(status);
CREATE INDEX IF NOT EXISTS idx_security_incidents_detected_at ON security_incidents(detected_at);

CREATE INDEX IF NOT EXISTS idx_compliance_violations_severity ON compliance_violations(severity);
CREATE INDEX IF NOT EXISTS idx_compliance_violations_status ON compliance_violations(status);
CREATE INDEX IF NOT EXISTS idx_compliance_violations_framework_id ON compliance_violations(framework_id);

-- Composite indexes for common queries
CREATE INDEX IF NOT EXISTS idx_security_events_user_timestamp ON security_events(user_id, timestamp);
CREATE INDEX IF NOT EXISTS idx_auth_attempts_user_timestamp ON auth_attempts(user_id, timestamp);
CREATE INDEX IF NOT EXISTS idx_data_access_user_operation ON data_access_logs(user_id, operation, timestamp);

-- Partial indexes for failed events
CREATE INDEX IF NOT EXISTS idx_security_events_failures ON security_events(timestamp, user_id) WHERE result = 'FAILURE';
CREATE INDEX IF NOT EXISTS idx_auth_attempts_failures ON auth_attempts(timestamp, user_id, ip_address) WHERE success = FALSE;

-- Insert default compliance frameworks
INSERT INTO compliance_frameworks (framework_id, name, version, description, requirements, assessment_frequency, compliance_status) VALUES
('SOX', 'Sarbanes-Oxley Act', '2002', 'Financial reporting and audit compliance', '{"section_302": "CEO/CFO certification", "section_404": "Internal control assessment", "section_409": "Real-time disclosure"}', 'ANNUALLY', 'UNKNOWN'),
('PCI_DSS', 'Payment Card Industry Data Security Standard', '4.0', 'Credit card data protection standards', '{"requirement_1": "Firewall configuration", "requirement_2": "Default passwords", "requirement_3": "Cardholder data protection"}', 'ANNUALLY', 'UNKNOWN'),
('GDPR', 'General Data Protection Regulation', '2018', 'EU data protection and privacy regulation', '{"article_25": "Data protection by design", "article_32": "Security of processing", "article_33": "Breach notification"}', 'QUARTERLY', 'UNKNOWN'),
('HIPAA', 'Health Insurance Portability and Accountability Act', '1996', 'Healthcare data protection standards', '{"security_rule": "PHI protection", "privacy_rule": "Use and disclosure", "breach_rule": "Breach notification"}', 'ANNUALLY', 'UNKNOWN'),
('SOC2', 'Service Organization Control 2', 'Type II', 'Security and availability controls', '{"security": "Protection against unauthorized access", "availability": "System availability", "confidentiality": "Information confidentiality"}', 'ANNUALLY', 'UNKNOWN'),
('ISO27001', 'Information Security Management', '2013', 'Information security management system', '{"clause_4": "Context of organization", "clause_6": "Planning", "clause_8": "Operation"}', 'ANNUALLY', 'UNKNOWN'),
('NIST_CSF', 'NIST Cybersecurity Framework', '1.1', 'Cybersecurity risk management framework', '{"identify": "Asset management", "protect": "Access control", "detect": "Anomalies and events", "respond": "Response planning", "recover": "Recovery planning"}', 'QUARTERLY', 'UNKNOWN');

-- Insert default audit configuration
INSERT INTO audit_configuration (config_key, config_value, description, category) VALUES
('retention_period_days', '"2555"', 'Audit log retention period in days (7 years)', 'RETENTION'),
('encryption_at_rest', 'true', 'Enable encryption for audit logs at rest', 'ENCRYPTION'),
('real_time_monitoring', 'true', 'Enable real-time security event monitoring', 'MONITORING'),
('alert_on_critical_events', 'true', 'Send alerts for critical security events', 'ALERTING'),
('failed_auth_threshold', '5', 'Number of failed auth attempts before alert', 'ALERTING'),
('sensitive_data_access_alert', 'true', 'Alert on sensitive data access', 'ALERTING'),
('compliance_violation_alert', 'true', 'Alert on compliance violations', 'ALERTING'),
('automated_remediation', 'false', 'Enable automated remediation for violations', 'MONITORING'),
('external_siem_integration', 'true', 'Enable external SIEM integration', 'MONITORING'),
('anonymization_rules', '{"pii_fields": ["email", "phone", "ssn"], "hash_algorithm": "SHA256"}', 'Data anonymization rules for audit logs', 'ENCRYPTION');

-- Views for reporting and compliance
CREATE OR REPLACE VIEW security_events_summary AS
SELECT 
    event_type,
    severity,
    result,
    COUNT(*) as event_count,
    COUNT(DISTINCT user_id) as unique_users,
    MIN(timestamp) as first_occurrence,
    MAX(timestamp) as last_occurrence
FROM security_events
WHERE timestamp >= NOW() - INTERVAL '30 days'
GROUP BY event_type, severity, result
ORDER BY event_count DESC;

CREATE OR REPLACE VIEW failed_auth_attempts_summary AS
SELECT 
    user_id,
    ip_address,
    auth_method,
    COUNT(*) as failed_attempts,
    MIN(timestamp) as first_failure,
    MAX(timestamp) as last_failure,
    array_agg(DISTINCT failure_code) as failure_codes
FROM auth_attempts
WHERE success = FALSE AND timestamp >= NOW() - INTERVAL '24 hours'
GROUP BY user_id, ip_address, auth_method
HAVING COUNT(*) >= 3
ORDER BY failed_attempts DESC;

CREATE OR REPLACE VIEW compliance_status_overview AS
SELECT 
    f.framework_id,
    f.name,
    f.compliance_status,
    f.compliance_score,
    COUNT(v.id) as open_violations,
    COUNT(p.id) as total_policies,
    f.last_assessment,
    f.next_assessment
FROM compliance_frameworks f
LEFT JOIN compliance_violations v ON f.framework_id = v.framework_id AND v.status = 'OPEN'
LEFT JOIN compliance_policies p ON f.framework_id = p.framework_id AND p.status = 'ACTIVE'
GROUP BY f.id, f.framework_id, f.name, f.compliance_status, f.compliance_score, f.last_assessment, f.next_assessment
ORDER BY f.name;

CREATE OR REPLACE VIEW high_risk_activities AS
SELECT 
    se.user_id,
    se.event_type,
    se.action,
    se.resource,
    se.timestamp,
    se.ip_address,
    se.risk_score,
    CASE 
        WHEN se.risk_score >= 80 THEN 'CRITICAL'
        WHEN se.risk_score >= 60 THEN 'HIGH'
        WHEN se.risk_score >= 40 THEN 'MEDIUM'
        ELSE 'LOW'
    END as risk_level
FROM security_events se
WHERE se.risk_score >= 40 AND se.timestamp >= NOW() - INTERVAL '7 days'
ORDER BY se.risk_score DESC, se.timestamp DESC;

-- Triggers for automatic updates
CREATE OR REPLACE FUNCTION update_modified_timestamp()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER update_vulnerability_findings_timestamp
    BEFORE UPDATE ON vulnerability_findings
    FOR EACH ROW
    EXECUTE FUNCTION update_modified_timestamp();

CREATE TRIGGER update_security_incidents_timestamp
    BEFORE UPDATE ON security_incidents
    FOR EACH ROW
    EXECUTE FUNCTION update_modified_timestamp();

CREATE TRIGGER update_compliance_frameworks_timestamp
    BEFORE UPDATE ON compliance_frameworks
    FOR EACH ROW
    EXECUTE FUNCTION update_modified_timestamp();

CREATE TRIGGER update_compliance_policies_timestamp
    BEFORE UPDATE ON compliance_policies
    FOR EACH ROW
    EXECUTE FUNCTION update_modified_timestamp();

CREATE TRIGGER update_compliance_violations_timestamp
    BEFORE UPDATE ON compliance_violations
    FOR EACH ROW
    EXECUTE FUNCTION update_modified_timestamp();

-- Function to calculate risk score based on event characteristics
CREATE OR REPLACE FUNCTION calculate_risk_score(
    p_event_type TEXT,
    p_result TEXT,
    p_user_id TEXT,
    p_ip_address INET,
    p_after_hours BOOLEAN DEFAULT FALSE
)
RETURNS INTEGER AS $$
DECLARE
    risk_score INTEGER := 0;
BEGIN
    -- Base risk by event type
    CASE p_event_type
        WHEN 'AUTH' THEN risk_score := risk_score + 20;
        WHEN 'AUTHZ' THEN risk_score := risk_score + 15;
        WHEN 'ENCRYPTION' THEN risk_score := risk_score + 10;
        WHEN 'SCAN' THEN risk_score := risk_score + 25;
        WHEN 'THREAT' THEN risk_score := risk_score + 50;
        ELSE risk_score := risk_score + 5;
    END CASE;
    
    -- Increase risk for failures
    IF p_result = 'FAILURE' THEN
        risk_score := risk_score + 30;
    END IF;
    
    -- Increase risk for privileged users
    IF p_user_id IN ('admin', 'root', 'administrator') THEN
        risk_score := risk_score + 20;
    END IF;
    
    -- Increase risk for after-hours activity
    IF p_after_hours THEN
        risk_score := risk_score + 15;
    END IF;
    
    -- Check for suspicious IP patterns (simplified)
    IF p_ip_address IS NOT NULL AND 
       NOT (p_ip_address <<= '192.168.0.0/16' OR 
            p_ip_address <<= '10.0.0.0/8' OR 
            p_ip_address <<= '172.16.0.0/12') THEN
        risk_score := risk_score + 25;
    END IF;
    
    RETURN LEAST(risk_score, 100);
END;
$$ LANGUAGE plpgsql;