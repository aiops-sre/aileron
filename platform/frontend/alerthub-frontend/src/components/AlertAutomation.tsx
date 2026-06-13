import React, { useState, useMemo } from 'react';
import { Bot, Play, Pause, Settings, Clock, CheckCircle, AlertTriangle, Zap } from 'lucide-react';
import type { Alert } from '@/types';
import { mlLearningSystem } from '@/lib/ml-learning';
import { extractMetadataFromAlert } from '@/lib/metadata-extractor';
import toast from 'react-hot-toast';

interface AutomationRule {
  id: string;
  name: string;
  enabled: boolean;
  conditions: {
    severity?: string[];
    source?: string[];
    keywords?: string[];
    infrastructure?: {
      cluster?: string;
      namespace?: string;
      service?: string;
    };
  };
  actions: {
    type: 'acknowledge' | 'resolve' | 'assign' | 'escalate' | 'create_ticket';
    parameters?: Record<string, any>;
  }[];
  stats: {
    triggered: number;
    successful: number;
    failed: number;
    lastTriggered?: Date;
  };
}

interface AlertAutomationProps {
  alerts: Alert[];
  onExecuteAction: (alertId: string, action: string, parameters?: any) => Promise<boolean>;
}

export function AlertAutomation({ alerts, onExecuteAction }: AlertAutomationProps) {
  const [automationRules, setAutomationRules] = useState<AutomationRule[]>(() => {
    // Load from localStorage or use defaults
    const saved = localStorage.getItem('alert_automation_rules');
    if (saved) {
      try {
        return JSON.parse(saved);
      } catch (e) {
        console.error('Failed to parse automation rules:', e);
      }
    }
    
    return [
      {
        id: 'auto-ack-info',
        name: 'Auto-acknowledge info alerts',
        enabled: true,
        conditions: {
          severity: ['info', 'low'],
          keywords: ['test', 'heartbeat', 'check']
        },
        actions: [{ type: 'acknowledge' }],
        stats: { triggered: 0, successful: 0, failed: 0 }
      },
      {
        id: 'auto-resolve-resolved',
        name: 'Auto-resolve duplicate resolved alerts',
        enabled: true,
        conditions: {
          keywords: ['resolved', 'recovered', 'ok']
        },
        actions: [{ type: 'resolve' }],
        stats: { triggered: 0, successful: 0, failed: 0 }
      },
      {
        id: 'escalate-critical',
        name: 'Escalate critical alerts after 15 minutes',
        enabled: false,
        conditions: {
          severity: ['critical']
        },
        actions: [{ 
          type: 'escalate',
          parameters: { delay: 900000, team: 'oncall' } // 15 minutes
        }],
        stats: { triggered: 0, successful: 0, failed: 0 }
      }
    ];
  });

  const [isConfiguring, setIsConfiguring] = useState(false);

  // Save rules to localStorage whenever they change
  const saveRules = (rules: AutomationRule[]) => {
    setAutomationRules(rules);
    localStorage.setItem('alert_automation_rules', JSON.stringify(rules));
  };

  // Check if alert matches rule conditions
  const matchesConditions = (alert: Alert, conditions: AutomationRule['conditions']): boolean => {
    const metadata = extractMetadataFromAlert(alert);
    
    // Check severity
    if (conditions.severity && !conditions.severity.includes(alert.severity)) {
      return false;
    }
    
    // Check source
    if (conditions.source && alert.source && !conditions.source.includes(alert.source)) {
      return false;
    }
    
    // Check keywords
    if (conditions.keywords) {
      const text = `${alert.title} ${alert.description || ''}`.toLowerCase();
      const hasKeyword = conditions.keywords.some(keyword => 
        text.includes(keyword.toLowerCase())
      );
      if (!hasKeyword) return false;
    }
    
    // Check infrastructure
    if (conditions.infrastructure) {
      const { cluster, namespace, service } = conditions.infrastructure;
      if (cluster && metadata.cluster !== cluster) return false;
      if (namespace && metadata.namespace !== namespace) return false;
      if (service && metadata.service !== service) return false;
    }
    
    return true;
  };

  // Get alerts that would be automated
  const automationCandidates = useMemo(() => {
    const candidates: { alert: Alert; rules: AutomationRule[] }[] = [];
    
    alerts.forEach(alert => {
      const matchingRules = automationRules.filter(rule => 
        rule.enabled && matchesConditions(alert, rule.conditions)
      );
      
      if (matchingRules.length > 0) {
        candidates.push({ alert, rules: matchingRules });
      }
    });
    
    return candidates;
  }, [alerts, automationRules]);

  // Execute automation for an alert
  const executeAutomation = async (alert: Alert, rules: AutomationRule[]) => {
    for (const rule of rules) {
      for (const action of rule.actions) {
        try {
          const success = await onExecuteAction(alert.id, action.type, action.parameters);
          
          // Update rule stats
          const updatedRules = automationRules.map(r => {
            if (r.id === rule.id) {
              return {
                ...r,
                stats: {
                  ...r.stats,
                  triggered: r.stats.triggered + 1,
                  successful: success ? r.stats.successful + 1 : r.stats.successful,
                  failed: success ? r.stats.failed : r.stats.failed + 1,
                  lastTriggered: new Date()
                }
              };
            }
            return r;
          });
          
          saveRules(updatedRules);
          
          if (success) {
            toast.success(`Automated ${action.type} for alert: ${alert.title.substring(0, 50)}...`);
          } else {
            toast.error(`Failed to execute ${action.type} for alert: ${alert.title.substring(0, 50)}...`);
          }
        } catch (error) {
          console.error('Automation execution failed:', error);
          toast.error(`Automation failed: ${error}`);
        }
      }
    }
  };

  // Toggle rule enabled/disabled
  const toggleRule = (ruleId: string) => {
    const updatedRules = automationRules.map(rule =>
      rule.id === ruleId ? { ...rule, enabled: !rule.enabled } : rule
    );
    saveRules(updatedRules);
  };

  // ML-based automation suggestions
  const automationSuggestions = useMemo(() => {
    const patterns = mlLearningSystem.getAllPatterns();
    const suggestions: Partial<AutomationRule>[] = [];
    
    // Suggest auto-resolution for frequently resolved patterns
    patterns.forEach(pattern => {
      const resolveRate = pattern.outcomes.resolved / 
        Math.max(1, pattern.outcomes.resolved + pattern.outcomes.escalated + pattern.outcomes.ignored);
      
      if (resolveRate > 0.8 && pattern.occurrences >= 5) {
        suggestions.push({
          name: `Auto-resolve: ${pattern.signature.split(':').slice(-1)[0] || 'Pattern'}`,
          conditions: {
            keywords: [pattern.signature.split(':').slice(-1)[0] || '']
          },
          actions: [{ type: 'resolve' }],
        });
      }
    });
    
    return suggestions.slice(0, 3);
  }, []);

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <div className="p-2 bg-purple-100 rounded-lg">
            <Bot className="w-5 h-5 text-purple-600" />
          </div>
          <div>
            <h3 className="text-lg font-semibold">Alert Automation</h3>
            <p className="text-sm text-muted-foreground">
              Automate responses to common alert patterns
            </p>
          </div>
        </div>
        <button
          onClick={() => setIsConfiguring(!isConfiguring)}
          className="btn-aileron bg-purple-500/10 text-purple-600 border border-purple-500/20 hover:bg-purple-500/20"
        >
          <Settings className="w-4 h-4 mr-2" />
          Configure
        </button>
      </div>

      {/* Statistics */}
      <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
        <div className="bg-blue-50 p-4 rounded-lg border border-blue-200">
          <div className="flex items-center gap-2 mb-2">
            <Zap className="w-4 h-4 text-blue-600" />
            <span className="text-sm font-semibold text-blue-800">Active Rules</span>
          </div>
          <div className="text-2xl font-bold text-blue-600">
            {automationRules.filter(r => r.enabled).length}
          </div>
        </div>
        
        <div className="bg-green-50 p-4 rounded-lg border border-green-200">
          <div className="flex items-center gap-2 mb-2">
            <CheckCircle className="w-4 h-4 text-green-600" />
            <span className="text-sm font-semibold text-green-800">Candidates</span>
          </div>
          <div className="text-2xl font-bold text-green-600">
            {automationCandidates.length}
          </div>
        </div>
        
        <div className="bg-orange-50 p-4 rounded-lg border border-orange-200">
          <div className="flex items-center gap-2 mb-2">
            <Clock className="w-4 h-4 text-orange-600" />
            <span className="text-sm font-semibold text-orange-800">Triggered</span>
          </div>
          <div className="text-2xl font-bold text-orange-600">
            {automationRules.reduce((sum, r) => sum + r.stats.triggered, 0)}
          </div>
        </div>
        
        <div className="bg-red-50 p-4 rounded-lg border border-red-200">
          <div className="flex items-center gap-2 mb-2">
            <AlertTriangle className="w-4 h-4 text-red-600" />
            <span className="text-sm font-semibold text-red-800">Success Rate</span>
          </div>
          <div className="text-2xl font-bold text-red-600">
            {automationRules.reduce((sum, r) => sum + r.stats.triggered, 0) > 0 
              ? Math.round(
                  (automationRules.reduce((sum, r) => sum + r.stats.successful, 0) / 
                   automationRules.reduce((sum, r) => sum + r.stats.triggered, 0)) * 100
                )
              : 0}%
          </div>
        </div>
      </div>

      {/* Automation Candidates */}
      {automationCandidates.length > 0 && (
        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <h4 className="text-md font-semibold">Ready for Automation</h4>
            <button
              onClick={async () => {
                for (const candidate of automationCandidates) {
                  await executeAutomation(candidate.alert, candidate.rules);
                }
              }}
              className="btn-aileron bg-green-500/10 text-green-600 border border-green-500/20 hover:bg-green-500/20"
            >
              <Play className="w-4 h-4 mr-2" />
              Execute All ({automationCandidates.length})
            </button>
          </div>
          
          <div className="space-y-2 max-h-64 overflow-y-auto">
            {automationCandidates.map(({ alert, rules }) => (
              <div
                key={alert.id}
                className="p-3 bg-muted/30 rounded-lg border border-muted-foreground/20"
              >
                <div className="flex items-start justify-between mb-2">
                  <div className="flex-1">
                    <h5 className="font-medium text-sm line-clamp-1">{alert.title}</h5>
                    <div className="flex items-center gap-2 mt-1">
                      <span className={`
                        px-1.5 py-0.5 rounded text-xs font-medium
                        ${alert.severity === 'critical' ? 'bg-red-100 text-red-700'
                          : alert.severity === 'high' ? 'bg-orange-100 text-orange-700'
                          : alert.severity === 'medium' ? 'bg-yellow-100 text-yellow-700'
                          : 'bg-gray-100 text-gray-700'
                        }
                      `}>
                        {alert.severity.toUpperCase()}
                      </span>
                      <span className="text-xs text-muted-foreground">
                        {alert.source || 'Unknown'}
                      </span>
                    </div>
                  </div>
                  <button
                    onClick={() => executeAutomation(alert, rules)}
                    className="btn-aileron bg-green-500/10 text-green-600 border border-green-500/20 hover:bg-green-500/20 px-3 py-1 text-xs"
                  >
                    Execute
                  </button>
                </div>
                
                <div className="flex flex-wrap gap-1">
                  {rules.map(rule => (
                    <span
                      key={rule.id}
                      className="px-2 py-1 bg-primary/10 text-primary rounded text-xs"
                    >
                      {rule.name}
                    </span>
                  ))}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Automation Rules */}
      <div className="space-y-3">
        <h4 className="text-md font-semibold">Automation Rules</h4>
        
        <div className="space-y-2">
          {automationRules.map((rule) => (
            <div
              key={rule.id}
              className={`
                p-4 rounded-lg border transition-all
                ${rule.enabled 
                  ? 'border-green-200 bg-green-50/50' 
                  : 'border-muted-foreground/20 bg-muted/30'
                }
              `}
            >
              <div className="flex items-start justify-between mb-3">
                <div className="flex items-center gap-3">
                  <button
                    onClick={() => toggleRule(rule.id)}
                    className={`
                      p-2 rounded-lg transition-colors
                      ${rule.enabled 
                        ? 'bg-green-100 text-green-600 hover:bg-green-200' 
                        : 'bg-muted text-muted-foreground hover:bg-muted/80'
                      }
                    `}
                  >
                    {rule.enabled ? <Play className="w-4 h-4" /> : <Pause className="w-4 h-4" />}
                  </button>
                  <div>
                    <h5 className="font-semibold text-sm">{rule.name}</h5>
                    <div className="text-xs text-muted-foreground mt-1">
                      {rule.actions.map(action => action.type).join(', ')}
                    </div>
                  </div>
                </div>
                
                <div className="text-right text-xs text-muted-foreground">
                  <div>Triggered: {rule.stats.triggered}</div>
                  <div>Success: {rule.stats.successful}/{rule.stats.triggered}</div>
                  {rule.stats.lastTriggered && (
                    <div>Last: {rule.stats.lastTriggered.toLocaleTimeString()}</div>
                  )}
                </div>
              </div>

              {/* Rule Conditions */}
              <div className="space-y-2">
                {rule.conditions.severity && (
                  <div className="flex items-center gap-2">
                    <span className="text-xs font-medium text-muted-foreground">Severity:</span>
                    <div className="flex gap-1">
                      {rule.conditions.severity.map(sev => (
                        <span key={sev} className="px-2 py-1 bg-muted rounded text-xs">
                          {sev}
                        </span>
                      ))}
                    </div>
                  </div>
                )}
                
                {rule.conditions.keywords && (
                  <div className="flex items-center gap-2">
                    <span className="text-xs font-medium text-muted-foreground">Keywords:</span>
                    <div className="flex gap-1">
                      {rule.conditions.keywords.map(keyword => (
                        <span key={keyword} className="px-2 py-1 bg-muted rounded text-xs">
                          {keyword}
                        </span>
                      ))}
                    </div>
                  </div>
                )}
              </div>
            </div>
          ))}
        </div>
      </div>

      {/* ML Suggestions */}
      {automationSuggestions.length > 0 && (
        <div className="space-y-3">
          <h4 className="text-md font-semibold flex items-center gap-2">
            <Bot className="w-4 h-4" />
            ML Suggestions
          </h4>
          
          <div className="space-y-2">
            {automationSuggestions.map((suggestion, idx) => (
              <div
                key={idx}
                className="p-3 bg-blue-50 border border-blue-200 rounded-lg"
              >
                <div className="flex items-center justify-between">
                  <div>
                    <h6 className="font-medium text-sm text-blue-800">
                      {suggestion.name}
                    </h6>
                    <p className="text-xs text-blue-600">
                      Based on historical patterns with high resolution rates
                    </p>
                  </div>
                  <button className="btn-aileron bg-blue-500/10 text-blue-600 border border-blue-500/20 hover:bg-blue-500/20 px-3 py-1 text-xs">
                    Add Rule
                  </button>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
