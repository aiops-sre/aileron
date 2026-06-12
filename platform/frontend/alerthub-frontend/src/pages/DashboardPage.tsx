import React, { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { motion } from 'framer-motion'
import {
  Bell,
  AlertTriangle,
  Clock,
  Shield,
  Activity,
  BarChart3,
  Zap,
  Brain,
  CheckCircle,
  Sparkles,
  ArrowUpRight,
  TrendingUp,
  TrendingDown,
  ChevronRight,
} from 'lucide-react'
import { formatTime } from '@/lib/utils'
import { alertsApi, workflowApi, intelligenceApi } from '@/lib/api'
import { useUniversalDataStore, selectDashboardData, selectIncidentsData } from '@/stores/universalDataStore'
import { useAlertsStore } from '@/stores/alertsStore'
import { calculateMTTR, calculateAlertVelocity } from '@/lib/analytics'
import type { Alert } from '@/types'

// ─── Design tokens ────────────────────────────────────────────────────────────
const t = {
  blue: '#007AFF',
  green: '#34C759',
  red: '#FF3B30',
  orange: '#FF9500',
  yellow: '#FFCC00',
  purple: '#AF52DE',
  gray: '#8E8E93',
  label: 'var(--color-text)',
  secondary: 'var(--color-text-secondary)',
  tertiary: 'var(--color-text-tertiary, #8E8E93)',
  quaternary: 'rgba(142,142,147,0.4)',
  sep: 'var(--color-separator, rgba(142,142,147,0.12))',
  fill: 'var(--color-fill, rgba(142,142,147,0.08))',
  fill2: 'rgba(142,142,147,0.12)',
  fill3: 'rgba(142,142,147,0.06)',
  bg: 'var(--color-background)',
  card: 'var(--color-card, rgba(255,255,255,0.8))',
  r: { xs: 4, sm: 6, md: 10, lg: 12, xl: 16, '2xl': 20 },
} as const

// ─── Helpers ──────────────────────────────────────────────────────────────────
const sevColor = (s: string) =>
  s === 'critical' ? t.red : s === 'high' ? t.orange : s === 'medium' ? t.yellow : t.blue

const sevBg = (s: string) =>
  s === 'critical' ? 'rgba(255,59,48,0.12)'
  : s === 'high' ? 'rgba(255,149,0,0.12)'
  : s === 'medium' ? 'rgba(255,204,0,0.12)'
  : 'rgba(0,122,255,0.12)'

// ─── Metric Card ──────────────────────────────────────────────────────────────
function MetricCard({
  icon: Icon, color, label, value, sub, trend, onClick, alert: isAlert,
}: {
  icon: React.ElementType; color: string; label: string; value: string | number
  sub?: string; trend?: { v: number; up: boolean }; onClick?: () => void; alert?: boolean
}) {
  return (
    <motion.div
      whileHover={{ y: -2 }}
      onClick={onClick}
      style={{
        background: isAlert ? `${color}0d` : t.card,
        border: `0.5px solid ${isAlert ? `${color}40` : t.sep}`,
        borderRadius: t.r.lg,
        padding: '16px 18px',
        cursor: onClick ? 'pointer' : 'default',
        display: 'flex', alignItems: 'center', gap: 14,
        transition: 'all 0.2s ease',
        position: 'relative', overflow: 'hidden',
      }}
      onMouseEnter={(e) => { if (onClick) e.currentTarget.style.boxShadow = '0 8px 24px rgba(0,0,0,0.10)' }}
      onMouseLeave={(e) => { e.currentTarget.style.boxShadow = 'none' }}
    >
      <div style={{
        width: 44, height: 44, borderRadius: t.r.md,
        background: color,
        display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0,
      }}>
        <Icon style={{ width: 22, height: 22, color: '#fff' }} />
      </div>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ fontSize: 10, fontWeight: 600, color: t.tertiary, textTransform: 'uppercase', letterSpacing: '0.5px', marginBottom: 4 }}>
          {label}
        </div>
        <div style={{ fontSize: 26, fontWeight: 800, color: t.label, lineHeight: 1, marginBottom: 3 }}>
          {value}
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
          {trend && trend.v > 0 && (
            <span style={{ display: 'flex', alignItems: 'center', gap: 2, fontSize: 10, fontWeight: 700, color: trend.up ? t.red : t.green }}>
              {trend.up ? <TrendingUp style={{ width: 10, height: 10 }} /> : <TrendingDown style={{ width: 10, height: 10 }} />}
              {trend.v}%
            </span>
          )}
          {sub && <span style={{ fontSize: 10, color: t.tertiary }}>{sub}</span>}
        </div>
      </div>
      {onClick && <ArrowUpRight style={{ width: 13, height: 13, color: t.quaternary, flexShrink: 0 }} />}
    </motion.div>
  )
}

// ─── Alert Feed Row ───────────────────────────────────────────────────────────
function AlertRow({ alert, onClick }: { alert: Alert; onClick: () => void }) {
  return (
    <div
      onClick={onClick}
      style={{
        display: 'flex', alignItems: 'center', gap: 10,
        padding: '9px 12px', borderRadius: t.r.sm,
        cursor: 'pointer', transition: 'background 0.12s',
      }}
      onMouseEnter={(e) => { e.currentTarget.style.background = t.fill2 }}
      onMouseLeave={(e) => { e.currentTarget.style.background = 'transparent' }}
    >
      <div style={{
        width: 7, height: 7, borderRadius: '50%',
        background: sevColor(alert.severity), flexShrink: 0,
      }} />
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ fontSize: 13, fontWeight: 500, color: t.label, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          {alert.title}
        </div>
        <div style={{ fontSize: 10, color: t.tertiary, marginTop: 1 }}>
          {alert.source} · {formatTime(alert.created_at)}
        </div>
      </div>
      <span style={{
        fontSize: 9, fontWeight: 800, padding: '2px 6px', borderRadius: t.r.xs,
        textTransform: 'uppercase', letterSpacing: '0.4px',
        background: sevBg(alert.severity), color: sevColor(alert.severity), flexShrink: 0,
      }}>
        {alert.severity}
      </span>
    </div>
  )
}

// ─── Incident Row ─────────────────────────────────────────────────────────────
function IncidentRow({ inc, onClick }: { inc: any; onClick: () => void }) {
  const color = sevColor(inc.severity)
  return (
    <div
      onClick={onClick}
      style={{
        padding: '11px 14px', borderRadius: t.r.sm,
        cursor: 'pointer', borderLeft: `3px solid ${color}`,
        background: t.fill3, transition: 'all 0.14s', marginBottom: 6,
      }}
      onMouseEnter={(e) => {
        e.currentTarget.style.background = t.fill2
        e.currentTarget.style.transform = 'translateX(2px)'
      }}
      onMouseLeave={(e) => {
        e.currentTarget.style.background = t.fill3
        e.currentTarget.style.transform = 'translateX(0)'
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 4 }}>
        <span style={{ fontSize: 10, fontWeight: 700, color: t.tertiary, textTransform: 'uppercase', letterSpacing: '0.5px' }}>
          {inc.incident_number ? `INC-${inc.incident_number}` : 'INCIDENT'}
        </span>
        <span style={{
          fontSize: 9, fontWeight: 800, padding: '2px 6px', borderRadius: t.r.xs,
          background: sevBg(inc.severity), color, textTransform: 'uppercase',
        }}>
          {inc.severity}
        </span>
      </div>
      <div style={{ fontSize: 13, fontWeight: 500, color: t.label, lineHeight: 1.35, marginBottom: 3 }}>
        {inc.title}
      </div>
      <div style={{ fontSize: 10, color: t.tertiary }}>
        {inc.alert_count ? `${inc.alert_count} alerts · ` : ''}{formatTime(inc.created_at)}
      </div>
    </div>
  )
}

// ─── Activity Chart ───────────────────────────────────────────────────────────
function ActivityChart({ hourly }: { hourly: number[] }) {
  const max = Math.max(...hourly) || 1
  const peak = Math.max(...hourly)
  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'flex-end', gap: 3, height: 72 }}>
        {hourly.map((v, i) => {
          const pct = (v / max) * 100
          const isRecent = i >= hourly.length - 4
          const isPeak = v === peak && peak > 0
          return (
            <div key={i} style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center' }} title={`${v} alerts`}>
              <div style={{
                width: '100%',
                height: `${Math.max(pct, 3)}%`,
                background: isPeak ? t.red : isRecent ? t.blue : `${t.blue}55`,
                borderRadius: '3px 3px 0 0',
                minHeight: 3,
                transition: 'height 0.4s ease',
              }} />
            </div>
          )
        })}
      </div>
      <div style={{ display: 'flex', marginTop: 5 }}>
        {hourly.map((_, i) => (
          <div key={i} style={{ flex: 1, textAlign: 'center' }}>
            {i % 6 === 0 && <span style={{ fontSize: 9, color: t.tertiary }}>{String(i).padStart(2, '0')}h</span>}
          </div>
        ))}
      </div>
    </div>
  )
}

// ─── Severity Bar ─────────────────────────────────────────────────────────────
function SeverityBar({ label, count, total, color }: { label: string; count: number; total: number; color: string }) {
  const pct = total > 0 ? (count / total) * 100 : 0
  return (
    <div style={{ marginBottom: 10 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 4 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 7 }}>
          <div style={{ width: 8, height: 8, borderRadius: 2, background: color }} />
          <span style={{ fontSize: 12, color: t.label, textTransform: 'capitalize' }}>{label}</span>
        </div>
        <span style={{ fontSize: 12, fontWeight: 700, color: t.label }}>{count}</span>
      </div>
      <div style={{ height: 5, borderRadius: 3, background: t.fill, overflow: 'hidden' }}>
        <motion.div
          initial={{ width: 0 }}
          animate={{ width: `${pct}%` }}
          transition={{ duration: 0.6, delay: 0.1 }}
          style={{ height: '100%', background: color, borderRadius: 3 }}
        />
      </div>
    </div>
  )
}

// ─── Dashboard Page ───────────────────────────────────────────────────────────
export function DashboardPage() {
  const navigate = useNavigate()
  const dashboardData = useUniversalDataStore(selectDashboardData)
  const incidentsData = useUniversalDataStore(selectIncidentsData)
  const alerts = useAlertsStore((s) => s.alerts)

  const [maintenanceCount, setMaintenanceCount] = useState(0)
  const [workflowCount, setWorkflowCount] = useState(0)
  const [intelligenceStats, setIntelligenceStats] = useState<any>(null)

  useEffect(() => {
    alertsApi.getActiveMaintenanceWindows()
      .then(r => setMaintenanceCount((r.data?.data?.windows || []).length))
      .catch(() => {})
    workflowApi.listWorkflows({ limit: 100 })
      .then(r => setWorkflowCount((r.data?.data?.workflows || []).filter((w: any) => w.enabled).length))
      .catch(() => {})
    // Load intelligence stats for the KubeSense health card
    intelligenceApi.getStats()
      .then(r => { const d = r.data?.data ?? r.data; setIntelligenceStats(d?.stats ?? d) })
      .catch(() => {})
  }, [])

  const stats = {
    total: alerts.length,
    open: alerts.filter(a => a.status === 'open').length,
    critical: alerts.filter(a => a.severity === 'critical').length,
    high: alerts.filter(a => a.severity === 'high').length,
    medium: alerts.filter(a => a.severity === 'medium').length,
    low: alerts.filter(a => a.severity === 'low').length,
    resolved: alerts.filter(a => a.status === 'resolved').length,
  }

  const mttr = calculateMTTR(alerts)
  const velocity = calculateAlertVelocity(alerts)
  const recentAlerts = dashboardData.recentAlerts.length > 0 ? dashboardData.recentAlerts : alerts.slice(0, 8)
  const activeIncidents: any[] = incidentsData.activeIncidents
  const criticalIncidents = activeIncidents.filter(i => i.severity === 'critical').length
  const correlatedPct = alerts.length > 0 ? Math.round(alerts.filter(a => a.correlation_id).length / alerts.length * 100) : 0

  const formatMTTR = (h: number) => h < 1 ? `${Math.round(h * 60)}m` : `${h.toFixed(1)}h`

  const alertTrend = { v: velocity.change, up: velocity.trend === 'up' }

  return (
    <div style={{ minHeight: '100vh', background: t.bg }}>
      <div style={{ maxWidth: 1340, margin: '0 auto', padding: '20px 20px' }}>

        {/* ── KPI strip ────────────────────────────────────────────────────── */}
        <div style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(6, 1fr)',
          gap: 10,
          marginBottom: 16,
        }}>
          <MetricCard icon={Bell} color={t.blue} label="Total Alerts"
            value={stats.total.toLocaleString()} trend={alertTrend}
            onClick={() => navigate('/alerts')} />
          <MetricCard icon={AlertTriangle} color={t.orange} label="Open Alerts"
            value={stats.open.toLocaleString()}
            sub={stats.open > 0 ? 'need attention' : 'all clear'}
            onClick={() => navigate('/alerts')} />
          <MetricCard icon={Zap} color={t.red} label="Critical"
            value={stats.critical.toLocaleString()}
            sub={stats.critical > 0 ? 'high priority' : 'none active'}
            alert={stats.critical > 0}
            onClick={() => navigate('/alerts?severity=critical')} />
          <MetricCard icon={Clock} color={t.green} label="MTTR"
            value={mttr.avg > 0 ? formatMTTR(mttr.avg / 60) : '—'}
            sub="mean time to resolve" />
          <MetricCard icon={Activity} color={t.purple} label="Active Incidents"
            value={activeIncidents.length.toLocaleString()}
            sub={criticalIncidents > 0 ? `${criticalIncidents} critical` : activeIncidents.length > 0 ? 'in progress' : 'none open'}
            alert={activeIncidents.length > 0}
            onClick={() => navigate('/incidents')} />
          <MetricCard icon={Shield} color={t.gray} label="Maintenance"
            value={maintenanceCount.toString()}
            sub={maintenanceCount > 0 ? 'windows active' : 'no windows'} />
          <MetricCard icon={Brain} color={t.blue} label="AI Correlated"
            value={`${correlatedPct}%`}
            sub="alerts grouped by AI"
            onClick={() => navigate('/aiops')} />
          <MetricCard icon={CheckCircle} color={t.green} label="RCA Complete"
            value={activeIncidents?.filter?.((i: any) => i.rca_status === 'completed').length > 0
              ? `${Math.round(activeIncidents.filter((i: any) => i.rca_status === 'completed').length / Math.max(activeIncidents.length, 1) * 100)}%`
              : '—'}
            sub="incidents with root cause"
            onClick={() => navigate('/incidents')} />
          <MetricCard icon={Shield} color={'#AF52DE'} label="KS Signals"
            value={intelligenceStats ? (intelligenceStats.kubesense_health_events_24h ?? '—') : '—'}
            sub={intelligenceStats ? `${intelligenceStats.kubesense_config_violations_24h ?? 0} violations · ${intelligenceStats.kubesense_forecasts_active ?? 0} forecasts` : 'loading…'}
            onClick={() => navigate('/kubesense')} />
        </div>

        {/* ── Main grid ────────────────────────────────────────────────────── */}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 380px', gap: 14, marginBottom: 14 }}>

          {/* LEFT column */}
          <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>

            {/* Activity chart */}
            <div style={{
              background: t.card, border: `0.5px solid ${t.sep}`,
              borderRadius: t.r.lg, padding: '18px 22px',
            }}>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 14 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <Activity style={{ width: 16, height: 16, color: t.blue }} />
                  <span style={{ fontSize: 14, fontWeight: 600, color: t.label }}>Alert Activity</span>
                  <span style={{ fontSize: 11, color: t.tertiary }}>24h</span>
                </div>
                <div style={{ display: 'flex', alignItems: 'center', gap: 16, fontSize: 11, color: t.secondary }}>
                  <span>{velocity.current} this hour</span>
                  <span style={{
                    display: 'flex', alignItems: 'center', gap: 3, fontWeight: 600,
                    color: velocity.trend === 'up' ? t.red : velocity.trend === 'down' ? t.green : t.tertiary,
                  }}>
                    {velocity.trend === 'up' ? <TrendingUp style={{ width: 11, height: 11 }} /> : <TrendingDown style={{ width: 11, height: 11 }} />}
                    {velocity.change}% vs prev hr
                  </span>
                </div>
              </div>
              {velocity.hourly.length > 0
                ? <ActivityChart hourly={velocity.hourly} />
                : <div style={{ height: 72, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                    <span style={{ fontSize: 13, color: t.tertiary }}>No activity data yet</span>
                  </div>
              }
            </div>

            {/* Recent alerts */}
            <div style={{
              background: t.card, border: `0.5px solid ${t.sep}`,
              borderRadius: t.r.lg, overflow: 'hidden', flex: 1,
            }}>
              <div style={{
                display: 'flex', alignItems: 'center', justifyContent: 'space-between',
                padding: '14px 18px', borderBottom: `0.5px solid ${t.sep}`,
              }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <Bell style={{ width: 15, height: 15, color: t.blue }} />
                  <span style={{ fontSize: 14, fontWeight: 600, color: t.label }}>Recent Alerts</span>
                  {stats.open > 0 && (
                    <span style={{
                      fontSize: 10, fontWeight: 700, padding: '1px 6px', borderRadius: 8,
                      background: t.orange, color: '#fff',
                    }}>
                      {stats.open} open
                    </span>
                  )}
                </div>
                <button onClick={() => navigate('/alerts')} style={{
                  display: 'flex', alignItems: 'center', gap: 3,
                  fontSize: 12, color: t.blue, background: 'none', border: 'none', cursor: 'pointer', fontWeight: 500,
                }}>
                  View All <ChevronRight style={{ width: 12, height: 12 }} />
                </button>
              </div>
              <div style={{ padding: '6px 8px' }}>
                {recentAlerts.length === 0 ? (
                  <div style={{ textAlign: 'center', padding: '28px 20px' }}>
                    <CheckCircle style={{ width: 26, height: 26, color: t.green, margin: '0 auto 8px' }} />
                    <p style={{ fontSize: 13, color: t.secondary, margin: 0 }}>No recent alerts</p>
                  </div>
                ) : (
                  recentAlerts.slice(0, 7).map((alert: Alert) => (
                    <AlertRow key={alert.id} alert={alert} onClick={() => navigate('/alerts')} />
                  ))
                )}
              </div>
            </div>
          </div>

          {/* RIGHT column */}
          <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>

            {/* Active incidents */}
            <div style={{
              background: t.card,
              border: `0.5px solid ${activeIncidents.length > 0 ? `${t.red}35` : t.sep}`,
              borderRadius: t.r.lg, overflow: 'hidden',
              flex: activeIncidents.length > 0 ? '1 1 auto' : '0 0 auto',
            }}>
              <div style={{
                display: 'flex', alignItems: 'center', justifyContent: 'space-between',
                padding: '14px 18px', borderBottom: `0.5px solid ${t.sep}`,
                background: activeIncidents.length > 0 ? 'rgba(255,59,48,0.04)' : 'transparent',
              }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <Activity style={{ width: 15, height: 15, color: activeIncidents.length > 0 ? t.red : t.purple }} />
                  <span style={{ fontSize: 14, fontWeight: 600, color: t.label }}>Active Incidents</span>
                  {activeIncidents.length > 0 && (
                    <span style={{
                      fontSize: 10, fontWeight: 700, padding: '1px 7px', borderRadius: 8,
                      background: t.red, color: '#fff',
                    }}>
                      {activeIncidents.length}
                    </span>
                  )}
                </div>
                <button onClick={() => navigate('/incidents')} style={{
                  display: 'flex', alignItems: 'center', gap: 3,
                  fontSize: 12, color: t.blue, background: 'none', border: 'none', cursor: 'pointer', fontWeight: 500,
                }}>
                  View All <ChevronRight style={{ width: 12, height: 12 }} />
                </button>
              </div>
              <div style={{ padding: '10px 12px' }}>
                {activeIncidents.length === 0 ? (
                  <div style={{ textAlign: 'center', padding: '20px 16px' }}>
                    <CheckCircle style={{ width: 26, height: 26, color: t.green, margin: '0 auto 8px' }} />
                    <p style={{ fontSize: 13, color: t.secondary, margin: 0 }}>No active incidents</p>
                  </div>
                ) : (
                  activeIncidents.slice(0, 4).map((inc: any) => (
                    <IncidentRow key={inc.id} inc={inc} onClick={() => navigate('/incidents')} />
                  ))
                )}
              </div>
            </div>

            {/* Severity breakdown */}
            <div style={{
              background: t.card, border: `0.5px solid ${t.sep}`,
              borderRadius: t.r.lg, padding: '16px 18px',
            }}>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 14 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <BarChart3 style={{ width: 15, height: 15, color: t.blue }} />
                  <span style={{ fontSize: 14, fontWeight: 600, color: t.label }}>By Severity</span>
                </div>
                <span style={{ fontSize: 11, color: t.tertiary }}>{stats.total} total</span>
              </div>
              <SeverityBar label="Critical" count={stats.critical} total={stats.total} color={t.red} />
              <SeverityBar label="High" count={stats.high} total={stats.total} color={t.orange} />
              <SeverityBar label="Medium" count={stats.medium} total={stats.total} color={t.yellow} />
              <SeverityBar label="Low" count={stats.low} total={stats.total} color={t.blue} />
              {alerts.length > 0 && (
                <div style={{
                  display: 'flex', justifyContent: 'space-between',
                  marginTop: 12, paddingTop: 12, borderTop: `0.5px solid ${t.sep}`,
                }}>
                  <span style={{ fontSize: 11, color: t.tertiary }}>{stats.resolved} resolved</span>
                  <span style={{ fontSize: 11, color: t.tertiary }}>{correlatedPct}% AI correlated</span>
                </div>
              )}
            </div>

            {/* System pulse */}
            <div style={{
              background: t.card, border: `0.5px solid ${t.sep}`,
              borderRadius: t.r.lg, padding: '14px 18px',
              display: 'flex', flexDirection: 'column', gap: 10,
            }}>
              <span style={{ fontSize: 13, fontWeight: 600, color: t.label }}>System Pulse</span>
              {[
                { icon: Sparkles, color: t.purple, label: 'AI Correlation', value: correlatedPct > 0 ? `${correlatedPct}%` : 'N/A' },
                { icon: Zap, color: t.orange, label: 'Active Workflows', value: workflowCount.toString() },
                { icon: Clock, color: t.green, label: 'Avg MTTR', value: mttr.avg > 0 ? formatMTTR(mttr.avg / 60) : 'N/A' },
              ].map(({ icon: Icon, color, label, value }) => (
                <div key={label} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <Icon style={{ width: 14, height: 14, color }} />
                    <span style={{ fontSize: 12, color: t.secondary }}>{label}</span>
                  </div>
                  <span style={{ fontSize: 13, fontWeight: 700, color: t.label }}>{value}</span>
                </div>
              ))}
            </div>
          </div>
        </div>

        {/* ── Quick Actions ─────────────────────────────────────────────────── */}
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 10 }}>
          {[
            { icon: Sparkles, color: t.purple, label: 'AI Correlations', desc: correlatedPct > 0 ? `${correlatedPct}% correlation rate` : 'AI-powered grouping', path: '/alerts?tab=correlations' },
            { icon: Zap, color: t.orange, label: 'Automations', desc: workflowCount > 0 ? `${workflowCount} workflow${workflowCount !== 1 ? 's' : ''} active` : 'Configure workflows', path: '/alerts?tab=workflows' },
            { icon: Activity, color: t.green, label: 'Platform Health', desc: 'Check service health', path: '/integration-health' },
            { icon: BarChart3, color: t.blue, label: 'Analytics', desc: 'Trends & anomalies', path: '/analytics' },
          ].map((a) => (
            <motion.button
              key={a.label}
              whileHover={{ y: -2 }}
              whileTap={{ scale: 0.98 }}
              onClick={() => navigate(a.path)}
              style={{
                display: 'flex', alignItems: 'center', gap: 12,
                padding: '14px 16px', borderRadius: t.r.lg,
                border: `0.5px solid ${t.sep}`, background: t.card,
                color: t.label, cursor: 'pointer', textAlign: 'left',
                transition: 'all 0.15s',
              }}
              onMouseEnter={(e) => {
                e.currentTarget.style.borderColor = a.color
                e.currentTarget.style.boxShadow = '0 6px 20px rgba(0,0,0,0.09)'
              }}
              onMouseLeave={(e) => {
                e.currentTarget.style.borderColor = t.sep
                e.currentTarget.style.boxShadow = 'none'
              }}
            >
              <div style={{
                width: 38, height: 38, borderRadius: t.r.md, flexShrink: 0,
                background: `${a.color}15`, border: `1.5px solid ${a.color}30`,
                display: 'flex', alignItems: 'center', justifyContent: 'center',
              }}>
                <a.icon style={{ width: 18, height: 18, color: a.color }} />
              </div>
              <div>
                <div style={{ fontSize: 13, fontWeight: 600, color: t.label, marginBottom: 2 }}>{a.label}</div>
                <div style={{ fontSize: 11, color: t.tertiary }}>{a.desc}</div>
              </div>
            </motion.button>
          ))}
        </div>

      </div>

      <style>{`
        @keyframes spin { from { transform: rotate(0deg) } to { transform: rotate(360deg) } }
      `}</style>
    </div>
  )
}
