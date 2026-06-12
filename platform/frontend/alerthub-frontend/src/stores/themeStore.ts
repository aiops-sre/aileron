import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import type { Theme } from '@/types';

interface ThemeState {
  theme: Theme;
  isDarkMode: boolean;
  
  // Actions
  setTheme: (theme: Theme) => void;
  toggleTheme: () => void;
}

export const useThemeStore = create<ThemeState>()(
  persist(
    (set, get) => ({
      theme: 'system',
      isDarkMode: false,
      
      setTheme: (theme: Theme) => {
        set({ theme });
        updateDarkMode(theme);
      },
      
      toggleTheme: () => {
        const { theme } = get();
        const newTheme = theme === 'light' ? 'dark' : theme === 'dark' ? 'system' : 'light';
        set({ theme: newTheme });
        updateDarkMode(newTheme);
      },
    }),
    {
      name: 'theme-storage',
    }
  )
);

function updateDarkMode(theme: Theme) {
  const root = document.documentElement;
  
  if (theme === 'dark') {
    root.classList.add('dark');
    useThemeStore.setState({ isDarkMode: true });
  } else if (theme === 'light') {
    root.classList.remove('dark');
    useThemeStore.setState({ isDarkMode: false });
  } else {
    // System theme
    const systemDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
    if (systemDark) {
      root.classList.add('dark');
      useThemeStore.setState({ isDarkMode: true });
    } else {
      root.classList.remove('dark');
      useThemeStore.setState({ isDarkMode: false });
    }
  }
}

// Initialize theme on first load
if (typeof window !== 'undefined') {
  const { theme } = useThemeStore.getState();
  updateDarkMode(theme);
  
  // Listen for system theme changes
  window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
    const { theme } = useThemeStore.getState();
    if (theme === 'system') {
      updateDarkMode(theme);
    }
  });
}