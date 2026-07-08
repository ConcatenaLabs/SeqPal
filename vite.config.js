import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Served under https://<host>/seqpal/ in production (Caddy serves the built
// static files and reverse-proxies /seqpal/api/* to seqpald). `base` makes asset
// URLs and the router basename subpath-aware in the build; local dev stays at '/'
// so http://localhost:5173 works unchanged. Override with BASE for other paths.
export default defineConfig(({ command }) => ({
  base: process.env.BASE || (command === 'build' ? '/seqpal/' : '/'),
  plugins: [react()],
  server: { host: true, port: 5173 },
}))
