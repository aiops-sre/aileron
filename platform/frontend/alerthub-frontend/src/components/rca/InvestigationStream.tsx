import React, { useEffect, useRef, useState, useCallback } from 'react'
import { motion, AnimatePresence } from 'framer-motion'
import {
  Brain, Wrench, CheckCircle2, AlertTriangle, Clock, ChevronDown,
  ChevronRight, ThumbsUp, ThumbsDown, Zap, Activity, Search,
  RefreshCw, BookOpen, TrendingUp,
} from 'lucide-react'
import toast from 'react-hot-toast'

interface ToolCallEvent { tool: string; args: Record<string, unknown>; result_preview?: string; duration_ms?: number }
interface StreamEvent {
  type: 'thought' | 'tool_call' | 'tool_result' | 'phase_change' | 'result' | 'error' | 'heartbeat'
  phase: string
  data: unknown
  timestamp: string
}
interface RootCause {
  summary: string; component: string; category: string
  confidence: number; evidence: string[]; timeline: { time: string; event: string }[]
}
interface RemediationStep {
  step: number; action: string; command?: string; automated: boolean; risk: string
}
interface InvestigationResult { summary: string; root_cause: RootCause; remediation: RemediationStep[] }

const PHASE_LABELS: Record<string, string> = {
  queued: 'Queued', context_gathering: 'Gathering Context',
  hypothesis_formation: 'Forming Hypotheses', evidence_collection: 'Collecting Evidence',
  root_cause_analysis: 'Root Cause Analysis', remediation_planning: 'Planning Remediation',
  completed: 'Complete', failed: 'Failed',
}
const PHASE_ORDER = ['queued','context_gathering','hypothesis_formation','evidence_collection','root_cause_analysis','remediation_planning','completed']

const RCA_URL = '/api/v1/rca'
const rcaAuthHeader = () => ({ 'Content-Type': 'application/json', 'Authorization': `Bearer ${sessionStorage.getItem('access_token') || localStorage.getItem('access_token') || ''}` })

export function InvestigationStream({ investigationId }: { investigationId: string }) {
  const [thoughts, setThoughts] = useState<string[]>([])
  const [toolCalls, setToolCalls] = useState<ToolCallEvent[]>([])
  const [currentPhase, setCurrentPhase] = useState('queued')
  const [result, setResult] = useState<InvestigationResult | null>(null)
  const [error, setError] = useState('')
  const [connected, setConnected] = useState(false)
  const [expandedTool, setExpandedTool] = useState<number | null>(null)
  const [feedback, setFeedback] = useState<number | null>(null)
  const [feedbackSent, setFeedbackSent] = useState(false)
  const [forecast, setForecast] = useState('')
  const [loading, setLoading] = useState(true)
  const thoughtsEndRef = useRef<HTMLDivElement>(null)
  const wsRef = useRef<WebSocket | null>(null)

  // Load existing investigation state via HTTP first, then conditionally open WebSocket
  useEffect(() => {
    let cancelled = false
    async function loadExisting() {
      try {
        const r = await fetch(`${RCA_URL}/investigations/${investigationId}`, { headers: rcaAuthHeader() })
        if (!r.ok) {
          // 404 = investigation not in memory/db; fall through to WebSocket as last resort
          setLoading(false)
          if (r.status === 404) {
            setError('Investigation record not found. It may have been from a previous deployment. Use "Re-investigate" to run a fresh analysis.')
            return
          }
          // Other errors: try WebSocket anyway
          if (!cancelled) openWebSocket()
          return
        }
        const data = await r.json()
        if (cancelled) return
        setCurrentPhase(data.phase || 'queued')
        if (data.thought_log?.length) setThoughts(data.thought_log)
        if (data.tool_calls?.length) {
          setToolCalls(data.tool_calls.map((tc: any) => ({
            tool: tc.tool, args: tc.args, result_preview: tc.result?.slice(0, 200), duration_ms: tc.duration_ms
          })))
        }
        // If already completed, reconstruct result from the stored investigation
        if (data.phase === 'completed' && data.root_cause) {
          setResult({
            summary: data.summary || data.root_cause.summary || '',
            root_cause: {
              summary: data.root_cause.summary || '',
              component: data.root_cause.component || 'unknown',
              category: data.root_cause.category || 'unknown',
              confidence: data.root_cause.confidence ?? 0.5,
              evidence: data.root_cause.evidence || [],
              timeline: data.root_cause.timeline || [],
            },
            remediation: (data.remediation || []).map((s: any) => ({
              step: s.step, action: s.action, command: s.command,
              automated: s.automated ?? false, risk: s.risk || 'low',
            })),
          })
          setLoading(false)
          return  // no need for WebSocket
        }
        if (data.phase === 'failed') {
          setError('Investigation failed — see logs or re-trigger')
          setLoading(false)
          return
        }
        // completed but no root_cause, OR still in progress — fall through to WebSocket
      } catch { /* network error — try WebSocket */ }
      setLoading(false)
      if (cancelled) return
      // Still in progress or unknown state — open WebSocket for live updates
      openWebSocket()
    }

    function openWebSocket() {
      const wsUrl = `${window.location.protocol === 'https:' ? 'wss' : 'ws'}://${window.location.host}/ws/investigations/${investigationId}`
      const ws = new WebSocket(wsUrl)
      wsRef.current = ws
      ws.onopen = () => setConnected(true)
      ws.onclose = (ev) => {
        setConnected(false)
        if (ev.code === 1008) {
          setError('Live stream unavailable — investigation is not currently running. Results (if any) are shown above.')
        }
      }
      ws.onerror = () => setError('WebSocket connection failed')
      ws.onmessage = (e) => {
        try {
          const event: StreamEvent = JSON.parse(e.data)
          if (event.type === 'heartbeat') return
          if (event.type === 'phase_change') setCurrentPhase(event.data as string)
          else if (event.type === 'thought') setThoughts(p => [...p, event.data as string])
          else if (event.type === 'tool_call') setToolCalls(p => [...p, event.data as ToolCallEvent])
          else if (event.type === 'tool_result') {
            setToolCalls(p => p.map((tc, i) => i === p.length - 1 ? { ...tc, ...(event.data as object) } : tc))
          }
          else if (event.type === 'result') setResult(event.data as InvestigationResult)
          else if (event.type === 'error') setError(event.data as string)
        } catch {}
      }
    }

    loadExisting()
    return () => {
      cancelled = true
      const ws = wsRef.current
      if (ws) {
        ws.onmessage = null; ws.onerror = null; ws.onclose = null
        if (ws.readyState === WebSocket.OPEN) ws.close(1000, 'unmounted')
        else if (ws.readyState === WebSocket.CONNECTING) ws.onopen = () => ws.close(1000, 'unmounted')
        wsRef.current = null
      }
    }
  }, [investigationId])

  useEffect(() => {
    thoughtsEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [thoughts])

  const sendFeedback = useCallback(async (score: number) => {
    setFeedback(score)
    try {
      await fetch(`${RCA_URL}/investigations/${investigationId}/feedback`, {
        method: 'POST',
        headers: rcaAuthHeader(),
        body: JSON.stringify({ investigation_id: investigationId, score, confirmed: score >= 4 }),
      })
      setFeedbackSent(true)
      toast.success('Feedback saved — helps train the model')
    } catch { toast.error('Failed to save feedback') }
  }, [investigationId])

  const loadForecast = useCallback(async () => {
    try {
      const r = await fetch(`${RCA_URL}/forecast/${investigationId}`, { method: 'POST', headers: rcaAuthHeader() })
      const d = await r.json()
      setForecast(d.forecast || '')
    } catch { toast.error('Forecast failed') }
  }, [investigationId])

  const phaseIndex = PHASE_ORDER.indexOf(currentPhase)

  if (loading) {
    return (
      <div style={{ padding: 24, display: 'flex', alignItems: 'center', gap: 10, color: 'var(--color-text-secondary)', fontSize: 14 }}>
        <motion.div animate={{ rotate: 360 }} transition={{ duration: 1.5, repeat: Infinity, ease: 'linear' }}>
          <RefreshCw size={16} color="#007AFF" />
        </motion.div>
        Loading investigation…
      </div>
    )
  }

  return (
    <div style={{ fontFamily: '-apple-system, BlinkMacSystemFont, "SF Pro Text", sans-serif', padding: 24, maxWidth: 900 }}>
      {/* Phase progress bar */}
      <div style={{ display: 'flex', gap: 4, marginBottom: 24, alignItems: 'center' }}>
        {PHASE_ORDER.filter(p => p !== 'queued').map((phase, i) => {
          const idx = PHASE_ORDER.indexOf(phase) - 1
          const done = phaseIndex - 1 > idx
          const active = phaseIndex - 1 === idx
          return (
            <React.Fragment key={phase}>
              <div style={{
                flex: 1, height: 4, borderRadius: 2,
                background: done ? '#34C759' : active ? '#007AFF' : 'rgba(142,142,147,0.2)',
                transition: 'background 0.3s',
              }} />
            </React.Fragment>
          )
        })}
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 20 }}>
        {currentPhase === 'completed' ? <CheckCircle2 size={20} color="#34C759" /> :
         currentPhase === 'failed' ? <AlertTriangle size={20} color="#FF3B30" /> :
         <motion.div animate={{ rotate: 360 }} transition={{ duration: 2, repeat: Infinity, ease: 'linear' }}>
           <RefreshCw size={18} color="#007AFF" />
         </motion.div>}
        <span style={{ fontSize: 15, fontWeight: 600, color: 'var(--color-text)' }}>
          {PHASE_LABELS[currentPhase] || currentPhase}
        </span>
        <span style={{ marginLeft: 'auto', fontSize: 12, color: connected ? '#34C759' : '#FF3B30' }}>
          ● {connected ? 'Live' : 'Disconnected'}
        </span>
      </div>

      {/* Completed but no root_cause — LLM timed out previously, offer re-investigation */}
      {currentPhase === 'completed' && !result && !error && (
        <div style={{ background: 'rgba(255,149,0,0.08)', border: '0.5px solid rgba(255,149,0,0.3)', borderRadius: 10, padding: 16, marginBottom: 16 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
            <AlertTriangle size={16} color="#FF9500" />
            <span style={{ fontSize: 14, fontWeight: 600, color: '#FF9500' }}>Root cause analysis incomplete</span>
          </div>
          <p style={{ fontSize: 13, color: 'var(--color-text-secondary)', margin: '0 0 12px 0' }}>
            The LLM agent timed out during the previous investigation run. Click below to re-run with the updated model.
          </p>
          <button
            onClick={async () => {
              try {
                const r = await fetch(`${RCA_URL}/investigations/${investigationId}`, { headers: rcaAuthHeader() })
                const d = await r.json()
                const body = {
                  alert_id: d.alert_id, alert_title: d.alert_title, alert_body: d.alert_body,
                  severity: d.severity, incident_id: d.incident_id,
                  namespace: d.namespace, cluster: d.cluster, service: d.service,
                }
                const nr = await fetch(`${RCA_URL}/investigations`, {
                  method: 'POST', headers: rcaAuthHeader(), body: JSON.stringify(body),
                })
                const nd = await nr.json()
                toast.success('Re-investigation started')
                // Navigate to the new investigation
                window.location.href = window.location.href.replace(investigationId, nd.investigation_id)
              } catch { toast.error('Failed to start re-investigation') }
            }}
            style={{ padding: '8px 16px', borderRadius: 8, border: 'none', background: '#FF9500', color: '#fff', cursor: 'pointer', fontSize: 13, fontWeight: 600 }}
          >
            Re-investigate
          </button>
        </div>
      )}

      {/* Tool calls */}
      {toolCalls.length > 0 && (
        <div style={{ marginBottom: 16 }}>
          <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--color-text-secondary)', marginBottom: 8, textTransform: 'uppercase', letterSpacing: '0.05em' }}>
            Tool Calls ({toolCalls.length})
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
            {toolCalls.map((tc, i) => (
              <div key={i}
                onClick={() => setExpandedTool(expandedTool === i ? null : i)}
                style={{ background: 'rgba(0,122,255,0.06)', borderRadius: 8, padding: '8px 12px', cursor: 'pointer', border: '0.5px solid rgba(0,122,255,0.15)' }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <Wrench size={13} color="#007AFF" />
                  <span style={{ fontSize: 13, fontWeight: 500, color: '#007AFF', fontFamily: 'monospace' }}>{tc.tool}</span>
                  {tc.duration_ms && <span style={{ fontSize: 11, color: 'var(--color-text-tertiary)', marginLeft: 'auto' }}>{tc.duration_ms}ms</span>}
                  {expandedTool === i ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
                </div>
                <AnimatePresence>
                  {expandedTool === i && (
                    <motion.div initial={{ height: 0 }} animate={{ height: 'auto' }} exit={{ height: 0 }} style={{ overflow: 'hidden' }}>
                      <pre style={{ fontSize: 11, marginTop: 8, color: 'var(--color-text-secondary)', whiteSpace: 'pre-wrap' }}>
                        {JSON.stringify(tc.args, null, 2)}
                      </pre>
                      {tc.result_preview && <div style={{ fontSize: 11, color: 'var(--color-text-tertiary)', marginTop: 4 }}>→ {tc.result_preview}</div>}
                    </motion.div>
                  )}
                </AnimatePresence>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Thoughts stream */}
      {thoughts.length > 0 && (
        <div style={{ marginBottom: 16 }}>
          <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--color-text-secondary)', marginBottom: 8, textTransform: 'uppercase', letterSpacing: '0.05em', display: 'flex', alignItems: 'center', gap: 6 }}>
            <Brain size={13} /> Agent Thoughts
          </div>
          <div style={{ background: 'rgba(142,142,147,0.06)', borderRadius: 10, padding: 14, maxHeight: 300, overflowY: 'auto' }}>
            {thoughts.map((t, i) => (
              <motion.p key={i} initial={{ opacity: 0, y: 4 }} animate={{ opacity: 1, y: 0 }}
                style={{ fontSize: 13, lineHeight: 1.6, color: 'var(--color-text)', margin: '0 0 8px 0' }}>
                {t}
              </motion.p>
            ))}
            <div ref={thoughtsEndRef} />
          </div>
        </div>
      )}

      {/* Result */}
      <AnimatePresence>
        {result && (
          <motion.div initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }}
            style={{ background: 'rgba(52,199,89,0.08)', border: '0.5px solid rgba(52,199,89,0.3)', borderRadius: 12, padding: 20, marginBottom: 16 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12 }}>
              <CheckCircle2 size={18} color="#34C759" />
              <span style={{ fontSize: 16, fontWeight: 600 }}>Root Cause Found</span>
              <span style={{ marginLeft: 'auto', fontSize: 13, color: result.root_cause.confidence >= 0.8 ? '#34C759' : '#FF9500',
                fontWeight: 600 }}>
                {Math.round(result.root_cause.confidence * 100)}% confidence
              </span>
            </div>
            <p style={{ fontSize: 14, fontWeight: 500, marginBottom: 8 }}>{result.summary}</p>
            <div style={{ fontSize: 13, color: 'var(--color-text-secondary)', marginBottom: 12 }}>
              <strong>Component:</strong> <code style={{ background: 'rgba(0,0,0,0.05)', padding: '1px 6px', borderRadius: 4 }}>{result.root_cause.component}</code>
              {'  '}<strong>Category:</strong> {result.root_cause.category.replace(/_/g, ' ')}
            </div>
            {result.root_cause.evidence.length > 0 && (
              <div style={{ marginBottom: 12 }}>
                <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--color-text-secondary)', marginBottom: 6 }}>EVIDENCE</div>
                {result.root_cause.evidence.map((e, i) => (
                  <div key={i} style={{ fontSize: 12, color: 'var(--color-text)', padding: '3px 0', display: 'flex', gap: 6 }}>
                    <span style={{ color: '#34C759' }}>•</span>{e}
                  </div>
                ))}
              </div>
            )}
            {result.remediation.length > 0 && (
              <div>
                <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--color-text-secondary)', marginBottom: 8 }}>REMEDIATION STEPS</div>
                {result.remediation.map(step => (
                  <div key={step.step} style={{ marginBottom: 8, padding: 10, background: 'rgba(0,0,0,0.03)', borderRadius: 8 }}>
                    <div style={{ fontSize: 13, fontWeight: 500, marginBottom: step.command ? 4 : 0 }}>
                      <span style={{ color: '#007AFF', marginRight: 8 }}>{step.step}.</span>{step.action}
                      <span style={{ float: 'right', fontSize: 11, padding: '1px 6px', borderRadius: 4,
                        background: step.risk === 'high' ? '#FF3B3020' : step.risk === 'medium' ? '#FF950020' : '#34C75920',
                        color: step.risk === 'high' ? '#FF3B30' : step.risk === 'medium' ? '#FF9500' : '#34C759' }}>
                        {step.risk} risk
                      </span>
                    </div>
                    {step.command && (
                      <code style={{ display: 'block', fontSize: 12, background: 'rgba(0,0,0,0.06)', padding: '4px 8px', borderRadius: 6, fontFamily: 'monospace', marginTop: 4 }}>
                        {step.command}
                      </code>
                    )}
                  </div>
                ))}
              </div>
            )}

            {/* Feedback */}
            {!feedbackSent ? (
              <div style={{ marginTop: 16, paddingTop: 16, borderTop: '0.5px solid rgba(0,0,0,0.1)', display: 'flex', alignItems: 'center', gap: 8 }}>
                <span style={{ fontSize: 12, color: 'var(--color-text-secondary)' }}>Was this RCA correct?</span>
                {[1,2,3,4,5].map(s => (
                  <button key={s} onClick={() => sendFeedback(s)}
                    style={{ width: 32, height: 32, borderRadius: 8, border: '0.5px solid rgba(142,142,147,0.3)', cursor: 'pointer',
                      background: feedback === s ? '#007AFF' : 'transparent', color: feedback === s ? '#fff' : 'var(--color-text)', fontSize: 13 }}>
                    {s}
                  </button>
                ))}
                <button onClick={loadForecast} style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 4, padding: '6px 12px', borderRadius: 8, border: '0.5px solid rgba(0,122,255,0.3)', background: 'transparent', cursor: 'pointer', fontSize: 12, color: '#007AFF' }}>
                  <TrendingUp size={13} /> Forecast escalation
                </button>
              </div>
            ) : (
              <div style={{ marginTop: 12, fontSize: 13, color: '#34C759', display: 'flex', alignItems: 'center', gap: 6 }}>
                <CheckCircle2 size={14} /> Feedback saved — training the model
              </div>
            )}
          </motion.div>
        )}
      </AnimatePresence>

      {/* Forecast */}
      {forecast && (
        <motion.div initial={{ opacity: 0 }} animate={{ opacity: 1 }}
          style={{ background: 'rgba(255,149,0,0.08)', border: '0.5px solid rgba(255,149,0,0.3)', borderRadius: 10, padding: 16, marginBottom: 16 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 8, fontSize: 13, fontWeight: 600, color: '#FF9500' }}>
            <TrendingUp size={15} /> Escalation Forecast
          </div>
          <p style={{ fontSize: 13, lineHeight: 1.6, color: 'var(--color-text)', margin: 0 }}>{forecast}</p>
        </motion.div>
      )}

      {error && (
        <div style={{ background: 'rgba(255,59,48,0.08)', border: '0.5px solid rgba(255,59,48,0.3)', borderRadius: 10, padding: 14, fontSize: 13, color: '#FF3B30' }}>
          <AlertTriangle size={14} style={{ display: 'inline', marginRight: 6 }} />{error}
        </div>
      )}
    </div>
  )
}
