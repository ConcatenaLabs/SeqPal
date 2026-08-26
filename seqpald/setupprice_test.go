package main

import (
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Every number the setup price is built from, read out of the page that
// publishes it. The platform charges what an issuer was quoted, so these two
// cannot drift: change one and this fails naming which.
func TestTheSetupPricesAreThePublishedOnes(t *testing.T) {
	src := readPricingSource(t)
	for _, c := range []struct {
		what    string
		pattern string
		charged float64
	}{
		{"native equity", `'native-equity':\s*([0-9]+)`, setupNativeEquityUSD},
		{"equity SPV", `'equity-spv':\s*([0-9]+)`, setupEquitySPVUSD},
		{"debt / yield", `'debt-yield':\s*([0-9]+)`, setupDebtYieldUSD},
		{"depository receipt", `'depository-receipt':\s*([0-9]+)`, setupDepositoryReceiptUSD},
		{"simple native equity", `base\s*=\s*([0-9]+)\n\s*simple\s*=\s*true`, setupSimpleNativeEquityUSD},
		{"simple tier threshold", `raiseUsd\s*<=\s*([0-9]+)`, setupSimpleThresholdUSD},
		{"secured debt add-on", `(?s)opts\.collateral !== 'Unsecured'.*?secured = ([0-9]+)`, setupSecuredDebtAddOnUSD},
		{"public-offering surcharge", `isPublic && structureId !== 'depository-receipt' \? ([0-9]+)`, setupPublicOfferingSurchargeUSD},
		{"BTC reference price", `BTC_USD_REF\s*=\s*([0-9]+)`, setupBTCUSDReference},
	} {
		published := publishedNumber(t, src, c.pattern, c.what)
		if published != c.charged {
			t.Errorf("%s is published at %v and charged at %v", c.what, published, c.charged)
		}
	}
}

// The setup-fee table an issuer reads is the same schedule the arithmetic uses.
// It is written for people, in dollars with commas, so it is checked separately
// from the numbers above.
func TestThePublishedSetupTableAgreesWithWhatIsCharged(t *testing.T) {
	src := readPricingSource(t)
	for _, c := range []struct {
		item    string
		charged float64
	}{
		{"Simple Native Equity", setupSimpleNativeEquityUSD},
		{"Native Equity", setupNativeEquityUSD},
		{"Equity SPV", setupEquitySPVUSD},
		{"Debt / Yield (unsecured)", setupDebtYieldUSD},
		{"Depository Receipt", setupDepositoryReceiptUSD},
		{"Public-offering surcharge", setupPublicOfferingSurchargeUSD},
	} {
		re := regexp.MustCompile(`(?s)item: '` + regexp.QuoteMeta(c.item) + `'.*?amount: '\+?\$([0-9,]+)`)
		m := re.FindStringSubmatch(src)
		if m == nil {
			t.Errorf("no published amount for %q; if that row was renamed, this test has to follow it", c.item)
			continue
		}
		published, err := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", ""), 64)
		if err != nil {
			t.Errorf("published amount for %q is not a number: %v", c.item, err)
			continue
		}
		if published != c.charged {
			t.Errorf("%s is listed at $%v and charged at $%v", c.item, published, c.charged)
		}
	}
}

// The arithmetic, on the cases the checkout screen distinguishes.
func TestWhatAnOfferingCostsToSetUp(t *testing.T) {
	terms := func(m map[string]any) json.RawMessage {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	for _, c := range []struct {
		name      string
		structure string
		terms     map[string]any
		want      float64
	}{
		{"a private native-equity raise", "native-equity", map[string]any{}, 12500},
		{"a small one takes the Simple tier", "native-equity", map[string]any{"raise": "400000"}, 7500},
		{"exactly at the threshold is still Simple", "native-equity", map[string]any{"raise": "500000"}, 7500},
		{"a dollar over is not", "native-equity", map[string]any{"raise": "500001"}, 12500},
		{"commas in the raise box are not digits", "native-equity", map[string]any{"raise": "$1,200,000"}, 12500},
		{"a BTC raise is compared in dollars", "native-equity", map[string]any{"raise": "5", "unit": "BTC"}, 7500},
		{"a bigger BTC raise is not Simple", "native-equity", map[string]any{"raise": "100", "unit": "BTC"}, 12500},
		{"public adds the surcharge", "native-equity", map[string]any{"is_public": true}, 25000},
		{"an SPV", "equity-spv", map[string]any{}, 17500},
		{"unsecured debt", "debt-yield", map[string]any{"fields": map[string]any{"collateral": "Unsecured"}}, 20000},
		{"secured debt adds the collateral work", "debt-yield", map[string]any{"fields": map[string]any{"collateral": "Real estate"}}, 30000},
		{"secured public debt adds both", "debt-yield", map[string]any{"is_public": true, "fields": map[string]any{"collateral": "Real estate"}}, 42500},
		{"a DR is always public and priced that way", "depository-receipt", map[string]any{"is_public": true}, 22500},
	} {
		got, err := publishedSetupFeeUSD(c.structure, terms(c.terms))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: charged $%v, want $%v", c.name, got, c.want)
		}
	}

	// A structure with no published price is not free, and not guessed.
	if _, err := publishedSetupFeeUSD("something-else", nil); err == nil {
		t.Errorf("an unpriced structure must be refused, not charged nothing")
	}
}

func readPricingSource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../src/data/pricing.js")
	if err != nil {
		t.Fatalf("read the published prices: %v", err)
	}
	return string(raw)
}

func publishedNumber(t *testing.T, src, pattern, what string) float64 {
	t.Helper()
	m := regexp.MustCompile(pattern).FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("no published figure for %s; if the pricing source was restructured, this test has to follow it", what)
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatalf("published figure for %s is not a number: %v", what, err)
	}
	return v
}

// End to end: an offering is quoted the published price for what it actually
// is, the deploy is refused until that is paid, and paying it opens the gate.
func TestAnOfferingIsChargedThePublishedPriceThroughTheAPI(t *testing.T) {
	h := newM5Harness(t, m5opts{})
	h.s.cfg.setupFeeOverrideUSD = -1 // charge the published schedule
	session, _, _ := h.register(genPriv(t), "Issuer", "HN")

	// A public native-equity raise over the Simple threshold: $12,500 + $12,500.
	issID := h.createIssuance(session, "Public Co", "PUBX", map[string]any{
		"jurisdictions": map[string]any{"HN": map[string]any{"access": "standard"}},
		"price":         1.0,
		"is_public":     true,
		"raise":         "2,000,000",
	})

	fees := h.do("GET", "/api/issuances/"+issID+"/fees", session, nil)
	if fees.code != 200 {
		t.Fatalf("fees: %d %s", fees.code, fees.errMsg())
	}
	if got, _ := fees.body["setup_fee_usd"].(float64); got != 25000 {
		t.Fatalf("quoted $%v to set up a public native-equity raise, want $25,000", got)
	}

	dep := h.do("POST", "/api/deploy", session, map[string]any{
		"issuance_id": issID, "supply": 1000000, "precision": 2,
	})
	if dep.code != 402 {
		t.Fatalf("deploy with the published fee unpaid = %d, want 402 (%s)", dep.code, dep.raw)
	}

	pay := h.do("POST", "/api/issuances/"+issID+"/fees/pay", session, map[string]any{
		"kind": "setup", "rail": "card",
	})
	if pay.code != 200 {
		t.Fatalf("fees/pay: %d %s", pay.code, pay.errMsg())
	}
	h.s.settleFiatDue()
	if dep := h.do("POST", "/api/deploy", session, map[string]any{
		"issuance_id": issID, "supply": 1000000, "precision": 2,
	}); dep.code != 200 {
		t.Fatalf("deploy after paying the published fee = %d, want 200 (%s)", dep.code, dep.raw)
	}
}

// What an issuer can get wrong is refused before they are asked to pay for it.
// A network-enforced deploy with no usable holding key is a misconfiguration:
// paying first and finding that out afterwards is money taken for a deploy that
// was never going to run.
func TestAMisconfiguredDeployIsRefusedBeforeItIsBilled(t *testing.T) {
	h := newM5Harness(t, m5opts{})
	h.s.cfg.setupFeeOverrideUSD = -1 // charge the published schedule
	h.s.cfg.damp = true
	session, _, _ := h.register(genPriv(t), "Issuer", "HN")
	issID := h.createIssuance(session, "Net Co", "NETQ", map[string]any{
		"jurisdictions": map[string]any{"HN": map[string]any{"access": "standard"}}, "price": 1.0,
	})

	// The account has an OpenAMP key of its own, so name a holding key that is
	// neither its own nor from any wallet it has linked.
	res := h.do("POST", "/api/deploy", session, map[string]any{
		"issuance_id": issID, "supply": 1000, "precision": 0,
		"enforcement": "network", "holder_key": strings.Repeat("ab", 32),
	})
	if res.code == 402 {
		t.Fatalf("the issuer was billed before being told the deploy was misconfigured: %s", res.raw)
	}
	if res.code != 400 && res.code != 403 {
		t.Fatalf("a deploy with an unusable holding key = %d, want it refused for the key (%s)", res.code, res.raw)
	}
}
