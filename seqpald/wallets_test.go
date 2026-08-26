package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func walletSession(t *testing.T, h *harness, desc string) (string, string) {
	t.Helper()
	ch := h.do("POST", "/api/auth/wallet/challenge", "", map[string]any{"descriptor": desc})
	if ch.code != 200 {
		t.Fatalf("challenge: %d %s", ch.code, ch.raw)
	}
	reg := h.do("POST", "/api/auth/wallet/register", "", map[string]any{
		"descriptor": desc, "challenge": ch.body["challenge"], "sig": "X",
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
	if session == "" {
		t.Fatal("no session cookie")
	}
	return session, reg.body["account"].(map[string]any)["aid"].(string)
}

// The wallet an ID was founded with is one of its wallets, not a special case
// living outside the list.
func TestFoundingWalletIsListed(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	session, _ := walletSession(t, h, testPKH)

	ws := h.do("GET", "/api/account/wallets", session, nil)
	if ws.code != 200 {
		t.Fatalf("wallets: %d %s", ws.code, ws.raw)
	}
	list, _ := ws.body["wallets"].([]any)
	if len(list) != 1 {
		t.Fatalf("a new ID must list the wallet it was made with, got %d", len(list))
	}
	if ws.body["has_enclave"] != false {
		t.Fatal("a descriptor wallet is not an OpenAMP account")
	}
}

// The point of the whole thing: one identity, more than one wallet.
func TestLinkingASecondDescriptorWallet(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	session, aid := walletSession(t, h, testPKH)
	second := strings.Replace(testPKH, "78a58319", "99a58319", 1)

	// Step one asks for a challenge rather than linking anything.
	ask := h.do("POST", "/api/account/wallets", session, map[string]any{"descriptor": second})
	if ask.code != 200 || ask.body["challenge"] == nil {
		t.Fatalf("linking must start with a challenge: %d %s", ask.code, ask.raw)
	}
	if ask.body["address"] == nil {
		t.Fatal("the holder needs to be told which address to sign with")
	}

	done := h.do("POST", "/api/account/wallets", session, map[string]any{
		"descriptor": second, "challenge": ask.body["challenge"], "sig": "X", "label": "Extension",
	})
	if done.code != 200 || done.body["wallet"] == nil {
		t.Fatalf("link: %d %s", done.code, done.raw)
	}

	ws := h.do("GET", "/api/account/wallets", session, nil)
	if list, _ := ws.body["wallets"].([]any); len(list) != 2 {
		t.Fatalf("the ID must now list two wallets, got %d", len(list))
	}

	// And signing in with the SECOND wallet lands in the SAME identity, which is
	// the whole reason for linking it.
	ch := h.do("POST", "/api/auth/wallet/challenge", "", map[string]any{"descriptor": second})
	in := h.do("POST", "/api/auth/wallet/login", "", map[string]any{
		"descriptor": second, "challenge": ch.body["challenge"], "sig": "X",
	})
	if in.code != 200 {
		t.Fatalf("sign-in with a linked wallet: %d %s", in.code, in.raw)
	}
	if got := in.body["account"].(map[string]any)["aid"]; got != aid {
		t.Fatalf("a linked wallet must sign into the same ID: got %v, want %v", got, aid)
	}
}

// A wallet belongs to one identity. Two would make eligibility ambiguous.
func TestAWalletCannotBeLinkedToTwoIdentities(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	sessionA, _ := walletSession(t, h, testPKH)
	second := strings.Replace(testPKH, "78a58319", "99a58319", 1)
	ask := h.do("POST", "/api/account/wallets", sessionA, map[string]any{"descriptor": second})
	if link := h.do("POST", "/api/account/wallets", sessionA, map[string]any{
		"descriptor": second, "challenge": ask.body["challenge"], "sig": "X",
	}); link.code != 200 {
		t.Fatalf("link: %d %s", link.code, link.raw)
	}

	third := strings.Replace(testPKH, "78a58319", "aaa58319", 1)
	sessionB, _ := walletSession(t, h, third)
	ask2 := h.do("POST", "/api/account/wallets", sessionB, map[string]any{"descriptor": second})
	clash := h.do("POST", "/api/account/wallets", sessionB, map[string]any{
		"descriptor": second, "challenge": ask2.body["challenge"], "sig": "X",
	})
	if clash.code != 409 {
		t.Fatalf("a wallet already linked elsewhere must be refused with 409, got %d %s",
			clash.code, clash.raw)
	}
}

// The only wallet cannot be unlinked: that would leave no way back in.
func TestTheLastWalletCannotBeUnlinked(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	session, _ := walletSession(t, h, testPKH)
	ws := h.do("GET", "/api/account/wallets", session, nil)
	first := ws.body["wallets"].([]any)[0].(map[string]any)["id"].(string)

	del := h.do("DELETE", "/api/account/wallets/"+first, session, nil)
	if del.code != 409 {
		t.Fatalf("unlinking the only wallet must be refused, got %d %s", del.code, del.raw)
	}
	if msg, _ := del.body["error"].(string); !strings.Contains(msg, "only wallet") {
		t.Fatalf("the refusal must say why: %q", msg)
	}
}

// A wallet is one wallet whichever form of its descriptor is presented. Linking
// it as wpkh and then signing in with its pkh form must land in the SAME ID; if
// the lookup misses, registration happily makes a second identity for one
// wallet, which is the thing linking exists to prevent.
func TestALinkedWalletResolvesByEitherForm(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	session, aid := walletSession(t, h, testPKH)

	secondPKH := strings.Replace(testPKH, "78a58319", "99a58319", 1)
	secondWPKH := strings.Replace(secondPKH, "pkh(", "wpkh(", 1)

	ask := h.do("POST", "/api/account/wallets", session, map[string]any{"descriptor": secondWPKH})
	if link := h.do("POST", "/api/account/wallets", session, map[string]any{
		"descriptor": secondWPKH, "challenge": ask.body["challenge"], "sig": "X",
	}); link.code != 200 {
		t.Fatalf("link as wpkh: %d %s", link.code, link.raw)
	}

	// Now sign in with the OTHER form of the same wallet.
	ch := h.do("POST", "/api/auth/wallet/challenge", "", map[string]any{"descriptor": secondPKH})
	if ch.body["registered"] != true {
		t.Fatalf("a linked wallet must be recognised in either form, got registered=%v", ch.body["registered"])
	}
	in := h.do("POST", "/api/auth/wallet/login", "", map[string]any{
		"descriptor": secondPKH, "challenge": ch.body["challenge"], "sig": "X",
	})
	if in.code != 200 {
		t.Fatalf("sign-in with the other form: %d %s", in.code, in.raw)
	}
	if got := in.body["account"].(map[string]any)["aid"]; got != aid {
		t.Fatalf("one wallet is one identity: got %v, want %v", got, aid)
	}
}

// The backfill and the Go path must produce the SAME key, or a wallet written
// by one is invisible to the other: the lookup misses, the wallet reports as
// unregistered, and registering it makes a second identity. The first version of
// that migration carried the checksum across from the form it converted, which
// belongs to different text, and did exactly this on a live database.
func TestMigratedKeysMatchComputedKeys(t *testing.T) {
	st, err := openStore(filepath.Join(t.TempDir(), "seqpald.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	// Rows written the way the pre-fix migration wrote them.
	for i, key := range []string{
		"pkh([82d3456e/84'/1'/0']tpubABC/0/*)#knvvevmh", // checksum carried across
		"pkh([82d3456e/84'/1'/0']tpubDEF/0/*)",          // already clean
	} {
		if _, err := st.db.Exec(
			`INSERT INTO account_wallets (id, aid, kind, descriptor, descriptor_key, xonly,
			  enclave_aid, label, proof, created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			"w"+string(rune('a'+i)), "acct", "descriptor",
			"wpkh([82d3456e/84'/1'/0']tpub/0/*)#zzzzzzzz", key, "", "", "", "migrated", 1); err != nil {
			t.Fatal(err)
		}
	}
	// Re-running the repair is what a deployment does on the next start.
	if _, err := st.db.Exec(
		`UPDATE account_wallets SET descriptor_key = substr(descriptor_key, 1, instr(descriptor_key, '#') - 1)
		  WHERE descriptor_key LIKE '%#%'`); err != nil {
		t.Fatal(err)
	}
	rows, err := st.db.Query(`SELECT descriptor_key FROM account_wallets`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(k, "#") {
			t.Fatalf("a stored key must carry no checksum, got %q", k)
		}
	}

	// And what the Go path computes carries none either, for either form.
	for _, d := range []string{
		"wpkh([82d3456e/84'/1'/0']tpubABC/0/*)#aaaaaaaa",
		"pkh([82d3456e/84'/1'/0']tpubABC/0/*)#bbbbbbbb",
	} {
		got := descriptorKeyOf(&AccountWallet{Kind: "descriptor", Descriptor: d})
		if strings.Contains(got, "#") {
			t.Fatalf("computed key must carry no checksum, got %q", got)
		}
	}
	// The two forms of one wallet compute to one key.
	a := descriptorKeyOf(&AccountWallet{Kind: "descriptor", Descriptor: "wpkh([aa/84h]tpubX/0/*)#aaaaaaaa"})
	b := descriptorKeyOf(&AccountWallet{Kind: "descriptor", Descriptor: "pkh([aa/84h]tpubX/0/*)#bbbbbbbb"})
	if a != b {
		t.Fatalf("one wallet, one key:\n  %s\n  %s", a, b)
	}
}
