// Turning what a wallet shows a holder into the account key SeqPal can challenge.
//
// A wallet displays its OpenAMP account id far more prominently than the key the
// id is derived from, so both are accepted wherever an account is named. The id
// is resolved through the policy server and then CHECKED: it is re-derived from
// the key that came back and must match what was pasted, so a wrong or
// substituted key is caught here rather than at a signature that mysteriously
// fails to verify.
import * as oamp from './openamp'
import { computeAID, isAid, isXonly } from './statements'

// Resolve an account id or an account key to { xonly, aid }. Throws with
// something a person can act on.
export async function resolveAccountKey(pasted) {
  const value = String(pasted || '').trim().toLowerCase()
  if (isXonly(value)) return { xonly: value, aid: computeAID([value]) }
  if (!isAid(value)) {
    throw new Error('That is neither an account id (40 hex) nor an account key (64 hex).')
  }
  let user
  try {
    user = await oamp.getUser(value)
  } catch (e) {
    if (e?.status === 404) {
      throw new Error(
        'No account with that id is registered with the policy server yet. Receive a restricted ' +
          'asset in that wallet first, or paste its account key instead.'
      )
    }
    throw e
  }
  const xonly = (user?.pubkeys || [])[0]
  if (!isXonly(xonly)) {
    throw new Error('The policy server has no account key registered for that account id.')
  }
  if (computeAID([xonly]) !== value) {
    throw new Error(
      'That account id does not match the key the policy server returned for it. Nothing was linked.'
    )
  }
  return { xonly, aid: value }
}
