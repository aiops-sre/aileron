import type { Alert } from '@/types'

export interface ClusterHealth {
  name: string
  score: number
  alertCount: number
  criticalCount: number
  avgResolutionTime: number
}

export interface AlertVelocity {
  current: number
  previous: number
  change: number
  trend: 'up' | 'down' | 'stable'
  hourly: number[]
}

export interface MTTRStats {
  avg: number
  p95: number
  target: number
  withinSLA: number
  total: number
}

export interface SLOCompliance {
  percentage: number
  met: number
  breached: number
  total: number
}

/**
 * Calculate cluster health scores
 */
export function calculateClusterHealth(alerts: Alert[]): {
  clusters: ClusterHealth[]
  avgScore: number
  healthy: number
  total: number
} {
  const clusterMap = new Map<string, {
    alerts: Alert[]
    critical: number
    resolved: number
  }>()

  // Group alerts by cluster
  alerts.forEach((alert) => {
    const cluster = alert.metadata?.cluster || alert.labels?.cluster || 'unknown'
    if (!clusterMap.has(cluster)) {
      clusterMap.set(cluster, { alerts: [], critical: 0, resolved: 0 })
    }
    const data = clusterMap.get(cluster)!
    data.alerts.push(alert)
    if (alert.severity === 'critical') data.critical++
    if (alert.status === 'resolved') data.resolved++
  })

  // Calculate health scores
  const clusters = Array.from(clusterMap.entries())
    .filter(([name]) => name !== 'unknown')
    .map(([name, data]) => {
      const total = data.alerts.length
      const criticalRatio = total > 0 ? data.critical / total : 0
      const resolvedRatio = total > 0 ? data.resolved / total : 1

      // Calculate average resolution time
      const resolvedAlerts = data.alerts.filter(
        (a) => a.status === 'resolved' && a.created_at && a.resolved_at
      )
      let avgResolution = 0
      if (resolvedAlerts.length > 0) {
        const totalTime = resolvedAlerts.reduce((sum, a) => {
          const created = new Date(a.created_at).getTime()
          const resolved = new Date(a.resolved_at!).getTime()
          return sum + (resolved - created) / 60000 // minutes
        }, 0)
        avgResolution = Math.round(totalTime / resolvedAlerts.length)
      }

      // Health score calculation
      let score = 100
      score -= criticalRatio * 40 // Penalize critical alerts
      score -= (1 - resolvedRatio) * 30 // Penalize unresolved
      score -= Math.min(total / 10, 20) // Penalize high volume
      score = Math.max(0, Math.round(score))

      return {
        name,
        score,
        alertCount: total,
        criticalCount: data.critical,
        avgResolutionTime: avgResolution,
      }
    })
    .sort((a, b) => b.score - a.score)

  const avgScore = clusters.length > 0
    ? Math.round(clusters.reduce((sum, c) => sum + c.score, 0) / clusters.length)
    : 100
  const healthy = clusters.filter((c) => c.score >= 90).length

  return {
    clusters: clusters.slice(0, 6),
    avgScore,
    healthy,
    total: clusters.length,
  }
}

/**
 * Calculate alert velocity and trends
 */
export function calculateAlertVelocity(alerts: Alert[]): AlertVelocity {
  const now = Date.now()
  const oneHourAgo = now - 3600000
  const twoHoursAgo = now - 7200000
  const oneDayAgo = now - 86400000

  const lastHour = alerts.filter((a) => new Date(a.created_at).getTime() > oneHourAgo).length
  const previousHour = alerts.filter((a) => {
    const time = new Date(a.created_at).getTime()
    return time > twoHoursAgo && time <= oneHourAgo
  }).length

  // Calculate 24-hour distribution
  const hourly = new Array(24).fill(0)
  alerts.forEach((alert) => {
    const created = new Date(alert.created_at).getTime()
    if (created > oneDayAgo) {
      const hoursAgo = Math.floor((now - created) / 3600000)
      if (hoursAgo >= 0 && hoursAgo < 24) {
        hourly[23 - hoursAgo]++
      }
    }
  })

  const change = previousHour > 0 ? Math.round(((lastHour - previousHour) / previousHour) * 100) : 0
  const trend = change > 0 ? 'up' : change < 0 ? 'down' : 'stable'

  return {
    current: lastHour,
    previous: previousHour,
    change: Math.abs(change),
    trend,
    hourly,
  }
}

/**
 * Calculate MTTR (Mean Time To Resolution) statistics
 */
export function calculateMTTR(alerts: Alert[]): MTTRStats {
  const resolvedAlerts = alerts.filter(
    (a) => a.status === 'resolved' && a.created_at && a.resolved_at
  )

  if (resolvedAlerts.length === 0) {
    return { avg: 0, p95: 0, target: 30, withinSLA: 0, total: 0 }
  }

  const resolutionTimes = resolvedAlerts
    .map((a) => {
      const created = new Date(a.created_at).getTime()
      const resolved = new Date(a.resolved_at!).getTime()
      return (resolved - created) / 60000 // minutes
    })
    .sort((a, b) => a - b)

  const avg = Math.round(resolutionTimes.reduce((sum, t) => sum + t, 0) / resolutionTimes.length)
  const p95Index = Math.floor(resolutionTimes.length * 0.95)
  const p95 = Math.round(resolutionTimes[p95Index] || 0)
  const target = 30 // 30 minutes target
  const withinSLA = resolutionTimes.filter((t) => t <= target).length

  return {
    avg,
    p95,
    target,
    withinSLA,
    total: resolutionTimes.length,
  }
}

/**
 * Calculate SLO compliance
 */
export function calculateSLOCompliance(alerts: Alert[]): SLOCompliance {
  const tracked = alerts.filter((a) => a.created_at)
  let met = 0
  let breached = 0

  tracked.forEach((alert) => {
    const created = new Date(alert.created_at).getTime()

    // Check acknowledgment SLA (5 minutes)
    if (alert.acknowledged_at) {
      const ackTime = new Date(alert.acknowledged_at).getTime()
      const ackMinutes = (ackTime - created) / 60000
      if (ackMinutes <= 5) met++
      else breached++
    } else if (alert.status === 'open') {
      const now = Date.now()
      const minutes = (now - created) / 60000
      if (minutes > 5) breached++
    }

    // Check resolution SLA for critical (30 minutes)
    if (alert.severity === 'critical' && alert.resolved_at) {
      const resolvedTime = new Date(alert.resolved_at).getTime()
      const resolutionMinutes = (resolvedTime - created) / 60000
      if (resolutionMinutes <= 30) met++
      else breached++
    }
  })

  const total = met + breached
  const percentage = total > 0 ? Math.round((met / total) * 100) : 100

  return {
    percentage,
    met,
    breached,
    total,
  }
}

/**
 * Find repeat offenders (recurring alert patterns)
 */
export function findRepeatOffenders(alerts: Alert[]): Array<{
  pattern: string
  occurrences: number
  frequency: number
  timeframe: string
}> {
  const patternMap = new Map<string, Date[]>()

  alerts.forEach((alert) => {
    const normalized = alert.title
      .toLowerCase()
      .replace(/\d+\.\d+\.\d+\.\d+/g, 'IP')
      .replace(/\d{4}-\d{2}-\d{2}/g, 'DATE')
      .replace(/\d{2}:\d{2}:\d{2}/g, 'TIME')
      .replace(/\d+/g, 'N')
      .replace(/\s+/g, ' ')
      .trim()

    if (!patternMap.has(normalized)) {
      patternMap.set(normalized, [])
    }
    patternMap.get(normalized)!.push(new Date(alert.created_at))
  })

  return Array.from(patternMap.entries())
    .filter(([, dates]) => dates.length >= 3)
    .map(([pattern, dates]) => {
      dates.sort((a, b) => a.getTime() - b.getTime())
      const first = dates[0].getTime()
      const last = dates[dates.length - 1].getTime()
      const diffHours = Math.round((last - first) / 3600000)

      return {
        pattern: pattern.substring(0, 50),
        occurrences: dates.length,
        frequency: dates.length,
        timeframe: diffHours < 24 ? `${diffHours}h` : `${Math.round(diffHours / 24)}d`,
      }
    })
    .sort((a, b) => b.frequency - a.frequency)
    .slice(0, 5)
}

/**
 * Analyze time patterns
 */
export function analyzeTimePatterns(alerts: Alert[]): {
  peakHours: Array<{ label: string; hour: number; count: number }>
  hourCounts: number[]
  insight: string
} {
  const hourCounts = new Array(24).fill(0)

  alerts.forEach((alert) => {
    const hour = new Date(alert.created_at).getHours()
    hourCounts[hour]++
  })

  const hoursWithCounts = hourCounts.map((count, hour) => ({ hour, count }))
  const sorted = [...hoursWithCounts].sort((a, b) => b.count - a.count)

  const peakHours = [
    { label: 'Highest Peak', ...sorted[0] },
    { label: 'Second Peak', ...sorted[1] },
    { label: 'Third Peak', ...sorted[2] },
  ]

  const maxHour = sorted[0].hour
  let insight = ''
  if (maxHour >= 9 && maxHour <= 17) {
    insight = 'Most alerts occur during business hours (9am-5pm), suggesting user activity correlation.'
  } else if (maxHour >= 0 && maxHour <= 5) {
    insight = 'Peak alerts during night hours may indicate batch job issues or automated processes.'
  } else {
    insight = 'Alert distribution shows activity peaks during deployment windows or high-traffic periods.'
  }

  return { peakHours, hourCounts, insight }
}