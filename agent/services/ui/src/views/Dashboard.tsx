import { useQuery } from '@tanstack/react-query';
import { motion } from 'framer-motion';
import { AlertTriangle, CheckCircle, TrendingUp, Shield, Zap, Clock, Activity, GitBranch } from 'lucide-react';
import { api } from '@/api/kubesense';
import { useStore } from '@/store';
import clsx from 'clsx';

const card = 'card p-4 animate-fade-in';

export default function Dashboard() {
  const { activeClusterId } = useStore();
  const cid = activeClusterId ?? '';

  const { data: clusters }  = useQuery({ queryKey: ['clusters'], queryFn: api.listClusters });
  const { data: changes }   = useQuery({
    queryKey: ['changes', cid], queryFn: () => api.getChangeHistory(cid, 2),
    enabled: !!cid,
  });
  const { data: playbooks } = useQuery({
    queryKey: ['playbooks', cid], queryFn: () => api.listPlaybooks(cid),
    enabled: !!cid,
  });

  const cluster = clusters?.clusters.find(c => c.id === cid);

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="max-w-6xl mx-auto space-y-4">

        {/* Header */}
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-lg font-semibold text-white">Intelligence Dashboard</h1>
            <p className="text-xs text-gray-500 mt-0.5">
              {cluster ? `Cluster: ${cluster.id} · ${cluster.node_count} nodes · ${cluster.status}` : 'No cluster selected'}
            </p>
          </div>
          <div className="flex items-center gap-2 text-xs text-gray-500">
            <span className="w-1.5 h-1.5 rounded-full bg-ok animate-pulse" />
            Live
          </div>
        </div>

        {/* ── KPI row ─────────────────────────────────────────────── */}
        <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
          <KPICard
            label="Cluster Health"
            value={cluster?.status === 'active' ? 'Healthy' : 'Degraded'}
            icon={<CheckCircle size={16} />}
            accent="ok"
            sub={`${cluster?.node_count ?? 0} nodes active`}
          />
          <KPICard
            label="Recent Changes"
            value={String(changes?.change_count ?? '—')}
            icon={<GitBranch size={16} />}
            accent="brand"
            sub="last 2 hours"
          />
          <KPICard
            label="Playbooks"
            value={String(playbooks?.total ?? '—')}
            icon={<Activity size={16} />}
            accent="purple"
            sub="auto-generated runbooks"
          />
          <KPICard
            label="Avg Heartbeat"
            value={cluster?.last_heartbeat ? timeSince(cluster.last_heartbeat) : '—'}
            icon={<Clock size={16} />}
            accent="teal"
            sub="agent last seen"
          />
        </div>

        {/* ── Main content grid ─────────────────────────────────── */}
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">

          {/* Recent changes with correlation */}
          <div className={clsx(card, 'lg:col-span-2')}>
            <h2 className="text-sm font-medium text-gray-200 mb-3 flex items-center gap-2">
              <GitBranch size={14} className="text-brand" />
              Recent Changes & Correlation
            </h2>
            {changes?.changes.length ? (
              <div className="space-y-2">
                {changes.changes.slice(0, 6).map((ch, i) => (
                  <ChangeRow key={i} change={ch} />
                ))}
              </div>
            ) : (
              <EmptyState text="No changes in the last 2 hours" />
            )}
          </div>

          {/* Playbooks */}
          <div className={card}>
            <h2 className="text-sm font-medium text-gray-200 mb-3 flex items-center gap-2">
              <Activity size={14} className="text-purple" />
              Auto-Generated Playbooks
            </h2>
            {playbooks?.playbooks.length ? (
              <div className="space-y-2">
                {playbooks.playbooks.slice(0, 5).map(pb => (
                  <PlaybookRow key={pb.id} playbook={pb} />
                ))}
              </div>
            ) : (
              <EmptyState text="No playbooks yet — resolve incidents to build them" />
            )}
          </div>
        </div>

        {/* ── Intelligence pills ────────────────────────────────── */}
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
          <IntelligenceCard
            title="Chaos Readiness"
            description="Score every workload for failure tolerance — single replicas, missing probes, no PDB."
            href="/intelligence"
            accent="border-warn"
            icon={<Zap size={15} className="text-warn" />}
          />
          <IntelligenceCard
            title="Drift Detection"
            description="Compare live cluster state against your GitOps desired state. Find who changed what manually."
            href="/intelligence"
            accent="border-danger"
            icon={<AlertTriangle size={15} className="text-danger" />}
          />
          <IntelligenceCard
            title="Toil Metrics"
            description="Track engineer-hours spent on manual operations. Identify what to automate first."
            href="/intelligence"
            accent="border-purple"
            icon={<TrendingUp size={15} className="text-purple" />}
          />
        </div>

      </div>
    </div>
  );
}

// ─── Sub-components ───────────────────────────────────────────────────────────

function KPICard({ label, value, icon, accent, sub }: {
  label: string; value: string; icon: React.ReactNode;
  accent: string; sub: string;
}) {
  const colors: Record<string, string> = {
    ok: 'text-ok', brand: 'text-brand', warn: 'text-warn',
    danger: 'text-danger', purple: 'text-purple', teal: 'text-teal',
  };
  return (
    <div className={card}>
      <div className="flex items-start justify-between">
        <div>
          <p className="text-xs text-gray-500">{label}</p>
          <p className={clsx('text-xl font-semibold mt-0.5', colors[accent])}>{value}</p>
          <p className="text-xs text-gray-600 mt-0.5">{sub}</p>
        </div>
        <div className={clsx('p-1.5 rounded-md', `bg-${accent}-dim`, colors[accent])}>{icon}</div>
      </div>
    </div>
  );
}

function ChangeRow({ change }: { change: NonNullable<ReturnType<typeof api.getChangeHistory> extends Promise<infer T> ? T : never>['changes'][0] }) {
  const score = change.correlation_score;
  const color = score >= 0.7 ? 'text-danger' : score >= 0.4 ? 'text-warn' : 'text-gray-500';
  return (
    <div className="flex items-center gap-3 py-1.5 px-2 rounded-md hover:bg-surface-overlay transition-colors">
      <div className={clsx('text-xs font-mono font-semibold w-8 text-right', color)}>
        {Math.round(score * 100)}%
      </div>
      <div className="flex-1 min-w-0">
        <p className="text-xs text-gray-200 truncate">
          <span className="text-gray-500">{change.resource_kind}/</span>{change.name}
        </p>
        <p className="text-xs text-gray-600 truncate">{change.actor} · {change.source}</p>
      </div>
      <span className="text-xs text-gray-600 font-mono flex-shrink-0">
        {timeSince(change.occurred_at)}
      </span>
    </div>
  );
}

function PlaybookRow({ playbook }: { playbook: import('@/api/kubesense').Playbook }) {
  return (
    <div className="flex items-center gap-2 py-1.5 px-2 rounded-md hover:bg-surface-overlay transition-colors cursor-pointer">
      <div className="flex-1 min-w-0">
        <p className="text-xs text-gray-200 truncate">{playbook.failure_mode}</p>
        <p className="text-xs text-gray-600">{playbook.data_points} incidents · {playbook.steps.length} steps</p>
      </div>
      <span className={clsx('badge text-xs', playbook.overall_success_rate >= 0.8 ? 'badge-ok' : 'badge-warn')}>
        {Math.round(playbook.overall_success_rate * 100)}%
      </span>
    </div>
  );
}

function IntelligenceCard({ title, description, accent, icon, href }: {
  title: string; description: string; accent: string; icon: React.ReactNode; href: string;
}) {
  return (
    <a href={href} className={clsx('card p-4 block border-l-2 hover:bg-surface-overlay transition-colors cursor-pointer', accent)}>
      <div className="flex items-center gap-2 mb-2">
        {icon}
        <h3 className="text-sm font-medium text-gray-200">{title}</h3>
      </div>
      <p className="text-xs text-gray-500 leading-relaxed">{description}</p>
    </a>
  );
}

function EmptyState({ text }: { text: string }) {
  return <p className="text-xs text-gray-600 text-center py-6">{text}</p>;
}

function timeSince(iso: string): string {
  const ms = Date.now() - new Date(iso).getTime();
  const m = Math.floor(ms / 60000);
  if (m < 1) return 'just now';
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}
