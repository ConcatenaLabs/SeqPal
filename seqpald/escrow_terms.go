package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
)

// An offering's escrow terms: everything every subscription address of the
// offering is derived from, bar the two parameters that are each
// subscription's own (its refund script and its SUBSCRIPTION reference).
//
// The issuer opens the escrow once, before the first subscription, and the
// terms are then fixed for good: a coin already paid to an address derived
// from them can only ever be spent as those terms say, so changing them would
// orphan it. The issuer names
//
//   - the release key: an x-only key from its OWN wallet, which signs the
//     release after the close and the cancel of a subscription. This platform
//     never holds it. The issuer proves it holds it by signing the terms with
//     it, as a tagged statement or as an ordinary signed message;
//   - the minimum: the least the release may pay it, in the payment asset;
//   - the close: a Sequentia block height (or a unix time) from which it may
//     release.
//
// The rest follows: the payment asset is the rail's, the payout script is the
// issuer's registered Sequentia payout mandate, the deadline is the close plus
// a fixed margin (SEQPALD_ESCROW_DEADLINE_DAYS, 14 days by default) from which
// anyone may refund a subscriber, and the fee cap
// (SEQPALD_ESCROW_FEE_CAP_ATOMS) bounds what a refund keeps back for its
// network fee.

const escrowTermsTag = "seqpal-escrow-terms-v1"

const (
	defaultEscrowDeadlineDays = 14
	defaultEscrowFeeCapAtoms  = 100000
)

func (s *server) escrowDeadlineDays() int64 {
	if s.cfg.escrowDeadlineDays > 0 {
		return s.cfg.escrowDeadlineDays
	}
	return defaultEscrowDeadlineDays
}

func (s *server) escrowFeeCap() uint64 {
	if s.cfg.escrowFeeCapAtoms > 0 {
		return s.cfg.escrowFeeCapAtoms
	}
	return defaultEscrowFeeCapAtoms
}

type EscrowTerms struct {
	IssuanceID    string `json:"issuance_id"`
	PayAsset      string `json:"pay_asset"`
	ReleaseKey    string `json:"release_key"`
	IssuerAddress string `json:"issuer_address"`
	IssuerSPK     string `json:"issuer_script_pubkey"`
	MinimumAtoms  uint64 `json:"minimum_atoms"`
	CloseLock     uint32 `json:"close"`
	DeadlineLock  uint32 `json:"deadline"`
	FeeCap        uint64 `json:"fee_cap"`
	TemplateHash  string `json:"template_hash"`
	Signature     string `json:"signature,omitempty"`
	CreatedAt     int64  `json:"created_at"`
}

func (s *Store) InsertEscrowTerms(t *EscrowTerms) error {
	now := time.Now().Unix()
	t.CreatedAt = now
	_, err := s.db.Exec(`INSERT INTO escrow_terms (issuance_id, pay_asset, release_key, issuer_address,
        issuer_spk, minimum_atoms, close_lock, deadline_lock, fee_cap, template_hash, signature, created_at, updated_at)
        VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.IssuanceID, t.PayAsset, t.ReleaseKey, t.IssuerAddress, t.IssuerSPK, t.MinimumAtoms, t.CloseLock,
		t.DeadlineLock, t.FeeCap, t.TemplateHash, t.Signature, now, now)
	return err
}

func (s *Store) EscrowTermsByIssuance(issuanceID string) (*EscrowTerms, error) {
	var t EscrowTerms
	err := s.db.QueryRow(`SELECT issuance_id, pay_asset, release_key, issuer_address, issuer_spk, minimum_atoms,
        close_lock, deadline_lock, fee_cap, template_hash, signature, created_at FROM escrow_terms WHERE issuance_id = ?`,
		issuanceID).Scan(&t.IssuanceID, &t.PayAsset, &t.ReleaseKey, &t.IssuerAddress, &t.IssuerSPK, &t.MinimumAtoms,
		&t.CloseLock, &t.DeadlineLock, &t.FeeCap, &t.TemplateHash, &t.Signature, &t.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Store) AllEscrowTerms() ([]*EscrowTerms, error) {
	rows, err := s.db.Query(`SELECT issuance_id FROM escrow_terms ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	out := []*EscrowTerms{}
	for _, id := range ids {
		t, err := s.EscrowTermsByIssuance(id)
		if err != nil {
			return nil, err
		}
		if t != nil {
			out = append(out, t)
		}
	}
	return out, nil
}

// escrowTermsStatement is what the release key signs: the whole of the terms,
// so the signature is the issuer's acknowledgment of every one of them, bound
// to this offering and to this template.
func escrowTermsStatement(t *EscrowTerms) []byte {
	b, _ := canonicalJSON(map[string]any{
		"action":         "open-subscription-escrow",
		"issuance_id":    t.IssuanceID,
		"template":       escrowTemplateName,
		"template_hash":  t.TemplateHash,
		"pay_asset":      t.PayAsset,
		"release_key":    t.ReleaseKey,
		"issuer_address": t.IssuerAddress,
		"minimum_atoms":  t.MinimumAtoms,
		"close":          t.CloseLock,
		"deadline":       t.DeadlineLock,
		"fee_cap":        t.FeeCap,
	})
	return b
}

type escrowTermsReq struct {
	ReleaseKey   string  `json:"release_key"`
	MinimumAtoms uint64  `json:"minimum_atoms"`
	Minimum      float64 `json:"minimum"` // whole units of the payment asset
	CloseHeight  int64   `json:"close_height"`
	CloseTime    int64   `json:"close_time"`
	Signature    string  `json:"signature"`
}

// escrowDeadlineFor is the close plus the documented margin, in the close's
// own unit.
func (s *server) escrowDeadlineFor(close uint32) (uint32, error) {
	days := s.escrowDeadlineDays()
	var d uint64
	if close < locktimeThreshold {
		d = uint64(close) + uint64(days)*uint64(s.cfg.blocksPerDay)
		if d >= locktimeThreshold {
			return 0, fmt.Errorf("the deadline would pass the largest block height a lock can name")
		}
	} else {
		d = uint64(close) + uint64(days)*86400
		if d > math.MaxUint32 {
			return 0, fmt.Errorf("the deadline would pass the largest time a lock can name")
		}
	}
	return uint32(d), nil
}

// handleEscrowTerms is POST /api/issuances/{id}/escrow (owner): open the
// offering's subscription escrow. Without a signature it returns the terms
// and the statement to sign with the release key; with one, it records them.
func (s *server) handleEscrowTerms(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	if s.cfg.nodeURL == "" || s.cfg.usdxAsset == "" {
		writeErr(w, 503, "the Sequentia payment rail is not available on this deployment")
		return
	}
	var req escrowTermsReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	defer s.st.LockIssuance(iss.ID)()
	existing, err := s.st.EscrowTermsByIssuance(iss.ID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if existing != nil {
		writeJSON(w, 200, map[string]any{"escrow": s.escrowTermsView(existing),
			"note": "this offering's escrow is open and its terms are fixed: every subscription address is derived from them"})
		return
	}
	if _, err := escrowDescriptor(); err != nil {
		writeErr(w, 500, "%v", err)
		return
	}
	key := strings.ToLower(strings.TrimSpace(req.ReleaseKey))
	if !validXOnly(key) {
		writeErr(w, 400, "release_key must be the 32-byte x-only public key (64 hex) of a key in your own wallet")
		return
	}
	m, err := s.st.MandateFor(iss.ID, "sequentia")
	if err != nil || m == nil || m.Address == "" {
		writeErr(w, 409, "register your Sequentia payout mandate first: the release pays the raise to it")
		return
	}
	spk, err := s.addressSPK(m.Address)
	if err != nil {
		writeErr(w, 502, "could not read your payout address's script: %v", err)
		return
	}
	minimum := req.MinimumAtoms
	if minimum == 0 && req.Minimum > 0 {
		minimum = usdToAtoms(req.Minimum)
	}
	if minimum == 0 {
		writeErr(w, 400, "a minimum raise is required: the least the release may pay you, in the payment asset")
		return
	}
	tip, mtp, err := s.chainTip()
	if err != nil {
		writeErr(w, 502, "could not read the chain tip: %v", err)
		return
	}
	var close uint32
	switch {
	case req.CloseHeight > 0 && req.CloseTime > 0:
		writeErr(w, 400, "give the close as a block height or as a time, not both")
		return
	case req.CloseHeight > 0:
		if req.CloseHeight >= locktimeThreshold {
			writeErr(w, 400, "close_height must be below 500,000,000")
			return
		}
		if req.CloseHeight <= tip {
			writeErr(w, 400, "the close must be after the current block (%d)", tip)
			return
		}
		close = uint32(req.CloseHeight)
	case req.CloseTime > 0:
		if req.CloseTime < locktimeThreshold || req.CloseTime > math.MaxUint32 {
			writeErr(w, 400, "close_time must be a unix time")
			return
		}
		if req.CloseTime <= mtp {
			writeErr(w, 400, "the close must be in the future")
			return
		}
		close = uint32(req.CloseTime)
	default:
		writeErr(w, 400, "a close is required: close_height (a Sequentia block) or close_time (a unix time)")
		return
	}
	deadline, err := s.escrowDeadlineFor(close)
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	// The release and every refund pay their network fee in the payment asset,
	// out of the coins. A node that does not accept that asset for fees could
	// never mine any of them.
	if _, err := s.feeExchangeRate(s.cfg.usdxAsset); err != nil {
		writeErr(w, 409, "the payment asset is not accepted for fees by this node, so no release or refund "+
			"could pay its own network fee: %v", err)
		return
	}
	t := &EscrowTerms{
		IssuanceID: iss.ID, PayAsset: strings.ToLower(s.cfg.usdxAsset), ReleaseKey: key,
		IssuerAddress: m.Address, IssuerSPK: spk, MinimumAtoms: minimum, CloseLock: close,
		DeadlineLock: deadline, FeeCap: s.escrowFeeCap(), TemplateHash: escrowTemplateHash,
	}
	stmt := escrowTermsStatement(t)
	if strings.TrimSpace(req.Signature) == "" {
		out := withNote(escrowTermsTag, stmt,
			"sign these terms with the release key itself -- tagged, or as an ordinary signed message from "+
				"that key's address -- then resubmit the same request with signature. The terms are fixed "+
				"once recorded.")
		out["escrow"] = s.escrowTermsView(t)
		writeJSON(w, 200, out)
		return
	}
	if err := s.verifyKeyStatement(key, escrowTermsTag, stmt, req.Signature); err != nil {
		writeErr(w, 400, "the signature does not verify for the release key: %v", err)
		return
	}
	t.Signature = strings.TrimSpace(req.Signature)
	if err := s.st.InsertEscrowTerms(t); err != nil {
		writeErr(w, 500, "could not record the escrow terms")
		return
	}
	s.st.Audit(acct.AID, "escrow.open", map[string]any{
		"issuance_id": iss.ID, "release_key": key, "minimum_atoms": minimum, "close": close,
		"deadline": deadline, "fee_cap": t.FeeCap, "issuer_address": m.Address, "template_hash": escrowTemplateHash,
	})
	writeJSON(w, 200, map[string]any{"escrow": s.escrowTermsView(t)})
}

// handleEscrowStatus is GET /api/issuances/{id}/escrow (owner): the terms, the
// money at the escrow addresses, and the spends built over it.
func (s *server) handleEscrowStatus(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	t, err := s.st.EscrowTermsByIssuance(iss.ID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if t == nil {
		writeJSON(w, 200, map[string]any{"escrow": nil})
		return
	}
	spends, _ := s.st.EscrowSpendsByIssuance(iss.ID)
	views := make([]map[string]any, 0, len(spends))
	for _, sp := range spends {
		views = append(views, s.escrowSpendView(t, sp))
	}
	coins, _ := s.st.EscrowCoinsByIssuance(iss.ID)
	var held uint64
	for _, c := range coins {
		if c.SpentTxid == "" {
			held += c.Atoms
		}
	}
	writeJSON(w, 200, map[string]any{"escrow": s.escrowTermsView(t), "coins": coins, "held_atoms": held, "spends": views})
}

// escrowTermsView is the terms as a screen shows them.
func (s *server) escrowTermsView(t *EscrowTerms) map[string]any {
	return map[string]any{
		"template": escrowTemplateName, "template_hash": t.TemplateHash, "pay_asset": t.PayAsset,
		"release_key": t.ReleaseKey, "issuer_address": t.IssuerAddress, "minimum_atoms": t.MinimumAtoms,
		"close": t.CloseLock, "close_text": describeLock(t.CloseLock),
		"deadline": t.DeadlineLock, "deadline_text": describeLock(t.DeadlineLock),
		"fee_cap": t.FeeCap, "signed": t.Signature != "",
	}
}

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
