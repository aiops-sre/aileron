// Intelligence view — SRE Intelligence Center.
// Shows chaos readiness, drift detection, toil metrics, noise budget in one place.
import { useQuery } from '@tanstack/react-query';
import { api } from '@/api/kubesense';
import { useStore } from '@/store';
import { Zap, AlertTriangle, TrendingUp, Bell, GitBranch, Activity } from 'lucide-react';
import clsx from 'clsx';

export default function Intelligence() {
  const { activeClusterId } = useStore();
  const cid = activeClusterId ?? '';

  const { data: playbooks } = useQuery({
    queryKey: ['playbooks', cid],
    queryFn: () => api.listPlaybooks(cid),
    enabled: !!cid,
  });

  const { data: changes } = useQuery({
    queryKey: ['changes-intel', cid],
    queryFn: () => api.getChangeHistory(cid, 24),
    enabled: !!cid,
  });

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="max-w-5xl mx-auto space-y-4">
        <div>
          <h1 className="text-lg font-semibold text-white">SRE Intelligence Center</h1>
          <p className="text-xs text-gray-500 mt-0.5">
            Everything an SRE needs: chaos readiness, drift detection, toil metrics, noise budget, playbooks.
          </p>
        </div>

        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">

          {/* Chaos Readiness */}
          <SectionCard
            title="Chaos Readiness"
            icon={<Zap size={14} className="text-warn" />}
            accent="border-warn"
            description="Are your workloads resilient to failures? Missing PDB, single replicas, no readiness probes."
          >
            <div className="text-xs text-gray-500 italic">
              Connect the kubesense-api to a live cluster to see chaos scores per workload.
              <br /><br />
              Each Deployment is scored 0–100 across: replica count, PDB coverage,
              readiness/liveness probes, resource limits, anti-affinity, image tag stability.
            </div>
          </SectionCard>

          {/* Drift Detection */}
          <SectionCard
            title="GitOps Drift Detection"
            icon={<AlertTriangle size={14} className="text-danger" />}
            accent="border-danger"
            description="Is your cluster state drifting from what Git says it should be?"
          >
            <div className="text-xs text-gray-500 italic">
              Register desired state from ArgoCD or Flux sync events.
              KubeSense will alert when live resources differ from Git —
              image version, replica count, config values, RBAC permissions.
              Drift is linked to the actor who caused it.
            </div>
          </SectionCard>

          {/* Toil Metrics */}
          <SectionCard
            title="Toil Quantification"
            icon={<TrendingUp size={14} className="text-purple" />}
            accent="border-purple"
            description="How many engineer-hours per week are spent on manual, repetitive operations?"
          >
            <div className="text-xs text-gray-500 italic">
              Record manual operations via the resolution feedback API.
              KubeSense computes toil per team per week and suggests specific
              automation (HPA, VPA, cert-manager, etc.) to eliminate each pattern.
            </div>
          </SectionCard>

          {/* Noise Budget */}
          <SectionCard
            title="Alert Noise Budget"
            icon={<Bell size={14} className="text-brand" />}
            accent="border-brand"
            description="Is your team drowning in alert noise? Track weekly alert volume and suppress duplicates."
          >
            <div className="text-xs text-gray-500 italic">
              Route all alerts through the noise budget tracker.
              Causal deduplication suppresses child alerts when the root cause is
              already being investigated. Weekly budget quota prevents alert flood.
            </div>
          </SectionCard>
        </div>

        {/* Playbooks */}
        <div className="card p-4">
          <div className="flex items-center gap-2 mb-3">
            <Activity size={14} className="text-purple" />
            <h2 className="text-sm font-medium text-gray-200">Auto-Generated Playbooks</h2>
            <span className="badge badge-info ml-auto">{playbooks?.total ?? 0} playbooks</span>
          </div>
          {playbooks?.playbooks.length ? (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
              {playbooks.playbooks.map(pb => (
                <PlaybookCard key={pb.id} playbook={pb} />
              ))}
            </div>
          ) : (
            <div className="text-xs text-gray-500 italic py-4 text-center">
              No playbooks yet. Resolve incidents via POST /api/v1/incidents/resolve
              to train the playbook generator with real resolution data.
            </div>
          )}
        </div>

        {/* Change history */}
        <div className="card p-4">
          <div className="flex items-center gap-2 mb-3">
            <GitBranch size={14} className="text-brand" />
            <h2 className="text-sm font-medium text-gray-200">Change Correlation (24h)</h2>
            <span className="badge badge-info ml-auto">{changes?.change_count ?? 0} changes</span>
          </div>
          {changes?.changes.length ? (
            <div className="space-y-1">
              {changes.changes.map((ch, i) => (
                <div key={i} className="flex items-center gap-3 text-xs py-1.5 px-2 rounded hover:bg-surface-overlay">
                  <span className={clsx(
                    'font-mono font-semibold w-8',
                    ch.correlation_score >= 0.7 ? 'text-danger' : ch.correlation_score >= 0.4 ? 'text-warn' : 'text-gray-500'
                  )}>
                    {Math.round(ch.correlation_score * 100)}%
                  </span>
                  <span className="text-gray-400">{ch.resource_kind}/{ch.name}</span>
                  <span className="text-gray-600">by {ch.actor}</span>
                  <span className="ml-auto text-gray-600">{timeSince(ch.occurred_at)}</span>
                </div>
              ))}
            </div>
          ) : (
            <div className="text-xs text-gray-500 italic text-center py-4">
              No changes tracked in the last 24 hours
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function SectionCard({ title, icon, accent, description, children }: {
  title: string; icon: React.ReactNode; accent: string; description: string; children: React.ReactNode;
}) {
  return (
    <div className={clsx('card p-4 border-l-2', accent)}>
      <div className="flex items-center gap-2 mb-1">
        {icon}
        <h3 className="text-sm font-medium text-gray-200">{title}</h3>
      </div>
      <p className="text-xs text-gray-500 mb-3">{description}</p>
      {children}
    </div>
  );
}

function PlaybookCard({ playbook }: { playbook: import('@/api/kubesense').Playbook }) {
  return (
    <div className="card p-3">
      <div className="flex items-center justify-between mb-2">
        <span className="text-xs font-medium text-gray-200">{playbook.failure_mode}</span>
        <span className={clsx('badge', playbook.overall_success_rate >= 0.8 ? 'badge-ok' : 'badge-warn')}>
          {Math.round(playbook.overall_success_rate * 100)}% success
        </span>
      </div>
      <div className="text-xs text-gray-500 mb-2">
        {playbook.data_points} incidents · {playbook.steps.length} steps
      </div>
      <div className="space-y-1">
        {playbook.steps.slice(0, 2).map(step => (
          <div key={step.order} className="text-xs text-gray-400 font-mono truncate">
            {step.order}. {step.command || step.action_type}
          </div>
        ))}
        {playbook.steps.length > 2 && (
          <div className="text-xs text-gray-600">+{playbook.steps.length - 2} more steps</div>
        )}
      </div>
    </div>
  );
}

function timeSince(iso: string): string {
  const ms = Date.now() - new Date(iso).getTime();
  const m = Math.floor(ms / 60000);
  if (m < 1) return 'just now';
  if (m < 60) return `${m}m ago`;
  return `${Math.floor(m / 60)}h ago`;
}
