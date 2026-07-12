import { Link, NavLink } from 'react-router-dom'
import { Icon } from './icons'
import { useStore } from '../lib/store'

// Header for the standalone SeqPal ID subsite (conceptually id.seqpal.io).
// It carries no issuance-business navigation: the only issuer cross-link appears
// for an identity that actually owns an issuance on the server.
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
  const { isSignedIn, account, signOut, issuances } = useStore()
  // Cross-link to the issuance platform only when this SeqPal ID owns at least
  // one issuance: someone who only holds assets never sees a path to the issuer
  // business from here.
  const isIssuer = isSignedIn && issuances.length > 0
  const isLoggedIn = isSignedIn
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
              {isIssuer && (
                <Link
                  to="/dashboard"
                  className="hidden rounded-lg px-3 py-2 text-sm font-medium text-ink-700 transition-colors hover:bg-ink-900/5 sm:flex sm:items-center sm:gap-1.5"
                >
                  Issuer dashboard
                  <Icon.arrowRight width={13} height={13} className="text-ink-500" />
                </Link>
              )}
              <button onClick={signOut} className="btn-ghost gap-2 px-2 text-ink-700 sm:ml-1 sm:px-4">
                <span className="flex h-6 w-6 items-center justify-center rounded-full bg-btc-50 text-xs font-bold text-btc-700">
                  {(account.display_name || '?').slice(0, 1).toUpperCase()}
                </span>
                <span className="hidden sm:inline">Sign out</span>
              </button>
            </>
          ) : (
            <span className="inline-flex items-center gap-1.5 text-sm text-ink-700/70">
              <Icon.shield width={16} height={16} className="text-seq-600" />
              Your compliance passport
            </span>
          )}
        </div>
      </div>
    </header>
  )
}
