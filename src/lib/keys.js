// Real Sequentia enclave keys for a SeqPal ID.
//
// A SeqPal ID is not just a login: it is a Sequentia identity. On registration
// we generate a real secp256k1 keypair; its x-only public key is what OpenAMP
// registers as the account's enclave key (from which the policy server derives
// the account id, AID, and the 2-of-2 enclave address that restricted assets
// live in). The private key is what the holder would later use to co-sign
// transfers out of the enclave.
//
// PoC posture: this is a public testnet. The private key is kept in the
// browser's localStorage next to the account. It never leaves the browser; only
// the x-only public key is sent to the policy server. A production wallet would
// keep this key in a hardware signer or the native wallet, exactly as the rest
// of the Sequentia stack does.
import { schnorr } from '@noble/curves/secp256k1'
import { bytesToHex, hexToBytes } from '@noble/curves/abstract/utils'
import { sha256 } from '@noble/hashes/sha256'

// The OpenAMP account id (AID) derived from a set of x-only pubkeys, matching
// the policy server's store.AID: sha256("openamp-aid-v1" || sorted-pubkey-hex),
// first 20 bytes, hex. For a SeqPal ID that is a single enclave key.
export function computeAID(xonlyHexes) {
  const sorted = [...xonlyHexes].sort()
  const h = sha256.create()
  h.update(new TextEncoder().encode('openamp-aid-v1'))
  for (const pk of sorted) h.update(new TextEncoder().encode(pk))
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

// BIP340 Schnorr signature over a 32-byte sighash (hex in, hex out). Used when
// the holder co-signs a transfer the policy server has built.
export function signSighash(privHex, sighashHex) {
  return bytesToHex(schnorr.sign(hexToBytes(sighashHex), hexToBytes(privHex)))
}
