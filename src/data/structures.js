// The four issuance structures SeqPal supports at launch.
// Sourced from the SeqPal product (Issuance Structures + Six-Step Onboarding).

export const STRUCTURES = [
  {
    id: 'native-equity',
    name: 'Native Equity Tokens',
    short: 'Native Equity',
    icon: 'equity',
    accent: 'btc',
    claim:
      'Direct ownership in the issuer LLC — voting rights, dividends, and a pro-rata claim on residual assets.',
    description:
      'Your Próspera LLC is the operating company itself. Tokens represent direct equity: the blockchain is the official Registry of Members, and SeqPal acts as Transfer Agent and Secretary. Dividends are paid pro-rata in stablecoins or BTC.',
    example:
      'A Bitcoin-native startup raising a seed round from a global investor base that wants its cap table on-chain from day one.',
    useCases: [
      'Startups raising seed / Series A from a global investor base',
      'Companies preserving the option of future public secondary trading',
      'Family-office vehicles admitting other family offices as LPs',
      'Bitcoin-treasury holding companies using BTC as their unit of account',
    ],
    setup: { from: 12500, label: '$12,500', simple: 7500 },
    annual: 5000,
    timeToDeploy: '3 business days',
    complexity: 'Low',
    seqpalRole: 'Transfer Agent & Secretary',
    offering: 'Private placement or public offering',
  },
  {
    id: 'equity-spv',
    name: 'Equity SPV Tokens',
    short: 'Equity SPV',
    icon: 'spv',
    accent: 'liquid',
    claim:
      'Beneficial interest in a single concentrated position the SPV holds in a foreign operating company.',
    description:
      'Your Próspera LLC is a Special Purpose Vehicle holding one concentrated position — typically a pre-IPO company. SeqPal is appointed Administrative Manager and Asset Custodian, holding the certificates in a segregated account and providing Proof of Position to all token holders.',
    example:
      'A family office tokenizing fractions of a pre-IPO unicorn position to syndicate to other accredited investors.',
    useCases: [
      'Family offices syndicating secondary positions in pre-IPO companies',
      'Founder secondaries selling a portion of vested shares',
      'GPs offering LPs early liquidity against a concentrated position',
      'Crypto-native investors seeking exposure to private-company equity',
    ],
    setup: { from: 17500, label: '$17,500' },
    annual: 8000,
    timeToDeploy: '5 business days',
    complexity: 'Medium',
    seqpalRole: 'Administrative Manager & Asset Custodian',
    offering: 'Private placement or public offering',
  },
  {
    id: 'debt-yield',
    name: 'Debt / Yield Tokens',
    short: 'Debt / Yield',
    icon: 'debt',
    accent: 'btc',
    claim:
      'A senior or subordinated debt claim against the LLC’s loan portfolio.',
    description:
      'Your Próspera LLC operates as a private-credit or specialty-finance vehicle issuing tokenized notes. SeqPal is appointed Calculation and Paying Agent — monitoring loan performance and automating periodic coupon distributions — and Collateral Agent where the loan is secured.',
    example:
      'A trade-finance operator funding a loan portfolio with tokenized senior notes and automated coupon distributions in BTC or stablecoins.',
    useCases: [
      'Trade-finance and invoice-finance operators',
      'Direct lenders to SMEs in emerging markets',
      'Real-estate bridge lenders raising senior debt against a deal',
      'Bitcoin-treasury operators raising BTC-denominated debt',
    ],
    setup: { from: 20000, label: '$20,000', securedAddOn: '+$5K–$15K' },
    annual: 10000,
    timeToDeploy: '5–7 business days',
    complexity: 'Medium-high',
    seqpalRole: 'Calculation, Paying & Collateral Agent',
    offering: 'Private placement or public offering',
  },
  {
    id: 'depository-receipt',
    name: 'Depository Receipt Tokens',
    short: 'Depository Receipt',
    icon: 'dr',
    accent: 'liquid',
    claim:
      'A 1:1 claim on a specific traditional financial instrument custodied on behalf of the LLC.',
    description:
      'SeqPal acts as a bare-trust holding vehicle, custodying traditional instruments (public stocks, ETFs, T-bills, commodities) in a segregated sub-account. Each token holder has a 1:1, redeemable claim on the underlying asset.',
    example:
      'Tokenized public equities held on Bitcoin rails for self-custody and 24/7 liquidity, settled in BTC.',
    useCases: [
      'Tokenized public equities (e.g. TSLA, NVDA, AAPL, SPY) on-chain',
      'Tokenized Treasuries and money-market funds for retail access',
      'Tokenized commodities with daily proof of reserves',
      'Tokenized exotic instruments — closed-end funds, REITs, structured notes',
    ],
    setup: { from: 22500, label: '$22,500', note: 'public offering only' },
    annual: 12000,
    timeToDeploy: '7–10 business days',
    complexity: 'High',
    seqpalRole: 'Registered Agent with Power of Attorney (custody)',
    offering: 'Always a public offering',
    requiresCustody: true,
  },
]

export const getStructure = (id) => STRUCTURES.find((s) => s.id === id)
