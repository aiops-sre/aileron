import { useEffect } from 'react'

interface KeyboardShortcut {
  key: string
  ctrl?: boolean
  meta?: boolean
  shift?: boolean
  callback: () => void
}

export function useKeyboard(shortcuts: KeyboardShortcut[]) {
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      shortcuts.forEach(({ key, ctrl, meta, shift, callback }) => {
        const keyMatch = e.key.toLowerCase() === key.toLowerCase()
        const ctrlMatch = ctrl ? e.ctrlKey : !e.ctrlKey
        const metaMatch = meta ? e.metaKey : !e.metaKey
        const shiftMatch = shift ? e.shiftKey : !e.shiftKey

        if (keyMatch && ctrlMatch && metaMatch && shiftMatch) {
          // Don't trigger if user is typing in an input
          if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) {
            return
          }
          
          e.preventDefault()
          callback()
        }
      })
    }

    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [shortcuts])
}

// Global keyboard shortcuts
export const SHORTCUTS = {
  COMMAND_PALETTE: { key: 'k', meta: true },
  REFRESH: { key: 'r', meta: true },
  NEW_ALERT: { key: 'n', meta: true },
  SEARCH: { key: 'f', meta: true },
  TOGGLE_SIDEBAR: { key: 'b', meta: true },
  HELP: { key: '/', meta: false },
}
