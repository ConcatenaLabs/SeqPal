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

	// M5 money engine.
	btcURL       string        // testnet4 (parent-chain) Bitcoin node RPC for the native-BTC escrow
	btcUser      string        // testnet4 RPC username (mainchainrpcuser)
	btcPass      string        // testnet4 RPC password (mainchainrpcpassword)
	usdxAsset    string        // USDX asset id (Sequentia payment asset)
	escrowConfs  int64         // confirmations before a deposit becomes in_escrow
	atomicClose  bool          // M6: settle USDX subscriptions as ONE atomic DvP tx (closing v2); v1 fallback otherwise
	setupFeeUSD  float64       // SeqPal platform setup fee (USD), blocks deploy until paid
	escrowFeeBps int64         // SeqPal escrow fee in basis points, deducted at release
	fiatSettle   time.Duration // simulated fiat pending->settled delay

	// Cron cadences (fast defaults so a demo runs unattended).
	screenRefresh   time.Duration // re-download the lists
	screenInterval  time.Duration // re-screen all identities
	expiryInterval  time.Duration // category expiry sweep
	autoReviewEvery time.Duration // auto-reviewer poll
	autoReviewDelay time.Duration // grace before the auto-reviewer acts
	watchInterval   time.Duration // chain watcher tick
	anchorInterval  time.Duration // log-head anchor cron

	// M7 (Backend-B) servicing cadences.
	rulesReconcile   time.Duration // heal half-applied rules mutations (amendment chain)
	snapshotInterval time.Duration // scheduled ownership snapshot + anchor
	reportInterval   time.Duration // labeled-simulated annual report to holders

	// M8 secondary-market cadence.
	walletPollInterval time.Duration // wallet-initiated transfer capture from openampd /v1/log
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

	// M5 money engine.
	escrow  *escrowState // idempotent escrow-wallet provisioning guard
	closeMu *keyedMutex  // serializes the closing engine per issuance

	// M7 servicing.
	distMu       *keyedMutex // serializes the distribution engine per run
	rulesMu      *keyedMutex // serializes rules mutations per issuance (amendment chain)
	clawMu       *keyedMutex // serializes clawbacks per (asset, holder)
	redeliverMu  *keyedMutex // serializes the stranded-key runbook per (issuance, old, new)
	drMu         *keyedMutex // serializes DR mint/redeem per issuance (M8)
	servicingMu1 sync.Once   // lazily provisions the servicing mutexes above
}

// ensureServicingMu lazily provisions the M7 (Backend-B) servicing mutexes so a
// directly-constructed server (the test harness) needs no extra ceremony; main()
// still sets them explicitly, in which case this is a no-op.
func (s *server) ensureServicingMu() {
	s.servicingMu1.Do(func() {
		if s.distMu == nil {
			s.distMu = newKeyedMutex()
		}
		if s.rulesMu == nil {
			s.rulesMu = newKeyedMutex()
		}
		if s.clawMu == nil {
			s.clawMu = newKeyedMutex()
		}
		if s.redeliverMu == nil {
			s.redeliverMu = newKeyedMutex()
		}
		if s.drMu == nil {
			s.drMu = newKeyedMutex()
		}
	})
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

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
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
	flag.StringVar(&cfg.btcURL, "btcurl", env("SEQPALD_BTC_RPC_URL", ""), "testnet4 (parent-chain) Bitcoin node RPC URL for the native-BTC escrow (empty = BTC rail disabled)")
	flag.StringVar(&cfg.btcUser, "btcuser", env("SEQPALD_BTC_RPC_USER", ""), "testnet4 Bitcoin RPC username (mainchainrpcuser)")
	flag.StringVar(&cfg.btcPass, "btcpass", env("SEQPALD_BTC_RPC_PASS", ""), "testnet4 Bitcoin RPC password (mainchainrpcpassword)")
	flag.StringVar(&cfg.usdxAsset, "usdxasset", env("SEQPALD_USDX_ASSET", "2a515539da5e6a60caa7766ecd65bac0c10d15717ddd2088844ba58f4d04b9de"), "USDX asset id (Sequentia payment asset)")
	flag.Int64Var(&cfg.escrowConfs, "escrowconfs", envInt("SEQPALD_ESCROW_CONFS", 1), "confirmations before a deposit becomes in_escrow")
	atomicDefault := env("SEQPALD_ATOMIC_CLOSE", "1") == "1" || strings.EqualFold(env("SEQPALD_ATOMIC_CLOSE", "1"), "true")
	flag.BoolVar(&cfg.atomicClose, "atomicclose", atomicDefault, "settle USDX subscriptions as one atomic delivery-versus-payment transaction (closing v2); falls back to the two-transaction close v1 when the policy server has no payment leg")
	flag.Parse()
	cfg.setupFeeUSD = envFloat("SEQPALD_SETUP_FEE_USD", 500)
	cfg.escrowFeeBps = envInt("SEQPALD_ESCROW_FEE_BPS", 50)

	cfg.adminAIDs = adminSet(adminAIDs)
	cfg.screenRefresh = 24 * time.Hour
	cfg.screenInterval = 24 * time.Hour
	cfg.expiryInterval = time.Hour
	cfg.autoReviewEvery = 5 * time.Second
	cfg.autoReviewDelay = 10 * time.Second
	cfg.watchInterval = 15 * time.Second
	cfg.anchorInterval = 24 * time.Hour
	cfg.fiatSettle = 8 * time.Second
	// Servicing cadences. Reconcile fast so a half-applied rules mutation heals
	// within a tick; snapshot daily; annual report yearly. All overridable in
	// seconds by env so a demo can run them unattended.
	cfg.rulesReconcile = time.Duration(envInt("SEQPALD_RULES_RECONCILE_SECS", 30)) * time.Second
	cfg.snapshotInterval = time.Duration(envInt("SEQPALD_SNAPSHOT_SECS", 24*3600)) * time.Second
	cfg.reportInterval = time.Duration(envInt("SEQPALD_REPORT_SECS", 365*24*3600)) * time.Second
	cfg.walletPollInterval = time.Duration(envInt("SEQPALD_WALLET_POLL_SECS", 15)) * time.Second
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
		cfg:         cfg,
		st:          st,
		http:        &http.Client{Timeout: 60 * time.Second},
		rl:          newRateLimiter(),
		catMu:       newKeyedMutex(),
		screen:      newScreener(cfg.screenDir),
		escrow:      newEscrowState(),
		closeMu:     newKeyedMutex(),
		distMu:      newKeyedMutex(),
		rulesMu:     newKeyedMutex(),
		clawMu:      newKeyedMutex(),
		redeliverMu: newKeyedMutex(),
		drMu:        newKeyedMutex(),
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
	mux.HandleFunc("GET /api/listings", s.handleListings)       // M8: issuer-granted listing authorization, public (serves the SeqDEX handover)

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

	// M5 money engine: the gated offering view (public teaser + gated full view),
	// gate capture, subscriptions and their per-rail deposits, the SIMULATED fiat
	// checkout, platform fees, payout mandates, and closing.
	mux.HandleFunc("GET /api/issuances/{id}/offering", s.handleOffering) // public: teaser or gated full view
	mux.HandleFunc("POST /api/issuances/{id}/gate", s.requireSession(s.handleGate))
	mux.HandleFunc("POST /api/issuances/{id}/subscribe", s.requireSession(s.handleSubscribe))
	mux.HandleFunc("GET /api/subscriptions", s.requireSession(s.handleMySubscriptions))
	mux.HandleFunc("GET /api/issuances/{id}/subscriptions", s.requireSession(s.handleIssuanceSubscriptions))
	mux.HandleFunc("GET /api/fiat/{id}", s.requireSession(s.handleFiatStatus))
	mux.HandleFunc("GET /api/issuances/{id}/fees", s.requireSession(s.handleFees))
	mux.HandleFunc("POST /api/issuances/{id}/fees/pay", s.requireSession(s.handlePayFee))
	mux.HandleFunc("GET /api/issuances/{id}/mandate", s.requireSession(s.handleMandates))
	mux.HandleFunc("POST /api/issuances/{id}/mandate", s.requireSession(s.handleMandate))
	mux.HandleFunc("POST /api/issuances/{id}/close", s.requireSession(s.handleClose))
	mux.HandleFunc("GET /api/issuances/{id}/settlements", s.requireSession(s.handleSettlements))

	// M7 transfer-agent servicing: investor payout mandates and the distribution
	// engine (create run + fund invoice, snapshot the register, execute pro-rata
	// net payouts to registered mandate addresses, read runs + per-holder txids).
	mux.HandleFunc("POST /api/mandates/investor", s.requireSession(s.handleInvestorMandate))
	mux.HandleFunc("GET /api/mandates/investor", s.requireSession(s.handleInvestorMandateGet))
	mux.HandleFunc("POST /api/issuances/{id}/distributions", s.requireSession(s.handleCreateDistribution))
	mux.HandleFunc("GET /api/issuances/{id}/distributions", s.requireSession(s.handleListDistributions))
	mux.HandleFunc("GET /api/issuances/{id}/distributions/{runID}", s.requireSession(s.handleGetDistribution))
	mux.HandleFunc("POST /api/issuances/{id}/distributions/{runID}/snapshot", s.requireSession(s.handleSnapshotDistribution))
	mux.HandleFunc("POST /api/issuances/{id}/distributions/{runID}/execute", s.requireSession(s.handleExecuteDistribution))

	// M7 servicing consoles: the freeze/clawback console (owner-scoped, reason
	// required), the holder notices inbox, and the stranded-key re-delivery runbook
	// (admin-scoped). The rules-amendment chain is exercised through the existing
	// POST /api/issuances/{id}/amendments (now a live mutation when new_rules is
	// supplied) and read, with its head-consistency invariant, at GET .../amendments.
	mux.HandleFunc("POST /api/issuances/{id}/freeze", s.requireSession(s.handleConsoleFreeze))
	mux.HandleFunc("POST /api/issuances/{id}/clawback", s.requireSession(s.handleConsoleClawback))
	// M9: complete a two-phase (external-issuer) clawback with the issuer's browser
	// signatures. Legacy (server-held issuer key) assets never reach here; their
	// clawback still completes in the single call above.
	mux.HandleFunc("POST /api/issuances/{id}/clawback/{cid}/complete", s.requireSession(s.handleConsoleClawbackComplete))
	mux.HandleFunc("GET /api/id/notices", s.requireSession(s.handleNotices))
	mux.HandleFunc("POST /api/id/redeliver", s.requireSession(s.requireAdmin(s.handleRedeliver)))

	// M8 secondary market: the market-abuse acknowledgment gate, policy-co-signed
	// holder-to-holder transfers (build + complete, with the first-class refusal
	// path and travel-rule capture), and the caller's transfer history.
	mux.HandleFunc("GET /api/id/market-abuse-ack", s.requireSession(s.handleMarketAbuseAckGet))
	mux.HandleFunc("POST /api/id/market-abuse-ack", s.requireSession(s.handleMarketAbuseAck))
	mux.HandleFunc("GET /api/transfers", s.requireSession(s.handleListMyTransfers))
	mux.HandleFunc("POST /api/transfers", s.requireSession(s.handleP2PInitiate))
	mux.HandleFunc("POST /api/transfers/{id}/complete", s.requireSession(s.handleP2PComplete))

	// M8 issuer surfaces: listing authorization grant and the Depository-Receipt
	// programme (enable + US-person exclusion, mint = reissuance, redeem = burn,
	// chain-derived supply).
	mux.HandleFunc("POST /api/issuances/{id}/listing", s.requireSession(s.handleGrantListing))
	mux.HandleFunc("GET /api/issuances/{id}/dr", s.requireSession(s.handleDRProgram))
	mux.HandleFunc("POST /api/issuances/{id}/dr/enable", s.requireSession(s.handleDREnable))
	mux.HandleFunc("POST /api/issuances/{id}/dr/mint", s.requireSession(s.handleDRMint))
	mux.HandleFunc("POST /api/issuances/{id}/dr/redeem", s.requireSession(s.handleDRRedeem))
	mux.HandleFunc("GET /api/issuances/{id}/dr/supply", s.requireSession(s.handleDRSupply))

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
	_, err := s.callOpenAMPStatus(method, path, token, body, out)
	return err
}

// callOpenAMPStatus is callOpenAMP that also returns the policy server's HTTP
// status code. It is how the P2P transfer path distinguishes a first-class policy
// REFUSAL (403 with a reason: an ineligible recipient, a resale inside the lockup
// window, a Reg S distribution-compliance window) from an operational error, so a
// refusal is surfaced honestly rather than swallowed as a 502. The error and its
// message are identical to callOpenAMP's, so every existing caller is byte-for-byte
// unchanged.
func (s *server) callOpenAMPStatus(method, path, token string, body any, out any) (int, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rdr = bytes.NewReader(b)
	}
	httpReq, err := http.NewRequest(method, s.cfg.openampURL+path, rdr)
	if err != nil {
		return 0, err
	}
	if body != nil {
		httpReq.Header.Set("content-type", "application/json")
	}
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := s.http.Do(httpReq)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		// Surface OpenAMP's own error message when present.
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return resp.StatusCode, fmt.Errorf("%s", e.Error)
		}
		return resp.StatusCode, fmt.Errorf("%s %s -> %d", method, path, resp.StatusCode)
	}
	if out != nil {
		return resp.StatusCode, json.Unmarshal(raw, out)
	}
	return resp.StatusCode, nil
}
