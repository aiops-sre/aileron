import React, { useState, useEffect, useCallback, useMemo } from 'react'
import { motion, AnimatePresence } from 'framer-motion'
import {
  TrendingUp, TrendingDown, AlertTriangle, CheckCircle, Server, Box,
  Cpu, HardDrive, Cloud, Zap, ChevronRight, ChevronDown, ChevronUp,
  RefreshCw, Copy, ClipboardCheck, Loader2, Globe, GitBranch,
  Target, Lightbulb, Activity, BarChart2, Layers, Filter,
} from 'lucide-react'

// ─── Apple Design Tokens ─────────────────────────────────────────────────────
const apple = {
  blue: '#007AFF', green: '#34C759', red: '#FF3B30', orange: '#FF9500',
  yellow: '#FFCC00', purple: '#AF52DE', pink: '#FF2D55', gray: '#8E8E93',
  teal: '#5AC8FA', indigo: '#5856D6',
  label: 'var(--color-text)', secondaryLabel: 'var(--color-text-secondary)',
  tertiaryLabel: 'var(--color-text-tertiary, #8E8E93)',
  separator: 'var(--color-separator, rgba(142, 142, 147, 0.12))',
  fill: 'var(--color-fill, rgba(142, 142, 147, 0.08))',
  tertiaryFill: 'rgba(142, 142, 147, 0.06)',
  background: 'var(--color-background)',
  secondaryBackground: 'var(--color-card, rgba(255, 255, 255, 0.8))',
  radius: { sm: 6, md: 10, lg: 12, xl: 16 },
} as const

// ─── Types ───────────────────────────────────────────────────────────────────
interface K8sResources {
  requests?: Record<string, string>
  limits?: Record<string, string>
}
interface K8sContainer { name: string; image: string; resources?: K8sResources; ready: boolean }
interface K8sPod {
  name: string; namespace: string; phase: string; node_name: string
  containers: K8sContainer[]; ready: boolean; restarts: number
}
interface K8sDeployment {
  name: string; namespace: string; replicas: number; ready_replicas: number
  containers: K8sContainer[]; selector: Record<string, string>
}
interface K8sNode {
  name: string; status: string; ready: boolean; roles: string[]
  capacity?: Record<string, string>; allocatable?: Record<string, string>
}
interface ClusterTopology {
  cluster: string
  pods: K8sPod[]; deployments: K8sDeployment[]; nodes: K8sNode[]
  summary: { pods: number; running_pods: number; nodes: number; ready_nodes: number; deployments: number }
}
interface ClusterListItem { name: string; environment: string; region: string }
interface InfraNode {
  id: string; name: string; type: string; layer: string; status: string
  health_status?: string
  dependents?: string[]
  properties: Record<string, any>
}
interface CloudStackRegion { region: string; layers: Record<string, InfraNode[]>; stats: Record<string, any> }

interface ContainerCapacityItem {
  cluster: string; namespace: string; deployment: string; pod: string; container: string
  cpuRequest: number; cpuLimit: number; memRequestMi: number; memLimitMi: number
  cpuWasteRatio: number; memWasteRatio: number; overallWasteScore: number
  suggestedCpuLimit: string; suggestedMemLimit: string
  patchCmd: string
}

interface NodeCapacityItem {
  cluster: string; name: string
  cpuCores: number; memGi: number; podCapacity: number
  podCount: number; podUtilPct: number
  roles: string[]
}

interface CloudStackHostItem {
  region: string; name: string; type: string
  cpuCores: number; memTotalGi: number; memAllocatedGi: number; memUsedPct: number
  vmCount: number
}

// ─── Resource Parsing ─────────────────────────────────────────────────────────
function parseCpuToMillis(s: string | undefined): number {
  if (!s) return 0
  if (s.endsWith('m')) return parseFloat(s)
  return parseFloat(s) * 1000
}

function parseMemToMi(s: string | undefined): number {
  if (!s) return 0
  if (s.endsWith('Ki')) return parseFloat(s) / 1024
  if (s.endsWith('Mi')) return parseFloat(s)
  if (s.endsWith('Gi')) return parseFloat(s) * 1024
  if (s.endsWith('Ti')) return parseFloat(s) * 1024 * 1024
  // plain bytes
  const n = parseFloat(s)
  return n / (1024 * 1024)
}

function parseCpuFromNode(s: string | undefined): number {
  if (!s) return 0
  return parseFloat(s)
}

function parseMemGiFromNode(s: string | undefined): number {
  if (!s) return 0
  if (s.endsWith('Ki')) return parseFloat(s) / (1024 * 1024)
  if (s.endsWith('Mi')) return parseFloat(s) / 1024
  if (s.endsWith('Gi')) return parseFloat(s)
  if (s.endsWith('Ti')) return parseFloat(s) * 1024
  return parseFloat(s) / (1024 * 1024 * 1024)
}

function formatCpu(millis: number): string {
  if (millis >= 1000) return `${(millis / 1000).toFixed(2)}`
  return `${Math.round(millis)}m`
}

function formatMem(mi: number): string {
  if (mi >= 1024) return `${(mi / 1024).toFixed(1)}Gi`
  return `${Math.round(mi)}Mi`
}

const authHeader = () => ({
  Authorization: `Bearer ${sessionStorage.getItem('access_token') || ''}`,
  'Content-Type': 'application/json',
})

// ─── Small UI helpers ─────────────────────────────────────────────────────────
function Badge({ color, children }: { color: string; children: React.ReactNode }) {
  return <span style={{ display: 'inline-block', fontSize: 11, fontWeight: 600, padding: '2px 7px', borderRadius: 4, background: color + '22', color }}>{children}</span>
}

function Spinner() {
  return <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', padding: 48 }}><Loader2 style={{ width: 28, height: 28, color: apple.blue, animation: 'spin 1s linear infinite' }} /></div>
}

function WasteBar({ pct, color }: { pct: number; color: string }) {
  return (
    <div style={{ height: 5, borderRadius: 3, background: apple.fill, overflow: 'hidden', flex: 1 }}>
      <div style={{ height: '100%', width: `${Math.min(pct, 100)}%`, background: color, borderRadius: 3, transition: 'width 0.5s ease' }} />
    </div>
  )
}

function ScoreBadge({ score }: { score: number }) {
  const color = score >= 80 ? apple.red : score >= 50 ? apple.orange : score >= 20 ? apple.yellow : apple.green
  const label = score >= 80 ? 'Critical' : score >= 50 ? 'High' : score >= 20 ? 'Medium' : 'Low'
  return <Badge color={color}>{label} waste</Badge>
}

function CopyBtn({ text }: { text: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <button onClick={() => { navigator.clipboard.writeText(text); setCopied(true); setTimeout(() => setCopied(false), 2000) }}
      style={{ padding: '3px 8px', fontSize: 11, borderRadius: apple.radius.sm, border: `0.5px solid ${apple.separator}`, background: apple.fill, color: apple.label, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 3, flexShrink: 0 }}>
      {copied ? <ClipboardCheck style={{ width: 10, height: 10, color: apple.green }} /> : <Copy style={{ width: 10, height: 10 }} />}
      {copied ? 'Copied' : 'Copy'}
    </button>
  )
}

// ─── Analysis helpers ──────────────────────────────────────────────────────────
function analyzeContainerCapacity(clusters: { name: string; topo: ClusterTopology }[]): ContainerCapacityItem[] {
  const items: ContainerCapacityItem[] = []
  for (const { name: cluster, topo } of clusters) {
    const depLookup = new Map<string, K8sDeployment>()
    for (const dep of topo.deployments) {
      depLookup.set(`${dep.namespace}/${dep.name}`, dep)
    }

    // Analyze from deployments (authoritative resource specs)
    for (const dep of topo.deployments) {
      for (const c of (dep.containers ?? [])) {
        const cpuReq = parseCpuToMillis(c.resources?.requests?.cpu)
        const cpuLim = parseCpuToMillis(c.resources?.limits?.cpu)
        const memReq = parseMemToMi(c.resources?.requests?.memory)
        const memLim = parseMemToMi(c.resources?.limits?.memory)

        if (cpuLim === 0 && memLim === 0) continue // no limits set — skip

        const cpuRatio = cpuLim > 0 && cpuReq > 0 ? cpuLim / cpuReq : cpuLim > 0 ? 99 : 0
        const memRatio = memLim > 0 && memReq > 0 ? memLim / memReq : memLim > 0 ? 99 : 0

        // Normalize waste: ratio of 1 = no waste, ratio of 10+ = very wasteful
        const cpuWaste = cpuRatio > 1 ? Math.min(((cpuRatio - 1) / 9) * 100, 100) : 0
        const memWaste = memRatio > 1 ? Math.min(((memRatio - 1) / 9) * 100, 100) : 0
        const overall = Math.round((cpuWaste + memWaste) / 2)

        if (overall < 5) continue // very tight, skip low-noise

        // Suggest right-sized limits: 2× request with min floor
        const sugCpuLimMillis = Math.max(cpuReq * 2, 50)
        const sugMemLimMi = Math.max(memReq * 2, 64)

        const patch = cpuLim > 0 && memLim > 0
          ? `kubectl patch deployment ${dep.name} -n ${dep.namespace} --type='json' -p='[{"op":"replace","path":"/spec/template/spec/containers/0/resources/limits","value":{"cpu":"${formatCpu(sugCpuLimMillis)}","memory":"${formatMem(sugMemLimMi)}"}}]'`
          : cpuLim > 0
            ? `kubectl patch deployment ${dep.name} -n ${dep.namespace} --type='json' -p='[{"op":"replace","path":"/spec/template/spec/containers/0/resources/limits/cpu","value":"${formatCpu(sugCpuLimMillis)}"}]'`
            : `kubectl patch deployment ${dep.name} -n ${dep.namespace} --type='json' -p='[{"op":"replace","path":"/spec/template/spec/containers/0/resources/limits/memory","value":"${formatMem(sugMemLimMi)}"}]'`

        items.push({
          cluster, namespace: dep.namespace, deployment: dep.name, pod: '', container: c.name,
          cpuRequest: cpuReq, cpuLimit: cpuLim, memRequestMi: memReq, memLimitMi: memLim,
          cpuWasteRatio: cpuRatio, memWasteRatio: memRatio,
          overallWasteScore: overall,
          suggestedCpuLimit: formatCpu(sugCpuLimMillis),
          suggestedMemLimit: formatMem(sugMemLimMi),
          patchCmd: patch,
        })
      }
    }
  }
  return items.sort((a, b) => b.overallWasteScore - a.overallWasteScore)
}

function analyzeNodeCapacity(clusters: { name: string; topo: ClusterTopology }[]): NodeCapacityItem[] {
  const items: NodeCapacityItem[] = []
  for (const { name: cluster, topo } of clusters) {
    for (const node of topo.nodes) {
      const cpuCores = parseCpuFromNode(node.capacity?.cpu)
      const memGi = parseMemGiFromNode(node.capacity?.memory)
      const podCapacity = parseInt(node.capacity?.pods ?? '110', 10)
      const podCount = topo.pods.filter(p => p.node_name === node.name).length
      const podUtilPct = podCapacity > 0 ? Math.round((podCount / podCapacity) * 100) : 0
      if (cpuCores === 0 && memGi === 0) continue
      items.push({ cluster, name: node.name, cpuCores, memGi, podCapacity, podCount, podUtilPct, roles: node.roles ?? [] })
    }
  }
  return items.sort((a, b) => b.podUtilPct - a.podUtilPct)
}

function analyzeCloudStack(regions: CloudStackRegion[]): CloudStackHostItem[] {
  const items: CloudStackHostItem[] = []
  const seen = new Set<string>()
  for (const region of regions) {
    for (const [, nodes] of Object.entries(region.layers)) {
      for (const node of nodes) {
        if (!['kvm_host', 'hypervisor', 'host'].some(t => node.type?.includes(t))) continue
        if (seen.has(node.name)) continue
        seen.add(node.name)
        const p = node.properties ?? {}
        const cpuCores = Number(p.cpu_cores ?? p.cpu_number ?? 0)
        const memTotalGi = Number(p.memory_total_mb ?? p.memory_total ?? 0) / 1024
        const memAllocGi = Number(p.memory_used_mb ?? p.memory_allocated_mb ?? p.memory_allocated ?? 0) / 1024
        const memUsedPct = memTotalGi > 0 ? Math.round((memAllocGi / memTotalGi) * 100) : 0
        const vmCount = node.dependents?.length ?? Number(p.vm_count ?? p.running_vms ?? 0)
        items.push({ region: region.region, name: node.name, type: node.type, cpuCores, memTotalGi, memAllocatedGi: memAllocGi, memUsedPct, vmCount })
      }
    }
  }
  return items.sort((a, b) => b.memUsedPct - a.memUsedPct)
}

// ─── Namespace Rollup ─────────────────────────────────────────────────────────
interface NamespaceRollup {
  cluster: string; namespace: string
  totalCpuRequestM: number; totalCpuLimitM: number
  totalMemRequestMi: number; totalMemLimitMi: number
  containerCount: number; wasteScore: number
}
function rollupByNamespace(items: ContainerCapacityItem[]): NamespaceRollup[] {
  const m = new Map<string, NamespaceRollup>()
  for (const item of items) {
    const key = `${item.cluster}/${item.namespace}`
    if (!m.has(key)) m.set(key, { cluster: item.cluster, namespace: item.namespace, totalCpuRequestM: 0, totalCpuLimitM: 0, totalMemRequestMi: 0, totalMemLimitMi: 0, containerCount: 0, wasteScore: 0 })
    const r = m.get(key)!
    r.totalCpuRequestM += item.cpuRequest
    r.totalCpuLimitM += item.cpuLimit
    r.totalMemRequestMi += item.memRequestMi
    r.totalMemLimitMi += item.memLimitMi
    r.containerCount++
    r.wasteScore = Math.max(r.wasteScore, item.overallWasteScore)
  }
  return Array.from(m.values()).sort((a, b) => b.wasteScore - a.wasteScore)
}

// ─── Main Page ────────────────────────────────────────────────────────────────
type PageTab = 'overview' | 'k8s' | 'cloudstack' | 'forecasts' | 'recommendations'

const CapacityPlanningPage: React.FC = () => {
  const [activeTab, setActiveTab] = useState<PageTab>('overview')
  const [clusters, setClusters] = useState<ClusterListItem[]>([])
  const [clusterTopologies, setClusterTopologies] = useState<Record<string, ClusterTopology>>({})
  const [busyClusters, setBusyClusters] = useState<Set<string>>(new Set())
  const [csRegions, setCsRegions] = useState<CloudStackRegion[]>([])
  const [csLoading, setCsLoading] = useState(false)
  const [lastRefreshed, setLastRefreshed] = useState<Date | null>(null)
  // KubeSense forecasts — predictive capacity data
  const [ksForecasts, setKsForecasts] = useState<any[]>([])
  const [ksForecastsLoading, setKsForecastsLoading] = useState(false)
  // Storage capacity events from KubeSense
  const [storageEvents, setStorageEvents] = useState<any[]>([])

  // K8s cluster list
  useEffect(() => {
    fetch('/api/v1/topology/k8s-clusters', { headers: authHeader() })
      .then(r => r.json())
      .then(json => {
        if (json.success) {
          const raw: any[] = json.data?.data?.clusters ?? json.data?.clusters ?? []
          setClusters(raw.map(c => ({ name: c.name ?? c.cluster_name, environment: c.environment, region: c.region })))
        }
      }).catch(() => {})
  }, [])

  // Fetch K8s topology per cluster
  useEffect(() => {
    if (clusters.length === 0) return
    clusters.forEach(async cluster => {
      if (clusterTopologies[cluster.name]) return
      setBusyClusters(prev => new Set([...prev, cluster.name]))
      try {
        const res = await fetch(`/api/v1/k8s-service/api/v1/clusters/${encodeURIComponent(cluster.name)}/topology`, { headers: authHeader() })
        const json = await res.json()
        if (json.success && json.data) {
          const d = json.data
          setClusterTopologies(prev => ({
            ...prev,
            [cluster.name]: {
              cluster: d.cluster ?? cluster.name,
              pods: d.pods ?? [],
              deployments: d.deployments ?? [],
              nodes: d.nodes ?? [],
              summary: d.summary ?? {},
            },
          }))
        }
      } catch (e) { console.error('[CapacityPlanning] K8s topology fetch failed:', cluster.name, e instanceof Error ? e.message : e) }
      setBusyClusters(prev => { const s = new Set(prev); s.delete(cluster.name); return s })
    })
    setLastRefreshed(new Date())
  }, [clusters])

  // Fetch CloudStack regions — try dynamic list first, fall back to defaults
  const fetchCloudStack = useCallback(async () => {
    setCsLoading(true)
    // Try to get dynamic region list from infra admin API
    let regionNames = ['rno', 'maiden', 'iad']
    try {
      const rr = await fetch('/api/v1/admin/infra/regions', { headers: authHeader() })
      if (rr.ok) {
        const rd = await rr.json()
        const dynamic = (rd?.data?.regions ?? rd?.regions ?? []).map((r: any) => r.name ?? r.region).filter(Boolean)
        if (dynamic.length > 0) regionNames = dynamic
      }
    } catch { /* fall back to defaults */ }
    const results: CloudStackRegion[] = []
    await Promise.all(regionNames.map(async region => {
      try {
        const res = await fetch(`/api/v1/topology/infrastructure/${region}`, { headers: authHeader() })
        const json = await res.json()
        if (json.success && json.data) results.push({ region, layers: json.data.layers ?? {}, stats: json.data.stats ?? {} })
      } catch (e) { console.error('[CapacityPlanning] CloudStack region fetch failed:', region, e instanceof Error ? e.message : e) }
    }))
    setCsRegions(results)
    setCsLoading(false)
  }, [])

  useEffect(() => { fetchCloudStack() }, [fetchCloudStack])

  // Fetch KubeSense predictive capacity forecasts
  useEffect(() => {
    setKsForecastsLoading(true)
    fetch('/api/v1/kubesense/db/forecasts', { headers: authHeader() })
      .then(r => r.json())
      .then(d => { setKsForecasts(d?.forecasts ?? []); setKsForecastsLoading(false) })
      .catch(() => setKsForecastsLoading(false))
    // Fetch storage capacity events for the Overview tab
    fetch('/api/v1/kubesense/db/health?event_type_prefix=storage.&limit=30', { headers: authHeader() })
      .then(r => r.json())
      .then(d => setStorageEvents(d?.events ?? []))
      .catch(() => {})
  }, [])

  const refresh = useCallback(async () => {
    setClusterTopologies({})
    setCsRegions([])
    setClusters([])
    setBusyClusters(new Set())
    await fetchCloudStack()
    const res = await fetch('/api/v1/topology/k8s-clusters', { headers: authHeader() })
    const json = await res.json()
    if (json.success) {
      const raw: any[] = json.data?.data?.clusters ?? json.data?.clusters ?? []
      setClusters(raw.map(c => ({ name: c.name ?? c.cluster_name, environment: c.environment, region: c.region })))
    }
    setLastRefreshed(new Date())
  }, [fetchCloudStack])

  const clusterList = useMemo(() =>
    Object.entries(clusterTopologies).map(([name, topo]) => ({ name, topo })),
    [clusterTopologies]
  )

  const containerItems = useMemo(() => analyzeContainerCapacity(clusterList), [clusterList])
  const nodeItems = useMemo(() => analyzeNodeCapacity(clusterList), [clusterList])
  const nsRollups = useMemo(() => rollupByNamespace(containerItems), [containerItems])
  const csHosts = useMemo(() => analyzeCloudStack(csRegions), [csRegions])

  const criticalCount = containerItems.filter(i => i.overallWasteScore >= 80).length
  const highCount = containerItems.filter(i => i.overallWasteScore >= 50 && i.overallWasteScore < 80).length
  const totalSavableCpuM = containerItems.reduce((s, i) => s + Math.max(0, i.cpuLimit - i.cpuRequest * 2), 0)
  const totalSavableMemMi = containerItems.reduce((s, i) => s + Math.max(0, i.memLimitMi - i.memRequestMi * 2), 0)

  const isLoading = busyClusters.size > 0 || csLoading

  const tabs: { key: PageTab; label: string; icon: React.ComponentType<any> }[] = [
    { key: 'overview', label: 'Overview', icon: BarChart2 },
    { key: 'k8s', label: 'K8s Workloads', icon: Box },
    { key: 'cloudstack', label: 'CloudStack', icon: Cloud },
    { key: 'forecasts', label: 'Forecasts', icon: TrendingUp },
    { key: 'recommendations', label: 'Recommendations', icon: Lightbulb },
  ]

  return (
    <div style={{ minHeight: '100vh', background: apple.background, fontFamily: '-apple-system, BlinkMacSystemFont, "SF Pro Text", sans-serif' }}>
      <style>{`@keyframes spin { from { transform: rotate(0deg) } to { transform: rotate(360deg) } } * { box-sizing: border-box }`}</style>

      {/* Header */}
      <div style={{ padding: '16px 28px', borderBottom: `0.5px solid ${apple.separator}`, background: apple.secondaryBackground, display: 'flex', alignItems: 'center', gap: 16 }}>
        <div style={{ flex: 1 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 3 }}>
            <div style={{ width: 32, height: 32, borderRadius: apple.radius.sm, background: apple.indigo + '18', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
              <Target style={{ width: 17, height: 17, color: apple.indigo }} />
            </div>
            <h1 style={{ fontSize: 17, fontWeight: 700, color: apple.label, margin: 0 }}>Intelligent Capacity Planning</h1>
            {isLoading && <Loader2 style={{ width: 14, height: 14, color: apple.blue, animation: 'spin 1s linear infinite' }} />}
          </div>
          {lastRefreshed && <div style={{ fontSize: 11, color: apple.tertiaryLabel, paddingLeft: 42 }}>Analysed {lastRefreshed.toLocaleTimeString()}</div>}
        </div>
        <button onClick={refresh} disabled={isLoading}
          style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '7px 14px', borderRadius: apple.radius.sm, border: `0.5px solid ${apple.separator}`, background: apple.fill, color: apple.label, fontSize: 13, fontWeight: 500, cursor: 'pointer', opacity: isLoading ? 0.6 : 1 }}>
          <RefreshCw style={{ width: 13, height: 13, animation: isLoading ? 'spin 1s linear infinite' : 'none' }} />
          Refresh
        </button>
      </div>

      {/* Tab bar */}
      <div style={{ padding: '0 28px', borderBottom: `0.5px solid ${apple.separator}`, background: apple.secondaryBackground, display: 'flex', gap: 0 }}>
        {tabs.map(t => (
          <button key={t.key} onClick={() => setActiveTab(t.key)}
            style={{ padding: '10px 16px', fontSize: 13, fontWeight: activeTab === t.key ? 600 : 400, border: 'none', borderBottom: activeTab === t.key ? `2px solid ${apple.indigo}` : '2px solid transparent', background: 'transparent', color: activeTab === t.key ? apple.indigo : apple.secondaryLabel, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 6, whiteSpace: 'nowrap', transition: 'all 0.12s', marginBottom: -1 }}>
            <t.icon style={{ width: 13, height: 13 }} />
            {t.label}
          </button>
        ))}
      </div>

      <div style={{ padding: '24px 28px', maxWidth: 1280, margin: '0 auto' }}>
        <AnimatePresence mode="wait">
          <motion.div key={activeTab} initial={{ opacity: 0, y: 6 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0 }} transition={{ duration: 0.15 }}>

            {activeTab === 'overview' && (
              <OverviewTab
                clusterList={clusterList} clusters={clusters}
                containerItems={containerItems} nodeItems={nodeItems}
                nsRollups={nsRollups} csHosts={csHosts}
                criticalCount={criticalCount} highCount={highCount}
                totalSavableCpuM={totalSavableCpuM} totalSavableMemMi={totalSavableMemMi}
                busyClusters={busyClusters} csLoading={csLoading}
                ksForecasts={ksForecasts} storageEvents={storageEvents}
                onTabChange={setActiveTab}
              />
            )}

            {activeTab === 'k8s' && (
              <K8sAnalysisTab
                containerItems={containerItems}
                nodeItems={nodeItems}
                nsRollups={nsRollups}
                busyClusters={busyClusters}
                clusterCount={clusters.length}
              />
            )}

            {activeTab === 'cloudstack' && (
              <CloudStackTab csHosts={csHosts} csRegions={csRegions} csLoading={csLoading} />
            )}

            {activeTab === 'forecasts' && (
              <ForecastsTab forecasts={ksForecasts} loading={ksForecastsLoading} />
            )}

            {activeTab === 'recommendations' && (
              <RecommendationsTab
                containerItems={containerItems}
                nodeItems={nodeItems}
                csHosts={csHosts}
                totalSavableCpuM={totalSavableCpuM}
                totalSavableMemMi={totalSavableMemMi}
              />
            )}

          </motion.div>
        </AnimatePresence>
      </div>
    </div>
  )
}

// ─── Overview Tab ──────────────────────────────────────────────────────────────
function OverviewTab({
  clusterList, clusters, containerItems, nodeItems, nsRollups, csHosts,
  criticalCount, highCount, totalSavableCpuM, totalSavableMemMi,
  busyClusters, csLoading, ksForecasts = [], storageEvents = [], onTabChange,
}: {
  clusterList: { name: string; topo: ClusterTopology }[]
  clusters: ClusterListItem[]
  containerItems: ContainerCapacityItem[]
  nodeItems: NodeCapacityItem[]
  nsRollups: NamespaceRollup[]
  csHosts: CloudStackHostItem[]
  criticalCount: number; highCount: number
  totalSavableCpuM: number; totalSavableMemMi: number
  busyClusters: Set<string>; csLoading: boolean
  ksForecasts?: any[]; storageEvents?: any[]
  onTabChange: (t: PageTab) => void
}) {
  const totalContainers = containerItems.length
  const avgWaste = totalContainers > 0 ? Math.round(containerItems.reduce((s, i) => s + i.overallWasteScore, 0) / totalContainers) : 0

  const summaryCards = [
    { label: 'Over-provisioned', value: criticalCount + highCount, sub: `${criticalCount} critical · ${highCount} high`, color: criticalCount > 0 ? apple.red : apple.orange, icon: AlertTriangle },
    { label: 'Reclaimable CPU', value: totalSavableCpuM > 0 ? formatCpu(totalSavableCpuM) : '0m', sub: 'If right-sized to 2× request', color: apple.indigo, icon: Cpu },
    { label: 'Reclaimable Memory', value: totalSavableMemMi > 0 ? formatMem(totalSavableMemMi) : '0Mi', sub: 'If right-sized to 2× request', color: apple.purple, icon: HardDrive },
    { label: 'Avg Waste Score', value: `${avgWaste}%`, sub: `across ${totalContainers} containers`, color: avgWaste >= 50 ? apple.orange : apple.green, icon: BarChart2 },
    { label: 'K8s Clusters', value: clusters.length, sub: `${Object.keys(clusterList.map(c => c.name)).length} analysed`, color: apple.blue, icon: Server },
    { label: 'CloudStack Hosts', value: csHosts.length, sub: `${csHosts.filter(h => h.memUsedPct > 85).length} high utilisation`, color: apple.teal, icon: Cloud },
  ]

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 24 }}>
      {/* Loading banners */}
      {(busyClusters.size > 0 || csLoading) && (
        <div style={{ background: apple.blue + '0d', border: `0.5px solid ${apple.blue}30`, borderRadius: apple.radius.md, padding: '10px 18px', display: 'flex', alignItems: 'center', gap: 10, fontSize: 13, color: apple.blue }}>
          <Loader2 style={{ width: 14, height: 14, animation: 'spin 1s linear infinite' }} />
          {busyClusters.size > 0 && <span>Loading {busyClusters.size} K8s cluster{busyClusters.size !== 1 ? 's' : ''}…</span>}
          {csLoading && <span>Loading CloudStack data…</span>}
          <span style={{ color: apple.tertiaryLabel }}>Analysis will update as data arrives.</span>
        </div>
      )}

      {/* Summary cards */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))', gap: 12 }}>
        {summaryCards.map(c => (
          <div key={c.label} style={{ background: apple.secondaryBackground, border: `0.5px solid ${apple.separator}`, borderRadius: apple.radius.lg, padding: '16px 18px' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 10 }}>
              <div style={{ width: 30, height: 30, borderRadius: apple.radius.sm, background: c.color + '18', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                <c.icon style={{ width: 15, height: 15, color: c.color }} />
              </div>
              <span style={{ fontSize: 12, color: apple.tertiaryLabel }}>{c.label}</span>
            </div>
            <div style={{ fontSize: 24, fontWeight: 700, color: apple.label, lineHeight: 1, marginBottom: 4 }}>{c.value}</div>
            <div style={{ fontSize: 11, color: apple.secondaryLabel }}>{c.sub}</div>
          </div>
        ))}
      </div>

      {/* Top namespace waste */}
      {nsRollups.length > 0 && (
        <Section icon={Layers} title="Namespace Capacity Overview" color={apple.orange}
          action={<button onClick={() => onTabChange('k8s')} style={linkBtnStyle}>View all K8s →</button>}>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))', gap: 10 }}>
            {nsRollups.slice(0, 9).map(ns => (
              <div key={`${ns.cluster}/${ns.namespace}`} style={{ padding: '12px 14px', background: apple.fill, borderRadius: apple.radius.md, border: `0.5px solid ${apple.separator}` }}>
                <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', marginBottom: 8 }}>
                  <div>
                    <div style={{ fontSize: 12, fontWeight: 600, color: apple.label, fontFamily: 'monospace' }}>{ns.namespace}</div>
                    <div style={{ fontSize: 10, color: apple.tertiaryLabel }}>{ns.cluster} · {ns.containerCount} containers</div>
                  </div>
                  <ScoreBadge score={ns.wasteScore} />
                </div>
                <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <span style={{ fontSize: 10, color: apple.tertiaryLabel, width: 28 }}>CPU</span>
                    <WasteBar pct={ns.totalCpuRequestM > 0 ? (ns.totalCpuLimitM / ns.totalCpuRequestM) * 20 : 0}
                      color={ns.totalCpuLimitM > ns.totalCpuRequestM * 3 ? apple.orange : apple.green} />
                    <span style={{ fontSize: 10, color: apple.tertiaryLabel, whiteSpace: 'nowrap' }}>
                      {formatCpu(ns.totalCpuRequestM)} req / {formatCpu(ns.totalCpuLimitM)} lim
                    </span>
                  </div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <span style={{ fontSize: 10, color: apple.tertiaryLabel, width: 28 }}>Mem</span>
                    <WasteBar pct={ns.totalMemRequestMi > 0 ? (ns.totalMemLimitMi / ns.totalMemRequestMi) * 20 : 0}
                      color={ns.totalMemLimitMi > ns.totalMemRequestMi * 3 ? apple.orange : apple.green} />
                    <span style={{ fontSize: 10, color: apple.tertiaryLabel, whiteSpace: 'nowrap' }}>
                      {formatMem(ns.totalMemRequestMi)} req / {formatMem(ns.totalMemLimitMi)} lim
                    </span>
                  </div>
                </div>
              </div>
            ))}
          </div>
        </Section>
      )}

      {/* Node capacity heatmap */}
      {nodeItems.length > 0 && (
        <Section icon={Server} title="Node Pod Capacity" color={apple.blue}>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(200px, 1fr))', gap: 8 }}>
            {nodeItems.map(n => {
              const pctColor = n.podUtilPct >= 85 ? apple.red : n.podUtilPct >= 65 ? apple.orange : apple.green
              return (
                <div key={`${n.cluster}/${n.name}`} style={{ padding: '10px 12px', borderRadius: apple.radius.md, background: apple.fill, border: `0.5px solid ${apple.separator}` }}>
                  <div style={{ fontSize: 11, fontWeight: 600, color: apple.label, fontFamily: 'monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', marginBottom: 4 }}>{n.name}</div>
                  <div style={{ fontSize: 10, color: apple.tertiaryLabel, marginBottom: 6 }}>{n.cluster} · {(n.roles ?? []).join(', ') || 'worker'}</div>
                  <div style={{ height: 4, borderRadius: 2, background: apple.secondaryBackground, overflow: 'hidden', marginBottom: 5 }}>
                    <div style={{ height: '100%', width: `${n.podUtilPct}%`, background: pctColor, borderRadius: 2 }} />
                  </div>
                  <div style={{ display: 'flex', justifyContent: 'space-between' }}>
                    <span style={{ fontSize: 11, color: pctColor, fontWeight: 600 }}>{n.podUtilPct}% pods</span>
                    <span style={{ fontSize: 10, color: apple.tertiaryLabel }}>{n.podCount}/{n.podCapacity}</span>
                  </div>
                  <div style={{ fontSize: 10, color: apple.tertiaryLabel, marginTop: 3 }}>
                    {n.cpuCores > 0 && `${n.cpuCores} CPU`}{n.cpuCores > 0 && n.memGi > 0 && ' · '}{n.memGi > 0 && `${n.memGi.toFixed(0)}Gi RAM`}
                  </div>
                </div>
              )
            })}
          </div>
        </Section>
      )}

      {/* CloudStack overview */}
      {csHosts.length > 0 && (
        <Section icon={Cloud} title="CloudStack Host Utilisation" color={apple.teal}
          action={<button onClick={() => onTabChange('cloudstack')} style={linkBtnStyle}>View all →</button>}>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(220px, 1fr))', gap: 8 }}>
            {csHosts.slice(0, 8).map(h => {
              const pctColor = h.memUsedPct >= 85 ? apple.red : h.memUsedPct >= 65 ? apple.orange : apple.green
              return (
                <div key={`${h.region}/${h.name}`} style={{ padding: '10px 12px', borderRadius: apple.radius.md, background: apple.fill, border: `0.5px solid ${apple.separator}` }}>
                  <div style={{ fontSize: 11, fontWeight: 600, color: apple.label, fontFamily: 'monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', marginBottom: 3 }}>{h.name}</div>
                  <div style={{ fontSize: 10, color: apple.tertiaryLabel, marginBottom: 6 }}>{h.region} · {h.vmCount} VMs</div>
                  <div style={{ height: 4, borderRadius: 2, background: apple.secondaryBackground, overflow: 'hidden', marginBottom: 4 }}>
                    <div style={{ height: '100%', width: `${Math.min(h.memUsedPct, 100)}%`, background: pctColor, borderRadius: 2 }} />
                  </div>
                  <div style={{ display: 'flex', justifyContent: 'space-between' }}>
                    <span style={{ fontSize: 11, color: pctColor, fontWeight: 600 }}>{h.memUsedPct}% mem</span>
                    <span style={{ fontSize: 10, color: apple.tertiaryLabel }}>{h.memAllocatedGi.toFixed(1)}/{h.memTotalGi.toFixed(1)} Gi</span>
                  </div>
                </div>
              )
            })}
          </div>
        </Section>
      )}

      {containerItems.length === 0 && nodeItems.length === 0 && csHosts.length === 0 && !busyClusters.size && !csLoading && (
        <div style={{ textAlign: 'center', padding: '48px 24px', color: apple.tertiaryLabel }}>
          <Target style={{ width: 44, height: 44, marginBottom: 12 }} />
          <div style={{ fontSize: 15, fontWeight: 600 }}>No capacity data available</div>
          <div style={{ fontSize: 13, marginTop: 4 }}>No K8s clusters or CloudStack regions are configured, or data is still loading.</div>
        </div>
      )}

      {/* KubeSense Capacity Intelligence preview */}
      {(ksForecasts.length > 0 || storageEvents.length > 0) && (
        <Section icon={TrendingUp} title="KubeSense Capacity Intelligence" color={apple.purple}
          action={<button style={linkBtnStyle} onClick={() => onTabChange('forecasts')}>See all forecasts →</button>}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            {/* Imminent breaches — sorted by predicted_breach */}
            {ksForecasts.slice(0, 5).map((f: any, i: number) => {
              const pct = Math.round((f.current_value ?? 0) * 100)
              const daysToBreachMs = f.predicted_breach ? (new Date(f.predicted_breach).getTime() - Date.now()) : null
              const daysTo = daysToBreachMs != null ? Math.ceil(daysToBreachMs / 86400000) : null
              const urgencyColor = daysTo != null && daysTo <= 3 ? apple.red : daysTo != null && daysTo <= 7 ? apple.orange : apple.yellow
              return (
                <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '8px 0', borderBottom: `0.5px solid ${apple.separator}` }}>
                  <div style={{ width: 6, height: 6, borderRadius: '50%', background: urgencyColor, flexShrink: 0 }} />
                  <div style={{ flex: 1 }}>
                    <span style={{ fontSize: 13, fontWeight: 500, color: apple.label }}>{f.namespace}/{f.resource_name}</span>
                    <span style={{ fontSize: 11, color: apple.tertiaryLabel, marginLeft: 8 }}>{f.target?.replace(/_/g, ' ')}</span>
                  </div>
                  <div style={{ fontSize: 12, color: apple.secondaryLabel }}>{pct}% used</div>
                  {daysTo != null && (
                    <div style={{ fontSize: 12, fontWeight: 600, color: urgencyColor }}>
                      {daysTo <= 0 ? 'BREACHED' : `${daysTo}d`}
                    </div>
                  )}
                </div>
              )
            })}
            {storageEvents.length > 0 && (
              <div style={{ marginTop: 4, padding: '8px 12px', background: `${apple.orange}08`, borderRadius: 8, fontSize: 12, color: apple.secondaryLabel }}>
                <span style={{ fontWeight: 600, color: apple.orange }}>⚠ Storage:</span> {storageEvents.length} storage event{storageEvents.length !== 1 ? 's' : ''} in the last 24h —{' '}
                {storageEvents.filter((e: any) => e.severity === 'critical').length} critical
              </div>
            )}
          </div>
        </Section>
      )}
    </div>
  )
}

const linkBtnStyle: React.CSSProperties = { padding: '4px 10px', fontSize: 12, borderRadius: 6, border: 'none', background: 'transparent', color: apple.blue, cursor: 'pointer', fontWeight: 500 }

function Section({ icon: Icon, title, color, children, action }: { icon: React.ComponentType<any>; title: string; color: string; children: React.ReactNode; action?: React.ReactNode }) {
  return (
    <div style={{ background: apple.secondaryBackground, border: `0.5px solid ${apple.separator}`, borderRadius: apple.radius.lg, overflow: 'hidden' }}>
      <div style={{ padding: '14px 18px', borderBottom: `0.5px solid ${apple.separator}`, display: 'flex', alignItems: 'center', gap: 8 }}>
        <Icon style={{ width: 16, height: 16, color }} />
        <span style={{ fontSize: 14, fontWeight: 600, color: apple.label, flex: 1 }}>{title}</span>
        {action}
      </div>
      <div style={{ padding: '14px 18px' }}>{children}</div>
    </div>
  )
}

// ─── Forecasts Tab (KubeSense predictive capacity) ───────────────────────────
function ForecastsTab({ forecasts, loading }: { forecasts: any[]; loading: boolean }) {
  if (loading) return (
    <div style={{ textAlign: 'center', padding: 40 }}>
      <Spinner /> <div style={{ marginTop: 12, fontSize: 14, color: apple.tertiaryLabel }}>Loading capacity forecasts…</div>
    </div>
  )
  if (forecasts.length === 0) return (
    <div style={{ textAlign: 'center', padding: '60px 24px', color: apple.tertiaryLabel }}>
      <TrendingUp style={{ width: 40, height: 40, marginBottom: 14 }} />
      <div style={{ fontSize: 15, fontWeight: 600, color: apple.secondaryLabel }}>No active forecasts</div>
      <div style={{ fontSize: 13, marginTop: 6 }}>
        KubeSense capacity forecasts appear when resources are approaching their thresholds.
        Forecasts are published every 5 minutes by the KubeSense API service.
      </div>
    </div>
  )

  const targetColor = (t: string) =>
    t?.includes('pvc') || t?.includes('storage') ? apple.red : t?.includes('cpu') ? apple.orange : t?.includes('memory') ? apple.yellow : apple.purple

  // Group by breach urgency
  const imminentBreaches = forecasts.filter(f => {
    if (!f.predicted_breach) return false
    const days = (new Date(f.predicted_breach).getTime() - Date.now()) / 86400000
    return days <= 7
  })
  const laterBreaches = forecasts.filter(f => {
    if (!f.predicted_breach) return true
    const days = (new Date(f.predicted_breach).getTime() - Date.now()) / 86400000
    return days > 7
  })

  const ForecastCard = ({ f }: { f: any }) => {
    const pct = Math.round((f.current_value ?? 0) * 100)
    const thr = Math.round((f.threshold ?? 0.85) * 100)
    const col = targetColor(f.target)
    const daysToBreachMs = f.predicted_breach ? (new Date(f.predicted_breach).getTime() - Date.now()) : null
    const daysTo = daysToBreachMs != null ? Math.ceil(daysToBreachMs / 86400000) : null
    const urgColor = daysTo != null && daysTo <= 3 ? apple.red : daysTo != null && daysTo <= 7 ? apple.orange : col
    return (
      <div style={{ padding: 16, background: apple.secondaryBackground, borderRadius: apple.radius.lg, border: `0.5px solid ${urgColor}30` }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 12 }}>
          <div style={{ width: 36, height: 36, borderRadius: apple.radius.md, background: `${col}15`, display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 }}>
            <TrendingUp style={{ width: 16, height: 16, color: col }} />
          </div>
          <div style={{ flex: 1 }}>
            <div style={{ fontWeight: 600, fontSize: 13, color: apple.label }}>{f.target?.replace(/_/g, ' ')?.toUpperCase()}</div>
            <div style={{ fontSize: 11, color: apple.tertiaryLabel }}>{f.namespace}/{f.resource_name}</div>
          </div>
          {daysTo != null && (
            <span style={{ fontSize: 12, fontWeight: 700, color: urgColor, padding: '3px 8px', borderRadius: 20, background: `${urgColor}15` }}>
              {daysTo <= 0 ? 'BREACHED' : `${daysTo}d to breach`}
            </span>
          )}
        </div>
        <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 12, color: apple.tertiaryLabel, marginBottom: 5 }}>
          <span>Current {pct}%</span><span>Threshold {thr}%</span>
        </div>
        <div style={{ height: 6, background: `${col}15`, borderRadius: 3, overflow: 'hidden', marginBottom: 8 }}>
          <div style={{ width: `${pct}%`, height: '100%', background: pct > 80 ? apple.red : pct > 60 ? apple.orange : apple.green, borderRadius: 3 }} />
        </div>
        <div style={{ display: 'flex', gap: 12, fontSize: 12, color: apple.secondaryLabel }}>
          {f.predicted_breach && <span><span style={{ fontWeight: 600, color: urgColor }}>Breach: </span>{new Date(f.predicted_breach).toLocaleDateString()}</span>}
          {f.trend_per_day > 0 && <span>+{(f.trend_per_day * 100).toFixed(1)}%/day</span>}
          <span>conf {Math.round((f.model_confidence ?? 0) * 100)}%</span>
        </div>
      </div>
    )
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 24 }}>
      {imminentBreaches.length > 0 && (
        <Section icon={AlertTriangle} title={`Imminent Breaches (≤7 days) — ${imminentBreaches.length} resource${imminentBreaches.length !== 1 ? 's' : ''}`} color={apple.red}>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))', gap: 12 }}>
            {imminentBreaches.map((f, i) => <ForecastCard key={i} f={f} />)}
          </div>
        </Section>
      )}
      {laterBreaches.length > 0 && (
        <Section icon={TrendingUp} title={`Long-Range Forecasts — ${laterBreaches.length} resource${laterBreaches.length !== 1 ? 's' : ''}`} color={apple.purple}>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))', gap: 12 }}>
            {laterBreaches.map((f, i) => <ForecastCard key={i} f={f} />)}
          </div>
        </Section>
      )}
    </div>
  )
}

// ─── K8s Analysis Tab ─────────────────────────────────────────────────────────
function K8sAnalysisTab({
  containerItems, nodeItems, nsRollups, busyClusters, clusterCount,
}: {
  containerItems: ContainerCapacityItem[]
  nodeItems: NodeCapacityItem[]
  nsRollups: NamespaceRollup[]
  busyClusters: Set<string>
  clusterCount: number
}) {
  const [view, setView] = useState<'containers' | 'nodes' | 'namespaces'>('containers')
  const [clusterFilter, setClusterFilter] = useState('')
  const [nsFilter, setNsFilter] = useState('')
  const [minScore, setMinScore] = useState(0)
  const [shown, setShown] = useState(30)

  const allClusters = Array.from(new Set(containerItems.map(i => i.cluster)))
  const allNs = Array.from(new Set(containerItems.map(i => i.namespace))).sort()

  const filtered = containerItems.filter(i =>
    (!clusterFilter || i.cluster === clusterFilter) &&
    (!nsFilter || i.namespace === nsFilter) &&
    i.overallWasteScore >= minScore
  )

  const filteredNodes = nodeItems.filter(n => !clusterFilter || n.cluster === clusterFilter)

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {busyClusters.size > 0 && (
        <div style={{ background: apple.blue + '0d', border: `0.5px solid ${apple.blue}25`, borderRadius: apple.radius.sm, padding: '8px 14px', fontSize: 12, color: apple.blue, display: 'flex', alignItems: 'center', gap: 8 }}>
          <Loader2 style={{ width: 12, height: 12, animation: 'spin 1s linear infinite' }} />
          Loading {busyClusters.size} of {clusterCount} clusters…
        </div>
      )}

      {/* View selector */}
      <div style={{ display: 'flex', gap: 4 }}>
        {(['containers', 'nodes', 'namespaces'] as const).map(v => (
          <button key={v} onClick={() => setView(v)} style={{ padding: '6px 14px', fontSize: 12, borderRadius: apple.radius.sm, border: `0.5px solid ${view === v ? apple.indigo : apple.separator}`, background: view === v ? apple.indigo : apple.fill, color: view === v ? '#fff' : apple.label, cursor: 'pointer', fontWeight: 500 }}>
            {v.charAt(0).toUpperCase() + v.slice(1)}
          </button>
        ))}
      </div>

      {/* Filters */}
      {view === 'containers' && (
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center' }}>
          <select value={clusterFilter} onChange={e => setClusterFilter(e.target.value)}
            style={{ padding: '5px 10px', borderRadius: apple.radius.sm, border: `0.5px solid ${apple.separator}`, background: apple.fill, color: apple.label, fontSize: 12, outline: 'none' }}>
            <option value="">All clusters</option>
            {allClusters.map(c => <option key={c} value={c}>{c}</option>)}
          </select>
          <select value={nsFilter} onChange={e => setNsFilter(e.target.value)}
            style={{ padding: '5px 10px', borderRadius: apple.radius.sm, border: `0.5px solid ${apple.separator}`, background: apple.fill, color: apple.label, fontSize: 12, outline: 'none' }}>
            <option value="">All namespaces</option>
            {allNs.map(n => <option key={n} value={n}>{n}</option>)}
          </select>
          <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
            <Filter style={{ width: 12, height: 12, color: apple.gray }} />
            <span style={{ fontSize: 12, color: apple.secondaryLabel }}>Min waste:</span>
            {[0, 20, 50, 80].map(s => (
              <button key={s} onClick={() => setMinScore(s)} style={{ padding: '3px 9px', fontSize: 11, borderRadius: 20, border: `0.5px solid ${minScore === s ? apple.indigo : apple.separator}`, background: minScore === s ? apple.indigo : apple.fill, color: minScore === s ? '#fff' : apple.secondaryLabel, cursor: 'pointer' }}>
                {s}%
              </button>
            ))}
          </div>
          <span style={{ fontSize: 12, color: apple.tertiaryLabel }}>{filtered.length} containers</span>
        </div>
      )}

      {/* Container table */}
      {view === 'containers' && (
        <div style={{ background: apple.secondaryBackground, border: `0.5px solid ${apple.separator}`, borderRadius: apple.radius.lg, overflow: 'hidden' }}>
          {filtered.length === 0 ? (
            <div style={{ textAlign: 'center', padding: '36px', color: apple.tertiaryLabel, fontSize: 13 }}>
              <CheckCircle style={{ width: 32, height: 32, color: apple.green, marginBottom: 8 }} />
              <div>No over-provisioned containers found</div>
            </div>
          ) : (
            <>
              <div style={{ display: 'grid', gridTemplateColumns: '1.5fr 1.2fr 1fr 90px 90px 90px 90px 100px', padding: '8px 16px', borderBottom: `0.5px solid ${apple.separator}`, fontSize: 11, fontWeight: 600, color: apple.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.04em' }}>
                <div>Deployment / Container</div><div>Namespace</div><div>Cluster</div><div>CPU Req</div><div>CPU Lim</div><div>Mem Req</div><div>Mem Lim</div><div>Waste</div>
              </div>
              {filtered.slice(0, shown).map((item, i) => (
                <ContainerRow key={`${item.cluster}/${item.namespace}/${item.deployment}/${item.container}`} item={item} idx={i} total={Math.min(shown, filtered.length)} />
              ))}
              {filtered.length > shown && (
                <div style={{ padding: '10px 16px', borderTop: `0.5px solid ${apple.separator}`, display: 'flex', gap: 8, justifyContent: 'center' }}>
                  <button onClick={() => setShown(n => n + 30)} style={{ padding: '5px 14px', fontSize: 12, borderRadius: apple.radius.sm, border: `0.5px solid ${apple.indigo}40`, background: apple.indigo + '12', color: apple.indigo, cursor: 'pointer' }}>
                    <ChevronDown style={{ width: 11, height: 11, display: 'inline', marginRight: 4 }} />Show more
                  </button>
                </div>
              )}
            </>
          )}
        </div>
      )}

      {/* Node table */}
      {view === 'nodes' && (
        <div style={{ background: apple.secondaryBackground, border: `0.5px solid ${apple.separator}`, borderRadius: apple.radius.lg, overflow: 'hidden' }}>
          <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr 100px 80px 80px 140px', padding: '8px 16px', borderBottom: `0.5px solid ${apple.separator}`, fontSize: 11, fontWeight: 600, color: apple.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.04em' }}>
            <div>Node</div><div>Cluster</div><div>CPU Cores</div><div>Memory</div><div>Pods</div><div>Pod Utilisation</div>
          </div>
          {filteredNodes.map((n, i) => {
            const pctColor = n.podUtilPct >= 85 ? apple.red : n.podUtilPct >= 65 ? apple.orange : apple.green
            return (
              <div key={`${n.cluster}/${n.name}`} style={{ display: 'grid', gridTemplateColumns: '2fr 1fr 100px 80px 80px 140px', padding: '10px 16px', borderBottom: i < filteredNodes.length - 1 ? `0.5px solid ${apple.separator}` : 'none', fontSize: 12, alignItems: 'center' }}>
                <div>
                  <div style={{ fontFamily: 'monospace', color: apple.label, fontSize: 11, fontWeight: 500 }}>{n.name}</div>
                  <div style={{ fontSize: 10, color: apple.tertiaryLabel }}>{(n.roles ?? []).join(', ') || 'worker'}</div>
                </div>
                <div style={{ color: apple.secondaryLabel }}>{n.cluster}</div>
                <div style={{ color: apple.label, fontWeight: 500 }}>{n.cpuCores > 0 ? n.cpuCores : '—'}</div>
                <div style={{ color: apple.label }}>{n.memGi > 0 ? `${n.memGi.toFixed(0)} Gi` : '—'}</div>
                <div style={{ color: apple.label }}>{n.podCount}/{n.podCapacity}</div>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <WasteBar pct={n.podUtilPct} color={pctColor} />
                  <span style={{ fontSize: 11, color: pctColor, fontWeight: 600, minWidth: 36 }}>{n.podUtilPct}%</span>
                </div>
              </div>
            )
          })}
          {filteredNodes.length === 0 && <div style={{ padding: '32px', textAlign: 'center', color: apple.tertiaryLabel, fontSize: 13 }}>No node capacity data available</div>}
        </div>
      )}

      {/* Namespace rollup */}
      {view === 'namespaces' && (
        <div style={{ background: apple.secondaryBackground, border: `0.5px solid ${apple.separator}`, borderRadius: apple.radius.lg, overflow: 'hidden' }}>
          <div style={{ display: 'grid', gridTemplateColumns: '1.5fr 1fr 70px 110px 110px 110px', padding: '8px 16px', borderBottom: `0.5px solid ${apple.separator}`, fontSize: 11, fontWeight: 600, color: apple.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.04em' }}>
            <div>Namespace</div><div>Cluster</div><div>Ctrs</div><div>CPU Req/Lim</div><div>Mem Req/Lim</div><div>Waste</div>
          </div>
          {nsRollups.map((ns, i) => (
            <div key={`${ns.cluster}/${ns.namespace}`} style={{ display: 'grid', gridTemplateColumns: '1.5fr 1fr 70px 110px 110px 110px', padding: '10px 16px', borderBottom: i < nsRollups.length - 1 ? `0.5px solid ${apple.separator}` : 'none', fontSize: 12, alignItems: 'center' }}>
              <div style={{ fontFamily: 'monospace', color: apple.label, fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{ns.namespace}</div>
              <div style={{ color: apple.secondaryLabel }}>{ns.cluster}</div>
              <div style={{ color: apple.tertiaryLabel }}>{ns.containerCount}</div>
              <div style={{ color: ns.totalCpuLimitM > ns.totalCpuRequestM * 3 ? apple.orange : apple.label, fontFamily: 'monospace', fontSize: 11 }}>
                {formatCpu(ns.totalCpuRequestM)} / {formatCpu(ns.totalCpuLimitM)}
              </div>
              <div style={{ color: ns.totalMemLimitMi > ns.totalMemRequestMi * 3 ? apple.orange : apple.label, fontFamily: 'monospace', fontSize: 11 }}>
                {formatMem(ns.totalMemRequestMi)} / {formatMem(ns.totalMemLimitMi)}
              </div>
              <div><ScoreBadge score={ns.wasteScore} /></div>
            </div>
          ))}
          {nsRollups.length === 0 && <div style={{ padding: '32px', textAlign: 'center', color: apple.tertiaryLabel, fontSize: 13 }}>No namespace data available</div>}
        </div>
      )}
    </div>
  )
}

function ContainerRow({ item, idx, total }: { item: ContainerCapacityItem; idx: number; total: number }) {
  const [expanded, setExpanded] = useState(false)
  const cpuColor = item.cpuWasteRatio > 5 ? apple.red : item.cpuWasteRatio > 2 ? apple.orange : apple.green
  const memColor = item.memWasteRatio > 5 ? apple.red : item.memWasteRatio > 2 ? apple.orange : apple.green

  return (
    <div style={{ borderBottom: idx < total - 1 ? `0.5px solid ${apple.separator}` : 'none' }}>
      <div onClick={() => setExpanded(!expanded)}
        style={{ display: 'grid', gridTemplateColumns: '1.5fr 1.2fr 1fr 90px 90px 90px 90px 100px', padding: '10px 16px', fontSize: 12, alignItems: 'center', cursor: 'pointer', background: expanded ? apple.fill : 'transparent' }}
        onMouseEnter={e => { if (!expanded) e.currentTarget.style.background = apple.fill }}
        onMouseLeave={e => { if (!expanded) e.currentTarget.style.background = 'transparent' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 5 }}>
          {expanded ? <ChevronUp style={{ width: 11, height: 11, color: apple.gray }} /> : <ChevronRight style={{ width: 11, height: 11, color: apple.gray }} />}
          <div>
            <div style={{ fontFamily: 'monospace', color: apple.label, fontWeight: 500, fontSize: 11 }}>{item.deployment}</div>
            <div style={{ fontSize: 10, color: apple.tertiaryLabel }}>{item.container}</div>
          </div>
        </div>
        <div style={{ color: apple.secondaryLabel, fontFamily: 'monospace', fontSize: 11 }}>{item.namespace}</div>
        <div style={{ color: apple.tertiaryLabel, fontSize: 11 }}>{item.cluster}</div>
        <div style={{ color: item.cpuRequest > 0 ? apple.label : apple.tertiaryLabel, fontFamily: 'monospace', fontSize: 11 }}>{item.cpuRequest > 0 ? formatCpu(item.cpuRequest) : '—'}</div>
        <div style={{ color: cpuColor, fontFamily: 'monospace', fontSize: 11, fontWeight: item.cpuWasteRatio > 2 ? 600 : 400 }}>{item.cpuLimit > 0 ? formatCpu(item.cpuLimit) : '—'}</div>
        <div style={{ color: item.memRequestMi > 0 ? apple.label : apple.tertiaryLabel, fontFamily: 'monospace', fontSize: 11 }}>{item.memRequestMi > 0 ? formatMem(item.memRequestMi) : '—'}</div>
        <div style={{ color: memColor, fontFamily: 'monospace', fontSize: 11, fontWeight: item.memWasteRatio > 2 ? 600 : 400 }}>{item.memLimitMi > 0 ? formatMem(item.memLimitMi) : '—'}</div>
        <div><ScoreBadge score={item.overallWasteScore} /></div>
      </div>
      {expanded && (
        <div style={{ background: apple.tertiaryFill, padding: '12px 32px 16px', borderTop: `0.5px solid ${apple.separator}` }}>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16, marginBottom: 12 }}>
            <div>
              <div style={{ fontSize: 11, color: apple.tertiaryLabel, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.04em', marginBottom: 8 }}>CPU Analysis</div>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
                <AnalysisRow label="Request" value={formatCpu(item.cpuRequest)} color={apple.green} />
                <AnalysisRow label="Current Limit" value={formatCpu(item.cpuLimit)} color={item.cpuWasteRatio > 2 ? apple.orange : apple.green} />
                <AnalysisRow label="Limit/Request ratio" value={`${item.cpuWasteRatio.toFixed(1)}×`} color={item.cpuWasteRatio > 3 ? apple.red : item.cpuWasteRatio > 2 ? apple.orange : apple.green} />
                <AnalysisRow label="Suggested limit" value={item.suggestedCpuLimit} color={apple.blue} />
              </div>
            </div>
            <div>
              <div style={{ fontSize: 11, color: apple.tertiaryLabel, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.04em', marginBottom: 8 }}>Memory Analysis</div>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
                <AnalysisRow label="Request" value={formatMem(item.memRequestMi)} color={apple.green} />
                <AnalysisRow label="Current Limit" value={formatMem(item.memLimitMi)} color={item.memWasteRatio > 2 ? apple.orange : apple.green} />
                <AnalysisRow label="Limit/Request ratio" value={`${item.memWasteRatio.toFixed(1)}×`} color={item.memWasteRatio > 3 ? apple.red : item.memWasteRatio > 2 ? apple.orange : apple.green} />
                <AnalysisRow label="Suggested limit" value={item.suggestedMemLimit} color={apple.blue} />
              </div>
            </div>
          </div>
          <div style={{ fontSize: 11, color: apple.tertiaryLabel, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.04em', marginBottom: 6 }}>kubectl Patch Command</div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, background: apple.fill, borderRadius: apple.radius.sm, padding: '8px 12px' }}>
            <code style={{ flex: 1, fontSize: 10, color: '#c9d1d9', fontFamily: '"SF Mono", monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{item.patchCmd}</code>
            <CopyBtn text={item.patchCmd} />
          </div>
        </div>
      )}
    </div>
  )
}

function AnalysisRow({ label, value, color }: { label: string; value: string; color: string }) {
  return (
    <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 12, padding: '3px 0' }}>
      <span style={{ color: apple.secondaryLabel }}>{label}</span>
      <span style={{ color, fontWeight: 600, fontFamily: 'monospace' }}>{value}</span>
    </div>
  )
}

// ─── CloudStack Tab ───────────────────────────────────────────────────────────
function CloudStackTab({ csHosts, csRegions, csLoading }: { csHosts: CloudStackHostItem[]; csRegions: CloudStackRegion[]; csLoading: boolean }) {
  const [regionFilter, setRegionFilter] = useState('')

  const regions = Array.from(new Set(csHosts.map(h => h.region)))
  const filtered = csHosts.filter(h => !regionFilter || h.region === regionFilter)

  if (csLoading) return <Spinner />

  if (csHosts.length === 0) {
    return (
      <div style={{ textAlign: 'center', padding: '48px', color: apple.tertiaryLabel }}>
        <Cloud style={{ width: 44, height: 44, marginBottom: 12 }} />
        <div style={{ fontSize: 14, fontWeight: 600 }}>No CloudStack data available</div>
        <div style={{ fontSize: 12, marginTop: 4 }}>CloudStack infrastructure data could not be loaded. Check region configuration.</div>
      </div>
    )
  }

  const totalHosts = filtered.length
  const highMemHosts = filtered.filter(h => h.memUsedPct >= 85).length
  const lowMemHosts = filtered.filter(h => h.memUsedPct < 30).length
  const totalVMs = filtered.reduce((s, h) => s + h.vmCount, 0)

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {/* Summary */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(160px, 1fr))', gap: 12 }}>
        {[
          { label: 'Total Hosts', value: totalHosts, color: apple.teal, icon: Server },
          { label: 'High Memory (>85%)', value: highMemHosts, color: apple.red, icon: AlertTriangle },
          { label: 'Under-utilised (<30%)', value: lowMemHosts, color: apple.orange, icon: TrendingDown },
          { label: 'Total VMs', value: totalVMs, color: apple.blue, icon: Activity },
        ].map(c => (
          <div key={c.label} style={{ background: apple.secondaryBackground, border: `0.5px solid ${apple.separator}`, borderRadius: apple.radius.lg, padding: '14px 16px' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 7, marginBottom: 8 }}>
              <c.icon style={{ width: 14, height: 14, color: c.color }} />
              <span style={{ fontSize: 11, color: apple.tertiaryLabel }}>{c.label}</span>
            </div>
            <div style={{ fontSize: 24, fontWeight: 700, color: apple.label }}>{c.value}</div>
          </div>
        ))}
      </div>

      {/* Region filter */}
      <div style={{ display: 'flex', gap: 6 }}>
        {['', ...regions].map(r => (
          <button key={r} onClick={() => setRegionFilter(r)} style={{ padding: '5px 12px', fontSize: 12, borderRadius: apple.radius.sm, border: `0.5px solid ${regionFilter === r ? apple.teal : apple.separator}`, background: regionFilter === r ? apple.teal + '18' : apple.fill, color: regionFilter === r ? apple.teal : apple.label, cursor: 'pointer', fontWeight: 500 }}>
            {r || 'All regions'}
          </button>
        ))}
      </div>

      {/* Host table */}
      <div style={{ background: apple.secondaryBackground, border: `0.5px solid ${apple.separator}`, borderRadius: apple.radius.lg, overflow: 'hidden' }}>
        <div style={{ display: 'grid', gridTemplateColumns: '2fr 80px 100px 80px 1fr 60px', padding: '8px 16px', borderBottom: `0.5px solid ${apple.separator}`, fontSize: 11, fontWeight: 600, color: apple.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.04em' }}>
          <div>Host</div><div>Region</div><div>CPU Cores</div><div>VMs</div><div>Memory Utilisation</div><div>Usage %</div>
        </div>
        {filtered.map((h, i) => {
          const pctColor = h.memUsedPct >= 85 ? apple.red : h.memUsedPct >= 65 ? apple.orange : h.memUsedPct < 30 ? apple.yellow : apple.green
          return (
            <div key={`${h.region}/${h.name}`} style={{ display: 'grid', gridTemplateColumns: '2fr 80px 100px 80px 1fr 60px', padding: '10px 16px', borderBottom: i < filtered.length - 1 ? `0.5px solid ${apple.separator}` : 'none', fontSize: 12, alignItems: 'center' }}>
              <div>
                <div style={{ fontFamily: 'monospace', color: apple.label, fontWeight: 500, fontSize: 11, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{h.name}</div>
                <div style={{ fontSize: 10, color: apple.tertiaryLabel }}>{h.type}</div>
              </div>
              <div><Badge color={apple.teal}>{h.region}</Badge></div>
              <div style={{ color: h.cpuCores > 0 ? apple.label : apple.tertiaryLabel }}>{h.cpuCores > 0 ? h.cpuCores : '—'}</div>
              <div style={{ color: apple.label }}>{h.vmCount}</div>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <WasteBar pct={h.memUsedPct} color={pctColor} />
                <span style={{ fontSize: 10, color: apple.tertiaryLabel, whiteSpace: 'nowrap' }}>{h.memAllocatedGi.toFixed(1)}/{h.memTotalGi.toFixed(1)} Gi</span>
              </div>
              <div style={{ fontSize: 12, fontWeight: 600, color: pctColor }}>{h.memUsedPct}%</div>
            </div>
          )
        })}
      </div>

      {/* Consolidation opportunities */}
      {lowMemHosts > 0 && (
        <div style={{ background: apple.orange + '0d', border: `0.5px solid ${apple.orange}30`, borderRadius: apple.radius.lg, padding: '14px 18px' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
            <TrendingDown style={{ width: 15, height: 15, color: apple.orange }} />
            <span style={{ fontSize: 13, fontWeight: 600, color: apple.orange }}>Consolidation Opportunity: {lowMemHosts} under-utilised host{lowMemHosts !== 1 ? 's' : ''} (&lt;30% memory)</span>
          </div>
          <p style={{ margin: 0, fontSize: 12, color: apple.secondaryLabel, lineHeight: 1.5 }}>
            {lowMemHosts} host{lowMemHosts !== 1 ? 's have' : ' has'} memory utilisation below 30%. Consider live-migrating VMs to consolidate and reduce active host count. Estimated saving: {lowMemHosts} × host power + IPMI management overhead.
          </p>
        </div>
      )}
    </div>
  )
}

// ─── Recommendations Tab ──────────────────────────────────────────────────────
function RecommendationsTab({
  containerItems, nodeItems, csHosts, totalSavableCpuM, totalSavableMemMi,
}: {
  containerItems: ContainerCapacityItem[]
  nodeItems: NodeCapacityItem[]
  csHosts: CloudStackHostItem[]
  totalSavableCpuM: number
  totalSavableMemMi: number
}) {
  const criticalContainers = containerItems.filter(i => i.overallWasteScore >= 80)
  const highContainers = containerItems.filter(i => i.overallWasteScore >= 50 && i.overallWasteScore < 80)
  const overloadedNodes = nodeItems.filter(n => n.podUtilPct >= 85)
  const underutilNodes = nodeItems.filter(n => n.podUtilPct < 20 && n.podCount > 0)
  const highMemHosts = csHosts.filter(h => h.memUsedPct >= 85)
  const lowMemHosts = csHosts.filter(h => h.memUsedPct < 30)

  interface Rec {
    priority: 'critical' | 'high' | 'medium' | 'low'
    category: string; title: string; detail: string; count: number
    commands?: string[]
  }

  const recs: Rec[] = []

  if (criticalContainers.length > 0) {
    recs.push({
      priority: 'critical', category: 'K8s Resources', count: criticalContainers.length,
      title: `Right-size ${criticalContainers.length} critically over-provisioned container${criticalContainers.length !== 1 ? 's' : ''}`,
      detail: `These containers have limit/request ratios above 9×. Reducing limits to 2× request reclaims significant CPU and memory without affecting normal operation. Savings: ~${formatCpu(totalSavableCpuM)} CPU, ~${formatMem(totalSavableMemMi)} memory.`,
      commands: criticalContainers.slice(0, 3).map(i => i.patchCmd),
    })
  }

  if (highContainers.length > 0) {
    recs.push({
      priority: 'high', category: 'K8s Resources', count: highContainers.length,
      title: `Review ${highContainers.length} high-waste container${highContainers.length !== 1 ? 's' : ''}`,
      detail: `Limit/request ratios between 4–9×. These are candidates for right-sizing once workload patterns are confirmed.`,
      commands: highContainers.slice(0, 2).map(i => i.patchCmd),
    })
  }

  if (overloadedNodes.length > 0) {
    recs.push({
      priority: 'high', category: 'K8s Nodes', count: overloadedNodes.length,
      title: `${overloadedNodes.length} node${overloadedNodes.length !== 1 ? 's' : ''} at pod capacity limit (>85%)`,
      detail: `High pod density increases scheduling pressure. Consider adding nodes or using pod disruption budgets to balance load.`,
      commands: overloadedNodes.map(n => `kubectl describe node ${n.name} | grep -A5 'Allocated resources'`),
    })
  }

  if (underutilNodes.length > 0) {
    recs.push({
      priority: 'medium', category: 'K8s Nodes', count: underutilNodes.length,
      title: `${underutilNodes.length} node${underutilNodes.length !== 1 ? 's' : ''} under-utilised (<20% pod capacity)`,
      detail: `These nodes run very few pods. Consider using cluster autoscaler or draining these nodes to reduce infrastructure cost.`,
      commands: underutilNodes.map(n => `kubectl drain ${n.name} --ignore-daemonsets --delete-emptydir-data`),
    })
  }

  if (highMemHosts.length > 0) {
    recs.push({
      priority: 'critical', category: 'CloudStack', count: highMemHosts.length,
      title: `${highMemHosts.length} CloudStack host${highMemHosts.length !== 1 ? 's' : ''} at memory pressure (>85%)`,
      detail: `These KVM hosts are critically allocated. Avoid scheduling new VMs here. Consider live-migrating some VMs to hosts with lower utilisation.`,
    })
  }

  if (lowMemHosts.length > 0) {
    recs.push({
      priority: 'low', category: 'CloudStack', count: lowMemHosts.length,
      title: `${lowMemHosts.length} CloudStack host${lowMemHosts.length !== 1 ? 's' : ''} under-utilised (<30% memory)`,
      detail: `These hosts run with low memory allocation. VMs from these hosts can be migrated to consolidate the fleet and power off unused hardware.`,
    })
  }

  if (recs.length === 0) {
    return (
      <div style={{ textAlign: 'center', padding: '60px 24px' }}>
        <CheckCircle style={{ width: 48, height: 48, color: apple.green, marginBottom: 12 }} />
        <div style={{ fontSize: 16, fontWeight: 600, color: apple.label, marginBottom: 6 }}>All systems well-provisioned</div>
        <div style={{ fontSize: 13, color: apple.secondaryLabel }}>No significant capacity issues detected. Check back after new deployments.</div>
      </div>
    )
  }

  const priorityColor: Record<string, string> = { critical: apple.red, high: apple.orange, medium: apple.yellow, low: apple.blue }
  const sorted = recs.sort((a, b) => {
    const order = { critical: 0, high: 1, medium: 2, low: 3 }
    return order[a.priority] - order[b.priority]
  })

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      {/* Forecast header */}
      <div style={{ background: apple.indigo + '0d', border: `0.5px solid ${apple.indigo}30`, borderRadius: apple.radius.lg, padding: '16px 20px' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 10 }}>
          <TrendingUp style={{ width: 16, height: 16, color: apple.indigo }} />
          <span style={{ fontSize: 14, fontWeight: 600, color: apple.indigo }}>Capacity Forecast Summary</span>
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(200px, 1fr))', gap: 12 }}>
          {totalSavableCpuM > 0 && (
            <div>
              <div style={{ fontSize: 12, color: apple.tertiaryLabel, marginBottom: 3 }}>Reclaimable CPU (right-sizing)</div>
              <div style={{ fontSize: 20, fontWeight: 700, color: apple.indigo }}>{formatCpu(totalSavableCpuM)}</div>
              <div style={{ fontSize: 11, color: apple.secondaryLabel }}>across {containerItems.length} analysed containers</div>
            </div>
          )}
          {totalSavableMemMi > 0 && (
            <div>
              <div style={{ fontSize: 12, color: apple.tertiaryLabel, marginBottom: 3 }}>Reclaimable Memory</div>
              <div style={{ fontSize: 20, fontWeight: 700, color: apple.purple }}>{formatMem(totalSavableMemMi)}</div>
              <div style={{ fontSize: 11, color: apple.secondaryLabel }}>from over-provisioned limits</div>
            </div>
          )}
          <div>
            <div style={{ fontSize: 12, color: apple.tertiaryLabel, marginBottom: 3 }}>Total Issues Found</div>
            <div style={{ fontSize: 20, fontWeight: 700, color: apple.orange }}>{recs.length}</div>
            <div style={{ fontSize: 11, color: apple.secondaryLabel }}>{recs.filter(r => r.priority === 'critical').length} critical · {recs.filter(r => r.priority === 'high').length} high</div>
          </div>
        </div>
      </div>

      {sorted.map((rec, i) => (
        <RecommendationCard key={i} rec={rec} priorityColor={priorityColor} />
      ))}
    </div>
  )
}

function RecommendationCard({ rec, priorityColor }: { rec: any; priorityColor: Record<string, string> }) {
  const [expanded, setExpanded] = useState(rec.priority === 'critical')
  const color = priorityColor[rec.priority] ?? apple.gray

  return (
    <div style={{ background: apple.secondaryBackground, border: `0.5px solid ${color}30`, borderRadius: apple.radius.lg, overflow: 'hidden' }}>
      <div onClick={() => setExpanded(!expanded)} style={{ padding: '14px 18px', cursor: 'pointer', display: 'flex', alignItems: 'flex-start', gap: 12 }}>
        <div style={{ width: 36, height: 36, borderRadius: apple.radius.sm, background: color + '18', display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0, marginTop: 2 }}>
          {rec.priority === 'critical' ? <AlertTriangle style={{ width: 18, height: 18, color }} /> :
            rec.priority === 'high' ? <Zap style={{ width: 18, height: 18, color }} /> :
              <Lightbulb style={{ width: 18, height: 18, color }} />}
        </div>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 5 }}>
            <Badge color={color}>{rec.priority.toUpperCase()}</Badge>
            <Badge color={apple.gray}>{rec.category}</Badge>
            <Badge color={apple.gray}>{rec.count} item{rec.count !== 1 ? 's' : ''}</Badge>
          </div>
          <div style={{ fontSize: 14, fontWeight: 600, color: apple.label }}>{rec.title}</div>
        </div>
        {expanded ? <ChevronUp style={{ width: 14, height: 14, color: apple.gray, flexShrink: 0, marginTop: 4 }} /> : <ChevronDown style={{ width: 14, height: 14, color: apple.gray, flexShrink: 0, marginTop: 4 }} />}
      </div>
      {expanded && (
        <div style={{ padding: '0 18px 16px 18px', paddingLeft: 66 }}>
          <p style={{ margin: '0 0 12px', fontSize: 13, color: apple.secondaryLabel, lineHeight: 1.6 }}>{rec.detail}</p>
          {rec.commands && rec.commands.length > 0 && (
            <>
              <div style={{ fontSize: 11, fontWeight: 600, color: apple.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.04em', marginBottom: 6 }}>
                Example commands ({rec.commands.length} shown)
              </div>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                {rec.commands.map((cmd: string, i: number) => (
                  <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 8, background: apple.fill, borderRadius: apple.radius.sm, padding: '7px 10px' }}>
                    <code style={{ flex: 1, fontSize: 10, color: '#c9d1d9', fontFamily: '"SF Mono", monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{cmd}</code>
                    <CopyBtn text={cmd} />
                  </div>
                ))}
              </div>
            </>
          )}
        </div>
      )}
    </div>
  )
}

export default CapacityPlanningPage
