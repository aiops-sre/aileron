import { create } from 'zustand'
import { subscribeWithSelector } from 'zustand/middleware'

interface User {
  id: string
  email: string
  full_name: string
  role: string
  groups?: string[]
  permissions?: string[]
}

interface TokenSet {
  access_token: string
  refresh_token?: string
  oauth_id_token?: string
  expires_at?: string
  expires_in?: number
}

interface AuthState {
  user: User | null
  tokens: TokenSet | null
  isAuthenticated: boolean
  isInitializing: boolean
  authInitialized: boolean
  sessionExpiry: string | null
  lastActivity: number
  authRetryCount: number
  authError: string | null
  lastAuthAttempt: number | null
  // Tracks whether a redirect to IdMS is already in flight.
  // Prevents duplicate navigations when React re-renders ProtectedRoute
  // multiple times before the browser finishes the location change.
  isRedirectingToAuth: boolean
}

interface AuthActions {
  logout: () => void
  setTokens: (tokens: TokenSet, user: User) => void
  refreshTokens: () => Promise<boolean>
  updateActivity: () => void
  startSessionMonitoring: () => void
  stopSessionMonitoring: () => void
  _handleAuthError: (error: any) => void
}

type EnhancedAuthStore = AuthState & AuthActions

let sessionCheckInterval: NodeJS.Timeout | null = null
let tokenRefreshTimeout: NodeJS.Timeout | null = null
// Module-level ref so the visibility listener can be removed on cleanup.
let visibilityChangeHandler: (() => void) | null = null

const ACTIVITY_TIMEOUT = 60 * 60 * 1000  // 1 hour (was 30 min — too aggressive for background tabs)
const PROACTIVE_REFRESH_WINDOW = 5 * 60 * 1000 // refresh 5 min before expiry

export const useEnhancedAuthStore = create<EnhancedAuthStore>()(
  subscribeWithSelector(
    (set, get) => ({
      user: null,
      tokens: null,
      isAuthenticated: false,
      isInitializing: true,
      authInitialized: false,
      sessionExpiry: null,
      lastActivity: Date.now(),
      authRetryCount: 0,
      authError: null,
      lastAuthAttempt: null,
      isRedirectingToAuth: false,

      logout: () => {
        sessionStorage.removeItem('access_token')
        sessionStorage.removeItem('refresh_token')
        sessionStorage.removeItem('oauth_id_token')
        sessionStorage.removeItem('floodgate_token')
        sessionStorage.removeItem('floodgate_token_expiry')
        sessionStorage.removeItem('floodgate_token_source')
        sessionStorage.removeItem('user')
        sessionStorage.removeItem('user_groups')
        localStorage.removeItem('access_token')
        localStorage.removeItem('floodgate_token')
        localStorage.removeItem('oauth_id_token')
        get().stopSessionMonitoring()
        set({
          user: null,
          tokens: null,
          isAuthenticated: false,
          sessionExpiry: null,
          authError: null,
          authRetryCount: 0,
          isRedirectingToAuth: false,
        })
        const redirect = encodeURIComponent(window.location.pathname + window.location.search)
        window.location.href = `/api/v1/auth/oidc?redirect=${redirect}`
      },

      setTokens: (tokens: TokenSet, user: User) => {
        sessionStorage.setItem('access_token', tokens.access_token)
        if (tokens.refresh_token) {
          sessionStorage.setItem('refresh_token', tokens.refresh_token)
        }
        if (tokens.oauth_id_token) {
          sessionStorage.setItem('oauth_id_token', tokens.oauth_id_token)
        }

        const expiryTime = (() => {
          if (tokens.expires_at) return tokens.expires_at
          if (tokens.expires_in) return new Date(Date.now() + tokens.expires_in * 1000).toISOString()
          try {
            const payload = JSON.parse(atob(tokens.access_token.split('.')[1]))
            if (payload.exp) return new Date(payload.exp * 1000).toISOString()
          } catch {}
          return new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString()
        })()

        sessionStorage.setItem('user', JSON.stringify(user))
        if (user.groups) {
          sessionStorage.setItem('user_groups', JSON.stringify(user.groups))
        }

        set({
          user,
          tokens: { ...tokens, expires_at: expiryTime },
          isAuthenticated: true,
          isInitializing: false,
          authInitialized: true,
          sessionExpiry: expiryTime,
          lastActivity: Date.now(),
          isRedirectingToAuth: false, // fresh token — clear the redirect guard
        })

        get().startSessionMonitoring()
      },

      refreshTokens: async () => {
        const { tokens } = get()
        if (!tokens?.refresh_token) return true

        try {
          const response = await fetch('/api/v1/auth/refresh', {
            method: 'POST',
            headers: {
              'Content-Type': 'application/json',
              Authorization: `Bearer ${tokens.access_token}`,
            },
            body: JSON.stringify({ refresh_token: tokens.refresh_token }),
          })
          if (!response.ok) throw new Error(`${response.status}`)
          const data = await response.json()
          if (data.success && data.data?.access_token) {
            get().setTokens(data.data, get().user!)
          }
          return true
        } catch {
          return true // keep existing auth on refresh failure
        }
      },

      updateActivity: () => {
        set({ lastActivity: Date.now() })
      },

      startSessionMonitoring: () => {
        get().stopSessionMonitoring()

        // Reset lastActivity when the tab becomes visible again.
        // Without this, a user returning after hours of background inactivity
        // would immediately trigger the activity timeout and get redirected to
        // re-auth even though they literally just clicked back to the tab.
        visibilityChangeHandler = () => {
          if (!document.hidden) {
            set({ lastActivity: Date.now() })

            // Proactively refresh if the token will expire in the next 10 minutes.
            // This avoids the jarring "suddenly logged out" experience when the
            // token expires while the user is actively using the app.
            const state = get()
            if (state.tokens?.expires_at) {
              const expiry = new Date(state.tokens.expires_at).getTime()
              const remainingMs = expiry - Date.now()
              if (remainingMs > 0 && remainingMs < 10 * 60 * 1000) {
                get().refreshTokens()
              }
            }
          }
        }
        document.addEventListener('visibilitychange', visibilityChangeHandler)

        sessionCheckInterval = setInterval(() => {
          const state = get()
          if (!state.tokens?.expires_at) return

          const expiry = new Date(state.tokens.expires_at).getTime()
          const now = Date.now()

          // Hard-expired: redirect to re-auth
          if (now >= expiry) {
            const redirect = encodeURIComponent(window.location.pathname + window.location.search)
            window.location.href = `/api/v1/auth/oidc?redirect=${redirect}`
            return
          }

          // Proactive refresh 5 min before expiry — no user disruption
          if (expiry - now < PROACTIVE_REFRESH_WINDOW) {
            get().refreshTokens()
            return
          }

          // Activity timeout — only redirect if the tab is currently VISIBLE.
          // When the tab is hidden, the user can't interact with it, so "inactivity"
          // is expected. We'd be redirecting people who just left the tab open.
          // The visibility handler resets lastActivity when they return.
          if (!document.hidden && now - state.lastActivity > ACTIVITY_TIMEOUT) {
            const redirect = encodeURIComponent(window.location.pathname + window.location.search)
            window.location.href = `/api/v1/auth/oidc?redirect=${redirect}`
          }
        }, 60 * 1000)
      },

      stopSessionMonitoring: () => {
        if (sessionCheckInterval) {
          clearInterval(sessionCheckInterval)
          sessionCheckInterval = null
        }
        if (tokenRefreshTimeout) {
          clearTimeout(tokenRefreshTimeout)
          tokenRefreshTimeout = null
        }
        if (visibilityChangeHandler) {
          document.removeEventListener('visibilitychange', visibilityChangeHandler)
          visibilityChangeHandler = null
        }
      },

      _handleAuthError: (_error: any) => {
        set({
          isInitializing: false,
          authInitialized: true,
          authRetryCount: get().authRetryCount + 1,
          lastAuthAttempt: Date.now(),
        })
      },
    })
  )
)

const authStore = useEnhancedAuthStore.getState()

const isTokenExpired = (token: string): boolean => {
  try {
    const payload = JSON.parse(atob(token.split('.')[1]))
    if (!payload.exp) return false
    return Date.now() >= payload.exp * 1000 - 30000
  } catch {
    return false
  }
}

// Called at app startup — loads cached tokens or marks unauthenticated.
export const initializeAuth = async (): Promise<boolean> => {
  const existingToken = sessionStorage.getItem('access_token')

  if (existingToken) {
    const cachedUser = sessionStorage.getItem('user')
    if (cachedUser) {
      try {
        const user = JSON.parse(cachedUser)
        if (!isTokenExpired(existingToken)) {
          try {
            const payload = JSON.parse(atob(existingToken.split('.')[1]))
            if (payload.role && payload.role !== user.role) {
              user.role = payload.role
            }
            // Proactively refresh if token expires within 10 minutes
            if (payload.exp) {
              const expiresInMs = payload.exp * 1000 - Date.now()
              if (expiresInMs > 0 && expiresInMs < 10 * 60 * 1000) {
                setTimeout(() => authStore.refreshTokens(), 500)
              }
            }
          } catch { /* ignore — cached role is the fallback */ }

          authStore.setTokens(
            {
              access_token: existingToken,
              refresh_token: sessionStorage.getItem('refresh_token') || undefined,
              oauth_id_token: sessionStorage.getItem('oauth_id_token') || undefined,
            },
            user
          )
          return true
        }
        // Expired — clear and fall through to unauthenticated
        sessionStorage.removeItem('access_token')
        sessionStorage.removeItem('refresh_token')
        sessionStorage.removeItem('user')
      } catch {
        sessionStorage.removeItem('access_token')
        sessionStorage.removeItem('user')
      }
    }
  }

  useEnhancedAuthStore.setState({
    isInitializing: false,
    authInitialized: true,
    isAuthenticated: false,
  })
  return false
}

export const setupActivityTracking = () => {
  const events = ['mousedown', 'mousemove', 'keypress', 'scroll', 'touchstart', 'click']
  const trackActivity = () => useEnhancedAuthStore.getState().updateActivity()
  events.forEach((event) => document.addEventListener(event, trackActivity, { passive: true }))
}

export const cleanupAuth = () => {
  useEnhancedAuthStore.getState().stopSessionMonitoring()
}

export const selectUser = (state: EnhancedAuthStore) => state.user
export const selectIsAuthenticated = (state: EnhancedAuthStore) => state.isAuthenticated
export const selectIsInitializing = (state: EnhancedAuthStore) => state.isInitializing
export const selectAuthInitialized = (state: EnhancedAuthStore) => state.authInitialized
export const selectAuthError = (state: EnhancedAuthStore) => state.authError
export const selectTokens = (state: EnhancedAuthStore) => state.tokens
