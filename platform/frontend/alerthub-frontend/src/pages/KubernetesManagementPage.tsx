import React, { useState, useEffect, useCallback, useRef, useMemo } from 'react'
import { motion, AnimatePresence } from 'framer-motion'
import {
  RefreshCw, Cloud, Activity, Search, ChevronDown, CheckCircle, AlertTriangle,
  XCircle, Server, Layers, Globe, GitBranch, Terminal, Copy, ClipboardCheck,
  Filter, Box, Network, Cpu, HardDrive, Clock, ChevronRight, ChevronUp,
  Info, AlertCircle, Loader2, Shield, Tag, Zap, FileText, Wrench, Play,
} from 'lucide-react'

// ─── Aileron Design Tokens ──────────────────────────────────────────────────────
const tokens = {
  blue: '#007AFF',
  green: '#34C759',
  red: '#FF3B30',
  orange: '#FF9500',
  yellow: '#FFCC00',
  purple: '#AF52DE',
  pink: '#FF2D55',
  gray: '#8E8E93',
  teal: '#5AC8FA',
  indigo: '#5856D6',
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

interface K8sPVC {
  name: string; namespace: string; status: string
  volume_name: string; requested_gb: number; capacity_gb: number
  access_modes: string[]; storage_class: string; volume_mode: string
}
interface K8sPV {
  name: string; capacity_gb: number; access_modes: string[]
  reclaim_policy: string; status: string; storage_class: string
  claim_ref: string; volume_mode: string
}
interface K8sContainerPort { name: string; container_port: number; protocol: string }
interface K8sContainer { name: string; image: string; ports: K8sContainerPort[]; ready: boolean }
interface K8sServicePort { name: string; port: number; target_port: string; protocol: string }

interface K8sPod {
  name: string; namespace: string; phase: string; labels: Record<string, string>
  containers: K8sContainer[]; ready: boolean; restarts: number; node_name: string
  created_at?: string
}
interface K8sService {
  name: string; namespace: string; type: string; cluster_ip: string
  ports: K8sServicePort[]; selector: Record<string, string>; labels: Record<string, string>
  created_at?: string
}
interface K8sDeployment {
  name: string; namespace: string; replicas: number; ready_replicas: number
  labels: Record<string, string>; selector: Record<string, string>
  containers: K8sContainer[]; created_at?: string
}
interface K8sIngressPath { path: string; service_name: string; service_port: number }
interface K8sIngressRule { host: string; paths: K8sIngressPath[] }
interface K8sIngressTLS { hosts: string[]; secret_name: string }
interface K8sIngress {
  name: string; namespace: string; rules: K8sIngressRule[]
  tls: K8sIngressTLS[]; labels: Record<string, string>; created_at?: string
}
interface K8sNode {
  name: string; status: string; roles: string[]; version: string; os: string
  labels: Record<string, string>; ready: boolean; schedulable: boolean
}
interface K8sNamespace { name: string; status: string; labels: Record<string, string> }
interface ClusterSummary {
  cluster: string; nodes: number; ready_nodes: number; namespaces: number
  pods: number; running_pods: number; deployments: number; healthy_deployments: number
  services: number; ingresses: number
  total_pvcs: number; bound_pvcs: number; pending_pvcs: number
  total_storage_gb: number; storage_health: string
}
interface TopologyData {
  cluster: string; timestamp: string
  nodes: K8sNode[]; namespaces: K8sNamespace[]
  pods: K8sPod[]; services: K8sService[]
  deployments: K8sDeployment[]; ingresses: K8sIngress[]
  persistent_volumes: K8sPV[]
  persistent_volume_claims: K8sPVC[]
  summary: ClusterSummary
}
interface K8sEvent {
  type: string; reason: string; message: string
  involved_object: { kind: string; name: string; namespace?: string }
  last_timestamp: string; count?: number
}
interface ClusterListItem { name: string; environment: string; region: string; enabled: boolean }

interface SearchResult {
  kind: 'Pod' | 'Service' | 'Deployment' | 'Ingress' | 'Node' | 'Namespace' | 'Event'
  name: string; namespace?: string; status?: string; extra?: string
  cluster: string
  tab: TabKey; nsFilter?: string; totalInCategory?: number
}

interface DiagIssue {
  severity: 'error' | 'warning' | 'info'
  title: string; detail: string; fix?: string
}

interface SelectedResource {
  kind: 'Pod' | 'Service' | 'Deployment' | 'Node' | 'Ingress'
  name: string
  namespace?: string
  cluster: string
}

type TabKey = 'overview' | 'workloads' | 'network' | 'topology' | 'nodes' | 'storage' | 'events' | 'troubleshoot'
type TroubleshootSection = 'ns-scan' | 'pod-logs' | 'kubectl' | 'checklist'

// ─── Helpers ──────────────────────────────────────────────────────────────────
const authHeader = () => ({
  'Authorization': `Bearer ${sessionStorage.getItem('access_token') || localStorage.getItem('access_token') || ''}`,
  'Content-Type': 'application/json',
})

function normalizeTopology(raw: any): TopologyData {
  return {
    cluster: raw.cluster ?? '',
    timestamp: raw.timestamp ?? '',
    nodes: raw.nodes ?? [],
    namespaces: raw.namespaces ?? [],
    pods: (raw.pods ?? []).map((p: any) => ({ ...p, containers: p.containers ?? [], labels: p.labels ?? {} })),
    services: (raw.services ?? []).map((s: any) => ({ ...s, ports: s.ports ?? [], selector: s.selector ?? {}, labels: s.labels ?? {} })),
    deployments: (raw.deployments ?? []).map((d: any) => ({ ...d, containers: d.containers ?? [], selector: d.selector ?? {}, labels: d.labels ?? {} })),
    ingresses: (raw.ingresses ?? []).map((i: any) => ({ ...i, rules: i.rules ?? [], tls: i.tls ?? [], labels: i.labels ?? {} })),
    persistent_volumes: (raw.persistent_volumes ?? []).map((pv: any) => ({ ...pv, access_modes: pv.access_modes ?? [] })),
    persistent_volume_claims: (raw.persistent_volume_claims ?? []).map((pvc: any) => ({ ...pvc, access_modes: pvc.access_modes ?? [] })),
    summary: {
      cluster: raw.summary?.cluster ?? '',
      nodes: raw.summary?.nodes ?? 0,
      ready_nodes: raw.summary?.ready_nodes ?? 0,
      namespaces: raw.summary?.namespaces ?? 0,
      pods: raw.summary?.pods ?? 0,
      running_pods: raw.summary?.running_pods ?? 0,
      deployments: raw.summary?.deployments ?? 0,
      healthy_deployments: raw.summary?.healthy_deployments ?? 0,
      services: raw.summary?.services ?? 0,
      ingresses: raw.summary?.ingresses ?? 0,
      total_pvcs: raw.summary?.total_pvcs ?? 0,
      bound_pvcs: raw.summary?.bound_pvcs ?? 0,
      pending_pvcs: raw.summary?.pending_pvcs ?? 0,
      total_storage_gb: raw.summary?.total_storage_gb ?? 0,
      storage_health: raw.summary?.storage_health ?? 'healthy',
    },
  }
}

function relativeAge(ts?: string): string {
  if (!ts) return 'Unknown'
  const d = new Date(ts)
  if (isNaN(d.getTime())) return 'Unknown'
  const secs = Math.floor((Date.now() - d.getTime()) / 1000)
  if (secs < 60) return `${secs}s`
  if (secs < 3600) return `${Math.floor(secs / 60)}m`
  if (secs < 86400) return `${Math.floor(secs / 3600)}h`
  return `${Math.floor(secs / 86400)}d`
}

function labelsMatch(selector: Record<string, string>, labels: Record<string, string>): boolean {
  if (!selector || Object.keys(selector).length === 0) return false
  return Object.entries(selector).every(([k, v]) => labels?.[k] === v)
}

function truncate(s: string, n: number) { return s.length > n ? s.slice(0, n) + '…' : s }

function getNsHealthScore(pods: K8sPod[], deps: K8sDeployment[]): { score: number; grade: 'A' | 'B' | 'C' | 'D' | 'F'; color: string } {
  if (pods.length === 0 && deps.length === 0) return { score: 100, grade: 'A', color: tokens.green }
  let total = 0, healthy = 0
  pods.forEach(p => { total++; if (p.phase === 'Running' && p.ready && p.restarts < 5) healthy++ })
  deps.forEach(d => { total++; if (d.ready_replicas >= d.replicas && d.replicas > 0) healthy++ })
  const score = total === 0 ? 100 : Math.round((healthy / total) * 100)
  const grade = score >= 95 ? 'A' : score >= 80 ? 'B' : score >= 60 ? 'C' : score >= 40 ? 'D' : 'F'
  const color = score >= 95 ? tokens.green : score >= 80 ? tokens.blue : score >= 60 ? tokens.orange : tokens.red
  return { score, grade, color }
}

function autoDiagnose(pod: K8sPod): DiagIssue[] {
  const issues: DiagIssue[] = []
  if (pod.phase === 'Pending') {
    issues.push({ severity: 'warning', title: 'Pod is Pending', detail: 'Pod has not been scheduled. Check for insufficient resources, node affinity, taints/tolerations, or PVC binding issues.', fix: `kubectl describe pod ${pod.name} -n ${pod.namespace}` })
  }
  if (pod.restarts >= 10) {
    issues.push({ severity: 'error', title: `CrashLoopBackOff: ${pod.restarts} restarts`, detail: 'Pod is repeatedly crashing. Inspect application logs from the previous container instance.', fix: `kubectl logs ${pod.name} -n ${pod.namespace} --previous` })
  } else if (pod.restarts >= 3) {
    issues.push({ severity: 'warning', title: `High restart count: ${pod.restarts}`, detail: 'Pod has restarted multiple times. May indicate intermittent crashes or resource pressure.', fix: `kubectl logs ${pod.name} -n ${pod.namespace}` })
  }
  if (!pod.ready && pod.phase === 'Running') {
    issues.push({ severity: 'warning', title: 'Running but Not Ready', detail: 'Readiness probe is failing. Service traffic will not be routed to this pod.', fix: `kubectl describe pod ${pod.name} -n ${pod.namespace} | grep -A20 Conditions` })
  }
  const notReadyCtrs = pod.containers?.filter(c => !c.ready) ?? []
  if (notReadyCtrs.length > 0 && pod.phase === 'Running') {
    issues.push({ severity: 'warning', title: `${notReadyCtrs.length} container(s) not ready`, detail: `Unready containers: ${notReadyCtrs.map(c => c.name).join(', ')}`, fix: `kubectl logs ${pod.name} -n ${pod.namespace} -c ${notReadyCtrs[0]?.name ?? ''}` })
  }
  if (pod.phase === 'Failed') {
    issues.push({ severity: 'error', title: 'Pod Failed', detail: 'Pod has terminated in a failed state.', fix: `kubectl describe pod ${pod.name} -n ${pod.namespace}` })
  }
  if (issues.length === 0) {
    issues.push({ severity: 'info', title: 'Pod appears healthy', detail: `Running and ready with ${pod.restarts} restart(s).` })
  }
  return issues
}

function getServicesWithNoEndpoints(services: K8sService[], pods: K8sPod[]): K8sService[] {
  return services.filter(svc => {
    if (!svc.selector || Object.keys(svc.selector).length === 0) return false
    return !pods.some(p => p.namespace === svc.namespace && p.phase === 'Running' && p.ready && labelsMatch(svc.selector, p.labels))
  })
}

// ─── Small reusable UI ────────────────────────────────────────────────────────
function Badge({ color, children }: { color: string; children: React.ReactNode }) {
  return (
    <span style={{ display: 'inline-block', fontSize: 11, fontWeight: 600, padding: '2px 7px', borderRadius: 4, background: color + '22', color }}>
      {children}
    </span>
  )
}

function PhaseBadge({ phase }: { phase: string }) {
  const c = phase === 'Running' ? tokens.green : phase === 'Pending' ? tokens.orange : phase === 'Succeeded' ? tokens.blue : tokens.red
  return <Badge color={c}>{phase}</Badge>
}

function TypeBadge({ type }: { type: string }) {
  const c = type === 'LoadBalancer' ? tokens.blue : type === 'NodePort' ? tokens.orange : tokens.gray
  return <Badge color={c}>{type}</Badge>
}

function Spinner() {
  return (
    <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', padding: 48 }}>
      <Loader2 style={{ width: 28, height: 28, color: tokens.blue, animation: 'spin 1s linear infinite' }} />
    </div>
  )
}

function EmptyState({ icon: Icon, text }: { icon: React.ComponentType<any>; text: string }) {
  return (
    <div style={{ textAlign: 'center', padding: '48px 24px' }}>
      <Icon style={{ width: 40, height: 40, color: tokens.gray, marginBottom: 12 }} />
      <p style={{ fontSize: 14, color: tokens.tertiaryLabel, margin: 0 }}>{text}</p>
    </div>
  )
}

function ShowMoreFooter({ shown, total, onShowMore, onShowAll, onCollapse, increment = 50 }: {
  shown: number; total: number
  onShowMore: () => void; onShowAll: () => void; onCollapse?: () => void
  increment?: number
}) {
  const remaining = total - shown
  if (remaining <= 0) {
    if (!onCollapse) return null
    return (
      <div style={{ padding: '10px 18px', borderTop: `0.5px solid ${tokens.separator}`, display: 'flex', justifyContent: 'center' }}>
        <button onClick={onCollapse} style={{ padding: '4px 12px', fontSize: 12, borderRadius: tokens.radius.sm, border: `0.5px solid ${tokens.separator}`, background: tokens.fill, color: tokens.gray, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 5 }}>
          <ChevronUp style={{ width: 11, height: 11 }} /> Collapse
        </button>
      </div>
    )
  }
  return (
    <div style={{ padding: '10px 18px', borderTop: `0.5px solid ${tokens.separator}`, display: 'flex', gap: 8, justifyContent: 'center', alignItems: 'center' }}>
      <button onClick={onShowMore} style={{ padding: '5px 14px', fontSize: 12, borderRadius: tokens.radius.sm, border: `0.5px solid ${tokens.blue}44`, background: tokens.blue + '12', color: tokens.blue, cursor: 'pointer', fontWeight: 500, display: 'flex', alignItems: 'center', gap: 5 }}>
        <ChevronDown style={{ width: 11, height: 11 }} />
        Show {Math.min(increment, remaining)} more
      </button>
      {remaining > increment && (
        <button onClick={onShowAll} style={{ padding: '5px 14px', fontSize: 12, borderRadius: tokens.radius.sm, border: `0.5px solid ${tokens.separator}`, background: tokens.fill, color: tokens.label, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 5 }}>
          Show all {remaining} remaining
        </button>
      )}
      <span style={{ fontSize: 11, color: tokens.tertiaryLabel }}>Showing {shown} of {total}</span>
    </div>
  )
}

function StatCard({ label, value, sub, color, icon: Icon }: {
  label: string; value: number | string; sub?: string; color: string; icon: React.ComponentType<any>
}) {
  return (
    <div style={{ background: tokens.secondaryBackground, border: `0.5px solid ${tokens.separator}`, borderRadius: tokens.radius.lg, padding: '16px 18px', display: 'flex', flexDirection: 'column', gap: 6 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <div style={{ width: 32, height: 32, borderRadius: tokens.radius.sm, background: color + '18', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
          <Icon style={{ width: 16, height: 16, color }} />
        </div>
        <span style={{ fontSize: 12, color: tokens.tertiaryLabel, fontWeight: 500 }}>{label}</span>
      </div>
      <div style={{ fontSize: 26, fontWeight: 700, color: tokens.label, lineHeight: 1 }}>{value}</div>
      {sub && <div style={{ fontSize: 12, color: tokens.secondaryLabel }}>{sub}</div>}
    </div>
  )
}

function SearchInput({ value, onChange, placeholder }: { value: string; onChange: (v: string) => void; placeholder: string }) {
  return (
    <div style={{ position: 'relative', marginBottom: 12 }}>
      <Search style={{ position: 'absolute', left: 10, top: '50%', transform: 'translateY(-50%)', width: 14, height: 14, color: tokens.gray }} />
      <input type="text" value={value} onChange={e => onChange(e.target.value)} placeholder={placeholder}
        style={{ width: '100%', padding: '7px 12px 7px 32px', boxSizing: 'border-box', borderRadius: tokens.radius.sm, border: `0.5px solid ${tokens.separator}`, background: tokens.fill, color: tokens.label, fontSize: 13, outline: 'none' }}
      />
    </div>
  )
}

// ─── Kubectl Command Panel ─────────────────────────────────────────────────────
function KubectlPanel({ pod, namespace, clusterContext }: { pod?: K8sPod; namespace?: string; clusterContext?: string }) {
  const [copied, setCopied] = useState<string | null>(null)
  const copy = (cmd: string, key: string) => {
    navigator.clipboard.writeText(cmd).then(() => { setCopied(key); setTimeout(() => setCopied(null), 2000) })
  }

  const ns = pod?.namespace ?? namespace ?? ''
  const podName = pod?.name ?? ''
  const ctx = clusterContext ? `--context ${clusterContext} ` : ''

  const cmds: { key: string; label: string; cmd: string; color?: string }[] = pod ? [
    { key: 'logs', label: 'Logs', cmd: `kubectl ${ctx}logs ${podName} -n ${ns} --tail=200` },
    { key: 'logs-f', label: 'Logs (follow)', cmd: `kubectl ${ctx}logs -f ${podName} -n ${ns}` },
    { key: 'prev', label: 'Previous logs', cmd: `kubectl ${ctx}logs ${podName} -n ${ns} --previous`, color: tokens.orange },
    { key: 'describe', label: 'Describe', cmd: `kubectl ${ctx}describe pod ${podName} -n ${ns}` },
    { key: 'events', label: 'Pod events', cmd: `kubectl ${ctx}get events -n ${ns} --field-selector involvedObject.name=${podName} --sort-by=.lastTimestamp` },
    { key: 'exec', label: 'Exec shell', cmd: `kubectl ${ctx}exec -it ${podName} -n ${ns} -- /bin/sh`, color: tokens.purple },
    { key: 'delete', label: 'Delete pod', cmd: `kubectl ${ctx}delete pod ${podName} -n ${ns}`, color: tokens.red },
    { key: 'json', label: 'Get JSON', cmd: `kubectl ${ctx}get pod ${podName} -n ${ns} -o json` },
  ] : [
    { key: 'pods', label: 'List pods', cmd: `kubectl ${ctx}get pods -n ${ns} -o wide` },
    { key: 'events', label: 'NS events', cmd: `kubectl ${ctx}get events -n ${ns} --sort-by=.lastTimestamp` },
    { key: 'warn-events', label: 'Warnings', cmd: `kubectl ${ctx}get events -n ${ns} --field-selector type=Warning --sort-by=.lastTimestamp` },
    { key: 'svcs', label: 'Services', cmd: `kubectl ${ctx}get svc -n ${ns} -o wide` },
    { key: 'deps', label: 'Deployments', cmd: `kubectl ${ctx}get deployments -n ${ns}` },
    { key: 'rs', label: 'ReplicaSets', cmd: `kubectl ${ctx}get rs -n ${ns}` },
    { key: 'quota', label: 'Resource quota', cmd: `kubectl ${ctx}get resourcequota -n ${ns}` },
    { key: 'hpa', label: 'HPA status', cmd: `kubectl ${ctx}get hpa -n ${ns}` },
    { key: 'pvc', label: 'PVC status', cmd: `kubectl ${ctx}get pvc -n ${ns}` },
    { key: 'cm', label: 'ConfigMaps', cmd: `kubectl ${ctx}get cm -n ${ns}` },
    { key: 'secrets', label: 'Secrets', cmd: `kubectl ${ctx}get secrets -n ${ns}` },
    { key: 'sa', label: 'ServiceAccounts', cmd: `kubectl ${ctx}get sa -n ${ns}` },
    { key: 'rollout-status', label: 'Rollout status', cmd: `kubectl ${ctx}rollout status deployment -n ${ns}` },
    { key: 'top-pods', label: 'Top pods', cmd: `kubectl ${ctx}top pods -n ${ns} --sort-by=cpu` },
  ]

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
      {cmds.map(c => (
        <div key={c.key} style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '6px 10px', borderRadius: tokens.radius.sm, background: '#0d111766', border: '0.5px solid #ffffff12' }}>
          <code style={{ flex: 1, fontSize: 11, color: '#c9d1d9', fontFamily: '"SF Mono", monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {c.cmd}
          </code>
          <button onClick={() => copy(c.cmd, c.key)} style={{ flexShrink: 0, padding: '3px 8px', fontSize: 11, borderRadius: tokens.radius.sm, border: '0.5px solid #ffffff20', background: '#ffffff10', color: c.color ?? '#c9d1d9', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 3, whiteSpace: 'nowrap' }}>
            {copied === c.key ? <ClipboardCheck style={{ width: 10, height: 10, color: tokens.green }} /> : <Copy style={{ width: 10, height: 10 }} />}
            {c.label}
          </button>
        </div>
      ))}
    </div>
  )
}

// ─── Auto-Diagnosis Card ───────────────────────────────────────────────────────
function DiagnosisCard({ issue }: { issue: DiagIssue }) {
  const [copied, setCopied] = useState(false)
  const color = issue.severity === 'error' ? tokens.red : issue.severity === 'warning' ? tokens.orange : tokens.blue
  const Icon = issue.severity === 'error' ? XCircle : issue.severity === 'warning' ? AlertTriangle : CheckCircle

  return (
    <div style={{ padding: '10px 14px', borderRadius: tokens.radius.sm, border: `0.5px solid ${color}30`, background: color + '0d', marginBottom: 6 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: issue.detail ? 4 : 0 }}>
        <Icon style={{ width: 14, height: 14, color, flexShrink: 0 }} />
        <span style={{ fontSize: 13, fontWeight: 600, color }}>{issue.title}</span>
      </div>
      {issue.detail && <p style={{ margin: '4px 0 0 22px', fontSize: 12, color: tokens.secondaryLabel, lineHeight: 1.5 }}>{issue.detail}</p>}
      {issue.fix && (
        <div style={{ marginTop: 8, marginLeft: 22, display: 'flex', alignItems: 'center', gap: 6 }}>
          <code style={{ fontSize: 11, color: '#c9d1d9', fontFamily: 'monospace', background: '#0d1117', padding: '3px 8px', borderRadius: 4, flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {issue.fix}
          </code>
          <button onClick={() => { navigator.clipboard.writeText(issue.fix!); setCopied(true); setTimeout(() => setCopied(false), 2000) }}
            style={{ padding: '3px 7px', fontSize: 11, borderRadius: tokens.radius.sm, border: '0.5px solid #ffffff20', background: '#ffffff10', color: '#c9d1d9', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 3, flexShrink: 0 }}>
            {copied ? <ClipboardCheck style={{ width: 10, height: 10, color: tokens.green }} /> : <Copy style={{ width: 10, height: 10 }} />}
            Copy
          </button>
        </div>
      )}
    </div>
  )
}

// ─── Global Search ────────────────────────────────────────────────────────────
function GlobalSearch({ allTopologies, allEvents, onNavigate, busyClusters }: {
  allTopologies: Record<string, TopologyData>
  allEvents: Record<string, K8sEvent[]>
  onNavigate: (cluster: string, tab: TabKey, ns?: string) => void
  busyClusters: Set<string>
}) {
  const [query, setQuery] = useState('')
  const [open, setOpen] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [])

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault(); setOpen(true); setTimeout(() => inputRef.current?.focus(), 50)
      }
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('keydown', handler)
    return () => document.removeEventListener('keydown', handler)
  }, [])

  // Search across ALL clusters
  const results = useMemo((): SearchResult[] => {
    if (query.trim().length < 2) return []
    const q = query.toLowerCase()
    const res: SearchResult[] = []

    for (const [clusterName, topo] of Object.entries(allTopologies)) {
      const podMatches = topo.pods.filter(p =>
        p.name.toLowerCase().includes(q) || p.namespace.toLowerCase().includes(q) ||
        p.node_name?.toLowerCase().includes(q) || p.phase.toLowerCase().includes(q) ||
        Object.entries(p.labels ?? {}).some(([k, v]) => `${k}=${v}`.toLowerCase().includes(q))
      )
      podMatches.slice(0, 5).forEach(p => res.push({ kind: 'Pod', name: p.name, namespace: p.namespace, status: p.phase, extra: p.node_name, cluster: clusterName, tab: 'workloads', nsFilter: p.namespace, totalInCategory: podMatches.length }))

      const svcMatches = topo.services.filter(s => s.name.toLowerCase().includes(q) || s.namespace.toLowerCase().includes(q))
      svcMatches.slice(0, 3).forEach(s => res.push({ kind: 'Service', name: s.name, namespace: s.namespace, status: s.type, cluster: clusterName, tab: 'network', nsFilter: s.namespace, totalInCategory: svcMatches.length }))

      const depMatches = topo.deployments.filter(d => d.name.toLowerCase().includes(q) || d.namespace.toLowerCase().includes(q))
      depMatches.slice(0, 3).forEach(d => res.push({ kind: 'Deployment', name: d.name, namespace: d.namespace, status: `${d.ready_replicas}/${d.replicas}`, cluster: clusterName, tab: 'workloads', nsFilter: d.namespace, totalInCategory: depMatches.length }))

      const ingMatches = topo.ingresses.filter(i => i.name.toLowerCase().includes(q) || i.namespace.toLowerCase().includes(q) || (i.rules ?? []).some(r => r.host?.toLowerCase().includes(q)))
      ingMatches.slice(0, 3).forEach(i => res.push({ kind: 'Ingress', name: i.name, namespace: i.namespace, cluster: clusterName, tab: 'network', nsFilter: i.namespace, totalInCategory: ingMatches.length }))

      const nodeMatches = topo.nodes.filter(n => n.name.toLowerCase().includes(q) || n.version?.toLowerCase().includes(q) || n.os?.toLowerCase().includes(q))
      nodeMatches.slice(0, 3).forEach(n => res.push({ kind: 'Node', name: n.name, status: n.status, cluster: clusterName, tab: 'nodes', totalInCategory: nodeMatches.length }))

      const nsMatches = topo.namespaces.filter(ns => ns.name.toLowerCase().includes(q))
      nsMatches.slice(0, 3).forEach(ns => res.push({ kind: 'Namespace', name: ns.name, status: ns.status, cluster: clusterName, tab: 'overview', nsFilter: ns.name, totalInCategory: nsMatches.length }))

      const clusterEvents = allEvents[clusterName] ?? []
      const evtMatches = clusterEvents.filter(e =>
        e.reason.toLowerCase().includes(q) || e.message.toLowerCase().includes(q) ||
        e.involved_object.name.toLowerCase().includes(q)
      )
      evtMatches.slice(0, 3).forEach(e => res.push({ kind: 'Event', name: e.involved_object.name, namespace: e.involved_object.namespace, status: e.type, extra: e.reason, cluster: clusterName, tab: 'events', totalInCategory: evtMatches.length }))
    }

    return res
  }, [allTopologies, allEvents, query])

  const kindColor: Record<string, string> = {
    Pod: tokens.green, Service: tokens.blue, Deployment: tokens.orange,
    Node: tokens.purple, Namespace: tokens.gray, Ingress: tokens.pink, Event: tokens.yellow,
  }

  // Group by cluster first, then kind within each cluster
  const groupedByClusters = useMemo(() => {
    const g: Record<string, Record<string, SearchResult[]>> = {}
    for (const r of results) {
      if (!g[r.cluster]) g[r.cluster] = {}
      if (!g[r.cluster][r.kind]) g[r.cluster][r.kind] = []
      g[r.cluster][r.kind].push(r)
    }
    return g
  }, [results])

  const clusterCount = Object.keys(allTopologies).length
  const busyCount = busyClusters.size
  const loadedCount = clusterCount - busyCount

  return (
    <div ref={containerRef} style={{ position: 'relative', flex: 1, maxWidth: 580 }}>
      <div style={{ position: 'relative' }}>
        <Search style={{ position: 'absolute', left: 10, top: '50%', transform: 'translateY(-50%)', width: 14, height: 14, color: tokens.gray, pointerEvents: 'none' }} />
        <input
          ref={inputRef}
          type="text"
          value={query}
          onChange={e => { setQuery(e.target.value); setOpen(true) }}
          onFocus={() => setOpen(true)}
          placeholder={clusterCount > 0 ? `Search across ${loadedCount}/${clusterCount} clusters… (⌘K)` : 'Global search… (⌘K)'}
          style={{ width: '100%', padding: '8px 32px 8px 32px', boxSizing: 'border-box', borderRadius: tokens.radius.md, border: `0.5px solid ${open && query ? tokens.blue + '80' : tokens.separator}`, background: tokens.fill, color: tokens.label, fontSize: 13, outline: 'none', transition: 'border-color 0.15s' }}
        />
        <div style={{ position: 'absolute', right: 8, top: '50%', transform: 'translateY(-50%)', display: 'flex', alignItems: 'center', gap: 4 }}>
          {busyCount > 0 && <Loader2 style={{ width: 12, height: 12, color: tokens.blue, animation: 'spin 1s linear infinite' }} />}
          {query && (
            <button onClick={() => { setQuery(''); setOpen(false) }} style={{ background: 'none', border: 'none', cursor: 'pointer', color: tokens.gray, padding: 2, display: 'flex' }}>
              <XCircle style={{ width: 13, height: 13 }} />
            </button>
          )}
        </div>
      </div>

      {open && query.length >= 2 && (
        <div style={{ position: 'absolute', top: '100%', left: 0, right: 0, zIndex: 2000, background: tokens.secondaryBackground, border: `0.5px solid ${tokens.separator}`, borderRadius: tokens.radius.md, boxShadow: '0 12px 40px rgba(0,0,0,0.22)', marginTop: 4, overflow: 'hidden', maxHeight: 560, overflowY: 'auto' }}>
          {results.length === 0 ? (
            <div style={{ padding: '24px 16px', textAlign: 'center' }}>
              <div style={{ fontSize: 13, color: tokens.tertiaryLabel }}>No resources match "{query}"</div>
              {busyCount > 0 && <div style={{ fontSize: 11, color: tokens.tertiaryLabel, marginTop: 6 }}>Still loading {busyCount} cluster(s)…</div>}
            </div>
          ) : (
            Object.entries(groupedByClusters).map(([cluster, kindMap]) => (
              <div key={cluster}>
                {/* Cluster header */}
                <div style={{ padding: '8px 14px 6px', background: tokens.fill, borderBottom: `0.5px solid ${tokens.separator}`, display: 'flex', alignItems: 'center', gap: 8, position: 'sticky', top: 0, zIndex: 1 }}>
                  <Cloud style={{ width: 12, height: 12, color: tokens.blue }} />
                  <span style={{ fontSize: 12, fontWeight: 700, color: tokens.blue }}>{cluster}</span>
                  <span style={{ fontSize: 11, color: tokens.tertiaryLabel }}>
                    {Object.values(kindMap).reduce((s, arr) => s + arr.length, 0)} results
                  </span>
                </div>
                {Object.entries(kindMap).map(([kind, items]) => (
                  <div key={kind}>
                    <div style={{ padding: '6px 14px 3px', display: 'flex', alignItems: 'center', gap: 6 }}>
                      <Badge color={kindColor[kind] ?? tokens.gray}>{kind}</Badge>
                      {items[0].totalInCategory && items[0].totalInCategory > items.length && (
                        <span style={{ fontSize: 11, color: tokens.tertiaryLabel }}>{items.length} of {items[0].totalInCategory}</span>
                      )}
                    </div>
                    {items.map((r, i) => (
                      <div key={i}
                        style={{ padding: '9px 14px 9px 24px', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 10, borderBottom: `0.5px solid ${tokens.separator}` }}
                        onClick={() => { onNavigate(r.cluster, r.tab, r.nsFilter); setOpen(false); setQuery('') }}
                        onMouseEnter={e => (e.currentTarget.style.background = tokens.fill)}
                        onMouseLeave={e => (e.currentTarget.style.background = 'transparent')}
                      >
                        <div style={{ flex: 1, minWidth: 0 }}>
                          <div style={{ fontSize: 13, fontWeight: 500, color: tokens.label, fontFamily: 'monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{r.name}</div>
                          <div style={{ fontSize: 11, color: tokens.tertiaryLabel, marginTop: 1 }}>
                            {r.namespace && <span>{r.namespace}</span>}
                            {r.extra && <span style={{ marginLeft: r.namespace ? 8 : 0 }}>{r.extra}</span>}
                          </div>
                        </div>
                        {r.status && <Badge color={r.kind === 'Pod' && r.status === 'Running' ? tokens.green : r.kind === 'Pod' && r.status !== 'Running' ? tokens.orange : tokens.gray}>{r.status}</Badge>}
                        <ChevronRight style={{ width: 12, height: 12, color: tokens.tertiaryLabel, flexShrink: 0 }} />
                      </div>
                    ))}
                  </div>
                ))}
              </div>
            ))
          )}
        </div>
      )}
    </div>
  )
}

// ─── Storage Tab ───────────────────────────────────────────────────────────────
function StorageTab({ data }: { data: TopologyData }) {
  const pvcs = data.persistent_volume_claims ?? []
  const pvs = data.persistent_volumes ?? []
  const s = data.summary
  const [nsFilter, setNsFilter] = useState('')
  const [statusFilter, setStatusFilter] = useState('')

  const filtered = pvcs.filter(p =>
    (!nsFilter || p.namespace === nsFilter) &&
    (!statusFilter || p.status === statusFilter)
  )
  const namespaces = [...new Set(pvcs.map(p => p.namespace))].sort()

  const statusColor = (st: string) => {
    if (st === 'Bound') return tokens.green
    if (st === 'Pending') return tokens.orange
    return tokens.red
  }

  const storageHealthColor = s.storage_health === 'critical' ? tokens.red : s.storage_health === 'warning' ? tokens.orange : tokens.green

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      {/* Summary cards */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(150px, 1fr))', gap: 12 }}>
        <StatCard label="Total PVCs" value={s.total_pvcs ?? 0} sub="Cluster-wide" color={tokens.blue} icon={HardDrive} />
        <StatCard label="Bound" value={s.bound_pvcs ?? 0} sub="Ready" color={tokens.green} icon={HardDrive} />
        <StatCard label="Pending" value={s.pending_pvcs ?? 0} sub={s.pending_pvcs ? 'Action needed' : 'All good'} color={(s.pending_pvcs ?? 0) > 0 ? tokens.orange : tokens.green} icon={AlertTriangle} />
        <StatCard label="Total Capacity" value={`${(s.total_storage_gb ?? 0).toFixed(1)} GB`} sub="Provisioned" color={tokens.purple} icon={HardDrive} />
        <StatCard label="PVs" value={pvs.length} sub="Cluster-wide" color={tokens.gray} icon={Layers} />
      </div>

      {/* Storage health banner */}
      {(s.pending_pvcs ?? 0) > 0 && (
        <div style={{ background: tokens.orange + '0d', border: `0.5px solid ${tokens.orange}40`, borderRadius: tokens.radius.lg, padding: '14px 18px', display: 'flex', alignItems: 'flex-start', gap: 10 }}>
          <AlertTriangle style={{ width: 16, height: 16, color: tokens.orange, flexShrink: 0, marginTop: 1 }} />
          <div>
            <div style={{ fontSize: 13, fontWeight: 600, color: tokens.orange, marginBottom: 4 }}>
              {s.pending_pvcs} PVC{(s.pending_pvcs ?? 0) > 1 ? 's' : ''} in Pending state
            </div>
            <div style={{ fontSize: 12, color: tokens.secondaryLabel }}>
              Pending PVCs block pod scheduling. Common causes: no matching StorageClass, PV capacity exhausted, or NetApp volume at capacity. Check NetApp volume utilization alerts for this cluster.
            </div>
          </div>
        </div>
      )}

      {/* PVC table */}
      <div style={{ background: tokens.secondaryBackground, border: `0.5px solid ${tokens.separator}`, borderRadius: tokens.radius.lg, overflow: 'hidden' }}>
        <div style={{ padding: '14px 18px', borderBottom: `0.5px solid ${tokens.separator}`, display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
          <HardDrive style={{ width: 16, height: 16, color: storageHealthColor }} />
          <span style={{ fontSize: 14, fontWeight: 600, color: tokens.label, flex: 1 }}>
            Persistent Volume Claims ({filtered.length})
          </span>
          <select value={nsFilter} onChange={e => setNsFilter(e.target.value)}
            style={{ padding: '4px 8px', borderRadius: 6, border: `0.5px solid ${tokens.separator}`, background: tokens.fill, color: tokens.label, fontSize: 12, cursor: 'pointer' }}>
            <option value="">All namespaces</option>
            {namespaces.map(ns => <option key={ns} value={ns}>{ns}</option>)}
          </select>
          <select value={statusFilter} onChange={e => setStatusFilter(e.target.value)}
            style={{ padding: '4px 8px', borderRadius: 6, border: `0.5px solid ${tokens.separator}`, background: tokens.fill, color: tokens.label, fontSize: 12, cursor: 'pointer' }}>
            <option value="">All statuses</option>
            <option value="Bound">Bound</option>
            <option value="Pending">Pending</option>
            <option value="Lost">Lost</option>
          </select>
        </div>

        {filtered.length === 0 ? (
          <div style={{ padding: '32px 18px', textAlign: 'center', color: tokens.tertiaryLabel, fontSize: 13 }}>
            No PVCs found
          </div>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
              <thead>
                <tr style={{ background: tokens.fill }}>
                  {['Name', 'Namespace', 'Status', 'Volume', 'Requested', 'Capacity', 'Access', 'Storage Class'].map(h => (
                    <th key={h} style={{ padding: '8px 14px', textAlign: 'left', fontWeight: 600, color: tokens.tertiaryLabel, fontSize: 11, textTransform: 'uppercase', letterSpacing: '0.04em', borderBottom: `0.5px solid ${tokens.separator}`, whiteSpace: 'nowrap' }}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {filtered.map((pvc, i) => (
                  <tr key={`${pvc.namespace}/${pvc.name}`}
                    style={{ background: i % 2 === 0 ? 'transparent' : tokens.fill + '80', borderBottom: `0.5px solid ${tokens.separator}` }}>
                    <td style={{ padding: '8px 14px', fontFamily: 'monospace', color: tokens.label, fontWeight: 500 }}>{pvc.name}</td>
                    <td style={{ padding: '8px 14px', color: tokens.secondaryLabel }}>{pvc.namespace}</td>
                    <td style={{ padding: '8px 14px' }}>
                      <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4, padding: '2px 8px', borderRadius: 4, background: statusColor(pvc.status) + '18', color: statusColor(pvc.status), fontSize: 11, fontWeight: 600 }}>
                        {pvc.status === 'Pending' && <AlertTriangle style={{ width: 10, height: 10 }} />}
                        {pvc.status}
                      </span>
                    </td>
                    <td style={{ padding: '8px 14px', fontFamily: 'monospace', color: tokens.secondaryLabel, maxWidth: 160, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{pvc.volume_name || '—'}</td>
                    <td style={{ padding: '8px 14px', color: tokens.label, fontWeight: 500 }}>{pvc.requested_gb > 0 ? `${pvc.requested_gb.toFixed(1)} GB` : '—'}</td>
                    <td style={{ padding: '8px 14px', color: pvc.capacity_gb > 0 ? tokens.label : tokens.tertiaryLabel, fontWeight: pvc.capacity_gb > 0 ? 500 : 400 }}>
                      {pvc.capacity_gb > 0 ? `${pvc.capacity_gb.toFixed(1)} GB` : '—'}
                    </td>
                    <td style={{ padding: '8px 14px', color: tokens.secondaryLabel, fontSize: 11 }}>{(pvc.access_modes ?? []).join(', ')}</td>
                    <td style={{ padding: '8px 14px', color: tokens.secondaryLabel, fontFamily: 'monospace', fontSize: 11 }}>{pvc.storage_class || '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {/* PV table */}
      {pvs.length > 0 && (
        <div style={{ background: tokens.secondaryBackground, border: `0.5px solid ${tokens.separator}`, borderRadius: tokens.radius.lg, overflow: 'hidden' }}>
          <div style={{ padding: '14px 18px', borderBottom: `0.5px solid ${tokens.separator}`, display: 'flex', alignItems: 'center', gap: 8 }}>
            <Layers style={{ width: 16, height: 16, color: tokens.purple }} />
            <span style={{ fontSize: 14, fontWeight: 600, color: tokens.label }}>Persistent Volumes ({pvs.length})</span>
          </div>
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
              <thead>
                <tr style={{ background: tokens.fill }}>
                  {['Name', 'Capacity', 'Status', 'Claim', 'Reclaim Policy', 'Storage Class', 'Access'].map(h => (
                    <th key={h} style={{ padding: '8px 14px', textAlign: 'left', fontWeight: 600, color: tokens.tertiaryLabel, fontSize: 11, textTransform: 'uppercase', letterSpacing: '0.04em', borderBottom: `0.5px solid ${tokens.separator}`, whiteSpace: 'nowrap' }}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {pvs.map((pv, i) => (
                  <tr key={pv.name} style={{ background: i % 2 === 0 ? 'transparent' : tokens.fill + '80', borderBottom: `0.5px solid ${tokens.separator}` }}>
                    <td style={{ padding: '8px 14px', fontFamily: 'monospace', color: tokens.label, fontWeight: 500 }}>{pv.name}</td>
                    <td style={{ padding: '8px 14px', color: tokens.label, fontWeight: 500 }}>{pv.capacity_gb > 0 ? `${pv.capacity_gb.toFixed(1)} GB` : '—'}</td>
                    <td style={{ padding: '8px 14px' }}>
                      <span style={{ padding: '2px 8px', borderRadius: 4, background: statusColor(pv.status) + '18', color: statusColor(pv.status), fontSize: 11, fontWeight: 600 }}>{pv.status}</span>
                    </td>
                    <td style={{ padding: '8px 14px', fontFamily: 'monospace', color: tokens.secondaryLabel, maxWidth: 180, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{pv.claim_ref || '—'}</td>
                    <td style={{ padding: '8px 14px', color: tokens.secondaryLabel }}>{pv.reclaim_policy}</td>
                    <td style={{ padding: '8px 14px', fontFamily: 'monospace', color: tokens.secondaryLabel, fontSize: 11 }}>{pv.storage_class || '—'}</td>
                    <td style={{ padding: '8px 14px', color: tokens.secondaryLabel, fontSize: 11 }}>{(pv.access_modes ?? []).join(', ')}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  )
}

// ─── Overview Tab ─────────────────────────────────────────────────────────────
function OverviewTab({ data, events, onResourceClick }: { data: TopologyData; events: K8sEvent[]; onResourceClick?: (r: SelectedResource) => void }) {
  const s = data.summary
  const [showAllUnhealthy, setShowAllUnhealthy] = useState(false)
  const unhealthyPods = data.pods.filter(p => p.phase !== 'Running' || !p.ready)
  const unhealthyDeps = data.deployments.filter(d => d.ready_replicas < d.replicas)
  const unhealthyNodes = data.nodes.filter(n => !n.ready)
  const warnEvents = events.filter(e => e.type === 'Warning').slice(0, 8)
  const deadServices = getServicesWithNoEndpoints(data.services, data.pods)

  // Top crashing pods
  const topCrashPods = [...data.pods].sort((a, b) => b.restarts - a.restarts).filter(p => p.restarts > 0).slice(0, 8)

  // Namespace health cards
  const nsHealthMap = useMemo(() => {
    const m: Record<string, ReturnType<typeof getNsHealthScore>> = {}
    Array.from(new Set(data.pods.map(p => p.namespace))).forEach(ns => {
      const nsPods = data.pods.filter(p => p.namespace === ns)
      const nsDeps = data.deployments.filter(d => d.namespace === ns)
      m[ns] = getNsHealthScore(nsPods, nsDeps)
    })
    return m
  }, [data.pods, data.deployments])

  const allUnhealthy: { key: string; label: string; badge: React.ReactNode; extraBadge: React.ReactNode; icon: React.ReactNode; resource: SelectedResource }[] = [
    ...unhealthyNodes.map(n => ({ key: n.name, label: `Node: ${n.name}`, badge: <Badge color={tokens.red}>NotReady</Badge>, extraBadge: null, icon: <XCircle style={{ width: 14, height: 14, color: tokens.red }} />, resource: { kind: 'Node' as const, name: n.name, cluster: data.cluster } })),
    ...unhealthyDeps.map(d => ({ key: `dep-${d.namespace}/${d.name}`, label: `Deployment: ${d.namespace}/${d.name}`, badge: <Badge color={tokens.orange}>{d.ready_replicas}/{d.replicas} ready</Badge>, extraBadge: null, icon: <AlertTriangle style={{ width: 14, height: 14, color: tokens.orange }} />, resource: { kind: 'Deployment' as const, name: d.name, namespace: d.namespace, cluster: data.cluster } })),
    ...unhealthyPods.map(p => ({ key: `pod-${p.namespace}/${p.name}`, label: `Pod: ${p.namespace}/${p.name}`, badge: <PhaseBadge phase={p.phase} />, extraBadge: p.restarts > 0 ? <Badge color={tokens.orange}>{p.restarts}r</Badge> : null, icon: <AlertCircle style={{ width: 14, height: 14, color: p.phase === 'Pending' ? tokens.orange : tokens.red }} />, resource: { kind: 'Pod' as const, name: p.name, namespace: p.namespace, cluster: data.cluster } })),
  ]
  const displayedUnhealthy = showAllUnhealthy ? allUnhealthy : allUnhealthy.slice(0, 10)

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      {/* Stat grid */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(160px, 1fr))', gap: 12 }}>
        <StatCard label="Nodes" value={`${s.ready_nodes}/${s.nodes}`} sub="Ready" color={s.ready_nodes === s.nodes ? tokens.green : tokens.orange} icon={Server} />
        <StatCard label="Pods" value={`${s.running_pods}/${s.pods}`} sub="Running" color={s.running_pods === s.pods ? tokens.green : tokens.orange} icon={Box} />
        <StatCard label="Deployments" value={`${s.healthy_deployments}/${s.deployments}`} sub="Healthy" color={s.healthy_deployments === s.deployments ? tokens.green : tokens.orange} icon={GitBranch} />
        <StatCard label="Services" value={s.services} sub="Total" color={tokens.blue} icon={Network} />
        <StatCard label="Ingresses" value={s.ingresses} sub="Total" color={tokens.purple} icon={Globe} />
        <StatCard label="Namespaces" value={s.namespaces} sub="Active" color={tokens.gray} icon={Layers} />
        <StatCard
          label="Storage"
          value={`${s.bound_pvcs ?? 0}/${s.total_pvcs ?? 0}`}
          sub={`PVCs Bound · ${((s.total_storage_gb ?? 0)).toFixed(1)} GB`}
          color={s.storage_health === 'critical' ? tokens.red : s.storage_health === 'warning' ? tokens.orange : tokens.green}
          icon={HardDrive}
        />
      </div>

      {/* Storage warning banner */}
      {(s.pending_pvcs ?? 0) > 0 && (
        <div style={{ background: tokens.orange + '0d', border: `0.5px solid ${tokens.orange}40`, borderRadius: tokens.radius.lg, padding: '12px 18px', display: 'flex', alignItems: 'center', gap: 10 }}>
          <AlertTriangle style={{ width: 15, height: 15, color: tokens.orange, flexShrink: 0 }} />
          <span style={{ fontSize: 13, fontWeight: 600, color: tokens.orange }}>
            {s.pending_pvcs} PVC{(s.pending_pvcs ?? 0) > 1 ? 's' : ''} pending — storage may be unavailable for affected pods.
            {' '}<span style={{ fontWeight: 400, color: tokens.secondaryLabel }}>Check the Storage tab for details.</span>
          </span>
        </div>
      )}

      {/* Dead services warning */}
      {deadServices.length > 0 && (
        <div style={{ background: tokens.red + '0d', border: `0.5px solid ${tokens.red}30`, borderRadius: tokens.radius.lg, padding: '12px 18px' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
            <AlertTriangle style={{ width: 15, height: 15, color: tokens.red }} />
            <span style={{ fontSize: 13, fontWeight: 600, color: tokens.red }}>{deadServices.length} Service(s) with No Healthy Endpoints</span>
          </div>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
            {deadServices.map(s => (
              <span key={`${s.namespace}/${s.name}`}
                onClick={() => onResourceClick?.({ kind: 'Service', name: s.name, namespace: s.namespace, cluster: data.cluster })}
                style={{ fontSize: 11, padding: '2px 8px', borderRadius: 4, background: tokens.red + '18', color: tokens.red, fontFamily: 'monospace', cursor: onResourceClick ? 'pointer' : 'default', textDecoration: onResourceClick ? 'underline dotted' : 'none' }}>
                {s.namespace}/{s.name}
              </span>
            ))}
          </div>
        </div>
      )}

      {/* Unhealthy Resources */}
      {allUnhealthy.length > 0 && (
        <div style={{ background: tokens.secondaryBackground, border: `0.5px solid ${tokens.separator}`, borderRadius: tokens.radius.lg, overflow: 'hidden' }}>
          <div style={{ padding: '14px 18px', borderBottom: `0.5px solid ${tokens.separator}`, display: 'flex', alignItems: 'center', gap: 8 }}>
            <AlertTriangle style={{ width: 16, height: 16, color: tokens.orange }} />
            <span style={{ fontSize: 14, fontWeight: 600, color: tokens.label }}>Unhealthy Resources</span>
            <Badge color={tokens.orange}>{allUnhealthy.length}</Badge>
          </div>
          <div style={{ padding: '4px 0' }}>
            {displayedUnhealthy.map(item => (
              <div key={item.key}
                onClick={() => onResourceClick?.(item.resource)}
                style={{ padding: '8px 18px', display: 'flex', alignItems: 'center', gap: 10, borderBottom: `0.5px solid ${tokens.separator}`, cursor: onResourceClick ? 'pointer' : 'default' }}
                onMouseEnter={e => { if (onResourceClick) e.currentTarget.style.background = tokens.fill }}
                onMouseLeave={e => { e.currentTarget.style.background = 'transparent' }}>
                {item.icon}
                <span style={{ fontSize: 12, color: tokens.label, flex: 1, fontFamily: 'monospace' }}>{item.label}</span>
                {item.badge}
                {item.extraBadge}
                {onResourceClick && <ChevronRight style={{ width: 12, height: 12, color: tokens.tertiaryLabel, flexShrink: 0 }} />}
              </div>
            ))}
          </div>
          {allUnhealthy.length > 10 && (
            <ShowMoreFooter shown={displayedUnhealthy.length} total={allUnhealthy.length} increment={20}
              onShowMore={() => setShowAllUnhealthy(true)} onShowAll={() => setShowAllUnhealthy(true)}
              onCollapse={() => setShowAllUnhealthy(false)} />
          )}
        </div>
      )}

      {/* Top crashing pods */}
      {topCrashPods.length > 0 && (
        <div style={{ background: tokens.secondaryBackground, border: `0.5px solid ${tokens.separator}`, borderRadius: tokens.radius.lg, overflow: 'hidden' }}>
          <div style={{ padding: '14px 18px', borderBottom: `0.5px solid ${tokens.separator}`, display: 'flex', alignItems: 'center', gap: 8 }}>
            <RefreshCw style={{ width: 16, height: 16, color: tokens.orange }} />
            <span style={{ fontSize: 14, fontWeight: 600, color: tokens.label }}>Top Restarting Pods</span>
          </div>
          <div style={{ padding: '4px 0' }}>
            {topCrashPods.map((p, i) => (
              <div key={`${p.namespace}/${p.name}`}
                onClick={() => onResourceClick?.({ kind: 'Pod', name: p.name, namespace: p.namespace, cluster: data.cluster })}
                style={{ padding: '8px 18px', display: 'grid', gridTemplateColumns: '24px 2fr 1.5fr 1fr 80px', gap: 10, alignItems: 'center', borderBottom: i < topCrashPods.length - 1 ? `0.5px solid ${tokens.separator}` : 'none', fontSize: 12, cursor: onResourceClick ? 'pointer' : 'default' }}
                onMouseEnter={e => { if (onResourceClick) e.currentTarget.style.background = tokens.fill }}
                onMouseLeave={e => { e.currentTarget.style.background = 'transparent' }}>
                <span style={{ color: tokens.tertiaryLabel, fontWeight: 600 }}>#{i + 1}</span>
                <span style={{ fontFamily: 'monospace', color: tokens.label, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{p.name}</span>
                <span style={{ color: tokens.secondaryLabel }}>{p.namespace}</span>
                <PhaseBadge phase={p.phase} />
                <Badge color={p.restarts >= 10 ? tokens.red : tokens.orange}>{p.restarts} restarts</Badge>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Namespace health grid */}
      {Object.keys(nsHealthMap).length > 0 && (
        <div style={{ background: tokens.secondaryBackground, border: `0.5px solid ${tokens.separator}`, borderRadius: tokens.radius.lg, overflow: 'hidden' }}>
          <div style={{ padding: '14px 18px', borderBottom: `0.5px solid ${tokens.separator}`, display: 'flex', alignItems: 'center', gap: 8 }}>
            <Layers style={{ width: 16, height: 16, color: tokens.blue }} />
            <span style={{ fontSize: 14, fontWeight: 600, color: tokens.label }}>Namespace Health</span>
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(200px, 1fr))', gap: 0 }}>
            {Object.entries(nsHealthMap).sort(([, a], [, b]) => a.score - b.score).map(([ns, h]) => {
              const nsPodCount = data.pods.filter(p => p.namespace === ns).length
              return (
                <div key={ns} style={{ padding: '12px 16px', borderRight: `0.5px solid ${tokens.separator}`, borderBottom: `0.5px solid ${tokens.separator}` }}>
                  <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 6 }}>
                    <span style={{ fontSize: 12, fontWeight: 600, color: tokens.label, fontFamily: 'monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: '75%' }}>{ns}</span>
                    <span style={{ fontSize: 16, fontWeight: 800, color: h.color }}>{h.grade}</span>
                  </div>
                  <div style={{ height: 4, borderRadius: 2, background: tokens.fill, overflow: 'hidden', marginBottom: 4 }}>
                    <div style={{ height: '100%', width: `${h.score}%`, background: h.color, borderRadius: 2 }} />
                  </div>
                  <span style={{ fontSize: 11, color: tokens.tertiaryLabel }}>{h.score}% healthy · {nsPodCount} pod{nsPodCount !== 1 ? 's' : ''}</span>
                </div>
              )
            })}
          </div>
        </div>
      )}

      {/* Recent warnings */}
      {warnEvents.length > 0 && (
        <div style={{ background: tokens.secondaryBackground, border: `0.5px solid ${tokens.separator}`, borderRadius: tokens.radius.lg, overflow: 'hidden' }}>
          <div style={{ padding: '14px 18px', borderBottom: `0.5px solid ${tokens.separator}`, display: 'flex', alignItems: 'center', gap: 8 }}>
            <AlertTriangle style={{ width: 16, height: 16, color: tokens.yellow }} />
            <span style={{ fontSize: 14, fontWeight: 600, color: tokens.label }}>Recent Warnings</span>
          </div>
          {warnEvents.map((ev, i) => (
            <div key={i} style={{ padding: '10px 18px', borderBottom: i < warnEvents.length - 1 ? `0.5px solid ${tokens.separator}` : 'none', display: 'grid', gridTemplateColumns: '90px 100px 130px 1fr', gap: 10, alignItems: 'start' }}>
              <span style={{ fontSize: 11, color: tokens.tertiaryLabel }}>{new Date(ev.last_timestamp).toLocaleTimeString()}</span>
              <span style={{ fontSize: 11, color: tokens.orange, fontWeight: 600 }}>{ev.reason}</span>
              <span style={{ fontSize: 11, color: tokens.secondaryLabel, fontFamily: 'monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{ev.involved_object.kind}/{truncate(ev.involved_object.name, 18)}</span>
              <span style={{ fontSize: 12, color: tokens.label }}>{truncate(ev.message, 100)}</span>
            </div>
          ))}
        </div>
      )}

      {allUnhealthy.length === 0 && deadServices.length === 0 && topCrashPods.length === 0 && (
        <div style={{ background: tokens.secondaryBackground, border: `0.5px solid ${tokens.separator}`, borderRadius: tokens.radius.lg, padding: '32px 24px', textAlign: 'center' }}>
          <CheckCircle style={{ width: 36, height: 36, color: tokens.green, marginBottom: 10 }} />
          <div style={{ fontSize: 15, fontWeight: 600, color: tokens.label }}>All resources healthy</div>
          <div style={{ fontSize: 13, color: tokens.secondaryLabel, marginTop: 4 }}>No issues detected in this cluster</div>
        </div>
      )}
    </div>
  )
}

// ─── Workloads Tab ────────────────────────────────────────────────────────────
function WorkloadsTab({ data, nsFilter, onGetLogs, onResourceClick }: { data: TopologyData; nsFilter: string; onGetLogs: (ns: string, pod: string) => void; onResourceClick?: (r: SelectedResource) => void }) {
  const [depSearch, setDepSearch] = useState('')
  const [podSearch, setPodSearch] = useState('')
  const [expandedPod, setExpandedPod] = useState<string | null>(null)
  const [phaseFilter, setPhaseFilter] = useState<'all' | 'Running' | 'Pending' | 'Failed' | 'unhealthy'>('all')
  const [podsShown, setPodsShown] = useState(15)
  const [depsShown, setDepsShown] = useState(15)

  const deps = data.deployments.filter(d =>
    (nsFilter === '' || d.namespace === nsFilter) &&
    (d.name.toLowerCase().includes(depSearch.toLowerCase()) || d.namespace.toLowerCase().includes(depSearch.toLowerCase()))
  )

  const pods = data.pods.filter(p => {
    if (nsFilter !== '' && p.namespace !== nsFilter) return false
    const q = podSearch.toLowerCase()
    if (q && !p.name.toLowerCase().includes(q) && !p.namespace.toLowerCase().includes(q) && !p.node_name?.toLowerCase().includes(q)) return false
    if (phaseFilter === 'all') return true
    if (phaseFilter === 'unhealthy') return p.phase !== 'Running' || !p.ready || p.restarts >= 3
    return p.phase === phaseFilter
  })

  // Reset shown count when filter changes
  useEffect(() => { setPodsShown(15) }, [nsFilter, podSearch, phaseFilter])
  useEffect(() => { setDepsShown(15) }, [nsFilter, depSearch])

  const phases = ['all', 'Running', 'Pending', 'Failed', 'unhealthy'] as const

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      {/* Deployments */}
      <div style={{ background: tokens.secondaryBackground, border: `0.5px solid ${tokens.separator}`, borderRadius: tokens.radius.lg, overflow: 'hidden' }}>
        <div style={{ padding: '14px 18px', borderBottom: `0.5px solid ${tokens.separator}`, display: 'flex', alignItems: 'center', gap: 8 }}>
          <GitBranch style={{ width: 16, height: 16, color: tokens.orange }} />
          <span style={{ fontSize: 14, fontWeight: 600, color: tokens.label }}>Deployments</span>
          <Badge color={tokens.gray}>{deps.length}</Badge>
        </div>
        <div style={{ padding: '12px 18px 0' }}>
          <SearchInput value={depSearch} onChange={setDepSearch} placeholder="Search deployments…" />
        </div>
        {deps.length === 0 ? <EmptyState icon={GitBranch} text="No deployments found" /> : (
          <div>
            <div style={{ display: 'grid', gridTemplateColumns: '2fr 1.5fr 80px 2fr 80px 100px', padding: '8px 18px', borderBottom: `0.5px solid ${tokens.separator}`, fontSize: 11, fontWeight: 600, color: tokens.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.04em' }}>
              <div>Name</div><div>Namespace</div><div>Ready</div><div>Image</div><div>Age</div><div>Status</div>
            </div>
            {deps.slice(0, depsShown).map((d, i) => {
              const healthy = d.ready_replicas >= d.replicas && d.replicas > 0
              const img = d.containers?.[0]?.image ?? '—'
              return (
                <div key={`${d.namespace}/${d.name}`}
                  onClick={() => onResourceClick?.({ kind: 'Deployment', name: d.name, namespace: d.namespace, cluster: data.cluster })}
                  style={{ display: 'grid', gridTemplateColumns: '2fr 1.5fr 80px 2fr 80px 100px', padding: '10px 18px', borderBottom: `0.5px solid ${tokens.separator}`, fontSize: 13, alignItems: 'center', cursor: onResourceClick ? 'pointer' : 'default' }}
                  onMouseEnter={e => { if (onResourceClick) e.currentTarget.style.background = tokens.fill }}
                  onMouseLeave={e => { e.currentTarget.style.background = 'transparent' }}>
                  <div style={{ color: tokens.label, fontWeight: 500, fontFamily: 'monospace', fontSize: 12 }}>{d.name}</div>
                  <div style={{ color: tokens.secondaryLabel }}>{d.namespace}</div>
                  <div style={{ color: healthy ? tokens.green : tokens.orange, fontWeight: 600 }}>{d.ready_replicas}/{d.replicas}</div>
                  <div style={{ color: tokens.tertiaryLabel, fontSize: 11, fontFamily: 'monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{truncate(img.split('/').pop() ?? img, 28)}</div>
                  <div style={{ color: tokens.tertiaryLabel }}>{relativeAge(d.created_at)}</div>
                  <div><Badge color={healthy ? tokens.green : tokens.orange}>{healthy ? 'Healthy' : 'Degraded'}</Badge></div>
                </div>
              )
            })}
            <ShowMoreFooter shown={Math.min(depsShown, deps.length)} total={deps.length}
              onShowMore={() => setDepsShown(n => Math.min(n + 25, deps.length))}
              onShowAll={() => setDepsShown(deps.length)}
              onCollapse={() => setDepsShown(15)} />
          </div>
        )}
      </div>

      {/* Pods */}
      <div style={{ background: tokens.secondaryBackground, border: `0.5px solid ${tokens.separator}`, borderRadius: tokens.radius.lg, overflow: 'hidden' }}>
        <div style={{ padding: '14px 18px', borderBottom: `0.5px solid ${tokens.separator}`, display: 'flex', alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap', gap: 8 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <Box style={{ width: 16, height: 16, color: tokens.green }} />
            <span style={{ fontSize: 14, fontWeight: 600, color: tokens.label }}>Pods</span>
            <Badge color={tokens.gray}>{data.pods.filter(p => nsFilter === '' || p.namespace === nsFilter).length} total</Badge>
            {phaseFilter !== 'all' && <Badge color={tokens.blue}>{pods.length} shown</Badge>}
          </div>
          {/* Phase filter pills */}
          <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap' }}>
            {phases.map(f => {
              const count = f === 'all'
                ? data.pods.filter(p => nsFilter === '' || p.namespace === nsFilter).length
                : f === 'unhealthy'
                  ? data.pods.filter(p => (nsFilter === '' || p.namespace === nsFilter) && (p.phase !== 'Running' || !p.ready || p.restarts >= 3)).length
                  : data.pods.filter(p => (nsFilter === '' || p.namespace === nsFilter) && p.phase === f).length
              return (
                <button key={f} onClick={() => setPhaseFilter(f)} style={{ padding: '3px 10px', fontSize: 11, borderRadius: 20, border: `0.5px solid ${phaseFilter === f ? tokens.blue : tokens.separator}`, background: phaseFilter === f ? tokens.blue : tokens.fill, color: phaseFilter === f ? '#fff' : tokens.secondaryLabel, cursor: 'pointer', fontWeight: 500 }}>
                  {f === 'all' ? `All ${count}` : f === 'unhealthy' ? `⚠ Unhealthy ${count}` : `${f} ${count}`}
                </button>
              )
            })}
          </div>
        </div>
        <div style={{ padding: '12px 18px 0' }}>
          <SearchInput value={podSearch} onChange={setPodSearch} placeholder="Search pods, namespaces, nodes…" />
        </div>
        {pods.length === 0 ? <EmptyState icon={Box} text="No pods found" /> : (
          <div>
            <div style={{ display: 'grid', gridTemplateColumns: '2fr 1.2fr 1.5fr 90px 70px 40px 60px 130px', padding: '8px 18px', borderBottom: `0.5px solid ${tokens.separator}`, fontSize: 11, fontWeight: 600, color: tokens.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.04em' }}>
              <div>Name</div><div>Namespace</div><div>Node</div><div>Phase</div><div>Restarts</div><div>Ctr</div><div>Age</div><div>Actions</div>
            </div>
            {pods.slice(0, podsShown).map((p) => {
              const key = `${p.namespace}/${p.name}`
              return (
                <div key={key}>
                  <div
                    style={{ display: 'grid', gridTemplateColumns: '2fr 1.2fr 1.5fr 90px 70px 40px 60px 130px', padding: '9px 18px', borderBottom: `0.5px solid ${tokens.separator}`, fontSize: 12, alignItems: 'center', cursor: 'pointer', background: expandedPod === key ? tokens.fill : 'transparent' }}
                    onClick={() => setExpandedPod(expandedPod === key ? null : key)}
                  >
                    <div style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
                      {expandedPod === key ? <ChevronUp style={{ width: 12, height: 12, color: tokens.gray }} /> : <ChevronRight style={{ width: 12, height: 12, color: tokens.gray }} />}
                      <span style={{ color: tokens.label, fontWeight: 500, fontFamily: 'monospace', fontSize: 11, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{truncate(p.name, 36)}</span>
                    </div>
                    <div style={{ color: tokens.secondaryLabel }}>{p.namespace}</div>
                    <div style={{ color: tokens.tertiaryLabel, fontSize: 11, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{truncate(p.node_name ?? '—', 22)}</div>
                    <div><PhaseBadge phase={p.phase} /></div>
                    <div style={{ color: p.restarts > 0 ? (p.restarts >= 5 ? tokens.red : tokens.orange) : tokens.tertiaryLabel, fontWeight: p.restarts > 0 ? 700 : 400 }}>{p.restarts}</div>
                    <div style={{ color: tokens.tertiaryLabel }}>{p.containers?.length ?? 0}</div>
                    <div style={{ color: tokens.tertiaryLabel }}>{relativeAge(p.created_at)}</div>
                    <div style={{ display: 'flex', gap: 4 }}>
                      <button onClick={e => { e.stopPropagation(); onGetLogs(p.namespace, p.name) }}
                        style={{ padding: '3px 7px', fontSize: 11, borderRadius: tokens.radius.sm, border: `0.5px solid ${tokens.separator}`, background: tokens.fill, color: tokens.label, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 3 }}>
                        <Terminal style={{ width: 11, height: 11 }} /> Logs
                      </button>
                      {onResourceClick && (
                        <button onClick={e => { e.stopPropagation(); onResourceClick({ kind: 'Pod', name: p.name, namespace: p.namespace, cluster: data.cluster }) }}
                          style={{ padding: '3px 7px', fontSize: 11, borderRadius: tokens.radius.sm, border: `0.5px solid ${tokens.blue}44`, background: tokens.blue + '12', color: tokens.blue, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 3 }}>
                          <Info style={{ width: 11, height: 11 }} />
                        </button>
                      )}
                    </div>
                  </div>
                  {expandedPod === key && (
                    <div style={{ background: tokens.tertiaryFill, padding: '12px 36px 14px', borderBottom: `0.5px solid ${tokens.separator}` }}>
                      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
                        <div>
                          <div style={{ fontSize: 11, color: tokens.tertiaryLabel, marginBottom: 6, textTransform: 'uppercase', letterSpacing: '0.05em' }}>Containers</div>
                          {p.containers?.map(c => (
                            <div key={c.name} style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 5 }}>
                              <Badge color={c.ready ? tokens.green : tokens.orange}>{c.ready ? '●' : '○'}</Badge>
                              <span style={{ fontSize: 11, fontFamily: 'monospace', color: tokens.label }}>{c.name}</span>
                              <span style={{ fontSize: 10, color: tokens.tertiaryLabel, fontFamily: 'monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{truncate(c.image.split('/').pop() ?? c.image, 32)}</span>
                            </div>
                          ))}
                        </div>
                        <div>
                          {p.labels && Object.keys(p.labels).length > 0 && (
                            <>
                              <div style={{ fontSize: 11, color: tokens.tertiaryLabel, marginBottom: 6, textTransform: 'uppercase', letterSpacing: '0.05em' }}>Labels</div>
                              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 3 }}>
                                {Object.entries(p.labels).slice(0, 10).map(([k, v]) => (
                                  <span key={k} style={{ fontSize: 10, padding: '2px 6px', borderRadius: 3, background: tokens.blue + '15', color: tokens.blue, fontFamily: 'monospace' }}>{k}={v}</span>
                                ))}
                              </div>
                            </>
                          )}
                          <div style={{ marginTop: 10 }}>
                            {autoDiagnose(p).map((issue, ii) => (
                              <div key={ii} style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 11, color: issue.severity === 'error' ? tokens.red : issue.severity === 'warning' ? tokens.orange : tokens.green, marginBottom: 3 }}>
                                {issue.severity === 'error' ? <XCircle style={{ width: 11, height: 11 }} /> : issue.severity === 'warning' ? <AlertTriangle style={{ width: 11, height: 11 }} /> : <CheckCircle style={{ width: 11, height: 11 }} />}
                                {issue.title}
                              </div>
                            ))}
                          </div>
                        </div>
                      </div>
                    </div>
                  )}
                </div>
              )
            })}
            <ShowMoreFooter shown={Math.min(podsShown, pods.length)} total={pods.length}
              onShowMore={() => setPodsShown(n => Math.min(n + 50, pods.length))}
              onShowAll={() => setPodsShown(pods.length)}
              onCollapse={() => setPodsShown(15)} />
          </div>
        )}
      </div>
    </div>
  )
}

// ─── Network Tab ──────────────────────────────────────────────────────────────
function NetworkTab({ data, nsFilter, onResourceClick }: { data: TopologyData; nsFilter: string; onResourceClick?: (r: SelectedResource) => void }) {
  const [svcSearch, setSvcSearch] = useState('')
  const [ingSearch, setIngSearch] = useState('')
  const [expandedIng, setExpandedIng] = useState<string | null>(null)
  const [svcsShown, setSvcsShown] = useState(20)
  const [showDeadOnly, setShowDeadOnly] = useState(false)

  const allSvcs = data.services.filter(s =>
    (nsFilter === '' || s.namespace === nsFilter) &&
    (s.name.toLowerCase().includes(svcSearch.toLowerCase()) || s.namespace.toLowerCase().includes(svcSearch.toLowerCase()))
  )
  const deadSvcIds = new Set(getServicesWithNoEndpoints(allSvcs, data.pods).map(s => `${s.namespace}/${s.name}`))
  const svcs = showDeadOnly ? allSvcs.filter(s => deadSvcIds.has(`${s.namespace}/${s.name}`)) : allSvcs

  const ings = data.ingresses.filter(ing =>
    (nsFilter === '' || ing.namespace === nsFilter) &&
    (ing.name.toLowerCase().includes(ingSearch.toLowerCase()) || ing.namespace.toLowerCase().includes(ingSearch.toLowerCase()))
  )

  const getIngressChain = (ing: K8sIngress) => {
    const chains: { host: string; path: string; svc: K8sService | undefined; pods: K8sPod[] }[] = []
    for (const rule of (ing.rules ?? [])) {
      for (const p of (rule.paths ?? [])) {
        const svc = data.services.find(s => s.name === p.service_name && s.namespace === ing.namespace)
        const pods = svc ? data.pods.filter(pod => pod.namespace === svc.namespace && labelsMatch(svc.selector, pod.labels)) : []
        chains.push({ host: rule.host, path: p.path, svc, pods })
      }
    }
    return chains
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      {/* Services */}
      <div style={{ background: tokens.secondaryBackground, border: `0.5px solid ${tokens.separator}`, borderRadius: tokens.radius.lg, overflow: 'hidden' }}>
        <div style={{ padding: '14px 18px', borderBottom: `0.5px solid ${tokens.separator}`, display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <Network style={{ width: 16, height: 16, color: tokens.blue }} />
            <span style={{ fontSize: 14, fontWeight: 600, color: tokens.label }}>Services</span>
            <Badge color={tokens.gray}>{svcs.length}</Badge>
            {deadSvcIds.size > 0 && <Badge color={tokens.red}>{deadSvcIds.size} dead endpoints</Badge>}
          </div>
          {deadSvcIds.size > 0 && (
            <button onClick={() => setShowDeadOnly(v => !v)} style={{ padding: '4px 10px', fontSize: 11, borderRadius: tokens.radius.sm, border: `0.5px solid ${showDeadOnly ? tokens.red : tokens.separator}`, background: showDeadOnly ? tokens.red + '18' : tokens.fill, color: showDeadOnly ? tokens.red : tokens.label, cursor: 'pointer', fontWeight: 500 }}>
              {showDeadOnly ? 'Show all' : 'Dead only'}
            </button>
          )}
        </div>
        <div style={{ padding: '12px 18px 0' }}>
          <SearchInput value={svcSearch} onChange={setSvcSearch} placeholder="Search services…" />
        </div>
        {svcs.length === 0 ? <EmptyState icon={Network} text="No services found" /> : (
          <div>
            <div style={{ display: 'grid', gridTemplateColumns: '2fr 1.2fr 100px 110px 1.5fr 1.5fr 60px', padding: '8px 18px', borderBottom: `0.5px solid ${tokens.separator}`, fontSize: 11, fontWeight: 600, color: tokens.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.04em' }}>
              <div>Name</div><div>Namespace</div><div>Type</div><div>Cluster IP</div><div>Ports</div><div>Selector</div><div>Age</div>
            </div>
            {svcs.slice(0, svcsShown).map((s, i) => {
              const isDead = deadSvcIds.has(`${s.namespace}/${s.name}`)
              return (
                <div key={`${s.namespace}/${s.name}`}
                  onClick={() => onResourceClick?.({ kind: 'Service', name: s.name, namespace: s.namespace, cluster: data.cluster })}
                  style={{ display: 'grid', gridTemplateColumns: '2fr 1.2fr 100px 110px 1.5fr 1.5fr 60px', padding: '10px 18px', borderBottom: `0.5px solid ${tokens.separator}`, fontSize: 12, alignItems: 'center', background: isDead ? tokens.red + '06' : 'transparent', cursor: onResourceClick ? 'pointer' : 'default' }}
                  onMouseEnter={e => { if (onResourceClick) e.currentTarget.style.background = isDead ? tokens.red + '0e' : tokens.fill }}
                  onMouseLeave={e => { e.currentTarget.style.background = isDead ? tokens.red + '06' : 'transparent' }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                    {isDead && <AlertTriangle style={{ width: 11, height: 11, color: tokens.red, flexShrink: 0 }} />}
                    <span style={{ color: isDead ? tokens.red : tokens.label, fontWeight: 500, fontFamily: 'monospace', fontSize: 11 }}>{s.name}</span>
                  </div>
                  <div style={{ color: tokens.secondaryLabel }}>{s.namespace}</div>
                  <div><TypeBadge type={s.type} /></div>
                  <div style={{ color: tokens.tertiaryLabel, fontFamily: 'monospace', fontSize: 11 }}>{s.cluster_ip}</div>
                  <div style={{ color: tokens.tertiaryLabel, fontSize: 11 }}>{(s.ports ?? []).map(p => `${p.port}/${p.protocol}`).join(', ') || '—'}</div>
                  <div style={{ display: 'flex', flexWrap: 'wrap', gap: 3 }}>
                    {Object.entries(s.selector ?? {}).slice(0, 2).map(([k, v]) => (
                      <span key={k} style={{ fontSize: 10, padding: '1px 5px', borderRadius: 3, background: tokens.blue + '15', color: tokens.blue, fontFamily: 'monospace' }}>{k}={v}</span>
                    ))}
                  </div>
                  <div style={{ color: tokens.tertiaryLabel }}>{relativeAge(s.created_at)}</div>
                </div>
              )
            })}
            <ShowMoreFooter shown={Math.min(svcsShown, svcs.length)} total={svcs.length}
              onShowMore={() => setSvcsShown(n => Math.min(n + 25, svcs.length))}
              onShowAll={() => setSvcsShown(svcs.length)}
              onCollapse={() => setSvcsShown(20)} />
          </div>
        )}
      </div>

      {/* Ingresses */}
      <div style={{ background: tokens.secondaryBackground, border: `0.5px solid ${tokens.separator}`, borderRadius: tokens.radius.lg, overflow: 'hidden' }}>
        <div style={{ padding: '14px 18px', borderBottom: `0.5px solid ${tokens.separator}`, display: 'flex', alignItems: 'center', gap: 8 }}>
          <Globe style={{ width: 16, height: 16, color: tokens.purple }} />
          <span style={{ fontSize: 14, fontWeight: 600, color: tokens.label }}>Ingresses</span>
          <Badge color={tokens.gray}>{ings.length}</Badge>
        </div>
        <div style={{ padding: '12px 18px 0' }}>
          <SearchInput value={ingSearch} onChange={setIngSearch} placeholder="Search ingresses, hosts…" />
        </div>
        {ings.length === 0 ? <EmptyState icon={Globe} text="No ingresses found" /> : (
          <div>
            {ings.map((ing, i) => {
              const hasTLS = (ing.tls ?? []).length > 0
              const key = `${ing.namespace}/${ing.name}`
              const expanded = expandedIng === key
              const chain = expanded ? getIngressChain(ing) : []
              return (
                <div key={key} style={{ borderBottom: i < ings.length - 1 ? `0.5px solid ${tokens.separator}` : 'none' }}>
                  <div style={{ padding: '12px 18px', cursor: 'pointer', display: 'grid', gridTemplateColumns: '2fr 1.2fr 2fr 80px 80px 40px', gap: 12, alignItems: 'center', fontSize: 12 }}
                    onClick={() => setExpandedIng(expanded ? null : key)}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                      {expanded ? <ChevronUp style={{ width: 12, height: 12, color: tokens.gray }} /> : <ChevronRight style={{ width: 12, height: 12, color: tokens.gray }} />}
                      <span style={{ color: tokens.label, fontWeight: 500, fontFamily: 'monospace', fontSize: 11 }}>{ing.name}</span>
                    </div>
                    <div style={{ color: tokens.secondaryLabel }}>{ing.namespace}</div>
                    <div style={{ color: tokens.tertiaryLabel, fontSize: 11 }}>{(ing.rules ?? []).map(r => r.host || '*').join(', ')}</div>
                    <div>{hasTLS && <Badge color={tokens.green}>TLS</Badge>}</div>
                    <div style={{ color: tokens.tertiaryLabel }}>{relativeAge(ing.created_at)}</div>
                    <div>
                      {onResourceClick && (
                        <button onClick={e => { e.stopPropagation(); onResourceClick({ kind: 'Ingress', name: ing.name, namespace: ing.namespace, cluster: data.cluster }) }}
                          style={{ padding: '3px 7px', fontSize: 11, borderRadius: tokens.radius.sm, border: `0.5px solid ${tokens.pink}44`, background: tokens.pink + '12', color: tokens.pink, cursor: 'pointer', display: 'flex', alignItems: 'center' }}>
                          <Info style={{ width: 11, height: 11 }} />
                        </button>
                      )}
                    </div>
                  </div>
                  {expanded && (
                    <div style={{ background: tokens.tertiaryFill, padding: '12px 36px 16px', borderTop: `0.5px solid ${tokens.separator}` }}>
                      <div style={{ fontSize: 12, fontWeight: 600, color: tokens.tertiaryLabel, marginBottom: 10, textTransform: 'uppercase', letterSpacing: '0.04em' }}>Routing Chain</div>
                      {chain.map((c, ci) => (
                        <div key={ci} style={{ marginBottom: 10 }}>
                          <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                            <Badge color={tokens.pink}>Ingress</Badge>
                            <span style={{ fontSize: 11, color: tokens.secondaryLabel }}>{c.host || '*'}{c.path}</span>
                            <span style={{ color: tokens.tertiaryLabel, fontSize: 14 }}>→</span>
                            {c.svc ? (
                              <>
                                <Badge color={tokens.blue}>Service</Badge>
                                <span style={{ fontSize: 11, color: tokens.secondaryLabel, fontFamily: 'monospace' }}>{c.svc.name}:{(c.svc.ports?.[0]?.port) ?? '?'}</span>
                                <span style={{ color: tokens.tertiaryLabel, fontSize: 14 }}>→</span>
                                <Badge color={c.pods.filter(p => p.ready).length > 0 ? tokens.green : tokens.red}>Pods</Badge>
                                <span style={{ fontSize: 11, color: tokens.secondaryLabel }}>{c.pods.filter(p => p.ready).length}/{c.pods.length} ready</span>
                              </>
                            ) : (
                              <span style={{ fontSize: 11, color: tokens.red }}>Service not found</span>
                            )}
                          </div>
                          {c.pods.length > 0 && (
                            <div style={{ marginTop: 6, marginLeft: 28, display: 'flex', gap: 5, flexWrap: 'wrap' }}>
                              {c.pods.slice(0, 6).map(p => (
                                <span key={p.name} style={{ fontSize: 10, padding: '2px 6px', borderRadius: 3, background: (p.ready ? tokens.green : tokens.orange) + '18', color: p.ready ? tokens.green : tokens.orange, fontFamily: 'monospace' }}>{truncate(p.name, 24)}</span>
                              ))}
                              {c.pods.length > 6 && <span style={{ fontSize: 10, color: tokens.tertiaryLabel }}>+{c.pods.length - 6} more</span>}
                            </div>
                          )}
                        </div>
                      ))}
                      {chain.length === 0 && <span style={{ fontSize: 12, color: tokens.tertiaryLabel }}>No routing rules</span>}
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        )}
      </div>
    </div>
  )
}

// ─── Topology SVG Tab ─────────────────────────────────────────────────────────
function TopologyTab({ data, nsFilter }: { data: TopologyData; nsFilter: string }) {
  const [selected, setSelected] = useState<string | null>(null)
  const [viewMode, setViewMode] = useState<'graph' | 'table'>('graph')
  const [graphLimit, setGraphLimit] = useState<{ ing: number; svc: number; dep: number; pod: number }>({ ing: 8, svc: 12, dep: 12, pod: 20 })

  const ingresses = data.ingresses.filter(i => nsFilter === '' || i.namespace === nsFilter)
  const services = data.services.filter(s => nsFilter === '' || s.namespace === nsFilter)
  const deployments = data.deployments.filter(d => nsFilter === '' || d.namespace === nsFilter)
  const pods = data.pods.filter(p => nsFilter === '' || p.namespace === nsFilter)

  const COL_X = [80, 280, 480, 680]
  const NODE_R = 26
  const W = 820
  const rowHeight = 68

  const ingNodes = ingresses.slice(0, graphLimit.ing)
  const svcNodes = services.slice(0, graphLimit.svc)
  const depNodes = deployments.slice(0, graphLimit.dep)
  const podNodes = pods.slice(0, graphLimit.pod)

  type GraphNode = { id: string; label: string; sub: string; kind: string; color: string; x: number; y: number }
  const nodes: GraphNode[] = []
  ingNodes.forEach((n, i) => nodes.push({ id: `ing:${n.namespace}/${n.name}`, label: truncate(n.name, 12), sub: n.namespace, kind: 'Ingress', color: tokens.pink, x: COL_X[0], y: 60 + i * rowHeight }))
  svcNodes.forEach((n, i) => nodes.push({ id: `svc:${n.namespace}/${n.name}`, label: truncate(n.name, 12), sub: n.namespace, kind: 'Service', color: tokens.blue, x: COL_X[1], y: 60 + i * rowHeight }))
  depNodes.forEach((n, i) => nodes.push({ id: `dep:${n.namespace}/${n.name}`, label: truncate(n.name, 12), sub: n.namespace, kind: 'Deployment', color: tokens.orange, x: COL_X[2], y: 60 + i * rowHeight }))
  podNodes.forEach((n, i) => nodes.push({ id: `pod:${n.namespace}/${n.name}`, label: truncate(n.name, 12), sub: n.namespace, kind: 'Pod', color: n.phase === 'Running' ? tokens.green : tokens.orange, x: COL_X[3], y: 60 + i * rowHeight }))

  type Edge = { from: string; to: string; dashed: boolean }
  const edges: Edge[] = []
  for (const ing of ingNodes) {
    for (const rule of (ing.rules ?? [])) {
      for (const path of (rule.paths ?? [])) {
        const svc = svcNodes.find(s => s.name === path.service_name && s.namespace === ing.namespace)
        if (svc) edges.push({ from: `ing:${ing.namespace}/${ing.name}`, to: `svc:${svc.namespace}/${svc.name}`, dashed: true })
      }
    }
  }
  for (const svc of svcNodes) {
    for (const pod of podNodes) {
      if (pod.namespace === svc.namespace && labelsMatch(svc.selector, pod.labels)) {
        edges.push({ from: `svc:${svc.namespace}/${svc.name}`, to: `pod:${pod.namespace}/${pod.name}`, dashed: false })
      }
    }
  }
  for (const dep of depNodes) {
    for (const pod of podNodes) {
      if (pod.namespace === dep.namespace && labelsMatch(dep.selector, pod.labels)) {
        edges.push({ from: `dep:${dep.namespace}/${dep.name}`, to: `pod:${pod.namespace}/${pod.name}`, dashed: false })
      }
    }
  }

  const nodeMap = new Map(nodes.map(n => [n.id, n]))
  const connectedIds = selected ? new Set<string>([selected, ...edges.filter(e => e.from === selected || e.to === selected).flatMap(e => [e.from, e.to])]) : null
  const maxRows = Math.max(ingNodes.length, svcNodes.length, depNodes.length, podNodes.length, 1)
  const H = Math.max(300, 60 + maxRows * rowHeight + 40)

  const truncatedCounts = { ing: ingresses.length - ingNodes.length, svc: services.length - svcNodes.length, dep: deployments.length - depNodes.length, pod: pods.length - podNodes.length }
  const hasTruncation = Object.values(truncatedCounts).some(v => v > 0)

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
        {(['graph', 'table'] as const).map(m => (
          <button key={m} onClick={() => setViewMode(m)} style={{ padding: '5px 14px', fontSize: 12, borderRadius: tokens.radius.sm, border: `0.5px solid ${viewMode === m ? tokens.blue : tokens.separator}`, background: viewMode === m ? tokens.blue : tokens.fill, color: viewMode === m ? '#fff' : tokens.label, cursor: 'pointer', fontWeight: 500 }}>
            {m === 'graph' ? 'Graph View' : 'Table View'}
          </button>
        ))}
        {selected && <button onClick={() => setSelected(null)} style={{ fontSize: 12, color: tokens.blue, background: 'none', border: 'none', cursor: 'pointer', marginLeft: 8 }}>Clear selection</button>}
      </div>

      {/* Truncation warning */}
      {hasTruncation && viewMode === 'graph' && (
        <div style={{ background: tokens.orange + '12', border: `0.5px solid ${tokens.orange}30`, borderRadius: tokens.radius.sm, padding: '8px 14px', fontSize: 12, color: tokens.orange, display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' }}>
          <AlertTriangle style={{ width: 13, height: 13, flexShrink: 0 }} />
          Graph shows a subset of resources.
          {truncatedCounts.ing > 0 && <span>+{truncatedCounts.ing} ingresses</span>}
          {truncatedCounts.svc > 0 && <span>+{truncatedCounts.svc} services</span>}
          {truncatedCounts.dep > 0 && <span>+{truncatedCounts.dep} deployments</span>}
          {truncatedCounts.pod > 0 && <span>+{truncatedCounts.pod} pods</span>}
          not shown.
          <button onClick={() => setViewMode('table')} style={{ color: tokens.blue, background: 'none', border: 'none', cursor: 'pointer', fontWeight: 600, padding: 0 }}>Switch to Table View →</button>
        </div>
      )}

      {viewMode === 'graph' ? (
        <div style={{ background: tokens.secondaryBackground, border: `0.5px solid ${tokens.separator}`, borderRadius: tokens.radius.lg, overflow: 'hidden' }}>
          <div style={{ overflowX: 'auto', padding: '0 0 8px' }}>
            <svg width={W} height={H} style={{ display: 'block', minWidth: W }}>
              <defs>
                <marker id="arrowBlue" markerWidth="6" markerHeight="6" refX="5" refY="3" orient="auto"><path d="M0,0 L0,6 L6,3 z" fill={tokens.blue} /></marker>
                <marker id="arrowGray" markerWidth="6" markerHeight="6" refX="5" refY="3" orient="auto"><path d="M0,0 L0,6 L6,3 z" fill={tokens.gray} /></marker>
              </defs>
              {[{ x: COL_X[0], label: 'Ingresses', color: tokens.pink, total: ingresses.length, shown: ingNodes.length },
                { x: COL_X[1], label: 'Services', color: tokens.blue, total: services.length, shown: svcNodes.length },
                { x: COL_X[2], label: 'Deployments', color: tokens.orange, total: deployments.length, shown: depNodes.length },
                { x: COL_X[3], label: 'Pods', color: tokens.green, total: pods.length, shown: podNodes.length },
              ].map(col => (
                <g key={col.label}>
                  <rect x={col.x - 50} y={4} width={100} height={22} rx={5} fill={col.color + '18'} />
                  <text x={col.x} y={19} textAnchor="middle" fontSize={10} fontWeight={600} fill={col.color}>{col.label} {col.total > col.shown ? `(${col.shown}/${col.total})` : ''}</text>
                </g>
              ))}
              {edges.map((edge, ei) => {
                const fromNode = nodeMap.get(edge.from)
                const toNode = nodeMap.get(edge.to)
                if (!fromNode || !toNode) return null
                const isActive = !selected || (selected === edge.from || selected === edge.to)
                return (
                  <line key={ei} x1={fromNode.x + NODE_R} y1={fromNode.y} x2={toNode.x - NODE_R} y2={toNode.y}
                    stroke={isActive ? (edge.dashed ? tokens.purple : tokens.blue) : tokens.gray}
                    strokeWidth={isActive ? 1.5 : 0.8} strokeDasharray={edge.dashed ? '5,4' : undefined}
                    opacity={isActive ? 1 : 0.12} markerEnd={isActive ? 'url(#arrowBlue)' : 'url(#arrowGray)'} />
                )
              })}
              {nodes.map(node => {
                const isSel = selected === node.id
                const isDimmed = connectedIds && !connectedIds.has(node.id)
                return (
                  <g key={node.id} style={{ cursor: 'pointer' }} onClick={() => setSelected(isSel ? null : node.id)}>
                    <circle cx={node.x} cy={node.y} r={NODE_R + 4} fill={node.color + '18'} opacity={isDimmed ? 0.2 : 1} />
                    <circle cx={node.x} cy={node.y} r={NODE_R} fill={node.color} opacity={isDimmed ? 0.2 : 1} stroke={isSel ? tokens.label : 'none'} strokeWidth={isSel ? 2.5 : 0} />
                    <text x={node.x} y={node.y + 4} textAnchor="middle" fontSize={9} fontWeight={600} fill="white" opacity={isDimmed ? 0.4 : 1}>{node.label}</text>
                    <text x={node.x} y={node.y + NODE_R + 13} textAnchor="middle" fontSize={9} fill={tokens.tertiaryLabel} opacity={isDimmed ? 0.3 : 0.85}>{truncate(node.sub, 12)}</text>
                  </g>
                )
              })}
            </svg>
          </div>
        </div>
      ) : (
        /* Table View */
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
          {[
            { label: 'Ingresses', icon: Globe, color: tokens.pink, items: ingresses.map(i => ({ id: `${i.namespace}/${i.name}`, name: i.name, ns: i.namespace, extra: (i.rules ?? []).map(r => r.host).join(', ') })) },
            { label: 'Services', icon: Network, color: tokens.blue, items: services.map(s => ({ id: `${s.namespace}/${s.name}`, name: s.name, ns: s.namespace, extra: s.type })) },
            { label: 'Deployments', icon: GitBranch, color: tokens.orange, items: deployments.map(d => ({ id: `${d.namespace}/${d.name}`, name: d.name, ns: d.namespace, extra: `${d.ready_replicas}/${d.replicas} ready` })) },
            { label: 'Pods', icon: Box, color: tokens.green, items: pods.map(p => ({ id: `${p.namespace}/${p.name}`, name: p.name, ns: p.namespace, extra: p.phase, highlight: p.phase !== 'Running' ? tokens.orange : undefined })) },
          ].map(section => {
            const Icon = section.icon
            return (
              <div key={section.label} style={{ background: tokens.secondaryBackground, border: `0.5px solid ${tokens.separator}`, borderRadius: tokens.radius.lg, overflow: 'hidden' }}>
                <div style={{ padding: '12px 16px', borderBottom: `0.5px solid ${tokens.separator}`, display: 'flex', alignItems: 'center', gap: 8 }}>
                  <Icon style={{ width: 14, height: 14, color: section.color }} />
                  <span style={{ fontSize: 13, fontWeight: 600, color: tokens.label }}>{section.label}</span>
                  <Badge color={section.color}>{section.items.length}</Badge>
                </div>
                <div style={{ maxHeight: 340, overflowY: 'auto' }}>
                  {section.items.map((item, i) => (
                    <div key={item.id} style={{ padding: '7px 16px', borderBottom: i < section.items.length - 1 ? `0.5px solid ${tokens.separator}` : 'none', display: 'flex', alignItems: 'center', gap: 8 }}>
                      <div style={{ flex: 1, minWidth: 0 }}>
                        <div style={{ fontSize: 11, fontFamily: 'monospace', color: tokens.label, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{item.name}</div>
                        <div style={{ fontSize: 10, color: tokens.tertiaryLabel }}>{item.ns}</div>
                      </div>
                      <span style={{ fontSize: 11, color: (item as any).highlight ?? tokens.tertiaryLabel, flexShrink: 0 }}>{item.extra}</span>
                    </div>
                  ))}
                </div>
              </div>
            )
          })}
        </div>
      )}

      {nodes.length === 0 && viewMode === 'graph' && (
        <EmptyState icon={GitBranch} text="No topology data for this namespace" />
      )}
    </div>
  )
}

// ─── Nodes Tab ────────────────────────────────────────────────────────────────
function NodesTab({ data, onResourceClick }: { data: TopologyData; onResourceClick?: (r: SelectedResource) => void }) {
  const [expandedNode, setExpandedNode] = useState<string | null>(null)
  const [search, setSearch] = useState('')
  const [roleFilter, setRoleFilter] = useState<'all' | 'master' | 'worker'>('all')

  const nodes = data.nodes.filter(n => {
    if (!n.name.toLowerCase().includes(search.toLowerCase())) return false
    if (roleFilter === 'master') return (n.roles ?? []).some(r => r === 'master' || r === 'control-plane')
    if (roleFilter === 'worker') return !(n.roles ?? []).some(r => r === 'master' || r === 'control-plane')
    return true
  })

  const getNodePods = (nodeName: string) => data.pods.filter(p => p.node_name === nodeName)
  const getNodeStats = (nodeName: string) => {
    const nPods = getNodePods(nodeName)
    const running = nPods.filter(p => p.phase === 'Running').length
    const restartTotal = nPods.reduce((s, p) => s + p.restarts, 0)
    return { total: nPods.length, running, restartTotal }
  }

  return (
    <div style={{ background: tokens.secondaryBackground, border: `0.5px solid ${tokens.separator}`, borderRadius: tokens.radius.lg, overflow: 'hidden' }}>
      <div style={{ padding: '14px 18px', borderBottom: `0.5px solid ${tokens.separator}`, display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <Server style={{ width: 16, height: 16, color: tokens.blue }} />
          <span style={{ fontSize: 14, fontWeight: 600, color: tokens.label }}>Nodes</span>
          <Badge color={tokens.gray}>{nodes.length}</Badge>
        </div>
        <div style={{ display: 'flex', gap: 4 }}>
          {(['all', 'master', 'worker'] as const).map(r => (
            <button key={r} onClick={() => setRoleFilter(r)} style={{ padding: '3px 10px', fontSize: 11, borderRadius: 20, border: `0.5px solid ${roleFilter === r ? tokens.blue : tokens.separator}`, background: roleFilter === r ? tokens.blue : tokens.fill, color: roleFilter === r ? '#fff' : tokens.secondaryLabel, cursor: 'pointer', fontWeight: 500 }}>
              {r === 'all' ? 'All' : r.charAt(0).toUpperCase() + r.slice(1)}
            </button>
          ))}
        </div>
      </div>
      <div style={{ padding: '12px 18px 0' }}>
        <SearchInput value={search} onChange={setSearch} placeholder="Search nodes…" />
      </div>
      {nodes.length === 0 ? <EmptyState icon={Server} text="No nodes found" /> : (
        <div>
          <div style={{ display: 'grid', gridTemplateColumns: '2fr 90px 100px 1.5fr 1fr 80px 60px 40px', padding: '8px 18px', borderBottom: `0.5px solid ${tokens.separator}`, fontSize: 11, fontWeight: 600, color: tokens.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.04em' }}>
            <div>Name</div><div>Status</div><div>Roles</div><div>OS / Version</div><div>Pods (run/total)</div><div>Restarts</div><div>Details</div><div></div>
          </div>
          {nodes.map((n, i) => {
            const stats = getNodeStats(n.name)
            const key = n.name
            const expanded = expandedNode === key
            return (
              <div key={key} style={{ borderBottom: i < nodes.length - 1 ? `0.5px solid ${tokens.separator}` : 'none' }}>
                <div style={{ display: 'grid', gridTemplateColumns: '2fr 90px 100px 1.5fr 1fr 80px 60px 40px', padding: '10px 18px', fontSize: 12, alignItems: 'center', cursor: 'pointer' }}
                  onClick={() => setExpandedNode(expanded ? null : key)}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 5 }}>
                    {expanded ? <ChevronUp style={{ width: 12, height: 12, color: tokens.gray }} /> : <ChevronRight style={{ width: 12, height: 12, color: tokens.gray }} />}
                    <span style={{ color: tokens.label, fontWeight: 500, fontFamily: 'monospace', fontSize: 11 }}>{n.name}</span>
                  </div>
                  <div><Badge color={n.ready ? tokens.green : tokens.red}>{n.status}</Badge></div>
                  <div style={{ display: 'flex', gap: 3, flexWrap: 'wrap' }}>
                    {(n.roles ?? []).map(r => <Badge key={r} color={r === 'master' || r === 'control-plane' ? tokens.purple : tokens.gray}>{r}</Badge>)}
                  </div>
                  <div style={{ color: tokens.tertiaryLabel, fontSize: 11 }}>{truncate(n.os ?? '—', 20)} / {n.version ?? '—'}</div>
                  <div style={{ color: stats.running < stats.total ? tokens.orange : tokens.green, fontWeight: 500 }}>{stats.running}/{stats.total}</div>
                  <div style={{ color: stats.restartTotal > 0 ? tokens.orange : tokens.tertiaryLabel }}>{stats.restartTotal}</div>
                  <div>
                    <button onClick={e => { e.stopPropagation(); setExpandedNode(expanded ? null : key) }}
                      style={{ padding: '3px 8px', fontSize: 11, borderRadius: tokens.radius.sm, border: `0.5px solid ${tokens.separator}`, background: tokens.fill, color: tokens.label, cursor: 'pointer' }}>
                      {expanded ? 'Hide' : 'Pods'}
                    </button>
                  </div>
                  <div>
                    {onResourceClick && (
                      <button onClick={e => { e.stopPropagation(); onResourceClick({ kind: 'Node', name: n.name, cluster: data.cluster }) }}
                        style={{ padding: '3px 7px', fontSize: 11, borderRadius: tokens.radius.sm, border: `0.5px solid ${tokens.purple}44`, background: tokens.purple + '12', color: tokens.purple, cursor: 'pointer', display: 'flex', alignItems: 'center' }}>
                        <Info style={{ width: 11, height: 11 }} />
                      </button>
                    )}
                  </div>
                </div>
                {expanded && (
                  <div style={{ background: tokens.tertiaryFill, padding: '10px 36px 14px', borderTop: `0.5px solid ${tokens.separator}` }}>
                    <div style={{ fontSize: 11, color: tokens.tertiaryLabel, marginBottom: 8 }}>Pods on {n.name} ({stats.total})</div>
                    {stats.total === 0 ? (
                      <span style={{ fontSize: 12, color: tokens.tertiaryLabel }}>No pods scheduled</span>
                    ) : (
                      <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                        {getNodePods(n.name).map(p => (
                          <div key={`${p.namespace}/${p.name}`} style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                            <span style={{ fontSize: 11, fontFamily: 'monospace', color: tokens.label, minWidth: 220 }}>{p.namespace}/{p.name}</span>
                            <PhaseBadge phase={p.phase} />
                            {p.restarts > 0 && <Badge color={tokens.orange}>{p.restarts}r</Badge>}
                          </div>
                        ))}
                      </div>
                    )}
                    <div style={{ marginTop: 12 }}>
                      <div style={{ fontSize: 11, color: tokens.tertiaryLabel, marginBottom: 6 }}>Node Labels</div>
                      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
                        {Object.entries(n.labels ?? {}).slice(0, 12).map(([k, v]) => (
                          <span key={k} style={{ fontSize: 10, padding: '2px 6px', borderRadius: 3, background: tokens.purple + '15', color: tokens.purple, fontFamily: 'monospace' }}>{k}={v}</span>
                        ))}
                      </div>
                    </div>
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

// ─── Events Tab ───────────────────────────────────────────────────────────────
function EventsTab({ clusterName, events, onRefresh }: { clusterName: string; events: K8sEvent[]; onRefresh: () => void }) {
  const [filter, setFilter] = useState<'all' | 'warning' | 'normal'>('all')
  const [search, setSearch] = useState('')
  const [nsFilter, setNsFilter] = useState('')
  const [kindFilter, setKindFilter] = useState('')
  const [eventsShown, setEventsShown] = useState(50)

  const namespaces = Array.from(new Set(events.map(e => e.involved_object.namespace).filter(Boolean) as string[])).sort()
  const kinds = Array.from(new Set(events.map(e => e.involved_object.kind))).sort()

  const filtered = events
    .filter(e => filter === 'all' || (filter === 'warning' ? e.type === 'Warning' : e.type === 'Normal'))
    .filter(e => !nsFilter || e.involved_object.namespace === nsFilter)
    .filter(e => !kindFilter || e.involved_object.kind === kindFilter)
    .filter(e => !search || e.reason.toLowerCase().includes(search.toLowerCase()) || e.message.toLowerCase().includes(search.toLowerCase()) || e.involved_object.name.toLowerCase().includes(search.toLowerCase()))

  useEffect(() => { setEventsShown(50) }, [filter, nsFilter, kindFilter, search])

  const warnCount = events.filter(e => e.type === 'Warning').length
  const normalCount = events.filter(e => e.type === 'Normal').length

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      {/* Filters row */}
      <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
        <div style={{ flex: 1, minWidth: 200 }}>
          <SearchInput value={search} onChange={setSearch} placeholder="Search events, reasons, objects…" />
        </div>
        <div style={{ display: 'flex', gap: 5 }}>
          {([['all', `All ${events.length}`], ['warning', `⚠ Warnings ${warnCount}`], ['normal', `Normal ${normalCount}`]] as const).map(([f, label]) => (
            <button key={f} onClick={() => setFilter(f)} style={{ padding: '6px 12px', fontSize: 12, borderRadius: tokens.radius.sm, border: `0.5px solid ${tokens.separator}`, background: filter === f ? tokens.blue : tokens.fill, color: filter === f ? '#fff' : tokens.label, cursor: 'pointer', fontWeight: 500, whiteSpace: 'nowrap' }}>{label}</button>
          ))}
        </div>
        <button onClick={onRefresh} style={{ padding: '6px 12px', fontSize: 12, borderRadius: tokens.radius.sm, border: `0.5px solid ${tokens.separator}`, background: tokens.fill, color: tokens.label, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 5 }}>
          <RefreshCw style={{ width: 12, height: 12 }} /> Refresh
        </button>
      </div>

      {/* Namespace + Kind filters */}
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
        <select value={nsFilter} onChange={e => setNsFilter(e.target.value)} style={{ padding: '5px 10px', borderRadius: tokens.radius.sm, border: `0.5px solid ${tokens.separator}`, background: tokens.fill, color: tokens.label, fontSize: 12, outline: 'none', cursor: 'pointer' }}>
          <option value="">All namespaces</option>
          {namespaces.map(ns => <option key={ns} value={ns}>{ns}</option>)}
        </select>
        <select value={kindFilter} onChange={e => setKindFilter(e.target.value)} style={{ padding: '5px 10px', borderRadius: tokens.radius.sm, border: `0.5px solid ${tokens.separator}`, background: tokens.fill, color: tokens.label, fontSize: 12, outline: 'none', cursor: 'pointer' }}>
          <option value="">All kinds</option>
          {kinds.map(k => <option key={k} value={k}>{k}</option>)}
        </select>
        {(nsFilter || kindFilter || search || filter !== 'all') && (
          <button onClick={() => { setNsFilter(''); setKindFilter(''); setSearch(''); setFilter('all') }} style={{ padding: '5px 10px', fontSize: 12, borderRadius: tokens.radius.sm, border: `0.5px solid ${tokens.orange}40`, background: tokens.orange + '12', color: tokens.orange, cursor: 'pointer' }}>
            Clear filters
          </button>
        )}
        <span style={{ fontSize: 12, color: tokens.tertiaryLabel, alignSelf: 'center' }}>
          {filtered.length} event{filtered.length !== 1 ? 's' : ''}
        </span>
      </div>

      <div style={{ background: tokens.secondaryBackground, border: `0.5px solid ${tokens.separator}`, borderRadius: tokens.radius.lg, overflow: 'hidden' }}>
        {filtered.length === 0 ? <EmptyState icon={Activity} text="No events found" /> : (
          <div>
            <div style={{ display: 'grid', gridTemplateColumns: '110px 80px 110px 90px 200px 1fr', padding: '8px 18px', borderBottom: `0.5px solid ${tokens.separator}`, fontSize: 11, fontWeight: 600, color: tokens.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.04em' }}>
              <div>Time</div><div>Type</div><div>Reason</div><div>Count</div><div>Object</div><div>Message</div>
            </div>
            {filtered.slice(0, eventsShown).map((ev, i) => (
              <div key={i} style={{ display: 'grid', gridTemplateColumns: '110px 80px 110px 90px 200px 1fr', padding: '9px 18px', borderBottom: i < Math.min(eventsShown, filtered.length) - 1 ? `0.5px solid ${tokens.separator}` : 'none', fontSize: 12, alignItems: 'start', background: ev.type === 'Warning' ? tokens.orange + '04' : 'transparent' }}>
                <div style={{ color: tokens.tertiaryLabel }}>{new Date(ev.last_timestamp).toLocaleTimeString()}</div>
                <div><Badge color={ev.type === 'Warning' ? tokens.orange : tokens.blue}>{ev.type}</Badge></div>
                <div style={{ color: ev.type === 'Warning' ? tokens.orange : tokens.label, fontWeight: 500 }}>{ev.reason}</div>
                <div style={{ color: tokens.tertiaryLabel }}>{ev.count ?? 1}×</div>
                <div style={{ color: tokens.secondaryLabel, fontSize: 11, fontFamily: 'monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {ev.involved_object.kind}/{truncate(ev.involved_object.name, 22)}
                  {ev.involved_object.namespace && <div style={{ color: tokens.tertiaryLabel }}>{ev.involved_object.namespace}</div>}
                </div>
                <div style={{ color: tokens.label, fontSize: 12 }}>{truncate(ev.message, 120)}</div>
              </div>
            ))}
            <ShowMoreFooter shown={Math.min(eventsShown, filtered.length)} total={filtered.length} increment={50}
              onShowMore={() => setEventsShown(n => Math.min(n + 50, filtered.length))}
              onShowAll={() => setEventsShown(filtered.length)}
              onCollapse={() => setEventsShown(50)} />
          </div>
        )}
      </div>
    </div>
  )
}

// ─── Namespace Auto-Scan Panel ────────────────────────────────────────────────
function NsScanPanel({ data, events, clusterName }: { data: TopologyData; events: K8sEvent[]; clusterName: string }) {
  const [scanNs, setScanNs] = useState('')
  const [podLogMap, setPodLogMap] = useState<Record<string, { logs: string; loading: boolean; error?: string; expanded: boolean }>>({})
  const [scanRunning, setScanRunning] = useState(false)
  const [scanComplete, setScanComplete] = useState(false)

  const availableNs = useMemo(() => Array.from(new Set(data.pods.map(p => p.namespace))).sort(), [data.pods])

  const nsPods = useMemo(() => data.pods.filter(p => p.namespace === scanNs), [scanNs, data.pods])
  const nsEvents = useMemo(() => events.filter(e => e.involved_object.namespace === scanNs), [scanNs, events])
  const nsDeployments = useMemo(() => data.deployments.filter(d => d.namespace === scanNs), [scanNs, data.deployments])
  const nsServices = useMemo(() => data.services.filter(s => s.namespace === scanNs), [scanNs, data.services])
  const unhealthyPods = useMemo(() => nsPods.filter(p => p.phase !== 'Running' || !p.ready || p.restarts >= 3).sort((a, b) => b.restarts - a.restarts), [nsPods])
  const deadSvcs = useMemo(() => getServicesWithNoEndpoints(nsServices, nsPods), [nsServices, nsPods])
  const nsHealth = useMemo(() => getNsHealthScore(nsPods, nsDeployments), [nsPods, nsDeployments])
  const recentWarnings = useMemo(() => nsEvents.filter(e => e.type === 'Warning').slice(0, 10), [nsEvents])

  const runScan = useCallback(async (ns: string) => {
    if (!ns || !clusterName) return
    setScanRunning(true); setScanComplete(false); setPodLogMap({})
    const toFetch = unhealthyPods.slice(0, 4)
    if (toFetch.length === 0) { setScanRunning(false); setScanComplete(true); return }

    await Promise.all(toFetch.map(async pod => {
      const container = pod.containers?.[0]?.name ?? ''
      setPodLogMap(prev => ({ ...prev, [pod.name]: { logs: '', loading: true, expanded: false } }))
      try {
        const url = `/api/v1/k8s-service/api/v1/clusters/${encodeURIComponent(clusterName)}/pods/${encodeURIComponent(pod.name)}/logs?namespace=${encodeURIComponent(ns)}&container=${encodeURIComponent(container)}&tail_lines=100`
        const res = await fetch(url, { headers: authHeader() })
        const json = await res.json()
        setPodLogMap(prev => ({ ...prev, [pod.name]: { logs: json.data?.logs ?? '(no output)', loading: false, expanded: false, error: json.success ? undefined : json.error } }))
      } catch (e: any) {
        setPodLogMap(prev => ({ ...prev, [pod.name]: { logs: '', loading: false, expanded: false, error: e.message } }))
      }
    }))

    setScanRunning(false); setScanComplete(true)
  }, [clusterName, unhealthyPods])

  useEffect(() => {
    if (scanNs) { setScanComplete(false); setPodLogMap({}); runScan(scanNs) }
  }, [scanNs])

  const inputStyle: React.CSSProperties = { width: '100%', padding: '7px 10px', boxSizing: 'border-box', borderRadius: tokens.radius.sm, border: `0.5px solid ${tokens.separator}`, background: tokens.fill, color: tokens.label, fontSize: 13, outline: 'none', cursor: 'pointer' }

  const toggleLog = (podName: string) => {
    setPodLogMap(prev => ({ ...prev, [podName]: { ...prev[podName], expanded: !prev[podName]?.expanded } }))
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {/* NS selector */}
      <div style={{ display: 'flex', gap: 10, alignItems: 'flex-end', flexWrap: 'wrap' }}>
        <div style={{ flex: 1, minWidth: 200 }}>
          <label style={{ fontSize: 12, fontWeight: 500, color: tokens.secondaryLabel, display: 'block', marginBottom: 4 }}>Namespace to diagnose</label>
          <select value={scanNs} onChange={e => setScanNs(e.target.value)} style={inputStyle}>
            <option value="">Select namespace…</option>
            {availableNs.map(ns => <option key={ns} value={ns}>{ns}</option>)}
          </select>
        </div>
        {scanNs && (
          <button onClick={() => runScan(scanNs)} disabled={scanRunning} style={{ padding: '7px 16px', borderRadius: tokens.radius.sm, border: 'none', background: tokens.blue, color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 6, opacity: scanRunning ? 0.6 : 1 }}>
            {scanRunning ? <Loader2 style={{ width: 14, height: 14, animation: 'spin 1s linear infinite' }} /> : <Zap style={{ width: 14, height: 14 }} />}
            {scanRunning ? 'Scanning…' : 'Re-scan'}
          </button>
        )}
      </div>

      {!scanNs && (
        <div style={{ textAlign: 'center', padding: '32px 0', color: tokens.tertiaryLabel, fontSize: 13 }}>
          Select a namespace to auto-run a full SRE diagnostic scan
        </div>
      )}

      {scanNs && (
        <>
          {/* Health Summary */}
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(160px, 1fr))', gap: 10 }}>
            <div style={{ background: tokens.secondaryBackground, border: `0.5px solid ${nsHealth.color}30`, borderRadius: tokens.radius.lg, padding: '14px 16px', textAlign: 'center' }}>
              <div style={{ fontSize: 36, fontWeight: 800, color: nsHealth.color }}>{nsHealth.grade}</div>
              <div style={{ fontSize: 12, color: tokens.secondaryLabel }}>{nsHealth.score}% healthy</div>
              <div style={{ fontSize: 11, color: tokens.tertiaryLabel }}>Health score</div>
            </div>
            <StatCard label="Total Pods" value={nsPods.length} sub={`${nsPods.filter(p => p.phase === 'Running' && p.ready).length} running & ready`} color={tokens.green} icon={Box} />
            <StatCard label="Deployments" value={nsDeployments.length} sub={`${nsDeployments.filter(d => d.ready_replicas >= d.replicas && d.replicas > 0).length} healthy`} color={tokens.orange} icon={GitBranch} />
            <StatCard label="Services" value={nsServices.length} sub={deadSvcs.length > 0 ? `${deadSvcs.length} dead endpoints` : 'all healthy'} color={deadSvcs.length > 0 ? tokens.red : tokens.blue} icon={Network} />
          </div>

          {/* Issues */}
          {unhealthyPods.length > 0 && (
            <div style={{ background: tokens.secondaryBackground, border: `0.5px solid ${tokens.separator}`, borderRadius: tokens.radius.lg, overflow: 'hidden' }}>
              <div style={{ padding: '14px 18px', borderBottom: `0.5px solid ${tokens.separator}`, display: 'flex', alignItems: 'center', gap: 8 }}>
                <AlertTriangle style={{ width: 16, height: 16, color: tokens.orange }} />
                <span style={{ fontSize: 14, fontWeight: 600, color: tokens.label }}>Pods Needing Attention ({unhealthyPods.length})</span>
                {scanRunning && <Loader2 style={{ width: 13, height: 13, color: tokens.blue, animation: 'spin 1s linear infinite', marginLeft: 4 }} />}
                {scanComplete && !scanRunning && <Badge color={tokens.green}>Logs fetched</Badge>}
              </div>
              {unhealthyPods.map((p, i) => {
                const logState = podLogMap[p.name]
                const diagIssues = autoDiagnose(p)
                return (
                  <div key={p.name} style={{ borderBottom: i < unhealthyPods.length - 1 ? `0.5px solid ${tokens.separator}` : 'none' }}>
                    <div style={{ padding: '12px 18px' }}>
                      {/* Pod header */}
                      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8, flexWrap: 'wrap' }}>
                        <span style={{ fontFamily: 'monospace', fontSize: 12, fontWeight: 600, color: tokens.label }}>{p.name}</span>
                        <PhaseBadge phase={p.phase} />
                        {p.restarts > 0 && <Badge color={p.restarts >= 5 ? tokens.red : tokens.orange}>{p.restarts} restarts</Badge>}
                        {!p.ready && p.phase === 'Running' && <Badge color={tokens.orange}>Not Ready</Badge>}
                        {logState && (
                          <button onClick={() => toggleLog(p.name)} style={{ marginLeft: 'auto', padding: '3px 10px', fontSize: 11, borderRadius: tokens.radius.sm, border: `0.5px solid ${tokens.separator}`, background: tokens.fill, color: tokens.label, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 4 }}>
                            <FileText style={{ width: 11, height: 11 }} />
                            {logState.expanded ? 'Hide logs' : 'Show logs'}
                            {logState.loading && <Loader2 style={{ width: 10, height: 10, animation: 'spin 1s linear infinite' }} />}
                          </button>
                        )}
                      </div>
                      {/* Diagnosis */}
                      {diagIssues.filter(d => d.severity !== 'info').map((issue, ii) => (
                        <DiagnosisCard key={ii} issue={issue} />
                      ))}
                      {/* Log output */}
                      {logState?.expanded && (
                        <div style={{ marginTop: 8 }}>
                          {logState.error ? (
                            <div style={{ padding: '8px 12px', borderRadius: tokens.radius.sm, background: tokens.red + '12', color: tokens.red, fontSize: 12 }}>{logState.error}</div>
                          ) : logState.loading ? (
                            <div style={{ display: 'flex', alignItems: 'center', gap: 6, color: tokens.tertiaryLabel, fontSize: 12 }}>
                              <Loader2 style={{ width: 13, height: 13, animation: 'spin 1s linear infinite' }} /> Fetching logs…
                            </div>
                          ) : (
                            <pre style={{ background: '#0d1117', color: '#c9d1d9', padding: 12, borderRadius: tokens.radius.md, fontSize: 10, fontFamily: '"SF Mono", monospace', overflowX: 'auto', overflowY: 'auto', maxHeight: 280, margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-all', lineHeight: 1.5 }}>
                              {logState.logs || '(no log output)'}
                            </pre>
                          )}
                        </div>
                      )}
                    </div>
                  </div>
                )
              })}
            </div>
          )}

          {/* Dead services */}
          {deadSvcs.length > 0 && (
            <div style={{ background: tokens.secondaryBackground, border: `0.5px solid ${tokens.red}30`, borderRadius: tokens.radius.lg, padding: '14px 18px' }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 10 }}>
                <Network style={{ width: 15, height: 15, color: tokens.red }} />
                <span style={{ fontSize: 13, fontWeight: 600, color: tokens.red }}>Services with No Healthy Endpoints ({deadSvcs.length})</span>
              </div>
              {deadSvcs.map(s => (
                <div key={s.name} style={{ marginBottom: 6 }}>
                  <DiagnosisCard issue={{ severity: 'error', title: `${s.name} has no ready pods`, detail: `Selector: ${Object.entries(s.selector ?? {}).map(([k, v]) => `${k}=${v}`).join(', ')} — no running pods matched in ${s.namespace}`, fix: `kubectl describe svc ${s.name} -n ${s.namespace}` }} />
                </div>
              ))}
            </div>
          )}

          {/* Deployment issues */}
          {nsDeployments.filter(d => d.ready_replicas < d.replicas).length > 0 && (
            <div style={{ background: tokens.secondaryBackground, border: `0.5px solid ${tokens.separator}`, borderRadius: tokens.radius.lg, overflow: 'hidden' }}>
              <div style={{ padding: '14px 18px', borderBottom: `0.5px solid ${tokens.separator}`, display: 'flex', alignItems: 'center', gap: 8 }}>
                <GitBranch style={{ width: 15, height: 15, color: tokens.orange }} />
                <span style={{ fontSize: 13, fontWeight: 600, color: tokens.label }}>Degraded Deployments</span>
              </div>
              {nsDeployments.filter(d => d.ready_replicas < d.replicas).map(d => (
                <div key={d.name} style={{ padding: '10px 18px', borderBottom: `0.5px solid ${tokens.separator}`, display: 'flex', alignItems: 'center', gap: 10, fontSize: 12 }}>
                  <AlertTriangle style={{ width: 13, height: 13, color: tokens.orange }} />
                  <span style={{ fontFamily: 'monospace', color: tokens.label, flex: 1 }}>{d.name}</span>
                  <Badge color={tokens.orange}>{d.ready_replicas}/{d.replicas} ready</Badge>
                  {d.ready_replicas === 0 && <Badge color={tokens.red}>No pods</Badge>}
                </div>
              ))}
            </div>
          )}

          {/* Namespace Events */}
          <div style={{ background: tokens.secondaryBackground, border: `0.5px solid ${tokens.separator}`, borderRadius: tokens.radius.lg, overflow: 'hidden' }}>
            <div style={{ padding: '14px 18px', borderBottom: `0.5px solid ${tokens.separator}`, display: 'flex', alignItems: 'center', gap: 8 }}>
              <Activity style={{ width: 15, height: 15, color: tokens.blue }} />
              <span style={{ fontSize: 13, fontWeight: 600, color: tokens.label }}>Namespace Events</span>
              <Badge color={tokens.gray}>{nsEvents.length}</Badge>
              {recentWarnings.length > 0 && <Badge color={tokens.orange}>{recentWarnings.length} warnings</Badge>}
            </div>
            {nsEvents.length === 0 ? (
              <div style={{ padding: '16px 18px', color: tokens.tertiaryLabel, fontSize: 13 }}>No events in this namespace</div>
            ) : (
              nsEvents.slice(0, 12).map((ev, i) => (
                <div key={i} style={{ padding: '8px 18px', borderBottom: i < Math.min(12, nsEvents.length) - 1 ? `0.5px solid ${tokens.separator}` : 'none', display: 'grid', gridTemplateColumns: '90px 80px 110px 1fr', gap: 10, fontSize: 12, background: ev.type === 'Warning' ? tokens.orange + '05' : 'transparent' }}>
                  <span style={{ color: tokens.tertiaryLabel }}>{new Date(ev.last_timestamp).toLocaleTimeString()}</span>
                  <Badge color={ev.type === 'Warning' ? tokens.orange : tokens.blue}>{ev.type}</Badge>
                  <span style={{ color: ev.type === 'Warning' ? tokens.orange : tokens.label, fontWeight: 500 }}>{ev.reason}</span>
                  <span style={{ color: tokens.secondaryLabel }}>{truncate(ev.message, 100)}</span>
                </div>
              ))
            )}
          </div>

          {/* kubectl commands for NS */}
          <div style={{ background: tokens.secondaryBackground, border: `0.5px solid ${tokens.separator}`, borderRadius: tokens.radius.lg, overflow: 'hidden' }}>
            <div style={{ padding: '14px 18px', borderBottom: `0.5px solid ${tokens.separator}`, display: 'flex', alignItems: 'center', gap: 8 }}>
              <Terminal style={{ width: 15, height: 15, color: tokens.green }} />
              <span style={{ fontSize: 13, fontWeight: 600, color: tokens.label }}>kubectl Commands for {scanNs}</span>
            </div>
            <div style={{ padding: 14 }}>
              <KubectlPanel namespace={scanNs} clusterContext={clusterName} />
            </div>
          </div>

          {unhealthyPods.length === 0 && deadSvcs.length === 0 && nsDeployments.filter(d => d.ready_replicas < d.replicas).length === 0 && (
            <div style={{ background: tokens.green + '0d', border: `0.5px solid ${tokens.green}30`, borderRadius: tokens.radius.lg, padding: '24px', textAlign: 'center' }}>
              <CheckCircle style={{ width: 32, height: 32, color: tokens.green, marginBottom: 8 }} />
              <div style={{ fontSize: 14, fontWeight: 600, color: tokens.green }}>Namespace "{scanNs}" looks healthy</div>
              <div style={{ fontSize: 12, color: tokens.secondaryLabel, marginTop: 4 }}>All pods running, all services have endpoints, all deployments at desired replicas</div>
            </div>
          )}
        </>
      )}
    </div>
  )
}

// ─── Troubleshoot Tab ─────────────────────────────────────────────────────────
function TroubleshootTab({ data, events, clusterName, initialPodNs, initialPodName }: {
  data: TopologyData; events: K8sEvent[]; clusterName: string; initialPodNs?: string; initialPodName?: string
}) {
  const [section, setSection] = useState<TroubleshootSection>('ns-scan')
  const [logNs, setLogNs] = useState(initialPodNs ?? '')
  const [logPod, setLogPod] = useState(initialPodName ?? '')
  const [logContainer, setLogContainer] = useState('')
  const [logOutput, setLogOutput] = useState('')
  const [logLoading, setLogLoading] = useState(false)
  const [logError, setLogError] = useState('')
  const [copied, setCopied] = useState(false)
  const [tailLines, setTailLines] = useState('200')
  const [showExpanded, setShowExpanded] = useState<Record<string, boolean>>({})

  // Auto-switch to pod logs section when triggered from workloads
  useEffect(() => {
    if (initialPodNs || initialPodName) setSection('pod-logs')
  }, [initialPodNs, initialPodName])

  useEffect(() => {
    if (!logPod || !logNs) { setLogContainer(''); return }
    const pod = data.pods.find(p => p.name === logPod && p.namespace === logNs)
    if (pod?.containers?.length) setLogContainer(pod.containers[0].name)
  }, [logPod, logNs, data.pods])

  const availableNamespaces = useMemo(() => Array.from(new Set(data.pods.map(p => p.namespace))).sort(), [data.pods])
  const podsInNs = data.pods.filter(p => p.namespace === logNs)
  const containersForPod = data.pods.find(p => p.name === logPod && p.namespace === logNs)?.containers ?? []
  const selectedPodData = data.pods.find(p => p.name === logPod && p.namespace === logNs)

  const fetchLogs = async () => {
    if (!logPod || !clusterName) return
    setLogLoading(true); setLogError(''); setLogOutput('')
    try {
      const url = `/api/v1/k8s-service/api/v1/clusters/${encodeURIComponent(clusterName)}/pods/${encodeURIComponent(logPod)}/logs?namespace=${encodeURIComponent(logNs)}&container=${encodeURIComponent(logContainer)}&tail_lines=${tailLines}`
      const res = await fetch(url, { headers: authHeader() })
      const json = await res.json()
      if (json.success) setLogOutput(json.data?.logs ?? '(empty)')
      else setLogError(json.error ?? 'Unknown error')
    } catch (e: any) {
      setLogError(e.message)
    } finally {
      setLogLoading(false)
    }
  }

  const copyLogs = () => {
    navigator.clipboard.writeText(logOutput).then(() => { setCopied(true); setTimeout(() => setCopied(false), 2000) })
  }

  // SRE Checklist data
  const crashPods = data.pods.filter(p => p.restarts >= 5).sort((a, b) => b.restarts - a.restarts)
  const pendingPods = data.pods.filter(p => p.phase === 'Pending')
  const notReadyNodes = data.nodes.filter(n => !n.ready)
  const zeroReadyDeps = data.deployments.filter(d => d.ready_replicas === 0 && d.replicas > 0)
  const highRestartPods = data.pods.filter(p => p.restarts >= 1 && p.restarts < 5).sort((a, b) => b.restarts - a.restarts)
  const failedPods = data.pods.filter(p => p.phase === 'Failed')
  const notReadyPods = data.pods.filter(p => p.phase === 'Running' && !p.ready)
  const allDeadSvcs = getServicesWithNoEndpoints(data.services, data.pods)

  const inputStyle: React.CSSProperties = { width: '100%', padding: '7px 10px', boxSizing: 'border-box', borderRadius: tokens.radius.sm, border: `0.5px solid ${tokens.separator}`, background: tokens.fill, color: tokens.label, fontSize: 13, outline: 'none' }
  const selectStyle: React.CSSProperties = { ...inputStyle, cursor: 'pointer' }
  const labelStyle: React.CSSProperties = { fontSize: 12, fontWeight: 500, color: tokens.secondaryLabel, display: 'block', marginBottom: 4 }

  const sections: { key: TroubleshootSection; label: string; icon: React.ComponentType<any>; badge?: number }[] = [
    { key: 'ns-scan', label: 'NS Auto-Scan', icon: Zap },
    { key: 'pod-logs', label: 'Pod Logs', icon: Terminal },
    { key: 'kubectl', label: 'kubectl', icon: Play },
    { key: 'checklist', label: 'SRE Checklist', icon: Shield, badge: crashPods.length + pendingPods.length + notReadyNodes.length + zeroReadyDeps.length + failedPods.length + allDeadSvcs.length },
  ]

  const checklistCards = [
    { title: 'CrashLoop / High Restarts', count: crashPods.length, color: tokens.red, icon: XCircle, items: crashPods.map(p => ({ label: `${p.namespace}/${p.name}`, extra: `${p.restarts} restarts`, color: p.restarts >= 10 ? tokens.red : tokens.orange })) },
    { title: 'Pods Failing', count: failedPods.length, color: tokens.red, icon: AlertCircle, items: failedPods.map(p => ({ label: `${p.namespace}/${p.name}`, extra: p.phase, color: tokens.red })) },
    { title: 'Pending Pods', count: pendingPods.length, color: tokens.orange, icon: Clock, items: pendingPods.map(p => ({ label: `${p.namespace}/${p.name}`, extra: relativeAge(p.created_at), color: tokens.orange })) },
    { title: 'Running But Not Ready', count: notReadyPods.length, color: tokens.orange, icon: AlertTriangle, items: notReadyPods.map(p => ({ label: `${p.namespace}/${p.name}`, extra: `${p.containers?.filter(c => !c.ready).length ?? 0} ctrs unready`, color: tokens.orange })) },
    { title: 'Nodes Not Ready', count: notReadyNodes.length, color: tokens.red, icon: Server, items: notReadyNodes.map(n => ({ label: n.name, extra: n.status, color: tokens.red })) },
    { title: 'Deployments — 0 Ready', count: zeroReadyDeps.length, color: tokens.red, icon: GitBranch, items: zeroReadyDeps.map(d => ({ label: `${d.namespace}/${d.name}`, extra: `0/${d.replicas}`, color: tokens.red })) },
    { title: 'Services — No Endpoints', count: allDeadSvcs.length, color: tokens.red, icon: Network, items: allDeadSvcs.map(s => ({ label: `${s.namespace}/${s.name}`, extra: s.type, color: tokens.red })) },
    { title: 'Moderate Restarts (1-4)', count: highRestartPods.length, color: tokens.orange, icon: RefreshCw, items: highRestartPods.map(p => ({ label: `${p.namespace}/${p.name}`, extra: `${p.restarts}r`, color: tokens.orange })) },
  ]

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {/* Sub-nav */}
      <div style={{ display: 'flex', gap: 4, borderBottom: `0.5px solid ${tokens.separator}`, paddingBottom: 0 }}>
        {sections.map(s => (
          <button key={s.key} onClick={() => setSection(s.key)} style={{ padding: '9px 14px', fontSize: 13, fontWeight: section === s.key ? 600 : 400, border: 'none', borderBottom: section === s.key ? `2px solid ${tokens.blue}` : '2px solid transparent', background: 'transparent', color: section === s.key ? tokens.blue : tokens.secondaryLabel, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 5, whiteSpace: 'nowrap', transition: 'all 0.12s', marginBottom: -1 }}>
            <s.icon style={{ width: 13, height: 13 }} />
            {s.label}
            {s.badge && s.badge > 0 ? <Badge color={tokens.red}>{s.badge}</Badge> : null}
          </button>
        ))}
      </div>

      {/* NS Auto-Scan */}
      {section === 'ns-scan' && (
        <NsScanPanel data={data} events={events} clusterName={clusterName} />
      )}

      {/* Pod Logs */}
      {section === 'pod-logs' && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          <div style={{ background: tokens.secondaryBackground, border: `0.5px solid ${tokens.separator}`, borderRadius: tokens.radius.lg, overflow: 'hidden' }}>
            <div style={{ padding: '14px 18px', borderBottom: `0.5px solid ${tokens.separator}`, display: 'flex', alignItems: 'center', gap: 8 }}>
              <Terminal style={{ width: 16, height: 16, color: tokens.green }} />
              <span style={{ fontSize: 14, fontWeight: 600, color: tokens.label }}>Pod Log Viewer</span>
            </div>
            <div style={{ padding: 18 }}>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 2fr 1fr 80px auto', gap: 10, alignItems: 'end', marginBottom: 12 }}>
                <div>
                  <label style={labelStyle}>Namespace</label>
                  <select value={logNs} onChange={e => { setLogNs(e.target.value); setLogPod('') }} style={selectStyle}>
                    <option value="">Select…</option>
                    {availableNamespaces.map(ns => <option key={ns} value={ns}>{ns}</option>)}
                  </select>
                </div>
                <div>
                  <label style={labelStyle}>Pod</label>
                  <select value={logPod} onChange={e => setLogPod(e.target.value)} style={selectStyle} disabled={!logNs}>
                    <option value="">Select pod…</option>
                    {podsInNs.map(p => <option key={p.name} value={p.name}>{p.name}{p.restarts > 0 ? ` (${p.restarts}r)` : ''}</option>)}
                  </select>
                </div>
                <div>
                  <label style={labelStyle}>Container</label>
                  <select value={logContainer} onChange={e => setLogContainer(e.target.value)} style={selectStyle} disabled={!logPod}>
                    <option value="">All</option>
                    {containersForPod.map(c => <option key={c.name} value={c.name}>{c.name}</option>)}
                  </select>
                </div>
                <div>
                  <label style={labelStyle}>Lines</label>
                  <select value={tailLines} onChange={e => setTailLines(e.target.value)} style={selectStyle}>
                    {['50', '100', '200', '500', '1000'].map(n => <option key={n} value={n}>{n}</option>)}
                  </select>
                </div>
                <button onClick={fetchLogs} disabled={!logPod || logLoading}
                  style={{ padding: '7px 16px', borderRadius: tokens.radius.sm, border: 'none', background: tokens.blue, color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 6, opacity: (!logPod || logLoading) ? 0.5 : 1 }}>
                  {logLoading ? <Loader2 style={{ width: 14, height: 14, animation: 'spin 1s linear infinite' }} /> : <Terminal style={{ width: 14, height: 14 }} />}
                  Fetch
                </button>
              </div>

              {/* Pod diagnosis inline */}
              {selectedPodData && logPod && (
                <div style={{ marginBottom: 12 }}>
                  {autoDiagnose(selectedPodData).map((issue, ii) => <DiagnosisCard key={ii} issue={issue} />)}
                </div>
              )}

              {logError && <div style={{ background: tokens.red + '15', border: `0.5px solid ${tokens.red}30`, borderRadius: tokens.radius.sm, padding: '10px 14px', marginBottom: 12, fontSize: 13, color: tokens.red }}>{logError}</div>}

              {logOutput && (
                <div>
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 6 }}>
                    <span style={{ fontSize: 11, color: tokens.tertiaryLabel, fontFamily: 'monospace' }}>
                      {logPod}{logContainer ? `:${logContainer}` : ''} — last {tailLines} lines
                    </span>
                    <button onClick={copyLogs} style={{ padding: '4px 10px', fontSize: 11, borderRadius: tokens.radius.sm, border: `0.5px solid ${tokens.separator}`, background: tokens.fill, color: tokens.label, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 4 }}>
                      {copied ? <ClipboardCheck style={{ width: 12, height: 12, color: tokens.green }} /> : <Copy style={{ width: 12, height: 12 }} />}
                      {copied ? 'Copied!' : 'Copy'}
                    </button>
                  </div>
                  <pre style={{ background: '#0d1117', color: '#c9d1d9', padding: 16, borderRadius: tokens.radius.md, fontSize: 11, fontFamily: '"SF Mono", "Monaco", monospace', overflowX: 'auto', overflowY: 'auto', maxHeight: 480, margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-all', lineHeight: 1.5 }}>
                    {logOutput}
                  </pre>
                </div>
              )}

              {!logOutput && !logError && !logLoading && (
                <div style={{ textAlign: 'center', padding: '24px 0', color: tokens.tertiaryLabel, fontSize: 13 }}>
                  Select namespace, pod and click Fetch to view logs
                </div>
              )}
            </div>
          </div>
        </div>
      )}

      {/* kubectl Commands */}
      {section === 'kubectl' && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          <div style={{ background: tokens.secondaryBackground, border: `0.5px solid ${tokens.separator}`, borderRadius: tokens.radius.lg, overflow: 'hidden' }}>
            <div style={{ padding: '14px 18px', borderBottom: `0.5px solid ${tokens.separator}`, display: 'flex', alignItems: 'center', gap: 8 }}>
              <Play style={{ width: 16, height: 16, color: tokens.blue }} />
              <span style={{ fontSize: 14, fontWeight: 600, color: tokens.label }}>kubectl Command Generator</span>
            </div>
            <div style={{ padding: 18 }}>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 2fr', gap: 12, marginBottom: 16 }}>
                <div>
                  <label style={labelStyle}>Namespace</label>
                  <select value={logNs} onChange={e => { setLogNs(e.target.value); setLogPod('') }} style={selectStyle}>
                    <option value="">Select…</option>
                    {availableNamespaces.map(ns => <option key={ns} value={ns}>{ns}</option>)}
                  </select>
                </div>
                <div>
                  <label style={labelStyle}>Pod (optional — for pod-level commands)</label>
                  <select value={logPod} onChange={e => setLogPod(e.target.value)} style={selectStyle} disabled={!logNs}>
                    <option value="">Select pod…</option>
                    {podsInNs.map(p => <option key={p.name} value={p.name}>{p.name}</option>)}
                  </select>
                </div>
              </div>
              {logNs ? (
                <KubectlPanel pod={selectedPodData} namespace={logNs} clusterContext={clusterName} />
              ) : (
                <div style={{ textAlign: 'center', padding: '24px 0', color: tokens.tertiaryLabel, fontSize: 13 }}>Select a namespace to generate kubectl commands</div>
              )}
            </div>
          </div>
        </div>
      )}

      {/* SRE Checklist */}
      {section === 'checklist' && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(280px, 1fr))', gap: 12 }}>
            {checklistCards.map(card => {
              const isExpanded = showExpanded[card.title]
              const displayedItems = isExpanded ? card.items : card.items.slice(0, 5)
              const Icon = card.icon
              return (
                <div key={card.title} style={{ background: tokens.secondaryBackground, border: `0.5px solid ${card.count > 0 ? card.color + '40' : tokens.separator}`, borderRadius: tokens.radius.lg, padding: '14px 16px' }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
                    <Icon style={{ width: 15, height: 15, color: card.count > 0 ? card.color : tokens.green }} />
                    <span style={{ fontSize: 13, fontWeight: 600, color: tokens.label, flex: 1 }}>{card.title}</span>
                    {card.count > 0 && <Badge color={card.color}>{card.count}</Badge>}
                  </div>
                  {card.count > 0 ? (
                    <>
                      <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
                        {displayedItems.map((item, ii) => (
                          <div key={ii} style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '4px 6px', borderRadius: tokens.radius.sm, background: tokens.fill }}>
                            <span style={{ fontSize: 11, fontFamily: 'monospace', color: tokens.label, flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{item.label}</span>
                            <span style={{ fontSize: 10, color: item.color, fontWeight: 600, flexShrink: 0 }}>{item.extra}</span>
                          </div>
                        ))}
                      </div>
                      {card.items.length > 5 && (
                        <button onClick={() => setShowExpanded(prev => ({ ...prev, [card.title]: !isExpanded }))}
                          style={{ marginTop: 6, width: '100%', padding: '5px 0', fontSize: 11, borderRadius: tokens.radius.sm, border: `0.5px solid ${tokens.separator}`, background: 'transparent', color: tokens.blue, cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 4, fontWeight: 500 }}>
                          {isExpanded ? <><ChevronUp style={{ width: 11, height: 11 }} /> Collapse</> : <><ChevronDown style={{ width: 11, height: 11 }} /> Show {card.items.length - 5} more</>}
                        </button>
                      )}
                    </>
                  ) : (
                    <div style={{ fontSize: 12, color: tokens.green, display: 'flex', alignItems: 'center', gap: 5 }}>
                      <CheckCircle style={{ width: 12, height: 12 }} /> No issues
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        </div>
      )}
    </div>
  )
}

// ─── Home Screen ──────────────────────────────────────────────────────────────
function HomeScreen({ clusters, allTopologies, allEvents, busyClusters, onSelectCluster }: {
  clusters: ClusterListItem[]
  allTopologies: Record<string, TopologyData>
  allEvents: Record<string, K8sEvent[]>
  busyClusters: Set<string>
  onSelectCluster: (name: string) => void
}) {
  const totalLoaded = Object.keys(allTopologies).length
  const globalStats = useMemo(() => {
    let totalPods = 0, runningPods = 0, totalDeps = 0, healthyDeps = 0, totalWarnings = 0, totalNodes = 0, readyNodes = 0
    for (const [cname, topo] of Object.entries(allTopologies)) {
      totalPods += topo.summary.pods; runningPods += topo.summary.running_pods
      totalDeps += topo.summary.deployments; healthyDeps += topo.summary.healthy_deployments
      totalNodes += topo.summary.nodes; readyNodes += topo.summary.ready_nodes
      totalWarnings += (allEvents[cname] ?? []).filter(e => e.type === 'Warning').length
    }
    return { totalPods, runningPods, totalDeps, healthyDeps, totalWarnings, totalNodes, readyNodes }
  }, [allTopologies, allEvents])

  return (
    <div style={{ maxWidth: 1080, margin: '0 auto', padding: '8px 0 40px' }}>
      <div style={{ textAlign: 'center', padding: '28px 0 32px' }}>
        <div style={{ display: 'flex', justifyContent: 'center', marginBottom: 16 }}>
          <div style={{ width: 60, height: 60, borderRadius: tokens.radius.lg, background: tokens.blue + '18', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
            <Cloud style={{ width: 30, height: 30, color: tokens.blue }} />
          </div>
        </div>
        <h1 style={{ fontSize: 24, fontWeight: 700, color: tokens.label, margin: '0 0 10px' }}>Kubernetes Intelligence</h1>
        <p style={{ fontSize: 14, color: tokens.secondaryLabel, margin: 0 }}>Multi-cluster visibility · Real-time SRE diagnostics · Click any resource for details</p>
      </div>

      {totalLoaded > 0 && (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(150px, 1fr))', gap: 12, marginBottom: 32 }}>
          <StatCard label="Total Pods" value={`${globalStats.runningPods}/${globalStats.totalPods}`} sub={`${totalLoaded} cluster${totalLoaded !== 1 ? 's' : ''} loaded`} color={globalStats.runningPods === globalStats.totalPods ? tokens.green : tokens.orange} icon={Box} />
          <StatCard label="Nodes" value={`${globalStats.readyNodes}/${globalStats.totalNodes}`} sub="Ready" color={globalStats.readyNodes === globalStats.totalNodes ? tokens.green : tokens.orange} icon={Server} />
          <StatCard label="Deployments" value={`${globalStats.healthyDeps}/${globalStats.totalDeps}`} sub="Healthy" color={globalStats.healthyDeps === globalStats.totalDeps ? tokens.green : tokens.orange} icon={GitBranch} />
          <StatCard label="Active Warnings" value={globalStats.totalWarnings} sub="Last 60 minutes" color={globalStats.totalWarnings > 0 ? tokens.orange : tokens.green} icon={AlertTriangle} />
        </div>
      )}

      <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 14 }}>
        <span style={{ fontSize: 14, fontWeight: 600, color: tokens.label }}>Clusters</span>
        <Badge color={tokens.gray}>{clusters.length}</Badge>
        {busyClusters.size > 0 && (
          <span style={{ display: 'flex', alignItems: 'center', gap: 5, fontSize: 12, color: tokens.tertiaryLabel }}>
            <Loader2 style={{ width: 12, height: 12, animation: 'spin 1s linear infinite', color: tokens.blue }} />
            Fetching {busyClusters.size} cluster{busyClusters.size !== 1 ? 's' : ''}…
          </span>
        )}
      </div>

      {clusters.length === 0 ? (
        <div style={{ background: tokens.orange + '15', border: `0.5px solid ${tokens.orange}30`, borderRadius: tokens.radius.md, padding: '20px 24px', fontSize: 13, color: tokens.orange, textAlign: 'center' }}>
          No clusters configured. Add clusters via the K8s Cluster management API.
        </div>
      ) : (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))', gap: 14 }}>
          {clusters.map(cluster => {
            const topo = allTopologies[cluster.name]
            const clusterEvts = allEvents[cluster.name] ?? []
            const isBusy = busyClusters.has(cluster.name)
            const health = topo ? getNsHealthScore(topo.pods, topo.deployments) : null
            const warnings = clusterEvts.filter(e => e.type === 'Warning').length
            const deadSvcCount = topo ? getServicesWithNoEndpoints(topo.services, topo.pods).length : 0
            return (
              <div key={cluster.name} onClick={() => onSelectCluster(cluster.name)}
                style={{ background: tokens.secondaryBackground, border: `0.5px solid ${health ? health.color + '40' : tokens.separator}`, borderRadius: tokens.radius.lg, padding: '16px 18px', cursor: 'pointer', transition: 'box-shadow 0.15s, border-color 0.15s' }}
                onMouseEnter={e => { e.currentTarget.style.boxShadow = '0 4px 18px rgba(0,122,255,0.14)'; e.currentTarget.style.borderColor = tokens.blue + '60' }}
                onMouseLeave={e => { e.currentTarget.style.boxShadow = 'none'; e.currentTarget.style.borderColor = health ? health.color + '40' : tokens.separator }}>
                <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', marginBottom: 10 }}>
                  <div>
                    <div style={{ fontSize: 13, fontWeight: 700, color: tokens.label, fontFamily: 'monospace', marginBottom: 5 }}>{cluster.name}</div>
                    <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap' }}>
                      {cluster.environment && <Badge color={tokens.blue}>{cluster.environment}</Badge>}
                      {cluster.region && <Badge color={tokens.gray}>{cluster.region}</Badge>}
                    </div>
                  </div>
                  {health && <span style={{ fontSize: 26, fontWeight: 800, color: health.color, lineHeight: 1 }}>{health.grade}</span>}
                  {isBusy && !topo && <Loader2 style={{ width: 20, height: 20, color: tokens.blue, animation: 'spin 1s linear infinite' }} />}
                </div>
                {topo ? (
                  <>
                    <div style={{ height: 4, borderRadius: 2, background: tokens.fill, overflow: 'hidden', marginBottom: 10 }}>
                      <div style={{ height: '100%', width: `${health!.score}%`, background: health!.color, borderRadius: 2, transition: 'width 0.4s ease' }} />
                    </div>
                    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 5 }}>
                      {[
                        { label: 'Pods', val: `${topo.summary.running_pods}/${topo.summary.pods}`, ok: topo.summary.running_pods === topo.summary.pods },
                        { label: 'Nodes', val: `${topo.summary.ready_nodes}/${topo.summary.nodes}`, ok: topo.summary.ready_nodes === topo.summary.nodes },
                        { label: 'Deps', val: `${topo.summary.healthy_deployments}/${topo.summary.deployments}`, ok: topo.summary.healthy_deployments === topo.summary.deployments },
                        { label: 'Warns', val: String(warnings), ok: warnings === 0 },
                      ].map(({ label, val, ok }) => (
                        <div key={label} style={{ textAlign: 'center', background: tokens.fill, borderRadius: tokens.radius.sm, padding: '6px 4px' }}>
                          <div style={{ fontSize: 13, fontWeight: 700, color: ok ? tokens.green : tokens.orange }}>{val}</div>
                          <div style={{ fontSize: 10, color: tokens.tertiaryLabel }}>{label}</div>
                        </div>
                      ))}
                    </div>
                    {deadSvcCount > 0 && (
                      <div style={{ marginTop: 8, fontSize: 11, color: tokens.red, display: 'flex', alignItems: 'center', gap: 4 }}>
                        <AlertTriangle style={{ width: 11, height: 11 }} />
                        {deadSvcCount} service{deadSvcCount !== 1 ? 's' : ''} with no endpoints
                      </div>
                    )}
                  </>
                ) : isBusy ? (
                  <div style={{ fontSize: 12, color: tokens.tertiaryLabel, marginTop: 4 }}>Loading cluster data…</div>
                ) : (
                  <div style={{ fontSize: 12, color: tokens.tertiaryLabel, marginTop: 4 }}>Click to load</div>
                )}
              </div>
            )
          })}
        </div>
      )}

      {totalLoaded > 0 && (
        <div style={{ marginTop: 28, background: tokens.blue + '0d', border: `0.5px solid ${tokens.blue}25`, borderRadius: tokens.radius.lg, padding: '14px 20px', display: 'flex', gap: 28, flexWrap: 'wrap' }}>
          {[
            { icon: Search, tip: '⌘K — Global search across all clusters' },
            { icon: Zap, tip: 'Troubleshoot → NS Auto-Scan for instant diagnostics' },
            { icon: Info, tip: 'Click any resource row to view full details' },
          ].map(({ icon: Icon, tip }) => (
            <div key={tip} style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 12, color: tokens.secondaryLabel }}>
              <Icon style={{ width: 13, height: 13, color: tokens.blue, flexShrink: 0 }} />
              {tip}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

// ─── Resource Detail Panel ────────────────────────────────────────────────────
function ResourceDetailPanel({ resource, topology, events, onClose }: {
  resource: SelectedResource
  topology: TopologyData
  events: K8sEvent[]
  onClose: () => void
}) {
  type PanelTab = 'summary' | 'logs' | 'events' | 'kubectl' | 'endpoints' | 'pods' | 'labels' | 'routing'
  const [panelTab, setPanelTab] = useState<PanelTab>('summary')
  const [logs, setLogs] = useState('')
  const [logsLoading, setLogsLoading] = useState(false)
  const [logsError, setLogsError] = useState('')
  const [logContainer, setLogContainer] = useState('')
  const [logsCopied, setLogsCopied] = useState(false)

  const pod = resource.kind === 'Pod' ? topology.pods.find(p => p.name === resource.name && p.namespace === resource.namespace) : undefined
  const service = resource.kind === 'Service' ? topology.services.find(s => s.name === resource.name && s.namespace === resource.namespace) : undefined
  const deployment = resource.kind === 'Deployment' ? topology.deployments.find(d => d.name === resource.name && d.namespace === resource.namespace) : undefined
  const node = resource.kind === 'Node' ? topology.nodes.find(n => n.name === resource.name) : undefined
  const ingress = resource.kind === 'Ingress' ? topology.ingresses.find(i => i.name === resource.name && i.namespace === resource.namespace) : undefined

  const resourceEvents = events.filter(e =>
    e.involved_object.name === resource.name &&
    (!resource.namespace || e.involved_object.namespace === resource.namespace)
  )

  useEffect(() => {
    setPanelTab('summary')
    setLogs(''); setLogsError('')
    if (pod?.containers?.length) setLogContainer(pod.containers[0].name)
  }, [resource.name, resource.namespace, resource.kind])

  const fetchLogs = async () => {
    if (!pod) return
    setLogsLoading(true); setLogsError(''); setLogs('')
    try {
      const url = `/api/v1/k8s-service/api/v1/clusters/${encodeURIComponent(resource.cluster)}/pods/${encodeURIComponent(pod.name)}/logs?namespace=${encodeURIComponent(pod.namespace)}&container=${encodeURIComponent(logContainer)}&tail_lines=200`
      const res = await fetch(url, { headers: authHeader() })
      const json = await res.json()
      if (json.success) setLogs(json.data?.logs ?? '(empty)')
      else setLogsError(json.error ?? 'Unknown error')
    } catch (e: any) { setLogsError(e.message) }
    finally { setLogsLoading(false) }
  }

  const kindColor: Record<string, string> = { Pod: tokens.green, Service: tokens.blue, Deployment: tokens.orange, Node: tokens.purple, Ingress: tokens.pink }
  const color = kindColor[resource.kind] ?? tokens.gray

  const panelTabs: { key: PanelTab; label: string }[] = resource.kind === 'Pod' ? [
    { key: 'summary', label: 'Summary' }, { key: 'logs', label: 'Logs' },
    { key: 'events', label: `Events${resourceEvents.length > 0 ? ` (${resourceEvents.length})` : ''}` }, { key: 'kubectl', label: 'kubectl' },
  ] : resource.kind === 'Service' ? [
    { key: 'summary', label: 'Summary' }, { key: 'endpoints', label: 'Endpoints' },
    { key: 'events', label: `Events${resourceEvents.length > 0 ? ` (${resourceEvents.length})` : ''}` }, { key: 'kubectl', label: 'kubectl' },
  ] : resource.kind === 'Deployment' ? [
    { key: 'summary', label: 'Summary' }, { key: 'pods', label: 'Pods' },
    { key: 'events', label: `Events${resourceEvents.length > 0 ? ` (${resourceEvents.length})` : ''}` }, { key: 'kubectl', label: 'kubectl' },
  ] : resource.kind === 'Node' ? [
    { key: 'summary', label: 'Summary' },
    { key: 'pods', label: `Pods (${topology.pods.filter(p => p.node_name === resource.name).length})` },
    { key: 'labels', label: 'Labels' },
  ] : [
    { key: 'summary', label: 'Summary' }, { key: 'routing', label: 'Routing' },
    { key: 'events', label: `Events${resourceEvents.length > 0 ? ` (${resourceEvents.length})` : ''}` },
  ]

  const fieldBlock = (label: string, content: React.ReactNode) => (
    <div style={{ background: tokens.fill, borderRadius: tokens.radius.sm, padding: '10px 12px' }}>
      <div style={{ fontSize: 10, color: tokens.tertiaryLabel, marginBottom: 5, textTransform: 'uppercase', letterSpacing: '0.04em' }}>{label}</div>
      {content}
    </div>
  )

  const sectionLabel = (text: string) => (
    <div style={{ fontSize: 11, fontWeight: 600, color: tokens.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.04em', marginBottom: 8 }}>{text}</div>
  )

  return (
    <>
      <div onClick={onClose} style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.28)', zIndex: 900 }} />
      <motion.div
        initial={{ x: 560 }} animate={{ x: 0 }} exit={{ x: 560 }}
        transition={{ type: 'spring', damping: 30, stiffness: 300 }}
        style={{ position: 'fixed', right: 0, top: 0, bottom: 0, width: 540, zIndex: 901, background: tokens.background, borderLeft: `0.5px solid ${tokens.separator}`, display: 'flex', flexDirection: 'column', boxShadow: '-8px 0 32px rgba(0,0,0,0.18)' }}
      >
        {/* Header */}
        <div style={{ padding: '14px 20px', borderBottom: `0.5px solid ${tokens.separator}`, background: tokens.secondaryBackground, display: 'flex', alignItems: 'flex-start', gap: 12, flexShrink: 0 }}>
          <div style={{ width: 36, height: 36, borderRadius: tokens.radius.sm, background: color + '18', display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 }}>
            {resource.kind === 'Pod' && <Box style={{ width: 18, height: 18, color }} />}
            {resource.kind === 'Service' && <Network style={{ width: 18, height: 18, color }} />}
            {resource.kind === 'Deployment' && <GitBranch style={{ width: 18, height: 18, color }} />}
            {resource.kind === 'Node' && <Server style={{ width: 18, height: 18, color }} />}
            {resource.kind === 'Ingress' && <Globe style={{ width: 18, height: 18, color }} />}
          </div>
          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 3 }}>
              <Badge color={color}>{resource.kind}</Badge>
              {resource.namespace && <span style={{ fontSize: 11, color: tokens.tertiaryLabel }}>{resource.namespace}</span>}
              <span style={{ fontSize: 11, color: tokens.tertiaryLabel }}>· {resource.cluster}</span>
            </div>
            <div style={{ fontSize: 14, fontWeight: 700, color: tokens.label, fontFamily: 'monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{resource.name}</div>
          </div>
          <button onClick={onClose} style={{ padding: 6, borderRadius: tokens.radius.sm, border: 'none', background: tokens.fill, color: tokens.secondaryLabel, cursor: 'pointer', flexShrink: 0, display: 'flex' }}>
            <XCircle style={{ width: 16, height: 16 }} />
          </button>
        </div>
        {/* Tab bar */}
        <div style={{ display: 'flex', borderBottom: `0.5px solid ${tokens.separator}`, background: tokens.secondaryBackground, flexShrink: 0, overflowX: 'auto' }}>
          {panelTabs.map(t => (
            <button key={t.key} onClick={() => setPanelTab(t.key as PanelTab)} style={{ padding: '9px 16px', fontSize: 12, fontWeight: panelTab === t.key ? 600 : 400, border: 'none', borderBottom: panelTab === t.key ? `2px solid ${tokens.blue}` : '2px solid transparent', background: 'transparent', color: panelTab === t.key ? tokens.blue : tokens.secondaryLabel, cursor: 'pointer', whiteSpace: 'nowrap', marginBottom: -1 }}>
              {t.label}
            </button>
          ))}
        </div>
        {/* Content */}
        <div style={{ flex: 1, overflowY: 'auto', padding: 20 }}>

          {/* ── Summary ── */}
          {panelTab === 'summary' && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
              {pod && (() => {
                const diagIssues = autoDiagnose(pod)
                return (
                  <>
                    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 8 }}>
                      {fieldBlock('Phase', <PhaseBadge phase={pod.phase} />)}
                      {fieldBlock('Restarts', <span style={{ fontSize: 15, fontWeight: 700, color: pod.restarts >= 5 ? tokens.red : pod.restarts > 0 ? tokens.orange : tokens.green }}>{pod.restarts}</span>)}
                      {fieldBlock('Ready', <span style={{ fontSize: 15, fontWeight: 700, color: pod.ready ? tokens.green : tokens.orange }}>{pod.ready ? 'Yes' : 'No'}</span>)}
                    </div>
                    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
                      {fieldBlock('Node', <span style={{ fontSize: 12, fontFamily: 'monospace', color: tokens.label }}>{pod.node_name ?? '—'}</span>)}
                      {fieldBlock('Age', <span style={{ fontSize: 12, color: tokens.label }}>{relativeAge(pod.created_at)}</span>)}
                    </div>
                    <div>{sectionLabel('Diagnosis')}{diagIssues.map((issue, i) => <DiagnosisCard key={i} issue={issue} />)}</div>
                    <div>
                      {sectionLabel(`Containers (${pod.containers?.length ?? 0})`)}
                      {(pod.containers ?? []).map(c => (
                        <div key={c.name} style={{ padding: '10px 12px', borderRadius: tokens.radius.sm, background: tokens.fill, marginBottom: 6, display: 'flex', alignItems: 'flex-start', gap: 10 }}>
                          <Badge color={c.ready ? tokens.green : tokens.orange}>{c.ready ? '●' : '○'}</Badge>
                          <div style={{ flex: 1, minWidth: 0 }}>
                            <div style={{ fontSize: 12, fontWeight: 600, color: tokens.label }}>{c.name}</div>
                            <div style={{ fontSize: 10, color: tokens.tertiaryLabel, fontFamily: 'monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', marginTop: 2 }}>{c.image}</div>
                            {(c.ports ?? []).length > 0 && <div style={{ fontSize: 10, color: tokens.secondaryLabel, marginTop: 2 }}>Ports: {c.ports.map(p => p.container_port).join(', ')}</div>}
                          </div>
                        </div>
                      ))}
                    </div>
                    {Object.keys(pod.labels ?? {}).length > 0 && (
                      <div>
                        {sectionLabel('Labels')}
                        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
                          {Object.entries(pod.labels ?? {}).map(([k, v]) => <span key={k} style={{ fontSize: 10, padding: '2px 7px', borderRadius: 4, background: tokens.blue + '15', color: tokens.blue, fontFamily: 'monospace' }}>{k}={v}</span>)}
                        </div>
                      </div>
                    )}
                  </>
                )
              })()}

              {service && (() => {
                const matchingPods = topology.pods.filter(p => p.namespace === service.namespace && labelsMatch(service.selector, p.labels))
                const readyPods = matchingPods.filter(p => p.ready && p.phase === 'Running')
                const isDead = Object.keys(service.selector ?? {}).length > 0 && readyPods.length === 0
                return (
                  <>
                    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
                      {fieldBlock('Type', <TypeBadge type={service.type} />)}
                      {fieldBlock('Cluster IP', <span style={{ fontSize: 12, fontFamily: 'monospace', color: tokens.label }}>{service.cluster_ip || '—'}</span>)}
                    </div>
                    {isDead && (
                      <div style={{ background: tokens.red + '12', border: `0.5px solid ${tokens.red}30`, borderRadius: tokens.radius.sm, padding: '10px 14px' }}>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, fontWeight: 600, color: tokens.red, marginBottom: 4 }}>
                          <AlertTriangle style={{ width: 13, height: 13 }} /> No healthy endpoints
                        </div>
                        <div style={{ fontSize: 12, color: tokens.secondaryLabel }}>Selector matches {matchingPods.length} pod{matchingPods.length !== 1 ? 's' : ''}, {readyPods.length} ready. Traffic will fail.</div>
                      </div>
                    )}
                    <div>
                      {sectionLabel('Ports')}
                      {(service.ports ?? []).length === 0 ? <span style={{ fontSize: 12, color: tokens.tertiaryLabel }}>No ports</span> :
                        (service.ports ?? []).map((p, i) => (
                          <div key={i} style={{ display: 'flex', gap: 10, fontSize: 12, padding: '6px 10px', borderRadius: tokens.radius.sm, background: tokens.fill, marginBottom: 4, alignItems: 'center' }}>
                            <span style={{ color: tokens.label, fontFamily: 'monospace', fontWeight: 600 }}>{p.port}</span>
                            <span style={{ color: tokens.tertiaryLabel }}>→ {p.target_port}</span>
                            <Badge color={tokens.gray}>{p.protocol}</Badge>
                            {p.name && <span style={{ color: tokens.secondaryLabel }}>{p.name}</span>}
                          </div>
                        ))}
                    </div>
                    <div>
                      {sectionLabel('Selector')}
                      {Object.keys(service.selector ?? {}).length === 0
                        ? <span style={{ fontSize: 12, color: tokens.tertiaryLabel }}>No selector (headless or external)</span>
                        : <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>{Object.entries(service.selector ?? {}).map(([k, v]) => <span key={k} style={{ fontSize: 11, padding: '2px 7px', borderRadius: 4, background: tokens.blue + '15', color: tokens.blue, fontFamily: 'monospace' }}>{k}={v}</span>)}</div>}
                    </div>
                    <div style={{ fontSize: 11, color: tokens.tertiaryLabel }}>Age: {relativeAge(service.created_at)}</div>
                  </>
                )
              })()}

              {deployment && (() => {
                const healthy = deployment.ready_replicas >= deployment.replicas && deployment.replicas > 0
                return (
                  <>
                    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 8 }}>
                      {fieldBlock('Desired', <span style={{ fontSize: 15, fontWeight: 700, color: tokens.label }}>{deployment.replicas}</span>)}
                      {fieldBlock('Ready', <span style={{ fontSize: 15, fontWeight: 700, color: healthy ? tokens.green : tokens.orange }}>{deployment.ready_replicas}</span>)}
                      {fieldBlock('Status', <Badge color={healthy ? tokens.green : tokens.orange}>{healthy ? 'Healthy' : 'Degraded'}</Badge>)}
                    </div>
                    <div>
                      {sectionLabel('Containers')}
                      {(deployment.containers ?? []).map(c => (
                        <div key={c.name} style={{ padding: '8px 12px', borderRadius: tokens.radius.sm, background: tokens.fill, marginBottom: 5 }}>
                          <div style={{ fontSize: 12, fontWeight: 600, color: tokens.label }}>{c.name}</div>
                          <div style={{ fontSize: 10, color: tokens.tertiaryLabel, fontFamily: 'monospace', marginTop: 2, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{c.image}</div>
                        </div>
                      ))}
                    </div>
                    <div>
                      {sectionLabel('Selector')}
                      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
                        {Object.entries(deployment.selector ?? {}).map(([k, v]) => <span key={k} style={{ fontSize: 11, padding: '2px 7px', borderRadius: 4, background: tokens.orange + '18', color: tokens.orange, fontFamily: 'monospace' }}>{k}={v}</span>)}
                      </div>
                    </div>
                    <div style={{ fontSize: 11, color: tokens.tertiaryLabel }}>Age: {relativeAge(deployment.created_at)}</div>
                  </>
                )
              })()}

              {node && (() => {
                const nodePods = topology.pods.filter(p => p.node_name === node.name)
                const runningPods = nodePods.filter(p => p.phase === 'Running').length
                const totalRestarts = nodePods.reduce((s, p) => s + p.restarts, 0)
                return (
                  <>
                    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(2, 1fr)', gap: 8 }}>
                      {fieldBlock('Status', <Badge color={node.ready ? tokens.green : tokens.red}>{node.status}</Badge>)}
                      {fieldBlock('Schedulable', <span style={{ fontSize: 13, color: node.schedulable !== false ? tokens.green : tokens.orange }}>{node.schedulable !== false ? 'Yes' : 'No'}</span>)}
                      {fieldBlock('Pods', <span style={{ fontSize: 13, fontWeight: 700, color: runningPods === nodePods.length ? tokens.green : tokens.orange }}>{runningPods}/{nodePods.length}</span>)}
                      {fieldBlock('Total Restarts', <span style={{ fontSize: 13, fontWeight: 700, color: totalRestarts > 0 ? tokens.orange : tokens.green }}>{totalRestarts}</span>)}
                    </div>
                    <div>
                      {sectionLabel('Roles')}
                      <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
                        {(node.roles ?? []).map(r => <Badge key={r} color={r === 'master' || r === 'control-plane' ? tokens.purple : tokens.gray}>{r}</Badge>)}
                      </div>
                    </div>
                    <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
                      <div style={{ fontSize: 11, color: tokens.tertiaryLabel }}>OS: {node.os ?? '—'}</div>
                      <div style={{ fontSize: 11, color: tokens.tertiaryLabel }}>K8s Version: {node.version ?? '—'}</div>
                    </div>
                  </>
                )
              })()}

              {ingress && (() => {
                const hasTLS = (ingress.tls ?? []).length > 0
                const hosts = (ingress.rules ?? []).map(r => r.host || '*')
                return (
                  <>
                    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
                      {fieldBlock('TLS', <Badge color={hasTLS ? tokens.green : tokens.gray}>{hasTLS ? 'Enabled' : 'None'}</Badge>)}
                      {fieldBlock('Rules', <span style={{ fontSize: 14, fontWeight: 700, color: tokens.label }}>{(ingress.rules ?? []).length}</span>)}
                    </div>
                    <div>
                      {sectionLabel('Hosts')}
                      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 5 }}>
                        {hosts.map((h, i) => <span key={i} style={{ fontSize: 12, fontFamily: 'monospace', color: tokens.label, padding: '2px 8px', borderRadius: 4, background: tokens.pink + '15' }}>{h}</span>)}
                      </div>
                    </div>
                    <div style={{ fontSize: 11, color: tokens.tertiaryLabel }}>Age: {relativeAge(ingress.created_at)}</div>
                  </>
                )
              })()}

              {!pod && !service && !deployment && !node && !ingress && (
                <div style={{ color: tokens.tertiaryLabel, fontSize: 13 }}>Resource not found in current topology</div>
              )}
            </div>
          )}

          {/* ── Logs ── */}
          {panelTab === 'logs' && pod && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
              <div style={{ display: 'flex', gap: 8, alignItems: 'flex-end' }}>
                <div style={{ flex: 1 }}>
                  <label style={{ fontSize: 12, color: tokens.tertiaryLabel, display: 'block', marginBottom: 3 }}>Container</label>
                  <select value={logContainer} onChange={e => setLogContainer(e.target.value)} style={{ width: '100%', padding: '7px 10px', borderRadius: tokens.radius.sm, border: `0.5px solid ${tokens.separator}`, background: tokens.fill, color: tokens.label, fontSize: 12, outline: 'none', cursor: 'pointer' }}>
                    {(pod.containers ?? []).map(c => <option key={c.name} value={c.name}>{c.name}</option>)}
                  </select>
                </div>
                <button onClick={fetchLogs} disabled={logsLoading} style={{ padding: '7px 16px', borderRadius: tokens.radius.sm, border: 'none', background: tokens.blue, color: '#fff', fontSize: 12, fontWeight: 600, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 5, opacity: logsLoading ? 0.6 : 1 }}>
                  {logsLoading ? <Loader2 style={{ width: 13, height: 13, animation: 'spin 1s linear infinite' }} /> : <Terminal style={{ width: 13, height: 13 }} />}
                  Fetch
                </button>
                {logs && (
                  <button onClick={() => { navigator.clipboard.writeText(logs); setLogsCopied(true); setTimeout(() => setLogsCopied(false), 2000) }} style={{ padding: '7px 12px', borderRadius: tokens.radius.sm, border: `0.5px solid ${tokens.separator}`, background: tokens.fill, color: tokens.label, fontSize: 12, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 4 }}>
                    {logsCopied ? <ClipboardCheck style={{ width: 13, height: 13, color: tokens.green }} /> : <Copy style={{ width: 13, height: 13 }} />}
                  </button>
                )}
              </div>
              {logsError && <div style={{ background: tokens.red + '15', borderRadius: tokens.radius.sm, padding: '10px 14px', fontSize: 12, color: tokens.red }}>{logsError}</div>}
              {logs ? (
                <pre style={{ background: '#0d1117', color: '#c9d1d9', padding: 14, borderRadius: tokens.radius.md, fontSize: 10, fontFamily: '"SF Mono", monospace', overflowX: 'auto', overflowY: 'auto', maxHeight: 520, margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-all', lineHeight: 1.5 }}>
                  {logs}
                </pre>
              ) : !logsLoading && !logsError && (
                <div style={{ textAlign: 'center', padding: '28px 0', color: tokens.tertiaryLabel, fontSize: 13 }}>Click "Fetch" to load container logs</div>
              )}
            </div>
          )}

          {/* ── Events ── */}
          {panelTab === 'events' && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
              {resourceEvents.length === 0
                ? <div style={{ textAlign: 'center', padding: '28px 0', color: tokens.tertiaryLabel, fontSize: 13 }}>No events found for this resource</div>
                : resourceEvents.map((ev, i) => (
                  <div key={i} style={{ padding: '10px 14px', borderRadius: tokens.radius.sm, background: ev.type === 'Warning' ? tokens.orange + '0d' : tokens.fill, border: `0.5px solid ${ev.type === 'Warning' ? tokens.orange + '30' : tokens.separator}`, display: 'flex', flexDirection: 'column', gap: 4 }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                      <Badge color={ev.type === 'Warning' ? tokens.orange : tokens.blue}>{ev.type}</Badge>
                      <span style={{ fontSize: 12, fontWeight: 600, color: ev.type === 'Warning' ? tokens.orange : tokens.label }}>{ev.reason}</span>
                      {ev.count && ev.count > 1 && <span style={{ fontSize: 11, color: tokens.tertiaryLabel }}>{ev.count}×</span>}
                      <span style={{ fontSize: 11, color: tokens.tertiaryLabel, marginLeft: 'auto' }}>{new Date(ev.last_timestamp).toLocaleTimeString()}</span>
                    </div>
                    <div style={{ fontSize: 12, color: tokens.secondaryLabel }}>{ev.message}</div>
                  </div>
                ))
              }
            </div>
          )}

          {/* ── kubectl ── */}
          {panelTab === 'kubectl' && (
            pod ? <KubectlPanel pod={pod} namespace={pod.namespace} clusterContext={resource.cluster} /> :
            service ? <KubectlPanel namespace={service.namespace} clusterContext={resource.cluster} /> :
            deployment ? <KubectlPanel namespace={deployment.namespace} clusterContext={resource.cluster} /> :
            <div style={{ textAlign: 'center', padding: '28px 0', color: tokens.tertiaryLabel, fontSize: 13 }}>No kubectl commands available</div>
          )}

          {/* ── Endpoints ── */}
          {panelTab === 'endpoints' && service && (() => {
            const matchingPods = topology.pods.filter(p => p.namespace === service.namespace && labelsMatch(service.selector, p.labels))
            const readyPods = matchingPods.filter(p => p.ready && p.phase === 'Running')
            return (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                <div style={{ padding: '10px 14px', borderRadius: tokens.radius.sm, background: readyPods.length > 0 ? tokens.green + '0d' : tokens.red + '0d', border: `0.5px solid ${readyPods.length > 0 ? tokens.green + '30' : tokens.red + '30'}`, display: 'flex', alignItems: 'center', gap: 8 }}>
                  {readyPods.length > 0 ? <CheckCircle style={{ width: 14, height: 14, color: tokens.green }} /> : <AlertTriangle style={{ width: 14, height: 14, color: tokens.red }} />}
                  <span style={{ fontSize: 13, fontWeight: 600, color: readyPods.length > 0 ? tokens.green : tokens.red }}>{readyPods.length}/{matchingPods.length} pods ready</span>
                </div>
                {matchingPods.length === 0
                  ? <div style={{ padding: '12px 14px', borderRadius: tokens.radius.sm, background: tokens.orange + '0d', border: `0.5px solid ${tokens.orange}30`, fontSize: 13, color: tokens.orange }}>
                      No pods match selector: {Object.entries(service.selector ?? {}).map(([k, v]) => `${k}=${v}`).join(', ')}
                    </div>
                  : matchingPods.map(p => (
                    <div key={p.name} style={{ padding: '10px 14px', borderRadius: tokens.radius.sm, background: tokens.fill, display: 'flex', alignItems: 'center', gap: 10 }}>
                      <div style={{ width: 8, height: 8, borderRadius: '50%', background: p.ready && p.phase === 'Running' ? tokens.green : tokens.orange, flexShrink: 0 }} />
                      <div style={{ flex: 1, minWidth: 0 }}>
                        <div style={{ fontSize: 12, fontFamily: 'monospace', color: tokens.label, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{p.name}</div>
                        <div style={{ fontSize: 10, color: tokens.tertiaryLabel }}>Node: {p.node_name ?? '—'}</div>
                      </div>
                      <PhaseBadge phase={p.phase} />
                      {p.restarts > 0 && <Badge color={tokens.orange}>{p.restarts}r</Badge>}
                    </div>
                  ))
                }
              </div>
            )
          })()}

          {/* ── Pods list (for Deployment / Node) ── */}
          {panelTab === 'pods' && (deployment || node) && (() => {
            const podList = deployment
              ? topology.pods.filter(p => p.namespace === deployment.namespace && labelsMatch(deployment.selector, p.labels))
              : topology.pods.filter(p => p.node_name === node!.name)
            if (podList.length === 0) return <div style={{ textAlign: 'center', padding: '28px 0', color: tokens.tertiaryLabel, fontSize: 13 }}>No pods found</div>
            return (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                {podList.map(p => (
                  <div key={p.name} style={{ padding: '10px 14px', borderRadius: tokens.radius.sm, background: tokens.fill, display: 'flex', alignItems: 'center', gap: 10 }}>
                    <div style={{ width: 8, height: 8, borderRadius: '50%', background: p.ready && p.phase === 'Running' ? tokens.green : p.phase === 'Pending' ? tokens.orange : tokens.red, flexShrink: 0 }} />
                    <div style={{ flex: 1, minWidth: 0 }}>
                      <div style={{ fontSize: 12, fontFamily: 'monospace', color: tokens.label, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{p.name}</div>
                      <div style={{ fontSize: 10, color: tokens.tertiaryLabel }}>{node ? p.namespace : `Node: ${p.node_name ?? '—'}`}</div>
                    </div>
                    <PhaseBadge phase={p.phase} />
                    {p.restarts > 0 && <Badge color={p.restarts >= 5 ? tokens.red : tokens.orange}>{p.restarts}r</Badge>}
                  </div>
                ))}
              </div>
            )
          })()}

          {/* ── Labels ── */}
          {panelTab === 'labels' && node && (
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 5 }}>
              {Object.entries(node.labels ?? {}).map(([k, v]) => (
                <span key={k} style={{ fontSize: 11, padding: '3px 8px', borderRadius: 4, background: tokens.purple + '15', color: tokens.purple, fontFamily: 'monospace' }}>{k}={v}</span>
              ))}
              {Object.keys(node.labels ?? {}).length === 0 && <span style={{ fontSize: 13, color: tokens.tertiaryLabel }}>No labels</span>}
            </div>
          )}

          {/* ── Routing (Ingress) ── */}
          {panelTab === 'routing' && ingress && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
              {(ingress.rules ?? []).length === 0
                ? <div style={{ textAlign: 'center', padding: '28px 0', color: tokens.tertiaryLabel, fontSize: 13 }}>No routing rules</div>
                : (ingress.rules ?? []).map((rule, ri) => (
                  <div key={ri}>
                    <div style={{ fontSize: 12, fontWeight: 700, color: tokens.pink, marginBottom: 8, fontFamily: 'monospace' }}>{rule.host || '*'}</div>
                    {(rule.paths ?? []).map((path, pi) => {
                      const svc = topology.services.find(s => s.name === path.service_name && s.namespace === ingress.namespace)
                      const pods = svc ? topology.pods.filter(p => p.namespace === svc.namespace && labelsMatch(svc.selector, p.labels)) : []
                      const readyPods = pods.filter(p => p.ready && p.phase === 'Running')
                      return (
                        <div key={pi} style={{ marginLeft: 14, marginBottom: 10 }}>
                          <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap', marginBottom: 5 }}>
                            <span style={{ fontSize: 11, color: tokens.secondaryLabel, fontFamily: 'monospace', background: tokens.fill, padding: '2px 8px', borderRadius: 4 }}>{path.path}</span>
                            <span style={{ color: tokens.tertiaryLabel }}>→</span>
                            <Badge color={tokens.blue}>svc:{path.service_name}:{path.service_port}</Badge>
                            <span style={{ color: tokens.tertiaryLabel }}>→</span>
                            <Badge color={readyPods.length > 0 ? tokens.green : tokens.red}>{readyPods.length}/{pods.length} pods</Badge>
                          </div>
                          {pods.length > 0 && (
                            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4, marginLeft: 4 }}>
                              {pods.slice(0, 5).map(p => (
                                <span key={p.name} style={{ fontSize: 10, padding: '2px 6px', borderRadius: 3, background: (p.ready ? tokens.green : tokens.orange) + '18', color: p.ready ? tokens.green : tokens.orange, fontFamily: 'monospace' }}>{truncate(p.name, 22)}</span>
                              ))}
                              {pods.length > 5 && <span style={{ fontSize: 10, color: tokens.tertiaryLabel }}>+{pods.length - 5} more</span>}
                            </div>
                          )}
                        </div>
                      )
                    })}
                  </div>
                ))
              }
            </div>
          )}
        </div>
      </motion.div>
    </>
  )
}

// ─── Sidebar ──────────────────────────────────────────────────────────────────
function Sidebar({
  clusters, selectedCluster, onSelectCluster,
  namespaces, selectedNs, onSelectNs,
  activeTab, onTabChange,
}: {
  clusters: ClusterListItem[]; selectedCluster: string; onSelectCluster: (name: string) => void
  namespaces: string[]; selectedNs: string; onSelectNs: (ns: string) => void
  activeTab: TabKey; onTabChange: (t: TabKey) => void
}) {
  const tabs: { key: TabKey; label: string; icon: React.ComponentType<any> }[] = [
    { key: 'overview', label: 'Overview', icon: Activity },
    { key: 'workloads', label: 'Workloads', icon: Box },
    { key: 'network', label: 'Network', icon: Network },
    { key: 'topology', label: 'Topology', icon: GitBranch },
    { key: 'nodes', label: 'Nodes', icon: Server },
    { key: 'storage', label: 'Storage / PVCs', icon: HardDrive },
    { key: 'events', label: 'Events', icon: Clock },
    { key: 'troubleshoot', label: 'Troubleshoot', icon: Wrench },
  ]

  const selStyle: React.CSSProperties = { width: '100%', padding: '7px 10px', boxSizing: 'border-box', borderRadius: tokens.radius.sm, border: `0.5px solid ${tokens.separator}`, background: tokens.fill, color: tokens.label, fontSize: 12, outline: 'none', cursor: 'pointer' }

  return (
    <div style={{ width: 240, flexShrink: 0, background: tokens.secondaryBackground, borderRight: `0.5px solid ${tokens.separator}`, display: 'flex', flexDirection: 'column', position: 'sticky', top: 0, height: '100vh', overflowY: 'auto' }}>
      <div style={{ padding: '20px 16px 14px', borderBottom: `0.5px solid ${tokens.separator}` }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <Cloud style={{ width: 20, height: 20, color: tokens.blue }} />
          <span style={{ fontSize: 15, fontWeight: 700, color: tokens.label }}>K8s Intelligence</span>
        </div>
      </div>

      <div style={{ padding: '14px 16px', borderBottom: `0.5px solid ${tokens.separator}` }}>
        <label style={{ fontSize: 11, fontWeight: 600, color: tokens.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.05em', display: 'block', marginBottom: 6 }}>Cluster</label>
        <div style={{ position: 'relative' }}>
          <select value={selectedCluster} onChange={e => onSelectCluster(e.target.value)} style={selStyle}>
            <option value="">Select cluster…</option>
            {clusters.map(c => <option key={c.name} value={c.name}>{c.name}</option>)}
          </select>
          <ChevronDown style={{ position: 'absolute', right: 8, top: '50%', transform: 'translateY(-50%)', width: 12, height: 12, color: tokens.gray, pointerEvents: 'none' }} />
        </div>
        {selectedCluster && clusters.find(c => c.name === selectedCluster) && (
          <div style={{ marginTop: 6, display: 'flex', gap: 4 }}>
            <Badge color={tokens.blue}>{clusters.find(c => c.name === selectedCluster)?.environment ?? ''}</Badge>
            <Badge color={tokens.gray}>{clusters.find(c => c.name === selectedCluster)?.region ?? ''}</Badge>
          </div>
        )}
      </div>

      <div style={{ padding: '14px 16px', borderBottom: `0.5px solid ${tokens.separator}` }}>
        <label style={{ fontSize: 11, fontWeight: 600, color: tokens.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.05em', display: 'block', marginBottom: 6 }}>Namespace</label>
        <div style={{ position: 'relative' }}>
          <select value={selectedNs} onChange={e => onSelectNs(e.target.value)} style={selStyle}>
            <option value="">All namespaces</option>
            {namespaces.map(ns => <option key={ns} value={ns}>{ns}</option>)}
          </select>
          <ChevronDown style={{ position: 'absolute', right: 8, top: '50%', transform: 'translateY(-50%)', width: 12, height: 12, color: tokens.gray, pointerEvents: 'none' }} />
        </div>
      </div>

      <nav style={{ padding: '10px 8px', flex: 1 }}>
        {tabs.map(tab => (
          <button key={tab.key} onClick={() => onTabChange(tab.key)} style={{ display: 'flex', alignItems: 'center', gap: 8, width: '100%', padding: '8px 10px', borderRadius: tokens.radius.sm, border: 'none', background: activeTab === tab.key ? tokens.blue + '18' : 'transparent', color: activeTab === tab.key ? tokens.blue : tokens.label, fontSize: 13, fontWeight: activeTab === tab.key ? 600 : 400, cursor: 'pointer', textAlign: 'left', transition: 'all 0.12s', marginBottom: 2 }}>
            <tab.icon style={{ width: 15, height: 15, flexShrink: 0 }} />
            {tab.label}
          </button>
        ))}
      </nav>
    </div>
  )
}

// ─── Main Page ────────────────────────────────────────────────────────────────
const KubernetesManagementPage: React.FC = () => {
  const [clusters, setClusters] = useState<ClusterListItem[]>([])
  const [selectedCluster, setSelectedCluster] = useState('')
  const [topology, setTopology] = useState<TopologyData | null>(null)
  const [events, setEvents] = useState<K8sEvent[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [lastRefreshed, setLastRefreshed] = useState<Date | null>(null)
  const [activeTab, setActiveTab] = useState<TabKey>('overview')
  const [selectedNs, setSelectedNs] = useState('')
  const [logsTarget, setLogsTarget] = useState<{ ns: string; pod: string } | null>(null)
  const [allTopologies, setAllTopologies] = useState<Record<string, TopologyData>>({})
  const [allEvents, setAllEvents] = useState<Record<string, K8sEvent[]>>({})
  const [busyClusters, setBusyClusters] = useState<Set<string>>(new Set())
  const [selectedResource, setSelectedResource] = useState<SelectedResource | null>(null)
  const eventsTimerRef = useRef<ReturnType<typeof setInterval> | null>(null)

  useEffect(() => {
    const fetchClusters = async () => {
      try {
        const res = await fetch('/api/v1/topology/k8s-clusters', { headers: authHeader() })
        const json = await res.json()
        if (json.success) {
          const raw: any[] = json.data?.clusters ?? []
          setClusters(raw.map(c => ({ name: c.name ?? c.cluster_name, environment: c.environment, region: c.region, enabled: c.enabled !== false })))
        }
      } catch {}
    }
    fetchClusters()
  }, [])

  // Background-fetch all cluster topologies for global search
  useEffect(() => {
    if (clusters.length === 0) return
    clusters.forEach(async cluster => {
      if (allTopologies[cluster.name]) return
      setBusyClusters(prev => new Set([...prev, cluster.name]))
      try {
        const [topoRes, evtRes] = await Promise.all([
          fetch(`/api/v1/k8s-service/api/v1/clusters/${encodeURIComponent(cluster.name)}/topology`, { headers: authHeader() }),
          fetch(`/api/v1/k8s-service/api/v1/clusters/${encodeURIComponent(cluster.name)}/events?since_minutes=60`, { headers: authHeader() }),
        ])
        const [topoJson, evtJson] = await Promise.all([topoRes.json(), evtRes.json()])
        if (topoJson.success) setAllTopologies(prev => ({ ...prev, [cluster.name]: normalizeTopology(topoJson.data) }))
        if (evtJson.success) setAllEvents(prev => ({ ...prev, [cluster.name]: evtJson.data?.events ?? [] }))
      } catch {}
      setBusyClusters(prev => { const s = new Set(prev); s.delete(cluster.name); return s })
    })
  }, [clusters])

  const fetchTopology = useCallback(async (name: string) => {
    if (!name) return
    setLoading(true); setError('')
    try {
      const res = await fetch(`/api/v1/k8s-service/api/v1/clusters/${encodeURIComponent(name)}/topology`, { headers: authHeader() })
      const json = await res.json()
      if (json.success) {
        const data = normalizeTopology(json.data)
        setTopology(data); setLastRefreshed(new Date())
        setAllTopologies(prev => ({ ...prev, [name]: data }))
      } else setError(json.error ?? 'Failed to load topology')
    } catch (e: any) {
      setError(e.message)
    } finally {
      setLoading(false)
    }
  }, [])

  const fetchEvents = useCallback(async (name: string) => {
    if (!name) return
    try {
      const res = await fetch(`/api/v1/k8s-service/api/v1/clusters/${encodeURIComponent(name)}/events?since_minutes=60`, { headers: authHeader() })
      const json = await res.json()
      if (json.success) {
        const evts = json.data?.events ?? []
        setEvents(evts)
        setAllEvents(prev => ({ ...prev, [name]: evts }))
      }
    } catch {}
  }, [])

  useEffect(() => {
    if (!selectedCluster) return
    setTopology(null); setEvents([]); setSelectedNs('')
    fetchTopology(selectedCluster)
    fetchEvents(selectedCluster)
    if (eventsTimerRef.current) clearInterval(eventsTimerRef.current)
    eventsTimerRef.current = setInterval(() => fetchEvents(selectedCluster), 30000)
    return () => { if (eventsTimerRef.current) clearInterval(eventsTimerRef.current) }
  }, [selectedCluster, fetchTopology, fetchEvents])

  const handleGetLogs = (ns: string, pod: string) => {
    setLogsTarget({ ns, pod })
    setActiveTab('troubleshoot')
  }

  const handleSearchNavigate = (cluster: string, tab: TabKey, ns?: string) => {
    if (cluster && cluster !== selectedCluster) {
      setSelectedCluster(cluster)
      // topology for this cluster may already be cached; if so, show it immediately
      if (allTopologies[cluster]) setTopology(allTopologies[cluster])
      if (allEvents[cluster]) setEvents(allEvents[cluster])
    }
    setActiveTab(tab)
    if (ns) setSelectedNs(ns)
    else setSelectedNs('')
  }

  const namespaces = topology ? Array.from(new Set(topology.namespaces?.map(n => n.name) ?? [])).sort() : []

  const tabs: { key: TabKey; label: string }[] = [
    { key: 'overview', label: 'Overview' },
    { key: 'workloads', label: 'Workloads' },
    { key: 'network', label: 'Network' },
    { key: 'topology', label: 'Topology' },
    { key: 'nodes', label: 'Nodes' },
    { key: 'events', label: 'Events' },
    { key: 'troubleshoot', label: 'Troubleshoot' },
  ]

  return (
    <div style={{ display: 'flex', height: '100vh', background: tokens.background, overflow: 'hidden' }}>
      <style>{`
        @keyframes spin { from { transform: rotate(0deg) } to { transform: rotate(360deg) } }
        * { box-sizing: border-box; }
      `}</style>

      <Sidebar
        clusters={clusters} selectedCluster={selectedCluster}
        onSelectCluster={name => { setSelectedCluster(name); setActiveTab('overview') }}
        namespaces={namespaces} selectedNs={selectedNs} onSelectNs={setSelectedNs}
        activeTab={activeTab} onTabChange={setActiveTab}
      />

      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
        {/* Header */}
        <div style={{ padding: '12px 24px', borderBottom: `0.5px solid ${tokens.separator}`, background: tokens.secondaryBackground, display: 'flex', alignItems: 'center', gap: 16, flexShrink: 0 }}>
          <div style={{ minWidth: 160 }}>
            <h1 style={{ fontSize: 16, fontWeight: 700, color: tokens.label, margin: 0, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
              {selectedCluster || 'K8s Intelligence'}
            </h1>
            {lastRefreshed && (
              <div style={{ fontSize: 10, color: tokens.tertiaryLabel, marginTop: 1 }}>
                Refreshed {lastRefreshed.toLocaleTimeString()}
              </div>
            )}
          </div>

          {/* Global Search — always visible, searches all clusters */}
          <GlobalSearch
            allTopologies={allTopologies}
            allEvents={allEvents}
            onNavigate={handleSearchNavigate}
            busyClusters={busyClusters}
          />

          <div style={{ display: 'flex', gap: 8, flexShrink: 0 }}>
            {selectedCluster && (
              <button onClick={() => { fetchTopology(selectedCluster); fetchEvents(selectedCluster) }} disabled={loading}
                style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '7px 14px', borderRadius: tokens.radius.sm, border: `0.5px solid ${tokens.separator}`, background: tokens.fill, color: tokens.label, fontSize: 13, fontWeight: 500, cursor: 'pointer', opacity: loading ? 0.6 : 1, whiteSpace: 'nowrap' }}>
                <RefreshCw style={{ width: 13, height: 13, animation: loading ? 'spin 1s linear infinite' : 'none' }} />
                Refresh
              </button>
            )}
          </div>
        </div>

        {/* Topology summary bar */}
        {topology && !loading && (
          <div style={{ padding: '6px 24px', borderBottom: `0.5px solid ${tokens.separator}`, background: tokens.secondaryBackground, display: 'flex', gap: 16, alignItems: 'center', flexShrink: 0, overflowX: 'auto' }}>
            {[
              { label: 'Pods', value: `${topology.summary.running_pods}/${topology.summary.pods}`, ok: topology.summary.running_pods === topology.summary.pods, color: tokens.green },
              { label: 'Nodes', value: `${topology.summary.ready_nodes}/${topology.summary.nodes}`, ok: topology.summary.ready_nodes === topology.summary.nodes, color: tokens.blue },
              { label: 'Deploys', value: `${topology.summary.healthy_deployments}/${topology.summary.deployments}`, ok: topology.summary.healthy_deployments === topology.summary.deployments, color: tokens.orange },
            ].map(s => (
              <div key={s.label} style={{ display: 'flex', alignItems: 'center', gap: 5, whiteSpace: 'nowrap' }}>
                <div style={{ width: 6, height: 6, borderRadius: '50%', background: s.ok ? tokens.green : tokens.orange }} />
                <span style={{ fontSize: 12, color: tokens.tertiaryLabel }}>{s.label}</span>
                <span style={{ fontSize: 12, fontWeight: 600, color: s.ok ? tokens.green : tokens.orange }}>{s.value}</span>
              </div>
            ))}
            <div style={{ width: 1, height: 14, background: tokens.separator }} />
            <span style={{ fontSize: 11, color: tokens.tertiaryLabel, whiteSpace: 'nowrap' }}>
              {events.filter(e => e.type === 'Warning').length} warnings in last 60m
            </span>
          </div>
        )}

        {/* Tab bar */}
        {selectedCluster && topology && (
          <div style={{ padding: '0 24px', borderBottom: `0.5px solid ${tokens.separator}`, background: tokens.secondaryBackground, display: 'flex', gap: 0, flexShrink: 0, overflowX: 'auto' }}>
            {tabs.map(tab => (
              <button key={tab.key} onClick={() => setActiveTab(tab.key)} style={{ padding: '10px 16px', fontSize: 13, fontWeight: activeTab === tab.key ? 600 : 400, border: 'none', borderBottom: activeTab === tab.key ? `2px solid ${tokens.blue}` : '2px solid transparent', background: 'transparent', color: activeTab === tab.key ? tokens.blue : tokens.secondaryLabel, cursor: 'pointer', whiteSpace: 'nowrap', transition: 'all 0.12s' }}>
                {tab.label}
              </button>
            ))}
          </div>
        )}

        {/* Content */}
        <div style={{ flex: 1, overflowY: 'auto', padding: '20px 24px' }}>
          {!selectedCluster && (
            <HomeScreen
              clusters={clusters}
              allTopologies={allTopologies}
              allEvents={allEvents}
              busyClusters={busyClusters}
              onSelectCluster={name => { setSelectedCluster(name); setActiveTab('overview') }}
            />
          )}

          {selectedCluster && loading && !topology && <Spinner />}

          {error && (
            <motion.div initial={{ opacity: 0, y: -8 }} animate={{ opacity: 1, y: 0 }}
              style={{ background: tokens.red + '15', border: `0.5px solid ${tokens.red}30`, borderRadius: tokens.radius.md, padding: '14px 18px', marginBottom: 16, display: 'flex', alignItems: 'center', gap: 10 }}>
              <XCircle style={{ width: 16, height: 16, color: tokens.red, flexShrink: 0 }} />
              <span style={{ fontSize: 13, color: tokens.red }}>{error}</span>
            </motion.div>
          )}

          {topology && !loading && (
            <AnimatePresence mode="wait">
              <motion.div key={activeTab} initial={{ opacity: 0, y: 6 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0, y: -4 }} transition={{ duration: 0.15 }}>
                {activeTab === 'overview' && <OverviewTab data={topology} events={events} onResourceClick={setSelectedResource} />}
                {activeTab === 'workloads' && <WorkloadsTab data={topology} nsFilter={selectedNs} onGetLogs={handleGetLogs} onResourceClick={setSelectedResource} />}
                {activeTab === 'network' && <NetworkTab data={topology} nsFilter={selectedNs} onResourceClick={setSelectedResource} />}
                {activeTab === 'topology' && <TopologyTab data={topology} nsFilter={selectedNs} />}
                {activeTab === 'nodes' && <NodesTab data={topology} onResourceClick={setSelectedResource} />}
                {activeTab === 'storage' && <StorageTab data={topology} />}
                {activeTab === 'events' && <EventsTab clusterName={selectedCluster} events={events} onRefresh={() => fetchEvents(selectedCluster)} />}
                {activeTab === 'troubleshoot' && (
                  <TroubleshootTab
                    data={topology}
                    events={events}
                    clusterName={selectedCluster}
                    initialPodNs={logsTarget?.ns}
                    initialPodName={logsTarget?.pod}
                  />
                )}
              </motion.div>
            </AnimatePresence>
          )}
        </div>
      </div>

      {/* Resource Detail Panel */}
      <AnimatePresence>
        {selectedResource && topology && (
          <ResourceDetailPanel
            resource={selectedResource}
            topology={topology}
            events={events}
            onClose={() => setSelectedResource(null)}
          />
        )}
      </AnimatePresence>
    </div>
  )
}

export default KubernetesManagementPage
