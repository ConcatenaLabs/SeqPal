import { Icon } from './icons'
import { Badge } from './ui'

// A SeqPal ID passport card — works for both individual and corporate profiles.
export default function Passport({ profile, type = 'individual' }) {
  const isCorp = type === 'corporate'
  return (
    <div className="overflow-hidden rounded-2xl border border-ink-800 bg-ink-950 text-white shadow-xl">
      <div className="flex items-center justify-between border-b border-white/10 px-5 py-3.5">
        <div className="flex items-center gap-2 text-sm font-semibold">
          <Icon.id width={18} height={18} className="text-btc" /> SeqPal ID
          <span className="text-white/40">·</span>
          <span className="text-white/60">{isCorp ? 'Corporate (KYB)' : 'Individual'}</span>
        </div>
        <Badge color="emerald">
          <span className="h-1.5 w-1.5 rounded-full bg-emerald-400" /> Verified
        </Badge>
      </div>
      <div className="space-y-4 px-5 py-5">
        <div>
          <div className="text-xs text-white/40">{isCorp ? 'Entity' : 'Name'}</div>
          <div className="text-lg font-bold">{isCorp ? profile.entity : profile.name}</div>
        </div>
        <div className="grid grid-cols-2 gap-4">
          <div>
            <div className="text-xs text-white/40">
              {isCorp ? 'Jurisdiction' : 'Residence'}
            </div>
            <div className="font-semibold">{profile.jurisdiction}</div>
          </div>
          <div>
            <div className="text-xs text-white/40">
              {isCorp ? 'Type' : 'Accreditation'}
            </div>
            <div className="font-semibold">
              {isCorp
                ? 'Corporate'
                : profile.accredited
                  ? profile.accreditationMethod === 'document'
                    ? 'Qualified · documented'
                    : 'Qualified · self-cert.'
                  : 'Retail'}
            </div>
          </div>
        </div>
        <div>
          <div className="text-xs text-white/40">SeqPal ID number</div>
          <div className="break-all font-mono text-sm text-liquid-400">
            {profile.idNumber}
          </div>
        </div>
        {!isCorp && profile.gaid && (
          <div>
            <div className="text-xs text-white/40">Linked wallet (Liquid GAID)</div>
            <div className="font-mono text-xs text-white/70">
              {profile.gaid.slice(0, 8)}…{profile.gaid.slice(-6)}
            </div>
          </div>
        )}
        <div className="flex flex-wrap items-center gap-2 border-t border-white/10 pt-3 text-xs text-white/60">
          <span className="inline-flex items-center gap-1.5">
            <Icon.shield width={14} height={14} className="text-liquid-400" /> Sanctions
            clear
          </span>
          <span className="text-white/20">·</span>
          <span>Sanctions screened every 24h</span>
          {!isCorp && profile.accredited && profile.accreditationBasis && (
            <>
              <span className="text-white/20">·</span>
              <span className="text-white/50">{profile.accreditationBasis}</span>
            </>
          )}
        </div>
      </div>
    </div>
  )
}
