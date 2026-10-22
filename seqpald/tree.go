package main

// The fourth enforcement election, "tree": a token SeqPal services exactly as a
// "serviced" one (every transfer co-signed by the policy server against the
// issuer's rules), whose holdings may also sit in the issuer's tree. There a
// transfer between approved holders settles at once, off the chain, after the
// same check; a holder can always take its tokens out to its own account on
// the chain; and the issuer's powers are the ones it has over the token on the
// chain: to refuse a transfer, and, where the token kept the clawback option,
// to take a holding back once it is on the chain.
//
// The deploy mints as a serviced deploy does and then asks the policy server
// to enable the token's tree with this deployment's operator
// (SEQPALD_TREE_OPERATOR). That second step can fail on its own; the token is
// then a serviced one, the answer says so, and POST /api/issuances/{id}/tree
// asks again.

import (
	"net/http"
	"strings"
)

// treeStatus is what a deploy or a retry says about the token's tree.
func treeStatus(enabled bool, operator, note string) map[string]any {
	return map[string]any{"enabled": enabled, "operator": operator, "note": note}
}

const treeEnabledNote = "Transfers between approved holders can settle at once in the issuer's tree, checked against the same rules. A holder can always take their tokens out to their own account on the chain."

// enableTree asks the policy server to enable iss's tree with this
// deployment's operator, records it on the issuance, and answers what happened.
func (s *server) enableTree(aid string, iss *Issuance) map[string]any {
	op := s.cfg.treeOperator
	var out map[string]any
	err := s.callOpenAMP("POST", "/v1/issuer/tree", s.cfg.issuerToken,
		map[string]any{"asset": iss.AssetID, "operator": op}, &out)
	if err != nil {
		s.st.Audit(aid, "deploy.tree_failed", map[string]any{"issuance_id": iss.ID, "asset": iss.AssetID, "error": err.Error()})
		return treeStatus(false, "", "The token is live, and its transfers are checked as for the standard option, "+
			"but its holdings cannot sit in the issuer's tree yet: the policy server did not enable it ("+err.Error()+"). "+
			"Ask again from the token's page.")
	}
	if err := s.st.UpdateIssuanceFields(iss.ID, map[string]any{"tree_operator": op}); err != nil {
		return treeStatus(false, "", "The policy server enabled the token's tree, and this platform could not record it: ask again from the token's page.")
	}
	iss.TreeOperator = op
	s.st.Audit(aid, "deploy.tree_enabled", map[string]any{"issuance_id": iss.ID, "asset": iss.AssetID, "operator": op})
	return treeStatus(true, op, treeEnabledNote)
}

// handleIssuanceTree enables a live "tree" issuance's tree when the deploy
// could not, and answers where it stands.
func (s *server) handleIssuanceTree(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	if iss.Enforcement != "tree" {
		writeErr(w, 409, "this token was not issued for the issuer's tree")
		return
	}
	if iss.Status != "live" || iss.AssetID == "" {
		writeErr(w, 409, "this issuance is not live on chain")
		return
	}
	if iss.TreeOperator != "" {
		writeJSON(w, 200, treeStatus(true, iss.TreeOperator, treeEnabledNote))
		return
	}
	if s.cfg.treeOperator == "" {
		writeErr(w, 501, "instant transfers are not available on this deployment")
		return
	}
	if strings.TrimSpace(s.cfg.issuerToken) == "" {
		writeErr(w, 503, "the deployment backend is not configured with an issuer token")
		return
	}
	writeJSON(w, 200, s.enableTree(acct.AID, iss))
}
