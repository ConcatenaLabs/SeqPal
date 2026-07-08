// seqpald is SeqPal's thin deployment backend.
//
// The OpenAMP policy server gates asset issuance behind a bearer token, which
// is a server-side secret and must never reach the browser. seqpald is the one
// component that holds it: the SeqPal front-end sends an issuer's enclave public
// key plus the asset parameters, and seqpald registers the account and mints the
// restricted asset through OpenAMP's issuer API. Everything else the front-end
// needs (assets, balances, addresses, the transparency log) it reads directly
// from OpenAMP's public endpoints.
//
// It holds no keys of its own and no database: it is a stateless, audited seam
// between the public SeqPal UI and the privileged OpenAMP issuer endpoint.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type config struct {
	listen       string
	openampURL   string // e.g. http://127.0.0.1:8722 (no trailing slash, no /v1)
	issuerToken  string
	confidential bool   // does this deployment's node support confidential issuance?
	network      string
	webroot      string // built SPA to serve at / (empty = API only)
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	cfg := config{}
	flag.StringVar(&cfg.listen, "listen", env("SEQPALD_LISTEN", "127.0.0.1:8730"), "HTTP listen address")
	flag.StringVar(&cfg.openampURL, "openamp", env("OPENAMPD_URL", "http://127.0.0.1:8722"), "OpenAMP policy server base URL")
	flag.StringVar(&cfg.issuerToken, "issuertoken", env("OPENAMPD_ISSUER_TOKEN", ""), "OpenAMP issuer bearer token")
	flag.StringVar(&cfg.network, "network", env("SEQPALD_NETWORK", "sequentia-testnet"), "network label reported to the UI")
	confDefault := env("SEQPALD_CONFIDENTIAL", "") == "1" || env("SEQPALD_CONFIDENTIAL", "") == "true"
	flag.BoolVar(&cfg.confidential, "confidential", confDefault, "node supports confidential issuance")
	flag.StringVar(&cfg.webroot, "webroot", env("SEQPALD_WEBROOT", ""), "built SPA directory to serve at / (empty = API only)")
	flag.Parse()

	cfg.openampURL = strings.TrimRight(cfg.openampURL, "/")
	if cfg.issuerToken == "" {
		log.Println("warning: no issuer token set; deployments will fail (set OPENAMPD_ISSUER_TOKEN)")
	}

	s := &server{cfg: cfg, http: &http.Client{Timeout: 60 * time.Second}}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("POST /api/deploy", s.handleDeploy)
	mux.HandleFunc("OPTIONS /api/deploy", func(w http.ResponseWriter, r *http.Request) { cors(w); w.WriteHeader(204) })
	// Serve the built SPA (and its client-side routes) when a webroot is set.
	// Caddy strips the /seqpal prefix before proxying, so the browser's absolute
	// asset paths (/seqpal/assets/...) arrive here as /assets/...; SPA routes
	// that aren't real files fall back to index.html. seqpald runs as root, so
	// this avoids Caddy's inability to read files under /root (mode 700).
	if cfg.webroot != "" {
		mux.HandleFunc("/", s.handleStatic)
	}

	log.Printf("seqpald listening on %s, OpenAMP at %s (confidential=%v, webroot=%q)", cfg.listen, cfg.openampURL, cfg.confidential, cfg.webroot)
	log.Fatal(http.ListenAndServe(cfg.listen, mux))
}

type server struct {
	cfg  config
	http *http.Client
}

func cors(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "content-type")
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	cors(w)
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, format string, args ...any) {
	writeJSON(w, code, map[string]string{"error": fmt.Sprintf(format, args...)})
}

// handleStatic serves the built SPA with single-page-app fallback: an existing
// file is served directly; anything else returns index.html so the client
// router can handle the route.
func (s *server) handleStatic(w http.ResponseWriter, r *http.Request) {
	clean := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))
	fp := filepath.Join(s.cfg.webroot, filepath.FromSlash(clean))
	// Contain within webroot (defense against path traversal).
	if rel, err := filepath.Rel(s.cfg.webroot, fp); err != nil || strings.HasPrefix(rel, "..") {
		http.NotFound(w, r)
		return
	}
	if fi, err := os.Stat(fp); err == nil && !fi.IsDir() {
		http.ServeFile(w, r, fp)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.cfg.webroot, "index.html"))
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	// Report whether the OpenAMP server is reachable so the UI can degrade
	// gracefully rather than failing at deploy time.
	upstream := false
	if resp, err := s.http.Get(s.cfg.openampURL + "/v1/assets"); err == nil {
		upstream = resp.StatusCode < 500
		resp.Body.Close()
	}
	writeJSON(w, 200, map[string]any{
		"ok":           upstream,
		"confidential": s.cfg.confidential,
		"network":      s.cfg.network,
	})
}

// deployReq is what the SeqPal front-end sends.
type deployReq struct {
	Name           string `json:"name"`
	Ticker         string `json:"ticker"`
	IssuerPubkey   string `json:"issuer_pubkey"` // x-only hex
	Supply         uint64 `json:"supply"`        // whole tokens
	Precision      int    `json:"precision"`
	Clawback       *bool  `json:"clawback"`
	Confidential   bool   `json:"confidential"`
	FeeConvertAtom uint64 `json:"fee_convert_atoms"`
	TermsHash      string `json:"terms_hash"`
}

func (s *server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	var req deployReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		writeErr(w, 400, "bad request body")
		return
	}
	if len(req.IssuerPubkey) != 64 {
		writeErr(w, 400, "issuer_pubkey must be a 32-byte x-only hex key")
		return
	}
	if req.Name == "" || req.Ticker == "" {
		writeErr(w, 400, "name and ticker are required")
		return
	}
	if req.Precision <= 0 || req.Precision > 8 {
		req.Precision = 8
	}
	if req.Supply == 0 {
		req.Supply = 1_000_000
	}
	if req.Confidential && !s.cfg.confidential {
		writeErr(w, 501, "confidential issuance is not available on this deployment; the node is not confidentiality-enabled (enabled on mainnet)")
		return
	}
	if s.cfg.issuerToken == "" {
		writeErr(w, 503, "deployment backend is not configured with an issuer token")
		return
	}

	// atoms = supply * 10^precision, guarded against uint64 overflow.
	scale := math.Pow10(req.Precision)
	if float64(req.Supply)*scale > 9.0e18 {
		writeErr(w, 400, "supply too large for the chosen precision")
		return
	}
	atoms := req.Supply * pow10(req.Precision)

	// 1. Register the issuer's enclave key as an OpenAMP account.
	aid, err := s.registerUser(req.IssuerPubkey)
	if err != nil {
		writeErr(w, 502, "register account: %v", err)
		return
	}

	// 2. Mint the restricted asset into that account's enclave. The issuer is
	//    also the initial holder (the issuer-of-record treasury); distribution
	//    to investors happens later through OpenAMP transfers.
	clawback := true
	if req.Clawback != nil {
		clawback = *req.Clawback
	}
	issueBody := map[string]any{
		"name":         req.Name,
		"ticker":       req.Ticker,
		"precision":    req.Precision,
		"atoms":        atoms,
		"holder_aid":   aid,
		"issuer_aid":   aid,
		"clawback":     clawback,
		"burn_allowed": false,
		"confidential": req.Confidential,
		"rules":        map[string]any{"fee_convert_atoms": req.FeeConvertAtom},
	}
	if req.TermsHash != "" {
		issueBody["terms_hash"] = req.TermsHash
	}

	var issued struct {
		Asset        string `json:"asset"`
		Txid         string `json:"txid"`
		ContractHash string `json:"contract_hash"`
		Error        string `json:"error"`
	}
	if err := s.callOpenAMP("POST", "/v1/issuer/assets", s.cfg.issuerToken, issueBody, &issued); err != nil {
		writeErr(w, 502, "issue: %v", err)
		return
	}

	// 3. Best-effort: fetch the enclave receive address for display.
	var addr struct {
		Address string `json:"address"`
	}
	_ = s.callOpenAMP("GET", "/v1/users/"+aid+"/address?asset="+issued.Asset, "", nil, &addr)

	writeJSON(w, 200, map[string]any{
		"asset":         issued.Asset,
		"txid":          issued.Txid,
		"contract_hash": issued.ContractHash,
		"aid":           aid,
		"address":       addr.Address,
	})
}

func (s *server) registerUser(xonly string) (string, error) {
	var out struct {
		AID string `json:"aid"`
	}
	err := s.callOpenAMP("POST", "/v1/users", "", map[string]any{"pubkeys": []string{xonly}}, &out)
	if err != nil {
		return "", err
	}
	if out.AID == "" {
		return "", fmt.Errorf("no aid returned")
	}
	return out.AID, nil
}

// callOpenAMP issues an authenticated (if token != "") JSON request to the
// policy server and decodes the response into out (may be nil).
func (s *server) callOpenAMP(method, path, token string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
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

func pow10(n int) uint64 {
	out := uint64(1)
	for i := 0; i < n; i++ {
		out *= 10
	}
	return out
}
