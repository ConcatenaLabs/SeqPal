package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
			if d, _ := p[0].(string); strings.HasPrefix(d, "wpkh(") {
				reply([]string{"ert1qnzten2u3ayqmnqtdul7z00v3uvapet7dv2789z"})
			} else {
				reply([]string{"2ds6y7euxH5WNMGRzTCxUDtYdd8EaCSAqD2"})
			}
		case "verifymessage":
			reply(signedOK)
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
