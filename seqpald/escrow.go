package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// Escrow wallets are the money-holding surface distinct from the per-offering
// restricted-asset enclave (which holds the mint and is the delivery SENDER).
// USDX subscriptions land in a dedicated Sequentia node wallet (seqpal-escrow);
// native BTC subscriptions land in a dedicated testnet4 wallet
// (seqpal-btc-escrow). Both are created/loaded idempotently and are NEVER any
// pre-existing wallet (treasury2, seqdex-mm-btc, seqln-*). Release and refund pay
// out of these wallets only.

const (
	seqEscrowWallet = "seqpal-escrow"
	btcEscrowWallet = "seqpal-btc-escrow"
)

// walletOnce guards one-time idempotent provisioning per wallet name so
// concurrent subscriptions do not race a createwallet.
type escrowState struct {
	mu    sync.Mutex
	ready map[string]bool
}

func newEscrowState() *escrowState { return &escrowState{ready: map[string]bool{}} }

// ensureSeqEscrowWallet loads or creates the USDX escrow wallet on the Sequentia
// node. Idempotent: an already-loaded wallet is a success, not an error.
func (s *server) ensureSeqEscrowWallet() error {
	return s.escrow.ensure("seq:"+seqEscrowWallet, func() error {
		return ensureWalletVia(func(method string, params ...any) (json.RawMessage, error) {
			return s.nodeRPC(method, params...)
		}, seqEscrowWallet)
	})
}

// ensureBTCEscrowWallet loads or creates the native-BTC escrow wallet on the
// testnet4 parent-chain node.
func (s *server) ensureBTCEscrowWallet() error {
	return s.escrow.ensure("btc:"+btcEscrowWallet, func() error {
		return ensureWalletVia(func(method string, params ...any) (json.RawMessage, error) {
			return s.btcRPC("", method, params...)
		}, btcEscrowWallet)
	})
}

func (e *escrowState) ensure(key string, fn func() error) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ready[key] {
		return nil
	}
	if err := fn(); err != nil {
		return err
	}
	e.ready[key] = true
	return nil
}

// ensureWalletVia performs the load-then-create idempotency dance using a
// node-level RPC caller (createwallet/loadwallet are node-scoped, not
// wallet-scoped, so the caller targets the node root).
func ensureWalletVia(nodeCall func(method string, params ...any) (json.RawMessage, error), name string) error {
	_, err := nodeCall("loadwallet", name)
	if err == nil {
		return nil
	}
	switch code := rpcCode(err); {
	case code == -35: // RPC_WALLET_ALREADY_LOADED
		return nil
	// "already loaded" is success whatever code carries it: newer node builds
	// phrase it inside a wallet-file-verification error ("Data file '...' is
	// already loaded") under a different code, and a loaded wallet is exactly
	// the state this function exists to reach.
	case strings.Contains(strings.ToLower(err.Error()), "already loaded"):
		return nil
	}
	// Not loadable: try to create it, then tolerate a concurrent create.
	_, cerr := nodeCall("createwallet", name)
	if cerr == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(cerr.Error()), "already exists") || rpcCode(cerr) == -4 {
		if _, lerr := nodeCall("loadwallet", name); lerr == nil || rpcCode(lerr) == -35 ||
			strings.Contains(strings.ToLower(fmt.Sprint(lerr)), "already loaded") {
			return nil
		}
	}
	return fmt.Errorf("provision wallet %s: load=%v create=%v", name, err, cerr)
}

// newUSDXDepositAddress derives a fresh USDX deposit address in seqpal-escrow.
// Transparent-by-default: getnewaddress yields the Bitcoin-format bech32 (tb1)
// unblinded address, so the deposit is a plain, watchable output.
func (s *server) newUSDXDepositAddress() (string, error) {
	if err := s.ensureSeqEscrowWallet(); err != nil {
		return "", err
	}
	res, err := s.walletRPC(seqEscrowWallet, "getnewaddress", "seqpal-subscription")
	if err != nil {
		return "", err
	}
	var addr string
	if err := json.Unmarshal(res, &addr); err != nil {
		return "", err
	}
	return addr, nil
}

// newBTCDepositAddress derives a fresh native-BTC (testnet4) deposit address in
// seqpal-btc-escrow.
func (s *server) newBTCDepositAddress() (string, error) {
	if err := s.ensureBTCEscrowWallet(); err != nil {
		return "", err
	}
	res, err := s.btcRPC(btcEscrowWallet, "getnewaddress", "seqpal-subscription", "bech32")
	if err != nil {
		return "", err
	}
	var addr string
	if err := json.Unmarshal(res, &addr); err != nil {
		return "", err
	}
	return addr, nil
}

// unspentEntry is the subset of listunspent both nodes return that the deposit
// watcher reads.
type unspentEntry struct {
	Txid          string      `json:"txid"`
	Address       string      `json:"address"`
	Amount        json.Number `json:"amount"`
	Asset         string      `json:"asset"`
	Confirmations int64       `json:"confirmations"`
}

// deposit is a detected credit to a subscription's deposit address.
type deposit struct {
	Txid          string
	Atoms         uint64 // atoms (USDX) or sats (BTC)
	Confirmations int64
}

// usdxDeposit reports the total USDX credited to a deposit address and the
// minimum confirmations across the crediting outputs (so "N confs" means every
// part of the deposit has N). ok is false when nothing has arrived yet.
func (s *server) usdxDeposit(address string) (deposit, bool, error) {
	return s.assetDeposit(address, s.cfg.usdxAsset)
}

// assetDeposit is usdxDeposit generalized to any Sequentia asset (the W-3
// corporate-action funding watcher pays dividends in the issuer's chosen
// asset).
func (s *server) assetDeposit(address, asset string) (deposit, bool, error) {
	if err := s.ensureSeqEscrowWallet(); err != nil {
		return deposit{}, false, err
	}
	res, err := s.walletRPC(seqEscrowWallet, "listunspent", 0, 9999999, []string{address}, true,
		map[string]any{"asset": asset})
	if err != nil {
		return deposit{}, false, err
	}
	return sumDeposit(res, address, asset)
}

// btcDeposit reports the total native BTC credited to a testnet4 deposit address.
func (s *server) btcDeposit(address string) (deposit, bool, error) {
	if err := s.ensureBTCEscrowWallet(); err != nil {
		return deposit{}, false, err
	}
	res, err := s.btcRPC(btcEscrowWallet, "listunspent", 0, 9999999, []string{address})
	if err != nil {
		return deposit{}, false, err
	}
	return sumDeposit(res, address, "")
}

// sumDeposit folds a listunspent result into a single deposit: total atoms/sats,
// the minimum confirmations, and a representative txid. wantAsset "" accepts any
// asset (native BTC); a set wantAsset keeps only matching outputs (USDX).
func sumDeposit(res json.RawMessage, address, wantAsset string) (deposit, bool, error) {
	var utxos []unspentEntry
	if err := json.Unmarshal(res, &utxos); err != nil {
		return deposit{}, false, err
	}
	var total uint64
	var minConf int64 = -1
	txid := ""
	found := false
	for _, u := range utxos {
		if address != "" && u.Address != address {
			continue
		}
		if wantAsset != "" && !strings.EqualFold(u.Asset, wantAsset) {
			continue
		}
		found = true
		total += atomsFromNumber(u.Amount)
		if minConf < 0 || u.Confirmations < minConf {
			minConf = u.Confirmations
		}
		if txid == "" {
			txid = u.Txid
		}
	}
	if !found {
		return deposit{}, false, nil
	}
	if minConf < 0 {
		minConf = 0
	}
	return deposit{Txid: txid, Atoms: total, Confirmations: minConf}, true, nil
}

// releaseUSDX pays USDX out of seqpal-escrow to the issuer's mandate address.
// Elements sendtoaddress carries the asset in the assetlabel position. The comment
// is a settlement-scoped marker so a lost-write re-close can reconcile the send
// (escrowFindSend) instead of re-broadcasting from the commingled wallet.
func (s *server) releaseUSDX(address string, atoms uint64, comment string) (string, error) {
	return s.releaseAsset(address, atoms, comment, s.cfg.usdxAsset)
}

// releaseAsset is releaseUSDX generalized to any Sequentia asset (W-3 dividend
// claims pay in the action's chosen asset). Same marker discipline.
func (s *server) releaseAsset(address string, atoms uint64, comment, asset string) (string, error) {
	if err := s.ensureSeqEscrowWallet(); err != nil {
		return "", err
	}
	res, err := s.walletRPC(seqEscrowWallet, "sendtoaddress",
		address, amount8(atoms), comment, "", false, false, 1, "UNSET", false, asset)
	if err != nil {
		return "", err
	}
	return decodeTxidResult(res)
}

// sendBTC pays native BTC out of seqpal-btc-escrow (release to an issuer mandate,
// or refund to a captured refund address). The comment is a settlement-scoped
// marker for reconciliation, as in releaseUSDX.
func (s *server) sendBTC(address string, sats uint64, comment string) (string, error) {
	if err := s.ensureBTCEscrowWallet(); err != nil {
		return "", err
	}
	res, err := s.btcRPC(btcEscrowWallet, "sendtoaddress", address, amount8(sats), comment)
	if err != nil {
		return "", err
	}
	return decodeTxidResult(res)
}

// escrowFindSend reconciles a release or refund after a lost write: it scans the
// escrow wallet's recent sends for one tagged with marker (the send comment) and
// returns its txid if a prior attempt already broadcast it. Because releases to one
// issuer's mandate share an address in a commingled wallet, the settlement-scoped
// comment (not the address) is what makes the match unambiguous. This gives release
// and refund the same never-double-spend guarantee delivery already has.
func (s *server) escrowFindSend(rail, marker string) (string, bool) {
	var raw json.RawMessage
	var err error
	switch rail {
	case "usdx":
		raw, err = s.walletRPC(seqEscrowWallet, "listtransactions", "*", 500, 0, true)
	case "btc":
		raw, err = s.btcRPC(btcEscrowWallet, "listtransactions", "*", 500, 0, true)
	default:
		return "", false
	}
	if err != nil {
		return "", false
	}
	var txs []struct {
		Category string `json:"category"`
		Comment  string `json:"comment"`
		Label    string `json:"label"`
		Txid     string `json:"txid"`
	}
	if json.Unmarshal(raw, &txs) != nil {
		return "", false
	}
	for _, t := range txs {
		if t.Category == "send" && (t.Comment == marker || t.Label == marker) {
			return t.Txid, true
		}
	}
	return "", false
}

func decodeTxidResult(res json.RawMessage) (string, error) {
	var txid string
	if err := json.Unmarshal(res, &txid); err == nil && txid != "" {
		return txid, nil
	}
	// Some nodes return {txid, ...} for sendtoaddress with verbose.
	var obj struct {
		Txid string `json:"txid"`
	}
	if err := json.Unmarshal(res, &obj); err == nil && obj.Txid != "" {
		return obj.Txid, nil
	}
	return "", fmt.Errorf("send returned no txid")
}

// amount8 renders an atom/sat count as an exact 8-decimal JSON number (no float
// rounding), the form Bitcoin/Elements sendtoaddress expects.
func amount8(atoms uint64) json.Number {
	return json.Number(fmt.Sprintf("%d.%08d", atoms/1e8, atoms%1e8))
}

// atomsFromNumber converts a whole-unit JSON amount (e.g. "1.50000000") to atoms
// without binary-float error, by parsing the decimal string directly.
func atomsFromNumber(n json.Number) uint64 {
	str := string(n)
	if str == "" {
		return 0
	}
	neg := strings.HasPrefix(str, "-")
	str = strings.TrimPrefix(str, "-")
	intPart, fracPart := str, ""
	if i := strings.IndexByte(str, '.'); i >= 0 {
		intPart, fracPart = str[:i], str[i+1:]
	}
	if len(fracPart) > 8 {
		fracPart = fracPart[:8]
	}
	for len(fracPart) < 8 {
		fracPart += "0"
	}
	var whole, frac uint64
	fmt.Sscanf(intPart, "%d", &whole)
	fmt.Sscanf(fracPart, "%d", &frac)
	total := whole*1e8 + frac
	if neg {
		return 0
	}
	return total
}
