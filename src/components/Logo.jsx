export default function Logo({ className = '', showText = true, light = false }) {
  return (
    <span className={`inline-flex items-center gap-2.5 ${className}`}>
      <svg width="30" height="30" viewBox="0 0 32 32" className="shrink-0" aria-hidden>
        <rect width="32" height="32" rx="8" fill={light ? '#ffffff' : '#0b1220'} />
        <rect x="7" y="7" width="7" height="7" rx="1.6" fill="#F7931A" />
        <rect x="18" y="7" width="7" height="7" rx="1.6" fill="#27c2c9" />
        <rect x="7" y="18" width="7" height="7" rx="1.6" fill="#27c2c9" />
        <rect x="18" y="18" width="7" height="7" rx="1.6" fill="#F7931A" />
      </svg>
      {showText && (
        <span
          className={`text-[1.35rem] font-extrabold tracking-tight ${
            light ? 'text-white' : 'text-ink-900'
          }`}
        >
          Seq<span className="text-btc">Pal</span>
        </span>
      )}
    </span>
  )
}
