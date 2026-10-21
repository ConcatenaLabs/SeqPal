# Payments to many wallets at once

A distribution's holders, or a refund's subscribers, paid in one payment the
issuer's own wallet makes, straight to each one's wallet. The code is
`payouts.go`; its proof is `payouts_test.go` and, against a real payment
service, `regtest_payouts_test.go` (`scripts/regtest-payouts.sh`).

## The payment service

`SEQPALD_PAYOUT_URL` names an [Arca](https://github.com/ConcatenaLabs/arca)
operator that serves the paid asset. A payment there is a distribution, A3.5's
claim tree: one round pays every wallet an allocation of its own, under a key
derived from the key the payer names; each wallet collects (takes up) its
allocation by itself the next time it syncs; what nobody collects by the
deadline (three days before the claim batch's first expiry, about 25 days after
the round at the specification's parameters) the operator pays back to the
payer's return address after the batch's sweep is final.

seqpald holds no key for it and signs nothing there. It reads two public calls:
`GET /v1/info` (which assets it serves, the smallest allocation, the fee
schedule) and `POST /v1/distribution_status` (a payment's claims, their states,
the deadline, the return). Pages never name the service: an issuer reads "pay
N holders", a holder "paid to your wallet".

## How a payment goes

1. **The list.** `POST /api/issuances/{id}/distributions/{runID}/payout` (a
   snapshotted run) or `POST /api/issuances/{id}/refunds` settles the list once,
   as a payout in state `planned`. Each holder is named by the enclave key of
   the SeqPal ID that holds its OpenAMP account (`account_wallets`), else by its
   account's one key at the policy server; a subscriber by its SeqPal ID's
   enclave key. Every wallet on the kit holds that key at `m/5/0` and looks for
   payments under it. Left out, each with its reason, its share staying with the
   issuer: an account with no key or several, a key already named, a share below
   the smallest allocation, a subscription paid on another rail. The page shows
   the total, the most the fee can be (the service's fixed parts exactly, its
   refresh fee at its full rate) and who is left out.
2. **The payment.** `GET …/payouts/{pid}/list` is the list, a line `KEY AMOUNT`
   each, in atoms of the paid asset. The issuer's wallet pays it, for example:

       arca distribute list.txt --asset ASSET --quote          # the exact fee
       arca distribute list.txt --asset ASSET --shown FEE      # the payment
       arca sync                                               # once its round is final

   and gives back the payment's id, the reference.
3. **The check.** `POST …/payouts/{pid}/reference {reference}` records it only
   when the service's status names exactly the list: the asset, every planned
   key once with exactly its amount, no other key; otherwise nothing is recorded
   and the refusal says what differs. The payout is then `paying`.
4. **Following it.** The money watcher reads the status every tick. Released:
   the payout is `paid`, every row `paid` with the round's txid (the one
   transaction that paid them), a ledger row `distribution` (or `refund`), a
   notice to each holder ("paid to your wallet … collects it when it next
   opens, before …"), the distribution run `complete` with its statements. A
   claim taken up: the row is `collected`. A claim returned: the row is
   `returned` with the return's txid, a ledger row `distribution_return` (or
   `refund_return`), and the run's holder row `returned`. No claim open: the
   payout is `complete`. A payment that never went out (`void`, `expired`)
   puts the payout back to `planned`.

## Rules

- **Restricted assets are refused** as the paid asset, at the run's creation
  and at the payout: every token issued here, and every asset the policy
  server knows. Their rules (the 2-of-2, a whitelist, supervision) would not
  travel with an allocation. Pay a restricted security's holders in USDX.
- **One payout per distribution run**, and a run paid to wallets is not paid
  on chain as well.
- **A reference is recorded against one payout only**, and a key once per
  payout.
- **A subscription is in at most one live refund**: a subscriber is never
  refunded twice while a refund of it is planned, paid or collected.
- **A refund is for money the escrow released** (`settled` subscriptions).
  Money still in the escrow covenant is refunded by the covenant's own cancel
  and refund, which pay every subscriber in one transaction to the script each
  gave; a covenant coin cannot join a payment to wallets.
- **What a payout does not do.** It moves no tokens. It does not choose the
  return address (the payer's wallet does). It cannot see the issuer's coins,
  so a total the wallet does not hold is the wallet's refusal.
