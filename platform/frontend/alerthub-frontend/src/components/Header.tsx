import React, { useState, useRef, useEffect } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { motion, AnimatePresence } from 'framer-motion'
import {
  Bell,
  Sun,
  Moon,
  User,
  Settings,
  LogOut,
  ChevronDown,
  ChevronUp,
  Menu,
  X,
  Brain,
} from 'lucide-react'
import { useEnhancedAuthStore, selectUser } from '@/stores/enhancedAuthStore'
import { useThemeStore } from '@/stores/themeStore'
import { useBreakpoint } from '@/hooks/useBreakpoint'

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Aileron Design Tokens
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

const tokens = {
  blue: '#007AFF',
  green: '#34C759',
  red: '#FF3B30',
  orange: '#FF9500',
  yellow: '#FFCC00',
  purple: '#AF52DE',
  gray: '#8E8E93',
  label: 'var(--color-text)',
  secondaryLabel: 'var(--color-text-secondary)',
  tertiaryLabel: 'var(--color-text-tertiary, #8E8E93)',
  quaternaryLabel: 'rgba(142, 142, 147, 0.4)',
  separator: 'var(--color-separator, rgba(142, 142, 147, 0.12))',
  fill: 'var(--color-fill, rgba(142, 142, 147, 0.08))',
  secondaryFill: 'rgba(142, 142, 147, 0.12)',
  tertiaryFill: 'rgba(142, 142, 147, 0.06)',
  background: 'var(--color-background)',
  secondaryBackground: 'var(--color-card, rgba(255, 255, 255, 0.8))',
  radius: { sm: 6, md: 10, lg: 12, xl: 16, '2xl': 20 },
} as const

export function Header() {
  const location = useLocation()
  const navigate = useNavigate()
  const user = useEnhancedAuthStore(selectUser)
  const logout = useEnhancedAuthStore((state) => state.logout)
  const { theme, toggleTheme } = useThemeStore()
  const { isMobile, isDesktop } = useBreakpoint()
  const [showUserMenu, setShowUserMenu] = useState(false)
  const [showMobileMenu, setShowMobileMenu] = useState(false)
  const [photoURL, setPhotoURL] = useState<string | null>(null)
  const menuRef = useRef<HTMLDivElement>(null)
  const mobileMenuRef = useRef<HTMLDivElement>(null)

  // Fetch profile photo from CSS via backend proxy
  useEffect(() => {
    const appToken = sessionStorage.getItem('access_token') || localStorage.getItem('access_token')
    if (!appToken) return

    const oidcToken = localStorage.getItem('oauth_id_token')
    const controller = new AbortController()
    const headers: Record<string, string> = {
      Authorization: `Bearer ${appToken}`,
    }
    if (oidcToken) headers['X-OIDC-Token'] = oidcToken

    fetch('/api/v1/users/me/photo', {
      headers,
      signal: controller.signal,
    })
      .then((r) => r.ok ? r.json() : null)
      .then((data) => {
        if (data?.success && data.data?.photo) {
          setPhotoURL(data.data.photo)
        }
      })
      .catch(() => {}) // silently fail — initials fallback is shown

    return () => controller.abort()
  }, [user?.id])

  // Close menu when clicking outside
  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(event.target as Node)) {
        setShowUserMenu(false)
      }
      if (mobileMenuRef.current && !mobileMenuRef.current.contains(event.target as Node)) {
        setShowMobileMenu(false)
      }
    }

    document.addEventListener('mousedown', handleClickOutside)
    return () => document.removeEventListener('mousedown', handleClickOutside)
  }, [])

  // Close mobile menu when resizing to desktop
  useEffect(() => {
    if (isDesktop) {
      setShowMobileMenu(false)
    }
  }, [isDesktop])

  const handleLogout = () => {
    logout()
    setShowUserMenu(false)
    setShowMobileMenu(false)
    navigate('/login')
  }

  const handleSettings = () => {
    setShowUserMenu(false)
    setShowMobileMenu(false)
    navigate('/settings')
  }

  const showSection = (section: string) => {
    const path = section === 'dashboard' ? '/dashboard' : `/${section}`
    navigate(path, { replace: false })
    setShowMobileMenu(false)
  }

  const getInitials = (name: string) => {
    const parts = name.split(' ')
    if (parts.length >= 2) {
      return (parts[0][0] + parts[1][0]).toUpperCase()
    }
    return name.substring(0, 2).toUpperCase()
  }

  const renderAvatar = (size: number, fontSize: number) => {
    if (photoURL) {
      return (
        <img
          src={photoURL}
          alt={user?.full_name || 'User'}
          style={{
            width: size, height: size,
            borderRadius: tokens.radius.sm,
            objectFit: 'cover',
            flexShrink: 0,
          }}
          onError={() => setPhotoURL(null)}
        />
      )
    }
    return (
      <div style={{
        width: size, height: size,
        borderRadius: tokens.radius.sm,
        background: `linear-gradient(135deg, ${tokens.blue}, ${tokens.purple})`,
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        fontSize, fontWeight: 600, color: '#fff', flexShrink: 0,
      }}>
        {user ? getInitials(user.full_name) : 'U'}
      </div>
    )
  }

  const navigationItems = [
    { path: '/dashboard',      label: 'Dashboard',     section: 'dashboard' },
    { path: '/alerts',         label: 'Alerts',        section: 'alerts' },
    { path: '/incidents',      label: 'Incidents',     section: 'incidents' },
    { path: '/aiops',          label: 'AIOps',         section: 'aiops' },
    { path: '/kubernetes',     label: 'Kubernetes',    section: 'kubernetes' },
    { path: '/kubesense',      label: 'KubeSense',     section: 'kubesense' },
    { path: '/capacity-planning', label: 'Capacity',   section: 'capacity-planning' },
    { path: '/infra-topology', label: 'Topology',      section: 'infra-topology' },
    { path: '/analytics',      label: 'Analytics',     section: 'analytics' },
    { path: '/ai-chat',        label: 'AI Chat',       section: 'ai-chat' },
    { path: '/admin',          label: 'Admin',         section: 'admin' },
  ]

  return (
    <>
      <header style={{
        position: 'sticky',
        top: 0,
        zIndex: 1000,
        height: 64,
        background: tokens.secondaryBackground,
        backdropFilter: 'saturate(180%) blur(20px)',
        WebkitBackdropFilter: 'saturate(180%) blur(20px)',
        borderBottom: `0.5px solid ${tokens.separator}`,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        padding: '0 20px',
        fontFamily: '-aileron-system, BlinkMacSystemFont, "SF Pro Text", "SF Pro Icons", "Helvetica Neue", sans-serif',
      }}>
        {/* Logo */}
        <motion.div
          whileHover={{ scale: 1.02 }}
          whileTap={{ scale: 0.98 }}
          onClick={() => navigate('/dashboard')}
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 6,
            cursor: 'pointer',
            fontSize: isMobile ? 15 : 18,
            fontWeight: 600,
            color: tokens.label,
          }}
        >
          <svg
            width="28"
            height="28"
            viewBox="0 0 26 26"
            fill="none"
            xmlns="http://www.w3.org/2000/svg"
            style={{
              color: tokens.label,
              transition: 'color 0.2s ease',
              flexShrink: 0,
            }}
          >
            <path
              d="M17.05 20.28c-.98.95-2.05.8-3.08.35-1.09-.46-2.09-.48-3.24 0-1.44.62-2.2.44-3.06-.35C2.79 15.25 3.51 7.59 9.05 7.31c1.35.07 2.29.74 3.08.8 1.18-.24 2.31-.93 3.57-.84 1.51.12 2.65.72 3.4 1.8-3.12 1.87-2.38 5.98.48 7.13-.57 1.5-1.31 2.99-2.53 4.08l-.05-.02v.02zM12.03 7.25c-.15-2.23 1.66-4.07 3.74-4.25.29 2.58-2.34 4.5-3.74 4.25z"
              fill="currentColor"
            />
          </svg>
          {!isMobile && 'SRE Command Center'}
          {isMobile && 'SRE'}
        </motion.div>

        {/* Desktop Navigation */}
        {!isMobile && (
          <nav style={{
            display: 'flex',
            alignItems: 'center',
            gap: 4
          }}>
            {navigationItems.map((item) => {
              const isActive = location.pathname === item.path ||
                (item.path === '/dashboard' && location.pathname === '/')
              const isHighlight = (item as any).highlight && !isActive

              return (
                <motion.button
                  key={item.path}
                  whileHover={{ scale: 1.02 }}
                  whileTap={{ scale: 0.98 }}
                  onClick={() => showSection(item.section)}
                  style={{
                    padding: '6px 12px',
                    borderRadius: tokens.radius.sm,
                    border: isHighlight ? `0.5px solid ${tokens.purple}40` : 'none',
                    background: isActive ? tokens.blue : isHighlight ? `${tokens.purple}12` : 'transparent',
                    color: isActive ? '#fff' : isHighlight ? tokens.purple : tokens.label,
                    fontSize: 13,
                    fontWeight: isActive ? 600 : 500,
                    cursor: 'pointer',
                    transition: 'all 0.15s ease',
                    opacity: isActive ? 1 : 0.8,
                    display: 'flex',
                    alignItems: 'center',
                    gap: 5,
                  }}
                  onMouseEnter={(e) => {
                    if (!isActive) {
                      e.currentTarget.style.background = isHighlight ? `${tokens.purple}20` : tokens.tertiaryFill
                      e.currentTarget.style.opacity = '1'
                    }
                  }}
                  onMouseLeave={(e) => {
                    if (!isActive) {
                      e.currentTarget.style.background = isHighlight ? `${tokens.purple}12` : 'transparent'
                      e.currentTarget.style.opacity = '0.8'
                    }
                  }}
                >
                  {isHighlight && <Brain style={{ width: 13, height: 13 }} />}
                  {item.label}
                </motion.button>
              )
            })}
          </nav>
        )}

        {/* Right Section */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          {/* Theme Toggle */}
          <motion.button
            whileHover={{ scale: 1.1 }}
            whileTap={{ scale: 0.95 }}
            onClick={toggleTheme}
            style={{
              width: 36,
              height: 36,
              borderRadius: tokens.radius.sm,
              border: 'none',
              background: tokens.fill,
              color: tokens.label,
              cursor: 'pointer',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              transition: 'all 0.15s ease',
            }}
            title={`Switch to ${theme === 'dark' ? 'light' : 'dark'} mode`}
            onMouseEnter={(e) => {
              e.currentTarget.style.background = tokens.secondaryFill
            }}
            onMouseLeave={(e) => {
              e.currentTarget.style.background = tokens.fill
            }}
          >
            {theme === 'dark' ? (
              <Sun style={{ width: 16, height: 16 }} />
            ) : (
              <Moon style={{ width: 16, height: 16 }} />
            )}
          </motion.button>

          {/* User Menu (desktop) or Avatar (mobile) */}
          {!isMobile ? (
            <div style={{ position: 'relative' }} ref={menuRef}>
              <motion.button
                whileHover={{ scale: 1.02 }}
                whileTap={{ scale: 0.98 }}
                onClick={() => setShowUserMenu(!showUserMenu)}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 8,
                  padding: '6px 12px 6px 8px',
                  borderRadius: tokens.radius.md,
                  border: 'none',
                  background: showUserMenu ? tokens.fill : 'transparent',
                  color: tokens.label,
                  cursor: 'pointer',
                  transition: 'all 0.15s ease',
                }}
                onMouseEnter={(e) => {
                  if (!showUserMenu) {
                    e.currentTarget.style.background = tokens.tertiaryFill
                  }
                }}
                onMouseLeave={(e) => {
                  if (!showUserMenu) {
                    e.currentTarget.style.background = 'transparent'
                  }
                }}
              >
                {renderAvatar(28, 12)}
                <span style={{ fontSize: 13, fontWeight: 500 }}>
                  {user?.full_name || 'User'}
                </span>
                {showUserMenu ? (
                  <ChevronUp style={{ width: 12, height: 12, color: tokens.tertiaryLabel }} />
                ) : (
                  <ChevronDown style={{ width: 12, height: 12, color: tokens.tertiaryLabel }} />
                )}
              </motion.button>

              {/* Dropdown Menu */}
              <AnimatePresence>
                {showUserMenu && (
                  <motion.div
                    initial={{ opacity: 0, y: -8, scale: 0.96 }}
                    animate={{ opacity: 1, y: 0, scale: 1 }}
                    exit={{ opacity: 0, y: -8, scale: 0.96 }}
                    transition={{ duration: 0.15, ease: 'easeOut' }}
                    style={{
                      position: 'absolute',
                      right: 0,
                      top: '100%',
                      marginTop: 8,
                      width: 200,
                      background: tokens.secondaryBackground,
                      borderRadius: tokens.radius.lg,
                      border: `0.5px solid ${tokens.separator}`,
                      boxShadow: '0 12px 48px rgba(0,0,0,0.15)',
                      overflow: 'hidden',
                    }}
                  >
                    {/* User Info */}
                    <div style={{
                      padding: 12,
                      borderBottom: `0.5px solid ${tokens.separator}`,
                      display: 'flex',
                      alignItems: 'center',
                      gap: 10,
                    }}>
                      {renderAvatar(40, 15)}
                      <div style={{ minWidth: 0 }}>
                        <div style={{ fontSize: 14, fontWeight: 600, color: tokens.label, marginBottom: 2, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                          {user?.full_name || 'User'}
                        </div>
                        <div style={{ fontSize: 12, color: tokens.secondaryLabel, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                          {user?.email || ''}
                        </div>
                      </div>
                    </div>

                    {/* Menu Items */}
                    <div style={{ padding: 4 }}>
                      <button
                        onClick={handleSettings}
                        style={{
                          width: '100%',
                          display: 'flex',
                          alignItems: 'center',
                          gap: 10,
                          padding: '8px 12px',
                          borderRadius: tokens.radius.sm,
                          border: 'none',
                          background: 'transparent',
                          color: tokens.label,
                          fontSize: 13,
                          cursor: 'pointer',
                          transition: 'background 0.15s ease',
                          textAlign: 'left',
                        }}
                        onMouseEnter={(e) => {
                          e.currentTarget.style.background = tokens.tertiaryFill
                        }}
                        onMouseLeave={(e) => {
                          e.currentTarget.style.background = 'transparent'
                        }}
                      >
                        <Settings style={{ width: 14, height: 14, color: tokens.secondaryLabel }} />
                        Settings
                      </button>

                      <div style={{ height: 1, background: tokens.separator, margin: '4px 8px' }} />

                      <button
                        onClick={handleLogout}
                        style={{
                          width: '100%',
                          display: 'flex',
                          alignItems: 'center',
                          gap: 10,
                          padding: '8px 12px',
                          borderRadius: tokens.radius.sm,
                          border: 'none',
                          background: 'transparent',
                          color: tokens.red,
                          fontSize: 13,
                          cursor: 'pointer',
                          transition: 'background 0.15s ease',
                          textAlign: 'left',
                        }}
                        onMouseEnter={(e) => {
                          e.currentTarget.style.background = `${tokens.red}15`
                        }}
                        onMouseLeave={(e) => {
                          e.currentTarget.style.background = 'transparent'
                        }}
                      >
                        <LogOut style={{ width: 14, height: 14 }} />
                        Sign Out
                      </button>
                    </div>
                  </motion.div>
                )}
              </AnimatePresence>
            </div>
          ) : (
            /* Mobile: avatar button (no name) */
            <motion.button
              whileTap={{ scale: 0.95 }}
              onClick={() => setShowUserMenu(!showUserMenu)}
              style={{
                padding: 0,
                border: 'none',
                background: 'transparent',
                cursor: 'pointer',
                display: 'flex',
                alignItems: 'center',
              }}
            >
              {renderAvatar(32, 13)}
            </motion.button>
          )}

          {/* Hamburger — mobile only */}
          {isMobile && (
            <motion.button
              whileTap={{ scale: 0.95 }}
              onClick={() => setShowMobileMenu((v) => !v)}
              style={{
                width: 36,
                height: 36,
                borderRadius: tokens.radius.sm,
                border: 'none',
                background: showMobileMenu ? tokens.fill : 'transparent',
                color: tokens.label,
                cursor: 'pointer',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
              }}
              aria-label="Toggle navigation menu"
            >
              {showMobileMenu ? (
                <X style={{ width: 18, height: 18 }} />
              ) : (
                <Menu style={{ width: 18, height: 18 }} />
              )}
            </motion.button>
          )}
        </div>
      </header>

      {/* Mobile Nav Panel */}
      <AnimatePresence>
        {isMobile && showMobileMenu && (
          <motion.div
            ref={mobileMenuRef}
            className="mobile-nav-panel"
            initial={{ opacity: 0, y: -8 }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0, y: -8 }}
            transition={{ duration: 0.2, ease: 'easeOut' }}
            style={{
              position: 'fixed',
              top: 64,
              left: 0,
              right: 0,
              bottom: 0,
              zIndex: 999,
              background: tokens.background,
              backdropFilter: 'saturate(180%) blur(20px)',
              WebkitBackdropFilter: 'saturate(180%) blur(20px)',
              overflowY: 'auto',
              display: 'flex',
              flexDirection: 'column',
              fontFamily: '-aileron-system, BlinkMacSystemFont, "SF Pro Text", "Helvetica Neue", sans-serif',
            }}
          >
            {/* Nav items */}
            <div style={{ flex: 1, padding: '8px 0' }}>
              {navigationItems.map((item, index) => {
                const isActive = location.pathname === item.path ||
                  (item.path === '/dashboard' && location.pathname === '/')
                const isHighlight = (item as any).highlight && !isActive

                return (
                  <motion.button
                    key={item.path}
                    initial={{ opacity: 0, x: -16 }}
                    animate={{ opacity: 1, x: 0 }}
                    transition={{ delay: index * 0.04, duration: 0.18 }}
                    onClick={() => showSection(item.section)}
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: 12,
                      width: '100%',
                      minHeight: 48,
                      padding: '14px 20px',
                      borderRadius: 0,
                      border: 'none',
                      borderBottom: `0.5px solid ${tokens.separator}`,
                      background: isActive ? `${tokens.blue}14` : 'transparent',
                      color: isActive ? tokens.blue : tokens.label,
                      fontSize: 16,
                      fontWeight: isActive ? 600 : 400,
                      cursor: 'pointer',
                      textAlign: 'left',
                      transition: 'background 0.15s ease',
                    }}
                  >
                    {isHighlight && <Brain style={{ width: 16, height: 16, color: tokens.purple }} />}
                    {item.label}
                    {isActive && (
                      <div style={{
                        marginLeft: 'auto',
                        width: 6,
                        height: 6,
                        borderRadius: '50%',
                        background: tokens.blue,
                        flexShrink: 0,
                      }} />
                    )}
                  </motion.button>
                )
              })}
            </div>

            {/* Bottom user section */}
            <div style={{
              borderTop: `0.5px solid ${tokens.separator}`,
              padding: '12px 0',
            }}>
              {/* User info row */}
              <div style={{
                display: 'flex',
                alignItems: 'center',
                gap: 12,
                padding: '10px 20px 14px',
                borderBottom: `0.5px solid ${tokens.separator}`,
              }}>
                {renderAvatar(40, 15)}
                <div style={{ minWidth: 0 }}>
                  <div style={{ fontSize: 15, fontWeight: 600, color: tokens.label, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {user?.full_name || 'User'}
                  </div>
                  <div style={{ fontSize: 13, color: tokens.secondaryLabel, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {user?.email || ''}
                  </div>
                </div>
              </div>

              {/* Settings */}
              <button
                onClick={handleSettings}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 12,
                  width: '100%',
                  minHeight: 48,
                  padding: '14px 20px',
                  border: 'none',
                  borderBottom: `0.5px solid ${tokens.separator}`,
                  background: 'transparent',
                  color: tokens.label,
                  fontSize: 16,
                  cursor: 'pointer',
                  textAlign: 'left',
                }}
              >
                <Settings style={{ width: 18, height: 18, color: tokens.secondaryLabel }} />
                Settings
              </button>

              {/* Sign Out */}
              <button
                onClick={handleLogout}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 12,
                  width: '100%',
                  minHeight: 48,
                  padding: '14px 20px',
                  border: 'none',
                  background: 'transparent',
                  color: tokens.red,
                  fontSize: 16,
                  cursor: 'pointer',
                  textAlign: 'left',
                }}
              >
                <LogOut style={{ width: 18, height: 18 }} />
                Sign Out
              </button>
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </>
  )
}
