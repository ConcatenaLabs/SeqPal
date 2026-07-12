import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Served under https://<host>/seqpal/ in production (Caddy reverse-proxies
// /seqpal/* to seqpald, which serves both the built SPA and /api/*). `base`
// makes asset URLs and the router basename subpath-aware in the build; local
// dev stays at '/' so http://localhost:5173 works unchanged. Override with BASE.
//
// The dev proxies keep the SPA on same-origin relative paths (/seqpal/api/*,
// /openamp/v1/*) in every environment, which is what the strict CSP
// (`connect-src 'self'`) requires, and they default to LOCAL backends so a dev
// build never mints on the live production box by accident.
//
// Aim dev at another backend deliberately by pointing the proxy at it. Both
// values are base URLs including any path prefix the backend is mounted under:
//
//   VITE_SEQPAL_API=https://sequentiatestnet.com/seqpal \
//   VITE_OPENAMP_API=https://sequentiatestnet.com/openamp \
//   npm run dev
//
// That configuration deploys REAL assets on the live Sequentia testnet.
const SEQPAL_API = process.env.VITE_SEQPAL_API || 'http://127.0.0.1:8730'
const OPENAMP_API = process.env.VITE_OPENAMP_API || 'http://127.0.0.1:8722'

// The /seqpal and /openamp prefixes belong to the box's Caddy front, not to the
// daemons: seqpald routes /api/*, openampd routes /v1/*. Strip the prefix and
// let the target's own base path (empty for localhost) put it back.
const strip = (prefix) => (path) => path.replace(prefix, '') || '/'

export default defineConfig(({ command }) => ({
  base: process.env.BASE || (command === 'build' ? '/seqpal/' : '/'),
  plugins: [react()],
  server: {
    host: true,
    port: 5173,
    proxy: {
      '/seqpal/api': {
        target: SEQPAL_API,
        changeOrigin: true,
        rewrite: strip('/seqpal'),
      },
      '/openamp': {
        target: OPENAMP_API,
        changeOrigin: true,
        rewrite: strip('/openamp'),
      },
    },
  },
}))
