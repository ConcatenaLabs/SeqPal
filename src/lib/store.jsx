import { createContext, useCallback, useContext, useEffect, useState } from 'react'
import * as api from './api'
import {
  computeAID,
  decryptKey,
  encryptKey,
  generateEnclaveKey,
  signChallenge,
  signStatement,
} from './keys'

// localStorage holds exactly two things, and neither is a financial fact:
//   - the ENCRYPTED enclave-key envelope {v, salt, iv, ct, xonly, aid}
//   - UI preferences
// Accounts, entities, issuances, asset ids and balances are read from seqpald
// and the policy server. Placement-portal drafts and the simulated subscription
// flow live in memory for the session only, so nothing fabricated is ever
// written down as if it were a record.
const ENVELOPE_KEY = 'seqpal.id.v1'
const PREFS_KEY = 'seqpal.prefs.v1'

function readJSON(key) {
  try {
    const raw = localStorage.getItem(key)
    return raw ? JSON.parse(raw) : null
  } catch {
    return null
  }
}

export function loadEnvelope() {
  const env = readJSON(ENVELOPE_KEY)
  return env?.ct && env?.xonly ? env : null
}

export function envelopeFilename(aid) {
  return `seqpal-id-${aid}.json`
}

// Build the export/backup file. It is the same envelope localStorage holds:
// encrypted under the user's passphrase, useless without it.
export function envelopeFile(envelope) {
  return new Blob([JSON.stringify(envelope, null, 2)], { type: 'application/json' })
}

export function downloadEnvelope(envelope) {
  const url = URL.createObjectURL(envelopeFile(envelope))
  const a = document.createElement('a')
  a.href = url
  a.download = envelopeFilename(envelope.aid)
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}

const StoreContext = createContext(null)

const EMPTY_SIM = { portal: null, subscriptions: [], activity: [] }

export function StoreProvider({ children }) {
  const [status, setStatus] = useState('loading') // loading | anon | in
  const [account, setAccount] = useState(null)
  const [entities, setEntities] = useState([])
  const [issuances, setIssuances] = useState([])
  const [envelope, setEnvelope] = useState(loadEnvelope)
  const [prefs, setPrefsState] = useState(() => readJSON(PREFS_KEY) || {})
  // The decrypted key is held for the session only: it signs login challenges
  // and, later, enclave co-signatures. It is never persisted in the clear.
  const [priv, setPriv] = useState(null)
  // Browser-session simulation of surfaces that have no server-side home yet
  // (placement portal, subscriptions, servicing activity). In memory on purpose.
  const [sim, setSim] = useState({})
  // Live chain-watch state, keyed by issuance_id, fed by the seqpald SSE stream
  // (GET /api/events). The server is authoritative: this is a mirror of the
  // latest event per issuance, never a fact the browser invents. Includes the
  // reorg-regression state so a chip can visibly regress.
  const [watch, setWatch] = useState({})

  const applyMe = useCallback((data) => {
    setAccount(data.account)
    setEntities(data.entities || [])
    setIssuances(data.issuances || [])
    setStatus('in')
  }, [])

  const refresh = useCallback(async () => {
    const data = await api.me()
    applyMe(data)
    return data
  }, [applyMe])

  // A live session cookie is what signs you in, not anything in this browser's
  // storage: the page reloads straight back into the account it belongs to.
  useEffect(() => {
    let cancelled = false
    api
      .me()
      .then((data) => {
        if (!cancelled) applyMe(data)
      })
      .catch(() => {
        if (!cancelled) setStatus('anon')
      })
    return () => {
      cancelled = true
    }
  }, [applyMe])

  // One Server-Sent Events connection for the signed-in principal. EventSource
  // sends the HttpOnly session cookie same-origin, reconnects on its own, and on
  // (re)connect seqpald replays a snapshot of every owned issuance, so a mirror
  // that missed an event heals on the next connect. Torn down on sign-out.
  useEffect(() => {
    if (status !== 'in') return undefined
    let es
    try {
      es = new EventSource(api.EVENTS_URL, { withCredentials: true })
    } catch {
      return undefined
    }
    es.addEventListener('watch', (e) => {
      try {
        const ev = JSON.parse(e.data)
        if (ev && ev.issuance_id) setWatch((w) => ({ ...w, [ev.issuance_id]: ev }))
      } catch {
        // A malformed frame is ignored; the next event or reconnect corrects it.
      }
    })
    return () => {
      try {
        es.close()
      } catch {
        // already closed
      }
    }
  }, [status])

  const persistEnvelope = (env) => {
    localStorage.setItem(ENVELOPE_KEY, JSON.stringify(env))
    setEnvelope(env)
  }

  const setPrefs = (patch) => {
    setPrefsState((p) => {
      const next = { ...p, ...patch }
      localStorage.setItem(PREFS_KEY, JSON.stringify(next))
      return next
    })
  }

  // ── identity ────────────────────────────────────────────────────────
  // Create the enclave key and seal it. Registration is NOT complete until the
  // caller has exported the backup and called registerWithKey: no export, no
  // account.
  const prepareId = async (passphrase) => {
    const key = generateEnclaveKey()
    const env = await encryptKey(key.priv, passphrase)
    env.xonly = key.xonly
    env.aid = computeAID([key.xonly])
    return { priv: key.priv, envelope: env }
  }

  // Proof of possession of the enclave key: fetch a challenge, sign it TAGGED,
  // present it. No password ever reaches the server.
  const handshake = async (privHex, xonly, fn, extra = {}) => {
    const { challenge } = await api.challenge(xonly)
    const sig = signChallenge(privHex, challenge)
    const { account: acct } = await fn({ xonly, challenge, sig, ...extra })
    setPriv(privHex)
    const data = await api.me()
    applyMe(data)
    return acct
  }

  const registerWithKey = async ({ priv: privHex, envelope: env, displayName, residence, profile }) => {
    const acct = await handshake(privHex, env.xonly, api.register, {
      kind: 'individual',
      display_name: displayName,
      residence,
      profile,
    })
    persistEnvelope(env)
    return acct
  }

  const unlock = async (passphrase) => {
    if (!envelope) throw new Error('There is no SeqPal ID in this browser. Import your backup file.')
    const privHex = await decryptKey(envelope, passphrase)
    return handshake(privHex, envelope.xonly, api.login)
  }

  const importId = async (env, passphrase) => {
    const privHex = await decryptKey(env, passphrase)
    const xonly = env.xonly
    const withAid = { ...env, xonly, aid: env.aid || computeAID([xonly]) }
    const acct = await handshake(privHex, xonly, api.login)
    persistEnvelope(withAid)
    return acct
  }

  const signOut = async () => {
    try {
      await api.logout()
    } catch {
      // A dead session is already signed out; clear the browser side regardless.
    }
    setPriv(null)
    setAccount(null)
    setEntities([])
    setIssuances([])
    setSim({})
    setWatch({})
    setStatus('anon')
  }

  // Erase the encrypted key from this browser. The caller (Dashboard) is
  // responsible for the guard: this is the irreversible half of a 2-of-2.
  const forgetId = async () => {
    await signOut()
    localStorage.removeItem(ENVELOPE_KEY)
    localStorage.removeItem(PREFS_KEY)
    setEnvelope(null)
    setPrefsState({})
  }

  // ── server-owned records ────────────────────────────────────────────
  const createEntity = async (body) => {
    const { entity } = await api.createEntity(body)
    setEntities((e) => [...e, entity])
    return entity
  }

  const createIssuance = async (body) => {
    const { issuance } = await api.createIssuance(body)
    setIssuances((i) => [issuance, ...i])
    return issuance
  }

  const patchIssuance = async (id, body) => {
    const { issuance } = await api.patchIssuance(id, body)
    setIssuances((list) => list.map((i) => (i.id === id ? issuance : i)))
    return issuance
  }

  // The real mint. seqpald re-derives the terms hash and the idempotency key, so
  // a retry of the same terms returns the first asset rather than minting twice.
  const deployIssuance = async (body) => {
    const res = await api.deploy(body)
    await refresh()
    return res
  }

  // Sign an application statement with the session's in-memory enclave key. The
  // decrypted key never leaves the store, so callers ask the store to sign
  // rather than handling the key themselves. Returns null when locked.
  const signWithKey = (tag, statement) => (priv ? signStatement(priv, tag, statement) : null)

  // ── in-memory simulation (portal drafts, subscriptions, servicing) ──
  // The latest chain-watch event for an issuance, or undefined until the SSE
  // stream delivers one (the surface then shows a distinct "awaiting" chip
  // rather than fabricating a confirmed state).
  const watchFor = (id) => watch[id]

  const simFor = (id) => sim[id] || EMPTY_SIM
  const updateSim = (id, patch) =>
    setSim((s) => {
      const cur = s[id] || EMPTY_SIM
      return { ...s, [id]: { ...cur, ...(typeof patch === 'function' ? patch(cur) : patch) } }
    })

  const value = {
    status,
    loading: status === 'loading',
    isSignedIn: status === 'in',
    account,
    entities,
    issuances,
    envelope,
    hasLocalId: !!envelope,
    prefs,
    setPrefs,
    refresh,
    prepareId,
    registerWithKey,
    unlock,
    importId,
    signOut,
    forgetId,
    createEntity,
    createIssuance,
    patchIssuance,
    deployIssuance,
    signWithKey,
    watch,
    watchFor,
    simFor,
    updateSim,
  }

  return <StoreContext.Provider value={value}>{children}</StoreContext.Provider>
}

export function useStore() {
  const ctx = useContext(StoreContext)
  if (!ctx) throw new Error('useStore must be used within StoreProvider')
  return ctx
}

export { slugify } from './util'
