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

// Pure helpers live in ./util; re-exported here for existing import sites.
export {
  fakeHex,
  fakeAssetId,
  fakeTxid,
  fakeGaid,
  fakeIdNumber,
  addBusinessDays,
  slugify,
} from './util'
