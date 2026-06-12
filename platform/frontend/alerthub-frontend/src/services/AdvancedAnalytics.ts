import type { Alert } from '@/types';

interface ClusterHealth {
  name: string;
  score: number;
  alerts: number;
  critical: number;
  avgResolution: number;
}

interface TopService {
  name: string;
  count: number;
  cluster: string;
}

interface AlertVelocity {
  current: number;
  previous: number;
  change: number;
  trend: string;
  hourly: number[];
}

interface MTTRStats {
  avg: number;
  target: number;
  p95: number;
}

interface RepeatOffender {
  pattern: string;
  occurrences: Date[];
  frequency: number;
  timeframe: string;
}

interface TimePatterns {
  peakHours: Array<{ label: string; hour: number; count: number }>;
  hourCounts: number[];
  insight: string;
}

interface SLACompliance {
  percentage: number;
  met: number;
  breached: number;
  total: number;
}

export interface AnalyticsReport {
  clusterHealth: {
    clusters: ClusterHealth[];
    avgScore: number;
    healthy: number;
    total: number;
  };
  topServices: TopService[];
  alertVelocity: AlertVelocity;
  mttr: MTTRStats;
  repeatOffenders: RepeatOffender[];
  timePatterns: TimePatterns;
  slaCompliance: SLACompliance;
  totalAlerts: number;
  generatedAt: string;
}

export class AdvancedAnalytics {
  private cache = new Map<string, { data: AnalyticsReport; timestamp: number }>();
  private cacheTimeout = 5 * 60 * 1000; // 5 minutes

  calculateClusterHealth(alerts: Alert[]) {
    const clusterMap = new Map<string, { 
      name: string; 
      alerts: Alert[]; 
      critical: number; 
      resolved: number 
    }>();
    
    alerts.forEach(alert => {
      const cluster = alert.labels?.cluster ||
                     alert.labels?.Cluster ||
                     (alert.description?.match(/k8s\.cluster\.name:\s*([a-zA-Z0-9_-]+)/i) || [])[1] ||
                     'unknown';
      
      if (!clusterMap.has(cluster)) {
        clusterMap.set(cluster, { name: cluster, alerts: [], critical: 0, resolved: 0 });
      }
      
      const clusterData = clusterMap.get(cluster)!;
      clusterData.alerts.push(alert);
      if (alert.severity === 'critical') clusterData.critical++;
      if (alert.status === 'resolved') clusterData.resolved++;
    });
    
    const clusters = Array.from(clusterMap.values()).map(cluster => {
      const totalAlerts = cluster.alerts.length;
      const criticalRatio = totalAlerts > 0 ? cluster.critical / totalAlerts : 0;
      const resolvedRatio = totalAlerts > 0 ? cluster.resolved / totalAlerts : 1;
      
      // Calculate MTTR
      const resolvedAlerts = cluster.alerts.filter(a => a.status === 'resolved' && a.created_at && a.resolved_at);
      let avgResolution = 0;
      if (resolvedAlerts.length > 0) {
        const totalTime = resolvedAlerts.reduce((sum, a) => {
          const created = new Date(a.created_at);
          const resolved = new Date(a.resolved_at!);
          return sum + (resolved.getTime() - created.getTime()) / 60000;
        }, 0);
        avgResolution = Math.round(totalTime / resolvedAlerts.length);
      }
      
      // Health score: penalize critical alerts, reward high resolution rate
      let score = 100;
      score -= criticalRatio * 40;
      score -= (1 - resolvedRatio) * 30;
      score -= Math.min(totalAlerts / 10, 20);
      score = Math.max(0, Math.round(score));
      
      return {
        name: cluster.name,
        score: score,
        alerts: totalAlerts,
        critical: cluster.critical,
        avgResolution: avgResolution || 0
      };
    }).filter(c => c.name !== 'unknown').sort((a, b) => b.score - a.score);
    
    const totalClusters = clusters.length;
    const healthyClusters = clusters.filter(c => c.score >= 90).length;
    const avgScore = totalClusters > 0 ? Math.round(clusters.reduce((sum, c) => sum + c.score, 0) / totalClusters) : 100;
    
    return {
      clusters: clusters.slice(0, 6),
      avgScore: avgScore,
      healthy: healthyClusters,
      total: totalClusters
    };
  }

  getTopAffectedServices(alerts: Alert[]): TopService[] {
    const serviceMap = new Map<string, TopService>();
    
    alerts.forEach(alert => {
      const service = alert.labels?.service ||
                     alert.labels?.Service ||
                     alert.labels?.workload ||
                     alert.labels?.ImpactedEntityNames ||
                     (alert.description?.match(/k8s\.workload\.name:\s*([a-zA-Z0-9_-]+)/i) || [])[1] ||
                     'unknown';
      
      const cluster = alert.labels?.cluster || alert.labels?.Cluster || 'N/A';
      
      if (service !== 'unknown') {
        if (!serviceMap.has(service)) {
          serviceMap.set(service, { name: service, count: 0, cluster: cluster });
        }
        serviceMap.get(service)!.count++;
      }
    });
    
    return Array.from(serviceMap.values())
      .sort((a, b) => b.count - a.count)
      .slice(0, 5);
  }

  calculateAlertVelocity(alerts: Alert[]): AlertVelocity {
    const now = new Date();
    const oneHourAgo = new Date(now.getTime() - 3600000);
    const twoHoursAgo = new Date(now.getTime() - 7200000);
    
    const lastHour = alerts.filter(a => new Date(a.created_at) > oneHourAgo).length;
    const previousHour = alerts.filter(a => {
      const created = new Date(a.created_at);
      return created > twoHoursAgo && created <= oneHourAgo;
    }).length;
    
    // Calculate hourly distribution for last 24 hours
    const hourly = new Array(24).fill(0);
    const oneDayAgo = new Date(now.getTime() - 86400000);
    
    alerts.forEach(alert => {
      const created = new Date(alert.created_at);
      if (created > oneDayAgo) {
        const hoursAgo = Math.floor((now.getTime() - created.getTime()) / 3600000);
        if (hoursAgo >= 0 && hoursAgo < 24) {
          hourly[23 - hoursAgo]++;
        }
      }
    });
    
    const change = previousHour > 0 ? Math.round(((lastHour - previousHour) / previousHour) * 100) : 0;
    const trend = change > 0 ? 'up' : change < 0 ? 'down' : 'stable';
    
    return {
      current: lastHour,
      previous: previousHour,
      change: Math.abs(change),
      trend: trend,
      hourly: hourly
    };
  }

  calculateMTTR(alerts: Alert[]): MTTRStats {
    const resolvedAlerts = alerts.filter(a =>
      a.status === 'resolved' &&
      a.created_at &&
      a.resolved_at
    );
    
    if (resolvedAlerts.length === 0) {
      return { avg: 0, target: 30, p95: 0 };
    }
    
    const resolutionTimes = resolvedAlerts.map(a => {
      const created = new Date(a.created_at);
      const resolved = new Date(a.resolved_at!);
      return (resolved.getTime() - created.getTime()) / 60000; // minutes
    }).sort((a, b) => a - b);
    
    const avg = Math.round(resolutionTimes.reduce((sum, t) => sum + t, 0) / resolutionTimes.length);
    const p95Index = Math.floor(resolutionTimes.length * 0.95);
    const p95 = Math.round(resolutionTimes[p95Index] || 0);
    
    return {
      avg: avg,
      target: 30,
      p95: p95
    };
  }

  findRepeatOffenders(alerts: Alert[]): RepeatOffender[] {
    const patternMap = new Map<string, { pattern: string; occurrences: Date[]; frequency: number }>();
    
    alerts.forEach(alert => {
      const normalized = alert.title
        .toLowerCase()
        .replace(/\d+\.\d+\.\d+\.\d+/g, 'IP')
        .replace(/\d{4}-\d{2}-\d{2}/g, 'DATE')
        .replace(/\d{2}:\d{2}:\d{2}/g, 'TIME')
        .replace(/\d+/g, 'N')
        .replace(/\s+/g, ' ')
        .trim();
      
      if (!patternMap.has(normalized)) {
        patternMap.set(normalized, {
          pattern: alert.title.replace(/\d+/g, 'X').substring(0, 50),
          occurrences: [],
          frequency: 0
        });
      }
      
      patternMap.get(normalized)!.occurrences.push(new Date(alert.created_at));
      patternMap.get(normalized)!.frequency++;
    });
    
    const repeats = Array.from(patternMap.values())
      .filter(p => p.frequency >= 3)
      .map(p => {
        p.occurrences.sort((a, b) => a.getTime() - b.getTime());
        const first = p.occurrences[0];
        const last = p.occurrences[p.occurrences.length - 1];
        const diffHours = Math.round((last.getTime() - first.getTime()) / 3600000);
        
        return {
          ...p,
          timeframe: diffHours < 24 ? `${diffHours}h` : `${Math.round(diffHours / 24)}d`
        };
      })
      .sort((a, b) => b.frequency - a.frequency)
      .slice(0, 5);
    
    return repeats;
  }

  analyzeTimePatterns(alerts: Alert[]): TimePatterns {
    const hourCounts = new Array(24).fill(0);
    
    alerts.forEach(alert => {
      const hour = new Date(alert.created_at).getHours();
      hourCounts[hour]++;
    });
    
    const hoursWithCounts = hourCounts.map((count, hour) => ({ hour, count }));
    const sortedHours = [...hoursWithCounts].sort((a, b) => b.count - a.count);
    
    const peakHours = [
      { label: 'Highest Peak', ...sortedHours[0] },
      { label: 'Second Peak', ...sortedHours[1] },
      { label: 'Third Peak', ...sortedHours[2] }
    ];
    
    const maxHour = sortedHours[0].hour;
    let insight = '';
    if (maxHour >= 9 && maxHour <= 17) {
      insight = 'Most alerts occur during business hours (9am-5pm), suggesting user activity correlation.';
    } else if (maxHour >= 0 && maxHour <= 5) {
      insight = 'Peak alerts during night hours may indicate batch job issues or automated processes.';
    } else {
      insight = 'Alert distribution shows activity peaks during deployment windows or high-traffic periods.';
    }
    
    return {
      peakHours: peakHours,
      hourCounts: hourCounts,
      insight: insight
    };
  }

  calculateSLACompliance(alerts: Alert[]): SLACompliance {
    const slaTracked = alerts.filter(a => a.created_at);
    
    let met = 0;
    let breached = 0;
    
    slaTracked.forEach(alert => {
      const created = new Date(alert.created_at);
      
      if (alert.acknowledged_at) {
        const ackTime = new Date(alert.acknowledged_at);
        const ackMinutes = (ackTime.getTime() - created.getTime()) / 60000;
        if (ackMinutes <= 5) met++;
        else breached++;
      } else if (alert.status === 'open') {
        const now = new Date();
        const minutes = (now.getTime() - created.getTime()) / 60000;
        if (minutes > 5) breached++;
      }
      
      if (alert.severity === 'critical' && alert.resolved_at) {
        const resolvedTime = new Date(alert.resolved_at);
        const resolutionMinutes = (resolvedTime.getTime() - created.getTime()) / 60000;
        if (resolutionMinutes <= 30) met++;
        else breached++;
      }
    });
    
    const total = met + breached;
    const percentage = total > 0 ? Math.round((met / total) * 100) : 100;
    
    return {
      percentage: percentage,
      met: met,
      breached: breached,
      total: total
    };
  }

  generateReport(alerts: Alert[]): AnalyticsReport {
    const cacheKey = `analytics_${alerts.length}_${Date.now()}`;
    
    if (this.cache.has(cacheKey)) {
      const cached = this.cache.get(cacheKey)!;
      if (Date.now() - cached.timestamp < this.cacheTimeout) {
        return cached.data;
      }
    }
    
    const report: AnalyticsReport = {
      clusterHealth: this.calculateClusterHealth(alerts),
      topServices: this.getTopAffectedServices(alerts),
      alertVelocity: this.calculateAlertVelocity(alerts),
      mttr: this.calculateMTTR(alerts),
      repeatOffenders: this.findRepeatOffenders(alerts),
      timePatterns: this.analyzeTimePatterns(alerts),
      slaCompliance: this.calculateSLACompliance(alerts),
      totalAlerts: alerts.length,
      generatedAt: new Date().toISOString()
    };
    
    this.cache.set(cacheKey, { data: report, timestamp: Date.now() });
    
    return report;
  }

  exportReport(alerts: Alert[]) {
    const report = this.generateReport(alerts);
    const json = JSON.stringify(report, null, 2);
    const blob = new Blob([json], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `sre-analytics-${new Date().toISOString().split('T')[0]}.json`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
    
    return report;
  }
}
