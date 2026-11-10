import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import legacy from '@vitejs/plugin-legacy'
import { compression } from 'vite-plugin-compression2'
import { resolve } from 'node:path'

export default defineConfig({
  plugins: [
    vue(),
    legacy({ targets: ['chrome >= 61', 'safari >= 11', 'firefox >= 60'] }),
    compression({ algorithm: 'brotliCompress', exclude: [/\.(png|jpg|woff2)$/], threshold: 512 }),
  ],
  build: {
    outDir: resolve(__dirname, '../server/internal/webui/dist'),
    emptyOutDir: true,
    rollupOptions: {
      input: {
        tv: resolve(__dirname, 'tv.html'),
        remote: resolve(__dirname, 'remote.html'),
        admin: resolve(__dirname, 'admin.html'),
      },
    },
  },
  server: {
    proxy: {
      '/api': 'http://localhost:7464',
      '/media': 'http://localhost:7464',
      '/covers': 'http://localhost:7464',
      '/backgrounds': 'http://localhost:7464',
      '/ws': { target: 'ws://localhost:7464', ws: true },
    },
  },
})
