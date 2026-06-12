import React, { useMemo } from 'react';
import { Link2, TrendingUp, Clock } from 'lucide-react';
import type { Alert } from '@/types';
import { formatTime } from '@/lib/utils';
import { MLCorrelationEngine } from '@/lib/ml-correlations';
import { extractMetadataFromAlert, isSameInfrastructure } from '@/lib/metadata-extractor';

interface SimilarAlertsViewerProps {
  alert: Alert;
  allAlerts: Alert[];
  onSelectAlert?: (alert: Alert) => void;
}

interface SimilarAlert extends Alert {
  similarity: number;
  similarityReasons: string[];
}

export function SimilarAlertsViewer({ 
  alert, 
  allAlerts, 
  onSelectAlert 
}: SimilarAlertsViewerProps) {
  const similarAlerts = useMemo(() => {
    if (!allAlerts?.length || allAlerts.length <= 1) return [];

    const engine = new MLCorrelationEngine()
    const currentMetadata = extractMetadataFromAlert(alert)
    
    const similar: SimilarAlert[] = allAlerts
      .filter(a => a.id !== alert.id)
      .map(a => {
        const alertMetadata = extractMetadataFromAlert(a)
        let similarity = 0
        const reasons: string[] = []

        // Infrastructure similarity (highest weight)
        if (isSameInfrastructure(currentMetadata, alertMetadata)) {
          similarity += 0.4
          reasons.push('Same infrastructure')
        } else {
          // Partial infrastructure matches
          if (currentMetadata.cluster && alertMetadata.cluster === currentMetadata.cluster) {
            similarity += 0.15
            reasons.push('Same cluster')
          }
          if (currentMetadata.namespace && alertMetadata.namespace === currentMetadata.namespace) {
            similarity += 0.15
            reasons.push('Same namespace')
          }
          if (currentMetadata.service && alertMetadata.service === currentMetadata.service) {
            similarity += 0.15
            reasons.push('Same service')
          }
          if (currentMetadata.host && alertMetadata.host === currentMetadata.host) {
            similarity += 0.15
            reasons.push('Same host')
          }
        }

        // Title similarity (text matching)
        const alertWords = alert.title.toLowerCase().split(/\s+/)
        const aWords = a.title.toLowerCase().split(/\s+/)
        const commonWords = alertWords.filter(w => w.length > 3 && aWords.includes(w))
        const titleSimilarity = commonWords.length / Math.max(alertWords.length, aWords.length)
        if (titleSimilarity > 0.3) {
          similarity += titleSimilarity * 0.25
          reasons.push('Similar title')
        }

        // Same severity
        if (a.severity === alert.severity) {
          similarity += 0.1
          reasons.push('Same severity')
        }

        // Same source
        if (a.source === alert.source) {
          similarity += 0.1
          reasons.push('Same source')
        }

        // Time proximity (recent alerts are more similar)
        const timeDiff = Math.abs(
          new Date(alert.created_at).getTime() - new Date(a.created_at).getTime()
        )
        const hoursDiff = timeDiff / (1000 * 60 * 60)
        if (hoursDiff <= 24) {
          const timeWeight = Math.max(0, (24 - hoursDiff) / 24 * 0.1)
          similarity += timeWeight
          if (hoursDiff <= 1) reasons.push('Very recent')
          else if (hoursDiff <= 6) reasons.push('Recent')
        }

        return {
          ...a,
          similarity: similarity,
          similarityReasons: reasons
        }
      })
      .filter(a => a.similarity > 0.2) // Only show reasonably similar alerts
      .sort((a, b) => b.similarity - a.similarity)
      .slice(0, 8) // Limit to top 8

    return similar
  }, [alert, allAlerts])

  if (similarAlerts.length === 0) {
    return (
      <div className="space-y-3">
        <h4 className="text-sm font-semibold text-muted-foreground uppercase tracking-wider flex items-center gap-2">
          <Link2 className="w-4 h-4" />
          Similar Alerts
        </h4>
        <div className="text-center py-8 px-4 bg-muted/30 rounded-lg border border-dashed border-muted-foreground/20">
          <Link2 className="w-8 h-8 mx-auto mb-3 text-muted-foreground/40" />
          <p className="text-sm text-muted-foreground">No similar alerts found</p>
          <p className="text-xs text-muted-foreground/70 mt-1">
            This alert appears to be unique or isolated
          </p>
        </div>
      </div>
    )
  }

  return (
    <div className="space-y-3">
      <h4 className="text-sm font-semibold text-muted-foreground uppercase tracking-wider flex items-center gap-2">
        <Link2 className="w-4 h-4" />
        Similar Alerts ({similarAlerts.length})
      </h4>
      
      <div className="space-y-2 max-h-64 overflow-y-auto">
        {similarAlerts.map((similarAlert) => (
          <div
            key={similarAlert.id}
            onClick={() => onSelectAlert?.(similarAlert)}
            className={`
              p-3 rounded-lg border cursor-pointer transition-all duration-200
              hover:shadow-md hover:border-primary/50 hover:bg-muted/50
              ${onSelectAlert ? 'cursor-pointer' : 'cursor-default'}
            `}
          >
            {/* Similarity Score Header */}
            <div className="flex items-center justify-between mb-2">
              <div className="flex items-center gap-2">
                <div 
                  className={`
                    px-2 py-1 rounded-md text-xs font-semibold
                    ${similarAlert.similarity > 0.7 
                      ? 'bg-green-100 text-green-700 border border-green-200' 
                      : similarAlert.similarity > 0.4
                      ? 'bg-yellow-100 text-yellow-700 border border-yellow-200'
                      : 'bg-blue-100 text-blue-700 border border-blue-200'
                    }
                  `}
                >
                  {Math.round(similarAlert.similarity * 100)}% match
                </div>
                <div className={`
                  px-2 py-1 rounded-md text-xs font-medium
                  ${similarAlert.severity === 'critical' 
                    ? 'bg-red-100 text-red-700 border border-red-200'
                    : similarAlert.severity === 'high'
                    ? 'bg-orange-100 text-orange-700 border border-orange-200' 
                    : similarAlert.severity === 'medium'
                    ? 'bg-yellow-100 text-yellow-700 border border-yellow-200'
                    : 'bg-gray-100 text-gray-700 border border-gray-200'
                  }
                `}>
                  {similarAlert.severity.toUpperCase()}
                </div>
              </div>
              <div className="flex items-center gap-1 text-xs text-muted-foreground">
                <Clock className="w-3 h-3" />
                {formatTime(similarAlert.created_at)}
              </div>
            </div>

            {/* Alert Title */}
            <h5 className="font-medium text-sm mb-2 text-foreground line-clamp-2">
              {similarAlert.title}
            </h5>

            {/* Similarity Reasons */}
            <div className="flex flex-wrap gap-1 mb-2">
              {similarAlert.similarityReasons.map((reason, idx) => (
                <span
                  key={idx}
                  className="inline-flex items-center px-2 py-0.5 rounded-full text-xs bg-primary/10 text-primary border border-primary/20"
                >
                  {reason}
                </span>
              ))}
            </div>

            {/* Source and Status */}
            <div className="flex justify-between items-center text-xs text-muted-foreground">
              <span>Source: {similarAlert.source || 'Unknown'}</span>
              <span className={`
                font-medium
                ${similarAlert.status === 'resolved' 
                  ? 'text-green-600' 
                  : similarAlert.status === 'acknowledged'
                  ? 'text-yellow-600'
                  : 'text-red-600'
                }
              `}>
                {similarAlert.status?.toUpperCase() || 'OPEN'}
              </span>
            </div>
          </div>
        ))}
      </div>

      {/* ML Insights */}
      {similarAlerts.length >= 3 && (
        <div className="mt-4 p-3 bg-blue-50 border border-blue-200 rounded-lg">
          <div className="flex items-center gap-2 mb-2">
            <TrendingUp className="w-4 h-4 text-blue-600" />
            <h6 className="text-sm font-semibold text-blue-800">Pattern Analysis</h6>
          </div>
          <p className="text-xs text-blue-700">
            {similarAlerts.length} similar alerts detected. This may indicate a recurring issue or 
            system-wide problem affecting{' '}
            {Array.from(new Set(similarAlerts.map(a => extractMetadataFromAlert(a).cluster))).filter(Boolean).length > 0
              ? `${Array.from(new Set(similarAlerts.map(a => extractMetadataFromAlert(a).cluster))).filter(Boolean).join(', ')}`
              : 'multiple components'
            }.
          </p>
        </div>
      )}
    </div>
  )
}