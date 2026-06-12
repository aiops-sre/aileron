import type { Alert } from '@/types'
import { extractMetadataFromAlert } from './metadata-extractor'

/**
 * Advanced ML-based Alert Correlation Engine
 * Implements statistical analysis, clustering, time-series patterns, and Bayesian inference
 * Enhanced with intelligent metadata extraction for improved accuracy
 */

export interface MLCorrelationInsight {
  type: 'anomaly' | 'cluster' | 'temporal' | 'causal' | 'predictive'
  title: string
  description: string
  confidence: number
  severity: 'critical' | 'high' | 'medium' | 'low'
  alerts: Alert[]
  metadata: Record<string, any>
}

export interface ClusterResult {
  clusterId: number
  alerts: Alert[]
  centroid: FeatureVector
  cohesion: number
  commonFeatures: string[]
}

export interface FeatureVector {
  severity: number
  temporal: number
  spatial: number
  frequency: number
  [key: string]: number
}

export interface TemporalPattern {
  pattern: 'burst' | 'gradual' | 'periodic' | 'cascade'
  startTime: Date
  duration: number // minutes
  alerts: Alert[]
  frequency: number
  confidence: number
}

export interface AnomalyScore {
  alert: Alert
  score: number
  reasons: string[]
  isAnomaly: boolean
}

export interface BayesianRootCause {
  cause: string
  probability: number
  evidence: string[]
  relatedAlerts: Alert[]
  confidence: number
}

export interface PredictiveInsight {
  prediction: string
  probability: number
  timeframe: string
  basedOn: string[]
  preventiveActions: string[]
}

/**
 * ML Correlation Engine Class
 */
export class MLCorrelationEngine {
  private historicalAlerts: Alert[] = []
  private featureCache: Map<string, FeatureVector> = new Map()
  private patternMemory: Map<string, number> = new Map()

  constructor(historicalData?: Alert[]) {
    if (historicalData) {
      this.historicalAlerts = historicalData
      this.buildPatternMemory()
    }
  }

  /**
   * Extract feature vector from alert for ML analysis
   * PRIMARY FOCUS: Cluster, Namespace, Workload, Host/IP info
   */
  private extractFeatures(alert: Alert): FeatureVector {
    const cached = this.featureCache.get(alert.id)
    if (cached) return cached

    // Extract and enrich metadata for better feature extraction
    const enrichedMetadata = extractMetadataFromAlert(alert)

    const features: FeatureVector = {
      // Severity encoding (lower weight)
      severity: this.encodeSeverity(alert.severity) * 0.5,
      
      // Temporal features (lower weight)
      temporal: new Date(alert.created_at).getTime(),
      hourOfDay: new Date(alert.created_at).getHours() * 0.3,
      dayOfWeek: new Date(alert.created_at).getDay() * 0.3,
      
      // PRIMARY: Infrastructure spatial features (HIGHEST WEIGHT)
      // Cluster encoding (weight: 5x)
      clusterHash: enrichedMetadata.cluster ? this.hashString(enrichedMetadata.cluster) * 5 : 0,
      // Namespace encoding (weight: 5x)
      namespaceHash: enrichedMetadata.namespace ? this.hashString(enrichedMetadata.namespace) * 5 : 0,
      // Workload encoding (weight: 5x)
      workloadHash: enrichedMetadata.workload || enrichedMetadata.service ?
        this.hashString(enrichedMetadata.workload || enrichedMetadata.service || '') * 5 : 0,
      // Host encoding (weight: 4x)
      hostHash: enrichedMetadata.host ? this.hashString(enrichedMetadata.host) * 4 : 0,
      hostIpHash: enrichedMetadata.hostIp ? this.hashString(enrichedMetadata.hostIp) * 4 : 0,
      
      // Combined spatial for overall grouping
      spatial: this.encodeSpatialFeatures(alert, enrichedMetadata),
      
      // Frequency features
      frequency: this.calculateAlertFrequency(alert),
      
      // Text features (lower weight for infrastructure focus)
      textComplexity: alert.title.split(' ').length * 0.2,
      
      // PRIMARY: Infrastructure presence flags (CRITICAL FOR CLUSTERING)
      hasCluster: enrichedMetadata.cluster ? 10 : 0,
      hasNamespace: enrichedMetadata.namespace ? 10 : 0,
      hasWorkload: (enrichedMetadata.service || enrichedMetadata.workload) ? 10 : 0,
      hasHost: enrichedMetadata.host ? 8 : 0,
      hasHostIp: enrichedMetadata.hostIp ? 8 : 0,
      hasNode: enrichedMetadata.node ? 5 : 0,
      hasPod: enrichedMetadata.pod ? 5 : 0,
    }

    this.featureCache.set(alert.id, features)
    return features
  }

  private hashString(str: string): number {
    let hash = 0
    for (let i = 0; i < str.length; i++) {
      hash = ((hash << 5) - hash) + str.charCodeAt(i)
      hash = hash & hash
    }
    return Math.abs(hash)
  }

  private encodeSeverity(severity: string): number {
    const map: Record<string, number> = {
      critical: 4,
      high: 3,
      medium: 2,
      low: 1,
      info: 0,
    }
    return map[severity] || 2
  }

  private encodeSpatialFeatures(alert: Alert, enrichedMetadata: any): number {
    // Use enhanced metadata for better spatial encoding
    const cluster = enrichedMetadata.cluster || alert.metadata?.cluster || ''
    const namespace = enrichedMetadata.namespace || alert.metadata?.namespace || ''
    const service = enrichedMetadata.service || enrichedMetadata.workload || alert.metadata?.service || alert.metadata?.workload || ''
    const pod = enrichedMetadata.pod || ''
    const node = enrichedMetadata.node || ''
    
    // Combine multiple spatial dimensions for more accurate encoding
    const combined = `${cluster}:${namespace}:${service}:${pod}:${node}`
    
    // Simple hash function
    let hash = 0
    for (let i = 0; i < combined.length; i++) {
      hash = ((hash << 5) - hash) + combined.charCodeAt(i)
      hash = hash & hash
    }
    return Math.abs(hash)
  }

  private calculateAlertFrequency(alert: Alert): number {
    const key = this.getAlertSignature(alert)
    return this.patternMemory.get(key) || 1
  }

  private getAlertSignature(alert: Alert): string {
    // Use metadata extractor for more accurate signature
    const enrichedMetadata = extractMetadataFromAlert(alert)
    
    const service = enrichedMetadata.service || enrichedMetadata.workload || alert.metadata?.service || alert.metadata?.workload || 'unknown'
    const cluster = enrichedMetadata.cluster || alert.metadata?.cluster || 'unknown'
    const namespace = enrichedMetadata.namespace || alert.metadata?.namespace || 'unknown'
    
    const titleWords = alert.title.toLowerCase()
      .split(/\s+/)
      .filter(w => w.length > 3 && !['alert', 'warning', 'error', 'critical'].includes(w))
      .slice(0, 3)
      .join(':')
    
    return `${cluster}:${namespace}:${service}:${titleWords}`
  }

  private buildPatternMemory(): void {
    this.patternMemory.clear()
    
    this.historicalAlerts.forEach(alert => {
      const signature = this.getAlertSignature(alert)
      this.patternMemory.set(signature, (this.patternMemory.get(signature) || 0) + 1)
    })
  }

  /**
   * Calculate Euclidean distance between feature vectors
   */
  private calculateDistance(v1: FeatureVector, v2: FeatureVector): number {
    const keys = new Set([...Object.keys(v1), ...Object.keys(v2)])
    let sum = 0
    
    keys.forEach(key => {
      const val1 = v1[key] || 0
      const val2 = v2[key] || 0
      sum += Math.pow(val1 - val2, 2)
    })
    
    return Math.sqrt(sum)
  }

  /**
   * K-means-style clustering for alert grouping
   */
  clusterAlerts(alerts: Alert[], k: number = 5): ClusterResult[] {
    if (alerts.length < k) k = Math.max(1, alerts.length)

    const features = alerts.map(a => this.extractFeatures(a))
    
    // Initialize centroids randomly
    let centroids: FeatureVector[] = []
    const shuffled = [...features].sort(() => Math.random() - 0.5)
    centroids = shuffled.slice(0, k)

    let assignments: number[] = new Array(alerts.length).fill(0)
    let iterations = 0
    const maxIterations = 10

    // K-means iteration
    while (iterations < maxIterations) {
      // Assign to nearest centroid
      const newAssignments = features.map(feat => {
        let minDist = Infinity
        let minIdx = 0
        
        centroids.forEach((centroid, idx) => {
          const dist = this.calculateDistance(feat, centroid)
          if (dist < minDist) {
            minDist = dist
            minIdx = idx
          }
        })
        
        return minIdx
      })

      // Check convergence
      if (JSON.stringify(newAssignments) === JSON.stringify(assignments)) break
      
      assignments = newAssignments

      // Update centroids
      centroids = centroids.map((_, idx) => {
        const clusterFeatures = features.filter((_, i) => assignments[i] === idx)
        if (clusterFeatures.length === 0) return centroids[idx]
        
        const newCentroid: FeatureVector = { severity: 0, temporal: 0, spatial: 0, frequency: 0 }
        const keys = new Set(clusterFeatures.flatMap(f => Object.keys(f)))
        
        keys.forEach(key => {
          const sum = clusterFeatures.reduce((s, f) => s + (f[key] || 0), 0)
          newCentroid[key] = sum / clusterFeatures.length
        })
        
        return newCentroid
      })

      iterations++
    }

    // Build cluster results
    const clusters: ClusterResult[] = centroids.map((centroid, idx) => {
      const clusterAlerts = alerts.filter((_, i) => assignments[i] === idx)
      
      // Calculate cohesion (average distance to centroid)
      const clusterFeatures = features.filter((_, i) => assignments[i] === idx)
      const cohesion = clusterFeatures.length > 0
        ? clusterFeatures.reduce((sum, f) => sum + this.calculateDistance(f, centroid), 0) / clusterFeatures.length
        : 0

      // Find common features
      const commonFeatures = this.findCommonFeatures(clusterAlerts)

      return {
        clusterId: idx,
        alerts: clusterAlerts,
        centroid,
        cohesion,
        commonFeatures,
      }
    })

    return clusters.filter(c => c.alerts.length > 0).sort((a, b) => b.alerts.length - a.alerts.length)
  }

  private findCommonFeatures(alerts: Alert[]): string[] {
    const features: string[] = []
    
    // Check for common cluster
    const clusters = alerts.map(a => a.metadata?.cluster).filter(Boolean)
    if (clusters.length > 0 && new Set(clusters).size === 1) {
      features.push(`cluster:${clusters[0]}`)
    }

    // Check for common namespace
    const namespaces = alerts.map(a => a.metadata?.namespace).filter(Boolean)
    if (namespaces.length > 0 && new Set(namespaces).size === 1) {
      features.push(`namespace:${namespaces[0]}`)
    }

    // Check for common service
    const services = alerts.map(a => a.metadata?.service || a.metadata?.workload).filter(Boolean)
    if (services.length > 0 && new Set(services).size === 1) {
      features.push(`service:${services[0]}`)
    }

    // Check for common severity
    const severities = alerts.map(a => a.severity)
    if (new Set(severities).size === 1) {
      features.push(`severity:${severities[0]}`)
    }

    return features
  }

  /**
   * Detect anomalies using statistical methods
   */
  detectAnomalies(alerts: Alert[]): AnomalyScore[] {
    const features = alerts.map(a => this.extractFeatures(a))
    
    // Calculate mean and std deviation for each feature
    const stats = this.calculateFeatureStats(features)
    
    return alerts.map((alert, idx) => {
      const feat = features[idx]
      const reasons: string[] = []
      let anomalyScore = 0

      // Check each feature for outliers (z-score > 2)
      Object.keys(stats).forEach(key => {
        const value = feat[key] || 0
        const zScore = Math.abs((value - stats[key].mean) / (stats[key].std || 1))
        
        if (zScore > 2) {
          anomalyScore += zScore
          if (key === 'severity') reasons.push('Unusual severity level')
          if (key === 'frequency') reasons.push('Rare occurrence pattern')
          if (key === 'temporal') reasons.push('Unusual timing')
        }
      })

      // Check for rare patterns
      const signature = this.getAlertSignature(alert)
      const frequency = this.patternMemory.get(signature) || 0
      if (frequency < 2) {
        anomalyScore += 2
        reasons.push('First-time or rare pattern')
      }

      return {
        alert,
        score: Math.min(100, anomalyScore * 10),
        reasons,
        isAnomaly: anomalyScore > 3,
      }
    }).sort((a, b) => b.score - a.score)
  }

  private calculateFeatureStats(features: FeatureVector[]): Record<string, { mean: number; std: number }> {
    const stats: Record<string, { mean: number; std: number }> = {}
    
    if (features.length === 0) return stats

    const keys = new Set(features.flatMap(f => Object.keys(f)))
    
    keys.forEach(key => {
      const values = features.map(f => f[key] || 0)
      const mean = values.reduce((s, v) => s + v, 0) / values.length
      const variance = values.reduce((s, v) => s + Math.pow(v - mean, 2), 0) / values.length
      const std = Math.sqrt(variance)
      
      stats[key] = { mean, std }
    })

    return stats
  }

  /**
   * Bayesian root cause analysis
   */
  bayesianRootCauseAnalysis(alerts: Alert[]): BayesianRootCause[] {
    const possibleCauses = [
      'Infrastructure Failure',
      'Resource Exhaustion', 
      'Network Issues',
      'Application Error',
      'Configuration Issue',
      'Database Problems',
      'External Dependency Failure',
      'Security Incident',
    ]

    const causeAnalysis = possibleCauses.map(cause => {
      const { prior, likelihood, evidence, relatedAlerts } = this.calculateBayesianProbability(
        cause,
        alerts
      )

      // Bayes theorem: P(cause|evidence) = P(evidence|cause) * P(cause) / P(evidence)
      // Simplified: posterior ∝ likelihood * prior
      const posterior = likelihood * prior

      return {
        cause,
        probability: Math.min(95, posterior * 100),
        evidence,
        relatedAlerts,
        confidence: this.calculateConfidence(evidence, relatedAlerts.length),
      }
    })

    return causeAnalysis
      .filter(c => c.probability > 5)
      .sort((a, b) => b.probability - a.probability)
      .slice(0, 5)
  }

  private calculateBayesianProbability(
    cause: string,
    alerts: Alert[]
  ): { prior: number; likelihood: number; evidence: string[]; relatedAlerts: Alert[] } {
    const evidence: string[] = []
    const relatedAlerts: Alert[] = []
    let evidenceCount = 0

    const keywords: Record<string, string[]> = {
      'Infrastructure Failure': ['node', 'host', 'pod', 'container', 'vm', 'instance', 'down', 'unreachable'],
      'Resource Exhaustion': ['memory', 'cpu', 'disk', 'quota', 'limit', 'throttle', 'oom'],
      'Network Issues': ['network', 'connection', 'timeout', 'latency', 'dns', 'tcp', 'unreachable'],
      'Application Error': ['error', 'exception', 'crash', 'failed', 'panic', 'segfault'],
      'Configuration Issue': ['config', 'permission', 'auth', 'credentials', 'invalid', 'misconfigured'],
      'Database Problems': ['database', 'db', 'query', 'sql', 'connection pool', 'deadlock'],
      'External Dependency Failure': ['api', 'external', 'third-party', 'upstream', 'dependency'],
      'Security Incident': ['security', 'breach', 'unauthorized', 'attack', 'malicious', 'vulnerability'],
    }

    const causeKeywords = keywords[cause] || []

    alerts.forEach(alert => {
      const title = alert.title.toLowerCase()
      const description = alert.description?.toLowerCase() || ''
      const text = `${title} ${description}`

      causeKeywords.forEach(keyword => {
        if (text.includes(keyword)) {
          evidenceCount++
          if (!evidence.includes(keyword)) {
            evidence.push(keyword)
          }
          if (!relatedAlerts.includes(alert)) {
            relatedAlerts.push(alert)
          }
        }
      })
    })

    // Prior probability (based on historical data or uniform)
    const prior = 1 / Object.keys(keywords).length

    // Likelihood: P(evidence|cause)
    const likelihood = evidenceCount / Math.max(1, alerts.length * causeKeywords.length)

    return { prior, likelihood, evidence, relatedAlerts }
  }

  private calculateConfidence(evidence: string[], alertCount: number): number {
    // Confidence increases with more evidence and more related alerts
    const evidenceScore = Math.min(50, evidence.length * 10)
    const alertScore = Math.min(45, alertCount * 5)
    return evidenceScore + alertScore
  }

  /**
   * Learn from new alerts
   */
  learn(newAlerts: Alert[]): void {
    this.historicalAlerts.push(...newAlerts)
    this.buildPatternMemory()
    
    // Clear feature cache for new analysis
    newAlerts.forEach(alert => {
      this.featureCache.delete(alert.id)
    })
  }
}

// Export singleton instance
export const mlEngine = new MLCorrelationEngine()
