package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// M4 legal-artifact pipeline: the content-addressed document set bound into
// terms_hash, offer-window preimage gating, the RFSA public-offering filing gate,
// and the instrument-characterization the compiler consumes. The acceptance
// artifact for the whole milestone is the binding chain: the terms document, with
// the document manifest inside it, hashes to the exact terms_hash that openampd
// commits on-chain as contract_hash, and recomputing it from the published bytes
// reproduces the same value regardless of key order.

// ============================================================================
// mock openampd that records the terms_hash it is asked to commit
// ============================================================================

// m4OA is an openampd stub whose issue handler records the terms_hash carried in
// the issue body and derives contract_hash from it, so a test can prove terms_hash
// is the exact value committed on chain (contract_hash), not a value the stub
// invented.
type m4OA struct {
	srv        *httptest.Server
	mu         sync.Mutex
	issues     int
	lastTerms  string   // terms_hash from the most recent POST /v1/issuer/assets
	holderAIDs []string // AIDs the holder register reports (for isHolder)
	anchorTxid string
}

func newM4OA(t *testing.T) *m4OA {
	t.Helper()
	f := &m4OA{anchorTxid: "an" + strings.Repeat("c", 62)}
	mux := http.NewServeMux()

	mux.HandleFunc("POST /v1/users", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Pubkeys []string `json:"pubkeys"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		writeJSON(w, 200, map[string]any{"aid": aidFor(req.Pubkeys)})
	})
	mux.HandleFunc("GET /v1/assets", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"assets": []any{}})
	})
	mux.HandleFunc("GET /v1/users/{aid}/address", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"address": "tb1p" + strings.Repeat("q", 58)})
	})
	mux.HandleFunc("POST /v1/issuer/assets", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-issuer-token" {
			writeJSON(w, 401, map[string]any{"error": "bad issuer token"})
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		termsHash, _ := body["terms_hash"].(string)
		f.mu.Lock()
		f.issues++
		n := f.issues
		f.lastTerms = termsHash
		f.mu.Unlock()
		// contract_hash is derived from terms_hash, mirroring an on-chain contract
		// that commits to the terms: it is a pure function of the committed hash, so
		// a test can independently reproduce it from the terms document alone.
		writeJSON(w, 200, map[string]any{
			"asset":         fmt.Sprintf("%064x", n),
			"txid":          fmt.Sprintf("%064x", 1000+n),
			"contract_hash": onchainCommitment(termsHash),
			"contract":      json.RawMessage(fmt.Sprintf(`{"terms_hash":%q,"version":1}`, termsHash)),
		})
	})
	mux.HandleFunc("GET /v1/issuer/holders", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-issuer-token" {
			writeJSON(w, 401, map[string]any{"error": "bad issuer token"})
			return
		}
		f.mu.Lock()
		aids := append([]string(nil), f.holderAIDs...)
		f.mu.Unlock()
		hs := make([]map[string]any, 0, len(aids))
		for _, a := range aids {
			hs = append(hs, map[string]any{"aid": a, "atoms": 1})
		}
		writeJSON(w, 200, map[string]any{"asset": "", "holders": hs})
	})
	mux.HandleFunc("POST /v1/issuer/anchor", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		txid := f.anchorTxid
		f.mu.Unlock()
		writeJSON(w, 200, map[string]any{"txid": txid, "seq": 7, "head": "he" + strings.Repeat("a", 62)})
	})

	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *m4OA) committedTermsHash() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastTerms
}

// onchainCommitment is the test's independent reproduction of the stub's
// contract_hash-from-terms_hash derivation. It stands in for "the contract commits
// to terms_hash": if the on-chain commitment reproduces from the recomputed
// terms_hash, the terms document binds the contract.
func onchainCommitment(termsHash string) string {
	return sha256Hex([]byte("contract-commit|" + termsHash))
}

// m4SeedDraft creates a draft issuance directly in the store with a chosen
// structure and terms, the state the document and deploy paths operate on.
func m4SeedDraft(t *testing.T, s *server, ownerAID, name, ticker, structureID string, terms string) string {
	t.Helper()
	id, _ := randHex(12)
	now := time.Now().Unix()
	if err := s.st.CreateIssuance(&Issuance{
		ID: id, OwnerAID: ownerAID, Name: name, Ticker: ticker,
		StructureID: structureID, Status: "draft", Terms: json.RawMessage(terms),
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

// recomputeTermsHash reproduces terms_hash the way an independent auditor would:
// canonicalize the published terms object (sorted keys, no whitespace) and sha256
// it. Key order in the input is irrelevant by construction.
func recomputeTermsHash(t *testing.T, termsObj any) string {
	t.Helper()
	canon, err := canonicalJSON(termsObj)
	if err != nil {
		t.Fatalf("canonicalize terms: %v", err)
	}
	return sha256Hex(canon)
}

// ============================================================================
// Deliverable 1: terms_hash binds the document manifest, and the on-chain
// contract_hash commits to it. Acceptance chain: terms doc -> terms_hash ->
// contract_hash, reproduced from the published bytes; plus reordered keys ->
// identical hash.
// ============================================================================

func TestTermsHashBindsDocumentsAndCommitsOnChain(t *testing.T) {
	oa := newM4OA(t)
	s := newM3Server(t, oa.srv.URL)
	h := s.handler()

	session, ownerAID, _ := m3SeedAccount(t, s, vecPriv, "Issuer Ida")
	// A private placement (no public marker), so the RFSA gate does not apply and
	// the focus stays on the binding chain.
	terms := `{"jurisdictions":{"DE":{"access":"standard"},"US":{"access":"restricted"}},"lockup_days":30,"reg_s":{"days":40}}`
	issID := m4SeedDraft(t, s, ownerAID, "Aurora Fund", "AURA", "native-equity", terms)

	// 1. Generate the document set; the response carries terms_hash + manifest_hash
	//    + the document list.
	gen := m3req(t, h, "POST", "/api/issuances/"+issID+"/documents", session, nil)
	if gen.code != 200 {
		t.Fatalf("generate documents = %d %s", gen.code, gen.errMsg())
	}
	termsHash, _ := gen.body["terms_hash"].(string)
	manifestHash, _ := gen.body["manifest_hash"].(string)
	if !isHex64(termsHash) || !isHex64(manifestHash) {
		t.Fatalf("terms_hash/manifest_hash not 64-hex: %q %q", termsHash, manifestHash)
	}
	docs, _ := gen.body["documents"].([]any)
	if len(docs) == 0 {
		t.Fatalf("no documents generated: %s", gen.raw)
	}

	// 2. The published terms document is public (the manifest is always public,
	//    even before deploy). Recompute terms_hash from its bytes.
	pub := m3req(t, h, "GET", "/api/terms/"+termsHash, "", nil) // anonymous
	if pub.code != 200 {
		t.Fatalf("public GET /api/terms = %d %s", pub.code, pub.errMsg())
	}
	publishedTerms := pub.body["terms"]
	if got := recomputeTermsHash(t, publishedTerms); got != termsHash {
		t.Fatalf("recomputed terms_hash from the published terms document = %s, want %s", got, termsHash)
	}

	// 3. The manifest is INSIDE the terms object, so terms_hash commits to the
	//    documents: the manifest_hash carried in terms.documents matches, and it is
	//    the sha256 of the canonical manifest set.
	tm, _ := publishedTerms.(map[string]any)
	docBlock, _ := tm["documents"].(map[string]any)
	if docBlock == nil {
		t.Fatalf("the terms object carries no documents block; terms_hash does not commit to documents")
	}
	if got, _ := docBlock["manifest_hash"].(string); got != manifestHash {
		t.Fatalf("terms.documents.manifest_hash = %q, want %q (documents not bound into terms)", got, manifestHash)
	}
	set := docBlock["set"]
	if got := sha256Hex(mustCanon(t, set)); got != manifestHash {
		t.Fatalf("sha256(canonical manifest set) = %s, want manifest_hash %s", got, manifestHash)
	}

	// 4. Deploy (private, ungated). The stub records the terms_hash it is asked to
	//    commit and returns a contract_hash derived from it. This closes the chain:
	//    the terms_hash the browser could recompute is the exact value committed on
	//    chain as contract_hash.
	dep := m3req(t, h, "POST", "/api/deploy", session, map[string]any{
		"issuance_id": issID, "supply": 1000, "precision": 2,
		"terms": json.RawMessage(terms), "terms_hash": termsHash,
	})
	if dep.code != 200 {
		t.Fatalf("private deploy = %d %s", dep.code, dep.errMsg())
	}
	if committed := oa.committedTermsHash(); committed != termsHash {
		t.Fatalf("openampd was asked to commit terms_hash %s, but the terms document hashes to %s", committed, termsHash)
	}
	gotContract, _ := dep.body["contract_hash"].(string)
	if gotContract != onchainCommitment(termsHash) {
		t.Fatalf("on-chain contract_hash = %s, does not reproduce from terms_hash %s (want %s)",
			gotContract, termsHash, onchainCommitment(termsHash))
	}

	// The genesis terms_hash the Verify explainer renders reproduces the same value
	// from the stored canonical terms.
	iss, _ := s.st.IssuanceByID(issID)
	if g := genesisTermsHash(iss); g != termsHash {
		t.Fatalf("genesis terms_hash = %s, want %s", g, termsHash)
	}
}

// The adversarial property: two issuers who enter the SAME terms with a DIFFERENT
// key order obtain the IDENTICAL on-chain commitment. Canonicalization, not byte
// order, is what terms_hash commits to.
func TestTermsHashIsKeyOrderIndependent(t *testing.T) {
	oa := newM4OA(t)
	s := newM3Server(t, oa.srv.URL)

	_, ownerAID, _ := m3SeedAccount(t, s, vecPriv, "Issuer Ida")
	// Same content, different top-level key order, and different nested key order.
	termsA := `{"lockup_days":30,"jurisdictions":{"DE":{"access":"standard"}},"reg_s":{"days":40}}`
	termsB := `{"reg_s":{"days":40},"jurisdictions":{"DE":{"access":"standard"}},"lockup_days":30}`

	// The document set is a function of the issuer name, ticker, and structure as
	// well as the matrix, so those are held identical; only the terms key order
	// differs between the two issuances.
	issA := m4SeedDraft(t, s, ownerAID, "Aurora Fund", "AURA", "native-equity", termsA)
	issB := m4SeedDraft(t, s, ownerAID, "Aurora Fund", "AURA", "native-equity", termsB)

	hashA := deriveTermsHash(t, s, issA, termsA)
	hashB := deriveTermsHash(t, s, issB, termsB)
	if hashA != hashB {
		t.Fatalf("reordered terms produced different terms_hash: %s vs %s", hashA, hashB)
	}
	if !isHex64(hashA) {
		t.Fatalf("terms_hash is not 64-hex: %q", hashA)
	}
}

// deriveTermsHash runs the exact server-side binding the deploy path runs
// (ensureDocuments -> canonicalize -> sha256) and returns terms_hash.
func deriveTermsHash(t *testing.T, s *server, issID, terms string) string {
	t.Helper()
	iss, err := s.st.IssuanceByID(issID)
	if err != nil || iss == nil {
		t.Fatalf("load issuance %s: %v", issID, err)
	}
	bound, _, err := s.ensureDocuments(iss, json.RawMessage(terms))
	if err != nil {
		t.Fatalf("ensureDocuments: %v", err)
	}
	var obj any
	if err := json.Unmarshal(bound, &obj); err != nil {
		t.Fatalf("unmarshal bound terms: %v", err)
	}
	return recomputeTermsHash(t, obj)
}

func mustCanon(t *testing.T, v any) []byte {
	t.Helper()
	b, err := canonicalJSON(v)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	return b
}

// ============================================================================
// Deliverable 2: offer-window preimage gating. The manifest is always public; a
// preimage is gate-passers-only during the offer window (anonymous -> non-200);
// at offer close preimages publish ungated.
// ============================================================================

func TestOfferWindowGatesDocumentPreimages(t *testing.T) {
	oa := newM4OA(t)
	s := newM3Server(t, oa.srv.URL)
	h := s.handler()

	owner, ownerAID, _ := m3SeedAccount(t, s, vecPriv, "Issuer Ida")
	issID := m4SeedDraft(t, s, ownerAID, "Aurora Fund", "AURA", "native-equity",
		`{"jurisdictions":{"DE":{"access":"standard"}}}`)

	// Generating documents opens the offer window (EnsureOffering, offer_open=true).
	gen := m3req(t, h, "POST", "/api/issuances/"+issID+"/documents", owner, nil)
	if gen.code != 200 {
		t.Fatalf("generate documents = %d %s", gen.code, gen.errMsg())
	}
	termsHash, _ := gen.body["terms_hash"].(string)
	docs, _ := gen.body["documents"].([]any)
	if len(docs) == 0 {
		t.Fatal("no documents to gate")
	}
	first, _ := docs[0].(map[string]any)
	docHash, _ := first["hash"].(string)
	if !isHex64(docHash) {
		t.Fatalf("document hash not 64-hex: %q", docHash)
	}

	// The manifest is always public, even during the offer window.
	if r := m3req(t, h, "GET", "/api/terms/"+termsHash, "", nil); r.code != 200 {
		t.Fatalf("public manifest during the offer window = %d, want 200", r.code)
	}

	// A gate-passer: a second identity holding a verified SeqPal ID.
	gate, gateAID, _ := m3SeedAccount(t, s, vecPriv2, "Gwen Gate")
	if err := s.st.UpsertClaims(&Claims{AID: gateAID, Residence: "DE", BaseEligibility: "pro", Status: "verified"}); err != nil {
		t.Fatal(err)
	}

	// --- during the offer window ---
	// Anonymous request for a preimage: the standing probe returns a non-200.
	anon := m3req(t, h, "GET", "/api/doc/"+docHash, "", nil)
	if anon.code == 200 {
		t.Fatalf("anonymous preimage during the offer window = 200; the offer-window gate is open")
	}
	if anon.code != 401 {
		t.Fatalf("anonymous preimage = %d, want 401", anon.code)
	}
	// A gate-passing session is served the preimage.
	if r := rawGet(t, h, "/api/doc/"+docHash, gate); r.code != 200 {
		t.Fatalf("gate-passer preimage during the offer window = %d, want 200 (%s)", r.code, r.raw)
	}
	// The issuer always retains access.
	if r := rawGet(t, h, "/api/doc/"+docHash, owner); r.code != 200 {
		t.Fatalf("issuer preimage during the offer window = %d, want 200", r.code)
	}

	// --- offer close ---
	if r := m3req(t, h, "POST", "/api/issuances/"+issID+"/offer-close", owner, nil); r.code != 200 {
		t.Fatalf("offer-close = %d %s", r.code, r.errMsg())
	}
	// After close an anonymous request is served the preimage.
	if r := rawGet(t, h, "/api/doc/"+docHash, ""); r.code != 200 {
		t.Fatalf("anonymous preimage after offer close = %d, want 200 (%s)", r.code, r.raw)
	}
}

// rawGet fetches a document preimage (an HTML/PDF body, not JSON) and reports the
// status and raw body, so the gating tests read a document endpoint that does not
// answer JSON.
func rawGet(t *testing.T, h http.Handler, path, session string) resp {
	t.Helper()
	req := httptest.NewRequest("GET", "/seqpal"+path, strings.NewReader(""))
	if session != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return resp{code: rec.Code, raw: rec.Body.String()}
}

// ============================================================================
// Deliverable 3: the RFSA filing gate. A public offering cannot deploy without a
// filing bound to its terms; with one it proceeds; the filing is retrievable and
// its hash is anchored.
// ============================================================================

func TestRFSAFilingGatesPublicOffering(t *testing.T) {
	oa := newM4OA(t)
	s := newM3Server(t, oa.srv.URL)
	h := s.handler()

	session, ownerAID, _ := m3SeedAccount(t, s, vecPriv, "Issuer Ida")
	publicTerms := `{"public":true,"jurisdictions":{"US":{"access":"restricted"}}}`
	issID := m4SeedDraft(t, s, ownerAID, "Aurora Fund", "AURA", "native-equity", publicTerms)

	deploy := func() resp {
		return m3req(t, h, "POST", "/api/deploy", session, map[string]any{
			"issuance_id": issID, "supply": 1000, "precision": 2, "terms": json.RawMessage(publicTerms),
		})
	}

	// 1. A public offering with no filing is refused.
	if r := deploy(); r.code != 403 {
		t.Fatalf("public deploy without a filing = %d, want 403 (%s)", r.code, r.errMsg())
	}

	// 2. Learn the terms_hash the deploy path commits to, then file against it.
	gen := m3req(t, h, "POST", "/api/issuances/"+issID+"/documents", session, nil)
	if gen.code != 200 {
		t.Fatalf("generate documents = %d %s", gen.code, gen.errMsg())
	}
	termsHash, _ := gen.body["terms_hash"].(string)
	manifestHash, _ := gen.body["manifest_hash"].(string)

	file := m3req(t, h, "POST", "/api/rfsa/filings", session, map[string]any{
		"issuer": "Aurora Fund", "structure": "equity",
		"doc_manifest_hash": manifestHash, "terms_hash": termsHash, "issuance_id": issID,
	})
	if file.code != 200 {
		t.Fatalf("file RFSA = %d %s", file.code, file.errMsg())
	}
	number, _ := file.body["filing_number"].(string)
	if !strings.HasPrefix(number, "RFSA-FP-") {
		t.Fatalf("filing_number = %q, want an RFSA-FP- number", number)
	}
	if lbl, _ := file.body["label"].(string); !strings.Contains(lbl, "simulated regulator") {
		t.Fatalf("filing is not labeled a simulated regulator: %q", lbl)
	}

	// 3. The filing is retrievable at the public lookup, and its hash is anchored.
	look := m3req(t, h, "GET", "/api/rfsa/filings/"+number, "", nil) // public
	if look.code != 200 {
		t.Fatalf("public RFSA lookup = %d %s", look.code, look.errMsg())
	}
	f, _ := look.body["filing"].(map[string]any)
	if f == nil {
		t.Fatalf("lookup returned no filing: %s", look.raw)
	}
	if got, _ := f["terms_hash"].(string); got != termsHash {
		t.Fatalf("filed terms_hash = %q, want %q", got, termsHash)
	}
	if fh, _ := f["filing_hash"].(string); !isHex64(fh) {
		t.Fatalf("filing_hash not content-addressed: %q", fh)
	}
	if at, _ := f["anchor_txid"].(string); at == "" {
		t.Fatalf("filing hash was not anchored (empty anchor_txid)")
	}

	// 4. With the filing in place the same public deploy proceeds.
	dep := deploy()
	if dep.code != 200 {
		t.Fatalf("public deploy with a filing = %d, want 200 (%s)", dep.code, dep.errMsg())
	}
	if committed := oa.committedTermsHash(); committed != termsHash {
		t.Fatalf("the filed terms_hash %s is not the committed one %s", termsHash, committed)
	}
}

// A private placement (the default, no public marker) needs no filing: the gate is
// opt-in, so the pre-M4 deploy path is unchanged.
func TestPrivatePlacementNeedsNoFiling(t *testing.T) {
	oa := newM4OA(t)
	s := newM3Server(t, oa.srv.URL)
	h := s.handler()

	session, ownerAID, _ := m3SeedAccount(t, s, vecPriv, "Issuer Ida")
	issID := m4SeedDraft(t, s, ownerAID, "Aurora Fund", "AURA", "native-equity",
		`{"jurisdictions":{"DE":{"access":"standard"}}}`)

	dep := m3req(t, h, "POST", "/api/deploy", session, map[string]any{
		"issuance_id": issID, "supply": 1000, "precision": 2,
	})
	if dep.code != 200 {
		t.Fatalf("private placement deploy = %d, want 200 (%s)", dep.code, dep.errMsg())
	}
}

// ============================================================================
// Deliverable 4: the matrix compiler consumes the instrument characterization. An
// AIF-classified structure disables the EU retail lift (no j:EUSTATE:ret admitted)
// and restricts EU member states to professional categories.
// ============================================================================

func TestCompilerConsumesAIFCharacterization(t *testing.T) {
	oa := newM4OA(t)
	s := newM3Server(t, oa.srv.URL)

	// DE (an EU member state) opens at "standard", which without a clamp admits
	// retail, accreditation, and professional. US (non-EU) is "restricted".
	terms := json.RawMessage(`{"jurisdictions":{"DE":{"access":"standard"},"US":{"access":"restricted"}}}`)

	// The characterization memo itself: an SPV is an AIF with no EU retail lift.
	ch, cherr := characterize("equity-spv")
	if cherr != nil {
		t.Fatalf("characterize equity-spv: %v", cherr)
	}
	if !ch.IsAIF || ch.EURetailLift || ch.UKGate != "cis-promotion" {
		t.Fatalf("equity-spv memo = is_aif=%v eu_retail_lift=%v uk_gate=%q, want true/false/cis-promotion",
			ch.IsAIF, ch.EURetailLift, ch.UKGate)
	}

	// AIF structure: EU (DE) is clamped to professional only.
	aif := &Issuance{ID: "aif1", StructureID: "equity-spv", Ticker: "SPVA", Name: "SPV A"}
	compiled, err := s.compileForIssuance(aif, terms)
	if err != nil {
		t.Fatalf("compile equity-spv: %v", err)
	}
	cats := toSet(compiled.AllowedCategories)
	if !cats["j:DE:pro"] {
		t.Fatalf("AIF DE lost its professional token; allowed = %v", compiled.AllowedCategories)
	}
	if cats["j:DE:ret"] {
		t.Fatalf("AIF admitted j:DE:ret; the EU retail lift was not disabled; allowed = %v", compiled.AllowedCategories)
	}
	if cats["j:DE:acc"] {
		t.Fatalf("AIF admitted j:DE:acc; EU access was not restricted to professional; allowed = %v", compiled.AllowedCategories)
	}
	// A non-EU jurisdiction is untouched by the AIF clamp.
	if !cats["j:US:acc"] || !cats["j:US:pro"] {
		t.Fatalf("the AIF clamp altered a non-EU jurisdiction (US); allowed = %v", compiled.AllowedCategories)
	}

	// Contrast: a plain-equity (non-AIF) structure with the identical matrix DOES
	// admit EU retail, proving the compiler's behavior is driven by the
	// characterization and not by the matrix alone.
	eq := &Issuance{ID: "eq1", StructureID: "native-equity", Ticker: "EQA", Name: "Equity A"}
	ec, err := s.compileForIssuance(eq, terms)
	if err != nil {
		t.Fatalf("compile equity: %v", err)
	}
	if !toSet(ec.AllowedCategories)["j:DE:ret"] {
		t.Fatalf("plain equity did not admit j:DE:ret; the retail lift is unavailable even for a non-AIF structure; allowed = %v", ec.AllowedCategories)
	}
	if nech, _ := characterize("native-equity"); nech.EURetailLift != true {
		t.Fatalf("equity memo disables the retail lift; want it available")
	}
}

func toSet(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, it := range items {
		m[it] = true
	}
	return m
}
