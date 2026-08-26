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
		if c.Status != "verified" {
			continue
		}
		// Identity-window expiry: the projection now yields no categories at all;
		// write it through so a real transfer refusal follows for every asset.
		if c.ValidUntil > 0 && now >= c.ValidUntil {
			if _, err := s.stampCategories(c.AID); err != nil {
				log.Printf("expiry: strip categories for %s: %v", c.AID, err)
				continue
			}
			s.st.Audit(c.AID, "id.category.expired", map[string]any{"valid_until": c.ValidUntil})
			continue
		}
		if c.ValidUntil > 0 && c.ValidUntil-now <= warn {
			if seen, _ := s.st.NoticeExists(c.AID, "pre-expiry"); !seen {
				_ = s.st.InsertNotice(c.AID, "pre-expiry",
					"Your SeqPal ID eligibility expires soon. Re-verify to keep holding SeqPal-managed assets.")
				s.st.Audit(c.AID, "id.pre_expiry_notice", map[string]any{"valid_until": c.ValidUntil})
			}
		}
		// Category-window expiry: an accreditation whose artifact has aged out drops
		// its acc tokens (a real refusal for accredited-only holdings) even while the
		// identity itself is still valid. projectCategories already excludes a stale
		// accreditation, so re-writing categories once enforces it at the policy
		// server. The notice-once guard keeps the cron from re-writing every tick.
		if c.Accredited && c.AccredValidUntil > 0 && now >= c.AccredValidUntil {
			if seen, _ := s.st.NoticeExists(c.AID, "accred-expired"); !seen {
				if _, err := s.stampCategories(c.AID); err != nil {
					log.Printf("expiry: strip accreditation categories for %s: %v", c.AID, err)
				} else {
					_ = s.st.InsertNotice(c.AID, "accred-expired",
						"Your accredited-investor verification has expired. Re-verify accreditation to keep access to accredited-only holdings.")
					s.st.Audit(c.AID, "id.accred.expired", map[string]any{"accred_valid_until": c.AccredValidUntil})
				}
			}
			continue
		}
		if c.Accredited && c.AccredValidUntil > 0 && c.AccredValidUntil-now <= warn {
			if seen, _ := s.st.NoticeExists(c.AID, "pre-accred-expiry"); !seen {
				_ = s.st.InsertNotice(c.AID, "pre-accred-expiry",
					"Your accredited-investor verification expires soon. Re-verify to keep accredited-only access.")
				s.st.Audit(c.AID, "id.pre_accred_expiry_notice", map[string]any{"accred_valid_until": c.AccredValidUntil})
			}
		}
	}
}

// startWorkers launches the background crons. Intervals are configurable so a
// demo can run them fast; production uses daily screening and hourly expiry.
func (s *server) startWorkers() {
	go s.expiryCron(s.cfg.expiryInterval)
	// M3: the chain watcher (only when a node RPC is configured) and the daily
	// log-head anchor cron.
	if s.cfg.nodeURL != "" {
		go s.runWatcher(s.cfg.watchInterval)
	}
	go s.runAnchorCron(s.cfg.anchorInterval)
	// M5: the deposit watcher (escrow deposits + on-chain fee payments) and the
	// SIMULATED fiat settlement cron.
	if s.cfg.nodeURL != "" || s.cfg.btcURL != "" {
		go s.runMoneyWatcher(s.cfg.watchInterval)
	}
	// M6: keep tracking settled native-BTC deposits so a post-delivery reorg on
	// testnet4 triggers a global freeze (Bitcoin anchoring is supreme).
	if s.cfg.btcURL != "" {
		go s.runBtcReorgWatcher(s.cfg.watchInterval)
	}
	go s.runFiatCron(2 * time.Second)
	// M7 (Backend-B): heal any half-applied rules mutation (amendment chain), and,
	// once a node/policy server is reachable, take scheduled ownership snapshots and
	// issue the labeled-simulated annual report. All best-effort and idempotent.
	if s.cfg.issuerToken != "" {
		go s.runRulesReconcileCron(s.cfg.rulesReconcile)
	}
	if s.cfg.nodeURL != "" {
		go s.runOwnershipSnapshotCron(s.cfg.snapshotInterval)
		go s.runAnnualReportCron(s.cfg.reportInterval)
	}
	// M8: capture wallet-initiated secondary transfers from openampd's /v1/log and
	// join them to identities server-side (the travel-rule record). Needs the issuer
	// token to read the holder register for beneficiary resolution.
	if s.cfg.issuerToken != "" {
		go s.runWalletTransferPoller(s.cfg.walletPollInterval)
	}
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
