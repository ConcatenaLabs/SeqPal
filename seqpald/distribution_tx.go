package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/ConcatenaLabs/SeqPal/seqpald/elements"
)

// Paying a distribution in ONE transaction, from the issuer's own funds.
//
// Once the register is snapshotted, executing the run without a transaction
// returns the payout: one output per holder -- each holder's registered payout
// address, the net amount after withholding, in the run's asset -- as a list
// and as an unfunded transaction. The issuer's own wallet funds it (adds its
// inputs, its change and the network fee, in whichever accepted asset it
// chooses) and signs it, and the issuer sends it back. This platform then
// checks that every holder's output is in it EXACTLY -- the right script, the
// right asset, the right amount, in the clear -- one output per holder, has the
// node check the whole transaction, and broadcasts it. Every holder's row
// records the one txid and the output that paid it.
//
// FUND SAFETY is the same invariant every payout here keeps, on one
// transaction instead of many: the run records the transaction's bytes and
// txid, and every row its output, BEFORE the broadcast; a lost write is healed
// by broadcasting those same bytes again, which can never pay anyone twice.
// Nothing the platform holds is spent: there is no servicing deposit.

type executeDistReq struct {
	Tx string `json:"tx"`
}

func (s *server) executeIssuerFunded(w http.ResponseWriter, r *http.Request, acct *Account, iss *Issuance, d *Distribution) {
	var req executeDistReq
	_ = readJSON(r, &req)
	// A payout already on its way: heal a lost write by sending the same bytes.
	if d.State == "paying" && d.PayoutTx != "" {
		if err := s.broadcastPayout(iss, d); err != nil {
			writeErr(w, 409, "the payout transaction %s did not broadcast: %v", d.PayoutTxid, err)
			return
		}
		d, _ = s.st.DistributionByID(d.ID)
		s.writeDistributionView(w, d)
		return
	}
	if strings.TrimSpace(req.Tx) == "" {
		s.writePayoutPlan(w, iss, d)
		return
	}
	rows, err := s.st.DistPaymentsByRun(d.ID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	var planned []*DistPayment
	for _, p := range rows {
		if p.State == "planned" {
			planned = append(planned, p)
		}
	}
	if len(planned) == 0 {
		writeErr(w, 409, "there is no planned payout to pay: execute without a transaction first")
		return
	}
	raw, err := hex.DecodeString(strings.TrimSpace(req.Tx))
	if err != nil {
		writeErr(w, 400, "tx must be the signed transaction in hex")
		return
	}
	tx, err := elements.DeserializeTx(raw)
	if err != nil {
		writeErr(w, 400, "the transaction does not decode: %v", err)
		return
	}
	vouts, err := matchPayoutOutputs(tx, d.AssetPayCcy(s), planned)
	if err != nil {
		writeErr(w, 400, "%v; nothing was broadcast", err)
		return
	}
	txid := tx.TxID()
	signed := hex.EncodeToString(tx.Serialize())
	// The node checks the transaction whole -- every input signed, the fee paid
	// in an asset it accepts -- before anything is recorded as paying.
	if err := s.testAccept(signed); err != nil && !alreadyBroadcast(err.Error()) {
		writeErr(w, 409, "%v; nothing was broadcast", err)
		return
	}
	// A node that already has it -- the issuer's own wallet broadcast it -- is
	// not a refusal: the payout is recorded and confirmed like any other.
	// The intent, with the exact bytes and every holder's output, before the broadcast.
	if err := s.st.UpdateDistributionFields(d.ID, map[string]any{"state": "paying", "payout_tx": signed, "payout_txid": txid}); err != nil {
		writeErr(w, 500, "store error")
		return
	}
	for i, p := range planned {
		_ = s.st.UpdateDistPaymentFields(d.ID, p.HolderAID, map[string]any{"state": "paying", "txid": txid, "vout": vouts[i]})
	}
	s.st.Audit(acct.AID, "distribution.execute.begin", map[string]any{"issuance_id": iss.ID, "run": d.ID, "txid": txid,
		"holders": len(planned)})
	d.State, d.PayoutTx, d.PayoutTxid = "paying", signed, txid
	if err := s.broadcastPayout(iss, d); err != nil {
		if !isRefusal(err) {
			// The node was not reached: the payout may have gone out. It stays
			// recorded as paying, and the next execute sends the same bytes again;
			// the issuer is never asked for a second, different transaction.
			writeErr(w, 502, "the payout %s could not be confirmed broadcast (%v); execute again to retry the "+
				"same transaction", txid, err)
			return
		}
		// The node refused it: nothing was paid, so the rows go back to planned
		// and the issuer can send a corrected transaction.
		_ = s.st.UpdateDistributionFields(d.ID, map[string]any{"state": "snapshotted", "payout_tx": "", "payout_txid": ""})
		for _, p := range planned {
			_ = s.st.UpdateDistPaymentFields(d.ID, p.HolderAID, map[string]any{"state": "planned", "txid": "", "vout": -1})
		}
		writeErr(w, 409, "%v; nothing was paid", err)
		return
	}
	d, _ = s.st.DistributionByID(d.ID)
	s.writeDistributionView(w, d)
}

// AssetPayCcy is the asset a run pays in. Distributions pay USDX.
func (d *Distribution) AssetPayCcy(s *server) string { return strings.ToLower(s.cfg.usdxAsset) }

// matchPayoutOutputs finds, for each planned row, a distinct output paying
// exactly its script, its asset and its net amount, in the clear. It returns
// the output index per row, in row order, or says which row is missing.
func matchPayoutOutputs(tx *elements.Tx, asset string, rows []*DistPayment) ([]int, error) {
	want, err := explicitAsset(asset)
	if err != nil {
		return nil, err
	}
	used := map[int]bool{}
	out := make([]int, len(rows))
	for i, p := range rows {
		spk, err := hex.DecodeString(p.MandateSPK)
		if err != nil || len(spk) == 0 {
			return nil, fmt.Errorf("holder %s has no payout script recorded", shortAID(p.HolderAID))
		}
		found := -1
		for n, o := range tx.Out {
			if used[n] {
				continue
			}
			v, ok := elements.ExplicitValueAmount(o.Value)
			if !ok || v != p.NetAtoms || !bytes.Equal(o.Asset, want) || !bytes.Equal(o.ScriptPubKey, spk) {
				continue
			}
			if len(o.Nonce) > 1 || (len(o.Nonce) == 1 && o.Nonce[0] != 0) {
				continue // a blinding nonce: not the plain payment the holder was promised
			}
			found = n
			break
		}
		if found < 0 {
			return nil, fmt.Errorf("the transaction does not pay holder %s exactly %d atoms of %s at %s",
				shortAID(p.HolderAID), p.NetAtoms, asset, p.MandateAddress)
		}
		used[found] = true
		out[i] = found
	}
	return out, nil
}

// testAccept asks the node whether it would accept a finished transaction.
func (s *server) testAccept(txHex string) error {
	res, err := s.nodeRPC("testmempoolaccept", []string{txHex})
	if err != nil {
		return err
	}
	var acc []struct {
		Allowed      bool   `json:"allowed"`
		RejectReason string `json:"reject-reason"`
	}
	if err := json.Unmarshal(res, &acc); err != nil || len(acc) != 1 {
		return fmt.Errorf("testmempoolaccept: unreadable answer")
	}
	if !acc[0].Allowed {
		return fmt.Errorf("the node refuses the transaction: %s", acc[0].RejectReason)
	}
	return nil
}

// broadcastPayout broadcasts the run's recorded payout and, once the node has
// it, marks every holder paid, writes the ledger and completes the run.
func (s *server) broadcastPayout(iss *Issuance, d *Distribution) error {
	if _, err := s.broadcastRaw(d.PayoutTx); err != nil {
		// Already mined and its outputs since spent is the one refusal that
		// still means paid: any holder's output still unspent says so.
		landed := false
		rows, _ := s.st.DistPaymentsByRun(d.ID)
		for _, p := range rows {
			if p.State == "paying" && p.Vout >= 0 {
				if o, e := s.unspentOut(d.PayoutTxid, uint32(p.Vout)); e == nil && o != nil {
					landed = true
					break
				}
			}
		}
		if !landed {
			return err
		}
	}
	rows, err := s.st.DistPaymentsByRun(d.ID)
	if err != nil {
		return err
	}
	for _, p := range rows {
		if p.State != "paying" {
			continue
		}
		_ = s.st.UpdateDistPaymentFields(d.ID, p.HolderAID, map[string]any{"state": "paid", "reason": ""})
		_ = s.st.InsertLedger(&LedgerEntry{
			IssuanceID: d.IssuanceID, Kind: "distribution", Rail: "usdx",
			Amount: p.NetAtoms, Ccy: "USDX", Txid: d.PayoutTxid,
		})
		s.st.Audit(p.HolderAID, "distribution.pay", map[string]any{
			"run": d.ID, "txid": d.PayoutTxid, "vout": p.Vout, "net_atoms": p.NetAtoms, "withheld_atoms": p.WithheldAtoms,
		})
	}
	rows, _ = s.st.DistPaymentsByRun(d.ID)
	for _, p := range rows {
		if p.State != "paid" && p.State != "skipped" && p.State != "no_payment" {
			// Holders left to pay (their payout address could not be checked when
			// the payout was planned): the paid rows keep this txid, and the run
			// goes back to be planned and paid again for the rest.
			_ = s.st.UpdateDistributionFields(d.ID, map[string]any{"state": "snapshotted", "payout_tx": "", "payout_txid": ""})
			return nil
		}
	}
	s.finalizeDistributionArtifacts(iss, d, rows)
	_ = s.st.UpdateDistributionFields(d.ID, map[string]any{"state": "complete"})
	s.st.Audit("", "distribution.complete", map[string]any{"issuance_id": iss.ID, "run": d.ID, "txid": d.PayoutTxid})
	return nil
}

// writePayoutPlan settles who is paid what and where, once, and returns it:
// the outputs, an unfunded transaction carrying exactly them, and how the
// issuer's wallet funds and signs it.
func (s *server) writePayoutPlan(w http.ResponseWriter, iss *Issuance, d *Distribution) {
	rows, err := s.st.DistPaymentsByRun(d.ID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	asset := d.AssetPayCcy(s)
	for _, p := range rows {
		if p.State != "pending" && p.State != "failed" {
			continue
		}
		if p.NetAtoms == 0 {
			_ = s.st.UpdateDistPaymentFields(d.ID, p.HolderAID, map[string]any{"state": "no_payment", "reason": "net zero after withholding"})
			continue
		}
		m, err := s.st.InvestorMandateFor(p.HolderAID, "sequentia")
		if err != nil || m == nil || m.Address == "" {
			_ = s.st.UpdateDistPaymentFields(d.ID, p.HolderAID, map[string]any{"state": "skipped", "reason": "no registered payout mandate"})
			_ = s.st.InsertNotice(p.HolderAID, "distribution",
				"A distribution to you was skipped because you have no registered payout address. Register a Sequentia payout mandate in your portal to receive it.")
			s.st.Audit(p.HolderAID, "distribution.skip.no_mandate", map[string]any{"run": d.ID})
			continue
		}
		// Never pay an enclave key-path address, which no wallet scans; fail
		// closed when that cannot be checked, leaving the row to plan later.
		if isEnclave, err := s.isInvestorEnclaveAddress(p.HolderAID, m.Address); err != nil {
			_ = s.st.UpdateDistPaymentFields(d.ID, p.HolderAID, map[string]any{"state": "failed", "reason": "enclave-address check unavailable; will retry"})
			continue
		} else if isEnclave {
			_ = s.st.UpdateDistPaymentFields(d.ID, p.HolderAID, map[string]any{"state": "skipped", "reason": "mandate address resolves to an enclave key-path address"})
			continue
		}
		spk, err := s.addressSPK(m.Address)
		if err != nil {
			_ = s.st.UpdateDistPaymentFields(d.ID, p.HolderAID, map[string]any{"state": "failed", "reason": "payout address unreadable: " + err.Error()})
			continue
		}
		_ = s.st.UpdateDistPaymentFields(d.ID, p.HolderAID, map[string]any{"state": "planned", "mandate_address": m.Address,
			"mandate_spk": spk, "reason": ""})
	}
	rows, _ = s.st.DistPaymentsByRun(d.ID)
	tx := &elements.Tx{Version: 2}
	outputs := []map[string]any{}
	var total uint64
	for _, p := range rows {
		if p.State != "planned" {
			continue
		}
		spk, _ := hex.DecodeString(p.MandateSPK)
		o, err := newTxOut(asset, p.NetAtoms, spk)
		if err != nil {
			writeErr(w, 500, "%v", err)
			return
		}
		tx.Out = append(tx.Out, o)
		total += p.NetAtoms
		outputs = append(outputs, map[string]any{"holder_aid": p.HolderAID, "address": p.MandateAddress,
			"script_pubkey": p.MandateSPK, "asset": asset, "atoms": p.NetAtoms, "amount": amount8(p.NetAtoms)})
	}
	plan := map[string]any{"outputs": outputs, "total_atoms": total, "asset": asset}
	if len(outputs) > 0 {
		unfunded := hex.EncodeToString(tx.Serialize())
		plan["unfunded_tx"] = unfunded
		plan["fund_command"] = fmt.Sprintf("sequentia-cli -rpcwallet=<your wallet> fundrawtransaction %s '{\"fee_asset\":\"%s\"}'", unfunded, asset)
		plan["sign_command"] = "sequentia-cli -rpcwallet=<your wallet> signrawtransactionwithwallet <the hex fundrawtransaction returned>"
		plan["note"] = "Fund and sign this in your own wallet: it adds your inputs, your change and the network " +
			"fee (in any asset the network accepts; the command above pays it in the distribution's own asset). " +
			"Then send the signed hex back as {\"tx\": ...}. Every holder's output must be in it exactly as listed."
	}
	runRows, _ := s.st.DistPaymentsByRun(d.ID)
	writeJSON(w, 200, map[string]any{"distribution": d, "payments": runRows, "payout": plan,
		"label": "real USDX pro-rata distribution; withholding remittance to tax authorities is out of scope and labeled simulated"})
}
