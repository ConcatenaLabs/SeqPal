package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// M5 introduces two escrow surfaces seqpald must WRITE to, distinct from the
// read-only chain watcher: a dedicated Sequentia node wallet (seqpal-escrow) for
// USDX deposits and release, reached through the node RPC with a /wallet/<name>
// path, and a dedicated testnet4 wallet (seqpal-btc-escrow) on the parent-chain
// Bitcoin node, reached through a separate RPC endpoint. Both go through the same
// small JSON-RPC helper. seqpald NEVER touches any other wallet on either node.

// jsonRPC issues one JSON-RPC 1.0 call to an arbitrary node endpoint (used for
// the wallet-scoped Sequentia endpoint and for the testnet4 Bitcoin node). It
// mirrors node.go's nodeRPC but is endpoint-parameterized.
func jsonRPC(client *http.Client, url, user, pass, method string, params ...any) (json.RawMessage, error) {
	if params == nil {
		params = []any{}
	}
	reqBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "1.0", "id": "seqpald", "method": method, "params": params,
	})
	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")
	if user != "" {
		httpReq.SetBasicAuth(user, pass)
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := readAllLimited(resp.Body, 8<<20)
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
		return nil, &rpcError{Code: out.Error.Code, Message: out.Error.Message, Method: method}
	}
	return out.Result, nil
}

// rpcError carries the node's error code so idempotent create/load-wallet logic
// can distinguish "already loaded" (a benign condition) from a real failure.
type rpcError struct {
	Code    int
	Message string
	Method  string
}

func (e *rpcError) Error() string { return fmt.Sprintf("%s: %s", e.Method, e.Message) }

func rpcCode(err error) int {
	if re, ok := err.(*rpcError); ok {
		return re.Code
	}
	return 0
}

// walletRPC calls a wallet-scoped method on the Sequentia node (seqpal-escrow).
// The node RPC multiplexes wallets by URL path (<base>/wallet/<name>), exactly as
// elements-cli -rpcwallet does under the hood.
func (s *server) walletRPC(wallet, method string, params ...any) (json.RawMessage, error) {
	if s.cfg.nodeURL == "" {
		return nil, fmt.Errorf("no Sequentia node RPC configured")
	}
	url := strings.TrimRight(s.cfg.nodeURL, "/") + "/wallet/" + wallet
	client := &http.Client{Timeout: 30 * time.Second}
	return jsonRPC(client, url, s.cfg.nodeUser, s.cfg.nodePass, method, params...)
}

// btcRPC calls a method on the parent-chain testnet4 Bitcoin node. wallet is
// empty for node-level calls (createwallet/loadwallet) and set to the wallet name
// for wallet-scoped calls (getnewaddress/listunspent/sendtoaddress).
func (s *server) btcRPC(wallet, method string, params ...any) (json.RawMessage, error) {
	if s.cfg.btcURL == "" {
		return nil, fmt.Errorf("no testnet4 BTC RPC configured (set SEQPALD_BTC_RPC_URL)")
	}
	url := strings.TrimRight(s.cfg.btcURL, "/")
	if wallet != "" {
		url += "/wallet/" + wallet
	}
	client := &http.Client{Timeout: 30 * time.Second}
	return jsonRPC(client, url, s.cfg.btcUser, s.cfg.btcPass, method, params...)
}
