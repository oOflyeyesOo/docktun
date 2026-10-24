/** @type {import('tailwindcss').Config} */
module.exports = {
  content: ['./index.html', './src/**/*.{vue,ts}'],
  theme: {
    extend: {
      colors: {
        void: '#f1f5f9',
        surface: '#ffffff',
        chrome: '#f8fafc',
        border: '#e2e8f0',
        muted: '#6b7280',
        neon: '#059669',
        magenta: '#7c3aed',
        cyan: '#0891b2',
        danger: '#e11d48',
      },
      fontFamily: {
        display: ['Inter', 'Segoe UI', 'sans-serif'],
        mono: ['JetBrains Mono', 'Fira Code', 'monospace'],
      },
      boxShadow: {
        neon: '0 0 5px #059669, 0 0 10px #05966940',
        'neon-lg': '0 0 10px #059669, 0 0 20px #05966960, 0 0 40px #05966930',
        'neon-magenta': '0 0 5px #7c3aed, 0 0 20px #7c3aed60',
        'neon-cyan': '0 0 5px #0891b2, 0 0 20px #0891b260',
      },
      animation: {
        blink: 'blink 1s step-end infinite',
        glitch: 'glitch 3s infinite',
      },
      keyframes: {
        blink: { '50%': { opacity: '0' } },
        glitch: {
          '0%, 92%, 100%': { transform: 'translate(0)' },
          '93%': { transform: 'translate(-2px, 2px)' },
          '95%': { transform: 'translate(2px, -1px)' },
          '97%': { transform: 'translate(-1px, -1px)' },
        },
      },
    },
  },
  plugins: [],
}
