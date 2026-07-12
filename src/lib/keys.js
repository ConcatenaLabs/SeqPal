// Real Sequentia enclave keys for a SeqPal ID.
//
// A SeqPal ID is a Sequentia identity: one secp256k1 keypair generated in this
// browser, whose x-only public key OpenAMP registers as the account's enclave
// key (the policy server derives the account id, AID, and the 2-of-2 enclave
// address that restricted assets live in from it). The private key is the
// holder's half of that 2-of-2.
//
// The key is persisted only as an AES-GCM envelope under a passphrase-derived
// key (encryptKey below). The plaintext key exists in memory for the session
// and is never written to storage.
import { schnorr } from '@noble/curves/secp256k1'
import { bytesToHex, hexToBytes } from '@noble/curves/abstract/utils'
import { sha256 } from '@noble/hashes/sha256'

const enc = new TextEncoder()

// The OpenAMP account id (AID) derived from a set of x-only pubkeys, matching
// the policy server's store.AID: sha256("openamp-aid-v1" || sorted-pubkey-hex),
// first 20 bytes, hex. For a SeqPal ID that is a single enclave key.
export function computeAID(xonlyHexes) {
  const sorted = [...xonlyHexes].sort()
  const h = sha256.create()
  h.update(enc.encode('openamp-aid-v1'))
  for (const pk of sorted) h.update(enc.encode(pk))
  return bytesToHex(h.digest().slice(0, 20))
}

// Generate a fresh enclave keypair. Returns { priv, xonly } as hex strings.
export function generateEnclaveKey() {
  const priv = schnorr.utils.randomPrivateKey()
  const xonly = schnorr.getPublicKey(priv) // 32-byte x-only
  return { priv: bytesToHex(priv), xonly: bytesToHex(xonly) }
}

// x-only public key (hex) for a stored private key.
export function xonlyOf(privHex) {
  return bytesToHex(schnorr.getPublicKey(hexToBytes(privHex)))
}

// BIP340's tagged hash: sha256(sha256(tag) || sha256(tag) || msg).
export function taggedHash(tag, msg) {
  const t = sha256(enc.encode(tag))
  const h = sha256.create()
  h.update(t)
  h.update(t)
  h.update(msg)
  return h.digest()
}

// Sign a seqpald login challenge. The enclave key signs the TAGGED hash of the
// challenge string, never a raw externally supplied digest: a party that could
// choose the 32 bytes this key signs would hold a signing oracle over transfer
// sighashes and could drain the enclave (openamp spec/venue-wallet-integration
// section 0.4). Tagging turns "make a challenge that is also a spendable
// sighash" into a preimage problem. Aux-rand is zero so the signature is
// deterministic and cross-implementation test vectors are possible.
const NO_AUX = new Uint8Array(32)
export function signChallenge(privHex, challenge) {
  const msg = taggedHash('openamp-challenge-v1', enc.encode(challenge))
  return bytesToHex(schnorr.sign(msg, hexToBytes(privHex), NO_AUX))
}

// Sign an application statement under a domain-separation tag (never a raw
// external digest, for the same signing-oracle reason as signChallenge). Used
// for the UBO declaration that binds a corporate entity to the controlling
// personal ID (tag seqpal-ubo-v1), which seqpald verifies against this key.
export function signStatement(privHex, tag, statement) {
  const msg = taggedHash(tag, enc.encode(statement))
  return bytesToHex(schnorr.sign(msg, hexToBytes(privHex), NO_AUX))
}

// E-signature over an offering document. The signer's enclave key signs the
// TAGGED document hash under tag "openamp-document-v1" (integration spec 0.4(2)):
// message = the 32 raw bytes of the document's content address, never the raw
// hash blind-signed (that would be a signing oracle over an arbitrary digest,
// spec 0.4(3)). seqpald verifies this tagged signature against the signer's
// registered x-only key, then stores and anchors it. The signature is
// cryptographically real; only the provider-grade e-signature UI is simulated.
// docHashHex is the 64-hex content address of the document.
export function signDocument(privHex, docHashHex) {
  const msg = taggedHash('openamp-document-v1', hexToBytes(docHashHex))
  return bytesToHex(schnorr.sign(msg, hexToBytes(privHex), NO_AUX))
}

// ── Encrypted key envelope ─────────────────────────────────────────────
// AES-GCM under PBKDF2-SHA256 (200k iterations, 16-byte salt). The envelope is
// what localStorage holds and what the user exports as a backup file.
const PBKDF2_ITERS = 200_000
export const ENVELOPE_VERSION = 1

async function deriveAesKey(passphrase, salt) {
  const material = await crypto.subtle.importKey(
    'raw',
    enc.encode(passphrase),
    'PBKDF2',
    false,
    ['deriveKey']
  )
  return crypto.subtle.deriveKey(
    { name: 'PBKDF2', salt, iterations: PBKDF2_ITERS, hash: 'SHA-256' },
    material,
    { name: 'AES-GCM', length: 256 },
    false,
    ['encrypt', 'decrypt']
  )
}

export async function encryptKey(privHex, passphrase) {
  const salt = crypto.getRandomValues(new Uint8Array(16))
  const iv = crypto.getRandomValues(new Uint8Array(12))
  const aes = await deriveAesKey(passphrase, salt)
  const ct = await crypto.subtle.encrypt({ name: 'AES-GCM', iv }, aes, hexToBytes(privHex))
  return {
    v: ENVELOPE_VERSION,
    salt: bytesToHex(salt),
    iv: bytesToHex(iv),
    ct: bytesToHex(new Uint8Array(ct)),
  }
}

export async function decryptKey(envelope, passphrase) {
  if (!envelope?.ct || !envelope?.salt || !envelope?.iv) {
    throw new Error('That file is not a SeqPal ID backup.')
  }
  const aes = await deriveAesKey(passphrase, hexToBytes(envelope.salt))
  let plain
  try {
    plain = await crypto.subtle.decrypt(
      { name: 'AES-GCM', iv: hexToBytes(envelope.iv) },
      aes,
      hexToBytes(envelope.ct)
    )
  } catch {
    throw new Error('Wrong passphrase for this SeqPal ID.')
  }
  const priv = bytesToHex(new Uint8Array(plain))
  const xonly = xonlyOf(priv)
  if (envelope.xonly && envelope.xonly !== xonly) {
    throw new Error('This backup is inconsistent: the key does not match its public key.')
  }
  return priv
}

// A passphrase hint, not a security control: the passphrase is the only thing
// between a stolen backup file and a live enclave key.
export function passphraseStrength(p) {
  const s = p || ''
  if (s.length < 10) return { level: 'weak', label: 'Too short: 10 characters minimum' }
  let classes = 0
  if (/[a-z]/.test(s)) classes++
  if (/[A-Z]/.test(s)) classes++
  if (/[0-9]/.test(s)) classes++
  if (/[^a-zA-Z0-9]/.test(s)) classes++
  if (s.length >= 16 && classes >= 3) return { level: 'strong', label: 'Strong' }
  if (s.length >= 12 && classes >= 2) return { level: 'fair', label: 'Fair, longer is better' }
  return { level: 'weak', label: 'Weak: use several words, or add length' }
}
