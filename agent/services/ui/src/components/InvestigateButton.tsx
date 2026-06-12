// InvestigateButton — launches a KubeSense RCA investigation from the UI.
// Shows real-time progress and the final result inline.
import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { api, type Investigation } from '@/api/kubesense';
import { Search, Clock, CheckCircle, XCircle, Activity } from 'lucide-react';
import clsx from 'clsx';

interface Props {
  clusterId: string;
  kind: string;
  namespace: string;
  name: string;
}

export default function InvestigateButton({ clusterId, kind, namespace, name }: Props) {
  const [invId, setInvId] = useState<string | null>(null);

  const startMutation = useMutation({
    mutationFn: () => api.startInvestigation({
      cluster_id: clusterId,
      affected_resources: [{ kind, namespace, name }],
      async: true,
    }),
    onSuccess: (data) => setInvId(data.investigation_id),
  });

  const { data: result, isLoading } = useQuery({
    queryKey: ['investigation', invId],
    queryFn: () => api.getInvestigation(invId!),
    enabled: !!invId,
    refetchInterval: (data) => (!data || data.status === 'running') ? 2000 : false,
  });

  const gradeColors: Record<string, string> = {
    A: 'text-ok', B: 'text-ok', C: 'text-warn', D: 'text-warn', F: 'text-danger',
  };

  return (
    <div className="space-y-4">
      <div>
        <h3 className="text-sm font-medium text-gray-300 mb-1">RCA Investigation</h3>
        <p className="text-xs text-gray-600">
          Trigger a full root cause analysis for this resource. KubeSense will traverse the
          topology graph, gather K8s events, correlate recent changes, and score hypotheses.
        </p>
      </div>

      {!invId && (
        <button
          className="btn-primary flex items-center gap-2"
          onClick={() => startMutation.mutate()}
          disabled={startMutation.isPending}
        >
          <Search size={13} />
          {startMutation.isPending ? 'Starting...' : 'Investigate this resource'}
        </button>
      )}

      {startMutation.isError && (
        <div className="card p-3 text-xs text-danger border-danger">
          {(startMutation.error as Error).message}
        </div>
      )}

      {invId && (
        <div className="space-y-3">
          {/* Status */}
          <div className="card p-3 flex items-center gap-3">
            {result?.status === 'running' || isLoading ? (
              <>
                <Activity size={14} className="text-brand animate-spin" />
                <span className="text-xs text-gray-300">Investigation running...</span>
              </>
            ) : result?.status === 'completed' ? (
              <>
                <CheckCircle size={14} className="text-ok" />
                <span className="text-xs text-gray-300">Completed in {result.duration_ms}ms</span>
              </>
            ) : (
              <>
                <XCircle size={14} className="text-danger" />
                <span className="text-xs text-danger">Investigation failed</span>
              </>
            )}
          </div>

          {/* Results */}
          {result?.status === 'completed' && result.root_cause && (
            <div className="space-y-3">
              {/* Evidence grade + confidence */}
              <div className="grid grid-cols-3 gap-2">
                <Metric label="Evidence Grade" value={result.evidence_grade} color={gradeColors[result.evidence_grade]} />
                <Metric label="Confidence" value={`${Math.round(result.confidence * 100)}%`} />
                <Metric label="Chain Depth" value={String(result.chain_length)} />
              </div>

              {/* Root cause */}
              <div className="card p-4 border-l-2 border-l-brand">
                <div className="text-xs text-gray-500 mb-1">Root cause identified</div>
                <div className="flex items-center gap-2">
                  <span className="badge badge-danger">{result.root_cause.failure_mode}</span>
                </div>
                <div className="mt-2 font-mono text-xs text-gray-300">
                  {result.root_cause.entity_kind}/{result.root_cause.entity_namespace}/{result.root_cause.entity_name}
                </div>
                <div className="mt-1 text-xs text-gray-500">
                  Confidence: <span className="text-white">{Math.round(result.root_cause.confidence * 100)}%</span>
                </div>
              </div>

              {/* Stats */}
              <div className="grid grid-cols-2 gap-2 text-xs">
                <div className="card p-2 text-center">
                  <div className="text-gray-100 font-semibold">{result.evidence_count}</div>
                  <div className="text-gray-500">Evidence items</div>
                </div>
                <div className="card p-2 text-center">
                  <div className="text-gray-100 font-semibold">{result.change_count}</div>
                  <div className="text-gray-500">Changes correlated</div>
                </div>
                <div className="card p-2 text-center">
                  <div className="text-gray-100 font-semibold">{result.hypotheses}</div>
                  <div className="text-gray-500">Hypotheses</div>
                </div>
                <div className="card p-2 text-center">
                  <div className="text-gray-100 font-semibold">{result.rejected}</div>
                  <div className="text-gray-500">Rejected</div>
                </div>
              </div>

              <button
                className="btn-ghost w-full text-xs"
                onClick={() => setInvId(null)}
              >
                Start new investigation
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function Metric({ label, value, color }: { label: string; value: string; color?: string }) {
  return (
    <div className="card p-3 text-center">
      <div className={clsx('text-xl font-bold font-mono', color ?? 'text-white')}>{value}</div>
      <div className="text-xs text-gray-500 mt-0.5">{label}</div>
    </div>
  );
}
