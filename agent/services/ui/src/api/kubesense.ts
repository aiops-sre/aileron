// KubeSense API client — typed interface to the kubesense-api service.
// All requests target /api/v1/* and are proxied to the backend in dev.
const BASE = import.meta.env.VITE_API_URL ?? '';

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const headers: HeadersInit = { 'Content-Type': 'application/json' };
  const token = localStorage.getItem('ks_token');
  if (token) (headers as Record<string, string>)['Authorization'] = `Bearer ${token}`;

  const res = await fetch(`${BASE}${path}`, { ...init, headers: { ...headers, ...init?.headers } });
  if (!res.ok) {
    const body = await res.text().catch(() => res.statusText);
    throw new Error(body || `HTTP ${res.status}`);
  }
  return res.json() as Promise<T>;
}

// ─── Types ────────────────────────────────────────────────────────────────────

export interface Cluster {
  id: string;
  first_seen: string;
  last_heartbeat: string;
  agent_version: string;
  node_count: number;
  status: string;
}

export interface TopologyNode {
  entity_id: string;
  entity_kind: string;
  namespace: string;
  name: string;
  depth: number;
}

export interface Investigation {
  investigation_id: string;
  status: 'running' | 'completed' | 'failed';
  confidence: number;
  evidence_grade: 'A' | 'B' | 'C' | 'D' | 'F';
  duration_ms: number;
  evidence_count: number;
  change_count: number;
  chain_length: number;
  hypotheses: number;
  rejected: number;
  root_cause?: {
    entity_id: string;
    entity_kind: string;
    entity_name: string;
    entity_namespace: string;
    confidence: number;
    failure_mode: string;
  };
}

export interface RiskScore {
  raw_score: number;
  level: 'low' | 'medium' | 'high' | 'critical';
  summary: string;
  factors: Array<{ name: string; score: number; description: string; weight: number }>;
  similar_incidents: Array<{
    failure_mode: string;
    namespace: string;
    resource_kind: string;
    similarity: number;
    occurred_at: string;
  }>;
}

export interface WorkloadScore {
  resource_kind: string;
  namespace: string;
  name: string;
  score: number;
  grade: 'A' | 'B' | 'C' | 'D' | 'F';
  replicas: number;
  findings: Array<{
    check_id: string;
    severity: string;
    title: string;
    description: string;
    remediation: string;
    score_penalty: number;
  }>;
}

export interface ClusterChaosScore {
  cluster_id: string;
  overall_score: number;
  overall_grade: string;
  workload_scores: WorkloadScore[];
  top_findings: WorkloadScore['findings'];
  total_workloads: number;
  healthy_count: number;
  at_risk_count: number;
  critical_count: number;
  summary: string;
}

export interface DriftRecord {
  id: string;
  detected_at: string;
  cluster_id: string;
  resource_kind: string;
  namespace: string;
  name: string;
  drift_type: string;
  severity: string;
  field: string;
  expected_value: string;
  actual_value: string;
  actor?: string;
  drifted_at?: string;
  git_source?: string;
  blast_radius: string;
  description: string;
  remediation: string;
}

export interface DriftReport {
  cluster_id: string;
  generated_at: string;
  total_drifted: number;
  critical_drifts: number;
  high_drifts: number;
  records: DriftRecord[];
  drift_score: number;
  drift_level: 'clean' | 'minor' | 'drifted' | 'critical';
  top_actor?: string;
  summary: string;
}

export interface ToilSummary {
  team: string;
  namespace?: string;
  window_days: number;
  total_actions: number;
  total_hours: number;
  automatable_hours: number;
  automatable_pct: number;
  weekly_hours: number;
  toil_score: number;
  toil_level: 'healthy' | 'warning' | 'critical';
  by_category: Array<{ category: string; count: number; hours: number; pct: number }>;
  top_patterns: Array<{ signature: string; count: number; total_hours: number }>;
  suggested_automations: Array<{ title: string; estimated_weekly_hours_saved: number; effort: string }>;
  trend: string;
  trend_pct: number;
}

export interface Playbook {
  id: string;
  title: string;
  failure_mode: string;
  resource_kind: string;
  overall_success_rate: number;
  data_points: number;
  steps: Array<{
    order: number;
    action_type: string;
    command?: string;
    description: string;
    success_rate: number;
  }>;
}

export interface ChangeHistory {
  incident_id: string;
  window_start: string;
  window_end: string;
  changes: Array<{
    resource_kind: string;
    namespace: string;
    name: string;
    source: string;
    actor: string;
    occurred_at: string;
    correlation_score: number;
    correlation_basis: string;
    git_commit?: { sha: string; pr_title: string; author: string };
  }>;
  top_change?: ChangeHistory['changes'][0];
  change_count: number;
}

// ─── API methods ──────────────────────────────────────────────────────────────

export const api = {
  // Clusters
  listClusters: () =>
    request<{ clusters: Cluster[]; total: number }>('/api/v1/clusters'),

  // Topology
  getUpstreamChain: (clusterId: string, kind: string, namespace: string, name: string) =>
    request<{ cluster_id: string; upstream: TopologyNode[]; count: number }>(
      `/api/v1/clusters/${clusterId}/topology?kind=${kind}&namespace=${namespace}&name=${name}`
    ),

  getBlastRadius: (clusterId: string, kind: string, namespace: string, name: string) =>
    request<{ affected: TopologyNode[]; total_affected: number }>(
      `/api/v1/clusters/${clusterId}/blast-radius?kind=${kind}&namespace=${namespace}&name=${name}`
    ),

  // Investigations
  startInvestigation: (body: {
    cluster_id: string;
    incident_id?: string;
    affected_resources: Array<{ kind: string; namespace: string; name: string }>;
    incident_time?: string;
    async?: boolean;
  }) =>
    request<Investigation>('/api/v1/investigations', { method: 'POST', body: JSON.stringify(body) }),

  getInvestigation: (id: string) =>
    request<Investigation>(`/api/v1/investigations/${id}`),

  // Risk scoring
  scoreChange: (body: {
    cluster_id: string;
    resource_kind: string;
    namespace: string;
    name: string;
    change_type: string;
    actor?: string;
    new_image_tag?: string;
    old_image_tag?: string;
  }) =>
    request<RiskScore>('/api/v1/risk/score', { method: 'POST', body: JSON.stringify(body) }),

  // Change history
  getChangeHistory: (clusterId: string, lookbackHours = 6) =>
    request<ChangeHistory>(
      `/api/v1/clusters/${clusterId}/change/history?lookback_hours=${lookbackHours}`
    ),

  // Playbooks
  listPlaybooks: (clusterId: string) =>
    request<{ playbooks: Playbook[]; total: number }>(
      `/api/v1/clusters/${clusterId}/playbooks`
    ),

  getPlaybook: (clusterId: string, failureMode: string, kind?: string) => {
    const q = kind ? `?kind=${kind}` : '';
    return request<Playbook>(`/api/v1/clusters/${clusterId}/playbooks/${failureMode}${q}`);
  },

  // Incident resolution feedback (wires into playbook + toil training)
  resolveIncident: (body: {
    incident_id: string;
    cluster_id: string;
    failure_mode: string;
    resource_kind: string;
    namespace: string;
    resolution_mins: number;
    actions: Array<{ type: string; command: string; was_effective: boolean }>;
    resolved_by: string;
  }) =>
    request<{ ok: boolean; playbook_updated: boolean }>(
      '/api/v1/incidents/resolve',
      { method: 'POST', body: JSON.stringify(body) }
    ),
};
