package main

import (
	"fmt"
	"html"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Servicing notices (M7 section 6): the holder notices inbox, the scheduled
// ownership snapshot + anchor, and the labeled-simulated annual report to holders.
// Category-expiry enforcement itself lives in the expiry cron (admin.go), which
// removes an expired accreditation or identity at the policy server so a real
// transfer refusal follows; the surfaces here are the holder-facing side of that.

// handleNotices is GET /api/id/notices (session): the caller's portal inbox,
// newest first. Pre-expiry warnings, skipped distributions, servicing actions, and
// annual-report availability all land here.
func (s *server) handleNotices(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	notices, err := s.st.NoticesByAID(acct.AID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	writeJSON(w, 200, map[string]any{"notices": notices})
}

// --- scheduled ownership snapshot + anchor -----------------------------------

// runOwnershipSnapshotCron periodically captures each live issuance's register at
// a Sequentia block height and anchors it, from go-live onward. The snapshots are
// an append-only, content-addressed time series of who held what and when.
func (s *server) runOwnershipSnapshotCron(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for range t.C {
		s.snapshotAllRegisters()
	}
}

func (s *server) snapshotAllRegisters() {
	issuances, err := s.st.LiveIssuances()
	if err != nil {
		return
	}
	for _, iss := range issuances {
		if _, err := s.snapshotRegister(iss, "snapshot"); err != nil {
			continue
		}
	}
}

// snapshotRegister reads the chain register for an issuance, stores a
// content-addressed register document, records the snapshot, and anchors it.
func (s *server) snapshotRegister(iss *Issuance, kind string) (*OwnershipSnapshot, error) {
	var rep holdersReport
	if err := s.callOpenAMP("GET", "/v1/issuer/holders?asset="+iss.AssetID, s.cfg.issuerToken, nil, &rep); err != nil {
		return nil, err
	}
	title, body := registerDoc(iss, &rep, kind)
	hash := sha256Hex([]byte(body))
	_ = s.st.PutDocument(&StoredDoc{
		DocHash: hash, IssuanceID: iss.ID, Kind: "ownership-" + kind, Title: title,
		ContentType: "text/html; charset=utf-8", AlwaysPublic: true, Body: []byte(body),
	})
	anchorTxid := s.anchorArtifact("ownership-"+kind, hash)
	o := &OwnershipSnapshot{
		ID: mustID(), IssuanceID: iss.ID, AssetID: iss.AssetID, Height: rep.Height,
		HoldersCount: countPositive(rep.Holders), TotalAtoms: rep.TotalAtoms, ReportHash: hash,
		Kind: kind, AnchorTxid: anchorTxid,
	}
	if err := s.st.InsertOwnershipSnapshot(o); err != nil {
		return nil, err
	}
	s.st.Audit("", "ownership."+kind, map[string]any{
		"issuance_id": iss.ID, "asset": iss.AssetID, "height": rep.Height,
		"holders": o.HoldersCount, "report_hash": hash, "anchor_txid": anchorTxid,
	})
	return o, nil
}

// --- scheduled annual report to holders --------------------------------------

// runAnnualReportCron periodically issues a labeled-simulated annual report to
// holders for each live issuance and anchors it, then drops a notice in each
// holder's inbox linking the report. It rate-limits itself to one report per
// interval per issuance.
func (s *server) runAnnualReportCron(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for range t.C {
		s.issueAnnualReports(interval)
	}
}

func (s *server) issueAnnualReports(interval time.Duration) {
	issuances, err := s.st.LiveIssuances()
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-interval).Unix()
	for _, iss := range issuances {
		if last, _ := s.st.LastSnapshotOfKind(iss.ID, "annual-report"); last != nil && last.CreatedAt > cutoff {
			continue // one report per interval
		}
		o, err := s.snapshotRegister(iss, "annual-report")
		if err != nil {
			continue
		}
		// Notify each current holder that the report is available.
		var rep holdersReport
		if err := s.callOpenAMP("GET", "/v1/issuer/holders?asset="+iss.AssetID, s.cfg.issuerToken, nil, &rep); err != nil {
			continue
		}
		body := fmt.Sprintf("The %s (%s) annual holder report is available: /seqpal/api/doc/%s. This is a labeled-simulated regulatory filing; the register math and its hash are real.",
			iss.Name, iss.Ticker, o.ReportHash)
		for aid, bal := range rep.Holders {
			if bal == 0 {
				continue
			}
			_ = s.st.InsertNotice(aid, "annual-report", body)
		}
	}
}

// --- report documents --------------------------------------------------------

func registerDoc(iss *Issuance, rep *holdersReport, kind string) (string, string) {
	label := "Ownership snapshot"
	intro := "A scheduled, content-addressed and anchored snapshot of the register: who held what at a Sequentia block height. The balances are read from the chain (confirmed enclave balances), never asserted."
	if kind == "annual-report" {
		label = "SIMULATED annual holder report"
		intro = "SIMULATED annual report to holders and regulators. The register below and this artifact's hash are real; no report is transmitted to any authority. The registrar entity is the reporting entity of record."
	}
	title := fmt.Sprintf("%s: %s (%s)", label, iss.Name, iss.Ticker)
	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>")
	b.WriteString(para(intro))
	b.WriteString("<table border=\"1\" cellpadding=\"4\"><tbody>")
	b.WriteString(row2("Asset", iss.AssetID))
	b.WriteString(row2("Record height", fmt.Sprintf("Sequentia block %d", rep.Height)))
	b.WriteString(row2("Holders of record", fmt.Sprintf("%d", countPositive(rep.Holders))))
	b.WriteString(row2("Total held (atoms)", fmt.Sprintf("%d", rep.TotalAtoms)))
	b.WriteString("</tbody></table>")
	b.WriteString("<h2>Register</h2>")
	b.WriteString("<table border=\"1\" cellpadding=\"4\"><tbody>")
	b.WriteString("<tr><th>Holder</th><th>Balance (atoms)</th></tr>")
	aids := make([]string, 0, len(rep.Holders))
	for aid := range rep.Holders {
		aids = append(aids, aid)
	}
	sort.Strings(aids)
	for _, aid := range aids {
		if rep.Holders[aid] == 0 {
			continue
		}
		b.WriteString("<tr><td>" + html.EscapeString(shortAID(aid)) + "</td><td>" + fmt.Sprintf("%d", rep.Holders[aid]) + "</td></tr>")
	}
	b.WriteString("</tbody></table>")
	return title, docPage("ownership-"+kind, title, b.String())
}

func countPositive(holders map[string]uint64) int {
	n := 0
	for _, bal := range holders {
		if bal > 0 {
			n++
		}
	}
	return n
}
