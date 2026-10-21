package main

import (
	"strings"
	"testing"
)

// The fee asset rule, as every wallet applies it: the asset moved where the
// node accepts it, else the accepted asset whose largest coin covers the most
// fees, none preferred for what it is.
func TestPickFeeAssetFollowsTheRule(t *testing.T) {
	policy := strings.Repeat("c8", 32)
	usdx := m5USDX
	gold := strings.Repeat("a1", 32)
	rates := map[string]uint64{policy: 100000000, usdx: 100000000, gold: 400000000000}

	cases := []struct {
		name  string
		moved string
		coins []feeCoin
		want  string
	}{
		{"the asset moved, accepted", usdx, []feeCoin{{policy, 900 * 1e8}}, usdx},
		{"the asset moved, accepted, held by nobody here", gold, nil, gold},
		{"nothing moved: the largest coin by value, a wallet with no policy asset",
			"", []feeCoin{{usdx, 3e8}, {usdx, 5e8}}, usdx},
		{"nothing moved: a policy coin worth less loses", "", []feeCoin{{policy, 2e8}, {usdx, 5e8}}, usdx},
		{"nothing moved: a policy coin worth more wins, for its worth", "", []feeCoin{{policy, 6e8}, {usdx, 5e8}}, policy},
		{"the largest coin, not the sum", "", []feeCoin{{usdx, 3e8}, {usdx, 3e8}, {policy, 5e8}}, policy},
		{"valued at the node's rate: one gold atom-heavy coin", "", []feeCoin{{gold, 1e6}, {usdx, 5e8}}, gold},
		{"a moved asset the node does not accept falls to the rule", strings.Repeat("bb", 32),
			[]feeCoin{{usdx, 5e8}, {strings.Repeat("bb", 32), 9e8}}, usdx},
		{"a tie goes to the lower id", "", []feeCoin{{usdx, 5e8}, {policy, 5e8}}, usdx},
	}
	for _, c := range cases {
		got, err := pickFeeAsset(c.moved, rates, c.coins)
		if err != nil || got != c.want {
			t.Errorf("%s: got %q, %v; want %q", c.name, got, err, c.want)
		}
	}
	if _, err := pickFeeAsset("", rates, []feeCoin{{strings.Repeat("bb", 32), 9e8}}); err == nil ||
		!strings.Contains(err.Error(), "no fee asset") {
		t.Errorf("a wallet with no accepted coin: got %v, want a refusal", err)
	}
}

// A bearer mint funded by an escrow wallet that holds no policy asset pays
// its fee in what the wallet holds (USDX), and never asks for the policy asset.
func TestBearerMintFeeFromAWalletWithNoPolicyAsset(t *testing.T) {
	h := newM10Harness(t)
	priv := genPriv(t)
	session, _, _ := h.register(priv, "Fee Issuer", "HN")
	h.deployBearerLive(t, session, priv, "FEEX", xonlyHex(t, genPriv(t)))
	h.seq.mu.Lock()
	got := append([]string(nil), h.sup.feeAssets...)
	h.seq.mu.Unlock()
	if len(got) == 0 {
		t.Fatal("the mint was funded with no fee_asset")
	}
	for _, a := range got {
		if a != m5USDX {
			t.Fatalf("the mint's funding paid its fee in %q; the wallet holds only USDX (%s)", a, m5USDX)
		}
	}
}

// The node keys the policy asset by its label: the rule still compares it by
// its id, so a wallet whose best coin is the policy asset pays in it.
func TestFeeRatesKeyedByLabelAreReadByAssetID(t *testing.T) {
	policy := strings.Repeat("c8", 32)
	usdx := m5USDX
	rates := ratesByAssetID(map[string]uint64{"bitcoin": 100000000, usdx: 100000000, "nolabel": 5},
		map[string]string{"bitcoin": strings.ToUpper(policy), "USDX": usdx})
	if rates[policy] != 100000000 || rates[usdx] != 100000000 || len(rates) != 2 {
		t.Fatalf("rates by asset id: %v", rates)
	}
	got, err := pickFeeAsset("", rates, []feeCoin{{policy, 6e8}, {usdx, 5e8}})
	if err != nil || got != policy {
		t.Fatalf("a policy coin worth more pays: got %q, %v", got, err)
	}
}
