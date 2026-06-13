import React, { useState, useEffect, useCallback } from 'react'
import { motion, AnimatePresence } from 'framer-motion'
import {
  Plus, Key, Copy, Trash2, CheckCircle, AlertTriangle,
  RefreshCw, Activity, Globe, Zap, Shield, ChevronRight,
  ChevronDown, Clock, X, Check, BarChart2, Webhook
} from 'lucide-react'

const tokens = {
  blue: '#007AFF',
  green: '#34C759',
  red: '#FF3B30',
  orange: '#FF9500',
  purple: '#AF52DE',
  teal: '#5AC8FA',
  label: 'var(--color-text)',
  secondaryLabel: 'var(--color-text-secondary)',
  tertiaryLabel: 'var(--color-text-tertiary, #8E8E93)',
  separator: 'var(--color-separator, rgba(142, 142, 147, 0.12))',
  fill: 'var(--color-fill, rgba(142, 142, 147, 0.08))',
  secondaryBackground: 'var(--color-card, rgba(255, 255, 255, 0.8))',
  radius: { sm: 6, md: 10, lg: 12, xl: 16 },
}

// ─── Types ────────────────────────────────────────────────────────────────────

interface EnterpriseAPIKey {
  id: string
  name: string
  key_prefix: string
  scopes: string[]
  tier_name: string
  description: string
  is_active: boolean
  expires_at?: string
  last_used_at?: string
  total_requests: number
  created_at: string
}

interface WebhookSourceKey {
  id: string
  name: string
  enabled: boolean
  last_used_at?: string
  created_at: string
}

interface WebhookSubscription {
  id: string
  name: string
  target_url: string
  event_types: string[]
  is_active: boolean
  verify_ssl: boolean
  total_deliveries: number
  failed_deliveries: number
  consecutive_failures: number
  last_delivery_at?: string
  last_success_at?: string
  last_failure_at?: string
  paused_until?: string
  description: string
  created_at: string
}

interface RateLimitStatus {
  tier_name: string
  display_name: string
  requests_per_minute: number
  requests_per_hour: number
  requests_per_day: number
  burst_limit: number
}

interface EventType {
  event_type: string
  category: string
  display_name: string
  description: string
}

// ─── API helpers ──────────────────────────────────────────────────────────────

const authHeader = () => ({
  Authorization: `Bearer ${sessionStorage.getItem('access_token')}`,
  'Content-Type': 'application/json',
})

async function apiFetch(path: string, init?: RequestInit) {
  const res = await fetch(path, { ...init, headers: { ...authHeader(), ...(init?.headers ?? {}) } })
  const data = await res.json()
  if (!res.ok) throw new Error(data.message || 'Request failed')
  return data
}

// ─── Root component ───────────────────────────────────────────────────────────

type Tab = 'api-keys' | 'webhooks' | 'rate-limits'

// Rendered as a tab inside AdminPage — no outer heading or page padding needed.
export function APIKeyManagementPage() {
  const [activeTab, setActiveTab] = useState<Tab>('api-keys')

  const tabs: { id: Tab; label: string; icon: React.ReactNode }[] = [
    { id: 'api-keys', label: 'API Keys', icon: <Key style={{ width: 15, height: 15 }} /> },
    { id: 'webhooks', label: 'Webhook Subscriptions', icon: <Webhook style={{ width: 15, height: 15 }} /> },
    { id: 'rate-limits', label: 'Rate Limits', icon: <Zap style={{ width: 15, height: 15 }} /> },
  ]

  return (
    <div>
      {/* Sub-tab bar */}
      <div style={{
        display: 'flex', gap: 4, padding: '4px',
        background: tokens.fill, borderRadius: tokens.radius.md,
        marginBottom: 20, width: 'fit-content',
      }}>
        {tabs.map(t => (
          <button
            key={t.id}
            onClick={() => setActiveTab(t.id)}
            style={{
              display: 'flex', alignItems: 'center', gap: 6,
              padding: '7px 14px', borderRadius: tokens.radius.sm, border: 'none',
              background: activeTab === t.id ? tokens.secondaryBackground : 'transparent',
              color: activeTab === t.id ? tokens.blue : tokens.secondaryLabel,
              fontSize: 13, fontWeight: activeTab === t.id ? 600 : 400,
              cursor: 'pointer',
              boxShadow: activeTab === t.id ? '0 1px 3px rgba(0,0,0,0.1)' : 'none',
              transition: 'all 0.15s',
            }}
          >
            {t.icon} {t.label}
          </button>
        ))}
      </div>

      {activeTab === 'api-keys' && <APIKeysTab />}
      {activeTab === 'webhooks' && <WebhooksTab />}
      {activeTab === 'rate-limits' && <RateLimitsTab />}
    </div>
  )
}

// ─── API Keys tab ─────────────────────────────────────────────────────────────

function APIKeysTab() {
  const [keys, setKeys] = useState<EnterpriseAPIKey[]>([])
  const [loading, setLoading] = useState(true)
  const [showCreate, setShowCreate] = useState(false)
  const [expandedId, setExpandedId] = useState<string | null>(null)

  // Webhook source keys (inbound auth for Dynatrace / Prometheus / Grafana)
  const [sourceKeys, setSourceKeys] = useState<WebhookSourceKey[]>([])
  const [sourceLoading, setSourceLoading] = useState(true)
  const [showCreateSource, setShowCreateSource] = useState(false)
  const [newSourceName, setNewSourceName] = useState('')
  const [createdSourceKey, setCreatedSourceKey] = useState<string | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const d = await apiFetch('/api/v1/enterprise/api-keys')
      setKeys(d.data?.keys ?? [])
    } catch { setKeys([]) }
    finally { setLoading(false) }
  }, [])

  const loadSourceKeys = useCallback(async () => {
    setSourceLoading(true)
    try {
      const d = await apiFetch('/api/v1/settings/api-keys')
      setSourceKeys(d.data?.keys ?? [])
    } catch { setSourceKeys([]) }
    finally { setSourceLoading(false) }
  }, [])

  useEffect(() => { load(); loadSourceKeys() }, [load, loadSourceKeys])

  const revoke = async (id: string, name: string) => {
    if (!confirm(`Revoke "${name}"? Systems using this key will immediately lose access.`)) return
    try {
      await apiFetch(`/api/v1/enterprise/api-keys/${id}`, { method: 'DELETE' })
      load()
    } catch (e: any) { alert(e.message) }
  }

  const rotate = async (id: string, name: string) => {
    if (!confirm(`Rotate "${name}"? A new key will be issued and the old key revoked.`)) return
    try {
      const d = await apiFetch(`/api/v1/enterprise/api-keys/${id}/rotate`, { method: 'POST' })
      alert(`Key rotated! New plaintext: ${d.data?.plaintext ?? '(see clipboard)'}`)
      load()
    } catch (e: any) { alert(e.message) }
  }

  const revokeSourceKey = async (id: string, name: string) => {
    if (!confirm(`Revoke "${name}"? Dynatrace / Prometheus webhooks using this key will stop authenticating.`)) return
    try {
      await apiFetch(`/api/v1/settings/api-keys/${id}`, { method: 'DELETE' })
      loadSourceKeys()
    } catch (e: any) { alert(e.message) }
  }

  const createSourceKey = async () => {
    if (!newSourceName.trim()) return
    try {
      const d = await apiFetch('/api/v1/settings/api-keys', {
        method: 'POST',
        body: JSON.stringify({ name: newSourceName.trim() }),
      })
      setCreatedSourceKey(d.data?.key ?? null)
      setNewSourceName('')
      loadSourceKeys()
    } catch (e: any) { alert(e.message) }
  }

  return (
    <>
      {/* ── Enterprise API Keys ─────────────────────────────────────── */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <p style={{ fontSize: 14, color: tokens.secondaryLabel, margin: 0 }}>
          Scoped, rotatable API keys for programmatic access. Each key is shown <strong>once</strong> at creation.
        </p>
        <button onClick={() => setShowCreate(true)} style={btnStyle(tokens.blue)}>
          <Plus style={{ width: 15, height: 15 }} /> Create Key
        </button>
      </div>

      {/* Usage snippet */}
      <div style={codeSnippetStyle}>
        <span style={{ color: tokens.blue, fontWeight: 600, marginRight: 8 }}>Authorization:</span>
        Bearer sk-ah-...
      </div>

      <Card>
        {loading ? <EmptyState icon={<Key />} title="Loading…" /> :
          keys.length === 0 ? (
            <EmptyState
              icon={<Key />} title="No API keys" subtitle="Create a key to authenticate programmatic access."
              action={<button onClick={() => setShowCreate(true)} style={btnStyle(tokens.blue)}>Create API Key</button>}
            />
          ) : keys.map(k => (
            <APIKeyRow
              key={k.id} k={k}
              expanded={expandedId === k.id}
              onExpand={() => setExpandedId(expandedId === k.id ? null : k.id)}
              onRevoke={() => revoke(k.id, k.name)}
              onRotate={() => rotate(k.id, k.name)}
            />
          ))}
      </Card>

      {showCreate && (
        <CreateAPIKeyModal onClose={() => setShowCreate(false)} onCreated={load} />
      )}

      {/* ── Webhook Source Keys ─────────────────────────────────────── */}
      <div style={{ marginTop: 36 }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 12 }}>
          <div>
            <h3 style={{ fontSize: 16, fontWeight: 700, color: tokens.label, margin: 0 }}>Webhook Source Keys</h3>
            <p style={{ fontSize: 13, color: tokens.secondaryLabel, margin: '4px 0 0' }}>
              Authentication keys for inbound webhooks from Dynatrace, Prometheus, and Grafana.
              Pass as <code style={{ fontFamily: 'monospace', fontSize: 12 }}>X-API-Key</code> header.
            </p>
          </div>
          <button onClick={() => setShowCreateSource(true)} style={btnStyle(tokens.teal)}>
            <Plus style={{ width: 15, height: 15 }} /> New Source Key
          </button>
        </div>

        <div style={codeSnippetStyle}>
          <span style={{ color: tokens.teal, fontWeight: 600, marginRight: 8 }}>X-API-Key:</span>
          ah_...
        </div>

        <Card>
          {sourceLoading ? <EmptyState icon={<Webhook />} title="Loading…" /> :
            sourceKeys.length === 0 ? (
              <EmptyState
                icon={<Webhook />} title="No webhook source keys"
                subtitle="Create a key and configure it in Dynatrace / Prometheus / Grafana as the webhook auth token."
                action={<button onClick={() => setShowCreateSource(true)} style={btnStyle(tokens.teal)}>Create Source Key</button>}
              />
            ) : sourceKeys.map(k => (
              <div key={k.id} style={{ padding: '12px 16px', borderBottom: `0.5px solid ${tokens.separator}`, opacity: k.enabled ? 1 : 0.5 }}>
                <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                    <Webhook style={{ width: 16, height: 16, color: tokens.teal, flexShrink: 0 }} />
                    <div>
                      <span style={{ fontSize: 14, fontWeight: 600, color: tokens.label }}>{k.name}</span>
                      <div style={{ display: 'flex', gap: 8, marginTop: 4, flexWrap: 'wrap' }}>
                        <Badge label={k.enabled ? 'Active' : 'Revoked'} color={k.enabled ? tokens.green : tokens.tertiaryLabel} />
                        <span style={{ fontSize: 12, color: tokens.secondaryLabel }}>
                          Created {new Date(k.created_at).toLocaleDateString()}
                        </span>
                        {k.last_used_at && (
                          <span style={{ fontSize: 12, color: tokens.secondaryLabel }}>
                            · Last used {new Date(k.last_used_at).toLocaleDateString()}
                          </span>
                        )}
                      </div>
                    </div>
                  </div>
                  {k.enabled && (
                    <IconBtn onClick={() => revokeSourceKey(k.id, k.name)} title="Revoke" color={tokens.red}>
                      <Trash2 style={{ width: 14, height: 14 }} />
                    </IconBtn>
                  )}
                </div>
              </div>
            ))}
        </Card>
      </div>

      {/* Create source key modal */}
      {showCreateSource && (
        <Modal onClose={() => { setShowCreateSource(false); setCreatedSourceKey(null); setNewSourceName('') }}>
          {createdSourceKey ? (
            <div>
              <h2 style={{ fontSize: 18, fontWeight: 700, color: tokens.label, marginBottom: 8 }}>Key created</h2>
              <p style={{ fontSize: 14, color: tokens.secondaryLabel, marginBottom: 16 }}>
                Copy this key now — it will <strong>not</strong> be shown again.
                Paste it into Dynatrace / Prometheus / Grafana as the <code style={{ fontFamily: 'monospace' }}>X-API-Key</code> header value.
              </p>
              <div style={{ ...codeSnippetStyle, display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8 }}>
                <span style={{ wordBreak: 'break-all' }}>{createdSourceKey}</span>
                <button onClick={() => navigator.clipboard.writeText(createdSourceKey)} style={{ ...btnStyle(tokens.blue), flexShrink: 0, padding: '6px 10px' }}>
                  <Copy style={{ width: 13, height: 13 }} />
                </button>
              </div>
              <button onClick={() => { setShowCreateSource(false); setCreatedSourceKey(null) }} style={{ ...btnStyle(tokens.green), width: '100%', justifyContent: 'center', marginTop: 16 }}>
                <Check style={{ width: 15, height: 15 }} /> Done
              </button>
            </div>
          ) : (
            <div>
              <h2 style={{ fontSize: 18, fontWeight: 700, color: tokens.label, marginBottom: 16 }}>New Webhook Source Key</h2>
              <FieldLabel>Key name</FieldLabel>
              <input
                value={newSourceName}
                onChange={e => setNewSourceName(e.target.value)}
                placeholder="e.g. Dynatrace Production"
                onKeyDown={e => e.key === 'Enter' && createSourceKey()}
                style={{ ...inputStyle, width: '100%', boxSizing: 'border-box', marginBottom: 20 }}
              />
              <div style={{ display: 'flex', gap: 10 }}>
                <button onClick={() => setShowCreateSource(false)} style={{ ...btnStyle(tokens.tertiaryLabel), flex: 1, justifyContent: 'center' }}>Cancel</button>
                <button onClick={createSourceKey} disabled={!newSourceName.trim()} style={{ ...btnStyle(tokens.teal), flex: 1, justifyContent: 'center' }}>
                  <Plus style={{ width: 15, height: 15 }} /> Create
                </button>
              </div>
            </div>
          )}
        </Modal>
      )}
    </>
  )
}

function APIKeyRow({ k, expanded, onExpand, onRevoke, onRotate }: {
  k: EnterpriseAPIKey
  expanded: boolean
  onExpand: () => void
  onRevoke: () => void
  onRotate: () => void
}) {
  const [usage, setUsage] = useState<any[] | null>(null)

  const loadUsage = async () => {
    if (usage) return
    try {
      const d = await apiFetch(`/api/v1/enterprise/api-keys/${k.id}/usage`)
      setUsage(d.data?.stats ?? [])
    } catch { setUsage([]) }
  }

  useEffect(() => { if (expanded) loadUsage() }, [expanded])

  const scopeColor = (s: string) => {
    if (s === 'admin') return tokens.red
    if (s.endsWith(':write')) return tokens.orange
    return tokens.blue
  }

  return (
    <div style={{
      padding: 16, borderBottom: `0.5px solid ${tokens.separator}`,
      opacity: k.is_active ? 1 : 0.5,
    }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
        <div style={{ flex: 1, cursor: 'pointer' }} onClick={onExpand}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
            <span style={{ fontSize: 14, fontWeight: 600, color: tokens.label }}>{k.name}</span>
            {!k.is_active && <Badge label="Revoked" color={tokens.red} />}
            <Badge label={k.tier_name} color={tokens.purple} />
            {expanded
              ? <ChevronDown style={{ width: 14, height: 14, color: tokens.tertiaryLabel }} />
              : <ChevronRight style={{ width: 14, height: 14, color: tokens.tertiaryLabel }} />
            }
          </div>
          <div style={{ fontFamily: 'SFMono-Regular,monospace', fontSize: 12, color: tokens.tertiaryLabel, marginBottom: 8 }}>
            {k.key_prefix}
          </div>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4, marginBottom: 8 }}>
            {k.scopes.map(s => (
              <span key={s} style={{
                padding: '1px 7px', borderRadius: 10,
                background: `${scopeColor(s)}15`, fontSize: 11, fontWeight: 600,
                color: scopeColor(s),
              }}>{s}</span>
            ))}
          </div>
          <div style={{ display: 'flex', gap: 16, fontSize: 12, color: tokens.tertiaryLabel }}>
            <span>Created {fmt(k.created_at)}</span>
            {k.last_used_at ? <span>Last used {fmt(k.last_used_at)}</span> : <span>Never used</span>}
            <span>{k.total_requests.toLocaleString()} requests</span>
            {k.expires_at && <span style={{ color: tokens.orange }}>Expires {fmt(k.expires_at)}</span>}
          </div>
        </div>

        {k.is_active && (
          <div style={{ display: 'flex', gap: 6, marginLeft: 12 }}>
            <IconBtn onClick={onRotate} title="Rotate" color={tokens.orange}>
              <RefreshCw style={{ width: 15, height: 15 }} />
            </IconBtn>
            <IconBtn onClick={onRevoke} title="Revoke" color={tokens.red}>
              <Trash2 style={{ width: 15, height: 15 }} />
            </IconBtn>
          </div>
        )}
      </div>

      <AnimatePresence>
        {expanded && (
          <motion.div initial={{ height: 0, opacity: 0 }} animate={{ height: 'auto', opacity: 1 }} exit={{ height: 0, opacity: 0 }}
            style={{ overflow: 'hidden' }}>
            <div style={{ paddingTop: 12, borderTop: `0.5px solid ${tokens.separator}`, marginTop: 12 }}>
              {k.description && (
                <p style={{ fontSize: 13, color: tokens.secondaryLabel, marginBottom: 12 }}>{k.description}</p>
              )}
              <p style={{ fontSize: 12, fontWeight: 600, color: tokens.secondaryLabel, marginBottom: 8 }}>
                Last 7 days usage
              </p>
              {usage === null ? (
                <p style={{ fontSize: 12, color: tokens.tertiaryLabel }}>Loading…</p>
              ) : usage.length === 0 ? (
                <p style={{ fontSize: 12, color: tokens.tertiaryLabel }}>No usage in the last 7 days</p>
              ) : (
                <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
                  {usage.map((row: any) => (
                    <div key={row.day} style={{
                      padding: '8px 12px', background: tokens.fill, borderRadius: tokens.radius.sm,
                      fontSize: 12, textAlign: 'center',
                    }}>
                      <div style={{ color: tokens.tertiaryLabel, marginBottom: 4 }}>{row.day}</div>
                      <div style={{ fontWeight: 600, color: tokens.label }}>{row.requests}</div>
                      {row.errors > 0 && <div style={{ color: tokens.red, fontSize: 11 }}>{row.errors} err</div>}
                    </div>
                  ))}
                </div>
              )}
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  )
}

function CreateAPIKeyModal({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [selectedScopes, setSelectedScopes] = useState<string[]>(['alerts:read'])
  const [availableScopes, setAvailableScopes] = useState<string[]>([])
  const [tierName, setTierName] = useState('standard')
  const [creating, setCreating] = useState(false)
  const [result, setResult] = useState<{ plaintext: string } | null>(null)
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    apiFetch('/api/v1/enterprise/api-keys/scopes')
      .then(d => setAvailableScopes(d.data?.scopes ?? []))
      .catch(() => {})
  }, [])

  const toggleScope = (s: string) => {
    setSelectedScopes(prev =>
      prev.includes(s) ? prev.filter(x => x !== s) : [...prev, s]
    )
  }

  const handleCreate = async () => {
    if (!name.trim() || selectedScopes.length === 0) return
    setCreating(true)
    try {
      const d = await apiFetch('/api/v1/enterprise/api-keys', {
        method: 'POST',
        body: JSON.stringify({ name: name.trim(), description, scopes: selectedScopes, tier_name: tierName }),
      })
      setResult({ plaintext: d.data?.plaintext })
      onCreated()
    } catch (e: any) { alert(e.message) }
    finally { setCreating(false) }
  }

  const copy = () => {
    if (!result) return
    navigator.clipboard.writeText(result.plaintext)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <Modal onClose={result ? undefined : onClose}>
      {result ? (
        <>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 16 }}>
            <CheckCircle style={{ width: 20, height: 20, color: tokens.green }} />
            <h3 style={{ fontSize: 18, fontWeight: 600, color: tokens.label, margin: 0 }}>API Key Created</h3>
          </div>
          <div style={{ padding: 12, background: `${tokens.orange}10`, border: `0.5px solid ${tokens.orange}50`,
            borderRadius: tokens.radius.sm, marginBottom: 16, display: 'flex', gap: 8 }}>
            <AlertTriangle style={{ width: 16, height: 16, color: tokens.orange, flexShrink: 0, marginTop: 2 }} />
            <p style={{ fontSize: 13, color: tokens.label, margin: 0, lineHeight: 1.5 }}>
              Copy this key now — <strong>it will not be shown again.</strong> Store it in a secrets manager.
            </p>
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '10px 12px',
            background: tokens.fill, borderRadius: tokens.radius.sm, marginBottom: 20,
            fontFamily: 'SFMono-Regular,monospace', fontSize: 12, wordBreak: 'break-all' }}>
            <span style={{ flex: 1, color: tokens.label }}>{result.plaintext}</span>
            <button onClick={copy} style={{ background: 'none', border: 'none', cursor: 'pointer',
              color: copied ? tokens.green : tokens.blue, padding: 4, flexShrink: 0 }}>
              {copied ? <Check style={{ width: 16, height: 16 }} /> : <Copy style={{ width: 16, height: 16 }} />}
            </button>
          </div>
          <button onClick={onClose} style={{ ...btnStyle(tokens.blue), width: '100%', justifyContent: 'center' }}>
            Done — I've saved the key
          </button>
        </>
      ) : (
        <>
          <h3 style={{ fontSize: 20, fontWeight: 600, color: tokens.label, marginBottom: 20 }}>Create API Key</h3>

          <FieldLabel>Name *</FieldLabel>
          <Input value={name} onChange={setName} placeholder="e.g., ci-pipeline, grafana-prod" autoFocus />

          <FieldLabel style={{ marginTop: 14 }}>Description</FieldLabel>
          <Input value={description} onChange={setDescription} placeholder="Optional description" />

          <FieldLabel style={{ marginTop: 14 }}>Tier</FieldLabel>
          <select value={tierName} onChange={e => setTierName(e.target.value)}
            style={{ ...inputStyle, width: '100%' }}>
            <option value="free">Free (60 req/min)</option>
            <option value="standard">Standard (300 req/min)</option>
            <option value="enterprise">Enterprise (2000 req/min)</option>
          </select>

          <FieldLabel style={{ marginTop: 14 }}>Scopes *</FieldLabel>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginBottom: 20 }}>
            {(availableScopes.length ? availableScopes : ['alerts:read','alerts:write','incidents:read','incidents:write','admin']).map(s => {
              const active = selectedScopes.includes(s)
              const color = s === 'admin' ? tokens.red : s.endsWith(':write') ? tokens.orange : tokens.blue
              return (
                <button key={s} onClick={() => toggleScope(s)}
                  style={{
                    padding: '4px 10px', borderRadius: 10, border: `0.5px solid ${active ? color : tokens.separator}`,
                    background: active ? `${color}15` : tokens.fill,
                    color: active ? color : tokens.secondaryLabel,
                    fontSize: 12, fontWeight: active ? 600 : 400, cursor: 'pointer',
                  }}>
                  {active && <span style={{ marginRight: 4 }}>✓</span>}{s}
                </button>
              )
            })}
          </div>

          <div style={{ display: 'flex', gap: 8 }}>
            <button onClick={onClose} style={{ ...btnStyle(tokens.separator), flex: 1, justifyContent: 'center',
              color: tokens.secondaryLabel, border: `0.5px solid ${tokens.separator}` }}>
              Cancel
            </button>
            <button onClick={handleCreate} disabled={!name.trim() || creating || selectedScopes.length === 0}
              style={{ ...btnStyle(tokens.blue), flex: 1, justifyContent: 'center',
                opacity: (!name.trim() || creating || selectedScopes.length === 0) ? 0.5 : 1 }}>
              {creating ? 'Creating…' : 'Create Key'}
            </button>
          </div>
        </>
      )}
    </Modal>
  )
}

// ─── Webhooks tab ─────────────────────────────────────────────────────────────

function WebhooksTab() {
  const [subs, setSubs] = useState<WebhookSubscription[]>([])
  const [loading, setLoading] = useState(true)
  const [showCreate, setShowCreate] = useState(false)
  const [expandedId, setExpandedId] = useState<string | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const d = await apiFetch('/api/v1/enterprise/webhooks')
      setSubs(d.data?.subscriptions ?? [])
    } catch { setSubs([]) }
    finally { setLoading(false) }
  }, [])

  useEffect(() => { load() }, [load])

  const del = async (id: string, name: string) => {
    if (!confirm(`Delete webhook subscription "${name}"?`)) return
    try {
      await apiFetch(`/api/v1/enterprise/webhooks/${id}`, { method: 'DELETE' })
      load()
    } catch (e: any) { alert(e.message) }
  }

  const pause = async (id: string) => {
    try {
      await apiFetch(`/api/v1/enterprise/webhooks/${id}/pause`, {
        method: 'POST', body: JSON.stringify({ minutes: 60 })
      })
      load()
    } catch (e: any) { alert(e.message) }
  }

  const resume = async (id: string) => {
    try {
      await apiFetch(`/api/v1/enterprise/webhooks/${id}/resume`, { method: 'POST' })
      load()
    } catch (e: any) { alert(e.message) }
  }

  return (
    <>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <p style={{ fontSize: 14, color: tokens.secondaryLabel, margin: 0 }}>
          AlertHub will POST events to your registered URLs signed with HMAC-SHA256.
        </p>
        <button onClick={() => setShowCreate(true)} style={btnStyle(tokens.blue)}>
          <Plus style={{ width: 15, height: 15 }} /> Add Subscription
        </button>
      </div>

      <Card>
        {loading ? <EmptyState icon={<Globe />} title="Loading…" /> :
          subs.length === 0 ? (
            <EmptyState
              icon={<Globe />}
              title="No webhook subscriptions"
              subtitle="Subscribe to alert and incident events to receive real-time notifications."
              action={<button onClick={() => setShowCreate(true)} style={btnStyle(tokens.blue)}>Add Subscription</button>}
            />
          ) : subs.map(s => (
            <WebhookRow
              key={s.id} sub={s}
              expanded={expandedId === s.id}
              onExpand={() => setExpandedId(expandedId === s.id ? null : s.id)}
              onDelete={() => del(s.id, s.name)}
              onPause={() => pause(s.id)}
              onResume={() => resume(s.id)}
            />
          ))}
      </Card>

      {showCreate && <CreateWebhookModal onClose={() => setShowCreate(false)} onCreated={load} />}
    </>
  )
}

function WebhookRow({ sub, expanded, onExpand, onDelete, onPause, onResume }: {
  sub: WebhookSubscription
  expanded: boolean
  onExpand: () => void
  onDelete: () => void
  onPause: () => void
  onResume: () => void
}) {
  const [deliveries, setDeliveries] = useState<any[] | null>(null)
  const isPaused = !!(sub.paused_until && new Date(sub.paused_until) > new Date())

  const loadDeliveries = async () => {
    if (deliveries) return
    try {
      const d = await apiFetch(`/api/v1/enterprise/webhooks/${sub.id}/deliveries`)
      setDeliveries(d.data?.deliveries ?? [])
    } catch { setDeliveries([]) }
  }

  useEffect(() => { if (expanded) loadDeliveries() }, [expanded])

  const statusColor = (s: string) =>
    s === 'delivered' ? tokens.green : s === 'dead_lettered' ? tokens.red : s === 'pending' ? tokens.orange : tokens.blue

  const health = sub.total_deliveries === 0 ? null :
    Math.round((1 - sub.failed_deliveries / sub.total_deliveries) * 100)

  return (
    <div style={{ padding: 16, borderBottom: `0.5px solid ${tokens.separator}`, opacity: sub.is_active ? 1 : 0.55 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
        <div style={{ flex: 1, cursor: 'pointer' }} onClick={onExpand}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
            <Globe style={{ width: 14, height: 14, color: tokens.teal, flexShrink: 0 }} />
            <span style={{ fontSize: 14, fontWeight: 600, color: tokens.label }}>{sub.name}</span>
            {isPaused && <Badge label="Paused" color={tokens.orange} />}
            {!sub.is_active && <Badge label="Inactive" color={tokens.red} />}
            {sub.consecutive_failures >= 5 && <Badge label={`${sub.consecutive_failures} failures`} color={tokens.red} />}
            {expanded ? <ChevronDown style={{ width: 14, height: 14, color: tokens.tertiaryLabel }} />
              : <ChevronRight style={{ width: 14, height: 14, color: tokens.tertiaryLabel }} />}
          </div>
          <div style={{ fontFamily: 'SFMono-Regular,monospace', fontSize: 11, color: tokens.tertiaryLabel, marginBottom: 8 }}>
            {sub.target_url}
          </div>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4, marginBottom: 8 }}>
            {sub.event_types.length === 0
              ? <span style={{ fontSize: 11, color: tokens.tertiaryLabel }}>All events</span>
              : sub.event_types.map(e => <EventBadge key={e} type={e} />)
            }
          </div>
          <div style={{ display: 'flex', gap: 16, fontSize: 12, color: tokens.tertiaryLabel }}>
            <span>{sub.total_deliveries} delivered</span>
            {sub.failed_deliveries > 0 && <span style={{ color: tokens.red }}>{sub.failed_deliveries} failed</span>}
            {health !== null && <span style={{ color: health >= 95 ? tokens.green : health >= 80 ? tokens.orange : tokens.red }}>
              {health}% success
            </span>}
            {sub.last_delivery_at && <span>Last {fmt(sub.last_delivery_at)}</span>}
          </div>
        </div>

        <div style={{ display: 'flex', gap: 6, marginLeft: 12 }}>
          {isPaused
            ? <IconBtn onClick={onResume} title="Resume" color={tokens.green}><Activity style={{ width: 15, height: 15 }} /></IconBtn>
            : <IconBtn onClick={onPause} title="Pause 1h" color={tokens.orange}><Clock style={{ width: 15, height: 15 }} /></IconBtn>
          }
          <IconBtn onClick={onDelete} title="Delete" color={tokens.red}><Trash2 style={{ width: 15, height: 15 }} /></IconBtn>
        </div>
      </div>

      <AnimatePresence>
        {expanded && (
          <motion.div initial={{ height: 0, opacity: 0 }} animate={{ height: 'auto', opacity: 1 }} exit={{ height: 0, opacity: 0 }}
            style={{ overflow: 'hidden' }}>
            <div style={{ paddingTop: 12, borderTop: `0.5px solid ${tokens.separator}`, marginTop: 12 }}>
              {sub.description && <p style={{ fontSize: 13, color: tokens.secondaryLabel, marginBottom: 12 }}>{sub.description}</p>}
              <div style={{ fontSize: 12, color: tokens.tertiaryLabel, marginBottom: 12 }}>
                SSL verification: <strong style={{ color: tokens.label }}>{sub.verify_ssl ? 'enabled' : 'disabled'}</strong>
                {sub.paused_until && isPaused && (
                  <span style={{ marginLeft: 16 }}>Paused until {fmt(sub.paused_until)}</span>
                )}
              </div>
              <p style={{ fontSize: 12, fontWeight: 600, color: tokens.secondaryLabel, marginBottom: 8 }}>
                Recent deliveries
              </p>
              {deliveries === null ? (
                <p style={{ fontSize: 12, color: tokens.tertiaryLabel }}>Loading…</p>
              ) : deliveries.length === 0 ? (
                <p style={{ fontSize: 12, color: tokens.tertiaryLabel }}>No deliveries yet</p>
              ) : deliveries.slice(0, 10).map((d: any) => (
                <div key={d.id} style={{
                  display: 'flex', alignItems: 'center', gap: 10, padding: '6px 0',
                  borderBottom: `0.5px solid ${tokens.separator}`, fontSize: 12,
                }}>
                  <span style={{ width: 8, height: 8, borderRadius: '50%', background: statusColor(d.status), flexShrink: 0 }} />
                  <span style={{ flex: 1, color: tokens.label }}>{d.event_type}</span>
                  <span style={{ color: statusColor(d.status), fontWeight: 600 }}>{d.status}</span>
                  {d.response_status && <span style={{ color: tokens.tertiaryLabel }}>{d.response_status}</span>}
                  {d.response_latency_ms && <span style={{ color: tokens.tertiaryLabel }}>{d.response_latency_ms}ms</span>}
                  <span style={{ color: tokens.tertiaryLabel }}>{d.attempt_count}/{d.max_attempts}</span>
                </div>
              ))}
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  )
}

function CreateWebhookModal({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [name, setName] = useState('')
  const [url, setUrl] = useState('')
  const [description, setDescription] = useState('')
  const [selectedEvents, setSelectedEvents] = useState<string[]>([])
  const [verifySSL, setVerifySSL] = useState(true)
  const [creating, setCreating] = useState(false)
  const [result, setResult] = useState<{ signing_secret: string } | null>(null)
  const [copied, setCopied] = useState(false)
  const [events, setEvents] = useState<EventType[]>([])

  useEffect(() => {
    apiFetch('/api/v1/enterprise/events')
      .then(d => setEvents(d.data?.events ?? []))
      .catch(() => {})
  }, [])

  const toggleEvent = (e: string) => {
    setSelectedEvents(prev => prev.includes(e) ? prev.filter(x => x !== e) : [...prev, e])
  }

  const handleCreate = async () => {
    if (!name.trim() || !url.trim()) return
    setCreating(true)
    try {
      const d = await apiFetch('/api/v1/enterprise/webhooks', {
        method: 'POST',
        body: JSON.stringify({
          name: name.trim(), target_url: url.trim(),
          description, event_types: selectedEvents, verify_ssl: verifySSL,
        }),
      })
      setResult({ signing_secret: d.data?.signing_secret })
      onCreated()
    } catch (e: any) { alert(e.message) }
    finally { setCreating(false) }
  }

  const categories = Array.from(new Set(events.map(e => e.category)))

  return (
    <Modal onClose={result ? undefined : onClose} wide>
      {result ? (
        <>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 16 }}>
            <CheckCircle style={{ width: 20, height: 20, color: tokens.green }} />
            <h3 style={{ fontSize: 18, fontWeight: 600, color: tokens.label, margin: 0 }}>Subscription Created</h3>
          </div>
          <div style={{ padding: 12, background: `${tokens.orange}10`, border: `0.5px solid ${tokens.orange}50`,
            borderRadius: tokens.radius.sm, marginBottom: 16, display: 'flex', gap: 8 }}>
            <AlertTriangle style={{ width: 16, height: 16, color: tokens.orange, flexShrink: 0, marginTop: 2 }} />
            <p style={{ fontSize: 13, color: tokens.label, margin: 0, lineHeight: 1.5 }}>
              Store this signing secret now — <strong>it will not be shown again.</strong>
              Use it to verify <code>X-AlertHub-Signature</code> headers.
            </p>
          </div>
          <div style={{ fontFamily: 'SFMono-Regular,monospace', fontSize: 12, padding: '10px 12px',
            background: tokens.fill, borderRadius: tokens.radius.sm, marginBottom: 8,
            display: 'flex', alignItems: 'center', gap: 8, wordBreak: 'break-all' }}>
            <span style={{ flex: 1, color: tokens.label }}>{result.signing_secret}</span>
            <button onClick={() => { navigator.clipboard.writeText(result.signing_secret); setCopied(true); setTimeout(() => setCopied(false), 2000) }}
              style={{ background: 'none', border: 'none', cursor: 'pointer', color: copied ? tokens.green : tokens.blue, padding: 4, flexShrink: 0 }}>
              {copied ? <Check style={{ width: 15, height: 15 }} /> : <Copy style={{ width: 15, height: 15 }} />}
            </button>
          </div>
          <div style={{ fontSize: 11, color: tokens.tertiaryLabel, fontFamily: 'SFMono-Regular,monospace', marginBottom: 20 }}>
            HMAC-SHA256(payload, secret) == X-AlertHub-Signature header value (after stripping "sha256=")
          </div>
          <button onClick={onClose} style={{ ...btnStyle(tokens.blue), width: '100%', justifyContent: 'center' }}>
            Done
          </button>
        </>
      ) : (
        <>
          <h3 style={{ fontSize: 20, fontWeight: 600, color: tokens.label, marginBottom: 20 }}>
            Add Webhook Subscription
          </h3>

          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14, marginBottom: 14 }}>
            <div>
              <FieldLabel>Name *</FieldLabel>
              <Input value={name} onChange={setName} placeholder="My Webhook" autoFocus />
            </div>
            <div>
              <FieldLabel>Target URL *</FieldLabel>
              <Input value={url} onChange={setUrl} placeholder="https://your-app.example.com/webhook" />
            </div>
          </div>

          <FieldLabel>Description</FieldLabel>
          <Input value={description} onChange={setDescription} placeholder="Optional" />

          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 14, marginBottom: 14 }}>
            <input type="checkbox" id="ssl" checked={verifySSL} onChange={e => setVerifySSL(e.target.checked)} />
            <label htmlFor="ssl" style={{ fontSize: 13, color: tokens.secondaryLabel, cursor: 'pointer' }}>
              Verify SSL certificate
            </label>
          </div>

          <FieldLabel>Event types (leave empty to receive all events)</FieldLabel>
          <div style={{ marginBottom: 20, maxHeight: 200, overflowY: 'auto',
            border: `0.5px solid ${tokens.separator}`, borderRadius: tokens.radius.sm, padding: 8 }}>
            {categories.map(cat => (
              <div key={cat} style={{ marginBottom: 8 }}>
                <p style={{ fontSize: 11, fontWeight: 700, color: tokens.tertiaryLabel, margin: '0 0 4px 0',
                  textTransform: 'uppercase', letterSpacing: '0.05em' }}>{cat}</p>
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
                  {events.filter(e => e.category === cat).map(e => {
                    const active = selectedEvents.includes(e.event_type)
                    return (
                      <button key={e.event_type} onClick={() => toggleEvent(e.event_type)}
                        title={e.description}
                        style={{
                          padding: '3px 8px', borderRadius: 8,
                          border: `0.5px solid ${active ? tokens.blue : tokens.separator}`,
                          background: active ? `${tokens.blue}15` : tokens.fill,
                          color: active ? tokens.blue : tokens.secondaryLabel,
                          fontSize: 11, fontWeight: active ? 600 : 400, cursor: 'pointer',
                        }}>
                        {active && '✓ '}{e.event_type}
                      </button>
                    )
                  })}
                </div>
              </div>
            ))}
          </div>

          <div style={{ display: 'flex', gap: 8 }}>
            <button onClick={onClose} style={{ ...btnStyle(tokens.fill), flex: 1, justifyContent: 'center',
              color: tokens.secondaryLabel, border: `0.5px solid ${tokens.separator}` }}>
              Cancel
            </button>
            <button onClick={handleCreate} disabled={!name.trim() || !url.trim() || creating}
              style={{ ...btnStyle(tokens.blue), flex: 1, justifyContent: 'center',
                opacity: (!name.trim() || !url.trim() || creating) ? 0.5 : 1 }}>
              {creating ? 'Creating…' : 'Create Subscription'}
            </button>
          </div>
        </>
      )}
    </Modal>
  )
}

// ─── Rate limits tab ──────────────────────────────────────────────────────────

function RateLimitsTab() {
  const [myLimit, setMyLimit] = useState<RateLimitStatus | null>(null)
  const [tiers, setTiers] = useState<any[]>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    Promise.all([
      apiFetch('/api/v1/enterprise/rate-limits/me').catch(() => null),
      apiFetch('/api/v1/enterprise/rate-limits').catch(() => null),
    ]).then(([me, allTiers]) => {
      if (me?.data) setMyLimit(me.data)
      if (allTiers?.data?.tiers) setTiers(allTiers.data.tiers)
      setLoading(false)
    })
  }, [])

  if (loading) return <EmptyState icon={<Zap />} title="Loading…" />

  return (
    <>
      {myLimit && (
        <div style={{
          padding: 20, background: `${tokens.blue}08`,
          border: `0.5px solid ${tokens.blue}30`, borderRadius: tokens.radius.lg, marginBottom: 24,
        }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 16 }}>
            <Shield style={{ width: 18, height: 18, color: tokens.blue }} />
            <span style={{ fontSize: 15, fontWeight: 600, color: tokens.label }}>
              Your tier: {myLimit.display_name}
            </span>
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 12 }}>
            {[
              { label: 'Per minute', value: myLimit.requests_per_minute },
              { label: 'Per hour', value: myLimit.requests_per_hour },
              { label: 'Per day', value: myLimit.requests_per_day },
              { label: 'Burst', value: myLimit.burst_limit },
            ].map(({ label, value }) => (
              <div key={label} style={{ textAlign: 'center', padding: '12px 8px',
                background: tokens.secondaryBackground, borderRadius: tokens.radius.md }}>
                <div style={{ fontSize: 22, fontWeight: 700, color: tokens.blue }}>{value.toLocaleString()}</div>
                <div style={{ fontSize: 12, color: tokens.tertiaryLabel, marginTop: 4 }}>{label}</div>
              </div>
            ))}
          </div>
        </div>
      )}

      <h2 style={{ fontSize: 16, fontWeight: 600, color: tokens.label, marginBottom: 12 }}>All Tiers</h2>
      <Card>
        <div style={{ overflowX: 'auto' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
            <thead>
              <tr style={{ borderBottom: `0.5px solid ${tokens.separator}` }}>
                {['Tier', 'Per min', 'Per hour', 'Per day', 'Burst', 'AI req/min', 'Webhook ingress/min'].map(h => (
                  <th key={h} style={{ padding: '8px 12px', textAlign: 'left', color: tokens.tertiaryLabel,
                    fontSize: 11, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.05em' }}>{h}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {tiers.map(t => (
                <tr key={t.name} style={{
                  borderBottom: `0.5px solid ${tokens.separator}`,
                  background: myLimit?.tier_name === t.name ? `${tokens.blue}05` : 'transparent',
                }}>
                  <td style={{ padding: '10px 12px' }}>
                    <span style={{ fontWeight: 600, color: tokens.label }}>{t.display_name}</span>
                    {myLimit?.tier_name === t.name && (
                      <span style={{ marginLeft: 6, fontSize: 11, color: tokens.blue, fontWeight: 600 }}>← your tier</span>
                    )}
                  </td>
                  {[t.requests_per_minute, t.requests_per_hour, t.requests_per_day,
                    t.burst_limit, t.ai_requests_per_min, t.webhook_ingress_per_min].map((v, i) => (
                    <td key={i} style={{ padding: '10px 12px', color: tokens.secondaryLabel }}>{v.toLocaleString()}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Card>
    </>
  )
}

// ─── Shared UI primitives ─────────────────────────────────────────────────────

function Card({ children }: { children: React.ReactNode }) {
  return (
    <div style={{
      background: tokens.secondaryBackground, borderRadius: tokens.radius.lg,
      border: `0.5px solid ${tokens.separator}`, overflow: 'hidden',
    }}>
      {children}
    </div>
  )
}

function EmptyState({ icon, title, subtitle, action }: {
  icon: React.ReactNode; title: string; subtitle?: string; action?: React.ReactNode
}) {
  return (
    <div style={{ padding: 60, textAlign: 'center' }}>
      <div style={{ color: tokens.tertiaryLabel, width: 40, height: 40, margin: '0 auto 16px' }}>{icon}</div>
      <h3 style={{ fontSize: 17, fontWeight: 600, color: tokens.label, marginBottom: 6 }}>{title}</h3>
      {subtitle && <p style={{ fontSize: 14, color: tokens.secondaryLabel, marginBottom: 20 }}>{subtitle}</p>}
      {action}
    </div>
  )
}

function Modal({ children, onClose, wide }: { children: React.ReactNode; onClose?: () => void; wide?: boolean }) {
  return (
    <motion.div initial={{ opacity: 0 }} animate={{ opacity: 1 }}
      style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.4)',
        backdropFilter: 'blur(20px)', display: 'flex',
        alignItems: 'center', justifyContent: 'center', zIndex: 1000 }}
      onClick={onClose}>
      <motion.div initial={{ scale: 0.95 }} animate={{ scale: 1 }}
        onClick={e => e.stopPropagation()}
        style={{ background: tokens.secondaryBackground, borderRadius: tokens.radius.xl,
          padding: 28, width: '90%', maxWidth: wide ? 640 : 500, maxHeight: '90vh', overflowY: 'auto' }}>
        {onClose && (
          <button onClick={onClose} style={{ position: 'absolute', top: 12, right: 12,
            background: 'none', border: 'none', cursor: 'pointer', color: tokens.tertiaryLabel, padding: 4 }}>
            <X style={{ width: 18, height: 18 }} />
          </button>
        )}
        {children}
      </motion.div>
    </motion.div>
  )
}

function Badge({ label, color }: { label: string; color: string }) {
  return (
    <span style={{ padding: '2px 7px', borderRadius: 10, background: `${color}15`,
      fontSize: 11, fontWeight: 600, color, flexShrink: 0 }}>{label}</span>
  )
}

function EventBadge({ type }: { type: string }) {
  const parts = type.split('.')
  const cat = parts[0]
  const color = cat === 'alert' ? tokens.orange : cat === 'incident' ? tokens.red : cat === 'rca' ? tokens.purple : tokens.blue
  return <Badge label={type} color={color} />
}

function IconBtn({ children, onClick, title, color }: {
  children: React.ReactNode; onClick: () => void; title: string; color: string
}) {
  return (
    <button onClick={onClick} title={title}
      style={{ padding: 7, borderRadius: tokens.radius.sm, border: 'none',
        background: `${color}15`, color, cursor: 'pointer' }}>
      {children}
    </button>
  )
}

function FieldLabel({ children, style }: { children: React.ReactNode; style?: React.CSSProperties }) {
  return (
    <label style={{ display: 'block', fontSize: 13, fontWeight: 500,
      color: tokens.secondaryLabel, marginBottom: 6, ...style }}>
      {children}
    </label>
  )
}

const codeSnippetStyle: React.CSSProperties = {
  fontFamily: 'SFMono-Regular, ui-monospace, monospace',
  fontSize: 12, padding: '10px 14px', borderRadius: tokens.radius.sm,
  background: tokens.fill, border: `0.5px solid ${tokens.separator}`,
  color: tokens.secondaryLabel, marginBottom: 16,
}

const inputStyle: React.CSSProperties = {
  height: 38, borderRadius: tokens.radius.sm,
  border: `0.5px solid ${tokens.separator}`, background: tokens.fill,
  padding: '0 10px', fontSize: 13, color: tokens.label, outline: 'none', boxSizing: 'border-box',
}

function Input({ value, onChange, placeholder, autoFocus }: {
  value: string; onChange: (v: string) => void; placeholder?: string; autoFocus?: boolean
}) {
  return (
    <input value={value} onChange={e => onChange(e.target.value)}
      placeholder={placeholder} autoFocus={autoFocus}
      style={{ ...inputStyle, width: '100%' }} />
  )
}

function btnStyle(bg: string): React.CSSProperties {
  return {
    display: 'flex', alignItems: 'center', gap: 6,
    padding: '9px 16px', borderRadius: tokens.radius.sm,
    border: 'none', background: bg, color: bg === tokens.fill ? tokens.secondaryLabel : '#fff',
    fontSize: 13, fontWeight: 500, cursor: 'pointer',
  }
}

function fmt(iso: string) {
  return new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}
