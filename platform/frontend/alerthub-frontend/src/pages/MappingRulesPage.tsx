import React, { useState, useEffect } from 'react'
import { motion } from 'framer-motion'
import {
  Plus,
  Edit,
  Trash2,
  Play,
  Code,
  ArrowRight,
  FileJson,
  Hash,
  Type,
  Calendar,
} from 'lucide-react'

const tokens = {
  blue: '#007AFF',
  green: '#34C759',
  red: '#FF3B30',
  orange: '#FF9500',
  purple: '#AF52DE',
  label: 'var(--color-text)',
  secondaryLabel: 'var(--color-text-secondary)',
  tertiaryLabel: 'var(--color-text-tertiary, #8E8E93)',
  separator: 'var(--color-separator, rgba(142, 142, 147, 0.12))',
  fill: 'var(--color-fill, rgba(142, 142, 147, 0.08))',
  secondaryBackground: 'var(--color-card, rgba(255, 255, 255, 0.8))',
  radius: { sm: 6, md: 10, lg: 12, xl: 16 },
}

interface MappingRule {
  id: string
  name: string
  description: string
  enabled: boolean
  priority: number
  matchers: FieldMatcher[]
  extractions: FieldExtraction[]
  created_at: string
  execution_count?: number
  success_rate?: number
}

interface FieldMatcher {
  field: string
  operator: 'equals' | 'contains' | 'regex' | 'exists'
  value?: string
}

interface FieldExtraction {
  source_field: string
  target_field: string
  extraction_type: 'regex' | 'json_path' | 'split' | 'substring'
  pattern?: string
  transform?: string
}

export function MappingRulesPage() {
  const [rules, setRules] = useState<MappingRule[]>([])
  const [showModal, setShowModal] = useState(false)
  const [editingRule, setEditingRule] = useState<MappingRule | null>(null)
  const [showTestModal, setShowTestModal] = useState(false)
  const [testingRule, setTestingRule] = useState<MappingRule | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    loadRules()
  }, [])

  const loadRules = async () => {
    setLoading(true)
    try {
      const response = await fetch('/api/v1/mapping/rules', {
        headers: {
          'Authorization': `Bearer ${sessionStorage.getItem('access_token') || localStorage.getItem('access_token')}`,
        },
      })
      const data = await response.json()
      setRules(data.data?.rules || [])
    } catch (error) {
      console.error('Failed to load mapping rules:', error)
    } finally {
      setLoading(false)
    }
  }

  const deleteRule = async (ruleId: string) => {
    if (!confirm('Delete this mapping rule?')) return

    try {
      await fetch(`/api/v1/mapping/rules/${ruleId}`, {
        method: 'DELETE',
        headers: {
          'Authorization': `Bearer ${sessionStorage.getItem('access_token') || localStorage.getItem('access_token')}`,
        },
      })
      loadRules()
    } catch (error) {
      alert('Failed to delete rule')
    }
  }

  const toggleRule = async (ruleId: string, enabled: boolean) => {
    try {
      await fetch(`/api/v1/mapping/rules/${ruleId}`, {
        method: 'PATCH',
        headers: {
          'Content-Type': 'application/json',
          'Authorization': `Bearer ${sessionStorage.getItem('access_token') || localStorage.getItem('access_token')}`,
        },
        body: JSON.stringify({ enabled }),
      })
      loadRules()
    } catch (error) {
      alert('Failed to toggle rule')
    }
  }

  const runRule = async (ruleId: string) => {
    try {
      const response = await fetch(`/api/v1/mapping/rules/${ruleId}/execute`, {
        method: 'POST',
        headers: {
          'Authorization': `Bearer ${sessionStorage.getItem('access_token') || localStorage.getItem('access_token')}`,
        },
      })
      const data = await response.json()
      alert(`Executed successfully! Processed ${data.data?.processed || 0} alerts`)
      loadRules()
    } catch (error) {
      alert('Failed to execute rule')
    }
  }

  return (
    <div style={{ padding: 24, maxWidth: 1400, margin: '0 auto' }}>
      {/* Header */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 24 }}>
        <div>
          <h1 style={{ fontSize: 28, fontWeight: 600, color: tokens.label, marginBottom: 8 }}>
            Mapping & Extraction Rules
          </h1>
          <p style={{ fontSize: 15, color: tokens.secondaryLabel }}>
            Extract and transform fields from alert payloads
          </p>
        </div>
        <button
          onClick={() => {
            setEditingRule(null)
            setShowModal(true)
          }}
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 8,
            padding: '10px 20px',
            borderRadius: tokens.radius.sm,
            border: 'none',
            background: tokens.purple,
            color: '#fff',
            fontSize: 14,
            fontWeight: 500,
            cursor: 'pointer',
          }}
        >
          <Plus style={{ width: 16, height: 16 }} />
          Create Rule
        </button>
      </div>

      {/* Stats */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 16, marginBottom: 24 }}>
        <div style={{
          padding: 16,
          background: tokens.secondaryBackground,
          borderRadius: tokens.radius.md,
          border: `0.5px solid ${tokens.separator}`,
        }}>
          <div style={{ fontSize: 24, fontWeight: 700, color: tokens.purple }}>{rules.length}</div>
          <div style={{ fontSize: 13, color: tokens.tertiaryLabel }}>Total Rules</div>
        </div>
        <div style={{
          padding: 16,
          background: tokens.secondaryBackground,
          borderRadius: tokens.radius.md,
          border: `0.5px solid ${tokens.separator}`,
        }}>
          <div style={{ fontSize: 24, fontWeight: 700, color: tokens.green }}>
            {rules.filter(r => r.enabled).length}
          </div>
          <div style={{ fontSize: 13, color: tokens.tertiaryLabel }}>Active Rules</div>
        </div>
        <div style={{
          padding: 16,
          background: tokens.secondaryBackground,
          borderRadius: tokens.radius.md,
          border: `0.5px solid ${tokens.separator}`,
        }}>
          <div style={{ fontSize: 24, fontWeight: 700, color: tokens.blue }}>
            {rules.reduce((sum, r) => sum + (r.execution_count || 0), 0)}
          </div>
          <div style={{ fontSize: 13, color: tokens.tertiaryLabel }}>Executions</div>
        </div>
        <div style={{
          padding: 16,
          background: tokens.secondaryBackground,
          borderRadius: tokens.radius.md,
          border: `0.5px solid ${tokens.separator}`,
        }}>
          <div style={{ fontSize: 24, fontWeight: 700, color: tokens.orange }}>
            {Math.round(rules.reduce((sum, r) => sum + (r.success_rate || 0), 0) / Math.max(1, rules.length))}%
          </div>
          <div style={{ fontSize: 13, color: tokens.tertiaryLabel }}>Success Rate</div>
        </div>
      </div>

      {/* Rules List */}
      <div style={{
        background: tokens.secondaryBackground,
        borderRadius: tokens.radius.lg,
        border: `0.5px solid ${tokens.separator}`,
        overflow: 'hidden',
      }}>
        {loading ? (
          <div style={{ padding: 40, textAlign: 'center', color: tokens.tertiaryLabel }}>
            Loading mapping rules...
          </div>
        ) : rules.length === 0 ? (
          <div style={{ padding: 60, textAlign: 'center' }}>
            <FileJson style={{ width: 48, height: 48, color: tokens.tertiaryLabel, margin: '0 auto 16px' }} />
            <h3 style={{ fontSize: 18, fontWeight: 600, color: tokens.label, marginBottom: 8 }}>
              No mapping rules yet
            </h3>
            <p style={{ fontSize: 14, color: tokens.secondaryLabel, marginBottom: 20 }}>
              Create rules to extract and transform alert data
            </p>
            <button
              onClick={() => setShowModal(true)}
              style={{
                padding: '10px 20px',
                borderRadius: tokens.radius.sm,
                border: 'none',
                background: tokens.purple,
                color: '#fff',
                fontSize: 14,
                fontWeight: 500,
                cursor: 'pointer',
              }}
            >
              Create Your First Rule
            </button>
          </div>
        ) : (
          <div style={{ padding: 20 }}>
            {rules.sort((a, b) => b.priority - a.priority).map((rule) => (
              <motion.div
                key={rule.id}
                initial={{ opacity: 0 }}
                animate={{ opacity: 1 }}
                style={{
                  padding: 16,
                  background: tokens.fill,
                  borderRadius: tokens.radius.md,
                  border: `0.5px solid ${tokens.separator}`,
                  marginBottom: 12,
                }}
              >
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
                  <div style={{ flex: 1 }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 8 }}>
                      <h4 style={{ fontSize: 16, fontWeight: 600, color: tokens.label, margin: 0 }}>
                        {rule.name}
                      </h4>
                      <span style={{
                        padding: '2px 8px',
                        borderRadius: 10,
                        background: `${tokens.purple}15`,
                        fontSize: 11,
                        fontWeight: 600,
                        color: tokens.purple,
                      }}>
                        Priority: {rule.priority}
                      </span>
                      <button
                        onClick={() => toggleRule(rule.id, !rule.enabled)}
                        style={{
                          width: 40,
                          height: 20,
                          borderRadius: 10,
                          background: rule.enabled ? tokens.green : tokens.tertiaryLabel,
                          border: 'none',
                          cursor: 'pointer',
                          position: 'relative',
                          transition: 'all 0.2s',
                        }}
                      >
                        <div style={{
                          width: 16,
                          height: 16,
                          borderRadius: '50%',
                          background: '#fff',
                          position: 'absolute',
                          top: 2,
                          left: rule.enabled ? 22 : 2,
                          transition: 'left 0.2s',
                          boxShadow: '0 1px 3px rgba(0,0,0,0.2)',
                        }} />
                      </button>
                    </div>
                    
                    <p style={{ fontSize: 13, color: tokens.secondaryLabel, marginBottom: 12 }}>
                      {rule.description}
                    </p>

                    {/* Extraction Preview */}
                    <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
                      {rule.extractions.slice(0, 3).map((extraction, idx) => (
                        <div
                          key={idx}
                          style={{
                            display: 'flex',
                            alignItems: 'center',
                            gap: 6,
                            padding: '4px 10px',
                            borderRadius: tokens.radius.sm,
                            background: `${tokens.blue}08`,
                            border: `0.5px solid ${tokens.blue}20`,
                            fontSize: 12,
                          }}
                        >
                          <Code style={{ width: 10, height: 10, color: tokens.blue }} />
                          <span style={{ color: tokens.label, fontFamily: 'SFMono-Regular, monospace' }}>
                            {extraction.source_field}
                          </span>
                          <ArrowRight style={{ width: 10, height: 10, color: tokens.tertiaryLabel }} />
                          <span style={{ color: tokens.label, fontFamily: 'SFMono-Regular, monospace' }}>
                            {extraction.target_field}
                          </span>
                        </div>
                      ))}
                      {rule.extractions.length > 3 && (
                        <span style={{ fontSize: 12, color: tokens.tertiaryLabel }}>
                          +{rule.extractions.length - 3} more
                        </span>
                      )}
                    </div>

                    {/* Stats */}
                    {rule.execution_count && rule.execution_count > 0 && (
                      <div style={{ display: 'flex', gap: 12, marginTop: 8 }}>
                        <span style={{ fontSize: 12, color: tokens.tertiaryLabel }}>
                          Executions: <strong style={{ color: tokens.label }}>{rule.execution_count}</strong>
                        </span>
                        <span style={{ fontSize: 12, color: tokens.tertiaryLabel }}>
                          Success: <strong style={{ color: tokens.green }}>{rule.success_rate}%</strong>
                        </span>
                      </div>
                    )}
                  </div>

                  <div style={{ display: 'flex', gap: 8 }}>
                    <button
                      onClick={() => runRule(rule.id)}
                      style={{
                        padding: 8,
                        borderRadius: tokens.radius.sm,
                        border: 'none',
                        background: `${tokens.green}15`,
                        color: tokens.green,
                        cursor: 'pointer',
                      }}
                      title="Run rule now"
                    >
                      <Play style={{ width: 16, height: 16 }} />
                    </button>
                    <button
                      onClick={() => {
                        setEditingRule(rule)
                        setShowModal(true)
                      }}
                      style={{
                        padding: 8,
                        borderRadius: tokens.radius.sm,
                        border: 'none',
                        background: tokens.fill,
                        color: tokens.blue,
                        cursor: 'pointer',
                      }}
                    >
                      <Edit style={{ width: 16, height: 16 }} />
                    </button>
                    <button
                      onClick={() => deleteRule(rule.id)}
                      style={{
                        padding: 8,
                        borderRadius: tokens.radius.sm,
                        border: 'none',
                        background: `${tokens.red}15`,
                        color: tokens.red,
                        cursor: 'pointer',
                      }}
                    >
                      <Trash2 style={{ width: 16, height: 16 }} />
                    </button>
                  </div>
                </div>
              </motion.div>
            ))}
          </div>
        )}
      </div>

      {/* Create/Edit Modal */}
      {showModal && (
        <MappingRuleModal
          rule={editingRule}
          onClose={() => {
            setShowModal(false)
            setEditingRule(null)
          }}
          onSaved={loadRules}
        />
      )}
    </div>
  )
}

// Simplified modal for now - full implementation would have complex extraction builder
function MappingRuleModal({ rule, onClose, onSaved }: any) {
  const [name, setName] = useState(rule?.name || '')
  const [description, setDescription] = useState(rule?.description || '')
  const [priority, setPriority] = useState(String(rule?.priority || 100))
  const [saving, setSaving] = useState(false)

  const handleSave = async () => {
    setSaving(true)
    try {
      const url = rule 
        ? `/api/v1/mapping/rules/${rule.id}`
        : '/api/v1/mapping/rules'
      
      await fetch(url, {
        method: rule ? 'PUT' : 'POST',
        headers: {
          'Content-Type': 'application/json',
          'Authorization': `Bearer ${sessionStorage.getItem('access_token') || localStorage.getItem('access_token')}`,
        },
        body: JSON.stringify({
          name,
          description,
          priority: parseInt(priority),
          matchers: [],
          extractions: [],
          enabled: true,
        }),
      })

      onSaved()
      onClose()
    } catch (error) {
      alert('Failed to save rule')
    } finally {
      setSaving(false)
    }
  }

  return (
    <motion.div
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      style={{
        position: 'fixed',
        inset: 0,
        background: 'rgba(0,0,0,0.4)',
        backdropFilter: 'blur(20px)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        zIndex: 1000,
      }}
      onClick={onClose}
    >
      <motion.div
        initial={{ scale: 0.95 }}
        animate={{ scale: 1 }}
        onClick={(e) => e.stopPropagation()}
        style={{
          background: tokens.secondaryBackground,
          borderRadius: tokens.radius.lg,
          padding: 24,
          width: '90%',
          maxWidth: 600,
          maxHeight: '90vh',
          overflowY: 'auto',
        }}
      >
        <h3 style={{ fontSize: 20, fontWeight: 600, color: tokens.label, marginBottom: 20 }}>
          {rule ? 'Edit' : 'Create'} Mapping Rule
        </h3>

        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          <div>
            <label style={{ display: 'block', fontSize: 13, fontWeight: 500, color: tokens.secondaryLabel, marginBottom: 8 }}>
              Rule Name
            </label>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g., Extract service from labels"
              style={{
                width: '100%',
                height: 40,
                borderRadius: tokens.radius.sm,
                border: `0.5px solid ${tokens.separator}`,
                background: tokens.fill,
                padding: '0 12px',
                fontSize: 14,
                color: tokens.label,
                outline: 'none',
              }}
            />
          </div>

          <div>
            <label style={{ display: 'block', fontSize: 13, fontWeight: 500, color: tokens.secondaryLabel, marginBottom: 8 }}>
              Description
            </label>
            <textarea
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="What does this rule extract..."
              rows={2}
              style={{
                width: '100%',
                borderRadius: tokens.radius.sm,
                border: `0.5px solid ${tokens.separator}`,
                background: tokens.fill,
                padding: 12,
                fontSize: 14,
                color: tokens.label,
                outline: 'none',
                resize: 'vertical',
              }}
            />
          </div>

          <div>
            <label style={{ display: 'block', fontSize: 13, fontWeight: 500, color: tokens.secondaryLabel, marginBottom: 8 }}>
              Priority (1-1000)
            </label>
            <input
              type="number"
              value={priority}
              onChange={(e) => setPriority(e.target.value)}
              min="1"
              max="1000"
              style={{
                width: '100%',
                height: 40,
                borderRadius: tokens.radius.sm,
                border: `0.5px solid ${tokens.separator}`,
                background: tokens.fill,
                padding: '0 12px',
                fontSize: 14,
                color: tokens.label,
                outline: 'none',
              }}
            />
            <p style={{ fontSize: 11, color: tokens.tertiaryLabel, marginTop: 4 }}>
              Higher priority rules execute first
            </p>
          </div>

          <div style={{ display: 'flex', gap: 8, marginTop: 8 }}>
            <button
              onClick={onClose}
              style={{
                flex: 1,
                padding: '10px 16px',
                borderRadius: tokens.radius.sm,
                border: `0.5px solid ${tokens.separator}`,
                background: tokens.fill,
                color: tokens.label,
                fontSize: 14,
                fontWeight: 500,
                cursor: 'pointer',
              }}
            >
              Cancel
            </button>
            <button
              onClick={handleSave}
              disabled={!name || saving}
              style={{
                flex: 1,
                padding: '10px 16px',
                borderRadius: tokens.radius.sm,
                border: 'none',
                background: tokens.purple,
                color: '#fff',
                fontSize: 14,
                fontWeight: 500,
                cursor: (!name || saving) ? 'default' : 'pointer',
                opacity: (!name || saving) ? 0.5 : 1,
              }}
            >
              {saving ? 'Saving...' : (rule ? 'Update' : 'Create') + ' Rule'}
            </button>
          </div>
        </div>
      </motion.div>
    </motion.div>
  )
}
