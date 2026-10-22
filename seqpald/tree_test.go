package main

import (
	"strings"
	"testing"
)

// The fourth election: a token whose holdings may sit in the issuer's tree.

const testTreeOperator = "1111111111111111111111111111111111111111111111111111111111111111"

func TestTreeElectionUnavailableWithoutAnOperator(t *testing.T) {
	h := newM10Harness(t)
	if hr := h.do("GET", "/api/health", "", nil); hr.body["tree"] != false {
		t.Fatalf("/health tree = %v, want false", hr.body["tree"])
	}
	session, aid, _ := h.register(genPriv(t), "Issuer", "HN")
	issID := h.createIssuance(session, "Tree Co", "TREX", map[string]any{
		"jurisdictions": map[string]any{"HN": map[string]any{"access": "standard"}},
	})
	r := h.do("POST", "/api/deploy", session, map[string]any{
		"issuance_id": issID, "supply": 1000, "precision": 2, "enforcement": "tree",
	})
	if r.code != 501 || !strings.Contains(r.errMsg(), "instant transfers are not available on this deployment") {
		t.Fatalf("tree deploy without an operator = %d %q, want 501", r.code, r.errMsg())
	}
	iss, _ := h.s.st.IssuanceByID(issID)
	if iss.Enforcement != "tree" {
		t.Fatalf("the election was not recorded: %q", iss.Enforcement)
	}
	if h.oa.assetCount() != 0 || len(h.oa.treeBodies) != 0 {
		t.Fatal("a refused tree deploy minted or enabled something")
	}
	assertAuditedM10(t, h.m5h, aid, "deploy.refused")
}

func TestTreeElectionDeploysAndShowsIt(t *testing.T) {
	h := newM10Harness(t)
	h.s.cfg.treeOperator = testTreeOperator
	if hr := h.do("GET", "/api/health", "", nil); hr.body["tree"] != true {
		t.Fatalf("/health tree = %v, want true", hr.body["tree"])
	}
	session, aid, _ := h.register(genPriv(t), "Issuer", "HN")
	issID := h.createIssuance(session, "Tree Co", "TREX", map[string]any{
		"jurisdictions": map[string]any{"HN": map[string]any{"access": "standard"}},
		"price":         1,
	})
	// A tree token is a serviced one: more of it may be created later.
	r := h.do("POST", "/api/deploy", session, map[string]any{
		"issuance_id": issID, "supply": 1000, "precision": 2, "max_supply": 2000, "enforcement": "tree",
	})
	if r.code != 200 {
		t.Fatalf("tree deploy = %d %s", r.code, r.raw)
	}
	tree, _ := r.body["tree"].(map[string]any)
	if tree["enabled"] != true || tree["operator"] != testTreeOperator {
		t.Fatalf("deploy answered tree %v", r.body["tree"])
	}
	asset, _ := r.body["asset"].(string)
	if len(h.oa.treeBodies) != 1 || h.oa.treeBodies[0]["asset"] != asset || h.oa.treeBodies[0]["operator"] != testTreeOperator {
		t.Fatalf("the policy server was asked %v", h.oa.treeBodies)
	}
	if len(h.oa.issueBodies) != 1 || h.oa.issueBodies[0]["holder_aid"] == "" {
		t.Fatalf("not minted as a serviced token: %v", h.oa.issueBodies)
	}
	iss, _ := h.s.st.IssuanceByID(issID)
	if iss.Enforcement != "tree" || iss.TreeOperator != testTreeOperator || iss.Status != "live" {
		t.Fatalf("issuance %q %q %q", iss.Enforcement, iss.TreeOperator, iss.Status)
	}
	// The issuer's pages read the election from its issuances.
	list := h.do("GET", "/api/issuances", session, nil)
	if !strings.Contains(list.raw, `"enforcement":"tree"`) || !strings.Contains(list.raw, `"tree_operator":"`+testTreeOperator+`"`) {
		t.Fatalf("issuances list does not show the election: %s", list.raw)
	}
	assertAuditedM10(t, h.m5h, aid, "deploy.tree_enabled")
	// Asked again, it says where it stands and asks nothing.
	again := h.do("POST", "/api/issuances/"+issID+"/tree", session, nil)
	if again.code != 200 || again.body["enabled"] != true || len(h.oa.treeBodies) != 1 {
		t.Fatalf("retry of an enabled tree = %d %s", again.code, again.raw)
	}
}

func TestTreeElectionWhenThePolicyServerRefuses(t *testing.T) {
	h := newM10Harness(t)
	h.s.cfg.treeOperator = testTreeOperator
	h.oa.treeRefuse = "asset's tree has leaves of operator 22 still held: they name that operator's key, so the operator cannot change while any is"
	session, aid, _ := h.register(genPriv(t), "Issuer", "HN")
	issID := h.createIssuance(session, "Tree Co", "TREX", map[string]any{
		"jurisdictions": map[string]any{"HN": map[string]any{"access": "standard"}},
	})
	r := h.do("POST", "/api/deploy", session, map[string]any{
		"issuance_id": issID, "supply": 1000, "precision": 2, "enforcement": "tree",
	})
	if r.code != 200 {
		t.Fatalf("deploy = %d %s", r.code, r.raw)
	}
	tree, _ := r.body["tree"].(map[string]any)
	note, _ := tree["note"].(string)
	if tree["enabled"] != false || !strings.Contains(note, "its holdings cannot sit in the issuer's tree yet") || !strings.Contains(note, "the operator cannot change") {
		t.Fatalf("a refused tree must be said so: %v", r.body["tree"])
	}
	iss, _ := h.s.st.IssuanceByID(issID)
	if iss.Status != "live" || iss.TreeOperator != "" {
		t.Fatalf("issuance %q tree %q", iss.Status, iss.TreeOperator)
	}
	assertAuditedM10(t, h.m5h, aid, "deploy.tree_failed")
	// The policy server can now: asked again from the token's page, it is.
	h.oa.treeRefuse = ""
	again := h.do("POST", "/api/issuances/"+issID+"/tree", session, nil)
	if again.code != 200 || again.body["enabled"] != true {
		t.Fatalf("retry = %d %s", again.code, again.raw)
	}
	if iss, _ := h.s.st.IssuanceByID(issID); iss.TreeOperator != testTreeOperator {
		t.Fatalf("retry did not record the tree: %q", iss.TreeOperator)
	}
	// A serviced token has no tree to enable.
	other := h.createIssuance(session, "Plain Co", "PLNX", map[string]any{
		"jurisdictions": map[string]any{"HN": map[string]any{"access": "standard"}},
	})
	if r := h.do("POST", "/api/issuances/"+other+"/tree", session, nil); r.code != 409 {
		t.Fatalf("tree on a draft serviced issuance = %d %s", r.code, r.raw)
	}
}
