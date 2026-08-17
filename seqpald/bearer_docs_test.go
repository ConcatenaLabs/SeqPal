package main

import (
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// Bearer charter document set: a bearer enforcement election generates the four
// charter documents (articles-of-incorporation, freeze-power-charter,
// shareholder-terms, risk-acceptance-instrument) beside the applicable shared
// set, omits the compiled-matrix artifacts (operating-agreement, kid-summary,
// lift-artifact), and binds all of it into the terms manifest so the on-chain
// contract commits to the exact bytes. Determinism is pinned by hash, and the
// non-bearer set is pinned byte-identical to what it produced before the
// bearer set existed.

// docPinContext is the fixed, fully deterministic generator input the pinned
// hashes are computed over. Any content change to the generator moves these
// hashes, which is the point: a pin failure means the committed document bytes
// changed and every derived asset id would change with them.
func docPinContext(t *testing.T) docContext {
	t.Helper()
	ch, err := characterize("equity")
	if err != nil {
		t.Fatal(err)
	}
	return docContext{
		IssuerName:   "Pinned Issuer Ltd",
		OfferingName: "Pinned Offering",
		Ticker:       "PIN",
		Structure:    ch.Structure,
		Char:         ch,
		Jurisdictions: []jurisdictionRow{
			{Code: "DE", Access: "standard"},
		},
		Lockup:         "No transfer lockup is configured for this offering.",
		IssuerExternal: true,
	}
}

func manifestHashOf(t *testing.T, docs []GeneratedDoc) string {
	t.Helper()
	set := make([]docManifestEntry, 0, len(docs))
	for _, d := range docs {
		set = append(set, docManifestEntry{Hash: d.Hash, Kind: d.Kind, Title: d.Title})
	}
	sort.Slice(set, func(i, j int) bool { return set[i].Kind < set[j].Kind })
	canon, err := canonicalJSON(set)
	if err != nil {
		t.Fatal(err)
	}
	return sha256Hex(canon)
}

// The non-bearer regression pin: this manifest hash was computed from the
// pinned context BEFORE the bearer charter set was introduced. A non-bearer
// issuance must keep producing byte-identical documents, because its terms_hash
// (and so its asset id) commits to them.
const pinnedServicedManifest = "ed6b67395bfcec9af51ad26248fb8a61479beb8b3c7941e6ace880c32f5f0f38"

func TestNonBearerDocumentsUnchangedByBearerSet(t *testing.T) {
	docs := buildDocumentSet(docPinContext(t))
	if got := manifestHashOf(t, docs); got != pinnedServicedManifest {
		t.Fatalf("non-bearer manifest hash = %s, want the pre-bearer pin %s (the standard document bytes changed)", got, pinnedServicedManifest)
	}
	kinds := make([]string, 0, len(docs))
	for _, d := range docs {
		kinds = append(kinds, d.Kind)
	}
	want := append([]string(nil), docKinds...)
	sort.Strings(want)
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("non-bearer kinds = %v, want %v", kinds, want)
	}
}

// The bearer determinism pin: same fixed input, same hashes, run to run.
var pinnedBearerDocs = map[string]string{
	"articles-of-incorporation":  "e7fed99a22aa846a383cdb16a8d5338a7817f08630750d9b621361814a5f44fa",
	"escrow-terms":               "ace0aa48abf1c17504da18996b7ef7c844767f96dc135d8ef40c5bf25071f058",
	"freeze-power-charter":       "06bbcdfc0f8578321b5a38a801c1adb33ee8aa12dbc31af4bf1afa198ed126fd",
	"offering-memorandum":        "09ed02572812cc6b5ee1ee369b47eef0869f99321dfb43cc08dba4501d34a783",
	"risk-acceptance-instrument": "0dce6f727a832fd30b3646f5a53e8a0c6e4df24ff0d0a04ea4f5b4761747f45d",
	"shareholder-terms":          "c487456a81b4db0c3aed420a0cc5c6b7b8e27eabbfe01ca372453e78ec73bbb7",
	"subscription-agreement":     "8a90fd7769c690e84caf581c0371c756e673906249df3e84f3214697e6f1e74a",
	"uk-investor-statement":      "fe7c5c7a10cb58036eb2aff0f828cd71698bd7de39e5d40e949d08a149f85c47",
}

const pinnedBearerManifest = "6910da87db8ae8e57fb64d5df405b7fe1a2871da65da43ad8cb6fecdc2da94ec"

func bearerPinContext(t *testing.T) docContext {
	ctx := docPinContext(t)
	ctx.Bearer = true
	ctx.Jurisdictions = nil
	ctx.EntityJurisdiction = "Próspera ZEDE, Roatán"
	return ctx
}

func TestBearerDocumentSetDeterministicAndPinned(t *testing.T) {
	ctx := bearerPinContext(t)
	docs := buildDocumentSet(ctx)
	again := buildDocumentSet(ctx)
	if len(docs) != len(bearerDocKinds) {
		t.Fatalf("bearer set has %d documents, want %d", len(docs), len(bearerDocKinds))
	}
	for i, d := range docs {
		if again[i].Hash != d.Hash {
			t.Fatalf("bearer document %s is not deterministic: %s then %s", d.Kind, d.Hash, again[i].Hash)
		}
		want, ok := pinnedBearerDocs[d.Kind]
		if !ok {
			t.Fatalf("unexpected bearer document kind %s", d.Kind)
		}
		if d.Hash != want {
			t.Fatalf("bearer document %s hash = %s, want pinned %s", d.Kind, d.Hash, want)
		}
		if sha256Hex(d.HTML) != d.Hash {
			t.Fatalf("bearer document %s hash is not the sha256 of its HTML", d.Kind)
		}
	}
	if got := manifestHashOf(t, docs); got != pinnedBearerManifest {
		t.Fatalf("bearer manifest hash = %s, want pinned %s", got, pinnedBearerManifest)
	}
	// The matrix-only artifacts are omitted for bearer.
	for _, d := range docs {
		switch d.Kind {
		case "operating-agreement", "kid-summary", "lift-artifact":
			t.Fatalf("bearer set carries the matrix artifact %s", d.Kind)
		}
	}
	// The verbatim attested statements appear in the risk-acceptance instrument.
	for _, d := range docs {
		if d.Kind != "risk-acceptance-instrument" {
			continue
		}
		body := string(d.HTML)
		for _, stmt := range []string{
			"My company has no United States operations, assets, or banking.",
			"I accept in writing that United States regulators may object, and that this is my company’s risk.",
		} {
			if !strings.Contains(body, stmt) {
				t.Fatalf("risk-acceptance-instrument is missing the attested statement %q", stmt)
			}
		}
	}
}

// The live-path property: a bearer deploy binds the charter manifest into the
// terms the on-chain contract commits to, the four charter documents exist in
// the store and are served, and the existing e-signature machinery signs them.
func TestBearerDeployBindsCharterDocumentsAndESign(t *testing.T) {
	h := newM10Harness(t)
	priv := genPriv(t)
	session, _, _ := h.register(priv, "Charter Issuer", "HN")
	recovery := xonlyHex(t, genPriv(t))
	issID, _ := h.deployBearerLive(t, session, priv, "CHTR", recovery)

	iss, err := h.s.st.IssuanceByID(issID)
	if err != nil || iss == nil {
		t.Fatalf("load issuance: %v", err)
	}
	var terms map[string]any
	if err := json.Unmarshal(rawOrEmpty(iss.Terms), &terms); err != nil {
		t.Fatalf("stored terms: %v", err)
	}
	docBlock, _ := terms["documents"].(map[string]any)
	if docBlock == nil {
		t.Fatal("the bearer terms carry no documents block")
	}
	set, _ := docBlock["set"].([]any)
	byKind := map[string]string{}
	for _, e := range set {
		row, _ := e.(map[string]any)
		kind, _ := row["kind"].(string)
		hash, _ := row["hash"].(string)
		byKind[kind] = hash
	}
	for _, kind := range []string{"articles-of-incorporation", "freeze-power-charter", "shareholder-terms", "risk-acceptance-instrument"} {
		if !isHex64(byKind[kind]) {
			t.Fatalf("the bearer manifest is missing %s: %v", kind, byKind)
		}
	}
	for _, kind := range []string{"operating-agreement", "kid-summary", "lift-artifact"} {
		if _, ok := byKind[kind]; ok {
			t.Fatalf("the bearer manifest carries the matrix artifact %s", kind)
		}
	}

	// The charter preimages are stored and served to the issuer session.
	docHash := byKind["risk-acceptance-instrument"]
	get := h.do("GET", "/api/doc/"+docHash, session, nil)
	if get.code != 200 {
		t.Fatalf("GET charter document = %d %s", get.code, get.errMsg())
	}

	// E-sign the instrument with the existing machinery: BIP340 over the tagged
	// document hash by the session key.
	msg, err := hex.DecodeString(docHash)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := signTagged(priv, documentTag, msg)
	if err != nil {
		t.Fatal(err)
	}
	signRes := h.do("POST", "/api/documents/"+docHash+"/sign", session, map[string]any{"sig": sig})
	if signRes.code != 200 {
		t.Fatalf("e-sign charter document = %d %s", signRes.code, signRes.errMsg())
	}
	sigs := h.do("GET", "/api/documents/"+docHash+"/signatures", "", nil)
	if sigs.code != 200 {
		t.Fatalf("signatures read = %d", sigs.code)
	}
	list, _ := sigs.body["signatures"].([]any)
	if len(list) != 1 {
		t.Fatalf("signatures = %d, want 1: %s", len(list), sigs.raw)
	}

	// The terms_hash the contract committed reproduces from the stored terms,
	// so the on-chain commitment covers the charter documents.
	if g := genesisTermsHash(iss); !isHex64(g) {
		t.Fatalf("genesis terms hash not reproducible: %q", g)
	}
}

// TestDeployAcceptsTermsHashOfTermsAsSent pins the cross-check semantics: the
// wizard hashes the terms it sends (it cannot know the document manifest the
// server binds), so that hash must be accepted, while a hash over neither the
// sent nor the bound terms must still be refused.
func TestDeployAcceptsTermsHashOfTermsAsSent(t *testing.T) {
	h := newM10Harness(t)
	priv := genPriv(t)
	session, _, _ := h.register(priv, "Hash Cross Check", "HN")

	terms := map[string]any{
		"structure":     "native-equity",
		"jurisdictions": map[string]any{"HN": map[string]any{"access": "standard"}},
		"price":         1.0,
	}
	canon, err := canonicalJSON(terms)
	if err != nil {
		t.Fatal(err)
	}
	sentHash := sha256Hex(canon)

	issID := h.createIssuance(session, "Hash Co", "HXCK", terms)
	dep := h.do("POST", "/api/deploy", session, map[string]any{
		"issuance_id": issID, "supply": 1000, "precision": 0,
		"terms": terms, "terms_hash": sentHash,
	})
	if dep.code != 200 {
		t.Fatalf("deploy with the as-sent terms_hash = %d %s", dep.code, dep.errMsg())
	}

	iss2 := h.createIssuance(session, "Hash Co Two", "HXCK2", terms)
	bad := h.do("POST", "/api/deploy", session, map[string]any{
		"issuance_id": iss2, "supply": 1000, "precision": 0,
		"terms": terms, "terms_hash": strings.Repeat("ab", 32),
	})
	if bad.code != 400 || !strings.Contains(bad.errMsg(), "terms_hash mismatch") {
		t.Fatalf("deploy with a foreign terms_hash = %d %s, want 400 mismatch", bad.code, bad.errMsg())
	}
}
