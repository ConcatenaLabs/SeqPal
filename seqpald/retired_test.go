package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// A new deploy electing "tree" is refused by name, pointing at "serviced", and
// nothing is minted or recorded as a tree.
func TestTreeElectionRefused(t *testing.T) {
	h := newM10Harness(t)
	if hr := h.do("GET", "/api/health", "", nil); hr.body["tree"] != nil {
		t.Fatalf("/health still reports tree = %v", hr.body["tree"])
	}
	session, aid, _ := h.register(genPriv(t), "Issuer", "HN")
	issID := h.createIssuance(session, "Tree Co", "TREX", map[string]any{
		"jurisdictions": map[string]any{"HN": map[string]any{"access": "standard"}},
	})
	r := h.do("POST", "/api/deploy", session, map[string]any{
		"issuance_id": issID, "supply": 1000, "precision": 2, "enforcement": "tree",
	})
	if r.code != 400 || !strings.Contains(r.errMsg(), enclaveRetired) || !strings.Contains(r.errMsg(), `choose "serviced"`) {
		t.Fatalf("tree deploy = %d %q, want 400 naming the retirement and serviced", r.code, r.errMsg())
	}
	if h.oa.assetCount() != 0 {
		t.Fatal("a refused tree deploy minted something")
	}
	var stored string
	if err := h.s.st.db.QueryRow(`SELECT enforcement FROM issuances WHERE id = ?`, issID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == "tree" {
		t.Fatal("the refused election was written as tree")
	}
	assertAuditedM10(t, h.m5h, aid, "deploy.refused")
	// The old retry route is gone, by name.
	g := h.do("POST", "/api/issuances/"+issID+"/tree", session, nil)
	if g.code != 410 || g.errMsg() != enclaveRetired {
		t.Fatalf("POST /api/issuances/{id}/tree = %d %q, want 410", g.code, g.errMsg())
	}
}

// An issuance stored with the "tree" election (column and terms) reads as
// "serviced" in the record and the API, and deploys as a serviced token.
func TestStoredTreeElectionReadsAsServiced(t *testing.T) {
	h := newM10Harness(t)
	session, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID := h.createIssuance(session, "Tree Co", "TREX", map[string]any{
		"jurisdictions": map[string]any{"HN": map[string]any{"access": "standard"}},
		"price":         1,
		"enforcement":   "tree",
	})
	if _, err := h.s.st.db.Exec(`UPDATE issuances SET enforcement = 'tree', tree_operator = ? WHERE id = ?`,
		strings.Repeat("11", 32), issID); err != nil {
		t.Fatal(err)
	}
	iss, err := h.s.st.IssuanceByID(issID)
	if err != nil || iss == nil {
		t.Fatalf("read: %v", err)
	}
	if iss.Enforcement != "serviced" {
		t.Fatalf("stored tree read as %q", iss.Enforcement)
	}
	// The record reads as serviced. The terms are returned as the issuer wrote
	// them (they are hashed into the deploy), and the page reads them through
	// the same rule (src/lib/issuance.js view).
	list := h.do("GET", "/api/issuances", session, nil)
	var listed struct {
		Issuances []map[string]any `json:"issuances"`
	}
	if err := json.Unmarshal([]byte(list.raw), &listed); err != nil || len(listed.Issuances) != 1 {
		t.Fatalf("issuances list: %v %s", err, list.raw)
	}
	if got := listed.Issuances[0]["enforcement"]; got != "serviced" {
		t.Fatalf("listed enforcement = %v", got)
	}
	if _, has := listed.Issuances[0]["tree_operator"]; has {
		t.Fatalf("listed tree_operator: %s", list.raw)
	}
	r := h.do("POST", "/api/deploy", session, map[string]any{
		"issuance_id": issID, "supply": 1000, "precision": 2, "max_supply": 2000,
	})
	if r.code != 200 {
		t.Fatalf("re-deploy of a stored tree draft = %d %s", r.code, r.raw)
	}
	if _, has := r.body["tree"]; has {
		t.Fatalf("deploy still answers tree: %v", r.body["tree"])
	}
	if len(h.oa.issueBodies) != 1 || h.oa.issueBodies[0]["holder_aid"] == "" {
		t.Fatalf("not minted as a serviced token: %v", h.oa.issueBodies)
	}
	iss, _ = h.s.st.IssuanceByID(issID)
	if iss.Enforcement != "serviced" || iss.Status != "live" {
		t.Fatalf("after deploy: %q %q", iss.Enforcement, iss.Status)
	}
}

func TestTreeOperatorAcceptedAndIgnored(t *testing.T) {
	if treeOperatorNote("") != "" {
		t.Fatal("unset must log nothing")
	}
	if n := treeOperatorNote("not-a-key"); !strings.Contains(n, enclaveRetired) {
		t.Fatalf("note = %q", n)
	}
	if readEnforcement(" Tree ") != "serviced" || readEnforcement("network") != "network" {
		t.Fatal("readEnforcement")
	}
}
