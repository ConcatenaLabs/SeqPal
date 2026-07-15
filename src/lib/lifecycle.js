// The issuance lifecycle, as seqpald actually records it.
//
// seqpald stores exactly two statuses: `draft` until the asset is minted, and
// `live` once the OpenAMP mint has returned an asset id and a txid. The former
// "Incorporating" and "Deploying" states were advanced by a button in the
// browser and tracked nothing, so they are gone: the steps below that SeqPal
// does not yet observe are labelled as such rather than shown as progress.

export const STATUS = {
  draft: {
    label: 'Draft',
    color: 'amber',
    blurb: 'Configured on SeqPal, not yet deployed on Sequentia',
  },
  live: {
    label: 'Deployed',
    color: 'emerald',
    blurb: 'The restricted asset is minted on Sequentia',
  },
}

// Preparation steps that happen off the platform. SeqPal does not observe them,
// so they carry no state: they are a checklist, not progress.
export const OFF_PLATFORM_STEPS = [
  {
    key: 'incorporation',
    label: 'Próspera LLC incorporated',
    detail: 'Registered on the Próspera e-registry, typically 1 to 3 business days',
  },
  {
    key: 'filing',
    label: 'Documents executed and RFSA filing',
    detail: 'Exempt notice (private placement) or RFSA registration (public offering)',
  },
]

// Depository Receipts additionally require a contracted brokerage-custody
// relationship before the programme can operate.
export function offPlatformSteps(structureId) {
  if (structureId === 'depository-receipt') {
    return [
      OFF_PLATFORM_STEPS[0],
      {
        key: 'custody',
        label: 'Brokerage custody contracted',
        detail: 'Segregated custody sub-account at the partner broker',
      },
      OFF_PLATFORM_STEPS[1],
    ]
  }
  return OFF_PLATFORM_STEPS
}
