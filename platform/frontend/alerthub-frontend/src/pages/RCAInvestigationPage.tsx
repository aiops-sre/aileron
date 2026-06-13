import React, { useState, useEffect, useCallback } from 'react'
import { motion, AnimatePresence } from 'framer-motion'
import { Search, Play, Brain, Clock, CheckCircle2, AlertTriangle, BookOpen, Cpu, Activity } from 'lucide-react'
import toast from 'react-hot-toast'
import { InvestigationStream } from '@/components/rca/InvestigationStream'
import { KnowledgeEditor } from '@/components/rca/KnowledgeEditor'

const RCA_URL = '/api/v1/rca'

const getAuthHeaders = () => ({
  Authorization: `Bearer ${sessionStorage.getItem('access_token') || localStorage.getItem('access_token') || ''}`,
  'Content-Type': 'application/json',
})

interface InvestigationSummary {
  id: string; alert_title: string; severity: string; phase: string
  started_at: string; summary?: string; root_cause?: { confidence: number; category: string }
}

type Tab = 'active' | 'history' | 'knowledge' | 'model'

const SEVERITY_COLOR: Record<string, string> = {
  critical: '#FF3B30', high: '#FF9500', medium: '#FFCC00', low: '#34C759'
}

export default function RCAInvestigationPage() {
  const [tab, setTab] = useState<Tab>('active')
  const [investigations, setInvestigations] = useState<InvestigationSummary[]>([])
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [modelInfo, setModelInfo] = useState<{ current_model: string; available_models: { name: string }[] } | null>(null)
  const [training, setTraining] = useState(false)

  // Manual investigation form
  const [manualTitle, setManualTitle] = useState('')
  const [manualSeverity, setManualSeverity] = useState('high')
  const [manualNs, setManualNs] = useState('')
  const [manualService, setManualService] = useState('')
  const [manualCluster, setManualCluster] = useState('')
  const [manualPod, setManualPod] = useState('')
  const [starting, setStarting] = useState(false)

  const loadInvestigations = useCallback(async () => {
    try {
      const r = await fetch(`${RCA_URL}/investigations?limit=30`)
      const data = await r.json()
      setInvestigations(Array.isArray(data) ? data : [])
    } catch {}
  }, [])

  const loadModelInfo = useCallback(async () => {
    try {
      const r = await fetch(`${RCA_URL}/model/info`)
      const d = await r.json()
      setModelInfo(d)
    } catch {}
  }, [])

  useEffect(() => {
    loadInvestigations()
    loadModelInfo()
    const interval = setInterval(loadInvestigations, 10000)
    return () => clearInterval(interval)
  }, [loadInvestigations, loadModelInfo])

  const startManualInvestigation = useCallback(async () => {
    if (!manualTitle) return toast.error('Enter an alert title')
    setStarting(true)
    try {
      const r = await fetch(`${RCA_URL}/investigations`, {
        method: 'POST',
        headers: getAuthHeaders(),
        body: JSON.stringify({
          alert_id: `manual-${Date.now()}`,
          alert_title: manualTitle,
          alert_body: {
            title: manualTitle, severity: manualSeverity, source: 'manual',
            ...(manualPod && { pod: manualPod, labels: { pod: manualPod } }),
          },
          severity: manualSeverity,
          namespace: manualNs || null,
          cluster: manualCluster || null,
          service: manualService || null,
        }),
      })
      const d = await r.json()
      setSelectedId(d.investigation_id)
      setTab('active')
      await loadInvestigations()
      toast.success('Investigation started')
      setManualTitle('')
    } catch { toast.error('Failed to start investigation') } finally { setStarting(false) }
  }, [manualTitle, manualSeverity, manualNs, manualService, loadInvestigations])

  const triggerTraining = useCallback(async () => {
    setTraining(true)
    try {
      await fetch(`${RCA_URL}/model/train`, { method: 'POST' })
      toast.success('Model training triggered — will complete in background')
    } catch { toast.error('Training failed') } finally {
      setTimeout(() => setTraining(false), 3000)
    }
  }, [])

  const activeInvs = investigations.filter(i => !['completed', 'failed'].includes(i.phase))
  const doneInvs = investigations.filter(i => ['completed', 'failed'].includes(i.phase))

  return (
    <div style={{ minHeight: '100vh', background: 'var(--color-background)', fontFamily: '-aileron-system, BlinkMacSystemFont, "SF Pro Text", sans-serif' }}>
      <div style={{ maxWidth: 1200, margin: '0 auto', padding: '24px 20px' }}>

        {/* Header */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 28 }}>
          <div style={{ width: 40, height: 40, borderRadius: 12, background: 'linear-gradient(135deg, #007AFF, #AF52DE)', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
            <Brain size={22} color="#fff" />
          </div>
          <div>
            <h1 style={{ fontSize: 22, fontWeight: 700, margin: 0 }}>RCA Investigation Agent</h1>
            <p style={{ fontSize: 13, color: 'var(--color-text-secondary)', margin: 0 }}>
              AI-powered root cause analysis · Powered by {modelInfo?.current_model || 'qwen2.5:14b'} via Ollama
            </p>
          </div>
          {activeInvs.length > 0 && (
            <div style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 6, padding: '6px 12px', borderRadius: 20, background: 'rgba(255,59,48,0.1)', color: '#FF3B30', fontSize: 13, fontWeight: 600 }}>
              <Activity size={14} />
              {activeInvs.length} active
            </div>
          )}
        </div>

        {/* Manual start */}
        <div style={{ background: 'var(--color-card, rgba(255,255,255,0.8))', borderRadius: 14, border: '0.5px solid var(--color-separator)', padding: 20, marginBottom: 24 }}>
          <div style={{ fontSize: 13, fontWeight: 600, marginBottom: 14, color: 'var(--color-text-secondary)' }}>START INVESTIGATION</div>
          <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap' }}>
            <input value={manualTitle} onChange={e => setManualTitle(e.target.value)}
              onKeyDown={e => e.key === 'Enter' && startManualInvestigation()}
              placeholder="Alert title or incident description…"
              style={{ flex: '1 1 300px', padding: '9px 14px', borderRadius: 9, border: '0.5px solid rgba(142,142,147,0.3)', fontSize: 14, background: 'var(--color-background)', color: 'var(--color-text)' }} />
            <select value={manualSeverity} onChange={e => setManualSeverity(e.target.value)}
              style={{ padding: '9px 12px', borderRadius: 9, border: '0.5px solid rgba(142,142,147,0.3)', fontSize: 14, background: 'var(--color-background)', color: SEVERITY_COLOR[manualSeverity] }}>
              {['critical','high','medium','low'].map(s => <option key={s} value={s}>{s}</option>)}
            </select>
            <input value={manualNs} onChange={e => setManualNs(e.target.value)} placeholder="namespace"
              style={{ padding: '9px 12px', borderRadius: 9, border: '0.5px solid rgba(142,142,147,0.3)', fontSize: 14, background: 'var(--color-background)', color: 'var(--color-text)', width: 160 }} />
            <input value={manualCluster} onChange={e => setManualCluster(e.target.value)} placeholder="cluster (e.g. example-cluster)"
              style={{ padding: '9px 12px', borderRadius: 9, border: '0.5px solid rgba(142,142,147,0.3)', fontSize: 14, background: 'var(--color-background)', color: 'var(--color-text)', width: 190 }} />
            <input value={manualPod} onChange={e => setManualPod(e.target.value)} placeholder="pod name (optional)"
              style={{ padding: '9px 12px', borderRadius: 9, border: '0.5px solid rgba(142,142,147,0.3)', fontSize: 14, background: 'var(--color-background)', color: 'var(--color-text)', width: 190 }} />
            <input value={manualService} onChange={e => setManualService(e.target.value)} placeholder="service (optional)"
              style={{ padding: '9px 12px', borderRadius: 9, border: '0.5px solid rgba(142,142,147,0.3)', fontSize: 14, background: 'var(--color-background)', color: 'var(--color-text)', width: 150 }} />
            <button onClick={startManualInvestigation} disabled={starting || !manualTitle}
              style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '9px 18px', borderRadius: 9, background: '#007AFF', color: '#fff', border: 'none', cursor: starting || !manualTitle ? 'not-allowed' : 'pointer', fontSize: 14, fontWeight: 600, opacity: starting || !manualTitle ? 0.6 : 1 }}>
              <Play size={14} />{starting ? 'Starting…' : 'Investigate'}
            </button>
          </div>
        </div>

        {/* Tabs */}
        <div style={{ display: 'flex', gap: 2, marginBottom: 20, background: 'rgba(142,142,147,0.08)', borderRadius: 10, padding: 3, width: 'fit-content' }}>
          {([['active', 'Investigations', activeInvs.length], ['history', 'History', doneInvs.length], ['knowledge', 'Knowledge Base', null], ['model', 'Model', null]] as [Tab, string, number|null][]).map(([t, label, count]) => (
            <button key={t} onClick={() => setTab(t)}
              style={{ padding: '7px 16px', borderRadius: 8, border: 'none', cursor: 'pointer', fontSize: 13, fontWeight: tab === t ? 600 : 400,
                background: tab === t ? 'var(--color-card, white)' : 'transparent',
                color: tab === t ? 'var(--color-text)' : 'var(--color-text-secondary)',
                boxShadow: tab === t ? '0 1px 4px rgba(0,0,0,0.1)' : 'none' }}>
              {label}{count != null ? ` (${count})` : ''}
            </button>
          ))}
        </div>

        {/* Content */}
        <AnimatePresence mode="wait">
          {tab === 'active' && (
            <motion.div key="active" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}
              style={{ display: 'grid', gridTemplateColumns: selectedId ? '280px 1fr' : '1fr', gap: 16 }}>
              {/* Investigation list */}
              <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                {investigations.length === 0 && (
                  <div style={{ textAlign: 'center', padding: 40, color: 'var(--color-text-secondary)', fontSize: 14 }}>
                    No investigations yet. Investigations start automatically for critical alerts, or trigger one manually above.
                  </div>
                )}
                {investigations.map(inv => (
                  <div key={inv.id} onClick={() => setSelectedId(inv.id === selectedId ? null : inv.id)}
                    style={{ padding: '12px 14px', borderRadius: 10, border: `0.5px solid ${inv.id === selectedId ? '#007AFF' : 'var(--color-separator)'}`,
                      background: inv.id === selectedId ? 'rgba(0,122,255,0.06)' : 'var(--color-card, rgba(255,255,255,0.8))',
                      cursor: 'pointer', transition: 'all 0.15s' }}>
                    <div style={{ display: 'flex', alignItems: 'flex-start', gap: 8 }}>
                      {inv.phase === 'completed' ? <CheckCircle2 size={14} color="#34C759" style={{ flexShrink: 0, marginTop: 2 }} /> :
                       inv.phase === 'failed' ? <AlertTriangle size={14} color="#FF3B30" style={{ flexShrink: 0, marginTop: 2 }} /> :
                       <motion.div animate={{ rotate: 360 }} transition={{ duration: 2, repeat: Infinity, ease: 'linear' }} style={{ flexShrink: 0, marginTop: 2 }}>
                         <Activity size={14} color="#007AFF" />
                       </motion.div>}
                      <div style={{ flex: 1, minWidth: 0 }}>
                        <div style={{ fontSize: 13, fontWeight: 500, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{inv.alert_title}</div>
                        <div style={{ fontSize: 11, color: 'var(--color-text-secondary)', marginTop: 2, display: 'flex', gap: 8 }}>
                          <span style={{ color: SEVERITY_COLOR[inv.severity] }}>{inv.severity}</span>
                          <span>{inv.phase.replace(/_/g, ' ')}</span>
                          {inv.root_cause && <span style={{ color: '#34C759' }}>{Math.round(inv.root_cause.confidence * 100)}% conf</span>}
                        </div>
                      </div>
                    </div>
                  </div>
                ))}
              </div>
              {selectedId && <InvestigationStream investigationId={selectedId} />}
            </motion.div>
          )}

          {tab === 'history' && (
            <motion.div key="history" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}>
              {doneInvs.length === 0 ? (
                <div style={{ textAlign: 'center', padding: 60, color: 'var(--color-text-secondary)', fontSize: 14 }}>No completed investigations yet</div>
              ) : (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                  {doneInvs.map(inv => (
                    <div key={inv.id} onClick={() => { setSelectedId(inv.id); setTab('active') }}
                      style={{ padding: '14px 16px', borderRadius: 10, border: '0.5px solid var(--color-separator)', background: 'var(--color-card, rgba(255,255,255,0.8))', cursor: 'pointer' }}>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: inv.summary ? 6 : 0 }}>
                        {inv.phase === 'completed' ? <CheckCircle2 size={14} color="#34C759" /> : <AlertTriangle size={14} color="#FF3B30" />}
                        <span style={{ fontSize: 14, fontWeight: 500 }}>{inv.alert_title}</span>
                        {inv.root_cause && (
                          <span style={{ marginLeft: 'auto', fontSize: 12, padding: '2px 8px', borderRadius: 6, background: 'rgba(52,199,89,0.1)', color: '#34C759' }}>
                            {inv.root_cause.category.replace(/_/g, ' ')} · {Math.round(inv.root_cause.confidence * 100)}%
                          </span>
                        )}
                      </div>
                      {inv.summary && <p style={{ fontSize: 13, color: 'var(--color-text-secondary)', margin: 0 }}>{inv.summary}</p>}
                    </div>
                  ))}
                </div>
              )}
            </motion.div>
          )}

          {tab === 'knowledge' && (
            <motion.div key="knowledge" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}>
              <KnowledgeEditor />
            </motion.div>
          )}

          {tab === 'model' && (
            <motion.div key="model" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}
              style={{ maxWidth: 600 }}>
              <div style={{ background: 'var(--color-card, rgba(255,255,255,0.8))', borderRadius: 14, border: '0.5px solid var(--color-separator)', padding: 24 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 20 }}>
                  <Cpu size={20} color="#007AFF" />
                  <h2 style={{ margin: 0, fontSize: 17, fontWeight: 600 }}>Local Model</h2>
                </div>
                <div style={{ marginBottom: 16 }}>
                  <div style={{ fontSize: 12, color: 'var(--color-text-secondary)', fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 6 }}>Active Model</div>
                  <code style={{ fontSize: 16, fontWeight: 600, color: '#007AFF', background: 'rgba(0,122,255,0.08)', padding: '4px 12px', borderRadius: 8 }}>{modelInfo?.current_model || 'qwen2.5:14b'}</code>
                </div>
                {modelInfo?.available_models && (
                  <div style={{ marginBottom: 20 }}>
                    <div style={{ fontSize: 12, color: 'var(--color-text-secondary)', fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 8 }}>Available Models</div>
                    {modelInfo.available_models.map((m, i) => (
                      <div key={i} style={{ fontSize: 13, padding: '4px 0', color: 'var(--color-text)' }}>
                        <code>{m.name}</code>
                      </div>
                    ))}
                  </div>
                )}
                <div style={{ padding: 16, background: 'rgba(0,122,255,0.06)', borderRadius: 10, marginBottom: 20 }}>
                  <div style={{ fontSize: 13, fontWeight: 600, marginBottom: 6 }}>How model learning works</div>
                  <ul style={{ margin: 0, paddingLeft: 18, fontSize: 13, lineHeight: 1.8, color: 'var(--color-text-secondary)' }}>
                    <li>Every confirmed investigation is stored in Weaviate with embeddings</li>
                    <li>New investigations retrieve top-3 similar past incidents as context (RAG)</li>
                    <li>Your feedback scores weight which incidents are most authoritative</li>
                    <li>Weekly: a custom Ollama model is built with learned patterns in system prompt</li>
                    <li>Incidents auto-ingested from Kafka correlation-results topic</li>
                  </ul>
                </div>
                <button onClick={triggerTraining} disabled={training}
                  style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '10px 20px', borderRadius: 10, background: training ? 'rgba(0,122,255,0.5)' : '#007AFF', color: '#fff', border: 'none', cursor: training ? 'not-allowed' : 'pointer', fontSize: 14, fontWeight: 600 }}>
                  <Brain size={15} />{training ? 'Training in progress…' : 'Trigger model update now'}
                </button>
              </div>
            </motion.div>
          )}
        </AnimatePresence>
      </div>
    </div>
  )
}
