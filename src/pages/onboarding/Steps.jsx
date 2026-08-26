import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Icon, StructureIcon } from '../../components/icons'
import { Badge, DemoNote } from '../../components/ui'
import Modal from '../../components/Modal'
import { useStore } from '../../lib/store'
import { toTerms } from '../../lib/issuance'
import { termsHash } from '../../lib/openamp'
import { isXonly } from '../../lib/statements'
import { bearerAttestation, compileIssuance, health } from '../../lib/api'
import { STRUCTURES, getStructure } from '../../data/structures'
import {
  JURISDICTIONS,
  CATCH_ALL_ROW,
  ELIG_CATEGORIES,
  EU_MEMBER_STATES,
  RESIDENCE_OPTIONS,
} from '../../data/jurisdictions'
import { computeSetupCost } from '../../data/pricing'
import { parseMoney } from '../../lib/economics'

function StepHeader({ n, title, sub }) {
  return (
    <div className="mb-7">
      <div className="text-xs font-semibold uppercase tracking-[0.18em] text-btc-600">
        Step {n} of 7
      </div>
      <h1 className="mt-2 text-2xl font-extrabold tracking-tight text-ink-900 sm:text-3xl">
        {title}
      </h1>
      {sub && <p className="mt-2 leading-relaxed text-ink-700/90">{sub}</p>}
    </div>
  )
}

/* ──────────────────── Step 1, Identity & principal ──────────────────── */

export function Step1Identity({ data, update }) {
  const { account, entities, createEntity } = useStore()
  const [adding, setAdding] = useState(false)
  const [form, setForm] = useState({ name: '', jurisdiction: 'United Arab Emirates' })
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)

  // The principal is named throughout the generated documents, so changing it
  // after e-signing voids the signature.
  const setPrincipal = (principal) =>
    update(
      data.principal?.entity_id === principal.entity_id
        ? { principal }
        : { principal, docsSigned: false }
    )

  const selectIndividual = () =>
    setPrincipal({ kind: 'individual', name: account.display_name, entity_id: null })

  const selectEntity = (e) =>
    setPrincipal({ kind: 'entity', name: e.name, entity_id: e.id })

  const addEntity = async (ev) => {
    ev.preventDefault()
    setErr(null)
    setBusy(true)
    try {
      const entity = await createEntity({
        name: form.name.trim(),
        jurisdiction: form.jurisdiction,
        profile: { kyb: 'simulated', verified_at: new Date().toISOString() },
      })
      selectEntity(entity)
      setAdding(false)
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }

  const isSel = (pred) => data.principal && pred(data.principal)

  return (
    <div>
      <StepHeader
        n={1}
        title="Who is forming this issuance?"
        sub="Choose who applies for and will own the new Próspera LLC. That LLC is the issuer of record and the principal: it runs its own placement portal and is solely responsible for the lawfulness of the offering in every jurisdiction where it makes it available. SeqPal enforces the configuration the issuer signs off on."
      />

      <div className="space-y-3">
        {/* The person themselves */}
        <button
          onClick={selectIndividual}
          className={`card flex w-full items-start gap-4 p-5 text-left transition-all ${
            isSel((p) => p.kind === 'individual')
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
              <span className="font-mono text-xs text-ink-700/60">{account.display_name}</span>
            </span>
            <span className="mt-1 block text-sm text-ink-700/80">
              For forming a brand-new Próspera LLC. You become the founder of the new entity,
              and the LLC itself is the business.
            </span>
            <span className="mt-2 inline-block">
              <Badge color="btc">Native Equity only</Badge>
            </span>
          </span>
          {isSel((p) => p.kind === 'individual') && (
            <span className="flex h-6 w-6 items-center justify-center rounded-full bg-btc text-white">
              <Icon.check width={14} height={14} />
            </span>
          )}
        </button>

        {/* Linked entities */}
        {entities.map((c) => (
          <button
            key={c.id}
            onClick={() => selectEntity(c)}
            className={`card flex w-full items-start gap-4 p-5 text-left transition-all ${
              isSel((p) => p.entity_id === c.id)
                ? 'ring-2 ring-seq shadow-glow'
                : 'hover:shadow-card'
            }`}
          >
            <span className="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl bg-seq/10 text-seq-600">
              <Icon.building width={22} height={22} />
            </span>
            <span className="min-w-0 flex-1">
              <span className="flex items-center gap-2">
                <span className="font-bold text-ink-900">{c.name}</span>
                <span className="font-mono text-xs text-ink-700/60">{c.jurisdiction}</span>
              </span>
              <span className="mt-1 block text-sm text-ink-700/80">
                Issue on behalf of this entity (KYB). Unlocks all four structures.
              </span>
              <span className="mt-2 inline-block">
                <Badge color="emerald">
                  <Icon.check width={12} height={12} /> KYB simulated
                </Badge>
              </span>
            </span>
            {isSel((p) => p.entity_id === c.id) && (
              <span className="flex h-6 w-6 items-center justify-center rounded-full bg-seq text-white">
                <Icon.check width={14} height={14} />
              </span>
            )}
          </button>
        ))}

        {/* Add an entity */}
        {adding ? (
          <form onSubmit={addEntity} className="card p-6">
            <div className="mb-4 flex items-center justify-between">
              <span className="text-sm font-semibold text-ink-900">
                Add a corporate entity (KYB)
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
                <label className="label" htmlFor="oc-entity">
                  Legal entity name
                </label>
                <input
                  id="oc-entity"
                  className="input"
                  placeholder="Acme Holdings Ltd"
                  value={form.name}
                  onChange={(e) => setForm({ ...form, name: e.target.value })}
                />
              </div>
              <div>
                <label className="label" htmlFor="oc-jur">
                  Jurisdiction of formation
                </label>
                <select
                  id="oc-jur"
                  className="select"
                  value={form.jurisdiction}
                  onChange={(e) => setForm({ ...form, jurisdiction: e.target.value })}
                >
                  {RESIDENCE_OPTIONS.map((r) => (
                    <option key={r.code}>{r.name}</option>
                  ))}
                </select>
              </div>
            </div>
            <DemoNote className="mt-4">
              KYB verification is SIMULATED. The entity is recorded against your SeqPal ID on
              the server. It carries no signing key of its own, so this issuance uses your own
              SeqPal ID key and the asset is held by your personal account (AID).
            </DemoNote>
            {err && (
              <p className="mt-3 rounded-lg bg-rose-50 px-3 py-2 text-sm text-rose-700">{err}</p>
            )}
            <button
              disabled={busy || !form.name.trim()}
              className="btn-primary mt-4 w-full disabled:opacity-50"
            >
              {busy ? (
                <>
                  <span className="h-4 w-4 animate-spin rounded-full border-2 border-white/40 border-t-white" />
                  Recording the entity
                </>
              ) : (
                <>
                  Add and select entity
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

/* ─────────────────────── Step 2, Architecture Routing ─────────────────────── */

export function Step2Structure({ data, update }) {
  const isCorp = data.principal?.kind === 'entity'

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
        sub="Four structures, each backed by a Próspera LLC and templated paperwork. Pick the one that matches your asset. You can always start another issuance in a different structure later."
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
                update(
                  s.id === data.structureId
                    ? {} // re-selecting changes nothing
                    : {
                        structureId: s.id,
                        isPublic: s.id === 'depository-receipt',
                        // a new structure means new attestation and a new
                        // document package, any prior e-signature is void
                        attested: false,
                        docsSigned: false,
                      }
                )
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
                    : 'bg-seq/10 text-seq-600'
                }`}
              >
                <Ic width={22} height={22} />
              </div>
              <h3 className="mt-4 font-bold text-ink-900">{s.name}</h3>
              <p className="mt-1.5 text-sm leading-relaxed text-ink-700/90">{s.claim}</p>
              {locked ? (
                <p className="mt-3 text-xs font-medium text-ink-600">
                  Requires a corporate (KYB) principal, switch in step 1.
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

/* ──────────────── Step 3, Enforcement election ──────────────── */

// Who can hold the token, and who enforces the rules. Three models, chosen
// before the deal terms because the choice shapes which rules exist at all.
// The "network" model is a per-deployment capability probed live from
// GET /api/health (field `damp`): when the deployment cannot run it, the card
// says so and cannot be selected, so the deploy never discovers a refusal at
// checkout.
const ENFORCEMENT_MODELS = [
  {
    id: 'serviced',
    badge: 'Standard',
    title: 'SeqPal enforces your rules',
    body: 'Only investors you approve can hold this token. SeqPal’s service checks every transfer against your rules.',
    goodFor: 'Private placements, offerings into regulated countries, tokens whose transfers may need discretion.',
    tradeoffs: [
      'The richest rule set: lockups, investor limits, country rules.',
      'Holders can make any transfer confidential, hiding it from the public while you still see everything.',
      'Transfers pause if SeqPal’s service is down.',
    ],
    regulatory: 'The strongest compliance story for offers that reach US, EU, or UK investors.',
  },
  {
    id: 'network',
    badge: null,
    title: 'The network enforces your rules',
    body: 'Investors verify with SeqPal ID exactly as in the standard option, and only verified investors can hold this token. The difference: the network itself enforces your rules, so trading between approved investors keeps working even when SeqPal’s service is offline.',
    goodFor: 'Tokens that must keep trading no matter what, such as compliant stablecoins.',
    tradeoffs: [
      'Rules are simpler: approved lists, blocked lists, holding periods, transfer limits.',
      'Rule changes and newly verified investors take effect when the updated list is published, not instantly.',
      'A holder can combine at most two of their coins of this token in a single transfer, so a holder with more makes more than one transfer. One transfer also moves the coins of a single holder, so two holders cannot pay from the same transfer. Both are fixed when the token is created and cannot be raised later.',
      'Who holds what is public.',
      'This token does not sit in an ordinary wallet balance, and no wallet sends it: you and your holders each need software that understands these rules to move a coin, and you sign every rule change with the key the token was issued at.',
      'Issuing it takes two rounds: SeqPal prepares the token, you run your own registrar against what it hands back, and you paste the results to mint. Nothing is minted in between, and the registrar is yours to run.',
    ],
    regulatory:
      'Investor vetting is identical to the standard option. SeqPal never touches a transfer: it verifies investors, publishes your rules, and services your register.',
  },
  {
    id: 'bearer',
    badge: null,
    title: 'Freely tradable',
    body: 'Anyone in the world can hold and trade this token. Under your company’s charter, the token is the share.',
    goodFor: 'Próspera companies with no US ties that want globally liquid stock.',
    tradeoffs: [
      'No transfer restrictions at all, so you cannot limit who buys on the open market.',
      'Who holds what is public.',
      'You can freeze specific balances if a court orders it.',
    ],
    extra:
      'Buyers in your initial sale are still identity-checked, and holders must verify their identity to collect dividends or vote.',
    regulatory:
      'Only for companies with no US operations, assets, or banking. You accept in writing that US regulators may object and that this is your risk.',
  },
]

export function Step3Enforcement({ data, update }) {
  // Whether this deployment can run network-enforced rules, probed live.
  const [dampAvailable, setDampAvailable] = useState(null)
  // And whether this issuer has an OpenAMP account to mint a serviced token into.
  const { servicedAvailable } = useStore()
  useEffect(() => {
    let cancelled = false
    health()
      .then((h) => {
        if (!cancelled) setDampAvailable(!!h.damp)
      })
      .catch(() => {
        if (!cancelled) setDampAvailable(null)
      })
    return () => {
      cancelled = true
    }
  }, [])

  const select = (id) => {
    if (id === data.enforcement) return
    // The election is part of the committed terms, so changing it voids any
    // prior e-signature, and switching away from freely-tradable drops the
    // bearer-only requirements (recovery key, attestation checkboxes).
    update({
      enforcement: id,
      docsSigned: false,
      ...(id !== 'bearer' ? { recovery: null, bearerNoUs: false, bearerRisk: false } : {}),
    })
  }

  return (
    <div>
      <StepHeader
        n={3}
        title="Who can hold your token, and who enforces the rules?"
        sub="Every model below is enforced for real on the Sequentia testnet. The choice is committed in your terms and shapes which rules you configure later in this flow."
      />
      <div className="space-y-4">
        {ENFORCEMENT_MODELS.map((m) => {
          const isNetwork = m.id === 'network'
          // A token SeqPal services settles through an account this issuer may
          // not have set up. Finding that out at checkout, after configuring the
          // whole issuance, is the failure the other card already avoids.
          // Both of these settle through, or are supervised by, an account this
          // issuer may not have set up. Only the chain-enforced option needs
          // nothing of the sort.
          const needsServicing = (m.id === 'serviced' || m.id === 'bearer') && !servicedAvailable
          const unavailable = (isNetwork && dampAvailable === false) || needsServicing
          const selected = data.enforcement === m.id
          return (
            // A div rather than a button so the "How this works" link inside
            // stays valid interactive content; key handling keeps it reachable.
            <div
              key={m.id}
              role="button"
              tabIndex={unavailable ? -1 : 0}
              aria-disabled={unavailable}
              aria-pressed={selected}
              onClick={() => !unavailable && select(m.id)}
              onKeyDown={(e) => {
                if (!unavailable && (e.key === 'Enter' || e.key === ' ')) {
                  e.preventDefault()
                  select(m.id)
                }
              }}
              className={`card relative w-full p-6 text-left transition-all ${
                unavailable
                  ? 'cursor-not-allowed opacity-60'
                  : selected
                    ? 'cursor-pointer ring-2 ring-btc shadow-glow'
                    : 'cursor-pointer hover:-translate-y-0.5 hover:shadow-card'
              }`}
            >
              {selected && (
                <span className="absolute right-4 top-4 flex h-6 w-6 items-center justify-center rounded-full bg-btc text-white">
                  <Icon.check width={14} height={14} />
                </span>
              )}
              <div className="flex items-center gap-2">
                <h3 className="font-bold text-ink-900">{m.title}</h3>
                {m.badge && <Badge color="btc">{m.badge}</Badge>}
              </div>
              <p className="mt-2 text-sm leading-relaxed text-ink-800">{m.body}</p>
              <div className="mt-4 grid gap-4 sm:grid-cols-2">
                <div>
                  <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/55">
                    Good for
                  </div>
                  <p className="mt-1 text-sm leading-relaxed text-ink-700/85">{m.goodFor}</p>
                  <div className="mt-3 text-xs font-semibold uppercase tracking-wide text-ink-700/55">
                    Regulatory
                  </div>
                  <p className="mt-1 text-sm leading-relaxed text-ink-700/85">{m.regulatory}</p>
                </div>
                <div>
                  <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/55">
                    Trade-offs
                  </div>
                  <ul className="mt-1 space-y-1">
                    {m.tradeoffs.map((t) => (
                      <li key={t} className="flex items-start gap-2 text-sm leading-relaxed text-ink-700/85">
                        <span className="mt-[7px] h-1 w-1 shrink-0 rounded-full bg-ink-700/50" />
                        {t}
                      </li>
                    ))}
                  </ul>
                </div>
              </div>
              {m.extra && (
                <p className="mt-3 rounded-lg bg-ink-900/[0.03] px-3 py-2 text-xs leading-relaxed text-ink-700/80">
                  {m.extra}
                </p>
              )}
              {unavailable && (
                <p className="mt-3 rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-xs font-medium text-amber-800">
                  {needsServicing
                    ? 'This option needs an account your SeqPal ID has not set up yet: one settles through SeqPal, the other is supervised by a key that signs its freezes. Set one up from your passport, or choose the option the chain enforces on its own.'
                    : 'Not available on this deployment.'}
                </p>
              )}
              <span className="mt-4 inline-flex items-center gap-1.5 text-xs font-medium text-seq-600">
                <Link
                  to="/docs"
                  onClick={(e) => e.stopPropagation()}
                  className="inline-flex items-center gap-1.5 hover:underline"
                >
                  How this works, in detail
                  <Icon.arrowRight width={12} height={12} />
                </Link>
              </span>
            </div>
          )
        })}
      </div>
    </div>
  )
}

/* ───────────────────────── Step 4, Data Room ───────────────────────── */

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
    'I attest that I have board consent, or a clean reading of the underlying company’s shareholder agreement, permitting this SPV tokenization, no right of first refusal, drag-along, or transfer restriction is breached. SeqPal disclaims responsibility for shareholder-agreement compliance.',
  'debt-yield':
    'I attest that borrower KYB is complete and a financial disclosure pack will be published verbatim to investors in the Note Purchase Agreement schedules. SeqPal is the platform, not the credit underwriter.',
  'depository-receipt':
    'I understand a contracted brokerage-custody relationship must be operational before this Depository Receipt programme can deploy.',
}

function DynamicField({ cfg, value, onChange, symbol = '$' }) {
  const id = `f-${cfg.k}`
  if (cfg.type === 'select') {
    return (
      <select id={id} className="select" value={value || ''} onChange={(e) => onChange(e.target.value)}>
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
        id={id}
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
          {symbol}
        </span>
        <input
          id={id}
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
      id={id}
      className="input"
      placeholder={cfg.placeholder}
      value={value || ''}
      onChange={(e) => onChange(e.target.value)}
    />
  )
}

export function Step4DataRoom({ data, update }) {
  const s = getStructure(data.structureId)
  const config = FIELD_CONFIG[data.structureId] || []
  const lockedPublic = data.structureId === 'depository-receipt'
  const symbol = data.unit === 'BTC' ? '₿' : '$'

  const setField = (k, v, sym = symbol) => {
    const fields = { ...data.fields, [k]: v }
    // The document package is generated from these inputs (plan §3.3 step 4);
    // editing any deal term after e-signing voids the signature.
    const patch = { fields, docsSigned: false }
    if (k === 'raise') {
      // Keep the raw input in the field; store a comma-formatted display copy.
      const n = Number(String(v).replace(/[^0-9.]/g, ''))
      patch.raise = v ? (n ? `${sym}${n.toLocaleString()}` : `${sym}${v}`) : ''
    }
    update(patch)
  }

  const setUnit = (unit) => {
    // Re-derive the stored raise display with the new symbol. The elected unit
    // of account appears in the documents, so changing it voids a signature.
    update({ unit, docsSigned: false })
    const v = data.fields?.raise
    if (v) setField('raise', v, unit === 'BTC' ? '₿' : '$')
  }

  return (
    <div>
      <StepHeader
        n={4}
        title="The data room"
        sub={`Enter the parameters for your ${s?.name} issuance. These feed directly into the templated paperwork generated in the next step.`}
      />

      <div className="card mb-5 p-5">
        <div className="text-sm font-semibold text-ink-900">Offering type</div>
        <p className="mt-1 text-sm text-ink-700/70">
          {lockedPublic
            ? 'Depository Receipts are always a public offering.'
            : 'Private placements are the default. Public offerings carry additional disclosure and a setup surcharge.'}
        </p>
        <div className="mt-3 flex gap-2">
          {[
            { v: false, label: 'Private placement' },
            { v: true, label: 'Public offering' },
          ].map((o) => (
            <button
              key={String(o.v)}
              disabled={lockedPublic}
              onClick={() =>
                o.v !== data.isPublic && update({ isPublic: o.v, docsSigned: false })
              }
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
        <div className="mb-5 grid gap-5 border-b border-ink-900/10 pb-5 sm:grid-cols-[1.4fr_1fr]">
          <div>
            <label className="label" htmlFor="f-entityName">
              Entity name (the new Próspera LLC)
            </label>
            <input
              id="f-entityName"
              className="input"
              placeholder="Aurora Ventures Fund I"
              value={data.entityName}
              onChange={(e) => update({ entityName: e.target.value, docsSigned: false })}
            />
            <p className="mt-1.5 text-xs text-ink-700/60">
              Registered as{' '}
              <span className="font-medium text-ink-800">
                {data.entityName ? `${data.entityName} LLC` : '‹name› LLC'}
              </span>{' '}
              in Próspera. This names the issuer of record on your formation documents.
            </p>
          </div>
          <div>
            <label className="label" htmlFor="f-unit">
              Unit of account
            </label>
            <select
              id="f-unit"
              className="select"
              value={data.unit || 'USD'}
              onChange={(e) => setUnit(e.target.value)}
            >
              <option value="USD">USD (default)</option>
              <option value="BTC">BTC, Bitcoin-denominated</option>
            </select>
            <p className="mt-1.5 text-xs text-ink-700/60">
              {data.unit === 'BTC'
                ? 'Books, raise, and distributions kept in BTC; any escrowed subscriptions are held in kind.'
                : 'A Próspera entity may instead adopt BTC as its unit of account.'}
            </p>
          </div>
        </div>
        <div className="grid gap-5 sm:grid-cols-2">
          {config.map((cfg) => (
            <div key={cfg.k} className={cfg.type === 'textarea' ? 'sm:col-span-2' : ''}>
              <label className="label" htmlFor={`f-${cfg.k}`}>
                {cfg.label}
              </label>
              <DynamicField
                cfg={cfg}
                value={data.fields[cfg.k]}
                onChange={(v) => setField(cfg.k, v)}
                symbol={symbol}
              />
            </div>
          ))}
        </div>
        <p className="mt-5 text-xs text-ink-700/60">
          All fields are optional in this demo. Enter as much or as little as you like.
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

/* ──────────────────── Step 4, Document Automation ──────────────────── */

const DOC_PACKAGE = (structureId, isPublic) => {
  const docs = [
    'Articles of Incorporation (Próspera LLC)',
    'Operating Agreement, blockchain as Registry of Members',
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
  // Capital-raise structures include the terms for the optional SeqPal escrow;
  // DRs use the brokerage custody mandate (added above) instead.
  if (structureId !== 'depository-receipt') docs.push('Escrow terms (optional SeqPal escrow)')
  if (isPublic || structureId === 'depository-receipt')
    docs.push('RFSA registration & Offering Memorandum package (Financial Products Registry)')
  if (isPublic) docs.push('Board & governance attestations')
  return docs
}

// A rendered, prefilled preview of a generated template, "the Magic Moment".
function DocPreview({ docName, data }) {
  const s = getStructure(data.structureId)
  const cfg = FIELD_CONFIG[data.structureId] || []
  const sym = data.unit === 'BTC' ? '₿' : '$'
  const terms = cfg
    .filter((c) => data.fields?.[c.k])
    .map((c) => ({
      label: c.label,
      value: c.type === 'money' ? `${sym}${data.fields[c.k]}` : data.fields[c.k],
    }))
  if (data.unit === 'BTC') {
    terms.push({ label: 'Unit of account', value: 'BTC (books, raise & distributions)' })
  }
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
                <span className="text-ink-700/70">Agent:</span> SeqPal, {s?.seqpalRole}
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
        Prefilled template, infrastructure, not legal advice. SeqPal is not a law firm.
        You may accept, modify, or reject any clause, with or without your own counsel.
      </p>
    </div>
  )
}

export function Step5Documents({ data, update }) {
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
        n={5}
        title="Document automation suite"
        sub="SeqPal’s document engine maps your inputs to a standardized library of clause templates. The package is rendered for your review and e-signature, prefilled templates you’re free to accept, modify, or reject."
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

          {data.isPublic && (
            <div className="mt-5 flex items-start gap-3 rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800">
              <Icon.doc width={18} height={18} className="mt-0.5 shrink-0" />
              <p className="leading-relaxed">
                <span className="font-semibold">Public offering, issuer-supplied items.</span>{' '}
                Beyond this generated package you’ll also provide audited (or audit-reviewed)
                financials and a signed local-counsel opinion for each admitted jurisdiction.
                SeqPal supplies the template framework and RFSA filing path; these are
                procured by you.
              </p>
            </div>
          )}

          <DemoNote className="mt-5">
            Click any document to preview the prefilled template. E-signature runs through
            an integrated provider in the live platform; here “sign” is simulated, no
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
              All documents signed, continue to compliance configuration.
            </p>
          )}
        </>
      )}
    </div>
  )
}

/* ─────────────── Step 5, Tokenomics & Compliance Baking ─────────────── */

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

export function Step6Compliance({ data, update }) {
  // The compiled-rules preview is computed server-side (seqpald is the only
  // place the authoritative rules are produced); a draft issuance is created
  // lazily the first time a preview is requested and reused at deploy.
  const { createIssuance } = useStore()
  const [preview, setPreview] = useState(null)
  const [previewErr, setPreviewErr] = useState(null)
  const [previewing, setPreviewing] = useState(false)
  const [advOpen, setAdvOpen] = useState(false)
  const [euPick, setEuPick] = useState({ code: 'DE', n: '' })
  const [jurQuery, setJurQuery] = useState('')
  // A freely-tradable token carries no transfer restrictions, so the rule
  // configuration below is hidden for it; the offering visibility choice stays
  // in the data-room step either way.
  const bearer = data.enforcement === 'bearer'

  // (Re)build the default policy whenever the offering type changes so the
  // public-offering overlay is reflected correctly.
  useEffect(() => {
    if (bearer) return
    if (!data.policy || data.policyPublic !== data.isPublic) {
      // Reset lifted/confirmed flags too: they mean different things in public
      // (registration confirmed) vs private (retail authorization) mode.
      update({
        policy: defaultPolicy(data.isPublic),
        policyPublic: data.isPublic,
        lifted: {},
      })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data.isPublic])

  // The token is usually named after the entity, prefill, leave editable.
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

  // ── Transfer restrictions (lockup, Reg S, holder cap) ────────────────────
  const lockup = data.lockup || { mode: 'none', days: '', height: '' }
  const setLockup = (patch) => update({ lockup: { ...lockup, ...patch } })
  const regS = data.regS || { enabled: false, prefix: 'j:US', mode: 'days', days: '', height: '' }
  const setRegS = (patch) => update({ regS: { ...regS, ...patch } })
  const euCaps = data.euCaps || {}
  const setEuCap = (code, v) => {
    const next = { ...euCaps }
    const n = Number(v)
    if (n > 0) next[code] = n
    else delete next[code]
    update({ euCaps: next })
  }

  // ── Per-jurisdiction eligibility-category refinement (advanced) ──────────
  const eligCategories = data.eligCategories || {}
  const admittedCodes = JURISDICTIONS.filter((j) => ['standard', 'restricted'].includes(policy[j.code]))
  const admitsForAccess = (access) =>
    access === 'standard' ? ['ret', 'acc', 'pro', 'hnw', 'soph'] : access === 'restricted' ? ['acc', 'pro'] : []
  const defaultElig = (access) => (access === 'restricted' ? ['acc', 'pro'] : ['ret', 'acc', 'pro'])
  const selectableFor = (code) => {
    const admits = admitsForAccess(policy[code])
    return ELIG_CATEGORIES.filter((c) => admits.includes(c.key) && (!c.gbOnly || code === 'GB'))
  }
  const eligFor = (code) => eligCategories[code] ?? defaultElig(policy[code])
  const toggleElig = (code, key) => {
    const cur = eligFor(code)
    const next = cur.includes(key) ? cur.filter((k) => k !== key) : [...cur, key]
    update({ eligCategories: { ...eligCategories, [code]: next } })
  }

  // ── Compiled-rules preview ───────────────────────────────────────────────
  const previewRules = async () => {
    setPreviewErr(null)
    setPreviewing(true)
    try {
      if (!data.name?.trim() || !data.ticker?.trim())
        throw new Error('Enter an asset name and ticker above before previewing the compiled rules.')
      let id = data.issuanceId
      if (!id) {
        const issuance = await createIssuance({
          name: data.name.trim(),
          ticker: data.ticker.trim(),
          structure_id: data.structureId,
          entity_id: data.principal?.entity_id || undefined,
          terms: toTerms(data),
        })
        id = issuance.id
        update({ issuanceId: id })
      }
      const res = await compileIssuance(id, { terms: toTerms(data) })
      setPreview(res)
    } catch (e) {
      setPreviewErr(e.message)
    } finally {
      setPreviewing(false)
    }
  }

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
        n={6}
        title="Tokenomics & compliance baking"
        sub={
          bearer
            ? 'Name your asset. You chose the freely-tradable model, so there are no transfer restrictions to configure: anyone can hold and trade this token on the open market.'
            : 'Name your asset and configure the policy that gets baked into the token’s whitelist. SeqPal supplies a suggested-minimum restriction set, you can make any rule stricter, but mandatory floors cannot be loosened.'
        }
      />

      <div className="card mb-5 p-7">
        <div className="grid gap-4 sm:grid-cols-2">
          <div>
            <label className="label" htmlFor="f-assetName">
              Asset name
            </label>
            <input
              id="f-assetName"
              className="input"
              placeholder="Acme SPV Series A"
              value={data.name}
              onChange={(e) => update({ name: e.target.value })}
            />
          </div>
          <div>
            <label className="label" htmlFor="f-ticker">
              Ticker
            </label>
            <input
              id="f-ticker"
              className="input font-mono uppercase"
              placeholder="ACMEA"
              value={data.ticker}
              onChange={(e) => update({ ticker: e.target.value.toUpperCase().slice(0, 8) })}
            />
          </div>
        </div>

        {!bearer && (
          <label className="mt-3 flex cursor-pointer items-start gap-3 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] px-4 py-3">
            <input
              type="checkbox"
              className="mt-1 h-4 w-4 accent-btc"
              checked={data.clawback !== false}
              onChange={(e) => update({ clawback: e.target.checked })}
            />
            <span className="text-sm">
              <span className="font-semibold text-ink-900">Issuer recovery power</span>
              <span className="block text-ink-700/70">
                You can reclaim tokens from a holder, two signatures needed, always
                public: your own key authorizes it, SeqPal adds the second signature,
                and the reason is recorded in the public log.
              </span>
            </span>
          </label>
        )}

        {bearer && (
          <div className="mt-5 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] px-4 py-3 text-sm text-ink-700/80">
            <span className="font-semibold text-ink-900">Freely tradable.</span> Anyone can
            hold and trade this token, and who holds what is public. Buyers in your
            initial sale are still identity-checked, and holders verify their identity to
            collect dividends or vote. You can freeze specific balances if a court orders
            it; the order document&rsquo;s fingerprint is recorded publicly beside the
            freeze. Before you deploy, the checkout step asks for an emergency recovery
            key and a signed attestation about US exposure.
          </div>
        )}
      </div>

      {!bearer && (
        <>
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

      {data.structureId === 'depository-receipt' && (
        <div className="mb-5 flex items-start gap-3 rounded-xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-800">
          <Icon.lock width={18} height={18} className="mt-0.5 shrink-0" />
          <p className="leading-relaxed">
            <span className="font-semibold">DR, US persons excluded at launch.</span>{' '}
            Depository Receipts mirroring US-listed securities carry SEC unregistered-ADR
            and synthetic-equity enforcement risk, so US persons are not admitted at launch. Admitting them
            requires your own US counsel (for example a Reg S structure). Confirm US here only on
            that basis.
          </p>
        </div>
      )}

      <div className="card overflow-hidden">
        <div className="border-b border-ink-900/10 px-6 py-4">
          <h3 className="font-bold text-ink-900">Jurisdiction matrix</h3>
          <p className="mt-1 text-sm text-ink-700/70">
            {data.isPublic
              ? 'Confirm, country by country, where the public offering is conducted and on what basis. A country you do not confirm stays excluded.'
              : 'SeqPal starts you on a suggested minimum: qualified investors only in every country, sanctions floor blocked. This is a suggestion, not advice. You are the principal and set the final policy; make any country stricter, or use upload-to-lift to admit retail with documented authority. The sanctions floor cannot be admitted.'}
          </p>
          <p className="mt-1 text-xs text-ink-700/55">
            Every country is here, and the catch-all is{' '}
            <span className="font-semibold text-ink-700">excluded by default</span>: a country you set
            to Excluded contributes no eligibility category, so a resident of it is refused when they
            try to receive the token.
          </p>
          <input
            className="input mt-3 w-full"
            placeholder="Search countries by name or ISO code…"
            value={jurQuery}
            onChange={(e) => setJurQuery(e.target.value)}
          />
        </div>
        <div className="max-h-[420px] divide-y divide-ink-900/10 overflow-y-auto">
          {JURISDICTIONS.filter((j) => {
            const q = jurQuery.trim().toLowerCase()
            return !q || j.name.toLowerCase().includes(q) || j.code.toLowerCase().includes(q)
          }).map((j) => {
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
                              : 'Authorization uploaded, retail may be admitted'
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
          <div className="flex items-center gap-4 bg-ink-900/[0.02] px-6 py-3">
            <div className="w-9 shrink-0 font-mono text-xs font-semibold text-ink-700/70">
              ···
            </div>
            <div className="min-w-0 flex-1">
              <div className="truncate text-sm font-medium text-ink-900">
                {CATCH_ALL_ROW.name}
              </div>
              <div className="truncate text-xs text-ink-700/60">{CATCH_ALL_ROW.basis}</div>
            </div>
            <span className="rounded-lg border border-ink-900/15 bg-ink-900/[0.03] px-2.5 py-1.5 text-xs font-semibold text-ink-600">
              Excluded
            </span>
          </div>
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
            SeqPal ID verification, the watchlist checks the verification provider runs as
            part of it, and OFAC/FATF-aligned blocks, including OFAC and EU territorial
            sanctions for the occupied Ukrainian territories, are always enforced and cannot
            be loosened.
          </p>
        </div>
      </div>

      {/* ── Transfer restrictions ─────────────────────────────────────────── */}
      <div className="card mt-5 p-7">
        <h3 className="font-bold text-ink-900">Transfer restrictions</h3>
        <p className="mt-1 text-sm text-ink-700/70">
          These compile into the on-chain rules that are checked on every transfer.
        </p>

        {/* Lockup */}
        <div className="mt-5">
          <div className="label">Lockup</div>
          <div className="flex flex-wrap gap-2">
            {[
              ['none', 'No lockup'],
              ['days', 'For a number of days'],
              ['height', 'Until a block height'],
            ].map(([m, label]) => (
              <button
                key={m}
                onClick={() => setLockup({ mode: m })}
                className={`rounded-lg border px-3 py-2 text-sm font-semibold transition-colors ${
                  lockup.mode === m
                    ? 'border-btc bg-btc-50 text-btc-700'
                    : 'border-ink-900/15 text-ink-700 hover:bg-ink-900/[0.02]'
                }`}
              >
                {label}
              </button>
            ))}
          </div>
          {lockup.mode === 'days' && (
            <div className="mt-3 max-w-xs">
              <input
                type="number"
                min="1"
                className="input"
                placeholder="e.g. 365"
                value={lockup.days}
                onChange={(e) => setLockup({ days: e.target.value })}
              />
              <p className="mt-1.5 text-xs text-ink-700/60">
                Converted to an absolute Sequentia block height against the chain tip when you
                deploy. Nothing is final at 0 confirmations.
              </p>
            </div>
          )}
          {lockup.mode === 'height' && (
            <div className="mt-3 max-w-xs">
              <input
                type="number"
                min="1"
                className="input"
                placeholder="e.g. 250000"
                value={lockup.height}
                onChange={(e) => setLockup({ height: e.target.value })}
              />
              <p className="mt-1.5 text-xs text-ink-700/60">
                Holders cannot move the asset until Sequentia block{' '}
                {lockup.height ? Number(lockup.height).toLocaleString() : 'H'}. The per-offering
                escrow is exempt so it can still deliver during the lockup.
              </p>
            </div>
          )}
        </div>

        {/* Reg S window */}
        <div className="mt-6 border-t border-ink-900/10 pt-5">
          <label className="flex cursor-pointer items-start gap-3">
            <input
              type="checkbox"
              className="mt-1 h-4 w-4 accent-btc"
              checked={!!regS.enabled}
              onChange={(e) => setRegS({ enabled: e.target.checked })}
            />
            <span className="text-sm">
              <span className="font-semibold text-ink-900">Reg S distribution-compliance window</span>
              <span className="mt-0.5 block text-ink-700/70">
                During the offshore compliance period, a non-primary holder may not deliver to a
                recipient carrying the restricted category prefix. Delivery by the offering escrow
                is exempt.
              </span>
            </span>
          </label>
          {regS.enabled && (
            <div className="mt-3 grid gap-3 sm:grid-cols-3">
              <div>
                <label className="label" htmlFor="regs-prefix">
                  Category prefix
                </label>
                <input
                  id="regs-prefix"
                  className="input font-mono"
                  placeholder="j:US"
                  value={regS.prefix}
                  onChange={(e) => setRegS({ prefix: e.target.value })}
                />
              </div>
              <div>
                <label className="label" htmlFor="regs-mode">
                  Window by
                </label>
                <select
                  id="regs-mode"
                  className="select"
                  value={regS.mode}
                  onChange={(e) => setRegS({ mode: e.target.value })}
                >
                  <option value="days">Days from tip</option>
                  <option value="height">Block height</option>
                </select>
              </div>
              <div>
                <label className="label" htmlFor="regs-val">
                  {regS.mode === 'height' ? 'Until block' : 'Days'}
                </label>
                <input
                  id="regs-val"
                  type="number"
                  min="1"
                  className="input"
                  placeholder={regS.mode === 'height' ? '250000' : '40'}
                  value={regS.mode === 'height' ? regS.height : regS.days}
                  onChange={(e) =>
                    setRegS(regS.mode === 'height' ? { height: e.target.value } : { days: e.target.value })
                  }
                />
              </div>
            </div>
          )}
        </div>

        {/* Global holder cap */}
        <div className="mt-6 border-t border-ink-900/10 pt-5">
          <label className="label" htmlFor="holder-cap">
            Global holder cap (optional)
          </label>
          <div className="max-w-xs">
            <input
              id="holder-cap"
              type="number"
              min="1"
              className="input"
              placeholder="e.g. 2000"
              value={data.holderCap || ''}
              onChange={(e) => update({ holderCap: e.target.value })}
            />
            <p className="mt-1.5 text-xs text-ink-700/60">
              Maximum distinct holders across all jurisdictions. Leave blank for no cap.
            </p>
          </div>
        </div>
      </div>

      {/* ── EU per-member-state offeree caps ──────────────────────────────── */}
      <div className="card mt-5 p-7">
        <h3 className="font-bold text-ink-900">EU per-member-state offeree caps</h3>
        <p className="mt-1 text-sm text-ink-700/70">
          The sub-150 prospectus-exemption count, bounded per member state. Compiles to a
          per-category holder cap on that state's retail token.
        </p>
        <div className="mt-4 flex flex-wrap items-end gap-3">
          <div>
            <label className="label" htmlFor="eu-state">
              Member state
            </label>
            <select
              id="eu-state"
              className="select"
              value={euPick.code}
              onChange={(e) => setEuPick({ ...euPick, code: e.target.value })}
            >
              {EU_MEMBER_STATES.map((m) => (
                <option key={m.code} value={m.code}>
                  {m.name} ({m.code})
                </option>
              ))}
            </select>
          </div>
          <div>
            <label className="label" htmlFor="eu-cap">
              Cap
            </label>
            <input
              id="eu-cap"
              type="number"
              min="1"
              className="input w-28"
              placeholder="149"
              value={euPick.n}
              onChange={(e) => setEuPick({ ...euPick, n: e.target.value })}
            />
          </div>
          <button
            onClick={() => {
              if (Number(euPick.n) > 0) {
                setEuCap(euPick.code, euPick.n)
                setEuPick({ ...euPick, n: '' })
              }
            }}
            className="btn-outline"
          >
            <Icon.check width={16} height={16} /> Add cap
          </button>
        </div>
        {Object.keys(euCaps).length > 0 && (
          <div className="mt-4 flex flex-wrap gap-2">
            {Object.entries(euCaps).map(([code, n]) => {
              const m = EU_MEMBER_STATES.find((s) => s.code === code)
              return (
                <span
                  key={code}
                  className="inline-flex items-center gap-2 rounded-lg border border-ink-900/15 bg-ink-900/[0.02] px-3 py-1.5 text-sm"
                >
                  <span className="font-mono text-xs text-ink-700/70">j:{code}:ret</span>
                  <span className="font-semibold text-ink-900">{Number(n).toLocaleString()}</span>
                  <span className="text-xs text-ink-700/60">{m?.name}</span>
                  <button
                    onClick={() => setEuCap(code, 0)}
                    aria-label={`Remove ${code} cap`}
                    className="text-ink-600 hover:text-rose-600"
                  >
                    <Icon.close width={14} height={14} />
                  </button>
                </span>
              )
            })}
          </div>
        )}
      </div>

      {/* ── Advanced: per-jurisdiction eligibility categories ─────────────── */}
      <div className="card mt-5 overflow-hidden">
        <button
          onClick={() => setAdvOpen((o) => !o)}
          className="flex w-full items-center justify-between px-6 py-4 text-left"
        >
          <span>
            <span className="font-bold text-ink-900">Eligibility categories (advanced)</span>
            <span className="mt-0.5 block text-sm text-ink-700/70">
              Narrow which categories each admitted jurisdiction accepts. The access level sets
              the default; you can only make a rule stricter.
            </span>
          </span>
          <Icon.arrowRight
            width={18}
            height={18}
            className={`shrink-0 text-ink-600 transition-transform ${advOpen ? 'rotate-90' : ''}`}
          />
        </button>
        {advOpen && (
          <div className="max-h-[380px] divide-y divide-ink-900/10 overflow-y-auto border-t border-ink-900/10">
            {admittedCodes.length === 0 ? (
              <p className="px-6 py-6 text-sm text-ink-700/60">
                No jurisdiction is admitted yet. Set at least one to Standard or Qualified only in
                the matrix above.
              </p>
            ) : (
              admittedCodes.map((j) => {
                const sel = eligFor(j.code)
                return (
                  <div key={j.code} className="flex flex-wrap items-center gap-3 px-6 py-3">
                    <div className="w-40 shrink-0">
                      <div className="text-sm font-medium text-ink-900">{j.name}</div>
                      <div className="font-mono text-[11px] text-ink-700/55">
                        {policy[j.code] === 'standard' ? 'Standard' : 'Qualified only'}
                      </div>
                    </div>
                    <div className="flex flex-wrap gap-1.5">
                      {selectableFor(j.code).map((c) => {
                        const on = sel.includes(c.key)
                        return (
                          <button
                            key={c.key}
                            title={c.hint}
                            onClick={() => toggleElig(j.code, c.key)}
                            className={`rounded-md border px-2 py-1 font-mono text-[11px] font-semibold transition-colors ${
                              on
                                ? 'border-btc bg-btc-50 text-btc-700'
                                : 'border-ink-900/15 text-ink-600 hover:bg-ink-900/[0.03]'
                            }`}
                          >
                            j:{j.code}:{c.key}
                          </button>
                        )
                      })}
                    </div>
                  </div>
                )
              })
            )}
          </div>
        )}
      </div>

      {/* ── Compiled-rules preview ────────────────────────────────────────── */}
      <div className="card mt-5 p-7">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 className="font-bold text-ink-900">Compiled policy preview</h3>
            <p className="mt-1 text-sm text-ink-700/70">
              SeqPal compiles the matrix into the rules that check every transfer. This is the
              authoritative compile, computed on the server, not in your browser.
            </p>
          </div>
          <button onClick={previewRules} disabled={previewing} className="btn-outline shrink-0 disabled:opacity-50">
            {previewing ? (
              <>
                <span className="h-4 w-4 animate-spin rounded-full border-2 border-ink-900/20 border-t-ink-700" />
                Compiling
              </>
            ) : (
              <>
                <Icon.bolt width={16} height={16} /> Preview compiled rules
              </>
            )}
          </button>
        </div>
        {previewErr && (
          <p className="mt-4 rounded-lg bg-rose-50 px-3 py-2 text-sm text-rose-700">{previewErr}</p>
        )}
        {preview && <CompiledRulesView preview={preview} />}
      </div>
        </>
      )}
    </div>
  )
}

// A read-only render of the rules seqpald compiled from the matrix. Heights are
// shown as absolute Sequentia block heights per the contract's display rule.
function CompiledRulesView({ preview }) {
  const r = preview.rules || {}
  const rows = []
  if (r.allowed_categories?.length)
    rows.push(['Allowed categories', r.allowed_categories.join('  ')])
  if (r.lockin_until_height)
    rows.push(['Lockup', `until Sequentia block ${Number(r.lockin_until_height).toLocaleString()}`])
  if (r.category_denies?.length)
    rows.push([
      'Reg S denies',
      r.category_denies
        .map((d) => `${d.prefix} until Sequentia block ${Number(d.until_height).toLocaleString()}`)
        .join('; '),
    ])
  if (r.holder_caps_by_category && Object.keys(r.holder_caps_by_category).length)
    rows.push([
      'Per-category caps',
      Object.entries(r.holder_caps_by_category)
        .map(([k, v]) => `${k} = ${Number(v).toLocaleString()}`)
        .join('; '),
    ])
  if (r.holder_cap) rows.push(['Global holder cap', Number(r.holder_cap).toLocaleString()])
  if (r.velocity_window_blocks || r.velocity_max_atoms)
    rows.push([
      'Velocity',
      `${Number(r.velocity_window_blocks || 0).toLocaleString()} blocks` +
        (r.velocity_max_atoms ? ` / ${Number(r.velocity_max_atoms).toLocaleString()} atoms` : ''),
    ])
  rows.push([
    'Primary senders (escrow / treasury)',
    r.primary_aids?.length ? r.primary_aids.join('  ') : 'assigned at deploy',
  ])

  return (
    <div className="mt-5">
      <div className="rounded-xl border border-ink-900/10 bg-ink-900/[0.02] p-5">
        {rows.length === 0 ? (
          <p className="text-sm text-ink-700/70">
            No categories are admitted, so no resident is eligible. Admit at least one
            jurisdiction in the matrix above.
          </p>
        ) : (
          <dl className="divide-y divide-ink-900/10 text-sm">
            {rows.map(([k, v]) => (
              <div key={k} className="flex flex-col gap-1 py-2.5 sm:flex-row sm:items-baseline sm:justify-between sm:gap-6">
                <dt className="shrink-0 text-ink-700/70">{k}</dt>
                <dd className="break-all font-mono text-xs text-ink-900 sm:text-right">{v}</dd>
              </div>
            ))}
          </dl>
        )}
      </div>
      <p className="mt-2 text-xs leading-relaxed text-ink-700/55">
        Chain tip {Number(preview.tip_height || 0).toLocaleString()} ·{' '}
        {Number(preview.blocks_per_day || 0).toLocaleString()} blocks/day. Day-based windows are
        resolved to absolute block heights when you deploy.
      </p>
    </div>
  )
}

/* ──────── Freely-tradable prerequisites: recovery key + attestation ──────── */

// The two extra things a freely-tradable deploy requires: (a) a recovery key,
// whose public half is registered at deploy; (b) the two attestation statements,
// signed with the session key at deploy time.
//
// The recovery key is a SECOND Sequentia wallet, not something generated here.
// Its whole purpose is to still be yours after your everyday key is stolen, and
// a key SeqPal made in this tab fails that twice over: it would put SeqPal in
// the business of running wallets for issuers, and a sibling derived from the
// same seed as the everyday key would be stolen in the same breath. So the
// issuer names the OpenAMP account key of a different wallet -- ideally one on
// different hardware -- and only its public half ever leaves that wallet.
function BearerRequirements({ data, update, hasKey, sessionXonly }) {
  const [entry, setEntry] = useState('')
  const [err, setErr] = useState(null)
  const rec = data.recovery
  const value = entry.trim().toLowerCase()

  const use = () => {
    setErr(null)
    if (!isXonly(value)) {
      setErr('A recovery key is an x-only public key: 64 hex characters.')
      return
    }
    if (sessionXonly && value === String(sessionXonly).toLowerCase()) {
      setErr(
        'That is the key you are signed in with. A recovery key has to be a DIFFERENT wallet, ' +
          'or it is stolen along with the one it is meant to replace.'
      )
      return
    }
    update({ recovery: { xonly: value } })
    setEntry('')
  }

  return (
    <div className="mb-5 space-y-5">
      <div className="card p-6">
        <div className="flex items-center gap-2">
          <Icon.lock width={18} height={18} className="text-seq-600" />
          <h3 className="font-bold text-ink-900">Emergency recovery key</h3>
          {rec?.xonly && (
            <Badge color="emerald" className="ml-auto">
              <Icon.check width={12} height={12} /> set
            </Badge>
          )}
        </div>
        <p className="mt-1.5 text-sm leading-relaxed text-ink-700/80">
          A second Sequentia wallet. If your everyday key is ever stolen, this key replaces it,
          so it has to be a wallet you keep separately, ideally on different hardware. Paste the
          account key that wallet shows for your Sequentia account. Only the public half is
          registered with your token; the wallet that holds it is backed up by its own recovery,
          not by SeqPal.
        </p>
        {!rec?.xonly ? (
          <div className="mt-4 flex flex-wrap items-end gap-3">
            <div className="min-w-[260px] flex-1">
              <label className="label" htmlFor="rec-xonly">
                Recovery wallet account key
              </label>
              <input
                id="rec-xonly"
                className="input font-mono text-xs"
                spellCheck={false}
                placeholder="64 hex characters"
                value={entry}
                onChange={(e) => setEntry(e.target.value)}
              />
            </div>
            <button onClick={use} disabled={!value} className="btn-primary disabled:opacity-50">
              Use this key
            </button>
          </div>
        ) : (
          <div className="mt-4 space-y-3">
            <div className="rounded-lg bg-ink-900/[0.03] px-3 py-2 font-mono text-xs text-ink-800">
              recovery public key: {rec.xonly.slice(0, 16)}…{rec.xonly.slice(-8)}
            </div>
            <button onClick={() => update({ recovery: null })} className="btn-outline w-full">
              Use a different wallet
            </button>
          </div>
        )}
        {err && <p className="mt-3 rounded-lg bg-rose-50 px-3 py-2 text-sm text-rose-700">{err}</p>}
      </div>

      <div className="card p-6">
        <label className="flex cursor-pointer items-start gap-3">
          <input
            type="checkbox"
            className="mt-0.5 h-4 w-4 accent-btc"
            checked={data.bearerPause !== false}
            onChange={(e) => update({ bearerPause: e.target.checked })}
          />
          <span className="text-sm leading-relaxed text-ink-700/85">
            <span className="font-semibold text-ink-900">Emergency pause</span>
            <br />
            With a court or regulator order you can pause all trading of this token, not
            just freeze single balances. This choice is made now and is permanent: it can
            never be added to the token later, and it can never be removed. Pausing uses
            the same signed, public, court-order-only process as a freeze, and lifting
            the pause works the same way.
          </span>
        </label>
      </div>

      <div className="card border-amber-200 bg-amber-50/60 p-6">
        <div className="flex items-center gap-2 text-sm font-semibold text-amber-800">
          <Icon.shield width={16} height={16} /> Required attestation, signed with your key
        </div>
        <label className="mt-3 flex cursor-pointer items-start gap-3">
          <input
            type="checkbox"
            checked={!!data.bearerNoUs}
            onChange={(e) => update({ bearerNoUs: e.target.checked })}
            className="mt-0.5 h-4 w-4 accent-btc"
          />
          <span className="text-sm leading-relaxed text-ink-800">
            My company has no United States operations, assets, or banking.
          </span>
        </label>
        <label className="mt-2 flex cursor-pointer items-start gap-3">
          <input
            type="checkbox"
            checked={!!data.bearerRisk}
            onChange={(e) => update({ bearerRisk: e.target.checked })}
            className="mt-0.5 h-4 w-4 accent-btc"
          />
          <span className="text-sm leading-relaxed text-ink-800">
            I accept in writing that United States regulators may object, and that this is
            my company&rsquo;s risk.
          </span>
        </label>
        <p className="mt-3 text-xs leading-relaxed text-amber-800/80">
          When you deploy, these statements are signed with your SeqPal ID key and recorded
          with the issuance. {!hasKey && 'No Sequentia wallet is connected: sign in with your wallet before deploying so the signature can be made.'}
        </p>
      </div>
    </div>
  )
}

/* ──────────────────── Step 7, Checkout & Deployment ──────────────────── */

export function Step7Checkout({ data, update, onDeployed }) {
  // Both create and deploy go through the store so its issuance list refreshes
  // before we navigate to the new issuance page.
  const { createIssuance, patchIssuance, deployIssuance, xonly, account, hasKey, signBearerStmt } =
    useStore()
  // What this deployment actually charges, rather than what was true wherever
  // the note was written. null until it is known, so nothing is asserted early.
  const [setupFeeUsd, setSetupFeeUsd] = useState(null)
  useEffect(() => {
    let cancelled = false
    health()
      .then((h) => !cancelled && setSetupFeeUsd(typeof h.setup_fee_usd === 'number' ? h.setup_fee_usd : null))
      .catch(() => !cancelled && setSetupFeeUsd(null))
    return () => {
      cancelled = true
    }
  }, [])
  const s = getStructure(data.structureId)
  const cost = computeSetupCost(data.structureId, data.isPublic, {
    raise: data.raise,
    collateral: data.fields?.collateral,
    unit: data.unit,
  })
  const [phase, setPhase] = useState('summary') // summary | working | live
  const [result, setResult] = useState(null) // { issuance, deploy }
  const [err, setErr] = useState(null)

  // The initial supply mints to the issuer's own secure account (the
  // issuer-of-record treasury). Distribution to investors is a later transfer,
  // so any "mints to a placement-portal escrow address" claim would be false
  // today.
  const mintTarget = 'issuer treasury'
  const bearer = data.enforcement === 'bearer'
  const network = data.enforcement === 'network'
  // A network-enforced deploy is prepared first and minted second: the values in
  // `params` come from the issuer's registrar and cannot be produced here or by
  // SeqPal, and `prepared` is what the server hands back to run the registrar
  // against. Both live in this step because they are deployment mechanics, not
  // properties of the offering.
  const [params, setParams] = useState({ user_cmr: '', verifier_cmr: '', pi: '' })
  const [prepared, setPrepared] = useState(null)
  const [paramsOpen, setParamsOpen] = useState(false)
  // A freely-tradable deploy needs two extra things before the button enables:
  // the recovery wallet's key and both attestation checkboxes.
  const bearerReady = !bearer || (!!data.recovery?.xonly && !!data.bearerNoUs && !!data.bearerRisk)
  // One whole token per unit of the target raise, defaulting to 1,000,000 for
  // structures without a raise.
  const supply = Math.max(1, Math.round(parseMoney(data.raise) || 1_000_000))
  const precision = 8

  // Deploy is real: create the issuance record on the server, then mint the
  // OpenAMP restricted asset. The terms object is what the on-chain contract
  // commits to, and seqpald recomputes its canonical hash; the value sent is a
  // cross-check that refuses on mismatch rather than minting the wrong thing.
  const deploy = async () => {
    setErr(null)
    setPhase('working')
    try {
      const terms = toTerms({ ...data, mintTarget })
      // Reuse the draft issuance the compiled-rules preview created, if any, so
      // the preview and the mint are the same record; otherwise create it now.
      let issuanceId = data.issuanceId
      if (issuanceId) {
        await patchIssuance(issuanceId, {
          name: data.name,
          ticker: data.ticker,
          structure_id: data.structureId,
          entity_id: data.principal?.entity_id || '',
          terms,
        })
      } else {
        const issuance = await createIssuance({
          name: data.name,
          ticker: data.ticker,
          structure_id: data.structureId,
          entity_id: data.principal?.entity_id || undefined,
          terms,
        })
        issuanceId = issuance.id
      }
      // Freely-tradable issuance: the signed attestation must be on record
      // before the deploy. Signed with the session key, tagged, over sha256 of
      // the canonical attestation JSON; the server verifies it against this
      // account's key and refuses the deploy without it.
      if (bearer) {
        const fields = {
          issuance_id: issuanceId,
          no_us_nexus: !!data.bearerNoUs,
          risk_accepted: !!data.bearerRisk,
          aid: account?.aid,
        }
        const sig = await signBearerStmt(fields)
        if (!sig) throw new Error('Your wallet did not return a signature for the attestation, so nothing was deployed.')
        await bearerAttestation(issuanceId, { ...fields, pubkey: xonly, sig })
      }
      const dep = await deployIssuance({
        issuance_id: issuanceId,
        supply,
        precision,
        // The enforcement election travels with the deploy. A freely-tradable
        // asset has no issuer recovery power; otherwise the toggle from the
        // tokenomics step decides.
        enforcement: data.enforcement || 'serviced',
        clawback: bearer ? false : data.clawback !== false,
        // M9: external issuer key. The entity's own SeqPal ID key becomes the
        // issuer half, so reclaiming tokens needs the issuer's browser
        // signature (two signatures in total) and the platform never holds an
        // issuer key for this asset. seqpald cross-checks it against the
        // deploying account.
        ...(xonly ? { issuer_pubkey: xonly } : {}),
        // The emergency key for a freely-tradable asset, generated and exported
        // in a second wallet the issuer names; only its public key is sent. The pause election is
        // permanent and committed into the token, so it travels with the deploy.
        ...(bearer && data.recovery?.xonly ? { recovery_pubkey: data.recovery.xonly } : {}),
        ...(bearer ? { pause: data.bearerPause !== false } : {}),
        // 0 = let the server derive the network fee conversion from the
        // offering price (a nonzero value would be an explicit issuer override).
        fee_convert_atoms: 0,
        // Network-enforced only: the values the issuer's registrar produced for
        // this policy. Sent only when supplied; without them the server prepares
        // the deployment and hands back what to run, which is the flow below.
        ...(network && params.pi
          ? {
              user_cmr: params.user_cmr.trim().toLowerCase(),
              verifier_cmr: params.verifier_cmr.trim().toLowerCase(),
              pi: params.pi.trim().toLowerCase(),
            }
          : {}),
        terms,
        terms_hash: await termsHash(terms),
      })
      setResult({ issuanceId, deploy: dep })
      setPhase('live')
    } catch (e) {
      // A prepared-but-unminted deployment is not a failure: the server has
      // fixed the asset and is waiting for the registrar's values. Keep what it
      // returned, open the parameter panel, and let the issuer paste them in.
      if (e.status === 409 && e.data?.stage === 'prepared') {
        setPrepared(e.data)
        setParamsOpen(true)
        setParams((p) => ({ ...p, pi: e.data.policy_commitment || '' }))
        // Carry the draft id forward so the retry resumes the same preparation
        // instead of creating a second issuance.
        if (e.data.issuance_id && !data.issuanceId) update({ issuanceId: e.data.issuance_id })
      }
      setErr({ message: e.message, status: e.status })
      setPhase('summary')
    }
  }

  if (phase === 'working') {
    return (
      <div>
        <StepHeader n={7} title="Deploying on Sequentia" />
        <div className="card p-10 text-center">
          <span className="mx-auto block h-10 w-10 animate-spin rounded-full border-4 border-btc/20 border-t-btc" />
          <p className="mt-5 font-medium text-ink-900">Minting on Sequentia…</p>
          <p className="mt-1 text-sm text-ink-700/70">
            Registering your key and issuing the asset under the rules you configured.
          </p>
        </div>
      </div>
    )
  }

  if (phase === 'live') {
    const d = result.deploy
    return (
      <div>
        <StepHeader n={7} title="Deployed on Sequentia" />
        <div className="card p-8">
          <div className="flex items-center gap-4">
            <span className="flex h-12 w-12 items-center justify-center rounded-full bg-emerald-500 text-white">
              <Icon.check width={26} height={26} />
            </span>
            <div>
              <h3 className="font-bold text-ink-900">Asset minted</h3>
              <p className="text-sm text-ink-700/80">
                {data.name} ({data.ticker}) is a real {bearer ? 'freely-tradable' : 'transfer-restricted'}{' '}
                asset on the Sequentia testnet.
              </p>
            </div>
          </div>

          <dl className="mt-6 divide-y divide-ink-900/10 text-sm">
            {[
              ['Asset id', d.asset],
              ['Issuance txid', d.txid],
              ['Contract hash', d.contract_hash],
              ['Holder account (AID)', d.aid],
              ['Secure address', d.address],
              ...(d.fee_convert_atoms
                ? [
                    [
                      'Network fee conversion',
                      `${Number(d.fee_convert_atoms).toLocaleString()} atoms, derived from your offering price`,
                    ],
                  ]
                : []),
            ].map(([k, v]) => (
              <div key={k} className="flex items-center justify-between gap-4 py-2.5">
                <dt className="shrink-0 text-ink-700/70">{k}</dt>
                <dd className="break-all text-right font-mono text-xs text-ink-900">
                  {v || 'not returned'}
                </dd>
              </div>
            ))}
          </dl>

          <div className="mt-5 rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-xs leading-relaxed text-amber-900">
            The transaction is broadcast, and nothing is final at 0 confirmations. Your issuance
            page tracks confirmations and the Bitcoin anchor depth as they land, and you can
            verify the identifiers on a Sequentia explorer.
          </div>

          <button onClick={() => onDeployed(result.issuanceId)} className="btn-primary mt-6 w-full">
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
        n={7}
        title="Checkout and deployment"
        sub="Review the summary and deploy. The setup fee is simulated in this build; the deploy is real and mints the asset on Sequentia."
      />

      {bearer && (
        <BearerRequirements data={data} update={update} hasKey={hasKey} sessionXonly={xonly} />
      )}
      {network && (
        <NetworkRuleParameters
          params={params}
          setParams={setParams}
          prepared={prepared}
          open={paramsOpen}
          setOpen={setParamsOpen}
        />
      )}

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
              ['Asset name', data.name || 'not set'],
              ['Ticker', data.ticker || 'not set'],
              ['Offering type', data.isPublic ? 'Public offering' : 'Private placement'],
              [
                'Who enforces the rules',
                bearer
                  ? 'Nobody restricts transfers: freely tradable, court-order freezes only'
                  : data.enforcement === 'network'
                    ? 'The network, from your published rules'
                    : 'SeqPal checks every transfer against your rules',
              ],
              ['Unit of account', data.unit === 'BTC' ? 'BTC (₿)' : 'USD ($)'],
              ['Target raise', data.raise || 'not set'],
              ['Initial mint to', mintTarget],
              ['Initial supply', supply.toLocaleString() + ' ' + (data.ticker || 'tokens')],
              ...(bearer
                ? []
                : [
                    [
                      'Issuer recovery power',
                      data.clawback !== false ? 'On: two signatures, always public' : 'Off',
                    ],
                  ]),
              ['Network fee conversion', 'Derived from your offering price'],
              ['Network', 'Sequentia'],
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
                {cost.simple ? 'Setup, Simple Native Equity' : `Setup, ${s?.short}`}
              </dt>
              <dd className="font-mono font-medium">${cost.base.toLocaleString()}</dd>
            </div>
            {cost.secured > 0 && (
              <div className="flex justify-between">
                <dt className="text-ink-700/80">Secured collateral add-on (est.)</dt>
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
              <dt className="font-semibold text-ink-900">Setup fee</dt>
              <dd className="font-mono font-bold text-ink-900">${cost.total.toLocaleString()}</dd>
            </div>
          </dl>
          <div className="mt-4 space-y-1.5 text-xs text-ink-700/60">
            <p>
              + ${s?.annual.toLocaleString()}/yr support
              {data.isPublic && data.structureId !== 'depository-receipt'
                ? ' + $6,000/yr public reporting'
                : ''}
              , billed after launch.
            </p>
            <p>
              + Escrow and Settlement Fee, when you hire SeqPal for escrow: 0.25%/mo on the
              subscription funds held ($5K min, 3% cap, typically about 1% of the raise). Escrow
              is optional; if you do not, subscription payments go directly to you.
            </p>
          </div>
          <DemoNote className="mt-5">
            {setupFeeUsd === 0
              ? 'No setup fee is charged on this deployment, so the deploy below runs straight away.'
              : setupFeeUsd > 0
                ? `The setup fee is $${setupFeeUsd.toLocaleString()} and the deploy is refused until it is paid.`
                : 'The deploy below is real and mints on the Sequentia testnet.'}{' '}
            The deploy below is real and mints on the Sequentia testnet.
          </DemoNote>
          {err && (
            <div className="mt-4 rounded-xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm">
              <p className="font-medium text-rose-700">{err.message}</p>
              {DEPLOY_HINT[err.status] && (
                <p className="mt-1 text-xs text-rose-700/80">{DEPLOY_HINT[err.status]}</p>
              )}
            </div>
          )}
          <button
            onClick={deploy}
            disabled={!bearerReady}
            className="btn-primary mt-5 w-full disabled:opacity-50"
          >
            <Icon.bolt width={16} height={16} />
            Deploy on Sequentia
          </button>
          {!bearerReady && (
            <p className="mt-2 text-center text-xs leading-relaxed text-amber-700">
              A freely-tradable deploy needs the recovery wallet's key and both signed
              statements above first.
            </p>
          )}
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

/* Network-enforced deploys need three values an ordinary issuer cannot compute
   and SeqPal cannot compute either: they come from the issuer's registrar, and
   they can only be produced once the deployment is prepared (the values depend
   on the token's own identifier, which is fixed at preparation). So this panel
   is deliberately two-stage: deploy once to prepare, run what the server hands
   back, paste the results, deploy again. Nothing is minted until the second
   deploy succeeds.

   Everything the registrar has to run is rendered from the server's response
   rather than written here, so this screen never has to name a tool. */
function NetworkRuleParameters({ params, setParams, prepared, open, setOpen }) {
  const [copied, setCopied] = useState('')
  const doc = prepared?.registrar_document
    ? JSON.stringify(prepared.registrar_document, null, 2)
    : ''
  const copy = async (what, text) => {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(what)
      setTimeout(() => setCopied(''), 1500)
    } catch {
      setCopied('')
    }
  }
  const field = (key, label) => (
    <label key={key} className="block">
      <span className="text-xs font-medium text-ink-700/80">{label}</span>
      <input
        value={params[key]}
        onChange={(e) => setParams({ ...params, [key]: e.target.value })}
        spellCheck={false}
        placeholder="64 characters"
        className="mt-1 w-full rounded-lg border border-ink-900/15 px-3 py-2 font-mono text-xs text-ink-900"
      />
    </label>
  )
  return (
    <div className="card mb-5 p-6">
      {prepared && (
        <div className="mb-5 rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-900">
          <p className="font-semibold">Your deployment is prepared. Nothing has been minted.</p>
          <p className="mt-1 text-xs leading-relaxed">
            Send the document below to your registrar, run the command it gives you, and paste
            the three values it prints into the fields below. Then deploy again: the token
            identifier is already fixed, so the second deploy mints exactly what was prepared.
          </p>
          <dl className="mt-3 space-y-1 text-xs">
            <div className="flex justify-between gap-3">
              <dt className="shrink-0 text-amber-900/70">Token identifier</dt>
              <dd className="break-all font-mono">{prepared.asset}</dd>
            </div>
            <div className="flex justify-between gap-3">
              <dt className="shrink-0 text-amber-900/70">Policy commitment</dt>
              <dd className="break-all font-mono">{prepared.policy_commitment}</dd>
            </div>
          </dl>
          {prepared.registrar_command && (
            <div className="mt-3">
              <p className="text-xs font-medium">Command your registrar runs</p>
              <pre className="mt-1 overflow-x-auto rounded-lg bg-amber-100/70 px-3 py-2 font-mono text-[11px] text-amber-900">
                {prepared.registrar_command}
              </pre>
            </div>
          )}
          {doc && (
            <div className="mt-3">
              <div className="flex items-center justify-between">
                <p className="text-xs font-medium">Document to run it against</p>
                <button
                  type="button"
                  onClick={() => copy('doc', doc)}
                  className="text-xs font-semibold text-amber-900 underline"
                >
                  {copied === 'doc' ? 'Copied' : 'Copy'}
                </button>
              </div>
              <pre className="mt-1 max-h-48 overflow-auto rounded-lg bg-amber-100/70 px-3 py-2 font-mono text-[11px] text-amber-900">
                {doc}
              </pre>
            </div>
          )}
        </div>
      )}
      <button
        type="button"
        onClick={() => setOpen(!open)}
        className="flex w-full items-center justify-between text-left"
      >
        <span className="font-bold text-ink-900">Advanced: on-chain rule parameters</span>
        <span className="text-xs font-semibold text-btc-600">{open ? 'Hide' : 'Show'}</span>
      </button>
      {open && (
        <div className="mt-4 space-y-3">
          <p className="text-xs leading-relaxed text-ink-700/80">
            Your registrar produces these values for your policy. Paste them here. You do not
            need them for the first deploy: deploy once, and SeqPal will tell you exactly what to
            send your registrar.
          </p>
          {field('user_cmr', 'Holder program identity')}
          {field('verifier_cmr', 'Rules program identity')}
          {field('pi', 'Policy commitment')}
        </div>
      )}
    </div>
  )
}

// What the server's deploy refusal means. The server's own message is shown
// verbatim above; this only adds context the server cannot know.
const DEPLOY_HINT = {
  400: 'The mint parameters were refused. Fix the issuance and try again: nothing was minted.',
  403: 'This issuance belongs to another SeqPal ID.',
  404: 'The issuance record could not be found on the server.',
  409: 'Choose a different ticker. Tickers are checked against the assets already live on the platform.',
  429: 'The deploy rate limit is per account and per platform over a rolling hour. Wait and try again.',
  501: 'This deployment cannot run network-enforced rules. Pick a supported enforcement model and deploy again; nothing was minted.',
  502: 'SeqPal’s transfer service refused or could not be reached. Nothing was minted.',
  503: 'The platform has no issuer token configured, so no deployment can be made from here right now.',
}
