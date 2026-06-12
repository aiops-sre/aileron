// Investigate view — full RCA investigation launcher with result display.
import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { api } from '@/api/kubesense';
import { useStore } from '@/store';
import { Search, Plus, Trash2, Activity, CheckCircle } from 'lucide-react';
import clsx from 'clsx';

export default function Investigate() {
  const { activeClusterId } = useStore();
  const cid = activeClusterId ?? '';

  const [resources, setResources] = useState([{ kind: 'Pod', namespace: '', name: '' }]);
  const [incidentTime, setIncidentTime] = useState('');
  const [invId, setInvId] = useState<string | null>(null);

  const mutation = useMutation({
    mutationFn: () => api.startInvestigation({
      cluster_id: cid,
      affected_resources: resources.filter(r => r.name),
      incident_time: incidentTime || undefined,
      async: true,
    }),
    onSuccess: (data) => setInvId(data.investigation_id),
  });

  const { data: result } = useQuery({
    queryKey: ['investigation', invId],
    queryFn: () => api.getInvestigation(invId!),
    enabled: !!invId,
    refetchInterval: (data) => (!data || data.status === 'running') ? 2000 : false,
  });

  const addResource = () => setResources(r => [...r, { kind: 'Pod', namespace: '', name: '' }]);
  const removeResource = (i: number) => setResources(r => r.filter((_, idx) => idx !== i));
  const updateResource = (i: number, field: string, value: string) =>
    setResources(r => r.map((res, idx) => idx === i ? { ...res, [field]: value } : res));

  const gradeColors: Record<string, string> = {
    A: 'text-ok', B: 'text-ok', C: 'text-warn', D: 'text-warn', F: 'text-danger',
  };

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="max-w-3xl mx-auto space-y-6">

        <div>
          <h1 className="text-lg font-semibold text-white">RCA Investigation</h1>
          <p className="text-xs text-gray-500 mt-0.5">
            Evidence-first root cause analysis. KubeSense traverses the topology graph,
            gathers K8s events, correlates changes, and ranks hypotheses by confidence.
          </p>
        </div>

        {/* Input form */}
        <div className="card p-4 space-y-4">
          <h2 className="text-sm font-medium text-gray-300">Affected Resources</h2>

          {resources.map((r, i) => (
            <div key={i} className="flex items-center gap-2">
              <select
                className="input py-1 text-xs w-28"
                value={r.kind}
                onChange={e => updateResource(i, 'kind', e.target.value)}
              >
                {['Pod', 'Deployment', 'StatefulSet', 'DaemonSet', 'Node', 'Service'].map(k => (
                  <option key={k}>{k}</option>
                ))}
              </select>
              <input
                className="input py-1 text-xs w-32"
                placeholder="namespace"
                value={r.namespace}
                onChange={e => updateResource(i, 'namespace', e.target.value)}
              />
              <input
                className="input py-1 text-xs flex-1"
                placeholder="resource name"
                value={r.name}
                onChange={e => updateResource(i, 'name', e.target.value)}
              />
              {resources.length > 1 && (
                <button className="btn-ghost p-1" onClick={() => removeResource(i)}>
                  <Trash2 size={12} />
                </button>
              )}
            </div>
          ))}

          <div className="flex items-center gap-3">
            <button className="btn-ghost text-xs flex items-center gap-1.5" onClick={addResource}>
              <Plus size={12} /> Add resource
            </button>
            <div className="flex items-center gap-2 ml-auto">
              <label className="text-xs text-gray-500">Incident time (optional)</label>
              <input
                type="datetime-local"
                className="input py-1 text-xs"
                value={incidentTime}
                onChange={e => setIncidentTime(e.target.value)}
              />
            </div>
          </div>

          <button
            className="btn-primary flex items-center gap-2"
            onClick={() => mutation.mutate()}
            disabled={mutation.isPending || !resources.some(r => r.name) || !cid}
          >
            <Search size={13} />
            {mutation.isPending ? 'Starting investigation...' : 'Investigate'}
          </button>

          {!cid && <p className="text-xs text-warn">No cluster selected — choose a cluster first</p>}
        </div>

        {/* Running indicator */}
        {invId && result?.status === 'running' && (
          <div className="card p-4 flex items-center gap-3">
            <Activity size={16} className="text-brand animate-spin" />
            <div>
              <p className="text-sm text-gray-200">Investigation running...</p>
              <p className="text-xs text-gray-500">Traversing topology · gathering evidence · scoring hypotheses</p>
            </div>
          </div>
        )}

        {/* Results */}
        {result?.status === 'completed' && (
          <div className="space-y-4 animate-fade-in">
            <div className="flex items-center gap-2">
              <CheckCircle size={15} className="text-ok" />
              <h2 className="text-sm font-medium text-gray-200">
                Investigation complete — {result.duration_ms}ms
              </h2>
              <span className={clsx('badge font-mono text-sm', gradeColors[result.evidence_grade])}>
                Grade {result.evidence_grade}
              </span>
            </div>

            {/* Root cause */}
            {result.root_cause && (
              <div className="card p-4 border-l-2 border-l-brand">
                <div className="text-xs text-gray-500 mb-2">Root cause identified</div>
                <div className="flex items-center gap-3 flex-wrap">
                  <span className="badge badge-danger text-sm">{result.root_cause.failure_mode}</span>
                  <span className="font-mono text-sm text-gray-200">
                    {result.root_cause.entity_kind}/{result.root_cause.entity_namespace}/{result.root_cause.entity_name}
                  </span>
                </div>
                <div className="mt-3 grid grid-cols-2 gap-3 text-xs">
                  <div>
                    <span className="text-gray-500">Confidence </span>
                    <span className="text-white font-semibold">{Math.round(result.root_cause.confidence * 100)}%</span>
                  </div>
                  <div>
                    <span className="text-gray-500">Overall confidence </span>
                    <span className="text-white font-semibold">{Math.round(result.confidence * 100)}%</span>
                  </div>
                  <div>
                    <span className="text-gray-500">Evidence items </span>
                    <span className="text-white font-semibold">{result.evidence_count}</span>
                  </div>
                  <div>
                    <span className="text-gray-500">Changes correlated </span>
                    <span className="text-white font-semibold">{result.change_count}</span>
                  </div>
                  <div>
                    <span className="text-gray-500">Hypotheses evaluated </span>
                    <span className="text-white font-semibold">{result.hypotheses}</span>
                  </div>
                  <div>
                    <span className="text-gray-500">Rejected </span>
                    <span className="text-gray-500">{result.rejected}</span>
                  </div>
                </div>
              </div>
            )}

            {/* Start new */}
            <button className="btn-ghost text-xs" onClick={() => { setInvId(null); mutation.reset(); }}>
              Start new investigation
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
