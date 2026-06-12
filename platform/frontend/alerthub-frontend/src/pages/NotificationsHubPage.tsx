import React, { useState, useEffect } from 'react'
import { motion, AnimatePresence } from 'framer-motion'
import {
  Bell,
  BellOff,
  Check,
  Trash2,
  Filter,
  AlertCircle,
  CheckCircle,
  XCircle,
  Info,
  ExternalLink,
} from 'lucide-react'

const apple = {
  blue: '#007AFF',
  green: '#34C759',
  red: '#FF3B30',
  orange: '#FF9500',
  purple: '#AF52DE',
  gray: '#8E8E93',
  label: 'var(--color-text)',
  secondaryLabel: 'var(--color-text-secondary)',
  tertiaryLabel: 'var(--color-text-tertiary, #8E8E93)',
  separator: 'var(--color-separator, rgba(142, 142, 147, 0.12))',
  fill: 'var(--color-fill, rgba(142, 142, 147, 0.08))',
  secondaryBackground: 'var(--color-card, rgba(255, 255, 255, 0.8))',
  radius: { sm: 6, md: 10, lg: 12 },
}

interface Notification {
  id: string
  type: 'alert' | 'incident' | 'system' | 'workflow'
  title: string
  message: string
  read: boolean
  severity: 'info' | 'warning' | 'error' | 'success'
  link?: string
  created_at: string
  metadata?: Record<string, any>
}

export function NotificationsHubPage() {
  const [notifications, setNotifications] = useState<Notification[]>([])
  const [filter, setFilter] = useState<'all' | 'unread' | 'read'>('all')
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    loadNotifications()
    // Poll for new notifications
    const interval = setInterval(loadNotifications, 30000)
    return () => clearInterval(interval)
  }, [])

  const loadNotifications = async () => {
    setLoading(true)
    try {
      const response = await fetch('/api/v1/notifications/recent', {
        headers: {
          'Authorization': `Bearer ${sessionStorage.getItem('access_token') || localStorage.getItem('access_token')}`,
        },
      })
      const data = await response.json()
      setNotifications(data.data || data.notifications || data.data?.notifications || [])
    } catch (error) {
      console.error('Failed to load notifications:', error)
    } finally {
      setLoading(false)
    }
  }

  const markAsRead = async (notificationId: string) => {
    try {
      await fetch(`/api/v1/notifications/${notificationId}/read`, {
        method: 'POST',
        headers: {
          'Authorization': `Bearer ${sessionStorage.getItem('access_token') || localStorage.getItem('access_token')}`,
        },
      })
      setNotifications(notifications.map(n => 
        n.id === notificationId ? { ...n, read: true } : n
      ))
    } catch (error) {
      console.error('Failed to mark as read:', error)
    }
  }

  const markAllAsRead = async () => {
    try {
      await fetch('/api/v1/notifications/read-all', {
        method: 'POST',
        headers: {
          'Authorization': `Bearer ${sessionStorage.getItem('access_token') || localStorage.getItem('access_token')}`,
        },
      })
      setNotifications(notifications.map(n => ({ ...n, read: true })))
    } catch (error) {
      alert('Failed to mark all as read')
    }
  }

  const deleteNotification = async (notificationId: string) => {
    try {
      await fetch(`/api/v1/notifications/${notificationId}`, {
        method: 'DELETE',
        headers: {
          'Authorization': `Bearer ${sessionStorage.getItem('access_token') || localStorage.getItem('access_token')}`,
        },
      })
      setNotifications(notifications.filter(n => n.id !== notificationId))
    } catch (error) {
      alert('Failed to delete notification')
    }
  }

  const filteredNotifications = notifications.filter(n => {
    if (filter === 'unread') return !n.read
    if (filter === 'read') return n.read
    return true
  })

  const unreadCount = notifications.filter(n => !n.read).length

  const getSeverityIcon = (severity: string) => {
    switch (severity) {
      case 'error': return <XCircle style={{ width: 16, height: 16, color: apple.red }} />
      case 'warning': return <AlertCircle style={{ width: 16, height: 16, color: apple.orange }} />
      case 'success': return <CheckCircle style={{ width: 16, height: 16, color: apple.green }} />
      default: return <Info style={{ width: 16, height: 16, color: apple.blue }} />
    }
  }

  const getSeverityColor = (severity: string) => {
    switch (severity) {
      case 'error': return apple.red
      case 'warning': return apple.orange
      case 'success': return apple.green
      default: return apple.blue
    }
  }

  return (
    <div style={{ padding: 24, maxWidth: 1200, margin: '0 auto' }}>
      {/* Header */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 24 }}>
        <div>
          <h1 style={{ fontSize: 28, fontWeight: 600, color: apple.label, marginBottom: 8 }}>
            Notifications
          </h1>
          <p style={{ fontSize: 15, color: apple.secondaryLabel }}>
            {unreadCount} unread notification{unreadCount !== 1 ? 's' : ''}
          </p>
        </div>
        {unreadCount > 0 && (
          <button
            onClick={markAllAsRead}
            style={{
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
            Mark All as Read
          </button>
        )}
      </div>

      {/* Filter Tabs */}
      <div style={{ display: 'flex', gap: 8, marginBottom: 24, borderBottom: `0.5px solid ${apple.separator}`, paddingBottom: 12 }}>
        {(['all', 'unread', 'read'] as const).map((f) => (
          <button
            key={f}
            onClick={() => setFilter(f)}
            style={{
              padding: '8px 16px',
              borderRadius: apple.radius.sm,
              border: 'none',
              background: filter === f ? apple.blue : 'transparent',
              color: filter === f ? '#fff' : apple.label,
              fontSize: 14,
              fontWeight: filter === f ? 600 : 400,
              cursor: 'pointer',
              transition: 'all 0.2s',
            }}
          >
            {f.charAt(0).toUpperCase() + f.slice(1)}
            {f === 'unread' && unreadCount > 0 && (
              <span style={{
                marginLeft: 6,
                padding: '2px 6px',
                borderRadius: 10,
                background: filter === f ? 'rgba(255,255,255,0.3)' : `${apple.red}15`,
                fontSize: 11,
                fontWeight: 600,
              }}>
                {unreadCount}
              </span>
            )}
          </button>
        ))}
      </div>

      {/* Notifications List */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        <AnimatePresence>
          {loading ? (
            <div style={{ padding: 40, textAlign: 'center', color: apple.tertiaryLabel }}>
              Loading notifications...
            </div>
          ) : filteredNotifications.length === 0 ? (
            <div style={{ padding: 60, textAlign: 'center' }}>
              <Bell style={{ width: 48, height: 48, color: apple.tertiaryLabel, margin: '0 auto 16px' }} />
              <h3 style={{ fontSize: 18, fontWeight: 600, color: apple.label, marginBottom: 8 }}>
                No notifications
              </h3>
              <p style={{ fontSize: 14, color: apple.secondaryLabel }}>
                {filter === 'unread' ? 'All caught up!' : 'No notifications to show'}
              </p>
            </div>
          ) : (
            filteredNotifications.map((notification) => (
              <motion.div
                key={notification.id}
                initial={{ opacity: 0, x: -10 }}
                animate={{ opacity: 1, x: 0 }}
                exit={{ opacity: 0, x: 10 }}
                style={{
                  padding: 16,
                  background: notification.read ? apple.fill : apple.secondaryBackground,
                  borderRadius: apple.radius.md,
                  border: `0.5px solid ${notification.read ? apple.separator : getSeverityColor(notification.severity) + '30'}`,
                  borderLeft: `3px solid ${getSeverityColor(notification.severity)}`,
                  cursor: 'pointer',
                  transition: 'all 0.2s',
                }}
                onClick={() => !notification.read && markAsRead(notification.id)}
              >
                <div style={{ display: 'flex', gap: 12 }}>
                  <div style={{
                    width: 40,
                    height: 40,
                    borderRadius: apple.radius.sm,
                    background: `${getSeverityColor(notification.severity)}15`,
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    flexShrink: 0,
                  }}>
                    {getSeverityIcon(notification.severity)}
                  </div>

                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 4 }}>
                      <h4 style={{
                        fontSize: 14,
                        fontWeight: notification.read ? 400 : 600,
                        color: apple.label,
                        margin: 0,
                      }}>
                        {notification.title}
                      </h4>
                      <span style={{ fontSize: 12, color: apple.tertiaryLabel, whiteSpace: 'nowrap' }}>
                        {new Date(notification.created_at).toLocaleTimeString([], { 
                          hour: '2-digit', 
                          minute: '2-digit' 
                        })}
                      </span>
                    </div>

                    <p style={{
                      fontSize: 13,
                      color: apple.secondaryLabel,
                      marginBottom: 8,
                      lineHeight: 1.4,
                    }}>
                      {notification.message}
                    </p>

                    <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                      <span style={{
                        padding: '2px 8px',
                        borderRadius: 10,
                        background: `${apple.gray}15`,
                        fontSize: 11,
                        color: apple.gray,
                        textTransform: 'uppercase',
                        fontWeight: 600,
                      }}>
                        {notification.type}
                      </span>

                      {notification.link && (
                        <a
                          href={notification.link}
                          onClick={(e) => e.stopPropagation()}
                          style={{
                            display: 'flex',
                            alignItems: 'center',
                            gap: 4,
                            fontSize: 12,
                            color: apple.blue,
                            textDecoration: 'none',
                            fontWeight: 500,
                          }}
                        >
                          View Details
                          <ExternalLink style={{ width: 12, height: 12 }} />
                        </a>
                      )}
                    </div>
                  </div>

                  <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                    {!notification.read && (
                      <button
                        onClick={(e) => {
                          e.stopPropagation()
                          markAsRead(notification.id)
                        }}
                        style={{
                          padding: 6,
                          borderRadius: apple.radius.sm,
                          border: 'none',
                          background: `${apple.green}15`,
                          color: apple.green,
                          cursor: 'pointer',
                        }}
                        title="Mark as read"
                      >
                        <Check style={{ width: 14, height: 14 }} />
                      </button>
                    )}
                    <button
                      onClick={(e) => {
                        e.stopPropagation()
                        deleteNotification(notification.id)
                      }}
                      style={{
                        padding: 6,
                        borderRadius: apple.radius.sm,
                        border: 'none',
                        background: `${apple.red}15`,
                        color: apple.red,
                        cursor: 'pointer',
                      }}
                      title="Delete"
                    >
                      <Trash2 style={{ width: 14, height: 14 }} />
                    </button>
                  </div>
                </div>
              </motion.div>
            ))
          )}
        </AnimatePresence>
      </div>
    </div>
  )
}
