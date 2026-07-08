import { Icon } from './icons'

export function SectionHeading({ eyebrow, title, sub, center, className = '' }) {
  return (
    <div className={`${center ? 'mx-auto max-w-2xl text-center' : 'max-w-2xl'} ${className}`}>
      {eyebrow && <div className="eyebrow">{eyebrow}</div>}
      <h2 className="mt-3 text-3xl font-extrabold tracking-tight text-ink-900 sm:text-4xl">
        {title}
      </h2>
      {sub && <p className="mt-4 text-lg leading-relaxed text-ink-700/90">{sub}</p>}
    </div>
  )
}

export function Badge({ children, color = 'btc', className = '' }) {
  const map = {
    btc: 'bg-btc/10 text-btc-700',
    seq: 'bg-seq/10 text-seq-600',
    emerald: 'bg-emerald-50 text-emerald-700',
    amber: 'bg-amber-50 text-amber-700',
    rose: 'bg-rose-50 text-rose-700',
    slate: 'bg-ink-900/[0.06] text-ink-700',
  }
  return <span className={`chip ${map[color]} ${className}`}>{children}</span>
}

// Reusable "this part is mocked in the demo" notice.
export function DemoNote({ children, className = '' }) {
  return (
    <div
      className={`flex items-start gap-3 rounded-xl border border-btc/20 bg-btc-50 px-4 py-3 text-sm text-btc-700 ${className}`}
    >
      <Icon.spark width={18} height={18} className="mt-0.5 shrink-0" />
      <p className="leading-relaxed">{children}</p>
    </div>
  )
}

export function Stat({ value, label }) {
  return (
    <div>
      <div className="text-3xl font-extrabold tracking-tight text-ink-900">{value}</div>
      <div className="mt-1 text-sm text-ink-700/80">{label}</div>
    </div>
  )
}
