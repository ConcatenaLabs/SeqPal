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
// the configured fallback tip (SEQPALD_TIP_HEIGHT). The real chain watcher
// lands in M3; until then this is the height source the compiler uses.
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

func (s *server) nodeBlockCount() (int64, error) {
	reqBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "1.0", "id": "seqpald", "method": "getblockcount", "params": []any{},
	})
	httpReq, err := http.NewRequest("POST", s.cfg.nodeURL, bytes.NewReader(reqBody))
	if err != nil {
		return 0, err
	}
	httpReq.Header.Set("content-type", "application/json")
	if s.cfg.nodeUser != "" {
		httpReq.SetBasicAuth(s.cfg.nodeUser, s.cfg.nodePass)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := readAllLimited(resp.Body, 1<<16)
	if resp.StatusCode >= 400 {
		return 0, fmt.Errorf("getblockcount -> %d", resp.StatusCode)
	}
	var out struct {
		Result int64 `json:"result"`
		Error  any   `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0, err
	}
	if out.Error != nil {
		return 0, fmt.Errorf("getblockcount error: %v", out.Error)
	}
	return out.Result, nil
}
