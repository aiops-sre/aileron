import type { Alert } from '@/types'

/**
 * Enhanced Metadata Extractor for Alert Infrastructure Information
 * Extracts cluster, namespace, workload, host, and other infrastructure details
 * from various alert sources and formats
 */

export interface ExtractedMetadata {
  // Core infrastructure
  cluster?: string
  namespace?: string
  workload?: string
  service?: string
  
  // Host/Node information
  host?: string
  hostIp?: string
  node?: string
  
  // Container/Pod information
  pod?: string
  container?: string
  
  // Network information
  ip?: string
  port?: string
  
  // Application information
  application?: string
  component?: string
  
  // Source-specific
  source?: string
  environment?: string
  region?: string
  zone?: string
}

/**
 * Extract metadata from alert using multiple strategies
 */
export function extractMetadataFromAlert(alert: Alert): ExtractedMetadata {
  const metadata: ExtractedMetadata = {}
  
  // Start with existing metadata
  if (alert.metadata) {
    Object.assign(metadata, alert.metadata)
  }
  
  // Extract from alert labels
  if (alert.labels) {
    if (alert.labels.cluster) metadata.cluster = alert.labels.cluster
    if (alert.labels.namespace) metadata.namespace = alert.labels.namespace
    if (alert.labels.service) metadata.service = alert.labels.service
    if (alert.labels.host) metadata.host = alert.labels.host
    if (alert.labels.pod) metadata.pod = alert.labels.pod
    if (alert.labels.container) metadata.container = alert.labels.container
    if (alert.labels.node) metadata.node = alert.labels.node
  }
  
  // Extract from title and description
  extractFromText(alert.title, metadata)
  if (alert.description) {
    extractFromText(alert.description, metadata)
  }
  
  // Source-specific extraction
  extractFromSource(alert, metadata)
  
  // Clean and normalize
  normalizeMetadata(metadata)
  
  return metadata
}

/**
 * Extract infrastructure info from text using patterns
 */
function extractFromText(text: string, metadata: ExtractedMetadata): void {
  // Kubernetes patterns
  const k8sPatterns = [
    // Namespace patterns
    /namespace[:\s=]+([a-z0-9-]+)/i,
    /ns[:\s=]+([a-z0-9-]+)/i,
    
    // Cluster patterns
    /cluster[:\s=]+([a-z0-9-]+)/i,
    /clusterName[:\s=]+([a-z0-9-]+)/i,
    
    // Service patterns
    /service[:\s=]+([a-z0-9-]+)/i,
    /svc[:\s=]+([a-z0-9-]+)/i,
    
    // Pod patterns
    /pod[:\s=]+([a-z0-9-]+)/i,
    /podName[:\s=]+([a-z0-9-]+)/i,
    
    // Node patterns
    /node[:\s=]+([a-z0-9.-]+)/i,
    /nodeName[:\s=]+([a-z0-9.-]+)/i,
    
    // Container patterns
    /container[:\s=]+([a-z0-9-]+)/i,
    /containerName[:\s=]+([a-z0-9-]+)/i,
  ]
  
  // Host/IP patterns
  const hostPatterns = [
    // Hostname patterns
    /host[:\s=]+([a-z0-9.-]+)/i,
    /hostname[:\s=]+([a-z0-9.-]+)/i,
    
    // IP patterns
    /ip[:\s=]+(\d+\.\d+\.\d+\.\d+)/i,
    /(\d+\.\d+\.\d+\.\d+)/,
  ]
  
  // Apply Kubernetes patterns
  k8sPatterns.forEach(pattern => {
    const match = text.match(pattern)
    if (match?.[1]) {
      const key = pattern.source.split('[')[0].toLowerCase()
      if (key.includes('namespace') || key.includes('ns')) {
        metadata.namespace = metadata.namespace || match[1]
      } else if (key.includes('cluster')) {
        metadata.cluster = metadata.cluster || match[1]
      } else if (key.includes('service') || key.includes('svc')) {
        metadata.service = metadata.service || match[1]
      } else if (key.includes('pod')) {
        metadata.pod = metadata.pod || match[1]
      } else if (key.includes('node')) {
        metadata.node = metadata.node || match[1]
      } else if (key.includes('container')) {
        metadata.container = metadata.container || match[1]
      }
    }
  })
  
  // Apply host patterns
  hostPatterns.forEach(pattern => {
    const match = text.match(pattern)
    if (match?.[1]) {
      if (pattern.source.includes('\\d+\\.')) {
        metadata.hostIp = metadata.hostIp || match[1]
      } else {
        metadata.host = metadata.host || match[1]
      }
    }
  })
}

/**
 * Source-specific extraction strategies
 */
function extractFromSource(alert: Alert, metadata: ExtractedMetadata): void {
  const source = alert.source?.toLowerCase()
  
  switch (source) {
    case 'dynatrace':
      extractDynatraceMetadata(alert, metadata)
      break
    case 'prometheus':
    case 'alertmanager':
      extractPrometheusMetadata(alert, metadata)
      break
    case 'grafana':
      extractGrafanaMetadata(alert, metadata)
      break
    case 'datadog':
      extractDatadogMetadata(alert, metadata)
      break
    case 'newrelic':
      extractNewRelicMetadata(alert, metadata)
      break
    default:
      // Generic extraction fallback
      extractGenericMetadata(alert, metadata)
  }
}

/**
 * Extract from Dynatrace alerts
 */
function extractDynatraceMetadata(alert: Alert, metadata: ExtractedMetadata): void {
  const text = `${alert.title} ${alert.description || ''}`
  
  // Dynatrace specific patterns
  const dynatracePatterns = [
    /Problem ID[:\s]+([A-Z0-9_-]+)/i,
    /Environment[:\s]+([a-z0-9-]+)/i,
    /Entity[:\s]+([A-Z0-9_-]+)/i,
  ]
  
  dynatracePatterns.forEach(pattern => {
    const match = text.match(pattern)
    if (match?.[1]) {
      const key = pattern.source.split('[')[0].toLowerCase()
      if (key.includes('environment')) {
        metadata.environment = metadata.environment || match[1]
      }
    }
  })
  
  // Extract from metadata if available
  if (alert.metadata) {
    if (alert.metadata.dt_entity_host) metadata.host = alert.metadata.dt_entity_host
    if (alert.metadata.dt_entity_process_group) metadata.service = alert.metadata.dt_entity_process_group
  }
}

/**
 * Extract from Prometheus/AlertManager alerts
 */
function extractPrometheusMetadata(alert: Alert, metadata: ExtractedMetadata): void {
  // Prometheus labels are typically in alert.labels
  if (alert.labels) {
    // Common Prometheus label mappings
    const labelMappings: Record<string, keyof ExtractedMetadata> = {
      'kubernetes_cluster': 'cluster',
      'kubernetes_namespace': 'namespace',
      'kubernetes_pod': 'pod',
      'kubernetes_container': 'container',
      'kubernetes_node': 'node',
      'job': 'service',
      'instance': 'host',
      'exported_instance': 'host',
    }
    
    Object.entries(labelMappings).forEach(([label, metaKey]) => {
      if (alert.labels[label] && !metadata[metaKey]) {
        metadata[metaKey] = alert.labels[label]
      }
    })
  }
}

/**
 * Extract from Grafana alerts
 */
function extractGrafanaMetadata(alert: Alert, metadata: ExtractedMetadata): void {
  // Grafana often includes datasource and query information
  if (alert.metadata) {
    if (alert.metadata.datasource) metadata.source = alert.metadata.datasource
    if (alert.metadata.folder) metadata.component = alert.metadata.folder
  }
}

/**
 * Extract from Datadog alerts
 */
function extractDatadogMetadata(alert: Alert, metadata: ExtractedMetadata): void {
  const text = `${alert.title} ${alert.description || ''}`
  
  // Datadog tag patterns
  const tagPattern = /@([a-zA-Z0-9_.-]+):([a-zA-Z0-9_.-]+)/g
  let match
  
  while ((match = tagPattern.exec(text)) !== null) {
    const [, key, value] = match
    switch (key.toLowerCase()) {
      case 'host':
        metadata.host = metadata.host || value
        break
      case 'service':
        metadata.service = metadata.service || value
        break
      case 'env':
      case 'environment':
        metadata.environment = metadata.environment || value
        break
      case 'cluster':
        metadata.cluster = metadata.cluster || value
        break
    }
  }
}

/**
 * Extract from New Relic alerts
 */
function extractNewRelicMetadata(alert: Alert, metadata: ExtractedMetadata): void {
  if (alert.metadata) {
    if (alert.metadata.hostname) metadata.host = alert.metadata.hostname
    if (alert.metadata.appName) metadata.service = alert.metadata.appName
    if (alert.metadata.environment) metadata.environment = alert.metadata.environment
  }
}

/**
 * Generic extraction for unknown sources
 */
function extractGenericMetadata(alert: Alert, metadata: ExtractedMetadata): void {
  // Try to extract common patterns
  const text = `${alert.title} ${alert.description || ''}`
  
  // Look for common infrastructure identifiers
  const patterns = [
    { pattern: /([a-z0-9-]+)\.(internal|local|com|net|org)/i, type: 'host' },
    { pattern: /\b([0-9]{1,3}\.){3}[0-9]{1,3}\b/, type: 'ip' },
    { pattern: /port[:\s]+(\d+)/i, type: 'port' },
  ]
  
  patterns.forEach(({ pattern, type }) => {
    const match = text.match(pattern)
    if (match) {
      switch (type) {
        case 'host':
          metadata.host = metadata.host || match[0]
          break
        case 'ip':
          metadata.hostIp = metadata.hostIp || match[0]
          break
        case 'port':
          metadata.port = metadata.port || match[1]
          break
      }
    }
  })
}

/**
 * Normalize and clean extracted metadata
 */
function normalizeMetadata(metadata: ExtractedMetadata): void {
  // Remove empty values
  Object.keys(metadata).forEach(key => {
    const value = metadata[key as keyof ExtractedMetadata]
    if (!value || value === 'unknown' || value === 'null' || value === '') {
      delete metadata[key as keyof ExtractedMetadata]
    }
  })
  
  // Normalize cluster names
  if (metadata.cluster) {
    metadata.cluster = metadata.cluster.toLowerCase().trim()
  }
  
  // Normalize namespace names
  if (metadata.namespace) {
    metadata.namespace = metadata.namespace.toLowerCase().trim()
  }
  
  // Normalize service names
  if (metadata.service) {
    metadata.service = metadata.service.toLowerCase().trim()
  }
  
  // Set workload as service if not present
  if (metadata.service && !metadata.workload) {
    metadata.workload = metadata.service
  }
}

/**
 * Get display-friendly metadata for UI
 */
export function getDisplayMetadata(metadata: ExtractedMetadata): Array<{
  key: string
  value: string
  label: string
}> {
  const display: Array<{ key: string; value: string; label: string }> = []
  
  const fieldMappings = [
    { key: 'cluster', label: 'Cluster' },
    { key: 'namespace', label: 'Namespace' },
    { key: 'service', label: 'Service' },
    { key: 'workload', label: 'Workload' },
    { key: 'host', label: 'Host' },
    { key: 'hostIp', label: 'Host IP' },
    { key: 'node', label: 'Node' },
    { key: 'pod', label: 'Pod' },
    { key: 'container', label: 'Container' },
    { key: 'environment', label: 'Environment' },
    { key: 'region', label: 'Region' },
    { key: 'zone', label: 'Zone' },
  ]
  
  fieldMappings.forEach(({ key, label }) => {
    const value = metadata[key as keyof ExtractedMetadata]
    if (value) {
      display.push({ key, value, label })
    }
  })
  
  return display
}

/**
 * Get infrastructure signature for similarity matching
 */
export function getInfrastructureSignature(metadata: ExtractedMetadata): string {
  const parts: string[] = []
  
  if (metadata.cluster) parts.push(`cluster:${metadata.cluster}`)
  if (metadata.namespace) parts.push(`namespace:${metadata.namespace}`)
  if (metadata.service || metadata.workload) {
    parts.push(`service:${metadata.service || metadata.workload}`)
  }
  if (metadata.host) parts.push(`host:${metadata.host}`)
  
  return parts.join('|') || 'unknown'
}

/**
 * Check if two metadata objects represent the same infrastructure
 */
export function isSameInfrastructure(meta1: ExtractedMetadata, meta2: ExtractedMetadata): boolean {
  const sig1 = getInfrastructureSignature(meta1)
  const sig2 = getInfrastructureSignature(meta2)
  
  if (sig1 === 'unknown' || sig2 === 'unknown') return false
  
  return sig1 === sig2
}