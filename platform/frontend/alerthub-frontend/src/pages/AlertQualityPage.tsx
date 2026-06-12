import React, { useState, useEffect } from 'react'
import { motion } from 'framer-motion'
import { TrendingUp, TrendingDown, AlertTriangle, Target, Award } from 'lucide-react'

const apple = {
  blue: '#007AFF',
  green: '#34C759',
  red: '#FF3B30',
  orange: '#FF9500',
  label: 'var(--color-text)',
  secondaryLabel: 'var(--color-text-secondary)',
  tertiaryLabel: 'var(--color-text-tertiary, #8E8E93)',
  separator: 'var(--color-separator, rgba(142, 142, 147, 0.12))',
  fill: 'var(--color-fill, rgba(142, 142, 147, 0.08))',
  secondaryBackground: 'var(--color-card, rgba(255, 255, 255, 0.8))',
  radius: { sm: 6, md: 10, lg: 12 },
}

interface AlertQuality {
  alert_name: string
  total_count: number
  acknowledged_count: number
  resolved_count: number
  false_positive_count: number
  avg_resolution_time: number
  quality_score: number
  trend: 'improving' | 'stable' | 'degrading'
  recommendations: string[]
}

export function AlertQualityPage() {
  const [qualityData, setQualityData] = useState<AlertQuality[]>([])
  const [loading, setLoading] = useState(true)
  const [sortBy, setSortBy] = useState<'quality' | 'count' | 'resolution_time'>('quality')

  useEffect(() => {
    loadAlertQuality()
  }, [])

  const loadAlertQuality = async () => {
    setLoading(true)
    try {
      const response = await fetch('/api/v1/analytics/alert-quality', {
        headers: {
          'Authorization': `Bearer ${sessionStorage.getItem('access_token') || localStorage.getItem('access_token')}`,
        },
      })
      const data = await response.json()
      setQualityData(data.data?.alerts || [])
    } catch (error) {
      console.error('Failed to load alert quality:', error)
    } finally {
      setLoading(false)
    }
  }

  const getQualityColor = (score: number) => {
    if (score >= 80) return apple.green
    if (score >= 60) return apple.orange
    return apple.red
  }

  const getQualityLabel = (score: number) => {
    if (score >= 80) return 'Excellent'
    if (score >= 60) return 'Good'
    if (score >= 40) return 'Fair'
    return 'Poor'
  }

  const sortedData = [...qualityData].sort((a, b) => {
    switch (sortBy) {
      case 'quality':
        return a.quality_score - b.quality_score
      case 'count':
        return b.total_count - a.total_count
      case 'resolution_time':
        return b.avg_resolution_time - a.avg_resolution_time
      default:
        return 0
    }
  })

  const avgQuality = qualityData.reduce((sum, d) => sum + d.quality_score, 0) / Math.max(1, qualityData.length)

  return (
    <div style={{ padding: 24, maxWidth: 1400, margin: '0 auto' }}>
      {/* Header */}
      <div style={{ marginBottom: 24 }}>
        <h1 style={{ fontSize: 28, fontWeight: 600, color: apple.label, marginBottom: 8 }}>
          Alert Quality
        </h1>
        <p style={{ fontSize: 15, color: apple.secondaryLabel }}>
          Analyze and improve alert signal-to-noise ratio
        </p>
      </div>

      {/* Summary Cards */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 16, marginBottom: 24 }}>
        <div style={{
          padding: 20,
          background: apple.secondaryBackground,
          borderRadius: apple.radius.lg,
          border: `0.5px solid ${apple.separator}`,
        }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
            <Award style={{ width: 20, height: 20, color: getQualityColor(avgQuality) }} />
            <span style={{ fontSize: 13, color: apple.tertiaryLabel }}>Overall Quality</span>
          </div>
          <div style={{ fontSize: 32, fontWeight: 700, color: getQualityColor(avgQuality) }}>
            {Math.round(avgQuality)}%
          </div>
          <div style={{ fontSize: 12, color: apple.tertiaryLabel, marginTop: 4 }}>
            {getQualityLabel(avgQuality)}
          </div>
        </div>

        <div style={{
          padding: 20,
          background: apple.secondaryBackground,
          borderRadius: apple.radius.lg,
          border: `0.5px solid ${apple.separator}`,
        }}>
          <div style={{ fontSize: 13, color: apple.tertiaryLabel, marginBottom: 8 }}>
            Noisy Alerts
          </div>
          <div style={{ fontSize: 32, fontWeight: 700, color: apple.orange }}>
            {qualityData.filter(d => d.quality_score < 60).length}
          </div>
          <div style={{ fontSize: 12, color: apple.tertiaryLabel, marginTop: 4 }}>
            Need attention
          </div>
        </div>

        <div style={{
          padding: 20,
          background: apple.secondaryBackground,
          borderRadius: apple.radius.lg,
          border: `0.5px solid ${apple.separator}`,
        }}>
          <div style={{ fontSize: 13, color: apple.tertiaryLabel, marginBottom: 8 }}>
            High Quality
          </div>
          <div style={{ fontSize: 32, fontWeight: 700, color: apple.green }}>
            {qualityData.filter(d => d.quality_score >= 80).length}
          </div>
          <div style={{ fontSize: 12, color: apple.tertiaryLabel, marginTop: 4 }}>
            Well configured
          </div>
        </div>

        <div style={{
          padding: 20,
          background: apple.secondaryBackground,
          borderRadius: apple.radius.lg,
          border: `0.5px solid ${apple.separator}`,
        }}>
          <div style={{ fontSize: 13, color: apple.tertiaryLabel, marginBottom: 8 }}>
            Improving
          </div>
          <div style={{ fontSize: 32, fontWeight: 700, color: apple.blue }}>
            {qualityData.filter(d => d.trend === 'improving').length}
          </div>
          <div style={{ fontSize: 12, color: apple.tertiaryLabel, marginTop: 4 }}>
            Positive trend
          </div>
        </div>
      </div>

      {/* Sort Controls */}
      <div style={{ marginBottom: 16 }}>
        <label style={{ fontSize: 13, fontWeight: 500, color: apple.secondaryLabel, marginRight: 12 }}>
          Sort by:
        </label>
        {(['quality', 'count', 'resolution_time'] as const).map((sort) => (
          <button
            key={sort}
            onClick={() => setSortBy(sort)}
            style={{
              padding: '6px 12px',
              marginRight: 8,
              borderRadius: apple.radius.sm,
              border: 'none',
              background: sortBy === sort ? apple.blue : apple.fill,
              color: sortBy === sort ? '#fff' : apple.label,
              fontSize: 13,
              fontWeight: 500,
              cursor: 'pointer',
            }}
          >
            {sort === 'quality' && 'Quality Score'}
            {sort === 'count' && 'Alert Count'}
            {sort === 'resolution_time' && 'Resolution Time'}
          </button>
        ))}
      </div>

      {/* Quality Table */}
      <div style={{
        background: apple.secondaryBackground,
        borderRadius: apple.radius.lg,
        border: `0.5px solid ${apple.separator}`,
        overflow: 'hidden',
      }}>
        {loading ? (
          <div style={{ padding: 40, textAlign: 'center', color: apple.tertiaryLabel }}>
            Analyzing alert quality...
          </div>
        ) : sortedData.length === 0 ? (
          <div style={{ padding: 60, textAlign: 'center' }}>
            <Target style={{ width: 48, height: 48, color: apple.tertiaryLabel, margin: '0 auto 16px' }} />
            <h3 style={{ fontSize: 18, fontWeight: 600, color: apple.label, marginBottom: 8 }}>
              No alert quality data
            </h3>
            <p style={{ fontSize: 14, color: apple.secondaryLabel }}>
              Quality metrics will appear as alerts are processed
            </p>
          </div>
        ) : (
          <div style={{ padding: 20 }}>
            {sortedData.map((alert) => (
              <motion.div
                key={alert.alert_name}
                initial={{ opacity: 0 }}
                animate={{ opacity: 1 }}
                style={{
                  padding: 16,
                  background: apple.fill,
                  borderRadius: apple.radius.md,
                  border: `0.5px solid ${apple.separator}`,
                  marginBottom: 12,
                }}
              >
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
                  <div style={{ flex: 1 }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 8 }}>
                      <h4 style={{ fontSize: 15, fontWeight: 600, color: apple.label, margin: 0 }}>
                        {alert.alert_name}
                      </h4>
                      {alert.trend === 'improving' && (
                        <TrendingUp style={{ width: 16, height: 16, color: apple.green }} />
                      )}
                      {alert.trend === 'degrading' && (
                        <TrendingDown style={{ width: 16, height: 16, color: apple.red }} />
                      )}
                    </div>

                    <div style={{ display: 'flex', gap: 16, marginBottom: 12 }}>
                      <div style={{ fontSize: 12, color: apple.tertiaryLabel }}>
                        Total: <strong style={{ color: apple.label }}>{alert.total_count}</strong>
                      </div>
                      <div style={{ fontSize: 12, color: apple.tertiaryLabel }}>
                        Ack: <strong style={{ color: apple.label }}>{alert.acknowledged_count}</strong>
                      </div>
                      <div style={{ fontSize: 12, color: apple.tertiaryLabel }}>
                        Resolved: <strong style={{ color: apple.label }}>{alert.resolved_count}</strong>
                      </div>
                      {alert.false_positive_count > 0 && (
                        <div style={{ fontSize: 12, color: apple.tertiaryLabel }}>
                          False Positives: <strong style={{ color: apple.red }}>{alert.false_positive_count}</strong>
                        </div>
                      )}
                      <div style={{ fontSize: 12, color: apple.tertiaryLabel }}>
                        Avg Resolution: <strong style={{ color: apple.label }}>{alert.avg_resolution_time}min</strong>
                      </div>
                    </div>

                    {/* Recommendations */}
                    {alert.recommendations && alert.recommendations.length > 0 && (
                      <div style={{
                        padding: 12,
                        background: `${apple.blue}08`,
                        borderRadius: apple.radius.sm,
                        marginTop: 8,
                      }}>
                        <div style={{ fontSize: 12, fontWeight: 600, color: apple.label, marginBottom: 6 }}>
                          💡 Recommendations:
                        </div>
                        <ul style={{ margin: 0, paddingLeft: 20 }}>
                          {alert.recommendations.map((rec, idx) => (
                            <li key={idx} style={{ fontSize: 12, color: apple.secondaryLabel, marginBottom: 4 }}>
                              {rec}
                            </li>
                          ))}
                        </ul>
                      </div>
                    )}
                  </div>

                  {/* Quality Score Badge */}
                  <div style={{
                    display: 'flex',
                    flexDirection: 'column',
                    alignItems: 'center',
                    minWidth: 80,
                  }}>
                    <div style={{
                      width: 60,
                      height: 60,
                      borderRadius: '50%',
                      background: `${getQualityColor(alert.quality_score)}15`,
                      border: `2px solid ${getQualityColor(alert.quality_score)}`,
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      marginBottom: 6,
                    }}>
                      <span style={{
                        fontSize: 20,
                        fontWeight: 700,
                        color: getQualityColor(alert.quality_score),
                      }}>
                        {Math.round(alert.quality_score)}
                      </span>
                    </div>
                    <span style={{ fontSize: 11, color: apple.tertiaryLabel, fontWeight: 500 }}>
                      {getQualityLabel(alert.quality_score)}
                    </span>
                  </div>
                </div>
              </motion.div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
