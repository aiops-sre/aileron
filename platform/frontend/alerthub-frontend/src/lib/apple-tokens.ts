// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Apple Design Tokens - Dark Mode Compatible
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

export const apple = {
  // System colors (fixed values, don't change with theme)
  blue: '#007AFF',
  green: '#34C759',
  red: '#FF3B30',
  orange: '#FF9500',
  yellow: '#FFCC00',
  purple: '#AF52DE',
  pink: '#FF2D55',
  teal: '#5AC8FA',
  indigo: '#5856D6',
  gray: '#8E8E93',
  gray2: '#636366',
  gray3: '#48484A',
  gray4: '#3A3A3C',
  gray5: '#2C2C2E',
  gray6: '#1C1C1E',

  // Semantic colors (use CSS variables that change with theme - NO FALLBACKS)
  label: 'var(--color-text)',
  secondaryLabel: 'var(--color-text-secondary)',
  tertiaryLabel: 'var(--color-text-tertiary)',
  quaternaryLabel: 'var(--color-text-quaternary)',
  separator: 'var(--color-separator)',
  fill: 'var(--color-fill)',
  secondaryFill: 'var(--color-secondary-fill)',
  tertiaryFill: 'var(--color-tertiary-fill)',
  background: 'var(--color-background)',
  secondaryBackground: 'var(--color-card)',
  groupedBackground: 'var(--color-grouped-background)',

  // Border radius (Apple uses specific values)
  radius: {
    sm: 6,
    md: 10,
    lg: 12,
    xl: 16,
    '2xl': 20,
  },
} as const

export default apple