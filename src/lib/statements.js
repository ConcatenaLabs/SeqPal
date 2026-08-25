// What a SeqPal ID signs, and the exact bytes of it.
//
// SeqPal holds no keys. Every signature here is produced by the holder's own
// Sequentia wallet (see wallet.js) over a message this module defines, so this
// file's job is to make the signed bytes reproducible: seqpald recomputes them
// from the same fields and verifies the signature against the account's
// registered x-only enclave key.
//
// Every message is signed TAGGED — the signature is over
// sha256(sha256(tag) || sha256(tag) || message) — which is what keeps an
// application signature from ever being mistakable for a spend. A taproot
// sighash is the same construction under the tag "TapSighash", so a statement
// signed under any tag below cannot collide with one.
import { bytesToHex } from '@noble/curves/abstract/utils'
import { sha256 } from '@noble/hashes/sha256'
import { canonicalJSON } from './openamp.js'

const enc = new TextEncoder()

// sha256 of a canonical JSON object (sorted keys, no whitespace), hex. Canonical
// JSON is what makes the signed bytes reproducible server-side from the same
// fields rather than from the same string.
function canonicalDigest(obj) {
  return bytesToHex(sha256(enc.encode(canonicalJSON(obj))))
}

// The BIP340 tags for the platform-layer statements a SeqPal ID signs, mirroring
// the server constants (secondary.go / listings.go / platform.go / closing.go).
// The statement itself is the exact canonical `sign_this` bytes seqpald returns.
export const MARKET_ABUSE_TAG = 'seqpal-market-abuse-ack-v1'
export const LISTING_TAG = 'seqpal-listing-v1'
export const MANDATE_TAG = 'seqpal-payout-mandate-v1'
export const CLOSE_TAG = 'seqpal-close-v1'
export const UBO_TAG = 'seqpal-ubo-v1'
export const DOCUMENT_TAG = 'openamp-document-v1'

// The bearer attestation an issuer signs before a freely-tradable deploy: over
// sha256 of the canonical JSON of { issuance_id, no_us_nexus, risk_accepted,
// aid }, verified by seqpald against the session key and recorded with the
// issuance.
export const BEARER_ATTESTATION_TAG = 'seqpal-bearer-attestation-v1'
export function bearerAttestationDigest({ issuance_id, no_us_nexus, risk_accepted, aid }) {
  return canonicalDigest({
    issuance_id,
    no_us_nexus: !!no_us_nexus,
    risk_accepted: !!risk_accepted,
    aid,
  })
}

// The holding proof an investor signs to claim a corporate action (dividend
// payout or vote), mirroring seqpald's holdingProofStatement exactly: v, the
// action and its asset, the record height, the SORTED outpoints, the purpose,
// and the presenting session's aid. The purpose and payout/choice fields bind
// the proof to exactly this use and the aid to this session, so a dividend proof
// can never be replayed as a ballot or by another account. The claimant key
// itself is bound by script derivation (the snapshot outputs must pay a script
// derived from it), not by the message.
export const HOLDING_PROOF_TAG = 'seqpal-holding-proof-v1'
export function holdingProofDigest({
  action_id,
  asset,
  record_height,
  outpoints,
  purpose,
  payout_address,
  choice,
  aid,
}) {
  const obj = {
    v: 1,
    action_id,
    asset,
    record_height: Number(record_height),
    outpoints: [...outpoints].sort(),
    purpose,
    aid,
  }
  if (purpose === 'dividend') obj.payout_address = payout_address
  else obj.choice = choice
  return canonicalDigest(obj)
}

// An x-only enclave public key as a wallet reports it: 32 bytes, hex.
export function isXonly(v) {
  return /^[0-9a-f]{64}$/i.test(String(v || '').trim())
}
