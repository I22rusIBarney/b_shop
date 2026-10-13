import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  build: { outDir: 'dist', emptyOutDir: true, sourcemap: false, chunkSizeWarningLimit: 800 },
  server: {
    proxy: { '/api': 'http://127.0.0.1:8099', '/files': 'http://127.0.0.1:8099', '/manifest.webmanifest': 'http://127.0.0.1:8099' },
  },
})
