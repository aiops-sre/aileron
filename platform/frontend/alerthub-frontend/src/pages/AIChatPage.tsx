import React, { useState, useEffect, useRef, useCallback } from 'react'
import { motion, AnimatePresence } from 'framer-motion'
import {
  MessageCircle, Plus, Search, Download, Key, Send, Copy, Trash2,
  Loader2, CheckCircle, XCircle, Sparkles, X, Eye, EyeOff, User,
  Keyboard, Paperclip, Mic, Camera, AlertTriangle, Activity,
  BarChart3, GitBranch, Brain, Shield, ChevronRight, Play,
  MessageSquare, Bot, Cpu, StopCircle, RotateCcw, ChevronDown, Pencil,
  Server, Layers, TrendingUp, Zap,
} from 'lucide-react'
import { MarkdownRenderer } from '../components/MarkdownRenderer'
import type { ChatMessage, AIModel } from '../services/AIService'
import { useKeyboard } from '../hooks/useKeyboard'
import { useSound, useSoundSettings } from '../hooks/useSound'
import { useSettingsStore } from '../stores/settingsStore'
import { useTheme } from '../hooks/useTheme'
import {
  aiApi, workflowApi, correlationApi, providersApi,
  incidentsApi, analyticsApi, topologyApi,
} from '../lib/api'

// ─── Design tokens ────────────────────────────────────────────────────────────
const t = {
  blue:   '#007AFF', green:  '#34C759', red:    '#FF3B30', orange: '#FF9500',
  purple: '#AF52DE', indigo: '#5856D6', teal:   '#5AC8FA', gray:   '#8E8E93',
  label:  'var(--color-text)',
  sub:    'var(--color-text-secondary)',
  dim:    'var(--color-text-tertiary, #8E8E93)',
  sep:    'var(--color-separator, rgba(142,142,147,0.14))',
  fill:   'var(--color-fill, rgba(142,142,147,0.08))',
  fill2:  'rgba(142,142,147,0.14)',
  bg:     'var(--color-background)',
  card:   'var(--color-card, rgba(255,255,255,0.92))',
  r: { xs: 4, sm: 8, md: 12, lg: 16, xl: 20, '2xl': 24 },
} as const

// Returns a stable key suffix for the current user so caches are never shared across accounts.
const getCurrentUserKey = (): string => {
  try {
    const u = sessionStorage.getItem('user')
    if (u) { const parsed = JSON.parse(u); const key = parsed.id || parsed.email; if (key) return key }
  } catch {}
  // Fallback: decode user ID from JWT so sessions are never cached under the shared 'anon' key
  // when sessionStorage is missing (e.g. fresh tab with token persisted only in localStorage).
  try {
    const token = sessionStorage.getItem('access_token') || localStorage.getItem('access_token')
    if (token) {
      const payload = JSON.parse(atob(token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/')))
      const id = payload.sub || payload.user_id
      if (id) return id
    }
  } catch {}
  return 'anon'
}

const CONFIG = {
  MAX_RETRIES: 3, RETRY_DELAY: 1000, MODEL_CACHE_TTL: 300000,
  SESSION_CACHE_TTL: 300000, DRAFT_SAVE_DELAY: 1000,
  MAX_MESSAGE_LENGTH: 10000, OFFLINE_CHECK_INTERVAL: 30000,
  API_TIMEOUT: 60000, STREAM_PARA_DELAY: 55, SCROLL_THRESHOLD: 180,
}

// ─── Types ────────────────────────────────────────────────────────────────────
interface Message extends ChatMessage { id: string; timestamp: string; bookmarked?: boolean }
interface LocalSession { id: string; title: string; messages: Message[]; createdAt: string; pinned?: boolean }
interface VoiceState { isListening: boolean; interim: string; final: string }

// ─── Commands catalogue ───────────────────────────────────────────────────────
const COMMANDS = [
  {
    group: 'Alerts & Incidents', color: t.red, icon: AlertTriangle,
    items: [
      { id: '/analyze-alerts',    label: 'Analyze Alerts',       desc: 'AI review of active alert patterns' },
      { id: '/alert-fatigue',     label: 'Alert Fatigue',        desc: 'Identify noisy, low-value alerts' },
      { id: '/predict-incidents', label: 'Predict Incidents',    desc: 'Predict escalations from current clusters' },
      { id: '/incidents-summary', label: 'Incidents Summary',    desc: 'Recent open incidents and stats' },
    ],
  },
  {
    group: 'Infrastructure', color: t.blue, icon: Server,
    items: [
      { id: '/infrastructure-status', label: 'Infra Topology',    desc: 'Infrastructure graph & node health' },
      { id: '/kubernetes-health',     label: 'Kubernetes Health', desc: 'Cluster health & workload status' },
      { id: '/system-overview',       label: 'System Overview',   desc: 'Full health snapshot across all services' },
    ],
  },
  {
    group: 'Analytics & Operations', color: t.purple, icon: TrendingUp,
    items: [
      { id: '/analytics-overview',    label: 'Analytics Overview',  desc: 'Dashboard metrics and trends' },
      { id: '/correlation-analysis',  label: 'Correlation Analysis',desc: 'Inspect current alert correlation clusters' },
      { id: '/rca-suggest',           label: 'RCA Suggest',         desc: 'AI root cause analysis for recent incidents' },
    ],
  },
  {
    group: 'Automation', color: t.green, icon: Zap,
    items: [
      { id: '/workflow-status',  label: 'Workflow Status',  desc: 'Review and optimize your automations' },
      { id: '/execute-workflow', label: 'Execute Workflow', desc: 'Trigger an automation from the AI' },
      { id: '/provider-health',  label: 'Provider Health',  desc: 'Health status of monitoring providers' },
    ],
  },
]

// ─── Token modal ──────────────────────────────────────────────────────────────
function TokenModal({ isOpen, onClose, onSave, token, setToken, expiry, setExpiry, error, isLoading }: {
  isOpen: boolean; onClose: () => void; onSave: () => void
  token: string; setToken: (v: string) => void
  expiry: string; setExpiry: (v: string) => void
  error: string | null; isLoading: boolean
}) {
  const [show, setShow] = useState(false)
  const [copied, setCopied] = useState(false)
  if (!isOpen) return null
  const cmd = `oidc-helper getToken -C hvys3fcwcteqrvw3qzkvtk86viuoqv --token-type=oauth --interactivity-type=none -E prod -G pkce -o openid,dsid,accountname,profile,groups | grep 'oauth-id-token' | awk '{print $2}'`
  return (
    <AnimatePresence>
      <motion.div initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}
        onClick={onClose}
        style={{ position: 'fixed', inset: 0, zIndex: 100, display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'rgba(0,0,0,0.5)', backdropFilter: 'blur(20px)' }}>
        <motion.div initial={{ opacity: 0, scale: 0.94, y: 12 }} animate={{ opacity: 1, scale: 1, y: 0 }}
          exit={{ opacity: 0, scale: 0.94, y: 12 }} transition={{ type: 'spring', damping: 28, stiffness: 380 }}
          onClick={e => e.stopPropagation()}
          style={{ width: '100%', maxWidth: 520, background: t.card, borderRadius: t.r['2xl'], boxShadow: '0 32px 80px rgba(0,0,0,0.25)', margin: '0 16px', overflow: 'hidden' }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '14px 20px', borderBottom: `1px solid ${t.sep}` }}>
            <button onClick={onClose} style={{ fontSize: 15, color: t.blue, background: 'none', border: 'none', cursor: 'pointer' }}>Cancel</button>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <div style={{ width: 28, height: 28, borderRadius: t.r.sm, background: `linear-gradient(135deg, ${t.blue}, ${t.indigo})`, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                <Key size={14} color="#fff" />
              </div>
              <span style={{ fontSize: 15, fontWeight: 600, color: t.label }}>OIDC Provider Token</span>
            </div>
            <div style={{ width: 60 }} />
          </div>
          <div style={{ padding: 20 }}>
            <p style={{ fontSize: 13, color: t.sub, marginBottom: 12 }}>Run this in your Mac terminal to get a token:</p>
            <div style={{ position: 'relative', marginBottom: 16 }}>
              <pre style={{ fontSize: 11, background: '#111', color: '#00e676', padding: 14, borderRadius: t.r.md, overflow: 'auto', margin: 0, lineHeight: 1.5, fontFamily: 'monospace' }}>{cmd}</pre>
              <button onClick={() => { navigator.clipboard.writeText(cmd); setCopied(true); setTimeout(() => setCopied(false), 2000) }}
                style={{ position: 'absolute', top: 8, right: 8, padding: '4px 8px', borderRadius: t.r.xs, background: 'rgba(255,255,255,0.12)', border: '1px solid rgba(255,255,255,0.2)', color: copied ? '#00e676' : '#fff', cursor: 'pointer', fontSize: 11, display: 'flex', alignItems: 'center', gap: 4 }}>
                {copied ? <><CheckCircle size={11} />Copied!</> : <><Copy size={11} />Copy</>}
              </button>
            </div>
            <div style={{ marginBottom: 16 }}>
              <label style={{ display: 'block', fontSize: 12, fontWeight: 500, color: t.sub, marginBottom: 6 }}>Paste token here</label>
              <div style={{ position: 'relative' }}>
                <textarea value={token} onChange={e => setToken(e.target.value)} rows={3} placeholder="eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9..."
                  style={{ width: '100%', borderRadius: t.r.md, border: `1px solid ${error ? t.red : t.sep}`, background: t.fill, padding: '10px 36px 10px 12px', fontSize: 11, color: t.label, outline: 'none', fontFamily: 'monospace', resize: 'vertical', lineHeight: 1.4 }} />
                <button onClick={() => setShow(!show)} style={{ position: 'absolute', right: 8, top: 10, background: 'none', border: 'none', cursor: 'pointer', padding: 4 }}>
                  {show ? <EyeOff size={15} color={t.gray} /> : <Eye size={15} color={t.gray} />}
                </button>
              </div>
            </div>
            {error && (
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '10px 12px', marginBottom: 16, background: `${t.red}12`, border: `1px solid ${t.red}30`, borderRadius: t.r.sm }}>
                <XCircle size={15} color={t.red} style={{ flexShrink: 0 }} />
                <p style={{ fontSize: 13, color: t.red, margin: 0 }}>{error}</p>
              </div>
            )}
            <button onClick={onSave} disabled={!token.trim() || isLoading}
              style={{ width: '100%', height: 40, borderRadius: t.r.md, border: 'none', background: `linear-gradient(135deg, ${t.blue}, ${t.indigo})`, color: '#fff', fontSize: 14, fontWeight: 600, cursor: (!token.trim() || isLoading) ? 'default' : 'pointer', opacity: (!token.trim() || isLoading) ? 0.5 : 1, display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 8 }}>
              {isLoading && <Loader2 size={15} style={{ animation: 'spin 1s linear infinite' }} />}
              {isLoading ? 'Validating…' : 'Save Token'}
            </button>
          </div>
        </motion.div>
      </motion.div>
    </AnimatePresence>
  )
}

// ─── Voice waveform bars ──────────────────────────────────────────────────────
function VoiceWaveform({ active }: { active: boolean }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 3, height: 18 }}>
      {[0.5, 0.8, 1, 0.7, 0.9, 0.6, 1].map((h, i) => (
        <motion.div key={i}
          animate={active ? { scaleY: [h, h * 1.8, h * 0.4, h] } : { scaleY: 0.25 }}
          transition={active ? { duration: 0.7, delay: i * 0.08, repeat: Infinity, ease: 'easeInOut' } : { duration: 0.2 }}
          style={{ width: 3, height: 16, borderRadius: 2, background: active ? t.red : t.dim, transformOrigin: 'center', opacity: active ? 1 : 0.4 }} />
      ))}
    </div>
  )
}

// ─── Thinking dots ────────────────────────────────────────────────────────────
function ThinkingDots() {
  return (
    <div style={{ display: 'flex', gap: 5, alignItems: 'center', padding: '4px 2px' }}>
      {[0, 1, 2].map(i => (
        <motion.div key={i}
          animate={{ scale: [1, 1.4, 1], opacity: [0.4, 1, 0.4] }}
          transition={{ duration: 1.2, delay: i * 0.2, repeat: Infinity, ease: 'easeInOut' }}
          style={{ width: 7, height: 7, borderRadius: '50%', background: t.blue }} />
      ))}
    </div>
  )
}

// ─── Main component ────────────────────────────────────────────────────────────
export function AIChatPage() {
  // ── Core state ────────────────────────────────────────────────────────────
  const [sessions, setSessions] = useState<LocalSession[]>([])
  const [currentSession, setCurrentSession] = useState<LocalSession | null>(null)
  const [messages, setMessages] = useState<Message[]>([])
  const [input, setInput] = useState('')
  const [isLoading, setIsLoading] = useState(false)
  const [isSessionLoading, setIsSessionLoading] = useState(false)
  const [isStreaming, setIsStreaming] = useState(false)
  const [connectionStatus, setConnectionStatus] = useState<'online' | 'offline'>('online')
  const [showTokenPrompt, setShowTokenPrompt] = useState(false)
  const [oidcToken, setOIDC ProviderToken] = useState('')
  const [tokenExpiry, setTokenExpiry] = useState('')
  const [models, setModels] = useState<AIModel[]>([])
  const [localModels, setLocalModels] = useState<{ id: string; name: string }[]>([])
  const [selectedModel, setSelectedModel] = useState<string>('')
  const [error, setError] = useState<string | null>(null)
  const [searchQuery, setSearchQuery] = useState('')
  const [showSearch, setShowSearch] = useState(false)
  const [isOnline, setIsOnline] = useState(navigator.onLine)
  const [retryCount, setRetryCount] = useState(0)
  const [charCount, setCharCount] = useState(0)
  const [draftSaved, setDraftSaved] = useState(false)
  const [tokenExpired, setTokenExpired] = useState(false)
  const [isLoadingModels, setIsLoadingModels] = useState(false)
  const [voiceState, setVoiceState] = useState<VoiceState>({ isListening: false, interim: '', final: '' })
  const [liveAlertCount, setLiveAlertCount] = useState<number | null>(null)
  const [copiedId, setCopiedId] = useState<string | null>(null)
  const [showScrollBtn, setShowScrollBtn] = useState(false)
  const [suggestions, setSuggestions] = useState<string[]>([])
  const [lastUserMsg, setLastUserMsg] = useState('')
  const [renamingSessionId, setRenamingSessionId] = useState<string | null>(null)
  const [renameValue, setRenameValue] = useState('')
  const [loadingCommandId, setLoadingCommandId] = useState<string | null>(null)
  const [thinkingMs, setThinkingMs] = useState(0)

  // ── Refs ──────────────────────────────────────────────────────────────────
  const messagesEndRef = useRef<HTMLDivElement>(null)
  const messagesScrollRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLTextAreaElement>(null)
  const renameInputRef = useRef<HTMLInputElement>(null)
  const draftTimeoutRef = useRef<NodeJS.Timeout>()
  const offlineCheckRef = useRef<NodeJS.Timeout>()
  const thinkingIntervalRef = useRef<NodeJS.Timeout>()
  const abortControllerRef = useRef<AbortController>()
  const speechRecRef = useRef<any>(null)
  const voiceStateRef = useRef(voiceState)
  const sendMessageRef = useRef<(text?: string) => Promise<void>>(() => Promise.resolve())

  useEffect(() => { voiceStateRef.current = voiceState }, [voiceState])

  const { settings } = useSettingsStore()
  const { theme } = useTheme()
  const { isSoundEnabled, volume } = useSoundSettings()
  const notificationSound = useSound('notification', { enabled: settings.soundEnabled && isSoundEnabled, volume: volume || 0.5 })
  const successSound = useSound('success', { enabled: settings.soundEnabled && isSoundEnabled, volume: volume || 0.5 })

  // ── Thinking timer ────────────────────────────────────────────────────────
  useEffect(() => {
    if (isLoading) {
      const start = Date.now()
      setThinkingMs(0)
      thinkingIntervalRef.current = setInterval(() => setThinkingMs(Date.now() - start), 100)
    } else {
      clearInterval(thinkingIntervalRef.current)
      setThinkingMs(0)
    }
    return () => clearInterval(thinkingIntervalRef.current)
  }, [isLoading])

  // ── Cached helpers ────────────────────────────────────────────────────────
  const getCachedModels = useCallback(() => {
    try {
      const cached = localStorage.getItem('ai_models_cache')
      if (!cached) return null
      const data = JSON.parse(cached)
      if (data.timestamp && Date.now() - data.timestamp < CONFIG.MODEL_CACHE_TTL) return data.models
      localStorage.removeItem('ai_models_cache')
    } catch {}
    return null
  }, [])

  const cacheModels = useCallback((m: AIModel[]) => {
    try { localStorage.setItem('ai_models_cache', JSON.stringify({ models: m, timestamp: Date.now() })) } catch {}
  }, [])

  const getCachedSessions = useCallback(() => {
    try {
      const key = `ai_sessions_cache_${getCurrentUserKey()}`
      const cached = localStorage.getItem(key)
      if (!cached) return null
      const data = JSON.parse(cached)
      if (data.timestamp && Date.now() - data.timestamp < CONFIG.SESSION_CACHE_TTL) return data.sessions
      localStorage.removeItem(key)
    } catch {}
    return null
  }, [])

  const cacheSessions = useCallback((s: LocalSession[]) => {
    try { localStorage.setItem(`ai_sessions_cache_${getCurrentUserKey()}`, JSON.stringify({ sessions: s, timestamp: Date.now() })) } catch {}
  }, [])

  const saveDraft = useCallback((text: string) => {
    if (draftTimeoutRef.current) clearTimeout(draftTimeoutRef.current)
    if (!text.trim()) { localStorage.removeItem(`ai_chat_draft_${getCurrentUserKey()}`); setDraftSaved(false); return }
    draftTimeoutRef.current = setTimeout(() => {
      localStorage.setItem(`ai_chat_draft_${getCurrentUserKey()}`, text); setDraftSaved(true)
      setTimeout(() => setDraftSaved(false), 2000)
    }, CONFIG.DRAFT_SAVE_DELAY)
  }, [])

  const clearDraft = useCallback(() => {
    localStorage.removeItem(`ai_chat_draft_${getCurrentUserKey()}`); setDraftSaved(false)
    if (draftTimeoutRef.current) clearTimeout(draftTimeoutRef.current)
  }, [])

  const restoreDraft = useCallback(() => {
    const draft = localStorage.getItem(`ai_chat_draft_${getCurrentUserKey()}`)
    if (draft && messages.length === 0) { setInput(draft); setCharCount(draft.length) }
  }, [messages.length])

  const checkOnlineStatus = useCallback(async () => {
    try {
      await fetch('/health', { method: 'HEAD', cache: 'no-cache' })
      if (!isOnline) { setIsOnline(true); setConnectionStatus('online') }
    } catch { if (isOnline) { setIsOnline(false); setConnectionStatus('offline') } }
  }, [isOnline])

  // ── Token management ──────────────────────────────────────────────────────
  const redirectToAuth = useCallback(() => {
    const last = sessionStorage.getItem('fg_last_reauth')
    if (last && Date.now() - parseInt(last) < 5 * 60 * 1000) { setTokenExpired(true); return }
    sessionStorage.setItem('fg_last_reauth', String(Date.now()))
    window.location.href = `/api/v1/auth/oidc?redirect=${encodeURIComponent(window.location.pathname)}`
  }, [])

  const silentRefreshOIDC ProviderToken = useCallback(async (): Promise<boolean> => {
    try {
      const appToken = sessionStorage.getItem('access_token') || localStorage.getItem('access_token')
      if (!appToken) return false
      const resp = await fetch('/api/v1/auth/oidc/oidc-refresh', { headers: { Authorization: `Bearer ${appToken}` } })
      if (!resp.ok) return false
      const data = await resp.json()
      if (data.success && data.data?.oidc_token) {
        const expiresIn = data.data.expires_in || 3300
        sessionStorage.setItem('oidc_token', data.data.oidc_token)
        sessionStorage.setItem('oidc_token_expiry', new Date(Date.now() + expiresIn * 1000 - 30000).toISOString())
        sessionStorage.setItem('oidc_token_source', 'auto-refresh')
        return true
      }
    } catch {}
    return false
  }, [])

  const checkOIDC ProviderToken = useCallback(async () => {
    const source = sessionStorage.getItem('oidc_token_source')
    if (source !== 'oidc-oauth2' && source !== 'auto-refresh') await silentRefreshOIDC ProviderToken()
    const token = sessionStorage.getItem('oidc_token') || sessionStorage.getItem('oauth_id_token')
    const expiry = sessionStorage.getItem('oidc_token_expiry')
    if (!token) {
      const ok = await silentRefreshOIDC ProviderToken()
      if (!ok) {
        // User has no OIDC Provider session — show manual token prompt instead of redirect-looping.
        setShowTokenPrompt(true)
      }
      return
    }
    if (expiry) {
      const expiryTime = new Date(expiry).getTime(); const now = Date.now()
      if (now >= expiryTime) {
        const ok = await silentRefreshOIDC ProviderToken()
        if (!ok) setTokenExpired(true); else { sessionStorage.removeItem('fg_last_reauth'); setTokenExpired(false) }
        return
      }
      if (now >= expiryTime - 5 * 60 * 1000) silentRefreshOIDC ProviderToken()
    }
    sessionStorage.removeItem('fg_last_reauth'); setTokenExpired(false)
  }, [silentRefreshOIDC ProviderToken, setShowTokenPrompt])

  // ── Loading ───────────────────────────────────────────────────────────────
  const loadLocalModels = useCallback(async () => {
    try {
      const resp = await fetch('/api/v1/ai/local/models'); if (!resp.ok) return
      const data = await resp.json()
      if (data.success && data.models) setLocalModels(data.models.map((m: any) => ({ id: m.id, name: m.name })))
    } catch {}
  }, [])

  const loadModelsOptimized = useCallback(async () => {
    try {
      setIsLoadingModels(true)
      const cached = getCachedModels()
      if (cached) {
        setModels(cached)
        if (!selectedModel && cached.length > 0) setSelectedModel(localStorage.getItem('selected_ai_model') || cached[0].id)
        setIsLoadingModels(false); return true
      }
      for (let attempt = 0; attempt < CONFIG.MAX_RETRIES; attempt++) {
        try {
          if (attempt > 0) { await new Promise(r => setTimeout(r, CONFIG.RETRY_DELAY * Math.pow(2, attempt - 1))); setRetryCount(attempt) }
          const response = await aiApi.listModels(); const data = response.data
          if (data?.success && data.models) {
            setModels(data.models); cacheModels(data.models)
            if (!selectedModel && data.models.length > 0) setSelectedModel(localStorage.getItem('selected_ai_model') || data.models[0].id)
            setConnectionStatus('online'); setTokenExpired(false); setIsLoadingModels(false); setRetryCount(0); return true
          }
          throw new Error('Invalid response format')
        } catch { if (attempt === CONFIG.MAX_RETRIES - 1) { setModels([]); setConnectionStatus('offline'); setIsLoadingModels(false); setRetryCount(0); return false } }
      }
      return false
    } catch { setModels([]); setConnectionStatus('offline'); setIsLoadingModels(false); setRetryCount(0); return false }
  }, [getCachedModels, cacheModels, selectedModel])

  const loadSessionsFromAPI = useCallback(async () => {
    try {
      const response = await aiApi.listSessions()
      if (response.data?.success && response.data.sessions) {
        const s: LocalSession[] = response.data.sessions.map((session: any) => ({
          id: session.id || `session-${Date.now()}-${Math.random()}`,
          title: session.title || 'New Conversation',
          messages: [], createdAt: session.created_at || new Date().toISOString(),
        }))
        cacheSessions(s); setSessions(s)
      }
    } catch { setSessions([]) }
  }, [cacheSessions])

  const loadSessionsOptimized = useCallback(async () => {
    try {
      const cached = getCachedSessions()
      if (cached) { setSessions(cached); loadSessionsFromAPI().catch(console.error); return }
      await loadSessionsFromAPI()
    } catch {}
  }, [getCachedSessions, loadSessionsFromAPI])

  // ── Session messages — uses isSessionLoading, NOT isLoading (so input stays enabled) ──
  const loadSessionMessages = useCallback(async (sessionId: string) => {
    try {
      setIsSessionLoading(true)
      const response = await aiApi.getSessionMessages(sessionId)
      if (response.data?.success && response.data.messages) {
        const raw = Array.isArray(response.data.messages) ? response.data.messages : []
        const msgs: Message[] = raw.map((msg: any, i: number) => ({
          id: typeof msg?.id === 'string' ? msg.id : `msg-${Date.now()}-${i}`,
          role: typeof msg?.role === 'string' ? msg.role : 'assistant',
          content: typeof msg?.content === 'string' ? msg.content : typeof msg?.message === 'string' ? msg.message : '',
          timestamp: typeof msg?.created_at === 'string' ? msg.created_at : new Date().toISOString(),
        })).filter((m: Message) => m.content.length > 0)
        setSessions(prev => prev.map(s => s.id === sessionId ? { ...s, messages: msgs } : s))
        setCurrentSession(prev => prev?.id === sessionId ? { ...prev, messages: msgs } : prev)
        setMessages(msgs)
      }
    } catch { setError('Failed to load conversation history') }
    finally { setIsSessionLoading(false) }
  }, [])

  // ── Initialization ────────────────────────────────────────────────────────
  useEffect(() => {
    let mounted = true
    const init = async () => {
      if (!mounted) return
      await checkOIDC ProviderToken()
      const onOnline = () => { if (!mounted) return; setIsOnline(true); checkOnlineStatus() }
      const onOffline = () => { if (!mounted) return; setIsOnline(false) }
      window.addEventListener('online', onOnline); window.addEventListener('offline', onOffline)
      offlineCheckRef.current = setInterval(() => { if (mounted) checkOnlineStatus() }, CONFIG.OFFLINE_CHECK_INTERVAL)
      try {
        await Promise.race([
          Promise.all([loadModelsOptimized(), loadSessionsOptimized(), loadLocalModels()]),
          new Promise((_, rej) => setTimeout(() => rej(new Error('timeout')), 10000)),
        ])
        if (mounted) restoreDraft()
      } catch {}
      try {
        const resp = await fetch('/api/v1/alerts?limit=1', { headers: { Authorization: `Bearer ${sessionStorage.getItem('access_token') || localStorage.getItem('access_token')}` } })
        const data = await resp.json()
        if (mounted && data?.total !== undefined) setLiveAlertCount(data.total)
      } catch {}
    }
    init()
    return () => {
      mounted = false
      if (offlineCheckRef.current) clearInterval(offlineCheckRef.current)
      if (thinkingIntervalRef.current) clearInterval(thinkingIntervalRef.current)
      if (abortControllerRef.current) abortControllerRef.current.abort()
      speechRecRef.current?.stop()
    }
  }, [])

  useEffect(() => { if (currentSession) setMessages(currentSession.messages || []) }, [currentSession])
  useEffect(() => {
    const el = messagesScrollRef.current
    if (!el) { messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' }); return }
    const dist = el.scrollHeight - el.scrollTop - el.clientHeight
    if (dist < CONFIG.SCROLL_THRESHOLD) messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages, isLoading, isStreaming])
  useEffect(() => { setCharCount(input.length); saveDraft(input) }, [input, saveDraft])

  // ── Keyboard shortcuts ────────────────────────────────────────────────────
  useKeyboard(settings.enableKeyboardShortcuts ? [
    { key: 'k', meta: true, callback: () => startNewChat() },
    { key: 'n', meta: true, callback: () => startNewChat() },
    { key: 'f', meta: true, callback: () => setShowSearch(s => !s) },
    { key: 'Enter', meta: true, callback: () => { if (input.trim()) sendMessageRef.current() } },
    { key: 'e', meta: true, callback: () => exportConversation() },
    { key: 'Escape', callback: () => { setRenamingSessionId(null); setShowSearch(false) } },
  ] : [])

  // ── Scroll tracking ───────────────────────────────────────────────────────
  const handleScroll = useCallback(() => {
    const el = messagesScrollRef.current; if (!el) return
    setShowScrollBtn(el.scrollHeight - el.scrollTop - el.clientHeight > CONFIG.SCROLL_THRESHOLD)
  }, [])

  const scrollToBottom = useCallback(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' }); setShowScrollBtn(false)
  }, [])

  // ── Quick suggestions ─────────────────────────────────────────────────────
  const buildSuggestions = useCallback((content: string): string[] => {
    const lc = content.toLowerCase()
    if (lc.match(/alert|severity|pager|critical|firing/))
      return ['What actions should I take for critical alerts?', 'Show me alert trends for the past 24h', 'Which alerts can be safely suppressed?']
    if (lc.match(/incident|outage|downtime|impact/))
      return ['What is the root cause of this incident?', 'Who should be notified about this?', 'Show me similar past incidents']
    if (lc.match(/kubernetes|pod|deploy|namespace|k8s|cluster/))
      return ['List all unhealthy pods', 'Which namespaces use the most memory?', 'Are there any failed deployments?']
    if (lc.match(/workflow|automation|trigger|runbook/))
      return ['Show all active workflows', 'Which automations ran in the last hour?', 'How do I create a new workflow?']
    if (lc.match(/topology|service|dependency|node|infrastructure/))
      return ['Show the infrastructure topology graph', 'Which services have the most dependencies?', 'Are there any single points of failure?']
    if (lc.match(/performance|latency|cpu|memory|capacity/))
      return ['Which services have high latency right now?', 'Show memory usage across all hosts', 'Predict resource exhaustion timeline']
    return ['Tell me more about this', 'What should I do next?', 'Show me related data']
  }, [])

  // ── Stop generation ───────────────────────────────────────────────────────
  const stopGeneration = useCallback(() => {
    abortControllerRef.current?.abort()
    setIsLoading(false); setIsStreaming(false)
  }, [])

  // ── Actions ───────────────────────────────────────────────────────────────
  const saveOIDC ProviderToken = async () => {
    if (!oidcToken.trim()) { setError('Please enter a valid token'); return }
    setIsLoading(true); setError(null)
    try {
      const expiryTime = tokenExpiry || new Date(Date.now() + 50 * 60 * 1000).toISOString()
      sessionStorage.setItem('oidc_token', oidcToken)
      sessionStorage.setItem('oidc_token_expiry', expiryTime)
      sessionStorage.setItem('oidc_token_source', 'manual')
      localStorage.removeItem('ai_models_cache')
      const ok = await loadModelsOptimized()
      if (ok) { setShowTokenPrompt(false); setTokenExpired(false) }
      else setError('Token saved but failed to load models.')
    } catch { setError('Token validation failed.') }
    finally { setIsLoading(false) }
  }

  const startNewChat = useCallback(() => {
    const s: LocalSession = { id: `session-${Date.now()}`, title: 'New Conversation', messages: [], createdAt: new Date().toISOString() }
    setSessions(prev => [s, ...prev]); setCurrentSession(s); setMessages([])
    setError(null); setSuggestions([])
    setTimeout(() => inputRef.current?.focus(), 100)
  }, [])

  // Optimistic session switch — show cached content immediately, refresh in background
  const handleSessionClick = useCallback((session: LocalSession) => {
    setCurrentSession(session); setMessages(session.messages || [])
    setSuggestions([])
    if (!session.id.startsWith('session-')) loadSessionMessages(session.id)
  }, [loadSessionMessages])

  const deleteSession = useCallback(async (sessionId: string, e: React.MouseEvent) => {
    e.stopPropagation()
    try {
      if (!sessionId.startsWith('session-')) await aiApi.deleteSession(sessionId)
      setSessions(prev => prev.filter(s => s.id !== sessionId))
      if (currentSession?.id === sessionId) { setCurrentSession(null); setMessages([]) }
    } catch { setError('Failed to delete session') }
  }, [currentSession])

  const startRename = useCallback((session: LocalSession, e: React.MouseEvent) => {
    e.stopPropagation(); setRenamingSessionId(session.id); setRenameValue(session.title)
    setTimeout(() => renameInputRef.current?.focus(), 50)
  }, [])

  const commitRename = useCallback((sessionId: string) => {
    if (!renameValue.trim()) { setRenamingSessionId(null); return }
    const newTitle = renameValue.trim()
    setSessions(prev => prev.map(s => s.id === sessionId ? { ...s, title: newTitle } : s))
    setCurrentSession(prev => prev?.id === sessionId ? { ...prev, title: newTitle } : prev)
    setRenamingSessionId(null)
  }, [renameValue])

  const exportConversation = useCallback(() => {
    const text = messages.map(m => `**${m.role === 'user' ? 'You' : 'AI'}** (${new Date(m.timestamp).toLocaleString()})\n${m.content}\n\n`).join('')
    const blob = new Blob([text], { type: 'text/markdown' })
    const url = URL.createObjectURL(blob); const a = document.createElement('a')
    a.href = url; a.download = `chat-${currentSession?.title || 'conversation'}.md`
    document.body.appendChild(a); a.click(); document.body.removeChild(a); URL.revokeObjectURL(url)
  }, [messages, currentSession])

  const copyMessage = useCallback((id: string, content: string) => {
    navigator.clipboard.writeText(content); setCopiedId(id); setTimeout(() => setCopiedId(null), 2000)
  }, [])

  // ── Regenerate last response ──────────────────────────────────────────────
  const regenerateLast = useCallback(() => {
    if (!lastUserMsg || isLoading || isStreaming) return
    setMessages(prev => {
      const idx = [...prev].reverse().findIndex(m => m.role === 'assistant')
      return idx === -1 ? prev : prev.slice(0, prev.length - 1 - idx)
    })
    setSuggestions([])
    sendMessageRef.current(lastUserMsg)
  }, [lastUserMsg, isLoading, isStreaming])

  // ── Voice input ───────────────────────────────────────────────────────────
  const toggleVoice = useCallback(() => {
    const SR = (window as any).SpeechRecognition || (window as any).webkitSpeechRecognition
    if (!SR) { setError('Voice recognition not supported in this browser. Use Chrome or Edge.'); return }

    if (voiceState.isListening) {
      speechRecRef.current?.stop(); setVoiceState({ isListening: false, interim: '', final: '' }); return
    }

    const rec = new SR()
    rec.continuous = true; rec.interimResults = true; rec.lang = 'en-US'
    speechRecRef.current = rec

    rec.onstart = () => setVoiceState(p => ({ ...p, isListening: true }))

    rec.onresult = (ev: any) => {
      let interimText = ''; let finalDelta = ''
      for (let i = ev.resultIndex; i < ev.results.length; i++) {
        const transcript = ev.results[i][0].transcript
        if (ev.results[i].isFinal) finalDelta += transcript
        else interimText += transcript
      }
      setVoiceState(p => {
        const newFinal = p.final + finalDelta
        if (finalDelta) setInput(newFinal.trim())
        return { ...p, interim: interimText, final: newFinal }
      })
    }

    rec.onerror = (ev: any) => {
      if (ev.error !== 'no-speech') setError(`Voice error: ${ev.error}`)
      setVoiceState({ isListening: false, interim: '', final: '' })
    }

    rec.onend = () => setVoiceState(p => ({ ...p, isListening: false, interim: '' }))
    rec.start()
  }, [voiceState.isListening])

  // ── Smart commands (sendMessageRef fixes stale closure) ───────────────────
  const handleSmartCommand = useCallback(async (command: string) => {
    setLoadingCommandId(command)
    try {
      setError(null)
      let prompt = ''

      // Helper: safely extract an array from any response shape
      const arr = (val: any, ...keys: string[]): any[] => {
        if (Array.isArray(val)) return val
        for (const k of [...keys, 'items', 'results', 'records', 'data']) {
          if (Array.isArray(val?.[k])) return val[k]
        }
        return []
      }

      // Unwrap double-envelope: { success, data: { key: [...] } } → inner object
      const unwrap = (r: any) => r?.data?.data ?? r?.data ?? r

      switch (command) {
        case '/analyze-alerts': {
          // Backend injects live alerts+stats via enhanceMessagesWithContext
          prompt = 'Analyze all currently active alerts in the system. Provide: (1) severity breakdown and counts, (2) top alert sources, (3) any patterns or recurring themes, (4) immediate action priorities ranked by risk. Use the live alert data you have access to.'
          break
        }
        case '/alert-fatigue': {
          try {
            const r = await aiApi.analyzeFatigue({ hours: 24 })
            const analysis = unwrap(r)
            prompt = `Perform a thorough alert fatigue analysis using both the engine results below and the live alert/incident data you have access to.\n\nFatigue engine output (24h window):\n- Fatigue detected: ${analysis?.fatigue_detected ?? 'unknown'}\n- Fatigue score: ${analysis?.fatigue_score ?? 0}\n- Noisy alerts: ${analysis?.noise_alerts ?? 0}\n- Duplicate alerts: ${analysis?.duplicate_alerts ?? 0}\n- Flapping alerts: ${analysis?.flapping_alerts ?? 0}\n- Affected sources: ${JSON.stringify(analysis?.affected_sources ?? [])}\n- Recommendations: ${JSON.stringify(analysis?.recommendations ?? [])}\n\nBased on this AND the live alert data, identify the top noisiest alerts, which should be suppressed, which thresholds need tuning, and give specific actionable recommendations.`
          } catch {
            prompt = 'Analyze alert fatigue in our system over the last 24 hours. Identify which alerts are too noisy, suggest which ones to suppress or tune, and recommend threshold changes. Use the live alert data you have access to.'
          }
          break
        }
        case '/predict-incidents': {
          try {
            const r = await aiApi.getAlertClusters({ hours: 24 })
            const d = unwrap(r)
            const clusters = arr(d, 'clusters')
            if (clusters.length > 0) {
              prompt = `Based on these ${clusters.length} alert correlation cluster(s) from the last 24 hours, predict which ones are likely to escalate into incidents:\n${JSON.stringify(clusters.slice(0, 10), null, 2)}\n\nFor each risk, provide: predicted impact, likelihood (%), recommended preventive action, and urgency level.`
            } else {
              prompt = 'Based on the current active alerts and system state, predict which alerts or combinations are most likely to escalate into incidents. Provide risk assessment, predicted impact, and recommended preventive actions.'
            }
          } catch {
            prompt = 'Analyze the current alerts and predict which are most likely to escalate into incidents. Provide risk scores, predicted impact, and preventive recommendations.'
          }
          break
        }
        case '/incidents-summary': {
          // Backend injects live incident data
          prompt = 'Give me a complete triage summary of all open incidents. For each, provide: severity, status, how long it has been open, and recommended next action. Then give overall incident health metrics and any patterns you see across incidents.'
          break
        }
        case '/infrastructure-status': {
          // Backend injects topology overview
          prompt = 'Analyze the infrastructure topology. Identify: (1) any unhealthy or degraded components, (2) potential bottlenecks or single points of failure, (3) components with the most dependencies, (4) any anomalies in the topology. Use the live infrastructure data you have access to.'
          break
        }
        case '/kubernetes-health': {
          // Backend injects k8s clusters
          prompt = 'Analyze all Kubernetes clusters. For each cluster provide: sync status, node/pod counts, any concerns. Highlight: clusters with sync failures, abnormally high pod counts, version mismatches, and overall recommendations for cluster health improvement.'
          break
        }
        case '/system-overview': {
          // Backend injects alerts + incidents + k8s + topology all at once
          prompt = 'Give me a complete SRE system health report covering all aspects: (1) Alert summary — active count by severity, top sources; (2) Incident status — open incidents by priority; (3) Kubernetes clusters — health status; (4) Infrastructure topology — entity counts, any unhealthy nodes; (5) Overall risk assessment and top 3 action items. Use all the live system data you have access to.'
          break
        }
        case '/analytics-overview': {
          try {
            const [dashRes, analyticsRes] = await Promise.all([
              analyticsApi.getDashboardMetrics().catch(() => null),
              analyticsApi.getAlertAnalytics({ time_range: '24h' }).catch(() => null),
            ])
            const dash = dashRes ? unwrap(dashRes) : null
            const analytics = analyticsRes ? unwrap(analyticsRes) : null
            if (dash || analytics) {
              prompt = `Analyze these AlertHub operational metrics and provide insights, trends, and recommendations:\n\nDashboard metrics:\n${JSON.stringify(dash, null, 2)}\n\nAlert analytics (24h):\n${JSON.stringify(analytics, null, 2)}\n\nFocus on: MTTR trends, alert volume trends, top alert sources, any anomalies, and specific improvement suggestions.`
            } else {
              prompt = 'Provide an analytics overview based on the current alert and incident data. Show trends, key metrics, MTTR estimates, and operational insights.'
            }
          } catch {
            prompt = 'Provide an analytics overview of the current system state — alert volume, incident trends, key operational metrics, and recommendations for improvement.'
          }
          break
        }
        case '/correlation-analysis': {
          try {
            const r = await correlationApi.listCorrelationClusters()
            const d = unwrap(r)
            const clusters = arr(d, 'clusters', 'correlation_clusters')
            if (clusters.length > 0) {
              prompt = `Analyze these ${clusters.length} alert correlation cluster(s) and suggest improvements to reduce noise and improve signal quality:\n${JSON.stringify(clusters.slice(0, 10), null, 2)}\n\nFor each cluster: explain the correlation pattern, assess if alerts should be grouped, and suggest correlation rule improvements.`
            } else {
              prompt = 'No active correlation clusters found. Based on the current active alerts, suggest which alerts should be correlated or grouped together to reduce noise and improve incident detection accuracy.'
            }
          } catch {
            prompt = 'Analyze alert correlation patterns. Based on the current active alerts, identify which ones are related, suggest grouping strategies, and recommend correlation rules to reduce noise.'
          }
          break
        }
        case '/rca-suggest': {
          // Backend injects open incidents
          prompt = 'Perform root cause analysis for all currently open incidents. For each incident: (1) identify the most likely root cause based on the alert data, (2) suggest the top 3 investigation steps, (3) recommend a mitigation action, (4) estimate time to resolve. Then provide any common root causes across incidents.'
          break
        }
        case '/workflow-status': {
          try {
            const r = await workflowApi.listWorkflows()
            const d = unwrap(r)
            const workflows = arr(d, 'workflows')
            if (workflows.length > 0) {
              const summary = workflows.slice(0, 8).map((w: any) => ({
                name: w.name, enabled: w.enabled,
                executions: w.executions ?? w.execution_count ?? 0,
                last_run: w.last_run, description: w.description,
              }))
              prompt = `Review these ${workflows.length} automated workflows and suggest optimizations:\n${JSON.stringify(summary, null, 2)}\n\nFor each: assess effectiveness, identify any that should be enabled/disabled, suggest trigger condition improvements, and recommend new workflows that would benefit our operations based on current alert/incident patterns.`
            } else {
              prompt = 'No workflows configured yet. Based on our current alert patterns and incident data, suggest 5 automated workflows that would significantly improve our SRE operations — include trigger conditions, actions, and expected impact.'
            }
          } catch {
            prompt = 'Review our automated workflow setup and suggest optimizations to improve alert response times and reduce manual toil.'
          }
          break
        }
        case '/execute-workflow': {
          try {
            const r = await workflowApi.listWorkflows({ enabled: true })
            const d = unwrap(r)
            const workflows = arr(d, 'workflows')
            const options = workflows.slice(0, 8).map((w: any) => ({ id: w.id, name: w.name, description: w.description }))
            if (options.length > 0) {
              prompt = `The following workflows are available to execute. Based on the current system state (alerts and incidents), which one(s) should we trigger and why?\n${JSON.stringify(options, null, 2)}\n\nProvide a recommendation with reasoning based on current system conditions.`
            } else {
              prompt = 'There are no enabled workflows available. Based on current alert and incident data, what immediate automated action would you recommend, and what workflow should we create to handle it?'
            }
          } catch {
            prompt = 'What automated workflow should we execute right now based on the current system state? Provide your recommendation with reasoning.'
          }
          break
        }
        case '/provider-health': {
          try {
            const r = await providersApi.listProviders()
            const rawData = r?.data
            const providers = arr(rawData, 'providers', 'alert_sources', 'sources')
            if (providers.length > 0) {
              const summary = providers.slice(0, 8).map((p: any) => ({
                name: p.name ?? p.type, type: p.type, enabled: p.enabled,
                status: p.status ?? p.health_status ?? 'unknown',
                last_event: p.last_event_at ?? p.updated_at,
              }))
              prompt = `Assess the health of these ${providers.length} monitoring provider(s):\n${JSON.stringify(summary, null, 2)}\n\nFor each: evaluate if it is healthy, identify any gaps in coverage, and suggest improvements. Also highlight any providers that are disabled or not sending data.`
            } else {
              prompt = 'Assess our monitoring provider setup. Based on the current alert sources and incoming alerts, identify: any coverage gaps, redundant monitors, providers that might be misconfigured, and recommendations for improving our monitoring coverage.'
            }
          } catch {
            prompt = 'Assess the health of our monitoring providers and alert sources. Identify any coverage gaps or issues using the available system data.'
          }
          break
        }
        default: prompt = command
      }

      if (prompt) await sendMessageRef.current(prompt)
    } catch (err: any) { setError(err.message || 'Command failed') }
    finally { setLoadingCommandId(null) }
  }, [])

  // ── Send message ──────────────────────────────────────────────────────────
  const sendMessage = useCallback(async (messageText?: string) => {
    const text = messageText || input.trim()
    if (!text || isLoading || isStreaming) return
    if (text.length > CONFIG.MAX_MESSAGE_LENGTH) { setError(`Message too long (max ${CONFIG.MAX_MESSAGE_LENGTH} chars)`); return }

    const isLocalModel = (selectedModel || '').startsWith('local:')
    if (!isLocalModel) {
      const ft = sessionStorage.getItem('oidc_token') || sessionStorage.getItem('oauth_id_token')
      const fe = sessionStorage.getItem('oidc_token_expiry')
      if (!ft) { const ok = await silentRefreshOIDC ProviderToken(); if (!ok) { setShowTokenPrompt(true); return } }
      else if (fe && Date.now() >= new Date(fe).getTime()) {
        const ok = await silentRefreshOIDC ProviderToken(); if (!ok) { setTokenExpired(true); return }
      }
    }

    if (settings.soundEnabled && isSoundEnabled) notificationSound.play()
    setLastUserMsg(text)

    const userMsg: Message = { id: `msg-${Date.now()}`, role: 'user', content: text, timestamp: new Date().toISOString() }
    let activeSession = currentSession
    if (!activeSession) {
      activeSession = { id: `session-${Date.now()}`, title: 'New Conversation', messages: [], createdAt: new Date().toISOString() }
      setSessions(prev => [activeSession!, ...prev]); setCurrentSession(activeSession)
    }

    const updatedMsgs = [...messages, userMsg]
    setMessages(updatedMsgs); setInput(''); setIsLoading(true); setError(null); setSuggestions([]); clearDraft()
    abortControllerRef.current = new AbortController()

    for (let attempt = 0; attempt < CONFIG.MAX_RETRIES; attempt++) {
      try {
        if (attempt > 0) { await new Promise(r => setTimeout(r, CONFIG.RETRY_DELAY * Math.pow(2, attempt - 1))); setRetryCount(attempt) }

        let fullContent = ''

        if (selectedModel.startsWith('local:')) {
          const r = await fetch('/api/v1/ai/local/chat', {
            method: 'POST', headers: { 'Content-Type': 'application/json' },
            signal: abortControllerRef.current.signal,
            body: JSON.stringify({ model: selectedModel, messages: updatedMsgs.map(m => ({ role: m.role, content: m.content })) }),
          })
          const d = await r.json()
          if (d?.success && d.response) fullContent = d.response.message || ''
          else throw new Error(d?.error || 'Local model error')
        } else {
          const payload = {
            model: selectedModel || 'alerthub-intelligence',
            messages: updatedMsgs.map(m => ({ role: m.role, content: m.content })),
            session_id: activeSession.id.startsWith('session-') ? undefined : activeSession.id,
            max_tokens: 4096,
          }
          const response = await aiApi.chat(payload); const data = response.data
          if (data?.success && data.response) fullContent = data.response.message || data.response.content || data.response
          else throw new Error(data?.error || 'Failed to get AI response')
        }

        // ── Streaming reveal (paragraph by paragraph) ─────────────────────
        setIsLoading(false); setIsStreaming(true); setRetryCount(0)
        const aiMsgId = `msg-${Date.now()}`
        const aiMsg: Message = { id: aiMsgId, role: 'assistant', content: '', timestamp: new Date().toISOString() }
        setMessages([...updatedMsgs, aiMsg])

        const paragraphs = fullContent.split(/\n\n+/).filter(Boolean)
        let streamedContent = ''
        let aborted = false

        for (let pi = 0; pi < paragraphs.length; pi++) {
          if (abortControllerRef.current?.signal.aborted) { aborted = true; break }
          streamedContent += (pi > 0 ? '\n\n' : '') + paragraphs[pi]
          setMessages(prev => prev.map(m => m.id === aiMsgId ? { ...m, content: streamedContent } : m))
          if (pi < paragraphs.length - 1) await new Promise<void>(r => setTimeout(r, CONFIG.STREAM_PARA_DELAY))
        }

        setIsStreaming(false)
        const finalAiMsg: Message = { id: aiMsgId, role: 'assistant', content: streamedContent, timestamp: new Date().toISOString() }
        const completeMsgs = [...updatedMsgs, finalAiMsg]
        const final = { ...activeSession!, messages: completeMsgs }
        if (updatedMsgs.length === 1) {
          final.title = userMsg.content.trim().split(' ').slice(0, 7).join(' ') + (userMsg.content.trim().split(' ').length > 7 ? '…' : '')
        }
        setSessions(prev => prev.map(s => s.id === activeSession!.id ? final : s))
        setCurrentSession(final)
        if (!aborted) {
          localStorage.removeItem(`ai_sessions_cache_${getCurrentUserKey()}`)
          if (settings.soundEnabled && isSoundEnabled) successSound.play()
          setConnectionStatus('online')
          setSuggestions(buildSuggestions(streamedContent))
        }
        return
      } catch (err: any) {
        if (err?.name === 'AbortError' || abortControllerRef.current?.signal.aborted) {
          setIsLoading(false); setIsStreaming(false); setRetryCount(0); return
        }
        if (attempt === CONFIG.MAX_RETRIES - 1) {
          setError(err.message || 'AI response failed'); setConnectionStatus('offline')
          setIsLoading(false); setIsStreaming(false); setRetryCount(0)
        }
      }
    }
  }, [input, isLoading, isStreaming, messages, selectedModel, currentSession, settings.soundEnabled, isSoundEnabled, notificationSound, successSound, clearDraft, silentRefreshOIDC ProviderToken, redirectToAuth, buildSuggestions])

  // Keep sendMessageRef current (fixes stale closure in handleSmartCommand + regenerateLast)
  useEffect(() => { sendMessageRef.current = sendMessage }, [sendMessage])

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      const text = input.trim()
      if (text.startsWith('/')) { handleSmartCommand(text); setInput('') } else sendMessage()
    }
  }

  // ── Derived ───────────────────────────────────────────────────────────────
  const filteredSessions = searchQuery
    ? sessions.filter(s => s.title.toLowerCase().includes(searchQuery.toLowerCase()) || s.messages.some(m => m.content.toLowerCase().includes(searchQuery.toLowerCase())))
    : sessions
  const hasToken = !!(sessionStorage.getItem('oidc_token') || sessionStorage.getItem('oauth_id_token'))

  const groupedSessions = useCallback(() => {
    const today = new Date(); today.setHours(0, 0, 0, 0)
    const yesterday = new Date(today); yesterday.setDate(yesterday.getDate() - 1)
    const lastWeek = new Date(today); lastWeek.setDate(lastWeek.getDate() - 7)
    const groups: { label: string; items: LocalSession[] }[] = [
      { label: 'Today', items: [] }, { label: 'Yesterday', items: [] },
      { label: 'Past 7 Days', items: [] }, { label: 'Older', items: [] },
    ]
    filteredSessions.forEach(s => {
      const d = new Date(s.createdAt)
      if (d >= today) groups[0].items.push(s)
      else if (d >= yesterday) groups[1].items.push(s)
      else if (d >= lastWeek) groups[2].items.push(s)
      else groups[3].items.push(s)
    })
    return groups.filter(g => g.items.length > 0)
  }, [filteredSessions])

  // ─── Render ────────────────────────────────────────────────────────────────
  return (
    <div style={{ height: 'calc(100vh - 64px)', display: 'flex', overflow: 'hidden', background: t.bg }}>

      {/* ── Sidebar ── */}
      <div style={{ width: 268, display: 'flex', flexDirection: 'column', height: '100%', borderRight: `1px solid ${t.sep}`, background: t.card, flexShrink: 0 }}>
        <div style={{ padding: '14px 12px 10px', borderBottom: `1px solid ${t.sep}` }}>
          {/* Brand */}
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 12 }}>
            <div style={{ width: 34, height: 34, borderRadius: t.r.md, background: `linear-gradient(135deg, ${t.purple} 0%, ${t.blue} 100%)`, display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0, boxShadow: `0 4px 12px ${t.purple}40` }}>
              <Bot size={18} color="#fff" />
            </div>
            <div>
              <p style={{ fontSize: 14, fontWeight: 700, color: t.label, margin: 0, lineHeight: 1.2 }}>AI Assistant</p>
              <p style={{ fontSize: 11, color: t.dim, margin: 0 }}>
                {isLoadingModels ? 'Loading models…' : models.length > 0 ? `${models.length + localModels.length} models` : 'No models'}
              </p>
            </div>
          </div>

          <motion.button whileHover={{ scale: 1.01 }} whileTap={{ scale: 0.98 }} onClick={startNewChat}
            style={{ width: '100%', padding: '9px 14px', borderRadius: t.r.md, border: 'none', background: `linear-gradient(135deg, ${t.blue}, ${t.indigo})`, color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 7, marginBottom: 8, boxShadow: `0 4px 14px ${t.blue}35` }}>
            <Plus size={15} /> New Chat
          </motion.button>

          <div style={{ display: 'flex', gap: 6 }}>
            {[
              { icon: Search, active: showSearch, onClick: () => setShowSearch(s => !s), title: 'Search', color: t.blue },
              { icon: Download, active: false, onClick: exportConversation, title: 'Export', color: t.green, disabled: messages.length === 0 },
              { icon: Key, active: false, onClick: () => setShowTokenPrompt(true), title: 'Token', color: hasToken ? t.green : t.orange },
            ].map(({ icon: Icon, active, onClick, title, color, disabled }) => (
              <button key={title} onClick={onClick} title={title} disabled={disabled}
                style={{ flex: 1, padding: '7px', borderRadius: t.r.sm, border: `1px solid ${active ? color : t.sep}`, background: active ? `${color}15` : t.fill, color: disabled ? t.dim : color, cursor: disabled ? 'default' : 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center', opacity: disabled ? 0.4 : 1, transition: 'all 0.15s' }}>
                <Icon size={14} />
              </button>
            ))}
          </div>

          <AnimatePresence>
            {showSearch && (
              <motion.div initial={{ height: 0, opacity: 0 }} animate={{ height: 'auto', opacity: 1 }} exit={{ height: 0, opacity: 0 }} style={{ overflow: 'hidden', marginTop: 8 }}>
                <div style={{ position: 'relative' }}>
                  <Search size={13} style={{ position: 'absolute', left: 9, top: '50%', transform: 'translateY(-50%)', color: t.dim }} />
                  <input type="text" placeholder="Search conversations…" value={searchQuery} onChange={e => setSearchQuery(e.target.value)} autoFocus
                    style={{ width: '100%', height: 32, borderRadius: t.r.sm, border: `1px solid ${t.sep}`, background: t.fill, paddingLeft: 28, paddingRight: 8, fontSize: 12, color: t.label, outline: 'none' }} />
                </div>
              </motion.div>
            )}
          </AnimatePresence>
        </div>

        {/* Sessions list */}
        <div style={{ flex: 1, overflowY: 'auto', padding: '8px 6px', minHeight: 0 }}>
          {groupedSessions().length === 0 ? (
            <div style={{ padding: '32px 16px', textAlign: 'center' }}>
              <MessageCircle size={28} color={t.dim} style={{ marginBottom: 8 }} />
              <p style={{ fontSize: 13, color: t.dim, margin: 0 }}>No conversations yet</p>
            </div>
          ) : (
            groupedSessions().map(group => (
              <div key={group.label} style={{ marginBottom: 16 }}>
                <p style={{ fontSize: 10, fontWeight: 700, color: t.dim, textTransform: 'uppercase', letterSpacing: '0.08em', padding: '0 8px 4px', margin: 0 }}>{group.label}</p>
                {group.items.map(session => (
                  <motion.div key={session.id} initial={{ opacity: 0, x: -6 }} animate={{ opacity: 1, x: 0 }}
                    onClick={() => handleSessionClick(session)}
                    style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '7px 8px', borderRadius: t.r.sm, cursor: 'pointer', background: currentSession?.id === session.id ? `${t.blue}15` : 'transparent', border: `1px solid ${currentSession?.id === session.id ? t.blue + '40' : 'transparent'}`, marginBottom: 2, transition: 'all 0.15s', position: 'relative' }}
                    onMouseEnter={e => { if (currentSession?.id !== session.id) e.currentTarget.style.background = t.fill; Array.from(e.currentTarget.querySelectorAll<HTMLButtonElement>('.session-action')).forEach(b => b.style.opacity = '1') }}
                    onMouseLeave={e => { if (currentSession?.id !== session.id) e.currentTarget.style.background = 'transparent'; Array.from(e.currentTarget.querySelectorAll<HTMLButtonElement>('.session-action')).forEach(b => b.style.opacity = '0') }}>
                    <MessageSquare size={13} color={currentSession?.id === session.id ? t.blue : t.dim} style={{ flexShrink: 0 }} />

                    {/* Inline rename editor */}
                    {renamingSessionId === session.id ? (
                      <input ref={renameInputRef} value={renameValue} onChange={e => setRenameValue(e.target.value)}
                        onBlur={() => commitRename(session.id)}
                        onKeyDown={e => { if (e.key === 'Enter') commitRename(session.id); if (e.key === 'Escape') setRenamingSessionId(null); e.stopPropagation() }}
                        onClick={e => e.stopPropagation()}
                        style={{ flex: 1, fontSize: 12, fontWeight: 600, color: t.label, background: t.fill, border: `1px solid ${t.blue}60`, borderRadius: t.r.xs, padding: '1px 6px', outline: 'none', fontFamily: 'inherit' }} />
                    ) : (
                      <span onDoubleClick={e => startRename(session, e)}
                        style={{ flex: 1, fontSize: 12, fontWeight: currentSession?.id === session.id ? 600 : 400, color: currentSession?.id === session.id ? t.blue : t.label, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                        {session.title}
                      </span>
                    )}

                    {session.messages.length > 0 && (
                      <span style={{ fontSize: 10, color: t.dim, flexShrink: 0 }}>{session.messages.length}</span>
                    )}

                    <button className="session-action" onClick={e => startRename(session, e)}
                      style={{ width: 18, height: 18, borderRadius: 3, background: 'transparent', border: 'none', color: t.dim, cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center', opacity: 0, transition: 'all 0.15s', flexShrink: 0 }}
                      onMouseEnter={e => { (e.currentTarget as HTMLButtonElement).style.color = t.blue }}
                      onMouseLeave={e => { (e.currentTarget as HTMLButtonElement).style.color = t.dim }}>
                      <Pencil size={10} />
                    </button>
                    <button className="session-action" onClick={e => deleteSession(session.id, e)}
                      style={{ width: 18, height: 18, borderRadius: 3, background: 'transparent', border: 'none', color: t.dim, cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center', opacity: 0, transition: 'all 0.15s', flexShrink: 0 }}
                      onMouseEnter={e => { (e.currentTarget as HTMLButtonElement).style.color = t.red }}
                      onMouseLeave={e => { (e.currentTarget as HTMLButtonElement).style.color = t.dim }}>
                      <Trash2 size={10} />
                    </button>
                  </motion.div>
                ))}
              </div>
            ))
          )}
        </div>

        {/* Model selector */}
        <div style={{ padding: '10px 12px', borderTop: `1px solid ${t.sep}` }}>
          <label style={{ fontSize: 10, fontWeight: 600, color: t.dim, textTransform: 'uppercase', letterSpacing: '0.08em', display: 'block', marginBottom: 5 }}>Model</label>
          <div style={{ position: 'relative' }}>
            <Cpu size={13} style={{ position: 'absolute', left: 9, top: '50%', transform: 'translateY(-50%)', color: selectedModel.startsWith('local:') ? t.purple : t.blue, zIndex: 1 }} />
            <select value={selectedModel} onChange={e => { setSelectedModel(e.target.value); localStorage.setItem('selected_ai_model', e.target.value) }}
              disabled={models.length === 0 && localModels.length === 0}
              style={{ width: '100%', height: 34, borderRadius: t.r.sm, border: `1px solid ${selectedModel.startsWith('local:') ? t.purple + '60' : t.sep}`, background: selectedModel.startsWith('local:') ? `${t.purple}10` : t.fill, color: selectedModel.startsWith('local:') ? t.purple : t.label, fontSize: 12, paddingLeft: 28, paddingRight: 8, outline: 'none', appearance: 'none', cursor: 'pointer' }}>
              {models.length === 0 && localModels.length === 0 && <option value="">No models available</option>}
              {models.length > 0 && (
                <optgroup label="☁️ OIDC Provider">
                  {models.map(m => <option key={m.id} value={m.id}>{m.name}</option>)}
                </optgroup>
              )}
              {localModels.length > 0 && (
                <optgroup label="🖥️ Local (Ollama)">
                  {localModels.map(m => <option key={m.id} value={m.id}>{m.name}</option>)}
                </optgroup>
              )}
            </select>
          </div>
        </div>
      </div>

      {/* ── Main chat area ── */}
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', height: '100%', overflow: 'hidden', minWidth: 0, position: 'relative' }}>

        {/* Header */}
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '12px 20px', borderBottom: `1px solid ${t.sep}`, background: t.card, flexShrink: 0 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            {currentSession ? (
              <>
                <div style={{ width: 8, height: 8, borderRadius: '50%', background: isOnline && connectionStatus === 'online' ? t.green : t.red, boxShadow: isOnline ? `0 0 0 3px ${t.green}30` : 'none' }} />
                <h2 style={{ fontSize: 15, fontWeight: 600, color: t.label, margin: 0, maxWidth: 360, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {currentSession.title}
                </h2>
                {messages.length > 0 && <span style={{ fontSize: 11, color: t.dim, background: t.fill, padding: '2px 8px', borderRadius: 20 }}>{messages.length} messages</span>}
                {isSessionLoading && (
                  <div style={{ display: 'flex', alignItems: 'center', gap: 5, fontSize: 11, color: t.dim }}>
                    <Loader2 size={11} style={{ animation: 'spin 1s linear infinite' }} /> Refreshing…
                  </div>
                )}
              </>
            ) : (
              <>
                <div style={{ width: 8, height: 8, borderRadius: '50%', background: isOnline ? t.green : t.red }} />
                <h2 style={{ fontSize: 15, fontWeight: 600, color: t.label, margin: 0 }}>AI Assistant</h2>
              </>
            )}
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            {/* Thinking timer */}
            {(isLoading || isStreaming) && (
              <div style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 11, color: t.blue, background: `${t.blue}10`, padding: '4px 10px', borderRadius: 20, fontVariantNumeric: 'tabular-nums' }}>
                <Loader2 size={11} style={{ animation: 'spin 1s linear infinite' }} />
                {isLoading ? `Thinking ${(thinkingMs / 1000).toFixed(1)}s` : 'Responding…'}
              </div>
            )}
            {retryCount > 0 && (
              <div style={{ display: 'flex', alignItems: 'center', gap: 4, fontSize: 11, color: t.orange, background: `${t.orange}12`, padding: '4px 10px', borderRadius: 20 }}>
                Retry {retryCount}/{CONFIG.MAX_RETRIES}
              </div>
            )}
            {tokenExpired && (
              <button onClick={() => redirectToAuth()}
                style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '5px 12px', borderRadius: 20, border: `1px solid ${t.red}50`, background: `${t.red}10`, color: t.red, cursor: 'pointer', fontSize: 12, fontWeight: 500 }}>
                <AlertTriangle size={12} /> Re-authenticate
              </button>
            )}
            {!tokenExpired && hasToken && (
              <div style={{ display: 'flex', alignItems: 'center', gap: 5, padding: '5px 12px', borderRadius: 20, border: `1px solid ${t.green}40`, background: `${t.green}10`, color: t.green, fontSize: 12, fontWeight: 500 }}>
                <CheckCircle size={12} /> Token Active
              </div>
            )}
            {messages.length > 0 && (
              <button onClick={exportConversation} title="Export conversation"
                style={{ width: 32, height: 32, borderRadius: t.r.sm, border: `1px solid ${t.sep}`, background: t.fill, color: t.dim, cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                <Download size={14} />
              </button>
            )}
            {!isOnline && <span style={{ fontSize: 11, color: t.red, background: `${t.red}12`, padding: '4px 10px', borderRadius: 20, fontWeight: 500 }}>Offline</span>}
          </div>
        </div>

        {/* Messages / Empty state */}
        <div ref={messagesScrollRef} onScroll={handleScroll}
          style={{ flex: 1, overflowY: 'auto', padding: '20px 24px', minHeight: 0 }}>
          {messages.length === 0 ? (
            <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', minHeight: '100%', paddingBottom: 24 }}>
              <div style={{ textAlign: 'center', marginBottom: 32, paddingTop: 28 }}>
                <motion.div initial={{ scale: 0.8, opacity: 0 }} animate={{ scale: 1, opacity: 1 }} transition={{ type: 'spring', damping: 16 }}
                  style={{ width: 80, height: 80, borderRadius: t.r.xl, background: `linear-gradient(135deg, ${t.purple} 0%, ${t.blue} 100%)`, display: 'flex', alignItems: 'center', justifyContent: 'center', margin: '0 auto 18px', boxShadow: `0 16px 48px ${t.purple}35` }}>
                  <Sparkles size={36} color="#fff" />
                </motion.div>
                <motion.h2 initial={{ y: 10, opacity: 0 }} animate={{ y: 0, opacity: 1 }} transition={{ delay: 0.1 }}
                  style={{ fontSize: 24, fontWeight: 700, color: t.label, margin: '0 0 8px' }}>
                  How can I help you?
                </motion.h2>
                <motion.p initial={{ y: 10, opacity: 0 }} animate={{ y: 0, opacity: 1 }} transition={{ delay: 0.15 }}
                  style={{ fontSize: 14, color: t.sub, maxWidth: 500, lineHeight: 1.6, margin: '0 auto' }}>
                  Ask me anything about alerts, incidents, Kubernetes, infrastructure topology, capacity, or SRE practices. I have live access to your environment.
                </motion.p>
              </div>

              {/* Live status bar */}
              <motion.div initial={{ y: 10, opacity: 0 }} animate={{ y: 0, opacity: 1 }} transition={{ delay: 0.18 }}
                style={{ display: 'flex', gap: 8, marginBottom: 32, flexWrap: 'wrap', justifyContent: 'center' }}>
                {[
                  { icon: Activity, label: isOnline ? 'Backend Online' : 'Offline', color: isOnline ? t.green : t.red },
                  { icon: AlertTriangle, label: liveAlertCount !== null ? `${liveAlertCount} Alerts` : 'Alerts', color: liveAlertCount && liveAlertCount > 0 ? t.orange : t.green },
                  { icon: Brain, label: models.length > 0 ? `${models.length} AI Models` : 'Loading…', color: models.length > 0 ? t.blue : t.gray },
                  { icon: Shield, label: hasToken ? 'Authenticated' : 'No Token', color: hasToken ? t.green : t.orange },
                ].map(({ icon: Icon, label, color }) => (
                  <div key={label} style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '5px 12px', borderRadius: 20, border: `1px solid ${color}30`, background: `${color}08`, fontSize: 12, color, fontWeight: 500 }}>
                    <Icon size={12} />{label}
                  </div>
                ))}
              </motion.div>

              {/* Command groups */}
              <div style={{ width: '100%', maxWidth: 780 }}>
                {COMMANDS.map((group, gi) => (
                  <motion.div key={group.group} initial={{ y: 14, opacity: 0 }} animate={{ y: 0, opacity: 1 }} transition={{ delay: 0.2 + gi * 0.05 }} style={{ marginBottom: 20 }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 10 }}>
                      <div style={{ width: 22, height: 22, borderRadius: t.r.xs, background: `${group.color}18`, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                        <group.icon size={12} color={group.color} />
                      </div>
                      <span style={{ fontSize: 11, fontWeight: 700, color: t.dim, textTransform: 'uppercase', letterSpacing: '0.08em' }}>{group.group}</span>
                    </div>
                    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(200px, 1fr))', gap: 8 }}>
                      {group.items.map(item => (
                        <motion.button key={item.id} whileHover={{ y: -2, boxShadow: `0 8px 24px ${group.color}20` }} whileTap={{ scale: 0.97 }}
                          onClick={() => handleSmartCommand(item.id)}
                          disabled={loadingCommandId !== null}
                          style={{ padding: '12px 14px', borderRadius: t.r.md, border: `1px solid ${loadingCommandId === item.id ? group.color + '60' : t.sep}`, background: loadingCommandId === item.id ? `${group.color}08` : t.card, color: t.label, cursor: loadingCommandId !== null ? 'default' : 'pointer', textAlign: 'left', transition: 'all 0.2s', opacity: loadingCommandId !== null && loadingCommandId !== item.id ? 0.5 : 1 }}>
                          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
                            <div style={{ width: 20, height: 20, borderRadius: t.r.xs, background: `${group.color}15`, display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 }}>
                              {loadingCommandId === item.id
                                ? <Loader2 size={11} color={group.color} style={{ animation: 'spin 1s linear infinite' }} />
                                : <ChevronRight size={11} color={group.color} />}
                            </div>
                            <span style={{ fontSize: 12, fontWeight: 600, color: loadingCommandId === item.id ? group.color : t.label }}>{item.label}</span>
                          </div>
                          <p style={{ fontSize: 11, color: t.dim, margin: 0, lineHeight: 1.4 }}>{item.desc}</p>
                        </motion.button>
                      ))}
                    </div>
                  </motion.div>
                ))}
              </div>

              {settings.enableKeyboardShortcuts && (
                <motion.div initial={{ opacity: 0 }} animate={{ opacity: 1 }} transition={{ delay: 0.5 }}
                  style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 11, color: t.dim, marginTop: 8 }}>
                  <Keyboard size={12} />
                  <span>⌘K new chat · ⌘↵ send · ⌘F search · ⌘E export</span>
                </motion.div>
              )}
            </div>
          ) : (
            <div style={{ maxWidth: 800, margin: '0 auto', width: '100%' }}>
              {messages.map((msg, idx) => (
                <motion.div key={msg.id} initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.2 }}
                  style={{ marginBottom: 20 }}>
                  {msg.role === 'user' ? (
                    <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 10 }}>
                      <div style={{ maxWidth: '76%' }}>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 6, justifyContent: 'flex-end', marginBottom: 5 }}>
                          <span style={{ fontSize: 11, color: t.dim }}>{new Date(msg.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</span>
                          <span style={{ fontSize: 12, fontWeight: 600, color: t.label }}>You</span>
                        </div>
                        <div style={{ background: `linear-gradient(135deg, ${t.blue}, ${t.indigo})`, borderRadius: `${t.r.lg}px ${t.r.lg}px ${t.r.sm}px ${t.r.lg}px`, padding: '11px 16px', color: '#fff', fontSize: 14, lineHeight: 1.55, wordBreak: 'break-word', boxShadow: `0 4px 16px ${t.blue}30` }}>
                          <div style={{ whiteSpace: 'pre-wrap' }}>{msg.content}</div>
                        </div>
                        <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 4 }}>
                          <button onClick={() => copyMessage(msg.id, msg.content)}
                            style={{ padding: '2px 8px', borderRadius: t.r.xs, border: 'none', background: 'transparent', color: t.dim, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 3, fontSize: 11 }}>
                            {copiedId === msg.id ? <><CheckCircle size={11} color={t.green} /> Copied</> : <><Copy size={11} /> Copy</>}
                          </button>
                        </div>
                      </div>
                      <div style={{ width: 32, height: 32, borderRadius: t.r.sm, background: `linear-gradient(135deg, ${t.blue}, ${t.indigo})`, display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0, boxShadow: `0 2px 8px ${t.blue}30` }}>
                        <User size={15} color="#fff" />
                      </div>
                    </div>
                  ) : (
                    <div style={{ display: 'flex', gap: 10, alignItems: 'flex-start' }}>
                      <div style={{ width: 32, height: 32, borderRadius: t.r.sm, background: `linear-gradient(135deg, ${t.purple}, ${t.blue})`, display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0, marginTop: 2, boxShadow: `0 2px 8px ${t.purple}30` }}>
                        <Sparkles size={15} color="#fff" />
                      </div>
                      <div style={{ flex: 1, minWidth: 0 }}>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 5 }}>
                          <span style={{ fontSize: 12, fontWeight: 600, color: t.label }}>AI Assistant</span>
                          <span style={{ fontSize: 10, color: t.dim, background: `${t.purple}12`, padding: '1px 7px', borderRadius: 10, fontWeight: 500 }}>
                            {selectedModel ? (models.find(m => m.id === selectedModel)?.name || selectedModel.replace('local:', '')) : 'AI'}
                          </span>
                          {idx === messages.length - 1 && isStreaming && (
                            <span style={{ fontSize: 10, color: t.blue, background: `${t.blue}12`, padding: '1px 7px', borderRadius: 10, fontWeight: 500 }}>
                              Responding…
                            </span>
                          )}
                          <span style={{ fontSize: 11, color: t.dim }}>{new Date(msg.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</span>
                        </div>
                        <div style={{ background: t.card, borderRadius: `${t.r.sm}px ${t.r.lg}px ${t.r.lg}px ${t.r.lg}px`, border: `1px solid ${t.sep}`, padding: '12px 16px', fontSize: 14, lineHeight: 1.6, color: t.label, boxShadow: '0 2px 12px rgba(0,0,0,0.05)' }}>
                          <MarkdownRenderer content={msg.content} />
                          {idx === messages.length - 1 && isStreaming && (
                            <motion.span animate={{ opacity: [1, 0] }} transition={{ duration: 0.5, repeat: Infinity, repeatType: 'reverse', ease: 'easeInOut' }}
                              style={{ display: 'inline-block', width: 2, height: '1em', background: t.blue, marginLeft: 2, verticalAlign: 'text-bottom', borderRadius: 1 }} />
                          )}
                        </div>
                        <div style={{ display: 'flex', gap: 4, marginTop: 6, flexWrap: 'wrap' }}>
                          <button onClick={() => copyMessage(msg.id, msg.content)}
                            style={{ padding: '3px 9px', borderRadius: t.r.xs, border: `1px solid ${t.sep}`, background: 'transparent', color: copiedId === msg.id ? t.green : t.dim, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 4, fontSize: 11, transition: 'all 0.15s' }}>
                            {copiedId === msg.id ? <><CheckCircle size={11} /> Copied</> : <><Copy size={11} /> Copy</>}
                          </button>
                          {idx === messages.length - 1 && msg.role === 'assistant' && !isLoading && !isStreaming && (
                            <button onClick={regenerateLast}
                              style={{ padding: '3px 9px', borderRadius: t.r.xs, border: `1px solid ${t.sep}`, background: 'transparent', color: t.dim, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 4, fontSize: 11, transition: 'all 0.15s' }}
                              onMouseEnter={e => { (e.currentTarget as HTMLButtonElement).style.color = t.orange }}
                              onMouseLeave={e => { (e.currentTarget as HTMLButtonElement).style.color = t.dim }}>
                              <RotateCcw size={11} /> Regenerate
                            </button>
                          )}
                        </div>
                      </div>
                    </div>
                  )}
                </motion.div>
              ))}

              {/* Quick follow-up suggestions */}
              <AnimatePresence>
                {!isLoading && !isStreaming && suggestions.length > 0 && (
                  <motion.div initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0 }}
                    style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginBottom: 16, marginLeft: 42 }}>
                    {suggestions.map(s => (
                      <motion.button key={s} whileHover={{ y: -1, boxShadow: `0 4px 12px ${t.blue}20` }} whileTap={{ scale: 0.97 }}
                        onClick={() => { setSuggestions([]); sendMessageRef.current(s) }}
                        style={{ padding: '6px 14px', borderRadius: 20, border: `1px solid ${t.blue}35`, background: `${t.blue}06`, color: t.blue, fontSize: 12, cursor: 'pointer', transition: 'all 0.2s', fontWeight: 500 }}>
                        {s}
                      </motion.button>
                    ))}
                  </motion.div>
                )}
              </AnimatePresence>

              {/* Thinking indicator */}
              <AnimatePresence>
                {isLoading && (
                  <motion.div initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0, y: -4 }}
                    style={{ display: 'flex', gap: 10, alignItems: 'flex-start', marginBottom: 20 }}>
                    <div style={{ width: 32, height: 32, borderRadius: t.r.sm, background: `linear-gradient(135deg, ${t.purple}, ${t.blue})`, display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 }}>
                      <Sparkles size={15} color="#fff" />
                    </div>
                    <div style={{ background: t.card, borderRadius: `${t.r.sm}px ${t.r.lg}px ${t.r.lg}px ${t.r.lg}px`, border: `1px solid ${t.sep}`, padding: '12px 16px', boxShadow: '0 2px 12px rgba(0,0,0,0.05)' }}>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                        <ThinkingDots />
                        {thinkingMs > 0 && (
                          <span style={{ fontSize: 11, color: t.dim, fontVariantNumeric: 'tabular-nums' }}>
                            {(thinkingMs / 1000).toFixed(1)}s
                          </span>
                        )}
                      </div>
                    </div>
                  </motion.div>
                )}
              </AnimatePresence>

              <div ref={messagesEndRef} />
            </div>
          )}
        </div>

        {/* Floating scroll-to-bottom */}
        <AnimatePresence>
          {showScrollBtn && messages.length > 0 && (
            <motion.button initial={{ opacity: 0, scale: 0.8 }} animate={{ opacity: 1, scale: 1 }} exit={{ opacity: 0, scale: 0.8 }}
              onClick={scrollToBottom}
              style={{ position: 'absolute', bottom: 90, left: '50%', transform: 'translateX(-50%)', zIndex: 10, padding: '6px 16px', borderRadius: 20, border: `1px solid ${t.sep}`, background: t.card, color: t.label, boxShadow: '0 4px 20px rgba(0,0,0,0.14)', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, fontWeight: 500 }}>
              <ChevronDown size={14} /> Scroll to bottom
            </motion.button>
          )}
        </AnimatePresence>

        {/* Error banner */}
        <AnimatePresence>
          {error && (
            <motion.div initial={{ height: 0, opacity: 0 }} animate={{ height: 'auto', opacity: 1 }} exit={{ height: 0, opacity: 0 }}
              style={{ background: `${t.red}10`, borderTop: `1px solid ${t.red}30`, padding: '8px 20px', display: 'flex', alignItems: 'center', justifyContent: 'space-between', flexShrink: 0 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: t.red }}>
                <AlertTriangle size={14} />{error}
              </div>
              <button onClick={() => setError(null)} style={{ background: 'none', border: 'none', cursor: 'pointer', color: t.red, padding: 4 }}>
                <X size={14} />
              </button>
            </motion.div>
          )}
        </AnimatePresence>

        {/* ── Input area ── */}
        <div style={{ padding: '12px 20px 16px', borderTop: `1px solid ${t.sep}`, background: t.card, flexShrink: 0 }}>
          <div style={{ maxWidth: 800, margin: '0 auto' }}>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
              {/* Voice transcript overlay */}
              <AnimatePresence>
                {voiceState.isListening && (
                  <motion.div initial={{ height: 0, opacity: 0 }} animate={{ height: 'auto', opacity: 1 }} exit={{ height: 0, opacity: 0 }}
                    style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '6px 12px', background: `${t.red}08`, border: `1px solid ${t.red}30`, borderRadius: t.r.sm, overflow: 'hidden' }}>
                    <VoiceWaveform active={true} />
                    <span style={{ fontSize: 12, color: t.red, fontStyle: voiceState.interim ? 'italic' : 'normal', flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {voiceState.interim ? `"${voiceState.interim}"` : 'Listening…'}
                    </span>
                    <span style={{ fontSize: 11, color: t.red, fontWeight: 500 }}>Tap mic to stop</span>
                  </motion.div>
                )}
              </AnimatePresence>

              <div style={{ display: 'flex', gap: 10, alignItems: 'flex-end', background: t.bg, borderRadius: t.r.lg, border: `1.5px solid ${charCount > CONFIG.MAX_MESSAGE_LENGTH * 0.9 ? t.red : t.sep}`, padding: '10px 12px', transition: 'border-color 0.2s', boxShadow: '0 2px 16px rgba(0,0,0,0.04)' }}>
                {/* Attachment tools */}
                <div style={{ display: 'flex', gap: 4, alignItems: 'flex-end', flexShrink: 0 }}>
                  <label title="Upload image" style={{ cursor: 'pointer' }}>
                    <input type="file" accept="image/*" multiple style={{ display: 'none' }}
                      onChange={e => Array.from(e.target.files || []).forEach(file => {
                        const fr = new FileReader(); fr.onload = ev => setInput(p => p + `\n[Image: ${file.name}]\n${ev.target?.result}\n`); fr.readAsDataURL(file)
                      })} />
                    <div style={{ width: 30, height: 30, borderRadius: t.r.sm, background: t.fill, display: 'flex', alignItems: 'center', justifyContent: 'center', cursor: 'pointer' }}
                      onMouseEnter={e => (e.currentTarget as HTMLDivElement).style.background = `${t.blue}20`}
                      onMouseLeave={e => (e.currentTarget as HTMLDivElement).style.background = t.fill}>
                      <Camera size={14} color={t.blue} />
                    </div>
                  </label>
                  <label title="Upload file" style={{ cursor: 'pointer' }}>
                    <input type="file" multiple style={{ display: 'none' }}
                      onChange={e => Array.from(e.target.files || []).forEach(file => {
                        if (file.type.startsWith('text/') || /\.(log|yaml|json|md|sh|py|go|ts|tsx|js)$/.test(file.name)) {
                          const fr = new FileReader(); fr.onload = ev => setInput(p => p + `\n[File: ${file.name}]\n\`\`\`\n${ev.target?.result}\n\`\`\`\n`); fr.readAsText(file)
                        } else setInput(p => p + `\n[Attached: ${file.name} (${(file.size / 1024).toFixed(1)}KB)]\n`)
                      })} />
                    <div style={{ width: 30, height: 30, borderRadius: t.r.sm, background: t.fill, display: 'flex', alignItems: 'center', justifyContent: 'center', cursor: 'pointer' }}
                      onMouseEnter={e => (e.currentTarget as HTMLDivElement).style.background = `${t.purple}20`}
                      onMouseLeave={e => (e.currentTarget as HTMLDivElement).style.background = t.fill}>
                      <Paperclip size={14} color={t.purple} />
                    </div>
                  </label>
                  {/* Voice button */}
                  <button title={voiceState.isListening ? 'Stop recording' : 'Voice input'} onClick={toggleVoice}
                    style={{ width: 30, height: 30, borderRadius: t.r.sm, border: 'none', background: voiceState.isListening ? `${t.red}18` : t.fill, display: 'flex', alignItems: 'center', justifyContent: 'center', cursor: 'pointer', transition: 'all 0.2s' }}>
                    {voiceState.isListening ? <VoiceWaveform active={true} /> : <Mic size={14} color={t.green} />}
                  </button>
                </div>

                {/* Textarea */}
                <textarea ref={inputRef} value={input} onChange={e => setInput(e.target.value)} onKeyDown={handleKeyDown}
                  placeholder={tokenExpired ? 'Session expired — click Re-authenticate above' : voiceState.isListening ? 'Listening… speak your question' : 'Ask anything about your infrastructure… or type / for commands'}
                  maxLength={CONFIG.MAX_MESSAGE_LENGTH}
                  disabled={tokenExpired}
                  rows={1}
                  style={{ flex: 1, border: 'none', background: 'transparent', fontSize: 14, color: t.label, outline: 'none', resize: 'none', fontFamily: 'inherit', lineHeight: 1.5, minHeight: 22, maxHeight: 160, overflow: 'auto', paddingTop: 4 }}
                  onInput={e => { const el = e.target as HTMLTextAreaElement; el.style.height = 'auto'; el.style.height = Math.min(el.scrollHeight, 160) + 'px' }} />

                {/* Right controls */}
                <div style={{ display: 'flex', alignItems: 'flex-end', gap: 6, flexShrink: 0 }}>
                  {draftSaved && (
                    <div style={{ display: 'flex', alignItems: 'center', gap: 3, fontSize: 10, color: t.green }}>
                      <CheckCircle size={9} /> Saved
                    </div>
                  )}
                  <span style={{ fontSize: 10, color: charCount > CONFIG.MAX_MESSAGE_LENGTH * 0.9 ? t.red : t.dim, minWidth: 30, textAlign: 'right' }}>
                    {charCount > 0 ? charCount : ''}
                  </span>

                  {/* Stop or Send button */}
                  {isLoading || isStreaming ? (
                    <motion.button whileHover={{ scale: 1.05 }} whileTap={{ scale: 0.93 }}
                      onClick={stopGeneration}
                      style={{ width: 34, height: 34, borderRadius: t.r.md, border: `1px solid ${t.red}40`, background: `${t.red}12`, color: t.red, cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0, transition: 'all 0.2s' }}
                      title="Stop generation">
                      <StopCircle size={16} />
                    </motion.button>
                  ) : (
                    <motion.button whileHover={{ scale: 1.05 }} whileTap={{ scale: 0.93 }}
                      onClick={() => { const text = input.trim(); if (text.startsWith('/')) { handleSmartCommand(text); setInput('') } else sendMessage() }}
                      disabled={!input.trim() || tokenExpired || charCount > CONFIG.MAX_MESSAGE_LENGTH}
                      style={{ width: 34, height: 34, borderRadius: t.r.md, border: 'none', background: (input.trim() && !tokenExpired) ? `linear-gradient(135deg, ${t.blue}, ${t.indigo})` : t.fill, color: (input.trim() && !tokenExpired) ? '#fff' : t.dim, cursor: (input.trim() && !tokenExpired) ? 'pointer' : 'default', display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0, transition: 'all 0.2s', boxShadow: (input.trim() && !tokenExpired) ? `0 4px 14px ${t.blue}40` : 'none' }}>
                      <Send size={15} />
                    </motion.button>
                  )}
                </div>
              </div>

              {settings.enableKeyboardShortcuts && (
                <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', fontSize: 11, color: t.dim, padding: '0 2px' }}>
                  <span>Type <code style={{ background: t.fill, padding: '1px 5px', borderRadius: 4, fontSize: 10 }}>/incidents-summary</code> · <code style={{ background: t.fill, padding: '1px 5px', borderRadius: 4, fontSize: 10 }}>/kubernetes-health</code> for live data</span>
                  <span style={{ display: 'flex', alignItems: 'center', gap: 4 }}><Keyboard size={11} /> ⌘K new · ⌘↵ send</span>
                </div>
              )}
            </div>
          </div>
        </div>
      </div>

      {/* Token modal (developer access) */}
      <TokenModal isOpen={showTokenPrompt} onClose={() => setShowTokenPrompt(false)} onSave={saveOIDC ProviderToken}
        token={oidcToken} setToken={setOIDC ProviderToken} expiry={tokenExpiry} setExpiry={setTokenExpiry}
        error={error} isLoading={isLoading} />

      <style>{`
        @keyframes spin { from { transform: rotate(0deg) } to { transform: rotate(360deg) } }
      `}</style>
    </div>
  )
}
