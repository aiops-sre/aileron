import React, { useState, useEffect } from 'react'
import { motion } from 'framer-motion'
import {
  Search,
  Server,
  Database,
  Globe,
  Shield,
  CheckCircle,
  XCircle,
  Play,
  Pause,
  Settings,
  RefreshCw,
  Eye,
  Plus,
  Loader2,
  Activity,
  Clock,
  Zap,
  AlertTriangle,
} from 'lucide-react'

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

interface DiscoveredService {
  id: string
  name: string
  type: 'database' | 'api' | 'cache' | 'message-queue' | 'monitoring' | 'other'
  host: string
  port: number
  status: 'healthy' | 'degraded' | 'unhealthy' | 'unknown'
  lastSeen: string
  version?: string
  tags: string[]
  metadata: Record<string, any>
}

interface DiscoveryTarget {
  id: string
  name: string
  type: 'kubernetes' | 'consul' | 'eureka' | 'dns' | 'network-scan'
  endpoint: string
  enabled: boolean
  lastRun: string
  nextRun: string
  status: 'running' | 'idle' | 'error'
  servicesFound: number
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Components
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

function ServiceCard({ service }: { service: DiscoveredService }) {
  const getStatusColor = (status: string) => {
    switch (status) {
      case 'healthy': return apple.green
      case 'degraded': return apple.orange
      case 'unhealthy': return apple.red
      default: return apple.gray
    }
  }

  const getTypeIcon = (type: string) => {
    switch (type) {
      case 'database': return Database
      case 'api': return Globe
      case 'cache': return Zap
      case 'message-queue': return Activity
      case 'monitoring': return Eye
      default: return Server
    }
  }

  const TypeIcon = getTypeIcon(service.type)

  return (
    <motion.div
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      whileHover={{ y: -2 }}
      style={{
        background: apple.secondaryBackground,
        border: `0.5px solid ${apple.separator}`,
        borderRadius: apple.radius.lg,
        padding: 16,
        cursor: 'pointer',
        transition: 'all 0.2s ease',
      }}
      onMouseEnter={(e) => {
        e.currentTarget.style.boxShadow = '0 8px 32px rgba(0,0,0,0.12)'
        e.currentTarget.style.borderColor = getStatusColor(service.status)
      }}
      onMouseLeave={(e) => {
        e.currentTarget.style.boxShadow = 'none'
        e.currentTarget.style.borderColor = apple.separator
      }}
    >
      {/* Header */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 12 }}>
        <div style={{
          width: 40,
          height: 40,
          borderRadius: apple.radius.md,
          background: getStatusColor(service.status),
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
        }}>
          <TypeIcon style={{ width: 20, height: 20, color: '#fff' }} />
        </div>
        <div style={{ flex: 1 }}>
          <h3 style={{ fontSize: 15, fontWeight: 600, color: apple.label, margin: 0 }}>
            {service.name}
          </h3>
          <p style={{ fontSize: 12, color: apple.secondaryLabel, margin: 0 }}>
            {service.host}:{service.port}
          </p>
        </div>
        <div style={{
          width: 8,
          height: 8,
          borderRadius: '50%',
          background: getStatusColor(service.status),
          flexShrink: 0,
        }} />
      </div>

      {/* Status & Type */}
      <div style={{ display: 'flex', gap: 6, marginBottom: 8 }}>
        <span style={{
          fontSize: 11,
          fontWeight: 600,
          padding: '2px 6px',
          borderRadius: 4,
          textTransform: 'uppercase',
          background: `${getStatusColor(service.status)}20`,
          color: getStatusColor(service.status),
        }}>
          {service.status}
        </span>
        <span style={{
          fontSize: 11,
          fontWeight: 500,
          padding: '2px 6px',
          borderRadius: 4,
          background: apple.fill,
          color: apple.secondaryLabel,
          textTransform: 'capitalize',
        }}>
          {service.type.replace('-', ' ')}
        </span>
        {service.version && (
          <span style={{
            fontSize: 11,
            fontWeight: 500,
            padding: '2px 6px',
            borderRadius: 4,
            background: apple.fill,
            color: apple.secondaryLabel,
          }}>
            v{service.version}
          </span>
        )}
      </div>

      {/* Tags */}
      {service.tags.length > 0 && (
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4, marginBottom: 8 }}>
          {service.tags.slice(0, 3).map(tag => (
            <span
              key={tag}
              style={{
                fontSize: 10,
                padding: '1px 4px',
                borderRadius: 3,
                background: apple.tertiaryFill,
                color: apple.tertiaryLabel,
              }}
            >
              {tag}
            </span>
          ))}
          {service.tags.length > 3 && (
            <span style={{
              fontSize: 10,
              color: apple.tertiaryLabel,
            }}>
              +{service.tags.length - 3} more
            </span>
          )}
        </div>
      )}

      {/* Last Seen */}
      <div style={{ 
        fontSize: 11, 
        color: apple.tertiaryLabel,
        display: 'flex',
        alignItems: 'center',
        gap: 4,
      }}>
        <Clock style={{ width: 10, height: 10 }} />
        Last seen {new Date(service.lastSeen).toLocaleString()}
      </div>
    </motion.div>
  )
}

function DiscoveryTargetCard({ target, onToggle, onRun }: { 
  target: DiscoveryTarget
  onToggle: (id: string) => void
  onRun: (id: string) => void
}) {
  const getStatusColor = (status: string) => {
    switch (status) {
      case 'running': return apple.blue
      case 'idle': return apple.gray
      case 'error': return apple.red
      default: return apple.gray
    }
  }

  return (
    <div style={{
      background: apple.secondaryBackground,
      border: `0.5px solid ${apple.separator}`,
      borderRadius: apple.radius.lg,
      padding: 16,
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 12 }}>
        <div style={{
          width: 36,
          height: 36,
          borderRadius: apple.radius.sm,
          background: target.enabled ? getStatusColor(target.status) : apple.fill,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
        }}>
          <Search style={{ width: 18, height: 18, color: target.enabled ? '#fff' : apple.tertiaryLabel }} />
        </div>
        <div style={{ flex: 1 }}>
          <h3 style={{ fontSize: 14, fontWeight: 600, color: apple.label, margin: 0 }}>
            {target.name}
          </h3>
          <p style={{ fontSize: 12, color: apple.secondaryLabel, margin: 0 }}>
            {target.endpoint} • {target.servicesFound} services
          </p>
        </div>
        <div style={{ display: 'flex', gap: 6 }}>
          <button
            onClick={() => onRun(target.id)}
            disabled={target.status === 'running'}
            style={{
              width: 28,
              height: 28,
              borderRadius: apple.radius.sm,
              border: 'none',
              background: target.status === 'running' ? apple.gray : apple.blue,
              color: '#fff',
              cursor: target.status === 'running' ? 'default' : 'pointer',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              opacity: target.status === 'running' ? 0.5 : 1,
            }}
          >
            {target.status === 'running' ? (
              <Loader2 style={{ width: 14, height: 14, animation: 'spin 1s linear infinite' }} />
            ) : (
              <Play style={{ width: 14, height: 14 }} />
            )}
          </button>
          <button
            onClick={() => onToggle(target.id)}
            style={{
              width: 28,
              height: 28,
              borderRadius: apple.radius.sm,
              border: `0.5px solid ${apple.separator}`,
              background: target.enabled ? apple.green : apple.fill,
              color: target.enabled ? '#fff' : apple.label,
              cursor: 'pointer',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
            }}
          >
            {target.enabled ? (
              <CheckCircle style={{ width: 14, height: 14 }} />
            ) : (
              <Pause style={{ width: 14, height: 14 }} />
            )}
          </button>
        </div>
      </div>

      <div style={{ display: 'flex', gap: 6, fontSize: 11, color: apple.tertiaryLabel }}>
        <span>Last: {new Date(target.lastRun).toLocaleString()}</span>
        {target.nextRun && (
          <>
            <span>•</span>
            <span>Next: {new Date(target.nextRun).toLocaleString()}</span>
          </>
        )}
      </div>
    </div>
  )
}

export function AutoDiscoveryPage() {
  const [services, setServices] = useState<DiscoveredService[]>([])
  const [targets, setTargets] = useState<DiscoveryTarget[]>([])
  const [isLoading, setIsLoading] = useState(false)
  const [searchQuery, setSearchQuery] = useState('')
  const [selectedType, setSelectedType] = useState<string>('')
  const [selectedStatus, setSelectedStatus] = useState<string>('')

  // Mock data generation
  const generateMockServices = (): DiscoveredService[] => {
    const types = ['database', 'api', 'cache', 'message-queue', 'monitoring'] as const
    const statuses = ['healthy', 'degraded', 'unhealthy', 'unknown'] as const
    const hosts = ['prod-db-1', 'api-gateway', 'redis-cluster', 'kafka-broker', 'prometheus']
    
    return Array.from({ length: 24 }, (_, i) => ({
      id: `service-${i}`,
      name: `${hosts[i % hosts.length]}-${Math.floor(i / hosts.length) + 1}`,
      type: types[i % types.length],
      host: `${hosts[i % hosts.length]}.internal.com`,
      port: 3000 + (i * 100),
      status: statuses[i % statuses.length],
      lastSeen: new Date(Date.now() - Math.random() * 3600000).toISOString(),
      version: Math.random() > 0.3 ? `${Math.floor(Math.random() * 5) + 1}.${Math.floor(Math.random() * 10)}.${Math.floor(Math.random() * 10)}` : undefined,
      tags: ['production', `env-${i % 3}`, types[i % types.length]],
      metadata: {
        cluster: `cluster-${Math.floor(i / 8) + 1}`,
        namespace: `app-${Math.floor(i / 4) + 1}`,
      }
    }))
  }

  const generateMockTargets = (): DiscoveryTarget[] => {
    return [
      {
        id: 'k8s-prod',
        name: 'Production Kubernetes',
        type: 'kubernetes',
        endpoint: 'https://k8s-prod.internal.com',
        enabled: true,
        lastRun: new Date(Date.now() - 300000).toISOString(),
        nextRun: new Date(Date.now() + 3300000).toISOString(),
        status: 'idle',
        servicesFound: 15,
      },
      {
        id: 'consul-dc1',
        name: 'Consul Datacenter 1',
        type: 'consul',
        endpoint: 'https://consul.internal.com:8500',
        enabled: true,
        lastRun: new Date(Date.now() - 180000).toISOString(),
        nextRun: new Date(Date.now() + 3420000).toISOString(),
        status: 'running',
        servicesFound: 8,
      },
      {
        id: 'network-scan',
        name: 'Network Discovery',
        type: 'network-scan',
        endpoint: '10.0.0.0/16',
        enabled: false,
        lastRun: new Date(Date.now() - 86400000).toISOString(),
        nextRun: new Date(Date.now() + 82800000).toISOString(),
        status: 'idle',
        servicesFound: 32,
      },
    ]
  }

  useEffect(() => {
    const loadData = async () => {
      setIsLoading(true)
      
      // Simulate API call
      setTimeout(() => {
        setServices(generateMockServices())
        setTargets(generateMockTargets())
        setIsLoading(false)
      }, 1000)
    }

    loadData()
  }, [])

  const filteredServices = services.filter(service => {
    const matchesSearch = !searchQuery || 
      service.name.toLowerCase().includes(searchQuery.toLowerCase()) ||
      service.host.toLowerCase().includes(searchQuery.toLowerCase())
    
    const matchesType = !selectedType || service.type === selectedType
    const matchesStatus = !selectedStatus || service.status === selectedStatus
    
    return matchesSearch && matchesType && matchesStatus
  })

  const runDiscovery = (targetId: string) => {
    setTargets(prev => prev.map(t => 
      t.id === targetId ? { ...t, status: 'running' } : t
    ))
    
    // Simulate discovery run
    setTimeout(() => {
      setTargets(prev => prev.map(t => 
        t.id === targetId ? { 
          ...t, 
          status: 'idle',
          lastRun: new Date().toISOString(),
          nextRun: new Date(Date.now() + 3600000).toISOString(),
          servicesFound: t.servicesFound + Math.floor(Math.random() * 5)
        } : t
      ))
    }, 3000)
  }

  const toggleTarget = (targetId: string) => {
    setTargets(prev => prev.map(t => 
      t.id === targetId ? { ...t, enabled: !t.enabled } : t
    ))
  }

  const runAllDiscovery = () => {
    targets.filter(t => t.enabled).forEach(target => {
      runDiscovery(target.id)
    })
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
            Discovering services...
          </p>
        </div>
      </div>
    )
  }

  const healthyCount = services.filter(s => s.status === 'healthy').length
  const degradedCount = services.filter(s => s.status === 'degraded').length
  const unhealthyCount = services.filter(s => s.status === 'unhealthy').length
  const activeTargets = targets.filter(t => t.enabled).length

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
              Auto Discovery
            </h1>
            <p style={{ fontSize: 15, color: apple.secondaryLabel, marginTop: 4 }}>
              Automatically discover and monitor services in your infrastructure
            </p>
          </div>
          <div style={{ display: 'flex', gap: 8 }}>
            <button
              onClick={() => console.log('Configure discovery')}
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
              <Settings style={{ width: 14, height: 14 }} />
              Configure
            </button>
            <button
              onClick={runAllDiscovery}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 6,
                padding: '8px 12px',
                borderRadius: apple.radius.sm,
                border: 'none',
                background: apple.blue,
                color: '#fff',
                fontSize: 13,
                fontWeight: 500,
                cursor: 'pointer',
              }}
            >
              <RefreshCw style={{ width: 14, height: 14 }} />
              Run All
            </button>
          </div>
        </div>

        {/* Stats */}
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
              {services.length}
            </div>
            <div style={{ fontSize: 11, color: apple.secondaryLabel, textTransform: 'uppercase', letterSpacing: '0.5px' }}>
              Total Services
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
              {healthyCount}
            </div>
            <div style={{ fontSize: 11, color: apple.secondaryLabel, textTransform: 'uppercase', letterSpacing: '0.5px' }}>
              Healthy
            </div>
          </div>
          <div style={{
            background: apple.secondaryBackground,
            border: `0.5px solid ${apple.separator}`,
            borderRadius: apple.radius.md,
            padding: '12px 16px',
            textAlign: 'center',
          }}>
            <div style={{ fontSize: 24, fontWeight: 700, color: apple.orange, marginBottom: 2 }}>
              {degradedCount}
            </div>
            <div style={{ fontSize: 11, color: apple.secondaryLabel, textTransform: 'uppercase', letterSpacing: '0.5px' }}>
              Degraded
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
              {unhealthyCount}
            </div>
            <div style={{ fontSize: 11, color: apple.secondaryLabel, textTransform: 'uppercase', letterSpacing: '0.5px' }}>
              Unhealthy
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
              {activeTargets}
            </div>
            <div style={{ fontSize: 11, color: apple.secondaryLabel, textTransform: 'uppercase', letterSpacing: '0.5px' }}>
              Active Targets
            </div>
          </div>
        </div>

        {/* Discovery Targets */}
        <div style={{ marginBottom: 32 }}>
          <h2 style={{ fontSize: 18, fontWeight: 600, color: apple.label, marginBottom: 16 }}>
            Discovery Targets
          </h2>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(300px, 1fr))', gap: 12 }}>
            {targets.map(target => (
              <DiscoveryTargetCard
                key={target.id}
                target={target}
                onToggle={toggleTarget}
                onRun={runDiscovery}
              />
            ))}
          </div>
        </div>

        {/* Services Section */}
        <div>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 16 }}>
            <h2 style={{ fontSize: 18, fontWeight: 600, color: apple.label, margin: 0 }}>
              Discovered Services
            </h2>
            <p style={{ fontSize: 13, color: apple.secondaryLabel }}>
              {filteredServices.length} of {services.length} services
            </p>
          </div>

          {/* Filters */}
          <div style={{ display: 'flex', gap: 12, marginBottom: 20, alignItems: 'center', flexWrap: 'wrap' }}>
            <div style={{ width: 240, position: 'relative' }}>
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
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                placeholder="Search services..."
                style={{
                  width: '100%',
                  height: 36,
                  borderRadius: apple.radius.md,
                  border: 'none',
                  background: apple.fill,
                  paddingLeft: 34,
                  paddingRight: 12,
                  fontSize: 14,
                  color: apple.label,
                  outline: 'none',
                }}
              />
            </div>
            
            <select
              value={selectedType}
              onChange={(e) => setSelectedType(e.target.value)}
              style={{
                height: 36,
                borderRadius: apple.radius.md,
                border: 'none',
                background: apple.fill,
                padding: '0 24px 0 12px',
                fontSize: 13,
                color: apple.label,
                outline: 'none',
                appearance: 'none',
                cursor: 'pointer',
              }}
            >
              <option value="">All Types</option>
              <option value="database">Database</option>
              <option value="api">API</option>
              <option value="cache">Cache</option>
              <option value="message-queue">Message Queue</option>
              <option value="monitoring">Monitoring</option>
            </select>

            <select
              value={selectedStatus}
              onChange={(e) => setSelectedStatus(e.target.value)}
              style={{
                height: 36,
                borderRadius: apple.radius.md,
                border: 'none',
                background: apple.fill,
                padding: '0 24px 0 12px',
                fontSize: 13,
                color: apple.label,
                outline: 'none',
                appearance: 'none',
                cursor: 'pointer',
              }}
            >
              <option value="">All Status</option>
              <option value="healthy">Healthy</option>
              <option value="degraded">Degraded</option>
              <option value="unhealthy">Unhealthy</option>
              <option value="unknown">Unknown</option>
            </select>

            <button
              onClick={() => {
                setSearchQuery('')
                setSelectedType('')
                setSelectedStatus('')
              }}
              style={{
                padding: '8px 12px',
                borderRadius: apple.radius.sm,
                border: `0.5px solid ${apple.separator}`,
                background: apple.fill,
                color: apple.secondaryLabel,
                fontSize: 13,
                cursor: 'pointer',
                display: 'flex',
                alignItems: 'center',
                gap: 4,
              }}
            >
              Clear
            </button>
          </div>

          {/* Services Grid */}
          {filteredServices.length === 0 ? (
            <div style={{
              textAlign: 'center',
              padding: '80px 20px',
              background: apple.secondaryBackground,
              borderRadius: apple.radius.lg,
              border: `0.5px solid ${apple.separator}`,
            }}>
              <Server style={{ width: 48, height: 48, color: apple.quaternaryLabel, margin: '0 auto 16px' }} />
              <h3 style={{ fontSize: 17, fontWeight: 500, color: apple.label, margin: '0 0 8px' }}>
                {searchQuery || selectedType || selectedStatus ? 'No matching services' : 'No services discovered'}
              </h3>
              <p style={{ fontSize: 13, color: apple.tertiaryLabel }}>
                {searchQuery || selectedType || selectedStatus 
                  ? 'Try adjusting your search criteria.'
                  : 'Run discovery targets to find services in your infrastructure.'
                }
              </p>
            </div>
          ) : (
            <div style={{ 
              display: 'grid', 
              gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))', 
              gap: 16 
            }}>
              {filteredServices.map(service => (
                <ServiceCard key={service.id} service={service} />
              ))}
            </div>
          )}
        </div>
      </div>

      {/* Global keyframes */}
      <style>{`@keyframes spin { from { transform: rotate(0deg) } to { transform: rotate(360deg) } }`}</style>
    </div>
  )
}
