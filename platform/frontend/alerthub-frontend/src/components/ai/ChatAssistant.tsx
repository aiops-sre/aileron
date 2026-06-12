import { useState, useEffect, useCallback, useRef } from 'react'
import { motion, AnimatePresence } from 'framer-motion'
import { Send, X, Bot, Sparkles, AlertCircle, RefreshCw, Copy, Download, Lightbulb, Check, Minimize2, Maximize2, Minus, Wifi, WifiOff, RotateCcw } from 'lucide-react'
import toast from 'react-hot-toast'
import apple from '@/lib/apple-tokens'
import { useKeyboard, SHORTCUTS } from '../../hooks/useKeyboard'
import { useSound, useSoundSettings } from '../../hooks/useSound'
import { useSettingsStore } from '../../stores/settingsStore'
import { useTheme } from '../../hooks/useTheme'
import { aiService } from '../../services/AIService'

// Returns a stable per-user key for localStorage cache scoping.
// Falls back to decoding the JWT when sessionStorage.user is absent
// (e.g. fresh tab where only localStorage has the access token).
const getChatUserKey = (): string => {
  try {
    const u = sessionStorage.getItem('user')
    if (u) { const p = JSON.parse(u); const k = p.id || p.email; if (k) return k }
  } catch {}
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

interface Message {
  id: string
  role: 'user' | 'assistant'
  content: string
  timestamp: Date
}

interface AIChatAssistantProps {
  isOpen: boolean
  onClose: () => void
}

interface AIModel {
  id: string
  name: string
  provider?: string
}

interface ChatSession {
  id: string
  title: string
  lastMessage?: string
  timestamp: Date
}

// Enhanced configuration constants
const CONFIG = {
  MAX_RETRIES: 3,
  RETRY_DELAY: 1000,
  MODEL_CACHE_TTL: 3600000, // 1 hour
  SESSION_CACHE_TTL: 300000, // 5 minutes
  DRAFT_SAVE_DELAY: 1000,
  MAX_MESSAGE_LENGTH: 10000,
  OFFLINE_CHECK_INTERVAL: 30000,
  API_TIMEOUT: 60000, // 60 seconds for AI responses
}

const SUGGESTED_PROMPTS = [
  { icon: '🔍', text: 'Analyze recent critical alerts', category: 'analysis' },
  { icon: '🛠️', text: 'How do I troubleshoot pod crashes?', category: 'troubleshoot' },
  { icon: '📊', text: 'Explain MTTR metrics on my dashboard', category: 'metrics' },
  { icon: '⚡', text: 'Best practices for alert management', category: 'tips' },
  { icon: '🔄', text: 'How to set up automation rules?', category: 'automation' },
  { icon: '👥', text: 'Help with team and user management', category: 'admin' },
]

export default function AIChatAssistant({ isOpen, onClose }: AIChatAssistantProps) {
  // Enhanced state management
  const [messages, setMessages] = useState<Message[]>([])
  const [input, setInput] = useState('')
  const [isTyping, setIsTyping] = useState(false)
  const [models, setModels] = useState<AIModel[]>([])
  const [selectedModel, setSelectedModel] = useState(() => localStorage.getItem('selected_ai_model') || '')
  const [sessionId, setSessionId] = useState<string | null>(null)
  const [sessions, setSessions] = useState<ChatSession[]>([])
  const [isLoadingModels, setIsLoadingModels] = useState(false)
  const [isLoadingSessions, setIsLoadingSessions] = useState(false)
  const [tokenExpired, setTokenExpired] = useState(false)
  const [copiedMessageId, setCopiedMessageId] = useState<string | null>(null)
  const [showTokenInput, setShowTokenInput] = useState(false)
  const [tokenInput, setTokenInput] = useState('')
  const [isMinimized, setIsMinimized] = useState(false)
  const [isMaximized, setIsMaximized] = useState(false)
  
  // Enhanced features state
  const [isOnline, setIsOnline] = useState(navigator.onLine)
  const [retryCount, setRetryCount] = useState(0)
  const [charCount, setCharCount] = useState(0)
  const [draftSaved, setDraftSaved] = useState(false)
  const [showSessions, setShowSessions] = useState(false)
  
  // Refs for cleanup and management
  const draftTimeoutRef = useRef<NodeJS.Timeout>()
  const offlineCheckRef = useRef<NodeJS.Timeout>()
  const abortControllerRef = useRef<AbortController>()

  // Settings integration
  const { settings } = useSettingsStore()
  
  // Theme integration
  const { theme, resolvedTheme, toggleTheme } = useTheme()
  
  // Sound hooks
  const { isSoundEnabled, volume } = useSoundSettings()
  const notificationSound = useSound('notification', {
    enabled: settings.soundEnabled && isSoundEnabled,
    volume: volume || 0.5
  })
  const successSound = useSound('success', {
    enabled: settings.soundEnabled && isSoundEnabled,
    volume: volume || 0.5
  })

  // Enhanced utility functions
  const getCachedModels = useCallback(() => {
    try {
      const cached = localStorage.getItem('ai_models_cache')
      if (!cached) return null
      
      const data = JSON.parse(cached)
      const now = Date.now()
      
      if (data.timestamp && (now - data.timestamp < CONFIG.MODEL_CACHE_TTL)) {
        return data.models
      }
      
      localStorage.removeItem('ai_models_cache')
      return null
    } catch (error) {
      console.error('Cache read error:', error)
      return null
    }
  }, [])

  const cacheModels = useCallback((models: AIModel[]) => {
    try {
      const data = {
        models,
        timestamp: Date.now(),
      }
      localStorage.setItem('ai_models_cache', JSON.stringify(data))
    } catch (error) {
      console.error('Cache write error:', error)
    }
  }, [])

  const getCachedSessions = useCallback(() => {
    try {
      const userKey = getChatUserKey()
      const cached = localStorage.getItem(`ai_sessions_cache_${userKey}`)
      if (!cached) return null

      const data = JSON.parse(cached)
      const now = Date.now()

      if (data.timestamp && (now - data.timestamp < CONFIG.SESSION_CACHE_TTL)) {
        return data.sessions
      }

      localStorage.removeItem(`ai_sessions_cache_${getChatUserKey()}`)
      return null
    } catch (error) {
      console.error('Session cache read error:', error)
      return null
    }
  }, [])

  const cacheSessions = useCallback((sessions: ChatSession[]) => {
    try {
      const userKey = getChatUserKey()
      const data = {
        sessions,
        timestamp: Date.now(),
      }
      localStorage.setItem(`ai_sessions_cache_${userKey}`, JSON.stringify(data))
    } catch (error) {
      console.error('Session cache write error:', error)
    }
  }, [])

  // Draft management
  const saveDraft = useCallback((text: string) => {
    if (draftTimeoutRef.current) {
      clearTimeout(draftTimeoutRef.current)
    }
    const draftKey = `ai_chat_draft_${getChatUserKey()}`
    if (!text.trim()) {
      localStorage.removeItem(draftKey)
      setDraftSaved(false)
      return
    }

    draftTimeoutRef.current = setTimeout(() => {
      localStorage.setItem(draftKey, text)
      setDraftSaved(true)
      setTimeout(() => setDraftSaved(false), 2000)
    }, CONFIG.DRAFT_SAVE_DELAY)
  }, [])

  const restoreDraft = useCallback(() => {
    const draftKey = `ai_chat_draft_${getChatUserKey()}`
    const draft = localStorage.getItem(draftKey)
    if (draft && messages.length === 0) {
      setInput(draft)
      setCharCount(draft.length)
    }
  }, [messages.length])

  const clearDraft = useCallback(() => {
    localStorage.removeItem('ai_chat_draft')
    setDraftSaved(false)
    if (draftTimeoutRef.current) {
      clearTimeout(draftTimeoutRef.current)
    }
  }, [])

  // Network resilience
  const fetchWithRetry = useCallback(async (url: string, options: RequestInit & { timeout?: number } = {}) => {
    const { timeout = CONFIG.API_TIMEOUT, ...fetchOptions } = options
    
    if (abortControllerRef.current) {
      abortControllerRef.current.abort()
    }
    
    abortControllerRef.current = new AbortController()
    const timeoutId = setTimeout(() => abortControllerRef.current?.abort(), timeout)
    
    try {
      const response = await fetch(url, {
        ...fetchOptions,
        signal: abortControllerRef.current.signal,
      })
      
      clearTimeout(timeoutId)
      
      if (!response.ok) {
        throw new Error(`HTTP ${response.status}: ${response.statusText}`)
      }
      
      return await response.json()
    } catch (error: any) {
      clearTimeout(timeoutId)
      if (error.name === 'AbortError') {
        throw new Error('Request timeout')
      }
      throw error
    }
  }, [])

  // Session management
  const startNewChat = useCallback(() => {
    setSessionId(null)
    setMessages([
      {
        id: '1',
        role: 'assistant',
        content: 'New chat started. How can I help you today?',
        timestamp: new Date(),
      },
    ])
    clearDraft()
    toast.success('Started new chat')
  }, [clearDraft])

  const loadSessions = useCallback(async () => {
    try {
      setIsLoadingSessions(true)

      // Check cache first
      const cached = getCachedSessions()
      if (cached) {
        setSessions(cached)
        console.log('✅ Sessions loaded from cache')
        return
      }

      // Delegate to AIService (correct /api/v1/ai/sessions path, consistent auth headers)
      const sessionList = await aiService.getSessions()
      // Map AIService ChatSession shape to local ChatSession shape
      const mapped: ChatSession[] = sessionList.map(s => ({
        id: s.id,
        title: s.title,
        lastMessage: undefined,
        timestamp: new Date(s.updated_at || s.created_at),
      }))
      setSessions(mapped)
      cacheSessions(mapped)
    } catch (error) {
      console.error('Failed to load sessions:', error)
    } finally {
      setIsLoadingSessions(false)
    }
  }, [getCachedSessions, cacheSessions])

  // Offline detection
  const checkOnlineStatus = useCallback(async () => {
    try {
      await fetch('/health', { method: 'HEAD', cache: 'no-cache' })
      if (!isOnline) {
        setIsOnline(true)
        toast.success('Connection restored')
      }
    } catch (error) {
      if (isOnline) {
        setIsOnline(false)
        toast.error('You are offline')
      }
    }
  }, [isOnline])

  // Enhanced keyboard shortcuts - only active when chat is open and keyboard shortcuts are enabled
  useKeyboard(
    isOpen && settings.enableKeyboardShortcuts ? [
      {
        key: 'Escape',
        callback: () => {
          if (isMaximized) {
            setIsMaximized(false)
          } else if (!isMinimized) {
            onClose()
          }
        }
      },
      {
        key: 'Enter',
        meta: true,
        callback: () => {
          if (input.trim()) {
            sendMessage()
          }
        }
      },
      {
        key: 'k',
        meta: true,
        callback: () => {
          clearChat()
        }
      },
      {
        key: 'n',
        meta: true,
        callback: () => {
          startNewChat()
        }
      },
      {
        key: 'm',
        meta: true,
        callback: () => {
          setIsMinimized(!isMinimized)
        }
      },
      {
        key: 'f',
        meta: true,
        callback: () => {
          setIsMaximized(!isMaximized)
        }
      },
      {
        key: 'e',
        meta: true,
        callback: () => {
          exportChat()
        }
      },
      {
        key: 'h',
        meta: true,
        callback: () => {
          setShowSessions(!showSessions)
        }
      }
    ] : []
  )

  // Token and model management functions
  const autoFetchTokenFromMac = useCallback(async () => {
    // Skip if we already have a valid token
    const existingToken = localStorage.getItem('oauth_id_token')
    const expiry = localStorage.getItem('floodgate_token_expiry')
    
    if (existingToken && expiry) {
      const expiryTime = new Date(expiry).getTime()
      if (Date.now() < expiryTime) {
        console.log('✅ Using existing valid token')
        return
      }
    }

    // Only try local Mac service when running locally (not in production)
    if (window.location.hostname !== 'localhost' && window.location.hostname !== '127.0.0.1') {
      return
    }

    // Try to fetch from local Mac service
    try {
      console.log('🔄 Attempting to fetch token from local Mac service...')
      const response = await fetch('http://localhost:9876', {
        method: 'GET',
        signal: AbortSignal.timeout(3000) // 3 second timeout
      })

      if (response.ok) {
        const data = await response.json()
        if (data.success && data.token) {
          localStorage.setItem('oauth_id_token', data.token)
          localStorage.setItem('floodgate_token_expiry', data.expiry)
          localStorage.setItem('oauth_source', 'local-mac')
          console.log('✅ Token fetched from local Mac service!')
          toast.success('Floodgate token loaded from your Mac!')
        }
      }
    } catch (error) {
      // Silently fail - user might not have service running
      console.log('ℹ️ Local token service not available (this is OK)')
    }
  }, [])

  const checkTokenExpiry = useCallback(() => {
    const expiry = localStorage.getItem('floodgate_token_expiry')
    if (expiry) {
      const expiryTime = new Date(expiry).getTime()
      if (Date.now() >= expiryTime) {
        setTokenExpired(true)
      }
    }
  }, [])

  // Enhanced loadModels with caching — delegates to AIService for a single code path
  const loadModelsOptimized = useCallback(async () => {
    try {
      setIsLoadingModels(true)

      // Check cache first
      const cached = getCachedModels()
      if (cached) {
        setModels(cached)
        if (!selectedModel && cached.length > 0) {
          setSelectedModel(cached[0].id)
          localStorage.setItem('selected_ai_model', cached[0].id)
        }
        console.log('✅ Models loaded from cache:', cached.length, 'models')
        return true
      }

      // Retry loop via AIService (consistent /api/v1/ai/models path, auth headers)
      for (let attempt = 0; attempt < CONFIG.MAX_RETRIES; attempt++) {
        try {
          if (attempt > 0) {
            await new Promise(resolve => setTimeout(resolve, CONFIG.RETRY_DELAY * Math.pow(2, attempt - 1)))
            setRetryCount(attempt)
          }

          const modelList = await aiService.getModels()

          if (modelList.length > 0) {
            setModels(modelList)
            cacheModels(modelList)

            if (!selectedModel) {
              setSelectedModel(modelList[0].id)
              localStorage.setItem('selected_ai_model', modelList[0].id)
            }

            setTokenExpired(false)
            toast.success(`${modelList.length} AI model${modelList.length > 1 ? 's' : ''} loaded!`)
            console.log('✅ Models loaded from API:', modelList.length, 'models')
            return true
          }

          return false
        } catch (error: any) {
          if (attempt === CONFIG.MAX_RETRIES - 1) {
            console.error('Failed to load models after retries:', error)
            setModels([])
            return false
          }
          console.warn(`Model load attempt ${attempt + 1} failed:`, error)
        }
      }

      return false
    } catch (error) {
      console.error('Failed to load models:', error)
      setModels([])
      return false
    } finally {
      setIsLoadingModels(false)
      setRetryCount(0)
    }
  }, [getCachedModels, cacheModels, selectedModel])

  // Use the optimized loadModels function
  const loadModels = loadModelsOptimized

  // Enhanced initialization with performance optimizations
  useEffect(() => {
    if (isOpen) {
      checkTokenExpiry()
      
      // Setup offline detection
      const handleOnline = () => {
        setIsOnline(true)
        checkOnlineStatus()
      }
      const handleOffline = () => setIsOnline(false)
      
      window.addEventListener('online', handleOnline)
      window.addEventListener('offline', handleOffline)
      
      // Periodic connection check
      offlineCheckRef.current = setInterval(checkOnlineStatus, CONFIG.OFFLINE_CHECK_INTERVAL)
      
      // Initialize app
      Promise.race([
        Promise.all([
          autoFetchTokenFromMac().then(() => loadModelsOptimized()),
          loadSessions(),
        ]),
        new Promise((_, reject) =>
          setTimeout(() => reject(new Error('Initialization timeout')), 10000)
        )
      ]).catch((error) => {
        console.error('Initialization failed:', error)
        toast.error('Failed to initialize. Some features may be limited.')
      })
      
      // Restore draft and show welcome message
      restoreDraft()
      if (messages.length === 0) {
        setMessages([
          {
            id: '1',
            role: 'assistant',
            content: 'Hello! I\'m AlertHub Intelligence, your AI assistant. I can help you analyze alerts, suggest resolutions, and answer questions about your infrastructure. How can I help you today?',
            timestamp: new Date(),
          },
        ])
      }
      
      return () => {
        window.removeEventListener('online', handleOnline)
        window.removeEventListener('offline', handleOffline)
        if (offlineCheckRef.current) clearInterval(offlineCheckRef.current)
        if (abortControllerRef.current) abortControllerRef.current.abort()
      }
    }
  }, [isOpen, messages.length, autoFetchTokenFromMac, loadModelsOptimized, loadSessions, restoreDraft, checkOnlineStatus])

  // Handle input changes with draft saving and character counting
  useEffect(() => {
    setCharCount(input.length)
    saveDraft(input)
  }, [input, saveDraft])

  const refreshToken = () => {
    localStorage.removeItem('floodgate_token_expiry')
    localStorage.removeItem('oauth_id_token')
    localStorage.removeItem('floodgate_token')
    window.location.href = '/login'
  }

  // Enhanced sendMessage with retry logic and better error handling
  const sendMessage = useCallback(async (messageText?: string) => {
    const text = messageText || input.trim()
    if (!text || isTyping) return

    // Input validation
    if (text.length > CONFIG.MAX_MESSAGE_LENGTH) {
      toast.error(`Message too long (max ${CONFIG.MAX_MESSAGE_LENGTH} characters)`)
      return
    }

    // Play sound notification when sending message
    if (settings.soundEnabled && isSoundEnabled) {
      notificationSound.play()
    }

    const userMessage: Message = {
      id: Date.now().toString(),
      role: 'user',
      content: text,
      timestamp: new Date(),
    }

    setMessages((prev) => [...prev, userMessage])
    setInput('')
    setIsTyping(true)
    clearDraft()

    // Enhanced retry logic — delegates to AIService for a single /api/v1/ai/chat code path
    for (let attempt = 0; attempt < CONFIG.MAX_RETRIES; attempt++) {
      try {
        if (attempt > 0) {
          await new Promise(resolve => setTimeout(resolve, CONFIG.RETRY_DELAY * Math.pow(2, attempt - 1)))
          setRetryCount(attempt)
          toast(`Retry attempt ${attempt}/${CONFIG.MAX_RETRIES}...`, { icon: '🔄' })
        }

        console.log('💬 Sending message to Floodgate:', selectedModel)

        const chatMessages = [...messages, userMessage].map(m => ({
          role: m.role as 'user' | 'assistant',
          content: m.content
        }))

        const chatResponse = await aiService.sendMessage(
          chatMessages,
          selectedModel || 'gcp:gemini-2.0-flash-exp',
          sessionId || undefined
        )

        if (chatResponse && chatResponse.content) {
          if (chatResponse.session_id) {
            setSessionId(chatResponse.session_id)
          }

          const aiMessage: Message = {
            id: (Date.now() + 1).toString(),
            role: 'assistant',
            content: chatResponse.content,
            timestamp: new Date(),
          }

          setMessages((prev) => [...prev, aiMessage])

          // Clear session cache to refresh
          const userKey = getChatUserKey()
          localStorage.removeItem(`ai_sessions_cache_${userKey}`)
          loadSessions()

          // Play success sound when AI responds
          if (settings.soundEnabled && isSoundEnabled) {
            successSound.play()
          }

          return // Success, exit retry loop
        } else {
          throw new Error('Invalid response format')
        }
      } catch (error: any) {
        console.error(`❌ Chat attempt ${attempt + 1} failed:`, error)

        if (attempt === CONFIG.MAX_RETRIES - 1) {
          // Final attempt failed, show mock response
          const aiMessage: Message = {
            id: (Date.now() + 1).toString(),
            role: 'assistant',
            content: generateMockResponse(text),
            timestamp: new Date(),
          }
          setMessages((prev) => [...prev, aiMessage])
          toast.error('Failed to get AI response. Showing demo response.')
        }
      }
    }
    
    setIsTyping(false)
    setRetryCount(0)
  }, [input, isTyping, messages, selectedModel, sessionId, settings.soundEnabled, isSoundEnabled, notificationSound, successSound, clearDraft, loadSessions])

  // Message regeneration capability
  const regenerateResponse = useCallback(async (messageIndex: number) => {
    if (messageIndex === 0 || isTyping) return
    
    // Remove all messages after the selected index
    const newMessages = messages.slice(0, messageIndex)
    setMessages(newMessages)
    
    // Find the last user message
    const lastUserMessage = newMessages[newMessages.length - 1]
    if (lastUserMessage && lastUserMessage.role === 'user') {
      setIsTyping(true)
      toast('Regenerating response...', { icon: '🔄' })
      
      try {
        await sendMessage(lastUserMessage.content)
        toast.success('Response regenerated')
      } catch (error) {
        console.error('Failed to regenerate:', error)
        toast.error('Failed to regenerate response')
      }
    }
  }, [messages, isTyping, sendMessage])

  const useSuggestion = (text: string) => {
    setInput(text)
    sendMessage(text)
  }

  const copyMessage = (messageId: string, content: string) => {
    navigator.clipboard.writeText(content).then(() => {
      setCopiedMessageId(messageId)
      toast.success('Message copied!')
      setTimeout(() => setCopiedMessageId(null), 2000)
    })
  }

  const exportChat = () => {
    if (messages.length <= 1) {
      toast.error('No messages to export')
      return
    }

    const chatData = {
      session_id: sessionId,
      model: selectedModel,
      timestamp: new Date().toISOString(),
      messages: messages.map(m => ({
        role: m.role,
        content: m.content,
        timestamp: m.timestamp.toISOString()
      }))
    }

    const blob = new Blob([JSON.stringify(chatData, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `alerthub-chat-${new Date().toISOString().slice(0, 10)}.json`
    a.click()
    URL.revokeObjectURL(url)
    toast.success('Chat exported!')
  }

  const generateMockResponse = (input: string): string => {
    const lowerInput = input.toLowerCase()

    if (lowerInput.includes('analyze') && lowerInput.includes('alert')) {
      return `📊 **Alert Analysis**\n\nBased on current patterns, here's what I found:\n\n**Top Issues:**\n• Pod eviction events - High frequency\n• Memory saturation - 3 clusters affected\n• Job failures - Recurring pattern\n\n**Recommendations:**\n1. Review resource limits on affected pods\n2. Check for memory leaks in recent deployments\n3. Set up automation for common failure patterns\n4. Consider increasing cluster capacity\n\nWould you like me to dive deeper into any of these?`
    } else if (lowerInput.includes('troubleshoot') && (lowerInput.includes('pod') || lowerInput.includes('crash'))) {
      return `🛠️ **Pod Troubleshooting Guide**\n\n**Step-by-step:**\n1. Check pod status:\n   \`kubectl describe pod <pod-name>\`\n\n2. View logs:\n   \`kubectl logs <pod-name> --previous\`\n\n3. Common causes:\n   • OOMKilled - Increase memory limits\n   • CrashLoopBackOff - Fix application startup\n   • ImagePullBackOff - Check image availability\n   • Pending - Check node resources\n\n4. Check events:\n   \`kubectl get events --sort-by=.lastTimestamp\`\n\nWhat specific error are you seeing?`
    } else if (lowerInput.includes('mttr') || (lowerInput.includes('metric') && lowerInput.includes('dashboard'))) {
      return `📈 **MTTR Metrics Explained**\n\n**Mean Time To Resolve (MTTR)** measures how quickly you resolve alerts:\n\n**Your Dashboard Shows:**\n• **Average MTTR**: Time from alert creation to resolution\n• **P95 MTTR**: 95% of alerts resolved within this time\n• **Within SLA**: Percentage meeting your targets\n\n**How to Improve:**\n✅ Set up automation rules\n✅ Use bulk operations\n✅ Enable smart insights\n✅ Train team on common issues\n\n**Target**: Keep MTTR < 30 minutes for critical alerts\n\nNeed help improving your MTTR?`
    } else if (lowerInput.includes('automation') || lowerInput.includes('rules')) {
      return `⚡ **Alert Automation Guide**\n\n**Available Automation:**\n\n1. **Auto-Acknowledge**\n   • Known maintenance windows\n   • Expected deployment alerts\n\n2. **Auto-Resolve**\n   • Temporary network blips\n   • Self-healing systems\n\n3. **Auto-Assign**\n   • Route by severity\n   • Route by cluster/service\n   • On-call rotation\n\n4. **Escalation Rules**\n   • Time-based escalation\n   • Severity-based routing\n\n**To Create**:\nGo to Alerts → Automation tab\n\nWhat would you like to automate?`
    } else if (lowerInput.includes('team') || lowerInput.includes('user') || lowerInput.includes('admin')) {
      return `👥 **Team Management Guide**\n\n**Admin Features:**\n\n**Users:**\n• Create users with specific roles\n• Assign to teams\n• Set active/inactive status\n• Track last login\n\n**Roles & Permissions:**\n• Admin - Full access\n• Operator - Manage alerts & incidents\n• Viewer - Read-only access\n• Custom roles with specific permissions\n\n**Teams:**\n• Group users by responsibility\n• Set on-call schedules\n• Assign alerts automatically\n\n**To Get Started:**\nAdmin → Users/Roles/Teams\n\nNeed help setting up a specific workflow?`
    } else if (lowerInput.includes('kubernetes') || lowerInput.includes('k8s')) {
      return `☸️ **Kubernetes Quick Reference**\n\n**Common Commands:**\n\`\`\`bash\n# Pod status\nkubectl get pods -A\n\n# Describe pod\nkubectl describe pod <name> -n <namespace>\n\n# Logs\nkubectl logs <pod> -f\n\n# Events\nkubectl get events --sort-by=.lastTimestamp\n\n# Resource usage\nkubectl top pods\n\`\`\`\n\n**Common Issues:**\n• **OOMKilled** → Increase memory limits\n• **CrashLoopBackOff** → Check logs, fix startup\n• **ImagePullBackOff** → Verify image exists\n• **Pending** → Check node resources\n\nWhat's your specific issue?`
    } else if (lowerInput.includes('best practice')) {
      return `✨ **AlertHub Best Practices**\n\n**Alert Management:**\n✅ Acknowledge within 5 minutes\n✅ Resolve or escalate within 30 minutes\n✅ Group related alerts using Correlations\n✅ Set up maintenance windows\n\n**Team Workflow:**\n✅ Define clear on-call rotations\n✅ Document runbooks\n✅ Use bulk operations\n✅ Review analytics weekly\n\n**Automation:**\n✅ Auto-acknowledge known issues\n✅ Auto-assign by severity\n✅ Escalate un-acked critical alerts\n\n**Performance:**\n✅ Keep open alerts < 50\n✅ Maintain 95%+ SLO compliance\n✅ Track MTTR trends\n\nImplementing any of these?`
    } else {
      return `I understand you're asking about: "${input}"\n\n**I can help with:**\n• 🔍 Alert analysis and troubleshooting\n• ⚡ Automation and workflow setup\n• 📊 Dashboard and metrics interpretation\n• 👥 Team and user management\n• ☸️ Kubernetes debugging\n• 📈 Performance optimization\n\n**Try asking:**\n• "Analyze recent critical alerts"\n• "How do I troubleshoot pod crashes?"\n• "Explain MTTR metrics"\n• "Help with automation rules"\n\nWhat would you like to know?`
    }
  }

  const clearChat = () => {
    setMessages([
      {
        id: '1',
        role: 'assistant',
        content: 'Chat cleared. How can I help you?',
        timestamp: new Date(),
      },
    ])
    setSessionId(null)
  }

  const handleTokenSubmit = () => {
    if (!tokenInput.trim()) {
      toast.error('Please enter a token')
      return
    }

    localStorage.setItem('oauth_id_token', tokenInput.trim())
    const expiry = new Date(Date.now() + 50 * 60 * 1000).toISOString()
    localStorage.setItem('floodgate_token_expiry', expiry)
    localStorage.setItem('oauth_source', 'manual')
    
    setTokenInput('')
    setShowTokenInput(false)
    setTokenExpired(false)
    toast.success('Token saved! Loading models...')
    
    // Reload models
    setTimeout(() => loadModels(), 500)
  }

  const showSuggestions = messages.length <= 1

  return (
    <AnimatePresence>
      {isOpen && (
        <>
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            onClick={onClose}
            style={{
              position: 'fixed',
              inset: 0,
              background: 'rgba(0, 0, 0, 0.4)',
              backdropFilter: 'blur(20px)',
              WebkitBackdropFilter: 'blur(20px)',
              zIndex: 45,
            }}
          />

          <motion.div
            initial={{ opacity: 0, scale: 0.95, y: 20 }}
            animate={{
              opacity: 1,
              scale: 1,
              y: 0,
              width: isMaximized ? '90vw' : isMinimized ? '420px' : '500px',
              height: isMinimized ? '64px' : isMaximized ? '90vh' : '680px',
            }}
            exit={{ opacity: 0, scale: 0.95, y: 20 }}
            transition={{ type: 'spring', damping: 25, stiffness: 300 }}
            style={{
              position: 'fixed',
              bottom: 32,
              right: 32,
              background: apple.secondaryBackground,
              borderRadius: apple.radius.xl,
              border: `0.5px solid ${apple.separator}`,
              boxShadow: '0 24px 80px rgba(0,0,0,0.2)',
              zIndex: 50,
              display: 'flex',
              flexDirection: 'column',
              overflow: 'hidden',
            }}
          >
            {/* Enhanced Header with Connection Status */}
            <div style={{
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
              padding: '16px 20px',
              borderBottom: `0.5px solid ${apple.separator}`,
              background: apple.fill,
            }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 12, flex: 1, minWidth: 0 }}>
                <div style={{
                  padding: 10,
                  borderRadius: apple.radius.md,
                  background: `linear-gradient(135deg, ${apple.purple}, ${apple.indigo})`,
                  boxShadow: '0 4px 12px rgba(175, 82, 222, 0.3)',
                }}>
                  <Sparkles style={{ width: 20, height: 20, color: '#fff' }} />
                </div>
                <div style={{ flex: 1, minWidth: 0 }}>
                  <h3 style={{ fontSize: 16, fontWeight: 600, color: apple.label, margin: 0 }}>
                    AlertHub Intelligence
                  </h3>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginTop: 2 }}>
                    {/* Connection Status Indicator */}
                    {isOnline ? (
                      <Wifi style={{ width: 12, height: 12, color: apple.green }} />
                    ) : (
                      <WifiOff style={{ width: 12, height: 12, color: apple.red }} />
                    )}
                    
                    {isLoadingModels ? (
                      <>
                        <RefreshCw style={{ width: 12, height: 12, color: apple.blue, animation: 'spin 1s linear infinite' }} />
                        <p style={{ fontSize: 12, color: apple.secondaryLabel, margin: 0 }}>Loading...</p>
                      </>
                    ) : (
                      <>
                        <div style={{
                          width: 8,
                          height: 8,
                          borderRadius: '50%',
                          background: models.length > 0 ? apple.green : apple.gray,
                          ...(models.length > 0 && { animation: 'pulse 2s infinite' }),
                        }} />
                        <p style={{ fontSize: 12, color: apple.secondaryLabel, margin: 0 }}>
                          {models.length > 0 ? 'AI-powered' : 'Demo Mode'}
                          {models.length > 0 && (() => {
                            const source = localStorage.getItem('oauth_source')
                            if (source) {
                              const sourceLabels: Record<string, string> = {
                                'mas-proxy': 'MAS Proxy',
                                'headers': 'MAS',
                                'floodgate-cli': 'CLI',
                                'database-cache': 'Cache',
                                'local-mac': 'Mac',
                                'backend': 'Backend'
                              }
                              return ` • ${sourceLabels[source] || source}`
                            }
                            return ''
                          })()}
                        </p>
                      </>
                    )}
                  </div>
                </div>
              </div>
              
              <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexShrink: 0 }}>
                {/* Retry indicator */}
                {retryCount > 0 && (
                  <div style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 4,
                    fontSize: 11,
                    color: apple.orange,
                    background: `${apple.orange}10`,
                    padding: '4px 8px',
                    borderRadius: apple.radius.sm,
                  }}>
                    <RefreshCw style={{ width: 12, height: 12, animation: 'spin 1s linear infinite' }} />
                    Retry {retryCount}/{CONFIG.MAX_RETRIES}
                  </div>
                )}

                {models.length > 0 && !isMinimized && (
                  <select
                    value={selectedModel}
                    onChange={(e) => {
                      setSelectedModel(e.target.value)
                      localStorage.setItem('selected_ai_model', e.target.value)
                      toast.success(`Switched to ${e.target.options[e.target.selectedIndex].text}`)
                    }}
                    style={{
                      fontSize: 12,
                      padding: '6px 8px',
                      maxWidth: 140,
                      borderRadius: apple.radius.sm,
                      border: `0.5px solid ${apple.separator}`,
                      background: apple.background,
                      color: apple.label,
                      outline: 'none',
                      appearance: 'none',
                      cursor: 'pointer',
                    }}
                  >
                    {models.map((model) => (
                      <option key={model.id} value={model.id}>
                        {model.name || model.id}
                      </option>
                    ))}
                  </select>
                )}
                
                {messages.length > 1 && !isMinimized && (
                  <button
                    onClick={exportChat}
                    style={{
                      width: 28,
                      height: 28,
                      borderRadius: apple.radius.sm,
                      border: `0.5px solid ${apple.separator}`,
                      background: apple.fill,
                      color: apple.label,
                      cursor: 'pointer',
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      transition: 'all 0.15s',
                    }}
                    title="Export Chat"
                    onMouseEnter={(e) => {
                      e.currentTarget.style.background = apple.secondaryFill
                    }}
                    onMouseLeave={(e) => {
                      e.currentTarget.style.background = apple.fill
                    }}
                  >
                    <Download style={{ width: 14, height: 14 }} />
                  </button>
                )}
                
                <button
                  onClick={() => setIsMinimized(!isMinimized)}
                  style={{
                    width: 28,
                    height: 28,
                    borderRadius: apple.radius.sm,
                    border: `0.5px solid ${apple.separator}`,
                    background: apple.fill,
                    color: apple.label,
                    cursor: 'pointer',
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    transition: 'all 0.15s',
                  }}
                  title={isMinimized ? 'Expand' : 'Minimize'}
                  onMouseEnter={(e) => {
                    e.currentTarget.style.background = apple.secondaryFill
                  }}
                  onMouseLeave={(e) => {
                    e.currentTarget.style.background = apple.fill
                  }}
                >
                  {isMinimized ? <Maximize2 style={{ width: 14, height: 14 }} /> : <Minus style={{ width: 14, height: 14 }} />}
                </button>
                
                {!isMinimized && (
                  <button
                    onClick={() => setIsMaximized(!isMaximized)}
                    style={{
                      width: 28,
                      height: 28,
                      borderRadius: apple.radius.sm,
                      border: `0.5px solid ${apple.separator}`,
                      background: apple.fill,
                      color: apple.label,
                      cursor: 'pointer',
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      transition: 'all 0.15s',
                    }}
                    title={isMaximized ? 'Restore' : 'Maximize'}
                    onMouseEnter={(e) => {
                      e.currentTarget.style.background = apple.secondaryFill
                    }}
                    onMouseLeave={(e) => {
                      e.currentTarget.style.background = apple.fill
                    }}
                  >
                    {isMaximized ? <Minimize2 style={{ width: 14, height: 14 }} /> : <Maximize2 style={{ width: 14, height: 14 }} />}
                  </button>
                )}
                
                <button
                  onClick={onClose}
                  style={{
                    width: 28,
                    height: 28,
                    borderRadius: apple.radius.sm,
                    border: `0.5px solid ${apple.separator}`,
                    background: apple.fill,
                    color: apple.red,
                    cursor: 'pointer',
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    transition: 'all 0.15s',
                  }}
                  onMouseEnter={(e) => {
                    e.currentTarget.style.background = `${apple.red}15`
                  }}
                  onMouseLeave={(e) => {
                    e.currentTarget.style.background = apple.fill
                  }}
                >
                  <X style={{ width: 16, height: 16 }} />
                </button>
              </div>
            </div>

            {/* Token Expired Warning */}
            {tokenExpired && !isMinimized && (
              <div style={{
                padding: 16,
                background: `${apple.orange}10`,
                borderBottom: `0.5px solid ${apple.orange}30`,
              }}>
                <div style={{ display: 'flex', alignItems: 'start', gap: 12 }}>
                  <AlertCircle style={{ width: 20, height: 20, color: apple.orange, flexShrink: 0, marginTop: 2 }} />
                  <div style={{ flex: 1 }}>
                    <p style={{ fontSize: 14, fontWeight: 600, color: apple.orange, marginBottom: 4 }}>
                      Token Expired
                    </p>
                    <p style={{ fontSize: 12, color: apple.secondaryLabel, marginBottom: 12 }}>
                      Your authentication token has expired. Please login again to use AI features.
                    </p>
                    <button
                      onClick={refreshToken}
                      style={{
                        display: 'flex',
                        alignItems: 'center',
                        gap: 6,
                        fontSize: 12,
                        padding: '8px 12px',
                        borderRadius: apple.radius.sm,
                        border: 'none',
                        background: apple.orange,
                        color: '#fff',
                        cursor: 'pointer',
                        fontWeight: 500,
                      }}
                    >
                      <RefreshCw style={{ width: 14, height: 14 }} />
                      Refresh & Login
                    </button>
                  </div>
                </div>
              </div>
            )}

            {/* Messages */}
            {!isMinimized && (
              <div style={{
                flex: 1,
                overflowY: 'auto',
                padding: 20,
                background: apple.background,
                display: 'flex',
                flexDirection: 'column',
                gap: 16,
              }}>
                {/* Token Setup Prompt */}
                {showTokenInput && (
                  <div style={{
                    padding: 16,
                    background: apple.secondaryBackground,
                    border: `0.5px solid ${apple.separator}`,
                    borderRadius: apple.radius.lg,
                    display: 'flex',
                    flexDirection: 'column',
                    gap: 12,
                  }}>
                    <div style={{ display: 'flex', alignItems: 'start', justifyContent: 'space-between' }}>
                      <h4 style={{ fontSize: 14, fontWeight: 600, color: apple.label, margin: 0 }}>
                        🔑 Setup Floodgate Token
                      </h4>
                      <button
                        onClick={() => setShowTokenInput(false)}
                        style={{
                          width: 24,
                          height: 24,
                          borderRadius: apple.radius.sm,
                          border: 'none',
                          background: apple.fill,
                          color: apple.label,
                          cursor: 'pointer',
                          display: 'flex',
                          alignItems: 'center',
                          justifyContent: 'center',
                        }}
                      >
                        <X style={{ width: 12, height: 12 }} />
                      </button>
                    </div>
                    
                    <p style={{ fontSize: 12, color: apple.secondaryLabel, margin: 0 }}>
                      Run this command on your Mac terminal:
                    </p>
                    
                    <div style={{ position: 'relative' }}>
                      <pre style={{
                        fontSize: 11,
                        background: '#1a1a1a',
                        color: '#00ff88',
                        padding: 12,
                        borderRadius: apple.radius.sm,
                        overflow: 'auto',
                        fontFamily: 'SFMono-Regular, Consolas, monospace',
                        margin: 0,
                      }}>
{`appleconnect getToken -C hvys3fcwcteqrvw3qzkvtk86viuoqv \\
  --token-type=oauth --interactivity-type=none \\
  -E prod -G pkce \\
  -o openid,dsid,accountname,profile,groups | \\
  grep 'oauth-id-token' | awk '{print $2}'`}
                      </pre>
                      <button
                        onClick={() => {
                          navigator.clipboard.writeText(`appleconnect getToken -C hvys3fcwcteqrvw3qzkvtk86viuoqv --token-type=oauth --interactivity-type=none -E prod -G pkce -o openid,dsid,accountname,profile,groups | grep 'oauth-id-token' | awk '{print $2}'`)
                          toast.success('Command copied to clipboard!')
                        }}
                        style={{
                          position: 'absolute',
                          top: 8,
                          right: 8,
                          padding: 6,
                          borderRadius: apple.radius.sm,
                          background: 'rgba(255,255,255,0.1)',
                          border: '1px solid rgba(255,255,255,0.2)',
                          color: '#fff',
                          cursor: 'pointer',
                          fontSize: 11,
                        }}
                        title="Copy command"
                      >
                        <Copy style={{ width: 12, height: 12 }} />
                      </button>
                    </div>
                    
                    <p style={{ fontSize: 12, color: apple.secondaryLabel }}>Paste the token below:</p>
                    
                    <textarea
                      value={tokenInput}
                      onChange={(e) => setTokenInput(e.target.value)}
                      placeholder="eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9..."
                      rows={3}
                      style={{
                        width: '100%',
                        padding: 10,
                        borderRadius: apple.radius.sm,
                        border: `0.5px solid ${apple.separator}`,
                        background: apple.fill,
                        color: apple.label,
                        fontSize: 11,
                        fontFamily: 'SFMono-Regular, Consolas, monospace',
                        outline: 'none',
                        resize: 'vertical',
                      }}
                    />
                    
                    <button
                      onClick={handleTokenSubmit}
                      disabled={!tokenInput.trim()}
                      style={{
                        width: '100%',
                        padding: '10px 16px',
                        borderRadius: apple.radius.sm,
                        border: 'none',
                        background: apple.blue,
                        color: '#fff',
                        fontSize: 13,
                        fontWeight: 500,
                        cursor: !tokenInput.trim() ? 'default' : 'pointer',
                        opacity: !tokenInput.trim() ? 0.5 : 1,
                      }}
                    >
                      Save Token & Load Models
                    </button>
                    
                    <p style={{ fontSize: 11, color: apple.tertiaryLabel, margin: 0 }}>
                      💡 This token is user-specific and stays on your browser only
                    </p>
                  </div>
                )}
                
                {/* Enable AI Banner */}
                {!showTokenInput && models.length === 0 && !isLoadingModels && (
                  <div style={{
                    padding: 14,
                    background: `${apple.blue}10`,
                    border: `0.5px solid ${apple.blue}30`,
                    borderRadius: apple.radius.lg,
                  }}>
                    <div style={{ display: 'flex', alignItems: 'start', gap: 10 }}>
                      <Sparkles style={{ width: 16, height: 16, color: apple.blue, flexShrink: 0, marginTop: 2 }} />
                      <div style={{ flex: 1 }}>
                        <p style={{ fontSize: 12, fontWeight: 600, color: apple.blue, marginBottom: 4 }}>
                          Enable Real AI Models
                        </p>
                        <p style={{ fontSize: 11, color: apple.secondaryLabel, marginBottom: 8 }}>
                          Get your Floodgate token to use Claude & Gemini
                        </p>
                        <button
                          onClick={() => setShowTokenInput(true)}
                          style={{
                            fontSize: 12,
                            padding: '6px 12px',
                            borderRadius: apple.radius.sm,
                            border: 'none',
                            background: apple.blue,
                            color: '#fff',
                            cursor: 'pointer',
                            fontWeight: 500,
                          }}
                        >
                          Setup Token
                        </button>
                      </div>
                    </div>
                  </div>
                )}

                {/* Suggested Prompts */}
                {showSuggestions && !showTokenInput && (
                  <div style={{
                    display: 'grid',
                    gridTemplateColumns: '1fr 1fr',
                    gap: 10,
                  }}>
                    {SUGGESTED_PROMPTS.map((prompt, index) => (
                      <motion.button
                        key={index}
                        initial={{ opacity: 0, y: 10 }}
                        animate={{ opacity: 1, y: 0 }}
                        transition={{ delay: index * 0.05 + 0.2 }}
                        onClick={() => useSuggestion(prompt.text)}
                        style={{
                          textAlign: 'left',
                          padding: 12,
                          background: apple.secondaryBackground,
                          border: `0.5px solid ${apple.separator}`,
                          borderRadius: apple.radius.md,
                          cursor: 'pointer',
                          transition: 'all 0.2s ease',
                        }}
                        onMouseEnter={(e) => {
                          e.currentTarget.style.borderColor = apple.blue
                          e.currentTarget.style.boxShadow = '0 4px 12px rgba(0, 122, 255, 0.15)'
                        }}
                        onMouseLeave={(e) => {
                          e.currentTarget.style.borderColor = apple.separator
                          e.currentTarget.style.boxShadow = 'none'
                        }}
                      >
                        <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4 }}>
                          <span style={{ fontSize: 16 }}>{prompt.icon}</span>
                          <Lightbulb style={{ width: 12, height: 12, color: apple.yellow }} />
                        </div>
                        <p style={{ fontSize: 12, fontWeight: 500, color: apple.label, lineHeight: 1.4, margin: 0 }}>
                          {prompt.text}
                        </p>
                      </motion.button>
                    ))}
                  </div>
                )}

                {/* Chat Messages */}
                {messages.map((message) => (
                  <motion.div
                    key={message.id}
                    initial={{ opacity: 0, y: 10 }}
                    animate={{ opacity: 1, y: 0 }}
                    style={{
                      display: 'flex',
                      justifyContent: message.role === 'user' ? 'flex-end' : 'flex-start',
                    }}
                  >
                    <div style={{ maxWidth: '85%' }} className="group">
                      <div
                        style={{
                          padding: 12,
                          borderRadius: apple.radius.lg,
                          background: message.role === 'user' ? apple.blue : apple.secondaryBackground,
                          border: message.role === 'user' ? 'none' : `0.5px solid ${apple.separator}`,
                          color: message.role === 'user' ? '#fff' : apple.label,
                          boxShadow: '0 2px 8px rgba(0,0,0,0.08)',
                        }}
                      >
                        {message.role === 'assistant' && (
                          <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 8 }}>
                            <Bot style={{ width: 14, height: 14, color: apple.purple }} />
                            <span style={{ fontSize: 12, fontWeight: 600, color: apple.secondaryLabel }}>AI</span>
                          </div>
                        )}
                        <p style={{
                          fontSize: 13,
                          lineHeight: 1.5,
                          whiteSpace: 'pre-wrap',
                          wordBreak: 'break-word',
                          margin: 0,
                        }}>
                          {message.content}
                        </p>
                        <p style={{
                          fontSize: 11,
                          marginTop: 6,
                          color: message.role === 'user' ? 'rgba(255,255,255,0.8)' : apple.tertiaryLabel,
                        }}>
                          {message.timestamp.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
                        </p>
                      </div>
                      
                      {message.role === 'assistant' && (
                        <div style={{
                          display: 'flex',
                          alignItems: 'center',
                          gap: 8,
                          marginTop: 6,
                          opacity: 0,
                          transition: 'opacity 0.2s',
                        }} className="group-hover:opacity-100">
                          <button
                            onClick={() => copyMessage(message.id, message.content)}
                            style={{
                              display: 'flex',
                              alignItems: 'center',
                              gap: 4,
                              fontSize: 11,
                              padding: '4px 8px',
                              borderRadius: apple.radius.sm,
                              border: 'none',
                              background: apple.fill,
                              color: apple.secondaryLabel,
                              cursor: 'pointer',
                              transition: 'all 0.15s',
                            }}
                            onMouseEnter={(e) => {
                              e.currentTarget.style.background = apple.secondaryFill
                            }}
                            onMouseLeave={(e) => {
                              e.currentTarget.style.background = apple.fill
                            }}
                          >
                            {copiedMessageId === message.id ? (
                              <><Check style={{ width: 12, height: 12, color: apple.green }} /> Copied</>
                            ) : (
                              <><Copy style={{ width: 12, height: 12 }} /> Copy</>
                            )}
                          </button>
                          
                          {/* Regenerate Response Button */}
                          <button
                            onClick={() => regenerateResponse(messages.indexOf(message))}
                            disabled={isTyping}
                            style={{
                              display: 'flex',
                              alignItems: 'center',
                              gap: 4,
                              fontSize: 11,
                              padding: '4px 8px',
                              borderRadius: apple.radius.sm,
                              border: 'none',
                              background: apple.fill,
                              color: apple.secondaryLabel,
                              cursor: isTyping ? 'default' : 'pointer',
                              opacity: isTyping ? 0.5 : 1,
                              transition: 'all 0.15s',
                            }}
                            onMouseEnter={(e) => {
                              if (!isTyping) e.currentTarget.style.background = apple.secondaryFill
                            }}
                            onMouseLeave={(e) => {
                              e.currentTarget.style.background = apple.fill
                            }}
                            title="Regenerate response"
                          >
                            <RotateCcw style={{ width: 12, height: 12 }} />
                            Regenerate
                          </button>
                        </div>
                      )}
                    </div>
                  </motion.div>
                ))}

                {isTyping && (
                  <motion.div
                    initial={{ opacity: 0 }}
                    animate={{ opacity: 1 }}
                    style={{ display: 'flex', justifyContent: 'flex-start' }}
                  >
                    <div style={{
                      padding: 12,
                      background: apple.secondaryBackground,
                      border: `0.5px solid ${apple.separator}`,
                      borderRadius: apple.radius.lg,
                    }}>
                      <div style={{ display: 'flex', gap: 4 }}>
                        <div style={{
                          width: 8,
                          height: 8,
                          borderRadius: '50%',
                          background: apple.secondaryLabel,
                          animation: 'bounce 1.4s infinite ease-in-out both',
                          animationDelay: '0ms',
                        }} />
                        <div style={{
                          width: 8,
                          height: 8,
                          borderRadius: '50%',
                          background: apple.secondaryLabel,
                          animation: 'bounce 1.4s infinite ease-in-out both',
                          animationDelay: '160ms',
                        }} />
                        <div style={{
                          width: 8,
                          height: 8,
                          borderRadius: '50%',
                          background: apple.secondaryLabel,
                          animation: 'bounce 1.4s infinite ease-in-out both',
                          animationDelay: '320ms',
                        }} />
                      </div>
                    </div>
                  </motion.div>
                )}
              </div>
            )}

            {/* Input */}
            {!isMinimized && (
              <div style={{
                padding: 16,
                borderTop: `0.5px solid ${apple.separator}`,
                background: apple.secondaryBackground,
              }}>
                {messages.length > 1 && (
                  <div style={{ display: 'flex', gap: 8, marginBottom: 12 }}>
                    <button
                      onClick={clearChat}
                      style={{
                        fontSize: 12,
                        color: apple.secondaryLabel,
                        background: 'none',
                        border: 'none',
                        cursor: 'pointer',
                        fontWeight: 500,
                        transition: 'color 0.15s',
                      }}
                      onMouseEnter={(e) => {
                        e.currentTarget.style.color = apple.label
                      }}
                      onMouseLeave={(e) => {
                        e.currentTarget.style.color = apple.secondaryLabel
                      }}
                    >
                      Clear Chat
                    </button>
                    {!tokenExpired && (
                      <button
                        onClick={loadModels}
                        disabled={isLoadingModels}
                        style={{
                          fontSize: 12,
                          color: apple.secondaryLabel,
                          background: 'none',
                          border: 'none',
                          cursor: isLoadingModels ? 'default' : 'pointer',
                          fontWeight: 500,
                          display: 'flex',
                          alignItems: 'center',
                          gap: 4,
                          opacity: isLoadingModels ? 0.5 : 1,
                          transition: 'color 0.15s',
                        }}
                        onMouseEnter={(e) => {
                          if (!isLoadingModels) e.currentTarget.style.color = apple.label
                        }}
                        onMouseLeave={(e) => {
                          e.currentTarget.style.color = apple.secondaryLabel
                        }}
                      >
                        <RefreshCw style={{ 
                          width: 12, 
                          height: 12, 
                          ...(isLoadingModels && { animation: 'spin 1s linear infinite' }) 
                        }} />
                        Reload Models
                      </button>
                    )}
                    {models.length === 0 && !isLoadingModels && (
                      <button
                        onClick={() => setShowTokenInput(true)}
                        style={{
                          fontSize: 12,
                          color: apple.secondaryLabel,
                          background: 'none',
                          border: 'none',
                          cursor: 'pointer',
                          fontWeight: 500,
                          transition: 'color 0.15s',
                        }}
                        onMouseEnter={(e) => {
                          e.currentTarget.style.color = apple.label
                        }}
                        onMouseLeave={(e) => {
                          e.currentTarget.style.color = apple.secondaryLabel
                        }}
                      >
                        Setup Token
                      </button>
                    )}
                  </div>
                )}
                
                {/* Enhanced Input Area with Character Counter */}
                <div style={{ position: 'relative' }}>
                  <div style={{ display: 'flex', gap: 8 }}>
                    <div style={{ position: 'relative', flex: 1 }}>
                      <input
                        type="text"
                        value={input}
                        onChange={(e) => setInput(e.target.value)}
                        onKeyPress={(e) => e.key === 'Enter' && !e.shiftKey && sendMessage()}
                        placeholder={tokenExpired ? "Login required to use AI" : "Ask me anything..."}
                        disabled={tokenExpired || isTyping}
                        maxLength={CONFIG.MAX_MESSAGE_LENGTH}
                        style={{
                          width: '100%',
                          height: 36,
                          padding: '0 12px',
                          paddingRight: '60px', // Space for character counter
                          borderRadius: apple.radius.md,
                          border: `0.5px solid ${charCount > CONFIG.MAX_MESSAGE_LENGTH * 0.9 ? apple.red : apple.separator}`,
                          background: apple.fill,
                          color: apple.label,
                          fontSize: 14,
                          outline: 'none',
                          transition: 'all 0.2s',
                        }}
                        onFocus={(e) => {
                          e.target.style.borderColor = charCount > CONFIG.MAX_MESSAGE_LENGTH * 0.9 ? apple.red : apple.blue
                          e.target.style.boxShadow = `0 0 0 3px rgba(${charCount > CONFIG.MAX_MESSAGE_LENGTH * 0.9 ? '255, 59, 48' : '0, 122, 255'}, 0.2)`
                        }}
                        onBlur={(e) => {
                          e.target.style.borderColor = charCount > CONFIG.MAX_MESSAGE_LENGTH * 0.9 ? apple.red : apple.separator
                          e.target.style.boxShadow = 'none'
                        }}
                      />
                      
                      {/* Character Counter */}
                      <div style={{
                        position: 'absolute',
                        right: 12,
                        top: '50%',
                        transform: 'translateY(-50%)',
                        fontSize: 11,
                        color: charCount > CONFIG.MAX_MESSAGE_LENGTH * 0.9 ? apple.red : apple.tertiaryLabel,
                        userSelect: 'none',
                        pointerEvents: 'none',
                      }}>
                        {charCount}/{CONFIG.MAX_MESSAGE_LENGTH}
                      </div>
                    </div>
                    
                    <button
                      onClick={() => sendMessage()}
                      disabled={!input.trim() || isTyping || tokenExpired || charCount > CONFIG.MAX_MESSAGE_LENGTH}
                      style={{
                        width: 36,
                        height: 36,
                        borderRadius: apple.radius.sm,
                        border: 'none',
                        background: (!input.trim() || isTyping || tokenExpired || charCount > CONFIG.MAX_MESSAGE_LENGTH)
                          ? apple.fill
                          : apple.blue,
                        color: (!input.trim() || isTyping || tokenExpired || charCount > CONFIG.MAX_MESSAGE_LENGTH)
                          ? apple.tertiaryLabel
                          : '#fff',
                        cursor: (!input.trim() || isTyping || tokenExpired || charCount > CONFIG.MAX_MESSAGE_LENGTH)
                          ? 'default'
                          : 'pointer',
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                        transition: 'all 0.15s',
                      }}
                    >
                      {isTyping ? (
                        <RefreshCw style={{ width: 16, height: 16, animation: 'spin 1s linear infinite' }} />
                      ) : (
                        <Send style={{ width: 16, height: 16 }} />
                      )}
                    </button>
                  </div>
                  
                  {/* Draft Saved Indicator */}
                  {draftSaved && (
                    <motion.div
                      initial={{ opacity: 0, y: 10 }}
                      animate={{ opacity: 1, y: 0 }}
                      exit={{ opacity: 0, y: 10 }}
                      style={{
                        position: 'absolute',
                        top: -24,
                        right: 0,
                        fontSize: 10,
                        color: apple.green,
                        background: `${apple.green}10`,
                        padding: '2px 6px',
                        borderRadius: apple.radius.sm,
                        display: 'flex',
                        alignItems: 'center',
                        gap: 4,
                      }}
                    >
                      <Check style={{ width: 10, height: 10 }} />
                      Draft saved
                    </motion.div>
                  )}
                </div>
                
                {/* Enhanced Status Bar */}
                <div style={{
                  display: 'flex',
                  justifyContent: 'space-between',
                  alignItems: 'center',
                  marginTop: 8,
                }}>
                  <p style={{
                    fontSize: 11,
                    color: apple.tertiaryLabel,
                    margin: 0,
                  }}>
                    {tokenExpired
                      ? 'Token expired - Click Setup Token'
                      : models.length > 0
                        ? `Using ${models.length} AI models via Floodgate`
                        : 'Demo mode - Click "Setup Token" for real AI'}
                  </p>
                  
                  {/* Connection Status */}
                  {!isOnline && (
                    <div style={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: 4,
                      fontSize: 11,
                      color: apple.red,
                      background: `${apple.red}10`,
                      padding: '2px 6px',
                      borderRadius: apple.radius.sm,
                    }}>
                      <WifiOff style={{ width: 10, height: 10 }} />
                      Offline
                    </div>
                  )}
                </div>
              </div>
            )}
          </motion.div>
        </>
      )}
    </AnimatePresence>
  )
}

// Floating AI Button
export function AIAssistantButton({ onClick }: { onClick: () => void }) {
  return (
    <motion.button
      initial={{ scale: 0 }}
      animate={{ scale: 1 }}
      whileHover={{ scale: 1.1 }}
      whileTap={{ scale: 0.95 }}
      onClick={onClick}
      style={{
        position: 'fixed',
        bottom: 24,
        right: 24,
        width: 56,
        height: 56,
        background: `linear-gradient(135deg, ${apple.purple}, ${apple.indigo})`,
        borderRadius: '50%',
        boxShadow: `0 12px 40px ${apple.purple}40`,
        border: 'none',
        cursor: 'pointer',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        zIndex: 40,
        transition: 'all 0.3s cubic-bezier(0.4, 0, 0.2, 1)',
      }}
      title="Open AI Assistant (⌘/)"
    >
      <Sparkles style={{ width: 24, height: 24, color: '#fff' }} />
    </motion.button>
  )
}
