// Topology view — live cluster resource graph using React Flow.
import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { ReactFlow, Background, Controls, MiniMap, type Node, type Edge, useNodesState, useEdgesState } from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { api } from '@/api/kubesense';
import { useStore } from '@/store';

const KIND_COLORS: Record<string, string> = {
  Deployment:             '#4a9eff',
  StatefulSet:            '#a855f7',
  DaemonSet:              '#00cec9',
  Pod:                    '#3fb950',
  Service:                '#f39c12',
  Ingress:                '#ff6b35',
  Node:                   '#636e72',
  ConfigMap:              '#74b9ff',
  Secret:                 '#fd79a8',
  PersistentVolumeClaim:  '#55efc4',
  Namespace:              '#dfe6e9',
};

export default function Topology() {
  const { activeClusterId } = useStore();
  const cid = activeClusterId ?? '';

  const [selectedKind, setSelectedKind] = useState('Deployment');
  const [selectedName, setSelectedName] = useState('');
  const [selectedNS, setSelectedNS]     = useState('');

  const { data, isLoading } = useQuery({
    queryKey: ['topology-graph', cid, selectedKind, selectedNS, selectedName],
    queryFn: () => api.getUpstreamChain(cid, selectedKind, selectedNS, selectedName),
    enabled: !!cid && !!selectedName,
  });

  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([]);

  useEffect(() => {
    if (!data?.upstream) return;

    const items = [
      { entity_id: `${selectedKind}/${selectedNS}/${selectedName}`, entity_kind: selectedKind, namespace: selectedNS, name: selectedName, depth: 0 },
      ...data.upstream,
    ];

    const newNodes: Node[] = items.map((n, i) => ({
      id: n.entity_id || `${n.entity_kind}/${n.namespace}/${n.name}`,
      data: { label: buildLabel(n.entity_kind, n.name, n.namespace) },
      position: { x: (i % 4) * 200, y: Math.floor(i / 4) * 100 },
      style: {
        background: KIND_COLORS[n.entity_kind] ?? '#636e72',
        color: '#fff',
        border: n.depth === 0 ? '2px solid #fff' : '1px solid rgba(255,255,255,0.2)',
        borderRadius: 8,
        padding: '8px 12px',
        fontSize: 11,
        fontFamily: 'JetBrains Mono, monospace',
        minWidth: 160,
      },
    }));

    // Build edges from depth ordering (each node connected to depth-1 parent)
    const newEdges: Edge[] = [];
    if (items.length > 1) {
      newEdges.push({
        id: `e-0-1`,
        source: newNodes[0].id,
        target: newNodes[1].id,
        style: { stroke: '#30363d', strokeWidth: 1.5 },
        animated: true,
      });
    }

    setNodes(newNodes);
    setEdges(newEdges);
  }, [data, selectedKind, selectedName, selectedNS]);

  return (
    <div className="h-full flex flex-col">
      {/* Toolbar */}
      <div className="flex items-center gap-3 px-4 py-2 border-b border-surface-border bg-surface-raised flex-shrink-0">
        <select className="input py-1 text-xs" value={selectedKind} onChange={e => setSelectedKind(e.target.value)}>
          {Object.keys(KIND_COLORS).map(k => <option key={k}>{k}</option>)}
        </select>
        <input className="input py-1 text-xs w-32" placeholder="namespace" value={selectedNS} onChange={e => setSelectedNS(e.target.value)} />
        <input className="input py-1 text-xs w-40" placeholder="resource name" value={selectedName} onChange={e => setSelectedName(e.target.value)} />
        {isLoading && <span className="text-xs text-gray-500">Loading...</span>}
        {data && <span className="text-xs text-gray-500">{data.count} upstream nodes</span>}
      </div>

      {/* Graph */}
      <div className="flex-1">
        {selectedName ? (
          <ReactFlow
            nodes={nodes}
            edges={edges}
            onNodesChange={onNodesChange}
            onEdgesChange={onEdgesChange}
            fitView
            attributionPosition="bottom-right"
          >
            <Background color="#30363d" gap={20} />
            <Controls style={{ background: '#161b22', border: '1px solid #30363d' }} />
            <MiniMap nodeColor={n => (n.style as any)?.background ?? '#636e72'} />
          </ReactFlow>
        ) : (
          <div className="h-full flex flex-col items-center justify-center text-gray-600">
            <p className="text-sm mb-1">Select a resource to visualize its topology</p>
            <p className="text-xs">Enter kind + namespace + name above to build the dependency graph</p>
          </div>
        )}
      </div>

      {/* Legend */}
      <div className="flex flex-wrap gap-3 px-4 py-2 border-t border-surface-border bg-surface-raised">
        {Object.entries(KIND_COLORS).slice(0, 8).map(([kind, color]) => (
          <div key={kind} className="flex items-center gap-1.5 text-xs text-gray-500">
            <span className="w-2.5 h-2.5 rounded-sm flex-shrink-0" style={{ background: color }} />
            {kind}
          </div>
        ))}
      </div>
    </div>
  );
}

function buildLabel(kind: string, name: string, ns: string) {
  return `${kind}\n${ns ? ns + '/' : ''}${name}`;
}
