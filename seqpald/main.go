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
	"strings"
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
}

type server struct {
	cfg  config
	st   *Store
	http *http.Client
	rl   *rateLimiter
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
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
	flag.Parse()

	cfg.openampURL = strings.TrimRight(cfg.openampURL, "/")
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
		cfg:  cfg,
		st:   st,
		http: &http.Client{Timeout: 60 * time.Second},
		rl:   newRateLimiter(),
	}

	log.Printf("seqpald listening on %s, OpenAMP at %s (db=%s, confidential=%v, webroot=%q)",
		cfg.listen, cfg.openampURL, cfg.dbPath, cfg.confidential, cfg.webroot)
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

	// Session required.
	mux.HandleFunc("POST /api/auth/logout", s.requireSession(s.handleLogout))
	mux.HandleFunc("GET /api/me", s.requireSession(s.handleMe))
	mux.HandleFunc("POST /api/entities", s.requireSession(s.handleCreateEntity))
	mux.HandleFunc("GET /api/issuances", s.requireSession(s.handleListIssuances))
	mux.HandleFunc("POST /api/issuances", s.requireSession(s.handleCreateIssuance))
	mux.HandleFunc("PATCH /api/issuances/{id}", s.requireSession(s.handlePatchIssuance))
	mux.HandleFunc("POST /api/deploy", s.requireSession(s.handleDeploy))

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeErr(w, 404, "no such endpoint")
	})

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
