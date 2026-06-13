import React, { useState, useEffect, useCallback } from 'react'
import { Server, Database, CheckCircle, AlertCircle, Search, RefreshCw, ChevronDown, ChevronRight } from 'lucide-react'

const tokens = {
  blue: '#007AFF',
  green: '#34C759',
  red: '#FF3B30',
  orange: '#FF9500',
  gray: '#8E8E93',
  label: 'var(--color-text)',
  secondaryLabel: 'var(--color-text-secondary)',
  tertiaryLabel: 'var(--color-text-tertiary, #8E8E93)',
  background: 'var(--color-background)',
  secondaryBackground: 'var(--color-card, rgba(255, 255, 255, 0.8))',
  separator: 'var(--color-separator, rgba(142, 142, 147, 0.12))',
  fill: 'var(--color-fill, rgba(142, 142, 147, 0.08))',
  radius: { sm: 6, md: 10, lg: 12 },
}

interface VM {
  id: string
  name: string
  status: string
  health_status: string
  properties?: any
}

interface Host {
  id: string
  name: string
  type: string
  status: string
  health_status: string
  properties?: any
  vms: VM[]
}

export function HostVMMapping() {
  const [hosts, setHosts] = useState<Host[]>([])
  const [loading, setLoading] = useState(true)
  const [expandedHosts, setExpandedHosts] = useState<Set<string>>(new Set())
  const [searchTerm, setSearchTerm] = useState('')
  const [selectedRegion, setSelectedRegion] = useState('region-a')

  const loadTopology = useCallback(async () => {
    try {
      setLoading(true)
      const response = await fetch(`/api/v1/topology/infrastructure/${selectedRegion}`, {
        headers: { 'Authorization': `Bearer ${localStorage.getItem('accessToken')}` },
      })
      const data = await response.json()
      
      console.log('Topology response:', data)
      
      if (data.success && data.data) {
        const cloudstackNodes = data.data.layers?.cloudstack || []
        const relationships = data.data.relationships || []
        
        console.log('CloudStack nodes:', cloudstackNodes.length)
        console.log('Relationships:', relationships.length)
        
        // Separate hosts and VMs
        const hostNodes = cloudstackNodes.filter((n: any) => n.type === 'host')
        const vmNodes = cloudstackNodes.filter((n: any) => n.type === 'vm')
        
        console.log('Hosts:', hostNodes.length, 'VMs:', vmNodes.length)
        
        // Build host-VM mapping
        const hostsWithVMs: Host[] = hostNodes.map((host: any) => ({
          ...host,
          vms: vmNodes.filter((vm: any) =>
            relationships.some((r: any) => r.source_id === vm.id && r.target_id === host.id && r.type === 'runs_on')
          )
        }))
        
        console.log('Mapped hosts with VMs:', hostsWithVMs.length, 'Total VMs:', hostsWithVMs.reduce((s, h) => s + h.vms.length, 0))
        setHosts(hostsWithVMs)
      } else {
        console.error('API response not successful:', data)
      }
    } catch (error) {
      console.error('Failed to load topology:', error)
    } finally {
      setLoading(false)
    }
  }, [selectedRegion])

  useEffect(() => {
    loadTopology()
  }, [loadTopology])

  const toggleHost = (hostId: string) => {
    setExpandedHosts(prev => {
      const newSet = new Set(prev)
      if (newSet.has(hostId)) {
        newSet.delete(hostId)
      } else {
        newSet.add(hostId)
      }
      return newSet
    })
  }

  const toggleAllHosts = () => {
    if (expandedHosts.size === hosts.length) {
      setExpandedHosts(new Set())
    } else {
      setExpandedHosts(new Set(hosts.map(h => h.id)))
    }
  }

  const filteredHosts = searchTerm
    ? hosts.filter(h => 
        h.name.toLowerCase().includes(searchTerm.toLowerCase()) ||
        h.vms.some(vm => vm.name.toLowerCase().includes(searchTerm.toLowerCase()))
      )
    : hosts

  const totalVMs = hosts.reduce((sum, h) => sum + h.vms.length, 0)
  const healthyHosts = hosts.filter(h => h.health_status === 'healthy').length
  const healthyVMs = hosts.reduce((sum, h) => sum + h.vms.filter(vm => vm.health_status === 'healthy').length, 0)

  return (
    <div style={{ padding: 24, maxWidth: 1600, margin: '0 auto', background: tokens.background, minHeight: '100vh' }}>
      {/* Header */}
      <div style={{ marginBottom: 24 }}>
        <h1 style={{ fontSize: 28, fontWeight: 600, color: tokens.label, marginBottom: 8 }}>
          Host to VM Mapping
        </h1>
        <p style={{ fontSize: 15, color: tokens.secondaryLabel }}>
          Infrastructure host and virtual machine relationships
        </p>
      </div>

      {/* Controls */}
      <div style={{ display: 'flex', gap: 12, marginBottom: 20 }}>
        <div style={{ flex: 1, position: 'relative' }}>
          <Search style={{ position: 'absolute', left: 12, top: '50%', transform: 'translateY(-50%)', width: 16, height: 16, color: tokens.tertiaryLabel }} />
          <input
            type="text"
            placeholder="Search hosts or VMs..."
            value={searchTerm}
            onChange={(e) => setSearchTerm(e.target.value)}
            style={{
              width: '100%',
              paddingLeft: 40,
              paddingRight: 12,
              height: 36,
              borderRadius: tokens.radius.sm,
              border: `0.5px solid ${tokens.separator}`,
              background: tokens.secondaryBackground,
              fontSize: 13,
              color: tokens.label,
              outline: 'none',
            }}
          />
        </div>

        <select
          value={selectedRegion}
          onChange={(e) => setSelectedRegion(e.target.value)}
          style={{
            height: 36,
            borderRadius: tokens.radius.sm,
            border: `0.5px solid ${tokens.separator}`,
            background: tokens.secondaryBackground,
            padding: '0 12px',
            fontSize: 13,
            color: tokens.label,
            outline: 'none',
          }}
        >
          <option value="region-a">Region A</option>
          <option value="region-b">Region B</option>
        </select>

        <button
          onClick={toggleAllHosts}
          style={{
            height: 36,
            padding: '0 16px',
            borderRadius: tokens.radius.sm,
            border: `0.5px solid ${tokens.separator}`,
            background: tokens.secondaryBackground,
            fontSize: 13,
            fontWeight: 500,
            color: tokens.label,
            cursor: 'pointer',
          }}
        >
          {expandedHosts.size === hosts.length ? 'Collapse All' : 'Expand All'}
        </button>

        <button
          onClick={loadTopology}
          disabled={loading}
          style={{
            height: 36,
            padding: '0 16px',
            borderRadius: tokens.radius.sm,
            border: 'none',
            background: tokens.blue,
            color: '#fff',
            fontSize: 13,
            fontWeight: 500,
            cursor: loading ? 'default' : 'pointer',
            opacity: loading ? 0.6 : 1,
            display: 'flex',
            alignItems: 'center',
            gap: 6,
          }}
        >
          <RefreshCw style={{ width: 14, height: 14 }} />
          Refresh
        </button>
      </div>

      {/* Statistics */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 16, marginBottom: 24 }}>
        <div style={{
          padding: 20,
          background: tokens.secondaryBackground,
          borderRadius: tokens.radius.lg,
          border: `0.5px solid ${tokens.separator}`,
        }}>
          <div style={{ fontSize: 13, color: tokens.tertiaryLabel, marginBottom: 4 }}>Total Hosts</div>
          <div style={{ fontSize: 32, fontWeight: 600, color: tokens.label }}>{hosts.length}</div>
        </div>
        <div style={{
          padding: 20,
          background: tokens.secondaryBackground,
          borderRadius: tokens.radius.lg,
          border: `0.5px solid ${tokens.separator}`,
        }}>
          <div style={{ fontSize: 13, color: tokens.tertiaryLabel, marginBottom: 4 }}>Total VMs</div>
          <div style={{ fontSize: 32, fontWeight: 600, color: tokens.blue }}>{totalVMs}</div>
        </div>
        <div style={{
          padding: 20,
          background: tokens.secondaryBackground,
          borderRadius: tokens.radius.lg,
          border: `0.5px solid ${tokens.separator}`,
        }}>
          <div style={{ fontSize: 13, color: tokens.tertiaryLabel, marginBottom: 4 }}>Healthy Hosts</div>
          <div style={{ fontSize: 32, fontWeight: 600, color: tokens.green }}>{healthyHosts}</div>
        </div>
        <div style={{
          padding: 20,
          background: tokens.secondaryBackground,
          borderRadius: tokens.radius.lg,
          border: `0.5px solid ${tokens.separator}`,
        }}>
          <div style={{ fontSize: 13, color: tokens.tertiaryLabel, marginBottom: 4 }}>Healthy VMs</div>
          <div style={{ fontSize: 32, fontWeight: 600, color: tokens.green }}>{healthyVMs}</div>
        </div>
      </div>

      {/* Host List */}
      {loading ? (
        <div style={{
          background: tokens.secondaryBackground,
          borderRadius: tokens.radius.lg,
          border: `0.5px solid ${tokens.separator}`,
          padding: 60,
          textAlign: 'center',
        }}>
          <div style={{ fontSize: 15, color: tokens.secondaryLabel }}>Loading topology...</div>
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          {filteredHosts.map((host) => {
            const isExpanded = expandedHosts.has(host.id)
            const healthIcon = host.health_status === 'healthy' ? CheckCircle : AlertCircle
            const healthColor = host.health_status === 'healthy' ? tokens.green : tokens.red
            
            return (
              <div
                key={host.id}
                style={{
                  background: tokens.secondaryBackground,
                  borderRadius: tokens.radius.lg,
                  border: `0.5px solid ${tokens.separator}`,
                  overflow: 'hidden',
                }}
              >
                {/* Host Header */}
                <div
                  onClick={() => toggleHost(host.id)}
                  style={{
                    padding: '16px 20px',
                    cursor: 'pointer',
                    display: 'flex',
                    alignItems: 'center',
                    gap: 16,
                    background: isExpanded ? tokens.fill : '#fff',
                    borderBottom: isExpanded ? `0.5px solid ${tokens.separator}` : 'none',
                  }}
                >
                  {isExpanded ? (
                    <ChevronDown style={{ width: 16, height: 16, color: tokens.tertiaryLabel }} />
                  ) : (
                    <ChevronRight style={{ width: 16, height: 16, color: tokens.tertiaryLabel }} />
                  )}
                  
                  <div style={{
                    width: 40,
                    height: 40,
                    borderRadius: tokens.radius.md,
                    background: `${healthColor}15`,
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                  }}>
                    <Server style={{ width: 20, height: 20, color: healthColor }} />
                  </div>

                  <div style={{ flex: 1 }}>
                    <div style={{ fontSize: 15, fontWeight: 600, color: tokens.label, marginBottom: 2 }}>
                      {host.name}
                    </div>
                    <div style={{ fontSize: 13, color: tokens.tertiaryLabel }}>
                      {host.vms.length} VMs · {host.status}
                    </div>
                  </div>

                  <div style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
                    {React.createElement(healthIcon, { style: { width: 16, height: 16, color: healthColor } })}
                    <span style={{ fontSize: 13, fontWeight: 500, color: healthColor }}>
                      {host.health_status}
                    </span>
                  </div>

                  <div style={{
                    padding: '4px 12px',
                    borderRadius: 12,
                    background: tokens.fill,
                    fontSize: 13,
                    fontWeight: 600,
                    color: tokens.label,
                  }}>
                    {host.vms.length}
                  </div>
                </div>

                {/* VMs List */}
                {isExpanded && (
                  <div style={{ padding: 20 }}>
                    {host.vms.length === 0 ? (
                      <div style={{ padding: 40, textAlign: 'center', color: tokens.tertiaryLabel, fontSize: 13 }}>
                        No virtual machines on this host
                      </div>
                    ) : (
                      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(320px, 1fr))', gap: 12 }}>
                        {host.vms.map((vm) => {
                          const vmHealthIcon = vm.health_status === 'healthy' ? CheckCircle : AlertCircle
                          const vmHealthColor = vm.health_status === 'healthy' ? tokens.green : tokens.red
                          
                          return (
                            <div
                              key={vm.id}
                              style={{
                                padding: 12,
                                background: tokens.fill,
                                borderRadius: tokens.radius.sm,
                                border: `0.5px solid ${tokens.separator}`,
                                display: 'flex',
                                alignItems: 'center',
                                gap: 12,
                              }}
                            >
                              <div style={{
                                width: 32,
                                height: 32,
                                borderRadius: tokens.radius.sm,
                                background: `${vmHealthColor}15`,
                                display: 'flex',
                                alignItems: 'center',
                                justifyContent: 'center',
                                flexShrink: 0,
                              }}>
                                <Database style={{ width: 16, height: 16, color: vmHealthColor }} />
                              </div>

                              <div style={{ flex: 1, minWidth: 0 }}>
                                <div style={{
                                  fontSize: 13,
                                  fontWeight: 500,
                                  color: tokens.label,
                                  marginBottom: 2,
                                  overflow: 'hidden',
                                  textOverflow: 'ellipsis',
                                  whiteSpace: 'nowrap',
                                }}>
                                  {vm.name}
                                </div>
                                <div style={{ fontSize: 11, color: tokens.tertiaryLabel }}>
                                  {vm.status}
                                  {vm.properties?.cpu && ` · ${vm.properties.cpu} vCPU`}
                                  {vm.properties?.memory && ` · ${vm.properties.memory}GB RAM`}
                                </div>
                              </div>

                              {React.createElement(vmHealthIcon, { 
                                style: { width: 14, height: 14, color: vmHealthColor, flexShrink: 0 } 
                              })}
                            </div>
                          )
                        })}
                      </div>
                    )}
                  </div>
                )}
              </div>
            )
          })}

          {filteredHosts.length === 0 && !loading && (
            <div style={{
              background: tokens.secondaryBackground,
              borderRadius: tokens.radius.lg,
              border: `0.5px solid ${tokens.separator}`,
              padding: 60,
              textAlign: 'center',
            }}>
              <div style={{ fontSize: 15, color: tokens.secondaryLabel }}>
                {searchTerm ? 'No hosts or VMs match your search' : 'No infrastructure hosts found'}
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
