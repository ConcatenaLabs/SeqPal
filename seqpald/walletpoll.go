package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// M8 wallet-transfer capture. A wallet-linked holder transfers a restricted asset
// in the live Sequentia wallet (the wallet co-signs with the policy server
// directly). seqpald cannot build that transfer, so it CAPTURES it after the fact:
// this poller reads openampd's public /v1/log, and for each settled "transfer" of
// a SeqPal-managed asset whose sender is a registered platform identity (and not
// one of seqpald's own primary enclaves, whose transfers are closings /
// distributions, not secondary trades), it records a P2P travel-rule row and joins
// both counterparties to identities server-side. The beneficiary is resolved by
// matching the transaction's outputs (via the box explorer) against the current
// holders' enclave addresses.
//
// The cursor (last processed log seq) is persisted in kv, so a restart resumes
// without rescanning, and capture is idempotent by txid (plus a browser-row guard),
// so a transfer seqpald built itself is never double-captured.

const walletPollCursorKey = "walletpoll_seq"

func (s *server) runWalletTransferPoller(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for range t.C {
		s.pollWalletTransfers()
	}
}

// logLine is the subset of an openampd /v1/log entry the poller reads.
type logLine struct {
	Seq    uint64 `json:"seq"`
	Time   string `json:"time"`
	Action string `json:"action"`
	Data   struct {
		Asset   string `json:"asset"`
		Sender  string `json:"sender"`
		Atoms   uint64 `json:"atoms"`
		Txid    string `json:"txid"`
		Refused bool   `json:"refused"`
	} `json:"data"`
}

func (s *server) pollWalletTransfers() {
	cur, _ := s.st.KVGet(walletPollCursorKey)
	lastSeq, _ := strconv.ParseUint(cur, 10, 64)

	req, err := http.NewRequest("GET", s.cfg.openampURL+"/v1/log", nil)
	if err != nil {
		return
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	maxSeq := lastSeq
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e logLine
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		if e.Seq > maxSeq {
			maxSeq = e.Seq
		}
		if e.Seq <= lastSeq {
			continue
		}
		s.captureWalletTransfer(&e)
	}
	if maxSeq > lastSeq {
		_ = s.st.KVSet(walletPollCursorKey, strconv.FormatUint(maxSeq, 10))
	}
}

// captureWalletTransfer records one wallet-initiated secondary transfer, joining
// both counterparties to registered identities. It is defensive: any condition
// that means "not a SeqPal secondary trade to capture" is a quiet skip.
func (s *server) captureWalletTransfer(e *logLine) {
	if e.Action != "transfer" || e.Data.Refused || e.Data.Txid == "" || e.Data.Asset == "" || e.Data.Sender == "" {
		return
	}
	iss, err := s.st.IssuanceByAsset(e.Data.Asset)
	if err != nil || iss == nil {
		return // not a SeqPal-managed asset
	}
	// A transfer FROM one of seqpald's own primary enclaves is a closing / DR
	// operation, not a secondary trade; the sender must be a registered investor.
	if k, _ := s.st.EnclaveKeyByAID(e.Data.Sender); k != nil {
		return
	}
	sender, err := s.st.AccountByAID(e.Data.Sender)
	if err != nil || sender == nil {
		return // sender is not a registered platform identity
	}
	// Idempotent: skip a txid already recorded, and skip a move a browser-initiated
	// transfer already owns (closing the capture race with seqpald's own path).
	if prev, _ := s.st.P2PTransferByTxid(e.Data.Txid); prev != nil {
		return
	}
	if owned, _ := s.st.P2PBrowserExistsForMove(e.Data.Asset, e.Data.Sender, e.Data.Atoms); owned {
		return
	}

	benefAID := s.resolveTransferBeneficiary(e.Data.Asset, e.Data.Sender, e.Data.Txid)
	senderClaims, _ := s.st.ClaimsByAID(e.Data.Sender)
	t := &P2PTransfer{
		ID: mustID(), IssuanceID: iss.ID, AssetID: e.Data.Asset,
		OriginatorAID: e.Data.Sender, Atoms: e.Data.Atoms, State: "settled", Source: "wallet",
		Txid: e.Data.Txid, OrigName: sender.DisplayName, OrigResidence: claimsResidence(senderClaims),
	}
	if benefAID != "" {
		if benef, _ := s.st.AccountByAID(benefAID); benef != nil {
			benefClaims, _ := s.st.ClaimsByAID(benefAID)
			t.BeneficiaryAID = benefAID
			t.BenefName = benef.DisplayName
			t.BenefResidence = claimsResidence(benefClaims)
			t.BenefRegistered = true
		} else {
			t.BeneficiaryAID = benefAID // a holder AID not registered as a platform identity
		}
	}
	if err := s.st.InsertP2PTransfer(t); err != nil {
		return
	}
	_ = s.st.InsertLedger(&LedgerEntry{
		IssuanceID: iss.ID, Kind: "p2p_transfer", Rail: "asset", Amount: e.Data.Atoms, Ccy: iss.Ticker, Txid: e.Data.Txid,
	})
	s.st.Audit(e.Data.Sender, "p2p.capture", map[string]any{
		"transfer": t.ID, "asset": e.Data.Asset, "beneficiary": t.BeneficiaryAID,
		"beneficiary_registered": t.BenefRegistered, "atoms": e.Data.Atoms, "txid": e.Data.Txid, "source": "wallet",
	})
}

// resolveTransferBeneficiary matches the transaction's output addresses against the
// current holders' enclave addresses for the asset, returning the beneficiary AID
// (the non-sender holder whose enclave address received an output), or "" when it
// cannot be resolved (a blinded output, an unregistered recipient, or the explorer
// being unavailable). It reads outputs from the box explorer, which indexes every
// tx without the node needing -txindex.
func (s *server) resolveTransferBeneficiary(assetID, senderAID, txid string) string {
	outAddrs := s.esploraTxOutAddresses(txid)
	if len(outAddrs) == 0 {
		return ""
	}
	var rep struct {
		Holders map[string]uint64 `json:"holders"`
	}
	if err := s.callOpenAMP("GET", "/v1/issuer/holders?asset="+assetID, s.cfg.issuerToken, nil, &rep); err != nil {
		return ""
	}
	for aid := range rep.Holders {
		if aid == senderAID {
			continue
		}
		var out struct {
			Address string `json:"address"`
		}
		if err := s.callOpenAMP("GET", "/v1/users/"+s.openampAIDFor(aid)+"/address?asset="+assetID, "", nil, &out); err != nil {
			continue
		}
		if out.Address != "" && outAddrs[out.Address] {
			return aid
		}
	}
	return ""
}

// esploraTxOutAddresses returns the set of output addresses of a txid from the box
// explorer's esplora API (the same explorer node.go already uses for tx status).
func (s *server) esploraTxOutAddresses(txid string) map[string]bool {
	if s.cfg.electrsURL == "" {
		return nil
	}
	url := strings.TrimRight(s.cfg.electrsURL, "/") + "/tx/" + txid
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	raw, _ := readAllLimited(resp.Body, 4<<20)
	var tx struct {
		Vout []struct {
			ScriptpubkeyAddress string `json:"scriptpubkey_address"`
		} `json:"vout"`
	}
	if json.Unmarshal(raw, &tx) != nil {
		return nil
	}
	set := map[string]bool{}
	for _, v := range tx.Vout {
		if v.ScriptpubkeyAddress != "" {
			set[v.ScriptpubkeyAddress] = true
		}
	}
	return set
}
