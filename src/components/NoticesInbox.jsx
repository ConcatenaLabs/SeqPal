import { Icon } from './icons'
import { Badge } from './ui'
import * as api from '../lib/api'
import { usePoll } from '../lib/poll'

// The holder notices inbox (M7 section 6). Pre-expiry warnings, skipped
// distributions, servicing actions (a freeze or clawback that affected the
// holder), and annual-report availability all land here, newest first, straight
// from the server. Some notice bodies carry a document link (an anchored,
// content-addressed artifact such as an annual report or a statement); those are
// rendered as a download so the holder can retrieve the artifact. Nothing here is
// invented in the browser: every notice is a server record.

const KIND_META = {
  'pre-expiry': { label: 'Identity expiry', color: 'amber' },
  'accred-expired': { label: 'Accreditation expired', color: 'rose' },
  'pre-accred-expiry': { label: 'Accreditation expiry', color: 'amber' },
  servicing: { label: 'Servicing', color: 'slate' },
  distribution: { label: 'Distribution', color: 'seq' },
  'annual-report': { label: 'Annual report', color: 'emerald' },
}

// A notice body may embed a document path like /seqpal/api/doc/<64-hex>. Split the
// body around any such link (a global regex with a capturing group keeps the
// delimiters) and match each part with a fresh, non-global regex so the test is
// not made stateful by the global flag's lastIndex.
const DOC_SPLIT = /(\/seqpal\/api\/doc\/[0-9a-f]{64})/gi
const isDocPath = (s) => /^\/seqpal\/api\/doc\/[0-9a-f]{64}$/i.test(s)

function NoticeBody({ body }) {
  const text = body || ''
  const parts = text.split(DOC_SPLIT)
  return (
    <span className="text-sm leading-relaxed text-ink-800">
      {parts.map((part, i) =>
        isDocPath(part) ? (
          <a
            key={i}
            href={part}
            target="_blank"
            rel="noopener noreferrer"
            className="mx-0.5 inline-flex items-center gap-1 rounded-md bg-seq/10 px-1.5 py-0.5 text-xs font-medium text-seq-600 hover:underline"
          >
            <Icon.doc width={12} height={12} /> Open document
          </a>
        ) : (
          <span key={i}>{part}</span>
        )
      )}
    </span>
  )
}

function noticeDate(ts) {
  const n = Number(ts) || 0
  if (!n) return ''
  return new Date(n * 1000).toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  })
}

export default function NoticesInbox() {
  const { data } = usePoll(() => api.notices(), { intervalMs: 20000 })
  const notices = data?.notices || []

  return (
    <div className="card p-6">
      <div className="flex items-center gap-2">
        <Icon.doc width={18} height={18} className="text-ink-700" />
        <h2 className="font-bold text-ink-900">Notices</h2>
        {notices.length > 0 && (
          <span className="ml-auto text-xs text-ink-700/50">
            {notices.length} notice{notices.length === 1 ? '' : 's'}
          </span>
        )}
      </div>
      <p className="mt-1.5 text-sm leading-relaxed text-ink-700/70">
        Pre-expiry reminders, distribution notices, servicing actions, and annual reports for your
        SeqPal ID, newest first. Statements and reports link to their anchored, content-addressed
        document.
      </p>

      {notices.length === 0 ? (
        <div className="mt-4 rounded-lg bg-ink-900/[0.03] px-4 py-3 text-sm text-ink-700/70">
          No notices yet. Distribution, servicing, and expiry notices appear here.
        </div>
      ) : (
        <div className="mt-4 space-y-2">
          {notices.map((n) => {
            const meta = KIND_META[n.kind] || { label: n.kind, color: 'slate' }
            return (
              <div key={n.id} className="rounded-lg border border-ink-900/10 px-3 py-2.5">
                <div className="flex items-center justify-between gap-2">
                  <Badge color={meta.color}>{meta.label}</Badge>
                  <span className="text-xs text-ink-700/50">{noticeDate(n.created_at)}</span>
                </div>
                <div className="mt-1.5">
                  <NoticeBody body={n.body} />
                </div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
