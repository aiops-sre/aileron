import { useEffect } from 'react'
import { useSettingsStore } from '../stores/settingsStore'

type Theme = 'dark' | 'light' | 'auto'
type ResolvedTheme = 'dark' | 'light'

export function useTheme() {
  const { settings, updateSettings } = useSettingsStore()
  const theme = settings.theme

  // Resolve 'auto' theme based on system preference
  const getResolvedTheme = (): ResolvedTheme => {
    if (theme === 'auto') {
      return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
    }
    return theme
  }

  useEffect(() => {
    const root = window.document.documentElement
    const resolvedTheme = getResolvedTheme()
    
    // Remove both classes first
    root.classList.remove('light', 'dark')
    // Add the resolved theme class
    root.classList.add(resolvedTheme)

    // Listen for system theme changes when in auto mode
    const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)')
    const handleChange = (e: MediaQueryListEvent) => {
      if (theme === 'auto') {
        root.classList.remove('light', 'dark')
        root.classList.add(e.matches ? 'dark' : 'light')
      }
    }

    mediaQuery.addEventListener('change', handleChange)
    return () => mediaQuery.removeEventListener('change', handleChange)
  }, [theme])

  const setTheme = (newTheme: Theme) => {
    updateSettings({ theme: newTheme })
  }

  const toggleTheme = () => {
    const newTheme = theme === 'dark' ? 'light' : theme === 'light' ? 'auto' : 'dark'
    setTheme(newTheme)
  }

  return {
    theme,
    resolvedTheme: getResolvedTheme(),
    setTheme,
    toggleTheme
  }
}