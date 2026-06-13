import axios from 'axios'
import { useEnhancedAuthStore } from '../stores/enhancedAuthStore'

const api = axios.create({
  baseURL: '/api/v1',
  headers: {
    'Content-Type': 'application/json',
  },
})

// Track if we're already showing the auth modal to prevent multiple popups
let isShowingAuthModal = false

// Request interceptor to add auth token
api.interceptors.request.use(
  (config) => {
    const token = sessionStorage.getItem('access_token') || localStorage.getItem('access_token')
    if (token) {
      config.headers.Authorization = `Bearer ${token}`
    }
    return config
  },
  (error) => Promise.reject(error)
)

// Enhanced response interceptor with better 401 handling
api.interceptors.response.use(
  (response) => response,
  async (error) => {
    if (error.response?.status === 401) {
      // Prevent multiple auth modals
      if (isShowingAuthModal) {
        return Promise.reject(error)
      }

      const currentPath = window.location.pathname
      
      // Don't show auth modal on auth pages
      if (currentPath.includes('/login') || currentPath.includes('/oauth') || currentPath.includes('/auth')) {
        return Promise.reject(error)
      }

      isShowingAuthModal = true

      try {
        // Try to refresh the token first.
        // MAS/OIDC users store tokens in sessionStorage; manual-login users use localStorage.
        const refreshToken = sessionStorage.getItem('refresh_token') || localStorage.getItem('refresh_token')
        if (refreshToken) {
          const refreshResponse = await axios.post('/api/v1/auth/refresh', {
            refresh_token: refreshToken
          })

          if (refreshResponse.data.access_token) {
            // Update tokens in whichever storage currently holds the access token.
            const inSession = !!sessionStorage.getItem('access_token')
            if (inSession) {
              sessionStorage.setItem('access_token', refreshResponse.data.access_token)
              if (refreshResponse.data.refresh_token) {
                sessionStorage.setItem('refresh_token', refreshResponse.data.refresh_token)
              }
            } else {
              localStorage.setItem('access_token', refreshResponse.data.access_token)
              if (refreshResponse.data.refresh_token) {
                localStorage.setItem('refresh_token', refreshResponse.data.refresh_token)
              }
            }

            // Update auth store
            const store = useEnhancedAuthStore.getState()
            if (store.tokens && store.user) {
              store.setTokens({ ...store.tokens, access_token: refreshResponse.data.access_token }, store.user)
            } else {
              localStorage.setItem('access_token', refreshResponse.data.access_token)
            }
            
            isShowingAuthModal = false
            
            // Retry the original request
            error.config.headers.Authorization = `Bearer ${refreshResponse.data.access_token}`
            return api.request(error.config)
          }
        }
      } catch (refreshError) {
        console.log('Token refresh failed, showing re-auth modal')
      }

      // Show re-authentication modal instead of redirect
      showReAuthModal()
      isShowingAuthModal = false
    }
    
    return Promise.reject(error)
  }
)

// Create and show re-authentication modal
function showReAuthModal() {
  // Remove any existing auth modal
  const existingModal = document.getElementById('auth-modal')
  if (existingModal) {
    existingModal.remove()
  }

  // Create modal HTML
  const modalHTML = `
    <div id="auth-modal" style="
      position: fixed;
      top: 0;
      left: 0;
      right: 0;
      bottom: 0;
      background: rgba(0, 0, 0, 0.5);
      display: flex;
      align-items: center;
      justify-content: center;
      z-index: 10000;
      font-family: -aileron-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
    ">
      <div style="
        background: var(--color-card, white);
        border-radius: 12px;
        padding: 24px;
        max-width: 400px;
        margin: 20px;
        box-shadow: 0 20px 40px rgba(0, 0, 0, 0.15);
        border: 0.5px solid var(--color-separator, rgba(0, 0, 0, 0.1));
      ">
        <div style="text-align: center; margin-bottom: 20px;">
          <div style="
            width: 48px;
            height: 48px;
            background: #007AFF;
            border-radius: 50%;
            display: flex;
            align-items: center;
            justify-content: center;
            margin: 0 auto 16px;
            color: white;
            font-size: 20px;
          ">🔒</div>
          <h2 style="
            margin: 0 0 8px 0;
            color: var(--color-text, #000);
            font-size: 20px;
            font-weight: 600;
          ">Session Expired</h2>
          <p style="
            margin: 0;
            color: var(--color-text-secondary, #666);
            font-size: 14px;
            line-height: 1.4;
          ">Your login session has expired. Please sign in again to continue.</p>
        </div>
        
        <div style="display: flex; gap: 12px;">
          <button id="auth-modal-refresh" style="
            flex: 1;
            background: #007AFF;
            color: white;
            border: none;
            border-radius: 8px;
            padding: 12px 16px;
            font-size: 14px;
            font-weight: 500;
            cursor: pointer;
            transition: background-color 0.2s;
          " onmouseover="this.style.background='#0056CC'" onmouseout="this.style.background='#007AFF'">
            Sign In Again
          </button>
          
          <button id="auth-modal-cancel" style="
            flex: 1;
            background: var(--color-fill, #F2F2F7);
            color: var(--color-text, #000);
            border: none;
            border-radius: 8px;
            padding: 12px 16px;
            font-size: 14px;
            font-weight: 500;
            cursor: pointer;
            transition: background-color 0.2s;
          " onmouseover="this.style.background='var(--color-fill-hover, #E5E5EA)'" onmouseout="this.style.background='var(--color-fill, #F2F2F7)'">
            Stay Here
          </button>
        </div>
      </div>
    </div>
  `

  // Add modal to DOM
  document.body.insertAdjacentHTML('beforeend', modalHTML)

  // Add event listeners
  document.getElementById('auth-modal-refresh')?.addEventListener('click', () => {
    // Clear auth state
    const { logout } = useEnhancedAuthStore.getState()
    logout()
    
    // Remove modal
    document.getElementById('auth-modal')?.remove()
    
    // Redirect to login or trigger OAuth
    const hasOAuth = localStorage.getItem('oauth_configured') === 'true'
    if (hasOAuth) {
      window.location.href = '/oauth/login'
    } else {
      window.location.href = '/login'
    }
  })

  document.getElementById('auth-modal-cancel')?.addEventListener('click', () => {
    document.getElementById('auth-modal')?.remove()
  })

  // Close modal on outside click
  document.getElementById('auth-modal')?.addEventListener('click', (e) => {
    if ((e.target as HTMLElement)?.id === 'auth-modal') {
      document.getElementById('auth-modal')?.remove()
    }
  })
}

export default api

// Enhanced API methods with better error handling
export const alertsApi = {
  list: (params?: any) => api.get('/alerts', { params }).catch(handleApiError),
  get: (id: string) => api.get(`/alerts/${id}`).catch(handleApiError),
  acknowledge: (id: string) => api.post(`/alerts/${id}/acknowledge`).catch(handleApiError),
  resolve: (id: string, data: any) => api.post(`/alerts/${id}/resolve`, data).catch(handleApiError),
  assign: (id: string, data: any) => api.post(`/alerts/${id}/assign`, data).catch(handleApiError),
  delete: (id: string) => api.delete(`/alerts/${id}`).catch(handleApiError),
  
  // Batch operations
  bulkAcknowledge: (alertIds: string[]) => api.post('/alerts/bulk/acknowledge', { alert_ids: alertIds }).catch(handleApiError),
  bulkResolve: (alertIds: string[], notes: string) => api.post('/alerts/bulk/resolve', { alert_ids: alertIds, notes }).catch(handleApiError),
  
  // Maintenance windows
  startMaintenance: (id: string, data: any) => api.post(`/alerts/${id}/maintenance/start`, data).catch(handleApiError),
  endMaintenance: (windowId: string) => api.post(`/alerts/maintenance/${windowId}/end`).catch(handleApiError),
  getMaintenanceWindows: (id: string) => api.get(`/alerts/${id}/maintenance`).catch(handleApiError),
  getActiveMaintenanceWindows: () => api.get('/alerts/maintenance/active').catch(handleApiError),
}

export const automationApi = {
  list: () => api.get('/automation/rules').catch(handleApiError),
  get: (id: string) => api.get(`/automation/rules/${id}`).catch(handleApiError),
  create: (data: any) => api.post('/automation/rules', data).catch(handleApiError),
  update: (id: string, data: any) => api.put(`/automation/rules/${id}`, data).catch(handleApiError),
  delete: (id: string) => api.delete(`/automation/rules/${id}`).catch(handleApiError),
  toggle: (id: string, enabled: boolean) => api.put(`/automation/rules/${id}/toggle`, { enabled }).catch(handleApiError),
}

export const settingsApi = {
  get: () => api.get('/users/settings').catch(handleApiError),
  update: (settings: any) => api.put('/users/settings', { settings }).catch(handleApiError),
}

export const authApi = {
  login: (credentials: { username: string; password: string }) =>
    api.post('/auth/login', credentials).catch(handleApiError),
  logout: () => api.post('/auth/logout').catch(handleApiError),
  me: () => api.get('/auth/me').catch(handleApiError),
  refresh: (refreshToken: string) => api.post('/auth/refresh', { refresh_token: refreshToken }).catch(handleApiError),
}

export const incidentsApi = {
  list: (params?: any) => api.get('/incidents', { params }).catch(handleApiError),
  get: (id: string) => api.get(`/incidents/${id}`).catch(handleApiError),
  create: (data: any) => api.post('/incidents', data).catch(handleApiError),
  update: (id: string, data: any) => api.put(`/incidents/${id}`, data).catch(handleApiError),
  resolve: (id: string, data: any) => api.post(`/incidents/${id}/resolve`, data).catch(handleApiError),
}

export const usersApi = {
  list: () => api.get('/users').catch(handleApiError),
  get: (id: string) => api.get(`/users/${id}`).catch(handleApiError),
  create: (data: any) => api.post('/users', data).catch(handleApiError),
  update: (id: string, data: any) => api.put(`/users/${id}`, data).catch(handleApiError),
  delete: (id: string) => api.delete(`/users/${id}`).catch(handleApiError),
}

export const integrationsApi = {
  list: () => api.get('/integrations').catch(handleApiError),
  get: (id: string) => api.get(`/integrations/${id}`).catch(handleApiError),
  create: (data: any) => api.post('/integrations', data).catch(handleApiError),
  update: (id: string, data: any) => api.put(`/integrations/${id}`, data).catch(handleApiError),
  delete: (id: string) => api.delete(`/integrations/${id}`).catch(handleApiError),
  toggle: (id: string, enabled: boolean) => api.put(`/integrations/${id}/toggle`, { enabled }).catch(handleApiError),
  configure: (id: string, config: any) => api.put(`/integrations/${id}/config`, config).catch(handleApiError),
  test: (id: string) => api.post(`/integrations/${id}/test`).catch(handleApiError),
  testConfig: (data: any) => api.post('/integrations/test-config', data).catch(handleApiError),
  sync: (id: string) => api.post(`/integrations/${id}/sync`).catch(handleApiError),
}

export const analyticsApi = {
  getMetrics: (params?: any) => api.get('/analytics/metrics', { params }).catch(handleApiError),
  getClusterHealth: () => api.get('/analytics/cluster-health').catch(handleApiError),
  getAlertVelocity: () => api.get('/analytics/alert-velocity').catch(handleApiError),
  getMTTR: () => api.get('/analytics/mttr').catch(handleApiError),
}

// Generic error handler for API calls
function handleApiError(error: any) {
  // Log the error for debugging
  console.error('API Error:', error)
  
  // If it's a 401, the interceptor will handle it
  // For other errors, we can add specific handling here
  if (error.response?.status === 503) {
    console.warn('Service temporarily unavailable')
  } else if (error.response?.status >= 500) {
    console.error('Server error:', error.response.status)
  }
  
  // Re-throw the error so calling code can handle it
  throw error
}

// Utility function to check if user is authenticated
export const isAuthenticated = () => {
  const token = sessionStorage.getItem('access_token') || localStorage.getItem('access_token')
  if (!token) return false
  
  try {
    // Basic token expiry check (if token contains expiry info)
    const payload = JSON.parse(atob(token.split('.')[1]))
    const now = Date.now() / 1000
    return payload.exp > now
  } catch {
    // If token parsing fails, assume it's valid and let backend validate
    return true
  }
}