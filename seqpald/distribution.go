package main

import (
	"fmt"
	"html"
	"math/big"
	"net/http"
	"sort"
	"strings"
)

// The distribution engine (M7 section 1), USDX only. A run funds a servicing
// deposit, snapshots the register at a Sequentia block height, computes pro-rata
// shares in atoms with disclosed dust, applies per-holder withholding from the
// ID tax status, and pays NET USDX to each holder's registered ordinary payout
// mandate address, one payment per holder.
//
// State machine: awaiting_funding -> funded -> snapshotted -> paying -> complete.
// ISSUER FUNDING IS FIRST: the run pays nothing until the servicing deposit
// confirms at escrowConfs and covers the gross pool; under-funding keeps it
// blocked and audit-logged.
//
// FUND-SAFETY is the M5/M6 invariant, reused exactly: exactly one payment row per
// (run, holder); the per-holder marker comment seqpal-dist-<runID>-<holderAID>;
// the intent (state=paying) persisted before broadcast; reconciliation by
// escrowFindSend on that marker before any retry; never a double-pay. Payments
// flow out of the servicing wallet (seqpal-escrow, the M5 money-holding surface),
// so releaseUSDX and escrowFindSend carry over unchanged.

// distMarker is the per-holder settlement-scoped comment that makes a lost-write
// payout reconcilable (escrowFindSend) instead of re-broadcast from the commingled
// servicing wallet.
func distMarker(runID, holderAID string) string { return "seqpal-dist-" + runID + "-" + holderAID }

// holdersReport mirrors openampd GET /v1/issuer/holders: confirmed enclave
// balances per AID (in atoms) captured WITH the Sequentia block height.
type holdersReport struct {
	Asset      string            `json:"asset"`
	Height     int64             `json:"height"`
	Holders    map[string]uint64 `json:"holders"`
	TotalAtoms uint64            `json:"total_atoms"`
}

// --- withholding -------------------------------------------------------------

// simTreatyBps returns the labeled-simulated US-source dividend withholding rate
// (basis points) for a residence country. It is a small demonstration table, not
// a tax determination: the statutory NRA chapter-3 rate is 30% (3000 bps), a tax
// treaty reduces it. A country with no entry defaults to the statutory 3000 bps.
func simTreatyBps(country string) int64 {
	switch strings.ToUpper(strings.TrimSpace(country)) {
	case "GB", "DE", "FR", "IE", "NL", "ES", "IT", "AU", "CA", "CH", "SE", "NO", "DK", "FI":
		return 1500 // 15% typical treaty rate on portfolio dividends
	case "JP":
		return 1000
	default:
		return 3000 // statutory NRA rate, no treaty on file
	}
}

// withholdingFor computes the per-holder withholding from the ID tax status.
// A US person (W-9 analogue) has no NRA chapter-3 withholding (they receive a
// 1099, not a 1042-S; section 871(m) dividend-equivalent withholding excludes US
// persons). A non-US person (W-8BEN) is withheld at the statutory 30% NRA rate,
// reduced by the treaty rate captured for their residence. An undetermined holder
// is withheld conservatively at the statutory rate. Returns the withheld atoms,
// the applied rate (bps), and the tax status label.
func withholdingFor(claims *Claims, gross uint64) (withheld uint64, treatyBps int64, status string) {
	if claims != nil && claims.USPerson {
		return 0, 0, "w9"
	}
	country := ""
	status = "w8ben"
	if claims == nil {
		status = "unknown"
	} else {
		country = claims.Residence
	}
	treatyBps = simTreatyBps(country)
	// mulDiv (big.Int) keeps floor(gross * bps / 10000) exact and overflow-safe for
	// large pools, matching the pro-rata discipline; a naive gross*bps would wrap
	// uint64 above a ~61M-USDX gross and corrupt the net = gross - withheld line.
	withheld = mulDiv(gross, uint64(treatyBps), 10000)
	return withheld, treatyBps, status
}

// --- create ------------------------------------------------------------------

type createDistReq struct {
	PoolAtoms uint64  `json:"pool_atoms"`
	PoolUSD   float64 `json:"pool_usd"`
	Memo      string  `json:"memo"`
}

// handleCreateDistribution is POST /api/issuances/{id}/distributions (owner):
// open a run in awaiting_funding with a servicing-wallet USDX deposit invoice for
// the gross pool. Pays nothing until funded.
func (s *server) handleCreateDistribution(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	if iss.Status != "live" || iss.AssetID == "" {
		writeErr(w, 409, "this issuance is not live on chain")
		return
	}
	if s.cfg.nodeURL == "" {
		writeErr(w, 503, "the USDX servicing rail is not available on this deployment")
		return
	}
	var req createDistReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	pool := req.PoolAtoms
	if pool == 0 && req.PoolUSD > 0 {
		pool = usdToAtoms(req.PoolUSD)
	}
	if pool == 0 {
		writeErr(w, 400, "a positive pool_atoms (or pool_usd) is required")
		return
	}
	addr, err := s.newUSDXDepositAddress()
	if err != nil {
		writeErr(w, 502, "could not derive a servicing deposit address: %v", err)
		return
	}
	d := &Distribution{
		ID:             mustID(),
		IssuanceID:     iss.ID,
		AssetID:        iss.AssetID,
		PoolAtoms:      pool,
		Memo:           strings.TrimSpace(req.Memo),
		State:          "awaiting_funding",
		DepositAddress: addr,
		DividendEquiv:  canonicalStructure(iss.StructureID) == "depositary-receipt",
	}
	if err := s.st.InsertDistribution(d); err != nil {
		writeErr(w, 500, "could not create the distribution run")
		return
	}
	s.st.Audit(acct.AID, "distribution.create", map[string]any{
		"issuance_id": iss.ID, "run": d.ID, "pool_atoms": pool, "deposit_address": addr,
	})
	writeJSON(w, 200, map[string]any{
		"distribution": d, "deposit_address": addr, "pay_amount": pool, "pay_ccy": "USDX",
		"confs_required": s.cfg.escrowConfs,
		"note":           "fund this servicing deposit with USDX covering the gross pool; the run pays nothing until it confirms",
	})
}

// --- funding watcher ---------------------------------------------------------

// watchDistributionDeposits advances a run from awaiting_funding to funded once
// the servicing deposit confirms at escrowConfs AND covers the gross pool. It
// only advances awaiting_funding rows, so it never double-credits; an
// under-funded (but confirmed) deposit is audit-logged and the run stays blocked.
func (s *server) watchDistributionDeposits() {
	if s.cfg.nodeURL == "" {
		return
	}
	runs, err := s.st.DistributionsByState("awaiting_funding")
	if err != nil {
		return
	}
	for _, d := range runs {
		if d.DepositAddress == "" {
			continue
		}
		dep, ok, err := s.usdxDeposit(d.DepositAddress)
		if err != nil || !ok || dep.Confirmations < s.cfg.escrowConfs {
			continue
		}
		if dep.Atoms < d.PoolAtoms {
			s.st.Audit("", "distribution.underfunded", map[string]any{
				"run": d.ID, "issuance_id": d.IssuanceID, "deposited": dep.Atoms, "required": d.PoolAtoms,
			})
			continue
		}
		if err := s.st.UpdateDistributionFields(d.ID, map[string]any{
			"state": "funded", "funded_atoms": dep.Atoms, "funded_txid": dep.Txid,
		}); err != nil {
			continue
		}
		_ = s.st.InsertLedger(&LedgerEntry{
			IssuanceID: d.IssuanceID, Kind: "dist_funding", Rail: "usdx",
			Amount: dep.Atoms, Ccy: "USDX", Txid: dep.Txid, FundsSimulated: false,
		})
		s.st.Audit("", "distribution.funded", map[string]any{
			"run": d.ID, "issuance_id": d.IssuanceID, "txid": dep.Txid, "atoms": dep.Atoms,
		})
	}
}

// --- snapshot ----------------------------------------------------------------

// handleSnapshotDistribution is POST /api/issuances/{id}/distributions/{runID}/
// snapshot (owner): capture the holder set + per-holder atoms from the CHAIN
// (openampd holders) at the current Sequentia block height, compute pro-rata +
// withholding, and persist an immutable per-holder payment table. Idempotent per
// run: a second call returns the same table.
func (s *server) handleSnapshotDistribution(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss, d := s.ownedDistribution(w, acct, r.PathValue("id"), r.PathValue("runID"))
	if d == nil {
		return
	}
	s.ensureServicingMu()
	unlock := s.distMu.lock(d.ID)
	defer unlock()
	d, _ = s.st.DistributionByID(d.ID)

	switch d.State {
	case "snapshotted", "paying", "complete":
		// Immutable once taken: return the existing table.
		s.writeDistributionView(w, d)
		return
	case "awaiting_funding":
		writeErr(w, 409, "fund the run before taking the record-date snapshot")
		return
	case "funded":
		// proceed
	default:
		writeErr(w, 409, "the run is in state %q and cannot be snapshotted", d.State)
		return
	}

	// Durability: a prior snapshot may have crashed after inserting some per-holder
	// rows but before the state/totals write (state still "funded"). The per-holder
	// rows are frozen (ON CONFLICT DO NOTHING) and are what payouts read, so the
	// summary must be recomputed FROM THOSE FROZEN ROWS, not from a fresh holders
	// read (whose balances may have drifted in the crash window), or the display
	// summary would disagree with what is actually paid. When no rows exist yet, this
	// is the normal first-time snapshot.
	var height int64
	var totalHeld, grossTotal, withheldTotal, netTotal uint64
	if existing, _ := s.st.DistPaymentsByRun(d.ID); len(existing) > 0 {
		height = d.SnapshotHeight
		for _, p := range existing {
			totalHeld += p.BalanceAtoms
			grossTotal += p.GrossAtoms
			withheldTotal += p.WithheldAtoms
			netTotal += p.NetAtoms
		}
	} else {
		var rep holdersReport
		if err := s.callOpenAMP("GET", "/v1/issuer/holders?asset="+d.AssetID, s.cfg.issuerToken, nil, &rep); err != nil {
			writeErr(w, 502, "could not read the holder register: %v", err)
			return
		}
		height = rep.Height

		// Sum only positive balances: a zero-balance AID is not a holder of record.
		type holderBal struct {
			aid string
			bal uint64
		}
		held := []holderBal{}
		for aid, bal := range rep.Holders {
			if bal == 0 {
				continue
			}
			held = append(held, holderBal{aid: aid, bal: bal})
			totalHeld += bal
		}
		sort.Slice(held, func(i, j int) bool { return held[i].aid < held[j].aid })
		if totalHeld == 0 {
			writeErr(w, 409, "the register has no holders with a positive balance to distribute to")
			return
		}

		// Pro-rata in atoms: gross_i = floor(pool * balance_i / total_held). The floor
		// remainder is disclosed as dust, never silently dropped.
		for _, h := range held {
			gross := mulDiv(d.PoolAtoms, h.bal, totalHeld)
			claims, _ := s.st.ClaimsByAID(h.aid)
			withheld, treatyBps, status := withholdingFor(claims, gross)
			net := gross - withheld
			grossTotal += gross
			withheldTotal += withheld
			netTotal += net
			_, _ = s.st.InsertDistPaymentIfAbsent(&DistPayment{
				RunID: d.ID, HolderAID: h.aid, BalanceAtoms: h.bal, GrossAtoms: gross,
				WithheldAtoms: withheld, NetAtoms: net, TreatyBps: treatyBps, TaxStatus: status,
				State: "pending",
			})
		}
	}
	dust := d.PoolAtoms - grossTotal

	// Acceptance invariant: sum(gross) == sum(net) + sum(withheld).
	if grossTotal != netTotal+withheldTotal {
		writeErr(w, 500, "withholding reconciliation failed (gross != net + withheld)")
		return
	}

	holderCount, _ := s.st.DistPaymentCount(d.ID)
	if err := s.st.UpdateDistributionFields(d.ID, map[string]any{
		"state": "snapshotted", "snapshot_height": height, "total_held": totalHeld,
		"dust_atoms": dust, "gross_total": grossTotal, "withheld_total": withheldTotal, "net_total": netTotal,
	}); err != nil {
		writeErr(w, 500, "store error")
		return
	}
	s.st.Audit(acct.AID, "distribution.snapshot", map[string]any{
		"issuance_id": iss.ID, "run": d.ID, "height": height, "holders": holderCount,
		"total_held": totalHeld, "pool_atoms": d.PoolAtoms, "gross_total": grossTotal,
		"withheld_total": withheldTotal, "net_total": netTotal, "dust_atoms": dust,
	})
	d, _ = s.st.DistributionByID(d.ID)
	s.writeDistributionView(w, d)
}

// mulDiv computes floor(a * b / c) exactly. The a*b product (pool atoms times a
// holder balance) can exceed uint64 for large pools, so it is done in big.Int to
// stay exact; the quotient always fits back in uint64 since it is at most the pool.
func mulDiv(a, b, c uint64) uint64 {
	if c == 0 {
		return 0
	}
	prod := new(big.Int).Mul(new(big.Int).SetUint64(a), new(big.Int).SetUint64(b))
	prod.Div(prod, new(big.Int).SetUint64(c))
	return prod.Uint64()
}

// --- execute -----------------------------------------------------------------

// handleExecuteDistribution is POST /api/issuances/{id}/distributions/{runID}/
// execute (owner): pay NET USDX to each holder's registered mandate address, one
// payment per holder, with M5/M6 idempotency + reconciliation. Resumable: a
// second call retries only rows that are not yet terminal.
func (s *server) handleExecuteDistribution(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss, d := s.ownedDistribution(w, acct, r.PathValue("id"), r.PathValue("runID"))
	if d == nil {
		return
	}
	s.ensureServicingMu()
	unlock := s.distMu.lock(d.ID)
	defer unlock()
	d, _ = s.st.DistributionByID(d.ID)

	if d.State == "complete" {
		s.writeDistributionView(w, d)
		return
	}
	if d.State != "snapshotted" && d.State != "paying" {
		writeErr(w, 409, "snapshot the run before executing (state is %q)", d.State)
		return
	}
	_ = s.st.UpdateDistributionFields(d.ID, map[string]any{"state": "paying"})
	s.st.Audit(acct.AID, "distribution.execute.begin", map[string]any{"issuance_id": iss.ID, "run": d.ID})

	rows, err := s.st.DistPaymentsByRun(d.ID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	for _, p := range rows {
		if p.State == "paid" || p.State == "skipped" || p.State == "no_payment" {
			continue
		}
		s.payOneHolder(d, p)
	}

	// Recompute completeness and, when every row is terminal, finalize artifacts
	// and mark the run complete. A remaining failed row keeps the run in paying so
	// a later execute retries it.
	rows, _ = s.st.DistPaymentsByRun(d.ID)
	allTerminal := true
	for _, p := range rows {
		if p.State != "paid" && p.State != "skipped" && p.State != "no_payment" {
			allTerminal = false
			break
		}
	}
	if allTerminal {
		s.finalizeDistributionArtifacts(iss, d, rows)
		_ = s.st.UpdateDistributionFields(d.ID, map[string]any{"state": "complete"})
		s.st.Audit(acct.AID, "distribution.complete", map[string]any{"issuance_id": iss.ID, "run": d.ID})
	}
	d, _ = s.st.DistributionByID(d.ID)
	s.writeDistributionView(w, d)
}

// payOneHolder runs the idempotent per-holder payout. It is safe to call
// repeatedly: the payment row is the single source of truth for what has already
// happened, and a lost write is reconciled by the marker comment before any retry.
func (s *server) payOneHolder(d *Distribution, p *DistPayment) {
	// A fully-withheld holder has nothing to pay on-chain; that is a terminal state,
	// not a skip, and it carries no txid legitimately.
	if p.NetAtoms == 0 {
		_ = s.st.UpdateDistPaymentFields(d.ID, p.HolderAID, map[string]any{
			"state": "no_payment", "reason": "net zero after withholding",
		})
		return
	}

	// A holder with no registered mandate is SKIPPED with a portal notice, never
	// paid to a guessed address.
	m, err := s.st.InvestorMandateFor(p.HolderAID, "sequentia")
	if err != nil || m == nil || m.Address == "" {
		_ = s.st.UpdateDistPaymentFields(d.ID, p.HolderAID, map[string]any{
			"state": "skipped", "reason": "no registered payout mandate",
		})
		_ = s.st.InsertNotice(p.HolderAID, "distribution",
			"A distribution to you was skipped because you have no registered payout address. Register a Sequentia payout mandate in your portal to receive it.")
		s.st.Audit(p.HolderAID, "distribution.skip.no_mandate", map[string]any{"run": d.ID})
		return
	}
	// Defense in depth: never pay an enclave key-path address even if one slipped
	// past registration (a wallet cannot scan it, so the funds would strand). Fail
	// closed: if the enclave check cannot complete (openampd unreachable), do NOT
	// pay this tick; leave the row retryable so a later execute settles it once the
	// check can run, rather than risk paying an unverified address.
	if isEnclave, err := s.isInvestorEnclaveAddress(p.HolderAID, m.Address); err != nil {
		_ = s.st.UpdateDistPaymentFields(d.ID, p.HolderAID, map[string]any{
			"state": "failed", "reason": "enclave-address check unavailable; will retry",
		})
		s.st.Audit(p.HolderAID, "distribution.defer.enclave_check_unavailable", map[string]any{"run": d.ID})
		return
	} else if isEnclave {
		_ = s.st.UpdateDistPaymentFields(d.ID, p.HolderAID, map[string]any{
			"state": "skipped", "reason": "mandate address resolves to an enclave key-path address",
		})
		s.st.Audit(p.HolderAID, "distribution.skip.enclave_mandate", map[string]any{"run": d.ID, "address": m.Address})
		return
	}

	marker := distMarker(d.ID, p.HolderAID)

	// Reconcile a lost write: if a prior attempt already broadcast this payout
	// (state paying, no recorded txid), find it by its marker comment and record it
	// rather than re-sending from the commingled servicing wallet.
	if p.State == "paying" && p.Txid == "" {
		if txid, found := s.escrowFindSend("usdx", marker); found {
			_ = s.st.UpdateDistPaymentFields(d.ID, p.HolderAID, map[string]any{
				"state": "paid", "txid": txid, "mandate_address": m.Address, "reason": "",
			})
			s.st.Audit(p.HolderAID, "distribution.pay.reconciled", map[string]any{"run": d.ID, "txid": txid})
			return
		}
	}

	// Persist the intent BEFORE broadcasting so a lost write is reconcilable.
	_ = s.st.UpdateDistPaymentFields(d.ID, p.HolderAID, map[string]any{
		"state": "paying", "mandate_address": m.Address,
	})
	txid, rerr := s.releaseUSDX(m.Address, p.NetAtoms, marker)
	if rerr != nil {
		// An individual payout failure is operational and per-holder: record it and
		// leave the run resumable, never abort the whole distribution.
		_ = s.st.UpdateDistPaymentFields(d.ID, p.HolderAID, map[string]any{
			"state": "failed", "reason": "payout failed: " + rerr.Error(),
		})
		s.st.Audit(p.HolderAID, "distribution.pay.failed", map[string]any{"run": d.ID, "error": rerr.Error()})
		return
	}
	_ = s.st.UpdateDistPaymentFields(d.ID, p.HolderAID, map[string]any{
		"state": "paid", "txid": txid, "reason": "",
	})
	_ = s.st.InsertLedger(&LedgerEntry{
		IssuanceID: d.IssuanceID, Kind: "distribution", Rail: "usdx",
		Amount: p.NetAtoms, Ccy: "USDX", Txid: txid, FundsSimulated: false,
	})
	s.st.Audit(p.HolderAID, "distribution.pay", map[string]any{
		"run": d.ID, "txid": txid, "net_atoms": p.NetAtoms, "withheld_atoms": p.WithheldAtoms,
	})
}

// --- reads -------------------------------------------------------------------

// handleListDistributions is GET /api/issuances/{id}/distributions (owner).
func (s *server) handleListDistributions(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	runs, err := s.st.DistributionsByIssuance(iss.ID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	writeJSON(w, 200, map[string]any{"issuance_id": iss.ID, "distributions": runs})
}

// handleGetDistribution is GET /api/issuances/{id}/distributions/{runID} (owner):
// the run plus its per-holder payment table.
func (s *server) handleGetDistribution(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	_, d := s.ownedDistribution(w, acct, r.PathValue("id"), r.PathValue("runID"))
	if d == nil {
		return
	}
	s.writeDistributionView(w, d)
}

func (s *server) writeDistributionView(w http.ResponseWriter, d *Distribution) {
	rows, _ := s.st.DistPaymentsByRun(d.ID)
	writeJSON(w, 200, map[string]any{
		"distribution": d,
		"payments":     rows,
		"label":        "real USDX pro-rata distribution; withholding remittance to tax authorities is out of scope and labeled simulated",
	})
}

// ownedDistribution loads a run and enforces that the session owns the parent
// issuance and that the run belongs to it. It writes the error response itself and
// returns (nil, nil) when the caller must stop.
func (s *server) ownedDistribution(w http.ResponseWriter, acct *Account, issuanceID, runID string) (*Issuance, *Distribution) {
	iss := s.ownedIssuance(w, acct, issuanceID)
	if iss == nil {
		return nil, nil
	}
	d, err := s.st.DistributionByID(runID)
	if err != nil {
		writeErr(w, 500, "store error")
		return nil, nil
	}
	if d == nil || d.IssuanceID != iss.ID {
		writeErr(w, 404, "unknown distribution run for this issuance")
		return nil, nil
	}
	return iss, d
}

// --- artifacts ---------------------------------------------------------------

// finalizeDistributionArtifacts produces the content-addressed, anchored M7
// artifacts once a run completes: a per-holder statement + 1042-S-style artifact,
// an issuer withholding summary, and a labeled-simulated annual CRS/FATCA
// artifact. The math and the hashes are real; the remittance to tax/regulatory
// authorities is simulated and labeled. Idempotent: it only generates when the
// withholding summary has not yet been produced.
func (s *server) finalizeDistributionArtifacts(iss *Issuance, d *Distribution, rows []*DistPayment) {
	if d.WithholdingHash != "" {
		return // already generated
	}
	// Per-holder statements (each also the 1042-S-style artifact).
	var stmtHashes []string
	for _, p := range rows {
		title, body := holderStatementDoc(iss, d, p)
		hash := sha256Hex([]byte(body))
		_ = s.st.PutDocument(&StoredDoc{
			DocHash: hash, IssuanceID: iss.ID, Kind: "distribution-statement", Title: title,
			ContentType: "text/html; charset=utf-8", AlwaysPublic: true, Body: []byte(body),
		})
		_ = s.st.UpdateDistPaymentFields(d.ID, p.HolderAID, map[string]any{"statement_hash": hash})
		stmtHashes = append(stmtHashes, hash)
	}
	sort.Strings(stmtHashes)
	statementSetHash := sha256Hex([]byte(strings.Join(stmtHashes, "\n")))

	// Issuer withholding summary.
	wTitle, wBody := withholdingSummaryDoc(iss, d, rows)
	wHash := sha256Hex([]byte(wBody))
	_ = s.st.PutDocument(&StoredDoc{
		DocHash: wHash, IssuanceID: iss.ID, Kind: "distribution-withholding-summary", Title: wTitle,
		ContentType: "text/html; charset=utf-8", AlwaysPublic: true, Body: []byte(wBody),
	})

	// Labeled-simulated annual CRS/FATCA artifact from the registrar entity.
	cTitle, cBody := s.crsFatcaDoc(iss, d, rows)
	cHash := sha256Hex([]byte(cBody))
	_ = s.st.PutDocument(&StoredDoc{
		DocHash: cHash, IssuanceID: iss.ID, Kind: "distribution-crs-fatca", Title: cTitle,
		ContentType: "text/html; charset=utf-8", AlwaysPublic: true, Body: []byte(cBody),
	})

	// Anchor the withholding summary hash via the existing log/anchor path.
	anchorTxid := s.anchorArtifact("distribution-withholding", wHash)

	_ = s.st.UpdateDistributionFields(d.ID, map[string]any{
		"statement_hash": statementSetHash, "withholding_hash": wHash, "crs_hash": cHash,
		"anchor_txid": anchorTxid,
	})
	s.st.Audit("", "distribution.artifacts", map[string]any{
		"run": d.ID, "issuance_id": iss.ID, "statement_set_hash": statementSetHash,
		"withholding_hash": wHash, "crs_hash": cHash, "anchor_txid": anchorTxid,
	})
}

func holderStatementDoc(iss *Issuance, d *Distribution, p *DistPayment) (string, string) {
	title := fmt.Sprintf("Distribution Statement: %s (%s), holder %s", iss.Name, iss.Ticker, shortAID(p.HolderAID))
	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>")
	b.WriteString(para("This statement and 1042-S-style artifact record one holder's share of a real USDX distribution on the Sequentia network. The pro-rata math and this artifact's hash are real; any remittance of the withheld amount to a tax authority is a simulated, labeled step."))
	b.WriteString("<table border=\"1\" cellpadding=\"4\"><tbody>")
	b.WriteString(row2("Run id", d.ID))
	b.WriteString(row2("Asset", iss.AssetID))
	b.WriteString(row2("Record-date height", fmt.Sprintf("Sequentia block %d", d.SnapshotHeight)))
	b.WriteString(row2("Holder AID", p.HolderAID))
	b.WriteString(row2("Balance of record (atoms)", fmt.Sprintf("%d", p.BalanceAtoms)))
	b.WriteString(row2("Gross (USDX atoms)", fmt.Sprintf("%d", p.GrossAtoms)))
	b.WriteString(row2("Tax status", taxStatusLabel(p.TaxStatus)))
	b.WriteString(row2("Withholding rate", fmt.Sprintf("%.2f%%", float64(p.TreatyBps)/100)))
	b.WriteString(row2("Withheld (USDX atoms)", fmt.Sprintf("%d", p.WithheldAtoms)))
	b.WriteString(row2("Net paid (USDX atoms)", fmt.Sprintf("%d", p.NetAtoms)))
	b.WriteString(row2("Payment status", p.State))
	if p.Txid != "" {
		b.WriteString(row2("Payout txid", p.Txid))
	}
	if p.MandateAddress != "" {
		b.WriteString(row2("Paid to", p.MandateAddress))
	}
	b.WriteString("</tbody></table>")
	if d.DividendEquiv {
		b.WriteString(para("Dividend-equivalent line (section 871(m)): this asset is a depositary receipt mirroring an underlying listed security, so the amount above is treated as a dividend-equivalent payment for chapter 3 withholding on non-US persons. US persons are excluded from 871(m) and receive a 1099-style report rather than a 1042-S."))
	}
	return title, docPage("distribution-statement", title, b.String())
}

func withholdingSummaryDoc(iss *Issuance, d *Distribution, rows []*DistPayment) (string, string) {
	title := fmt.Sprintf("Issuer Withholding Summary: %s (%s), run %s", iss.Name, iss.Ticker, d.ID)
	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>")
	b.WriteString(para("Aggregate withholding for one USDX distribution run. Sum of gross equals sum of net plus sum of withheld (an acceptance invariant). The dust remainder from pro-rata flooring is disclosed and stays in the servicing wallet, never silently dropped. Remittance of the withheld amount to a tax authority is a simulated, labeled step in this proof of concept."))
	b.WriteString("<table border=\"1\" cellpadding=\"4\"><tbody>")
	b.WriteString(row2("Asset", iss.AssetID))
	b.WriteString(row2("Record-date height", fmt.Sprintf("Sequentia block %d", d.SnapshotHeight)))
	b.WriteString(row2("Pool (USDX atoms)", fmt.Sprintf("%d", d.PoolAtoms)))
	b.WriteString(row2("Total balance of record (atoms)", fmt.Sprintf("%d", d.TotalHeld)))
	b.WriteString(row2("Gross total (USDX atoms)", fmt.Sprintf("%d", d.GrossTotal)))
	b.WriteString(row2("Withheld total (USDX atoms)", fmt.Sprintf("%d", d.WithheldTotal)))
	b.WriteString(row2("Net total paid (USDX atoms)", fmt.Sprintf("%d", d.NetTotal)))
	b.WriteString(row2("Dust remainder (USDX atoms)", fmt.Sprintf("%d", d.DustAtoms)))
	b.WriteString(row2("Holders of record", fmt.Sprintf("%d", len(rows))))
	b.WriteString("</tbody></table>")
	b.WriteString("<h2>Per-holder withholding</h2>")
	b.WriteString("<table border=\"1\" cellpadding=\"4\"><tbody>")
	b.WriteString("<tr><th>Holder</th><th>Status</th><th>Rate</th><th>Gross</th><th>Withheld</th><th>Net</th><th>Payment</th></tr>")
	for _, p := range rows {
		b.WriteString("<tr>")
		b.WriteString("<td>" + html.EscapeString(shortAID(p.HolderAID)) + "</td>")
		b.WriteString("<td>" + html.EscapeString(taxStatusLabel(p.TaxStatus)) + "</td>")
		b.WriteString("<td>" + fmt.Sprintf("%.2f%%", float64(p.TreatyBps)/100) + "</td>")
		b.WriteString("<td>" + fmt.Sprintf("%d", p.GrossAtoms) + "</td>")
		b.WriteString("<td>" + fmt.Sprintf("%d", p.WithheldAtoms) + "</td>")
		b.WriteString("<td>" + fmt.Sprintf("%d", p.NetAtoms) + "</td>")
		b.WriteString("<td>" + html.EscapeString(p.State) + "</td>")
		b.WriteString("</tr>")
	}
	b.WriteString("</tbody></table>")
	return title, docPage("distribution-withholding-summary", title, b.String())
}

func (s *server) crsFatcaDoc(iss *Issuance, d *Distribution, rows []*DistPayment) (string, string) {
	title := fmt.Sprintf("SIMULATED annual CRS/FATCA report: %s (%s), run %s", iss.Ticker, iss.Name, d.ID)
	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>")
	b.WriteString(para("SIMULATED regulator / tax-authority remittance. This artifact demonstrates the annual CRS/FATCA reporting a registrar would file: the reportable-account math and this artifact's hash are real; no report is transmitted to any tax authority. The registrar entity is the reporting entity of record."))
	b.WriteString("<table border=\"1\" cellpadding=\"4\"><tbody>")
	b.WriteString("<tr><th>Holder</th><th>Tax status</th><th>Tax residencies</th><th>Gross (USDX atoms)</th><th>Withheld (USDX atoms)</th></tr>")
	for _, p := range rows {
		tr := ""
		if c, _ := s.stClaimsResidency(p.HolderAID); c != "" {
			tr = c
		}
		b.WriteString("<tr>")
		b.WriteString("<td>" + html.EscapeString(shortAID(p.HolderAID)) + "</td>")
		b.WriteString("<td>" + html.EscapeString(taxStatusLabel(p.TaxStatus)) + "</td>")
		b.WriteString("<td>" + html.EscapeString(tr) + "</td>")
		b.WriteString("<td>" + fmt.Sprintf("%d", p.GrossAtoms) + "</td>")
		b.WriteString("<td>" + fmt.Sprintf("%d", p.WithheldAtoms) + "</td>")
		b.WriteString("</tr>")
	}
	b.WriteString("</tbody></table>")
	b.WriteString(para(fmt.Sprintf("Record-date height: Sequentia block %d. Gross total %d, withheld total %d USDX atoms.", d.SnapshotHeight, d.GrossTotal, d.WithheldTotal)))
	return title, docPage("distribution-crs-fatca", title, b.String())
}

// stClaimsResidency is a small helper for the CRS artifact: the holder's declared
// residence, or empty when no claims record exists. Bound to the server so the doc
// builders (methods) can read the store.
func (s *server) stClaimsResidency(aid string) (string, error) {
	c, err := s.st.ClaimsByAID(aid)
	if err != nil || c == nil {
		return "", err
	}
	return c.Residence, nil
}

func row2(k, v string) string {
	return "<tr><th>" + html.EscapeString(k) + "</th><td>" + html.EscapeString(v) + "</td></tr>"
}

func shortAID(aid string) string {
	if len(aid) <= 12 {
		return aid
	}
	return aid[:12]
}

func taxStatusLabel(s string) string {
	switch s {
	case "w9":
		return "US person (W-9)"
	case "w8ben":
		return "non-US person (W-8BEN)"
	default:
		return "undetermined"
	}
}
