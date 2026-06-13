import React, { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { motion, AnimatePresence } from 'framer-motion'
import {
  Plus,
  X,
  UserCheck,
  Sparkles,
  MessageCircle,
  Zap,
} from 'lucide-react'

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Aileron Design Tokens
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

const tokens = {
  blue: '#007AFF',
  green: '#34C759',
  red: '#FF3B30',
  orange: '#FF9500',
  purple: '#AF52DE',
  radius: { sm: 6, md: 10, lg: 12, xl: 16, '2xl': 20 },
} as const

interface FloatingActionButtonsProps {
  onCallCount?: number
  onAIChatClick?: () => void
  isAIChatOpen?: boolean
}

export function FloatingActionButtons({ 
  onCallCount = 0, 
  onAIChatClick, 
  isAIChatOpen = false 
}: FloatingActionButtonsProps) {
  const navigate = useNavigate()
  const [isExpanded, setIsExpanded] = useState(false)

  // Hide FAB when AI chat is open to prevent overlap
  if (isAIChatOpen) {
    return null
  }

  const fabVariants = {
    hidden: { scale: 0, opacity: 0 },
    visible: { 
      scale: 1, 
      opacity: 1,
      transition: { 
        type: 'spring',
        stiffness: 300,
        damping: 20,
        duration: 0.3,
      }
    },
    exit: { 
      scale: 0, 
      opacity: 0,
      transition: { duration: 0.2 }
    }
  }

  const buttonVariants = {
    hidden: { 
      scale: 0, 
      opacity: 0,
      y: 20
    },
    visible: (i: number) => ({
      scale: 1,
      opacity: 1,
      y: 0,
      transition: {
        delay: i * 0.08,
        type: 'spring',
        stiffness: 300,
        damping: 20
      }
    }),
    exit: {
      scale: 0,
      opacity: 0,
      y: 20,
      transition: { duration: 0.15 }
    }
  }

  const actionButtons = [
    {
      id: 'oncall',
      icon: UserCheck,
      gradient: `linear-gradient(135deg, ${tokens.green}, #30d158)`,
      shadow: `0 8px 24px ${tokens.green}40`,
      title: 'On-Call Schedule',
      onClick: () => {
        navigate('/oncall')
        setIsExpanded(false)
      },
      badge: onCallCount,
    },
    {
      id: 'ai-chat',
      icon: Sparkles,
      gradient: `linear-gradient(135deg, ${tokens.purple}, #bf5af2)`,
      shadow: `0 8px 24px ${tokens.purple}40`,
      title: 'AI Assistant',
      onClick: () => {
        if (onAIChatClick) {
          onAIChatClick()
        } else {
          navigate('/ai-chat')
        }
        setIsExpanded(false)
      },
    },
  ]

  return (
    <div style={{
      position: 'fixed',
      bottom: 24,
      right: 24,
      zIndex: 40,
      display: 'flex',
      flexDirection: 'column',
      alignItems: 'flex-end',
      gap: 12,
    }}>
      <AnimatePresence>
        {/* Action Buttons */}
        {isExpanded && actionButtons.map((button, index) => (
          <motion.div
            key={button.id}
            custom={index}
            variants={buttonVariants}
            initial="hidden"
            animate="visible"
            exit="exit"
          >
            <motion.button
              whileHover={{ scale: 1.1 }}
              whileTap={{ scale: 0.95 }}
              onClick={button.onClick}
              style={{
                position: 'relative',
                width: 56,
                height: 56,
                borderRadius: '50%',
                border: 'none',
                background: button.gradient,
                boxShadow: button.shadow,
                cursor: 'pointer',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                transition: 'all 0.2s cubic-bezier(0.4, 0, 0.2, 1)',
              }}
              title={button.title}
              onMouseEnter={(e) => {
                e.currentTarget.style.transform = 'scale(1.1)'
              }}
              onMouseLeave={(e) => {
                e.currentTarget.style.transform = 'scale(1)'
              }}
            >
              <button.icon style={{ width: 22, height: 22, color: '#fff' }} />
              
              {/* Badge for on-call count */}
              {button.badge && button.badge > 0 && (
                <motion.div
                  initial={{ scale: 0 }}
                  animate={{ scale: 1 }}
                  style={{
                    position: 'absolute',
                    top: -4,
                    right: -4,
                    width: 24,
                    height: 24,
                    borderRadius: '50%',
                    background: tokens.red,
                    border: `2px solid var(--color-background)`,
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    fontSize: 11,
                    fontWeight: 700,
                    color: '#fff',
                    boxShadow: `0 4px 12px ${tokens.red}40`,
                  }}
                >
                  {button.badge}
                </motion.div>
              )}
            </motion.button>
          </motion.div>
        ))}
      </AnimatePresence>

      {/* Main FAB */}
      <motion.button
        variants={fabVariants}
        initial="hidden"
        animate="visible"
        whileHover={{ scale: 1.05 }}
        whileTap={{ scale: 0.95 }}
        onClick={() => setIsExpanded(!isExpanded)}
        style={{
          width: 64,
          height: 64,
          borderRadius: '50%',
          border: 'none',
          background: isExpanded
            ? `linear-gradient(135deg, ${tokens.red}, #ff453a)`
            : `linear-gradient(135deg, ${tokens.blue}, #00c3ff)`,
          boxShadow: isExpanded
            ? `0 12px 40px ${tokens.red}50`
            : `0 12px 40px ${tokens.blue}50`,
          cursor: 'pointer',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          transition: 'all 0.3s cubic-bezier(0.4, 0, 0.2, 1)',
        }}
        title={isExpanded ? 'Close' : 'Quick Actions'}
      >
        <motion.div
          animate={{ rotate: isExpanded ? 45 : 0 }}
          transition={{ duration: 0.3, ease: 'easeInOut' }}
        >
          {isExpanded ? (
            <X style={{ width: 24, height: 24, color: '#fff' }} />
          ) : (
            <Plus style={{ width: 24, height: 24, color: '#fff' }} />
          )}
        </motion.div>
      </motion.button>

      {/* Backdrop overlay when expanded */}
      <AnimatePresence>
        {isExpanded && (
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.2 }}
            onClick={() => setIsExpanded(false)}
            style={{
              position: 'fixed',
              inset: 0,
              background: 'rgba(0, 0, 0, 0.2)',
              backdropFilter: 'blur(4px)',
              WebkitBackdropFilter: 'blur(4px)',
              zIndex: -1,
            }}
          />
        )}
      </AnimatePresence>
    </div>
  )
}
