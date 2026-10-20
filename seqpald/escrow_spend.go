package main

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Spending the subscription escrow.
//
// Three kinds of transaction spend an escrow coin, one per leaf:
//
//	release  every delivered subscription's coins, after the close, output 0
//	         the raise to the issuer. The issuer signs it.
//	cancel   chosen subscriptions' coins back to their subscribers, at any time.
//	         The issuer signs it.
//	refund   every undelivered subscription's coins back to their subscribers,
//	         once the deadline has passed. Nobody signs it; this platform builds
//	         and broadcasts it unprompted, and anyone else can too.
//
// For the two keyed kinds this platform hands the issuer the TRANSACTION, never
// a digest: the unsigned transaction and, per input, the leaf script, its
// control block, the key and the sighash this platform computed. The issuer's
// wallet recomputes each sighash from the transaction itself and checks that
// the control block commits to the coin it spends before it signs --
// `signopenamptransfer` in a Sequentia node wallet does exactly that -- so a
// signature made there authorises that one transaction and nothing else.
// Before anything is broadcast, every returned signature is verified here
// against the release key over the sighash computed here, and the finished
// transaction is checked by the node (testmempoolaccept).
//
// FUND SAFETY. Each spend is recorded before it is broadcast, with its txid,
// which a segwit-style transaction fixes before it is signed. The signed bytes
// are recorded before the broadcast too, so a lost write is healed by
// broadcasting the same bytes again: it is one transaction, it can only be
// mined once, and a node that already has it says so. A new spend is never
// built over a coin that an unbroadcast-or-broadcast spend already holds.

type EscrowSpend struct {
	ID         string         `json:"id"`
	IssuanceID string         `json:"issuance_id"`
	Kind       string         `json:"kind"`  // release | cancel | refund
	State      string         `json:"state"` // built | broadcasting | broadcast | superseded | failed
	Txid       string         `json:"txid"`
	UnsignedTx string         `json:"unsigned_tx"`
	SignedTx   string         `json:"signed_tx,omitempty"`
	Inputs     []escrowInput  `json:"inputs"`
	Detail     map[string]any `json:"detail"`
	Error      string         `json:"error,omitempty"`
	CreatedAt  int64          `json:"created_at"`
	UpdatedAt  int64          `json:"updated_at"`
}

// --- store -----------------------------------------------------------------------

func (s *Store) InsertEscrowSpend(sp *EscrowSpend) error {
	now := time.Now().Unix()
	sp.CreatedAt, sp.UpdatedAt = now, now
	ins, _ := json.Marshal(sp.Inputs)
	det, _ := json.Marshal(sp.Detail)
	_, err := s.db.Exec(`INSERT INTO escrow_spends (id, issuance_id, kind, state, txid, unsigned_tx, signed_tx,
        inputs, detail, error, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		sp.ID, sp.IssuanceID, sp.Kind, sp.State, sp.Txid, sp.UnsignedTx, sp.SignedTx, string(ins), string(det),
		sp.Error, now, now)
	if err != nil {
		return err
	}
	// The record is what everything after this reads, so the in-memory spend
	// is put in the same shape as one read back from the store.
	sp.Detail = map[string]any{}
	_ = json.Unmarshal(det, &sp.Detail)
	return nil
}

const spendCols = `id, issuance_id, kind, state, txid, unsigned_tx, signed_tx, inputs, detail, error, created_at, updated_at`

func scanSpend(sc scanner) (*EscrowSpend, error) {
	var sp EscrowSpend
	var ins, det string
	if err := sc.Scan(&sp.ID, &sp.IssuanceID, &sp.Kind, &sp.State, &sp.Txid, &sp.UnsignedTx, &sp.SignedTx,
		&ins, &det, &sp.Error, &sp.CreatedAt, &sp.UpdatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(ins), &sp.Inputs)
	_ = json.Unmarshal([]byte(det), &sp.Detail)
	return &sp, nil
}

func (s *Store) EscrowSpendByID(id string) (*EscrowSpend, error) {
	sp, err := scanSpend(s.db.QueryRow(`SELECT `+spendCols+` FROM escrow_spends WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return sp, err
}

func (s *Store) querySpends(q string, args ...any) ([]*EscrowSpend, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*EscrowSpend{}
	for rows.Next() {
		sp, err := scanSpend(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

func (s *Store) EscrowSpendsByIssuance(issuanceID string) ([]*EscrowSpend, error) {
	return s.querySpends(`SELECT `+spendCols+` FROM escrow_spends WHERE issuance_id = ? ORDER BY created_at`, issuanceID)
}

func (s *Store) EscrowSpendsByState(state string) ([]*EscrowSpend, error) {
	return s.querySpends(`SELECT `+spendCols+` FROM escrow_spends WHERE state = ? ORDER BY created_at`, state)
}

// TransitionEscrowSpend moves a spend from one state to another only if it is
// still in the first, and reports whether it did: what applies a broadcast to
// the books runs once, whoever gets there first.
func (s *Store) TransitionEscrowSpend(id, from, to string) (bool, error) {
	res, err := s.db.Exec(`UPDATE escrow_spends SET state = ?, error = '', updated_at = ? WHERE id = ? AND state = ?`,
		to, time.Now().Unix(), id, from)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (s *Store) UpdateEscrowSpendFields(id string, fields map[string]any) error {
	return s.updateFields("escrow_spends", "id", id, fields)
}

func (s *Store) RecordEscrowCoin(issuanceID string, c escrowCoin) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(`INSERT INTO escrow_coins (txid, vout, subscription_id, issuance_id, asset, atoms, height,
        created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?) ON CONFLICT(txid, vout) DO NOTHING`,
		c.Txid, c.Vout, c.SubscriptionID, issuanceID, c.Asset, c.Atoms, c.Height, now, now)
	return err
}

type EscrowCoinRow struct {
	escrowCoin
	SpentTxid string `json:"spent_txid,omitempty"`
}

func (s *Store) EscrowCoinsByIssuance(issuanceID string) ([]*EscrowCoinRow, error) {
	return s.queryCoins(`SELECT txid, vout, subscription_id, asset, atoms, height, spent_txid FROM escrow_coins
        WHERE issuance_id = ? ORDER BY created_at, txid, vout`, issuanceID)
}

func (s *Store) EscrowCoinsBySubscription(subID string) ([]*EscrowCoinRow, error) {
	return s.queryCoins(`SELECT txid, vout, subscription_id, asset, atoms, height, spent_txid FROM escrow_coins
        WHERE subscription_id = ? ORDER BY created_at, txid, vout`, subID)
}

func (s *Store) queryCoins(q string, args ...any) ([]*EscrowCoinRow, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*EscrowCoinRow{}
	for rows.Next() {
		var c EscrowCoinRow
		if err := rows.Scan(&c.Txid, &c.Vout, &c.SubscriptionID, &c.Asset, &c.Atoms, &c.Height, &c.SpentTxid); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

func (s *Store) MarkEscrowCoinSpent(txid string, vout uint32, spentTxid string) error {
	_, err := s.db.Exec(`UPDATE escrow_coins SET spent_txid = ?, updated_at = ? WHERE txid = ? AND vout = ?`,
		spentTxid, time.Now().Unix(), txid, vout)
	return err
}

// --- instances and coins -----------------------------------------------------------

// isCovenantSub is a subscription paid into the escrow covenant.
func isCovenantSub(sub *Subscription) bool { return sub.Rail == "usdx" && sub.EscrowSPK != "" }

// escrowParamsFor is a subscription's parameters under its offering's terms.
func escrowParamsFor(t *EscrowTerms, sub *Subscription) escrowParams {
	return escrowParams{
		PayAsset: t.PayAsset, IssuerKey: t.ReleaseKey, IssuerSPK: t.IssuerSPK, Minimum: t.MinimumAtoms,
		Close: t.CloseLock, RefundSPK: sub.RefundSPK, Deadline: t.DeadlineLock, FeeCap: t.FeeCap,
		Subscription: sub.EscrowRef,
	}
}

// escrowInstanceFor re-derives a subscription's escrow output and refuses one
// that no longer derives to the address the subscriber was given.
func (s *server) escrowInstanceFor(t *EscrowTerms, sub *Subscription) (*escrowInstance, error) {
	e, err := deriveEscrow(escrowParamsFor(t, sub), s.cfg.network)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(hex.EncodeToString(e.SPK), sub.EscrowSPK) {
		return nil, fmt.Errorf("subscription %s no longer derives to its escrow address", sub.ID)
	}
	return e, nil
}

// liveEscrowCoins reads every coin of the payment asset now at the given
// subscriptions' escrow addresses, from the UTXO set, records each, and
// returns them in a fixed order with each subscription's instance.
func (s *server) liveEscrowCoins(t *EscrowTerms, subs []*Subscription) ([]escrowCoin, map[string]*escrowInstance, error) {
	inst := map[string]*escrowInstance{}
	bySPK := map[string]*Subscription{}
	spks := []string{}
	for _, sub := range subs {
		e, err := s.escrowInstanceFor(t, sub)
		if err != nil {
			return nil, nil, err
		}
		inst[sub.ID] = e
		k := strings.ToLower(sub.EscrowSPK)
		bySPK[k] = sub
		spks = append(spks, k)
	}
	found, _, err := s.scanSPKs(spks)
	if err != nil {
		return nil, nil, fmt.Errorf("could not read the escrow addresses from the chain: %w", err)
	}
	coins := []escrowCoin{}
	for _, sub := range subs {
		var mine []escrowCoin
		for _, u := range found[strings.ToLower(sub.EscrowSPK)] {
			if !strings.EqualFold(u.Asset, t.PayAsset) {
				continue
			}
			c := escrowCoin{Txid: u.Txid, Vout: u.Vout, Atoms: atomsFromNumber(u.Amount), Asset: strings.ToLower(u.Asset),
				Height: u.Height, SubscriptionID: sub.ID}
			_ = s.st.RecordEscrowCoin(t.IssuanceID, c)
			mine = append(mine, c)
		}
		sort.Slice(mine, func(i, j int) bool {
			if mine[i].Txid != mine[j].Txid {
				return mine[i].Txid < mine[j].Txid
			}
			return mine[i].Vout < mine[j].Vout
		})
		coins = append(coins, mine...)
	}
	return coins, inst, nil
}

// heldCoins is the set of coins a live spend (built, broadcasting or broadcast)
// already covers, which no other spend may be built over.
func (s *server) heldCoins(issuanceID, exceptKind string) (map[string]*EscrowSpend, error) {
	spends, err := s.st.EscrowSpendsByIssuance(issuanceID)
	if err != nil {
		return nil, err
	}
	out := map[string]*EscrowSpend{}
	for _, sp := range spends {
		switch sp.State {
		case "broadcasting", "broadcast":
		case "built":
			if sp.Kind == exceptKind {
				continue // a built spend of the kind being rebuilt is superseded, not held
			}
		default:
			continue
		}
		for _, in := range sp.Inputs {
			out[fmt.Sprintf("%s:%d", in.Txid, in.Vout)] = sp
		}
	}
	return out, nil
}

// supersedeBuilt marks every unsigned spend of a kind superseded: once a new
// one is built, a signature over an old one is a signature over a transaction
// this platform will not broadcast.
func (s *server) supersedeBuilt(issuanceID, kind, keep string) {
	spends, _ := s.st.EscrowSpendsByIssuance(issuanceID)
	for _, sp := range spends {
		if sp.Kind == kind && sp.State == "built" && sp.ID != keep {
			_ = s.st.UpdateEscrowSpendFields(sp.ID, map[string]any{"state": "superseded"})
		}
	}
}

// platformFeeSPK is where the platform's escrow fee is paid:
// SEQPALD_REVENUE_ADDRESS, or a fresh address of the platform's own wallet.
// It receives the platform's fee and nothing else.
func (s *server) platformFeeSPK() (string, []byte, error) {
	addr := strings.TrimSpace(s.cfg.revenueAddress)
	if addr == "" {
		a, err := s.newPlatformRevenueAddress()
		if err != nil {
			return "", nil, err
		}
		addr = a
	}
	spk, err := s.addressSPK(addr)
	if err != nil {
		return "", nil, err
	}
	b, _ := hex.DecodeString(spk)
	return addr, b, nil
}

// escrowFeePreview is the escrow fee a subscription would be charged now,
// without fixing it: what a dry run of the release prices.
func (s *server) escrowFeePreview(sub *Subscription) uint64 {
	if f, ok, err := s.st.AccruedEscrowFee(sub.ID); err == nil && ok {
		return f
	}
	if s.cfg.escrowFeeOverrideBps >= 0 {
		f, _ := escrowFeeSplit(sub, s.cfg.escrowFeeOverrideBps)
		return f
	}
	share, err := s.publishedEscrowFeeFor(sub, time.Now().Unix())
	if err != nil {
		return 0
	}
	return share.Fee
}

// --- building ---------------------------------------------------------------------

// buildReleaseSpend builds the offering's release over every coin of the given
// subscriptions. With record false it is a dry run: nothing is written and no
// fee is fixed.
func (s *server) buildReleaseSpend(iss *Issuance, t *EscrowTerms, subs []*Subscription, record bool) (*EscrowSpend, *releaseBuild, error) {
	if len(subs) == 0 {
		return nil, nil, fmt.Errorf("no subscription is ready to release")
	}
	coins, inst, err := s.liveEscrowCoins(t, subs)
	if err != nil {
		return nil, nil, err
	}
	held, err := s.heldCoins(iss.ID, "release")
	if err != nil {
		return nil, nil, err
	}
	free := coins[:0:0]
	for _, c := range coins {
		if sp := held[fmt.Sprintf("%s:%d", c.Txid, c.Vout)]; sp != nil {
			return nil, nil, fmt.Errorf("coin %s:%d is already in the %s transaction %s (%s)", c.Txid, c.Vout, sp.Kind, sp.Txid, sp.State)
		}
		free = append(free, c)
	}
	var fee uint64
	for _, sub := range subs {
		if record {
			f, _, _ := s.escrowFeeFor(sub)
			fee += f
		} else {
			fee += s.escrowFeePreview(sub)
		}
	}
	var platformAddr string
	var platformSPK []byte
	if fee > 0 {
		platformAddr, platformSPK, err = s.platformFeeSPK()
		if err != nil {
			return nil, nil, fmt.Errorf("the platform fee address: %w", err)
		}
	}
	rate, err := s.feeRateAtomsPerKvB(t.PayAsset)
	if err != nil {
		return nil, nil, err
	}
	// What each subscriber paid beyond their subscription goes back to them.
	paid := map[string]uint64{}
	for _, c := range free {
		paid[c.SubscriptionID] += c.Atoms
	}
	var excess []escrowExcess
	for _, sub := range subs {
		if paid[sub.ID] > sub.PayAmount && sub.PayAmount > 0 {
			excess = append(excess, escrowExcess{SubscriptionID: sub.ID, RefundSPK: sub.RefundSPK, Atoms: paid[sub.ID] - sub.PayAmount})
		}
	}
	b, err := buildReleaseReturning(t, free, inst, excess, fee, platformSPK, platformAddr, rate)
	if err != nil {
		return nil, nil, err
	}
	genesis, err := s.genesisHash()
	if err != nil {
		return nil, nil, err
	}
	if err := escrowSighashes(b.Tx, b.Inputs, genesis); err != nil {
		return nil, nil, err
	}
	sp := &EscrowSpend{
		ID: mustID(), IssuanceID: iss.ID, Kind: "release", State: "built", Txid: b.Tx.TxID(),
		UnsignedTx: hex.EncodeToString(b.Tx.Serialize()), Inputs: b.Inputs,
		Detail: map[string]any{
			"outputs": b.Outputs, "total_atoms": b.Total, "escrow_fee_atoms": b.EscrowFee,
			"network_fee_atoms": b.NetworkFee, "to_issuer_atoms": b.ToIssuer, "minimum_atoms": b.Minimum,
			"locktime": b.LockTime, "vsize": b.VSize, "fee_rate_atoms_per_kvb": b.FeePerKvB,
			"subscriptions": subIDs(subs),
		},
	}
	if !record {
		return sp, b, nil
	}
	if err := s.st.InsertEscrowSpend(sp); err != nil {
		return nil, nil, err
	}
	s.supersedeBuilt(iss.ID, "release", sp.ID)
	s.st.Audit("", "escrow.release_built", map[string]any{"issuance_id": iss.ID, "spend": sp.ID, "txid": sp.Txid,
		"to_issuer": b.ToIssuer, "escrow_fee": b.EscrowFee, "network_fee": b.NetworkFee, "coins": len(b.Inputs)})
	return sp, b, nil
}

// buildReturnSpend builds a cancel (signed by the issuer) or a refund (signed
// by nobody, valid from the deadline) over every coin of the given
// subscriptions. A refund needs no signature, so its witness is in place as
// soon as it is built.
func (s *server) buildReturnSpend(kind string, iss *Issuance, t *EscrowTerms, subs []*Subscription, record bool) (*EscrowSpend, error) {
	if len(subs) == 0 {
		return nil, fmt.Errorf("no subscription to return")
	}
	coins, inst, err := s.liveEscrowCoins(t, subs)
	if err != nil {
		return nil, err
	}
	held, err := s.heldCoins(iss.ID, kind)
	if err != nil {
		return nil, err
	}
	free := coins[:0:0]
	for _, c := range coins {
		if sp := held[fmt.Sprintf("%s:%d", c.Txid, c.Vout)]; sp != nil {
			if sp.Kind == "cancel" && sp.State == "built" && kind == "refund" {
				// An unsigned cancel does not hold a coin against the refund that
				// needs nobody's signature.
			} else {
				return nil, fmt.Errorf("coin %s:%d is already in the %s transaction %s (%s)", c.Txid, c.Vout, sp.Kind, sp.Txid, sp.State)
			}
		}
		free = append(free, c)
	}
	if len(free) == 0 {
		return nil, fmt.Errorf("there is nothing at these escrow addresses to return")
	}
	refund := map[string]string{}
	for _, sub := range subs {
		refund[sub.ID] = sub.RefundSPK
	}
	rate, err := s.feeRateAtomsPerKvB(t.PayAsset)
	if err != nil {
		return nil, err
	}
	b, err := buildReturn(kind, t, free, inst, refund, rate)
	if err != nil {
		return nil, err
	}
	genesis, err := s.genesisHash()
	if err != nil {
		return nil, err
	}
	if err := escrowSighashes(b.Tx, b.Inputs, genesis); err != nil {
		return nil, err
	}
	sp := &EscrowSpend{
		ID: mustID(), IssuanceID: iss.ID, Kind: kind, State: "built", Txid: b.Tx.TxID(),
		UnsignedTx: hex.EncodeToString(b.Tx.Serialize()), Inputs: b.Inputs,
		Detail: map[string]any{
			"outputs": b.Outputs, "network_fee_atoms": b.NetworkFee, "locktime": b.LockTime, "vsize": b.VSize,
			"fee_cap_atoms": t.FeeCap, "subscriptions": subIDs(subs),
		},
	}
	if kind == "refund" {
		tx, err := assembleEscrowWitness(sp.UnsignedTx, sp.Inputs, nil, genesis)
		if err != nil {
			return nil, err
		}
		sp.SignedTx = hex.EncodeToString(tx.Serialize())
	}
	if !record {
		return sp, nil
	}
	if err := s.st.InsertEscrowSpend(sp); err != nil {
		return nil, err
	}
	s.supersedeBuilt(iss.ID, kind, sp.ID)
	s.st.Audit("", "escrow."+kind+"_built", map[string]any{"issuance_id": iss.ID, "spend": sp.ID, "txid": sp.Txid,
		"coins": len(b.Inputs), "network_fee": b.NetworkFee})
	return sp, nil
}

func subIDs(subs []*Subscription) []string {
	out := make([]string, 0, len(subs))
	for _, s := range subs {
		out = append(out, s.ID)
	}
	return out
}

// --- what the issuer is handed -------------------------------------------------------

func (s *server) escrowSpendView(t *EscrowTerms, sp *EscrowSpend) map[string]any {
	v := map[string]any{
		"id": sp.ID, "kind": sp.Kind, "state": sp.State, "txid": sp.Txid, "detail": sp.Detail,
		"unsigned_tx": sp.UnsignedTx, "error": sp.Error,
	}
	if sp.SignedTx != "" && sp.Kind == "refund" {
		v["signed_tx"] = sp.SignedTx
	}
	if (sp.Kind == "release" || sp.Kind == "cancel") && sp.State == "built" {
		toSign := make([]map[string]any, 0, len(sp.Inputs))
		for _, in := range sp.Inputs {
			toSign = append(toSign, map[string]any{
				"vin": in.Vin, "sighash": in.Sighash, "xonlykey": in.XOnlyKey, "leaf": in.Script, "control": in.Control,
			})
		}
		v["to_sign"] = toSign
		v["sign_command"] = fmt.Sprintf("sequentia-cli -rpcwallet=<the wallet holding %s> signopenamptransfer '%s' '%s'",
			t.ReleaseKey, sp.UnsignedTx, jsonString(toSign))
		v["note"] = "Your wallet checks this transaction before it signs: it recomputes every sighash from the " +
			"transaction, checks that each leaf and control block commit to the escrow coin being spent, and " +
			"signs only with the release key. Send back the signatures it returns."
	}
	return v
}

// --- signing and broadcasting -------------------------------------------------------------

type spendSigsReq struct {
	Signatures json.RawMessage `json:"signatures"`
}

// parseSpendSigs reads the signatures in either form a wallet produces them:
// signopenamptransfer's [{vin, signature}], or {"<vin>": signature}.
func parseSpendSigs(raw json.RawMessage) (map[int]string, error) {
	out := map[int]string{}
	var list []struct {
		Vin       int    `json:"vin"`
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal(raw, &list); err == nil {
		for _, e := range list {
			out[e.Vin] = strings.ToLower(strings.TrimSpace(e.Signature))
		}
		return out, nil
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("signatures must be [{vin, signature}] or {\"<vin>\": signature}")
	}
	for k, v := range m {
		n, err := strconv.Atoi(k)
		if err != nil {
			return nil, fmt.Errorf("%q is not an input index", k)
		}
		out[n] = strings.ToLower(strings.TrimSpace(v))
	}
	return out, nil
}

// handleEscrowSpendSign is POST /api/issuances/{id}/escrow/spends/{sid}/sign
// (owner): the issuer's signatures over a built release or cancel. Each is
// verified before anything is broadcast.
func (s *server) handleEscrowSpendSign(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	defer s.st.LockIssuance(iss.ID)()
	t, err := s.st.EscrowTermsByIssuance(iss.ID)
	if err != nil || t == nil {
		writeErr(w, 404, "this offering has no escrow")
		return
	}
	sp, err := s.st.EscrowSpendByID(r.PathValue("sid"))
	if err != nil || sp == nil || sp.IssuanceID != iss.ID {
		writeErr(w, 404, "unknown escrow spend for this offering")
		return
	}
	switch sp.State {
	case "broadcast":
		writeJSON(w, 200, map[string]any{"spend": s.escrowSpendView(t, sp)})
		return
	case "broadcasting":
		s.finishBroadcast(t, sp)
		sp, _ = s.st.EscrowSpendByID(sp.ID)
		writeJSON(w, 200, map[string]any{"spend": s.escrowSpendView(t, sp)})
		return
	case "built":
	default:
		writeErr(w, 409, "this %s transaction is %s and will not be broadcast; use the current one", sp.Kind, sp.State)
		return
	}
	if sp.Kind != "release" && sp.Kind != "cancel" {
		writeErr(w, 409, "a %s needs no signature", sp.Kind)
		return
	}
	var req spendSigsReq
	if err := readJSON(r, &req); err != nil || len(req.Signatures) == 0 {
		writeErr(w, 400, "signatures are required")
		return
	}
	sigs, err := parseSpendSigs(req.Signatures)
	if err != nil {
		writeErr(w, 400, "%v", err)
		return
	}
	if sp.Kind == "release" {
		tip, mtp, err := s.chainTip()
		if err != nil {
			writeErr(w, 502, "could not read the chain tip: %v", err)
			return
		}
		if !lockReached(t.CloseLock, tip, mtp) {
			writeErr(w, 409, "the release cannot be mined before the close (%s); the chain is at block %d",
				describeLock(t.CloseLock), tip)
			return
		}
	}
	genesis, err := s.genesisHash()
	if err != nil {
		writeErr(w, 502, "%v", err)
		return
	}
	tx, err := assembleEscrowWitness(sp.UnsignedTx, sp.Inputs, sigs, genesis)
	if err != nil {
		writeErr(w, 400, "%v; nothing was broadcast", err)
		return
	}
	if tx.TxID() != sp.Txid {
		writeErr(w, 500, "the assembled transaction is not the one built; nothing was broadcast")
		return
	}
	signed := hex.EncodeToString(tx.Serialize())
	// The intent, with the exact bytes, before the broadcast.
	if err := s.st.UpdateEscrowSpendFields(sp.ID, map[string]any{"state": "broadcasting", "signed_tx": signed}); err != nil {
		writeErr(w, 500, "store error")
		return
	}
	sp.State, sp.SignedTx = "broadcasting", signed
	if err := s.finishBroadcast(t, sp); err != nil {
		writeErr(w, 409, "%v", err)
		return
	}
	sp, _ = s.st.EscrowSpendByID(sp.ID)
	writeJSON(w, 200, map[string]any{"spend": s.escrowSpendView(t, sp)})
}

// finishBroadcast broadcasts a spend whose signed bytes are recorded and
// applies what it did to the books, once. A node refusal that is not "already
// have it" leaves the spend where it was and returns the node's reason.
func (s *server) finishBroadcast(t *EscrowTerms, sp *EscrowSpend) error {
	if sp.State != "broadcasting" || sp.SignedTx == "" {
		return nil
	}
	txid, err := s.broadcastRaw(sp.SignedTx)
	if err != nil {
		// Refused. Either this transaction is already on chain and the node can
		// no longer tell (its coins are spent and so are its outputs), or a coin
		// it spends is gone to another transaction, or it is simply not valid
		// yet. Which one decides what the books say.
		landed, gone := s.spendLanded(sp)
		if landed {
			if ok, _ := s.st.TransitionEscrowSpend(sp.ID, "broadcasting", "broadcast"); ok {
				s.applyEscrowSpend(t, sp)
			}
			return nil
		}
		_ = s.st.UpdateEscrowSpendFields(sp.ID, map[string]any{"error": err.Error()})
		s.st.Audit("", "escrow."+sp.Kind+"_refused", map[string]any{"issuance_id": sp.IssuanceID, "spend": sp.ID, "error": err.Error()})
		switch {
		case gone:
			// Another transaction spent a coin of this one: it can never be mined.
			_ = s.st.UpdateEscrowSpendFields(sp.ID, map[string]any{"state": "failed"})
		case isRefusal(err) && sp.Kind == "refund":
			// The node will not take this refund as built -- its fee no longer
			// meets the node's floor, say. Nothing broadcast it, so it is set
			// aside and the next pass builds it again at the current rate.
			_ = s.st.UpdateEscrowSpendFields(sp.ID, map[string]any{"state": "superseded"})
		case isRefusal(err):
			// The node said no, so nothing broadcast it: back to built, to be
			// signed again or superseded. A keyless refund stays broadcasting and
			// is retried. Any other error -- a node not reached -- leaves the spend
			// broadcasting, because it may have gone out, and the watcher sends
			// the same bytes again until the node answers.
			_ = s.st.UpdateEscrowSpendFields(sp.ID, map[string]any{"state": "built", "signed_tx": ""})
		}
		return err
	}
	if txid != "" && !strings.EqualFold(txid, sp.Txid) {
		log.Printf("escrow: node reports txid %s for spend %s (recorded %s)", txid, sp.ID, sp.Txid)
	}
	ok, err := s.st.TransitionEscrowSpend(sp.ID, "broadcasting", "broadcast")
	if err != nil {
		return err
	}
	if ok {
		s.applyEscrowSpend(t, sp)
	}
	return nil
}

// spendLanded reports whether a spend's transaction is on chain, judged by
// any of its own outputs still being unspent (landed), and whether any coin it
// spends is gone (gone). Without a transaction index this is what the node can
// answer; a transaction whose every output is spent as well is reported gone,
// and the spend is left for a person to reconcile from the chain.
func (s *server) spendLanded(sp *EscrowSpend) (landed, gone bool) {
	nOut := 1
	if outs, ok := sp.Detail["outputs"].([]any); ok && len(outs) > 1 {
		nOut = len(outs) - 1 // the last is the fee output, which is not a coin
	}
	for n := 0; n < nOut; n++ {
		if o, err := s.unspentOut(sp.Txid, uint32(n)); err == nil && o != nil {
			return true, false
		}
	}
	for _, in := range sp.Inputs {
		o, err := s.unspentOut(in.Txid, in.Vout)
		if err == nil && o == nil {
			return false, true
		}
	}
	return false, false
}

// applyEscrowSpend records what a broadcast spend did: the coins it spent, and
// for each subscription it covered, its settlement and the ledger.
func (s *server) applyEscrowSpend(t *EscrowTerms, sp *EscrowSpend) {
	perSub := map[string]uint64{}
	order := []string{}
	for _, in := range sp.Inputs {
		_ = s.st.MarkEscrowCoinSpent(in.Txid, in.Vout, sp.Txid)
		if _, ok := perSub[in.SubscriptionID]; !ok {
			order = append(order, in.SubscriptionID)
		}
		perSub[in.SubscriptionID] += in.Atoms
	}
	returned := map[string]uint64{}
	if outs, ok := sp.Detail["outputs"].([]any); ok {
		for _, o := range outs {
			m, _ := o.(map[string]any)
			if m["pays"] == "subscriber" {
				id, _ := m["subscription_id"].(string)
				if a, ok := m["atoms"].(float64); ok {
					returned[id] += uint64(a)
				}
			}
		}
	}
	for _, id := range order {
		sub, _ := s.st.SubscriptionByID(id)
		if sub == nil {
			continue
		}
		_, _ = s.st.CreateSettlementIfAbsent(sub.ID, sub.IssuanceID)
		switch sp.Kind {
		case "release":
			fee, _, _ := s.escrowFeeFor(sub)
			net := minusFee(minusFee(perSub[id], returned[id]), fee)
			if returned[id] > 0 {
				_ = s.st.InsertLedger(&LedgerEntry{SubscriptionID: sub.ID, IssuanceID: sub.IssuanceID, Kind: "refund",
					Rail: sub.Rail, Amount: returned[id], Ccy: sub.PayCcy, Txid: sp.Txid})
			}
			_ = s.st.UpdateSettlementFields(sub.ID, map[string]any{"release_txid": sp.Txid, "fee_atoms": fee, "state": "settled"})
			_ = s.st.UpdateSubscriptionFields(sub.ID, map[string]any{"state": "settled"})
			_ = s.st.InsertLedger(&LedgerEntry{SubscriptionID: sub.ID, IssuanceID: sub.IssuanceID, Kind: "release",
				Rail: sub.Rail, Amount: net, Ccy: sub.PayCcy, Txid: sp.Txid})
			s.st.Audit(sub.InvestorAID, "settle.done", map[string]any{"issuance_id": sub.IssuanceID, "sub": sub.ID,
				"release_txid": sp.Txid, "escrow": true})
		case "cancel", "refund":
			// A coin that arrived after the subscription settled is returned
			// without touching how it settled.
			if sub.State != "settled" && sub.State != "refunded" {
				_ = s.st.UpdateSettlementFields(sub.ID, map[string]any{"refund_txid": sp.Txid, "state": "refunded"})
				_ = s.st.UpdateSubscriptionFields(sub.ID, map[string]any{"state": "refunded"})
			}
			_ = s.st.InsertLedger(&LedgerEntry{SubscriptionID: sub.ID, IssuanceID: sub.IssuanceID, Kind: "refund",
				Rail: sub.Rail, Amount: returned[id], Ccy: sub.PayCcy, Txid: sp.Txid})
			s.st.Audit(sub.InvestorAID, "settle.refund", map[string]any{"issuance_id": sub.IssuanceID, "sub": sub.ID,
				"refund_txid": sp.Txid, "path": sp.Kind, "refund_atoms": returned[id]})
		}
	}
	if sp.Kind == "release" {
		if nf, ok := sp.Detail["network_fee_atoms"].(float64); ok && nf > 0 {
			_ = s.st.InsertLedger(&LedgerEntry{IssuanceID: sp.IssuanceID, Kind: "network_fee", Rail: "usdx",
				Amount: uint64(nf), Ccy: "USDX", Txid: sp.Txid})
		}
	}
	s.st.Audit("", "escrow."+sp.Kind+"_broadcast", map[string]any{"issuance_id": sp.IssuanceID, "spend": sp.ID,
		"txid": sp.Txid, "subscriptions": order})
}

// --- cancel and the subscriber's own view ------------------------------------------------

type escrowCancelReq struct {
	SubscriptionIDs []string `json:"subscription_ids"`
}

// handleEscrowCancel is POST /api/issuances/{id}/escrow/cancel (owner): build a
// cancel returning chosen subscriptions -- or every one not yet delivered --
// to their subscribers, for the issuer to sign.
func (s *server) handleEscrowCancel(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	var req escrowCancelReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	defer s.st.LockIssuance(iss.ID)()
	t, err := s.st.EscrowTermsByIssuance(iss.ID)
	if err != nil || t == nil {
		writeErr(w, 404, "this offering has no escrow")
		return
	}
	want := map[string]bool{}
	for _, id := range req.SubscriptionIDs {
		want[id] = true
	}
	all, err := s.st.SubscriptionsByIssuance(iss.ID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	var subs []*Subscription
	for _, sub := range all {
		if !isCovenantSub(sub) || (len(want) > 0 && !want[sub.ID]) {
			continue
		}
		if sub.State != "created" && sub.State != "in_escrow" {
			continue
		}
		if st, _ := s.st.SettlementByID(sub.ID); escrowDeliveredOrDelivering(st) {
			// Its tokens are delivered, or a delivery is under way: its money is
			// the issuer's, released with the rest. Only an issuer that names it
			// returns it anyway -- the way out when a release can no longer
			// reach the minimum -- and never one whose delivery is unresolved.
			if !want[sub.ID] || st.State == "delivering" {
				continue
			}
		}
		subs = append(subs, sub)
	}
	if len(subs) == 0 {
		writeErr(w, 409, "no undelivered subscription with money in escrow to cancel")
		return
	}
	sp, err := s.buildReturnSpend("cancel", iss, t, subs, true)
	if err != nil {
		writeErr(w, 409, "could not build the cancel: %v", err)
		return
	}
	for _, sub := range subs {
		_, _ = s.st.CreateSettlementIfAbsent(sub.ID, iss.ID)
		_ = s.st.UpdateSettlementFields(sub.ID, map[string]any{"state": "awaiting_cancel"})
	}
	s.st.Audit(acct.AID, "escrow.cancel_requested", map[string]any{"issuance_id": iss.ID, "spend": sp.ID, "subscriptions": subIDs(subs)})
	writeJSON(w, 200, map[string]any{"spend": s.escrowSpendView(t, sp)})
}

// handleSubscriptionEscrow is GET /api/subscriptions/{id}/escrow (the
// subscriber): where the money is, under what terms, and -- once it is there --
// the finished refund transaction, which needs no signature and is valid from
// the deadline. The subscriber can keep it and broadcast it themselves, so they
// are repaid even if this platform and the issuer both disappear.
func (s *server) handleSubscriptionEscrow(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	sub, err := s.st.SubscriptionByID(r.PathValue("id"))
	if err != nil || sub == nil || sub.InvestorAID != acct.AID {
		writeErr(w, 404, "unknown subscription")
		return
	}
	if !isCovenantSub(sub) {
		writeErr(w, 409, "this subscription is not held by the escrow covenant")
		return
	}
	t, err := s.st.EscrowTermsByIssuance(sub.IssuanceID)
	if err != nil || t == nil {
		writeErr(w, 500, "the offering's escrow terms are missing")
		return
	}
	e, err := s.escrowInstanceFor(t, sub)
	if err != nil {
		writeErr(w, 500, "%v", err)
		return
	}
	out := map[string]any{
		"subscription_id": sub.ID, "escrow": s.escrowTermsView(t), "address": sub.DepositAddress,
		"script_pubkey": sub.EscrowSPK, "refund_script_pubkey": sub.RefundSPK, "subscription_ref": sub.EscrowRef,
		"params": e.Params,
		"refund_leaf": map[string]any{"script": hex.EncodeToString(e.Leaves["refund"].Script),
			"control": hex.EncodeToString(e.Leaves["refund"].Control)},
	}
	coins, _ := s.st.EscrowCoinsBySubscription(sub.ID)
	out["coins"] = coins
	if st, _ := s.st.SettlementByID(sub.ID); escrowDeliveredOrDelivering(st) {
		out["refund_note"] = "your tokens are delivered, so this payment is the issuer's to release"
	} else if sub.State == "created" || sub.State == "in_escrow" {
		if txHex, err := s.refundTxFor(t, sub, e, coins); err == nil {
			out["refund_tx"] = txHex
			out["refund_note"] = fmt.Sprintf("valid from %s: anyone can broadcast it, for example with "+
				"sequentia-cli sendrawtransaction <refund_tx>. If the money has been released to the issuer "+
				"first, it is spent and this transaction is no longer valid.", describeLock(t.DeadlineLock))
		} else {
			out["refund_note"] = err.Error()
		}
	}
	writeJSON(w, 200, out)
}

// refundTxFor is the finished keyless refund of a subscription's coins, built
// from the coins already recorded for it (each checked still unspent) rather
// than a fresh scan of the chain, which a page view has no business causing.
func (s *server) refundTxFor(t *EscrowTerms, sub *Subscription, e *escrowInstance, rows []*EscrowCoinRow) (string, error) {
	var coins []escrowCoin
	for _, c := range rows {
		if c.SpentTxid != "" || !strings.EqualFold(c.Asset, t.PayAsset) {
			continue
		}
		if o, err := s.unspentOut(c.Txid, c.Vout); err != nil || o == nil {
			continue
		}
		coins = append(coins, c.escrowCoin)
	}
	if len(coins) == 0 {
		return "", fmt.Errorf("no payment has been seen at this escrow address yet")
	}
	rate, err := s.feeRateAtomsPerKvB(t.PayAsset)
	if err != nil {
		return "", err
	}
	b, err := buildReturn("refund", t, coins, map[string]*escrowInstance{sub.ID: e}, map[string]string{sub.ID: sub.RefundSPK}, rate)
	if err != nil {
		return "", err
	}
	genesis, err := s.genesisHash()
	if err != nil {
		return "", err
	}
	tx, err := assembleEscrowWitness(hex.EncodeToString(b.Tx.Serialize()), b.Inputs, nil, genesis)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(tx.Serialize()), nil
}

// --- the watcher's part ---------------------------------------------------------------------

// watchEscrowSpends heals lost writes and refunds what the deadline has
// freed: a spend recorded as broadcasting is broadcast again (the same
// bytes), and once an offering's deadline has passed every subscription whose
// tokens were not delivered is refunded to its subscriber, by the keyless
// path, without waiting for anyone.
func (s *server) watchEscrowSpends() {
	if s.cfg.nodeURL == "" {
		return
	}
	if pending, err := s.st.EscrowSpendsByState("broadcasting"); err == nil {
		for _, sp := range pending {
			s.healBroadcast(sp.IssuanceID, sp.ID)
		}
	}
	terms, err := s.st.AllEscrowTerms()
	if err != nil {
		return
	}
	tip, mtp, err := s.chainTip()
	if err != nil {
		return
	}
	for _, t := range terms {
		if !lockReached(t.DeadlineLock, tip, mtp) {
			continue
		}
		s.refundAfterDeadline(t)
	}
}

// healBroadcast re-broadcasts one spend left broadcasting, under the
// offering's lock and from a fresh read, so it never races the handler that is
// broadcasting the same spend.
func (s *server) healBroadcast(issuanceID, spendID string) {
	defer s.st.LockIssuance(issuanceID)()
	sp, err := s.st.EscrowSpendByID(spendID)
	if err != nil || sp == nil || sp.State != "broadcasting" {
		return
	}
	if t, _ := s.st.EscrowTermsByIssuance(issuanceID); t != nil {
		_ = s.finishBroadcast(t, sp)
	}
}

// refundIdle remembers an offering whose last deadline pass found nothing to
// refund, so the next full scan of the UTXO set for it waits a while.
var refundIdle = struct {
	sync.Mutex
	at map[string]time.Time
}{at: map[string]time.Time{}}

const refundIdleWait = 5 * time.Minute

func (s *server) refundAfterDeadline(t *EscrowTerms) {
	refundIdle.Lock()
	last, idle := refundIdle.at[t.IssuanceID]
	refundIdle.Unlock()
	if idle && time.Since(last) < refundIdleWait {
		return
	}
	markIdle := func() {
		refundIdle.Lock()
		refundIdle.at[t.IssuanceID] = time.Now()
		refundIdle.Unlock()
	}
	iss, _ := s.st.IssuanceByID(t.IssuanceID)
	if iss == nil {
		return
	}
	defer s.st.LockIssuance(iss.ID)()
	all, err := s.st.SubscriptionsByIssuance(iss.ID)
	if err != nil {
		return
	}
	var subs []*Subscription
	for _, sub := range all {
		if !isCovenantSub(sub) {
			continue
		}
		// A subscription whose tokens are delivered (or being delivered) and
		// whose money is not yet released is the issuer's to release. Every
		// other one is refunded: one never delivered, and -- for a coin that
		// arrived after the release or after a refund -- one already settled.
		st, _ := s.st.SettlementByID(sub.ID)
		if (sub.State == "created" || sub.State == "in_escrow") && escrowDeliveredOrDelivering(st) {
			continue
		}
		subs = append(subs, sub)
	}
	if len(subs) == 0 {
		return
	}
	// Only subscriptions that have coins, and none of whose coins a live
	// release, cancel or refund already holds: the builder refuses both, and a
	// held coin is already on its way somewhere.
	coins, _, err := s.liveEscrowCoins(t, subs)
	if err != nil {
		return
	}
	if len(coins) == 0 {
		markIdle()
		return
	}
	held, err := s.heldCoins(iss.ID, "")
	if err != nil {
		return
	}
	with, blocked := map[string]bool{}, map[string]bool{}
	for _, c := range coins {
		with[c.SubscriptionID] = true
		if sp := held[fmt.Sprintf("%s:%d", c.Txid, c.Vout)]; sp != nil && !(sp.Kind == "cancel" && sp.State == "built") {
			blocked[c.SubscriptionID] = true
		}
	}
	var due []*Subscription
	for _, sub := range subs {
		if with[sub.ID] && !blocked[sub.ID] {
			due = append(due, sub)
		}
	}
	if len(due) == 0 {
		return
	}
	sp, err := s.buildReturnSpend("refund", iss, t, due, true)
	if err != nil {
		// Logged, not audited: the watcher retries every tick.
		log.Printf("escrow: refund for %s after the deadline: %v", iss.ID, err)
		return
	}
	s.supersedeBuilt(iss.ID, "cancel", "")
	_ = s.st.UpdateEscrowSpendFields(sp.ID, map[string]any{"state": "broadcasting"})
	sp.State = "broadcasting"
	_ = s.finishBroadcast(t, sp)
}

// escrowDeliveredOrDelivering is a settlement whose tokens are delivered, or
// whose delivery is under way or was interrupted: its money is not refunded.
func escrowDeliveredOrDelivering(st *Settlement) bool {
	if st == nil {
		return false
	}
	switch st.State {
	case "delivering", "awaiting_release", "settled":
		return true
	}
	return st.DeliveryTxid != "" && st.State != "refunded"
}
