import React, { useState, useEffect, useRef, useCallback } from 'react'
import {
  Activity, Server, AlertTriangle, CheckCircle, RefreshCw,
  Network, Shield, TrendingUp, Loader2, GitBranch,
  Zap, HardDrive, Radio, Database, Search, BarChart3,
  GitCommit, Lock, ChevronDown, X, BookOpen, AlertCircle,
  TrendingDown, DollarSign,
} from 'lucide-react'

// ─── Design tokens ────────────────────────────────────────────────────────────
const c = {
  blue:    '#007AFF', green:   '#34C759', red:     '#FF3B30',
  orange:  '#FF9500', yellow:  '#FFCC00', purple:  '#AF52DE',
  teal:    '#5AC8FA', indigo:  '#5856D6', gray:    '#8E8E93',
  label:   'var(--color-text)',
  sub:     'var(--color-text-secondary)',
  tertiary:'var(--color-text-tertiary, #8E8E93)',
  sep:     'var(--color-separator, rgba(142,142,147,0.12))',
  fill:    'var(--color-fill, rgba(142,142,147,0.08))',
  bg:      'var(--color-background)',
  card:    'var(--color-card, rgba(255,255,255,0.8))',
}

// ─── Types ────────────────────────────────────────────────────────────────────
type Tab = 'overview' | 'clusters' | 'health' | 'changes' | 'storage' |
           'violations' | 'chaos' | 'forecasts' | 'investigations' | 'apm' |
           'playbooks' | 'risk' | 'correlation' | 'topology'

interface DataCache {
  health:         any[];
  changes:        any[];
  storage:        any[];
  violations:     any[];
  chaos:          any[];
  forecasts:      any[];
  investigations: any[];
  apm:            any[];
  fetchedAt:      number; // epoch ms
}

const EMPTY_CACHE: DataCache = {
  health: [], changes: [], storage: [], violations: [],
  chaos: [], forecasts: [], investigations: [], apm: [],
  fetchedAt: 0,
}

// ─── Auth ─────────────────────────────────────────────────────────────────────
const token = () => sessionStorage.getItem('access_token') || localStorage.getItem('access_token')
const authHdr = (): HeadersInit => token() ? { Authorization: `Bearer ${token()}` } : {}

async function dbGet(path: string, timeoutMs = 8000) {
  const ctrl = new AbortController()
  const t = setTimeout(() => ctrl.abort(), timeoutMs)
  try {
    const r = await fetch(`/api/v1/kubesense/db${path}`, { headers: authHdr(), signal: ctrl.signal })
    if (!r.ok) throw new Error(`HTTP ${r.status}`)
    return r.json()
  } finally { clearTimeout(t) }
}
async function ksGet(path: string, timeoutMs = 5000) {
  const ctrl = new AbortController()
  const t = setTimeout(() => ctrl.abort(), timeoutMs)
  try {
    const r = await fetch(`/api/v1/kubesense${path}`, { headers: authHdr(), signal: ctrl.signal })
    if (!r.ok) throw new Error(`HTTP ${r.status}`)
    return r.json()
  } finally { clearTimeout(t) }
}

// ─── Helpers ──────────────────────────────────────────────────────────────────
function ago(iso: string) {
  if (!iso) return ''
  const s = Math.floor((Date.now() - new Date(iso).getTime()) / 1000)
  if (s < 60) return `${s}s ago`
  if (s < 3600) return `${Math.floor(s / 60)}m ago`
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`
  return `${Math.floor(s / 86400)}d ago`
}
function sevColor(s: string) {
  return s === 'critical' ? c.red : s === 'high' ? c.orange : s === 'medium' ? c.yellow
       : s === 'info' ? c.blue : c.gray
}
function gradeColor(g: string) {
  return g === 'A' ? c.green : g === 'B' ? c.blue : g === 'C' ? c.orange : c.red
}

// ─── Shared sub-components ────────────────────────────────────────────────────
function EmptyState({ icon: Icon, title, sub }: { icon: any; title: string; sub?: string }) {
  return (
    <div style={{ textAlign: 'center', padding: '52px 0' }}>
      <Icon style={{ width: 34, height: 34, color: c.tertiary, margin: '0 auto 12px' }} />
      <div style={{ fontSize: 14, fontWeight: 600, color: c.sub, marginBottom: sub ? 6 : 0 }}>{title}</div>
      {sub && <div style={{ fontSize: 12, color: c.tertiary, maxWidth: 380, margin: '0 auto', lineHeight: 1.5 }}>{sub}</div>}
    </div>
  )
}
function Spinner() {
  return (
    <div style={{ textAlign: 'center', padding: 52 }}>
      <Loader2 style={{ width: 22, height: 22, color: c.blue, margin: '0 auto', animation: 'spin 1s linear infinite' }} />
    </div>
  )
}
function SevBadge({ s }: { s: string }) {
  return (
    <span style={{ fontSize: 10, padding: '2px 7px', borderRadius: 12,
      background: `${sevColor(s)}15`, color: sevColor(s), fontWeight: 600, textTransform: 'uppercase' as const }}>
      {s}
    </span>
  )
}
function CountBadge({ n, color }: { n: number; color: string }) {
  return (
    <span style={{ fontSize: 10, padding: '2px 6px', borderRadius: 10,
      background: `${color}18`, color, fontWeight: 700, marginLeft: 4 }}>
      {n}
    </span>
  )
}
function SearchInput({ value, onChange, placeholder }: { value: string; onChange: (v: string) => void; placeholder: string }) {
  return (
    <div style={{ position: 'relative', marginBottom: 14 }}>
      <Search style={{ position: 'absolute', left: 10, top: '50%', transform: 'translateY(-50%)', width: 13, height: 13, color: c.tertiary, pointerEvents: 'none' }} />
      <input value={value} onChange={e => onChange(e.target.value)} placeholder={placeholder}
        style={{ width: '100%', boxSizing: 'border-box' as const, padding: '8px 32px 8px 30px',
          borderRadius: 9, border: `0.5px solid ${c.sep}`, background: c.fill,
          fontSize: 13, color: c.label, outline: 'none' }} />
      {value && (
        <button onClick={() => onChange('')} style={{ position: 'absolute', right: 10, top: '50%', transform: 'translateY(-50%)', background: 'none', border: 'none', cursor: 'pointer', padding: 0 }}>
          <X style={{ width: 12, height: 12, color: c.tertiary }} />
        </button>
      )}
    </div>
  )
}

// ─── Tab: Overview ────────────────────────────────────────────────────────────
function OverviewTab({ data, clusters, clusterID }: { data: DataCache; clusters: any[]; clusterID: string }) {
  const [stats, setStats] = useState<any>(null)
  const [dbStats, setDbStats] = useState<any>(null)

  useEffect(() => {
    const ctrl = new AbortController()
    // AlertHub intelligence stats
    fetch('/api/v1/intelligence/stats', { headers: authHdr(), signal: ctrl.signal })
      .then(r => r.json()).then(d => setStats(d?.stats ?? d?.data?.stats ?? d)).catch(() => {})
    return () => ctrl.abort()
  }, [])

  useEffect(() => {
    // KubeSense DB stats — pre-aggregated counts, much faster than counting array lengths
    if (!clusterID) return
    dbGet(`/stats?cluster_id=${clusterID}`, 5000).then(d => setDbStats(d)).catch(() => {})
  }, [clusterID])

  const isLive = data.fetchedAt > 0 && (Date.now() - data.fetchedAt) < 2 * 60 * 1000

  // Use DB stats for accurate counts; fall back to in-memory array lengths for fast display
  const kpis = [
    { label: 'Health Events',     value: dbStats?.health_events_total ?? data.health.length,     color: c.orange, icon: Activity },
    { label: 'Active Changes',    value: dbStats?.changes_total ?? data.changes.length,           color: c.indigo, icon: GitBranch },
    { label: 'Storage Alerts',    value: dbStats?.storage_events_total ?? data.storage.length,   color: c.teal,   icon: HardDrive },
    { label: 'Violations',        value: dbStats?.violations_total ?? data.violations.length,    color: c.red,    icon: Shield },
    { label: 'Chaos Scores',      value: dbStats?.chaos_scores_total ?? data.chaos.length,       color: c.yellow, icon: Zap },
    { label: 'Investigations',    value: dbStats?.investigations_total ?? data.investigations.length, color: c.blue, icon: Search },
  ]

  const topHealth = data.health.slice(0, 5)
  const topChanges = data.changes.slice(0, 5)

  return (
    <div>
      {/* Live freshness badge */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 14 }}>
        <span style={{
          display: 'inline-flex', alignItems: 'center', gap: 5,
          fontSize: 11, fontWeight: 600, padding: '3px 10px', borderRadius: 20,
          background: isLive ? `${c.green}14` : `${c.orange}14`,
          color: isLive ? c.green : c.orange,
          border: `0.5px solid ${isLive ? c.green : c.orange}30`,
        }}>
          <span style={{ width: 6, height: 6, borderRadius: '50%', background: isLive ? c.green : c.orange, display: 'inline-block' }} />
          {isLive ? 'Live' : 'Stale'}
        </span>
        {data.fetchedAt > 0 && (
          <span style={{ fontSize: 11, color: c.tertiary }}>
            Last refreshed {ago(new Date(data.fetchedAt).toISOString())}
          </span>
        )}
      </div>

      {/* KPI grid */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 10, marginBottom: 20 }}>
        {kpis.map((k, i) => (
          <div key={i} style={{ padding: 14, background: `${k.color}08`, borderRadius: 10, border: `0.5px solid ${k.color}20` }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
              <k.icon style={{ width: 13, height: 13, color: k.color }} />
              <span style={{ fontSize: 11, color: c.sub, textTransform: 'uppercase' as const, letterSpacing: '0.3px' }}>{k.label}</span>
            </div>
            <div style={{ fontSize: 26, fontWeight: 700, color: k.color }}>{k.value}</div>
          </div>
        ))}
      </div>

      {/* Two-column recent activity */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16, marginBottom: 16 }}>
        {/* Recent Health */}
        <div style={{ background: c.fill, borderRadius: 12, padding: 14, border: `0.5px solid ${c.sep}` }}>
          <div style={{ fontSize: 12, fontWeight: 600, color: c.sub, marginBottom: 10, display: 'flex', alignItems: 'center', gap: 6 }}>
            <Activity style={{ width: 12, height: 12 }} /> Recent Health Events
          </div>
          {topHealth.length === 0 ? (
            <div style={{ fontSize: 12, color: c.tertiary }}>No recent events</div>
          ) : topHealth.map((e: any, i: number) => (
            <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '5px 0', borderTop: i > 0 ? `0.5px solid ${c.sep}` : 'none' }}>
              <div style={{ width: 5, height: 5, borderRadius: '50%', background: sevColor(e.severity), flexShrink: 0 }} />
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontSize: 12, color: c.label, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{e.event_type?.replace('health.', '').replace(/_/g, ' ')}</div>
                <div style={{ fontSize: 10, color: c.tertiary }}>{e.namespace}/{e.resource_name}</div>
              </div>
              <div style={{ fontSize: 10, color: c.tertiary, flexShrink: 0 }}>{ago(e.occurred_at)}</div>
            </div>
          ))}
        </div>

        {/* Recent Changes */}
        <div style={{ background: c.fill, borderRadius: 12, padding: 14, border: `0.5px solid ${c.sep}` }}>
          <div style={{ fontSize: 12, fontWeight: 600, color: c.sub, marginBottom: 10, display: 'flex', alignItems: 'center', gap: 6 }}>
            <GitCommit style={{ width: 12, height: 12 }} /> Recent Changes
          </div>
          {topChanges.length === 0 ? (
            <div style={{ fontSize: 12, color: c.tertiary }}>No recent changes</div>
          ) : topChanges.map((e: any, i: number) => (
            <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '5px 0', borderTop: i > 0 ? `0.5px solid ${c.sep}` : 'none' }}>
              <GitCommit style={{ width: 11, height: 11, color: c.indigo, flexShrink: 0 }} />
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontSize: 12, color: c.label, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{e.event_type?.replace('change.', '').replace(/\./g, ' → ')}</div>
                <div style={{ fontSize: 10, color: c.tertiary }}>{e.namespace}/{e.resource_name}</div>
              </div>
              <div style={{ fontSize: 10, color: c.tertiary, flexShrink: 0 }}>{ago(e.occurred_at)}</div>
            </div>
          ))}
        </div>
      </div>

      {/* Platform description */}
      <div style={{ padding: 14, background: `${c.blue}05`, borderRadius: 10, border: `0.5px solid ${c.blue}18`, fontSize: 12, color: c.sub, lineHeight: 1.7 }}>
        <strong style={{ color: c.label }}>KubeSense Intelligence Platform</strong>
        {clusterID ? ` · Cluster: ${clusterID}` : ' · No cluster connected'}.{' '}
        {clusters.length} cluster{clusters.length !== 1 ? 's' : ''} registered.
        Events flow via <strong style={{ color: c.label }}>agent → Kafka → collector → AlertHub</strong>.
        Chaos readiness, config violations, and capacity forecasts are computed continuously.
      </div>
    </div>
  )
}

// ─── Tab: Clusters ────────────────────────────────────────────────────────────
function ClustersTab({ clusters, coreOnline }: { clusters: any[]; coreOnline: boolean | null }) {
  if (!coreOnline) return (
    <EmptyState icon={Server} title="kubesense-core unreachable"
      sub="Cluster metadata requires kubesense-core running in the aileron-agent namespace. DB-backed tabs still show live data." />
  )
  if (clusters.length === 0) return (
    <EmptyState icon={Server} title="No clusters registered"
      sub="Deploy the KubeSense agent with --cluster-id=<name> pointing at the Strimzi Kafka cluster." />
  )
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
      {clusters.map((cl: any) => {
        const statusColor = cl.status === 'active' ? c.green : cl.status === 'stale' ? c.orange : c.red
        return (
          <div key={cl.id} style={{ padding: 16, background: c.fill, borderRadius: 12, border: `0.5px solid ${c.sep}` }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
              <div style={{ width: 10, height: 10, borderRadius: '50%', background: statusColor, flexShrink: 0 }} />
              <div style={{ flex: 1 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
                  <span style={{ fontWeight: 600, fontSize: 14, color: c.label }}>{cl.id}</span>
                  <span style={{ fontSize: 10, padding: '1px 7px', borderRadius: 10, background: `${statusColor}15`, color: statusColor, fontWeight: 600 }}>{cl.status}</span>
                </div>
                <div style={{ display: 'flex', gap: 16, fontSize: 12, color: c.sub, flexWrap: 'wrap' as const }}>
                  <span><strong style={{ color: c.label }}>{cl.node_count ?? '—'}</strong> nodes</span>
                  {cl.agent_version && <span>agent {cl.agent_version}</span>}
                  {cl.last_heartbeat && <span>heartbeat {ago(cl.last_heartbeat)}</span>}
                  {cl.first_seen && <span>first seen {ago(cl.first_seen)}</span>}
                </div>
              </div>
              {cl.last_agent_id && (
                <div style={{ fontSize: 11, color: c.tertiary, fontFamily: 'monospace' }}>{cl.last_agent_id.substring(0, 16)}…</div>
              )}
            </div>
          </div>
        )
      })}
    </div>
  )
}

// ─── Tab: Health Events ───────────────────────────────────────────────────────
function HealthTab({ events }: { events: any[] }) {
  const [q, setQ] = useState('')
  const [limit, setLimit] = useState(40)

  const filtered = events.filter(e =>
    !q || e.event_type?.includes(q.toLowerCase()) ||
    e.namespace?.includes(q.toLowerCase()) ||
    e.resource_name?.includes(q.toLowerCase()) ||
    e.resource_kind?.includes(q.toLowerCase())
  )
  const visible = filtered.slice(0, limit)

  if (events.length === 0) return (
    <EmptyState icon={Activity} title="No health events in the last 7 days"
      sub="Health events appear when pods crash, nodes go NotReady, or OOM kills occur." />
  )
  return (
    <div>
      <SearchInput value={q} onChange={setQ} placeholder="Filter by event type, namespace, resource…" />
      <div style={{ fontSize: 12, color: c.tertiary, marginBottom: 10 }}>
        Showing {visible.length} of {filtered.length} events
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
        {visible.map((e: any, i: number) => (
          <div key={i} style={{ padding: '9px 12px', background: c.fill, borderRadius: 9, border: `0.5px solid ${c.sep}`, display: 'flex', alignItems: 'center', gap: 10 }}>
            <div style={{ width: 5, height: 5, borderRadius: '50%', background: sevColor(e.severity), flexShrink: 0 }} />
            <div style={{ flex: 1, minWidth: 0 }}>
              <div style={{ fontSize: 13, color: c.label, fontWeight: 500 }}>
                {e.event_type?.replace('health.', '').replace(/_/g, ' ')}
              </div>
              <div style={{ fontSize: 11, color: c.sub, marginTop: 1 }}>
                {[e.resource_kind, e.namespace, e.resource_name].filter(Boolean).join(' / ')}
              </div>
            </div>
            <SevBadge s={e.severity} />
            <div style={{ fontSize: 11, color: c.tertiary, flexShrink: 0 }}>{ago(e.occurred_at)}</div>
          </div>
        ))}
      </div>
      {filtered.length > limit && (
        <button onClick={() => setLimit(l => l + 40)}
          style={{ display: 'block', width: '100%', marginTop: 12, padding: '9px 0', borderRadius: 9, border: `0.5px solid ${c.sep}`, background: c.fill, cursor: 'pointer', fontSize: 13, color: c.sub }}>
          Load more ({filtered.length - limit} remaining)
        </button>
      )}
    </div>
  )
}

// ─── Tab: Changes ─────────────────────────────────────────────────────────────
const changeIcon: Record<string, any> = {
  'change.deployment.rollout': GitBranch,
  'change.configmap.updated':  Database,
  'change.secret.rotated':     Lock,
  'change.rbac.updated':       Shield,
  'change.hpa.scaled':         TrendingUp,
}
const changeLabel: Record<string, string> = {
  'change.deployment.rollout': 'Deployment rollout',
  'change.configmap.updated':  'ConfigMap updated',
  'change.secret.rotated':     'Secret rotated',
  'change.rbac.updated':       'RBAC changed',
  'change.hpa.scaled':         'HPA scaled',
}

function ChangesTab({ events }: { events: any[] }) {
  const [q, setQ] = useState('')
  const [filter, setFilter] = useState('all')

  const types = Array.from(new Set(events.map(e => e.event_type))).sort()
  const filtered = events.filter(e =>
    (filter === 'all' || e.event_type === filter) &&
    (!q || e.namespace?.includes(q.toLowerCase()) || e.resource_name?.includes(q.toLowerCase()))
  )

  if (events.length === 0) return (
    <EmptyState icon={GitCommit} title="No change events in the last 7 days"
      sub="Change events appear when deployments roll out, ConfigMaps update, secrets rotate, RBAC rules change, or HPAs scale." />
  )
  return (
    <div>
      <div style={{ display: 'flex', gap: 6, marginBottom: 10, flexWrap: 'wrap' as const }}>
        {['all', ...types].map(t => (
          <button key={t} onClick={() => setFilter(t)} style={{ padding: '4px 11px', borderRadius: 8, border: 'none', cursor: 'pointer', fontSize: 12, fontWeight: 500,
            background: filter === t ? `${c.indigo}18` : c.fill, color: filter === t ? c.indigo : c.sub }}>
            {t === 'all' ? 'All' : (changeLabel[t] ?? t.replace('change.', '').replace(/\./g, ' '))}
            {t !== 'all' && <CountBadge n={events.filter(e => e.event_type === t).length} color={c.indigo} />}
          </button>
        ))}
      </div>
      <SearchInput value={q} onChange={setQ} placeholder="Filter by namespace or resource…" />
      <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
        {filtered.slice(0, 80).map((e: any, i: number) => {
          const Icon = changeIcon[e.event_type] ?? GitCommit
          return (
            <div key={i} style={{ padding: '10px 14px', background: `${c.indigo}04`, borderRadius: 10, border: `0.5px solid ${c.indigo}15`, display: 'flex', alignItems: 'center', gap: 12 }}>
              <Icon style={{ width: 14, height: 14, color: c.indigo, flexShrink: 0 }} />
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontSize: 13, fontWeight: 500, color: c.label }}>
                  {changeLabel[e.event_type] ?? e.event_type}
                </div>
                <div style={{ fontSize: 11, color: c.sub, marginTop: 2 }}>
                  {e.resource_kind} · {[e.namespace, e.resource_name].filter(Boolean).join('/')}
                </div>
              </div>
              <div style={{ fontSize: 11, color: c.tertiary, flexShrink: 0 }}>{ago(e.occurred_at)}</div>
            </div>
          )
        })}
        {filtered.length === 0 && <EmptyState icon={GitCommit} title="No matches" />}
      </div>
    </div>
  )
}

// ─── Tab: Storage ─────────────────────────────────────────────────────────────
function StorageTab({ events }: { events: any[] }) {
  if (events.length === 0) return (
    <EmptyState icon={HardDrive} title="No storage alerts in the last 7 days"
      sub="Storage events appear when PVCs go Pending/Lost or approach capacity thresholds." />
  )
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      {events.map((e: any, i: number) => (
        <div key={i} style={{ padding: '10px 14px', background: `${sevColor(e.severity)}05`, borderRadius: 10, border: `0.5px solid ${sevColor(e.severity)}25`, display: 'flex', alignItems: 'center', gap: 12 }}>
          <HardDrive style={{ width: 14, height: 14, color: sevColor(e.severity), flexShrink: 0 }} />
          <div style={{ flex: 1 }}>
            <div style={{ fontSize: 13, fontWeight: 500, color: c.label }}>
              {e.event_type?.replace('storage.', '').replace(/\./g, ' → ')}
            </div>
            <div style={{ fontSize: 11, color: c.sub, marginTop: 2 }}>
              {[e.namespace, e.resource_name].filter(Boolean).join('/')}
            </div>
          </div>
          <SevBadge s={e.severity} />
          <div style={{ fontSize: 11, color: c.tertiary, flexShrink: 0 }}>{ago(e.occurred_at)}</div>
        </div>
      ))}
    </div>
  )
}

// ─── Tab: Violations ──────────────────────────────────────────────────────────
function ViolationsTab({ violations }: { violations: any[] }) {
  const [q, setQ] = useState('')
  const [expandedKey, setExpandedKey] = useState<string | null>(null)

  // Deduplicate by rule_id + namespace + resource_name, keep latest occurrence
  const deduped = Object.values(
    violations.reduce((acc: Record<string, any>, v: any) => {
      const key = `${v.rule_id}/${v.namespace}/${v.resource_name}`
      if (!acc[key]) {
        acc[key] = { ...v, _count: 1, _first_seen: v.occurred_at, _key: key }
      } else {
        acc[key]._count++
        // Keep the most recent
        if (v.occurred_at > acc[key].occurred_at) {
          acc[key] = { ...v, _count: acc[key]._count, _first_seen: acc[key]._first_seen, _key: key }
        }
      }
      return acc
    }, {})
  ) as any[]

  const filtered = deduped.filter(v =>
    !q || v.rule_id?.toLowerCase().includes(q.toLowerCase()) ||
    v.namespace?.toLowerCase().includes(q.toLowerCase()) ||
    v.resource_name?.toLowerCase().includes(q.toLowerCase()) ||
    v.message?.toLowerCase().includes(q.toLowerCase())
  )

  // Sort: critical first, then high, then by count
  const sorted = filtered.sort((a, b) => {
    const sevOrder = { critical: 0, high: 1, medium: 2, low: 3, info: 4 }
    const da = (sevOrder as any)[a.severity] ?? 5
    const db2 = (sevOrder as any)[b.severity] ?? 5
    return da !== db2 ? da - db2 : b._count - a._count
  })

  if (violations.length === 0) return (
    <EmptyState icon={Shield} title="No configuration violations"
      sub="Violations appear when deployments are missing readiness probes, resource limits, have single replicas, or use :latest image tags." />
  )

  return (
    <div>
      <SearchInput value={q} onChange={setQ} placeholder="Filter by rule, namespace, resource, message…" />
      <div style={{ fontSize: 12, color: c.tertiary, marginBottom: 10 }}>
        {sorted.length} unique violation{sorted.length !== 1 ? 's' : ''}{violations.length > sorted.length ? ` (${violations.length} total occurrences deduped)` : ''}
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
        {sorted.map((v: any) => {
          const expanded = expandedKey === v._key
          return (
            <div key={v._key} style={{ background: c.fill, borderRadius: 10, border: `0.5px solid ${c.sep}`, overflow: 'hidden' }}>
              <div onClick={() => setExpandedKey(expanded ? null : v._key)}
                style={{ padding: '12px 14px', cursor: 'pointer', display: 'flex', alignItems: 'flex-start', gap: 10 }}>
                <div style={{ width: 3, borderRadius: 2, alignSelf: 'stretch', background: sevColor(v.severity), flexShrink: 0 }} />
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4, flexWrap: 'wrap' as const }}>
                    <span style={{ fontWeight: 600, fontSize: 13, color: c.label }}>{v.rule_id}</span>
                    <SevBadge s={v.severity} />
                    {v._count > 1 && (
                      <span style={{ fontSize: 10, padding: '2px 7px', borderRadius: 10, background: `${c.orange}15`, color: c.orange, fontWeight: 600 }}>×{v._count}</span>
                    )}
                    <span style={{ fontSize: 11, color: c.tertiary }}>{v.resource_kind}/{v.namespace}/{v.resource_name}</span>
                  </div>
                  <div style={{ fontSize: 12, color: c.sub, lineHeight: 1.5 }}>{v.message}</div>
                </div>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexShrink: 0 }}>
                  <div style={{ fontSize: 11, color: c.tertiary }}>{ago(v.occurred_at)}</div>
                  <ChevronDown style={{ width: 13, height: 13, color: c.tertiary, transform: expanded ? 'rotate(180deg)' : 'none', transition: 'transform 0.15s' }} />
                </div>
              </div>
              {expanded && v.remediation && (
                <div style={{ padding: '0 14px 12px 27px', borderTop: `0.5px solid ${c.sep}` }}>
                  <div style={{ fontSize: 12, color: c.blue, marginTop: 8 }}>
                    <strong>Fix: </strong>{v.remediation}
                  </div>
                  {v._count > 1 && (
                    <div style={{ fontSize: 11, color: c.tertiary, marginTop: 4 }}>
                      First detected {ago(v._first_seen)} · {v._count} occurrences (scanner runs every 5 min)
                    </div>
                  )}
                </div>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}

// ─── Tab: Chaos ───────────────────────────────────────────────────────────────
function ChaosTab({ scores }: { scores: any[] }) {
  if (scores.length === 0) return (
    <EmptyState icon={Zap} title="No chaos readiness scores"
      sub="Chaos scores appear every 5 minutes once the KubeSense agent is running. Scores reflect replica count, PDB coverage, and readiness probe status." />
  )
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      {scores.map((d: any, i: number) => {
        const scoreRaw = d.cluster_score ?? 0
        const score = parseFloat(scoreRaw.toFixed(1))
        const scoreInt = Math.round(score)
        const grade = d.grade ?? (scoreInt >= 85 ? 'A' : scoreInt >= 70 ? 'B' : scoreInt >= 55 ? 'C' : 'D')
        const scoreColor = scoreInt >= 85 ? c.green : scoreInt >= 70 ? c.blue : scoreInt >= 55 ? c.orange : c.red
        return (
          <div key={i} style={{ padding: 16, background: c.fill, borderRadius: 12, border: `0.5px solid ${c.sep}` }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 14, marginBottom: 10 }}>
              <div style={{ width: 52, height: 52, borderRadius: 14, background: `${scoreColor}14`, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', flexShrink: 0, border: `1.5px solid ${scoreColor}30` }}>
                <span style={{ fontSize: 20, fontWeight: 700, color: scoreColor, lineHeight: 1 }}>{grade}</span>
                <span style={{ fontSize: 9, color: c.sub, marginTop: 1 }}>{score.toFixed(1)}/100</span>
              </div>
              <div style={{ flex: 1 }}>
                <div style={{ fontWeight: 600, fontSize: 14, color: c.label, marginBottom: 3 }}>Cluster: {d.cluster_id}</div>
                <div style={{ fontSize: 12, color: c.sub }}>{d.summary}</div>
              </div>
              <div style={{ fontSize: 11, color: c.tertiary }}>{ago(d.timestamp)}</div>
            </div>
            <div style={{ height: 5, background: `${scoreColor}14`, borderRadius: 3, overflow: 'hidden', marginBottom: 8 }}>
              <div style={{ width: `${scoreInt}%`, height: '100%', background: scoreColor, borderRadius: 3 }} />
            </div>
            {(d.total_workloads ?? 0) > 0 && (
              <div style={{ display: 'flex', gap: 16, fontSize: 12, color: c.sub }}>
                <span><strong style={{ color: c.label }}>{d.total_workloads}</strong> workloads scored</span>
                {(d.high_risk_count ?? 0) > 0 && (
                  <span><strong style={{ color: c.red }}>{d.high_risk_count}</strong> high-risk</span>
                )}
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}

// ─── Tab: Forecasts ───────────────────────────────────────────────────────────
function ForecastsTab({ forecasts }: { forecasts: any[] }) {
  if (forecasts.length === 0) return (
    <EmptyState icon={TrendingUp} title="No capacity forecasts"
      sub="Forecasts appear when resources approach thresholds. Requires the kubesense-api service with configured data sources." />
  )
  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(260px, 1fr))', gap: 12 }}>
      {forecasts.map((f: any, i: number) => {
        const pct = Math.round((f.current_value ?? 0) * 100)
        const thr = Math.round((f.threshold ?? 0.85) * 100)
        const col = pct > 85 ? c.red : pct > 70 ? c.orange : c.green
        return (
          <div key={i} style={{ padding: 16, background: c.fill, borderRadius: 12, border: `0.5px solid ${c.sep}` }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 10 }}>
              <HardDrive style={{ width: 14, height: 14, color: col, flexShrink: 0 }} />
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontWeight: 600, fontSize: 13, color: c.label }}>{f.target?.replace(/_/g, ' ')}</div>
                <div style={{ fontSize: 11, color: c.sub }}>{f.namespace}/{f.resource_name}</div>
              </div>
            </div>
            <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 11, color: c.sub, marginBottom: 4 }}>
              <span>Current <strong style={{ color: c.label }}>{pct}%</strong></span>
              <span>Threshold <strong style={{ color: c.label }}>{thr}%</strong></span>
            </div>
            <div style={{ height: 5, background: `${col}18`, borderRadius: 3, marginBottom: 8, overflow: 'hidden' }}>
              <div style={{ width: `${pct}%`, height: '100%', background: col, borderRadius: 3 }} />
            </div>
            <div style={{ fontSize: 12, color: c.sub }}>
              <strong style={{ color: col }}>Breach: </strong>
              {f.predicted_breach ? new Date(f.predicted_breach).toLocaleString() : 'Not imminent'}
            </div>
            {f.trend_per_day > 0 && (
              <div style={{ fontSize: 11, color: c.tertiary, marginTop: 3 }}>
                +{(f.trend_per_day * 100).toFixed(1)}%/day · model conf {Math.round((f.model_confidence ?? 0) * 100)}%
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}

// ─── Tab: Investigations ──────────────────────────────────────────────────────
function InvestigationsTab({ results }: { results: any[] }) {
  if (results.length === 0) return (
    <EmptyState icon={Search} title="No KubeSense investigation results"
      sub="Investigations run automatically for critical/high incidents. Results appear here once completed." />
  )
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
      {results.map((r: any, i: number) => (
        <div key={i} style={{ padding: 16, background: c.fill, borderRadius: 12, border: `0.5px solid ${c.sep}` }}>
          <div style={{ display: 'flex', alignItems: 'flex-start', gap: 12 }}>
            <div style={{ width: 46, height: 46, borderRadius: 12, background: `${gradeColor(r.grade)}14`, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', flexShrink: 0, border: `1px solid ${gradeColor(r.grade)}30` }}>
              <span style={{ fontSize: 18, fontWeight: 700, color: gradeColor(r.grade), lineHeight: 1 }}>{r.grade ?? '?'}</span>
              <span style={{ fontSize: 9, color: c.sub, marginTop: 1 }}>{Math.round((r.confidence ?? 0) * 100)}%</span>
            </div>
            <div style={{ flex: 1, minWidth: 0 }}>
              <div style={{ display: 'flex', gap: 10, fontSize: 11, color: c.tertiary, marginBottom: 5 }}>
                <span>Incident {r.incident_id?.substring(0, 8)}…</span>
                {r.cluster_id && <span>· {r.cluster_id}</span>}
                {r.evidence_count === -1
                  ? <span style={{ color: c.orange }}>· pending</span>
                  : r.evidence_count > 0 && <span>· {r.evidence_count} evidence</span>
                }
              </div>
              <div style={{ fontSize: 13, color: c.label, lineHeight: 1.5, fontWeight: 500 }}>{r.root_cause}</div>
              {r.summary && r.summary !== r.root_cause && (
                <div style={{ fontSize: 12, color: c.sub, marginTop: 4, lineHeight: 1.5 }}>{r.summary}</div>
              )}
            </div>
            <div style={{ fontSize: 11, color: c.tertiary, flexShrink: 0 }}>{ago(r.completed_at)}</div>
          </div>
        </div>
      ))}
    </div>
  )
}

// ─── Tab: APM ─────────────────────────────────────────────────────────────────
function APMTab({ signals }: { signals: any[] }) {
  if (signals.length === 0) return (
    <EmptyState icon={BarChart3} title="No APM signals"
      sub="Golden signals (latency, traffic, errors, saturation) are published by kubesense-api every 60 seconds. Requires kubesense-api deployed with a configured metrics source." />
  )
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      {signals.map((s: any, i: number) => {
        const errorPct = Math.round((s.error_rate ?? 0) * 100)
        const satPct = Math.round((s.saturation ?? 0) * 100)
        const errorColor = errorPct > 5 ? c.red : errorPct > 1 ? c.orange : c.green
        const satColor = satPct > 80 ? c.red : satPct > 60 ? c.orange : c.green
        return (
          <div key={i} style={{ padding: '12px 14px', background: c.fill, borderRadius: 10, border: `0.5px solid ${c.sep}` }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
              <Activity style={{ width: 14, height: 14, color: c.blue, flexShrink: 0 }} />
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontSize: 13, fontWeight: 600, color: c.label, marginBottom: 5 }}>
                  {s.namespace}/{s.service_name}
                </div>
                <div style={{ display: 'flex', gap: 16, fontSize: 12, flexWrap: 'wrap' as const }}>
                  <span style={{ color: c.sub }}><strong style={{ color: c.label }}>{(s.request_rate ?? 0).toFixed(1)}</strong> req/s</span>
                  <span style={{ color: c.sub }}><strong style={{ color: errorColor }}>{errorPct}%</strong> errors</span>
                  <span style={{ color: c.sub }}><strong style={{ color: c.label }}>{(s.latency_p99_ms ?? 0).toFixed(0)}</strong>ms p99</span>
                  <span style={{ color: c.sub }}><strong style={{ color: satColor }}>{satPct}%</strong> saturation</span>
                </div>
              </div>
              <div style={{ fontSize: 11, color: c.tertiary, flexShrink: 0 }}>{ago(s.sampled_at)}</div>
            </div>
          </div>
        )
      })}
    </div>
  )
}

// ─── Tab: Topology ────────────────────────────────────────────────────────────
const KINDS = ['Pod', 'Deployment', 'Service', 'Node', 'StatefulSet', 'PersistentVolumeClaim']
const KIND_COLORS: Record<string, string> = {
  Pod: c.blue, Deployment: c.green, Service: c.orange,
  Node: c.purple, StatefulSet: c.indigo, PersistentVolumeClaim: c.red,
}

function TopologyTab({ clusterID, coreOnline }: { clusterID: string; coreOnline: boolean | null }) {
  const [nodes, setNodes] = useState<any[]>([])
  const [loading, setLoading] = useState(false)
  const [kind, setKind] = useState('Deployment')
  const [q, setQ] = useState('')

  useEffect(() => {
    if (!coreOnline || !clusterID) return
    setLoading(true)
    ksGet(`/topology?cluster=${clusterID}&kind=${kind}&limit=200`)
      .then(d => { setNodes(d?.nodes ?? []); setLoading(false) })
      .catch(() => setLoading(false))
  }, [clusterID, kind, coreOnline])

  if (!coreOnline) return (
    <EmptyState icon={Network} title="Topology requires kubesense-core"
      sub="Check that kubesense-core is running in the aileron-agent namespace." />
  )

  // Group nodes by namespace
  const filtered = nodes.filter(n => !q || n.name?.includes(q) || n.namespace?.includes(q))
  const byNamespace = filtered.reduce((acc: Record<string, any[]>, n) => {
    const ns = n.namespace || '(cluster-scoped)'
    if (!acc[ns]) acc[ns] = []
    acc[ns].push(n)
    return acc
  }, {})
  const namespaces = Object.keys(byNamespace).sort()
  const col = KIND_COLORS[kind] ?? c.blue

  return (
    <div>
      {/* Kind filter */}
      <div style={{ display: 'flex', gap: 6, marginBottom: 12, flexWrap: 'wrap' as const }}>
        {KINDS.map(k => (
          <button key={k} onClick={() => setKind(k)} style={{ padding: '4px 11px', borderRadius: 8, border: 'none', cursor: 'pointer', fontSize: 12, fontWeight: 500,
            background: kind === k ? `${KIND_COLORS[k] || c.blue}18` : c.fill,
            color: kind === k ? (KIND_COLORS[k] || c.blue) : c.sub }}>
            {k}
          </button>
        ))}
      </div>
      <SearchInput value={q} onChange={setQ} placeholder={`Filter ${kind}s by name or namespace…`} />

      {loading ? <Spinner /> : filtered.length === 0 ? (
        <EmptyState icon={Network} title={`No ${kind}s found`} />
      ) : (
        <div>
          <div style={{ fontSize: 12, color: c.tertiary, marginBottom: 10 }}>
            {filtered.length} {kind}{filtered.length !== 1 ? 's' : ''} across {namespaces.length} namespace{namespaces.length !== 1 ? 's' : ''}
          </div>
          {namespaces.map(ns => (
            <div key={ns} style={{ marginBottom: 16 }}>
              <div style={{ fontSize: 11, fontWeight: 600, color: c.sub, textTransform: 'uppercase' as const, letterSpacing: '0.4px', marginBottom: 6, display: 'flex', alignItems: 'center', gap: 6 }}>
                <div style={{ width: 3, height: 3, borderRadius: '50%', background: c.sub }} />
                {ns}
                <CountBadge n={byNamespace[ns].length} color={col} />
              </div>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
                {byNamespace[ns].map((n: any, i: number) => (
                  <div key={i} style={{ padding: '7px 12px', background: c.fill, borderRadius: 8, display: 'flex', alignItems: 'center', gap: 10 }}>
                    <div style={{ width: 5, height: 5, borderRadius: '50%', background: col, flexShrink: 0 }} />
                    <span style={{ fontSize: 13, color: c.label, fontWeight: 500, flex: 1 }}>{n.name}</span>
                    {n.status && <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 8, background: `${n.status === 'Running' ? c.green : c.orange}15`, color: n.status === 'Running' ? c.green : c.orange, fontWeight: 600 }}>{n.status}</span>}
                  </div>
                ))}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

// ─── Tab: Playbooks ───────────────────────────────────────────────────────────
function PlaybooksTab({ clusterID, apiOnline }: { clusterID: string; apiOnline: boolean | null }) {
  const [playbooks, setPlaybooks] = useState<any[]>([])
  const [loading, setLoading] = useState(true)
  const [expanded, setExpanded] = useState<string | null>(null)

  useEffect(() => {
    if (!clusterID) { setLoading(false); return }
    setLoading(true)
    ksGet(`/clusters/${clusterID}/playbooks`, 8000)
      .then(d => { setPlaybooks(d?.playbooks ?? []); setLoading(false) })
      .catch(() => setLoading(false))
  }, [clusterID])

  if (!apiOnline) return (
    <EmptyState icon={BookOpen} title="kubesense-api unreachable"
      sub="Playbooks require kubesense-api running in the aileron-agent namespace." />
  )
  if (loading) return <Spinner />
  if (playbooks.length === 0) return (
    <EmptyState icon={BookOpen} title="No playbooks generated yet"
      sub="Playbooks are auto-generated from resolved incidents. At least 2 resolved incidents with the same failure mode are required to generate a playbook." />
  )

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
      <div style={{ fontSize: 12, color: c.tertiary, marginBottom: 4 }}>
        {playbooks.length} playbook{playbooks.length !== 1 ? 's' : ''} · auto-generated from resolved incident patterns
      </div>
      {playbooks.map((pb: any, i: number) => {
        const key = pb.failure_mode ?? String(i)
        const exp = expanded === key
        const successPct = Math.round((pb.success_rate ?? 0) * 100)
        const successColor = successPct >= 80 ? c.green : successPct >= 50 ? c.orange : c.red
        return (
          <div key={key} style={{ background: c.fill, borderRadius: 12, border: `0.5px solid ${c.sep}`, overflow: 'hidden' }}>
            <div onClick={() => setExpanded(exp ? null : key)}
              style={{ padding: '14px 16px', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 12 }}>
              <BookOpen style={{ width: 14, height: 14, color: c.blue, flexShrink: 0 }} />
              <div style={{ flex: 1 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
                  <span style={{ fontWeight: 600, fontSize: 13, color: c.label }}>{pb.failure_mode}</span>
                  {pb.resource_kind && (
                    <span style={{ fontSize: 11, padding: '1px 6px', borderRadius: 8, background: `${c.blue}12`, color: c.blue }}>{pb.resource_kind}</span>
                  )}
                </div>
                <div style={{ display: 'flex', gap: 12, fontSize: 12, color: c.sub }}>
                  <span><strong style={{ color: successColor }}>{successPct}%</strong> success rate</span>
                  {pb.data_points > 0 && <span><strong style={{ color: c.label }}>{pb.data_points}</strong> incidents</span>}
                  {pb.steps?.length > 0 && <span><strong style={{ color: c.label }}>{pb.steps.length}</strong> steps</span>}
                </div>
              </div>
              <ChevronDown style={{ width: 13, height: 13, color: c.tertiary, transform: exp ? 'rotate(180deg)' : 'none', transition: 'transform 0.15s', flexShrink: 0 }} />
            </div>
            {exp && pb.steps?.length > 0 && (
              <div style={{ padding: '0 16px 14px', borderTop: `0.5px solid ${c.sep}` }}>
                <div style={{ marginTop: 12, display: 'flex', flexDirection: 'column', gap: 8 }}>
                  {pb.steps.map((step: any, si: number) => {
                    const stepSuccess = Math.round((step.success_rate ?? 0) * 100)
                    const stepColor = stepSuccess >= 80 ? c.green : stepSuccess >= 50 ? c.orange : c.red
                    return (
                      <div key={si} style={{ display: 'flex', gap: 10, padding: '10px 12px', background: c.bg, borderRadius: 9, border: `0.5px solid ${c.sep}` }}>
                        <div style={{ width: 20, height: 20, borderRadius: 6, background: `${c.blue}14`, display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0, fontSize: 11, fontWeight: 700, color: c.blue }}>{si + 1}</div>
                        <div style={{ flex: 1 }}>
                          <div style={{ fontSize: 13, color: c.label, marginBottom: 4 }}>{step.description}</div>
                          {step.command && (
                            <code style={{ fontSize: 11, padding: '3px 8px', background: `${c.fill}`, borderRadius: 6, color: c.label, display: 'block', marginBottom: 4 }}>{step.command}</code>
                          )}
                          <div style={{ display: 'flex', gap: 10, fontSize: 11, color: c.tertiary }}>
                            <span style={{ color: stepColor }}>{stepSuccess}% success</span>
                            {step.reversible && <span style={{ color: c.green }}>reversible</span>}
                            {step.estimated_minutes && <span>~{step.estimated_minutes}min</span>}
                          </div>
                        </div>
                      </div>
                    )
                  })}
                </div>
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}

// ─── Tab: Risk Scorer ─────────────────────────────────────────────────────────
const RISK_CHANGE_TYPES = ['image_update', 'scale', 'config', 'secret', 'network_policy', 'rbac', 'delete', 'create']
const RISK_ACTORS = ['kubectl', 'argocd', 'helm', 'github_actions', 'operator', 'manual']

function RiskTab({ clusterID, apiOnline }: { clusterID: string; apiOnline: boolean | null }) {
  const [form, setForm] = useState({ resourceKind: 'Deployment', namespace: '', name: '', changeType: 'image_update', actor: 'argocd', newImageTag: '', oldImageTag: '' })
  const [result, setResult] = useState<any>(null)
  const [scoring, setScoring] = useState(false)

  const score = async () => {
    setScoring(true)
    setResult(null)
    try {
      const r = await fetch('/api/v1/kubesense/risk/score', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', ...(token() ? { Authorization: `Bearer ${token()}` } : {}) },
        body: JSON.stringify({
          cluster_id: clusterID, resource_kind: form.resourceKind,
          namespace: form.namespace, name: form.name,
          change_type: form.changeType, actor: form.actor,
          new_image_tag: form.newImageTag, old_image_tag: form.oldImageTag,
        }),
      })
      const d = await r.json()
      setResult(d)
    } catch (e) {
      setResult({ error: String(e) })
    }
    setScoring(false)
  }

  const riskColor = (level: string) =>
    level === 'critical' ? c.red : level === 'high' ? c.orange : level === 'medium' ? c.yellow : c.green

  const inp = (label: string, key: keyof typeof form, placeholder = '') => (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
      <label style={{ fontSize: 11, color: c.sub, fontWeight: 600 }}>{label}</label>
      <input value={form[key]} onChange={e => setForm(f => ({ ...f, [key]: e.target.value }))}
        placeholder={placeholder}
        style={{ padding: '7px 10px', borderRadius: 8, border: `0.5px solid ${c.sep}`, background: c.fill, fontSize: 13, color: c.label, outline: 'none' }} />
    </div>
  )
  const sel = (label: string, key: keyof typeof form, options: string[]) => (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
      <label style={{ fontSize: 11, color: c.sub, fontWeight: 600 }}>{label}</label>
      <select value={form[key]} onChange={e => setForm(f => ({ ...f, [key]: e.target.value }))}
        style={{ padding: '7px 10px', borderRadius: 8, border: `0.5px solid ${c.sep}`, background: c.fill, fontSize: 13, color: c.label, cursor: 'pointer' }}>
        {options.map(o => <option key={o} value={o}>{o}</option>)}
      </select>
    </div>
  )

  return (
    <div>
      {!apiOnline && (
        <div style={{ marginBottom: 16, padding: '10px 14px', background: `${c.orange}08`, borderRadius: 10, border: `0.5px solid ${c.orange}28`, fontSize: 13, color: c.sub }}>
          <strong style={{ color: c.orange }}>kubesense-api unreachable.</strong> Risk scoring requires kubesense-api in the aileron-agent namespace.
        </div>
      )}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginBottom: 16 }}>
        {sel('Resource Kind', 'resourceKind', ['Deployment', 'StatefulSet', 'DaemonSet', 'ConfigMap', 'Secret', 'Service', 'Node'])}
        {inp('Namespace', 'namespace', 'e.g. production')}
        {inp('Resource Name', 'name', 'e.g. payment-service')}
        {sel('Change Type', 'changeType', RISK_CHANGE_TYPES)}
        {sel('Actor', 'actor', RISK_ACTORS)}
        {inp('New Image Tag', 'newImageTag', 'e.g. v2.4.1')}
        {inp('Old Image Tag', 'oldImageTag', 'e.g. v2.4.0')}
      </div>
      <button onClick={score} disabled={scoring || !form.name || !apiOnline}
        style={{ padding: '10px 24px', borderRadius: 10, background: c.blue, border: 'none', color: '#fff', fontSize: 13, fontWeight: 600, cursor: (scoring || !form.name || !apiOnline) ? 'not-allowed' : 'pointer', opacity: (scoring || !form.name) ? 0.6 : 1, display: 'flex', alignItems: 'center', gap: 8 }}>
        {scoring ? <Loader2 style={{ width: 14, height: 14, animation: 'spin 1s linear infinite' }} /> : <AlertCircle style={{ width: 14, height: 14 }} />}
        {scoring ? 'Scoring…' : 'Score This Change'}
      </button>

      {result && !result.error && (
        <div style={{ marginTop: 20, padding: 18, background: `${riskColor(result.level)}06`, borderRadius: 14, border: `1px solid ${riskColor(result.level)}30` }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 14, marginBottom: 14 }}>
            <div style={{ padding: '8px 16px', borderRadius: 10, background: `${riskColor(result.level)}18`, color: riskColor(result.level), fontWeight: 700, fontSize: 16, textTransform: 'uppercase' as const }}>
              {result.level ?? 'unknown'}
            </div>
            <div>
              <div style={{ fontSize: 14, fontWeight: 600, color: c.label }}>Risk Score: {Math.round((result.raw ?? 0) * 100)}/100</div>
              <div style={{ fontSize: 12, color: c.sub }}>{result.summary}</div>
            </div>
          </div>
          {result.factors?.length > 0 && (
            <div>
              <div style={{ fontSize: 11, fontWeight: 600, color: c.sub, textTransform: 'uppercase' as const, marginBottom: 8, letterSpacing: '0.4px' }}>Risk Factors</div>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                {result.factors.map((f: any, i: number) => (
                  <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '8px 12px', background: c.fill, borderRadius: 8 }}>
                    <div style={{ flex: 1 }}>
                      <div style={{ fontSize: 12, fontWeight: 600, color: c.label }}>{f.name}</div>
                      <div style={{ fontSize: 11, color: c.sub }}>{f.description}</div>
                    </div>
                    <div style={{ fontSize: 11, fontWeight: 700, color: riskColor(f.score > 0.7 ? 'high' : f.score > 0.4 ? 'medium' : 'low') }}>
                      {Math.round(f.score * 100)}
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      )}
      {result?.error && (
        <div style={{ marginTop: 16, padding: 14, background: `${c.red}08`, borderRadius: 10, fontSize: 13, color: c.red }}>{result.error}</div>
      )}
    </div>
  )
}

// ─── Tab: Correlation Incidents (RCA-Operator) ────────────────────────────────
const PHASE_COLORS: Record<string, string> = {
  Detecting: '#FF9500', Active: '#FF3B30', Resolved: '#34C759',
}
const SEV_COLORS: Record<string, string> = {
  P1: '#FF3B30', P2: '#FF9500', P3: '#FFCC00', P4: '#34C759',
}

function CorrelationTab({ clusterID, apiOnline }: { clusterID: string; apiOnline: boolean | null }) {
  const [incidents, setIncidents] = useState<any[]>([])
  const [rules, setRules] = useState<any[]>([])
  const [status, setStatus] = useState<any>(null)
  const [narratives, setNarratives] = useState<Record<string, string>>({}) // incident_id → text
  const [loading, setLoading] = useState(true)
  const [view, setView] = useState<'incidents' | 'rules' | 'status'>('incidents')
  const [phaseFilter, setPhaseFilter] = useState('all')
  const [expanded, setExpanded] = useState<string | null>(null)

  useEffect(() => {
    if (!apiOnline) { setLoading(false); return }
    setLoading(true)
    const q = clusterID ? `?cluster_id=${clusterID}` : ''
    Promise.all([
      ksGet(`/correlation/incidents${q}`, 8000).then(d => setIncidents(d?.incidents ?? [])).catch(() => {}),
      ksGet('/correlation/rules', 8000).then(d => setRules(d?.rules ?? [])).catch(() => {}),
      ksGet('/correlation/status', 4000).then(d => setStatus(d)).catch(() => {}),
      ksGet(`/narratives${q}`, 8000).then(d => {
        const map: Record<string, string> = {}
        for (const n of d?.narratives ?? []) { map[n.incident_id] = n.narrative }
        setNarratives(map)
      }).catch(() => {}),
    ]).finally(() => setLoading(false))
  }, [clusterID, apiOnline])

  if (!apiOnline) return (
    <EmptyState icon={Activity} title="kubesense-api unreachable"
      sub="Correlation incidents require kubesense-api in the aileron-agent namespace." />
  )
  if (loading) return <Spinner />

  const filteredIncidents = phaseFilter === 'all' ? incidents : incidents.filter((i: any) => i.phase === phaseFilter)

  return (
    <div>
      {status && (
        <div style={{ display: 'flex', gap: 16, padding: '10px 14px', background: `${c.blue}06`, borderRadius: 10, border: `0.5px solid ${c.blue}18`, marginBottom: 16, flexWrap: 'wrap' as const }}>
          <span style={{ fontSize: 12, color: c.sub }}>
            Buffer: <strong style={{ color: c.label }}>{status.buffer_len ?? 0}</strong> events
            {(status.buffer_len ?? 0) === 0 && (
              <span style={{ fontSize: 11, color: c.tertiary, marginLeft: 4 }}>(populates within 30s of startup)</span>
            )}
          </span>
          <span style={{ fontSize: 12, color: c.sub }}>Rules: <strong style={{ color: c.label }}>{status.rule_count ?? 0}</strong></span>
          <span style={{ fontSize: 12, color: c.sub }}>Mining: <strong style={{ color: c.label }}>{status.tracked_patterns ?? 0}</strong> patterns</span>
          <span style={{ fontSize: 12, color: c.sub }}>Active: <strong style={{ color: c.red }}>{status.active_incidents ?? 0}</strong> incidents</span>
        </div>
      )}
      <div style={{ display: 'flex', gap: 6, marginBottom: 14 }}>
        {(['incidents', 'rules', 'status'] as const).map(v => (
          <button key={v} onClick={() => setView(v)} style={{ padding: '5px 14px', borderRadius: 8, border: 'none', cursor: 'pointer', fontSize: 12, fontWeight: 500,
            background: view === v ? `${c.blue}18` : c.fill, color: view === v ? c.blue : c.sub }}>
            {v === 'incidents' ? `Incidents (${incidents.length})` : v === 'rules' ? `Rules (${rules.length})` : 'Engine Status'}
          </button>
        ))}
      </div>

      {view === 'incidents' && (
        <div>
          <div style={{ display: 'flex', gap: 6, marginBottom: 12 }}>
            {['all', 'Detecting', 'Active', 'Resolved'].map(p => (
              <button key={p} onClick={() => setPhaseFilter(p)} style={{ padding: '3px 10px', borderRadius: 8, border: 'none', cursor: 'pointer', fontSize: 12,
                background: phaseFilter === p ? `${PHASE_COLORS[p] ?? c.blue}18` : c.fill,
                color: phaseFilter === p ? (PHASE_COLORS[p] ?? c.blue) : c.sub }}>
                {p === 'all' ? `All (${incidents.length})` : `${p} (${incidents.filter((i: any) => i.phase === p).length})`}
              </button>
            ))}
          </div>
          {filteredIncidents.length === 0 ? (
            <EmptyState icon={CheckCircle} title="No incidents"
              sub="Correlation incidents appear when the rule engine detects co-occurring event patterns in the 15-minute sliding buffer. Built-in rules: OOM+CrashLoop, NodePressure+Eviction, ConfigChange+CrashLoop, and more." />
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {filteredIncidents.map((inc: any) => {
                const phaseColor = PHASE_COLORS[inc.phase] ?? c.gray
                const sevColor = SEV_COLORS[inc.severity] ?? c.gray
                const exp = expanded === inc.id
                return (
                  <div key={inc.id} style={{ background: c.fill, borderRadius: 11, border: `0.5px solid ${phaseColor}30`, overflow: 'hidden' }}>
                    <div onClick={() => setExpanded(exp ? null : inc.id)}
                      style={{ padding: '12px 14px', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 10 }}>
                      <div style={{ fontSize: 10, padding: '2px 7px', borderRadius: 8, background: `${phaseColor}18`, color: phaseColor, fontWeight: 700, flexShrink: 0 }}>{inc.phase}</div>
                      <div style={{ flex: 1, minWidth: 0 }}>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
                          <span style={{ fontWeight: 600, fontSize: 13, color: c.label }}>{inc.incident_type}</span>
                          <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 7, background: `${sevColor}18`, color: sevColor, fontWeight: 700 }}>{inc.severity}</span>
                          {(() => {
                            const m = inc.correlation_method ?? ''
                            if (m === 'topology') return <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 7, background: `${c.blue}18`, color: c.blue, fontWeight: 700 }}>TOPO RCA</span>
                            if (m === 'change_correlation') return <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 7, background: `${c.purple}18`, color: c.purple, fontWeight: 700 }}>CHANGE RCA</span>
                            if (m.startsWith('auto')) return <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 7, background: `${c.green}18`, color: c.green, fontWeight: 700 }}>AUTO DETECTED</span>
                            if (m) return <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 7, background: `${c.gray ?? c.sub}18`, color: c.gray ?? c.sub, fontWeight: 700 }}>RULE</span>
                            return null
                          })()}
                        </div>
                        <div style={{ fontSize: 12, color: c.sub }}>{inc.summary}</div>
                        <div style={{ fontSize: 11, color: c.tertiary, marginTop: 3 }}>
                          {[inc.namespace, inc.resource_kind, inc.resource_name].filter(Boolean).join(' / ')}
                          {inc.rule_name && ` · rule: ${inc.rule_name}`}
                          {` · ${inc.signal_count ?? 1} signal${(inc.signal_count ?? 1) !== 1 ? 's' : ''}`}
                        </div>
                      </div>
                      <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 4, flexShrink: 0 }}>
                        <div style={{ fontSize: 11, color: c.tertiary }}>{ago(inc.first_observed_at)}</div>
                        <ChevronDown style={{ width: 13, height: 13, color: c.tertiary, transform: exp ? 'rotate(180deg)' : 'none' }} />
                      </div>
                    </div>
                    {exp && (narratives[inc.id] || inc.timeline?.length > 0) && (
                      <div style={{ padding: '0 14px 12px', borderTop: `0.5px solid ${c.sep}` }}>
                        {narratives[inc.id] && (
                          <div style={{ marginTop: 12, marginBottom: 10 }}>
                            <div style={{ fontSize: 11, fontWeight: 600, color: c.sub, marginBottom: 6, textTransform: 'uppercase' as const, display: 'flex', alignItems: 'center', gap: 6 }}>
                              <Activity style={{ width: 11, height: 11 }} /> LLM Narrative
                            </div>
                            <div style={{ fontSize: 13, color: c.label, lineHeight: 1.7, padding: '10px 12px', background: `${c.blue}05`, borderRadius: 9, border: `0.5px solid ${c.blue}18` }}>
                              {narratives[inc.id]}
                            </div>
                          </div>
                        )}
                        {inc.timeline?.length > 0 && (
                        <div>
                        <div style={{ fontSize: 11, fontWeight: 600, color: c.sub, margin: '10px 0 6px', textTransform: 'uppercase' as const }}>Timeline</div>
                        {inc.timeline.slice(-8).map((t: any, i: number) => (
                          <div key={i} style={{ display: 'flex', gap: 10, padding: '4px 0', borderTop: i > 0 ? `0.5px solid ${c.sep}` : 'none' }}>
                            <div style={{ fontSize: 11, color: c.tertiary, flexShrink: 0, minWidth: 55 }}>{ago(t.time)}</div>
                            <div style={{ fontSize: 12, color: c.sub }}>{t.event}</div>
                          </div>
                        ))}
                      </div>
                        )}
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </div>
      )}

      {view === 'rules' && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {rules.length === 0 ? (
            <EmptyState icon={Shield} title="No correlation rules" sub="Built-in rules are seeded on first startup of kubesense-api." />
          ) : rules.map((r: any, i: number) => (
            <div key={i} style={{ padding: '10px 14px', background: c.fill, borderRadius: 10, border: `0.5px solid ${c.sep}` }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
                <span style={{ fontWeight: 600, fontSize: 13, color: c.label }}>{r.name}</span>
                {r.auto_generated && (
                  <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 7, background: `${c.purple}18`, color: c.purple, fontWeight: 600 }}>AUTO-DETECTED</span>
                )}
                <span style={{ fontSize: 10, padding: '1px 5px', borderRadius: 6, background: c.fill, color: c.sub }}>P{r.priority}</span>
                <span style={{ fontSize: 10, color: c.tertiary, marginLeft: 'auto' }}>{r.scope}</span>
              </div>
              <div style={{ fontSize: 12, color: c.sub }}>
                <strong>Trigger:</strong> {r.trigger_event_type} → <strong>Fires:</strong> {r.fires_incident_type} ({r.fires_severity})
              </div>
              <div style={{ fontSize: 12, color: c.tertiary, marginTop: 2 }}>{r.fires_summary}</div>
              {r.data_points > 0 && <div style={{ fontSize: 11, color: c.tertiary, marginTop: 2 }}>{r.data_points} co-occurrences observed</div>}
            </div>
          ))}
        </div>
      )}

      {view === 'status' && status && (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(2, 1fr)', gap: 12 }}>
          {[
            { label: 'Correlator online', value: status.online ? '✓ Running' : '✗ Offline', color: status.online ? c.green : c.red },
            { label: 'Buffer events (15-min window)', value: status.buffer_len ?? 0, color: c.blue },
            { label: 'Correlation rules loaded', value: status.rule_count ?? 0, color: c.indigo },
            { label: 'Auto-detection patterns tracked', value: status.tracked_patterns ?? 0, color: c.purple },
            { label: 'Active/Detecting incidents', value: status.active_incidents ?? 0, color: c.orange },
            { label: 'Baselines Active', value: status.baseline_models ?? 0, color: c.teal ?? c.blue },
            { label: 'Flap Suppressed', value: status.flap_suppressed ?? 0, color: c.red },
          ].map((k, i) => (
            <div key={i} style={{ padding: 14, background: `${k.color}08`, borderRadius: 10, border: `0.5px solid ${k.color}20` }}>
              <div style={{ fontSize: 11, color: c.sub, marginBottom: 6 }}>{k.label}</div>
              <div style={{ fontSize: 22, fontWeight: 700, color: k.color }}>{k.value}</div>
              {k.label === 'Buffer events (15-min window)' && k.value === 0 && (
                <div style={{ fontSize: 11, color: c.tertiary, marginTop: 4 }}>populates within 30s of startup</div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

// ─── Main Page ────────────────────────────────────────────────────────────────
export function KubeSensePage() {
  const [tab, setTab] = useState<Tab>('overview')
  const [clusters, setClusters] = useState<any[]>([])
  const [selectedCluster, setSelectedCluster] = useState('')
  const [coreOnline, setCoreOnline] = useState<boolean | null>(null)
  const [apiOnline, setApiOnline] = useState<boolean | null>(null)
  const [data, setData] = useState<DataCache>(EMPTY_CACHE)
  const [loadingDB, setLoadingDB] = useState(true)
  const [lastRefresh, setLastRefresh] = useState(0)
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null)
  // Track cluster in ref so interval callback doesn't capture stale value
  const clusterRef = useRef('')

  const fetchAll = useCallback(async (clusterID: string) => {
    setLoadingDB(true)
    const q = clusterID ? `?cluster_id=${clusterID}` : ''

    // All DB calls in parallel — each stores to state independently so
    // tabs render as soon as their data arrives, not waiting for the slowest.
    const [healthP, changesP, storageP, violP, chaosP, foreP, invP, apmP] = await Promise.allSettled([
      dbGet(`/health${q}&event_type_prefix=health&limit=150`),
      dbGet(`/health${q}&event_type_prefix=change&limit=100`),
      dbGet(`/health${q}&event_type_prefix=storage&limit=50`),
      dbGet(`/violations${q}`),
      dbGet(`/chaos${q}`),
      dbGet(`/forecasts${q}`),
      dbGet(`/investigations${q}`),
      dbGet(`/apm${q}`),
    ])

    setData({
      health:         healthP.status === 'fulfilled' ? (healthP.value?.events ?? []) : [],
      changes:        changesP.status === 'fulfilled' ? (changesP.value?.events ?? []) : [],
      storage:        storageP.status === 'fulfilled' ? (storageP.value?.events ?? []) : [],
      violations:     violP.status === 'fulfilled' ? (violP.value?.violations ?? []) : [],
      chaos:          chaosP.status === 'fulfilled' ? (chaosP.value?.chaos_scores ?? []) : [],
      forecasts:      foreP.status === 'fulfilled' ? (foreP.value?.forecasts ?? []) : [],
      investigations: invP.status === 'fulfilled' ? (invP.value?.results ?? []) : [],
      apm:            apmP.status === 'fulfilled' ? (apmP.value?.signals ?? []) : [],
      fetchedAt: Date.now(),
    })
    setLoadingDB(false)
    setLastRefresh(Date.now())
  }, [])

  const fetchClusters = useCallback(async () => {
    try {
      const d = await ksGet('/clusters', 5000)
      const list: any[] = d?.clusters ?? d?.data?.clusters ?? []
      setCoreOnline(!d?.error && Array.isArray(list))
      setClusters(list)
      if (!clusterRef.current && list.length > 0) {
        clusterRef.current = list[0].id
        setSelectedCluster(list[0].id)
      }
    } catch {
      setCoreOnline(false)
    }
    // Probe kubesense-api health (separate service, separate pod)
    try {
      const h = await ksGet('/investigations/health-check', 3000)
      setApiOnline(h?.error !== 'not found' && h !== null)
    } catch {
      // 404 from a live server = api is up; connection refused = down
      setApiOnline(false)
    }
    // Simpler: just try risk/score with empty body to get a 405/400 (means api is up)
    try {
      const r = await fetch('/api/v1/kubesense/risk/score', {
        method: 'POST', signal: AbortSignal.timeout(3000),
        headers: { 'Content-Type': 'application/json', ...(token() ? { Authorization: `Bearer ${token()}` } : {}) },
        body: '{}',
      })
      setApiOnline(r.status < 500) // 400 = bad request but server is up
    } catch { setApiOnline(false) }
  }, [])

  // Initial load + 30s auto-refresh. Use ref to avoid stale closure in interval.
  useEffect(() => {
    fetchClusters()
    fetchAll(clusterRef.current)
    intervalRef.current = setInterval(() => {
      if (!document.hidden) {
        fetchClusters()
        fetchAll(clusterRef.current)
      }
    }, 30_000)
    return () => { if (intervalRef.current) clearInterval(intervalRef.current) }
  }, []) // intentionally empty — runs once on mount

  // Re-fetch DB data when cluster selection changes
  const handleClusterChange = (id: string) => {
    clusterRef.current = id
    setSelectedCluster(id)
    fetchAll(id)
  }

  const refresh = () => {
    fetchClusters()
    fetchAll(clusterRef.current)
  }

  const tabs: { id: Tab; label: string; icon: any; count?: number; badge?: string }[] = [
    { id: 'overview',       label: 'Overview',        icon: Activity },
    { id: 'clusters',       label: 'Clusters',         icon: Server,       count: clusters.length },
    { id: 'health',         label: 'Health',           icon: AlertTriangle, count: data.health.length },
    { id: 'changes',        label: 'Changes',          icon: GitCommit,     count: data.changes.length },
    { id: 'storage',        label: 'Storage',          icon: HardDrive,     count: data.storage.length },
    { id: 'violations',     label: 'Violations',       icon: Shield,        count: data.violations.length },
    { id: 'chaos',          label: 'Chaos',            icon: Zap,           count: data.chaos.length },
    { id: 'forecasts',      label: 'Forecasts',        icon: TrendingUp,    count: data.forecasts.length },
    { id: 'investigations', label: 'Investigations',   icon: Search,        count: data.investigations.length },
    { id: 'apm',            label: 'APM',              icon: BarChart3,     count: data.apm.length },
    { id: 'playbooks',      label: 'Playbooks',        icon: BookOpen },
    { id: 'risk',           label: 'Risk Score',       icon: AlertCircle },
    { id: 'correlation',    label: 'Correlation',      icon: Activity,    count: 0 },
    { id: 'topology',       label: 'Topology',         icon: Network },
  ]

  const tabCountColor = (id: Tab) => {
    if (id === 'health' && data.health.some((e: any) => e.severity === 'critical')) return c.red
    if (id === 'violations' && data.violations.some((e: any) => e.severity === 'critical')) return c.red
    if (id === 'storage' && data.storage.length > 0) return c.orange
    return c.tertiary
  }

  return (
    <div style={{ minHeight: '100vh', background: c.bg, fontFamily: '-aileron-system, BlinkMacSystemFont, "SF Pro Text", sans-serif' }}>
      {/* Spinning keyframe */}
      <style>{`@keyframes spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }`}</style>
      <div style={{ maxWidth: 1200, margin: '0 auto', padding: '24px 20px' }}>

        {/* Header */}
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 18 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
            <div style={{ width: 40, height: 40, borderRadius: 12, background: `${c.blue}14`, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
              <Radio style={{ width: 20, height: 20, color: c.blue }} />
            </div>
            <div>
              <h1 style={{ fontSize: 22, fontWeight: 700, color: c.label, margin: 0 }}>KubeSense</h1>
              <div style={{ fontSize: 12, color: c.sub, marginTop: 2 }}>
                {loadingDB
                  ? 'Loading…'
                  : `${clusters.length} cluster${clusters.length !== 1 ? 's' : ''} · refreshed ${ago(new Date(lastRefresh).toISOString())}`
                }
              </div>
            </div>
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            {/* Last refresh indicator */}
            {!loadingDB && lastRefresh > 0 && (
              <div style={{
                display: 'flex', alignItems: 'center', gap: 6,
                padding: '5px 12px', borderRadius: 20,
                background: (Date.now() - lastRefresh) < 2 * 60 * 1000 ? `${c.green}10` : `${c.orange}10`,
                border: `0.5px solid ${(Date.now() - lastRefresh) < 2 * 60 * 1000 ? c.green : c.orange}30`,
              }}>
                <span style={{
                  width: 6, height: 6, borderRadius: '50%', flexShrink: 0,
                  background: (Date.now() - lastRefresh) < 2 * 60 * 1000 ? c.green : c.orange,
                }} />
                <span style={{ fontSize: 12, fontWeight: 600, color: (Date.now() - lastRefresh) < 2 * 60 * 1000 ? c.green : c.orange }}>
                  {ago(new Date(lastRefresh).toISOString())}
                </span>
              </div>
            )}
            {/* Cluster selector */}
            {clusters.length > 1 && (
              <select value={selectedCluster} onChange={e => handleClusterChange(e.target.value)}
                style={{ padding: '6px 12px', borderRadius: 8, border: `0.5px solid ${c.sep}`, background: c.fill, fontSize: 13, color: c.label, cursor: 'pointer' }}>
                {clusters.map((cl: any) => <option key={cl.id} value={cl.id}>{cl.id}</option>)}
              </select>
            )}
            {clusters.length === 1 && (
              <span style={{ fontSize: 13, color: c.sub, padding: '6px 12px', background: c.fill, borderRadius: 8, border: `0.5px solid ${c.sep}` }}>
                {selectedCluster || clusters[0]?.id}
              </span>
            )}
            <button onClick={refresh} disabled={loadingDB}
              style={{ padding: '6px 12px', borderRadius: 8, background: c.fill, border: `0.5px solid ${c.sep}`, cursor: loadingDB ? 'default' : 'pointer', display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, color: c.sub, opacity: loadingDB ? 0.6 : 1 }}>
              <RefreshCw style={{ width: 13, height: 13, animation: loadingDB ? 'spin 1s linear infinite' : 'none' }} />
              Refresh
            </button>
          </div>
        </div>

        {/* Status banners */}
        {!loadingDB && coreOnline === false && (
          <div style={{ marginBottom: 14, padding: '10px 16px', background: `${c.orange}08`, borderRadius: 10, border: `0.5px solid ${c.orange}28`, display: 'flex', alignItems: 'center', gap: 10 }}>
            <AlertTriangle style={{ width: 14, height: 14, color: c.orange, flexShrink: 0 }} />
            <div style={{ fontSize: 13, color: c.sub }}>
              <strong style={{ color: c.orange }}>kubesense-core unreachable.</strong>{' '}
              Clusters and Topology tabs require it. All DB-backed tabs show live data from AlertHub.
            </div>
            <span style={{ fontSize: 10, padding: '2px 7px', borderRadius: 10, background: data.fetchedAt > 0 ? `${c.green}14` : `${c.red}10`, color: data.fetchedAt > 0 ? c.green : c.red, fontWeight: 600, flexShrink: 0 }}>
              {data.fetchedAt > 0 ? 'DB OK' : 'NO DATA'}
            </span>
          </div>
        )}
        {!loadingDB && clusters.length === 0 && coreOnline !== false && (
          <div style={{ marginBottom: 14, padding: '10px 16px', background: `${c.blue}05`, borderRadius: 10, border: `0.5px solid ${c.blue}18`, fontSize: 13, color: c.sub }}>
            <strong style={{ color: c.label }}>No clusters registered yet.</strong>{' '}
            Deploy the KubeSense agent with{' '}
            <code style={{ background: c.fill, padding: '1px 5px', borderRadius: 4, fontSize: 12 }}>--cluster-id=&lt;name&gt;</code>{' '}
            pointing at this Kafka cluster.
          </div>
        )}

        {/* Tab bar */}
        <div style={{ display: 'flex', gap: 0, marginBottom: 20, borderBottom: `0.5px solid ${c.sep}`, overflowX: 'auto' as const, scrollbarWidth: 'none' as const }}>
          {tabs.map(t => {
            const coreOnly = t.id === 'clusters' || t.id === 'topology'
            const dimmed = coreOnly && coreOnline === false
            const active = tab === t.id
            const cnt = t.count ?? 0
            const cntColor = tabCountColor(t.id)
            return (
              <button key={t.id} onClick={() => setTab(t.id)} style={{
                display: 'flex', alignItems: 'center', gap: 5, padding: '8px 13px',
                border: 'none', cursor: dimmed ? 'not-allowed' : 'pointer', fontSize: 13,
                fontWeight: active ? 600 : 500, background: 'transparent',
                whiteSpace: 'nowrap' as const, flexShrink: 0,
                borderBottom: active ? `2px solid ${c.blue}` : '2px solid transparent',
                color: active ? c.blue : dimmed ? c.tertiary : c.sub,
                opacity: dimmed ? 0.45 : 1, marginBottom: -1,
              }}>
                <t.icon style={{ width: 13, height: 13 }} />
                {t.label}
                {cnt > 0 && !active && (
                  <span style={{ fontSize: 10, padding: '1px 5px', borderRadius: 8, background: `${cntColor}14`, color: cntColor, fontWeight: 700, marginLeft: 1 }}>{cnt}</span>
                )}
              </button>
            )
          })}
        </div>

        {/* Content panel */}
        <div style={{ background: c.card, borderRadius: 16, padding: 24, border: `0.5px solid ${c.sep}`, minHeight: 300 }}>
          {loadingDB && tab !== 'clusters' && tab !== 'topology' ? (
            <Spinner />
          ) : (
            <>
              {tab === 'overview'       && <OverviewTab data={data} clusters={clusters} clusterID={selectedCluster} />}
              {tab === 'clusters'       && <ClustersTab clusters={clusters} coreOnline={coreOnline} />}
              {tab === 'health'         && <HealthTab events={data.health} />}
              {tab === 'changes'        && <ChangesTab events={data.changes} />}
              {tab === 'storage'        && <StorageTab events={data.storage} />}
              {tab === 'violations'     && <ViolationsTab violations={data.violations} />}
              {tab === 'chaos'          && <ChaosTab scores={data.chaos} />}
              {tab === 'forecasts'      && <ForecastsTab forecasts={data.forecasts} />}
              {tab === 'investigations' && <InvestigationsTab results={data.investigations} />}
              {tab === 'apm'            && <APMTab signals={data.apm} />}
              {tab === 'playbooks'      && <PlaybooksTab clusterID={selectedCluster} apiOnline={apiOnline} />}
              {tab === 'risk'           && <RiskTab clusterID={selectedCluster} apiOnline={apiOnline} />}
              {tab === 'correlation'    && <CorrelationTab clusterID={selectedCluster} apiOnline={apiOnline} />}
              {tab === 'topology'       && <TopologyTab clusterID={selectedCluster} coreOnline={coreOnline} />}
            </>
          )}
        </div>

        {/* Footer */}
        {data.fetchedAt > 0 && (
          <div style={{ marginTop: 12, textAlign: 'right', fontSize: 11, color: c.tertiary }}>
            Data fetched {ago(new Date(data.fetchedAt).toISOString())} · auto-refreshes every 30s
          </div>
        )}
      </div>
    </div>
  )
}
