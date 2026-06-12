import { useState, useEffect } from 'react'
import { motion } from 'framer-motion'
import {
  User,
  Palette,
  Globe,
  Bell,
  Monitor,
  Zap,
  RotateCcw,
  Check,
  Cloud,
  CloudOff,
  Loader2,
  Sparkles,
  RefreshCw,
  CheckCircle,
  XCircle,
  Clock,
  Volume2,
  Mail,
  Settings,
  Timer,
  Eye,
  EyeOff,
  ChevronRight,
  ChevronDown,
} from 'lucide-react'
import { useSettingsStore } from '@/stores/settingsStore'
import toast from 'react-hot-toast'

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Apple Design Tokens (matching AdminPage)
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

const apple = {
  // System colors (matches Apple HIG)
  blue: '#007AFF',
  green: '#34C759',
  red: '#FF3B30',
  orange: '#FF9500',
  yellow: '#FFCC00',
  purple: '#AF52DE',
  pink: '#FF2D55',
  teal: '#5AC8FA',
  indigo: '#5856D6',
  gray: '#8E8E93',

  // Semantic
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
  groupedBackground: 'var(--color-fill, rgba(142, 142, 147, 0.06))',

  // Radius
  radius: {
    sm: 6,
    md: 10,
    lg: 12,
    xl: 16,
    '2xl': 20,
  },
} as const

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Apple Components
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

/** Apple iOS-style toggle switch */
function AppleToggle({ checked, onChange, disabled = false }: {
  checked: boolean
  onChange: (v: boolean) => void
  disabled?: boolean
}) {
  return (
    <button
      role="switch"
      aria-checked={checked}
      disabled={disabled}
      onClick={() => !disabled && onChange(!checked)}
      style={{
        position: 'relative',
        width: 51,
        height: 31,
        borderRadius: 31,
        border: 'none',
        cursor: disabled ? 'default' : 'pointer',
        background: checked ? apple.green : 'rgba(142, 142, 147, 0.24)',
        transition: 'background 0.25s ease',
        flexShrink: 0,
        opacity: disabled ? 0.5 : 1,
        padding: 0,
      }}
    >
      <span
        style={{
          position: 'absolute',
          top: 2,
          left: checked ? 22 : 2,
          width: 27,
          height: 27,
          borderRadius: '50%',
          background: '#FFFFFF',
          boxShadow: '0 1px 3px rgba(0,0,0,0.15), 0 1px 1px rgba(0,0,0,0.06)',
          transition: 'left 0.25s cubic-bezier(0.4, 0, 0.2, 1)',
        }}
      />
    </button>
  )
}

/** Apple-style segmented control */
function SegmentedControl({
  segments,
  selected,
  onChange,
}: {
  segments: { id: string; label: string }[]
  selected: string
  onChange: (id: string) => void
}) {
  return (
    <div
      style={{
        display: 'inline-flex',
        padding: 2,
        borderRadius: apple.radius.md,
        background: apple.fill,
        gap: 1,
      }}
    >
      {segments.map((seg) => {
        const active = seg.id === selected
        return (
          <button
            key={seg.id}
            onClick={() => onChange(seg.id)}
            style={{
              padding: '6px 16px',
              borderRadius: apple.radius.sm + 2,
              fontSize: 13,
              fontWeight: active ? 600 : 400,
              color: active ? apple.label : apple.secondaryLabel,
              background: active ? apple.secondaryBackground : 'transparent',
              border: 'none',
              cursor: 'pointer',
              transition: 'all 0.2s ease',
              boxShadow: active ? '0 1px 4px rgba(0,0,0,0.06), 0 0.5px 1px rgba(0,0,0,0.04)' : 'none',
              whiteSpace: 'nowrap',
            }}
          >
            {seg.label}
          </button>
        )
      })}
    </div>
  )
}

/** Apple-style grouped list container */
function GroupedList({ children, header, footer }: {
  children: React.ReactNode
  header?: string
  footer?: string
}) {
  return (
    <div style={{ marginBottom: 32 }}>
      {header && (
        <div style={{
          fontSize: 13,
          fontWeight: 400,
          color: apple.secondaryLabel,
          padding: '0 20px 8px',
          textTransform: 'uppercase',
          letterSpacing: '0.02em',
        }}>
          {header}
        </div>
      )}
      <div style={{
        background: apple.secondaryBackground,
        borderRadius: apple.radius.lg,
        border: `0.5px solid ${apple.separator}`,
        overflow: 'hidden',
      }}>
        {children}
      </div>
      {footer && (
        <div style={{
          fontSize: 12,
          color: apple.tertiaryLabel,
          padding: '8px 20px 0',
          lineHeight: 1.4,
        }}>
          {footer}
        </div>
      )}
    </div>
  )
}

/** Single row inside a GroupedList */
function GroupedRow({
  icon,
  iconColor,
  label,
  detail,
  accessory,
  onClick,
  isLast = false,
}: {
  icon?: React.ElementType
  iconColor?: string
  label: string
  detail?: string
  accessory?: React.ReactNode
  onClick?: () => void
  isLast?: boolean
}) {
  const Icon = icon
  return (
    <div
      onClick={onClick}
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 14,
        padding: '12px 16px',
        cursor: onClick ? 'pointer' : 'default',
        transition: 'background 0.15s ease',
        borderBottom: isLast ? 'none' : `0.5px solid ${apple.separator}`,
      }}
      onMouseEnter={(e) => {
        if (onClick) (e.currentTarget as HTMLElement).style.background = apple.tertiaryFill
      }}
      onMouseLeave={(e) => {
        (e.currentTarget as HTMLElement).style.background = 'transparent'
      }}
    >
      {Icon && (
        <div style={{
          width: 30,
          height: 30,
          borderRadius: 7,
          background: iconColor || apple.blue,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          flexShrink: 0,
        }}>
          <Icon style={{ width: 16, height: 16, color: '#fff' }} />
        </div>
      )}
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{
          fontSize: 15,
          fontWeight: 400,
          color: apple.label,
          lineHeight: 1.3,
        }}>
          {label}
        </div>
        {detail && (
          <div style={{ fontSize: 13, color: apple.tertiaryLabel, marginTop: 1 }}>
            {detail}
          </div>
        )}
      </div>
      {accessory || (onClick && (
        <ChevronRight style={{ width: 14, height: 14, color: apple.quaternaryLabel, flexShrink: 0 }} />
      ))}
    </div>
  )
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Sidebar Navigation
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

function Sidebar({
  items,
  selected,
  onSelect,
}: {
  items: { id: string; label: string; icon: React.ElementType; iconColor: string }[]
  selected: string
  onSelect: (id: string) => void
}) {
  return (
    <nav style={{
      width: 220,
      flexShrink: 0,
      padding: '8px 0',
    }}>
      {items.map((item) => {
        const active = item.id === selected
        const Icon = item.icon
        return (
          <button
            key={item.id}
            onClick={() => onSelect(item.id)}
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 10,
              width: '100%',
              padding: '7px 12px',
              borderRadius: apple.radius.sm,
              border: 'none',
              cursor: 'pointer',
              background: active ? 'rgba(0, 122, 255, 0.12)' : 'transparent',
              transition: 'background 0.15s',
              marginBottom: 1,
              textAlign: 'left',
            }}
            onMouseEnter={(e) => {
              if (!active) (e.currentTarget as HTMLElement).style.background = apple.tertiaryFill
            }}
            onMouseLeave={(e) => {
              if (!active) (e.currentTarget as HTMLElement).style.background = 'transparent'
            }}
          >
            <div style={{
              width: 26,
              height: 26,
              borderRadius: 6,
              background: item.iconColor,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              flexShrink: 0,
            }}>
              <Icon style={{ width: 14, height: 14, color: '#fff' }} />
            </div>
            <span style={{
              fontSize: 13,
              fontWeight: active ? 600 : 400,
              color: active ? apple.blue : apple.label,
              flex: 1,
            }}>
              {item.label}
            </span>
          </button>
        )
      })}
    </nav>
  )
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Floodgate Token Status Component
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

function FloodgateTokenStatus() {
  const [tokenInfo, setTokenInfo] = useState<{
    hasToken: boolean
    source?: string
    expiry?: string
    isExpired: boolean
    timeRemaining?: string
  }>({
    hasToken: false,
    isExpired: false,
  })

  useEffect(() => {
    const checkTokenStatus = () => {
      const token = localStorage.getItem('oauth_id_token')
      const source = localStorage.getItem('oauth_source')
      const expiryStr = localStorage.getItem('floodgate_token_expiry')
      
      if (!token) {
        setTokenInfo({
          hasToken: false,
          isExpired: false,
        })
        return
      }

      const hasToken = !!token
      let isExpired = false
      let timeRemaining: string | undefined

      if (expiryStr) {
        const expiryTime = new Date(expiryStr).getTime()
        const now = Date.now()
        isExpired = now >= expiryTime

        if (!isExpired) {
          const remaining = expiryTime - now
          const minutes = Math.floor(remaining / 60000)
          const hours = Math.floor(minutes / 60)
          
          if (hours > 0) {
            timeRemaining = `${hours}h ${minutes % 60}m`
          } else {
            timeRemaining = `${minutes}m`
          }
        }
      }

      setTokenInfo({
        hasToken,
        source: source || undefined,
        expiry: expiryStr || undefined,
        isExpired,
        timeRemaining,
      })
    }

    checkTokenStatus()
    const interval = setInterval(checkTokenStatus, 60000)
    return () => clearInterval(interval)
  }, [])

  const getSourceDisplay = (source?: string) => {
    const sources: Record<string, { label: string; icon: JSX.Element; color: string }> = {
      'mas-proxy': {
        label: 'MAS Proxy (Multi-Audience)',
        icon: <Cloud style={{ width: 16, height: 16 }} />,
        color: apple.green,
      },
      'headers': {
        label: 'MAS Headers',
        icon: <Cloud style={{ width: 16, height: 16 }} />,
        color: apple.blue,
      },
      'floodgate-cli': {
        label: 'AppleConnect CLI',
        icon: <Monitor style={{ width: 16, height: 16 }} />,
        color: apple.orange,
      },
      'database-cache': {
        label: 'Database Cache',
        icon: <Clock style={{ width: 16, height: 16 }} />,
        color: apple.yellow,
      },
      'local-mac': {
        label: 'Local Mac Service',
        icon: <Monitor style={{ width: 16, height: 16 }} />,
        color: apple.orange,
      },
      'backend': {
        label: 'Backend Service',
        icon: <Cloud style={{ width: 16, height: 16 }} />,
        color: apple.blue,
      },
    }

    return sources[source || ''] || {
      label: source || 'Unknown',
      icon: <Cloud style={{ width: 16, height: 16 }} />,
      color: apple.gray,
    }
  }

  const refreshToken = () => {
    localStorage.removeItem('floodgate_token_expiry')
    localStorage.removeItem('oauth_id_token')
    localStorage.removeItem('oauth_source')
    window.location.href = '/login'
  }

  if (!tokenInfo.hasToken) {
    return (
      <GroupedList>
        <div style={{
          display: 'flex',
          alignItems: 'start',
          gap: 12,
          padding: '16px',
        }}>
          <div style={{
            width: 30,
            height: 30,
            borderRadius: 7,
            background: apple.red,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            flexShrink: 0,
          }}>
            <XCircle style={{ width: 16, height: 16, color: '#fff' }} />
          </div>
          <div style={{ flex: 1 }}>
            <p style={{ fontSize: 15, fontWeight: 500, color: apple.red, margin: 0 }}>
              No Floodgate Token
            </p>
            <p style={{ fontSize: 13, color: apple.tertiaryLabel, marginTop: 4, marginBottom: 12 }}>
              AI features will use demo mode. Login via MAS to get a token.
            </p>
            <button
              onClick={refreshToken}
              style={{
                padding: '8px 16px',
                borderRadius: apple.radius.sm,
                border: 'none',
                background: apple.blue,
                color: '#fff',
                fontSize: 13,
                fontWeight: 500,
                cursor: 'pointer',
                display: 'flex',
                alignItems: 'center',
                gap: 6,
              }}
            >
              <RefreshCw style={{ width: 14, height: 14 }} />
              Login to Get Token
            </button>
          </div>
        </div>
      </GroupedList>
    )
  }

  const sourceInfo = getSourceDisplay(tokenInfo.source)

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {/* Token Status */}
      <GroupedList>
        <div style={{
          display: 'flex',
          alignItems: 'start',
          gap: 12,
          padding: '16px',
        }}>
          <div style={{
            width: 30,
            height: 30,
            borderRadius: 7,
            background: tokenInfo.isExpired ? apple.red : apple.green,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            flexShrink: 0,
          }}>
            {tokenInfo.isExpired ? (
              <XCircle style={{ width: 16, height: 16, color: '#fff' }} />
            ) : (
              <CheckCircle style={{ width: 16, height: 16, color: '#fff' }} />
            )}
          </div>
          <div style={{ flex: 1 }}>
            <p style={{ 
              fontSize: 15, 
              fontWeight: 500, 
              color: tokenInfo.isExpired ? apple.red : apple.green, 
              margin: 0 
            }}>
              {tokenInfo.isExpired ? 'Token Expired' : 'Token Active'}
            </p>
            <p style={{ fontSize: 13, color: apple.tertiaryLabel, marginTop: 4 }}>
              {tokenInfo.isExpired
                ? 'Your token has expired. Login again to use AI features.'
                : `Token valid for ${tokenInfo.timeRemaining || 'unknown time'}`}
            </p>
            {tokenInfo.isExpired && (
              <button
                onClick={refreshToken}
                style={{
                  padding: '8px 16px',
                  borderRadius: apple.radius.sm,
                  border: 'none',
                  background: apple.blue,
                  color: '#fff',
                  fontSize: 13,
                  fontWeight: 500,
                  cursor: 'pointer',
                  display: 'flex',
                  alignItems: 'center',
                  gap: 6,
                  marginTop: 12,
                }}
              >
                <RefreshCw style={{ width: 14, height: 14 }} />
                Refresh Token
              </button>
            )}
          </div>
        </div>
      </GroupedList>

      {/* Token Details */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
        <GroupedList>
          <div style={{ padding: '12px 16px' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
              <span style={{ color: sourceInfo.color }}>{sourceInfo.icon}</span>
              <p style={{ fontSize: 13, fontWeight: 500, color: apple.secondaryLabel, margin: 0 }}>
                Token Source
              </p>
            </div>
            <p style={{ fontSize: 15, fontWeight: 500, color: sourceInfo.color, margin: 0 }}>
              {sourceInfo.label}
            </p>
          </div>
        </GroupedList>

        <GroupedList>
          <div style={{ padding: '12px 16px' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
              <Clock style={{ width: 16, height: 16, color: apple.gray }} />
              <p style={{ fontSize: 13, fontWeight: 500, color: apple.secondaryLabel, margin: 0 }}>
                Expires At
              </p>
            </div>
            <p style={{ fontSize: 15, fontWeight: 500, color: apple.label, margin: 0 }}>
              {tokenInfo.expiry
                ? new Date(tokenInfo.expiry).toLocaleString()
                : 'Unknown'}
            </p>
          </div>
        </GroupedList>
      </div>

      {/* Info */}
      <GroupedList footer="Token sources are prioritized: MAS Proxy > MAS Headers > CLI > Cache > Local Service">
        <div style={{ padding: '12px 16px' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
            <Sparkles style={{ width: 16, height: 16, color: apple.blue }} />
            <span style={{ fontSize: 13, fontWeight: 500, color: apple.secondaryLabel }}>
              Token Source Information
            </span>
          </div>
          <div style={{ fontSize: 12, color: apple.tertiaryLabel, lineHeight: 1.4 }}>
            <strong>MAS Proxy:</strong> Multi-audience token from MAS (fastest, recommended)<br />
            <strong>MAS Headers:</strong> OAuth token from MAS authentication headers<br />
            <strong>AppleConnect CLI:</strong> Token fetched via backend CLI command
          </div>
        </div>
      </GroupedList>
    </div>
  )
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Settings Panels
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

function AppearancePanel({ settings, updateSettings }: { settings: any; updateSettings: any }) {
  const themes = [
    { id: 'light', label: 'Light' },
    { id: 'dark', label: 'Dark' },
    { id: 'auto', label: 'Auto' },
  ]

  const accentColors = [
    { value: '#007AFF', label: 'Blue' },
    { value: '#5856D6', label: 'Purple' },
    { value: '#34C759', label: 'Green' },
    { value: '#FF9500', label: 'Orange' },
    { value: '#FF3B30', label: 'Red' },
    { value: '#5AC8FA', label: 'Teal' },
  ]

  return (
    <div>
      <div style={{ marginBottom: 20 }}>
        <h2 style={{ fontSize: 22, fontWeight: 700, color: apple.label, margin: 0 }}>Appearance</h2>
        <p style={{ fontSize: 13, color: apple.secondaryLabel, marginTop: 2 }}>
          Customize the look and feel of your interface
        </p>
      </div>

      {/* Theme Selection */}
      <GroupedList header="Color Scheme">
        <div style={{ padding: '16px' }}>
          <SegmentedControl
            segments={themes}
            selected={settings.theme}
            onChange={(theme) => updateSettings({ theme })}
          />
        </div>
      </GroupedList>

      {/* Accent Color */}
      <GroupedList header="Accent Color">
        <div style={{ padding: '16px' }}>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(6, 1fr)', gap: 8 }}>
            {accentColors.map((color) => (
              <button
                key={color.value}
                onClick={() => updateSettings({ accentColor: color.value })}
                style={{
                  width: '100%',
                  aspectRatio: '1',
                  borderRadius: apple.radius.sm,
                  border: settings.accentColor === color.value 
                    ? `2px solid ${apple.label}` 
                    : `1px solid ${apple.separator}`,
                  background: color.value,
                  cursor: 'pointer',
                  transition: 'all 0.15s',
                  transform: settings.accentColor === color.value ? 'scale(1.1)' : 'scale(1)',
                }}
                title={color.label}
              />
            ))}
          </div>
        </div>
      </GroupedList>

      {/* Other Options */}
      <GroupedList>
        <GroupedRow
          icon={Monitor}
          iconColor={apple.purple}
          label="Compact Mode"
          detail="Reduce spacing for denser information display"
          accessory={
            <AppleToggle 
              checked={settings.compactMode} 
              onChange={(v) => updateSettings({ compactMode: v })} 
            />
          }
        />
      </GroupedList>
    </div>
  )
}

function TimezonePanel({ settings, updateSettings }: { settings: any; updateSettings: any }) {
  const timezones = [
    { value: 'America/New_York', label: 'Eastern Time (ET)' },
    { value: 'America/Chicago', label: 'Central Time (CT)' },
    { value: 'America/Denver', label: 'Mountain Time (MT)' },
    { value: 'America/Los_Angeles', label: 'Pacific Time (PT)' },
    { value: 'America/Anchorage', label: 'Alaska Time (AKT)' },
    { value: 'Pacific/Honolulu', label: 'Hawaii Time (HT)' },
    { value: 'Europe/London', label: 'London (GMT)' },
    { value: 'Europe/Paris', label: 'Paris (CET)' },
    { value: 'Europe/Berlin', label: 'Berlin (CET)' },
    { value: 'Europe/Moscow', label: 'Moscow (MSK)' },
    { value: 'Asia/Dubai', label: 'Dubai (GST)' },
    { value: 'Asia/Kolkata', label: 'India (IST)' },
    { value: 'Asia/Singapore', label: 'Singapore (SGT)' },
    { value: 'Asia/Tokyo', label: 'Tokyo (JST)' },
    { value: 'Asia/Shanghai', label: 'Shanghai (CST)' },
    { value: 'Australia/Sydney', label: 'Sydney (AEDT)' },
    { value: 'Pacific/Auckland', label: 'Auckland (NZDT)' },
  ]

  return (
    <div>
      <div style={{ marginBottom: 20 }}>
        <h2 style={{ fontSize: 22, fontWeight: 700, color: apple.label, margin: 0 }}>Time & Region</h2>
        <p style={{ fontSize: 13, color: apple.secondaryLabel, marginTop: 2 }}>
          Configure timezone and time display preferences
        </p>
      </div>

      <GroupedList header="Timezone">
        <div style={{ padding: '12px 16px', borderBottom: `0.5px solid ${apple.separator}` }}>
          <select
            value={settings.timezone}
            onChange={(e) => updateSettings({ timezone: e.target.value })}
            style={{
              width: '100%',
              height: 38,
              borderRadius: apple.radius.md,
              border: `0.5px solid ${apple.separator}`,
              background: apple.tertiaryFill,
              padding: '0 32px 0 12px',
              fontSize: 15,
              color: apple.label,
              outline: 'none',
              appearance: 'none',
              cursor: 'pointer',
            }}
          >
            {timezones.map((tz) => (
              <option key={tz.value} value={tz.value}>
                {tz.label}
              </option>
            ))}
          </select>
        </div>
        <div style={{ padding: '12px 16px' }}>
          <div style={{ fontSize: 13, color: apple.tertiaryLabel }}>
            Current time:{' '}
            {new Date().toLocaleTimeString('en-US', {
              timeZone: settings.timezone,
              hour: '2-digit',
              minute: '2-digit',
              hour12: !settings.use24HourTime,
            })}
          </div>
        </div>
      </GroupedList>

      <GroupedList>
        <GroupedRow
          icon={Timer}
          iconColor={apple.blue}
          label="24-Hour Time Format"
          detail="Display time in 24-hour format (e.g., 13:00 instead of 1:00 PM)"
          accessory={
            <AppleToggle 
              checked={settings.use24HourTime} 
              onChange={(v) => updateSettings({ use24HourTime: v })} 
            />
          }
          isLast
        />
      </GroupedList>
    </div>
  )
}

function NotificationsPanel({ settings, updateSettings }: { settings: any; updateSettings: any }) {
  return (
    <div>
      <div style={{ marginBottom: 20 }}>
        <h2 style={{ fontSize: 22, fontWeight: 700, color: apple.label, margin: 0 }}>Notifications</h2>
        <p style={{ fontSize: 13, color: apple.secondaryLabel, marginTop: 2 }}>
          Configure alert notifications and preferences
        </p>
      </div>

      <GroupedList header="Notification Types">
        <GroupedRow
          icon={Volume2}
          iconColor={apple.red}
          label="Sound Notifications"
          detail="Play sound when new alerts arrive"
          accessory={
            <AppleToggle 
              checked={settings.soundEnabled} 
              onChange={(v) => updateSettings({ soundEnabled: v })} 
            />
          }
        />
        <GroupedRow
          icon={Monitor}
          iconColor={apple.blue}
          label="Desktop Notifications"
          detail="Show browser notifications for new alerts"
          accessory={
            <AppleToggle 
              checked={settings.desktopNotifications} 
              onChange={(v) => updateSettings({ desktopNotifications: v })} 
            />
          }
        />
        <GroupedRow
          icon={Mail}
          iconColor={apple.green}
          label="Email Notifications"
          detail="Receive email alerts for important events"
          accessory={
            <AppleToggle 
              checked={settings.emailNotifications} 
              onChange={(v) => updateSettings({ emailNotifications: v })} 
            />
          }
          isLast
        />
      </GroupedList>

      <GroupedList header="Alert Severity">
        <GroupedRow
          icon={Zap}
          iconColor={apple.red}
          label="Critical Alert Notifications"
          detail="Get notified immediately for critical severity alerts"
          accessory={
            <AppleToggle 
              checked={settings.notifyOnCritical} 
              onChange={(v) => updateSettings({ notifyOnCritical: v })} 
            />
          }
        />
        <GroupedRow
          icon={Bell}
          iconColor={apple.orange}
          label="High Priority Notifications"
          detail="Get notified for high severity alerts"
          accessory={
            <AppleToggle 
              checked={settings.notifyOnHigh} 
              onChange={(v) => updateSettings({ notifyOnHigh: v })} 
            />
          }
        />
        <GroupedRow
          icon={User}
          iconColor={apple.blue}
          label="Assignment Notifications"
          detail="Get notified when alerts are assigned to you"
          accessory={
            <AppleToggle 
              checked={settings.notifyOnAssignment} 
              onChange={(v) => updateSettings({ notifyOnAssignment: v })} 
            />
          }
          isLast
        />
      </GroupedList>
    </div>
  )
}

function DisplayPanel({ settings, updateSettings }: { settings: any; updateSettings: any }) {
  return (
    <div>
      <div style={{ marginBottom: 20 }}>
        <h2 style={{ fontSize: 22, fontWeight: 700, color: apple.label, margin: 0 }}>Display</h2>
        <p style={{ fontSize: 13, color: apple.secondaryLabel, marginTop: 2 }}>
          Control how information is displayed
        </p>
      </div>

      <GroupedList header="Alert Display">
        <div style={{ padding: '12px 16px', borderBottom: `0.5px solid ${apple.separator}` }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <div>
              <div style={{ fontSize: 15, color: apple.label }}>Alerts Per Page</div>
              <div style={{ fontSize: 13, color: apple.tertiaryLabel, marginTop: 1 }}>
                Number of alerts to display per page
              </div>
            </div>
            <select
              value={settings.alertsPerPage}
              onChange={(e) => updateSettings({ alertsPerPage: Number(e.target.value) })}
              style={{
                height: 32,
                borderRadius: apple.radius.sm,
                border: `0.5px solid ${apple.separator}`,
                background: apple.tertiaryFill,
                padding: '0 24px 0 8px',
                fontSize: 15,
                color: apple.label,
                outline: 'none',
                appearance: 'none',
                cursor: 'pointer',
              }}
            >
              <option value="10">10</option>
              <option value="20">20</option>
              <option value="50">50</option>
              <option value="100">100</option>
            </select>
          </div>
        </div>
        <div style={{ padding: '12px 16px' }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <div>
              <div style={{ fontSize: 15, color: apple.label }}>Auto-Refresh Interval</div>
              <div style={{ fontSize: 13, color: apple.tertiaryLabel, marginTop: 1 }}>
                How often to check for new alerts
              </div>
            </div>
            <select
              value={settings.autoRefreshInterval}
              onChange={(e) => updateSettings({ autoRefreshInterval: Number(e.target.value) })}
              style={{
                height: 32,
                borderRadius: apple.radius.sm,
                border: `0.5px solid ${apple.separator}`,
                background: apple.tertiaryFill,
                padding: '0 24px 0 8px',
                fontSize: 15,
                color: apple.label,
                outline: 'none',
                appearance: 'none',
                cursor: 'pointer',
              }}
            >
              <option value="5">5 seconds</option>
              <option value="10">10 seconds</option>
              <option value="30">30 seconds</option>
              <option value="60">1 minute</option>
              <option value="300">5 minutes</option>
            </select>
          </div>
        </div>
      </GroupedList>

      <GroupedList header="Content">
        <GroupedRow
          icon={Eye}
          iconColor={apple.blue}
          label="Show Resolved Alerts"
          detail="Display resolved alerts in the default view"
          accessory={
            <AppleToggle 
              checked={settings.showResolvedAlerts} 
              onChange={(v) => updateSettings({ showResolvedAlerts: v })} 
            />
          }
        />
        <GroupedRow
          icon={Settings}
          iconColor={apple.gray}
          label="Show Alert Metadata"
          detail="Display extracted metadata badges on alert cards"
          accessory={
            <AppleToggle 
              checked={settings.showMetadata} 
              onChange={(v) => updateSettings({ showMetadata: v })} 
            />
          }
          isLast
        />
      </GroupedList>
    </div>
  )
}

function AdvancedPanel({ settings, updateSettings, onReset, onSync, isLoading, isSyncing, lastSynced }: { 
  settings: any; 
  updateSettings: any; 
  onReset: () => void;
  onSync: () => void;
  isLoading: boolean;
  isSyncing: boolean;
  lastSynced?: Date;
}) {
  return (
    <div>
      <div style={{ marginBottom: 20 }}>
        <h2 style={{ fontSize: 22, fontWeight: 700, color: apple.label, margin: 0 }}>Advanced</h2>
        <p style={{ fontSize: 13, color: apple.secondaryLabel, marginTop: 2 }}>
          Advanced features and system settings
        </p>
      </div>

      {/* Sync Status */}
      <GroupedList header="Data Synchronization">
        <div style={{ padding: '16px' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12 }}>
            {isSyncing ? (
              <Loader2 style={{ width: 16, height: 16, color: apple.blue, animation: 'spin 1s linear infinite' }} />
            ) : lastSynced ? (
              <Cloud style={{ width: 16, height: 16, color: apple.green }} />
            ) : (
              <CloudOff style={{ width: 16, height: 16, color: apple.gray }} />
            )}
            <span style={{ fontSize: 13, color: apple.secondaryLabel }}>
              {isSyncing 
                ? 'Syncing to database...' 
                : lastSynced 
                  ? `Last synced: ${lastSynced.toLocaleString()}` 
                  : 'Settings stored locally only'}
            </span>
          </div>
          <div style={{ display: 'flex', gap: 8 }}>
            <button
              onClick={onSync}
              disabled={isSyncing}
              style={{
                flex: 1,
                padding: '8px 16px',
                borderRadius: apple.radius.sm,
                border: 'none',
                background: apple.blue,
                color: '#fff',
                fontSize: 13,
                fontWeight: 500,
                cursor: isSyncing ? 'default' : 'pointer',
                opacity: isSyncing ? 0.5 : 1,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                gap: 6,
              }}
            >
              <Cloud style={{ width: 14, height: 14 }} />
              Sync Now
            </button>
            <button
              onClick={onReset}
              disabled={isSyncing}
              style={{
                flex: 1,
                padding: '8px 16px',
                borderRadius: apple.radius.sm,
                border: `0.5px solid ${apple.separator}`,
                background: apple.fill,
                color: apple.label,
                fontSize: 13,
                fontWeight: 500,
                cursor: isSyncing ? 'default' : 'pointer',
                opacity: isSyncing ? 0.5 : 1,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                gap: 6,
              }}
            >
              <RotateCcw style={{ width: 14, height: 14 }} />
              Reset All
            </button>
          </div>
        </div>
      </GroupedList>

      {/* Advanced Features */}
      <GroupedList header="Features">
        <GroupedRow
          icon={Sparkles}
          iconColor={apple.purple}
          label="AI Assistant"
          detail="Enable AI-powered chat assistant for troubleshooting"
          accessory={
            <AppleToggle 
              checked={settings.enableAIAssistant} 
              onChange={(v) => updateSettings({ enableAIAssistant: v })} 
            />
          }
        />
        <GroupedRow
          icon={Zap}
          iconColor={apple.orange}
          label="Keyboard Shortcuts"
          detail="Enable keyboard shortcuts for faster navigation"
          accessory={
            <AppleToggle 
              checked={settings.enableKeyboardShortcuts} 
              onChange={(v) => updateSettings({ enableKeyboardShortcuts: v })} 
            />
          }
        />
        <GroupedRow
          icon={Settings}
          iconColor={apple.gray}
          label="Debug Mode"
          detail="Show additional debugging information in console"
          accessory={
            <AppleToggle 
              checked={settings.debugMode} 
              onChange={(v) => updateSettings({ debugMode: v })} 
            />
          }
          isLast
        />
      </GroupedList>

      {/* Floodgate Token Status */}
      <GroupedList header="AI Token Status">
        <div style={{ padding: '16px' }}>
          <FloodgateTokenStatus />
        </div>
      </GroupedList>
    </div>
  )
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Main Settings Page
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

export function SettingsPage() {
  const { 
    settings, 
    isLoading,
    isSyncing, 
    lastSynced,
    updateSettings, 
    resetSettings,
    loadSettingsFromDB,
    syncSettings 
  } = useSettingsStore()
  
  const [activeSection, setActiveSection] = useState('appearance')

  useEffect(() => {
    const token = sessionStorage.getItem('access_token') || localStorage.getItem('access_token')
    if (token) {
      loadSettingsFromDB().catch(() => {
        console.log('Settings: Using local storage only')
      })
    }
  }, [])

  const handleSettingChange = async (updates: any) => {
    try {
      await updateSettings(updates)
    } catch (error) {
      toast.error('Failed to save setting')
    }
  }

  const handleReset = async () => {
    if (confirm('Are you sure you want to reset all settings to defaults?')) {
      try {
        await resetSettings()
        toast.success('Settings reset to defaults')
      } catch (error) {
        toast.error('Failed to reset settings')
      }
    }
  }

  const handleManualSync = async () => {
    try {
      await syncSettings()
      toast.success('Settings synced to database')
    } catch (error) {
      toast.error('Failed to sync settings')
    }
  }

  const sidebarItems = [
    { id: 'appearance', label: 'Appearance', icon: Palette, iconColor: apple.pink },
    { id: 'timezone', label: 'Time & Region', icon: Globe, iconColor: apple.blue },
    { id: 'notifications', label: 'Notifications', icon: Bell, iconColor: apple.orange },
    { id: 'display', label: 'Display', icon: Monitor, iconColor: apple.green },
    { id: 'advanced', label: 'Advanced', icon: Zap, iconColor: apple.purple },
  ]

  if (isLoading) {
    return (
      <div style={{
        minHeight: '100vh',
        background: apple.background,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
      }}>
        <div style={{ textAlign: 'center' }}>
          <Loader2 style={{ width: 32, height: 32, color: apple.blue, animation: 'spin 1s linear infinite', margin: '0 auto 16px' }} />
          <p style={{ fontSize: 15, color: apple.secondaryLabel }}>Loading Settings...</p>
        </div>
      </div>
    )
  }

  return (
    <div style={{
      minHeight: '100vh',
      background: apple.background,
    }}>
      {/* macOS System Settings layout: sidebar + content */}
      <div style={{
        display: 'flex',
        maxWidth: 960,
        margin: '0 auto',
        padding: '24px 16px',
        gap: 32,
        minHeight: '100vh',
      }}>
        {/* Sidebar */}
        <div style={{
          position: 'sticky',
          top: 24,
          alignSelf: 'flex-start',
        }}>
          <div style={{
            fontSize: 28,
            fontWeight: 700,
            color: apple.label,
            padding: '4px 12px 20px',
            letterSpacing: '-0.02em',
          }}>
            Settings
          </div>

          {/* Sync Status Indicator */}
          <div style={{
            padding: '8px 12px 16px',
            marginBottom: 8,
          }}>
            <div style={{
              display: 'flex',
              alignItems: 'center',
              gap: 6,
              padding: '6px 8px',
              borderRadius: apple.radius.sm,
              background: apple.tertiaryFill,
            }}>
              {isSyncing ? (
                <Loader2 style={{ width: 12, height: 12, color: apple.blue, animation: 'spin 1s linear infinite' }} />
              ) : lastSynced ? (
                <Cloud style={{ width: 12, height: 12, color: apple.green }} />
              ) : (
                <CloudOff style={{ width: 12, height: 12, color: apple.gray }} />
              )}
              <span style={{ fontSize: 11, color: apple.tertiaryLabel }}>
                {isSyncing ? 'Syncing...' : lastSynced ? 'Synced' : 'Local only'}
              </span>
            </div>
          </div>

          <Sidebar items={sidebarItems} selected={activeSection} onSelect={setActiveSection} />
        </div>

        {/* Content */}
        <div style={{ flex: 1, minWidth: 0 }}>
          <motion.div
            key={activeSection}
            initial={{ opacity: 0, x: 12 }}
            animate={{ opacity: 1, x: 0 }}
            exit={{ opacity: 0, x: -12 }}
            transition={{ duration: 0.2, ease: 'easeOut' }}
          >
            {activeSection === 'appearance' && (
              <AppearancePanel settings={settings} updateSettings={handleSettingChange} />
            )}
            {activeSection === 'timezone' && (
              <TimezonePanel settings={settings} updateSettings={handleSettingChange} />
            )}
            {activeSection === 'notifications' && (
              <NotificationsPanel settings={settings} updateSettings={handleSettingChange} />
            )}
            {activeSection === 'display' && (
              <DisplayPanel settings={settings} updateSettings={handleSettingChange} />
            )}
            {activeSection === 'advanced' && (
              <AdvancedPanel
                settings={settings}
                updateSettings={handleSettingChange}
                onReset={handleReset}
                onSync={handleManualSync}
                isLoading={isLoading}
                isSyncing={isSyncing}
                lastSynced={lastSynced ? new Date(lastSynced) : undefined}
              />
            )}
          </motion.div>
        </div>
      </div>

      {/* Global keyframe for spinner */}
      <style>{`@keyframes spin { from { transform: rotate(0deg) } to { transform: rotate(360deg) } }`}</style>
    </div>
  )
}
