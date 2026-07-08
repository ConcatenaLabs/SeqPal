/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{js,jsx}'],
  theme: {
    extend: {
      colors: {
        ink: {
          950: '#070b14',
          900: '#0b1220',
          800: '#111c30',
          700: '#1b2940',
          600: '#2b3c59',
        },
        btc: {
          DEFAULT: '#F7931A',
          50: '#fff7ed',
          100: '#ffedd5',
          400: '#fbab4e',
          500: '#F7931A',
          600: '#e07d09',
          700: '#b9650a',
        },
        seq: {
          DEFAULT: '#27c2c9',
          400: '#3dd6dd',
          500: '#27c2c9',
          600: '#1299aa',
        },
      },
      fontFamily: {
        sans: ['Inter', 'ui-sans-serif', 'system-ui', 'sans-serif'],
        mono: ['"JetBrains Mono"', 'ui-monospace', 'SFMono-Regular', 'monospace'],
      },
      boxShadow: {
        card: '0 1px 2px rgba(7,11,20,0.06), 0 8px 24px -8px rgba(7,11,20,0.12)',
        glow: '0 0 0 1px rgba(247,147,26,0.25), 0 12px 40px -12px rgba(247,147,26,0.35)',
      },
    },
  },
  plugins: [],
}
