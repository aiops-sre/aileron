import React, { Component, ReactNode } from 'react'
import { AlertTriangle, RefreshCw } from 'lucide-react'

// Aileron Design Tokens
const tokens = {
  red: '#FF3B30',
  blue: '#007AFF',
  label: 'var(--color-text)',
  secondaryLabel: 'var(--color-text-secondary)',
  tertiaryLabel: 'var(--color-text-tertiary, #8E8E93)',
  background: 'var(--color-background)',
  secondaryBackground: 'var(--color-card, rgba(255, 255, 255, 0.8))',
  separator: 'var(--color-separator, rgba(142, 142, 147, 0.12))',
  fill: 'var(--color-fill, rgba(142, 142, 147, 0.08))',
  radius: { sm: 6, md: 10, lg: 12, xl: 16 },
} as const

interface Props {
  children: ReactNode
  fallback?: ReactNode
  onError?: (error: Error, errorInfo: React.ErrorInfo) => void
}

interface State {
  hasError: boolean
  error?: Error
}

export class ErrorBoundary extends Component<Props, State> {
  constructor(props: Props) {
    super(props)
    this.state = { hasError: false }
  }

  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error }
  }

  componentDidCatch(error: Error, errorInfo: React.ErrorInfo) {
    // Log only the message — never the full error object, stack trace, or component
    // tree, as these can expose file paths, DB connection strings, and infra details.
    const safeMessage = error?.message?.substring(0, 200) ?? 'unknown error'
    console.error('[ErrorBoundary] Caught:', safeMessage)

    if (error.message.includes('401') || error.message.includes('Authentication')) {
      // Clear potentially stale auth data and redirect to re-authenticate
      sessionStorage.removeItem('access_token')
      sessionStorage.removeItem('user')
      setTimeout(() => { window.location.href = '/api/v1/auth/oidc?redirect=/' }, 2000)
    }

    if (this.props.onError) {
      this.props.onError(error, errorInfo)
    }
  }

  handleReset = () => {
    this.setState({ hasError: false, error: undefined })
  }

  render() {
    if (this.state.hasError) {
      // Custom fallback UI
      if (this.props.fallback) {
        return this.props.fallback
      }

      // Default Aileron-style error UI
      return (
        <div style={{
          minHeight: '400px',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          padding: '40px 20px',
          background: tokens.background,
        }}>
          <div style={{
            textAlign: 'center',
            maxWidth: '400px',
            background: tokens.secondaryBackground,
            borderRadius: tokens.radius.xl,
            padding: '32px',
            border: `0.5px solid ${tokens.separator}`,
            boxShadow: '0 8px 32px rgba(0,0,0,0.1)',
          }}>
            <div style={{
              width: '56px',
              height: '56px',
              borderRadius: tokens.radius.md,
              background: tokens.red,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              margin: '0 auto 20px',
            }}>
              <AlertTriangle style={{ width: 28, height: 28, color: '#fff' }} />
            </div>
            
            <h2 style={{
              fontSize: 20,
              fontWeight: 600,
              color: tokens.label,
              margin: '0 0 8px',
            }}>
              Something went wrong
            </h2>
            
            <p style={{
              fontSize: 15,
              color: tokens.secondaryLabel,
              lineHeight: 1.4,
              marginBottom: 20,
            }}>
              {this.state.error?.message || 'An unexpected error occurred'}
            </p>
            
            <div style={{ display: 'flex', gap: 8, justifyContent: 'center' }}>
              <button
                onClick={this.handleReset}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 6,
                  padding: '10px 16px',
                  borderRadius: tokens.radius.sm,
                  border: 'none',
                  background: tokens.blue,
                  color: '#fff',
                  fontSize: 14,
                  fontWeight: 500,
                  cursor: 'pointer',
                  transition: 'background 0.2s',
                }}
                onMouseEnter={(e) => {
                  e.currentTarget.style.background = '#0066D6'
                }}
                onMouseLeave={(e) => {
                  e.currentTarget.style.background = tokens.blue
                }}
              >
                <RefreshCw style={{ width: 14, height: 14 }} />
                Try Again
              </button>
              
              <button
                onClick={() => window.location.reload()}
                style={{
                  padding: '10px 16px',
                  borderRadius: tokens.radius.sm,
                  border: `0.5px solid ${tokens.separator}`,
                  background: tokens.fill,
                  color: tokens.label,
                  fontSize: 14,
                  fontWeight: 500,
                  cursor: 'pointer',
                }}
              >
                Reload Page
              </button>
            </div>
            
            {/* Error details for debugging */}
            {process.env.NODE_ENV === 'development' && this.state.error && (
              <details style={{
                marginTop: 20,
                padding: 12,
                background: tokens.fill,
                borderRadius: tokens.radius.sm,
                fontSize: 12,
                color: tokens.tertiaryLabel,
                textAlign: 'left',
              }}>
                <summary style={{ cursor: 'pointer', fontWeight: 500 }}>
                  Error Details (Dev Mode)
                </summary>
                <pre style={{
                  marginTop: 8,
                  padding: 8,
                  background: '#000',
                  color: '#fff',
                  borderRadius: 4,
                  overflow: 'auto',
                  fontSize: 11,
                  lineHeight: 1.4,
                }}>
                  {this.state.error.stack}
                </pre>
              </details>
            )}
          </div>
        </div>
      )
    }

    return this.props.children
  }
}

// Higher-order component wrapper for easy use
export const withErrorBoundary = <P extends object>(
  Component: React.ComponentType<P>,
  fallback?: ReactNode
) => {
  const WrappedComponent = (props: P) => (
    <ErrorBoundary fallback={fallback}>
      <Component {...props} />
    </ErrorBoundary>
  )
  
  WrappedComponent.displayName = `withErrorBoundary(${Component.displayName || Component.name})`
  return WrappedComponent
}

export const setupGlobalErrorHandling = () => {
  window.addEventListener('unhandledrejection', (event) => {
    const msg = event.reason?.message?.substring(0, 200) ?? 'unhandled rejection'
    console.error('[GlobalError] Unhandled rejection:', msg)
    event.preventDefault()
    if (msg.includes('ChunkLoadError') || msg.includes('Loading chunk')) {
      window.location.reload()
    }
  })

  window.addEventListener('error', (event) => {
    const msg = event.error?.message?.substring(0, 200) ?? 'unknown error'
    if (msg.includes('ResizeObserver loop limit exceeded')) return
    if (msg.includes('Non-Error promise rejection')) return
    console.error('[GlobalError]', msg)
  })
}