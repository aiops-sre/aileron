/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      fontFamily: {
        sans: ['-aileron-system', 'BlinkMacSystemFont', 'SF Pro Text', 'SF Pro Icons', 'Helvetica Neue', 'sans-serif'],
        display: ['-aileron-system', 'BlinkMacSystemFont', 'SF Pro Display', 'SF Pro Icons', 'Helvetica Neue', 'sans-serif'],
      },
      colors: {
        aileron: {
          blue: '#0071e3',
          'blue-hover': '#0077ed',
          green: '#34c759',
          red: '#ff3b30',
          orange: '#ff9500',
          yellow: '#ffcc00',
          purple: '#af52de',
          pink: '#ff2d92',
          text: '#1d1d1f',
          'text-secondary': '#6e6e73',
          'text-tertiary': '#a1a1a6',
          background: '#ffffff',
          fill: '#f5f5f7',
          'fill-secondary': '#f2f2f7',
          separator: '#d2d2d7',
          'separator-opaque': '#c6c6c8',
        },
        dark: {
          text: '#f5f5f7',
          'text-secondary': '#a1a1a6',
          'text-tertiary': '#6e6e73',
          background: '#000000',
          'background-elevated': '#1c1c1e',
          'background-secondary': '#2c2c2e',
          fill: '#1d1d1f',
          'fill-secondary': '#28282a',
          separator: '#424245',
          'separator-opaque': '#38383a',
        }
      },
      borderRadius: {
        'aileron-lg': '12px',
        'aileron-xl': '18px',
        'aileron-2xl': '24px',
      },
      boxShadow: {
        'aileron-sm': '0 2px 8px rgba(0,0,0,0.04)',
        'aileron-md': '0 4px 12px rgba(0,0,0,0.08)',
        'aileron-lg': '0 8px 24px rgba(0,0,0,0.12)',
        'aileron-xl': '0 16px 40px rgba(0,0,0,0.16)',
      },
      backdropBlur: {
        'aileron': '20px',
      },
      animation: {
        'pulse-ring': 'pulse 2s cubic-bezier(0.4, 0, 0.6, 1) infinite',
        'slide-up': 'slideUp 0.3s ease-out',
        'slide-down': 'slideDown 0.3s ease-out',
        'fade-in': 'fadeIn 0.2s ease-out',
        'scale-in': 'scaleIn 0.2s ease-out',
      },
      keyframes: {
        slideUp: {
          '0%': { transform: 'translateY(100%)' },
          '100%': { transform: 'translateY(0)' },
        },
        slideDown: {
          '0%': { transform: 'translateY(-100%)' },
          '100%': { transform: 'translateY(0)' },
        },
        fadeIn: {
          '0%': { opacity: '0' },
          '100%': { opacity: '1' },
        },
        scaleIn: {
          '0%': { transform: 'scale(0.9)', opacity: '0' },
          '100%': { transform: 'scale(1)', opacity: '1' },
        },
      },
    },
  },
  plugins: [
    require('@tailwindcss/forms'),
    require('@tailwindcss/typography'),
  ],
  darkMode: 'class',
}