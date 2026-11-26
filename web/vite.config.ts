import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import legacy from '@vitejs/plugin-legacy'
import { compression } from 'vite-plugin-compression2'
import { execSync } from 'node:child_process'
import { resolve } from 'node:path'

function resolveAppVersion(): string {
  if (process.env.VERSION) return process.env.VERSION
  try {
    return execSync('git describe --tags --always', { stdio: ['ignore', 'pipe', 'ignore'] }).toString().trim() || 'dev'
  } catch {
    return 'dev'
  }
}

export default defineConfig({
  define: {
    'import.meta.env.VITE_APP_VERSION': JSON.stringify(resolveAppVersion()),
  },
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
      '/icon': 'http://localhost:7464',
      '/fonts': 'http://localhost:7464',
      '/backgrounds': 'http://localhost:7464',
      '/ws': { target: 'ws://localhost:7464', ws: true },
    },
  },
})
