// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Kentaurus Incidents Store
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import kentaurusService, { mapKentaurusIncident, calculateIncidentMetrics } from '@/services/KentaurusService'
import type {
  KentaurusIncident,
  KentaurusIncidentFilters,
  DateRange,
  IncidentMetrics,
  IncidentNotification,
  SavedView,
  KentaurusCreateIncidentRequest,
} from '@/types/kentaurus'

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Store Types
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

interface KentaurusIncidentsState {
  // Data
  incidents: KentaurusIncident[]
  metrics: IncidentMetrics | null
  notifications: IncidentNotification[]
  savedViews: SavedView[]
  selectedIncident: KentaurusIncident | null
  totalCount: number
  currentPage: number
  readonly pageSize: number

  // UI State
  isLoading: boolean
  isRefreshing: boolean
  isCreating: boolean
  error: string | null
  lastSync: string | null

  // Filters
  filters: KentaurusIncidentFilters
  dateRange: DateRange
  activeView: string | null

  // Actions - Data Loading
  loadIncidents: () => Promise<void>
  loadPage: (page: number) => Promise<void>
  loadMonthIncidents: (year: number, month: number) => Promise<void>
  refreshIncidents: () => Promise<void>

  // Actions - CRUD Operations
  createIncident: (request: Omit<KentaurusCreateIncidentRequest, 'module' | 'callingApp' | 'configuration'>) => Promise<boolean>
  updateIncident: (ticketId: string, fields: {
    ticketStatus?: string
    workLog?: string
    additionalComments?: string
    resolution?: string
    resolutionSummary?: string
    impact?: '1' | '2' | '3'
    urgency?: '1' | '2' | '3'
  }) => Promise<boolean>
  acknowledgeIncident: (ticketId: string) => Promise<boolean>
  reopenIncident: (number: string, comment: string) => Promise<boolean>
  selectIncident: (incident: KentaurusIncident | null) => void

  // Actions - Filters and Views
  setFilters: (filters: Partial<KentaurusIncidentFilters>) => void
  setDateRange: (dateRange: DateRange) => void
  clearFilters: () => void

  // Actions - Saved Views
  saveView: (name: string) => void
  loadView: (viewId: string) => void
  deleteView: (viewId: string) => void

  // Actions - Notifications
  addNotification: (notification: Omit<IncidentNotification, 'id' | 'timestamp' | 'read'>) => void
  markNotificationRead: (notificationId: string) => void
  clearNotifications: () => void

  // Actions - Metrics
  calculateMetrics: () => void

  // Actions - Export
  exportIncidents: (format: 'csv' | 'json' | 'excel') => void

  // Reset
  reset: () => void
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Initial State
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

const getDefaultDateRange = (): DateRange => {
  const now = new Date()
  const start = new Date(now.getFullYear(), now.getMonth(), 1)
  const end = new Date(now.getFullYear(), now.getMonth(), now.getDate(), 23, 59, 59)
  
  return {
    start: start.toISOString(),
    end: end.toISOString(),
    type: 'current_month',
  }
}

const initialState = {
  incidents: [],
  metrics: null,
  notifications: [],
  savedViews: [],
  selectedIncident: null,
  totalCount: 0,
  currentPage: 0,
  pageSize: 100,
  isLoading: false,
  isRefreshing: false,
  isCreating: false,
  error: null,
  lastSync: null,
  filters: {
    search: '',
    severity: '',
    status: '',
    assigned_to: '',
    impact: '',
    urgency: '',
    state: '',
    assignment_group: '',
    environment: '',
    sla_breach: false,
  } as KentaurusIncidentFilters,
  dateRange: getDefaultDateRange(),
  activeView: null,
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Store Implementation
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

export const useKentaurusIncidentsStore = create<KentaurusIncidentsState>()(
  persist(
    (set, get) => ({
      ...initialState,

      // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
      // Data Loading
      // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

      loadIncidents: async () => {
        set({ isLoading: true, error: null, currentPage: 0 })
        try {
          const { dateRange, pageSize } = get()
          const response = await kentaurusService.queryIncidents(dateRange, {}, 0, pageSize)
          if (response.success && response.data) {
            const incidents = response.data.records.map(mapKentaurusIncident)
            set({
              incidents,
              totalCount: response.data.total,
              currentPage: 0,
              isLoading: false,
              lastSync: new Date().toISOString(),
              error: null,
            })
            get().calculateMetrics()
          } else {
            set({ isLoading: false, error: response.error || 'Failed to load incidents' })
          }
        } catch (error) {
          console.error('[Incidents Store] Load failed:', error)
          set({ isLoading: false, error: error instanceof Error ? error.message : 'Unknown error' })
        }
      },

      loadPage: async (page: number) => {
        const { dateRange, pageSize, totalCount } = get()
        const maxPage = Math.max(0, Math.ceil(totalCount / pageSize) - 1)
        const safePage = Math.min(page, maxPage)
        const offset = safePage * pageSize
        set({ isLoading: true, error: null })
        try {
          const response = await kentaurusService.queryIncidents(dateRange, {}, offset, pageSize)
          if (response.success && response.data) {
            const incidents = response.data.records.map(mapKentaurusIncident)
            // If a non-first page returns empty results, the API likely hit a dead end.
            // Keep existing incidents to avoid blanking the view; Kentaurus pagination
            // does not always honor offset reliably when results are exhausted.
            if (incidents.length === 0 && safePage > 0) {
              set({ isLoading: false, currentPage: safePage })
              return
            }
            set({
              incidents,
              totalCount: response.data.total,
              currentPage: safePage,
              isLoading: false,
              lastSync: new Date().toISOString(),
              error: null,
            })
            get().calculateMetrics()
          } else {
            set({ isLoading: false, error: response.error || 'Failed to load page' })
          }
        } catch (error) {
          console.error('[Incidents Store] Load page failed:', error)
          set({ isLoading: false, error: error instanceof Error ? error.message : 'Unknown error' })
        }
      },

      loadMonthIncidents: async (year: number, month: number) => {
        set({ isLoading: true, error: null })
        
        try {
          const response = await kentaurusService.queryMonthIncidents(year, month)
          
          if (response.success && response.data) {
            const incidents = response.data.records.map(mapKentaurusIncident)
            
            // Update date range
            const start = new Date(year, month, 1)
            const end = new Date(year, month + 1, 0, 23, 59, 59)
            
            set({
              incidents,
              dateRange: {
                start: start.toISOString(),
                end: end.toISOString(),
                type: 'custom',
              },
              isLoading: false,
              lastSync: new Date().toISOString(),
              error: null,
            })
            
            get().calculateMetrics()
          } else {
            set({
              isLoading: false,
              error: response.error || 'Failed to load incidents',
            })
          }
        } catch (error) {
          console.error('[Incidents Store] Load month failed:', error)
          set({
            isLoading: false,
            error: error instanceof Error ? error.message : 'Unknown error',
          })
        }
      },

      refreshIncidents: async () => {
        set({ isRefreshing: true, error: null })
        
        try {
          await get().loadIncidents()
        } finally {
          set({ isRefreshing: false })
        }
      },

      // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
      // CRUD Operations
      // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

      createIncident: async (request) => {
        set({ isCreating: true, error: null })
        
        try {
          const response = await kentaurusService.createIncident(request)
          
          if (response.success && response.data) {
            // Refresh incidents to include the new one
            await get().loadIncidents()
            
            // Add notification
            get().addNotification({
              incident_id: response.data.sys_id,
              incident_number: response.data.incident_number,
              type: 'created',
              title: 'Incident Created',
              message: `Incident ${response.data.incident_number} has been created successfully`,
              severity: request.impact === '1' ? 'critical' : request.impact === '2' ? 'high' : 'medium',
            })
            
            set({ isCreating: false })
            return true
          } else {
            set({
              isCreating: false,
              error: response.error || 'Failed to create incident',
            })
            return false
          }
        } catch (error) {
          console.error('[Incidents Store] Create failed:', error)
          set({
            isCreating: false,
            error: error instanceof Error ? error.message : 'Unknown error',
          })
          return false
        }
      },

      selectIncident: (incident) => {
        set({ selectedIncident: incident })
      },

      updateIncident: async (ticketId, fields) => {
        set({ error: null })
        try {
          const result = await kentaurusService.updateIncident(ticketId, fields)
          if (result.success) {
            await get().loadIncidents()
            return true
          }
          set({ error: result.error || 'Failed to update incident' })
          return false
        } catch (error) {
          set({ error: error instanceof Error ? error.message : 'Unknown error' })
          return false
        }
      },

      acknowledgeIncident: async (ticketId) => {
        return get().updateIncident(ticketId, { ticketStatus: 'In Progress' })
      },

      reopenIncident: async (number, comment) => {
        set({ error: null })
        try {
          const result = await kentaurusService.reopenIncident(number, comment)
          if (result.success) {
            await get().loadIncidents()
            return true
          }
          set({ error: result.error || 'Failed to reopen incident' })
          return false
        } catch (error) {
          set({ error: error instanceof Error ? error.message : 'Unknown error' })
          return false
        }
      },

      // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
      // Filters and Views
      // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

      setFilters: (newFilters) => {
        set((state) => ({
          filters: { ...state.filters, ...newFilters },
        }))
      },

      setDateRange: (dateRange) => {
        set({ dateRange, currentPage: 0 })
        get().loadIncidents()
      },

      clearFilters: () => {
        set({
          filters: initialState.filters,
          activeView: null,
        })
      },

      // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
      // Saved Views
      // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

      saveView: (name) => {
        const { filters, savedViews } = get()
        const newView: SavedView = {
          id: `view-${Date.now()}`,
          name,
          filters,
          sort_by: 'created_at',
          sort_order: 'desc',
          is_default: false,
          created_by: 'current_user',
          created_at: new Date().toISOString(),
        }
        
        set({
          savedViews: [...savedViews, newView],
          activeView: newView.id,
        })
      },

      loadView: (viewId) => {
        const { savedViews } = get()
        const view = savedViews.find(v => v.id === viewId)
        
        if (view) {
          set({
            filters: view.filters,
            activeView: viewId,
          })
        }
      },

      deleteView: (viewId) => {
        set((state) => ({
          savedViews: state.savedViews.filter(v => v.id !== viewId),
          activeView: state.activeView === viewId ? null : state.activeView,
        }))
      },

      // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
      // Notifications
      // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

      addNotification: (notificationData) => {
        const notification: IncidentNotification = {
          ...notificationData,
          id: `notif-${Date.now()}`,
          timestamp: new Date().toISOString(),
          read: false,
        }
        
        set((state) => ({
          notifications: [notification, ...state.notifications].slice(0, 100), // Keep last 100
        }))
      },

      markNotificationRead: (notificationId) => {
        set((state) => ({
          notifications: state.notifications.map(n =>
            n.id === notificationId ? { ...n, read: true } : n
          ),
        }))
      },

      clearNotifications: () => {
        set({ notifications: [] })
      },

      // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
      // Metrics
      // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

      calculateMetrics: () => {
        const { incidents } = get()
        const metrics = calculateIncidentMetrics(incidents)
        set({ metrics })
      },

      // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
      // Export
      // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

      exportIncidents: (format) => {
        const { incidents } = get()
        
        if (format === 'json') {
          const data = JSON.stringify(incidents, null, 2)
          const blob = new Blob([data], { type: 'application/json' })
          const url = URL.createObjectURL(blob)
          const link = document.createElement('a')
          link.href = url
          link.download = `incidents-${new Date().toISOString()}.json`
          link.click()
          URL.revokeObjectURL(url)
        } else if (format === 'csv') {
          const headers = [
            'Incident Number',
            'Title',
            'Severity',
            'Status',
            'Assigned To',
            'Opened At',
            'Resolved At',
            'Time to Resolve (hours)',
          ]
          
          const rows = incidents.map(inc => [
            inc.incident_number,
            inc.title,
            inc.severity,
            inc.status,
            inc.assigned_to_name || '',
            inc.opened_at,
            inc.resolved_at || '',
            inc.time_to_resolve ? (inc.time_to_resolve / 60).toFixed(2) : '',
          ])
          
          const csv = [
            headers.join(','),
            ...rows.map(row => row.map(cell => `"${cell}"`).join(',')),
          ].join('\n')
          
          const blob = new Blob([csv], { type: 'text/csv' })
          const url = URL.createObjectURL(blob)
          const link = document.createElement('a')
          link.href = url
          link.download = `incidents-${new Date().toISOString()}.csv`
          link.click()
          URL.revokeObjectURL(url)
        }
      },

      // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
      // Reset
      // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

      reset: () => {
        set(initialState)
      },
    }),
    {
      name: 'kentaurus-incidents-store',
      partialize: (state) => ({
        savedViews: state.savedViews,
        filters: state.filters,
        dateRange: state.dateRange,
        activeView: state.activeView,
      }),
    }
  )
)

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Selectors
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

export const selectFilteredIncidents = (state: KentaurusIncidentsState): KentaurusIncident[] => {
  let filtered = [...state.incidents]
  const { filters } = state

  // Search filter
  if (filters.search) {
    const searchTerm = filters.search.toLowerCase()
    filtered = filtered.filter(
      inc =>
        inc.title.toLowerCase().includes(searchTerm) ||
        inc.description.toLowerCase().includes(searchTerm) ||
        inc.incident_number.toLowerCase().includes(searchTerm) ||
        inc.assigned_to_name?.toLowerCase().includes(searchTerm)
    )
  }

  // Severity filter
  if (filters.severity) {
    filtered = filtered.filter(inc => inc.severity === filters.severity)
  }

  // Status filter
  if (filters.status) {
    filtered = filtered.filter(inc => inc.status === filters.status)
  }

  // Impact filter
  if (filters.impact) {
    filtered = filtered.filter(inc => inc.impact_level === filters.impact)
  }

  // Urgency filter
  if (filters.urgency) {
    filtered = filtered.filter(inc => inc.urgency_level === filters.urgency)
  }

  // Assigned to filter
  if (filters.assigned_to) {
    filtered = filtered.filter(inc => inc.assigned_to_name === filters.assigned_to)
  }

  // Environment filter
  if (filters.environment) {
    filtered = filtered.filter(inc => inc.environment === filters.environment)
  }

  // SLA breach filter
  if (filters.sla_breach) {
    filtered = filtered.filter(inc => inc.sla_breach === true)
  }

  return filtered.sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())
}

export const selectUnreadNotifications = (state: KentaurusIncidentsState) =>
  state.notifications.filter(n => !n.read)

export const selectIncidentsByStatus = (state: KentaurusIncidentsState) => ({
  open: state.incidents.filter(i => i.status === 'open'),
  investigating: state.incidents.filter(i => i.status === 'investigating'),
  resolved: state.incidents.filter(i => i.status === 'resolved'),
})
