import React, { useEffect, useRef } from 'react'
import { useNavigate } from 'react-router-dom'
import { motion } from 'framer-motion'
import { Loader2, Shield } from 'lucide-react'
import { useEnhancedAuthStore } from '@/stores/enhancedAuthStore'
import toast from 'react-hot-toast'

const apple = {
  blue: '#007AFF',
  purple: '#AF52DE',
  label: 'var(--color-text)',
  secondaryLabel: 'var(--color-text-secondary)',
  tertiaryLabel: 'var(--color-text-tertiary, #8E8E93)',
  separator: 'var(--color-separator, rgba(142, 142, 147, 0.12))',
  fill: 'var(--color-fill, rgba(142, 142, 147, 0.08))',
  tertiaryFill: 'rgba(142, 142, 147, 0.06)',
  background: 'var(--color-background)',
  secondaryBackground: 'var(--color-card, rgba(255, 255, 255, 0.8))',
  radius: { sm: 6, md: 10, lg: 12, xl: 16 },
} as const

export function OAuthCallbackPage() {
  const navigate = useNavigate()
  const setTokens = useEnhancedAuthStore((state) => state.setTokens)
  const handled = useRef(false)

  useEffect(() => {
    if (handled.current) return
    handled.current = true

    const handleCallback = async () => {
      const urlParams = new URLSearchParams(window.location.search)

      // Hard IdMS error — show error page, do NOT loop back to IDMS
      const error = urlParams.get('error')
      if (error) {
        const desc = urlParams.get('error_description') || error
        console.error('[OAuth] IdMS error:', error)
        toast.error(`Authentication error: ${desc}`, { duration: 8000 })
        navigate(
          `/manual-login?error=${encodeURIComponent(error)}&error_description=${encodeURIComponent(desc)}`,
          { replace: true }
        )
        return
      }

      // Normal path: backend issued a short-lived one-time exchange code
      const exchangeCode = urlParams.get('exchange_code')
      if (exchangeCode) {
        const exchangeEndpoint = urlParams.get('exchange_endpoint') || '/api/v1/auth/oidc/exchange'
        try {
          const resp = await fetch(`${exchangeEndpoint}?code=${encodeURIComponent(exchangeCode)}`, {
            method: 'GET',
            headers: { 'Content-Type': 'application/json' },
          })
          if (!resp.ok) {
            const body = await resp.text()
            throw new Error(`Exchange endpoint returned ${resp.status}: ${body}`)
          }
          const data = await resp.json()
          if (!data.success || !data.data) {
            throw new Error(data.message || 'Exchange response missing data')
          }
          const { tokens, user, redirect, idms_token, floodgate_token, floodgate_expires_in } = data.data
          setTokens(
            {
              access_token: tokens.access_token,
              refresh_token: tokens.refresh_token || undefined,
            },
            {
              id: user.id,
              email: user.email,
              full_name: user.full_name || user.username || user.email,
              role: user.role_name || 'viewer',
            }
          )
          // Store IDMS token for server-side features (e.g. CSS photo lookup)
          if (idms_token) {
            localStorage.setItem('oauth_id_token', idms_token)
          }
          // Store the Floodgate-scoped token (aud: sear-floodgate) for AI chat.
          // floodgate_token is the result of a server-side token exchange; fall back
          // to the raw IDMS token only if the exchange failed (user lacks access).
          const fgToken = floodgate_token || idms_token
          if (fgToken) {
            const expiresInMs = (floodgate_expires_in && floodgate_expires_in > 0)
              ? (floodgate_expires_in - 30) * 1000
              : 55 * 60 * 1000
            localStorage.setItem('floodgate_token', fgToken)
            localStorage.setItem('floodgate_token_expiry', new Date(Date.now() + expiresInMs).toISOString())
            localStorage.setItem('floodgate_token_source', floodgate_token ? 'idms-oauth2' : 'idms-direct')
            // Fresh login — clear stale model cache so AI chat fetches live Floodgate models
            localStorage.removeItem('ai_models_cache')
            // Clear the re-auth loop guard so the next token expiry can redirect again
            sessionStorage.removeItem('fg_last_reauth')
          }
          toast.success('Welcome back!')
          const destination =
            redirect && redirect.startsWith('/') && !redirect.startsWith('//')
              ? redirect
              : '/dashboard'
          navigate(destination, { replace: true })
        } catch (err: any) {
          const msg = (err?.message ?? 'Authentication exchange failed').substring(0, 200)
          console.error('[OAuth] Exchange failed')
          toast.error(`Login failed: ${msg}`, { duration: 8000 })
          // Go to manual-login NOT back to IDMS — avoids infinite redirect loop
          navigate(
            `/manual-login?error=exchange_failed&error_description=${encodeURIComponent(msg)}`,
            { replace: true }
          )
        }
        return
      }

      // Legacy: direct token in URL (old header-based flow)
      const token = urlParams.get('token')
      const userId = urlParams.get('user_id')
      if (token && userId) {
        const oauthIdToken = urlParams.get('oauth_id_token')
        if (oauthIdToken) {
          localStorage.setItem('oauth_id_token', oauthIdToken)
          localStorage.setItem('floodgate_token_expiry', new Date(Date.now() + 50 * 60 * 1000).toISOString())
        }
        setTokens(
          {
            access_token: token,
            refresh_token: urlParams.get('refresh_token') || undefined,
            oauth_id_token: oauthIdToken || undefined,
          },
          {
            id: userId,
            email: urlParams.get('email') || '',
            full_name:
              urlParams.get('full_name') || urlParams.get('username') || urlParams.get('email') || '',
            role: urlParams.get('role') || 'viewer',
          }
        )
        toast.success('Welcome back!')
        navigate('/dashboard', { replace: true })
        return
      }

      // Nothing recognizable in the URL — show error, do NOT re-trigger IDMS
      navigate(
        '/manual-login?error=missing_params&error_description=' +
          encodeURIComponent('No authentication data found in callback URL'),
        { replace: true }
      )
    }

    handleCallback()
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <div
      style={{
        minHeight: '100vh',
        background: apple.background,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        fontFamily:
          '-apple-system, BlinkMacSystemFont, "SF Pro Text", "Helvetica Neue", sans-serif',
      }}
    >
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
          Completing Authentication
        </h2>
        <p
          style={{
            fontSize: 15,
            color: apple.secondaryLabel,
            marginBottom: 24,
            lineHeight: 1.4,
          }}
        >
          Securely processing your credentials…
        </p>

        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            gap: 8,
            padding: '12px 20px',
            background: apple.fill,
            borderRadius: apple.radius.md,
          }}
        >
          <Loader2
            style={{
              width: 18,
              height: 18,
              color: apple.blue,
              animation: 'spin 1s linear infinite',
            }}
          />
          <span style={{ fontSize: 14, color: apple.secondaryLabel, fontWeight: 500 }}>
            Processing…
          </span>
        </div>

        <div
          style={{
            marginTop: 24,
            padding: 16,
            background: apple.tertiaryFill,
            borderRadius: apple.radius.sm,
            border: `0.5px solid ${apple.separator}`,
          }}
        >
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
            <Shield style={{ width: 14, height: 14, color: apple.blue }} />
            <span style={{ fontSize: 12, fontWeight: 600, color: apple.label }}>
              Secure Authentication
            </span>
          </div>
          <p style={{ fontSize: 11, color: apple.tertiaryLabel, lineHeight: 1.4, margin: 0 }}>
            Your credentials are encrypted and processed securely through Apple's authentication
            system.
          </p>
        </div>
      </motion.div>

      <style>{`@keyframes spin { from { transform: rotate(0deg) } to { transform: rotate(360deg) } }`}</style>
    </div>
  )
}
