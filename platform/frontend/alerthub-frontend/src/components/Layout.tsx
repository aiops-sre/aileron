import React, { useState, useEffect } from 'react'
import { motion } from 'framer-motion'
import { useLocation } from 'react-router-dom'
import { Header } from './Header'
import AIChatAssistant from './ai/ChatAssistant'
import { FloatingActionButtons } from './FloatingActionButtons'
import { useAlertsStore, startBackgroundRefresh, stopBackgroundRefresh } from '@/stores/alertsStore'
import { initializeEnhancedDataLoading, cleanupEnhancedDataLoading } from '@/stores/enhancedUniversalDataStore'

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Apple Design Tokens
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

const apple = {
  background: 'var(--color-background)',
} as const

interface LayoutProps {
  children: React.ReactNode
  className?: string
  onCallCount?: number
}

export function Layout({ children, className, onCallCount = 0 }: LayoutProps) {
  const [isAIChatOpen, setIsAIChatOpen] = useState(false)
  const location = useLocation()
  const loadAlerts = useAlertsStore((state) => state.loadAlerts)
  
  // Check if we're on the AI chat page to hide floating action button
  const isOnAIChatPage = location.pathname === '/ai-chat'

  // Initialize universal background data loading when layout mounts
  useEffect(() => {
    const initializeBackgroundSystems = async () => {
      // Start loading alerts immediately (not silently for initial load)
      loadAlerts(false)
      
      // Start alert-specific background refresh
      startBackgroundRefresh()
      
      // Initialize enhanced universal data loading for all pages
      await initializeEnhancedDataLoading()
      
      console.log('✅ All background data loading systems initialized')
    }
    
    initializeBackgroundSystems()
    
    // Cleanup on unmount
    return () => {
      stopBackgroundRefresh()
      cleanupEnhancedDataLoading()
    }
  }, [loadAlerts])

  // Keyboard shortcut for AI assistant (⌘/ or Ctrl/)
  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key === '/') {
        event.preventDefault()
        setIsAIChatOpen(true)
      }
      
      // ESC to close AI chat
      if (event.key === 'Escape' && isAIChatOpen) {
        setIsAIChatOpen(false)
      }
    }

    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [isAIChatOpen])

  return (
    <div style={{
      minHeight: '100vh',
      background: apple.background,
      display: 'flex',
      flexDirection: 'column',
    }}>
      <Header />
      
      <motion.main
        initial={{ opacity: 0, y: 8 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.3, ease: 'easeOut' }}
        style={{
          flex: 1,
          display: 'flex',
          flexDirection: 'column',
          position: 'relative',
        }}
        className={className}
      >
        {children}
      </motion.main>
      
      {/* Floating Action Buttons */}
      <FloatingActionButtons
        onCallCount={onCallCount}
        onAIChatClick={() => setIsAIChatOpen(true)}
        isAIChatOpen={isAIChatOpen}
      />
      
      {/* AI Chat Assistant Modal */}
      <AIChatAssistant
        isOpen={isAIChatOpen}
        onClose={() => setIsAIChatOpen(false)}
      />
    </div>
  )
}