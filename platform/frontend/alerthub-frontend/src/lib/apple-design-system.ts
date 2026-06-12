/**
 * Revolutionary Apple Design System for AlertHub Enterprise
 * Complete glassmorphism effects, advanced animations, and Apple HIG compliance
 */

export const AppleDesignSystem = {
  // Core Colors - Apple Human Interface Guidelines
  colors: {
    // Primary System Colors
    blue: '#007AFF',
    green: '#34C759',
    indigo: '#5856D6',
    orange: '#FF9500',
    pink: '#FF2D92',
    purple: '#AF52DE',
    red: '#FF3B30',
    teal: '#5AC8FA',
    yellow: '#FFCC00',
    
    // Gray Colors
    gray: '#8E8E93',
    gray2: '#AEAEB2',
    gray3: '#C7C7CC',
    gray4: '#D1D1D6',
    gray5: '#E5E5EA',
    gray6: '#F2F2F7',
    
    // Semantic Colors (CSS variables)
    label: 'var(--color-text)',
    secondaryLabel: 'var(--color-text-secondary)',
    tertiaryLabel: 'var(--color-text-tertiary)',
    quaternaryLabel: 'var(--color-text-quaternary)',
    
    // Background Colors
    background: 'var(--color-background)',
    secondaryBackground: 'var(--color-card)',
    tertiaryBackground: 'var(--color-background-tertiary)',
    
    // Fill Colors
    fill: 'var(--color-fill)',
    secondaryFill: 'var(--color-fill-secondary)',
    tertiaryFill: 'var(--color-fill-tertiary)',
    quaternaryFill: 'var(--color-fill-quaternary)',
    
    // Separator Colors
    separator: 'var(--color-separator)',
    opaqueSeparator: 'var(--color-separator-opaque)',
    
    // Link Colors
    link: 'var(--color-link)',
    
    // Premium Gradients
    gradients: {
      blueToIndigo: 'linear-gradient(135deg, #007AFF 0%, #5856D6 100%)',
      purpleToPink: 'linear-gradient(135deg, #AF52DE 0%, #FF2D92 100%)',
      orangeToRed: 'linear-gradient(135deg, #FF9500 0%, #FF3B30 100%)',
      greenToTeal: 'linear-gradient(135deg, #34C759 0%, #5AC8FA 100%)',
      
      // Glassmorphism gradients
      glass: 'linear-gradient(135deg, rgba(255, 255, 255, 0.1) 0%, rgba(255, 255, 255, 0.05) 100%)',
      glassReverse: 'linear-gradient(135deg, rgba(255, 255, 255, 0.05) 0%, rgba(255, 255, 255, 0.1) 100%)',
      
      // Dark mode glassmorphism
      darkGlass: 'linear-gradient(135deg, rgba(255, 255, 255, 0.05) 0%, rgba(255, 255, 255, 0.02) 100%)',
      darkGlassReverse: 'linear-gradient(135deg, rgba(255, 255, 255, 0.02) 0%, rgba(255, 255, 255, 0.05) 100%)',
    }
  },
  
  // Typography
  typography: {
    // Font Family
    fontFamily: '-apple-system, BlinkMacSystemFont, "SF Pro Text", "SF Pro Icons", "SF Pro Display", "Helvetica Neue", Helvetica, Arial, sans-serif',
    monoFamily: 'SF Mono, Monaco, Consolas, "Liberation Mono", "Courier New", monospace',
    
    // Font Sizes
    sizes: {
      largeTitle: 34,
      title1: 28,
      title2: 22,
      title3: 20,
      headline: 17,
      body: 17,
      callout: 16,
      subhead: 15,
      footnote: 13,
      caption1: 12,
      caption2: 11,
    },
    
    // Font Weights
    weights: {
      ultraLight: 100,
      thin: 200,
      light: 300,
      regular: 400,
      medium: 500,
      semibold: 600,
      bold: 700,
      heavy: 800,
      black: 900,
    },
    
    // Line Heights
    lineHeights: {
      tight: 1.2,
      normal: 1.4,
      relaxed: 1.6,
      loose: 1.8,
    }
  },
  
  // Spacing System
  spacing: {
    xs: 4,
    sm: 8,
    md: 16,
    lg: 24,
    xl: 32,
    '2xl': 48,
    '3xl': 64,
    '4xl': 96,
    '5xl': 128,
  },
  
  // Border Radius
  radius: {
    none: 0,
    sm: 6,
    md: 10,
    lg: 12,
    xl: 16,
    '2xl': 20,
    '3xl': 28,
    full: 9999,
  },
  
  // Shadows
  shadows: {
    // iOS-style shadows
    sm: '0 1px 3px rgba(0, 0, 0, 0.12), 0 1px 2px rgba(0, 0, 0, 0.24)',
    md: '0 4px 6px rgba(0, 0, 0, 0.07), 0 1px 3px rgba(0, 0, 0, 0.06)',
    lg: '0 10px 15px rgba(0, 0, 0, 0.1), 0 4px 6px rgba(0, 0, 0, 0.05)',
    xl: '0 20px 25px rgba(0, 0, 0, 0.1), 0 10px 10px rgba(0, 0, 0, 0.04)',
    '2xl': '0 25px 50px rgba(0, 0, 0, 0.25)',
    
    // Glassmorphism shadows
    glass: '0 8px 32px rgba(0, 0, 0, 0.12), inset 0 1px 0 rgba(255, 255, 255, 0.2)',
    glassLg: '0 16px 64px rgba(0, 0, 0, 0.16), inset 0 1px 0 rgba(255, 255, 255, 0.2)',
    
    // Card shadows
    card: '0 4px 16px rgba(0, 0, 0, 0.08), 0 1px 4px rgba(0, 0, 0, 0.04)',
    cardHover: '0 8px 32px rgba(0, 0, 0, 0.12), 0 4px 8px rgba(0, 0, 0, 0.06)',
  },
  
  // Glassmorphism Effects
  glassmorphism: {
    // Standard Glass Cards
    card: {
      background: 'rgba(255, 255, 255, 0.8)',
      backdropFilter: 'blur(20px) saturate(1.8)',
      border: '0.5px solid rgba(255, 255, 255, 0.2)',
      boxShadow: '0 8px 32px rgba(0, 0, 0, 0.12), inset 0 1px 0 rgba(255, 255, 255, 0.2)',
    },
    
    cardDark: {
      background: 'rgba(0, 0, 0, 0.3)',
      backdropFilter: 'blur(20px) saturate(1.8)',
      border: '0.5px solid rgba(255, 255, 255, 0.1)',
      boxShadow: '0 8px 32px rgba(0, 0, 0, 0.3), inset 0 1px 0 rgba(255, 255, 255, 0.05)',
    },
    
    // Premium Glass Effects
    premium: {
      background: 'rgba(255, 255, 255, 0.95)',
      backdropFilter: 'blur(30px) saturate(2)',
      border: '0.5px solid rgba(255, 255, 255, 0.3)',
      boxShadow: '0 16px 64px rgba(0, 0, 0, 0.16), inset 0 2px 0 rgba(255, 255, 255, 0.3)',
    },
    
    premiumDark: {
      background: 'rgba(0, 0, 0, 0.4)',
      backdropFilter: 'blur(30px) saturate(2)',
      border: '0.5px solid rgba(255, 255, 255, 0.15)',
      boxShadow: '0 16px 64px rgba(0, 0, 0, 0.4), inset 0 2px 0 rgba(255, 255, 255, 0.1)',
    },
    
    // Subtle Glass for backgrounds
    subtle: {
      background: 'rgba(255, 255, 255, 0.6)',
      backdropFilter: 'blur(15px) saturate(1.5)',
      border: '0.5px solid rgba(255, 255, 255, 0.15)',
      boxShadow: '0 4px 16px rgba(0, 0, 0, 0.08)',
    },
    
    subtleDark: {
      background: 'rgba(0, 0, 0, 0.2)',
      backdropFilter: 'blur(15px) saturate(1.5)',
      border: '0.5px solid rgba(255, 255, 255, 0.08)',
      boxShadow: '0 4px 16px rgba(0, 0, 0, 0.2)',
    },
  },
  
  // Animation System
  animations: {
    // Timing Functions
    timingFunctions: {
      ease: 'cubic-bezier(0.25, 0.1, 0.25, 1)',
      easeIn: 'cubic-bezier(0.42, 0, 1, 1)',
      easeOut: 'cubic-bezier(0, 0, 0.58, 1)',
      easeInOut: 'cubic-bezier(0.42, 0, 0.58, 1)',
      
      // Apple's signature curves
      appleEase: 'cubic-bezier(0.4, 0.0, 0.2, 1)',
      appleSpring: 'cubic-bezier(0.175, 0.885, 0.32, 1.275)',
      appleBounce: 'cubic-bezier(0.68, -0.55, 0.265, 1.55)',
    },
    
    // Durations
    durations: {
      instant: '0ms',
      fast: '150ms',
      normal: '300ms',
      slow: '500ms',
      slower: '750ms',
    },
    
    // Transforms
    transforms: {
      scale: 'scale(1.05)',
      scaleDown: 'scale(0.95)',
      scaleUp: 'scale(1.02)',
      
      // Micro-interactions
      pressDown: 'scale(0.98)',
      pressUp: 'scale(1.02)',
      
      // Slide animations
      slideUp: 'translateY(-4px)',
      slideDown: 'translateY(4px)',
      slideLeft: 'translateX(-4px)',
      slideRight: 'translateX(4px)',
    },
  },
  
  // Component Styles
  components: {
    // Button Styles
    button: {
      primary: {
        background: '#007AFF',
        color: '#FFFFFF',
        borderRadius: 10,
        padding: '12px 20px',
        fontWeight: 600,
        transition: 'all 150ms cubic-bezier(0.4, 0.0, 0.2, 1)',
        border: 'none',
        cursor: 'pointer',
      },
      
      secondary: {
        background: 'rgba(0, 122, 255, 0.1)',
        color: '#007AFF',
        borderRadius: 10,
        padding: '12px 20px',
        fontWeight: 600,
        transition: 'all 150ms cubic-bezier(0.4, 0.0, 0.2, 1)',
        border: '0.5px solid rgba(0, 122, 255, 0.2)',
        cursor: 'pointer',
      },
      
      glass: {
        background: 'rgba(255, 255, 255, 0.1)',
        backdropFilter: 'blur(10px) saturate(1.5)',
        color: 'var(--color-text)',
        borderRadius: 10,
        padding: '12px 20px',
        fontWeight: 600,
        transition: 'all 150ms cubic-bezier(0.4, 0.0, 0.2, 1)',
        border: '0.5px solid rgba(255, 255, 255, 0.2)',
        cursor: 'pointer',
      },
    },
    
    // Card Styles
    card: {
      standard: {
        background: 'var(--color-card)',
        borderRadius: 12,
        padding: 20,
        border: '0.5px solid var(--color-separator)',
        transition: 'all 150ms cubic-bezier(0.4, 0.0, 0.2, 1)',
      },
      
      glass: {
        background: 'rgba(255, 255, 255, 0.8)',
        backdropFilter: 'blur(20px) saturate(1.8)',
        borderRadius: 16,
        padding: 24,
        border: '0.5px solid rgba(255, 255, 255, 0.2)',
        boxShadow: '0 8px 32px rgba(0, 0, 0, 0.12), inset 0 1px 0 rgba(255, 255, 255, 0.2)',
        transition: 'all 300ms cubic-bezier(0.4, 0.0, 0.2, 1)',
      },
      
      premium: {
        background: 'rgba(255, 255, 255, 0.95)',
        backdropFilter: 'blur(30px) saturate(2)',
        borderRadius: 20,
        padding: 28,
        border: '0.5px solid rgba(255, 255, 255, 0.3)',
        boxShadow: '0 16px 64px rgba(0, 0, 0, 0.16), inset 0 2px 0 rgba(255, 255, 255, 0.3)',
        transition: 'all 300ms cubic-bezier(0.175, 0.885, 0.32, 1.275)',
      },
    },
  },
  
  // Utility Functions
  utils: {
    // Create glassmorphism style object
    createGlassmorphism: (opacity = 0.8, blur = 20, saturation = 1.8) => ({
      background: `rgba(255, 255, 255, ${opacity})`,
      backdropFilter: `blur(${blur}px) saturate(${saturation})`,
      border: `0.5px solid rgba(255, 255, 255, ${opacity * 0.25})`,
      boxShadow: `0 8px 32px rgba(0, 0, 0, 0.12), inset 0 1px 0 rgba(255, 255, 255, ${opacity * 0.25})`,
    }),
    
    // Create dark glassmorphism
    createDarkGlassmorphism: (opacity = 0.3, blur = 20, saturation = 1.8) => ({
      background: `rgba(0, 0, 0, ${opacity})`,
      backdropFilter: `blur(${blur}px) saturate(${saturation})`,
      border: `0.5px solid rgba(255, 255, 255, ${opacity * 0.33})`,
      boxShadow: `0 8px 32px rgba(0, 0, 0, 0.3), inset 0 1px 0 rgba(255, 255, 255, ${opacity * 0.17})`,
    }),
    
    // Create hover effect
    createHoverEffect: (scale = 1.02, shadow = '0 8px 32px rgba(0, 0, 0, 0.12)') => ({
      transform: `scale(${scale})`,
      boxShadow: shadow,
      transition: 'all 150ms cubic-bezier(0.4, 0.0, 0.2, 1)',
    }),
    
    // Create press effect
    createPressEffect: (scale = 0.98) => ({
      transform: `scale(${scale})`,
      transition: 'all 100ms cubic-bezier(0.4, 0.0, 0.2, 1)',
    }),
  }
} as const

export type AppleDesignSystemType = typeof AppleDesignSystem

// Utility hook for accessing design system
export const useAppleDesign = () => AppleDesignSystem

// CSS-in-JS helper for glassmorphism
export const glassmorphismCSS = (variant: keyof typeof AppleDesignSystem.glassmorphism = 'card') => 
  AppleDesignSystem.glassmorphism[variant]

// Color utility functions
export const appleColors = AppleDesignSystem.colors
export const appleTypography = AppleDesignSystem.typography
export const appleSpacing = AppleDesignSystem.spacing
export const appleRadius = AppleDesignSystem.radius
export const appleShadows = AppleDesignSystem.shadows
export const appleAnimations = AppleDesignSystem.animations