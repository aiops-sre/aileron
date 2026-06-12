import { create } from 'zustand'
import { subscribeWithSelector } from 'zustand/middleware'
import { alertsApi } from '@/lib/api'
import type { Alert, AlertFilters } from '@/types'
import { mlLearningSystem } from '@/lib/ml-learning'
import { MLCorrelationEngine } from '@/lib/ml-correlations'

interface MLInsights {
  anomalies: any[]
  correlations: any[]
  predictions: any[]
  patterns: any[]
  lastUpdated: number | null
}

interface AlertsState {
  alerts: Alert[]
  filteredAlerts: Alert[]
  filters: AlertFilters
  isInitialLoading: boolean
  isRefreshing: boolean
  lastUpdated: number | null
  totalAlerts: number
  error: string | null
  mlInsights: MLInsights
  isGeneratingInsights: boolean
  // Pagination state
  currentPage: number
  pageSize: number
  totalPages: number
}

interface AlertsActions {
  // Data loading with pagination
  loadAlerts: (silent?: boolean, page?: number, pageSize?: number) => Promise<void>
  refreshAlerts: () => Promise<void>
  
  // Filtering with server-side support
  setFilters: (filters: Partial<AlertFilters>) => void
  clearFilters: () => void
  
  // Pagination
  setPage: (page: number) => void
  setPageSize: (pageSize: number) => void
  
  // Utils
  getAlertById: (id: string) => Alert | undefined
  markAsRead: (ids: string[]) => void
  
  // ML functionality
  generateMLInsights: () => Promise<void>
  getMLCorrelations: () => any[]
  getMLPredictions: () => any[]
  
  // Internal methods
  _updateLastUpdated: () => void
}

type AlertsStore = AlertsState & AlertsActions

const initialFilters: AlertFilters = {
  search: '',
  severity: '',
  status: '',
  time_range: '',
  source: ''
}

export const useAlertsStore = create<AlertsStore>()(
  subscribeWithSelector(
    (set, get) => ({
      // Initial state
      alerts: [],
      filteredAlerts: [],
      filters: initialFilters,
      isInitialLoading: false,
      isRefreshing: false,
      lastUpdated: null,
      totalAlerts: 0,
      error: null,
      mlInsights: {
        anomalies: [],
        correlations: [],
        predictions: [],
        patterns: [],
        lastUpdated: null
      },
      isGeneratingInsights: false,
      // Pagination state
      currentPage: 1,
      pageSize: 50,
      totalPages: 1,

      // Update last updated timestamp
      _updateLastUpdated: () => {
        set({ lastUpdated: Date.now() })
      },

      // Pagination methods
      setPage: (page: number) => {
        set({ currentPage: page })
        get().loadAlerts(false, page, get().pageSize)
      },

      setPageSize: (pageSize: number) => {
        set({ pageSize, currentPage: 1 })
        get().loadAlerts(false, 1, pageSize)
      },

      // Load alerts with pagination and server-side filtering
      loadAlerts: async (silent = false, page = 1, pageSize = 50) => {
        const { filters } = get()
        
        if (!silent) {
          set({ error: null, isInitialLoading: true })
        }

        try {
          const response = await alertsApi.list({
            limit: pageSize,
            offset: (page - 1) * pageSize,
            sort: 'created_at',
            order: 'desc',
            // Server-side filtering
            severity: filters.severity || undefined,
            status: filters.status || undefined,
            source: filters.source || undefined,
            search: filters.search || undefined,
            time_range: filters.time_range || undefined,
          })

          if (response.data.success && response.data.data?.alerts) {
            const alerts = response.data.data.alerts
            const total = response.data.data.total || alerts.length
            const totalPages = Math.ceil(total / pageSize)
            
            set({
              alerts,
              filteredAlerts: alerts, // With server-side filtering, these are the same
              totalAlerts: total,
              totalPages,
              currentPage: page,
              pageSize,
            })
            
            get()._updateLastUpdated()
          }
        } catch (error) {
          console.error('Error loading alerts:', error)
          if (!silent) {
            set({ error: error instanceof Error ? error.message : 'Failed to load alerts' })
          }
        } finally {
          set({ isInitialLoading: false })
        }
      },

      // Refresh alerts (with loading indicator)
      refreshAlerts: async () => {
        const { currentPage, pageSize } = get()
        set({ isRefreshing: true, error: null })

        try {
          await get().loadAlerts(false, currentPage, pageSize)
          get()._updateLastUpdated()
        } catch (error) {
          console.error('Error refreshing alerts:', error)
          set({ error: error instanceof Error ? error.message : 'Failed to refresh alerts' })
        } finally {
          set({ isRefreshing: false })
        }
      },

      // Set filters and trigger server-side filtering
      setFilters: (newFilters) => {
        set({ filters: { ...get().filters, ...newFilters }, currentPage: 1 })
        get().loadAlerts(false, 1, get().pageSize)
      },

      // Clear all filters
      clearFilters: () => {
        set({ filters: initialFilters, currentPage: 1 })
        get().loadAlerts(false, 1, get().pageSize)
      },

      // Get alert by ID
      getAlertById: (id: string) => {
        return get().alerts.find(alert => alert.id === id)
      },

      // Mark alerts as read (placeholder for future implementation)
      markAsRead: (ids: string[]) => {
        // This could update alert status or add read timestamps
        console.log('Marking alerts as read:', ids)
      },

      // Generate ML insights from current alerts
      generateMLInsights: async () => {
        const { alerts } = get()
        if (alerts.length === 0) return

        set({ isGeneratingInsights: true })

        try {
          // Learn from alerts
          mlLearningSystem.learnFromAlerts(alerts)
          
          // Generate correlations
          const engine = new MLCorrelationEngine(mlLearningSystem.getAlertHistory())
          const clusters = engine.clusterAlerts(alerts)
          const anomalies = engine.detectAnomalies(alerts)
          const rootCauses = engine.bayesianRootCauseAnalysis(alerts)
          
          // Get predictions
          const predictions = mlLearningSystem.predictPatterns(alerts)
          const patterns = mlLearningSystem.getAllPatterns()

          set({
            mlInsights: {
              anomalies,
              correlations: clusters,
              predictions,
              patterns: patterns.slice(0, 10), // Top 10 patterns
              lastUpdated: Date.now()
            }
          })
        } catch (error) {
          console.error('Error generating ML insights:', error)
        } finally {
          set({ isGeneratingInsights: false })
        }
      },

      // Get ML correlations
      getMLCorrelations: () => {
        return get().mlInsights.correlations
      },

      // Get ML predictions
      getMLPredictions: () => {
        return get().mlInsights.predictions
      },
    })
  )
)

// Background refresh functionality
let backgroundRefreshInterval: NodeJS.Timeout | null = null

export const startBackgroundRefresh = () => {
  if (backgroundRefreshInterval) {
    clearInterval(backgroundRefreshInterval)
  }
  
  // Refresh every 15 seconds for real-time updates
  backgroundRefreshInterval = setInterval(() => {
    const store = useAlertsStore.getState()
    // Pass current page/size so background refresh doesn't reset the user's pagination position.
    store.loadAlerts(true, store.currentPage, store.pageSize)
  }, 15000)
}

export const stopBackgroundRefresh = () => {
  if (backgroundRefreshInterval) {
    clearInterval(backgroundRefreshInterval)
    backgroundRefreshInterval = null
  }
}

// Selectors for performance optimization
export const selectAlerts = (state: AlertsStore) => state.alerts
export const selectFilteredAlerts = (state: AlertsStore) => state.filteredAlerts
export const selectIsLoading = (state: AlertsStore) => state.isInitialLoading
export const selectIsRefreshing = (state: AlertsStore) => state.isRefreshing
export const selectFilters = (state: AlertsStore) => state.filters
export const selectTotalAlerts = (state: AlertsStore) => state.totalAlerts