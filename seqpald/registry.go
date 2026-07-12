package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

var assetIDRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

const assetProofPrefix = "sequentia-asset-proof-"

// regThrottle rate-limits registry publication retries so a permanently
// rejected contract (for example one the box registry has not yet been extended
// to accept) is not re-POSTed on every watcher tick.
type regThrottle struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (t *regThrottle) allow(id string, every time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.last == nil {
		t.last = map[string]time.Time{}
	}
	if last, ok := t.last[id]; ok && time.Since(last) < every {
		return false
	}
	t.last[id] = time.Now()
	return true
}

// maybePublishRegistry posts the issued asset's contract to the Sequentia asset
// registry so the ticker resolves at /registry/{id} and feeds /prices discovery.
// It runs once the issuance is confirmed (the registry verifies the on-chain
// commitment via electrs, which needs the issuance indexed), sends the EXACT
// canonical contract bytes openampd committed (so the registry's contract_hash
// check passes), records the resolvable registry URL on the watch row, and is
// best-effort: an unreachable or rejecting registry is logged and the deploy is
// unaffected.
func (s *server) maybePublishRegistry(w *WatchState) {
	if w.RegistryURL != "" || s.cfg.registryURL == "" || w.AssetID == "" {
		return
	}
	if !s.regThrottle.allow(w.IssuanceID, 10*time.Minute) {
		return
	}

	// The canonical contract bytes: prefer the ones captured at deploy; otherwise
	// fetch the exact stored bytes from openampd so a reconciled row still
	// publishes a contract whose hash matches the on-chain commitment.
	contract := w.Contract
	if contract == "" {
		var asset struct {
			Contract json.RawMessage `json:"contract"`
		}
		if err := s.callOpenAMP("GET", "/v1/assets/"+w.AssetID, "", nil, &asset); err != nil || len(asset.Contract) == 0 {
			return
		}
		contract = string(asset.Contract)
	}
	var contractObj json.RawMessage = json.RawMessage(contract)

	body, _ := json.Marshal(map[string]any{"asset_id": w.AssetID, "contract": contractObj})
	base := strings.TrimRight(s.cfg.registryURL, "/")
	req, err := http.NewRequest("POST", base+"/", strings.NewReader(string(body)))
	if err != nil {
		return
	}
	req.Header.Set("content-type", "application/json")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("registry publish %s: %v", w.AssetID, err)
		return
	}
	defer resp.Body.Close()
	raw, _ := readAllLimited(resp.Body, 1<<20)
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		log.Printf("registry publish %s: %d %s", w.AssetID, resp.StatusCode, e.Error)
		return
	}
	var entry struct {
		Verified bool `json:"verified"`
	}
	_ = json.Unmarshal(raw, &entry)
	w.RegistryURL = base + "/" + w.AssetID
	if err := s.st.UpsertWatch(w); err == nil {
		s.bus().publish(w.OwnerAID, eventFrom(w))
		s.st.Audit(w.OwnerAID, "registry.publish", map[string]any{
			"issuance_id": w.IssuanceID, "asset": w.AssetID,
			"registry_url": w.RegistryURL, "verified": entry.Verified,
		})
	}
}

// assetProofText is the exact authorization line the registry's domain-proof
// check requires the body to equal (trimmed), served as text/plain.
func assetProofText(domain, assetID string) string {
	return fmt.Sprintf("Authorize linking the domain name %s to the Sequentia asset %s", domain, assetID)
}

// handleAssetProof serves GET /.well-known/sequentia-asset-proof-<assetid> so the
// registry's domain-proof step can verify the entity domain controls the asset.
// It only authorizes assets seqpald actually deployed (present in the watch
// table), never an arbitrary asset id, and serves the line as text/plain exactly
// as the registry requires. Reaching it at the domain root depends on the box
// routing /.well-known to seqpald; where the registry runs with
// REQUIRE_DOMAIN_PROOF off, discovery-only publication is acceptable.
func (s *server) handleAssetProof(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/.well-known/")
	if !strings.HasPrefix(name, assetProofPrefix) {
		http.NotFound(w, r)
		return
	}
	assetID := strings.ToLower(strings.TrimPrefix(name, assetProofPrefix))
	if !assetIDRE.MatchString(assetID) {
		http.NotFound(w, r)
		return
	}
	if s.cfg.entityDomain == "" {
		http.NotFound(w, r)
		return
	}
	got, err := s.st.WatchByAsset(assetID)
	if err != nil || got == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(200)
	fmt.Fprint(w, assetProofText(s.cfg.entityDomain, assetID))
}
