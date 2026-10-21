package main

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"sort"
	"strings"
)

// The fee asset of a transaction a platform wallet funds.
//
// Sequentia's fee market is open: a transaction pays its fee in exactly one
// asset, which each node values at broadcast through its own exchange-rate
// table (getfeeexchangerates), and no asset is the default or a fallback.
// Every wallet chooses the fee asset by the same rule, and seqpald does too:
//
//  1. the asset the transaction moves, when the node accepts it for fees;
//  2. otherwise, among the assets the node accepts, the one whose largest coin
//     in the paying wallet covers the most fees: the coin's atoms valued at the
//     node's rate, so the coin worth most in the reference unit wins. No asset
//     is preferred for what it is, the policy asset included; a tie goes to
//     the lower asset id, which says nothing about either asset.
//
// SEQPALD_FEE_ASSET, where a deployment sets it, is the operator's own choice
// of asset and is used as long as the node accepts it; a deployment that names
// an asset the node does not accept is refused rather than quietly served by
// another.

// feeCoin is one spendable coin of a wallet, as listunspent reports it.
type feeCoin struct {
	Asset string
	Atoms uint64
}

// pickFeeAsset applies the rule to a node's rates (asset id -> rate, the
// node's integer rate where 1e8 is one reference unit per whole coin) and a
// wallet's coins. `moved` is the asset the transaction moves, or empty.
func pickFeeAsset(moved string, rates map[string]uint64, coins []feeCoin) (string, error) {
	moved = strings.ToLower(moved)
	if moved != "" && rates[moved] > 0 {
		return moved, nil
	}
	largest := map[string]uint64{}
	for _, c := range coins {
		a := strings.ToLower(c.Asset)
		if rates[a] == 0 || c.Atoms == 0 {
			continue
		}
		if c.Atoms > largest[a] {
			largest[a] = c.Atoms
		}
	}
	if len(largest) == 0 {
		accepted := make([]string, 0, len(rates))
		for a, r := range rates {
			if r > 0 {
				accepted = append(accepted, a)
			}
		}
		sort.Strings(accepted)
		what := "the asset it moves is not one the node accepts for fees, and"
		if moved == "" {
			what = "it moves no asset of its own, and"
		}
		return "", fmt.Errorf("no fee asset: %s the paying wallet holds no coin of an asset the node accepts (%s)",
			what, strings.Join(accepted, ", "))
	}
	assets := make([]string, 0, len(largest))
	for a := range largest {
		assets = append(assets, a)
	}
	sort.Strings(assets)
	best, bestWorth := "", new(big.Int)
	for _, a := range assets {
		worth := new(big.Int).Mul(new(big.Int).SetUint64(largest[a]), new(big.Int).SetUint64(rates[a]))
		if best == "" || worth.Cmp(bestWorth) > 0 {
			best, bestWorth = a, worth
		}
	}
	return best, nil
}

// feeRates is the node's fee exchange-rate table, by lowercase asset id.
func (s *server) feeRates() (map[string]uint64, error) {
	res, err := s.nodeRPC("getfeeexchangerates")
	if err != nil {
		return nil, fmt.Errorf("getfeeexchangerates: %w", err)
	}
	var raw map[string]json.Number
	if err := json.Unmarshal(res, &raw); err != nil {
		return nil, fmt.Errorf("getfeeexchangerates: %w", err)
	}
	out := make(map[string]uint64, len(raw))
	for a, r := range raw {
		n, ok := new(big.Int).SetString(r.String(), 10)
		if !ok || n.Sign() <= 0 || !n.IsUint64() {
			continue
		}
		out[strings.ToLower(a)] = n.Uint64()
	}
	// The node keys an asset it has a label for by that label (the policy
	// asset is "bitcoin"), and a wallet's coins by asset id: read the labels so
	// that every accepted asset is compared by its id, the policy asset too.
	labelled := false
	for a := range out {
		if !isAssetID(a) {
			labelled = true
			break
		}
	}
	if !labelled {
		return out, nil
	}
	lres, err := s.nodeRPC("dumpassetlabels")
	if err != nil {
		return nil, fmt.Errorf("dumpassetlabels: %w", err)
	}
	var labels map[string]string
	if err := json.Unmarshal(lres, &labels); err != nil {
		return nil, fmt.Errorf("dumpassetlabels: %w", err)
	}
	return ratesByAssetID(out, labels), nil
}

// ratesByAssetID re-keys a rate table whose keys may be labels to asset ids,
// using the node's labels. A label the node cannot resolve is dropped: no
// coin could match it.
func ratesByAssetID(rates map[string]uint64, labels map[string]string) map[string]uint64 {
	byLabel := make(map[string]string, len(labels))
	for l, id := range labels {
		byLabel[strings.ToLower(l)] = strings.ToLower(id)
	}
	out := make(map[string]uint64, len(rates))
	for k, r := range rates {
		if isAssetID(k) {
			out[k] = r
		} else if id, ok := byLabel[k]; ok && isAssetID(id) {
			out[id] = r
		}
	}
	return out
}

func isAssetID(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// walletFeeCoins lists a wallet's spendable coins for the rule.
func (s *server) walletFeeCoins(wallet string) ([]feeCoin, error) {
	res, err := s.walletRPC(wallet, "listunspent", 0)
	if err != nil {
		return nil, fmt.Errorf("listunspent: %w", err)
	}
	var utxos []struct {
		Asset     string      `json:"asset"`
		Amount    json.Number `json:"amount"`
		Spendable *bool       `json:"spendable"`
	}
	if err := json.Unmarshal(res, &utxos); err != nil {
		return nil, fmt.Errorf("listunspent: %w", err)
	}
	out := make([]feeCoin, 0, len(utxos))
	for _, u := range utxos {
		if u.Spendable != nil && !*u.Spendable {
			continue
		}
		out = append(out, feeCoin{Asset: u.Asset, Atoms: atomsFromNumber(u.Amount)})
	}
	return out, nil
}

// feeAssetFor names the asset a transaction funded by `wallet` pays its fee
// in, by the rule above. `moved` is the asset it moves, or empty.
func (s *server) feeAssetFor(wallet, moved string) (string, error) {
	rates, err := s.feeRates()
	if err != nil {
		return "", err
	}
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("SEQPALD_FEE_ASSET"))); v != "" {
		if rates[v] == 0 {
			return "", fmt.Errorf("SEQPALD_FEE_ASSET is %s, which this node does not accept for fees", v)
		}
		return v, nil
	}
	if moved != "" && rates[strings.ToLower(moved)] > 0 {
		return strings.ToLower(moved), nil
	}
	coins, err := s.walletFeeCoins(wallet)
	if err != nil {
		return "", err
	}
	return pickFeeAsset(moved, rates, coins)
}
