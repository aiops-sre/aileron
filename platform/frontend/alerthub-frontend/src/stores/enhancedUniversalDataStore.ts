import { create } from 'zustand'
import { subscribeWithSelector } from 'zustand/middleware'
import { alertsApi, analyticsApi, dashboardApi, usersApi, rolesApi, incidentsApi, integrationsApi, settingsApi, configApi } from '@/lib/api'

// Enhanced data types with proper error handling
interface DashboardData {
  metrics: any[]
  recentAlerts: any[]
  systemStatus: any
  incidentSummary: any
  healthChecks: any[]
  _lastError?: string
  _lastUpdate?: number
}

interface AIChatData {
  sessions: any[]
  models: any[]
  recentConversations: any[]
  userPreferences: any
  _lastError?: string
  _lastUpdate?: number
}

interface AdminData {
  users: any[]
  roles: any[]
  auditLogs: any[]
  systemHealth: any
  permissions: any[]
  _lastError?: string
  _lastUpdate?: number
}

interface AnalyticsData {
  charts: any[]
  metrics: any[]
  performanceData: any
  trends: any[]
  _lastError?: string
  _lastUpdate?: number
}

interface IncidentsData {
  activeIncidents: any[]
  incidentHistory: any[]
  assignments: any[]
  escalations: any[]
  _lastError?: string
  _lastUpdate?: number
}

interface IntegrationsData {
  healthStatus: any[]
  configurations: any[]
  metrics: any[]
  connections: any[]
  _lastError?: string
  _lastUpdate?: number
}

interface SettingsData {
  userPreferences: any
  systemConfig: any
  notificationSettings: any
  themeSettings: any
  _lastError?: string
  _lastUpdate?: number
}

interface UniversalDataState {
  dashboard: DashboardData
  aiChat: AIChatData
  admin: AdminData
  analytics: AnalyticsData
  incidents: IncidentsData
  integrations: IntegrationsData
  settings: SettingsData
  
  // Enhanced error tracking
  isInitialized: boolean
  globalErrors: Record<string, string>
  retryCount: Record<string, number>
  lastUpdated: Record<string, number>
  refreshIntervals: Record<string, number>
}

interface UniversalDataActions {
  // Core loading functions
  initializeAllData: () => Promise<void>
  loadPageData: (page: string, silent?: boolean) => Promise<void>
  refreshAllData: () => Promise<void>
  
  // Individual page loaders
  loadDashboardData: (silent?: boolean) => Promise<void>
  loadAIChatData: (silent?: boolean) => Promise<void>
  loadAdminData: (silent?: boolean) => Promise<void>
  loadAnalyticsData: (silent?: boolean) => Promise<void>
  loadIncidentsData: (silent?: boolean) => Promise<void>
  loadIntegrationsData: (silent?: boolean) => Promise<void>
  loadSettingsData: (silent?: boolean) => Promise<void>
  
  // Background refresh management
  startUniversalRefresh: () => void
  stopUniversalRefresh: () => void
}

type EnhancedUniversalDataStore = UniversalDataState & UniversalDataActions

export type { EnhancedUniversalDataStore }

// Background refresh intervals
let universalRefreshInterval: NodeJS.Timeout | null = null
const pageRefreshIntervals: Record<string, NodeJS.Timeout | null> = {}

// Refresh intervals
const REFRESH_INTERVALS = {
  dashboard: 60000,    // 1 minute
  alerts: 30000,       // 30 seconds
  admin: 300000,       // 5 minutes
  analytics: 120000,   // 2 minutes
  incidents: 120000,   // 2 minutes
  integrations: 300000, // 5 minutes
  settings: 600000,    // 10 minutes
}

export const useEnhancedUniversalDataStore = create<EnhancedUniversalDataStore>()(
  subscribeWithSelector(
    (set, get) => ({
      // Initial state with empty data
      dashboard: {
        metrics: [],
        recentAlerts: [],
        systemStatus: null,
        incidentSummary: null,
        healthChecks: []
      },
      aiChat: {
        sessions: [],
        models: [],
        recentConversations: [],
        userPreferences: null
      },
      admin: {
        users: [],
        roles: [],
        auditLogs: [],
        systemHealth: null,
        permissions: []
      },
      analytics: {
        charts: [],
        metrics: [],
        performanceData: null,
        trends: []
      },
      incidents: {
        activeIncidents: [],
        incidentHistory: [],
        assignments: [],
        escalations: []
      },
      integrations: {
        healthStatus: [],
        configurations: [],
        metrics: [],
        connections: []
      },
      settings: {
        userPreferences: null,
        systemConfig: null,
        notificationSettings: null,
        themeSettings: null
      },
      
      isInitialized: false,
      globalErrors: {},
      retryCount: {},
      lastUpdated: {},
      refreshIntervals: REFRESH_INTERVALS,

      // Initialize all data loading with authentication check
      initializeAllData: async () => {
        console.log('🚀 Starting universal data loading with real backend APIs...')
        
        // Only load data that doesn't require authentication immediately
        const loadPromises = [
          get().loadAIChatData(true), // Safe - no auth required
          get().loadSettingsData(true), // Safe - local data only
        ]

        // Load safe data first
        await Promise.allSettled(loadPromises)
        
        set({ isInitialized: true })
        
        // Start background refresh for authenticated data
        get().startUniversalRefresh()
        
        console.log('✅ Universal data loading initialized (authenticated data will load when ready)')
      },

      // Dashboard data loading with auth checking
      loadDashboardData: async (silent = true) => {
        // Check if we have a valid auth token before making API calls
        const token = sessionStorage.getItem('access_token') || localStorage.getItem('access_token')
        if (!token) {
          console.log('⚠️ Skipping dashboard load - no auth token available')
          return
        }
        
        try {
          console.log('🔄 Loading dashboard data with authenticated APIs...')
          
          // Load with proper error handling
          const [alertsRes, dashboardRes] = await Promise.allSettled([
            alertsApi.list({ limit: 10, sort: 'created_at', order: 'desc' }),
            dashboardApi.getStats()
          ])

          const updates: Partial<DashboardData> = {
            recentAlerts: [],
            metrics: []
          }
          
          // Process recent alerts
          if (alertsRes.status === 'fulfilled' && alertsRes.value.data.success) {
            updates.recentAlerts = alertsRes.value.data.data?.alerts || []
            console.log('✅ Recent alerts loaded')
          } else if (alertsRes.status === 'rejected') {
            console.warn('⚠️ Failed to load alerts:', alertsRes.reason)
          }
          
          // Process dashboard metrics
          if (dashboardRes.status === 'fulfilled' && dashboardRes.value.data.success) {
            updates.metrics = [dashboardRes.value.data.data]
            console.log('✅ Dashboard metrics loaded')
          } else if (dashboardRes.status === 'rejected') {
            console.warn('⚠️ Failed to load dashboard stats:', dashboardRes.reason)
          }

          set(state => ({
            dashboard: { ...state.dashboard, ...updates, _lastUpdate: Date.now() }
          }))
          
        } catch (error) {
          console.error('❌ Dashboard data loading failed:', error)
          set(state => ({
            dashboard: {
              ...state.dashboard,
              _lastError: (error as Error).message,
              _lastUpdate: Date.now()
            }
          }))
        }
      },

      // Admin data loading with auth checking
      loadAdminData: async (silent = true) => {
        const token = sessionStorage.getItem('access_token') || localStorage.getItem('access_token')
        if (!token) {
          console.log('⚠️ Skipping admin load - no auth token available')
          return
        }
        
        try {
          console.log('🔄 Loading admin data with authenticated APIs...')
          
          const [usersRes, rolesRes] = await Promise.allSettled([
            usersApi.list(),
            rolesApi.list()
          ])

          const updates: Partial<AdminData> = {
            users: [],
            roles: [],
            auditLogs: [],
            systemHealth: { status: 'unknown' },
            permissions: []
          }
          
          // Process users
          if (usersRes.status === 'fulfilled') {
            if (usersRes.value.data?.success && usersRes.value.data?.data?.users) {
              updates.users = usersRes.value.data.data.users
              console.log('✅ Users loaded')
            } else if (Array.isArray(usersRes.value.data)) {
              updates.users = usersRes.value.data
              console.log('✅ Users loaded (array format)')
            }
          } else if (usersRes.status === 'rejected') {
            console.warn('⚠️ Failed to load users:', usersRes.reason)
          }
          
          // Process roles
          if (rolesRes.status === 'fulfilled') {
            if (rolesRes.value.data?.success && rolesRes.value.data?.data?.roles) {
              updates.roles = rolesRes.value.data.data.roles
              console.log('✅ Roles loaded')
            } else if (Array.isArray(rolesRes.value.data)) {
              updates.roles = rolesRes.value.data
              console.log('✅ Roles loaded (array format)')
            }
          } else if (rolesRes.status === 'rejected') {
            console.warn('⚠️ Failed to load roles:', rolesRes.reason)
          }

          set(state => ({
            admin: { ...state.admin, ...updates, _lastUpdate: Date.now() }
          }))
          
        } catch (error) {
          console.error('❌ Admin data loading failed:', error)
          set(state => ({
            admin: {
              ...state.admin,
              _lastError: (error as Error).message,
              _lastUpdate: Date.now()
            }
          }))
        }
      },

      // Analytics data loading with auth checking
      loadAnalyticsData: async (silent = true) => {
        const token = sessionStorage.getItem('access_token') || localStorage.getItem('access_token')
        if (!token) {
          console.log('⚠️ Skipping analytics load - no auth token available')
          return
        }
        
        // Analytics data is calculated locally from alerts (like frontend-3.0)
        console.log('✅ Analytics will be calculated locally from alert data')
        
        const updates: Partial<AnalyticsData> = {
          charts: [],
          metrics: [],
          performanceData: null,
          trends: [],
          _lastUpdate: Date.now()
        }
        
        set(state => ({
          analytics: { ...state.analytics, ...updates }
        }))
      },

      // AI Chat data loading (minimal for now)
      loadAIChatData: async (silent = true) => {
        const updates = {
          sessions: [],
          models: [],
          recentConversations: [],
          userPreferences: { theme: 'auto', model: 'gpt-4' },
          _lastUpdate: Date.now()
        }
        
        set(state => ({
          aiChat: { ...state.aiChat, ...updates }
        }))
        
        console.log('✅ AI Chat data initialized')
      },

      // Incidents data loading with auth checking
      loadIncidentsData: async (silent = true) => {
        const token = sessionStorage.getItem('access_token') || localStorage.getItem('access_token')
        if (!token) {
          console.log('⚠️ Skipping incidents load - no auth token available')
          return
        }
        
        try {
          console.log('🔄 Loading incidents data with authenticated APIs...')
          
          const [activeRes, historyRes] = await Promise.allSettled([
            incidentsApi.list({ status: 'open,investigating', limit: 100 }),
            incidentsApi.list({ status: 'resolved', limit: 50 })
          ])

          const updates: Partial<IncidentsData> = {
            activeIncidents: [],
            incidentHistory: [],
            assignments: [],
            escalations: []
          }
          
          if (activeRes.status === 'fulfilled' && activeRes.value.data.success) {
            updates.activeIncidents = activeRes.value.data.data?.incidents || []
            console.log('✅ Active incidents loaded')
          } else if (activeRes.status === 'rejected') {
            console.warn('⚠️ Failed to load active incidents:', activeRes.reason)
          }
          
          if (historyRes.status === 'fulfilled' && historyRes.value.data.success) {
            updates.incidentHistory = historyRes.value.data.data?.incidents || []
            console.log('✅ Incident history loaded')
          } else if (historyRes.status === 'rejected') {
            console.warn('⚠️ Failed to load incident history:', historyRes.reason)
          }

          set(state => ({
            incidents: { ...state.incidents, ...updates, _lastUpdate: Date.now() }
          }))
          
        } catch (error) {
          console.error('❌ Incidents data loading failed:', error)
          set(state => ({
            incidents: {
              ...state.incidents,
              _lastError: (error as Error).message,
              _lastUpdate: Date.now()
            }
          }))
        }
      },

      // Integrations data loading with backend endpoints
      loadIntegrationsData: async (silent = true) => {
        try {
          const [listRes] = await Promise.allSettled([
            integrationsApi.list()
          ])

          const updates: Partial<IntegrationsData> = {
            configurations: [],
            healthStatus: [],
            metrics: [],
            connections: []
          }
          
          if (listRes.status === 'fulfilled' && listRes.value.data.success) {
            updates.configurations = listRes.value.data.data || []
          }

          set(state => ({
            integrations: { ...state.integrations, ...updates, _lastUpdate: Date.now() }
          }))
          
          console.log('✅ Integrations data loaded from backend APIs')
          
        } catch (error) {
          console.error('❌ Integrations data loading failed:', error)
          set(state => ({
            integrations: { 
              ...state.integrations, 
              _lastError: (error as Error).message,
              _lastUpdate: Date.now()
            }
          }))
        }
      },

      // Settings data loading
      loadSettingsData: async (silent = true) => {
        // Replace mock with real API calls to /users/settings and /system/config
        const defaults = {
          userPreferences: {
            theme: localStorage.getItem('theme') || 'auto',
            notifications: true, sound: true, email_alerts: false
          },
          systemConfig: { alert_retention_days: 90, incident_retention_days: 365,
            auto_escalation: true, maintenance_mode: false },
          notificationSettings: { critical_alerts: true, email_digest: false, slack_notifications: true },
          themeSettings: { dark_mode: 'auto', color_scheme: 'blue' },
          _lastUpdate: Date.now()
        }
        try {
          const [userRes, sysRes] = await Promise.allSettled([
            settingsApi.get(),
            configApi.getSystemConfig(),
          ])
          const userSettings = userRes.status === 'fulfilled' ? (userRes.value?.data?.data || userRes.value?.data || {}) : {}
          const sysConfig   = sysRes.status === 'fulfilled'  ? (sysRes.value?.data?.data  || sysRes.value?.data  || {}) : {}
          set(state => ({
            settings: {
              ...state.settings,
              ...defaults,
              userPreferences: { ...defaults.userPreferences, ...userSettings },
              systemConfig:    { ...defaults.systemConfig,    ...sysConfig },
              _lastUpdate: Date.now()
            }
          }))
        } catch {
          set(state => ({ settings: { ...state.settings, ...defaults } }))
        }
      },

      // Generic page data loader
      loadPageData: async (page: string, silent = true) => {
        const loaderMap: Record<string, () => Promise<void>> = {
          dashboard: () => get().loadDashboardData(silent),
          aiChat: () => get().loadAIChatData(silent),
          admin: () => get().loadAdminData(silent),
          analytics: () => get().loadAnalyticsData(silent),
          incidents: () => get().loadIncidentsData(silent),
          integrations: () => get().loadIntegrationsData(silent),
          settings: () => get().loadSettingsData(silent),
        }
        
        const loader = loaderMap[page]
        if (loader) {
          await loader()
        }
      },

      // Refresh all data
      refreshAllData: async () => {
        console.log('🔄 Refreshing all application data...')
        
        const refreshPromises = [
          get().loadDashboardData(true),
          get().loadAdminData(true),
          get().loadAnalyticsData(true),
          get().loadIncidentsData(true),
          get().loadIntegrationsData(true),
          get().loadSettingsData(true),
        ]

        await Promise.allSettled(refreshPromises)
        console.log('✅ Data refresh complete')
      },

      // Start background refresh
      startUniversalRefresh: () => {
        get().stopUniversalRefresh()
        
        // Set up refresh intervals for each data type
        Object.entries(REFRESH_INTERVALS).forEach(([page, interval]) => {
          pageRefreshIntervals[page] = setInterval(() => {
            console.log(`🔄 Background refresh: ${page}`)
            get().loadPageData(page, true)
          }, interval)
        })
        
        console.log('✅ Universal background refresh started')
      },

      // Stop all background refresh
      stopUniversalRefresh: () => {
        Object.values(pageRefreshIntervals).forEach(interval => {
          if (interval) clearInterval(interval)
        })
        
        Object.keys(pageRefreshIntervals).forEach(key => {
          pageRefreshIntervals[key] = null
        })
      },
    })
  )
)

// Initialize with error handling
let isEnhancedInitialized = false

export const initializeEnhancedDataLoading = async () => {
  if (isEnhancedInitialized) return
  
  console.log('🚀 Initializing universal data loading with real backend APIs...')
  isEnhancedInitialized = true
  
  const store = useEnhancedUniversalDataStore.getState()
  await store.initializeAllData()
}

export const cleanupEnhancedDataLoading = () => {
  useEnhancedUniversalDataStore.getState().stopUniversalRefresh()
  isEnhancedInitialized = false
}

// Selectors
export const selectDashboardData = (state: EnhancedUniversalDataStore) => state.dashboard
export const selectAdminData = (state: EnhancedUniversalDataStore) => state.admin
export const selectAnalyticsData = (state: EnhancedUniversalDataStore) => state.analytics
export const selectIncidentsData = (state: EnhancedUniversalDataStore) => state.incidents
export const selectIntegrationsData = (state: EnhancedUniversalDataStore) => state.integrations
export const selectSettingsData = (state: EnhancedUniversalDataStore) => state.settings
export const selectAIChatData = (state: EnhancedUniversalDataStore) => state.aiChat
export const selectGlobalErrors = (state: EnhancedUniversalDataStore) => state.globalErrors
export const selectRetryCount = (state: EnhancedUniversalDataStore) => state.retryCount