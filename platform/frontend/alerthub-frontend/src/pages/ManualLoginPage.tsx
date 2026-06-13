import React, { useState, useEffect } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { motion } from 'framer-motion'
import { Eye, EyeOff, Loader2, Lock, Shield } from 'lucide-react'
import { useEnhancedAuthStore } from '@/stores/enhancedAuthStore'
import toast from 'react-hot-toast'

const tokens = {
  blue: '#007AFF',
  red: '#FF3B30',
  label: 'var(--color-text)',
  secondaryLabel: 'var(--color-text-secondary)',
  tertiaryLabel: 'var(--color-text-tertiary, #8E8E93)',
  background: 'var(--color-background)',
  secondaryBackground: 'var(--color-card, rgba(255, 255, 255, 0.8))',
  separator: 'var(--color-separator, rgba(142, 142, 147, 0.12))',
  fill: 'var(--color-fill, rgba(142, 142, 147, 0.08))',
  radius: { sm: 6, md: 10, lg: 12, xl: 16 },
} as const

export function ManualLoginPage() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const isAuthenticated = useEnhancedAuthStore((s) => s.isAuthenticated)
  const setTokens = useEnhancedAuthStore((s) => s.setTokens)

  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [showPassword, setShowPassword] = useState(false)
  const [isLoading, setIsLoading] = useState(false)

  const redirect = searchParams.get('redirect') || '/dashboard'

  useEffect(() => {
    if (isAuthenticated) navigate(redirect, { replace: true })
  }, [isAuthenticated, navigate, redirect])

  const handleLogin = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!username.trim() || !password) return
    setIsLoading(true)
    try {
      const res = await fetch('/api/v1/auth/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username: username.trim(), password }),
      })
      const data = await res.json()
      if (!res.ok || !data.success) {
        throw new Error(data.message || 'Invalid credentials')
      }
      const { tokens, user } = data.data
      setTokens(
        { access_token: tokens.access_token, refresh_token: tokens.refresh_token },
        { id: user.id, email: user.email, full_name: user.full_name || user.username, role: user.role_name || 'viewer' }
      )
      toast.success('Welcome back!')
      navigate(redirect, { replace: true })
    } catch (err: any) {
      toast.error(err?.message || 'Login failed')
    } finally {
      setIsLoading(false)
    }
  }

  return (
    <div style={{
      minHeight: '100vh',
      background: tokens.background,
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
      fontFamily: '-aileron-system, BlinkMacSystemFont, "SF Pro Text", "Helvetica Neue", sans-serif',
    }}>
      <motion.div
        initial={{ opacity: 0, y: 12 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.3 }}
        style={{
          background: tokens.secondaryBackground,
          borderRadius: tokens.radius.xl,
          border: `0.5px solid ${tokens.separator}`,
          padding: 40,
          width: '90%',
          maxWidth: 380,
          boxShadow: '0 20px 60px rgba(0,0,0,0.1)',
        }}
      >
        {/* Icon */}
        <div style={{ textAlign: 'center', marginBottom: 28 }}>
          <div style={{
            width: 56,
            height: 56,
            borderRadius: tokens.radius.lg,
            background: `linear-gradient(135deg, #636366, #3A3A3C)`,
            display: 'inline-flex',
            alignItems: 'center',
            justifyContent: 'center',
            marginBottom: 16,
          }}>
            <Lock style={{ width: 24, height: 24, color: '#fff' }} />
          </div>
          <h1 style={{ fontSize: 20, fontWeight: 700, color: tokens.label, margin: 0 }}>
            Manual Login
          </h1>
          <p style={{ fontSize: 13, color: tokens.tertiaryLabel, margin: '6px 0 0', lineHeight: 1.4 }}>
            Emergency access — normally sign in via Aileron SSO
          </p>
        </div>

        {/* Form */}
        <form onSubmit={handleLogin}>
          <div style={{ marginBottom: 12 }}>
            <input
              type="text"
              placeholder="Username"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              autoComplete="username"
              style={{
                width: '100%',
                padding: '12px 14px',
                borderRadius: tokens.radius.md,
                border: `0.5px solid ${tokens.separator}`,
                background: tokens.fill,
                color: tokens.label,
                fontSize: 15,
                outline: 'none',
                boxSizing: 'border-box',
              }}
            />
          </div>

          <div style={{ marginBottom: 20, position: 'relative' }}>
            <input
              type={showPassword ? 'text' : 'password'}
              placeholder="Password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="current-password"
              style={{
                width: '100%',
                padding: '12px 44px 12px 14px',
                borderRadius: tokens.radius.md,
                border: `0.5px solid ${tokens.separator}`,
                background: tokens.fill,
                color: tokens.label,
                fontSize: 15,
                outline: 'none',
                boxSizing: 'border-box',
              }}
            />
            <button
              type="button"
              onClick={() => setShowPassword((p) => !p)}
              style={{
                position: 'absolute', right: 12, top: '50%', transform: 'translateY(-50%)',
                background: 'none', border: 'none', cursor: 'pointer', color: tokens.tertiaryLabel,
                display: 'flex', alignItems: 'center',
              }}
            >
              {showPassword ? <EyeOff size={16} /> : <Eye size={16} />}
            </button>
          </div>

          <button
            type="submit"
            disabled={isLoading || !username || !password}
            style={{
              width: '100%',
              padding: '13px',
              borderRadius: tokens.radius.md,
              background: isLoading || !username || !password ? 'rgba(142,142,147,0.3)' : tokens.blue,
              color: '#fff',
              fontSize: 15,
              fontWeight: 600,
              border: 'none',
              cursor: isLoading || !username || !password ? 'not-allowed' : 'pointer',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              gap: 8,
            }}
          >
            {isLoading ? <><Loader2 size={16} style={{ animation: 'spin 1s linear infinite' }} /> Signing in...</> : 'Sign In'}
          </button>
        </form>

        {/* SSO link */}
        <div style={{ marginTop: 20, textAlign: 'center' }}>
          <a
            href="/api/v1/auth/oidc"
            style={{ fontSize: 13, color: tokens.blue, textDecoration: 'none', display: 'inline-flex', alignItems: 'center', gap: 5 }}
          >
            <Shield size={13} />
            Sign in with Aileron SSO instead
          </a>
        </div>
      </motion.div>

      <style>{`@keyframes spin { from { transform: rotate(0deg) } to { transform: rotate(360deg) } }`}</style>
    </div>
  )
}
