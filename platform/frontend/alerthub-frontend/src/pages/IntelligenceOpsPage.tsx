import React, { useState, useEffect, useCallback } from 'react'
import {
  Shield,
  BookOpen,
  Cpu,
  Search,
  Plus,
  Trash2,
  Play,
  CheckCircle,
  XCircle,
  AlertTriangle,
  Loader2,
  RefreshCw,
  ChevronRight,
  FlaskConical,
} from 'lucide-react'

const tokens = {
  blue: '#007AFF', green: '#34C759', red: '#FF3B30',
  orange: '#FF9500', purple: '#AF52DE', yellow: '#FFCC00',
  label: 'var(--color-label)', secondaryLabel: 'var(--color-secondary-label)',
  tertiaryLabel: 'var(--color-tertiary-label)', separator: 'var(--color-separator)',
  fill: 'var(--color-fill)', background: 'var(--color-background)',
  secondaryBackground: 'var(--color-secondary-background)',
}

const token = () => sessionStorage.getItem('access_token') || localStorage.getItem('access_token')
const authFetch = async (url: string, opts: RequestInit = {}) => {
  const r = await fetch(url, { ...opts, headers: { ...opts.headers as any, Authorization: `Bearer ${token()}`, 'Content-Type': 'application/json' } })
  return r.json()
}

type Tab = 'policies' | 'runbooks' | 'model' | 'test'

// ─── Types ────────────────────────────────────────────────────────────────────
interface Policy {
  id: string; name: string; description: string
  policy_type: string; condition: string; action: string
  enabled: boolean; priority: number; created_at: string
}
interface Runbook {
  id: string; name: string; domain: string; entity_type: string
  failure_class: string; content: string; source: string; created_at: string
}
interface TestResult { action: string; policy_name: string; policy_id: string }

// ─── Policy type options ──────────────────────────────────────────────────────
const POLICY_TYPES = [
  { value: 'suppress_alert', label: 'Suppress Alert' },
  { value: 'suppress_incident', label: 'Suppress Incident' },
  { value: 'skip_rca', label: 'Skip RCA' },
  { value: 'require_approval', label: 'Require Approval' },
  { value: 'auto_resolve', label: 'Auto Resolve' },
]

const policyTypeColor = (type: string) => {
  switch (type) {
    case 'suppress_alert':    return tokens.orange
    case 'suppress_incident': return tokens.red
    case 'skip_rca':          return tokens.purple
    case 'require_approval':  return tokens.yellow
    case 'auto_resolve':      return tokens.green
    default: return tokens.blue
  }
}

// ─── Policies Tab ─────────────────────────────────────────────────────────────
function PoliciesTab() {
  const [policies, setPolicies] = useState<Policy[]>([])
  const [loading, setLoading] = useState(false)
  const [showAdd, setShowAdd] = useState(false)
  const [form, setForm] = useState({ name: '', description: '', policy_type: 'suppress_alert', condition: '{}', action: 'suppress', priority: 50 })

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const d = await authFetch('/api/v1/intelligence-policies')
      const data = d?.data ?? d
      setPolicies(data?.policies ?? [])
    } finally { setLoading(false) }
  }, [])

  useEffect(() => { load() }, [load])

  const toggle = async (id: string) => {
    await authFetch(`/api/v1/intelligence-policies/${id}/toggle`, { method: 'PATCH' })
    load()
  }

  const remove = async (id: string, name: string) => {
    if (!confirm(`Delete policy "${name}"?`)) return
    await authFetch(`/api/v1/intelligence-policies/${id}`, { method: 'DELETE' })
    load()
  }

  const add = async () => {
    try {
      JSON.parse(form.condition)
    } catch {
      alert('Condition must be valid JSON')
      return
    }
    await authFetch('/api/v1/intelligence-policies', {
      method: 'POST',
      body: JSON.stringify({ ...form, condition: form.condition }),
    })
    setShowAdd(false)
    setForm({ name: '', description: '', policy_type: 'suppress_alert', condition: '{}', action: 'suppress', priority: 50 })
    load()
  }

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 16 }}>
        <div>
          <div style={{ fontSize: 15, fontWeight: 600, color: tokens.label }}>Intelligence Policies</div>
          <div style={{ fontSize: 12, color: tokens.secondaryLabel, marginTop: 2 }}>DB-driven suppression rules replacing hardcoded filters</div>
        </div>
        <button onClick={() => setShowAdd(!showAdd)} style={{ padding: '7px 14px', borderRadius: 8, background: tokens.blue, color: '#fff', border: 'none', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 6, fontSize: 13 }}>
          <Plus style={{ width: 14, height: 14 }} /> Add Policy
        </button>
      </div>

      {showAdd && (
        <div style={{ marginBottom: 20, padding: 16, background: tokens.fill, borderRadius: 12, border: `0.5px solid ${tokens.separator}` }}>
          <div style={{ fontSize: 13, fontWeight: 600, color: tokens.label, marginBottom: 12 }}>New Policy</div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginBottom: 10 }}>
            <input placeholder="Policy name*" value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))}
              style={{ padding: '8px 12px', borderRadius: 8, border: `1px solid ${tokens.separator}`, background: tokens.background, fontSize: 13, color: tokens.label }} />
            <select value={form.policy_type} onChange={e => setForm(f => ({ ...f, policy_type: e.target.value }))}
              style={{ padding: '8px 12px', borderRadius: 8, border: `1px solid ${tokens.separator}`, background: tokens.background, fontSize: 13, color: tokens.label }}>
              {POLICY_TYPES.map(t => <option key={t.value} value={t.value}>{t.label}</option>)}
            </select>
          </div>
          <input placeholder="Description" value={form.description} onChange={e => setForm(f => ({ ...f, description: e.target.value }))}
            style={{ width: '100%', padding: '8px 12px', borderRadius: 8, border: `1px solid ${tokens.separator}`, background: tokens.background, fontSize: 13, color: tokens.label, marginBottom: 10, boxSizing: 'border-box' as const }} />
          <div style={{ marginBottom: 10 }}>
            <div style={{ fontSize: 11, color: tokens.tertiaryLabel, marginBottom: 4 }}>
              Condition JSON (keys: source, severity, title_contains, namespace_prefix, entity_type, label_key, label_value)
            </div>
            <textarea rows={3} value={form.condition} onChange={e => setForm(f => ({ ...f, condition: e.target.value }))}
              placeholder={'{"title_contains": "liveness-fail"}'}
              style={{ width: '100%', padding: '8px 12px', borderRadius: 8, border: `1px solid ${tokens.separator}`, background: tokens.background, fontSize: 12, fontFamily: 'monospace', color: tokens.label, resize: 'vertical', boxSizing: 'border-box' as const }} />
          </div>
          <div style={{ display: 'flex', gap: 10, justifyContent: 'flex-end' }}>
            <button onClick={() => setShowAdd(false)} style={{ padding: '7px 14px', borderRadius: 8, background: tokens.fill, color: tokens.secondaryLabel, border: `1px solid ${tokens.separator}`, cursor: 'pointer', fontSize: 13 }}>Cancel</button>
            <button onClick={add} disabled={!form.name} style={{ padding: '7px 14px', borderRadius: 8, background: tokens.blue, color: '#fff', border: 'none', cursor: 'pointer', fontSize: 13, opacity: form.name ? 1 : 0.5 }}>Save Policy</button>
          </div>
        </div>
      )}

      {loading ? (
        <div style={{ textAlign: 'center', padding: 40 }}><Loader2 style={{ width: 24, height: 24, color: tokens.blue, margin: '0 auto' }} /></div>
      ) : policies.length === 0 ? (
        <div style={{ textAlign: 'center', padding: '40px 0', color: tokens.tertiaryLabel }}>
          <Shield style={{ width: 32, height: 32, opacity: 0.3, margin: '0 auto 12px' }} />
          <div style={{ fontSize: 13 }}>No policies defined. System uses built-in rules.</div>
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {policies.map(p => (
            <div key={p.id} style={{ padding: '12px 16px', borderRadius: 10, background: tokens.fill, border: `0.5px solid ${tokens.separator}`, display: 'flex', alignItems: 'center', gap: 12 }}>
              <div style={{ flex: 1 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
                  <span style={{ fontSize: 13, fontWeight: 600, color: tokens.label }}>{p.name}</span>
                  <span style={{ fontSize: 10, padding: '2px 8px', borderRadius: 12, background: `${policyTypeColor(p.policy_type)}15`, color: policyTypeColor(p.policy_type), fontWeight: 600, textTransform: 'uppercase' as const }}>
                    {p.policy_type.replace(/_/g, ' ')}
                  </span>
                  <span style={{ fontSize: 10, padding: '2px 6px', borderRadius: 8, background: `${tokens.blue}10`, color: tokens.blue }}>p={p.priority}</span>
                </div>
                {p.description && <div style={{ fontSize: 12, color: tokens.secondaryLabel, marginBottom: 4 }}>{p.description}</div>}
                <code style={{ fontSize: 11, color: tokens.tertiaryLabel, background: 'rgba(0,0,0,0.04)', padding: '2px 6px', borderRadius: 4 }}>
                  {typeof p.condition === 'string' ? p.condition : JSON.stringify(p.condition)}
                </code>
              </div>
              <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexShrink: 0 }}>
                <button onClick={() => toggle(p.id)} title={p.enabled ? 'Disable' : 'Enable'}
                  style={{ padding: '6px 10px', borderRadius: 7, border: 'none', cursor: 'pointer', fontSize: 12, background: p.enabled ? `${tokens.green}15` : `${tokens.red}10`, color: p.enabled ? tokens.green : tokens.red }}>
                  {p.enabled ? <CheckCircle style={{ width: 14, height: 14 }} /> : <XCircle style={{ width: 14, height: 14 }} />}
                </button>
                <button onClick={() => remove(p.id, p.name)} title="Delete"
                  style={{ padding: '6px 10px', borderRadius: 7, border: 'none', cursor: 'pointer', background: `${tokens.red}10`, color: tokens.red }}>
                  <Trash2 style={{ width: 13, height: 13 }} />
                </button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

// ─── Runbooks Tab ─────────────────────────────────────────────────────────────
function RunbooksTab() {
  const [runbooks, setRunbooks] = useState<Runbook[]>([])
  const [loading, setLoading] = useState(false)
  const [showAdd, setShowAdd] = useState(false)
  const [expanded, setExpanded] = useState<string | null>(null)
  const [form, setForm] = useState({ name: '', domain: '', entity_type: '', failure_class: '', content: '' })

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const d = await authFetch('/api/v1/runbooks')
      const data = d?.data ?? d
      setRunbooks(data?.runbooks ?? [])
    } finally { setLoading(false) }
  }, [])

  useEffect(() => { load() }, [load])

  const add = async () => {
    if (!form.name || !form.content) return
    await authFetch('/api/v1/runbooks', { method: 'POST', body: JSON.stringify(form) })
    setShowAdd(false)
    setForm({ name: '', domain: '', entity_type: '', failure_class: '', content: '' })
    load()
  }

  const remove = async (id: string, name: string, source: string) => {
    if (source === 'system') { alert('System runbooks cannot be deleted.'); return }
    if (!confirm(`Delete runbook "${name}"?`)) return
    await authFetch(`/api/v1/runbooks/${id}`, { method: 'DELETE' })
    load()
  }

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 16 }}>
        <div>
          <div style={{ fontSize: 15, fontWeight: 600, color: tokens.label }}>Investigation Runbooks</div>
          <div style={{ fontSize: 12, color: tokens.secondaryLabel, marginTop: 2 }}>Injected as context evidence into OIE investigations (HolmesGPT SkillCatalog)</div>
        </div>
        <button onClick={() => setShowAdd(!showAdd)} style={{ padding: '7px 14px', borderRadius: 8, background: tokens.blue, color: '#fff', border: 'none', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 6, fontSize: 13 }}>
          <Plus style={{ width: 14, height: 14 }} /> Add Runbook
        </button>
      </div>

      {showAdd && (
        <div style={{ marginBottom: 20, padding: 16, background: tokens.fill, borderRadius: 12, border: `0.5px solid ${tokens.separator}` }}>
          <div style={{ fontSize: 13, fontWeight: 600, color: tokens.label, marginBottom: 12 }}>New Runbook</div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr 1fr', gap: 10, marginBottom: 10 }}>
            <input placeholder="Name*" value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))}
              style={{ padding: '8px 12px', borderRadius: 8, border: `1px solid ${tokens.separator}`, background: tokens.background, fontSize: 13, color: tokens.label }} />
            <input placeholder="Domain (e.g. k8s)" value={form.domain} onChange={e => setForm(f => ({ ...f, domain: e.target.value }))}
              style={{ padding: '8px 12px', borderRadius: 8, border: `1px solid ${tokens.separator}`, background: tokens.background, fontSize: 13, color: tokens.label }} />
            <input placeholder="Entity type (e.g. pod)" value={form.entity_type} onChange={e => setForm(f => ({ ...f, entity_type: e.target.value }))}
              style={{ padding: '8px 12px', borderRadius: 8, border: `1px solid ${tokens.separator}`, background: tokens.background, fontSize: 13, color: tokens.label }} />
            <input placeholder="Failure class (e.g. OOMKilled)" value={form.failure_class} onChange={e => setForm(f => ({ ...f, failure_class: e.target.value }))}
              style={{ padding: '8px 12px', borderRadius: 8, border: `1px solid ${tokens.separator}`, background: tokens.background, fontSize: 13, color: tokens.label }} />
          </div>
          <textarea rows={5} placeholder="Runbook content — step-by-step investigation and remediation guide*" value={form.content}
            onChange={e => setForm(f => ({ ...f, content: e.target.value }))}
            style={{ width: '100%', padding: '8px 12px', borderRadius: 8, border: `1px solid ${tokens.separator}`, background: tokens.background, fontSize: 13, color: tokens.label, resize: 'vertical', boxSizing: 'border-box' as const, marginBottom: 10 }} />
          <div style={{ display: 'flex', gap: 10, justifyContent: 'flex-end' }}>
            <button onClick={() => setShowAdd(false)} style={{ padding: '7px 14px', borderRadius: 8, background: tokens.fill, color: tokens.secondaryLabel, border: `1px solid ${tokens.separator}`, cursor: 'pointer', fontSize: 13 }}>Cancel</button>
            <button onClick={add} disabled={!form.name || !form.content} style={{ padding: '7px 14px', borderRadius: 8, background: tokens.blue, color: '#fff', border: 'none', cursor: 'pointer', fontSize: 13, opacity: form.name && form.content ? 1 : 0.5 }}>Save Runbook</button>
          </div>
        </div>
      )}

      {loading ? (
        <div style={{ textAlign: 'center', padding: 40 }}><Loader2 style={{ width: 24, height: 24, color: tokens.blue, margin: '0 auto' }} /></div>
      ) : runbooks.length === 0 ? (
        <div style={{ textAlign: 'center', padding: '40px 0', color: tokens.tertiaryLabel }}>
          <BookOpen style={{ width: 32, height: 32, opacity: 0.3, margin: '0 auto 12px' }} />
          <div style={{ fontSize: 13 }}>No runbooks found.</div>
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {runbooks.map(rb => (
            <div key={rb.id} style={{ borderRadius: 10, border: `0.5px solid ${tokens.separator}`, overflow: 'hidden' }}>
              <div onClick={() => setExpanded(expanded === rb.id ? null : rb.id)}
                style={{ padding: '12px 16px', background: tokens.fill, display: 'flex', alignItems: 'center', gap: 10, cursor: 'pointer' }}>
                <ChevronRight style={{ width: 14, height: 14, color: tokens.secondaryLabel, transform: expanded === rb.id ? 'rotate(90deg)' : 'none', transition: 'transform 0.15s' }} />
                <div style={{ flex: 1 }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <span style={{ fontSize: 13, fontWeight: 600, color: tokens.label }}>{rb.name}</span>
                    {rb.source === 'system' && <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 8, background: `${tokens.purple}15`, color: tokens.purple, fontWeight: 600 }}>SYSTEM</span>}
                  </div>
                  <div style={{ display: 'flex', gap: 8, marginTop: 3 }}>
                    {rb.domain && <span style={{ fontSize: 11, color: tokens.tertiaryLabel }}>domain: {rb.domain}</span>}
                    {rb.entity_type && <span style={{ fontSize: 11, color: tokens.tertiaryLabel }}>entity: {rb.entity_type}</span>}
                    {rb.failure_class && <span style={{ fontSize: 11, color: tokens.tertiaryLabel }}>class: {rb.failure_class}</span>}
                  </div>
                </div>
                {rb.source !== 'system' && (
                  <button onClick={(e) => { e.stopPropagation(); remove(rb.id, rb.name, rb.source) }}
                    style={{ padding: '5px 8px', borderRadius: 7, border: 'none', cursor: 'pointer', background: `${tokens.red}10`, color: tokens.red }}>
                    <Trash2 style={{ width: 13, height: 13 }} />
                  </button>
                )}
              </div>
              {expanded === rb.id && (
                <div style={{ padding: '12px 16px', background: tokens.background, borderTop: `0.5px solid ${tokens.separator}`, fontSize: 13, color: tokens.secondaryLabel, lineHeight: 1.6, whiteSpace: 'pre-wrap' }}>
                  {rb.content}
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

// ─── Model Status Tab ─────────────────────────────────────────────────────────
function ModelStatusTab() {
  const [mcpStatus, setMcpStatus] = useState<any>(null)
  const [loading, setLoading] = useState(false)

  const load = async () => {
    setLoading(true)
    try {
      const d = await authFetch('/api/v1/mcp')
      setMcpStatus(d?.data ?? d)
    } finally { setLoading(false) }
  }

  useEffect(() => { load() }, [])

  const modelUrl = '/api/v1/incidents' // just a health check proxy

  return (
    <div>
      <div style={{ marginBottom: 20 }}>
        <div style={{ fontSize: 15, fontWeight: 600, color: tokens.label, marginBottom: 4 }}>Model & MCP Status</div>
        <div style={{ fontSize: 12, color: tokens.secondaryLabel }}>LLMFit model routing configuration and MCP server tools</div>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16, marginBottom: 24 }}>
        {[
          { label: 'Default LLM Model', env: 'LLM_MODEL', hint: 'Used as fallback for all roles' },
          { label: 'RCA Model', env: 'LLM_RCA_MODEL', hint: 'Quality model for hypothesis synthesis' },
          { label: 'Triage Model', env: 'LLM_TRIAGE_MODEL', hint: 'Fast model for evidence compaction' },
          { label: 'Narrative Model', env: 'LLM_NARRATIVE_MODEL', hint: 'OIE narrator for human-readable RCA' },
        ].map(m => (
          <div key={m.env} style={{ padding: 16, background: tokens.fill, borderRadius: 12, border: `0.5px solid ${tokens.separator}` }}>
            <div style={{ fontSize: 12, fontWeight: 600, color: tokens.secondaryLabel, marginBottom: 4 }}>{m.label}</div>
            <div style={{ fontSize: 13, color: tokens.label, fontFamily: 'monospace' }}>{m.env}</div>
            <div style={{ fontSize: 11, color: tokens.tertiaryLabel, marginTop: 4 }}>{m.hint}</div>
          </div>
        ))}
      </div>

      <div style={{ marginBottom: 16, display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <div style={{ fontSize: 14, fontWeight: 600, color: tokens.label }}>MCP Server Tools</div>
        <button onClick={load} style={{ padding: '6px 12px', borderRadius: 8, background: tokens.fill, color: tokens.secondaryLabel, border: `1px solid ${tokens.separator}`, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 6, fontSize: 12 }}>
          <RefreshCw style={{ width: 12, height: 12 }} /> Refresh
        </button>
      </div>

      {loading ? (
        <div style={{ textAlign: 'center', padding: 24 }}><Loader2 style={{ width: 20, height: 20, color: tokens.blue, margin: '0 auto' }} /></div>
      ) : mcpStatus?.tools ? (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {mcpStatus.tools.map((t: any) => (
            <div key={t.name} style={{ padding: '12px 16px', borderRadius: 10, background: tokens.fill, border: `0.5px solid ${tokens.separator}` }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
                <Cpu style={{ width: 13, height: 13, color: tokens.blue }} />
                <span style={{ fontSize: 13, fontWeight: 600, color: tokens.label, fontFamily: 'monospace' }}>{t.name}</span>
                <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 8, background: `${tokens.green}15`, color: tokens.green }}>active</span>
              </div>
              <div style={{ fontSize: 12, color: tokens.secondaryLabel }}>{t.description}</div>
            </div>
          ))}
        </div>
      ) : (
        <div style={{ textAlign: 'center', padding: '32px 0', color: tokens.tertiaryLabel }}>
          <Cpu style={{ width: 28, height: 28, opacity: 0.3, margin: '0 auto 10px' }} />
          <div style={{ fontSize: 13 }}>MCP server status unavailable</div>
          <div style={{ fontSize: 12, marginTop: 4 }}>Ensure AlertHub backend is running</div>
        </div>
      )}
    </div>
  )
}

// ─── Policy Test Tab ──────────────────────────────────────────────────────────
function PolicyTestTab() {
  const [form, setForm] = useState({ source: 'dynatrace', title: '', severity: 'high', namespace: '', entity_type: '', labels: '{}' })
  const [result, setResult] = useState<TestResult | null>(null)
  const [loading, setLoading] = useState(false)

  const test = async () => {
    let labels: any = {}
    try { labels = JSON.parse(form.labels) } catch { alert('Labels must be valid JSON'); return }
    setLoading(true)
    try {
      const d = await authFetch('/api/v1/intelligence-policies/evaluate', {
        method: 'POST',
        body: JSON.stringify({ ...form, labels }),
      })
      const data = d?.data ?? d
      setResult(data)
    } finally { setLoading(false) }
  }

  const actionColor = (a: string) => a === 'allow' ? tokens.green : a === 'suppress' || a === 'suppress_alert' ? tokens.red : tokens.orange

  return (
    <div>
      <div style={{ marginBottom: 20 }}>
        <div style={{ fontSize: 15, fontWeight: 600, color: tokens.label, marginBottom: 4 }}>Policy Evaluation Test</div>
        <div style={{ fontSize: 12, color: tokens.secondaryLabel }}>Test what decision a synthetic alert signal gets from the policy engine</div>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginBottom: 12 }}>
        <div>
          <div style={{ fontSize: 11, color: tokens.tertiaryLabel, marginBottom: 4 }}>Source</div>
          <input value={form.source} onChange={e => setForm(f => ({ ...f, source: e.target.value }))}
            placeholder="dynatrace, prometheus, kubernetes…"
            style={{ width: '100%', padding: '8px 12px', borderRadius: 8, border: `1px solid ${tokens.separator}`, background: tokens.background, fontSize: 13, color: tokens.label, boxSizing: 'border-box' as const }} />
        </div>
        <div>
          <div style={{ fontSize: 11, color: tokens.tertiaryLabel, marginBottom: 4 }}>Severity</div>
          <select value={form.severity} onChange={e => setForm(f => ({ ...f, severity: e.target.value }))}
            style={{ width: '100%', padding: '8px 12px', borderRadius: 8, border: `1px solid ${tokens.separator}`, background: tokens.background, fontSize: 13, color: tokens.label }}>
            {['critical','high','medium','low'].map(s => <option key={s} value={s}>{s}</option>)}
          </select>
        </div>
      </div>

      <div style={{ marginBottom: 12 }}>
        <div style={{ fontSize: 11, color: tokens.tertiaryLabel, marginBottom: 4 }}>Alert Title</div>
        <input value={form.title} onChange={e => setForm(f => ({ ...f, title: e.target.value }))}
          placeholder="e.g. Pod liveness-fail-test restarting in namespace dev"
          style={{ width: '100%', padding: '8px 12px', borderRadius: 8, border: `1px solid ${tokens.separator}`, background: tokens.background, fontSize: 13, color: tokens.label, boxSizing: 'border-box' as const }} />
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginBottom: 12 }}>
        <div>
          <div style={{ fontSize: 11, color: tokens.tertiaryLabel, marginBottom: 4 }}>Namespace</div>
          <input value={form.namespace} onChange={e => setForm(f => ({ ...f, namespace: e.target.value }))}
            style={{ width: '100%', padding: '8px 12px', borderRadius: 8, border: `1px solid ${tokens.separator}`, background: tokens.background, fontSize: 13, color: tokens.label, boxSizing: 'border-box' as const }} />
        </div>
        <div>
          <div style={{ fontSize: 11, color: tokens.tertiaryLabel, marginBottom: 4 }}>Entity Type</div>
          <input value={form.entity_type} onChange={e => setForm(f => ({ ...f, entity_type: e.target.value }))}
            placeholder="pod, node, service…"
            style={{ width: '100%', padding: '8px 12px', borderRadius: 8, border: `1px solid ${tokens.separator}`, background: tokens.background, fontSize: 13, color: tokens.label, boxSizing: 'border-box' as const }} />
        </div>
      </div>

      <div style={{ marginBottom: 16 }}>
        <div style={{ fontSize: 11, color: tokens.tertiaryLabel, marginBottom: 4 }}>Labels (JSON)</div>
        <input value={form.labels} onChange={e => setForm(f => ({ ...f, labels: e.target.value }))}
          placeholder={'{"environment": "dev"}'}
          style={{ width: '100%', padding: '8px 12px', borderRadius: 8, border: `1px solid ${tokens.separator}`, background: tokens.background, fontSize: 13, fontFamily: 'monospace', color: tokens.label, boxSizing: 'border-box' as const }} />
      </div>

      <button onClick={test} disabled={loading}
        style={{ padding: '9px 20px', borderRadius: 9, background: tokens.blue, color: '#fff', border: 'none', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 8, fontSize: 14, marginBottom: 20 }}>
        {loading ? <Loader2 style={{ width: 14, height: 14 }} /> : <Play style={{ width: 14, height: 14 }} />}
        Evaluate
      </button>

      {result && (
        <div style={{ padding: 16, borderRadius: 12, border: `2px solid ${actionColor(result.action)}40`, background: `${actionColor(result.action)}08` }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 8 }}>
            <div style={{ fontSize: 24, fontWeight: 700, color: actionColor(result.action), textTransform: 'uppercase' as const }}>
              {result.action === 'allow' ? '✓ ALLOW' : `✗ ${result.action.toUpperCase().replace(/_/g, ' ')}`}
            </div>
          </div>
          {result.policy_name && (
            <div style={{ fontSize: 13, color: tokens.secondaryLabel }}>
              Matched policy: <strong style={{ color: tokens.label }}>{result.policy_name}</strong>
            </div>
          )}
          {!result.policy_name && result.action === 'allow' && (
            <div style={{ fontSize: 13, color: tokens.tertiaryLabel }}>No policies matched — signal proceeds through the pipeline.</div>
          )}
        </div>
      )}
    </div>
  )
}

// ─── Page ─────────────────────────────────────────────────────────────────────
const TABS: { id: Tab; label: string; icon: React.ReactNode }[] = [
  { id: 'policies', label: 'Policies', icon: <Shield style={{ width: 15, height: 15 }} /> },
  { id: 'runbooks', label: 'Runbooks', icon: <BookOpen style={{ width: 15, height: 15 }} /> },
  { id: 'model',    label: 'Model Status', icon: <Cpu style={{ width: 15, height: 15 }} /> },
  { id: 'test',     label: 'Policy Test', icon: <FlaskConical style={{ width: 15, height: 15 }} /> },
]

export function IntelligenceOpsPage() {
  const [tab, setTab] = useState<Tab>('policies')

  return (
    <div style={{
      minHeight: '100vh', background: tokens.background,
      fontFamily: '-aileron-system, BlinkMacSystemFont, "SF Pro Text", sans-serif',
      padding: '32px 24px',
    }}>
      {/* Header */}
      <div style={{ marginBottom: 28 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 8 }}>
          <div style={{ width: 36, height: 36, borderRadius: 10, background: `${tokens.purple}20`, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
            <Shield style={{ width: 18, height: 18, color: tokens.purple }} />
          </div>
          <div>
            <h1 style={{ fontSize: 22, fontWeight: 700, color: tokens.label, margin: 0 }}>Intelligence Operations</h1>
            <p style={{ fontSize: 13, color: tokens.secondaryLabel, margin: 0 }}>Policies · Runbooks · Model routing · Policy evaluation</p>
          </div>
        </div>
      </div>

      {/* Tabs */}
      <div style={{ display: 'flex', gap: 4, marginBottom: 24, background: tokens.fill, padding: 4, borderRadius: 12, width: 'fit-content' }}>
        {TABS.map(t => (
          <button key={t.id} onClick={() => setTab(t.id)} style={{
            display: 'flex', alignItems: 'center', gap: 7, padding: '8px 16px', borderRadius: 9, border: 'none', cursor: 'pointer', fontSize: 13, fontWeight: 500,
            background: tab === t.id ? tokens.background : 'transparent',
            color: tab === t.id ? tokens.label : tokens.secondaryLabel,
            boxShadow: tab === t.id ? '0 1px 4px rgba(0,0,0,0.1)' : 'none',
            transition: 'all 0.15s',
          }}>
            {t.icon} {t.label}
          </button>
        ))}
      </div>

      {/* Content */}
      <div style={{ maxWidth: 900, background: tokens.secondaryBackground, borderRadius: 16, padding: 28, border: `0.5px solid ${tokens.separator}` }}>
        {tab === 'policies'  && <PoliciesTab />}
        {tab === 'runbooks'  && <RunbooksTab />}
        {tab === 'model'     && <ModelStatusTab />}
        {tab === 'test'      && <PolicyTestTab />}
      </div>
    </div>
  )
}
