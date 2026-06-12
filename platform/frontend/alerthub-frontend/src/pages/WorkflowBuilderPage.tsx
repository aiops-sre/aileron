import React, { useState, useEffect, useCallback } from 'react';
import { motion, AnimatePresence } from 'framer-motion';
import {
  Play,
  Pause,
  Plus,
  Save,
  Download,
  Upload,
  Settings,
  Trash2,
  Copy,
  Edit3,
  CheckCircle,
  XCircle,
  AlertTriangle,
  Clock,
  MoreVertical,
  Zap,
  GitBranch,
  Target,
  Filter,
  Search,
  RefreshCw,
  Eye,
  EyeOff,
  X,
  Loader2,
} from 'lucide-react';
import { workflowApi } from '../lib/api';

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Apple Design Tokens - Match existing theme
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

// Types
interface WorkflowStep {
  id: string;
  name: string;
  type: 'action' | 'condition' | 'wait' | 'notification';
  action?: Record<string, any>;
  condition?: Record<string, any>;
  enabled: boolean;
  position: { x: number; y: number };
}

interface WorkflowTrigger {
  type: 'alert' | 'schedule' | 'manual' | 'webhook';
  conditions?: Array<{
    field: string;
    operator: string;
    value: any;
  }>;
  schedule?: string;
}

interface Workflow {
  id: string;
  name: string;
  description: string;
  triggers: WorkflowTrigger[];
  steps: WorkflowStep[];
  enabled: boolean;
  tags: string[];
  created_at: string;
  updated_at: string;
  executions: number;
  last_run?: string;
  status?: 'active' | 'paused' | 'error';
}

interface WorkflowExecution {
  id: string;
  workflow_id: string;
  status: 'running' | 'completed' | 'failed' | 'cancelled';
  started_at: string;
  completed_at?: string;
  duration?: string;
  trigger_event: Record<string, any>;
  step_results?: Record<string, any>;
  error?: string;
}

interface WorkflowTemplate {
  id: string;
  name: string;
  description: string;
  category: string;
  tags: string[];
  usage_count: number;
  template_data: {
    triggers: any[];
    steps: any[];
  };
  parameters: Array<{
    name: string;
    type: string;
    default?: any;
    required?: boolean;
    description: string;
  }>;
}

const WorkflowBuilderPage: React.FC = () => {
  // State management
  const [workflows, setWorkflows] = useState<Workflow[]>([]);
  const [selectedWorkflow, setSelectedWorkflow] = useState<Workflow | null>(null);
  const [editingWorkflow, setEditingWorkflow] = useState<Workflow | null>(null);
  const [workflowExecutions, setWorkflowExecutions] = useState<WorkflowExecution[]>([]);
  const [templates, setTemplates] = useState<WorkflowTemplate[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  
  // UI state
  const [activeTab, setActiveTab] = useState<'workflows' | 'executions' | 'templates'>('workflows');
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [showTemplateModal, setShowTemplateModal] = useState(false);
  const [searchQuery, setSearchQuery] = useState('');
  const [filterEnabled, setFilterEnabled] = useState<boolean | null>(null);
  const [selectedTemplate, setSelectedTemplate] = useState<WorkflowTemplate | null>(null);

  // Load workflows
  const loadWorkflows = useCallback(async () => {
    try {
      setLoading(true);
      const response = await workflowApi.listWorkflows({
        enabled: filterEnabled ?? undefined,
      });
      
      if (response.data?.success) {
        setWorkflows(response.data.data?.workflows || []);
      }
    } catch (err: any) {
      setError(err.message || 'Failed to load workflows');
    } finally {
      setLoading(false);
    }
  }, [filterEnabled]);

  // Load workflow executions
  const loadExecutions = useCallback(async (workflowId?: string) => {
    if (!workflowId && !selectedWorkflow) return;
    
    try {
      const id = workflowId || selectedWorkflow!.id;
      const response = await workflowApi.listWorkflowExecutions(id);
      
      if (response.data?.success) {
        setWorkflowExecutions(response.data.data?.executions || []);
      }
    } catch (err: any) {
      console.error('Failed to load executions:', err);
    }
  }, [selectedWorkflow]);

  // Load templates
  const loadTemplates = useCallback(async () => {
    try {
      const response = await workflowApi.listWorkflowTemplates();
      
      if (response.data?.success) {
        setTemplates(response.data.data?.templates || []);
      }
    } catch (err: any) {
      console.error('Failed to load templates:', err);
    }
  }, []);

  // Initialize
  useEffect(() => {
    loadWorkflows();
    loadTemplates();
  }, [loadWorkflows, loadTemplates]);

  // Load executions when workflow selected
  useEffect(() => {
    if (selectedWorkflow && activeTab === 'executions') {
      loadExecutions();
    }
  }, [selectedWorkflow, activeTab, loadExecutions]);

  // Execute workflow
  const executeWorkflow = async (workflow: Workflow) => {
    try {
      await workflowApi.executeWorkflow(workflow.id);
      
      // Refresh executions
      loadExecutions(workflow.id);
      
      // Show success message
      setError(null);
    } catch (err: any) {
      setError(err.message || 'Failed to execute workflow');
    }
  };

  // Toggle workflow enabled state
  const toggleWorkflow = async (workflow: Workflow) => {
    try {
      if (workflow.enabled) {
        await workflowApi.disableWorkflow(workflow.id);
      } else {
        await workflowApi.enableWorkflow(workflow.id);
      }
      
      // Refresh workflows
      loadWorkflows();
    } catch (err: any) {
      setError(err.message || 'Failed to toggle workflow');
    }
  };

  // Delete workflow
  const deleteWorkflow = async (workflow: Workflow) => {
    if (!window.confirm('Are you sure you want to delete this workflow?')) {
      return;
    }
    
    try {
      await workflowApi.deleteWorkflow(workflow.id);
      setWorkflows(prev => prev.filter(w => w.id !== workflow.id));
      
      if (selectedWorkflow?.id === workflow.id) {
        setSelectedWorkflow(null);
      }
    } catch (err: any) {
      setError(err.message || 'Failed to delete workflow');
    }
  };

  // Create workflow from template
  const createFromTemplate = async (template: WorkflowTemplate, name: string, parameters: Record<string, any>) => {
    try {
      const response = await workflowApi.createFromTemplate(template.id, {
        name,
        parameters,
        enabled: true,
      });
      
      if (response.data?.success) {
        loadWorkflows();
        setShowTemplateModal(false);
        setSelectedTemplate(null);
      }
    } catch (err: any) {
      setError(err.message || 'Failed to create workflow from template');
    }
  };

  // Filter workflows
  const filteredWorkflows = workflows.filter(workflow => {
    const matchesSearch = !searchQuery || 
      workflow.name.toLowerCase().includes(searchQuery.toLowerCase()) ||
      workflow.description.toLowerCase().includes(searchQuery.toLowerCase()) ||
      workflow.tags.some(tag => tag.toLowerCase().includes(searchQuery.toLowerCase()));
    
    const matchesFilter = filterEnabled === null || workflow.enabled === filterEnabled;
    
    return matchesSearch && matchesFilter;
  });

  // Get status color (Apple theme)
  const getStatusColor = (status?: string) => {
    switch (status) {
      case 'active': return apple.green;
      case 'paused': return apple.yellow;
      case 'error': return apple.red;
      default: return apple.gray;
    }
  };

  const getExecutionStatusColor = (status: string) => {
    switch (status) {
      case 'running': return apple.blue;
      case 'completed': return apple.green;
      case 'failed': return apple.red;
      case 'cancelled': return apple.gray;
      default: return apple.gray;
    }
  };

  return (
    <div style={{
      minHeight: '100vh',
      background: apple.background,
    }}>
      <div style={{
        display: 'flex',
        flexDirection: window.innerWidth < 1024 ? 'column' : 'row',
        maxWidth: 1200,
        margin: '0 auto',
        padding: window.innerWidth < 768 ? '16px 12px' : '24px 16px',
        gap: window.innerWidth < 1024 ? 16 : 32,
        minHeight: '100vh',
      }}>
        {/* Sidebar */}
        <div style={{
          position: window.innerWidth < 1024 ? 'static' : 'sticky',
          top: window.innerWidth < 1024 ? 'auto' : 24,
          alignSelf: 'flex-start',
          width: window.innerWidth < 1024 ? '100%' : 220,
        }}>
          <div style={{
            display: 'flex',
            alignItems: 'center',
            gap: 8,
            padding: window.innerWidth < 768 ? '4px 8px 16px' : '4px 12px 20px',
            justifyContent: window.innerWidth < 768 ? 'center' : 'flex-start',
          }}>
            <span style={{
              fontSize: 28,
              fontWeight: 700,
              color: apple.label,
              letterSpacing: '-0.02em',
            }}>
              Workflows
            </span>
            <div style={{
              display: 'flex',
              alignItems: 'center',
              gap: 4,
              padding: '2px 6px',
              borderRadius: 8,
              background: 'rgba(175, 82, 222, 0.1)',
            }}>
              <div style={{
                width: 6,
                height: 6,
                borderRadius: '50%',
                background: apple.purple,
                animation: 'pulse 2s infinite',
              }} />
              <span style={{
                fontSize: 10,
                fontWeight: 600,
                color: apple.purple,
                textTransform: 'uppercase',
                letterSpacing: '0.5px',
              }}>
                Auto
              </span>
            </div>
          </div>

          {/* Tab Navigation */}
          <nav style={{ padding: '8px 0' }}>
            {[
              { id: 'workflows', label: 'All Workflows', icon: GitBranch, iconColor: apple.purple, count: workflows.length },
              { id: 'executions', label: 'Executions', icon: Clock, iconColor: apple.orange, count: workflowExecutions.length, disabled: !selectedWorkflow },
              { id: 'templates', label: 'Templates', icon: Target, iconColor: apple.green, count: templates.length },
            ].map((item) => {
              const active = item.id === activeTab
              const Icon = item.icon
              return (
                <button
                  key={item.id}
                  onClick={() => !item.disabled && setActiveTab(item.id as any)}
                  disabled={item.disabled}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 10,
                    width: '100%',
                    padding: '7px 12px',
                    borderRadius: apple.radius.sm,
                    border: 'none',
                    cursor: item.disabled ? 'not-allowed' : 'pointer',
                    background: active ? 'rgba(0, 122, 255, 0.12)' : 'transparent',
                    transition: 'background 0.15s',
                    marginBottom: 1,
                    textAlign: 'left',
                    opacity: item.disabled ? 0.5 : 1,
                  }}
                  onMouseEnter={(e) => {
                    if (!active && !item.disabled) (e.currentTarget as HTMLElement).style.background = apple.tertiaryFill
                  }}
                  onMouseLeave={(e) => {
                    if (!active && !item.disabled) (e.currentTarget as HTMLElement).style.background = 'transparent'
                  }}
                >
                  <div style={{
                    width: 26,
                    height: 26,
                    borderRadius: 6,
                    background: item.iconColor,
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    flexShrink: 0,
                  }}>
                    <Icon style={{ width: 14, height: 14, color: '#fff' }} />
                  </div>
                  <span style={{
                    fontSize: 13,
                    fontWeight: active ? 600 : 400,
                    color: active ? apple.blue : apple.label,
                    flex: 1,
                  }}>
                    {item.label}
                  </span>
                  {item.count !== undefined && item.count > 0 && (
                    <span style={{
                      fontSize: 12,
                      fontWeight: 500,
                      color: apple.tertiaryLabel,
                      background: apple.fill,
                      padding: '1px 7px',
                      borderRadius: 10,
                    }}>
                      {item.count}
                    </span>
                  )}
                </button>
              )
            })}
          </nav>
        </div>

        {/* Content */}
        <div style={{ flex: 1, minWidth: 0 }}>
          {/* Header */}
          <div style={{ marginBottom: 20 }}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 16 }}>
              <div>
                <h1 style={{ fontSize: 22, fontWeight: 700, color: apple.label, margin: 0 }}>
                  {activeTab === 'workflows' ? 'Workflow Automation' : 
                   activeTab === 'executions' ? 'Execution History' : 'Workflow Templates'}
                </h1>
                <p style={{ fontSize: 13, color: apple.secondaryLabel, marginTop: 2 }}>
                  {activeTab === 'workflows' ? `${workflows.length} workflows configured` :
                   activeTab === 'executions' ? `${workflowExecutions.length} executions` :
                   `${templates.length} templates available`}
                </p>
              </div>
              
              <div style={{ display: 'flex', gap: 8 }}>
                {activeTab === 'workflows' && (
                  <>
                    <button
                      onClick={() => setShowTemplateModal(true)}
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
                      Templates
                    </button>
                    
                    <button
                      onClick={() => setShowCreateModal(true)}
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
                      <Plus style={{ width: 14, height: 14 }} />
                      Create
                    </button>
                  </>
                )}
                
                <button
                  onClick={loadWorkflows}
                  disabled={loading}
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
                    cursor: loading ? 'default' : 'pointer',
                    opacity: loading ? 0.5 : 1,
                  }}
                >
                  <RefreshCw style={{ width: 14, height: 14, ...(loading && { animation: 'spin 1s linear infinite' }) }} />
                  Refresh
                </button>
              </div>
            </div>

            {/* Error Alert */}
            {error && (
              <div style={{
                display: 'flex',
                alignItems: 'center',
                gap: 8,
                padding: 12,
                marginBottom: 16,
                background: `${apple.red}15`,
                border: `0.5px solid ${apple.red}30`,
                borderRadius: apple.radius.sm,
              }}>
                <AlertTriangle style={{ width: 16, height: 16, color: apple.red, flexShrink: 0 }} />
                <p style={{ fontSize: 13, color: apple.red, margin: 0, flex: 1 }}>
                  {error}
                </p>
                <button
                  onClick={() => setError(null)}
                  style={{
                    background: 'none',
                    border: 'none',
                    cursor: 'pointer',
                    padding: 2,
                    color: apple.red,
                  }}
                >
                  <X style={{ width: 14, height: 14 }} />
                </button>
              </div>
            )}

            {/* Search and Filters for workflows tab */}
            {activeTab === 'workflows' && (
              <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
                <div style={{ position: 'relative', width: 200 }}>
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
                    placeholder="Search workflows..."
                    style={{
                      width: '100%',
                      height: 36,
                      borderRadius: apple.radius.md,
                      border: 'none',
                      background: apple.fill,
                      paddingLeft: 34,
                      paddingRight: searchQuery ? 34 : 12,
                      fontSize: 13,
                      color: apple.label,
                      outline: 'none',
                      transition: 'box-shadow 0.2s ease',
                    }}
                    onFocus={(e) => {
                      e.target.style.boxShadow = `0 0 0 3px rgba(175, 82, 222, 0.25)`
                    }}
                    onBlur={(e) => {
                      e.target.style.boxShadow = 'none'
                    }}
                  />
                  {searchQuery && (
                    <button
                      onClick={() => setSearchQuery('')}
                      style={{
                        position: 'absolute',
                        right: 8,
                        top: '50%',
                        transform: 'translateY(-50%)',
                        width: 20,
                        height: 20,
                        borderRadius: '50%',
                        background: apple.tertiaryLabel,
                        border: 'none',
                        cursor: 'pointer',
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                        padding: 0,
                      }}
                    >
                      <X style={{ width: 12, height: 12, color: apple.secondaryBackground }} />
                    </button>
                  )}
                </div>
                
                <select
                  value={filterEnabled === null ? 'all' : filterEnabled.toString()}
                  onChange={(e) => {
                    const value = e.target.value;
                    setFilterEnabled(value === 'all' ? null : value === 'true');
                  }}
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
                  <option value="all">All Workflows</option>
                  <option value="true">Enabled Only</option>
                  <option value="false">Disabled Only</option>
                </select>
              </div>
            )}
          </div>

          {/* Content Area */}
          <div style={{
            background: apple.secondaryBackground,
            borderRadius: apple.radius.lg,
            border: `0.5px solid ${apple.separator}`,
            overflow: 'hidden',
            minHeight: 600,
          }}>
            <AnimatePresence mode="wait">
              <motion.div
                key={activeTab}
                initial={{ opacity: 0, y: 8 }}
                animate={{ opacity: 1, y: 0 }}
                exit={{ opacity: 0, y: -8 }}
                transition={{ duration: 0.2 }}
                style={{ height: '100%', padding: 16 }}
              >
                {/* Workflows Tab */}
                {activeTab === 'workflows' && (
                  loading ? (
                    <div style={{
                      textAlign: 'center',
                      padding: '80px 20px',
                    }}>
                      <Loader2 style={{ width: 32, height: 32, color: apple.purple, margin: '0 auto 16px', animation: 'spin 1s linear infinite' }} />
                      <p style={{ fontSize: 15, color: apple.secondaryLabel, margin: 0 }}>
                        Loading workflows...
                      </p>
                    </div>
                  ) : filteredWorkflows.length > 0 ? (
                    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(300px, 1fr))', gap: 16 }}>
                      {filteredWorkflows.map((workflow) => (
                        <motion.div
                          key={workflow.id}
                          initial={{ opacity: 0, y: 20 }}
                          animate={{ opacity: 1, y: 0 }}
                          style={{
                            background: apple.secondaryBackground,
                            border: `0.5px solid ${apple.separator}`,
                            borderRadius: apple.radius.lg,
                            padding: 16,
                            cursor: 'pointer',
                            ...(selectedWorkflow?.id === workflow.id && { 
                              boxShadow: `0 0 0 2px ${apple.purple}` 
                            })
                          }}
                          onClick={() => setSelectedWorkflow(workflow)}
                        >
                          <div style={{ display: 'flex', alignItems: 'start', justifyContent: 'space-between', marginBottom: 12 }}>
                            <div style={{ flex: 1, minWidth: 0 }}>
                              <h3 style={{ 
                                fontSize: 16, 
                                fontWeight: 600, 
                                color: apple.label, 
                                margin: 0, 
                                marginBottom: 4,
                                overflow: 'hidden',
                                textOverflow: 'ellipsis',
                                whiteSpace: 'nowrap',
                              }}>
                                {workflow.name}
                              </h3>
                              <p style={{ fontSize: 13, color: apple.secondaryLabel, margin: 0, lineHeight: 1.4 }}>
                                {workflow.description}
                              </p>
                            </div>
                            
                            <button style={{
                              background: 'none',
                              border: 'none',
                              cursor: 'pointer',
                              padding: 4,
                              color: apple.tertiaryLabel,
                              marginLeft: 8,
                            }}>
                              <MoreVertical style={{ width: 16, height: 16 }} />
                            </button>
                          </div>

                          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 12 }}>
                            <div style={{
                              display: 'flex',
                              alignItems: 'center',
                              gap: 4,
                              padding: '2px 8px',
                              borderRadius: 12,
                              background: workflow.enabled ? `${apple.green}15` : `${apple.gray}15`,
                              fontSize: 11,
                              fontWeight: 500,
                              color: workflow.enabled ? apple.green : apple.gray,
                            }}>
                              {workflow.enabled ? (
                                <>
                                  <CheckCircle style={{ width: 12, height: 12 }} />
                                  Enabled
                                </>
                              ) : (
                                <>
                                  <Pause style={{ width: 12, height: 12 }} />
                                  Disabled
                                </>
                              )}
                            </div>
                            
                            {workflow.status && (
                              <div style={{
                                padding: '2px 8px',
                                borderRadius: 12,
                                background: `${getStatusColor(workflow.status)}15`,
                                fontSize: 11,
                                fontWeight: 500,
                                color: getStatusColor(workflow.status),
                              }}>
                                {workflow.status}
                              </div>
                            )}
                          </div>

                          <div style={{ 
                            display: 'flex', 
                            alignItems: 'center', 
                            justifyContent: 'space-between', 
                            fontSize: 12, 
                            color: apple.tertiaryLabel,
                            marginBottom: 12,
                          }}>
                            <span>{workflow.executions || 0} executions</span>
                            {workflow.last_run && (
                              <span>Last: {new Date(workflow.last_run).toLocaleDateString()}</span>
                            )}
                          </div>

                          <div style={{ display: 'flex', gap: 8 }}>
                            <button
                              onClick={(e) => {
                                e.stopPropagation();
                                executeWorkflow(workflow);
                              }}
                              disabled={!workflow.enabled}
                              style={{
                                flex: 1,
                                display: 'flex',
                                alignItems: 'center',
                                justifyContent: 'center',
                                gap: 4,
                                padding: '6px 12px',
                                borderRadius: apple.radius.sm,
                                border: 'none',
                                background: workflow.enabled ? apple.blue : apple.fill,
                                color: workflow.enabled ? '#fff' : apple.tertiaryLabel,
                                fontSize: 12,
                                fontWeight: 500,
                                cursor: workflow.enabled ? 'pointer' : 'not-allowed',
                                opacity: workflow.enabled ? 1 : 0.5,
                              }}
                            >
                              <Play style={{ width: 12, height: 12 }} />
                              Execute
                            </button>
                            
                            <button
                              onClick={(e) => {
                                e.stopPropagation();
                                toggleWorkflow(workflow);
                              }}
                              style={{
                                padding: '6px',
                                borderRadius: apple.radius.sm,
                                border: `0.5px solid ${apple.separator}`,
                                background: apple.fill,
                                color: apple.label,
                                cursor: 'pointer',
                              }}
                            >
                              {workflow.enabled ? (
                                <EyeOff style={{ width: 14, height: 14 }} />
                              ) : (
                                <Eye style={{ width: 14, height: 14 }} />
                              )}
                            </button>
                            
                            <button
                              onClick={(e) => {
                                e.stopPropagation();
                                deleteWorkflow(workflow);
                              }}
                              style={{
                                padding: '6px',
                                borderRadius: apple.radius.sm,
                                border: `0.5px solid ${apple.red}30`,
                                background: `${apple.red}10`,
                                color: apple.red,
                                cursor: 'pointer',
                              }}
                            >
                              <Trash2 style={{ width: 14, height: 14 }} />
                            </button>
                          </div>
                        </motion.div>
                      ))}
                    </div>
                  ) : (
                    <div style={{
                      textAlign: 'center',
                      padding: '80px 20px',
                    }}>
                      <GitBranch style={{ width: 48, height: 48, color: apple.quaternaryLabel, margin: '0 auto 16px' }} />
                      <p style={{ fontSize: 17, fontWeight: 500, color: apple.secondaryLabel, margin: 0 }}>
                        No workflows found
                      </p>
                      <p style={{ fontSize: 13, color: apple.tertiaryLabel, marginTop: 4 }}>
                        {searchQuery || filterEnabled !== null 
                          ? 'Try adjusting your search or filters'
                          : 'Get started by creating your first workflow'
                        }
                      </p>
                      {!searchQuery && filterEnabled === null && (
                        <button
                          onClick={() => setShowCreateModal(true)}
                          style={{
                            display: 'inline-flex',
                            alignItems: 'center',
                            gap: 6,
                            padding: '8px 16px',
                            borderRadius: apple.radius.sm,
                            border: 'none',
                            background: apple.blue,
                            color: '#fff',
                            fontSize: 13,
                            fontWeight: 500,
                            cursor: 'pointer',
                            marginTop: 16,
                          }}
                        >
                          <Plus style={{ width: 14, height: 14 }} />
                          Create Workflow
                        </button>
                      )}
                    </div>
                  )
                )}

                {/* Executions Tab */}
                {activeTab === 'executions' && (
                  selectedWorkflow ? (
                    <div>
                      <div style={{ marginBottom: 16 }}>
                        <h3 style={{ fontSize: 16, fontWeight: 600, color: apple.label, margin: 0 }}>
                          Executions for "{selectedWorkflow.name}"
                        </h3>
                        <p style={{ fontSize: 13, color: apple.secondaryLabel, marginTop: 4 }}>
                          Recent execution history and status
                        </p>
                      </div>

                      {workflowExecutions.length > 0 ? (
                        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                          {workflowExecutions.map((execution) => (
                            <div key={execution.id} style={{
                              background: apple.secondaryBackground,
                              border: `0.5px solid ${apple.separator}`,
                              borderRadius: apple.radius.lg,
                              padding: 16,
                            }}>
                              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                                <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                                  <div style={{
                                    padding: '4px 8px',
                                    borderRadius: 12,
                                    background: `${getExecutionStatusColor(execution.status)}15`,
                                    fontSize: 11,
                                    fontWeight: 500,
                                    color: getExecutionStatusColor(execution.status),
                                  }}>
                                    {execution.status}
                                  </div>
                                  
                                  <span style={{ fontSize: 13, color: apple.secondaryLabel }}>
                                    Started: {new Date(execution.started_at).toLocaleString()}
                                  </span>
                                  
                                  {execution.duration && (
                                    <span style={{ fontSize: 13, color: apple.secondaryLabel }}>
                                      Duration: {execution.duration}
                                    </span>
                                  )}
                                </div>
                                
                                {execution.status === 'running' && (
                                  <button style={{
                                    padding: 4,
                                    border: 'none',
                                    background: 'none',
                                    cursor: 'pointer',
                                    color: apple.red,
                                  }}>
                                    <XCircle style={{ width: 16, height: 16 }} />
                                  </button>
                                )}
                              </div>
                              
                              {execution.error && (
                                <div style={{ marginTop: 8, fontSize: 13, color: apple.red }}>
                                  Error: {execution.error}
                                </div>
                              )}
                            </div>
                          ))}
                        </div>
                      ) : (
                        <div style={{
                          textAlign: 'center',
                          padding: '60px 20px',
                        }}>
                          <Clock style={{ width: 48, height: 48, color: apple.quaternaryLabel, margin: '0 auto 16px' }} />
                          <p style={{ fontSize: 15, fontWeight: 500, color: apple.secondaryLabel, margin: 0 }}>
                            No executions yet
                          </p>
                          <p style={{ fontSize: 13, color: apple.tertiaryLabel, marginTop: 4 }}>
                            Execute the workflow to see its history here
                          </p>
                        </div>
                      )}
                    </div>
                  ) : (
                    <div style={{
                      textAlign: 'center',
                      padding: '80px 20px',
                    }}>
                      <Target style={{ width: 48, height: 48, color: apple.quaternaryLabel, margin: '0 auto 16px' }} />
                      <p style={{ fontSize: 15, fontWeight: 500, color: apple.secondaryLabel, margin: 0 }}>
                        Select a workflow
                      </p>
                      <p style={{ fontSize: 13, color: apple.tertiaryLabel, marginTop: 4 }}>
                        Choose a workflow to view its execution history
                      </p>
                    </div>
                  )
                )}

                {/* Templates Tab */}
                {activeTab === 'templates' && (
                  <div>
                    <div style={{ marginBottom: 16 }}>
                      <h3 style={{ fontSize: 16, fontWeight: 600, color: apple.label, margin: 0 }}>
                        Workflow Templates
                      </h3>
                      <p style={{ fontSize: 13, color: apple.secondaryLabel, marginTop: 4 }}>
                        Pre-built workflow templates to get you started quickly
                      </p>
                    </div>

                    {templates.length > 0 ? (
                      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))', gap: 16 }}>
                        {templates.map((template) => (
                          <div key={template.id} style={{
                            background: apple.secondaryBackground,
                            border: `0.5px solid ${apple.separator}`,
                            borderRadius: apple.radius.lg,
                            padding: 16,
                          }}>
                            <div style={{ marginBottom: 12 }}>
                              <h4 style={{ fontSize: 15, fontWeight: 600, color: apple.label, margin: 0, marginBottom: 4 }}>
                                {template.name}
                              </h4>
                              <p style={{ fontSize: 13, color: apple.secondaryLabel, margin: 0, lineHeight: 1.4 }}>
                                {template.description}
                              </p>
                            </div>
                            
                            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 12 }}>
                              <div style={{
                                padding: '2px 8px',
                                borderRadius: 12,
                                background: `${apple.blue}15`,
                                fontSize: 11,
                                fontWeight: 500,
                                color: apple.blue,
                              }}>
                                {template.category}
                              </div>
                              <span style={{ fontSize: 12, color: apple.tertiaryLabel }}>
                                {template.usage_count} uses
                              </span>
                            </div>
                            
                            <button
                              onClick={() => {
                                setSelectedTemplate(template);
                                setShowTemplateModal(true);
                              }}
                              style={{
                                width: '100%',
                                display: 'flex',
                                alignItems: 'center',
                                justifyContent: 'center',
                                gap: 6,
                                padding: '8px 16px',
                                borderRadius: apple.radius.sm,
                                border: 'none',
                                background: apple.blue,
                                color: '#fff',
                                fontSize: 13,
                                fontWeight: 500,
                                cursor: 'pointer',
                              }}
                            >
                              <Plus style={{ width: 14, height: 14 }} />
                              Use Template
                            </button>
                          </div>
                        ))}
                      </div>
                    ) : (
                      <div style={{
                        textAlign: 'center',
                        padding: '80px 20px',
                      }}>
                        <Target style={{ width: 48, height: 48, color: apple.quaternaryLabel, margin: '0 auto 16px' }} />
                        <p style={{ fontSize: 15, fontWeight: 500, color: apple.secondaryLabel, margin: 0 }}>
                          No templates available
                        </p>
                        <p style={{ fontSize: 13, color: apple.tertiaryLabel, marginTop: 4 }}>
                          Check back later for pre-built workflow templates
                        </p>
                      </div>
                    )}
                  </div>
                )}
              </motion.div>
            </AnimatePresence>
          </div>
        </div>
      </div>

      {/* Template Modal */}
      {showTemplateModal && (
        <div style={{
          position: 'fixed',
          inset: 0,
          background: 'rgba(0, 0, 0, 0.4)',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          zIndex: 50,
          backdropFilter: 'blur(10px)',
        }}>
          <div style={{
            background: apple.secondaryBackground,
            borderRadius: apple.radius.lg,
            padding: 20,
            width: '90%',
            maxWidth: 400,
            boxShadow: '0 20px 40px rgba(0,0,0,0.15)',
          }}>
            <h3 style={{ fontSize: 17, fontWeight: 600, color: apple.label, margin: 0, marginBottom: 8 }}>
              Create from Template
            </h3>
            <p style={{ fontSize: 14, color: apple.secondaryLabel, margin: 0, marginBottom: 20 }}>
              This feature will be implemented to create workflows from templates
            </p>
            <div style={{ display: 'flex', gap: 12 }}>
              <button
                onClick={() => {
                  setShowTemplateModal(false);
                  setSelectedTemplate(null);
                }}
                style={{
                  flex: 1,
                  padding: '8px 16px',
                  borderRadius: apple.radius.sm,
                  border: `0.5px solid ${apple.separator}`,
                  background: apple.fill,
                  color: apple.label,
                  fontSize: 14,
                  fontWeight: 500,
                  cursor: 'pointer',
                }}
              >
                Cancel
              </button>
              <button
                onClick={() => {
                  // TODO: Implement template creation
                  setShowTemplateModal(false);
                }}
                style={{
                  flex: 1,
                  padding: '8px 16px',
                  borderRadius: apple.radius.sm,
                  border: 'none',
                  background: apple.blue,
                  color: '#fff',
                  fontSize: 14,
                  fontWeight: 500,
                  cursor: 'pointer',
                }}
              >
                Create
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Create Modal */}
      {showCreateModal && (
        <div style={{
          position: 'fixed',
          inset: 0,
          background: 'rgba(0, 0, 0, 0.4)',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          zIndex: 50,
          backdropFilter: 'blur(10px)',
        }}>
          <div style={{
            background: apple.secondaryBackground,
            borderRadius: apple.radius.lg,
            padding: 20,
            width: '90%',
            maxWidth: 400,
            boxShadow: '0 20px 40px rgba(0,0,0,0.15)',
          }}>
            <h3 style={{ fontSize: 17, fontWeight: 600, color: apple.label, margin: 0, marginBottom: 8 }}>
              Create New Workflow
            </h3>
            <p style={{ fontSize: 14, color: apple.secondaryLabel, margin: 0, marginBottom: 20 }}>
              Workflow builder interface will be implemented here
            </p>
            <div style={{ display: 'flex', gap: 12 }}>
              <button
                onClick={() => setShowCreateModal(false)}
                style={{
                  flex: 1,
                  padding: '8px 16px',
                  borderRadius: apple.radius.sm,
                  border: `0.5px solid ${apple.separator}`,
                  background: apple.fill,
                  color: apple.label,
                  fontSize: 14,
                  fontWeight: 500,
                  cursor: 'pointer',
                }}
              >
                Cancel
              </button>
              <button
                onClick={() => {
                  // TODO: Implement workflow creation
                  setShowCreateModal(false);
                }}
                style={{
                  flex: 1,
                  padding: '8px 16px',
                  borderRadius: apple.radius.sm,
                  border: 'none',
                  background: apple.blue,
                  color: '#fff',
                  fontSize: 14,
                  fontWeight: 500,
                  cursor: 'pointer',
                }}
              >
                Create
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Global styles */}
      <style>{`
        @keyframes spin { from { transform: rotate(0deg) } to { transform: rotate(360deg) } }
        @keyframes pulse { 0%, 100% { opacity: 1 } 50% { opacity: 0.5 } }
      `}</style>
    </div>
  );
};

export default WorkflowBuilderPage;