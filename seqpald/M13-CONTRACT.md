# M13: the controls a network-enforced token actually has

Matching test: `m13_test.go`. Front-end: `src/components/PolicyConsole.jsx`.

M12 made a network-enforced deploy real. It left three gaps, and this milestone
closes them: the token could be issued but never policed, every downstream flow
was written for a different mechanism and would have misbehaved silently, and the
one limitation an issuer must know before choosing the model was nowhere in the
copy.

## 1. The holder-list and frozen-coin console

For a serviced token a freeze is a signature this platform withholds. For a
freely-tradable one it is a record on chain. For a network-enforced one it is
neither: the rules live in a **published list** the network reads on every
transfer, so the two controls an issuer keeps are

- changing who may hold the token, and
- stopping one specific coin,

and both take effect when the updated list is published **and the on-chain rules
output has moved onto it**, not the moment the issuer presses a button.

### API

| Route | What |
|---|---|
| `GET /api/issuances/{id}/policy` | The published holder list (with each holder's height bounds), the frozen-coin count, the list version and commitment, plus this platform's change history |
| `POST /api/issuances/{id}/policy/freeze` | Remove holders and/or publish coins as frozen |
| `POST /api/issuances/{id}/policy/unfreeze` | Restore holders and/or release coins |
| `POST /api/issuances/{id}/policy/{opID}/complete` | The issuer's signature plus the registrar's two values |

Owner session only, and only for a **live** network-enforced issuance; anything
else answers 409 naming the election.

`reason` and `order_hash` are both required, exactly as on the freely-tradable
console. The order document is hashed **in the browser**; only the fingerprint
reaches this server and only the fingerprint is published.

### Why a change takes two attempts

The on-chain rules program is compiled per policy, outside this platform and
outside the policy server. So `complete` without the registrar's two values
answers **409 carrying the document to compile against and the commands to run**.
That is a step in the flow, not a failure, and the SPA renders it as one.

### Custody and idempotency

seqpald holds no key. The build returns the 32-byte message the issuer signs in
their own browser with their own key, and only a signed change is published.

`damp_policy_ops` is the idempotency anchor, mirroring `supervision_ops`: the row
is created before the issuer signs, keyed by (issuance, kind, order hash,
targets), so a replayed build resumes the same change rather than opening a second
sequence number at the policy server. A replayed completion is answered from the
row, and the policy server's own completion is idempotent behind it.

## 2. What every other flow does, and why

Each of these branches **explicitly**. A flow written against a platform-held
account and a platform co-signature does not fail cleanly for a network-enforced
token, it fails obscurely, so a refusal here always carries a reason an issuer
understands.

| Flow | Decision | Why |
|---|---|---|
| Subscriptions (`subscribe.go`) | **Refused, 409** | A subscription promises a delivery, and delivery goes to the investor's own holding address, which only the issuer's own wallet can sign. Refused before an escrow address exists, so no money is taken for a delivery this platform cannot make. |
| Closing (`closing.go`) | **Refused, 409** | Closing IS the delivery. Refused at the entry point, which covers the whole settlement state machine below it: `settleOne` is reached from nowhere else. |
| Secondary transfers (`secondary.go`) | **Refused, 409, with the alternative** | The transfer the holder wanted is possible, just not here: they send it from their own wallet, their wallet reads the published lists and builds against them, and the network checks it. |
| Distributions (`distribution.go`) | **Works, unchanged** | The register is derivable from the chain: the published holder list names the holders and the policy server scans their addresses. A payout is an ordinary payment in another asset, so it needs no co-signature. A holder with no payout address on file is skipped with that reason, as for any other token. |
| Listings (`listings.go`) | **Works, and reflects the chain** | The read carries the published list's version, commitment, holder count and the per-transfer coin bound, so a venue can fetch the same document and reach the same answer instead of trusting a stamp. |
| Eligibility (`id.go`) | **Works, from the published list** | Eligibility for this token is membership of the list the chain reads, not a category stamp this platform grants. A stamp-based answer would be wrong in both directions. The holder's height bounds ride along, so a venue is not told "eligible" about a holder whose window has not opened. |
| Investor mandates (`mandate_investor.go`) | **Works, unchanged** | A payout address is still checked against the investor's holding address for this token. Pasting one into the other strands funds exactly as it would for any other election, so the check stays. |
| Receipt programme (`dr.go`) | **Refused, 409** on enable, mint and redeem | It mints into a platform-held account and relies on transfer rules this platform checks when it approves a transfer. Neither exists here. The chain-derived supply read is unaffected. |

## 3. The limit the issuer has to know first

The rules program checks each of a transfer's inputs and outputs against the
published lists, so the number of slots it checks is fixed when the program is
compiled, and that number is part of the program's identity. The shipped program
bounds a transfer to four inputs and six outputs, which leaves room for **two** of
the holder's own coins.

So: **a holder can combine at most two of their coins of this token in a single
transfer**, and it cannot be raised for a token already issued. That sentence, and
"changes take effect when the updated list is published", appear on the wizard
card where the issuer chooses the model, on the issuance detail page, and on the
console itself. `test/network-copy.test.js` pins all three, and pins the
protocol-level explanation to `/docs`, which stays the only surface that uses
protocol names.
