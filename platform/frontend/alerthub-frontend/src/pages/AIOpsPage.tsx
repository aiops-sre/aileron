import React, { useState, useEffect, useCallback, useRef } from 'react'
import { BarChart, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer, CartesianGrid } from 'recharts'
import { useNavigate } from 'react-router-dom'
import { useBreakpoint } from '@/hooks/useBreakpoint'
import { motion, AnimatePresence } from 'framer-motion'
import {
  Brain, Zap, TrendingUp, AlertCircle, Target, Activity, BarChart3,
  Lightbulb, Search, RefreshCw, Eye, Filter, X, CheckCircle, Clock,
  Loader2, GitBranch, Link2, ThumbsUp, ThumbsDown, AlertTriangle,
  ArrowRight, ChevronDown, ChevronUp, Cpu, Radio, Layers, Telescope,
  Play, Plus, Trash2, Pause, Settings, Key, Sparkles, Shield,
  BookOpen, ThumbsUp as Approve, ThumbsDown as Reject,
} from 'lucide-react'
import { aiopsApi, aiApi, correlationApi, workflowApi, incidentsApi, intelligenceApi } from '../lib/api'
import { InvestigationStream } from '@/components/rca/InvestigationStream'
import { KnowledgeEditor } from '@/components/rca/KnowledgeEditor'
import toast from 'react-hot-toast'

// ── Design tokens ──────────────────────────────────────────────────────────
const a = {
  blue: '#007AFF', green: '#34C759', red: '#FF3B30', orange: '#FF9500',
  yellow: '#FFCC00', purple: '#AF52DE', teal: '#5AC8FA', indigo: '#5856D6',
  gray: '#8E8E93',
  label: 'var(--color-text)',
  secondaryLabel: 'var(--color-text-secondary)',
  tertiaryLabel: 'var(--color-text-tertiary, #8E8E93)',
  quaternaryLabel: 'rgba(142,142,147,0.35)',
  separator: 'var(--color-separator, rgba(142,142,147,0.12))',
  fill: 'var(--color-fill, rgba(142,142,147,0.08))',
  bg: 'var(--color-background)',
  card: 'var(--color-card, rgba(255,255,255,0.85))',
  r: { xs: 4, sm: 6, md: 10, lg: 12, xl: 16 },
} as const

// ── Intelligent workflow templates ─────────────────────────────────────────
const INTELLIGENT_TEMPLATES = [
  {
    id: 'critical-escalate',
    name: 'Critical Alert Auto-Escalation',
    description: 'Notifies on-call team immediately, escalates to manager if unacknowledged in 5 min',
    category: 'Incident Response',
    color: '#FF3B30',
    trigger: { type: 'alert', conditions: [{ field: 'severity', operator: '==', value: 'critical' }] },
    steps: [
      { id: 's1', name: 'Notify Slack #incidents', type: 'notification', enabled: true, position: { x: 0, y: 0 }, action: { channel: '#incidents', message: '🚨 Critical: {{alert.title}} from {{alert.source}}' } },
      { id: 's2', name: 'Wait 5 minutes', type: 'wait', enabled: true, position: { x: 0, y: 1 }, action: { duration: '5m' } },
      { id: 's3', name: 'Escalate to PagerDuty', type: 'action', enabled: true, position: { x: 0, y: 2 }, action: { type: 'pagerduty_escalate', policy: 'default' } },
    ],
    tags: ['incident-response', 'critical', 'auto'],
  },
  {
    id: 'oomkilled-remediation',
    name: 'K8s OOMKilled Remediation',
    description: 'Detects OOMKilled pods and automatically increases memory limits by 25% via kubectl patch',
    category: 'Auto-Remediation',
    color: '#FF9500',
    trigger: { type: 'alert', conditions: [{ field: 'title', operator: 'contains', value: 'OOMKilled' }] },
    steps: [
      { id: 's1', name: 'Extract pod info', type: 'action', enabled: true, position: { x: 0, y: 0 }, action: { type: 'kubectl', command: 'get pod {{alert.pod}} -n {{alert.namespace}} -o json' } },
      { id: 's2', name: 'Patch memory limits +25%', type: 'action', enabled: true, position: { x: 0, y: 1 }, action: { type: 'kubectl', command: "patch deployment {{alert.deployment}} -n {{alert.namespace}} --patch '{\"spec\":{\"template\":{\"spec\":{\"containers\":[{\"name\":\"{{alert.container}}\",\"resources\":{\"limits\":{\"memory\":\"{{computed.newMemory}}\"}}}]}}}}'" } },
      { id: 's3', name: 'Notify team', type: 'notification', enabled: true, position: { x: 0, y: 2 }, action: { channel: '#sre-alerts', message: '✅ Auto-remediated OOMKilled pod {{alert.pod}}: limits patched' } },
    ],
    tags: ['k8s', 'oomkilled', 'auto-remediation'],
  },
  {
    id: 'high-cpu-autoscale',
    name: 'High CPU — Auto-Scale',
    description: 'Triggers HPA scale-up when CPU throttling exceeds threshold, verifies after 10 min',
    category: 'Auto-Remediation',
    color: '#AF52DE',
    trigger: { type: 'alert', conditions: [{ field: 'metric_name', operator: 'contains', value: 'cpu_throttle' }] },
    steps: [
      { id: 's1', name: 'Check replicas', type: 'action', enabled: true, position: { x: 0, y: 0 }, action: { type: 'kubectl', command: 'get hpa -n {{alert.namespace}}' } },
      { id: 's2', name: 'Scale up +2', type: 'action', enabled: true, position: { x: 0, y: 1 }, action: { type: 'kubectl', command: 'scale deployment {{alert.deployment}} --replicas={{computed.newReplicas}} -n {{alert.namespace}}' } },
      { id: 's3', name: 'Wait 10 min', type: 'wait', enabled: true, position: { x: 0, y: 2 }, action: { duration: '10m' } },
      { id: 's4', name: 'Verify CPU normalized', type: 'condition', enabled: true, position: { x: 0, y: 3 }, condition: { field: 'alert.status', operator: '==', value: 'resolved' } },
    ],
    tags: ['k8s', 'cpu', 'scaling'],
  },
  {
    id: 'daily-healthcheck',
    name: 'Daily Infra Health Report',
    description: 'Every morning at 6 AM — validates cluster nodes, checks failing pods, sends Slack digest',
    category: 'Scheduled',
    color: '#34C759',
    trigger: { type: 'schedule', schedule: '0 6 * * *' },
    steps: [
      { id: 's1', name: 'Check node health', type: 'action', enabled: true, position: { x: 0, y: 0 }, action: { type: 'kubectl', command: 'get nodes --all-namespaces' } },
      { id: 's2', name: 'Find failing pods', type: 'action', enabled: true, position: { x: 0, y: 1 }, action: { type: 'kubectl', command: 'get pods -A --field-selector=status.phase!=Running' } },
      { id: 's3', name: 'Send health report', type: 'notification', enabled: true, position: { x: 0, y: 2 }, action: { channel: '#sre-daily', message: '📊 Daily health: {{computed.nodeCount}} nodes OK, {{computed.failingPods}} failing pods' } },
    ],
    tags: ['scheduled', 'health', 'daily'],
  },
  {
    id: 'db-recovery',
    name: 'Database Connection Recovery',
    description: 'Auto-detects DB connection pool exhaustion and rolls out a restart with readiness check',
    category: 'Auto-Remediation',
    color: '#5AC8FA',
    trigger: { type: 'alert', conditions: [{ field: 'title', operator: 'contains', value: 'database' }] },
    steps: [
      { id: 's1', name: 'Inspect connection pool', type: 'action', enabled: true, position: { x: 0, y: 0 }, action: { type: 'kubectl', command: 'exec -n {{alert.namespace}} {{alert.pod}} -- env | grep -i db' } },
      { id: 's2', name: 'Rollout restart', type: 'action', enabled: true, position: { x: 0, y: 1 }, action: { type: 'kubectl', command: 'rollout restart deployment/{{alert.deployment}} -n {{alert.namespace}}' } },
      { id: 's3', name: 'Wait for rollout', type: 'wait', enabled: true, position: { x: 0, y: 2 }, action: { duration: '2m' } },
      { id: 's4', name: 'Verify recovery', type: 'action', enabled: true, position: { x: 0, y: 3 }, action: { type: 'kubectl', command: 'rollout status deployment/{{alert.deployment}} -n {{alert.namespace}}' } },
    ],
    tags: ['database', 'recovery', 'auto-remediation'],
  },
]

// ── Types ──────────────────────────────────────────────────────────────────
interface PipelineResult {
  id: string
  alert_id: string
  incident_id?: string
  alert_title: string
  alert_source: string
  alert_severity: string
  alert_description?: string
  cluster?: string
  namespace?: string
  workload?: string
  incident_number?: string
  alert_status?: string
  decision: 'create_incident' | 'merge_incident' | 'monitor' | 'discard'
  final_score: number
  dominant_strategy: string
  semantic_score: number
  temporal_score: number
  topology_score: number
  rules_score: number
  reasoning: string
  ai_root_cause: string
  matched_node_label: string
  root_cause_label: string
  elapsed_ms: number
  processed_at: string
  // V2 fields
  domain?: string
  ontology_class?: string
  topo_root_entity?: string
  blast_radius_count?: number
  rca_hypotheses?: Array<{
    entity_id: string; entity_label: string; entity_type: string
    confidence: number
    evidence: Array<{ source: string; score: number; description: string }>
    reasoning: string
  }>
  explanation_json?: {
    decision_path: string[]
    score_contributions: Record<string, number>
    why_merged?: string
    why_created?: string
    topology_chain?: string[]
  }
}

interface PipelineStats {
  total_processed: number
  total_incidents_created: number
  total_merged: number
  total_monitored: number
  total_discarded: number
  avg_score: number
  avg_elapsed_ms: number
  by_strategy: Record<string, number>
  by_source: Record<string, number>
  by_domain?: Record<string, number>
  noise_reduction_rate: number
}

interface DashboardData {
  pipeline: PipelineStats & { by_domain?: Record<string, number> }
  hourly_activity: Array<{ hour: string; count: number }>
  recent_incidents: Array<{ id: string; title: string; severity: string; status: string; created_at: string }>
}

interface AlertCluster {
  id: string; name: string; alerts_count: number
  confidence: number; cluster_type: string; created_at: string; status: 'active' | 'resolved'
}

interface FatigueAnalysis {
  score: number
  factors: Array<{ name: string; impact: number; description: string }>
  recommendations: string[]
}

// ── Helpers ────────────────────────────────────────────────────────────────
const decisionConfig: Record<string, { label: string; color: string; bg: string; icon: React.ComponentType<any> }> = {
  create_incident: { label: 'Incident Created', color: a.green,  bg: `${a.green}15`,  icon: AlertCircle },
  merge_incident:  { label: 'Merged',           color: a.blue,   bg: `${a.blue}15`,   icon: Link2 },
  monitor:         { label: 'Monitoring',        color: a.orange, bg: `${a.orange}15`, icon: Eye },
  discard:         { label: 'Discarded',         color: a.gray,   bg: `rgba(142,142,147,0.12)`, icon: X },
}

const strategyColors: Record<string, string> = {
  semantic: a.purple, temporal: a.teal, topology: a.orange, rules: a.indigo,
  root_cause_engine: a.red, root_cause: a.red,
}

const domainColors: Record<string, string> = {
  kubernetes: '#326CE5', database: '#FF6B6B', network: '#4ECDC4',
  application: '#45B7D1', infrastructure: '#96CEB4', security: '#FFEAA7',
  storage: '#DDA0DD', messaging: '#98D8C8', unknown: a.gray,
}

const severityColor = (s: string) =>
  s === 'critical' ? a.red : s === 'high' ? a.orange : s === 'warning' ? a.yellow : a.gray

const sourceColor = (s: string) => {
  const m: Record<string, string> = {
    dynatrace: a.purple, prometheus: a.orange, grafana: a.blue,
    zabbix: a.teal, datadog: '#632CA6', nagios: '#000', pagerduty: '#06AC38',
  }
  return m[s?.toLowerCase()] ?? a.gray
}

const pct = (n: number) => `${Math.round(n * 100)}%`
const fmtTime = (iso: string) => {
  if (!iso) return ''
  const d = new Date(iso)
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}
const fmtDate = (iso: string) => {
  if (!iso) return ''
  const d = new Date(iso)
  return d.toLocaleDateString([], { month: 'short', day: 'numeric' }) + ' ' +
    d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

// ── Mini score bar ─────────────────────────────────────────────────────────
const ScoreBar: React.FC<{ label: string; value: number; color: string }> = ({ label, value, color }) => (
  <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
    <span style={{ fontSize: 10, color: a.tertiaryLabel, width: 52, flexShrink: 0, textTransform: 'capitalize' }}>{label}</span>
    <div style={{ flex: 1, height: 4, background: a.fill, borderRadius: 2, overflow: 'hidden' }}>
      <div style={{ width: `${Math.min(100, value * 100)}%`, height: '100%', background: color, borderRadius: 2, transition: 'width 0.6s ease' }} />
    </div>
    <span style={{ fontSize: 10, color: a.secondaryLabel, width: 30, textAlign: 'right', flexShrink: 0 }}>{Math.round(value * 100)}%</span>
  </div>
)

// ── Correlation result card ────────────────────────────────────────────────
const ResultCard: React.FC<{
  r: PipelineResult
  onFeedback: (alertId: string, type: string, dominantStrategy: string) => void
  feedbackSent: Set<string>
}> = ({ r, onFeedback, feedbackSent }) => {
  const [expanded, setExpanded] = useState(false)
  const navigate = useNavigate()
  const dc = decisionConfig[r.decision] ?? decisionConfig.discard
  const DIcon = dc.icon
  const hasFeedback = feedbackSent.has(r.alert_id)

  return (
    <motion.div
      layout
      initial={{ opacity: 0, y: 6 }}
      animate={{ opacity: 1, y: 0 }}
      style={{
        background: a.card,
        border: `0.5px solid ${a.separator}`,
        borderRadius: a.r.lg,
        overflow: 'hidden',
        marginBottom: 8,
      }}
    >
      <div
        style={{ padding: '12px 14px', cursor: 'pointer', userSelect: 'none' }}
        onClick={() => setExpanded(e => !e)}
      >
        <div style={{ display: 'flex', alignItems: 'flex-start', gap: 10 }}>
          <div style={{
            flexShrink: 0, marginTop: 1,
            display: 'flex', alignItems: 'center', gap: 4,
            padding: '2px 8px', borderRadius: 20,
            background: dc.bg, color: dc.color,
            fontSize: 10, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.3px',
            whiteSpace: 'nowrap',
          }}>
            <DIcon style={{ width: 9, height: 9 }} />
            {dc.label}
          </div>

          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ fontSize: 13, fontWeight: 600, color: a.label, lineHeight: 1.3, marginBottom: 3 }}>
              {r.alert_title || '(untitled alert)'}
            </div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' }}>
              <span style={{
                fontSize: 10, fontWeight: 600, padding: '1px 6px', borderRadius: 4,
                background: `${sourceColor(r.alert_source)}20`, color: sourceColor(r.alert_source),
              }}>
                {r.alert_source || 'unknown'}
              </span>
              <span style={{
                fontSize: 10, fontWeight: 500, padding: '1px 6px', borderRadius: 4,
                background: `${severityColor(r.alert_severity)}15`, color: severityColor(r.alert_severity),
              }}>
                {r.alert_severity}
              </span>
              {r.domain && (
                <span style={{
                  fontSize: 10, fontWeight: 500, padding: '1px 6px', borderRadius: 4,
                  background: `${domainColors[r.domain] ?? a.gray}20`, color: domainColors[r.domain] ?? a.gray,
                }}>
                  {r.domain}
                </span>
              )}
              {r.ontology_class && (
                <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 4, background: a.fill, color: a.secondaryLabel }}>
                  {r.ontology_class}
                </span>
              )}
              <span style={{ fontSize: 11, color: a.secondaryLabel }}>
                score: <strong style={{ color: r.final_score >= 0.7 ? a.green : r.final_score >= 0.4 ? a.yellow : a.red }}>
                  {Math.round(r.final_score * 100)}%
                </strong>
              </span>
              {r.dominant_strategy && (
                <span style={{
                  fontSize: 10, padding: '1px 6px', borderRadius: 4,
                  background: `${strategyColors[r.dominant_strategy] ?? a.gray}18`,
                  color: strategyColors[r.dominant_strategy] ?? a.gray,
                  fontWeight: 500,
                }}>
                  ★ {r.dominant_strategy}
                </span>
              )}
              <span style={{ fontSize: 10, color: a.tertiaryLabel }}>{fmtTime(r.processed_at)}</span>
              <span style={{ fontSize: 10, color: a.quaternaryLabel }}>{r.elapsed_ms}ms</span>
            </div>
          </div>

          <div style={{ color: a.tertiaryLabel, flexShrink: 0, marginTop: 2 }}>
            {expanded ? <ChevronUp style={{ width: 14, height: 14 }} /> : <ChevronDown style={{ width: 14, height: 14 }} />}
          </div>
        </div>

        {/* Score breakdown — always visible */}
        <div style={{ marginTop: 10, display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '4px 16px' }}>
          <ScoreBar label="semantic"  value={r.semantic_score}  color={r.semantic_score > 0 ? strategyColors.semantic : a.gray} />
          <ScoreBar label="temporal"  value={r.temporal_score}  color={r.temporal_score > 0 ? strategyColors.temporal : a.gray} />
          <ScoreBar label="topology"  value={r.topology_score}  color={r.topology_score > 0 ? strategyColors.topology : a.gray} />
          <ScoreBar label="rules"     value={r.rules_score}     color={r.rules_score > 0 ? strategyColors.rules : a.gray} />
        </div>
      </div>

      <AnimatePresence>
        {expanded && (
          <motion.div
            initial={{ height: 0, opacity: 0 }}
            animate={{ height: 'auto', opacity: 1 }}
            exit={{ height: 0, opacity: 0 }}
            transition={{ duration: 0.2 }}
            style={{ overflow: 'hidden' }}
          >
            <div style={{
              padding: '0 14px 14px',
              borderTop: `0.5px solid ${a.separator}`,
              paddingTop: 12,
              display: 'flex', flexDirection: 'column', gap: 10,
            }}>
              {/* ── Incident formation audit trail ── */}
              {r.incident_id && (
                <div style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12 }}>
                  <CheckCircle style={{ width: 12, height: 12, color: r.decision === 'merge_incident' ? a.blue : a.green }} />
                  <span style={{ color: a.secondaryLabel }}>
                    {r.decision === 'merge_incident' ? 'Merged into:' : 'Created:'}
                  </span>
                  <a
                    href="#"
                    style={{ color: a.blue, fontFamily: 'monospace', fontSize: 11, textDecoration: 'none', fontWeight: 600 }}
                    onClick={e => { e.preventDefault(); e.stopPropagation(); navigate('/incidents') }}
                  >
                    {r.incident_number ? `Incident #${r.incident_number}` : `Incident ${r.incident_id.slice(0, 8)}…`}
                  </a>
                  {r.blast_radius_count != null && r.blast_radius_count > 0 && (
                    <span style={{ fontSize: 10, color: a.orange, fontWeight: 500 }}>
                      · blast: {r.blast_radius_count} entities
                    </span>
                  )}
                </div>
              )}

              {/* ── Score contribution breakdown ── */}
              <div style={{ padding: '10px 12px', borderRadius: a.r.sm, background: a.fill, border: `0.5px solid ${a.separator}` }}>
                <div style={{ fontSize: 10, fontWeight: 600, color: a.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.4px', marginBottom: 8 }}>
                  Score Breakdown
                </div>
                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '6px 16px' }}>
                  {[
                    { label: 'Semantic', val: r.semantic_score, color: strategyColors.semantic, hint: 'Title/desc similarity' },
                    { label: 'Temporal', val: r.temporal_score, color: strategyColors.temporal, hint: 'Time proximity' },
                    { label: 'Topology', val: r.topology_score, color: strategyColors.topology, hint: 'Infra graph overlap' },
                    { label: 'Rules', val: r.rules_score, color: strategyColors.rules, hint: 'Policy match' },
                  ].map(({ label, val, color, hint }) => (
                    <div key={label}>
                      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 3 }}>
                        <span style={{ fontSize: 10, color: a.secondaryLabel, fontWeight: 500 }}>{label}</span>
                        <span style={{ fontSize: 10, color: val >= 0.7 ? color : a.tertiaryLabel, fontWeight: 600 }}>{Math.round(val * 100)}%</span>
                      </div>
                      <div style={{ height: 5, background: `${a.separator}`, borderRadius: 3, overflow: 'hidden' }}>
                        <div style={{ width: `${Math.min(100, val * 100)}%`, height: '100%', background: val > 0 ? color : a.fill, borderRadius: 3, transition: 'width 0.5s ease' }} />
                      </div>
                      <div style={{ fontSize: 9, color: a.tertiaryLabel, marginTop: 2 }}>{hint}</div>
                    </div>
                  ))}
                </div>
                {r.dominant_strategy === 'root_cause_engine' && (
                  <div style={{ marginTop: 8, padding: '5px 8px', borderRadius: a.r.xs, background: `${a.red}10`, border: `0.5px solid ${a.red}30`, fontSize: 10, color: a.red }}>
                    ⚡ Root Cause Engine bypassed scoring — deterministic entity-tree decision
                  </div>
                )}
              </div>

              {/* ── Topology path / chain ── */}
              {(r.topo_root_entity || r.matched_node_label || r.root_cause_label || r.explanation_json?.topology_chain) && (
                <div style={{ padding: '10px 12px', borderRadius: a.r.sm, background: `${a.orange}08`, border: `0.5px solid ${a.orange}25` }}>
                  <div style={{ fontSize: 10, fontWeight: 600, color: a.orange, textTransform: 'uppercase', letterSpacing: '0.4px', marginBottom: 6 }}>
                    Topology Path
                  </div>
                  {r.explanation_json?.topology_chain && r.explanation_json.topology_chain.length > 0 ? (
                    <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 4 }}>
                      {r.explanation_json.topology_chain.map((node, i) => (
                        <React.Fragment key={i}>
                          <span style={{ padding: '2px 7px', borderRadius: 4, background: `${a.orange}18`, color: a.orange, fontSize: 10, fontWeight: 500 }}>{node}</span>
                          {i < r.explanation_json!.topology_chain!.length - 1 && (
                            <ArrowRight style={{ width: 9, height: 9, color: a.tertiaryLabel }} />
                          )}
                        </React.Fragment>
                      ))}
                    </div>
                  ) : (
                    <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' }}>
                      {r.topo_root_entity && (
                        <span style={{ padding: '2px 7px', borderRadius: 4, background: `${a.red}18`, color: a.red, fontSize: 10, fontWeight: 600 }}>
                          Root: {r.topo_root_entity}
                        </span>
                      )}
                      {r.matched_node_label && (
                        <>
                          <ArrowRight style={{ width: 9, height: 9, color: a.tertiaryLabel }} />
                          <span style={{ padding: '2px 7px', borderRadius: 4, background: `${a.orange}18`, color: a.orange, fontSize: 10 }}>
                            matched: {r.matched_node_label}
                          </span>
                        </>
                      )}
                      {r.root_cause_label && r.root_cause_label !== r.matched_node_label && (
                        <>
                          <ArrowRight style={{ width: 9, height: 9, color: a.tertiaryLabel }} />
                          <span style={{ padding: '2px 7px', borderRadius: 4, background: `${a.red}18`, color: a.red, fontSize: 10 }}>
                            cause: {r.root_cause_label}
                          </span>
                        </>
                      )}
                    </div>
                  )}
                </div>
              )}

              {/* ── RCA Evidence chain ── */}
              {r.rca_hypotheses && r.rca_hypotheses.length > 0 && (
                <div style={{ padding: '10px 12px', borderRadius: a.r.sm, background: `${a.purple}08`, border: `0.5px solid ${a.purple}25` }}>
                  <div style={{ fontSize: 10, fontWeight: 600, color: a.purple, textTransform: 'uppercase', letterSpacing: '0.4px', marginBottom: 8 }}>
                    RCA Evidence Chain
                  </div>
                  {r.rca_hypotheses.slice(0, 3).map((h, i) => (
                    <div key={i} style={{ marginBottom: i < 2 ? 8 : 0 }}>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4 }}>
                        <div style={{ width: 16, height: 16, borderRadius: '50%', background: `${a.purple}20`, display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 }}>
                          <span style={{ fontSize: 9, fontWeight: 700, color: a.purple }}>{i + 1}</span>
                        </div>
                        <span style={{ fontSize: 11, fontWeight: 600, color: a.label }}>{h.entity_label}</span>
                        <span style={{ fontSize: 9, color: a.tertiaryLabel }}>({h.entity_type})</span>
                        <div style={{ flex: 1, height: 3, background: a.fill, borderRadius: 2, overflow: 'hidden' }}>
                          <div style={{ width: `${Math.round(h.confidence * 100)}%`, height: '100%', background: h.confidence >= 0.7 ? a.green : h.confidence >= 0.4 ? a.orange : a.gray, borderRadius: 2 }} />
                        </div>
                        <span style={{ fontSize: 10, fontWeight: 600, color: h.confidence >= 0.7 ? a.green : h.confidence >= 0.4 ? a.orange : a.gray, flexShrink: 0 }}>
                          {Math.round(h.confidence * 100)}%
                        </span>
                      </div>
                      {h.reasoning && (
                        <div style={{ marginLeft: 22, fontSize: 11, color: a.secondaryLabel, lineHeight: 1.4 }}>{h.reasoning}</div>
                      )}
                    </div>
                  ))}
                </div>
              )}

              {/* ── Reasoning / decision path ── */}
              {r.reasoning && (
                <div style={{ padding: '8px 10px', borderRadius: a.r.sm, background: `${a.blue}08`, border: `0.5px solid ${a.blue}20` }}>
                  <div style={{ fontSize: 10, fontWeight: 600, color: a.blue, textTransform: 'uppercase', letterSpacing: '0.4px', marginBottom: 4 }}>
                    Decision Reasoning
                  </div>
                  <div style={{ fontSize: 11, color: a.secondaryLabel, lineHeight: 1.5 }}>{r.reasoning}</div>
                  {r.explanation_json?.why_merged && (
                    <div style={{ marginTop: 6, fontSize: 11, color: a.blue, lineHeight: 1.4 }}>
                      <strong>Why merged:</strong> {r.explanation_json.why_merged}
                    </div>
                  )}
                  {r.explanation_json?.why_created && (
                    <div style={{ marginTop: 6, fontSize: 11, color: a.green, lineHeight: 1.4 }}>
                      <strong>Why new incident:</strong> {r.explanation_json.why_created}
                    </div>
                  )}
                </div>
              )}

              {/* ── Zero-score explanation ── */}
              {r.semantic_score === 0 && r.topology_score === 0 && r.decision === 'monitor' && (
                <div style={{ padding: '6px 10px', borderRadius: a.r.sm, background: `${a.orange}10`, border: `0.5px solid ${a.orange}25`, fontSize: 11, color: a.orange }}>
                  ⚠ Semantic and topology scores are zero — K8s labels may not be indexed yet for this alert source. Deferred incident grouping will run in ~2 min.
                </div>
              )}

              {/* ── Alert infra context ── */}
              {(r.cluster || r.namespace || r.workload || r.alert_description) && (
                <div style={{ padding: '8px 10px', borderRadius: a.r.sm, background: `${a.teal}10`, border: `0.5px solid ${a.teal}25`, fontSize: 12 }}>
                  {r.alert_description && (
                    <div style={{ color: a.label, lineHeight: 1.5, marginBottom: (r.cluster || r.namespace || r.workload) ? 8 : 0 }}>
                      <div style={{ fontWeight: 600, color: a.secondaryLabel, fontSize: 10, marginBottom: 4, textTransform: 'uppercase', letterSpacing: '0.3px' }}>
                        Problem Details
                      </div>
                      <div style={{ maxHeight: 100, overflowY: 'auto', fontSize: 11, whiteSpace: 'pre-wrap', wordBreak: 'break-word', color: a.secondaryLabel }}>
                        {r.alert_description.split('\n').filter(l => l.trim()).slice(0, 10).join('\n')}
                      </div>
                    </div>
                  )}
                  {(r.cluster || r.namespace || r.workload) && (
                    <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginTop: r.alert_description ? 4 : 0 }}>
                      {r.cluster && <span style={{ padding: '1px 6px', borderRadius: 4, background: `${a.blue}15`, color: a.blue, fontSize: 10, fontWeight: 500 }}>cluster: {r.cluster}</span>}
                      {r.namespace && <span style={{ padding: '1px 6px', borderRadius: 4, background: `${a.teal}15`, color: a.teal, fontSize: 10, fontWeight: 500 }}>ns: {r.namespace}</span>}
                      {r.workload && <span style={{ padding: '1px 6px', borderRadius: 4, background: `${a.purple}15`, color: a.purple, fontSize: 10, fontWeight: 500 }}>workload: {r.workload}</span>}
                    </div>
                  )}
                </div>
              )}

              {/* ── AI Root Cause ── */}
              {r.ai_root_cause && (
                <div style={{ padding: '8px 10px', borderRadius: a.r.sm, background: `${a.purple}10`, border: `0.5px solid ${a.purple}25`, fontSize: 12, color: a.label, lineHeight: 1.5 }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 5, marginBottom: 4 }}>
                    <Brain style={{ width: 11, height: 11, color: a.purple }} />
                    <span style={{ fontSize: 10, fontWeight: 600, color: a.purple, textTransform: 'uppercase', letterSpacing: '0.3px' }}>AI Root Cause Analysis</span>
                  </div>
                  {r.ai_root_cause}
                </div>
              )}

              {/* ── Feedback ── */}
              {!hasFeedback ? (
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 2 }}>
                  <span style={{ fontSize: 11, color: a.tertiaryLabel }}>Feedback:</span>
                  <button onClick={e => { e.stopPropagation(); onFeedback(r.alert_id, 'confirmed', r.dominant_strategy) }}
                    style={{ display: 'flex', alignItems: 'center', gap: 4, padding: '4px 10px', borderRadius: a.r.sm, border: `0.5px solid ${a.green}40`, background: `${a.green}12`, color: a.green, fontSize: 11, fontWeight: 500, cursor: 'pointer' }}>
                    <ThumbsUp style={{ width: 11, height: 11 }} /> Correct
                  </button>
                  <button onClick={e => { e.stopPropagation(); onFeedback(r.alert_id, 'false_positive', r.dominant_strategy) }}
                    style={{ display: 'flex', alignItems: 'center', gap: 4, padding: '4px 10px', borderRadius: a.r.sm, border: `0.5px solid ${a.red}40`, background: `${a.red}12`, color: a.red, fontSize: 11, fontWeight: 500, cursor: 'pointer' }}>
                    <ThumbsDown style={{ width: 11, height: 11 }} /> False Positive
                  </button>
                  <button onClick={e => { e.stopPropagation(); onFeedback(r.alert_id, 'missed_correlation', r.dominant_strategy) }}
                    style={{ display: 'flex', alignItems: 'center', gap: 4, padding: '4px 10px', borderRadius: a.r.sm, border: `0.5px solid ${a.orange}40`, background: `${a.orange}12`, color: a.orange, fontSize: 11, fontWeight: 500, cursor: 'pointer' }}>
                    <AlertTriangle style={{ width: 11, height: 11 }} /> Missed
                  </button>
                </div>
              ) : (
                <div style={{ display: 'flex', alignItems: 'center', gap: 5, fontSize: 11, color: a.green }}>
                  <CheckCircle style={{ width: 12, height: 12 }} /> Feedback recorded — weights will auto-recalibrate
                </div>
              )}
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </motion.div>
  )
}

// ── Stat card ──────────────────────────────────────────────────────────────
const StatCard: React.FC<{
  icon: React.ComponentType<any>; iconColor: string; label: string
  value: string | number; sub?: string
}> = ({ icon: Icon, iconColor, label, value, sub }) => (
  <div style={{
    background: a.card, border: `0.5px solid ${a.separator}`, borderRadius: a.r.lg, padding: 16,
    display: 'flex', flexDirection: 'column', gap: 6,
  }}>
    <div style={{ display: 'flex', alignItems: 'center', gap: 7 }}>
      <div style={{
        width: 28, height: 28, borderRadius: a.r.sm,
        background: `${iconColor}18`, display: 'flex', alignItems: 'center', justifyContent: 'center',
      }}>
        <Icon style={{ width: 14, height: 14, color: iconColor }} />
      </div>
      <span style={{ fontSize: 12, color: a.secondaryLabel }}>{label}</span>
    </div>
    <div style={{ fontSize: 26, fontWeight: 700, color: a.label, lineHeight: 1 }}>{value}</div>
    {sub && <div style={{ fontSize: 11, color: a.tertiaryLabel }}>{sub}</div>}
  </div>
)

// ── Main component ─────────────────────────────────────────────────────────
const AIOpsPage: React.FC = () => {
  const navigate = useNavigate()
  const { isMobile, isDesktop } = useBreakpoint()
  type Tab = 'overview' | 'situations' | 'correlations' | 'predictions' | 'clusters' | 'fatigue' | 'workflows' | 'rca' | 'intelligence'
  type IntelSubTab = 'overview' | 'policies' | 'runbooks' | 'model' | 'test'
  const [activeTab, setActiveTab] = useState<Tab>('overview')
  const [intelSubTab, setIntelSubTab] = useState<IntelSubTab>('overview')
  const [timeRange, setTimeRange] = useState('24')

  // AIOps data state
  const [dashboard, setDashboard] = useState<DashboardData | null>(null)
  const [pipelineResults, setPipelineResults] = useState<PipelineResult[]>([])
  const [pipelineStats, setPipelineStats] = useState<PipelineStats | null>(null)
  const [clusters, setClusters] = useState<AlertCluster[]>([])
  const [fatigueAnalysis, setFatigueAnalysis] = useState<FatigueAnalysis | null>(null)
  const [loading, setLoading] = useState(true)
  const [correlationsLoading, setCorrelationsLoading] = useState(false)
  const [intelligenceStats, setIntelligenceStats] = useState<any>(null)
  const [pendingRemediations, setPendingRemediations] = useState<any[]>([])
  // KubeSense unified situations state (merged from correlation engine + AlertHub)
  const [ksSituations, setKsSituations] = useState<any[]>([])
  const [ksCorrelStatus, setKsCorrelStatus] = useState<any>(null)
  const [ksDbStats, setKsDbStats] = useState<any>(null)
  const [situationsLoading, setSituationsLoading] = useState(false)
  // Intelligence sub-tab state
  const [policies, setPolicies] = useState<any[]>([])
  const [runbooks, setRunbooks] = useState<any[]>([])
  const [policiesLoaded, setPoliciesLoaded] = useState(false)
  const [runbooksLoaded, setRunbooksLoaded] = useState(false)
  const [showAddPolicy, setShowAddPolicy] = useState(false)
  const [showAddRunbook, setShowAddRunbook] = useState(false)
  const [expandedRunbook, setExpandedRunbook] = useState<string | null>(null)
  const [policyForm, setPolicyForm] = useState({ name: '', description: '', policy_type: 'suppress_alert', condition: '{}', priority: 50 })
  const [runbookForm, setRunbookForm] = useState({ name: '', domain: '', entity_type: '', failure_class: '', content: '' })
  const [policyTestForm, setPolicyTestForm] = useState({ source: 'dynatrace', title: '', severity: 'high', namespace: '', entity_type: '', labels: '{}' })
  const [policyTestResult, setPolicyTestResult] = useState<any>(null)
  const [policyTestLoading, setPolicyTestLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [searchQuery, setSearchQuery] = useState('')
  const [decisionFilter, setDecisionFilter] = useState('all')
  const [sourceFilter, setSourceFilter] = useState('all')
  const [feedbackSent, setFeedbackSent] = useState<Set<string>>(new Set())
  const [liveCount, setLiveCount] = useState(0)
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null)

  // Workflow state
  const [workflows, setWorkflows] = useState<any[]>([])
  const [workflowsLoading, setWorkflowsLoading] = useState(false)
  const [selectedWf, setSelectedWf] = useState<any | null>(null)
  const [wfExecs, setWfExecs] = useState<any[]>([])
  const [wfExecsLoading, setWfExecsLoading] = useState(false)
  const [showCreateWf, setShowCreateWf] = useState(false)
  const [wfForm, setWfForm] = useState({ name: '', description: '', triggerType: 'alert', severity: 'critical', source: '', cron: '0 6 * * *', tags: '' })
  const [wfCreating, setWfCreating] = useState(false)
  const [wfSearchQuery, setWfSearchQuery] = useState('')

  // RCA state
  const [invs, setInvs] = useState<any[]>([])
  const [selectedInvId, setSelectedInvId] = useState<string | null>(null)
  const [rcaSubTab, setRcaSubTab] = useState<'active' | 'history' | 'knowledge' | 'model'>('active')
  const [rcaModel, setRcaModel] = useState<any>(null)
  const [invTitle, setInvTitle] = useState('')
  const [invSeverity, setInvSeverity] = useState('high')
  const [invNs, setInvNs] = useState('')
  const [invCluster, setInvCluster] = useState('')
  const [invPod, setInvPod] = useState('')
  const [invService, setInvService] = useState('')
  const [invStarting, setInvStarting] = useState(false)
  const [rcaTraining, setRcaTraining] = useState(false)

  // Floodgate RCA state (separate from AI chat Floodgate)
  const [floodgateRcaToken, setFloodgateRcaToken] = useState<string>(() =>
    localStorage.getItem('rca_floodgate_token') || ''
  )
  const [floodgateRcaModel, setFloodgateRcaModel] = useState<'claude-sonnet-4-6' | 'claude-opus-4-7'>('claude-sonnet-4-6')
  const [floodgateRunningFor, setFloodgateRunningFor] = useState<string | null>(null)
  const [floodgateTokenStatus, setFloodgateTokenStatus] = useState<'idle' | 'testing' | 'valid' | 'invalid'>('idle')

  const [processingMonitored, setProcessingMonitored] = useState(false)

  const hours = parseInt(timeRange)

  // ── AIOps loaders ──────────────────────────────────────────────────────
  const loadOverview = useCallback(async () => {
    try {
      setLoading(true)
      setError(null)
      const [dashRes, clustersRes, fatigueRes] = await Promise.allSettled([
        aiopsApi.getDashboard({ hours }),
        aiApi.getAlertClusters({ hours }),
        aiApi.analyzeFatigue({ hours }),
      ])
      if (dashRes.status === 'fulfilled' && dashRes.value.data) {
        setDashboard(dashRes.value.data)
      }
      if (clustersRes.status === 'fulfilled' && clustersRes.value.data?.success) {
        setClusters(clustersRes.value.data.clusters || [])
      }
      if (fatigueRes.status === 'fulfilled' && fatigueRes.value.data?.success) {
        setFatigueAnalysis(fatigueRes.value.data.analysis)
      }
    } catch (err: any) {
      setError(err.message || 'Failed to load AIOps data')
    } finally {
      setLoading(false)
    }
  }, [hours])

  const loadCorrelations = useCallback(async () => {
    try {
      setCorrelationsLoading(true)
      const res = await aiopsApi.getPipelineResults({ hours, limit: 200 })
      if (res.data) {
        setPipelineResults(res.data.results || [])
        setPipelineStats(res.data.stats || null)
        setLiveCount(c => c + 1)
      }
    } catch {
      // silent
    } finally {
      setCorrelationsLoading(false)
    }
  }, [hours])

  const processMonitoredAlerts = useCallback(async () => {
    setProcessingMonitored(true)
    try {
      const r = await aiopsApi.processMonitored()
      const d = r.data
      toast.success(`Processed ${d.total} monitor alerts: ${d.merged} merged, ${d.created} new incidents`)
      await loadOverview()
    } catch (e: any) {
      toast.error(e?.response?.data?.message || 'Failed to process monitored alerts')
    } finally {
      setProcessingMonitored(false)
    }
  }, [loadOverview])

  // ── Workflow loaders ────────────────────────────────────────────────────
  const loadWorkflows = useCallback(async () => {
    try {
      setWorkflowsLoading(true)
      const res = await workflowApi.listWorkflows()
      if (res.data?.success) setWorkflows(res.data.data?.workflows || [])
    } catch { /* silent */ }
    finally { setWorkflowsLoading(false) }
  }, [])

  const loadWfExecs = useCallback(async (wfId: string) => {
    try {
      setWfExecsLoading(true)
      const res = await workflowApi.listWorkflowExecutions(wfId)
      if (res.data?.success) setWfExecs(res.data.data?.executions || [])
    } catch { /* silent */ }
    finally { setWfExecsLoading(false) }
  }, [])

  const executeWf = useCallback(async (wf: any) => {
    try {
      await workflowApi.executeWorkflow(wf.id)
      toast.success(`"${wf.name}" triggered`)
      loadWfExecs(wf.id)
    } catch { toast.error('Failed to execute workflow') }
  }, [loadWfExecs])

  const toggleWf = useCallback(async (wf: any) => {
    try {
      if (wf.enabled) await workflowApi.disableWorkflow(wf.id)
      else await workflowApi.enableWorkflow(wf.id)
      loadWorkflows()
    } catch { toast.error('Failed to toggle workflow') }
  }, [loadWorkflows])

  const deleteWf = useCallback(async (wf: any) => {
    if (!window.confirm(`Delete "${wf.name}"?`)) return
    try {
      await workflowApi.deleteWorkflow(wf.id)
      setWorkflows(prev => prev.filter(w => w.id !== wf.id))
      if (selectedWf?.id === wf.id) setSelectedWf(null)
    } catch { toast.error('Failed to delete workflow') }
  }, [selectedWf])

  const deployTemplate = useCallback(async (tpl: typeof INTELLIGENT_TEMPLATES[number]) => {
    try {
      const res = await (workflowApi as any).createWorkflow({
        name: tpl.name,
        description: tpl.description,
        triggers: [tpl.trigger],
        steps: tpl.steps,
        enabled: true,
        tags: tpl.tags,
      })
      if (res.data?.success) {
        toast.success(`"${tpl.name}" deployed!`)
        loadWorkflows()
      }
    } catch { toast.error('Failed to deploy template') }
  }, [loadWorkflows])

  const createWorkflow = useCallback(async () => {
    if (!wfForm.name.trim()) return toast.error('Workflow name is required')
    setWfCreating(true)
    try {
      const triggers: any[] = []
      if (wfForm.triggerType === 'alert') {
        const conds: any[] = []
        if (wfForm.severity) conds.push({ field: 'severity', operator: '==', value: wfForm.severity })
        if (wfForm.source) conds.push({ field: 'source', operator: 'contains', value: wfForm.source })
        triggers.push({ type: 'alert', conditions: conds })
      } else if (wfForm.triggerType === 'schedule') {
        triggers.push({ type: 'schedule', schedule: wfForm.cron })
      } else {
        triggers.push({ type: wfForm.triggerType })
      }
      const res = await (workflowApi as any).createWorkflow({
        name: wfForm.name.trim(),
        description: wfForm.description,
        triggers,
        steps: [{ id: 's1', name: 'Notify Slack', type: 'notification', enabled: true, position: { x: 0, y: 0 }, action: { channel: '#incidents', message: `Workflow "${wfForm.name}" triggered` } }],
        enabled: true,
        tags: wfForm.tags ? wfForm.tags.split(',').map(t => t.trim()).filter(Boolean) : [],
      })
      if (res.data?.success) {
        toast.success('Workflow created')
        setShowCreateWf(false)
        setWfForm({ name: '', description: '', triggerType: 'alert', severity: 'critical', source: '', cron: '0 6 * * *', tags: '' })
        loadWorkflows()
      }
    } catch { toast.error('Failed to create workflow') }
    finally { setWfCreating(false) }
  }, [wfForm, loadWorkflows])

  // ── RCA callbacks ───────────────────────────────────────────────────────
  const loadInvestigations = useCallback(async () => {
    try {
      const r = await fetch('/api/v1/rca/investigations?limit=30')
      const data = await r.json()
      setInvs(Array.isArray(data) ? data : [])
    } catch {}
  }, [])

  const loadRcaModel = useCallback(async () => {
    try {
      const r = await fetch('/api/v1/rca/model/info')
      setRcaModel(await r.json())
    } catch {}
  }, [])

  const startInvestigation = useCallback(async () => {
    if (!invTitle) return toast.error('Enter an alert title')
    const isFloodgate = floodgateRcaModel !== null && floodgateRcaToken && rcaSubTab !== 'model'
    // If using Floodgate but no token, redirect to model tab
    if (floodgateRcaToken && !invTitle) return
    setInvStarting(true)
    try {
      const body: any = {
        alert_id: `manual-${Date.now()}`,
        alert_title: invTitle,
        alert_body: { title: invTitle, severity: invSeverity, source: 'manual', ...(invPod && { pod: invPod }) },
        severity: invSeverity,
        namespace: invNs || null,
        cluster: invCluster || null,
        service: invService || null,
      }
      // Pass Floodgate config if token is set and a claude model is selected
      if (floodgateRcaToken && floodgateRcaModel) {
        body.llm_provider = 'floodgate'
        body.llm_model = floodgateRcaModel
        body.llm_token = floodgateRcaToken
      }
      const r = await fetch('/api/v1/rca/investigations', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })
      const d = await r.json()
      setSelectedInvId(d.investigation_id)
      setRcaSubTab('active')
      await loadInvestigations()
      const modelLabel = floodgateRcaToken ? `Floodgate ${floodgateRcaModel}` : 'local Ollama'
      toast.success(`Investigation started (${modelLabel})`)
      setInvTitle('')
    } catch { toast.error('Failed to start investigation') }
    finally { setInvStarting(false) }
  }, [invTitle, invSeverity, invNs, invCluster, invService, invPod, floodgateRcaToken, floodgateRcaModel, loadInvestigations, rcaSubTab])

  const triggerRcaTraining = useCallback(async () => {
    setRcaTraining(true)
    try {
      await fetch('/api/v1/rca/model/train', { method: 'POST' })
      toast.success('Model training triggered')
    } catch { toast.error('Training failed') }
    finally { setTimeout(() => setRcaTraining(false), 3000) }
  }, [])

  const runFloodgateRca = useCallback(async (inv: any) => {
    if (!floodgateRcaToken.trim()) {
      toast.error('Enter your Floodgate token in the Model tab first')
      setRcaSubTab('model')
      return
    }
    if (!inv.incident_id) {
      toast.error('This investigation has no linked incident ID')
      return
    }
    setFloodgateRunningFor(inv.id)
    try {
      await incidentsApi.floodgateRCA(inv.incident_id, {
        model: floodgateRcaModel,
        token: floodgateRcaToken,
      })
      toast.success(`Floodgate RCA completed for "${inv.alert_title}"`)
      await loadInvestigations()
    } catch (e: any) {
      toast.error(e?.response?.data?.message || 'Floodgate RCA failed')
    } finally {
      setFloodgateRunningFor(null)
    }
  }, [floodgateRcaToken, floodgateRcaModel, loadInvestigations])

  // ── Effects ─────────────────────────────────────────────────────────────
  useEffect(() => {
    loadOverview()
    loadCorrelations()
  }, [loadOverview, loadCorrelations])

  useEffect(() => {
    if (intervalRef.current) clearInterval(intervalRef.current)
    if (activeTab === 'correlations' || activeTab === 'overview') {
      intervalRef.current = setInterval(loadCorrelations, 15000)
    }
    return () => { if (intervalRef.current) clearInterval(intervalRef.current) }
  }, [activeTab, loadCorrelations])

  // Load KubeSense situations when tab activates (or on mount for overview)
  const loadKsSituations = useCallback(async () => {
    setSituationsLoading(true)
    try {
      const token = sessionStorage.getItem('access_token') || localStorage.getItem('access_token')
      const hdr: HeadersInit = token ? { Authorization: `Bearer ${token}` } : {}
      const [sitRes, statusRes, statsRes] = await Promise.allSettled([
        fetch('/api/v1/kubesense/correlation/incidents', { headers: hdr }).then(r => r.json()),
        fetch('/api/v1/kubesense/correlation/status', { headers: hdr }).then(r => r.json()),
        fetch('/api/v1/kubesense/db/stats', { headers: hdr }).then(r => r.json()),
      ])
      if (sitRes.status === 'fulfilled') setKsSituations(sitRes.value?.incidents ?? [])
      if (statusRes.status === 'fulfilled') setKsCorrelStatus(statusRes.value)
      if (statsRes.status === 'fulfilled') setKsDbStats(statsRes.value)
    } catch { /* silent */ }
    setSituationsLoading(false)
  }, [])

  useEffect(() => {
    if (activeTab === 'situations' || activeTab === 'overview') {
      loadKsSituations()
    }
  }, [activeTab, loadKsSituations])

  useEffect(() => {
    if (activeTab === 'workflows') loadWorkflows()
    if (activeTab === 'rca') {
      loadInvestigations()
      loadRcaModel()
      const interval = setInterval(loadInvestigations, 10000)
      return () => clearInterval(interval)
    }
    if (activeTab === 'intelligence') {
      intelligenceApi.getStats()
        .then(r => { const d = r.data?.data ?? r.data; setIntelligenceStats(d?.stats ?? d) })
        .catch(() => {})
      intelligenceApi.getRecentRemediations('proposed')
        .then(r => { const d = r.data?.data ?? r.data; setPendingRemediations(d?.remediations ?? []) })
        .catch(() => {})
    }
  }, [activeTab, loadWorkflows, loadInvestigations, loadRcaModel])

  // Load policies and runbooks when switching to those sub-tabs
  useEffect(() => {
    if (activeTab !== 'intelligence') return
    if (intelSubTab === 'policies' && !policiesLoaded) {
      intelligenceApi.listPolicies()
        .then(r => { const d = r.data?.data ?? r.data; setPolicies(d?.policies ?? []); setPoliciesLoaded(true) })
        .catch(() => {})
    }
    if (intelSubTab === 'runbooks' && !runbooksLoaded) {
      intelligenceApi.listRunbooks()
        .then(r => { const d = r.data?.data ?? r.data; setRunbooks(d?.runbooks ?? []); setRunbooksLoaded(true) })
        .catch(() => {})
    }
  }, [activeTab, intelSubTab, policiesLoaded, runbooksLoaded])

  const reloadPolicies = () => {
    setPoliciesLoaded(false)
    intelligenceApi.listPolicies()
      .then(r => { const d = r.data?.data ?? r.data; setPolicies(d?.policies ?? []); setPoliciesLoaded(true) })
      .catch(() => {})
  }
  const reloadRunbooks = () => {
    setRunbooksLoaded(false)
    intelligenceApi.listRunbooks()
      .then(r => { const d = r.data?.data ?? r.data; setRunbooks(d?.runbooks ?? []); setRunbooksLoaded(true) })
      .catch(() => {})
  }
  const runPolicyTest = async () => {
    let labels: any = {}
    try { labels = JSON.parse(policyTestForm.labels) } catch { return }
    setPolicyTestLoading(true)
    try {
      const r = await intelligenceApi.evaluatePolicy({ ...policyTestForm, labels })
      const d = r.data?.data ?? r.data
      setPolicyTestResult(d)
    } catch { setPolicyTestResult(null) }
    setPolicyTestLoading(false)
  }

  const handleFeedback = async (alertId: string, type: string, dominantStrategy: string) => {
    try {
      await correlationApi.recordFeedback({
        alert_id: alertId,
        feedback_type: type as any,
        dominant_strategy: dominantStrategy,
      })
      setFeedbackSent(s => new Set([...s, alertId]))
    } catch {
      // silent
    }
  }

  const filteredResults = pipelineResults.filter(r => {
    const matchDecision = decisionFilter === 'all' || r.decision === decisionFilter
    const matchSource = sourceFilter === 'all' || r.alert_source?.toLowerCase().includes(sourceFilter.toLowerCase())
    const matchSearch = !searchQuery ||
      r.alert_title?.toLowerCase().includes(searchQuery.toLowerCase()) ||
      r.alert_source?.toLowerCase().includes(searchQuery.toLowerCase()) ||
      r.dominant_strategy?.toLowerCase().includes(searchQuery.toLowerCase())
    return matchDecision && matchSource && matchSearch
  })

  const monitorCandidates = pipelineResults.filter(r => r.decision === 'monitor')
    .sort((a, b) => b.final_score - a.final_score)

  const pipeline = dashboard?.pipeline
  const activeRcaCount = invs.filter(i => !['completed', 'failed'].includes(i.phase)).length

  const navItems: Array<{ id: Tab; label: string; icon: React.ComponentType<any>; color: string; count?: number; badge?: string }> = [
    { id: 'overview',      label: 'Overview',      icon: Activity,    color: a.blue },
    // Situations: the BigPanda/Davis AI "Problems" view — KubeSense + AlertHub in one place
    { id: 'situations',    label: 'Situations',    icon: Layers,      color: '#FF6B35', count: ksSituations.filter((s: any) => s.phase !== 'Resolved').length || undefined, badge: 'NEW' },
    { id: 'correlations',  label: 'Correlations',  icon: Link2,       color: a.purple, count: pipelineResults.length },
    { id: 'predictions',   label: 'Predictions',   icon: TrendingUp,  color: a.green,  count: monitorCandidates.length },
    { id: 'clusters',      label: 'Alert Clusters',icon: GitBranch,   color: a.orange, count: clusters.length },
    { id: 'fatigue',       label: 'Alert Fatigue', icon: Target,      color: a.red },
    { id: 'workflows',     label: 'Workflows',     icon: Zap,         color: a.purple, count: workflows.filter(w => w.enabled).length },
    { id: 'rca',           label: 'RCA',           icon: Brain,       color: a.indigo, count: activeRcaCount || undefined },
    { id: 'intelligence',  label: 'Intelligence',  icon: Shield,      color: a.purple, count: pendingRemediations.length || undefined },
  ]

  return (
    <div style={{ minHeight: '100vh', background: a.bg }}>
      <div style={{
        display: 'flex', flexDirection: !isDesktop ? 'column' : 'row',
        maxWidth: 1280, margin: '0 auto',
        padding: isMobile ? '16px 12px' : '24px 16px',
        gap: !isDesktop ? 16 : 28, minHeight: '100vh',
      }}>
        {/* ── Sidebar ── */}
        <div style={{
          position: !isDesktop ? 'static' : 'sticky', top: 24,
          alignSelf: 'flex-start', width: !isDesktop ? '100%' : 220,
        }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '4px 12px 20px' }}>
            <span style={{ fontSize: 26, fontWeight: 700, color: a.label, letterSpacing: '-0.02em' }}>AIOps</span>
            <div style={{ display: 'flex', alignItems: 'center', gap: 4, padding: '2px 6px', borderRadius: 8, background: 'rgba(0,122,255,0.1)' }}>
              <div style={{ width: 6, height: 6, borderRadius: '50%', background: a.blue, animation: 'pulse 2s infinite' }} />
              <span style={{ fontSize: 10, fontWeight: 600, color: a.blue, textTransform: 'uppercase', letterSpacing: '0.5px' }}>Live</span>
            </div>
          </div>

          <nav style={{ padding: '4px 0' }}>
            {navItems.map(item => {
              const active = item.id === activeTab
              const Icon = item.icon
              return (
                <button key={item.id} onClick={() => setActiveTab(item.id)} style={{
                  display: 'flex', alignItems: 'center', gap: 10, width: '100%',
                  padding: '7px 12px', borderRadius: a.r.sm, border: 'none', cursor: 'pointer',
                  background: active ? 'rgba(0,122,255,0.12)' : 'transparent', transition: 'background 0.15s',
                  marginBottom: 1, textAlign: 'left',
                }}
                  onMouseEnter={e => { if (!active) (e.currentTarget as HTMLElement).style.background = a.fill }}
                  onMouseLeave={e => { if (!active) (e.currentTarget as HTMLElement).style.background = 'transparent' }}
                >
                  <div style={{ width: 26, height: 26, borderRadius: a.r.xs + 2, background: item.color, display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 }}>
                    <Icon style={{ width: 13, height: 13, color: '#fff' }} />
                  </div>
                  <span style={{ fontSize: 13, fontWeight: active ? 600 : 400, color: active ? a.blue : a.label, flex: 1 }}>
                    {item.label}
                  </span>
                  {(item as any).badge && (
                    <span style={{ fontSize: 9, fontWeight: 700, color: '#FF6B35', background: '#FF6B3515', padding: '1px 5px', borderRadius: 6, letterSpacing: '0.3px' }}>
                      {(item as any).badge}
                    </span>
                  )}
                  {item.count !== undefined && item.count > 0 && (
                    <span style={{ fontSize: 11, fontWeight: 500, color: a.tertiaryLabel, background: a.fill, padding: '1px 7px', borderRadius: 10 }}>
                      {item.count}
                    </span>
                  )}
                </button>
              )
            })}
          </nav>

          {pipelineStats && Object.keys(pipelineStats.by_strategy).length > 0 && (
            <div style={{ margin: '16px 12px 0', padding: '10px', background: a.fill, borderRadius: a.r.md }}>
              <div style={{ fontSize: 10, fontWeight: 600, color: a.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.4px', marginBottom: 8 }}>
                Strategy Dominance
              </div>
              {Object.entries(pipelineStats.by_strategy).map(([strat, cnt]) => (
                <div key={strat} style={{ display: 'flex', alignItems: 'center', gap: 5, marginBottom: 4 }}>
                  <div style={{ width: 6, height: 6, borderRadius: '50%', background: strategyColors[strat] ?? a.gray, flexShrink: 0 }} />
                  <span style={{ fontSize: 11, color: a.label, flex: 1, textTransform: 'capitalize' }}>{strat}</span>
                  <span style={{ fontSize: 11, fontWeight: 600, color: strategyColors[strat] ?? a.gray }}>{cnt}</span>
                </div>
              ))}
            </div>
          )}
        </div>

        {/* ── Main content ── */}
        <div style={{ flex: 1, minWidth: 0 }}>
          {/* Header */}
          <div style={{ marginBottom: 16 }}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 12 }}>
              <div>
                <h1 style={{ fontSize: 20, fontWeight: 700, color: a.label, margin: 0 }}>AI-Powered Operations</h1>
                <p style={{ fontSize: 12, color: a.secondaryLabel, marginTop: 2 }}>
                  Dynatrace → Kafka → 4-strategy parallel correlation → intelligent incident management
                </p>
              </div>
              <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                {(activeTab === 'correlations' || activeTab === 'overview') && liveCount > 0 && (
                  <div style={{ display: 'flex', alignItems: 'center', gap: 4, fontSize: 11, color: a.green }}>
                    <div style={{ width: 6, height: 6, borderRadius: '50%', background: a.green, animation: 'pulse 1.5s infinite' }} />
                    Live • refreshes every 15s
                  </div>
                )}
                {(activeTab === 'overview' || activeTab === 'correlations' || activeTab === 'predictions' || activeTab === 'clusters' || activeTab === 'fatigue') && (
                  <>
                    <select value={timeRange} onChange={e => setTimeRange(e.target.value)} style={{
                      height: 34, borderRadius: a.r.md, border: 'none', background: a.fill,
                      padding: '0 22px 0 10px', fontSize: 12, color: a.label, outline: 'none', appearance: 'none', cursor: 'pointer',
                    }}>
                      <option value="6">Last 6 Hours</option>
                      <option value="24">Last 24 Hours</option>
                      <option value="72">Last 3 Days</option>
                      <option value="168">Last 7 Days</option>
                    </select>
                    <button onClick={() => { loadOverview(); loadCorrelations() }} disabled={loading} style={{
                      display: 'flex', alignItems: 'center', gap: 5, padding: '7px 12px',
                      borderRadius: a.r.sm, border: `0.5px solid ${a.separator}`, background: a.fill,
                      color: a.label, fontSize: 12, fontWeight: 500, cursor: loading ? 'default' : 'pointer', opacity: loading ? 0.5 : 1,
                    }}>
                      <RefreshCw style={{ width: 13, height: 13, ...(loading && { animation: 'spin 1s linear infinite' }) }} />
                      Refresh
                    </button>
                  </>
                )}
              </div>
            </div>

            {error && (
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: 10, marginBottom: 12, background: `${a.red}12`, border: `0.5px solid ${a.red}30`, borderRadius: a.r.sm }}>
                <AlertCircle style={{ width: 14, height: 14, color: a.red, flexShrink: 0 }} />
                <span style={{ fontSize: 12, color: a.red, flex: 1 }}>{error}</span>
                <button onClick={() => setError(null)} style={{ background: 'none', border: 'none', cursor: 'pointer', padding: 2, color: a.red }}>
                  <X style={{ width: 12, height: 12 }} />
                </button>
              </div>
            )}
          </div>

          {/* Tab content */}
          <AnimatePresence mode="wait">
            <motion.div key={activeTab} initial={{ opacity: 0, y: 6 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0, y: -6 }} transition={{ duration: 0.18 }}>

              {/* ── OVERVIEW ── */}
              {activeTab === 'overview' && (
                <div>
                  {loading ? (
                    <div style={{ textAlign: 'center', padding: '60px 0' }}>
                      <Loader2 style={{ width: 28, height: 28, color: a.blue, margin: '0 auto 10px', animation: 'spin 1s linear infinite' }} />
                      <p style={{ fontSize: 13, color: a.secondaryLabel }}>Loading pipeline data…</p>
                    </div>
                  ) : (
                    <>
                      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(160px, 1fr))', gap: 10, marginBottom: 20 }}>
                        <StatCard icon={Zap} iconColor={a.blue} label="Alerts Processed" value={pipeline?.total_processed ?? pipelineStats?.total_processed ?? 0} sub={`in last ${hours}h`} />
                        <StatCard icon={AlertCircle} iconColor={a.red} label="Incidents Created" value={pipeline?.total_incidents_created ?? pipelineStats?.total_incidents_created ?? 0} sub="auto-created" />
                        <StatCard icon={Link2} iconColor={a.blue} label="Merged" value={pipeline?.total_merged ?? pipelineStats?.total_merged ?? 0} sub="correlated into existing" />
                        <StatCard icon={Eye} iconColor={a.orange} label="Monitoring" value={pipeline?.total_monitored ?? pipelineStats?.total_monitored ?? 0} sub="borderline alerts" />
                        <StatCard icon={Filter} iconColor={a.gray} label="Noise Filtered" value={`${pipeline?.noise_reduction_rate ?? pipelineStats?.noise_reduction_rate ?? 0}%`} sub="merged + monitored + discarded" />
                        <StatCard icon={Activity} iconColor={a.green} label="Avg Score" value={`${Math.round((pipeline?.avg_score ?? pipelineStats?.avg_score ?? 0) * 100)}%`} sub="mean correlation confidence" />
                      </div>

                      {/* Domain distribution + strategy breakdown row */}
                      {(() => {
                        const byDomain = pipeline?.by_domain ?? {}
                        const byStrategy = pipeline?.by_strategy ?? pipelineStats?.by_strategy ?? {}
                        const hasDomain = Object.keys(byDomain).length > 0
                        const hasStrategy = Object.keys(byStrategy).length > 0
                        if (!hasDomain && !hasStrategy) return null
                        return (
                          <div style={{ display: 'grid', gridTemplateColumns: hasDomain && hasStrategy ? '1fr 1fr' : '1fr', gap: 10, marginBottom: 16 }}>
                            {hasDomain && (
                              <div style={{ background: a.card, border: `0.5px solid ${a.separator}`, borderRadius: a.r.lg, padding: 14 }}>
                                <div style={{ fontSize: 12, fontWeight: 600, color: a.label, marginBottom: 10 }}>Alert Domains (V2 Ontology)</div>
                                {Object.entries(byDomain).sort((x, y) => y[1] - x[1]).map(([domain, cnt]) => {
                                  const total = Object.values(byDomain).reduce((s, n) => s + n, 0)
                                  const pct = total > 0 ? (cnt / total) * 100 : 0
                                  const color = domainColors[domain] ?? a.gray
                                  return (
                                    <div key={domain} style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
                                      <span style={{ width: 8, height: 8, borderRadius: '50%', background: color, flexShrink: 0, display: 'inline-block' }} />
                                      <span style={{ fontSize: 11, color: a.label, flex: 1, textTransform: 'capitalize' }}>{domain}</span>
                                      <div style={{ width: 60, height: 4, background: a.fill, borderRadius: 2, overflow: 'hidden' }}>
                                        <div style={{ width: `${pct}%`, height: '100%', background: color, borderRadius: 2 }} />
                                      </div>
                                      <span style={{ fontSize: 10, color: a.tertiaryLabel, width: 24, textAlign: 'right' }}>{cnt}</span>
                                    </div>
                                  )
                                })}
                              </div>
                            )}
                            {hasStrategy && (
                              <div style={{ background: a.card, border: `0.5px solid ${a.separator}`, borderRadius: a.r.lg, padding: 14 }}>
                                <div style={{ fontSize: 12, fontWeight: 600, color: a.label, marginBottom: 10 }}>Dominant Strategies</div>
                                {Object.entries(byStrategy).sort((x, y) => y[1] - x[1]).map(([strat, cnt]) => {
                                  const total = Object.values(byStrategy).reduce((s, n) => s + n, 0)
                                  const pct = total > 0 ? (cnt / total) * 100 : 0
                                  const color = strategyColors[strat] ?? a.gray
                                  return (
                                    <div key={strat} style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
                                      <span style={{ width: 8, height: 8, borderRadius: '50%', background: color, flexShrink: 0, display: 'inline-block' }} />
                                      <span style={{ fontSize: 11, color: a.label, flex: 1, textTransform: 'capitalize' }}>{strat.replace(/_/g, ' ')}</span>
                                      <div style={{ width: 60, height: 4, background: a.fill, borderRadius: 2, overflow: 'hidden' }}>
                                        <div style={{ width: `${pct}%`, height: '100%', background: color, borderRadius: 2 }} />
                                      </div>
                                      <span style={{ fontSize: 10, color: a.tertiaryLabel, width: 24, textAlign: 'right' }}>{cnt}</span>
                                    </div>
                                  )
                                })}
                              </div>
                            )}
                          </div>
                        )
                      })()}

                      {dashboard?.hourly_activity && dashboard.hourly_activity.length > 0 && (                        <div style={{ background: a.card, border: `0.5px solid ${a.separator}`, borderRadius: a.r.lg, padding: 16, marginBottom: 16 }}>
                          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 12 }}>
                            <span style={{ fontSize: 13, fontWeight: 600, color: a.label }}>Pipeline Activity (Last 24h)</span>
                            <span style={{ fontSize: 11, color: a.tertiaryLabel }}>
                              {dashboard.hourly_activity.reduce((s, b) => s + b.count, 0)} total alerts
                            </span>
                          </div>
                          <ResponsiveContainer width="100%" height={90}>
                            <BarChart data={dashboard.hourly_activity} margin={{ top: 0, right: 0, left: -28, bottom: 0 }} barSize={6}>
                              <CartesianGrid strokeDasharray="3 3" stroke={`rgba(142,142,147,0.08)`} vertical={false} />
                              <XAxis dataKey="hour" tick={{ fontSize: 9, fill: 'rgba(142,142,147,0.6)' }} tickLine={false} axisLine={false} interval={5} />
                              <YAxis tick={{ fontSize: 9, fill: 'rgba(142,142,147,0.6)' }} tickLine={false} axisLine={false} allowDecimals={false} />
                              <Tooltip
                                contentStyle={{ background: 'var(--color-card)', border: `0.5px solid rgba(142,142,147,0.2)`, borderRadius: 8, fontSize: 11 }}
                                labelStyle={{ color: 'var(--color-text-secondary)', fontWeight: 600 }}
                                itemStyle={{ color: '#007AFF' }}
                                formatter={(v: number) => [v, 'Alerts']}
                              />
                              <Bar dataKey="count" fill="#007AFF" radius={[2, 2, 0, 0]} fillOpacity={0.8} />
                            </BarChart>
                          </ResponsiveContainer>
                        </div>
                      )}

                      {dashboard?.recent_incidents && dashboard.recent_incidents.length > 0 && (
                        <div style={{ background: a.card, border: `0.5px solid ${a.separator}`, borderRadius: a.r.lg, padding: 16, marginBottom: 16 }}>
                          <div style={{ fontSize: 13, fontWeight: 600, color: a.label, marginBottom: 12 }}>Recent Auto-Created Incidents</div>
                          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                            {dashboard.recent_incidents.map(inc => (
                              <a key={inc.id} href="#" onClick={e => { e.preventDefault(); navigate('/incidents', { state: { highlightId: inc.id } }) }} style={{ textDecoration: 'none', display: 'flex', alignItems: 'center', gap: 10, padding: '8px 10px', background: a.fill, borderRadius: a.r.sm }}>
                                <div style={{ width: 8, height: 8, borderRadius: '50%', background: severityColor(inc.severity), flexShrink: 0 }} />
                                <span style={{ fontSize: 12, color: a.label, flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{inc.title}</span>
                                <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 4, background: inc.status === 'open' ? `${a.red}15` : `${a.green}15`, color: inc.status === 'open' ? a.red : a.green }}>{inc.status}</span>
                                <span style={{ fontSize: 10, color: a.tertiaryLabel, flexShrink: 0 }}>{fmtDate(inc.created_at)}</span>
                              </a>
                            ))}
                          </div>
                        </div>
                      )}

                      {pipelineResults.length > 0 && (
                        <div style={{ background: a.card, border: `0.5px solid ${a.separator}`, borderRadius: a.r.lg, padding: 16 }}>
                          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 12 }}>
                            <span style={{ fontSize: 13, fontWeight: 600, color: a.label }}>Recent Correlations</span>
                            <button onClick={() => setActiveTab('correlations')} style={{ display: 'flex', alignItems: 'center', gap: 4, background: 'none', border: 'none', cursor: 'pointer', fontSize: 11, color: a.blue }}>
                              View all <ArrowRight style={{ width: 11, height: 11 }} />
                            </button>
                          </div>
                          {pipelineResults.slice(0, 5).map(r => {
                            const dc = decisionConfig[r.decision] ?? decisionConfig.discard
                            return (
                              <div key={r.id} style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '6px 0', borderBottom: `0.5px solid ${a.separator}` }}>
                                <span style={{ fontSize: 10, fontWeight: 600, padding: '1px 7px', borderRadius: 10, background: dc.bg, color: dc.color, flexShrink: 0 }}>{dc.label}</span>
                                <span style={{ fontSize: 12, color: a.label, flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{r.alert_title}</span>
                                <span style={{ fontSize: 10, padding: '1px 5px', borderRadius: 4, background: `${sourceColor(r.alert_source)}18`, color: sourceColor(r.alert_source), flexShrink: 0 }}>{r.alert_source}</span>
                                <span style={{ fontSize: 10, color: a.tertiaryLabel, flexShrink: 0 }}>{Math.round(r.final_score * 100)}%</span>
                              </div>
                            )
                          })}
                        </div>
                      )}

                      {!pipelineStats && !pipeline && (
                        <div style={{ textAlign: 'center', padding: '60px 20px' }}>
                          <Cpu style={{ width: 48, height: 48, color: a.quaternaryLabel, margin: '0 auto 12px' }} />
                          <p style={{ fontSize: 14, fontWeight: 500, color: a.secondaryLabel }}>No pipeline data yet</p>
                          <p style={{ fontSize: 12, color: a.tertiaryLabel }}>Send a Dynatrace webhook to start correlation</p>
                        </div>
                      )}
                    </>
                  )}
                </div>
              )}

              {/* ── SITUATIONS (unified KubeSense + AlertHub — Davis AI / BigPanda style) ── */}
              {activeTab === 'situations' && (
                <div>
                  {/* Platform header */}
                  <div style={{ marginBottom: 20, padding: '14px 18px', background: 'linear-gradient(135deg, #FF6B3508, #FF9F4308)', borderRadius: 14, border: '0.5px solid #FF6B3525' }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 10 }}>
                      <Layers style={{ width: 18, height: 18, color: '#FF6B35' }} />
                      <span style={{ fontSize: 15, fontWeight: 700, color: a.label }}>Active Situations</span>
                      <span style={{ fontSize: 11, color: a.tertiaryLabel }}>— Moogsoft-style unified incident view (KubeSense + AlertHub)</span>
                    </div>
                    {/* Noise reduction stats */}
                    <div style={{ display: 'flex', gap: 20, flexWrap: 'wrap' as const }}>
                      {ksCorrelStatus && (
                        <>
                          <div style={{ textAlign: 'center' }}>
                            <div style={{ fontSize: 22, fontWeight: 700, color: '#FF6B35' }}>{ksCorrelStatus.active_incidents ?? 0}</div>
                            <div style={{ fontSize: 11, color: a.tertiaryLabel }}>Situations</div>
                          </div>
                          <div style={{ textAlign: 'center' }}>
                            <div style={{ fontSize: 22, fontWeight: 700, color: a.blue }}>{ksCorrelStatus.buffer_len ?? 0}</div>
                            <div style={{ fontSize: 11, color: a.tertiaryLabel }}>Events (15-min window)</div>
                          </div>
                          <div style={{ textAlign: 'center' }}>
                            <div style={{ fontSize: 22, fontWeight: 700, color: a.green }}>{ksCorrelStatus.rule_count ?? 0}</div>
                            <div style={{ fontSize: 11, color: a.tertiaryLabel }}>Correlation Rules</div>
                          </div>
                          {ksDbStats && (
                            <div style={{ textAlign: 'center' }}>
                              <div style={{ fontSize: 22, fontWeight: 700, color: a.purple }}>
                                {ksCorrelStatus.active_incidents > 0 && ksDbStats.health_events_total > 0
                                  ? `${Math.round((1 - ksCorrelStatus.active_incidents / Math.max(1, ksDbStats.health_events_total / 1000)) * 100)}%`
                                  : '—'}
                              </div>
                              <div style={{ fontSize: 11, color: a.tertiaryLabel }}>Noise Reduction</div>
                            </div>
                          )}
                        </>
                      )}
                    </div>
                  </div>

                  {/* Situations list */}
                  {situationsLoading ? (
                    <div style={{ textAlign: 'center', padding: 40, color: a.tertiaryLabel }}>Loading situations…</div>
                  ) : ksSituations.length === 0 ? (
                    <div style={{ textAlign: 'center', padding: 60 }}>
                      <Layers style={{ width: 40, height: 40, color: a.tertiaryLabel, margin: '0 auto 12px' }} />
                      <div style={{ fontSize: 15, fontWeight: 600, color: a.secondaryLabel, marginBottom: 6 }}>No active situations</div>
                      <div style={{ fontSize: 13, color: a.tertiaryLabel }}>All clear — the correlation engine is monitoring {ksCorrelStatus?.buffer_len ?? 0} events in real-time</div>
                    </div>
                  ) : (
                    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                      {ksSituations.map((sit: any) => {
                        const phaseColor = sit.phase === 'Active' ? a.red : sit.phase === 'Detecting' ? a.orange : a.green
                        const sevColor = sit.severity === 'P1' ? a.red : sit.severity === 'P2' ? a.orange : sit.severity === 'P3' ? a.yellow : a.green
                        const isAutoDetected = sit.rule_name?.startsWith('auto.')
                        const isChangeCorr = sit.correlation_method === 'change_correlation'
                        return (
                          <div key={sit.id || sit.fingerprint} style={{ padding: 16, background: a.card, borderRadius: 14, border: `0.5px solid ${phaseColor}30`, boxShadow: '0 1px 4px rgba(0,0,0,0.04)' }}>
                            <div style={{ display: 'flex', alignItems: 'flex-start', gap: 12 }}>
                              {/* Phase badge */}
                              <div style={{ fontSize: 10, padding: '3px 8px', borderRadius: 8, background: `${phaseColor}18`, color: phaseColor, fontWeight: 700, flexShrink: 0, marginTop: 2 }}>
                                {sit.phase ?? 'Active'}
                              </div>
                              <div style={{ flex: 1, minWidth: 0 }}>
                                {/* Title row */}
                                <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6, flexWrap: 'wrap' as const }}>
                                  <span style={{ fontWeight: 700, fontSize: 14, color: a.label }}>{sit.incident_type ?? sit.type ?? 'Correlation'}</span>
                                  <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 6, background: `${sevColor}18`, color: sevColor, fontWeight: 700 }}>{sit.severity ?? '—'}</span>
                                  {isChangeCorr && <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 6, background: `${a.purple}18`, color: a.purple, fontWeight: 700 }}>⚡ CHANGE RCA</span>}
                                  {isAutoDetected && <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 6, background: `${a.green}18`, color: a.green, fontWeight: 700 }}>🧠 AUTO LEARNED</span>}
                                </div>
                                {/* Summary */}
                                {sit.summary && <div style={{ fontSize: 13, color: a.secondaryLabel, marginBottom: 6 }}>{sit.summary}</div>}
                                {/* Meta */}
                                <div style={{ display: 'flex', gap: 14, fontSize: 11, color: a.tertiaryLabel, flexWrap: 'wrap' as const }}>
                                  {sit.namespace && <span>ns: <strong style={{ color: a.secondaryLabel }}>{sit.namespace}</strong></span>}
                                  {sit.resource_name && <span>resource: <strong style={{ color: a.secondaryLabel }}>{sit.resource_name}</strong></span>}
                                  {sit.signal_count != null && <span><strong style={{ color: a.label }}>{sit.signal_count}</strong> signals</span>}
                                  {sit.rule_name && <span>rule: {sit.rule_name.replace('auto.', '').replace(/-/g, ' ')}</span>}
                                  {sit.first_observed_at && <span>{new Date(sit.first_observed_at).toLocaleTimeString()}</span>}
                                </div>
                              </div>
                            </div>
                          </div>
                        )
                      })}
                    </div>
                  )}

                  {/* Bridge to K8s explorer */}
                  <div style={{ marginTop: 20, padding: '12px 16px', background: a.fill, borderRadius: 10, border: `0.5px solid ${a.separator}`, display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                    <span style={{ fontSize: 13, color: a.secondaryLabel }}>Full K8s intelligence: health events, violations, chaos scores, topology</span>
                    <button onClick={() => navigate('/kubesense')} style={{ fontSize: 13, color: a.blue, background: 'none', border: 'none', cursor: 'pointer', fontWeight: 600 }}>
                      Open K8s Intelligence →
                    </button>
                  </div>
                </div>
              )}

              {/* ── CORRELATIONS ── */}
              {activeTab === 'correlations' && (
                <div>
                  <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', marginBottom: 14 }}>
                    <div style={{ position: 'relative', flex: '1 1 180px', minWidth: 150 }}>
                      <Search style={{ position: 'absolute', left: 9, top: '50%', transform: 'translateY(-50%)', width: 13, height: 13, color: a.tertiaryLabel, pointerEvents: 'none' }} />
                      <input type="text" value={searchQuery} onChange={e => setSearchQuery(e.target.value)} placeholder="Search correlations…"
                        style={{ width: '100%', height: 34, borderRadius: a.r.md, border: 'none', background: a.fill, paddingLeft: 30, paddingRight: 10, fontSize: 12, color: a.label, outline: 'none', boxSizing: 'border-box' }}
                        onFocus={e => (e.target.style.boxShadow = `0 0 0 3px rgba(0,122,255,0.2)`)}
                        onBlur={e => (e.target.style.boxShadow = 'none')}
                      />
                    </div>
                    <select value={decisionFilter} onChange={e => setDecisionFilter(e.target.value)} style={{ height: 34, borderRadius: a.r.md, border: 'none', background: a.fill, padding: '0 20px 0 10px', fontSize: 12, color: a.label, outline: 'none', appearance: 'none', cursor: 'pointer' }}>
                      <option value="all">All Decisions</option>
                      <option value="create_incident">Incident Created</option>
                      <option value="merge_incident">Merged</option>
                      <option value="monitor">Monitoring</option>
                      <option value="discard">Discarded</option>
                    </select>
                    <select value={sourceFilter} onChange={e => setSourceFilter(e.target.value)} style={{ height: 34, borderRadius: a.r.md, border: 'none', background: a.fill, padding: '0 20px 0 10px', fontSize: 12, color: a.label, outline: 'none', appearance: 'none', cursor: 'pointer' }}>
                      <option value="all">All Sources</option>
                      {Object.keys(pipelineStats?.by_source ?? {}).map(src => (
                        <option key={src} value={src}>{src} ({pipelineStats!.by_source[src]})</option>
                      ))}
                    </select>
                    {correlationsLoading && (
                      <Loader2 style={{ width: 14, height: 14, color: a.blue, animation: 'spin 1s linear infinite', flexShrink: 0 }} />
                    )}
                    <span style={{ fontSize: 11, color: a.tertiaryLabel, marginLeft: 'auto' }}>
                      {filteredResults.length} result{filteredResults.length !== 1 ? 's' : ''}
                    </span>
                  </div>

                  {pipelineStats && (
                    <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', marginBottom: 14 }}>
                      {[
                        { key: 'create_incident', label: 'Incidents', val: pipelineStats.total_incidents_created },
                        { key: 'merge_incident',  label: 'Merged',    val: pipelineStats.total_merged },
                        { key: 'monitor',         label: 'Monitor',   val: pipelineStats.total_monitored },
                        { key: 'discard',         label: 'Discarded', val: pipelineStats.total_discarded },
                      ].map(chip => {
                        const dc = decisionConfig[chip.key]
                        return (
                          <button key={chip.key}
                            onClick={() => setDecisionFilter(decisionFilter === chip.key ? 'all' : chip.key)}
                            style={{
                              display: 'flex', alignItems: 'center', gap: 5, padding: '4px 10px', borderRadius: 20,
                              border: `0.5px solid ${decisionFilter === chip.key ? dc.color : 'transparent'}`,
                              background: decisionFilter === chip.key ? dc.bg : a.fill,
                              color: decisionFilter === chip.key ? dc.color : a.secondaryLabel,
                              fontSize: 11, fontWeight: 500, cursor: 'pointer',
                            }}
                          >
                            {chip.label}
                            <span style={{ fontWeight: 700 }}>{chip.val}</span>
                          </button>
                        )
                      })}
                    </div>
                  )}

                  {/* RCE Stage breakdown — shows which pipeline stage fired per result */}
                  {filteredResults.length > 0 && (() => {
                    const stage1 = filteredResults.filter(r => r.dominant_strategy === 'critical_fast_path').length
                    const stage2 = filteredResults.filter(r => r.dominant_strategy === 'topology').length
                    const stage3 = filteredResults.filter(r => r.dominant_strategy !== 'critical_fast_path' && r.dominant_strategy !== 'topology').length
                    return (
                      <div style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '8px 12px', background: a.fill, borderRadius: a.r.md, marginBottom: 14, flexWrap: 'wrap' }}>
                        <span style={{ fontSize: 10, fontWeight: 600, color: a.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.4px', marginRight: 4 }}>RCE Stage</span>
                        {[
                          { label: 'S1 · DT Root', value: stage1, color: a.orange },
                          { label: 'S2 · Topology', value: stage2, color: a.teal },
                          { label: 'S3 · Scoring', value: stage3, color: a.purple },
                        ].map(({ label, value, color }) => (
                          <span key={label} style={{ display: 'flex', alignItems: 'center', gap: 4, padding: '2px 8px', borderRadius: 12, background: `${color}15`, fontSize: 11 }}>
                            <span style={{ fontWeight: 700, color }}>{value}</span>
                            <span style={{ color: a.secondaryLabel }}>{label}</span>
                          </span>
                        ))}
                        <span style={{ fontSize: 10, color: a.tertiaryLabel, marginLeft: 'auto' }}>
                          S1 = Dynatrace root entity · S2 = topology graph · S3 = 4-strategy scoring
                        </span>
                      </div>
                    )
                  })()}

                  {filteredResults.length === 0 && !correlationsLoading ? (
                    <div style={{ textAlign: 'center', padding: '60px 20px' }}>
                      <Layers style={{ width: 48, height: 48, color: a.quaternaryLabel, margin: '0 auto 12px' }} />
                      <p style={{ fontSize: 14, fontWeight: 500, color: a.secondaryLabel }}>
                        {pipelineResults.length === 0 ? 'No correlation results yet' : 'No results match your filters'}
                      </p>
                      <p style={{ fontSize: 12, color: a.tertiaryLabel, marginTop: 4 }}>
                        {pipelineResults.length === 0 ? 'Send a Dynatrace alert to POST /api/v1/webhooks/dynatrace' : 'Try adjusting filters'}
                      </p>
                    </div>
                  ) : (
                    <div>
                      {filteredResults.map(r => (
                        <ResultCard key={r.id} r={r} onFeedback={handleFeedback} feedbackSent={feedbackSent} />
                      ))}
                    </div>
                  )}

                  {/* K8s Infrastructure Intelligence — unified from KubeSense */}
                  {(ksCorrelStatus || ksDbStats) && (
                    <div style={{ marginTop: 20, padding: '14px 18px', background: 'linear-gradient(135deg, #FF6B3506, #5AC8FA06)', borderRadius: 14, border: '0.5px solid #FF6B3520' }}>
                      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 12 }}>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                          <Layers style={{ width: 15, height: 15, color: '#FF6B35' }} />
                          <span style={{ fontSize: 13, fontWeight: 700, color: a.label }}>K8s Infrastructure Intelligence</span>
                        </div>
                        <button onClick={() => setActiveTab('situations')} style={{ fontSize: 12, color: '#FF6B35', background: 'none', border: 'none', cursor: 'pointer', fontWeight: 600 }}>
                          View Situations →
                        </button>
                      </div>
                      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(130px, 1fr))', gap: 8 }}>
                        {ksCorrelStatus && <>
                          <div style={{ textAlign: 'center', padding: '8px 4px' }}>
                            <div style={{ fontSize: 20, fontWeight: 700, color: '#FF6B35' }}>{ksCorrelStatus.active_incidents ?? 0}</div>
                            <div style={{ fontSize: 11, color: a.tertiaryLabel }}>Active Situations</div>
                          </div>
                          <div style={{ textAlign: 'center', padding: '8px 4px' }}>
                            <div style={{ fontSize: 20, fontWeight: 700, color: a.blue }}>{ksCorrelStatus.buffer_len?.toLocaleString() ?? 0}</div>
                            <div style={{ fontSize: 11, color: a.tertiaryLabel }}>Events Buffered</div>
                          </div>
                          <div style={{ textAlign: 'center', padding: '8px 4px' }}>
                            <div style={{ fontSize: 20, fontWeight: 700, color: a.purple }}>{ksCorrelStatus.rule_count ?? 0}</div>
                            <div style={{ fontSize: 11, color: a.tertiaryLabel }}>Correlation Rules</div>
                          </div>
                        </>}
                        {ksDbStats && <>
                          <div style={{ textAlign: 'center', padding: '8px 4px' }}>
                            <div style={{ fontSize: 20, fontWeight: 700, color: a.red }}>{ksDbStats.violations_total?.toLocaleString() ?? 0}</div>
                            <div style={{ fontSize: 11, color: a.tertiaryLabel }}>Config Violations</div>
                          </div>
                          <div style={{ textAlign: 'center', padding: '8px 4px' }}>
                            <div style={{ fontSize: 20, fontWeight: 700, color: a.orange }}>{ksDbStats.chaos_scores_total?.toLocaleString() ?? 0}</div>
                            <div style={{ fontSize: 11, color: a.tertiaryLabel }}>Chaos Scores</div>
                          </div>
                        </>}
                      </div>
                    </div>
                  )}
                </div>
              )}

              {/* ── PREDICTIONS ── */}
              {activeTab === 'predictions' && (
                <div>
                  <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', marginBottom: 14, gap: 10 }}>
                    <div>
                      <h3 style={{ fontSize: 15, fontWeight: 600, color: a.label, margin: 0 }}>Escalation Risk</h3>
                      <p style={{ fontSize: 12, color: a.secondaryLabel, marginTop: 3 }}>
                        Alerts currently in "monitor" state — borderline correlation scores that could escalate
                      </p>
                    </div>
                    {monitorCandidates.some(r => !['resolved', 'closed'].includes(r.alert_status ?? '')) && (
                      <button
                        onClick={processMonitoredAlerts}
                        disabled={processingMonitored}
                        style={{
                          padding: '6px 14px', borderRadius: a.r.sm, fontSize: 12, fontWeight: 500, cursor: processingMonitored ? 'wait' : 'pointer',
                          border: `0.5px solid ${a.blue}50`, background: `${a.blue}15`, color: a.blue,
                          flexShrink: 0, whiteSpace: 'nowrap',
                        }}
                      >
                        {processingMonitored ? 'Processing…' : `Process ${monitorCandidates.filter(r => !['resolved','closed'].includes(r.alert_status ?? '')).length} Open`}
                      </button>
                    )}
                  </div>

                  {monitorCandidates.length === 0 ? (
                    <div style={{ textAlign: 'center', padding: '60px 20px' }}>
                      <Telescope style={{ width: 48, height: 48, color: a.quaternaryLabel, margin: '0 auto 12px' }} />
                      <p style={{ fontSize: 14, fontWeight: 500, color: a.secondaryLabel }}>No alerts in monitor state</p>
                      <p style={{ fontSize: 12, color: a.tertiaryLabel, marginTop: 4 }}>Good signal — the pipeline isn't holding any borderline alerts</p>
                    </div>
                  ) : (
                    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                      {monitorCandidates.map(r => {
                        const isResolved = ['resolved', 'closed'].includes(r.alert_status ?? '')
                        return (
                          <div key={r.id} style={{ background: a.card, border: `0.5px solid ${isResolved ? a.separator : `${a.orange}30`}`, borderRadius: a.r.lg, padding: 14, opacity: isResolved ? 0.6 : 1 }}>
                            <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 10, marginBottom: 10 }}>
                              <div style={{ flex: 1, minWidth: 0 }}>
                                <div style={{ fontSize: 13, fontWeight: 600, color: isResolved ? a.secondaryLabel : a.label, marginBottom: 4, textDecoration: isResolved ? 'line-through' : 'none' }}>{r.alert_title}</div>
                                <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
                                  <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 4, background: `${sourceColor(r.alert_source)}18`, color: sourceColor(r.alert_source) }}>{r.alert_source}</span>
                                  <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 4, background: `${severityColor(r.alert_severity)}15`, color: severityColor(r.alert_severity) }}>{r.alert_severity}</span>
                                  {r.alert_status && (
                                    <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 4, background: isResolved ? `${a.gray}20` : `${a.orange}15`, color: isResolved ? a.tertiaryLabel : a.orange, textTransform: 'capitalize' }}>
                                      {r.alert_status}
                                    </span>
                                  )}
                                  <span style={{ fontSize: 10, color: a.tertiaryLabel }}>{fmtDate(r.processed_at)}</span>
                                </div>
                              </div>
                              <div style={{ textAlign: 'center', flexShrink: 0 }}>
                                <div style={{ fontSize: 20, fontWeight: 700, color: isResolved ? a.gray : r.final_score >= 0.5 ? a.orange : a.gray }}>
                                  {Math.round(r.final_score * 100)}%
                                </div>
                                <div style={{ fontSize: 9, color: a.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.3px' }}>Risk Score</div>
                              </div>
                            </div>

                            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '3px 12px', marginBottom: 10 }}>
                              <ScoreBar label="semantic"  value={r.semantic_score}  color={strategyColors.semantic} />
                              <ScoreBar label="temporal"  value={r.temporal_score}  color={strategyColors.temporal} />
                              <ScoreBar label="topology"  value={r.topology_score}  color={strategyColors.topology} />
                              <ScoreBar label="rules"     value={r.rules_score}     color={strategyColors.rules} />
                            </div>

                            {r.reasoning && <p style={{ fontSize: 11, color: a.secondaryLabel, margin: 0, lineHeight: 1.4 }}>{r.reasoning}</p>}

                            {!isResolved && (
                              <div style={{ display: 'flex', gap: 8, marginTop: 10 }}>
                                <button
                                  onClick={() => handleFeedback(r.alert_id, 'missed_correlation', r.dominant_strategy)}
                                  disabled={feedbackSent.has(r.alert_id)}
                                  style={{
                                    padding: '5px 12px', borderRadius: a.r.sm, fontSize: 11, fontWeight: 500, cursor: 'pointer',
                                    border: `0.5px solid ${a.orange}50`, background: `${a.orange}12`, color: a.orange,
                                    opacity: feedbackSent.has(r.alert_id) ? 0.5 : 1,
                                  }}
                                >
                                  {feedbackSent.has(r.alert_id) ? '✓ Escalated' : 'Mark as Missed Correlation'}
                                </button>
                              </div>
                            )}
                          </div>
                        )
                      })}
                    </div>
                  )}
                </div>
              )}

              {/* ── CLUSTERS ── */}
              {activeTab === 'clusters' && (
                <div>
                  <div style={{ marginBottom: 14 }}>
                    <h3 style={{ fontSize: 15, fontWeight: 600, color: a.label, margin: 0 }}>Alert Clusters</h3>
                    <p style={{ fontSize: 12, color: a.secondaryLabel, marginTop: 3 }}>AI-grouped alerts based on similarity and timing</p>
                  </div>
                  {clusters.length > 0 ? (
                    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                      {clusters.map(cluster => (
                        <div key={cluster.id} style={{ background: a.card, border: `0.5px solid ${a.separator}`, borderRadius: a.r.lg, padding: 14 }}>
                          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 8 }}>
                            <div style={{ display: 'flex', alignItems: 'center', gap: 7 }}>
                              <GitBranch style={{ width: 14, height: 14, color: a.orange }} />
                              <h4 style={{ fontSize: 13, fontWeight: 600, color: a.label, margin: 0 }}>{cluster.name}</h4>
                            </div>
                            <span style={{ fontSize: 10, padding: '2px 8px', borderRadius: 12, background: cluster.status === 'active' ? `${a.green}15` : `${a.gray}15`, color: cluster.status === 'active' ? a.green : a.gray }}>
                              {cluster.status}
                            </span>
                          </div>
                          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 12, fontSize: 12 }}>
                            <div><span style={{ color: a.tertiaryLabel }}>Alerts</span><div style={{ fontWeight: 600, color: a.label }}>{cluster.alerts_count}</div></div>
                            <div>
                              <span style={{ color: a.tertiaryLabel }}>Confidence</span>
                              <div style={{ fontWeight: 600, color: cluster.confidence >= 0.8 ? a.green : cluster.confidence >= 0.6 ? a.yellow : a.red }}>{pct(cluster.confidence)}</div>
                            </div>
                            <div><span style={{ color: a.tertiaryLabel }}>Type</span><div style={{ fontWeight: 600, color: a.label }}>{cluster.cluster_type}</div></div>
                          </div>
                        </div>
                      ))}
                    </div>
                  ) : (
                    <div style={{ textAlign: 'center', padding: '60px 20px' }}>
                      <GitBranch style={{ width: 48, height: 48, color: a.quaternaryLabel, margin: '0 auto 12px' }} />
                      <p style={{ fontSize: 14, fontWeight: 500, color: a.secondaryLabel }}>No alert clusters found</p>
                      <p style={{ fontSize: 12, color: a.tertiaryLabel }}>Clusters appear when similar alerts are detected</p>
                    </div>
                  )}
                </div>
              )}

              {/* ── FATIGUE ── */}
              {activeTab === 'fatigue' && (
                <div>
                  <div style={{ marginBottom: 14 }}>
                    <h3 style={{ fontSize: 15, fontWeight: 600, color: a.label, margin: 0 }}>Alert Fatigue Analysis</h3>
                    <p style={{ fontSize: 12, color: a.secondaryLabel, marginTop: 3 }}>Alert noise and quality metrics</p>
                  </div>
                  {pipelineStats && (
                    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(160px, 1fr))', gap: 10, marginBottom: 16 }}>
                      <StatCard icon={Filter} iconColor={a.green} label="Noise Reduction" value={`${pipelineStats.noise_reduction_rate}%`} sub="alerts not creating new incidents" />
                      <StatCard icon={Zap} iconColor={a.blue} label="Avg Response" value={`${Math.round(pipelineStats.avg_elapsed_ms)}ms`} sub="pipeline processing time" />
                      <StatCard icon={BarChart3} iconColor={a.purple} label="Avg Confidence" value={`${Math.round(pipelineStats.avg_score * 100)}%`} sub="mean correlation score" />
                    </div>
                  )}
                  {fatigueAnalysis ? (
                    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
                      <div style={{ background: a.card, border: `0.5px solid ${a.separator}`, borderRadius: a.r.lg, padding: 16 }}>
                        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 10 }}>
                          <h4 style={{ fontSize: 14, fontWeight: 600, color: a.label, margin: 0 }}>Overall Fatigue Score</h4>
                          <div style={{ fontSize: 22, fontWeight: 700, color: fatigueAnalysis.score > 0.7 ? a.red : fatigueAnalysis.score > 0.4 ? a.yellow : a.green }}>
                            {Math.round(fatigueAnalysis.score * 100)}%
                          </div>
                        </div>
                        <div style={{ width: '100%', height: 8, background: a.fill, borderRadius: 4, overflow: 'hidden' }}>
                          <div style={{ width: `${fatigueAnalysis.score * 100}%`, height: '100%', background: `linear-gradient(90deg, ${a.green}, ${a.yellow}, ${a.red})`, transition: 'width 0.3s' }} />
                        </div>
                      </div>
                      {(fatigueAnalysis.factors || []).length > 0 && (
                        <div style={{ background: a.card, border: `0.5px solid ${a.separator}`, borderRadius: a.r.lg, padding: 16 }}>
                          <h4 style={{ fontSize: 14, fontWeight: 600, color: a.label, margin: 0, marginBottom: 10 }}>Contributing Factors</h4>
                          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                            {fatigueAnalysis.factors.map((f, i) => (
                              <div key={i} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '7px 10px', background: a.fill, borderRadius: a.r.sm }}>
                                <div>
                                  <div style={{ fontSize: 12, fontWeight: 500, color: a.label }}>{f.name}</div>
                                  <div style={{ fontSize: 11, color: a.secondaryLabel }}>{f.description}</div>
                                </div>
                                <div style={{ fontSize: 12, fontWeight: 600, color: f.impact > 0.7 ? a.red : f.impact > 0.4 ? a.yellow : a.green }}>{Math.round(f.impact * 100)}%</div>
                              </div>
                            ))}
                          </div>
                        </div>
                      )}
                      {(fatigueAnalysis.recommendations || []).length > 0 && (
                        <div style={{ background: a.card, border: `0.5px solid ${a.separator}`, borderRadius: a.r.lg, padding: 16 }}>
                          <h4 style={{ fontSize: 14, fontWeight: 600, color: a.label, margin: 0, marginBottom: 10 }}>Recommendations</h4>
                          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                            {fatigueAnalysis.recommendations.map((rec, i) => (
                              <div key={i} style={{ display: 'flex', alignItems: 'flex-start', gap: 8, padding: '7px 10px', background: `${a.blue}10`, border: `0.5px solid ${a.blue}25`, borderRadius: a.r.sm }}>
                                <Lightbulb style={{ width: 13, height: 13, color: a.blue, marginTop: 1, flexShrink: 0 }} />
                                <div style={{ fontSize: 12, color: a.label, lineHeight: 1.45 }}>{rec}</div>
                              </div>
                            ))}
                          </div>
                        </div>
                      )}
                    </div>
                  ) : (
                    <div style={{ textAlign: 'center', padding: '60px 20px' }}>
                      <Target style={{ width: 48, height: 48, color: a.quaternaryLabel, margin: '0 auto 12px' }} />
                      <p style={{ fontSize: 14, fontWeight: 500, color: a.secondaryLabel }}>No fatigue analysis available</p>
                      <p style={{ fontSize: 12, color: a.tertiaryLabel }}>Analysis appears when sufficient alert data is collected</p>
                    </div>
                  )}
                </div>
              )}

              {/* ── WORKFLOWS ── */}
              {activeTab === 'workflows' && (
                <div>
                  {/* Intelligent templates — shown when no workflows exist */}
                  {workflows.length === 0 && !workflowsLoading && (
                    <div style={{ background: `${a.purple}06`, border: `0.5px solid ${a.purple}20`, borderRadius: a.r.lg, padding: 16, marginBottom: 16 }}>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12 }}>
                        <Zap style={{ width: 13, height: 13, color: a.purple }} />
                        <span style={{ fontSize: 12, fontWeight: 600, color: a.label }}>Intelligent Workflow Templates</span>
                        <span style={{ fontSize: 11, color: a.secondaryLabel }}>One-click deploy pre-built automations</span>
                      </div>
                      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(220px, 1fr))', gap: 10 }}>
                        {INTELLIGENT_TEMPLATES.map(tpl => (
                          <div key={tpl.id} style={{ background: a.card, border: `0.5px solid ${a.separator}`, borderRadius: a.r.md, padding: 12 }}>
                            <div style={{ width: 8, height: 8, borderRadius: '50%', background: tpl.color, marginBottom: 6 }} />
                            <div style={{ fontSize: 12, fontWeight: 600, color: a.label, marginBottom: 4 }}>{tpl.name}</div>
                            <div style={{ fontSize: 11, color: a.secondaryLabel, lineHeight: 1.4, marginBottom: 10 }}>{tpl.description}</div>
                            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                              <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 4, background: `${tpl.color}15`, color: tpl.color }}>{tpl.category}</span>
                              <button
                                onClick={() => deployTemplate(tpl)}
                                style={{ display: 'flex', alignItems: 'center', gap: 4, padding: '4px 10px', borderRadius: a.r.xs, border: 'none', background: tpl.color, color: '#fff', fontSize: 11, fontWeight: 600, cursor: 'pointer' }}
                              >
                                <Plus style={{ width: 10, height: 10 }} /> Deploy
                              </button>
                            </div>
                          </div>
                        ))}
                      </div>
                    </div>
                  )}

                  {/* Toolbar */}
                  <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 14 }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                      <div style={{ position: 'relative' }}>
                        <Search style={{ position: 'absolute', left: 9, top: '50%', transform: 'translateY(-50%)', width: 13, height: 13, color: a.tertiaryLabel, pointerEvents: 'none' }} />
                        <input
                          type="text" value={wfSearchQuery} onChange={e => setWfSearchQuery(e.target.value)}
                          placeholder="Search workflows…"
                          style={{ height: 34, borderRadius: a.r.md, border: 'none', background: a.fill, paddingLeft: 30, paddingRight: 12, fontSize: 12, color: a.label, outline: 'none', width: 200 }}
                        />
                      </div>
                      {workflowsLoading && <Loader2 style={{ width: 14, height: 14, color: a.blue, animation: 'spin 1s linear infinite' }} />}
                    </div>
                    <div style={{ display: 'flex', gap: 8 }}>
                      <button onClick={loadWorkflows} style={{ display: 'flex', alignItems: 'center', gap: 5, padding: '6px 12px', borderRadius: a.r.sm, border: `0.5px solid ${a.separator}`, background: a.fill, color: a.label, fontSize: 12, cursor: 'pointer' }}>
                        <RefreshCw style={{ width: 12, height: 12 }} /> Refresh
                      </button>
                      <button onClick={() => setShowCreateWf(true)} style={{ display: 'flex', alignItems: 'center', gap: 5, padding: '6px 14px', borderRadius: a.r.sm, border: 'none', background: a.purple, color: '#fff', fontSize: 12, fontWeight: 600, cursor: 'pointer' }}>
                        <Plus style={{ width: 12, height: 12 }} /> New Workflow
                      </button>
                    </div>
                  </div>

                  {/* Workflow list */}
                  {workflowsLoading ? (
                    <div style={{ textAlign: 'center', padding: '60px 0' }}>
                      <Loader2 style={{ width: 28, height: 28, color: a.purple, margin: '0 auto 10px', animation: 'spin 1s linear infinite' }} />
                      <p style={{ fontSize: 13, color: a.secondaryLabel }}>Loading workflows…</p>
                    </div>
                  ) : workflows.length > 0 ? (
                    <>
                      {/* Quick-deploy strip when workflows already exist */}
                      <div style={{ marginBottom: 14 }}>
                        <div style={{ fontSize: 10, fontWeight: 600, color: a.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.4px', marginBottom: 6 }}>Quick-Deploy Templates</div>
                        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
                          {INTELLIGENT_TEMPLATES.map(tpl => (
                            <button key={tpl.id} onClick={() => deployTemplate(tpl)} style={{
                              display: 'flex', alignItems: 'center', gap: 5, padding: '4px 10px', borderRadius: 20,
                              border: `0.5px solid ${tpl.color}40`, background: `${tpl.color}0D`,
                              color: tpl.color, fontSize: 11, fontWeight: 500, cursor: 'pointer',
                            }}>
                              <Plus style={{ width: 10, height: 10 }} /> {tpl.name}
                            </button>
                          ))}
                        </div>
                      </div>

                      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))', gap: 12 }}>
                        {workflows
                          .filter(w => !wfSearchQuery || w.name?.toLowerCase().includes(wfSearchQuery.toLowerCase()) || w.description?.toLowerCase().includes(wfSearchQuery.toLowerCase()))
                          .map(wf => (
                            <div key={wf.id} style={{
                              background: a.card, borderRadius: a.r.lg, padding: 14,
                              border: `0.5px solid ${selectedWf?.id === wf.id ? a.purple : a.separator}`,
                              boxShadow: selectedWf?.id === wf.id ? `0 0 0 2px ${a.purple}30` : 'none',
                              cursor: 'pointer',
                            }} onClick={() => { setSelectedWf(wf === selectedWf ? null : wf); loadWfExecs(wf.id) }}>
                              <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', marginBottom: 8 }}>
                                <div style={{ flex: 1, minWidth: 0 }}>
                                  <div style={{ fontSize: 14, fontWeight: 600, color: a.label, marginBottom: 2, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{wf.name}</div>
                                  <div style={{ fontSize: 12, color: a.secondaryLabel, lineHeight: 1.3 }}>{wf.description}</div>
                                </div>
                                <div style={{
                                  display: 'flex', alignItems: 'center', gap: 4, padding: '2px 7px', borderRadius: 12, flexShrink: 0, marginLeft: 8,
                                  background: wf.enabled ? `${a.green}15` : `rgba(142,142,147,0.12)`,
                                  fontSize: 10, fontWeight: 500, color: wf.enabled ? a.green : a.gray,
                                }}>
                                  {wf.enabled ? <CheckCircle style={{ width: 10, height: 10 }} /> : <Pause style={{ width: 10, height: 10 }} />}
                                  {wf.enabled ? 'Active' : 'Paused'}
                                </div>
                              </div>
                              <div style={{ display: 'flex', gap: 5, marginBottom: 10, flexWrap: 'wrap' }}>
                                {wf.triggers?.[0]?.type && (
                                  <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 4, background: `${a.blue}12`, color: a.blue }}>
                                    {wf.triggers[0].type} trigger
                                  </span>
                                )}
                                {wf.steps?.length > 0 && (
                                  <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 4, background: a.fill, color: a.secondaryLabel }}>
                                    {wf.steps.length} step{wf.steps.length !== 1 ? 's' : ''}
                                  </span>
                                )}
                                {(wf.tags || []).slice(0, 2).map((tag: string) => (
                                  <span key={tag} style={{ fontSize: 10, padding: '1px 6px', borderRadius: 4, background: `${a.purple}0D`, color: a.purple }}>{tag}</span>
                                ))}
                              </div>
                              <div style={{ fontSize: 11, color: a.tertiaryLabel, marginBottom: 10, display: 'flex', justifyContent: 'space-between' }}>
                                <span>{wf.executions || 0} executions</span>
                                {wf.last_run && <span>Last: {new Date(wf.last_run).toLocaleDateString()}</span>}
                              </div>
                              <div style={{ display: 'flex', gap: 6 }}>
                                <button onClick={e => { e.stopPropagation(); executeWf(wf) }} disabled={!wf.enabled} style={{
                                  flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 4,
                                  padding: '5px 10px', borderRadius: a.r.sm, border: 'none',
                                  background: wf.enabled ? a.blue : a.fill, color: wf.enabled ? '#fff' : a.tertiaryLabel,
                                  fontSize: 11, fontWeight: 500, cursor: wf.enabled ? 'pointer' : 'not-allowed', opacity: wf.enabled ? 1 : 0.5,
                                }}>
                                  <Play style={{ width: 11, height: 11 }} /> Execute
                                </button>
                                <button onClick={e => { e.stopPropagation(); toggleWf(wf) }} style={{ padding: '5px 8px', borderRadius: a.r.sm, border: `0.5px solid ${a.separator}`, background: a.fill, color: a.label, cursor: 'pointer' }}>
                                  {wf.enabled ? <Pause style={{ width: 13, height: 13 }} /> : <Play style={{ width: 13, height: 13 }} />}
                                </button>
                                <button onClick={e => { e.stopPropagation(); deleteWf(wf) }} style={{ padding: '5px 8px', borderRadius: a.r.sm, border: `0.5px solid ${a.red}30`, background: `${a.red}0D`, color: a.red, cursor: 'pointer' }}>
                                  <Trash2 style={{ width: 13, height: 13 }} />
                                </button>
                              </div>
                            </div>
                          ))}
                      </div>

                      {/* Executions panel */}
                      {selectedWf && (
                        <div style={{ marginTop: 16, background: a.card, border: `0.5px solid ${a.purple}30`, borderRadius: a.r.lg, padding: 16 }}>
                          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 12 }}>
                            <span style={{ fontSize: 13, fontWeight: 600, color: a.label }}>Executions — {selectedWf.name}</span>
                            <button onClick={() => loadWfExecs(selectedWf.id)} style={{ padding: '4px 8px', borderRadius: a.r.sm, border: `0.5px solid ${a.separator}`, background: a.fill, color: a.label, fontSize: 11, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 4 }}>
                              <RefreshCw style={{ width: 11, height: 11 }} />
                            </button>
                          </div>
                          {wfExecsLoading ? (
                            <div style={{ textAlign: 'center', padding: '16px 0' }}>
                              <Loader2 style={{ width: 18, height: 18, color: a.purple, margin: '0 auto', animation: 'spin 1s linear infinite' }} />
                            </div>
                          ) : wfExecs.length > 0 ? (
                            <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                              {wfExecs.slice(0, 10).map(exec => {
                                const sc = exec.status === 'completed' ? a.green : exec.status === 'failed' ? a.red : exec.status === 'running' ? a.blue : a.gray
                                return (
                                  <div key={exec.id} style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '7px 10px', background: a.fill, borderRadius: a.r.sm }}>
                                    <span style={{ fontSize: 10, fontWeight: 600, padding: '1px 7px', borderRadius: 10, background: `${sc}15`, color: sc }}>{exec.status}</span>
                                    <span style={{ fontSize: 11, color: a.secondaryLabel, flex: 1 }}>{new Date(exec.started_at).toLocaleString()}</span>
                                    {exec.duration && <span style={{ fontSize: 10, color: a.tertiaryLabel }}>{exec.duration}</span>}
                                    {exec.error && <span style={{ fontSize: 10, color: a.red }}>{exec.error}</span>}
                                  </div>
                                )
                              })}
                            </div>
                          ) : (
                            <div style={{ textAlign: 'center', padding: '16px 0', fontSize: 12, color: a.tertiaryLabel }}>No executions yet — click Execute to run the workflow</div>
                          )}
                        </div>
                      )}
                    </>
                  ) : (
                    <div style={{ textAlign: 'center', padding: '40px 20px' }}>
                      <GitBranch style={{ width: 40, height: 40, color: a.quaternaryLabel, margin: '0 auto 12px' }} />
                      <p style={{ fontSize: 14, fontWeight: 500, color: a.secondaryLabel }}>No workflows yet</p>
                      <p style={{ fontSize: 12, color: a.tertiaryLabel }}>Deploy a template above or create a custom workflow</p>
                    </div>
                  )}

                  {/* Create Workflow Modal */}
                  {showCreateWf && (
                    <div style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.4)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 100, backdropFilter: 'blur(8px)' }}>
                      <motion.div initial={{ scale: 0.95, opacity: 0 }} animate={{ scale: 1, opacity: 1 }} style={{ background: a.card, borderRadius: a.r.xl, padding: 24, width: '90%', maxWidth: 480, boxShadow: '0 20px 60px rgba(0,0,0,0.2)', maxHeight: '90vh', overflowY: 'auto' }}>
                        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 20 }}>
                          <h3 style={{ fontSize: 17, fontWeight: 700, color: a.label, margin: 0 }}>Create Workflow</h3>
                          <button onClick={() => setShowCreateWf(false)} style={{ background: 'none', border: 'none', cursor: 'pointer', color: a.tertiaryLabel, padding: 4 }}>
                            <X style={{ width: 16, height: 16 }} />
                          </button>
                        </div>
                        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                          <div>
                            <label style={{ fontSize: 11, fontWeight: 600, color: a.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.4px', display: 'block', marginBottom: 6 }}>Name *</label>
                            <input value={wfForm.name} onChange={e => setWfForm(f => ({ ...f, name: e.target.value }))}
                              placeholder="e.g. Critical Alert Escalation"
                              style={{ width: '100%', padding: '9px 12px', borderRadius: a.r.sm, border: `0.5px solid ${a.separator}`, background: a.fill, color: a.label, fontSize: 13, outline: 'none', boxSizing: 'border-box' }}
                              onFocus={e => (e.target.style.boxShadow = `0 0 0 3px ${a.purple}30`)}
                              onBlur={e => (e.target.style.boxShadow = 'none')}
                            />
                          </div>
                          <div>
                            <label style={{ fontSize: 11, fontWeight: 600, color: a.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.4px', display: 'block', marginBottom: 6 }}>Description</label>
                            <input value={wfForm.description} onChange={e => setWfForm(f => ({ ...f, description: e.target.value }))}
                              placeholder="What does this workflow do?"
                              style={{ width: '100%', padding: '9px 12px', borderRadius: a.r.sm, border: `0.5px solid ${a.separator}`, background: a.fill, color: a.label, fontSize: 13, outline: 'none', boxSizing: 'border-box' }}
                            />
                          </div>
                          <div>
                            <label style={{ fontSize: 11, fontWeight: 600, color: a.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.4px', display: 'block', marginBottom: 6 }}>Trigger Type</label>
                            <select value={wfForm.triggerType} onChange={e => setWfForm(f => ({ ...f, triggerType: e.target.value }))}
                              style={{ width: '100%', padding: '9px 12px', borderRadius: a.r.sm, border: `0.5px solid ${a.separator}`, background: a.fill, color: a.label, fontSize: 13, outline: 'none', appearance: 'none' }}>
                              <option value="alert">Alert Trigger</option>
                              <option value="schedule">Scheduled (Cron)</option>
                              <option value="manual">Manual</option>
                              <option value="webhook">Webhook</option>
                            </select>
                          </div>
                          {wfForm.triggerType === 'alert' && (
                            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
                              <div>
                                <label style={{ fontSize: 11, fontWeight: 600, color: a.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.4px', display: 'block', marginBottom: 6 }}>Severity</label>
                                <select value={wfForm.severity} onChange={e => setWfForm(f => ({ ...f, severity: e.target.value }))}
                                  style={{ width: '100%', padding: '9px 12px', borderRadius: a.r.sm, border: `0.5px solid ${a.separator}`, background: a.fill, color: a.label, fontSize: 13, outline: 'none', appearance: 'none' }}>
                                  <option value="">Any</option>
                                  <option value="critical">Critical</option>
                                  <option value="high">High</option>
                                  <option value="warning">Warning</option>
                                  <option value="info">Info</option>
                                </select>
                              </div>
                              <div>
                                <label style={{ fontSize: 11, fontWeight: 600, color: a.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.4px', display: 'block', marginBottom: 6 }}>Source Filter</label>
                                <input value={wfForm.source} onChange={e => setWfForm(f => ({ ...f, source: e.target.value }))}
                                  placeholder="e.g. dynatrace"
                                  style={{ width: '100%', padding: '9px 12px', borderRadius: a.r.sm, border: `0.5px solid ${a.separator}`, background: a.fill, color: a.label, fontSize: 13, outline: 'none' }} />
                              </div>
                            </div>
                          )}
                          {wfForm.triggerType === 'schedule' && (
                            <div>
                              <label style={{ fontSize: 11, fontWeight: 600, color: a.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.4px', display: 'block', marginBottom: 6 }}>Cron Expression</label>
                              <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', marginBottom: 8 }}>
                                {[['Every 30 min', '*/30 * * * *'], ['Every hour', '0 * * * *'], ['Daily 6 AM', '0 6 * * *'], ['Weekly Mon', '0 9 * * 1']].map(([label, val]) => (
                                  <button key={val} onClick={() => setWfForm(f => ({ ...f, cron: val }))}
                                    style={{ padding: '3px 8px', borderRadius: 6, border: `0.5px solid ${wfForm.cron === val ? a.purple : a.separator}`, background: wfForm.cron === val ? `${a.purple}15` : a.fill, color: wfForm.cron === val ? a.purple : a.secondaryLabel, fontSize: 10, cursor: 'pointer' }}>
                                    {label}
                                  </button>
                                ))}
                              </div>
                              <input value={wfForm.cron} onChange={e => setWfForm(f => ({ ...f, cron: e.target.value }))}
                                placeholder="*/30 * * * *"
                                style={{ width: '100%', padding: '9px 12px', borderRadius: a.r.sm, border: `0.5px solid ${a.separator}`, background: a.fill, color: a.label, fontSize: 13, outline: 'none', fontFamily: 'monospace', boxSizing: 'border-box' }} />
                            </div>
                          )}
                          <div>
                            <label style={{ fontSize: 11, fontWeight: 600, color: a.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.4px', display: 'block', marginBottom: 6 }}>Tags (comma-separated)</label>
                            <input value={wfForm.tags} onChange={e => setWfForm(f => ({ ...f, tags: e.target.value }))}
                              placeholder="k8s, auto-remediation, critical"
                              style={{ width: '100%', padding: '9px 12px', borderRadius: a.r.sm, border: `0.5px solid ${a.separator}`, background: a.fill, color: a.label, fontSize: 13, outline: 'none', boxSizing: 'border-box' }} />
                          </div>
                        </div>
                        <div style={{ display: 'flex', gap: 10, marginTop: 20 }}>
                          <button onClick={() => setShowCreateWf(false)} style={{ flex: 1, padding: '10px 16px', borderRadius: a.r.sm, border: `0.5px solid ${a.separator}`, background: a.fill, color: a.label, fontSize: 13, fontWeight: 500, cursor: 'pointer' }}>
                            Cancel
                          </button>
                          <button onClick={createWorkflow} disabled={wfCreating || !wfForm.name.trim()} style={{ flex: 2, padding: '10px 16px', borderRadius: a.r.sm, border: 'none', background: a.purple, color: '#fff', fontSize: 13, fontWeight: 600, cursor: wfCreating || !wfForm.name.trim() ? 'not-allowed' : 'pointer', opacity: wfCreating || !wfForm.name.trim() ? 0.6 : 1, display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 6 }}>
                            {wfCreating ? <><Loader2 style={{ width: 14, height: 14, animation: 'spin 1s linear infinite' }} /> Creating…</> : <><Plus style={{ width: 14, height: 14 }} /> Create Workflow</>}
                          </button>
                        </div>
                      </motion.div>
                    </div>
                  )}
                </div>
              )}

              {/* ── RCA ── */}
              {activeTab === 'rca' && (
                <div>
                  {/* Start investigation form */}
                  <div style={{ background: a.card, border: `0.5px solid ${a.separator}`, borderRadius: a.r.lg, padding: 16, marginBottom: 20 }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12 }}>
                      <Brain style={{ width: 14, height: 14, color: a.indigo }} />
                      <span style={{ fontSize: 11, fontWeight: 600, color: a.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.4px' }}>Start Investigation</span>
                      {/* Show which LLM will be used */}
                      {floodgateRcaToken ? (
                        <span style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 4, fontSize: 10, padding: '2px 8px', borderRadius: 4, background: `${a.orange}12`, color: a.orange, fontWeight: 600 }}>
                          <Sparkles style={{ width: 9, height: 9 }} />
                          Floodgate {floodgateRcaModel === 'claude-sonnet-4-6' ? 'Sonnet 4.6' : 'Opus 4.7'}
                        </span>
                      ) : rcaModel?.current_model ? (
                        <code style={{ fontSize: 10, padding: '1px 8px', borderRadius: 4, background: `${a.indigo}12`, color: a.indigo, marginLeft: 'auto' }}>
                          {rcaModel.current_model}
                        </code>
                      ) : null}
                    </div>
                    <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
                      <input value={invTitle} onChange={e => setInvTitle(e.target.value)}
                        onKeyDown={e => e.key === 'Enter' && startInvestigation()}
                        placeholder="Alert title or incident description…"
                        style={{ flex: '1 1 280px', padding: '9px 12px', borderRadius: a.r.sm, border: `0.5px solid ${a.separator}`, background: a.fill, color: a.label, fontSize: 13, outline: 'none' }}
                        onFocus={e => (e.target.style.boxShadow = `0 0 0 3px ${a.indigo}30`)}
                        onBlur={e => (e.target.style.boxShadow = 'none')}
                      />
                      <select value={invSeverity} onChange={e => setInvSeverity(e.target.value)}
                        style={{ padding: '9px 12px', borderRadius: a.r.sm, border: `0.5px solid ${a.separator}`, background: a.fill, color: invSeverity === 'critical' ? a.red : invSeverity === 'high' ? a.orange : invSeverity === 'medium' ? a.yellow : a.green, fontSize: 13, outline: 'none', appearance: 'none' }}>
                        {['critical', 'high', 'medium', 'low'].map(s => <option key={s} value={s}>{s}</option>)}
                      </select>
                      <input value={invNs} onChange={e => setInvNs(e.target.value)} placeholder="namespace"
                        style={{ padding: '9px 12px', borderRadius: a.r.sm, border: `0.5px solid ${a.separator}`, background: a.fill, color: a.label, fontSize: 13, outline: 'none', width: 120 }} />
                      <input value={invCluster} onChange={e => setInvCluster(e.target.value)} placeholder="cluster"
                        style={{ padding: '9px 12px', borderRadius: a.r.sm, border: `0.5px solid ${a.separator}`, background: a.fill, color: a.label, fontSize: 13, outline: 'none', width: 140 }} />
                      <input value={invPod} onChange={e => setInvPod(e.target.value)} placeholder="pod (optional)"
                        style={{ padding: '9px 12px', borderRadius: a.r.sm, border: `0.5px solid ${a.separator}`, background: a.fill, color: a.label, fontSize: 13, outline: 'none', width: 140 }} />
                      <button onClick={startInvestigation} disabled={invStarting || !invTitle}
                        style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '9px 16px', borderRadius: a.r.sm, background: invStarting || !invTitle ? `${floodgateRcaToken ? a.orange : a.indigo}50` : (floodgateRcaToken ? a.orange : a.indigo), color: '#fff', border: 'none', cursor: invStarting || !invTitle ? 'not-allowed' : 'pointer', fontSize: 13, fontWeight: 600 }}>
                        {floodgateRcaToken ? <Sparkles style={{ width: 13, height: 13 }} /> : <Play style={{ width: 13, height: 13 }} />}{invStarting ? 'Starting…' : 'Investigate'}
                      </button>
                    </div>
                  </div>

                  {/* RCA Sub-tabs */}
                  <div style={{ display: 'flex', gap: 2, marginBottom: 16, background: a.fill, borderRadius: a.r.md, padding: 3, width: 'fit-content' }}>
                    {([
                      ['active', `Investigations (${invs.filter(i => !['completed','failed'].includes(i.phase)).length})`],
                      ['history', `History (${invs.filter(i => ['completed','failed'].includes(i.phase)).length})`],
                      ['knowledge', 'Knowledge Base'],
                      ['model', 'Model'],
                    ] as [typeof rcaSubTab, string][]).map(([t, label]) => (
                      <button key={t} onClick={() => setRcaSubTab(t)}
                        style={{
                          padding: '6px 14px', borderRadius: a.r.sm, border: 'none', cursor: 'pointer',
                          fontSize: 12, fontWeight: rcaSubTab === t ? 600 : 400,
                          background: rcaSubTab === t ? a.card : 'transparent',
                          color: rcaSubTab === t ? a.label : a.secondaryLabel,
                          boxShadow: rcaSubTab === t ? '0 1px 4px rgba(0,0,0,0.1)' : 'none',
                        }}>
                        {label}
                      </button>
                    ))}
                  </div>

                  <AnimatePresence mode="wait">
                    {rcaSubTab === 'active' && (
                      <motion.div key="rca-active" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}
                        style={{ display: 'grid', gridTemplateColumns: selectedInvId ? '260px 1fr' : '1fr', gap: 16, minHeight: 300 }}>
                        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                          {invs.length === 0 ? (
                            <div style={{ textAlign: 'center', padding: '40px 20px', color: a.secondaryLabel, fontSize: 13 }}>
                              No investigations yet. Start one above or wait for a critical alert.
                            </div>
                          ) : (
                            invs.map(inv => (
                              <div key={inv.id} onClick={() => setSelectedInvId(inv.id === selectedInvId ? null : inv.id)}
                                style={{
                                  padding: '12px 14px', borderRadius: a.r.md, cursor: 'pointer', transition: 'all 0.15s',
                                  border: `0.5px solid ${inv.id === selectedInvId ? a.indigo : a.separator}`,
                                  background: inv.id === selectedInvId ? `${a.indigo}08` : a.card,
                                }}>
                                <div style={{ display: 'flex', alignItems: 'flex-start', gap: 8 }}>
                                  {inv.phase === 'completed' ? (
                                    <CheckCircle style={{ width: 14, height: 14, color: a.green, flexShrink: 0, marginTop: 2 }} />
                                  ) : inv.phase === 'failed' ? (
                                    <AlertTriangle style={{ width: 14, height: 14, color: a.red, flexShrink: 0, marginTop: 2 }} />
                                  ) : (
                                    <motion.div animate={{ rotate: 360 }} transition={{ duration: 2, repeat: Infinity, ease: 'linear' }} style={{ flexShrink: 0, marginTop: 2 }}>
                                      <Activity style={{ width: 14, height: 14, color: a.blue }} />
                                    </motion.div>
                                  )}
                                  <div style={{ flex: 1, minWidth: 0 }}>
                                    <div style={{ fontSize: 13, fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: a.label }}>{inv.alert_title}</div>
                                    <div style={{ fontSize: 11, color: a.secondaryLabel, marginTop: 2, display: 'flex', gap: 8 }}>
                                      <span style={{ color: inv.severity === 'critical' ? a.red : inv.severity === 'high' ? a.orange : inv.severity === 'medium' ? a.yellow : a.green }}>{inv.severity}</span>
                                      <span>{inv.phase?.replace(/_/g, ' ')}</span>
                                      {inv.root_cause && <span style={{ color: a.green }}>{Math.round(inv.root_cause.confidence * 100)}% conf</span>}
                                    </div>
                                  </div>
                                  {/* Floodgate re-run button — only for investigations linked to an incident */}
                                  {inv.incident_id && (
                                    <button
                                      onClick={e => { e.stopPropagation(); runFloodgateRca(inv) }}
                                      disabled={floodgateRunningFor === inv.id}
                                      title={`Re-run RCA with Floodgate ${floodgateRcaModel}`}
                                      style={{
                                        display: 'flex', alignItems: 'center', gap: 4, padding: '4px 8px',
                                        borderRadius: a.r.xs, border: `0.5px solid ${a.orange}40`,
                                        background: `${a.orange}10`, color: a.orange,
                                        fontSize: 11, fontWeight: 600, cursor: floodgateRunningFor === inv.id ? 'not-allowed' : 'pointer',
                                        opacity: floodgateRunningFor === inv.id ? 0.5 : 1, flexShrink: 0,
                                      }}>
                                      {floodgateRunningFor === inv.id
                                        ? <Loader2 style={{ width: 10, height: 10 }} />
                                        : <Sparkles style={{ width: 10, height: 10 }} />}
                                      Claude
                                    </button>
                                  )}
                                </div>
                              </div>
                            ))
                          )}
                        </div>
                        {selectedInvId && (
                          <div style={{ minWidth: 0 }}>
                            <InvestigationStream investigationId={selectedInvId} />
                          </div>
                        )}
                      </motion.div>
                    )}

                    {rcaSubTab === 'history' && (
                      <motion.div key="rca-history" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}>
                        {invs.filter(i => ['completed', 'failed'].includes(i.phase)).length === 0 ? (
                          <div style={{ textAlign: 'center', padding: '60px 20px', color: a.secondaryLabel, fontSize: 13 }}>No completed investigations yet</div>
                        ) : (
                          <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                            {invs.filter(i => ['completed', 'failed'].includes(i.phase)).map(inv => (
                              <div key={inv.id} onClick={() => { setSelectedInvId(inv.id); setRcaSubTab('active') }}
                                style={{ padding: '14px 16px', borderRadius: a.r.md, border: `0.5px solid ${a.separator}`, background: a.card, cursor: 'pointer' }}>
                                <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: inv.summary ? 6 : 0 }}>
                                  {inv.phase === 'completed' ? <CheckCircle style={{ width: 14, height: 14, color: a.green }} /> : <AlertTriangle style={{ width: 14, height: 14, color: a.red }} />}
                                  <span style={{ fontSize: 13, fontWeight: 500, color: a.label, flex: 1 }}>{inv.alert_title}</span>
                                  {inv.root_cause && (
                                    <span style={{ fontSize: 11, padding: '2px 8px', borderRadius: 6, background: `${a.green}12`, color: a.green }}>
                                      {inv.root_cause.category?.replace(/_/g, ' ')} · {Math.round(inv.root_cause.confidence * 100)}%
                                    </span>
                                  )}
                                </div>
                                {inv.summary && <p style={{ fontSize: 12, color: a.secondaryLabel, margin: 0 }}>{inv.summary}</p>}
                              </div>
                            ))}
                          </div>
                        )}
                      </motion.div>
                    )}

                    {rcaSubTab === 'knowledge' && (
                      <motion.div key="rca-knowledge" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}>
                        <KnowledgeEditor />
                      </motion.div>
                    )}

                    {rcaSubTab === 'model' && (
                      <motion.div key="rca-model" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }} style={{ maxWidth: 540 }}>
                        {/* Local model card */}
                        <div style={{ background: a.card, border: `0.5px solid ${a.separator}`, borderRadius: a.r.lg, padding: 20, marginBottom: 16 }}>
                          <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 16 }}>
                            <Cpu style={{ width: 18, height: 18, color: a.indigo }} />
                            <h3 style={{ margin: 0, fontSize: 15, fontWeight: 600, color: a.label }}>Local Model</h3>
                            <code style={{ fontSize: 13, fontWeight: 600, color: a.indigo, background: `${a.indigo}10`, padding: '2px 10px', borderRadius: 6, marginLeft: 4 }}>
                              {rcaModel?.current_model || 'qwen2.5:14b'}
                            </code>
                          </div>
                          {rcaModel?.available_models && (
                            <div style={{ marginBottom: 16 }}>
                              <div style={{ fontSize: 10, fontWeight: 600, color: a.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.4px', marginBottom: 6 }}>Available Models</div>
                              {rcaModel.available_models.map((m: any, i: number) => (
                                <div key={i} style={{ fontSize: 12, padding: '3px 0', color: a.label }}><code>{m.name}</code></div>
                              ))}
                            </div>
                          )}
                          <div style={{ padding: 12, background: `${a.indigo}08`, borderRadius: a.r.sm, marginBottom: 16, fontSize: 12, color: a.secondaryLabel, lineHeight: 1.6 }}>
                            <strong style={{ color: a.label }}>How model learning works:</strong><br />
                            Confirmed investigations → Weaviate embeddings → RAG retrieval for new investigations → weekly custom Ollama model rebuild with learned patterns in system prompt.
                          </div>
                          <button onClick={triggerRcaTraining} disabled={rcaTraining}
                            style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '9px 16px', borderRadius: a.r.sm, background: rcaTraining ? `${a.indigo}60` : a.indigo, color: '#fff', border: 'none', cursor: rcaTraining ? 'not-allowed' : 'pointer', fontSize: 13, fontWeight: 600 }}>
                            <Brain style={{ width: 14, height: 14 }} />{rcaTraining ? 'Training in progress…' : 'Trigger model update'}
                          </button>
                        </div>

                        {/* Floodgate Claude card */}
                        <div style={{ background: a.card, border: `0.5px solid ${a.separator}`, borderRadius: a.r.lg, padding: 20 }}>
                          <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 6 }}>
                            <Sparkles style={{ width: 18, height: 18, color: a.orange }} />
                            <h3 style={{ margin: 0, fontSize: 15, fontWeight: 600, color: a.label }}>Floodgate Claude</h3>
                            <span style={{ fontSize: 11, padding: '2px 8px', borderRadius: 5, background: `${a.orange}15`, color: a.orange, fontWeight: 600 }}>Separate from AI Chat</span>
                          </div>
                          <p style={{ fontSize: 12, color: a.secondaryLabel, margin: '0 0 16px', lineHeight: 1.5 }}>
                            Use Claude Sonnet or Opus via Floodgate for RCA on existing incidents and investigations. Run the command below in Terminal to get your token, then paste it here.
                          </p>
                          <div style={{ padding: 10, background: 'rgba(0,0,0,0.04)', borderRadius: a.r.sm, marginBottom: 14, fontFamily: 'monospace', fontSize: 11, color: a.label, wordBreak: 'break-all', lineHeight: 1.6 }}>
                            appleconnect getToken -C hvys3fcwcteqrvw3qzkvtk86viuoqv --token-type=oauth --interactivity-type=none -E prod -G pkce -o openid,dsid,accountname,profile,groups | grep 'oauth-id-token' | awk '{`{print $2}`}'
                          </div>

                          <div style={{ marginBottom: 12 }}>
                            <div style={{ fontSize: 11, fontWeight: 600, color: a.tertiaryLabel, marginBottom: 6, textTransform: 'uppercase' }}>Claude Model</div>
                            <div style={{ display: 'flex', gap: 8 }}>
                              {([
                                ['claude-sonnet-4-6', 'Claude Sonnet 4.6'],
                                ['claude-opus-4-7',   'Claude Opus 4.7'],
                              ] as [typeof floodgateRcaModel, string][]).map(([val, label]) => (
                                <button key={val} onClick={() => setFloodgateRcaModel(val)}
                                  style={{
                                    padding: '7px 14px', borderRadius: a.r.sm, border: `0.5px solid ${floodgateRcaModel === val ? a.orange : a.separator}`,
                                    background: floodgateRcaModel === val ? `${a.orange}12` : a.fill,
                                    color: floodgateRcaModel === val ? a.orange : a.secondaryLabel,
                                    fontSize: 12, fontWeight: floodgateRcaModel === val ? 600 : 400, cursor: 'pointer',
                                  }}>
                                  {label}
                                </button>
                              ))}
                            </div>
                          </div>

                          <div style={{ marginBottom: 14 }}>
                            <div style={{ fontSize: 11, fontWeight: 600, color: a.tertiaryLabel, marginBottom: 6, textTransform: 'uppercase' }}>OAuth Token (from appleconnect)</div>
                            <div style={{ position: 'relative' }}>
                              <Key style={{ position: 'absolute', left: 10, top: '50%', transform: 'translateY(-50%)', width: 13, height: 13, color: a.tertiaryLabel }} />
                              <input
                                type="password"
                                value={floodgateRcaToken}
                                onChange={e => {
                                  setFloodgateRcaToken(e.target.value)
                                  localStorage.setItem('rca_floodgate_token', e.target.value)
                                  setFloodgateTokenStatus('idle')
                                }}
                                placeholder="Paste your OAuth ID token here…"
                                style={{
                                  width: '100%', padding: '9px 12px 9px 32px', borderRadius: a.r.sm,
                                  border: `0.5px solid ${floodgateRcaToken ? a.orange : a.separator}`,
                                  background: a.fill, color: a.label, fontSize: 13, outline: 'none',
                                  boxSizing: 'border-box',
                                }}
                              />
                            </div>
                            {floodgateRcaToken && (
                              <div style={{ marginTop: 5, fontSize: 11, color: a.green }}>
                                Token saved · stored in browser only · never sent to our servers except to proxy Floodgate
                              </div>
                            )}
                          </div>

                          {/* Test Connection button */}
                          {floodgateRcaToken && (
                            <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 14 }}>
                              <button
                                onClick={async () => {
                                  setFloodgateTokenStatus('testing')
                                  try {
                                    const r = await incidentsApi.testFloodgateToken(floodgateRcaToken)
                                    setFloodgateTokenStatus(r.data?.valid ? 'valid' : 'invalid')
                                  } catch {
                                    setFloodgateTokenStatus('invalid')
                                  }
                                }}
                                disabled={floodgateTokenStatus === 'testing'}
                                style={{
                                  display: 'flex', alignItems: 'center', gap: 6,
                                  padding: '7px 14px', borderRadius: a.r.sm,
                                  background: floodgateTokenStatus === 'testing' ? `${a.orange}40` : `${a.orange}15`,
                                  color: a.orange, border: `0.5px solid ${a.orange}40`,
                                  fontSize: 12, fontWeight: 600, cursor: floodgateTokenStatus === 'testing' ? 'not-allowed' : 'pointer',
                                }}>
                                {floodgateTokenStatus === 'testing'
                                  ? <><span style={{ display: 'inline-block', width: 12, height: 12, border: `2px solid ${a.orange}`, borderTopColor: 'transparent', borderRadius: '50%', animation: 'spin 0.8s linear infinite' }} /> Testing…</>
                                  : <><Sparkles style={{ width: 12, height: 12 }} /> Test Connection</>
                                }
                              </button>
                              {floodgateTokenStatus === 'valid' && (
                                <span style={{ fontSize: 12, color: a.green, fontWeight: 600 }}>✓ Connected to Floodgate</span>
                              )}
                              {floodgateTokenStatus === 'invalid' && (
                                <span style={{ fontSize: 12, color: a.red, fontWeight: 600 }}>✗ Token invalid or expired</span>
                              )}
                            </div>
                          )}

                          <div style={{ padding: 10, background: `${a.orange}08`, borderRadius: a.r.sm, fontSize: 12, color: a.secondaryLabel, lineHeight: 1.5 }}>
                            <strong style={{ color: a.label }}>Usage:</strong> Once the token is set, click the{' '}
                            <span style={{ color: a.orange, fontWeight: 600 }}>Sparkles</span> button on any investigation to re-run RCA with Floodgate Claude.
                            Results are stored in the incident record and shown in the Incidents page RCA tab.
                          </div>
                        </div>
                      </motion.div>
                    )}
                  </AnimatePresence>
                </div>
              )}

            </motion.div>
          </AnimatePresence>

          {/* ── Intelligence tab panel ─────────────────────────────────────── */}
          {activeTab === 'intelligence' && (
            <div style={{ padding: '4px 0' }}>
              {/* Intelligence sub-tab bar */}
              <div style={{ display: 'flex', gap: 2, marginBottom: 20, borderBottom: `0.5px solid ${a.separator}`, paddingBottom: 0 }}>
                {[
                  { id: 'overview' as IntelSubTab, label: 'Overview' },
                  { id: 'policies' as IntelSubTab, label: 'Policies' },
                  { id: 'runbooks' as IntelSubTab, label: 'Runbooks' },
                  { id: 'model' as IntelSubTab, label: 'Model' },
                  { id: 'test' as IntelSubTab, label: 'Policy Test' },
                ].map(st => (
                  <button key={st.id} onClick={() => setIntelSubTab(st.id)} style={{
                    padding: '7px 16px', border: 'none', cursor: 'pointer', fontSize: 13, fontWeight: 500,
                    background: 'transparent', borderBottom: intelSubTab === st.id ? `2px solid ${a.purple}` : '2px solid transparent',
                    color: intelSubTab === st.id ? a.purple : a.secondaryLabel, transition: 'all 0.15s', marginBottom: -1,
                  }}>
                    {st.label}
                  </button>
                ))}
              </div>

              {/* ── Overview sub-tab ─── */}
              {intelSubTab === 'overview' && (
                <div>
                  {intelligenceStats ? (
                    <div>
                      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 12, marginBottom: 20 }}>
                        {[
                          { label: 'Policies Active', value: intelligenceStats.policies_enabled, total: intelligenceStats.policies_total, color: a.purple },
                          { label: 'Runbooks', value: intelligenceStats.runbooks_total, color: a.blue },
                          { label: 'KS Health Events (24h)', value: intelligenceStats.kubesense_health_events_24h, color: a.orange },
                          { label: 'KS APM Signals (24h)', value: intelligenceStats.kubesense_apm_signals_24h, color: a.green },
                          { label: 'Postmortems Generated', value: intelligenceStats.postmortems_generated, sub: `${intelligenceStats.postmortems_llm} by LLM`, color: a.indigo },
                          { label: 'KS Config Violations (24h)', value: intelligenceStats.kubesense_config_violations_24h, color: a.red },
                          { label: 'KS Forecasts Active', value: intelligenceStats.kubesense_forecasts_active, color: a.yellow },
                          { label: 'OIE Avg Confidence (7d)', value: `${Math.round((intelligenceStats.oie_avg_confidence || 0) * 100)}%`, color: a.green },
                        ].map((s: any, i) => (
                          <div key={i} style={{ padding: '14px 16px', background: `${s.color}08`, borderRadius: 10, border: `0.5px solid ${s.color}20` }}>
                            <div style={{ fontSize: 11, color: a.secondaryLabel, marginBottom: 4, textTransform: 'uppercase' as const, letterSpacing: '0.3px' }}>{s.label}</div>
                            <div style={{ fontSize: 22, fontWeight: 700, color: s.color }}>{s.value ?? '—'}</div>
                            {s.total !== undefined && <div style={{ fontSize: 11, color: a.tertiaryLabel }}>of {s.total} total</div>}
                            {s.sub && <div style={{ fontSize: 11, color: a.tertiaryLabel }}>{s.sub}</div>}
                          </div>
                        ))}
                      </div>
                      {/* Pending gate approvals */}
                      <div>
                        <div style={{ fontSize: 14, fontWeight: 600, color: a.label, marginBottom: 10, display: 'flex', alignItems: 'center', gap: 8 }}>
                          <Shield style={{ width: 15, height: 15, color: a.purple }} />
                          Pending Gate Approvals
                          {pendingRemediations.length > 0 && (
                            <span style={{ fontSize: 11, padding: '1px 8px', borderRadius: 12, background: `${a.orange}20`, color: a.orange, fontWeight: 600 }}>
                              {pendingRemediations.length} waiting
                            </span>
                          )}
                        </div>
                        {pendingRemediations.length === 0 ? (
                          <div style={{ padding: '16px 0', color: a.tertiaryLabel, fontSize: 13 }}>No pending remediations — all gate hooks are clear.</div>
                        ) : (
                          <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                            {pendingRemediations.map((r: any) => (
                              <div key={r.id} style={{ padding: '12px 16px', background: a.fill, borderRadius: 10, border: `0.5px solid ${a.separator}` }}>
                                <div style={{ display: 'flex', alignItems: 'flex-start', gap: 10, justifyContent: 'space-between' }}>
                                  <div style={{ flex: 1 }}>
                                    <div style={{ fontSize: 12, color: a.tertiaryLabel, marginBottom: 2 }}>{r.incident_number} — {r.incident_title?.substring(0, 55)}</div>
                                    <div style={{ fontSize: 13, color: a.label, marginBottom: 4 }}>{r.proposed_action}</div>
                                    <div style={{ display: 'flex', gap: 8 }}>
                                      <span style={{ fontSize: 10, padding: '1px 7px', borderRadius: 12, background: r.risk_level === 'high' ? `${a.red}15` : `${a.orange}15`, color: r.risk_level === 'high' ? a.red : a.orange, fontWeight: 600 }}>{r.risk_level} risk</span>
                                      <span style={{ fontSize: 11, color: a.tertiaryLabel }}>by {r.proposed_by}</span>
                                    </div>
                                  </div>
                                  <div style={{ display: 'flex', gap: 6 }}>
                                    <button onClick={async () => { try { await incidentsApi.approveRemediation(r.incident_id, r.id); setPendingRemediations(p => p.filter(x => x.id !== r.id)) } catch {} }}
                                      style={{ padding: '6px 10px', borderRadius: 7, border: 'none', cursor: 'pointer', background: `${a.green}15`, color: a.green, display: 'flex', alignItems: 'center', gap: 4, fontSize: 12 }}>
                                      <Approve style={{ width: 13, height: 13 }} /> Approve
                                    </button>
                                    <button onClick={async () => { try { await incidentsApi.rejectRemediation(r.incident_id, r.id); setPendingRemediations(p => p.filter(x => x.id !== r.id)) } catch {} }}
                                      style={{ padding: '6px 10px', borderRadius: 7, border: 'none', cursor: 'pointer', background: `${a.red}10`, color: a.red, display: 'flex', alignItems: 'center', gap: 4, fontSize: 12 }}>
                                      <Reject style={{ width: 13, height: 13 }} /> Reject
                                    </button>
                                  </div>
                                </div>
                              </div>
                            ))}
                          </div>
                        )}
                      </div>
                    </div>
                  ) : (
                    <div style={{ textAlign: 'center', padding: 40 }}>
                      <Loader2 style={{ width: 24, height: 24, color: a.blue, margin: '0 auto 10px' }} />
                      <div style={{ fontSize: 13, color: a.secondaryLabel }}>Loading intelligence stats…</div>
                    </div>
                  )}
                </div>
              )}

              {/* ── Policies sub-tab ─── */}
              {intelSubTab === 'policies' && (
                <div>
                  <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 16 }}>
                    <div>
                      <div style={{ fontSize: 14, fontWeight: 600, color: a.label }}>Intelligence Policies</div>
                      <div style={{ fontSize: 12, color: a.secondaryLabel, marginTop: 2 }}>DB-driven suppression rules replacing hardcoded filters</div>
                    </div>
                    <button onClick={() => setShowAddPolicy(!showAddPolicy)}
                      style={{ padding: '7px 14px', borderRadius: 8, background: a.purple, color: '#fff', border: 'none', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 6, fontSize: 13 }}>
                      <Plus style={{ width: 14, height: 14 }} /> Add Policy
                    </button>
                  </div>
                  {showAddPolicy && (
                    <div style={{ marginBottom: 16, padding: 14, background: a.fill, borderRadius: 10, border: `0.5px solid ${a.separator}` }}>
                      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginBottom: 10 }}>
                        <input placeholder="Policy name*" value={policyForm.name} onChange={e => setPolicyForm(f => ({ ...f, name: e.target.value }))}
                          style={{ padding: '8px 12px', borderRadius: 8, border: `1px solid ${a.separator}`, background: 'var(--color-background)', fontSize: 13, color: a.label }} />
                        <select value={policyForm.policy_type} onChange={e => setPolicyForm(f => ({ ...f, policy_type: e.target.value }))}
                          style={{ padding: '8px 12px', borderRadius: 8, border: `1px solid ${a.separator}`, background: 'var(--color-background)', fontSize: 13, color: a.label }}>
                          {['suppress_alert','suppress_incident','skip_rca','require_approval','auto_resolve'].map(t => (
                            <option key={t} value={t}>{t.replace(/_/g,' ')}</option>
                          ))}
                        </select>
                      </div>
                      <div style={{ marginBottom: 10 }}>
                        <div style={{ fontSize: 11, color: a.tertiaryLabel, marginBottom: 4 }}>Condition JSON (source, severity, title_contains, namespace_prefix, entity_type, label_key/value)</div>
                        <textarea rows={2} value={policyForm.condition} onChange={e => setPolicyForm(f => ({ ...f, condition: e.target.value }))}
                          placeholder={'{"title_contains": "liveness-fail"}'}
                          style={{ width: '100%', padding: '8px 12px', borderRadius: 8, border: `1px solid ${a.separator}`, background: 'var(--color-background)', fontSize: 12, fontFamily: 'monospace', color: a.label, resize: 'vertical', boxSizing: 'border-box' as const }} />
                      </div>
                      <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
                        <button onClick={() => setShowAddPolicy(false)} style={{ padding: '7px 14px', borderRadius: 8, background: a.fill, color: a.secondaryLabel, border: `1px solid ${a.separator}`, cursor: 'pointer', fontSize: 13 }}>Cancel</button>
                        <button onClick={async () => {
                          try { JSON.parse(policyForm.condition) } catch { alert('Invalid JSON'); return }
                          await intelligenceApi.createPolicy({ ...policyForm, condition: policyForm.condition })
                          setShowAddPolicy(false); setPolicyForm({ name: '', description: '', policy_type: 'suppress_alert', condition: '{}', priority: 50 }); reloadPolicies()
                        }} disabled={!policyForm.name} style={{ padding: '7px 14px', borderRadius: 8, background: a.purple, color: '#fff', border: 'none', cursor: 'pointer', fontSize: 13, opacity: policyForm.name ? 1 : 0.5 }}>Save</button>
                      </div>
                    </div>
                  )}
                  {!policiesLoaded ? (
                    <div style={{ textAlign: 'center', padding: 32 }}><Loader2 style={{ width: 20, height: 20, color: a.blue, margin: '0 auto' }} /></div>
                  ) : policies.length === 0 ? (
                    <div style={{ textAlign: 'center', padding: '32px 0', color: a.tertiaryLabel, fontSize: 13 }}>No policies defined. System uses built-in rules.</div>
                  ) : (
                    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                      {policies.map((p: any) => {
                        const typeColors: any = { suppress_alert: a.orange, suppress_incident: a.red, skip_rca: a.purple, require_approval: a.yellow, auto_resolve: a.green }
                        const tc = typeColors[p.policy_type] || a.blue
                        return (
                          <div key={p.id} style={{ padding: '10px 14px', background: a.fill, borderRadius: 10, border: `0.5px solid ${a.separator}`, display: 'flex', alignItems: 'center', gap: 10 }}>
                            <div style={{ flex: 1 }}>
                              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 3 }}>
                                <span style={{ fontSize: 13, fontWeight: 600, color: a.label }}>{p.name}</span>
                                <span style={{ fontSize: 10, padding: '1px 7px', borderRadius: 10, background: `${tc}15`, color: tc, fontWeight: 600, textTransform: 'uppercase' as const }}>{p.policy_type?.replace(/_/g,' ')}</span>
                                {!p.enabled && <span style={{ fontSize: 10, padding: '1px 7px', borderRadius: 10, background: `${a.red}10`, color: a.red }}>disabled</span>}
                              </div>
                              <code style={{ fontSize: 11, color: a.tertiaryLabel }}>{typeof p.condition === 'string' ? p.condition : JSON.stringify(p.condition)}</code>
                            </div>
                            <div style={{ display: 'flex', gap: 6 }}>
                              <button onClick={async () => { await intelligenceApi.togglePolicy(p.id); reloadPolicies() }}
                                style={{ padding: '5px 9px', borderRadius: 7, border: 'none', cursor: 'pointer', background: p.enabled ? `${a.green}15` : `${a.red}10`, color: p.enabled ? a.green : a.red, fontSize: 12 }}>
                                {p.enabled ? '✓' : '○'}
                              </button>
                              <button onClick={async () => { if (confirm(`Delete "${p.name}"?`)) { await intelligenceApi.deletePolicy(p.id); reloadPolicies() } }}
                                style={{ padding: '5px 9px', borderRadius: 7, border: 'none', cursor: 'pointer', background: `${a.red}10`, color: a.red }}>
                                <Trash2 style={{ width: 12, height: 12 }} />
                              </button>
                            </div>
                          </div>
                        )
                      })}
                    </div>
                  )}
                </div>
              )}

              {/* ── Runbooks sub-tab ─── */}
              {intelSubTab === 'runbooks' && (
                <div>
                  <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 16 }}>
                    <div>
                      <div style={{ fontSize: 14, fontWeight: 600, color: a.label }}>Investigation Runbooks</div>
                      <div style={{ fontSize: 12, color: a.secondaryLabel, marginTop: 2 }}>Injected as context into OIE investigations (HolmesGPT SkillCatalog)</div>
                    </div>
                    <button onClick={() => setShowAddRunbook(!showAddRunbook)}
                      style={{ padding: '7px 14px', borderRadius: 8, background: a.blue, color: '#fff', border: 'none', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 6, fontSize: 13 }}>
                      <Plus style={{ width: 14, height: 14 }} /> Add Runbook
                    </button>
                  </div>
                  {showAddRunbook && (
                    <div style={{ marginBottom: 16, padding: 14, background: a.fill, borderRadius: 10, border: `0.5px solid ${a.separator}` }}>
                      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr 1fr', gap: 10, marginBottom: 10 }}>
                        {[['Name*', 'name', 'runbookForm'], ['Domain', 'domain', ''], ['Entity type', 'entity_type', ''], ['Failure class', 'failure_class', '']].map(([ph, field]) => (
                          <input key={field} placeholder={ph} value={(runbookForm as any)[field]} onChange={e => setRunbookForm(f => ({ ...f, [field]: e.target.value }))}
                            style={{ padding: '8px 12px', borderRadius: 8, border: `1px solid ${a.separator}`, background: 'var(--color-background)', fontSize: 13, color: a.label }} />
                        ))}
                      </div>
                      <textarea rows={4} placeholder="Runbook content*" value={runbookForm.content} onChange={e => setRunbookForm(f => ({ ...f, content: e.target.value }))}
                        style={{ width: '100%', padding: '8px 12px', borderRadius: 8, border: `1px solid ${a.separator}`, background: 'var(--color-background)', fontSize: 13, color: a.label, resize: 'vertical', boxSizing: 'border-box' as const, marginBottom: 10 }} />
                      <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
                        <button onClick={() => setShowAddRunbook(false)} style={{ padding: '7px 14px', borderRadius: 8, background: a.fill, color: a.secondaryLabel, border: `1px solid ${a.separator}`, cursor: 'pointer', fontSize: 13 }}>Cancel</button>
                        <button onClick={async () => {
                          if (!runbookForm.name || !runbookForm.content) return
                          await intelligenceApi.createRunbook(runbookForm)
                          setShowAddRunbook(false); setRunbookForm({ name: '', domain: '', entity_type: '', failure_class: '', content: '' }); reloadRunbooks()
                        }} disabled={!runbookForm.name || !runbookForm.content}
                          style={{ padding: '7px 14px', borderRadius: 8, background: a.blue, color: '#fff', border: 'none', cursor: 'pointer', fontSize: 13, opacity: runbookForm.name && runbookForm.content ? 1 : 0.5 }}>Save</button>
                      </div>
                    </div>
                  )}
                  {!runbooksLoaded ? (
                    <div style={{ textAlign: 'center', padding: 32 }}><Loader2 style={{ width: 20, height: 20, color: a.blue, margin: '0 auto' }} /></div>
                  ) : runbooks.length === 0 ? (
                    <div style={{ textAlign: 'center', padding: '32px 0', color: a.tertiaryLabel, fontSize: 13 }}>No runbooks. Click Add Runbook to create your first.</div>
                  ) : (
                    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                      {runbooks.map((rb: any) => (
                        <div key={rb.id} style={{ borderRadius: 10, border: `0.5px solid ${a.separator}`, overflow: 'hidden' }}>
                          <div onClick={() => setExpandedRunbook(expandedRunbook === rb.id ? null : rb.id)}
                            style={{ padding: '10px 14px', background: a.fill, display: 'flex', alignItems: 'center', gap: 10, cursor: 'pointer' }}>
                            <ChevronDown style={{ width: 13, height: 13, color: a.secondaryLabel, transform: expandedRunbook === rb.id ? 'rotate(180deg)' : 'none', transition: 'transform 0.15s', flexShrink: 0 }} />
                            <div style={{ flex: 1 }}>
                              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                                <span style={{ fontSize: 13, fontWeight: 600, color: a.label }}>{rb.name}</span>
                                {rb.source === 'system' && <span style={{ fontSize: 10, padding: '1px 5px', borderRadius: 6, background: `${a.purple}15`, color: a.purple }}>system</span>}
                              </div>
                              <div style={{ fontSize: 11, color: a.tertiaryLabel, marginTop: 2 }}>
                                {[rb.domain && `domain:${rb.domain}`, rb.entity_type && `entity:${rb.entity_type}`, rb.failure_class && `class:${rb.failure_class}`].filter(Boolean).join(' · ') || 'all'}
                              </div>
                            </div>
                            {rb.source !== 'system' && (
                              <button onClick={async (e) => { e.stopPropagation(); if (confirm(`Delete "${rb.name}"?`)) { await intelligenceApi.deleteRunbook(rb.id); reloadRunbooks() } }}
                                style={{ padding: '4px 8px', borderRadius: 6, border: 'none', cursor: 'pointer', background: `${a.red}10`, color: a.red, flexShrink: 0 }}>
                                <Trash2 style={{ width: 12, height: 12 }} />
                              </button>
                            )}
                          </div>
                          {expandedRunbook === rb.id && (
                            <div style={{ padding: '10px 14px', borderTop: `0.5px solid ${a.separator}`, fontSize: 13, color: a.secondaryLabel, lineHeight: 1.6, whiteSpace: 'pre-wrap', background: 'var(--color-background)' }}>
                              {rb.content}
                            </div>
                          )}
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              )}

              {/* ── Model sub-tab ─── */}
              {intelSubTab === 'model' && (
                <div>
                  <div style={{ fontSize: 14, fontWeight: 600, color: a.label, marginBottom: 16 }}>Model Routing Configuration</div>
                  <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginBottom: 24 }}>
                    {[
                      { label: 'Default Model', env: 'LLM_MODEL', hint: 'Fallback for all roles' },
                      { label: 'RCA Model', env: 'LLM_RCA_MODEL', hint: 'Quality model for hypothesis synthesis' },
                      { label: 'Triage Model', env: 'LLM_TRIAGE_MODEL', hint: 'Fast model for evidence compaction' },
                      { label: 'Narrative Model', env: 'LLM_NARRATIVE_MODEL', hint: 'OIE narrator for human-readable RCA' },
                      { label: 'OIE Narrative', env: 'OIE_OLLAMA_MODEL_NARRATIVE', hint: 'Per-role override for OIE service' },
                      { label: 'OIE RCA', env: 'OIE_OLLAMA_MODEL_RCA', hint: 'Per-role override for OIE service' },
                    ].map(m => (
                      <div key={m.env} style={{ padding: 14, background: a.fill, borderRadius: 10, border: `0.5px solid ${a.separator}` }}>
                        <div style={{ fontSize: 11, fontWeight: 600, color: a.secondaryLabel, marginBottom: 4 }}>{m.label}</div>
                        <div style={{ fontSize: 12, color: a.label, fontFamily: 'monospace', background: `${a.purple}08`, padding: '3px 8px', borderRadius: 6, display: 'inline-block', marginBottom: 4 }}>{m.env}</div>
                        <div style={{ fontSize: 11, color: a.tertiaryLabel }}>{m.hint}</div>
                      </div>
                    ))}
                  </div>
                  <div style={{ padding: 14, background: `${a.blue}05`, borderRadius: 10, border: `0.5px solid ${a.blue}20` }}>
                    <div style={{ fontSize: 13, fontWeight: 600, color: a.label, marginBottom: 6 }}>MCP Server</div>
                    <div style={{ fontSize: 12, color: a.secondaryLabel, marginBottom: 8 }}>7 tools available to Claude Desktop, Cursor, Windsurf via <code style={{ background: a.fill, padding: '1px 5px', borderRadius: 4 }}>POST /api/v1/mcp</code></div>
                    {['list_incidents','get_incident','get_rca_decisions','search_incidents','get_postmortem','list_runbooks','propose_remediation'].map(t => (
                      <div key={t} style={{ display: 'inline-flex', alignItems: 'center', gap: 5, margin: '2px 4px 2px 0', padding: '2px 8px', borderRadius: 12, background: `${a.green}10`, color: a.green, fontSize: 11 }}>
                        <Cpu style={{ width: 10, height: 10 }} /> {t}
                      </div>
                    ))}
                  </div>
                </div>
              )}

              {/* ── Policy Test sub-tab ─── */}
              {intelSubTab === 'test' && (
                <div>
                  <div style={{ fontSize: 14, fontWeight: 600, color: a.label, marginBottom: 4 }}>Policy Evaluation Test</div>
                  <div style={{ fontSize: 12, color: a.secondaryLabel, marginBottom: 16 }}>Test what decision a synthetic signal gets from the policy engine</div>
                  <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginBottom: 12 }}>
                    <div>
                      <div style={{ fontSize: 11, color: a.tertiaryLabel, marginBottom: 4 }}>Source</div>
                      <input value={policyTestForm.source} onChange={e => setPolicyTestForm(f => ({ ...f, source: e.target.value }))} placeholder="dynatrace, prometheus…"
                        style={{ width: '100%', padding: '8px 12px', borderRadius: 8, border: `1px solid ${a.separator}`, background: 'var(--color-background)', fontSize: 13, color: a.label, boxSizing: 'border-box' as const }} />
                    </div>
                    <div>
                      <div style={{ fontSize: 11, color: a.tertiaryLabel, marginBottom: 4 }}>Severity</div>
                      <select value={policyTestForm.severity} onChange={e => setPolicyTestForm(f => ({ ...f, severity: e.target.value }))}
                        style={{ width: '100%', padding: '8px 12px', borderRadius: 8, border: `1px solid ${a.separator}`, background: 'var(--color-background)', fontSize: 13, color: a.label }}>
                        {['critical','high','medium','low'].map(s => <option key={s} value={s}>{s}</option>)}
                      </select>
                    </div>
                  </div>
                  <div style={{ marginBottom: 12 }}>
                    <div style={{ fontSize: 11, color: a.tertiaryLabel, marginBottom: 4 }}>Alert Title</div>
                    <input value={policyTestForm.title} onChange={e => setPolicyTestForm(f => ({ ...f, title: e.target.value }))} placeholder="e.g. Pod liveness-fail-test restarting"
                      style={{ width: '100%', padding: '8px 12px', borderRadius: 8, border: `1px solid ${a.separator}`, background: 'var(--color-background)', fontSize: 13, color: a.label, boxSizing: 'border-box' as const }} />
                  </div>
                  <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginBottom: 12 }}>
                    <div>
                      <div style={{ fontSize: 11, color: a.tertiaryLabel, marginBottom: 4 }}>Namespace</div>
                      <input value={policyTestForm.namespace} onChange={e => setPolicyTestForm(f => ({ ...f, namespace: e.target.value }))}
                        style={{ width: '100%', padding: '8px 12px', borderRadius: 8, border: `1px solid ${a.separator}`, background: 'var(--color-background)', fontSize: 13, color: a.label, boxSizing: 'border-box' as const }} />
                    </div>
                    <div>
                      <div style={{ fontSize: 11, color: a.tertiaryLabel, marginBottom: 4 }}>Labels JSON</div>
                      <input value={policyTestForm.labels} onChange={e => setPolicyTestForm(f => ({ ...f, labels: e.target.value }))} placeholder='{"environment":"dev"}'
                        style={{ width: '100%', padding: '8px 12px', borderRadius: 8, border: `1px solid ${a.separator}`, background: 'var(--color-background)', fontSize: 13, fontFamily: 'monospace', color: a.label, boxSizing: 'border-box' as const }} />
                    </div>
                  </div>
                  <button onClick={runPolicyTest} disabled={policyTestLoading}
                    style={{ padding: '8px 20px', borderRadius: 9, background: a.purple, color: '#fff', border: 'none', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 8, fontSize: 14, marginBottom: 16 }}>
                    {policyTestLoading ? <Loader2 style={{ width: 14, height: 14 }} /> : <Play style={{ width: 14, height: 14 }} />}
                    Evaluate
                  </button>
                  {policyTestResult && (
                    <div style={{ padding: 16, borderRadius: 12, border: `2px solid ${policyTestResult.action === 'allow' ? `${a.green}40` : `${a.red}40`}`, background: policyTestResult.action === 'allow' ? `${a.green}05` : `${a.red}05` }}>
                      <div style={{ fontSize: 20, fontWeight: 700, color: policyTestResult.action === 'allow' ? a.green : a.red, marginBottom: 6 }}>
                        {policyTestResult.action === 'allow' ? '✓ ALLOW' : `✗ ${policyTestResult.action?.toUpperCase().replace(/_/g,' ')}`}
                      </div>
                      {policyTestResult.policy_name ? (
                        <div style={{ fontSize: 13, color: a.secondaryLabel }}>Matched policy: <strong style={{ color: a.label }}>{policyTestResult.policy_name}</strong></div>
                      ) : (
                        <div style={{ fontSize: 13, color: a.tertiaryLabel }}>No policies matched — signal proceeds through pipeline.</div>
                      )}
                    </div>
                  )}
                </div>
              )}
            </div>
          )}
        </div>
      </div>

      <style>{`
        @keyframes spin { from { transform: rotate(0deg) } to { transform: rotate(360deg) } }
        @keyframes pulse { 0%, 100% { opacity: 1 } 50% { opacity: 0.5 } }
      `}</style>
    </div>
  )
}

export default AIOpsPage
