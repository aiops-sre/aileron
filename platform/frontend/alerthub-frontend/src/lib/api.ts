import axios from 'axios'
import { useEnhancedAuthStore } from '@/stores/enhancedAuthStore'

const api = axios.create({
  baseURL: '/api/v1',
  headers: {
    'Content-Type': 'application/json',
  },
  timeout: 180000, // 3 min — AI chat (Floodgate LLM) can take 60-90s for longer responses
})

// Request interceptor — attach JWT and Floodgate token
api.interceptors.request.use(
  (config) => {
    const token = sessionStorage.getItem('access_token')
    if (token) {
      config.headers.Authorization = `Bearer ${token}`
    }
    // Forward Floodgate/IdMS token for AI endpoints (sessionStorage only — never localStorage)
    const floodgateToken =
      sessionStorage.getItem('floodgate_token') ||
      sessionStorage.getItem('oauth_id_token')
    if (floodgateToken) {
      config.headers['X-Floodgate-Token'] = floodgateToken
    }
    return config
  },
  (error) => Promise.reject(error)
)

// Response interceptor — on 401 clear tokens and re-run OAuth2 flow
api.interceptors.response.use(
  (response) => response,
  async (error) => {
    const { response, config } = error

    if (response?.status === 401) {
      // Avoid redirect loops on the auth endpoints themselves
      const isAuthEndpoint = (config?.url as string | undefined)?.startsWith('/auth/')
      if (!isAuthEndpoint) {
        useEnhancedAuthStore.getState().logout() // clears localStorage + redirects to OAuth2
      }
    } else if (response?.status === 404) {
      console.warn(`⚠️ API endpoint not found: ${config?.url}`)
    } else if (response?.status >= 500) {
      console.error(`🚨 Server Error ${response.status}:`, {
        url: config?.url,
        method: config?.method,
        data: response?.data,
      })
    }

    return Promise.reject(error)
  }
)

export default api

// ✅ ALERTS API (MATCHING ACTUAL BACKEND - alerts.go) 
export const alertsApi = {
  list: (params?: any) => api.get('/alerts', { params }),
  get: (id: string) => api.get(`/alerts/${id}`),
  acknowledge: (id: string) => api.post(`/alerts/${id}/acknowledge`),
  resolve: (id: string, data: any) => api.post(`/alerts/${id}/resolve`, data),
  assign: (id: string, data: any) => api.post(`/alerts/${id}/assign`, data),
  delete: (id: string) => api.delete(`/alerts/${id}`),
  
  // Batch operations (✅ EXISTS in backend)
  bulkAcknowledge: (alertIds: string[]) => api.post('/alerts/bulk/acknowledge', { alert_ids: alertIds }),
  bulkResolve: (alertIds: string[], notes: string) => api.post('/alerts/bulk/resolve', { alert_ids: alertIds, notes }),
  
  // Maintenance windows (✅ EXISTS in backend)
  startMaintenance: (id: string, data: any) => api.post(`/alerts/${id}/maintenance/start`, data),
  endMaintenance: (windowId: string) => api.post(`/alerts/maintenance/${windowId}/end`),
  getMaintenanceWindows: (id: string) => api.get(`/alerts/${id}/maintenance`),
  getActiveMaintenanceWindows: () => api.get('/alerts/maintenance/active'),
  
  // Comments (✅ EXISTS in backend)
  getComments: (id: string) => api.get(`/alerts/${id}/comments`),
  addComment: (id: string, data: { comment: string }) => api.post(`/alerts/${id}/comments`, data),
  deleteComment: (id: string, commentId: string) => api.delete(`/alerts/${id}/comments/${commentId}`),
  
  // NEW: Autonomous AIOps endpoints (✅ ADDED to backend)
  triggerRCA: (id: string) => api.post(`/alerts/${id}/trigger-rca`),
  triggerAutoRemediation: (id: string) => api.post(`/alerts/${id}/auto-remediate`),
  getAutonomousAnalysis: (id: string) => api.get(`/alerts/${id}/autonomous-analysis`),
  processAutonomously: (id: string) => api.post(`/alerts/${id}/process-autonomous`),
  getAutonomousStats: () => api.get('/alerts/autonomous/stats'),
  getCounts: () => api.get('/alerts/counts'),
}

// ✅ INCIDENTS API (MATCHING ACTUAL BACKEND - incidents.go)
export const incidentsApi = {
  // Core CRUD operations (✅ EXISTS in backend)
  list: (params?: any) => api.get('/incidents', { params }),
  get: (id: string) => api.get(`/incidents/${id}`),
  create: (data: any) => api.post('/incidents', data),
  update: (id: string, data: any) => api.put(`/incidents/${id}`, data),
  resolve: (id: string, data: any) => api.post(`/incidents/${id}/resolve`, data),
  assign: (id: string, data: { assign_to: string }) => api.post(`/incidents/${id}/assign`, data),
  
  // Timeline management (✅ EXISTS in backend)
  getTimeline: (id: string) => api.get(`/incidents/${id}/timeline`),
  addTimelineEvent: (id: string, data: {
    event_type: string;
    title: string;
    description?: string;
    metadata?: any;
  }) => api.post(`/incidents/${id}/timeline`, data),
  
  // AI and RCA (✅ EXISTS in backend)
  performRCA: (id: string) => api.post(`/incidents/${id}/rca`),
  floodgateRCA: (id: string, data: { model: string; token: string }) =>
    api.post(`/incidents/${id}/rca/floodgate`, data),
  testFloodgateToken: (token: string) =>
    api.post('/incidents/floodgate-token-test', { token }),
  getStats: () => api.get('/incidents/stats'),
  getAlerts: (id: string) => api.get(`/incidents/${id}/alerts`),

  // RCA decisions from CACIE (hypothesis ranking + evidence sources)
  getRCADecisions: (id: string) => api.get(`/incidents/${id}/rca-decisions`),
  // OKG change intelligence near the incident
  getChanges: (id: string, lookbackMinutes = 120) =>
    api.get(`/incidents/${id}/changes`, { params: { lookback_minutes: lookbackMinutes } }),

  // Postmortem (auto-generated on incident resolution — Aurora pattern)
  getPostmortem: (id: string) => api.get(`/incidents/${id}/postmortem`),
  generatePostmortem: (id: string) => api.post(`/incidents/${id}/postmortem/generate`),

  // Gate hooks — remediations_pending (Sympozium pattern)
  getRemediations: (id: string) => api.get(`/incidents/${id}/remediations`),
  proposeRemediation: (id: string, data: { proposed_action: string; action_type?: string; risk_level?: string; proposed_by?: string }) =>
    api.post(`/incidents/${id}/remediations`, data),
  approveRemediation: (incidentId: string, remediationId: string) =>
    api.post(`/incidents/${incidentId}/remediations/${remediationId}/approve`),
  rejectRemediation: (incidentId: string, remediationId: string, reason?: string) =>
    api.post(`/incidents/${incidentId}/remediations/${remediationId}/reject`, { reason }),
  
  // HCL Kentaurus integration (✅ EXISTS - keep existing)
  createHCLIncident: (data: {
    title: string
    description: string
    impact?: string
    urgency?: string
    priority?: string
    configuration?: string
    assignmentGroupId?: string
  }) => api.post('/incidents/hcl/create', {
    module: 'incident',
    callingApp: '928952',
    configuration: data.configuration || 'Interactive-Infra-ADC',
    impact: data.impact || '1 - High',
    urgency: data.urgency || '3 - Low',
    priority: data.priority || '3 - Low',
    assignmentGroupId: data.assignmentGroupId || '13283967',
    title: data.title,
    description: data.description,
  }),
  
  listHCLIncidents: (params?: any) => api.post('/incidents/hcl/query', {
    module: 'incident',
    number: '',
    query: params?.query || '',
    count: params?.count || 100,
    offset: params?.offset || 0,
    sortOrder: params?.sortOrder || 'desc',
    sortByField: 'lastUpdatedOn',
  }),
  
  getHCLIncident: (number: string) => api.get(`/incidents/hcl/${number}`),
  updateHCLIncident: (data: any) => api.put(`/incidents/hcl/${data.ticketId}`, data),
  reopenHCLIncident: (data: any) => api.post(`/incidents/hcl/${data.number}/reopen`, data),
}

// ✅ WORKFLOWS API (MATCHING ACTUAL BACKEND - workflows.go)
export const workflowApi = {
  // Core CRUD (✅ EXISTS in backend)
  listWorkflows: (params?: any) => api.get('/workflows', { params }),
  getWorkflow: (id: string) => api.get(`/workflows/${id}`),
  createWorkflow: (data: {
    name: string;
    description?: string;
    triggers: any[];
    steps: any[];
    enabled?: boolean;
    tags?: string[];
    metadata?: any;
  }) => api.post('/workflows', data),
  updateWorkflow: (id: string, data: any) => api.put(`/workflows/${id}`, data),
  deleteWorkflow: (id: string) => api.delete(`/workflows/${id}`),
  
  // Execution (✅ EXISTS in backend)
  executeWorkflow: (id: string, data?: { trigger_event?: any }) =>
    api.post(`/workflows/${id}/execute`, data),
  enableWorkflow: (id: string) => api.post(`/workflows/${id}/enable`),
  disableWorkflow: (id: string) => api.post(`/workflows/${id}/disable`),
  
  // Execution history (✅ EXISTS in backend)
  listExecutions: (workflowId: string, params?: any) =>
    api.get(`/workflows/${workflowId}/executions`, { params }),
  listWorkflowExecutions: (workflowId: string, params?: any) =>
    api.get(`/workflows/${workflowId}/executions`, { params }),  // Alias for compatibility
  getExecution: (workflowId: string, executionId: string) =>
    api.get(`/workflows/${workflowId}/executions/${executionId}`),
  cancelExecution: (workflowId: string, executionId: string) =>
    api.post(`/workflows/${workflowId}/executions/${executionId}/cancel`),
  
  // Templates (✅ EXISTS in backend)
  listTemplates: (params?: any) => api.get('/workflows/templates', { params }),
  listWorkflowTemplates: (params?: any) => api.get('/workflows/templates', { params }),  // Alias for compatibility
  getTemplate: (id: string) => api.get(`/workflows/templates/${id}`),
  createFromTemplate: (templateId: string, data: any) =>
    api.post(`/workflows/templates/${templateId}/create`, data),
}

// ✅ ANALYTICS API (MATCHING ACTUAL BACKEND - analytics.go)
export const analyticsApi = {
  // Core analytics (✅ EXISTS in backend)
  getDashboardMetrics: () => api.get('/analytics/dashboard'),
  getAlertAnalytics: (params?: { time_range?: string }) => api.get('/analytics/alerts', { params }),
  getIncidentAnalytics: (params?: { time_range?: string }) => api.get('/analytics/incidents', { params }),
  getMetrics: () => api.get('/analytics/metrics'),
  
  // Reports (✅ EXISTS in backend)
  generateReport: (data: {
    report_type: string;
    start_date: string;
    end_date: string;
  }) => api.post('/analytics/reports', data),
  
  // Legacy dashboard endpoint (✅ EXISTS in backend)
  getStats: () => api.get('/dashboard/stats'),
}

// ✅ AI API (MATCHING ACTUAL BACKEND - ai.go)
export const aiApi = {
  // Core AI endpoints (✅ EXISTS in backend)
  chat: (data: {
    model: string
    messages: Array<{ role: string; content: string }>
    session_id?: string
    max_tokens?: number
  }) => api.post('/ai/chat', data),
  
  // AI session management (✅ EXISTS in backend)
  listSessions: () => api.get('/ai/sessions'),
  getSession: (sessionId: string) => api.get(`/ai/sessions/${sessionId}`),
  getSessionMessages: (sessionId: string) => api.get(`/ai/sessions/${sessionId}/messages`),
  deleteSession: (sessionId: string) => api.delete(`/ai/sessions/${sessionId}`),
  
  // AI tools and capabilities (✅ EXISTS in backend)
  listTools: () => api.get('/ai/tools'),
  executeTool: (data: { tool: string; params?: Record<string, any> }) =>
    api.post('/ai/tools/execute', data),
  getCapabilities: () => api.get('/ai/capabilities'),
  
  // AI analysis (✅ EXISTS in backend - GET not POST)
  analyzeAlert: (alertId: string) => api.get(`/ai/analyze/${alertId}`),
  
  // AI health (✅ EXISTS in backend)
  healthCheck: () => api.get('/ai/health'),
  
  // Additional methods for frontend components
  listModels: () => api.get('/ai/models'),
  analyzeFatigue: (params?: { hours?: number }) => api.get('/ai/fatigue', { params }),
  getAlertClusters: (params?: { hours?: number }) => api.get('/ai/clusters', { params }),
}

// ✅ CORRELATION API (MATCHING ACTUAL BACKEND - enhanced_correlation.go)
export const correlationApi = {
  // AI correlation (✅ EXISTS in backend - GET /correlation/alert/:id, POST /correlation/ai/alert/:id/correlate)
  getAICorrelationForAlert: (alertId: string) => api.get(`/correlation/alert/${alertId}`),
  correlateAlert: (alertId: string, data: any) => api.post(`/correlation/ai/alert/${alertId}/correlate`, data),

  // Additional methods used by frontend components
  getAlertCorrelation: (alertId: string) => api.get(`/correlation/alert/${alertId}`),
  listCorrelationClusters: (params?: any) => api.get('/correlation/ai/analytics'),

  // Pipeline results — persisted correlation decisions from the 4-strategy engine
  getPipelineResults: (params?: { limit?: number; hours?: number; decision?: string; source?: string }) =>
    api.get('/correlation/pipeline/results', { params }),

  // Feedback loop
  recordFeedback: (data: {
    alert_id: string
    incident_id?: string
    feedback_type: 'confirmed' | 'false_positive' | 'missed_correlation' | 'split'
    dominant_strategy?: string
    notes?: string
  }) => api.post('/correlation/feedback', data),
  getFeedbackStats: () => api.get('/correlation/feedback/stats'),
  getFeedbackWeightHistory: () => api.get('/correlation/feedback/weights/history'),
  forceRecalibrate: () => api.post('/correlation/feedback/recalibrate', {}),
}

// ✅ AIOPS API
export const aiopsApi = {
  getDashboard: (params?: { hours?: number }) => api.get('/aiops/dashboard', { params }),
  getPipelineResults: (params?: { limit?: number; hours?: number; decision?: string; source?: string }) =>
    api.get('/correlation/pipeline/results', { params }),
  processMonitored: () => api.post('/correlation/pipeline/process-monitored'),
}

// ✅ PLATFORM HEALTH API
export const platformHealthApi = {
  getIntegrationHealth: () => api.get('/integrations/health'),
}

// ✅ USER MANAGEMENT API (MATCHING ACTUAL BACKEND - users.go)  
export const usersApi = {
  list: (params?: any) => api.get('/users', { params }),
  get: (id: string) => api.get(`/users/${id}`),
  create: (data: {
    username: string;
    email: string;
    full_name: string;
    password: string;
    role_id: string;
    phone?: string;
    timezone?: string;
    is_active?: boolean;
  }) => api.post('/users', data),
  update: (id: string, data: any) => api.put(`/users/${id}`, data),
  delete: (id: string) => api.delete(`/users/${id}`),
  getPermissions: (id: string) => api.get(`/users/${id}/permissions`),
  checkPermission: (id: string, permission: string) =>
    api.get(`/users/${id}/check-permission?permission=${permission}`),
  getSettings: () => api.get('/users/settings'),
  updateSettings: (settings: any) => api.put('/users/settings', { settings }),
}

// ✅ ROLES API (MATCHING ACTUAL BACKEND - roles.go)
export const rolesApi = {
  list: () => api.get('/roles'),
  get: (id: string) => api.get(`/roles/${id}`),
  create: (data: { name: string; description?: string }) => api.post('/roles', data),
  update: (id: string, data: { name: string; description?: string }) => api.put(`/roles/${id}`, data),
  delete: (id: string) => api.delete(`/roles/${id}`),
  getPermissions: (id: string) => api.get(`/roles/${id}/permissions`),
  assignPermissions: (id: string, permissionIds: string[]) =>
    api.post(`/roles/${id}/permissions`, { permission_ids: permissionIds }),
  removePermission: (id: string, permissionId: string) =>
    api.delete(`/roles/${id}/permissions/${permissionId}`),
  assignRoleToUser: (userId: string, roleName: string) =>
    api.post(`/users/${userId}/role`, { role_name: roleName }),
}

// ✅ PERMISSIONS API
export const permissionsApi = {
  list: () => api.get('/permissions'),
  create: (data: { name: string; resource: string; action: string; description?: string }) =>
    api.post('/permissions', data),
  delete: (id: string) => api.delete(`/permissions/${id}`),
}

// ✅ LDAP GROUP MAPPING API (DS-LDAP group → role mappings, live without restart)
export const ldapMappingsApi = {
  list: () => api.get('/ldap-mappings'),
  upsert: (data: { ldap_group: string; role_id: string }) => api.post('/ldap-mappings', data),
  delete: (id: string) => api.delete(`/ldap-mappings/${id}`),
}

// ✅ AUDIT API (MATCHING ACTUAL BACKEND)
export const auditApi = {
  getLogs: (params?: { limit?: number; page?: number }) => api.get('/audit/logs', { params }),
}

// ✅ INTEGRATIONS API (MATCHING ACTUAL BACKEND - integrations.go)
export const integrationsApi = {
  list: () => api.get('/integrations'),
  get: (id: string) => api.get(`/integrations/${id}`),
  create: (data: {
    name: string;
    type: string;
    enabled?: boolean;
    config?: any;
  }) => api.post('/integrations', data),
  update: (id: string, data: any) => api.put(`/integrations/${id}`, data),
  delete: (id: string) => api.delete(`/integrations/${id}`),
  toggle: (id: string, enabled: boolean) => api.put(`/integrations/${id}/toggle`, { enabled }),
  test: (id: string) => api.post(`/integrations/${id}/test`),
  sync: (id: string) => api.post(`/integrations/${id}/sync`),
  
  // Monitoring integrations (✅ EXISTS in backend)
  monitoring: {
    list: () => api.get('/integrations/monitoring'),
    configure: (type: string, data: any) => api.post(`/integrations/monitoring/${type}`, data),
    test: (type: string) => api.post(`/integrations/monitoring/${type}/test`),
  },
  
  // Notification integrations (✅ EXISTS in backend)
  notifications: {
    list: () => api.get('/integrations/notifications'),
    configure: (type: string, data: any) => api.post(`/integrations/notifications/${type}`, data),
    send: (type: string, data: any) => api.post(`/integrations/notifications/${type}/send`, data),
  },
  
  // Ticketing integrations (✅ EXISTS in backend)
  ticketing: {
    list: () => api.get('/integrations/ticketing'),
    configure: (type: string, data: any) => api.post(`/integrations/ticketing/${type}`, data),
    createTicket: (type: string, data: any) => api.post(`/integrations/ticketing/${type}/tickets`, data),
  },
}

// ✅ NOTIFICATION API (MATCHING ACTUAL BACKEND - notifications.go)
export const notificationsApi = {
  listChannels: () => api.get('/notifications/channels'),
  createChannel: (data: any) => api.post('/notifications/channels', data),
  testChannel: (id: string) => api.post(`/notifications/channels/${id}/test`),
}

// ✅ TOPOLOGY API (MATCHING ACTUAL BACKEND - topology_handlers.go)
export const topologyApi = {
  // Infrastructure topology (✅ EXISTS in backend)
  getInfrastructureGraph: () => api.get('/topology/complete'),
  getInfrastructureNodes: (params?: any) => api.get('/topology/complete'),
  refreshInfrastructure: () => api.post('/topology/discover'),
  discoverInfrastructure: () => api.post('/topology/discover'),

  // Service topology (✅ EXISTS in backend - note: /services not /service)
  getServiceGraph: () => api.get('/topology/services/graph'),
  getServiceNodes: (params?: any) => api.get('/topology/services/graph'),
  refreshServices: () => api.post('/topology/discover'),

  // Enhanced topology config (✅ EXISTS - /topology/config, no PUT)
  getEnhancedConfig: () => api.get('/topology/config'),
  updateEnhancedConfig: (data: any) => api.post('/topology/config', data),

  // K8s topology (✅ EXISTS in backend - /topology/k8s-clusters not /k8s/clusters)
  listK8sClusters: () => api.get('/topology/k8s-clusters'),
  addK8sCluster: (data: {
    name: string;
    environment: string;
    region: string;
    api_server_url: string;
    service_account_token: string;
    ca_cert_data?: string;
  }) => api.post('/topology/k8s-clusters', data),
  removeK8sCluster: (clusterName: string) => api.delete(`/topology/k8s-clusters/${clusterName}`),
  discoverK8sCluster: (clusterName: string) => api.post(`/topology/discovery/${clusterName}`),

  // Additional methods for frontend components
  getTopologyGraph: () => api.get('/topology/complete'),
  discoverServices: () => api.post('/topology/discover'),
}

// Dashboard API (for legacy compatibility)
export const dashboardApi = {
  // '/dashboard/stats' does not exist — use real analytics/dashboard endpoint
  getStats: () => api.get('/analytics/dashboard'),
}

// Providers API (for component compatibility)
export const providersApi = {
  listProviders: () => api.get('/integrations/auth-providers'),
  getProvider: (id: string) => api.get(`/integrations/auth-providers/${id}`),
}

// ✅ OAUTH API (MATCHING ACTUAL BACKEND - oauth.go, oauth_handlers.go)
export const oauthApi = {
  // Corporate OAuth (✅ EXISTS in backend - /oauth/token and /oauth/refresh)
  getCorporateTokens: () => api.get('/oauth/corporate/tokens'),
  refreshCorporateToken: () => api.post('/oauth/refresh'),

  // Floodgate integration (✅ EXISTS in backend)
  getFloodgateModels: () => api.get('/oauth/floodgate/models'),
  proxyFloodgateRequest: (data: {
    endpoint: string;
    method: string;
    payload?: any;
  }) => api.post('/oauth/floodgate/proxy', data),

  // Generic OAuth2 providers (✅ EXISTS in backend - public list, admin create)
  listOAuth2Providers: () => api.get('/auth/oauth2/providers'),
  createOAuth2Provider: (data: any) => api.post('/admin/oauth2/providers', data),

  // IDMS authentication
  getIDMSSettings: () => api.get('/auth/oidc/settings'),
  getIDMSGroups: () => api.get('/auth/oidc/groups'),
  checkIDMSPermission: (data: { permission: string }) => api.post('/auth/oidc/check-permission', data),
}

// ✅ WEBSOCKET API (MATCHING ACTUAL BACKEND - websocket.go)
export const websocketApi = {
  connect: (endpoint: string) => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const wsUrl = `${protocol}//${window.location.host}/api/v1/ws/${endpoint}`
    return new WebSocket(wsUrl)
  },
  
  // Real-time subscriptions (✅ EXISTS in backend)
  subscribeToAlerts: () => websocketApi.connect('alerts'),
  subscribeToIncidents: () => websocketApi.connect('incidents'),
  subscribeToHealth: () => websocketApi.connect('health'),
}

// ✅ DEDUPLICATION API (MATCHING ACTUAL BACKEND - deduplication.go)
export const deduplicationApi = {
  getRules: () => api.get('/deduplication/rules'),
  createRule: (data: any) => api.post('/deduplication/rules', data),
  updateRule: (id: string, data: any) => api.put(`/deduplication/rules/${id}`, data),
  deleteRule: (id: string) => api.delete(`/deduplication/rules/${id}`),
  getStats: () => api.get('/deduplication/stats'),
  processAlerts: () => api.post('/deduplication/process'),
}

// ✅ AUTHENTICATION API (MATCHING ACTUAL BACKEND - auth.go)
export const authApi = {
  login: (credentials: { username: string; password: string }) => api.post('/auth/login', credentials),
  logout: () => api.post('/auth/logout'),
  me: () => api.get('/auth/me'),
  refreshToken: () => api.post('/auth/refresh'),
}

// ✅ CONFIG API (MATCHING ACTUAL BACKEND - config.go)
export const configApi = {
  getSAMLConfig: () => api.get('/config/saml'),  // Public endpoint
  getSystemConfig: () => api.get('/system/config'),
  updateSystemConfig: (data: any) => api.put('/system/config', data),
  testSAMLConfig: () => api.post('/config/saml/test'),
}

// ✅ SETTINGS API (from user settings)
export const settingsApi = {
  get: () => api.get('/users/settings'),
  update: (settings: any) => api.put('/users/settings', { settings }),
}

// Separate axios instance for LLM text generation (ollama on CPU can take 60-120s)
const llmApi = axios.create({
  baseURL: '/api/v1',
  headers: { 'Content-Type': 'application/json' },
  timeout: 180000,
})
llmApi.interceptors.request.use((config) => {
  const token = sessionStorage.getItem('access_token')
  if (token) config.headers.Authorization = `Bearer ${token}`
  return config
}, (error) => Promise.reject(error))

// ✅ ADMIN LLM API (MATCHING ACTUAL BACKEND - llm_infra_handlers.go)
export const adminLLMApi = {
  getConfigs: () => api.get('/admin/llm/configs'),
  createConfig: (data: any) => api.post('/admin/llm/configs', data),
  updateConfig: (id: string, data: any) => api.put(`/admin/llm/configs/${id}`, data),
  deleteConfig: (id: string) => api.delete(`/admin/llm/configs/${id}`),
  testConfig: (data: any) => api.post('/admin/llm/test', data),
  status: () => api.get('/admin/llm/status'),
  query: (data: { query: string; alert_id?: string; context?: string; model?: string }) => llmApi.post('/admin/llm/query', data),
}

// ✅ ADMIN ALERT SOURCES API (MATCHING ACTUAL BACKEND - llm_infra_handlers.go)
export const adminAlertSourcesApi = {
  list: () => api.get('/admin/alert-sources'),
  create: (data: any) => api.post('/admin/alert-sources', data),
  update: (id: string, data: any) => api.put(`/admin/alert-sources/${id}`, data),
  delete: (id: string) => api.delete(`/admin/alert-sources/${id}`),
}

// ✅ ADMIN INFRA API (MATCHING ACTUAL BACKEND - llm_infra_handlers.go)
export const adminInfraApi = {
  getRegions: () => api.get('/admin/infra/regions'),
  createRegion: (data: any) => api.post('/admin/infra/regions', data),
  getCloudStack: () => api.get('/admin/infra/cloudstack'),
  createCloudStack: (data: any) => api.post('/admin/infra/cloudstack', data),
  updateCloudStack: (id: string, data: any) => api.put(`/admin/infra/cloudstack/${id}`, data),
  deleteCloudStack: (id: string) => api.delete(`/admin/infra/cloudstack/${id}`),
  getNetApp: () => api.get('/admin/infra/netapp'),
  createNetApp: (data: any) => api.post('/admin/infra/netapp', data),
  updateNetApp: (id: string, data: any) => api.put(`/admin/infra/netapp/${id}`, data),
}

// ✅ ADMIN CORRELATION API (MATCHING ACTUAL BACKEND - llm_infra_handlers.go)
export const adminCorrelationApi = {
  getConfig: () => api.get('/admin/correlation/config'),
  updateConfig: (key: string, value: any) => api.put('/admin/correlation/config', { key, value }),
}

// Intelligence Operations API
export const intelligenceApi = {
  getStats: () => api.get('/intelligence/stats'),
  getRecentRemediations: (status?: string) =>
    api.get('/intelligence/remediations', { params: status ? { status } : {} }),
  // Policy engine
  listPolicies: () => api.get('/intelligence-policies'),
  createPolicy: (data: any) => api.post('/intelligence-policies', data),
  togglePolicy: (id: string) => api.patch(`/intelligence-policies/${id}/toggle`),
  deletePolicy: (id: string) => api.delete(`/intelligence-policies/${id}`),
  evaluatePolicy: (data: any) => api.post('/intelligence-policies/evaluate', data),
  // Runbook catalog
  listRunbooks: (params?: { domain?: string; entity_type?: string }) =>
    api.get('/runbooks', { params }),
  createRunbook: (data: any) => api.post('/runbooks', data),
  deleteRunbook: (id: string) => api.delete(`/runbooks/${id}`),
}

export { api }
