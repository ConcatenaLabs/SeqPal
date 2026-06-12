// Issuance ownership. An issuance belongs to the signed-in account when its
// principal's SeqPal ID number matches the individual ID or any linked
// corporate (KYB) ID. Issuances persist in the demo store across sign-outs, so
// a different persona registering in the same browser must not see or manage
// another principal's issuances.
export function ownsIssuance(account, iss) {
  const pid = iss?.principal?.idNumber
  if (!pid || !account?.individual) return false
  if (pid === account.individual.idNumber) return true
  return (account.corporates || []).some((c) => c.idNumber === pid)
}

export function ownedIssuances(account, issuances) {
  return (issuances || []).filter((i) => ownsIssuance(account, i))
}
