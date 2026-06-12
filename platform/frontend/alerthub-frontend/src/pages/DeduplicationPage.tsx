import React, { useState, useEffect } from 'react'
import { motion } from 'framer-motion'
import { 
  Plus, 
  Edit, 
  Trash2, 
  Play, 
  Save, 
  X,
  Code,
  Fingerprint,
  CheckCircle,
  XCircle,
} from 'lucide-react'

const apple = {
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

interface DeduplicationRule {
  id: string
  name: string
  description: string
  enabled: boolean
  fields: string[]
  fingerprint_fields: string[]
  merge_strategy: 'first' | 'last' | 'concatenate'
  time_window: number // seconds
  created_at: string
  last_run?: string
  dedup_count?: number
}

export function DeduplicationPage() {
  const [rules, setRules] = useState<DeduplicationRule[]>([])
  const [showModal, setShowModal] = useState(false)
  const [editingRule, setEditingRule] = useState<DeduplicationRule | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    loadRules()
  }, [])

  const loadRules = async () => {
    setLoading(true)
    try {
      const response = await fetch('/api/v1/deduplication/rules', {
        headers: {
          'Authorization': `Bearer ${sessionStorage.getItem('access_token') || localStorage.getItem('access_token')}`,
        },
      })
      const data = await response.json()
      setRules(data.data?.rules || [])
    } catch (error) {
      console.error('Failed to load deduplication rules:', error)
    } finally {
      setLoading(false)
    }
  }

  const deleteRule = async (ruleId: string) => {
    if (!confirm('Delete this deduplication rule?')) return

    try {
      await fetch(`/api/v1/deduplication/rules/${ruleId}`, {
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
      await fetch(`/api/v1/deduplication/rules/${ruleId}`, {
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

  return (
    <div style={{ padding: 24, maxWidth: 1400, margin: '0 auto' }}>
      {/* Header */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 24 }}>
        <div>
          <h1 style={{ fontSize: 28, fontWeight: 600, color: apple.label, marginBottom: 8 }}>
            Alert Deduplication Rules
          </h1>
          <p style={{ fontSize: 15, color: apple.secondaryLabel }}>
            Automatically merge duplicate alerts based on field matching
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
            borderRadius: apple.radius.sm,
            border: 'none',
            background: apple.blue,
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
          background: apple.secondaryBackground,
          borderRadius: apple.radius.md,
          border: `0.5px solid ${apple.separator}`,
        }}>
          <div style={{ fontSize: 24, fontWeight: 700, color: apple.blue }}>{rules.length}</div>
          <div style={{ fontSize: 13, color: apple.tertiaryLabel }}>Total Rules</div>
        </div>
        <div style={{
          padding: 16,
          background: apple.secondaryBackground,
          borderRadius: apple.radius.md,
          border: `0.5px solid ${apple.separator}`,
        }}>
          <div style={{ fontSize: 24, fontWeight: 700, color: apple.green }}>
            {rules.filter(r => r.enabled).length}
          </div>
          <div style={{ fontSize: 13, color: apple.tertiaryLabel }}>Active Rules</div>
        </div>
        <div style={{
          padding: 16,
          background: apple.secondaryBackground,
          borderRadius: apple.radius.md,
          border: `0.5px solid ${apple.separator}`,
        }}>
          <div style={{ fontSize: 24, fontWeight: 700, color: apple.purple }}>
            {rules.reduce((sum, r) => sum + (r.dedup_count || 0), 0)}
          </div>
          <div style={{ fontSize: 13, color: apple.tertiaryLabel }}>Alerts Deduplicated</div>
        </div>
        <div style={{
          padding: 16,
          background: apple.secondaryBackground,
          borderRadius: apple.radius.md,
          border: `0.5px solid ${apple.separator}`,
        }}>
          <div style={{ fontSize: 24, fontWeight: 700, color: apple.orange }}>
            {Math.round((rules.reduce((sum, r) => sum + (r.dedup_count || 0), 0) / Math.max(1, rules.length)))}
          </div>
          <div style={{ fontSize: 13, color: apple.tertiaryLabel }}>Avg per Rule</div>
        </div>
      </div>

      {/* Rules Table */}
      <div style={{
        background: apple.secondaryBackground,
        borderRadius: apple.radius.lg,
        border: `0.5px solid ${apple.separator}`,
        overflow: 'hidden',
      }}>
        {loading ? (
          <div style={{ padding: 40, textAlign: 'center', color: apple.tertiaryLabel }}>
            Loading deduplication rules...
          </div>
        ) : rules.length === 0 ? (
          <div style={{ padding: 60, textAlign: 'center' }}>
            <Fingerprint style={{ width: 48, height: 48, color: apple.tertiaryLabel, margin: '0 auto 16px' }} />
            <h3 style={{ fontSize: 18, fontWeight: 600, color: apple.label, marginBottom: 8 }}>
              No deduplication rules yet
            </h3>
            <p style={{ fontSize: 14, color: apple.secondaryLabel, marginBottom: 20 }}>
              Create rules to automatically merge duplicate alerts
            </p>
            <button
              onClick={() => setShowModal(true)}
              style={{
                padding: '10px 20px',
                borderRadius: apple.radius.sm,
                border: 'none',
                background: apple.blue,
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
            {rules.map((rule) => (
              <motion.div
                key={rule.id}
                initial={{ opacity: 0 }}
                animate={{ opacity: 1 }}
                style={{
                  padding: 16,
                  background: apple.fill,
                  borderRadius: apple.radius.md,
                  border: `0.5px solid ${apple.separator}`,
                  marginBottom: 12,
                }}
              >
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
                  <div style={{ flex: 1 }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 8 }}>
                      <h4 style={{ fontSize: 16, fontWeight: 600, color: apple.label, margin: 0 }}>
                        {rule.name}
                      </h4>
                      <button
                        onClick={() => toggleRule(rule.id, !rule.enabled)}
                        style={{
                          width: 40,
                          height: 20,
                          borderRadius: 10,
                          background: rule.enabled ? apple.green : apple.tertiaryLabel,
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
                    
                    <p style={{ fontSize: 13, color: apple.secondaryLabel, marginBottom: 12 }}>
                      {rule.description}
                    </p>

                    <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
                      <div style={{
                        display: 'flex',
                        alignItems: 'center',
                        gap: 4,
                        padding: '4px 8px',
                        borderRadius: apple.radius.sm,
                        background: `${apple.purple}15`,
                        fontSize: 12,
                        color: apple.purple,
                      }}>
                        <Fingerprint style={{ width: 12, height: 12 }} />
                        Fields: {rule.fingerprint_fields.join(', ')}
                      </div>
                      <div style={{
                        padding: '4px 8px',
                        borderRadius: apple.radius.sm,
                        background: `${apple.orange}15`,
                        fontSize: 12,
                        color: apple.orange,
                      }}>
                        Window: {rule.time_window / 60}min
                      </div>
                      {rule.dedup_count && rule.dedup_count > 0 && (
                        <div style={{
                          padding: '4px 8px',
                          borderRadius: apple.radius.sm,
                          background: `${apple.green}15`,
                          fontSize: 12,
                          color: apple.green,
                        }}>
                          Merged: {rule.dedup_count} alerts
                        </div>
                      )}
                    </div>
                  </div>

                  <div style={{ display: 'flex', gap: 8 }}>
                    <button
                      onClick={() => {
                        setEditingRule(rule)
                        setShowModal(true)
                      }}
                      style={{
                        padding: 8,
                        borderRadius: apple.radius.sm,
                        border: 'none',
                        background: apple.fill,
                        color: apple.blue,
                        cursor: 'pointer',
                      }}
                    >
                      <Edit style={{ width: 16, height: 16 }} />
                    </button>
                    <button
                      onClick={() => deleteRule(rule.id)}
                      style={{
                        padding: 8,
                        borderRadius: apple.radius.sm,
                        border: 'none',
                        background: `${apple.red}15`,
                        color: apple.red,
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

      {/* Create/Edit Rule Modal */}
      {showModal && (
        <DeduplicationRuleModal
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

// Rule Creation/Edit Modal
function DeduplicationRuleModal({
  rule,
  onClose,
  onSaved,
}: {
  rule: DeduplicationRule | null
  onClose: () => void
  onSaved: () => void
}) {
  const [name, setName] = useState(rule?.name || '')
  const [description, setDescription] = useState(rule?.description || '')
  const [fields, setFields] = useState<string[]>(rule?.fingerprint_fields || [])
  const [newField, setNewField] = useState('')
  const [timeWindow, setTimeWindow] = useState(String((rule?.time_window || 3600) / 60))
  const [mergeStrategy, setMergeStrategy] = useState(rule?.merge_strategy || 'first')
  const [saving, setSaving] = useState(false)

  const addField = () => {
    if (newField && !fields.includes(newField)) {
      setFields([...fields, newField])
      setNewField('')
    }
  }

  const removeField = (field: string) => {
    setFields(fields.filter(f => f !== field))
  }

  const handleSave = async () => {
    setSaving(true)
    try {
      const url = rule 
        ? `/api/v1/deduplication/rules/${rule.id}`
        : '/api/v1/deduplication/rules'
      
      const response = await fetch(url, {
        method: rule ? 'PUT' : 'POST',
        headers: {
          'Content-Type': 'application/json',
          'Authorization': `Bearer ${sessionStorage.getItem('access_token') || localStorage.getItem('access_token')}`,
        },
        body: JSON.stringify({
          name,
          description,
          fingerprint_fields: fields,
          time_window: parseInt(timeWindow) * 60,
          merge_strategy: mergeStrategy,
          enabled: true,
        }),
      })

      if (response.ok) {
        onSaved()
        onClose()
      }
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
          background: apple.secondaryBackground,
          borderRadius: apple.radius.lg,
          padding: 24,
          width: '90%',
          maxWidth: 600,
          maxHeight: '90vh',
          overflowY: 'auto',
        }}
      >
        <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 20 }}>
          <h3 style={{ fontSize: 20, fontWeight: 600, color: apple.label, margin: 0 }}>
            {rule ? 'Edit' : 'Create'} Deduplication Rule
          </h3>
          <button onClick={onClose} style={{ background: 'none', border: 'none', cursor: 'pointer', padding: 4 }}>
            <X style={{ width: 20, height: 20, color: apple.secondaryLabel }} />
          </button>
        </div>

        {/* Form */}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          <div>
            <label style={{ display: 'block', fontSize: 13, fontWeight: 500, color: apple.secondaryLabel, marginBottom: 8 }}>
              Rule Name
            </label>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g., Merge by service and environment"
              style={{
                width: '100%',
                height: 40,
                borderRadius: apple.radius.sm,
                border: `0.5px solid ${apple.separator}`,
                background: apple.fill,
                padding: '0 12px',
                fontSize: 14,
                color: apple.label,
                outline: 'none',
              }}
            />
          </div>

          <div>
            <label style={{ display: 'block', fontSize: 13, fontWeight: 500, color: apple.secondaryLabel, marginBottom: 8 }}>
              Description
            </label>
            <textarea
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Describe when this rule should deduplicate alerts..."
              rows={2}
              style={{
                width: '100%',
                borderRadius: apple.radius.sm,
                border: `0.5px solid ${apple.separator}`,
                background: apple.fill,
                padding: 12,
                fontSize: 14,
                color: apple.label,
                outline: 'none',
                resize: 'vertical',
              }}
            />
          </div>

          <div>
            <label style={{ display: 'block', fontSize: 13, fontWeight: 500, color: apple.secondaryLabel, marginBottom: 8 }}>
              Fingerprint Fields
            </label>
            <div style={{ display: 'flex', gap: 8, marginBottom: 8 }}>
              <input
                type="text"
                value={newField}
                onChange={(e) => setNewField(e.target.value)}
                onKeyPress={(e) => e.key === 'Enter' && addField()}
                placeholder="Field name (e.g., service, environment)"
                style={{
                  flex: 1,
                  height: 36,
                  borderRadius: apple.radius.sm,
                  border: `0.5px solid ${apple.separator}`,
                  background: apple.fill,
                  padding: '0 12px',
                  fontSize: 13,
                  color: apple.label,
                  outline: 'none',
                }}
              />
              <button
                onClick={addField}
                style={{
                  padding: '0 16px',
                  borderRadius: apple.radius.sm,
                  border: 'none',
                  background: apple.blue,
                  color: '#fff',
                  fontSize: 13,
                  fontWeight: 500,
                  cursor: 'pointer',
                }}
              >
                Add
              </button>
            </div>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
              {fields.map((field) => (
                <div
                  key={field}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 6,
                    padding: '4px 8px 4px 12px',
                    borderRadius: 12,
                    background: `${apple.purple}15`,
                    border: `0.5px solid ${apple.purple}30`,
                  }}
                >
                  <span style={{ fontSize: 12, color: apple.purple, fontWeight: 500 }}>{field}</span>
                  <button
                    onClick={() => removeField(field)}
                    style={{
                      background: 'none',
                      border: 'none',
                      cursor: 'pointer',
                      padding: 2,
                      display: 'flex',
                    }}
                  >
                    <X style={{ width: 12, height: 12, color: apple.purple }} />
                  </button>
                </div>
              ))}
            </div>
            <p style={{ fontSize: 11, color: apple.tertiaryLabel, marginTop: 6 }}>
              Alerts with matching values for these fields will be merged
            </p>
          </div>

          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
            <div>
              <label style={{ display: 'block', fontSize: 13, fontWeight: 500, color: apple.secondaryLabel, marginBottom: 8 }}>
                Time Window
              </label>
              <select
                value={timeWindow}
                onChange={(e) => setTimeWindow(e.target.value)}
                style={{
                  width: '100%',
                  height: 40,
                  borderRadius: apple.radius.sm,
                  border: `0.5px solid ${apple.separator}`,
                  background: apple.fill,
                  padding: '0 12px',
                  fontSize: 14,
                  color: apple.label,
                  outline: 'none',
                }}
              >
                <option value="5">5 minutes</option>
                <option value="15">15 minutes</option>
                <option value="30">30 minutes</option>
                <option value="60">1 hour</option>
                <option value="360">6 hours</option>
                <option value="1440">24 hours</option>
              </select>
            </div>

            <div>
              <label style={{ display: 'block', fontSize: 13, fontWeight: 500, color: apple.secondaryLabel, marginBottom: 8 }}>
                Merge Strategy
              </label>
              <select
                value={mergeStrategy}
                onChange={(e) => setMergeStrategy(e.target.value as any)}
                style={{
                  width: '100%',
                  height: 40,
                  borderRadius: apple.radius.sm,
                  border: `0.5px solid ${apple.separator}`,
                  background: apple.fill,
                  padding: '0 12px',
                  fontSize: 14,
                  color: apple.label,
                  outline: 'none',
                }}
              >
                <option value="first">Keep first alert</option>
                <option value="last">Keep latest alert</option>
                <option value="concatenate">Concatenate details</option>
              </select>
            </div>
          </div>

          <div style={{ display: 'flex', gap: 8, marginTop: 8 }}>
            <button
              onClick={onClose}
              style={{
                flex: 1,
                padding: '10px 16px',
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
              onClick={handleSave}
              disabled={!name || fields.length === 0 || saving}
              style={{
                flex: 1,
                padding: '10px 16px',
                borderRadius: apple.radius.sm,
                border: 'none',
                background: apple.blue,
                color: '#fff',
                fontSize: 14,
                fontWeight: 500,
                cursor: (!name || fields.length === 0 || saving) ? 'default' : 'pointer',
                opacity: (!name || fields.length === 0 || saving) ? 0.5 : 1,
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
