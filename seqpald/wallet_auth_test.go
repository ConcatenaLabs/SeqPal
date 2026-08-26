package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A node stub answering only the three pure RPCs a wallet sign-in needs. The
// values are the real ones a Sequentia node returns for this descriptor, taken
// from a running node, so the shapes are not invented here.
func newWalletNode(t *testing.T, signedOK bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		reply := func(v any) {
			_ = json.NewEncoder(w).Encode(map[string]any{"result": v, "error": nil, "id": "seqpald"})
		}
		switch req.Method {
		case "getdescriptorinfo":
			var p []string
			_ = json.Unmarshal(req.Params, &p)
			d := p[0]
			if i := strings.Index(d, "#"); i >= 0 {
				d = d[:i]
			}
			reply(map[string]any{"descriptor": d, "checksum": "0wcatm2p", "hasprivatekeys": false})
		case "deriveaddresses":
			// The two forms of one key give two different addresses, which is the
			// whole point: the holder sees the wpkh one, the node verifies the
			// pkh one.
			var p []any
			_ = json.Unmarshal(req.Params, &p)
			// The node refuses a descriptor with no checksum, and answering one
			// anyway hid a bug where three call sites derived from text the node
			// would never have accepted.
			if d, _ := p[0].(string); !strings.Contains(d, "#") {
				_ = json.NewEncoder(w).Encode(map[string]any{"result": nil,
					"error": map[string]any{"code": -5, "message": "Missing checksum"}})
				return
			}
			// The node refuses a range for a descriptor that makes one address,
			// and refuses to derive without one for a descriptor that makes a
			// chain. Answering both hid a whole class of derivation that could
			// never have worked.
			d, _ := p[0].(string)
			ranged := strings.Contains(d, "/*")
			if ranged != (len(p) > 1) {
				msg := "Range should not be specified for an un-ranged descriptor"
				if ranged {
					msg = "Range must be specified for a ranged descriptor"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"result": nil,
					"error": map[string]any{"code": -8, "message": msg}})
				return
			}
			if strings.HasPrefix(d, "wpkh(") {
				reply([]string{"ert1qnzten2u3ayqmnqtdul7z00v3uvapet7dv2789z"})
			} else {
				reply([]string{"2ds6y7euxH5WNMGRzTCxUDtYdd8EaCSAqD2"})
			}
		case "verifymessage":
			reply(signedOK)
		case "validateaddress":
			var p []string
			_ = json.Unmarshal(req.Params, &p)
			reply(map[string]any{"isvalid": len(p) > 0 && p[0] != ""})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"result": nil,
				"error": map[string]any{"code": -32601, "message": "no such method: " + req.Method}})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

const testPKH = "pkh([78a58319/44'/1'/0']tpubDCTudosJmS58rksmdnazbWxbQyCAcxncXqT9cQy5rpg94dyseRE5oNF99AhMxgn1bLxU94UeSxfUj6M2WwPRnxHjHkPaqoTXWkfigM2vcd1/0/*)"

// A wallet with no enclave key gets a SeqPal ID, and it is the same ID next time.
func TestWalletSignUpAndSignIn(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL

	ch := h.do("POST", "/api/auth/wallet/challenge", "", map[string]any{"descriptor": testPKH})
	if ch.code != 200 {
		t.Fatalf("challenge: %d %s", ch.code, ch.raw)
	}
	if ch.body["address"] != "2ds6y7euxH5WNMGRzTCxUDtYdd8EaCSAqD2" {
		t.Fatalf("the address to sign with must come from the descriptor, got %v", ch.body["address"])
	}
	if ch.body["registered"] != false {
		t.Fatal("an unknown wallet must report itself as not yet registered")
	}
	accountID, _ := ch.body["account_id"].(string)
	if len(accountID) != 40 {
		t.Fatalf("account id: %v", ch.body["account_id"])
	}

	reg := h.do("POST", "/api/auth/wallet/register", "", map[string]any{
		"descriptor": testPKH, "challenge": ch.body["challenge"], "sig": "ILLUSTRATIVE",
		"display_name": "Wallet Wendy", "residence": "AE",
	})
	if reg.code != 200 {
		t.Fatalf("register: %d %s", reg.code, reg.raw)
	}
	acct, _ := reg.body["account"].(map[string]any)
	if acct["identity"] != "xpub" {
		t.Fatalf("identity must be xpub, got %v", acct["identity"])
	}
	if acct["xonly"] != nil && acct["xonly"] != "" {
		t.Fatalf("a wallet account must report no enclave key, got %v", acct["xonly"])
	}
	if acct["aid"] != accountID {
		t.Fatal("the account id must be the one the challenge named")
	}

	// Signing in again finds the same account rather than making a second one.
	ch2 := h.do("POST", "/api/auth/wallet/challenge", "", map[string]any{"descriptor": testPKH})
	if ch2.body["registered"] != true {
		t.Fatal("a known wallet must report itself as registered")
	}
	in := h.do("POST", "/api/auth/wallet/login", "", map[string]any{
		"descriptor": testPKH, "challenge": ch2.body["challenge"], "sig": "ILLUSTRATIVE",
	})
	if in.code != 200 {
		t.Fatalf("login: %d %s", in.code, in.raw)
	}
	if a, _ := in.body["account"].(map[string]any); a["aid"] != accountID {
		t.Fatal("signing in again must land in the same account")
	}
}

// Two different wallets must both be able to register: accounts.xonly is UNIQUE
// and neither has an enclave key to put in it.
func TestTwoWalletAccountsCanCoexist(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	second := strings.Replace(testPKH, "78a58319", "99a58319", 1)

	for _, desc := range []string{testPKH, second} {
		ch := h.do("POST", "/api/auth/wallet/challenge", "", map[string]any{"descriptor": desc})
		if ch.code != 200 {
			t.Fatalf("challenge for %.20s: %d %s", desc, ch.code, ch.raw)
		}
		reg := h.do("POST", "/api/auth/wallet/register", "", map[string]any{
			"descriptor": desc, "challenge": ch.body["challenge"], "sig": "X", "display_name": "W",
		})
		if reg.code != 200 {
			t.Fatalf("second wallet account must register too, got %d %s", reg.code, reg.raw)
		}
	}
}

// A signature that does not verify is not a sign-in.
func TestWalletSignInRefusesABadSignature(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, false).URL
	ch := h.do("POST", "/api/auth/wallet/challenge", "", map[string]any{"descriptor": testPKH})
	reg := h.do("POST", "/api/auth/wallet/register", "", map[string]any{
		"descriptor": testPKH, "challenge": ch.body["challenge"], "sig": "wrong", "display_name": "W",
	})
	if reg.code != 401 {
		t.Fatalf("a bad signature must be refused with 401, got %d %s", reg.code, reg.raw)
	}
}

// The boundary this whole account kind exists to draw.
func TestWalletAccountIsRefusedOpenAMPButNotTheRest(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	ch := h.do("POST", "/api/auth/wallet/challenge", "", map[string]any{"descriptor": testPKH})
	reg := h.do("POST", "/api/auth/wallet/register", "", map[string]any{
		"descriptor": testPKH, "challenge": ch.body["challenge"], "sig": "X", "display_name": "W",
	})
	if reg.code != 200 {
		t.Fatalf("register: %d %s", reg.code, reg.raw)
	}
	var session string
	for _, c := range reg.set {
		if c.Name == sessionCookie {
			session = c.Value
		}
	}
	if session == "" {
		t.Fatal("no session cookie")
	}

	// An enclave transfer is out of reach, and says why.
	tr := h.do("POST", "/api/transfers", session, map[string]any{"asset": strings.Repeat("a", 64), "to_aid": strings.Repeat("b", 40), "atoms": 1})
	if tr.code != 403 {
		t.Fatalf("an enclave transfer must be refused with 403, got %d %s", tr.code, tr.raw)
	}
	if msg, _ := tr.body["error"].(string); !strings.Contains(msg, "no OpenAMP account attached") {
		t.Fatalf("the refusal must say what is missing, got %q", msg)
	}

	// The identity surfaces are not behind that gate.
	if me := h.do("GET", "/api/me", session, nil); me.code != 200 {
		t.Fatalf("/api/me must work for a wallet account, got %d %s", me.code, me.raw)
	}
	if ent := h.do("POST", "/api/entities", session, map[string]any{"name": "Acme", "jurisdiction": "PZ"}); ent.code >= 500 || ent.code == 403 {
		t.Fatalf("creating an entity must not be behind the enclave gate, got %d %s", ent.code, ent.raw)
	}
}

// The bug this fixes: SeqPal told a holder to sign with a legacy m-prefixed
// address their wallet never shows and cannot produce a receive address for.
// The address to sign with must be the one their own wallet displays; the
// legacy form is SeqPal's business, not theirs.
func TestWalletChallengeNamesTheAddressTheWalletShows(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	wpkh := strings.Replace(testPKH, "pkh(", "wpkh(", 1)

	ch := h.do("POST", "/api/auth/wallet/challenge", "", map[string]any{"descriptor": wpkh})
	if ch.code != 200 {
		t.Fatalf("a wpkh descriptor must be accepted: %d %s", ch.code, ch.raw)
	}
	if got, _ := ch.body["address"].(string); got != "ert1qnzten2u3ayqmnqtdul7z00v3uvapet7dv2789z" {
		t.Fatalf("the address shown must be the wallet's own form, got %q", got)
	}
	if ch.body["index"] == nil {
		t.Fatal("the address index must be stated: a Sign tab asks for it")
	}
}

// One wallet is one SeqPal ID, whichever form of its descriptor gets pasted.
func TestBothDescriptorFormsAreTheSameAccount(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	wpkh := strings.Replace(testPKH, "pkh(", "wpkh(", 1)

	a := h.do("POST", "/api/auth/wallet/challenge", "", map[string]any{"descriptor": testPKH})
	b := h.do("POST", "/api/auth/wallet/challenge", "", map[string]any{"descriptor": wpkh})
	if a.code != 200 || b.code != 200 {
		t.Fatalf("both forms must be accepted: %d / %d", a.code, b.code)
	}
	if a.body["account_id"] != b.body["account_id"] {
		t.Fatalf("pkh and wpkh of one key must be ONE account: %v vs %v",
			a.body["account_id"], b.body["account_id"])
	}
}

// A flow that sends someone to another application and back must survive a bad
// paste. Burning the challenge on a signature that did not verify makes one
// mistake cost the whole exchange, which is what happened live.
func TestABadSignatureDoesNotBurnTheChallenge(t *testing.T) {
	h := newHarness(t)
	bad := newWalletNode(t, false)
	h.s.cfg.nodeURL = bad.URL

	ch := h.do("POST", "/api/auth/wallet/challenge", "", map[string]any{"descriptor": testPKH})
	if ch.code != 200 {
		t.Fatalf("challenge: %d %s", ch.code, ch.raw)
	}
	first := h.do("POST", "/api/auth/wallet/register", "", map[string]any{
		"descriptor": testPKH, "challenge": ch.body["challenge"], "sig": "wrong", "display_name": "W",
	})
	if first.code != 401 {
		t.Fatalf("a bad signature must be refused, got %d %s", first.code, first.raw)
	}
	if msg, _ := first.body["error"].(string); strings.Contains(msg, "already used") {
		t.Fatalf("the challenge must not be spent by a failed attempt, got %q", msg)
	}

	// Same challenge, this time with a signature that verifies.
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	second := h.do("POST", "/api/auth/wallet/register", "", map[string]any{
		"descriptor": testPKH, "challenge": ch.body["challenge"], "sig": "right", "display_name": "W",
	})
	if second.code != 200 {
		t.Fatalf("retrying the same challenge with a good signature must work, got %d %s",
			second.code, second.raw)
	}
}

// Once it has worked, it is spent: the same signature must not be replayable.
func TestAVerifiedChallengeIsSpent(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	ch := h.do("POST", "/api/auth/wallet/challenge", "", map[string]any{"descriptor": testPKH})
	body := map[string]any{
		"descriptor": testPKH, "challenge": ch.body["challenge"], "sig": "right", "display_name": "W",
	}
	if first := h.do("POST", "/api/auth/wallet/register", "", body); first.code != 200 {
		t.Fatalf("register: %d %s", first.code, first.raw)
	}
	replay := h.do("POST", "/api/auth/wallet/login", "", body)
	if replay.code == 200 {
		t.Fatal("a spent challenge must not be replayable")
	}
}

// The window has to cover leaving the page, finding a signing screen, signing
// and coming back. Two minutes did not.
func TestWalletChallengeWindowIsLongEnoughToLeaveThePage(t *testing.T) {
	if walletChallengeTTL <= challengeTTL {
		t.Fatalf("the wallet window (%s) must be longer than the enclave one (%s): one is a click, "+
			"the other is a trip to another application", walletChallengeTTL, challengeTTL)
	}
	if walletChallengeTTL < 10*time.Minute {
		t.Fatalf("%s is not long enough for an out-of-band signature", walletChallengeTTL)
	}
}

// Verifying an identity must not require an enclave account. This failed live
// with "register with the policy server: bad pubkey", because a wallet-backed
// ID has no key to register -- and what it is verified FOR needs none.
func TestVerifyWorksWithoutAnEnclave(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	ch := h.do("POST", "/api/auth/wallet/challenge", "", map[string]any{"descriptor": testPKH})
	reg := h.do("POST", "/api/auth/wallet/register", "", map[string]any{
		"descriptor": testPKH, "challenge": ch.body["challenge"], "sig": "X",
		"display_name": "Wallet Wendy", "residence": "AE",
	})
	if reg.code != 200 {
		t.Fatalf("register: %d %s", reg.code, reg.raw)
	}
	var session string
	for _, c := range reg.set {
		if c.Name == sessionCookie {
			session = c.Value
		}
	}

	v := h.do("POST", "/api/id/verify", session, map[string]any{
		"residence": "AE", "screening_name": "Wallet Wendy", "base_eligibility": "ret",
	})
	if v.code != 200 {
		t.Fatalf("verifying a wallet-backed ID must work, got %d %s", v.code, v.raw)
	}
	// Submitted, not decided: an ID with no OpenAMP account goes to the provider
	// like any other, and is granted nothing until they answer.
	if v.body["status"] != "submitted" {
		t.Fatalf("status: %v", v.body["status"])
	}
	if v.body["aid"] != reg.body["account"].(map[string]any)["aid"] {
		t.Fatal("the id in the verification result must be this account's own")
	}
	aid := reg.body["account"].(map[string]any)["aid"].(string)

	// And once the provider clears it, the categories are projected and carried
	// even though this ID has no OpenAMP account to stamp them on.
	h.adjudicate(aid, idvClear)
	p := h.do("GET", "/api/id/passport", session, nil)
	cats, _ := p.body["categories"].([]any)
	if len(cats) == 0 {
		t.Fatalf("categories must still be projected, even with nowhere to stamp them: %s", p.raw)
	}
}

// A verified wallet-backed ID must not read as "Verified" with nothing to show
// for it, and its own account id must not be presented as an enclave account.
func TestPassportOfAWalletBackedID(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	ch := h.do("POST", "/api/auth/wallet/challenge", "", map[string]any{"descriptor": testPKH})
	reg := h.do("POST", "/api/auth/wallet/register", "", map[string]any{
		"descriptor": testPKH, "challenge": ch.body["challenge"], "sig": "X",
		"display_name": "Wallet Wendy", "residence": "AE",
	})
	var session string
	for _, c := range reg.set {
		if c.Name == sessionCookie {
			session = c.Value
		}
	}
	h.verifyIdentity(session, reg.body["account"].(map[string]any)["aid"].(string), map[string]any{
		"residence": "AE", "screening_name": "Wallet Wendy", "base_eligibility": "ret",
	})

	p := h.do("GET", "/api/id/passport", session, nil)
	if p.code != 200 {
		t.Fatalf("passport: %d %s", p.code, p.raw)
	}
	if p.body["has_enclave"] != false {
		t.Fatalf("the passport must say there is no enclave account, got %v", p.body["has_enclave"])
	}
	if k, _ := p.body["enclave_key"].(string); k != "" {
		t.Fatalf("there is no enclave key to report, got %q", k)
	}
	// The point of the fix: verified, with the categories that verification
	// projected, rather than zero because it read a policy server that has never
	// heard of this account.
	cats, _ := p.body["categories"].([]any)
	if len(cats) == 0 {
		t.Fatal("a verified wallet-backed ID must carry the categories it was granted")
	}
	if p.body["status"] != "verified" {
		t.Fatalf("status: %v", p.body["status"])
	}
}

// A node that signs in fine but never recognises a holding key as the wallet's
// own, and refuses the whitelist statement: precisely the case where a holder
// must prove the key with a signature, and fails to.
func newWalletNodeNoMatch(t *testing.T) *httptest.Server {
	t.Helper()
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		reply := func(v any) {
			_ = json.NewEncoder(w).Encode(map[string]any{"result": v, "error": nil, "id": "seqpald"})
		}
		switch req.Method {
		case "getdescriptorinfo":
			var p []string
			_ = json.Unmarshal(req.Params, &p)
			d := p[0]
			if i := strings.Index(d, "#"); i >= 0 {
				d = d[:i]
			}
			reply(map[string]any{"descriptor": d, "checksum": "0wcatm2p", "hasprivatekeys": false})
		case "deriveaddresses":
			// A different address every time, so no key is ever recognised as one
			// this wallet derives.
			n++
			reply([]string{fmt.Sprintf("2ds6y7euxH5WNMGRzTCxUDtYdd8EaCSAq%02d", n%100)})
		case "verifymessage":
			// Sign-in verifies; the whitelist statement does not.
			var p []string
			_ = json.Unmarshal(req.Params, &p)
			msg := ""
			if len(p) > 2 {
				msg = p[2]
			}
			reply(!strings.Contains(msg, "whitelist-request"))
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"result": nil,
				"error": map[string]any{"code": -32601, "message": "no such method"}})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}
