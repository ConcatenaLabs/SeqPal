// seqpald is SeqPal's platform backend.
//
// It holds three things the browser must never hold: the OpenAMP issuer bearer
// token, the books and records (accounts, entities, issuances, deployments), and
// the append-only audit chain. Principals authenticate by proving possession of
// their enclave key (BIP340 over a tagged challenge), never with a password, and
// every ownership decision is derived from the session's AID rather than from
// anything the client sends. The browser keeps only an encrypted key, a session
// cookie, and UI state; no financial fact is asserted by the browser.
//
// Interface contract: seqpald/M1-CONTRACT.md.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type config struct {
	listen       string
	dbPath       string
	openampURL   string // e.g. http://127.0.0.1:8722 (no trailing slash, no /v1)
	issuerToken  string
	confidential bool // does this deployment's node support confidential issuance?
	network      string
	webroot      string   // built SPA to serve at / (empty = API only)
	devOrigins   []string // extra allowed CORS origins for local development

	// M2 compliance engine.
	screenDir string          // cache dir for downloaded sanctions lists
	adminAIDs map[string]bool // AIDs allowed to use the manual-review surface

	// Chain-derived compile inputs. Tip height comes from the node RPC when
	// configured, else assumedTip. The M3 chain watcher uses the same node RPC.
	blocksPerDay int64
	assumedTip   int64
	nodeURL      string
	nodeUser     string
	nodePass     string

	// M3 truthful chain surfaces.
	electrsURL           string // box explorer (electrs) base; confirmation source when the node lacks -txindex
	registryURL          string // Sequentia asset registry base (publication target)
	priceURL             string // box price feed base (GET /prices, POST /seed)
	entityDomain         string // committed in the contract at issue for registry proof
	entityName           string // optional issuer display name (OA-1)
	operatorName         string // optional operator identity (OA-1)
	operatorRegistration string // optional operator registration (OA-1)
	policyFeeSats        int64  // network fee reference (SEQ-sats) for fee_convert derivation

	// Cron cadences (fast defaults so a demo runs unattended).
	screenRefresh   time.Duration // re-download the lists
	screenInterval  time.Duration // re-screen all identities
	expiryInterval  time.Duration // category expiry sweep
	autoReviewEvery time.Duration // auto-reviewer poll
	autoReviewDelay time.Duration // grace before the auto-reviewer acts
	watchInterval   time.Duration // chain watcher tick
	anchorInterval  time.Duration // log-head anchor cron
}

type server struct {
	cfg    config
	st     *Store
	http   *http.Client
	rl     *rateLimiter
	catMu  *keyedMutex // serializes openampd category writes per AID
	screen *screener   // sanctions lists

	// M3 chain surfaces.
	sse         *sseBroker  // SSE fan-out (lazily built via bus())
	busOnce     sync.Once   //
	regThrottle regThrottle // rate-limits registry publish retries
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func main() {
	cfg := config{}
	var devOrigins string
	flag.StringVar(&cfg.listen, "listen", env("SEQPALD_LISTEN", "127.0.0.1:8730"), "HTTP listen address")
	flag.StringVar(&cfg.dbPath, "db", env("SEQPALD_DB", "./seqpald.db"), "SQLite database path")
	flag.StringVar(&cfg.openampURL, "openamp", env("OPENAMPD_URL", "http://127.0.0.1:8722"), "OpenAMP policy server base URL")
	flag.StringVar(&cfg.issuerToken, "issuertoken", env("OPENAMPD_ISSUER_TOKEN", ""), "OpenAMP issuer bearer token")
	flag.StringVar(&cfg.network, "network", env("SEQPALD_NETWORK", "sequentia-testnet"), "network label reported to the UI")
	confDefault := env("SEQPALD_CONFIDENTIAL", "") == "1" || env("SEQPALD_CONFIDENTIAL", "") == "true"
	flag.BoolVar(&cfg.confidential, "confidential", confDefault, "node supports confidential issuance")
	flag.StringVar(&cfg.webroot, "webroot", env("SEQPALD_WEBROOT", ""), "built SPA directory to serve at / (empty = API only)")
	flag.StringVar(&devOrigins, "devorigins", env("SEQPALD_DEV_ORIGINS", ""), "comma-separated extra CORS origins for local development")
	var adminAIDs string
	flag.StringVar(&adminAIDs, "adminaids", env("SEQPALD_ADMIN_AIDS", ""), "comma-separated AIDs allowed to use the manual-review surface")
	flag.StringVar(&cfg.screenDir, "screendir", env("SEQPALD_SCREEN_DIR", "./sanctions-cache"), "cache directory for downloaded sanctions lists")
	flag.Int64Var(&cfg.blocksPerDay, "blocksperday", envInt("SEQPALD_BLOCKS_PER_DAY", 144), "assumed Sequentia blocks per day for lockup height conversion")
	flag.Int64Var(&cfg.assumedTip, "tipheight", envInt("SEQPALD_TIP_HEIGHT", 0), "fallback tip height when no node RPC is configured")
	flag.StringVar(&cfg.nodeURL, "nodeurl", env("SEQPALD_NODE_URL", ""), "Sequentia node JSON-RPC URL for the chain watcher and tip height (optional)")
	flag.StringVar(&cfg.nodeUser, "nodeuser", env("SEQPALD_NODE_USER", ""), "node RPC username")
	flag.StringVar(&cfg.nodePass, "nodepass", env("SEQPALD_NODE_PASS", ""), "node RPC password")
	flag.StringVar(&cfg.electrsURL, "electrsurl", env("SEQPALD_ELECTRS_URL", "http://127.0.0.1:3003"), "box explorer (electrs) base URL; confirmation source when the node lacks -txindex")
	flag.StringVar(&cfg.registryURL, "registryurl", env("SEQPALD_REGISTRY_URL", "http://127.0.0.1:3005"), "Sequentia asset registry base URL for publication (empty = disabled)")
	flag.StringVar(&cfg.priceURL, "priceurl", env("SEQPALD_PRICE_URL", "http://127.0.0.1:8088"), "box price feed base URL (GET /prices, POST /seed; empty = disabled)")
	flag.StringVar(&cfg.entityDomain, "entitydomain", env("SEQPALD_ENTITY_DOMAIN", "sequentiatestnet.com"), "entity domain committed in the contract for registry proof")
	flag.StringVar(&cfg.entityName, "entityname", env("SEQPALD_ENTITY_NAME", ""), "optional issuer display name added to the contract (OA-1)")
	flag.StringVar(&cfg.operatorName, "operatorname", env("SEQPALD_OPERATOR_NAME", ""), "optional operator identity added to the contract (OA-1)")
	flag.StringVar(&cfg.operatorRegistration, "operatorreg", env("SEQPALD_OPERATOR_REGISTRATION", ""), "optional operator registration added to the contract (OA-1)")
	flag.Int64Var(&cfg.policyFeeSats, "policyfeesats", envInt("SEQPALD_POLICY_FEE_SATS", 1000), "network fee reference in SEQ-sats used to derive fee_convert_atoms")
	flag.Parse()

	cfg.adminAIDs = adminSet(adminAIDs)
	cfg.screenRefresh = 24 * time.Hour
	cfg.screenInterval = 24 * time.Hour
	cfg.expiryInterval = time.Hour
	cfg.autoReviewEvery = 5 * time.Second
	cfg.autoReviewDelay = 10 * time.Second
	cfg.watchInterval = 15 * time.Second
	cfg.anchorInterval = 24 * time.Hour
	cfg.openampURL = strings.TrimRight(cfg.openampURL, "/")
	cfg.electrsURL = strings.TrimRight(cfg.electrsURL, "/")
	cfg.registryURL = strings.TrimRight(cfg.registryURL, "/")
	cfg.priceURL = strings.TrimRight(cfg.priceURL, "/")
	for _, o := range strings.Split(devOrigins, ",") {
		if o = strings.TrimSpace(o); o != "" {
			cfg.devOrigins = append(cfg.devOrigins, strings.TrimRight(o, "/"))
		}
	}
	if cfg.issuerToken == "" {
		log.Println("warning: no issuer token set; deployments will fail (set OPENAMPD_ISSUER_TOKEN)")
	}

	st, err := openStore(cfg.dbPath)
	if err != nil {
		log.Fatalf("open store %s: %v", cfg.dbPath, err)
	}
	defer st.Close()
	if err := st.PurgeExpired(); err != nil {
		log.Printf("warning: purge expired sessions and challenges: %v", err)
	}

	s := &server{
		cfg:    cfg,
		st:     st,
		http:   &http.Client{Timeout: 60 * time.Second},
		rl:     newRateLimiter(),
		catMu:  newKeyedMutex(),
		screen: newScreener(cfg.screenDir),
	}
	s.startWorkers()

	log.Printf("seqpald listening on %s, OpenAMP at %s (db=%s, confidential=%v, webroot=%q, admins=%d)",
		cfg.listen, cfg.openampURL, cfg.dbPath, cfg.confidential, cfg.webroot, len(cfg.adminAIDs))
	log.Fatal(http.ListenAndServe(cfg.listen, s.handler()))
}

// handler wires every route. Caddy strips the /seqpal prefix in production, so
// requests arrive here as /api/...; the prefix is stripped here too, so a direct
// hit on the listener behaves identically to one through the proxy.
func (s *server) handler() http.Handler {
	mux := http.NewServeMux()

	// Public.
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("POST /api/auth/challenge", s.handleChallenge)
	mux.HandleFunc("POST /api/auth/register", s.handleRegister)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("GET /api/eligibility", s.handleEligibility) // advisory preflight, public (serves the SeqDEX handover)

	// M4 legal artifact pipeline: public surfaces. The terms manifest is public
	// immediately; document preimages are offer-window gated inside handleDoc; the
	// characterization memo, the RFSA lookup, and document signatures are public.
	mux.HandleFunc("GET /api/terms/{hash}", s.handleTerms)
	mux.HandleFunc("GET /api/doc/{hash}", s.handleDoc)
	mux.HandleFunc("GET /api/characterization", s.handleCharacterization)
	mux.HandleFunc("GET /api/rfsa/filings/{number}", s.handleRFSALookup)
	mux.HandleFunc("GET /api/documents/{hash}/signatures", s.handleDocSignatures)

	// Session required.
	mux.HandleFunc("POST /api/auth/logout", s.requireSession(s.handleLogout))
	mux.HandleFunc("GET /api/me", s.requireSession(s.handleMe))
	mux.HandleFunc("POST /api/entities", s.requireSession(s.handleCreateEntity))
	mux.HandleFunc("GET /api/issuances", s.requireSession(s.handleListIssuances))
	mux.HandleFunc("POST /api/issuances", s.requireSession(s.handleCreateIssuance))
	mux.HandleFunc("PATCH /api/issuances/{id}", s.requireSession(s.handlePatchIssuance))
	mux.HandleFunc("POST /api/issuances/{id}/compile", s.requireSession(s.handleCompile))
	mux.HandleFunc("POST /api/deploy", s.requireSession(s.handleDeploy))

	// M4 legal artifact pipeline: session-gated actions.
	mux.HandleFunc("POST /api/issuances/{id}/documents", s.requireSession(s.handleGenerateDocuments))
	mux.HandleFunc("POST /api/issuances/{id}/offer-close", s.requireSession(s.handleOfferClose))
	mux.HandleFunc("GET /api/issuances/{id}/amendments", s.requireSession(s.handleAmendments))
	mux.HandleFunc("POST /api/issuances/{id}/amendments", s.requireSession(s.handleGenerateAmendment))
	mux.HandleFunc("POST /api/rfsa/filings", s.requireSession(s.handleRFSAFile))
	mux.HandleFunc("POST /api/documents/{hash}/sign", s.requireSession(s.handleSignDocument))

	// M3 truthful chain surfaces: the SSE stream and the owner-scoped reads.
	mux.HandleFunc("GET /api/events", s.requireSession(s.handleEvents))
	mux.HandleFunc("GET /api/issuances/{id}/holders", s.requireSession(s.handleIssuanceHolders))
	mux.HandleFunc("GET /api/issuances/{id}/log", s.requireSession(s.handleIssuanceLog))

	// SeqPal ID surface.
	mux.HandleFunc("POST /api/id/verify", s.requireSession(s.handleIDVerify))
	mux.HandleFunc("GET /api/id/passport", s.requireSession(s.handleIDPassport))
	mux.HandleFunc("POST /api/id/entities/{id}/verify", s.requireSession(s.handleEntityVerify))

	// Manual-review surface (session + admin).
	mux.HandleFunc("GET /api/admin/review-queue", s.requireSession(s.requireAdmin(s.handleReviewQueue)))
	mux.HandleFunc("POST /api/admin/review/{id}", s.requireSession(s.requireAdmin(s.handleReviewDecide)))

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeErr(w, 404, "no such endpoint")
	})

	// Public asset-registry domain proof. The registry fetches this at the domain
	// root; reaching it there depends on the box routing /.well-known to seqpald.
	mux.HandleFunc("GET /.well-known/", s.handleAssetProof)

	if s.cfg.webroot != "" {
		mux.HandleFunc("/", s.handleStatic)
	}

	return stripSeqpal(s.securityHeaders(s.cors(mux)))
}

func stripSeqpal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/seqpal/") {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/seqpal")
		}
		next.ServeHTTP(w, r)
	})
}

// securityHeaders sets the contract's header set on every response. connect-src
// 'self' holds because OpenAMP is same-origin behind Caddy in production and
// behind the vite proxy in development.
func (s *server) securityHeaders(next http.Handler) http.Handler {
	const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; " +
		"base-uri 'none'; form-action 'self'; object-src 'none'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Permissions-Policy", "geolocation=(), camera=(), microphone=()")
		next.ServeHTTP(w, r)
	})
}

// cors allows same-origin requests only, plus an explicit development allowlist.
// The old wildcard let any page on the internet drive the API with the browser's
// cookies; there is no wildcard here, and credentials are only ever granted to an
// origin we named.
func (s *server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			if s.originAllowed(origin, r) {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Credentials", "true")
				h.Set("Access-Control-Allow-Headers", "content-type")
				h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, OPTIONS")
				h.Add("Vary", "Origin")
			} else if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusForbidden)
				return
			}
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) originAllowed(origin string, r *http.Request) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if u.Host == r.Host {
		return true
	}
	origin = strings.TrimRight(origin, "/")
	for _, allowed := range s.cfg.devOrigins {
		if strings.EqualFold(allowed, origin) {
			return true
		}
	}
	return false
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxRequestBody))
	return dec.Decode(v)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, format string, args ...any) {
	writeJSON(w, code, map[string]string{"error": fmt.Sprintf(format, args...)})
}

// callOpenAMP issues an authenticated (if token != "") JSON request to the policy
// server and decodes the response into out (may be nil).
func (s *server) callOpenAMP(method, path, token string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	httpReq, err := http.NewRequest(method, s.cfg.openampURL+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		httpReq.Header.Set("content-type", "application/json")
	}
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := s.http.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		// Surface OpenAMP's own error message when present.
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return fmt.Errorf("%s", e.Error)
		}
		return fmt.Errorf("%s %s -> %d", method, path, resp.StatusCode)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}
