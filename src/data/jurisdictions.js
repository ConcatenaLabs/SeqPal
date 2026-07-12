// SeqPal's templated suggested-minimum private-placement default — the
// Investor Jurisdiction Compliance Matrix (Appendix C of the plan). This is the
// canonical reference behind SeqPal ID's default whitelist and the portal gate.
//
// Tiers:
//   standard   – admitted; local-law screening at investment time
//   restricted – qualified / accredited only, per local regime (upload-to-lift)
//   blocked    – mandatory floor; non-overridable (OFAC / FATF-aligned)

export const TIERS = {
  standard: { label: 'Standard', color: 'emerald' },
  restricted: { label: 'Qualified only', color: 'amber' },
  blocked: { label: 'Blocked', color: 'rose' },
}

export const JURISDICTIONS = [
  { code: 'US', name: 'United States', tier: 'restricted', basis: 'Reg D Rule 501 accredited investor (506(c); Reg S for non-US)', cap: 'No cap on accredited; non-accredited excluded' },
  { code: 'CA', name: 'Canada', tier: 'restricted', basis: 'NI 45-106 accredited investor', cap: 'No cap on accredited; non-accredited excluded' },
  { code: 'EU', name: 'European Union', tier: 'restricted', basis: 'MiFID II professional / qualified investor', cap: 'Up to 149 non-qualified per country' },
  { code: 'GB', name: 'United Kingdom', tier: 'restricted', basis: 'FSMA s.86 sophisticated / high-net-worth (+ Financial Promotion Order)', cap: 'Up to 149 non-qualified' },
  { code: 'CH', name: 'Switzerland', tier: 'restricted', basis: 'FinSA Art. 4 professional / institutional client', cap: 'No retail' },
  { code: 'SG', name: 'Singapore', tier: 'restricted', basis: 'SFA s.275 accredited / institutional investor', cap: 'No non-accredited' },
  { code: 'HK', name: 'Hong Kong (SAR)', tier: 'restricted', basis: 'SFO Schedule 1 professional investor', cap: 'Up to 50 non-professional' },
  { code: 'JP', name: 'Japan', tier: 'restricted', basis: 'FIEA QII / specified investor (tokutei-toushika)', cap: 'Up to 49 non-accredited' },
  { code: 'KR', name: 'South Korea', tier: 'restricted', basis: 'Capital Markets Act qualified professional investor', cap: 'Up to 49 non-accredited' },
  { code: 'AU', name: 'Australia', tier: 'restricted', basis: 'Corporations Act s.708(8) sophisticated / s.708(11) professional', cap: 'Up to 20 non-sophisticated; AUD$2M agg.' },
  { code: 'MO', name: 'Macao (SAR)', tier: 'restricted', basis: 'AMCM qualified investor (Macao private-placement regime)', cap: 'No non-professional' },
  { code: 'TW', name: 'Taiwan', tier: 'restricted', basis: 'FSC professional investor', cap: 'No retail' },
  { code: 'AE', name: 'UAE (ADGM / DIFC)', tier: 'restricted', basis: 'DFSA / FSRA professional client', cap: 'Professional only' },
  { code: 'TR', name: 'Turkey', tier: 'restricted', basis: 'CMB qualified investor', cap: 'No retail' },
  { code: 'IL', name: 'Israel', tier: 'restricted', basis: 'Securities Law First Addendum qualified investor', cap: 'No retail' },
  { code: 'SA', name: 'Saudi Arabia', tier: 'restricted', basis: 'CMA qualified client', cap: 'No retail' },
  { code: 'BR', name: 'Brazil', tier: 'restricted', basis: 'CVM professional / qualified investor (Res. 30/2021)', cap: 'No retail' },
  { code: 'MX', name: 'Mexico', tier: 'restricted', basis: 'CNBV institutional / qualified investor', cap: 'No retail' },
  { code: 'IN', name: 'India', tier: 'restricted', basis: 'Documented RBI / SEBI eligibility', cap: 'Subject to FEMA constraints' },
  { code: 'PK', name: 'Pakistan', tier: 'restricted', basis: 'Documented local-regulator approval', cap: 'Local caps apply' },
  { code: 'BD', name: 'Bangladesh', tier: 'restricted', basis: 'Documented local-regulator approval', cap: 'Local caps apply' },
  { code: 'NG', name: 'Nigeria', tier: 'restricted', basis: 'Documented local-regulator approval', cap: 'Local caps apply' },
  { code: 'EG', name: 'Egypt', tier: 'restricted', basis: 'Documented local-regulator approval', cap: 'Local caps apply' },
  { code: 'VN', name: 'Vietnam', tier: 'restricted', basis: 'Local qualified / professional investor', cap: 'Local caps apply' },
  { code: 'ID', name: 'Indonesia', tier: 'restricted', basis: 'Local qualified / professional investor', cap: 'Local caps apply' },
  { code: 'PH', name: 'Philippines', tier: 'restricted', basis: 'Local qualified / professional investor', cap: 'Local caps apply' },
  { code: 'TH', name: 'Thailand', tier: 'restricted', basis: 'Local qualified / professional investor', cap: 'Local caps apply' },
  { code: 'SV', name: 'El Salvador', tier: 'standard', basis: 'Local accredited / sophisticated (Digital Assets Law)', cap: 'Local-law caps apply' },
  { code: 'PA', name: 'Panama', tier: 'standard', basis: 'SMV inversionista calificado (Decree Law 1/1999, as amended)', cap: 'Local-law caps apply' },
  { code: 'AR', name: 'Argentina', tier: 'standard', basis: 'CNV inversor calificado', cap: 'Local-law caps apply' },
  { code: 'HN', name: 'Honduras', tier: 'standard', basis: 'Local accredited / sophisticated investor', cap: 'Local-law caps apply' },
  { code: 'CN', name: 'Mainland China', tier: 'blocked', basis: 'PRC prohibition on cross-border retail securities', cap: 'n/a' },
  { code: 'RU', name: 'Russia', tier: 'blocked', basis: 'OFAC / EU sanctions', cap: 'n/a' },
  { code: 'KP', name: 'North Korea (DPRK)', tier: 'blocked', basis: 'OFAC / FATF comprehensive sanctions', cap: 'n/a' },
  { code: 'IR', name: 'Iran', tier: 'blocked', basis: 'OFAC / FATF comprehensive sanctions', cap: 'n/a' },
  { code: 'SY', name: 'Syria', tier: 'blocked', basis: 'OFAC / FATF comprehensive sanctions', cap: 'n/a' },
  { code: 'CU', name: 'Cuba', tier: 'blocked', basis: 'OFAC comprehensive sanctions', cap: 'n/a' },
  { code: 'BY', name: 'Belarus', tier: 'blocked', basis: 'OFAC / FATF-aligned restrictions', cap: 'n/a' },
  { code: 'VE', name: 'Venezuela', tier: 'blocked', basis: 'OFAC / FATF-aligned restrictions', cap: 'n/a' },
  { code: 'MM', name: 'Myanmar', tier: 'blocked', basis: 'OFAC / FATF-aligned restrictions', cap: 'n/a' },
  { code: 'AF', name: 'Afghanistan', tier: 'blocked', basis: 'OFAC / FATF-aligned restrictions', cap: 'n/a' },
  { code: 'YE', name: 'Yemen', tier: 'blocked', basis: 'OFAC / FATF-aligned restrictions', cap: 'n/a' },
  { code: 'LY', name: 'Libya', tier: 'blocked', basis: 'OFAC / FATF-aligned restrictions', cap: 'n/a' },
  { code: 'SS', name: 'South Sudan', tier: 'blocked', basis: 'OFAC / FATF-aligned restrictions', cap: 'n/a' },
  { code: 'SO', name: 'Somalia', tier: 'blocked', basis: 'OFAC / FATF-aligned restrictions', cap: 'n/a' },
  { code: 'SD', name: 'Sudan', tier: 'blocked', basis: 'OFAC / FATF-aligned restrictions', cap: 'n/a' },
]

// The compiled policy is catch-all EXCLUDED by default: only a jurisdiction the
// issuer explicitly admits contributes eligibility categories, so a resident of
// an unlisted jurisdiction matches no category and is refused by the policy
// server. Admitting a jurisdiction not in the list is done by adding it to the
// matrix, never by a permissive default.
export const CATCH_ALL_ROW = {
  name: 'All other jurisdictions',
  tier: 'excluded',
  basis: 'Excluded by default until the issuer admits the jurisdiction explicitly',
  cap: 'n/a',
}

// The eligibility categories a SeqPal ID can carry within a jurisdiction. These
// are the elig half of the j:<ISO2>:<elig> category tokens the policy server
// gates on. hnw and soph are UK statutory investor statements.
export const ELIG_CATEGORIES = [
  { key: 'ret', label: 'Retail', hint: 'Non-qualified individuals' },
  { key: 'acc', label: 'Accredited', hint: 'Documented accredited / qualified investor' },
  { key: 'pro', label: 'Professional', hint: 'Professional / institutional client' },
  { key: 'hnw', label: 'High-net-worth', hint: 'UK FSMA high-net-worth statement', gbOnly: true },
  { key: 'soph', label: 'Sophisticated', hint: 'UK FSMA sophisticated-investor statement', gbOnly: true },
]

// What each access level admits, mirroring the seqpald compiler. The catch-all
// (a jurisdiction not admitted) admits nothing.
export const ACCESS_ADMITS = {
  standard: ['ret', 'acc', 'pro'],
  restricted: ['acc', 'pro'],
  excluded: [],
}

// EU member states, for the per-member-state offeree caps (the sub-150
// prospectus-exemption count). Compiled into holder_caps_by_category as
// j:<state>:ret. The EU-wide matrix row governs baseline access; a cap here
// bounds retail offerees in that specific member state.
export const EU_MEMBER_STATES = [
  { code: 'DE', name: 'Germany' },
  { code: 'FR', name: 'France' },
  { code: 'IT', name: 'Italy' },
  { code: 'ES', name: 'Spain' },
  { code: 'NL', name: 'Netherlands' },
  { code: 'IE', name: 'Ireland' },
  { code: 'BE', name: 'Belgium' },
  { code: 'AT', name: 'Austria' },
  { code: 'PT', name: 'Portugal' },
  { code: 'FI', name: 'Finland' },
  { code: 'LU', name: 'Luxembourg' },
  { code: 'SE', name: 'Sweden' },
  { code: 'DK', name: 'Denmark' },
  { code: 'PL', name: 'Poland' },
]

// Jurisdiction options offered at SeqPal ID registration, with the qualified-
// investor basis SeqPal applies for that residence.
export const RESIDENCE_OPTIONS = [
  { code: 'US', name: 'United States', accreditationLabel: 'Accredited investor — Reg D Rule 501' },
  { code: 'GB', name: 'United Kingdom', accreditationLabel: 'Sophisticated / high-net-worth — FSMA s.86' },
  { code: 'EU', name: 'European Union', accreditationLabel: 'Professional / qualified investor — MiFID II' },
  { code: 'CH', name: 'Switzerland', accreditationLabel: 'Professional client — FinSA Art. 4' },
  { code: 'SG', name: 'Singapore', accreditationLabel: 'Accredited investor — SFA s.275' },
  { code: 'AE', name: 'UAE (ADGM / DIFC)', accreditationLabel: 'Professional client — DFSA / FSRA' },
  { code: 'BR', name: 'Brazil', accreditationLabel: 'Professional / qualified investor — CVM' },
  { code: 'SV', name: 'El Salvador', accreditationLabel: 'Accredited / sophisticated — Digital Assets Law' },
  { code: 'AR', name: 'Argentina', accreditationLabel: 'Inversor calificado — CNV' },
]
