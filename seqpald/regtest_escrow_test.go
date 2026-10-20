package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ConcatenaLabs/SeqPal/seqpald/elements"
)

// TestRegtestEscrowCovenant is the end-to-end proof of the subscription escrow
// covenant and the one-transaction distribution, run against a REAL Sequentia
// node on a private elementsregtest chain and the REAL seqpald binary, over
// its HTTP API. The policy server is the openampd test stub (m5_test.go),
// served from this process: token delivery is OpenAMP's and is not what this
// proves. scripts/regtest-escrow.sh starts the node, prepares the chain and
// runs this; without SEQPAL_REGTEST_NODE_URL it is skipped.
//
// What it shows, each on chain:
//   - every subscription output pays its own escrow covenant address, and the
//     platform wallet seqpal-escrow holds none of it;
//   - a release before the close is refused IN A BLOCK (generateblock), both
//     with its lock time below the close (the script's CHECKLOCKTIMEVERIFY) and
//     at the close but mined early (not final);
//   - the release after the close, signed in the issuer's own node wallet with
//     signopenamptransfer, broadcast by seqpald and mined, output 0 paying the
//     issuer at least the minimum;
//   - an issuer-signed cancel, and the keyless refund after the deadline,
//     broadcast by seqpald's own watcher and mined; a refund before the
//     deadline refused in a block;
//   - a distribution to ten holders in one transaction, funded and signed by
//     the issuer's own wallet, mined.
func TestRegtestEscrowCovenant(t *testing.T) {
	nodeURL := os.Getenv("SEQPAL_REGTEST_NODE_URL")
	if nodeURL == "" {
		t.Skip("set SEQPAL_REGTEST_NODE_URL (scripts/regtest-escrow.sh does) to run against a regtest node")
	}
	r := &regtest{t: t, url: nodeURL, user: os.Getenv("SEQPAL_REGTEST_NODE_USER"), pass: os.Getenv("SEQPAL_REGTEST_NODE_PASS"),
		usdx: os.Getenv("SEQPAL_REGTEST_USDX"), bin: os.Getenv("SEQPAL_REGTEST_SEQPALD"), work: os.Getenv("SEQPAL_REGTEST_WORKDIR")}
	if r.usdx == "" || r.bin == "" || r.work == "" {
		t.Fatal("SEQPAL_REGTEST_USDX, SEQPAL_REGTEST_SEQPALD and SEQPAL_REGTEST_WORKDIR are required")
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

type regtest struct {
	t                         *testing.T
	url, user, pass, usdx     string
	bin, work                 string
	ev                        *os.File
	base                      string
	oa                        *m5Stub
	minerAddr                 string
	issuerPayout, revenueAddr string
}

func (r *regtest) log(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	r.t.Log(msg)
	if r.ev != nil {
		fmt.Fprintln(r.ev, msg)
	}
}

func (r *regtest) rpcErr(wallet, method string, params ...any) (json.RawMessage, error) {
	u := r.url
	if wallet != "" {
		u = strings.TrimRight(u, "/") + "/wallet/" + wallet
	}
	return jsonRPC(&http.Client{Timeout: 120 * time.Second}, u, r.user, r.pass, method, params...)
}

func (r *regtest) rpc(wallet, method string, params ...any) json.RawMessage {
	r.t.Helper()
	res, err := r.rpcErr(wallet, method, params...)
	if err != nil {
		r.t.Fatalf("%s %s: %v", wallet, method, err)
	}
	return res
}

func (r *regtest) str(wallet, method string, params ...any) string {
	r.t.Helper()
	var s string
	if err := json.Unmarshal(r.rpc(wallet, method, params...), &s); err != nil {
		r.t.Fatalf("%s: not a string", method)
	}
	return s
}

func (r *regtest) height() int64 {
	var h int64
	json.Unmarshal(r.rpc("", "getblockcount"), &h)
	return h
}

func (r *regtest) mine(n int) {
	r.rpc("", "generatetoaddress", n, r.minerAddr)
}

func (r *regtest) mineTo(h int64) {
	if d := h - r.height(); d > 0 {
		r.mine(int(d))
	}
}

// forceBlock mines a block containing exactly these transactions, bypassing
// the mempool, and returns the node's refusal (empty when it was mined).
func (r *regtest) forceBlock(txs ...string) string {
	_, err := r.rpcErr("", "generateblock", r.minerAddr, txs)
	if err != nil {
		return err.Error()
	}
	return ""
}

// --- the HTTP side --------------------------------------------------------------

// actor is one person using SeqPal: a key their wallet holds, and the session
// cookie seqpald gave them. The cookie is Secure (production is behind TLS), so
// it is carried by hand over this loopback connection.
type actor struct {
	name, priv, xonly, aid string
	session                string
}

func (r *regtest) newActor(name string) *actor {
	priv := genPriv(r.t)
	return &actor{name: name, priv: priv, xonly: xonlyHex(r.t, priv)}
}

func (r *regtest) call(a *actor, method, path string, body any) (int, map[string]any, string) {
	r.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader([]byte("{}"))
	}
	req, _ := http.NewRequest(method, r.base+"/seqpal"+path, rd)
	req.Header.Set("content-type", "application/json")
	if a != nil && a.session != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: a.session})
	}
	resp, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
	if err != nil {
		r.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	if a != nil {
		for _, c := range resp.Cookies() {
			if c.Name == sessionCookie && c.Value != "" {
				a.session = c.Value
			}
		}
	}
	raw, _ := readAllLimited(resp.Body, 16<<20)
	var out map[string]any
	json.Unmarshal(raw, &out)
	return resp.StatusCode, out, string(raw)
}

func (r *regtest) must(a *actor, method, path string, body any) map[string]any {
	r.t.Helper()
	code, out, raw := r.call(a, method, path, body)
	if code != 200 {
		r.t.Fatalf("%s %s: %d %s", method, path, code, raw)
	}
	return out
}

func (r *regtest) register(a *actor, residence string) {
	ch := r.must(nil, "POST", "/api/auth/challenge", map[string]any{"xonly": a.xonly})
	challenge, _ := ch["challenge"].(string)
	out := r.must(a, "POST", "/api/auth/register", map[string]any{
		"xonly": a.xonly, "challenge": challenge, "sig": signChallengeHex(r.t, a.priv, challenge),
		"display_name": a.name, "residence": residence,
	})
	acct, _ := out["account"].(map[string]any)
	a.aid, _ = acct["aid"].(string)
}

// verify submits the identity to the (simulated) verification provider and
// waits for its decision, which this deployment delivers in a second.
func (r *regtest) verify(a *actor) {
	r.must(a, "POST", "/api/id/verify", map[string]any{"residence": "HN", "base_eligibility": "ret"})
	for i := 0; i < 60; i++ {
		_, me, _ := r.call(a, "GET", "/api/id/passport", nil)
		if me["status"] == "verified" {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	r.t.Fatalf("%s was not verified", a.name)
}

// sign2 runs a two-phase signed statement: post without a signature, sign the
// sign_this bytes under the returned tag with the actor's key, post again.
func (r *regtest) sign2(a *actor, path string, body map[string]any) map[string]any {
	pre := r.must(a, "POST", path, body)
	signThis, _ := pre["sign_this"].(string)
	tag, _ := pre["tag"].(string)
	if signThis == "" || tag == "" {
		r.t.Fatalf("%s returned nothing to sign: %v", path, pre)
	}
	body["signature"] = signCanonical(r.t, a.priv, tag, signThis)
	return r.must(a, "POST", path, body)
}

func (r *regtest) deploy(issuer *actor, ticker string) (string, string) {
	out := r.must(issuer, "POST", "/api/issuances", map[string]any{
		"name": ticker + " Co", "ticker": ticker, "structure_id": "native-equity",
		"terms": map[string]any{"jurisdictions": map[string]any{"HN": map[string]any{"access": "standard"}}, "price": 1.0},
	})
	iss, _ := out["issuance"].(map[string]any)
	id, _ := iss["id"].(string)
	dep := r.must(issuer, "POST", "/api/deploy", map[string]any{"issuance_id": id, "supply": 1000000, "precision": 2})
	asset, _ := dep["asset"].(string)
	if pub, _ := dep["issuer_pubkey"].(string); pub != "" {
		r.oa.markExternal(asset, pub)
	}
	return id, asset
}

// releaseKey is a key of the issuer's OWN node wallet: a legacy address, so the
// wallet can both sign the escrow terms as an ordinary message and sign the
// release leaf with signopenamptransfer.
func (r *regtest) releaseKey() (addr, xonly string) {
	addr = r.str("issuer", "getnewaddress", "release-key", "legacy")
	var info struct {
		PubKey string `json:"pubkey"`
	}
	json.Unmarshal(r.rpc("issuer", "getaddressinfo", addr), &info)
	return addr, info.PubKey[2:]
}

// openEscrow opens an offering's escrow: the issuer's payout mandate, then the
// terms, signed by the release key in the issuer's node wallet (signmessage).
func (r *regtest) openEscrow(issuer *actor, issID string, minimum uint64, close int64) map[string]any {
	r.sign2(issuer, "/api/issuances/"+issID+"/mandate", map[string]any{"chain": "sequentia", "address": r.issuerPayout})
	keyAddr, key := r.releaseKey()
	body := map[string]any{"release_key": key, "minimum_atoms": minimum, "close_height": close}
	pre := r.must(issuer, "POST", "/api/issuances/"+issID+"/escrow", body)
	msg, _ := pre["sign_this_message"].(string)
	body["signature"] = r.str("issuer", "signmessage", keyAddr, msg)
	out := r.must(issuer, "POST", "/api/issuances/"+issID+"/escrow", body)
	esc, _ := out["escrow"].(map[string]any)
	r.log("  escrow opened: release key %s (a key of the issuer's own node wallet, proven by signmessage), "+
		"minimum %d atoms, close %v, deadline %v, fee cap %d atoms",
		key, jsonAtoms(esc["minimum_atoms"]), esc["close_text"], esc["deadline_text"], jsonAtoms(esc["fee_cap"]))
	return esc
}

type paidSub struct {
	id, address, spk, txid, refund string
	vout                           uint32
	atoms                          uint64
}

// subscribe subscribes on the Sequentia rail and pays the deposit from the
// subscribers' wallet, transparently, in USDX, the fee in USDX too.
func (r *regtest) subscribe(inv *actor, issID string, tokens int) paidSub {
	refund := r.str("subs", "getnewaddress", inv.name+"-refund", "bech32")
	out := r.must(inv, "POST", "/api/issuances/"+issID+"/subscribe", map[string]any{
		"rail": "usdx", "amount": tokens, "refund_address": refund,
	})
	sub, _ := out["subscription"].(map[string]any)
	p := paidSub{refund: refund}
	p.id, _ = sub["id"].(string)
	p.address, _ = out["deposit_address"].(string)
	p.spk, _ = sub["escrow_spk"].(string)
	p.atoms = jsonAtoms(out["pay_amount"])
	var vi struct {
		ScriptPubKey string `json:"scriptPubKey"`
	}
	json.Unmarshal(r.rpc("", "validateaddress", p.address), &vi)
	if vi.ScriptPubKey != p.spk {
		r.t.Fatalf("the node reads %s as %s, seqpald derived %s", p.address, vi.ScriptPubKey, p.spk)
	}
	p.txid = r.str("subs", "sendtoaddress", p.address, amount8(p.atoms), "", "", false, false, 1, "UNSET", false,
		r.usdx, false, nil, r.usdx)
	var tx struct {
		Vout []struct {
			N            uint32 `json:"n"`
			Asset        string `json:"asset"`
			ScriptPubKey struct {
				Hex string `json:"hex"`
			} `json:"scriptPubKey"`
		} `json:"vout"`
	}
	json.Unmarshal(r.rpc("", "getrawtransaction", p.txid, true), &tx)
	found := false
	for _, o := range tx.Vout {
		if o.ScriptPubKey.Hex == p.spk {
			if found {
				r.t.Fatalf("deposit %s pays the escrow twice", p.txid)
			}
			p.vout, found = o.N, true
			if o.Asset != r.usdx {
				r.t.Fatalf("deposit %s pays the escrow in %s", p.txid, o.Asset)
			}
		}
	}
	if !found {
		r.t.Fatalf("deposit %s does not pay the escrow address %s", p.txid, p.address)
	}
	return p
}

func (r *regtest) waitSubState(inv *actor, subID, want string) {
	for i := 0; i < 120; i++ {
		out := r.must(inv, "GET", "/api/subscriptions", nil)
		list, _ := out["subscriptions"].([]any)
		for _, s := range list {
			m, _ := s.(map[string]any)
			if m["id"] == subID && m["state"] == want {
				return
			}
		}
		time.Sleep(time.Second)
	}
	r.t.Fatalf("subscription %s never reached %s", subID, want)
}

// signSpend signs a built escrow spend in the issuer's own node wallet:
// signopenamptransfer recomputes each sighash from the transaction, checks the
// control block commits to the coin, and signs only what matches.
func (r *regtest) signSpend(spend map[string]any) json.RawMessage {
	tx, _ := spend["unsigned_tx"].(string)
	res := r.rpc("issuer", "signopenamptransfer", tx, spend["to_sign"])
	var out struct {
		Signatures json.RawMessage `json:"signatures"`
	}
	json.Unmarshal(res, &out)
	return out.Signatures
}

func (r *regtest) startSeqpald() {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		r.t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	price := newPriceStub(r.t, map[string]float64{"BTC": 100000, "SEQ": 0.1, "USDX": 1})
	cmd := exec.Command(r.bin)
	cmd.Env = append(os.Environ(),
		"SEQPALD_LISTEN="+addr, "SEQPALD_DB="+filepath.Join(r.work, "seqpald.db"),
		"OPENAMPD_URL="+r.oa.srv.URL, "OPENAMPD_ISSUER_TOKEN=test-issuer-token",
		"SEQPALD_NETWORK=elementsregtest", "SEQPALD_NODE_URL="+r.url, "SEQPALD_NODE_USER="+r.user,
		"SEQPALD_NODE_PASS="+r.pass, "SEQPALD_USDX_ASSET="+r.usdx, "SEQPALD_PRICE_URL="+price.URL,
		"SEQPALD_ELECTRS_URL=", "SEQPALD_REGISTRY_URL=", "SEQPALD_SETUP_FEE_USD=0", "SEQPALD_KYC_FEE_USD=0",
		"SEQPALD_KYB_FEE_USD=0", "SEQPALD_ESCROW_FEE_BPS=50", "SEQPALD_ESCROW_CONFS=1", "SEQPALD_IDV_DECISION_SECS=1",
		"SEQPALD_BLOCKS_PER_DAY=2", "SEQPALD_REVENUE_ADDRESS="+r.revenueAddr, "SEQPALD_WEBROOT="+os.Getenv("SEQPAL_REGTEST_WEBROOT"),
	)
	logf, err := os.Create(filepath.Join(r.work, "seqpald.log"))
	if err != nil {
		r.t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		r.t.Fatal(err)
	}
	r.log("seqpald binary %s started, pid %d, listening on %s", r.bin, cmd.Process.Pid, addr)
	r.t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
		logf.Close()
	})
	r.base = "http://" + addr
	for i := 0; i < 100; i++ {
		if resp, err := http.Get(r.base + "/api/health"); err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	r.t.Fatal("seqpald did not come up")
}

func (r *regtest) run() {
	t := r.t
	r.minerAddr = r.str("miner", "getnewaddress", "", "bech32")
	r.issuerPayout = r.str("issuer", "getnewaddress", "payout", "bech32")
	r.revenueAddr = r.str("platform", "getnewaddress", "escrow-fee", "bech32")
	r.oa = newM5Stub(t)
	r.startSeqpald()
	var genesis string
	json.Unmarshal(r.rpc("", "getblockhash", 0), &genesis)
	r.log("node: elementsregtest, genesis %s, tip %d, USDX %s", genesis, r.height(), r.usdx)
	r.log("openampd: the seqpald test stub (m5_test.go) at %s; token delivery is the stub's", r.oa.srv.URL)

	issuer := r.newActor("Issuer")
	r.register(issuer, "HN")

	// ===== Offering 1: subscriptions, a release refused before the close, the release.
	r.log("")
	r.log("== offering 1: three subscriptions, released after the close ==")
	iss1, _ := r.deploy(issuer, "ESCA")
	close1 := r.height() + 25
	esc1 := r.openEscrow(issuer, iss1, 250*1e8, close1)
	var subs1 []paidSub
	var invs1 []*actor
	for i, tokens := range []int{100, 150, 50} {
		inv := r.newActor(fmt.Sprintf("Subscriber %c", 'A'+i))
		r.register(inv, "HN")
		r.verify(inv)
		p := r.subscribe(inv, iss1, tokens)
		subs1 = append(subs1, p)
		invs1 = append(invs1, inv)
		r.log("  %s subscribed %d tokens: pays %d atoms USDX to its own escrow address %s (spk %s) in tx %s:%d",
			inv.name, tokens, p.atoms, p.address, p.spk, p.txid, p.vout)
	}
	r.mine(1)
	for i, p := range subs1 {
		r.waitSubState(invs1[i], p.id, "in_escrow")
	}
	r.log("  all three in_escrow, credited from scantxoutset (no wallet), at %d confirmation(s)", 1)

	// Every subscription output pays its escrow covenant address, and nothing of it reaches seqpal-escrow.
	addrs := map[string]bool{}
	for _, p := range subs1 {
		addrs[p.address] = true
	}
	if len(addrs) != 3 {
		t.Fatalf("subscriptions share an escrow address: %v", addrs)
	}
	r.assertHotWalletClean("after the deposits", subs1)

	// --- a release before the close, forced into a block -------------------------
	st := r.must(issuer, "GET", "/api/issuances/"+iss1+"/escrow", nil)
	terms := termsFromView(t, st["escrow"].(map[string]any), iss1, r.issuerPayoutSPK())
	early := r.buildReleaseLike(terms, subs1, uint32(r.height()))
	errStr := r.forceBlock(early)
	r.log("  release with lock time %d (below the close %d), signed by the issuer wallet, forced with generateblock at tip %d:", r.height(), close1, r.height())
	r.log("    refused: %s", errStr)
	if !strings.Contains(errStr, "Locktime requirement not satisfied") {
		t.Fatalf("early release: want the CHECKLOCKTIMEVERIFY failure, got %q", errStr)
	}
	atClose := r.buildReleaseLike(terms, subs1, uint32(close1))
	errStr = r.forceBlock(atClose)
	r.log("  release with lock time %d (the close) mined at tip %d, before the close:", close1, r.height())
	r.log("    refused: %s", errStr)
	if !strings.Contains(errStr, "bad-txns-nonfinal") {
		t.Fatalf("release at the close mined early: want bad-txns-nonfinal, got %q", errStr)
	}
	code, _, raw := r.call(issuer, "POST", "/api/issuances/"+iss1+"/close", map[string]any{})
	if code == 200 {
		// The two-phase close: sign the authorization first.
		pre := r.must(issuer, "POST", "/api/issuances/"+iss1+"/close", map[string]any{})
		sig := signCanonical(t, issuer.priv, pre["tag"].(string), pre["sign_this"].(string))
		code, _, raw = r.call(issuer, "POST", "/api/issuances/"+iss1+"/close", map[string]any{"signature": sig, "signer_xonly": issuer.xonly})
	}
	r.log("  seqpald close before the close height: %d %s", code, strings.TrimSpace(raw))
	if code != 409 || r.oa.transfers() != 0 {
		t.Fatalf("close before the close: %d, deliveries %d", code, r.oa.transfers())
	}

	// --- after the close: deliver, then the issuer signs the release ----------------
	r.mineTo(close1)
	pre := r.must(issuer, "POST", "/api/issuances/"+iss1+"/close", map[string]any{})
	sig := signCanonical(t, issuer.priv, pre["tag"].(string), pre["sign_this"].(string))
	closed := r.must(issuer, "POST", "/api/issuances/"+iss1+"/close", map[string]any{"signature": sig, "signer_xonly": issuer.xonly})
	spends, _ := closed["escrow_spends"].([]any)
	if len(spends) != 1 {
		t.Fatalf("close returned %d spends, want one release: %v", len(spends), closed)
	}
	rel := spends[0].(map[string]any)
	det := rel["detail"].(map[string]any)
	r.log("  at tip %d seqpald delivered %d subscription(s) (stub) and built release %s over %d coins: total %d, "+
		"to the issuer %d, escrow fee %d, network fee %d (%d vB at %d atoms/kvB, in USDX), lock time %d",
		r.height(), r.oa.transfers(), rel["txid"], len(rel["to_sign"].([]any)), jsonAtoms(det["total_atoms"]),
		jsonAtoms(det["to_issuer_atoms"]), jsonAtoms(det["escrow_fee_atoms"]), jsonAtoms(det["network_fee_atoms"]),
		jsonAtoms(det["vsize"]), jsonAtoms(det["fee_rate_atoms_per_kvb"]), jsonAtoms(det["locktime"]))
	for _, o := range det["outputs"].([]any) {
		m := o.(map[string]any)
		r.log("    output %d: %d atoms to the %s %v", jsonAtoms(m["n"]), jsonAtoms(m["atoms"]), m["pays"], m["address"])
	}
	sigs := r.signSpend(rel)
	r.log("  issuer wallet signopenamptransfer returned %s", string(sigs))
	signed := r.must(issuer, "POST", "/api/issuances/"+iss1+"/escrow/spends/"+rel["id"].(string)+"/sign", map[string]any{"signatures": sigs})
	sp := signed["spend"].(map[string]any)
	if sp["state"] != "broadcast" {
		t.Fatalf("release not broadcast: %v", sp)
	}
	relTxid := sp["txid"].(string)
	r.mine(1)
	r.logMined("release", relTxid)
	r.assertOutput(relTxid, 0, r.issuerPayoutSPK(), r.usdx, uint64(jsonAtoms(esc1["minimum_atoms"])), true)
	for i, p := range subs1 {
		r.waitSubState(invs1[i], p.id, "settled")
	}
	r.log("  all three settled with the release txid")
	r.assertHotWalletClean("after the release", subs1)

	// ===== Offering 2: a cancel by the issuer, the keyless refund after the deadline.
	r.log("")
	r.log("== offering 2: one cancelled by the issuer, two refunded by anyone after the deadline ==")
	iss2, _ := r.deploy(issuer, "ESCB")
	close2 := r.height() + 8
	esc2 := r.openEscrow(issuer, iss2, 1000*1e8, close2)
	deadline2 := int64(jsonAtoms(esc2["deadline"]))
	var subs2 []paidSub
	var invs2 []*actor
	for i, tokens := range []int{40, 30, 20} {
		inv := r.newActor(fmt.Sprintf("Subscriber %c", 'D'+i))
		r.register(inv, "HN")
		r.verify(inv)
		p := r.subscribe(inv, iss2, tokens)
		subs2 = append(subs2, p)
		invs2 = append(invs2, inv)
		r.log("  %s subscribed %d tokens: %d atoms to %s in %s:%d", inv.name, tokens, p.atoms, p.address, p.txid, p.vout)
	}
	r.mine(1)
	for i, p := range subs2 {
		r.waitSubState(invs2[i], p.id, "in_escrow")
	}
	cancel := r.must(issuer, "POST", "/api/issuances/"+iss2+"/escrow/cancel", map[string]any{"subscription_ids": []string{subs2[0].id}})
	csp := cancel["spend"].(map[string]any)
	csigs := r.signSpend(csp)
	cdone := r.must(issuer, "POST", "/api/issuances/"+iss2+"/escrow/spends/"+csp["id"].(string)+"/sign", map[string]any{"signatures": csigs})
	cancelTxid := cdone["spend"].(map[string]any)["txid"].(string)
	r.mine(1)
	r.logMined("cancel (issuer-signed, before the close)", cancelTxid)
	r.assertOutput(cancelTxid, 0, r.spkOf(subs2[0].refund), r.usdx, subs2[0].atoms-r.feeCap(esc2), false)
	r.waitSubState(invs2[0], subs2[0].id, "refunded")

	// The subscriber's own refund transaction, which needs no signature.
	own := r.must(invs2[1], "GET", "/api/subscriptions/"+subs2[1].id+"/escrow", nil)
	refundTx, _ := own["refund_tx"].(string)
	if refundTx == "" {
		t.Fatalf("no refund_tx for the subscriber: %v", own)
	}
	errStr = r.forceBlock(refundTx)
	r.log("  the subscriber's own refund (lock time %d, the deadline) forced at tip %d, before the deadline:", deadline2, r.height())
	r.log("    refused: %s", errStr)
	if !strings.Contains(errStr, "bad-txns-nonfinal") {
		t.Fatalf("early refund: want bad-txns-nonfinal, got %q", errStr)
	}
	belowDeadline := r.refundLike(refundTx, uint32(r.height()))
	errStr = r.forceBlock(belowDeadline)
	r.log("  the same refund with its lock time set below the deadline (%d), forced:", r.height())
	r.log("    refused: %s", errStr)
	if !strings.Contains(errStr, "Locktime requirement not satisfied") {
		t.Fatalf("refund below the deadline: want the CHECKLOCKTIMEVERIFY failure, got %q", errStr)
	}

	// From the deadline, seqpald's own watcher refunds the two undelivered subscriptions.
	r.mineTo(deadline2)
	r.log("  mined to the deadline %d; waiting for seqpald's watcher", deadline2)
	var refundTxid string
	for i := 0; i < 90 && refundTxid == ""; i++ {
		time.Sleep(time.Second)
		st2 := r.must(issuer, "GET", "/api/issuances/"+iss2+"/escrow", nil)
		for _, s := range st2["spends"].([]any) {
			m := s.(map[string]any)
			if m["kind"] == "refund" && m["state"] == "broadcast" {
				refundTxid = m["txid"].(string)
			}
		}
	}
	if refundTxid == "" {
		t.Fatal("seqpald did not refund after the deadline")
	}
	r.mine(1)
	r.logMined("refund (keyless, broadcast by seqpald's watcher)", refundTxid)
	r.assertOutput(refundTxid, 0, r.spkOf(subs2[1].refund), r.usdx, subs2[1].atoms-r.feeCap(esc2), false)
	r.assertOutput(refundTxid, 1, r.spkOf(subs2[2].refund), r.usdx, subs2[2].atoms-r.feeCap(esc2), false)
	r.waitSubState(invs2[1], subs2[1].id, "refunded")
	r.waitSubState(invs2[2], subs2[2].id, "refunded")
	r.assertHotWalletClean("after the cancel and the refund", append(append([]paidSub{}, subs1...), subs2...))

	// ===== A distribution to ten holders in one transaction.
	r.log("")
	r.log("== distribution: ten holders of ESCD paid in one transaction from the issuer's own wallet ==")
	iss3, asset3 := r.deploy(issuer, "ESCD")
	var holders []*actor
	for i := 0; i < 10; i++ {
		h := r.newActor(fmt.Sprintf("Holder %d", i+1))
		r.register(h, "HN")
		addr := r.str("holders", "getnewaddress", h.name, "bech32")
		r.sign2(h, "/api/mandates/investor", map[string]any{"chain": "sequentia", "address": addr})
		r.oa.setBalance(h.aid, asset3, uint64(100*(i+1)))
		holders = append(holders, h)
	}
	cr := r.must(issuer, "POST", "/api/issuances/"+iss3+"/distributions", map[string]any{"pool_atoms": 55 * 1e8, "memo": "regtest dividend"})
	runID := cr["distribution"].(map[string]any)["id"].(string)
	r.must(issuer, "POST", "/api/issuances/"+iss3+"/distributions/"+runID+"/snapshot", nil)
	plan := r.must(issuer, "POST", "/api/issuances/"+iss3+"/distributions/"+runID+"/execute", map[string]any{})
	payout := plan["payout"].(map[string]any)
	outs := payout["outputs"].([]any)
	r.log("  payout plan: %d outputs totalling %d atoms of %s; the issuer's wallet funds it (fundrawtransaction, "+
		"fee in USDX) and signs it (signrawtransactionwithwallet)", len(outs), jsonAtoms(payout["total_atoms"]), payout["asset"])
	var funded struct {
		Hex string `json:"hex"`
	}
	json.Unmarshal(r.rpc("issuer", "fundrawtransaction", payout["unfunded_tx"], map[string]any{"fee_asset": r.usdx}), &funded)
	var signedTx struct {
		Hex      string `json:"hex"`
		Complete bool   `json:"complete"`
	}
	json.Unmarshal(r.rpc("issuer", "signrawtransactionwithwallet", funded.Hex), &signedTx)
	if !signedTx.Complete {
		t.Fatal("the issuer wallet did not complete the payout")
	}
	// A transaction that pays one holder an atom short is refused before broadcast.
	short := r.shortPayout(signedTx.Hex, outs[0].(map[string]any))
	code, _, raw = r.call(issuer, "POST", "/api/issuances/"+iss3+"/distributions/"+runID+"/execute", map[string]any{"tx": short})
	r.log("  a payout one atom short for one holder: %d %s", code, strings.TrimSpace(raw))
	if code != 400 {
		t.Fatalf("short payout accepted: %d", code)
	}
	done := r.must(issuer, "POST", "/api/issuances/"+iss3+"/distributions/"+runID+"/execute", map[string]any{"tx": signedTx.Hex})
	d := done["distribution"].(map[string]any)
	payTxid, _ := d["payout_txid"].(string)
	r.mine(1)
	r.logMined("distribution payout", payTxid)
	var dtx struct {
		Vout []struct {
			N     int         `json:"n"`
			Value json.Number `json:"value"`
			Asset string      `json:"asset"`
			SPK   struct {
				Hex     string `json:"hex"`
				Address string `json:"address"`
			} `json:"scriptPubKey"`
		} `json:"vout"`
	}
	json.Unmarshal(r.rpc("", "getrawtransaction", payTxid, true), &dtx)
	payments := done["payments"].([]any)
	paid := 0
	for _, p := range payments {
		m := p.(map[string]any)
		if m["state"] != "paid" || m["txid"] != payTxid {
			t.Fatalf("holder row not paid by the one txid: %v", m)
		}
		v := int(m["vout"].(float64))
		o := dtx.Vout[v]
		if o.SPK.Hex != m["mandate_script_pubkey"] || atomsFromNumber(o.Value) != jsonAtoms(m["net_atoms"]) || o.Asset != r.usdx {
			t.Fatalf("holder row %v does not match output %d %+v", m, v, o)
		}
		r.log("    holder %s: output %d pays %s %s USDX (net; %d atoms withheld)", shortAID(m["holder_aid"].(string)), v,
			o.SPK.Address, o.Value, jsonAtoms(m["withheld_atoms"]))
		paid++
	}
	if paid != 10 {
		t.Fatalf("%d holders paid, want 10", paid)
	}
	r.log("  ten holders paid in the one transaction %s (%d outputs in all, holders + change + fee)", payTxid, len(dtx.Vout))
	r.assertHotWalletClean("at the end", append(append([]paidSub{}, subs1...), subs2...))

	r.feesWithNoPolicyAsset(issuer)

	if helper := os.Getenv("SEQPAL_REGTEST_ISSUER_PAGE"); helper != "" {
		r.offeringFromThePage(helper, issuer)
	}
}

// feesWithNoPolicyAsset mints a bearer token, whose funding seqpald pays from
// its seqpal-escrow wallet, with that wallet holding USDX and none of the
// policy asset: the fee asset is chosen by the rule every wallet uses (the
// accepted asset whose largest coin covers the most fees), never a fallback to
// the policy asset, so the mint pays its fee in USDX.
func (r *regtest) feesWithNoPolicyAsset(issuer *actor) {
	t := r.t
	r.log("")
	r.log("== fees from a wallet with no policy asset: a bearer mint funded by seqpal-escrow holding only USDX ==")
	addr := r.str("seqpal-escrow", "getnewaddress", "fees", "bech32")
	r.str("miner", "sendtoaddress", addr, "50", "", "", false, false, 1, "UNSET", false, r.usdx, false, nil, r.usdx)
	r.mine(1)
	var policy string
	var labels map[string]string
	json.Unmarshal(r.rpc("", "dumpassetlabels"), &labels)
	policy = labels["bitcoin"]
	var bal map[string]map[string]map[string]json.Number
	json.Unmarshal(r.rpc("seqpal-escrow", "getbalances"), &bal)
	r.log("  seqpal-escrow holds %v (policy asset %s: %v)", bal["mine"]["trusted"], policy, bal["mine"]["trusted"][policy])
	if v, ok := bal["mine"]["trusted"][policy]; ok && v.String() != "0" && v.String() != "0.00000000" {
		t.Fatalf("seqpal-escrow holds the policy asset: %v", v)
	}
	out := r.must(issuer, "POST", "/api/issuances", map[string]any{
		"name": "ESCF Co", "ticker": "ESCF", "structure_id": "native-equity",
		"terms": map[string]any{"jurisdictions": map[string]any{"HN": map[string]any{"access": "standard"}}, "price": 1.0},
	})
	issID, _ := out["issuance"].(map[string]any)["id"].(string)
	first := r.must(issuer, "POST", "/api/issuances/"+issID+"/bearer-attestation", map[string]any{"no_us_nexus": true, "risk_accepted": true})
	signThis, _ := first["sign_this"].(string)
	digest := sha256.Sum256([]byte(signThis))
	sig, err := signTagged(issuer.priv, bearerAttestationTag, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	r.must(issuer, "POST", "/api/issuances/"+issID+"/bearer-attestation", map[string]any{"no_us_nexus": true, "risk_accepted": true, "statement_sig": sig})
	dep := r.must(issuer, "POST", "/api/deploy", map[string]any{"issuance_id": issID, "supply": 1000000, "precision": 2,
		"enforcement": "bearer", "recovery_pubkey": xonlyHex(t, genPriv(t))})
	txid, _ := dep["txid"].(string)
	if txid == "" {
		t.Fatalf("bearer deploy returned no txid: %v", dep)
	}
	r.mine(1)
	var tx struct {
		Vsize int64 `json:"vsize"`
		Vout  []struct {
			Value        json.Number `json:"value"`
			Asset        string      `json:"asset"`
			ScriptPubKey struct {
				Type string `json:"type"`
			} `json:"scriptPubKey"`
		} `json:"vout"`
		Confirmations int64 `json:"confirmations"`
	}
	json.Unmarshal(r.rpc("", "getrawtransaction", txid, true), &tx)
	fees := 0
	for _, o := range tx.Vout {
		if o.ScriptPubKey.Type == "fee" {
			fees++
			r.log("  bearer mint %s (%d vB, %d confirmation): fee output %s of asset %s", txid, tx.Vsize, tx.Confirmations, o.Value, o.Asset)
			if o.Asset != r.usdx {
				t.Fatalf("the mint paid its fee in %s, not USDX", o.Asset)
			}
		}
	}
	if fees != 1 || tx.Confirmations < 1 {
		t.Fatalf("mint %s: %d fee outputs, %d confirmations", txid, fees, tx.Confirmations)
	}
	r.log("  the mint's fee is paid in USDX, the only asset the paying wallet holds; nothing fell back to the policy asset")
}

// issuerPage runs the issuer's browser wallet on seqpald's own issuer page:
// SEQPAL_REGTEST_ISSUER_PAGE names the sequentia-extension drive
// test/regtest/seqpal-issuer.mjs, which runs the extension's provider router
// and its contract engine, opens the page in headless Chromium as the issuer
// (its session cookie), clicks through the page and the wallet's approval page,
// and prints what it did as JSON.
func (r *regtest) issuerPage(helper, mode string, issuer *actor, issID string, extra ...string) map[string]any {
	r.t.Helper()
	args := []string{helper, mode}
	if ev := os.Getenv("SEQPAL_REGTEST_PAGE_EVIDENCE"); ev != "" {
		args = append(args, ev)
	}
	cmd := exec.Command("node", args...)
	cmd.Env = append(os.Environ(), "SEQPAL_URL="+r.base, "SEQPAL_SESSION="+issuer.session, "SEQPAL_ISSUANCE="+issID,
		"NODE_RPC_URL="+r.url, "NODE_RPC_AUTH="+r.user+":"+r.pass)
	cmd.Env = append(cmd.Env, extra...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	for _, line := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
		if strings.HasPrefix(line, "[seqpal-issuer") {
			r.log("    %s", line)
		}
	}
	if err != nil {
		r.t.Fatalf("the issuer page (%s): %v\n%s", mode, err, stderr.String())
	}
	var res map[string]any
	if err := json.Unmarshal(out, &res); err != nil {
		r.t.Fatalf("the issuer page (%s) printed %q", mode, out)
	}
	return res
}

// offeringFromThePage is an offering whose escrow the issuer opens, and whose
// release the issuer signs, in a browser wallet on SeqPal's issuer page: the
// release key is the wallet's contract key, the terms are a statement signed
// with it, and the release is rebuilt, shown and signed by the wallet's
// contract engine, then verified and broadcast by seqpald.
func (r *regtest) offeringFromThePage(helper string, issuer *actor) {
	t := r.t
	r.log("")
	r.log("== offering 3: the escrow opened and released from the issuer page, by the browser wallet's contract engine ==")
	iss3, _ := r.deploy(issuer, "ESCE")
	r.sign2(issuer, "/api/issuances/"+iss3+"/mandate", map[string]any{"chain": "sequentia", "address": r.issuerPayout})
	close3 := r.height() + 12
	opened := r.issuerPage(helper, "open", issuer, iss3, "MINIMUM=250", fmt.Sprintf("CLOSE=%d", close3))
	st := r.must(issuer, "GET", "/api/issuances/"+iss3+"/escrow", nil)
	esc3, _ := st["escrow"].(map[string]any)
	if esc3 == nil || esc3["release_key"] != opened["release_key"] {
		t.Fatalf("the escrow the page opened: %v, the wallet's key %v", esc3, opened["release_key"])
	}
	r.log("  escrow opened from the page: release key %v (the browser wallet's contract key, proven by a tagged statement), minimum %d atoms, close %v",
		esc3["release_key"], jsonAtoms(esc3["minimum_atoms"]), esc3["close_text"])
	var subs3 []paidSub
	var invs3 []*actor
	for i, tokens := range []int{100, 150, 50} {
		inv := r.newActor(fmt.Sprintf("Subscriber %c3", 'A'+i))
		r.register(inv, "HN")
		r.verify(inv)
		p := r.subscribe(inv, iss3, tokens)
		subs3 = append(subs3, p)
		invs3 = append(invs3, inv)
	}
	r.mine(1)
	for i, p := range subs3 {
		r.waitSubState(invs3[i], p.id, "in_escrow")
	}
	r.mineTo(close3)
	pre := r.must(issuer, "POST", "/api/issuances/"+iss3+"/close", map[string]any{})
	sig := signCanonical(t, issuer.priv, pre["tag"].(string), pre["sign_this"].(string))
	closed := r.must(issuer, "POST", "/api/issuances/"+iss3+"/close", map[string]any{"signature": sig, "signer_xonly": issuer.xonly})
	spends, _ := closed["escrow_spends"].([]any)
	if len(spends) != 1 {
		t.Fatalf("close returned %d spends, want one release", len(spends))
	}
	rel := spends[0].(map[string]any)
	if rel["engine"] == nil {
		t.Fatalf("the release carries no engine request: %v", rel["engine_error"])
	}
	r.log("  seqpald delivered and built release %s over %d coins; the issuer signs it on the page", rel["txid"], len(rel["to_sign"].([]any)))
	signed := r.issuerPage(helper, "release", issuer, iss3)
	if signed["txid"] != rel["txid"] {
		t.Fatalf("the page broadcast %v, seqpald built %v", signed["txid"], rel["txid"])
	}
	relTxid := rel["txid"].(string)
	r.mine(1)
	r.logMined("release (signed by the browser wallet's contract engine)", relTxid)
	r.assertOutput(relTxid, 0, r.issuerPayoutSPK(), r.usdx, uint64(jsonAtoms(esc3["minimum_atoms"])), true)
	for i, p := range subs3 {
		r.waitSubState(invs3[i], p.id, "settled")
	}
	r.log("  all three settled with the release txid")
}

// --- helpers for the forced negatives ---------------------------------------------------

func (r *regtest) issuerPayoutSPK() string { return r.spkOf(r.issuerPayout) }

func (r *regtest) spkOf(addr string) string {
	var vi struct {
		ScriptPubKey string `json:"scriptPubKey"`
	}
	json.Unmarshal(r.rpc("", "validateaddress", addr), &vi)
	return vi.ScriptPubKey
}

func (r *regtest) feeCap(esc map[string]any) uint64 { return jsonAtoms(esc["fee_cap"]) }

func termsFromView(t *testing.T, v map[string]any, issID, issuerSPK string) *EscrowTerms {
	return &EscrowTerms{
		IssuanceID: issID, PayAsset: v["pay_asset"].(string), ReleaseKey: v["release_key"].(string),
		IssuerAddress: v["issuer_address"].(string), IssuerSPK: issuerSPK, MinimumAtoms: jsonAtoms(v["minimum_atoms"]),
		CloseLock: uint32(jsonAtoms(v["close"])), DeadlineLock: uint32(jsonAtoms(v["deadline"])), FeeCap: jsonAtoms(v["fee_cap"]),
	}
}

// buildReleaseLike builds, with seqpald's own builder, a release of the given
// subscriptions' coins at a chosen lock time, and has the issuer's wallet sign
// it: the transaction seqpald itself would hand the issuer, moved in time.
func (r *regtest) buildReleaseLike(terms *EscrowTerms, subs []paidSub, lock uint32) string {
	t := r.t
	tl := *terms
	tl.CloseLock = lock
	inst := map[string]*escrowInstance{}
	var coins []escrowCoin
	for _, p := range subs {
		// The subscription's refund script and reference are read back from the
		// subscriber's own escrow view, as anyone with it could.
		e := r.instanceOf(terms, p)
		inst[p.id] = e
		coins = append(coins, escrowCoin{Txid: p.txid, Vout: p.vout, Atoms: p.atoms, Asset: terms.PayAsset, SubscriptionID: p.id})
	}
	b, err := buildRelease(terms, coins, inst, 0, nil, "", 2000)
	if err != nil {
		t.Fatal(err)
	}
	b.Tx.LockTime = lock
	return r.signInWallet(b.Tx, b.Inputs)
}

func (r *regtest) instanceOf(terms *EscrowTerms, p paidSub) *escrowInstance {
	refundSPK := r.spkOf(p.refund)
	// The reference is the subscription id under its issuance: the same
	// function seqpald derives it with.
	ref := escrowSubscriptionRef(terms.IssuanceID, p.id)
	e, err := deriveEscrow(escrowParamsFor(terms, &Subscription{RefundSPK: refundSPK, EscrowRef: ref}), "elementsregtest")
	if err != nil {
		r.t.Fatal(err)
	}
	if hex.EncodeToString(e.SPK) != p.spk {
		r.t.Fatalf("re-derived %x, subscription pays %s", e.SPK, p.spk)
	}
	return e
}

func (r *regtest) signInWallet(tx *elements.Tx, ins []escrowInput) string {
	var g [32]byte
	var display string
	json.Unmarshal(r.rpc("", "getblockhash", 0), &display)
	b, _ := hex.DecodeString(display)
	for i := 0; i < 32; i++ {
		g[i] = b[31-i]
	}
	if err := escrowSighashes(tx, ins, g); err != nil {
		r.t.Fatal(err)
	}
	unsigned := hex.EncodeToString(tx.Serialize())
	toSign := []map[string]any{}
	for _, in := range ins {
		toSign = append(toSign, map[string]any{"vin": in.Vin, "sighash": in.Sighash, "xonlykey": in.XOnlyKey, "leaf": in.Script, "control": in.Control})
	}
	var out struct {
		Signatures json.RawMessage `json:"signatures"`
	}
	json.Unmarshal(r.rpc("issuer", "signopenamptransfer", unsigned, toSign), &out)
	sigs, err := parseSpendSigs(out.Signatures)
	if err != nil {
		r.t.Fatal(err)
	}
	full, err := assembleEscrowWitness(unsigned, ins, sigs, g)
	if err != nil {
		r.t.Fatal(err)
	}
	return hex.EncodeToString(full.Serialize())
}

// refundLike rewrites a keyless refund's lock time (it carries no signature,
// so nothing else needs to change).
func (r *regtest) refundLike(txHex string, lock uint32) string {
	raw, _ := hex.DecodeString(txHex)
	tx, err := elements.DeserializeTx(raw)
	if err != nil {
		r.t.Fatal(err)
	}
	tx.LockTime = lock
	return hex.EncodeToString(tx.Serialize())
}

// shortPayout re-signs the issuer's payout with one holder's output an atom
// short, in the issuer's own wallet.
func (r *regtest) shortPayout(signedHex string, out0 map[string]any) string {
	raw, _ := hex.DecodeString(signedHex)
	tx, err := elements.DeserializeTx(raw)
	if err != nil {
		r.t.Fatal(err)
	}
	want, _ := hex.DecodeString(out0["script_pubkey"].(string))
	for _, o := range tx.Out {
		if bytes.Equal(o.ScriptPubKey, want) {
			v, _ := elements.ExplicitValueAmount(o.Value)
			o.Value = elements.ExplicitValue(v - 1)
			break
		}
	}
	tx.InWit, tx.OutWit = nil, nil
	var s struct {
		Hex string `json:"hex"`
	}
	json.Unmarshal(r.rpc("issuer", "signrawtransactionwithwallet", hex.EncodeToString(tx.Serialize())), &s)
	return s.Hex
}

func (r *regtest) logMined(what, txid string) {
	var tx struct {
		Confirmations int64  `json:"confirmations"`
		Blockhash     string `json:"blockhash"`
		Vsize         int64  `json:"vsize"`
		Vin           []any  `json:"vin"`
		Vout          []any  `json:"vout"`
	}
	json.Unmarshal(r.rpc("", "getrawtransaction", txid, true), &tx)
	if tx.Confirmations < 1 {
		r.t.Fatalf("%s %s is not mined", what, txid)
	}
	r.log("  %s %s MINED in block %s (%d confirmation), %d inputs, %d outputs, %d vB",
		what, txid, tx.Blockhash, tx.Confirmations, len(tx.Vin), len(tx.Vout), tx.Vsize)
}

func (r *regtest) assertOutput(txid string, n int, spk, asset string, atLeast uint64, exactOrMore bool) {
	var tx struct {
		Vout []struct {
			Value json.Number `json:"value"`
			Asset string      `json:"asset"`
			SPK   struct {
				Hex string `json:"hex"`
			} `json:"scriptPubKey"`
		} `json:"vout"`
	}
	json.Unmarshal(r.rpc("", "getrawtransaction", txid, true), &tx)
	o := tx.Vout[n]
	v := atomsFromNumber(o.Value)
	if o.SPK.Hex != spk || o.Asset != asset || v < atLeast {
		r.t.Fatalf("%s output %d = %s %s %d, want %s %s >= %d", txid, n, o.SPK.Hex, o.Asset, v, spk, asset, atLeast)
	}
	r.log("    output %d pays %d atoms of %s to %s (at least %d required)", n, v, asset, spk, atLeast)
}

// assertHotWalletClean checks the platform wallet seqpal-escrow: no
// transaction of its history is a deposit or a spend of any escrow coin, and
// it holds no USDX.
func (r *regtest) assertHotWalletClean(when string, subs []paidSub) {
	var txs []struct {
		Txid     string `json:"txid"`
		Category string `json:"category"`
	}
	json.Unmarshal(r.rpc("seqpal-escrow", "listtransactions", "*", 10000, 0, true), &txs)
	deposits := map[string]bool{}
	for _, p := range subs {
		deposits[p.txid] = true
	}
	for _, tx := range txs {
		if deposits[tx.Txid] {
			r.t.Fatalf("seqpal-escrow's history holds the deposit %s", tx.Txid)
		}
	}
	// No output of any deposit is the platform wallet's.
	outputs := 0
	for _, p := range subs {
		var tx struct {
			Vout []struct {
				SPK struct {
					Address string `json:"address"`
				} `json:"scriptPubKey"`
			} `json:"vout"`
		}
		json.Unmarshal(r.rpc("", "getrawtransaction", p.txid, true), &tx)
		for _, o := range tx.Vout {
			if o.SPK.Address == "" {
				continue
			}
			var info struct {
				IsMine bool `json:"ismine"`
			}
			json.Unmarshal(r.rpc("seqpal-escrow", "getaddressinfo", o.SPK.Address), &info)
			if info.IsMine {
				r.t.Fatalf("deposit %s pays %s, an address of seqpal-escrow", p.txid, o.SPK.Address)
			}
			outputs++
		}
	}
	var bal map[string]any
	json.Unmarshal(r.rpc("seqpal-escrow", "getbalances"), &bal)
	mine, _ := bal["mine"].(map[string]any)
	trusted, _ := mine["trusted"].(map[string]any)
	if v, ok := trusted[r.usdx]; ok && fmt.Sprint(v) != "0" && fmt.Sprint(v) != "0.00000000" {
		r.t.Fatalf("seqpal-escrow holds USDX: %v", v)
	}
	usdx := trusted[r.usdx]
	if usdx == nil {
		usdx = 0
	}
	r.log("  seqpal-escrow %s: listtransactions holds %d entries and none of the %d deposits; none of the %d "+
		"addressed outputs of those deposits is its (getaddressinfo ismine=false); USDX balance %v",
		when, len(txs), len(subs), outputs, usdx)
}
