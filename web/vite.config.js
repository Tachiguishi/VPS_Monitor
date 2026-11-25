import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [vue(), tailwindcss()],
  server: {
    host: '127.0.0.1',
    proxy: {
      '/api': process.env.MONITOR_API_URL || 'http://127.0.0.1:8080',
    },
  },
})
