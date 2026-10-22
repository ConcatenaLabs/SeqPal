package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Payouts to many wallets at once: a distribution's holders, or a refund's
// subscribers, paid by ONE payment the issuer's own wallet makes.
//
// The payment service (SEQPALD_PAYOUT_URL) is an operator that pays many
// wallets from one round: each wallet gets an allocation of its own, under a
// key derived from a key the payer names, which the wallet collects by itself
// when it next opens; what nobody collects by the deadline the service pays
// back to the payer. This platform never pays and holds no key:
//
//  1. It settles the list: every holder (or subscriber), named by the key of
//     its OpenAMP account -- the key a SeqPal ID is -- and the amount. Anyone
//     it cannot name is left out with the reason, and their amount stays with
//     the issuer.
//  2. The issuer's own wallet makes the payment from the list and gives back
//     its reference (the service's distribution id).
//  3. The platform reads the service's public status for that reference and
//     records it only if it names EXACTLY the list: the asset, every key once,
//     every amount, nothing else.
//  4. It follows the status from then on: paid (each allocation is its
//     wallet's), collected, returned (and the transaction that returned it).
//
// A restricted asset is refused as the paid asset: its transfer rules travel
// with it, and an allocation of one would need a template that does not
// exist. A restricted security's holders are paid in an unrestricted asset
// such as USDX.

// --- the payment service ----------------------------------------------------------

// payoutCall posts to the payment service.
func (s *server) payoutCall(path string, body any, out any) error {
	if s.cfg.payoutURL == "" {
		return fmt.Errorf("payments to many wallets at once are not available on this deployment")
	}
	b, _ := json.Marshal(body)
	method := "POST"
	var rdr io.Reader = bytes.NewReader(b)
	if body == nil {
		method, rdr = "GET", nil
	}
	req, err := http.NewRequest(method, strings.TrimRight(s.cfg.payoutURL, "/")+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("the payment service is unreachable: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		// A refusal is {"error": {"code", "message"}}.
		var e struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		msg := e.Error.Message
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		return &payoutRefusal{status: resp.StatusCode, code: e.Error.Code, msg: msg}
	}
	return json.Unmarshal(raw, out)
}

type payoutRefusal struct {
	status    int
	code, msg string
}

func (e *payoutRefusal) Error() string {
	return fmt.Sprintf("the payment service answers %d %s: %s", e.status, e.code, e.msg)
}

// serviceInfo is what the payment service publishes about the assets it pays.
type serviceInfo struct {
	Assets []struct {
		Asset   string          `json:"asset"`
		MinLeaf string          `json:"min_leaf"`
		Fees    json.RawMessage `json:"fees"`
	} `json:"assets"`
	Fees json.RawMessage `json:"fees"`
}

// feeSchedule is the part of a fee schedule a payout's fee is made of.
type feeSchedule struct {
	RefreshPPM   uint64 `json:"refresh_ppm"`
	RefreshBase  string `json:"refresh_base"`
	OffboardBase string `json:"offboard_base"`
}

// assetTerms answers whether the service pays in `asset`, the smallest
// allocation it makes in it, and its fee schedule there.
func (s *server) assetTerms(asset string) (served bool, minAtoms uint64, fees feeSchedule, err error) {
	var info serviceInfo
	if err = s.payoutCall("/v1/info", nil, &info); err != nil {
		return
	}
	_ = json.Unmarshal(info.Fees, &fees)
	for _, a := range info.Assets {
		if !strings.EqualFold(a.Asset, asset) {
			continue
		}
		served = true
		minAtoms, _ = strconv.ParseUint(a.MinLeaf, 10, 64)
		if len(a.Fees) > 0 {
			var own map[string]json.RawMessage
			if json.Unmarshal(a.Fees, &own) == nil {
				if v, ok := own["refresh_ppm"]; ok {
					_ = json.Unmarshal(v, &fees.RefreshPPM)
				}
				if v, ok := own["refresh_base"]; ok {
					_ = json.Unmarshal(v, &fees.RefreshBase)
				}
				if v, ok := own["offboard_base"]; ok {
					_ = json.Unmarshal(v, &fees.OffboardBase)
				}
			}
		}
		return
	}
	return
}

// feeBound is the most the service's fee for a payment of `total` to `n`
// wallets can be: its refresh fee's fixed part per allocation, its offboard's
// fixed part once (for the return), and its refresh fee on the coin given up,
// counted at its full rate. The payer's wallet shows the exact figure before
// it signs; this is the ceiling the issuer is shown first.
func feeBound(fs feeSchedule, total uint64, n int) uint64 {
	base, _ := strconv.ParseUint(fs.RefreshBase, 10, 64)
	off, _ := strconv.ParseUint(fs.OffboardBase, 10, 64)
	fixed := base*uint64(n) + off
	coin := total + fixed
	var refresh uint64
	for i := 0; i < 3; i++ {
		refresh = mulDivCeil(coin+refresh, fs.RefreshPPM, 1_000_000) + base
	}
	return fixed + refresh
}

// mulDivCeil is ceil(a*b/c), exact.
func mulDivCeil(a, b, c uint64) uint64 {
	if c == 0 {
		return 0
	}
	prod := new(big.Int).Mul(new(big.Int).SetUint64(a), new(big.Int).SetUint64(b))
	q, m := new(big.Int).DivMod(prod, new(big.Int).SetUint64(c), new(big.Int))
	if m.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsUint64() {
		return 0
	}
	return q.Uint64()
}

// --- restricted assets ----------------------------------------------------------

// restrictedAsset reports why `asset` cannot be paid to many wallets at once,
// or "" when it can. Every token this platform issued carries rules: a
// serviced one settles only in OpenAMP's 2-of-2 accounts, a network-enforced
// one only to its whitelist, a freely-tradable one stays under its issuer's
// supervision. An asset the policy server knows is one of the first kind
// whoever issued it.
func (s *server) restrictedAsset(asset string) (string, error) {
	asset = strings.ToLower(strings.TrimSpace(asset))
	if iss, err := s.st.IssuanceByAsset(asset); err != nil {
		return "", err
	} else if iss != nil {
		return fmt.Sprintf("%s is a token issued here (%s), and its transfer rules travel with it", iss.Ticker, issuanceKindWord(iss)), nil
	}
	if s.cfg.openampURL != "" {
		var a map[string]any
		code, err := s.callOpenAMPStatus("GET", "/v1/assets/"+asset, "", nil, &a)
		if err == nil && code == 200 {
			return "it is a restricted asset whose transfers the policy server co-signs", nil
		}
		if code != 404 && code != 0 && err != nil {
			return "", fmt.Errorf("could not ask the policy server whether the asset is restricted: %v", err)
		}
	}
	return "", nil
}

func issuanceKindWord(iss *Issuance) string {
	switch iss.Enforcement {
	case "network":
		return "network-enforced"
	case "bearer":
		return "freely tradable, under its issuer's supervision"
	case "tree":
		return "transfers co-signed by the policy server, in the issuer's tree as on the chain"
	default:
		return "transfers co-signed by the policy server"
	}
}

const restrictedNote = "A payment to many wallets at once cannot carry a restricted asset's rules yet. " +
	"Pay in an unrestricted asset, such as USDX."

// refuseRestricted writes the refusal for a restricted paid asset and reports
// whether it did.
func (s *server) refuseRestricted(w http.ResponseWriter, aid, asset, flow string) bool {
	why, err := s.restrictedAsset(asset)
	if err != nil {
		writeErr(w, 502, "%v", err)
		return true
	}
	if why == "" {
		return false
	}
	s.st.Audit(aid, flow+".refused", map[string]any{"asset": asset, "reason": why})
	writeJSON(w, 409, map[string]any{"error": "This asset cannot be paid this way: " + why + ". " + restrictedNote,
		"restricted": true})
	return true
}

// --- naming ---------------------------------------------------------------------

// holderKey is the key a register holder (an OpenAMP account id) is named by:
// the enclave key the SeqPal ID that holds that account proved, else the
// account's one key at the policy server. "" and the reason when there is
// none.
func (s *server) holderKey(enclaveAID string) (string, string) {
	if k, err := s.st.EnclaveKeyOfAID(enclaveAID); err == nil && k != "" {
		return k, ""
	}
	var u struct {
		Pubkeys []string `json:"pubkeys"`
	}
	code, err := s.callOpenAMPStatus("GET", "/v1/users/"+enclaveAID, "", nil, &u)
	if err != nil || code != 200 {
		return "", "has no SeqPal ID and the policy server does not answer for its account, so it has no wallet key to be paid under"
	}
	if len(u.Pubkeys) != 1 || !xonlyRe.MatchString(strings.ToLower(u.Pubkeys[0])) {
		return "", "is an account of several keys, not one wallet's, so it cannot be paid this way"
	}
	return strings.ToLower(u.Pubkeys[0]), ""
}

var xonlyRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// accountKey is the key a SeqPal ID (by its account id) is named by: the
// enclave key of its OpenAMP account.
func (s *server) accountKey(aid string) (string, string) {
	wls, err := s.st.AccountWallets(aid)
	if err != nil {
		return "", "could not be read"
	}
	var keys []string
	for _, wl := range wls {
		if wl.Kind == "enclave" && wl.XOnly != "" {
			keys = append(keys, strings.ToLower(wl.XOnly))
		}
	}
	if len(keys) != 1 {
		return "", "has no OpenAMP account, so there is no wallet key to pay it under"
	}
	return keys[0], ""
}

// --- planning -------------------------------------------------------------------

// planRows settles who is in the payment: in order, each wanted (holder,
// subscription, key or reason, amount), left out when it has no key, when its
// key is one already named, when its amount is below the smallest allocation.
func (s *server) planRows(p *Payout, wanted []*PayoutRow, minAtoms uint64) []*PayoutRow {
	seen := map[string]string{}
	for _, r := range wanted {
		switch {
		case r.State == "left_out":
		case r.Atoms == 0:
			r.State, r.Reason = "left_out", "nothing to pay"
		case r.Key == "":
			r.State = "left_out"
		case seen[r.Key] != "":
			r.State, r.Reason = "left_out", "is named by the same wallet key as "+shortAID(seen[r.Key])+", which is paid once"
			r.Key = ""
		case r.Atoms < minAtoms:
			r.State, r.Reason = "left_out", fmt.Sprintf("is owed %d atoms, below the smallest payment the service makes (%d)", r.Atoms, minAtoms)
		default:
			r.State = "planned"
			seen[r.Key] = r.HolderAID
			p.TotalAtoms += r.Atoms
			p.Holders++
		}
		if r.State == "left_out" {
			p.LeftOut++
		}
	}
	return wanted
}

// handleDistributionPayout is POST /api/issuances/{id}/distributions/{runID}/payout
// (owner): the snapshotted run's holders as one payment to their wallets. It
// settles the list once; a second call returns it.
func (s *server) handleDistributionPayout(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss, d := s.ownedDistribution(w, acct, r.PathValue("id"), r.PathValue("runID"))
	if d == nil {
		return
	}
	defer s.st.LockIssuance(iss.ID)()
	if p, err := s.st.PayoutBySource("distribution", d.ID); err != nil {
		writeErr(w, 500, "store error")
		return
	} else if p != nil {
		s.writePayout(w, p)
		return
	}
	d, _ = s.st.DistributionByID(d.ID)
	if d.State != "snapshotted" {
		writeErr(w, 409, "take the record-date snapshot first (the run is %q)", d.State)
		return
	}
	asset := d.AssetPayCcy(s)
	if s.refuseRestricted(w, acct.AID, asset, "payout") {
		return
	}
	served, minAtoms, fees, err := s.assetTerms(asset)
	if err != nil {
		writeErr(w, 503, "%v", err)
		return
	}
	if !served {
		writeErr(w, 409, "the payment service does not pay in this asset; pay the holders in one on-chain transaction instead")
		return
	}
	pays, err := s.st.DistPaymentsByRun(d.ID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	p := &Payout{ID: mustID(), IssuanceID: iss.ID, Kind: "distribution", SourceID: d.ID, Asset: asset, State: "planned",
		Memo: d.Memo}
	var wanted []*PayoutRow
	for _, dp := range pays {
		row := &PayoutRow{HolderAID: dp.HolderAID, Atoms: dp.NetAtoms, ClaimIndex: -1}
		if dp.NetAtoms > 0 {
			row.Key, row.Reason = s.holderKey(dp.HolderAID)
			if row.Key == "" {
				row.State = "left_out"
			}
		}
		wanted = append(wanted, row)
	}
	rows := s.planRows(p, wanted, minAtoms)
	if p.Holders == 0 {
		writeErr(w, 409, "no holder of this run can be paid this way")
		return
	}
	p.FeeBound = feeBound(fees, p.TotalAtoms, p.Holders)
	if err := s.st.InsertPayout(p, rows); err != nil {
		writeErr(w, 500, "could not record the payout: %v", err)
		return
	}
	for _, row := range rows {
		state, reason := "planned", ""
		if row.State == "left_out" {
			state, reason = "skipped", "not paid by the payment to wallets: "+row.Reason
		}
		if row.Atoms == 0 {
			state, reason = "no_payment", "net zero after withholding"
		}
		_ = s.st.UpdateDistPaymentFields(d.ID, row.HolderAID, map[string]any{"state": state, "reason": reason})
	}
	_ = s.st.UpdateDistributionFields(d.ID, map[string]any{"state": "paying"})
	s.st.Audit(acct.AID, "payout.plan", map[string]any{"issuance_id": iss.ID, "payout": p.ID, "kind": "distribution",
		"run": d.ID, "asset": asset, "holders": p.Holders, "left_out": p.LeftOut, "total_atoms": p.TotalAtoms, "fee_bound": p.FeeBound})
	s.writePayout(w, p)
}

type createRefundReq struct {
	SubscriptionIDs []string `json:"subscription_ids"`
	Memo            string   `json:"memo"`
}

// handleCreateRefund is POST /api/issuances/{id}/refunds (owner): a refund,
// from the issuer's own funds, to subscribers whose money the issuer already
// received (the escrow released it). Each is paid what it deposited, in the
// asset it paid, named by its OpenAMP account's key. It moves no tokens:
// taking those back is a clawback, a decision of its own.
//
// A subscription still in the escrow covenant is not refunded here: the
// covenant's own cancel and refund pay it back, in one transaction, to the
// script each subscriber gave.
func (s *server) handleCreateRefund(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	var req createRefundReq
	if err := readJSON(r, &req); err != nil && err != io.EOF {
		writeErr(w, 400, "bad request body")
		return
	}
	defer s.st.LockIssuance(iss.ID)()
	subs, err := s.st.SubscriptionsByIssuance(iss.ID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	want := map[string]bool{}
	for _, id := range req.SubscriptionIDs {
		want[id] = true
	}
	asset := strings.ToLower(s.cfg.usdxAsset)
	var wanted []*PayoutRow
	for _, sub := range subs {
		if len(want) > 0 && !want[sub.ID] {
			continue
		}
		if sub.State != "settled" {
			if len(want) > 0 {
				writeErr(w, 409, "subscription %s is %s: only a subscription whose money was released to you is refunded here", sub.ID, sub.State)
				return
			}
			continue
		}
		row := &PayoutRow{HolderAID: sub.InvestorAID, SubscriptionID: sub.ID, Atoms: sub.DepositedAtoms, ClaimIndex: -1}
		if sub.Rail != "usdx" {
			row.State, row.Reason = "left_out", "paid on the "+sub.Rail+" rail; refunded on that rail"
		} else {
			row.Key, row.Reason = s.accountKey(sub.InvestorAID)
			if row.Key == "" {
				row.State = "left_out"
			}
		}
		wanted = append(wanted, row)
	}
	if len(wanted) == 0 {
		writeErr(w, 409, "no subscription of this offering has had its money released to you, so there is nothing to refund here")
		return
	}
	if s.refuseRestricted(w, acct.AID, asset, "refund") {
		return
	}
	served, minAtoms, fees, err := s.assetTerms(asset)
	if err != nil {
		writeErr(w, 503, "%v", err)
		return
	}
	if !served {
		writeErr(w, 409, "the payment service does not pay in USDX on this deployment")
		return
	}
	p := &Payout{ID: mustID(), IssuanceID: iss.ID, Kind: "refund", Asset: asset, State: "planned",
		Memo: strings.TrimSpace(req.Memo)}
	rows := s.planRows(p, wanted, minAtoms)
	if p.Holders == 0 {
		writeErr(w, 409, "no subscriber here can be refunded this way")
		return
	}
	p.FeeBound = feeBound(fees, p.TotalAtoms, p.Holders)
	if err := s.st.InsertPayout(p, rows); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeErr(w, 409, "a subscriber named here is already in a refund that is planned or paid")
			return
		}
		writeErr(w, 500, "could not record the refund: %v", err)
		return
	}
	s.st.Audit(acct.AID, "payout.plan", map[string]any{"issuance_id": iss.ID, "payout": p.ID, "kind": "refund",
		"asset": asset, "holders": p.Holders, "left_out": p.LeftOut, "total_atoms": p.TotalAtoms, "fee_bound": p.FeeBound})
	s.writePayout(w, p)
}

// --- reading ----------------------------------------------------------------------

func (s *server) payoutView(p *Payout) map[string]any {
	rows, _ := s.st.PayoutRows(p.ID)
	ccy := s.assetLabel(p.Asset)
	return map[string]any{"payout": p, "rows": rows, "asset_label": ccy,
		"summary": payoutSummary(p, ccy)}
}

func (s *server) writePayout(w http.ResponseWriter, p *Payout) {
	writeJSON(w, 200, s.payoutView(p))
}

// assetLabel is how an asset is named to a person: USDX, a token issued
// here by its ticker, any other by its id's first characters.
func (s *server) assetLabel(asset string) string {
	if strings.EqualFold(asset, s.cfg.usdxAsset) {
		return "USDX"
	}
	if iss, _ := s.st.IssuanceByAsset(asset); iss != nil && iss.Ticker != "" {
		return iss.Ticker
	}
	if len(asset) > 8 {
		return asset[:8] + "…"
	}
	return asset
}

// payoutSummary says where a payout stands in plain words.
func payoutSummary(p *Payout, ccy string) string {
	who := "holders"
	if p.Kind == "refund" {
		who = "subscribers"
	}
	amt := func(a uint64) string {
		s := strings.TrimRight(strings.TrimRight(string(amount8(a)), "0"), ".")
		return s + " " + ccy
	}
	verb := "Pay"
	if p.Kind == "refund" {
		verb = "Refund"
	}
	switch p.State {
	case "planned":
		return fmt.Sprintf("%s %d %s a total of %s from your wallet. The fee is at most %s.", verb, p.Holders, who, amt(p.TotalAtoms), amt(p.FeeBound))
	case "paying":
		return fmt.Sprintf("Your payment of %s to %d %s is on its way.", amt(p.TotalAtoms), p.Holders, who)
	case "paid":
		return fmt.Sprintf("Paid %s to %d %s' wallets; %s collected so far. Their wallets collect it when they next open, before %s.",
			amt(p.TotalAtoms), p.Holders, who, amt(p.CollectedAtoms), deadlineWords(p.Deadline))
	case "complete":
		s := fmt.Sprintf("Paid %s to %d %s; %s collected.", amt(p.TotalAtoms), p.Holders, who, amt(p.CollectedAtoms))
		if p.ReturnedAtoms > 0 {
			s += fmt.Sprintf(" %s was not collected by the deadline and came back to you.", amt(p.ReturnedAtoms))
		}
		return s
	}
	return p.State
}

func deadlineWords(t int64) string {
	if t == 0 {
		return "the deadline"
	}
	return time.Unix(t, 0).UTC().Format("2 January 2006, 15:04 UTC")
}

// handleListPayouts is GET /api/issuances/{id}/payouts (owner).
func (s *server) handleListPayouts(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	ps, err := s.st.PayoutsByIssuance(iss.ID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	out := []map[string]any{}
	for _, p := range ps {
		out = append(out, s.payoutView(p))
	}
	writeJSON(w, 200, map[string]any{"issuance_id": iss.ID, "payouts": out, "available": s.cfg.payoutURL != ""})
}

func (s *server) ownedPayout(w http.ResponseWriter, r *http.Request) (*Issuance, *Payout) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return nil, nil
	}
	p, err := s.st.PayoutByID(r.PathValue("pid"))
	if err != nil {
		writeErr(w, 500, "store error")
		return nil, nil
	}
	if p == nil || p.IssuanceID != iss.ID {
		writeErr(w, 404, "unknown payout for this issuance")
		return nil, nil
	}
	return iss, p
}

// handlePayoutList is GET /api/issuances/{id}/payouts/{pid}/list (owner): the
// payment list the issuer's wallet pays, one line per wallet, KEY AMOUNT,
// the amount in atoms of the payout's asset.
func (s *server) handlePayoutList(w http.ResponseWriter, r *http.Request) {
	_, p := s.ownedPayout(w, r)
	if p == nil {
		return
	}
	rows, err := s.st.PayoutRows(p.ID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	w.Header().Set("content-type", "text/plain; charset=utf-8")
	w.Header().Set("content-disposition", fmt.Sprintf(`attachment; filename="seqpal-%s-%s.txt"`, p.Kind, p.ID[:8]))
	fmt.Fprintf(w, "# SeqPal %s %s: %d wallets, %d atoms of asset %s in all\n", p.Kind, p.ID, p.Holders, p.TotalAtoms, p.Asset)
	for _, row := range rows {
		if row.State == "left_out" || row.Key == "" {
			continue
		}
		fmt.Fprintf(w, "%s %d\n", row.Key, row.Atoms)
	}
}

// --- the reference ----------------------------------------------------------------

type distributionStatus struct {
	DistributionID string `json:"distribution_id"`
	State          string `json:"state"`
	Asset          string `json:"asset"`
	ReturnScript   string `json:"return_script"`
	Round          string `json:"round"`
	Expiry         int64  `json:"expiry"`
	Deadline       int64  `json:"deadline"`
	Claims         []struct {
		Index      int    `json:"index"`
		Key        string `json:"key"`
		Value      string `json:"value"`
		LeafID     string `json:"leaf_id"`
		State      string `json:"state"`
		ReturnedIn string `json:"returned_in"`
	} `json:"claims"`
	Returns []struct {
		Txid  string `json:"txid"`
		Value string `json:"value"`
		State string `json:"state"`
	} `json:"returns"`
}

func (s *server) paymentStatus(ref string) (*distributionStatus, error) {
	var st distributionStatus
	if err := s.payoutCall("/v1/distribution_status", map[string]any{"distribution_id": ref}, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// matchPayment checks a payment's status against the planned rows: the asset,
// every planned key exactly once with exactly its amount, and no other key.
// It answers each row's claim index.
func matchPayment(p *Payout, rows []*PayoutRow, st *distributionStatus) (map[string]int, error) {
	if !strings.EqualFold(st.Asset, p.Asset) {
		return nil, fmt.Errorf("the payment is in asset %s, and this payout pays in %s", st.Asset, p.Asset)
	}
	byKey := map[string]*PayoutRow{}
	for _, r := range rows {
		if r.State != "left_out" && r.Key != "" {
			byKey[r.Key] = r
		}
	}
	index := map[string]int{}
	for _, c := range st.Claims {
		k := strings.ToLower(c.Key)
		r := byKey[k]
		if _, twice := index[k]; twice {
			return nil, fmt.Errorf("the payment names wallet key %s twice", k)
		}
		if r == nil {
			return nil, fmt.Errorf("the payment pays wallet key %s, which is not one of this payout's", k)
		}
		v, err := strconv.ParseUint(c.Value, 10, 64)
		if err != nil || v != r.Atoms {
			return nil, fmt.Errorf("the payment pays %s %s atoms, and the list says %d", shortAID(r.HolderAID), c.Value, r.Atoms)
		}
		index[k] = c.Index
	}
	for k, r := range byKey {
		if _, ok := index[k]; !ok {
			return nil, fmt.Errorf("the payment leaves out %s (wallet key %s)", shortAID(r.HolderAID), k)
		}
	}
	return index, nil
}

type referenceReq struct {
	Reference string `json:"reference"`
}

var refRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// handlePayoutReference is POST /api/issuances/{id}/payouts/{pid}/reference
// (owner): the reference of the payment the issuer's wallet made. It is
// recorded only when the payment names exactly the planned list.
func (s *server) handlePayoutReference(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss, p := s.ownedPayout(w, r)
	if p == nil {
		return
	}
	var req referenceReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	ref := strings.ToLower(strings.TrimSpace(req.Reference))
	if !refRe.MatchString(ref) {
		writeErr(w, 400, "a payment reference is 64 hexadecimal characters, as your wallet shows it")
		return
	}
	defer s.st.LockIssuance(iss.ID)()
	p, _ = s.st.PayoutByID(p.ID)
	if p.State != "planned" {
		if p.Reference == ref {
			s.writePayout(w, p)
			return
		}
		writeErr(w, 409, "this payout already has a payment (%s)", p.Reference)
		return
	}
	st, err := s.paymentStatus(ref)
	if err != nil {
		if pr, ok := err.(*payoutRefusal); ok && pr.status == 404 {
			writeErr(w, 404, "the payment service knows no payment %s", ref)
			return
		}
		writeErr(w, 502, "%v", err)
		return
	}
	switch st.State {
	case "pending", "issued", "released":
	default:
		writeErr(w, 409, "that payment is %s: it pays nobody", st.State)
		return
	}
	rows, err := s.st.PayoutRows(p.ID)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	index, err := matchPayment(p, rows, st)
	if err != nil {
		s.st.Audit(acct.AID, "payout.reference.refused", map[string]any{"payout": p.ID, "reference": ref, "reason": err.Error()})
		writeErr(w, 409, "%v; nothing was recorded", err)
		return
	}
	if err := s.st.UpdatePayoutFields(p.ID, map[string]any{"state": "paying", "reference": ref}); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeErr(w, 409, "that payment is already recorded against another payout")
			return
		}
		writeErr(w, 500, "store error")
		return
	}
	for _, row := range rows {
		if i, ok := index[row.Key]; ok && row.State == "planned" {
			_ = s.st.UpdatePayoutRowFields(p.ID, row.HolderAID, map[string]any{"state": "pending", "claim_index": i})
		}
	}
	s.st.Audit(acct.AID, "payout.reference", map[string]any{"issuance_id": iss.ID, "payout": p.ID, "reference": ref,
		"holders": p.Holders, "total_atoms": p.TotalAtoms})
	s.followPayoutLocked(p.ID)
	p, _ = s.st.PayoutByID(p.ID)
	s.writePayout(w, p)
}

// --- following ----------------------------------------------------------------------

// watchPayouts follows every submitted payout to its end.
func (s *server) watchPayouts() {
	if s.cfg.payoutURL == "" {
		return
	}
	ps, err := s.st.PayoutsFollowed()
	if err != nil {
		return
	}
	for _, p := range ps {
		s.followPayout(p.ID)
	}
}

// followPayout reads one payout's status and records what changed: the
// payment going out (each allocation its wallet's), each one collected, each
// one returned with the transaction that returned it.
func (s *server) followPayout(id string) {
	p, err := s.st.PayoutByID(id)
	if err != nil || p == nil || (p.State != "paying" && p.State != "paid") {
		return
	}
	defer s.st.LockIssuance(p.IssuanceID)()
	s.followPayoutLocked(id)
}

// followPayoutLocked is followPayout with the issuance's lock already held.
func (s *server) followPayoutLocked(id string) {
	p, err := s.st.PayoutByID(id)
	if err != nil || p == nil || (p.State != "paying" && p.State != "paid") {
		return
	}
	st, err := s.paymentStatus(p.Reference)
	if err != nil {
		return
	}
	iss, _ := s.st.IssuanceByID(p.IssuanceID)
	ccy := s.assetLabel(p.Asset)
	rows, _ := s.st.PayoutRows(p.ID)
	byIndex := map[int]*PayoutRow{}
	for _, row := range rows {
		if row.ClaimIndex >= 0 {
			byIndex[row.ClaimIndex] = row
		}
	}
	switch st.State {
	case "void", "expired":
		// The payment never went out: the payer kept its coins. The list stands
		// and can be paid again.
		_ = s.st.UpdatePayoutFields(p.ID, map[string]any{"state": "planned", "reference": ""})
		for _, row := range rows {
			if row.State == "pending" {
				_ = s.st.UpdatePayoutRowFields(p.ID, row.HolderAID, map[string]any{"state": "planned", "claim_index": -1})
			}
		}
		s.st.Audit("", "payout.void", map[string]any{"payout": p.ID, "reference": p.Reference, "state": st.State})
		return
	case "released":
	default:
		return
	}
	if p.State == "paying" {
		_ = s.st.UpdatePayoutFields(p.ID, map[string]any{"state": "paid", "round_txid": st.Round, "expiry": st.Expiry,
			"deadline": st.Deadline})
		p.State, p.RoundTxid, p.Deadline = "paid", st.Round, st.Deadline
		s.payoutWentOut(iss, p, rows, ccy)
	}
	var collected, returned uint64
	returnTxids := map[string]bool{}
	open := 0
	for _, c := range st.Claims {
		row := byIndex[c.Index]
		if row == nil {
			continue
		}
		switch c.State {
		case "claimed":
			collected += row.Atoms
			if row.State != "collected" {
				_ = s.st.UpdatePayoutRowFields(p.ID, row.HolderAID, map[string]any{"state": "collected", "leaf_id": c.LeafID})
				s.st.Audit(row.HolderAID, "payout.collected", map[string]any{"payout": p.ID, "atoms": row.Atoms})
			}
		case "returned":
			returned += row.Atoms
			returnTxids[c.ReturnedIn] = true
			if row.State != "returned" {
				_ = s.st.UpdatePayoutRowFields(p.ID, row.HolderAID, map[string]any{"state": "returned", "returned_in": c.ReturnedIn,
					"leaf_id": c.LeafID})
				s.recordReturn(iss, p, row, c.ReturnedIn, ccy)
			}
		default:
			open++
			if row.LeafID == "" && c.LeafID != "" {
				_ = s.st.UpdatePayoutRowFields(p.ID, row.HolderAID, map[string]any{"leaf_id": c.LeafID})
			}
		}
	}
	var txids []string
	for t := range returnTxids {
		if t != "" {
			txids = append(txids, t)
		}
	}
	sort.Strings(txids)
	fields := map[string]any{"collected_atoms": collected, "returned_atoms": returned, "return_txids": strings.Join(txids, ",")}
	if open == 0 && len(st.Claims) > 0 {
		fields["state"] = "complete"
		s.st.Audit("", "payout.complete", map[string]any{"payout": p.ID, "collected_atoms": collected, "returned_atoms": returned,
			"return_txids": txids})
	}
	_ = s.st.UpdatePayoutFields(p.ID, fields)
}

// payoutWentOut records the payment going out: every listed wallet's
// allocation is now its own, paid by the one round transaction.
func (s *server) payoutWentOut(iss *Issuance, p *Payout, rows []*PayoutRow, ccy string) {
	kind := "distribution"
	if p.Kind == "refund" {
		kind = "refund"
	}
	for _, row := range rows {
		if row.State != "pending" {
			continue
		}
		_ = s.st.UpdatePayoutRowFields(p.ID, row.HolderAID, map[string]any{"state": "paid"})
		_ = s.st.InsertLedger(&LedgerEntry{IssuanceID: p.IssuanceID, SubscriptionID: row.SubscriptionID, Kind: kind, Rail: "wallet",
			Amount: row.Atoms, Ccy: ccy, Txid: p.RoundTxid})
		what := "A distribution"
		if p.Kind == "refund" {
			what = "A refund"
		}
		name := ""
		if iss != nil {
			name = " from " + iss.Name
		}
		notify := row.HolderAID
		if p.Kind == "distribution" {
			if acct, _ := s.st.AccountByEnclaveKey(row.Key); acct != nil {
				notify = acct.AID
			}
		}
		_ = s.st.InsertNotice(notify, kind, fmt.Sprintf("%s of %s %s%s was paid to your wallet. Your wallet collects it when it next "+
			"opens, before %s; after that it goes back to the payer.", what, amount8(row.Atoms), ccy, name, deadlineWords(p.Deadline)))
		if p.Kind == "distribution" {
			_ = s.st.UpdateDistPaymentFields(p.SourceID, row.HolderAID, map[string]any{"state": "paid", "txid": p.RoundTxid,
				"reason": "paid to the holder's wallet"})
		}
		s.st.Audit(row.HolderAID, "payout.paid", map[string]any{"payout": p.ID, "atoms": row.Atoms, "round": p.RoundTxid})
	}
	if p.Kind == "distribution" && iss != nil {
		if d, _ := s.st.DistributionByID(p.SourceID); d != nil {
			_ = s.st.UpdateDistributionFields(d.ID, map[string]any{"state": "complete", "payout_txid": p.RoundTxid})
			pays, _ := s.st.DistPaymentsByRun(d.ID)
			s.finalizeDistributionArtifacts(iss, d, pays)
		}
	}
	if p.Kind == "refund" {
		for _, row := range rows {
			if row.State == "pending" && row.SubscriptionID != "" {
				_ = s.st.UpdateSettlementFields(row.SubscriptionID, map[string]any{"refund_txid": p.RoundTxid})
			}
		}
	}
	s.st.Audit("", "payout.paid", map[string]any{"payout": p.ID, "round": p.RoundTxid, "deadline": p.Deadline})
}

// recordReturn records one allocation that came back to the issuer.
func (s *server) recordReturn(iss *Issuance, p *Payout, row *PayoutRow, txid, ccy string) {
	kind := "distribution_return"
	if p.Kind == "refund" {
		kind = "refund_return"
	}
	_ = s.st.InsertLedger(&LedgerEntry{IssuanceID: p.IssuanceID, SubscriptionID: row.SubscriptionID, Kind: kind, Rail: "wallet",
		Amount: row.Atoms, Ccy: ccy, Txid: txid})
	if p.Kind == "distribution" {
		_ = s.st.UpdateDistPaymentFields(p.SourceID, row.HolderAID, map[string]any{"state": "returned",
			"reason": "not collected by the deadline; paid back to the issuer in " + txid})
	}
	s.st.Audit(row.HolderAID, "payout.returned", map[string]any{"payout": p.ID, "atoms": row.Atoms, "txid": txid})
}
