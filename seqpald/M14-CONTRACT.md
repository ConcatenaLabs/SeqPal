# M14: the subscription escrow covenant, and distributions in one transaction

Matching test: `m14_test.go`. Code: `escrow_covenant.go` (derivation and spend building),
`escrow_terms.go`, `escrow_spend.go`, `escrow_close.go`, `escrow_chain.go`,
`distribution_tx.go`. Front-end: `src/components/ClosingCard.jsx`,
`src/components/money/SubscribeFlow.jsx`, `src/components/DistributionConsole.jsx`.

A USDX subscription is paid into an escrow the network enforces, and no wallet of this
platform ever holds it. A distribution is one transaction the issuer's own wallet funds and
signs. Both replace a send from the platform's `seqpal-escrow` wallet.

## 1. The covenant

Every subscription is paid to an address of its own, an instance of the
`sequentia/subscription-escrow` template of
[`ConcatenaLabs/sequentia-contracts`](https://github.com/ConcatenaLabs/sequentia-contracts)
(`docs/subscription-escrow.md` there is its specification):

    P2TR(NUMS, {{release, cancel}, {refund, H_TapData(SUBSCRIPTION)}})

The internal key is NUMS, so the output has no key path and nobody holds a key to it.

| Leaf | Who | When | Where the money goes |
|---|---|---|---|
| `release` | the issuer's release key | from `CLOSE` | into one transaction whose output 0 pays the issuer's payout script at least `MINIMUM` of the payment asset |
| `cancel` | the issuer's release key | at any time | output `k` returns the coin at input `k` to the subscriber's refund script, less at most `FEE_CAP` |
| `refund` | anyone, no signature | from `DEADLINE` | the same output as the cancel |

The descriptor file is vendored at `covenants/subscription_escrow.json` and pinned by its
template hash, `e602bafffda0dd1f2c19b3725fb4a84aa3106ceee88ada4ff6865b249ca254b4`: a file
whose hash differs is refused, so a changed template can never silently move where
subscribers pay. Addresses are derived by the template's Go mirror,
`github.com/ConcatenaLabs/sequentia-contracts/mirrors/go`, pinned in `go.mod` at the
sequentia-contracts merge `e94ab98`. Spends are built and their sighashes computed by the
`elements` package, a copy of `openampd/internal/elements` from `ConcatenaLabs/openamp`
(its PIN note in `elements/tx.go` names the commit); that package's golden vectors run with
this suite.

## 2. Opening the escrow (the issuer)

`POST /api/issuances/{id}/escrow` (owner) with `release_key`, `minimum` (whole units) or
`minimum_atoms`, and `close_height` (or `close_time`). Without `signature` it answers the
terms and the statement to sign; with one it records them. `GET` the same path reads them.

| Parameter | Comes from |
|---|---|
| `PAY_ASSET` | the rail's asset, `SEQPALD_USDX_ASSET`, internal byte order |
| `ISSUER_KEY` | `release_key`: an x-only key in the issuer's OWN Sequentia wallet |
| `ISSUER_SCRIPT` | SHA256 of the scriptPubKey of the issuer's Sequentia payout mandate, which must be registered first |
| `MINIMUM` | the issuer's minimum raise |
| `CLOSE` | the close: a block height, or a unix time at or above 500,000,000 |
| `DEADLINE` | the close plus `SEQPALD_ESCROW_DEADLINE_DAYS` (14 days by default; in blocks at `SEQPALD_BLOCKS_PER_DAY` for a height close) |
| `FEE_CAP` | `SEQPALD_ESCROW_FEE_CAP_ATOMS` (100,000 by default) |
| `REFUND_SCRIPT` | SHA256 of the scriptPubKey of the subscriber's refund address, per subscription |
| `SUBSCRIPTION` | SHA256 of `seqpal/subscription-escrow/v1`, the issuance id and the subscription id, per subscription |

The statement (tag `seqpal-escrow-terms-v1`) is the whole of the terms, so the signature is
the issuer's acknowledgment of every one of them. It is signed **by the release key**, as a
tagged statement or as an ordinary signed message from that key's legacy address
(`verifyKeyStatement`), which proves the issuer holds the key. A browser wallet with a
contract engine names its contract key (`getContractKey`) and signs the statement with it
(`signContractStatement`) from the issuance page. The terms are then fixed for
good: every subscription address commits to them, so the Sequentia payout mandate cannot
change either. The payment asset must be one the node accepts for fees.

## 3. Subscribing (the subscriber)

`POST /api/issuances/{id}/subscribe` with `rail: usdx` derives the subscription's address
from the terms and the refund address and refuses unless the node reads the address back as
exactly the derived output. It is refused before the escrow is open and from the close on.
A request with `confidential: true` is refused before anything is recorded or paid: a
confidential coin at the escrow can only be released to the issuer, never cancelled or
refunded, because the cancel and refund leaves read the coin's asset and amount explicitly.
The answer carries the address, the terms, the parameters and the refund leaf.

The deposit is read from the chain without a wallet: one `scantxoutset` over `raw()`
descriptors for every open subscription, confirmations counted from each coin's height. Only
explicit coins of the payment asset count. A confidential coin carries no amount the chain
shows, and the cancel and refund leaves could not return it either. A subscription becomes
`in_escrow` at `SEQPALD_ESCROW_CONFS` confirmations, as on every rail.

`GET /api/subscriptions/{id}/escrow` (the subscriber) answers where the money is, its coins,
and the finished refund transaction: it needs nobody's signature, it is valid from the
deadline, and anyone can broadcast it.

## 4. Closing

`POST /api/issuances/{id}/close` keeps its signed closing authorization. For subscriptions in
the covenant, in this order:

1. Nothing moves unless the close has passed, the deadline is more than a day away (in
   blocks at `SEQPALD_BLOCKS_PER_DAY` for a height lock), and a dry run of the release over
   every subscription in escrow pays the issuer at least the minimum. Otherwise 409, with
   nothing delivered or released.
2. The tokens are delivered from the offering's escrow enclave, a transfer seqpald signs with
   the escrow enclave key and the policy server co-signs, with the same settlement row and
   intent-before-transfer as every delivery. A delivered subscription's settlement is
   `awaiting_release`; a refused or failed one is `delivery_failed`, then `awaiting_cancel`,
   and is never counted delivered from a balance (an investor who already holds the token
   would look delivered). A delivery interrupted by a stop is reconciled from the investor's
   balance only when it covers every delivery this offering made to them; otherwise it stays
   `delivering`, neither released nor refunded, for a person to reconcile. Once a delivery
   has failed, the rest are delivered only while the deliverable set can still be released at
   the minimum.
3. ONE release is built over the coins of every delivered subscription: output 0 the raise to
   the issuer, then the platform's escrow fee to `SEQPALD_REVENUE_ADDRESS` (or a fresh
   address of the platform's own wallet), then anything a subscriber paid beyond their
   subscription back to their refund address, then the network fee, paid in the payment
   asset out of the coins. The lock time is the close; each input carries a non-final
   sequence.
4. A cancel is built over the subscriptions that could not be delivered.

The answer carries both under `escrow_spends`. A repeated close hands back the same built
transaction while it covers the same subscriptions, so a signature already made still
applies. `POST /api/issuances/{id}/escrow/cancel` builds a cancel over chosen subscriptions,
or every undelivered one. A delivered subscription is cancelled only when the issuer names it: that is the way out
when deliveries failed after some succeeded and the delivered rest no longer reaches the
minimum, so no release can be built; the issuer returns their money and recovers the tokens
with a clawback.

Who each step depends on. The money depends on nobody's goodwill: it can leave escrow only to
the issuer, after the close, in a release that pays at least the minimum, or back to the
subscriber, and the deadline returns it if neither the issuer nor SeqPal acts. The tokens
depend on SeqPal and the policy server, which deliver them from the offering's escrow
enclave. The covenant cannot see the tokens: SeqPal hands the issuer the release only after
delivering, but an issuer that signs a release on its own after the close takes the raise
whether or not the tokens have gone out, and the tokens are then still SeqPal's to deliver.
The issuer depends on its own signature for the raise, and on signing it before the
deadline, because from the deadline the refund is open too and whichever is mined first
wins.

## 5. Signing and broadcasting

A release or a cancel goes to the issuer as the TRANSACTION, never a digest: the unsigned
transaction and, per input, `vin`, `sighash`, `xonlykey`, `leaf` and `control`, exactly the
input of the node wallet RPC `signopenamptransfer`, which recomputes every sighash from the
transaction, checks the control block commits to the coin being spent and that the key is in
the leaf, and signs only then. The sighash is Elements taproot, script path,
`SIGHASH_DEFAULT`, under the chain's genesis hash: it commits to every input, every output,
the lock time and the leaf, so a signature authorises that one transaction through that one
leaf, and not another coin, the other keyed leaf, or another chain.

A built release or cancel also carries `engine`: the same transaction as a browser wallet's
contract engine takes it (`signContractSpend`): the `sequentia/subscription-escrow`
template, each coin's own instance (the first as the contract coin, the others as siblings),
the sequence and the outputs, read from the unsigned transaction. The engine recomputes every
coin's address from its instance, rebuilds the transaction, shows it under the five-point
rule and signs each escrow input with its contract key, which must be the release key; the
issuance page sends the signed transaction back unbroadcast.

`POST /api/issuances/{id}/escrow/spends/{sid}/sign` takes the signatures as the RPC returns
them, or `signed_tx`, the whole signed transaction: it must be the transaction built, every
input spent through its recorded leaf, and the signatures are read out of it. Each is verified against the release key over the sighash computed here, a 65-byte
signature (an explicit sighash type) is refused, and the assembled transaction must have the
recorded txid and pass `testmempoolaccept` before it is sent.

Fund safety:

- Every spend is recorded (`escrow_spends`) before it is broadcast, with its txid, which the
  transaction fixes before it is signed, and its signed bytes. A lost write is healed by
  broadcasting the same bytes again: one transaction, mined at most once, and a node that
  already has it says so.
- A release or cancel the node refuses goes back to `built`; a refused refund is set aside
  (`superseded`) and built again at the current fee rate on the next pass; a spend whose coin
  another transaction spent is `failed`. An error reaching the node is not a refusal: the
  spend stays `broadcasting` and the watcher sends the same bytes again, under the offering's
  lock. A spend is `broadcast` once the node has it, and the move to `broadcast` is a
  conditional update, so the books are applied once however many callers get there.
- No spend is built over a coin a broadcasting or broadcast spend holds. Building a spend of
  a kind supersedes the unsigned one before it.
- The books are written once per broadcast: the coins marked spent, each settlement `settled`
  (release) or `refunded` (cancel, refund), and the ledger.

From the deadline the watcher builds and broadcasts the refund, unprompted, for every
subscription whose tokens were not delivered and whose coins no live spend holds, and for any
coin that reached a subscription's address after it settled. A delivered (or delivering)
subscription's money is the issuer's, released with the rest, and is left to the release; its
subscriber is no longer offered the refund transaction either. An offering whose last pass
found nothing to refund is scanned again after five minutes, not on every tick.

The books follow the node's acceptance: a spend counts as settled once the node has it in
its mempool, and there is no reorg watcher for the covenant rail. A confidential payment, or
one in another asset, is not credited; the covenant's cancel and refund cannot return a
confidential coin at all, so subscribers are told to pay transparently and a confidential
subscription is refused (section 3).

## 6. Distributions in one transaction

A run is created `open`, with no deposit, and snapshotted as before. Then
`POST /api/issuances/{id}/distributions/{runID}/execute`:

- without `tx`: each payable row is settled once into `planned` (its registered payout address
  and that address's scriptPubKey; an enclave key-path address is skipped, an unchecked one
  retried later) and the answer is the payout: one output per holder at their net, an
  unfunded transaction carrying exactly them, and the commands for the issuer's own wallet to
  fund it (`fundrawtransaction`, fee in any accepted asset; the command names the run's own)
  and sign it (`signrawtransactionwithwallet`);
- with `tx`: every planned row must be matched by its own output paying exactly its script,
  the run's asset and its net, explicit and with no nonce. The node checks the transaction
  whole (`testmempoolaccept`). Then the run records the bytes and the txid and every row its
  output, before the broadcast; a node refusal puts the rows back to `planned` (an error
  reaching the node leaves the run `paying`, never asking for a second transaction); a node
  that already has it (the issuer's wallet broadcast it) counts as broadcast; a broadcast marks
  them `paid` with the one txid, writes the ledger and completes the run with its artifacts.
  A run left `paying` is broadcast again with the same bytes.

Withheld amounts and the flooring dust are not in the transaction, so they stay with the
issuer.

## 7. What stays custodial

- The native-BTC rail: Bitcoin script cannot check an offering's minimum, so a tBTC
  subscription is paid to the platform's `seqpal-btc-escrow` wallet and released or refunded
  from it (`M5-CONTRACT.md`).
- Corporate-action dividend claims, paid from the `seqpal-escrow` servicing wallet after the
  issuer funds it (`M10-CONTRACT.md`).
- A USDX subscription or a distribution run paid into `seqpal-escrow` before the covenant held
  them, which settles out of it by the M5/M6/M7 state machines. A send from that wallet pays
  its network fee by the rule every wallet uses (`feeasset.go`): the asset it sends where the
  node accepts it, else the accepted asset whose largest coin in the wallet covers the most
  fees, or `SEQPALD_FEE_ASSET` where the operator names one the node accepts; never the policy
  asset by default.
