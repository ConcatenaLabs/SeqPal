package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// The four public sanctions sources screened at ID creation and daily by cron.
// A platform admitting EU and UK personas cannot screen US lists alone, so all
// four are consulted. Order is stable so the passport renders it deterministically.
var sanctionsLists = []string{"ofac_sdn", "ofac_consolidated", "eu_consolidated", "un_sc"}

// sanctionsSources are the public download URLs. These are fetched best-effort
// and cached to disk; when a source is unreachable in the build/test
// environment the bundled fixture below stands in for it, but the screening path
// and the refusal are real either way.
var sanctionsSources = map[string]string{
	"ofac_sdn":          "https://www.treasury.gov/ofac/downloads/sdn.csv",
	"ofac_consolidated": "https://www.treasury.gov/ofac/downloads/consolidated/cons_prim.csv",
	// The europeaid path redirects, and the redirect ends in a 403: this list has
	// never actually downloaded. The public token endpoint below is the one the
	// EU serves the consolidated list from, and it answers 200.
	"eu_consolidated": "https://webgate.ec.europa.eu/fsd/fsf/public/files/csvFullSanctionsList_1_1/content?token=dG9rZW4tMjAxNw",
	"un_sc":           "https://scsanctions.un.org/resources/xml/en/consolidated.xml",
}

// fixtureList seeds each list with at least the deterministic TEST personas, so
// demos and tests exercise the real refusal path even offline. Disposition tells
// the SIMULATED auto-reviewer what to do with a match on that entry: "confirm"
// (a real hit -> freeze) or "clear" (a false positive -> proceed). Entries with
// no disposition are treated as genuine hits that only a human may clear.
type fixtureList struct {
	names        []string
	dispositions map[string]string
}

// bundledFixture: fabricated, clearly-labeled TEST entries. Real list downloads
// are MERGED on top of these; the fixture never masks a real entry.
var bundledFixture = map[string]fixtureList{
	"ofac_sdn": {
		names:        []string{"OFAC SDN TEST PERSONA CONFIRM"},
		dispositions: map[string]string{"OFAC SDN TEST PERSONA CONFIRM": "confirm"},
	},
	"ofac_consolidated": {
		names:        []string{"OFAC CONSOLIDATED TEST PERSONA CONFIRM"},
		dispositions: map[string]string{"OFAC CONSOLIDATED TEST PERSONA CONFIRM": "confirm"},
	},
	"eu_consolidated": {
		names:        []string{"SEQPAL EU TEST FALSE POSITIVE CLEAR"},
		dispositions: map[string]string{"SEQPAL EU TEST FALSE POSITIVE CLEAR": "clear"},
	},
	"un_sc": {
		names:        []string{"UN SC TEST PERSONA CONFIRM"},
		dispositions: map[string]string{"UN SC TEST PERSONA CONFIRM": "confirm"},
	},
}

// screener holds the parsed name sets per list. Matching is exact on a
// normalized full name (uppercase, whitespace collapsed): low false-positive,
// deterministic, and defensible. Production uses fuzzy/transliteration matching;
// that refinement is documented, the enforcement is not simulated.
type screener struct {
	mu           sync.RWMutex
	names        map[string]map[string]bool   // list -> normalized name -> present
	dispositions map[string]map[string]string // list -> normalized name -> confirm|clear
	cacheDir     string
	refreshedAt  time.Time
	client       *http.Client
	// Whether the name sets are the real lists rather than the bundled test
	// fixture. An instance told where to cache the lists is expected to have
	// them, and must not decide anyone is clean until it does.
	loaded bool
}

func newScreener(cacheDir string) *screener {
	sc := &screener{
		names:        map[string]map[string]bool{},
		dispositions: map[string]map[string]string{},
		cacheDir:     cacheDir,
		// The EU list is 25MB, and 30 seconds did not always cover it even when
		// the URL was right.
		client: &http.Client{Timeout: 3 * time.Minute},
	}
	sc.loadFixture()
	// The lists on disk are the ones this instance last downloaded, and reading
	// them costs a second. Without this the process came up screening against
	// the bundled fixture -- which is to say, against almost nobody -- and kept
	// doing so until the first refresh finished, half a minute later, granting
	// eligibility to anyone who verified in between.
	sc.loadCache()
	// With nowhere to cache, this is a test or an offline demo: the fixture is
	// the documented stand-in and there is nothing to wait for.
	if cacheDir == "" {
		sc.loaded = true
	}
	return sc
}

// loadCache seeds the name sets from the last successful download of each list.
func (sc *screener) loadCache() {
	for _, list := range sanctionsLists {
		body := sc.readCache(list)
		if body == nil {
			continue
		}
		names := parseSanctions(list, body)
		if len(names) == 0 {
			continue
		}
		sc.mu.Lock()
		for _, n := range names {
			sc.names[list][normalizeName(n)] = true
		}
		total := len(sc.names[list])
		sc.loaded = true
		sc.mu.Unlock()
		log.Printf("screening: %s loaded from cache, %d names on file", list, total)
	}
}

// ready reports whether this instance is screening against the real lists. An
// instance that is not has no business deciding an identity is clean.
func (sc *screener) ready() bool {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.loaded
}

func (sc *screener) loadFixture() {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	for _, list := range sanctionsLists {
		if sc.names[list] == nil {
			sc.names[list] = map[string]bool{}
		}
		if sc.dispositions[list] == nil {
			sc.dispositions[list] = map[string]string{}
		}
		fx := bundledFixture[list]
		for _, n := range fx.names {
			sc.names[list][normalizeName(n)] = true
		}
		for n, d := range fx.dispositions {
			sc.dispositions[list][normalizeName(n)] = d
		}
	}
}

// Refresh fetches and parses every source, caching the raw download to disk. A
// source that cannot be reached (or parsed) leaves that list at its previous
// contents (fixture plus any earlier successful download). It is safe to call
// from a cron and at startup.
func (sc *screener) Refresh() {
	for _, list := range sanctionsLists {
		url := sanctionsSources[list]
		body, err := sc.fetch(url)
		if err != nil {
			log.Printf("screening: %s unreachable (%v); using cached/fixture", list, err)
			if cached := sc.readCache(list); cached != nil {
				body = cached
			} else {
				continue
			}
		} else {
			sc.writeCache(list, body)
		}
		names := parseSanctions(list, body)
		if len(names) == 0 {
			// A source that downloads but yields no names is a parser that no
			// longer matches the file, and it leaves this list at the bundled
			// fixture -- which is to say, not screening. Silence made that
			// indistinguishable from a list that simply had nothing new.
			log.Printf("screening: %s downloaded but parsed to no names; leaving it at cached/fixture", list)
			continue
		}
		sc.mu.Lock()
		for _, n := range names {
			sc.names[list][normalizeName(n)] = true
		}
		total := len(sc.names[list])
		sc.loaded = true
		sc.mu.Unlock()
		log.Printf("screening: %s refreshed, %d names on file", list, total)
	}
	sc.mu.Lock()
	sc.refreshedAt = time.Now()
	sc.mu.Unlock()
}

func (sc *screener) fetch(url string) ([]byte, error) {
	resp, err := sc.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, errStatus(resp.StatusCode)
	}
	return readAllLimited(resp.Body, 64<<20)
}

func (sc *screener) cachePath(list string) string {
	if sc.cacheDir == "" {
		return ""
	}
	return filepath.Join(sc.cacheDir, "sanctions_"+list+".raw")
}

func (sc *screener) writeCache(list string, body []byte) {
	p := sc.cachePath(list)
	if p == "" {
		return
	}
	_ = os.MkdirAll(sc.cacheDir, 0o700)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err == nil {
		_ = os.Rename(tmp, p)
	}
}

func (sc *screener) readCache(list string) []byte {
	p := sc.cachePath(list)
	if p == "" {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	return b
}

// Screen matches a registered name against every list and returns one result per
// list. lastScreenedAt is stamped now on every list, matched flags the hits.
func (sc *screener) Screen(name string) []ScreeningResult {
	norm := normalizeName(name)
	now := time.Now().Unix()
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	out := make([]ScreeningResult, 0, len(sanctionsLists))
	for _, list := range sanctionsLists {
		r := ScreeningResult{List: list, LastScreenedAt: now}
		if norm != "" && sc.names[list][norm] {
			r.Matched = true
			r.MatchedEntry = name
		}
		out = append(out, r)
	}
	return out
}

// disposition returns the SIMULATED auto-reviewer's hint for a matched entry on
// a list ("confirm", "clear", or "" for a genuine hit only a human clears).
func (sc *screener) disposition(list, matchedEntry string) string {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	if m := sc.dispositions[list]; m != nil {
		return m[normalizeName(matchedEntry)]
	}
	return ""
}

func (sc *screener) refreshedAtUnix() int64 {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	if sc.refreshedAt.IsZero() {
		return 0
	}
	return sc.refreshedAt.Unix()
}

// --- parsing -----------------------------------------------------------------

var xmlNameTag = regexp.MustCompile(`(?is)<(?:WHOLE_NAME|FULL_NAME|NAME_ORIGINAL_SCRIPT)>([^<]+)</`)

// parseSanctions extracts candidate full names from a raw download. CSV lists
// (OFAC) put the primary name in the second field; the UN XML uses a WHOLE_NAME
// element; the EU CSV is semicolon-delimited with the name in an early field.
// This is best-effort by design: unparsed real entries just do not screen, the
// fixture personas always do, and the refusal path is real.
// Which field of the EU consolidated CSV carries the name. The file is wide (118
// columns) and its first sixteen describe the designation rather than the
// person, so this is not a number to guess at: it is NameAlias_WholeName.
const euWholeNameField = 19

func parseSanctions(list string, body []byte) []string {
	text := string(body)
	switch list {
	case "un_sc":
		var out []string
		for _, m := range xmlNameTag.FindAllStringSubmatch(text, -1) {
			if n := strings.TrimSpace(m[1]); n != "" {
				out = append(out, n)
			}
		}
		return out
	case "eu_consolidated":
		// Semicolon-delimited, with a header row. The name is NameAlias_WholeName;
		// field 8 is Entity_SubjectType_ClassificationCode, which is the literal
		// word "person" or "enterprise" on every row -- a list of those matches
		// nobody, which is what this screened against for as long as it was set.
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		}
		return parseDelimited(text, ';', euWholeNameField)
	default: // ofac_sdn, ofac_consolidated
		return parseDelimited(text, ',', 1)
	}
}

// parseDelimited pulls field `idx` from each line of a delimited file, tolerating
// simple double-quoted fields. It scans up to `idx` fields and stops.
func parseDelimited(text string, delim byte, idx int) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		fields := splitDelimited(line, delim)
		if idx < len(fields) {
			n := strings.Trim(strings.TrimSpace(fields[idx]), `"`)
			// OFAC uses -0- as an empty placeholder.
			if n != "" && n != "-0-" {
				out = append(out, n)
			}
		}
	}
	return out
}

func splitDelimited(line string, delim byte) []string {
	var fields []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '"':
			inQuote = !inQuote
		case c == delim && !inQuote:
			fields = append(fields, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	fields = append(fields, cur.String())
	return fields
}

var wsRun = regexp.MustCompile(`\s+`)

func normalizeName(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	return wsRun.ReplaceAllString(s, " ")
}
