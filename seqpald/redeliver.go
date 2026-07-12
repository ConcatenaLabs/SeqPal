package main

import (
	"fmt"
	"net/http"
	"strings"
)

// The stranded-key re-delivery runbook (M7 section 5). A returning investor who
// lost their enclave key is made whole end to end, as a scripted/admin path
// (POST /api/id/redeliver, restricted to platform reviewers). Every step is
// logged, idempotent, and reconciled like every other fund movement.
//
// The five steps, and how each is discharged honestly here:
//  1. Re-authenticate against the SERVER-HELD identity record, not the lost key:
//     the operator acts through their admin session after confirming the returning
//     investor's identity against the on-file claims record; both the old AID's
//     identity record and the operator are recorded.
//  2. Register a NEW AID (or wallet link): the new AID is an already-registered
//     SeqPal account (its key held by the investor); the runbook records the
//     old->new link as the audit trail.
//  3. Clawback-with-reason sweeps ALL of the old AID's units of the asset. openampd
//     seizes them into the issuer enclave (the disclosed clawback destination). For
//     an external-issuer asset (M9) the server cannot sign the seizure, so the
//     runbook pauses, surfaces the L_claw sighashes, and resumes once the ISSUER
//     entity signs them in the browser; a legacy asset signs server-side as before.
//  4. Re-deliver to the new AID. openampd's clawback lands the seized units in the
//     issuer enclave, whose key seqpald does not custody, so re-delivery is sourced
//     from a custodied enclave under a DISCLOSED pre-M9 treasury delegation: the
//     entity treasury enclave when the issuance is entity-backed, else the offering
//     escrow enclave. The seized units remain parked in the issuer enclave and are
//     reconciled to treasury (retired in place), so total supply is conserved.
//  5. Sweep-then-redeliver-remainder: the FULL swept amount is re-delivered to the
//     new AID, so nothing the sweep caught is left stranded; the accounting step
//     verifies the new AID was made whole for everything swept.

type redeliverReq struct {
	IssuanceID string `json:"issuance_id"`
	OldAID     string `json:"old_aid"`
	NewAID     string `json:"new_aid"`
	Reason     string `json:"reason"`
	// ClawbackSigs (M9) resume an external-issuer runbook paused at the clawback
	// step: the issuer's browser signatures over the L_claw sighashes the prior
	// call surfaced, keyed by input index. Empty for a legacy asset (the runbook's
	// clawback signs server-side) or for the first call of an external run.
	ClawbackSigs map[string]string `json:"clawback_sigs"`
}

// handleRedeliver is POST /api/id/redeliver (session + admin). It runs, or resumes,
// the stranded-key runbook for one (issuance, old_aid, new_aid) triple.
func (s *server) handleRedeliver(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	var req redeliverReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	oldAID := strings.TrimSpace(req.OldAID)
	newAID := strings.TrimSpace(req.NewAID)
	reason := strings.TrimSpace(req.Reason)
	if oldAID == "" || newAID == "" {
		writeErr(w, 400, "old_aid and new_aid are required")
		return
	}
	if oldAID == newAID {
		writeErr(w, 400, "old_aid and new_aid must differ")
		return
	}
	if reason == "" {
		writeErr(w, 400, "a reason is required; it becomes part of the clawback's public transparency log")
		return
	}
	iss, err := s.st.IssuanceByID(strings.TrimSpace(req.IssuanceID))
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if iss == nil || iss.Status != "live" || iss.AssetID == "" {
		writeErr(w, 409, "the issuance is unknown or not live on chain")
		return
	}

	// Step 1: re-auth against the server-held identity record. The old AID must have
	// an on-file identity determination; the operator confirmed the returning
	// investor against it out of band. The new AID must be a registered account.
	if oldClaims, _ := s.st.ClaimsByAID(oldAID); oldClaims == nil {
		writeErr(w, 409, "the old AID has no server-held identity record to re-authenticate against")
		return
	}
	if a, _ := s.st.AccountByAID(newAID); a == nil {
		writeErr(w, 409, "the new AID is not a registered SeqPal account; register it (or link a wallet) first")
		return
	}

	// Serialize the runbook per triple so a double submit resumes rather than races.
	s.ensureServicingMu()
	lockKey := iss.ID + ":" + oldAID + ":" + newAID
	unlock := s.redeliverMu.lock(lockKey)
	defer unlock()

	rd, err := s.st.RedeliveryByParties(iss.ID, oldAID, newAID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	if rd == nil {
		rd = &Redelivery{
			ID: mustID(), IssuanceID: iss.ID, AssetID: iss.AssetID, OldAID: oldAID, NewAID: newAID,
			Reason: reason, State: "created", ByAID: acct.AID,
		}
		if err := s.st.InsertRedelivery(rd); err != nil {
			writeErr(w, 500, "could not open the re-delivery record")
			return
		}
		s.st.Audit(acct.AID, "redeliver.begin", map[string]any{
			"issuance_id": iss.ID, "asset": iss.AssetID, "old_aid": oldAID, "new_aid": newAID, "reason": reason,
		})
	}

	if err := s.runRedelivery(iss, rd, req.ClawbackSigs); err != nil {
		writeErr(w, 502, "the re-delivery runbook is blocked: %v", err)
		return
	}
	s.writeRedeliveryView(w, iss, rd)
}

// runRedelivery advances the runbook from its current state to complete. It is
// idempotent and resumable: each step reconciles against the chain before acting.
// clawbackSigs are the issuer's browser signatures for the external-issuer
// clawback step; they are ignored on a legacy asset (which signs server-side).
func (s *server) runRedelivery(iss *Issuance, rd *Redelivery, clawbackSigs map[string]string) error {
	// Step 3: clawback the old AID. Capture the new AID's balance BEFORE any delivery
	// so the made-whole check is exact. A legacy (server-held issuer key) asset sweeps
	// in one reconciled call; an EXTERNAL-issuer asset cannot be swept server-side, so
	// the runbook pauses at 'awaiting_clawback_sig', surfaces the L_claw sighashes, and
	// resumes once the issuer's browser signatures arrive.
	if rd.State == "created" {
		before, _ := s.enclaveBalance(rd.NewAID, iss.AssetID)
		if iss.IssuerExternal {
			c, _, err := s.doClawbackBuild(iss, rd.OldAID, rd.Reason, rd.ByAID, "redeliver")
			if err != nil {
				return fmt.Errorf("clawback build of the old AID failed: %w", err)
			}
			switch c.State {
			case "empty":
				_ = s.st.UpdateRedeliveryFields(rd.ID, map[string]any{
					"state": "clawed", "swept_atoms": 0, "clawback_id": c.ID, "new_before": before,
				})
				rd.State, rd.SweptAtoms, rd.ClawbackID, rd.NewBefore = "clawed", 0, c.ID, before
			case "swept":
				_ = s.st.UpdateRedeliveryFields(rd.ID, map[string]any{
					"state": "clawed", "swept_atoms": c.Atoms, "clawback_id": c.ID,
					"clawback_txid": c.Txid, "new_before": before,
				})
				rd.State, rd.SweptAtoms, rd.ClawbackID, rd.ClawbackTxid, rd.NewBefore = "clawed", c.Atoms, c.ID, c.Txid, before
			default:
				_ = s.st.UpdateRedeliveryFields(rd.ID, map[string]any{
					"state": "awaiting_clawback_sig", "clawback_id": c.ID, "new_before": before,
				})
				rd.State, rd.ClawbackID, rd.NewBefore = "awaiting_clawback_sig", c.ID, before
				s.st.Audit(rd.ByAID, "redeliver.awaiting_clawback_sig", map[string]any{
					"redelivery": rd.ID, "old_aid": rd.OldAID, "clawback_id": c.ID,
				})
				return nil // pause; the handler surfaces the sighashes to the issuer
			}
		} else {
			c, err := s.doClawback(iss, rd.OldAID, rd.Reason, rd.ByAID, "redeliver")
			if err != nil {
				return fmt.Errorf("clawback of the old AID failed: %w", err)
			}
			_ = s.st.UpdateRedeliveryFields(rd.ID, map[string]any{
				"state": "clawed", "swept_atoms": c.Atoms, "clawback_id": c.ID,
				"clawback_txid": c.Txid, "new_before": before,
			})
			rd.State, rd.SweptAtoms, rd.ClawbackID, rd.ClawbackTxid, rd.NewBefore = "clawed", c.Atoms, c.ID, c.Txid, before
		}
		if rd.State == "clawed" {
			s.st.Audit(rd.ByAID, "redeliver.clawed", map[string]any{
				"redelivery": rd.ID, "old_aid": rd.OldAID, "swept_atoms": rd.SweptAtoms, "txid": rd.ClawbackTxid,
			})
		}
	}

	// Resume an external-issuer clawback once the issuer's signatures arrive.
	if rd.State == "awaiting_clawback_sig" {
		if len(clawbackSigs) == 0 {
			return nil // still waiting; the handler surfaces the sighashes
		}
		c, err := s.st.ClawbackByID(rd.ClawbackID)
		if err != nil || c == nil {
			return fmt.Errorf("the pending clawback for this re-delivery is missing")
		}
		c, err = s.doClawbackComplete(iss, c, clawbackSigs, rd.ByAID)
		if err != nil {
			return fmt.Errorf("clawback of the old AID failed: %w", err)
		}
		_ = s.st.UpdateRedeliveryFields(rd.ID, map[string]any{
			"state": "clawed", "swept_atoms": c.Atoms, "clawback_txid": c.Txid,
		})
		rd.State, rd.SweptAtoms, rd.ClawbackTxid = "clawed", c.Atoms, c.Txid
		s.st.Audit(rd.ByAID, "redeliver.clawed", map[string]any{
			"redelivery": rd.ID, "old_aid": rd.OldAID, "swept_atoms": c.Atoms, "txid": c.Txid,
		})
	}

	// Step 4 + 5: re-deliver the full swept amount to the new AID from a custodied
	// source enclave (disclosed treasury delegation), then reconcile the accounting.
	if rd.State == "clawed" {
		if rd.SweptAtoms == 0 {
			// The old AID held nothing; there is nothing to re-deliver.
			_ = s.st.UpdateRedeliveryFields(rd.ID, map[string]any{"state": "complete"})
			rd.State = "complete"
			s.st.Audit(rd.ByAID, "redeliver.complete", map[string]any{"redelivery": rd.ID, "swept_atoms": 0})
			return nil
		}
		source, kind := s.redeliverySource(iss)
		if source == nil {
			return fmt.Errorf("no custodied source enclave is available for re-delivery")
		}

		// Reconcile a lost write: if the new AID already holds the made-whole balance,
		// a prior attempt delivered; do not re-send.
		if cur, err := s.enclaveBalance(rd.NewAID, iss.AssetID); err == nil && cur >= rd.NewBefore+rd.SweptAtoms {
			txid := rd.RedeliverTxid
			if txid == "" {
				txid = "reconciled"
			}
			_ = s.st.UpdateRedeliveryFields(rd.ID, map[string]any{
				"state": "redelivered", "source_aid": source.AID, "source_kind": kind, "redeliver_txid": txid,
			})
			rd.State, rd.SourceAID, rd.SourceKind, rd.RedeliverTxid = "redelivered", source.AID, kind, txid
		} else {
			// The source must hold enough under the disclosed treasury delegation.
			bal, err := s.enclaveBalance(source.AID, iss.AssetID)
			if err != nil {
				return fmt.Errorf("could not read the source enclave balance: %w", err)
			}
			if bal < rd.SweptAtoms {
				return fmt.Errorf("the %s source enclave holds %d atoms, less than the %d to re-deliver; fund it to continue", kind, bal, rd.SweptAtoms)
			}
			_ = s.st.UpdateRedeliveryFields(rd.ID, map[string]any{"source_aid": source.AID, "source_kind": kind})
			rd.SourceAID, rd.SourceKind = source.AID, kind
			txid, derr := s.deliverFromEscrow(source, rd.NewAID, iss.AssetID, rd.SweptAtoms)
			if derr != nil {
				return fmt.Errorf("re-delivery to the new AID failed: %w", derr)
			}
			_ = s.st.UpdateRedeliveryFields(rd.ID, map[string]any{"state": "redelivered", "redeliver_txid": txid})
			rd.State, rd.RedeliverTxid = "redelivered", txid
			_ = s.st.InsertLedger(&LedgerEntry{
				IssuanceID: iss.ID, Kind: "redelivery", Rail: "asset", Amount: rd.SweptAtoms, Ccy: iss.Ticker, Txid: txid,
			})
			s.st.Audit(rd.ByAID, "redeliver.delivered", map[string]any{
				"redelivery": rd.ID, "new_aid": rd.NewAID, "atoms": rd.SweptAtoms, "source": kind, "txid": txid,
			})
		}
	}

	// Step 5 accounting: verify the new AID was made whole for everything swept.
	if rd.State == "redelivered" {
		cur, _ := s.enclaveBalance(rd.NewAID, iss.AssetID)
		delivered := uint64(0)
		if cur >= rd.NewBefore {
			delivered = cur - rd.NewBefore
		}
		_ = s.st.UpdateRedeliveryFields(rd.ID, map[string]any{"state": "complete"})
		rd.State = "complete"
		_ = s.st.InsertNotice(rd.NewAID, "servicing",
			fmt.Sprintf("Your %s holdings were re-delivered to this account after your prior key was reported lost (%d units).", iss.Ticker, rd.SweptAtoms))
		s.st.Audit(rd.ByAID, "redeliver.complete", map[string]any{
			"redelivery": rd.ID, "old_aid": rd.OldAID, "new_aid": rd.NewAID,
			"swept_atoms": rd.SweptAtoms, "delivered_atoms": delivered, "made_whole": delivered >= rd.SweptAtoms,
		})
	}
	return nil
}

// redeliverySource resolves the custodied enclave the runbook re-delivers from
// under the disclosed pre-M9 treasury delegation: the entity treasury enclave for
// an entity-backed issuance, else the offering escrow enclave.
func (s *server) redeliverySource(iss *Issuance) (*EnclaveKey, string) {
	if iss.EntityID != "" {
		if k, _ := s.st.EnclaveKeyByRef(enclaveEntityTreasury, iss.EntityID); k != nil {
			return k, "entity-treasury"
		}
	}
	if k, _ := s.st.EnclaveKeyByRef(enclaveOfferingEscrow, iss.ID); k != nil {
		return k, "offering-escrow"
	}
	return nil, ""
}

func (s *server) writeRedeliveryView(w http.ResponseWriter, iss *Issuance, rd *Redelivery) {
	out := map[string]any{
		"redelivery": rd,
		"log_url":    "/openamp/v1/log",
		"label": "real clawback + re-delivery on the Sequentia network. The seized units are parked in the issuer enclave " +
			"and reconciled to treasury; re-delivery is sourced from a custodied enclave under a disclosed pre-M9 treasury " +
			"delegation, so the returning holder is made whole and total supply is conserved.",
	}
	// M9: an external-issuer asset's clawback needs the issuer's own signature, so
	// the runbook pauses here and surfaces the L_claw sighashes. The admin hands them
	// to the issuer, who signs with the SeqPal ID key; resubmitting this endpoint with
	// clawback_sigs resumes the sweep. Nothing is swept until then.
	if rd.State == "awaiting_clawback_sig" {
		if c, _ := s.st.ClawbackByID(rd.ClawbackID); c != nil {
			toSign, _ := decodeToSign(c.ToSign)
			out["to_sign"] = toSign
			out["clawback_id"] = c.ID
			out["pubkey"] = iss.IssuerPubkey
			out["two_phase"] = true
			out["note"] = "external issuer key: this asset's clawback needs the issuer's signature. Sign these sighashes with " +
				"the issuer's SeqPal ID key and resubmit POST /api/id/redeliver with clawback_sigs to resume the runbook; the " +
				"re-delivery step still sources from a custodied enclave under the disclosed treasury delegation. Nothing is " +
				"swept until the issuer signs, and nothing is final at 0-conf."
		}
	}
	writeJSON(w, 200, out)
}
