package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

const (
	sessionCookie  = "seqpal_session"
	sessionPath    = "/seqpal"
	sessionTTL     = 14 * 24 * time.Hour
	challengeTTL   = 120 * time.Second
	challengeTag   = "openamp-challenge-v1"
	aidTag         = "openamp-aid-v1"
	maxRequestBody = 1 << 16
)

// aidFor mirrors openampd's store.AID exactly: sha256 over the tag followed by
// the lexicographically sorted x-only pubkey hex strings, first 20 bytes, hex.
// The client's AID is never trusted; it is recomputed here from the key that
// actually produced the signature.
func aidFor(pubkeys []string) string {
	sorted := append([]string(nil), pubkeys...)
	sort.Strings(sorted)
	h := sha256.New()
	h.Write([]byte(aidTag))
	for _, pk := range sorted {
		h.Write([]byte(pk))
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:20])
}

// taggedHash is BIP340's tagged hash: sha256(sha256(tag) || sha256(tag) || m).
func taggedHash(tag string, msg []byte) [32]byte {
	t := sha256.Sum256([]byte(tag))
	h := sha256.New()
	h.Write(t[:])
	h.Write(t[:])
	h.Write(msg)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// verifyChallengeSig verifies a BIP340 signature over the TAGGED challenge. The
// enclave key must never sign a raw externally supplied digest: tagging is what
// keeps a challenge from ever equalling a spendable transfer sighash
// (openamp spec/venue-wallet-integration.md 0.4).
func verifyChallengeSig(xonly, challenge, sigHex string) error {
	pkBytes, err := hex.DecodeString(xonly)
	if err != nil || len(pkBytes) != 32 {
		return fmt.Errorf("xonly must be a 32-byte hex key")
	}
	pub, err := schnorr.ParsePubKey(pkBytes)
	if err != nil {
		return fmt.Errorf("xonly is not a valid x-only public key")
	}
	sigBytes, err := hex.DecodeString(sigHex)
	if err != nil || len(sigBytes) != 64 {
		return fmt.Errorf("sig must be a 64-byte hex BIP340 signature")
	}
	sig, err := schnorr.ParseSignature(sigBytes)
	if err != nil {
		return fmt.Errorf("sig is not a valid BIP340 signature")
	}
	msg := taggedHash(challengeTag, []byte(challenge))
	if !sig.Verify(msg[:], pub) {
		return fmt.Errorf("signature does not verify for this key and challenge")
	}
	return nil
}

func validXOnly(xonly string) bool {
	if len(xonly) != 64 || strings.ToLower(xonly) != xonly {
		return false
	}
	b, err := hex.DecodeString(xonly)
	if err != nil {
		return false
	}
	_, err = schnorr.ParsePubKey(b)
	return err == nil
}

type challengeReq struct {
	XOnly string `json:"xonly"`
}

func (s *server) handleChallenge(w http.ResponseWriter, r *http.Request) {
	var req challengeReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	if !validXOnly(req.XOnly) {
		writeErr(w, 400, "xonly must be a valid 32-byte x-only public key in lowercase hex")
		return
	}
	challenge, exp, err := s.st.CreateChallenge(req.XOnly, challengeTTL)
	if err != nil {
		writeErr(w, 500, "could not issue a challenge")
		return
	}
	writeJSON(w, 200, map[string]any{"challenge": challenge, "expires_at": exp})
}

type authReq struct {
	XOnly       string          `json:"xonly"`
	Challenge   string          `json:"challenge"`
	Sig         string          `json:"sig"`
	Kind        string          `json:"kind"`
	DisplayName string          `json:"display_name"`
	Residence   string          `json:"residence"`
	IDNumber    string          `json:"id_number"`
	Profile     json.RawMessage `json:"profile"`
}

// authenticate verifies the presented proof of possession and returns the AID
// the key actually maps to.
func (s *server) authenticate(req *authReq) (string, error) {
	if !validXOnly(req.XOnly) {
		return "", fmt.Errorf("xonly must be a valid 32-byte x-only public key in lowercase hex")
	}
	if err := s.st.ConsumeChallenge(req.Challenge, req.XOnly); err != nil {
		return "", err
	}
	if err := verifyChallengeSig(req.XOnly, req.Challenge, req.Sig); err != nil {
		return "", err
	}
	return aidFor([]string{req.XOnly}), nil
}

func (s *server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req authReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	aid, err := s.authenticate(&req)
	if err != nil {
		s.st.Audit("", "auth.register.refused", map[string]any{"xonly": req.XOnly, "reason": err.Error()})
		writeErr(w, 401, "%v", err)
		return
	}
	if existing, err := s.st.AccountByAID(aid); err != nil {
		writeErr(w, 500, "store error")
		return
	} else if existing != nil {
		writeErr(w, 409, "an account already exists for this key; sign in instead")
		return
	}
	kind := req.Kind
	if kind != "individual" && kind != "corporate" {
		kind = "individual"
	}
	profile := req.Profile
	if len(profile) == 0 {
		profile, _ = json.Marshal(map[string]any{"residence": req.Residence})
	}
	acct := &Account{
		AID:         aid,
		Kind:        kind,
		XOnly:       req.XOnly,
		DisplayName: strings.TrimSpace(req.DisplayName),
		IDNumber:    strings.TrimSpace(req.IDNumber),
		Profile:     profile,
		CreatedAt:   time.Now().Unix(),
	}
	if err := s.st.CreateAccount(acct); err != nil {
		writeErr(w, 409, "could not create the account: %v", err)
		return
	}
	s.st.Audit(aid, "auth.register", map[string]any{"kind": acct.Kind, "display_name": acct.DisplayName})
	s.openSession(w, acct)
	writeJSON(w, 200, map[string]any{"account": acct})
}

func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req authReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	aid, err := s.authenticate(&req)
	if err != nil {
		s.st.Audit("", "auth.login.refused", map[string]any{"xonly": req.XOnly, "reason": err.Error()})
		writeErr(w, 401, "%v", err)
		return
	}
	acct, err := s.st.AccountByAID(aid)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if acct == nil {
		writeErr(w, 404, "no account exists for this key; register first")
		return
	}
	s.st.Audit(aid, "auth.login", map[string]any{})
	s.openSession(w, acct)
	writeJSON(w, 200, map[string]any{"account": acct})
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.st.DeleteSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     sessionPath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	s.st.Audit(acct.AID, "auth.logout", map[string]any{})
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *server) openSession(w http.ResponseWriter, acct *Account) {
	token, exp, err := s.st.CreateSession(acct.AID, sessionTTL)
	if err != nil {
		writeErr(w, 500, "could not open a session")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     sessionPath,
		Expires:  time.Unix(exp, 0),
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

// principal is the account behind the current session; requireSession has
// already established it, so handlers can use it without a nil check.
type ctxKey struct{}

func principal(r *http.Request) *Account {
	acct, _ := r.Context().Value(ctxKey{}).(*Account)
	return acct
}

func withPrincipal(r *http.Request, acct *Account) context.Context {
	return context.WithValue(r.Context(), ctxKey{}, acct)
}

func (s *server) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			writeErr(w, 401, "sign in first")
			return
		}
		acct, err := s.st.SessionAccount(c.Value)
		if err != nil {
			writeErr(w, 500, "store error")
			return
		}
		if acct == nil {
			writeErr(w, 401, "your session has expired; sign in again")
			return
		}
		next(w, r.WithContext(withPrincipal(r, acct)))
	}
}
