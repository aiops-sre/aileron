import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import api from '../lib/api-axios'

export interface UserSettings {
  // Appearance
  theme: 'light' | 'dark' | 'auto'
  accentColor: string
  compactMode: boolean
  
  // Timezone
  timezone: string
  use24HourTime: boolean
  
  // Notifications
  soundEnabled: boolean
  desktopNotifications: boolean
  emailNotifications: boolean
  notifyOnCritical: boolean
  notifyOnHigh: boolean
  notifyOnAssignment: boolean
  
  // Display
  alertsPerPage: number
  showResolvedAlerts: boolean
  autoRefreshInterval: number // in seconds
  showMetadata: boolean
  
  // Advanced
  enableAIAssistant: boolean
  enableKeyboardShortcuts: boolean
  debugMode: boolean
}

interface SettingsStore {
  settings: UserSettings
  isLoading: boolean
  isSyncing: boolean
  lastSynced: string | null
  updateSettings: (settings: Partial<UserSettings>) => Promise<void>
  resetSettings: () => Promise<void>
  loadSettingsFromDB: () => Promise<void>
  syncSettings: () => Promise<void>
}

const defaultSettings: UserSettings = {
  theme: 'auto',
  accentColor: '#0071e3',
  compactMode: false,
  timezone: 'America/New_York',
  use24HourTime: false,
  soundEnabled: true,
  desktopNotifications: true,
  emailNotifications: false,
  notifyOnCritical: true,
  notifyOnHigh: true,
  notifyOnAssignment: true,
  alertsPerPage: 50,
  autoRefreshInterval: 30,
  showResolvedAlerts: false,
  showMetadata: true,
  enableAIAssistant: true,
  enableKeyboardShortcuts: true,
  debugMode: false,
};

export const useSettingsStore = create<SettingsStore>()(
  persist(
    (set, get) => ({
      settings: defaultSettings,
      isLoading: false,
      isSyncing: false,
      lastSynced: null,

      // Load settings from database
      loadSettingsFromDB: async () => {
        try {
          set({ isLoading: true })
          const response = await api.get('/users/settings')
          const dbSettings = response.data?.data?.settings

          if (dbSettings) {
            set({
              settings: { ...defaultSettings, ...dbSettings },
              lastSynced: new Date().toISOString(),
              isLoading: false,
            })
          } else {
            set({ isLoading: false })
          }
        } catch (error) {
          console.error('Failed to load settings from DB:', error)
          set({ isLoading: false })
          // Fall back to localStorage settings
        }
      },

      // Update settings locally and sync to database
      updateSettings: async (newSettings) => {
        const updatedSettings = { ...get().settings, ...newSettings }
        
        // Update local state immediately for responsive UI
        set({ settings: updatedSettings, isSyncing: true })

        try {
          // Sync to database in background
          await api.put('/users/settings', { settings: updatedSettings })
          set({ isSyncing: false, lastSynced: new Date().toISOString() })
        } catch (error) {
          console.error('Failed to sync settings to DB:', error)
          set({ isSyncing: false })
          // Settings remain in localStorage even if sync fails
        }
      },

      // Reset to defaults and sync
      resetSettings: async () => {
        set({ settings: defaultSettings, isSyncing: true })

        try {
          await api.put('/users/settings', { settings: defaultSettings })
          set({ isSyncing: false, lastSynced: new Date().toISOString() })
        } catch (error) {
          console.error('Failed to reset settings in DB:', error)
          set({ isSyncing: false })
        }
      },

      // Manual sync to database
      syncSettings: async () => {
        const currentSettings = get().settings
        set({ isSyncing: true })

        try {
          await api.put('/users/settings', { settings: currentSettings })
          set({ isSyncing: false, lastSynced: new Date().toISOString() })
        } catch (error) {
          console.error('Failed to sync settings:', error)
          set({ isSyncing: false })
        }
      },
    }),
    {
      name: 'user-settings',
      partialize: (state) => ({ settings: state.settings, lastSynced: state.lastSynced }),
    }
  )
);
