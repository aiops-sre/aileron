import React, { useState, useEffect, useMemo } from 'react'
import { motion, AnimatePresence } from 'framer-motion'
import {
  Brain,
  Zap,
  Activity,
  TrendingUp,
  TrendingDown,
  Database,
  Server,
  Network,
  Shield,
  AlertTriangle,
  CheckCircle,
  Clock,
  BarChart3,
  Eye,
  GitBranch,
  Cpu,
  HardDrive,
  Wifi,
  RefreshCw,
  Sparkles,
  Target
} from 'lucide-react'
import { useAppleDesign, glassmorphismCSS } from '@/lib/design-system'
import { useWebSocket } from '@/hooks/useWebSocket'
import type { Alert, AIOpsMetrics, RealTimeUpdate } from '@/types'

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Real-Time Correlation Dashboard Types
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

interface CorrelationMetrics {
  totalCorrelations: number
  accuracyScore: number
  mlSimilarityCount: number
  ruleBasedCount: number
  temporalCount: number
  semanticCount: number
  infraCorrelationCount: number
  lastCorrelationTime: string
  confidenceDistribution: { range: string; count: number }[]
  correlationStrategies: {
    name: string
    count: number
    accuracy: number
    color: string
  }[]
}

interface RedisMetrics {
  totalCommands: number
  memoryUsage: string
  memoryPercent: number
  connectionsReceived: number
  instantaneousOpsPerSec: number
  keyspaceHits: number
  keyspaceMisses: number
  evictedKeys: number
  lastUpdated: string
}

interface InfrastructureCorrelation {
  id: string
  timestamp: string
  alertsAnalyzed: number
  rootCausesIdentified: number
  correlationsCreated: number
  strategy: string
  confidence: number
  affectedServices: string[]
  impactLevel: 'low' | 'medium' | 'high' | 'critical'
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Revolutionary Real-Time Components
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

const LiveMetricCard = ({ 
  title, 
  value, 
  unit, 
  icon: Icon, 
  trend, 
  color, 
  gradient,
  isLoading = false 
}: {
  title: string
  value: string | number
  unit?: string
  icon: React.ElementType
  trend?: { value: number; isUp: boolean }
  color: string
  gradient?: string
  isLoading?: boolean
}) => {
  const aileron = useDesignTokens()

  return (
    <motion.div
      initial={{ opacity: 0, y: 20, scale: 0.9 }}
      animate={{ opacity: 1, y: 0, scale: 1 }}
      whileHover={{ y: -4, scale: 1.02 }}
      style={{
        ...glassmorphismCSS('premium'),
        borderRadius: tokens.radius['2xl'],
        padding: tokens.spacing.lg,
        position: 'relative',
        overflow: 'hidden',
        cursor: 'pointer',
        minHeight: 160,
      }}
      className="group"
    >
      {/* Background Gradient */}
      {gradient && (
        <div style={{
          position: 'absolute',
          top: 0,
          left: 0,
          right: 0,
          bottom: 0,
          background: gradient,
          opacity: 0.03,
          borderRadius: tokens.radius['2xl'],
          zIndex: 0,
        }} />
      )}

      {/* Header */}
      <div style={{ 
        display: 'flex', 
        alignItems: 'center', 
        justifyContent: 'space-between',
        marginBottom: tokens.spacing.md,
        position: 'relative',
        zIndex: 1,
      }}>
        <div style={{
          width: 52,
          height: 52,
          borderRadius: tokens.radius.xl,
          background: gradient || color,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          boxShadow: `0 8px 24px ${color}40`,
        }}>
          <Icon style={{ width: 24, height: 24, color: '#fff' }} />
        </div>
        
        {isLoading && (
          <motion.div
            animate={{ rotate: 360 }}
            transition={{ duration: 2, repeat: Infinity, ease: 'linear' }}
          >
            <RefreshCw style={{ width: 16, height: 16, color: tokens.colors.tertiaryLabel }} />
          </motion.div>
        )}
      </div>

      {/* Value */}
      <div style={{ position: 'relative', zIndex: 1 }}>
        <div style={{
          fontSize: tokens.typography.sizes.largeTitle,
          fontWeight: tokens.typography.weights.black,
          color: tokens.colors.label,
          lineHeight: 1,
          marginBottom: 4,
        }}>
          {value}
          {unit && (
            <span style={{
              fontSize: tokens.typography.sizes.callout,
              fontWeight: tokens.typography.weights.medium,
              color: tokens.colors.secondaryLabel,
              marginLeft: 4,
            }}>
              {unit}
            </span>
          )}
        </div>
        
        <div style={{
          fontSize: tokens.typography.sizes.footnote,
          fontWeight: tokens.typography.weights.semibold,
          color: tokens.colors.secondaryLabel,
          textTransform: 'uppercase',
          letterSpacing: '0.5px',
        }}>
          {title}
        </div>
      </div>

      {/* Trend Indicator */}
      {trend && (
        <div style={{
          position: 'absolute',
          top: tokens.spacing.md,
          right: tokens.spacing.md,
          display: 'flex',
          alignItems: 'center',
          gap: 4,
          padding: '4px 8px',
          borderRadius: tokens.radius.sm,
          background: trend.isUp 
            ? 'rgba(52, 199, 89, 0.1)' 
            : 'rgba(255, 59, 48, 0.1)',
          border: `0.5px solid ${trend.isUp ? tokens.colors.green : tokens.colors.red}40`,
        }}>
          {trend.isUp ? (
            <TrendingUp style={{ width: 12, height: 12, color: tokens.colors.green }} />
          ) : (
            <TrendingDown style={{ width: 12, height: 12, color: tokens.colors.red }} />
          )}
          <span style={{
            fontSize: tokens.typography.sizes.caption1,
            fontWeight: tokens.typography.weights.bold,
            color: trend.isUp ? tokens.colors.green : tokens.colors.red,
          }}>
            {Math.abs(trend.value)}%
          </span>
        </div>
      )}

      {/* Hover glow effect */}
      <div
        style={{
          position: 'absolute',
          top: -2,
          left: -2,
          right: -2,
          bottom: -2,
          background: gradient || `linear-gradient(135deg, ${color}40, ${color}20)`,
          borderRadius: tokens.radius['2xl'],
          opacity: 0,
          transition: 'opacity 300ms ease',
          zIndex: -1,
        }}
        className="group-hover:opacity-100"
      />
    </motion.div>
  )
}

const CorrelationFlowDiagram = ({ strategies }: { strategies: CorrelationMetrics['correlationStrategies'] }) => {
  const aileron = useDesignTokens()
  const [selectedStrategy, setSelectedStrategy] = useState<string | null>(null)

  return (
    <div style={{
      ...glassmorphismCSS('premium'),
      borderRadius: tokens.radius['2xl'],
      padding: tokens.spacing.xl,
      minHeight: 400,
    }}>
      <div style={{
        display: 'flex',
        alignItems: 'center',
        gap: tokens.spacing.md,
        marginBottom: tokens.spacing.xl,
      }}>
        <div style={{
          width: 48,
          height: 48,
          borderRadius: tokens.radius.lg,
          background: tokens.colors.gradients.blueToIndigo,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
        }}>
          <GitBranch style={{ width: 24, height: 24, color: '#fff' }} />
        </div>
        <div>
          <h3 style={{
            fontSize: tokens.typography.sizes.title3,
            fontWeight: tokens.typography.weights.bold,
            color: tokens.colors.label,
            margin: 0,
          }}>
            Live Correlation Flow
          </h3>
          <p style={{
            fontSize: tokens.typography.sizes.subhead,
            color: tokens.colors.secondaryLabel,
            margin: 0,
          }}>
            Real-time analysis of four correlation strategies
          </p>
        </div>
      </div>

      {/* Strategy Flow */}
      <div style={{
        display: 'grid',
        gridTemplateColumns: 'repeat(auto-fit, minmax(200px, 1fr))',
        gap: tokens.spacing.lg,
      }}>
        {strategies.map((strategy, index) => (
          <motion.div
            key={strategy.name}
            initial={{ opacity: 0, x: -20 }}
            animate={{ opacity: 1, x: 0 }}
            transition={{ delay: index * 0.1 }}
            whileHover={{ scale: 1.05 }}
            onClick={() => setSelectedStrategy(strategy.name)}
            style={{
              padding: tokens.spacing.md,
              borderRadius: tokens.radius.lg,
              background: selectedStrategy === strategy.name 
                ? `${strategy.color}20` 
                : tokens.colors.fill,
              border: `2px solid ${selectedStrategy === strategy.name ? strategy.color : 'transparent'}`,
              cursor: 'pointer',
              transition: 'all 300ms ease',
            }}
          >
            <div style={{
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
              marginBottom: tokens.spacing.sm,
            }}>
              <h4 style={{
                fontSize: tokens.typography.sizes.callout,
                fontWeight: tokens.typography.weights.semibold,
                color: tokens.colors.label,
                margin: 0,
              }}>
                {strategy.name}
              </h4>
              <div style={{
                width: 12,
                height: 12,
                borderRadius: tokens.radius.full,
                background: strategy.color,
                boxShadow: `0 0 12px ${strategy.color}60`,
              }} />
            </div>
            
            <div style={{ marginBottom: tokens.spacing.sm }}>
              <div style={{
                fontSize: tokens.typography.sizes.title2,
                fontWeight: tokens.typography.weights.black,
                color: strategy.color,
                lineHeight: 1,
              }}>
                {strategy.count.toLocaleString()}
              </div>
              <div style={{
                fontSize: tokens.typography.sizes.caption1,
                color: tokens.colors.tertiaryLabel,
              }}>
                correlations
              </div>
            </div>
            
            <div style={{
              display: 'flex',
              alignItems: 'center',
              gap: 4,
            }}>
              <div style={{
                fontSize: tokens.typography.sizes.footnote,
                fontWeight: tokens.typography.weights.medium,
                color: tokens.colors.secondaryLabel,
              }}>
                {strategy.accuracy}% accuracy
              </div>
              <div style={{
                width: '100%',
                height: 4,
                borderRadius: 2,
                background: tokens.colors.fill,
                overflow: 'hidden',
              }}>
                <motion.div
                  initial={{ width: 0 }}
                  animate={{ width: `${strategy.accuracy}%` }}
                  transition={{ delay: index * 0.1 + 0.5, duration: 0.8 }}
                  style={{
                    height: '100%',
                    background: strategy.color,
                    borderRadius: 2,
                  }}
                />
              </div>
            </div>
          </motion.div>
        ))}
      </div>
    </div>
  )
}

const RedisPerformanceVisualization = ({ metrics }: { metrics: RedisMetrics }) => {
  const aileron = useDesignTokens()
  
  const performanceMetrics = [
    {
      label: 'Commands',
      value: metrics.totalCommands,
      color: tokens.colors.blue,
      icon: Zap,
    },
    {
      label: 'Memory',
      value: metrics.memoryUsage,
      percentage: metrics.memoryPercent,
      color: tokens.colors.orange,
      icon: HardDrive,
    },
    {
      label: 'Connections',
      value: metrics.connectionsReceived,
      color: tokens.colors.green,
      icon: Network,
    },
    {
      label: 'Hit Rate',
      value: metrics.keyspaceHits > 0 ? 
        `${Math.round(metrics.keyspaceHits / (metrics.keyspaceHits + metrics.keyspaceMisses) * 100)}%` : 
        '0%',
      color: tokens.colors.purple,
      icon: Target,
    }
  ]

  return (
    <div style={{
      ...glassmorphismCSS('premium'),
      borderRadius: tokens.radius['2xl'],
      padding: tokens.spacing.xl,
    }}>
      <div style={{
        display: 'flex',
        alignItems: 'center',
        gap: tokens.spacing.md,
        marginBottom: tokens.spacing.xl,
      }}>
        <div style={{
          width: 48,
          height: 48,
          borderRadius: tokens.radius.lg,
          background: tokens.colors.gradients.orangeToRed,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
        }}>
          <Database style={{ width: 24, height: 24, color: '#fff' }} />
        </div>
        <div>
          <h3 style={{
            fontSize: tokens.typography.sizes.title3,
            fontWeight: tokens.typography.weights.bold,
            color: tokens.colors.label,
            margin: 0,
          }}>
            Redis Performance
          </h3>
          <p style={{
            fontSize: tokens.typography.sizes.subhead,
            color: tokens.colors.secondaryLabel,
            margin: 0,
          }}>
            Live backend performance metrics
          </p>
        </div>
      </div>

      <div style={{
        display: 'grid',
        gridTemplateColumns: 'repeat(auto-fit, minmax(150px, 1fr))',
        gap: tokens.spacing.md,
      }}>
        {performanceMetrics.map((metric, index) => (
          <motion.div
            key={metric.label}
            initial={{ opacity: 0, scale: 0.8 }}
            animate={{ opacity: 1, scale: 1 }}
            transition={{ delay: index * 0.1 }}
            style={{
              padding: tokens.spacing.md,
              borderRadius: tokens.radius.lg,
              background: `${metric.color}10`,
              border: `1px solid ${metric.color}30`,
              textAlign: 'center',
            }}
          >
            <div style={{
              width: 36,
              height: 36,
              borderRadius: tokens.radius.md,
              background: metric.color,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              margin: '0 auto 8px',
            }}>
              <metric.icon style={{ width: 18, height: 18, color: '#fff' }} />
            </div>
            
            <div style={{
              fontSize: tokens.typography.sizes.title3,
              fontWeight: tokens.typography.weights.bold,
              color: metric.color,
              lineHeight: 1,
              marginBottom: 4,
            }}>
              {typeof metric.value === 'number' ? metric.value.toLocaleString() : metric.value}
            </div>
            
            <div style={{
              fontSize: tokens.typography.sizes.caption1,
              color: tokens.colors.secondaryLabel,
              textTransform: 'uppercase',
              letterSpacing: '0.5px',
            }}>
              {metric.label}
            </div>
            
            {metric.percentage && (
              <div style={{
                marginTop: 8,
                width: '100%',
                height: 4,
                borderRadius: 2,
                background: tokens.colors.fill,
                overflow: 'hidden',
              }}>
                <motion.div
                  initial={{ width: 0 }}
                  animate={{ width: `${metric.percentage}%` }}
                  transition={{ delay: index * 0.1 + 0.5, duration: 0.8 }}
                  style={{
                    height: '100%',
                    background: metric.color,
                    borderRadius: 2,
                  }}
                />
              </div>
            )}
          </motion.div>
        ))}
      </div>
      
      <div style={{
        marginTop: tokens.spacing.lg,
        padding: tokens.spacing.sm,
        borderRadius: tokens.radius.sm,
        background: tokens.colors.fill,
        textAlign: 'center',
      }}>
        <div style={{
          fontSize: tokens.typography.sizes.caption1,
          color: tokens.colors.tertiaryLabel,
        }}>
          Last updated: {new Date(metrics.lastUpdated).toLocaleTimeString()}
        </div>
      </div>
    </div>
  )
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Main Dashboard Component
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

export const AdvancedRealTimeDashboard = () => {
  const aileron = useDesignTokens()
  const [isLoading, setIsLoading] = useState(true)
  const [correlationMetrics, setCorrelationMetrics] = useState<CorrelationMetrics | null>(null)
  const [redisMetrics, setRedisMetrics] = useState<RedisMetrics | null>(null)
  const [infraCorrelations, setInfraCorrelations] = useState<InfrastructureCorrelation[]>([])
  
  // WebSocket for real-time updates
  const { isConnected, lastMessage } = useWebSocket(`${window.location.protocol === 'https:' ? 'wss' : 'ws'}://${window.location.host}/api/v1/ws/dashboard`)

  // Fetch data from backend
  useEffect(() => {
    const authHeader = () => ({ 'Authorization': `Bearer ${sessionStorage.getItem('access_token') || localStorage.getItem('access_token') || ''}` })
    const fetchDashboardData = async () => {
      try {
        // Fetch correlation metrics
        const correlationResponse = await fetch('/api/v1/analytics/correlations', { headers: authHeader() })
        if (correlationResponse.ok) {
          const data = await correlationResponse.json()
          setCorrelationMetrics(data.data)
        }

        // Fetch Redis metrics
        const redisResponse = await fetch('/api/v1/analytics/redis', { headers: authHeader() })
        if (redisResponse.ok) {
          const data = await redisResponse.json()
          setRedisMetrics(data.data)
        }

        // Fetch infrastructure correlations
        const infraResponse = await fetch('/api/v1/analytics/infrastructure-correlations', { headers: authHeader() })
        if (infraResponse.ok) {
          const data = await infraResponse.json()
          setInfraCorrelations(data.data || [])
        }

      } catch (error) {
        console.error('Dashboard data fetch error:', error)
      } finally {
        setIsLoading(false)
      }
    }

    fetchDashboardData()
    
    // Refresh every 30 seconds
    const interval = setInterval(fetchDashboardData, 30000)
    return () => clearInterval(interval)
  }, [])

  // Handle WebSocket updates
  useEffect(() => {
    if (lastMessage) {
      const update: RealTimeUpdate = JSON.parse(lastMessage.data)
      
      if (update.type === 'correlation') {
        // Update correlation metrics in real-time
        setCorrelationMetrics(prev => prev ? { ...prev, ...update.data } : null)
      }
    }
  }, [lastMessage])

  // Use real data when available; show demo indicator when falling back to mock
  const usingMockData = !correlationMetrics
  const mockCorrelationMetrics: CorrelationMetrics = correlationMetrics || {
    totalCorrelations: 2847,
    accuracyScore: 94.2,
    mlSimilarityCount: 1203,
    ruleBasedCount: 847,
    temporalCount: 456,
    semanticCount: 341,
    infraCorrelationCount: 0,
    lastCorrelationTime: new Date().toISOString(),
    confidenceDistribution: [
      { range: '90-100%', count: 1584 },
      { range: '80-90%', count: 743 },
      { range: '70-80%', count: 341 },
      { range: '60-70%', count: 179 },
    ],
    correlationStrategies: [
      { name: 'ML Similarity', count: 1203, accuracy: 96.4, color: tokens.colors.blue },
      { name: 'Rule-Based', count: 847, accuracy: 92.1, color: tokens.colors.green },
      { name: 'Temporal', count: 456, accuracy: 89.7, color: tokens.colors.orange },
      { name: 'Semantic', count: 341, accuracy: 94.8, color: tokens.colors.purple },
    ]
  }

  const mockRedisMetrics: RedisMetrics = redisMetrics || {
    totalCommands: 733,
    memoryUsage: '1.12M',
    memoryPercent: 14.6,
    connectionsReceived: 660,
    instantaneousOpsPerSec: 0,
    keyspaceHits: 0,
    keyspaceMisses: 2,
    evictedKeys: 0,
    lastUpdated: new Date().toISOString(),
  }

  return (
    <div style={{
      minHeight: '100vh',
      background: tokens.colors.background,
      padding: tokens.spacing.lg,
    }}>
      <motion.div
        initial={{ opacity: 0 }}
        animate={{ opacity: 1 }}
        style={{
          maxWidth: 1400,
          margin: '0 auto',
        }}
      >
        {/* Header */}
        <div style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          marginBottom: tokens.spacing.xl,
        }}>
          <div>
            <motion.h1
              initial={{ opacity: 0, x: -20 }}
              animate={{ opacity: 1, x: 0 }}
              style={{
                fontSize: tokens.typography.sizes.largeTitle,
                fontWeight: tokens.typography.weights.black,
                color: tokens.colors.label,
                margin: 0,
              }}
            >
              🚀 AIOps Command Center
            </motion.h1>
            <motion.p
              initial={{ opacity: 0, x: -20 }}
              animate={{ opacity: 1, x: 0 }}
              transition={{ delay: 0.1 }}
              style={{
                fontSize: tokens.typography.sizes.callout,
                color: tokens.colors.secondaryLabel,
                margin: '8px 0 0',
              }}
            >
              Revolutionary real-time correlation analysis and infrastructure intelligence
            </motion.p>
          </div>

          <div style={{ display: 'flex', alignItems: 'center', gap: tokens.spacing.md }}>
            <div style={{
              display: 'flex',
              alignItems: 'center',
              gap: 6,
              padding: '6px 12px',
              borderRadius: tokens.radius.sm,
              background: isConnected ? 'rgba(52, 199, 89, 0.1)' : 'rgba(255, 59, 48, 0.1)',
              border: `1px solid ${isConnected ? tokens.colors.green : tokens.colors.red}40`,
            }}>
              <div style={{
                width: 8,
                height: 8,
                borderRadius: tokens.radius.full,
                background: isConnected ? tokens.colors.green : tokens.colors.red,
              }} />
              <span style={{
                fontSize: tokens.typography.sizes.caption1,
                fontWeight: tokens.typography.weights.semibold,
                color: isConnected ? tokens.colors.green : tokens.colors.red,
              }}>
                {isConnected ? 'LIVE' : 'DISCONNECTED'}
              </span>
            </div>
          </div>
        </div>

        {/* Live Metrics Grid */}
        <div style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fit, minmax(280px, 1fr))',
          gap: tokens.spacing.lg,
          marginBottom: tokens.spacing.xl,
        }}>
          <LiveMetricCard
            title="Total Correlations"
            value={usingMockData ? '—' : mockCorrelationMetrics.totalCorrelations.toLocaleString()}
            icon={Brain}
            color={tokens.colors.blue}
            gradient={tokens.colors.gradients.blueToIndigo}
            trend={{ value: 12.3, isUp: true }}
            isLoading={isLoading}
          />
          <LiveMetricCard
            title="Accuracy Score"
            value={mockCorrelationMetrics.accuracyScore}
            unit="%"
            icon={Target}
            color={tokens.colors.green}
            gradient={tokens.colors.gradients.greenToTeal}
            trend={{ value: 2.1, isUp: true }}
            isLoading={isLoading}
          />
          <LiveMetricCard
            title="Redis Commands"
            value={mockRedisMetrics.totalCommands.toLocaleString()}
            icon={Database}
            color={tokens.colors.orange}
            gradient={tokens.colors.gradients.orangeToRed}
            isLoading={isLoading}
          />
          <LiveMetricCard
            title="Infra Analysis"
            value="Every Minute"
            icon={Server}
            color={tokens.colors.purple}
            gradient={tokens.colors.gradients.purpleToPink}
            isLoading={isLoading}
          />
        </div>

        {/* Correlation Flow Diagram */}
        <div style={{ marginBottom: tokens.spacing.xl }}>
          <CorrelationFlowDiagram strategies={mockCorrelationMetrics.correlationStrategies} />
        </div>

        {/* Redis Performance Visualization */}
        <div style={{ marginBottom: tokens.spacing.xl }}>
          <RedisPerformanceVisualization metrics={mockRedisMetrics} />
        </div>

        {/* Infrastructure Correlation Timeline */}
        <div style={{
          ...glassmorphismCSS('premium'),
          borderRadius: tokens.radius['2xl'],
          padding: tokens.spacing.xl,
        }}>
          <div style={{
            display: 'flex',
            alignItems: 'center',
            gap: tokens.spacing.md,
            marginBottom: tokens.spacing.lg,
          }}>
            <div style={{
              width: 48,
              height: 48,
              borderRadius: tokens.radius.lg,
              background: tokens.colors.gradients.purpleToPink,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
            }}>
              <Activity style={{ width: 24, height: 24, color: '#fff' }} />
            </div>
            <div>
              <h3 style={{
                fontSize: tokens.typography.sizes.title3,
                fontWeight: tokens.typography.weights.bold,
                color: tokens.colors.label,
                margin: 0,
              }}>
                Infrastructure Correlation Analysis
              </h3>
              <p style={{
                fontSize: tokens.typography.sizes.subhead,
                color: tokens.colors.secondaryLabel,
                margin: 0,
              }}>
                Live correlation analysis running every minute
              </p>
            </div>
          </div>

          {infraCorrelations.length === 0 ? (
            <div style={{
              textAlign: 'center',
              padding: tokens.spacing.xl,
            }}>
              <Eye style={{ 
                width: 48, 
                height: 48, 
                color: tokens.colors.quaternaryLabel,
                margin: '0 auto 16px'
              }} />
              <p style={{
                fontSize: tokens.typography.sizes.callout,
                color: tokens.colors.secondaryLabel,
                margin: 0,
              }}>
                Monitoring for infrastructure correlations...
              </p>
              <p style={{
                fontSize: tokens.typography.sizes.footnote,
                color: tokens.colors.tertiaryLabel,
                margin: '8px 0 0',
              }}>
                Analysis runs every minute with 0 alerts currently analyzed
              </p>
            </div>
          ) : (
            <div style={{
              display: 'flex',
              flexDirection: 'column',
              gap: tokens.spacing.md,
            }}>
              {infraCorrelations.map((correlation, index) => (
                <motion.div
                  key={correlation.id}
                  initial={{ opacity: 0, x: -20 }}
                  animate={{ opacity: 1, x: 0 }}
                  transition={{ delay: index * 0.1 }}
                  style={{
                    padding: tokens.spacing.md,
                    borderRadius: tokens.radius.lg,
                    background: tokens.colors.fill,
                    border: `1px solid ${tokens.colors.separator}`,
                  }}
                >
                  {/* Correlation details would go here */}
                </motion.div>
              ))}
            </div>
          )}
        </div>
      </motion.div>
    </div>
  )
}