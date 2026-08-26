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
