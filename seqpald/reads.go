package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
)

// Owner-scoped reads. Both 403 unless the session owns the issuance: the cap
// table and the issuance's slice of the transparency log are the issuer's view,
// not public. The public transparency log itself remains available unfiltered at
// openampd's /v1/log for independent verification.

// handleIssuanceHolders proxies openampd GET /v1/issuer/holders?asset= : the
// register / cap table, confirmed enclave balances per AID, in atoms. This is
// the only truthful source for "held" (a real on-chain balance > 0); no surface
// may render a holder from localStorage.
func (s *server) handleIssuanceHolders(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	if iss.AssetID == "" {
		writeErr(w, 409, "this issuance is not yet deployed on chain")
		return
	}
	var holders json.RawMessage
	if err := s.callOpenAMP("GET", "/v1/issuer/holders?asset="+iss.AssetID, s.cfg.issuerToken, nil, &holders); err != nil {
		writeErr(w, 502, "could not read the holder register: %v", err)
		return
	}
	s.st.Audit(acct.AID, "issuance.holders.read", map[string]any{"issuance_id": iss.ID, "asset": iss.AssetID})
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(200)
	w.Write(holders)
}

// logEntry mirrors openampd's transparency-log line. The SPA recomputes each
// entry's hash as sha256("<seq>|<prev>|<time>|<action>|<data>") to verify it
// client-side, and links each anchor entry's txid to the explorer.
type logEntry struct {
	Seq    uint64          `json:"seq"`
	Prev   string          `json:"prev"`
	Time   string          `json:"time"`
	Action string          `json:"action"`
	Data   json.RawMessage `json:"data"`
	Hash   string          `json:"hash"`
}

// handleIssuanceLog proxies the slice of openampd's /v1/log relevant to this
// issuance: every entry that names the asset id or its issuance txid, plus every
// log-head anchor entry (so the anchor txids that commit the log on-chain are
// surfaced). The full contiguous chain, for end-to-end hash-chain verification,
// remains at the public /openamp/v1/log, returned as log_url.
func (s *server) handleIssuanceLog(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	req, err := http.NewRequest("GET", s.cfg.openampURL+"/v1/log", nil)
	if err != nil {
		writeErr(w, 500, "internal error")
		return
	}
	resp, err := s.http.Do(req)
	if err != nil {
		writeErr(w, 502, "could not read the transparency log: %v", err)
		return
	}
	defer resp.Body.Close()

	needle := []byte(iss.AssetID)
	txid := []byte(iss.Txid)
	entries := []logEntry{}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e logEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		keep := e.Action == "anchor"
		if !keep && iss.AssetID != "" && bytes.Contains(e.Data, needle) {
			keep = true
		}
		if !keep && iss.Txid != "" && bytes.Contains(e.Data, txid) {
			keep = true
		}
		if keep {
			entries = append(entries, e)
		}
	}
	writeJSON(w, 200, map[string]any{
		"issuance_id": iss.ID,
		"asset":       iss.AssetID,
		"entries":     entries,
		"log_url":     "/openamp/v1/log",
	})
}
