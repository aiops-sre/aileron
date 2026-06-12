import React, { useState } from 'react'
import { motion } from 'framer-motion'
import {
  Check, CheckCheck, UserPlus, ExternalLink,
  Server, Cpu, Box, Globe, Layers, Activity, Network,
  Link2, GitMerge, Shield, X,
} from 'lucide-react'
import type { Alert } from '@/types'

const c = {
  blue: '#007AFF',
  green: '#34C759',
  red: '#FF3B30',
  orange: '#FF9500',
  yellow: '#FFCC00',
  purple: '#AF52DE',
  teal: '#30B0C7',
  indigo: '#5856D6',
  gray: '#8E8E93',
  label: 'var(--color-text)',
  secondaryLabel: 'var(--color-text-secondary)',
  tertiaryLabel: 'var(--color-text-tertiary, #8E8E93)',
  separator: 'var(--color-separator, rgba(142, 142, 147, 0.12))',
  fill: 'var(--color-fill, rgba(142, 142, 147, 0.08))',
  bg: 'var(--color-card, rgba(255, 255, 255, 0.8))',
} as const

// Column layout constants — must match AlertsPage column headers exactly
export const COL = {
  sev: 72,      // Severity pill
  status: 96,   // Status pill
  zone: 136,    // Source / Zone
  flags: 50,    // Flags / Impact
  age: 40,      // Age
  assignee: 26, // Assignee avatar (always reserved)
  actions: 80,  // Action buttons
  gap: 8,
} as const

const SEV_CFG: Record<string, { label: string; color: string; bg: string }> = {
  critical: { label: 'Critical', color: '#CC1F11', bg: 'rgba(255,59,48,0.10)' },
  high:     { label: 'High',     color: '#C25000', bg: 'rgba(255,107,0,0.10)' },
  medium:   { label: 'Medium',   color: '#8A6800', bg: 'rgba(255,204,0,0.12)' },
  low:      { label: 'Low',      color: '#0062CC', bg: 'rgba(0,122,255,0.09)' },
}

const SEV_DOT: Record<string, string> = {
  critical: '#FF3B30', high: '#FF9500', medium: '#FFCC00', low: '#007AFF',
}

const STATUS_CFG: Record<string, { label: string; color: string; bg: string }> = {
  open:          { label: 'Open',          color: '#CC1F11', bg: 'rgba(255,59,48,0.09)'  },
  acknowledged:  { label: 'Acknowledged',  color: '#C25000', bg: 'rgba(255,149,0,0.09)' },
  investigating: { label: 'Investigating', color: '#0062CC', bg: 'rgba(0,122,255,0.09)' },
  resolved:      { label: 'Resolved',      color: '#1A7F3C', bg: 'rgba(52,199,89,0.09)' },
  closed:        { label: 'Closed',        color: '#636366', bg: 'rgba(99,99,102,0.09)' },
  suppressed:    { label: 'Suppressed',    color: '#8E8E93', bg: 'rgba(142,142,147,0.09)'},
}

const SOURCE_LABEL: Record<string, string> = {
  dynatrace:  'Dynatrace',
  prometheus: 'Prometheus',
  grafana:    'Grafana',
  splunk:     'Splunk',
  webhook:    'Webhook',
  generic:    'Generic',
  pagerduty:  'PagerDuty',
}

const ENTITY_ICON: Record<string, React.ElementType> = {
  host: Server, vm: Server, 'virtual machine': Server,
  node: Cpu, kubernetes_node: Cpu,
  pod: Box, container: Box,
  service: Layers, process_group: Layers,
  application: Globe,
  network: Network,
}

function entityIcon(type?: string): React.ElementType {
  if (!type) return Activity
  const key = type.toLowerCase()
  for (const k of Object.keys(ENTITY_ICON)) {
    if (key.includes(k)) return ENTITY_ICON[k]
  }
  return Activity
}

function timeAgo(ts?: string): string {
  if (!ts) return ''
  const diff = Date.now() - new Date(ts).getTime()
  const m = Math.floor(diff / 60000)
  if (m < 1) return 'now'
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h`
  return `${Math.floor(h / 24)}d`
}

function absTime(ts?: string): string {
  if (!ts) return ''
  return new Date(ts).toLocaleString(undefined, {
    month: 'short', day: 'numeric',
    hour: '2-digit', minute: '2-digit',
  })
}

function MetaChip({ label, color, bg, icon: Icon }: {
  label: string; color: string; bg: string; icon?: React.ElementType
}) {
  return (
    <span style={{
      display: 'inline-flex', alignItems: 'center', gap: 3,
      fontSize: 10, fontWeight: 500, padding: '1px 6px',
      borderRadius: 4, background: bg, color, flexShrink: 0,
      maxWidth: 110, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
    }}>
      {Icon && <Icon style={{ width: 9, height: 9, flexShrink: 0 }} />}
      {label}
    </span>
  )
}

function ActionBtn({ icon: Icon, label, color, bg, onClick }: {
  icon: React.ElementType; label: string; color: string; bg: string
  onClick: (e: React.MouseEvent) => void
}) {
  const [hover, setHover] = useState(false)
  return (
    <button
      onClick={onClick}
      title={label}
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
      style={{
        height: 26, borderRadius: 5, border: 'none',
        background: hover ? bg : c.fill,
        cursor: 'pointer', display: 'flex',
        alignItems: 'center', justifyContent: 'center',
        flexShrink: 0, transition: 'background 0.12s',
        padding: '0 7px', gap: 4,
      }}
    >
      <Icon style={{ width: 12, height: 12, color: hover ? color : c.tertiaryLabel, transition: 'color 0.12s' }} />
      {hover && (
        <span style={{ fontSize: 10, fontWeight: 500, color, whiteSpace: 'nowrap' }}>{label}</span>
      )}
    </button>
  )
}

// Inline assign input — avoids prompt()
function AssignInput({ onSubmit, onCancel }: {
  onSubmit: (uid: string) => void
  onCancel: () => void
}) {
  const [val, setVal] = useState('')
  return (
    <div
      style={{
        display: 'flex', alignItems: 'center', gap: 4,
        background: c.bg, border: `1px solid ${c.blue}`,
        borderRadius: 6, padding: '2px 4px', flexShrink: 0,
      }}
      onClick={(e) => e.stopPropagation()}
    >
      <input
        autoFocus
        value={val}
        onChange={(e) => setVal(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && val.trim()) onSubmit(val.trim())
          if (e.key === 'Escape') onCancel()
        }}
        placeholder="User ID or email"
        style={{
          border: 'none', outline: 'none', background: 'transparent',
          fontSize: 11, color: c.label, width: 120,
        }}
      />
      <button
        onClick={() => val.trim() && onSubmit(val.trim())}
        style={{ background: 'none', border: 'none', cursor: 'pointer', padding: 0 }}
      >
        <Check style={{ width: 12, height: 12, color: c.green }} />
      </button>
      <button
        onClick={onCancel}
        style={{ background: 'none', border: 'none', cursor: 'pointer', padding: 0 }}
      >
        <X style={{ width: 12, height: 12, color: c.gray }} />
      </button>
    </div>
  )
}

export interface AlertCardProps {
  alert: Alert
  pNumber?: number
  bulkMode?: boolean
  isSelected?: boolean
  onSelect?: (alert: Alert) => void
  onCardClick?: (alert: Alert) => void
  onAcknowledge?: (alert: Alert) => void
  onResolve?: (alert: Alert) => void
  onAssign?: (alert: Alert, userId: string) => void
  onMaintenance?: (alert: Alert) => void
  onDynatraceLink?: (alert: Alert) => void
}

export function AlertCard({
  alert,
  pNumber,
  bulkMode = false,
  isSelected = false,
  onSelect,
  onCardClick,
  onAcknowledge,
  onResolve,
  onAssign,
  onDynatraceLink,
}: AlertCardProps) {
  const [hovered, setHovered] = useState(false)
  const [assigning, setAssigning] = useState(false)

  const sevCfg  = SEV_CFG[alert.severity]  ?? SEV_CFG.low
  const sevDot  = SEV_DOT[alert.severity]  ?? c.gray
  const statCfg = STATUS_CFG[alert.status] ?? STATUS_CFG.open

  const m = alert.metadata ?? {}
  const l = alert.labels ?? {}

  const entityType = l.entity_type || m.entity_type || m.dynatrace_entity_type
  const hostname   = alert.hostname || l.hostname || m.host || m.hostname
  const hostIP     = alert.ip_address || l.ip || m.ip_address
  const workload   = alert.workload || l.workload || m.workload
  const namespace  = alert.namespace || l.namespace || m.namespace
  const cluster    = alert.cluster || l.cluster || m.cluster
  const mgmtZone   = l.management_zone || m.management_zone
  const dtUrl      = m.dynatrace_problem_url || m.problemUrl
  const dtImpact   = l.impact || m.dynatrace_impact
  const rootCause  = l.root_cause_entity || m.dynatrace_root_cause
  const impacted   = m.dynatrace_impacted_entities
  const impactedCnt = Array.isArray(impacted) ? impacted.length
    : typeof impacted === 'number' ? impacted : undefined
  const occurrences  = (alert.count && alert.count > 1) ? alert.count
    : (m.count || m.duplicate_count || 1)
  const isCorrelated = !!alert.correlation_id
  const isLinked     = !!(alert.linked_incident_id || m.linked_incident_id || m.incident_id)
  const slaBreached  = alert.sla_met_response_time === false || alert.sla_met_resolution_time === false
  const corrScore    = alert.correlation_score
  const assigneeName = alert.assigned_to_name || (alert.assigned_to ? alert.assigned_to.slice(0, 8) : undefined)
  const assigneeInitials = assigneeName
    ? assigneeName.split(/[ _@.-]/).filter(Boolean).map(w => w[0]).join('').toUpperCase().slice(0, 2)
    : undefined

  const EntityIcon = entityIcon(entityType)
  const stop = (e: React.MouseEvent) => e.stopPropagation()

  const sourceLabel = SOURCE_LABEL[alert.source?.toLowerCase() ?? ''] ?? alert.source ?? 'System'

  return (
    <motion.div
      initial={{ opacity: 0, y: 2 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.1 }}
      onClick={() => {
        if (assigning) return
        if (bulkMode && onSelect) onSelect(alert)
        else onCardClick?.(alert)
      }}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: COL.gap,
        minHeight: 52,
        background: isSelected
          ? 'rgba(0,122,255,0.06)'
          : hovered ? 'rgba(142,142,147,0.05)' : c.bg,
        border: isSelected
          ? `0.5px solid ${c.blue}`
          : `0.5px solid ${c.separator}`,
        borderLeft: `3px solid ${sevDot}`,
        borderRadius: 7,
        cursor: assigning ? 'default' : 'pointer',
        padding: '0 10px',
        overflow: 'hidden',
        marginBottom: 2,
        transition: 'background 0.12s',
      }}
    >
      {/* Bulk checkbox */}
      {bulkMode && (
        <div
          onClick={(e) => { stop(e); onSelect?.(alert) }}
          style={{
            width: 16, height: 16, borderRadius: 4, flexShrink: 0,
            border: isSelected ? 'none' : `1.5px solid ${c.separator}`,
            background: isSelected ? c.blue : 'transparent',
            display: 'flex', alignItems: 'center', justifyContent: 'center',
            cursor: 'pointer',
          }}
        >
          {isSelected && <Check style={{ width: 10, height: 10, color: '#fff' }} />}
        </div>
      )}

      {/* Severity pill */}
      <span style={{
        flexShrink: 0, width: COL.sev, display: 'flex', alignItems: 'center', gap: 5,
        fontSize: 11, fontWeight: 600, padding: '3px 7px', borderRadius: 5,
        background: sevCfg.bg, color: sevCfg.color, justifyContent: 'center',
      }}>
        <span style={{
          width: 6, height: 6, borderRadius: '50%',
          background: sevDot, flexShrink: 0,
        }} />
        {sevCfg.label}
      </span>

      {/* Status pill */}
      <span style={{
        flexShrink: 0, width: COL.status,
        fontSize: 11, fontWeight: 500, padding: '3px 8px', borderRadius: 5,
        background: statCfg.bg, color: statCfg.color,
        textAlign: 'center', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
      }}>
        {statCfg.label}
      </span>

      {/* Title + entity sub-row */}
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{
          fontSize: 13, fontWeight: 600, color: c.label,
          overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
          marginBottom: 2, lineHeight: 1.3,
        }}>
          {pNumber && (
            <span style={{ color: c.blue, fontWeight: 700, marginRight: 6, fontSize: 11 }}>
              P-{pNumber}
            </span>
          )}
          {alert.title}
        </div>
        <div style={{
          display: 'flex', alignItems: 'center', gap: 5,
          overflow: 'hidden', flexWrap: 'nowrap',
        }}>
          {entityType && (
            <MetaChip
              label={entityType}
              color={c.teal}
              bg="rgba(48,176,199,0.10)"
              icon={EntityIcon}
            />
          )}
          {(hostname || hostIP) && (
            <span style={{
              fontSize: 10, color: c.tertiaryLabel, fontFamily: 'ui-monospace, monospace', flexShrink: 0,
            }}>
              {hostname ?? hostIP}{hostname && hostIP ? ` · ${hostIP}` : ''}
            </span>
          )}
          {workload && <MetaChip label={workload} color={c.green} bg="rgba(52,199,89,0.09)" />}
          {namespace && <MetaChip label={namespace} color={c.blue} bg="rgba(0,122,255,0.09)" />}
          {cluster && !namespace && <MetaChip label={cluster} color={c.indigo} bg="rgba(88,86,214,0.09)" />}
          {rootCause && (
            <span style={{ fontSize: 10, color: c.orange, flexShrink: 0 }}>
              RC: {rootCause.length > 20 ? rootCause.slice(0, 20) + '…' : rootCause}
            </span>
          )}
        </div>
      </div>

      {/* Source + Zone */}
      <div style={{
        flexShrink: 0, width: COL.zone,
        display: 'flex', flexDirection: 'column', gap: 3, alignItems: 'flex-end',
      }}>
        <span style={{
          fontSize: 10, fontWeight: 600, color: c.secondaryLabel,
          background: c.fill, padding: '2px 6px', borderRadius: 4,
          letterSpacing: '0.2px', whiteSpace: 'nowrap',
        }}>
          {sourceLabel}
        </span>
        {mgmtZone && (
          <MetaChip
            label={mgmtZone.length > 16 ? mgmtZone.slice(0, 16) + '…' : mgmtZone}
            color={c.purple} bg="rgba(175,82,222,0.09)"
            icon={Shield}
          />
        )}
        {dtImpact && !mgmtZone && (
          <MetaChip
            label={dtImpact}
            color={dtImpact === 'APPLICATION' ? c.red : c.orange}
            bg={dtImpact === 'APPLICATION' ? 'rgba(255,59,48,0.09)' : 'rgba(255,149,0,0.09)'}
          />
        )}
      </div>

      {/* Flags / Impact */}
      <div style={{
        flexShrink: 0, width: COL.flags,
        display: 'flex', flexDirection: 'column', gap: 2, alignItems: 'flex-end',
      }}>
        {isCorrelated && (
          <span title={corrScore ? `Correlation score: ${(corrScore * 100).toFixed(0)}%` : 'Correlated'} style={{
            display: 'flex', alignItems: 'center', gap: 2,
            fontSize: 10, fontWeight: 600, color: c.purple,
            background: 'rgba(175,82,222,0.10)', padding: '1px 5px',
            borderRadius: 4, whiteSpace: 'nowrap',
          }}>
            <Link2 style={{ width: 9, height: 9 }} />
            {corrScore ? `${(corrScore * 100).toFixed(0)}%` : 'COR'}
          </span>
        )}
        {(alert as any).dominant_strategy && (alert as any).dominant_strategy !== 'root_cause_engine' && (
          <span title={`Strategy: ${(alert as any).dominant_strategy}`} style={{
            fontSize: 9, fontWeight: 600, padding: '1px 4px', borderRadius: 3,
            color: (alert as any).dominant_strategy === 'topology' ? '#007AFF' : (alert as any).dominant_strategy === 'semantic' ? '#34C759' : '#FF9500',
            background: (alert as any).dominant_strategy === 'topology' ? 'rgba(0,122,255,0.08)' : (alert as any).dominant_strategy === 'semantic' ? 'rgba(52,199,89,0.08)' : 'rgba(255,149,0,0.08)',
          }}>
            {(alert as any).dominant_strategy === 'topology' ? 'TOPO' : (alert as any).dominant_strategy === 'semantic' ? 'SEM' : 'TEMP'}
          </span>
        )}
        {isLinked && !isCorrelated && (
          <span title="Linked to incident" style={{
            display: 'flex', alignItems: 'center', gap: 2,
            fontSize: 10, fontWeight: 600, color: c.indigo,
            background: 'rgba(88,86,214,0.10)', padding: '1px 5px',
            borderRadius: 4, whiteSpace: 'nowrap',
          }}>
            <GitMerge style={{ width: 9, height: 9 }} />
            INC
          </span>
        )}
        {occurrences > 1 && (
          <span title={`${occurrences} occurrences`} style={{
            fontSize: 10, fontWeight: 700, color: c.orange,
            background: 'rgba(255,149,0,0.10)', padding: '1px 5px',
            borderRadius: 4, whiteSpace: 'nowrap',
          }}>
            ×{occurrences}
          </span>
        )}
        {impactedCnt !== undefined && impactedCnt > 0 && (
          <span title={`${impactedCnt} impacted entities`} style={{
            fontSize: 10, color: c.orange, fontWeight: 600,
            background: 'rgba(255,149,0,0.09)', padding: '1px 5px',
            borderRadius: 4, whiteSpace: 'nowrap',
          }}>
            {impactedCnt} imp.
          </span>
        )}
        {slaBreached && (
          <span title="SLA breached" style={{
            fontSize: 10, fontWeight: 700, color: c.red,
            background: 'rgba(255,59,48,0.10)', padding: '1px 5px',
            borderRadius: 4, whiteSpace: 'nowrap',
          }}>
            SLA
          </span>
        )}
      </div>

      {/* Age */}
      <div
        style={{ flexShrink: 0, width: COL.age, textAlign: 'right' }}
        title={absTime(alert.created_at)}
      >
        <div style={{ fontSize: 11, fontWeight: 500, color: c.tertiaryLabel, whiteSpace: 'nowrap' }}>
          {timeAgo(alert.created_at)}
        </div>
      </div>

      {/* Assignee avatar */}
      <div style={{ flexShrink: 0, width: COL.assignee, display: 'flex', justifyContent: 'center' }}>
        {assigneeInitials ? (
          <div
            title={assigneeName}
            style={{
              width: 22, height: 22, borderRadius: '50%',
              background: 'rgba(0,122,255,0.18)', color: c.blue,
              fontSize: 8, fontWeight: 700,
              display: 'flex', alignItems: 'center', justifyContent: 'center',
              letterSpacing: '0.5px',
            }}
          >
            {assigneeInitials}
          </div>
        ) : null}
      </div>

      {/* Actions */}
      {!bulkMode && (
        assigning ? (
          <AssignInput
            onSubmit={(uid) => { onAssign?.(alert, uid); setAssigning(false) }}
            onCancel={() => setAssigning(false)}
          />
        ) : (
          <div
            style={{
              display: 'flex', gap: 3, flexShrink: 0, width: COL.actions, justifyContent: 'flex-end',
              opacity: hovered ? 1 : 0.4, transition: 'opacity 0.15s',
            }}
            onClick={stop}
          >
            {dtUrl && (
              <ActionBtn
                icon={ExternalLink}
                label="Dynatrace"
                color={c.blue}
                bg="rgba(0,122,255,0.15)"
                onClick={(e) => { stop(e); onDynatraceLink?.(alert) }}
              />
            )}
            {alert.status === 'open' && onAcknowledge && (
              <ActionBtn
                icon={Check}
                label="Ack"
                color={c.green}
                bg="rgba(52,199,89,0.15)"
                onClick={(e) => { stop(e); onAcknowledge(alert) }}
              />
            )}
            {!(['resolved', 'closed'] as string[]).includes(alert.status) && onResolve && (
              <ActionBtn
                icon={CheckCheck}
                label="Resolve"
                color={c.blue}
                bg="rgba(0,122,255,0.15)"
                onClick={(e) => { stop(e); onResolve(alert) }}
              />
            )}
            {onAssign && (
              <ActionBtn
                icon={UserPlus}
                label="Assign"
                color={c.gray}
                bg={c.fill}
                onClick={(e) => { stop(e); setAssigning(true) }}
              />
            )}
          </div>
        )
      )}
    </motion.div>
  )
}
