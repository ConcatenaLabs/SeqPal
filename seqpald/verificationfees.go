package main

import (
	"net/http"
	"strings"
	"time"
)

// Verification is not free to SeqPal: the identity-verification provider charges
// per check, whether the check clears or refuses. The fee is owed by the account
// that asks for the check, and it is collected BEFORE the check is submitted --
// after submission the cost is already incurred, so a fee collected afterwards
// would be a fee the platform can be refused.
//
// One paid invoice buys one check. A resubmission is the same check continuing
// (the provider asked for a better photo, not for a new applicant), so it does
// not raise a second fee. A refusal is terminal and cannot be resubmitted at all.

// What a check costs where a deployment does not say otherwise. These are the
// PUBLISHED prices, from src/data/pricing.js, and a test holds them to it: the
// pricing page is what an issuer reads before they arrive, so charging more
// than it says overcharges them.
const (
	defaultKYCFeeUSD = 20
	defaultKYBFeeUSD = 150
)

// verificationFeeKind maps a check kind to the fee kind that buys it.
func verificationFeeKind(checkKind string) string {
	if checkKind == "business" {
		return "kyb"
	}
	return "kyc"
}

// verificationFeeUSD is what a check of that fee kind costs the account.
func (s *server) verificationFeeUSD(feeKind string) float64 {
	if feeKind == "kyb" {
		return s.cfg.kybFeeUSD
	}
	return s.cfg.kycFeeUSD
}

// ensureVerificationFee returns the invoice for one check, raising it the first
// time it is asked for. A deployment that configures the fee at zero -- as a
// deployment with no provider bill to recoup would -- raises it already paid,
// so the gate below is a no-op rather than a thing to special-case.
func (s *server) ensureVerificationFee(aid, feeKind, subject string) (*FeeInvoice, error) {
	if inv, err := s.st.AccountFee(aid, feeKind, subject); err != nil {
		return nil, err
	} else if inv != nil {
		return inv, nil
	}
	inv := &FeeInvoice{
		ID: mustID(), AID: aid, Subject: subject, Kind: feeKind,
		AmountUSD: s.verificationFeeUSD(feeKind), State: "unpaid",
	}
	if inv.AmountUSD <= 0 {
		inv.State = "paid"
		inv.PaidAt = time.Now().Unix()
	}
	if err := s.st.InsertFeeInvoice(inv); err != nil {
		// Lost a race with a concurrent raise: the page that quotes this fee
		// polls, and several cards poll at once. The unique index is what makes
		// one of them lose rather than both winning and the holder paying an
		// invoice the gate does not read.
		if existing, qerr := s.st.AccountFee(aid, feeKind, subject); qerr == nil && existing != nil {
			return existing, nil
		}
		return nil, err
	}
	return inv, nil
}

// requireVerificationFee is the submit gate. It writes the 402 itself and reports
// whether the caller may proceed, so a handler reads as one line.
func (s *server) requireVerificationFee(w http.ResponseWriter, aid, checkKind, subject string) bool {
	inv, err := s.ensureVerificationFee(aid, verificationFeeKind(checkKind), subject)
	if err != nil {
		writeErr(w, 500, "store error")
		return false
	}
	if inv.State != "paid" {
		writeJSON(w, 402, map[string]any{
			"error":   "this verification is not paid for yet",
			"invoice": inv,
		})
		return false
	}
	return true
}

// handleVerificationFees is GET /api/id/fees (session): what the holder owes for
// their own identity check, and for each business they are having verified.
func (s *server) handleVerificationFees(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	identity, err := s.ensureVerificationFee(acct.AID, "kyc", "")
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	ents, err := s.st.EntitiesByOwner(acct.AID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	businesses := []map[string]any{}
	for _, e := range ents {
		inv, err := s.ensureVerificationFee(acct.AID, "kyb", e.ID)
		if err != nil {
			writeErr(w, 500, "store error")
			return
		}
		businesses = append(businesses, map[string]any{
			"entity_id": e.ID, "name": e.Name, "invoice": inv,
		})
	}
	writeJSON(w, 200, map[string]any{
		"identity":   identity,
		"businesses": businesses,
		"btc_rail":   s.cfg.btcURL != "",
	})
}

type payVerificationFeeReq struct {
	Kind     string `json:"kind"`      // identity | business
	EntityID string `json:"entity_id"` // required for a business check
	Rail     string `json:"rail"`
}

// handlePayVerificationFee is POST /api/id/fees/pay (session): collect the fee
// for one check on the holder's chosen rail, on the same rails as every other
// platform fee.
func (s *server) handlePayVerificationFee(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	var req payVerificationFeeReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if kind == "" {
		kind = "identity"
	}
	if kind != "identity" && kind != "business" {
		writeErr(w, 400, "kind must be identity or business")
		return
	}
	subject := ""
	if kind == "business" {
		// Charge only for a business this account actually owns: the subject is
		// what the invoice buys, and an unowned one would let a caller park a
		// payment against someone else's check.
		ent := s.ownedEntity(w, acct, strings.TrimSpace(req.EntityID))
		if ent == nil {
			return
		}
		subject = ent.ID
	}
	inv, err := s.ensureVerificationFee(acct.AID, verificationFeeKind(kind), subject)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if inv.State == "paid" {
		writeJSON(w, 200, map[string]any{"invoice": inv, "already_paid": true})
		return
	}
	ctx := map[string]any{"check_kind": kind}
	if subject != "" {
		ctx["entity_id"] = subject
	}
	s.payInvoiceOnRail(w, acct, inv, req.Rail, ctx)
}
