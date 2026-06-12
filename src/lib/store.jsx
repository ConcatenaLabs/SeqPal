import { createContext, useContext, useEffect, useState } from 'react'

const KEY = 'seqpal.demo.v2'

// account.individual === the logged-in natural person (their SeqPal ID).
// account.corporates === verified corporate (KYB) SeqPal IDs linked to that person.
// personas === previously verified accounts in this browser, keyed by the
// individual's SeqPal ID number, so a returning user can sign back in (the live
// platform authenticates against the SeqPal ID record; the demo has no backend).
const blank = {
  account: {
    individual: null,
    corporates: [],
  },
  personas: {},
  issuances: [],
}

function load() {
  try {
    const raw = localStorage.getItem(KEY)
    if (!raw) return blank
    const parsed = JSON.parse(raw)
    return {
      account: { ...blank.account, ...(parsed.account || {}) },
      personas: parsed.personas || {},
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
  // Keep the personas roster current with whatever account is signed in, so a
  // sign-out (or browser close) never orphans a verified SeqPal ID.
  const saveAccount = (s, account) =>
    account.individual
      ? { ...s.personas, [account.individual.idNumber]: account }
      : s.personas

  const registerIndividual = (individual) =>
    setState((s) => {
      const account = { individual, corporates: [] }
      return { ...s, account, personas: saveAccount(s, account) }
    })

  const addCorporate = (corporate) =>
    setState((s) => {
      const account = {
        ...s.account,
        corporates: [...s.account.corporates, corporate],
      }
      return { ...s, account, personas: saveAccount(s, account) }
    })

  // "Logging in" in the demo = restoring a previously verified persona; the
  // live platform authenticates against the SeqPal ID record.
  const signInAs = (idNumber) =>
    setState((s) =>
      s.personas[idNumber] ? { ...s, account: s.personas[idNumber] } : s
    )

  const signOut = () =>
    setState((s) => ({
      ...s,
      personas: saveAccount(s, s.account),
      account: { individual: null, corporates: [] },
    }))

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
    signInAs,
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
