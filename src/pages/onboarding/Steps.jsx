import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Icon, StructureIcon } from '../../components/icons'
import { Badge, DemoNote } from '../../components/ui'
import Modal from '../../components/Modal'
import {
  useStore,
  fakeAssetId,
  fakeTxid,
  fakeIdNumber,
  addBusinessDays,
} from '../../lib/store'
import { STRUCTURES, getStructure } from '../../data/structures'
import { JURISDICTIONS } from '../../data/jurisdictions'
import { computeSetupCost } from '../../data/pricing'

// Target time-to-live in business days, [private, public]. Public offerings take
// materially longer (extra RFSA disclosure, audited financials, per-jurisdiction
// filings) per the plan's deploy-time table.
const DEPLOY_DAYS = {
  'native-equity': [3, 10],
  'equity-spv': [5, 12],
  'debt-yield': [6, 12],
  'depository-receipt': [9, 9],
}
const deployDays = (structureId, isPublic) => {
  const [priv, pub] = DEPLOY_DAYS[structureId] || [3, 10]
  return isPublic ? pub : priv
}

function StepHeader({ n, title, sub }) {
  return (
    <div className="mb-7">
      <div className="text-xs font-semibold uppercase tracking-[0.18em] text-btc-600">
        Step {n} of 6
      </div>
      <h1 className="mt-2 text-2xl font-extrabold tracking-tight text-ink-900 sm:text-3xl">
        {title}
      </h1>
      {sub && <p className="mt-2 leading-relaxed text-ink-700/90">{sub}</p>}
    </div>
  )
}

/* ──────────────────── Step 1 — Identity & principal ──────────────────── */

export function Step1Identity({ data, update }) {
  const { account, addCorporate } = useStore()
  const [adding, setAdding] = useState(false)
  const [form, setForm] = useState({ entity: '', jurisdiction: 'United Arab Emirates' })
  const [verifying, setVerifying] = useState(false)

  const selectIndividual = () =>
    update({
      principal: {
        type: 'individual',
        name: account.individual.name,
        idNumber: account.individual.idNumber,
      },
    })

  const selectCorporate = (c) =>
    update({
      principal: { type: 'corporate', name: c.entity, idNumber: c.idNumber, corpId: c.id },
    })

  const addEntity = (e) => {
    e.preventDefault()
    setVerifying(true)
    setTimeout(() => {
      const corp = {
        id: 'corp_' + Math.random().toString(36).slice(2, 9),
        entity: form.entity || 'Acme Holdings Ltd',
        jurisdiction: form.jurisdiction,
        idNumber: fakeIdNumber('SQID-C'),
        verifiedAt: new Date().toISOString(),
      }
      addCorporate(corp)
      selectCorporate(corp)
      setVerifying(false)
      setAdding(false)
    }, 1300)
  }

  const isSel = (pred) => data.principal && pred(data.principal)

  return (
    <div>
      <StepHeader
        n={1}
        title="Who is forming this issuance?"
        sub="Choose who applies for and will own the new Próspera LLC. On every issuance that newly-formed LLC is the issuer of record — the principal legally responsible for the offering — and SeqPal acts only as enforcement agent for the configuration it signs off on."
      />

      <div className="space-y-3">
        {/* Individual */}
        <button
          onClick={selectIndividual}
          className={`card flex w-full items-start gap-4 p-5 text-left transition-all ${
            isSel((p) => p.type === 'individual')
              ? 'ring-2 ring-btc shadow-glow'
              : 'hover:shadow-card'
          }`}
        >
          <span className="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl bg-btc-50 text-btc-600">
            <Icon.users width={22} height={22} />
          </span>
          <span className="min-w-0 flex-1">
            <span className="flex items-center gap-2">
              <span className="font-bold text-ink-900">Issue as myself</span>
              <span className="font-mono text-xs text-ink-700/60">
                {account.individual.name}
              </span>
            </span>
            <span className="mt-1 block text-sm text-ink-700/80">
              For forming a brand-new Próspera LLC. You become the founder of the new
              entity — the LLC itself is the business.
            </span>
            <span className="mt-2 inline-block">
              <Badge color="btc">Native Equity only</Badge>
            </span>
          </span>
          {isSel((p) => p.type === 'individual') && (
            <span className="flex h-6 w-6 items-center justify-center rounded-full bg-btc text-white">
              <Icon.check width={14} height={14} />
            </span>
          )}
        </button>

        {/* Corporates */}
        {account.corporates.map((c) => (
          <button
            key={c.id}
            onClick={() => selectCorporate(c)}
            className={`card flex w-full items-start gap-4 p-5 text-left transition-all ${
              isSel((p) => p.corpId === c.id)
                ? 'ring-2 ring-liquid shadow-glow'
                : 'hover:shadow-card'
            }`}
          >
            <span className="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl bg-liquid/10 text-liquid-600">
              <Icon.building width={22} height={22} />
            </span>
            <span className="min-w-0 flex-1">
              <span className="flex items-center gap-2">
                <span className="font-bold text-ink-900">{c.entity}</span>
                <span className="font-mono text-xs text-ink-700/60">{c.idNumber}</span>
              </span>
              <span className="mt-1 block text-sm text-ink-700/80">
                Issue on behalf of this verified entity (KYB). Unlocks all four structures.
              </span>
              <span className="mt-2 inline-block">
                <Badge color="emerald">
                  <Icon.check width={12} height={12} /> KYB verified
                </Badge>
              </span>
            </span>
            {isSel((p) => p.corpId === c.id) && (
              <span className="flex h-6 w-6 items-center justify-center rounded-full bg-liquid text-white">
                <Icon.check width={14} height={14} />
              </span>
            )}
          </button>
        ))}

        {/* Add corporate */}
        {adding ? (
          <form onSubmit={addEntity} className="card p-6">
            <div className="mb-4 flex items-center justify-between">
              <span className="text-sm font-semibold text-ink-900">
                Add a corporate SeqPal ID (KYB)
              </span>
              <button
                type="button"
                onClick={() => setAdding(false)}
                aria-label="Cancel"
                className="text-ink-600 hover:text-ink-900"
              >
                <Icon.close width={18} height={18} />
              </button>
            </div>
            <div className="grid gap-4 sm:grid-cols-2">
              <div>
                <label className="label">Legal entity name</label>
                <input
                  className="input"
                  placeholder="Acme Holdings Ltd"
                  value={form.entity}
                  onChange={(e) => setForm({ ...form, entity: e.target.value })}
                />
              </div>
              <div>
                <label className="label">Jurisdiction of formation</label>
                <select
                  className="select"
                  value={form.jurisdiction}
                  onChange={(e) => setForm({ ...form, jurisdiction: e.target.value })}
                >
                  {[
                    'United Arab Emirates',
                    'Switzerland',
                    'Singapore',
                    'Cayman Islands',
                    'United States',
                    'El Salvador',
                  ].map((j) => (
                    <option key={j}>{j}</option>
                  ))}
                </select>
              </div>
            </div>
            <DemoNote className="mt-4">
              KYB verification is mocked. The $150 corporate ID fee is not charged.
            </DemoNote>
            <button disabled={verifying} className="btn-primary mt-4 w-full">
              {verifying ? (
                <>
                  <span className="h-4 w-4 animate-spin rounded-full border-2 border-white/40 border-t-white" />
                  Running KYB verification…
                </>
              ) : (
                <>
                  Verify & select entity
                  <Icon.arrowRight width={16} height={16} />
                </>
              )}
            </button>
          </form>
        ) : (
          <button
            onClick={() => setAdding(true)}
            className="flex w-full items-center justify-center gap-2 rounded-2xl border border-dashed border-ink-900/20 py-4 text-sm font-semibold text-ink-700 hover:border-ink-900/40 hover:bg-ink-900/[0.02]"
          >
            <Icon.spark width={16} height={16} /> Add a corporate entity (KYB)
          </button>
        )}
      </div>
    </div>
  )
}

/* ─────────────────────── Step 2 — Architecture Routing ─────────────────────── */

export function Step2Structure({ data, update }) {
  const isCorp = data.principal?.type === 'corporate'

  // If the selected structure became invalid (e.g. principal switched to
  // individual), clear it.
  useEffect(() => {
    if (data.structureId) {
      const s = getStructure(data.structureId)
      if (s?.requiresKyb && !isCorp) update({ structureId: null })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isCorp])

  return (
    <div>
      <StepHeader
        n={2}
        title="Choose your issuance structure"
        sub="Four structures, each backed by a Próspera LLC and templated paperwork. Pick the one that matches your asset — you can always start another issuance in a different structure later."
      />
      <div className="grid gap-4 sm:grid-cols-2">
        {STRUCTURES.map((s) => {
          const Ic = StructureIcon[s.icon]
          const locked = s.requiresKyb && !isCorp
          const selected = data.structureId === s.id
          return (
            <button
              key={s.id}
              disabled={locked}
              onClick={() =>
                update({
                  structureId: s.id,
                  isPublic: s.id === 'depository-receipt',
                  attested: false,
                })
              }
              className={`card relative p-6 text-left transition-all ${
                locked
                  ? 'cursor-not-allowed opacity-55'
                  : selected
                    ? 'ring-2 ring-btc shadow-glow'
                    : 'hover:-translate-y-0.5 hover:shadow-card'
              }`}
            >
              {selected && (
                <span className="absolute right-4 top-4 flex h-6 w-6 items-center justify-center rounded-full bg-btc text-white">
                  <Icon.check width={14} height={14} />
                </span>
              )}
              {locked && (
                <span className="absolute right-4 top-4 text-ink-500">
                  <Icon.lock width={18} height={18} />
                </span>
              )}
              <div
                className={`flex h-11 w-11 items-center justify-center rounded-xl ${
                  s.accent === 'btc'
                    ? 'bg-btc-50 text-btc-600'
                    : 'bg-liquid/10 text-liquid-600'
                }`}
              >
                <Ic width={22} height={22} />
              </div>
              <h3 className="mt-4 font-bold text-ink-900">{s.name}</h3>
              <p className="mt-1.5 text-sm leading-relaxed text-ink-700/90">{s.claim}</p>
              {locked ? (
                <p className="mt-3 text-xs font-medium text-ink-600">
                  Requires a corporate (KYB) principal — switch in step 1.
                </p>
              ) : (
                <div className="mt-4 flex flex-wrap gap-x-4 gap-y-1 text-xs text-ink-700/70">
                  <span className="inline-flex items-center gap-1">
                    <Icon.tag width={13} height={13} /> from {s.setup.label}
                  </span>
                  <span className="inline-flex items-center gap-1">
                    <Icon.clock width={13} height={13} /> {s.timeToDeploy}
                  </span>
                </div>
              )}
            </button>
          )
        })}
      </div>
      {data.structureId === 'depository-receipt' && (
        <DemoNote className="mt-5">
          Depository Receipts require a contracted brokerage-custody relationship and are
          always a public offering. In the live platform DRs deploy once custody is in
          place; here you can preview the full flow.
        </DemoNote>
      )}
    </div>
  )
}

/* ───────────────────────── Step 3 — Data Room ───────────────────────── */

const FIELD_CONFIG = {
  'native-equity': [
    { k: 'raise', label: 'Target raise', type: 'money', placeholder: '5,000,000' },
    { k: 'premoney', label: 'Pre-money valuation', type: 'money', placeholder: '20,000,000' },
    { k: 'supply', label: 'Total token supply', type: 'number', placeholder: '10,000,000' },
    { k: 'governance', label: 'Governance & voting', type: 'select', options: ['On-chain governance module', 'Classical proxy (run by SeqPal)', 'Non-voting'] },
    { k: 'dividend', label: 'Dividend policy', type: 'select', options: ['Discretionary', 'Fixed schedule', 'None'] },
    { k: 'lockup', label: 'Lockup', type: 'select', options: ['None', '6 months', '12 months', 'Custom'] },
  ],
  'equity-spv': [
    { k: 'raise', label: 'Target raise', type: 'money', placeholder: '5,000,000' },
    { k: 'company', label: 'Target company name', type: 'text', placeholder: 'Nebula Robotics Inc.' },
    { k: 'pricePerShare', label: 'Price per share', type: 'money', placeholder: '142.50' },
    { k: 'shareClass', label: 'Share class', type: 'select', options: ['Common', 'Preferred', 'SAFE', 'Other'] },
    { k: 'proof', label: 'Proof of position', type: 'select', options: ['On file', 'Escrow arrangement', 'Pending'] },
    { k: 'waterfall', label: 'Distribution waterfall', type: 'textarea', placeholder: 'e.g. return of capital, then pro-rata to token holders…' },
  ],
  'debt-yield': [
    { k: 'raise', label: 'Principal / target raise', type: 'money', placeholder: '2,000,000' },
    { k: 'borrower', label: 'Borrower / target identity', type: 'text', placeholder: 'Meridian Trade Finance Ltd' },
    { k: 'rate', label: 'Interest rate (% p.a.)', type: 'text', placeholder: '11.5' },
    { k: 'maturity', label: 'Maturity', type: 'text', placeholder: '24 months' },
    { k: 'schedule', label: 'Payment schedule', type: 'select', options: ['Monthly', 'Quarterly', 'Bullet'] },
    { k: 'collateral', label: 'Collateral package', type: 'select', options: ['Unsecured', 'BTC multi-sig', 'Real estate', 'Receivables', 'Other'] },
    { k: 'daycount', label: 'Day-count convention', type: 'select', options: ['30/360', 'Actual/365'] },
  ],
  'depository-receipt': [
    { k: 'asset', label: 'Underlying ticker / ISIN', type: 'text', placeholder: 'SPY / US78462F1030' },
    { k: 'quantity', label: 'Target quantity', type: 'number', placeholder: '50,000' },
    { k: 'mandate', label: 'Execution mandate', type: 'select', options: ['Direct deposit of securities', 'Cash-for-purchase by SeqPal'] },
    { k: 'redemption', label: 'Redemption mechanics', type: 'select', options: ['In-kind', 'Cash', 'Either'] },
    { k: 'nav', label: 'NAV reporting frequency', type: 'select', options: ['Daily', 'Weekly', 'Monthly'] },
  ],
}

// Structure-specific mandatory attestations the flow won't advance without.
const ATTESTATIONS = {
  'equity-spv':
    'I attest that I have board consent, or a clean reading of the underlying company’s shareholder agreement, permitting this SPV tokenization — no right of first refusal, drag-along, or transfer restriction is breached. SeqPal disclaims responsibility for shareholder-agreement compliance.',
  'debt-yield':
    'I attest that borrower KYB is complete and a financial disclosure pack will be published verbatim to investors in the Note Purchase Agreement schedules. SeqPal is the platform, not the credit underwriter.',
  'depository-receipt':
    'I understand a contracted brokerage-custody relationship must be operational before this Depository Receipt programme can deploy.',
}

function DynamicField({ cfg, value, onChange }) {
  if (cfg.type === 'select') {
    return (
      <select className="select" value={value || ''} onChange={(e) => onChange(e.target.value)}>
        <option value="" disabled>
          Select…
        </option>
        {cfg.options.map((o) => (
          <option key={o}>{o}</option>
        ))}
      </select>
    )
  }
  if (cfg.type === 'textarea') {
    return (
      <textarea
        className="input min-h-[88px] resize-y"
        placeholder={cfg.placeholder}
        value={value || ''}
        onChange={(e) => onChange(e.target.value)}
      />
    )
  }
  if (cfg.type === 'money') {
    return (
      <div className="relative">
        <span className="pointer-events-none absolute left-3.5 top-1/2 -translate-y-1/2 text-sm text-ink-700/60">
          $
        </span>
        <input
          className="input pl-7"
          placeholder={cfg.placeholder}
          value={value || ''}
          onChange={(e) => onChange(e.target.value)}
        />
      </div>
    )
  }
  return (
    <input
      className="input"
      placeholder={cfg.placeholder}
      value={value || ''}
      onChange={(e) => onChange(e.target.value)}
    />
  )
}

export function Step3DataRoom({ data, update }) {
  const s = getStructure(data.structureId)
  const config = FIELD_CONFIG[data.structureId] || []
  const lockedPublic = data.structureId === 'depository-receipt'

  const setField = (k, v) => {
    const fields = { ...data.fields, [k]: v }
    const patch = { fields }
    if (k === 'raise') patch.raise = v ? `$${v}` : ''
    update(patch)
  }

  return (
    <div>
      <StepHeader
        n={3}
        title="The data room"
        sub={`Enter the parameters for your ${s?.name} issuance. These feed directly into the templated paperwork generated in the next step.`}
      />

      <div className="card mb-5 p-5">
        <div className="text-sm font-semibold text-ink-900">Offering type</div>
        <p className="mt-1 text-sm text-ink-700/70">
          {lockedPublic
            ? 'Depository Receipts are always a public offering.'
            : 'Private placements are the launch default. Public offerings carry additional disclosure and a setup surcharge.'}
        </p>
        <div className="mt-3 flex gap-2">
          {[
            { v: false, label: 'Private placement' },
            { v: true, label: 'Public offering' },
          ].map((o) => (
            <button
              key={String(o.v)}
              disabled={lockedPublic}
              onClick={() => update({ isPublic: o.v })}
              className={`flex-1 rounded-lg border px-4 py-2.5 text-sm font-semibold transition-colors ${
                data.isPublic === o.v
                  ? 'border-btc bg-btc-50 text-btc-700'
                  : 'border-ink-900/15 text-ink-700 hover:bg-ink-900/[0.02]'
              } ${lockedPublic && !o.v ? 'cursor-not-allowed opacity-40' : ''}`}
            >
              {o.label}
            </button>
          ))}
        </div>
      </div>

      <div className="card p-7">
        <div className="mb-5 border-b border-ink-900/10 pb-5">
          <label className="label">Entity name (the new Próspera LLC)</label>
          <input
            className="input"
            placeholder="Aurora Ventures Fund I"
            value={data.entityName}
            onChange={(e) => update({ entityName: e.target.value })}
          />
          <p className="mt-1.5 text-xs text-ink-700/60">
            Registered as{' '}
            <span className="font-medium text-ink-800">
              {data.entityName ? `${data.entityName} LLC` : '‹name› LLC'}
            </span>{' '}
            in Próspera. This names the issuer of record on your formation documents.
          </p>
        </div>
        <div className="grid gap-5 sm:grid-cols-2">
          {config.map((cfg) => (
            <div key={cfg.k} className={cfg.type === 'textarea' ? 'sm:col-span-2' : ''}>
              <label className="label">{cfg.label}</label>
              <DynamicField
                cfg={cfg}
                value={data.fields[cfg.k]}
                onChange={(v) => setField(cfg.k, v)}
              />
            </div>
          ))}
        </div>
        <p className="mt-5 text-xs text-ink-700/60">
          All fields are optional in this demo — enter as much or as little as you like.
        </p>
      </div>

      {ATTESTATIONS[data.structureId] && (
        <div className="card mt-5 border-amber-200 bg-amber-50/60 p-5">
          <div className="flex items-center gap-2 text-sm font-semibold text-amber-800">
            <Icon.shield width={16} height={16} /> Required attestation
          </div>
          <label className="mt-3 flex cursor-pointer items-start gap-3">
            <input
              type="checkbox"
              checked={!!data.attested}
              onChange={(e) => update({ attested: e.target.checked })}
              className="mt-0.5 h-4 w-4 accent-btc"
            />
            <span className="text-sm leading-relaxed text-ink-800">
              {ATTESTATIONS[data.structureId]}
            </span>
          </label>
        </div>
      )}
    </div>
  )
}

/* ──────────────────── Step 4 — Document Automation ──────────────────── */

const DOC_PACKAGE = (structureId, isPublic) => {
  const docs = [
    'Articles of Incorporation (Próspera LLC)',
    'Operating Agreement — blockchain as Registry of Members',
  ]
  if (structureId === 'native-equity') {
    docs.push('Share Issuance Resolutions', 'Investment Memorandum / Token Subscription Agreement')
  } else if (structureId === 'equity-spv') {
    docs.push('Administrative Manager & Custody Agreement', 'Proof-of-Position attestation', 'Investment Memorandum')
  } else if (structureId === 'debt-yield') {
    docs.push('Note Purchase Agreement', 'Calculation & Paying Agent Agreement', 'Borrower disclosure pack')
  } else if (structureId === 'depository-receipt') {
    docs.push('Depository Agreement', 'Brokerage Custody Mandate (Power of Attorney)')
  }
  docs.push('Tri-party escrow agreement')
  if (isPublic || structureId === 'depository-receipt') docs.push('RFSA Offering Memorandum filing package')
  return docs
}

// A rendered, prefilled preview of a generated template — "the Magic Moment".
function DocPreview({ docName, data }) {
  const s = getStructure(data.structureId)
  const cfg = FIELD_CONFIG[data.structureId] || []
  const terms = cfg
    .filter((c) => data.fields?.[c.k])
    .map((c) => ({
      label: c.label,
      value: c.type === 'money' ? `$${data.fields[c.k]}` : data.fields[c.k],
    }))
  const llc = data.entityName
    ? `${data.entityName} LLC`
    : data.name
      ? `${data.name} LLC`
      : 'New Próspera LLC'
  return (
    <div>
      <div className="rounded-lg border border-ink-900/10">
        <div className="border-b border-ink-900/10 bg-ink-900/[0.02] px-5 py-4 text-center">
          <div className="text-[10px] font-semibold uppercase tracking-[0.2em] text-ink-700/50">
            Próspera ZEDE · Draft
          </div>
          <div className="mt-1 font-bold text-ink-900">{docName}</div>
          <div className="text-xs text-ink-700/60">
            {llc}
            {data.ticker ? ` · ${data.ticker}` : ''}
          </div>
        </div>
        <div className="space-y-4 px-5 py-5 text-sm text-ink-800">
          <section>
            <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/50">
              Parties
            </div>
            <ul className="mt-1.5 space-y-0.5">
              <li>
                <span className="text-ink-700/70">Issuer of record:</span> {llc}
              </li>
              <li>
                <span className="text-ink-700/70">Applicant / owner:</span>{' '}
                {data.principal?.name}
              </li>
              <li>
                <span className="text-ink-700/70">Agent:</span> SeqPal — {s?.seqpalRole}
              </li>
            </ul>
          </section>
          {terms.length > 0 && (
            <section>
              <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/50">
                Deal terms (from your data room)
              </div>
              <dl className="mt-1.5 divide-y divide-ink-900/5">
                {terms.map((t) => (
                  <div key={t.label} className="flex justify-between gap-4 py-1">
                    <dt className="text-ink-700/70">{t.label}</dt>
                    <dd className="text-right font-medium text-ink-900">{t.value}</dd>
                  </div>
                ))}
              </dl>
            </section>
          )}
          <section className="space-y-2 text-xs leading-relaxed text-ink-700/80">
            <p>
              <span className="font-semibold text-ink-900">1. Registry of Members.</span>{' '}
              The blockchain is hereby declared the official Registry of Members for the{' '}
              {data.ticker || 'token'} class; the on-chain record is dispositive.
            </p>
            <p>
              <span className="font-semibold text-ink-900">2. Agent.</span> {llc} appoints
              SeqPal as {s?.seqpalRole}, to enforce the transfer policy exactly as
              configured and signed off by the issuer.
            </p>
            <p>
              <span className="font-semibold text-ink-900">3. Eligibility.</span> Transfers
              are permitted only between holders whose SeqPal ID satisfies this offering’s
              jurisdiction and accreditation policy; mandatory floors (sanctions,
              OFAC/FATF) always apply.
            </p>
            {data.isPublic && (
              <p>
                <span className="font-semibold text-ink-900">4. Public offering.</span>{' '}
                Filed with the RFSA; admitted jurisdictions limited to those with a
                confirmed registration or exemption.
              </p>
            )}
          </section>
        </div>
      </div>
      <p className="mt-3 text-xs leading-relaxed text-ink-700/60">
        Prefilled template — infrastructure, not legal advice. SeqPal is not a law firm.
        You may accept, modify, or reject any clause, with or without your own counsel.
      </p>
    </div>
  )
}

export function Step4Documents({ data, update }) {
  const [phase, setPhase] = useState(data.docsSigned ? 'signed' : 'idle')
  const docs = DOC_PACKAGE(data.structureId, data.isPublic)

  const [previewDoc, setPreviewDoc] = useState(null)
  const generate = () => {
    setPhase('generating')
    setTimeout(() => setPhase('ready'), 1600)
  }
  const sign = () => {
    setPhase('signed')
    update({ docsSigned: true })
  }

  return (
    <div>
      <StepHeader
        n={4}
        title="Document automation suite"
        sub="SeqPal’s document engine maps your inputs to a standardized library of clause templates. The package is rendered for your review and e-signature — prefilled templates you’re free to accept, modify, or reject."
      />

      {phase === 'idle' && (
        <div className="card p-8 text-center">
          <div className="mx-auto flex h-14 w-14 items-center justify-center rounded-2xl bg-btc-50 text-btc-600">
            <Icon.doc width={28} height={28} />
          </div>
          <h3 className="mt-5 text-lg font-bold text-ink-900">
            Ready to generate your document package
          </h3>
          <p className="mx-auto mt-2 max-w-md text-sm text-ink-700/80">
            {docs.length} documents will be drafted from your data-room inputs.
          </p>
          <button onClick={generate} className="btn-primary mt-6">
            <Icon.spark width={16} height={16} />
            Generate documents
          </button>
        </div>
      )}

      {phase === 'generating' && (
        <div className="card p-8 text-center">
          <span className="mx-auto block h-10 w-10 animate-spin rounded-full border-4 border-btc/20 border-t-btc" />
          <h3 className="mt-5 font-bold text-ink-900">Drafting your documents…</h3>
          <p className="mt-1 text-sm text-ink-700/70">Mapping inputs to clause templates</p>
        </div>
      )}

      {(phase === 'ready' || phase === 'signed') && (
        <>
          <div className="card overflow-hidden">
            <div className="flex items-center justify-between border-b border-ink-900/10 px-6 py-4">
              <h3 className="font-bold text-ink-900">Document package</h3>
              <Badge color={phase === 'signed' ? 'emerald' : 'amber'}>
                {phase === 'signed' ? 'All signed' : 'Awaiting signature'}
              </Badge>
            </div>
            <div className="divide-y divide-ink-900/10">
              {docs.map((d) => (
                <button
                  key={d}
                  onClick={() => setPreviewDoc(d)}
                  className="flex w-full items-center gap-3 px-6 py-3.5 text-left transition-colors hover:bg-ink-900/[0.02]"
                >
                  <Icon.doc width={18} height={18} className="text-ink-600" />
                  <span className="flex-1 text-sm font-medium text-ink-900">{d}</span>
                  <span className="inline-flex items-center gap-1 text-xs font-medium text-btc-600">
                    <Icon.external width={13} height={13} /> Preview
                  </span>
                  {phase === 'signed' && (
                    <Badge color="emerald">
                      <Icon.check width={12} height={12} /> signed
                    </Badge>
                  )}
                </button>
              ))}
            </div>
          </div>

          <DemoNote className="mt-5">
            Click any document to preview the prefilled template. E-signature runs through
            an integrated provider in the live platform; here “sign” is simulated — no
            documents are actually executed. SeqPal is not a law firm and these templates
            are infrastructure, not legal advice.
          </DemoNote>

          <Modal
            open={!!previewDoc}
            onClose={() => setPreviewDoc(null)}
            title={previewDoc || ''}
            wide
          >
            <DocPreview docName={previewDoc} data={data} />
          </Modal>

          {phase === 'ready' && (
            <button onClick={sign} className="btn-primary mt-5 w-full">
              <Icon.check width={16} height={16} />
              Review & e-sign all documents
            </button>
          )}
          {phase === 'signed' && (
            <p className="mt-5 text-center text-sm font-medium text-emerald-600">
              All documents signed — continue to compliance configuration.
            </p>
          )}
        </>
      )}
    </div>
  )
}

/* ─────────────── Step 5 — Tokenomics & Compliance Baking ─────────────── */

function defaultPolicy(isPublic) {
  const p = {}
  // Private placement: start from the suggested-minimum per-jurisdiction tier.
  // Public offering overlay: everything is excluded by default; the issuer must
  // affirmatively admit each jurisdiction by confirming a registration/exemption.
  for (const j of JURISDICTIONS) {
    if (j.tier === 'blocked') p[j.code] = 'blocked'
    else p[j.code] = isPublic ? 'excluded' : j.tier
  }
  return p
}

export function Step5Compliance({ data, update }) {
  // (Re)build the default policy whenever the offering type changes so the
  // public-offering overlay is reflected correctly.
  useEffect(() => {
    if (!data.policy || data.policyPublic !== data.isPublic) {
      update({ policy: defaultPolicy(data.isPublic), policyPublic: data.isPublic })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data.isPublic])

  // The token is usually named after the entity — prefill, leave editable.
  useEffect(() => {
    if (!data.name && data.entityName) update({ name: data.entityName })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const policy = data.policy || defaultPolicy(data.isPublic)
  const setTier = (code, tier) => update({ policy: { ...policy, [code]: tier } })

  // upload-to-lift: an uploaded authorization unlocks admitting retail (Standard)
  // in a normally-restricted jurisdiction. Mandatory floors can never be lifted.
  const lifted = data.lifted || {}
  const lift = (code) => update({ lifted: { ...lifted, [code]: true } })

  const optionsFor = (j) => {
    if (j.tier === 'blocked') return ['blocked']
    if (data.isPublic) {
      // Public offering: a jurisdiction is excluded until the issuer affirmatively
      // confirms a public-offering registration/exemption is in place there.
      return lifted[j.code] ? ['standard', 'restricted', 'excluded'] : ['excluded']
    }
    if (j.tier === 'restricted')
      return lifted[j.code] ? ['standard', 'restricted', 'excluded'] : ['restricted', 'excluded']
    return ['standard', 'restricted', 'excluded']
  }

  const tierStyle = {
    standard: 'border-emerald-300 bg-emerald-50 text-emerald-700',
    restricted: 'border-amber-300 bg-amber-50 text-amber-700',
    excluded: 'border-ink-900/15 bg-ink-900/[0.03] text-ink-600',
    blocked: 'border-rose-300 bg-rose-50 text-rose-700',
  }
  const tierLabel = {
    standard: 'Standard',
    restricted: 'Qualified only',
    excluded: 'Excluded',
    blocked: 'Blocked',
  }

  return (
    <div>
      <StepHeader
        n={5}
        title="Tokenomics & compliance baking"
        sub="Name your asset and configure the policy that gets baked into the token’s whitelist. SeqPal supplies a suggested-minimum restriction set — you can make any rule stricter, but mandatory floors cannot be loosened."
      />

      <div className="card mb-5 p-7">
        <div className="grid gap-4 sm:grid-cols-2">
          <div>
            <label className="label">Asset name</label>
            <input
              className="input"
              placeholder="Acme SPV Series A"
              value={data.name}
              onChange={(e) => update({ name: e.target.value })}
            />
          </div>
          <div>
            <label className="label">Ticker</label>
            <input
              className="input font-mono uppercase"
              placeholder="ACMEA"
              value={data.ticker}
              onChange={(e) => update({ ticker: e.target.value.toUpperCase().slice(0, 8) })}
            />
          </div>
        </div>
      </div>

      {data.isPublic && (
        <div className="mb-5 flex items-start gap-3 rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800">
          <Icon.globe width={18} height={18} className="mt-0.5 shrink-0" />
          <p className="leading-relaxed">
            <span className="font-semibold">Public-offering overlay.</span> Every
            jurisdiction starts <span className="font-semibold">excluded</span>. Admit one
            only by confirming a public-offering registration or exemption is in place
            there; admitting retail (Standard) requires the upload-to-lift authorization
            below.
          </p>
        </div>
      )}

      <div className="card overflow-hidden">
        <div className="border-b border-ink-900/10 px-6 py-4">
          <h3 className="font-bold text-ink-900">Jurisdiction matrix</h3>
          <p className="mt-1 text-sm text-ink-700/70">
            {data.isPublic
              ? 'Confirm, jurisdiction by jurisdiction, where the public offering is conducted. Unconfirmed jurisdictions stay excluded.'
              : 'Cross-checked against the SeqPal ID jurisdiction matrix. Blocked jurisdictions are a mandatory floor and cannot be admitted.'}
          </p>
        </div>
        <div className="max-h-[420px] divide-y divide-ink-900/10 overflow-y-auto">
          {JURISDICTIONS.map((j) => {
            const current = policy[j.code]
            const opts = optionsFor(j)
            return (
              <div key={j.code} className="flex items-center gap-4 px-6 py-3">
                <div className="w-9 shrink-0 font-mono text-xs font-semibold text-ink-700/70">
                  {j.code}
                </div>
                <div className="min-w-0 flex-1">
                  <div className="truncate text-sm font-medium text-ink-900">{j.name}</div>
                  {j.basis && (
                    <div className="truncate text-xs text-ink-700/60">{j.basis}</div>
                  )}
                  {j.cap && j.tier !== 'blocked' && (
                    <div className="truncate text-[11px] text-ink-700/45">
                      Cap: {j.cap}
                    </div>
                  )}
                </div>
                {j.tier === 'blocked' ? (
                  <span
                    className={`inline-flex items-center gap-1 rounded-lg border px-2.5 py-1.5 text-xs font-semibold ${tierStyle.blocked}`}
                  >
                    <Icon.lock width={12} height={12} /> Blocked
                  </span>
                ) : (
                  <div className="flex items-center gap-1.5">
                    {(data.isPublic || j.tier === 'restricted') &&
                      (lifted[j.code] ? (
                        <span
                          className="inline-flex items-center gap-1 rounded-md bg-btc-50 px-1.5 py-1 text-[10px] font-semibold text-btc-700"
                          title={
                            data.isPublic
                              ? 'Public-offering registration / exemption confirmed'
                              : 'Authorization uploaded — retail may be admitted'
                          }
                        >
                          <Icon.check width={10} height={10} />{' '}
                          {data.isPublic ? 'confirmed' : 'lifted'}
                        </span>
                      ) : (
                        <button
                          onClick={() => lift(j.code)}
                          title={
                            data.isPublic
                              ? 'Confirm a public-offering registration or exemption is in place (demo)'
                              : 'Upload a regulatory authorization to admit retail (demo)'
                          }
                          className="inline-flex items-center gap-1 rounded-md border border-ink-900/15 px-1.5 py-1 text-[10px] font-medium text-ink-600 hover:bg-ink-900/[0.04]"
                        >
                          <Icon.upload width={10} height={10} />{' '}
                          {data.isPublic ? 'confirm' : 'lift'}
                        </button>
                      ))}
                    <div className="flex gap-1">
                      {opts.map((o) => (
                        <button
                          key={o}
                          onClick={() => setTier(j.code, o)}
                          className={`rounded-lg border px-2.5 py-1.5 text-xs font-semibold transition-colors ${
                            current === o
                              ? tierStyle[o]
                              : 'border-transparent text-ink-600 hover:bg-ink-900/[0.04]'
                          }`}
                        >
                          {tierLabel[o]}
                        </button>
                      ))}
                    </div>
                  </div>
                )}
              </div>
            )
          })}
        </div>
      </div>

      <div className="mt-5 grid gap-4 sm:grid-cols-2">
        <div className="rounded-xl border border-dashed border-ink-900/20 p-5">
          <div className="flex items-center gap-2 text-sm font-semibold text-ink-900">
            <Icon.upload width={16} height={16} className="text-btc-600" /> Upload-to-lift
          </div>
          <p className="mt-1.5 text-xs leading-relaxed text-ink-700/70">
            Obtained an approved prospectus or local registration? Use{' '}
            <span className="font-medium text-ink-800">lift</span> next to a restricted
            jurisdiction to admit retail there for this issuance. Upload is mocked in the
            demo; SeqPal checks only facial validity and never lifts a mandatory floor.
          </p>
        </div>
        <div className="rounded-xl border border-ink-900/10 bg-ink-900/[0.03] p-5">
          <div className="flex items-center gap-2 text-sm font-semibold text-ink-900">
            <Icon.lock width={16} height={16} className="text-ink-600" /> Mandatory floors
          </div>
          <p className="mt-1.5 text-xs leading-relaxed text-ink-700/70">
            SeqPal ID verification, continuous sanctions screening, and OFAC/FATF-aligned
            blocks are always enforced and cannot be loosened.
          </p>
        </div>
      </div>
    </div>
  )
}

/* ──────────────────── Step 6 — Checkout & Deployment ──────────────────── */

export function Step6Checkout({ data, onDeployed }) {
  const { addIssuance } = useStore()
  const s = getStructure(data.structureId)
  const cost = computeSetupCost(data.structureId, data.isPublic, {
    raise: data.raise,
    collateral: data.fields?.collateral,
  })
  const [phase, setPhase] = useState('summary') // summary | processing | submitted
  const [eta, setEta] = useState(null)
  const [issuanceId, setIssuanceId] = useState(null)

  const isRaise = data.structureId !== 'depository-receipt'
  // Private placements mint to the escrow address; DRs / public equity to the issuer wallet.
  const mintTarget =
    isRaise && !data.isPublic ? 'placement-portal escrow address' : 'issuer wallet'

  const pay = () => {
    setPhase('processing')
    setTimeout(() => {
      const created = new Date()
      const etaDate = addBusinessDays(created, deployDays(data.structureId, data.isPublic))
      const issuance = {
        id: 'iss_' + Math.random().toString(36).slice(2, 9),
        name: data.name,
        ticker: data.ticker,
        entityName: data.entityName,
        structureId: data.structureId,
        principal: data.principal,
        isPublic: data.isPublic,
        raise: data.raise,
        fields: data.fields,
        policy: data.policy,
        mintTarget,
        assetId: fakeAssetId(),
        txid: fakeTxid(),
        status: 'awaiting_incorporation',
        createdAt: created.toISOString(),
        incorporationEta: etaDate.toISOString(),
        portal: { configured: false, published: false },
      }
      addIssuance(issuance)
      setIssuanceId(issuance.id)
      setEta(etaDate)
      setPhase('submitted')
    }, 1600)
  }

  if (phase === 'processing') {
    return (
      <div>
        <StepHeader n={6} title="Processing payment" />
        <div className="card p-10 text-center">
          <span className="mx-auto block h-10 w-10 animate-spin rounded-full border-4 border-btc/20 border-t-btc" />
          <p className="mt-5 font-medium text-ink-900">Confirming your setup-fee payment…</p>
        </div>
      </div>
    )
  }

  if (phase === 'submitted') {
    return (
      <div>
        <StepHeader n={6} title="Submitted for incorporation" />
        <div className="card p-8">
          <div className="flex items-center gap-4">
            <span className="flex h-12 w-12 items-center justify-center rounded-full bg-emerald-500 text-white">
              <Icon.check width={26} height={26} />
            </span>
            <div>
              <h3 className="font-bold text-ink-900">Payment received</h3>
              <p className="text-sm text-ink-700/80">
                Your signed documents have been submitted and your Próspera LLC is being
                incorporated.
              </p>
            </div>
          </div>

          <div className="mt-6 rounded-xl border border-amber-200 bg-amber-50 px-5 py-4">
            <div className="flex items-center gap-2 text-sm font-semibold text-amber-800">
              <Icon.clock width={16} height={16} />
              Próspera incorporation in progress
            </div>
            <p className="mt-1 text-sm text-amber-800/90">
              Entity registration on the Próspera e-registry typically takes 1–3 business
              days. Estimated completion:{' '}
              <span className="font-semibold">
                {eta?.toLocaleDateString(undefined, {
                  weekday: 'short',
                  month: 'short',
                  day: 'numeric',
                })}
              </span>
              . We’ll then file with the RFSA (where applicable), deploy the AMP asset, and
              mint the initial supply to the {mintTarget}.
            </p>
          </div>

          <DemoNote className="mt-5">
            In the demo you can fast-forward incorporation and deployment from the
            issuance page.
          </DemoNote>

          <button
            onClick={() => onDeployed(issuanceId)}
            className="btn-primary mt-6 w-full"
          >
            Go to my issuance
            <Icon.arrowRight width={16} height={16} />
          </button>
        </div>
      </div>
    )
  }

  return (
    <div>
      <StepHeader
        n={6}
        title="Checkout & deployment"
        sub="Review the final summary and pay the fixed setup fee. SeqPal then registers the entity on the Próspera e-registry, files with the RFSA where applicable, and deploys the AMP asset."
      />

      <div className="grid gap-5 lg:grid-cols-5">
        <div className="card p-6 lg:col-span-3">
          <h3 className="font-bold text-ink-900">Summary</h3>
          <dl className="mt-4 divide-y divide-ink-900/10 text-sm">
            {[
              ['Applicant / owner', data.principal?.name],
              [
                'Issuer of record',
                data.entityName
                  ? `${data.entityName} LLC`
                  : data.name
                    ? `${data.name} LLC`
                    : 'New Próspera LLC',
              ],
              ['Structure', s?.name],
              ['Asset name', data.name || '—'],
              ['Ticker', data.ticker || '—'],
              ['Offering type', data.isPublic ? 'Public offering' : 'Private placement'],
              ['Target raise', data.raise || '—'],
              ['Initial mint to', mintTarget],
              ['Network', 'Liquid · Blockstream AMP'],
            ].map(([k, v]) => (
              <div key={k} className="flex items-center justify-between py-2.5">
                <dt className="text-ink-700/70">{k}</dt>
                <dd className="font-medium text-ink-900">{v}</dd>
              </div>
            ))}
          </dl>
        </div>

        <div className="card p-6 lg:col-span-2">
          <h3 className="font-bold text-ink-900">Cost breakdown</h3>
          <dl className="mt-4 space-y-2.5 text-sm">
            <div className="flex justify-between">
              <dt className="text-ink-700/80">
                {cost.simple ? 'Setup — Simple Native Equity' : `Setup — ${s?.short}`}
              </dt>
              <dd className="font-mono font-medium">${cost.base.toLocaleString()}</dd>
            </div>
            {cost.secured > 0 && (
              <div className="flex justify-between">
                <dt className="text-ink-700/80">Secured collateral add-on</dt>
                <dd className="font-mono font-medium">${cost.secured.toLocaleString()}</dd>
              </div>
            )}
            {cost.surcharge > 0 && (
              <div className="flex justify-between">
                <dt className="text-ink-700/80">Public-offering surcharge</dt>
                <dd className="font-mono font-medium">${cost.surcharge.toLocaleString()}</dd>
              </div>
            )}
            <div className="flex justify-between border-t border-ink-900/10 pt-2.5 text-base">
              <dt className="font-semibold text-ink-900">Due today</dt>
              <dd className="font-mono font-bold text-ink-900">
                ${cost.total.toLocaleString()}
              </dd>
            </div>
          </dl>
          <div className="mt-4 space-y-1.5 text-xs text-ink-700/60">
            <p>+ ${s?.annual.toLocaleString()}/yr support, billed after launch.</p>
            <p>+ Platform Services Fee (3% cap, $10K floor) on capital raised.</p>
          </div>
          <DemoNote className="mt-5">Payment is mocked — nothing is charged.</DemoNote>
          <button onClick={pay} className="btn-primary mt-5 w-full">
            <Icon.bolt width={16} height={16} />
            Pay ${cost.total.toLocaleString()} & submit
          </button>
          <Link
            to="/pricing"
            className="mt-3 block text-center text-xs font-medium text-ink-700/70 hover:text-ink-900"
          >
            View full fee schedule
          </Link>
        </div>
      </div>
    </div>
  )
}
