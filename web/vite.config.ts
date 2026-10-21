import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'node:path'
import pkg from './package.json' with { type: 'json' }

const proxyTarget = process.env.KAIROS_PROXY ?? 'http://server:8080'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  define: { __APP_VERSION__: JSON.stringify(pkg.version) },
  resolve: {
    alias: { '@': path.resolve(import.meta.dirname, 'src') },
  },
  build: {
    outDir: '../webdist/dist',
    emptyOutDir: true,
    sourcemap: false,
    // Never inline fonts as data: URLs; the server CSP only allows 'self'.
    assetsInlineLimit: (file) => (/\.(woff2?|ttf|otf)$/.test(file) ? false : undefined),
  },
  server: {
    port: 5173,
    host: true,
    proxy: {
      '/api': { target: proxyTarget, changeOrigin: true, ws: true },
      '/healthz': { target: proxyTarget, changeOrigin: true },
    },
  },
})