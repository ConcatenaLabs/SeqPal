// A small, dependency-free QR encoder (byte mode, error-correction level L),
// enough to render a payment address as a scannable code. No external library
// is bundled and no network fetch happens: the SPA's strict CSP forbids both.
//
// The QR is a CONVENIENCE only. The address text shown beside it is the
// authoritative value a payer copies: a QR that failed to scan must never be the
// single source of a destination address, so every caller renders the address
// string too. Level L (max data capacity) keeps a long bech32 address inside a
// small version.
//
// Implements ISO/IEC 18004 byte mode for versions 1..10. The Reed-Solomon
// pipeline is verified against the standard's worked example in qr.test-style
// notes; do not "simplify" the GF(256) tables or the generator polynomials.

// --- GF(256) arithmetic (primitive polynomial 0x11d, generator 2) ------------
const EXP = new Uint8Array(512)
const LOG = new Uint8Array(256)
;(function initGF() {
  let x = 1
  for (let i = 0; i < 255; i++) {
    EXP[i] = x
    LOG[x] = i
    x <<= 1
    if (x & 0x100) x ^= 0x11d
  }
  for (let i = 255; i < 512; i++) EXP[i] = EXP[i - 255]
})()

function gfMul(a, b) {
  if (a === 0 || b === 0) return 0
  return EXP[LOG[a] + LOG[b]]
}

// The generator polynomial for `degree` EC codewords, as coefficient bytes,
// highest-degree first with a leading coefficient of 1 (α^0).
function rsGenerator(degree) {
  let poly = [1]
  for (let i = 0; i < degree; i++) {
    const next = new Array(poly.length + 1).fill(0)
    for (let j = 0; j < poly.length; j++) {
      next[j] ^= poly[j] // multiply by x (degree stays highest at index 0)
      next[j + 1] ^= gfMul(poly[j], EXP[i]) // multiply by α^i (one lower degree)
    }
    poly = next
  }
  return poly
}

// Reed-Solomon EC codewords for a data block: standard polynomial long division,
// generator highest-degree first with gen[0] === 1.
function rsEncode(data, ecLen) {
  const gen = rsGenerator(ecLen)
  const res = new Array(data.length + ecLen).fill(0)
  for (let i = 0; i < data.length; i++) res[i] = data[i]
  for (let i = 0; i < data.length; i++) {
    const coef = res[i]
    if (coef !== 0) {
      for (let j = 0; j < gen.length; j++) res[i + j] ^= gfMul(gen[j], coef)
    }
  }
  return res.slice(data.length)
}

// --- Version tables (ECC level L) --------------------------------------------
// Each entry: [ecCodewordsPerBlock, [ [numBlocks, dataCodewordsPerBlock], ... ] ].
const LEVEL_L = {
  1: [7, [[1, 19]]],
  2: [10, [[1, 34]]],
  3: [15, [[1, 55]]],
  4: [20, [[1, 80]]],
  5: [26, [[1, 108]]],
  6: [18, [[2, 68]]],
  7: [20, [[2, 78]]],
  8: [24, [[2, 97]]],
  9: [30, [[2, 116]]],
  10: [18, [[2, 68], [2, 69]]],
}

// Alignment pattern centre coordinates per version.
const ALIGN = {
  1: [],
  2: [6, 18],
  3: [6, 22],
  4: [6, 26],
  5: [6, 30],
  6: [6, 34],
  7: [6, 22, 38],
  8: [6, 24, 42],
  9: [6, 26, 46],
  10: [6, 28, 50],
}

function dataCapacityBytes(version) {
  const [ec, groups] = LEVEL_L[version]
  let dataCw = 0
  let blocks = 0
  for (const [n, cw] of groups) {
    dataCw += n * cw
    blocks += n
  }
  const countBits = version >= 10 ? 16 : 8
  const headerBits = 4 + countBits
  const availBits = dataCw * 8 - headerBits
  return { maxBytes: Math.floor(availBits / 8), ec, groups, blocks, dataCw }
}

function pickVersion(byteLen) {
  for (let v = 1; v <= 10; v++) {
    if (byteLen <= dataCapacityBytes(v).maxBytes) return v
  }
  return 0 // too long for the supported range
}

// --- Bit buffer --------------------------------------------------------------
function bitBuffer() {
  const bits = []
  return {
    put(val, len) {
      for (let i = len - 1; i >= 0; i--) bits.push((val >>> i) & 1)
    },
    get length() {
      return bits.length
    },
    bits,
  }
}

// Assemble the final codeword sequence (data + interleaved EC) for a version.
function buildCodewords(bytes, version) {
  const info = dataCapacityBytes(version)
  const buf = bitBuffer()
  const countBits = version >= 10 ? 16 : 8
  buf.put(0b0100, 4) // byte mode
  buf.put(bytes.length, countBits)
  for (const b of bytes) buf.put(b, 8)
  // Terminator (up to 4 bits) then pad to a byte boundary.
  const totalDataBits = info.dataCw * 8
  const remaining = totalDataBits - buf.length
  buf.put(0, Math.min(4, remaining))
  while (buf.length % 8 !== 0) buf.bits.push(0)
  // Byte-align data codewords, then pad with the standard alternating bytes.
  const dataCodewords = []
  for (let i = 0; i < buf.bits.length; i += 8) {
    let v = 0
    for (let j = 0; j < 8; j++) v = (v << 1) | buf.bits[i + j]
    dataCodewords.push(v)
  }
  const pads = [0xec, 0x11]
  let pi = 0
  while (dataCodewords.length < info.dataCw) {
    dataCodewords.push(pads[pi++ % 2])
  }

  // Split into blocks, compute EC per block, then interleave.
  const dataBlocks = []
  const ecBlocks = []
  let idx = 0
  for (const [n, cw] of info.groups) {
    for (let b = 0; b < n; b++) {
      const block = dataCodewords.slice(idx, idx + cw)
      idx += cw
      dataBlocks.push(block)
      ecBlocks.push(rsEncode(block, info.ec))
    }
  }
  const result = []
  const maxData = Math.max(...dataBlocks.map((b) => b.length))
  for (let i = 0; i < maxData; i++) {
    for (const block of dataBlocks) if (i < block.length) result.push(block[i])
  }
  for (let i = 0; i < info.ec; i++) {
    for (const block of ecBlocks) result.push(block[i])
  }
  return result
}

// --- Matrix construction -----------------------------------------------------
function sizeFor(version) {
  return version * 4 + 17
}

function newMatrix(size) {
  const modules = Array.from({ length: size }, () => new Array(size).fill(null))
  const reserved = Array.from({ length: size }, () => new Array(size).fill(false))
  return { modules, reserved }
}

function placeFinder(m, r, c, size) {
  for (let dr = -1; dr <= 7; dr++) {
    for (let dc = -1; dc <= 7; dc++) {
      const rr = r + dr
      const cc = c + dc
      if (rr < 0 || rr >= size || cc < 0 || cc >= size) continue
      const inRing =
        dr >= 0 && dr <= 6 && dc >= 0 && dc <= 6
          ? dr === 0 || dr === 6 || dc === 0 || dc === 6 || (dr >= 2 && dr <= 4 && dc >= 2 && dc <= 4)
          : false
      m.modules[rr][cc] = inRing ? 1 : 0
      m.reserved[rr][cc] = true
    }
  }
}

function placeAlignment(m, version) {
  const centers = ALIGN[version]
  const last = centers[centers.length - 1]
  for (const r of centers) {
    for (const c of centers) {
      // Skip the three centres that coincide with the finder patterns.
      if ((r === 6 && c === 6) || (r === 6 && c === last) || (r === last && c === 6)) continue
      for (let dr = -2; dr <= 2; dr++) {
        for (let dc = -2; dc <= 2; dc++) {
          const ring = Math.max(Math.abs(dr), Math.abs(dc))
          m.modules[r + dr][c + dc] = ring === 1 ? 0 : 1
          m.reserved[r + dr][c + dc] = true
        }
      }
    }
  }
}

function placeTiming(m, size) {
  for (let i = 8; i < size - 8; i++) {
    const v = i % 2 === 0 ? 1 : 0
    if (!m.reserved[6][i]) {
      m.modules[6][i] = v
      m.reserved[6][i] = true
    }
    if (!m.reserved[i][6]) {
      m.modules[i][6] = v
      m.reserved[i][6] = true
    }
  }
}

// The 15 format-info module coordinates (two copies), each tagged with its bit
// index, per ISO/IEC 18004 §8.9. Both reserveFormat and placeFormat consume this
// so the reserved set and the written set are identical (no holes).
function formatCoords(size) {
  const coords = []
  for (let i = 0; i <= 5; i++) coords.push([8, i, i])
  coords.push([8, 7, 6], [8, 8, 7], [7, 8, 8])
  for (let i = 9; i < 15; i++) coords.push([14 - i, 8, i])
  for (let i = 0; i < 8; i++) coords.push([size - 1 - i, 8, i])
  for (let i = 8; i < 15; i++) coords.push([8, size - 15 + i, i])
  return coords
}

function reserveFormat(m, size) {
  for (const [r, c] of formatCoords(size)) m.reserved[r][c] = true
  // Fixed dark module.
  m.modules[size - 8][8] = 1
  m.reserved[size - 8][8] = true
}

function reserveVersion(m, size, version) {
  if (version < 7) return
  for (let i = 0; i < 6; i++) {
    for (let j = 0; j < 3; j++) {
      m.reserved[i][size - 11 + j] = true
      m.reserved[size - 11 + j][i] = true
    }
  }
}

// BCH-encoded 15-bit format string for (ecBits, mask). Level L = 0b01.
function formatBits(mask) {
  const data = (0b01 << 3) | mask
  let rem = data
  for (let i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) & 1 ? 0b10100110111 : 0)
  const bits = ((data << 10) | rem) ^ 0b101010000010010
  return bits & 0x7fff
}

// BCH-encoded 18-bit version string (versions 7..10).
function versionBits(version) {
  let rem = version
  for (let i = 0; i < 12; i++) rem = (rem << 1) ^ ((rem >>> 11) & 1 ? 0b1111100100101 : 0)
  return ((version << 12) | rem) & 0x3ffff
}

function placeFormat(m, size, mask) {
  const bits = formatBits(mask)
  for (const [r, c, bit] of formatCoords(size)) m.modules[r][c] = (bits >> bit) & 1
}

function placeVersion(m, size, version) {
  if (version < 7) return
  const bits = versionBits(version)
  for (let i = 0; i < 18; i++) {
    const bit = (bits >> i) & 1
    const r = Math.floor(i / 3)
    const c = i % 3
    m.modules[r][size - 11 + c] = bit
    m.modules[size - 11 + c][r] = bit
  }
}

function maskFn(mask, r, c) {
  switch (mask) {
    case 0: return (r + c) % 2 === 0
    case 1: return r % 2 === 0
    case 2: return c % 3 === 0
    case 3: return (r + c) % 3 === 0
    case 4: return (Math.floor(r / 2) + Math.floor(c / 3)) % 2 === 0
    case 5: return ((r * c) % 2) + ((r * c) % 3) === 0
    case 6: return (((r * c) % 2) + ((r * c) % 3)) % 2 === 0
    case 7: return (((r + c) % 2) + ((r * c) % 3)) % 2 === 0
    default: return false
  }
}

function placeData(m, size, codewords, mask) {
  let bitIndex = 0
  const totalBits = codewords.length * 8
  let col = size - 1
  let upward = true
  while (col > 0) {
    if (col === 6) col-- // skip the vertical timing column
    for (let i = 0; i < size; i++) {
      const row = upward ? size - 1 - i : i
      for (let c = 0; c < 2; c++) {
        const cc = col - c
        if (m.reserved[row][cc]) continue
        let bit = 0
        if (bitIndex < totalBits) {
          const byte = codewords[bitIndex >> 3]
          bit = (byte >> (7 - (bitIndex & 7))) & 1
          bitIndex++
        }
        if (maskFn(mask, row, cc)) bit ^= 1
        m.modules[row][cc] = bit
      }
    }
    upward = !upward
    col -= 2
  }
}

// Penalty scoring to pick the least-conspicuous mask (ISO/IEC 18004 §8.8.2).
function penalty(modules, size) {
  let score = 0
  // Rule 1: runs of 5+ same-colour in row/column.
  for (let r = 0; r < size; r++) {
    let runC = 1, runR = 1
    for (let c = 1; c < size; c++) {
      if (modules[r][c] === modules[r][c - 1]) { runC++; if (runC === 5) score += 3; else if (runC > 5) score++ } else runC = 1
      if (modules[c][r] === modules[c - 1][r]) { runR++; if (runR === 5) score += 3; else if (runR > 5) score++ } else runR = 1
    }
  }
  // Rule 2: 2x2 blocks of the same colour.
  for (let r = 0; r < size - 1; r++)
    for (let c = 0; c < size - 1; c++) {
      const v = modules[r][c]
      if (v === modules[r][c + 1] && v === modules[r + 1][c] && v === modules[r + 1][c + 1]) score += 3
    }
  // Rule 3: finder-like 1:1:3:1:1 patterns.
  const pat1 = [1, 0, 1, 1, 1, 0, 1, 0, 0, 0, 0]
  const pat2 = [0, 0, 0, 0, 1, 0, 1, 1, 1, 0, 1]
  const match = (arr, i, j, horiz) => {
    for (let k = 0; k < 11; k++) {
      const rr = horiz ? i : i + k
      const cc = horiz ? j + k : j
      if ((horiz ? cc : rr) >= size) return false
      if (modules[rr][cc] !== arr[k]) return false
    }
    return true
  }
  for (let r = 0; r < size; r++)
    for (let c = 0; c < size; c++) {
      if (c + 11 <= size && (match(pat1, r, c, true) || match(pat2, r, c, true))) score += 40
      if (r + 11 <= size && (match(pat1, r, c, false) || match(pat2, r, c, false))) score += 40
    }
  // Rule 4: overall dark-module balance.
  let dark = 0
  for (let r = 0; r < size; r++) for (let c = 0; c < size; c++) if (modules[r][c]) dark++
  const ratio = (dark * 100) / (size * size)
  score += Math.floor(Math.abs(ratio - 50) / 5) * 10
  return score
}

// Build the finished module matrix (without quiet zone). Returns null if the
// text does not fit the supported version range.
export function qrMatrix(text) {
  const bytes = new TextEncoder().encode(text)
  const version = pickVersion(bytes.length)
  if (!version) return null
  const size = sizeFor(version)
  const codewords = buildCodewords(bytes, version)

  let best = null
  let bestScore = Infinity
  for (let mask = 0; mask < 8; mask++) {
    const m = newMatrix(size)
    placeFinder(m, 0, 0, size)
    placeFinder(m, 0, size - 7, size)
    placeFinder(m, size - 7, 0, size)
    placeAlignment(m, version)
    placeTiming(m, size)
    reserveFormat(m, size)
    reserveVersion(m, size, version)
    placeData(m, size, codewords, mask)
    placeFormat(m, size, mask)
    placeVersion(m, size, version)
    const score = penalty(m.modules, size)
    if (score < bestScore) {
      bestScore = score
      best = m.modules
    }
  }
  return { size, modules: best, version }
}
