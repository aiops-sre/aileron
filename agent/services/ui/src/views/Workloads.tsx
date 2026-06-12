// Workloads view — resource browser with inline KubeSense intelligence overlays.
// Every resource row shows: chaos score badge, drift indicator, risk tier.
// Clicking a resource opens the detail panel with full intelligence context.
import { useState, useCallback } from 'react';
import { useQuery, useMutation } from '@tanstack/react-query';
import { motion, AnimatePresence } from 'framer-motion';
import { Search, ChevronRight, X, Zap, GitBranch, AlertTriangle, Play, CheckCircle } from 'lucide-react';
import { api, type WorkloadScore } from '@/api/kubesense';
import { useStore } from '@/store';
import ChaosScoreBadge from '@/components/ChaosScoreBadge';
import PreApplyRisk from '@/components/PreApplyRisk';
import InvestigateButton from '@/components/InvestigateButton';
import clsx from 'clsx';

// Simulated workload list — in production this is fetched via direct K8s API
// or through the kubesense-api proxy endpoint.
const MOCK_WORKLOADS = [
  { kind: 'Deployment', namespace: 'payments',  name: 'checkout',       replicas: 3, ready: 2, image: 'checkout:v2.1.0',  status: 'degraded' },
  { kind: 'Deployment', namespace: 'payments',  name: 'payment-svc',    replicas: 2, ready: 2, image: 'payment:v1.8.3',   status: 'running' },
  { kind: 'Deployment', namespace: 'production',name: 'api-gateway',    replicas: 5, ready: 5, image: 'gateway:v3.2.1',   status: 'running' },
  { kind: 'Deployment', namespace: 'production',name: 'auth-service',   replicas: 2, ready: 1, image: 'auth:latest',      status: 'degraded' },
  { kind: 'StatefulSet',namespace: 'databases', name: 'postgres-main',  replicas: 3, ready: 3, image: 'postgres:15.3',    status: 'running' },
  { kind: 'DaemonSet',  namespace: 'monitoring',name: 'node-exporter',  replicas: 6, ready: 6, image: 'node-exporter:v1', status: 'running' },
];

type Workload = typeof MOCK_WORKLOADS[0];

export default function Workloads() {
  const { activeClusterId, activeNamespace } = useStore();
  const cid = activeClusterId ?? '';

  const [selected, setSelected]   = useState<Workload | null>(null);
  const [filter, setFilter]        = useState('');
  const [kindFilter, setKindFilter] = useState('all');

  const visible = MOCK_WORKLOADS.filter(w => {
    const nsMatch = activeNamespace === 'all' || w.namespace === activeNamespace;
    const kindMatch = kindFilter === 'all' || w.kind === kindFilter;
    const textMatch = !filter || `${w.namespace}/${w.name}`.includes(filter.toLowerCase());
    return nsMatch && kindMatch && textMatch;
  });

  return (
    <div className="h-full flex overflow-hidden">

      {/* ── Resource list ─────────────────────────────────── */}
      <div className={clsx('flex flex-col border-r border-surface-border transition-all', selected ? 'w-80' : 'flex-1')}>

        {/* Toolbar */}
        <div className="flex items-center gap-2 px-3 py-2 border-b border-surface-border bg-surface-raised">
          <div className="relative flex-1">
            <Search size={12} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-gray-500" />
            <input
              className="input w-full pl-7 py-1 text-xs"
              placeholder="Filter by name or namespace..."
              value={filter}
              onChange={e => setFilter(e.target.value)}
            />
          </div>
          <select
            value={kindFilter}
            onChange={e => setKindFilter(e.target.value)}
            className="input py-1 text-xs"
          >
            <option value="all">All kinds</option>
            <option value="Deployment">Deployments</option>
            <option value="StatefulSet">StatefulSets</option>
            <option value="DaemonSet">DaemonSets</option>
          </select>
        </div>

        {/* Column headers */}
        <div className="grid grid-cols-12 px-3 py-1.5 text-xs text-gray-600 border-b border-surface-border bg-surface-raised">
          <span className="col-span-4">Name</span>
          <span className="col-span-2">Namespace</span>
          <span className="col-span-2 text-center">Status</span>
          <span className="col-span-2 text-center">Replicas</span>
          <span className="col-span-2 text-center">Chaos</span>
        </div>

        {/* Rows */}
        <div className="flex-1 overflow-y-auto">
          {visible.map((w, i) => (
            <WorkloadRow
              key={`${w.kind}/${w.namespace}/${w.name}`}
              workload={w}
              clusterId={cid}
              isSelected={selected?.name === w.name && selected?.namespace === w.namespace}
              onClick={() => setSelected(w.name === selected?.name && w.namespace === selected?.namespace ? null : w)}
            />
          ))}
          {visible.length === 0 && (
            <div className="py-12 text-center text-xs text-gray-600">No workloads match the current filter</div>
          )}
        </div>
      </div>

      {/* ── Detail panel ──────────────────────────────────── */}
      <AnimatePresence>
        {selected && (
          <motion.div
            key={selected.name}
            initial={{ x: 20, opacity: 0 }}
            animate={{ x: 0, opacity: 1 }}
            exit={{ x: 20, opacity: 0 }}
            transition={{ duration: 0.15 }}
            className="flex-1 flex flex-col overflow-hidden border-l border-surface-border"
          >
            <WorkloadDetail
              workload={selected}
              clusterId={cid}
              onClose={() => setSelected(null)}
            />
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}

// ─── WorkloadRow ──────────────────────────────────────────────────────────────

function WorkloadRow({ workload: w, clusterId, isSelected, onClick }: {
  workload: Workload; clusterId: string;
  isSelected: boolean; onClick: () => void;
}) {
  const { data: riskData } = useQuery({
    queryKey: ['risk', clusterId, w.kind, w.namespace, w.name],
    queryFn: () => api.scoreChange({
      cluster_id: clusterId, resource_kind: w.kind,
      namespace: w.namespace, name: w.name, change_type: 'image_update',
    }),
    enabled: !!clusterId,
    staleTime: 60_000,
  });

  const statusColor = w.status === 'running' ? 'bg-ok' : 'bg-danger';
  const readyFraction = w.replicas > 0 ? w.ready / w.replicas : 0;

  return (
    <div
      onClick={onClick}
      className={clsx(
        'grid grid-cols-12 items-center px-3 py-2 cursor-pointer border-b border-surface-border',
        'hover:bg-surface-raised transition-colors text-xs',
        isSelected ? 'bg-brand-glow border-l-2 border-l-brand' : ''
      )}
    >
      {/* Name */}
      <div className="col-span-4 flex items-center gap-1.5 min-w-0">
        <span className="font-mono text-gray-400 text-xs">{w.kind.slice(0, 1).toLowerCase()}/</span>
        <span className="text-gray-100 font-medium truncate">{w.name}</span>
        {w.image.includes('latest') && (
          <span className="badge badge-warn flex-shrink-0" title="Mutable :latest tag">!</span>
        )}
      </div>

      {/* Namespace */}
      <span className="col-span-2 text-gray-500 font-mono truncate">{w.namespace}</span>

      {/* Status */}
      <div className="col-span-2 flex justify-center">
        <span className={clsx('w-1.5 h-1.5 rounded-full', statusColor)} />
      </div>

      {/* Replicas */}
      <div className="col-span-2 text-center">
        <span className={clsx(readyFraction < 1 ? 'text-warn' : 'text-gray-400')}>
          {w.ready}/{w.replicas}
        </span>
      </div>

      {/* Chaos score badge */}
      <div className="col-span-2 flex justify-center">
        <ChaosScoreBadge
          clusterId={clusterId}
          kind={w.kind}
          namespace={w.namespace}
          name={w.name}
          compact
        />
      </div>
    </div>
  );
}

// ─── WorkloadDetail ───────────────────────────────────────────────────────────

function WorkloadDetail({ workload: w, clusterId, onClose }: {
  workload: Workload; clusterId: string; onClose: () => void;
}) {
  const [activeTab, setActiveTab] = useState<'overview' | 'chaos' | 'risk' | 'topology' | 'investigate'>('overview');

  const TABS = [
    { id: 'overview',    label: 'Overview' },
    { id: 'chaos',       label: 'Chaos Score' },
    { id: 'risk',        label: 'Risk Score' },
    { id: 'topology',    label: 'Topology' },
    { id: 'investigate', label: 'Investigate' },
  ] as const;

  return (
    <div className="h-full flex flex-col">
      {/* Header */}
      <div className="flex items-center gap-3 px-4 py-3 border-b border-surface-border bg-surface-raised flex-shrink-0">
        <div>
          <div className="flex items-center gap-2">
            <span className="text-xs text-gray-500 font-mono">{w.kind}</span>
            <span className="text-gray-400">/</span>
            <h2 className="text-sm font-semibold text-white">{w.name}</h2>
          </div>
          <p className="text-xs text-gray-500">{w.namespace} · {w.image}</p>
        </div>
        <button onClick={onClose} className="ml-auto btn-ghost p-1">
          <X size={14} />
        </button>
      </div>

      {/* Tabs */}
      <div className="flex border-b border-surface-border bg-surface-raised flex-shrink-0">
        {TABS.map(t => (
          <button
            key={t.id}
            onClick={() => setActiveTab(t.id)}
            className={clsx(
              'px-4 py-2 text-xs font-medium border-b-2 transition-colors',
              activeTab === t.id
                ? 'border-brand text-brand'
                : 'border-transparent text-gray-500 hover:text-gray-300'
            )}
          >
            {t.label}
          </button>
        ))}
      </div>

      {/* Tab content */}
      <div className="flex-1 overflow-y-auto">
        {activeTab === 'overview' && <OverviewTab w={w} />}
        {activeTab === 'chaos' && (
          <div className="p-4">
            <ChaosScoreBadge clusterId={clusterId} kind={w.kind} namespace={w.namespace} name={w.name} expanded />
          </div>
        )}
        {activeTab === 'risk' && (
          <div className="p-4">
            <PreApplyRisk clusterId={clusterId} kind={w.kind} namespace={w.namespace} name={w.name} />
          </div>
        )}
        {activeTab === 'topology' && <TopologyTab clusterId={clusterId} w={w} />}
        {activeTab === 'investigate' && (
          <div className="p-4">
            <InvestigateButton clusterId={clusterId} kind={w.kind} namespace={w.namespace} name={w.name} />
          </div>
        )}
      </div>
    </div>
  );
}

function OverviewTab({ w }: { workload: Workload }) {
  const rows: [string, string][] = [
    ['Kind',       w.kind],
    ['Namespace',  w.namespace],
    ['Name',       w.name],
    ['Image',      w.image],
    ['Replicas',   `${w.ready} ready / ${w.replicas} desired`],
    ['Status',     w.status],
  ];
  return (
    <div className="p-4">
      <div className="card overflow-hidden">
        <table className="w-full text-xs">
          <tbody>
            {rows.map(([k, v]) => (
              <tr key={k} className="border-b border-surface-border last:border-0">
                <td className="py-2 px-3 text-gray-500 w-32 font-medium">{k}</td>
                <td className="py-2 px-3 text-gray-200 font-mono">{v}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function TopologyTab({ clusterId, w }: { clusterId: string; workload: Workload }) {
  const { data, isLoading } = useQuery({
    queryKey: ['topology', clusterId, w.kind, w.namespace, w.name],
    queryFn: () => api.getUpstreamChain(clusterId, w.kind, w.namespace, w.name),
    enabled: !!clusterId,
  });
  const { data: blastData } = useQuery({
    queryKey: ['blast', clusterId, w.kind, w.namespace, w.name],
    queryFn: () => api.getBlastRadius(clusterId, w.kind, w.namespace, w.name),
    enabled: !!clusterId,
  });

  if (isLoading) return <div className="p-4 text-xs text-gray-500">Loading topology...</div>;

  return (
    <div className="p-4 space-y-4">
      <Section title="Upstream chain" count={data?.count ?? 0}>
        {data?.upstream.map((n, i) => (
          <NodeRow key={i} node={n} />
        ))}
      </Section>
      <Section title="Blast radius (downstream)" count={blastData?.total_affected ?? 0}>
        {blastData?.affected.map((n, i) => (
          <NodeRow key={i} node={n} />
        ))}
      </Section>
    </div>
  );
}

function Section({ title, count, children }: { title: string; count: number; children: React.ReactNode }) {
  return (
    <div>
      <div className="flex items-center gap-2 mb-2">
        <h3 className="text-xs font-medium text-gray-400">{title}</h3>
        <span className="badge badge-info">{count}</span>
      </div>
      <div className="space-y-1">{children}</div>
    </div>
  );
}

function NodeRow({ node }: { node: import('@/api/kubesense').TopologyNode }) {
  return (
    <div className="flex items-center gap-2 px-2 py-1.5 rounded bg-surface-overlay border border-surface-border text-xs">
      <span className="text-gray-500 w-20 flex-shrink-0">{node.entity_kind}</span>
      <span className="text-gray-300 font-mono truncate">{node.namespace}/{node.name}</span>
      {node.depth > 0 && <span className="ml-auto text-gray-600 text-xs">depth {node.depth}</span>}
    </div>
  );
}
