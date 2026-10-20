package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
)

// What the escrow covenant needs from the Sequentia node. None of it touches a
// wallet: the coins at an escrow address are read from the UTXO set
// (scantxoutset with raw() descriptors, gettxout on a known outpoint), their
// confirmations from their height, and a finished spend goes out through
// testmempoolaccept and sendrawtransaction.

// addressSPK is an address's scriptPubKey, as the node reads it on this chain.
func (s *server) addressSPK(addr string) (string, error) {
	res, err := s.nodeRPC("validateaddress", strings.TrimSpace(addr))
	if err != nil {
		return "", err
	}
	var v struct {
		IsValid      bool   `json:"isvalid"`
		ScriptPubKey string `json:"scriptPubKey"`
	}
	if err := json.Unmarshal(res, &v); err != nil {
		return "", err
	}
	if !v.IsValid || v.ScriptPubKey == "" {
		return "", fmt.Errorf("the address is not valid on this network")
	}
	return strings.ToLower(v.ScriptPubKey), nil
}

// scanEntry is one scantxoutset unspent.
type scanEntry struct {
	Txid         string      `json:"txid"`
	Vout         uint32      `json:"vout"`
	ScriptPubKey string      `json:"scriptPubKey"`
	Amount       json.Number `json:"amount"`
	Asset        string      `json:"asset"`
	Height       int64       `json:"height"`
}

// scanMu serialises this process's scans: the node runs one scantxoutset at a
// time and refuses a second while the first is running.
var scanMu sync.Mutex

// scanSPKs reads every unspent output paying any of the given scriptPubKeys
// from the node's UTXO set, which holds confirmed coins only. It returns them
// by scriptPubKey with the height the scan ran at. Only explicit coins are
// returned: a confidential output carries no amount or asset to read, and the
// covenant's leaves cannot return one either.
func (s *server) scanSPKs(spks []string) (map[string][]scanEntry, int64, error) {
	out := map[string][]scanEntry{}
	if len(spks) == 0 {
		return out, 0, nil
	}
	descs := make([]string, 0, len(spks))
	for _, spk := range spks {
		descs = append(descs, "raw("+strings.ToLower(spk)+")")
	}
	scanMu.Lock()
	defer scanMu.Unlock()
	res, err := s.nodeRPC("scantxoutset", "start", descs)
	if err != nil {
		return nil, 0, err
	}
	var r struct {
		Success  bool        `json:"success"`
		Height   int64       `json:"height"`
		Unspents []scanEntry `json:"unspents"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, 0, err
	}
	if !r.Success {
		return nil, 0, fmt.Errorf("scantxoutset did not complete")
	}
	for _, u := range r.Unspents {
		if u.Asset == "" || u.Amount == "" {
			continue
		}
		k := strings.ToLower(u.ScriptPubKey)
		out[k] = append(out[k], u)
	}
	return out, r.Height, nil
}

// txOut is what gettxout reports about one outpoint.
type txOut struct {
	Confirmations int64       `json:"confirmations"`
	Value         json.Number `json:"value"`
	Asset         string      `json:"asset"`
	ScriptPubKey  struct {
		Hex string `json:"hex"`
	} `json:"scriptPubKey"`
}

// unspentOut reports an outpoint that is still unspent (mempool spends count
// as spent), or nil when it is not.
func (s *server) unspentOut(txid string, vout uint32) (*txOut, error) {
	res, err := s.nodeRPC("gettxout", txid, vout, true)
	if err != nil {
		return nil, err
	}
	if len(res) == 0 || string(res) == "null" {
		return nil, nil
	}
	var o txOut
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	return &o, nil
}

// chainTip is the tip height and its median time past, what a lock time is
// measured against.
func (s *server) chainTip() (int64, int64, error) {
	res, err := s.nodeRPC("getblockchaininfo")
	if err != nil {
		return 0, 0, err
	}
	var v struct {
		Blocks     int64 `json:"blocks"`
		MedianTime int64 `json:"mediantime"`
	}
	if err := json.Unmarshal(res, &v); err != nil {
		return 0, 0, err
	}
	return v.Blocks, v.MedianTime, nil
}

var genesisCache struct {
	mu sync.Mutex
	h  [32]byte
	ok bool
}

// genesisHash is the chain's genesis block hash in internal byte order: an
// Elements taproot sighash commits to it, which is what stops a signature
// made on one chain from being replayed on another.
func (s *server) genesisHash() ([32]byte, error) {
	genesisCache.mu.Lock()
	defer genesisCache.mu.Unlock()
	if genesisCache.ok {
		return genesisCache.h, nil
	}
	res, err := s.nodeRPC("getblockhash", 0)
	if err != nil {
		return [32]byte{}, err
	}
	var display string
	if err := json.Unmarshal(res, &display); err != nil {
		return [32]byte{}, err
	}
	b, err := hex.DecodeString(display)
	if err != nil || len(b) != 32 {
		return [32]byte{}, fmt.Errorf("getblockhash returned %q", display)
	}
	var h [32]byte
	for i := 0; i < 32; i++ {
		h[i] = b[31-i]
	}
	genesisCache.h, genesisCache.ok = h, true
	return h, nil
}

// feeRateAtomsPerKvB is the network fee rate in the fee asset's OWN atoms per
// 1000 vbytes, as this node values that asset. An asset the node does not
// accept for fees is refused: the open fee market has no default asset, and a
// spend never falls back to paying in another one.
func (s *server) feeRateAtomsPerKvB(asset string) (uint64, error) {
	res, err := s.nodeRPC("estimatesmartfee", 6, "CONSERVATIVE", asset)
	if err != nil {
		return 0, fmt.Errorf("estimatesmartfee: %w", err)
	}
	var est struct {
		Accepted        *bool       `json:"accepted"`
		Rate            json.Number `json:"rate"`
		AssetAtomsPerKvB json.Number `json:"asset_atoms_per_kvb"`
	}
	if err := json.Unmarshal(res, &est); err != nil {
		return 0, err
	}
	if est.Accepted != nil && !*est.Accepted {
		return 0, fmt.Errorf("this node does not accept fees in %s", asset)
	}
	if v, err := est.AssetAtomsPerKvB.Float64(); err == nil && v > 0 {
		return uint64(math.Ceil(v)), nil
	}
	// No estimate yet (a quiet chain has no fee history): the node's own
	// floor, in the reference unit, converted at the node's rate for this
	// asset -- ceil(rfa * 1e8 / rate) -- and doubled for headroom.
	rate, err := est.Rate.Float64()
	if err != nil || rate <= 0 {
		rate, err = s.feeExchangeRate(asset)
		if err != nil {
			return 0, err
		}
	}
	mres, err := s.nodeRPC("getmempoolinfo")
	if err != nil {
		return 0, err
	}
	var mi struct {
		MempoolMinFee float64 `json:"mempoolminfee"`
		MinRelayFee   float64 `json:"minrelaytxfee"`
	}
	if err := json.Unmarshal(mres, &mi); err != nil {
		return 0, err
	}
	floor := math.Max(mi.MempoolMinFee, mi.MinRelayFee) // reference units per kvB
	rfa := floor * 1e8
	atoms := math.Ceil(rfa*1e8/rate) * 2
	if atoms < 1 {
		atoms = 1
	}
	return uint64(atoms), nil
}

// feeExchangeRate is the node's whitelist rate for an asset, refusing one the
// node does not accept.
func (s *server) feeExchangeRate(asset string) (float64, error) {
	res, err := s.nodeRPC("getfeeexchangerates")
	if err != nil {
		return 0, err
	}
	var rates map[string]float64
	if err := json.Unmarshal(res, &rates); err != nil {
		return 0, err
	}
	r, ok := rates[strings.ToLower(asset)]
	if !ok || r <= 0 {
		return 0, fmt.Errorf("this node does not accept fees in %s", asset)
	}
	return r, nil
}

// nodeRefusal is the node's own answer that it will not take a transaction:
// definitive, unlike an error reaching it, after which the transaction may or
// may not have gone out and must be treated as possibly broadcast.
type nodeRefusal struct{ reason string }

func (e *nodeRefusal) Error() string { return "the node refuses the transaction: " + e.reason }

func isRefusal(err error) bool {
	_, ok := err.(*nodeRefusal)
	return ok
}

// broadcastRaw checks a finished transaction against the node's own rules
// and broadcasts it. Broadcasting the same bytes twice is harmless -- it is
// one transaction with one txid -- so a node that already has it, in its
// mempool or in a block, counts as a success: that is what lets a lost write
// be healed by sending the same transaction again.
func (s *server) broadcastRaw(txHex string) (string, error) {
	res, err := s.nodeRPC("testmempoolaccept", []string{txHex})
	if err != nil {
		return "", err
	}
	var acc []struct {
		Txid         string `json:"txid"`
		Allowed      bool   `json:"allowed"`
		RejectReason string `json:"reject-reason"`
	}
	if err := json.Unmarshal(res, &acc); err != nil || len(acc) != 1 {
		return "", fmt.Errorf("testmempoolaccept: unreadable answer")
	}
	if !acc[0].Allowed {
		reason := acc[0].RejectReason
		if alreadyBroadcast(reason) {
			return acc[0].Txid, nil
		}
		return "", &nodeRefusal{reason: reason}
	}
	res, err = s.nodeRPC("sendrawtransaction", txHex)
	if err != nil {
		if alreadyBroadcast(err.Error()) {
			return acc[0].Txid, nil
		}
		if rpcCode(err) != 0 {
			return "", &nodeRefusal{reason: err.Error()}
		}
		return "", err
	}
	var txid string
	if err := json.Unmarshal(res, &txid); err != nil {
		return "", err
	}
	return txid, nil
}

func alreadyBroadcast(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "already in block chain") || strings.Contains(m, "txn-already-in-mempool") ||
		strings.Contains(m, "txn-already-known") || strings.Contains(m, "already known")
}
