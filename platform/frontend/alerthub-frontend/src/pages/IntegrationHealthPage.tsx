import React, { useState, useEffect, useCallback } from 'react'
import { motion } from 'framer-motion'
import {
  Activity,
  CheckCircle,
  XCircle,
  AlertTriangle,
  Clock,
  RefreshCw,
  Zap,
  Database,
  Globe,
  Server,
  WifiOff,
  Loader2,
  Brain,
  Network,
  TrendingUp,
  TrendingDown,
  BarChart3,
} from 'lucide-react'
import { platformHealthApi } from '@/lib/api'

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Design tokens
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
  radius: { sm: 6, md: 10, lg: 12, xl: 16 },
} as const

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Types
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

interface ServiceHealth {
  name: string
  type: string
  status: 'healthy' | 'unhealthy' | 'offline'
  healthy: boolean
  response_time_ms: number
  endpoint: string
  last_checked: string
  error?: string
}

interface HealthSummary {
  total: number
  healthy: number
  unhealthy: number
  health_score: number
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Helpers
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

function getStatusColor(status: string) {
  switch (status) {
    case 'healthy': return tokens.green
    case 'unhealthy': return tokens.red
    case 'offline': return tokens.gray
    default: return tokens.gray
  }
}

function getTypeIcon(type: string): React.ElementType {
  switch (type) {
    case 'database': return Database
    case 'cache': return Zap
    case 'ai': return Brain
    case 'vector': return Network
    case 'gateway': return Globe
    case 'api': return Server
    default: return Server
  }
}

function formatMs(ms: number) {
  if (ms === 0) return '< 1ms'
  return ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(1)}s`
}

function formatLastChecked(iso: string) {
  try {
    const d = new Date(iso)
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
  } catch {
    return iso
  }
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Components
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

function ServiceCard({ service }: { service: ServiceHealth }) {
  const statusColor = getStatusColor(service.status)
  const TypeIcon = getTypeIcon(service.type)

  return (
    <motion.div
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      whileHover={{ y: -2 }}
      style={{
        background: tokens.secondaryBackground,
        border: `0.5px solid ${tokens.separator}`,
        borderRadius: tokens.radius.lg,
        padding: 20,
        transition: 'all 0.2s ease',
      }}
      onMouseEnter={(e) => {
        e.currentTarget.style.boxShadow = '0 8px 32px rgba(0,0,0,0.12)'
        e.currentTarget.style.borderColor = statusColor
      }}
      onMouseLeave={(e) => {
        e.currentTarget.style.boxShadow = 'none'
        e.currentTarget.style.borderColor = tokens.separator
      }}
    >
      {/* Header */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 16 }}>
        <div style={{
          width: 44,
          height: 44,
          borderRadius: tokens.radius.md,
          background: `${statusColor}20`,
          border: `1.5px solid ${statusColor}40`,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          flexShrink: 0,
        }}>
          <TypeIcon style={{ width: 22, height: 22, color: statusColor }} />
        </div>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 2 }}>
            <h3 style={{
              fontSize: 15,
              fontWeight: 600,
              color: tokens.label,
              margin: 0,
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              whiteSpace: 'nowrap',
            }}>
              {service.name}
            </h3>
          </div>
          <span style={{ fontSize: 12, color: tokens.secondaryLabel, textTransform: 'capitalize' }}>
            {service.type}
          </span>
        </div>
        <div style={{
          fontSize: 11,
          fontWeight: 600,
          padding: '3px 8px',
          borderRadius: 5,
          textTransform: 'uppercase',
          background: `${statusColor}20`,
          color: statusColor,
          flexShrink: 0,
        }}>
          {service.status}
        </div>
      </div>

      {/* Metrics */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginBottom: 12 }}>
        <div style={{
          padding: '10px 12px',
          background: tokens.tertiaryFill,
          borderRadius: tokens.radius.sm,
        }}>
          <div style={{ fontSize: 16, fontWeight: 700, color: tokens.label }}>
            {formatMs(service.response_time_ms)}
          </div>
          <div style={{ fontSize: 10, color: tokens.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.5px', marginTop: 2 }}>
            Response Time
          </div>
        </div>
        <div style={{
          padding: '10px 12px',
          background: tokens.tertiaryFill,
          borderRadius: tokens.radius.sm,
        }}>
          <div style={{ fontSize: 16, fontWeight: 700, color: service.healthy ? tokens.green : tokens.red }}>
            {service.healthy ? 'Online' : 'Down'}
          </div>
          <div style={{ fontSize: 10, color: tokens.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.5px', marginTop: 2 }}>
            Status
          </div>
        </div>
      </div>

      {/* Error message */}
      {service.error && (
        <div style={{
          padding: '8px 10px',
          background: 'rgba(255, 59, 48, 0.08)',
          borderRadius: tokens.radius.sm,
          marginBottom: 10,
          border: `0.5px solid rgba(255, 59, 48, 0.2)`,
        }}>
          <div style={{ fontSize: 11, color: tokens.red, fontFamily: 'monospace', wordBreak: 'break-all' }}>
            {service.error.length > 80 ? service.error.slice(0, 80) + '…' : service.error}
          </div>
        </div>
      )}

      {/* Footer */}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <span style={{ fontSize: 11, color: tokens.tertiaryLabel }}>
          {service.endpoint !== '/api/v1' && service.endpoint !== 'postgres://[internal]' && service.endpoint !== 'redis://[internal]'
            ? new URL(service.endpoint).hostname
            : service.endpoint}
        </span>
        <span style={{ fontSize: 11, color: tokens.tertiaryLabel }}>
          {formatLastChecked(service.last_checked)}
        </span>
      </div>
    </motion.div>
  )
}

function SummaryCard({
  title,
  value,
  subtitle,
  icon: Icon,
  iconColor,
}: {
  title: string
  value: string
  subtitle: string
  icon: React.ElementType
  iconColor: string
}) {
  return (
    <div style={{
      background: tokens.secondaryBackground,
      border: `0.5px solid ${tokens.separator}`,
      borderRadius: tokens.radius.lg,
      padding: '20px 16px',
      textAlign: 'center',
    }}>
      <div style={{
        width: 44,
        height: 44,
        borderRadius: tokens.radius.md,
        background: `${iconColor}20`,
        border: `1.5px solid ${iconColor}40`,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        margin: '0 auto 12px',
      }}>
        <Icon style={{ width: 22, height: 22, color: iconColor }} />
      </div>
      <div style={{ fontSize: 26, fontWeight: 700, color: tokens.label, marginBottom: 4 }}>
        {value}
      </div>
      <div style={{ fontSize: 13, color: tokens.secondaryLabel, marginBottom: 4 }}>
        {title}
      </div>
      <div style={{ fontSize: 11, color: tokens.tertiaryLabel }}>
        {subtitle}
      </div>
    </div>
  )
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Main page
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

export function IntegrationHealthPage() {
  const [services, setServices] = useState<ServiceHealth[]>([])
  const [summary, setSummary] = useState<HealthSummary | null>(null)
  const [isLoading, setIsLoading] = useState(true)
  const [isRefreshing, setIsRefreshing] = useState(false)
  const [lastRefresh, setLastRefresh] = useState<Date | null>(null)
  const [error, setError] = useState<string | null>(null)

  const loadHealth = useCallback(async (silent = false) => {
    if (!silent) setIsLoading(true)
    else setIsRefreshing(true)
    setError(null)

    try {
      const res = await platformHealthApi.getIntegrationHealth()
      const data = res.data?.data
      if (data) {
        setServices(data.services || [])
        setSummary(data.summary || null)
        setLastRefresh(new Date())
      }
    } catch (err: any) {
      setError(err?.response?.data?.message || err?.message || 'Failed to load platform health')
    } finally {
      setIsLoading(false)
      setIsRefreshing(false)
    }
  }, [])

  useEffect(() => {
    loadHealth(false)
    const interval = setInterval(() => loadHealth(true), 30000)
    return () => clearInterval(interval)
  }, [loadHealth])

  if (isLoading) {
    return (
      <div style={{
        minHeight: '100vh',
        background: tokens.background,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
      }}>
        <div style={{ textAlign: 'center' }}>
          <Loader2 style={{
            width: 32,
            height: 32,
            color: tokens.blue,
            animation: 'spin 1s linear infinite',
            margin: '0 auto 16px',
          }} />
          <p style={{ fontSize: 15, color: tokens.secondaryLabel }}>
            Checking platform health…
          </p>
        </div>
      </div>
    )
  }

  const healthyCount = summary?.healthy ?? services.filter(s => s.healthy).length
  const totalCount = summary?.total ?? services.length
  const unhealthyCount = summary?.unhealthy ?? services.filter(s => !s.healthy).length
  const healthScore = summary?.health_score ?? (totalCount > 0 ? (healthyCount / totalCount) * 100 : 0)
  const scoreColor = healthScore >= 80 ? tokens.green : healthScore >= 60 ? tokens.orange : tokens.red

  const offlineCount = services.filter(s => s.status === 'offline').length
  const degradedCount = services.filter(s => s.status === 'unhealthy').length
  const avgResponse = services.length > 0
    ? Math.round(services.reduce((s, svc) => s + svc.response_time_ms, 0) / services.length)
    : 0

  return (
    <div style={{ minHeight: '100vh', background: tokens.background }}>
      <div style={{ maxWidth: 1200, margin: '0 auto', padding: '24px 16px' }}>

        {/* Header */}
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 28 }}>
          <div>
            <h1 style={{ fontSize: 28, fontWeight: 700, color: tokens.label, margin: 0 }}>
              Platform Health
            </h1>
            <p style={{ fontSize: 15, color: tokens.secondaryLabel, marginTop: 4 }}>
              Real-time health status of all platform components
            </p>
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            {lastRefresh && (
              <span style={{ fontSize: 12, color: tokens.tertiaryLabel }}>
                Updated {lastRefresh.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })}
              </span>
            )}
            <button
              onClick={() => loadHealth(true)}
              disabled={isRefreshing}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 6,
                padding: '8px 14px',
                borderRadius: tokens.radius.sm,
                border: 'none',
                background: tokens.blue,
                color: '#fff',
                fontSize: 13,
                fontWeight: 500,
                cursor: isRefreshing ? 'default' : 'pointer',
                opacity: isRefreshing ? 0.7 : 1,
              }}
            >
              <RefreshCw style={{
                width: 14,
                height: 14,
                ...(isRefreshing && { animation: 'spin 1s linear infinite' }),
              }} />
              Refresh
            </button>
          </div>
        </div>

        {/* Error banner */}
        {error && (
          <div style={{
            padding: '12px 16px',
            background: 'rgba(255, 59, 48, 0.08)',
            border: `0.5px solid rgba(255, 59, 48, 0.3)`,
            borderRadius: tokens.radius.md,
            marginBottom: 24,
            display: 'flex',
            alignItems: 'center',
            gap: 8,
          }}>
            <AlertTriangle style={{ width: 16, height: 16, color: tokens.red, flexShrink: 0 }} />
            <span style={{ fontSize: 13, color: tokens.red }}>{error}</span>
          </div>
        )}

        {/* Health Score + Summary */}
        <div style={{
          display: 'grid',
          gridTemplateColumns: '200px 1fr',
          gap: 20,
          marginBottom: 28,
        }}>
          {/* Score */}
          <div style={{
            background: tokens.secondaryBackground,
            border: `0.5px solid ${tokens.separator}`,
            borderRadius: tokens.radius.lg,
            padding: 24,
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            justifyContent: 'center',
            gap: 8,
          }}>
            <div style={{ fontSize: 52, fontWeight: 800, color: scoreColor, lineHeight: 1 }}>
              {Math.round(healthScore)}%
            </div>
            <div style={{ fontSize: 13, fontWeight: 600, color: tokens.secondaryLabel }}>
              Health Score
            </div>
            <div style={{ fontSize: 11, color: tokens.tertiaryLabel }}>
              {healthyCount}/{totalCount} services healthy
            </div>
            {/* Progress bar */}
            <div style={{
              width: '100%',
              height: 6,
              borderRadius: 3,
              background: tokens.fill,
              overflow: 'hidden',
              marginTop: 4,
            }}>
              <div style={{
                height: '100%',
                width: `${healthScore}%`,
                background: scoreColor,
                borderRadius: 3,
                transition: 'width 0.5s ease',
              }} />
            </div>
          </div>

          {/* Stats grid */}
          <div style={{
            display: 'grid',
            gridTemplateColumns: 'repeat(4, 1fr)',
            gap: 12,
          }}>
            <SummaryCard
              title="Healthy"
              value={healthyCount.toString()}
              subtitle="All systems operational"
              icon={CheckCircle}
              iconColor={tokens.green}
            />
            <SummaryCard
              title="Degraded"
              value={degradedCount.toString()}
              subtitle="Experiencing issues"
              icon={AlertTriangle}
              iconColor={tokens.orange}
            />
            <SummaryCard
              title="Offline"
              value={offlineCount.toString()}
              subtitle="Not responding"
              icon={WifiOff}
              iconColor={tokens.gray}
            />
            <SummaryCard
              title="Avg Response"
              value={avgResponse > 0 ? `${avgResponse}ms` : 'N/A'}
              subtitle="Across all services"
              icon={Clock}
              iconColor={tokens.blue}
            />
          </div>
        </div>

        {/* Services grid */}
        <div>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 16 }}>
            <h2 style={{ fontSize: 18, fontWeight: 600, color: tokens.label, margin: 0 }}>
              Platform Services
            </h2>
            <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
              <div style={{
                width: 7,
                height: 7,
                borderRadius: '50%',
                background: tokens.green,
                animation: 'pulse 2s infinite',
              }} />
              <span style={{ fontSize: 12, color: tokens.secondaryLabel }}>
                Auto-refreshing every 30s
              </span>
            </div>
          </div>

          {services.length === 0 && !error ? (
            <div style={{
              textAlign: 'center',
              padding: '80px 20px',
              background: tokens.secondaryBackground,
              borderRadius: tokens.radius.lg,
              border: `0.5px solid ${tokens.separator}`,
            }}>
              <Activity style={{ width: 40, height: 40, color: tokens.quaternaryLabel, margin: '0 auto 16px' }} />
              <p style={{ fontSize: 15, color: tokens.secondaryLabel }}>No platform services reported</p>
            </div>
          ) : (
            <div style={{
              display: 'grid',
              gridTemplateColumns: 'repeat(auto-fill, minmax(300px, 1fr))',
              gap: 16,
            }}>
              {services.map((service) => (
                <ServiceCard key={service.name} service={service} />
              ))}
            </div>
          )}
        </div>

        {/* Breakdown bar */}
        {services.length > 0 && (
          <div style={{
            marginTop: 28,
            background: tokens.secondaryBackground,
            border: `0.5px solid ${tokens.separator}`,
            borderRadius: tokens.radius.lg,
            padding: '20px 24px',
          }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 14 }}>
              <BarChart3 style={{ width: 16, height: 16, color: tokens.blue }} />
              <h3 style={{ fontSize: 15, fontWeight: 600, color: tokens.label, margin: 0 }}>
                Status Breakdown
              </h3>
            </div>
            <div style={{ display: 'flex', height: 10, borderRadius: 5, overflow: 'hidden', background: tokens.fill }}>
              {healthyCount > 0 && (
                <div style={{ flex: healthyCount, background: tokens.green, transition: 'flex 0.5s ease' }} />
              )}
              {degradedCount > 0 && (
                <div style={{ flex: degradedCount, background: tokens.red, transition: 'flex 0.5s ease' }} />
              )}
              {offlineCount > 0 && (
                <div style={{ flex: offlineCount, background: tokens.gray, transition: 'flex 0.5s ease' }} />
              )}
            </div>
            <div style={{ display: 'flex', gap: 20, marginTop: 12 }}>
              {[
                { label: 'Healthy', count: healthyCount, color: tokens.green },
                { label: 'Unhealthy', count: degradedCount, color: tokens.red },
                { label: 'Offline', count: offlineCount, color: tokens.gray },
              ].map(({ label, count, color }) => (
                <div key={label} style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                  <div style={{ width: 10, height: 10, borderRadius: 2, background: color }} />
                  <span style={{ fontSize: 12, color: tokens.secondaryLabel }}>
                    {label}: <strong style={{ color: tokens.label }}>{count}</strong>
                  </span>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>

      <style>{`
        @keyframes spin { from { transform: rotate(0deg) } to { transform: rotate(360deg) } }
        @keyframes pulse { 0%, 100% { opacity: 1 } 50% { opacity: 0.5 } }
      `}</style>
    </div>
  )
}
