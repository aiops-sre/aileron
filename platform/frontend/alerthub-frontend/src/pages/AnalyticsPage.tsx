import React, { useState, useEffect, useRef, useCallback } from 'react'
import { motion, AnimatePresence } from 'framer-motion'
import { Chart, registerables } from 'chart.js'
import {
  Activity, BarChart3, AlertTriangle, Target, Brain, Shield,
  RefreshCw, Download, TrendingUp, TrendingDown, Clock,
  CheckCircle, XCircle, Zap, GitMerge, Eye, Layers,
  ChevronUp, ChevronDown, Minus,
} from 'lucide-react'

Chart.register(...registerables)

// ─── Design tokens ──────────────────────────────────────────────────────────

const C = {
  blue: '#007AFF', green: '#34C759', red: '#FF3B30', orange: '#FF9500',
  yellow: '#FFCC00', purple: '#AF52DE', teal: '#32ADE6', pink: '#FF2D55',
  bg: 'var(--color-background)',
  card: 'var(--color-card, rgba(255,255,255,0.85))',
  text: 'var(--color-text)',
  sub: 'var(--color-text-secondary)',
  sep: 'var(--color-separator, rgba(142,142,147,0.15))',
  fill: 'rgba(142,142,147,0.08)',
  r: { sm: 6, md: 10, lg: 12, xl: 16 },
} as const

const SEV_COLORS: Record<string, string> = {
  critical: C.red, high: C.orange, medium: C.yellow, low: C.blue, info: C.teal,
}

const STATUS_COLORS: Record<string, string> = {
  open: C.red, investigating: C.orange, identified: C.yellow,
  monitoring: C.teal, resolved: C.green, closed: C.purple,
  acknowledged: C.blue, closed_nc: C.purple,
}

// ─── API helper ─────────────────────────────────────────────────────────────

const apiFetch = async (path: string) => {
  const token = sessionStorage.getItem('access_token') || localStorage.getItem('access_token')
  const res = await fetch(path, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  const json = await res.json()
  return json.data ?? json
}

// ─── Shared components ───────────────────────────────────────────────────────

const Card: React.FC<{ children: React.ReactNode; style?: React.CSSProperties }> = ({ children, style }) => (
  <div style={{
    background: C.card, border: `0.5px solid ${C.sep}`, borderRadius: C.r.lg,
    padding: 20, ...style,
  }}>{children}</div>
)

const CardTitle: React.FC<{ icon: React.ElementType; color: string; title: string }> = ({ icon: Icon, color, title }) => (
  <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 16 }}>
    <div style={{ width: 28, height: 28, borderRadius: C.r.sm, background: color, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
      <Icon style={{ width: 15, height: 15, color: '#fff' }} />
    </div>
    <span style={{ fontSize: 15, fontWeight: 600, color: C.text }}>{title}</span>
  </div>
)

interface KPIProps {
  label: string
  value: string
  sub?: string
  color?: string
  trend?: 'up' | 'down' | 'neutral'
  trendGood?: boolean // true = up is good, false = up is bad
}
const KPI: React.FC<KPIProps> = ({ label, value, sub, color = C.blue, trend, trendGood = true }) => {
  const TrendIcon = trend === 'up' ? ChevronUp : trend === 'down' ? ChevronDown : Minus
  const trendColor = !trend ? C.sub : (trend === 'up') === trendGood ? C.green : C.red
  return (
    <Card>
      <div style={{ fontSize: 13, color: C.sub, marginBottom: 6 }}>{label}</div>
      <div style={{ fontSize: 26, fontWeight: 700, color, letterSpacing: '-0.02em', lineHeight: 1 }}>{value}</div>
      {sub && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 4, marginTop: 6 }}>
          {trend && <TrendIcon style={{ width: 12, height: 12, color: trendColor }} />}
          <span style={{ fontSize: 12, color: trendColor }}>{sub}</span>
        </div>
      )}
    </Card>
  )
}

const ChartBox: React.FC<{
  title: string; icon: React.ElementType; color: string; height?: number
  children: React.ReactNode; style?: React.CSSProperties
}> = ({ title, icon, color, height = 280, children, style }) => (
  <Card style={style}>
    <CardTitle icon={icon} color={color} title={title} />
    <div style={{ position: 'relative', height }}>{children}</div>
  </Card>
)

// ─── Time range selector ─────────────────────────────────────────────────────

const TIME_RANGES = [
  { id: '24h', label: '24h' },
  { id: '7d', label: '7d' },
  { id: '30d', label: '30d' },
  { id: '90d', label: '90d' },
]

const TimeRangePicker: React.FC<{ value: string; onChange: (v: string) => void }> = ({ value, onChange }) => (
  <div style={{ display: 'flex', gap: 2, background: C.fill, borderRadius: C.r.md, padding: 3 }}>
    {TIME_RANGES.map(r => (
      <button key={r.id} onClick={() => onChange(r.id)} style={{
        padding: '4px 10px', borderRadius: C.r.sm - 2, border: 'none', cursor: 'pointer', fontSize: 12, fontWeight: 500,
        background: value === r.id ? C.blue : 'transparent',
        color: value === r.id ? '#fff' : C.sub,
        transition: 'all 0.15s',
      }}>{r.label}</button>
    ))}
  </div>
)

// ─── Chart helpers ───────────────────────────────────────────────────────────

const chartDefaults = {
  responsive: true,
  maintainAspectRatio: false,
  plugins: {
    legend: { labels: { font: { size: 12 }, boxWidth: 12, padding: 16 } },
    tooltip: { cornerRadius: 8, padding: 10 },
  },
  scales: {
    x: { grid: { color: 'rgba(142,142,147,0.1)' }, ticks: { font: { size: 11 } } },
    y: { grid: { color: 'rgba(142,142,147,0.1)' }, ticks: { font: { size: 11 } } },
  },
}

const destroyChart = (ref: React.MutableRefObject<Chart | null>) => {
  ref.current?.destroy()
  ref.current = null
}

// ─── Sidebar ─────────────────────────────────────────────────────────────────

type TabId = 'overview' | 'alerts' | 'incidents' | 'correlation' | 'rca' | 'slo'

const TABS = [
  { id: 'overview' as TabId, label: 'Overview', icon: Activity, color: C.blue },
  { id: 'alerts' as TabId, label: 'Alert Analytics', icon: BarChart3, color: C.orange },
  { id: 'incidents' as TabId, label: 'Incident Analytics', icon: AlertTriangle, color: C.red },
  { id: 'correlation' as TabId, label: 'Correlation Intelligence', icon: GitMerge, color: C.teal },
  { id: 'rca' as TabId, label: 'RCA Insights', icon: Brain, color: C.purple },
  { id: 'slo' as TabId, label: 'SLO Performance', icon: Shield, color: C.green },
]

const Sidebar: React.FC<{ active: TabId; onSelect: (id: TabId) => void }> = ({ active, onSelect }) => (
  <nav style={{ width: 210, flexShrink: 0, paddingTop: 8 }}>
    {TABS.map(t => {
      const Icon = t.icon
      const isActive = t.id === active
      return (
        <button key={t.id} onClick={() => onSelect(t.id)} style={{
          display: 'flex', alignItems: 'center', gap: 10, width: '100%',
          padding: '7px 10px', borderRadius: C.r.sm, border: 'none', cursor: 'pointer',
          background: isActive ? 'rgba(0,122,255,0.1)' : 'transparent', marginBottom: 2,
          transition: 'background 0.15s',
        }}>
          <div style={{ width: 26, height: 26, borderRadius: 6, background: t.color, display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 }}>
            <Icon style={{ width: 14, height: 14, color: '#fff' }} />
          </div>
          <span style={{ fontSize: 13, fontWeight: isActive ? 600 : 400, color: isActive ? C.blue : C.text, flex: 1, textAlign: 'left' }}>
            {t.label}
          </span>
        </button>
      )
    })}
  </nav>
)

// ─── Loading skeleton ────────────────────────────────────────────────────────

const Skeleton: React.FC<{ height?: number; style?: React.CSSProperties }> = ({ height = 20, style }) => (
  <div style={{ height, borderRadius: C.r.sm, background: C.fill, animation: 'pulse 1.5s ease-in-out infinite', ...style }} />
)

// ─── Overview Tab ────────────────────────────────────────────────────────────

const OverviewTab: React.FC<{ timeRange: string }> = ({ timeRange }) => {
  const [data, setData] = useState<any>(null)
  const [loading, setLoading] = useState(true)

  const alertTrendRef = useRef<HTMLCanvasElement>(null)
  const incidentTrendRef = useRef<HTMLCanvasElement>(null)
  const severityRef = useRef<HTMLCanvasElement>(null)
  const sourceRef = useRef<HTMLCanvasElement>(null)
  const alertChart = useRef<Chart | null>(null)
  const incidentChart = useRef<Chart | null>(null)
  const sevChart = useRef<Chart | null>(null)
  const srcChart = useRef<Chart | null>(null)

  const buildCharts = useCallback((d: any) => {
    // Alert trend
    destroyChart(alertChart)
    if (alertTrendRef.current) {
      alertChart.current = new Chart(alertTrendRef.current, {
        type: 'line',
        data: {
          labels: (d.alert_trend || []).map((b: any) => b.date),
          datasets: [{
            label: 'Alerts', data: (d.alert_trend || []).map((b: any) => b.count),
            borderColor: C.blue, backgroundColor: `${C.blue}25`, tension: 0.4, fill: true, pointRadius: 3,
          }, {
            label: 'Incidents', data: (d.incident_trend || []).map((b: any) => b.count),
            borderColor: C.red, backgroundColor: `${C.red}18`, tension: 0.4, fill: true, pointRadius: 3,
          }],
        },
        options: { ...chartDefaults },
      })
    }
    // Severity doughnut
    destroyChart(sevChart)
    const sevLabels = ['critical', 'high', 'medium', 'low', 'info']
    if (severityRef.current) {
      sevChart.current = new Chart(severityRef.current, {
        type: 'doughnut',
        data: {
          labels: sevLabels.map(s => s.charAt(0).toUpperCase() + s.slice(1)),
          datasets: [{ data: sevLabels.map(s => d.alerts_by_severity?.[s] || 0), backgroundColor: sevLabels.map(s => SEV_COLORS[s]), borderWidth: 0 }],
        },
        options: { responsive: true, maintainAspectRatio: false, plugins: { legend: { position: 'right', labels: { font: { size: 12 }, boxWidth: 12 } } } },
      })
    }
    // Source bar
    destroyChart(srcChart)
    const srcEntries = Object.entries(d.alerts_by_source || {}).slice(0, 8)
    if (sourceRef.current) {
      srcChart.current = new Chart(sourceRef.current, {
        type: 'bar',
        data: {
          labels: srcEntries.map(([k]) => k),
          datasets: [{ label: 'Alerts', data: srcEntries.map(([, v]) => v as number), backgroundColor: C.blue, borderRadius: 4 }],
        },
        options: { ...chartDefaults, indexAxis: 'y' as const, plugins: { legend: { display: false } } },
      })
    }
  }, [])

  useEffect(() => {
    setLoading(true)
    apiFetch(`/api/v1/analytics/overview?time_range=${timeRange}`)
      .then(d => { setData(d); setLoading(false) })
      .catch(() => setLoading(false))
  }, [timeRange])

  useEffect(() => {
    if (data) buildCharts(data)
    return () => { destroyChart(alertChart); destroyChart(incidentChart); destroyChart(sevChart); destroyChart(srcChart) }
  }, [data, buildCharts])

  if (loading) return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4,1fr)', gap: 16 }}>{[...Array(8)].map((_, i) => <Skeleton key={i} height={90} />)}</div>
      <Skeleton height={300} />
    </div>
  )

  const fmt = (n: number, dec = 1) => n?.toFixed(dec) ?? '—'

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      {/* KPIs row 1 */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4,1fr)', gap: 14 }}>
        <KPI label="Total Alerts" value={(data?.total_alerts || 0).toLocaleString()} sub={`${data?.open_alerts || 0} open`} color={C.blue} />
        <KPI label="Critical Alerts" value={(data?.critical_alerts || 0).toLocaleString()} sub="unresolved" color={C.red} />
        <KPI label="Total Incidents" value={(data?.total_incidents || 0).toLocaleString()} sub={`${data?.open_incidents || 0} open`} color={C.orange} />
        <KPI label="Resolution Rate" value={`${fmt(data?.resolution_rate)}%`} sub={data?.resolution_rate >= 90 ? 'On track' : 'Below target'} color={data?.resolution_rate >= 90 ? C.green : C.red} trend={data?.resolution_rate >= 90 ? 'up' : 'down'} trendGood />
      </div>
      {/* KPIs row 2 */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4,1fr)', gap: 14 }}>
        <KPI label="MTTR" value={`${fmt(data?.mttr_hours)}h`} sub="Mean time to resolve" color={C.teal} />
        <KPI label="MTTA" value={`${fmt(data?.mtta_hours)}h`} sub="Mean time to acknowledge" color={C.purple} />
        <KPI label="Noise Reduction" value={`${fmt(data?.noise_reduction_pct)}%`} sub="Alerts → incidents" color={C.green} />
        <KPI label="RCA Completion" value={`${fmt(data?.rca_completion_rate)}%`} sub="Auto-investigated" color={C.blue} />
      </div>

      {/* Alert + Incident trends */}
      <Card>
        <CardTitle icon={TrendingUp} color={C.blue} title={`Alert & Incident Trend (${timeRange})`} />
        <div style={{ height: 300 }}><canvas ref={alertTrendRef} /></div>
      </Card>

      {/* Severity + Source */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 20 }}>
        <Card>
          <CardTitle icon={AlertTriangle} color={C.orange} title="Alerts by Severity" />
          <div style={{ height: 260 }}><canvas ref={severityRef} /></div>
        </Card>
        <Card>
          <CardTitle icon={BarChart3} color={C.green} title="Alerts by Source" />
          <div style={{ height: 260 }}><canvas ref={sourceRef} /></div>
        </Card>
      </div>

      {/* Incident status breakdown */}
      <Card>
        <CardTitle icon={Layers} color={C.teal} title="Incident Status Breakdown" />
        <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
          {Object.entries(data?.incidents_by_status || {}).map(([k, v]) => (
            <div key={k} style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '8px 14px', borderRadius: C.r.md, background: `${STATUS_COLORS[k] || C.sub}18`, border: `1px solid ${STATUS_COLORS[k] || C.sub}30` }}>
              <div style={{ width: 8, height: 8, borderRadius: '50%', background: STATUS_COLORS[k] || C.sub }} />
              <span style={{ fontSize: 13, fontWeight: 600, color: STATUS_COLORS[k] || C.text }}>{v as number}</span>
              <span style={{ fontSize: 12, color: C.sub, textTransform: 'capitalize' }}>{k}</span>
            </div>
          ))}
        </div>
      </Card>
    </div>
  )
}

// ─── Alert Analytics Tab ─────────────────────────────────────────────────────

const AlertAnalyticsTab: React.FC<{ timeRange: string }> = ({ timeRange }) => {
  const [data, setData] = useState<any>(null)
  const [loading, setLoading] = useState(true)

  const trendRef = useRef<HTMLCanvasElement>(null)
  const sevRef = useRef<HTMLCanvasElement>(null)
  const statusRef = useRef<HTMLCanvasElement>(null)
  const hourRef = useRef<HTMLCanvasElement>(null)
  const mttrSevRef = useRef<HTMLCanvasElement>(null)
  const trendChart = useRef<Chart | null>(null)
  const sevChart = useRef<Chart | null>(null)
  const statusChart = useRef<Chart | null>(null)
  const hourChart = useRef<Chart | null>(null)
  const mttrChart = useRef<Chart | null>(null)

  useEffect(() => {
    setLoading(true)
    apiFetch(`/api/v1/analytics/alerts/detail?time_range=${timeRange}`)
      .then(d => { setData(d); setLoading(false) })
      .catch(() => setLoading(false))
  }, [timeRange])

  useEffect(() => {
    if (!data) return
    const cleanup = () => { destroyChart(trendChart); destroyChart(sevChart); destroyChart(statusChart); destroyChart(hourChart); destroyChart(mttrChart) }
    cleanup()

    if (trendRef.current) {
      trendChart.current = new Chart(trendRef.current, {
        type: 'bar',
        data: {
          labels: (data.trend || []).map((b: any) => b.date),
          datasets: [{ label: 'Alerts', data: (data.trend || []).map((b: any) => b.count), backgroundColor: `${C.blue}cc`, borderRadius: 3 }],
        },
        options: { ...chartDefaults, plugins: { legend: { display: false } } },
      })
    }

    const sevLabels = ['critical', 'high', 'medium', 'low', 'info']
    if (sevRef.current) {
      sevChart.current = new Chart(sevRef.current, {
        type: 'doughnut',
        data: {
          labels: sevLabels.map(s => s.charAt(0).toUpperCase() + s.slice(1)),
          datasets: [{ data: sevLabels.map(s => data.by_severity?.[s] || 0), backgroundColor: sevLabels.map(s => SEV_COLORS[s]), borderWidth: 0 }],
        },
        options: { responsive: true, maintainAspectRatio: false, plugins: { legend: { position: 'right', labels: { font: { size: 12 }, boxWidth: 12 } } } },
      })
    }

    const statusEntries = Object.entries(data.by_status || {})
    if (statusRef.current) {
      statusChart.current = new Chart(statusRef.current, {
        type: 'bar',
        data: {
          labels: statusEntries.map(([k]) => k),
          datasets: [{ label: 'Alerts', data: statusEntries.map(([, v]) => v as number), backgroundColor: statusEntries.map(([k]) => STATUS_COLORS[k] || C.blue), borderRadius: 4 }],
        },
        options: { ...chartDefaults, plugins: { legend: { display: false } } },
      })
    }

    // Hourly distribution — fill in all 24 hours
    const hourMap: Record<number, number> = {}
    ;(data.hourly_distribution || []).forEach((h: any) => { hourMap[h.hour] = h.count })
    const hours = Array.from({ length: 24 }, (_, i) => i)
    if (hourRef.current) {
      hourChart.current = new Chart(hourRef.current, {
        type: 'bar',
        data: {
          labels: hours.map(h => `${h.toString().padStart(2, '0')}:00`),
          datasets: [{ label: 'Avg alerts', data: hours.map(h => hourMap[h] || 0), backgroundColor: hours.map(h => h >= 9 && h <= 18 ? `${C.blue}cc` : `${C.orange}cc`), borderRadius: 2 }],
        },
        options: { ...chartDefaults, plugins: { legend: { display: false } } },
      })
    }

    // MTTR by severity
    const mttrEntries = Object.entries(data.mttr_by_severity || {})
    if (mttrSevRef.current) {
      mttrChart.current = new Chart(mttrSevRef.current, {
        type: 'bar',
        data: {
          labels: mttrEntries.map(([k]) => k.charAt(0).toUpperCase() + k.slice(1)),
          datasets: [{ label: 'MTTR (hours)', data: mttrEntries.map(([, v]) => parseFloat((v as number).toFixed(2))), backgroundColor: mttrEntries.map(([k]) => SEV_COLORS[k] || C.blue), borderRadius: 4 }],
        },
        options: { ...chartDefaults, plugins: { legend: { display: false } } },
      })
    }

    return cleanup
  }, [data])

  if (loading) return <Skeleton height={500} />

  const fmt = (n: number, dec = 1) => n?.toFixed(dec) ?? '—'

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4,1fr)', gap: 14 }}>
        <KPI label="Total Alerts" value={(data?.total_alerts || 0).toLocaleString()} color={C.blue} />
        <KPI label="Critical Alerts" value={(data?.critical_alerts || 0).toLocaleString()} color={C.red} />
        <KPI label="Resolution Rate" value={`${fmt(data?.resolution_rate)}%`} color={data?.resolution_rate >= 90 ? C.green : C.orange} />
        <KPI label="Avg MTTR" value={`${fmt(data?.avg_mttr_hours)}h`} sub={`${(data?.dedup_count || 0).toLocaleString()} raw events deduped`} color={C.teal} />
      </div>

      <Card>
        <CardTitle icon={BarChart3} color={C.blue} title="Alert Volume Trend" />
        <div style={{ height: 260 }}><canvas ref={trendRef} /></div>
      </Card>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 20 }}>
        <Card>
          <CardTitle icon={AlertTriangle} color={C.orange} title="By Severity" />
          <div style={{ height: 240 }}><canvas ref={sevRef} /></div>
        </Card>
        <Card>
          <CardTitle icon={Activity} color={C.green} title="By Status" />
          <div style={{ height: 240 }}><canvas ref={statusRef} /></div>
        </Card>
      </div>

      <Card>
        <CardTitle icon={Clock} color={C.orange} title="Hourly Distribution (when do alerts fire?)" />
        <div style={{ fontSize: 12, color: C.sub, marginBottom: 10 }}>Blue = business hours · Orange = off-hours</div>
        <div style={{ height: 200 }}><canvas ref={hourRef} /></div>
      </Card>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 20 }}>
        <Card>
          <CardTitle icon={Clock} color={C.purple} title="MTTR by Severity" />
          <div style={{ height: 240 }}><canvas ref={mttrSevRef} /></div>
        </Card>

        <Card>
          <CardTitle icon={TrendingUp} color={C.teal} title="Top Alert Sources" />
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8, maxHeight: 240, overflowY: 'auto' }}>
            {(data?.top_services || []).map((s: any, i: number) => {
              const max = data.top_services[0]?.count || 1
              const pct = (s.count / max) * 100
              return (
                <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                  <span style={{ fontSize: 12, color: C.sub, width: 18, textAlign: 'right' }}>#{i + 1}</span>
                  <span style={{ fontSize: 13, color: C.text, flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{s.service}</span>
                  <div style={{ width: 100, height: 6, borderRadius: 3, background: C.fill }}>
                    <div style={{ width: `${pct}%`, height: '100%', borderRadius: 3, background: i === 0 ? C.red : C.blue }} />
                  </div>
                  <span style={{ fontSize: 12, fontWeight: 600, color: C.text, width: 40, textAlign: 'right' }}>{s.count}</span>
                </div>
              )
            })}
          </div>
        </Card>
      </div>
    </div>
  )
}

// ─── Incident Analytics Tab ──────────────────────────────────────────────────

const IncidentAnalyticsTab: React.FC<{ timeRange: string }> = ({ timeRange }) => {
  const [data, setData] = useState<any>(null)
  const [loading, setLoading] = useState(true)

  const trendRef = useRef<HTMLCanvasElement>(null)
  const sevRef = useRef<HTMLCanvasElement>(null)
  const statusRef = useRef<HTMLCanvasElement>(null)
  const mttrRef = useRef<HTMLCanvasElement>(null)
  const autoRef = useRef<HTMLCanvasElement>(null)
  const trendChart = useRef<Chart | null>(null)
  const sevChart = useRef<Chart | null>(null)
  const statusChart = useRef<Chart | null>(null)
  const mttrChart = useRef<Chart | null>(null)
  const autoChart = useRef<Chart | null>(null)

  useEffect(() => {
    setLoading(true)
    apiFetch(`/api/v1/analytics/incidents/detail?time_range=${timeRange}`)
      .then(d => { setData(d); setLoading(false) })
      .catch(() => setLoading(false))
  }, [timeRange])

  useEffect(() => {
    if (!data) return
    const cleanup = () => { destroyChart(trendChart); destroyChart(sevChart); destroyChart(statusChart); destroyChart(mttrChart); destroyChart(autoChart) }
    cleanup()

    if (trendRef.current) {
      trendChart.current = new Chart(trendRef.current, {
        type: 'line',
        data: {
          labels: (data.trend || []).map((b: any) => b.date),
          datasets: [{ label: 'Incidents', data: (data.trend || []).map((b: any) => b.count), borderColor: C.red, backgroundColor: `${C.red}20`, tension: 0.4, fill: true, pointRadius: 3 }],
        },
        options: { ...chartDefaults, plugins: { legend: { display: false } } },
      })
    }

    const sevLabels = ['critical', 'high', 'medium', 'low']
    if (sevRef.current) {
      sevChart.current = new Chart(sevRef.current, {
        type: 'bar',
        data: {
          labels: sevLabels.map(s => s.charAt(0).toUpperCase() + s.slice(1)),
          datasets: [{ label: 'Incidents', data: sevLabels.map(s => data.by_severity?.[s] || 0), backgroundColor: sevLabels.map(s => SEV_COLORS[s]), borderRadius: 4 }],
        },
        options: { ...chartDefaults, plugins: { legend: { display: false } } },
      })
    }

    const statusEntries = Object.entries(data.by_status || {})
    if (statusRef.current) {
      statusChart.current = new Chart(statusRef.current, {
        type: 'doughnut',
        data: {
          labels: statusEntries.map(([k]) => k),
          datasets: [{ data: statusEntries.map(([, v]) => v as number), backgroundColor: statusEntries.map(([k]) => STATUS_COLORS[k] || C.blue), borderWidth: 0 }],
        },
        options: { responsive: true, maintainAspectRatio: false, plugins: { legend: { position: 'right', labels: { font: { size: 12 }, boxWidth: 12 } } } },
      })
    }

    if (mttrRef.current) {
      mttrChart.current = new Chart(mttrRef.current, {
        type: 'line',
        data: {
          labels: (data.mttr_trend || []).map((b: any) => b.date),
          datasets: [{ label: 'MTTR (h)', data: (data.mttr_trend || []).map((b: any) => b.mttr_hours?.toFixed(2)), borderColor: C.orange, backgroundColor: `${C.orange}20`, tension: 0.4, fill: true, pointRadius: 3 }],
        },
        options: { ...chartDefaults, plugins: { legend: { display: false } } },
      })
    }

    if (autoRef.current) {
      autoChart.current = new Chart(autoRef.current, {
        type: 'doughnut',
        data: {
          labels: ['Auto-created', 'Manual'],
          datasets: [{ data: [data.auto_created || 0, data.manual_created || 0], backgroundColor: [C.blue, C.sep], borderWidth: 0 }],
        },
        options: { responsive: true, maintainAspectRatio: false, plugins: { legend: { position: 'bottom', labels: { font: { size: 12 }, boxWidth: 12 } } } },
      })
    }

    return cleanup
  }, [data])

  if (loading) return <Skeleton height={500} />

  const fmt = (n: number, dec = 1) => n?.toFixed(dec) ?? '—'

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4,1fr)', gap: 14 }}>
        <KPI label="Total Incidents" value={(data?.total_incidents || 0).toLocaleString()} color={C.red} />
        <KPI label="Open Incidents" value={(data?.open_incidents || 0).toLocaleString()} color={C.orange} />
        <KPI label="MTTR" value={`${fmt(data?.mttr_hours)}h`} color={C.teal} />
        <KPI label="MTTA" value={`${fmt(data?.mtta_hours)}h`} color={C.purple} />
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3,1fr)', gap: 14 }}>
        <KPI label="Resolution Rate" value={`${fmt(data?.resolution_rate)}%`} color={data?.resolution_rate >= 90 ? C.green : C.orange} />
        <KPI label="Escalation Rate" value={`${fmt(data?.escalation_rate)}%`} color={C.yellow} />
        <KPI label="RCA Completed" value={(data?.rca_completed_count || 0).toLocaleString()} sub="Investigations done" color={C.blue} />
      </div>

      <Card>
        <CardTitle icon={TrendingUp} color={C.red} title="Incident Volume Trend" />
        <div style={{ height: 240 }}><canvas ref={trendRef} /></div>
      </Card>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 20 }}>
        <Card>
          <CardTitle icon={AlertTriangle} color={C.orange} title="By Severity" />
          <div style={{ height: 240 }}><canvas ref={sevRef} /></div>
        </Card>
        <Card>
          <CardTitle icon={Activity} color={C.teal} title="By Status" />
          <div style={{ height: 240 }}><canvas ref={statusRef} /></div>
        </Card>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: 20 }}>
        <Card>
          <CardTitle icon={Clock} color={C.orange} title="MTTR Trend Over Time" />
          <div style={{ height: 240 }}><canvas ref={mttrRef} /></div>
        </Card>
        <Card>
          <CardTitle icon={Zap} color={C.blue} title="Creation Method" />
          <div style={{ height: 240 }}><canvas ref={autoRef} /></div>
        </Card>
      </div>

      <Card>
        <CardTitle icon={BarChart3} color={C.purple} title="Top Sources (by incident count)" />
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {(data?.top_services_by_incidents || []).slice(0, 8).map((s: any, i: number) => {
            const max = data.top_services_by_incidents[0]?.count || 1
            const pct = (s.count / max) * 100
            return (
              <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                <span style={{ fontSize: 12, color: C.sub, width: 18, textAlign: 'right' }}>#{i + 1}</span>
                <span style={{ fontSize: 13, color: C.text, flex: 1 }}>{s.service}</span>
                <div style={{ width: 120, height: 6, borderRadius: 3, background: C.fill }}>
                  <div style={{ width: `${pct}%`, height: '100%', borderRadius: 3, background: i === 0 ? C.red : C.orange }} />
                </div>
                <span style={{ fontSize: 12, fontWeight: 600, color: C.text, width: 40, textAlign: 'right' }}>{s.count}</span>
              </div>
            )
          })}
        </div>
      </Card>
    </div>
  )
}

// ─── Correlation Intelligence Tab ────────────────────────────────────────────

const CorrelationTab: React.FC<{ timeRange: string }> = ({ timeRange }) => {
  const [data, setData] = useState<any>(null)
  const [loading, setLoading] = useState(true)

  const decisionRef = useRef<HTMLCanvasElement>(null)
  const stratRef = useRef<HTMLCanvasElement>(null)
  const apiRef = useRef<HTMLCanvasElement>(null)
  const trendRef = useRef<HTMLCanvasElement>(null)
  const decisionChart = useRef<Chart | null>(null)
  const stratChart = useRef<Chart | null>(null)
  const apiChart = useRef<Chart | null>(null)
  const trendChart = useRef<Chart | null>(null)

  useEffect(() => {
    setLoading(true)
    apiFetch(`/api/v1/analytics/correlation?time_range=${timeRange}`)
      .then(d => { setData(d); setLoading(false) })
      .catch(() => setLoading(false))
  }, [timeRange])

  useEffect(() => {
    if (!data) return
    const cleanup = () => { destroyChart(decisionChart); destroyChart(stratChart); destroyChart(apiChart); destroyChart(trendChart) }
    cleanup()

    const decEntries = Object.entries(data.decision_distribution || {})
    const decColors: Record<string, string> = { CreateIncident: C.red, MergeIntoExisting: C.green, DecisionMonitor: C.orange, dropped: C.purple }
    if (decisionRef.current) {
      decisionChart.current = new Chart(decisionRef.current, {
        type: 'doughnut',
        data: {
          labels: decEntries.map(([k]) => k),
          datasets: [{ data: decEntries.map(([, v]) => v as number), backgroundColor: decEntries.map(([k]) => decColors[k] || C.blue), borderWidth: 0 }],
        },
        options: { responsive: true, maintainAspectRatio: false, plugins: { legend: { position: 'right', labels: { font: { size: 11 }, boxWidth: 12 } } } },
      })
    }

    const ss = data.strategy_scores || {}
    if (stratRef.current) {
      stratChart.current = new Chart(stratRef.current, {
        type: 'radar',
        data: {
          labels: ['Topology (35%)', 'Semantic (25%)', 'Temporal (25%)', 'Rules (15%)'],
          datasets: [{
            label: 'Avg Score', data: [ss.topology || 0, ss.semantic || 0, ss.temporal || 0, ss.rules || 0].map((v: number) => +(v * 100).toFixed(1)),
            borderColor: C.blue, backgroundColor: `${C.blue}30`, pointBackgroundColor: C.blue, pointRadius: 4,
          }],
        },
        options: {
          responsive: true, maintainAspectRatio: false,
          plugins: { legend: { display: false } },
          scales: { r: { beginAtZero: true, max: 100, ticks: { font: { size: 10 } }, pointLabels: { font: { size: 11 } } } },
        },
      })
    }

    const apiBuckets = data.alerts_per_incident || []
    if (apiRef.current) {
      apiChart.current = new Chart(apiRef.current, {
        type: 'bar',
        data: {
          labels: apiBuckets.map((b: any) => `${b.bucket} alerts`),
          datasets: [{ label: 'Incidents', data: apiBuckets.map((b: any) => b.count), backgroundColor: C.teal, borderRadius: 4 }],
        },
        options: { ...chartDefaults, plugins: { legend: { display: false } } },
      })
    }

    if (trendRef.current) {
      trendChart.current = new Chart(trendRef.current, {
        type: 'bar',
        data: {
          labels: (data.trend || []).map((b: any) => b.date),
          datasets: [{ label: 'Processed', data: (data.trend || []).map((b: any) => b.count), backgroundColor: `${C.teal}cc`, borderRadius: 3 }],
        },
        options: { ...chartDefaults, plugins: { legend: { display: false } } },
      })
    }

    return cleanup
  }, [data])

  if (loading) return <Skeleton height={500} />

  const fmt = (n: number, dec = 1) => n?.toFixed(dec) ?? '—'

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4,1fr)', gap: 14 }}>
        <KPI label="Total Processed" value={(data?.total_processed || 0).toLocaleString()} color={C.blue} />
        <KPI label="Noise Reduction" value={`${fmt(data?.noise_reduction_pct)}%`} sub="Alerts merged" color={C.green} trend="up" trendGood />
        <KPI label="Avg Correlation Score" value={fmt(data?.avg_score, 3)} color={C.teal} />
        <KPI label="Avg Confidence" value={fmt(data?.avg_confidence, 3)} color={C.purple} />
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3,1fr)', gap: 14 }}>
        <KPI label="New Incidents" value={(data?.incidents_created || 0).toLocaleString()} color={C.red} />
        <KPI label="Alerts Merged" value={(data?.alerts_merged || 0).toLocaleString()} color={C.green} sub="Into existing incidents" />
        <KPI label="Monitoring Queue" value={(data?.monitoring_queued || 0).toLocaleString()} color={C.orange} sub="Hold-window entries" />
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 20 }}>
        <Card>
          <CardTitle icon={GitMerge} color={C.teal} title="Decision Distribution" />
          <div style={{ height: 260 }}><canvas ref={decisionRef} /></div>
        </Card>
        <Card>
          <CardTitle icon={Target} color={C.blue} title="Strategy Effectiveness (avg score %)" />
          <div style={{ height: 260 }}><canvas ref={stratRef} /></div>
        </Card>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 20 }}>
        <Card>
          <CardTitle icon={Layers} color={C.orange} title="Alerts per Incident Distribution" />
          <div style={{ height: 240 }}><canvas ref={apiRef} /></div>
        </Card>
        <Card>
          <CardTitle icon={TrendingUp} color={C.teal} title="Correlation Pipeline Volume Trend" />
          <div style={{ height: 240 }}><canvas ref={trendRef} /></div>
        </Card>
      </div>
    </div>
  )
}

// ─── RCA Insights Tab ────────────────────────────────────────────────────────

const RCAInsightsTab: React.FC<{ timeRange: string }> = ({ timeRange }) => {
  const [data, setData] = useState<any>(null)
  const [loading, setLoading] = useState(true)

  const phaseRef = useRef<HTMLCanvasElement>(null)
  const confRef = useRef<HTMLCanvasElement>(null)
  const trendRef = useRef<HTMLCanvasElement>(null)
  const phaseChart = useRef<Chart | null>(null)
  const confChart = useRef<Chart | null>(null)
  const trendChart = useRef<Chart | null>(null)

  useEffect(() => {
    setLoading(true)
    apiFetch(`/api/v1/analytics/rca?time_range=${timeRange}`)
      .then(d => { setData(d); setLoading(false) })
      .catch(() => setLoading(false))
  }, [timeRange])

  useEffect(() => {
    if (!data) return
    const cleanup = () => { destroyChart(phaseChart); destroyChart(confChart); destroyChart(trendChart) }
    cleanup()

    const phaseColors: Record<string, string> = { completed: C.green, failed: C.red, investigating: C.orange, queued: C.blue }
    const phaseEntries = Object.entries(data.phase_distribution || {})
    if (phaseRef.current) {
      phaseChart.current = new Chart(phaseRef.current, {
        type: 'doughnut',
        data: {
          labels: phaseEntries.map(([k]) => k),
          datasets: [{ data: phaseEntries.map(([, v]) => v as number), backgroundColor: phaseEntries.map(([k]) => phaseColors[k] || C.blue), borderWidth: 0 }],
        },
        options: { responsive: true, maintainAspectRatio: false, plugins: { legend: { position: 'bottom', labels: { font: { size: 12 }, boxWidth: 12 } } } },
      })
    }

    const confBuckets = data.confidence_buckets || []
    if (confRef.current) {
      confChart.current = new Chart(confRef.current, {
        type: 'bar',
        data: {
          labels: confBuckets.map((b: any) => b.range),
          datasets: [{ label: 'Investigations', data: confBuckets.map((b: any) => b.count), backgroundColor: [C.red, C.orange, C.yellow, C.teal, C.green], borderRadius: 4 }],
        },
        options: { ...chartDefaults, plugins: { legend: { display: false } } },
      })
    }

    if (trendRef.current) {
      trendChart.current = new Chart(trendRef.current, {
        type: 'bar',
        data: {
          labels: (data.trend || []).map((b: any) => b.date),
          datasets: [{ label: 'Investigations', data: (data.trend || []).map((b: any) => b.count), backgroundColor: `${C.purple}cc`, borderRadius: 3 }],
        },
        options: { ...chartDefaults, plugins: { legend: { display: false } } },
      })
    }

    return cleanup
  }, [data])

  if (loading) return <Skeleton height={500} />

  const fmt = (n: number, dec = 1) => n?.toFixed(dec) ?? '—'

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4,1fr)', gap: 14 }}>
        <KPI label="Total Investigations" value={(data?.total_investigations || 0).toLocaleString()} color={C.purple} />
        <KPI label="Completion Rate" value={`${fmt(data?.completion_rate)}%`} color={data?.completion_rate >= 70 ? C.green : C.orange} />
        <KPI label="Avg Confidence" value={fmt(data?.avg_confidence, 2)} sub="0.0 – 1.0 scale" color={C.blue} />
        <KPI label="Avg Investigation Time" value={`${fmt(data?.avg_investigation_minutes)}m`} sub="CPU Ollama inference" color={C.teal} />
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 20 }}>
        <Card>
          <CardTitle icon={Brain} color={C.purple} title="Investigation Phase Distribution" />
          <div style={{ height: 240 }}><canvas ref={phaseRef} /></div>
        </Card>
        <Card>
          <CardTitle icon={Target} color={C.green} title="Confidence Score Distribution" />
          <div style={{ height: 240 }}><canvas ref={confRef} /></div>
        </Card>
      </div>

      <Card>
        <CardTitle icon={TrendingUp} color={C.purple} title="RCA Investigation Volume Trend" />
        <div style={{ height: 200 }}><canvas ref={trendRef} /></div>
      </Card>

      <Card>
        <CardTitle icon={Eye} color={C.blue} title="Recent Investigations" />
        <div style={{ overflowX: 'auto' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
            <thead>
              <tr style={{ borderBottom: `1px solid ${C.sep}` }}>
                {['Phase', 'Confidence', 'Root Cause', 'Created At'].map(h => (
                  <th key={h} style={{ textAlign: 'left', padding: '6px 10px', color: C.sub, fontWeight: 500 }}>{h}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {(data?.recent_investigations || []).map((r: any) => {
                const phaseColors: Record<string, string> = { completed: C.green, failed: C.red, investigating: C.orange, queued: C.blue }
                return (
                  <tr key={r.id} style={{ borderBottom: `0.5px solid ${C.sep}` }}>
                    <td style={{ padding: '8px 10px' }}>
                      <span style={{ padding: '2px 8px', borderRadius: 20, fontSize: 11, fontWeight: 600, background: `${phaseColors[r.phase] || C.blue}22`, color: phaseColors[r.phase] || C.blue }}>
                        {r.phase}
                      </span>
                    </td>
                    <td style={{ padding: '8px 10px', color: r.confidence >= 0.7 ? C.green : C.orange, fontWeight: 600 }}>
                      {r.confidence > 0 ? r.confidence.toFixed(2) : '—'}
                    </td>
                    <td style={{ padding: '8px 10px', color: C.text, maxWidth: 320, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {r.root_cause || <span style={{ color: C.sub }}>—</span>}
                    </td>
                    <td style={{ padding: '8px 10px', color: C.sub }}>
                      {new Date(r.created_at).toLocaleString()}
                    </td>
                  </tr>
                )
              })}
              {(!data?.recent_investigations?.length) && (
                <tr><td colSpan={4} style={{ padding: '20px 10px', textAlign: 'center', color: C.sub }}>No investigations in this period</td></tr>
              )}
            </tbody>
          </table>
        </div>
      </Card>
    </div>
  )
}

// ─── SLO Performance Tab ─────────────────────────────────────────────────────

const SLOTab: React.FC<{ timeRange: string }> = ({ timeRange }) => {
  const [data, setData] = useState<any>(null)
  const [loading, setLoading] = useState(true)

  const mttrRef = useRef<HTMLCanvasElement>(null)
  const resRef = useRef<HTMLCanvasElement>(null)
  const mttrChart = useRef<Chart | null>(null)
  const resChart = useRef<Chart | null>(null)

  useEffect(() => {
    setLoading(true)
    apiFetch(`/api/v1/analytics/slo?time_range=${timeRange}`)
      .then(d => { setData(d); setLoading(false) })
      .catch(() => setLoading(false))
  }, [timeRange])

  useEffect(() => {
    if (!data) return
    const cleanup = () => { destroyChart(mttrChart); destroyChart(resChart) }
    cleanup()

    if (mttrRef.current) {
      mttrChart.current = new Chart(mttrRef.current, {
        type: 'line',
        data: {
          labels: (data.mttr_trend || []).map((b: any) => b.date),
          datasets: [
            { label: 'Actual MTTR (h)', data: (data.mttr_trend || []).map((b: any) => b.mttr_hours?.toFixed(2)), borderColor: C.orange, backgroundColor: `${C.orange}20`, tension: 0.4, fill: true, pointRadius: 3 },
          ],
        },
        options: { ...chartDefaults, plugins: { legend: { display: false } } },
      })
    }

    if (resRef.current) {
      resChart.current = new Chart(resRef.current, {
        type: 'line',
        data: {
          labels: (data.resolution_trend || []).map((b: any) => b.date),
          datasets: [
            { label: 'Resolution Rate %', data: (data.resolution_trend || []).map((b: any) => b.resolution_rate), borderColor: C.green, backgroundColor: `${C.green}20`, tension: 0.4, fill: true, pointRadius: 3 },
            { label: 'Target (95%)', data: (data.resolution_trend || []).map(() => 95), borderColor: C.red, borderDash: [5, 5], pointRadius: 0 },
          ],
        },
        options: { ...chartDefaults },
      })
    }

    return cleanup
  }, [data])

  if (loading) return <Skeleton height={500} />

  const fmt = (n: number, dec = 1) => n?.toFixed(dec) ?? '—'
  const overall = data?.overall_compliance_pct || 0

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      {/* Overall SLO score */}
      <Card>
        <div style={{ display: 'flex', alignItems: 'center', gap: 24 }}>
          <div style={{ textAlign: 'center', flexShrink: 0 }}>
            <div style={{ fontSize: 56, fontWeight: 700, letterSpacing: '-0.03em', color: overall >= 90 ? C.green : overall >= 75 ? C.orange : C.red, lineHeight: 1 }}>
              {fmt(overall)}%
            </div>
            <div style={{ fontSize: 13, color: C.sub, marginTop: 4 }}>Overall SLO Compliance</div>
          </div>
          <div style={{ flex: 1, display: 'grid', gridTemplateColumns: 'repeat(3,1fr)', gap: 14 }}>
            <KPI label="MTTR Compliance" value={`${fmt(data?.mttr_compliance_pct)}%`} color={data?.mttr_compliance_pct >= 85 ? C.green : C.orange} />
            <KPI label="MTTA Compliance" value={`${fmt(data?.mtta_compliance_pct)}%`} color={data?.mtta_compliance_pct >= 85 ? C.green : C.orange} />
            <KPI label="Resolution SLO" value={`${fmt(data?.resolution_slo_pct)}%`} sub="Target ≥ 95%" color={data?.resolution_slo_pct >= 95 ? C.green : C.red} />
          </div>
        </div>
      </Card>

      {/* SLO targets table */}
      <Card>
        <CardTitle icon={Shield} color={C.green} title="SLO Targets by Severity" />
        <div style={{ overflowX: 'auto' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
            <thead>
              <tr style={{ borderBottom: `1px solid ${C.sep}` }}>
                {['Severity', 'MTTR Target', 'Actual MTTR', 'MTTA Target', 'Actual MTTA', 'Compliance'].map(h => (
                  <th key={h} style={{ textAlign: 'left', padding: '6px 12px', color: C.sub, fontWeight: 500 }}>{h}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {(data?.slo_targets || []).map((t: any) => {
                const ok = t.compliance_pct >= 90
                const warn = t.compliance_pct >= 70
                const compColor = ok ? C.green : warn ? C.orange : C.red
                const mttrOk = t.actual_mttr <= t.mttr_target_hours || t.actual_mttr === 0
                const mttaOk = t.actual_mtta <= t.mtta_target_hours || t.actual_mtta === 0
                return (
                  <tr key={t.severity} style={{ borderBottom: `0.5px solid ${C.sep}` }}>
                    <td style={{ padding: '10px 12px' }}>
                      <span style={{ padding: '2px 10px', borderRadius: 20, fontSize: 12, fontWeight: 600, background: `${SEV_COLORS[t.severity] || C.blue}22`, color: SEV_COLORS[t.severity] || C.blue, textTransform: 'capitalize' }}>{t.severity}</span>
                    </td>
                    <td style={{ padding: '10px 12px', color: C.sub }}>{t.mttr_target_hours}h</td>
                    <td style={{ padding: '10px 12px', color: mttrOk ? C.green : C.red, fontWeight: 600 }}>
                      {t.actual_mttr > 0 ? `${t.actual_mttr.toFixed(1)}h` : '—'}
                      {t.actual_mttr > 0 && (mttrOk ? <CheckCircle style={{ width: 12, height: 12, display: 'inline', marginLeft: 4 }} /> : <XCircle style={{ width: 12, height: 12, display: 'inline', marginLeft: 4 }} />)}
                    </td>
                    <td style={{ padding: '10px 12px', color: C.sub }}>{t.mtta_target_hours}h</td>
                    <td style={{ padding: '10px 12px', color: mttaOk ? C.green : C.red, fontWeight: 600 }}>
                      {t.actual_mtta > 0 ? `${t.actual_mtta.toFixed(1)}h` : '—'}
                    </td>
                    <td style={{ padding: '10px 12px' }}>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                        <div style={{ width: 80, height: 6, borderRadius: 3, background: C.fill }}>
                          <div style={{ width: `${Math.min(t.compliance_pct, 100)}%`, height: '100%', borderRadius: 3, background: compColor }} />
                        </div>
                        <span style={{ fontSize: 12, fontWeight: 600, color: compColor }}>{t.compliance_pct.toFixed(0)}%</span>
                      </div>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      </Card>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 20 }}>
        <Card>
          <CardTitle icon={Clock} color={C.orange} title="MTTR Trend" />
          <div style={{ height: 240 }}><canvas ref={mttrRef} /></div>
        </Card>
        <Card>
          <CardTitle icon={CheckCircle} color={C.green} title="Resolution Rate vs 95% Target" />
          <div style={{ height: 240 }}><canvas ref={resRef} /></div>
        </Card>
      </div>
    </div>
  )
}

// ─── Export helper ───────────────────────────────────────────────────────────

const exportCSV = (tab: TabId, data: any) => {
  if (!data) return
  const date = new Date().toISOString().split('T')[0]
  let csv = ''
  if (tab === 'overview') {
    csv = `Metric,Value\nTotal Alerts,${data.total_alerts}\nOpen Alerts,${data.open_alerts}\nCritical Alerts,${data.critical_alerts}\nTotal Incidents,${data.total_incidents}\nOpen Incidents,${data.open_incidents}\nMTTR (hours),${data.mttr_hours?.toFixed(2)}\nMTTA (hours),${data.mtta_hours?.toFixed(2)}\nResolution Rate,${data.resolution_rate?.toFixed(1)}%\nNoise Reduction,${data.noise_reduction_pct?.toFixed(1)}%\nRCA Completion Rate,${data.rca_completion_rate?.toFixed(1)}%\n`
  } else {
    csv = `Data exported at,${new Date().toISOString()}\n${JSON.stringify(data)}`
  }
  const blob = new Blob([csv], { type: 'text/csv' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url; a.download = `alerthub-analytics-${tab}-${date}.csv`
  document.body.appendChild(a); a.click()
  document.body.removeChild(a); URL.revokeObjectURL(url)
}

// ─── Main AnalyticsPage ───────────────────────────────────────────────────────

export function AnalyticsPage() {
  const [activeTab, setActiveTab] = useState<TabId>('overview')
  const [timeRange, setTimeRange] = useState('30d')
  const [refreshKey, setRefreshKey] = useState(0)
  const [exportData, setExportData] = useState<any>(null)

  const refresh = () => setRefreshKey(k => k + 1)
  const currentTab = TABS.find(t => t.id === activeTab)!

  return (
    <div style={{ minHeight: '100vh', background: C.bg }}>
      <div style={{ display: 'flex', maxWidth: 1280, margin: '0 auto', padding: '24px 16px', gap: 28, minHeight: '100vh' }}>
        {/* Sidebar */}
        <div style={{ position: 'sticky', top: 24, alignSelf: 'flex-start' }}>
          <div style={{ fontSize: 26, fontWeight: 700, color: C.text, padding: '4px 10px 20px', letterSpacing: '-0.02em' }}>
            Analytics
          </div>
          <Sidebar active={activeTab} onSelect={(id) => { setActiveTab(id); setRefreshKey(k => k + 1) }} />
        </div>

        {/* Content */}
        <div style={{ flex: 1, minWidth: 0 }}>
          {/* Header bar */}
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 24 }}>
            <div>
              <h1 style={{ fontSize: 20, fontWeight: 700, color: C.text, margin: 0 }}>{currentTab.label}</h1>
              <p style={{ fontSize: 13, color: C.sub, marginTop: 2 }}>Live data from PostgreSQL · refreshed on demand</p>
            </div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
              <TimeRangePicker value={timeRange} onChange={v => { setTimeRange(v); setRefreshKey(k => k + 1) }} />
              <button onClick={refresh} style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '7px 12px', borderRadius: C.r.sm, border: `0.5px solid ${C.sep}`, background: C.fill, color: C.text, fontSize: 13, fontWeight: 500, cursor: 'pointer' }}>
                <RefreshCw style={{ width: 14, height: 14 }} />Refresh
              </button>
              <button onClick={() => exportCSV(activeTab, exportData)} style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '7px 12px', borderRadius: C.r.sm, border: 'none', background: C.blue, color: '#fff', fontSize: 13, fontWeight: 500, cursor: 'pointer' }}>
                <Download style={{ width: 14, height: 14 }} />Export
              </button>
            </div>
          </div>

          {/* Tab content */}
          <AnimatePresence mode="wait">
            <motion.div key={`${activeTab}-${timeRange}-${refreshKey}`} initial={{ opacity: 0, y: 6 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0 }} transition={{ duration: 0.18 }}>
              {activeTab === 'overview' && <OverviewTab key={refreshKey} timeRange={timeRange} />}
              {activeTab === 'alerts' && <AlertAnalyticsTab key={refreshKey} timeRange={timeRange} />}
              {activeTab === 'incidents' && <IncidentAnalyticsTab key={refreshKey} timeRange={timeRange} />}
              {activeTab === 'correlation' && <CorrelationTab key={refreshKey} timeRange={timeRange} />}
              {activeTab === 'rca' && <RCAInsightsTab key={refreshKey} timeRange={timeRange} />}
              {activeTab === 'slo' && <SLOTab key={refreshKey} timeRange={timeRange} />}
            </motion.div>
          </AnimatePresence>
        </div>
      </div>

      <style>{`@keyframes pulse { 0%,100% { opacity: 1 } 50% { opacity: 0.4 } }`}</style>
    </div>
  )
}
