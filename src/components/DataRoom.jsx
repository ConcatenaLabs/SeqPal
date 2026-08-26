import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Icon } from './icons'
import { Badge } from './ui'
import CopyId from './CopyId'
import * as api from '../lib/api'
import { useStore } from '../lib/store'
import OfflineSignature from './OfflineSignature'

// The data room (M4). seqpald generates a deterministic, content-addressed
// offering document set and binds its manifest INTO the terms object, so the
// on-chain terms_hash commits to the exact document bytes. This surface shows
// the documents with their hashes, the hash-to-chain binding, the e-signature
// flow (a real BIP340 signature by the enclave key over the tagged document
// hash), and the mandatory risk and subscription acknowledgment.
//
// Access model: the manifest (hashes) is public immediately; a document preimage
// is gate-passers-only during the offer window and publishes at offer close.

const KIND_META = {
  'offering-memorandum': {
    label: 'Offering memorandum',
    note: 'Per-structure risk factors, including the mandatory clawback risk factor.',
  },
  'subscription-agreement': {
    label: 'Subscription agreement',
    note: 'The investor commitment, executed by e-signature.',
  },
  'operating-agreement': {
    label: 'Operating agreement',
    note: 'Declares the on-chain register dispositive under Próspera law.',
  },
  'escrow-terms': {
    label: 'Escrow terms',
    note: 'Custody and release conditions for subscription funds.',
  },
  'kid-summary': {
    label: 'Key information summary',
    note: 'Not a PRIIPs KID; an SPV or depositary receipt sold to EU retail requires one.',
  },
  'uk-investor-statement': {
    label: 'UK investor statement',
    note: 'SI 2024/301 wording and the income and net-asset thresholds.',
  },
  'lift-artifact': {
    label: 'Basis of admission',
    note: 'The lift artifact recording why each jurisdiction is admitted.',
  },
  // The bearer charter set (freely tradable issuances).
  'articles-of-incorporation': {
    label: 'Articles of incorporation',
    note: 'The ledger-securities election, the on-chain share ledger designation, and the transfer approval waiver.',
  },
  'freeze-power-charter': {
    label: 'Freeze power charter',
    note: 'The court-order-only freeze and pause powers, two-key custody, and the public record property.',
  },
  'shareholder-terms': {
    label: 'Shareholder terms',
    note: 'Anyone may hold and trade; dividends and voting attach by registering as a holder of record.',
  },
  'risk-acceptance-instrument': {
    label: 'Risk acceptance instrument',
    note: 'The signed no-US-nexus and regulator-objection acceptance, refreshed annually; the company bears the stated risk.',
  },
}

function kindMeta(kind) {
  return KIND_META[kind] || { label: kind, note: '' }
}

function DocRow({ doc, offerOpen, canSign }) {
  const { signDoc, hasKey } = useStore()
  const [sigCount, setSigCount] = useState(null)
  const [state, setState] = useState({ busy: false, err: null, ok: false })
  // A SeqPal ID that is only a wallet has nothing here that can sign: it is
  // handed the exact characters and pastes the signature back.
  const [prep, setPrep] = useState(null)
  const [sig, setSig] = useState('')
  const meta = kindMeta(doc.kind)

  const loadSigs = () => {
    let cancelled = false
    api
      .docSignatures(doc.hash)
      .then((r) => {
        if (!cancelled) setSigCount((r.signatures || []).length)
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }
  useEffect(loadSigs, [doc.hash])

  const sign = async () => {
    setState({ busy: true, err: null, ok: false })
    try {
      // The label only names the document in the wallet's prompt; the
      // signature commits to the hash, which is what seqpald verifies.
      if (!hasKey) {
        setPrep(await api.signDocumentSig(doc.hash, ''))
        setState({ busy: false, ok: false, err: null })
        return
      }
      const signature = await signDoc(doc.hash, meta.label)
      if (!signature) {
        setState({
          busy: false,
          ok: false,
          err: 'Your wallet did not return a signature, so nothing was signed. SeqPal never holds the key that signs this.',
        })
        return
      }
      await api.signDocumentSig(doc.hash, signature)
      setState({ busy: false, ok: true, err: null })
      loadSigs()
    } catch (e) {
      setState({ busy: false, ok: false, err: e.message })
    }
  }

  const signPasted = async () => {
    setState({ busy: true, err: null, ok: false })
    try {
      await api.signDocumentSig(doc.hash, sig.trim())
      setSig('')
      setPrep(null)
      setState({ busy: false, ok: true, err: null })
      loadSigs()
    } catch (e) {
      setState({ busy: false, ok: false, err: e.message })
    }
  }

  return (
    <div className="rounded-xl border border-ink-900/10 px-4 py-3">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <Icon.doc width={16} height={16} className="shrink-0 text-seq-600" />
            <span className="font-semibold text-ink-900">{meta.label}</span>
            {sigCount > 0 && (
              <Badge color="emerald">
                <Icon.check width={11} height={11} /> {sigCount} signed
              </Badge>
            )}
          </div>
          {meta.note && <p className="mt-1 text-xs leading-relaxed text-ink-700/70">{meta.note}</p>}
          <div className="mt-2">
            <CopyId value={doc.hash} label="document hash" />
          </div>
        </div>
        <div className="flex shrink-0 flex-col items-end gap-1.5">
          <div className="flex gap-1.5">
            <a
              href={api.docUrl(doc.hash)}
              target="_blank"
              rel="noopener noreferrer"
              className="btn-outline px-2.5 py-1.5 text-xs"
            >
              <Icon.external width={13} height={13} /> HTML
            </a>
            <a
              href={api.docUrl(doc.hash, { pdf: true })}
              target="_blank"
              rel="noopener noreferrer"
              className="btn-outline px-2.5 py-1.5 text-xs"
            >
              <Icon.doc width={13} height={13} /> PDF
            </a>
          </div>
          {canSign && (
            <button
              onClick={sign}
              disabled={state.busy}
              className="btn-ghost px-2.5 py-1.5 text-xs disabled:opacity-60"
            >
              {state.busy ? 'Signing…' : state.ok ? 'Signed' : 'E-sign'}
            </button>
          )}
        </div>
      </div>
      {state.err && <p className="mt-2 text-xs text-rose-700">{state.err}</p>}
      <OfflineSignature
        prep={prep}
        sig={sig}
        onSig={setSig}
        onSubmit={signPasted}
        busy={state.busy}
        label="Record the signature"
      />
      {!offerOpen && (
        <p className="mt-2 text-[11px] text-ink-700/55">
          Offer window closed: this preimage is public.
        </p>
      )}
    </div>
  )
}

function Characterization({ structureId }) {
  const [ch, setCh] = useState(null)
  useEffect(() => {
    let cancelled = false
    api
      .characterization(structureId)
      .then((r) => {
        if (!cancelled) setCh(r.characterization)
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [structureId])
  if (!ch) return null
  return (
    <div className="mt-5 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] px-4 py-3">
      <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">
        Instrument characterization
      </div>
      <p className="mt-1.5 text-sm leading-relaxed text-ink-700/80">{ch.analysis}</p>
      <div className="mt-2 flex flex-wrap gap-1.5">
        <Badge color="slate">{ch.instrument}</Badge>
        {ch.is_aif && <Badge color="amber">AIF (AIFMD)</Badge>}
        {ch.is_cis && <Badge color="amber">CIS (FSMA s.235)</Badge>}
        {ch.priips && <Badge color="amber">PRIIPs KID for EU retail</Badge>}
        <Badge color={ch.eu_retail_lift ? 'emerald' : 'slate'}>
          EU retail lift {ch.eu_retail_lift ? 'available' : 'disabled'}
        </Badge>
      </div>
      <p className="mt-2 text-[11px] text-ink-700/55">
        This classification feeds the matrix compiler: an AIF-classified structure restricts EU
        access to professional categories. Production analysis, not legal advice.
      </p>
    </div>
  )
}

export default function DataRoom({ iss }) {
  const [state, setState] = useState({ loading: true })
  const [gen, setGen] = useState({ busy: false, err: null })
  const [ack, setAck] = useState(false)

  const load = async () => {
    // Resolve a terms_hash to fetch the manifest. A live issuance exposes its
    // genesis terms_hash through the amendments endpoint; a draft has one only
    // after the package is generated (held in component state below).
    try {
      if (iss.live) {
        const a = await api.amendments(iss.id)
        if (a.genesis_terms_hash) {
          const terms = await api.getTerms(a.genesis_terms_hash)
          setState({ loading: false, terms, generated: true })
          return
        }
      }
      setState({ loading: false, terms: null, generated: false })
    } catch (e) {
      setState({ loading: false, error: e.message })
    }
  }
  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [iss.id, iss.live])

  const generate = async () => {
    setGen({ busy: true, err: null })
    try {
      const res = await api.generateDocuments(iss.id)
      const terms = await api.getTerms(res.terms_hash)
      setState({ loading: false, terms, generated: true })
      setGen({ busy: false, err: null })
    } catch (e) {
      setGen({ busy: false, err: e.message })
    }
  }

  const { loading, terms, generated, error } = state
  const docs = terms?.manifest?.set || []
  const offerOpen = terms?.access?.offer_open !== false
  const termsHash = terms?.terms_hash
  const manifestHash = terms?.manifest_hash

  return (
    <div className="card p-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <Icon.receipt width={18} height={18} className="text-seq-600" />
          <h2 className="font-bold text-ink-900">Data room</h2>
        </div>
        {generated && (
          <Badge color={offerOpen ? 'amber' : 'emerald'}>
            {offerOpen ? 'Offer window open' : 'Preimages public'}
          </Badge>
        )}
      </div>
      <p className="mt-1.5 text-sm leading-relaxed text-ink-700/70">
        seqpald renders the offering documents deterministically and content-addresses each one.
        A hash of the package is bound inside the terms object, so the on-chain{' '}
        <span className="font-mono text-xs">terms_hash</span> commits to the exact document bytes.
      </p>

      {loading && (
        <div className="mt-6 flex justify-center py-6">
          <span className="h-6 w-6 animate-spin rounded-full border-4 border-btc/20 border-t-btc" />
        </div>
      )}
      {error && (
        <p className="mt-4 rounded-lg border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700">
          {error}
        </p>
      )}

      {!loading && !generated && (
        <div className="mt-5">
          <button onClick={generate} disabled={gen.busy} className="btn-primary disabled:opacity-60">
            {gen.busy ? 'Generating…' : 'Generate the document package'}
            <Icon.arrowRight width={15} height={15} />
          </button>
          {gen.err && <p className="mt-2 text-sm text-rose-700">{gen.err}</p>}
          <p className="mt-2 text-xs leading-relaxed text-ink-700/60">
            The package is deterministic: the same terms always produce the same documents and the
            same hashes. Generating it binds the manifest into the terms so a deploy commits to it.
          </p>
        </div>
      )}

      {!loading && generated && (
        <>
          <div className="mt-5 grid gap-2 sm:grid-cols-2">
            <div className="rounded-lg bg-ink-900/[0.03] px-3 py-2">
              <div className="text-[11px] font-semibold uppercase tracking-wide text-ink-700/60">
                Manifest hash
              </div>
              <div className="mt-1">
                <CopyId value={manifestHash} label="manifest hash" />
              </div>
            </div>
            <div className="rounded-lg bg-ink-900/[0.03] px-3 py-2">
              <div className="text-[11px] font-semibold uppercase tracking-wide text-ink-700/60">
                terms_hash (committed on chain)
              </div>
              <div className="mt-1">
                <CopyId value={termsHash} label="terms hash" />
              </div>
            </div>
          </div>

          <div className="mt-4 space-y-2">
            {docs.map((d) => (
              <DocRow key={d.hash} doc={d} offerOpen={offerOpen} canSign />
            ))}
          </div>

          <Characterization structureId={iss.structureId} />

          {/* Mandatory clawback risk factor, surfaced out of the memorandum. */}
          <div className="mt-5 rounded-xl border border-amber-200 bg-amber-50 px-4 py-3">
            <div className="flex items-center gap-2 text-sm font-semibold text-amber-800">
              <Icon.shield width={15} height={15} /> Mandatory risk factor: clawback
            </div>
            <p className="mt-1.5 text-sm leading-relaxed text-amber-800/90">
              This asset is clawback-enabled. The issuer can seize a holder's position through a
              two-phase clawback: the issuer signs with its own key, held in its browser, and the
              registrar co-signs. SeqPal holds no key that can move a holder's position on its own.
              Every routine transfer carries SeqPal's policy co-signature, which is negative control,
              the power to refuse a transfer that breaks the issuer's rules, not the power to
              originate one. This same risk factor appears in the offering memorandum above.
            </p>
          </div>

          {/* The investor acknowledgment, previewed for the issuer. Investors
              read and accept this on the placement portal before they subscribe;
              here it shows the issuer the binding their investors will see. */}
          <div className="mt-4 rounded-xl border border-ink-900/10 px-4 py-3">
            <div className="text-[11px] font-semibold uppercase tracking-wide text-ink-700/60">
              Investor acknowledgment, as your investors see it
            </div>
            <label className="mt-2 flex cursor-pointer items-start gap-3">
              <input
                type="checkbox"
                className="mt-0.5 h-4 w-4 accent-btc"
                checked={ack}
                onChange={(e) => setAck(e.target.checked)}
              />
              <span className="text-sm leading-relaxed text-ink-800">
                I have read the offering memorandum, the subscription agreement, and the risk
                factors, and I understand that the on-chain register is dispositive and that every
                transfer is policy co-signed between eligible holders.
              </span>
            </label>
            <p className="mt-3 text-xs leading-relaxed text-ink-700/60">
              Investors accept this and subscribe on your placement portal, where subscription and
              settlement run on the Sequentia testnet. On-chain USDX and BTC settle for real, and
              card and bank rails are simulated and labeled.
            </p>
          </div>

          <div className="mt-4 rounded-lg bg-ink-900/[0.03] px-4 py-3 text-xs leading-relaxed text-ink-700/70">
            {terms?.access?.model}{' '}
            {iss.assetId && (
              <Link
                to={`/docs/verify?asset=${encodeURIComponent(iss.assetId)}`}
                className="font-medium text-seq-600 hover:underline"
              >
                Verify the terms-to-chain binding independently.
              </Link>
            )}
          </div>
          <p className="mt-2 text-[11px] leading-relaxed text-ink-700/55">
            E-signature: the signature is real, over the real document hash &mdash; tagged and made
            by your OpenAMP key, or an ordinary signed message from a wallet you have linked &mdash;
            and it is stored with the key or address that checks it, so anyone can. Only the
            provider-grade e-signature interface is a simulation.
          </p>
        </>
      )}
    </div>
  )
}
