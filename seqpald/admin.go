package main

import (
	"log"
	"net/http"
	"strings"
	"time"
)

// requireAdmin gates the seqpald manual-review surface. Admin AIDs are configured
// out of band (SEQPALD_ADMIN_AIDS); the reviewer is a real person acting through
// their SeqPal ID session.
func (s *server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		acct := principal(r)
		if acct == nil || !s.cfg.adminAIDs[acct.AID] {
			writeErr(w, 403, "this surface is restricted to platform reviewers")
			return
		}
		next(w, r)
	}
}

// GET /api/admin/review-queue
func (s *server) handleReviewQueue(w http.ResponseWriter, r *http.Request) {
	items, err := s.st.ReviewsByState("pending")
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	writeJSON(w, 200, map[string]any{"queue": items})
}

type reviewDecideReq struct {
	Decision string `json:"decision"` // clear | confirm
	Note     string `json:"note"`
}

// POST /api/admin/review/{id}
func (s *server) handleReviewDecide(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	id := r.PathValue("id")
	var req reviewDecideReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	if req.Decision != "clear" && req.Decision != "confirm" {
		writeErr(w, 400, "decision must be clear or confirm")
		return
	}
	item, err := s.st.ReviewByID(id)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if item == nil {
		writeErr(w, 404, "unknown review item")
		return
	}
	if item.State != "pending" {
		writeErr(w, 409, "this item was already decided (%s)", item.State)
		return
	}
	if err := s.decideReview(item, req.Decision, "admin:"+acct.AID, req.Note); err != nil {
		writeErr(w, 502, "%v", err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "decision": req.Decision, "aid": item.AID})
}

// decideReview resolves one review-queue item and applies the consequence. A
// CONFIRMED hit freezes the AID (a global freeze, correct under the single-
// operator model) and refuses the identity; a CLEARED false positive lets the
// verification proceed once no pending reviews remain for that AID. The DB
// transition is single-shot, so a human and the auto-reviewer cannot both act.
func (s *server) decideReview(item *ReviewItem, decision, by, note string) error {
	state := "cleared"
	if decision == "confirm" {
		state = "confirmed"
	}
	ok, err := s.st.DecideReview(item.ID, state, by, note)
	if err != nil {
		return err
	}
	if !ok {
		return nil // already decided by someone else
	}
	s.st.Audit(item.AID, "review.decide", map[string]any{
		"review_id": item.ID, "list": item.List, "decision": decision, "by": by,
	})
	if decision == "confirm" {
		// Freeze first (fund-safety), then mark the claims refused and strip any
		// categories through the write queue.
		if err := s.callOpenAMP("POST", "/v1/issuer/freeze", s.cfg.issuerToken,
			map[string]any{"aid": item.AID, "frozen": true}, nil); err != nil {
			return err
		}
		if claims, _ := s.st.ClaimsByAID(item.AID); claims != nil {
			claims.Status = "refused"
			_ = s.st.UpsertClaims(claims)
		}
		if _, err := s.writeCategories(item.AID); err != nil {
			log.Printf("review confirm: strip categories for %s: %v", item.AID, err)
		}
		s.st.Audit(item.AID, "sanctions.freeze", map[string]any{"review_id": item.ID, "list": item.List})
		return nil
	}
	// Cleared: proceed only when nothing else is pending for this AID.
	return s.finalizeAfterClear(item.AID)
}

// finalizeAfterClear completes a verification that was parked in review, once all
// its review items are resolved and none confirmed. It runs the same simulated
// document-review persona logic and, on approval, signs the claims record and
// stamps categories.
func (s *server) finalizeAfterClear(aid string) error {
	pending, err := s.st.PendingReviewsByAID(aid)
	if err != nil {
		return err
	}
	if len(pending) > 0 {
		return nil // still under review
	}
	claims, err := s.st.ClaimsByAID(aid)
	if err != nil || claims == nil {
		return err
	}
	if claims.Status == "refused" {
		return nil // a sibling item was confirmed
	}
	acct, err := s.st.AccountByAID(aid)
	if err != nil {
		return err
	}
	name := ""
	if acct != nil {
		name = acct.DisplayName
	}
	switch personaDisposition(name) {
	case "reject":
		claims.Status = "refused"
		return s.st.UpsertClaims(claims)
	case "needs_info":
		claims.Status = "needs_info"
		return s.st.UpsertClaims(claims)
	}
	claims.Status = "verified"
	if claims.VerifiedAt == 0 {
		claims.VerifiedAt = time.Now().Unix()
	}
	if sig, err := s.signClaims(claims); err == nil {
		claims.ClaimsSig = sig
	}
	if err := s.st.UpsertClaims(claims); err != nil {
		return err
	}
	cats, err := s.writeCategories(aid)
	if err != nil {
		return err
	}
	s.st.Audit(aid, "id.verify.approved", map[string]any{"categories": cats, "via": "review-cleared"})
	return nil
}

// --- background workers -------------------------------------------------------

// autoReviewLoop is the labeled SIMULATED auto-reviewer. After a short delay it
// resolves the deterministic TEST personas (matches whose fixture entry carries a
// disposition) so unattended walkthroughs complete; a human reviewer can always
// act first from the admin page. Genuine hits (no disposition) are left for a
// human. Nothing outside the KYC-simulation boundary is scripted.
func (s *server) autoReviewLoop(interval, delay time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for range t.C {
		s.runAutoReview(delay)
	}
}

func (s *server) runAutoReview(delay time.Duration) {
	items, err := s.st.ReviewsByState("pending")
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-delay).Unix()
	for _, it := range items {
		if it.Disposition == "" || it.CreatedAt > cutoff {
			continue
		}
		decision := it.Disposition // "confirm" | "clear"
		if decision != "confirm" && decision != "clear" {
			continue
		}
		if err := s.decideReview(it, decision, "auto-reviewer(SIMULATED)", "deterministic TEST persona"); err != nil {
			log.Printf("auto-reviewer: %s %s: %v", it.ID, decision, err)
		}
	}
}

// screeningCron re-screens every claims record against refreshed lists. A NEW hit
// on a previously verified identity moves it to pending review and strips its
// categories through the write queue; a hit already tracked is not re-queued.
func (s *server) screeningCron(refresh, interval time.Duration) {
	s.screen.Refresh()
	rt := time.NewTicker(refresh)
	st := time.NewTicker(interval)
	defer rt.Stop()
	defer st.Stop()
	for {
		select {
		case <-rt.C:
			s.screen.Refresh()
		case <-st.C:
			s.rescreenAll()
		}
	}
}

func (s *server) rescreenAll() {
	claims, err := s.st.AllClaims()
	if err != nil {
		return
	}
	for _, c := range claims {
		acct, err := s.st.AccountByAID(c.AID)
		if err != nil || acct == nil || acct.DisplayName == "" {
			continue
		}
		results := s.screen.Screen(acct.DisplayName)
		var newHits []ScreeningResult
		for _, r := range results {
			prev, _ := s.screeningWas(c.AID, r.List)
			_ = s.st.UpsertScreening(c.AID, r)
			if r.Matched && !prev {
				newHits = append(newHits, r)
			}
		}
		if len(newHits) == 0 || c.Status != "verified" {
			continue
		}
		c.Status = "pending_review"
		_ = s.st.UpsertClaims(c)
		for _, h := range newHits {
			id, _ := randHex(12)
			_ = s.st.InsertReview(&ReviewItem{
				ID: id, AID: c.AID, List: h.List, MatchedEntry: h.MatchedEntry,
				State: "pending", Disposition: s.screen.disposition(h.List, h.MatchedEntry),
				CreatedAt: time.Now().Unix(),
			})
		}
		if _, err := s.writeCategories(c.AID); err != nil {
			log.Printf("rescreen: strip categories for %s: %v", c.AID, err)
		}
		s.st.Audit(c.AID, "id.rescreen.pending_review", map[string]any{"lists": listsOf(newHits)})
	}
}

func (s *server) screeningWas(aid, list string) (bool, error) {
	results, err := s.st.ScreeningByAID(aid)
	if err != nil {
		return false, err
	}
	for _, r := range results {
		if r.List == list {
			return r.Matched, nil
		}
	}
	return false, nil
}

// expiryCron notifies holders 14 days before their identity expires and removes
// categories once expired (through the write queue, so a real transfer refusal
// follows). Expiry is a platform-wide identity attribute: stale accreditation is
// stale for every asset the holder owns, which is intended.
func (s *server) expiryCron(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for range t.C {
		s.runExpiry()
	}
}

func (s *server) runExpiry() {
	claims, err := s.st.AllClaims()
	if err != nil {
		return
	}
	now := time.Now().Unix()
	warn := int64(expiryWarnWindow.Seconds())
	for _, c := range claims {
		if c.Status != "verified" || c.ValidUntil == 0 {
			continue
		}
		if now >= c.ValidUntil {
			// Expired: the projection now yields no categories; write it through.
			if _, err := s.writeCategories(c.AID); err != nil {
				log.Printf("expiry: strip categories for %s: %v", c.AID, err)
				continue
			}
			s.st.Audit(c.AID, "id.category.expired", map[string]any{"valid_until": c.ValidUntil})
			continue
		}
		if c.ValidUntil-now <= warn {
			if seen, _ := s.st.NoticeExists(c.AID, "pre-expiry"); !seen {
				_ = s.st.InsertNotice(c.AID, "pre-expiry",
					"Your SeqPal ID eligibility expires soon. Re-verify to keep holding SeqPal-managed assets.")
				s.st.Audit(c.AID, "id.pre_expiry_notice", map[string]any{"valid_until": c.ValidUntil})
			}
		}
	}
}

// startWorkers launches the background crons. Intervals are configurable so a
// demo can run them fast; production uses daily screening and hourly expiry.
func (s *server) startWorkers() {
	go s.screeningCron(s.cfg.screenRefresh, s.cfg.screenInterval)
	go s.expiryCron(s.cfg.expiryInterval)
	go s.autoReviewLoop(s.cfg.autoReviewEvery, s.cfg.autoReviewDelay)
	// M3: the chain watcher (only when a node RPC is configured) and the daily
	// log-head anchor cron.
	if s.cfg.nodeURL != "" {
		go s.runWatcher(s.cfg.watchInterval)
	}
	go s.runAnchorCron(s.cfg.anchorInterval)
}

func adminSet(csv string) map[string]bool {
	out := map[string]bool{}
	for _, a := range strings.Split(csv, ",") {
		if a = strings.TrimSpace(a); a != "" {
			out[a] = true
		}
	}
	return out
}
