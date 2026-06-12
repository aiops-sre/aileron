import React, { useState, useEffect } from 'react'
import { motion, AnimatePresence } from 'framer-motion'
import {
  TestTube,
  Play,
  CheckCircle,
  XCircle,
  AlertTriangle,
  RefreshCw,
  Eye,
  Copy,
  Download,
  Settings,
  Loader2,
  Zap,
  Database,
  Globe,
  Clock,
  Activity,
} from 'lucide-react'
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
// Types
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

interface TestResult {
  id: string
  name: string
  status: 'running' | 'passed' | 'failed' | 'pending'
  duration?: number
  error?: string
  details?: string
  timestamp: string
}

interface TestSuite {
  id: string
  name: string
  description: string
  tests: TestResult[]
  status: 'running' | 'passed' | 'failed' | 'pending'
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Components
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

function TestCard({ test, onRun, onViewDetails }: { 
  test: TestResult
  onRun: (id: string) => void
  onViewDetails: (test: TestResult) => void
}) {
  const getStatusColor = (status: string) => {
    switch (status) {
      case 'passed': return apple.green
      case 'failed': return apple.red
      case 'running': return apple.blue
      case 'pending': return apple.gray
      default: return apple.gray
    }
  }

  const getStatusIcon = (status: string) => {
    switch (status) {
      case 'passed': return CheckCircle
      case 'failed': return XCircle
      case 'running': return Loader2
      case 'pending': return Clock
      default: return Clock
    }
  }

  const StatusIcon = getStatusIcon(test.status)
  const statusColor = getStatusColor(test.status)

  return (
    <motion.div
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      style={{
        background: apple.secondaryBackground,
        border: `0.5px solid ${apple.separator}`,
        borderRadius: apple.radius.lg,
        padding: 16,
        transition: 'all 0.2s ease',
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 12 }}>
        <div style={{
          width: 36,
          height: 36,
          borderRadius: apple.radius.sm,
          background: statusColor,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
        }}>
          <StatusIcon style={{ 
            width: 18, 
            height: 18, 
            color: '#fff',
            ...(test.status === 'running' && { animation: 'spin 1s linear infinite' })
          }} />
        </div>
        <div style={{ flex: 1 }}>
          <h3 style={{ fontSize: 15, fontWeight: 600, color: apple.label, margin: 0 }}>
            {test.name}
          </h3>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 2 }}>
            <span style={{
              fontSize: 11,
              fontWeight: 600,
              padding: '2px 6px',
              borderRadius: 4,
              textTransform: 'uppercase',
              background: `${statusColor}20`,
              color: statusColor,
            }}>
              {test.status}
            </span>
            {test.duration && (
              <span style={{ fontSize: 11, color: apple.tertiaryLabel }}>
                {test.duration}ms
              </span>
            )}
          </div>
        </div>
        <div style={{ display: 'flex', gap: 4 }}>
          <button
            onClick={() => onRun(test.id)}
            disabled={test.status === 'running'}
            style={{
              width: 28,
              height: 28,
              borderRadius: apple.radius.sm,
              border: 'none',
              background: test.status === 'running' ? apple.gray : apple.blue,
              color: '#fff',
              cursor: test.status === 'running' ? 'default' : 'pointer',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              opacity: test.status === 'running' ? 0.5 : 1,
            }}
          >
            <Play style={{ width: 12, height: 12 }} />
          </button>
          <button
            onClick={() => onViewDetails(test)}
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
            }}
          >
            <Eye style={{ width: 12, height: 12 }} />
          </button>
        </div>
      </div>

      {test.error && (
        <div style={{
          padding: 10,
          background: `${apple.red}10`,
          border: `0.5px solid ${apple.red}30`,
          borderRadius: apple.radius.sm,
          marginTop: 8,
        }}>
          <p style={{ fontSize: 12, color: apple.red, margin: 0, fontFamily: 'SFMono-Regular, Consolas, monospace' }}>
            {test.error}
          </p>
        </div>
      )}
    </motion.div>
  )
}

function TestSuiteCard({ suite, onRunSuite }: { 
  suite: TestSuite
  onRunSuite: (id: string) => void
}) {
  const passedCount = suite.tests.filter(t => t.status === 'passed').length
  const failedCount = suite.tests.filter(t => t.status === 'failed').length
  const totalCount = suite.tests.length

  const getSuiteStatusColor = () => {
    if (suite.status === 'running') return apple.blue
    if (suite.status === 'passed') return apple.green
    if (suite.status === 'failed') return apple.red
    return apple.gray
  }

  return (
    <div style={{
      background: apple.secondaryBackground,
      border: `0.5px solid ${apple.separator}`,
      borderRadius: apple.radius.lg,
      overflow: 'hidden',
      marginBottom: 16,
    }}>
      {/* Suite Header */}
      <div style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        padding: '16px 20px',
        borderBottom: `0.5px solid ${apple.separator}`,
        background: apple.tertiaryFill,
      }}>
        <div style={{ flex: 1 }}>
          <h3 style={{ fontSize: 16, fontWeight: 600, color: apple.label, margin: 0 }}>
            {suite.name}
          </h3>
          <p style={{ fontSize: 13, color: apple.secondaryLabel, margin: 0 }}>
            {suite.description}
          </p>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <div style={{
            fontSize: 12,
            color: apple.secondaryLabel,
            textAlign: 'right',
          }}>
            <div>
              <span style={{ color: apple.green }}>{passedCount}</span>
              {' / '}
              <span style={{ color: totalCount > 0 ? apple.label : apple.tertiaryLabel }}>
                {totalCount}
              </span>
              {' passed'}
            </div>
            {failedCount > 0 && (
              <div style={{ color: apple.red, marginTop: 2 }}>
                {failedCount} failed
              </div>
            )}
          </div>
          <button
            onClick={() => onRunSuite(suite.id)}
            disabled={suite.status === 'running'}
            style={{
              padding: '6px 12px',
              borderRadius: apple.radius.sm,
              border: 'none',
              background: suite.status === 'running' ? apple.gray : apple.blue,
              color: '#fff',
              fontSize: 12,
              fontWeight: 500,
              cursor: suite.status === 'running' ? 'default' : 'pointer',
              display: 'flex',
              alignItems: 'center',
              gap: 4,
              opacity: suite.status === 'running' ? 0.7 : 1,
            }}
          >
            {suite.status === 'running' ? (
              <Loader2 style={{ width: 12, height: 12, animation: 'spin 1s linear infinite' }} />
            ) : (
              <Play style={{ width: 12, height: 12 }} />
            )}
            {suite.status === 'running' ? 'Running...' : 'Run Suite'}
          </button>
        </div>
      </div>

      {/* Progress Bar */}
      {totalCount > 0 && (
        <div style={{
          height: 4,
          background: apple.fill,
          overflow: 'hidden',
        }}>
          <div style={{
            height: '100%',
            width: `${(passedCount / totalCount) * 100}%`,
            background: apple.green,
            transition: 'width 0.3s ease',
          }} />
        </div>
      )}

      {/* Test Results Summary */}
      <div style={{ padding: '12px 20px' }}>
        <div style={{ display: 'flex', gap: 8 }}>
          {passedCount > 0 && (
            <span style={{
              fontSize: 11,
              fontWeight: 600,
              padding: '2px 6px',
              borderRadius: 4,
              background: `${apple.green}20`,
              color: apple.green,
            }}>
              {passedCount} Passed
            </span>
          )}
          {failedCount > 0 && (
            <span style={{
              fontSize: 11,
              fontWeight: 600,
              padding: '2px 6px',
              borderRadius: 4,
              background: `${apple.red}20`,
              color: apple.red,
            }}>
              {failedCount} Failed
            </span>
          )}
          {suite.status === 'running' && (
            <span style={{
              fontSize: 11,
              fontWeight: 600,
              padding: '2px 6px',
              borderRadius: 4,
              background: `${apple.blue}20`,
              color: apple.blue,
            }}>
              Running...
            </span>
          )}
        </div>
      </div>
    </div>
  )
}

export function FloodgateTestPage() {
  const [testSuites, setTestSuites] = useState<TestSuite[]>([])
  const [isLoading, setIsLoading] = useState(false)
  const [selectedTest, setSelectedTest] = useState<TestResult | null>(null)

  // Generate mock test data
  const generateMockTestSuites = (): TestSuite[] => {
    return [
      {
        id: 'api-tests',
        name: 'API Connectivity Tests',
        description: 'Test connections to external APIs and services',
        status: 'passed',
        tests: [
          {
            id: 'test-1',
            name: 'Floodgate API Connection',
            status: 'passed',
            duration: 245,
            timestamp: new Date().toISOString(),
          },
          {
            id: 'test-2',
            name: 'Authentication Service',
            status: 'passed',
            duration: 180,
            timestamp: new Date().toISOString(),
          },
          {
            id: 'test-3',
            name: 'Database Connectivity',
            status: 'failed',
            duration: 5000,
            error: 'Connection timeout: Unable to connect to database after 5000ms',
            timestamp: new Date().toISOString(),
          },
        ]
      },
      {
        id: 'integration-tests',
        name: 'Integration Health Checks',
        description: 'Verify all integrations are functioning correctly',
        status: 'running',
        tests: [
          {
            id: 'test-4',
            name: 'Prometheus Metrics',
            status: 'passed',
            duration: 120,
            timestamp: new Date().toISOString(),
          },
          {
            id: 'test-5',
            name: 'Grafana Dashboard',
            status: 'running',
            timestamp: new Date().toISOString(),
          },
          {
            id: 'test-6',
            name: 'Alert Manager',
            status: 'pending',
            timestamp: new Date().toISOString(),
          },
        ]
      },
      {
        id: 'security-tests',
        name: 'Security & Authentication',
        description: 'Validate security configurations and token management',
        status: 'pending',
        tests: [
          {
            id: 'test-7',
            name: 'Token Validation',
            status: 'pending',
            timestamp: new Date().toISOString(),
          },
          {
            id: 'test-8',
            name: 'RBAC Permissions',
            status: 'pending',
            timestamp: new Date().toISOString(),
          },
          {
            id: 'test-9',
            name: 'SSL Certificate Check',
            status: 'pending',
            timestamp: new Date().toISOString(),
          },
        ]
      },
    ]
  }

  useEffect(() => {
    setIsLoading(true)
    setTimeout(() => {
      setTestSuites(generateMockTestSuites())
      setIsLoading(false)
    }, 1000)
  }, [])

  const runTest = (testId: string) => {
    setTestSuites(prev => prev.map(suite => ({
      ...suite,
      tests: suite.tests.map(test => 
        test.id === testId ? { ...test, status: 'running' } : test
      )
    })))

    // Simulate test execution
    setTimeout(() => {
      setTestSuites(prev => prev.map(suite => ({
        ...suite,
        tests: suite.tests.map(test => 
          test.id === testId ? {
            ...test,
            status: Math.random() > 0.3 ? 'passed' : 'failed',
            duration: Math.floor(Math.random() * 1000) + 100,
            ...(Math.random() < 0.3 && { 
              error: 'Test failed due to network timeout or invalid response'
            }),
            timestamp: new Date().toISOString(),
          } : test
        )
      })))
    }, 2000)
  }

  const runTestSuite = (suiteId: string) => {
    setTestSuites(prev => prev.map(suite => 
      suite.id === suiteId ? {
        ...suite,
        status: 'running',
        tests: suite.tests.map(test => ({ ...test, status: 'running' }))
      } : suite
    ))

    // Simulate suite execution
    setTimeout(() => {
      setTestSuites(prev => prev.map(suite => 
        suite.id === suiteId ? {
          ...suite,
          status: Math.random() > 0.2 ? 'passed' : 'failed',
          tests: suite.tests.map(test => ({
            ...test,
            status: Math.random() > 0.2 ? 'passed' : 'failed',
            duration: Math.floor(Math.random() * 1000) + 100,
            timestamp: new Date().toISOString(),
          }))
        } : suite
      ))
    }, 3000)
  }

  const runAllTests = () => {
    testSuites.forEach(suite => {
      runTestSuite(suite.id)
    })
  }

  const exportResults = () => {
    const results = testSuites.map(suite => ({
      suite: suite.name,
      tests: suite.tests.map(test => ({
        name: test.name,
        status: test.status,
        duration: test.duration,
        error: test.error,
      }))
    }))

    const blob = new Blob([JSON.stringify(results, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `floodgate-test-results-${new Date().toISOString().split('T')[0]}.json`
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
  }

  if (isLoading) {
    return (
      <div style={{
        minHeight: '100vh',
        background: apple.background,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
      }}>
        <div style={{ textAlign: 'center' }}>
          <Loader2 style={{ 
            width: 32, 
            height: 32, 
            color: apple.blue, 
            animation: 'spin 1s linear infinite', 
            margin: '0 auto 16px' 
          }} />
          <p style={{ fontSize: 15, color: apple.secondaryLabel }}>
            Initializing test environment...
          </p>
        </div>
      </div>
    )
  }

  const totalTests = testSuites.reduce((sum, suite) => sum + suite.tests.length, 0)
  const passedTests = testSuites.reduce((sum, suite) => sum + suite.tests.filter(t => t.status === 'passed').length, 0)
  const failedTests = testSuites.reduce((sum, suite) => sum + suite.tests.filter(t => t.status === 'failed').length, 0)
  const runningTests = testSuites.reduce((sum, suite) => sum + suite.tests.filter(t => t.status === 'running').length, 0)

  return (
    <div style={{
      minHeight: '100vh',
      background: apple.background,
    }}>
      <div style={{
        maxWidth: 1200,
        margin: '0 auto',
        padding: '24px 16px',
      }}>
        {/* Header */}
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 32 }}>
          <div>
            <h1 style={{ fontSize: 28, fontWeight: 700, color: apple.label, margin: 0 }}>
              Floodgate Test Suite
            </h1>
            <p style={{ fontSize: 15, color: apple.secondaryLabel, marginTop: 4 }}>
              Test system health and integration connectivity
            </p>
          </div>
          <div style={{ display: 'flex', gap: 8 }}>
            <button
              onClick={exportResults}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 6,
                padding: '8px 12px',
                borderRadius: apple.radius.sm,
                border: `0.5px solid ${apple.separator}`,
                background: apple.fill,
                color: apple.label,
                fontSize: 13,
                fontWeight: 500,
                cursor: 'pointer',
              }}
            >
              <Download style={{ width: 14, height: 14 }} />
              Export
            </button>
            <button
              onClick={runAllTests}
              disabled={runningTests > 0}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 6,
                padding: '8px 12px',
                borderRadius: apple.radius.sm,
                border: 'none',
                background: runningTests > 0 ? apple.gray : apple.blue,
                color: '#fff',
                fontSize: 13,
                fontWeight: 500,
                cursor: runningTests > 0 ? 'default' : 'pointer',
                opacity: runningTests > 0 ? 0.7 : 1,
              }}
            >
              {runningTests > 0 ? (
                <Loader2 style={{ width: 14, height: 14, animation: 'spin 1s linear infinite' }} />
              ) : (
                <Play style={{ width: 14, height: 14 }} />
              )}
              Run All Tests
            </button>
          </div>
        </div>

        {/* Test Stats */}
        <div style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fit, minmax(140px, 1fr))',
          gap: 12,
          marginBottom: 24,
        }}>
          <div style={{
            background: apple.secondaryBackground,
            border: `0.5px solid ${apple.separator}`,
            borderRadius: apple.radius.md,
            padding: '12px 16px',
            textAlign: 'center',
          }}>
            <div style={{ fontSize: 24, fontWeight: 700, color: apple.label, marginBottom: 2 }}>
              {totalTests}
            </div>
            <div style={{ fontSize: 11, color: apple.secondaryLabel, textTransform: 'uppercase', letterSpacing: '0.5px' }}>
              Total Tests
            </div>
          </div>
          <div style={{
            background: apple.secondaryBackground,
            border: `0.5px solid ${apple.separator}`,
            borderRadius: apple.radius.md,
            padding: '12px 16px',
            textAlign: 'center',
          }}>
            <div style={{ fontSize: 24, fontWeight: 700, color: apple.green, marginBottom: 2 }}>
              {passedTests}
            </div>
            <div style={{ fontSize: 11, color: apple.secondaryLabel, textTransform: 'uppercase', letterSpacing: '0.5px' }}>
              Passed
            </div>
          </div>
          <div style={{
            background: apple.secondaryBackground,
            border: `0.5px solid ${apple.separator}`,
            borderRadius: apple.radius.md,
            padding: '12px 16px',
            textAlign: 'center',
          }}>
            <div style={{ fontSize: 24, fontWeight: 700, color: apple.red, marginBottom: 2 }}>
              {failedTests}
            </div>
            <div style={{ fontSize: 11, color: apple.secondaryLabel, textTransform: 'uppercase', letterSpacing: '0.5px' }}>
              Failed
            </div>
          </div>
          <div style={{
            background: apple.secondaryBackground,
            border: `0.5px solid ${apple.separator}`,
            borderRadius: apple.radius.md,
            padding: '12px 16px',
            textAlign: 'center',
          }}>
            <div style={{ fontSize: 24, fontWeight: 700, color: apple.blue, marginBottom: 2 }}>
              {runningTests}
            </div>
            <div style={{ fontSize: 11, color: apple.secondaryLabel, textTransform: 'uppercase', letterSpacing: '0.5px' }}>
              Running
            </div>
          </div>
        </div>

        {/* Test Suites */}
        <div>
          {testSuites.map(suite => (
            <TestSuiteCard
              key={suite.id}
              suite={suite}
              onRunSuite={runTestSuite}
            />
          ))}
        </div>

        {/* Individual Tests */}
        <div style={{ marginTop: 32 }}>
          <h2 style={{ fontSize: 20, fontWeight: 600, color: apple.label, marginBottom: 16 }}>
            All Tests
          </h2>
          <div style={{ 
            display: 'grid', 
            gridTemplateColumns: 'repeat(auto-fill, minmax(300px, 1fr))', 
            gap: 12 
          }}>
            {testSuites.flatMap(suite => suite.tests).map(test => (
              <TestCard
                key={test.id}
                test={test}
                onRun={runTest}
                onViewDetails={setSelectedTest}
              />
            ))}
          </div>
        </div>
      </div>

      {/* Test Details Modal */}
      <AnimatePresence>
        {selectedTest && (
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            onClick={() => setSelectedTest(null)}
            style={{
              position: 'fixed',
              inset: 0,
              zIndex: 50,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              background: 'rgba(0, 0, 0, 0.4)',
              backdropFilter: 'blur(20px)',
              WebkitBackdropFilter: 'blur(20px)',
            }}
          >
            <motion.div
              initial={{ opacity: 0, scale: 0.95 }}
              animate={{ opacity: 1, scale: 1 }}
              exit={{ opacity: 0, scale: 0.95 }}
              onClick={(e) => e.stopPropagation()}
              style={{
                width: '90%',
                maxWidth: 500,
                background: apple.secondaryBackground,
                borderRadius: apple.radius.xl,
                boxShadow: '0 24px 80px rgba(0,0,0,0.2)',
                overflow: 'hidden',
              }}
            >
              {/* Header */}
              <div style={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                padding: '16px 20px',
                borderBottom: `0.5px solid ${apple.separator}`,
              }}>
                <h3 style={{ fontSize: 17, fontWeight: 600, color: apple.label, margin: 0 }}>
                  Test Details
                </h3>
                <button
                  onClick={() => setSelectedTest(null)}
                  style={{
                    width: 28,
                    height: 28,
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
                  ×
                </button>
              </div>

              {/* Content */}
              <div style={{ padding: 20 }}>
                <div style={{ marginBottom: 16 }}>
                  <h4 style={{ fontSize: 16, fontWeight: 600, color: apple.label, marginBottom: 8 }}>
                    {selectedTest.name}
                  </h4>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12 }}>
                    <span style={{
                      fontSize: 11,
                      fontWeight: 600,
                      padding: '3px 8px',
                      borderRadius: 5,
                      textTransform: 'uppercase',
                      background: `${apple.blue}20`,
                      color: apple.blue,
                    }}>
                      {selectedTest.status}
                    </span>
                    {selectedTest.duration && (
                      <span style={{ fontSize: 12, color: apple.tertiaryLabel }}>
                        {selectedTest.duration}ms
                      </span>
                    )}
                  </div>
                </div>

                {selectedTest.error && (
                  <div style={{
                    padding: 12,
                    background: `${apple.red}10`,
                    border: `0.5px solid ${apple.red}30`,
                    borderRadius: apple.radius.sm,
                    marginBottom: 16,
                  }}>
                    <h5 style={{ fontSize: 13, fontWeight: 600, color: apple.red, marginBottom: 6 }}>
                      Error Details
                    </h5>
                    <p style={{ 
                      fontSize: 12, 
                      color: apple.red, 
                      margin: 0, 
                      fontFamily: 'SFMono-Regular, Consolas, monospace',
                      lineHeight: 1.4,
                    }}>
                      {selectedTest.error}
                    </p>
                  </div>
                )}

                <div style={{
                  background: apple.tertiaryFill,
                  borderRadius: apple.radius.sm,
                  padding: 12,
                  marginBottom: 16,
                }}>
                  <div style={{ fontSize: 12, color: apple.secondaryLabel, marginBottom: 4 }}>
                    Executed at
                  </div>
                  <div style={{ fontSize: 14, color: apple.label }}>
                    {new Date(selectedTest.timestamp).toLocaleString()}
                  </div>
                </div>

                <div style={{ display: 'flex', gap: 8 }}>
                  <button
                    onClick={() => runTest(selectedTest.id)}
                    style={{
                      flex: 1,
                      padding: '10px 16px',
                      borderRadius: apple.radius.sm,
                      border: 'none',
                      background: apple.blue,
                      color: '#fff',
                      fontSize: 14,
                      fontWeight: 500,
                      cursor: 'pointer',
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      gap: 6,
                    }}
                  >
                    <RefreshCw style={{ width: 14, height: 14 }} />
                    Rerun Test
                  </button>
                  <button
                    onClick={() => navigator.clipboard.writeText(selectedTest.error || selectedTest.name)}
                    style={{
                      flex: 1,
                      padding: '10px 16px',
                      borderRadius: apple.radius.sm,
                      border: `0.5px solid ${apple.separator}`,
                      background: apple.fill,
                      color: apple.label,
                      fontSize: 14,
                      fontWeight: 500,
                      cursor: 'pointer',
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      gap: 6,
                    }}
                  >
                    <Copy style={{ width: 14, height: 14 }} />
                    Copy
                  </button>
                </div>
              </div>
            </motion.div>
          </motion.div>
        )}
      </AnimatePresence>

      {/* Global keyframes */}
      <style>{`@keyframes spin { from { transform: rotate(0deg) } to { transform: rotate(360deg) } }`}</style>
    </div>
  )
}
