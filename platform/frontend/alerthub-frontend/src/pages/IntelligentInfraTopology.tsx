import React, { useState, useEffect, useCallback, useRef, useMemo, memo } from 'react';
import { motion, AnimatePresence } from 'framer-motion';
import {
  Server, Box, Cpu, Search, RefreshCw, ChevronRight,
  X, Activity, CheckCircle, AlertTriangle, XCircle,
  HardDrive, Network, GitBranch, Layers, Clock,
  ArrowUpRight, Zap, Info, Globe, Copy, Check,
  Filter, AlertCircle, Database, Archive, Cloud,
} from 'lucide-react';

// ─── types ────────────────────────────────────────────────────────────────────
interface GraphNode {
  id: string;
  node_type: 'bare_metal' | 'cloudstack_vm' | 'k8s_cluster' | 'k8s_node' | 'k8s_pod'
           | 'netapp_cluster' | 'netapp_node' | 'netapp_aggregate' | 'netapp_svm' | 'netapp_volume'
           | 'netapp_s3_bucket' | 'k8s_pvc' | 'k8s_pv';
  label: string;
  status: string;
  health: string;
  layer: number;
  parent_id?: string;
  data: Record<string, any>;
}
interface GraphEdge {
  id: string; source: string; target: string;
  edge_type: string; label?: string; animated: boolean;
}
interface LayerStat {
  count: number; healthy: number; degraded: number; unhealthy: number; unknown: number;
}
interface SourceSyncInfo {
  name: string;
  type: 'cloudstack' | 'k8s';
  last_sync: string;
  node_count: number;
  is_stale?: boolean;
}
interface InfraGraph {
  nodes: GraphNode[]; edges: GraphEdge[];
  stats: { total_nodes: number; total_edges: number };
  layer_stats: Record<string, LayerStat>;
  cached_at: string; cache_age_seconds: number;
  sources?: SourceSyncInfo[];
  is_data_stale?: boolean;
  stale_reason?: string;
  cs_last_sync?: string;
  building?: boolean;
}
interface ExpandResult {
  parent_id: string; children: GraphNode[]; edges: GraphEdge[];
}
interface SearchResult {
  node: GraphNode; parents: GraphNode[]; children: GraphNode[];
}
type HealthFilter = 'all' | 'healthy' | 'degraded' | 'unhealthy';
type ActiveLayer = 'all' | 'bare_metal' | 'cloudstack_vm' | 'k8s_cluster' | 'k8s_node' | 'k8s_pod'
                | 'netapp_cluster' | 'netapp_node' | 'netapp_aggregate' | 'netapp_svm' | 'netapp_volume'
                | 'netapp_s3_bucket' | 'k8s_pvc' | 'k8s_pv';

// ─── design tokens ────────────────────────────────────────────────────────────
const c = {
  bg:     'var(--color-background)',
  card:   'var(--color-card, rgba(255,255,255,0.85))',
  text:   'var(--color-text)',
  sub:    'var(--color-text-secondary)',
  sep:    'var(--color-separator, rgba(142,142,147,0.15))',
  fill:   'var(--color-fill, rgba(142,142,147,0.08))',
  blue:   '#007AFF', green:  '#34C759', red:    '#FF3B30',
  orange: '#FF9500', purple: '#AF52DE', gray:   '#8E8E93',
  teal:   '#32ADE6', indigo: '#5856D6', yellow: '#FFCC00',
};

const NODE_CFG: Record<string, { color: string; icon: React.ElementType; label: string }> = {
  bare_metal:       { color: c.gray,   icon: HardDrive, label: 'Bare Metal'         },
  cloudstack_vm:    { color: c.blue,   icon: Server,    label: 'CloudStack VM'       },
  k8s_cluster:      { color: c.teal,   icon: Globe,     label: 'K8s Cluster'         },
  k8s_node:         { color: c.indigo, icon: Cpu,       label: 'K8s Node'            },
  k8s_pod:          { color: c.purple, icon: Box,       label: 'Pod'                 },
  k8s_pvc:          { color: '#5AC8FA', icon: Archive,  label: 'PVC'                 },
  k8s_pv:           { color: '#5AC8FA', icon: Archive,  label: 'PV'                  },
  netapp_cluster:   { color: '#007AFF', icon: Network,   label: 'NetApp Cluster'      },
  netapp_node:      { color: '#30D158', icon: Server,    label: 'NetApp Node'         },
  netapp_aggregate: { color: '#FF9F0A', icon: Layers,    label: 'Aggregate'           },
  netapp_svm:       { color: '#32ADE6', icon: GitBranch, label: 'SVM'                 },
  netapp_volume:    { color: '#FF6B35', icon: Database,  label: 'Volume'              },
  netapp_s3_bucket: { color: '#BF5AF2', icon: Cloud,     label: 'S3 Bucket'           },
};
const HEALTH_COLOR: Record<string, string> = {
  healthy: c.green, degraded: c.orange, unhealthy: c.red, unknown: c.gray,
};

function HealthIcon({ h, size = 12 }: { h: string; size?: number }) {
  const s = { width: size, height: size, flexShrink: 0 as const };
  if (h === 'healthy')   return <CheckCircle   style={{ ...s, color: c.green  }} />;
  if (h === 'degraded')  return <AlertTriangle style={{ ...s, color: c.orange }} />;
  if (h === 'unhealthy') return <XCircle       style={{ ...s, color: c.red    }} />;
  return <Activity style={{ ...s, color: c.gray }} />;
}

// ─── capacity bar ─────────────────────────────────────────────────────────────
function CapacityBar({ totalGB, usedGB, percent }: { totalGB: number; usedGB: number; percent?: number }) {
  const pct = percent != null ? percent : totalGB > 0 ? Math.round((usedGB / totalGB) * 100) : 0;
  const freeGB = Math.max(0, totalGB - usedGB);
  const barColor = pct >= 85 ? c.red : pct >= 70 ? c.orange : c.green;
  const fmt = (gb: number) =>
    gb >= 1024 ? `${(gb / 1024).toFixed(1)} TB` :
    gb >= 1    ? `${gb.toFixed(0)} GB` :
    gb > 0     ? `${(gb * 1024).toFixed(0)} MB` : '0 B';
  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 4 }}>
        <span style={{ fontSize: 10, color: c.sub }}>Capacity</span>
        <span style={{ fontSize: 11, fontWeight: 700, color: barColor }}>{pct}%</span>
      </div>
      <div style={{ height: 5, borderRadius: 3, background: c.sep, overflow: 'hidden', marginBottom: 5 }}>
        <div style={{ width: `${Math.min(pct, 100)}%`, height: '100%', background: barColor, borderRadius: 3, transition: 'width 0.4s ease' }} />
      </div>
      <div style={{ display: 'flex', justifyContent: 'space-between' }}>
        <span style={{ fontSize: 10, fontWeight: 600, color: c.text }}>{fmt(usedGB)} used</span>
        <span style={{ fontSize: 10, color: c.sub }}>{fmt(freeGB)} free · {fmt(totalGB)} total</span>
      </div>
    </div>
  );
}

// ─── storage relation section ─────────────────────────────────────────────────
function StorageSection({ node, allEdges, nodeByID, onSelect }: {
  node: GraphNode; allEdges: GraphEdge[];
  nodeByID: Record<string, GraphNode>; onSelect: (n: GraphNode) => void;
}) {
  const nt = node.node_type;
  const totalGB = Number(node.data?.space_total_gb ?? 0);
  const usedGB  = Number(node.data?.space_used_gb  ?? 0);
  const pct     = Number(node.data?.space_percent   ?? 0);

  const backingVolume: GraphNode | null = (() => {
    if (nt !== 'k8s_pvc' && nt !== 'k8s_pv') return null;
    // Direct: volume -[BACKS_PVC]-> this node
    const direct = allEdges.find(e => e.target === node.id && e.edge_type === 'backs_pvc');
    if (direct) return nodeByID[direct.source] ?? null;
    // For PVs: traverse to bound PVC first, then find its backing volume
    if (nt === 'k8s_pv') {
      for (const e of allEdges) {
        if (e.source !== node.id && e.target !== node.id) continue;
        const otherId = e.source === node.id ? e.target : e.source;
        const other = nodeByID[otherId];
        if (other?.node_type === 'k8s_pvc') {
          const volEdge = allEdges.find(ee => ee.target === other.id && ee.edge_type === 'backs_pvc');
          if (volEdge) return nodeByID[volEdge.source] ?? null;
        }
      }
    }
    return null;
  })();

  const boundPVCs: GraphNode[] = (() => {
    if (nt !== 'netapp_volume') return [];
    return allEdges.filter(e => e.source === node.id && e.edge_type === 'backs_pvc')
      .map(e => nodeByID[e.target]).filter(Boolean) as GraphNode[];
  })();

  const aggVolumes: GraphNode[] = (() => {
    if (nt !== 'netapp_aggregate') return [];
    return allEdges.filter(e => e.source === node.id && (e.edge_type === 'hosts_volume' || e.edge_type === 'has_volume'))
      .map(e => nodeByID[e.target]).filter((n): n is GraphNode => n?.node_type === 'netapp_volume');
  })();

  const clusterAggregates: GraphNode[] = (() => {
    if (nt !== 'netapp_cluster') return [];
    return Object.values(nodeByID).filter(n => n.node_type === 'netapp_aggregate' && n.data?.cluster === node.label);
  })();
  const clusterTotalGB   = clusterAggregates.reduce((s, a) => s + Number(a.data?.space_total_gb ?? 0), 0);
  const clusterUsedGB    = clusterAggregates.reduce((s, a) => s + Number(a.data?.space_used_gb  ?? 0), 0);
  const clusterVolCount  = Object.values(nodeByID).filter(n => n.node_type === 'netapp_volume'    && n.data?.cluster === node.label).length;
  const clusterBuckets   = Object.values(nodeByID).filter(n => n.node_type === 'netapp_s3_bucket' && n.data?.cluster === node.label);

  const box: React.CSSProperties = { marginBottom: 10, padding: '10px 11px', borderRadius: 9, background: c.fill, border: `1px solid ${c.sep}` };
  const sectionLabel: React.CSSProperties = { fontSize: 9, fontWeight: 800, color: c.sub, textTransform: 'uppercase', letterSpacing: '0.07em', marginBottom: 8, display: 'block' };
  const fmt = (gb: number) =>
    gb >= 1024 ? `${(gb / 1024).toFixed(1)} TB` :
    gb >= 1    ? `${gb.toFixed(0)} GB` :
    gb > 0     ? `${(gb * 1024).toFixed(0)} MB` : '0 B';

  return (
    <>
      {/* Capacity bar for any storage node that has space metrics */}
      {totalGB > 0 && (
        <div style={box}>
          <span style={sectionLabel}>Capacity</span>
          <CapacityBar totalGB={totalGB} usedGB={usedGB} percent={pct || undefined} />
          <div style={{ display: 'flex', gap: 10, marginTop: 6, flexWrap: 'wrap' }}>
            {node.data?.svm  && <span style={{ fontSize: 10, color: c.sub }}>SVM: <b style={{ color: c.text }}>{node.data.svm}</b></span>}
            {node.data?.node && <span style={{ fontSize: 10, color: c.sub }}>Node: <b style={{ color: c.text }}>{node.data.node}</b></span>}
          </div>
        </div>
      )}

      {/* netapp_cluster: aggregate capacity + summary */}
      {nt === 'netapp_cluster' && clusterAggregates.length > 0 && (
        <div style={box}>
          <span style={sectionLabel}>Storage Summary</span>
          {clusterTotalGB > 0 && <CapacityBar totalGB={clusterTotalGB} usedGB={clusterUsedGB} />}
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 5, marginTop: 8 }}>
            {[
              { label: 'Aggregates', v: clusterAggregates.length, color: '#FF9F0A' },
              { label: 'Volumes',    v: clusterVolCount,           color: '#FF6B35' },
              { label: 'S3 Buckets', v: clusterBuckets.length,     color: '#BF5AF2' },
              { label: 'Total',      v: fmt(clusterTotalGB),       color: c.blue   },
            ].map(item => (
              <div key={item.label} style={{ textAlign: 'center', padding: '5px 4px', borderRadius: 7,
                background: item.color + '12', border: `1px solid ${item.color}30` }}>
                <div style={{ fontSize: 14, fontWeight: 800, color: item.color, lineHeight: 1.1 }}>{item.v}</div>
                <div style={{ fontSize: 9, color: c.sub, marginTop: 2 }}>{item.label}</div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* netapp_aggregate: list child volumes with mini bars */}
      {nt === 'netapp_aggregate' && aggVolumes.length > 0 && (
        <div style={box}>
          <span style={sectionLabel}>Volumes ({aggVolumes.length})</span>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 4, maxHeight: 200, overflowY: 'auto' }}>
            {aggVolumes.map(vol => {
              const vT = Number(vol.data?.space_total_gb ?? 0);
              const vU = Number(vol.data?.space_used_gb  ?? 0);
              const vP = vT > 0 ? Math.round((vU / vT) * 100) : 0;
              const bc = vP >= 85 ? c.red : vP >= 70 ? c.orange : c.green;
              return (
                <div key={vol.id} onClick={() => onSelect(vol)} style={{ display: 'flex', alignItems: 'center', gap: 6,
                  cursor: 'pointer', padding: '4px 6px', borderRadius: 6,
                  background: c.card, border: `1px solid transparent`,
                  transition: 'border-color 0.1s' }}
                  onMouseEnter={e => (e.currentTarget.style.borderColor = c.sep)}
                  onMouseLeave={e => (e.currentTarget.style.borderColor = 'transparent')}>
                  <Database style={{ width: 10, height: 10, color: vol.data?.is_pvc ? '#5AC8FA' : '#FF6B35', flexShrink: 0 }} />
                  <span style={{ fontSize: 10, color: c.text, flex: 1, minWidth: 0,
                    overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {vol.label}
                  </span>
                  {vT > 0 && (
                    <div style={{ width: 44, height: 3, borderRadius: 2, background: c.sep, overflow: 'hidden', flexShrink: 0 }}>
                      <div style={{ width: `${Math.min(vP, 100)}%`, height: '100%', background: bc }} />
                    </div>
                  )}
                  {vP > 0 && <span style={{ fontSize: 9, color: bc, fontWeight: 700, flexShrink: 0, minWidth: 26, textAlign: 'right' }}>{vP}%</span>}
                </div>
              );
            })}
          </div>
        </div>
      )}

      {/* netapp_volume: bound K8s PVC(s) */}
      {nt === 'netapp_volume' && boundPVCs.length > 0 && (
        <div style={box}>
          <span style={sectionLabel}>K8s Binding{boundPVCs.length > 1 ? 's' : ''}</span>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
            {boundPVCs.map(pvc => (
              <div key={pvc.id} onClick={() => onSelect(pvc)} style={{
                display: 'flex', alignItems: 'flex-start', gap: 8, padding: '7px 9px',
                borderRadius: 8, background: c.card, cursor: 'pointer',
                border: `1px solid ${'#5AC8FA'}30`,
                transition: 'border-color 0.1s' }}
                onMouseEnter={e => (e.currentTarget.style.borderColor = '#5AC8FA80')}
                onMouseLeave={e => (e.currentTarget.style.borderColor = '#5AC8FA30')}>
                <Archive style={{ width: 12, height: 12, color: '#5AC8FA', flexShrink: 0, marginTop: 1 }} />
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={{ fontSize: 11, fontWeight: 700, color: c.text, wordBreak: 'break-all', lineHeight: 1.3 }}>{pvc.label}</div>
                  {pvc.data?.namespace && <div style={{ fontSize: 10, color: c.sub, marginTop: 2 }}>ns: {pvc.data.namespace}</div>}
                  <div style={{ display: 'flex', gap: 5, marginTop: 3, flexWrap: 'wrap' }}>
                    {pvc.data?.phase && (
                      <span style={{ fontSize: 9, padding: '1px 5px', borderRadius: 4, fontWeight: 600,
                        background: (pvc.data.phase === 'Bound' ? c.green : c.orange) + '20',
                        color: pvc.data.phase === 'Bound' ? c.green : c.orange }}>
                        {pvc.data.phase}
                      </span>
                    )}
                    {pvc.data?.cluster && <span style={{ fontSize: 9, color: c.sub }}>{pvc.data.cluster}</span>}
                  </div>
                </div>
                <ArrowUpRight style={{ width: 10, height: 10, color: c.sub, flexShrink: 0 }} />
              </div>
            ))}
          </div>
        </div>
      )}

      {/* k8s_pvc / k8s_pv: backing NetApp volume */}
      {(nt === 'k8s_pvc' || nt === 'k8s_pv') && backingVolume && (
        <div style={box}>
          <span style={sectionLabel}>Storage Backend</span>
          <div onClick={() => onSelect(backingVolume)} style={{
            display: 'flex', alignItems: 'flex-start', gap: 8, padding: '7px 9px',
            borderRadius: 8, background: c.card, cursor: 'pointer',
            border: `1px solid ${'#FF6B35'}30`,
            transition: 'border-color 0.1s' }}
            onMouseEnter={e => (e.currentTarget.style.borderColor = '#FF6B3580')}
            onMouseLeave={e => (e.currentTarget.style.borderColor = '#FF6B3530')}>
            <Database style={{ width: 12, height: 12, color: '#FF6B35', flexShrink: 0, marginTop: 1 }} />
            <div style={{ flex: 1, minWidth: 0 }}>
              <div style={{ fontSize: 11, fontWeight: 700, color: c.text, wordBreak: 'break-all', lineHeight: 1.3 }}>{backingVolume.label}</div>
              <div style={{ fontSize: 10, color: c.sub, marginTop: 1 }}>NetApp Volume</div>
              {backingVolume.data?.svm     && <div style={{ fontSize: 10, color: c.sub }}>SVM: <b style={{ color: c.text }}>{backingVolume.data.svm}</b></div>}
              {backingVolume.data?.cluster && <div style={{ fontSize: 10, color: c.sub }}>Cluster: <b style={{ color: c.text }}>{backingVolume.data.cluster}</b></div>}
              {Number(backingVolume.data?.space_total_gb) > 0 && (
                <div style={{ marginTop: 7 }}>
                  <CapacityBar totalGB={Number(backingVolume.data.space_total_gb)} usedGB={Number(backingVolume.data.space_used_gb ?? 0)} />
                </div>
              )}
            </div>
            <ArrowUpRight style={{ width: 10, height: 10, color: c.sub, flexShrink: 0 }} />
          </div>
        </div>
      )}

      {/* netapp_s3_bucket: backing ONTAP volume */}
      {nt === 'netapp_s3_bucket' && node.data?.backing_volume && (() => {
        const vol = Object.values(nodeByID).find(n =>
          n.node_type === 'netapp_volume' && n.label === node.data.backing_volume
        );
        if (!vol) return null;
        return (
          <div style={box}>
            <span style={sectionLabel}>Backing Volume</span>
            <div onClick={() => onSelect(vol)} style={{
              display: 'flex', alignItems: 'flex-start', gap: 8, padding: '7px 9px',
              borderRadius: 8, background: c.card, cursor: 'pointer',
              border: `1px solid ${'#FF6B35'}30`, transition: 'border-color 0.1s' }}
              onMouseEnter={e => (e.currentTarget.style.borderColor = '#FF6B3580')}
              onMouseLeave={e => (e.currentTarget.style.borderColor = '#FF6B3530')}>
              <Database style={{ width: 12, height: 12, color: '#FF6B35', flexShrink: 0, marginTop: 1 }} />
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontSize: 11, fontWeight: 700, color: c.text, wordBreak: 'break-all' }}>{vol.label}</div>
                <div style={{ fontSize: 10, color: c.sub }}>ONTAP Object Store Volume</div>
                {vol.data?.cluster && <div style={{ fontSize: 10, color: c.sub }}>Cluster: <b style={{ color: c.text }}>{vol.data.cluster}</b></div>}
              </div>
              <ArrowUpRight style={{ width: 10, height: 10, color: c.sub, flexShrink: 0 }} />
            </div>
          </div>
        );
      })()}

      {/* netapp_svm: list of S3 buckets */}
      {nt === 'netapp_svm' && (() => {
        const svmBuckets = allEdges
          .filter(e => e.source === node.id && e.edge_type === 'has_bucket')
          .map(e => nodeByID[e.target]).filter(Boolean) as GraphNode[];
        if (svmBuckets.length === 0) return null;
        return (
          <div style={box}>
            <span style={sectionLabel}>S3 Buckets ({svmBuckets.length})</span>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 4, maxHeight: 200, overflowY: 'auto' }}>
              {svmBuckets.map(b => {
                const bP = Number(b.data?.space_percent ?? 0);
                const bc = bP >= 85 ? c.red : bP >= 70 ? c.orange : c.green;
                return (
                  <div key={b.id} onClick={() => onSelect(b)} style={{
                    display: 'flex', alignItems: 'center', gap: 6, cursor: 'pointer',
                    padding: '4px 6px', borderRadius: 6, background: c.card,
                    border: '1px solid transparent', transition: 'border-color 0.1s' }}
                    onMouseEnter={e => (e.currentTarget.style.borderColor = c.sep)}
                    onMouseLeave={e => (e.currentTarget.style.borderColor = 'transparent')}>
                    <Cloud style={{ width: 10, height: 10, color: '#BF5AF2', flexShrink: 0 }} />
                    <span style={{ fontSize: 10, color: c.text, flex: 1, minWidth: 0,
                      overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {b.label}
                    </span>
                    {bP > 0 && (
                      <div style={{ width: 44, height: 3, borderRadius: 2, background: c.sep, overflow: 'hidden', flexShrink: 0 }}>
                        <div style={{ width: `${Math.min(bP, 100)}%`, height: '100%', background: bc }} />
                      </div>
                    )}
                    {bP > 0 && <span style={{ fontSize: 9, color: bc, fontWeight: 700, flexShrink: 0 }}>{bP}%</span>}
                  </div>
                );
              })}
            </div>
          </div>
        );
      })()}
    </>
  );
}

// ─── API helpers ──────────────────────────────────────────────────────────────
const AUTH = () => ({
  'Content-Type': 'application/json',
  Authorization: 'Bearer ' + (sessionStorage.getItem('access_token') || localStorage.getItem('access_token') || ''),
});

async function fetchGraph(live = false): Promise<InfraGraph> {
  const url = live ? '/api/v1/topology/graph?live=true' : '/api/v1/topology/graph';
  const r = await fetch(url, { headers: AUTH() });
  const j = await r.json();
  return j.data as InfraGraph;
}
async function fetchExpand(nodeId: string): Promise<ExpandResult> {
  const r = await fetch(`/api/v1/topology/graph/node/${encodeURIComponent(nodeId)}/expand`, { headers: AUTH() });
  const j = await r.json();
  const d = j.data as ExpandResult;
  return { ...d, children: d.children ?? [], edges: d.edges ?? [] };
}
async function fetchSearch(q: string): Promise<SearchResult[]> {
  const r = await fetch(`/api/v1/topology/graph/search?q=${encodeURIComponent(q)}`, { headers: AUTH() });
  const j = await r.json();
  const results = (j.data?.results ?? []) as SearchResult[];
  return results.map(sr => ({ ...sr, parents: sr.parents ?? [], children: sr.children ?? [] }));
}

// ─── copy-to-clipboard hook ───────────────────────────────────────────────────
function useCopy() {
  const [copied, setCopied] = useState('');
  const copy = useCallback((text: string, key: string) => {
    navigator.clipboard.writeText(text).then(() => {
      setCopied(key);
      setTimeout(() => setCopied(''), 1800);
    });
  }, []);
  return { copied, copy };
}

// ─── mini-graph SVG ───────────────────────────────────────────────────────────
const NW = 160, NH = 60, CGAP = 14, RGAP = 76;

function MiniGraph({ center, parents, children, onSelect }: {
  center: GraphNode; parents: GraphNode[]; children: GraphNode[];
  onSelect: (n: GraphNode) => void;
}) {
  const sp = parents  ?? [];
  const sc = children ?? [];
  const vis = sc.slice(0, 8);
  const hidden = sc.length - vis.length;
  const cols = Math.max(sp.length, vis.length, 1);
  const W = Math.max(cols * (NW + CGAP), NW + 40);
  const hasParents = sp.length > 0;
  const H = hasParents ? NH * 3 + RGAP * 2 + 50 : NH * 2 + RGAP + 50;
  const cx = W / 2;
  const py = hasParents ? 24 : -999;
  const my = hasParents ? py + NH + RGAP : 24;
  const cy2 = my + NH + RGAP;

  function rx(i: number, total: number) {
    const tw = total * NW + (total - 1) * CGAP;
    return cx - tw / 2 + i * (NW + CGAP);
  }

  function NRect({ node, x, y, isCenter }: { node: GraphNode; x: number; y: number; isCenter?: boolean }) {
    const cfg = NODE_CFG[node.node_type] ?? NODE_CFG.cloudstack_vm;
    const hc = HEALTH_COLOR[node.health] ?? c.gray;
    const lbl = node.label.length > 18 ? node.label.slice(0, 17) + '…' : node.label;
    return (
      <g onClick={() => onSelect(node)} style={{ cursor: 'pointer' }}>
        <rect x={x} y={y} width={NW} height={NH} rx={9}
          fill={isCenter ? cfg.color + '20' : c.fill}
          stroke={isCenter ? cfg.color : c.sep}
          strokeWidth={isCenter ? 2 : 1} />
        <rect x={x} y={y + 8} width={3} height={NH - 16} rx={1.5} fill={cfg.color} />
        <circle cx={x + NW - 13} cy={y + 13} r={4.5} fill={hc} />
        <text x={x + 13} y={y + 20} fontSize={10} fontWeight={600} fill={c.text} style={{ fontFamily: 'system-ui' }}>{lbl}</text>
        <text x={x + 13} y={y + 33} fontSize={9}  fill={c.sub}  style={{ fontFamily: 'system-ui' }}>{cfg.label}</text>
        <text x={x + 13} y={y + 48} fontSize={9}  fill={hc}     style={{ fontFamily: 'system-ui' }}>{node.health}</text>
      </g>
    );
  }

  function Curve({ x1, y1, x2, y2, col }: { x1: number; y1: number; x2: number; y2: number; col: string }) {
    const my2 = (y1 + y2) / 2;
    return <path d={`M${x1},${y1} C${x1},${my2} ${x2},${my2} ${x2},${y2}`}
      fill="none" stroke={col} strokeWidth={1.5} strokeDasharray="4 3" opacity={0.55} />;
  }

  return (
    <svg width="100%" viewBox={`0 0 ${W} ${H}`} style={{ overflow: 'visible', maxHeight: 460 }}>
      {sp.map((p, i) => {
        const px2 = rx(i, sp.length);
        return (
          <g key={p.id}>
            <Curve x1={px2 + NW / 2} y1={py + NH} x2={cx} y2={my}
              col={NODE_CFG[p.node_type]?.color ?? c.gray} />
            <NRect node={p} x={px2} y={py} />
          </g>
        );
      })}
      <NRect node={center} x={cx - NW / 2} y={my} isCenter />
      {vis.map((ch, i) => {
        const chx = rx(i, vis.length);
        return (
          <g key={ch.id}>
            <Curve x1={cx} y1={my + NH} x2={chx + NW / 2} y2={cy2}
              col={NODE_CFG[ch.node_type]?.color ?? c.gray} />
            <NRect node={ch} x={chx} y={cy2} />
          </g>
        );
      })}
      {hidden > 0 && (
        <g>
          <rect x={W - 64} y={cy2 + 6} width={56} height={24} rx={12}
            fill={c.orange + '22'} stroke={c.orange} strokeWidth={1} />
          <text x={W - 36} y={cy2 + 22} fontSize={10} fill={c.orange}
            textAnchor="middle" fontWeight={600} style={{ fontFamily: 'system-ui' }}>
            +{hidden}
          </text>
        </g>
      )}
    </svg>
  );
}

// ─── sidebar node item ────────────────────────────────────────────────────────
const NodeItem = memo(function NodeItem({ node, selected, onClick }: { node: GraphNode; selected: boolean; onClick: () => void }) {
  const [hovered, setHovered] = useState(false);
  const cfg = NODE_CFG[node.node_type] ?? NODE_CFG.cloudstack_vm;
  const hc  = HEALTH_COLOR[node.health] ?? c.gray;
  const ip  = node.data?.ip || node.data?.internal_ip || node.data?.pod_ip || '';
  const cluster = node.data?.cloudstack_cluster || node.data?.cluster || '';
  const secondary =
    ip                                                ? ip :
    node.data?.kvm_host                              ? node.data.kvm_host :
    node.data?.namespace                             ? node.data.namespace :
    node.data?.datacenter                            ? node.data.datacenter :
    node.data?.svm                                   ? `SVM: ${node.data.svm}` :
    node.data?.space_percent != null                 ? `${node.data.space_percent}% used` :
    (cluster && node.node_type !== 'k8s_cluster')    ? cluster : '';

  return (
    <div
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      onClick={onClick}
      style={{
        display: 'flex', alignItems: 'center', gap: 8, padding: '7px 10px',
        borderRadius: 8, cursor: 'pointer', marginBottom: 1,
        background: selected ? cfg.color + '16' : 'transparent',
        border: `1px solid ${selected ? cfg.color + '50' : 'transparent'}`,
        transform: hovered ? 'translateX(2px)' : 'translateX(0)',
        transition: 'background 0.12s, transform 0.1s',
      }}>
      <div style={{ width: 3, height: 34, borderRadius: 2, background: cfg.color, flexShrink: 0 }} />
      <cfg.icon style={{ width: 14, height: 14, color: cfg.color, flexShrink: 0 }} />
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ fontSize: 12, fontWeight: 600, color: c.text,
          whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}
          title={node.label}>
          {node.label}
        </div>
        {secondary && (
          <div style={{ fontSize: 10, color: c.sub, overflow: 'hidden', textOverflow: 'ellipsis',
            whiteSpace: 'nowrap', fontFamily: ip ? 'ui-monospace, monospace' : undefined }}>
            {secondary}
          </div>
        )}
      </div>
      <div style={{ width: 6, height: 6, borderRadius: '50%', background: hc, flexShrink: 0 }} />
    </div>
  );
});

// ─── compact layer chip ───────────────────────────────────────────────────────
function LayerChip({ stat, color, icon: Icon, label, selected, onClick, isStale, syncAge }: {
  stat: LayerStat | undefined; color: string; icon: React.ElementType; label: string;
  selected: boolean; onClick: () => void; isStale?: boolean; syncAge?: string;
}) {
  const total     = stat?.count     || 0;
  const healthy   = stat?.healthy   || 0;
  const degraded  = stat?.degraded  || 0;
  const unhealthy = stat?.unhealthy || 0;
  const healthPct = total > 0 ? Math.round(healthy / total * 100) : null;
  const pctColor  = unhealthy > 0 ? c.red : degraded > 0 ? c.orange : c.green;
  return (
    <motion.div whileHover={{ y: -1 }} onClick={onClick} style={{
      padding: '6px 9px 6px', borderRadius: 9, cursor: 'pointer', position: 'relative',
      background: selected ? color + '18' : c.fill,
      border: `1.5px solid ${selected ? color : c.sep}`,
      flex: '1 1 0', minWidth: 0,
      transition: 'border-color 0.15s, background 0.15s',
    }}>
      {/* Header: icon + label + health % */}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 4 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
          <Icon style={{ width: 10, height: 10, color, flexShrink: 0 }} />
          <span style={{ fontSize: 9, fontWeight: 700, color: c.sub, whiteSpace: 'nowrap', letterSpacing: '0.02em' }}>{label}</span>
        </div>
        {healthPct !== null && (
          <span style={{ fontSize: 8, fontWeight: 700, color: pctColor, marginLeft: 4 }}>{healthPct}%</span>
        )}
      </div>
      {/* Count */}
      <div style={{ fontSize: 20, fontWeight: 800, color, lineHeight: 1, marginBottom: 4, letterSpacing: '-0.5px' }}>
        {total.toLocaleString()}
      </div>
      {/* Segmented health bar */}
      {total > 0 && (
        <div style={{ height: 2, borderRadius: 1, background: c.sep, overflow: 'hidden', display: 'flex', marginBottom: 3 }}>
          {healthy   > 0 && <div style={{ flex: healthy,   background: c.green  }} />}
          {degraded  > 0 && <div style={{ flex: degraded,  background: c.orange }} />}
          {unhealthy > 0 && <div style={{ flex: unhealthy, background: c.red    }} />}
        </div>
      )}
      {/* Issue badges + sync age */}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', minHeight: 11 }}>
        <div style={{ display: 'flex', gap: 4 }}>
          {degraded  > 0 && <span style={{ fontSize: 8, color: c.orange, fontWeight: 700 }}>⚠{degraded.toLocaleString()}</span>}
          {unhealthy > 0 && <span style={{ fontSize: 8, color: c.red,    fontWeight: 700 }}>✕{unhealthy.toLocaleString()}</span>}
          {total === 0 && <span style={{ fontSize: 8, color: c.gray }}>—</span>}
        </div>
        {syncAge && (
          <span style={{ fontSize: 7, color: isStale ? c.yellow : c.gray, fontWeight: 600, letterSpacing: '0.01em' }}>
            {isStale ? '⚠' : '↻'}{syncAge}
          </span>
        )}
      </div>
    </motion.div>
  );
}

function StatsGroup({ label, labelColor, flex, children }: { label: string; labelColor?: string; flex?: number; children: React.ReactNode }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 5, flex: flex !== undefined ? flex : '0 0 auto', minWidth: 0 }}>
      <span style={{ fontSize: 8, fontWeight: 800, color: labelColor || c.sub,
        textTransform: 'uppercase', letterSpacing: '0.08em', paddingLeft: 1 }}>{label}</span>
      <div style={{ display: 'flex', gap: 4 }}>{children}</div>
    </div>
  );
}

// ─── node detail panel ────────────────────────────────────────────────────────
function NodeDetail({ node, onClose, onExpand, onFilterHealth, expandedChildren, allEdges, nodeByID, onSelectNode }: {
  node: GraphNode; onClose: () => void; onExpand: () => void;
  onFilterHealth: (layer: ActiveLayer, h: HealthFilter) => void;
  expandedChildren: GraphNode[];
  allEdges: GraphEdge[];
  nodeByID: Record<string, GraphNode>;
  onSelectNode: (n: GraphNode) => void;
}) {
  const cfg = NODE_CFG[node.node_type] ?? NODE_CFG.cloudstack_vm;
  const hc  = HEALTH_COLOR[node.health] ?? c.gray;
  const { copied, copy } = useCopy();

  const ipKeys = ['ip', 'internal_ip', 'pod_ip', 'host_ip'];
  const storageSkipKeys = new Set([
    'space_total_gb', 'space_used_gb', 'space_percent',
    'is_pvc', 'pvc_uid', 'pvc_name',
  ]);

  return (
    <motion.div initial={{ x: 40, opacity: 0 }} animate={{ x: 0, opacity: 1 }} exit={{ x: 40, opacity: 0 }}
      style={{ background: c.card, border: `1px solid ${c.sep}`, borderRadius: 14, padding: 18 }}>

      {/* Header */}
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', marginBottom: 14 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <div style={{ width: 38, height: 38, borderRadius: 10, background: cfg.color + '1A',
            display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 }}>
            <cfg.icon style={{ width: 18, height: 18, color: cfg.color }} />
          </div>
          <div>
            <div style={{ fontSize: 13, fontWeight: 700, color: c.text, wordBreak: 'break-all', lineHeight: 1.3 }}>
              {node.label}
            </div>
            <div style={{ fontSize: 10, color: cfg.color, fontWeight: 600, marginTop: 2 }}>{cfg.label}</div>
          </div>
        </div>
        <button onClick={onClose}
          style={{ background: 'none', border: 'none', cursor: 'pointer', padding: 4, color: c.sub, flexShrink: 0 }}>
          <X style={{ width: 15, height: 15 }} />
        </button>
      </div>

      {/* Health status — clickable to filter sibling nodes */}
      <button
        onClick={() => onFilterHealth(node.node_type as ActiveLayer, node.health as HealthFilter)}
        style={{
          width: '100%', display: 'flex', alignItems: 'center', gap: 7, marginBottom: 12,
          padding: '7px 10px', borderRadius: 8, cursor: 'pointer',
          background: hc + '15', border: `1px solid ${hc}40`,
        }}
      >
        <HealthIcon h={node.health} size={14} />
        <span style={{ fontSize: 12, fontWeight: 600, color: hc, textTransform: 'capitalize', flex: 1, textAlign: 'left' }}>
          {node.health || 'unknown'}
        </span>
        <span style={{ fontSize: 10, color: c.sub }}>{node.status}</span>
        <span style={{ fontSize: 9, color: hc, opacity: 0.7 }}>filter siblings →</span>
      </button>

      {/* IP addresses — copy buttons */}
      {ipKeys.filter(k => node.data?.[k]).map(k => (
        <div key={k} style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 6,
          padding: '5px 8px', borderRadius: 7, background: c.fill }}>
          <span style={{ fontSize: 10, color: c.sub, width: 58, flexShrink: 0, textTransform: 'capitalize' }}>
            {k.replace(/_/g, ' ')}
          </span>
          <span style={{ fontSize: 11, fontWeight: 600, color: c.text, flex: 1, fontFamily: 'ui-monospace, monospace' }}>
            {String(node.data[k])}
          </span>
          <button onClick={() => copy(String(node.data[k]), k)}
            style={{ background: 'none', border: 'none', cursor: 'pointer', padding: 2, color: c.gray }}>
            {copied === k
              ? <Check style={{ width: 12, height: 12, color: c.green }} />
              : <Copy style={{ width: 12, height: 12 }} />}
          </button>
        </div>
      ))}

      {/* Storage relationships + capacity (PVC↔NetApp, cluster summary, aggregate volumes) */}
      <StorageSection node={node} allEdges={allEdges} nodeByID={nodeByID} onSelect={onSelectNode} />

      {/* Other properties */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 4, marginBottom: 14 }}>
        {Object.entries(node.data || {}).map(([k, v]) => {
          if (ipKeys.includes(k)) return null;
          if (storageSkipKeys.has(k)) return null;
          if (v === null || v === undefined || v === '' || v === 0 && k !== 'restarts') return null;
          // Format ISO date strings as relative time.
          let display: string;
          if (typeof v === 'string' && /^\d{4}-\d{2}-\d{2}T/.test(v)) {
            const age = Math.round((Date.now() - new Date(v).getTime()) / 60000);
            display = age < 60 ? `${age}m ago` : age < 1440 ? `${Math.round(age / 60)}h ago` : new Date(v).toLocaleDateString();
          } else {
            display = Array.isArray(v) ? v.join(', ') : String(v);
          }
          if (display === '[object Object]' || display === 'false') return null;
          const isBool = v === true;
          return (
            <div key={k} style={{ display: 'flex', justifyContent: 'space-between', gap: 8, alignItems: 'center' }}>
              <span style={{ fontSize: 10, color: c.sub, textTransform: 'capitalize',
                whiteSpace: 'nowrap', flexShrink: 0 }}>
                {k.replace(/_/g, ' ')}
              </span>
              <span style={{ fontSize: 10, fontWeight: 500,
                color: isBool ? c.green : c.text, textAlign: 'right',
                wordBreak: 'break-all', maxWidth: '58%' }}>
                {isBool ? '✓ yes' : display.length > 36 ? display.slice(0, 34) + '…' : display}
              </span>
            </div>
          );
        })}
      </div>

      {/* Expand button */}
      <button onClick={onExpand} style={{
        width: '100%', padding: '8px 0', borderRadius: 8,
        border: `1px solid ${cfg.color}44`, background: cfg.color + '10',
        color: cfg.color, fontSize: 11, fontWeight: 600, cursor: 'pointer',
        display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 6,
      }}>
        <GitBranch style={{ width: 12, height: 12 }} />
        {expandedChildren.length > 0 ? `${expandedChildren.length} child nodes loaded` : 'Load child relationships'}
      </button>

      {/* Expanded children */}
      {expandedChildren.length > 0 && (
        <div style={{ marginTop: 12 }}>
          <div style={{ fontSize: 10, fontWeight: 600, color: c.sub, marginBottom: 6,
            textTransform: 'uppercase', letterSpacing: '0.06em' }}>
            Children ({expandedChildren.length})
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 3, maxHeight: 240, overflowY: 'auto' }}>
            {expandedChildren.map(ch => {
              const ccfg = NODE_CFG[ch.node_type] ?? NODE_CFG.cloudstack_vm;
              return (
                <div key={ch.id} style={{ display: 'flex', alignItems: 'center', gap: 6,
                  padding: '4px 7px', borderRadius: 6,
                  background: HEALTH_COLOR[ch.health] === c.red ? c.red + '10'
                    : HEALTH_COLOR[ch.health] === c.orange ? c.orange + '10'
                    : c.fill,
                  border: `1px solid ${HEALTH_COLOR[ch.health] === c.red ? c.red + '30'
                    : HEALTH_COLOR[ch.health] === c.orange ? c.orange + '30' : 'transparent'}`,
                }}>
                  <ccfg.icon style={{ width: 11, height: 11, color: ccfg.color, flexShrink: 0 }} />
                  <span style={{ fontSize: 10, color: c.text, flex: 1, minWidth: 0,
                    overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {ch.label}
                  </span>
                  <HealthIcon h={ch.health} size={10} />
                </div>
              );
            })}
          </div>
        </div>
      )}
    </motion.div>
  );
}

// ─── MAIN PAGE ────────────────────────────────────────────────────────────────
const LAYERS: { key: ActiveLayer; label: string; icon: React.ElementType; color: string }[] = [
  { key: 'all',              label: 'All',    icon: Layers,    color: c.text    },
  { key: 'bare_metal',       label: 'BM',     icon: HardDrive, color: c.gray    },
  { key: 'cloudstack_vm',    label: 'VMs',    icon: Server,    color: c.blue    },
  { key: 'k8s_cluster',      label: 'K8s',    icon: Globe,     color: c.teal    },
  { key: 'k8s_pod',          label: 'Pods',   icon: Box,       color: c.purple  },
  { key: 'k8s_pvc',          label: 'PVCs',   icon: Archive,   color: '#5AC8FA' },
  { key: 'netapp_volume',    label: 'Vols',   icon: Database,  color: '#FF6B35' },
  { key: 'netapp_s3_bucket', label: 'S3',     icon: Cloud,     color: '#BF5AF2' },
  { key: 'netapp_cluster',   label: 'NetApp', icon: Network,   color: '#007AFF' },
];

export default function IntelligentInfraTopology() {
  const [graph,            setGraph]            = useState<InfraGraph | null>(null);
  const [loading,          setLoading]          = useState(true);
  const [refreshing,       setRefreshing]       = useState(false);
  const [activeLayer,      setActiveLayer]      = useState<ActiveLayer>('all');
  const [healthFilter,     setHealthFilter]     = useState<HealthFilter>('all');
  const [clusterFilter,    setClusterFilter]    = useState('');
  const [namespaceFilter,  setNamespaceFilter]  = useState('');
  const [sidebarLimit,     setSidebarLimit]     = useState(100);
  const [selectedNode,     setSelectedNode]     = useState<GraphNode | null>(null);
  const [graphParents,     setGraphParents]     = useState<GraphNode[]>([]);
  const [graphChildren,    setGraphChildren]    = useState<GraphNode[]>([]);
  const [expandedChildren, setExpandedChildren] = useState<GraphNode[]>([]);
  const [expandedEdges,    setExpandedEdges]    = useState<GraphEdge[]>([]);
  const [searchQuery,      setSearchQuery]      = useState('');
  const [searchResults,    setSearchResults]    = useState<SearchResult[] | null>(null);
  const [searching,        setSearching]        = useState(false);
  const searchTimer = useRef<ReturnType<typeof setTimeout>>();
  const searchRef   = useRef<HTMLInputElement>(null);

  const { nodeByID, childrenOf, parentOf } = useMemo(() => {
    if (!graph) return { nodeByID: {} as Record<string, GraphNode>, childrenOf: {} as Record<string, string[]>, parentOf: {} as Record<string, string> };
    const nodeByID: Record<string, GraphNode> = {};
    for (const n of graph.nodes) nodeByID[n.id] = n;
    const childrenOf: Record<string, string[]> = {};
    const parentOf:   Record<string, string>   = {};
    for (const e of graph.edges) {
      childrenOf[e.source] = childrenOf[e.source] || [];
      childrenOf[e.source].push(e.target);
      if (!parentOf[e.target]) parentOf[e.target] = e.source;
    }
    return { nodeByID, childrenOf, parentOf };
  }, [graph]);

  const availableClusters = useMemo(() => {
    if (!graph) return [] as string[];
    let nodes = activeLayer === 'all' ? graph.nodes : graph.nodes.filter(n => n.node_type === activeLayer);
    if (healthFilter !== 'all') nodes = nodes.filter(n => n.health === healthFilter);
    const seen = new Set<string>();
    for (const n of nodes) {
      const cl = (n.data?.cluster || n.data?.cloudstack_cluster) as string | undefined;
      if (cl) seen.add(cl);
    }
    return Array.from(seen).sort();
  }, [graph, activeLayer, healthFilter]);

  const availableNamespaces = useMemo(() => {
    if (!graph) return [] as string[];
    let nodes = activeLayer === 'all' ? graph.nodes : graph.nodes.filter(n => n.node_type === activeLayer);
    if (healthFilter !== 'all') nodes = nodes.filter(n => n.health === healthFilter);
    const seen = new Set<string>();
    for (const n of nodes) {
      if (n.data?.namespace) seen.add(n.data.namespace as string);
    }
    return Array.from(seen).sort();
  }, [graph, activeLayer, healthFilter]);

  const loadGraph = useCallback(async (silent = false, live = false) => {
    if (!silent) setLoading(true); else setRefreshing(true);
    try { setGraph(await fetchGraph(live)); }
    catch (err) { console.error(err); }
    finally { setLoading(false); setRefreshing(false); }
  }, []);

  useEffect(() => { loadGraph(); }, [loadGraph]);
  useEffect(() => {
    const id = setInterval(() => loadGraph(true), 120_000);
    return () => clearInterval(id);
  }, [loadGraph]);

  // Poll every 3 s while the backend is still building the graph on first start.
  useEffect(() => {
    if (!graph?.building) return;
    const id = setInterval(() => loadGraph(true), 3_000);
    return () => clearInterval(id);
  }, [graph?.building, loadGraph]);

  // ⌘K / Ctrl+K focuses search
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') { e.preventDefault(); searchRef.current?.focus(); }
      if (e.key === 'Escape') { setSearchQuery(''); setSearchResults(null); searchRef.current?.blur(); }
    };
    document.addEventListener('keydown', h);
    return () => document.removeEventListener('keydown', h);
  }, []);

  const handleSelectNode = useCallback((node: GraphNode) => {
    setSelectedNode(node);
    setExpandedChildren([]);
    setExpandedEdges([]);
    const pid  = parentOf[node.id];
    const par  = pid ? nodeByID[pid] : null;
    const gpid = par ? parentOf[par.id] : undefined;
    const gpar = gpid ? nodeByID[gpid] : null;
    const parents: GraphNode[] = [];
    if (gpar) parents.push(gpar);
    if (par)  parents.push(par);
    setGraphParents(parents);
    setGraphChildren(
      (childrenOf[node.id] ?? []).slice(0, 24).map(id => nodeByID[id]).filter(Boolean) as GraphNode[]
    );
    // Clear search when node is selected
    setSearchQuery('');
    setSearchResults(null);
  }, [nodeByID, childrenOf, parentOf]);

  const handleExpand = useCallback(async () => {
    if (!selectedNode) return;
    const res = await fetchExpand(selectedNode.id);
    setExpandedChildren(res.children);
    setExpandedEdges(res.edges);
    setGraphChildren(res.children.slice(0, 24));
  }, [selectedNode]);

  // Layer+health combo filter from LayerCard or NodeDetail
  const handleFilterHealth = useCallback((layer: ActiveLayer, h: HealthFilter) => {
    setActiveLayer(layer);
    setHealthFilter(h);
    setClusterFilter('');
    setNamespaceFilter('');
    setSelectedNode(null);
    setSearchQuery('');
    setSearchResults(null);
  }, []);

  // Debounced search
  useEffect(() => {
    if (searchTimer.current) clearTimeout(searchTimer.current);
    if (!searchQuery.trim()) { setSearchResults(null); return; }
    setSearching(true);
    searchTimer.current = setTimeout(async () => {
      try {
        const results = await fetchSearch(searchQuery);
        setSearchResults(results);
      } catch { setSearchResults([]); }
      setSearching(false);
    }, 320);
  }, [searchQuery]);

  // Reset sidebar limit when any filter changes
  useEffect(() => { setSidebarLimit(100); }, [activeLayer, healthFilter, clusterFilter, namespaceFilter]);

  // Sidebar node list (full filtered list; pagination applied in render)
  const sidebarNodes = useMemo(() => {
    if (!graph) return [];
    let nodes = activeLayer === 'all' ? graph.nodes : graph.nodes.filter(n => n.node_type === activeLayer);
    if (healthFilter !== 'all') nodes = nodes.filter(n => n.health === healthFilter);
    if (clusterFilter) nodes = nodes.filter(n =>
      n.data?.cluster === clusterFilter || n.data?.cloudstack_cluster === clusterFilter
    );
    if (namespaceFilter) nodes = nodes.filter(n => n.data?.namespace === namespaceFilter);
    return nodes;
  }, [graph, activeLayer, healthFilter, clusterFilter, namespaceFilter]);

  // Global health counts for banner
  const globalIssues = useMemo(() => {
    if (!graph) return { degraded: 0, unhealthy: 0 };
    const ls = graph.layer_stats || {};
    const degraded  = Object.values(ls).reduce((s, v) => s + (v.degraded  || 0), 0);
    const unhealthy = Object.values(ls).reduce((s, v) => s + (v.unhealthy || 0), 0);
    return { degraded, unhealthy };
  }, [graph]);

  // ─── loading ───────────────────────────────────────────────────────────────
  if (loading && !graph) return (
    <div style={{ minHeight: '100vh', background: c.bg, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
      <div style={{ textAlign: 'center' }}>
        <motion.div animate={{ rotate: 360 }} transition={{ repeat: Infinity, duration: 1.1, ease: 'linear' }}>
          <Network style={{ width: 48, height: 48, color: c.blue }} />
        </motion.div>
        <p style={{ fontSize: 17, color: c.sub, marginTop: 20, fontWeight: 500 }}>Loading infrastructure topology…</p>
        <p style={{ fontSize: 12, color: c.gray, marginTop: 6 }}>BM → VMs → K8s clusters → nodes → pods → NetApp storage</p>
      </div>
    </div>
  );

  if (graph?.building) return (
    <div style={{ minHeight: '100vh', background: c.bg, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
      <div style={{ textAlign: 'center', maxWidth: 420 }}>
        <motion.div animate={{ rotate: 360 }} transition={{ repeat: Infinity, duration: 1.4, ease: 'linear' }}>
          <Network style={{ width: 48, height: 48, color: c.blue }} />
        </motion.div>
        <p style={{ fontSize: 17, color: c.sub, marginTop: 20, fontWeight: 500 }}>Building topology graph…</p>
        <p style={{ fontSize: 12, color: c.gray, marginTop: 6, lineHeight: 1.6 }}>
          Discovering CloudStack hosts, VMs, Kubernetes clusters and NetApp storage.<br />
          This typically takes 30–90 s on the first load after a restart.
        </p>
        <div style={{ marginTop: 18, padding: '8px 16px', borderRadius: 9,
          background: c.fill, border: `1px solid ${c.sep}`, display: 'inline-block' }}>
          <span style={{ fontSize: 11, color: c.gray }}>Auto-refreshing every 3 s…</span>
        </div>
      </div>
    </div>
  );

  const ls = graph?.layer_stats || {};

  return (
    <div style={{ height: '100vh', background: c.bg, display: 'flex', flexDirection: 'column',
      fontFamily: '-aileron-system, BlinkMacSystemFont, "SF Pro Text", sans-serif', overflow: 'hidden' }}>

      {/* ── PAGE HEADER ──────────────────────────────────────────────────────── */}
      <div style={{ padding: '12px 20px', borderBottom: `1px solid ${c.sep}`,
        background: c.card, display: 'flex', alignItems: 'center', gap: 14, flexShrink: 0,
        backdropFilter: 'blur(12px)', WebkitBackdropFilter: 'blur(12px)' }}>

        {/* Title */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexShrink: 0 }}>
          <div style={{ width: 36, height: 36, borderRadius: 9, background: c.blue + '18',
            display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
            <Network style={{ width: 18, height: 18, color: c.blue }} />
          </div>
          <div>
            <div style={{ fontSize: 17, fontWeight: 700, color: c.text, lineHeight: 1 }}>Infra Topology</div>
            <div style={{ fontSize: 11, color: c.sub, marginTop: 2, display: 'flex', gap: 6, alignItems: 'center' }}>
              <span>{(graph?.stats.total_nodes || 0).toLocaleString()} nodes</span>
              <span style={{ color: c.sep }}>·</span>
              <span>{(graph?.stats.total_edges || 0).toLocaleString()} edges</span>
              {graph && (
                <>
                  <span style={{ color: c.sep }}>·</span>
                  <Clock style={{ width: 9, height: 9 }} />
                  <span style={{ color: graph.is_data_stale ? c.yellow : undefined }}>
                    {graph.is_data_stale ? 'cached' : `${Math.round((graph.cache_age_seconds || 0) / 60)}m ago`}
                  </span>
                  {graph.is_data_stale && graph.cs_last_sync && (
                    <>
                      <span style={{ color: c.sep }}>·</span>
                      <span style={{ color: c.yellow, fontWeight: 600 }}>
                        CS: {(() => {
                          const age = Math.round((Date.now() - new Date(graph.cs_last_sync).getTime()) / 60000);
                          return age < 60 ? `${age}m` : `${Math.round(age / 60)}h`;
                        })()} stale
                      </span>
                    </>
                  )}
                </>
              )}
            </div>
          </div>
        </div>

        {/* Search bar */}
        <div style={{ flex: 1, maxWidth: 520, position: 'relative' }}>
          <Search style={{ position: 'absolute', left: 11, top: '50%', transform: 'translateY(-50%)',
            width: 14, height: 14, color: searching ? c.blue : c.gray, pointerEvents: 'none',
            transition: 'color 0.2s' }} />
          <input
            ref={searchRef}
            value={searchQuery}
            onChange={e => setSearchQuery(e.target.value)}
            placeholder="Search by IP, hostname, pod name, namespace, cluster… (⌘K)"
            style={{
              width: '100%', padding: '8px 34px 8px 33px',
              borderRadius: 9, border: `1.5px solid ${searchQuery ? c.blue + '60' : c.sep}`,
              background: c.fill, color: c.text, fontSize: 12, outline: 'none',
              boxSizing: 'border-box', transition: 'border-color 0.2s',
            }}
          />
          {searching && (
            <motion.div animate={{ rotate: 360 }} transition={{ repeat: Infinity, duration: 0.7, ease: 'linear' }}
              style={{ position: 'absolute', right: 9, top: '50%', transform: 'translateY(-50%)' }}>
              <RefreshCw style={{ width: 12, height: 12, color: c.blue }} />
            </motion.div>
          )}
          {searchQuery && !searching && (
            <button onClick={() => { setSearchQuery(''); setSearchResults(null); }}
              style={{ position: 'absolute', right: 7, top: '50%', transform: 'translateY(-50%)',
                background: 'none', border: 'none', cursor: 'pointer', color: c.gray, padding: 3 }}>
              <X style={{ width: 12, height: 12 }} />
            </button>
          )}
        </div>

        {/* Active filter pill */}
        {(activeLayer !== 'all' || healthFilter !== 'all' || clusterFilter || namespaceFilter) && (
          <div style={{ display: 'flex', alignItems: 'center', gap: 5, padding: '5px 10px',
            borderRadius: 20, background: c.blue + '15', border: `1px solid ${c.blue}40`,
            fontSize: 11, color: c.blue, fontWeight: 600, flexShrink: 0 }}>
            <Filter style={{ width: 11, height: 11 }} />
            {activeLayer !== 'all' && NODE_CFG[activeLayer]?.label}
            {healthFilter !== 'all' && (activeLayer !== 'all' ? ' · ' : '') + healthFilter}
            {clusterFilter && ` · ${clusterFilter}`}
            {namespaceFilter && ` · ns:${namespaceFilter}`}
            <button onClick={() => { setActiveLayer('all'); setHealthFilter('all'); setClusterFilter(''); setNamespaceFilter(''); }}
              style={{ background: 'none', border: 'none', cursor: 'pointer', color: c.blue, padding: 0, marginLeft: 2 }}>
              <X style={{ width: 10, height: 10 }} />
            </button>
          </div>
        )}

        <button onClick={() => loadGraph(true, true)} disabled={refreshing}
          style={{ display: 'flex', alignItems: 'center', gap: 5, padding: '7px 12px', flexShrink: 0,
            borderRadius: 8, border: `1px solid ${c.blue}40`, background: c.blue + '15',
            color: c.blue, fontSize: 12, fontWeight: 600, cursor: refreshing ? 'not-allowed' : 'pointer' }}>
          <motion.div animate={refreshing ? { rotate: 360 } : {}}
            transition={refreshing ? { repeat: Infinity, duration: 0.8, ease: 'linear' } : {}}>
            <RefreshCw style={{ width: 13, height: 13 }} />
          </motion.div>
          Refresh
        </button>
      </div>

      {/* ── GLOBAL HEALTH BANNER ─────────────────────────────────────────────── */}
      <AnimatePresence>
        {(globalIssues.unhealthy > 0 || globalIssues.degraded > 0) && (
          <motion.div initial={{ height: 0, opacity: 0 }} animate={{ height: 'auto', opacity: 1 }}
            exit={{ height: 0, opacity: 0 }}
            style={{ borderBottom: `1px solid ${globalIssues.unhealthy > 0 ? c.red + '50' : c.orange + '50'}`,
              background: globalIssues.unhealthy > 0 ? c.red + '0A' : c.orange + '0A',
              flexShrink: 0, overflow: 'hidden' }}>
            <div style={{ padding: '6px 20px', display: 'flex', alignItems: 'center', gap: 12 }}>
              <AlertCircle style={{ width: 14, height: 14,
                color: globalIssues.unhealthy > 0 ? c.red : c.orange, flexShrink: 0 }} />
              <span style={{ fontSize: 12, color: c.text, fontWeight: 500 }}>
                Infrastructure alert:&nbsp;
                {globalIssues.unhealthy > 0 && (
                  <span style={{ color: c.red, fontWeight: 700 }}>{globalIssues.unhealthy} down</span>
                )}
                {globalIssues.unhealthy > 0 && globalIssues.degraded > 0 && <span style={{ color: c.sub }}> · </span>}
                {globalIssues.degraded > 0 && (
                  <span style={{ color: c.orange, fontWeight: 700 }}>{globalIssues.degraded} degraded</span>
                )}
              </span>
              <div style={{ display: 'flex', gap: 6, marginLeft: 4 }}>
                {globalIssues.unhealthy > 0 && (
                  <button onClick={() => handleFilterHealth('all', 'unhealthy')}
                    style={{ fontSize: 10, padding: '2px 8px', borderRadius: 5, cursor: 'pointer',
                      border: `1px solid ${c.red}50`, background: c.red + '15', color: c.red, fontWeight: 600 }}>
                    Show down nodes
                  </button>
                )}
                {globalIssues.degraded > 0 && (
                  <button onClick={() => handleFilterHealth('all', 'degraded')}
                    style={{ fontSize: 10, padding: '2px 8px', borderRadius: 5, cursor: 'pointer',
                      border: `1px solid ${c.orange}50`, background: c.orange + '15', color: c.orange, fontWeight: 600 }}>
                    Show degraded
                  </button>
                )}
              </div>
            </div>
          </motion.div>
        )}
      </AnimatePresence>

      {/* ── STALE DATA BANNER ────────────────────────────────────────────────── */}
      <AnimatePresence>
        {graph?.is_data_stale && (
          <motion.div initial={{ height: 0, opacity: 0 }} animate={{ height: 'auto', opacity: 1 }}
            exit={{ height: 0, opacity: 0 }}
            style={{ borderBottom: `1px solid ${c.yellow}50`, background: c.yellow + '12',
              flexShrink: 0, overflow: 'hidden' }}>
            <div style={{ padding: '6px 20px', display: 'flex', alignItems: 'center', gap: 10 }}>
              <AlertTriangle style={{ width: 13, height: 13, color: c.yellow, flexShrink: 0 }} />
              <span style={{ fontSize: 12, color: c.text, fontWeight: 500 }}>
                {graph.stale_reason || 'CloudStack data is from cache — live API unreachable'}
                {graph.cs_last_sync && (
                  <span style={{ color: c.sub }}>
                    &nbsp;· Last live sync:&nbsp;
                    <span style={{ color: c.yellow, fontWeight: 600 }}>
                      {(() => {
                        const age = Math.round((Date.now() - new Date(graph.cs_last_sync).getTime()) / 60000);
                        return age < 60 ? `${age}m ago` : `${Math.round(age / 60)}h ago`;
                      })()}
                    </span>
                  </span>
                )}
              </span>
              <button onClick={() => loadGraph(true, true)} disabled={refreshing}
                style={{ marginLeft: 'auto', fontSize: 11, padding: '3px 10px', borderRadius: 6,
                  cursor: refreshing ? 'not-allowed' : 'pointer', border: `1px solid ${c.yellow}60`,
                  background: c.yellow + '20', color: c.yellow, fontWeight: 600, flexShrink: 0 }}>
                Retry live
              </button>
            </div>
          </motion.div>
        )}
      </AnimatePresence>

      {/* ── LAYER SUMMARY CHIPS ──────────────────────────────────────────────── */}
      {(() => {
        const cacheAge = graph?.cache_age_seconds;
        const cacheAgeStr = cacheAge != null
          ? (cacheAge < 60 ? `${cacheAge}s` : `${Math.round(cacheAge / 60)}m`)
          : undefined;
        const csStale = graph?.is_data_stale;
        const csLastSync = graph?.cs_last_sync;
        const csAgeStr = csLastSync
          ? (() => { const a = Math.round((Date.now() - new Date(csLastSync).getTime()) / 60000); return a < 60 ? `${a}m` : `${Math.round(a / 60)}h`; })()
          : cacheAgeStr;
        return (
          <div style={{ display: 'flex', gap: 6, padding: '8px 20px',
            borderBottom: `1px solid ${c.sep}`, flexShrink: 0, alignItems: 'flex-end' }}>

            <StatsGroup label="Compute" labelColor={c.gray} flex={2}>
              <LayerChip stat={ls.bare_metal}    color={c.gray}  icon={HardDrive} label="Bare Metal"
                selected={activeLayer === 'bare_metal'}    isStale={csStale} syncAge={csAgeStr}
                onClick={() => { setActiveLayer(p => p === 'bare_metal'    ? 'all' : 'bare_metal');    setHealthFilter('all'); }} />
              <LayerChip stat={ls.cloudstack_vm} color={c.blue}  icon={Server}    label="VMs"
                selected={activeLayer === 'cloudstack_vm'} isStale={csStale} syncAge={csAgeStr}
                onClick={() => { setActiveLayer(p => p === 'cloudstack_vm' ? 'all' : 'cloudstack_vm'); setHealthFilter('all'); }} />
            </StatsGroup>

            <div style={{ width: 1, height: 44, background: c.sep, flexShrink: 0 }} />

            <StatsGroup label="Kubernetes" labelColor={c.teal} flex={5}>
              <LayerChip stat={ls.k8s_cluster} color={c.teal}   icon={Globe}    label="Clusters"
                selected={activeLayer === 'k8s_cluster'} syncAge={cacheAgeStr}
                onClick={() => { setActiveLayer(p => p === 'k8s_cluster' ? 'all' : 'k8s_cluster'); setHealthFilter('all'); }} />
              <LayerChip stat={ls.k8s_node}    color={c.indigo} icon={Cpu}      label="Nodes"
                selected={activeLayer === 'k8s_node'}    syncAge={cacheAgeStr}
                onClick={() => { setActiveLayer(p => p === 'k8s_node'    ? 'all' : 'k8s_node');    setHealthFilter('all'); }} />
              <LayerChip stat={ls.k8s_pod}     color={c.purple} icon={Box}      label="Pods"
                selected={activeLayer === 'k8s_pod'}     syncAge={cacheAgeStr}
                onClick={() => { setActiveLayer(p => p === 'k8s_pod'     ? 'all' : 'k8s_pod');     setHealthFilter('all'); }} />
              <LayerChip stat={ls.k8s_pvc}     color="#5AC8FA"  icon={Archive}  label="PVCs"
                selected={activeLayer === 'k8s_pvc'}     syncAge={cacheAgeStr}
                onClick={() => { setActiveLayer(p => p === 'k8s_pvc'     ? 'all' : 'k8s_pvc');     setHealthFilter('all'); }} />
              <LayerChip stat={ls.k8s_pv}      color="#5AC8FA"  icon={Archive}  label="PVs"
                selected={activeLayer === 'k8s_pv'}      syncAge={cacheAgeStr}
                onClick={() => { setActiveLayer(p => p === 'k8s_pv'      ? 'all' : 'k8s_pv');      setHealthFilter('all'); }} />
            </StatsGroup>

            <div style={{ width: 1, height: 44, background: c.sep, flexShrink: 0 }} />

            <StatsGroup label="NetApp Storage" labelColor="#007AFF" flex={6}>
              <LayerChip stat={ls.netapp_cluster}   color="#007AFF" icon={Network}   label="Clusters"
                selected={activeLayer === 'netapp_cluster'}   syncAge={cacheAgeStr}
                onClick={() => { setActiveLayer(p => p === 'netapp_cluster'   ? 'all' : 'netapp_cluster');   setHealthFilter('all'); }} />
              <LayerChip stat={ls.netapp_node}      color="#30D158" icon={Server}    label="Controllers"
                selected={activeLayer === 'netapp_node'}      syncAge={cacheAgeStr}
                onClick={() => { setActiveLayer(p => p === 'netapp_node'      ? 'all' : 'netapp_node');      setHealthFilter('all'); }} />
              <LayerChip stat={ls.netapp_aggregate} color="#FF9F0A" icon={Layers}    label="Aggregates"
                selected={activeLayer === 'netapp_aggregate'} syncAge={cacheAgeStr}
                onClick={() => { setActiveLayer(p => p === 'netapp_aggregate' ? 'all' : 'netapp_aggregate'); setHealthFilter('all'); }} />
              <LayerChip stat={ls.netapp_svm}       color="#32ADE6" icon={GitBranch} label="SVMs"
                selected={activeLayer === 'netapp_svm'}       syncAge={cacheAgeStr}
                onClick={() => { setActiveLayer(p => p === 'netapp_svm'       ? 'all' : 'netapp_svm');       setHealthFilter('all'); }} />
              <LayerChip stat={ls.netapp_volume}    color="#FF6B35" icon={Database}  label="Volumes"
                selected={activeLayer === 'netapp_volume'}    syncAge={cacheAgeStr}
                onClick={() => { setActiveLayer(p => p === 'netapp_volume'    ? 'all' : 'netapp_volume');    setHealthFilter('all'); }} />
              <LayerChip stat={ls.netapp_s3_bucket} color="#BF5AF2" icon={Cloud}     label="S3 Buckets"
                selected={activeLayer === 'netapp_s3_bucket'} syncAge={cacheAgeStr}
                onClick={() => { setActiveLayer(p => p === 'netapp_s3_bucket' ? 'all' : 'netapp_s3_bucket'); setHealthFilter('all'); }} />
            </StatsGroup>
          </div>
        );
      })()}

      {/* ── DATA SOURCES BAR ─────────────────────────────────────────────────── */}
      {graph?.sources && graph.sources.length > 0 && (
        <div style={{ display: 'flex', gap: 5, padding: '5px 20px',
          borderBottom: `1px solid ${c.sep}`, overflowX: 'auto', flexShrink: 0,
          alignItems: 'center', background: c.fill }}>
          <span style={{ fontSize: 9, color: c.sub, fontWeight: 700, letterSpacing: '0.07em',
            flexShrink: 0, textTransform: 'uppercase' }}>Sources</span>
          <span style={{ width: 1, height: 10, background: c.sep, flexShrink: 0 }} />
          {graph.sources.map(src => {
            const age = Math.round((Date.now() - new Date(src.last_sync).getTime()) / 60000);
            const ageStr = age < 60 ? `${age}m` : `${Math.round(age / 60)}h`;
            const col = src.is_stale ? c.yellow : age > 10 ? c.orange : c.green;
            const SrcIcon = src.type === 'k8s' ? Globe : Server;
            return (
              <div key={src.name} title={`${src.name} · ${src.node_count} nodes · synced ${ageStr} ago`}
                style={{ display: 'flex', alignItems: 'center', gap: 4, padding: '2px 8px',
                  borderRadius: 20, background: col + '12', border: `1px solid ${col}30`,
                  fontSize: 10, color: c.text, flexShrink: 0, whiteSpace: 'nowrap', cursor: 'default' }}>
                <div style={{ width: 5, height: 5, borderRadius: '50%', background: col, flexShrink: 0 }} />
                <SrcIcon style={{ width: 9, height: 9, color: col, flexShrink: 0 }} />
                <span style={{ fontWeight: 600 }}>{src.name}</span>
                <span style={{ color: col, fontWeight: 600 }}>{ageStr}</span>
                <span style={{ color: c.sub }}>· {src.node_count.toLocaleString()}</span>
              </div>
            );
          })}
        </div>
      )}

      {/* ── 3-PANEL BODY ─────────────────────────────────────────────────────── */}
      <div style={{ flex: 1, display: 'flex', overflow: 'hidden', minHeight: 0 }}>

        {/* LEFT ── node list ─────────────────────────────────────────────────── */}
        <div style={{ width: 270, borderRight: `1px solid ${c.sep}`, display: 'flex',
          flexDirection: 'column', overflow: 'hidden', flexShrink: 0 }}>

          {/* Layer type tabs */}
          <div style={{ display: 'flex', borderBottom: `1px solid ${c.sep}`, flexShrink: 0 }}>
            {LAYERS.map(l => (
              <button key={l.key} onClick={() => { setActiveLayer(l.key); setHealthFilter('all'); setClusterFilter(''); setNamespaceFilter(''); setSearchQuery(''); setSearchResults(null); }}
                style={{
                  flex: '1 1 0', padding: '7px 2px', border: 'none', cursor: 'pointer',
                  background: 'none',
                  borderBottom: `2px solid ${activeLayer === l.key ? l.color : 'transparent'}`,
                  color: activeLayer === l.key ? l.color : c.gray,
                  display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 2,
                }}>
                <l.icon style={{ width: 12, height: 12 }} />
                <span style={{ fontSize: 8, fontWeight: 600, whiteSpace: 'nowrap' }}>{l.label}</span>
              </button>
            ))}
          </div>

          {/* Health filter chips */}
          <div style={{ display: 'flex', gap: 4, padding: '6px 8px',
            borderBottom: `1px solid ${c.sep}`, flexShrink: 0 }}>
            {(['all', 'healthy', 'degraded', 'unhealthy'] as HealthFilter[]).map(h => {
              const col = h === 'all' ? c.blue : HEALTH_COLOR[h];
              const icon = h === 'healthy' ? '✓' : h === 'degraded' ? '⚠' : h === 'unhealthy' ? '✕' : '◎';
              return (
                <button key={h} onClick={() => setHealthFilter(h)}
                  style={{
                    flex: '1 1 0', padding: '3px 0', fontSize: 9, fontWeight: 600,
                    borderRadius: 5, cursor: 'pointer',
                    background: healthFilter === h ? col + '20' : 'transparent',
                    border: `1px solid ${healthFilter === h ? col + '60' : 'transparent'}`,
                    color: healthFilter === h ? col : c.gray,
                  }}>
                  {icon} {h === 'all' ? 'All' : h.charAt(0).toUpperCase() + h.slice(1)}
                </button>
              );
            })}
          </div>

          {/* Cluster / namespace filters */}
          {(availableClusters.length > 0 || availableNamespaces.length > 0) && (
            <div style={{ display: 'flex', gap: 4, padding: '5px 8px',
              borderBottom: `1px solid ${c.sep}`, flexShrink: 0 }}>
              {availableClusters.length > 0 && (
                <select
                  value={clusterFilter}
                  onChange={e => setClusterFilter(e.target.value)}
                  style={{
                    flex: 1, fontSize: 10, padding: '3px 5px', borderRadius: 5,
                    border: `1px solid ${clusterFilter ? c.blue + '60' : c.sep}`,
                    background: clusterFilter ? c.blue + '10' : c.fill,
                    color: c.text, outline: 'none', cursor: 'pointer',
                  }}>
                  <option value="">All clusters</option>
                  {availableClusters.map(cl => <option key={cl} value={cl}>{cl}</option>)}
                </select>
              )}
              {availableNamespaces.length > 0 && (
                <select
                  value={namespaceFilter}
                  onChange={e => setNamespaceFilter(e.target.value)}
                  style={{
                    flex: 1, fontSize: 10, padding: '3px 5px', borderRadius: 5,
                    border: `1px solid ${namespaceFilter ? c.purple + '60' : c.sep}`,
                    background: namespaceFilter ? c.purple + '10' : c.fill,
                    color: c.text, outline: 'none', cursor: 'pointer',
                  }}>
                  <option value="">All namespaces</option>
                  {availableNamespaces.map(ns => <option key={ns} value={ns}>{ns}</option>)}
                </select>
              )}
              {(clusterFilter || namespaceFilter) && (
                <button onClick={() => { setClusterFilter(''); setNamespaceFilter(''); }}
                  style={{ padding: '2px 6px', borderRadius: 5, cursor: 'pointer', fontSize: 9,
                    border: `1px solid ${c.sep}`, background: 'none', color: c.gray }}>
                  ✕
                </button>
              )}
            </div>
          )}

          {/* List header */}
          <div style={{ padding: '5px 10px 3px', flexShrink: 0,
            display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <span style={{ fontSize: 10, color: c.sub }}>
              {searchResults !== null
                ? `${searchResults.length} result${searchResults.length !== 1 ? 's' : ''} for "${searchQuery}"`
                : `${sidebarNodes.length.toLocaleString()} node${sidebarNodes.length !== 1 ? 's' : ''}`}
            </span>
            {(searchResults !== null || healthFilter !== 'all' || clusterFilter || namespaceFilter) && (
              <button onClick={() => { setSearchQuery(''); setSearchResults(null); setHealthFilter('all'); setClusterFilter(''); setNamespaceFilter(''); }}
                style={{ background: 'none', border: 'none', cursor: 'pointer', color: c.gray, fontSize: 10 }}>
                clear
              </button>
            )}
          </div>

          {/* ── Scrollable node list ───────────────────────────────────────────── */}
          <div style={{ flex: 1, overflowY: 'auto', padding: '4px 6px' }}>

            {/* SEARCH MODE */}
            {searchResults !== null && (
              <>
                {searchResults.length === 0 && (
                  <div style={{ padding: '28px 16px', textAlign: 'center', color: c.gray, fontSize: 12 }}>
                    No matches.<br />
                    <span style={{ fontSize: 10, lineHeight: 1.8 }}>
                      Try: IP address · hostname · pod name · namespace · cluster name
                    </span>
                  </div>
                )}
                {searchResults.map(sr => (
                  <div key={sr.node.id} style={{
                    marginBottom: 4, borderRadius: 8, overflow: 'hidden',
                    border: `1px solid ${c.sep}`, background: c.fill,
                  }}>
                    {/* Breadcrumb parents */}
                    {sr.parents.length > 0 && (
                      <div style={{ padding: '3px 8px 0', display: 'flex', gap: 3,
                        alignItems: 'center', flexWrap: 'wrap' }}>
                        {sr.parents.map((p, i) => (
                          <React.Fragment key={p.id}>
                            <button onClick={() => handleSelectNode(p)}
                              style={{ background: 'none', border: 'none', cursor: 'pointer', padding: 0,
                                fontSize: 9, color: NODE_CFG[p.node_type]?.color ?? c.gray, fontWeight: 600 }}>
                              {p.label}
                            </button>
                            {i < sr.parents.length - 1 && <ChevronRight style={{ width: 7, height: 7, color: c.gray }} />}
                          </React.Fragment>
                        ))}
                        <ChevronRight style={{ width: 7, height: 7, color: c.gray }} />
                      </div>
                    )}
                    {/* Main match */}
                    <NodeItem node={sr.node} selected={selectedNode?.id === sr.node.id}
                      onClick={() => handleSelectNode(sr.node)} />
                    {/* Child tags */}
                    {sr.children.length > 0 && (
                      <div style={{ padding: '0 8px 5px', display: 'flex', gap: 3, flexWrap: 'wrap' }}>
                        {sr.children.slice(0, 4).map(ch => {
                          const ccfg = NODE_CFG[ch.node_type] ?? NODE_CFG.cloudstack_vm;
                          return (
                            <button key={ch.id} onClick={() => handleSelectNode(ch)}
                              style={{ fontSize: 8, padding: '1px 5px', borderRadius: 3, cursor: 'pointer',
                                background: ccfg.color + '15', color: ccfg.color, fontWeight: 600,
                                border: `1px solid ${ccfg.color}30` }}>
                              {ch.label}
                            </button>
                          );
                        })}
                        {sr.children.length > 4 && (
                          <span style={{ fontSize: 8, color: c.gray, alignSelf: 'center' }}>
                            +{sr.children.length - 4}
                          </span>
                        )}
                      </div>
                    )}
                  </div>
                ))}
              </>
            )}

            {/* BROWSE MODE */}
            {searchResults === null && (
              <>
                {sidebarNodes.length === 0 ? (
                  <div style={{ padding: '28px 16px', textAlign: 'center', color: c.gray, fontSize: 12 }}>
                    No nodes match the current filter
                  </div>
                ) : (
                  sidebarNodes.slice(0, sidebarLimit).map(n => (
                    <NodeItem key={n.id} node={n}
                      selected={selectedNode?.id === n.id}
                      onClick={() => handleSelectNode(n)} />
                  ))
                )}
                {sidebarNodes.length > sidebarLimit && (
                  <button onClick={() => setSidebarLimit(l => l + 100)}
                    style={{
                      width: '100%', marginTop: 4, padding: '6px 0', borderRadius: 7,
                      border: `1px solid ${c.sep}`, background: c.fill,
                      color: c.sub, fontSize: 10, fontWeight: 600, cursor: 'pointer',
                    }}>
                    Show {Math.min(100, sidebarNodes.length - sidebarLimit)} more
                    <span style={{ color: c.gray, fontWeight: 400 }}> · {sidebarNodes.length - sidebarLimit} remaining</span>
                  </button>
                )}
              </>
            )}
          </div>
        </div>

        {/* CENTER ── relationship graph ─────────────────────────────────────── */}
        <div style={{ flex: 1, overflow: 'auto', padding: 20, display: 'flex',
          flexDirection: 'column', gap: 18, minWidth: 0 }}>

          {!selectedNode ? (
            <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center',
              justifyContent: 'center', flex: 1, gap: 22 }}>
              <motion.div initial={{ scale: 0.9, opacity: 0 }} animate={{ scale: 1, opacity: 1 }}
                style={{ textAlign: 'center', maxWidth: 480 }}>
                <div style={{ width: 72, height: 72, borderRadius: 18, background: c.blue + '15',
                  display: 'flex', alignItems: 'center', justifyContent: 'center', margin: '0 auto 18px' }}>
                  <Network style={{ width: 36, height: 36, color: c.blue }} />
                </div>
                <h2 style={{ fontSize: 20, fontWeight: 700, color: c.text, marginBottom: 8, marginTop: 0 }}>
                  Select a node to explore
                </h2>
                <p style={{ fontSize: 13, color: c.sub, lineHeight: 1.6, marginBottom: 20 }}>
                  Click any node to visualise its full relationship chain — bare metal → VMs → K8s nodes → pods.
                  Search by IP, hostname, namespace, or any field.
                </p>

                {/* Quick-launch buttons */}
                <div style={{ display: 'flex', gap: 10, justifyContent: 'center', flexWrap: 'wrap', marginBottom: 24 }}>
                  {(['bare_metal', 'k8s_cluster', 'cloudstack_vm'] as const).map(type => {
                    const cfg   = NODE_CFG[type];
                    const first = graph?.nodes.find(n => n.node_type === type);
                    if (!first) return null;
                    return (
                      <motion.div key={type} whileHover={{ y: -3 }} onClick={() => handleSelectNode(first)}
                        style={{ display: 'flex', alignItems: 'center', gap: 7, padding: '9px 13px',
                          borderRadius: 10, border: `1px solid ${cfg.color}40`, cursor: 'pointer',
                          background: cfg.color + '0C' }}>
                        <cfg.icon style={{ width: 14, height: 14, color: cfg.color }} />
                        <span style={{ fontSize: 11, fontWeight: 600, color: cfg.color }}>Explore {cfg.label}</span>
                        <ArrowUpRight style={{ width: 11, height: 11, color: cfg.color }} />
                      </motion.div>
                    );
                  })}
                  {/* Show degraded if any */}
                  {globalIssues.degraded + globalIssues.unhealthy > 0 && (
                    <motion.div whileHover={{ y: -3 }}
                      onClick={() => handleFilterHealth('all', globalIssues.unhealthy > 0 ? 'unhealthy' : 'degraded')}
                      style={{ display: 'flex', alignItems: 'center', gap: 7, padding: '9px 13px',
                        borderRadius: 10, border: `1px solid ${globalIssues.unhealthy > 0 ? c.red : c.orange}50`,
                        cursor: 'pointer',
                        background: (globalIssues.unhealthy > 0 ? c.red : c.orange) + '0C' }}>
                      <AlertTriangle style={{ width: 14, height: 14,
                        color: globalIssues.unhealthy > 0 ? c.red : c.orange }} />
                      <span style={{ fontSize: 11, fontWeight: 600,
                        color: globalIssues.unhealthy > 0 ? c.red : c.orange }}>
                        {globalIssues.unhealthy > 0
                          ? `${globalIssues.unhealthy} down`
                          : `${globalIssues.degraded} degraded`}
                      </span>
                    </motion.div>
                  )}
                </div>

                {/* Stats mini grid */}
                <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 8, maxWidth: 520 }}>
                  {[
                    { label: 'Nodes',        v: graph?.stats.total_nodes,   col: c.blue,   icon: Layers  },
                    { label: 'Edges',         v: graph?.stats.total_edges,   col: c.teal,   icon: GitBranch },
                    { label: 'Pods healthy',  v: ls.k8s_pod?.healthy,        col: c.green,  icon: Zap     },
                    { label: 'Issues',        v: globalIssues.degraded + globalIssues.unhealthy, col: globalIssues.unhealthy > 0 ? c.red : c.orange, icon: AlertTriangle },
                  ].map(s => (
                    <div key={s.label} style={{ padding: '10px 12px', borderRadius: 10,
                      background: s.col + '0D', border: `1px solid ${s.col}28`, textAlign: 'left' }}>
                      <s.icon style={{ width: 13, height: 13, color: s.col, marginBottom: 5 }} />
                      <div style={{ fontSize: 18, fontWeight: 700, color: s.col, lineHeight: 1 }}>
                        {(s.v || 0).toLocaleString()}
                      </div>
                      <div style={{ fontSize: 9, color: c.sub, marginTop: 3 }}>{s.label}</div>
                    </div>
                  ))}
                </div>
              </motion.div>
            </div>
          ) : (
            <AnimatePresence mode="wait">
              <motion.div key={selectedNode.id} initial={{ opacity: 0, y: 8 }}
                animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0, y: -8 }}
                style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>

                {/* Breadcrumb */}
                <div style={{ display: 'flex', alignItems: 'center', gap: 5, fontSize: 12, flexWrap: 'wrap' }}>
                  <button onClick={() => setSelectedNode(null)}
                    style={{ background: 'none', border: 'none', cursor: 'pointer',
                      fontSize: 11, color: c.blue, padding: 0, fontWeight: 500 }}>
                    All
                  </button>
                  {graphParents.map(p => (
                    <React.Fragment key={p.id}>
                      <ChevronRight style={{ width: 10, height: 10, color: c.gray }} />
                      <button onClick={() => handleSelectNode(p)}
                        style={{ background: 'none', border: 'none', cursor: 'pointer', padding: 0,
                          fontSize: 11, color: NODE_CFG[p.node_type]?.color ?? c.gray, fontWeight: 500 }}>
                        {p.label}
                      </button>
                    </React.Fragment>
                  ))}
                  <ChevronRight style={{ width: 10, height: 10, color: c.gray }} />
                  <span style={{ fontSize: 12, fontWeight: 700, color: c.text }}>{selectedNode.label}</span>
                  {graphChildren.length > 0 && (
                    <>
                      <ChevronRight style={{ width: 10, height: 10, color: c.gray }} />
                      <span style={{ fontSize: 11, color: c.gray }}>{graphChildren.length} children</span>
                    </>
                  )}
                  {/* Health badge */}
                  <span style={{ marginLeft: 6, display: 'inline-flex', alignItems: 'center', gap: 4,
                    padding: '2px 7px', borderRadius: 10, fontSize: 10, fontWeight: 600,
                    background: (HEALTH_COLOR[selectedNode.health] ?? c.gray) + '18',
                    color: HEALTH_COLOR[selectedNode.health] ?? c.gray }}>
                    <HealthIcon h={selectedNode.health} size={10} />
                    {selectedNode.health}
                  </span>
                </div>

                {/* SVG mini-graph */}
                <div style={{ background: c.card, border: `1px solid ${c.sep}`,
                  borderRadius: 14, padding: 18, overflow: 'auto' }}>
                  <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 14 }}>
                    <span style={{ fontSize: 10, fontWeight: 600, color: c.sub,
                      textTransform: 'uppercase', letterSpacing: '0.06em' }}>
                      Relationship Graph
                    </span>
                    <span style={{ fontSize: 10, color: c.gray }}>
                      {graphParents.length} parent{graphParents.length !== 1 ? 's' : ''} ·{' '}
                      {(expandedChildren.length || graphChildren.length)} child{graphChildren.length !== 1 ? 'ren' : ''}
                    </span>
                  </div>
                  <MiniGraph
                    center={selectedNode}
                    parents={graphParents}
                    children={expandedChildren.length > 0 ? expandedChildren.slice(0, 20) : graphChildren}
                    onSelect={handleSelectNode}
                  />
                </div>

                {/* Children grid */}
                {(expandedChildren.length > 0 || graphChildren.length > 0) && (() => {
                  const kids = expandedChildren.length > 0 ? expandedChildren : graphChildren;
                  const degradedKids   = kids.filter(k => k.health === 'degraded');
                  const unhealthyKids  = kids.filter(k => k.health === 'unhealthy');
                  return (
                    <div>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 10 }}>
                        <span style={{ fontSize: 10, fontWeight: 600, color: c.sub,
                          textTransform: 'uppercase', letterSpacing: '0.06em' }}>
                          Children ({kids.length})
                        </span>
                        {unhealthyKids.length > 0 && (
                          <span style={{ fontSize: 9, padding: '1px 6px', borderRadius: 4,
                            background: c.red + '15', color: c.red, fontWeight: 600,
                            border: `1px solid ${c.red}30` }}>
                            {unhealthyKids.length} down
                          </span>
                        )}
                        {degradedKids.length > 0 && (
                          <span style={{ fontSize: 9, padding: '1px 6px', borderRadius: 4,
                            background: c.orange + '15', color: c.orange, fontWeight: 600,
                            border: `1px solid ${c.orange}30` }}>
                            {degradedKids.length} degraded
                          </span>
                        )}
                      </div>
                      <div style={{ display: 'grid',
                        gridTemplateColumns: 'repeat(auto-fill, minmax(170px, 1fr))', gap: 7 }}>
                        {kids.map(child => {
                          const ccfg = NODE_CFG[child.node_type] ?? NODE_CFG.cloudstack_vm;
                          const chc  = HEALTH_COLOR[child.health] ?? c.gray;
                          const isIssue = child.health === 'unhealthy' || child.health === 'degraded';
                          return (
                            <motion.div key={child.id} whileHover={{ y: -2 }}
                              onClick={() => handleSelectNode(child)}
                              style={{ padding: '9px 11px', borderRadius: 10, cursor: 'pointer',
                                border: `1px solid ${isIssue ? chc + '50' : ccfg.color + '28'}`,
                                background: isIssue ? chc + '0C' : ccfg.color + '07' }}>
                              <div style={{ display: 'flex', alignItems: 'center', gap: 5, marginBottom: 3 }}>
                                <ccfg.icon style={{ width: 12, height: 12, color: ccfg.color, flexShrink: 0 }} />
                                <span style={{ fontSize: 11, fontWeight: 700, color: c.text, flex: 1, minWidth: 0,
                                  overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                                  {child.label}
                                </span>
                                <HealthIcon h={child.health} size={10} />
                              </div>
                              {(child.data?.ip || child.data?.internal_ip) && (
                                <div style={{ fontSize: 9, color: c.sub, fontFamily: 'ui-monospace, monospace' }}>
                                  {child.data.ip || child.data.internal_ip}
                                </div>
                              )}
                              {child.data?.namespace && (
                                <div style={{ fontSize: 9, color: c.sub }}>{child.data.namespace}</div>
                              )}
                              {child.data?.phase && (
                                <div style={{ fontSize: 9, color: chc, fontWeight: 600 }}>{child.data.phase}</div>
                              )}
                            </motion.div>
                          );
                        })}
                      </div>
                    </div>
                  );
                })()}
              </motion.div>
            </AnimatePresence>
          )}
        </div>

        {/* RIGHT ── node detail panel ─────────────────────────────────────────── */}
        <div style={{ width: 290, borderLeft: `1px solid ${c.sep}`, overflow: 'auto',
          padding: 14, flexShrink: 0 }}>
          <AnimatePresence>
            {selectedNode && (
              <NodeDetail
                key={selectedNode.id}
                node={selectedNode}
                onClose={() => { setSelectedNode(null); }}
                onExpand={handleExpand}
                onFilterHealth={handleFilterHealth}
                expandedChildren={expandedChildren}
                allEdges={graph?.edges ?? []}
                nodeByID={nodeByID}
                onSelectNode={handleSelectNode}
              />
            )}
          </AnimatePresence>
          {!selectedNode && (
            <div style={{ textAlign: 'center', padding: '40px 14px', color: c.gray }}>
              <Info style={{ width: 28, height: 28, marginBottom: 10 }} />
              <p style={{ fontSize: 12, lineHeight: 1.5, margin: 0 }}>
                Select a node<br />to see details
              </p>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
