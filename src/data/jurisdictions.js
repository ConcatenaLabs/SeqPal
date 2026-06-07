// SeqPal ID's templated suggested-minimum restriction set for a private placement.
// Issuers may tighten any restriction; mandatory floors (sanctions screening,
// identity verification, OFAC/FATF-aligned blocks) can never be loosened.
//
// Tiers:
//   open       – admitted (subject to identity verification & sanctions screening)
//   restricted – qualified / accredited investors only, per local regime
//   blocked    – mandatory floor; cannot be admitted

export const TIERS = {
  open: { label: 'Open', color: 'emerald' },
  restricted: { label: 'Qualified only', color: 'amber' },
  blocked: { label: 'Blocked', color: 'rose' },
}

export const JURISDICTIONS = [
  { code: 'US', name: 'United States', tier: 'restricted', basis: 'Reg D Rule 506(c) — verified accredited investor' },
  { code: 'CA', name: 'Canada', tier: 'restricted', basis: 'Accredited investor exemption' },
  { code: 'GB', name: 'United Kingdom', tier: 'restricted', basis: 'FSMA s.86 — self-certified high-net-worth / sophisticated' },
  { code: 'EU', name: 'European Union', tier: 'restricted', basis: 'MiFID II professional / qualified investor (+ per-country tail)' },
  { code: 'CH', name: 'Switzerland', tier: 'restricted', basis: 'FinSA professional client' },
  { code: 'SG', name: 'Singapore', tier: 'restricted', basis: 'SFA s.275 accredited investor' },
  { code: 'HK', name: 'Hong Kong', tier: 'restricted', basis: 'Professional investor' },
  { code: 'JP', name: 'Japan', tier: 'restricted', basis: 'Tokutei-toushika (specified investor)' },
  { code: 'AU', name: 'Australia', tier: 'restricted', basis: 'Corporations Act s.708 sophisticated investor' },
  { code: 'AE', name: 'United Arab Emirates', tier: 'open', basis: null },
  { code: 'SV', name: 'El Salvador', tier: 'open', basis: null },
  { code: 'AR', name: 'Argentina', tier: 'open', basis: null },
  { code: 'HN', name: 'Honduras', tier: 'open', basis: null },
  { code: 'BR', name: 'Brazil', tier: 'open', basis: null },
  { code: 'ZA', name: 'South Africa', tier: 'open', basis: null },
  { code: 'KP', name: 'North Korea', tier: 'blocked', basis: 'OFAC / FATF' },
  { code: 'IR', name: 'Iran', tier: 'blocked', basis: 'OFAC / FATF' },
  { code: 'SY', name: 'Syria', tier: 'blocked', basis: 'OFAC / FATF' },
  { code: 'CU', name: 'Cuba', tier: 'blocked', basis: 'OFAC / FATF' },
]

// Jurisdiction options offered at SeqPal ID registration, with the qualified-
// investor basis SeqPal applies for that residence.
export const RESIDENCE_OPTIONS = [
  { code: 'US', name: 'United States', accreditationLabel: 'Accredited investor — Reg D Rule 501' },
  { code: 'GB', name: 'United Kingdom', accreditationLabel: 'High-net-worth / sophisticated — FSMA s.86' },
  { code: 'EU', name: 'European Union', accreditationLabel: 'Professional / qualified investor — MiFID II' },
  { code: 'CH', name: 'Switzerland', accreditationLabel: 'Professional client — FinSA' },
  { code: 'SG', name: 'Singapore', accreditationLabel: 'Accredited investor — SFA' },
  { code: 'AE', name: 'United Arab Emirates', accreditationLabel: 'Self-certified qualified investor' },
  { code: 'SV', name: 'El Salvador', accreditationLabel: 'Self-certified qualified investor' },
  { code: 'BR', name: 'Brazil', accreditationLabel: 'Self-certified qualified investor' },
]
