import { useState } from 'react'
import { Icon } from './icons'
import { Badge } from './ui'
import * as api from '../lib/api'
import OfflineSignature from './OfflineSignature'
import { usePoll } from '../lib/poll'
import { useStore } from '../lib/store'
import { canonicalJSON } from '../lib/openamp'
import { LISTING_TAG } from '../lib/statements'

// The venue listing authorization (contract section 7, integration spec Part 2.2).
// An issuer GRANTS that its asset may be listed on a venue (SeqDEX, the Sequentia
// wallet); a venue READS this at the public GET /api/listings to learn which assets
// an issuer authorized. The split is the whole point of the SeqDEX handover: a
// venue can CHECK eligibility (GET /api/eligibility) and read listing authorization,
// but it can never GRANT eligibility, which SeqPal ID alone stamps.
export default function ListingCard({ iss }) {
  const { signWithKey, xonly, hasKey } = useStore()
  const { data, refresh } = usePoll(() => api.listings({ asset: iss.assetId }), { intervalMs: 20000 })
  const current = data?.authorized ? data.listing : null

  const [venues, setVenues] = useState('SeqDEX, Sequentia wallet')
  const [sign, setSign] = useState(false)
  const [busy, setBusy] = useState(null) // 'grant' | 'revoke' | null
  const [err, setErr] = useState(null)
  // A SeqPal ID that is only a wallet signs this elsewhere and pastes it back.
  // The statement is the same either way; only where it gets signed differs.
  const [prep, setPrep] = useState(null)
  const [sig, setSig] = useState('')

  const parseVenues = () =>
    venues
      .split(',')
      .map((v) => v.trim())
      .filter(Boolean)

  const submit = async (authorized) => {
    setErr(null)
    setBusy(authorized ? 'grant' : 'revoke')
    try {
      const vs = parseVenues()
      const body = { authorized, venues: vs }
      if (sign && authorized) {
        // The exact canonical bytes seqpald verifies (listings.go listingStatement),
        // signed TAGGED with the issuer's own key, or as an ordinary message with
        // the tag on the line above -- which is the same commitment written out.
        const statement = canonicalJSON({ role: 'listing', asset: iss.assetId, authorized, venues: vs })
        if (!hasKey) {
          setPrep({ sign_this_message: LISTING_TAG + '\n' + statement })
          return
        }
        const signature = await signWithKey(LISTING_TAG, statement)
        if (!signature) {
          setErr('Your wallet did not return a signature. Grant it unsigned, or try again.')
          return
        }
        body.signature = signature
        body.signer_xonly = xonly
      }
      await api.grantListing(iss.id, body)
      refresh()
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(null)
    }
  }

  const submitSigned = async () => {
    setErr(null)
    setBusy('grant')
    try {
      await api.grantListing(iss.id, {
        authorized: true,
        venues: parseVenues(),
        signature: sig.trim(),
      })
      setSig('')
      setPrep(null)
      refresh()
    } catch (e) {
      setErr(e.message)
    } finally {
      setBusy(null)
    }
  }

  // Listing.venues is a JSON array in the response (seqpald marshals it from a
  // json.RawMessage), so it arrives already parsed; tolerate a string just in case.
  let currentVenues = []
  const rawVenues = current?.venues
  if (Array.isArray(rawVenues)) currentVenues = rawVenues
  else if (typeof rawVenues === 'string' && rawVenues.trim()) {
    try {
      currentVenues = JSON.parse(rawVenues)
    } catch {
      currentVenues = []
    }
  }

  return (
    <div className="card p-6">
      <div className="flex items-center gap-2">
        <Icon.tag width={18} height={18} className="text-seq-600" />
        <h2 className="font-bold text-ink-900">Venue listing authorization</h2>
        <Badge color={current ? 'emerald' : 'slate'} className="ml-auto">
          {current ? 'Authorized' : 'Not authorized'}
        </Badge>
      </div>
      <p className="mt-1.5 text-sm leading-relaxed text-ink-700/70">
        Authorize a venue to list this asset. A venue reads your authorization at the public listings
        endpoint and checks a holder's eligibility separately. It can never grant eligibility: SeqPal
        ID stamps that, and the policy server enforces it on every transfer.
      </p>

      {current && (
        <div className="mt-4 rounded-lg border border-emerald-200 bg-emerald-50 px-3 py-2.5 text-sm">
          <div className="font-medium text-emerald-800">Listed for</div>
          <div className="mt-1 flex flex-wrap gap-1.5">
            {currentVenues.length > 0 ? (
              currentVenues.map((v) => (
                <Badge key={v} color="emerald">
                  {v}
                </Badge>
              ))
            ) : (
              <span className="text-xs text-emerald-700">Any venue that honors SeqPal ID</span>
            )}
          </div>
          {current.signature && (
            <p className="mt-1.5 text-xs text-emerald-700/80">Signed by the issuer key.</p>
          )}
        </div>
      )}

      <div className="mt-4 space-y-3">
        <div>
          <label className="label" htmlFor="lst-venues">
            Venues (comma-separated, optional)
          </label>
          <input
            id="lst-venues"
            className="input"
            placeholder="SeqDEX, Sequentia wallet"
            value={venues}
            onChange={(e) => setVenues(e.target.value)}
          />
          <p className="mt-1 text-xs text-ink-700/60">
            Leave blank to authorize any venue that honors SeqPal ID eligibility.
          </p>
        </div>
        <label className="flex cursor-pointer items-center gap-2 text-sm text-ink-700/80">
          <input
            type="checkbox"
            className="h-4 w-4 accent-seq-600"
            checked={sign}
            onChange={(e) => setSign(e.target.checked)}
          />
          Sign the authorization with my SeqPal ID key
        </label>

        {err && <p className="text-sm font-medium text-rose-600">{err}</p>}
        <OfflineSignature
          prep={prep}
          sig={sig}
          onSig={setSig}
          onSubmit={submitSigned}
          busy={busy === 'grant'}
          label="Grant the signed authorization"
        />

        <div className="flex flex-wrap gap-2">
          <button onClick={() => submit(true)} disabled={!!busy} className="btn-primary disabled:opacity-60">
            {busy === 'grant' ? 'Authorizing…' : current ? 'Update authorization' : 'Authorize listing'}
          </button>
          {current && (
            <button onClick={() => submit(false)} disabled={!!busy} className="btn-outline disabled:opacity-60">
              {busy === 'revoke' ? 'Revoking…' : 'Revoke'}
            </button>
          )}
        </div>
      </div>

      <p className="mt-4 text-[11px] leading-relaxed text-ink-700/55">
        Authorization is read at{' '}
        <span className="font-mono">GET /api/listings?asset={iss.assetId?.slice(0, 8)}…</span>. It
        authorizes listing only. Eligibility is checked at{' '}
        <span className="font-mono">GET /api/eligibility</span> and is never granted by a venue.
      </p>
    </div>
  )
}
