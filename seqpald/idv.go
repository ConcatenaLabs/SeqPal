package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// Identity verification, as a provider does it.
//
// Verification is not something this platform performs. It is performed by an
// identity-verification provider -- document and selfie checks, PEP and adverse
// media, watchlist screening -- and this platform consumes the decision. That is
// what production looks like, so it is what this looks like, with a simulated
// provider standing in until a real one is wired up.
//
// The shape matters more than the simulation. Real providers are ASYNCHRONOUS: a
// check is created, it runs for a while, and the result arrives later on a
// callback the provider signs. So verification here returns "submitted" and
// grants nothing; the adjudication arrives at /api/id/verify/callback and is what
// decides. Swapping in a real provider is an adapter and a signature check, not a
// change to the flow, the states, or the screens.
//
// This platform therefore runs no sanctions lists of its own. It did, and the
// screening was real, but a demo that verifies nobody's identity has no reason to
// hold a watchlist -- and keeping one would be a piece of production that
// production will not contain.

// idvDecision is what a provider concludes. The names are the ones this domain
// uses (Onfido's vocabulary is clear/consider/reject, with a separate
// resubmission request); anything a provider adds maps onto one of these.
type idvDecision string

const (
	// The applicant is who they say and is on no list: verified.
	idvClear idvDecision = "clear"
	// The provider will not pass them. There is no SeqPal reviewer to appeal to
	// -- adjudication belongs to the provider -- so this is final: refused.
	idvReject idvDecision = "reject"
	// The provider cannot conclude on what it was given and wants more. The
	// holder can submit again.
	idvResubmit idvDecision = "resubmission"
)

func (d idvDecision) valid() bool {
	switch d {
	case idvClear, idvReject, idvResubmit:
		return true
	}
	return false
}

// idvProvider creates checks. It does not return decisions: a decision arrives
// later, on the callback, which is the whole reason this interface is shaped
// this way.
type idvProvider interface {
	// Name is what goes on the record beside the decision, so a check can always
	// be traced to who made it.
	Name() string
	// CreateCheck submits an applicant and returns the provider's own reference
	// for it. The reference is what comes back on the callback.
	CreateCheck(check *VerificationCheck) (providerRef string, err error)
	// PollCheck asks the provider where a check stands, and is the backstop for
	// a callback that never arrived: delivery is over a network, and a decision
	// this platform never hears is a holder stuck at "submitted" with no way
	// out, having already paid for the check. decided is false while the
	// provider is still working, which is not an error.
	PollCheck(check *VerificationCheck) (decision idvDecision, reason string, decided bool, err error)
}

// --- the simulated provider --------------------------------------------------

// simulatedIDV stands in for a real provider on a deployment that has none. It
// decides deterministically by name, so the rejected and resubmission paths are
// demonstrable and tests are stable, and it delivers its decision the way a real
// provider does: by calling the callback, over HTTP, with the shared secret. The
// callback path is therefore exercised in the demo rather than bypassed, which
// is the part that has to be right when a real provider replaces this.
type simulatedIDV struct {
	callbackURL string
	secret      string
	delay       time.Duration
	client      *http.Client
}

func (p *simulatedIDV) Name() string { return "simulated" }

// PollCheck answers what the simulator would have delivered, once it would have
// delivered it. The decision is deterministic in the name, so asking is the same
// answer as the callback carried -- which is exactly the property that makes a
// reconciliation poll safe against a real provider too.
func (p *simulatedIDV) PollCheck(check *VerificationCheck) (idvDecision, string, bool, error) {
	if time.Since(time.Unix(check.CreatedAt, 0)) < p.delay {
		return "", "", false, nil
	}
	decision, reason := simulatedDecision(check.SubjectName)
	return decision, reason, true, nil
}

func (p *simulatedIDV) CreateCheck(check *VerificationCheck) (string, error) {
	ref := "sim-" + check.ID
	decision, reason := simulatedDecision(check.SubjectName)
	go func() {
		time.Sleep(p.delay)
		body, _ := json.Marshal(map[string]any{
			"provider_ref": ref,
			"result":       string(decision),
			"reason":       reason,
		})
		req, err := http.NewRequest("POST", p.callbackURL, bytes.NewReader(body))
		if err != nil {
			log.Printf("idv(simulated): build callback for %s: %v", check.ID, err)
			return
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set(idvSignatureHeader, p.secret)
		resp, err := p.client.Do(req)
		if err != nil {
			log.Printf("idv(simulated): deliver decision for %s: %v", check.ID, err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			raw, _ := readAllLimited(resp.Body, 4<<10)
			log.Printf("idv(simulated): callback for %s answered %d: %s", check.ID, resp.StatusCode, raw)
		}
	}()
	return ref, nil
}

// simulatedDecision is deterministic by name so a demo can show every outcome on
// purpose and a test can rely on it. Everything not named as a persona clears,
// because the interesting states are the ones that are hard to reach by accident.
func simulatedDecision(name string) (idvDecision, string) {
	switch {
	case strings.Contains(strings.ToUpper(name), "REJECT"):
		return idvReject, "document review rejected (SIMULATED provider persona)"
	case strings.Contains(strings.ToUpper(name), "RESUBMIT"),
		strings.Contains(strings.ToUpper(name), "NEEDS INFO"):
		return idvResubmit, "the images supplied could not be read (SIMULATED provider persona)"
	}
	return idvClear, ""
}

// --- the callback ------------------------------------------------------------

// idvSignatureHeader is where a provider puts what proves the callback is theirs.
// A real one signs the body; the simulator presents the shared secret. Either
// way nothing is accepted without it, because this endpoint decides who is
// verified and it cannot be open.
const idvSignatureHeader = "X-SeqPal-IDV-Signature"

type idvCallbackReq struct {
	ProviderRef string `json:"provider_ref"`
	Result      string `json:"result"`
	Reason      string `json:"reason"`
}

// handleIDVCallback is POST /api/id/verify/callback: the provider's decision.
//
// No session: the caller is the provider, not the holder. Authenticated by the
// shared secret, and idempotent, because a provider that does not hear 200
// retries and must not be able to decide the same check twice.
func (s *server) handleIDVCallback(w http.ResponseWriter, r *http.Request) {
	if s.cfg.idvSecret == "" || !secretEqual(r.Header.Get(idvSignatureHeader), s.cfg.idvSecret) {
		writeErr(w, 401, "this callback is not signed by the verification provider")
		return
	}
	var req idvCallbackReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	ref := strings.TrimSpace(req.ProviderRef)
	if ref == "" {
		writeErr(w, 400, "provider_ref is required")
		return
	}
	decision := idvDecision(strings.ToLower(strings.TrimSpace(req.Result)))
	if !decision.valid() {
		writeErr(w, 400, "result must be clear, reject or resubmission")
		return
	}
	check, err := s.st.VerificationCheckByRef(ref)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if check == nil {
		writeErr(w, 404, "no such check")
		return
	}
	// Idempotent: a provider that did not hear 200 sends it again, and the second
	// delivery must not re-decide anything.
	if check.Status == "complete" {
		writeJSON(w, 200, map[string]any{"check_id": check.ID, "result": check.Result, "note": "already decided"})
		return
	}
	if err := s.applyAdjudication(check, decision, strings.TrimSpace(req.Reason)); err != nil {
		writeErr(w, 500, "could not apply the decision: %v", err)
		return
	}
	writeJSON(w, 200, map[string]any{"check_id": check.ID, "result": string(decision)})
}

// applyAdjudication turns a provider's decision into what this platform does
// about it. It is the single place a verification outcome takes effect, so a
// real provider's webhook lands exactly where the simulator's does.
func (s *server) applyAdjudication(check *VerificationCheck, decision idvDecision, reason string) error {
	claims, err := s.st.ClaimsByAID(check.AID)
	if err != nil {
		return fmt.Errorf("read claims: %w", err)
	}
	if claims == nil {
		return fmt.Errorf("no claims record for %s", check.AID)
	}
	now := time.Now().Unix()

	switch decision {
	case idvResubmit:
		claims.Status = "needs_info"
		if err := s.st.UpsertClaims(claims); err != nil {
			return err
		}
		s.st.Audit(check.AID, "id.verify.needs_info", map[string]any{
			"check": check.ID, "provider": check.Provider, "reason": reason,
		})

	case idvReject:
		// Refuse the claims FIRST. It is the record every eligibility read on this
		// platform consults, it can only restrict, and for an ID with no OpenAMP
		// account it is the whole of the enforcement.
		claims.Status = "refused"
		if err := s.st.UpsertClaims(claims); err != nil {
			return err
		}
		froze, ferr := s.freezeAtPolicyServer(check.AID)
		if ferr != nil {
			log.Printf("idv: freeze %s after a refusal: %v", check.AID, ferr)
		}
		if _, err := s.stampCategories(check.AID); err != nil {
			log.Printf("idv: strip categories for %s: %v", check.AID, err)
		}
		s.st.Audit(check.AID, "id.verify.refused", map[string]any{
			"check": check.ID, "provider": check.Provider, "reason": reason,
			"enclave_frozen": froze,
		})

	case idvClear:
		claims.Status = "verified"
		claims.VerifiedAt = now
		sig, err := s.signClaims(claims)
		if err != nil {
			return fmt.Errorf("sign claims: %w", err)
		}
		claims.ClaimsSig = sig
		if err := s.st.UpsertClaims(claims); err != nil {
			return err
		}
		cats, err := s.stampCategories(check.AID)
		if err != nil {
			return fmt.Errorf("stamp categories: %w", err)
		}
		s.st.Audit(check.AID, "id.verify.approved", map[string]any{
			"check": check.ID, "provider": check.Provider, "categories": cats,
		})
	}

	// The decision is recorded last and only once: a callback and a reconciling
	// poll can arrive together, and the first of them is the decision. The work
	// above may then have run twice, which is why all of it is idempotent -- the
	// claims are upserted, the categories restamped, the freeze re-asserted.
	decided, err := s.st.CompleteVerificationCheck(check.ID, string(decision), reason, now)
	if err != nil {
		return err
	}
	if !decided {
		s.st.Audit(check.AID, "id.verify.already_decided", map[string]any{
			"check": check.ID, "decision": string(decision),
		})
	}
	return nil
}

// secretEqual compares in constant time, so a caller cannot learn the secret one
// byte at a time from how long the comparison took.
func secretEqual(got, want string) bool {
	if len(got) != len(want) {
		return false
	}
	var diff byte
	for i := 0; i < len(got); i++ {
		diff |= got[i] ^ want[i]
	}
	return diff == 0
}

// runIDVReconcileCron is the backstop for a decision this platform never heard.
// A callback crosses a network once: the process can be restarted between the
// submission and the delivery, the delivery can fail, and the provider can drop
// it. Without this, a check that goes quiet leaves the holder at "submitted"
// forever -- unable to submit again, since submitting over a check that is with
// the provider is refused, and already charged for that check.
//
// Reconciling by polling is not a demo affordance: it is what an integration
// with a real provider does about missed webhooks, which is why the poll sits
// on the provider interface rather than beside the simulator.
func (s *server) runIDVReconcileCron(every time.Duration) {
	if every <= 0 {
		every = time.Minute
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for range t.C {
		s.reconcileVerifications()
	}
}

func (s *server) reconcileVerifications() {
	// Only checks old enough that a decision was due. One submitted a moment ago
	// is not late, it is in flight.
	cutoff := time.Now().Add(-s.cfg.idvGrace).Unix()
	checks, err := s.st.OutstandingVerificationChecks(cutoff)
	if err != nil {
		return
	}
	for _, c := range checks {
		decision, reason, decided, err := s.idv.PollCheck(c)
		if err != nil {
			log.Printf("idv: poll %s (%s): %v", c.ID, c.ProviderRef, err)
			continue
		}
		if !decided || !decision.valid() {
			continue
		}
		s.st.Audit(c.AID, "id.verify.reconciled", map[string]any{
			"check": c.ID, "provider": c.Provider, "decision": string(decision),
		})
		if err := s.applyAdjudication(c, decision, reason); err != nil {
			log.Printf("idv: reconcile %s: %v", c.ID, err)
		}
	}
}

// newIDVProvider builds the adapter this deployment verifies through. Only the
// simulator exists so far; a real provider is another type satisfying the same
// interface and a name here, which is the whole point of the interface.
func newIDVProvider(cfg config) idvProvider {
	switch cfg.idvProvider {
	case "", "simulated":
		return &simulatedIDV{
			// Over loopback to this process's own listener: the simulator
			// delivers its decision the way a provider does, so the callback is
			// exercised here rather than skipped.
			callbackURL: "http://" + cfg.listen + "/api/id/verify/callback",
			secret:      cfg.idvSecret,
			delay:       cfg.idvDecision,
			client:      &http.Client{Timeout: 20 * time.Second},
		}
	default:
		log.Fatalf("unknown identity-verification provider %q", cfg.idvProvider)
		return nil
	}
}

// verificationView is what a holder is shown about their own check: who has it,
// where it is, and what they said. Nil until they have submitted one.
func verificationView(c *VerificationCheck) map[string]any {
	if c == nil {
		return nil
	}
	out := map[string]any{
		"provider":   c.Provider,
		"status":     c.Status,
		"kind":       c.Kind,
		"created_at": c.CreatedAt,
	}
	if c.Status == "complete" {
		out["result"] = c.Result
		out["decided_at"] = c.DecidedAt
		if c.Reason != "" {
			out["reason"] = c.Reason
		}
	}
	return out
}
