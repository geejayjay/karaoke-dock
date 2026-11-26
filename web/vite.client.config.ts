import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { execSync } from 'node:child_process'
import { renameSync, existsSync } from 'node:fs'
import { resolve } from 'node:path'

function resolveAppVersion(): string {
  if (process.env.VERSION) return process.env.VERSION
  try {
    return execSync('git describe --tags --always', { stdio: ['ignore', 'pipe', 'ignore'] }).toString().trim() || 'dev'
  } catch {
    return 'dev'
  }
}

export default defineConfig(({ mode }) => {
  const target = mode === 'tv' ? 'tv' : 'mobile'
  const entryHtml = target === 'tv' ? 'tv.html' : 'remote.html'
  const outDir = resolve(__dirname, `../clients/${target}/dist`)

  return {
    base: './',
    define: {
      'import.meta.env.VITE_CLIENT_MODE': JSON.stringify(target),
      'import.meta.env.VITE_APP_VERSION': JSON.stringify(resolveAppVersion()),
    },
    plugins: [
      vue(),
      {
        name: 'karaokedock-client-html',
        transformIndexHtml(html) {
          return html.replace(/<link[^>]+rel=["'](?:apple-touch-icon|icon)["'][^>]*>\s*/gi, '')
        },
        closeBundle() {
          const built = resolve(outDir, entryHtml)
          const index = resolve(outDir, 'index.html')
          if (existsSync(built)) renameSync(built, index)
        },
      },
    ],
    build: {
      outDir,
      emptyOutDir: true,
      rollupOptions: {
        input: resolve(__dirname, entryHtml),
      },
    },
  }
})
