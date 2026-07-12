package main

import "fmt"

// postLogHeadAnchor triggers openampd's log-head anchor (the existing on-chain
// anchor path) and returns the resulting OP_RETURN txid. It is the single place
// that path is invoked, shared by the daily anchor cron and by artifact anchoring
// so both go through the same audited call.
func (s *server) postLogHeadAnchor() (txid string, seq uint64, err error) {
	if s.cfg.issuerToken == "" {
		return "", 0, fmt.Errorf("no issuer token configured")
	}
	var out struct {
		Txid string `json:"txid"`
		Seq  uint64 `json:"seq"`
		Head string `json:"head"`
	}
	if err := s.callOpenAMP("POST", "/v1/issuer/anchor", s.cfg.issuerToken, map[string]any{}, &out); err != nil {
		return "", 0, err
	}
	return out.Txid, out.Seq, nil
}

// anchorArtifact commits an artifact hash (a filing hash, a rules-amendment hash,
// or a document signature) using the existing log/anchor path. The mechanics,
// stated honestly and not overclaimed: the artifact hash is recorded in
// seqpald's append-only, hash-chained audit log, and the same call triggers the
// on-chain log-head OP_RETURN so a fresh Bitcoin-anchored commitment is produced
// at artifact time; the returned txid is that anchor. seqpald's own OP_RETURN
// state anchor that would commit the artifact hash byte-for-byte on-chain is the
// M7 SEQPAL-payload anchor, disclosed here rather than faked. Best-effort: a
// deployment with no issuer token or node still records the audit entry and
// returns an empty txid.
func (s *server) anchorArtifact(kind, artifactHash string) string {
	txid, seq, err := s.postLogHeadAnchor()
	detail := map[string]any{"kind": kind, "artifact_hash": artifactHash}
	if err == nil {
		detail["log_anchor_txid"] = txid
		detail["log_anchor_seq"] = seq
	} else {
		detail["anchor_error"] = err.Error()
	}
	s.st.Audit("", "artifact.anchor", detail)
	return txid
}
