import { useEffect, useState } from 'react'
import { Icon } from './icons'
import { Badge } from './ui'
import CopyId from './CopyId'
import * as api from '../lib/api'

// The RFSA Financial Products Registry filing (M4). A public offering cannot
// deploy without a filing, which seqpald enforces (a 403 at deploy otherwise).
// The regulator RELATIONSHIP is a labeled simulation; the number, the public
// lookup, the deploy gate, and the anchored filing hash are all real.
//
// The filing binds the terms_hash and the document manifest hash, so it is
// generated from the same deterministic document package the data room shows.
export default function RfsaFilingCard({ iss }) {
  const [hashes, setHashes] = useState(null)
  const [state, setState] = useState({ busy: false, err: null, filing: null })

  useEffect(() => {
    let cancelled = false
    // Deterministically resolve terms_hash + manifest_hash for the filing. The
    // generator is content-addressed and idempotent, so this never mints or
    // double-writes anything.
    api
      .generateDocuments(iss.id)
      .then((r) => {
        if (!cancelled) setHashes({ termsHash: r.terms_hash, manifestHash: r.manifest_hash })
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [iss.id])

  const file = async () => {
    if (!hashes) return
    setState({ busy: true, err: null, filing: null })
    try {
      const res = await api.rfsaFile({
        structure: iss.structureId,
        terms_hash: hashes.termsHash,
        doc_manifest_hash: hashes.manifestHash,
        issuance_id: iss.id,
      })
      setState({ busy: false, err: null, filing: res.filing })
    } catch (e) {
      setState({ busy: false, err: e.message, filing: null })
    }
  }

  const filing = state.filing

  return (
    <div className="card p-6">
      <div className="flex items-center gap-2">
        <Icon.building width={18} height={18} className="text-btc-600" />
        <h2 className="font-bold text-ink-900">RFSA filing</h2>
        <Badge color="amber" className="ml-auto">
          Simulated regulator
        </Badge>
      </div>
      <p className="mt-1.5 text-sm leading-relaxed text-ink-700/70">
        This is marked a public offering, so it cannot deploy until it is filed with the RFSA
        Financial Products Registry. The filing binds the terms hash and the document manifest and
        is anchored on chain.
      </p>

      {!filing ? (
        <>
          <button
            onClick={file}
            disabled={state.busy || !hashes}
            className="btn-primary mt-4 w-full disabled:opacity-60"
          >
            {state.busy ? 'Filing…' : hashes ? 'File with the RFSA registry' : 'Preparing the package…'}
          </button>
          {state.err && <p className="mt-2 text-sm text-rose-700">{state.err}</p>}
        </>
      ) : (
        <div className="mt-4 space-y-2">
          <div className="flex items-center justify-between rounded-lg bg-emerald-50 px-4 py-2.5">
            <span className="text-sm text-emerald-800">Filed</span>
            <span className="font-mono text-sm font-semibold text-emerald-800">
              {filing.filing_number}
            </span>
          </div>
          <div className="rounded-lg bg-ink-900/[0.03] px-3 py-2">
            <div className="text-[11px] font-semibold uppercase tracking-wide text-ink-700/60">
              Filing hash (anchored)
            </div>
            <div className="mt-1">
              <CopyId value={filing.filing_hash} label="filing hash" />
            </div>
          </div>
          {filing.anchor_txid && (
            <div className="rounded-lg bg-ink-900/[0.03] px-3 py-2">
              <div className="text-[11px] font-semibold uppercase tracking-wide text-ink-700/60">
                Anchor txid
              </div>
              <div className="mt-1">
                <CopyId value={filing.anchor_txid} kind="tx" label="anchor txid" />
              </div>
            </div>
          )}
        </div>
      )}
      <p className="mt-3 text-[11px] leading-relaxed text-ink-700/55">
        Simulated regulator, real registry mechanics. The filing is publicly resolvable on the Legal
        and Licensing page and gates this deploy on the server, not just here.
      </p>
    </div>
  )
}
