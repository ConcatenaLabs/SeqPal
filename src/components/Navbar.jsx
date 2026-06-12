import { useState } from 'react'
import { Link, NavLink } from 'react-router-dom'
import Logo from './Logo'
import { Icon } from './icons'
import { useStore } from '../lib/store'

const links = [
  { to: '/products', label: 'Products' },
  { to: '/structures', label: 'Issuance Structures' },
  { to: '/pricing', label: 'Pricing' },
  { to: '/id', label: 'SeqPal ID' },
]

export default function Navbar() {
  const [open, setOpen] = useState(false)
  const { isLoggedIn, account } = useStore()

  return (
    <header className="sticky top-0 z-40 border-b border-ink-900/10 bg-white/80 backdrop-blur-lg">
      <div className="container-x flex h-16 items-center justify-between gap-4">
        <Link to="/" className="shrink-0" onClick={() => setOpen(false)}>
          <Logo />
        </Link>

        <nav className="hidden items-center gap-1 lg:flex">
          {links.map((l) => (
            <NavLink
              key={l.to}
              to={l.to}
              className={({ isActive }) =>
                `rounded-lg px-3 py-2 text-sm font-medium transition-colors ${
                  isActive
                    ? 'text-btc-600'
                    : 'text-ink-700 hover:bg-ink-900/5 hover:text-ink-900'
                }`
              }
            >
              {l.label}
            </NavLink>
          ))}
        </nav>

        <div className="hidden items-center gap-2 lg:flex">
          {isLoggedIn ? (
            <Link to="/dashboard" className="btn-ghost gap-2">
              <span className="flex h-6 w-6 items-center justify-center rounded-full bg-btc-50 text-xs font-bold text-btc-700">
                {account.individual.name.slice(0, 1).toUpperCase()}
              </span>
              Dashboard
            </Link>
          ) : (
            <Link to="/id" className="btn-ghost">
              Sign in
            </Link>
          )}
          <Link to="/onboarding" className="btn-primary">
            Launch an issuance
            <Icon.arrowRight width={16} height={16} />
          </Link>
        </div>

        <button
          className="btn-ghost px-2 lg:hidden"
          onClick={() => setOpen((o) => !o)}
          aria-label="Toggle menu"
        >
          {open ? <Icon.close /> : <Icon.menu />}
        </button>
      </div>

      {open && (
        <div className="border-t border-ink-900/10 bg-white lg:hidden">
          <div className="container-x flex flex-col gap-1 py-3">
            {links.map((l) => (
              <Link
                key={l.to}
                to={l.to}
                onClick={() => setOpen(false)}
                className="rounded-lg px-3 py-2.5 text-sm font-medium text-ink-800 hover:bg-ink-900/5"
              >
                {l.label}
              </Link>
            ))}
            <div className="mt-2 flex flex-col gap-2">
              <Link
                to={isLoggedIn ? '/dashboard' : '/id'}
                onClick={() => setOpen(false)}
                className="btn-outline w-full"
              >
                {isLoggedIn ? 'Dashboard' : 'Sign in'}
              </Link>
              <Link to="/onboarding" onClick={() => setOpen(false)} className="btn-primary w-full">
                Launch an issuance
              </Link>
            </div>
          </div>
        </div>
      )}
    </header>
  )
}
