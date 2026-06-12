// PreApplyRisk — shows risk score BEFORE applying a manifest.
// Called from the workload detail panel and from the YAML editor.
// The most unique IDE feature: pre-deploy intelligence at edit time.
import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { api, type RiskScore } from '@/api/kubesense';
import { AlertTriangle, CheckCircle, Info, ChevronDown, ChevronUp } from 'lucide-react';
import clsx from 'clsx';

interface Props {
  clusterId: string;
  kind: string;
  namespace: string;
  name: string;
  newImageTag?: string;
  oldImageTag?: string;
  changeType?: string;
}

export default function PreApplyRisk({
  clusterId, kind, namespace, name,
  newImageTag, oldImageTag, changeType = 'image_update',
}: Props) {
  const [expanded, setExpanded] = useState(false);

  const mutation = useMutation({
    mutationFn: () => api.scoreChange({
      cluster_id: clusterId, resource_kind: kind,
      namespace, name, change_type: changeType,
      new_image_tag: newImageTag, old_image_tag: oldImageTag,
    }),
  });

  const score = mutation.data;

  const levelConfig: Record<RiskScore['level'], { label: string; className: string; icon: React.ReactNode }> = {
    low:      { label: 'Low Risk',      className: 'border-ok bg-ok-dim text-ok',         icon: <CheckCircle size={14} /> },
    medium:   { label: 'Medium Risk',   className: 'border-warn bg-warn-dim text-warn',    icon: <Info size={14} /> },
    high:     { label: 'High Risk',     className: 'border-danger bg-danger-dim text-danger', icon: <AlertTriangle size={14} /> },
    critical: { label: 'Critical Risk', className: 'border-critical bg-critical-dim text-critical', icon: <AlertTriangle size={14} /> },
  };

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium text-gray-300">Pre-Apply Risk Score</h3>
        <button
          className="btn-primary text-xs"
          onClick={() => mutation.mutate()}
          disabled={mutation.isPending}
        >
          {mutation.isPending ? 'Scoring...' : 'Score this change'}
        </button>
      </div>

      <p className="text-xs text-gray-600">
        Score this change against historical incident patterns before applying to the cluster.
        KubeSense checks resource type, namespace tier, time-of-day, image tag patterns, and prior incidents.
      </p>

      {mutation.isError && (
        <div className="card p-3 border-danger text-xs text-danger">
          {(mutation.error as Error).message}
        </div>
      )}

      {score && (
        <div className="space-y-3">
          {/* Main score card */}
          <div className={clsx('card border p-4', levelConfig[score.level].className)}>
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2">
                {levelConfig[score.level].icon}
                <span className="font-semibold">{levelConfig[score.level].label}</span>
              </div>
              <span className="text-2xl font-bold font-mono">{Math.round(score.raw_score * 100)}%</span>
            </div>
            <p className="text-xs mt-2 opacity-80 leading-relaxed">{score.summary}</p>
          </div>

          {/* Factor breakdown */}
          <button
            onClick={() => setExpanded(!expanded)}
            className="w-full flex items-center justify-between text-xs text-gray-400 hover:text-gray-300 transition-colors"
          >
            <span>Risk factor breakdown ({score.factors.length} factors)</span>
            {expanded ? <ChevronUp size={12} /> : <ChevronDown size={12} />}
          </button>

          {expanded && (
            <div className="space-y-2">
              {score.factors
                .sort((a, b) => b.score - a.score)
                .map((f, i) => (
                  <div key={i} className="flex items-center gap-3 text-xs">
                    <div className="w-32 text-gray-500 truncate">{f.name.replace(/_/g, ' ')}</div>
                    <div className="flex-1 h-1.5 bg-surface-overlay rounded-full overflow-hidden">
                      <div
                        className={clsx(
                          'h-full rounded-full transition-all',
                          f.score >= 0.7 ? 'bg-danger' : f.score >= 0.4 ? 'bg-warn' : 'bg-ok'
                        )}
                        style={{ width: `${f.score * 100}%` }}
                      />
                    </div>
                    <span className="w-8 text-right text-gray-500">{Math.round(f.score * 100)}%</span>
                  </div>
                ))}
            </div>
          )}

          {/* Similar past incidents */}
          {score.similar_incidents.length > 0 && (
            <div className="card p-3 border-warn bg-warn-dim">
              <h4 className="text-xs font-medium text-warn mb-2">
                Similar historical incidents ({score.similar_incidents.length})
              </h4>
              <div className="space-y-1">
                {score.similar_incidents.map((inc, i) => (
                  <div key={i} className="text-xs text-gray-300">
                    <span className="font-mono text-warn">{Math.round(inc.similarity * 100)}% match</span>
                    {' — '}{inc.failure_mode} in <span className="font-mono">{inc.namespace}</span>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
