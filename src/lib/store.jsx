import { createContext, useContext, useEffect, useState } from 'react'

const KEY = 'seqpal.demo.v2'

// account.individual === the logged-in natural person (their SeqPal ID).
// account.corporates === verified corporate (KYB) SeqPal IDs linked to that person.
const blank = {
  account: {
    individual: null,
    corporates: [],
  },
  issuances: [],
}

function load() {
  try {
    const raw = localStorage.getItem(KEY)
    if (!raw) return blank
    const parsed = JSON.parse(raw)
    return {
      account: { ...blank.account, ...(parsed.account || {}) },
      issuances: parsed.issuances || [],
    }
  } catch {
    return blank
  }
}

const StoreContext = createContext(null)

export function StoreProvider({ children }) {
  const [state, setState] = useState(load)

  useEffect(() => {
    localStorage.setItem(KEY, JSON.stringify(state))
  }, [state])

  // ── account / identity ──────────────────────────────────────────────
  const registerIndividual = (individual) =>
    setState((s) => ({ ...s, account: { ...s.account, individual } }))

  const addCorporate = (corporate) =>
    setState((s) => ({
      ...s,
      account: { ...s.account, corporates: [...s.account.corporates, corporate] },
    }))

  const signOut = () =>
    setState((s) => ({ ...s, account: { individual: null, corporates: [] } }))

  // ── issuances ───────────────────────────────────────────────────────
  const addIssuance = (issuance) =>
    setState((s) => ({ ...s, issuances: [issuance, ...s.issuances] }))

  const updateIssuance = (id, patch) =>
    setState((s) => ({
      ...s,
      issuances: s.issuances.map((i) =>
        i.id === id ? { ...i, ...(typeof patch === 'function' ? patch(i) : patch) } : i
      ),
    }))

  const reset = () => {
    localStorage.removeItem(KEY)
    setState(blank)
  }

  const value = {
    ...state,
    isLoggedIn: !!state.account.individual,
    registerIndividual,
    addCorporate,
    signOut,
    addIssuance,
    updateIssuance,
    reset,
  }

  return <StoreContext.Provider value={value}>{children}</StoreContext.Provider>
}

export function useStore() {
  const ctx = useContext(StoreContext)
  if (!ctx) throw new Error('useStore must be used within StoreProvider')
  return ctx
}

// ── demo id / hash generators ──────────────────────────────────────────
export function fakeHex(len = 64) {
  const hex = '0123456789abcdef'
  let out = ''
  for (let i = 0; i < len; i++) out += hex[Math.floor(Math.random() * 16)]
  return out
}
export const fakeAssetId = () => fakeHex(64)
export const fakeTxid = () => fakeHex(64)

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
