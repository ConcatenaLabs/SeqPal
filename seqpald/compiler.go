package main

import (
	"encoding/json"
	"sort"
	"strings"
)

// CompiledRules mirrors openampd's store.Rules JSON shape (OA-3). seqpald emits
// this object as the `rules` field of POST /v1/issuer/assets and /v1/issuer/rules.
// Every field is omitempty, so an asset with no compiled policy serializes and
// behaves exactly as a pre-OA-3 asset.
type CompiledRules struct {
	AllowedCategories    []string       `json:"allowed_categories,omitempty"`
	VelocityWindowBlocks int64          `json:"velocity_window_blocks,omitempty"`
	VelocityMaxAtoms     uint64         `json:"velocity_max_atoms,omitempty"`
	HolderCap            int            `json:"holder_cap,omitempty"`
	LockinUntilHeight    int64          `json:"lockin_until_height,omitempty"`
	FeeConvertAtoms      uint64         `json:"fee_convert_atoms,omitempty"`
	PrimaryAIDs          []string       `json:"primary_aids,omitempty"`
	CategoryDenies       []CategoryDeny `json:"category_denies,omitempty"`
	HolderCapsByCategory map[string]int `json:"holder_caps_by_category,omitempty"`
}

// CategoryDeny is a Reg S distribution-compliance window: a non-primary sender
// may not deliver to a recipient carrying a category with Prefix while the chain
// height is below UntilHeight.
type CategoryDeny struct {
	Prefix      string `json:"prefix"`
	UntilHeight int64  `json:"until_height"`
}

// --- the Step 5 matrix (terms) ------------------------------------------------

// jurisdictionTerm is one row of the issuer's jurisdiction matrix.
type jurisdictionTerm struct {
	Access         string   `json:"access"`          // standard | restricted | excluded
	EligCategories []string `json:"elig_categories"` // optional issuer selection, clamped by access
}

// regSTerm is an offshore distribution-compliance period. Either UntilHeight
// (absolute) or Days (converted against the current tip) may be given.
type regSTerm struct {
	Prefix      string `json:"prefix"`
	UntilHeight int64  `json:"until_height"`
	Days        int64  `json:"days"`
}

// matrixTerms is the subset of the issuance terms object the compiler consumes.
// Unknown keys in terms are ignored (documents, promotion facets, etc. live in
// the same object but are not policy).
type matrixTerms struct {
	Jurisdictions map[string]jurisdictionTerm `json:"jurisdictions"`
	LockupDays    int64                       `json:"lockup_days"`
	LockupHeight  int64                       `json:"lockup_height"`
	RegS          *regSTerm                   `json:"reg_s"`
	EUCaps        map[string]int              `json:"eu_caps"`
	Structure     json.RawMessage             `json:"structure"` // string name or object with velocity
	HolderCap     int                         `json:"holder_cap"`
}

// compileEnv carries the chain-derived inputs the compiler needs to turn a
// relative lockup into an absolute Sequentia block height. Keeping them explicit
// is what makes compileRules a pure, unit-testable function.
type compileEnv struct {
	TipHeight    int64
	BlocksPerDay int64
	FeeConvert   uint64
	PrimaryAIDs  []string
	// Characterization is the instrument classification for the structure (M4).
	// Its zero value (IsAIF false) is a no-op, so a compileRules call that does
	// not set it behaves exactly as it did before M4; an AIF-classified structure
	// restricts EU member-state access to professional categories.
	Characterization Characterization
}

var accessAdmits = map[string]map[string]bool{
	"standard":   {"ret": true, "acc": true, "pro": true, "hnw": true, "soph": true},
	"restricted": {"acc": true, "pro": true},
	"excluded":   {},
}

var standardDefault = []string{"ret", "acc", "pro"}
var restrictedDefault = []string{"acc", "pro"}

// compileRules is the heart of M2: a pure function from the Step 5 matrix (plus
// lockup, Reg S, EU caps, structure, and the chain-derived env) to openampd
// rules. It is unit-tested against fixed matrices. The catch-all is EXCLUDED by
// default: a jurisdiction absent from the matrix contributes no tokens, so its
// residents match no allowed category and are refused.
func compileRules(terms json.RawMessage, env compileEnv) (CompiledRules, error) {
	var mt matrixTerms
	if len(terms) > 0 {
		if err := json.Unmarshal(terms, &mt); err != nil {
			return CompiledRules{}, err
		}
	}
	out := CompiledRules{FeeConvertAtoms: env.FeeConvert}
	if len(env.PrimaryAIDs) > 0 {
		out.PrimaryAIDs = append([]string(nil), env.PrimaryAIDs...)
	}

	// allowed_categories: union of admitted j:XX:elig tokens.
	allowed := map[string]bool{}
	for iso2, jt := range mt.Jurisdictions {
		code := normalizeResidence(iso2)
		if code == "" {
			continue
		}
		// The floor holds here too, whatever the terms say. The deploy refuses a
		// matrix that names one of these, so reaching this is a matrix compiled
		// by some other path; it admits nothing either way.
		if sanctionsFloor[code] {
			continue
		}
		admits := accessAdmits[strings.ToLower(strings.TrimSpace(jt.Access))]
		if admits == nil {
			// Unknown access value is treated as excluded (fail closed).
			continue
		}
		selection := jt.EligCategories
		if len(selection) == 0 {
			if strings.EqualFold(jt.Access, "restricted") {
				selection = restrictedDefault
			} else {
				selection = standardDefault
			}
		}
		for _, e := range selection {
			e = strings.ToLower(strings.TrimSpace(e))
			if admits[e] {
				allowed["j:"+code+":"+e] = true
			}
		}
	}
	// AIF marketing clamp (M4): an AIF-classified structure disables the EU retail
	// lift and restricts EU member states to professional categories. This runs
	// on the admitted set before it is frozen into allowed_categories.
	clampForCharacterization(allowed, env.Characterization)
	if len(allowed) > 0 {
		out.AllowedCategories = sortedKeysBool(allowed)
	}

	// lockin_until_height: absolute height preferred, else days from the tip.
	if mt.LockupHeight > 0 {
		out.LockinUntilHeight = mt.LockupHeight
	} else if mt.LockupDays > 0 && env.BlocksPerDay > 0 {
		out.LockinUntilHeight = env.TipHeight + mt.LockupDays*env.BlocksPerDay
	}

	// category_denies: the Reg S window (default prefix j:US).
	if mt.RegS != nil {
		prefix := strings.TrimSpace(mt.RegS.Prefix)
		if prefix == "" {
			prefix = "j:US"
		}
		until := mt.RegS.UntilHeight
		if until == 0 && mt.RegS.Days > 0 && env.BlocksPerDay > 0 {
			until = env.TipHeight + mt.RegS.Days*env.BlocksPerDay
		}
		if until > 0 {
			out.CategoryDenies = []CategoryDeny{{Prefix: prefix, UntilHeight: until}}
		}
	}

	// holder_caps_by_category: EU per-member-state caps. A bare state code maps
	// to that state's retail token (the sub-150 offeree exemption counts
	// non-qualified persons); an explicit token key is used verbatim.
	if len(mt.EUCaps) > 0 {
		caps := map[string]int{}
		for k, v := range mt.EUCaps {
			if v <= 0 {
				continue
			}
			if strings.Contains(k, ":") {
				caps[k] = v
				continue
			}
			code := normalizeResidence(k)
			if code == "" {
				continue
			}
			caps["j:"+code+":ret"] = v
		}
		if len(caps) > 0 {
			out.HolderCapsByCategory = caps
		}
	}

	// global holder cap (structure-independent, if the matrix sets one).
	if mt.HolderCap > 0 {
		out.HolderCap = mt.HolderCap
	}

	// velocity: structure defaults, overridable in the structure object.
	win, max := velocityForStructure(mt.Structure)
	out.VelocityWindowBlocks = win
	out.VelocityMaxAtoms = max

	return out, nil
}

// velocityForStructure resolves the velocity defaults for a structure. terms.structure
// may be a bare string name or an object {name, velocity_window_blocks, velocity_max_atoms}.
func velocityForStructure(raw json.RawMessage) (windowBlocks int64, maxAtoms uint64) {
	if len(raw) == 0 {
		return 0, 0
	}
	// Object form takes precedence.
	var obj struct {
		Name                 string `json:"name"`
		VelocityWindowBlocks int64  `json:"velocity_window_blocks"`
		VelocityMaxAtoms     uint64 `json:"velocity_max_atoms"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && (obj.VelocityWindowBlocks > 0 || obj.VelocityMaxAtoms > 0 || obj.Name != "") {
		if obj.VelocityWindowBlocks > 0 || obj.VelocityMaxAtoms > 0 {
			return obj.VelocityWindowBlocks, obj.VelocityMaxAtoms
		}
		return structureDefault(obj.Name)
	}
	var name string
	if err := json.Unmarshal(raw, &name); err == nil {
		return structureDefault(name)
	}
	return 0, 0
}

// structureDefault holds the built-in per-structure velocity defaults. Equity
// structures have no default velocity limit; a debt/yield structure applies a
// daily window with no atom ceiling unless the issuer sets one (the ceiling is
// supply-relative and so is expressed explicitly in terms.structure).
func structureDefault(name string) (int64, uint64) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debt-yield", "debt", "yield":
		return 144, 0
	default:
		return 0, 0
	}
}

func sortedKeysBool(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// The OFAC- and FATF-aligned sanctions floor: jurisdictions no issuer may admit,
// whatever their matrix says.
//
// It was drawn in the browser -- src/data/jurisdictions.js, tier "blocked",
// overridable false -- and enforced only by the wizard that renders it. The
// compiler admitted whatever the terms named, so a matrix posted to the API
// directly, rather than clicked through the wizard, put j:IR:ret and its
// neighbours into an asset's allowed categories. The Legal and Licensing page
// says of this floor that "it can never be admitted", which was true of the
// screen and not of the platform.
//
// Kept here, where the rules are actually made. The list is the same one the
// wizard draws; both sides must move together, and a test asserts they have.
var sanctionsFloor = map[string]bool{
	"AF": true, // Afghanistan
	"BY": true, // Belarus
	"CU": true, // Cuba
	"IR": true, // Iran
	"KP": true, // North Korea
	"MM": true, // Myanmar
	"RU": true, // Russia
	"SY": true, // Syria
	"VE": true, // Venezuela
}

// admittedFloorJurisdictions returns the floor jurisdictions a terms matrix
// tries to admit, so a deploy can refuse and say which rather than quietly
// compiling something other than what the issuer submitted.
func admittedFloorJurisdictions(terms json.RawMessage) []string {
	var mt matrixTerms
	if err := json.Unmarshal(rawOrEmpty(terms), &mt); err != nil {
		return nil
	}
	var out []string
	for iso2, jt := range mt.Jurisdictions {
		code := normalizeResidence(iso2)
		if code == "" || !sanctionsFloor[code] {
			continue
		}
		// "excluded" is a known access level that admits nothing, which is not the
		// same as admitting: naming a floor jurisdiction in order to exclude it is
		// exactly what a careful matrix does.
		if len(accessAdmits[strings.ToLower(strings.TrimSpace(jt.Access))]) > 0 {
			out = append(out, code)
		}
	}
	sort.Strings(out)
	return out
}
