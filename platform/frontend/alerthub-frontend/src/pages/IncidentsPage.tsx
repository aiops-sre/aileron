import React, { useState, useEffect, useRef, useMemo } from 'react'
import { useNavigate, useLocation } from 'react-router-dom'
import { motion, AnimatePresence } from 'framer-motion'
import {
  AlertTriangle,
  Plus,
  Search,
  RefreshCw,
  Clock,
  User,
  CheckCircle,
  XCircle,
  Play,
  Check,
  Eye,
  Loader2,
  Filter,
  Users,
  Server,
  Zap,
  X,
  Download,
  BarChart3,
  TrendingDown,
  Bell,
  Activity,
  Shield,
  GitBranch,
  Cpu,
  ChevronRight,
  Radio,
  Network,
  Sparkles,
  Key,
  FileText,
  CheckSquare,
  ThumbsUp,
  ThumbsDown,
  BookOpen,
} from 'lucide-react'
import { useKentaurusIncidentsStore, selectFilteredIncidents, selectUnreadNotifications } from '@/stores/kentaurusIncidentsStore'
import type { KentaurusIncident, DateRange } from '@/types/kentaurus'
import { incidentsApi } from '@/lib/api'
import toast from 'react-hot-toast'

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Apple Design Tokens
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

const apple = {
  blue: '#007AFF',
  green: '#34C759',
  red: '#FF3B30',
  orange: '#FF9500',
  yellow: '#FFCC00',
  purple: '#AF52DE',
  gray: '#8E8E93',
  label: 'var(--color-text)',
  secondaryLabel: 'var(--color-text-secondary)',
  tertiaryLabel: 'var(--color-text-tertiary, #8E8E93)',
  quaternaryLabel: 'rgba(142, 142, 147, 0.4)',
  separator: 'var(--color-separator, rgba(142, 142, 147, 0.12))',
  fill: 'var(--color-fill, rgba(142, 142, 147, 0.08))',
  secondaryFill: 'rgba(142, 142, 147, 0.12)',
  tertiaryFill: 'rgba(142, 142, 147, 0.06)',
  background: 'var(--color-background)',
  secondaryBackground: 'var(--color-card, rgba(255, 255, 255, 0.8))',
  radius: { sm: 6, md: 10, lg: 12, xl: 16, '2xl': 20 },
} as const

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Utility Components
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

function AppleSearch({ value, onChange, placeholder = 'Search' }: {
  value: string
  onChange: (v: string) => void
  placeholder?: string
}) {
  return (
    <div style={{ position: 'relative' }}>
      <Search style={{
        position: 'absolute',
        left: 10,
        top: '50%',
        transform: 'translateY(-50%)',
        width: 16,
        height: 16,
        color: apple.tertiaryLabel,
        pointerEvents: 'none',
      }} />
      <input
        type="text"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        style={{
          width: '100%',
          height: 36,
          borderRadius: apple.radius.md,
          border: 'none',
          background: apple.fill,
          paddingLeft: 34,
          paddingRight: value ? 34 : 12,
          fontSize: 15,
          color: apple.label,
          outline: 'none',
          transition: 'box-shadow 0.2s ease',
        }}
        onFocus={(e) => {
          e.target.style.boxShadow = `0 0 0 3px rgba(0, 122, 255, 0.25)`
        }}
        onBlur={(e) => {
          e.target.style.boxShadow = 'none'
        }}
      />
      {value && (
        <button
          onClick={() => onChange('')}
          style={{
            position: 'absolute',
            right: 8,
            top: '50%',
            transform: 'translateY(-50%)',
            width: 20,
            height: 20,
            borderRadius: '50%',
            background: apple.tertiaryLabel,
            border: 'none',
            cursor: 'pointer',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            padding: 0,
          }}
        >
          <X style={{ width: 12, height: 12, color: apple.secondaryBackground }} />
        </button>
      )}
    </div>
  )
}

function AppleButton({
  children,
  onClick,
  variant = 'primary',
  size = 'medium',
  icon,
  disabled = false,
  loading = false,
}: {
  children: React.ReactNode
  onClick?: () => void
  variant?: 'primary' | 'secondary' | 'danger' | 'success'
  size?: 'small' | 'medium' | 'large'
  icon?: React.ReactNode
  disabled?: boolean
  loading?: boolean
}) {
  const variants = {
    primary: { bg: apple.blue, color: '#fff' },
    secondary: { bg: apple.fill, color: apple.label },
    danger: { bg: apple.red, color: '#fff' },
    success: { bg: apple.green, color: '#fff' },
  }

  const sizes = {
    small: { padding: '6px 12px', fontSize: 12 },
    medium: { padding: '10px 16px', fontSize: 14 },
    large: { padding: '12px 20px', fontSize: 16 },
  }

  const style = variants[variant]
  const sizeStyle = sizes[size]

  return (
    <button
      onClick={onClick}
      disabled={disabled || loading}
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 6,
        ...sizeStyle,
        borderRadius: apple.radius.sm,
        border: variant === 'secondary' ? `0.5px solid ${apple.separator}` : 'none',
        background: style.bg,
        color: style.color,
        fontWeight: 500,
        cursor: disabled || loading ? 'not-allowed' : 'pointer',
        opacity: disabled || loading ? 0.6 : 1,
        transition: 'all 0.2s ease',
      }}
    >
      {loading ? <Loader2 style={{ width: 14, height: 14, animation: 'spin 1s linear infinite' }} /> : icon}
      {children}
    </button>
  )
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Incident Creation Modal
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

function CreateIncidentModal({ isOpen, onClose }: { isOpen: boolean; onClose: () => void }) {
  const { createIncident, isCreating } = useKentaurusIncidentsStore()
  const [formData, setFormData] = useState({
    title: '',
    description: '',
    impact: '2' as '1' | '2' | '3',
    urgency: '2' as '1' | '2' | '3',
    environment: '',
    affectedServices: '',
  })

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    const success = await createIncident(formData)
    if (success) {
      onClose()
      setFormData({
        title: '',
        description: '',
        impact: '2',
        urgency: '2',
        environment: '',
        affectedServices: '',
      })
    }
  }

  if (!isOpen) return null

  return (
    <div style={{
      position: 'fixed',
      top: 0,
      left: 0,
      right: 0,
      bottom: 0,
      background: 'rgba(0, 0, 0, 0.5)',
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
      zIndex: 1000,
      padding: 16,
    }} onClick={onClose}>
      <motion.div
        initial={{ opacity: 0, scale: 0.95 }}
        animate={{ opacity: 1, scale: 1 }}
        exit={{ opacity: 0, scale: 0.95 }}
        onClick={(e) => e.stopPropagation()}
        style={{
          background: apple.secondaryBackground,
          borderRadius: apple.radius.xl,
          padding: 24,
          maxWidth: 600,
          width: '100%',
          maxHeight: '90vh',
          overflow: 'auto',
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 24 }}>
          <h2 style={{ fontSize: 20, fontWeight: 600, color: apple.label, margin: 0 }}>Create New Incident</h2>
          <button onClick={onClose} style={{ background: 'none', border: 'none', cursor: 'pointer' }}>
            <X style={{ width: 20, height: 20, color: apple.tertiaryLabel }} />
          </button>
        </div>

        <form onSubmit={handleSubmit}>
          <div style={{ marginBottom: 16 }}>
            <label style={{ display: 'block', fontSize: 13, fontWeight: 500, color: apple.label, marginBottom: 6 }}>
              Title <span style={{ color: apple.red }}>*</span>
            </label>
            <input
              type="text"
              required
              value={formData.title}
              onChange={(e) => setFormData({ ...formData, title: e.target.value })}
              style={{
                width: '100%',
                padding: '10px 12px',
                borderRadius: apple.radius.md,
                border: `0.5px solid ${apple.separator}`,
                background: apple.fill,
                fontSize: 15,
                color: apple.label,
                outline: 'none',
              }}
              placeholder="Brief description of the incident"
            />
          </div>

          <div style={{ marginBottom: 16 }}>
            <label style={{ display: 'block', fontSize: 13, fontWeight: 500, color: apple.label, marginBottom: 6 }}>
              Description <span style={{ color: apple.red }}>*</span>
            </label>
            <textarea
              required
              value={formData.description}
              onChange={(e) => setFormData({ ...formData, description: e.target.value })}
              rows={4}
              style={{
                width: '100%',
                padding: '10px 12px',
                borderRadius: apple.radius.md,
                border: `0.5px solid ${apple.separator}`,
                background: apple.fill,
                fontSize: 15,
                color: apple.label,
                outline: 'none',
                resize: 'vertical',
                fontFamily: 'inherit',
              }}
              placeholder="Detailed description of the incident..."
            />
          </div>

          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16, marginBottom: 16 }}>
            <div>
              <label style={{ display: 'block', fontSize: 13, fontWeight: 500, color: apple.label, marginBottom: 6 }}>
                Impact <span style={{ color: apple.red }}>*</span>
              </label>
              <select
                value={formData.impact}
                onChange={(e) => setFormData({ ...formData, impact: e.target.value as '1' | '2' | '3' })}
                style={{
                  width: '100%',
                  padding: '10px 12px',
                  borderRadius: apple.radius.md,
                  border: `0.5px solid ${apple.separator}`,
                  background: apple.fill,
                  fontSize: 15,
                  color: apple.label,
                  outline: 'none',
                  cursor: 'pointer',
                }}
              >
                <option value="1">High - Critical business impact</option>
                <option value="2">Medium - Moderate impact</option>
                <option value="3">Low - Minor impact</option>
              </select>
            </div>

            <div>
              <label style={{ display: 'block', fontSize: 13, fontWeight: 500, color: apple.label, marginBottom: 6 }}>
                Urgency <span style={{ color: apple.red }}>*</span>
              </label>
              <select
                value={formData.urgency}
                onChange={(e) => setFormData({ ...formData, urgency: e.target.value as '1' | '2' | '3' })}
                style={{
                  width: '100%',
                  padding: '10px 12px',
                  borderRadius: apple.radius.md,
                  border: `0.5px solid ${apple.separator}`,
                  background: apple.fill,
                  fontSize: 15,
                  color: apple.label,
                  outline: 'none',
                  cursor: 'pointer',
                }}
              >
                <option value="1">High - Immediate action required</option>
                <option value="2">Medium - Action required soon</option>
                <option value="3">Low - Can wait</option>
              </select>
            </div>
          </div>

          <div style={{ marginBottom: 16 }}>
            <label style={{ display: 'block', fontSize: 13, fontWeight: 500, color: apple.label, marginBottom: 6 }}>
              Environment
            </label>
            <input
              type="text"
              value={formData.environment}
              onChange={(e) => setFormData({ ...formData, environment: e.target.value })}
              style={{
                width: '100%',
                padding: '10px 12px',
                borderRadius: apple.radius.md,
                border: `0.5px solid ${apple.separator}`,
                background: apple.fill,
                fontSize: 15,
                color: apple.label,
                outline: 'none',
              }}
              placeholder="Production, Staging, etc."
            />
          </div>

          <div style={{ marginBottom: 24 }}>
            <label style={{ display: 'block', fontSize: 13, fontWeight: 500, color: apple.label, marginBottom: 6 }}>
              Affected Services
            </label>
            <input
              type="text"
              value={formData.affectedServices}
              onChange={(e) => setFormData({ ...formData, affectedServices: e.target.value })}
              style={{
                width: '100%',
                padding: '10px 12px',
                borderRadius: apple.radius.md,
                border: `0.5px solid ${apple.separator}`,
                background: apple.fill,
                fontSize: 15,
                color: apple.label,
                outline: 'none',
              }}
              placeholder="API, Database, etc."
            />
          </div>

          <div style={{ display: 'flex', gap: 12, justifyContent: 'flex-end' }}>
            <AppleButton variant="secondary" onClick={onClose} disabled={isCreating}>
              Cancel
            </AppleButton>
            <button type="submit" disabled={isCreating} style={{ display: "flex", alignItems: "center", gap: 6, padding: "10px 16px", borderRadius: apple.radius.sm, border: "none", background: apple.blue, color: "#fff", fontSize: 14, fontWeight: 500, cursor: isCreating ? "not-allowed" : "pointer", opacity: isCreating ? 0.6 : 1, transition: "all 0.2s ease" }}>{isCreating ? <Loader2 style={{ width: 16, height: 16, animation: "spin 1s linear infinite" }} /> : <Plus style={{ width: 16, height: 16 }} />}
              Create Incident
            </button>
          </div>
        </form>
      </motion.div>
    </div>
  )
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Incident Detail Modal
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

function IncidentDetailModal({
  incident,
  onClose,
  onUpdate,
  onAcknowledge,
  onReopen,
}: {
  incident: KentaurusIncident | null
  onClose: () => void
  onUpdate?: (ticketId: string, fields: Record<string, string>) => Promise<boolean>
  onAcknowledge?: (ticketId: string) => Promise<boolean>
  onReopen?: (number: string, comment: string) => Promise<boolean>
}) {
  const [timelineEvents, setTimelineEvents] = useState<any[]>([])
  const [loadingTimeline, setLoadingTimeline] = useState(false)
  const [actionPanel, setActionPanel] = useState<'none' | 'update' | 'reopen'>('none')
  const [actionBusy, setActionBusy] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)
  const [updateFields, setUpdateFields] = useState({
    ticketStatus: '',
    workLog: '',
    additionalComments: '',
    resolutionSummary: '',
  })
  const [reopenComment, setReopenComment] = useState('')

  useEffect(() => {
    if (!incident) return
    setLoadingTimeline(true)
    incidentsApi.getTimeline(incident.id)
      .then(r => {
        const events = r.data?.data ?? r.data ?? []
        setTimelineEvents(Array.isArray(events) ? events : [])
      })
      .catch(() => setTimelineEvents([]))
      .finally(() => setLoadingTimeline(false))
  }, [incident?.id])

  if (!incident) return null

  const formatDateTime = (timestamp: string) => {
    return new Date(timestamp).toLocaleString('en-US', {
      month: 'short',
      day: 'numeric',
      year: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    })
  }

  const calculateDuration = (start: string, end?: string) => {
    const diff = (end ? new Date(end) : new Date()).getTime() - new Date(start).getTime()
    const hours = Math.floor(diff / 3600000)
    const minutes = Math.floor((diff % 3600000) / 60000)
    if (hours > 0) return `${hours}h ${minutes}m`
    return `${minutes}m`
  }

  const getImpactLabel = (level: string) => {
    const map: Record<string, string> = { '1': 'High', '2': 'Medium', '3': 'Low' }
    return map[level] || level
  }

  const eventColor = (type: string) => {
    if (type === 'auto_created' || type === 'created') return apple.blue
    if (type === 'alert_added') return apple.orange
    if (type === 'acknowledged') return apple.yellow
    if (type === 'resolved') return apple.green
    if (type === 'ai_analysis' || type === 'rca_started') return '#AF52DE'
    return apple.gray
  }

  const alertCount = (incident.alert_ids?.length ?? (incident as any).related_alerts?.length) || 0
  const blastRadius: any[] = (incident as any).blast_radius ?? []

  return (
    <div style={{
      position: 'fixed',
      top: 0, left: 0, right: 0, bottom: 0,
      background: 'rgba(0, 0, 0, 0.5)',
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
      zIndex: 1000,
      padding: 16,
    }} onClick={onClose}>
      <motion.div
        initial={{ opacity: 0, scale: 0.95 }}
        animate={{ opacity: 1, scale: 1 }}
        exit={{ opacity: 0, scale: 0.95 }}
        onClick={(e) => e.stopPropagation()}
        style={{
          background: apple.secondaryBackground,
          borderRadius: apple.radius.xl,
          padding: 24,
          maxWidth: 800,
          width: '100%',
          maxHeight: '90vh',
          overflow: 'auto',
        }}
      >
        {/* Header */}
        <div style={{ display: 'flex', alignItems: 'start', justifyContent: 'space-between', marginBottom: 24 }}>
          <div>
            <div style={{ fontSize: 11, fontWeight: 600, color: apple.tertiaryLabel, marginBottom: 4, textTransform: 'uppercase', letterSpacing: '0.5px' }}>
              {incident.incident_number}
              {(incident as any).auto_created && (
                <span style={{ marginLeft: 6, color: apple.blue }}>· Auto-created</span>
              )}
            </div>
            <h2 style={{ fontSize: 20, fontWeight: 600, color: apple.label, margin: '0 0 12px' }}>
              {incident.title}
            </h2>
            <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
              <span style={{ fontSize: 11, fontWeight: 600, padding: '3px 8px', borderRadius: 5, background: `${apple.red}20`, color: apple.red, textTransform: 'uppercase' }}>
                {incident.severity}
              </span>
              <span style={{ fontSize: 11, fontWeight: 600, padding: '3px 8px', borderRadius: 5, background: `${apple.orange}20`, color: apple.orange, textTransform: 'uppercase' }}>
                {incident.status}
              </span>
              {alertCount > 0 && (
                <span style={{ fontSize: 11, fontWeight: 600, padding: '3px 8px', borderRadius: 5, background: `${apple.blue}15`, color: apple.blue }}>
                  {alertCount} alert{alertCount !== 1 ? 's' : ''}
                </span>
              )}
            </div>
          </div>
          <button onClick={onClose} style={{ background: 'none', border: 'none', cursor: 'pointer', padding: 4 }}>
            <X style={{ width: 20, height: 20, color: apple.tertiaryLabel }} />
          </button>
        </div>

        {/* Details Grid */}
        <div style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fit, minmax(160px, 1fr))',
          gap: 12,
          padding: 16,
          background: apple.tertiaryFill,
          borderRadius: apple.radius.md,
          marginBottom: 20,
        }}>
          <div>
            <div style={{ fontSize: 11, color: apple.tertiaryLabel, marginBottom: 4 }}>Impact</div>
            <div style={{ fontSize: 14, fontWeight: 500, color: apple.label }}>{getImpactLabel(incident.impact_level)}</div>
          </div>
          <div>
            <div style={{ fontSize: 11, color: apple.tertiaryLabel, marginBottom: 4 }}>Urgency</div>
            <div style={{ fontSize: 14, fontWeight: 500, color: apple.label }}>{getImpactLabel(incident.urgency_level)}</div>
          </div>
          <div>
            <div style={{ fontSize: 11, color: apple.tertiaryLabel, marginBottom: 4 }}>Priority</div>
            <div style={{ fontSize: 14, fontWeight: 500, color: apple.label }}>{incident.priority || 'N/A'}</div>
          </div>
          <div>
            <div style={{ fontSize: 11, color: apple.tertiaryLabel, marginBottom: 4 }}>Assigned To</div>
            <div style={{ fontSize: 14, fontWeight: 500, color: apple.label }}>{incident.assigned_to_name || 'Unassigned'}</div>
          </div>
          {(incident as any).correlation_method && (
            <div>
              <div style={{ fontSize: 11, color: apple.tertiaryLabel, marginBottom: 4 }}>Correlation</div>
              <div style={{ fontSize: 13, fontWeight: 500, color: apple.purple }}>{(incident as any).correlation_method}</div>
            </div>
          )}
          <div>
            <div style={{ fontSize: 11, color: apple.tertiaryLabel, marginBottom: 4 }}>Duration</div>
            <div style={{ fontSize: 14, fontWeight: 500, color: apple.label }}>
              {calculateDuration(incident.opened_at, incident.resolved_at)}
            </div>
          </div>
        </div>

        {/* Description */}
        {incident.description && (
          <div style={{ marginBottom: 20 }}>
            <h3 style={{ fontSize: 15, fontWeight: 600, color: apple.label, marginBottom: 8 }}>Description</h3>
            <p style={{ fontSize: 14, color: apple.secondaryLabel, lineHeight: 1.5, margin: 0 }}>
              {incident.description}
            </p>
          </div>
        )}

        {/* Timeline */}
        <div style={{ marginBottom: 20 }}>
          <h3 style={{ fontSize: 15, fontWeight: 600, color: apple.label, marginBottom: 12 }}>Timeline</h3>
          <div style={{ position: 'relative' }}>
            {/* vertical line */}
            <div style={{ position: 'absolute', left: 3, top: 8, bottom: 8, width: 2, background: apple.separator, borderRadius: 1 }} />
            <div style={{ display: 'flex', flexDirection: 'column', gap: 0 }}>
              {/* Opened */}
              <div style={{ display: 'flex', alignItems: 'flex-start', gap: 12, paddingBottom: 16 }}>
                <div style={{ width: 8, height: 8, borderRadius: '50%', background: apple.blue, flexShrink: 0, marginTop: 3, zIndex: 1 }} />
                <div style={{ flex: 1 }}>
                  <div style={{ fontSize: 13, fontWeight: 500, color: apple.label }}>Incident Opened</div>
                  <div style={{ fontSize: 12, color: apple.tertiaryLabel }}>{formatDateTime(incident.opened_at)}</div>
                </div>
              </div>

              {/* DB timeline events */}
              {loadingTimeline && (
                <div style={{ display: 'flex', alignItems: 'flex-start', gap: 12, paddingBottom: 16 }}>
                  <div style={{ width: 8, height: 8, borderRadius: '50%', background: apple.gray, flexShrink: 0, marginTop: 3, zIndex: 1 }} />
                  <div style={{ fontSize: 12, color: apple.tertiaryLabel }}>Loading events…</div>
                </div>
              )}
              {timelineEvents.map((ev, i) => (
                <div key={ev.id ?? i} style={{ display: 'flex', alignItems: 'flex-start', gap: 12, paddingBottom: 16 }}>
                  <div style={{ width: 8, height: 8, borderRadius: '50%', background: eventColor(ev.event_type), flexShrink: 0, marginTop: 3, zIndex: 1 }} />
                  <div style={{ flex: 1 }}>
                    <div style={{ fontSize: 13, fontWeight: 500, color: apple.label }}>{ev.title}</div>
                    {ev.description && (
                      <div style={{ fontSize: 12, color: apple.secondaryLabel, marginTop: 2 }}>{ev.description}</div>
                    )}
                    <div style={{ fontSize: 11, color: apple.tertiaryLabel, marginTop: 2 }}>
                      {formatDateTime(ev.created_at)}
                      {ev.user_name && ` · ${ev.user_name}`}
                    </div>
                  </div>
                </div>
              ))}

              {incident.acknowledged_at && (
                <div style={{ display: 'flex', alignItems: 'flex-start', gap: 12, paddingBottom: 16 }}>
                  <div style={{ width: 8, height: 8, borderRadius: '50%', background: apple.yellow, flexShrink: 0, marginTop: 3, zIndex: 1 }} />
                  <div style={{ flex: 1 }}>
                    <div style={{ fontSize: 13, fontWeight: 500, color: apple.label }}>Acknowledged</div>
                    <div style={{ fontSize: 12, color: apple.tertiaryLabel }}>{formatDateTime(incident.acknowledged_at)}</div>
                  </div>
                </div>
              )}

              {incident.resolved_at ? (
                <div style={{ display: 'flex', alignItems: 'flex-start', gap: 12 }}>
                  <div style={{ width: 8, height: 8, borderRadius: '50%', background: apple.green, flexShrink: 0, marginTop: 3, zIndex: 1 }} />
                  <div style={{ flex: 1 }}>
                    <div style={{ fontSize: 13, fontWeight: 500, color: apple.green }}>Resolved</div>
                    <div style={{ fontSize: 12, color: apple.tertiaryLabel }}>
                      {formatDateTime(incident.resolved_at)} · Total: {calculateDuration(incident.opened_at, incident.resolved_at)}
                    </div>
                  </div>
                </div>
              ) : (
                <div style={{ display: 'flex', alignItems: 'flex-start', gap: 12 }}>
                  <div style={{ width: 8, height: 8, borderRadius: '50%', background: apple.orange, flexShrink: 0, marginTop: 3, zIndex: 1 }} />
                  <div style={{ flex: 1 }}>
                    <div style={{ fontSize: 13, fontWeight: 500, color: apple.orange }}>Ongoing · {calculateDuration(incident.opened_at)}</div>
                  </div>
                </div>
              )}
            </div>
          </div>
        </div>

        {/* Blast Radius */}
        {blastRadius.length > 0 && (
          <div style={{ marginBottom: 20 }}>
            <h3 style={{ fontSize: 15, fontWeight: 600, color: apple.label, marginBottom: 8 }}>Blast Radius</h3>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
              {blastRadius.map((node: any, i: number) => (
                <span key={i} style={{
                  fontSize: 11, padding: '3px 8px', borderRadius: 5,
                  background: node.impact === 'direct' ? `${apple.red}15` : `${apple.orange}15`,
                  color: node.impact === 'direct' ? apple.red : apple.orange,
                  border: `0.5px solid ${node.impact === 'direct' ? apple.red : apple.orange}40`,
                }}>
                  {node.node_type}: {node.node_id}
                </span>
              ))}
            </div>
          </div>
        )}

        {/* Additional Info */}
        {(incident.environment || incident.affected_services || incident.assignment_group_name) && (
          <div style={{ marginBottom: 20 }}>
            <h3 style={{ fontSize: 15, fontWeight: 600, color: apple.label, marginBottom: 8 }}>Additional Information</h3>
            <div style={{ display: 'grid', gridTemplateColumns: '120px 1fr', gap: 8, fontSize: 13 }}>
              {incident.environment && (
                <>
                  <div style={{ color: apple.tertiaryLabel }}>Environment:</div>
                  <div style={{ color: apple.label, fontWeight: 500 }}>{incident.environment}</div>
                </>
              )}
              {incident.affected_services && (
                <>
                  <div style={{ color: apple.tertiaryLabel }}>Services:</div>
                  <div style={{ color: apple.label, fontWeight: 500 }}>{incident.affected_services}</div>
                </>
              )}
              {incident.assignment_group_name && (
                <>
                  <div style={{ color: apple.tertiaryLabel }}>Team:</div>
                  <div style={{ color: apple.label, fontWeight: 500 }}>{incident.assignment_group_name}</div>
                </>
              )}
            </div>
          </div>
        )}

        {/* ── HCL Actions ─────────────────────────────────────────────────── */}
        {(onAcknowledge || onUpdate || onReopen) && (
          <div style={{
            borderTop: `0.5px solid ${apple.separator}`,
            paddingTop: 20,
            marginTop: 4,
          }}>
            <h3 style={{ fontSize: 15, fontWeight: 600, color: apple.label, marginBottom: 12 }}>Actions</h3>

            {actionError && (
              <div style={{
                padding: '8px 12px', marginBottom: 12, borderRadius: 8,
                background: `${apple.red}12`, color: apple.red, fontSize: 13,
              }}>
                {actionError}
              </div>
            )}

            {/* Open/Investigating: Acknowledge + Update */}
            {(incident.status === 'open' || incident.status === 'investigating') && (
              <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap', marginBottom: actionPanel !== 'none' ? 16 : 0 }}>
                {onAcknowledge && incident.status === 'open' && (
                  <button
                    disabled={actionBusy}
                    onClick={async () => {
                      setActionBusy(true); setActionError(null)
                      const ok = await onAcknowledge(incident.sys_id)
                      setActionBusy(false)
                      if (ok) onClose()
                      else setActionError('Failed to acknowledge — try again')
                    }}
                    style={{
                      padding: '8px 16px', borderRadius: 8, border: 'none', fontSize: 13, fontWeight: 500,
                      background: `${apple.yellow}20`, color: apple.yellow, cursor: actionBusy ? 'not-allowed' : 'pointer',
                      opacity: actionBusy ? 0.6 : 1,
                    }}
                  >
                    {actionBusy ? 'Working…' : 'Acknowledge'}
                  </button>
                )}
                {onUpdate && (
                  <button
                    onClick={() => setActionPanel(p => p === 'update' ? 'none' : 'update')}
                    style={{
                      padding: '8px 16px', borderRadius: 8, border: 'none', fontSize: 13, fontWeight: 500,
                      background: `${apple.blue}15`, color: apple.blue, cursor: 'pointer',
                    }}
                  >
                    Update
                  </button>
                )}
              </div>
            )}

            {/* Resolved/Closed: Reopen */}
            {(incident.status === 'resolved' || incident.state?.toLowerCase() === 'closed') && onReopen && (
              <div style={{ marginBottom: actionPanel !== 'none' ? 16 : 0 }}>
                <button
                  onClick={() => setActionPanel(p => p === 'reopen' ? 'none' : 'reopen')}
                  style={{
                    padding: '8px 16px', borderRadius: 8, border: 'none', fontSize: 13, fontWeight: 500,
                    background: `${apple.orange}15`, color: apple.orange, cursor: 'pointer',
                  }}
                >
                  Reopen Incident
                </button>
              </div>
            )}

            {/* Update panel */}
            {actionPanel === 'update' && (
              <div style={{ background: apple.tertiaryFill, borderRadius: 10, padding: 16, display: 'flex', flexDirection: 'column', gap: 12 }}>
                <div>
                  <label style={{ display: 'block', fontSize: 12, fontWeight: 500, color: apple.secondaryLabel, marginBottom: 4 }}>Status</label>
                  <select
                    value={updateFields.ticketStatus}
                    onChange={e => setUpdateFields(f => ({ ...f, ticketStatus: e.target.value }))}
                    style={{
                      width: '100%', padding: '8px 10px', borderRadius: 8, border: `0.5px solid ${apple.separator}`,
                      background: apple.fill, fontSize: 14, color: apple.label, outline: 'none',
                    }}
                  >
                    <option value="">— no change —</option>
                    <option value="In Progress">In Progress</option>
                    <option value="On Hold">On Hold</option>
                    <option value="Resolved">Resolved</option>
                  </select>
                </div>
                <div>
                  <label style={{ display: 'block', fontSize: 12, fontWeight: 500, color: apple.secondaryLabel, marginBottom: 4 }}>Work Notes</label>
                  <textarea
                    rows={3}
                    value={updateFields.workLog}
                    onChange={e => setUpdateFields(f => ({ ...f, workLog: e.target.value }))}
                    placeholder="Internal work notes…"
                    style={{
                      width: '100%', padding: '8px 10px', borderRadius: 8, border: `0.5px solid ${apple.separator}`,
                      background: apple.fill, fontSize: 14, color: apple.label, outline: 'none',
                      resize: 'vertical', fontFamily: 'inherit',
                    }}
                  />
                </div>
                <div>
                  <label style={{ display: 'block', fontSize: 12, fontWeight: 500, color: apple.secondaryLabel, marginBottom: 4 }}>Additional Comments</label>
                  <textarea
                    rows={2}
                    value={updateFields.additionalComments}
                    onChange={e => setUpdateFields(f => ({ ...f, additionalComments: e.target.value }))}
                    placeholder="Comments visible to caller…"
                    style={{
                      width: '100%', padding: '8px 10px', borderRadius: 8, border: `0.5px solid ${apple.separator}`,
                      background: apple.fill, fontSize: 14, color: apple.label, outline: 'none',
                      resize: 'vertical', fontFamily: 'inherit',
                    }}
                  />
                </div>
                {(updateFields.ticketStatus === 'Resolved') && (
                  <div>
                    <label style={{ display: 'block', fontSize: 12, fontWeight: 500, color: apple.secondaryLabel, marginBottom: 4 }}>Resolution Summary</label>
                    <textarea
                      rows={2}
                      value={updateFields.resolutionSummary}
                      onChange={e => setUpdateFields(f => ({ ...f, resolutionSummary: e.target.value }))}
                      placeholder="What was done to resolve this…"
                      style={{
                        width: '100%', padding: '8px 10px', borderRadius: 8, border: `0.5px solid ${apple.separator}`,
                        background: apple.fill, fontSize: 14, color: apple.label, outline: 'none',
                        resize: 'vertical', fontFamily: 'inherit',
                      }}
                    />
                  </div>
                )}
                <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
                  <button
                    onClick={() => { setActionPanel('none'); setActionError(null) }}
                    style={{
                      padding: '7px 14px', borderRadius: 8, border: 'none', fontSize: 13,
                      background: apple.fill, color: apple.secondaryLabel, cursor: 'pointer',
                    }}
                  >Cancel</button>
                  <button
                    disabled={actionBusy}
                    onClick={async () => {
                      setActionBusy(true); setActionError(null)
                      const payload: Record<string, string> = {}
                      if (updateFields.ticketStatus) payload.ticketStatus = updateFields.ticketStatus
                      if (updateFields.workLog) payload.workLog = updateFields.workLog
                      if (updateFields.additionalComments) payload.additionalComments = updateFields.additionalComments
                      if (updateFields.resolutionSummary) payload.resolutionSummary = updateFields.resolutionSummary
                      const ok = await onUpdate!(incident.sys_id, payload)
                      setActionBusy(false)
                      if (ok) { setActionPanel('none'); onClose() }
                      else setActionError('Update failed — try again')
                    }}
                    style={{
                      padding: '7px 14px', borderRadius: 8, border: 'none', fontSize: 13, fontWeight: 500,
                      background: apple.blue, color: '#fff', cursor: actionBusy ? 'not-allowed' : 'pointer',
                      opacity: actionBusy ? 0.6 : 1,
                    }}
                  >{actionBusy ? 'Saving…' : 'Save Update'}</button>
                </div>
              </div>
            )}

            {/* Reopen panel */}
            {actionPanel === 'reopen' && (
              <div style={{ background: apple.tertiaryFill, borderRadius: 10, padding: 16, display: 'flex', flexDirection: 'column', gap: 12 }}>
                <div>
                  <label style={{ display: 'block', fontSize: 12, fontWeight: 500, color: apple.secondaryLabel, marginBottom: 4 }}>
                    Reason for reopening <span style={{ color: apple.red }}>*</span>
                  </label>
                  <textarea
                    rows={3}
                    value={reopenComment}
                    onChange={e => setReopenComment(e.target.value)}
                    placeholder="Explain why this incident needs to be reopened…"
                    style={{
                      width: '100%', padding: '8px 10px', borderRadius: 8, border: `0.5px solid ${apple.separator}`,
                      background: apple.fill, fontSize: 14, color: apple.label, outline: 'none',
                      resize: 'vertical', fontFamily: 'inherit',
                    }}
                  />
                </div>
                <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
                  <button
                    onClick={() => { setActionPanel('none'); setActionError(null) }}
                    style={{
                      padding: '7px 14px', borderRadius: 8, border: 'none', fontSize: 13,
                      background: apple.fill, color: apple.secondaryLabel, cursor: 'pointer',
                    }}
                  >Cancel</button>
                  <button
                    disabled={actionBusy || !reopenComment.trim()}
                    onClick={async () => {
                      setActionBusy(true); setActionError(null)
                      const ok = await onReopen!(incident.incident_number, reopenComment)
                      setActionBusy(false)
                      if (ok) { setActionPanel('none'); onClose() }
                      else setActionError('Reopen failed — try again')
                    }}
                    style={{
                      padding: '7px 14px', borderRadius: 8, border: 'none', fontSize: 13, fontWeight: 500,
                      background: apple.orange, color: '#fff',
                      cursor: (actionBusy || !reopenComment.trim()) ? 'not-allowed' : 'pointer',
                      opacity: (actionBusy || !reopenComment.trim()) ? 0.6 : 1,
                    }}
                  >{actionBusy ? 'Reopening…' : 'Confirm Reopen'}</button>
                </div>
              </div>
            )}
          </div>
        )}
      </motion.div>
    </div>
  )
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Incident Card Component
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

function IncidentCard({ incident, onClick, onAcknowledge }: {
  incident: KentaurusIncident
  onClick?: (incident: KentaurusIncident) => void
  onAcknowledge?: (incident: KentaurusIncident) => void
}) {
  const getSeverityColor = (severity: string) => {
    switch (severity) {
      case 'critical': return apple.red
      case 'high': return apple.orange
      case 'medium': return apple.yellow
      case 'low': return apple.blue
      default: return apple.gray
    }
  }

  const getStatusColor = (status: string) => {
    switch (status) {
      case 'open': return apple.red
      case 'investigating': return apple.orange
      case 'resolved': return apple.green
      default: return apple.gray
    }
  }

  const formatTime = (timestamp: string) => {
    const date = new Date(timestamp)
    const now = new Date()
    const diff = now.getTime() - date.getTime()
    const minutes = Math.floor(diff / 60000)
    const hours = Math.floor(minutes / 60)
    const days = Math.floor(hours / 24)
    
    if (days > 0) return `${days}d ago`
    if (hours > 0) return `${hours}h ago`
    if (minutes > 0) return `${minutes}m ago`
    return 'Just now'
  }

  const formatDateTime = (timestamp: string) => {
    return new Date(timestamp).toLocaleString('en-US', {
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit'
    })
  }

  const calculateDuration = (start: string, end?: string) => {
    const diff = (end ? new Date(end) : new Date()).getTime() - new Date(start).getTime()
    const hours = Math.floor(diff / 3600000)
    const minutes = Math.floor((diff % 3600000) / 60000)
    
    if (hours > 0) return `${hours}h ${minutes}m`
    return `${minutes}m`
  }

  return (
    <motion.div
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      whileHover={{ y: -2 }}
      onClick={() => onClick?.(incident)}
      style={{
        background: apple.secondaryBackground,
        border: `0.5px solid ${apple.separator}`,
        borderLeft: `4px solid ${getSeverityColor(incident.severity)}`,
        borderRadius: apple.radius.lg,
        padding: 20,
        cursor: onClick ? 'pointer' : 'default',
        transition: 'all 0.2s ease',
        marginBottom: 12,
      }}
      onMouseEnter={(e) => {
        if (onClick) {
          e.currentTarget.style.boxShadow = '0 8px 32px rgba(0,0,0,0.12)'
          e.currentTarget.style.borderColor = getSeverityColor(incident.severity)
        }
      }}
      onMouseLeave={(e) => {
        e.currentTarget.style.boxShadow = 'none'
        e.currentTarget.style.borderColor = apple.separator
      }}
    >
      {/* Header */}
      <div style={{ display: 'flex', alignItems: 'start', justifyContent: 'space-between', marginBottom: 12 }}>
        <div style={{ flex: 1 }}>
          <div style={{
            fontSize: 11,
            fontWeight: 600,
            color: apple.tertiaryLabel,
            marginBottom: 4,
            textTransform: 'uppercase',
            letterSpacing: '0.5px',
          }}>
            INCIDENT #{incident.incident_number}
          </div>
          <h3 style={{
            fontSize: 17,
            fontWeight: 600,
            color: apple.label,
            margin: '0 0 8px',
            lineHeight: 1.3,
          }}>
            {incident.title}
          </h3>
        </div>
        <div style={{ 
          fontSize: 12, 
          color: apple.tertiaryLabel,
          textAlign: 'right',
          flexShrink: 0,
          marginLeft: 12,
        }}>
          {formatTime(incident.created_at)}
        </div>
      </div>

      {/* Badges */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 12, flexWrap: 'wrap' }}>
        <span style={{
          display: 'inline-flex',
          alignItems: 'center',
          gap: 4,
          fontSize: 11,
          fontWeight: 600,
          padding: '3px 8px',
          borderRadius: 5,
          textTransform: 'uppercase',
          background: `${getSeverityColor(incident.severity)}20`,
          color: getSeverityColor(incident.severity),
        }}>
          <AlertTriangle style={{ width: 10, height: 10 }} />
          {incident.severity}
        </span>
        <span style={{
          display: 'inline-flex',
          alignItems: 'center',
          gap: 4,
          fontSize: 11,
          fontWeight: 600,
          padding: '3px 8px',
          borderRadius: 5,
          textTransform: 'uppercase',
          background: `${getStatusColor(incident.status)}20`,
          color: getStatusColor(incident.status),
        }}>
          {incident.status === 'open' ? <XCircle style={{ width: 10, height: 10 }} /> :
           incident.status === 'investigating' ? <Play style={{ width: 10, height: 10 }} /> :
           <CheckCircle style={{ width: 10, height: 10 }} />}
          {incident.status}
        </span>
        {incident.assigned_to_name && (
          <span style={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: 4,
            fontSize: 11,
            fontWeight: 500,
            padding: '3px 8px',
            borderRadius: 5,
            background: `${apple.blue}20`,
            color: apple.blue,
          }}>
            <User style={{ width: 10, height: 10 }} />
            {incident.assigned_to_name}
          </span>
        )}
        {incident.affected_services && (
          <span style={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: 4,
            fontSize: 11,
            fontWeight: 500,
            padding: '3px 8px',
            borderRadius: 5,
            background: apple.fill,
            color: apple.secondaryLabel,
          }}>
            <Server style={{ width: 10, height: 10 }} />
            {incident.affected_services}
          </span>
        )}
      </div>

      {/* Description */}
      {incident.description && (
        <p style={{
          fontSize: 14,
          color: apple.secondaryLabel,
          lineHeight: 1.4,
          margin: '0 0 16px',
          display: '-webkit-box',
          WebkitLineClamp: 2,
          WebkitBoxOrient: 'vertical',
          overflow: 'hidden',
        }}>
          {incident.description}
        </p>
      )}

      {/* Timeline */}
      <div style={{
        display: 'grid',
        gridTemplateColumns: 'repeat(auto-fit, minmax(140px, 1fr))',
        gap: 12,
        padding: 12,
        background: apple.tertiaryFill,
        borderRadius: apple.radius.sm,
      }}>
        <div>
          <div style={{ fontSize: 11, color: apple.tertiaryLabel, marginBottom: 2 }}>Started</div>
          <div style={{ fontSize: 13, fontWeight: 500, color: apple.label }}>
            {formatDateTime(incident.started_at || incident.created_at)}
          </div>
        </div>
        {incident.resolved_at ? (
          <>
            <div>
              <div style={{ fontSize: 11, color: apple.tertiaryLabel, marginBottom: 2 }}>Resolved</div>
              <div style={{ fontSize: 13, fontWeight: 500, color: apple.label }}>
                {formatDateTime(incident.resolved_at)}
              </div>
            </div>
            <div>
              <div style={{ fontSize: 11, color: apple.tertiaryLabel, marginBottom: 2 }}>Duration</div>
              <div style={{ fontSize: 13, fontWeight: 500, color: apple.label }}>
                {calculateDuration(incident.started_at, incident.resolved_at)}
              </div>
            </div>
          </>
        ) : (
          <div>
            <div style={{ fontSize: 11, color: apple.tertiaryLabel, marginBottom: 2 }}>Duration</div>
            <div style={{ fontSize: 13, fontWeight: 500, color: apple.label }}>
              {calculateDuration(incident.started_at)}
            </div>
          </div>
        )}
      </div>

      {/* Quick Actions */}
      {onAcknowledge && incident.status === 'open' && (
        <div style={{ display: 'flex', gap: 8, marginTop: 12, justifyContent: 'flex-end' }}>
          <button
            onClick={(e) => { e.stopPropagation(); onAcknowledge(incident) }}
            style={{
              padding: '5px 12px', borderRadius: 8, border: 'none', fontSize: 12, fontWeight: 500,
              background: `${apple.yellow}20`, color: apple.yellow, cursor: 'pointer',
            }}
          >
            Acknowledge
          </button>
        </div>
      )}
    </motion.div>
  )
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Local Incident Detail Modal (for pipeline-created incidents with RCA/blast radius)
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

const RCA_WS_HOST = `${window.location.protocol === 'https:' ? 'wss' : 'ws'}://${window.location.host}`

function RCAStatusBadge({ status, confidence }: { status: string; confidence?: number }) {
  const config: Record<string, { color: string; icon: React.ReactNode; label: string }> = {
    none:         { color: apple.gray,   icon: <Clock style={{ width: 10, height: 10 }} />,     label: 'No RCA' },
    queued:       { color: apple.blue,   icon: <Loader2 style={{ width: 10, height: 10 }} />,   label: 'RCA Queued' },
    investigating:{ color: apple.orange, icon: <Radio style={{ width: 10, height: 10 }} />,     label: 'Investigating' },
    completed:    { color: apple.green,  icon: <CheckCircle style={{ width: 10, height: 10 }} />,label: 'RCA Done' },
    failed:       { color: apple.red,    icon: <XCircle style={{ width: 10, height: 10 }} />,   label: 'RCA Failed' },
  }
  const c = config[status] ?? config.none
  return (
    <span style={{
      display: 'inline-flex', alignItems: 'center', gap: 4,
      fontSize: 11, fontWeight: 600, padding: '3px 8px', borderRadius: 5,
      background: `${c.color}20`, color: c.color,
    }}>
      {c.icon}
      {c.label}
      {confidence && confidence > 0 && status === 'completed' && (
        <span style={{ opacity: 0.7 }}>{Math.round(confidence * 100)}%</span>
      )}
    </span>
  )
}

function InvestigationStreamInline({ investigationId }: { investigationId: string }) {
  const [events, setEvents] = useState<any[]>([])
  const [connected, setConnected] = useState(false)
  const [done, setDone] = useState(false)
  const wsRef = useRef<WebSocket | null>(null)
  const bottomRef = useRef<HTMLDivElement>(null)
  const retryTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const mountedRef = useRef(true)

  useEffect(() => {
    mountedRef.current = true
    return () => { mountedRef.current = false }
  }, [])

  useEffect(() => {
    if (!investigationId) return

    let retryCount = 0
    const maxRetries = 20  // ~20 retries × 30s max interval = ~10 min retry window for long investigations

    function connect() {
      if (!mountedRef.current) return
      const ws = new WebSocket(`${RCA_WS_HOST}/ws/investigations/${investigationId}`)
      wsRef.current = ws

      ws.onopen = () => {
        if (!mountedRef.current) { ws.close(); return }
        retryCount = 0
        setConnected(true)
      }
      ws.onmessage = (e) => {
        if (!mountedRef.current) return
        try {
          const ev = JSON.parse(e.data)
          if (ev.type === 'heartbeat') return
          setEvents(prev => [...prev.slice(-99), ev])
          if (ev.type === 'result' || ev.type === 'error') setDone(true)
        } catch (e) {
          console.error('[RCA stream] message parse error:', e instanceof Error ? e.message : 'unknown')
        }
      }
      ws.onerror = () => {
        // Let onclose handle retry
      }
      ws.onclose = (e) => {
        if (!mountedRef.current) return
        setConnected(false)
        // Normal close (done) or intentional close codes — don't retry
        if (e.code === 1000 || e.code === 1008 || done) {
          setDone(true)
          return
        }
        // Retry with backoff for transient failures
        if (retryCount < maxRetries) {
          retryCount++
          const delay = Math.min(1000 * retryCount, 30000)  // up to 30s between retries
          retryTimerRef.current = setTimeout(connect, delay)
        } else {
          setDone(true)
        }
      }
    }

    connect()

    return () => {
      mountedRef.current = false
      if (retryTimerRef.current) clearTimeout(retryTimerRef.current)
      const ws = wsRef.current
      wsRef.current = null
      if (ws) {
        ws.onmessage = null
        ws.onerror = null
        ws.onclose = null
        if (ws.readyState === WebSocket.OPEN) {
          ws.close(1000, 'component unmounted')
        } else if (ws.readyState === WebSocket.CONNECTING) {
          ws.onopen = () => ws.close(1000, 'component unmounted')
        }
      }
    }
  }, [investigationId])

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [events])

  const phaseColor: Record<string, string> = {
    context_gathering: apple.blue,
    hypothesis_formation: apple.purple,
    evidence_collection: apple.orange,
    root_cause_analysis: apple.red,
    remediation_planning: apple.green,
    completed: apple.green,
    failed: apple.red,
  }

  return (
    <div style={{ fontSize: 13, lineHeight: 1.5 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12 }}>
        {connected && !done && (
          <span style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, color: apple.green }}>
            <Radio style={{ width: 12, height: 12 }} /> Live
          </span>
        )}
        {done && <span style={{ fontSize: 12, color: apple.gray }}>Investigation complete</span>}
        {!connected && !done && <span style={{ fontSize: 12, color: apple.orange }}>Connecting…</span>}
      </div>
      <div style={{
        background: 'rgba(0,0,0,0.04)', borderRadius: 8, padding: 12,
        maxHeight: 280, overflowY: 'auto', fontFamily: 'monospace',
      }}>
        {events.length === 0 && (
          <div style={{ color: apple.tertiaryLabel, fontStyle: 'italic' }}>Waiting for investigation events…</div>
        )}
        {events.map((ev, i) => (
          <div key={i} style={{ marginBottom: 8, display: 'flex', gap: 8, alignItems: 'flex-start' }}>
            {ev.phase && (
              <span style={{
                fontSize: 10, fontWeight: 700, padding: '2px 6px', borderRadius: 4,
                background: `${phaseColor[ev.phase] ?? apple.gray}20`,
                color: phaseColor[ev.phase] ?? apple.gray,
                flexShrink: 0, marginTop: 1, textTransform: 'uppercase',
              }}>
                {ev.phase.replace(/_/g, ' ')}
              </span>
            )}
            <div style={{ color: apple.label, flex: 1 }}>
              {ev.type === 'thought' && <span>{ev.data}</span>}
              {ev.type === 'tool_call' && (
                <span><span style={{ color: apple.blue }}>{ev.data?.tool}</span>{ev.data?.result ? `: ${String(ev.data.result).slice(0, 120)}` : ''}</span>
              )}
              {ev.type === 'phase_change' && (
                <span style={{ fontWeight: 600, color: phaseColor[ev.phase] ?? apple.label }}>Phase: {ev.data}</span>
              )}
              {ev.type === 'result' && (
                <div>
                  {ev.data?.root_cause && (
                    <div style={{ fontWeight: 600, color: apple.green }}>Root cause: {ev.data.root_cause.summary}</div>
                  )}
                  {ev.data?.summary && <div style={{ color: apple.secondaryLabel, marginTop: 4 }}>{ev.data.summary}</div>}
                  {ev.data?.v2_scoring && (
                    <div style={{ marginTop: 6, padding: '6px 8px', borderRadius: 6, background: `${apple.purple}10`, border: `0.5px solid ${apple.purple}25` }}>
                      <div style={{ fontSize: 10, fontWeight: 700, color: apple.purple, marginBottom: 4 }}>V2 SCORING</div>
                      <div style={{ fontSize: 11, color: apple.label }}>
                        {ev.data.v2_scoring.top_hypothesis && <span>Entity: <b>{ev.data.v2_scoring.top_hypothesis}</b> · </span>}
                        Confidence: <b>{Math.round((ev.data.v2_scoring.confidence ?? 0) * 100)}%</b>
                        {ev.data.v2_scoring.domain && <span> · Domain: {ev.data.v2_scoring.domain}</span>}
                      </div>
                      {ev.data.v2_scoring.evidence_breakdown && Object.keys(ev.data.v2_scoring.evidence_breakdown).length > 0 && (
                        <div style={{ marginTop: 4, display: 'flex', flexWrap: 'wrap', gap: 4 }}>
                          {Object.entries(ev.data.v2_scoring.evidence_breakdown).map(([src, score]: [string, any]) => (
                            <span key={src} style={{ fontSize: 9, padding: '1px 6px', borderRadius: 10, background: `${apple.purple}20`, color: apple.purple }}>
                              {src}: {typeof score === 'number' ? Math.round(score * 100) + '%' : String(score)}
                            </span>
                          ))}
                        </div>
                      )}
                    </div>
                  )}
                </div>
              )}
              {ev.type === 'error' && <span style={{ color: apple.red }}>{ev.data}</span>}
            </div>
          </div>
        ))}
        <div ref={bottomRef} />
      </div>
    </div>
  )
}

// ── Floodgate RCA inline section ────────────────────────────────────────────
function FloodgateRcaSection({
  incidentId, floodgateToken, setFloodgateToken, floodgateModel, setFloodgateModel, running, onRun,
}: {
  incidentId: string
  floodgateToken: string
  setFloodgateToken: (v: string) => void
  floodgateModel: 'claude-sonnet-4-6' | 'claude-opus-4-7'
  setFloodgateModel: (v: 'claude-sonnet-4-6' | 'claude-opus-4-7') => void
  running: boolean
  onRun: () => void
}) {
  const [tokenStatus, setTokenStatus] = React.useState<'idle' | 'testing' | 'valid' | 'invalid'>('idle')
  return (
    <div style={{ textAlign: 'left', maxWidth: 420, margin: '0 auto', padding: '0 16px' }}>
      <div style={{ padding: 14, background: `${apple.orange}08`, borderRadius: apple.radius.lg, border: `0.5px solid ${apple.orange}20` }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 10 }}>
          <Sparkles style={{ width: 14, height: 14, color: apple.orange }} />
          <span style={{ fontSize: 12, fontWeight: 600, color: apple.orange }}>Run with Floodgate Claude</span>
          <span style={{ marginLeft: 'auto', fontSize: 10, color: apple.tertiaryLabel }}>optional · uses your quota</span>
        </div>

        <div style={{ display: 'flex', gap: 6, marginBottom: 10 }}>
          {(['claude-sonnet-4-6', 'claude-opus-4-7'] as const).map(m => (
            <button key={m} onClick={() => setFloodgateModel(m)}
              style={{ padding: '5px 10px', borderRadius: apple.radius.sm, border: `0.5px solid ${floodgateModel === m ? apple.orange : apple.separator}`, background: floodgateModel === m ? `${apple.orange}12` : apple.fill, color: floodgateModel === m ? apple.orange : apple.secondaryLabel, fontSize: 11, fontWeight: floodgateModel === m ? 600 : 400, cursor: 'pointer' }}>
              {m === 'claude-sonnet-4-6' ? 'Sonnet 4.6' : 'Opus 4.7'}
            </button>
          ))}
        </div>

        <div style={{ position: 'relative', marginBottom: 10 }}>
          <Key style={{ position: 'absolute', left: 10, top: '50%', transform: 'translateY(-50%)', width: 12, height: 12, color: apple.tertiaryLabel }} />
          <input
            type="password"
            value={floodgateToken}
            onChange={e => { setFloodgateToken(e.target.value); setTokenStatus('idle') }}
            placeholder="Paste your personal Floodgate OAuth token…"
            style={{ width: '100%', padding: '8px 10px 8px 30px', borderRadius: apple.radius.sm, border: `0.5px solid ${floodgateToken ? apple.orange : apple.separator}`, background: apple.fill, color: apple.label, fontSize: 12, outline: 'none', boxSizing: 'border-box' }}
          />
        </div>

        {!floodgateToken && (
          <div style={{ fontSize: 11, color: apple.secondaryLabel, marginBottom: 10, lineHeight: 1.6 }}>
            This uses your personal Floodgate API quota. Run this command in your terminal to get a short-lived token:<br />
            <code style={{ fontSize: 10, color: apple.orange, wordBreak: 'break-all' }}>
              appleconnect getToken -C hvys3fcwcteqrvw3qzkvtk86viuoqv --token-type=oauth --interactivity-type=none -E prod -G pkce -o openid,dsid,accountname,profile,groups | grep 'oauth-id-token' | awk '{`{print $2}`}'
            </code>
            <br /><span style={{ color: apple.tertiaryLabel }}>The token is not saved — paste it fresh each time you want to use Floodgate RCA.</span>
          </div>
        )}

        {floodgateToken && (
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 10 }}>
            <button
              onClick={async () => {
                setTokenStatus('testing')
                try {
                  const r = await incidentsApi.testFloodgateToken(floodgateToken)
                  setTokenStatus(r.data?.valid ? 'valid' : 'invalid')
                } catch {
                  setTokenStatus('invalid')
                }
              }}
              disabled={tokenStatus === 'testing'}
              style={{ display: 'flex', alignItems: 'center', gap: 5, padding: '5px 10px', borderRadius: apple.radius.sm, background: `${apple.orange}15`, color: apple.orange, border: `0.5px solid ${apple.orange}40`, fontSize: 11, fontWeight: 600, cursor: tokenStatus === 'testing' ? 'not-allowed' : 'pointer' }}>
              {tokenStatus === 'testing'
                ? <><span style={{ display: 'inline-block', width: 10, height: 10, border: `2px solid ${apple.orange}`, borderTopColor: 'transparent', borderRadius: '50%', animation: 'spin 0.8s linear infinite' }} /> Testing…</>
                : <><Sparkles style={{ width: 10, height: 10 }} /> Test</>
              }
            </button>
            {tokenStatus === 'valid' && <span style={{ fontSize: 11, color: apple.green, fontWeight: 600 }}>✓ Connected</span>}
            {tokenStatus === 'invalid' && <span style={{ fontSize: 11, color: apple.red, fontWeight: 600 }}>✗ Invalid or expired</span>}
          </div>
        )}

        <button onClick={onRun} disabled={running || !floodgateToken.trim()}
          style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '8px 14px', borderRadius: apple.radius.sm, background: running || !floodgateToken ? `${apple.orange}50` : apple.orange, color: '#fff', border: 'none', cursor: running || !floodgateToken ? 'not-allowed' : 'pointer', fontSize: 12, fontWeight: 600 }}>
          {running ? <Loader2 style={{ width: 12, height: 12 }} /> : <Sparkles style={{ width: 12, height: 12 }} />}
          {running ? 'Running RCA…' : 'Run Floodgate RCA'}
        </button>
      </div>
    </div>
  )
}

function LocalIncidentDetailModal({ incident, onClose }: { incident: any; onClose: () => void }) {
  const [tab, setTab] = useState<'overview' | 'rca' | 'blast_radius' | 'correlation' | 'timeline' | 'alerts' | 'postmortem' | 'remediations'>('overview')
  const [timeline, setTimeline] = useState<any[]>([])
  const [loadingTimeline, setLoadingTimeline] = useState(false)
  const [fullIncident, setFullIncident] = useState<any>(incident)
  const [rcaData, setRcaData] = useState<any>(null)
  const [loadingRca, setLoadingRca] = useState(false)
  const [oieInvestigation, setOieInvestigation] = useState<any>(null)
  const [kubesenseInvestigation, setKubesenseInvestigation] = useState<any>(null)
  const [rcaDecisions, setRcaDecisions] = useState<any>(null)
  const [okgChanges, setOkgChanges] = useState<any[]>([])
  const [correlatedAlerts, setCorrelatedAlerts] = useState<any[]>([])
  const [loadingAlerts, setLoadingAlerts] = useState(false)
  const [postmortem, setPostmortem] = useState<any>(null)
  const [loadingPostmortem, setLoadingPostmortem] = useState(false)
  const [remediations, setRemediations] = useState<any[]>([])
  const [loadingRemediations, setLoadingRemediations] = useState(false)
  const [newRemediation, setNewRemediation] = useState('')

  // Floodgate RCA — token is never persisted; user must paste it manually each session
  const [floodgateToken, setFloodgateToken] = useState<string>('')
  const [floodgateModel, setFloodgateModel] = useState<'claude-sonnet-4-6' | 'claude-opus-4-7'>('claude-sonnet-4-6')
  const [floodgateRunning, setFloodgateRunning] = useState(false)

  const runFloodgateRca = async () => {
    if (!floodgateToken.trim()) return
    setFloodgateRunning(true)
    try {
      const r = await incidentsApi.floodgateRCA(incident.id, { model: floodgateModel, token: floodgateToken })
      if (r.data?.success) {
        await fetchFullIncident()
        toast.success('Floodgate RCA completed')
      }
    } catch (e: any) {
      toast.error(e?.response?.data?.message || 'Floodgate RCA failed')
    } finally {
      setFloodgateRunning(false)
    }
  }

  const token = sessionStorage.getItem('access_token') || localStorage.getItem('access_token')

  // Fetch full incident detail (has ai_root_cause, rca_confidence etc)
  const fetchFullIncident = async () => {
    try {
      const r = await fetch(`/api/v1/incidents/${incident.id}`, {
        headers: { Authorization: `Bearer ${token}` }
      })
      if (r.status === 404) return
      const d = await r.json()
      if (d.success && d.data) {
        setFullIncident(d.data)
        // OIE investigation result — enriched by backend from OIE service
        if (d.investigation) setOieInvestigation(d.investigation)
      }
    } catch (e) {
      console.error('[Incident] fetch detail failed:', e instanceof Error ? e.message.substring(0, 200) : 'unknown')
    }
  }

  // Fetch investigation details from RCA orchestrator
  const fetchRcaData = async (invId: string) => {
    if (!invId || loadingRca) return
    setLoadingRca(true)
    try {
      const r = await fetch(`/api/v1/rca/investigations/${invId}`, {
        headers: { Authorization: `Bearer ${token}` }
      })
      const d = await r.json()
      if (d.id) setRcaData(d)
    } catch (e) {
      console.error('[RCA] fetch failed:', e instanceof Error ? e.message.substring(0, 200) : 'unknown')
    }
    setLoadingRca(false)
  }

  useEffect(() => {
    fetchFullIncident()
  }, [incident.id])

  const fetchPostmortem = async () => {
    if (loadingPostmortem) return
    setLoadingPostmortem(true)
    try {
      const r = await incidentsApi.getPostmortem(incident.id)
      const d = r.data?.data ?? r.data
      if (d?.postmortem) setPostmortem(d.postmortem)
    } catch (e: any) {
      if (e?.response?.status !== 404) {
        console.error('[Postmortem] fetch failed:', e?.message?.substring(0, 200))
      }
    } finally {
      setLoadingPostmortem(false)
    }
  }

  const fetchRemediations = async () => {
    if (loadingRemediations) return
    setLoadingRemediations(true)
    try {
      const r = await incidentsApi.getRemediations(incident.id)
      const d = r.data?.data ?? r.data
      setRemediations(d?.remediations ?? [])
    } catch (e) {
      console.error('[Remediations] fetch failed')
    } finally {
      setLoadingRemediations(false)
    }
  }

  const proposeRemediation = async () => {
    if (!newRemediation.trim()) return
    try {
      await incidentsApi.proposeRemediation(incident.id, { proposed_action: newRemediation, proposed_by: 'operator' })
      setNewRemediation('')
      fetchRemediations()
    } catch (e) {
      console.error('[Remediations] propose failed')
    }
  }

  const actOnRemediation = async (remId: string, action: 'approve' | 'reject') => {
    try {
      if (action === 'approve') await incidentsApi.approveRemediation(incident.id, remId)
      else await incidentsApi.rejectRemediation(incident.id, remId)
      fetchRemediations()
    } catch (e) {
      console.error('[Remediations] action failed')
    }
  }

  const fetchCorrelatedAlerts = async () => {
    if (loadingAlerts) return
    setLoadingAlerts(true)
    try {
      const res = await incidentsApi.getAlerts(incident.id)
      if (res.data?.success) {
        setCorrelatedAlerts(res.data.data?.alerts || [])
      }
    } catch (e) {
      console.error('[Incident] fetch alerts failed:', e instanceof Error ? e.message.substring(0, 200) : 'unknown')
    }
    setLoadingAlerts(false)
  }

  const fetchTimeline = async () => {
    setLoadingTimeline(true)
    try {
      const r = await fetch(`/api/v1/incidents/${incident.id}/timeline`, {
        headers: { Authorization: `Bearer ${token}` }
      })
      const d = await r.json()
      if (d.success) setTimeline(d.data || [])
    } catch (e) {
      console.error('[Incident] fetch timeline failed:', e instanceof Error ? e.message.substring(0, 200) : 'unknown')
    }
    setLoadingTimeline(false)
  }

  useEffect(() => {
    if (tab === 'rca') {
      fetchFullIncident()
      const invId = fullIncident?.rca_investigation_id || incident?.rca_investigation_id
      if (invId) fetchRcaData(invId)
      // Fetch CACIE rca_decisions and OKG change intelligence
      incidentsApi.getRCADecisions(incident.id).then(r => { if (r?.data) setRcaDecisions(r.data) }).catch(() => {})
      incidentsApi.getChanges(incident.id, 120).then(r => { if (r?.data?.changes) setOkgChanges(r.data.changes) }).catch(() => {})
      // Fetch parallel KubeSense investigation result
      fetch(`/api/v1/incidents/${incident.id}/kubesense-investigation`, {
        headers: { Authorization: `Bearer ${token}` }
      }).then(r => r.json()).then(d => { if (d?.found && d?.result) setKubesenseInvestigation(d.result) }).catch(() => {})
    }
    if (tab === 'timeline') fetchTimeline()
    if (tab === 'alerts') fetchCorrelatedAlerts()
    if (tab === 'postmortem') fetchPostmortem()
    if (tab === 'remediations') fetchRemediations()
  }, [tab])

  // Load correlated alerts eagerly on open so they're ready when user clicks the tab
  useEffect(() => {
    fetchCorrelatedAlerts()
  }, [incident.id])

  const sevColor = incident.severity === 'critical' ? apple.red
    : incident.severity === 'high' ? apple.orange
    : incident.severity === 'medium' ? apple.yellow : apple.green

  const tabs = [
    { id: 'overview', label: 'Overview', icon: <Eye style={{ width: 14, height: 14 }} /> },
    { id: 'rca', label: 'RCA', icon: <Activity style={{ width: 14, height: 14 }} /> },
    { id: 'blast_radius', label: 'Blast Radius', icon: <Network style={{ width: 14, height: 14 }} /> },
    { id: 'correlation', label: 'Correlation', icon: <GitBranch style={{ width: 14, height: 14 }} /> },
    { id: 'alerts', label: `Correlated Alerts (${correlatedAlerts.length > 0 ? correlatedAlerts.length : (incident.alert_count ?? incident.alert_ids?.length ?? 0)})`, icon: <AlertTriangle style={{ width: 14, height: 14 }} /> },
    { id: 'timeline', label: 'Timeline', icon: <Clock style={{ width: 14, height: 14 }} /> },
    { id: 'postmortem', label: 'Postmortem', icon: <FileText style={{ width: 14, height: 14 }} /> },
    { id: 'remediations', label: 'Actions', icon: <CheckSquare style={{ width: 14, height: 14 }} /> },
  ] as const

  return (
    <div style={{
      position: 'fixed', inset: 0, zIndex: 9999,
      background: 'rgba(0,0,0,0.5)', backdropFilter: 'blur(8px)',
      display: 'flex', alignItems: 'center', justifyContent: 'center',
      padding: 24,
    }} onClick={(e) => { if (e.target === e.currentTarget) onClose() }}>
      <motion.div
        initial={{ opacity: 0, scale: 0.96, y: 16 }}
        animate={{ opacity: 1, scale: 1, y: 0 }}
        exit={{ opacity: 0, scale: 0.96, y: 16 }}
        style={{
          background: 'var(--color-background)',
          borderRadius: 16,
          width: '100%', maxWidth: 760,
          maxHeight: '88vh', overflow: 'hidden',
          display: 'flex', flexDirection: 'column',
          boxShadow: '0 32px 80px rgba(0,0,0,0.3)',
          border: `0.5px solid ${apple.separator}`,
        }}
      >
        {/* Header */}
        <div style={{
          padding: '20px 24px 16px',
          borderBottom: `0.5px solid ${apple.separator}`,
          flexShrink: 0,
        }}>
          <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 12 }}>
            <div style={{ flex: 1 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
                <div style={{ width: 10, height: 10, borderRadius: '50%', background: sevColor, flexShrink: 0 }} />
                <span style={{ fontSize: 11, fontWeight: 600, color: apple.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.5px' }}>
                  INCIDENT #{incident.incident_number}
                </span>
                {incident.auto_created && (
                  <span style={{ fontSize: 10, padding: '2px 7px', borderRadius: 20, background: `${apple.purple}20`, color: apple.purple, fontWeight: 600 }}>
                    AI CORRELATED
                  </span>
                )}
              </div>
              <h2 style={{ fontSize: 19, fontWeight: 700, color: apple.label, margin: 0, lineHeight: 1.3 }}>
                {incident.title}
              </h2>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 10, flexWrap: 'wrap' }}>
                <span style={{ fontSize: 11, fontWeight: 600, padding: '3px 8px', borderRadius: 5, background: `${sevColor}20`, color: sevColor, textTransform: 'uppercase' }}>
                  {incident.severity}
                </span>
                <span style={{ fontSize: 11, fontWeight: 600, padding: '3px 8px', borderRadius: 5, background: apple.fill, color: apple.secondaryLabel, textTransform: 'uppercase' }}>
                  {incident.status}
                </span>
                <RCAStatusBadge status={incident.rca_status || 'none'} confidence={incident.rca_confidence} />
                {(incident.alert_count > 0 || (incident.alert_ids?.length > 0)) && (
                  <span style={{ fontSize: 11, padding: '3px 8px', borderRadius: 5, background: `${apple.blue}15`, color: apple.blue }}>
                    {incident.alert_count ?? incident.alert_ids?.length} alert{(incident.alert_count ?? incident.alert_ids?.length) !== 1 ? 's' : ''} correlated
                  </span>
                )}
              </div>
            </div>
            <button onClick={onClose} style={{ background: 'none', border: 'none', cursor: 'pointer', padding: 4, color: apple.secondaryLabel, flexShrink: 0 }}>
              <X style={{ width: 20, height: 20 }} />
            </button>
          </div>

          {/* Tabs */}
          <div style={{ display: 'flex', gap: 4, marginTop: 16 }}>
            {tabs.map(t => (
              <button
                key={t.id}
                onClick={() => setTab(t.id as any)}
                style={{
                  display: 'flex', alignItems: 'center', gap: 6,
                  padding: '7px 14px', borderRadius: 8, border: 'none', cursor: 'pointer', fontSize: 13, fontWeight: 500,
                  background: tab === t.id ? apple.blue : 'transparent',
                  color: tab === t.id ? '#fff' : apple.secondaryLabel,
                  transition: 'all 0.15s',
                }}
              >
                {t.icon}
                {t.label}
              </button>
            ))}
          </div>
        </div>

        {/* Body */}
        <div style={{ flex: 1, overflowY: 'auto', padding: '20px 24px' }}>
          {tab === 'overview' && (
            <div>
              {/* AI Root Cause banner — most important signal */}
              {(fullIncident?.ai_root_cause || incident.ai_root_cause) && (
                <div style={{ marginBottom: 16, padding: 14, background: `${apple.purple}08`, borderRadius: 10, border: `0.5px solid ${apple.purple}30` }}>
                  <div style={{ fontSize: 11, fontWeight: 600, color: apple.purple, textTransform: 'uppercase', marginBottom: 6, display: 'flex', alignItems: 'center', gap: 5 }}>
                    <Cpu style={{ width: 11, height: 11 }} /> AI Root Cause Analysis
                  </div>
                  <p style={{ fontSize: 13, color: apple.label, lineHeight: 1.6, margin: 0 }}>{fullIncident?.ai_root_cause || incident.ai_root_cause}</p>
                </div>
              )}
              {incident.description && (
                <div style={{ marginBottom: 16 }}>
                  <div style={{ fontSize: 11, fontWeight: 600, color: apple.tertiaryLabel, textTransform: 'uppercase', marginBottom: 6 }}>Description</div>
                  <p style={{ fontSize: 13, color: apple.label, lineHeight: 1.6, margin: 0 }}>{incident.description}</p>
                </div>
              )}

              {/* Correlated Alerts summary inline — show immediately on open */}
              {correlatedAlerts.length > 0 && (
                <div style={{ marginBottom: 16 }}>
                  <div style={{ fontSize: 11, fontWeight: 600, color: apple.tertiaryLabel, textTransform: 'uppercase', marginBottom: 8, display: 'flex', alignItems: 'center', gap: 6 }}>
                    <AlertTriangle style={{ width: 11, height: 11 }} />
                    {correlatedAlerts.length} Correlated Alert{correlatedAlerts.length !== 1 ? 's' : ''}
                  </div>
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                    {correlatedAlerts.map((alert: any, i: number) => {
                      const sevColor = alert.severity === 'critical' ? apple.red
                        : alert.severity === 'high' ? apple.orange
                        : alert.severity === 'warning' ? apple.yellow : apple.green
                      const node = alert.labels?.node || alert.labels?.['k8s.node.name'] || alert.metadata?.node
                      const cluster = alert.labels?.cluster || alert.labels?.['k8s.cluster.name'] || alert.metadata?.cluster
                      const namespace = alert.labels?.namespace || alert.labels?.['k8s.namespace.name'] || alert.metadata?.namespace
                      const workload = alert.labels?.deployment || alert.labels?.app || alert.labels?.workload || alert.metadata?.workload
                      const host = alert.labels?.['host.name'] || alert.labels?.hostname || alert.labels?.host
                      return (
                        <div key={i} style={{
                          padding: '10px 12px', borderRadius: 8,
                          background: apple.fill,
                          border: `0.5px solid ${apple.separator}`,
                          borderLeft: `3px solid ${sevColor}`,
                        }}>
                          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8, marginBottom: 3 }}>
                            <div style={{ fontSize: 13, fontWeight: 600, color: apple.label, lineHeight: 1.3, flex: 1 }}>
                              {alert.title}
                            </div>
                            <span style={{
                              flexShrink: 0, fontSize: 10, fontWeight: 600, padding: '2px 6px',
                              borderRadius: 3, background: `${sevColor}20`, color: sevColor,
                              textTransform: 'uppercase',
                            }}>
                              {alert.severity}
                            </span>
                          </div>
                          {alert.description && (
                            <div style={{ fontSize: 11, color: apple.secondaryLabel, lineHeight: 1.4, marginBottom: 5 }}>
                              {alert.description}
                            </div>
                          )}
                          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4, alignItems: 'center' }}>
                            {cluster && <span style={{ padding: '1px 5px', borderRadius: 3, background: `${apple.blue}15`, color: apple.blue, fontSize: 10, fontWeight: 500 }}>{cluster}</span>}
                            {node && <span style={{ padding: '1px 5px', borderRadius: 3, background: `${apple.orange}15`, color: apple.orange, fontSize: 10, fontWeight: 500 }}>node: {node}</span>}
                            {host && <span style={{ padding: '1px 5px', borderRadius: 3, background: `${apple.orange}15`, color: apple.orange, fontSize: 10, fontWeight: 500 }}>host: {host}</span>}
                            {namespace && <span style={{ padding: '1px 5px', borderRadius: 3, background: `${apple.blue}10`, color: apple.secondaryLabel, fontSize: 10 }}>ns: {namespace}</span>}
                            {workload && <span style={{ padding: '1px 5px', borderRadius: 3, background: `${apple.purple}15`, color: apple.purple, fontSize: 10 }}>{workload}</span>}
                            <span style={{ fontSize: 10, color: apple.tertiaryLabel }}>{alert.source}</span>
                          </div>
                        </div>
                      )
                    })}
                  </div>
                </div>
              )}
              {loadingAlerts && correlatedAlerts.length === 0 && (
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 16, color: apple.secondaryLabel, fontSize: 13 }}>
                  <Loader2 style={{ width: 14, height: 14 }} /> Loading correlated alerts…
                </div>
              )}

              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
                {[
                  { label: 'Created', value: new Date(incident.created_at).toLocaleString() },
                  { label: 'Priority', value: incident.priority || '—' },
                  { label: 'Assigned To', value: incident.assigned_to_name || 'Unassigned' },
                  { label: 'RCA Status', value: incident.rca_status || 'none' },
                ].map(({ label, value }) => (
                  <div key={label} style={{ padding: '12px 14px', background: apple.fill, borderRadius: 8 }}>
                    <div style={{ fontSize: 11, color: apple.tertiaryLabel, marginBottom: 4 }}>{label}</div>
                    <div style={{ fontSize: 14, fontWeight: 500, color: apple.label }}>{value}</div>
                  </div>
                ))}
              </div>

              {/* Root entity / cascade chain */}
              {(incident.correlation_id || incident.topology_path) && (
                <div style={{ marginTop: 16 }}>
                  {incident.correlation_id && (
                    <div style={{ marginBottom: 12, padding: '10px 14px', background: `${apple.purple}08`, borderRadius: 10, border: `0.5px solid ${apple.purple}25` }}>
                      <div style={{ fontSize: 11, fontWeight: 600, color: apple.purple, textTransform: 'uppercase', letterSpacing: '0.3px', marginBottom: 5 }}>Root Entity</div>
                      <code style={{ fontSize: 12, color: apple.label, wordBreak: 'break-all' }}>{incident.correlation_id}</code>
                    </div>
                  )}
                  {incident.topology_path && incident.topology_path !== '' && (
                    <div style={{ padding: '10px 14px', background: apple.fill, borderRadius: 10, border: `0.5px solid ${apple.separator}` }}>
                      <div style={{ fontSize: 11, fontWeight: 600, color: apple.tertiaryLabel, textTransform: 'uppercase', letterSpacing: '0.3px', marginBottom: 8 }}>Cascade Chain</div>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 4, flexWrap: 'wrap' }}>
                        {incident.topology_path.split('/').filter(Boolean).map((seg: string, i: number, arr: string[]) => (
                          <React.Fragment key={i}>
                            <span style={{
                              fontSize: 11, padding: '3px 8px', borderRadius: 5, fontFamily: 'monospace',
                              background: i === 0 ? `${apple.orange}18` : i === arr.length - 1 ? `${apple.blue}18` : `${apple.gray}12`,
                              color: i === 0 ? apple.orange : i === arr.length - 1 ? apple.blue : apple.secondaryLabel,
                              border: `0.5px solid ${i === 0 ? `${apple.orange}30` : i === arr.length - 1 ? `${apple.blue}30` : apple.separator}`,
                            }}>{seg}</span>
                            {i < arr.length - 1 && <span style={{ fontSize: 10, color: apple.tertiaryLabel }}>→</span>}
                          </React.Fragment>
                        ))}
                      </div>
                      {incident.topology_path.startsWith('h:') && (
                        <div style={{ marginTop: 6, fontSize: 11, color: apple.orange, display: 'flex', alignItems: 'center', gap: 4 }}>
                          <span>⚠</span> Bare-host anchor (no K8s cluster)
                        </div>
                      )}
                    </div>
                  )}
                </div>
              )}

              {/* V2: Ontology domain + evolution metadata */}
              {((incident as any).ontology_domain || (incident as any).evolution_generation > 0 || (incident as any).merge_source_ids?.length > 0) && (
                <div style={{ marginTop: 16, display: 'flex', flexDirection: 'column', gap: 10 }}>
                  {(incident as any).ontology_domain && (
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                      <span style={{ fontSize: 11, color: apple.tertiaryLabel, fontWeight: 600, textTransform: 'uppercase' }}>Domain</span>
                      <span style={{
                        fontSize: 12, padding: '3px 10px', borderRadius: 20,
                        background: `${apple.purple}18`, color: apple.purple,
                        border: `0.5px solid ${apple.purple}30`, fontWeight: 600,
                      }}>{(incident as any).ontology_domain}</span>
                      {(incident as any).topo_root_entity_id && (
                        <span style={{ fontSize: 11, color: apple.secondaryLabel, fontFamily: 'monospace' }}>
                          root: {(incident as any).topo_root_entity_id}
                        </span>
                      )}
                    </div>
                  )}
                  {(incident as any).evolution_generation > 0 && (
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                      <span style={{ fontSize: 11, color: apple.tertiaryLabel, fontWeight: 600, textTransform: 'uppercase' }}>Evolution</span>
                      <span style={{
                        fontSize: 12, padding: '3px 10px', borderRadius: 20,
                        background: `${apple.orange}18`, color: apple.orange,
                        border: `0.5px solid ${apple.orange}30`, fontWeight: 600,
                      }}>Gen {(incident as any).evolution_generation}</span>
                      {(incident as any).merge_source_ids?.length > 0 && (
                        <span style={{ fontSize: 11, color: apple.secondaryLabel }}>
                          merged from {(incident as any).merge_source_ids.length} incident{(incident as any).merge_source_ids.length !== 1 ? 's' : ''}
                        </span>
                      )}
                    </div>
                  )}
                </div>
              )}

            </div>
          )}

          {tab === 'rca' && (() => {
            // Always use the freshly fetched full incident for RCA display
            const inc = fullIncident || incident
            const invId = inc.rca_investigation_id
            const rcaStatus = inc.rca_status || 'none'
            const aiRootCause = inc.ai_root_cause || rcaData?.root_cause?.summary || rcaData?.summary || ''
            const rcaConfidence = inc.rca_confidence || rcaData?.root_cause?.confidence || 0
            const remediation: any[] = rcaData?.remediation || []
            const thoughtLog: string[] = rcaData?.thought_log || []

            return (
            <div>
              {loadingRca && !rcaData && (
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12, color: apple.secondaryLabel, fontSize: 13 }}>
                  <Loader2 style={{ width: 14, height: 14 }} /> Loading investigation…
                </div>
              )}

              {/* Show root cause whenever ai_root_cause is set (CACIE result) OR orchestrator completes */}
              {(aiRootCause || (rcaStatus === 'completed' && rcaData)) ? (
                <div>
                  {aiRootCause && (
                    <div style={{ marginBottom: 16, padding: 16, background: `${apple.green}08`, borderRadius: 10, border: `0.5px solid ${apple.green}30` }}>
                      <div style={{ fontSize: 12, fontWeight: 600, color: apple.green, marginBottom: 6 }}>Root Cause</div>
                      <p style={{ fontSize: 14, color: apple.label, margin: 0, lineHeight: 1.6 }}>{aiRootCause}</p>
                      {rcaConfidence > 0 && (
                        <div style={{ marginTop: 8, fontSize: 13, color: apple.green }}>
                          Confidence: {Math.round(rcaConfidence * 100)}%
                        </div>
                      )}
                    </div>
                  )}

                  {/* OKG Change Intelligence — deployment changes near this incident */}
                  {okgChanges.length > 0 && (
                    <div style={{ marginBottom: 16 }}>
                      <div style={{ fontSize: 12, fontWeight: 600, color: apple.tertiaryLabel, marginBottom: 8, textTransform: 'uppercase', letterSpacing: '0.4px' }}>
                        Change Intelligence — {okgChanges.length} deployment{okgChanges.length !== 1 ? 's' : ''} before incident
                      </div>
                      {okgChanges.slice(0, 5).map((c: any, i: number) => (
                        <div key={i} style={{
                          display: 'flex', alignItems: 'center', gap: 10,
                          padding: '8px 12px', borderRadius: 8, marginBottom: 6,
                          background: c.causality_score >= 0.80 ? `${apple.orange}08` : apple.fill,
                          border: `0.5px solid ${c.causality_score >= 0.80 ? apple.orange + '30' : apple.separator}`,
                        }}>
                          <div style={{ flex: 1, minWidth: 0 }}>
                            <div style={{ fontSize: 13, fontWeight: 500, color: apple.label }}>{c.service || c.title}</div>
                            <div style={{ fontSize: 11, color: apple.secondaryLabel }}>{c.namespace} · {c.delta_minutes}m before incident</div>
                          </div>
                          <div style={{
                            fontSize: 11, fontWeight: 600, padding: '2px 8px', borderRadius: 6,
                            background: c.risk_level === 'high' ? `${apple.orange}20` : `${apple.blue}15`,
                            color: c.risk_level === 'high' ? apple.orange : apple.blue,
                          }}>
                            {Math.round(c.causality_score * 100)}% causal
                          </div>
                        </div>
                      ))}
                    </div>
                  )}

                  {/* CACIE Ranked Hypotheses — from rca_hypotheses on the incident */}
                  {inc.rca_hypotheses && (() => {
                    try {
                      const hyps = typeof inc.rca_hypotheses === 'string'
                        ? JSON.parse(inc.rca_hypotheses)
                        : inc.rca_hypotheses
                      if (!Array.isArray(hyps) || hyps.length === 0) return null
                      return (
                        <div style={{ marginBottom: 16 }}>
                          <div style={{ fontSize: 12, fontWeight: 600, color: apple.tertiaryLabel, marginBottom: 8, textTransform: 'uppercase', letterSpacing: '0.4px' }}>
                            RCA Hypotheses ({hyps.length})
                          </div>
                          {hyps.slice(0, 5).map((h: any, i: number) => (
                            <div key={i} style={{
                              display: 'flex', alignItems: 'center', gap: 10,
                              padding: '8px 12px', borderRadius: 8, marginBottom: 6,
                              background: i === 0 ? `${apple.blue}08` : apple.fill,
                              border: `0.5px solid ${i === 0 ? apple.blue + '30' : apple.separator}`,
                            }}>
                              <span style={{ fontSize: 12, fontWeight: 700, color: i === 0 ? apple.blue : apple.tertiaryLabel, minWidth: 20 }}>#{i + 1}</span>
                              <div style={{ flex: 1, minWidth: 0 }}>
                                <div style={{ fontSize: 13, fontWeight: 500, color: apple.label }}>{h.entity_label || h.EntityLabel || '—'}</div>
                                <div style={{ fontSize: 11, color: apple.secondaryLabel }}>{h.entity_type || h.EntityType || h.entity_kind || h.EntityKind || ''}</div>
                              </div>
                              <div style={{ fontSize: 12, fontWeight: 600, color: h.confidence > 0.6 ? apple.green : apple.orange }}>
                                {h.confidence ? `${Math.round(h.confidence * 100)}%` : '—'}
                              </div>
                            </div>
                          ))}
                        </div>
                      )
                    } catch { return null }
                  })()}

                  {/* Topology blast radius details */}
                  {inc.blast_radius_details && (() => {
                    try {
                      const details = typeof inc.blast_radius_details === 'string'
                        ? JSON.parse(inc.blast_radius_details)
                        : inc.blast_radius_details
                      if (!Array.isArray(details) || details.length === 0) return null
                      return (
                        <div style={{ marginBottom: 16 }}>
                          <div style={{ fontSize: 12, fontWeight: 600, color: apple.tertiaryLabel, marginBottom: 8, textTransform: 'uppercase', letterSpacing: '0.4px' }}>
                            Blast Radius — {details.length} affected resources
                          </div>
                          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
                            {details.slice(0, 20).map((n: any, i: number) => (
                              <div key={i} style={{
                                padding: '3px 10px', borderRadius: 12,
                                background: apple.fill, border: `0.5px solid ${apple.separator}`,
                                fontSize: 11, color: apple.secondaryLabel,
                              }}>
                                <span style={{ fontWeight: 600, color: apple.label }}>{n.name || n.label}</span>
                                {n.namespace && <span style={{ color: apple.tertiaryLabel }}> · {n.namespace}</span>}
                                {n.infra_level && <span style={{ color: apple.blue }}> [{n.infra_level}]</span>}
                              </div>
                            ))}
                            {details.length > 20 && <div style={{ padding: '3px 10px', fontSize: 11, color: apple.tertiaryLabel }}>+{details.length - 20} more</div>}
                          </div>
                        </div>
                      )
                    } catch { return null }
                  })()}
                  {rcaData?.root_cause?.component && rcaData.root_cause.component !== 'unknown' && (
                    <div style={{ marginBottom: 12, padding: 12, background: apple.fill, borderRadius: 8 }}>
                      <span style={{ fontSize: 12, color: apple.tertiaryLabel }}>Component: </span>
                      <span style={{ fontSize: 13, fontWeight: 600, color: apple.label }}>{rcaData.root_cause.component}</span>
                      {rcaData.root_cause.category && rcaData.root_cause.category !== 'unknown' && (
                        <span style={{ marginLeft: 12, fontSize: 12, color: apple.secondaryLabel }}>· {rcaData.root_cause.category}</span>
                      )}
                    </div>
                  )}
                  {remediation.length > 0 && (
                    <div style={{ marginBottom: 16 }}>
                      <div style={{ fontSize: 12, fontWeight: 600, color: apple.tertiaryLabel, marginBottom: 8, textTransform: 'uppercase' }}>Remediation Steps</div>
                      {remediation.map((step: any, i: number) => (
                        <div key={i} style={{ display: 'flex', gap: 10, padding: '10px 12px', background: apple.fill, borderRadius: 8, marginBottom: 6, border: `0.5px solid ${apple.separator}` }}>
                          <span style={{ minWidth: 20, height: 20, borderRadius: '50%', background: `${apple.blue}20`, color: apple.blue, fontSize: 11, fontWeight: 700, display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 }}>{step.step || i + 1}</span>
                          <div>
                            <div style={{ fontSize: 13, color: apple.label }}>{step.action}</div>
                            {step.command && <code style={{ fontSize: 11, color: apple.orange, display: 'block', marginTop: 4 }}>{step.command}</code>}
                          </div>
                        </div>
                      ))}
                    </div>
                  )}
                  {rcaData?.root_cause?.evidence?.length > 0 && (
                    <div>
                      <div style={{ fontSize: 12, fontWeight: 600, color: apple.tertiaryLabel, marginBottom: 6, textTransform: 'uppercase' }}>Evidence</div>
                      {rcaData.root_cause.evidence.map((e: string, i: number) => (
                        <div key={i} style={{ fontSize: 13, color: apple.secondaryLabel, padding: '6px 0', borderBottom: `0.5px solid ${apple.separator}` }}>· {e}</div>
                      ))}
                    </div>
                  )}
                  {rcaData?.root_cause?.timeline?.length > 0 && (
                    <div style={{ marginTop: 16 }}>
                      <div style={{ fontSize: 12, fontWeight: 600, color: apple.tertiaryLabel, marginBottom: 8, textTransform: 'uppercase' }}>Timeline</div>
                      {rcaData.root_cause.timeline.map((entry: any, i: number) => (
                        <div key={i} style={{ display: 'flex', gap: 10, padding: '6px 0', borderBottom: `0.5px solid ${apple.separator}` }}>
                          <span style={{ fontSize: 11, color: apple.tertiaryLabel, minWidth: 80, flexShrink: 0 }}>{entry.time ? new Date(entry.time).toLocaleTimeString() : '—'}</span>
                          <span style={{ fontSize: 13, color: apple.secondaryLabel }}>{entry.event}</span>
                        </div>
                      ))}
                    </div>
                  )}
                  {rcaData?.similar_incidents?.length > 0 && (
                    <div style={{ marginTop: 16 }}>
                      <div style={{ fontSize: 12, fontWeight: 600, color: apple.tertiaryLabel, marginBottom: 8, textTransform: 'uppercase' }}>Similar Past Incidents</div>
                      {rcaData.similar_incidents.slice(0, 5).map((si: any, i: number) => (
                        <div key={i} style={{ padding: '8px 12px', marginBottom: 6, borderRadius: 8, background: apple.fill, border: `0.5px solid ${apple.separator}` }}>
                          <div style={{ fontSize: 13, fontWeight: 500, color: apple.label }}>{si.alert_title || si.title || 'Unknown incident'}</div>
                          {si.root_cause_summary && <div style={{ fontSize: 12, color: apple.secondaryLabel, marginTop: 3 }}>Resolution: {si.root_cause_summary}</div>}
                          {(si.combined_score != null || si.similarity_score != null) && (
                            <div style={{ fontSize: 11, color: apple.tertiaryLabel, marginTop: 3 }}>
                              Similarity: {Math.round(((si.combined_score ?? si.similarity_score) || 0) * 100)}%
                            </div>
                          )}
                        </div>
                      ))}
                    </div>
                  )}
                </div>

              ) : rcaStatus === 'failed' ? (
                <div>
                  {/* If there's a real root cause (from CACIE before OIE timeout), show it */}
                  {aiRootCause && !aiRootCause.includes('investigation timed out') && !aiRootCause.includes('OIE investigation completed with insufficient') ? (
                    <div>
                      <div style={{ marginBottom: 12, padding: 14, background: `${apple.orange}08`, borderRadius: 10, border: `0.5px solid ${apple.orange}30` }}>
                        <div style={{ fontSize: 12, fontWeight: 600, color: apple.orange, marginBottom: 6 }}>PRELIMINARY RCA (Investigation Incomplete)</div>
                        <p style={{ fontSize: 13, color: apple.label, lineHeight: 1.6, margin: 0 }}>{aiRootCause}</p>
                        <div style={{ fontSize: 11, color: apple.tertiaryLabel, marginTop: 6 }}>OIE deep investigation did not complete — this is the best available correlation result. Verify manually.</div>
                      </div>
                    </div>
                  ) : (
                    <div style={{ textAlign: 'center', padding: '32px 0', color: apple.secondaryLabel }}>
                      <XCircle style={{ width: 28, height: 28, margin: '0 auto 10px', color: apple.red, opacity: 0.6 }} />
                      <div style={{ fontSize: 14, fontWeight: 500, color: apple.label, marginBottom: 6 }}>RCA Investigation Failed</div>
                      <div style={{ fontSize: 13, color: apple.secondaryLabel, maxWidth: 400, margin: '0 auto', lineHeight: 1.5 }}>
                        {aiRootCause || 'The OIE investigation did not complete. Check that OIE_ALERTHUB_BASE_URL is configured and the OIE service is healthy.'}
                      </div>
                      <button onClick={fetchFullIncident} style={{ marginTop: 12, padding: '7px 14px', borderRadius: 8, background: apple.fill, border: `0.5px solid ${apple.separator}`, cursor: 'pointer', fontSize: 13, color: apple.secondaryLabel }}>
                        Refresh
                      </button>
                    </div>
                  )}
                </div>

              ) : invId && (rcaStatus === 'queued' || rcaStatus === 'investigating') ? (
                <div>
                  {/* Stuck RCA banner: if updated >20 min ago and still investigating */}
                  {rcaStatus === 'investigating' && inc.updated_at && (() => {
                    const updatedAt = new Date(inc.updated_at).getTime()
                    const stuckMs = Date.now() - updatedAt
                    if (stuckMs > 20 * 60 * 1000) {
                      return (
                        <div style={{ marginBottom: 12, padding: '10px 14px', borderRadius: 8, background: `${apple.orange}12`, border: `0.5px solid ${apple.orange}40`, display: 'flex', alignItems: 'center', gap: 8 }}>
                          <AlertTriangle style={{ width: 14, height: 14, color: apple.orange, flexShrink: 0 }} />
                          <span style={{ fontSize: 12, color: apple.orange }}>
                            RCA has been running for {Math.round(stuckMs / 60000)} minutes. Investigation may be stalled — check orchestrator logs or re-trigger.
                          </span>
                        </div>
                      )
                    }
                    return null
                  })()}
                  {/* OIE evidence-based investigation result */}
                  {oieInvestigation && oieInvestigation.status === 'COMPLETED' && oieInvestigation.rca && (
                    <div style={{ marginBottom: 16, padding: 14, background: `rgba(0,122,255,0.04)`, borderRadius: 10, border: `0.5px solid rgba(0,122,255,0.2)` }}>
                      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 8 }}>
                        <div style={{ fontSize: 12, fontWeight: 600, color: '#007AFF', letterSpacing: '0.3px' }}>OIE Evidence-Based RCA</div>
                        <div style={{ fontSize: 11, color: oieInvestigation.rca.confidence_band === 'CONFIRMED' ? '#34C759' : oieInvestigation.rca.confidence_band === 'LIKELY' ? '#007AFF' : '#FF9500', fontWeight: 600 }}>
                          {oieInvestigation.rca.confidence_band} · {Math.round(oieInvestigation.rca.confidence * 100)}%
                        </div>
                      </div>
                      <div style={{ fontSize: 13, color: 'rgba(0,0,0,0.85)', lineHeight: 1.5, marginBottom: 6 }}>{oieInvestigation.rca.summary}</div>
                      {oieInvestigation.rca.narrative && (
                        <div style={{ fontSize: 13, color: 'rgba(0,0,0,0.75)', lineHeight: 1.6, marginBottom: 8, padding: 10, background: 'rgba(0,0,0,0.03)', borderRadius: 8, borderLeft: '3px solid #007AFF' }}>
                          {oieInvestigation.rca.narrative}
                        </div>
                      )}
                      {/* Citation chips — HolmesGPT pattern */}
                      {oieInvestigation.rca.citations?.length > 0 && (
                        <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', marginBottom: 8 }}>
                          {oieInvestigation.rca.citations.map((c: any, i: number) => (
                            <span key={i} title={c.description} style={{
                              fontSize: 10, padding: '2px 8px', borderRadius: 12,
                              background: 'rgba(0,122,255,0.1)', color: '#007AFF',
                              border: '0.5px solid rgba(0,122,255,0.25)', cursor: 'default',
                            }}>
                              [{c.source}] {c.evidence_type.replace(/_/g, ' ')}
                            </span>
                          ))}
                        </div>
                      )}
                      {oieInvestigation.rca.evidence_gathered > 0 && (
                        <div style={{ fontSize: 11, color: 'rgba(0,0,0,0.45)' }}>
                          {oieInvestigation.rca.evidence_gathered} evidence pieces · {(oieInvestigation.rca.evidence_sources || []).join(', ')} · {oieInvestigation.rca.hypotheses_generated} hypotheses evaluated
                        </div>
                      )}
                    </div>
                  )}
                  <InvestigationStreamInline investigationId={invId} />
                  {/* KubeSense parallel investigation result */}
                  {kubesenseInvestigation && (
                    <div style={{ marginTop: 12, marginBottom: 8, padding: 14, background: 'rgba(52,199,89,0.04)', borderRadius: 10, border: '0.5px solid rgba(52,199,89,0.2)' }}>
                      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 8 }}>
                        <div style={{ fontSize: 12, fontWeight: 600, color: '#34C759', letterSpacing: '0.3px' }}>KubeSense Parallel RCA</div>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                          <span style={{
                            fontSize: 13, fontWeight: 700,
                            color: kubesenseInvestigation.grade === 'A' ? '#34C759' : kubesenseInvestigation.grade === 'B' ? '#007AFF' : kubesenseInvestigation.grade === 'C' ? '#FF9500' : '#FF3B30',
                          }}>Grade {kubesenseInvestigation.grade}</span>
                          <span style={{ fontSize: 11, color: 'rgba(0,0,0,0.45)' }}>
                            {Math.round(kubesenseInvestigation.confidence * 100)}% confidence
                          </span>
                        </div>
                      </div>
                      {kubesenseInvestigation.root_cause && (
                        <div style={{ fontSize: 13, color: 'rgba(0,0,0,0.85)', lineHeight: 1.5, marginBottom: 6 }}>
                          {kubesenseInvestigation.root_cause}
                        </div>
                      )}
                      {kubesenseInvestigation.summary && kubesenseInvestigation.summary !== kubesenseInvestigation.root_cause && (
                        <div style={{ fontSize: 12, color: 'rgba(0,0,0,0.55)', lineHeight: 1.5, marginBottom: 6 }}>
                          {kubesenseInvestigation.summary}
                        </div>
                      )}
                      <div style={{ fontSize: 11, color: 'rgba(0,0,0,0.35)' }}>
                        {kubesenseInvestigation.evidence_count} evidence pieces · cluster: {kubesenseInvestigation.cluster_id}
                      </div>
                    </div>
                  )}
                  {thoughtLog.length > 0 && (
                    <div style={{ marginTop: 16 }}>
                      <div style={{ fontSize: 12, fontWeight: 600, color: apple.tertiaryLabel, marginBottom: 6 }}>Investigation Log</div>
                      <div style={{ background: 'rgba(0,0,0,0.04)', borderRadius: 8, padding: 12, maxHeight: 200, overflowY: 'auto', fontFamily: 'monospace', fontSize: 12 }}>
                        {thoughtLog.map((t, i) => <div key={i} style={{ marginBottom: 6, color: apple.secondaryLabel }}>→ {t}</div>)}
                      </div>
                    </div>
                  )}
                </div>

              ) : rcaStatus === 'completed' && !aiRootCause ? (
                <div>
                  {/* Show OIE result even in completed state — OIE runs in parallel */}
                  {oieInvestigation && oieInvestigation.status === 'COMPLETED' && oieInvestigation.rca && (
                    <div style={{ marginBottom: 16, padding: 14, background: 'rgba(0,122,255,0.04)', borderRadius: 10, border: '0.5px solid rgba(0,122,255,0.2)' }}>
                      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 8 }}>
                        <div style={{ fontSize: 12, fontWeight: 600, color: '#007AFF', letterSpacing: '0.3px' }}>OIE Evidence-Based RCA</div>
                        <div style={{ fontSize: 11, color: oieInvestigation.rca.confidence_band === 'CONFIRMED' ? '#34C759' : oieInvestigation.rca.confidence_band === 'LIKELY' ? '#007AFF' : '#FF9500', fontWeight: 600 }}>
                          {oieInvestigation.rca.confidence_band} · {Math.round(oieInvestigation.rca.confidence * 100)}%
                        </div>
                      </div>
                      <div style={{ fontSize: 13, color: 'rgba(0,0,0,0.85)', lineHeight: 1.5, marginBottom: 6 }}>{oieInvestigation.rca.summary}</div>
                      {oieInvestigation.rca.evidence_gathered > 0 && (
                        <div style={{ fontSize: 11, color: 'rgba(0,0,0,0.45)' }}>
                          {oieInvestigation.rca.evidence_gathered} evidence pieces · {(oieInvestigation.rca.evidence_sources || []).join(', ')} · {oieInvestigation.rca.hypotheses_generated} hypotheses evaluated
                        </div>
                      )}
                    </div>
                  )}
                  <div style={{ textAlign: 'center', padding: '20px 0', color: apple.secondaryLabel }}>
                    <CheckCircle style={{ width: 32, height: 32, margin: '0 auto 12px', color: apple.green, opacity: 0.6 }} />
                    <div style={{ fontSize: 14 }}>Investigation completed</div>
                    <div style={{ fontSize: 13, marginTop: 6, opacity: 0.7, marginBottom: 20 }}>Awaiting result sync from orchestrator.</div>
                    <FloodgateRcaSection incidentId={incident.id} floodgateToken={floodgateToken} setFloodgateToken={setFloodgateToken} floodgateModel={floodgateModel} setFloodgateModel={setFloodgateModel} running={floodgateRunning} onRun={runFloodgateRca} />
                  </div>
                </div>

              ) : (
                <div>
                  {oieInvestigation && oieInvestigation.status === 'COMPLETED' && oieInvestigation.rca && (
                    <div style={{ marginBottom: 16, padding: 14, background: 'rgba(0,122,255,0.04)', borderRadius: 10, border: '0.5px solid rgba(0,122,255,0.2)' }}>
                      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 8 }}>
                        <div style={{ fontSize: 12, fontWeight: 600, color: '#007AFF', letterSpacing: '0.3px' }}>OIE Evidence-Based RCA</div>
                        <div style={{ fontSize: 11, color: oieInvestigation.rca.confidence_band === 'CONFIRMED' ? '#34C759' : oieInvestigation.rca.confidence_band === 'LIKELY' ? '#007AFF' : '#FF9500', fontWeight: 600 }}>
                          {oieInvestigation.rca.confidence_band} · {Math.round(oieInvestigation.rca.confidence * 100)}%
                        </div>
                      </div>
                      <div style={{ fontSize: 13, color: 'rgba(0,0,0,0.85)', lineHeight: 1.5, marginBottom: 6 }}>{oieInvestigation.rca.summary}</div>
                      {oieInvestigation.rca.evidence_gathered > 0 && (
                        <div style={{ fontSize: 11, color: 'rgba(0,0,0,0.45)' }}>
                          {oieInvestigation.rca.evidence_gathered} evidence pieces · {(oieInvestigation.rca.evidence_sources || []).join(', ')} · {oieInvestigation.rca.hypotheses_generated} hypotheses evaluated
                        </div>
                      )}
                    </div>
                  )}
                  <div style={{ textAlign: 'center', padding: oieInvestigation ? '16px 0' : '32px 0', color: apple.secondaryLabel }}>
                    <Activity style={{ width: 32, height: 32, margin: '0 auto 12px', opacity: 0.4 }} />
                    <div style={{ fontSize: 14 }}>No RCA investigation started yet.</div>
                    <div style={{ fontSize: 13, marginTop: 6, opacity: 0.7, marginBottom: 20 }}>RCA auto-triggers when the correlation engine creates incidents.</div>
                    <FloodgateRcaSection incidentId={incident.id} floodgateToken={floodgateToken} setFloodgateToken={setFloodgateToken} floodgateModel={floodgateModel} setFloodgateModel={setFloodgateModel} running={floodgateRunning} onRun={runFloodgateRca} />
                  </div>
                </div>
              )}

              {/* V2: Probabilistic RCA hypotheses */}
              {(() => {
                const hyps: any[] = (inc as any).rca_hypotheses ?? []
                if (!hyps.length) return null
                return (
                  <div style={{ marginTop: 20 }}>
                    <div style={{ fontSize: 12, fontWeight: 600, color: apple.tertiaryLabel, textTransform: 'uppercase', marginBottom: 10 }}>
                      V2 Probabilistic Hypotheses
                    </div>
                    {hyps.map((h: any, i: number) => {
                      const pct = Math.round((h.confidence ?? 0) * 100)
                      const barColor = pct >= 80 ? apple.green : pct >= 50 ? apple.orange : apple.gray
                      return (
                        <div key={i} style={{
                          marginBottom: 10, padding: '12px 14px', borderRadius: 10,
                          background: apple.fill, border: `0.5px solid ${apple.separator}`,
                        }}>
                          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 6 }}>
                            <span style={{ fontSize: 13, fontWeight: 600, color: apple.label }}>{h.entity_label || h.entity_id}</span>
                            <span style={{ fontSize: 12, fontWeight: 700, color: barColor }}>{pct}%</span>
                          </div>
                          <div style={{ height: 4, borderRadius: 2, background: apple.separator, marginBottom: 8 }}>
                            <div style={{ width: `${pct}%`, height: '100%', borderRadius: 2, background: barColor, transition: 'width 0.4s' }} />
                          </div>
                          {h.entity_type && (
                            <span style={{ fontSize: 10, padding: '2px 7px', borderRadius: 20, background: `${apple.blue}15`, color: apple.blue, fontWeight: 500 }}>{h.entity_type}</span>
                          )}
                          {h.reasoning && (
                            <div style={{ fontSize: 12, color: apple.secondaryLabel, marginTop: 6, lineHeight: 1.5 }}>{h.reasoning}</div>
                          )}
                        </div>
                      )
                    })}
                  </div>
                )
              })()}
            </div>
            )
          })()}

          {tab === 'blast_radius' && (() => {
            const inc = fullIncident || incident
            const blastRadiusData: any[] = inc.blast_radius ?? []
            const topologyPath = inc.topology_path || incident.topology_path
            const affectedServices: string[] = inc.affected_services_names ?? incident.affected_services_names ?? []
            const causalChain: any[] = inc.causal_chain ?? (incident as any).causal_chain ?? []
            return (
            <div>
              {blastRadiusData.length === 0 ? (
                <div style={{ textAlign: 'center', padding: '40px 0', color: apple.secondaryLabel }}>
                  <Shield style={{ width: 32, height: 32, margin: '0 auto 12px', opacity: 0.4 }} />
                  <div style={{ fontSize: 14 }}>No blast radius data available.</div>
                  <div style={{ fontSize: 13, marginTop: 6, opacity: 0.7 }}>Populated when topology strategy matches infra graph nodes.</div>
                </div>
              ) : (
                <div>
                  <div style={{ marginBottom: 16, padding: '12px 16px', background: `${apple.orange}10`, borderRadius: 10, border: `0.5px solid ${apple.orange}30` }}>
                    <div style={{ fontSize: 13, fontWeight: 600, color: apple.orange }}>
                      {blastRadiusData.length} infrastructure {blastRadiusData.length === 1 ? 'node' : 'nodes'} potentially impacted
                    </div>
                    {topologyPath && (
                      <div style={{ fontSize: 12, color: apple.secondaryLabel, marginTop: 6 }}>Path: {topologyPath}</div>
                    )}
                  </div>
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                    {blastRadiusData.map((node: string | any, i: number) => {
                      // Backend sends either string[] (entity IDs) or {node_id, node_type, impact}[]
                      const nodeId = typeof node === 'string' ? node : (node.node_id || node.entity_id || String(node))
                      const nodeType = typeof node === 'object' ? (node.node_type || node.entity_type || '') : ''
                      return (
                      <div key={i} style={{
                        display: 'flex', alignItems: 'center', gap: 10,
                        padding: '10px 14px', borderRadius: 8, background: apple.fill,
                        border: `0.5px solid ${apple.separator}`,
                      }}>
                        <Server style={{ width: 14, height: 14, color: apple.orange, flexShrink: 0 }} />
                        <span style={{ fontSize: 14, color: apple.label }}>{nodeId}</span>
                        {nodeType && <span style={{ fontSize: 11, color: apple.secondaryLabel }}>({nodeType})</span>}
                        <span style={{ marginLeft: 'auto', fontSize: 11, color: apple.tertiaryLabel }}>Node {i + 1}</span>
                      </div>
                      )
                    })}
                  </div>
                  {affectedServices.length > 0 && (
                    <div style={{ marginTop: 16 }}>
                      <div style={{ fontSize: 12, fontWeight: 600, color: apple.tertiaryLabel, textTransform: 'uppercase', marginBottom: 8 }}>Affected Services</div>
                      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
                        {affectedServices.map((s: string, i: number) => (
                          <span key={i} style={{ padding: '4px 10px', borderRadius: 20, background: `${apple.red}15`, color: apple.red, fontSize: 12 }}>{s}</span>
                        ))}
                      </div>
                    </div>
                  )}

                  {/* V2: Causal chain from recursive topo RCA */}
                  {(() => {
                    if (!causalChain.length) return null
                    return (
                      <div style={{ marginTop: 16 }}>
                        <div style={{ fontSize: 12, fontWeight: 600, color: apple.tertiaryLabel, textTransform: 'uppercase', marginBottom: 8 }}>Causal Chain</div>
                        <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                          {causalChain.map((link: any, i: number) => (
                            <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12 }}>
                              <code style={{ padding: '2px 8px', borderRadius: 5, background: `${apple.orange}15`, color: apple.orange, fontSize: 11 }}>{link.from}</code>
                              <span style={{ color: apple.tertiaryLabel }}>→ {link.type} →</span>
                              <code style={{ padding: '2px 8px', borderRadius: 5, background: `${apple.blue}15`, color: apple.blue, fontSize: 11 }}>{link.to}</code>
                              {link.weight && (
                                <span style={{ marginLeft: 'auto', fontSize: 10, color: apple.tertiaryLabel }}>{Math.round(link.weight * 100)}%</span>
                              )}
                            </div>
                          ))}
                        </div>
                      </div>
                    )
                  })()}
                </div>
              )}
            </div>
            )
          })()}

          {tab === 'correlation' && (
            <div>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginBottom: 16 }}>
                {[
                  { label: 'Dominant Strategy', value: incident.dominant_strategy || '—', color: apple.purple },
                  { label: 'Correlation Method', value: incident.correlation_method || '—', color: apple.blue },
                  { label: 'Confidence Score', value: incident.correlation_confidence ? `${Math.round(incident.correlation_confidence * 100)}%` : '—', color: apple.green },
                  { label: 'Alert Count', value: String(incident.alert_count ?? incident.alert_ids?.length ?? 0), color: apple.orange },
                  ...((incident as any).ontology_domain ? [{ label: 'Ontology Domain', value: (incident as any).ontology_domain, color: apple.purple }] : []),
                  ...((incident as any).evolution_generation > 0 ? [{ label: 'Evolution Gen', value: `Gen ${(incident as any).evolution_generation}`, color: apple.orange }] : []),
                ].map(({ label, value, color }) => (
                  <div key={label} style={{ padding: '14px 16px', background: apple.fill, borderRadius: 10, border: `0.5px solid ${apple.separator}` }}>
                    <div style={{ fontSize: 11, color: apple.tertiaryLabel, marginBottom: 6, textTransform: 'uppercase' }}>{label}</div>
                    <div style={{ fontSize: 16, fontWeight: 600, color }}>{value}</div>
                  </div>
                ))}
              </div>
              {incident.topology_path && (
                <div style={{ padding: 16, background: apple.fill, borderRadius: 10 }}>
                  <div style={{ fontSize: 12, fontWeight: 600, color: apple.tertiaryLabel, textTransform: 'uppercase', marginBottom: 8 }}>Topology Path</div>
                  <p style={{ fontSize: 13, color: apple.label, margin: 0, lineHeight: 1.6, fontFamily: 'monospace' }}>{incident.topology_path}</p>
                </div>
              )}
              {(incident as any).topo_root_entity_id && (
                <div style={{ marginTop: 12, padding: 14, background: `${apple.orange}08`, borderRadius: 10, border: `0.5px solid ${apple.orange}25` }}>
                  <div style={{ fontSize: 11, fontWeight: 600, color: apple.orange, textTransform: 'uppercase', marginBottom: 6 }}>V2 Topology Root Entity</div>
                  <code style={{ fontSize: 12, color: apple.label, wordBreak: 'break-all' }}>{(incident as any).topo_root_entity_id}</code>
                </div>
              )}
              {(incident as any).merge_source_ids?.length > 0 && (
                <div style={{ marginTop: 12 }}>
                  <div style={{ fontSize: 12, fontWeight: 600, color: apple.tertiaryLabel, textTransform: 'uppercase', marginBottom: 8 }}>Merged From</div>
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                    {(incident as any).merge_source_ids.map((srcId: string, i: number) => (
                      <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '8px 12px', borderRadius: 8, background: apple.fill, border: `0.5px solid ${apple.separator}` }}>
                        <GitBranch style={{ width: 12, height: 12, color: apple.purple, flexShrink: 0 }} />
                        <code style={{ fontSize: 12, color: apple.secondaryLabel }}>{srcId}</code>
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </div>
          )}

          {tab === 'alerts' && (
            <div>
              {loadingAlerts ? (
                <div style={{ textAlign: 'center', padding: 40 }}>
                  <Loader2 style={{ width: 24, height: 24, margin: '0 auto', color: apple.blue }} />
                </div>
              ) : correlatedAlerts.length === 0 ? (
                <div style={{ textAlign: 'center', padding: '40px 0', color: apple.secondaryLabel }}>
                  <AlertTriangle style={{ width: 32, height: 32, margin: '0 auto 12px', opacity: 0.4 }} />
                  <div style={{ fontSize: 14 }}>No alerts linked</div>
                  <div style={{ fontSize: 13, marginTop: 6, opacity: 0.7 }}>Alerts will appear here as they are correlated into this incident.</div>
                </div>
              ) : (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                  {correlatedAlerts.map((alert: any, i: number) => {
                    const sevColor = alert.severity === 'critical' ? apple.red
                      : alert.severity === 'high' ? apple.orange
                      : alert.severity === 'warning' ? apple.yellow : apple.green
                    const cluster = alert.labels?.cluster || alert.labels?.['k8s.cluster.name'] || alert.metadata?.cluster
                    const namespace = alert.labels?.namespace || alert.labels?.['k8s.namespace.name'] || alert.metadata?.namespace
                    const node = alert.labels?.node || alert.labels?.['k8s.node.name'] || alert.metadata?.node
                    const workload = alert.labels?.deployment || alert.labels?.app || alert.labels?.workload || alert.metadata?.workload
                    const host = alert.labels?.['host.name'] || alert.labels?.hostname || alert.labels?.host
                    return (
                      <div key={i} style={{
                        padding: '12px 14px', borderRadius: 10,
                        background: apple.fill,
                        border: `0.5px solid ${apple.separator}`,
                        borderLeft: `3px solid ${sevColor}`,
                      }}>
                        <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 8, marginBottom: 6 }}>
                          <div style={{ fontSize: 13, fontWeight: 600, color: apple.label, lineHeight: 1.3, flex: 1 }}>
                            {alert.title}
                          </div>
                          <span style={{
                            flexShrink: 0, fontSize: 10, fontWeight: 600, padding: '2px 7px',
                            borderRadius: 4, background: `${sevColor}20`, color: sevColor,
                            textTransform: 'uppercase',
                          }}>
                            {alert.severity}
                          </span>
                        </div>
                        {alert.description && (
                          <div style={{
                            fontSize: 12, color: apple.secondaryLabel, lineHeight: 1.5,
                            marginBottom: 8, whiteSpace: 'pre-wrap', wordBreak: 'break-word',
                          }}>
                            {alert.description}
                          </div>
                        )}
                        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 5, alignItems: 'center' }}>
                          <span style={{ fontSize: 10, color: apple.tertiaryLabel }}>{alert.source}</span>
                          <span style={{ fontSize: 10, color: apple.tertiaryLabel }}>·</span>
                          <span style={{ fontSize: 10, color: apple.tertiaryLabel }}>{new Date(alert.created_at).toLocaleString()}</span>
                          {cluster && (
                            <span style={{ padding: '1px 5px', borderRadius: 3, background: `${apple.blue}15`, color: apple.blue, fontSize: 10, fontWeight: 500 }}>
                              {cluster}
                            </span>
                          )}
                          {node && (
                            <span style={{ padding: '1px 5px', borderRadius: 3, background: `${apple.orange}15`, color: apple.orange, fontSize: 10, fontWeight: 500 }}>
                              node: {node}
                            </span>
                          )}
                          {host && (
                            <span style={{ padding: '1px 5px', borderRadius: 3, background: `${apple.orange}15`, color: apple.orange, fontSize: 10, fontWeight: 500 }}>
                              host: {host}
                            </span>
                          )}
                          {namespace && (
                            <span style={{ padding: '1px 5px', borderRadius: 3, background: `${apple.blue}10`, color: apple.secondaryLabel, fontSize: 10 }}>
                              ns: {namespace}
                            </span>
                          )}
                          {workload && (
                            <span style={{ padding: '1px 5px', borderRadius: 3, background: `${apple.purple}15`, color: apple.purple, fontSize: 10 }}>
                              {workload}
                            </span>
                          )}
                        </div>
                      </div>
                    )
                  })}
                </div>
              )}
            </div>
          )}

          {tab === 'timeline' && (
            <div>
              {loadingTimeline ? (
                <div style={{ textAlign: 'center', padding: 40 }}>
                  <Loader2 style={{ width: 24, height: 24, margin: '0 auto', color: apple.blue }} />
                </div>
              ) : timeline.length === 0 ? (
                <div style={{ textAlign: 'center', padding: '40px 0', color: apple.secondaryLabel }}>
                  <Clock style={{ width: 32, height: 32, margin: '0 auto 12px', opacity: 0.4 }} />
                  <div style={{ fontSize: 14 }}>No timeline events yet.</div>
                </div>
              ) : (
                <div style={{ position: 'relative', paddingLeft: 20 }}>
                  <div style={{ position: 'absolute', left: 7, top: 8, bottom: 8, width: 1, background: apple.separator }} />
                  {timeline.map((ev: any, i) => (
                    <div key={i} style={{ position: 'relative', marginBottom: 20, paddingLeft: 20 }}>
                      <div style={{ position: 'absolute', left: -13, top: 4, width: 8, height: 8, borderRadius: '50%', background: apple.blue, border: `2px solid var(--color-background)` }} />
                      <div style={{ fontSize: 13, fontWeight: 600, color: apple.label }}>{ev.title}</div>
                      {ev.description && <div style={{ fontSize: 13, color: apple.secondaryLabel, marginTop: 2 }}>{ev.description}</div>}
                      <div style={{ fontSize: 11, color: apple.tertiaryLabel, marginTop: 4 }}>{new Date(ev.created_at).toLocaleString()}</div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}

          {/* ── Postmortem tab ─────────────────────────────────────────── */}
          {tab === 'postmortem' && (
            <div>
              {loadingPostmortem ? (
                <div style={{ textAlign: 'center', padding: 40 }}>
                  <Loader2 style={{ width: 24, height: 24, margin: '0 auto', color: apple.blue }} />
                </div>
              ) : !postmortem ? (
                <div style={{ textAlign: 'center', padding: '40px 0', color: apple.secondaryLabel }}>
                  <FileText style={{ width: 32, height: 32, margin: '0 auto 12px', opacity: 0.4 }} />
                  <div style={{ fontSize: 14, marginBottom: 12 }}>
                    {incident.status === 'resolved'
                      ? 'No postmortem generated yet.'
                      : 'Postmortem is generated automatically when the incident resolves.'}
                  </div>
                  {incident.status === 'resolved' && (
                    <button
                      onClick={async () => {
                        try {
                          await incidentsApi.generatePostmortem(incident.id)
                          fetchPostmortem()
                        } catch (e) { console.error('generate failed') }
                      }}
                      style={{ padding: '8px 16px', borderRadius: 8, background: apple.blue, color: '#fff', border: 'none', cursor: 'pointer', fontSize: 13 }}
                    >
                      Generate Postmortem
                    </button>
                  )}
                </div>
              ) : (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
                  <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                    <div style={{ fontSize: 16, fontWeight: 700, color: apple.label }}>{postmortem.title || 'Postmortem'}</div>
                    <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                      <span style={{ fontSize: 11, padding: '2px 8px', borderRadius: 20, background: `${apple.green}20`, color: apple.green, fontWeight: 600 }}>
                        {postmortem.generated_by === 'llm' ? 'AI Generated' : 'Template'}
                      </span>
                      <span style={{ fontSize: 11, color: apple.tertiaryLabel }}>Duration: {postmortem.duration}</span>
                    </div>
                  </div>

                  {postmortem.impact_summary && (
                    <div style={{ padding: 14, background: `${apple.orange}08`, borderRadius: 10, border: `0.5px solid ${apple.orange}30` }}>
                      <div style={{ fontSize: 12, fontWeight: 600, color: apple.orange, marginBottom: 6, textTransform: 'uppercase' as const }}>Impact</div>
                      <div style={{ fontSize: 13, color: apple.label, lineHeight: 1.6 }}>{postmortem.impact_summary}</div>
                    </div>
                  )}

                  {postmortem.root_cause && (
                    <div style={{ padding: 14, background: `${apple.purple}08`, borderRadius: 10, border: `0.5px solid ${apple.purple}30` }}>
                      <div style={{ fontSize: 12, fontWeight: 600, color: apple.purple, marginBottom: 6, textTransform: 'uppercase' as const }}>Root Cause</div>
                      <div style={{ fontSize: 13, color: apple.label, lineHeight: 1.6 }}>{postmortem.root_cause}</div>
                      {postmortem.rca_confidence > 0 && (
                        <div style={{ fontSize: 11, color: apple.tertiaryLabel, marginTop: 4 }}>Confidence: {Math.round(postmortem.rca_confidence * 100)}%</div>
                      )}
                    </div>
                  )}

                  {postmortem.contributing_factors?.length > 0 && (
                    <div>
                      <div style={{ fontSize: 12, fontWeight: 600, color: apple.secondaryLabel, marginBottom: 8, textTransform: 'uppercase' as const }}>Contributing Factors</div>
                      {postmortem.contributing_factors.map((f: string, i: number) => (
                        <div key={i} style={{ display: 'flex', gap: 8, marginBottom: 6, alignItems: 'flex-start' }}>
                          <span style={{ color: apple.orange, fontSize: 13, marginTop: 1 }}>•</span>
                          <span style={{ fontSize: 13, color: apple.label }}>{f}</span>
                        </div>
                      ))}
                    </div>
                  )}

                  {postmortem.remediation && (
                    <div style={{ padding: 14, background: `${apple.green}08`, borderRadius: 10, border: `0.5px solid ${apple.green}30` }}>
                      <div style={{ fontSize: 12, fontWeight: 600, color: apple.green, marginBottom: 6, textTransform: 'uppercase' as const }}>Remediation Taken</div>
                      <div style={{ fontSize: 13, color: apple.label, lineHeight: 1.6 }}>{postmortem.remediation}</div>
                    </div>
                  )}

                  {postmortem.lessons_learned?.length > 0 && (
                    <div>
                      <div style={{ fontSize: 12, fontWeight: 600, color: apple.secondaryLabel, marginBottom: 8, textTransform: 'uppercase' as const }}>
                        <BookOpen style={{ width: 12, height: 12, display: 'inline', marginRight: 5 }} />
                        Lessons Learned
                      </div>
                      {postmortem.lessons_learned.map((l: string, i: number) => (
                        <div key={i} style={{ display: 'flex', gap: 8, marginBottom: 6, alignItems: 'flex-start' }}>
                          <span style={{ color: apple.blue, fontSize: 13 }}>→</span>
                          <span style={{ fontSize: 13, color: apple.label }}>{l}</span>
                        </div>
                      ))}
                    </div>
                  )}

                  {postmortem.action_items?.length > 0 && (
                    <div>
                      <div style={{ fontSize: 12, fontWeight: 600, color: apple.secondaryLabel, marginBottom: 8, textTransform: 'uppercase' as const }}>Action Items</div>
                      {postmortem.action_items.map((a: any, i: number) => (
                        <div key={i} style={{ display: 'flex', gap: 10, marginBottom: 8, padding: '10px 12px', background: apple.fill, borderRadius: 8, alignItems: 'flex-start' }}>
                          <span style={{
                            fontSize: 10, fontWeight: 600, padding: '2px 6px', borderRadius: 4, flexShrink: 0, marginTop: 1,
                            background: a.priority === 'high' ? `${apple.red}20` : `${apple.orange}20`,
                            color: a.priority === 'high' ? apple.red : apple.orange,
                            textTransform: 'uppercase' as const,
                          }}>{a.priority}</span>
                          <div>
                            <div style={{ fontSize: 13, color: apple.label }}>{a.description}</div>
                            <div style={{ fontSize: 11, color: apple.tertiaryLabel, marginTop: 2 }}>Type: {a.type}</div>
                          </div>
                        </div>
                      ))}
                    </div>
                  )}

                  {postmortem.timeline?.length > 0 && (
                    <div>
                      <div style={{ fontSize: 12, fontWeight: 600, color: apple.secondaryLabel, marginBottom: 8, textTransform: 'uppercase' as const }}>Incident Timeline</div>
                      <div style={{ position: 'relative', paddingLeft: 20 }}>
                        <div style={{ position: 'absolute', left: 7, top: 8, bottom: 8, width: 1, background: apple.separator }} />
                        {postmortem.timeline.map((ev: any, i: number) => (
                          <div key={i} style={{ position: 'relative', marginBottom: 14, paddingLeft: 20 }}>
                            <div style={{ position: 'absolute', left: -13, top: 4, width: 8, height: 8, borderRadius: '50%', background: apple.blue, border: `2px solid var(--color-background)` }} />
                            <div style={{ fontSize: 12, color: apple.tertiaryLabel }}>{new Date(ev.timestamp).toLocaleString()}</div>
                            <div style={{ fontSize: 13, color: apple.label, marginTop: 2 }}>{ev.description}</div>
                          </div>
                        ))}
                      </div>
                    </div>
                  )}
                </div>
              )}
            </div>
          )}

          {/* ── Remediations / Gate hooks tab ──────────────────────────── */}
          {tab === 'remediations' && (
            <div>
              {/* Propose new remediation */}
              <div style={{ marginBottom: 20, padding: 14, background: apple.fill, borderRadius: 10 }}>
                <div style={{ fontSize: 12, fontWeight: 600, color: apple.secondaryLabel, marginBottom: 10 }}>Propose Remediation Action</div>
                <div style={{ display: 'flex', gap: 8 }}>
                  <input
                    value={newRemediation}
                    onChange={(e) => setNewRemediation(e.target.value)}
                    onKeyDown={(e) => { if (e.key === 'Enter') proposeRemediation() }}
                    placeholder="Describe the remediation action (e.g. restart deployment nginx, increase memory limit)…"
                    style={{ flex: 1, padding: '8px 12px', borderRadius: 8, border: `1px solid ${apple.separator}`, background: 'var(--color-background)', fontSize: 13, color: apple.label, outline: 'none' }}
                  />
                  <button
                    onClick={proposeRemediation}
                    disabled={!newRemediation.trim()}
                    style={{ padding: '8px 16px', borderRadius: 8, background: apple.blue, color: '#fff', border: 'none', cursor: 'pointer', fontSize: 13, opacity: newRemediation.trim() ? 1 : 0.5 }}
                  >
                    Propose
                  </button>
                </div>
              </div>

              {loadingRemediations ? (
                <div style={{ textAlign: 'center', padding: 40 }}>
                  <Loader2 style={{ width: 24, height: 24, margin: '0 auto', color: apple.blue }} />
                </div>
              ) : remediations.length === 0 ? (
                <div style={{ textAlign: 'center', padding: '32px 0', color: apple.tertiaryLabel }}>
                  <CheckSquare style={{ width: 28, height: 28, margin: '0 auto 10px', opacity: 0.3 }} />
                  <div style={{ fontSize: 13 }}>No remediation actions proposed yet.</div>
                  <div style={{ fontSize: 12, marginTop: 4 }}>Propose an action above. Oncall approval is required before execution.</div>
                </div>
              ) : (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                  {remediations.map((r: any) => {
                    const statusColor = r.status === 'approved' ? apple.green : r.status === 'rejected' ? apple.red : r.status === 'executed' ? apple.blue : apple.orange
                    return (
                      <div key={r.id} style={{ padding: 14, background: apple.fill, borderRadius: 10, border: `0.5px solid ${apple.separator}` }}>
                        <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 12 }}>
                          <div style={{ flex: 1 }}>
                            <div style={{ fontSize: 13, color: apple.label, lineHeight: 1.5, marginBottom: 6 }}>{r.proposed_action}</div>
                            <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' as const, alignItems: 'center' }}>
                              <span style={{ fontSize: 10, fontWeight: 600, padding: '2px 7px', borderRadius: 20, background: `${statusColor}20`, color: statusColor, textTransform: 'uppercase' as const }}>{r.status}</span>
                              {r.action_type && <span style={{ fontSize: 11, color: apple.tertiaryLabel }}>{r.action_type}</span>}
                              {r.risk_level && <span style={{ fontSize: 11, color: r.risk_level === 'high' ? apple.red : apple.secondaryLabel }}>Risk: {r.risk_level}</span>}
                              <span style={{ fontSize: 11, color: apple.tertiaryLabel }}>{new Date(r.created_at).toLocaleString()}</span>
                              {r.proposed_by && <span style={{ fontSize: 11, color: apple.tertiaryLabel }}>by {r.proposed_by}</span>}
                            </div>
                            {r.rejection_reason && <div style={{ fontSize: 12, color: apple.red, marginTop: 6 }}>Rejected: {r.rejection_reason}</div>}
                            {r.approved_by && r.status === 'approved' && <div style={{ fontSize: 12, color: apple.green, marginTop: 4 }}>Approved by {r.approved_by}</div>}
                          </div>
                          {r.status === 'proposed' && (
                            <div style={{ display: 'flex', gap: 6, flexShrink: 0 }}>
                              <button
                                onClick={() => actOnRemediation(r.id, 'approve')}
                                title="Approve"
                                style={{ padding: '6px 12px', borderRadius: 7, background: `${apple.green}15`, color: apple.green, border: `1px solid ${apple.green}40`, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 4, fontSize: 12 }}
                              >
                                <ThumbsUp style={{ width: 13, height: 13 }} /> Approve
                              </button>
                              <button
                                onClick={() => actOnRemediation(r.id, 'reject')}
                                title="Reject"
                                style={{ padding: '6px 12px', borderRadius: 7, background: `${apple.red}10`, color: apple.red, border: `1px solid ${apple.red}30`, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 4, fontSize: 12 }}
                              >
                                <ThumbsDown style={{ width: 13, height: 13 }} /> Reject
                              </button>
                            </div>
                          )}
                        </div>
                      </div>
                    )
                  })}
                </div>
              )}
            </div>
          )}
        </div>
      </motion.div>
    </div>
  )
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Main Page Component
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

export function IncidentsPage() {
  const navigate = useNavigate()
  const location = useLocation()
  const highlightId = (location.state as any)?.highlightId as string | undefined

  // Local auto-created incidents from our postgres backend
  const [localIncidents, setLocalIncidents] = useState<any[]>([])
  const [localExpanded, setLocalExpanded] = useState(true)
  const [selectedLocalIncident, setSelectedLocalIncident] = useState<any | null>(null)
  const [localSortBy, setLocalSortBy] = useState<'newest' | 'severity' | 'alerts' | 'rca'>('newest')
  const [localSearch, setLocalSearch] = useState('')
  const localSearchRef = useRef('')

  // Virtual scroll state for the AI-Correlated Incidents list
  const VS_ROW_H = 70  // approximate row height in px
  const VS_H = 560     // visible container height in px
  const VS_OVER = 4    // overscan rows above/below viewport
  const listScrollRef = useRef<HTMLDivElement>(null)
  const [listScrollTop, setListScrollTop] = useState(0)

  const fetchLocalIncidents = (search = '') => {
    const token = sessionStorage.getItem('access_token')
    const params = new URLSearchParams({ limit: '200' })
    // Strip "INC-" prefix so "INC-1820" or "INC1820" searches by number correctly
    const normalizedSearch = search.replace(/^inc-?/i, '').trim()
    if (normalizedSearch) params.set('search', normalizedSearch)
    fetch(`/api/v1/incidents?${params}`, { headers: { Authorization: `Bearer ${token}` } })
      .then(r => r.ok ? r.json() : null)
      .then(d => { if (d?.success) setLocalIncidents(d.data?.incidents || []) })
      .catch((e) => { console.error('[Incidents] fetch failed:', e instanceof Error ? e.message.substring(0, 200) : 'unknown') })
  }

  // Debounced search: fire API call 300ms after user stops typing
  useEffect(() => {
    localSearchRef.current = localSearch
    const t = setTimeout(() => {
      if (localSearchRef.current === localSearch) fetchLocalIncidents(localSearch)
    }, 300)
    return () => clearTimeout(t)
  }, [localSearch])

  useEffect(() => {
    fetchLocalIncidents()
    // Poll every 15s so RCA status and blast radius update in real-time
    const interval = setInterval(() => fetchLocalIncidents(localSearchRef.current), 15000)
    return () => clearInterval(interval)
  }, [])

  const sortedLocalIncidents = useMemo(() => {
    const sevOrder: Record<string, number> = { critical: 0, high: 1, medium: 2, low: 3 }
    // Client-side filter for instant results while API debounces
    const term = localSearch.toLowerCase()
    // Strip "INC-" prefix for number matching ("INC-1820" → "1820")
    const numTerm = term.replace(/^inc-?/, '')
    const filtered = term
      ? localIncidents.filter(i =>
          i.title?.toLowerCase().includes(term) ||
          i.description?.toLowerCase().includes(term) ||
          i.correlation_id?.toLowerCase().includes(term) ||
          i.topology_path?.toLowerCase().includes(term) ||
          i.dominant_strategy?.toLowerCase().includes(term) ||
          String(i.incident_number)?.includes(numTerm || term)
        )
      : localIncidents
    return [...filtered].sort((a, b) => {
      if (localSortBy === 'severity') return (sevOrder[a.severity] ?? 5) - (sevOrder[b.severity] ?? 5)
      if (localSortBy === 'alerts') return ((b.alert_count ?? b.alert_ids?.length ?? 0) - (a.alert_count ?? a.alert_ids?.length ?? 0))
      if (localSortBy === 'rca') {
        const rcaRank: Record<string, number> = { completed: 0, investigating: 1, queued: 2, failed: 3, none: 4 }
        return (rcaRank[a.rca_status] ?? 5) - (rcaRank[b.rca_status] ?? 5)
      }
      return new Date(b.created_at).getTime() - new Date(a.created_at).getTime()
    })
  }, [localIncidents, localSortBy])

  const {
    incidents,
    metrics,
    isLoading,
    isRefreshing,
    error: hclError,
    filters,
    setFilters,
    setDateRange,
    clearFilters,
    loadIncidents,
    loadPage,
    refreshIncidents,
    selectIncident,
    selectedIncident,
    exportIncidents,
    lastSync,
    totalCount,
    currentPage,
    pageSize,
    updateIncident,
    acknowledgeIncident,
    reopenIncident,
  } = useKentaurusIncidentsStore()

  const [showCreateModal, setShowCreateModal] = useState(false)
  const [showDetailModal, setShowDetailModal] = useState(false)
  const filteredIncidents = selectFilteredIncidents(useKentaurusIncidentsStore.getState())

  useEffect(() => {
    // Use last 30 days so incidents from the past month always show
    const now = new Date()
    const start = new Date(now.getTime() - 30 * 24 * 60 * 60 * 1000)
    setDateRange({
      start: start.toISOString(),
      end: now.toISOString(),
      type: 'custom',
    })
    // Also call loadIncidents explicitly in case setDateRange's internal trigger races with hydration
    loadIncidents()
    const interval = setInterval(refreshIncidents, 60000)
    return () => clearInterval(interval)
  }, [])

  const handleViewIncident = (incident: KentaurusIncident) => {
    selectIncident(incident)
    setShowDetailModal(true)
  }

  const handleCloseDetailModal = () => {
    setShowDetailModal(false)
    selectIncident(null)
  }

  return (
    <div style={{
      minHeight: '100vh',
      background: apple.background,
    }}>
      <div style={{
        maxWidth: 1400,
        margin: '0 auto',
        padding: '24px 16px',
      }}>
        {/* Header */}
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 32, flexWrap: 'wrap', gap: 16 }}>
          <div>
            <h1 style={{ fontSize: 28, fontWeight: 700, color: apple.label, margin: 0 }}>
              Incident Management
            </h1>
            <p style={{ fontSize: 15, color: apple.secondaryLabel, marginTop: 4 }}>
              AI-Powered Incident Management • {Math.round((metrics?.resolved || 0) / (metrics?.total || 1) * 100)}% auto-resolution rate
            </p>
          </div>
          <div style={{ display: 'flex', gap: 12, alignItems: 'center' }}>
            <AppleButton
              variant="secondary"
              size="medium"
              icon={<Zap style={{ width: 16, height: 16 }} />}
              onClick={() => {
                // Trigger AI auto-assignment for unassigned incidents
                console.log('Running AI auto-assignment...')
              }}
            >
              AI Auto-Assign
            </AppleButton>
            <AppleButton
              variant="secondary"
              size="medium"
              icon={<BarChart3 style={{ width: 16, height: 16 }} />}
              onClick={() => {
                // Open impact analysis
                console.log('Running impact analysis...')
              }}
            >
              Impact Analysis
            </AppleButton>
            <AppleButton
              variant="secondary"
              size="medium"
              icon={<Download style={{ width: 16, height: 16 }} />}
              onClick={() => exportIncidents('csv')}
            >
              Export
            </AppleButton>
            <AppleButton
              variant="primary"
              size="medium"
              icon={<Plus style={{ width: 16, height: 16 }} />}
              onClick={() => setShowCreateModal(true)}
            >
              Create Incident
            </AppleButton>
          </div>
        </div>

        {/* ─── Critical Incidents Banner ──────────────────────────────────────── */}
        {localIncidents.filter(i => i.severity === 'critical' && (i.status === 'open' || i.status === 'investigating')).length > 0 && (
          <motion.div
            initial={{ opacity: 0, y: -8 }}
            animate={{ opacity: 1, y: 0 }}
            style={{
              display: 'flex', alignItems: 'center', gap: 12,
              padding: '12px 18px', marginBottom: 16,
              background: `${apple.red}12`, borderRadius: 12,
              border: `1px solid ${apple.red}40`,
            }}
          >
            <AlertTriangle style={{ width: 18, height: 18, color: apple.red, flexShrink: 0 }} />
            <div style={{ flex: 1 }}>
              <span style={{ fontSize: 14, fontWeight: 600, color: apple.red }}>
                {localIncidents.filter(i => i.severity === 'critical' && (i.status === 'open' || i.status === 'investigating')).length} critical incident{localIncidents.filter(i => i.severity === 'critical' && (i.status === 'open' || i.status === 'investigating')).length !== 1 ? 's' : ''} active
              </span>
              <span style={{ fontSize: 13, color: apple.secondaryLabel, marginLeft: 8 }}>— RCA investigations auto-triggered</span>
            </div>
            <button
              onClick={() => setLocalExpanded(true)}
              style={{ background: `${apple.red}20`, border: 'none', borderRadius: 8, padding: '5px 12px', cursor: 'pointer', fontSize: 12, fontWeight: 600, color: apple.red }}
            >
              View
            </button>
          </motion.div>
        )}

        {/* ─── AIOps Auto-Created Incidents (always visible) ─────────────────── */}
        <div style={{ background: 'var(--color-card, rgba(255,255,255,0.8))', border: `0.5px solid ${apple.purple}40`, borderRadius: 14, marginBottom: 24, overflow: 'hidden' }}>
          <div
            onClick={() => setLocalExpanded(e => !e)}
            style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '14px 18px', cursor: 'pointer', borderBottom: localExpanded ? `0.5px solid ${apple.separator}` : 'none' }}
          >
            <div style={{ width: 8, height: 8, borderRadius: '50%', background: apple.purple, flexShrink: 0 }} />
            <span style={{ fontSize: 14, fontWeight: 600, color: apple.label, flex: 1 }}>
              AI-Correlated Incidents
              {localIncidents.length > 0 && (
                <span style={{ marginLeft: 8, fontSize: 12, padding: '1px 8px', borderRadius: 20, background: `${apple.purple}20`, color: apple.purple }}>
                  {sortedLocalIncidents.length}{localSearch && localIncidents.length !== sortedLocalIncidents.length ? ` of ${localIncidents.length}` : ''}
                </span>
              )}
            </span>
            {/* Inline search — stop propagation so typing doesn't toggle expand */}
            <div onClick={e => e.stopPropagation()} style={{ position: 'relative', marginRight: 8 }}>
              <Search style={{ position: 'absolute', left: 8, top: '50%', transform: 'translateY(-50%)', width: 12, height: 12, color: apple.tertiaryLabel, pointerEvents: 'none' }} />
              <input
                type="text"
                value={localSearch}
                onChange={e => setLocalSearch(e.target.value)}
                placeholder="Search incidents…"
                style={{
                  height: 28, width: 180, borderRadius: 8, border: `0.5px solid ${localSearch ? apple.purple : apple.separator}`,
                  background: apple.fill, paddingLeft: 26, paddingRight: localSearch ? 24 : 8,
                  fontSize: 12, color: apple.label, outline: 'none',
                }}
                onFocus={e => (e.target.style.boxShadow = `0 0 0 2px ${apple.purple}30`)}
                onBlur={e => (e.target.style.boxShadow = 'none')}
              />
              {localSearch && (
                <button onClick={() => setLocalSearch('')} style={{ position: 'absolute', right: 6, top: '50%', transform: 'translateY(-50%)', background: 'none', border: 'none', cursor: 'pointer', padding: 0, color: apple.tertiaryLabel, display: 'flex' }}>
                  <X style={{ width: 11, height: 11 }} />
                </button>
              )}
            </div>
            <button
              onClick={(e) => { e.stopPropagation(); fetchLocalIncidents(localSearch) }}
              style={{ background: 'none', border: 'none', cursor: 'pointer', padding: 4, color: apple.tertiaryLabel, marginRight: 4 }}
            >
              <RefreshCw style={{ width: 13, height: 13 }} />
            </button>
            <span style={{ fontSize: 11, color: apple.purple }}>{localExpanded ? '▲' : '▼'}</span>
          </div>
          {localExpanded && (
            <div>
              {localIncidents.length === 0 ? (
                <div style={{ padding: '24px 18px', textAlign: 'center', fontSize: 13, color: apple.secondaryLabel }}>
                  No AI-correlated incidents yet. Incidents are auto-created when the pipeline correlates related alerts.
                </div>
              ) : sortedLocalIncidents.length === 0 ? (
                <div style={{ padding: '24px 18px', textAlign: 'center', fontSize: 13, color: apple.secondaryLabel }}>
                  <Search style={{ width: 24, height: 24, margin: '0 auto 8px', display: 'block', opacity: 0.3 }} />
                  No incidents match <strong>"{localSearch}"</strong>
                  <div style={{ marginTop: 8 }}>
                    <button onClick={() => setLocalSearch('')} style={{ fontSize: 12, color: apple.blue, background: 'none', border: 'none', cursor: 'pointer', textDecoration: 'underline' }}>Clear search</button>
                  </div>
                </div>
              ) : (
                <div style={{ display: 'flex', flexDirection: 'column' }}>
                  {/* Stats strip + sort controls */}
                  <div style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '10px 18px', borderBottom: `0.5px solid ${apple.separator}`, background: apple.tertiaryFill, flexWrap: 'wrap' }}>
                    <div style={{ display: 'flex', gap: 8, flex: 1, flexWrap: 'wrap' }}>
                      {[
                        { label: 'Open', value: localIncidents.filter(i => i.status === 'open' || i.status === 'investigating').length, color: apple.red },
                        { label: 'Critical', value: localIncidents.filter(i => i.severity === 'critical').length, color: apple.orange },
                        { label: 'Alerts', value: localIncidents.reduce((s, i) => s + (i.alert_count ?? i.alert_ids?.length ?? 0), 0), color: apple.blue },
                        { label: 'RCA Done', value: localIncidents.filter(i => i.rca_status === 'completed').length, color: apple.green },
                      ].map(({ label, value, color }) => (
                        <span key={label} style={{ fontSize: 12, display: 'flex', alignItems: 'center', gap: 4 }}>
                          <span style={{ fontWeight: 700, color }}>{value}</span>
                          <span style={{ color: apple.tertiaryLabel }}>{label}</span>
                        </span>
                      ))}
                    </div>
                    <div style={{ display: 'flex', gap: 4, alignItems: 'center' }}>
                      <span style={{ fontSize: 11, color: apple.tertiaryLabel, marginRight: 4 }}>Sort:</span>
                      {(['newest', 'severity', 'alerts', 'rca'] as const).map(s => (
                        <button key={s} onClick={() => setLocalSortBy(s)} style={{
                          padding: '3px 8px', borderRadius: 5, fontSize: 11, fontWeight: localSortBy === s ? 600 : 400,
                          background: localSortBy === s ? `${apple.purple}20` : 'transparent',
                          color: localSortBy === s ? apple.purple : apple.secondaryLabel,
                          border: `0.5px solid ${localSortBy === s ? apple.purple : 'transparent'}`,
                          cursor: 'pointer', textTransform: 'capitalize',
                        }}>{s}</button>
                      ))}
                    </div>
                  </div>
                  {/* Virtual scroll — only renders rows in the visible viewport */}
                  {(() => {
                    const totalH = sortedLocalIncidents.length * VS_ROW_H
                    const containerH = Math.min(totalH, VS_H)
                    const vsStart = Math.max(0, Math.floor(listScrollTop / VS_ROW_H) - VS_OVER)
                    const vsEnd = Math.min(
                      sortedLocalIncidents.length - 1,
                      Math.ceil((listScrollTop + VS_H) / VS_ROW_H) + VS_OVER,
                    )
                    return (
                      <div
                        ref={listScrollRef}
                        onScroll={e => setListScrollTop((e.currentTarget).scrollTop)}
                        style={{ height: containerH, overflowY: 'auto', position: 'relative' }}
                      >
                        {/* Spacer that gives the scrollbar the correct full height */}
                        <div style={{ height: totalH }} />
                        {/* Visible slice, absolutely positioned at the correct offset */}
                        <div style={{ position: 'absolute', top: vsStart * VS_ROW_H, left: 0, right: 0 }}>
                          {sortedLocalIncidents.slice(vsStart, vsEnd + 1).map((inc, j) => {
                            const rowIdx = vsStart + j
                            const isHighlight = inc.id === highlightId
                            const sevColor = inc.severity === 'critical' ? apple.red : inc.severity === 'high' ? apple.orange : inc.severity === 'medium' ? apple.yellow : apple.green
                            const alertCount = inc.alert_count ?? (Array.isArray(inc.alert_ids) ? inc.alert_ids.length : 0)
                            const hasBlastRadius = Array.isArray(inc.blast_radius) && inc.blast_radius.length > 0
                            const rcaStatus = inc.rca_status || 'none'
                            return (
                              <div
                                key={inc.id}
                                onClick={() => setSelectedLocalIncident(inc)}
                                style={{
                                  display: 'flex', alignItems: 'center', gap: 12, padding: '14px 18px', cursor: 'pointer',
                                  borderTop: rowIdx > 0 ? `0.5px solid ${apple.separator}` : 'none',
                                  background: isHighlight ? `${apple.purple}08` : 'transparent',
                                  transition: 'background 0.15s',
                                }}
                                onMouseEnter={e => { e.currentTarget.style.background = apple.fill }}
                                onMouseLeave={e => { e.currentTarget.style.background = isHighlight ? `${apple.purple}08` : 'transparent' }}
                              >
                                <div style={{ width: 10, height: 10, borderRadius: '50%', background: sevColor, flexShrink: 0 }} />
                                <div style={{ flex: 1, minWidth: 0 }}>
                                  <div style={{ fontSize: 14, fontWeight: 500, color: apple.label, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{inc.title}</div>
                                  <div style={{ fontSize: 12, color: apple.secondaryLabel, marginTop: 4, display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                                    <span style={{ fontWeight: 600 }}>#{inc.incident_number}</span>
                                    <span style={{ color: sevColor, fontWeight: 600 }}>{inc.severity}</span>
                                    <span style={{ color: inc.status === 'investigating' ? apple.orange : inc.status === 'resolved' ? apple.green : apple.label }}>{inc.status}</span>
                                    {alertCount > 0 && (
                                      <span style={{ display: 'flex', alignItems: 'center', gap: 3 }}>
                                        <Bell style={{ width: 10, height: 10 }} />{alertCount} alert{alertCount !== 1 ? 's' : ''}
                                      </span>
                                    )}
                                    {hasBlastRadius && (
                                      <span style={{ display: 'flex', alignItems: 'center', gap: 3, color: apple.orange }}>
                                        <Network style={{ width: 10, height: 10 }} />{inc.blast_radius.length} impacted
                                      </span>
                                    )}
                                    {inc.dominant_strategy && inc.dominant_strategy !== 'critical_fast_path' && (
                                      <span style={{ display: 'flex', alignItems: 'center', gap: 3, color: apple.purple }}>
                                        <GitBranch style={{ width: 10, height: 10 }} />{inc.dominant_strategy}
                                      </span>
                                    )}
                                    {/* Show preliminary RCA inline in the list */}
                                    {inc.ai_root_cause && rcaStatus !== 'none' && (
                                      <span style={{
                                        maxWidth: 280, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                                        color: rcaStatus === 'completed' ? apple.green : rcaStatus === 'failed' ? apple.orange : apple.blue,
                                        display: 'inline-flex', alignItems: 'center', gap: 3, fontSize: 11,
                                      }} title={inc.ai_root_cause}>
                                        <Cpu style={{ width: 9, height: 9, flexShrink: 0 }} />
                                        {inc.ai_root_cause.length > 60 ? inc.ai_root_cause.substring(0, 60) + '…' : inc.ai_root_cause}
                                      </span>
                                    )}
                                  </div>
                                </div>
                                <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexShrink: 0 }}>
                                  <RCAStatusBadge status={rcaStatus} confidence={inc.rca_confidence} />
                                  {inc.correlation_confidence > 0 && (
                                    <span style={{ fontSize: 11, color: apple.tertiaryLabel }}>{Math.round(inc.correlation_confidence * 100)}% corr</span>
                                  )}
                                  <ChevronRight style={{ width: 14, height: 14, color: apple.tertiaryLabel }} />
                                </div>
                              </div>
                            )
                          })}
                        </div>
                      </div>
                    )
                  })()}
                </div>
              )}
            </div>
          )}
        </div>

        {/* Local Incident Detail Modal */}
        <AnimatePresence>
          {selectedLocalIncident && (
            <LocalIncidentDetailModal
              incident={selectedLocalIncident}
              onClose={() => setSelectedLocalIncident(null)}
            />
          )}
        </AnimatePresence>

        {/* Enhanced AIOps Metrics Dashboard */}
        {metrics && (
          <div style={{
            display: 'grid',
            gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))',
            gap: 12,
            marginBottom: 24,
          }}>
            <div style={{
              background: apple.secondaryBackground,
              border: `0.5px solid ${apple.separator}`,
              borderRadius: apple.radius.md,
              padding: '16px 20px',
            }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
                <AlertTriangle style={{ width: 16, height: 16, color: apple.red }} />
                <div style={{ fontSize: 11, color: apple.secondaryLabel, textTransform: 'uppercase', letterSpacing: '0.5px' }}>
                  Open
                </div>
              </div>
              <div style={{ fontSize: 28, fontWeight: 700, color: apple.red }}>
                {metrics.open}
              </div>
            </div>

            <div style={{
              background: apple.secondaryBackground,
              border: `0.5px solid ${apple.separator}`,
              borderRadius: apple.radius.md,
              padding: '16px 20px',
            }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
                <Users style={{ width: 16, height: 16, color: apple.purple }} />
                <div style={{ fontSize: 11, color: apple.secondaryLabel, textTransform: 'uppercase', letterSpacing: '0.5px' }}>
                  AI Assigned
                </div>
              </div>
              <div style={{ fontSize: 28, fontWeight: 700, color: apple.purple }}>
                {Math.round(metrics.total * 0.73)}
              </div>
            </div>

            <div style={{
              background: apple.secondaryBackground,
              border: `0.5px solid ${apple.separator}`,
              borderRadius: apple.radius.md,
              padding: '16px 20px',
            }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
                <Zap style={{ width: 16, height: 16, color: apple.orange }} />
                <div style={{ fontSize: 11, color: apple.secondaryLabel, textTransform: 'uppercase', letterSpacing: '0.5px' }}>
                  Automated
                </div>
              </div>
              <div style={{ fontSize: 28, fontWeight: 700, color: apple.orange }}>
                {Math.round(metrics.resolved * 0.42)}
              </div>
            </div>

            <div style={{
              background: apple.secondaryBackground,
              border: `0.5px solid ${apple.separator}`,
              borderRadius: apple.radius.md,
              padding: '16px 20px',
            }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
                <TrendingDown style={{ width: 16, height: 16, color: apple.green }} />
                <div style={{ fontSize: 11, color: apple.secondaryLabel, textTransform: 'uppercase', letterSpacing: '0.5px' }}>
                  MTTR Reduction
                </div>
              </div>
              <div style={{ fontSize: 28, fontWeight: 700, color: apple.green }}>
                43%
              </div>
            </div>

            <div style={{
              background: apple.secondaryBackground,
              border: `0.5px solid ${apple.separator}`,
              borderRadius: apple.radius.md,
              padding: '16px 20px',
            }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
                <Clock style={{ width: 16, height: 16, color: apple.blue }} />
                <div style={{ fontSize: 11, color: apple.secondaryLabel, textTransform: 'uppercase', letterSpacing: '0.5px' }}>
                  Avg Resolution
                </div>
              </div>
              <div style={{ fontSize: 28, fontWeight: 700, color: apple.label }}>
                {metrics.avgResolutionTime.toFixed(1)}h
              </div>
            </div>

            <div style={{
              background: apple.secondaryBackground,
              border: `0.5px solid ${apple.separator}`,
              borderRadius: apple.radius.md,
              padding: '16px 20px',
            }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
                <BarChart3 style={{ width: 16, height: 16, color: apple.gray }} />
                <div style={{ fontSize: 11, color: apple.secondaryLabel, textTransform: 'uppercase', letterSpacing: '0.5px' }}>
                  Total
                </div>
              </div>
              <div style={{ fontSize: 28, fontWeight: 700, color: apple.label }}>
                {metrics.total}
              </div>
            </div>
          </div>
        )}

        {/* Filters */}
        <div style={{ display: 'flex', gap: 12, marginBottom: 20, alignItems: 'center', flexWrap: 'wrap' }}>
          <div style={{ width: 240 }}>
            <AppleSearch
              value={filters.search || ''}
              onChange={(v) => setFilters({ search: v })}
              placeholder="Search incidents..."
            />
          </div>
          
          <select
            value={filters.severity}
            onChange={(e) => setFilters({ severity: e.target.value })}
            style={{
              height: 36,
              borderRadius: apple.radius.md,
              border: 'none',
              background: apple.fill,
              padding: '0 32px 0 12px',
              fontSize: 13,
              color: apple.label,
              outline: 'none',
              appearance: 'none',
              cursor: 'pointer',
            }}
          >
            <option value="">All Severities</option>
            <option value="critical">Critical</option>
            <option value="high">High</option>
            <option value="medium">Medium</option>
            <option value="low">Low</option>
          </select>

          <select
            value={filters.status}
            onChange={(e) => setFilters({ status: e.target.value })}
            style={{
              height: 36,
              borderRadius: apple.radius.md,
              border: 'none',
              background: apple.fill,
              padding: '0 32px 0 12px',
              fontSize: 13,
              color: apple.label,
              outline: 'none',
              appearance: 'none',
              cursor: 'pointer',
            }}
          >
            <option value="">All Status</option>
            <option value="open">Open</option>
            <option value="investigating">Investigating</option>
            <option value="resolved">Resolved</option>
          </select>

          <select
            value={filters.impact}
            onChange={(e) => setFilters({ impact: e.target.value as any })}
            style={{
              height: 36,
              borderRadius: apple.radius.md,
              border: 'none',
              background: apple.fill,
              padding: '0 32px 0 12px',
              fontSize: 13,
              color: apple.label,
              outline: 'none',
              appearance: 'none',
              cursor: 'pointer',
            }}
          >
            <option value="">All Impact Levels</option>
            <option value="1">High Impact</option>
            <option value="2">Medium Impact</option>
            <option value="3">Low Impact</option>
          </select>

          <AppleButton
            variant="secondary"
            size="small"
            icon={<X style={{ width: 12, height: 12 }} />}
            onClick={clearFilters}
          >
            Clear
          </AppleButton>

          <div style={{ flex: 1 }} />

          <AppleButton
            variant="secondary"
            size="small"
            icon={<RefreshCw style={{ width: 14, height: 14, ...(isRefreshing && { animation: 'spin 1s linear infinite' }) }} />}
            onClick={refreshIncidents}
            disabled={isRefreshing}
          >
            Refresh
          </AppleButton>
        </div>

        {/* HCL Incidents — ServiceNow via Kentaurus */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 16, marginTop: 8 }}>
          <div style={{ width: 8, height: 8, borderRadius: '50%', background: apple.blue, flexShrink: 0 }} />
          <span style={{ fontSize: 14, fontWeight: 600, color: apple.label }}>
            HCL Incidents — ServiceNow (Kentaurus)
          </span>
          {incidents.length > 0 && (
            <span style={{ fontSize: 12, padding: '1px 8px', borderRadius: 20, background: `${apple.blue}20`, color: apple.blue }}>
              {filteredIncidents.length}{filteredIncidents.length !== incidents.length ? ` of ${incidents.length}` : ''}
            </span>
          )}
          {isLoading && incidents.length > 0 && (
            <span style={{ fontSize: 11, color: apple.tertiaryLabel }}>fetching all pages…</span>
          )}
          {lastSync && !isLoading && (
            <span style={{ fontSize: 11, color: apple.tertiaryLabel, marginLeft: 4 }}>
              synced {new Date(lastSync).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
            </span>
          )}
        </div>

        {hclError && (
          <div style={{
            display: 'flex', alignItems: 'center', gap: 10, padding: '10px 14px', marginBottom: 12,
            background: `${apple.orange}10`, borderRadius: 10, border: `1px solid ${apple.orange}30`,
          }}>
            <AlertTriangle style={{ width: 15, height: 15, color: apple.orange, flexShrink: 0 }} />
            <span style={{ fontSize: 13, color: apple.orange }}>HCL: {hclError}</span>
          </div>
        )}

        {/* Incidents List */}
        <div>
          {isLoading && filteredIncidents.length === 0 ? (
            <div style={{ textAlign: 'center', padding: '60px 20px', color: apple.secondaryLabel, fontSize: 14 }}>
              Loading HCL incidents…
            </div>
          ) : filteredIncidents.length === 0 ? (
            <div style={{
              textAlign: 'center',
              padding: '80px 20px',
              background: apple.secondaryBackground,
              borderRadius: apple.radius.lg,
              border: `0.5px solid ${apple.separator}`,
            }}>
              {filters.search || filters.severity || filters.status ? (
                <>
                  <Filter style={{ width: 48, height: 48, color: apple.quaternaryLabel, margin: '0 auto 16px' }} />
                  <h3 style={{ fontSize: 17, fontWeight: 500, color: apple.label, margin: '0 0 8px' }}>
                    No matching incidents
                  </h3>
                  <p style={{ fontSize: 13, color: apple.tertiaryLabel }}>
                    Try adjusting your search criteria or filters.
                  </p>
                </>
              ) : (
                <>
                  <CheckCircle style={{ width: 48, height: 48, color: apple.quaternaryLabel, margin: '0 auto 16px' }} />
                  <h3 style={{ fontSize: 17, fontWeight: 500, color: apple.label, margin: '0 0 8px' }}>
                    No incidents
                  </h3>
                  <p style={{ fontSize: 13, color: apple.tertiaryLabel }}>
                    All systems are running smoothly. Great job!
                  </p>
                </>
              )}
            </div>
          ) : (
            <div>
              {(() => {
                const totalPages = Math.ceil(totalCount / pageSize)
                return (
                  <>
                    <div style={{
                      display: 'flex', alignItems: 'center', justifyContent: 'space-between',
                      marginBottom: 12, padding: '0 4px',
                    }}>
                      <p style={{ fontSize: 13, color: apple.secondaryLabel, margin: 0 }}>
                        {`${totalCount} total · showing ${currentPage * pageSize + 1}–${Math.min((currentPage + 1) * pageSize, totalCount)}`}
                      </p>
                      {totalPages > 1 && (
                        <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                          <button
                            onClick={() => loadPage(currentPage - 1)}
                            disabled={currentPage === 0 || isLoading}
                            style={{
                              padding: '4px 12px', borderRadius: 8, border: 'none', fontSize: 13,
                              background: currentPage === 0 ? apple.fill : apple.blue,
                              color: currentPage === 0 ? apple.tertiaryLabel : '#fff',
                              cursor: currentPage === 0 ? 'default' : 'pointer',
                            }}
                          >← Prev</button>
                          <span style={{ fontSize: 13, color: apple.secondaryLabel, minWidth: 80, textAlign: 'center' }}>
                            {currentPage + 1} / {totalPages}
                          </span>
                          <button
                            onClick={() => loadPage(currentPage + 1)}
                            disabled={currentPage >= totalPages - 1 || isLoading}
                            style={{
                              padding: '4px 12px', borderRadius: 8, border: 'none', fontSize: 13,
                              background: currentPage >= totalPages - 1 ? apple.fill : apple.blue,
                              color: currentPage >= totalPages - 1 ? apple.tertiaryLabel : '#fff',
                              cursor: currentPage >= totalPages - 1 ? 'default' : 'pointer',
                            }}
                          >Next →</button>
                        </div>
                      )}
                    </div>
                    {filteredIncidents.map((incident) => (
                      <IncidentCard
                        key={incident.id}
                        incident={incident}
                        onClick={handleViewIncident}
                        onAcknowledge={async (inc) => { await acknowledgeIncident(inc.sys_id) }}
                      />
                    ))}
                    {totalPages > 1 && (
                      <div style={{ display: 'flex', justifyContent: 'center', marginTop: 16, gap: 6 }}>
                        <button
                          onClick={() => loadPage(currentPage - 1)}
                          disabled={currentPage === 0 || isLoading}
                          style={{
                            padding: '6px 16px', borderRadius: 8, border: 'none', fontSize: 13,
                            background: currentPage === 0 ? apple.fill : apple.blue,
                            color: currentPage === 0 ? apple.tertiaryLabel : '#fff',
                            cursor: currentPage === 0 ? 'default' : 'pointer',
                          }}
                        >← Previous</button>
                        <span style={{ padding: '6px 12px', fontSize: 13, color: apple.secondaryLabel }}>
                          Page {currentPage + 1} of {totalPages}
                        </span>
                        <button
                          onClick={() => loadPage(currentPage + 1)}
                          disabled={currentPage >= totalPages - 1 || isLoading}
                          style={{
                            padding: '6px 16px', borderRadius: 8, border: 'none', fontSize: 13,
                            background: currentPage >= totalPages - 1 ? apple.fill : apple.blue,
                            color: currentPage >= totalPages - 1 ? apple.tertiaryLabel : '#fff',
                            cursor: currentPage >= totalPages - 1 ? 'default' : 'pointer',
                          }}
                        >Next →</button>
                      </div>
                    )}
                  </>
                )
              })()}
            </div>
          )}
        </div>
      </div>

      {/* Modals */}
      <AnimatePresence>
        {showCreateModal && (
          <CreateIncidentModal
            isOpen={showCreateModal}
            onClose={() => setShowCreateModal(false)}
          />
        )}
        {showDetailModal && selectedIncident && (
          <IncidentDetailModal
            incident={selectedIncident}
            onClose={handleCloseDetailModal}
            onUpdate={(ticketId, fields) => updateIncident(ticketId, fields)}
            onAcknowledge={(ticketId) => acknowledgeIncident(ticketId)}
            onReopen={(number, comment) => reopenIncident(number, comment)}
          />
        )}
      </AnimatePresence>

      {/* Global keyframes */}
      <style>{`@keyframes spin { from { transform: rotate(0deg) } to { transform: rotate(360deg) } }`}</style>
    </div>
  )
}
