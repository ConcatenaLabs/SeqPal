// A SeqPal identity for the acceptance driver. A SeqPal ID is the enclave key of
// the holder's own Sequentia wallet, so the driver has to play the wallet's part:
// wallet-signer.mjs holds the key and produces exactly what a wallet produces,
// over the message constructions that ship in src/lib/statements.js. That is the
// whole point of the acceptance driver, to prove the deployed server accepts
// exactly what a real holder's wallet produces.
//
// The signing guards (never sign an input whose pubkey is not this key's own
// x-only) mirror store.jsx signSpend verbatim, so the driver cannot be turned
// into a signing oracle any more than a wallet can.
import { readFile } from 'node:fs/promises'
import {
  computeAID,
  generateEnclaveKey,
  signChallenge,
  signClawbackSighash,
  signClosing,
  signMandate,
  signSighash,
  signStatement,
  xonlyOf,
} from './wallet-signer.mjs'

export class SeqPalID {
  constructor(priv, label = 'id') {
    this.priv = priv
    this.xonly = xonlyOf(priv)
    this.aid = computeAID([this.xonly])
    this.label = label
  }

  // A fresh ephemeral identity. Used in --dry-run (no server writes) and for the
  // beneficiary/investor personas a run mints on the fly.
  static fresh(label) {
    const { priv } = generateEnclaveKey()
    return new SeqPalID(priv, label)
  }

  // Load a persisted SeqPal ID backup (the exact encrypted envelope the SPA
  // exports: keys.js encryptKey / store.jsx downloadEnvelope) and decrypt it with
  // a passphrase. Both the path and the passphrase come from the caller's env,
  // never inlined, so no secret lives in this repo.
  static async fromEnvelope(path, passphrase, label) {
    const env = JSON.parse(await readFile(path, 'utf8'))
    const priv = await decryptKey(env, passphrase)
    return new SeqPalID(priv, label)
  }

  // Proof of possession, exactly store.jsx handshake: fetch a challenge, sign it
  // TAGGED (signChallenge), present it to register or login, keep the session
  // cookie in the client's jar. `kind` extras (display_name, residence, profile)
  // ride along for register.
  async handshake(http, mode /* 'register' | 'login' */, extra = {}) {
    const { challenge } = await http.post('/seqpal/api/auth/challenge', { xonly: this.xonly })
    const sig = signChallenge(this.priv, challenge)
    const path = mode === 'register' ? '/seqpal/api/auth/register' : '/seqpal/api/auth/login'
    const { account } = await http.post(path, { xonly: this.xonly, challenge, sig, ...extra })
    return account
  }

  // Sign a login challenge WITHOUT presenting it (dry-run shaping proof).
  challengeSig(challenge) {
    return signChallenge(this.priv, challenge)
  }

  // Sign the exact `sign_this` bytes seqpald returns for a payout mandate / a
  // closing authorization (tagged, matching keys.MANDATE_TAG / CLOSE_TAG).
  mandateSig(statement) {
    return signMandate(this.priv, statement)
  }

  closingSig(statement) {
    return signClosing(this.priv, statement)
  }

  // Sign an application statement under an explicit tag (market-abuse ack,
  // listing grant). Identical to store.jsx signWithKey(tag, statement).
  statementSig(tag, statement) {
    return signStatement(this.priv, tag, statement)
  }

  // Turn openampd's to_sign list into the { input: signature } map the transfer
  // complete endpoint expects, refusing any input whose pubkey is not this key's
  // own x-only. This is store.jsx signTransferSigs, verbatim.
  transferSigs(toSign) {
    const sigs = {}
    for (const ts of toSign || []) {
      if (ts.pubkey && ts.pubkey.toLowerCase() !== this.xonly.toLowerCase()) {
        throw new Error('This transfer asks for a signature from a key you do not hold. Nothing was signed.')
      }
      sigs[String(ts.input)] = signSighash(this.priv, ts.sighash)
    }
    return sigs
  }

  // The two-phase (external-issuer) clawback co-signature map. store.jsx
  // signClawbackSigs, verbatim: the distinct clawback-spend signer, same guard.
  clawbackSigs(toSign) {
    const sigs = {}
    for (const ts of toSign || []) {
      if (ts.pubkey && ts.pubkey.toLowerCase() !== this.xonly.toLowerCase()) {
        throw new Error('This clawback asks for a signature from a key you do not hold. Nothing was signed.')
      }
      sigs[String(ts.input)] = signClawbackSighash(this.priv, ts.sighash)
    }
    return sigs
  }
}
