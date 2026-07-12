import { useMemo } from 'react'
import { qrMatrix } from '../lib/qr'

// Render a payment address as a scannable QR (self-contained SVG, no external
// asset, CSP-safe). The QR is a CONVENIENCE: the address text is always shown
// beside it as the authoritative value a payer copies, so a code that failed to
// scan can never be the sole source of a destination address.
export default function QrCode({ value, size = 160, className = '' }) {
  const qr = useMemo(() => (value ? qrMatrix(value) : null), [value])
  if (!qr) {
    return (
      <div
        className={`flex items-center justify-center rounded-lg border border-ink-900/10 bg-white p-3 text-center text-[11px] text-ink-700/60 ${className}`}
        style={{ width: size, height: size }}
      >
        Address is too long to show as a QR. Copy the text below.
      </div>
    )
  }
  const quiet = 4
  const dim = qr.size + quiet * 2
  const rects = []
  for (let r = 0; r < qr.size; r++) {
    for (let c = 0; c < qr.size; c++) {
      if (qr.modules[r][c]) rects.push(`M${c + quiet},${r + quiet}h1v1h-1z`)
    }
  }
  return (
    <svg
      width={size}
      height={size}
      viewBox={`0 0 ${dim} ${dim}`}
      shapeRendering="crispEdges"
      role="img"
      aria-label="Payment address QR code"
      className={`rounded-lg border border-ink-900/10 bg-white ${className}`}
    >
      <rect width={dim} height={dim} fill="#ffffff" />
      <path d={rects.join('')} fill="#0f172a" />
    </svg>
  )
}
