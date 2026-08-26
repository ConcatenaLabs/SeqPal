package main

import (
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
)

// Platform fees and issuer payout mandates.
//
// SeqPal's own fees are invoiced and COLLECTED before deploy, payable by the
// ISSUER's choice of rail (on-chain USDX/tBTC, or the SIMULATED fiat rail). An
// unpaid setup fee blocks deploy. The escrow fee accrues on real balances and is
// deducted at release (in the closing engine). Before closing, the issuer
// registers a BIP340-signed payout mandate whose ordinary address escrow release
// pays; an enclave key-path address is rejected.

const mandateTag = "seqpal-payout-mandate-v1"

// ensureSetupInvoice returns the issuance's setup-fee invoice, creating an unpaid
// one (priced from cfg.setupFeeUSD) on first call. A zero configured fee yields a
// pre-paid invoice so a fee-free deployment is never blocked.
func (s *server) ensureSetupInvoice(issuanceID string) (*FeeInvoice, error) {
	if inv, err := s.st.SetupFeeForIssuance(issuanceID); err != nil {
		return nil, err
	} else if inv != nil {
		return inv, nil
	}
	inv := &FeeInvoice{
		ID: mustID(), IssuanceID: issuanceID, Kind: "setup",
		AmountUSD: s.cfg.setupFeeUSD, State: "unpaid",
	}
	if s.cfg.setupFeeUSD <= 0 {
		inv.State = "paid"
		inv.PaidAt = time.Now().Unix()
	}
	if err := s.st.InsertFeeInvoice(inv); err != nil {
		return nil, err
	}
	return inv, nil
}

// setupFeePaid reports whether the issuance's setup fee is paid. It is the deploy
// gate: an unpaid setup fee blocks the mint.
func (s *server) setupFeePaid(issuanceID string) (bool, error) {
	inv, err := s.ensureSetupInvoice(issuanceID)
	if err != nil {
		return false, err
	}
	return inv.State == "paid", nil
}

// handleFees is GET /issuances/{id}/fees (owner): the fee invoices, with the setup
// invoice auto-created.
func (s *server) handleFees(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	if _, err := s.ensureSetupInvoice(iss.ID); err != nil {
		writeErr(w, 500, "store error")
		return
	}
	invoices, err := s.st.FeeInvoicesByIssuance(iss.ID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	writeJSON(w, 200, map[string]any{"issuance_id": iss.ID, "invoices": invoices, "setup_fee_usd": s.cfg.setupFeeUSD, "escrow_fee_bps": s.cfg.escrowFeeBps})
}

type payFeeReq struct {
	Kind string `json:"kind"` // setup (default)
	Rail string `json:"rail"` // usdx | btc | card | bank
}

// handlePayFee is POST /issuances/{id}/fees/pay (owner): collect a platform fee on
// the issuer's chosen rail. Fiat starts a SIMULATED checkout; on-chain returns a
// deposit address the deposit watcher credits when the payment confirms.
func (s *server) handlePayFee(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	var req payFeeReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	if req.Kind == "" {
		req.Kind = "setup"
	}
	if req.Kind != "setup" {
		writeErr(w, 400, "only the setup fee is collectible here (the escrow fee is deducted at release)")
		return
	}
	inv, err := s.ensureSetupInvoice(iss.ID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if inv.State == "paid" {
		writeJSON(w, 200, map[string]any{"invoice": inv, "already_paid": true})
		return
	}
	s.payInvoiceOnRail(w, acct, inv, req.Rail, map[string]any{"issuance_id": iss.ID})
}

// payInvoiceOnRail collects one fee invoice on the payer's chosen rail, whatever
// the invoice is for. Fiat starts a SIMULATED checkout; on-chain returns a deposit
// address the deposit watcher credits when the payment confirms. ctx names the
// thing being paid for, and is recorded in the audit entry.
func (s *server) payInvoiceOnRail(w http.ResponseWriter, acct *Account, inv *FeeInvoice, wanted string, ctx map[string]any) {
	// One quote at a time on one invoice. Everything below reads what this
	// invoice already holds for the chosen rail and then writes to it, so two
	// requests arriving together would each derive a fresh address and one of
	// them would be forgotten -- and a forgotten address is one nothing watches,
	// which is what quoting per rail exists to prevent.
	defer s.st.LockFee(inv.ID)()
	if fresh, err := s.st.FeeInvoiceByID(inv.ID); err == nil && fresh != nil {
		*inv = *fresh
	}
	audit := func(event string, extra map[string]any) {
		fields := map[string]any{"invoice": inv.ID}
		for k, v := range ctx {
			fields[k] = v
		}
		for k, v := range extra {
			fields[k] = v
		}
		s.st.Audit(acct.AID, event, fields)
	}
	rail := strings.ToLower(strings.TrimSpace(wanted))
	switch rail {
	case "card", "bank":
		pay, err := s.startFiatFeePayment(inv.ID, rail, inv.AmountUSD)
		if err != nil {
			writeErr(w, 500, "could not start the simulated checkout")
			return
		}
		// What was actually charged, not the invoice's zero: this row is the
		// record of the payment, and a fee booked at nothing is not one.
		_ = s.st.SetFeeQuote(inv, rail, FeeQuote{Amount: pay.AmountMinor, Ccy: pay.Ccy})
		_ = s.st.UpdateFeeInvoiceFields(inv.ID, map[string]any{"funds_simulated": 1})
		audit("fee.pay.fiat", map[string]any{"rail": rail, "simulated": true})
		writeJSON(w, 200, map[string]any{"invoice": inv, "checkout": fiatView(pay), "funds_simulated": true})

	case "usdx":
		q, err := s.feeQuoteFor(inv, "usdx", usdToAtoms(inv.AmountUSD), "USDX", s.newUSDXDepositAddress)
		if err != nil {
			writeErr(w, 502, "could not derive a fee deposit address: %v", err)
			return
		}
		addr, amount := q.Address, q.Amount
		audit("fee.pay.usdx", map[string]any{"address": addr, "amount": amount})
		writeJSON(w, 200, map[string]any{"invoice_id": inv.ID, "rail": "usdx", "deposit_address": addr, "pay_amount": amount, "pay_ccy": "USDX", "confs_required": s.cfg.escrowConfs})

	case "btc":
		if s.cfg.btcURL == "" {
			writeErr(w, 503, "the BTC rail is not available on this deployment")
			return
		}
		btcUSD := btcPrice(s.fetchPrices())
		if btcUSD <= 0 {
			writeErr(w, 502, "no BTC/USD rate is available")
			return
		}
		q, err := s.feeQuoteFor(inv, "btc", uint64(math.Ceil(inv.AmountUSD/btcUSD*1e8)), "BTC",
			s.newBTCDepositAddress)
		if err != nil {
			writeErr(w, 502, "could not derive a fee deposit address: %v", err)
			return
		}
		addr, amount := q.Address, q.Amount
		audit("fee.pay.btc", map[string]any{"address": addr, "amount": amount})
		writeJSON(w, 200, map[string]any{"invoice_id": inv.ID, "rail": "btc", "deposit_address": addr, "pay_amount": amount, "pay_ccy": "BTC", "confs_required": s.cfg.escrowConfs})

	default:
		writeErr(w, 400, "rail must be one of usdx, btc, card, bank")
	}
}

// feeAddressFor returns the deposit address this invoice already has for that
// rail, deriving one only the first time. Quoting the same rail twice used to
// burn a fresh address and forget the previous one, so a payer who had already
// sent to it was never credited; every address this invoice hands out stays
// watched.
func (s *server) feeQuoteFor(inv *FeeInvoice, rail string, amount uint64, ccy string,
	derive func() (string, error)) (FeeQuote, error) {
	q, ok := inv.Quotes[rail]
	if !ok || q.Address == "" {
		addr, err := derive()
		if err != nil {
			return FeeQuote{}, err
		}
		q = FeeQuote{Address: addr}
	}
	// The amount is re-quoted every time -- a BTC price moves -- but the address
	// does not change, and the watcher credits on the smallest amount this rail
	// was ever quoted at, so a payer who acted on an earlier quote is not short.
	if q.Amount == 0 || amount < q.Amount {
		q.Amount = amount
	}
	q.Ccy = ccy
	if err := s.st.SetFeeQuote(inv, rail, q); err != nil {
		return FeeQuote{}, err
	}
	return q, nil
}

// onFiatFeeSettled marks a fee invoice paid when its SIMULATED checkout settles.
func (s *server) onFiatFeeSettled(p *FiatPayment) {
	inv, err := s.st.FeeInvoiceByID(p.InvoiceID)
	if err != nil || inv == nil || inv.State == "paid" {
		return
	}
	s.recordFeePaid(inv, p.Receipt, p.AmountMinor, p.Ccy, true)
}

// recordFeePaid marks one fee invoice paid and books it.
//
// The escrow ledger is per-offering: every row belongs to an issuance, and the
// readers group by it. A verification fee belongs to a person and to no
// offering, so it is not written there -- an orphan row with an empty issuance
// id would be in the books without being in anybody's books. Its record is the
// invoice itself, which carries the rail, the amount and the settling txid, and
// the audit entry below.
func (s *server) recordFeePaid(inv *FeeInvoice, txid string, amount uint64, ccy string, simulated bool) {
	_ = s.st.UpdateFeeInvoiceFields(inv.ID, map[string]any{
		"state": "paid", "txid": txid, "paid_at": time.Now().Unix(),
	})
	if inv.IssuanceID != "" {
		_ = s.st.InsertLedger(&LedgerEntry{
			IssuanceID: inv.IssuanceID, Kind: inv.Kind + "_fee", Rail: inv.Rail,
			Amount: amount, Ccy: ccy, Txid: txid, FundsSimulated: simulated,
		})
	}
	fields := map[string]any{
		"invoice": inv.ID, "kind": inv.Kind, "rail": inv.Rail,
		"amount": amount, "ccy": ccy, "simulated": simulated,
	}
	if inv.IssuanceID != "" {
		fields["issuance_id"] = inv.IssuanceID
	}
	if inv.Subject != "" {
		fields["entity_id"] = inv.Subject
	}
	if txid != "" {
		fields["txid"] = txid
	}
	s.st.Audit(inv.AID, "fee.paid", fields)
}

// onFeeDepositConfirmed marks an on-chain fee invoice paid when its deposit
// address is credited at N confirmations (called by the deposit watcher).
func (s *server) onFeeDepositConfirmed(inv *FeeInvoice, rail string, q FeeQuote, txid string, atoms uint64) {
	if inv.State == "paid" {
		return
	}
	// Booked on the rail that actually paid, which need not be the last one
	// quoted: an invoice quoted on two rails is settled by whichever one arrives.
	inv.Rail, inv.Address, inv.Ccy = rail, q.Address, q.Ccy
	_ = s.st.UpdateFeeInvoiceFields(inv.ID, map[string]any{
		"rail": rail, "address": q.Address, "ccy": q.Ccy,
	})
	s.recordFeePaid(inv, txid, atoms, q.Ccy, false)
}

// --- issuer payout mandates --------------------------------------------------

type mandateReq struct {
	Chain       string `json:"chain"` // sequentia | bitcoin
	Asset       string `json:"asset"`
	Address     string `json:"address"`
	Signature   string `json:"signature"`
	SignerXOnly string `json:"signer_xonly"`
}

// handleMandate is POST /issuances/{id}/mandate (owner): register a BIP340-signed
// payout mandate. The address is validated for the correct network; an enclave
// key-path address is rejected; the signature is verified over the canonical
// mandate statement by the issuer's enclave key.
func (s *server) handleMandate(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	var req mandateReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	chain := strings.ToLower(strings.TrimSpace(req.Chain))
	addr := strings.TrimSpace(req.Address)
	switch chain {
	case "sequentia":
		if err := s.validateSeqAddress(addr); err != nil {
			writeErr(w, 400, "address is not a valid Sequentia address: %v", err)
			return
		}
	case "bitcoin":
		if err := s.validateBTCAddress(addr); err != nil {
			writeErr(w, 400, "address is not a valid testnet4 Bitcoin address: %v", err)
			return
		}
	default:
		writeErr(w, 400, "chain must be sequentia or bitcoin")
		return
	}

	// Reject an enclave key-path address: escrow release must pay an ordinary
	// wallet-scanned address, never a 2-of-2 enclave script no wallet monitors.
	isEnclave, err := s.isEnclaveAddress(iss, addr)
	if err != nil {
		// Fail closed, exactly as the investor-side check does: an address we
		// cannot confirm is NOT an enclave address might be one, and paying one
		// strands the money at a script no wallet scans.
		writeErr(w, 503, "cannot verify the payout address right now; please retry")
		return
	}
	if isEnclave {
		writeErr(w, 400, "the payout address is an enclave key-path address; provide an ordinary wallet address")
		return
	}

	// Verify the mandate signature by the issuer's enclave key.
	if named := strings.TrimSpace(req.SignerXOnly); named != "" && !strings.EqualFold(named, acct.XOnly) {
		writeErr(w, 403, "the mandate must be signed by the issuer's own SeqPal ID key")
		return
	}
	signer := acct.XOnly
	statement, _ := canonicalJSON(map[string]any{
		"issuance_id": iss.ID, "chain": chain, "asset": req.Asset, "address": addr,
	})
	if strings.TrimSpace(req.Signature) == "" {
		// No signature yet: return the exact bytes to sign (the SPA / wallet signs).
		writeJSON(w, 200, withNote(mandateTag, statement,
			"sign these canonical bytes with your SeqPal ID key, then resubmit with signature; an "+
				"ordinary wallet signs sign_this_message as a message instead"))
		return
	}
	verifiedBy, verr := s.verifyAccountStatementBy(acct, mandateTag, statement, req.Signature)
	if verr != nil {
		writeErr(w, 400, "the mandate signature does not verify for your key")
		return
	}
	m := &PayoutMandate{
		IssuanceID: iss.ID, Chain: chain, Asset: req.Asset, Address: addr,
		Signature: req.Signature, SignerXOnly: signer, SignerAddress: verifiedBy,
	}
	if err := s.st.UpsertMandate(m); err != nil {
		writeErr(w, 500, "store error")
		return
	}
	s.st.Audit(acct.AID, "mandate.register", map[string]any{"issuance_id": iss.ID, "chain": chain, "address": addr})
	writeJSON(w, 200, map[string]any{"mandate": m})
}

// isEnclaveAddress reports whether addr is one of the offering's enclave key-path
// addresses (the minted-asset enclave receive address, or a provisioned escrow /
// treasury enclave address), which a mandate must never target.
func (s *server) isEnclaveAddress(iss *Issuance, addr string) (bool, error) {
	if addr == "" {
		return false, nil
	}
	if iss.EnclaveAddress != "" && iss.EnclaveAddress == addr {
		return true, nil
	}
	// The per-offering escrow enclave and (if entity-backed) treasury enclave
	// receive addresses for the asset. An address that cannot be resolved is not
	// an address that differs: the caller fails closed on it, the same way the
	// investor-side check always has. These enclaves are accounts this platform
	// registered itself, so an unresolvable one is an outage rather than an
	// account the policy server was never told about.
	var probeErr error
	for _, k := range s.offeringEnclaves(iss) {
		var out struct {
			Address string `json:"address"`
		}
		if err := s.callOpenAMP("GET", "/v1/users/"+k.AID+"/address?asset="+iss.AssetID, "", nil, &out); err != nil {
			probeErr = fmt.Errorf("resolve enclave %s: %w", k.AID, err)
			continue
		}
		if out.Address != "" && out.Address == addr {
			return true, nil
		}
	}
	return false, probeErr
}

// offeringEnclaves returns the escrow and (if any) treasury enclave keys for an
// issuance.
func (s *server) offeringEnclaves(iss *Issuance) []*EnclaveKey {
	var out []*EnclaveKey
	if k, _ := s.st.EnclaveKeyByRef(enclaveOfferingEscrow, iss.ID); k != nil {
		out = append(out, k)
	}
	if iss.EntityID != "" {
		if k, _ := s.st.EnclaveKeyByRef(enclaveEntityTreasury, iss.EntityID); k != nil {
			out = append(out, k)
		}
	}
	return out
}

// handleMandates is GET /issuances/{id}/mandate (owner): the registered mandates.
func (s *server) handleMandates(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	ms, err := s.st.MandatesByIssuance(iss.ID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	writeJSON(w, 200, map[string]any{"issuance_id": iss.ID, "mandates": ms})
}
