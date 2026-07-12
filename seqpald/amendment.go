package main

import (
	"fmt"
	"html"
	"strings"
	"time"
)

// The rules-amendment generator produces the content-addressed artifact that
// keeps the on-chain commitment truthful after the first policy-rules change: an
// asset's genesis terms_hash plus an anchored chain of amendments, each recording
// the prior rules hash, the new rules hash, the basis, and the effective height.
// The mutation paths that actually call POST /v1/issuer/rules are M7; M4 provides
// the generator and the anchored-chain rendering data for the Verify explainer.
// generateAmendment therefore produces and anchors the ARTIFACT; it does not
// itself change any policy rule.

// rulesHash is the content address of a compiled rules object: sha256 over the
// canonical JSON. It is what an amendment records for prior and new rules so the
// chain is verifiable.
func rulesHash(r CompiledRules) (string, error) {
	canon, err := canonicalJSON(r)
	if err != nil {
		return "", err
	}
	return sha256Hex(canon), nil
}

// generateAmendment builds, stores, and anchors a rules-amendment artifact for an
// issuance. The artifact bytes are deterministic given the inputs and the
// position in the chain, so the amend_hash is a stable content address. The
// artifact is stored in the document store (always public: amendments are
// post-issuance and never offer-gated), the metadata joins the amendment chain,
// and the hash is anchored via the existing log/anchor path.
func (s *server) generateAmendment(iss *Issuance, priorRulesHash, newRulesHash, basis string, effectiveHeight int64) (*Amendment, error) {
	count, err := s.st.AmendmentCount(iss.ID)
	if err != nil {
		return nil, err
	}
	ord := int64(count + 1)
	title, body := amendmentDoc(iss, priorRulesHash, newRulesHash, basis, effectiveHeight, ord)
	amendHash := sha256Hex([]byte(body))
	now := time.Now().Unix()
	if err := s.st.PutDocument(&StoredDoc{
		DocHash: amendHash, IssuanceID: iss.ID, Kind: "rules-amendment", Title: title,
		ContentType: "text/html; charset=utf-8", AlwaysPublic: true, Body: []byte(body), CreatedAt: now,
	}); err != nil {
		return nil, err
	}
	anchorTxid := s.anchorArtifact("rules-amendment", amendHash)
	a := &Amendment{
		AmendHash: amendHash, IssuanceID: iss.ID, AssetID: iss.AssetID, Ord: ord,
		PriorRulesHash: priorRulesHash, NewRulesHash: newRulesHash, Basis: basis,
		EffectiveHeight: effectiveHeight, AnchorTxid: anchorTxid, CreatedAt: now,
	}
	if err := s.st.InsertAmendment(a); err != nil {
		return nil, err
	}
	s.st.Audit(iss.OwnerAID, "rules.amendment.generate", map[string]any{
		"issuance_id": iss.ID, "asset": iss.AssetID, "ord": ord, "amend_hash": amendHash,
		"prior_rules_hash": priorRulesHash, "new_rules_hash": newRulesHash,
		"basis": basis, "effective_height": effectiveHeight, "anchor_txid": anchorTxid,
	})
	return a, nil
}

func amendmentDoc(iss *Issuance, priorRulesHash, newRulesHash, basis string, effectiveHeight, ord int64) (string, string) {
	title := fmt.Sprintf("Rules Amendment %d: %s (%s)", ord, iss.Name, iss.Ticker)
	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>")
	b.WriteString(para(fmt.Sprintf("This artifact records amendment %d to the policy rules of the Sequentia network asset %s.", ord, iss.Ticker)))
	b.WriteString("<table border=\"1\" cellpadding=\"4\"><tbody>")
	b.WriteString("<tr><th>Amendment index</th><td>" + fmt.Sprintf("%d", ord) + "</td></tr>")
	b.WriteString("<tr><th>Asset</th><td>" + html.EscapeString(iss.AssetID) + "</td></tr>")
	b.WriteString("<tr><th>Prior rules hash</th><td>" + html.EscapeString(priorRulesHash) + "</td></tr>")
	b.WriteString("<tr><th>New rules hash</th><td>" + html.EscapeString(newRulesHash) + "</td></tr>")
	b.WriteString("<tr><th>Effective height</th><td>Sequentia block " + fmt.Sprintf("%d", effectiveHeight) + "</td></tr>")
	b.WriteString("<tr><th>Basis</th><td>" + html.EscapeString(basis) + "</td></tr>")
	b.WriteString("</tbody></table>")
	b.WriteString(para("The on-chain commitment remains the genesis terms_hash plus this anchored amendment chain. The effective height, a Sequentia block height and not a calendar date, is binding."))
	return title, docPage("rules-amendment", title, b.String())
}
