/** @type {import('tailwindcss').Config} */
export default {
  darkMode: 'class',
  content: ['./index.html', './src/**/*.{js,ts,jsx,tsx}'],
  theme: {
    extend: {
      colors: {
        bg:       '#0A0A0B',
        surface:  '#141416',
        elevated: '#1A1A1D',
        border:   '#232326',
        hairline: '#1C1C1F',
        text:     '#F4F4F5',
        muted:    '#8A8A93',
        faint:    '#5A5A62',
        accent: {
          DEFAULT: '#E24B4A',
          hover:   '#C93F3E',
          subtle:  'rgba(226,75,74,0.12)',
        },
        profit:  '#22C55E',
        loss:    '#E24B4A',
        warning: '#F59E0B',
        demo:    '#F59E0B',
        real:    '#22C55E',
      },
      fontFamily: {
        sans: ['Inter', 'system-ui', '-apple-system', 'Segoe UI', 'sans-serif'],
      },
      fontSize: {
        meta:    ['12px', { lineHeight: '16px' }],
        body:    ['14px', { lineHeight: '20px' }],
        card:    ['16px', { lineHeight: '24px' }],
        title:   ['24px', { lineHeight: '32px' }],
        hero:    ['36px', { lineHeight: '40px' }],
      },
      borderRadius: {
        card: '16px',
        pill: '999px',
      },
      spacing: {
        gutter: '16px',
        tabbar: '64px',
      },
      keyframes: {
        'flash-up':   { '0%': { color: '#22C55E' }, '100%': { color: 'inherit' } },
        'flash-down': { '0%': { color: '#E24B4A' }, '100%': { color: 'inherit' } },
        'slide-up':   { '0%': { transform: 'translateY(100%)' }, '100%': { transform: 'translateY(0)' } },
        'fade-in':    { '0%': { opacity: '0' }, '100%': { opacity: '1' } },
        'shimmer':    { '0%': { backgroundPosition: '-400px 0' }, '100%': { backgroundPosition: '400px 0' } },
      },
      animation: {
        'flash-up':   'flash-up 300ms ease-out',
        'flash-down': 'flash-down 300ms ease-out',
        'slide-up':   'slide-up 220ms cubic-bezier(0.32,0.72,0,1)',
        'fade-in':    'fade-in 150ms ease-out',
        'shimmer':    'shimmer 1.4s linear infinite',
      },
    },
  },
  plugins: [require('tailwindcss-animate')],
}
