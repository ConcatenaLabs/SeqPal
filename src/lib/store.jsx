import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'
import * as api from './api'
import * as wallet from './wallet'
import {
  BEARER_ATTESTATION_TAG,
  CLOSE_TAG,
  HOLDING_PROOF_TAG,
  MANDATE_TAG,
  bearerAttestationDigest,
  holdingProofDigest,
} from './statements'

// A SeqPal ID is the enclave key of a Sequentia wallet the holder already has
// (see wallet.js). This browser holds no key material at all: localStorage keeps
// UI preferences and, for a wallet linked by hand, the PUBLIC x-only key of the
// account signed in, so a reload knows whose signature to ask for. Accounts,
// entities, issuances, asset ids and balances are read from seqpald and the
// policy server. Placement-portal drafts and the simulated subscription flow
// live in memory for the session only, so nothing fabricated is ever written
// down as if it were a record.
const SIGNER_KEY = 'seqpal.signer.v1'
const PREFS_KEY = 'seqpal.prefs.v1'

// The tag every SeqPal application statement is signed under, by tag name, so a
// linked wallet can be told exactly what it is being asked to sign.
export const TAG_LABELS = {
  'openamp-challenge-v1': 'Sign-in challenge',
  'openamp-document-v1': 'Offering document',
  'seqpal-ubo-v1': 'UBO declaration',
  [MANDATE_TAG]: 'Payout mandate',
  [CLOSE_TAG]: 'Closing authorization',
  [BEARER_ATTESTATION_TAG]: 'Bearer issuance attestation',
  [HOLDING_PROOF_TAG]: 'Holding proof',
  'seqpal-market-abuse-ack-v1': 'Market-abuse acknowledgment',
  'seqpal-listing-v1': 'Listing acknowledgment',
}

function readJSON(key) {
  try {
    const raw = localStorage.getItem(key)
    return raw ? JSON.parse(raw) : null
  } catch {
    return null
  }
}

// The signed-in signer, as far as this browser needs to remember it: which kind
// of wallet, and its PUBLIC key. Never a secret, so localStorage is the right
// home and clearing it costs nothing but a reconnect.
function loadSigner() {
  const s = readJSON(SIGNER_KEY)
  return s?.kind && s?.xonly ? s : null
}

const StoreContext = createContext(null)

const EMPTY_SIM = { portal: null, subscriptions: [], activity: [] }

export function StoreProvider({ children }) {
  const [status, setStatus] = useState('loading') // loading | anon | in
  const [account, setAccount] = useState(null)
  const [entities, setEntities] = useState([])
  const [issuances, setIssuances] = useState([])
  const [prefs, setPrefsState] = useState(() => readJSON(PREFS_KEY) || {})
  // The wallet this session signs with: { kind: 'extension' | 'linked', xonly,
  // aid }. Never a key. An 'extension' signer means every signature is a
  // request to the wallet extension; a 'linked' one means the holder produces
  // it in their own wallet and pastes it back.
  const [signer, setSignerState] = useState(loadSigner)
  // One outstanding request for a hand-signed statement, for a linked wallet.
  // The signing surfaces await a promise; the prompt resolves it.
  const [pendingSig, setPendingSig] = useState(null)
  const pendingRef = useRef(null)
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

  const setSigner = useCallback((s) => {
    if (s) localStorage.setItem(SIGNER_KEY, JSON.stringify(s))
    else localStorage.removeItem(SIGNER_KEY)
    setSignerState(s)
  }, [])

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

  // Re-attach the browser wallet after a reload. The extension answers silently
  // when this origin is already connected and the wallet unlocked, so a signed-in
  // holder gets their signing ability back without a prompt; if it stays locked,
  // the first signature prompts for the unlock, which is the extension's job and
  // not this page's.
  useEffect(() => {
    if (status !== 'in' || !account?.xonly) return undefined
    if (signer && signer.xonly === account.xonly) return undefined
    let cancelled = false
    ;(async () => {
      const p = await wallet.waitForProvider()
      if (!p || cancelled) return
      try {
        const id = await wallet.connectSilently()
        if (!cancelled && id && id.xonly === account.xonly) {
          setSigner({ kind: 'extension', xonly: id.xonly, aid: id.aid })
        }
      } catch {
        // Not connected, locked, or an older extension: the holder reconnects
        // from the ID page. Nothing here should nag.
      }
    })()
    return () => {
      cancelled = true
    }
  }, [status, account?.xonly, signer, setSigner])

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

  const setPrefs = (patch) => {
    setPrefsState((p) => {
      const next = { ...p, ...patch }
      localStorage.setItem(PREFS_KEY, JSON.stringify(next))
      return next
    })
  }

  // ── signing ─────────────────────────────────────────────────────────
  // Every application signature in SeqPal is a BIP340 signature over a
  // DOMAIN-TAGGED message, and this is the one place that decides who produces
  // it. Nothing below ever sees a private key: an extension wallet is asked, a
  // linked wallet's holder is asked. Both return the same 128-hex signature,
  // and seqpald verifies both the same way.

  // Ask the holder of a linked wallet to sign, and wait for them. Rejecting the
  // prompt rejects the promise, which the calling surface reports as "nothing
  // was signed" — the same shape as the wallet extension refusing.
  const askLinked = (req) =>
    new Promise((resolve, reject) => {
      const entry = { ...req, resolve, reject }
      pendingRef.current = entry
      setPendingSig(entry)
    })

  const resolvePendingSig = (signature) => {
    const p = pendingRef.current
    pendingRef.current = null
    setPendingSig(null)
    p?.resolve(String(signature).trim().toLowerCase())
  }

  const cancelPendingSig = () => {
    const p = pendingRef.current
    pendingRef.current = null
    setPendingSig(null)
    p?.reject(new Error('You cancelled the signature. Nothing was signed.'))
  }

  // The single tagged signer. `statement` is UTF-8 text; `hash` is a 32-byte
  // content address. Exactly one of them, matching the provider protocol and
  // seqpald's two verification paths.
  const signTagged = async (tag, { statement, hash, label } = {}) => {
    if (!signer) return null
    if (signer.kind === 'extension') return wallet.signTagged(tag, { statement, hash, label })
    return askLinked({ tag, statement, hash, label, xonly: signer.xonly })
  }

  // ── identity ────────────────────────────────────────────────────────
  // Proof of possession of the enclave key: fetch a challenge, have the wallet
  // sign it TAGGED, present it. No key and no passphrase ever reaches SeqPal.
  const handshake = async (identity, fn, extra = {}) => {
    const { challenge } = await api.challenge(identity.xonly)
    const sig =
      identity.kind === 'extension'
        ? await wallet.signTagged(wallet.CHALLENGE_TAG, { statement: challenge })
        : await askLinked({ tag: wallet.CHALLENGE_TAG, statement: challenge, xonly: identity.xonly })
    const { account: acct } = await fn({ xonly: identity.xonly, challenge, sig, ...extra })
    setSigner({ kind: identity.kind, xonly: identity.xonly, aid: identity.aid })
    const data = await api.me()
    applyMe(data)
    return acct
  }

  // Attach the browser wallet: connect (which prompts once per origin and
  // doubles as its unlock screen) and read the enclave identity. This does not
  // sign in on its own — the caller decides whether that identity is an existing
  // account to sign into or a new one to register.
  const connectExtension = async () => {
    const id = await wallet.connect()
    return { ...id, kind: 'extension' }
  }

  // Sign in with an identity already attached. Returns null when seqpald has no
  // account for that key, which is the caller's cue to collect a profile and
  // register instead.
  const signIn = async (identity) => {
    try {
      return await handshake(identity, api.login)
    } catch (e) {
      if (e?.status === 404) return null
      throw e
    }
  }

  const registerId = async (identity, { displayName, residence, profile }) =>
    handshake(identity, api.register, {
      kind: 'individual',
      display_name: displayName,
      residence,
      profile,
    })

  const signOut = async () => {
    try {
      await api.logout()
    } catch {
      // A dead session is already signed out; clear the browser side regardless.
    }
    setSigner(null)
    setAccount(null)
    setEntities([])
    setIssuances([])
    setSim({})
    setWatch({})
    setStatus('anon')
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

  // Sign an application statement (a UBO declaration, a market-abuse or listing
  // acknowledgment) under its domain-separation tag.
  const signWithKey = (tag, statement) => signTagged(tag, { statement })

  // E-sign an offering document: the wallet signs the TAGGED document hash (tag
  // openamp-document-v1), never a raw blind digest. `title` only names the
  // document in the wallet's prompt; the signature commits to the hash.
  const signDoc = (docHashHex, title) =>
    signTagged('openamp-document-v1', { hash: docHashHex, label: title })

  // Payout-mandate and closing authorizations, over the canonical `sign_this`
  // statement seqpald returned.
  const signMandateStmt = (statement) => signTagged(MANDATE_TAG, { statement })
  const signCloseStmt = (statement) => signTagged(CLOSE_TAG, { statement })

  // The bearer attestation and the corporate-action holding proof are signed
  // over the sha256 of a canonical JSON statement. keys.js owns that
  // construction so the digest is identical whoever ends up signing it.
  const signBearerStmt = (fields) =>
    signTagged(BEARER_ATTESTATION_TAG, { hash: bearerAttestationDigest(fields) })
  const signHoldingStmt = (fields) =>
    signTagged(HOLDING_PROOF_TAG, { hash: holdingProofDigest(fields) })

  // Co-sign a policy-server spend build. The wallet is handed the TRANSACTION
  // seqpald returned, never the sighashes: it decodes it, recomputes every
  // digest from the enclave leaf itself and refuses on a mismatch, which is what
  // keeps a signing oracle over the enclave key from existing at all. Returns
  // the { input: signature } map the completion endpoint expects.
  //
  // `built` is the response from the build call ({ tx, to_sign, ... }); `leaf`
  // says which spend path this is, and for a clawback `fromAid` is the holder
  // whose enclave output is swept, since the leaf comes from THEIR address.
  const signSpend = async (built, { asset, recipientAid, leaf = 'transfer', fromAid } = {}) => {
    if (!signer) return null
    if (signer.kind !== 'extension') {
      throw new Error(
        'This transfer has to be co-signed by a wallet that can verify the transaction it is ' +
          'signing. Sign in with the browser wallet extension to complete it.'
      )
    }
    const mine = signer.xonly
    for (const ts of built?.to_sign || []) {
      if (ts.pubkey && ts.pubkey.toLowerCase() !== String(mine).toLowerCase()) {
        throw new Error('This asks for a signature from a key you do not hold. Nothing was signed.')
      }
    }
    return wallet.signSpend({
      asset,
      tx: built?.tx,
      toSign: built?.to_sign,
      recipientAid,
      leaf,
      fromAid,
    })
  }

  const signTransferSigs = (built, opts) => signSpend(built, { ...opts, leaf: 'transfer' })
  const signClawbackSigs = (built, opts) => signSpend(built, { ...opts, leaf: 'claw' })

  // Authorize a supervision action (a court-ordered freeze or the lift of one)
  // on a supervised asset this account is the operational key for.
  //
  // The wallet is handed the RECORD, never the message: the node's message is a
  // tagged hash over the asset, the frozen address and the outpoint the record
  // is bound to, so the wallet rebuilds it from those, shows them, and signs its
  // own reconstruction. That is what lets an issuer freeze with one approval
  // without the enclave key ever signing bytes it could not check.
  const signSupervision = async (built) => {
    if (!signer) return null
    if (signer.kind !== 'extension') {
      throw new Error(
        'A freeze has to be authorized by a wallet that can rebuild and check the record it is ' +
          'signing. Sign in with the browser wallet extension to authorize it.'
      )
    }
    const rec = built?.record
    if (!rec) {
      throw new Error(
        'This build did not return what the freeze commits to, which a wallet needs in order to ' +
          'check it. Nothing was signed.'
      )
    }
    return wallet.signSupervision({
      kind: rec.kind,
      asset: rec.asset,
      address: rec.target,
      txid: rec.txid,
      vout: rec.vout,
    })
  }

  // Authorize a NETWORK-ENFORCED (OpenDAMP) policy change: a freeze or a lift
  // that the chain's own rules enforce, rather than a supervision record.
  //
  // What the issuer's key signs there is the TAGGED hash of the policy snapshot,
  // so the wallet is given the snapshot hash and applies the tag itself. The
  // change it represents (which holders, which coins, the reason) is shown on
  // this page before the wallet is asked, which is where it can be read.
  const signPolicySnapshot = async (built) => {
    if (!signer) return null
    const hash = built?.snapshotHash
    const tag = built?.snapshotTag
    if (!hash || !tag) {
      throw new Error(
        'This build did not return the policy snapshot the change commits to, which a wallet ' +
          'needs in order to sign under its own tag. Nothing was signed.'
      )
    }
    return signTagged(tag, {
      hash,
      label: 'Policy update (' + (built.action || 'change') + ')',
    })
  }

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
    prefs,
    setPrefs,
    refresh,
    connectExtension,
    signIn,
    registerId,
    signOut,
    createEntity,
    createIssuance,
    patchIssuance,
    deployIssuance,
    signWithKey,
    signDoc,
    signMandateStmt,
    signCloseStmt,
    signTransferSigs,
    signClawbackSigs,
    signBearerStmt,
    signHoldingStmt,
    signSupervision,
    signPolicySnapshot,
    signer,
    signerKind: signer?.kind || null,
    xonly: signer?.xonly || account?.xonly,
    // Whether this session can produce a signature at all. A wallet is attached
    // or it is not; there is no locked-key state of SeqPal's own any more.
    hasKey: !!signer,
    pendingSig,
    resolvePendingSig,
    cancelPendingSig,
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
