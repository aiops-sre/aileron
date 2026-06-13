import React, { useState, useEffect, useRef, useCallback } from 'react'
import { motion, AnimatePresence } from 'framer-motion'
import {
  Users, Shield, Network, Activity, Brain, Radio, Monitor, Settings, Key,
  Plus, Pencil, Trash2, Search, X, Check, Loader2, RefreshCw,
  ChevronDown, ChevronRight, ToggleLeft, ToggleRight, Eye, EyeOff,
  Bell, BarChart, Ticket, Zap, GitBranch, Save, AlertCircle, TestTube,
  Server,
} from 'lucide-react'
import toast from 'react-hot-toast'
import { APIKeyManagementPage } from '@/pages/APIKeyManagementPage'

// Normalize any backend response to an array — handles [], {users:[]}, {data:[]}, {items:[]}, etc.
function toArr<T>(raw: any, ...keys: string[]): T[] {
  if (Array.isArray(raw)) return raw
  for (const k of [...keys, 'users', 'roles', 'mappings', 'sources', 'logs', 'configs',
                   'integrations', 'items', 'results', 'data', 'records']) {
    if (Array.isArray(raw?.[k])) return raw[k]
  }
  return []
}

import {
  usersApi, rolesApi, ldapMappingsApi, auditApi,
  integrationsApi, adminLLMApi, adminAlertSourcesApi, adminCorrelationApi,
  configApi, topologyApi, adminInfraApi,
} from '@/lib/api'
import api from '@/lib/api-axios'

const c = {
  blue: '#007AFF', green: '#34C759', red: '#FF3B30', orange: '#FF9500',
  purple: '#AF52DE', gray: '#8E8E93', yellow: '#FFCC00',
  label: 'var(--color-text)', secondary: 'var(--color-text-secondary)',
  tertiary: 'var(--color-text-tertiary, #8E8E93)',
  separator: 'var(--color-separator, rgba(142,142,147,.12))',
  fill: 'var(--color-fill, rgba(142,142,147,.08))',
  fill2: 'rgba(142,142,147,.12)', fill3: 'rgba(142,142,147,.06)',
  bg: 'var(--color-background)', card: 'var(--color-card, rgba(255,255,255,.8))',
  r: { sm: 6, md: 10, lg: 12, xl: 16 },
} as const

// ─── Shared primitives ───────────────────────────────────────────────────────

function Btn({
  onClick, disabled, loading, variant = 'primary', size = 'md', children, style: sx,
}: {
  onClick?: () => void; disabled?: boolean; loading?: boolean
  variant?: 'primary' | 'ghost' | 'danger'; size?: 'sm' | 'md'
  children: React.ReactNode; style?: React.CSSProperties
}) {
  const bg = variant === 'primary' ? c.blue : variant === 'danger' ? c.red : 'transparent'
  const textCol = variant === 'ghost' ? c.blue : '#fff'
  const border = variant === 'ghost' ? `1px solid ${c.blue}` : 'none'
  return (
    <button
      onClick={onClick}
      disabled={disabled || loading}
      style={{
        display: 'flex', alignItems: 'center', gap: 6,
        padding: size === 'sm' ? '5px 12px' : '8px 16px',
        fontSize: size === 'sm' ? 13 : 14, fontWeight: 500,
        borderRadius: c.r.sm, border, background: bg, color: textCol,
        cursor: disabled || loading ? 'not-allowed' : 'pointer',
        opacity: disabled || loading ? 0.6 : 1, transition: 'opacity .15s',
        ...sx,
      }}
    >
      {loading && <Loader2 style={{ width: 14, height: 14, animation: 'spin 1s linear infinite' }} />}
      {children}
    </button>
  )
}

function Field({
  label, value, onChange, type = 'text', placeholder, required, disabled,
  hint, children,
}: {
  label: string; value?: string; onChange?: (v: string) => void
  type?: string; placeholder?: string; required?: boolean; disabled?: boolean
  hint?: string; children?: React.ReactNode
}) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
      <label style={{ fontSize: 12, fontWeight: 600, color: c.secondary }}>{label}{required && ' *'}</label>
      {children ?? (
        <input
          type={type} value={value} placeholder={placeholder}
          required={required} disabled={disabled}
          onChange={(e) => onChange?.(e.target.value)}
          style={{
            padding: '8px 10px', borderRadius: c.r.sm, fontSize: 14, color: c.label,
            background: c.fill, border: `1px solid ${c.separator}`, outline: 'none',
          }}
        />
      )}
      {hint && <span style={{ fontSize: 11, color: c.tertiary }}>{hint}</span>}
    </div>
  )
}

function Badge({ text, color }: { text: string; color: string }) {
  return (
    <span style={{
      fontSize: 11, fontWeight: 600, padding: '2px 8px', borderRadius: 10,
      background: `${color}22`, color, border: `1px solid ${color}44`,
    }}>{text}</span>
  )
}

function Modal({ open, onClose, title, children, width = 480 }: {
  open: boolean; onClose: () => void; title: string
  children: React.ReactNode; width?: number
}) {
  if (!open) return null
  return (
    <div style={{
      position: 'fixed', inset: 0, background: 'rgba(0,0,0,.5)',
      display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000,
    }}
      onClick={(e) => { if (e.target === e.currentTarget) onClose() }}
    >
      <motion.div initial={{ opacity: 0, scale: .95 }} animate={{ opacity: 1, scale: 1 }}
        style={{
          background: c.card, borderRadius: c.r.lg, padding: 24, width: '90%',
          maxWidth: width, maxHeight: '90vh', overflowY: 'auto',
          boxShadow: '0 24px 80px rgba(0,0,0,.2)',
        }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 20 }}>
          <h3 style={{ fontSize: 17, fontWeight: 600, color: c.label, margin: 0 }}>{title}</h3>
          <button onClick={onClose} style={{ background: 'none', border: 'none', cursor: 'pointer', padding: 4 }}>
            <X style={{ width: 18, height: 18, color: c.tertiary }} />
          </button>
        </div>
        {children}
      </motion.div>
    </div>
  )
}

// ─── Types ───────────────────────────────────────────────────────────────────

interface User {
  id: string; username: string; email: string; full_name: string
  role_id: string; role_name: string; is_active: boolean
  last_login?: string; created_at: string
}
interface Role { id: string; name: string; description: string; is_system_role: boolean }

// ─── Sidebar ─────────────────────────────────────────────────────────────────

const NAV = [
  { id: 'users',         label: 'Users',           icon: Users,    color: c.blue   },
  { id: 'roles',         label: 'Roles',            icon: Shield,   color: c.purple },
  { id: 'ldap',          label: 'LDAP Mappings',    icon: Network,  color: c.orange },
  { id: 'audit',         label: 'Audit Logs',       icon: Activity, color: c.red    },
  { id: 'api-keys',      label: 'API Keys',         icon: Key,      color: c.blue   },
  { id: 'alert-sources', label: 'Alert Sources',    icon: Radio,    color: c.orange },
  { id: 'integrations',  label: 'Integrations',     icon: Monitor,  color: c.yellow },
  { id: 'infrastructure',label: 'Infrastructure',   icon: Server,   color: c.green  },
  { id: 'ai-llm',        label: 'AI & LLM',         icon: Brain,    color: c.purple },
  { id: 'settings',      label: 'Settings',         icon: Settings, color: c.gray   },
] as const
type SectionId = typeof NAV[number]['id']

function Sidebar({ active, onSelect, counts }: {
  active: SectionId; onSelect: (id: SectionId) => void
  counts: Partial<Record<SectionId, number>>
}) {
  return (
    <nav style={{ width: 200, flexShrink: 0, padding: '4px 0' }}>
      {NAV.map(({ id, label, icon: Icon, color }) => {
        const on = id === active
        return (
          <button key={id} onClick={() => onSelect(id)} style={{
            display: 'flex', alignItems: 'center', gap: 10, width: '100%',
            padding: '7px 10px', borderRadius: c.r.sm, border: 'none',
            background: on ? `${c.blue}18` : 'transparent',
            cursor: 'pointer', marginBottom: 1, textAlign: 'left',
            transition: 'background .12s',
          }}>
            <div style={{
              width: 26, height: 26, borderRadius: 6, background: color,
              display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0,
            }}>
              <Icon style={{ width: 14, height: 14, color: '#fff' }} />
            </div>
            <span style={{ fontSize: 13, fontWeight: on ? 600 : 400, color: on ? c.blue : c.label, flex: 1 }}>
              {label}
            </span>
            {counts[id] !== undefined && (
              <span style={{ fontSize: 11, color: c.tertiary, background: c.fill, padding: '1px 6px', borderRadius: 8 }}>
                {counts[id]}
              </span>
            )}
          </button>
        )
      })}
    </nav>
  )
}

// ─── Users Section ───────────────────────────────────────────────────────────

// Format a username/email-prefix into a readable display name.
// "ch_sri_nagendra" → "Ch Sri Nagendra", "vnakhate" → "Vnakhate"
function formatUsername(raw: string): string {
  return raw
    .split(/[_.\-]+/)
    .map(w => w.charAt(0).toUpperCase() + w.slice(1).toLowerCase())
    .join(' ')
}

function UsersSection({ roles }: { roles: Role[] }) {
  const [users, setUsers] = useState<User[]>([])
  const [loading, setLoading] = useState(true)
  const [q, setQ] = useState('')
  const [editing, setEditing] = useState<User | null>(null)
  const [creating, setCreating] = useState(false)
  const [createForm, setCreateForm] = useState({ username: '', email: '', full_name: '', password: '', role_id: '', is_active: true })
  const [saving, setSaving] = useState(false)
  const [photos, setPhotos] = useState<Record<string, string>>({})

  useEffect(() => { load() }, [])
  const load = async () => {
    setLoading(true)
    try {
      const r = await usersApi.list()
      const loaded: User[] = toArr(r.data?.data ?? r.data, 'users')
      setUsers(loaded)
      // Fetch photos in background; errors are silently ignored
      loaded.forEach(async (u) => {
        try {
          const pr = await fetch(`/api/v1/users/${u.id}/photo`, {
            credentials: 'include',
            headers: { Authorization: `Bearer ${sessionStorage.getItem('access_token') || localStorage.getItem('access_token') || ''}` },
          })
          if (pr.ok) {
            const pd = await pr.json()
            const url = pd?.data?.photo || pd?.data?.data?.photo || ''
            if (url) setPhotos(prev => ({ ...prev, [u.id]: url }))
          }
        } catch { /* silently ignore */ }
      })
    } catch { toast.error('Failed to load users') } finally { setLoading(false) }
  }

  const filtered = (users || []).filter(u =>
    !q || u.username?.toLowerCase().includes(q.toLowerCase()) ||
    u.email?.toLowerCase().includes(q.toLowerCase()) ||
    u.full_name?.toLowerCase().includes(q.toLowerCase())
  )

  const toggleActive = async (user: User) => {
    try {
      await usersApi.update(user.id, { is_active: !user.is_active })
      setUsers(prev => prev.map(u => u.id === user.id ? { ...u, is_active: !u.is_active } : u))
      toast.success(user.is_active ? 'User deactivated' : 'User activated')
    } catch { toast.error('Failed to update user') }
  }

  const deleteUser = async (id: string) => {
    if (!confirm('Delete this user?')) return
    try { await usersApi.delete(id); setUsers(prev => prev.filter(u => u.id !== id)); toast.success('User deleted') }
    catch { toast.error('Failed to delete user') }
  }

  const saveEdit = async () => {
    if (!editing) return
    setSaving(true)
    try {
      await usersApi.update(editing.id, { full_name: editing.full_name, email: editing.email, role_id: editing.role_id, is_active: editing.is_active })
      setUsers(prev => prev.map(u => u.id === editing.id ? editing : u))
      setEditing(null); toast.success('User saved')
    } catch { toast.error('Failed to save') } finally { setSaving(false) }
  }

  const createUser = async () => {
    if (!createForm.username || !createForm.email || !createForm.password) { toast.error('Username, email, password required'); return }
    setSaving(true)
    try {
      await usersApi.create(createForm as any)
      setCreating(false)
      setCreateForm({ username: '', email: '', full_name: '', password: '', role_id: '', is_active: true })
      await load(); toast.success('User created')
    } catch (e: any) { toast.error(e?.response?.data?.error || 'Failed to create user') } finally { setSaving(false) }
  }

  const displayName = (u: User) => u.full_name || formatUsername(u.username || u.email?.split('@')[0] || '??')
  const initials = (u: User) => displayName(u).split(' ').map(n => n[0]).join('').toUpperCase().slice(0, 2)
  const avatarColor = (u: User) => [c.blue, c.green, c.orange, c.purple, c.red][u.id?.charCodeAt(0) % 5] || c.blue

  return (
    <div>
      <div style={{ display: 'flex', gap: 10, marginBottom: 16, alignItems: 'center' }}>
        <div style={{ flex: 1, position: 'relative' }}>
          <Search style={{ position: 'absolute', left: 10, top: '50%', transform: 'translateY(-50%)', width: 16, height: 16, color: c.tertiary }} />
          <input value={q} onChange={e => setQ(e.target.value)} placeholder="Search users…"
            style={{ width: '100%', paddingLeft: 34, paddingRight: 10, height: 36, borderRadius: c.r.sm, fontSize: 14, border: `1px solid ${c.separator}`, background: c.fill, color: c.label, outline: 'none', boxSizing: 'border-box' }} />
        </div>
        <Btn onClick={() => setCreating(true)} size="sm"><Plus style={{ width: 14, height: 14 }} /> Add User</Btn>
        <Btn onClick={load} variant="ghost" size="sm"><RefreshCw style={{ width: 14, height: 14 }} /></Btn>
      </div>

      {loading ? (
        <div style={{ textAlign: 'center', padding: 40, color: c.tertiary }}>
          <Loader2 style={{ width: 24, height: 24, animation: 'spin 1s linear infinite', margin: '0 auto' }} />
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          {filtered.map(u => (
            <div key={u.id} style={{
              display: 'flex', alignItems: 'center', gap: 12, padding: '10px 14px',
              borderRadius: c.r.md, background: c.card, border: `1px solid ${c.separator}`,
              opacity: u.is_active ? 1 : 0.5,
            }}>
              <div style={{ width: 36, height: 36, borderRadius: '50%', background: avatarColor(u), display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 13, fontWeight: 600, color: '#fff', flexShrink: 0, overflow: 'hidden' }}>
                {photos[u.id]
                  ? <img src={photos[u.id]} alt={displayName(u)} style={{ width: '100%', height: '100%', objectFit: 'cover' }} />
                  : initials(u)
                }
              </div>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontSize: 14, fontWeight: 500, color: c.label }}>{displayName(u)}</div>
                <div style={{ fontSize: 12, color: c.tertiary, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{u.email}</div>
              </div>
              <Badge text={u.role_name || 'No Role'} color={c.blue} />
              <button onClick={() => toggleActive(u)} style={{ background: 'none', border: 'none', cursor: 'pointer', color: u.is_active ? c.green : c.gray, padding: 4 }}>
                {u.is_active ? <ToggleRight style={{ width: 22, height: 22 }} /> : <ToggleLeft style={{ width: 22, height: 22 }} />}
              </button>
              <button onClick={() => setEditing({ ...u })} style={{ background: 'none', border: 'none', cursor: 'pointer', color: c.tertiary, padding: 4 }}>
                <Pencil style={{ width: 14, height: 14 }} />
              </button>
              <button onClick={() => deleteUser(u.id)} style={{ background: 'none', border: 'none', cursor: 'pointer', color: c.red, padding: 4 }}>
                <Trash2 style={{ width: 14, height: 14 }} />
              </button>
            </div>
          ))}
          {filtered.length === 0 && <div style={{ textAlign: 'center', padding: 40, color: c.tertiary, fontSize: 14 }}>No users found</div>}
        </div>
      )}

      <Modal open={!!editing} onClose={() => setEditing(null)} title="Edit User">
        {editing && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
            <Field label="Full Name" value={editing.full_name} onChange={v => setEditing({ ...editing, full_name: v })} placeholder="Full name" />
            <Field label="Email" value={editing.email} onChange={v => setEditing({ ...editing, email: v })} type="email" placeholder="user@example.com" />
            <Field label="Role">
              <select value={editing.role_id} onChange={e => setEditing({ ...editing, role_id: e.target.value })}
                style={{ padding: '8px 10px', borderRadius: c.r.sm, fontSize: 14, border: `1px solid ${c.separator}`, background: c.fill, color: c.label }}>
                <option value="">No Role</option>
                {(roles || []).map(r => <option key={r.id} value={r.id}>{r.name}</option>)}
              </select>
            </Field>
            <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 14, color: c.label, cursor: 'pointer' }}>
              <input type="checkbox" checked={editing.is_active} onChange={e => setEditing({ ...editing, is_active: e.target.checked })} />
              Active
            </label>
            <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end', marginTop: 8 }}>
              <Btn variant="ghost" onClick={() => setEditing(null)}>Cancel</Btn>
              <Btn onClick={saveEdit} loading={saving}>Save</Btn>
            </div>
          </div>
        )}
      </Modal>

      <Modal open={creating} onClose={() => setCreating(false)} title="Create User">
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <Field label="Username" required value={createForm.username} onChange={v => setCreateForm(f => ({ ...f, username: v }))} placeholder="jdoe" />
          <Field label="Full Name" value={createForm.full_name} onChange={v => setCreateForm(f => ({ ...f, full_name: v }))} placeholder="Jane Doe" />
          <Field label="Email" required value={createForm.email} onChange={v => setCreateForm(f => ({ ...f, email: v }))} type="email" placeholder="jdoe@example.com" />
          <Field label="Password" required value={createForm.password} onChange={v => setCreateForm(f => ({ ...f, password: v }))} type="password" placeholder="••••••••" />
          <Field label="Role">
            <select value={createForm.role_id} onChange={e => setCreateForm(f => ({ ...f, role_id: e.target.value }))}
              style={{ padding: '8px 10px', borderRadius: c.r.sm, fontSize: 14, border: `1px solid ${c.separator}`, background: c.fill, color: c.label }}>
              <option value="">No Role</option>
              {(roles || []).map(r => <option key={r.id} value={r.id}>{r.name}</option>)}
            </select>
          </Field>
          <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end', marginTop: 8 }}>
            <Btn variant="ghost" onClick={() => setCreating(false)}>Cancel</Btn>
            <Btn onClick={createUser} loading={saving}>Create</Btn>
          </div>
        </div>
      </Modal>
    </div>
  )
}

// ─── Roles Section ───────────────────────────────────────────────────────────

function PermissionsModal({ roleId, roleName, onClose }: { roleId: string; roleName: string; onClose: () => void }) {
  const [allPerms, setAllPerms] = useState<any[]>([])
  const [grouped, setGrouped] = useState<Record<string, any[]>>({})
  const [assigned, setAssigned] = useState<Set<string>>(new Set())
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    const load = async () => {
      setLoading(true)
      try {
        const [allRes, roleRes] = await Promise.all([
          api.get('/permissions'),
          rolesApi.getPermissions(roleId),
        ])
        const perms = toArr(allRes.data?.data ?? allRes.data, 'permissions')
        const grp: Record<string, any[]> = allRes.data?.data?.grouped || {}
        if (Object.keys(grp).length === 0) {
          perms.forEach((p: any) => { grp[p.resource] = [...(grp[p.resource] || []), p] })
        }
        setAllPerms(perms)
        setGrouped(grp)
        const rolePerms = toArr(roleRes.data?.data ?? roleRes.data, 'permissions')
        setAssigned(new Set(rolePerms.map((p: any) => p.id)))
      } catch { toast.error('Failed to load permissions') }
      finally { setLoading(false) }
    }
    load()
  }, [roleId])

  const toggle = (id: string) => setAssigned(prev => {
    const next = new Set(prev)
    next.has(id) ? next.delete(id) : next.add(id)
    return next
  })

  const save = async () => {
    setSaving(true)
    try {
      await rolesApi.assignPermissions(roleId, Array.from(assigned))
      toast.success('Permissions saved')
      onClose()
    } catch { toast.error('Failed to save permissions') }
    finally { setSaving(false) }
  }

  const actionColor = (a: string) => ({ view: c.blue, create: c.green, update: c.orange, delete: c.red })[a] || c.gray

  return (
    <Modal open onClose={onClose} title={`Permissions — ${roleName}`} width={600}>
      {loading ? (
        <div style={{ display: 'flex', justifyContent: 'center', padding: 32 }}>
          <Loader2 style={{ width: 22, height: 22, animation: 'spin 1s linear infinite', color: c.blue }} />
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          {/* Select All / None */}
          <div style={{ display: 'flex', gap: 8 }}>
            <Btn size="sm" variant="ghost" onClick={() => setAssigned(new Set(allPerms.map((p: any) => p.id)))}>Select All</Btn>
            <Btn size="sm" variant="ghost" onClick={() => setAssigned(new Set())}>Clear All</Btn>
            <span style={{ fontSize: 12, color: c.tertiary, marginLeft: 'auto', alignSelf: 'center' }}>{assigned.size} / {allPerms.length} selected</span>
          </div>

          {/* Permissions grouped by resource */}
          <div style={{ maxHeight: 420, overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: 12 }}>
            {Object.entries(grouped).sort(([a], [b]) => a.localeCompare(b)).map(([resource, perms]) => (
              <div key={resource} style={{ padding: 12, background: c.fill, borderRadius: c.r.md, border: `1px solid ${c.separator}` }}>
                <div style={{ fontSize: 12, fontWeight: 700, color: c.label, textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 10 }}>
                  {resource}
                </div>
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
                  {(perms as any[]).map(p => (
                    <label key={p.id} style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '5px 10px', borderRadius: c.r.sm, border: `1px solid ${assigned.has(p.id) ? actionColor(p.action) + '60' : c.separator}`, background: assigned.has(p.id) ? actionColor(p.action) + '12' : c.card, cursor: 'pointer', transition: 'all .15s', userSelect: 'none' }}>
                      <input type="checkbox" checked={assigned.has(p.id)} onChange={() => toggle(p.id)} style={{ accentColor: actionColor(p.action) }} />
                      <span style={{ fontSize: 12, color: assigned.has(p.id) ? actionColor(p.action) : c.secondary, fontWeight: assigned.has(p.id) ? 600 : 400 }}>
                        {p.action}
                      </span>
                    </label>
                  ))}
                </div>
              </div>
            ))}
            {allPerms.length === 0 && (
              <div style={{ textAlign: 'center', padding: 32, color: c.tertiary, fontSize: 13 }}>
                No permissions in the system. Run the seed migration to populate them.
              </div>
            )}
          </div>

          <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end', marginTop: 4, borderTop: `1px solid ${c.separator}`, paddingTop: 16 }}>
            <Btn variant="ghost" onClick={onClose}>Cancel</Btn>
            <Btn onClick={save} loading={saving}><Check style={{ width: 14, height: 14 }} /> Save Permissions</Btn>
          </div>
        </div>
      )}
    </Modal>
  )
}

function RolesSection({ roles, onRolesChange }: { roles: Role[]; onRolesChange: (r: Role[]) => void }) {
  const [loading, setLoading] = useState(false)
  const [editing, setEditing] = useState<{ id?: string; name: string; description: string } | null>(null)
  const [saving, setSaving] = useState(false)
  const [permRoleId, setPermRoleId] = useState<{ id: string; name: string } | null>(null)

  const save = async () => {
    if (!editing?.name) { toast.error('Name required'); return }
    setSaving(true)
    try {
      if (editing.id) {
        await rolesApi.update(editing.id, { name: editing.name, description: editing.description })
        onRolesChange((roles || []).map(r => r.id === editing.id ? { ...r, ...editing } : r))
        toast.success('Role updated')
      } else {
        const res = await rolesApi.create({ name: editing.name, description: editing.description })
        const newRole = res.data?.data ?? res.data
        onRolesChange([...(roles || []), newRole])
        toast.success('Role created')
      }
      setEditing(null)
    } catch (e: any) { toast.error(e?.response?.data?.error || e?.response?.data?.message || 'Failed to save') } finally { setSaving(false) }
  }

  const del = async (id: string) => {
    if (!confirm('Delete this role?')) return
    try { await rolesApi.delete(id); onRolesChange((roles || []).filter(r => r.id !== id)); toast.success('Role deleted') }
    catch { toast.error('Cannot delete system role or role in use') }
  }

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 16 }}>
        <Btn size="sm" onClick={() => setEditing({ name: '', description: '' })}>
          <Plus style={{ width: 14, height: 14 }} /> New Role
        </Btn>
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
        {(roles || []).map(r => (
          <div key={r.id} style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '12px 14px', borderRadius: c.r.md, background: c.card, border: `1px solid ${c.separator}` }}>
            <div style={{ width: 36, height: 36, borderRadius: c.r.sm, background: `${c.purple}22`, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
              <Shield style={{ width: 18, height: 18, color: c.purple }} />
            </div>
            <div style={{ flex: 1 }}>
              <div style={{ fontSize: 14, fontWeight: 600, color: c.label }}>{r.name}</div>
              <div style={{ fontSize: 12, color: c.tertiary }}>{r.description || 'No description'}</div>
            </div>
            {r.is_system_role && <Badge text="System" color={c.gray} />}
            <Btn size="sm" variant="ghost" onClick={() => setPermRoleId({ id: r.id, name: r.name })}>
              <Shield style={{ width: 12, height: 12 }} /> Permissions
            </Btn>
            {!r.is_system_role && (
              <>
                <button onClick={() => setEditing({ id: r.id, name: r.name, description: r.description })} style={{ background: 'none', border: 'none', cursor: 'pointer', color: c.tertiary, padding: 4 }}>
                  <Pencil style={{ width: 14, height: 14 }} />
                </button>
                <button onClick={() => del(r.id)} style={{ background: 'none', border: 'none', cursor: 'pointer', color: c.red, padding: 4 }}>
                  <Trash2 style={{ width: 14, height: 14 }} />
                </button>
              </>
            )}
          </div>
        ))}
        {roles.length === 0 && <div style={{ textAlign: 'center', padding: 40, color: c.tertiary, fontSize: 14 }}>No roles defined</div>}
      </div>

      {permRoleId && (
        <PermissionsModal roleId={permRoleId.id} roleName={permRoleId.name} onClose={() => setPermRoleId(null)} />
      )}

      <Modal open={!!editing} onClose={() => setEditing(null)} title={editing?.id ? 'Edit Role' : 'New Role'}>
        {editing && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
            <Field label="Name" required value={editing.name} onChange={v => setEditing({ ...editing, name: v })} placeholder="e.g. sre-lead" />
            <Field label="Description" value={editing.description} onChange={v => setEditing({ ...editing, description: v })} placeholder="Optional description" />
            <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end', marginTop: 8 }}>
              <Btn variant="ghost" onClick={() => setEditing(null)}>Cancel</Btn>
              <Btn onClick={save} loading={saving}>Save</Btn>
            </div>
          </div>
        )}
      </Modal>
    </div>
  )
}

// ─── LDAP Mappings Section ────────────────────────────────────────────────────

function LDAPSection({ roles }: { roles: Role[] }) {
  const [mappings, setMappings] = useState<any[]>([])
  const [loading, setLoading] = useState(true)
  const [form, setForm] = useState({ ldap_group: '', role_id: '' })
  const [suggestions, setSuggestions] = useState<string[]>([])
  const [showSuggest, setShowSuggest] = useState(false)
  const [suggestLoading, setSuggestLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const debounce = useRef<ReturnType<typeof setTimeout>>()

  useEffect(() => { load() }, [])
  const load = async () => {
    setLoading(true)
    try { const r = await ldapMappingsApi.list(); setMappings(toArr(r.data?.data ?? r.data, 'mappings')) } catch { toast.error('Failed to load LDAP mappings') } finally { setLoading(false) }
  }

  const fetchGroups = useCallback(async (q: string) => {
    setSuggestLoading(true)
    try {
      const r = await api.get(`/admin/ldap/groups/search?q=${encodeURIComponent(q)}`)
      const groups = r.data?.data?.groups || r.data?.groups || []
      setSuggestions(groups)
      setShowSuggest(true)
    } catch { setSuggestions([]) } finally { setSuggestLoading(false) }
  }, [])

  const onGroupChange = useCallback((v: string) => {
    setForm(f => ({ ...f, ldap_group: v }))
    clearTimeout(debounce.current)
    debounce.current = setTimeout(() => fetchGroups(v), 250)
  }, [fetchGroups])

  const onGroupFocus = useCallback(() => {
    if (suggestions.length > 0) { setShowSuggest(true); return }
    fetchGroups(form.ldap_group)
  }, [suggestions, form.ldap_group, fetchGroups])

  const addMapping = async () => {
    if (!form.ldap_group || !form.role_id) { toast.error('Group and role required'); return }
    setSaving(true)
    try {
      await ldapMappingsApi.upsert(form)
      await load()
      setForm({ ldap_group: '', role_id: '' })
      toast.success('Mapping saved')
    } catch { toast.error('Failed to save mapping') } finally { setSaving(false) }
  }

  const del = async (id: string) => {
    if (!confirm('Remove this mapping?')) return
    try { await ldapMappingsApi.delete(id); setMappings(prev => prev.filter(m => m.id !== id)); toast.success('Mapping removed') }
    catch { toast.error('Failed to remove mapping') }
  }

  return (
    <div>
      {/* Add form */}
      <div style={{ padding: 16, background: c.card, borderRadius: c.r.md, border: `1px solid ${c.separator}`, marginBottom: 16 }}>
        <div style={{ fontSize: 13, fontWeight: 600, color: c.label, marginBottom: 12 }}>Add / Update Mapping</div>
        <div style={{ display: 'flex', gap: 10, alignItems: 'flex-end', flexWrap: 'wrap' }}>
          <div style={{ flex: 1, minWidth: 200, position: 'relative' }}>
            <label style={{ fontSize: 12, fontWeight: 600, color: c.secondary, display: 'block', marginBottom: 4 }}>LDAP Group</label>
            <input value={form.ldap_group} onChange={e => onGroupChange(e.target.value)}
              onBlur={() => setTimeout(() => setShowSuggest(false), 150)}
              onFocus={onGroupFocus}
              placeholder="Click to browse or type to search…"
              style={{ width: '100%', padding: '8px 10px', borderRadius: c.r.sm, fontSize: 14, border: `1px solid ${c.separator}`, background: c.fill, color: c.label, outline: 'none', boxSizing: 'border-box' }} />
            {showSuggest && (
              <div style={{ position: 'absolute', top: '100%', left: 0, right: 0, background: c.card, border: `1px solid ${c.separator}`, borderRadius: c.r.sm, boxShadow: '0 8px 24px rgba(0,0,0,.15)', zIndex: 100, maxHeight: 220, overflowY: 'auto' }}>
                {suggestLoading ? (
                  <div style={{ padding: '10px 12px', fontSize: 13, color: c.tertiary, display: 'flex', alignItems: 'center', gap: 6 }}>
                    <Loader2 style={{ width: 12, height: 12, animation: 'spin 1s linear infinite' }} /> Loading groups from directory…
                  </div>
                ) : suggestions.length === 0 ? (
                  <div style={{ padding: '10px 12px', fontSize: 13, color: c.tertiary }}>No groups found</div>
                ) : suggestions.map(s => (
                  <button key={s} onMouseDown={() => { setForm(f => ({ ...f, ldap_group: s })); setShowSuggest(false) }}
                    style={{ display: 'block', width: '100%', padding: '8px 12px', textAlign: 'left', background: 'none', border: 'none', fontSize: 13, color: c.label, cursor: 'pointer', borderBottom: `1px solid ${c.separator}` }}>
                    {s}
                  </button>
                ))}
              </div>
            )}
          </div>
          <div style={{ flex: 1, minWidth: 150 }}>
            <label style={{ fontSize: 12, fontWeight: 600, color: c.secondary, display: 'block', marginBottom: 4 }}>Role</label>
            <select value={form.role_id} onChange={e => setForm(f => ({ ...f, role_id: e.target.value }))}
              style={{ width: '100%', padding: '8px 10px', borderRadius: c.r.sm, fontSize: 14, border: `1px solid ${c.separator}`, background: c.fill, color: c.label }}>
              <option value="">Select role…</option>
              {(roles || []).map(r => <option key={r.id} value={r.id}>{r.name}</option>)}
            </select>
          </div>
          <Btn onClick={addMapping} loading={saving}>
            <Save style={{ width: 14, height: 14 }} /> Save Mapping
          </Btn>
        </div>
      </div>

      {loading ? <div style={{ textAlign: 'center', padding: 40 }}><Loader2 style={{ width: 24, height: 24, animation: 'spin 1s linear infinite', color: c.blue, margin: '0 auto' }} /></div> : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          {(mappings || []).map(m => (
            <div key={m.id} style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '10px 14px', borderRadius: c.r.md, background: c.card, border: `1px solid ${c.separator}` }}>
              <Network style={{ width: 16, height: 16, color: c.orange, flexShrink: 0 }} />
              <span style={{ flex: 1, fontSize: 14, color: c.label, fontFamily: 'monospace' }}>{m.ldap_group}</span>
              <ChevronRight style={{ width: 14, height: 14, color: c.tertiary }} />
              <Badge text={m.role_name || m.role_id} color={c.purple} />
              <button onClick={() => del(m.id)} style={{ background: 'none', border: 'none', cursor: 'pointer', color: c.red, padding: 4, marginLeft: 4 }}>
                <Trash2 style={{ width: 14, height: 14 }} />
              </button>
            </div>
          ))}
          {mappings.length === 0 && <div style={{ textAlign: 'center', padding: 40, color: c.tertiary, fontSize: 14 }}>No LDAP group mappings configured</div>}
        </div>
      )}
    </div>
  )
}

// ─── Audit Logs Section ───────────────────────────────────────────────────────

function AuditSection() {
  const [logs, setLogs] = useState<any[]>([])
  const [loading, setLoading] = useState(true)
  const [q, setQ] = useState('')
  const [filterAction, setFilterAction] = useState('')

  useEffect(() => { load() }, [])
  const load = async () => {
    setLoading(true)
    try { const r = await auditApi.getLogs({ limit: 200 }); setLogs(toArr(r.data?.data ?? r.data, 'logs')) }
    catch { toast.error('Failed to load audit logs') } finally { setLoading(false) }
  }

  const actionMeta: Record<string, { label: string; color: string; icon: string }> = {
    login:               { label: 'Login',             color: c.purple, icon: '🔐' },
    logout:              { label: 'Logout',            color: c.gray,   icon: '🚪' },
    login_failed:        { label: 'Login Failed',      color: c.red,    icon: '⚠️' },
    user_created:        { label: 'User Created',      color: c.green,  icon: '👤' },
    user_updated:        { label: 'User Updated',      color: c.blue,   icon: '✏️' },
    user_deleted:        { label: 'User Deleted',      color: c.red,    icon: '🗑️' },
    role_created:        { label: 'Role Created',      color: c.green,  icon: '🛡️' },
    role_updated:        { label: 'Role Updated',      color: c.blue,   icon: '🛡️' },
    role_deleted:        { label: 'Role Deleted',      color: c.red,    icon: '🛡️' },
    role_assigned:       { label: 'Role Assigned',     color: c.orange, icon: '🔗' },
    permissions_updated: { label: 'Permissions',       color: c.orange, icon: '🔑' },
    settings_updated:    { label: 'Settings',          color: c.blue,   icon: '⚙️' },
    ldap_mapping_upserted:{ label: 'LDAP Mapping',    color: c.orange, icon: '🌐' },
    ldap_mapping_deleted: { label: 'LDAP Removed',    color: c.red,    icon: '🌐' },
    alert_source_created:{ label: 'Source Added',     color: c.green,  icon: '📡' },
    alert_source_deleted:{ label: 'Source Removed',   color: c.red,    icon: '📡' },
  }

  const getActionMeta = (action: string) =>
    actionMeta[action] || { label: action?.replace(/_/g, ' ') || '—', color: c.gray, icon: '📋' }

  const formatDetails = (action: string, details: any): string => {
    if (!details) return ''
    const d = typeof details === 'string' ? (() => { try { return JSON.parse(details) } catch { return null } })() : details
    if (!d) return typeof details === 'string' ? details : ''

    if (action === 'settings_updated') {
      const keys = d.settings_keys || d.keys || []
      return `Updated ${keys.length} setting${keys.length !== 1 ? 's' : ''}`
    }
    if (action === 'permissions_updated') return `${d.count ?? 0} permissions assigned`
    if (action === 'role_created' || action === 'role_updated') return `Role: ${d.role_name || d.name || ''}`
    if (action === 'role_assigned') return `Role → ${d.role || ''}`
    if (action === 'user_created') return `User: ${d.username || d.email || ''}`
    if (action === 'ldap_mapping_upserted') return `${d.ldap_group || ''} → role`
    if (action === 'login_failed') return `User: ${d.username || d.email || ''}`
    if (action === 'alert_source_created') return `Source: ${d.name || ''}`

    // Fallback: pick 1-2 meaningful keys
    const meaningful = Object.entries(d).filter(([k]) => !['id', 'user_id', 'created_at', 'updated_at'].includes(k)).slice(0, 2)
    return meaningful.map(([k, v]) => `${k}: ${String(v).slice(0, 40)}`).join(', ')
  }

  const formatActor = (l: any): string => {
    if (l.username && l.username !== 'system') return l.username
    if (l.user_email) return l.user_email
    if (l.resource_type === 'auth' || !l.user_id) return 'system'
    // UUID — show first 8 chars
    return l.user_id ? `user-${String(l.user_id).slice(0, 8)}` : 'system'
  }

  const relativeTime = (ts: string): string => {
    if (!ts) return '—'
    const diff = Date.now() - new Date(ts).getTime()
    const m = Math.floor(diff / 60000)
    if (m < 1) return 'just now'
    if (m < 60) return `${m}m ago`
    const h = Math.floor(m / 60)
    if (h < 24) return `${h}h ago`
    const d = Math.floor(h / 24)
    if (d < 7) return `${d}d ago`
    return new Date(ts).toLocaleDateString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
  }

  const uniqueActions = Array.from(new Set(logs.map(l => l.action).filter(Boolean))).sort()

  const filtered = logs.filter(l => {
    if (filterAction && l.action !== filterAction) return false
    if (!q) return true
    const det = formatDetails(l.action, l.details)
    return JSON.stringify(l).toLowerCase().includes(q.toLowerCase()) || det.toLowerCase().includes(q.toLowerCase())
  })

  return (
    <div>
      <div style={{ display: 'flex', gap: 8, marginBottom: 16, flexWrap: 'wrap' }}>
        <div style={{ flex: 1, minWidth: 200, position: 'relative' }}>
          <Search style={{ position: 'absolute', left: 10, top: '50%', transform: 'translateY(-50%)', width: 16, height: 16, color: c.tertiary }} />
          <input value={q} onChange={e => setQ(e.target.value)} placeholder="Search logs…"
            style={{ width: '100%', paddingLeft: 34, paddingRight: 10, height: 36, borderRadius: c.r.sm, fontSize: 14, border: `1px solid ${c.separator}`, background: c.fill, color: c.label, outline: 'none', boxSizing: 'border-box' }} />
        </div>
        <select value={filterAction} onChange={e => setFilterAction(e.target.value)}
          style={{ height: 36, borderRadius: c.r.sm, border: `1px solid ${c.separator}`, background: c.fill, color: filterAction ? c.label : c.tertiary, fontSize: 13, padding: '0 10px', outline: 'none' }}>
          <option value="">All actions</option>
          {uniqueActions.map(a => <option key={a} value={a}>{getActionMeta(a).label}</option>)}
        </select>
        <Btn onClick={load} variant="ghost" size="sm" loading={loading}><RefreshCw style={{ width: 14, height: 14 }} /></Btn>
      </div>

      {loading ? (
        <div style={{ textAlign: 'center', padding: 40 }}>
          <Loader2 style={{ width: 24, height: 24, animation: 'spin 1s linear infinite', color: c.blue, margin: '0 auto' }} />
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
          {filtered.slice(0, 150).map((l, i) => {
            const meta = getActionMeta(l.action || '')
            const detail = formatDetails(l.action, l.details)
            const actor = formatActor(l)
            return (
              <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '8px 12px', borderRadius: c.r.sm, background: c.card, border: `1px solid ${c.separator}` }}>
                {/* Colored dot */}
                <div style={{ width: 8, height: 8, borderRadius: '50%', background: meta.color, flexShrink: 0 }} />
                {/* Timestamp */}
                <span title={l.created_at ? new Date(l.created_at).toLocaleString() : ''} style={{ fontSize: 11, color: c.tertiary, width: 80, flexShrink: 0, cursor: 'help' }}>
                  {relativeTime(l.created_at)}
                </span>
                {/* Action badge */}
                <span style={{ fontSize: 12, fontWeight: 600, color: meta.color, width: 130, flexShrink: 0, display: 'flex', alignItems: 'center', gap: 4 }}>
                  <span>{meta.icon}</span>
                  <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{meta.label}</span>
                </span>
                {/* Detail */}
                <span style={{ fontSize: 13, color: c.label, flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {detail || `${l.resource_type || ''}${l.resource_id ? ` #${String(l.resource_id).slice(0, 8)}` : ''}`}
                </span>
                {/* Actor */}
                <span style={{ fontSize: 11, color: c.tertiary, flexShrink: 0, maxWidth: 140, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {actor}
                </span>
                {/* IP (optional) */}
                {l.ip_address && l.ip_address !== '::1' && l.ip_address !== '127.0.0.1' && (
                  <span style={{ fontSize: 10, color: c.tertiary, flexShrink: 0 }}>{l.ip_address}</span>
                )}
              </div>
            )
          })}
          {filtered.length === 0 && <div style={{ textAlign: 'center', padding: 40, color: c.tertiary, fontSize: 14 }}>No audit logs found</div>}
          {filtered.length > 150 && <div style={{ textAlign: 'center', padding: 8, color: c.tertiary, fontSize: 12 }}>Showing 150 of {filtered.length} entries — refine search to narrow results</div>}
        </div>
      )}
    </div>
  )
}

// ─── Alert Sources Section ────────────────────────────────────────────────────

const SOURCE_TYPES = ['dynatrace', 'datadog', 'prometheus', 'pagerduty', 'grafana', 'zabbix', 'nagios', 'splunk', 'custom']

function AlertSourcesSection() {
  const [sources, setSources] = useState<any[]>([])
  const [loading, setLoading] = useState(true)
  const [editing, setEditing] = useState<any | null>(null)
  const [creating, setCreating] = useState(false)
  const [form, setForm] = useState({ name: '', source_type: 'dynatrace', display_name: '', endpoint_url: '', api_key: '', webhook_secret: '', polling_interval_seconds: 60, enabled: true })
  const [saving, setSaving] = useState(false)
  const [showKey, setShowKey] = useState(false)

  useEffect(() => { load() }, [])
  const load = async () => {
    setLoading(true)
    try { const r = await adminAlertSourcesApi.list(); setSources(toArr(r.data?.data ?? r.data, 'sources')) }
    catch (e: any) { if (e?.response?.status !== 403) toast.error('Failed to load alert sources') } finally { setLoading(false) }
  }

  const save = async () => {
    if (!form.name || !form.source_type) { toast.error('Name and type required'); return }
    setSaving(true)
    try {
      if (editing?.id) { await adminAlertSourcesApi.update(editing.id, form) }
      else { await adminAlertSourcesApi.create(form) }
      setCreating(false); setEditing(null)
      setForm({ name: '', source_type: 'dynatrace', display_name: '', endpoint_url: '', api_key: '', webhook_secret: '', polling_interval_seconds: 60, enabled: true })
      await load(); toast.success('Alert source saved')
    } catch { toast.error('Failed to save alert source') } finally { setSaving(false) }
  }

  const del = async (id: string) => {
    if (!confirm('Delete this alert source?')) return
    try { await adminAlertSourcesApi.delete(id); setSources(prev => prev.filter(s => s.id !== id)); toast.success('Deleted') }
    catch { toast.error('Failed to delete') }
  }

  const openEdit = (s: any) => { setForm({ ...s }); setEditing(s); setCreating(true) }

  const sourceColor = (t: string) => ({ dynatrace: c.blue, datadog: c.purple, prometheus: c.orange, pagerduty: c.green })[t] || c.gray

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 16, gap: 8 }}>
        <Btn size="sm" onClick={() => { setEditing(null); setForm({ name: '', source_type: 'dynatrace', display_name: '', endpoint_url: '', api_key: '', webhook_secret: '', polling_interval_seconds: 60, enabled: true }); setCreating(true) }}>
          <Plus style={{ width: 14, height: 14 }} /> Add Source
        </Btn>
        <Btn onClick={load} variant="ghost" size="sm"><RefreshCw style={{ width: 14, height: 14 }} /></Btn>
      </div>
      {loading ? <div style={{ textAlign: 'center', padding: 40 }}><Loader2 style={{ width: 24, height: 24, animation: 'spin 1s linear infinite', color: c.blue, margin: '0 auto' }} /></div> : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {(sources || []).map(s => (
            <div key={s.id} style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '12px 14px', borderRadius: c.r.md, background: c.card, border: `1px solid ${c.separator}` }}>
              <div style={{ width: 36, height: 36, borderRadius: c.r.sm, background: `${sourceColor(s.source_type)}22`, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                <Radio style={{ width: 18, height: 18, color: sourceColor(s.source_type) }} />
              </div>
              <div style={{ flex: 1 }}>
                <div style={{ fontSize: 14, fontWeight: 500, color: c.label }}>{s.display_name || s.name}</div>
                <div style={{ fontSize: 12, color: c.tertiary }}>{s.source_type} {s.endpoint_url ? `• ${s.endpoint_url}` : ''}</div>
              </div>
              <Badge text={s.enabled ? 'Active' : 'Disabled'} color={s.enabled ? c.green : c.gray} />
              <button onClick={() => openEdit(s)} style={{ background: 'none', border: 'none', cursor: 'pointer', color: c.tertiary, padding: 4 }}>
                <Pencil style={{ width: 14, height: 14 }} />
              </button>
              <button onClick={() => del(s.id)} style={{ background: 'none', border: 'none', cursor: 'pointer', color: c.red, padding: 4 }}>
                <Trash2 style={{ width: 14, height: 14 }} />
              </button>
            </div>
          ))}
          {sources.length === 0 && <div style={{ textAlign: 'center', padding: 40, color: c.tertiary, fontSize: 14 }}>No alert sources configured</div>}
        </div>
      )}

      <Modal open={creating} onClose={() => { setCreating(false); setEditing(null) }} title={editing ? 'Edit Alert Source' : 'Add Alert Source'} width={520}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
            <Field label="Name" required value={form.name} onChange={v => setForm(f => ({ ...f, name: v }))} placeholder="e.g. prod-dynatrace" />
            <Field label="Display Name" value={form.display_name} onChange={v => setForm(f => ({ ...f, display_name: v }))} placeholder="Production Dynatrace" />
          </div>
          <Field label="Source Type">
            <select value={form.source_type} onChange={e => setForm(f => ({ ...f, source_type: e.target.value }))}
              style={{ padding: '8px 10px', borderRadius: c.r.sm, fontSize: 14, border: `1px solid ${c.separator}`, background: c.fill, color: c.label }}>
              {SOURCE_TYPES.map(t => <option key={t} value={t}>{t}</option>)}
            </select>
          </Field>
          <Field label="Endpoint URL" value={form.endpoint_url} onChange={v => setForm(f => ({ ...f, endpoint_url: v }))} placeholder="https://…" />
          <div style={{ position: 'relative' }}>
            <Field label="API Key" value={form.api_key} type={showKey ? 'text' : 'password'} onChange={v => setForm(f => ({ ...f, api_key: v }))} placeholder="•••••" />
            <button onClick={() => setShowKey(x => !x)} style={{ position: 'absolute', right: 8, bottom: 8, background: 'none', border: 'none', cursor: 'pointer', color: c.tertiary }}>
              {showKey ? <EyeOff style={{ width: 14, height: 14 }} /> : <Eye style={{ width: 14, height: 14 }} />}
            </button>
          </div>
          <Field label="Webhook Secret" value={form.webhook_secret} type="password" onChange={v => setForm(f => ({ ...f, webhook_secret: v }))} placeholder="•••••" />
          <Field label="Polling Interval (seconds)" value={String(form.polling_interval_seconds)} type="number" onChange={v => setForm(f => ({ ...f, polling_interval_seconds: parseInt(v) || 60 }))} />
          <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 14, color: c.label, cursor: 'pointer' }}>
            <input type="checkbox" checked={form.enabled} onChange={e => setForm(f => ({ ...f, enabled: e.target.checked })) } />
            Enabled
          </label>
          <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end', marginTop: 8 }}>
            <Btn variant="ghost" onClick={() => { setCreating(false); setEditing(null) }}>Cancel</Btn>
            <Btn onClick={save} loading={saving}>Save</Btn>
          </div>
        </div>
      </Modal>
    </div>
  )
}

// ─── Integrations Section ─────────────────────────────────────────────────────

const INT_TABS = [
  { id: 'monitoring', label: 'Monitoring', icon: BarChart },
  { id: 'notifications', label: 'Notifications', icon: Bell },
  { id: 'ticketing', label: 'Ticketing', icon: Ticket },
  { id: 'ai', label: 'AI Providers', icon: Zap },
] as const

const INT_CATALOG: Record<string, { type: string; name: string; desc: string; color: string }[]> = {
  monitoring: [
    { type: 'dynatrace', name: 'Dynatrace', desc: 'APM and infrastructure monitoring', color: c.blue },
    { type: 'datadog', name: 'Datadog', desc: 'Cloud monitoring platform', color: c.purple },
    { type: 'prometheus', name: 'Prometheus', desc: 'Open-source metrics collection', color: c.orange },
    { type: 'grafana', name: 'Grafana', desc: 'Visualization and dashboards', color: c.orange },
    { type: 'new-relic', name: 'New Relic', desc: 'Full-stack observability', color: c.green },
    { type: 'zabbix', name: 'Zabbix', desc: 'Enterprise network monitoring', color: c.red },
  ],
  notifications: [
    { type: 'slack', name: 'Slack', desc: 'Team messaging and alerts', color: '#4A154B' },
    { type: 'email', name: 'Email / SMTP', desc: 'Email notifications', color: c.blue },
    { type: 'teams', name: 'MS Teams', desc: 'Microsoft Teams webhooks', color: c.purple },
    { type: 'pagerduty', name: 'PagerDuty', desc: 'On-call alerting', color: c.green },
  ],
  ticketing: [
    { type: 'servicenow', name: 'ServiceNow', desc: 'IT service management', color: c.green },
    { type: 'jira', name: 'Jira', desc: 'Issue and project tracking', color: c.blue },
    { type: 'github', name: 'GitHub Issues', desc: 'GitHub issue creation', color: c.label },
  ],
  ai: [
    { type: 'openai', name: 'OpenAI', desc: 'GPT models for AI analysis', color: c.green },
    { type: 'anthropic', name: 'Anthropic', desc: 'Claude models', color: c.orange },
    { type: 'ollama', name: 'Ollama (Local)', desc: 'Self-hosted LLM inference', color: c.purple },
  ],
}

function IntegrationsSection() {
  const [tab, setTab] = useState<'monitoring' | 'notifications' | 'ticketing' | 'ai'>('monitoring')
  const [integrations, setIntegrations] = useState<any[]>([])
  const [loading, setLoading] = useState(true)
  const [configuring, setConfiguring] = useState<any | null>(null)
  const [configForm, setConfigForm] = useState<Record<string, string>>({})
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState<string | null>(null)

  useEffect(() => { load() }, [])
  const load = async () => {
    setLoading(true)
    try { const r = await integrationsApi.list(); setIntegrations(toArr(r.data?.data ?? r.data, 'integrations')) }
    catch { /* non-fatal */ } finally { setLoading(false) }
  }

  const getStatus = (type: string) => integrations.find(i => i.type === type || i.name?.toLowerCase().includes(type.toLowerCase()))

  const testIntegration = async (id: string) => {
    setTesting(id)
    try { await integrationsApi.test(id); toast.success('Connection successful!') }
    catch { toast.error('Connection test failed') } finally { setTesting(null) }
  }

  const catalog = INT_CATALOG[tab] || []

  return (
    <div>
      <div style={{ display: 'flex', gap: 4, marginBottom: 20, background: c.fill, padding: 4, borderRadius: c.r.md }}>
        {INT_TABS.map(({ id, label, icon: Icon }) => (
          <button key={id} onClick={() => setTab(id as any)} style={{
            flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 6,
            padding: '7px 12px', borderRadius: c.r.sm, border: 'none', cursor: 'pointer',
            background: tab === id ? c.card : 'transparent', fontSize: 13, fontWeight: tab === id ? 600 : 400,
            color: tab === id ? c.label : c.tertiary,
            boxShadow: tab === id ? '0 1px 4px rgba(0,0,0,.08)' : 'none',
          }}>
            <Icon style={{ width: 14, height: 14 }} />
            {label}
          </button>
        ))}
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))', gap: 12 }}>
        {catalog.map(item => {
          const existing = getStatus(item.type)
          const connected = existing?.enabled || existing?.status === 'active'
          return (
            <div key={item.type} style={{ padding: 16, background: c.card, borderRadius: c.r.md, border: `1px solid ${c.separator}` }}>
              <div style={{ display: 'flex', alignItems: 'flex-start', gap: 10, marginBottom: 12 }}>
                <div style={{ width: 40, height: 40, borderRadius: c.r.sm, background: `${item.color}22`, display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 }}>
                  <Monitor style={{ width: 20, height: 20, color: item.color }} />
                </div>
                <div style={{ flex: 1 }}>
                  <div style={{ fontSize: 14, fontWeight: 600, color: c.label }}>{item.name}</div>
                  <div style={{ fontSize: 12, color: c.tertiary }}>{item.desc}</div>
                </div>
                <div style={{ width: 8, height: 8, borderRadius: '50%', background: connected ? c.green : c.fill2, marginTop: 4 }} />
              </div>
              <div style={{ display: 'flex', gap: 8 }}>
                <Btn size="sm" variant={connected ? 'ghost' : 'primary'} style={{ flex: 1, justifyContent: 'center' }}
                  onClick={() => { setConfiguring(item); setConfigForm(existing?.config || {}) }}>
                  {connected ? 'Configure' : 'Connect'}
                </Btn>
                {connected && existing?.id && (
                  <Btn size="sm" variant="ghost" loading={testing === existing.id} onClick={() => testIntegration(existing.id)}>
                    <TestTube style={{ width: 13, height: 13 }} />
                  </Btn>
                )}
              </div>
            </div>
          )
        })}
      </div>

      <Modal open={!!configuring} onClose={() => setConfiguring(null)} title={`Configure ${configuring?.name}`}>
        {configuring && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
            <div style={{ padding: 12, background: c.fill, borderRadius: c.r.sm, fontSize: 13, color: c.secondary }}>
              Configure your {configuring.name} integration credentials below.
            </div>
            {['url', 'api_key', 'token', 'username', 'webhook_url'].map(key => (
              <Field key={key} label={key.replace(/_/g, ' ').replace(/\b\w/g, l => l.toUpperCase())}
                value={configForm[key] || ''} onChange={v => setConfigForm(f => ({ ...f, [key]: v }))}
                type={key.includes('key') || key.includes('token') ? 'password' : 'text'} />
            ))}
            <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end', marginTop: 8 }}>
              <Btn variant="ghost" onClick={() => setConfiguring(null)}>Cancel</Btn>
              <Btn loading={saving} onClick={async () => {
                setSaving(true)
                try {
                  await integrationsApi.create({ name: configuring.name, type: configuring.type, enabled: true, config: configForm })
                  await load(); setConfiguring(null); toast.success('Integration saved')
                } catch { toast.error('Failed to save') } finally { setSaving(false) }
              }}>Save</Btn>
            </div>
          </div>
        )}
      </Modal>
    </div>
  )
}

// ─── AI & LLM Section ─────────────────────────────────────────────────────────

function AILLMSection() {
  const [configs, setConfigs] = useState<any[]>([])
  const [loading, setLoading] = useState(true)
  const [status, setStatus] = useState<any>(null)
  const [editing, setEditing] = useState<any | null>(null)
  const [creating, setCreating] = useState(false)
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState<string | null>(null)
  const [correlationCfg, setCorrelationCfg] = useState<Record<string, any>>({})
  const [corrLoading, setCorrLoading] = useState(false)
  const [editKey, setEditKey] = useState<string | null>(null)
  const [editVal, setEditVal] = useState('')
  const defaultForm = { name: '', provider: 'ollama', model_name: 'phi3:mini', endpoint_url: 'http://ollama.aileron.svc.cluster.local:11434', enabled: true, use_for_rca: true, use_for_correlation: true, use_for_remediation: true, use_for_summarization: true }
  const [form, setForm] = useState(defaultForm)

  useEffect(() => { load(); loadCorr() }, [])
  const load = async () => {
    setLoading(true)
    try {
      const [cfgRes, statusRes] = await Promise.allSettled([adminLLMApi.getConfigs(), adminLLMApi.status()])
      if (cfgRes.status === 'fulfilled') setConfigs(toArr(cfgRes.value.data?.data ?? cfgRes.value.data, 'configs'))
      if (statusRes.status === 'fulfilled') setStatus(statusRes.value.data)
    } catch { /* non-fatal */ } finally { setLoading(false) }
  }
  const loadCorr = async () => {
    setCorrLoading(true)
    try { const r = await adminCorrelationApi.getConfig(); setCorrelationCfg(r.data || {}) }
    catch { /* non-fatal */ } finally { setCorrLoading(false) }
  }

  const save = async () => {
    if (!form.name) { toast.error('Name required'); return }
    setSaving(true)
    try {
      if (editing?.id) { await adminLLMApi.updateConfig(editing.id, form) }
      else { await adminLLMApi.createConfig(form) }
      setCreating(false); setEditing(null); await load(); toast.success('LLM config saved')
    } catch { toast.error('Failed to save') } finally { setSaving(false) }
  }

  const del = async (id: string) => {
    if (!confirm('Delete this LLM config?')) return
    try { await adminLLMApi.deleteConfig(id); setConfigs(p => p.filter(c => c.id !== id)); toast.success('Deleted') }
    catch { toast.error('Failed to delete') }
  }

  const testConfig = async (id: string) => {
    setTesting(id)
    try { await adminLLMApi.testConfig({ id }); toast.success('LLM connection OK') }
    catch { toast.error('LLM connection failed') } finally { setTesting(null) }
  }

  const saveCorrKey = async (key: string) => {
    try {
      await adminCorrelationApi.updateConfig(key, editVal)
      setCorrelationCfg(p => ({ ...p, [key]: editVal })); setEditKey(null); toast.success('Saved')
    } catch { toast.error('Failed to save') }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 24 }}>
      {/* LLM Status Banner */}
      {status && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '10px 14px', borderRadius: c.r.md, background: status.connected ? `${c.green}18` : `${c.red}18`, border: `1px solid ${status.connected ? c.green : c.red}44` }}>
          <div style={{ width: 8, height: 8, borderRadius: '50%', background: status.connected ? c.green : c.red }} />
          <span style={{ fontSize: 13, fontWeight: 500, color: status.connected ? c.green : c.red }}>
            {status.connected ? `LLM Connected — ${status.model || 'phi3:mini'}` : 'LLM Disconnected'}
          </span>
        </div>
      )}

      {/* LLM Configs */}
      <div>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 12 }}>
          <h4 style={{ margin: 0, fontSize: 15, fontWeight: 600, color: c.label }}>LLM Models</h4>
          <div style={{ display: 'flex', gap: 8 }}>
            <Btn onClick={load} variant="ghost" size="sm"><RefreshCw style={{ width: 14, height: 14 }} /></Btn>
            <Btn size="sm" onClick={() => { setEditing(null); setForm(defaultForm); setCreating(true) }}>
              <Plus style={{ width: 14, height: 14 }} /> Add Model
            </Btn>
          </div>
        </div>
        {loading ? <div style={{ textAlign: 'center', padding: 32 }}><Loader2 style={{ width: 22, height: 22, animation: 'spin 1s linear infinite', color: c.blue, margin: '0 auto' }} /></div> : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
            {(configs || []).map(cfg => (
              <div key={cfg.id} style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '12px 14px', borderRadius: c.r.md, background: c.card, border: `1px solid ${c.separator}` }}>
                <div style={{ width: 36, height: 36, borderRadius: c.r.sm, background: `${c.purple}22`, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                  <Brain style={{ width: 18, height: 18, color: c.purple }} />
                </div>
                <div style={{ flex: 1 }}>
                  <div style={{ fontSize: 14, fontWeight: 500, color: c.label }}>{cfg.name}</div>
                  <div style={{ fontSize: 12, color: c.tertiary }}>{cfg.provider} • {cfg.model_name}</div>
                </div>
                <Badge text={cfg.enabled ? 'Enabled' : 'Disabled'} color={cfg.enabled ? c.green : c.gray} />
                <Btn size="sm" variant="ghost" loading={testing === cfg.id} onClick={() => testConfig(cfg.id)}>
                  <TestTube style={{ width: 13, height: 13 }} />
                </Btn>
                <button onClick={() => { setEditing(cfg); setForm({ ...defaultForm, ...cfg }); setCreating(true) }} style={{ background: 'none', border: 'none', cursor: 'pointer', color: c.tertiary, padding: 4 }}>
                  <Pencil style={{ width: 14, height: 14 }} />
                </button>
                <button onClick={() => del(cfg.id)} style={{ background: 'none', border: 'none', cursor: 'pointer', color: c.red, padding: 4 }}>
                  <Trash2 style={{ width: 14, height: 14 }} />
                </button>
              </div>
            ))}
            {configs.length === 0 && <div style={{ textAlign: 'center', padding: 32, color: c.tertiary, fontSize: 14 }}>No LLM models configured</div>}
          </div>
        )}
      </div>

      {/* Correlation Engine Config */}
      <div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12 }}>
          <GitBranch style={{ width: 16, height: 16, color: c.blue }} />
          <h4 style={{ margin: 0, fontSize: 15, fontWeight: 600, color: c.label }}>Correlation Engine</h4>
          {corrLoading && <Loader2 style={{ width: 14, height: 14, animation: 'spin 1s linear infinite', color: c.blue }} />}
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          {Object.entries(correlationCfg).map(([key, val]) => (
            <div key={key} style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '10px 14px', borderRadius: c.r.sm, background: c.card, border: `1px solid ${c.separator}` }}>
              <span style={{ fontSize: 13, fontFamily: 'monospace', color: c.secondary, width: 200, flexShrink: 0 }}>{key}</span>
              {editKey === key ? (
                <>
                  <input value={editVal} onChange={e => setEditVal(e.target.value)}
                    style={{ flex: 1, padding: '4px 8px', borderRadius: c.r.sm, fontSize: 13, border: `1px solid ${c.separator}`, background: c.fill, color: c.label }} />
                  <button onClick={() => saveCorrKey(key)} style={{ background: 'none', border: 'none', cursor: 'pointer', color: c.green }}>
                    <Check style={{ width: 14, height: 14 }} />
                  </button>
                  <button onClick={() => setEditKey(null)} style={{ background: 'none', border: 'none', cursor: 'pointer', color: c.red }}>
                    <X style={{ width: 14, height: 14 }} />
                  </button>
                </>
              ) : (
                <>
                  <span style={{ flex: 1, fontSize: 13, color: c.label, fontFamily: 'monospace' }}>{String(val)}</span>
                  <button onClick={() => { setEditKey(key); setEditVal(String(val)) }} style={{ background: 'none', border: 'none', cursor: 'pointer', color: c.tertiary, padding: 4 }}>
                    <Pencil style={{ width: 13, height: 13 }} />
                  </button>
                </>
              )}
            </div>
          ))}
          {Object.keys(correlationCfg).length === 0 && !corrLoading && (
            <div style={{ textAlign: 'center', padding: 24, color: c.tertiary, fontSize: 13 }}>No correlation settings available</div>
          )}
        </div>
      </div>

      <Modal open={creating} onClose={() => { setCreating(false); setEditing(null) }} title={editing ? 'Edit LLM Model' : 'Add LLM Model'} width={520}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
            <Field label="Name" required value={form.name} onChange={v => setForm(f => ({ ...f, name: v }))} placeholder="prod-ollama" />
            <Field label="Provider">
              <select value={form.provider} onChange={e => setForm(f => ({ ...f, provider: e.target.value }))}
                style={{ padding: '8px 10px', borderRadius: c.r.sm, fontSize: 14, border: `1px solid ${c.separator}`, background: c.fill, color: c.label }}>
                {['ollama', 'openai', 'anthropic', 'oidc'].map(p => <option key={p} value={p}>{p}</option>)}
              </select>
            </Field>
          </div>
          <Field label="Model Name" value={form.model_name} onChange={v => setForm(f => ({ ...f, model_name: v }))} placeholder="phi3:mini" />
          <Field label="Endpoint URL" value={form.endpoint_url} onChange={v => setForm(f => ({ ...f, endpoint_url: v }))} placeholder="http://…" />
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
            {(['use_for_rca', 'use_for_correlation', 'use_for_remediation', 'use_for_summarization'] as const).map(key => (
              <label key={key} style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, color: c.label, cursor: 'pointer' }}>
                <input type="checkbox" checked={(form as any)[key]} onChange={e => setForm(f => ({ ...f, [key]: e.target.checked }))} />
                {key.replace('use_for_', '').replace(/_/g, ' ').replace(/\b\w/g, l => l.toUpperCase())}
              </label>
            ))}
          </div>
          <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 14, color: c.label, cursor: 'pointer' }}>
            <input type="checkbox" checked={form.enabled} onChange={e => setForm(f => ({ ...f, enabled: e.target.checked }))} />
            Enabled
          </label>
          <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end', marginTop: 8 }}>
            <Btn variant="ghost" onClick={() => { setCreating(false); setEditing(null) }}>Cancel</Btn>
            <Btn onClick={save} loading={saving}>Save</Btn>
          </div>
        </div>
      </Modal>
    </div>
  )
}

// ─── Infrastructure Section ────────────────────────────────────────────────────

function InfrastructureSection() {
  const [tab, setTab] = useState<'k8s' | 'cloudstack'>('k8s')
  const [clusters, setClusters] = useState<any[]>([])
  const [cloudStacks, setCloudStacks] = useState<any[]>([])
  const [loading, setLoading] = useState(true)
  const [csError, setCsError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [showK8sForm, setShowK8sForm] = useState(false)
  const [showCSForm, setShowCSForm] = useState(false)
  const [k8sForm, setK8sForm] = useState({ name: '', environment: 'production', region: '', api_server_url: '', service_account_token: '', ca_cert_data: '' })
  const [csForm, setCsForm] = useState({ name: '', display_name: '', api_url: '', api_key: '', secret_key: '', zone_id: '', environment: '', enabled: true })
  const [showToken, setShowToken] = useState(false)
  const [showSecret, setShowSecret] = useState(false)

  useEffect(() => { loadAll() }, [])
  const loadAll = async () => {
    setLoading(true)
    setCsError(null)
    try {
      const [k8sRes, csRes] = await Promise.allSettled([topologyApi.listK8sClusters(), adminInfraApi.getCloudStack()])
      if (k8sRes.status === 'fulfilled') setClusters(toArr(k8sRes.value.data?.data ?? k8sRes.value.data, 'clusters'))
      if (csRes.status === 'fulfilled') {
        setCloudStacks(toArr(csRes.value.data?.data ?? csRes.value.data, 'configs', 'cloudstack', 'items'))
      } else {
        const errMsg = (csRes.reason as any)?.response?.data?.error || (csRes.reason as any)?.message || 'Failed to load CloudStack configs'
        console.error('[CloudStack] load failed:', csRes.reason)
        setCsError(errMsg)
      }
    } catch { /* non-fatal */ } finally { setLoading(false) }
  }

  const addK8s = async () => {
    if (!k8sForm.name || !k8sForm.api_server_url || !k8sForm.service_account_token) {
      toast.error('Name, API Server URL, and service account token are required'); return
    }
    setSaving(true)
    try {
      await topologyApi.addK8sCluster(k8sForm)
      setShowK8sForm(false)
      setK8sForm({ name: '', environment: 'production', region: '', api_server_url: '', service_account_token: '', ca_cert_data: '' })
      await loadAll(); toast.success('Cluster added')
    } catch { toast.error('Failed to add cluster') } finally { setSaving(false) }
  }

  const removeK8s = async (name: string) => {
    if (!confirm(`Remove cluster "${name}"?`)) return
    try { await topologyApi.removeK8sCluster(name); setClusters(prev => prev.filter(c => c.name !== name)); toast.success('Cluster removed') }
    catch { toast.error('Failed to remove cluster') }
  }

  const discoverK8s = async (name: string) => {
    try { await topologyApi.discoverK8sCluster(name); toast.success('Discovery triggered') }
    catch { toast.error('Discovery failed') }
  }

  const addCS = async () => {
    if (!csForm.name || !csForm.api_url) { toast.error('Name and API URL required'); return }
    if (!csForm.api_key || !csForm.secret_key) { toast.error('API Key and Secret Key are required'); return }
    setSaving(true)
    try {
      await adminInfraApi.createCloudStack(csForm)
      setShowCSForm(false)
      setCsForm({ name: '', display_name: '', api_url: '', api_key: '', secret_key: '', zone_id: '', environment: '', enabled: true })
      await loadAll(); toast.success('CloudStack config saved')
    } catch (e: any) { toast.error(e?.response?.data?.message || 'Failed to save CloudStack config') } finally { setSaving(false) }
  }

  const removeCS = async (id: string) => {
    if (!confirm('Remove this CloudStack config?')) return
    try { await adminInfraApi.deleteCloudStack(id); setCloudStacks(prev => prev.filter(c => c.id !== id)); toast.success('Removed') }
    catch { toast.error('Failed to remove') }
  }

  const envColor = (env: string) => {
    if (/prod/i.test(env)) return c.red
    if (/stag/i.test(env)) return c.orange
    return c.blue
  }

  return (
    <div>
      {/* Tab switcher */}
      <div style={{ display: 'flex', gap: 4, marginBottom: 20, background: c.fill, padding: 4, borderRadius: c.r.md, width: 'fit-content' }}>
        {([['k8s', 'Kubernetes Clusters'], ['cloudstack', 'CloudStack']] as const).map(([id, label]) => (
          <button key={id} onClick={() => setTab(id)} style={{
            padding: '7px 16px', borderRadius: c.r.sm, border: 'none', cursor: 'pointer',
            background: tab === id ? c.card : 'transparent', fontSize: 13,
            fontWeight: tab === id ? 600 : 400, color: tab === id ? c.label : c.tertiary,
            boxShadow: tab === id ? '0 1px 4px rgba(0,0,0,.08)' : 'none',
          }}>{label}</button>
        ))}
      </div>

      {tab === 'k8s' && (
        <div>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
            <span style={{ fontSize: 13, color: c.secondary }}>{clusters.length} cluster{clusters.length !== 1 ? 's' : ''} configured</span>
            <Btn variant="primary" size="sm" onClick={() => setShowK8sForm(v => !v)}>
              <Plus style={{ width: 13, height: 13 }} /> Add Cluster
            </Btn>
          </div>

          {showK8sForm && (
            <div style={{ padding: 16, background: c.card, borderRadius: c.r.md, border: `1px solid ${c.separator}`, marginBottom: 16 }}>
              <div style={{ fontSize: 13, fontWeight: 600, color: c.label, marginBottom: 12 }}>New Kubernetes Cluster</div>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginBottom: 10 }}>
                <Field label="Cluster Name" value={k8sForm.name} onChange={v => setK8sForm(f => ({ ...f, name: v }))} placeholder="example-cluster" required />
                <Field label="Environment" value={k8sForm.environment} onChange={v => setK8sForm(f => ({ ...f, environment: v }))} placeholder="production" />
                <Field label="Region" value={k8sForm.region} onChange={v => setK8sForm(f => ({ ...f, region: v }))} placeholder="us-east-1" />
                <Field label="API Server URL" value={k8sForm.api_server_url} onChange={v => setK8sForm(f => ({ ...f, api_server_url: v }))} placeholder="https://k8s.example.com:6443" required />
              </div>
              <div style={{ marginBottom: 10 }}>
                <label style={{ fontSize: 12, fontWeight: 600, color: c.secondary, display: 'block', marginBottom: 4 }}>Service Account Token *</label>
                <div style={{ position: 'relative' }}>
                  <input type={showToken ? 'text' : 'password'} value={k8sForm.service_account_token}
                    onChange={e => setK8sForm(f => ({ ...f, service_account_token: e.target.value }))}
                    placeholder="eyJhbGciOiJ..." style={{ width: '100%', padding: '8px 36px 8px 10px', borderRadius: c.r.sm, fontSize: 14, color: c.label, background: c.fill, border: `1px solid ${c.separator}`, outline: 'none', boxSizing: 'border-box' }} />
                  <button onClick={() => setShowToken(v => !v)} style={{ position: 'absolute', right: 8, top: '50%', transform: 'translateY(-50%)', background: 'none', border: 'none', cursor: 'pointer', color: c.tertiary }}>
                    {showToken ? <EyeOff style={{ width: 14, height: 14 }} /> : <Eye style={{ width: 14, height: 14 }} />}
                  </button>
                </div>
              </div>
              <Field label="CA Certificate (base64, optional)" value={k8sForm.ca_cert_data} onChange={v => setK8sForm(f => ({ ...f, ca_cert_data: v }))} placeholder="LS0tLS1CRUdJTi..." />
              <div style={{ display: 'flex', gap: 8, marginTop: 12 }}>
                <Btn variant="primary" size="sm" onClick={addK8s} loading={saving}>Add Cluster</Btn>
                <Btn variant="ghost" size="sm" onClick={() => setShowK8sForm(false)}>Cancel</Btn>
              </div>
            </div>
          )}

          {loading ? (
            <div style={{ display: 'flex', justifyContent: 'center', padding: 40 }}><Loader2 style={{ width: 20, height: 20, animation: 'spin 1s linear infinite', color: c.tertiary }} /></div>
          ) : clusters.length === 0 ? (
            <div style={{ textAlign: 'center', padding: '40px 20px', color: c.tertiary, fontSize: 14 }}>No Kubernetes clusters configured. Add one to enable topology discovery.</div>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {clusters.map((cl: any, i) => (
                <div key={cl.name || i} style={{ padding: '12px 14px', background: c.card, borderRadius: c.r.md, border: `1px solid ${c.separator}`, display: 'flex', alignItems: 'center', gap: 12 }}>
                  <div style={{ flex: 1 }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                      <span style={{ fontSize: 14, fontWeight: 600, color: c.label }}>{cl.name}</span>
                      {cl.environment && <Badge text={cl.environment} color={envColor(cl.environment)} />}
                      {cl.region && <span style={{ fontSize: 12, color: c.tertiary }}>{cl.region}</span>}
                    </div>
                    {cl.api_server_url && <div style={{ fontSize: 12, color: c.tertiary, marginTop: 2 }}>{cl.api_server_url}</div>}
                  </div>
                  <div style={{ display: 'flex', gap: 6 }}>
                    <Btn variant="ghost" size="sm" onClick={() => discoverK8s(cl.name)}>
                      <RefreshCw style={{ width: 12, height: 12 }} /> Discover
                    </Btn>
                    <Btn variant="danger" size="sm" onClick={() => removeK8s(cl.name)}>
                      <Trash2 style={{ width: 12, height: 12 }} />
                    </Btn>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {tab === 'cloudstack' && (
        <div>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
            <span style={{ fontSize: 13, color: c.secondary }}>{cloudStacks.length} config{cloudStacks.length !== 1 ? 's' : ''}</span>
            <Btn variant="primary" size="sm" onClick={() => setShowCSForm(v => !v)}>
              <Plus style={{ width: 13, height: 13 }} /> Add Config
            </Btn>
          </div>

          {showCSForm && (
            <div style={{ padding: 16, background: c.card, borderRadius: c.r.md, border: `1px solid ${c.separator}`, marginBottom: 16 }}>
              <div style={{ fontSize: 13, fontWeight: 600, color: c.label, marginBottom: 12 }}>New CloudStack Config</div>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginBottom: 10 }}>
                <Field label="Name *" value={csForm.name} onChange={v => setCsForm(f => ({ ...f, name: v }))} placeholder="cloudstack-prod" required />
                <Field label="Display Name" value={csForm.display_name} onChange={v => setCsForm(f => ({ ...f, display_name: v }))} placeholder="Production CloudStack" />
                <Field label="API URL *" value={csForm.api_url} onChange={v => setCsForm(f => ({ ...f, api_url: v }))} placeholder="https://cloudstack.example.com/client/api" required />
                <Field label="Zone ID" value={csForm.zone_id} onChange={v => setCsForm(f => ({ ...f, zone_id: v }))} placeholder="zone1-id" />
                <Field label="API Key *" value={csForm.api_key} onChange={v => setCsForm(f => ({ ...f, api_key: v }))} placeholder="API key" />
                <Field label="Environment" value={csForm.environment} onChange={v => setCsForm(f => ({ ...f, environment: v }))} placeholder="production" />
              </div>
              <div style={{ marginBottom: 10 }}>
                <label style={{ fontSize: 12, fontWeight: 600, color: c.secondary, display: 'block', marginBottom: 4 }}>Secret Key *</label>
                <div style={{ position: 'relative' }}>
                  <input type={showSecret ? 'text' : 'password'} value={csForm.secret_key}
                    onChange={e => setCsForm(f => ({ ...f, secret_key: e.target.value }))}
                    placeholder="Secret key" style={{ width: '100%', padding: '8px 36px 8px 10px', borderRadius: c.r.sm, fontSize: 14, color: c.label, background: c.fill, border: `1px solid ${c.separator}`, outline: 'none', boxSizing: 'border-box' }} />
                  <button onClick={() => setShowSecret(v => !v)} style={{ position: 'absolute', right: 8, top: '50%', transform: 'translateY(-50%)', background: 'none', border: 'none', cursor: 'pointer', color: c.tertiary }}>
                    {showSecret ? <EyeOff style={{ width: 14, height: 14 }} /> : <Eye style={{ width: 14, height: 14 }} />}
                  </button>
                </div>
              </div>
              <div style={{ display: 'flex', gap: 8, marginTop: 12 }}>
                <Btn variant="primary" size="sm" onClick={addCS} loading={saving}>Save Config</Btn>
                <Btn variant="ghost" size="sm" onClick={() => setShowCSForm(false)}>Cancel</Btn>
              </div>
            </div>
          )}

          {loading ? (
            <div style={{ display: 'flex', justifyContent: 'center', padding: 40 }}><Loader2 style={{ width: 20, height: 20, animation: 'spin 1s linear infinite', color: c.tertiary }} /></div>
          ) : csError ? (
            <div style={{ padding: '16px', background: `${c.red}15`, borderRadius: c.r.md, border: `1px solid ${c.red}40` }}>
              <div style={{ fontSize: 13, fontWeight: 600, color: c.red, marginBottom: 4 }}>Failed to load CloudStack configurations</div>
              <div style={{ fontSize: 12, color: c.secondary, fontFamily: 'monospace' }}>{csError}</div>
              <Btn variant="ghost" size="sm" onClick={loadAll} style={{ marginTop: 8 }}>Retry</Btn>
            </div>
          ) : cloudStacks.length === 0 ? (
            <div style={{ textAlign: 'center', padding: '40px 20px', color: c.tertiary, fontSize: 14 }}>No CloudStack configs. Add one to enable VM/host topology discovery.</div>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {cloudStacks.map((cs: any, i) => (
                <div key={cs.id || i} style={{ padding: '12px 14px', background: c.card, borderRadius: c.r.md, border: `1px solid ${c.separator}`, display: 'flex', alignItems: 'center', gap: 12 }}>
                  <div style={{ flex: 1 }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                      <span style={{ fontSize: 14, fontWeight: 600, color: c.label }}>{cs.name}</span>
                      {cs.zone_id && <Badge text={cs.zone_id} color={c.blue} />}
                      {cs.environment && <Badge text={cs.environment} color={envColor(cs.environment)} />}
                      <Badge text={cs.enabled ? 'enabled' : 'disabled'} color={cs.enabled ? c.green : c.gray} />
                    </div>
                    {cs.api_url && <div style={{ fontSize: 12, color: c.tertiary, marginTop: 2 }}>{cs.api_url}</div>}
                    {(cs.vm_count > 0 || cs.sync_status) && (
                      <div style={{ fontSize: 11, color: c.tertiary, marginTop: 2 }}>
                        {cs.vm_count > 0 && `${cs.vm_count} VMs`}
                        {cs.sync_status && ` • sync: ${cs.sync_status}`}
                        {cs.last_sync && ` • last synced ${new Date(cs.last_sync).toLocaleDateString()}`}
                      </div>
                    )}
                  </div>
                  <Btn variant="danger" size="sm" onClick={() => removeCS(cs.id)}>
                    <Trash2 style={{ width: 12, height: 12 }} />
                  </Btn>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  )
}

// ─── Settings Section ─────────────────────────────────────────────────────────

const SETTING_GROUPS = [
  {
    id: 'general', label: 'General', icon: Settings, color: '#8E8E93',
    fields: [
      { key: 'app_name',          label: 'Application Name',      type: 'text',   placeholder: 'AlertHub', hint: 'Displayed in the browser tab and emails' },
      { key: 'base_url',          label: 'Base URL',              type: 'text',   placeholder: 'https://alerthub.internal' },
      { key: 'default_timezone',  label: 'Default Timezone',      type: 'text',   placeholder: 'America/Los_Angeles' },
      { key: 'max_sessions_per_user', label: 'Max Sessions / User', type: 'number', placeholder: '5' },
    ],
  },
  {
    id: 'security', label: 'Security', icon: Shield, color: '#FF3B30',
    fields: [
      { key: 'session_timeout_minutes', label: 'Session Timeout (min)', type: 'number', placeholder: '480' },
      { key: 'jwt_expiry_hours',       label: 'JWT Expiry (hours)',    type: 'number', placeholder: '8' },
      { key: 'api_rate_limit_rpm',     label: 'API Rate Limit (req/min)', type: 'number', placeholder: '600' },
      { key: 'min_password_length',    label: 'Min Password Length',  type: 'number', placeholder: '12' },
      { key: 'mfa_required',           label: 'Require MFA',          type: 'bool' },
      { key: 'password_complexity',    label: 'Password Complexity',  type: 'bool', hint: 'Requires upper, lower, number, special' },
      { key: 'ldap_auto_provision',    label: 'LDAP Auto-Provision',  type: 'bool', hint: 'Create user accounts on first LDAP login' },
    ],
  },
  {
    id: 'alerts', label: 'Alerts & Incidents', icon: AlertCircle, color: '#FF9500',
    fields: [
      { key: 'default_alert_severity',    label: 'Default Alert Severity', type: 'select', options: ['low', 'medium', 'high', 'critical'], placeholder: 'medium' },
      { key: 'alert_retention_days',      label: 'Alert Retention (days)',   type: 'number', placeholder: '90' },
      { key: 'auto_close_resolved_hours', label: 'Auto-close After (hours)', type: 'number', placeholder: '24' },
      { key: 'max_alerts_per_page',       label: 'Alerts Per Page',          type: 'number', placeholder: '50' },
      { key: 'alert_deduplication',       label: 'Alert Deduplication',      type: 'bool' },
      { key: 'auto_create_incident_on_critical', label: 'Auto-create Incident on Critical', type: 'bool' },
      { key: 'incident_auto_assign',      label: 'Auto-assign Incidents',    type: 'bool' },
    ],
  },
  {
    id: 'notifications', label: 'Notifications', icon: Bell, color: '#007AFF',
    fields: [
      { key: 'enabled',          category: 'smtp',         label: 'Email Notifications',      type: 'bool' },
      { key: 'host',             category: 'smtp',         label: 'SMTP Host',                type: 'text',     placeholder: 'smtp.example.com' },
      { key: 'port',             category: 'smtp',         label: 'SMTP Port',                type: 'number',   placeholder: '587' },
      { key: 'from',             category: 'smtp',         label: 'From Address',             type: 'text',     placeholder: 'alerts@example.com' },
      { key: 'username',         category: 'smtp',         label: 'SMTP Username',            type: 'text',     placeholder: 'relay-user' },
      { key: 'password',         category: 'smtp',         label: 'SMTP Password',            type: 'password', placeholder: '••••••••', isSecret: true },
      { key: 'tls',              category: 'smtp',         label: 'Enable TLS/STARTTLS',      type: 'bool' },
      { key: 'enabled',          category: 'slack',        label: 'Slack Notifications',      type: 'bool' },
      { key: 'webhook_url',      category: 'slack',        label: 'Slack Webhook URL',        type: 'text',     placeholder: 'https://hooks.slack.com/services/…' },
      { key: 'default_channel',  category: 'slack',        label: 'Default Channel',          type: 'text',     placeholder: '#sre-alerts' },
      { key: 'delay_minutes',    category: 'notification', label: 'Notification Delay (min)', type: 'number',   placeholder: '0' },
    ],
  },
  {
    id: 'ai', label: 'AI & Automation', icon: Brain, color: '#AF52DE',
    fields: [
      { key: 'ai_context_enabled',     label: 'AI Live Context',         type: 'bool', hint: 'Inject live alerts/incidents into AI prompts' },
      { key: 'ai_max_tokens',          label: 'AI Max Tokens',           type: 'number', placeholder: '4096' },
      { key: 'workflow_auto_execute',  label: 'Workflow Auto-Execute',   type: 'bool', hint: 'Allow workflows to trigger without approval' },
      { key: 'correlation_window_sec', label: 'Correlation Window (sec)', type: 'number', placeholder: '300' },
      { key: 'ai_rca_enabled',         label: 'AI Root Cause Analysis',  type: 'bool' },
    ],
  },
  {
    id: 'retention', label: 'Data Retention', icon: Activity, color: '#34C759',
    fields: [
      { key: 'audit_log_retention_days',    label: 'Audit Log Retention (days)',    type: 'number', placeholder: '365' },
      { key: 'alert_data_retention_days',   label: 'Alert Data Retention (days)',   type: 'number', placeholder: '90' },
      { key: 'chat_session_retention_days', label: 'Chat Session Retention (days)', type: 'number', placeholder: '30' },
      { key: 'metric_data_retention_days',  label: 'Metric Data Retention (days)',  type: 'number', placeholder: '180' },
    ],
  },
  {
    id: 'maintenance', label: 'Maintenance', icon: Zap, color: '#FF3B30',
    fields: [
      { key: 'maintenance_mode',    label: 'Maintenance Mode',        type: 'bool', hint: 'Show maintenance page to non-admin users' },
      { key: 'debug_mode',          label: 'Debug Mode',              type: 'bool', hint: 'Enable verbose logging' },
      { key: 'registration_enabled', label: 'Open Registration',     type: 'bool', hint: 'Allow new users to self-register' },
      { key: 'read_only_mode',      label: 'Read-Only Mode',          type: 'bool', hint: 'Disable all write operations' },
    ],
  },
] as const

function SettingsSection() {
  const [cfg, setCfg] = useState<Record<string, any>>({})
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [activeGroup, setActiveGroup] = useState('general')

  useEffect(() => {
    setLoading(true)
    configApi.getSystemConfig()
      .then(r => { setCfg(r.data?.data ?? r.data ?? {}); setLoading(false) })
      .catch(() => setLoading(false))
  }, [])

  const set = (key: string, value: any, category?: string) => {
    const cat = category ?? activeGroup
    setCfg(p => ({ ...p, [cat]: { ...((p[cat] as any) || {}), [key]: value } }))
  }

  const save = async () => {
    setSaving(true)
    try {
      await configApi.updateSystemConfig(cfg); toast.success('Settings saved')
    } catch { toast.error('Failed to save settings') } finally { setSaving(false) }
  }

  const activeGrp = SETTING_GROUPS.find(g => g.id === activeGroup) ?? SETTING_GROUPS[0]

  return (
    <div style={{ display: 'flex', gap: 20 }}>
      {/* Group nav */}
      <div style={{ width: 180, flexShrink: 0, display: 'flex', flexDirection: 'column', gap: 2 }}>
        {SETTING_GROUPS.map(grp => {
          const Icon = grp.icon as any
          const on = grp.id === activeGroup
          return (
            <button key={grp.id} onClick={() => setActiveGroup(grp.id)} style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '7px 10px', borderRadius: c.r.sm, border: 'none', background: on ? `${grp.color}18` : 'transparent', cursor: 'pointer', textAlign: 'left', transition: 'background .12s' }}>
              <div style={{ width: 22, height: 22, borderRadius: 5, background: on ? grp.color : c.fill2, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                <Icon style={{ width: 12, height: 12, color: on ? '#fff' : c.tertiary }} />
              </div>
              <span style={{ fontSize: 12, fontWeight: on ? 600 : 400, color: on ? grp.color : c.label }}>{grp.label}</span>
            </button>
          )
        })}
      </div>

      {/* Settings panel */}
      <div style={{ flex: 1 }}>
        {loading ? (
          <div style={{ textAlign: 'center', padding: 40 }}><Loader2 style={{ width: 24, height: 24, animation: 'spin 1s linear infinite', color: c.blue, margin: '0 auto' }} /></div>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
            <div style={{ padding: 20, background: c.card, borderRadius: c.r.md, border: `1px solid ${c.separator}` }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 18 }}>
                <div style={{ width: 28, height: 28, borderRadius: c.r.sm, background: (activeGrp as any).color, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                  <activeGrp.icon style={{ width: 14, height: 14, color: '#fff' }} />
                </div>
                <h4 style={{ margin: 0, fontSize: 15, fontWeight: 600, color: c.label }}>{activeGrp.label}</h4>
              </div>

              <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
                {(activeGrp.fields as unknown as any[]).map((f: any) => {
                  const cat = f.category ?? activeGrp.id
                  const val = ((cfg[cat] as any) || {})[f.key] ?? ''
                  if (f.type === 'bool') {
                    return (
                      <label key={`${cat}.${f.key}`} style={{ display: 'flex', alignItems: 'flex-start', gap: 12, padding: '10px 12px', borderRadius: c.r.sm, background: c.fill, cursor: 'pointer' }}>
                        <input type="checkbox" checked={!!val} onChange={e => set(f.key, e.target.checked, cat)}
                          style={{ marginTop: 2, accentColor: (activeGrp as any).color, width: 15, height: 15, flexShrink: 0 }} />
                        <div>
                          <div style={{ fontSize: 13, fontWeight: 500, color: c.label }}>{f.label}</div>
                          {f.hint && <div style={{ fontSize: 11, color: c.tertiary, marginTop: 1 }}>{f.hint}</div>}
                          {f.category && <div style={{ fontSize: 10, color: c.tertiary, marginTop: 1, fontFamily: 'monospace' }}>{f.category}.{f.key}</div>}
                        </div>
                      </label>
                    )
                  }
                  if (f.type === 'select') {
                    return (
                      <div key={`${cat}.${f.key}`} style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                        <label style={{ fontSize: 12, fontWeight: 600, color: c.secondary }}>{f.label}</label>
                        <select value={String(val)} onChange={e => set(f.key, e.target.value, cat)}
                          style={{ padding: '8px 10px', borderRadius: c.r.sm, fontSize: 14, border: `1px solid ${c.separator}`, background: c.fill, color: val ? c.label : c.tertiary, outline: 'none' }}>
                          <option value="">Default ({f.placeholder})</option>
                          {(f.options || []).map((o: string) => <option key={o} value={o}>{o}</option>)}
                        </select>
                        {f.hint && <span style={{ fontSize: 11, color: c.tertiary }}>{f.hint}</span>}
                      </div>
                    )
                  }
                  return (
                    <div key={`${cat}.${f.key}`} style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                      <label style={{ fontSize: 12, fontWeight: 600, color: c.secondary }}>{f.label}</label>
                      <input
                        type={f.type === 'password' ? 'password' : f.type}
                        value={String(val)}
                        onChange={e => set(f.key, f.type === 'number' ? (parseInt(e.target.value) || 0) : e.target.value, cat)}
                        placeholder={f.placeholder}
                        style={{ padding: '8px 10px', borderRadius: c.r.sm, fontSize: 14, color: c.label, background: c.fill, border: `1px solid ${c.separator}`, outline: 'none' }}
                      />
                      {f.hint && <span style={{ fontSize: 11, color: c.tertiary }}>{f.hint}</span>}
                    </div>
                  )
                })}
              </div>
            </div>

            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <span style={{ fontSize: 12, color: c.tertiary }}>Changes are applied immediately after saving</span>
              <Btn onClick={save} loading={saving}><Save style={{ width: 14, height: 14 }} /> Save Settings</Btn>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

// ─── Main AdminPage ───────────────────────────────────────────────────────────

export default function AdminPage() {
  const [section, setSection] = useState<SectionId>('users')
  const [roles, setRoles] = useState<Role[]>([])
  const [rolesLoaded, setRolesLoaded] = useState(false)

  useEffect(() => {
    rolesApi.list()
      .then(r => {
        const raw = r.data?.data ?? r.data
        const arr: Role[] = Array.isArray(raw) ? raw : Array.isArray(raw?.roles) ? raw.roles : []
        setRoles(arr)
        setRolesLoaded(true)
      })
      .catch(() => setRolesLoaded(true))
  }, [])

  const navInfo = NAV.find(n => n.id === section)!

  return (
    <div style={{ minHeight: '100vh', background: c.bg, fontFamily: '-aileron-system, BlinkMacSystemFont, "SF Pro Text", sans-serif' }}>
      <div style={{ maxWidth: 1280, margin: '0 auto', padding: '24px 20px' }}>
        {/* Header */}
        <div style={{ marginBottom: 24 }}>
          <h1 style={{ fontSize: 28, fontWeight: 700, color: c.label, margin: 0 }}>Admin</h1>
          <p style={{ fontSize: 14, color: c.tertiary, margin: '4px 0 0' }}>System configuration and user management</p>
        </div>

        <div style={{ display: 'flex', gap: 24, alignItems: 'flex-start' }}>
          {/* Sidebar */}
          <div style={{ flexShrink: 0 }}>
            <Sidebar active={section} onSelect={setSection} counts={{}} />
          </div>

          {/* Content */}
          <div style={{ flex: 1, minWidth: 0 }}>
            {/* Section header */}
            <div style={{ marginBottom: 20 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                <div style={{ width: 32, height: 32, borderRadius: c.r.sm, background: navInfo.color, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                  <navInfo.icon style={{ width: 16, height: 16, color: '#fff' }} />
                </div>
                <h2 style={{ fontSize: 20, fontWeight: 600, color: c.label, margin: 0 }}>{navInfo.label}</h2>
              </div>
            </div>

            <AnimatePresence mode="wait">
              <motion.div key={section} initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0, y: -8 }} transition={{ duration: 0.15 }}>
                {section === 'users' && rolesLoaded && <UsersSection roles={roles} />}
                {section === 'roles' && rolesLoaded && <RolesSection roles={roles} onRolesChange={setRoles} />}
                {section === 'ldap' && rolesLoaded && <LDAPSection roles={roles} />}
                {section === 'audit' && <AuditSection />}
                {section === 'api-keys' && <APIKeyManagementPage />}
                {section === 'alert-sources' && <AlertSourcesSection />}
                {section === 'integrations' && <IntegrationsSection />}
                {section === 'infrastructure' && <InfrastructureSection />}
                {section === 'ai-llm' && <AILLMSection />}
                {section === 'settings' && <SettingsSection />}
              </motion.div>
            </AnimatePresence>
          </div>
        </div>
      </div>

      <style>{`@keyframes spin { from { transform: rotate(0deg) } to { transform: rotate(360deg) } }`}</style>
    </div>
  )
}
