import React, { useState, useMemo, useEffect, useCallback } from 'react'
import { motion, AnimatePresence } from 'framer-motion'
import {
  X, Check, CheckCheck, UserPlus, ExternalLink, Brain, Activity,
  Download, Copy, Clock, Server, GitMerge, Link2, Zap, Hash,
  MessageSquare, Send, Trash2, Shield, ChevronDown, ChevronRight,
  AlertTriangle, TrendingUp, Wrench, RefreshCw, Pause, Play,
  BookOpen, Target, Cpu, Network, BarChart2, Loader2, ThumbsUp,
  ThumbsDown, Terminal, Bell, BellOff, Info,
} from 'lucide-react'
import type { Alert } from '@/types'
import { MLCorrelationEngine } from '@/lib/ml-correlations'
import { mlLearningSystem } from '@/lib/ml-learning'
import { extractMetadataFromAlert } from '@/lib/metadata-extractor'
import { SimilarAlertsViewer } from './SimilarAlertsViewer'
import { alertsApi } from '@/lib/api'
import toast from 'react-hot-toast'

// ── Design tokens ─────────────────────────────────────────────────────────────
const c = {
  blue:   '#007AFF', green:  '#34C759', red:    '#FF3B30', orange: '#FF9500',
  yellow: '#FFCC00', purple: '#AF52DE', teal:   '#30B0C7', indigo: '#5856D6',
  pink:   '#FF2D55', gray:   '#8E8E93',
  label:  'var(--color-text)',
  sec:    'var(--color-text-secondary)',
  tert:   'var(--color-text-tertiary, #8E8E93)',
  sep:    'var(--color-separator, rgba(142,142,147,0.12))',
  fill:   'var(--color-fill, rgba(142,142,147,0.08))',
  fill2:  'rgba(142,142,147,0.12)',
  bg:     'var(--color-background)',
  card:   'var(--color-card, rgba(255,255,255,0.8))',
  r:      { xs: 4, sm: 6, md: 10, lg: 12 },
} as const

const SEV: Record<string, { color: string; bg: string; text: string }> = {
  critical: { color: '#fff',  bg: c.red,    text: 'CRITICAL' },
  high:     { color: '#fff',  bg: c.orange, text: 'HIGH' },
  medium:   { color: '#664400', bg: c.yellow, text: 'MEDIUM' },
  low:      { color: '#fff',  bg: c.blue,   text: 'LOW' },
}
const STATUS: Record<string, { color: string; label: string }> = {
  open:          { color: c.red,    label: 'Open' },
  acknowledged:  { color: c.orange, label: 'Acknowledged' },
  investigating: { color: c.blue,   label: 'Investigating' },
  resolved:      { color: c.green,  label: 'Resolved' },
  closed:        { color: c.gray,   label: 'Closed' },
}
type Tab = 'overview' | 'infrastructure' | 'analysis' | 'activity'

// ── Auth header helper ────────────────────────────────────────────────────────
function authHeaders() {
  const tok = sessionStorage.getItem('access_token') || localStorage.getItem('access_token') || ''
  return { 'Content-Type': 'application/json', ...(tok && { Authorization: `Bearer ${tok}` }) }
}
async function apiFetch(path: string, opts: RequestInit = {}) {
  const r = await fetch(path, { headers: authHeaders(), ...opts })
  if (!r.ok) throw new Error(`HTTP ${r.status}`)
  return r.json()
}

// ── Micro-components ─────────────────────────────────────────────────────────
function Chip({ label, color, bg }: { label: string; color: string; bg: string }) {
  return (
    <span style={{
      fontSize: 10, fontWeight: 700, padding: '3px 7px', borderRadius: 4,
      background: bg, color, letterSpacing: '0.4px', flexShrink: 0, lineHeight: 1.4,
    }}>{label}</span>
  )
}

function Row({ label, value, mono, accent, onCopy }: {
  label: string; value?: string | null; mono?: boolean; accent?: string; onCopy?: () => void
}) {
  if (!value) return null
  return (
    <div style={{
      display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start',
      padding: '5px 0', borderBottom: `0.5px solid ${c.sep}`,
    }}>
      <span style={{ fontSize: 11, color: c.tert, minWidth: 112, flexShrink: 0, paddingRight: 8 }}>{label}</span>
      <span
        style={{
          fontSize: 12, color: accent || c.label, textAlign: 'right',
          fontFamily: mono ? 'ui-monospace,monospace' : undefined,
          wordBreak: 'break-all', flex: 1, cursor: onCopy ? 'pointer' : undefined,
        }}
        onClick={onCopy}
        title={onCopy ? 'Click to copy' : undefined}
      >
        {value}
        {onCopy && <Copy style={{ width: 9, height: 9, marginLeft: 3, opacity: 0.4, display: 'inline', verticalAlign: 'middle' }} />}
      </span>
    </div>
  )
}

function SectionHead({ label, accent }: { label: string; accent?: string }) {
  return (
    <div style={{
      fontSize: 9, fontWeight: 700, color: accent || c.tert, textTransform: 'uppercase',
      letterSpacing: '0.9px', padding: '14px 0 6px', userSelect: 'none',
    }}>{label}</div>
  )
}

function StatCard({ label, value, color, icon: Icon, sub }: {
  label: string; value: string; color: string; icon?: React.ElementType; sub?: string
}) {
  return (
    <div style={{
      flex: 1, padding: '10px 11px', borderRadius: c.r.md,
      background: `${color}12`, border: `0.5px solid ${color}35`,
      display: 'flex', flexDirection: 'column', gap: 3, minWidth: 0,
    }}>
      {Icon && <Icon style={{ width: 11, height: 11, color, marginBottom: 1 }} />}
      <div style={{ fontSize: 15, fontWeight: 700, color, lineHeight: 1, letterSpacing: '-0.4px' }}>{value}</div>
      <div style={{ fontSize: 9.5, color: c.tert, fontWeight: 500, letterSpacing: '0.2px' }}>{label}</div>
      {sub && <div style={{ fontSize: 9, color, opacity: 0.7 }}>{sub}</div>}
    </div>
  )
}

function ActionBtn({ label, icon: Icon, onClick, color, disabled, loading }: {
  label: string; icon: React.ElementType; onClick: () => void; color: string; disabled?: boolean; loading?: boolean
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled || loading}
      style={{
        flex: 1, minWidth: 'calc(50% - 4px)', padding: '9px 8px', borderRadius: c.r.md,
        background: disabled ? c.fill : `${color}12`,
        border: `0.5px solid ${disabled ? c.sep : `${color}40`}`,
        color: disabled ? c.tert : color,
        cursor: disabled ? 'not-allowed' : 'pointer',
        fontSize: 11.5, fontWeight: 600,
        display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 5,
        transition: 'background 0.12s, transform 0.1s',
        opacity: loading ? 0.7 : 1,
      }}
      onMouseEnter={(e) => { if (!disabled && !loading) e.currentTarget.style.background = `${color}22` }}
      onMouseLeave={(e) => { if (!disabled && !loading) e.currentTarget.style.background = `${color}12` }}
      onMouseDown={(e) => { if (!disabled && !loading) e.currentTarget.style.transform = 'scale(0.97)' }}
      onMouseUp={(e) => { e.currentTarget.style.transform = 'scale(1)' }}
    >
      {loading
        ? <Loader2 style={{ width: 14, height: 14, animation: 'spin 1s linear infinite' }} />
        : <Icon style={{ width: 14, height: 14 }} />
      }
      {label}
    </button>
  )
}

function ConfBar({ value, max = 100, color }: { value: number; max?: number; color: string }) {
  const pct = Math.min(100, Math.round((value / max) * 100))
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8, flex: 1 }}>
      <div style={{ flex: 1, height: 4, borderRadius: 2, background: c.fill2, overflow: 'hidden' }}>
        <div style={{
          height: '100%', borderRadius: 2, background: color,
          width: `${pct}%`, transition: 'width 0.4s ease',
        }} />
      </div>
      <span style={{ fontSize: 11, fontWeight: 600, color, minWidth: 28, textAlign: 'right' }}>{pct}%</span>
    </div>
  )
}

function Collapsible({ title, accent, children, defaultOpen = true }: {
  title: string; accent?: string; children: React.ReactNode; defaultOpen?: boolean
}) {
  const [open, setOpen] = useState(defaultOpen)
  return (
    <div style={{ marginBottom: 2 }}>
      <button
        onClick={() => setOpen(o => !o)}
        style={{
          display: 'flex', alignItems: 'center', gap: 6, width: '100%',
          background: 'none', border: 'none', cursor: 'pointer',
          padding: '12px 0 6px', textAlign: 'left',
        }}
      >
        <span style={{
          fontSize: 9.5, fontWeight: 700, color: accent || c.tert,
          textTransform: 'uppercase', letterSpacing: '0.8px', flex: 1,
        }}>{title}</span>
        {open
          ? <ChevronDown style={{ width: 12, height: 12, color: c.tert }} />
          : <ChevronRight style={{ width: 12, height: 12, color: c.tert }} />
        }
      </button>
      {open && <>{children}</>}
    </div>
  )
}

function LoadBtn({ label, onClick, loading }: { label: string; onClick: () => void; loading: boolean }) {
  return (
    <button
      onClick={onClick}
      disabled={loading}
      style={{
        fontSize: 11, fontWeight: 600, color: c.blue, background: `${c.blue}10`,
        border: `0.5px solid ${c.blue}40`, borderRadius: c.r.sm,
        padding: '5px 10px', cursor: loading ? 'default' : 'pointer',
        display: 'flex', alignItems: 'center', gap: 4,
      }}
    >
      {loading
        ? <Loader2 style={{ width: 10, height: 10, animation: 'spin 1s linear infinite' }} />
        : <RefreshCw style={{ width: 10, height: 10 }} />
      }
      {loading ? 'Loading…' : label}
    </button>
  )
}

// ── Duration helper ───────────────────────────────────────────────────────────
function fmtDuration(startIso: string, endIso?: string) {
  const start = new Date(startIso).getTime()
  const end   = endIso ? new Date(endIso).getTime() : Date.now()
  const diff  = end - start
  const h = Math.floor(diff / 3600000)
  const m = Math.floor((diff % 3600000) / 60000)
  if (h >= 24) return `${Math.floor(h / 24)}d ${h % 24}h`
  return h > 0 ? `${h}h ${m}m` : `${m}m`
}

// ── Props ─────────────────────────────────────────────────────────────────────
interface AlertDetailPanelProps {
  alert: Alert
  isOpen: boolean
  onClose: () => void
  onAcknowledge?: (id: string) => Promise<void>
  onResolve?: (id: string) => Promise<void>
  onAssign?: (id: string, userId?: string) => Promise<void>
  allAlerts?: Alert[]
  onViewSimilar?: (alert: Alert) => void
}

// ═══════════════════════════════════════════════════════════════════════════════
// Main Component
// ═══════════════════════════════════════════════════════════════════════════════
export function AlertDetailPanel({
  alert, isOpen, onClose, onAcknowledge, onResolve, onAssign,
  allAlerts = [], onViewSimilar,
}: AlertDetailPanelProps) {
  const [tab, setTab]         = useState<Tab>('overview')
  const [comments, setComments]         = useState<any[]>([])
  const [commentText, setCommentText]   = useState('')
  const [commentsLoading, setCommentsLoading] = useState(false)
  const [actionLoading, setActionLoading]     = useState<Record<string, boolean>>({})
  const [analysisData, setAnalysisData]       = useState<any>(null)
  const [predictionData, setPredictionData]   = useState<any>(null)
  const [runbooksData, setRunbooksData]       = useState<any[]>([])
  const [correlationData, setCorrelationData] = useState<any>(null)
  const [autonomousData, setAutonomousData]   = useState<any>(null)
  const [maintenanceActive, setMaintenanceActive] = useState(false)

  // Reset state when alert changes
  useEffect(() => {
    setTab('overview')
    setAnalysisData(null)
    setPredictionData(null)
    setRunbooksData([])
    setCorrelationData(null)
    setAutonomousData(null)
  }, [alert?.id])

  // Load comments
  useEffect(() => {
    if (!alert?.id || !isOpen) return
    setCommentsLoading(true)
    alertsApi.getComments(alert.id)
      .then(r => { if (r.data?.success) setComments(r.data.data?.comments ?? []) })
      .catch(() => {})
      .finally(() => setCommentsLoading(false))
  }, [alert?.id, isOpen])

  // Load maintenance status
  useEffect(() => {
    if (!alert?.id || !isOpen) return
    apiFetch(`/api/v1/alerts/${alert.id}/maintenance`)
      .then(d => { setMaintenanceActive((d.data?.windows ?? []).some((w: any) => w.active)) })
      .catch(() => {})
  }, [alert?.id, isOpen])

  // ── Actions ───────────────────────────────────────────────────────────────
  const withLoading = useCallback(async (key: string, fn: () => Promise<void>) => {
    setActionLoading(p => ({ ...p, [key]: true }))
    try { await fn() } finally { setActionLoading(p => ({ ...p, [key]: false })) }
  }, [])

  const handleAcknowledge = () => withLoading('ack', async () => {
    await onAcknowledge?.(alert.id)
    toast.success('Alert acknowledged')
  })

  const handleResolve = () => withLoading('resolve', async () => {
    await onResolve?.(alert.id)
    toast.success('Alert resolved')
  })

  const handleAutoRemediate = () => withLoading('remediate', async () => {
    await apiFetch(`/api/v1/alerts/${alert.id}/auto-remediate`, { method: 'POST' })
    toast.success('Auto-remediation triggered')
  })

  const handleTriggerRCA = () => withLoading('rca', async () => {
    await alertsApi.triggerRCA(alert.id)
    toast.success('RCA triggered — results appear in Analysis tab')
  })

  const handleProcessAutonomous = () => withLoading('autonomous', async () => {
    await apiFetch(`/api/v1/alerts/${alert.id}/process-autonomous`, { method: 'POST' })
    toast.success('Autonomous processing triggered')
  })

  const handleMaintenance = async () => {
    if (maintenanceActive) {
      toast('To end maintenance, use the Maintenance window manager')
      return
    }
    await withLoading('maintenance', async () => {
      await apiFetch(`/api/v1/alerts/${alert.id}/maintenance/start`, {
        method: 'POST',
        body: JSON.stringify({ duration_minutes: 60, reason: 'Manual maintenance from alert panel' }),
      })
      setMaintenanceActive(true)
      toast.success('Maintenance window started (1h)')
    })
  }

  // ── Analysis loaders ─────────────────────────────────────────────────────
  const loadAnalysis = () => withLoading('analysis', async () => {
    const d = await apiFetch(`/api/v1/ai/analyze/${alert.id}`)
    setAnalysisData(d.data ?? d)
  })

  const loadPrediction = () => withLoading('prediction', async () => {
    const d = await apiFetch(`/api/v1/ai/predict/${alert.id}`)
    setPredictionData(d.data ?? d)
  })

  const loadRunbooks = () => withLoading('runbooks', async () => {
    const d = await apiFetch(`/api/v1/ai/runbooks/${alert.id}`)
    setRunbooksData(d.data?.runbooks ?? d.runbooks ?? [])
  })

  const loadCorrelation = () => withLoading('correlation', async () => {
    const d = await apiFetch(`/api/v1/correlation/alert/${alert.id}`)
    setCorrelationData(d.data ?? d)
  })

  const loadAutonomous = () => withLoading('autonomous_load', async () => {
    const d = await apiFetch(`/api/v1/alerts/${alert.id}/autonomous-analysis`)
    setAutonomousData(d.data ?? d)
  })

  const submitFeedback = async (correct: boolean) => {
    try {
      await apiFetch('/api/v1/correlation/ai/feedback', {
        method: 'POST',
        body: JSON.stringify({
          alert_id: alert.id,
          correlation_id: alert.correlation_id,
          is_correct: correct,
          operator_notes: correct ? 'Correctly correlated' : 'Incorrect correlation',
        }),
      })
      toast.success(correct ? 'Feedback submitted: ✓ Correct' : 'Feedback submitted: ✗ Incorrect')
    } catch { toast.error('Failed to submit feedback') }
  }

  const handleAICorrelate = () => withLoading('ai_correlate', async () => {
    const d = await apiFetch(`/api/v1/correlation/ai/alert/${alert.id}/correlate`, { method: 'POST' })
    setCorrelationData(d.data ?? d)
    toast.success('AI correlation complete')
  })

  const handleAddComment = async () => {
    if (!commentText.trim()) return
    try {
      await alertsApi.addComment(alert.id, { comment: commentText.trim() })
      setCommentText('')
      const r = await alertsApi.getComments(alert.id)
      if (r.data?.success) setComments(r.data.data?.comments ?? [])
      toast.success('Comment added')
    } catch { toast.error('Failed to add comment') }
  }

  const handleDeleteComment = async (cid: string) => {
    try {
      await alertsApi.deleteComment(alert.id, cid)
      setComments(p => p.filter(x => x.id !== cid))
      toast.success('Comment deleted')
    } catch { toast.error('Failed to delete comment') }
  }

  const handleCreateTicket = async (url: string, label: string) => {
    try {
      const d = await apiFetch(url, {
        method: 'POST',
        body: JSON.stringify({ alert_id: alert.id, title: alert.title }),
      })
      toast.success(`${label}: ${d.key || d.number || '✓'}`)
    } catch { toast.error(`${label} integration not configured`) }
  }

  // ── Extract metadata ─────────────────────────────────────────────────────
  const m  = alert?.metadata ?? {}
  const l  = alert?.labels   ?? {}

  const dtProblemUrl      = m.dynatrace_problem_url || m.problemUrl || m.problemurl
  const dtProblemId       = (l.problem_id || m.problem_id || '').replace(/^P-/, '')
  const dtState           = m.dynatrace_state || m.state
  const dtImpact          = l.impact || m.dynatrace_impact || m.impact_level
  const mgmtZone          = l.management_zone || m.management_zone
  const dtTags            = m.dynatrace_tags as string[] | undefined
  const dtEntityTags      = m.dynatrace_entity_tags as string[] | undefined
  const dtCustomProps     = m.dynatrace_custom_properties as Record<string, string> | undefined
  const impactedEntities  = m.dynatrace_impacted_entities as any[] | undefined
  const rootCauseEntity   = l.root_cause_entity || m.dynatrace_root_cause
  const rootCauseEntityId = l.root_cause_entity_id
  const rootCauseType     = l.root_cause_entity_type
  const entityType  = l.entity_type  || m.entity_type
  const entityId    = l.entity_id    || m.entity_id
  const hostname    = alert.hostname || l.hostname || l['host.name'] || l['k8s.node.name'] || m.entity_name || m.hostname
  const hostIP      = alert.ip_address || l.ip || m.ip_address
  const workload    = alert.workload   || l['k8s.workload.name'] || l.workload
  const workloadKind = l['k8s.workload.kind']
  const namespace   = alert.namespace  || l['k8s.namespace.name'] || l.namespace
  const cluster     = alert.cluster    || l['k8s.cluster.name']   || l.cluster
  const node        = alert.node       || l['k8s.node.name']      || l.node
  const topologyPath = l.topology_path || m.topology_path
  const aiRootCause = m.ai_root_cause  || m.llm_root_cause
  const isDynatrace = alert.source === 'dynatrace' || !!dtProblemUrl
  const slaBreach   = alert.sla_met_response_time === false || alert.sla_met_resolution_time === false

  const mlInsights = useMemo(() => {
    if (!alert) return null
    try {
      const engine     = new MLCorrelationEngine([alert])
      const anomalies  = engine.detectAnomalies([alert])
      const anomaly    = anomalies.find(x => x.alert.id === alert.id)
      const rootCauses = engine.bayesianRootCauseAnalysis([alert])
      return { anomaly, rootCauses: rootCauses.slice(0, 4) }
    } catch { return null }
  }, [alert])

  if (!alert) return null

  const sev      = SEV[alert.severity]  || { color: '#fff', bg: c.gray, text: alert.severity?.toUpperCase() }
  const status   = STATUS[alert.status] || { color: c.gray, label: alert.status }
  const sevColor = sev.bg
  const duration = fmtDuration(alert.first_seen_at || alert.created_at, alert.resolved_at || undefined)

  const tabs = [
    { id: 'overview'       as Tab, label: 'Overview' },
    { id: 'infrastructure' as Tab, label: 'Infrastructure' },
    { id: 'analysis'       as Tab, label: 'AI Analysis' },
    { id: 'activity'       as Tab, label: `Activity${comments.length > 0 ? ` · ${comments.length}` : ''}` },
  ]

  // ── Render ───────────────────────────────────────────────────────────────
  return (
    <AnimatePresence>
      {isOpen && (
        <>
          <motion.div
            initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}
            transition={{ duration: 0.2 }}
            onClick={onClose}
            style={{
              position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.38)',
              zIndex: 9998, backdropFilter: 'blur(8px)', WebkitBackdropFilter: 'blur(8px)',
            }}
          />

          <motion.div
            initial={{ x: '100%' }}
            animate={{ x: 0 }}
            exit={{ x: '100%' }}
            transition={{ type: 'spring', damping: 30, stiffness: 370 }}
            style={{
              position: 'fixed', right: 0, top: 64, height: 'calc(100vh - 64px)',
              width: '100%', maxWidth: 600,
              background: c.bg, borderLeft: `0.5px solid ${c.sep}`,
              boxShadow: '-16px 0 64px rgba(0,0,0,0.16)',
              zIndex: 9999, display: 'flex', flexDirection: 'column', overflow: 'hidden',
            }}
          >
            {/* ── HEADER ──────────────────────────────────────────────────── */}
            <div style={{ flexShrink: 0, background: c.bg, borderBottom: `0.5px solid ${c.sep}` }}>
              {/* Severity gradient stripe */}
              <div style={{ height: 2, background: `linear-gradient(90deg, ${sevColor}ee, ${sevColor}00)` }} />

              <div style={{ padding: '12px 18px 0', borderLeft: `4px solid ${sevColor}` }}>
                {/* Badge row */}
                <div style={{ display: 'flex', alignItems: 'center', gap: 5, marginBottom: 7, flexWrap: 'wrap' }}>
                  <Chip label={sev.text}    color={sev.color}   bg={sev.bg} />
                  <Chip label={status.label} color={status.color} bg={`${status.color}18`} />
                  <Chip label={alert.source} color={c.tert}      bg={c.fill2} />
                  {dtProblemId && <Chip label={`P-${dtProblemId}`} color={c.orange} bg="rgba(255,149,0,0.12)" />}
                  {isDynatrace && dtImpact && (
                    <Chip
                      label={dtImpact.replace('_', ' ')}
                      color={['FULL_SERVICE','APPLICATION'].includes(dtImpact) ? c.red : c.orange}
                      bg={['FULL_SERVICE','APPLICATION'].includes(dtImpact) ? 'rgba(255,59,48,0.12)' : 'rgba(255,149,0,0.12)'}
                    />
                  )}
                  {alert.is_correlated && <Chip label="CORRELATED" color={c.purple} bg="rgba(175,82,222,0.12)" />}
                  {maintenanceActive && <Chip label="MAINTENANCE" color={c.teal} bg="rgba(48,176,199,0.12)" />}
                  {slaBreach && <Chip label="SLA BREACH" color={c.red} bg="rgba(255,59,48,0.12)" />}
                  <div style={{ flex: 1 }} />
                  <button
                    onClick={onClose}
                    style={{
                      width: 26, height: 26, borderRadius: c.r.sm, background: c.fill,
                      border: `0.5px solid ${c.sep}`, cursor: 'pointer',
                      display: 'flex', alignItems: 'center', justifyContent: 'center',
                    }}
                    onMouseEnter={(e) => { e.currentTarget.style.background = c.red }}
                    onMouseLeave={(e) => { e.currentTarget.style.background = c.fill }}
                  >
                    <X style={{ width: 13, height: 13, color: c.tert }} />
                  </button>
                </div>

                {/* Title */}
                <h2 style={{ fontSize: 14.5, fontWeight: 700, color: c.label, margin: '0 0 4px', lineHeight: 1.35, letterSpacing: '-0.2px' }}>
                  {alert.title}
                </h2>

                {/* Meta row */}
                <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 11, flexWrap: 'wrap' }}>
                  <button
                    onClick={() => { navigator.clipboard.writeText(alert.id); toast.success('ID copied') }}
                    style={{ background: 'none', border: 'none', cursor: 'pointer', padding: 0, display: 'flex', alignItems: 'center', gap: 3 }}
                  >
                    <span style={{ fontSize: 10, color: c.tert, fontFamily: 'ui-monospace,monospace' }}>{alert.id.slice(0, 8)}…</span>
                    <Copy style={{ width: 8, height: 8, color: c.tert }} />
                  </button>
                  <span style={{ fontSize: 10, color: c.sep }}>|</span>
                  <span style={{ fontSize: 10, color: c.tert }}>{new Date(alert.created_at).toLocaleString()}</span>
                  <span style={{ fontSize: 10, color: c.sep }}>|</span>
                  <span style={{ fontSize: 10, color: sevColor, fontWeight: 700 }}>{duration}</span>
                  {alert.count && alert.count > 1 && (
                    <>
                      <span style={{ fontSize: 10, color: c.sep }}>|</span>
                      <span style={{ fontSize: 10, color: c.orange, fontWeight: 600 }}>{alert.count}× occurrences</span>
                    </>
                  )}
                </div>
              </div>

              {/* Tab bar */}
              <div style={{ display: 'flex', padding: '0 18px', borderTop: `0.5px solid ${c.sep}` }}>
                {tabs.map(t => (
                  <button
                    key={t.id}
                    onClick={() => setTab(t.id)}
                    style={{
                      padding: '8px 13px', background: 'none', border: 'none', cursor: 'pointer',
                      fontSize: 12, fontWeight: tab === t.id ? 600 : 400,
                      color: tab === t.id ? c.blue : c.tert,
                      borderBottom: `2px solid ${tab === t.id ? c.blue : 'transparent'}`,
                      marginBottom: -0.5, transition: 'color 0.12s', whiteSpace: 'nowrap',
                    }}
                  >{t.label}</button>
                ))}
              </div>
            </div>

            {/* ── TAB CONTENT ─────────────────────────────────────────────── */}
            <div style={{ flex: 1, overflowY: 'auto' }}>
              <AnimatePresence mode="wait">
                <motion.div
                  key={tab}
                  initial={{ opacity: 0, y: 5 }}
                  animate={{ opacity: 1, y: 0 }}
                  exit={{ opacity: 0, y: -5 }}
                  transition={{ duration: 0.13 }}
                  style={{ padding: '14px 18px 32px' }}
                >

                  {/* ─────────────────── OVERVIEW ───────────────────────── */}
                  {tab === 'overview' && (
                    <OverviewTab
                      alert={alert} duration={duration} sevColor={sevColor}
                      dtProblemUrl={dtProblemUrl} dtProblemId={dtProblemId} mgmtZone={mgmtZone}
                      cluster={cluster} namespace={namespace}
                      maintenanceActive={maintenanceActive}
                      actionLoading={actionLoading}
                      onAcknowledge={handleAcknowledge}
                      onResolve={handleResolve}
                      onAssign={() => onAssign?.(alert.id)}
                      onTriggerRCA={handleTriggerRCA}
                      onAutoRemediate={handleAutoRemediate}
                      onProcessAutonomous={handleProcessAutonomous}
                      onMaintenance={handleMaintenance}
                    />
                  )}

                  {/* ─────────────────── INFRASTRUCTURE ─────────────────── */}
                  {tab === 'infrastructure' && (
                    <InfraTab
                      alert={alert}
                      workload={workload} workloadKind={workloadKind}
                      namespace={namespace} cluster={cluster}
                      node={node} hostname={hostname} hostIP={hostIP}
                      entityType={entityType} entityId={entityId}
                      topologyPath={topologyPath}
                      isDynatrace={isDynatrace}
                      dtProblemUrl={dtProblemUrl} dtProblemId={dtProblemId}
                      dtState={dtState} dtImpact={dtImpact}
                      mgmtZone={mgmtZone}
                      rootCauseEntity={rootCauseEntity}
                      rootCauseEntityType={rootCauseType}
                      rootCauseEntityId={rootCauseEntityId}
                      impactedEntities={impactedEntities}
                      dtTags={dtTags} dtEntityTags={dtEntityTags}
                      dtCustomProps={dtCustomProps}
                      labels={l} tags={alert.tags}
                    />
                  )}

                  {/* ─────────────────── AI ANALYSIS ─────────────────────── */}
                  {tab === 'analysis' && (
                    <AnalysisTab
                      alert={alert}
                      aiRootCause={aiRootCause}
                      mlInsights={mlInsights}
                      allAlerts={allAlerts}
                      analysisData={analysisData}
                      predictionData={predictionData}
                      runbooksData={runbooksData}
                      correlationData={correlationData}
                      actionLoading={actionLoading}
                      onLoadAnalysis={loadAnalysis}
                      onLoadPrediction={loadPrediction}
                      onLoadRunbooks={loadRunbooks}
                      onLoadCorrelation={loadCorrelation}
                      onAICorrelate={handleAICorrelate}
                      onTriggerRCA={handleTriggerRCA}
                      onFeedback={submitFeedback}
                      onViewSimilar={onViewSimilar}
                    />
                  )}

                  {/* ─────────────────── ACTIVITY ────────────────────────── */}
                  {tab === 'activity' && (
                    <ActivityTab
                      alert={alert}
                      comments={comments}
                      commentsLoading={commentsLoading}
                      commentText={commentText}
                      setCommentText={setCommentText}
                      autonomousData={autonomousData}
                      actionLoading={actionLoading}
                      onAddComment={handleAddComment}
                      onDeleteComment={handleDeleteComment}
                      onLoadAutonomous={loadAutonomous}
                      onCreateTicket={handleCreateTicket}
                    />
                  )}

                </motion.div>
              </AnimatePresence>
            </div>

            {/* ── FOOTER ──────────────────────────────────────────────────── */}
            <div style={{
              flexShrink: 0, padding: '8px 18px', borderTop: `0.5px solid ${c.sep}`,
              background: c.bg, display: 'flex', gap: 6,
            }}>
              <button
                onClick={() => {
                  const blob = new Blob([JSON.stringify(alert, null, 2)], { type: 'application/json' })
                  const url = URL.createObjectURL(blob)
                  const el = document.createElement('a')
                  el.href = url; el.download = `alert-${alert.id.slice(0, 8)}.json`
                  el.click(); URL.revokeObjectURL(url)
                  toast.success('Exported')
                }}
                style={{
                  flex: 1, padding: '7px 10px', borderRadius: c.r.sm,
                  border: `0.5px solid ${c.sep}`, background: c.fill,
                  color: c.sec, fontSize: 11, cursor: 'pointer',
                  display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 5,
                }}
              >
                <Download style={{ width: 11, height: 11 }} /> Export JSON
              </button>
              <button
                onClick={() => { navigator.clipboard.writeText(JSON.stringify(alert, null, 2)); toast.success('Copied') }}
                style={{
                  flex: 1, padding: '7px 10px', borderRadius: c.r.sm,
                  border: `0.5px solid ${c.sep}`, background: c.fill,
                  color: c.sec, fontSize: 11, cursor: 'pointer',
                  display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 5,
                }}
              >
                <Copy style={{ width: 11, height: 11 }} /> Copy Alert
              </button>
              {alert.source_url && (
                <a
                  href={alert.source_url} target="_blank" rel="noopener noreferrer"
                  style={{
                    flex: 1, padding: '7px 10px', borderRadius: c.r.sm,
                    border: `0.5px solid ${c.sep}`, background: c.fill,
                    color: c.sec, fontSize: 11, cursor: 'pointer',
                    display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 5,
                    textDecoration: 'none',
                  }}
                >
                  <ExternalLink style={{ width: 11, height: 11 }} /> Source
                </a>
              )}
            </div>
          </motion.div>
        </>
      )}
    </AnimatePresence>
  )
}

// ═══════════════════════════════════════════════════════════════════════════════
// OVERVIEW TAB
// ═══════════════════════════════════════════════════════════════════════════════
function OverviewTab({ alert, duration, sevColor, dtProblemUrl, dtProblemId, mgmtZone, cluster, namespace, maintenanceActive, actionLoading, onAcknowledge, onResolve, onAssign, onTriggerRCA, onAutoRemediate, onProcessAutonomous, onMaintenance }: any) {
  const canAck  = alert.status === 'open'
  const canRes  = !['resolved', 'closed'].includes(alert.status)
  const corrScore = alert.correlation_score ? Math.round(alert.correlation_score * 100) : null
  const slaBreach = alert.sla_met_response_time === false || alert.sla_met_resolution_time === false

  return (
    <>
      {/* Stat mini-cards */}
      <div style={{ display: 'flex', gap: 7, marginBottom: 14, flexWrap: 'wrap' }}>
        <StatCard label="Duration" value={duration} color={sevColor} icon={Clock} />
        {(alert.count ?? 0) > 1 && <StatCard label="Occurrences" value={`${alert.count}×`} color={c.orange} icon={Bell} />}
        {(cluster || namespace) && (
          <StatCard label={cluster ? 'Cluster' : 'Namespace'} value={(cluster || namespace || '').split('-').pop() || cluster || namespace} color={c.teal} icon={Server} />
        )}
        {corrScore !== null && <StatCard label="Corr. Score" value={`${corrScore}%`} color={c.purple} icon={GitMerge} />}
        {mgmtZone && <StatCard label="Mgmt Zone" value={mgmtZone} color={c.indigo} icon={Shield} />}
        {alert.rca_confidence && (
          <StatCard label="RCA Conf." value={`${Math.round(alert.rca_confidence * 100)}%`} color={c.green} icon={Brain} />
        )}
      </div>

      {/* SLA breach warning */}
      {slaBreach && (
        <div style={{
          padding: '8px 12px', borderRadius: c.r.md, marginBottom: 12,
          background: 'rgba(255,59,48,0.08)', border: `0.5px solid rgba(255,59,48,0.25)`,
          display: 'flex', alignItems: 'center', gap: 8,
        }}>
          <AlertTriangle style={{ width: 14, height: 14, color: c.red, flexShrink: 0 }} />
          <div>
            <div style={{ fontSize: 12, fontWeight: 700, color: c.red }}>SLA Breach</div>
            <div style={{ fontSize: 11, color: c.sec }}>
              {alert.sla_met_response_time === false && 'Response time exceeded. '}
              {alert.sla_met_resolution_time === false && 'Resolution time exceeded.'}
            </div>
          </div>
        </div>
      )}

      {/* Description */}
      {alert.description && (
        <div style={{
          padding: '10px 13px', borderRadius: c.r.md, marginBottom: 14,
          background: c.fill, border: `0.5px solid ${c.sep}`,
        }}>
          <p style={{ fontSize: 13, color: c.label, lineHeight: 1.65, margin: 0 }}>{alert.description}</p>
        </div>
      )}

      {/* AI classification / info */}
      {(alert.ai_classification || alert.info) && (
        <div style={{
          padding: '8px 12px', borderRadius: c.r.md, marginBottom: 12,
          background: 'rgba(175,82,222,0.06)', border: `0.5px solid rgba(175,82,222,0.2)`,
          display: 'flex', gap: 8, alignItems: 'flex-start',
        }}>
          <Brain style={{ width: 13, height: 13, color: c.purple, marginTop: 1, flexShrink: 0 }} />
          <div>
            {alert.ai_classification && (
              <div style={{ fontSize: 11, fontWeight: 600, color: c.purple, marginBottom: 2 }}>
                AI: {alert.ai_classification}
                {alert.ai_confidence && ` (${Math.round(alert.ai_confidence * 100)}% confidence)`}
              </div>
            )}
            {alert.info && <div style={{ fontSize: 12, color: c.sec }}>{alert.info}</div>}
          </div>
        </div>
      )}

      {/* Action grid */}
      <SectionHead label="Actions" accent={c.blue} />
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginBottom: 8 }}>
        <ActionBtn label="Acknowledge" icon={Check}   onClick={onAcknowledge}    color={c.orange}  disabled={!canAck}              loading={actionLoading.ack} />
        <ActionBtn label="Resolve"     icon={CheckCheck} onClick={onResolve}     color={c.green}   disabled={!canRes}              loading={actionLoading.resolve} />
        <ActionBtn label={alert.assigned_to_name ? `Assigned: ${alert.assigned_to_name.split(' ')[0]}` : 'Assign'} icon={UserPlus} onClick={onAssign} color={c.blue}   loading={actionLoading.assign} />
        <ActionBtn label="Trigger RCA" icon={Brain}   onClick={onTriggerRCA}     color={c.indigo}                                  loading={actionLoading.rca} />
        <ActionBtn label="Auto-Remediate" icon={Wrench} onClick={onAutoRemediate} color={c.teal}                                   loading={actionLoading.remediate} />
        <ActionBtn label="AI Process"  icon={Zap}     onClick={onProcessAutonomous} color={c.purple}                              loading={actionLoading.autonomous} />
        <ActionBtn label={maintenanceActive ? 'In Maintenance' : 'Suppress 1h'} icon={maintenanceActive ? BellOff : Pause} onClick={onMaintenance} color={maintenanceActive ? c.teal : c.gray} loading={actionLoading.maintenance} />
      </div>

      {/* External link */}
      {dtProblemUrl && (
        <a
          href={dtProblemUrl} target="_blank" rel="noopener noreferrer"
          style={{
            display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 6,
            padding: '9px', borderRadius: c.r.md, marginTop: 8,
            background: 'rgba(255,149,0,0.08)', border: `0.5px solid rgba(255,149,0,0.3)`,
            color: c.orange, fontSize: 12, fontWeight: 600, textDecoration: 'none',
          }}
        >
          <ExternalLink style={{ width: 12, height: 12 }} />
          Open in Dynatrace {dtProblemId ? `· P-${dtProblemId}` : ''}
        </a>
      )}

      {/* Key details */}
      <SectionHead label="Details" />
      <Row label="Alert Source"  value={alert.source} />
      <Row label="Assigned To"  value={alert.assigned_to_name || alert.assigned_to} />
      <Row label="Acknowledged" value={alert.acknowledged_at ? `${new Date(alert.acknowledged_at).toLocaleString()} by ${alert.acknowledged_by_name || alert.acknowledged_by || '—'}` : null} />
      <Row label="Resolved"     value={alert.resolved_at ? `${new Date(alert.resolved_at).toLocaleString()} by ${alert.resolved_by_name || alert.resolved_by || '—'}` : null} />
      <Row label="Mgmt Zone"    value={mgmtZone} />
      <Row label="Resolution"   value={alert.resolution_type} accent={alert.resolution_type === 'auto' ? c.green : undefined} />
      {alert.fingerprint && (
        <Row label="Fingerprint" value={alert.fingerprint} mono
          onCopy={() => { navigator.clipboard.writeText(alert.fingerprint!); toast.success('Copied') }}
        />
      )}
    </>
  )
}

// ═══════════════════════════════════════════════════════════════════════════════
// INFRASTRUCTURE TAB
// ═══════════════════════════════════════════════════════════════════════════════
function InfraTab({ alert, workload, workloadKind, namespace, cluster, node, hostname, hostIP, entityType, entityId, topologyPath, isDynatrace, dtProblemUrl, dtProblemId, dtState, dtImpact, mgmtZone, rootCauseEntity, rootCauseEntityType, rootCauseEntityId, impactedEntities, dtTags, dtEntityTags, dtCustomProps, labels, tags }: any) {
  const hasK8s = workload || namespace || cluster || node

  const IMPACT_COLOR: Record<string, string> = {
    FULL_SERVICE: c.red, APPLICATION: c.orange, SERVICE: c.orange, INFRASTRUCTURE: c.yellow,
  }

  const excludedLabelKeys = new Set([
    'entity_type','entity_id','ip','hostname','namespace','cluster','workload','node',
    'management_zone','problem_id','impact','root_cause_entity','root_cause_entity_id',
    'root_cause_entity_type','host.name','k8s.namespace.name','k8s.cluster.name',
    'k8s.workload.name','k8s.workload.kind','k8s.node.name',
  ])

  const extraLabels = Object.entries(labels || {}).filter(([k]) => !excludedLabelKeys.has(k))

  return (
    <>
      {/* Kubernetes */}
      {hasK8s && (
        <>
          <SectionHead label="Kubernetes" accent={c.teal} />
          <div style={{
            padding: '10px 13px', borderRadius: c.r.md, marginBottom: 12,
            background: 'rgba(48,176,199,0.06)', border: `0.5px solid rgba(48,176,199,0.2)`,
          }}>
            {workload && (
              <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 5 }}>
                {workloadKind && (
                  <span style={{
                    fontSize: 9, fontWeight: 700, padding: '2px 6px', borderRadius: 3,
                    background: 'rgba(48,176,199,0.18)', color: c.teal, textTransform: 'uppercase', letterSpacing: '0.5px',
                  }}>{workloadKind}</span>
                )}
                <span style={{ fontSize: 13, fontWeight: 700, color: c.label, fontFamily: 'ui-monospace,monospace' }}>{workload}</span>
                <button onClick={() => { navigator.clipboard.writeText(workload); toast.success('Copied') }}
                  style={{ background: 'none', border: 'none', cursor: 'pointer', padding: 2 }}>
                  <Copy style={{ width: 9, height: 9, color: c.tert }} />
                </button>
              </div>
            )}
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 12, fontSize: 11 }}>
              {namespace && <span style={{ color: c.sec }}><span style={{ color: c.tert }}>ns</span>: <span style={{ color: c.teal, fontFamily: 'ui-monospace,monospace' }}>{namespace}</span></span>}
              {cluster   && <span style={{ color: c.sec }}><span style={{ color: c.tert }}>cluster</span>: <span style={{ color: c.teal, fontFamily: 'ui-monospace,monospace' }}>{cluster}</span></span>}
              {node      && <span style={{ color: c.sec }}><span style={{ color: c.tert }}>node</span>: <span style={{ color: c.teal, fontFamily: 'ui-monospace,monospace' }}>{node}</span></span>}
            </div>
          </div>
        </>
      )}

      {/* Entity */}
      {(entityType || entityId || hostname || hostIP) && (
        <>
          <SectionHead label="Entity & Host" accent={c.blue} />
          <Row label="Entity Type" value={entityType} />
          <Row label="Entity ID"   value={entityId}   mono onCopy={() => { navigator.clipboard.writeText(entityId); toast.success('Copied') }} />
          <Row label="Host / Node" value={hostname || node} mono onCopy={() => { navigator.clipboard.writeText(hostname || node); toast.success('Copied') }} />
          <Row label="IP Address"  value={hostIP}     mono onCopy={() => { navigator.clipboard.writeText(hostIP); toast.success('Copied') }} />
        </>
      )}

      {/* Topology path */}
      {topologyPath && (
        <>
          <SectionHead label="Topology Path" />
          <div style={{
            padding: '8px 10px', borderRadius: c.r.sm, marginBottom: 8,
            background: c.fill, border: `0.5px solid ${c.sep}`,
            fontSize: 11, fontFamily: 'ui-monospace,monospace', color: c.teal,
            lineHeight: 1.7, wordBreak: 'break-all',
          }}>{topologyPath}</div>
        </>
      )}

      {/* Dynatrace problem */}
      {isDynatrace && (
        <>
          <SectionHead label="Dynatrace Problem" accent={c.orange} />
          {dtProblemUrl && (
            <a href={dtProblemUrl} target="_blank" rel="noopener noreferrer"
              style={{
                display: 'flex', alignItems: 'center', gap: 6, padding: '8px 12px',
                borderRadius: c.r.sm, background: 'rgba(255,149,0,0.08)',
                border: `0.5px solid rgba(255,149,0,0.25)`, color: c.orange,
                fontSize: 12, fontWeight: 600, textDecoration: 'none', marginBottom: 10,
              }}>
              <ExternalLink style={{ width: 11, height: 11 }} />
              Open in Dynatrace {dtProblemId ? `· P-${dtProblemId}` : ''}
            </a>
          )}
          <Row label="Problem ID"    value={dtProblemId ? `P-${dtProblemId}` : null} mono onCopy={() => { navigator.clipboard.writeText(`P-${dtProblemId}`); toast.success('Copied') }} />
          <Row label="DT State"      value={dtState}  accent={dtState === 'OPEN' ? c.red : c.green} />
          <Row label="Impact"        value={dtImpact?.replace('_', ' ')} accent={IMPACT_COLOR[dtImpact] || c.gray} />
          <Row label="Management Zone" value={mgmtZone} />
          <Row label="Root Cause"    value={rootCauseEntity}   accent={c.orange} />
          <Row label="RC Entity Type" value={rootCauseEntityType} />
          <Row label="RC Entity ID"  value={rootCauseEntityId} mono onCopy={() => { navigator.clipboard.writeText(rootCauseEntityId); toast.success('Copied') }} />

          {/* Impacted entities */}
          {impactedEntities && impactedEntities.length > 0 && (
            <Collapsible title={`Impacted Entities (${impactedEntities.length})`} accent={c.orange} defaultOpen={impactedEntities.length <= 5}>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 4, paddingBottom: 8 }}>
                {impactedEntities.slice(0, 12).map((e: any, i: number) => (
                  <div key={i} style={{
                    padding: '5px 8px', borderRadius: c.r.sm,
                    background: c.fill, border: `0.5px solid ${c.sep}`,
                    fontSize: 11, display: 'flex', gap: 8, alignItems: 'center',
                  }}>
                    <span style={{ color: c.orange, fontWeight: 600, flex: 1 }}>
                      {typeof e === 'string' ? e.split('(')[0].trim() : e.name || e.entityId || JSON.stringify(e)}
                    </span>
                    {typeof e === 'object' && e.entityId && (
                      <span style={{ color: c.tert, fontSize: 10, fontFamily: 'ui-monospace,monospace' }}>{e.entityId}</span>
                    )}
                  </div>
                ))}
                {impactedEntities.length > 12 && (
                  <div style={{ fontSize: 11, color: c.tert }}>+ {impactedEntities.length - 12} more</div>
                )}
              </div>
            </Collapsible>
          )}

          {/* DT Tags */}
          {((dtTags?.length ?? 0) + (dtEntityTags?.length ?? 0)) > 0 && (
            <>
              <div style={{ fontSize: 9.5, color: c.tert, fontWeight: 700, textTransform: 'uppercase', letterSpacing: '0.7px', marginTop: 10, marginBottom: 6 }}>DT Tags</div>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4, marginBottom: 10 }}>
                {[...(dtTags || []), ...(dtEntityTags || [])].map((t: string, i: number) => (
                  <span key={i} style={{ fontSize: 10, padding: '2px 7px', borderRadius: 4, background: 'rgba(255,149,0,0.1)', color: c.orange }}>{t}</span>
                ))}
              </div>
            </>
          )}

          {/* Custom Properties */}
          {dtCustomProps && Object.keys(dtCustomProps).length > 0 && (
            <Collapsible title="Custom Properties" accent={c.orange} defaultOpen={false}>
              {Object.entries(dtCustomProps).map(([k, v]) => (
                <Row key={k} label={k} value={String(v)} />
              ))}
            </Collapsible>
          )}
        </>
      )}

      {/* Labels & Tags */}
      {((tags?.length ?? 0) > 0 || extraLabels.length > 0) && (
        <Collapsible title="Labels & Tags" defaultOpen={false}>
          {tags?.length > 0 && (
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4, marginBottom: 8 }}>
              {tags.map((t: string, i: number) => (
                <span key={i} style={{
                  fontSize: 10, padding: '2px 7px', borderRadius: 4,
                  background: c.fill2, color: c.sec, border: `0.5px solid ${c.sep}`,
                }}>{t}</span>
              ))}
            </div>
          )}
          {extraLabels.map(([k, v]) => (
            <Row key={k} label={k} value={v as string} />
          ))}
        </Collapsible>
      )}
    </>
  )
}

// ═══════════════════════════════════════════════════════════════════════════════
// AI ANALYSIS TAB
// ═══════════════════════════════════════════════════════════════════════════════
function AnalysisTab({ alert, aiRootCause, mlInsights, allAlerts, analysisData, predictionData, runbooksData, correlationData, actionLoading, onLoadAnalysis, onLoadPrediction, onLoadRunbooks, onLoadCorrelation, onAICorrelate, onTriggerRCA, onFeedback, onViewSimilar }: any) {
  const hasCorrelation = alert.correlation_id || alert.is_correlated || correlationData
  const blastRadius = alert.metadata?.blast_radius_score
  const linkedIncident = alert.linked_incident_id || alert.metadata?.linked_incident_id

  return (
    <>
      {/* Correlation */}
      <SectionHead label="Correlation" accent={c.purple} />
      <div style={{
        padding: '12px', borderRadius: c.r.md, marginBottom: 12,
        background: hasCorrelation ? 'rgba(175,82,222,0.06)' : c.fill,
        border: `0.5px solid ${hasCorrelation ? 'rgba(175,82,222,0.2)' : c.sep}`,
      }}>
        {alert.correlation_id && (
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
            <span style={{ fontSize: 11, color: c.tert }}>Correlation ID</span>
            <button
              onClick={() => { navigator.clipboard.writeText(alert.correlation_id); toast.success('Copied') }}
              style={{
                fontSize: 11, fontFamily: 'ui-monospace,monospace', color: c.purple,
                background: 'none', border: 'none', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 3,
              }}
            >
              {alert.correlation_id.slice(0, 20)}… <Copy style={{ width: 8, height: 8 }} />
            </button>
          </div>
        )}
        {alert.correlation_score != null && (
          <div style={{ marginBottom: 8 }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 4 }}>
              <span style={{ fontSize: 11, color: c.tert }}>Confidence</span>
            </div>
            <ConfBar value={Math.round(alert.correlation_score * 100)} color={c.purple} />
          </div>
        )}
        {blastRadius != null && (
          <div style={{ marginBottom: 8 }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 4 }}>
              <span style={{ fontSize: 11, color: c.tert }}>Blast Radius</span>
            </div>
            <ConfBar value={Math.round(Number(blastRadius) * 100)} color={Number(blastRadius) > 0.6 ? c.red : c.orange} />
          </div>
        )}
        {linkedIncident && (
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', paddingTop: 6, borderTop: `0.5px solid ${c.sep}`, marginTop: 4 }}>
            <span style={{ fontSize: 11, color: c.tert }}>Linked Incident</span>
            <span style={{ fontSize: 12, color: c.indigo, fontWeight: 600, fontFamily: 'ui-monospace,monospace' }}>{linkedIncident}</span>
          </div>
        )}
        {!hasCorrelation && (
          <div style={{ fontSize: 12, color: c.tert }}>No correlation data · run AI Correlate to detect</div>
        )}
      </div>

      {/* Correlation actions */}
      <div style={{ display: 'flex', gap: 6, marginBottom: 14 }}>
        <LoadBtn label="Load Correlation" onClick={onLoadCorrelation} loading={actionLoading.correlation} />
        <button
          onClick={onAICorrelate}
          disabled={actionLoading.ai_correlate}
          style={{
            fontSize: 11, fontWeight: 600, color: c.purple, background: `${c.purple}10`,
            border: `0.5px solid ${c.purple}40`, borderRadius: c.r.sm,
            padding: '5px 10px', cursor: actionLoading.ai_correlate ? 'default' : 'pointer',
            display: 'flex', alignItems: 'center', gap: 4,
          }}
        >
          {actionLoading.ai_correlate
            ? <Loader2 style={{ width: 10, height: 10, animation: 'spin 1s linear infinite' }} />
            : <Brain style={{ width: 10, height: 10 }} />
          }
          AI Correlate
        </button>
        {hasCorrelation && (
          <>
            <button onClick={() => onFeedback(true)}
              style={{ fontSize: 11, color: c.green, background: `${c.green}10`, border: `0.5px solid ${c.green}40`, borderRadius: c.r.sm, padding: '5px 8px', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 3 }}>
              <ThumbsUp style={{ width: 10, height: 10 }} /> Correct
            </button>
            <button onClick={() => onFeedback(false)}
              style={{ fontSize: 11, color: c.red, background: `${c.red}10`, border: `0.5px solid ${c.red}40`, borderRadius: c.r.sm, padding: '5px 8px', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 3 }}>
              <ThumbsDown style={{ width: 10, height: 10 }} /> Incorrect
            </button>
          </>
        )}
      </div>

      {/* Corr details from API */}
      {correlationData && (
        <div style={{ padding: 12, borderRadius: c.r.md, background: c.fill, border: `0.5px solid ${c.sep}`, marginBottom: 14 }}>
          {correlationData.reasoning && (
            <p style={{ fontSize: 12, color: c.label, lineHeight: 1.6, margin: 0 }}>{correlationData.reasoning}</p>
          )}
          {correlationData.strategy_scores && Object.entries(correlationData.strategy_scores as Record<string, number>).map(([k, v]) => (
            <div key={k} style={{ marginTop: 6 }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 3 }}>
                <span style={{ fontSize: 11, color: c.tert }}>{k}</span>
              </div>
              <ConfBar value={Math.round(v * 100)} color={c.blue} />
            </div>
          ))}
        </div>
      )}

      {/* AI Root Cause */}
      {aiRootCause && (
        <>
          <SectionHead label="AI Root Cause Analysis" accent={c.indigo} />
          <div style={{
            padding: '12px 14px', borderRadius: c.r.md, marginBottom: 14,
            background: 'rgba(88,86,214,0.06)', border: `0.5px solid rgba(88,86,214,0.2)`,
          }}>
            <div style={{ display: 'flex', gap: 8, alignItems: 'flex-start' }}>
              <Brain style={{ width: 14, height: 14, color: c.indigo, marginTop: 2, flexShrink: 0 }} />
              <p style={{ fontSize: 13, color: c.label, lineHeight: 1.7, margin: 0 }}>{aiRootCause}</p>
            </div>
          </div>
        </>
      )}

      {/* AI analyze result */}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 6 }}>
        <SectionHead label="Deep AI Analysis" accent={c.blue} />
        <div style={{ display: 'flex', gap: 6, alignItems: 'center', paddingBottom: 6 }}>
          <LoadBtn label="Analyze" onClick={onLoadAnalysis} loading={actionLoading.analysis} />
          <button onClick={onTriggerRCA} disabled={actionLoading.rca}
            style={{ fontSize: 11, color: c.indigo, background: `${c.indigo}10`, border: `0.5px solid ${c.indigo}40`, borderRadius: c.r.sm, padding: '5px 8px', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 3 }}>
            <Target style={{ width: 10, height: 10 }} /> Trigger RCA
          </button>
        </div>
      </div>
      {analysisData ? (
        <div style={{ padding: 12, borderRadius: c.r.md, marginBottom: 14, background: c.fill, border: `0.5px solid ${c.sep}` }}>
          {analysisData.summary && <p style={{ fontSize: 12, color: c.label, lineHeight: 1.65, margin: '0 0 8px' }}>{analysisData.summary}</p>}
          {analysisData.recommendations?.length > 0 && (
            <>
              <div style={{ fontSize: 10, fontWeight: 700, color: c.tert, textTransform: 'uppercase', letterSpacing: '0.7px', marginBottom: 6 }}>Recommendations</div>
              {analysisData.recommendations.map((r: string, i: number) => (
                <div key={i} style={{ display: 'flex', gap: 6, marginBottom: 5 }}>
                  <span style={{ color: c.blue, fontSize: 11, flexShrink: 0 }}>›</span>
                  <span style={{ fontSize: 12, color: c.sec }}>{r}</span>
                </div>
              ))}
            </>
          )}
          {analysisData.severity_score != null && (
            <div style={{ marginTop: 8 }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 4 }}>
                <span style={{ fontSize: 11, color: c.tert }}>Severity Score</span>
              </div>
              <ConfBar value={analysisData.severity_score} color={analysisData.severity_score > 70 ? c.red : analysisData.severity_score > 40 ? c.orange : c.green} />
            </div>
          )}
        </div>
      ) : (
        <div style={{ fontSize: 12, color: c.tert, padding: '8px 0', marginBottom: 14 }}>Click "Analyze" to run deep AI analysis on this alert.</div>
      )}

      {/* Escalation Prediction */}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 6 }}>
        <SectionHead label="Escalation Prediction" accent={c.red} />
        <div style={{ paddingBottom: 6 }}>
          <LoadBtn label="Predict" onClick={onLoadPrediction} loading={actionLoading.prediction} />
        </div>
      </div>
      {predictionData ? (
        <div style={{
          padding: 12, borderRadius: c.r.md, marginBottom: 14,
          background: predictionData.risk === 'HIGH' ? 'rgba(255,59,48,0.06)' : c.fill,
          border: `0.5px solid ${predictionData.risk === 'HIGH' ? 'rgba(255,59,48,0.2)' : c.sep}`,
        }}>
          {predictionData.risk && (
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
              <TrendingUp style={{ width: 13, height: 13, color: predictionData.risk === 'HIGH' ? c.red : c.orange }} />
              <span style={{ fontSize: 13, fontWeight: 700, color: predictionData.risk === 'HIGH' ? c.red : c.orange }}>
                Risk: {predictionData.risk}
              </span>
            </div>
          )}
          {predictionData.message && <p style={{ fontSize: 12, color: c.label, margin: 0, lineHeight: 1.6 }}>{predictionData.message}</p>}
          {predictionData.confidence != null && (
            <div style={{ marginTop: 8 }}>
              <ConfBar value={Math.round(predictionData.confidence * 100)} color={c.red} />
            </div>
          )}
        </div>
      ) : (
        <div style={{ fontSize: 12, color: c.tert, padding: '4px 0', marginBottom: 14 }}>Click "Predict" to assess escalation risk.</div>
      )}

      {/* Runbooks */}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 6 }}>
        <SectionHead label="Runbooks" accent={c.green} />
        <div style={{ paddingBottom: 6 }}>
          <LoadBtn label="Load Runbooks" onClick={onLoadRunbooks} loading={actionLoading.runbooks} />
        </div>
      </div>
      {runbooksData.length > 0 ? (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6, marginBottom: 14 }}>
          {runbooksData.map((rb: any, i: number) => (
            <div key={i} style={{
              padding: '10px 12px', borderRadius: c.r.md,
              background: 'rgba(52,199,89,0.06)', border: `0.5px solid rgba(52,199,89,0.2)`,
            }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4 }}>
                <BookOpen style={{ width: 11, height: 11, color: c.green }} />
                <span style={{ fontSize: 12, fontWeight: 600, color: c.label }}>{rb.title || rb.name}</span>
              </div>
              {rb.description && <p style={{ fontSize: 11, color: c.sec, margin: 0, lineHeight: 1.5 }}>{rb.description}</p>}
              {rb.steps?.length > 0 && (
                <div style={{ marginTop: 6 }}>
                  {rb.steps.slice(0, 3).map((s: string, si: number) => (
                    <div key={si} style={{ display: 'flex', gap: 6, marginTop: 3 }}>
                      <span style={{ fontSize: 10, color: c.green, fontWeight: 700, flexShrink: 0 }}>{si + 1}.</span>
                      <span style={{ fontSize: 11, color: c.sec }}>{s}</span>
                    </div>
                  ))}
                  {rb.steps.length > 3 && <div style={{ fontSize: 10, color: c.tert, marginTop: 3 }}>+ {rb.steps.length - 3} more steps</div>}
                </div>
              )}
              {rb.url && (
                <a href={rb.url} target="_blank" rel="noopener noreferrer"
                  style={{ fontSize: 11, color: c.blue, display: 'flex', alignItems: 'center', gap: 3, marginTop: 6, textDecoration: 'none' }}>
                  <ExternalLink style={{ width: 9, height: 9 }} /> View runbook
                </a>
              )}
            </div>
          ))}
        </div>
      ) : (
        <div style={{ fontSize: 12, color: c.tert, padding: '4px 0', marginBottom: 14 }}>Click "Load Runbooks" to fetch recommended remediation steps.</div>
      )}

      {/* ML Analysis */}
      {mlInsights && (mlInsights.anomaly || mlInsights.rootCauses?.length > 0) && (
        <>
          <SectionHead label="ML Signals" accent={c.orange} />
          {mlInsights.anomaly && (
            <div style={{ marginBottom: 10 }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 4 }}>
                <span style={{ fontSize: 11, color: c.tert }}>Anomaly Score</span>
                <span style={{ fontSize: 13, fontWeight: 700, color: mlInsights.anomaly.score > 70 ? c.red : mlInsights.anomaly.score > 40 ? c.orange : c.green }}>
                  {Math.round(mlInsights.anomaly.score)}/100
                </span>
              </div>
              <ConfBar value={mlInsights.anomaly.score} color={mlInsights.anomaly.score > 70 ? c.red : mlInsights.anomaly.score > 40 ? c.orange : c.green} />
              {mlInsights.anomaly.reasons?.length > 0 && (
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4, marginTop: 6 }}>
                  {mlInsights.anomaly.reasons.map((r: string, i: number) => (
                    <span key={i} style={{ fontSize: 9.5, padding: '2px 6px', borderRadius: 4, background: 'rgba(255,149,0,0.1)', color: c.orange }}>{r}</span>
                  ))}
                </div>
              )}
            </div>
          )}
          {mlInsights.rootCauses?.map((rc: any, i: number) => (
            <div key={i} style={{ marginBottom: 8 }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 3 }}>
                <span style={{ fontSize: 11, color: c.sec }}>{rc.cause}</span>
                <span style={{ fontSize: 11, fontWeight: 700, color: c.blue }}>{Math.round(rc.probability)}%</span>
              </div>
              <ConfBar value={rc.probability} color={c.blue} />
            </div>
          ))}
        </>
      )}

      {/* Similar Alerts */}
      {allAlerts.length > 1 && (
        <Collapsible title="Similar Alerts" accent={c.teal} defaultOpen={false}>
          <SimilarAlertsViewer alert={alert} allAlerts={allAlerts} onSelectAlert={onViewSimilar} />
        </Collapsible>
      )}
    </>
  )
}

// ═══════════════════════════════════════════════════════════════════════════════
// ACTIVITY TAB
// ═══════════════════════════════════════════════════════════════════════════════
function ActivityTab({ alert, comments, commentsLoading, commentText, setCommentText, autonomousData, actionLoading, onAddComment, onDeleteComment, onLoadAutonomous, onCreateTicket }: any) {
  const timelineEvents = [
    { ts: alert.first_seen_at || alert.created_at, label: 'Alert Received',   color: c.blue,   by: null },
    { ts: alert.created_at !== alert.first_seen_at ? alert.created_at : null, label: 'Alert Triggered', color: c.red, by: null },
    { ts: alert.acknowledged_at, label: 'Acknowledged', color: c.orange, by: alert.acknowledged_by_name || alert.acknowledged_by },
    { ts: alert.resolved_at,     label: 'Resolved',     color: c.green,  by: alert.resolved_by_name || alert.resolved_by },
  ].filter(e => e.ts)

  const slaBreach = alert.sla_met_response_time === false || alert.sla_met_resolution_time === false

  return (
    <>
      {/* Timeline */}
      <SectionHead label="Timeline" accent={c.blue} />
      <div style={{ position: 'relative', paddingLeft: 20, marginBottom: 16 }}>
        <div style={{
          position: 'absolute', left: 3, top: 6, bottom: 8, width: 1.5,
          background: `linear-gradient(${c.sep}, transparent)`, borderRadius: 1,
        }} />
        {timelineEvents.map((ev, i) => (
          <div key={i} style={{ display: 'flex', gap: 12, alignItems: 'flex-start', marginBottom: 14, position: 'relative' }}>
            <div style={{
              position: 'absolute', left: -20, top: 4,
              width: 8, height: 8, borderRadius: '50%',
              background: ev.color, border: `2px solid ${c.bg}`, zIndex: 1,
              boxShadow: `0 0 0 1px ${ev.color}`,
            }} />
            <div>
              <div style={{ fontSize: 12.5, fontWeight: 600, color: c.label }}>{ev.label}</div>
              <div style={{ fontSize: 10.5, color: c.tert, marginTop: 1 }}>
                {new Date(ev.ts!).toLocaleString()}
                {ev.by && <span style={{ color: c.sec }}> · by {ev.by}</span>}
              </div>
            </div>
          </div>
        ))}

        {/* Duration pill */}
        <div style={{
          display: 'inline-flex', alignItems: 'center', gap: 6,
          padding: '5px 10px', borderRadius: 20,
          background: c.fill, border: `0.5px solid ${c.sep}`,
          fontSize: 11, color: c.sec,
        }}>
          <Clock style={{ width: 10, height: 10, color: c.tert }} />
          <span style={{ fontWeight: 600, color: c.label }}>
            {(() => {
              const s = alert.first_seen_at || alert.created_at
              const e = alert.resolved_at
              return e
                ? `Resolved in ${fmtDuration(s, e)}`
                : `Active for ${fmtDuration(s)}`
            })()}
          </span>
        </div>
      </div>

      {/* Occurrence & Identity */}
      <SectionHead label="Occurrence & Identity" accent={c.gray} />
      {alert.first_seen_at && <Row label="First Seen" value={new Date(alert.first_seen_at).toLocaleString()} />}
      {alert.last_seen_at  && <Row label="Last Seen"  value={new Date(alert.last_seen_at).toLocaleString()} />}
      {(alert.count ?? 0) > 1 && <Row label="Count" value={`${alert.count} occurrences`} accent={c.orange} />}
      {alert.fingerprint && (
        <Row label="Fingerprint" value={alert.fingerprint} mono
          onCopy={() => { navigator.clipboard.writeText(alert.fingerprint!); toast.success('Copied') }} />
      )}
      {alert.linked_incident_id && <Row label="Incident" value={alert.linked_incident_id} mono accent={c.indigo} />}
      {slaBreach && (
        <div style={{
          marginTop: 8, padding: '7px 10px', borderRadius: c.r.sm,
          background: 'rgba(255,59,48,0.07)', border: `0.5px solid rgba(255,59,48,0.2)`,
          fontSize: 11, color: c.red, fontWeight: 600, display: 'flex', alignItems: 'center', gap: 6,
        }}>
          <AlertTriangle style={{ width: 11, height: 11 }} />
          SLA Breached
          {alert.sla_met_response_time === false && ' · Response time'}
          {alert.sla_met_resolution_time === false && ' · Resolution time'}
        </div>
      )}

      {/* Autonomous analysis */}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', margin: '14px 0 6px' }}>
        <SectionHead label="Autonomous AIOps" accent={c.purple} />
        <div style={{ paddingBottom: 6 }}>
          <LoadBtn label="Load Analysis" onClick={onLoadAutonomous} loading={actionLoading.autonomous_load} />
        </div>
      </div>
      {autonomousData ? (
        <div style={{
          padding: 12, borderRadius: c.r.md, marginBottom: 14,
          background: 'rgba(175,82,222,0.06)', border: `0.5px solid rgba(175,82,222,0.2)`,
        }}>
          {autonomousData.agent_processed && (
            <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 6 }}>
              <Zap style={{ width: 12, height: 12, color: c.purple }} />
              <span style={{ fontSize: 12, fontWeight: 600, color: c.purple }}>Processed by Autonomous Agent</span>
            </div>
          )}
          {autonomousData.confidence != null && (
            <div style={{ marginBottom: 8 }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 3 }}>
                <span style={{ fontSize: 11, color: c.tert }}>Agent Confidence</span>
              </div>
              <ConfBar value={Math.round(autonomousData.confidence * 100)} color={c.purple} />
            </div>
          )}
          {autonomousData.summary && <p style={{ fontSize: 12, color: c.label, margin: 0, lineHeight: 1.6 }}>{autonomousData.summary}</p>}
          {autonomousData.actions_taken?.length > 0 && (
            <div style={{ marginTop: 8 }}>
              <div style={{ fontSize: 10, color: c.tert, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.6px', marginBottom: 5 }}>Actions Taken</div>
              {autonomousData.actions_taken.map((a: string, i: number) => (
                <div key={i} style={{ display: 'flex', gap: 6, marginBottom: 4 }}>
                  <Check style={{ width: 10, height: 10, color: c.green, marginTop: 1.5, flexShrink: 0 }} />
                  <span style={{ fontSize: 11, color: c.sec }}>{a}</span>
                </div>
              ))}
            </div>
          )}
        </div>
      ) : (
        <div style={{ fontSize: 12, color: c.tert, marginBottom: 14 }}>Click "Load Analysis" to fetch autonomous processing results.</div>
      )}

      {/* Create Ticket */}
      <SectionHead label="Create Ticket" accent={c.indigo} />
      <div style={{ display: 'flex', gap: 6, marginBottom: 16 }}>
        {[
          { label: 'JIRA', url: '/api/v1/integrations/ticketing/jira/tickets', color: c.purple, icon: '📋' },
          { label: 'ServiceNow', url: '/api/v1/integrations/ticketing/servicenow/incidents', color: c.teal, icon: '🎫' },
        ].map(btn => (
          <button
            key={btn.label}
            onClick={() => onCreateTicket(btn.url, btn.label)}
            style={{
              flex: 1, padding: '9px 12px', borderRadius: c.r.md,
              background: `${btn.color}0d`, border: `0.5px solid ${btn.color}35`,
              color: btn.color, fontSize: 12, fontWeight: 600, cursor: 'pointer',
              display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 6,
            }}
            onMouseEnter={(e) => { e.currentTarget.style.background = `${btn.color}1c` }}
            onMouseLeave={(e) => { e.currentTarget.style.background = `${btn.color}0d` }}
          >
            <span>{btn.icon}</span> {btn.label}
          </button>
        ))}
      </div>

      {/* Comments */}
      <SectionHead label={`Comments${comments.length > 0 ? ` (${comments.length})` : ''}`} accent={c.blue} />
      <div style={{ marginBottom: 8 }}>
        {commentsLoading ? (
          <div style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '10px 0', color: c.tert, fontSize: 12 }}>
            <Loader2 style={{ width: 12, height: 12, animation: 'spin 1s linear infinite' }} /> Loading…
          </div>
        ) : comments.length === 0 ? (
          <div style={{ fontSize: 12, color: c.tert, padding: '8px 0', textAlign: 'center' }}>No comments yet — be the first to add context.</div>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginBottom: 10 }}>
            {comments.map((cm: any) => (
              <div key={cm.id} style={{
                padding: '9px 11px', borderRadius: c.r.md,
                background: c.fill, border: `0.5px solid ${c.sep}`,
              }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 5 }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                    <div style={{
                      width: 22, height: 22, borderRadius: '50%', background: c.blue,
                      color: '#fff', fontSize: 9, fontWeight: 700,
                      display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0,
                    }}>
                      {(cm.username || cm.user || 'U').slice(0, 2).toUpperCase()}
                    </div>
                    <span style={{ fontSize: 12, fontWeight: 600, color: c.blue }}>{cm.username || cm.user}</span>
                  </div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                    <span style={{ fontSize: 10, color: c.tert }}>{new Date(cm.created_at).toLocaleString()}</span>
                    <button
                      onClick={() => onDeleteComment(cm.id)}
                      style={{ background: 'none', border: 'none', cursor: 'pointer', padding: 2, color: c.tert, opacity: 0.5 }}
                      title="Delete"
                    >
                      <Trash2 style={{ width: 10, height: 10 }} />
                    </button>
                  </div>
                </div>
                <p style={{ fontSize: 12.5, color: c.label, margin: 0, lineHeight: 1.55 }}>{cm.comment}</p>
              </div>
            ))}
          </div>
        )}

        {/* Add comment */}
        <div style={{ display: 'flex', gap: 6 }}>
          <input
            type="text"
            value={commentText}
            onChange={(e) => setCommentText(e.target.value)}
            onKeyDown={(e) => { if (e.key === 'Enter' && !e.shiftKey) onAddComment() }}
            placeholder="Add context, notes, or findings…"
            style={{
              flex: 1, height: 34, borderRadius: c.r.md, border: `0.5px solid ${c.sep}`,
              background: c.fill, padding: '0 11px', fontSize: 12,
              color: c.label, outline: 'none',
            }}
            onFocus={(e) => { e.target.style.borderColor = c.blue; e.target.style.boxShadow = '0 0 0 2px rgba(0,122,255,0.12)' }}
            onBlur={(e) => { e.target.style.borderColor = c.sep; e.target.style.boxShadow = 'none' }}
          />
          <button
            onClick={onAddComment}
            disabled={!commentText.trim()}
            style={{
              width: 34, height: 34, borderRadius: c.r.md, border: 'none',
              background: commentText.trim() ? c.blue : c.fill,
              cursor: commentText.trim() ? 'pointer' : 'default',
              display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0,
              transition: 'background 0.12s',
            }}
          >
            <Send style={{ width: 13, height: 13, color: commentText.trim() ? '#fff' : c.tert }} />
          </button>
        </div>
      </div>
    </>
  )
}
