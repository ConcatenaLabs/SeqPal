import { Link } from 'react-router-dom'
import { Icon } from '../components/icons'

export default function NotFound() {
  return (
    <section className="container-x flex flex-col items-center py-32 text-center">
      <div className="text-6xl font-extrabold tracking-tight text-ink-900">404</div>
      <p className="mt-3 text-ink-700/80">This page hasn’t been tokenized yet.</p>
      <Link to="/" className="btn-primary mt-8">
        <Icon.arrowLeft width={16} height={16} />
        Back home
      </Link>
    </section>
  )
}
