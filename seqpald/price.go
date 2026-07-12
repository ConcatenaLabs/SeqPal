package main

import (
	"encoding/json"
	"log"
	"math"
	"net/http"
	"strings"
	"time"
)

// The price server is consulted, never modified: its rate calculation is a
// standing invariant of the fee market (the any-asset fee exchange rates are
// correct and must not be inverted or rescaled). seqpald only READS /prices to
// derive a fee-conversion figure, and WRITES a data-only offering-reference
// entry so the new ticker appears in /prices. Neither touches the rate math.

// priceEntry is one asset's row in the market-data feed's /prices response
// (ticker -> {price, market_cap, volume_24h}), the shape mock-price-api.py and a
// real Sequentia price API both serve.
type priceEntry struct {
	Price float64 `json:"price"`
}

// fetchPrices reads the box price feed's /prices and returns upper-cased
// ticker -> price. A feed that is unreachable yields an empty map, never an
// error the caller must handle: price-derived figures degrade to their safe
// fallback rather than blocking a deploy.
func (s *server) fetchPrices() map[string]float64 {
	out := map[string]float64{}
	if s.cfg.priceURL == "" {
		return out
	}
	req, err := http.NewRequest("GET", strings.TrimRight(s.cfg.priceURL, "/")+"/prices", nil)
	if err != nil {
		return out
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return out
	}
	defer resp.Body.Close()
	raw, _ := readAllLimited(resp.Body, 1<<20)
	var feed map[string]priceEntry
	if err := json.Unmarshal(raw, &feed); err != nil {
		return out
	}
	for k, v := range feed {
		out[strings.ToUpper(k)] = v.Price
	}
	return out
}

// seqPrice returns the Sequence token's reference price from the feed, accepting
// either the pricing key (SEQ) or the testnet display ticker (TSEQ). SEQ has no
// privileged standing in the fee market; it is looked up here only because
// fee-value conversion is expressed relative to it, exactly as the price server
// and exchangerates.cpp already do.
func seqPrice(prices map[string]float64) float64 {
	if p := prices["SEQ"]; p > 0 {
		return p
	}
	return prices["TSEQ"]
}

// feeConvertAtoms derives fee_convert_atoms from the price feed instead of a
// hardcoded constant. It applies the canonical, value-preserving relation
// (memory axiom: the any-asset fee rates are correct, never inverted):
//
//	rate            = (asset_price / SEQ_price) * 1e8      // asset value in SEQ-sats
//	fee_convert_atoms = ceil(policy_fee_sats / rate)       // preserves fee value
//
// so a more valuable asset pays FEWER atoms for the same fee value, which is
// correct, not a bug. Returns ok=false when a needed price is missing, so the
// caller can fall back safely.
func (s *server) feeConvertAtoms(assetTicker string, offeringPriceUSD float64) (uint64, bool) {
	prices := s.fetchPrices()
	seq := seqPrice(prices)
	asset := offeringPriceUSD
	if asset <= 0 {
		asset = prices[strings.ToUpper(assetTicker)]
	}
	if seq <= 0 || asset <= 0 {
		return 0, false
	}
	rate := asset / seq * 1e8
	if rate <= 0 {
		return 0, false
	}
	atoms := math.Ceil(float64(s.cfg.policyFeeSats) / rate)
	if atoms < 1 {
		atoms = 1
	}
	return uint64(atoms), true
}

// offeringPrice extracts the issuer's chosen offering reference price (and its
// quote currency) from an issuance's terms. Several common key spellings are
// accepted; a missing or non-positive price means the offering carries no
// reference price to seed (best-effort).
func offeringPrice(terms json.RawMessage) (price float64, quote string, ok bool) {
	if len(terms) == 0 {
		return 0, "", false
	}
	var m map[string]any
	if err := json.Unmarshal(terms, &m); err != nil {
		return 0, "", false
	}
	quote = "USD"
	if q, isStr := m["quote"].(string); isStr && q != "" {
		quote = strings.ToUpper(q)
	} else if c, isStr := m["currency"].(string); isStr && c != "" {
		quote = strings.ToUpper(c)
	}
	for _, k := range []string{"price", "offering_price", "price_per_token", "price_usd"} {
		if v, isNum := m[k].(float64); isNum && v > 0 {
			return v, quote, true
		}
	}
	return 0, quote, false
}

// maybeSeedPrice publishes the offering's reference price into the box price
// feed as a DATA-ONLY entry tagged kind:"offering-reference", so /prices serves
// the new ticker (consuming UIs label the source). It is idempotent per
// issuance (the price_seeded flag) and best-effort: a feed without the seed
// endpoint, or an unreachable one, is logged and the deploy still succeeds.
//
// The seed POST body is exactly:
//
//	{"ticker","asset_id","price","quote","kind":"offering-reference"}
//
// The box mock feed (contrib/sequentia/mock-price-api.py) needs a matching
// additive POST /seed handler that merges the entry into what GET /prices
// returns; that is the only place the ticker becomes visible in /prices, and it
// touches no rate calculation (that lives in price_server.py, untouched).
func (s *server) maybeSeedPrice(w *WatchState) {
	if w.PriceSeeded || s.cfg.priceURL == "" {
		return
	}
	iss, err := s.st.IssuanceByID(w.IssuanceID)
	if err != nil || iss == nil {
		return
	}
	price, quote, ok := offeringPrice(iss.Terms)
	if !ok {
		return // no reference price to seed
	}
	body, _ := json.Marshal(map[string]any{
		"ticker":   iss.Ticker,
		"asset_id": w.AssetID,
		"price":    price,
		"quote":    quote,
		"kind":     "offering-reference",
	})
	req, err := http.NewRequest("POST", strings.TrimRight(s.cfg.priceURL, "/")+"/seed", strings.NewReader(string(body)))
	if err != nil {
		return
	}
	req.Header.Set("content-type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("price seed for %s (%s): %v", iss.Ticker, w.AssetID, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		log.Printf("price seed for %s (%s): feed returned %d (needs POST /seed handler)", iss.Ticker, w.AssetID, resp.StatusCode)
		return
	}
	w.PriceSeeded = true
	if err := s.st.UpsertWatch(w); err == nil {
		s.st.Audit(w.OwnerAID, "price.seed", map[string]any{
			"issuance_id": w.IssuanceID, "asset": w.AssetID, "ticker": iss.Ticker,
			"price": price, "quote": quote, "kind": "offering-reference",
		})
	}
}
