package main

import (
	"encoding/json"
	"os"
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

// One enclave per SeqPal ID has to hold even when two requests arrive at once.
// The handler checks before inserting, and between those two statements a
// second request passes the same check: two enclave accounts on one ID, and no
// answer to which one its restricted assets settle in.
func TestOneEnclavePerAccountIsEnforcedInTheSchema(t *testing.T) {
	st, err := openStore(filepath.Join(t.TempDir(), "seqpald.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.CreateAccount(&Account{
		AID: "acct1", Kind: "individual", XOnly: strings.Repeat("a", 64),
		DisplayName: "A", CreatedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}

	first := &AccountWallet{ID: "w1", AID: "acct1", Kind: "enclave",
		XOnly: strings.Repeat("a", 64), EnclaveAID: "e1"}
	if err := st.InsertAccountWallet(first); err != nil {
		t.Fatalf("the first enclave must link: %v", err)
	}
	second := &AccountWallet{ID: "w2", AID: "acct1", Kind: "enclave",
		XOnly: strings.Repeat("b", 64), EnclaveAID: "e2"}
	if err := st.InsertAccountWallet(second); err == nil {
		t.Fatal("a second enclave on one ID must be refused by the database, not just by a check")
	}

	// Descriptor wallets are not limited: they are places a holder keeps keys.
	for i, d := range []string{"pkh(A/0/*)", "pkh(B/0/*)", "pkh(C/0/*)"} {
		if err := st.InsertAccountWallet(&AccountWallet{
			ID: "d" + string(rune('a'+i)), AID: "acct1", Kind: "descriptor", Descriptor: d,
		}); err != nil {
			t.Fatalf("descriptor wallet %d must link: %v", i, err)
		}
	}
}

// An ID founded as a wallet keeps its SeqPal account id forever, and the policy
// server knows it by the account its enclave key derives -- a different id. Every
// question asked of openampd about "this holder" has to name the second one.
//
// Confirmed against the live service before this was written: a verified holder
// attached an OpenAMP account and their passport went to zero categories,
// because the passport asked openampd about an account it had never heard of.
func TestTheTwoAccountIdsOfAnAttachedWallet(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	h.s.screen = newScreener("")
	session, aid := walletSession(t, h, testPKH)
	if v := h.do("POST", "/api/id/verify", session, map[string]any{
		"residence": "AE", "screening_name": "Wallet Wendy", "base_eligibility": "ret",
	}); v.code != 200 {
		t.Fatalf("verify: %d %s", v.code, v.raw)
	}
	before := h.do("GET", "/api/id/passport", session, nil)
	beforeCats, _ := before.body["categories"].([]any)
	if len(beforeCats) == 0 {
		t.Fatalf("a verified wallet-backed ID must carry categories: %s", before.raw)
	}

	// Attach an OpenAMP account, exactly as the browser does.
	xonly := xonlyHex(t, vecPriv)
	enclaveAID := aidFor([]string{xonly})
	if enclaveAID == aid {
		t.Fatal("this test needs the two ids to differ")
	}
	ch := h.do("POST", "/api/auth/challenge", "", map[string]any{"xonly": xonly})
	if ch.code != 200 {
		t.Fatalf("challenge: %d %s", ch.code, ch.raw)
	}
	challenge, _ := ch.body["challenge"].(string)
	if challenge == "" {
		t.Fatalf("no challenge in %s", ch.raw)
	}
	att := h.do("POST", "/api/auth/attach-enclave", session, map[string]any{
		"xonly": xonly, "challenge": challenge,
		"sig": signChallengeHex(t, vecPriv, challenge),
	})
	if att.code != 200 {
		t.Fatalf("attach: %d %s", att.code, att.raw)
	}

	if got := h.s.enclaveAIDFor(aid); got != enclaveAID {
		t.Fatalf("the policy-server id for this ID is %q, want %q", got, enclaveAID)
	}

	// The credential the holder already had must still be there afterwards.
	after := h.do("GET", "/api/id/passport", session, nil)
	afterCats, _ := after.body["categories"].([]any)
	if len(afterCats) != len(beforeCats) {
		t.Fatalf("attaching an OpenAMP account lost the categories: %d -> %d\n%s",
			len(beforeCats), len(afterCats), after.raw)
	}
}

// Delivery at closing names the recipient to the policy server, so it has to
// name the account the policy server holds. A subscription is filed under the
// SeqPal account id, which for an ID founded as a wallet is a different string
// -- and delivering to an account openampd has never heard of is not a thing to
// discover at closing, with the investor's money already in escrow.
func TestDeliveryNamesTheAccountThePolicyServerHolds(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	h.s.screen = newScreener("")
	_, aid := walletSession(t, h, testPKH)

	// No OpenAMP account: there is nothing to deliver to, and the resolver says
	// so rather than handing back an id that means nothing there.
	if got := h.s.openampAIDFor(aid); got != "" {
		t.Fatalf("an ID with no OpenAMP account resolves to %q, want empty", got)
	}

	// An id that is not a SeqPal account at all -- an escrow enclave, a
	// counterparty, a venue quoting one -- is already an openampd id.
	other := strings.Repeat("9", 40)
	if got := h.s.openampAIDFor(other); got != other {
		t.Fatalf("a foreign id was rewritten to %q", got)
	}
}

// The passport carries both ids, because they are the same string only for an
// ID founded on an OpenAMP account. Labelling the SeqPal id as the AID sends a
// holder to a venue quoting an account the policy server does not have.
func TestThePassportCarriesBothIds(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	h.s.screen = newScreener("")
	session, aid := walletSession(t, h, testPKH)

	p := h.do("GET", "/api/id/passport", session, nil)
	if p.code != 200 {
		t.Fatalf("passport: %d %s", p.code, p.raw)
	}
	if got, _ := p.body["aid"].(string); got != aid {
		t.Fatalf("the SeqPal account id is %q, want %q", got, aid)
	}
	if got, _ := p.body["enclave_aid"].(string); got != "" {
		t.Fatalf("an ID with no OpenAMP account reports one: %q", got)
	}

	// Attach one: the second id appears, and it is NOT the first.
	xonly := xonlyHex(t, vecPriv)
	ch := h.do("POST", "/api/auth/challenge", "", map[string]any{"xonly": xonly})
	challenge, _ := ch.body["challenge"].(string)
	if att := h.do("POST", "/api/auth/attach-enclave", session, map[string]any{
		"xonly": xonly, "challenge": challenge, "sig": signChallengeHex(t, vecPriv, challenge),
	}); att.code != 200 {
		t.Fatalf("attach: %d %s", att.code, att.raw)
	}
	p = h.do("GET", "/api/id/passport", session, nil)
	enclaveAID, _ := p.body["enclave_aid"].(string)
	if enclaveAID == "" || enclaveAID == aid {
		t.Fatalf("after attaching, the OpenAMP account id is %q (SeqPal id %q)", enclaveAID, aid)
	}
	if enclaveAID != aidFor([]string{xonly}) {
		t.Fatalf("the OpenAMP account id is not the one this key derives: %q", enclaveAID)
	}
}

// Every account id this platform hands to the POLICY SERVER has to be the one
// the policy server holds. This reads each callOpenAMP invocation and the lines
// that build its body, and fails on a SeqPal account id appearing there -- which
// is the failure this resolver exists for, and a silent one: openampd answers
// about an account it does not have, and the answer is what a holder with
// nothing looks like.
//
// It sees payloads built beside their call. A payload assembled somewhere else
// and passed in (the asset rules are one) is not visible to it, so this is a
// guard against the common shape rather than a proof.
func TestNoPayloadNamesASeqPalIdToThePolicyServer(t *testing.T) {
	seqpalIDs := []string{"acct.AID", "toAID", "sub.InvestorAID", "investorAID", "holderAID"}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(src), "\n")
		for i, ln := range lines {
			if !strings.Contains(ln, "callOpenAMP") {
				continue
			}
			// The body literal, which is written just before the call as often as
			// just inside it.
			start, end := i-12, i+8
			if start < 0 {
				start = 0
			}
			if end > len(lines) {
				end = len(lines)
			}
			window := strings.Join(lines[start:end], "\n")
			for _, id := range seqpalIDs {
				for _, shape := range []string{`_aid": ` + id, `"aid": ` + id, `"/v1/users/"+` + id} {
					if strings.Contains(window, shape) {
						t.Errorf("%s:%d hands a SeqPal account id to the policy server (%s).\n"+
							"Go through openampAIDFor or enclaveAIDOf: the two ids are the same "+
							"string only for an ID founded on an OpenAMP account.", f, i+1, id)
					}
				}
			}
		}
	}
}

// The register is keyed by the policy server's account ids and a passport shows
// the SeqPal one. They were the same string until a SeqPal ID could be founded
// on a wallet, so an issuer reading their own cap table lost the ability to tell
// which verified identity a row belongs to.
func TestTheRegisterCanBeMatchedToIdentities(t *testing.T) {
	h := newHarness(t)
	h.s.cfg.nodeURL = newWalletNode(t, true).URL
	h.s.screen = newScreener("")
	session, aid := walletSession(t, h, testPKH)

	xonly := xonlyHex(t, vecPriv)
	enclaveAID := aidFor([]string{xonly})
	ch := h.do("POST", "/api/auth/challenge", "", map[string]any{"xonly": xonly})
	challenge, _ := ch.body["challenge"].(string)
	if att := h.do("POST", "/api/auth/attach-enclave", session, map[string]any{
		"xonly": xonly, "challenge": challenge, "sig": signChallengeHex(t, vecPriv, challenge),
	}); att.code != 200 {
		t.Fatalf("attach: %d %s", att.code, att.raw)
	}

	register := json.RawMessage(`{"holders":{"` + enclaveAID + `":1000,"` +
		strings.Repeat("7", 40) + `":50}}`)
	mapped := h.s.seqpalIDsInRegister(register)
	if mapped[enclaveAID] != aid {
		t.Fatalf("the register row for this holder maps to %q, want the SeqPal id %q",
			mapped[enclaveAID], aid)
	}
	// A holder this platform never registered simply has no entry, rather than a
	// guess.
	if _, ok := mapped[strings.Repeat("7", 40)]; ok {
		t.Fatal("a stranger's row was given a SeqPal identity")
	}
}
