package main

import (
	"fmt"
	"math/big"
	"net/http"
	"strings"
)

// The most there will ever be of a token.
//
// A serviced token's reissuance token is held by the policy server in a supply
// cap: a covenant that mints only through it and counts what it has minted
// against a cap fixed when the token was issued. Nobody can raise that cap, the
// platform and the issuer included, and nothing else can mint the token. So the
// issuer's power to create more of their token is decided once, at issuance:
// the issuer flow asks for the most there will ever be of it (max_supply, whole
// tokens, issuance included), and the policy server's cap is max_supply less
// the initial supply. The default the page shows is the initial supply, so the
// supply is final unless the issuer chooses otherwise, with the consequence in
// front of them: an issuer who can create more can dilute every holder.
//
// A network-enforced token's cap is 0 (the policy server never mints one), and
// a freely-tradable one is not issued through the policy server, so the
// question is asked of serviced tokens only.

// supplyView is a live token's supply as the page shows it: what exists now,
// what was created at issuance, what has been created since through the cap,
// what can still be created, and the most there will ever be. Atoms throughout.
type supplyView struct {
	Asset            string `json:"asset"`
	Ticker           string `json:"ticker"`
	Precision        int    `json:"precision"`
	CirculatingAtoms uint64 `json:"circulating_atoms"`
	Height           int64  `json:"height"`
	InitialAtoms     uint64 `json:"initial_atoms"`
	// Capped is true when the policy server holds the token in a supply cap; the
	// four figures below are then the cap's. False means no more of the token
	// can be created through this platform at all.
	Capped      bool   `json:"capped"`
	CapAtoms    uint64 `json:"cap_atoms"`    // the most created after issuance
	MintedAtoms uint64 `json:"minted_atoms"` // created after issuance so far
	RoomAtoms   uint64 `json:"room_atoms"`   // can still be created
	MaxAtoms    uint64 `json:"max_atoms"`    // initial + cap: the most there will ever be
	Final       bool   `json:"final"`        // nothing more can ever be created
	Words       string `json:"words"`        // the same, in a sentence
}

// readSupply asks the policy server for a live serviced or network token's
// supply and its cap.
func (s *server) readSupply(iss *Issuance) (*supplyView, error) {
	var out struct {
		CirculatingAtoms uint64 `json:"circulating_atoms"`
		Height           int64  `json:"height"`
		SupplyCap        *struct {
			CapAtoms    uint64 `json:"cap_atoms"`
			MintedAtoms uint64 `json:"minted_atoms"`
			RoomAtoms   uint64 `json:"room_atoms"`
		} `json:"supply_cap"`
	}
	if err := s.callOpenAMP("GET", "/v1/supply?asset="+iss.AssetID, "", nil, &out); err != nil {
		return nil, err
	}
	initial, _ := atomsFor(iss.Supply, iss.Precision)
	v := &supplyView{
		Asset: iss.AssetID, Ticker: iss.Ticker, Precision: iss.Precision,
		CirculatingAtoms: out.CirculatingAtoms, Height: out.Height, InitialAtoms: initial,
	}
	if c := out.SupplyCap; c != nil {
		v.Capped = true
		v.CapAtoms, v.MintedAtoms, v.RoomAtoms = c.CapAtoms, c.MintedAtoms, c.RoomAtoms
	}
	v.MaxAtoms = initial + v.CapAtoms
	if v.MaxAtoms < initial { // overflow: no real cap reaches it
		v.MaxAtoms = ^uint64(0)
	}
	v.Final = v.RoomAtoms == 0
	v.Words = supplyWords(v)
	return v, nil
}

// supplyWords says where the token's supply stands in one sentence.
func supplyWords(v *supplyView) string {
	t := v.Ticker
	switch {
	case !v.Capped || v.CapAtoms == 0:
		return fmt.Sprintf("The supply of %s is final: no more of it can ever be created.", t)
	case v.RoomAtoms == 0:
		return fmt.Sprintf("All %s %s that can ever exist have been created: no more can be.",
			tokens(v.MaxAtoms, v.Precision), t)
	default:
		return fmt.Sprintf("%s %s can still be created, and never more: the most there will ever be of %s is %s.",
			tokens(v.RoomAtoms, v.Precision), t, t, tokens(v.MaxAtoms, v.Precision))
	}
}

// mintRefusal is the plain-words reason a mint of `atoms` cannot happen, or ""
// when it can.
func mintRefusal(v *supplyView, atoms uint64) string {
	t := v.Ticker
	if !v.Capped || v.CapAtoms == 0 {
		return fmt.Sprintf("No more %s can be created. Its supply was made final when it was issued, and nobody, you included, can change that.", t)
	}
	if atoms > v.RoomAtoms {
		if v.RoomAtoms == 0 {
			return fmt.Sprintf("No more %s can be created: all %s that can ever exist already have been, the most you set when you issued it.",
				t, tokens(v.MaxAtoms, v.Precision))
		}
		return fmt.Sprintf("This would create %s %s, but only %s more can ever be created: the most there will ever be of %s is %s, which you set when you issued it and nobody can raise. Nothing was created.",
			tokens(atoms, v.Precision), t, tokens(v.RoomAtoms, v.Precision), t, tokens(v.MaxAtoms, v.Precision))
	}
	return ""
}

// plainReissueError puts a policy server refusal of a mint into the issuer's
// words; anything else is passed through as it came.
func plainReissueError(v *supplyView, atoms uint64, err error) string {
	msg := err.Error()
	switch {
	case v != nil && strings.Contains(msg, "above its cap"):
		if r := mintRefusal(v, atoms); r != "" {
			return r
		}
		return fmt.Sprintf("This would create more %s than can ever exist. Nothing was created.", v.Ticker)
	case strings.Contains(msg, "in no supply cap"):
		return "No more of this token can be created: it was issued with no way to create more."
	case strings.Contains(msg, "cannot be confidential"):
		return "New tokens are always created in the open: the amount is public on the chain, so it cannot be hidden. A holder can still choose privacy when they move them."
	}
	return "The token could not be created: " + msg
}

// tokens renders atoms as whole tokens at the given precision, grouped in
// threes, without going through a float.
func tokens(atoms uint64, precision int) string {
	n := new(big.Int).SetUint64(atoms)
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(precision)), nil)
	whole, frac := new(big.Int).QuoRem(n, scale, new(big.Int))
	ws := whole.String()
	var b strings.Builder
	for i, c := range ws {
		if i > 0 && (len(ws)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if precision > 0 && frac.Sign() != 0 {
		fs := fmt.Sprintf("%0*s", precision, frac.String())
		b.WriteString("." + strings.TrimRight(fs, "0"))
	}
	return b.String()
}

// handleIssuanceSupply is GET /api/issuances/{id}/supply (owner): the token's
// supply, what can still be created, and the most there will ever be.
func (s *server) handleIssuanceSupply(w http.ResponseWriter, r *http.Request) {
	acct := principal(r)
	iss := s.ownedIssuance(w, acct, r.PathValue("id"))
	if iss == nil {
		return
	}
	if iss.Status != "live" || iss.AssetID == "" {
		writeErr(w, 409, "this issuance is not live on chain")
		return
	}
	if iss.Enforcement == "bearer" {
		writeErr(w, 409, "a freely-tradable token's supply is read from the chain, not from this platform's policy server")
		return
	}
	v, err := s.readSupply(iss)
	if err != nil {
		writeErr(w, 502, "could not read the supply: %v", err)
		return
	}
	writeJSON(w, 200, v)
}
