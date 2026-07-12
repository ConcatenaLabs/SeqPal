package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// tipHeight returns the current Sequentia block height, used to turn a relative
// lockup or Reg S period into an absolute block height at compile time. It calls
// the node's getblockcount when a node RPC is configured; otherwise it returns
// the configured fallback tip (SEQPALD_TIP_HEIGHT).
func (s *server) tipHeight() int64 {
	if s.cfg.nodeURL == "" {
		return s.cfg.assumedTip
	}
	h, err := s.nodeBlockCount()
	if err != nil {
		return s.cfg.assumedTip
	}
	return h
}

// nodeRPC issues one JSON-RPC call to the configured Sequentia node and returns
// the raw `result`. Errors from the node (non-nil `error`) and HTTP failures are
// both surfaced. The node RPC is read-only from seqpald's point of view: the
// watcher only ever queries chain state, it never asks the node to sign or move
// anything.
func (s *server) nodeRPC(method string, params ...any) (json.RawMessage, error) {
	if params == nil {
		params = []any{}
	}
	reqBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "1.0", "id": "seqpald", "method": method, "params": params,
	})
	httpReq, err := http.NewRequest("POST", s.cfg.nodeURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")
	if s.cfg.nodeUser != "" {
		httpReq.SetBasicAuth(s.cfg.nodeUser, s.cfg.nodePass)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := readAllLimited(resp.Body, 4<<20)
	var out struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("%s -> HTTP %d", method, resp.StatusCode)
		}
		return nil, err
	}
	if out.Error != nil {
		return nil, fmt.Errorf("%s: %s", method, out.Error.Message)
	}
	return out.Result, nil
}

func (s *server) nodeBlockCount() (int64, error) {
	res, err := s.nodeRPC("getblockcount")
	if err != nil {
		return 0, err
	}
	var h int64
	if err := json.Unmarshal(res, &h); err != nil {
		return 0, err
	}
	return h, nil
}

// anchorStatus is the node's view of its parent-chain (Bitcoin) anchor at the
// tip: getanchorstatus (see doc/sequentia/03-bitcoin-anchoring.md). The tip's
// anchorheight is the most recent Bitcoin height the chain has anchored to, so
// it is the reference the watcher measures a confirming block's anchor depth
// against without needing a separate Bitcoin RPC. The call fails on a chain
// where con_bitcoin_anchor is disabled; the watcher degrades to confirmations
// only in that case and never claims a block is anchored.
type nodeAnchorStatus struct {
	ValidateAnchor bool   `json:"validateanchor"`
	TipHeight      int64  `json:"tipheight"`
	AnchorHeight   int64  `json:"anchorheight"`
	AnchorHash     string `json:"anchorhash"`
	AnchorStatus   string `json:"anchorstatus"`
}

func (s *server) anchorStatus() (*nodeAnchorStatus, error) {
	res, err := s.nodeRPC("getanchorstatus")
	if err != nil {
		return nil, err
	}
	var a nodeAnchorStatus
	if err := json.Unmarshal(res, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// nodeRawTx is the subset of getrawtransaction (verbose) the watcher reads to
// discover which block confirms an issuance txid. Discovery requires the node to
// index transactions (-txindex, which the box already runs for its explorer);
// the reorg re-check below uses getblockheader by hash and needs no index.
type nodeRawTx struct {
	Txid          string `json:"txid"`
	Confirmations int64  `json:"confirmations"`
	BlockHash     string `json:"blockhash"`
	InActiveChain *bool  `json:"in_active_chain"`
}

func (s *server) rawTransaction(txid string) (*nodeRawTx, error) {
	res, err := s.nodeRPC("getrawtransaction", txid, true)
	if err != nil {
		return nil, err
	}
	var tx nodeRawTx
	if err := json.Unmarshal(res, &tx); err != nil {
		return nil, err
	}
	return &tx, nil
}

// nodeBlockHeader is the subset of getblockheader the watcher reads. A stale
// (reorged-out) block returns confirmations < 0, which is exactly how the
// watcher detects a Bitcoin-driven regression. anchorheight/anchorhash are
// present only on a chain with con_bitcoin_anchor enabled.
type nodeBlockHeader struct {
	Hash          string `json:"hash"`
	Confirmations int64  `json:"confirmations"`
	Height        int64  `json:"height"`
	AnchorHeight  int64  `json:"anchorheight"`
	AnchorHash    string `json:"anchorhash"`
	PosCertified  bool   `json:"poscertified"`
}

func (s *server) blockHeader(hash string) (*nodeBlockHeader, error) {
	res, err := s.nodeRPC("getblockheader", hash, true)
	if err != nil {
		return nil, err
	}
	var h nodeBlockHeader
	if err := json.Unmarshal(res, &h); err != nil {
		return nil, err
	}
	return &h, nil
}
