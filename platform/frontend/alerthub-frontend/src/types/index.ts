// Alert Types
export interface Alert {
  id: string;
  title: string;
  description: string;
  severity: 'critical' | 'high' | 'medium' | 'low';
  status: 'open' | 'acknowledged' | 'investigating' | 'resolved';
  source: string;
  source_id?: string;
  source_url?: string;
  created_at: string;
  updated_at: string;
  acknowledged_at?: string;
  resolved_at?: string;
  assigned_to?: string;
  assigned_to_name?: string;
  acknowledged_by?: string;
  acknowledged_by_name?: string;
  resolved_by?: string;
  resolved_by_name?: string;
  tags: string[];
  labels: Record<string, string>;
  fingerprint?: string;
  // Correlation
  correlation_id?: string;
  correlation_score?: number;
  is_correlated?: boolean;
  similar_alerts?: string[];
  confidence_score?: number;
  // Incident linkage
  linked_incident_id?: string;
  // Occurrence tracking
  count?: number;
  first_seen_at?: string;
  last_seen_at?: string;
  // AI
  ai_confidence?: number;
  ai_classification?: string;
  // Resolution
  resolution_type?: string;
  // SLA
  sla_met_response_time?: boolean;
  sla_met_resolution_time?: boolean;
  // Maintenance
  maintenance_status?: number;
  is_alert_active?: boolean;
  // Autonomous AIOps
  agent_processed?: boolean;
  rca_confidence?: number;
  autonomous_analysis?: Record<string, any>;
  // Rich metadata
  metadata?: Record<string, any>;
  info?: string;
  message?: string;
  cluster?: string;
  namespace?: string;
  workload?: string;
  node?: string;
  host?: string;
  ip_address?: string;
  hostname?: string;
}

// Incident Types
export interface IncidentTimelineEvent {
  id: string;
  event_type: string;
  title: string;
  description?: string;
  user_name?: string;
  metadata?: Record<string, any>;
  created_at: string;
}

export interface Incident {
  id: string;
  incident_number: string;
  title: string;
  description: string;
  severity: 'critical' | 'high' | 'medium' | 'low';
  status: 'open' | 'investigating' | 'resolved';
  created_at: string;
  updated_at: string;
  started_at: string;
  detected_at?: string;
  acknowledged_at?: string;
  resolved_at?: string;
  assigned_to?: string;
  assigned_to_name?: string;
  affected_services: string;
  related_alerts: string[];
  alert_ids?: string[];
  auto_created?: boolean;
  correlation_method?: string;
  blast_radius?: string[] | Array<{ node_id: string; node_type: string; impact: string }>;
  topology_path?: string;
  // V2 Correlation Engine fields
  ontology_domain?: string;
  topo_root_entity_id?: string;
  causal_chain?: Array<{ from: string; to: string; type: string; weight: number }>;
  rca_hypotheses?: Array<{
    entity_id: string;
    entity_label: string;
    entity_type: string;
    confidence: number;
    evidence: Array<{ source: string; score: number; description: string }>;
    reasoning: string;
  }>;
  evolution_generation?: number;
  merge_source_ids?: string[];
}

// User Types
export interface User {
  id: string;
  username: string;
  email: string;
  full_name: string;
  role: string;
  is_active: boolean;
  last_login?: string;
  created_at: string;
  oauth_id?: string;
}

export interface Role {
  id: string;
  name: string;
  description: string;
  permissions: string[];
  is_system: boolean;
  created_at: string;
}

// Authentication Types
export interface AuthTokens {
  access_token: string;
  refresh_token: string;
  oauth_id_token?: string;
}

export interface LoginRequest {
  username: string;
  password: string;
  remember?: boolean;
}

export interface LoginResponse {
  success: boolean;
  message?: string;
  data?: {
    tokens: AuthTokens;
    user: User;
    oauth_id_token?: string;
  };
  oauth_source?: string;
}

// Integration Types
export interface Integration {
  id: string;
  name: string;
  type: 'monitoring' | 'notification' | 'ticketing';
  enabled: boolean;
  configuration: Record<string, any>;
  status: 'connected' | 'disconnected' | 'error';
  last_sync?: string;
}

// Settings Types
export interface SystemSettings {
  system_name: string;
  session_timeout: number;
  auto_refresh_interval: number;
  mfa_required: boolean;
}

export interface MonitoringConfig {
  dynatrace?: {
    enabled: boolean;
    url: string;
    token: string;
    env_id: string;
    import_problems: boolean;
    sync_status: boolean;
    import_metrics: boolean;
    create_events: boolean;
    sync_interval: number;
  };
  prometheus?: {
    enabled: boolean;
    url: string;
    alertmanager_url?: string;
    import_alerts: boolean;
    query_metrics: boolean;
    export_metrics: boolean;
    custom_rules?: string;
  };
  grafana?: {
    enabled: boolean;
    url: string;
    api_key: string;
    default_dashboard?: string;
    embed_dashboards: boolean;
    annotations: boolean;
    import_alerts: boolean;
  };
}

// API Response Types
export interface ApiResponse<T = any> {
  success: boolean;
  message?: string;
  data?: T;
  error?: string;
}

export interface PaginatedResponse<T> {
  success: boolean;
  data: {
    items: T[];
    total: number;
    page: number;
    limit: number;
    pages: number;
  };
}

// Filter and Search Types
export interface AlertFilters {
  severity?: string;
  status?: string;
  source?: string;
  time_range?: string;
  search?: string;
  tags?: string[];
  entity_type?: string;
  management_zone?: string;
  assigned?: string;
  correlated?: string;
  sort_by?: string;
  sort_order?: string;
}

export interface IncidentFilters {
  severity?: string;
  status?: string;
  assigned_to?: string;
  time_range?: string;
  search?: string;
}

// Chart and Analytics Types
export interface ChartData {
  name: string;
  value: number;
  timestamp?: string;
}

export interface StatsData {
  total_alerts: number;
  open_alerts: number;
  critical_alerts: number;
  total_incidents: number;
  open_incidents: number;
  mttr_hours: number;
  alerts_by_source: Record<string, number>;
  alerts_by_severity: Record<string, number>;
  incidents_by_severity: Record<string, number>;
  alert_trend: TrendPoint[];
  incident_trend: TrendPoint[];
  top_alert_sources: SourceMetric[];
  recent_activity: ActivityEvent[];
  ai_analysis_stats: Record<string, any>;
  notification_stats: Record<string, number>;
  active_maintenance_windows: number;
  oncall_engineers: number;
}

export interface TrendPoint {
  timestamp: string;
  value: number;
}

export interface SourceMetric {
  source: string;
  count: number;
}

export interface ActivityEvent {
  id: string;
  type: string;
  title: string;
  description: string;
  severity: string;
  timestamp: string;
}

// Theme Types
export type Theme = 'light' | 'dark' | 'system';

// Navigation Types
export interface NavItem {
  name: string;
  href: string;
  icon: React.ComponentType<any>;
  current?: boolean;
  badge?: number;
}

// WebSocket Types
export interface WebSocketMessage {
  type: 'alert' | 'incident' | 'notification' | 'stats_update';
  data: any;
  timestamp: string;
}

// AI Assistant Types
export interface AIMessage {
  id: string;
  role: 'user' | 'assistant';
  content: string;
  timestamp: string;
}

// Removed duplicate - using enhanced version below

// Alert Correlation Types
export interface CorrelationResult {
  alert_id: string;
  correlation_id: string;
  similar_alerts: string[];
  confidence_score: number;
  correlation_type: 'ml_similarity' | 'rule_based' | 'exact_duplicate' | 'time_window';
  recommended_action: 'create_incident' | 'merge_with_existing' | 'escalate' | 'suppress' | 'correlate';
  is_duplicate: boolean;
  duplicate_of?: string;
  created_at: string;
}

export interface SimilarityMetrics {
  title_similarity: number;
  description_similarity: number;
  source_similarity: number;
  tag_similarity: number;
  label_similarity: number;
  time_similarity: number;
  overall_similarity: number;
}

export interface CorrelationRule {
  id: string;
  name: string;
  description: string;
  conditions: RuleCondition[];
  actions: RuleAction[];
  priority: number;
  enabled: boolean;
  metadata: Record<string, any>;
  created_at: string;
  updated_at: string;
}

export interface RuleCondition {
  field: string;
  operator: 'equals' | 'contains' | 'starts_with' | 'regex' | 'in' | 'exists';
  value: any;
  weight: number;
}

export interface RuleAction {
  type: 'escalate' | 'correlate' | 'create_incident' | 'notify' | 'suppress_duplicates' | 'group';
  parameters: Record<string, any>;
}

export interface CorrelationCluster {
  id: string;
  cluster_id: string;
  name: string;
  description: string;
  alert_ids: string[];
  cluster_type: 'similarity' | 'rule_based' | 'manual';
  confidence_score: number;
  status: 'active' | 'resolved' | 'merged';
  root_alert_id?: string;
  incident_id?: string;
  created_at: string;
  updated_at: string;
}

// Topology and Service Mapping Types
export interface Service {
  id: string;
  name: string;
  display_name?: string;
  type: ServiceType;
  status: ServiceStatus;
  description: string;
  version?: string;
  environment: string;
  owner?: string;
  owner_email?: string;
  repository?: string;
  documentation?: string;
  runbook_url?: string;
  dashboard_url?: string;
  tags: string[];
  labels: Record<string, string>;
  metadata: Record<string, any>;
  health_endpoint?: string;
  dependencies?: ServiceDependency[];
  dependents?: ServiceDependency[];
  sla?: ServiceSLA;
  metrics?: ServiceMetrics;
  created_at: string;
  updated_at: string;
}

export type ServiceType = 'application' | 'database' | 'load_balancer' | 'cache' | 'queue' | 'storage' | 'network' | 'infrastructure' | 'api' | 'microservice';
export type ServiceStatus = 'healthy' | 'degraded' | 'down' | 'maintenance' | 'unknown';

export interface ServiceDependency {
  id: string;
  from_service_id: string;
  to_service_id: string;
  from_service_name: string;
  to_service_name: string;
  dependency_type: 'hard' | 'soft' | 'optional';
  description: string;
  protocol?: string;
  port?: number;
  endpoint?: string;
  health_endpoint?: string;
  timeout_seconds: number;
  circuit_breaker: boolean;
  created_at: string;
  updated_at: string;
}

export interface ServiceSLA {
  availability: number;
  response_time_ms: number;
  throughput_rps: number;
  error_rate_percent: number;
}

export interface ServiceMetrics {
  cpu_percent: number;
  memory_percent: number;
  disk_percent: number;
  network_mbps: number;
  response_time_ms: number;
  throughput_rps: number;
  error_rate_percent: number;
  availability_percent: number;
  last_updated: string;
}

export interface TopologyGraph {
  services: Record<string, Service>;
  dependencies: Record<string, ServiceDependency>;
  layers: Record<string, string[]>;
  metadata: Record<string, any>;
  generated_at: string;
}

export interface ImpactAnalysis {
  service_id: string;
  service_name: string;
  directly_affected: string[];
  indirectly_affected: string[];
  critical_path: string[];
  estimated_affected_users: number;
  estimated_revenue_impact: number;
  business_criticality: string;
  estimated_recovery_time: number;
  recommendations: string[];
  alternative_services: string[];
  metadata: Record<string, any>;
  analysis_time: string;
}

// Workflow Types
export interface Workflow {
  id: string;
  name: string;
  description: string;
  triggers: WorkflowTrigger[];
  steps: WorkflowStep[];
  enabled: boolean;
  metadata: Record<string, any>;
  created_by: string;
  created_at: string;
  updated_at: string;
  version: number;
  tags: string[];
}

export interface WorkflowTrigger {
  type: 'alert' | 'incident' | 'schedule' | 'webhook' | 'manual';
  conditions: TriggerCondition[];
  schedule?: ScheduleConfig;
  metadata: Record<string, any>;
}

export interface TriggerCondition {
  field: string;
  operator: string;
  value: any;
}

export interface ScheduleConfig {
  type: 'cron' | 'interval' | 'once';
  value: string;
  timezone: string;
}

export interface WorkflowStep {
  id: string;
  name: string;
  type: 'action' | 'condition' | 'parallel' | 'sequential';
  action?: WorkflowAction;
  condition?: StepCondition;
  steps?: WorkflowStep[];
  on_success?: string;
  on_failure?: string;
  retry?: RetryConfig;
  timeout?: number;
  depends_on?: string[];
  metadata: Record<string, any>;
}

export interface WorkflowAction {
  type: string;
  parameters: Record<string, any>;
  outputs?: Record<string, string>;
}

export interface StepCondition {
  expression: string;
  variables: Record<string, any>;
}

export interface RetryConfig {
  max_attempts: number;
  delay: number;
  backoff_type: 'fixed' | 'exponential' | 'linear';
}

export interface WorkflowExecution {
  id: string;
  workflow_id: string;
  status: 'running' | 'completed' | 'failed' | 'cancelled';
  trigger_event: Record<string, any>;
  context: Record<string, any>;
  step_results: Record<string, StepResult>;
  started_at: string;
  completed_at?: string;
  error?: string;
  logs: ExecutionLog[];
}

export interface StepResult {
  status: 'success' | 'failure' | 'skipped';
  output: Record<string, any>;
  error?: string;
  started_at: string;
  completed_at: string;
  duration: number;
  attempts: number;
}

export interface ExecutionLog {
  timestamp: string;
  level: 'info' | 'warn' | 'error' | 'debug';
  message: string;
  step_id?: string;
  data?: Record<string, any>;
}

// Provider Integration Types
export interface Provider {
  id: string;
  name: string;
  type: ProviderType;
  version: string;
  description: string;
  capabilities: ProviderCapability[];
  config_schema: Record<string, ConfigField>;
  status: ProviderStatus;
  enabled: boolean;
  last_sync?: string;
  metrics?: ProviderMetrics;
}

export type ProviderType = 'monitoring' | 'database' | 'communication' | 'incident_management' | 'ticketing' | 'cloud_provider' | 'data_warehouse' | 'ai';
export type ProviderCapability = 'pull' | 'push' | 'bidirection' | 'query' | 'webhook' | 'stream' | 'notify' | 'execute';
export type ProviderStatus = 'active' | 'inactive' | 'error' | 'configuring';

export interface ConfigField {
  type: 'string' | 'number' | 'boolean' | 'array' | 'object';
  required: boolean;
  default?: any;
  description: string;
  validation?: string;
  sensitive?: boolean;
  options?: string[];
}

export interface ProviderMetrics {
  request_count: number;
  error_count: number;
  success_rate: number;
  avg_response_time: number;
  last_request?: string;
  last_error?: string;
  data_points: number;
}

// Enhanced Incident Types
export interface EnhancedIncident {
  id: string;
  incident_number: number;
  title: string;
  description: string;
  severity: 'critical' | 'high' | 'medium' | 'low';
  status: 'open' | 'investigating' | 'resolved' | 'closed';
  priority: string;
  impact: string;
  urgency: string;
  created_at: string;
  updated_at: string;
  started_at: string;
  assigned_to?: string;
  assigned_to_name?: string;
  created_by?: string;
  created_by_name?: string;
  
  // Enhanced AIOps fields
  correlation_id?: string;
  affected_services?: string[];
  similar_incidents?: SimilarIncidentReference[];
  business_impact?: BusinessImpact;
  impact_analysis?: ImpactAnalysis;
  ai_root_cause?: string;
  ai_recommendations?: string[];
  auto_assignment?: AutoAssignmentResult;
  escalation_policy?: EscalationPolicy;
  workflow_executions?: string[];
  communication_plan?: CommunicationPlan;
  
  detected_at?: string;
  acknowledged_at?: string;
  resolved_at?: string;
  closed_at?: string;
}

export interface BusinessImpact {
  level: 'low' | 'medium' | 'high' | 'critical';
  estimated_revenue_loss: number;
  affected_users: number;
  affected_transactions: number;
  sla_breaches: string[];
  compliance_impact: string[];
  reputation_risk: string;
}

export interface AutoAssignmentResult {
  assigned_to: string;
  assignment_reason: string;
  confidence: number;
  alternative_users: string[];
}

export interface EscalationPolicy {
  id: string;
  name: string;
  rules: EscalationRule[];
  enabled: boolean;
  last_updated: string;
}

export interface EscalationRule {
  level: number;
  condition: 'time_elapsed' | 'no_acknowledgment' | 'severity_change';
  threshold: number;
  actions: string[];
  recipients: string[];
  channels: string[];
}

export interface CommunicationPlan {
  stakeholders: Stakeholder[];
  communications: PlannedCommunication[];
  status_page_updates: boolean;
  social_media_plan?: SocialMediaPlan;
}

export interface Stakeholder {
  user_id: string;
  name: string;
  role: string;
  contact_info: string;
  notify_level: 'immediate' | 'hourly' | 'resolution';
}

export interface PlannedCommunication {
  type: 'initial' | 'update' | 'resolution' | 'postmortem';
  recipients: string[];
  template: string;
  scheduled_at: string;
  sent_at?: string;
}

export interface SocialMediaPlan {
  enabled: boolean;
  platforms: string[];
  template: string;
  approved: boolean;
}

export interface SimilarIncidentReference {
  incident_id: string;
  title: string;
  similarity_score: number;
  resolution: string;
  resolution_time: number;
  lessons_learned: string[];
}

// Pattern and Anomaly Detection Types
export interface AlertPattern {
  pattern_id: string;
  type: string;
  frequency: number;
  alert_ids: string[];
  description: string;
  confidence: number;
  detected_at: string;
}

export interface Anomaly {
  type: string;
  severity: string;
  description: string;
  detected_at: string;
  score: number;
  affected_metrics?: string[];
}

// AI Analysis Types
export interface AIAnalysis {
  classification: string;
  confidence: number;
  root_cause?: string;
  recommendations?: string[];
  severity_prediction?: string;
  resolution_suggestion?: string;
  business_impact?: BusinessImpact;
  similar_incidents?: SimilarIncidentReference[];
}

// AI Provider Types
export type AIProvider = 'openai' | 'anthropic' | 'google' | 'azure' | 'ollama' | 'deepseek' | 'grok' | 'llamacpp';

export interface AIConfig {
  provider: AIProvider;
  api_key: string;
  base_url: string;
  model: string;
  max_tokens: number;
  temperature: number;
  timeout: number;
  enabled: boolean;
}

// Real-time Updates and WebSocket Types
export interface RealTimeUpdate {
  type: 'alert' | 'incident' | 'correlation' | 'workflow' | 'topology' | 'ai_analysis';
  action: 'created' | 'updated' | 'resolved' | 'correlated' | 'analyzed' | 'escalated';
  data: any;
  timestamp: string;
  user_id?: string;
  metadata?: Record<string, any>;
}

// Dashboard Enhancement Types
export interface AIOpsMetrics {
  correlation_accuracy: number;
  automation_rate: number;
  ai_analysis_success_rate: number;
  pattern_detection_count: number;
  anomaly_detection_count: number;
  workflow_execution_rate: number;
  provider_health_score: number;
  mttr_improvement: number;
}

// Advanced Filtering Types
export interface AdvancedFilters {
  correlation_confidence?: number;
  ai_analyzed?: boolean;
  workflow_automated?: boolean;
  business_impact?: string;
  similar_incidents?: boolean;
  affected_services?: string[];
  time_range?: {
    start: string;
    end: string;
  };
}