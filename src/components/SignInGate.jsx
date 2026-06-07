import { Link, useLocation } from 'react-router-dom'
import { Icon } from './icons'

// Renders a friendly sign-in prompt for routes that require a SeqPal ID.
export default function SignInGate({ title, body }) {
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
      <p className="mt-3 max-w-md text-ink-700/80">
        {body ||
          'The issuer dashboard requires a verified SeqPal ID. Creating one takes a minute — it’s also your identity passport for every SeqPal-issued asset.'}
      </p>
      <div className="mt-8 flex flex-wrap justify-center gap-3">
        <Link to={`/id?next=${next}`} className="btn-primary px-5 py-3 text-base">
          Create / sign in with SeqPal ID
          <Icon.arrowRight width={18} height={18} />
        </Link>
        <Link to="/" className="btn-outline px-5 py-3 text-base">
          Back home
        </Link>
      </div>
    </section>
  )
}
