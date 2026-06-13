import React, { useState } from 'react'
import { motion } from 'framer-motion'
import { useQuery } from '@tanstack/react-query'
import {
  UserCheck,
  Phone,
  Mail,
  RefreshCw,
  Calendar,
  Users,
  History,
  CheckCircle,
  Clock,
  User,
  CalendarDays,
  Loader2,
  AlertCircle,
  Filter,
  TrendingUp,
  Activity,
} from 'lucide-react'
import { pagerDutyService } from '@/services/PagerDutyService'
import type { PDCurrentOnCall, PDShift, PDSchedule } from '@/types/pagerduty'

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Aileron Design Tokens
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

const tokens = {
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
// Components
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

function PersonAvatar({ name, avatarUrl, size = 48 }: { name: string; avatarUrl?: string; size?: number }) {
  const getInitials = (name: string) => {
    const parts = name.split(' ')
    if (parts.length >= 2) {
      return (parts[0][0] + parts[1][0]).toUpperCase()
    }
    return name.substring(0, 2).toUpperCase()
  }

  const hue = name.split('').reduce((a, c) => a + c.charCodeAt(0), 0) % 360

  if (avatarUrl) {
    return (
      <div style={{
        width: size,
        height: size,
        borderRadius: size * 0.42,
        overflow: 'hidden',
        flexShrink: 0,
        boxShadow: '0 2px 8px rgba(0,0,0,0.1)',
      }}>
        <img src={avatarUrl} alt={name} style={{ width: '100%', height: '100%', objectFit: 'cover' }} />
      </div>
    )
  }

  return (
    <div style={{
      width: size,
      height: size,
      borderRadius: size * 0.42,
      background: `hsl(${hue}, 55%, 58%)`,
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
      flexShrink: 0,
      boxShadow: '0 2px 8px rgba(0,0,0,0.1)',
    }}>
      <span style={{
        color: '#fff',
        fontSize: size * 0.4,
        fontWeight: 600,
        lineHeight: 1,
      }}>
        {getInitials(name)}
      </span>
    </div>
  )
}

function CurrentOnCallCard({ oncall }: { oncall: PDCurrentOnCall }) {
  const formatTimeRange = (start: string, end: string) => {
    const startDate = new Date(start)
    const endDate = new Date(end)
    const now = new Date()

    const formatTime = (date: Date) => {
      return date.toLocaleString('en-US', {
        month: 'short',
        day: 'numeric',
        hour: '2-digit',
        minute: '2-digit',
        hour12: true
      })
    }

    if (startDate < now && endDate > now) {
      return `Until ${formatTime(endDate)}`
    }

    return `${formatTime(startDate)} - ${formatTime(endDate)}`
  }

  const getTimeRemaining = (endTime: string) => {
    const now = new Date()
    const end = new Date(endTime)
    const diff = end.getTime() - now.getTime()
    
    const hours = Math.floor(diff / 3600000)
    const minutes = Math.floor((diff % 3600000) / 60000)
    
    if (hours > 24) {
      return `${Math.floor(hours / 24)}d ${hours % 24}h remaining`
    } else if (hours > 0) {
      return `${hours}h ${minutes}m remaining`
    } else if (minutes > 0) {
      return `${minutes}m remaining`
    }
    return 'Ending soon'
  }

  return (
    <motion.div
      initial={{ opacity: 0, scale: 0.98 }}
      animate={{ opacity: 1, scale: 1 }}
      style={{
        background: `linear-gradient(135deg, ${tokens.green}15, ${tokens.green}08)`,
        border: `0.5px solid ${tokens.green}40`,
        borderRadius: tokens.radius.lg,
        padding: 20,
        position: 'relative',
        overflow: 'hidden',
      }}
    >
      {/* Live indicator */}
      <div style={{
        position: 'absolute',
        top: 12,
        right: 12,
        width: 12,
        height: 12,
        borderRadius: '50%',
        background: tokens.green,
        boxShadow: `0 0 12px ${tokens.green}60`,
        animation: 'pulse 2s infinite',
      }} />

      <div style={{ display: 'flex', alignItems: 'center', gap: 16, marginBottom: 16 }}>
        <PersonAvatar name={oncall.user.name} avatarUrl={oncall.user.avatar_url} size={64} />
        <div style={{ flex: 1 }}>
          <h3 style={{ fontSize: 18, fontWeight: 600, color: tokens.label, margin: '0 0 4px' }}>
            {oncall.user.name}
          </h3>
          <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 2 }}>
            <Mail style={{ width: 12, height: 12, color: tokens.secondaryLabel }} />
            <span style={{ fontSize: 13, color: tokens.secondaryLabel }}>
              {oncall.user.email}
            </span>
          </div>
        </div>
      </div>

      {/* Details */}
      <div style={{
        background: tokens.secondaryBackground,
        borderRadius: tokens.radius.sm,
        padding: 12,
        marginBottom: 12,
      }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 6 }}>
          <span style={{ fontSize: 12, fontWeight: 500, color: tokens.secondaryLabel }}>Schedule</span>
          <span style={{ fontSize: 13, fontWeight: 600, color: tokens.label }}>{oncall.schedule_name}</span>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
          <span style={{ fontSize: 12, fontWeight: 500, color: tokens.secondaryLabel }}>Shift</span>
          <span style={{ fontSize: 13, color: tokens.label }}>{formatTimeRange(oncall.start, oncall.end)}</span>
        </div>
      </div>

      {/* Time remaining */}
      <div style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        gap: 6,
        padding: '8px 12px',
        background: `${tokens.green}20`,
        borderRadius: tokens.radius.sm,
        fontSize: 12,
        fontWeight: 600,
        color: tokens.green,
      }}>
        <Clock style={{ width: 12, height: 12 }} />
        {getTimeRemaining(oncall.end)}
      </div>
    </motion.div>
  )
}

function UpcomingShiftCard({ shift, scheduleName }: { shift: PDShift; scheduleName: string }) {
  const formatTimeRange = (start: string, end: string) => {
    const startDate = new Date(start)
    const endDate = new Date(end)

    const formatTime = (date: Date) => {
      return date.toLocaleString('en-US', {
        month: 'short',
        day: 'numeric',
        hour: '2-digit',
        minute: '2-digit',
        hour12: true
      })
    }

    return `${formatTime(startDate)} - ${formatTime(endDate)}`
  }

  return (
    <motion.div
      initial={{ opacity: 0, x: -8 }}
      animate={{ opacity: 1, x: 0 }}
      style={{
        background: tokens.secondaryBackground,
        border: `0.5px solid ${tokens.separator}`,
        borderRadius: tokens.radius.lg,
        padding: 16,
        display: 'flex',
        alignItems: 'center',
        gap: 12,
        transition: 'all 0.15s ease',
        cursor: 'pointer',
      }}
      onMouseEnter={(e) => {
        e.currentTarget.style.background = tokens.tertiaryFill
        e.currentTarget.style.borderColor = tokens.blue
      }}
      onMouseLeave={(e) => {
        e.currentTarget.style.background = tokens.secondaryBackground
        e.currentTarget.style.borderColor = tokens.separator
      }}
    >
      <PersonAvatar name={shift.user.name} avatarUrl={shift.user.avatar_url} size={40} />
      <div style={{ flex: 1 }}>
        <h4 style={{ fontSize: 15, fontWeight: 600, color: tokens.label, margin: '0 0 4px' }}>
          {shift.user.name}
        </h4>
        <div style={{ fontSize: 12, color: tokens.secondaryLabel }}>
          {scheduleName} • {formatTimeRange(shift.start, shift.end)}
        </div>
      </div>
      <div style={{
        fontSize: 11,
        fontWeight: 600,
        padding: '3px 8px',
        borderRadius: 5,
        textTransform: 'uppercase',
        background: `${tokens.blue}20`,
        color: tokens.blue,
        letterSpacing: '0.3px',
      }}>
        Upcoming
      </div>
    </motion.div>
  )
}

export const OnCallSchedule: React.FC = () => {
  const [selectedSchedules, setSelectedSchedules] = useState<string[]>([])
  const [upcomingDays, setUpcomingDays] = useState(7)

  // Fetch schedules
  const { data: schedulesData } = useQuery({
    queryKey: ['pagerduty-schedules'],
    queryFn: () => pagerDutyService.getSchedules(),
    retry: 2,
    staleTime: 300000, // 5 minutes
  })

  // Fetch current on-call
  const { 
    data: currentData, 
    isLoading: isLoadingCurrent, 
    error: errorCurrent, 
    refetch: refetchCurrent 
  } = useQuery({
    queryKey: ['pagerduty-current', selectedSchedules],
    queryFn: () => pagerDutyService.getCurrentOnCall(selectedSchedules.length > 0 ? selectedSchedules : undefined),
    retry: 2,
    refetchInterval: 60000, // Auto-refresh every minute
    staleTime: 30000,
  })

  // Fetch upcoming on-call
  const { 
    data: upcomingData, 
    isLoading: isLoadingUpcoming,
    refetch: refetchUpcoming 
  } = useQuery({
    queryKey: ['pagerduty-upcoming', selectedSchedules, upcomingDays],
    queryFn: () => pagerDutyService.getUpcomingOnCall(upcomingDays, selectedSchedules.length > 0 ? selectedSchedules : undefined),
    retry: 2,
    refetchInterval: 300000, // Auto-refresh every 5 minutes
    staleTime: 120000,
  })

  // Fetch incident stats for metrics
  const { data: statsData } = useQuery({
    queryKey: ['pagerduty-incident-stats'],
    queryFn: () => pagerDutyService.getIncidentStats(),
    retry: 2,
    refetchInterval: 300000,
    staleTime: 120000,
  })

  const handleRefresh = () => {
    refetchCurrent()
    refetchUpcoming()
  }

  const isLoading = isLoadingCurrent || isLoadingUpcoming

  if (isLoading && !currentData) {
    return (
      <div style={{
        minHeight: '100vh',
        background: tokens.background,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
      }}>
        <div style={{ textAlign: 'center' }}>
          <Loader2 style={{ 
            width: 32, 
            height: 32, 
            color: tokens.blue, 
            animation: 'spin 1s linear infinite', 
            margin: '0 auto 16px' 
          }} />
          <p style={{ fontSize: 15, color: tokens.secondaryLabel }}>
            Loading on-call schedule from PagerDuty...
          </p>
        </div>
      </div>
    )
  }

  if (errorCurrent && !currentData) {
    return (
      <div style={{
        minHeight: '100vh',
        background: tokens.background,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
      }}>
        <div style={{ textAlign: 'center', maxWidth: 400, padding: '0 20px' }}>
          <div style={{
            width: 64,
            height: 64,
            borderRadius: tokens.radius.xl,
            background: tokens.red,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            margin: '0 auto 20px',
          }}>
            <AlertCircle style={{ width: 28, height: 28, color: '#fff' }} />
          </div>
          <h2 style={{ fontSize: 20, fontWeight: 600, color: tokens.label, marginBottom: 8 }}>
            Failed to Load Schedule
          </h2>
          <p style={{ fontSize: 15, color: tokens.secondaryLabel, marginBottom: 20 }}>
            {errorCurrent instanceof Error ? errorCurrent.message : 'Unable to connect to PagerDuty service'}
          </p>
          <button
            onClick={handleRefresh}
            style={{
              padding: '10px 20px',
              borderRadius: tokens.radius.sm,
              border: 'none',
              background: tokens.blue,
              color: '#fff',
              fontSize: 14,
              fontWeight: 500,
              cursor: 'pointer',
              display: 'flex',
              alignItems: 'center',
              gap: 6,
              margin: '0 auto',
            }}
          >
            <RefreshCw style={{ width: 16, height: 16 }} />
            Retry
          </button>
        </div>
      </div>
    )
  }

  const schedules = schedulesData?.data || []
  const currentOnCall = currentData?.data || []
  const upcomingShifts = upcomingData?.data || []
  const stats = statsData?.data

  return (
    <div style={{
      minHeight: '100vh',
      background: tokens.background,
    }}>
      <div style={{
        maxWidth: 1200,
        margin: '0 auto',
        padding: '24px 16px',
      }}>
        {/* Header */}
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 32, flexWrap: 'wrap', gap: 16 }}>
          <div>
            <h1 style={{ fontSize: 28, fontWeight: 700, color: tokens.label, margin: 0 }}>
              On-Call Schedule
            </h1>
            <p style={{ fontSize: 15, color: tokens.secondaryLabel, marginTop: 4 }}>
              PagerDuty rotation and upcoming shifts • {currentData?.cached && 'Cached data'}
            </p>
          </div>
          <button
            onClick={handleRefresh}
            disabled={isLoading}
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 6,
              padding: '8px 12px',
              borderRadius: tokens.radius.sm,
              border: 'none',
              background: tokens.blue,
              color: '#fff',
              fontSize: 13,
              fontWeight: 500,
              cursor: isLoading ? 'not-allowed' : 'pointer',
              opacity: isLoading ? 0.6 : 1,
            }}
          >
            <RefreshCw style={{ width: 14, height: 14, ...(isLoading && { animation: 'spin 1s linear infinite' }) }} />
            Refresh
          </button>
        </div>

        {/* Filters */}
        {schedules.length > 0 && (
          <div style={{ marginBottom: 24 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12 }}>
              <Filter style={{ width: 16, height: 16, color: tokens.secondaryLabel }} />
              <span style={{ fontSize: 14, fontWeight: 500, color: tokens.label }}>Filter by Schedule</span>
            </div>
            <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
              {schedules.map(schedule => (
                <button
                  key={schedule.id}
                  onClick={() => {
                    setSelectedSchedules(prev =>
                      prev.includes(schedule.id)
                        ? prev.filter(id => id !== schedule.id)
                        : [...prev, schedule.id]
                    )
                  }}
                  style={{
                    padding: '6px 12px',
                    borderRadius: tokens.radius.sm,
                    border: `0.5px solid ${selectedSchedules.includes(schedule.id) ? tokens.blue : tokens.separator}`,
                    background: selectedSchedules.includes(schedule.id) ? `${tokens.blue}20` : tokens.fill,
                    color: selectedSchedules.includes(schedule.id) ? tokens.blue : tokens.label,
                    fontSize: 13,
                    fontWeight: 500,
                    cursor: 'pointer',
                    transition: 'all 0.15s ease',
                  }}
                >
                  {schedule.name}
                </button>
              ))}
            </div>
          </div>
        )}

        {/* Statistics */}
        {stats && (
          <div style={{
            display: 'grid',
            gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))',
            gap: 12,
            marginBottom: 24,
          }}>
            <div style={{
              background: tokens.secondaryBackground,
              border: `0.5px solid ${tokens.separator}`,
              borderRadius: tokens.radius.md,
              padding: '16px 20px',
            }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
                <Activity style={{ width: 16, height: 16, color: tokens.blue }} />
                <div style={{ fontSize: 11, color: tokens.secondaryLabel, textTransform: 'uppercase', letterSpacing: '0.5px' }}>
                  Total Incidents
                </div>
              </div>
              <div style={{ fontSize: 28, fontWeight: 700, color: tokens.label }}>
                {stats.total_incidents}
              </div>
            </div>

            <div style={{
              background: tokens.secondaryBackground,
              border: `0.5px solid ${tokens.separator}`,
              borderRadius: tokens.radius.md,
              padding: '16px 20px',
            }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
                <TrendingUp style={{ width: 16, height: 16, color: tokens.green }} />
                <div style={{ fontSize: 11, color: tokens.secondaryLabel, textTransform: 'uppercase', letterSpacing: '0.5px' }}>
                  Avg Resolution
                </div>
              </div>
              <div style={{ fontSize: 28, fontWeight: 700, color: tokens.label }}>
                {Math.round(stats.avg_resolution_time_seconds / 60)}m
              </div>
            </div>

            <div style={{
              background: tokens.secondaryBackground,
              border: `0.5px solid ${tokens.separator}`,
              borderRadius: tokens.radius.md,
              padding: '16px 20px',
            }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
                <UserCheck style={{ width: 16, height: 16, color: tokens.purple }} />
                <div style={{ fontSize: 11, color: tokens.secondaryLabel, textTransform: 'uppercase', letterSpacing: '0.5px' }}>
                  On-Call Now
                </div>
              </div>
              <div style={{ fontSize: 28, fontWeight: 700, color: tokens.label }}>
                {currentOnCall.length}
              </div>
            </div>
          </div>
        )}

        {/* Current On-Call Section */}
        <div style={{ marginBottom: 32 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 16 }}>
            <UserCheck style={{ width: 20, height: 20, color: tokens.green }} />
            <h2 style={{ fontSize: 20, fontWeight: 600, color: tokens.label, margin: 0 }}>
              Currently On-Call
            </h2>
            <div style={{
              display: 'flex',
              alignItems: 'center',
              gap: 4,
              padding: '2px 8px',
              borderRadius: 8,
              background: `${tokens.green}15`,
            }}>
              <div style={{
                width: 6,
                height: 6,
                borderRadius: '50%',
                background: tokens.green,
                animation: 'pulse 2s infinite',
              }} />
              <span style={{
                fontSize: 10,
                fontWeight: 600,
                color: tokens.green,
                textTransform: 'uppercase',
                letterSpacing: '0.5px',
              }}>
                Live
              </span>
            </div>
          </div>

          {currentOnCall.length > 0 ? (
            <div style={{ 
              display: 'grid', 
              gridTemplateColumns: 'repeat(auto-fit, minmax(320px, 1fr))', 
              gap: 16 
            }}>
              {currentOnCall.map((oncall) => (
                <CurrentOnCallCard key={oncall.schedule_id} oncall={oncall} />
              ))}
            </div>
          ) : (
            <div style={{
              textAlign: 'center',
              padding: '60px 20px',
              background: tokens.secondaryBackground,
              border: `0.5px solid ${tokens.separator}`,
              borderRadius: tokens.radius.lg,
            }}>
              <UserCheck style={{ width: 48, height: 48, color: tokens.quaternaryLabel, margin: '0 auto 16px' }} />
              <h3 style={{ fontSize: 17, fontWeight: 500, color: tokens.label, margin: '0 0 8px' }}>
                No one is currently on-call
              </h3>
              <p style={{ fontSize: 13, color: tokens.tertiaryLabel }}>
                Check the schedule configuration in PagerDuty.
              </p>
            </div>
          )}
        </div>

        {/* Upcoming Schedule */}
        {upcomingShifts.length > 0 && (
          <div style={{ marginBottom: 32 }}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 16 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <Calendar style={{ width: 20, height: 20, color: tokens.blue }} />
                <h2 style={{ fontSize: 20, fontWeight: 600, color: tokens.label, margin: 0 }}>
                  Upcoming Shifts
                </h2>
              </div>
              <select
                value={upcomingDays}
                onChange={(e) => setUpcomingDays(Number(e.target.value))}
                style={{
                  padding: '6px 12px',
                  borderRadius: tokens.radius.sm,
                  border: `0.5px solid ${tokens.separator}`,
                  background: tokens.fill,
                  fontSize: 13,
                  color: tokens.label,
                  cursor: 'pointer',
                  outline: 'none',
                }}
              >
                <option value={7}>Next 7 days</option>
                <option value={14}>Next 14 days</option>
                <option value={30}>Next 30 days</option>
              </select>
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {upcomingShifts.map((schedule) => 
                (schedule.shifts || []).map((shift, idx) => (
                  <UpcomingShiftCard 
                    key={`${schedule.schedule_id}-${idx}`} 
                    shift={shift} 
                    scheduleName={schedule.schedule_name}
                  />
                ))
              )}
            </div>
          </div>
        )}

        {/* Schedule Information */}
        {schedules.length > 0 && (
          <div style={{
            background: tokens.secondaryBackground,
            border: `0.5px solid ${tokens.separator}`,
            borderRadius: tokens.radius.lg,
            padding: 20,
            marginBottom: 24,
          }}>
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(200px, 1fr))', gap: 16 }}>
              <div>
                <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4 }}>
                  <CalendarDays style={{ width: 14, height: 14, color: tokens.secondaryLabel }} />
                  <span style={{ fontSize: 12, fontWeight: 500, color: tokens.secondaryLabel }}>
                    Active Schedules
                  </span>
                </div>
                <div style={{ fontSize: 16, fontWeight: 600, color: tokens.label }}>
                  {schedules.length}
                </div>
              </div>
              <div>
                <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4 }}>
                  <Users style={{ width: 14, height: 14, color: tokens.secondaryLabel }} />
                  <span style={{ fontSize: 12, fontWeight: 500, color: tokens.secondaryLabel }}>
                    Team Size
                  </span>
                </div>
                <div style={{ fontSize: 16, fontWeight: 600, color: tokens.label }}>
                  {currentOnCall.length + upcomingShifts.reduce((sum, s) => sum + (s.shifts?.length ?? 0), 0)} engineers
                </div>
              </div>
              <div>
                <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4 }}>
                  <Clock style={{ width: 14, height: 14, color: tokens.secondaryLabel }} />
                  <span style={{ fontSize: 12, fontWeight: 500, color: tokens.secondaryLabel }}>
                    Time Zone
                  </span>
                </div>
                <div style={{ fontSize: 16, fontWeight: 600, color: tokens.label }}>
                  {schedules[0]?.time_zone || 'N/A'}
                </div>
              </div>
            </div>
          </div>
        )}
      </div>

      {/* Global keyframes */}
      <style>{`
        @keyframes pulse {
          0%, 100% {
            opacity: 1;
            transform: scale(1);
          }
          50% {
            opacity: 0.7;
            transform: scale(1.05);
          }
        }
        @keyframes spin { 
          from { transform: rotate(0deg) } 
          to { transform: rotate(360deg) } 
        }
      `}</style>
    </div>
  )
}
