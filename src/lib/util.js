// Pure, framework-free helpers (no React/JSX) so they can be unit-tested and
// reused without importing the store/context module.

// ── demo id / hash generators ──────────────────────────────────────────
export function fakeHex(len = 64) {
  const hex = '0123456789abcdef'
  let out = ''
  for (let i = 0; i < len; i++) out += hex[Math.floor(Math.random() * 16)]
  return out
}
export const fakeAssetId = () => fakeHex(64)
export const fakeTxid = () => fakeHex(64)

// A plausible Blockstream Green Account ID (the wallet identifier AMP whitelists).
export function fakeGaid() {
  const chars = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz0123456789'
  let s = ''
  for (let i = 0; i < 40; i++) s += chars[Math.floor(Math.random() * chars.length)]
  return s
}

export function fakeIdNumber(prefix) {
  return (
    prefix +
    '-' +
    Math.random().toString(36).slice(2, 8).toUpperCase() +
    '-' +
    Math.random().toString(36).slice(2, 6).toUpperCase()
  )
}

// Add N business days to a date (skips Sat/Sun).
export function addBusinessDays(from, n) {
  const d = new Date(from)
  let added = 0
  while (added < n) {
    d.setDate(d.getDate() + 1)
    const day = d.getDay()
    if (day !== 0 && day !== 6) added++
  }
  return d
}

export function slugify(s) {
  return (s || '')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/(^-|-$)/g, '')
    .slice(0, 24)
}
