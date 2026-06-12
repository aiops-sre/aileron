import React, { useState, useMemo, useEffect, useRef, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { motion, AnimatePresence } from 'framer-motion'
import {
  Bell, Filter, Search, RefreshCw, CheckSquare, Zap, Settings,
  BarChart3, Link, Lightbulb, X, Fingerprint, FileJson, Target,
  ChevronLeft, ChevronRight, Wifi, TrendingUp, Brain, AlertTriangle,
  CheckCircle, ArrowUp, ArrowDown, ArrowUpDown,
} from 'lucide-react'
import { formatTime } from '@/lib/utils'
import type { Alert, AlertFilters } from '@/types'
import { alertsApi } from '@/lib/api'
import {
  useAlertsStore, selectFilteredAlerts, selectIsRefreshing,
  selectFilters, selectTotalAlerts, selectAlerts,
} from '@/stores/alertsStore'
import { SmartInsightsRenderer } from '@/components/SmartInsightsRenderer'
import { AlertAutomation } from '@/components/AlertAutomation'
import { AlertDetailPanel } from '@/components/AlertDetailPanel'
import { AlertCard, COL } from '@/components/AlertCard'
import { DeduplicationPage } from '@/pages/DeduplicationPage'
import { MappingRulesPage } from '@/pages/MappingRulesPage'
import { AlertQualityPage } from '@/pages/AlertQualityPage'

// ─── Design tokens ────────────────────────────────────────────────────────────
const ap = {
  blue:    '#007AFF',
  green:   '#34C759',
  red:     '#FF3B30',
  orange:  '#FF9500',
  yellow:  '#FFCC00',
  purple:  '#AF52DE',
  gray:    '#8E8E93',
  label:   'var(--color-text)',
  sec:     'var(--color-text-secondary)',
  tert:    'var(--color-text-tertiary, #8E8E93)',
  sep:     'var(--color-separator, rgba(142, 142, 147, 0.12))',
  fill:    'var(--color-fill, rgba(142, 142, 147, 0.08))',
  fill2:   'rgba(142, 142, 147, 0.12)',
  bg:      'var(--color-background)',
  card:    'var(--color-card, rgba(255, 255, 255, 0.8))',
  r:       { xs: 4, sm: 6, md: 10, lg: 12, xl: 16 },
} as const

const AUTO_REFRESH_SEC = 300

// ─── Search input ─────────────────────────────────────────────────────────────
function SearchInput({ value, onChange, placeholder = 'Search' }: {
  value: string; onChange: (v: string) => void; placeholder?: string
}) {
  return (
    <div style={{ position: 'relative' }}>
      <Search style={{
        position: 'absolute', left: 10, top: '50%', transform: 'translateY(-50%)',
        width: 13, height: 13, color: ap.tert, pointerEvents: 'none',
      }} />
      <input
        type="text"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        style={{
          width: '100%', height: 32, borderRadius: ap.r.md, border: `0.5px solid ${ap.sep}`,
          background: ap.card, paddingLeft: 30, paddingRight: value ? 28 : 10,
          fontSize: 13, color: ap.label, outline: 'none',
          boxShadow: 'none', transition: 'border-color 0.15s, box-shadow 0.15s',
        }}
        onFocus={(e) => {
          e.target.style.borderColor = ap.blue
          e.target.style.boxShadow = '0 0 0 3px rgba(0,122,255,0.15)'
        }}
        onBlur={(e) => {
          e.target.style.borderColor = ap.sep
          e.target.style.boxShadow = 'none'
        }}
      />
      {value && (
        <button onClick={() => onChange('')} style={{
          position: 'absolute', right: 6, top: '50%', transform: 'translateY(-50%)',
          width: 16, height: 16, borderRadius: '50%', background: ap.tert,
          border: 'none', cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center',
        }}>
          <X style={{ width: 9, height: 9, color: '#fff' }} />
        </button>
      )}
    </div>
  )
}

// ─── Filter select ─────────────────────────────────────────────────────────────
function FilterSelect({ value, onChange, placeholder, options }: {
  value: string; onChange: (v: string) => void; placeholder: string
  options: { value: string; label: string }[]
}) {
  const active = !!value
  return (
    <div style={{ position: 'relative' }}>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        style={{
          height: 32, borderRadius: ap.r.md,
          border: active ? `0.5px solid ${ap.blue}` : `0.5px solid ${ap.sep}`,
          background: active ? 'rgba(0,122,255,0.07)' : ap.card,
          padding: '0 24px 0 10px', fontSize: 12,
          color: active ? ap.blue : ap.label,
          fontWeight: active ? 600 : 400,
          outline: 'none', appearance: 'none', cursor: 'pointer',
        }}
      >
        <option value="">{placeholder}</option>
        {options.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
      </select>
      <ArrowUpDown style={{
        position: 'absolute', right: 7, top: '50%', transform: 'translateY(-50%)',
        width: 10, height: 10, pointerEvents: 'none',
        color: active ? ap.blue : ap.tert,
      }} />
    </div>
  )
}

// ─── Active filter chips ───────────────────────────────────────────────────────
function FilterChip({ label, onRemove }: { label: string; onRemove: () => void }) {
  return (
    <span style={{
      display: 'inline-flex', alignItems: 'center', gap: 4,
      fontSize: 11, fontWeight: 500, padding: '2px 8px 2px 10px',
      borderRadius: 20, background: 'rgba(0,122,255,0.10)',
      color: ap.blue, border: `0.5px solid rgba(0,122,255,0.25)`,
    }}>
      {label}
      <button
        onClick={onRemove}
        style={{
          background: 'none', border: 'none', cursor: 'pointer',
          padding: 0, display: 'flex', alignItems: 'center',
        }}
      >
        <X style={{ width: 10, height: 10, color: ap.blue }} />
      </button>
    </span>
  )
}

// ─── Pagination ────────────────────────────────────────────────────────────────
function Pagination({
  currentPage, totalPages, pageSize, totalItems,
  onPageChange, onPageSizeChange,
}: {
  currentPage: number; totalPages: number; pageSize: number; totalItems: number
  onPageChange: (p: number) => void; onPageSizeChange: (s: number) => void
}) {
  const start = totalItems === 0 ? 0 : (currentPage - 1) * pageSize + 1
  const end   = Math.min(currentPage * pageSize, totalItems)

  const pages: (number | '…')[] = []
  if (totalPages <= 7) {
    for (let i = 1; i <= totalPages; i++) pages.push(i)
  } else {
    pages.push(1)
    if (currentPage > 3) pages.push('…')
    for (let i = Math.max(2, currentPage - 1); i <= Math.min(totalPages - 1, currentPage + 1); i++) pages.push(i)
    if (currentPage < totalPages - 2) pages.push('…')
    pages.push(totalPages)
  }

  const btnStyle = (active: boolean, disabled = false): React.CSSProperties => ({
    minWidth: 30, height: 30, borderRadius: ap.r.sm,
    border: active ? 'none' : `0.5px solid ${ap.sep}`,
    background: active ? ap.blue : ap.card,
    color: active ? '#fff' : ap.label,
    fontSize: 12, fontWeight: active ? 600 : 400,
    cursor: disabled ? 'default' : 'pointer',
    opacity: disabled ? 0.35 : 1,
    display: 'flex', alignItems: 'center', justifyContent: 'center',
    padding: '0 8px', transition: 'background 0.12s',
  } as React.CSSProperties)

  return (
    <div style={{
      display: 'flex', alignItems: 'center', justifyContent: 'space-between',
      padding: '12px 16px', borderTop: `0.5px solid ${ap.sep}`,
      background: ap.card,
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        <span style={{ fontSize: 12, color: ap.sec }}>
          {totalItems === 0 ? 'No results' : `${start}–${end} of ${totalItems.toLocaleString()}`}
        </span>
        <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
          <span style={{ fontSize: 12, color: ap.tert }}>Rows:</span>
          <select
            value={pageSize}
            onChange={(e) => onPageSizeChange(Number(e.target.value))}
            style={{
              height: 28, borderRadius: ap.r.sm,
              border: `0.5px solid ${ap.sep}`, background: ap.card,
              padding: '0 20px 0 8px', fontSize: 12, color: ap.label,
              outline: 'none', appearance: 'none', cursor: 'pointer',
            }}
          >
            {[25, 50, 100, 200].map(s => <option key={s} value={s}>{s}</option>)}
          </select>
        </div>
      </div>

      {totalPages > 1 && (
        <div style={{ display: 'flex', gap: 4, alignItems: 'center' }}>
          <button
            onClick={() => onPageChange(1)}
            disabled={currentPage === 1}
            style={btnStyle(false, currentPage === 1)}
          >
            «
          </button>
          <button
            onClick={() => onPageChange(currentPage - 1)}
            disabled={currentPage === 1}
            style={btnStyle(false, currentPage === 1)}
          >
            <ChevronLeft style={{ width: 13, height: 13 }} />
          </button>

          {pages.map((p, i) =>
            p === '…' ? (
              <span key={`e${i}`} style={{ fontSize: 12, color: ap.tert, padding: '0 4px' }}>…</span>
            ) : (
              <button
                key={p}
                onClick={() => onPageChange(p as number)}
                style={btnStyle(p === currentPage)}
              >
                {p}
              </button>
            )
          )}

          <button
            onClick={() => onPageChange(currentPage + 1)}
            disabled={currentPage === totalPages}
            style={btnStyle(false, currentPage === totalPages)}
          >
            <ChevronRight style={{ width: 13, height: 13 }} />
          </button>
          <button
            onClick={() => onPageChange(totalPages)}
            disabled={currentPage === totalPages}
            style={btnStyle(false, currentPage === totalPages)}
          >
            »
          </button>
        </div>
      )}
    </div>
  )
}

// ─── Stats strip ──────────────────────────────────────────────────────────────
function StatsStrip({ totalAlerts, serverCounts, alerts, autonomousStats }: {
  totalAlerts: number
  serverCounts: { open: number; acknowledged: number; criticalOpen: number } | null
  alerts: Alert[]
  autonomousStats: Record<string, any> | null
}) {
  const correlated = alerts.filter(a => !!a.correlation_id).length
  const corrPct    = alerts.length > 0 ? ((correlated / alerts.length) * 100).toFixed(0) : '0'
  const noiseRed   = autonomousStats?.auto_resolved_rate
    ? `${(autonomousStats.auto_resolved_rate * 100).toFixed(0)}%` : '—'

  const open     = serverCounts?.open     ?? alerts.filter(a => a.status === 'open').length
  const acked    = serverCounts?.acknowledged ?? alerts.filter(a => a.status === 'acknowledged').length
  const critical = serverCounts?.criticalOpen ?? alerts.filter(a => a.severity === 'critical' && a.status === 'open').length

  const metrics = [
    { label: 'Total Alerts',     value: totalAlerts.toLocaleString(), sub: 'all alerts',        color: ap.blue   },
    { label: 'Open',             value: open.toLocaleString(),        sub: 'unresolved',        color: ap.red    },
    { label: 'Acknowledged',     value: acked.toLocaleString(),       sub: 'pending review',    color: ap.orange },
    { label: 'Critical Open',    value: critical.toLocaleString(),    sub: 'needs attention',   color: '#CC1F11' },
    { label: 'Correlation Rate', value: `${corrPct}%`,                sub: 'auto-correlated',   color: ap.purple },
    { label: 'Noise Reduction',  value: noiseRed,                     sub: 'auto-resolved',     color: ap.green  },
  ]

  return (
    <div style={{
      display: 'grid', gridTemplateColumns: 'repeat(6, 1fr)',
      gap: 8, marginBottom: 16,
    }}>
      {metrics.map(({ label, value, sub, color }) => (
        <div key={label} style={{
          padding: '12px 14px', borderRadius: ap.r.md,
          background: ap.card, border: `0.5px solid ${ap.sep}`,
        }}>
          <div style={{
            fontSize: 11, fontWeight: 500, color: ap.tert,
            textTransform: 'uppercase', letterSpacing: '0.5px', marginBottom: 6,
          }}>
            {label}
          </div>
          <div style={{ fontSize: 22, fontWeight: 700, color, lineHeight: 1.1, marginBottom: 2 }}>
            {value}
          </div>
          <div style={{ fontSize: 10, color: ap.tert }}>{sub}</div>
        </div>
      ))}
    </div>
  )
}

// ─── Column header with sort ───────────────────────────────────────────────────
function ColHeader({ label, sortKey, currentSort, currentOrder, onSort, style: extraStyle }: {
  label: string; sortKey?: string
  currentSort?: string; currentOrder?: string
  onSort?: (key: string) => void
  style?: React.CSSProperties
}) {
  if (!sortKey || !onSort) {
    return (
      <span style={{ fontSize: 10, fontWeight: 700, color: ap.tert, textTransform: 'uppercase', letterSpacing: '0.5px', ...extraStyle }}>
        {label}
      </span>
    )
  }
  const active = currentSort === sortKey
  const asc    = active && currentOrder === 'asc'
  const SortIcon = active ? (asc ? ArrowUp : ArrowDown) : ArrowUpDown
  return (
    <button
      onClick={() => onSort(sortKey)}
      style={{
        display: 'inline-flex', alignItems: 'center', gap: 4,
        background: 'none', border: 'none', cursor: 'pointer', padding: 0,
        fontSize: 10, fontWeight: 700,
        color: active ? ap.blue : ap.tert,
        textTransform: 'uppercase', letterSpacing: '0.5px',
        ...extraStyle,
      }}
    >
      {label}
      <SortIcon style={{ width: 9, height: 9 }} />
    </button>
  )
}

// ─── Analytics tab ─────────────────────────────────────────────────────────────
function AnalyticsTab({ autonomousStats }: { autonomousStats: Record<string, any> | null }) {
  if (!autonomousStats) {
    return (
      <div style={{ textAlign: 'center', padding: '60px 20px', color: ap.sec, fontSize: 13 }}>
        Loading analytics…
      </div>
    )
  }

  const items = [
    { label: 'Agent-Processed Alerts',  value: autonomousStats.agent_processed_count ?? 0,      detail: 'Processed by autonomous AI agent', color: ap.purple, icon: Brain },
    { label: 'Correlation Accuracy',    value: `${((autonomousStats.correlation_accuracy ?? 0) * 100).toFixed(1)}%`, detail: 'High-confidence correlations (>80%)', color: ap.blue, icon: Link },
    { label: 'Auto-Resolution Rate',    value: `${((autonomousStats.auto_resolved_rate ?? 0) * 100).toFixed(1)}%`,  detail: 'Resolved without manual action',     color: ap.green, icon: CheckCircle },
    { label: 'Total Alerts (All Time)', value: autonomousStats.total_alerts ?? 0,               detail: 'Across all sources',               color: ap.gray, icon: Bell },
  ]

  return (
    <div style={{ padding: 20 }}>
      <div style={{ marginBottom: 20 }}>
        <h3 style={{ fontSize: 15, fontWeight: 700, color: ap.label, margin: '0 0 3px' }}>
          AIOps Analytics
        </h3>
        <p style={{ fontSize: 12, color: ap.sec, margin: 0 }}>
          Real-time statistics from the autonomous correlation and resolution pipeline
        </p>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginBottom: 24 }}>
        {items.map(({ label, value, detail, color, icon: Icon }) => (
          <div key={label} style={{
            padding: 16, borderRadius: ap.r.lg,
            background: ap.fill, border: `0.5px solid ${ap.sep}`,
          }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12 }}>
              <div style={{
                width: 30, height: 30, borderRadius: 8, background: `${color}15`,
                display: 'flex', alignItems: 'center', justifyContent: 'center',
              }}>
                <Icon style={{ width: 14, height: 14, color }} />
              </div>
              <span style={{ fontSize: 12, fontWeight: 600, color: ap.sec }}>{label}</span>
            </div>
            <div style={{ fontSize: 26, fontWeight: 700, color, marginBottom: 4 }}>{value}</div>
            <div style={{ fontSize: 11, color: ap.tert }}>{detail}</div>
          </div>
        ))}
      </div>

      <div style={{
        padding: 14, borderRadius: ap.r.md,
        background: 'rgba(0,122,255,0.05)', border: `0.5px solid rgba(0,122,255,0.18)`,
      }}>
        <div style={{ fontSize: 11, fontWeight: 700, color: ap.blue, marginBottom: 6, textTransform: 'uppercase', letterSpacing: '0.5px', display: 'flex', alignItems: 'center', gap: 5 }}>
          <TrendingUp style={{ width: 12, height: 12 }} />
          Intelligence Summary
        </div>
        <p style={{ fontSize: 12, color: ap.sec, margin: 0, lineHeight: 1.65 }}>
          The autonomous agent has processed{' '}
          <strong style={{ color: ap.purple }}>{autonomousStats.agent_processed_count ?? 0} alerts</strong>{' '}
          with a correlation accuracy of{' '}
          <strong style={{ color: ap.blue }}>{((autonomousStats.correlation_accuracy ?? 0) * 100).toFixed(1)}%</strong>.{' '}
          Auto-resolution rate is{' '}
          <strong style={{ color: ap.green }}>{((autonomousStats.auto_resolved_rate ?? 0) * 100).toFixed(1)}%</strong>{' '}
          of all resolved alerts.
        </p>
      </div>
    </div>
  )
}

// ─── Correlations tab ──────────────────────────────────────────────────────────
function CorrelationsTab({ alerts, onAlertClick }: {
  alerts: Alert[]; onAlertClick: (alert: Alert) => void
}) {
  const correlated = alerts.filter(a => !!a.correlation_id)
  const corrRate   = alerts.length > 0
    ? ((correlated.length / alerts.length) * 100).toFixed(1) : '0'

  const groups = useMemo(() => {
    const map = new Map<string, Alert[]>()
    correlated.forEach(a => {
      const key = a.correlation_id!
      if (!map.has(key)) map.set(key, [])
      map.get(key)!.push(a)
    })
    return Array.from(map.entries()).sort((a, b) => b[1].length - a[1].length).slice(0, 30)
  }, [correlated])

  const SEV_DOT: Record<string, string> = {
    critical: '#FF3B30', high: '#FF9500', medium: '#FFCC00', low: '#007AFF',
  }

  return (
    <div style={{ padding: 16 }}>
      <div style={{ marginBottom: 16 }}>
        <h3 style={{ fontSize: 15, fontWeight: 700, color: ap.label, margin: '0 0 3px' }}>
          AI-Powered Correlations
        </h3>
        <p style={{ fontSize: 12, color: ap.sec, margin: 0 }}>
          {correlated.length} correlated alerts · {groups.length} groups · {corrRate}% correlation rate
        </p>
      </div>

      {groups.length === 0 ? (
        <div style={{ textAlign: 'center', padding: '40px 20px', color: ap.sec, fontSize: 13 }}>
          No correlated alerts found
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {groups.map(([corrId, groupAlerts]) => {
            const top  = groupAlerts[0]
            const maxScore  = Math.max(...groupAlerts.map(a => a.correlation_score ?? 0))
            const domSev    = groupAlerts.some(a => a.severity === 'critical') ? 'critical'
              : groupAlerts.some(a => a.severity === 'high') ? 'high' : top.severity
            return (
              <motion.div
                key={corrId}
                initial={{ opacity: 0, y: 4 }}
                animate={{ opacity: 1, y: 0 }}
                style={{
                  background: ap.fill, border: `0.5px solid ${ap.sep}`,
                  borderLeft: `3px solid ${SEV_DOT[domSev] ?? ap.gray}`,
                  borderRadius: ap.r.md, overflow: 'hidden',
                }}
              >
                <div style={{
                  display: 'flex', alignItems: 'center', justifyContent: 'space-between',
                  padding: '9px 12px', background: 'rgba(175,82,222,0.05)',
                  borderBottom: `0.5px solid ${ap.sep}`,
                }}>
                  <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                    <span style={{
                      fontSize: 10, fontWeight: 700, color: ap.purple,
                      background: 'rgba(175,82,222,0.12)', padding: '2px 7px', borderRadius: 4,
                    }}>
                      COR-{corrId.slice(0, 8).toUpperCase()}
                    </span>
                    <span style={{ fontSize: 12, fontWeight: 600, color: ap.label }}>
                      {groupAlerts.length} alert{groupAlerts.length !== 1 ? 's' : ''}
                    </span>
                    {maxScore > 0 && (
                      <span style={{ fontSize: 11, color: ap.sec }}>
                        · Confidence {(maxScore * 100).toFixed(0)}%
                      </span>
                    )}
                  </div>
                  <span style={{ fontSize: 11, color: ap.tert }}>{formatTime(top.created_at)}</span>
                </div>

                <div style={{ padding: '6px 8px', display: 'flex', flexDirection: 'column', gap: 4 }}>
                  {groupAlerts.slice(0, 4).map(alert => (
                    <div
                      key={alert.id}
                      onClick={() => onAlertClick(alert)}
                      style={{
                        display: 'flex', alignItems: 'center', gap: 8,
                        padding: '6px 10px', borderRadius: 5, cursor: 'pointer',
                        background: ap.card, border: `0.5px solid ${ap.sep}`,
                      }}
                      onMouseEnter={(e) => { (e.currentTarget as HTMLElement).style.background = ap.fill }}
                      onMouseLeave={(e) => { (e.currentTarget as HTMLElement).style.background = ap.card }}
                    >
                      <span style={{
                        width: 8, height: 8, borderRadius: '50%', flexShrink: 0,
                        background: SEV_DOT[alert.severity] ?? ap.gray,
                      }} />
                      <span style={{
                        fontSize: 11, fontWeight: 600, color: ap.sec, flexShrink: 0, minWidth: 60,
                      }}>
                        {alert.severity.charAt(0).toUpperCase() + alert.severity.slice(1)}
                      </span>
                      <span style={{
                        fontSize: 12, color: ap.label, flex: 1,
                        overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                      }}>
                        {alert.title}
                      </span>
                      <span style={{ fontSize: 10, color: ap.tert, flexShrink: 0 }}>
                        {alert.source}
                      </span>
                    </div>
                  ))}
                  {groupAlerts.length > 4 && (
                    <div style={{ fontSize: 11, color: ap.tert, padding: '3px 10px' }}>
                      +{groupAlerts.length - 4} more in this group
                    </div>
                  )}
                </div>
              </motion.div>
            )
          })}
        </div>
      )}
    </div>
  )
}

// ─── Sidebar ───────────────────────────────────────────────────────────────────
function Sidebar({ items, selected, onSelect }: {
  items: { id: string; label: string; icon: React.ElementType; iconColor: string; count?: number }[]
  selected: string; onSelect: (id: string) => void
}) {
  return (
    <nav style={{ width: 216, flexShrink: 0, padding: '8px 0' }}>
      {items.map((item) => {
        const active = item.id === selected
        const Icon = item.icon
        return (
          <button
            key={item.id}
            onClick={() => onSelect(item.id)}
            style={{
              display: 'flex', alignItems: 'center', gap: 10, width: '100%',
              padding: '7px 10px', borderRadius: ap.r.sm, border: 'none',
              cursor: 'pointer',
              background: active ? 'rgba(0,122,255,0.10)' : 'transparent',
              transition: 'background 0.12s', marginBottom: 1, textAlign: 'left',
            }}
            onMouseEnter={(e) => { if (!active) (e.currentTarget as HTMLElement).style.background = 'rgba(142,142,147,0.07)' }}
            onMouseLeave={(e) => { if (!active) (e.currentTarget as HTMLElement).style.background = 'transparent' }}
          >
            <div style={{
              width: 26, height: 26, borderRadius: 6, background: item.iconColor,
              display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0,
            }}>
              <Icon style={{ width: 13, height: 13, color: '#fff' }} />
            </div>
            <span style={{
              fontSize: 13, fontWeight: active ? 600 : 400,
              color: active ? ap.blue : ap.label, flex: 1,
            }}>
              {item.label}
            </span>
            {item.count !== undefined && (
              <span style={{
                fontSize: 11, fontWeight: 500, color: ap.tert,
                background: ap.fill, padding: '1px 7px', borderRadius: 10,
              }}>
                {item.count.toLocaleString()}
              </span>
            )}
          </button>
        )
      })}
    </nav>
  )
}

// ─── Main page ─────────────────────────────────────────────────────────────────
interface UIState {
  selectedAlerts: Set<string>
  activeTab: string
  currentPage: number
  pageSize: number
  isBulkMode: boolean
  showDetailPanel: boolean
  selectedAlert: Alert | null
  countdown: number
  extraFiltersOpen: boolean
  selectAll: boolean
}

export function AlertsPage() {
  const navigate         = useNavigate()
  const alerts           = useAlertsStore(selectAlerts)
  const filteredAlerts   = useAlertsStore(selectFilteredAlerts)
  const isRefreshing     = useAlertsStore(selectIsRefreshing)
  const filters          = useAlertsStore(selectFilters)
  const totalAlerts      = useAlertsStore(selectTotalAlerts)
  const storeTotalPages  = useAlertsStore(s => s.totalPages)
  const storeCurrentPage = useAlertsStore(s => s.currentPage)
  const refreshAlerts    = useAlertsStore((s) => s.refreshAlerts)
  const loadAlerts       = useAlertsStore((s) => s.loadAlerts)
  const setFilters       = useAlertsStore((s) => s.setFilters)
  const clearFilters     = useAlertsStore((s) => s.clearFilters)
  const setStorePage     = useAlertsStore(s => s.setPage)
  const setStorePageSize = useAlertsStore(s => s.setPageSize)

  const [autonomousStats, setAutonomousStats] = useState<Record<string, any> | null>(null)
  const [serverCounts, setServerCounts] = useState<{
    open: number; acknowledged: number; criticalOpen: number
  } | null>(null)

  useEffect(() => {
    if (alerts.length === 0) loadAlerts()
    alertsApi.getAutonomousStats().then(res => {
      if (res.data?.success) setAutonomousStats(res.data.data)
    }).catch(() => {})
    alertsApi.getCounts().then(res => {
      const d = res.data?.data ?? res.data
      if (d) setServerCounts({ open: d.open ?? 0, acknowledged: d.acknowledged ?? 0, criticalOpen: d.critical_open ?? 0 })
    }).catch(() => {})
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  const [ui, setUI] = useState<UIState>({
    selectedAlerts: new Set(),
    activeTab: 'all-alerts',
    currentPage: 1,
    pageSize: 50,
    isBulkMode: false,
    showDetailPanel: false,
    selectedAlert: null,
    countdown: AUTO_REFRESH_SEC,
    extraFiltersOpen: false,
    selectAll: false,
  })

  const countdownRef = useRef<ReturnType<typeof setInterval> | null>(null)
  useEffect(() => {
    countdownRef.current = setInterval(() => {
      setUI(prev => {
        if (prev.countdown <= 1) {
          refreshAlerts()
          return { ...prev, countdown: AUTO_REFRESH_SEC }
        }
        return { ...prev, countdown: prev.countdown - 1 }
      })
    }, 1000)
    return () => { if (countdownRef.current) clearInterval(countdownRef.current) }
  }, [refreshAlerts])

  // WebSocket subscription — real-time alert count change detection.
  // Falls back to the 5-minute polling above when WS is unavailable.
  const wsRef = useRef<WebSocket | null>(null)
  const lastAlertCountRef = useRef<number | null>(null)
  useEffect(() => {
    const token = sessionStorage.getItem('access_token')
    if (!token) return

    const proto = window.location.protocol === 'https:' ? 'wss' : 'ws'
    const host  = window.location.host
    const url   = `${proto}://${host}/api/v1/ws/dashboard?token=${encodeURIComponent(token)}`

    let reconnectTimer: ReturnType<typeof setTimeout> | null = null
    let alive = true

    function connect() {
      if (!alive) return
      const ws = new WebSocket(url)
      wsRef.current = ws

      ws.onmessage = (ev) => {
        try {
          const msg: { type: string; event: string; data: any } = JSON.parse(ev.data)
          if (msg.event === 'metrics_update' || msg.event === 'periodic_update') {
            const count: number | undefined = msg.data?.alerts?.total
            if (count !== undefined && count !== lastAlertCountRef.current) {
              lastAlertCountRef.current = count
              refreshAlerts()
              setUI(prev => ({ ...prev, countdown: AUTO_REFRESH_SEC }))
            }
          }
        } catch { /* ignore parse errors */ }
      }

      ws.onclose = () => {
        wsRef.current = null
        if (alive) {
          reconnectTimer = setTimeout(connect, 10_000)
        }
      }

      ws.onerror = () => { ws.close() }
    }

    connect()

    return () => {
      alive = false
      if (reconnectTimer) clearTimeout(reconnectTimer)
      if (wsRef.current) {
        wsRef.current.onclose = null
        wsRef.current.close()
        wsRef.current = null
      }
    }
  }, [refreshAlerts]) // eslint-disable-line react-hooks/exhaustive-deps

  const handleRefresh = useCallback(() => {
    refreshAlerts()
    setUI(prev => ({ ...prev, countdown: AUTO_REFRESH_SEC }))
  }, [refreshAlerts])

  const handleFilter = (key: string, value: string) => {
    setFilters({ [key]: value })
    setUI(prev => ({ ...prev, currentPage: 1 }))
  }

  const handleSort = (key: string) => {
    if (filters.sort_by === key) {
      setFilters({ sort_order: filters.sort_order === 'asc' ? 'desc' : 'asc' })
    } else {
      setFilters({ sort_by: key, sort_order: 'desc' })
    }
  }


  const sorted = useMemo(() => {
    const copy  = [...filteredAlerts]
    const sb    = filters.sort_by || 'created_at'
    const order = filters.sort_order || 'desc'
    const RANK: Record<string, number> = { critical: 4, high: 3, medium: 2, low: 1 }
    copy.sort((a, b) => {
      let av: any, bv: any
      if (sb === 'severity') { av = RANK[a.severity] ?? 0; bv = RANK[b.severity] ?? 0 }
      else if (sb === 'status')  { av = a.status;    bv = b.status }
      else if (sb === 'source')  { av = a.source;    bv = b.source }
      else                       { av = a.created_at; bv = b.created_at }
      if (av < bv) return order === 'asc' ? -1 : 1
      if (av > bv) return order === 'asc' ? 1  : -1
      return 0
    })
    return copy
  }, [filteredAlerts, filters.sort_by, filters.sort_order])

  // Extra client-side filters
  const extraFiltered = useMemo(() => sorted.filter(a => {
    if (filters.entity_type) {
      const et = a.labels?.entity_type || a.metadata?.entity_type || ''
      if (!et.toLowerCase().includes(filters.entity_type.toLowerCase())) return false
    }
    if (filters.management_zone) {
      const mz = a.labels?.management_zone || a.metadata?.management_zone || ''
      if (!mz.toLowerCase().includes(filters.management_zone.toLowerCase())) return false
    }
    if (filters.correlated === 'yes' && !a.correlation_id)   return false
    if (filters.correlated === 'no'  && !!a.correlation_id)  return false
    if (filters.assigned   === 'yes' && !a.assigned_to)      return false
    if (filters.assigned   === 'no'  && !!a.assigned_to)     return false
    return true
  }), [sorted, filters.entity_type, filters.management_zone, filters.correlated, filters.assigned])

  const hasExtraFilters = !!(
    filters.entity_type || filters.management_zone || filters.correlated || filters.assigned
  )

  // When no extra client-side filters are active, delegate pagination to the store
  // (server-side: totalAlerts, totalPages, currentPage from the API response).
  // When extra filters are active, fall back to local pagination of the loaded page.
  const effectiveTotalItems = hasExtraFilters ? extraFiltered.length : totalAlerts
  const effectiveTotalPages = hasExtraFilters
    ? Math.max(1, Math.ceil(extraFiltered.length / ui.pageSize))
    : storeTotalPages
  const effectiveCurrentPage = hasExtraFilters ? Math.min(ui.currentPage, effectiveTotalPages) : storeCurrentPage

  const paged = useMemo(() => {
    if (hasExtraFilters) {
      const start = (effectiveCurrentPage - 1) * ui.pageSize
      return extraFiltered.slice(start, start + ui.pageSize)
    }
    // Server already returned the correct page; just apply the client-side sort result
    return extraFiltered
  }, [extraFiltered, hasExtraFilters, effectiveCurrentPage, ui.pageSize])

  const statusCounts = useMemo(() => {
    const counts: Record<string, number> = {}
    alerts.forEach(a => { counts[a.status] = (counts[a.status] || 0) + 1 })
    return counts
  }, [alerts])

  const hasActiveFilters = !!(
    filters.severity || filters.status || filters.source ||
    filters.entity_type || filters.management_zone ||
    filters.correlated || filters.assigned || filters.search
  )

  // Active filter chips metadata
  const activeFilterChips: { key: string; label: string }[] = []
  if (filters.severity)        activeFilterChips.push({ key: 'severity',        label: `Severity: ${filters.severity}` })
  if (filters.source)          activeFilterChips.push({ key: 'source',          label: `Source: ${filters.source}` })
  if (filters.time_range)      activeFilterChips.push({ key: 'time_range',      label: `Time: ${filters.time_range}` })
  if (filters.entity_type)     activeFilterChips.push({ key: 'entity_type',     label: `Entity: ${filters.entity_type}` })
  if (filters.correlated)      activeFilterChips.push({ key: 'correlated',      label: `Correlated: ${filters.correlated}` })
  if (filters.assigned)        activeFilterChips.push({ key: 'assigned',        label: `Assigned: ${filters.assigned}` })
  if (filters.management_zone) activeFilterChips.push({ key: 'management_zone', label: `Zone: ${filters.management_zone}` })

  const sidebarItems = [
    { id: 'all-alerts',    label: 'All Alerts',      icon: Bell,        iconColor: ap.blue,   count: filteredAlerts.length },
    { id: 'smart-insights',label: 'Smart Insights',  icon: Lightbulb,   iconColor: '#FFCC00' },
    { id: 'correlations',  label: 'AI Correlations', icon: Link,        iconColor: ap.purple },
    { id: 'automation',    label: 'Automation',      icon: Zap,         iconColor: ap.orange },
    { id: 'deduplication', label: 'Deduplication',   icon: Fingerprint, iconColor: '#5856D6' },
    { id: 'mapping',       label: 'Mapping Rules',   icon: FileJson,    iconColor: '#5856D6' },
    { id: 'quality',       label: 'Quality',         icon: Target,      iconColor: ap.green  },
    { id: 'analytics',     label: 'Analytics',       icon: BarChart3,   iconColor: ap.green  },
  ]

  const STATUS_TABS = [
    { key: '',             label: 'All' },
    { key: 'open',         label: 'Open' },
    { key: 'acknowledged', label: 'Acknowledged' },
    { key: 'investigating',label: 'Investigating' },
    { key: 'resolved',     label: 'Resolved' },
  ]

  // Column header padding must match card padding (0 10px + gap 8)
  const headerPad = `7px 10px`

  return (
    <div style={{ minHeight: '100vh', background: ap.bg }}>
      <div style={{
        display: 'flex', flexDirection: 'row',
        maxWidth: 1440, margin: '0 auto',
        padding: '24px 16px', gap: 28, minHeight: '100vh',
      }}>

        {/* ── Sidebar ── */}
        <div style={{ position: 'sticky', top: 24, alignSelf: 'flex-start', width: 216 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '4px 10px 20px' }}>
            <span style={{ fontSize: 24, fontWeight: 700, color: ap.label, letterSpacing: '-0.03em' }}>
              Alerts
            </span>
            <div style={{
              display: 'flex', alignItems: 'center', gap: 4,
              padding: '2px 7px', borderRadius: 6, background: 'rgba(52,199,89,0.09)',
              border: '0.5px solid rgba(52,199,89,0.25)',
            }}>
              <div style={{ width: 5, height: 5, borderRadius: '50%', background: ap.green, animation: 'pulse 2s infinite' }} />
              <span style={{ fontSize: 9, fontWeight: 700, color: ap.green, textTransform: 'uppercase', letterSpacing: '0.5px' }}>
                Live
              </span>
            </div>
          </div>

          <Sidebar items={sidebarItems} selected={ui.activeTab} onSelect={(id) => setUI(prev => ({ ...prev, activeTab: id }))} />

          {/* Status summary */}
          <div style={{
            marginTop: 16, padding: '12px 10px', borderRadius: ap.r.md,
            background: ap.card, border: `0.5px solid ${ap.sep}`,
          }}>
            <div style={{
              fontSize: 10, fontWeight: 700, color: ap.tert,
              marginBottom: 10, textTransform: 'uppercase', letterSpacing: '0.6px',
            }}>
              Status Summary
            </div>
            {[
              { s: 'open',          label: 'Open',         color: '#CC1F11' },
              { s: 'acknowledged',  label: 'Acknowledged', color: '#C25000' },
              { s: 'investigating', label: 'Investigating',color: '#0062CC' },
              { s: 'resolved',      label: 'Resolved',     color: '#1A7F3C' },
            ].map(({ s, label, color }) => (
              <div key={s} style={{
                display: 'flex', justifyContent: 'space-between', alignItems: 'center',
                marginBottom: 6, cursor: 'pointer',
              }}
                onClick={() => handleFilter('status', filters.status === s ? '' : s)}
              >
                <span style={{ fontSize: 12, color: filters.status === s ? color : ap.sec, fontWeight: filters.status === s ? 600 : 400 }}>
                  {label}
                </span>
                <span style={{
                  fontSize: 11, fontWeight: 600, color,
                  background: `${color}15`, padding: '1px 7px', borderRadius: 4,
                }}>
                  {statusCounts[s] ?? 0}
                </span>
              </div>
            ))}
          </div>
        </div>

        {/* ── Content ── */}
        <div style={{ flex: 1, minWidth: 0 }}>

          {/* Stats strip */}
          {ui.activeTab === 'all-alerts' && (
            <StatsStrip
              totalAlerts={totalAlerts}
              serverCounts={serverCounts}
              alerts={alerts}
              autonomousStats={autonomousStats}
            />
          )}

          {/* Page header */}
          <div style={{ marginBottom: 14 }}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 10 }}>
              <div>
                <h1 style={{ fontSize: 18, fontWeight: 700, color: ap.label, margin: 0, letterSpacing: '-0.01em' }}>
                  {sidebarItems.find(i => i.id === ui.activeTab)?.label ?? 'Alerts'}
                </h1>
                <p style={{ fontSize: 12, color: ap.sec, marginTop: 2, margin: 0 }}>
                  {extraFiltered.length.toLocaleString()} of {totalAlerts.toLocaleString()} alerts
                  {hasActiveFilters ? ' (filtered)' : ''}
                </p>
              </div>

              <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
                {/* Auto-refresh indicator */}
                <div style={{
                  display: 'flex', alignItems: 'center', gap: 5, padding: '5px 10px',
                  borderRadius: ap.r.sm, background: ap.card, border: `0.5px solid ${ap.sep}`,
                }}>
                  <Wifi style={{ width: 11, height: 11, color: ap.green }} />
                  <span style={{ fontSize: 11, color: ap.sec, fontVariantNumeric: 'tabular-nums' }}>
                    {ui.countdown}s
                  </span>
                </div>
                <button
                  onClick={handleRefresh}
                  disabled={isRefreshing}
                  style={{
                    display: 'flex', alignItems: 'center', gap: 5, padding: '6px 12px',
                    borderRadius: ap.r.sm, border: `0.5px solid ${ap.sep}`,
                    background: ap.card, color: ap.label, fontSize: 12, fontWeight: 500,
                    cursor: isRefreshing ? 'default' : 'pointer', opacity: isRefreshing ? 0.5 : 1,
                  }}
                >
                  <RefreshCw style={{ width: 12, height: 12, ...(isRefreshing ? { animation: 'spin 1s linear infinite' } : {}) }} />
                  Refresh
                </button>
                <button
                  onClick={() => setUI(prev => ({
                    ...prev,
                    isBulkMode: !prev.isBulkMode,
                    selectedAlerts: new Set(),
                    selectAll: false,
                  }))}
                  style={{
                    display: 'flex', alignItems: 'center', gap: 5, padding: '6px 12px',
                    borderRadius: ap.r.sm, border: 'none',
                    background: ui.isBulkMode ? ap.blue : ap.fill,
                    color: ui.isBulkMode ? '#fff' : ap.label,
                    fontSize: 12, fontWeight: 500, cursor: 'pointer',
                  }}
                >
                  <CheckSquare style={{ width: 12, height: 12 }} />
                  {ui.isBulkMode ? 'Exit Bulk' : 'Bulk Select'}
                </button>
              </div>
            </div>

            {/* Status tabs */}
            {ui.activeTab === 'all-alerts' && (
              <div style={{ display: 'flex', gap: 2, marginBottom: 10 }}>
                {STATUS_TABS.map(({ key, label }) => {
                  const active = (filters.status || '') === key
                  const cnt = key ? (statusCounts[key] ?? 0) : totalAlerts
                  return (
                    <button
                      key={key}
                      onClick={() => handleFilter('status', key)}
                      style={{
                        padding: '5px 12px', borderRadius: ap.r.sm,
                        border: active ? 'none' : `0.5px solid ${ap.sep}`,
                        background: active ? ap.blue : 'transparent',
                        color: active ? '#fff' : ap.sec,
                        fontSize: 12, fontWeight: active ? 600 : 400, cursor: 'pointer',
                        display: 'flex', alignItems: 'center', gap: 6,
                      }}
                    >
                      {label}
                      <span style={{
                        fontSize: 10, fontVariantNumeric: 'tabular-nums',
                        background: active ? 'rgba(255,255,255,0.22)' : ap.fill,
                        color: active ? '#fff' : ap.tert,
                        padding: '0 5px', borderRadius: 8, minWidth: 18, textAlign: 'center',
                      }}>
                        {cnt.toLocaleString()}
                      </span>
                    </button>
                  )
                })}
              </div>
            )}

            {/* Filter row */}
            {ui.activeTab === 'all-alerts' && (
              <>
                <div style={{ display: 'flex', gap: 6, alignItems: 'center', flexWrap: 'wrap' }}>
                  <div style={{ width: 240 }}>
                    <SearchInput
                      value={filters.search || ''}
                      onChange={(v) => { setFilters({ search: v }); setUI(prev => ({ ...prev, currentPage: 1 })) }}
                      placeholder="Search title, entity, hostname…"
                    />
                  </div>
                  <FilterSelect
                    value={filters.severity || ''} onChange={(v) => handleFilter('severity', v)}
                    placeholder="Severity"
                    options={[
                      { value: 'critical', label: 'Critical' },
                      { value: 'high',     label: 'High' },
                      { value: 'medium',   label: 'Medium' },
                      { value: 'low',      label: 'Low' },
                    ]}
                  />
                  <FilterSelect
                    value={filters.source || ''} onChange={(v) => handleFilter('source', v)}
                    placeholder="Source"
                    options={[
                      { value: 'dynatrace',  label: 'Dynatrace' },
                      { value: 'prometheus', label: 'Prometheus' },
                      { value: 'grafana',    label: 'Grafana' },
                      { value: 'splunk',     label: 'Splunk' },
                      { value: 'webhook',    label: 'Webhook' },
                    ]}
                  />
                  <FilterSelect
                    value={filters.time_range || ''} onChange={(v) => handleFilter('time_range', v)}
                    placeholder="Time range"
                    options={[
                      { value: '15m', label: 'Last 15 min' },
                      { value: '1h',  label: 'Last 1 hour' },
                      { value: '6h',  label: 'Last 6 hours' },
                      { value: '24h', label: 'Last 24 hours' },
                      { value: '7d',  label: 'Last 7 days' },
                    ]}
                  />
                  <button
                    onClick={() => setUI(prev => ({ ...prev, extraFiltersOpen: !prev.extraFiltersOpen }))}
                    style={{
                      display: 'flex', alignItems: 'center', gap: 4,
                      padding: '5px 10px', borderRadius: ap.r.sm, cursor: 'pointer', fontSize: 12,
                      border: ui.extraFiltersOpen ? `0.5px solid ${ap.blue}` : `0.5px solid ${ap.sep}`,
                      background: ui.extraFiltersOpen ? 'rgba(0,122,255,0.07)' : ap.card,
                      color: ui.extraFiltersOpen ? ap.blue : ap.sec,
                      fontWeight: ui.extraFiltersOpen ? 600 : 400,
                    }}
                  >
                    <Filter style={{ width: 11, height: 11 }} />
                    More filters
                    {(filters.entity_type || filters.management_zone || filters.correlated || filters.assigned) && (
                      <span style={{ width: 5, height: 5, borderRadius: '50%', background: ap.orange, flexShrink: 0 }} />
                    )}
                  </button>
                  <FilterSelect
                    value={filters.sort_by || ''} onChange={(v) => handleFilter('sort_by', v)}
                    placeholder="Sort by"
                    options={[
                      { value: 'created_at', label: 'Newest first' },
                      { value: 'severity',   label: 'Severity' },
                      { value: 'status',     label: 'Status' },
                      { value: 'source',     label: 'Source' },
                    ]}
                  />
                  {hasActiveFilters && (
                    <button
                      onClick={() => { clearFilters(); setUI(prev => ({ ...prev, currentPage: 1 })) }}
                      style={{
                        display: 'flex', alignItems: 'center', gap: 4,
                        padding: '5px 10px', borderRadius: ap.r.sm,
                        border: `0.5px solid ${ap.sep}`, background: ap.card,
                        color: ap.sec, fontSize: 12, cursor: 'pointer',
                      }}
                    >
                      <X style={{ width: 11, height: 11 }} /> Clear all
                    </button>
                  )}
                </div>

                {/* Extra filter panel */}
                <AnimatePresence>
                  {ui.extraFiltersOpen && (
                    <motion.div
                      initial={{ opacity: 0, height: 0 }}
                      animate={{ opacity: 1, height: 'auto' }}
                      exit={{ opacity: 0, height: 0 }}
                      style={{
                        display: 'flex', gap: 6, flexWrap: 'wrap', marginTop: 8,
                        padding: '10px 12px', borderRadius: ap.r.md,
                        background: ap.card, border: `0.5px solid ${ap.sep}`,
                      }}
                    >
                      <FilterSelect
                        value={filters.entity_type || ''} onChange={(v) => handleFilter('entity_type', v)}
                        placeholder="Entity type"
                        options={[
                          { value: 'host',          label: 'Host / VM' },
                          { value: 'node',          label: 'K8s Node' },
                          { value: 'pod',           label: 'Pod / Container' },
                          { value: 'service',       label: 'Service' },
                          { value: 'application',   label: 'Application' },
                          { value: 'process_group', label: 'Process Group' },
                        ]}
                      />
                      <FilterSelect
                        value={filters.correlated || ''} onChange={(v) => handleFilter('correlated', v)}
                        placeholder="Correlation"
                        options={[
                          { value: 'yes', label: 'Correlated' },
                          { value: 'no',  label: 'Not correlated' },
                        ]}
                      />
                      <FilterSelect
                        value={filters.assigned || ''} onChange={(v) => handleFilter('assigned', v)}
                        placeholder="Assignment"
                        options={[
                          { value: 'yes', label: 'Assigned' },
                          { value: 'no',  label: 'Unassigned' },
                        ]}
                      />
                      <div style={{ width: 160 }}>
                        <SearchInput
                          value={filters.management_zone || ''}
                          onChange={(v) => handleFilter('management_zone', v)}
                          placeholder="Management zone…"
                        />
                      </div>
                    </motion.div>
                  )}
                </AnimatePresence>

                {/* Active filter chips */}
                {activeFilterChips.length > 0 && (
                  <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', marginTop: 8 }}>
                    {activeFilterChips.map(chip => (
                      <FilterChip
                        key={chip.key}
                        label={chip.label}
                        onRemove={() => handleFilter(chip.key, '')}
                      />
                    ))}
                  </div>
                )}
              </>
            )}
          </div>

          {/* Content area */}
          <div style={{
            background: ap.card,
            borderRadius: ap.r.lg,
            border: `0.5px solid ${ap.sep}`,
            overflow: 'hidden',
            minHeight: 400,
          }}>
            <AnimatePresence mode="wait">
              <motion.div
                key={ui.activeTab}
                initial={{ opacity: 0, y: 4 }}
                animate={{ opacity: 1, y: 0 }}
                exit={{ opacity: 0, y: -4 }}
                transition={{ duration: 0.12 }}
              >
                {ui.activeTab === 'all-alerts' && (
                  <div>
                    {/* Column headers */}
                    <div style={{
                      display: 'flex', alignItems: 'center',
                      padding: headerPad,
                      borderBottom: `0.5px solid ${ap.sep}`,
                      background: 'rgba(142,142,147,0.04)',
                      gap: COL.gap,
                    }}>
                      {/* Bulk select-all */}
                      {ui.isBulkMode && (
                        <div
                          onClick={() => {
                            const allOnPage = new Set(paged.map(a => a.id))
                            const allSelected = paged.every(a => ui.selectedAlerts.has(a.id))
                            setUI(prev => ({
                              ...prev,
                              selectedAlerts: allSelected
                                ? new Set([...prev.selectedAlerts].filter(id => !allOnPage.has(id)))
                                : new Set([...prev.selectedAlerts, ...allOnPage]),
                            }))
                          }}
                          style={{
                            width: 16, height: 16, borderRadius: 4, flexShrink: 0,
                            border: `1.5px solid ${ap.sep}`,
                            background: paged.length > 0 && paged.every(a => ui.selectedAlerts.has(a.id)) ? ap.blue : 'transparent',
                            display: 'flex', alignItems: 'center', justifyContent: 'center', cursor: 'pointer',
                          }}
                        >
                          {paged.length > 0 && paged.every(a => ui.selectedAlerts.has(a.id)) && (
                            <svg width="10" height="10" viewBox="0 0 10 10" fill="none">
                              <path d="M2 5l2.5 2.5L8 3" stroke="#fff" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
                            </svg>
                          )}
                        </div>
                      )}

                      <ColHeader
                        label="Severity" sortKey="severity"
                        currentSort={filters.sort_by} currentOrder={filters.sort_order}
                        onSort={handleSort}
                        style={{ width: COL.sev, flexShrink: 0 }}
                      />
                      <ColHeader
                        label="Status" sortKey="status"
                        currentSort={filters.sort_by} currentOrder={filters.sort_order}
                        onSort={handleSort}
                        style={{ width: COL.status, flexShrink: 0 }}
                      />
                      <ColHeader
                        label="Alert / Entity"
                        style={{ flex: 1 }}
                      />
                      <ColHeader
                        label="Source / Zone" sortKey="source"
                        currentSort={filters.sort_by} currentOrder={filters.sort_order}
                        onSort={handleSort}
                        style={{ width: COL.zone, flexShrink: 0, textAlign: 'right' as const }}
                      />
                      <ColHeader label="Impact" style={{ width: COL.flags, flexShrink: 0, textAlign: 'right' as const }} />
                      <ColHeader
                        label="Age" sortKey="created_at"
                        currentSort={filters.sort_by} currentOrder={filters.sort_order}
                        onSort={handleSort}
                        style={{ width: COL.age, flexShrink: 0, textAlign: 'right' as const }}
                      />
                      <div style={{ width: COL.assignee, flexShrink: 0 }} />
                      <div style={{ width: COL.actions, flexShrink: 0 }} />
                    </div>

                    {paged.length === 0 ? (
                      <div style={{ textAlign: 'center', padding: '72px 20px' }}>
                        <Bell style={{ width: 36, height: 36, color: ap.tert, margin: '0 auto 12px' }} />
                        <p style={{ fontSize: 14, fontWeight: 600, color: ap.sec, margin: '0 0 4px' }}>
                          No alerts found
                        </p>
                        {hasActiveFilters && (
                          <>
                            <p style={{ fontSize: 12, color: ap.tert, margin: '0 0 14px' }}>
                              Your active filters returned no results.
                            </p>
                            <button
                              onClick={() => { clearFilters(); setUI(prev => ({ ...prev, currentPage: 1 })) }}
                              style={{
                                padding: '7px 16px', borderRadius: ap.r.sm, border: 'none',
                                background: ap.blue, color: '#fff', fontSize: 13, cursor: 'pointer',
                              }}
                            >
                              Clear filters
                            </button>
                          </>
                        )}
                      </div>
                    ) : (
                      <div style={{ padding: '8px 10px', display: 'flex', flexDirection: 'column', gap: 2 }}>
                        {paged.map((alert) => (
                          <AlertCard
                            key={alert.id}
                            alert={alert}
                            bulkMode={ui.isBulkMode}
                            isSelected={ui.selectedAlerts.has(alert.id)}
                            onSelect={(a) => {
                              setUI(prev => {
                                const s = new Set(prev.selectedAlerts)
                                s.has(a.id) ? s.delete(a.id) : s.add(a.id)
                                return { ...prev, selectedAlerts: s }
                              })
                            }}
                            onCardClick={(a) => setUI(prev => ({ ...prev, selectedAlert: a, showDetailPanel: true }))}
                            onAcknowledge={async (a) => {
                              try { await alertsApi.acknowledge(a.id); refreshAlerts() } catch {}
                            }}
                            onResolve={async (a) => {
                              try { await alertsApi.resolve(a.id, { notes: 'Resolved' }); refreshAlerts() } catch {}
                            }}
                            onAssign={async (a, uid) => {
                              try { await alertsApi.assign(a.id, { assign_to: uid }); refreshAlerts() } catch {}
                            }}
                            onDynatraceLink={(a) => {
                              const url = a.metadata?.dynatrace_problem_url || a.metadata?.problemUrl
                              if (url) window.open(url, '_blank', 'noopener')
                            }}
                          />
                        ))}
                      </div>
                    )}

                    {/* Pagination — always shown */}
                    <Pagination
                      currentPage={effectiveCurrentPage}
                      totalPages={effectiveTotalPages}
                      pageSize={ui.pageSize}
                      totalItems={effectiveTotalItems}
                      onPageChange={(p) => {
                        if (hasExtraFilters) {
                          setUI(prev => ({ ...prev, currentPage: p }))
                        } else {
                          setStorePage(p)
                        }
                      }}
                      onPageSizeChange={(s) => {
                        setUI(prev => ({ ...prev, pageSize: s, currentPage: 1 }))
                        if (!hasExtraFilters) setStorePageSize(s)
                      }}
                    />
                  </div>
                )}

                {ui.activeTab === 'smart-insights' && (
                  <div style={{ padding: 16 }}>
                    <SmartInsightsRenderer
                      alerts={alerts}
                      onAlertClick={(a) => setUI(prev => ({ ...prev, selectedAlert: a, showDetailPanel: true }))}
                      onFilterPreset={(preset) => {
                        const presets: Record<string, Partial<AlertFilters>> = {
                          open: { status: 'open' }, acknowledged: { status: 'acknowledged' }, critical: { severity: 'critical' },
                        }
                        if (presets[preset]) setFilters(presets[preset])
                      }}
                    />
                  </div>
                )}

                {ui.activeTab === 'correlations' && (
                  <CorrelationsTab
                    alerts={alerts}
                    onAlertClick={(a) => setUI(prev => ({ ...prev, selectedAlert: a, showDetailPanel: true }))}
                  />
                )}

                {ui.activeTab === 'automation' && (
                  <div style={{ padding: 16 }}>
                    <AlertAutomation
                      alerts={alerts}
                      onExecuteAction={async (alertId, action, params) => {
                        console.log('Automation:', action, alertId, params)
                        return true
                      }}
                    />
                  </div>
                )}

                {ui.activeTab === 'deduplication' && <DeduplicationPage />}
                {ui.activeTab === 'mapping'       && <MappingRulesPage />}
                {ui.activeTab === 'quality'       && <AlertQualityPage />}
                {ui.activeTab === 'analytics'     && <AnalyticsTab autonomousStats={autonomousStats} />}

                {!['all-alerts','smart-insights','correlations','automation','deduplication','mapping','quality','analytics'].includes(ui.activeTab) && (
                  <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', padding: '80px 20px', textAlign: 'center' }}>
                    <div>
                      <Settings style={{ width: 36, height: 36, color: ap.tert, margin: '0 auto 12px' }} />
                      <p style={{ fontSize: 13, color: ap.tert }}>Coming soon</p>
                    </div>
                  </div>
                )}
              </motion.div>
            </AnimatePresence>
          </div>
        </div>
      </div>

      {/* Bulk Actions Bar */}
      <AnimatePresence>
        {ui.isBulkMode && ui.selectedAlerts.size > 0 && (
          <motion.div
            initial={{ opacity: 0, y: 20 }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0, y: 20 }}
            style={{
              position: 'fixed', bottom: 24, left: '50%', transform: 'translateX(-50%)',
              background: ap.card, border: `0.5px solid ${ap.sep}`,
              borderRadius: ap.r.lg, padding: '10px 20px',
              boxShadow: '0 8px 40px rgba(0,0,0,0.18), 0 0 0 1px rgba(142,142,147,0.10)',
              display: 'flex', alignItems: 'center', gap: 12, zIndex: 50,
            }}
          >
            <span style={{ fontSize: 12, fontWeight: 600, color: ap.label }}>
              {ui.selectedAlerts.size.toLocaleString()} selected
            </span>
            <div style={{ width: 1, height: 16, background: ap.sep }} />
            {[
              { label: 'Acknowledge', color: ap.green, bg: 'rgba(52,199,89,0.12)', action: async () => {
                try {
                  await alertsApi.bulkAcknowledge(Array.from(ui.selectedAlerts))
                  refreshAlerts()
                  setUI(prev => ({ ...prev, selectedAlerts: new Set() }))
                } catch {}
              }},
              { label: 'Resolve', color: ap.blue, bg: 'rgba(0,122,255,0.12)', action: async () => {
                try {
                  await alertsApi.bulkResolve(Array.from(ui.selectedAlerts), 'Bulk resolved')
                  refreshAlerts()
                  setUI(prev => ({ ...prev, selectedAlerts: new Set() }))
                } catch {}
              }},
            ].map(btn => (
              <button
                key={btn.label}
                onClick={btn.action}
                style={{
                  padding: '6px 14px', borderRadius: ap.r.sm, border: `0.5px solid ${btn.bg}`,
                  background: btn.bg, color: btn.color,
                  fontSize: 12, fontWeight: 600, cursor: 'pointer',
                }}
              >
                {btn.label}
              </button>
            ))}
            <button
              onClick={() => setUI(prev => ({ ...prev, selectedAlerts: new Set() }))}
              style={{
                padding: '5px 10px', borderRadius: ap.r.sm,
                border: `0.5px solid ${ap.sep}`, background: 'transparent',
                color: ap.sec, fontSize: 12, cursor: 'pointer',
                display: 'flex', alignItems: 'center', gap: 4,
              }}
            >
              <X style={{ width: 11, height: 11 }} /> Deselect
            </button>
          </motion.div>
        )}
      </AnimatePresence>

      {/* Alert Detail Panel */}
      {ui.showDetailPanel && ui.selectedAlert && (
        <AlertDetailPanel
          alert={ui.selectedAlert}
          isOpen={ui.showDetailPanel}
          onClose={() => setUI(prev => ({ ...prev, showDetailPanel: false, selectedAlert: null }))}
          allAlerts={alerts}
          onViewSimilar={(a) => setUI(prev => ({ ...prev, selectedAlert: a }))}
          onAcknowledge={async (id) => {
            try { await alertsApi.acknowledge(id); refreshAlerts(); setUI(prev => ({ ...prev, showDetailPanel: false, selectedAlert: null })) } catch {}
          }}
          onResolve={async (id) => {
            try { await alertsApi.resolve(id, { notes: 'Resolved from detail panel' }); refreshAlerts(); setUI(prev => ({ ...prev, showDetailPanel: false, selectedAlert: null })) } catch {}
          }}
          onAssign={async (id, userId) => {
            if (userId) {
              try { await alertsApi.assign(id, { assign_to: userId }); refreshAlerts() } catch {}
            }
          }}
        />
      )}

      <style>{`
        @keyframes spin  { from { transform: rotate(0deg) } to { transform: rotate(360deg) } }
        @keyframes pulse { 0%, 100% { opacity: 1 } 50% { opacity: 0.4 } }
      `}</style>
    </div>
  )
}
