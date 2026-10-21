package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// A payment service stand-in: what an operator publishes (/v1/info) and its
// public status of a payment to many wallets (/v1/distribution_status), set
// by the test. The regtest proof (regtest_payouts_test.go) runs the real one.
type fakePayService struct {
	srv    *httptest.Server
	mu     sync.Mutex
	asset  string
	status map[string]map[string]any
}

func newFakePayService(t *testing.T, asset string) *fakePayService {
	f := &fakePayService{asset: asset, status: map[string]map[string]any{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/info", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{
			"assets": []any{map[string]any{"asset": f.asset, "min_leaf": "330",
				"fees": map[string]any{"refresh_ppm": 2000, "refresh_base": "10", "offboard_base": "100"}}},
			"fees": map[string]any{"refresh_ppm": 1000, "refresh_base": "5", "offboard_base": "50", "free_window_seconds": 0},
		})
	})
	mux.HandleFunc("POST /v1/distribution_status", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID string `json:"distribution_id"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		st, ok := f.status[req.ID]
		f.mu.Unlock()
		if !ok {
			writeJSON(w, 404, map[string]any{"error": map[string]any{"code": "unknown_participation", "message": "no distribution " + req.ID + " is known"}})
			return
		}
		writeJSON(w, 200, st)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

type fakeClaim struct {
	key        string
	value      uint64
	state      string
	returnedIn string
}

func (f *fakePayService) set(id, state, asset string, claims []fakeClaim) {
	var cs []any
	for i, c := range claims {
		cs = append(cs, map[string]any{"index": i, "key": c.key, "value": jsonU(c.value), "state": c.state,
			"leaf_id": strings.Repeat("c", 63) + string(rune('0'+i%10)), "returned_in": nilIfEmpty(c.returnedIn)})
	}
	f.mu.Lock()
	f.status[id] = map[string]any{"distribution_id": id, "state": state, "asset": asset, "return_script": "0014ab",
		"round": strings.Repeat("a", 64), "expiry": 1900000000, "deadline": 1899740800, "claims": cs, "returns": []any{}}
	f.mu.Unlock()
}

func jsonU(v uint64) string { b, _ := json.Marshal(v); return string(b) }
func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func ref(c byte) string { return strings.Repeat(string(c), 64) }

// TestPayoutDistributionToWallets: a distribution run's holders paid in one
// payment to their wallets. Each holder is named by its OpenAMP account's key;
// a holder with no single key, and one owed less than the smallest payment,
// are left out with the reason. A restricted asset is refused as the paid
// asset. The reference is recorded only when the payment names exactly the
// list; then the payout follows it: paid, collected, returned.
func TestPayoutDistributionToWallets(t *testing.T) {
	h := newM7Harness(t, m5opts{})
	svc := newFakePayService(t, m5USDX)
	h.s.cfg.payoutURL = svc.srv.URL
	issuer, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID, assetID, _ := h.deployLivePrivate(issuer, "PAYW", "HN", 1.0)

	// The security itself as the paid asset: refused, it is restricted.
	r := h.do("POST", "/api/issuances/"+issID+"/distributions", issuer, map[string]any{"pool_atoms": 5000, "asset": assetID})
	if r.code != 409 || r.body["restricted"] != true || !strings.Contains(r.errMsg(), "transfer rules travel with it") {
		t.Fatalf("a restricted asset as the paid asset: %d %s", r.code, r.raw)
	}
	t.Logf("restricted asset refused: %d %s", r.code, r.errMsg())

	keyOf := map[string]string{}
	var aids []string
	for i, res := range []string{"US", "GB", "FR"} {
		priv := genPriv(t)
		_, aid, x := h.register(priv, "Holder "+string(rune('A'+i)), res)
		if _, err := h.s.registerUser(x); err != nil {
			t.Fatal(err)
		}
		keyOf[aid] = x
		aids = append(aids, aid)
		h.oa.setBalance(aid, assetID, 100)
	}
	h.s.st.UpsertClaims(&Claims{AID: aids[0], Residence: "US", BaseEligibility: "ret", USPerson: true, Status: "verified"})
	h.s.st.UpsertClaims(&Claims{AID: aids[1], Residence: "GB", BaseEligibility: "ret", Status: "verified"})
	// No SeqPal ID and unknown to the policy server; an account of two keys; and
	// a holder owed less than the smallest payment.
	unknown := strings.Repeat("d", 40)
	h.oa.setBalance(unknown, assetID, 100)
	twoKeys := strings.Repeat("e", 40)
	h.oa.mu.Lock()
	h.oa.users[twoKeys] = &m5User{AID: twoKeys, Pubkeys: []string{strings.Repeat("1", 64), strings.Repeat("2", 64)}}
	h.oa.mu.Unlock()
	h.oa.setBalance(twoKeys, assetID, 100)
	_, small, sx := h.register(genPriv(t), "Holder small", "US")
	h.s.registerUser(sx)
	h.s.st.UpsertClaims(&Claims{AID: small, Residence: "US", BaseEligibility: "ret", USPerson: true, Status: "verified"})
	h.oa.setBalance(small, assetID, 1)

	cr := h.do("POST", "/api/issuances/"+issID+"/distributions", issuer, map[string]any{"pool_atoms": 50100, "memo": "Q3"})
	if cr.code != 200 {
		t.Fatalf("create: %d %s", cr.code, cr.raw)
	}
	runID := cr.body["distribution"].(map[string]any)["id"].(string)
	if s := h.do("POST", "/api/issuances/"+issID+"/distributions/"+runID+"/snapshot", issuer, nil); s.code != 200 {
		t.Fatalf("snapshot: %d %s", s.code, s.raw)
	}
	pl := h.do("POST", "/api/issuances/"+issID+"/distributions/"+runID+"/payout", issuer, nil)
	if pl.code != 200 {
		t.Fatalf("payout plan: %d %s", pl.code, pl.raw)
	}
	pay := pl.body["payout"].(map[string]any)
	pid := pay["id"].(string)
	t.Logf("plan: %s", pl.body["summary"])
	if jsonAtoms(pay["holders"]) != 3 || jsonAtoms(pay["left_out"]) != 3 {
		t.Fatalf("holders %v left out %v, want 3 and 3: %s", pay["holders"], pay["left_out"], pl.raw)
	}
	rows, _ := h.s.st.PayoutRows(pid)
	net := map[string]uint64{}
	for _, row := range rows {
		switch row.HolderAID {
		case unknown, twoKeys, small:
			if row.State != "left_out" || row.Reason == "" {
				t.Fatalf("holder %s: %+v, want left out with a reason", row.HolderAID, row)
			}
			t.Logf("left out %s: %s", shortAID(row.HolderAID), row.Reason)
		default:
			if row.State != "planned" || row.Key != keyOf[row.HolderAID] {
				t.Fatalf("holder %s: %+v, want planned under its enclave key", row.HolderAID, row)
			}
			net[row.Key] = row.Atoms
		}
	}
	if fb := jsonAtoms(pay["fee_bound"]); fb == 0 {
		t.Fatalf("no fee bound: %s", pl.raw)
	}
	if again := h.do("POST", "/api/issuances/"+issID+"/distributions/"+runID+"/payout", issuer, nil); again.body["payout"].(map[string]any)["id"] != pid {
		t.Fatalf("a second plan made another payout: %s", again.raw)
	}
	if ex := h.do("POST", "/api/issuances/"+issID+"/distributions/"+runID+"/execute", issuer, map[string]any{}); ex.code != 409 {
		t.Fatalf("the run paid on chain as well: %d %s", ex.code, ex.raw)
	}

	// The list the issuer's wallet pays.
	lst := h.do("GET", "/api/issuances/"+issID+"/payouts/"+pid+"/list", issuer, nil)
	lines := 0
	for _, l := range strings.Split(lst.raw, "\n") {
		if l != "" && !strings.HasPrefix(l, "#") {
			lines++
		}
	}
	if lst.code != 200 || lines != 3 {
		t.Fatalf("list: %d %q", lst.code, lst.raw)
	}

	var good []fakeClaim
	var keys []string
	for k, v := range net {
		good = append(good, fakeClaim{key: k, value: v, state: "pending"})
		keys = append(keys, k)
	}
	refuse := func(name, id, asset string, claims []fakeClaim, want string) {
		svc.set(id, "pending", asset, claims)
		r := h.do("POST", "/api/issuances/"+issID+"/payouts/"+pid+"/reference", issuer, map[string]any{"reference": id})
		if r.code != 409 || !strings.Contains(r.errMsg(), want) {
			t.Fatalf("%s: %d %s, want 409 %q", name, r.code, r.raw, want)
		}
		t.Logf("refused, %s: %s", name, r.errMsg())
	}
	refuse("a holder named twice", ref('1'), m5USDX, append(append([]fakeClaim{}, good...), good[0]), "twice")
	short := append([]fakeClaim{}, good...)
	short[1].value--
	refuse("one atom short", ref('2'), m5USDX, short, "the list says")
	refuse("a stranger paid", ref('3'), m5USDX, append(append([]fakeClaim{}, good...), fakeClaim{key: strings.Repeat("9", 64), value: 500}), "not one of this payout's")
	refuse("a holder left out", ref('4'), m5USDX, good[:2], "leaves out")
	refuse("another asset", ref('5'), strings.Repeat("7", 64), good, "this payout pays in")
	if r := h.do("POST", "/api/issuances/"+issID+"/payouts/"+pid+"/reference", issuer, map[string]any{"reference": ref('6')}); r.code != 404 {
		t.Fatalf("an unknown reference: %d %s", r.code, r.raw)
	}

	id := ref('f')
	svc.set(id, "pending", m5USDX, good)
	ok := h.do("POST", "/api/issuances/"+issID+"/payouts/"+pid+"/reference", issuer, map[string]any{"reference": id})
	if ok.code != 200 || ok.body["payout"].(map[string]any)["state"] != "paying" {
		t.Fatalf("the right payment: %d %s", ok.code, ok.raw)
	}

	// It goes out: every holder's allocation is its wallet's.
	for i := range good {
		good[i].state = "open"
	}
	svc.set(id, "released", m5USDX, good)
	h.s.watchPayouts()
	p, _ := h.s.st.PayoutByID(pid)
	if p.State != "paid" || p.RoundTxid != ref('a') || p.Deadline == 0 {
		t.Fatalf("after release: %+v", p)
	}
	d, _ := h.s.st.DistributionByID(runID)
	if d.State != "complete" || d.PayoutTxid != ref('a') || d.WithholdingHash == "" {
		t.Fatalf("the run after release: %+v", d)
	}
	led, _ := h.s.st.LedgerByIssuance(issID)
	paid := 0
	for _, e := range led {
		if e.Kind == "distribution" && e.Rail == "wallet" && e.Txid == ref('a') {
			paid++
		}
	}
	if paid != 3 {
		t.Fatalf("%d ledger rows for the payment, want 3", paid)
	}
	ns, _ := h.s.st.NoticesByAID(aids[0])
	found := false
	for _, n := range ns {
		if strings.Contains(n.Body, "paid to your wallet") {
			found = true
			t.Logf("holder notice: %s", n.Body)
		}
	}
	if !found {
		t.Fatal("the holder was not told")
	}

	// Two collected, one returned at the deadline.
	var returnedKey string
	for i := range good {
		if i == 2 {
			good[i].state, good[i].returnedIn = "returned", ref('b')
			returnedKey = good[i].key
		} else {
			good[i].state = "claimed"
		}
	}
	svc.set(id, "released", m5USDX, good)
	h.s.followPayout(pid)
	p, _ = h.s.st.PayoutByID(pid)
	if p.State != "complete" || p.ReturnedAtoms != net[returnedKey] || p.ReturnTxids != ref('b') ||
		p.CollectedAtoms != p.TotalAtoms-net[returnedKey] {
		t.Fatalf("at the end: %+v", p)
	}
	t.Logf("end: %s", payoutSummary(p, "USDX"))
	pays, _ := h.s.st.DistPaymentsByRun(runID)
	for _, dp := range pays {
		if keyOf[dp.HolderAID] == returnedKey && dp.State != "returned" {
			t.Fatalf("the returned holder's payment row: %+v", dp)
		}
	}
	led, _ = h.s.st.LedgerByIssuance(issID)
	back := 0
	for _, e := range led {
		if e.Kind == "distribution_return" && e.Txid == ref('b') && e.Amount == net[returnedKey] {
			back++
		}
	}
	if back != 1 {
		t.Fatalf("%d return rows in the ledger, want 1", back)
	}
	if r := h.do("POST", "/api/issuances/"+issID+"/payouts/"+pid+"/reference", issuer, map[string]any{"reference": ref('3')}); r.code != 409 {
		t.Fatalf("a second payment for a paid payout: %d %s", r.code, r.raw)
	}
}

// TestPayoutRefundToWallets: a refund of released subscriptions, each
// subscriber named by its OpenAMP key and paid what it deposited; a
// subscription on another rail is left out; nobody is refunded twice.
func TestPayoutRefundToWallets(t *testing.T) {
	h := newM7Harness(t, m5opts{})
	svc := newFakePayService(t, m5USDX)
	h.s.cfg.payoutURL = svc.srv.URL
	issuer, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID, _, _ := h.deployLivePrivate(issuer, "REFW", "HN", 1.0)
	var keys []string
	for i, rail := range []string{"usdx", "usdx", "btc", "usdx"} {
		_, aid, x := h.register(genPriv(t), "Subscriber", "HN")
		state := "settled"
		if i == 3 {
			state = "in_escrow" // still in the covenant: refunded by it, not here
		}
		sub := &Subscription{ID: "sub" + string(rune('0'+i)), IssuanceID: issID, InvestorAID: aid, Rail: rail,
			PayCcy: "USDX", State: state, DepositedAtoms: uint64(1000 * (i + 1)), TokenAtoms: 100}
		if err := h.s.st.InsertSubscription(sub); err != nil {
			t.Fatal(err)
		}
		h.s.st.CreateSettlementIfAbsent(sub.ID, issID)
		keys = append(keys, x)
	}
	r := h.do("POST", "/api/issuances/"+issID+"/refunds", issuer, map[string]any{"memo": "offering unwound"})
	if r.code != 200 {
		t.Fatalf("refund: %d %s", r.code, r.raw)
	}
	pay := r.body["payout"].(map[string]any)
	pid := pay["id"].(string)
	if jsonAtoms(pay["holders"]) != 2 || jsonAtoms(pay["left_out"]) != 1 || jsonAtoms(pay["total_atoms"]) != 3000 {
		t.Fatalf("refund plan: %s", r.raw)
	}
	t.Logf("refund plan: %s", r.body["summary"])
	// The same subscribers again: refused while the first refund stands.
	if again := h.do("POST", "/api/issuances/"+issID+"/refunds", issuer, map[string]any{"subscription_ids": []string{"sub0"}}); again.code != 409 {
		t.Fatalf("a second refund of sub0: %d %s", again.code, again.raw)
	} else {
		t.Logf("refunded twice refused: %s", again.errMsg())
	}
	if one := h.do("POST", "/api/issuances/"+issID+"/refunds", issuer, map[string]any{"subscription_ids": []string{"sub3"}}); one.code != 409 {
		t.Fatalf("a refund of a subscription still in escrow: %d %s", one.code, one.raw)
	}
	id := ref('e')
	svc.set(id, "released", m5USDX, []fakeClaim{{keys[0], 1000, "open", ""}, {keys[1], 2000, "open", ""}})
	ok := h.do("POST", "/api/issuances/"+issID+"/payouts/"+pid+"/reference", issuer, map[string]any{"reference": id})
	if ok.code != 200 || ok.body["payout"].(map[string]any)["state"] != "paid" {
		t.Fatalf("refund reference: %d %s", ok.code, ok.raw)
	}
	st, _ := h.s.st.SettlementByID("sub0")
	if st.RefundTxid != ref('a') {
		t.Fatalf("settlement after the refund: %+v", st)
	}
	t.Logf("paid: %s", ok.body["summary"])
}
