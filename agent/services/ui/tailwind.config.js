/** @type {import('tailwindcss').Config} */
export default {
  darkMode: 'class',
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        // KubeSense dark palette
        surface:  { DEFAULT: '#0d1117', raised: '#161b22', overlay: '#1c2128', border: '#30363d' },
        brand:    { DEFAULT: '#4a9eff', dim: '#1d4a8c', glow: 'rgba(74,158,255,0.15)' },
        ok:       { DEFAULT: '#3fb950', dim: '#1a4023' },
        warn:     { DEFAULT: '#d29922', dim: '#4a3510' },
        danger:   { DEFAULT: '#f85149', dim: '#4a1a19' },
        critical: { DEFAULT: '#ff6b35', dim: '#4a2210' },
        purple:   { DEFAULT: '#a855f7', dim: '#2d1b4e' },
        teal:     { DEFAULT: '#00cec9', dim: '#003d3d' },
      },
      fontFamily: {
        mono: ['JetBrains Mono', 'Fira Code', 'Cascadia Code', 'monospace'],
        sans: ['Inter', 'system-ui', 'sans-serif'],
      },
      animation: {
        'fade-in':    'fadeIn 0.15s ease-out',
        'slide-up':   'slideUp 0.2s ease-out',
        'pulse-ring': 'pulseRing 1.5s cubic-bezier(0.4, 0, 0.6, 1) infinite',
      },
      keyframes: {
        fadeIn:    { from: { opacity: '0' }, to: { opacity: '1' } },
        slideUp:   { from: { opacity: '0', transform: 'translateY(8px)' }, to: { opacity: '1', transform: 'translateY(0)' } },
        pulseRing: { '0%,100%': { opacity: '1' }, '50%': { opacity: '.4' } },
      },
    },
  },
  plugins: [],
};
