/** @type {import('tailwindcss').Config} */
export default {
  content: ['./remote.html', './admin.html', './src/remote/**/*.{vue,ts}', './src/admin/**/*.{vue,ts}', './src/shared/**/*.{vue,ts}'],
  theme: {
    extend: {
      colors: {
        ink: { 950: '#0d0b14', 900: '#15121f', 800: '#1f1b2e', 700: '#2b2640', 500: '#6f688c', 300: '#b6b0cc', 100: '#ece9f5' },
        brand: { DEFAULT: '#ff4d8d', soft: '#ff4d8d22' },
      },
    },
  },
  plugins: [],
}
