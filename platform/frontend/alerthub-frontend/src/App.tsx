import { Routes, Route, Navigate, BrowserRouter, useLocation } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useEnhancedAuthStore, initializeAuth, setupActivityTracking, selectIsAuthenticated, selectIsInitializing, selectUser } from '@/stores/enhancedAuthStore'
import { initializeDataLoading } from '@/stores/universalDataStore'
import { Toaster } from 'react-hot-toast'
import { ThemeProvider } from '@/components/ThemeProvider'
import { Layout } from '@/components/Layout'
import { ErrorBoundary, setupGlobalErrorHandling } from '@/components/ErrorBoundary'
import { useEffect, useRef, useState, lazy, Suspense } from 'react'
import { motion } from 'framer-motion'
import { Shield, Loader2, RefreshCw } from 'lucide-react'

// Import pages
import { ManualLoginPage } from '@/pages/ManualLoginPage'
import { OAuthCallbackPage } from '@/pages/OAuthCallbackPage'
import { FloodgateTestPage } from '@/pages/FloodgateTestPage'
import { DashboardPage } from '@/pages/DashboardPage'
import { RevolutionaryAIOpsDashboard } from '@/pages/RevolutionaryDashboardPage'
import { AlertsPage } from '@/pages/AlertsPage'
import { IncidentsPage } from '@/pages/IncidentsPage'
import { AnalyticsPage } from '@/pages/AnalyticsPage'
import { SettingsPage } from '@/pages/SettingsPage'
import { OnCallSchedule } from '@/pages/OnCallSchedule'
import { AIChatPage } from '@/pages/AIChatPage'
import { AutoDiscoveryPage } from '@/pages/AutoDiscoveryPage'
import { IntegrationHealthPage } from '@/pages/IntegrationHealthPage'
import { ObservabilityPage } from '@/pages/ObservabilityPage'
import AdminPage from '@/pages/AdminPage'

// Import new Keep AIOps feature pages
import { DeduplicationPage } from '@/pages/DeduplicationPage'
import { MappingRulesPage } from '@/pages/MappingRulesPage'
import { NotificationsHubPage } from '@/pages/NotificationsHubPage'
import { AlertQualityPage } from '@/pages/AlertQualityPage'
import { APIKeyManagementPage } from '@/pages/APIKeyManagementPage'
import { HostVMMapping } from '@/pages/HostVMMapping'

// Import new backend-integrated pages
import WorkflowBuilderPage from '@/pages/WorkflowBuilderPage'
import AIOpsPage from '@/pages/AIOpsPage'
import KubernetesManagementPage from '@/pages/KubernetesManagementPage'
const IntelligentInfraTopology = lazy(() => import('@/pages/IntelligentInfraTopology'))
import RCAInvestigationPage from '@/pages/RCAInvestigationPage'
import CapacityPlanningPage from '@/pages/CapacityPlanningPage'
import { KubeSensePage } from '@/pages/KubeSensePage'

// Apple Design Tokens for loading screen
const apple = {
  blue: '#007AFF',
  green: '#34C759',
  purple: '#AF52DE',
  label: 'var(--color-text)',
  secondaryLabel: 'var(--color-text-secondary)',
  tertiaryLabel: 'var(--color-text-tertiary, #8E8E93)',
  background: 'var(--color-background)',
  secondaryBackground: 'var(--color-card, rgba(255, 255, 255, 0.8))',
  separator: 'var(--color-separator, rgba(142, 142, 147, 0.12))',
  fill: 'var(--color-fill, rgba(142, 142, 147, 0.08))',
  tertiaryFill: 'rgba(142, 142, 147, 0.06)',
  radius: { sm: 6, md: 10, lg: 12, xl: 16, '2xl': 20 },
} as const

// Create a client
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      refetchOnWindowFocus: false,
    },
  },
})

// Loading screen for authentication
const AuthenticationLoader = ({ isReauthenticating = false }: { isReauthenticating?: boolean }) => {
  const [showRetry, setShowRetry] = useState(false)

  // If authentication takes more than 8 seconds, show a manual "Try Again" button.
  // This catches the case where the IdMS redirect is stuck or the browser blocked it.
  useEffect(() => {
    const t = setTimeout(() => setShowRetry(true), 8000)
    return () => clearTimeout(t)
  }, [])

  const handleRetry = () => {
    window.location.reload()
  }

  return (
  <div style={{
    minHeight: '100vh',
    background: apple.background,
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    fontFamily: '-apple-system, BlinkMacSystemFont, "SF Pro Text", "SF Pro Icons", "Helvetica Neue", sans-serif',
  }}>
    <motion.div
      initial={{ opacity: 0, scale: 0.9 }}
      animate={{ opacity: 1, scale: 1 }}
      transition={{ duration: 0.3 }}
      style={{
        background: apple.secondaryBackground,
        borderRadius: apple.radius.xl,
        border: `0.5px solid ${apple.separator}`,
        padding: 40,
        textAlign: 'center',
        boxShadow: '0 20px 60px rgba(0,0,0,0.1)',
        maxWidth: 400,
        width: '90%',
      }}
    >
      <motion.div
        animate={{ rotate: 360 }}
        transition={{ duration: 2, repeat: Infinity, ease: 'linear' }}
        style={{
          width: 64,
          height: 64,
          borderRadius: apple.radius.xl,
          background: `linear-gradient(135deg, ${apple.blue}, ${apple.purple})`,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          margin: '0 auto 24px',
        }}
      >
        <Shield style={{ width: 28, height: 28, color: '#fff' }} />
      </motion.div>

      <h2 style={{ fontSize: 20, fontWeight: 600, color: apple.label, marginBottom: 8 }}>
        {isReauthenticating ? 'Refreshing Session' : 'Initializing SRE Command Center'}
      </h2>

      <p style={{ fontSize: 15, color: apple.secondaryLabel, marginBottom: 24, lineHeight: 1.4 }}>
        {isReauthenticating
          ? 'Updating your authentication tokens in the background...'
          : 'Setting up your secure session with Apple authentication...'
        }
      </p>

      <div style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        gap: 8,
        padding: '12px 20px',
        background: apple.fill,
        borderRadius: apple.radius.md,
      }}>
        <Loader2 style={{ width: 18, height: 18, color: apple.blue, animation: 'spin 1s linear infinite' }} />
        <span style={{ fontSize: 14, color: apple.secondaryLabel, fontWeight: 500 }}>
          {isReauthenticating ? 'Refreshing tokens...' : 'Authenticating with IdMS...'}
        </span>
      </div>

      {/* Manual retry button — appears after 8s in case the redirect is stuck */}
      {showRetry && (
        <motion.div
          initial={{ opacity: 0, y: 8 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.2 }}
          style={{ marginTop: 20 }}
        >
          <p style={{ fontSize: 12, color: apple.tertiaryLabel, marginBottom: 10 }}>
            Taking longer than expected?
          </p>
          <button
            onClick={handleRetry}
            style={{
              display: 'inline-flex', alignItems: 'center', gap: 6,
              padding: '8px 16px', borderRadius: apple.radius.md,
              background: `${apple.blue}15`, border: `0.5px solid ${apple.blue}40`,
              color: apple.blue, fontSize: 13, fontWeight: 600, cursor: 'pointer',
            }}
          >
            <RefreshCw style={{ width: 13, height: 13 }} />
            Try Again
          </button>
        </motion.div>
      )}

      <div style={{
        marginTop: 24,
        padding: 16,
        background: apple.tertiaryFill,
        borderRadius: apple.radius.sm,
        border: `0.5px solid ${apple.separator}`,
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
          <Shield style={{ width: 14, height: 14, color: apple.blue }} />
          <span style={{ fontSize: 12, fontWeight: 600, color: apple.label }}>
            Apple Single Sign-On
          </span>
        </div>
        <p style={{ fontSize: 11, color: apple.tertiaryLabel, lineHeight: 1.4, margin: 0 }}>
          {isReauthenticating
            ? 'Your session is being refreshed automatically.'
            : 'Secure authentication through Apple\'s enterprise system.'
          }
        </p>
      </div>
    </motion.div>

    <style>{`@keyframes spin { from { transform: rotate(0deg) } to { transform: rotate(360deg) } }`}</style>
  </div>
  )
}

// Protected route wrapper — auto-redirects to IdMS OAuth2 when unauthenticated
const ProtectedRoute = ({ children }: { children: React.ReactNode }) => {
  const isAuthenticated = useEnhancedAuthStore(selectIsAuthenticated)
  const isInitializing = useEnhancedAuthStore(selectIsInitializing)
  const location = useLocation()
  // Prevent duplicate redirects: once we've kicked off the navigation to IdMS,
  // don't do it again even if React re-renders this component (which it will, because
  // setting window.location.href is async and React may render several more times
  // before the browser actually navigates away).
  const redirectingRef = useRef(false)

  useEffect(() => {
    // Only redirect when auth is fully resolved (not still initializing) AND the user
    // is not authenticated AND we haven't already kicked off a redirect this render cycle.
    if (!isInitializing && !isAuthenticated && !redirectingRef.current) {
      redirectingRef.current = true
      const redirect = encodeURIComponent(location.pathname + location.search)
      window.location.href = `/api/v1/auth/oidc?redirect=${redirect}`
    }
  }, [isInitializing, isAuthenticated, location.pathname, location.search])

  // Reset redirect guard when we have a valid session (e.g. after OAuth callback)
  if (isAuthenticated) {
    redirectingRef.current = false
  }

  if (isInitializing || !isAuthenticated) {
    return <AuthenticationLoader />
  }

  return (
    <ErrorBoundary>
      <Layout>{children}</Layout>
    </ErrorBoundary>
  )
}

const ADMIN_ROLES = new Set(['admin', 'superadmin', 'super_admin', 'administrator', 'owner'])

// Admin-only route — any non-admin authenticated user is bounced to the dashboard
const AdminRoute = ({ children }: { children: React.ReactNode }) => {
  const user = useEnhancedAuthStore(selectUser)
  const isInitializing = useEnhancedAuthStore(selectIsInitializing)
  if (isInitializing) return <AuthenticationLoader />
  const isAdmin = user && ADMIN_ROLES.has(user.role?.toLowerCase?.() ?? '')
  if (!isAdmin) return <Navigate to="/dashboard" replace />
  return <>{children}</>
}

function App() {
  useEffect(() => {
    const initApp = async () => {
      console.log('🚀 Starting SRE Command Center...')
      
      // Setup global error handling first
      setupGlobalErrorHandling()
      
      // Setup activity tracking
      setupActivityTracking()
      
      // Initialize authentication (checks localStorage — does NOT call any network endpoint)
      const authSuccess = await initializeAuth()

      if (authSuccess) {
        try {
          await initializeDataLoading()
        } catch (error) {
          console.warn('⚠️ Data loading failed:', error)
        }
      }
    }
    
    initApp()
  }, [])

  return (
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        <BrowserRouter>
          <Routes>
            {/* Public routes */}
            <Route path="/oauth/callback" element={<OAuthCallbackPage />} />
            <Route path="/auth/oidc/callback" element={<OAuthCallbackPage />} />
            <Route path="/manual-login" element={<ManualLoginPage />} />
            
            {/* Test route */}
            <Route path="/floodgate-test" element={<FloodgateTestPage />} />
            
            {/* Revolutionary AIOps Dashboard - The Greatest Frontend */}
            <Route
              path="/revolutionary"
              element={
                <ProtectedRoute>
                  <RevolutionaryAIOpsDashboard />
                </ProtectedRoute>
              }
            />
            
            {/* All routes are now protected with automated authentication */}
            <Route
              path="/dashboard"
              element={
                <ProtectedRoute>
                  <DashboardPage />
                </ProtectedRoute>
              }
            />
            <Route
              path="/alerts"
              element={
                <ProtectedRoute>
                  <AlertsPage />
                </ProtectedRoute>
              }
            />
            <Route
              path="/incidents"
              element={
                <ProtectedRoute>
                  <IncidentsPage />
                </ProtectedRoute>
              }
            />
            <Route
              path="/analytics"
              element={
                <ProtectedRoute>
                  <AnalyticsPage />
                </ProtectedRoute>
              }
            />
            <Route
              path="/observability"
              element={
                <ProtectedRoute>
                  <ObservabilityPage />
                </ProtectedRoute>
              }
            />
            <Route
              path="/ai-chat"
              element={
                <ProtectedRoute>
                  <AIChatPage />
                </ProtectedRoute>
              }
            />
            <Route
              path="/settings"
              element={
                <ProtectedRoute>
                  <SettingsPage />
                </ProtectedRoute>
              }
            />
            <Route
              path="/oncall"
              element={
                <ProtectedRoute>
                  <OnCallSchedule />
                </ProtectedRoute>
              }
            />
            <Route
              path="/auto-discovery"
              element={
                <ProtectedRoute>
                  <AutoDiscoveryPage />
                </ProtectedRoute>
              }
            />
            <Route
              path="/topology-discovery"
              element={<Navigate to="/capacity-planning" replace />}
            />
            <Route
              path="/capacity-planning"
              element={
                <ProtectedRoute>
                  <CapacityPlanningPage />
                </ProtectedRoute>
              }
            />
            <Route
              path="/host-vm-mapping"
              element={
                <ProtectedRoute>
                  <HostVMMapping />
                </ProtectedRoute>
              }
            />
            <Route
              path="/integration-health"
              element={
                <ProtectedRoute>
                  <IntegrationHealthPage />
                </ProtectedRoute>
              }
            />
            <Route
              path="/admin"
              element={
                <ProtectedRoute>
                  <AdminRoute>
                    <AdminPage />
                  </AdminRoute>
                </ProtectedRoute>
              }
            />
            
            {/* New Keep AIOps Feature Routes */}
            <Route
              path="/deduplication"
              element={
                <ProtectedRoute>
                  <DeduplicationPage />
                </ProtectedRoute>
              }
            />
            <Route
              path="/mapping"
              element={
                <ProtectedRoute>
                  <MappingRulesPage />
                </ProtectedRoute>
              }
            />
            <Route
              path="/notifications"
              element={
                <ProtectedRoute>
                  <NotificationsHubPage />
                </ProtectedRoute>
              }
            />
            <Route
              path="/alert-quality"
              element={
                <ProtectedRoute>
                  <AlertQualityPage />
                </ProtectedRoute>
              }
            />
            <Route
              path="/api-keys"
              element={
                <ProtectedRoute>
                  <AdminRoute>
                    <APIKeyManagementPage />
                  </AdminRoute>
                </ProtectedRoute>
              }
            />
            
            {/* New Backend-Integrated Feature Routes */}
            <Route
              path="/workflows"
              element={
                <ProtectedRoute>
                  <WorkflowBuilderPage />
                </ProtectedRoute>
              }
            />
            <Route
              path="/aiops"
              element={
                <ProtectedRoute>
                  <AIOpsPage />
                </ProtectedRoute>
              }
            />
            <Route
              path="/rca"
              element={
                <ProtectedRoute>
                  <RCAInvestigationPage />
                </ProtectedRoute>
              }
            />
            
            {/* Kubernetes Intelligence Microservice Integration */}
            <Route
              path="/kubernetes"
              element={
                <ProtectedRoute>
                  <KubernetesManagementPage />
                </ProtectedRoute>
              }
            />
            
            {/* Redirect legacy topology routes to the unified intelligent topology page */}
            <Route path="/infrastructure" element={<Navigate to="/infra-topology" replace />} />
            <Route path="/k8s-explorer" element={<Navigate to="/infra-topology" replace />} />

            {/* KubeSense — Kubernetes Intelligence Platform */}
            <Route
              path="/kubesense"
              element={
                <ProtectedRoute>
                  <KubeSensePage />
                </ProtectedRoute>
              }
            />

            {/* /intelligence redirects to AIOps — content is now the Intelligence tab */}
            <Route path="/intelligence" element={<Navigate to="/aiops" replace />} />

            {/* Intelligent Infrastructure Topology - Redis-cached graph with search */}
            <Route
              path="/infra-topology"
              element={
                <ProtectedRoute>
                  <Suspense fallback={
                    <div style={{ minHeight: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                      <Loader2 style={{ width: 32, height: 32, color: '#007AFF', animation: 'spin 1s linear infinite' }} />
                    </div>
                  }>
                    <IntelligentInfraTopology />
                  </Suspense>
                </ProtectedRoute>
              }
            />
            
            {/* Default route - all paths lead to dashboard with authentication */}
            <Route path="*" element={<Navigate to="/dashboard" replace />} />
          </Routes>
          <Toaster position="top-right" />
        </BrowserRouter>
      </ThemeProvider>
    </QueryClientProvider>
  )
}

export default App
