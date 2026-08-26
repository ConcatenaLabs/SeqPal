// Just enough BIP32 to be a wallet.
//
// A SeqPal ID that is only a wallet is identified by a RANGED descriptor -- an
// account xpub with a receive chain -- because that is what a wallet is: a key
// that makes addresses, not one address. Proving it means signing a message
// with the private key behind the first of those addresses.
//
// So a driver that plays such a holder has to derive, and this is the smallest
// thing that does: a master key from a seed, hardened derivation down to the
// account, the serialised account tpub, and the child private key for a receive
// index. Nothing here is a wallet feature of SeqPal's -- it is test scaffolding
// that lets the drill speak the same language as a real wallet.
import { secp256k1 } from '@noble/curves/secp256k1'
import { hmac } from '@noble/hashes/hmac'
import { sha512 } from '@noble/hashes/sha512'
import { sha256 } from '@noble/hashes/sha256'
import { ripemd160 } from '@noble/hashes/ripemd160'
import { bytesToHex, hexToBytes, concatBytes } from '@noble/curves/abstract/utils'

// Sequentia's testnet extended-key versions, the same as Bitcoin's testnet
// (chainparams.cpp, the `test` chain): tpub and tprv.
const XPUB_VERSION = 0x043587cf

const B58 = '123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz'

function base58check(payload) {
  const checksum = sha256(sha256(payload)).slice(0, 4)
  const full = concatBytes(payload, checksum)
  let n = 0n
  for (const b of full) n = n * 256n + BigInt(b)
  let out = ''
  while (n > 0n) {
    out = B58[Number(n % 58n)] + out
    n /= 58n
  }
  for (const b of full) {
    if (b !== 0) break
    out = '1' + out
  }
  return out
}

const be32 = (n) => Uint8Array.of((n >>> 24) & 255, (n >>> 16) & 255, (n >>> 8) & 255, n & 255)
const hash160 = (b) => ripemd160(sha256(b))
const pub = (priv) => secp256k1.getPublicKey(priv, true)

function masterFrom(seed) {
  const I = hmac(sha512, new TextEncoder().encode('Bitcoin seed'), seed)
  return { priv: I.slice(0, 32), chain: I.slice(32), depth: 0, index: 0, parentFp: be32(0) }
}

function derive(node, index) {
  const hardened = index >= 0x80000000
  const data = hardened
    ? concatBytes(Uint8Array.of(0), node.priv, be32(index))
    : concatBytes(pub(node.priv), be32(index))
  const I = hmac(sha512, node.chain, data)
  const tweak = secp256k1.utils.normPrivateKeyToScalar(I.slice(0, 32))
  const parent = secp256k1.utils.normPrivateKeyToScalar(node.priv)
  const child = (tweak + parent) % secp256k1.CURVE.n
  if (child === 0n) throw new Error('invalid child key, pick another seed')
  const priv = hexToBytes(child.toString(16).padStart(64, '0'))
  return {
    priv,
    chain: I.slice(32),
    depth: node.depth + 1,
    index,
    parentFp: hash160(pub(node.priv)).slice(0, 4),
  }
}

function serializeXpub(node) {
  return base58check(
    concatBytes(
      be32(XPUB_VERSION),
      Uint8Array.of(node.depth),
      node.parentFp,
      be32(node.index),
      node.chain,
      pub(node.priv),
    ),
  )
}

const H = (n) => n + 0x80000000

// A wallet at m/44'/1'/0': the account xpub with its key origin, the ranged
// receive descriptor SeqPal signs people in with, and the private key for any
// receive index so the drill can sign as that address.
export function walletFromSeed(seedHex) {
  const master = masterFrom(hexToBytes(seedHex))
  const fingerprint = bytesToHex(hash160(pub(master.priv)).slice(0, 4))
  let node = master
  for (const i of [H(44), H(1), H(0)]) node = derive(node, i)
  const account = node
  const xpub = serializeXpub(account)
  return {
    fingerprint,
    xpub,
    descriptor: `pkh([${fingerprint}/44h/1h/0h]${xpub}/0/*)`,
    // The private key behind receive address `index`.
    receiveKey(index) {
      return bytesToHex(derive(derive(account, 0), index).priv)
    },
  }
}
