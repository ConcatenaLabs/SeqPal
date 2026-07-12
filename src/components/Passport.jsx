import { Icon } from './icons'
import { Badge } from './ui'

// A SeqPal ID passport card. Pass `account` for the person's own SeqPal ID (the
// server record from GET /me) or `entity` for a linked corporate (KYB) record.
export default function Passport({ account, entity }) {
  const isCorp = !!entity
  const rec = entity || account
  const p = rec?.profile && typeof rec.profile === 'object' ? rec.profile : {}
  const accreditation = p.accredited
    ? p.accreditation_method === 'document'
      ? 'Qualified, documented'
      : 'Qualified, self-certified'
    : 'Retail'

  return (
    <div className="overflow-hidden rounded-2xl border border-ink-800 bg-ink-950 text-white shadow-xl">
      <div className="flex items-center justify-between border-b border-white/10 px-5 py-3.5">
        <div className="flex items-center gap-2 text-sm font-semibold">
          <Icon.id width={18} height={18} className="text-btc" /> SeqPal ID
          <span className="text-white/40">·</span>
          <span className="text-white/60">{isCorp ? 'Entity (KYB)' : 'Individual'}</span>
        </div>
        <Badge color="emerald">
          <span className="h-1.5 w-1.5 rounded-full bg-emerald-400" /> Verified
        </Badge>
      </div>
      <div className="space-y-4 px-5 py-5">
        <div>
          <div className="text-xs text-white/40">{isCorp ? 'Entity' : 'Name'}</div>
          <div className="text-lg font-bold">
            {isCorp ? rec.name : rec.display_name || 'Unnamed'}
          </div>
        </div>
        <div className="grid grid-cols-2 gap-4">
          <div>
            <div className="text-xs text-white/40">{isCorp ? 'Jurisdiction' : 'Residence'}</div>
            <div className="font-semibold">
              {isCorp ? rec.jurisdiction || 'Not set' : p.residence || 'Not set'}
            </div>
          </div>
          <div>
            <div className="text-xs text-white/40">{isCorp ? 'Type' : 'Qualified status'}</div>
            <div className="font-semibold">{isCorp ? 'Corporate' : accreditation}</div>
          </div>
        </div>
        <div>
          <div className="text-xs text-white/40">
            {isCorp ? 'Entity record id' : 'Sequentia enclave account (AID)'}
          </div>
          <div className="break-all font-mono text-sm text-seq-400">
            {isCorp ? rec.id : rec.aid}
          </div>
        </div>
        {!isCorp && (
          <div>
            <div className="text-xs text-white/40">Enclave key (x-only)</div>
            <div className="break-all font-mono text-xs text-white/70">{rec.xonly}</div>
          </div>
        )}
        <div className="flex flex-wrap items-center gap-2 border-t border-white/10 pt-3 text-xs text-white/60">
          <span className="inline-flex items-center gap-1.5">
            <Icon.shield width={14} height={14} className="text-seq-400" /> Screening
            simulated in this build
          </span>
          {!isCorp && p.accredited && p.accreditation_basis && (
            <>
              <span className="text-white/20">·</span>
              <span className="text-white/50">{p.accreditation_basis}</span>
            </>
          )}
        </div>
      </div>
    </div>
  )
}
