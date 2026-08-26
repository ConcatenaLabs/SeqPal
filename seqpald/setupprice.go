package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// What SeqPal charges to set an offering up. These are the PUBLISHED prices, and
// the platform charges them: the numbers below are the ones in
// src/data/pricing.js, which is the page an issuer reads before they arrive, and
// a test holds the two together.
//
// The price is derived from the issuance's own committed terms -- structure,
// public or private, the target raise, whether debt is secured -- so it is the
// same arithmetic the checkout screen showed that issuer, from the same inputs.
const (
	setupNativeEquityUSD      = 12500
	setupEquitySPVUSD         = 17500
	setupDebtYieldUSD         = 20000
	setupDepositoryReceiptUSD = 22500

	// Simple Native Equity: a smaller tier for raises up to this much.
	setupSimpleNativeEquityUSD = 7500
	setupSimpleThresholdUSD    = 500000

	// Secured debt is quoted as a range (+$5K-$15K) and charged at its midpoint,
	// which is the figure the checkout screen shows.
	setupSecuredDebtAddOnUSD = 10000

	// Added to every structure but the Depository Receipt, which is always public
	// and is priced that way already.
	setupPublicOfferingSurchargeUSD = 12500

	// Only for turning a BTC-denominated raise into the USD figure the Simple
	// tier's threshold is written in.
	setupBTCUSDReference = 63500
)

// setupTerms is the part of an issuance's terms that its price depends on.
type setupTerms struct {
	IsPublic bool           `json:"is_public"`
	Unit     string         `json:"unit"`
	Raise    any            `json:"raise"`
	Fields   map[string]any `json:"fields"`
}

// publishedSetupFeeUSD is what the pricing page quotes for this issuance.
//
// An unknown structure has no published price, and inventing one would either
// overcharge an issuer or -- worse, since this is the deploy gate -- let an
// offering through for nothing. So it is an error, and the caller says so.
func publishedSetupFeeUSD(structureID string, termsRaw json.RawMessage) (float64, error) {
	base, ok := map[string]float64{
		"native-equity":      setupNativeEquityUSD,
		"equity-spv":         setupEquitySPVUSD,
		"debt-yield":         setupDebtYieldUSD,
		"depository-receipt": setupDepositoryReceiptUSD,
	}[strings.TrimSpace(structureID)]
	if !ok {
		return 0, fmt.Errorf("no published setup price for structure %q", structureID)
	}

	var t setupTerms
	if len(termsRaw) > 0 {
		_ = json.Unmarshal(termsRaw, &t) // terms that will not parse price as the bare structure
	}

	// The Simple tier's threshold is a USD figure, so a BTC-denominated raise is
	// converted before it is compared: a 100-BTC raise is about $6M, not $100.
	raise := parseRaise(t.Raise)
	if strings.EqualFold(strings.TrimSpace(t.Unit), "BTC") {
		raise *= setupBTCUSDReference
	}
	if structureID == "native-equity" && raise > 0 && raise <= setupSimpleThresholdUSD {
		base = setupSimpleNativeEquityUSD
	}

	secured := 0.0
	if structureID == "debt-yield" {
		if c, _ := t.Fields["collateral"].(string); c != "" && !strings.EqualFold(strings.TrimSpace(c), "Unsecured") {
			secured = setupSecuredDebtAddOnUSD
		}
	}

	surcharge := 0.0
	if t.IsPublic && structureID != "depository-receipt" {
		surcharge = setupPublicOfferingSurchargeUSD
	}
	return base + secured + surcharge, nil
}

// parseRaise reads the target raise the way the checkout screen does: keep the
// digits and the dots, drop everything else, and read what is left as a number.
// "1,000,000" is a million and "$2.5m" is 2.5, which is the same answer the
// issuer was quoted from the same box.
func parseRaise(v any) float64 {
	var s string
	switch x := v.(type) {
	case string:
		s = x
	case float64:
		return x
	case nil:
		return 0
	default:
		s = fmt.Sprint(x)
	}
	kept := strings.Map(func(r rune) rune {
		if (r >= '0' && r <= '9') || r == '.' {
			return r
		}
		return -1
	}, s)
	n, err := strconv.ParseFloat(kept, 64)
	if err != nil {
		return 0
	}
	return n
}
