import React, { useState, useEffect, useMemo } from 'react'
import { motion, AnimatePresence } from 'framer-motion'
import {
  Brain,
  Zap,
  Eye,
  Sparkles,
  TrendingUp,
  Activity,
  Target,
  CheckCircle,
  AlertCircle,
  Clock,
  Users,
  Database,
  Network,
  Server,
  Cpu,
  BarChart3,
  ArrowRight,
  Play,
  Pause,
  RotateCcw,
  Settings,
  Filter,
  Search,
  MessageCircle,
  ThumbsUp,
  ThumbsDown,
  Star,
  Send
} from 'lucide-react'
import { useAppleDesign, glassmorphismCSS } from '@/lib/apple-design-system'
import { useWebSocket } from '@/hooks/useWebSocket'
import type { Alert, EnhancedIncident, AIAnalysis, RealTimeUpdate } from '@/types'

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Autonomous AIOps Types
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

interface AIInvestigation {
  id: string
  alertId: string
  timestamp: string
  status: 'analyzing' | 'completed' | 'failed' | 'streaming'
  confidence: number
  analysis: {
    rootCause?: string
    correlation?: string
    businessImpact?: string
    recommendations?: string[]
    similarIncidents?: Array<{
      id: string
      title: string
      similarity: number
      resolution: string
    }>
  }
  learningFeedback?: {
    accuracy: number
    usefulness: number
    feedback: string
  }
  streamingSteps: Array<{
    step: string
    status: 'pending' | 'processing' | 'completed' | 'failed'
    result?: string
    timestamp: string
    confidence?: number
  }>
}

interface LearningSystemMetrics {
  totalInvestigations: number
  averageAccuracy: number
  improvementTrend: number
  confidenceThreshold: number
  automationRate: number
  humanFeedbackCount: number
  modelVersion: string
  lastTraining: string
}

interface AutonomousDecision {
  id: string
  type: 'escalate' | 'correlate' | 'suppress' | 'auto_resolve' | 'create_incident'
  alertId: string
  confidence: number
  reasoning: string
  executedAt: string
  outcome?: 'success' | 'failure' | 'pending'
  humanOverride?: boolean
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Revolutionary UI Components
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

const AIInvestigationStream = ({ investigation }: { investigation: AIInvestigation }) => {
  const apple = useAppleDesign()
  const [expandedStep, setExpandedStep] = useState<string | null>(null)

  const getStepIcon = (status: string) => {
    switch (status) {
      case 'completed': return CheckCircle
      case 'processing': return Activity
      case 'failed': return AlertCircle
      default: return Clock
    }
  }

  const getStepColor = (status: string) => {
    switch (status) {
      case 'completed': return apple.colors.green
      case 'processing': return apple.colors.blue
      case 'failed': return apple.colors.red
      default: return apple.colors.gray
    }
  }

  return (
    <div style={{
      ...glassmorphismCSS('premium'),
      borderRadius: apple.radius['2xl'],
      padding: apple.spacing.xl,
      marginBottom: apple.spacing.lg,
    }}>
      {/* Investigation Header */}
      <div style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        marginBottom: apple.spacing.lg,
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: apple.spacing.md }}>
          <div style={{
            width: 56,
            height: 56,
            borderRadius: apple.radius.xl,
            background: investigation.status === 'streaming' 
              ? apple.colors.gradients.blueToIndigo
              : investigation.status === 'completed'
              ? apple.colors.gradients.greenToTeal
              : apple.colors.gradients.orangeToRed,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
          }}>
            {investigation.status === 'streaming' ? (
              <motion.div
                animate={{ rotate: 360 }}
                transition={{ duration: 2, repeat: Infinity, ease: 'linear' }}
              >
                <Brain style={{ width: 28, height: 28, color: '#fff' }} />
              </motion.div>
            ) : (
              <Brain style={{ width: 28, height: 28, color: '#fff' }} />
            )}
          </div>
          
          <div>
            <h3 style={{
              fontSize: apple.typography.sizes.title3,
              fontWeight: apple.typography.weights.bold,
              color: apple.colors.label,
              margin: 0,
            }}>
              AI Investigation #{investigation.id.slice(-6)}
            </h3>
            <p style={{
              fontSize: apple.typography.sizes.subhead,
              color: apple.colors.secondaryLabel,
              margin: 0,
            }}>
              {investigation.status === 'streaming' ? 'Analyzing in real-time...' :
               investigation.status === 'completed' ? 'Analysis completed' :
               investigation.status === 'failed' ? 'Analysis failed' : 'Queued for analysis'}
            </p>
          </div>
        </div>

        <div style={{ display: 'flex', alignItems: 'center', gap: apple.spacing.sm }}>
          <div style={{
            padding: '8px 16px',
            borderRadius: apple.radius.lg,
            background: `${getStepColor(investigation.status)}20`,
            border: `1px solid ${getStepColor(investigation.status)}40`,
          }}>
            <span style={{
              fontSize: apple.typography.sizes.footnote,
              fontWeight: apple.typography.weights.bold,
              color: getStepColor(investigation.status),
            }}>
              {Math.round(investigation.confidence)}% confidence
            </span>
          </div>
        </div>
      </div>

      {/* Streaming Steps */}
      <div style={{
        display: 'flex',
        flexDirection: 'column',
        gap: apple.spacing.sm,
      }}>
        {investigation.streamingSteps.map((step, index) => {
          const StepIcon = getStepIcon(step.status)
          const isExpanded = expandedStep === step.step

          return (
            <motion.div
              key={step.step}
              initial={{ opacity: 0, x: -20 }}
              animate={{ opacity: 1, x: 0 }}
              transition={{ delay: index * 0.1 }}
            >
              <div
                onClick={() => setExpandedStep(isExpanded ? null : step.step)}
                style={{
                  padding: apple.spacing.md,
                  borderRadius: apple.radius.lg,
                  background: apple.colors.fill,
                  border: `1px solid ${apple.colors.separator}`,
                  cursor: 'pointer',
                  transition: 'all 200ms ease',
                }}
                onMouseEnter={(e) => {
                  e.currentTarget.style.background = apple.colors.secondaryFill
                  e.currentTarget.style.borderColor = getStepColor(step.status)
                }}
                onMouseLeave={(e) => {
                  e.currentTarget.style.background = apple.colors.fill
                  e.currentTarget.style.borderColor = apple.colors.separator
                }}
              >
                <div style={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'space-between',
                }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: apple.spacing.sm }}>
                    <div style={{
                      width: 32,
                      height: 32,
                      borderRadius: apple.radius.md,
                      background: getStepColor(step.status),
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                    }}>
                      {step.status === 'processing' ? (
                        <motion.div
                          animate={{ rotate: 360 }}
                          transition={{ duration: 1, repeat: Infinity, ease: 'linear' }}
                        >
                          <StepIcon style={{ width: 16, height: 16, color: '#fff' }} />
                        </motion.div>
                      ) : (
                        <StepIcon style={{ width: 16, height: 16, color: '#fff' }} />
                      )}
                    </div>
                    
                    <div>
                      <div style={{
                        fontSize: apple.typography.sizes.callout,
                        fontWeight: apple.typography.weights.semibold,
                        color: apple.colors.label,
                      }}>
                        {step.step}
                      </div>
                      <div style={{
                        fontSize: apple.typography.sizes.caption1,
                        color: apple.colors.tertiaryLabel,
                      }}>
                        {new Date(step.timestamp).toLocaleTimeString()}
                      </div>
                    </div>
                  </div>

                  {step.confidence && (
                    <div style={{
                      fontSize: apple.typography.sizes.footnote,
                      fontWeight: apple.typography.weights.semibold,
                      color: getStepColor(step.status),
                    }}>
                      {Math.round(step.confidence)}%
                    </div>
                  )}
                </div>

                <AnimatePresence>
                  {isExpanded && step.result && (
                    <motion.div
                      initial={{ opacity: 0, height: 0 }}
                      animate={{ opacity: 1, height: 'auto' }}
                      exit={{ opacity: 0, height: 0 }}
                      style={{
                        marginTop: apple.spacing.md,
                        padding: apple.spacing.md,
                        borderRadius: apple.radius.sm,
                        background: apple.colors.tertiaryFill,
                      }}
                    >
                      <div style={{
                        fontSize: apple.typography.sizes.subhead,
                        color: apple.colors.secondaryLabel,
                        lineHeight: 1.5,
                      }}>
                        {step.result}
                      </div>
                    </motion.div>
                  )}
                </AnimatePresence>
              </div>
            </motion.div>
          )
        })}
      </div>

      {/* Investigation Results */}
      {investigation.status === 'completed' && investigation.analysis && (
        <div style={{ marginTop: apple.spacing.lg }}>
          <div style={{
            padding: apple.spacing.lg,
            borderRadius: apple.radius.lg,
            background: 'rgba(52, 199, 89, 0.1)',
            border: '1px solid rgba(52, 199, 89, 0.2)',
          }}>
            <h4 style={{
              fontSize: apple.typography.sizes.headline,
              fontWeight: apple.typography.weights.bold,
              color: apple.colors.green,
              margin: '0 0 12px',
            }}>
              Analysis Complete
            </h4>
            
            {investigation.analysis.rootCause && (
              <div style={{ marginBottom: apple.spacing.md }}>
                <div style={{
                  fontSize: apple.typography.sizes.footnote,
                  fontWeight: apple.typography.weights.semibold,
                  color: apple.colors.secondaryLabel,
                  textTransform: 'uppercase',
                  letterSpacing: '0.5px',
                  marginBottom: 4,
                }}>
                  Root Cause
                </div>
                <div style={{
                  fontSize: apple.typography.sizes.subhead,
                  color: apple.colors.label,
                  lineHeight: 1.4,
                }}>
                  {investigation.analysis.rootCause}
                </div>
              </div>
            )}

            {investigation.analysis.recommendations && (
              <div style={{ marginBottom: apple.spacing.md }}>
                <div style={{
                  fontSize: apple.typography.sizes.footnote,
                  fontWeight: apple.typography.weights.semibold,
                  color: apple.colors.secondaryLabel,
                  textTransform: 'uppercase',
                  letterSpacing: '0.5px',
                  marginBottom: 8,
                }}>
                  Recommendations
                </div>
                <div style={{
                  display: 'flex',
                  flexDirection: 'column',
                  gap: 6,
                }}>
                  {investigation.analysis.recommendations.map((rec, index) => (
                    <div
                      key={index}
                      style={{
                        display: 'flex',
                        alignItems: 'flex-start',
                        gap: 8,
                        fontSize: apple.typography.sizes.subhead,
                        color: apple.colors.label,
                        lineHeight: 1.4,
                      }}
                    >
                      <Target style={{ 
                        width: 16, 
                        height: 16, 
                        color: apple.colors.blue,
                        marginTop: 2,
                        flexShrink: 0
                      }} />
                      {rec}
                    </div>
                  ))}
                </div>
              </div>
            )}

            {/* Feedback Section */}
            <div style={{
              display: 'flex',
              alignItems: 'center',
              gap: apple.spacing.md,
              marginTop: apple.spacing.lg,
              padding: apple.spacing.md,
              borderRadius: apple.radius.sm,
              background: apple.colors.fill,
            }}>
              <span style={{
                fontSize: apple.typography.sizes.footnote,
                fontWeight: apple.typography.weights.medium,
                color: apple.colors.secondaryLabel,
              }}>
                Was this analysis helpful?
              </span>
              
              <div style={{ display: 'flex', gap: apple.spacing.sm }}>
                <motion.button
                  whileHover={{ scale: 1.05 }}
                  whileTap={{ scale: 0.95 }}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 4,
                    padding: '6px 12px',
                    borderRadius: apple.radius.sm,
                    border: 'none',
                    background: apple.colors.green,
                    color: '#fff',
                    fontSize: apple.typography.sizes.caption1,
                    fontWeight: apple.typography.weights.semibold,
                    cursor: 'pointer',
                  }}
                >
                  <ThumbsUp style={{ width: 12, height: 12 }} />
                  Helpful
                </motion.button>
                
                <motion.button
                  whileHover={{ scale: 1.05 }}
                  whileTap={{ scale: 0.95 }}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 4,
                    padding: '6px 12px',
                    borderRadius: apple.radius.sm,
                    border: `1px solid ${apple.colors.separator}`,
                    background: apple.colors.fill,
                    color: apple.colors.secondaryLabel,
                    fontSize: apple.typography.sizes.caption1,
                    fontWeight: apple.typography.weights.semibold,
                    cursor: 'pointer',
                  }}
                >
                  <ThumbsDown style={{ width: 12, height: 12 }} />
                  Not helpful
                </motion.button>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

const LearningSystemDashboard = ({ metrics }: { metrics: LearningSystemMetrics }) => {
  const apple = useAppleDesign()

  const learningMetrics = [
    {
      label: 'Accuracy Trend',
      value: `${metrics.averageAccuracy.toFixed(1)}%`,
      trend: { value: metrics.improvementTrend, isUp: metrics.improvementTrend > 0 },
      icon: Target,
      color: apple.colors.blue,
    },
    {
      label: 'Automation Rate',
      value: `${metrics.automationRate.toFixed(1)}%`,
      icon: Zap,
      color: apple.colors.orange,
    },
    {
      label: 'Human Feedback',
      value: metrics.humanFeedbackCount.toLocaleString(),
      icon: Users,
      color: apple.colors.green,
    },
    {
      label: 'Model Version',
      value: metrics.modelVersion,
      icon: Brain,
      color: apple.colors.purple,
    }
  ]

  return (
    <div style={{
      ...glassmorphismCSS('premium'),
      borderRadius: apple.radius['2xl'],
      padding: apple.spacing.xl,
    }}>
      <div style={{
        display: 'flex',
        alignItems: 'center',
        gap: apple.spacing.md,
        marginBottom: apple.spacing.xl,
      }}>
        <div style={{
          width: 52,
          height: 52,
          borderRadius: apple.radius.xl,
          background: apple.colors.gradients.purpleToPink,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
        }}>
          <Sparkles style={{ width: 26, height: 26, color: '#fff' }} />
        </div>
        <div>
          <h3 style={{
            fontSize: apple.typography.sizes.title3,
            fontWeight: apple.typography.weights.bold,
            color: apple.colors.label,
            margin: 0,
          }}>
            Continuous Learning System
          </h3>
          <p style={{
            fontSize: apple.typography.sizes.subhead,
            color: apple.colors.secondaryLabel,
            margin: 0,
          }}>
            AI model improvement and performance tracking
          </p>
        </div>
      </div>

      <div style={{
        display: 'grid',
        gridTemplateColumns: 'repeat(auto-fit, minmax(200px, 1fr))',
        gap: apple.spacing.lg,
      }}>
        {learningMetrics.map((metric, index) => (
          <motion.div
            key={metric.label}
            initial={{ opacity: 0, y: 20 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ delay: index * 0.1 }}
            style={{
              padding: apple.spacing.lg,
              borderRadius: apple.radius.lg,
              background: `${metric.color}10`,
              border: `1px solid ${metric.color}30`,
              textAlign: 'center',
            }}
          >
            <div style={{
              width: 40,
              height: 40,
              borderRadius: apple.radius.md,
              background: metric.color,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              margin: '0 auto 12px',
            }}>
              <metric.icon style={{ width: 20, height: 20, color: '#fff' }} />
            </div>
            
            <div style={{
              fontSize: apple.typography.sizes.title2,
              fontWeight: apple.typography.weights.bold,
              color: metric.color,
              lineHeight: 1,
              marginBottom: 4,
            }}>
              {metric.value}
            </div>
            
            <div style={{
              fontSize: apple.typography.sizes.caption1,
              color: apple.colors.secondaryLabel,
              textTransform: 'uppercase',
              letterSpacing: '0.5px',
            }}>
              {metric.label}
            </div>
            
            {metric.trend && (
              <div style={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                gap: 4,
                marginTop: 8,
              }}>
                <TrendingUp style={{
                  width: 12,
                  height: 12,
                  color: metric.trend.isUp ? apple.colors.green : apple.colors.red
                }} />
                <span style={{
                  fontSize: apple.typography.sizes.caption2,
                  fontWeight: apple.typography.weights.semibold,
                  color: metric.trend.isUp ? apple.colors.green : apple.colors.red,
                }}>
                  {metric.trend.value > 0 ? '+' : ''}{metric.trend.value.toFixed(1)}%
                </span>
              </div>
            )}
          </motion.div>
        ))}
      </div>
    </div>
  )
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Main Command Center Component
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

export const AutonomousAIOpsCommandCenter = () => {
  const apple = useAppleDesign()
  const [activeInvestigations, setActiveInvestigations] = useState<AIInvestigation[]>([])
  const [learningMetrics, setLearningMetrics] = useState<LearningSystemMetrics | null>(null)
  const [autonomousDecisions, setAutonomousDecisions] = useState<AutonomousDecision[]>([])
  const [isAutoMode, setIsAutoMode] = useState(true)

  // WebSocket for real-time AI updates
  const { isConnected, lastMessage } = useWebSocket(`${window.location.protocol === 'https:' ? 'wss' : 'ws'}://${window.location.host}/api/v1/ws/ai-investigations`)

  // Fetch real investigations and learning metrics from the backend
  useEffect(() => {
    const token = sessionStorage.getItem('access_token') || localStorage.getItem('access_token') || ''
    const headers: Record<string, string> = { 'Content-Type': 'application/json' }
    if (token) headers['Authorization'] = `Bearer ${token}`

    fetch('/api/v1/rca/investigations', { headers })
      .then(r => r.ok ? r.json() : null)
      .then(json => {
        if (!json) return
        const data = json.data?.data ?? json.data ?? json
        const items: AIInvestigation[] = data?.investigations ?? data?.items ?? []
        setActiveInvestigations(items)
      })
      .catch(() => { /* leave state as empty array */ })

    fetch('/api/v1/ai/learning-metrics', { headers })
      .then(r => r.ok ? r.json() : null)
      .then(json => {
        if (!json) return
        const data = json.data?.data ?? json.data ?? json
        if (data && typeof data === 'object') {
          setLearningMetrics(data as LearningSystemMetrics)
        }
      })
      .catch(() => { /* leave metrics as null */ })
  }, [])

  return (
    <div style={{
      minHeight: '100vh',
      background: apple.colors.background,
      padding: apple.spacing.lg,
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
          marginBottom: apple.spacing.xl,
        }}>
          <div>
            <motion.h1
              initial={{ opacity: 0, x: -20 }}
              animate={{ opacity: 1, x: 0 }}
              style={{
                fontSize: apple.typography.sizes.largeTitle,
                fontWeight: apple.typography.weights.black,
                color: apple.colors.label,
                margin: 0,
              }}
            >
              🤖 Autonomous AIOps Command Center
            </motion.h1>
            <motion.p
              initial={{ opacity: 0, x: -20 }}
              animate={{ opacity: 1, x: 0 }}
              transition={{ delay: 0.1 }}
              style={{
                fontSize: apple.typography.sizes.callout,
                color: apple.colors.secondaryLabel,
                margin: '8px 0 0',
              }}
            >
              AI-powered investigation streaming with continuous learning and autonomous decision making
            </motion.p>
          </div>

          <div style={{ display: 'flex', alignItems: 'center', gap: apple.spacing.md }}>
            <div style={{
              display: 'flex',
              alignItems: 'center',
              gap: 6,
              padding: '8px 16px',
              borderRadius: apple.radius.lg,
              background: isAutoMode ? 'rgba(52, 199, 89, 0.1)' : 'rgba(255, 149, 0, 0.1)',
              border: `1px solid ${isAutoMode ? apple.colors.green : apple.colors.orange}40`,
            }}>
              <div style={{
                width: 8,
                height: 8,
                borderRadius: apple.radius.full,
                background: isAutoMode ? apple.colors.green : apple.colors.orange,
              }} />
              <span style={{
                fontSize: apple.typography.sizes.footnote,
                fontWeight: apple.typography.weights.semibold,
                color: isAutoMode ? apple.colors.green : apple.colors.orange,
              }}>
                {isAutoMode ? 'AUTONOMOUS MODE' : 'MANUAL MODE'}
              </span>
            </div>

            <motion.button
              whileHover={{ scale: 1.05 }}
              whileTap={{ scale: 0.95 }}
              onClick={() => setIsAutoMode(!isAutoMode)}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 6,
                padding: '8px 16px',
                borderRadius: apple.radius.lg,
                border: `1px solid ${apple.colors.separator}`,
                background: apple.colors.fill,
                color: apple.colors.label,
                fontSize: apple.typography.sizes.footnote,
                fontWeight: apple.typography.weights.semibold,
                cursor: 'pointer',
              }}
            >
              {isAutoMode ? <Pause /> : <Play />}
              {isAutoMode ? 'Pause Auto' : 'Enable Auto'}
            </motion.button>
          </div>
        </div>

        {/* Learning System Dashboard */}
        {learningMetrics && (
          <div style={{ marginBottom: apple.spacing.xl }}>
            <LearningSystemDashboard metrics={learningMetrics} />
          </div>
        )}

        {/* Active Investigations */}
        <div>
          <div style={{
            display: 'flex',
            alignItems: 'center',
            gap: apple.spacing.md,
            marginBottom: apple.spacing.lg,
          }}>
            <h2 style={{
              fontSize: apple.typography.sizes.title2,
              fontWeight: apple.typography.weights.bold,
              color: apple.colors.label,
              margin: 0,
            }}>
              Live AI Investigations
            </h2>
            <div style={{
              padding: '4px 12px',
              borderRadius: apple.radius.sm,
              background: apple.colors.blue,
              color: '#fff',
              fontSize: apple.typography.sizes.caption1,
              fontWeight: apple.typography.weights.bold,
            }}>
              {activeInvestigations.length} Active
            </div>
          </div>

          {activeInvestigations.length === 0 ? (
            <div style={{
              ...glassmorphismCSS('card'),
              borderRadius: apple.radius['2xl'],
              padding: apple.spacing.xl,
              textAlign: 'center',
            }}>
              <Eye style={{ 
                width: 48, 
                height: 48, 
                color: apple.colors.quaternaryLabel,
                margin: '0 auto 16px'
              }} />
              <p style={{
                fontSize: apple.typography.sizes.callout,
                color: apple.colors.secondaryLabel,
                margin: 0,
              }}>
                No active investigations. AI is monitoring for new alerts...
              </p>
            </div>
          ) : (
            <div style={{
              display: 'flex',
              flexDirection: 'column',
              gap: apple.spacing.lg,
            }}>
              {activeInvestigations.map(investigation => (
                <AIInvestigationStream
                  key={investigation.id}
                  investigation={investigation}
                />
              ))}
            </div>
          )}
        </div>
      </motion.div>
    </div>
  )
}