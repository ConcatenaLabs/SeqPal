// A SeqPal ID is the enclave key of a Sequentia wallet its holder already has.
//
// SeqPal is not a wallet. It does not generate keys, does not hold them, does
// not unlock them, and has no backup of its own to lose: the identity behind a
// SeqPal ID is the OpenAMP enclave account the wallet already derives at m/5/0,
// registered with the policy server, which derives the account id (AID) and the
// 2-of-2 enclave address restricted assets live in from it. That is the same
// account the holder sees in their wallet, so a security token delivered to a
// SeqPal ID is a token they can actually see and move.
//
// Two ways to attach one:
//
//   'extension'  a wallet that injects window.sequentia. SeqPal asks it for the
//                identity and for each signature; the key never enters this page.
//   'linked'     any other Sequentia wallet. SeqPal holds only the x-only public
//                key and asks the holder to produce each signature in their own
//                wallet, then paste it back. Nothing about it is SeqPal-specific:
//                the wallet signs a domain-tagged statement, which is what
//                seqpald verifies either way.
//
// There is deliberately no third way. A key generated in a browser tab is a
// wallet SeqPal would be running on the holder's behalf, and losing half of a
// 2-of-2 to a cleared cache is not a failure mode a tokenization platform gets
// to hand its users.

export const CHALLENGE_TAG = 'openamp-challenge-v1'

// The methods a wallet extension must expose for a SeqPal ID. An older build
// that predates them can still be installed, and saying so plainly beats a
// method-not-found error from deep inside a signing flow.
const REQUIRED = ['openampGetIdentity', 'openampSignTagged', 'openampSignSpend']

export function provider() {
  const p = typeof window !== 'undefined' ? window.sequentia : null
  return p && p.isSequentia ? p : null
}

// The content script injects at document_start, but a page that renders before
// the extension has run would see nothing; wait briefly for its ready event
// rather than deciding on the first paint that no wallet is installed.
export function waitForProvider(ms = 1500) {
  const now = provider()
  if (now) return Promise.resolve(now)
  return new Promise((resolve) => {
    let done = false
    const finish = () => {
      if (done) return
      done = true
      window.removeEventListener('sequentia#initialized', finish)
      resolve(provider())
    }
    window.addEventListener('sequentia#initialized', finish)
    setTimeout(finish, ms)
  })
}

async function request(method, params) {
  const p = provider()
  if (!p) throw new Error('No Sequentia wallet extension is available in this browser.')
  return p.request({ method, params })
}

export async function capabilities() {
  return request('getCapabilities', {})
}

// Which of the required methods this wallet is missing, if any.
export async function missingMethods() {
  let caps
  try {
    caps = await capabilities()
  } catch {
    return REQUIRED
  }
  const have = new Set(caps?.methods || [])
  return REQUIRED.filter((m) => !have.has(m))
}

// Connect (prompts once per origin, and doubles as the unlock prompt), then
// read the identity. Returns { aid, xonly }.
export async function connect() {
  const missing = await missingMethods()
  if (missing.length) {
    throw new Error(
      'This wallet extension is too old for a SeqPal ID: it is missing ' +
        missing.join(', ') +
        '. Update it and try again.'
    )
  }
  await request('connect', {})
  const id = await request('openampGetIdentity', {})
  if (!id?.xonly || !id?.aid) throw new Error('The wallet returned no OpenAMP identity.')
  return { aid: id.aid, xonly: id.xonly }
}

// A BIP340 signature over a domain-tagged message. Exactly one of statement
// (UTF-8 text) and hash (a 32-byte content address) — the same two forms the
// provider protocol takes, and the same two seqpald verifies.
export async function signTagged(tag, { statement, hash, label } = {}) {
  const params = { tag }
  if (statement !== undefined) params.statement = statement
  if (hash !== undefined) params.hash = hash
  if (label) params.label = label
  const res = await request('openampSignTagged', params)
  if (!res?.signature) throw new Error('The wallet returned no signature.')
  return res.signature
}

// Co-sign an enclave spend seqpald built. The wallet is handed the TRANSACTION,
// never a digest: it decodes it, recomputes every sighash from the enclave leaf
// itself, and refuses on a mismatch. `leaf` selects which spend path is being
// taken ('transfer' for a holder-to-holder transfer, 'claw' for an issuer
// clawback), and for a clawback `fromAid` is the holder whose enclave output is
// being swept, since the leaf and control block come from THEIR address.
// Returns the { input: signature } map seqpald's completion endpoints expect.
export async function signSpend({ asset, tx, toSign, recipientAid, leaf, fromAid }) {
  if (!tx) {
    throw new Error(
      'This build of seqpald did not return the transaction, which a wallet needs in order to ' +
        'verify what it is signing. Nothing was signed.'
    )
  }
  const res = await request('openampSignSpend', { asset, tx, toSign, recipientAid, leaf, fromAid })
  if (!res?.sigs) throw new Error('The wallet returned no signatures.')
  return res.sigs
}

// Re-attach after a page reload, without prompting. getAccounts is silent by
// contract: it answers with nothing at all when this origin is not connected or
// the wallet is locked, which is exactly the "say nothing" case here.
export async function connectSilently() {
  const p = provider()
  if (!p) return null
  const { accounts } = await p.request({ method: 'getAccounts', params: {} })
  if (!accounts || !accounts.length) return null
  const id = await p.request({ method: 'openampGetIdentity', params: {} })
  return id?.xonly && id?.aid ? { aid: id.aid, xonly: id.xonly } : null
}

// Authorize a supervised asset's freeze, pause or lift, with the wallet's key in
// its role as the asset's OPERATIONAL key. The wallet is given the fields the
// node's message commits to, never the message: it rebuilds the record from the
// asset, the address and the outpoint, shows them, and signs its own
// reconstruction. Misdescribe any of them and the network rejects the signature
// rather than enforcing a freeze the issuer did not intend.
export async function signSupervision({ kind, asset, address, txid, vout }) {
  if (!asset || txid === undefined || vout === undefined) {
    throw new Error(
      'This build of seqpald did not return what the freeze commits to, which a wallet needs in ' +
        'order to rebuild and check it. Nothing was signed.'
    )
  }
  const res = await request('openampSignSupervision', { kind, asset, address, txid, vout })
  if (!res?.signature) throw new Error('The wallet returned no signature.')
  return res.signature
}
