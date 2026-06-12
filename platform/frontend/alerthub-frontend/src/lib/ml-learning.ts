import type { Alert } from '@/types'
import { MLCorrelationEngine } from './ml-correlations'
import { extractMetadataFromAlert } from './metadata-extractor'

/**
 * Historical Alert Pattern Learning System
 * Stores and learns from historical alert data to improve predictions
 * Enhanced with metadata extraction for better pattern recognition
 */

export interface HistoricalPattern {
  id: string
  signature: string
  occurrences: number
  firstSeen: Date
  lastSeen: Date
  averageInterval: number // minutes
  severity: string
  metadata: {
    clusters: Set<string>
    namespaces: Set<string>
    services: Set<string>
  }
  outcomes: {
    resolved: number
    escalated: number
    ignored: number
  }
  relatedPatterns: string[]
}

export interface LearningMetrics {
  totalPatterns: number
  accuracyScore: number
  predictionsMade: number
  predictionsCorrect: number
  lastLearningDate: Date
  dataPoints: number
}

export interface PatternPrediction {
  patternId: string
  confidence: number
  expectedTime: Date
  recommendedAction: string
  basedOnHistory: boolean
}

export class MLLearningSystem {
  private patterns: Map<string, HistoricalPattern> = new Map()
  private alertHistory: Alert[] = []
  private predictionAccuracy: Map<string, { total: number; correct: number }> = new Map()
  private storageKey = 'alerthub_ml_patterns'
  private historyKey = 'alerthub_alert_history'
  private maxHistorySize = 10000 // Keep last 10k alerts

  constructor() {
    this.loadFromStorage()
  }

  /**
   * Load historical data from localStorage
   */
  private loadFromStorage(): void {
    try {
      // Load patterns
      const patternsData = localStorage.getItem(this.storageKey)
      if (patternsData) {
        const parsed = JSON.parse(patternsData)
        Object.entries(parsed).forEach(([key, value]: [string, any]) => {
          this.patterns.set(key, {
            ...value,
            firstSeen: new Date(value.firstSeen),
            lastSeen: new Date(value.lastSeen),
            metadata: {
              clusters: new Set(value.metadata.clusters),
              namespaces: new Set(value.metadata.namespaces),
              services: new Set(value.metadata.services),
            },
          })
        })
      }

      // Load alert history
      const historyData = localStorage.getItem(this.historyKey)
      if (historyData) {
        this.alertHistory = JSON.parse(historyData)
      }

      console.log(`Loaded ${this.patterns.size} patterns and ${this.alertHistory.length} historical alerts`)
    } catch (error) {
      console.error('Error loading ML learning data:', error)
    }
  }

  /**
   * Save to localStorage
   */
  private saveToStorage(): void {
    try {
      // Save patterns
      const patternsObj: Record<string, any> = {}
      this.patterns.forEach((pattern, key) => {
        patternsObj[key] = {
          ...pattern,
          metadata: {
            clusters: Array.from(pattern.metadata.clusters),
            namespaces: Array.from(pattern.metadata.namespaces),
            services: Array.from(pattern.metadata.services),
          },
        }
      })
      localStorage.setItem(this.storageKey, JSON.stringify(patternsObj))

      // Save alert history (keep only recent)
      const recentHistory = this.alertHistory.slice(-this.maxHistorySize)
      localStorage.setItem(this.historyKey, JSON.stringify(recentHistory))
    } catch (error) {
      console.error('Error saving ML learning data:', error)
    }
  }

  /**
   * Generate signature for an alert
   * Enhanced with metadata extraction for better pattern matching
   */
  private generateSignature(alert: Alert): string {
    // Use metadata extractor for more accurate signatures
    const enrichedMetadata = extractMetadataFromAlert(alert)
    
    const cluster = enrichedMetadata.cluster || 'unknown'
    const namespace = enrichedMetadata.namespace || 'unknown'
    const service = enrichedMetadata.service || enrichedMetadata.workload || 'unknown'
    const severity = alert.severity
    
    // Extract key words from title, excluding common alert terms
    const titleWords = alert.title
      .toLowerCase()
      .split(/\s+/)
      .filter(w => w.length > 3 && !['alert', 'warning', 'error', 'critical', 'high', 'medium', 'low'].includes(w))
      .slice(0, 3)
      .sort()
      .join(':')
    
    // Include more context for better pattern matching
    return `${cluster}:${namespace}:${service}:${severity}:${titleWords}`
  }

  /**
   * Learn from new alerts
   */
  learnFromAlerts(alerts: Alert[]): void {
    alerts.forEach(alert => {
      // Add to history
      this.alertHistory.push(alert)
      
      // Update patterns
      const signature = this.generateSignature(alert)
      const existing = this.patterns.get(signature)
      
      if (existing) {
        // Update existing pattern
        existing.occurrences++
        existing.lastSeen = new Date(alert.created_at)
        
        // Update average interval
        const timeSinceFirst = new Date(alert.created_at).getTime() - existing.firstSeen.getTime()
        existing.averageInterval = timeSinceFirst / (existing.occurrences * 60000) // minutes
        
        // Update metadata using enriched metadata
        const enrichedMetadata = extractMetadataFromAlert(alert)
        if (enrichedMetadata.cluster) existing.metadata.clusters.add(enrichedMetadata.cluster)
        if (enrichedMetadata.namespace) existing.metadata.namespaces.add(enrichedMetadata.namespace)
        const service = enrichedMetadata.service || enrichedMetadata.workload
        if (service) existing.metadata.services.add(service)
        
        // Track outcomes
        if (alert.status === 'resolved') existing.outcomes.resolved++
        if (alert.severity === 'critical') existing.outcomes.escalated++
      } else {
        // Create new pattern with enriched metadata
        const enrichedMetadata = extractMetadataFromAlert(alert)
        const newPattern: HistoricalPattern = {
          id: `pattern_${Date.now()}_${Math.random().toString(36).substr(2, 9)}`,
          signature,
          occurrences: 1,
          firstSeen: new Date(alert.created_at),
          lastSeen: new Date(alert.created_at),
          averageInterval: 0,
          severity: alert.severity,
          metadata: {
            clusters: new Set(enrichedMetadata.cluster ? [enrichedMetadata.cluster] : []),
            namespaces: new Set(enrichedMetadata.namespace ? [enrichedMetadata.namespace] : []),
            services: new Set([enrichedMetadata.service, enrichedMetadata.workload].filter((s): s is string => Boolean(s))),
          },
          outcomes: {
            resolved: alert.status === 'resolved' ? 1 : 0,
            escalated: alert.severity === 'critical' ? 1 : 0,
            ignored: 0,
          },
          relatedPatterns: [],
        }
        this.patterns.set(signature, newPattern)
      }
    })

    // Find related patterns
    this.updateRelatedPatterns()
    
    // Trim history if needed
    if (this.alertHistory.length > this.maxHistorySize) {
      this.alertHistory = this.alertHistory.slice(-this.maxHistorySize)
    }
    
    this.saveToStorage()
  }

  /**
   * Update related patterns based on co-occurrence
   */
  private updateRelatedPatterns(): void {
    const timeWindow = 30 * 60 * 1000 // 30 minutes in milliseconds
    const coOccurrence = new Map<string, Map<string, number>>()

    // Analyze co-occurrence in history
    this.alertHistory.forEach((alert, idx) => {
      const sig1 = this.generateSignature(alert)
      const alertTime = new Date(alert.created_at).getTime()

      // Look for alerts within time window
      for (let j = idx + 1; j < this.alertHistory.length; j++) {
        const other = this.alertHistory[j]
        const otherTime = new Date(other.created_at).getTime()
        
        if (otherTime - alertTime > timeWindow) break
        
        const sig2 = this.generateSignature(other)
        if (sig1 !== sig2) {
          if (!coOccurrence.has(sig1)) coOccurrence.set(sig1, new Map())
          const sig1Map = coOccurrence.get(sig1)!
          sig1Map.set(sig2, (sig1Map.get(sig2) || 0) + 1)
        }
      }
    })

    // Update patterns with top related patterns
    coOccurrence.forEach((related, signature) => {
      const pattern = this.patterns.get(signature)
      if (pattern) {
        pattern.relatedPatterns = Array.from(related.entries())
          .sort((a, b) => b[1] - a[1])
          .slice(0, 5)
          .map(([sig]) => sig)
      }
    })
  }

  /**
   * Get learning metrics
   */
  getMetrics(): LearningMetrics {
    let totalPredictions = 0
    let correctPredictions = 0

    this.predictionAccuracy.forEach(({ total, correct }) => {
      totalPredictions += total
      correctPredictions += correct
    })

    const accuracyScore = totalPredictions > 0 
      ? Math.round((correctPredictions / totalPredictions) * 100)
      : 0

    return {
      totalPatterns: this.patterns.size,
      accuracyScore,
      predictionsMade: totalPredictions,
      predictionsCorrect: correctPredictions,
      lastLearningDate: new Date(),
      dataPoints: this.alertHistory.length,
    }
  }

  /**
   * Predict future patterns based on historical data
   */
  predictPatterns(currentAlerts: Alert[]): PatternPrediction[] {
    const predictions: PatternPrediction[] = []
    const now = Date.now()

    // Check current alerts for known patterns
    currentAlerts.forEach(alert => {
      const signature = this.generateSignature(alert)
      const pattern = this.patterns.get(signature)

      if (pattern) {
        // Predict related patterns
        pattern.relatedPatterns.forEach(relatedSig => {
          const relatedPattern = this.patterns.get(relatedSig)
          if (relatedPattern) {
            // Estimate when this pattern might occur
            const avgInterval = pattern.averageInterval || 15
            const expectedTime = new Date(now + avgInterval * 60000)
            
            // Calculate confidence based on historical frequency
            const frequency = pattern.occurrences / Math.max(1, 
              (now - pattern.firstSeen.getTime()) / (24 * 60 * 60 * 1000) // days
            )
            const confidence = Math.min(95, 40 + frequency * 20)

            predictions.push({
              patternId: relatedPattern.id,
              confidence,
              expectedTime,
              recommendedAction: this.getRecommendedAction(relatedPattern),
              basedOnHistory: true,
            })
          }
        })
      }
    })

    // Check for periodic patterns
    this.patterns.forEach(pattern => {
      if (pattern.occurrences >= 3 && pattern.averageInterval > 0) {
        const timeSinceLastSeen = now - pattern.lastSeen.getTime()
        const intervalMs = pattern.averageInterval * 60000
        
        // If we're near the expected next occurrence
        if (timeSinceLastSeen >= intervalMs * 0.8 && timeSinceLastSeen <= intervalMs * 1.2) {
          predictions.push({
            patternId: pattern.id,
            confidence: Math.min(90, 50 + pattern.occurrences * 5),
            expectedTime: new Date(pattern.lastSeen.getTime() + intervalMs),
            recommendedAction: this.getRecommendedAction(pattern),
            basedOnHistory: true,
          })
        }
      }
    })

    return predictions
      .sort((a, b) => b.confidence - a.confidence)
      .slice(0, 10)
  }

  /**
   * Get recommended action for a pattern
   */
  private getRecommendedAction(pattern: HistoricalPattern): string {
    const { resolved, escalated, ignored } = pattern.outcomes
    const total = resolved + escalated + ignored

    if (total === 0) return 'Monitor and assess'
    
    const resolveRate = resolved / total
    const escalateRate = escalated / total

    if (resolveRate > 0.7) return 'Auto-resolve if conditions match'
    if (escalateRate > 0.5) return 'Immediate escalation recommended'
    if (pattern.severity === 'critical') return 'Alert on-call team'
    
    return 'Monitor and evaluate'
  }

  /**
   * Get pattern insights
   */
  getPatternInsights(): {
    topPatterns: HistoricalPattern[]
    criticalPatterns: HistoricalPattern[]
    periodicPatterns: HistoricalPattern[]
  } {
    const allPatterns = Array.from(this.patterns.values())

    return {
      topPatterns: allPatterns
        .sort((a, b) => b.occurrences - a.occurrences)
        .slice(0, 10),
      criticalPatterns: allPatterns
        .filter(p => p.severity === 'critical')
        .sort((a, b) => b.occurrences - a.occurrences)
        .slice(0, 5),
      periodicPatterns: allPatterns
        .filter(p => p.occurrences >= 3 && p.averageInterval > 0)
        .sort((a, b) => b.occurrences - a.occurrences)
        .slice(0, 5),
    }
  }

  /**
   * Get enhanced ML engine with historical data
   */
  getEnhancedEngine(): MLCorrelationEngine {
    return new MLCorrelationEngine(this.alertHistory)
  }

  /**
   * Record prediction accuracy
   */
  recordPrediction(patternId: string, wasCorrect: boolean): void {
    if (!this.predictionAccuracy.has(patternId)) {
      this.predictionAccuracy.set(patternId, { total: 0, correct: 0 })
    }
    
    const stats = this.predictionAccuracy.get(patternId)!
    stats.total++
    if (wasCorrect) stats.correct++
    
    this.saveToStorage()
  }

  /**
   * Clear all historical data
   */
  clearHistory(): void {
    this.patterns.clear()
    this.alertHistory = []
    this.predictionAccuracy.clear()
    localStorage.removeItem(this.storageKey)
    localStorage.removeItem(this.historyKey)
  }

  /**
   * Export data for analysis
   */
  exportData(): string {
    return JSON.stringify({
      patterns: Array.from(this.patterns.entries()),
      history: this.alertHistory,
      metrics: this.getMetrics(),
      insights: this.getPatternInsights(),
    }, null, 2)
  }

  /**
   * Get all patterns
   */
  getAllPatterns(): HistoricalPattern[] {
    return Array.from(this.patterns.values())
  }

  /**
   * Get alert history
   */
  getAlertHistory(): Alert[] {
    return this.alertHistory
  }
}

// Export singleton instance
export const mlLearningSystem = new MLLearningSystem()
