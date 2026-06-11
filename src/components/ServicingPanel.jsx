import { useState } from 'react'
import { Icon } from './icons'
import { Badge } from './ui'
import Modal from './Modal'
import { useStore } from '../lib/store'

const DIST_LABEL = {
  'native-equity': 'Dividend',
  'equity-spv': 'Dividend',
  'debt-yield': 'Coupon',
  'depository-receipt': 'Yield pass-through',
}

// Structure-specific event fees (plan 4.3.3 / 4.4.3), shown when the issuer
// initiates the corresponding corporate action.
const FEE_BY_ACTION = {
  'Waterfall distribution': '0.50% of distribution value · $5K min',
  'Early redemption / call': '1% of outstanding principal',
  'Default workflow': 'Out-of-pocket pass-through + $10K–$50K work fee',
}

const CORP_ACTIONS = {
  'native-equity': ['Reissuance', 'Lockup release', 'Voting proxy', 'Redemption'],
  'equity-spv': ['Waterfall distribution', 'Proof-of-position update', 'Redemption'],
  'debt-yield': ['Early redemption / call', 'Covenant update', 'Default workflow'],
  'depository-receipt': ['Proof-of-reserves attestation', 'NAV update', 'Reconciliation'],
}

function uid() {
  return Math.random().toString(36).slice(2, 9)
}

export default function ServicingPanel({ iss }) {
  const { updateIssuance } = useStore()
  const [modal, setModal] = useState(null) // 'dist' | 'corp' | 'mint' | 'redeem' | null
  const [toast, setToast] = useState(null)

  const distLabel = DIST_LABEL[iss.structureId] || 'Distribution'
  const corpActions = CORP_ACTIONS[iss.structureId] || ['Corporate action']
  const isDR = iss.structureId === 'depository-receipt'

  // forms
  // BTC-denominated issuances pair naturally with BTC distributions (plan 1.3).
  const [dist, setDist] = useState({
    amount: '',
    asset: iss.unit === 'BTC' ? 'BTC (L-BTC)' : 'USDt',
    recordDate: '',
  })
  const [corp, setCorp] = useState({ type: corpActions[0], note: '' })
  const [mint, setMint] = useState({ qty: '', mandate: 'Direct deposit of securities' })
  const [redeem, setRedeem] = useState({ qty: '' })

  const activity = iss.activity || []

  const pushActivity = (entry) => {
    updateIssuance(iss.id, (i) => ({
      activity: [{ id: uid(), at: new Date().toISOString(), ...entry }, ...(i.activity || [])],
    }))
    setModal(null)
    setToast(entry.summary)
    setTimeout(() => setToast(null), 3200)
  }

  return (
    <div className="card p-6">
      <div className="flex items-center gap-2">
        <Icon.exchange width={18} height={18} className="text-ink-700" />
        <h2 className="font-bold text-ink-900">Automated Transfer Agent</h2>
      </div>
      <p className="mt-1 text-sm text-ink-700/70">
        The blockchain is the official Registry of Members. Schedule distributions and
        process corporate actions — all logged immutably.
      </p>

      {/* action buttons */}
      <div className="mt-4 grid gap-2 sm:grid-cols-2">
        <button
          onClick={() => setModal('dist')}
          className="flex items-center gap-2.5 rounded-lg border border-ink-900/10 px-3 py-2.5 text-left text-sm font-medium text-ink-800 hover:bg-ink-900/[0.02]"
        >
          <Icon.coins width={18} height={18} className="text-ink-600" />
          Schedule {distLabel.toLowerCase()}
        </button>
        <button
          onClick={() => setModal('corp')}
          className="flex items-center gap-2.5 rounded-lg border border-ink-900/10 px-3 py-2.5 text-left text-sm font-medium text-ink-800 hover:bg-ink-900/[0.02]"
        >
          <Icon.layers width={18} height={18} className="text-ink-600" />
          Corporate action
        </button>
        {isDR && (
          <>
            <button
              onClick={() => setModal('mint')}
              className="flex items-center gap-2.5 rounded-lg border border-ink-900/10 px-3 py-2.5 text-left text-sm font-medium text-ink-800 hover:bg-ink-900/[0.02]"
            >
              <Icon.receipt width={18} height={18} className="text-ink-600" />
              Mint receipts
            </button>
            <button
              onClick={() => setModal('redeem')}
              className="flex items-center gap-2.5 rounded-lg border border-ink-900/10 px-3 py-2.5 text-left text-sm font-medium text-ink-800 hover:bg-ink-900/[0.02]"
            >
              <Icon.wallet width={18} height={18} className="text-ink-600" />
              Redeem
            </button>
          </>
        )}
        <button
          onClick={() =>
            (setToast('Holder statements exported (demo)'),
            setTimeout(() => setToast(null), 3200))
          }
          className="flex items-center gap-2.5 rounded-lg border border-ink-900/10 px-3 py-2.5 text-left text-sm font-medium text-ink-800 hover:bg-ink-900/[0.02]"
        >
          <Icon.doc width={18} height={18} className="text-ink-600" />
          Holder statements
        </button>
      </div>

      {isDR && (
        <p className="mt-3 rounded-lg bg-ink-900/[0.03] px-3 py-2 text-xs text-ink-700/70">
          Programme fees: mint 0.25% (in-kind) / 0.30% (cash-settled) · redeem 0.50% ·
          management 0.75%/yr of AUM, deducted from NAV or yield.
        </p>
      )}

      {/* activity log */}
      {activity.length > 0 && (
        <div className="mt-5">
          <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">
            Recent activity
          </div>
          <div className="mt-2 space-y-2">
            {activity.slice(0, 4).map((a) => (
              <div
                key={a.id}
                className="flex items-center gap-3 rounded-lg bg-ink-900/[0.03] px-3 py-2.5 text-sm"
              >
                <span className="flex h-7 w-7 items-center justify-center rounded-full bg-emerald-50 text-emerald-600">
                  <Icon.check width={14} height={14} />
                </span>
                <span className="flex-1 text-ink-900">{a.summary}</span>
                <span className="text-xs text-ink-700/50">
                  {new Date(a.at).toLocaleDateString()}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* toast */}
      {toast && (
        <div className="mt-4 flex items-center gap-2 rounded-lg bg-emerald-50 px-3 py-2.5 text-sm font-medium text-emerald-700">
          <Icon.check width={16} height={16} /> {toast}
        </div>
      )}

      {/* ── Distribution modal ── */}
      <Modal
        open={modal === 'dist'}
        onClose={() => setModal(null)}
        title={`Schedule ${distLabel.toLowerCase()}`}
        footer={
          <button
            onClick={() =>
              pushActivity({
                summary: `${distLabel} of $${dist.amount || '0'} in ${dist.asset} scheduled`,
              })
            }
            className="btn-primary w-full"
          >
            Confirm &amp; schedule
          </button>
        }
      >
        <div className="space-y-4">
          <div>
            <label className="label" htmlFor="sv-dist-amount">
              Amount per token-holder pool
            </label>
            <div className="relative">
              <span className="pointer-events-none absolute left-3.5 top-1/2 -translate-y-1/2 text-sm text-ink-700/60">
                $
              </span>
              <input
                id="sv-dist-amount"
                className="input pl-7"
                placeholder="100,000"
                value={dist.amount}
                onChange={(e) => setDist({ ...dist, amount: e.target.value })}
              />
            </div>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="label" htmlFor="sv-dist-asset">
                Settlement asset
              </label>
              <select
                id="sv-dist-asset"
                className="select"
                value={dist.asset}
                onChange={(e) => setDist({ ...dist, asset: e.target.value })}
              >
                <option>USDt</option>
                <option>BTC (L-BTC)</option>
              </select>
            </div>
            <div>
              <label className="label" htmlFor="sv-dist-date">
                Record date
              </label>
              <input
                id="sv-dist-date"
                type="date"
                className="input"
                value={dist.recordDate}
                onChange={(e) => setDist({ ...dist, recordDate: e.target.value })}
              />
            </div>
          </div>
          {iss.structureId === 'debt-yield' && (
            <div className="rounded-lg bg-ink-900/[0.03] px-3 py-2 text-xs text-ink-700/70">
              Coupons run automatically per the Note’s agreed schedule
              {iss.fields?.schedule ? ` (${iss.fields.schedule.toLowerCase()}` : ''}
              {iss.fields?.schedule && iss.fields?.rate
                ? `, ${iss.fields.rate}% p.a.)`
                : iss.fields?.schedule
                  ? ')'
                  : ''}{' '}
              — SeqPal acts as Calculation &amp; Paying Agent. Use this for an ad-hoc or
              catch-up distribution.
            </div>
          )}
          <p className="text-xs text-ink-700/60">
            Paid pro-rata across the whitelisted holder set as of the record-date snapshot.
            Mocked in the demo.
          </p>
        </div>
      </Modal>

      {/* ── Corporate action modal ── */}
      <Modal
        open={modal === 'corp'}
        onClose={() => setModal(null)}
        title="Process corporate action"
        footer={
          <button
            onClick={() => pushActivity({ summary: `${corp.type} processed` })}
            className="btn-primary w-full"
          >
            Process action
          </button>
        }
      >
        <div className="space-y-4">
          <div>
            <label className="label" htmlFor="sv-corp-type">
              Action type
            </label>
            <select
              id="sv-corp-type"
              className="select"
              value={corp.type}
              onChange={(e) => setCorp({ ...corp, type: e.target.value })}
            >
              {corpActions.map((a) => (
                <option key={a}>{a}</option>
              ))}
            </select>
          </div>
          <div>
            <label className="label" htmlFor="sv-corp-note">
              Note (optional)
            </label>
            <textarea
              id="sv-corp-note"
              className="input min-h-[72px] resize-y"
              placeholder="Context for the registry log…"
              value={corp.note}
              onChange={(e) => setCorp({ ...corp, note: e.target.value })}
            />
          </div>
          {FEE_BY_ACTION[corp.type] && (
            <div className="flex items-center justify-between rounded-lg bg-ink-900/[0.03] px-3 py-2 text-xs">
              <span className="text-ink-700/70">Event fee</span>
              <span className="font-mono font-semibold text-ink-900">
                {FEE_BY_ACTION[corp.type]}
              </span>
            </div>
          )}
          <p className="text-xs text-ink-700/60">
            Logged immutably with AMP authorization context. Mocked in the demo.
          </p>
        </div>
      </Modal>

      {/* ── DR mint modal ── */}
      <Modal
        open={modal === 'mint'}
        onClose={() => setModal(null)}
        title="Mint depository receipts"
        footer={
          <button
            onClick={() =>
              pushActivity({ summary: `Minted ${mint.qty || '0'} ${iss.ticker} receipts` })
            }
            className="btn-primary w-full"
          >
            Confirm mint
          </button>
        }
      >
        <div className="space-y-4">
          <div>
            <label className="label" htmlFor="sv-mint-qty">
              Quantity
            </label>
            <input
              id="sv-mint-qty"
              className="input"
              placeholder="10,000"
              value={mint.qty}
              onChange={(e) => setMint({ ...mint, qty: e.target.value })}
            />
          </div>
          <div>
            <label className="label" htmlFor="sv-mint-exec">
              Execution
            </label>
            <select
              id="sv-mint-exec"
              className="select"
              value={mint.mandate}
              onChange={(e) => setMint({ ...mint, mandate: e.target.value })}
            >
              <option>Direct deposit of securities</option>
              <option>Cash-for-purchase by SeqPal</option>
            </select>
          </div>
          <div className="flex items-center justify-between rounded-lg bg-ink-900/[0.03] px-3 py-2 text-xs">
            <span className="text-ink-700/70">Minting fee</span>
            <span className="font-mono font-semibold text-ink-900">
              {mint.mandate === 'Cash-for-purchase by SeqPal'
                ? '0.30% of value (cash-settled)'
                : '0.25% of value (in-kind)'}
            </span>
          </div>
          <p className="text-xs text-ink-700/60">
            Underlying held in a segregated custody sub-account; on-chain supply
            reconciled against custody balance. Mocked in the demo.
          </p>
        </div>
      </Modal>

      {/* ── DR redeem modal ── */}
      <Modal
        open={modal === 'redeem'}
        onClose={() => setModal(null)}
        title="Redeem depository receipts"
        footer={
          <button
            onClick={() =>
              pushActivity({ summary: `Redeemed ${redeem.qty || '0'} ${iss.ticker} receipts` })
            }
            className="btn-primary w-full"
          >
            Process redemption
          </button>
        }
      >
        <div className="space-y-4">
          <div>
            <label className="label" htmlFor="sv-redeem-qty">
              Quantity to redeem
            </label>
            <input
              id="sv-redeem-qty"
              className="input"
              placeholder="5,000"
              value={redeem.qty}
              onChange={(e) => setRedeem({ ...redeem, qty: e.target.value })}
            />
          </div>
          <div className="flex items-center justify-between rounded-lg bg-ink-900/[0.03] px-3 py-2 text-xs">
            <span className="text-ink-700/70">Redemption fee</span>
            <span className="font-mono font-semibold text-ink-900">0.50% of payout value</span>
          </div>
          <p className="text-xs text-ink-700/60">
            Tokens are burned and the underlying released per the Depository Agreement.
            Mocked in the demo.
          </p>
        </div>
      </Modal>
    </div>
  )
}
