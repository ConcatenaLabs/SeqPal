package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

// M8 listings authorization (contract section 7, integration spec Part 2.2). An
// issuer GRANTS a listing authorization for one of its assets; a venue (SeqDEX,
// the web wallet) READS which assets an issuer authorized for listing. The split
// is the whole point of the SeqDEX handover: a venue can CHECK eligibility
// (/api/eligibility) and read listing authorization (/api/listings), but it can
// never GRANT eligibility, which is stamped by SeqPal ID alone. GET /api/listings
// is public; the grant is owner-scoped.

const listingTag = "seqpal-listing-v1"

// listingStatement is the canonical bytes an issuer optionally signs to grant a
// listing authorization.
func listingStatement(assetID string, authorized bool, venues []string) []byte {
	stmt, _ := canonicalJSON(map[string]any{
		"role": "listing", "asset": assetID, "authorized": authorized, "venues": venues,
	})
	return stmt
}

type listingReq struct {
	Authorized  bool     `json:"authorized"`
	Venues      []string `json:"venues"`
	Signature   string   `json:"signature"`
	SignerXOnly string   `json:"signer_xonly"`
}

// handleGrantListing is POST /api/issuances/{id}/listing (owner): grant or revoke
// listing authorization for the issuance's asset. An optional signature by the
// issuer's own key is recorded when present. The grant only authorizes listing; it
// grants no eligibility.
func (s *server) handleGrantListing(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	if iss.Status != "live" || iss.AssetID == "" {
		writeErr(w, 409, "this issuance is not live on chain")
		return
	}
	var req listingReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	venues := make([]string, 0, len(req.Venues))
	for _, v := range req.Venues {
		if v = strings.TrimSpace(v); v != "" {
			venues = append(venues, v)
		}
	}
	l := &Listing{
		AssetID: iss.AssetID, IssuanceID: iss.ID, IssuerAID: acct.AID, Ticker: iss.Ticker, Name: iss.Name,
		Authorized: req.Authorized,
	}
	l.Venues, _ = json.Marshal(venues)
	if sig := strings.TrimSpace(req.Signature); sig != "" {
		signer := strings.TrimSpace(req.SignerXOnly)
		if signer == "" {
			signer = acct.XOnly
		}
		if signer != acct.XOnly {
			writeErr(w, 403, "the listing authorization must be signed by the issuer's own key")
			return
		}
		if err := verifyTaggedByKey(signer, listingTag, listingStatement(iss.AssetID, req.Authorized, venues), sig); err != nil {
			writeErr(w, 400, "the listing signature does not verify for the issuer key")
			return
		}
		l.Signature, l.SignerXOnly = sig, signer
	}
	if err := s.st.UpsertListing(l); err != nil {
		writeErr(w, 500, "store error")
		return
	}
	s.st.Audit(acct.AID, "listing.grant", map[string]any{
		"issuance_id": iss.ID, "asset": iss.AssetID, "authorized": req.Authorized, "venues": venues,
	})
	writeJSON(w, 200, map[string]any{
		"listing": l,
		"note":    "listing authorization only; a venue may list this asset but eligibility is stamped by SeqPal ID, never granted by a venue",
	})
}

// handleListings is GET /api/listings (public, serves the SeqDEX handover). With
// ?asset=<id> it returns that asset's authorization (or a not-authorized stub);
// otherwise it returns every authorized listing, optionally filtered by ?issuer.
// A venue reads this to learn which assets an issuer authorized for listing.
func (s *server) handleListings(w http.ResponseWriter, r *http.Request) {
	asset := strings.TrimSpace(r.URL.Query().Get("asset"))
	issuer := strings.TrimSpace(r.URL.Query().Get("issuer"))
	if asset != "" {
		l, err := s.st.ListingByAsset(asset)
		if err != nil {
			writeErr(w, 500, "store error")
			return
		}
		if l == nil || !l.Authorized {
			writeJSON(w, 200, map[string]any{"asset": asset, "authorized": false,
				"note": "no issuer listing authorization on record; a venue can check eligibility separately at /api/eligibility"})
			return
		}
		out := map[string]any{"listing": l, "authorized": true}
		// For a token whose rules the network enforces, a venue's real question is
		// answered by the PUBLISHED HOLDER LIST rather than by anything this platform
		// stamps, so the read carries that list's version and commitment. A venue can
		// fetch the same document itself and reach the same answer, which is the point.
		if iss, _ := s.st.IssuanceByAsset(asset); networkEnforced(iss) {
			out["enforcement"] = "network"
			out["eligibility_source"] = "the published holder list for this token, which the network itself checks on every transfer"
			out["max_coins_per_transfer"] = maxCoinsPerTransfer
			var pol publishedPolicy
			if err := s.callOpenAMP("GET", "/v1/snapshots?asset="+asset, "", nil, &pol); err == nil {
				out["policy"] = map[string]any{
					"list_version":      pol.Seq,
					"commitment":        pol.Pi,
					"holder_count":      len(pol.Snapshot.Predicates.Whitelist.Entries),
					"frozen_coin_count": len(pol.Snapshot.Predicates.Blacklist.Entries),
				}
			}
		}
		writeJSON(w, 200, out)
		return
	}
	listings, err := s.st.AuthorizedListings(issuer)
	if err != nil {
		writeErr(w, 500, "store error")
		return
	}
	writeJSON(w, 200, map[string]any{
		"listings": listings,
		"note":     "issuer-granted listing authorizations; eligibility is checked at /api/eligibility and can never be granted by a venue",
	})
}
