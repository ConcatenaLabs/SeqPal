package main

import (
	"fmt"
	"strings"
)

// Instrument characterization is the legal predicate the rest of the M4 pipeline
// depends on: before a jurisdiction matrix can be compiled or an offering
// document generated, each structure must be classified as a plain transferable
// security or as an alternative investment fund (AIFMD) / collective investment
// scheme (UK FSMA s.235), because that classification changes the marketing
// regime and the PRIIPs position per jurisdiction. The classification is a pure
// function of the structure name, so the memo, the generated documents, and the
// policy compiler all read the same determination.
//
// The compiler CONSUMES the classification (Section 4.2 of the plan): an
// AIF-classified structure disables the EU retail lift, restricts EU access to
// professional categories consistent with Article 42 NPPR, and switches the UK
// gate to the CIS-promotion wording. The document generator reads the same memo
// for the per-structure risk factors and the PRIIPs applicability line.

// euMemberStates is the set of ISO2 codes treated as EU member states for the
// AIF marketing clamp and per-member-state offeree counting. The list is the 27
// members; it is deliberately explicit rather than derived so a policy decision
// (which states are inside the EU marketing perimeter) is auditable.
var euMemberStates = map[string]bool{
	"AT": true, "BE": true, "BG": true, "HR": true, "CY": true, "CZ": true,
	"DK": true, "EE": true, "FI": true, "FR": true, "DE": true, "GR": true,
	"HU": true, "IE": true, "IT": true, "LV": true, "LT": true, "LU": true,
	"MT": true, "NL": true, "PL": true, "PT": true, "RO": true, "SK": true,
	"SI": true, "ES": true, "SE": true,
}

// Characterization is the per-structure instrument-characterization memo. It is
// exposed for the SPA (GET /api/characterization), consumed by the matrix
// compiler, and rendered into the generated offering documents.
type Characterization struct {
	Structure    string            `json:"structure"`
	Instrument   string            `json:"instrument"`     // plain-language instrument type
	IsAIF        bool              `json:"is_aif"`         // AIFMD alternative investment fund
	IsCIS        bool              `json:"is_cis"`         // UK FSMA s.235 collective investment scheme
	PRIIPs       bool              `json:"priips"`         // a PRIIPs KID is required for EU retail
	EURetailLift bool              `json:"eu_retail_lift"` // may EU retail be lifted in at all
	UKGate       string            `json:"uk_gate"`        // fpo-statement | cis-promotion
	Analysis     string            `json:"analysis"`       // prose determination
	Marketing    map[string]string `json:"marketing"`      // jurisdiction bucket -> regime note
}

// canonicalStructure maps the free-form structure name an issuer picked (the
// issuance StructureID or terms.structure) onto one of the four canonical
// structure ids the pipeline reasons about. The canonical id is
// "depository-receipt" (the spelling the SPA uses); "depositary-receipt" stays
// accepted as an alias. An EMPTY name is the documented default and maps to
// plain equity; an unrecognized non-empty name returns ok=false so the caller
// FAILS CLOSED (W-7): a structure the pipeline cannot classify must refuse
// rather than silently characterize as equity, because the classification
// changes the marketing regime, the PRIIPs position, and the compiled rules.
func canonicalStructure(name string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "equity-spv", "spv", "equity_spv", "holdco", "holding":
		return "equity-spv", true
	case "debt-yield", "debt", "yield", "note", "bond":
		return "debt-yield", true
	case "depository-receipt", "dr", "receipt", "depositary-receipt":
		return "depository-receipt", true
	case "equity", "native-equity", "equity-direct", "share", "shares", "":
		return "equity", true
	default:
		return "", false
	}
}

// characterize returns the classification for a structure. The four structures:
//
//   - equity: a direct transferable security (shares of an operating company).
//     Not a fund. EU retail is reachable through the prospectus-exemption route;
//     shares are outside PRIIPs unless packaged. UK promotion runs through the
//     FPO investor statements.
//   - equity-spv: a special-purpose holding vehicle. A vehicle whose purpose is
//     to pool investor capital to hold assets is capable of being an AIF under
//     AIFMD and a collective investment scheme under UK FSMA s.235, so it is
//     classified as such: EU marketing runs through Article 42 NPPR
//     (professional-only) and UK promotion through the CIS-promotion exemptions,
//     and a PRIIPs KID is required before any EU retail admission.
//   - debt-yield: a pooled yield instrument, classified as an AIF/CIS on the
//     same basis as the SPV.
//   - depository-receipt: a receipt mirroring an underlying listed security. It
//     is itself a transferable security rather than a fund, but it is a packaged
//     retail product, so a PRIIPs KID is required for EU retail and a
//     dividend-equivalent withholding and US-person analysis attaches.
//
// An unrecognized non-empty structure name is an error (fail closed, W-7); an
// empty name characterizes as plain equity, the documented default.
func characterize(name string) (Characterization, error) {
	s, ok := canonicalStructure(name)
	if !ok {
		return Characterization{}, fmt.Errorf("unrecognized structure %q", strings.TrimSpace(name))
	}
	switch s {
	case "equity-spv":
		return Characterization{
			Structure:    s,
			Instrument:   "a unit or share in a special-purpose holding vehicle",
			IsAIF:        true,
			IsCIS:        true,
			PRIIPs:       true,
			EURetailLift: false,
			UKGate:       "cis-promotion",
			Analysis: "A special-purpose vehicle whose purpose is to pool investor capital to acquire and hold assets " +
				"is capable of constituting an alternative investment fund under AIFMD and an unregulated collective " +
				"investment scheme under UK FSMA section 235. On that classification, prospectus-exemption offeree " +
				"counting and the Financial Promotion Order statements are the wrong framework for EU and UK marketing. " +
				"EU marketing is available only through Article 42 national private placement regime registration, which " +
				"is in practice restricted to professional investors, and UK promotion runs through FSMA section 238 and " +
				"the Promotion of Collective Investment Schemes (Exemptions) Order rather than FPO Articles 48 and 50A. " +
				"A PRIIPs-compliant key information document is required before any offer to EU retail.",
			Marketing: map[string]string{
				"eu": "Article 42 NPPR registration per member state, professional investors only; EU retail requires a PRIIPs KID and is not admitted by lift.",
				"uk": "FSMA section 238 with the CIS-promotion exemptions; a self-certified sophisticated or high-net-worth statement is recorded before the offering renders.",
				"us": "Regulation S offshore or Rule 506(c) verified accreditation; 506(c) purchasers hold Rule 144 restricted securities.",
			},
		}, nil
	case "debt-yield":
		return Characterization{
			Structure:    s,
			Instrument:   "a debt or yield instrument in a pooled vehicle",
			IsAIF:        true,
			IsCIS:        true,
			PRIIPs:       true,
			EURetailLift: false,
			UKGate:       "cis-promotion",
			Analysis: "A pooled yield instrument that returns income from a managed pool of assets is capable of " +
				"constituting an alternative investment fund under AIFMD and a collective investment scheme under UK " +
				"FSMA section 235. EU marketing therefore runs through Article 42 national private placement regime " +
				"registration on a professional-only basis, UK promotion runs through the CIS-promotion exemptions, and a " +
				"PRIIPs key information document is required before any EU retail admission. Credit and default risk of " +
				"the underlying pool is borne by the holder.",
			Marketing: map[string]string{
				"eu": "Article 42 NPPR registration per member state, professional investors only; EU retail requires a PRIIPs KID and is not admitted by lift.",
				"uk": "FSMA section 238 with the CIS-promotion exemptions.",
				"us": "Regulation S offshore or Rule 506(c) verified accreditation; 506(c) purchasers hold Rule 144 restricted securities.",
			},
		}, nil
	case "depository-receipt":
		return Characterization{
			Structure:    s,
			Instrument:   "a depository receipt over an underlying security",
			IsAIF:        false,
			IsCIS:        false,
			PRIIPs:       true,
			EURetailLift: true,
			UKGate:       "fpo-statement",
			Analysis: "A depository receipt is a transferable security in its own right rather than a fund, so the " +
				"prospectus-exemption and Financial Promotion Order frameworks apply. It is nevertheless a packaged " +
				"retail product, so a PRIIPs-compliant key information document is required before any offer to EU retail, " +
				"and a receipt mirroring a US-listed security carries a dividend-equivalent withholding position under " +
				"chapter 3 and section 871(m) and a US-person exclusion. The holder relies on the custodian of the " +
				"underlying, whose reserve attestations are a simulated element in this proof of concept.",
			Marketing: map[string]string{
				"eu": "Prospectus Regulation exemption with per-member-state offeree counting; EU retail requires a PRIIPs KID.",
				"uk": "FPO Articles 48 and 50A investor statements (SI 2024/301 wording and thresholds).",
				"us": "US-person excluded; Regulation S offshore distribution compliance under 17 CFR 230.903.",
			},
		}, nil
	default: // equity
		return Characterization{
			Structure:    "equity",
			Instrument:   "a direct transferable equity security",
			IsAIF:        false,
			IsCIS:        false,
			PRIIPs:       false,
			EURetailLift: true,
			UKGate:       "fpo-statement",
			Analysis: "Direct equity in an operating company is a transferable security and is not a collective " +
				"investment. EU marketing runs through the Prospectus Regulation exemptions with per-member-state " +
				"offeree counting, and UK promotion runs through the Financial Promotion Order investor statements. " +
				"Shares are outside the PRIIPs regime unless packaged. The classification does not remove the " +
				"jurisdiction-specific access rules the issuer configures.",
			Marketing: map[string]string{
				"eu": "Prospectus Regulation exemption with per-member-state offeree counting; EU retail may be admitted by lift.",
				"uk": "FPO Articles 48 and 50A investor statements (SI 2024/301 wording and thresholds).",
				"us": "Regulation S offshore or Rule 506(c) verified accreditation; 506(c) purchasers hold Rule 144 restricted securities.",
			},
		}, nil
	}
}

// clampForCharacterization applies the AIF marketing clamp to a set of admitted
// category tokens. For an AIF-classified structure, EU member-state access is
// restricted to professional categories: retail (ret) and the US-accreditation
// token (acc) are removed for EU states, leaving only the professional (pro)
// token. Non-EU jurisdictions and non-AIF structures are untouched, so the pure
// compiler behaves exactly as before whenever the characterization is the zero
// value (IsAIF false), which is what the direct compiler unit tests exercise.
func clampForCharacterization(allowed map[string]bool, ch Characterization) {
	if !ch.IsAIF {
		return
	}
	for tok := range allowed {
		parts := strings.Split(tok, ":")
		if len(parts) != 3 || parts[0] != "j" {
			continue
		}
		iso2, elig := parts[1], parts[2]
		if euMemberStates[iso2] && elig != "pro" {
			delete(allowed, tok)
		}
	}
}
