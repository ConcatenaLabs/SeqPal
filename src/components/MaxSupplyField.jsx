// The most there will ever be of a token, asked once, at deploy.
//
// A token this platform services can have more of it created later, through
// the policy server, but never past this number: nobody can raise it after the
// deploy, the issuer and the platform included. That is the issuer's power to
// dilute every holder, so it is asked as a plain question with its consequence
// beside it. The default is the initial supply, which makes the supply final:
// a power over holders is something an issuer chooses to keep, not something
// they keep by not noticing a field.
//
// A token whose rules the network enforces cannot have more created later at
// all, so for it this states that instead of asking.

function whole(v) {
  const n = Number(String(v ?? '').replace(/[^0-9]/g, ''))
  return Number.isFinite(n) ? n : 0
}

export function maxSupplyOf(value, supply) {
  return value === '' || value == null ? whole(supply) : whole(value)
}

export default function MaxSupplyField({ id = 'dep-max-supply', supply, value, onChange, ticker, enforcement }) {
  const t = ticker || 'tokens'
  const initial = whole(supply)
  if (enforcement === 'bearer') return null
  if (enforcement === 'network') {
    return (
      <div className="mt-4 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] px-4 py-3 text-xs leading-relaxed text-ink-700/85">
        <span className="font-medium text-ink-900">The most there will ever be: </span>
        {initial ? initial.toLocaleString() : 'what you create now'} {t}. A token whose rules the network
        enforces cannot have more of it created later, by you or by anyone, so what you create now is all
        there will ever be.
      </div>
    )
  }
  const max = maxSupplyOf(value, supply)
  const extra = max - initial
  return (
    <div className="mt-4">
      <label className="label" htmlFor={id}>
        The most there will ever be of this token (whole tokens)
      </label>
      <input
        id={id}
        className="input"
        inputMode="numeric"
        value={value === '' || value == null ? String(initial || '') : value}
        onChange={(e) => onChange(e.target.value)}
      />
      <p className="mt-2 text-xs leading-relaxed text-ink-700/70">
        This counts the {initial ? initial.toLocaleString() : ''} {t} you create now. Later you can create more
        only up to this number, and once you deploy nobody can raise it, you included. It starts at your
        initial supply, so the supply is final unless you change it here. Every token you allow above that is
        one you can later create, and creating it makes each holder&rsquo;s share smaller; your holders see this
        number.
      </p>
      <p
        data-testid="max-supply-consequence"
        className={
          'mt-2 rounded-lg px-3 py-2 text-xs font-medium ' +
          (extra < 0 ? 'bg-rose-50 text-rose-700' : 'bg-ink-900/[0.03] text-ink-900')
        }
      >
        {extra < 0
          ? `This is less than the ${initial.toLocaleString()} ${t} you are creating now. Make it at least that.`
          : extra === 0
            ? `Final: no more ${t} can ever be created after this deploy.`
            : `You can create up to ${extra.toLocaleString()} more ${t} later, and never more: at most ${max.toLocaleString()} ${t} will ever exist.`}
      </p>
    </div>
  )
}
