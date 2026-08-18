import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { Icon, StructureIcon } from '../components/icons'
import { Badge, DemoNote } from '../components/ui'
import SignInGate from '../components/SignInGate'
import ServicingPanel from '../components/ServicingPanel'
import CopyId from '../components/CopyId'
import { ChainChip, ChainDetail, ReorgBanner } from '../components/ChainStatus'
import TransparencyLog from '../components/TransparencyLog'
import NetworkFees from '../components/NetworkFees'
import RegistryBadge from '../components/RegistryBadge'
import HolderRegister from '../components/HolderRegister'
import DataRoom from '../components/DataRoom'
import RfsaFilingCard from '../components/RfsaFilingCard'
import PlatformFeeCard from '../components/PlatformFeeCard'
import PayoutMandateCard from '../components/PayoutMandateCard'
import ClosingCard from '../components/ClosingCard'
import DistributionConsole from '../components/DistributionConsole'
import FreezeClawbackConsole from '../components/FreezeClawbackConsole'
import SupervisionConsole from '../components/SupervisionConsole'
import PolicyConsole from '../components/PolicyConsole'
import CorporateActionsCard from '../components/CorporateActionsCard'
import AmendmentChainCard from '../components/AmendmentChainCard'
import DRConsole from '../components/DRConsole'
import ListingCard from '../components/ListingCard'
import { useStore } from '../lib/store'
import { view } from '../lib/issuance'
import { downloadPortalBundle } from '../lib/portalBundle'
import { getStructure } from '../data/structures'
import { JURISDICTIONS } from '../data/jurisdictions'
import { STATUS, offPlatformSteps } from '../lib/lifecycle'
import { termsHash } from '../lib/openamp'

// What the server's refusal means, in one line. The server's own message is
// always shown verbatim above this: this only adds context it cannot know.
const DEPLOY_HINT = {
  400: 'The mint parameters were refused. Fix them here and try again: nothing was minted.',
  402: 'The SeqPal setup fee is unpaid. Pay it in the Setup fee card, then deploy.',
  403: 'This issuance belongs to another SeqPal ID.',
  404: 'This issuance no longer exists on the server.',
  409: 'Pick a different ticker. Tickers are checked against the assets already live on the policy server.',
  429: 'The deploy rate limit is per account and per platform, over a rolling hour. Wait and try again.',
  501: 'This deployment cannot run network-enforced rules. Pick a supported enforcement model and deploy again; nothing was minted.',
  502: 'The policy server refused or could not be reached. Nothing was minted.',
  503: 'The platform has no issuer token configured, so no deployment can be made from here right now.',
}

function DeployCard({ iss, onDeployed }) {
  const { deployIssuance, xonly } = useStore()
  const [form, setForm] = useState({
    supply: iss.supply > 0 ? String(iss.supply) : '1000000',
    precision: iss.precision >= 1 && iss.precision <= 8 ? iss.precision : 8,
    clawback: iss.clawback !== false,
  })
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)

  const steps = offPlatformSteps(iss.structureId)

  const deploy = async () => {
    setErr(null)
    setBusy(true)
    try {
      // The terms object is what the on-chain contract commits to. seqpald
      // recomputes the canonical hash server-side; the value sent here is a
      // cross-check, and a mismatch refuses the deploy rather than minting
      // something the issuer never saw.
      const terms = iss.terms && typeof iss.terms === 'object' ? iss.terms : {}
      const res = await deployIssuance({
        issuance_id: iss.id,
        supply: Number(form.supply.replace(/[^0-9]/g, '')) || 0,
        precision: Number(form.precision),
        // The enforcement election was made in the wizard and lives in the
        // committed terms; the deploy restates it.
        enforcement: iss.enforcement || 'serviced',
        clawback: form.clawback,
        // The entity's own SeqPal ID key becomes the enclave issuer half, so a
        // clawback needs the issuer's browser signature (two-phase) and the platform
        // never holds an issuer key for this asset. seqpald cross-checks this against
        // the deploying account and refuses a mismatch.
        ...(xonly ? { issuer_pubkey: xonly } : {}),
        // fee_convert_atoms is intentionally not sent: seqpald derives it from
        // the price server (value-preserving conversion), and a nonzero value
        // here would be treated as an explicit issuer override.
        terms,
        terms_hash: await termsHash(terms),
      })
      onDeployed?.(res)
    } catch (e) {
      setErr({ message: e.message, status: e.status })
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="card p-6">
      <div className="flex items-center justify-between">
        <h2 className="font-bold text-ink-900">Deploy on Sequentia</h2>
        <Badge color={STATUS.draft.color}>{STATUS.draft.label}</Badge>
      </div>

      <div className="mt-4 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] px-4 py-3">
        <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">
          Before you deploy, off the platform
        </div>
        <ul className="mt-2 space-y-1.5">
          {steps.map((m) => (
            <li key={m.key} className="flex items-start justify-between gap-3 text-sm">
              <span className="min-w-0">
                <span className="font-medium text-ink-900">{m.label}</span>
                <span className="block text-xs text-ink-700/70">{m.detail}</span>
              </span>
              <Badge color="amber" className="mt-0.5 shrink-0">
                Simulated
              </Badge>
            </li>
          ))}
        </ul>
        <p className="mt-2 text-xs text-ink-700/60">
          Incorporation and the RFSA filing produce watermarked simulated artifacts in this build:
          no Próspera e-registry sandbox exists, so the certificate and entity number are labeled
          simulations, though their hashes are anchored. These steps happen off the platform, and
          SeqPal does not track them. The deploy below is real.
        </p>
      </div>

      <div className="mt-5 grid gap-4 sm:grid-cols-2">
        <div>
          <label className="label" htmlFor="dep-supply">
            Initial supply (whole tokens)
          </label>
          <input
            id="dep-supply"
            className="input"
            inputMode="numeric"
            value={form.supply}
            onChange={(e) => setForm({ ...form, supply: e.target.value })}
          />
        </div>
        <div>
          <label className="label" htmlFor="dep-precision">
            Precision (decimals)
          </label>
          <select
            id="dep-precision"
            className="select"
            value={form.precision}
            onChange={(e) => setForm({ ...form, precision: Number(e.target.value) })}
          >
            {[1, 2, 3, 4, 5, 6, 7, 8].map((p) => (
              <option key={p} value={p}>
                {p}
              </option>
            ))}
          </select>
        </div>
      </div>

      <div className="mt-4 space-y-2">
        <label className="flex cursor-pointer items-start gap-3 rounded-xl border border-ink-900/10 px-4 py-3">
          <input
            type="checkbox"
            className="mt-1 h-4 w-4 accent-btc"
            checked={form.clawback}
            onChange={(e) => setForm({ ...form, clawback: e.target.checked })}
          />
          <span className="text-sm">
            <span className="font-medium text-ink-900">Clawback enabled</span>
            <span className="block text-xs leading-relaxed text-ink-700/70">
              The asset can be recovered from a holder by the issuer of record. The clawback
              key is your own SeqPal ID key, so a clawback needs your signature and the platform
              cannot move a holder's position on its own.
            </span>
          </span>
        </label>
      </div>

      {err && (
        <div className="mt-4 rounded-xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm">
          <p className="font-medium text-rose-700">{err.message}</p>
          {DEPLOY_HINT[err.status] && (
            <p className="mt-1 text-xs text-rose-700/80">{DEPLOY_HINT[err.status]}</p>
          )}
        </div>
      )}

      {iss.enforcement === 'bearer' && (
        <p className="mt-4 rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-xs leading-relaxed text-amber-800">
          This is a freely-tradable issuance. Deploy it from the issuance wizard&rsquo;s
          checkout step, which collects the two things it requires: the exported emergency
          recovery key and the signed attestation about US exposure.
        </p>
      )}
      <button
        onClick={deploy}
        disabled={busy || iss.enforcement === 'bearer'}
        className="btn-primary mt-5 w-full disabled:opacity-60"
      >
        {busy ? (
          <>
            <span className="h-4 w-4 animate-spin rounded-full border-2 border-white/30 border-t-white" />
            Minting on Sequentia…
          </>
        ) : (
          <>
            Deploy on Sequentia
            <Icon.arrowRight width={15} height={15} />
          </>
        )}
      </button>
      <p className="mt-2 text-center text-xs leading-relaxed text-ink-700/60">
        This mints a real restricted asset on the Sequentia testnet. A repeat of the
        same terms returns the same asset instead of minting a second one.
      </p>
    </div>
  )
}

function AssetCard({ iss, watch }) {
  const live = iss.live
  return (
    <div className="card p-6">
      <div className="flex items-center justify-between gap-3">
        <h2 className="font-bold text-ink-900">Asset</h2>
        {live && <ChainChip watch={watch} />}
      </div>
      {live ? (
        <div className="mt-3 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] px-4 py-3">
          <ChainDetail watch={watch} />
          {!watch && (
            <p className="text-[11px] leading-relaxed text-ink-700/60">
              The identifiers below are real and on chain. Waiting for the first chain-status event:
              a state is only as final as its Bitcoin anchor is deep, so nothing here is final at 0
              confirmations.
            </p>
          )}
        </div>
      ) : (
        <div className="mt-3 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] px-4 py-3 text-xs text-ink-700/70">
          Nothing is minted yet, so there is no asset id and no txid. They appear here, from
          the policy server, the moment the deploy succeeds.
        </div>
      )}
      <dl className="mt-4 divide-y divide-ink-900/10 text-sm">
        {[
          ['Issuer of record', `${iss.entityName || iss.name} LLC · Próspera`],
          ['Network', 'Sequentia (a Bitcoin sidechain)'],
          [
            'Issuance layer',
            iss.enforcement === 'bearer'
              ? 'Freely tradable · court-order freezes only'
              : iss.enforcement === 'network'
                ? 'Network-enforced rules · verified holders'
                : 'Policy server · transfer-restricted enclave',
          ],
          ['Asset id', <CopyId key="a" value={iss.assetId} kind="asset" label="asset id" />],
          ['Issuance txid', <CopyId key="t" value={iss.txid} kind="tx" label="issuance txid" />],
          ['Contract hash', <CopyId key="c" value={iss.contractHash} label="contract hash" />],
          ['Holder account (AID)', <CopyId key="h" value={iss.holderAid} label="holder AID" />],
          ...(iss.enforcement === 'network'
            ? [
                [
                  'Holding address',
                  <CopyId key="hc" value={iss.holderAddress} label="holding address" />,
                ],
                [
                  'Rules address',
                  <CopyId key="vc" value={iss.rulesAddress} label="rules address" />,
                ],
                [
                  'Policy commitment',
                  <CopyId key="pc" value={iss.policyCommitment} label="policy commitment" />,
                ],
              ]
            : [
                [
                  'Enclave address',
                  <CopyId key="e" value={iss.enclaveAddress} label="enclave address" />,
                ],
              ]),
          ...(live && iss.assetId
            ? [['Asset registry', <RegistryBadge key="r" assetId={iss.assetId} />]]
            : []),
          [
            'Initial supply',
            iss.supply ? `${iss.supply.toLocaleString()} ${iss.ticker}` : 'not set',
          ],
          ['Precision', iss.precision || 'not set'],
          ['Target raise', iss.raise || 'not set'],
          ['Offering type', iss.isPublic ? 'Public offering' : 'Private placement'],
        ].map(([k, v]) => (
          <div key={k} className="flex items-center justify-between gap-4 py-3">
            <dt className="shrink-0 text-ink-700/70">{k}</dt>
            <dd className="min-w-0 text-right font-medium text-ink-900">{v}</dd>
          </div>
        ))}
      </dl>
      {iss.enforcement === 'network' && (
        <p className="mt-4 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] px-4 py-3 text-xs leading-relaxed text-ink-700/85">
          Holders hold this token at addresses the network itself polices. Transfers do not need
          SeqPal to be online. The rules address is where your published policy lives on chain:
          every transfer of this token has to spend it, which is what makes the rules bind. Changing
          the policy, or halting the token, is done from that address with your issuer key, and a
          change takes effect when the updated list is published rather than the moment you make it.
          One limit worth knowing: a holder can combine at most two of their coins of this token in
          a single transfer, and one transfer moves one holder's coins only, so two holders cannot pay from the same transfer, so a holder with more makes more than one transfer.
        </p>
      )}
      {live && iss.assetId && (
        <Link
          to={`/docs/verify?asset=${encodeURIComponent(iss.assetId)}`}
          className="mt-4 inline-flex items-center gap-1.5 text-sm font-medium text-seq-600 hover:underline"
        >
          <Icon.shield width={15} height={15} /> Verify independently
        </Link>
      )}
    </div>
  )
}

function PortalCard({ iss, portal }) {
  const isRaise = iss.structureId !== 'depository-receipt'
  const published = portal?.published

  return (
    <div className="card p-6">
      <div className="flex items-center gap-2">
        <Icon.globe width={18} height={18} className="text-seq-600" />
        <h2 className="font-bold text-ink-900">Placement Portal</h2>
        {published && (
          <Badge color="amber" className="ml-auto">
            Preview
          </Badge>
        )}
      </div>
      <p className="mt-1.5 text-sm leading-relaxed text-ink-700/70">
        {isRaise
          ? 'A branded fundraising portal on your own domain, where an investor clears the SeqPal ID gate, signs the subscription agreement, and funds the subscription.'
          : 'Depository Receipts are minted and redeemed directly rather than raised through a subscription portal.'}
      </p>

      {!isRaise ? (
        <div className="mt-4 rounded-lg bg-ink-900/[0.03] px-4 py-3 text-sm text-ink-700/80">
          Mint and redemption are handled from the asset's transfer-agent controls.
        </div>
      ) : !iss.live ? (
        <div className="mt-4 rounded-lg bg-ink-900/[0.03] px-4 py-3 text-sm text-ink-700/70">
          Available once the asset is deployed.
        </div>
      ) : published ? (
        <div className="mt-4 space-y-3">
          <div className="flex items-center justify-between rounded-lg bg-ink-900/[0.03] px-4 py-2.5 text-sm">
            <span className="text-ink-700">Preview at</span>
            <span className="font-mono text-xs text-seq-600">invest.{portal.slug}.com</span>
          </div>
          <div className="flex gap-2">
            <Link to={`/portal/${iss.id}`} className="btn-primary flex-1">
              <Icon.external width={15} height={15} /> View investor portal
            </Link>
            <Link to={`/issuance/${iss.id}/portal`} className="btn-outline">
              Edit
            </Link>
          </div>
          <button
            onClick={() => downloadPortalBundle(iss, portal)}
            className="btn-outline w-full"
          >
            <Icon.upload width={15} height={15} className="rotate-180" /> Download portal bundle to self-host
          </button>
        </div>
      ) : (
        <Link to={`/issuance/${iss.id}/portal`} className="btn-primary mt-4 w-full">
          <Icon.spark width={16} height={16} /> Set up your placement portal
        </Link>
      )}
      {isRaise && (
        <p className="mt-3 text-[11px] leading-relaxed text-ink-700/55">
          Subscriptions, escrow, and closing are real on the testnet: USDX and native BTC move on
          chain, and card and bank rails are simulated and labeled. Manage the setup fee, payout
          mandate, and closing below.
        </p>
      )}
    </div>
  )
}

export default function IssuanceDetail() {
  const { id } = useParams()
  const { loading, isSignedIn, issuances, simFor, watchFor } = useStore()

  if (loading) {
    return (
      <section className="container-x flex justify-center py-24">
        <span className="h-8 w-8 animate-spin rounded-full border-4 border-btc/20 border-t-btc" />
      </section>
    )
  }
  if (!isSignedIn) return <SignInGate />

  // GET /me returns only the session AID's issuances, so an issuance owned by
  // another SeqPal ID is simply not here.
  const found = issuances.find((i) => i.id === id)
  if (!found) {
    return (
      <section className="container-x py-24 text-center">
        <p className="text-ink-700">
          Issuance not found. It does not exist, or it belongs to another SeqPal ID.
        </p>
        <Link to="/dashboard" className="btn-outline mt-6">
          Back to dashboard
        </Link>
      </section>
    )
  }

  const iss = view(found)
  const s = getStructure(iss.structureId)
  const Ic = StructureIcon[s?.icon] || Icon.layers
  const sim = simFor(iss.id)
  const watch = watchFor(iss.id)
  const isRaise = iss.structureId !== 'depository-receipt'
  const bearerAsset = iss.enforcement === 'bearer'
  const networkAsset = iss.enforcement === 'network'

  const openJ = JURISDICTIONS.filter((j) => iss.policy?.[j.code] === 'standard')
  const restrictedJ = JURISDICTIONS.filter((j) => iss.policy?.[j.code] === 'restricted')

  return (
    <section className="container-x py-12">
      <Link
        to="/dashboard"
        className="inline-flex items-center gap-1.5 text-sm font-medium text-ink-700 hover:text-ink-900"
      >
        <Icon.arrowLeft width={15} height={15} /> Dashboard
      </Link>

      <div className="mt-5 flex flex-wrap items-start justify-between gap-4">
        <div className="flex items-center gap-4">
          <div
            className={`flex h-14 w-14 items-center justify-center rounded-2xl ${
              s?.accent === 'seq' ? 'bg-seq/10 text-seq-600' : 'bg-btc-50 text-btc-600'
            }`}
          >
            <Ic width={28} height={28} />
          </div>
          <div>
            <div className="flex items-center gap-3">
              <h1 className="text-2xl font-extrabold tracking-tight text-ink-900">{iss.name}</h1>
              <span className="font-mono text-sm text-ink-700/60">{iss.ticker}</span>
            </div>
            <div className="mt-1 text-sm text-ink-700/80">
              {s?.name || 'Structure not set'}
              {iss.principal?.name ? ` · ${iss.principal.name}` : ''}
            </div>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Badge color={(STATUS[iss.status] || STATUS.draft).color}>
            {iss.live && <span className="h-1.5 w-1.5 rounded-full bg-emerald-500" />}
            {(STATUS[iss.status] || STATUS.draft).label}
          </Badge>
          {iss.live && <ChainChip watch={watch} />}
        </div>
      </div>

      {iss.live && <ReorgBanner watch={watch} className="mt-6" />}

      <div className="mt-8 grid gap-6 lg:grid-cols-3">
        <div className="space-y-6">
          {!iss.live && iss.isPublic && <RfsaFilingCard iss={iss} />}
          <PlatformFeeCard iss={iss} />
          {!iss.live && <DeployCard iss={iss} />}
          <PortalCard iss={iss} portal={sim.portal} />
        </div>

        <div className="space-y-6 lg:col-span-2">
          <AssetCard iss={iss} watch={watch} />

          <DataRoom iss={iss} />

          {iss.live && (
            <>
              <HolderRegister iss={iss} />
              <AmendmentChainCard iss={iss} />
              {!isRaise && <DRConsole iss={iss} />}
              <DistributionConsole iss={iss} />
              {bearerAsset ? (
                <>
                  <SupervisionConsole iss={iss} />
                  <CorporateActionsCard iss={iss} />
                </>
              ) : networkAsset ? (
                <PolicyConsole iss={iss} />
              ) : (
                <FreezeClawbackConsole iss={iss} />
              )}
              <NetworkFees iss={iss} />
              <TransparencyLog iss={iss} />
              <ServicingPanel iss={iss} />

              <ListingCard iss={iss} />

              {isRaise && (
                <>
                  <PayoutMandateCard iss={iss} />
                  <ClosingCard iss={iss} />
                </>
              )}

              {bearerAsset ? (
                <div className="card p-6">
                  <h2 className="font-bold text-ink-900">Trading rules</h2>
                  <p className="mt-2 text-sm leading-relaxed text-ink-700/80">
                    None. This token is freely tradable: anyone in the world can hold and
                    trade it, and who holds what is public. The one intervention is the
                    court-ordered freeze above. Buyers in the initial sale were
                    identity-checked, and holders verify their identity to collect
                    dividends or vote.
                  </p>
                </div>
              ) : (
              <div className="card overflow-hidden">
                <div className="border-b border-ink-900/10 px-5 py-3.5">
                  <h2 className="font-bold text-ink-900">Compliance policy</h2>
                </div>
                <div className="space-y-4 px-5 py-4 text-sm">
                  <div>
                    <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">
                      Standard jurisdictions
                    </div>
                    <div className="mt-2 flex flex-wrap gap-1.5">
                      {openJ.length ? (
                        openJ.map((j) => (
                          <Badge key={j.code} color="emerald">
                            {j.code}
                          </Badge>
                        ))
                      ) : (
                        <span className="text-ink-700/60">None</span>
                      )}
                    </div>
                  </div>
                  <div>
                    <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">
                      Qualified / accredited only
                    </div>
                    <div className="mt-2 flex flex-wrap gap-1.5">
                      {restrictedJ.length ? (
                        restrictedJ.map((j) => (
                          <Badge key={j.code} color="amber">
                            {j.code}
                          </Badge>
                        ))
                      ) : (
                        <span className="text-ink-700/60">None</span>
                      )}
                    </div>
                  </div>
                  <DemoNote>
                    This policy is committed to the asset's contract through its terms hash,
                    and the terms hash is real. It is not yet compiled into rules the policy
                    server enforces on a transfer, so no eligibility check runs on this asset
                    yet.
                  </DemoNote>
                </div>
              </div>
              )}
            </>
          )}
        </div>
      </div>
    </section>
  )
}
