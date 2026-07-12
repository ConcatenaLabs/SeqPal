import { Link, useLocation } from 'react-router-dom'
import { Icon } from './icons'

// Shown on routes that need a live session. A SeqPal ID is not an issuer login:
// it is the identity and compliance passport for anyone who wants access to
// SeqPal-managed assets, and an issuer page is one of the doors it opens.
// Callers may say what THIS page needs; they must not imply the ID exists only
// for them.
export default function SignInGate({ title, body, backTo = '/', backLabel = 'Back home' }) {
  const loc = useLocation()
  const next = encodeURIComponent(loc.pathname + loc.search)
  return (
    <section className="container-x flex flex-col items-center py-24 text-center">
      <div className="flex h-16 w-16 items-center justify-center rounded-2xl bg-btc-50 text-btc-600">
        <Icon.id width={32} height={32} />
      </div>
      <h1 className="mt-6 text-2xl font-extrabold tracking-tight text-ink-900">
        {title || 'Sign in with your SeqPal ID'}
      </h1>
      <p className="mt-3 max-w-md leading-relaxed text-ink-700/80">
        {body ||
          'Your SeqPal ID is your identity and compliance passport for SeqPal-managed assets on Sequentia: the credential that lets you hold, trade, or issue them. This page needs it.'}
      </p>
      <div className="mt-8 flex flex-wrap justify-center gap-3">
        <Link to={`/id?next=${next}`} className="btn-primary px-5 py-3 text-base">
          Create or sign in with your SeqPal ID
          <Icon.arrowRight width={18} height={18} />
        </Link>
        <Link to={backTo} className="btn-outline px-5 py-3 text-base">
          {backLabel}
        </Link>
      </div>
    </section>
  )
}
