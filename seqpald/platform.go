package main

import (
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
	rail := strings.ToLower(strings.TrimSpace(req.Rail))
	switch rail {
	case "card", "bank":
		pay, err := s.startFiatFeePayment(inv.ID, rail, inv.AmountUSD)
		if err != nil {
			writeErr(w, 500, "could not start the simulated checkout")
			return
		}
		_ = s.st.UpdateFeeInvoiceFields(inv.ID, map[string]any{"rail": rail, "funds_simulated": 1, "ccy": "USD", "amount": inv.Amount})
		s.st.Audit(acct.AID, "fee.pay.fiat", map[string]any{"issuance_id": iss.ID, "invoice": inv.ID, "rail": rail, "simulated": true})
		writeJSON(w, 200, map[string]any{"invoice": inv, "checkout": fiatView(pay), "funds_simulated": true})

	case "usdx":
		addr, err := s.newUSDXDepositAddress()
		if err != nil {
			writeErr(w, 502, "could not derive a fee deposit address: %v", err)
			return
		}
		amount := usdToAtoms(inv.AmountUSD)
		_ = s.st.UpdateFeeInvoiceFields(inv.ID, map[string]any{"rail": "usdx", "address": addr, "amount": amount, "ccy": "USDX"})
		s.st.Audit(acct.AID, "fee.pay.usdx", map[string]any{"issuance_id": iss.ID, "invoice": inv.ID, "address": addr, "amount": amount})
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
		addr, err := s.newBTCDepositAddress()
		if err != nil {
			writeErr(w, 502, "could not derive a fee deposit address: %v", err)
			return
		}
		amount := uint64(math.Ceil(inv.AmountUSD / btcUSD * 1e8))
		_ = s.st.UpdateFeeInvoiceFields(inv.ID, map[string]any{"rail": "btc", "address": addr, "amount": amount, "ccy": "BTC"})
		s.st.Audit(acct.AID, "fee.pay.btc", map[string]any{"issuance_id": iss.ID, "invoice": inv.ID, "address": addr, "amount": amount})
		writeJSON(w, 200, map[string]any{"invoice_id": inv.ID, "rail": "btc", "deposit_address": addr, "pay_amount": amount, "pay_ccy": "BTC", "confs_required": s.cfg.escrowConfs})

	default:
		writeErr(w, 400, "rail must be one of usdx, btc, card, bank")
	}
}

// onFiatFeeSettled marks a fee invoice paid when its SIMULATED checkout settles.
func (s *server) onFiatFeeSettled(p *FiatPayment) {
	inv, err := s.st.FeeInvoiceByID(p.InvoiceID)
	if err != nil || inv == nil || inv.State == "paid" {
		return
	}
	_ = s.st.UpdateFeeInvoiceFields(inv.ID, map[string]any{"state": "paid", "txid": p.Receipt, "paid_at": time.Now().Unix()})
	_ = s.st.InsertLedger(&LedgerEntry{
		IssuanceID: inv.IssuanceID, Kind: "setup_fee", Rail: inv.Rail,
		Amount: p.AmountMinor, Ccy: p.Ccy, Txid: p.Receipt, FundsSimulated: true,
	})
	s.st.Audit("", "fee.paid", map[string]any{"issuance_id": inv.IssuanceID, "invoice": inv.ID, "rail": inv.Rail, "simulated": true})
}

// onFeeDepositConfirmed marks an on-chain fee invoice paid when its deposit
// address is credited at N confirmations (called by the deposit watcher).
func (s *server) onFeeDepositConfirmed(inv *FeeInvoice, txid string, atoms uint64) {
	if inv.State == "paid" {
		return
	}
	_ = s.st.UpdateFeeInvoiceFields(inv.ID, map[string]any{"state": "paid", "txid": txid, "paid_at": time.Now().Unix()})
	_ = s.st.InsertLedger(&LedgerEntry{
		IssuanceID: inv.IssuanceID, Kind: "setup_fee", Rail: inv.Rail,
		Amount: atoms, Ccy: inv.Ccy, Txid: txid, FundsSimulated: false,
	})
	s.st.Audit("", "fee.paid", map[string]any{"issuance_id": inv.IssuanceID, "invoice": inv.ID, "rail": inv.Rail, "txid": txid})
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
	if s.isEnclaveAddress(iss, addr) {
		writeErr(w, 400, "the payout address is an enclave key-path address; provide an ordinary wallet address")
		return
	}

	// Verify the mandate signature by the issuer's enclave key.
	signer := strings.TrimSpace(req.SignerXOnly)
	if signer == "" {
		signer = acct.XOnly
	}
	if signer != acct.XOnly {
		writeErr(w, 403, "the mandate must be signed by the issuer's own SeqPal ID key")
		return
	}
	statement, _ := canonicalJSON(map[string]any{
		"issuance_id": iss.ID, "chain": chain, "asset": req.Asset, "address": addr,
	})
	if strings.TrimSpace(req.Signature) == "" {
		// No signature yet: return the exact bytes to sign (the SPA / wallet signs).
		writeJSON(w, 200, map[string]any{
			"sign_this": string(statement), "tag": mandateTag,
			"note": "sign these canonical bytes with your SeqPal ID key, then resubmit with signature",
		})
		return
	}
	if err := verifyTaggedByKey(signer, mandateTag, statement, req.Signature); err != nil {
		writeErr(w, 400, "the mandate signature does not verify for your key")
		return
	}
	m := &PayoutMandate{
		IssuanceID: iss.ID, Chain: chain, Asset: req.Asset, Address: addr,
		Signature: req.Signature, SignerXOnly: signer,
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
func (s *server) isEnclaveAddress(iss *Issuance, addr string) bool {
	if addr == "" {
		return false
	}
	if iss.EnclaveAddress != "" && iss.EnclaveAddress == addr {
		return true
	}
	// The per-offering escrow enclave and (if entity-backed) treasury enclave
	// receive addresses for the asset.
	for _, k := range s.offeringEnclaves(iss) {
		var out struct {
			Address string `json:"address"`
		}
		if err := s.callOpenAMP("GET", "/v1/users/"+k.AID+"/address?asset="+iss.AssetID, "", nil, &out); err == nil {
			if out.Address != "" && out.Address == addr {
				return true
			}
		}
	}
	return false
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
