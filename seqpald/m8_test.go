package main

import (
	"strings"
	"testing"
)

// M8 (secondary market + Depository-Receipt programme + listings) tests, the
// tests-owner deliverable. They reuse the M5/M6/M7 harness verbatim (newM7Harness):
// the openampd stub h.oa, extended additively in m5_test.go with the OA-6 reissue,
// OA-5 burn build, GET /v1/supply, a co-sign-time refusal, a broadcast-then-lost
// response, and the settled transfer/burn transparency log; the escrow node stub
// h.seq; and the register/deploy helpers.
//
// COVERAGE
//  1. P2P secondary transfer (browser-key): a policy-co-signed holder-to-holder
//     transfer settles; the travel-rule record captures BOTH counterparties; a
//     replay never double-transfers or double-writes the ledger; a lost complete
//     write is reconciled from the log, never re-broadcast (the M5/M6 invariant).
//  2. THE REFUSAL PATH is first class: an ineligible recipient, a resale inside the
//     lockup window, and a Reg S window each return a REAL 403 with the reason,
//     surfaced honestly and terminal (a replay stays refused), nothing delivered.
//  3. Market-abuse acknowledgment gates the transfer surface (unrecorded -> 403
//     market_abuse_ack); a signed acknowledgment by the caller's own key is
//     accepted and opens the surface.
//  4. Wallet-initiated transfer capture: the /v1/log poller records a wallet
//     transfer, joins the originator to a registered identity, and is idempotent.
//  5. DR programme: enable enforces the US-person exclusion as a REAL j:US category
//     deny; a mint (OA-6) raises chain-derived supply into the custodian enclave and
//     is idempotent by request_id (incl. across token re-blinding); a redeem (OA-5)
//     burn lowers chain-derived supply and is idempotent + lost-write reconciled,
//     never double-burned; a non-custodied redeem holder is refused.
//  6. Listings: an issuer grant is a public, venue-facing read; a revoke withholds
//     it; a venue can only CHECK, never GRANT.

// drSupplyRead reads the chain-derived circulating supply through the owner API.
func (h *m5h) drSupplyRead(session, issID string) uint64 {
	h.t.Helper()
	r := h.do("GET", "/api/issuances/"+issID+"/dr/supply", session, nil)
	if r.code != 200 {
		h.t.Fatalf("dr supply: %d %s", r.code, r.errMsg())
	}
	v, _ := r.body["circulating_atoms"].(float64)
	return uint64(v)
}

// ackMarketAbuse records the once-per-investor acknowledgment that gates the
// transfer surfaces (no signature; a signature is exercised separately).
func (h *m5h) ackMarketAbuse(session string) {
	h.t.Helper()
	r := h.do("POST", "/api/id/market-abuse-ack", session, map[string]any{})
	if r.code != 200 {
		h.t.Fatalf("market-abuse ack: %d %s", r.code, r.errMsg())
	}
}

func (h *m5h) p2pLedgerRows(issID, kind string) int {
	h.t.Helper()
	ledger, err := h.s.st.LedgerByIssuance(issID)
	if err != nil {
		h.t.Fatalf("ledger: %v", err)
	}
	n := 0
	for _, e := range ledger {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

// ===========================================================================
// 1. P2P secondary transfer (browser-key): settle, travel rule, idempotency.
// ===========================================================================

func TestM8_P2PBrowserTransferCosignsWithTravelRuleAndIsIdempotent(t *testing.T) {
	h := newM7Harness(t, m5opts{})
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID, assetID, _ := h.deployLivePrivate(issuerSession, "SEC", "HN", 1.0)

	aSession, aAID, _ := h.verifiedInvestor(genPriv(t), "Alice", "HN", "ret")
	_, bAID, _ := h.verifiedInvestor(genPriv(t), "Bob", "GB", "ret")
	h.ackMarketAbuse(aSession)

	ini := h.do("POST", "/api/transfers", aSession, map[string]any{"asset": assetID, "to_aid": bAID, "atoms": 100})
	if ini.code != 200 {
		t.Fatalf("initiate: %d %s", ini.code, ini.errMsg())
	}
	transferID, _ := ini.body["transfer_id"].(string)
	if transferID == "" {
		t.Fatalf("no transfer_id: %s", ini.raw)
	}
	// The travel-rule record captures BOTH counterparties (name + residence + AID).
	tr, _ := ini.body["travel_rule"].(map[string]any)
	orig, _ := tr["originator"].(map[string]any)
	benef, _ := tr["beneficiary"].(map[string]any)
	if orig["aid"] != aAID || benef["aid"] != bAID {
		t.Fatalf("travel-rule AIDs not captured: %v", tr)
	}
	if orig["name"] != "Alice" || benef["name"] != "Bob" {
		t.Fatalf("travel-rule names not captured: %v", tr)
	}
	if orig["residence"] != "HN" || benef["residence"] != "GB" {
		t.Fatalf("travel-rule residences not captured: %v", tr)
	}
	if reg, _ := benef["registered"].(bool); !reg {
		t.Fatalf("beneficiary must be a registered platform identity: %v", tr)
	}

	// Complete: the browser signs, the policy server co-signs and broadcasts.
	comp := h.do("POST", "/api/transfers/"+transferID+"/complete", aSession, map[string]any{
		"sigs": map[string]string{"0": strings.Repeat("ab", 64)},
	})
	if comp.code != 200 {
		t.Fatalf("complete: %d %s", comp.code, comp.errMsg())
	}
	if st, _ := comp.body["state"].(string); st != "settled" {
		t.Fatalf("state=%q want settled (%s)", st, comp.raw)
	}
	if txid, _ := comp.body["txid"].(string); txid == "" {
		t.Fatalf("no settle txid: %s", comp.raw)
	}
	if h.oa.transfers() != 1 {
		t.Fatalf("deliveries=%d want 1", h.oa.transfers())
	}
	if h.oa.balanceOf(bAID, assetID) != 100 {
		t.Fatalf("beneficiary balance=%d want 100", h.oa.balanceOf(bAID, assetID))
	}
	if h.p2pLedgerRows(issID, "p2p_transfer") != 1 {
		t.Fatalf("p2p_transfer ledger rows=%d want 1", h.p2pLedgerRows(issID, "p2p_transfer"))
	}

	// Idempotent: a replayed complete does not double-transfer or double-write.
	comp2 := h.do("POST", "/api/transfers/"+transferID+"/complete", aSession, map[string]any{
		"sigs": map[string]string{"0": strings.Repeat("ab", 64)},
	})
	if comp2.code != 200 {
		t.Fatalf("replay complete: %d %s", comp2.code, comp2.raw)
	}
	if h.oa.transfers() != 1 {
		t.Fatalf("replay double-delivered: deliveries=%d", h.oa.transfers())
	}
	if h.p2pLedgerRows(issID, "p2p_transfer") != 1 {
		t.Fatalf("replay double-wrote the ledger: rows=%d", h.p2pLedgerRows(issID, "p2p_transfer"))
	}
}

// ===========================================================================
// 2. The refusal path is first class: a real 403 with the reason.
// ===========================================================================

func TestM8_P2PRefusalPathReturns403WithReason(t *testing.T) {
	cases := []struct{ name, reason string }{
		{"ineligible", "the recipient is not eligible to hold this restricted asset"},
		{"lockup", "resale is inside the 12-month lockup window"},
		{"regs", "the Reg S distribution-compliance window has not elapsed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newM7Harness(t, m5opts{})
			issuerSession, _, _ := h.register(genPriv(t), "Issuer", "HN")
			issID, assetID, _ := h.deployLivePrivate(issuerSession, "REF", "HN", 1.0)
			aSession, _, _ := h.verifiedInvestor(genPriv(t), "Alice", "HN", "ret")
			_, bAID, _ := h.verifiedInvestor(genPriv(t), "Bob", "HN", "ret")
			h.ackMarketAbuse(aSession)
			h.oa.setRefusal(bAID, tc.reason)

			ini := h.do("POST", "/api/transfers", aSession, map[string]any{"asset": assetID, "to_aid": bAID, "atoms": 50})
			if ini.code != 200 {
				t.Fatalf("initiate: %d %s", ini.code, ini.errMsg())
			}
			transferID, _ := ini.body["transfer_id"].(string)

			comp := h.do("POST", "/api/transfers/"+transferID+"/complete", aSession, map[string]any{"sigs": map[string]string{"0": "ab"}})
			if comp.code != 403 {
				t.Fatalf("refusal code=%d want 403 (%s)", comp.code, comp.raw)
			}
			if refused, _ := comp.body["refused"].(bool); !refused {
				t.Fatalf("refused flag not set: %s", comp.raw)
			}
			if reason, _ := comp.body["reason"].(string); reason != tc.reason {
				t.Fatalf("reason=%q want %q", reason, tc.reason)
			}
			// A refusal is honest and points at the public transparency log.
			if comp.body["log_url"] != "/openamp/v1/log" {
				t.Fatalf("no log_url on the refusal: %s", comp.raw)
			}
			// Nothing was delivered and no ledger row was written.
			if h.oa.transfers() != 0 {
				t.Fatalf("a refused transfer delivered: deliveries=%d", h.oa.transfers())
			}
			if h.p2pLedgerRows(issID, "p2p_transfer") != 0 {
				t.Fatalf("a refused transfer wrote a ledger row")
			}
			// Terminal + idempotent: a replay stays refused with the same reason.
			comp2 := h.do("POST", "/api/transfers/"+transferID+"/complete", aSession, map[string]any{"sigs": map[string]string{"0": "ab"}})
			if comp2.code != 403 {
				t.Fatalf("replayed refusal code=%d want 403", comp2.code)
			}
			if reason, _ := comp2.body["reason"].(string); reason != tc.reason {
				t.Fatalf("replayed reason=%q want %q", reason, tc.reason)
			}
		})
	}
}

// ===========================================================================
// 3. A lost complete write is reconciled from the log, never re-broadcast.
// ===========================================================================

func TestM8_P2PReconcilesLostWriteNoDoubleTransfer(t *testing.T) {
	h := newM7Harness(t, m5opts{})
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID, assetID, _ := h.deployLivePrivate(issuerSession, "RCN", "HN", 1.0)
	aSession, _, _ := h.verifiedInvestor(genPriv(t), "Alice", "HN", "ret")
	_, bAID, _ := h.verifiedInvestor(genPriv(t), "Bob", "HN", "ret")
	h.ackMarketAbuse(aSession)

	ini := h.do("POST", "/api/transfers", aSession, map[string]any{"asset": assetID, "to_aid": bAID, "atoms": 70})
	transferID, _ := ini.body["transfer_id"].(string)

	// The policy server broadcasts + logs the transfer, but the response is lost.
	h.oa.broadcastThenFailOnce = true
	comp := h.do("POST", "/api/transfers/"+transferID+"/complete", aSession, map[string]any{"sigs": map[string]string{"0": "ab"}})
	if comp.code != 200 {
		t.Fatalf("reconcile complete: %d %s", comp.code, comp.raw)
	}
	if reconciled, _ := comp.body["reconciled"].(bool); !reconciled {
		t.Fatalf("settle not reconciled from the log: %s", comp.raw)
	}
	if h.oa.transfers() != 1 {
		t.Fatalf("double-transfer: deliveries=%d", h.oa.transfers())
	}
	// A reconciled settle skips the ledger row (never double books).
	if h.p2pLedgerRows(issID, "p2p_transfer") != 0 {
		t.Fatalf("a reconciled settle wrote a ledger row (double books)")
	}
	// Idempotent replay: settled is terminal, no re-broadcast.
	comp2 := h.do("POST", "/api/transfers/"+transferID+"/complete", aSession, map[string]any{"sigs": map[string]string{"0": "ab"}})
	if comp2.code != 200 {
		t.Fatalf("replay: %d %s", comp2.code, comp2.raw)
	}
	if h.oa.transfers() != 1 {
		t.Fatalf("replay re-broadcast: deliveries=%d", h.oa.transfers())
	}
}

// ===========================================================================
// 4. Market-abuse acknowledgment gates the transfer surface.
// ===========================================================================

func TestM8_MarketAbuseAckGatesSurfaceAndSignedVariant(t *testing.T) {
	h := newM7Harness(t, m5opts{})
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "HN")
	_, assetID, _ := h.deployLivePrivate(issuerSession, "MAB", "HN", 1.0)
	aPriv := genPriv(t)
	aSession, _, aXOnly := h.verifiedInvestor(aPriv, "Alice", "HN", "ret")
	_, bAID, _ := h.verifiedInvestor(genPriv(t), "Bob", "HN", "ret")

	// Without the acknowledgment the transfer surface is closed.
	blocked := h.do("POST", "/api/transfers", aSession, map[string]any{"asset": assetID, "to_aid": bAID, "atoms": 10})
	if blocked.code != 403 {
		t.Fatalf("transfer allowed without the ack: %d %s", blocked.code, blocked.raw)
	}
	if blocked.body["requirement"] != "market_abuse_ack" {
		t.Fatalf("wrong gate requirement: %s", blocked.raw)
	}

	// GET exposes the canonical bytes + tag to sign.
	get := h.do("GET", "/api/id/market-abuse-ack", aSession, nil)
	if acked, _ := get.body["acknowledged"].(bool); acked {
		t.Fatalf("acknowledged before recording")
	}
	signThis, _ := get.body["sign_this"].(string)
	tag, _ := get.body["tag"].(string)
	if signThis == "" || tag != marketAbuseTag {
		t.Fatalf("missing sign_this/tag: %s", get.raw)
	}

	// A signed acknowledgment by the caller's own key is accepted.
	sig := signCanonical(t, aPriv, tag, signThis)
	ack := h.do("POST", "/api/id/market-abuse-ack", aSession, map[string]any{"signature": sig, "signer_xonly": aXOnly})
	if ack.code != 200 {
		t.Fatalf("signed ack: %d %s", ack.code, ack.errMsg())
	}
	if acked, _ := ack.body["acknowledged"].(bool); !acked {
		t.Fatalf("ack not recorded: %s", ack.raw)
	}

	// The surface is now open.
	open := h.do("POST", "/api/transfers", aSession, map[string]any{"asset": assetID, "to_aid": bAID, "atoms": 10})
	if open.code != 200 {
		t.Fatalf("transfer still blocked after the ack: %d %s", open.code, open.raw)
	}
}

// ===========================================================================
// 5. Wallet-initiated transfer captured by the /v1/log poller.
// ===========================================================================

func TestM8_WalletInitiatedTransferCapturedByPoller(t *testing.T) {
	h := newM7Harness(t, m5opts{})
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "HN")
	_, assetID, _ := h.deployLivePrivate(issuerSession, "WAL", "HN", 1.0)
	// A wallet-linked holder: a registered investor, NOT one of seqpald's enclaves.
	_, senderAID, _ := h.verifiedInvestor(genPriv(t), "WalletHolder", "HN", "ret")

	// The live wallet co-signs a secondary transfer with the policy server directly;
	// it lands in the public log. seqpald cannot build it, so the poller captures it.
	txid := strings.Repeat("cd", 32)
	h.oa.injectWalletTransfer(assetID, senderAID, 120, txid)
	h.s.pollWalletTransfers()

	got, _ := h.s.st.P2PTransferByTxid(txid)
	if got == nil {
		t.Fatalf("wallet transfer not captured")
	}
	if got.Source != "wallet" || got.State != "settled" {
		t.Fatalf("capture shape: source=%q state=%q", got.Source, got.State)
	}
	if got.OriginatorAID != senderAID || got.OrigName != "WalletHolder" {
		t.Fatalf("originator not joined to the identity: %+v", got)
	}
	if got.Atoms != 120 {
		t.Fatalf("captured atoms=%d want 120", got.Atoms)
	}

	// Idempotent: a second poll of the same log entry does not double-capture.
	h.s.pollWalletTransfers()
	again, _ := h.s.st.P2PTransferByTxid(txid)
	if again == nil || again.ID != got.ID {
		t.Fatalf("double-capture changed the record")
	}
}

// ===========================================================================
// 6. DR programme: US exclusion, mint (supply up), redeem (supply down).
// ===========================================================================

func TestM8_DREnableEnforcesUSExclusionCategoryRule(t *testing.T) {
	h := newM7Harness(t, m5opts{})
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID, assetID, _ := h.deployLivePrivate(issuerSession, "DRX", "HN", 1.0)

	en := h.do("POST", "/api/issuances/"+issID+"/dr/enable", issuerSession, map[string]any{})
	if en.code != 200 {
		t.Fatalf("dr enable: %d %s", en.code, en.errMsg())
	}
	prog, _ := h.s.st.DRProgramByIssuance(issID)
	if prog == nil || !prog.Enabled {
		t.Fatalf("DR program not enabled")
	}
	// The US-person exclusion is a REAL policy-server j:US category deny, applied
	// through the amendment chain, not a display string.
	rules := string(h.oa.rulesFor(assetID))
	if !strings.Contains(rules, "j:US") {
		t.Fatalf("US exclusion not written as a j:US category deny: %s", rules)
	}
	if prog.USExclusionHeight == 0 || prog.MutationID == "" {
		t.Fatalf("US exclusion height/mutation not recorded: %+v", prog)
	}
}

func TestM8_DRMintRaisesChainDerivedSupplyIdempotent(t *testing.T) {
	h := newM7Harness(t, m5opts{})
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID, assetID, escrowAID := h.deployLivePrivate(issuerSession, "DRM", "HN", 1.0)
	h.do("POST", "/api/issuances/"+issID+"/dr/enable", issuerSession, map[string]any{})

	// Seed the DR custodian (offering escrow) enclave with the initial float.
	h.oa.setBalance(escrowAID, assetID, 1000)
	if sup := h.drSupplyRead(issuerSession, issID); sup != 1000 {
		t.Fatalf("initial chain-derived supply=%d want 1000", sup)
	}

	m := h.do("POST", "/api/issuances/"+issID+"/dr/mint", issuerSession, map[string]any{"atoms": 500, "request_id": "mint-1"})
	if m.code != 200 {
		t.Fatalf("dr mint: %d %s", m.code, m.errMsg())
	}
	if src, _ := m.body["supply_source"].(string); src != "chain-derived" {
		t.Fatalf("supply_source=%q want chain-derived", src)
	}
	if got := uint64(m.body["circulating_atoms"].(float64)); got != 1500 {
		t.Fatalf("supply after mint=%d want 1500", got)
	}
	if h.oa.balanceOf(escrowAID, assetID) != 1500 {
		t.Fatalf("mint did not land in the custodian enclave: balance=%d", h.oa.balanceOf(escrowAID, assetID))
	}
	txid1, _ := m.body["reissue_txid"].(string)
	if txid1 == "" {
		t.Fatalf("no reissue txid: %s", m.raw)
	}

	// Idempotent by request_id: a replay returns the same txid and does not re-mint.
	m2 := h.do("POST", "/api/issuances/"+issID+"/dr/mint", issuerSession, map[string]any{"atoms": 500, "request_id": "mint-1"})
	if m2.code != 200 {
		t.Fatalf("replay mint: %d %s", m2.code, m2.raw)
	}
	if txid2, _ := m2.body["reissue_txid"].(string); txid2 != txid1 {
		t.Fatalf("replay minted a new txid: %s vs %s", txid2, txid1)
	}
	if h.oa.supplyOf(assetID) != 1500 {
		t.Fatalf("replay double-minted: supply=%d want 1500", h.oa.supplyOf(assetID))
	}
}

func TestM8_DRMintHandlesTokenReblinding(t *testing.T) {
	h := newM7Harness(t, m5opts{})
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID, assetID, escrowAID := h.deployLivePrivate(issuerSession, "DRB", "HN", 1.0)
	h.do("POST", "/api/issuances/"+issID+"/dr/enable", issuerSession, map[string]any{})
	h.oa.setBalance(escrowAID, assetID, 100)

	// The policy server re-blinds the reissuance token first (one 202 "reblinding"
	// response), then broadcasts. seqpald retries the same request_id transparently.
	h.oa.reblindLeft["reb-1"] = 1
	m := h.do("POST", "/api/issuances/"+issID+"/dr/mint", issuerSession, map[string]any{"atoms": 250, "request_id": "reb-1"})
	if m.code != 200 {
		t.Fatalf("mint through reblinding: %d %s", m.code, m.raw)
	}
	if txid, _ := m.body["reissue_txid"].(string); txid == "" {
		t.Fatalf("no reissue txid after reblinding: %s", m.raw)
	}
	if h.oa.supplyOf(assetID) != 350 {
		t.Fatalf("supply after reblinded mint=%d want 350", h.oa.supplyOf(assetID))
	}
}

func TestM8_DRRedeemBurnLowersChainDerivedSupplyIdempotent(t *testing.T) {
	h := newM7Harness(t, m5opts{})
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID, assetID, escrowAID := h.deployLivePrivate(issuerSession, "DRR", "HN", 1.0)
	h.do("POST", "/api/issuances/"+issID+"/dr/enable", issuerSession, map[string]any{})
	h.oa.setBalance(escrowAID, assetID, 1000)

	rd := h.do("POST", "/api/issuances/"+issID+"/dr/redeem", issuerSession, map[string]any{"atoms": 400, "request_id": "red-1"})
	if rd.code != 200 {
		t.Fatalf("dr redeem: %d %s", rd.code, rd.errMsg())
	}
	if got := uint64(rd.body["circulating_atoms"].(float64)); got != 600 {
		t.Fatalf("supply after redeem=%d want 600", got)
	}
	if h.oa.balanceOf(escrowAID, assetID) != 600 {
		t.Fatalf("custodian balance after burn=%d want 600", h.oa.balanceOf(escrowAID, assetID))
	}
	if h.oa.burnLogCount(assetID) != 1 {
		t.Fatalf("burn log entries=%d want 1", h.oa.burnLogCount(assetID))
	}
	if h.p2pLedgerRows(issID, "dr_redeem_burn") != 1 {
		t.Fatalf("dr_redeem_burn ledger rows=%d want 1", h.p2pLedgerRows(issID, "dr_redeem_burn"))
	}

	// Idempotent by request_id: a replay does not burn a second time.
	rd2 := h.do("POST", "/api/issuances/"+issID+"/dr/redeem", issuerSession, map[string]any{"atoms": 400, "request_id": "red-1"})
	if rd2.code != 200 {
		t.Fatalf("replay redeem: %d %s", rd2.code, rd2.raw)
	}
	if h.oa.supplyOf(assetID) != 600 {
		t.Fatalf("replay double-burned: supply=%d want 600", h.oa.supplyOf(assetID))
	}
	if h.oa.burnLogCount(assetID) != 1 {
		t.Fatalf("replay wrote a second burn: count=%d", h.oa.burnLogCount(assetID))
	}
}

func TestM8_DRRedeemReconcilesLostBurnNoDoubleBurn(t *testing.T) {
	h := newM7Harness(t, m5opts{})
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID, assetID, escrowAID := h.deployLivePrivate(issuerSession, "DRL", "HN", 1.0)
	h.do("POST", "/api/issuances/"+issID+"/dr/enable", issuerSession, map[string]any{})
	h.oa.setBalance(escrowAID, assetID, 1000)

	// The burn broadcasts + logs, but the /complete response is lost. seqpald must
	// reconcile from the log, never double-burn.
	h.oa.broadcastThenFailOnce = true
	rd := h.do("POST", "/api/issuances/"+issID+"/dr/redeem", issuerSession, map[string]any{"atoms": 300, "request_id": "rl-1"})
	if rd.code != 200 {
		t.Fatalf("redeem should reconcile from the log: %d %s", rd.code, rd.raw)
	}
	if h.oa.supplyOf(assetID) != 700 {
		t.Fatalf("supply after reconciled burn=%d want 700", h.oa.supplyOf(assetID))
	}
	if h.oa.burnLogCount(assetID) != 1 {
		t.Fatalf("burn count=%d want exactly 1 (no double-burn)", h.oa.burnLogCount(assetID))
	}
	op, _ := h.s.st.DROpByRequestID("rl-1")
	if op == nil || op.State != "broadcast" || op.Txid == "" {
		t.Fatalf("op not reconciled to broadcast: %+v", op)
	}
}

func TestM8_DRRedeemNonCustodiedHolderRefused(t *testing.T) {
	h := newM7Harness(t, m5opts{})
	issuerSession, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID, _, _ := h.deployLivePrivate(issuerSession, "DRN", "HN", 1.0)
	h.do("POST", "/api/issuances/"+issID+"/dr/enable", issuerSession, map[string]any{})

	// A registered investor is not one of seqpald's custodied enclaves; a holder-
	// signed burn goes through the wallet, not the DR console.
	_, bAID, _ := h.verifiedInvestor(genPriv(t), "Bob", "HN", "ret")
	rd := h.do("POST", "/api/issuances/"+issID+"/dr/redeem", issuerSession, map[string]any{"atoms": 100, "holder_aid": bAID, "request_id": "n-1"})
	if rd.code != 400 {
		t.Fatalf("non-custodied redeem code=%d want 400 (%s)", rd.code, rd.raw)
	}
}

// ===========================================================================
// 7. Listings: issuer grants, a venue reads (never grants).
// ===========================================================================

func TestM8_ListingsGrantRevokeAndPublicRead(t *testing.T) {
	h := newM7Harness(t, m5opts{})
	issuerSession, issuerAID, _ := h.register(genPriv(t), "Issuer", "HN")
	issID, assetID, _ := h.deployLivePrivate(issuerSession, "LST", "HN", 1.0)

	// Before any grant: not authorized (a PUBLIC read, no session).
	pre := h.do("GET", "/api/listings?asset="+assetID, "", nil)
	if pre.code != 200 {
		t.Fatalf("public listings read: %d %s", pre.code, pre.raw)
	}
	if auth, _ := pre.body["authorized"].(bool); auth {
		t.Fatalf("asset authorized before any grant")
	}

	// Grant (owner-scoped).
	g := h.do("POST", "/api/issuances/"+issID+"/listing", issuerSession, map[string]any{"authorized": true, "venues": []string{"seqdex"}})
	if g.code != 200 {
		t.Fatalf("grant: %d %s", g.code, g.errMsg())
	}

	// The public, venue-facing read now returns the authorization.
	post := h.do("GET", "/api/listings?asset="+assetID, "", nil)
	if auth, _ := post.body["authorized"].(bool); !auth {
		t.Fatalf("asset not authorized after grant: %s", post.raw)
	}
	all := h.do("GET", "/api/listings?issuer="+issuerAID, "", nil)
	arr, _ := all.body["listings"].([]any)
	if len(arr) != 1 {
		t.Fatalf("issuer listings=%d want 1: %s", len(arr), all.raw)
	}

	// A revoke withholds it from the venue-facing read.
	rv := h.do("POST", "/api/issuances/"+issID+"/listing", issuerSession, map[string]any{"authorized": false})
	if rv.code != 200 {
		t.Fatalf("revoke: %d %s", rv.code, rv.errMsg())
	}
	rev := h.do("GET", "/api/listings?asset="+assetID, "", nil)
	if auth, _ := rev.body["authorized"].(bool); auth {
		t.Fatalf("revoked asset still authorized")
	}
	all2 := h.do("GET", "/api/listings", "", nil)
	if arr2, _ := all2.body["listings"].([]any); len(arr2) != 0 {
		t.Fatalf("revoked listing still in the public list: %d", len(arr2))
	}
}
