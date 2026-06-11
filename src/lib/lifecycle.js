// Post-checkout lifecycle for an issuance. Próspera incorporation is not
// instant — entity registration on the e-registry takes 1–3 business days —
// so an issuance moves through several states before it is live.

export const STATUS = {
  awaiting_incorporation: {
    label: 'Incorporating',
    color: 'amber',
    blurb: 'Próspera LLC registration in progress',
  },
  deploying: {
    label: 'Deploying',
    color: 'liquid',
    blurb: 'Filing with the RFSA and deploying on Liquid',
  },
  live: {
    label: 'Live',
    color: 'emerald',
    blurb: 'Deployed and serviced by the Transfer Agent',
  },
}

// The ordered milestones shown on the issuance timeline.
export const MILESTONES = [
  { key: 'payment', label: 'Setup fee paid', detail: 'Checkout complete' },
  {
    key: 'incorporation',
    label: 'Próspera LLC incorporated',
    detail: 'Registered on the Próspera e-registry',
  },
  {
    key: 'filing',
    label: 'Documents executed & RFSA filing',
    detail: 'Exempt notice (private placement) or RFSA registration (public offering)',
  },
  {
    key: 'deployment',
    label: 'AMP asset deployed',
    detail: 'Transfer-Restricted asset minted; Transfer Agent active',
  },
  { key: 'live', label: 'Live', detail: 'Ready for the placement portal' },
]

// Depository Receipts additionally require a contracted brokerage-custody
// relationship (the plan's single largest DR operational risk), so their
// lifecycle carries an extra milestone.
export function milestonesFor(structureId) {
  if (structureId === 'depository-receipt') {
    return [
      MILESTONES[0],
      MILESTONES[1],
      {
        key: 'custody',
        label: 'Brokerage custody contracted',
        detail: 'Segregated custody sub-account at the partner broker',
      },
      MILESTONES[2],
      MILESTONES[3],
      { key: 'live', label: 'Live', detail: 'Ready to mint & redeem' },
    ]
  }
  return MILESTONES
}

// How many milestones are complete for a given status.
export function completedCount(status, structureId) {
  if (status === 'awaiting_incorporation') return 1
  if (status === 'deploying') return 2
  if (status === 'live') return milestonesFor(structureId).length
  return 0
}

// The next status in the chain (used by the demo "advance" control).
export function nextStatus(status) {
  if (status === 'awaiting_incorporation') return 'deploying'
  if (status === 'deploying') return 'live'
  return 'live'
}
