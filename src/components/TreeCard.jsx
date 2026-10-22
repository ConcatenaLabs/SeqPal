import { useState } from 'react'
import { Badge } from './ui'
import CopyId from './CopyId'
import * as api from '../lib/api'

// A token elected for the issuer's tree: what the tree adds, where it stands,
// and the issuer's powers there, in plain words. When the policy server could
// not enable the tree at deploy, the issuer asks again from here.
export default function TreeCard({ iss }) {
  const [state, setState] = useState(null)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)
  const enabled = state ? state.enabled : !!iss.treeOperator
  const operator = state?.operator || iss.treeOperator

  const enable = async () => {
    setErr(null)
    setBusy(true)
    try {
      setState(await api.enableTree(iss.id))
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="card p-6">
      <div className="flex items-center justify-between gap-3">
        <h2 className="font-bold text-ink-900">Instant transfers in your tree</h2>
        <Badge color={enabled ? 'emerald' : 'amber'}>{enabled ? 'On' : 'Not on yet'}</Badge>
      </div>
      <p className="mt-3 text-sm leading-relaxed text-ink-700/85">
        Holders of this token can also keep it in your tree, where a transfer between approved
        investors settles at once, after the same check against your rules as a transfer on the
        chain.
      </p>
      <ul className="mt-3 space-y-1.5 text-sm leading-relaxed text-ink-700/85">
        <li>A holder can always take their tokens out to their own account on the chain, without your approval, after a short wait.</li>
        <li>You can refuse a transfer that breaks your rules, or freeze a holder.</li>
        <li>
          {iss.clawback !== false
            ? 'You kept the clawback option: you can take back a holding once it is on the chain.'
            : 'You gave up the clawback option: nobody can take a holding back.'}
        </li>
        <li>You cannot move anyone&rsquo;s holding without them.</li>
      </ul>
      {enabled && operator && (
        <dl className="mt-4 text-sm">
          <div className="flex items-center justify-between gap-4 py-2">
            <dt className="shrink-0 text-ink-700/70">Tree operator key</dt>
            <dd className="min-w-0 text-right font-medium text-ink-900">
              <CopyId value={operator} label="tree operator key" />
            </dd>
          </div>
        </dl>
      )}
      {!enabled && iss.live && (
        <>
          <p className="mt-4 rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-xs leading-relaxed text-amber-800">
            {state?.note ||
              'The token is live and its transfers are checked as for the standard option, but its holdings cannot sit in your tree until the policy server turns it on.'}
          </p>
          {err && <p className="mt-2 text-xs text-rose-700">{err}</p>}
          <button onClick={enable} disabled={busy} className="btn-outline mt-4 w-full disabled:opacity-60">
            {busy ? 'Asking the policy server…' : 'Turn on instant transfers'}
          </button>
        </>
      )}
    </div>
  )
}
