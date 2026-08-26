package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
)

// Subscriptions: POST /issuances/{id}/subscribe {rail, amount, refund_address}.
// The investor must be a verified, eligible SeqPal ID. The endpoint validates
// eligibility, runs the offer-side gates for the investor's jurisdiction, records
// a subscription, and returns the per-rail deposit address (usdx/btc) or starts
// the SIMULATED fiat checkout (card/bank). States: created -> in_escrow -> settled
// | refunded. The watcher advances in_escrow at N confirmations; nothing is
// settled until close.

type subscribeReq struct {
	Rail          string  `json:"rail"`   // usdx | btc | card | bank
	Amount        float64 `json:"amount"` // whole tokens to purchase
	RefundAddress string  `json:"refund_address"`
}

func (s *server) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss, err := s.st.IssuanceByID(r.PathValue("id"))
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if iss == nil {
		writeErr(w, 404, "unknown issuance")
		return
	}
	if iss.Status != "live" || iss.AssetID == "" {
		writeErr(w, 409, "this offering is not yet live on chain")
		return
	}
	if o, _ := s.st.OfferingByIssuance(iss.ID); o != nil && !o.OfferOpen {
		writeErr(w, 409, "the offer window for this issuance is closed")
		return
	}
	// A subscription is a promise of delivery, and for a token whose rules the
	// network enforces the delivery goes to the investor's own holding address and
	// only the issuer's own wallet can sign it. Taking money for a delivery this
	// platform cannot make would be the worst possible silence, so it refuses here,
	// before an escrow address exists.
	if s.refuseForNetwork(w, acct.AID, iss, "subscribe",
		"This token's rules are enforced by the network, and this platform does not run its initial sale: the tokens are delivered to each investor's own holding address, which only the issuer's own wallet can sign. Subscriptions are open for tokens whose transfers this platform services.") {
		return
	}
	if s.refuseForBearer(w, acct.AID, iss, "subscribe",
		"This token is freely tradable: it is an ordinary on-chain holding with no escrow enclave to deliver it from, so this platform does not run its sale. Buy it from whoever holds it. Subscriptions are open for tokens whose transfers this platform services.") {
		return
	}
	// Only now is a missing OpenAMP account the actual problem. Asked earlier, it
	// would answer a question about the investor when the answer is about the
	// token: a serviced token is delivered into an enclave, and this ID has none.
	if !s.hasEnclave(acct) {
		writeErr(w, 403, "this token is delivered into an OpenAMP account and your SeqPal ID has "+
			"none attached. Attach one, or subscribe to a token whose delivery does not need it")
		return
	}

	var req subscribeReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	req.Rail = strings.ToLower(strings.TrimSpace(req.Rail))
	// A number of tokens, and nothing else. A negative one converts to a vast
	// unsigned value further down and gets refused for being "too large", which
	// tells the buyer the opposite of what they did wrong; NaN and the infinities
	// convert to something undefined.
	if !(req.Amount > 0) || math.IsInf(req.Amount, 0) || math.IsNaN(req.Amount) {
		writeErr(w, 400, "amount must be a positive number of tokens")
		return
	}

	// Verified, eligible investor.
	claims, _ := s.st.ClaimsByAID(acct.AID)
	if !eligibilityLive(claims, time.Now().Unix()) {
		writeErr(w, 403, "a verified SeqPal ID is required to subscribe")
		return
	}
	var user openampUser
	if err := s.callOpenAMP("GET", "/v1/users/"+s.enclaveAIDOf(acct), "", nil, &user); err != nil {
		writeErr(w, 502, "could not read your policy-server registration: %v", err)
		return
	}
	rules, ok, err := s.assetRules(iss.AssetID)
	if err != nil {
		writeErr(w, 502, "could not read the asset rules: %v", err)
		return
	}
	if !ok {
		writeErr(w, 409, "the offering asset is not resolvable on the policy server")
		return
	}
	if eligible, reasons := evalEligibility(user.Categories, user.Frozen, rules, s.tipHeight()); !eligible {
		s.st.Audit(acct.AID, "subscribe.refused", map[string]any{"issuance_id": iss.ID, "reasons": reasons})
		writeJSON(w, 403, map[string]any{"error": "not eligible to hold this asset", "reasons": reasons})
		return
	}

	// Promotion gate must be passed to subscribe (it is the offeree-counting event).
	granted, promoReqs, _, _ := s.promotionGate(iss, iss.Terms, acct, claims)
	if !granted {
		writeJSON(w, 403, map[string]any{"error": "complete the promotion gate first", "requirements": promoReqs})
		return
	}
	s.recordOffereeGrant(iss, acct, claims)

	// Pricing: the offering reference price is required for a real-money leg.
	price, _, hasPrice := offeringPrice(iss.Terms)
	if !hasPrice || price <= 0 {
		writeErr(w, 409, "this offering has no reference price; a price is required before subscriptions can be funded")
		return
	}
	subUSD := req.Amount * price // quote assumed USD-equivalent for the PoC

	// Subscribe-side gates (appropriateness/KID, SoF > USD 10k, US 506(c)).
	if gateReqs := s.subscribeGates(iss, iss.Terms, acct, claims, subUSD); len(gateReqs) > 0 {
		writeJSON(w, 403, map[string]any{"error": "additional steps are required before subscribing", "requirements": gateReqs})
		return
	}

	var tokenAtoms uint64
	if req.Amount == math.Trunc(req.Amount) {
		ta, ok := atomsFor(uint64(req.Amount), iss.Precision)
		if !ok {
			writeErr(w, 400, "amount is too large for this asset's precision")
			return
		}
		tokenAtoms = ta
	} else {
		ta := req.Amount * math.Pow10(iss.Precision)
		if ta < 1 {
			writeErr(w, 400, "amount is smaller than one atom at this asset's precision")
			return
		}
		// The whole-token branch checks this through atomsFor; this one checked
		// nothing, so a fractional amount whose atoms overflow a uint64 -- around
		// a hundred billion tokens at 8 decimals -- converted to an undefined
		// value and was ACCEPTED. The subscription, its price and its escrow
		// expectation were then all built on that number.
		if ta >= float64(math.MaxUint64) {
			writeErr(w, 400, "amount is too large for this asset's precision")
			return
		}
		tokenAtoms = uint64(math.Round(ta))
	}
	if tokenAtoms == 0 {
		writeErr(w, 400, "amount rounds to zero atoms")
		return
	}

	sub := &Subscription{
		ID:          mustID(),
		IssuanceID:  iss.ID,
		InvestorAID: acct.AID,
		Rail:        req.Rail,
		TokenAtoms:  tokenAtoms,
		State:       "created",
	}

	switch req.Rail {
	case "usdx":
		if s.cfg.nodeURL == "" {
			writeErr(w, 503, "the USDX rail is not available on this deployment")
			return
		}
		if err := s.validateSeqAddress(req.RefundAddress); err != nil {
			writeErr(w, 400, "refund_address is not a valid Sequentia address: %v", err)
			return
		}
		addr, err := s.newUSDXDepositAddress()
		if err != nil {
			writeErr(w, 502, "could not derive a USDX deposit address: %v", err)
			return
		}
		sub.DepositAddress = addr
		sub.RefundAddress = req.RefundAddress
		sub.PayAmount = usdToAtoms(subUSD)
		sub.PayCcy = "USDX"
		if err := s.st.InsertSubscription(sub); err != nil {
			writeErr(w, 500, "could not record the subscription")
			return
		}
		s.st.Audit(acct.AID, "subscribe.usdx", map[string]any{"issuance_id": iss.ID, "sub": sub.ID, "deposit_address": addr, "pay_atoms": sub.PayAmount})
		writeJSON(w, 200, map[string]any{"subscription": sub, "deposit_address": addr, "pay_amount": sub.PayAmount, "pay_ccy": "USDX", "confs_required": s.cfg.escrowConfs})

	case "btc":
		if s.cfg.btcURL == "" {
			writeErr(w, 503, "the native BTC rail is not available on this deployment")
			return
		}
		if err := s.validateBTCAddress(req.RefundAddress); err != nil {
			writeErr(w, 400, "refund_address is not a valid testnet4 Bitcoin address: %v", err)
			return
		}
		btcUSD := btcPrice(s.fetchPrices())
		if btcUSD <= 0 {
			writeErr(w, 502, "no BTC/USD rate is available from the price server")
			return
		}
		addr, err := s.newBTCDepositAddress()
		if err != nil {
			writeErr(w, 502, "could not derive a testnet4 deposit address: %v", err)
			return
		}
		sub.DepositAddress = addr
		sub.RefundAddress = req.RefundAddress
		sub.PayAmount = uint64(math.Ceil(subUSD / btcUSD * 1e8)) // expected sats
		sub.PayCcy = "BTC"
		sub.USDRate = btcUSD // provisional; the confirmed rate is re-captured at N confs
		if err := s.st.InsertSubscription(sub); err != nil {
			writeErr(w, 500, "could not record the subscription")
			return
		}
		// The BTC rail is registrar-style, so a post-delivery testnet4 reorg is a
		// residual risk. Clawback is the resolution if a reorged deposit does not
		// re-confirm; an offering without clawback carries the residual risk, stated
		// here and in the audit so it is never silent.
		reorgNote := "if this BTC deposit is reorged out after delivery, the account is frozen and, if the deposit does not re-confirm, the tokens are clawed back with reason"
		if !iss.Clawback {
			reorgNote = "this offering was issued WITHOUT clawback: if this BTC deposit is reorged out after delivery, the account is frozen but the delivered tokens cannot be clawed back, a residual reorg risk you accept on the native-BTC rail"
		}
		s.st.Audit(acct.AID, "subscribe.btc", map[string]any{"issuance_id": iss.ID, "sub": sub.ID, "deposit_address": addr, "pay_sats": sub.PayAmount, "rate": btcUSD, "clawback": iss.Clawback})
		writeJSON(w, 200, map[string]any{
			"subscription": sub, "deposit_address": addr, "pay_amount": sub.PayAmount, "pay_ccy": "BTC",
			"quoted_rate_usd_btc": btcUSD, "confs_required": s.cfg.escrowConfs,
			"registrar_note": "cross-chain registrar settlement: this BTC payment is confirmed on testnet4, then delivery is co-signed on Sequentia; it is not atomic",
			"reorg_note":     reorgNote,
		})

	case "card", "bank":
		sub.RefundAddress = strings.TrimSpace(req.RefundAddress) // optional for fiat
		sub.PayAmount = usdToMinor(subUSD)
		sub.PayCcy = "USD"
		sub.FundsSimulated = true
		if err := s.st.InsertSubscription(sub); err != nil {
			writeErr(w, 500, "could not record the subscription")
			return
		}
		checkout, err := s.startFiatCheckout(sub, req.Rail, subUSD)
		if err != nil {
			writeErr(w, 500, "could not start the simulated checkout: %v", err)
			return
		}
		s.st.Audit(acct.AID, "subscribe.fiat", map[string]any{"issuance_id": iss.ID, "sub": sub.ID, "rail": req.Rail, "amount_usd": subUSD, "simulated": true})
		writeJSON(w, 200, map[string]any{
			"subscription": sub, "checkout": checkout, "funds_simulated": true,
			"label": "SIMULATED payment: no real card or bank transfer is processed",
		})

	default:
		writeErr(w, 400, "rail must be one of usdx, btc, card, bank")
	}
}

// handleMySubscriptions is GET /subscriptions: the caller's own subscriptions.
func (s *server) handleMySubscriptions(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	subs, err := s.st.SubscriptionsByInvestor(acct.AID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	writeJSON(w, 200, map[string]any{"subscriptions": subs})
}

// handleIssuanceSubscriptions is GET /issuances/{id}/subscriptions (owner): the
// offering's subscription book and escrow ledger.
func (s *server) handleIssuanceSubscriptions(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	subs, err := s.st.SubscriptionsByIssuance(iss.ID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	ledger, _ := s.st.LedgerByIssuance(iss.ID)
	writeJSON(w, 200, map[string]any{"issuance_id": iss.ID, "subscriptions": subs, "ledger": ledger})
}

// --- pricing helpers ---------------------------------------------------------

// btcPrice returns BTC/USD from the price feed (BTC or the testnet ticker TBTC).
func btcPrice(prices map[string]float64) float64 {
	if p := prices["BTC"]; p > 0 {
		return p
	}
	return prices["TBTC"]
}

// usdToAtoms converts a USD figure to USDX atoms (USDX is treated 1:1 with USD at
// 8 decimals for the PoC).
func usdToAtoms(usd float64) uint64 {
	if usd <= 0 {
		return 0
	}
	return uint64(math.Round(usd * 1e8))
}

// usdToMinor converts a USD figure to minor units (cents).
func usdToMinor(usd float64) uint64 {
	if usd <= 0 {
		return 0
	}
	return uint64(math.Round(usd * 100))
}

// --- address validation ------------------------------------------------------

// validateSeqAddress checks a Sequentia (Elements) address is valid on the
// configured network via the node's validateaddress. It rejects an empty address
// and any address the node reports invalid, so a refund can only be captured for
// an address the correct network recognizes.
func (s *server) validateSeqAddress(addr string) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return fmt.Errorf("a refund address is required")
	}
	res, err := s.nodeRPC("validateaddress", addr)
	if err != nil {
		return fmt.Errorf("could not validate the address: %v", err)
	}
	return checkValidAddress(res)
}

// validateBTCAddress checks a testnet4 Bitcoin address via the parent-chain
// node's validateaddress.
func (s *server) validateBTCAddress(addr string) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return fmt.Errorf("a refund address is required")
	}
	res, err := s.btcRPC("", "validateaddress", addr)
	if err != nil {
		return fmt.Errorf("could not validate the address: %v", err)
	}
	return checkValidAddress(res)
}

func checkValidAddress(res json.RawMessage) error {
	var out struct {
		IsValid bool `json:"isvalid"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return err
	}
	if !out.IsValid {
		return fmt.Errorf("the address is not valid on this network")
	}
	return nil
}

func mustID() string {
	id, err := randHex(16)
	if err != nil {
		return fmt.Sprintf("sub-%d", time.Now().UnixNano())
	}
	return id
}
