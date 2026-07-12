import { useState } from 'react'
import { Link } from 'react-router-dom'
import { Icon } from '../icons'
import { Badge } from '../ui'
import * as api from '../../lib/api'

// The offer-side gate steps. seqpald returns the unsatisfied requirements for
// the visitor's jurisdiction; this renders the exact next step for each. Three
// kinds are self-clearable (the UK SI 2024/301 statement, the appropriateness +
// KID acknowledgement, the source-of-funds declaration); the rest (a
// jurisdiction block, the EU offeree cap, a US accreditation requirement) are
// shown as hard blocks the investor cannot self-clear. Clearing a gate re-fetches
// the offering, which is the gate-passage (and EU offeree-counting) event.

function UkStatement({ id, onCleared }) {
  const [basis, setBasis] = useState('hnw')
  const [income, setIncome] = useState('')
  const [netAssets, setNetAssets] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)
  const submit = async () => {
    setErr(null)
    setBusy(true)
    try {
      await api.gate(id, {
        kind: 'uk_statement',
        basis,
        income_gbp: Number(String(income).replace(/[^0-9.]/g, '')) || 0,
        net_assets: Number(String(netAssets).replace(/[^0-9.]/g, '')) || 0,
      })
      onCleared()
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="mt-3 space-y-3">
      <div className="flex gap-2">
        <button
          type="button"
          onClick={() => setBasis('hnw')}
          className={`flex-1 rounded-lg border px-3 py-2 text-left text-sm ${
            basis === 'hnw' ? 'border-seq-500 bg-seq/[0.06]' : 'border-ink-900/15'
          }`}
        >
          <span className="font-semibold text-ink-900">High net worth</span>
          <span className="block text-[11px] text-ink-700/70">
            Income GBP 100,000+ or net assets GBP 250,000+
          </span>
        </button>
        <button
          type="button"
          onClick={() => setBasis('soph')}
          className={`flex-1 rounded-lg border px-3 py-2 text-left text-sm ${
            basis === 'soph' ? 'border-seq-500 bg-seq/[0.06]' : 'border-ink-900/15'
          }`}
        >
          <span className="font-semibold text-ink-900">Self-certified sophisticated</span>
          <span className="block text-[11px] text-ink-700/70">Investment experience basis</span>
        </button>
      </div>
      {basis === 'hnw' && (
        <div className="grid grid-cols-2 gap-2">
          <div>
            <label className="label" htmlFor="uk-income">
              Annual income (GBP)
            </label>
            <input
              id="uk-income"
              className="input"
              inputMode="numeric"
              value={income}
              onChange={(e) => setIncome(e.target.value)}
            />
          </div>
          <div>
            <label className="label" htmlFor="uk-assets">
              Net assets (GBP)
            </label>
            <input
              id="uk-assets"
              className="input"
              inputMode="numeric"
              value={netAssets}
              onChange={(e) => setNetAssets(e.target.value)}
            />
          </div>
        </div>
      )}
      <p className="text-[11px] leading-relaxed text-ink-700/60">
        By submitting you make the SI 2024/301 {basis === 'hnw' ? 'high-net-worth' : 'self-certified sophisticated'}{' '}
        investor statement. It is recorded on your SeqPal ID and is valid for 12 months.
      </p>
      {err && <p className="text-xs font-medium text-rose-600">{err}</p>}
      <button onClick={submit} disabled={busy} className="btn-primary w-full disabled:opacity-60">
        {busy ? 'Recording…' : 'Make statement'}
      </button>
    </div>
  )
}

function Appropriateness({ id, onCleared }) {
  const [kidAck, setKidAck] = useState(false)
  const [experience, setExperience] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)
  const submit = async () => {
    setErr(null)
    setBusy(true)
    try {
      await api.gate(id, { kind: 'appropriateness', kid_ack: true, answers: { experience } })
      onCleared()
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="mt-3 space-y-3">
      <div>
        <label className="label" htmlFor="appr-exp">
          What is your experience with instruments of this kind?
        </label>
        <select
          id="appr-exp"
          className="select"
          value={experience}
          onChange={(e) => setExperience(e.target.value)}
        >
          <option value="">Select one</option>
          <option value="none">No prior experience</option>
          <option value="some">Some experience</option>
          <option value="professional">Professional experience</option>
        </select>
      </div>
      <label className="flex cursor-pointer items-start gap-2.5 rounded-lg border border-ink-900/15 px-3 py-2.5">
        <input
          type="checkbox"
          checked={kidAck}
          onChange={(e) => setKidAck(e.target.checked)}
          className="mt-0.5 h-4 w-4 accent-btc"
        />
        <span className="text-sm text-ink-800">
          I have read the Key Information Document (KID) style summary and its standardized risk
          warning for this offering.
        </span>
      </label>
      {err && <p className="text-xs font-medium text-rose-600">{err}</p>}
      <button
        onClick={submit}
        disabled={busy || !kidAck || !experience}
        className="btn-primary w-full disabled:opacity-60"
      >
        {busy ? 'Recording…' : 'Confirm appropriateness'}
      </button>
    </div>
  )
}

function SourceOfFunds({ id, onCleared }) {
  const [source, setSource] = useState('')
  const [amount, setAmount] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)
  const submit = async () => {
    setErr(null)
    setBusy(true)
    try {
      await api.gate(id, {
        kind: 'sof',
        source,
        amount_usd: Number(String(amount).replace(/[^0-9.]/g, '')) || 0,
      })
      onCleared()
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="mt-3 space-y-3">
      <div>
        <label className="label" htmlFor="sof-src">
          Source of the funds
        </label>
        <select
          id="sof-src"
          className="select"
          value={source}
          onChange={(e) => setSource(e.target.value)}
        >
          <option value="">Select one</option>
          <option value="employment">Employment income</option>
          <option value="business">Business income / sale</option>
          <option value="investments">Investment proceeds</option>
          <option value="inheritance">Inheritance or gift</option>
          <option value="other">Other</option>
        </select>
      </div>
      <div>
        <label className="label" htmlFor="sof-amt">
          Approximate amount (USD)
        </label>
        <input
          id="sof-amt"
          className="input"
          inputMode="numeric"
          value={amount}
          onChange={(e) => setAmount(e.target.value)}
        />
      </div>
      {err && <p className="text-xs font-medium text-rose-600">{err}</p>}
      <button
        onClick={submit}
        disabled={busy || !source}
        className="btn-primary w-full disabled:opacity-60"
      >
        {busy ? 'Recording…' : 'Declare source of funds'}
      </button>
    </div>
  )
}

const TITLES = {
  uk_statement: 'UK investor statement',
  appropriateness: 'Appropriateness and KID',
  sof: 'Source of funds',
  accreditation: 'Accredited status required',
  jurisdiction: 'Jurisdiction',
  offeree_cap: 'Offeree limit reached',
}

export default function GateSteps({ id, requirements, onCleared }) {
  return (
    <div className="space-y-3">
      {requirements.map((req) => {
        const blocked = req.blocked
        return (
          <div
            key={req.kind + req.message}
            className={`rounded-xl border p-4 ${
              blocked ? 'border-rose-200 bg-rose-50/60' : 'border-ink-900/12 bg-white'
            }`}
          >
            <div className="flex items-center gap-2">
              {blocked ? (
                <Icon.close width={16} height={16} className="text-rose-600" />
              ) : (
                <Icon.shield width={16} height={16} className="text-seq-600" />
              )}
              <span className="text-sm font-semibold text-ink-900">
                {TITLES[req.kind] || req.kind}
              </span>
              {blocked && (
                <Badge color="rose" className="ml-auto">
                  Blocked
                </Badge>
              )}
            </div>
            <p className="mt-1.5 text-sm text-ink-700/85">{req.message}</p>
            {!blocked && req.kind === 'uk_statement' && <UkStatement id={id} onCleared={onCleared} />}
            {!blocked && req.kind === 'appropriateness' && (
              <Appropriateness id={id} onCleared={onCleared} />
            )}
            {!blocked && req.kind === 'sof' && <SourceOfFunds id={id} onCleared={onCleared} />}
            {!blocked && req.kind === 'jurisdiction' && (
              <Link to="/id" className="btn-outline mt-3 w-full">
                Complete SeqPal ID verification
                <Icon.arrowRight width={15} height={15} />
              </Link>
            )}
          </div>
        )
      })}
    </div>
  )
}
