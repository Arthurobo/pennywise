/** @type {import('tailwindcss').Config} */
module.exports = {
  content: [
    "./internal/templates/**/*.html",
    "./internal/static/js/app.js",
  ],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        // Money palette
        surplus: '#16a34a',
        warning: '#d97706',
        over:    '#dc2626',
      },
      fontFamily: {
        sans: ['ui-sans-serif', 'system-ui', '-apple-system', 'Segoe UI', 'Roboto', 'sans-serif'],
        mono: ['ui-monospace', 'SFMono-Regular', 'Menlo', 'monospace'],
      },
    },
  },
  plugins: [],
};
