// Canonical investor-eligibility rule for a SeqPal-issued asset.
// This is the single source of truth shared by the investor portal gate and the
// "offerings you can access" list — keep them from drifting apart.
//
// A holder is eligible iff their residence jurisdiction is:
//   - 'standard'   → admitted (local-law screening is the issuer's responsibility), or
//   - 'restricted' → admitted only if the holder is a qualified/accredited investor.
// 'excluded', 'blocked', or an unknown jurisdiction → not eligible.

export function tierFor(policy, residenceCode) {
  return policy ? policy[residenceCode] : undefined
}

export function isEligible(policy, residenceCode, accredited) {
  const tier = tierFor(policy, residenceCode)
  return tier === 'standard' || (tier === 'restricted' && !!accredited)
}
