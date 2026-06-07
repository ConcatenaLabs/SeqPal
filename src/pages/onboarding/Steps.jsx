import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Icon, StructureIcon } from '../../components/icons'
import { Badge, DemoNote } from '../../components/ui'
import { useStore, fakeAssetId, fakeTxid } from '../../lib/store'
import { STRUCTURES, getStructure } from '../../data/structures'
import { JURISDICTIONS } from '../../data/jurisdictions'
import { computeSetupCost } from '../../data/pricing'

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

/* ───────────────────────── Step 1 — Identity & KYB ───────────────────────── */

export function Step1Identity({ next }) {
  const { id, setId } = useStore()
  const [verifying, setVerifying] = useState(false)

  const verify = () => {
    setVerifying(true)
    setTimeout(() => {
      setId({
        entity: 'Acme Holdings Ltd',
        jurisdiction: 'United Arab Emirates',
        idNumber:
          'SQID-' +
          Math.random().toString(36).slice(2, 8).toUpperCase() +
          '-' +
          Math.random().toString(36).slice(2, 6).toUpperCase(),
        verifiedAt: new Date().toISOString(),
      })
      setVerifying(false)
    }, 1400)
  }

  return (
    <div>
      <StepHeader
        n={1}
        title="Identity & KYB check"
        sub="Issuing on SeqPal starts with a verified corporate SeqPal ID. Submit your entity details, UBO and director documents, and the relevant W-8/W-9 forms — then you’re admitted to the flow."
      />

      {id ? (
        <div className="card p-7">
          <div className="flex items-center gap-3">
            <span className="flex h-11 w-11 items-center justify-center rounded-xl bg-emerald-50 text-emerald-600">
              <Icon.check width={24} height={24} />
            </span>
            <div>
              <div className="font-bold text-ink-900">{id.entity}</div>
              <div className="text-sm text-ink-700/70">
                Corporate SeqPal ID verified · {id.idNumber}
              </div>
            </div>
            <Badge color="emerald" className="ml-auto">
              Verified
            </Badge>
          </div>
          <div className="mt-5 grid gap-3 sm:grid-cols-2">
            {[
              'Document verification & liveness',
              'Sanctions screening (OFAC, EU, UN, UK HMT, PEP)',
              'AML risk scoring',
              'Cryptographically linked UBO IDs',
            ].map((t) => (
              <div key={t} className="flex items-center gap-2 text-sm text-ink-800">
                <Icon.check width={15} height={15} className="text-btc-600" />
                {t}
              </div>
            ))}
          </div>
          <p className="mt-5 text-sm text-ink-700/70">
            You’re verified — continue to choose an issuance structure.
          </p>
        </div>
      ) : (
        <div className="card p-7">
          <div className="grid gap-4 sm:grid-cols-2">
            <div>
              <label className="label">Legal entity name</label>
              <input className="input" defaultValue="Acme Holdings Ltd" />
            </div>
            <div>
              <label className="label">Jurisdiction of formation</label>
              <input className="input" defaultValue="United Arab Emirates" />
            </div>
          </div>
          <div className="mt-4 rounded-xl border border-dashed border-ink-900/20 p-6 text-center">
            <Icon.upload width={24} height={24} className="mx-auto text-ink-600" />
            <p className="mt-2 text-sm font-medium text-ink-800">
              UBO & director passports, proof-of-address, W-8/W-9
            </p>
            <p className="mt-1 text-xs text-ink-700/60">
              Document upload and KYC verification are mocked in this demo
            </p>
          </div>
          <DemoNote className="mt-5">
            The real platform runs document verification, liveness, and sanctions
            screening through a KYC vendor. Here we simulate an instant pass. The $150
            corporate ID fee is not charged.
          </DemoNote>
          <button onClick={verify} disabled={verifying} className="btn-primary mt-5 w-full">
            {verifying ? (
              <>
                <span className="h-4 w-4 animate-spin rounded-full border-2 border-white/40 border-t-white" />
                Running verification…
              </>
            ) : (
              <>
                Simulate KYB verification
                <Icon.arrowRight width={16} height={16} />
              </>
            )}
          </button>
        </div>
      )}
    </div>
  )
}

/* ─────────────────────── Step 2 — Architecture Routing ─────────────────────── */

export function Step2Structure({ data, update }) {
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
          const selected = data.structureId === s.id
          return (
            <button
              key={s.id}
              onClick={() =>
                update({
                  structureId: s.id,
                  isPublic: s.id === 'depository-receipt',
                })
              }
              className={`card relative p-6 text-left transition-all ${
                selected
                  ? 'ring-2 ring-btc shadow-glow'
                  : 'hover:-translate-y-0.5 hover:shadow-card'
              }`}
            >
              {selected && (
                <span className="absolute right-4 top-4 flex h-6 w-6 items-center justify-center rounded-full bg-btc text-white">
                  <Icon.check width={14} height={14} />
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
              <div className="mt-4 flex flex-wrap gap-x-4 gap-y-1 text-xs text-ink-700/70">
                <span className="inline-flex items-center gap-1">
                  <Icon.tag width={13} height={13} /> from {s.setup.label}
                </span>
                <span className="inline-flex items-center gap-1">
                  <Icon.clock width={13} height={13} /> {s.timeToDeploy}
                </span>
              </div>
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
      type={cfg.type === 'number' ? 'text' : 'text'}
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

      {/* offering type */}
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

export function Step4Documents({ data, update }) {
  const [phase, setPhase] = useState(data.docsSigned ? 'signed' : 'idle') // idle | generating | ready | signed
  const docs = DOC_PACKAGE(data.structureId, data.isPublic)

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
          <p className="mt-1 text-sm text-ink-700/70">
            Mapping inputs to clause templates
          </p>
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
                <div key={d} className="flex items-center gap-3 px-6 py-3.5">
                  <Icon.doc width={18} height={18} className="text-ink-600" />
                  <span className="flex-1 text-sm font-medium text-ink-900">{d}</span>
                  {phase === 'signed' ? (
                    <Badge color="emerald">
                      <Icon.check width={12} height={12} /> signed
                    </Badge>
                  ) : (
                    <span className="text-xs text-ink-700/60">PDF · ready</span>
                  )}
                </div>
              ))}
            </div>
          </div>

          <DemoNote className="mt-5">
            E-signature runs through an integrated provider in the live platform. Here,
            “sign” is simulated — no documents are actually executed. SeqPal is not a law
            firm and these templates are infrastructure, not legal advice.
          </DemoNote>

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

function defaultPolicy() {
  const p = {}
  for (const j of JURISDICTIONS) p[j.code] = j.tier
  return p
}

export function Step5Compliance({ data, update }) {
  // initialise policy once
  useEffect(() => {
    if (!data.policy) update({ policy: defaultPolicy() })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const policy = data.policy || defaultPolicy()

  const setTier = (code, tier) => update({ policy: { ...policy, [code]: tier } })

  const optionsFor = (j) => {
    if (j.tier === 'blocked') return ['blocked']
    if (j.tier === 'restricted') return ['restricted', 'excluded']
    return ['open', 'restricted', 'excluded'] // suggested-open can be tightened
  }

  const tierStyle = {
    open: 'border-emerald-300 bg-emerald-50 text-emerald-700',
    restricted: 'border-amber-300 bg-amber-50 text-amber-700',
    excluded: 'border-ink-900/15 bg-ink-900/[0.03] text-ink-600',
    blocked: 'border-rose-300 bg-rose-50 text-rose-700',
  }
  const tierLabel = {
    open: 'Open',
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

      <div className="card overflow-hidden">
        <div className="border-b border-ink-900/10 px-6 py-4">
          <h3 className="font-bold text-ink-900">Jurisdiction matrix</h3>
          <p className="mt-1 text-sm text-ink-700/70">
            Cross-checked against the SeqPal ID jurisdiction matrix. Blocked jurisdictions
            are a mandatory floor and cannot be admitted.
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
                </div>
                {j.tier === 'blocked' ? (
                  <span
                    className={`inline-flex items-center gap-1 rounded-lg border px-2.5 py-1.5 text-xs font-semibold ${tierStyle.blocked}`}
                  >
                    <Icon.lock width={12} height={12} /> Blocked
                  </span>
                ) : (
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
            Obtained an approved prospectus or local registration? Upload it to lift a
            default restriction for this issuance. (Skipped in the demo.)
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

const DEPLOY_STEPS = [
  'Registering entity on the Próspera e-registry',
  'Filing offering documents with the RFSA',
  'Deploying Transfer-Restricted asset via Blockstream AMP',
  'Minting initial supply & activating the Transfer Agent',
]

export function Step6Checkout({ data, onDeployed }) {
  const { addIssuance } = useStore()
  const s = getStructure(data.structureId)
  const cost = computeSetupCost(data.structureId, data.isPublic)
  const [phase, setPhase] = useState('summary') // summary | deploying | done
  const [progress, setProgress] = useState(0)

  const deploy = () => {
    setPhase('deploying')
    let i = 0
    const tick = () => {
      i += 1
      setProgress(i)
      if (i < DEPLOY_STEPS.length) {
        setTimeout(tick, 900)
      } else {
        const issuance = {
          id: 'iss_' + Math.random().toString(36).slice(2, 9),
          name: data.name,
          ticker: data.ticker,
          structureId: data.structureId,
          isPublic: data.isPublic,
          raise: data.raise,
          policy: data.policy,
          assetId: fakeAssetId(),
          txid: fakeTxid(),
          status: 'deployed',
          createdAt: new Date().toISOString(),
        }
        setTimeout(() => {
          addIssuance(issuance)
          setPhase('done')
          setTimeout(() => onDeployed(issuance.id), 1100)
        }, 700)
      }
    }
    setTimeout(tick, 900)
  }

  if (phase === 'deploying' || phase === 'done') {
    return (
      <div>
        <StepHeader n={6} title="Deploying your issuance" />
        <div className="card p-8">
          <div className="space-y-4">
            {DEPLOY_STEPS.map((label, i) => {
              const state = i < progress ? 'done' : i === progress ? 'active' : 'todo'
              return (
                <div key={label} className="flex items-center gap-3">
                  <span
                    className={`flex h-7 w-7 items-center justify-center rounded-full ${
                      state === 'done'
                        ? 'bg-emerald-500 text-white'
                        : state === 'active'
                          ? 'bg-btc text-white'
                          : 'bg-ink-900/10 text-ink-600'
                    }`}
                  >
                    {state === 'done' ? (
                      <Icon.check width={15} height={15} />
                    ) : state === 'active' ? (
                      <span className="h-3.5 w-3.5 animate-spin rounded-full border-2 border-white/40 border-t-white" />
                    ) : (
                      <span className="text-xs font-bold">{i + 1}</span>
                    )}
                  </span>
                  <span
                    className={`text-sm ${
                      state === 'todo' ? 'text-ink-600' : 'font-medium text-ink-900'
                    }`}
                  >
                    {label}
                  </span>
                </div>
              )
            })}
          </div>
          {phase === 'done' && (
            <div className="mt-7 rounded-xl bg-emerald-50 p-5 text-center">
              <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-emerald-500 text-white">
                <Icon.check width={26} height={26} />
              </div>
              <h3 className="mt-3 font-bold text-ink-900">{data.name} is live</h3>
              <p className="mt-1 text-sm text-ink-700/80">Taking you to your issuance…</p>
            </div>
          )}
        </div>
      </div>
    )
  }

  return (
    <div>
      <StepHeader
        n={6}
        title="Checkout & deployment"
        sub="Review the final summary, pay the fixed setup fee, and deploy. SeqPal then registers the entity, files with the RFSA where applicable, and deploys the AMP asset."
      />

      <div className="grid gap-5 lg:grid-cols-5">
        {/* Summary */}
        <div className="card p-6 lg:col-span-3">
          <h3 className="font-bold text-ink-900">Summary</h3>
          <dl className="mt-4 divide-y divide-ink-900/10 text-sm">
            {[
              ['Issuer', 'Acme Holdings Ltd'],
              ['Structure', s?.name],
              ['Asset name', data.name || '—'],
              ['Ticker', data.ticker || '—'],
              ['Offering type', data.isPublic ? 'Public offering' : 'Private placement'],
              ['Target raise', data.raise || '—'],
              ['Network', 'Liquid · Blockstream AMP'],
            ].map(([k, v]) => (
              <div key={k} className="flex items-center justify-between py-2.5">
                <dt className="text-ink-700/70">{k}</dt>
                <dd className="font-medium text-ink-900">{v}</dd>
              </div>
            ))}
          </dl>
        </div>

        {/* Cost */}
        <div className="card p-6 lg:col-span-2">
          <h3 className="font-bold text-ink-900">Cost breakdown</h3>
          <dl className="mt-4 space-y-2.5 text-sm">
            <div className="flex justify-between">
              <dt className="text-ink-700/80">Setup — {s?.short}</dt>
              <dd className="font-mono font-medium">${cost.base.toLocaleString()}</dd>
            </div>
            {cost.surcharge > 0 && (
              <div className="flex justify-between">
                <dt className="text-ink-700/80">Public-offering surcharge</dt>
                <dd className="font-mono font-medium">
                  ${cost.surcharge.toLocaleString()}
                </dd>
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
          <DemoNote className="mt-5">
            Payment is mocked — no card or stablecoin is charged.
          </DemoNote>
          <button onClick={deploy} className="btn-primary mt-5 w-full">
            <Icon.bolt width={16} height={16} />
            Pay ${cost.total.toLocaleString()} & deploy
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
