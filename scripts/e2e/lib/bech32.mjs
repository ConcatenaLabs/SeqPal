// Bech32 / bech32m (BIP173 / BIP350) segwit address encoding for the live
// drill, plus the two address builders the drill needs: the holder's P2TR
// key-path address (witness v1 over the 32-byte x-only key, bech32m) and an
// ordinary P2WPKH payout address (witness v0 over hash160 of the compressed
// key, bech32). Sequentia's default unblinded addresses are byte-identical to
// Bitcoin's format, so the testnet HRP is "tb".
//
// Implemented here because the repo carries no bech32 dependency. The encoder
// is pinned in test/enforcement.test.js against vectors taken from the
// Sequentia node's own src/test/data/key_io_valid.json (chain "test"), so a
// drift from what the node parses fails the suite, not the drill.
import { sha256 } from '@noble/hashes/sha256'
import { ripemd160 } from '@noble/hashes/ripemd160'
import { secp256k1 } from '@noble/curves/secp256k1'
import { hexToBytes, bytesToHex } from '@noble/curves/abstract/utils'

const CHARSET = 'qpzry9x8gf2tvdw0s3jn54khce6mua7l'
const GENERATOR = [0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3]
const BECH32M_CONST = 0x2bc830a3

function polymod(values) {
  let chk = 1
  for (const v of values) {
    const top = chk >>> 25
    chk = ((chk & 0x1ffffff) << 5) ^ v
    for (let i = 0; i < 5; i++) {
      if ((top >>> i) & 1) chk ^= GENERATOR[i]
    }
  }
  return chk >>> 0
}

function hrpExpand(hrp) {
  const out = []
  for (const c of hrp) out.push(c.charCodeAt(0) >>> 5)
  out.push(0)
  for (const c of hrp) out.push(c.charCodeAt(0) & 31)
  return out
}

function createChecksum(hrp, data, constant) {
  const values = [...hrpExpand(hrp), ...data, 0, 0, 0, 0, 0, 0]
  const mod = polymod(values) ^ constant
  const out = []
  for (let i = 0; i < 6; i++) out.push((mod >>> (5 * (5 - i))) & 31)
  return out
}

// Regroup bits: from-bit groups to to-bit groups, left-padded per BIP173.
function convertBits(data, from, to, pad) {
  let acc = 0
  let bits = 0
  const out = []
  const maxv = (1 << to) - 1
  for (const value of data) {
    if (value < 0 || value >>> from !== 0) throw new Error('invalid value for convertBits')
    acc = (acc << from) | value
    bits += from
    while (bits >= to) {
      bits -= to
      out.push((acc >>> bits) & maxv)
    }
  }
  if (pad) {
    if (bits > 0) out.push((acc << (to - bits)) & maxv)
  } else if (bits >= from || (acc << (to - bits)) & maxv) {
    throw new Error('invalid padding in convertBits')
  }
  return out
}

// Encode a segwit address: bech32 for witness v0, bech32m for v1 and above
// (BIP350). programHex is the witness program in hex.
export function encodeSegwit(hrp, version, programHex) {
  if (version < 0 || version > 16) throw new Error('witness version out of range')
  const program = hexToBytes(programHex)
  if (program.length < 2 || program.length > 40) throw new Error('witness program length out of range')
  if (version === 0 && program.length !== 20 && program.length !== 32) {
    throw new Error('a v0 witness program must be 20 or 32 bytes')
  }
  const constant = version === 0 ? 1 : BECH32M_CONST
  const data = [version, ...convertBits(program, 8, 5, true)]
  const combined = [...data, ...createChecksum(hrp, data, constant)]
  return hrp + '1' + combined.map((d) => CHARSET[d]).join('')
}

// Decode a segwit address back to { hrp, version, program } (hex program),
// verifying the checksum under the spec the version requires. Used by the
// tests to prove encode/decode agree, and by the drill to double-check a
// derived address before printing an operator send command.
export function decodeSegwit(addr) {
  const lower = addr.toLowerCase()
  if (lower !== addr && addr.toUpperCase() !== addr) throw new Error('mixed-case address')
  const pos = lower.lastIndexOf('1')
  if (pos < 1 || pos + 7 > lower.length) throw new Error('invalid separator position')
  const hrp = lower.slice(0, pos)
  const data = []
  for (const c of lower.slice(pos + 1)) {
    const d = CHARSET.indexOf(c)
    if (d === -1) throw new Error('invalid character in data part')
    data.push(d)
  }
  const version = data[0]
  const constant = version === 0 ? 1 : BECH32M_CONST
  if (polymod([...hrpExpand(hrp), ...data]) !== constant) throw new Error('checksum mismatch')
  const program = convertBits(data.slice(1, -6), 5, 8, false)
  return { hrp, version, program: bytesToHex(Uint8Array.from(program)) }
}

export function hash160(bytes) {
  return ripemd160(sha256(bytes))
}

// The P2TR key-path address for a 32-byte x-only key: witness v1, bech32m.
// This treats the x-only key AS the output key (key-path, no script tree
// tweak), which is exactly the script seqpald's claim derivation expects
// (5120 || xonly, actions.go scriptForClaimKey).
export function p2trAddress(xonlyHex, hrp = 'tb') {
  if (!/^[0-9a-f]{64}$/.test(xonlyHex)) throw new Error('xonly must be 64 lowercase hex characters')
  return encodeSegwit(hrp, 1, xonlyHex)
}

// The ordinary P2WPKH address for a private key: witness v0 over hash160 of
// the real compressed public key (not an assumed-even prefix), bech32.
export function p2wpkhAddressFromPriv(privHex, hrp = 'tb') {
  const compressed = secp256k1.getPublicKey(privHex, true)
  return encodeSegwit(hrp, 0, bytesToHex(hash160(compressed)))
}

// The compressed public key for a private key, hex. The claim API accepts a
// 66-hex compressed key for P2WPKH outpoints; exposing it here keeps the
// drill's key handling in one place.
export function compressedPubkey(privHex) {
  return bytesToHex(secp256k1.getPublicKey(privHex, true))
}
