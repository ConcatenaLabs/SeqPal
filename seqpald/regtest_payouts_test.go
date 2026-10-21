package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/sha512"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
)

// TestRegtestPayouts is the end-to-end proof of distributions and refunds paid
// to many wallets at once, run against a REAL payment service (an Arca
// operator, arca's `arca_operator_for_browsers` harness, on its anchored
// regtest chain), the REAL seqpald binary over its HTTP API, and the `arca`
// command line as the issuer's wallet and as every holder's wallet. The policy
// server is the openampd test stub. scripts/regtest-payouts.sh runs it;
// without SEQPAL_PAYOUT_CONTROL it is skipped.
//
// What it shows:
//   - a restricted asset (the security itself) refused as the paid asset;
//   - a distribution of USDX (the harness's asset X) to 5 holders, each named
//     by the OpenAMP key its own wallet derives at m/5/0, in one payment the
//     issuer's wallet makes from the list seqpald gives; a holder with no wallet
//     key left out with the reason;
//   - the payer's wallet refusing a holder named twice and a total it does not
//     hold, before anything is signed;
//   - seqpald recording the payment only once it names exactly the list, then
//     following it: paid, 3 collected by their wallets with no prompt but a
//     sync, 2 uncollected and returned to the issuer at the deadline, the
//     return transaction on the chain paying exactly their sum;
//   - a refund to 3 subscribers the same way, every one collected.
func TestRegtestPayouts(t *testing.T) {
	control := os.Getenv("SEQPAL_PAYOUT_CONTROL")
	if control == "" {
		t.Skip("set SEQPAL_PAYOUT_CONTROL (scripts/regtest-payouts.sh does) to run against an operator harness")
	}
	r := &payReg{t: t, control: strings.TrimRight(control, "/"), arca: os.Getenv("SEQPAL_PAYOUT_ARCA"),
		bin: os.Getenv("SEQPAL_REGTEST_SEQPALD"), work: os.Getenv("SEQPAL_REGTEST_WORKDIR")}
	if r.arca == "" || r.bin == "" || r.work == "" {
		t.Fatal("SEQPAL_PAYOUT_ARCA, SEQPAL_REGTEST_SEQPALD and SEQPAL_REGTEST_WORKDIR are required")
	}
	if p := os.Getenv("SEQPAL_REGTEST_EVIDENCE"); p != "" {
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		r.ev = f
	}
	r.run()
}

type payReg struct {
	t                 *testing.T
	control, arca     string
	bin, work         string
	ev                *os.File
	server, nodeURL   string
	nodeUser, nodePwd string
	x                 string
	base              string
	oa                *m5Stub
	reg               *regtest
}

func (r *payReg) log(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	r.t.Log(line)
	if r.ev != nil {
		fmt.Fprintln(r.ev, line)
	}
}

// ctl is one call to the operator harness's control address.
func (r *payReg) ctl(path string, body any) map[string]any {
	r.t.Helper()
	b, _ := json.Marshal(body)
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Post(r.control+path, "application/json", bytes.NewReader(b))
	if err != nil {
		r.t.Fatalf("control %s: %v", path, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != 200 {
		r.t.Fatalf("control %s: %d %v", path, resp.StatusCode, out)
	}
	return out
}

// wallet is an arca command-line wallet in its own directory.
type wallet struct {
	r    *payReg
	name string
	dir  string
}

func (r *payReg) newWallet(name string, mnemonic string) *wallet {
	w := &wallet{r: r, name: name, dir: filepath.Join(r.work, "wallets", strings.ReplaceAll(name, " ", "-"))}
	args := []string{"create", "--server", r.server, "--node-url", r.nodeURL, "--node-user", r.nodeUser}
	if mnemonic != "" {
		args = append(args, "--mnemonic", mnemonic)
	}
	w.ok(args...)
	return w
}

func (w *wallet) run(args ...string) (bool, map[string]any, any, string) {
	cmd := exec.Command(w.r.arca, append([]string{"--datadir", w.dir, "--witness-patience", "120"}, args...)...)
	cmd.Env = append(os.Environ(), "ARCA_NODE_PASSWORD="+w.r.nodePwd)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var v any
	json.Unmarshal(out.Bytes(), &v)
	m, _ := v.(map[string]any)
	return err == nil, m, v, strings.TrimSpace(errb.String() + " " + out.String())
}

func (w *wallet) ok(args ...string) map[string]any {
	w.r.t.Helper()
	ok, m, _, text := w.run(args...)
	if !ok {
		w.r.t.Fatalf("%s: arca %v: %s", w.name, args, text)
	}
	return m
}

func (w *wallet) list(args ...string) []any {
	w.r.t.Helper()
	ok, _, v, text := w.run(args...)
	if !ok {
		w.r.t.Fatalf("%s: arca %v: %s", w.name, args, text)
	}
	a, _ := v.([]any)
	return a
}

func (w *wallet) refused(want string, args ...string) string {
	w.r.t.Helper()
	ok, _, _, text := w.run(args...)
	if ok || !strings.Contains(text, want) {
		w.r.t.Fatalf("%s: arca %v: want a refusal containing %q, got ok=%v %s", w.name, args, want, ok, text)
	}
	return text
}

func (w *wallet) mnemonic() string {
	b, err := os.ReadFile(filepath.Join(w.dir, "mnemonic"))
	if err != nil {
		w.r.t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

// liveX is what a wallet holds of X in live coins.
func (w *wallet) liveX() uint64 {
	var total uint64
	for _, c := range w.list("coins") {
		m := c.(map[string]any)
		if m["state"] == "live" && m["asset"] == w.r.x {
			v, _ := strconv.ParseUint(fmt.Sprint(m["value"]), 10, 64)
			total += v
		}
	}
	return total
}

// openampPriv is the secret of a mnemonic's OpenAMP key, m/5/0 (BIP39 seed,
// BIP32 unhardened steps), as every wallet on the kit derives it: the key a
// SeqPal ID is. The wallet's own `arca openamp-key` is checked against it.
func openampPriv(t *testing.T, mnemonic string) string {
	seed, err := pbkdf2.Key(sha512.New, mnemonic, []byte("mnemonic"), 2048, 64)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha512.New, []byte("Bitcoin seed"))
	mac.Write(seed)
	I := mac.Sum(nil)
	var k btcec.ModNScalar
	k.SetByteSlice(I[:32])
	chain := I[32:]
	for _, i := range []uint32{5, 0} {
		pub := btcec.PrivKeyFromScalar(&k).PubKey().SerializeCompressed()
		data := append(append([]byte{}, pub...), 0, 0, 0, 0)
		binary.BigEndian.PutUint32(data[33:], i)
		m := hmac.New(sha512.New, chain)
		m.Write(data)
		I = m.Sum(nil)
		var il btcec.ModNScalar
		il.SetByteSlice(I[:32])
		k.Add(&il)
		chain = I[32:]
	}
	b := k.Bytes()
	return hex.EncodeToString(b[:])
}

func (r *payReg) startSeqpald() {
	reg := &regtest{t: r.t, url: r.nodeURL, user: r.nodeUser, pass: r.nodePwd, usdx: r.x, bin: r.bin, work: r.work, ev: r.ev, oa: r.oa}
	reg.revenueAddr = ""
	extra := []string{"SEQPALD_PAYOUT_URL=" + r.server}
	reg.startSeqpaldWith(extra)
	r.base = reg.base
	r.reg = reg
}

func (r *payReg) run() {
	t := r.t
	st := r.ctl("/state", nil)
	r.server, r.nodeURL = st["server"].(string), st["node_url"].(string)
	r.nodeUser, r.nodePwd, r.x = st["node_user"].(string), st["node_password"].(string), st["x"].(string)
	r.log("operator harness: server %s, node %s (elementsregtest, anchored), X %s (USDX here), genesis %s",
		r.server, r.nodeURL, r.x, st["genesis_hash"])
	r.oa = newM5Stub(t)
	r.startSeqpald()
	reg := r.reg
	r.log("seqpald %s with SEQPALD_PAYOUT_URL=%s, SEQPALD_USDX_ASSET=X; openampd: the test stub", r.base, r.server)

	// --- the issuer: a SeqPal ID, and its own wallet holding X off the chain ---
	issuer := reg.newActor("Issuer")
	reg.register(issuer, "HN")
	payer := r.newWallet("issuer wallet", "")
	addr := payer.ok("address")
	r.ctl("/fund", map[string]any{"script": addr["script_pubkey"], "asset": r.x, "amount": 40_000_000})
	r.ctl("/produce", nil)
	payer.ok("board", r.x, "30000000")
	r.ctl("/produce", nil)
	r.ctl("/bury", nil)
	for i := 0; i < 60; i++ {
		b := payer.list("boards")
		if len(b) > 0 && b[0].(map[string]any)["server"].(map[string]any)["state"] == "credited" {
			break
		}
		time.Sleep(time.Second)
	}
	payer.ok("sync")
	r.log("the issuer's wallet boards 30,000,000 X and holds %d X live", payer.liveX())

	// --- the security, and its holders, each a wallet with its own OpenAMP key ---
	issID, asset := reg.deploy(issuer, "PAYD")
	r.log("")
	r.log("== distribution: the holders of PAYD (%s) paid USDX to their wallets ==", asset)
	code, out, raw := reg.call(issuer, "POST", "/api/issuances/"+issID+"/distributions", map[string]any{"pool_atoms": 5000000, "asset": asset})
	r.log("  the security itself as the paid asset: %d %s", code, strings.TrimSpace(raw))
	if code != 409 || out["restricted"] != true {
		t.Fatalf("a restricted asset as the paid asset: %d %s", code, raw)
	}
	type holder struct {
		a   *actor
		w   *wallet
		key string
	}
	var holders []holder
	for i := 0; i < 5; i++ {
		w := r.newWallet(fmt.Sprintf("holder %d", i+1), "")
		priv := openampPriv(t, w.mnemonic())
		a := &actor{name: fmt.Sprintf("Holder %d", i+1), priv: priv, xonly: xonlyHex(t, priv)}
		if k := w.ok("openamp-key")["key"]; k != a.xonly {
			t.Fatalf("the wallet's OpenAMP key %v is not m/5/0 of its mnemonic %s", k, a.xonly)
		}
		reg.register(a, "HN")
		res := "GB"
		if i%2 == 0 {
			res = "US"
		}
		r.must(func() error {
			return r.seedClaims(a.aid, res)
		})
		r.oa.setBalance(a.aid, asset, uint64(1000*(i+1)))
		holders = append(holders, holder{a, w, a.xonly})
		r.log("  holder %d: SeqPal ID %s, OpenAMP key %s (its wallet's m/5/0), holds %d PAYD atoms, %s person", i+1,
			shortAID(a.aid), a.xonly, 1000*(i+1), map[string]string{"US": "US", "GB": "non-US (GB)"}[res])
	}
	// The issuer's own escrow enclave holds some too: an account seqpald knows no
	// wallet key for, so it is left out.
	stray := strings.Repeat("ab", 20)
	r.oa.setBalance(stray, asset, 1000)

	cr := reg.must(issuer, "POST", "/api/issuances/"+issID+"/distributions", map[string]any{"pool_atoms": 16_000_000, "memo": "regtest dividend"})
	runID := cr["distribution"].(map[string]any)["id"].(string)
	reg.must(issuer, "POST", "/api/issuances/"+issID+"/distributions/"+runID+"/snapshot", nil)
	plan := reg.must(issuer, "POST", "/api/issuances/"+issID+"/distributions/"+runID+"/payout", nil)
	pay := plan["payout"].(map[string]any)
	pid := pay["id"].(string)
	r.log("  the issuer's page: %s", plan["summary"])
	for _, row := range plan["rows"].([]any) {
		m := row.(map[string]any)
		r.log("    %s %s %d atoms %v %v", shortAID(m["holder_aid"].(string)), m["state"], jsonAtoms(m["atoms"]), orNone(m["key"]), orNone(m["reason"]))
	}
	if jsonAtoms(pay["holders"]) != 5 || jsonAtoms(pay["left_out"]) != 1 {
		t.Fatalf("want 5 holders and 1 left out: %v", pay)
	}
	list := r.getText(issuer, "/api/issuances/"+issID+"/payouts/"+pid+"/list")
	file := filepath.Join(r.work, "distribution.txt")
	os.WriteFile(file, []byte(list), 0o600)
	r.log("  the payment list seqpald gives (%s):\n%s", file, indent(list))
	r.shot(issuer, issID, "distribution", "1-distribution-planned")

	// The payer's wallet refuses, before anything is signed: a holder named
	// twice, and a total it does not hold.
	lines := strings.Split(strings.TrimSpace(list), "\n")
	twice := filepath.Join(r.work, "twice.txt")
	os.WriteFile(twice, []byte(lines[1]+"\n"+lines[1]+"\n"), 0o600)
	r.log("  a holder named twice: %s", payer.refused("is named twice", "distribute", twice, "--asset", r.x, "--quote"))
	tooMuch := filepath.Join(r.work, "too-much.txt")
	os.WriteFile(tooMuch, []byte(strings.Fields(lines[1])[0]+" 900000000\n"), 0o600)
	r.log("  a total the wallet does not hold, quoted: %s", payer.refused("and the claims and the fee take", "distribute", tooMuch, "--asset", r.x, "--quote"))
	r.log("  a total the wallet does not hold, paid: %s", payer.refused("and the claims and the fee take", "distribute", tooMuch, "--asset", r.x))

	quote := payer.ok("distribute", file, "--asset", r.x, "--quote")
	fee, _ := strconv.ParseUint(fmt.Sprint(quote["fee"]), 10, 64)
	r.log("  the wallet's quote: total %v, fee %d (the page's bound %d), split %v", quote["total"], fee, jsonAtoms(pay["fee_bound"]), quote["split"])
	if fee > jsonAtoms(pay["fee_bound"]) {
		t.Fatalf("the wallet's fee %d is above the bound the page showed %d", fee, jsonAtoms(pay["fee_bound"]))
	}
	d := payer.ok("distribute", file, "--asset", r.x, "--shown", strconv.FormatUint(fee, 10))
	refID := d["distribution"].(string)
	r.log("  the wallet's payment: reference %s, state %v", refID, d["state"])
	rec := reg.must(issuer, "POST", "/api/issuances/"+issID+"/payouts/"+pid+"/reference", map[string]any{"reference": refID})
	r.log("  recorded: %s", rec["summary"])

	round := r.ctl("/round", nil)
	r.log("  the round %v: %v vB", round["round"], round["vsize"])
	payer.ok("sync")
	got := r.waitPayout(issuer, issID, pid, "paid")
	r.log("  the issuer's page: %s", got["summary"])
	if got["payout"].(map[string]any)["round_txid"] != round["round"] {
		t.Fatalf("the payout names round %v, the harness built %v", got["payout"].(map[string]any)["round_txid"], round["round"])
	}

	// Three holders' wallets collect at their next sync; two never open.
	for _, h := range holders[:3] {
		found := h.w.list("claims")
		if len(found) != 1 || found[0].(map[string]any)["named_as"] != "openamp" {
			t.Fatalf("%s's claims: %v", h.a.name, found)
		}
		h.w.list("claim")
		r.log("  %s's wallet: claims found under its OpenAMP key, taken up; it holds %d X", h.a.name, h.w.liveX())
	}
	got = r.waitCollected(issuer, issID, pid, 3)
	r.log("  the issuer's page: %s", got["summary"])
	r.shot(issuer, issID, "distribution", "2-distribution-paid")

	// --- the refund: three subscribers, paid back from the issuer's wallet ---
	r.log("")
	r.log("== refund: three subscribers of PAYR whose money was released, refunded to their wallets ==")
	issR, _ := reg.deploy(issuer, "PAYR")
	var subs []holder
	for i := 0; i < 3; i++ {
		w := r.newWallet(fmt.Sprintf("subscriber %d", i+1), "")
		priv := openampPriv(t, w.mnemonic())
		a := &actor{name: fmt.Sprintf("Subscriber %d", i+1), priv: priv, xonly: xonlyHex(t, priv)}
		reg.register(a, "HN")
		subs = append(subs, holder{a, w, a.xonly})
		id := fmt.Sprintf("payr-sub-%d", i+1)
		r.must(func() error { return r.seedSettledSubscription(id, issR, a.aid, uint64(1_000_000*(i+1))) })
		r.log("  %s: SeqPal ID %s, OpenAMP key %s; subscription %s settled (deposited %d USDX atoms, released to the issuer)",
			a.name, shortAID(a.aid), a.xonly, id, 1_000_000*(i+1))
	}
	rp := reg.must(issuer, "POST", "/api/issuances/"+issR+"/refunds", map[string]any{"memo": "offering unwound"})
	rpay := rp["payout"].(map[string]any)
	rpid := rpay["id"].(string)
	r.log("  the issuer's page: %s", rp["summary"])
	r.shot(issuer, issR, "refund", "4-refund-planned")
	code, _, raw = reg.call(issuer, "POST", "/api/issuances/"+issR+"/refunds", map[string]any{"subscription_ids": []string{"payr-sub-1"}})
	r.log("  the same subscriber refunded again: %d %s", code, strings.TrimSpace(raw))
	if code != 409 {
		t.Fatalf("a second refund: %d", code)
	}
	rlist := r.getText(issuer, "/api/issuances/"+issR+"/payouts/"+rpid+"/list")
	rfile := filepath.Join(r.work, "refund.txt")
	os.WriteFile(rfile, []byte(rlist), 0o600)
	rq := payer.ok("distribute", rfile, "--asset", r.x, "--quote")
	rd := payer.ok("distribute", rfile, "--asset", r.x, "--shown", fmt.Sprint(rq["fee"]))
	reg.must(issuer, "POST", "/api/issuances/"+issR+"/payouts/"+rpid+"/reference", map[string]any{"reference": rd["distribution"]})
	rround := r.ctl("/round", nil)
	payer.ok("sync")
	r.waitPayout(issuer, issR, rpid, "paid")
	r.log("  refund %v in round %v; fee %v", rd["distribution"], rround["round"], rq["fee"])
	for _, h := range subs {
		h.w.list("claim")
		r.log("  %s's wallet took its refund up; it holds %d X", h.a.name, h.w.liveX())
	}
	rgot := r.waitPayout(issuer, issR, rpid, "complete")
	r.log("  the issuer's page: %s", rgot["summary"])
	r.shot(issuer, issR, "refund", "5-refund-complete")

	// --- the deadline: the uncollected two return to the issuer ---
	r.log("")
	r.log("== the deadline: what holders 4 and 5 never collected returns to the issuer ==")
	expiry := int64(jsonAtoms(got["payout"].(map[string]any)["expiry"]))
	now := int64(jsonAtoms(r.ctl("/advance", map[string]any{"seconds": 1})["median_time"]))
	// The collected coins' free window, so the holders that collected keep them.
	if wait := expiry - 4*86400 - 12*3600 - now; wait > 0 {
		r.ctl("/advance", map[string]any{"seconds": wait})
	}
	for _, h := range append(append([]holder{}, holders[:3]...), subs...) {
		h.w.ok("sync")
	}
	payer.ok("sync")
	r.ctl("/round", nil)
	for _, h := range append(append([]holder{}, holders[:3]...), subs...) {
		h.w.ok("sync")
	}
	now = int64(jsonAtoms(r.ctl("/advance", map[string]any{"seconds": 1})["median_time"]))
	r.ctl("/advance", map[string]any{"seconds": expiry - now + 3600})
	r.ctl("/settle", nil)
	r.ctl("/advance", map[string]any{"seconds": 37 * 3600})
	var final map[string]any
	for i := 0; i < 20; i++ {
		r.ctl("/settle", nil)
		_, final, _ = reg.call(issuer, "GET", "/api/issuances/"+issID+"/payouts", nil)
		p := r.findPayout(final, pid)
		if p["payout"].(map[string]any)["state"] == "complete" {
			final = p
			break
		}
		time.Sleep(16 * time.Second) // seqpald's watcher ticks every 15 s
	}
	fp := final["payout"].(map[string]any)
	if fp["state"] != "complete" {
		t.Fatalf("the payout did not complete: %v", final)
	}
	r.log("  the issuer's page: %s", final["summary"])
	r.shot(issuer, issID, "distribution", "3-distribution-complete")
	var unclaimed uint64
	for _, row := range final["rows"].([]any) {
		m := row.(map[string]any)
		r.log("    %s %s %d atoms %v", shortAID(m["holder_aid"].(string)), m["state"], jsonAtoms(m["atoms"]), orNone(m["returned_in"]))
		if m["state"] == "returned" {
			unclaimed += jsonAtoms(m["atoms"])
		}
	}
	retTxid := fp["return_txids"].(string)
	var want uint64
	for _, h := range holders[3:] {
		for _, row := range final["rows"].([]any) {
			m := row.(map[string]any)
			if m["key"] == h.key {
				want += jsonAtoms(m["atoms"])
				if m["state"] != "returned" {
					t.Fatalf("%s: %v, want returned", h.a.name, m)
				}
			}
		}
	}
	if unclaimed != want || jsonAtoms(fp["returned_atoms"]) != want || retTxid == "" || strings.Contains(retTxid, ",") {
		t.Fatalf("returned %d (%v) in %q, want %d in one transaction", unclaimed, fp["returned_atoms"], retTxid, want)
	}
	// On the chain: the return pays the issuer's return script exactly that, in X.
	status := r.ctlStatus(refID)
	rs := status["return_script"].(string)
	tx := r.ctl("/rpc", map[string]any{"method": "getrawtransaction", "params": []any{retTxid, true}})
	var paid uint64
	for _, o := range tx["vout"].([]any) {
		m := o.(map[string]any)
		spk := m["scriptPubKey"].(map[string]any)
		if spk["hex"] == rs && m["asset"] == r.x {
			paid += atomsFromNumber(json.Number(fmt.Sprint(m["value"])))
		}
	}
	r.log("  the return %s on the chain: %v vB, pays the issuer's return script %s exactly %d X", retTxid, tx["vsize"], rs, paid)
	if paid != want {
		t.Fatalf("the return pays %d, want %d", paid, want)
	}
	// The issuer's books: the ledger rows, and the run's holder rows.
	led := r.ledgerKinds(issID)
	r.log("  the ledger of PAYD: %v", led)
	if led["distribution"] != 5 || led["distribution_return"] != 2 {
		t.Fatalf("ledger %v, want 5 distribution and 2 distribution_return rows", led)
	}
	view := reg.must(issuer, "GET", "/api/issuances/"+issID+"/distributions/"+runID, nil)
	states := map[string]int{}
	for _, p := range view["payments"].([]any) {
		states[p.(map[string]any)["state"].(string)]++
	}
	r.log("  the run's holder rows: %v; run %v", states, view["distribution"].(map[string]any)["state"])
	if states["paid"] != 3 || states["returned"] != 2 || states["skipped"] != 1 {
		t.Fatalf("holder rows %v", states)
	}
	for _, h := range holders[:3] {
		r.log("  %s still holds %d X after the return", h.a.name, h.w.liveX())
	}
	late := holders[4].w.list("claims")
	r.log("  holder 5's wallet, opened after the deadline, sees its claim %v", late[0].(map[string]any)["state"])
}

// shot screenshots the issuer's page (scripts/e2e/payouts-page.mjs) when
// SEQPAL_REGTEST_SHOTS names a directory, CHROME a Chromium, and seqpald serves
// the built page.
func (r *payReg) shot(a *actor, issID, mode, name string) {
	dir, chrome := os.Getenv("SEQPAL_REGTEST_SHOTS"), os.Getenv("CHROME")
	if dir == "" || chrome == "" || os.Getenv("SEQPAL_REGTEST_WEBROOT") == "" {
		return
	}
	out := filepath.Join(dir, name+".png")
	cmd := exec.Command("node", filepath.Join("..", "scripts", "e2e", "payouts-page.mjs"), chrome, r.base, a.session, issID, mode, out)
	b, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("screenshot %s: %v\n%s", name, err, b)
	}
	r.log("  screenshot %s: %s", out, strings.TrimSpace(string(b)))
}

func (r *payReg) must(f func() error) {
	r.t.Helper()
	if err := f(); err != nil {
		r.t.Fatal(err)
	}
}

func orNone(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func indent(s string) string {
	return "      " + strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n      ")
}

func (r *payReg) getText(a *actor, path string) string {
	req, _ := http.NewRequest("GET", r.base+"/seqpal"+path, nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: a.session})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	var b bytes.Buffer
	b.ReadFrom(resp.Body)
	if resp.StatusCode != 200 {
		r.t.Fatalf("GET %s: %d %s", path, resp.StatusCode, b.String())
	}
	return b.String()
}

func (r *payReg) findPayout(list map[string]any, pid string) map[string]any {
	for _, p := range list["payouts"].([]any) {
		m := p.(map[string]any)
		if m["payout"].(map[string]any)["id"] == pid {
			return m
		}
	}
	r.t.Fatalf("payout %s not listed", pid)
	return nil
}

func (r *payReg) waitPayout(a *actor, issID, pid, state string) map[string]any {
	for i := 0; i < 60; i++ {
		_, list, _ := r.reg.call(a, "GET", "/api/issuances/"+issID+"/payouts", nil)
		p := r.findPayout(list, pid)
		if p["payout"].(map[string]any)["state"] == state {
			return p
		}
		time.Sleep(2 * time.Second)
	}
	r.t.Fatalf("payout %s never reached %s", pid, state)
	return nil
}

func (r *payReg) waitCollected(a *actor, issID, pid string, n int) map[string]any {
	for i := 0; i < 60; i++ {
		_, list, _ := r.reg.call(a, "GET", "/api/issuances/"+issID+"/payouts", nil)
		p := r.findPayout(list, pid)
		c := 0
		for _, row := range p["rows"].([]any) {
			if row.(map[string]any)["state"] == "collected" {
				c++
			}
		}
		if c == n {
			return p
		}
		time.Sleep(2 * time.Second)
	}
	r.t.Fatalf("payout %s: never %d collected", pid, n)
	return nil
}

// ctlStatus reads the payment service's public status of a payment.
func (r *payReg) ctlStatus(id string) map[string]any {
	b, _ := json.Marshal(map[string]any{"distribution_id": id})
	resp, err := http.Post(r.server+"/v1/distribution_status", "application/json", bytes.NewReader(b))
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return out
}

func (r *payReg) db() *sql.DB {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(r.work, "seqpald.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		r.t.Fatal(err)
	}
	return db
}

// seedClaims gives a holder a verified identity and its tax residence, which
// the withholding reads; an identity provider does this in production.
func (r *payReg) seedClaims(aid, residence string) error {
	db := r.db()
	defer db.Close()
	st := &Store{db: db}
	return st.UpsertClaims(&Claims{AID: aid, Residence: residence, BaseEligibility: "ret", USPerson: residence == "US", Status: "verified"})
}

// seedSettledSubscription writes a subscription whose money the escrow
// released to the issuer and whose tokens were delivered, as a close leaves
// it. The close itself is proven by scripts/regtest-escrow.sh; this proof
// starts where a refund after the release starts.
func (r *payReg) seedSettledSubscription(id, issID, aid string, atoms uint64) error {
	db := r.db()
	defer db.Close()
	st := &Store{db: db}
	if err := st.InsertSubscription(&Subscription{ID: id, IssuanceID: issID, InvestorAID: aid, Rail: "usdx", PayCcy: "USDX",
		State: "settled", DepositedAtoms: atoms, PayAmount: atoms, TokenAtoms: atoms / 100}); err != nil {
		return err
	}
	if _, err := st.CreateSettlementIfAbsent(id, issID); err != nil {
		return err
	}
	return st.UpdateSettlementFields(id, map[string]any{"state": "settled", "release_txid": strings.Repeat("0", 64)})
}

func (r *payReg) ledgerKinds(issID string) map[string]int {
	db := r.db()
	defer db.Close()
	st := &Store{db: db}
	rows, err := st.LedgerByIssuance(issID)
	if err != nil {
		r.t.Fatal(err)
	}
	out := map[string]int{}
	for _, e := range rows {
		out[e.Kind]++
	}
	return out
}
