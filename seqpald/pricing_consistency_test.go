package main

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

// The price this platform charges and the price it publishes must be the same
// number. The pricing page is what an issuer reads before they arrive, so it is
// the contract; a default that quietly disagrees with it overcharges them.
func TestTheVerificationFeesMatchThePublishedPrices(t *testing.T) {
	raw, err := os.ReadFile("../src/data/pricing.js")
	if err != nil {
		t.Fatalf("read the published prices: %v", err)
	}
	for _, c := range []struct {
		item    string
		charged float64
	}{
		{"SeqPal ID, individual", defaultKYCFeeUSD},
		{"SeqPal ID, corporate", defaultKYBFeeUSD},
	} {
		published := publishedPrice(t, string(raw), c.item)
		if published != c.charged {
			t.Errorf("%s is published at $%v and charged at $%v", c.item, published, c.charged)
		}
	}
}

// publishedPrice reads the amount published for one fee row.
func publishedPrice(t *testing.T, src, item string) float64 {
	t.Helper()
	re := regexp.MustCompile(`(?s)item: '` + regexp.QuoteMeta(item) + `'.*?amount: '\$([0-9,]+)'`)
	m := re.FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("no published price for %q; if the row was renamed, this test has to follow it", item)
	}
	v, err := strconv.ParseFloat(regexp.MustCompile(`,`).ReplaceAllString(m[1], ""), 64)
	if err != nil {
		t.Fatalf("published price for %q is not a number: %v", item, err)
	}
	return v
}

// The sanctions floor is drawn in the browser and enforced here. A country the
// issuer's screen shows as blocked that this server would admit is the UI
// telling them something the platform will not do -- and the other way round is
// a deploy refused for a reason nothing on screen explains.
func TestTheSanctionsFloorIsTheOneTheIssuerIsShown(t *testing.T) {
	raw, err := os.ReadFile("../src/data/jurisdictions.js")
	if err != nil {
		t.Fatalf("read the jurisdiction table: %v", err)
	}
	re := regexp.MustCompile(`code: '([A-Z]{2})'[^}]*tier: 'blocked'`)
	shown := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
		shown[m[1]] = true
	}
	if len(shown) == 0 {
		t.Fatal("no blocked jurisdictions found; if that table was restructured, this test has to follow it")
	}
	for code := range shown {
		if !sanctionsFloor[code] {
			t.Errorf("%s is shown to the issuer as blocked and this server would admit it", code)
		}
	}
	for code := range sanctionsFloor {
		if !shown[code] {
			t.Errorf("%s is refused by this server and the issuer is never shown it as blocked", code)
		}
	}
}
