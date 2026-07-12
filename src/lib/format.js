// Unit-aware, float-safe formatting. Atoms are the internal integer unit for
// every asset; a display figure is atoms scaled down by the asset's precision.
// BTC (the parent chain, a first-class asset) is 8-decimal: 40,000,000 sats is
// 0.4 BTC, never 0. All scaling is done on strings so a large supply or an
// 8-decimal fraction never loses a digit to floating point.

// Group the integer part in threes without going through Number (so a supply
// past 2^53 still groups correctly).
function group(intStr) {
  const neg = intStr.startsWith('-')
  const digits = neg ? intStr.slice(1) : intStr
  return (neg ? '-' : '') + digits.replace(/\B(?=(\d{3})+(?!\d))/g, ',')
}

// Scale an integer atom count down by `decimals`, returning a plain decimal
// string (no grouping, no float). scaleDown(40000000, 8) === "0.40000000".
export function scaleDown(atomsLike, decimals) {
  let s = String(atomsLike ?? '0').trim()
  const neg = s.startsWith('-')
  s = s.replace(/[^0-9]/g, '') || '0'
  const d = Math.max(0, Number(decimals) || 0)
  if (d === 0) return (neg ? '-' : '') + s
  s = s.padStart(d + 1, '0')
  const whole = s.slice(0, s.length - d)
  const frac = s.slice(s.length - d)
  return (neg ? '-' : '') + whole + '.' + frac
}

// Drop trailing zeros in the fractional part (and a bare trailing dot).
export function trimZeros(dec) {
  if (!dec.includes('.')) return dec
  return dec.replace(/0+$/, '').replace(/\.$/, '')
}

// A raw integer atom count, grouped, e.g. "1,000,000 atoms".
export function fmtAtoms(atoms) {
  return group(String(Math.trunc(Number(atoms) || 0))) + ' atoms'
}

// An asset amount from atoms, respecting the asset's precision, optionally
// suffixed with its ticker. Trailing zeros are trimmed for readability but a
// real fraction is always shown in full.
export function fmtAssetAmount(atoms, precision, ticker) {
  const dec = trimZeros(scaleDown(atoms, precision))
  const [w, f] = dec.split('.')
  const out = f !== undefined ? group(w) + '.' + f : group(w)
  return ticker ? `${out} ${ticker}` : out
}

// BTC from sats: full 8-decimal, grouped. Kept unambiguous (0.40000000) so a
// fractional balance is never misread as zero.
export function fmtBTC(sats) {
  const dec = scaleDown(sats, 8)
  const [w, f] = dec.split('.')
  return group(w) + '.' + f + ' BTC'
}

// A reference-currency figure fed by the price server: value in its quote.
// USD and BTC get their conventional symbol; any other quote is shown as a
// suffixed ticker (SEQ has no privileged symbol, it is one quote among equals).
export function fmtReference(value, quote) {
  const n = Number(value) || 0
  const q = String(quote || 'USD').toUpperCase()
  if (q === 'USD') {
    // A tiny value (a valuable asset's fee can be a fraction of a cent, which is
    // correct, not a bug) must not round to $0; keep two significant figures.
    if (n !== 0 && Math.abs(n) < 0.01)
      return '$' + n.toLocaleString('en-US', { maximumSignificantDigits: 2 })
    return '$' + n.toLocaleString('en-US', { maximumFractionDigits: 2 })
  }
  if (q === 'BTC') return trimZeros(n.toFixed(8)) + ' BTC'
  return n.toLocaleString('en-US', { maximumFractionDigits: 8 }) + ' ' + q
}
