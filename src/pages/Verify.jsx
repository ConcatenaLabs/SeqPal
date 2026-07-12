import { useEffect, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { Icon } from '../components/icons'
import { Badge } from '../components/ui'
import CopyId from '../components/CopyId'
import { getAsset, canonicalJSON, termsHash } from '../lib/openamp'
import { getRegistryEntry } from '../lib/market'
import { txUrl, assetUrl, registryPath, EXPLORER } from '../lib/chain'

// Public "Verify independently" explainer (M3 requirement 5 / item 75). It walks
// an auditor with no SeqPal session from the terms document, to terms_hash, to
// the on-chain contract_hash, to the policy-key commitment, with the live chain
// links at each step. The terms DOCUMENT itself is generated in M4; what M3
// provides is this verification chain and the ability to recompute the on-chain
// contract commitment in the auditor's own browser.

function Step({ n, title, children }) {
  return (
    <div className="flex gap-4">
      <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-ink-900 text-sm font-bold text-white">
        {n}
      </div>
      <div className="min-w-0 flex-1 pb-8">
        <h3 className="font-bold text-ink-900">{title}</h3>
        <div className="mt-2 space-y-2 text-sm leading-relaxed text-ink-700/80">{children}</div>
      </div>
    </div>
  )
}

function KV({ k, children }) {
  return (
    <div className="flex flex-col gap-1 rounded-lg bg-ink-900/[0.03] px-3 py-2 sm:flex-row sm:items-center sm:justify-between">
      <span className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">{k}</span>
      <span className="min-w-0">{children}</span>
    </div>
  )
}

export default function Verify() {
  const [params] = useSearchParams()
  const assetId = params.get('asset') || ''
  const [state, setState] = useState({ loading: !!assetId })

  useEffect(() => {
    if (!assetId) return undefined
    let cancelled = false
    setState({ loading: true })
    ;(async () => {
      try {
        const asset = await getAsset(assetId)
        // Recompute the on-chain commitment in the browser: sha256 of the
        // canonical contract JSON must equal the on-chain contract_hash.
        let recomputed = null
        try {
          const obj = typeof asset.contract === 'string' ? JSON.parse(asset.contract) : asset.contract
          recomputed = await termsHash(obj)
        } catch {
          recomputed = null
        }
        let registry = null
        try {
          registry = await getRegistryEntry(assetId)
        } catch {
          registry = null
        }
        if (!cancelled) setState({ loading: false, asset, recomputed, registry })
      } catch (e) {
        if (!cancelled) setState({ loading: false, error: e.message })
      }
    })()
    return () => {
      cancelled = true
    }
  }, [assetId])

  const { loading, asset, recomputed, registry, error } = state
  const contract =
    asset && (typeof asset.contract === 'string' ? safeParse(asset.contract) : asset.contract)
  const termsHashOnChain = contract?.terms_hash
  const hashMatch = asset && recomputed && recomputed === asset.contract_hash

  return (
    <section className="container-x max-w-3xl py-14">
      <div className="eyebrow">Trust nothing, verify everything</div>
      <h1 className="mt-3 text-3xl font-extrabold tracking-tight text-ink-900 sm:text-4xl">
        Verify independently
      </h1>
      <p className="mt-4 text-lg leading-relaxed text-ink-700/90">
        You do not have to trust SeqPal for any financial fact about a SeqPal-managed asset. Every
        claim resolves to something you can check yourself on the Sequentia chain and the public
        policy server. This page walks the chain from the offering terms to the on-chain
        commitment to the key that co-signs every transfer.
      </p>

      {!assetId && (
        <div className="mt-6 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] px-4 py-3 text-sm text-ink-700/70">
          Open this page from a deployed asset (or add <span className="font-mono">?asset=&lt;id&gt;</span>{' '}
          to the URL) to see the live on-chain values filled in and recomputed in your browser.
        </div>
      )}

      {loading && (
        <div className="mt-8 flex justify-center py-10">
          <span className="h-8 w-8 animate-spin rounded-full border-4 border-btc/20 border-t-btc" />
        </div>
      )}
      {error && (
        <div className="mt-8 rounded-xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700">
          The policy server did not return this asset ({error}). It may not be deployed, or the id
          may be wrong.
        </div>
      )}

      <div className="mt-10">
        <Step n={1} title="The offering terms document">
          <p>
            An offering is described by a terms document. Its canonical form is hashed to a{' '}
            <span className="font-mono text-xs">terms_hash</span>: change one character of the terms
            and the hash changes. The document generator and the document-to-hash manifest arrive in
            the next milestone; today the hash commitment is already live and checkable.
          </p>
          {termsHashOnChain && (
            <KV k="terms_hash (in the contract)">
              <CopyId value={termsHashOnChain} label="terms hash" />
            </KV>
          )}
        </Step>

        <Step n={2} title="The terms hash is committed inside the on-chain contract">
          <p>
            The <span className="font-mono text-xs">terms_hash</span> is embedded in the asset's
            contract, a small canonical JSON document. The contract is hashed the same way to a{' '}
            <span className="font-mono text-xs">contract_hash = sha256(canonical(contract))</span>,
            and the Sequentia asset id is derived from the issuance output and that contract hash. So
            the asset id itself commits to the exact terms.
          </p>
          {asset ? (
            <>
              <KV k="asset id">
                <CopyId value={asset.id} kind="asset" label="asset id" />
              </KV>
              <KV k="issuance txid">
                <CopyId value={asset.issue_txid} kind="tx" label="issuance txid" />
              </KV>
              <KV k="contract_hash (on chain)">
                <CopyId value={asset.contract_hash} label="contract hash" />
              </KV>
              <KV k="recomputed in your browser">
                {recomputed ? (
                  <span className="inline-flex items-center gap-2">
                    <CopyId value={recomputed} label="recomputed hash" />
                    <Badge color={hashMatch ? 'emerald' : 'amber'}>
                      {hashMatch ? (
                        <>
                          <Icon.check width={12} height={12} /> matches
                        </>
                      ) : (
                        'compare above'
                      )}
                    </Badge>
                  </span>
                ) : (
                  <span className="text-ink-700/50">could not parse the contract</span>
                )}
              </KV>
              <p className="text-xs text-ink-700/60">
                The registry performs the stronger check on chain: it re-derives the asset id from
                the issuance output and the contract hash and requires it to equal the id above, so a
                forged reply cannot decouple the terms from the asset.
              </p>
            </>
          ) : (
            <p className="text-xs text-ink-700/60">
              With an asset id, the live contract hash is fetched from{' '}
              <span className="font-mono">/openamp/v1/assets/&lt;id&gt;</span> and recomputed here.
            </p>
          )}
        </Step>

        <Step n={3} title="The commitment is on the Sequentia chain, following Bitcoin">
          <p>
            The issuance transaction is a real Sequentia transaction. Sequentia is a Bitcoin
            sidechain: every block references a Bitcoin block header and reorganises in real time
            whenever Bitcoin does, so a confirmation is only as final as its Bitcoin anchor is deep.
            View the issuance and the asset on the explorer.
          </p>
          <div className="flex flex-wrap gap-2">
            {asset?.issue_txid && (
              <a href={txUrl(asset.issue_txid)} target="_blank" rel="noopener noreferrer" className="btn-outline">
                <Icon.external width={15} height={15} /> Issuance transaction
              </a>
            )}
            {asset?.id && (
              <a href={assetUrl(asset.id)} target="_blank" rel="noopener noreferrer" className="btn-outline">
                <Icon.external width={15} height={15} /> Asset on the explorer
              </a>
            )}
            {asset?.id && (
              <a href={registryPath(asset.id)} target="_blank" rel="noopener noreferrer" className="btn-outline">
                <Icon.external width={15} height={15} /> Registry entry
              </a>
            )}
          </div>
          {registry && (
            <div className="flex flex-wrap gap-2 pt-1">
              <Badge color={registry.verified_chain ? 'emerald' : 'amber'}>
                {registry.verified_chain ? 'registry: on-chain commitment verified' : 'registry: chain not verified'}
              </Badge>
              {registry.contract?.entity?.domain && (
                <Badge color={registry.verified_domain ? 'emerald' : 'slate'}>
                  domain {registry.contract.entity.domain}
                  {registry.verified_domain ? ' verified' : ''}
                </Badge>
              )}
            </div>
          )}
        </Step>

        <Step n={4} title="The policy key that co-signs every transfer">
          <p>
            A SeqPal-managed asset is transfer-restricted: it can only move when the OpenAMP policy
            key co-signs, which is how eligibility is enforced. The contract commits to that policy
            key, so you can confirm the key checking a transfer is the same key the asset was minted
            under. A restricted asset is one row among equals on Sequentia: it is not privileged, it
            is only permissioned.
          </p>
          {asset?.policy_pub && (
            <KV k="policy key commitment">
              <CopyId value={asset.policy_pub} label="policy pubkey" />
            </KV>
          )}
          {asset?.issuer_pub && (
            <KV k="issuer key">
              <CopyId value={asset.issuer_pub} label="issuer pubkey" />
            </KV>
          )}
          <p className="text-xs text-ink-700/60">
            Every policy decision is recorded in the public, hash-chained transparency log at{' '}
            <a href="/openamp/v1/log" target="_blank" rel="noopener noreferrer" className="font-medium text-seq-600 hover:underline">
              /openamp/v1/log
            </a>
            , which you can walk end to end and re-hash yourself.
          </p>
        </Step>
      </div>

      <div className="mt-4 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] px-5 py-4 text-sm text-ink-700/80">
        <div className="flex items-center gap-2 font-semibold text-ink-900">
          <Icon.shield width={16} height={16} /> What this does not yet cover
        </div>
        <p className="mt-1.5 leading-relaxed">
          The signed terms document and its content-addressed manifest are generated in the next
          milestone; until then this page verifies the on-chain commitment and the hash chain, not
          the human-readable document behind the hash. Everything shown above is real and live on
          the Sequentia testnet.
        </p>
      </div>

      <div className="mt-8">
        <Link to="/" className="text-sm font-medium text-seq-600 hover:underline">
          Back to SeqPal
        </Link>
        <span className="px-2 text-ink-700/30">·</span>
        <a href={EXPLORER} target="_blank" rel="noopener noreferrer" className="text-sm font-medium text-seq-600 hover:underline">
          Sequentia testnet explorer
        </a>
      </div>
    </section>
  )
}

function safeParse(s) {
  try {
    return JSON.parse(s)
  } catch {
    return null
  }
}
