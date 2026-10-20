import { useEffect, useState } from 'react'
import { Icon } from './icons'
import { Badge } from './ui'
import { txUrl } from '../lib/chain'
import { fmtAssetAmount } from '../lib/format'
import * as api from '../lib/api'
import { usePoll } from '../lib/poll'
import { useStore } from '../lib/store'
import OfflineSignature from './OfflineSignature'
import { fmtRail, subStateChip, settlementModel } from '../lib/money'
import * as wallet from '../lib/wallet'

// Whether this browser has a wallet that can hold an escrow's release key and
// sign with it (its contract engine). Asked once per mount, never assumed.
function useContractWallet() {
  const [ok, setOk] = useState(false)
  useEffect(() => {
    let live = true
    wallet.waitForProvider().then((p) => (p ? wallet.hasContractEngine() : false)).then((v) => live && setOk(v))
    return () => {
      live = false
    }
  }, [])
  return ok
}

// A short investor AID for the book.
const shortAid = (a) => (a ? `${a.slice(0, 6)}…${a.slice(-4)}` : '-')

// A settlement txid line: real on-chain delivery/release resolves on the
// explorer; a "reconciled" delivery (a prior attempt already delivered, detected
// by a balance scan) and a SIMULATED fiat reference are labeled, not linked.
function TxLine({ label, txid }) {
  if (!txid) return null
  const onchain = /^[0-9a-fA-F]{64}$/.test(txid)
  return (
    <div className="flex items-center justify-between gap-3 text-xs">
      <span className="text-ink-700/60">{label}</span>
      {onchain ? (
        <a
          href={txUrl(txid)}
          target="_blank"
          rel="noopener noreferrer"
          className="font-mono text-seq-600 hover:underline"
        >
          {txid.slice(0, 12)}…
        </a>
      ) : (
        <span className="font-mono text-ink-700/70">{txid}</span>
      )}
    </div>
  )
}


const usdx = (atoms) => fmtRail(atoms, 'USDX')

// Read the signatures a wallet returned, in the forms wallets produce them:
// signopenamptransfer's whole answer { signatures: [{ vin, signature }] }, the
// list alone, or { "<vin>": signature }.
function parseSignatures(text) {
  const v = JSON.parse(text)
  if (v && Array.isArray(v.signatures)) return v.signatures
  if (Array.isArray(v)) return v
  if (v && typeof v === 'object') return v
  throw new Error('Paste the JSON your wallet returned.')
}

const PAYS = {
  issuer: 'To you',
  platform: 'Platform escrow fee',
  subscriber: 'Back to the subscriber',
  network_fee: 'Network fee',
}

const SPEND_KIND = {
  release: { title: 'Release', color: 'emerald' },
  cancel: { title: 'Cancel', color: 'amber' },
  refund: { title: 'Refund after the deadline', color: 'slate' },
}

// One transaction that spends escrow coins. A release or a cancel is signed by
// the issuer's own wallet: this page shows the transaction and the command that
// signs it, and takes back what the wallet returns. It never signs anything
// itself, and it never hands a wallet a bare digest: the wallet is given the
// transaction and recomputes every sighash from it.
function SpendPanel({ iss, spend, onDone }) {
  const [text, setText] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)
  const [copied, setCopied] = useState(false)
  const kind = SPEND_KIND[spend.kind] || { title: spend.kind, color: 'slate' }
  const outs = spend.detail?.outputs || []
  const toSign = spend.state === 'built' && spend.sign_command
  const canEngine = useContractWallet() && toSign && spend.engine

  // The browser wallet's contract engine rebuilds this transaction from the
  // escrow's template and each coin's instance, shows it under its five-point
  // approval and signs every escrow input with its contract key. The signed
  // transaction comes back here unbroadcast; seqpald checks it is this one and
  // verifies every signature before it broadcasts.
  const signInWallet = async () => {
    setErr(null)
    setBusy(true)
    try {
      const hex = await wallet.signContractSpend(spend.engine)
      await api.signEscrowSpendTx(iss.id, spend.id, hex)
      onDone()
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }

  const submit = async () => {
    setErr(null)
    setBusy(true)
    try {
      await api.signEscrowSpend(iss.id, spend.id, parseSignatures(text.trim()))
      setText('')
      onDone()
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(spend.sign_command)
      setCopied(true)
      setTimeout(() => setCopied(false), 1400)
    } catch {
      // Clipboard denied: the command is still shown below.
    }
  }

  return (
    <div className="rounded-xl border border-ink-900/10 bg-white p-4">
      <div className="flex items-center gap-2">
        <span className="text-sm font-semibold text-ink-900">{kind.title}</span>
        <Badge color={kind.color}>{spend.state}</Badge>
        {spend.txid && <span className="ml-auto"><TxLine label="" txid={spend.state === 'broadcast' ? spend.txid : ''} /></span>}
      </div>
      <div className="mt-2 divide-y divide-ink-900/5 rounded-lg border border-ink-900/10">
        {outs.map((o) => (
          <div key={o.n} className="flex items-center justify-between gap-3 px-3 py-1.5 text-xs">
            <span className="text-ink-700/80">
              {PAYS[o.pays] || o.pays}
              {o.address ? <span className="ml-1 font-mono text-ink-700/55">{o.address.slice(0, 14)}…</span> : null}
            </span>
            <span className="font-mono text-ink-900">{usdx(o.atoms)}</span>
          </div>
        ))}
      </div>
      {spend.kind === 'release' && spend.detail?.minimum_atoms != null && (
        <p className="mt-1.5 text-[11px] text-ink-700/60">
          The escrow lets this pay you only because it pays at least your minimum of{' '}
          {usdx(spend.detail.minimum_atoms)}. The network fee is paid in the payment asset itself,
          out of the money in escrow.
        </p>
      )}
      {spend.error && <p className="mt-1.5 text-xs text-rose-600">{spend.error}</p>}
      {canEngine && (
        <div className="mt-3">
          <p className="text-xs leading-relaxed text-ink-700/75">
            Your browser wallet holds a contract key. If it is this escrow&rsquo;s release key, the wallet
            rebuilds this transaction itself, shows you what it spends and pays, and signs it.
          </p>
          <button onClick={signInWallet} disabled={busy} className="btn-primary mt-2 disabled:opacity-50" data-sign-in-wallet={spend.id}>
            {busy ? 'Waiting for the wallet' : 'Sign in this browser\u2019s wallet'}
          </button>
        </div>
      )}
      {toSign && (
        <div className="mt-3">
          <p className="text-xs leading-relaxed text-ink-700/75">
            Sign it in the Sequentia wallet that holds your release key. Your wallet checks the
            transaction before it signs: it recomputes every sighash itself, checks that each escrow
            coin really is the one being spent, and signs with the release key only.
          </p>
          <div className="mt-2 flex items-center justify-between">
            <span className="text-[11px] font-semibold uppercase tracking-wide text-ink-700/60">Command</span>
            <button type="button" onClick={copy} className="text-xs font-medium text-seq-600 hover:underline">
              {copied ? 'Copied' : 'Copy'}
            </button>
          </div>
          <pre className="mt-1 max-h-32 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-ink-900/[0.04] p-2 font-mono text-[10px] text-ink-800">
            {spend.sign_command}
          </pre>
          <textarea
            className="input mt-2 font-mono text-xs"
            rows={3}
            spellCheck={false}
            placeholder='{"signatures":[{"vin":0,"signature":"..."}]}'
            value={text}
            onChange={(e) => setText(e.target.value)}
          />
          <button onClick={submit} disabled={busy || !text.trim()} className="btn-primary mt-2 disabled:opacity-50">
            {busy ? 'Checking' : 'Send the signatures'}
          </button>
          {err && <p className="mt-1.5 text-xs font-medium text-rose-600">{err}</p>}
        </div>
      )}
    </div>
  )
}

// Opening the escrow fixes, once and for good, what every subscription address
// of this offering is derived from: the release key, the minimum and the close.
// The release key is a key in the issuer's OWN Sequentia wallet, which signs the
// release and any cancel; SeqPal never holds it. The issuer proves it holds the
// key by signing the terms with it.
function OpenEscrowForm({ iss, onOpened }) {
  const [key, setKey] = useState('')
  const [minimum, setMinimum] = useState('')
  const [close, setClose] = useState('')
  const [prep, setPrep] = useState(null)
  const [sig, setSig] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)
  const hasWallet = useContractWallet()

  const body = () => ({
    release_key: key.trim().toLowerCase(),
    minimum: Number(String(minimum).replace(/[^0-9.]/g, '')) || 0,
    close_height: Number(String(close).replace(/[^0-9]/g, '')) || 0,
  })
  const run = async (fn) => {
    setErr(null)
    setBusy(true)
    try {
      await fn()
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }
  const prepare = () =>
    run(async () => {
      setPrep(await api.openEscrow(iss.id, body()))
    })
  const submit = () =>
    run(async () => {
      await api.openEscrow(iss.id, { ...body(), signature: sig.trim() })
      setPrep(null)
      setSig('')
      onOpened()
    })
  // The browser wallet's contract key as the release key, and the terms signed
  // with it as a tagged statement.
  const useWalletKey = () =>
    run(async () => {
      setKey(await wallet.contractKey())
    })
  const signWithWallet = () =>
    run(async () => {
      const signature = await wallet.signContractStatement(prep.tag, prep.sign_this)
      await api.openEscrow(iss.id, { ...body(), signature })
      setPrep(null)
      onOpened()
    })

  return (
    <div className="mt-4 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] p-4">
      <div className="text-sm font-semibold text-ink-900">Open the escrow</div>
      <p className="mt-1 text-xs leading-relaxed text-ink-700/70">
        Subscribers on USDX pay into the escrow, and they cannot subscribe until it is open. Name a
        key from your own Sequentia wallet, for example the public key of a new legacy address
        (sequentia-cli getnewaddress &quot;escrow&quot; legacy, then getaddressinfo), without its
        first byte. Payments go to your Sequentia payout mandate, which must be registered first.
        These terms cannot change once the escrow is open.
      </p>
      <div className="mt-3 grid gap-3 sm:grid-cols-3">
        <div className="sm:col-span-3">
          <label className="label" htmlFor="esc-key">Release key (x-only, 64 hex)</label>
          <input id="esc-key" className="input font-mono text-xs" value={key} onChange={(e) => setKey(e.target.value)} />
          {hasWallet && (
            <button type="button" onClick={useWalletKey} disabled={busy} className="btn-outline mt-2 text-xs disabled:opacity-50" data-use-wallet-key>
              Use this browser wallet&rsquo;s contract key
            </button>
          )}
        </div>
        <div>
          <label className="label" htmlFor="esc-min">Minimum raise (USDX)</label>
          <input id="esc-min" className="input" inputMode="decimal" value={minimum} onChange={(e) => setMinimum(e.target.value)} />
        </div>
        <div className="sm:col-span-2">
          <label className="label" htmlFor="esc-close">Close (Sequentia block height)</label>
          <input id="esc-close" className="input" inputMode="numeric" value={close} onChange={(e) => setClose(e.target.value)} />
        </div>
      </div>
      {!prep && (
        <button onClick={prepare} disabled={busy || !key.trim() || !minimum || !close} className="btn-primary mt-3 disabled:opacity-50">
          {busy ? 'Checking' : 'Show the terms to sign'}
        </button>
      )}
      {prep?.escrow && (
        <div className="mt-3 space-y-1 text-xs text-ink-700/80">
          <div>Release from {prep.escrow.close_text}, paying you at least {usdx(prep.escrow.minimum_atoms)}.</div>
          <div>From {prep.escrow.deadline_text}, anyone can return an undelivered subscription to its subscriber.</div>
          <div>A refund keeps back at most {usdx(prep.escrow.fee_cap)} for its network fee.</div>
        </div>
      )}
      {prep?.sign_this && hasWallet && (
        <button onClick={signWithWallet} disabled={busy} className="btn-primary mt-3 disabled:opacity-50" data-sign-terms-in-wallet>
          {busy ? 'Waiting for the wallet' : 'Sign the terms in this browser\u2019s wallet'}
        </button>
      )}
      {prep?.sign_this_message && (
        <div className="mt-3">
          <p className="text-xs leading-relaxed text-ink-700/75">
            Sign these exact characters as an ordinary message from the legacy address of the release
            key (sequentia-cli signmessage), and paste the signature. A wallet that signs SeqPal&rsquo;s
            tagged statements may sign the canonical terms under the tag {prep.tag} instead.
          </p>
          <pre className="mt-2 max-h-40 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-white/70 p-3 font-mono text-[11px] text-ink-900">
            {prep.sign_this_message}
          </pre>
          <textarea className="input mt-2 font-mono text-xs" rows={2} spellCheck={false} placeholder="signature"
            value={sig} onChange={(e) => setSig(e.target.value)} />
          <button onClick={submit} disabled={busy || !sig.trim()} className="btn-primary mt-2 disabled:opacity-50">
            {busy ? 'Checking' : 'Open the escrow'}
          </button>
        </div>
      )}
      {err && <p className="mt-2 text-xs font-medium text-rose-600">{err}</p>}
    </div>
  )
}

// The offering's escrow: its terms, the money at its addresses, and every
// transaction built over it.
function EscrowPanel({ iss, extraSpends }) {
  const { data, refresh } = usePoll(() => api.escrowStatus(iss.id), { intervalMs: 8000, deps: [iss.id] })
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)
  const t = data?.escrow
  if (data && !t) return <OpenEscrowForm iss={iss} onOpened={refresh} />
  if (!t) return null
  const byId = Object.fromEntries((data?.spends || []).map((sp) => [sp.id, sp]))
  for (const sp of extraSpends || []) if (!byId[sp.id]) byId[sp.id] = sp
  const spends = Object.values(byId).filter((sp) => sp.state !== 'superseded')
  const cancelAll = async () => {
    setErr(null)
    setBusy(true)
    try {
      await api.cancelEscrow(iss.id, {})
      refresh()
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="mt-4 rounded-xl border border-ink-900/10 bg-ink-900/[0.02] p-4">
      <div className="flex items-center gap-2 text-sm font-semibold text-ink-900">
        <Icon.lock width={15} height={15} className="text-seq-600" /> The escrow
        <span className="ml-auto font-mono text-xs font-normal text-ink-700/70">{usdx(data?.held_atoms || 0)} held</span>
      </div>
      <ul className="mt-2 space-y-1 text-xs leading-relaxed text-ink-700/75">
        <li>Released from {t.close_text}, only in one release paying you at least {usdx(t.minimum_atoms)}, signed by your release key.</li>
        <li>Returned at any time by a cancel you sign; from {t.deadline_text}, returned by anyone, with no signature.</li>
        <li>No SeqPal wallet holds a key to this money. Each subscription is paid to its own escrow address.</li>
      </ul>
      {spends.length > 0 && (
        <div className="mt-3 space-y-3">
          {spends.map((sp) => (
            <SpendPanel key={sp.id} iss={iss} spend={sp} onDone={refresh} />
          ))}
        </div>
      )}
      <button onClick={cancelAll} disabled={busy} className="btn-outline mt-3 text-xs disabled:opacity-50">
        {busy ? 'Building' : 'Return every undelivered subscription'}
      </button>
      {err && <p className="mt-2 text-xs font-medium text-rose-600">{err}</p>}
    </div>
  )
}

export default function ClosingCard({ iss }) {
  const { signCloseStmt, xonly, hasKey } = useStore()
  const { data: book, refresh: refreshBook } = usePoll(() => api.issuanceSubscriptions(iss.id), {
    intervalMs: 6000,
    deps: [iss.id],
  })
  const { data: settle, refresh: refreshSettle } = usePoll(() => api.settlements(iss.id), {
    intervalMs: 6000,
    deps: [iss.id],
  })
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(null)
  const [results, setResults] = useState(null)
  const [spends, setSpends] = useState([])
  const [prep, setPrep] = useState(null)
  const [sig, setSig] = useState('')

  const subs = book?.subscriptions || []
  const ledger = book?.ledger || []
  const settlements = settle?.settlements || []
  const settById = Object.fromEntries(settlements.map((s) => [s.subscription.id, s.settlement]))

  const inEscrow = subs.filter((s) => s.state === 'in_escrow')
  const settled = subs.filter((s) => s.state === 'settled')
  const refunded = subs.filter((s) => s.state === 'refunded')

  const close = async () => {
    setErr(null)
    setBusy(true)
    try {
      const got = await api.close(iss.id, {})
      if (!got.sign_this) {
        refreshBook()
        return
      }
      if (!hasKey) {
        setPrep(got)
        return
      }
      const signature = await signCloseStmt(got.sign_this)
      if (!signature) {
        setErr('Your wallet did not return a signature, so nothing was closed.')
        return
      }
      const res = await api.close(iss.id, { signature, signer_xonly: xonly })
      setResults(res.results || [])
      setSpends(res.escrow_spends || [])
      refreshBook()
      refreshSettle()
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }

  const closeSigned = async () => {
    setErr(null)
    setBusy(true)
    try {
      const res = await api.close(iss.id, { signature: sig.trim() })
      setResults(res.results || [])
      setSpends(res.escrow_spends || [])
      setSig('')
      setPrep(null)
      refreshBook()
      refreshSettle()
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="card p-6">
      <div className="flex items-center gap-2">
        <Icon.coins width={18} height={18} className="text-btc-600" />
        <h2 className="font-bold text-ink-900">Subscriptions and closing</h2>
        <Badge color="emerald" className="ml-auto">
          Real money
        </Badge>
      </div>
      <p className="mt-1.5 text-xs leading-relaxed text-ink-700/70">
        Closing delivers each subscriber&rsquo;s tokens from the offering&rsquo;s escrow enclave,
        which SeqPal and the policy server sign together, and then pays you. A USDX subscription is
        held at an escrow address no SeqPal wallet holds a key to: once the tokens are delivered,
        one release pays you the raise, and you sign it in your own wallet. Until you sign, the
        money stays in escrow, and from the deadline any subscriber can take it back, so sign
        promptly. A subscription whose tokens cannot be delivered is returned by a cancel you sign.
        A native BTC subscription is held in a SeqPal wallet and settles cross-chain, not
        atomically: the payment confirmed on Bitcoin testnet4 and delivery is co-signed on
        Sequentia. Nothing is final at 0 confirmations.
      </p>
      <EscrowPanel iss={iss} extraSpends={spends} />

      <div className="mt-4 grid grid-cols-3 gap-3">
        {[
          ['In escrow', inEscrow.length],
          ['Settled', settled.length],
          ['Refunded', refunded.length],
        ].map(([k, v]) => (
          <div key={k} className="rounded-lg bg-ink-900/[0.03] px-3 py-2.5">
            <div className="text-xs text-ink-700/60">{k}</div>
            <div className="mt-0.5 font-bold text-ink-900">{v}</div>
          </div>
        ))}
      </div>

      {subs.length === 0 ? (
        <div className="mt-4 rounded-lg bg-ink-900/[0.03] px-4 py-3 text-sm text-ink-700/70">
          No subscriptions yet. Investors subscribe from the offering portal.
        </div>
      ) : (
        <div className="mt-4 space-y-2">
          {subs.map((s) => {
            const chip = subStateChip(s.state)
            const st = settById[s.id]
            const model = settlementModel(s.rail)
            // A settlement whose delivery and release were one transaction carries
            // one combined txid, so it is rendered once, not as two lines.
            const atomic = st && st.delivery_txid && st.delivery_txid === st.release_txid
            return (
              <div key={s.id} className="rounded-lg border border-ink-900/10 px-3 py-2.5">
                <div className="flex items-center justify-between gap-3 text-sm">
                  <div className="flex items-center gap-2">
                    <span className="font-mono text-xs text-ink-700/80">{shortAid(s.investor_aid)}</span>
                    <Badge color={s.rail === 'btc' ? 'btc' : s.rail === 'usdx' ? 'seq' : 'amber'}>
                      {s.rail}
                      {s.funds_simulated ? ' · SIM' : ''}
                    </Badge>
                  </div>
                  <Badge color={chip.color}>{chip.label}</Badge>
                </div>
                <div className="mt-1.5 flex items-center justify-between text-xs text-ink-700/70">
                  <span>{fmtAssetAmount(s.token_atoms, iss.precision, iss.ticker)}</span>
                  <span>{fmtRail(s.deposited_atoms || s.pay_amount, s.pay_ccy)} escrowed</span>
                </div>
                {model.label && (
                  <div className="mt-1 flex items-center gap-1.5 text-[11px] text-ink-700/55">
                    <Icon.exchange width={11} height={11} className="shrink-0" />
                    {model.label}
                  </div>
                )}
                {st?.state === 'awaiting_release' && (
                  <div className="mt-1 text-[11px] text-emerald-700">
                    Tokens delivered. The money is released when you sign the release.
                  </div>
                )}
                {st?.state === 'awaiting_cancel' && (
                  <div className="mt-1 text-[11px] text-amber-700">
                    Not delivered. The money is returned when you sign the cancel, or by anyone from
                    the deadline.
                  </div>
                )}
                {st && (st.delivery_txid || st.release_txid || st.refund_txid) && (
                  <div className="mt-2 space-y-1 border-t border-ink-900/5 pt-2">
                    {atomic ? (
                      <TxLine label="Atomic delivery versus payment" txid={st.delivery_txid} />
                    ) : (
                      <>
                        <TxLine label="Delivery" txid={st.delivery_txid} />
                        <TxLine label="Release" txid={st.release_txid} />
                      </>
                    )}
                    <TxLine label="Refund" txid={st.refund_txid} />
                    {(st.state === 'failed' || st.state === 'delivery_failed') && st.error && (
                      <p className="text-xs text-rose-600">{st.error}</p>
                    )}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}

      {results && (
        <div className="mt-4 rounded-lg border border-ink-900/10 bg-ink-900/[0.02] p-3">
          <div className="text-xs font-semibold uppercase tracking-wide text-ink-700/60">
            Close results
          </div>
          <ul className="mt-1.5 space-y-1 text-xs text-ink-700/80">
            {results.map((r) => (
              <li key={r.subscription_id} className="flex items-center justify-between gap-2">
                <span className="font-mono">{shortAid(r.investor_aid)}</span>
                <span>
                  {r.state}
                  {r.message ? ` · ${r.message}` : ''}
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}

      {err && (
        <div className="mt-4 rounded-lg border border-rose-200 bg-rose-50 px-3 py-2 text-sm font-medium text-rose-700">
          {err}
        </div>
      )}

      <button
        onClick={close}
        disabled={busy || inEscrow.length === 0}
        className="btn-primary mt-4 w-full disabled:opacity-60"
      >
        <Icon.check width={16} height={16} />
        {busy ? 'Closing…' : `Sign closing authorization${inEscrow.length ? ` (${inEscrow.length} to settle)` : ''}`}
      </button>
      <OfflineSignature
        prep={prep}
        sig={sig}
        onSig={setSig}
        onSubmit={closeSigned}
        busy={busy}
        label="Authorize closing"
      />
      {ledger.length > 0 && (
        <details className="mt-4">
          <summary className="cursor-pointer text-xs font-semibold uppercase tracking-wide text-ink-700/60">
            Escrow ledger ({ledger.length})
          </summary>
          <div className="mt-2 divide-y divide-ink-900/5 rounded-lg border border-ink-900/10">
            {ledger.map((e) => (
              <div key={e.id} className="flex items-center justify-between gap-2 px-3 py-2 text-xs">
                <span className="capitalize text-ink-800">{e.kind.replace('_', ' ')}</span>
                <span className="flex items-center gap-2">
                  <span className="font-mono text-ink-700/80">{fmtRail(e.amount, e.ccy)}</span>
                  {e.funds_simulated && <Badge color="amber">SIM</Badge>}
                </span>
              </div>
            ))}
          </div>
        </details>
      )}
    </div>
  )
}
