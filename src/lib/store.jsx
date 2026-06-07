import { createContext, useContext, useEffect, useState } from 'react'

const KEY = 'seqpal.demo.v1'

const blank = {
  id: null, // SeqPal corporate ID profile (null until onboarded)
  issuances: [], // deployed / draft issuances
}

function load() {
  try {
    const raw = localStorage.getItem(KEY)
    if (!raw) return blank
    return { ...blank, ...JSON.parse(raw) }
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

  const setId = (id) => setState((s) => ({ ...s, id }))

  const addIssuance = (issuance) =>
    setState((s) => ({ ...s, issuances: [issuance, ...s.issuances] }))

  const reset = () => {
    localStorage.removeItem(KEY)
    setState(blank)
  }

  return (
    <StoreContext.Provider value={{ ...state, setId, addIssuance, reset }}>
      {children}
    </StoreContext.Provider>
  )
}

export function useStore() {
  const ctx = useContext(StoreContext)
  if (!ctx) throw new Error('useStore must be used within StoreProvider')
  return ctx
}

// Generate a plausible-looking Liquid asset id for the demo.
export function fakeAssetId() {
  const hex = '0123456789abcdef'
  let out = ''
  for (let i = 0; i < 64; i++) out += hex[Math.floor(Math.random() * 16)]
  return out
}

export function fakeTxid() {
  return fakeAssetId()
}
