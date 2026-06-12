import { Link, NavLink } from 'react-router-dom'
import { Icon } from './icons'
import { useStore } from '../lib/store'

// Header for the standalone SeqPal ID subsite (conceptually id.seqpal.io).
// Deliberately carries NO issuance-business navigation — an investor lands here
// to verify and manage their identity passport without seeing the issuer product.
function IdWordmark() {
  return (
    <span className="inline-flex items-center gap-2.5">
      <svg width="30" height="30" viewBox="0 0 32 32" className="shrink-0" aria-hidden>
        <rect width="32" height="32" rx="8" fill="#0b1220" />
        <rect x="7" y="7" width="7" height="7" rx="1.6" fill="#F7931A" />
        <rect x="18" y="7" width="7" height="7" rx="1.6" fill="#27c2c9" />
        <rect x="7" y="18" width="7" height="7" rx="1.6" fill="#27c2c9" />
        <rect x="18" y="18" width="7" height="7" rx="1.6" fill="#F7931A" />
      </svg>
      <span className="text-[1.35rem] font-extrabold tracking-tight text-ink-900">
        Seq<span className="text-btc">Pal</span>
        <span className="ml-1.5 rounded-md bg-ink-900 px-1.5 py-0.5 text-sm font-bold text-white">
          ID
        </span>
      </span>
    </span>
  )
}

export default function IdNav() {
  const { isLoggedIn, account, signOut } = useStore()
  return (
    <header className="sticky top-0 z-40 border-b border-ink-900/10 bg-white/80 backdrop-blur-lg">
      <div className="container-x flex h-16 items-center justify-between gap-4">
        <Link to="/id" aria-label="SeqPal ID home">
          <IdWordmark />
        </Link>
        <div className="flex items-center gap-1">
          {isLoggedIn ? (
            <>
              <NavLink
                to="/id"
                end
                className={({ isActive }) =>
                  `hidden rounded-lg px-3 py-2 text-sm font-medium transition-colors sm:block ${
                    isActive ? 'text-btc-600' : 'text-ink-700 hover:bg-ink-900/5'
                  }`
                }
              >
                My ID
              </NavLink>
              <NavLink
                to="/holdings"
                className={({ isActive }) =>
                  `rounded-lg px-3 py-2 text-sm font-medium transition-colors ${
                    isActive ? 'text-btc-600' : 'text-ink-700 hover:bg-ink-900/5'
                  }`
                }
              >
                Holdings
              </NavLink>
              <button onClick={signOut} className="btn-ghost gap-2 px-2 text-ink-700 sm:ml-1 sm:px-4">
                <span className="flex h-6 w-6 items-center justify-center rounded-full bg-btc-50 text-xs font-bold text-btc-700">
                  {account.individual.name.slice(0, 1).toUpperCase()}
                </span>
                <span className="hidden sm:inline">Sign out</span>
              </button>
            </>
          ) : (
            <span className="inline-flex items-center gap-1.5 text-sm text-ink-700/70">
              <Icon.shield width={16} height={16} className="text-liquid-600" />
              Your compliance passport
            </span>
          )}
        </div>
      </div>
    </header>
  )
}
