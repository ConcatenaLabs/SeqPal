package main

import (
	"net/http"
	"strings"
)

// Restricted assets stay on the chain: an issuance is never held in an Ark
// tree. Holdings and transfers of a serviced token are in enclaves the policy
// server co-signs, and trades go through the swap HTLC, an asset-for-asset
// trade or a gated order. The "tree" election that once put a serviced token's
// holdings in the issuer's tree is refused for a new deploy and read as
// "serviced" wherever a stored one is read back.

// enclaveRetired is the sentence every refusal of the tree names, the same
// one the policy server answers with.
const enclaveRetired = "enclave-1 was retired: restricted assets stay on the chain"

// treeRetired is the refusal of a deploy that elects "tree".
const treeRetired = enclaveRetired + `; choose "serviced", whose transfers the policy server co-signs on the chain`

// readEnforcement is a stored or submitted election as it is read back: lower
// case and trimmed, with "tree" read as "serviced" (what a tree token was on
// the chain all along).
func readEnforcement(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "tree" {
		return "serviced"
	}
	return v
}

// handleTreeRetired answers the old POST /api/issuances/{id}/tree with 410 Gone,
// so a page still calling it gets a named refusal rather than a 404.
func handleTreeRetired(w http.ResponseWriter, r *http.Request) {
	writeErr(w, http.StatusGone, enclaveRetired)
}

// treeOperatorNote is the one log line a set SEQPALD_TREE_OPERATOR gets: it is
// accepted and ignored, so an environment file that still sets it starts.
func treeOperatorNote(v string) string {
	if strings.TrimSpace(v) == "" {
		return ""
	}
	return "SEQPALD_TREE_OPERATOR is ignored: " + enclaveRetired
}
