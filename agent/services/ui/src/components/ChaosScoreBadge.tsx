// ChaosScoreBadge — inline chaos readiness indicator.
// compact mode: a small coloured score pill (e.g. A/73).
// expanded mode: full finding list with remediation.
import { useQuery } from '@tanstack/react-query';
import { api } from '@/api/kubesense';
import clsx from 'clsx';
import { AlertTriangle, CheckCircle, Minus } from 'lucide-react';

interface Props {
  clusterId: string;
  kind: string;
  namespace: string;
  name: string;
  compact?: boolean;
  expanded?: boolean;
}

export default function ChaosScoreBadge({ clusterId, kind, namespace, name, compact, expanded }: Props) {
  // In production this calls a cluster-level chaos score endpoint
  // Here we score inline using the risk/score API as a proxy
  const { data: riskData, isLoading } = useQuery({
    queryKey: ['risk-chaos', clusterId, kind, namespace, name],
    queryFn: () => api.scoreChange({ cluster_id: clusterId, resource_kind: kind, namespace, name, change_type: 'image_update' }),
    enabled: !!clusterId && !!kind && !!name,
    staleTime: 120_000,
  });

  if (isLoading) {
    return compact ? <span className="badge bg-surface-border text-gray-500">—</span> : null;
  }

  // Derive a pseudo-chaos score from risk factors
  const score = riskData ? Math.round((1 - riskData.raw_score) * 100) : null;
  const grade = score == null ? '?' : score >= 90 ? 'A' : score >= 75 ? 'B' : score >= 55 ? 'C' : score >= 35 ? 'D' : 'F';
  const level = grade === 'A' || grade === 'B' ? 'ok' : grade === 'C' ? 'warn' : 'danger';
  const levelColors: Record<string, string> = {
    ok: 'badge-ok', warn: 'badge-warn', danger: 'badge-danger',
  };

  if (compact) {
    return (
      <span className={clsx('badge font-mono font-semibold', levelColors[level])}>
        {grade}{score != null ? `·${score}` : ''}
      </span>
    );
  }

  if (expanded && riskData) {
    return (
      <div className="space-y-4">
        {/* Score summary */}
        <div className="flex items-center gap-4 card p-4">
          <div className="text-center">
            <div className={clsx('text-4xl font-bold font-mono', `text-${level === 'ok' ? 'ok' : level === 'warn' ? 'warn' : 'danger'}`)}>
              {grade}
            </div>
            <div className="text-xs text-gray-500 mt-0.5">Grade</div>
          </div>
          <div>
            <div className="text-lg font-semibold text-white">{score ?? '—'}<span className="text-sm text-gray-500">/100</span></div>
            <div className="text-xs text-gray-500">Chaos readiness score</div>
            <div className="text-xs text-gray-600 mt-1">{riskData.summary}</div>
          </div>
        </div>

        {/* Risk factors as findings */}
        <div>
          <h3 className="text-xs font-medium text-gray-400 mb-2">Risk Factors</h3>
          <div className="space-y-2">
            {riskData.factors
              .sort((a, b) => b.score - a.score)
              .map((f, i) => (
                <div key={i} className="card p-3">
                  <div className="flex items-center justify-between mb-1">
                    <span className="text-xs font-medium text-gray-200">{f.name.replace(/_/g, ' ')}</span>
                    <ScoreBar value={f.score} />
                  </div>
                  <p className="text-xs text-gray-500">{f.description}</p>
                </div>
              ))}
          </div>
        </div>

        {/* Similar historical incidents */}
        {riskData.similar_incidents.length > 0 && (
          <div>
            <h3 className="text-xs font-medium text-gray-400 mb-2">Similar Historical Incidents</h3>
            <div className="space-y-1">
              {riskData.similar_incidents.map((inc, i) => (
                <div key={i} className="flex items-center gap-3 px-3 py-2 card text-xs">
                  <span className="text-danger font-mono font-semibold">{Math.round(inc.similarity * 100)}%</span>
                  <span className="text-gray-300">{inc.failure_mode}</span>
                  <span className="text-gray-500">in {inc.namespace}</span>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
    );
  }

  return <span className={clsx('badge font-mono', levelColors[level])}>{grade}</span>;
}

function ScoreBar({ value }: { value: number }) {
  const color = value >= 0.7 ? 'bg-danger' : value >= 0.4 ? 'bg-warn' : 'bg-ok';
  return (
    <div className="flex items-center gap-2">
      <div className="w-20 h-1.5 bg-surface-overlay rounded-full overflow-hidden">
        <div className={clsx('h-full rounded-full', color)} style={{ width: `${value * 100}%` }} />
      </div>
      <span className="text-xs text-gray-500 w-8 text-right">{Math.round(value * 100)}%</span>
    </div>
  );
}
