package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ConcatenaLabs/SeqPal/seqpald/elements"
)

// M14: subscriptions held by the escrow covenant, against the stubs.
//
// The fake node's chain (fakechain_test.go) checks every spend the way the
// covenant would: the control block commits to the coin, the BIP340 signature
// verifies over the Elements taproot sighash, CHECKLOCKTIMEVERIFY is met, and
// the pinned output is the one the leaf names. The regtest proof against a
// real sequentiad lives in scripts/; these cover the platform's logic.

const m14Payout = "seq-issuer-payout"

// openEscrow registers the issuer's Sequentia payout mandate and opens the
// offering's escrow with a fresh release key, closing `closeIn` blocks after
// the tip. It returns the release key's private key.
func (h *m5h) openEscrow(issuerSession, issID string, minimum uint64, closeIn int64) string {
	h.t.Helper()
	if err := h.s.st.UpsertMandate(&PayoutMandate{IssuanceID: issID, Chain: "sequentia", Address: m14Payout}); err != nil {
		h.t.Fatal(err)
	}
	priv := genPriv(h.t)
	body := map[string]any{"release_key": xonlyHex(h.t, priv), "minimum_atoms": minimum,
		"close_height": h.seq.height + closeIn}
	pre := h.do("POST", "/api/issuances/"+issID+"/escrow", issuerSession, body)
	if pre.code != 200 {
		h.t.Fatalf("escrow (sign_this): %d %s", pre.code, pre.errMsg())
	}
	signThis, _ := pre.body["sign_this"].(string)
	tag, _ := pre.body["tag"].(string)
	if signThis == "" || tag != escrowTermsTag {
		h.t.Fatalf("escrow open returned no statement: %s", pre.raw)
	}
	body["signature"] = signCanonical(h.t, priv, tag, signThis)
	fin := h.do("POST", "/api/issuances/"+issID+"/escrow", issuerSession, body)
	if fin.code != 200 {
		h.t.Fatalf("escrow open: %d %s", fin.code, fin.errMsg())
	}
	return priv
}

type m14env struct {
	h                                      *m5h
	issuerSession, issuerXOnly, issuerPriv string
	issID, assetID                         string
	releasePriv                            string
	terms                                  *EscrowTerms
}

func newM14(t *testing.T, minimum uint64) *m14env {
	t.Helper()
	h := newM5Harness(t, m5opts{escrowConfs: 2})
	h.s.cfg.revenueAddress = "seq-platform-revenue"
	e := &m14env{h: h}
	e.issuerPriv = genPriv(t)
	e.issuerSession, _, e.issuerXOnly = h.register(e.issuerPriv, "Issuer", "HN")
	e.issID, e.assetID, _ = h.deployLivePrivate(e.issuerSession, "ESCR", "HN", 1.0)
	e.releasePriv = h.openEscrow(e.issuerSession, e.issID, minimum, 10)
	e.terms, _ = h.s.st.EscrowTermsByIssuance(e.issID)
	if e.terms == nil {
		t.Fatal("no escrow terms recorded")
	}
	return e
}

// subscribe a fresh verified investor for `tokens` tokens and pay the full
// amount into its escrow address, confirmed to in_escrow.
func (e *m14env) subscribePaid(t *testing.T, name, refund string, tokens int) (*Subscription, string) {
	t.Helper()
	h := e.h
	sess, aid, _ := h.verifiedInvestor(genPriv(t), name, "HN", "ret")
	res := h.do("POST", "/api/issuances/"+e.issID+"/subscribe", sess, map[string]any{
		"rail": "usdx", "amount": tokens, "refund_address": refund,
	})
	if res.code != 200 {
		t.Fatalf("subscribe: %d %s", res.code, res.errMsg())
	}
	subObj, _ := res.body["subscription"].(map[string]any)
	id, _ := subObj["id"].(string)
	sub, _ := h.s.st.SubscriptionByID(id)
	h.seq.addCoin(sub.EscrowSPK, m5USDX, sub.PayAmount)
	h.seq.mine(1)
	h.s.watchDeposits()
	sub, _ = h.s.st.SubscriptionByID(id)
	if sub.State != "in_escrow" {
		t.Fatalf("subscription %s state %q after 2 confs, want in_escrow", name, sub.State)
	}
	return sub, aid
}

func (e *m14env) close(t *testing.T) resp {
	t.Helper()
	h := e.h
	pre := h.do("POST", "/api/issuances/"+e.issID+"/close", e.issuerSession, map[string]any{})
	if pre.code != 200 {
		t.Fatalf("close (sign_this): %d %s", pre.code, pre.errMsg())
	}
	sig := signCanonical(t, e.issuerPriv, pre.body["tag"].(string), pre.body["sign_this"].(string))
	return h.do("POST", "/api/issuances/"+e.issID+"/close", e.issuerSession, map[string]any{
		"signature": sig, "signer_xonly": e.issuerXOnly,
	})
}

func spendsOf(t *testing.T, r resp) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	list, _ := r.body["escrow_spends"].([]any)
	for _, v := range list {
		m, _ := v.(map[string]any)
		out[m["kind"].(string)] = m
	}
	return out
}

// signSpend signs every to_sign entry with priv, after checking each sighash
// against the Elements taproot sighash the test computes itself from the
// transaction and the fake chain's prevouts.
func (e *m14env) signSpend(t *testing.T, sp map[string]any, priv string) map[string]any {
	t.Helper()
	tx, err := decodeHexTx(sp["unsigned_tx"].(string))
	if err != nil {
		t.Fatal(err)
	}
	spent := make([]*elements.SpentOutput, len(tx.In))
	for i, in := range tx.In {
		k := outpointKey(in.Prevout)
		parts := strings.SplitN(k, ":", 2)
		var vout uint32
		fmt.Sscanf(parts[1], "%d", &vout)
		u := e.h.seq.unspent(parts[0], vout)
		if u == nil {
			t.Fatalf("input %d spends %s, not on the fake chain", i, k)
		}
		a, _ := hex.DecodeString(u.asset)
		var a32 [32]byte
		copy(a32[:], a)
		spk, _ := hex.DecodeString(u.spk)
		spent[i] = &elements.SpentOutput{Asset: elements.ExplicitAsset(a32), Value: elements.ExplicitValue(u.atoms), ScriptPubKey: spk}
	}
	var genesis [32]byte
	gd, _ := hex.DecodeString(fakeGenesisDisplay)
	for i := 0; i < 32; i++ {
		genesis[i] = gd[31-i]
	}
	var sigs []map[string]any
	toSign, _ := sp["to_sign"].([]any)
	if len(toSign) != len(tx.In) {
		t.Fatalf("to_sign has %d entries for %d inputs", len(toSign), len(tx.In))
	}
	for _, v := range toSign {
		m := v.(map[string]any)
		vin := int(m["vin"].(float64))
		leaf, _ := hex.DecodeString(m["leaf"].(string))
		if m["xonlykey"] != e.terms.ReleaseKey {
			t.Fatalf("input %d names key %v, want the release key", vin, m["xonlykey"])
		}
		if c, _ := m["control"].(string); c == "" {
			t.Fatalf("input %d has no control block", vin)
		}
		want, err := elements.TaprootSighash(tx, spent, elements.SighashDefault, genesis, vin, leaf)
		if err != nil {
			t.Fatal(err)
		}
		if m["sighash"] != hex.EncodeToString(want[:]) {
			t.Fatalf("input %d sighash %v, the transaction's is %x", vin, m["sighash"], want)
		}
		sig, err := signSighash(priv, m["sighash"].(string))
		if err != nil {
			t.Fatal(err)
		}
		sigs = append(sigs, map[string]any{"vin": vin, "signature": sig})
	}
	return map[string]any{"signatures": sigs}
}

func (e *m14env) postSigs(sp map[string]any, body map[string]any) resp {
	return e.h.do("POST", "/api/issuances/"+e.issID+"/escrow/spends/"+sp["id"].(string)+"/sign", e.issuerSession, body)
}

// assertNoWalletMoney fails if any platform wallet was asked to hold, find or
// move money on these paths.
func assertNoWalletMoney(t *testing.T, h *m5h) {
	t.Helper()
	for _, m := range h.seq.walletCallsOf(seqEscrowWallet) {
		switch m {
		case "sendtoaddress", "listunspent", "getnewaddress", "signrawtransactionwithwallet", "sendmany", "listtransactions":
			t.Fatalf("the platform wallet %s was asked for %s on a covenant path", seqEscrowWallet, m)
		}
	}
}

// ---------------------------------------------------------------------------

func TestM14EscrowTermsAreProvenAndFixed(t *testing.T) {
	h := newM5Harness(t, m5opts{})
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID, _, _ := h.deployLivePrivate(issuerSession, "TERM", "HN", 1.0)

	priv := genPriv(t)
	body := map[string]any{"release_key": xonlyHex(t, priv), "minimum_atoms": 1000, "close_height": h.seq.height + 5}
	// No mandate yet: the release would have nowhere to pay.
	if r := h.do("POST", "/api/issuances/"+issID+"/escrow", issuerSession, body); r.code != 409 {
		t.Fatalf("escrow without a payout mandate = %d, want 409 (%s)", r.code, r.raw)
	}
	h.s.st.UpsertMandate(&PayoutMandate{IssuanceID: issID, Chain: "sequentia", Address: m14Payout})
	// A close in the past is refused.
	past := map[string]any{"release_key": xonlyHex(t, priv), "minimum_atoms": 1000, "close_height": h.seq.height}
	if r := h.do("POST", "/api/issuances/"+issID+"/escrow", issuerSession, past); r.code != 400 {
		t.Fatalf("a close at the tip = %d, want 400", r.code)
	}
	pre := h.do("POST", "/api/issuances/"+issID+"/escrow", issuerSession, body)
	if pre.code != 200 || pre.body["sign_this"] == nil {
		t.Fatalf("unsigned open: %d %s", pre.code, pre.raw)
	}
	if got, _ := h.s.st.EscrowTermsByIssuance(issID); got != nil {
		t.Fatalf("an unsigned open recorded terms")
	}
	// Signed by another key: refused.
	body["signature"] = signCanonical(t, genPriv(t), escrowTermsTag, pre.body["sign_this"].(string))
	if r := h.do("POST", "/api/issuances/"+issID+"/escrow", issuerSession, body); r.code != 400 {
		t.Fatalf("terms signed by another key = %d, want 400", r.code)
	}
	body["signature"] = signCanonical(t, priv, escrowTermsTag, pre.body["sign_this"].(string))
	if r := h.do("POST", "/api/issuances/"+issID+"/escrow", issuerSession, body); r.code != 200 {
		t.Fatalf("signed open: %d %s", r.code, r.raw)
	}
	t1, _ := h.s.st.EscrowTermsByIssuance(issID)
	if t1 == nil || t1.ReleaseKey != xonlyHex(t, priv) || t1.MinimumAtoms != 1000 || t1.TemplateHash != escrowTemplateHash ||
		t1.IssuerSPK != fakeSPK(m14Payout) || t1.DeadlineLock != t1.CloseLock+uint32(defaultEscrowDeadlineDays*h.s.cfg.blocksPerDay) {
		t.Fatalf("recorded terms = %+v", t1)
	}
	// A second open with other terms changes nothing.
	other := genPriv(t)
	r := h.do("POST", "/api/issuances/"+issID+"/escrow", issuerSession, map[string]any{
		"release_key": xonlyHex(t, other), "minimum_atoms": 5, "close_height": h.seq.height + 50})
	if r.code != 200 {
		t.Fatalf("second open: %d", r.code)
	}
	if t2, _ := h.s.st.EscrowTermsByIssuance(issID); t2.ReleaseKey != t1.ReleaseKey || t2.MinimumAtoms != 1000 {
		t.Fatalf("the terms changed on a second open: %+v", t2)
	}
	// The payout mandate the escrow commits to cannot move.
	m := h.do("POST", "/api/issuances/"+issID+"/mandate", issuerSession, map[string]any{"chain": "sequentia", "address": "seq-elsewhere"})
	if m.code != 409 {
		t.Fatalf("mandate change after the escrow opened = %d, want 409 (%s)", m.code, m.raw)
	}
}

func TestM14SubscriptionPaysItsOwnCovenantAddress(t *testing.T) {
	e := newM14(t, 1)
	h := e.h
	a, _ := e.subscribePaid(t, "A", "seq-refund-a", 10)
	b, _ := e.subscribePaid(t, "B", "seq-refund-b", 10)
	for _, sub := range []*Subscription{a, b} {
		inst, err := deriveEscrow(escrowParamsFor(e.terms, sub), h.s.cfg.network)
		if err != nil {
			t.Fatal(err)
		}
		if sub.DepositAddress != inst.Address || sub.EscrowSPK != hex.EncodeToString(inst.SPK) {
			t.Fatalf("subscription %s pays %s, the covenant derives %s", sub.ID, sub.DepositAddress, inst.Address)
		}
		if !strings.HasPrefix(sub.DepositAddress, "tb1p") || fakeSPK(sub.DepositAddress) != sub.EscrowSPK {
			t.Fatalf("deposit address %s is not the taproot output %s", sub.DepositAddress, sub.EscrowSPK)
		}
		if sub.RefundSPK != fakeSPK(sub.RefundAddress) {
			t.Fatalf("refund script %s is not the refund address's", sub.RefundSPK)
		}
	}
	if a.DepositAddress == b.DepositAddress {
		t.Fatalf("two subscriptions share an escrow address")
	}
	assertNoWalletMoney(t, h)
}

func TestM14DepositCreditedFromChainInPayAssetAtDepth(t *testing.T) {
	e := newM14(t, 1)
	h := e.h
	sess, _, _ := h.verifiedInvestor(genPriv(t), "Inv", "HN", "ret")
	res := h.do("POST", "/api/issuances/"+e.issID+"/subscribe", sess, map[string]any{"rail": "usdx", "amount": 10, "refund_address": "seq-r"})
	if res.code != 200 {
		t.Fatalf("subscribe: %d %s", res.code, res.errMsg())
	}
	id := res.body["subscription"].(map[string]any)["id"].(string)
	sub, _ := h.s.st.SubscriptionByID(id)
	// A coin of another asset at the address is not a payment.
	h.seq.addCoin(sub.EscrowSPK, strings.Repeat("c8", 32), sub.PayAmount)
	h.seq.mine(5)
	h.s.watchDeposits()
	if got, _ := h.s.st.SubscriptionByID(id); got.State != "created" || got.DepositedAtoms != 0 {
		t.Fatalf("a coin of another asset credited the subscription: %+v", got)
	}
	// The pay asset, one confirmation: recorded, not yet in escrow.
	h.seq.addCoin(sub.EscrowSPK, m5USDX, sub.PayAmount)
	h.s.watchDeposits()
	got, _ := h.s.st.SubscriptionByID(id)
	if got.State != "created" || got.Confirmations != 1 || got.DepositedAtoms != sub.PayAmount {
		t.Fatalf("at 1 conf: %+v", got)
	}
	h.seq.mine(1)
	h.s.watchDeposits()
	if got, _ = h.s.st.SubscriptionByID(id); got.State != "in_escrow" || got.Confirmations != 2 {
		t.Fatalf("at 2 confs: state %q confs %d", got.State, got.Confirmations)
	}
	assertNoWalletMoney(t, h)
}

func TestM14ReleaseOnlyAfterCloseSignedByTheIssuer(t *testing.T) {
	e := newM14(t, 1000)
	h := e.h
	a, aidA := e.subscribePaid(t, "A", "seq-refund-a", 100)
	b, _ := e.subscribePaid(t, "B", "seq-refund-b", 50)

	// Before the close: refused, nothing delivered.
	if r := e.close(t); r.code != 409 || h.oa.transfers() != 0 {
		t.Fatalf("close before CLOSE = %d (%d deliveries), want 409 and none: %s", r.code, h.oa.transfers(), r.raw)
	}
	h.seq.setHeight(int64(e.terms.CloseLock))
	r := e.close(t)
	if r.code != 200 {
		t.Fatalf("close: %d %s", r.code, r.raw)
	}
	if h.oa.transfers() != 2 {
		t.Fatalf("deliveries = %d, want 2", h.oa.transfers())
	}
	sp := spendsOf(t, r)
	rel := sp["release"]
	if rel == nil || len(sp) != 1 {
		t.Fatalf("close returned %v, want exactly one release", sp)
	}
	if h.seq.broadcastCount() != 0 {
		t.Fatal("something was broadcast before the issuer signed")
	}
	for _, s := range []*Subscription{a, b} {
		st, _ := h.s.st.SettlementByID(s.ID)
		got, _ := h.s.st.SubscriptionByID(s.ID)
		if st.State != "awaiting_release" || got.State != "in_escrow" {
			t.Fatalf("after delivery: settlement %q subscription %q", st.State, got.State)
		}
	}
	// A wrong key is refused, and nothing broadcast.
	if w := e.postSigs(rel, e.signSpend(t, rel, genPriv(t))); w.code != 400 || h.seq.broadcastCount() != 0 {
		t.Fatalf("wrong-key release = %d, broadcasts %d", w.code, h.seq.broadcastCount())
	}
	sigs := e.signSpend(t, rel, e.releasePriv)
	ok := e.postSigs(rel, sigs)
	if ok.code != 200 || h.seq.broadcastCount() != 1 {
		t.Fatalf("signed release = %d, broadcasts %d: %s", ok.code, h.seq.broadcastCount(), ok.raw)
	}
	// Replayed: no second broadcast.
	if again := e.postSigs(rel, sigs); again.code != 200 || h.seq.broadcastCount() != 1 {
		t.Fatalf("replayed sign = %d, broadcasts %d", again.code, h.seq.broadcastCount())
	}
	txid := rel["txid"].(string)
	tx, _ := decodeHexTx(h.seq.txHex(txid))
	total := a.PayAmount + b.PayAmount
	fee := a.PayAmount*50/10000 + b.PayAmount*50/10000
	v0, _ := elements.ExplicitValueAmount(tx.Out[0].Value)
	v1, _ := elements.ExplicitValueAmount(tx.Out[1].Value)
	vf, _ := elements.ExplicitValueAmount(tx.Out[2].Value)
	if hex.EncodeToString(tx.Out[0].ScriptPubKey) != fakeSPK(m14Payout) || v0 < e.terms.MinimumAtoms {
		t.Fatalf("output 0 = %d to %x, want >= the minimum to the issuer", v0, tx.Out[0].ScriptPubKey)
	}
	if hex.EncodeToString(tx.Out[1].ScriptPubKey) != fakeSPK("seq-platform-revenue") || v1 != fee {
		t.Fatalf("output 1 = %d to %x, want the escrow fee %d to the platform", v1, tx.Out[1].ScriptPubKey, fee)
	}
	if len(tx.Out[2].ScriptPubKey) != 0 || displayAsset(tx.Out[2].Asset) != m5USDX || v0+v1+vf != total {
		t.Fatalf("fee output %d in %s; outputs sum %d of %d", vf, displayAsset(tx.Out[2].Asset), v0+v1+vf, total)
	}
	for _, s := range []*Subscription{a, b} {
		st, _ := h.s.st.SettlementByID(s.ID)
		got, _ := h.s.st.SubscriptionByID(s.ID)
		if st.State != "settled" || st.ReleaseTxid != txid || got.State != "settled" {
			t.Fatalf("after release: %+v / %q", st, got.State)
		}
	}
	_ = aidA
	// A re-close moves nothing.
	if r2 := e.close(t); r2.code != 200 || h.oa.transfers() != 2 || h.seq.broadcastCount() != 1 {
		t.Fatalf("re-close: %d, deliveries %d, broadcasts %d", r2.code, h.oa.transfers(), h.seq.broadcastCount())
	}
	assertNoWalletMoney(t, h)
}

func TestM14ReleaseLostWriteIsHealedWithTheSameBytes(t *testing.T) {
	e := newM14(t, 1)
	h := e.h
	a, _ := e.subscribePaid(t, "A", "seq-refund-a", 10)
	h.seq.setHeight(int64(e.terms.CloseLock))
	r := e.close(t)
	rel := spendsOf(t, r)["release"]
	sp, _ := h.s.st.EscrowSpendByID(rel["id"].(string))
	sigs, _ := parseSpendSigs(mustJSON(e.signSpend(t, rel, e.releasePriv)["signatures"]))
	genesis, _ := h.s.genesisHash()
	tx, err := assembleEscrowWitness(sp.UnsignedTx, sp.Inputs, sigs, genesis)
	if err != nil {
		t.Fatal(err)
	}
	// The intent was written, then the process died before the broadcast.
	h.s.st.UpdateEscrowSpendFields(sp.ID, map[string]any{"state": "broadcasting", "signed_tx": hex.EncodeToString(tx.Serialize())})
	h.s.watchDeposits()
	if h.seq.broadcastCount() != 1 || h.seq.txHex(sp.Txid) == "" {
		t.Fatalf("lost write not healed: broadcasts %d", h.seq.broadcastCount())
	}
	if st, _ := h.s.st.SettlementByID(a.ID); st.State != "settled" || st.ReleaseTxid != sp.Txid {
		t.Fatalf("settlement after heal: %+v", st)
	}
	h.s.watchDeposits()
	if h.seq.broadcastCount() != 1 {
		t.Fatal("the healed release was broadcast twice")
	}
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func TestM14CloseBelowMinimumDeliversNothing(t *testing.T) {
	e := newM14(t, 1_000_000*1e8)
	h := e.h
	e.subscribePaid(t, "A", "seq-refund-a", 10)
	h.seq.setHeight(int64(e.terms.CloseLock))
	r := e.close(t)
	if r.code != 409 || !strings.Contains(r.errMsg(), "minimum") || h.oa.transfers() != 0 || h.seq.broadcastCount() != 0 {
		t.Fatalf("close below the minimum = %d (%s), deliveries %d", r.code, r.errMsg(), h.oa.transfers())
	}
}

func TestM14FailedDeliveryIsCancelledNotReleased(t *testing.T) {
	e := newM14(t, 1)
	h := e.h
	a, _ := e.subscribePaid(t, "A", "seq-refund-a", 10)
	b, aidB := e.subscribePaid(t, "B", "seq-refund-b", 20)
	h.oa.setRefusal(aidB, "recipient not eligible")
	h.seq.setHeight(int64(e.terms.CloseLock))
	r := e.close(t)
	if r.code != 200 {
		t.Fatalf("close: %d %s", r.code, r.raw)
	}
	sp := spendsOf(t, r)
	rel, can := sp["release"], sp["cancel"]
	if rel == nil || can == nil {
		t.Fatalf("want a release and a cancel, got %v", sp)
	}
	relSp, _ := h.s.st.EscrowSpendByID(rel["id"].(string))
	canSp, _ := h.s.st.EscrowSpendByID(can["id"].(string))
	if len(relSp.Inputs) != 1 || relSp.Inputs[0].SubscriptionID != a.ID || len(canSp.Inputs) != 1 || canSp.Inputs[0].SubscriptionID != b.ID {
		t.Fatalf("release covers %v, cancel covers %v", relSp.Inputs, canSp.Inputs)
	}
	ctx, _ := decodeHexTx(canSp.UnsignedTx)
	v, _ := elements.ExplicitValueAmount(ctx.Out[0].Value)
	if hex.EncodeToString(ctx.Out[0].ScriptPubKey) != b.RefundSPK || v+e.terms.FeeCap < b.PayAmount || v >= b.PayAmount {
		t.Fatalf("cancel output 0 = %d to %x, want coin less at most the fee cap to B's refund", v, ctx.Out[0].ScriptPubKey)
	}
	for _, s := range []map[string]any{rel, can} {
		if res := e.postSigs(s, e.signSpend(t, s, e.releasePriv)); res.code != 200 {
			t.Fatalf("sign %s: %d %s", s["kind"], res.code, res.raw)
		}
	}
	if st, _ := h.s.st.SettlementByID(b.ID); st.State != "refunded" || st.RefundTxid != canSp.Txid {
		t.Fatalf("B after cancel: %+v", st)
	}
	if st, _ := h.s.st.SettlementByID(a.ID); st.State != "settled" {
		t.Fatalf("A after release: %+v", st)
	}
	assertNoWalletMoney(t, h)
}

func TestM14IssuerCancelsUndeliveredSubscriptions(t *testing.T) {
	e := newM14(t, 1)
	h := e.h
	a, _ := e.subscribePaid(t, "A", "seq-refund-a", 10)
	r := h.do("POST", "/api/issuances/"+e.issID+"/escrow/cancel", e.issuerSession, map[string]any{})
	if r.code != 200 {
		t.Fatalf("cancel: %d %s", r.code, r.raw)
	}
	can := r.body["spend"].(map[string]any)
	if can["kind"] != "cancel" || can["sign_command"] == nil {
		t.Fatalf("cancel spend view: %v", can)
	}
	if res := e.postSigs(can, e.signSpend(t, can, e.releasePriv)); res.code != 200 {
		t.Fatalf("sign cancel: %d %s", res.code, res.raw)
	}
	if got, _ := h.s.st.SubscriptionByID(a.ID); got.State != "refunded" {
		t.Fatalf("subscription after cancel = %q", got.State)
	}
	// Nothing left to cancel.
	if r2 := h.do("POST", "/api/issuances/"+e.issID+"/escrow/cancel", e.issuerSession, map[string]any{}); r2.code != 409 {
		t.Fatalf("second cancel = %d, want 409", r2.code)
	}
}

func TestM14DeadlineRefundsUndeliveredOnlyWithNoSignature(t *testing.T) {
	e := newM14(t, 1)
	h := e.h
	a, _ := e.subscribePaid(t, "A", "seq-refund-a", 10)
	b, aidB := e.subscribePaid(t, "B", "seq-refund-b", 20)
	h.oa.setRefusal(aidB, "recipient not eligible")
	h.seq.setHeight(int64(e.terms.CloseLock))
	if r := e.close(t); r.code != 200 {
		t.Fatalf("close: %d", r.code)
	}
	// The keyless refund is not final before the deadline.
	pre, err := h.s.buildReturnSpend("refund", mustIss(t, h, e.issID), e.terms, []*Subscription{b}, false)
	if err != nil {
		t.Fatalf("refund build: %v", err)
	}
	h.seq.mu.Lock()
	_, early := h.seq.checkTx(pre.SignedTx)
	h.seq.mu.Unlock()
	if early != "non-final" {
		t.Fatalf("refund before the deadline: %q, want non-final", early)
	}
	// Before the deadline the watcher refunds nothing.
	h.s.watchDeposits()
	if h.seq.broadcastCount() != 0 {
		t.Fatal("a refund went out before the deadline")
	}
	h.seq.setHeight(int64(e.terms.DeadlineLock))
	h.s.watchDeposits()
	if h.seq.broadcastCount() != 1 {
		t.Fatalf("broadcasts after the deadline = %d, want the one refund", h.seq.broadcastCount())
	}
	if got, _ := h.s.st.SubscriptionByID(b.ID); got.State != "refunded" {
		t.Fatalf("B after the deadline = %q, want refunded", got.State)
	}
	// A was delivered: its money is the issuer's, and stays in escrow for the release.
	coins, _ := h.s.st.EscrowCoinsBySubscription(a.ID)
	if len(coins) != 1 || h.seq.unspent(coins[0].Txid, coins[0].Vout) == nil {
		t.Fatalf("A's coin was moved by the deadline refund")
	}
	if got, _ := h.s.st.SubscriptionByID(a.ID); got.State != "in_escrow" {
		t.Fatalf("A after the deadline = %q", got.State)
	}
	h.s.watchDeposits()
	if h.seq.broadcastCount() != 1 {
		t.Fatal("the refund was broadcast twice")
	}
	assertNoWalletMoney(t, h)
}

func mustIss(t *testing.T, h *m5h, id string) *Issuance {
	iss, err := h.s.st.IssuanceByID(id)
	if err != nil || iss == nil {
		t.Fatalf("issuance %s: %v", id, err)
	}
	return iss
}

func TestM14SubscriberSeesTheirRefundTransaction(t *testing.T) {
	e := newM14(t, 1)
	h := e.h
	sess, _, _ := h.verifiedInvestor(genPriv(t), "Inv", "HN", "ret")
	res := h.do("POST", "/api/issuances/"+e.issID+"/subscribe", sess, map[string]any{"rail": "usdx", "amount": 10, "refund_address": "seq-r"})
	id := res.body["subscription"].(map[string]any)["id"].(string)
	sub, _ := h.s.st.SubscriptionByID(id)
	h.seq.addCoin(sub.EscrowSPK, m5USDX, sub.PayAmount)
	h.seq.mine(1)
	h.s.watchDeposits() // the watcher records the coin the refund is built from
	v := h.do("GET", "/api/subscriptions/"+id+"/escrow", sess, nil)
	if v.code != 200 {
		t.Fatalf("escrow view: %d %s", v.code, v.raw)
	}
	rtx, _ := v.body["refund_tx"].(string)
	if rtx == "" || v.body["address"] != sub.DepositAddress {
		t.Fatalf("escrow view without a refund transaction: %s", v.raw)
	}
	// Someone else's subscription is not theirs to read.
	other, _, _ := h.verifiedInvestor(genPriv(t), "Other", "HN", "ret")
	if o := h.do("GET", "/api/subscriptions/"+id+"/escrow", other, nil); o.code != 404 {
		t.Fatalf("another investor read the escrow view: %d", o.code)
	}
	h.seq.setHeight(int64(e.terms.DeadlineLock))
	h.seq.mu.Lock()
	_, reason := h.seq.checkTx(rtx)
	h.seq.mu.Unlock()
	if reason != "" {
		t.Fatalf("the subscriber's refund transaction is refused at the deadline: %s", reason)
	}
}

// TestM14FakeChainRefusesBrokenReleases keeps the stub honest: releases the
// platform would never build -- locked below the close, paying the issuer one
// atom under the minimum, paying another script, signed by another key -- are
// refused by the fake chain for the covenant's own reason, while the correct
// one is accepted.
func TestM14FakeChainRefusesBrokenReleases(t *testing.T) {
	e := newM14(t, 1000*1e8)
	h := e.h
	a, _ := e.subscribePaid(t, "A", "seq-refund-a", 1200)
	h.seq.setHeight(int64(e.terms.CloseLock))
	coins, inst, err := h.s.liveEscrowCoins(e.terms, []*Subscription{a})
	if err != nil || len(coins) != 1 {
		t.Fatalf("coins: %v %v", coins, err)
	}
	genesis, _ := h.s.genesisHash()
	platform := []byte{0x00, 0x14, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	verdict := func(t2 EscrowTerms, escrowFee uint64, priv string) string {
		t.Helper()
		b, err := buildRelease(&t2, coins, inst, escrowFee, platform, "x", 1000)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if err := escrowSighashes(b.Tx, b.Inputs, genesis); err != nil {
			t.Fatal(err)
		}
		sig, _ := signSighash(priv, b.Inputs[0].Sighash)
		tx, err := assembleEscrowWitness(hex.EncodeToString(b.Tx.Serialize()), b.Inputs, map[int]string{0: sig}, genesis)
		if err != nil && priv == e.releasePriv {
			t.Fatalf("assemble: %v", err)
		}
		if err != nil { // another key: assemble refuses it; force it into the witness
			tx = b.Tx
			tx.NormalizeWitness()
			sb, _ := hex.DecodeString(sig)
			sc, _ := hex.DecodeString(b.Inputs[0].Script)
			cb, _ := hex.DecodeString(b.Inputs[0].Control)
			tx.InWit[0].ScriptWitness = [][]byte{sb, sc, cb}
		}
		h.seq.mu.Lock()
		defer h.seq.mu.Unlock()
		_, reason := h.seq.checkTx(hex.EncodeToString(tx.Serialize()))
		return reason
	}
	good := *e.terms
	if r := verdict(good, 0, e.releasePriv); r != "" {
		t.Fatalf("the correct release is refused: %s", r)
	}
	early := *e.terms
	early.CloseLock--
	if r := verdict(early, 0, e.releasePriv); !strings.Contains(r, "Locktime requirement not satisfied") {
		t.Fatalf("a release locked below the close: %q", r)
	}
	short := *e.terms
	short.MinimumAtoms = 0
	// An escrow fee one atom larger than leaves the issuer its minimum.
	b, _ := buildRelease(&short, coins, inst, 1, platform, "x", 1000)
	over := coins[0].Atoms - e.terms.MinimumAtoms - b.NetworkFee + 1
	if r := verdict(short, over, e.releasePriv); !strings.Contains(r, "OP_VERIFY") {
		t.Fatalf("a release one atom under the minimum: %q", r)
	}
	elsewhere := *e.terms
	elsewhere.IssuerSPK = fakeSPK("seq-somebody-else")
	if r := verdict(elsewhere, 0, e.releasePriv); !strings.Contains(r, "OP_EQUALVERIFY") {
		t.Fatalf("a release to another script: %q", r)
	}
	if r := verdict(good, 0, genPriv(t)); !strings.Contains(r, "Invalid Schnorr signature") {
		t.Fatalf("a release signed by another key: %q", r)
	}
}

// A confidential payment to a subscription escrow is refused before anything
// is paid or recorded: a confidential coin there could only be released, never
// cancelled or refunded, because the return leaves read the coin's explicit
// asset and amount.
func TestM14ConfidentialSubscriptionRefusedBeforePayment(t *testing.T) {
	e := newM14(t, 1)
	h := e.h
	sess, _, _ := h.verifiedInvestor(genPriv(t), "Conf", "HN", "ret")
	before, _ := h.s.st.SubscriptionsByIssuance(e.issID)
	res := h.do("POST", "/api/issuances/"+e.issID+"/subscribe", sess, map[string]any{
		"rail": "usdx", "amount": 10, "refund_address": "seq-r", "confidential": true,
	})
	if res.code != 400 || !strings.Contains(res.errMsg(), "can only ever be released to the issuer") {
		t.Fatalf("confidential subscription = %d %q, want 400 and the reason", res.code, res.errMsg())
	}
	after, _ := h.s.st.SubscriptionsByIssuance(e.issID)
	if len(after) != len(before) {
		t.Fatalf("a refused confidential subscription was recorded: %d -> %d", len(before), len(after))
	}
	// The same subscription, transparent, is taken.
	res = h.do("POST", "/api/issuances/"+e.issID+"/subscribe", sess, map[string]any{
		"rail": "usdx", "amount": 10, "refund_address": "seq-r",
	})
	if res.code != 200 {
		t.Fatalf("transparent subscription: %d %s", res.code, res.errMsg())
	}
	assertNoWalletMoney(t, h)
}

// A built release carries what a wallet's contract engine needs to sign it
// itself, read from the transaction seqpald built, and the sign endpoint takes
// the whole signed transaction back: it must be the one built, every input
// spent through its own leaf, every signature the release key's.
func TestM14ReleaseSignedAsAWholeTransaction(t *testing.T) {
	e := newM14(t, 1000)
	h := e.h
	a, _ := e.subscribePaid(t, "A", "seq-refund-a", 100)
	b, _ := e.subscribePaid(t, "B", "seq-refund-b", 50)
	h.seq.setHeight(int64(e.terms.CloseLock))
	r := e.close(t)
	if r.code != 200 {
		t.Fatalf("close: %d %s", r.code, r.raw)
	}
	rel := spendsOf(t, r)["release"]
	eng, _ := rel["engine"].(map[string]any)
	if eng == nil {
		t.Fatalf("the release view carries no engine request: %v", rel["engine_error"])
	}
	if eng["templateHash"] != escrowTemplateHash || eng["txid"] != rel["txid"] {
		t.Fatalf("engine request names template %v and txid %v", eng["templateHash"], eng["txid"])
	}
	req := eng["request"].(map[string]any)
	inputs := req["inputs"].([]any)
	outputs := req["outputs"].([]any)
	tx, _ := decodeHexTx(rel["unsigned_tx"].(string))
	if req["path"] != "release" || len(inputs) != 2 || len(outputs) != len(tx.Out) ||
		uint32(req["sequence"].(float64)) != escrowSequence {
		t.Fatalf("engine request: path %v, %d inputs, %d outputs (tx %d), sequence %v", req["path"], len(inputs), len(outputs), len(tx.Out), req["sequence"])
	}
	if inputs[0].(map[string]any)["is"] != "contract" || inputs[1].(map[string]any)["is"] != "sibling" {
		t.Fatalf("inputs %v: want the contract coin, then a sibling", inputs)
	}
	// Each coin's instance is its own subscription's, deriving its address.
	subs := map[string]*Subscription{a.ID: a, b.ID: b}
	stored, _ := h.s.st.EscrowSpendByID(rel["id"].(string))
	insts := []map[string]any{eng["instance"].(map[string]any), inputs[1].(map[string]any)["instance"].(map[string]any)}
	for i, in := range stored.Inputs {
		want, _ := deriveEscrow(escrowParamsFor(e.terms, subs[in.SubscriptionID]), h.s.cfg.network)
		got := insts[i]["params"].(map[string]any)
		for k, v := range want.Params {
			if got[k] != v {
				t.Fatalf("input %d instance %s = %v, want %v", i, k, got[k], v)
			}
		}
	}
	if o := outputs[len(outputs)-1].(map[string]any); o["to"] != "fee" || o["asset"] != m5USDX {
		t.Fatalf("last output %v: want the fee in USDX", o)
	}
	if o := outputs[0].(map[string]any); o["to"] != "pay" || o["script"] != fakeSPK(m14Payout) {
		t.Fatalf("output 0 %v: want the issuer's payout", o)
	}

	// The signed transaction, as a wallet hands it back.
	signed := func(priv string) string {
		raw := e.signSpend(t, rel, priv)["signatures"].([]map[string]any)
		sigs := map[int]string{}
		for _, s := range raw {
			sigs[s["vin"].(int)] = s["signature"].(string)
		}
		// assembleEscrowWitness verifies each signature; a wrong key is
		// assembled by hand to reach the endpoint.
		stx, _ := decodeHexTx(stored.UnsignedTx)
		stx.NormalizeWitness()
		for i, in := range stored.Inputs {
			sig, _ := hex.DecodeString(sigs[i])
			script, _ := hex.DecodeString(in.Script)
			control, _ := hex.DecodeString(in.Control)
			stx.InWit[i].ScriptWitness = [][]byte{sig, script, control}
		}
		return hex.EncodeToString(stx.Serialize())
	}
	// Another transaction: refused before anything is checked further.
	other, _ := decodeHexTx(signed(e.releasePriv))
	v0, _ := elements.ExplicitValueAmount(other.Out[0].Value)
	other.Out[0].Value = elements.ExplicitValue(v0 - 1)
	if w := e.postSigs(rel, map[string]any{"signed_tx": hex.EncodeToString(other.Serialize())}); w.code != 400 ||
		!strings.Contains(w.errMsg(), "not the") || h.seq.broadcastCount() != 0 {
		t.Fatalf("another transaction = %d %q, broadcasts %d", w.code, w.errMsg(), h.seq.broadcastCount())
	}
	// Signed by another key: refused, nothing broadcast.
	if w := e.postSigs(rel, map[string]any{"signed_tx": signed(genPriv(t))}); w.code != 400 || h.seq.broadcastCount() != 0 {
		t.Fatalf("another key = %d %q, broadcasts %d", w.code, w.errMsg(), h.seq.broadcastCount())
	}
	// The release key's: verified and broadcast.
	if ok := e.postSigs(rel, map[string]any{"signed_tx": signed(e.releasePriv)}); ok.code != 200 || h.seq.broadcastCount() != 1 {
		t.Fatalf("signed release = %d %s, broadcasts %d", ok.code, ok.raw, h.seq.broadcastCount())
	}
	assertNoWalletMoney(t, h)
}
