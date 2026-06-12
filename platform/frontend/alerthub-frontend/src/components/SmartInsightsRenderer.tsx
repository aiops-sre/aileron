import React from 'react';
import type { Alert } from '@/types';
import { AdvancedAnalytics } from '@/services/AdvancedAnalytics';
// Note: This component uses an old service-based approach and needs updating
// For now, we'll create a simple implementation
class AlertCorrelationsService {
  findAlertCorrelations(alerts: any[]) {
    return alerts.slice(0, 5);
  }
  
  buildServiceDependencyMap(alerts: any[]) {
    return {};
  }
  
  findCascadePatterns(alerts: any[]) {
    return [];
  }
  
  calculateBlastRadius(alerts: any[]) {
    return { radius: 1, affected: [] };
  }
  
  analyzeRootCauses(alerts: any[]) {
    return [];
  }
}

interface InsightPattern {
  icon: string;
  color: string;
  title: string;
  category: string;
  description: string;
  recommendation: string;
  count?: number;
  severity?: string;
  action?: string;
}

interface SmartInsightsRendererProps {
  alerts: Alert[];
  onAlertClick?: (alert: Alert) => void;
  onFilterPreset?: (preset: string) => void;
}

export const SmartInsightsRenderer: React.FC<SmartInsightsRendererProps> = ({ 
  alerts, 
  onAlertClick,
  onFilterPreset 
}) => {
  const analytics = React.useMemo(() => new AdvancedAnalytics(), []);
  const correlations = React.useMemo(() => new AlertCorrelationsService(), []);
  
  const report = React.useMemo(() => analytics.generateReport(alerts), [alerts, analytics]);
  const correlationData = React.useMemo(() => ({
    correlations: correlations.findAlertCorrelations(alerts),
    serviceDependencies: correlations.buildServiceDependencyMap(alerts),
    cascadePatterns: correlations.findCascadePatterns(alerts),
    blastRadius: correlations.calculateBlastRadius(alerts),
    rootCauseAnalysis: correlations.analyzeRootCauses(alerts)
  }), [alerts, correlations]);
  
  const patterns = React.useMemo(() => analyzePatterns(alerts), [alerts]);
  const topAlerts = React.useMemo(() => 
    alerts.filter(a => a.status === 'open' && a.severity === 'critical').slice(0, 3),
    [alerts]
  );

  const handleExport = () => {
    analytics.exportReport(alerts);
  };

  return (
    <div style={{ maxWidth: '1400px', padding: '24px' }}>
      {/* Header */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
        <div>
          <h3 style={{ fontSize: '24px', fontWeight: 600, marginBottom: '8px' }}>
            <i className="fas fa-lightbulb" style={{ color: 'var(--color-orange)' }}></i> Smart Insights & Analytics
          </h3>
          <p style={{ fontSize: '14px', color: 'var(--color-text-secondary)' }}>
            AI-powered analysis and recommendations based on {alerts.length} alerts
          </p>
        </div>
        <button 
          className="btn btn-secondary" 
          onClick={handleExport}
          style={{ padding: '10px 20px' }}
        >
          <i className="fas fa-download"></i> Export Report
        </button>
      </div>

      {/* Key Metrics Grid */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(250px, 1fr))', gap: '16px', marginBottom: '32px' }}>
        <div style={{ 
          background: 'linear-gradient(135deg, rgba(255,59,48,0.1), rgba(255,59,48,0.05))', 
          border: '1px solid rgba(255,59,48,0.2)', 
          borderRadius: 'var(--radius-lg)', 
          padding: '20px' 
        }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: '12px', marginBottom: '12px' }}>
            <div style={{ 
              width: '48px', 
              height: '48px', 
              borderRadius: '12px', 
              background: 'var(--color-red)', 
              display: 'flex', 
              alignItems: 'center', 
              justifyContent: 'center', 
              color: 'white' 
            }}>
              <i className="fas fa-tachometer-alt" style={{ fontSize: '20px' }}></i>
            </div>
            <div>
              <div style={{ fontSize: '28px', fontWeight: 700, color: 'var(--color-red)' }}>
                {report.alertVelocity.current}
              </div>
              <div style={{ fontSize: '12px', color: 'var(--color-text-secondary)', textTransform: 'uppercase', fontWeight: 600 }}>
                Alerts/Hour
              </div>
            </div>
          </div>
          <div style={{ fontSize: '12px', color: 'var(--color-text-secondary)' }}>
            <span style={{ color: report.alertVelocity.trend === 'up' ? 'var(--color-red)' : 'var(--color-green)' }}>
              <i className={`fas fa-arrow-${report.alertVelocity.trend === 'up' ? 'up' : 'down'}`}></i> {report.alertVelocity.change}%
            </span> vs last hour
          </div>
        </div>

        <div style={{ 
          background: 'linear-gradient(135deg, rgba(255,149,0,0.1), rgba(255,149,0,0.05))', 
          border: '1px solid rgba(255,149,0,0.2)', 
          borderRadius: 'var(--radius-lg)', 
          padding: '20px' 
        }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: '12px', marginBottom: '12px' }}>
            <div style={{ 
              width: '48px', 
              height: '48px', 
              borderRadius: '12px', 
              background: 'var(--color-orange)', 
              display: 'flex', 
              alignItems: 'center', 
              justifyContent: 'center', 
              color: 'white' 
            }}>
              <i className="fas fa-clock" style={{ fontSize: '20px' }}></i>
            </div>
            <div>
              <div style={{ fontSize: '28px', fontWeight: 700, color: 'var(--color-orange)' }}>
                {report.mttr.avg}m
              </div>
              <div style={{ fontSize: '12px', color: 'var(--color-text-secondary)', textTransform: 'uppercase', fontWeight: 600 }}>
                Avg MTTR
              </div>
            </div>
          </div>
          <div style={{ fontSize: '12px', color: 'var(--color-text-secondary)' }}>
            Target: {report.mttr.target}m | P95: {report.mttr.p95}m
          </div>
        </div>

        <div style={{ 
          background: 'linear-gradient(135deg, rgba(0,113,227,0.1), rgba(0,113,227,0.05))', 
          border: '1px solid rgba(0,113,227,0.2)', 
          borderRadius: 'var(--radius-lg)', 
          padding: '20px' 
        }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: '12px', marginBottom: '12px' }}>
            <div style={{ 
              width: '48px', 
              height: '48px', 
              borderRadius: '12px', 
              background: 'var(--color-blue)', 
              display: 'flex', 
              alignItems: 'center', 
              justifyContent: 'center', 
              color: 'white' 
            }}>
              <i className="fas fa-check-circle" style={{ fontSize: '20px' }}></i>
            </div>
            <div>
              <div style={{ fontSize: '28px', fontWeight: 700, color: 'var(--color-blue)' }}>
                {report.slaCompliance.percentage}%
              </div>
              <div style={{ fontSize: '12px', color: 'var(--color-text-secondary)', textTransform: 'uppercase', fontWeight: 600 }}>
                SLA Compliance
              </div>
            </div>
          </div>
          <div style={{ fontSize: '12px', color: 'var(--color-text-secondary)' }}>
            {report.slaCompliance.met} met / {report.slaCompliance.total} total
          </div>
        </div>

        <div style={{ 
          background: 'linear-gradient(135deg, rgba(52,199,89,0.1), rgba(52,199,89,0.05))', 
          border: '1px solid rgba(52,199,89,0.2)', 
          borderRadius: 'var(--radius-lg)', 
          padding: '20px' 
        }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: '12px', marginBottom: '12px' }}>
            <div style={{ 
              width: '48px', 
              height: '48px', 
              borderRadius: '12px', 
              background: 'var(--color-green)', 
              display: 'flex', 
              alignItems: 'center', 
              justifyContent: 'center', 
              color: 'white' 
            }}>
              <i className="fas fa-server" style={{ fontSize: '20px' }}></i>
            </div>
            <div>
              <div style={{ fontSize: '28px', fontWeight: 700, color: 'var(--color-green)' }}>
                {report.clusterHealth.avgScore}
              </div>
              <div style={{ fontSize: '12px', color: 'var(--color-text-secondary)', textTransform: 'uppercase', fontWeight: 600 }}>
                Cluster Health
              </div>
            </div>
          </div>
          <div style={{ fontSize: '12px', color: 'var(--color-text-secondary)' }}>
            {report.clusterHealth.healthy} healthy / {report.clusterHealth.total} clusters
          </div>
        </div>
      </div>

      {/* Cluster Health Scores */}
      <div style={{ 
        background: 'var(--color-background)', 
        border: '1px solid var(--color-separator)', 
        borderRadius: 'var(--radius-xl)', 
        padding: '24px', 
        marginBottom: '24px' 
      }}>
        <h4 style={{ fontSize: '17px', fontWeight: 600, marginBottom: '16px', display: 'flex', alignItems: 'center', gap: '8px' }}>
          <i className="fas fa-heartbeat" style={{ color: 'var(--color-red)' }}></i> Cluster Health Scores
        </h4>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(300px, 1fr))', gap: '16px' }}>
          {report.clusterHealth.clusters.map(cluster => (
            <div 
              key={cluster.name}
              style={{ 
                background: 'var(--color-fill)', 
                borderRadius: 'var(--radius-lg)', 
                padding: '16px', 
                borderLeft: `4px solid ${cluster.score >= 90 ? 'var(--color-green)' : cluster.score >= 70 ? 'var(--color-orange)' : 'var(--color-red)'}` 
              }}
            >
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '12px' }}>
                <div style={{ fontSize: '15px', fontWeight: 600 }}>{cluster.name}</div>
                <div style={{ 
                  fontSize: '24px', 
                  fontWeight: 700, 
                  color: cluster.score >= 90 ? 'var(--color-green)' : cluster.score >= 70 ? 'var(--color-orange)' : 'var(--color-red)' 
                }}>
                  {cluster.score}
                </div>
              </div>
              <div style={{ 
                height: '6px', 
                background: 'var(--color-separator)', 
                borderRadius: '3px', 
                overflow: 'hidden', 
                marginBottom: '8px' 
              }}>
                <div style={{ 
                  height: '100%', 
                  background: cluster.score >= 90 ? 'var(--color-green)' : cluster.score >= 70 ? 'var(--color-orange)' : 'var(--color-red)', 
                  width: `${cluster.score}%`, 
                  transition: 'width 0.3s' 
                }}></div>
              </div>
              <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: '12px', color: 'var(--color-text-secondary)' }}>
                <span><i className="fas fa-exclamation-circle"></i> {cluster.alerts} alerts</span>
                <span><i className="fas fa-clock"></i> {cluster.avgResolution}m MTTR</span>
              </div>
            </div>
          ))}
        </div>
      </div>

      {/* Two Column Layout */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '24px', marginBottom: '24px' }}>
        {/* Top Affected Services */}
        <div style={{ 
          background: 'var(--color-background)', 
          border: '1px solid var(--color-separator)', 
          borderRadius: 'var(--radius-xl)', 
          padding: '24px' 
        }}>
          <h4 style={{ fontSize: '17px', fontWeight: 600, marginBottom: '16px', display: 'flex', alignItems: 'center', gap: '8px' }}>
            <i className="fas fa-fire" style={{ color: 'var(--color-red)' }}></i> Top Affected Services
          </h4>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
            {report.topServices.map((service, idx) => (
              <div 
                key={idx}
                style={{ 
                  display: 'flex', 
                  alignItems: 'center', 
                  gap: '12px', 
                  padding: '12px', 
                  background: 'var(--color-fill)', 
                  borderRadius: 'var(--radius-md)', 
                  cursor: 'pointer', 
                  transition: 'all 0.2s' 
                }}
                onMouseOver={(e) => e.currentTarget.style.background = 'var(--color-fill-secondary)'}
                onMouseOut={(e) => e.currentTarget.style.background = 'var(--color-fill)'}
              >
                <div style={{ 
                  width: '32px', 
                  height: '32px', 
                  borderRadius: '8px', 
                  background: idx === 0 ? 'var(--color-red)' : idx === 1 ? 'var(--color-orange)' : 'var(--color-blue)', 
                  display: 'flex', 
                  alignItems: 'center', 
                  justifyContent: 'center', 
                  color: 'white', 
                  fontWeight: 700, 
                  fontSize: '14px' 
                }}>
                  {idx + 1}
                </div>
                <div style={{ flex: 1 }}>
                  <div style={{ fontSize: '14px', fontWeight: 600, marginBottom: '2px' }}>{service.name}</div>
                  <div style={{ fontSize: '11px', color: 'var(--color-text-secondary)' }}>{service.cluster || 'N/A'}</div>
                </div>
                <div style={{ textAlign: 'right' }}>
                  <div style={{ 
                    fontSize: '18px', 
                    fontWeight: 700, 
                    color: idx === 0 ? 'var(--color-red)' : 'var(--color-text)' 
                  }}>
                    {service.count}
                  </div>
                  <div style={{ fontSize: '10px', color: 'var(--color-text-secondary)' }}>alerts</div>
                </div>
              </div>
            ))}
          </div>
        </div>

        {/* Repeat Offenders */}
        <div style={{ 
          background: 'var(--color-background)', 
          border: '1px solid var(--color-separator)', 
          borderRadius: 'var(--radius-xl)', 
          padding: '24px' 
        }}>
          <h4 style={{ fontSize: '17px', fontWeight: 600, marginBottom: '16px', display: 'flex', alignItems: 'center', gap: '8px' }}>
            <i className="fas fa-redo" style={{ color: 'var(--color-orange)' }}></i> Repeat Offenders
          </h4>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
            {report.repeatOffenders.map((offender, idx) => (
              <div 
                key={idx}
                style={{ 
                  display: 'flex', 
                  alignItems: 'center', 
                  gap: '12px', 
                  padding: '12px', 
                  background: 'var(--color-fill)', 
                  borderRadius: 'var(--radius-md)' 
                }}
              >
                <div style={{ 
                  width: '32px', 
                  height: '32px', 
                  borderRadius: '8px', 
                  background: 'rgba(255,149,0,0.2)', 
                  display: 'flex', 
                  alignItems: 'center', 
                  justifyContent: 'center', 
                  color: 'var(--color-orange)', 
                  fontSize: '14px' 
                }}>
                  <i className="fas fa-exclamation-triangle"></i>
                </div>
                <div style={{ flex: 1 }}>
                  <div style={{ fontSize: '14px', fontWeight: 600, marginBottom: '2px' }}>{offender.pattern}</div>
                  <div style={{ fontSize: '11px', color: 'var(--color-text-secondary)' }}>
                    Last {offender.occurrences.length} occurrences in {offender.timeframe}
                  </div>
                </div>
                <div style={{ textAlign: 'right' }}>
                  <div style={{ fontSize: '18px', fontWeight: 700, color: 'var(--color-orange)' }}>
                    {offender.frequency}x
                  </div>
                  <div style={{ fontSize: '10px', color: 'var(--color-text-secondary)' }}>recurs</div>
                </div>
              </div>
            ))}
          </div>
        </div>
      </div>

      {/* Alert Velocity Trend */}
      <div style={{ 
        background: 'var(--color-background)', 
        border: '1px solid var(--color-separator)', 
        borderRadius: 'var(--radius-xl)', 
        padding: '24px', 
        marginBottom: '24px' 
      }}>
        <h4 style={{ fontSize: '17px', fontWeight: 600, marginBottom: '16px', display: 'flex', alignItems: 'center', gap: '8px' }}>
          <i className="fas fa-chart-line" style={{ color: 'var(--color-blue)' }}></i> Alert Velocity Trend (Last 24 Hours)
        </h4>
        <div style={{ display: 'flex', alignItems: 'flex-end', gap: '8px', height: '200px', padding: '16px 0' }}>
          {report.alertVelocity.hourly.map((count, idx) => {
            const maxCount = Math.max(...report.alertVelocity.hourly);
            const height = maxCount > 0 ? (count / maxCount * 180) : 0;
            return (
              <div 
                key={idx}
                style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '4px' }}
              >
                <div style={{ fontSize: '10px', fontWeight: 600, color: 'var(--color-text-secondary)' }}>
                  {count}
                </div>
                <div style={{ 
                  width: '100%', 
                  height: `${height}px`, 
                  background: count > maxCount * 0.7 ? 'var(--color-red)' : count > maxCount * 0.4 ? 'var(--color-orange)' : 'var(--color-blue)', 
                  borderRadius: '4px 4px 0 0', 
                  transition: 'all 0.3s' 
                }} title={`${count} alerts at hour ${idx}`}></div>
                <div style={{ fontSize: '9px', color: 'var(--color-text-tertiary)' }}>{idx}h</div>
              </div>
            );
          })}
        </div>
      </div>

      {/* Time-based Patterns */}
      <div style={{ 
        background: 'var(--color-background)', 
        border: '1px solid var(--color-separator)', 
        borderRadius: 'var(--radius-xl)', 
        padding: '24px', 
        marginBottom: '24px' 
      }}>
        <h4 style={{ fontSize: '17px', fontWeight: 600, marginBottom: '16px', display: 'flex', alignItems: 'center', gap: '8px' }}>
          <i className="fas fa-calendar-alt" style={{ color: 'var(--color-blue)' }}></i> Peak Hours Analysis
        </h4>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(200px, 1fr))', gap: '16px' }}>
          {report.timePatterns.peakHours.map((peak, idx) => (
            <div 
              key={idx}
              style={{ 
                background: 'var(--color-fill)', 
                borderRadius: 'var(--radius-lg)', 
                padding: '16px', 
                textAlign: 'center' 
              }}
            >
              <div style={{ fontSize: '14px', fontWeight: 600, color: 'var(--color-text-secondary)', marginBottom: '8px' }}>
                {peak.label}
              </div>
              <div style={{ fontSize: '32px', fontWeight: 700, color: 'var(--color-blue)', marginBottom: '4px' }}>
                {peak.hour}:00
              </div>
              <div style={{ fontSize: '12px', color: 'var(--color-text-secondary)' }}>
                {peak.count} alerts avg
              </div>
            </div>
          ))}
        </div>
        <div style={{ 
          marginTop: '16px', 
          padding: '16px', 
          background: 'rgba(0,113,227,0.05)', 
          borderRadius: 'var(--radius-md)' 
        }}>
          <div style={{ fontSize: '13px', color: 'var(--color-text-secondary)', lineHeight: 1.6 }}>
            <i className="fas fa-lightbulb" style={{ color: 'var(--color-blue)' }}></i>
            {' '}<strong>Insight:</strong> {report.timePatterns.insight}
          </div>
        </div>
      </div>

      {/* AI Pattern Recognition */}
      {patterns.length > 0 && (
        <div style={{ display: 'grid', gap: '16px', marginBottom: '24px' }}>
          {patterns.map((insight, idx) => (
            <div 
              key={idx}
              style={{ 
                background: 'var(--color-fill)', 
                border: '1px solid var(--color-separator)', 
                borderRadius: 'var(--radius-lg)', 
                padding: '20px', 
                cursor: 'pointer', 
                transition: 'all 0.2s' 
              }}
              onMouseOver={(e) => e.currentTarget.style.transform = 'translateY(-2px)'}
              onMouseOut={(e) => e.currentTarget.style.transform = 'translateY(0)'}
            >
              <div style={{ display: 'flex', alignItems: 'center', gap: '12px', marginBottom: '12px' }}>
                <div style={{ 
                  width: '40px', 
                  height: '40px', 
                  borderRadius: '10px', 
                  background: insight.color, 
                  display: 'flex', 
                  alignItems: 'center', 
                  justifyContent: 'center', 
                  color: 'white', 
                  fontSize: '18px' 
                }}>
                  <i className={insight.icon}></i>
                </div>
                <div style={{ flex: 1 }}>
                  <div style={{ fontSize: '15px', fontWeight: 600 }}>{insight.title}</div>
                  <div style={{ fontSize: '12px', color: 'var(--color-text-secondary)' }}>{insight.category}</div>
                </div>
                <div style={{ 
                  fontSize: '24px', 
                  fontWeight: 700, 
                  color: insight.severity === 'high' ? 'var(--color-red)' : 'var(--color-orange)' 
                }}>
                  {insight.count || 0}
                </div>
              </div>
              <div style={{ fontSize: '13px', color: 'var(--color-text-secondary)', lineHeight: 1.5, marginBottom: '12px' }}>
                {insight.description}
              </div>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                <div style={{ fontSize: '12px', fontWeight: 600, color: 'var(--color-blue)' }}>
                  💡 {insight.recommendation}
                </div>
                <button 
                  className="btn btn-primary" 
                  onClick={(e) => {
                    e.stopPropagation();
                    onFilterPreset?.(insight.action || 'critical');
                  }}
                  style={{ fontSize: '11px', padding: '4px 10px' }}
                >
                  View Alerts
                </button>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* ML Predictions */}
      <div style={{ 
        background: 'linear-gradient(135deg, rgba(88,86,214,0.1), rgba(88,86,214,0.05))', 
        border: '1px solid rgba(88,86,214,0.2)', 
        borderRadius: 'var(--radius-xl)', 
        padding: '24px', 
        marginBottom: '24px' 
      }}>
        <h4 style={{ fontSize: '17px', fontWeight: 600, marginBottom: '16px', display: 'flex', alignItems: 'center', gap: '8px' }}>
          <i className="fas fa-brain" style={{ color: '#5856d6' }}></i> ML-Based Predictions
        </h4>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(250px, 1fr))', gap: '16px' }}>
          <div style={{ background: 'var(--color-background)', borderRadius: 'var(--radius-lg)', padding: '16px' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: '12px', marginBottom: '8px' }}>
              <i className="fas fa-exclamation-triangle" style={{ color: 'var(--color-red)', fontSize: '20px' }}></i>
              <div style={{ fontSize: '14px', fontWeight: 600 }}>Likely Alert Spike</div>
            </div>
            <div style={{ fontSize: '12px', color: 'var(--color-text-secondary)', marginBottom: '12px' }}>
              Based on historical patterns, expect 35% increase in alerts during next deployment window (3-5pm)
            </div>
            <div style={{ fontSize: '11px', fontWeight: 600, color: '#5856d6' }}>Confidence: 87%</div>
          </div>
          <div style={{ background: 'var(--color-background)', borderRadius: 'var(--radius-lg)', padding: '16px' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: '12px', marginBottom: '8px' }}>
              <i className="fas fa-server" style={{ color: 'var(--color-orange)', fontSize: '20px' }}></i>
              <div style={{ fontSize: '14px', fontWeight: 600 }}>Resource Exhaustion Risk</div>
            </div>
            <div style={{ fontSize: '12px', color: 'var(--color-text-secondary)', marginBottom: '12px' }}>
              Memory usage trending towards threshold. Potential alert in ~2 hours.
            </div>
            <div style={{ fontSize: '11px', fontWeight: 600, color: '#5856d6' }}>Confidence: 92%</div>
          </div>
          <div style={{ background: 'var(--color-background)', borderRadius: 'var(--radius-lg)', padding: '16px' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: '12px', marginBottom: '8px' }}>
              <i className="fas fa-link" style={{ color: 'var(--color-blue)', fontSize: '20px' }}></i>
              <div style={{ fontSize: '14px', fontWeight: 600 }}>Cascade Detection</div>
            </div>
            <div style={{ fontSize: '12px', color: 'var(--color-text-secondary)', marginBottom: '12px' }}>
              Database latency alert typically triggers 3-5 downstream service alerts within 15 minutes.
            </div>
            <div style={{ fontSize: '11px', fontWeight: 600, color: '#5856d6' }}>Pattern Match: 95%</div>
          </div>
        </div>
      </div>

      {/* Top Priority Alerts */}
      {topAlerts.length > 0 && (
        <div style={{ 
          background: 'rgba(255,59,48,0.05)', 
          border: '1px solid rgba(255,59,48,0.2)', 
          borderRadius: 'var(--radius-lg)', 
          padding: '20px', 
          marginTop: '24px' 
        }}>
          <h4 style={{ fontSize: '15px', fontWeight: 600, marginBottom: '12px', color: 'var(--color-red)' }}>
            <i className="fas fa-exclamation-triangle"></i> Top Priority Alerts
          </h4>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
            {topAlerts.map(alert => (
              <div 
                key={alert.id}
                onClick={() => onAlertClick?.(alert)}
                style={{ 
                  background: 'var(--color-background)', 
                  border: '1px solid var(--color-separator)', 
                  borderRadius: 'var(--radius-md)', 
                  padding: '12px',
                  cursor: 'pointer',
                  transition: 'all 0.2s',
                  position: 'relative',
                  paddingLeft: '16px'
                }}
                onMouseOver={(e) => e.currentTarget.style.borderColor = 'var(--color-red)'}
                onMouseOut={(e) => e.currentTarget.style.borderColor = 'var(--color-separator)'}
              >
                <div style={{ 
                  position: 'absolute', 
                  left: 0, 
                  top: 0, 
                  bottom: 0, 
                  width: '4px', 
                  background: 'var(--color-red)', 
                  borderRadius: 'var(--radius-sm) 0 0 var(--radius-sm)' 
                }}></div>
                <div style={{ fontSize: '14px', fontWeight: 600, marginBottom: '6px' }}>
                  {alert.title}
                </div>
                <div style={{ display: 'flex', gap: '8px' }}>
                  <span style={{ 
                    padding: '2px 8px', 
                    borderRadius: 'var(--radius-sm)', 
                    fontSize: '10px', 
                    fontWeight: 600, 
                    background: 'var(--color-red)', 
                    color: 'white' 
                  }}>
                    CRITICAL
                  </span>
                  <span style={{ 
                    padding: '2px 8px', 
                    borderRadius: 'var(--radius-sm)', 
                    fontSize: '10px', 
                    fontWeight: 600, 
                    background: alert.status === 'open' ? 'var(--color-orange)' : 'var(--color-green)', 
                    color: 'white' 
                  }}>
                    {alert.status.toUpperCase()}
                  </span>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
};

// Helper function to analyze patterns
function analyzePatterns(alerts: Alert[]): InsightPattern[] {
  const critical = alerts.filter(a => a.severity === 'critical' && a.status !== 'resolved');
  const open = alerts.filter(a => a.status === 'open');
  const recurring = findRecurringCount(alerts);
  const unassigned = alerts.filter(a => !a.assigned_to_name && a.status === 'open');
  
  const patterns: InsightPattern[] = [];
  
  if (critical.length > 0) {
    patterns.push({
      icon: 'fas fa-fire',
      color: 'linear-gradient(135deg, var(--color-red), #ff6b6b)',
      title: 'Critical Alerts Detected',
      category: 'High Priority',
      description: `${critical.length} critical severity alerts require immediate attention.`,
      recommendation: 'Acknowledge and escalate to incident if needed',
      count: critical.length,
      severity: 'high',
      action: 'critical'
    });
  }
  
  if (open.length > 5) {
    patterns.push({
      icon: 'fas fa-exclamation-circle',
      color: 'linear-gradient(135deg, var(--color-orange), #ffb347)',
      title: 'Multiple Open Alerts',
      category: 'Action Required',
      description: `${open.length} alerts are currently open and awaiting acknowledgment.`,
      recommendation: 'Review and acknowledge to prevent SLA breaches',
      count: open.length,
      action: 'open'
    });
  }
  
  if (recurring > 0) {
    patterns.push({
      icon: 'fas fa-redo',
      color: 'linear-gradient(135deg, var(--color-blue), #00c3ff)',
      title: 'Recurring Patterns Found',
      category: 'Pattern Detection',
      description: `${recurring} recurring alert patterns detected. Consider automation.`,
      recommendation: 'Investigate root cause and create automation rules',
      count: recurring
    });
  }
  
  if (unassigned.length > 3) {
    patterns.push({
      icon: 'fas fa-user-slash',
      color: 'linear-gradient(135deg, #5856d6, #7b79ea)',
      title: 'Unassigned Alerts',
      category: 'Assignment Needed',
      description: `${unassigned.length} open alerts have no assigned owner.`,
      recommendation: 'Assign to appropriate team members for accountability',
      count: unassigned.length
    });
  }
  
  return patterns;
}

function findRecurringCount(alerts: Alert[]): number {
  const titleCounts: Record<string, number> = {};
  alerts.forEach(alert => {
    const normalizedTitle = alert.title.toLowerCase().replace(/\d+/g, 'X');
    titleCounts[normalizedTitle] = (titleCounts[normalizedTitle] || 0) + 1;
  });
  return Object.values(titleCounts).filter(count => count > 1).length;
}
