import React, { useState, useCallback } from 'react'
import { BookOpen, Plus, Tag, Save, X } from 'lucide-react'
import toast from 'react-hot-toast'

const RCA_URL = '/api/v1/rca'
const rcaAuthHeader = () => ({ 'Content-Type': 'application/json', 'Authorization': `Bearer ${sessionStorage.getItem('access_token') || localStorage.getItem('access_token') || ''}` })

interface KnowledgeEntry {
  title: string; content: string; category: string; tags: string[]
}

const CATEGORIES = ['runbook','known_issue','architecture','dependency','config','procedure']

export function KnowledgeEditor({ onSaved }: { onSaved?: () => void }) {
  const [entry, setEntry] = useState<KnowledgeEntry>({ title: '', content: '', category: 'known_issue', tags: [] })
  const [tagInput, setTagInput] = useState('')
  const [saving, setSaving] = useState(false)

  const addTag = useCallback(() => {
    const t = tagInput.trim()
    if (t && !entry.tags.includes(t)) {
      setEntry(e => ({ ...e, tags: [...e.tags, t] }))
      setTagInput('')
    }
  }, [tagInput, entry.tags])

  const save = useCallback(async () => {
    if (!entry.title || !entry.content) return toast.error('Title and content required')
    setSaving(true)
    try {
      await fetch(`${RCA_URL}/knowledge`, {
        method: 'POST',
        headers: rcaAuthHeader(),
        body: JSON.stringify(entry),
      })
      toast.success('Knowledge entry saved — the agent will use this in future investigations')
      setEntry({ title: '', content: '', category: 'known_issue', tags: [] })
      onSaved?.()
    } catch { toast.error('Failed to save') } finally { setSaving(false) }
  }, [entry, onSaved])

  return (
    <div style={{ fontFamily: '-aileron-system, BlinkMacSystemFont, "SF Pro Text", sans-serif', padding: 20, maxWidth: 640 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 20 }}>
        <BookOpen size={18} color="#007AFF" />
        <span style={{ fontSize: 16, fontWeight: 600 }}>Add to Agent Knowledge Base</span>
      </div>
      <p style={{ fontSize: 13, color: 'var(--color-text-secondary)', marginBottom: 20 }}>
        Add runbooks, known issues, or architecture context. The RCA agent will retrieve relevant entries when investigating.
      </p>

      <div style={{ marginBottom: 14 }}>
        <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--color-text-secondary)', textTransform: 'uppercase', letterSpacing: '0.05em', display: 'block', marginBottom: 6 }}>Title</label>
        <input value={entry.title} onChange={e => setEntry(p => ({ ...p, title: e.target.value }))}
          placeholder="e.g. OOMKill remediation for payment-service"
          style={{ width: '100%', padding: '8px 12px', borderRadius: 8, border: '0.5px solid rgba(142,142,147,0.3)', fontSize: 14, background: 'var(--color-background)', color: 'var(--color-text)', boxSizing: 'border-box' }} />
      </div>

      <div style={{ marginBottom: 14 }}>
        <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--color-text-secondary)', textTransform: 'uppercase', letterSpacing: '0.05em', display: 'block', marginBottom: 6 }}>Category</label>
        <select value={entry.category} onChange={e => setEntry(p => ({ ...p, category: e.target.value }))}
          style={{ padding: '8px 12px', borderRadius: 8, border: '0.5px solid rgba(142,142,147,0.3)', fontSize: 14, background: 'var(--color-background)', color: 'var(--color-text)' }}>
          {CATEGORIES.map(c => <option key={c} value={c}>{c.replace(/_/g, ' ')}</option>)}
        </select>
      </div>

      <div style={{ marginBottom: 14 }}>
        <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--color-text-secondary)', textTransform: 'uppercase', letterSpacing: '0.05em', display: 'block', marginBottom: 6 }}>Content</label>
        <textarea value={entry.content} onChange={e => setEntry(p => ({ ...p, content: e.target.value }))}
          rows={8}
          placeholder="Describe the issue, symptoms, root cause, and remediation steps in detail. The more specific, the better the agent retrieval."
          style={{ width: '100%', padding: '10px 12px', borderRadius: 8, border: '0.5px solid rgba(142,142,147,0.3)', fontSize: 13, lineHeight: 1.6, background: 'var(--color-background)', color: 'var(--color-text)', resize: 'vertical', fontFamily: 'inherit', boxSizing: 'border-box' }} />
      </div>

      <div style={{ marginBottom: 20 }}>
        <label style={{ fontSize: 12, fontWeight: 600, color: 'var(--color-text-secondary)', textTransform: 'uppercase', letterSpacing: '0.05em', display: 'block', marginBottom: 6 }}>Tags</label>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginBottom: 8 }}>
          {entry.tags.map(tag => (
            <span key={tag} style={{ display: 'flex', alignItems: 'center', gap: 4, padding: '3px 10px', borderRadius: 20, background: 'rgba(0,122,255,0.1)', color: '#007AFF', fontSize: 12 }}>
              {tag}
              <X size={11} style={{ cursor: 'pointer' }} onClick={() => setEntry(p => ({ ...p, tags: p.tags.filter(t => t !== tag) }))} />
            </span>
          ))}
        </div>
        <div style={{ display: 'flex', gap: 8 }}>
          <input value={tagInput} onChange={e => setTagInput(e.target.value)}
            onKeyDown={e => e.key === 'Enter' && addTag()}
            placeholder="Add tag (press Enter)"
            style={{ flex: 1, padding: '6px 10px', borderRadius: 8, border: '0.5px solid rgba(142,142,147,0.3)', fontSize: 13, background: 'var(--color-background)', color: 'var(--color-text)' }} />
          <button onClick={addTag} style={{ padding: '6px 12px', borderRadius: 8, border: '0.5px solid rgba(0,122,255,0.3)', background: 'transparent', color: '#007AFF', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 4, fontSize: 13 }}>
            <Plus size={13} /> Add
          </button>
        </div>
      </div>

      <button onClick={save} disabled={saving || !entry.title || !entry.content}
        style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '10px 20px', borderRadius: 10, background: '#007AFF', color: '#fff', border: 'none', cursor: saving ? 'not-allowed' : 'pointer', fontSize: 14, fontWeight: 600, opacity: saving || !entry.title || !entry.content ? 0.6 : 1 }}>
        <Save size={15} />{saving ? 'Saving…' : 'Save to Knowledge Base'}
      </button>
    </div>
  )
}
