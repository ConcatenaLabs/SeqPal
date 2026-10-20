package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"

	"github.com/ConcatenaLabs/SeqPal/seqpald/elements"
)

// A small UTXO chain inside the fakeNode, for the escrow covenant and the
// one-transaction distribution. It answers what that code asks a node --
// scantxoutset, gettxout, getblockchaininfo, getblockhash, the fee rates,
// testmempoolaccept, sendrawtransaction -- and its testmempoolaccept is a real
// check, not a yes: every input must exist unspent, a lock time must be final
// at the next height, and an input spent through an escrow leaf must carry a
// witness whose control block commits to the coin, whose BIP340 signature
// (keyed leaves) verifies over the Elements taproot sighash, whose
// CHECKLOCKTIMEVERIFY is met, and whose pinned output is the one the leaf
// names. A stub that said yes to everything could not tell a correct spend
// from a broken one.

var fakeGenesisDisplay = strings.Repeat("0f", 32)

type fakeUTXO struct {
	txid    string
	vout    uint32
	spk     string // hex
	asset   string // display hex
	atoms   uint64
	height  int64 // 0 = in the mempool
	spent   bool
}

type fakeChain struct {
	utxos      map[string]*fakeUTXO // "txid:vout"
	txs        map[string]string    // txid -> hex, every tx sendrawtransaction accepted
	broadcasts []string             // txids, in order, one per accepted sendrawtransaction call that was new
	sendCalls  int                  // every sendrawtransaction call, new or repeated
	mtp        int64
	nCoin      int
	rejectAll  string // when set, testmempoolaccept refuses everything with this reason
}

func (n *fakeNode) chain() *fakeChain {
	if n.fc == nil {
		n.fc = &fakeChain{utxos: map[string]*fakeUTXO{}, txs: map[string]string{}, mtp: 1790000000}
	}
	return n.fc
}

// addCoin puts a confirmed coin on the fake chain at the current tip.
func (n *fakeNode) addCoin(spk, asset string, atoms uint64) (string, uint32) {
	n.mu.Lock()
	defer n.mu.Unlock()
	c := n.chain()
	c.nCoin++
	txid := fmt.Sprintf("%064x", 0xc0000+c.nCoin)
	c.utxos[txid+":0"] = &fakeUTXO{txid: txid, vout: 0, spk: strings.ToLower(spk), asset: strings.ToLower(asset), atoms: atoms, height: n.height}
	return txid, 0
}

// mine confirms every mempool coin at the next height and advances the tip.
func (n *fakeNode) mine(blocks int64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	c := n.chain()
	for i := int64(0); i < blocks; i++ {
		n.height++
		for _, u := range c.utxos {
			if u.height == 0 {
				u.height = n.height
			}
		}
	}
}

func (n *fakeNode) setHeight(h int64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.height = h
}

func (n *fakeNode) broadcastCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.chain().broadcasts)
}

func (n *fakeNode) sendRawCalls() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.chain().sendCalls
}

func (n *fakeNode) txHex(txid string) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.chain().txs[txid]
}

func (n *fakeNode) unspent(txid string, vout uint32) *fakeUTXO {
	n.mu.Lock()
	defer n.mu.Unlock()
	u := n.chain().utxos[fmt.Sprintf("%s:%d", txid, vout)]
	if u == nil || u.spent {
		return nil
	}
	cp := *u
	return &cp
}

// walletCallsOf lists the wallet-scoped methods called against one wallet.
func (n *fakeNode) walletCallsOf(wallet string) []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []string
	for _, c := range n.walletCalls {
		if c.wallet == wallet {
			out = append(out, c.method)
		}
	}
	return out
}

// fakeSPK is the scriptPubKey the fake node reads for an address: the real
// witness program for a bech32/bech32m address, a deterministic P2WPKH for
// the plain test strings the suite uses as addresses.
func fakeSPK(addr string) string {
	if spk, ok := decodeSegwitAddr(addr); ok {
		return spk
	}
	h := sha256.Sum256([]byte(addr))
	return "0014" + hex.EncodeToString(h[:20])
}

// --- bech32 / bech32m ---------------------------------------------------------

const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

func bech32Polymod(v []int) uint32 {
	gen := []uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	chk := uint32(1)
	for _, x := range v {
		b := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(x)
		for i := 0; i < 5; i++ {
			if (b>>uint(i))&1 == 1 {
				chk ^= gen[i]
			}
		}
	}
	return chk
}

func decodeSegwitAddr(addr string) (string, bool) {
	a := strings.ToLower(addr)
	pos := strings.LastIndexByte(a, '1')
	if pos < 1 || pos+7 > len(a) {
		return "", false
	}
	hrp, data := a[:pos], a[pos+1:]
	var vals []int
	for _, ch := range data {
		i := strings.IndexRune(bech32Charset, ch)
		if i < 0 {
			return "", false
		}
		vals = append(vals, i)
	}
	var exp []int
	for _, ch := range hrp {
		exp = append(exp, int(ch)>>5)
	}
	exp = append(exp, 0)
	for _, ch := range hrp {
		exp = append(exp, int(ch)&31)
	}
	pm := bech32Polymod(append(exp, vals...))
	if pm != 1 && pm != 0x2bc830a3 {
		return "", false
	}
	vals = vals[:len(vals)-6]
	if len(vals) < 1 {
		return "", false
	}
	ver := vals[0]
	if (ver == 0) != (pm == 1) {
		return "", false
	}
	var prog []byte
	acc, bits := 0, 0
	for _, v := range vals[1:] {
		acc = acc<<5 | v
		bits += 5
		for bits >= 8 {
			bits -= 8
			prog = append(prog, byte(acc>>uint(bits)))
		}
	}
	if len(prog) < 2 || len(prog) > 40 {
		return "", false
	}
	op := byte(0)
	if ver > 0 {
		op = byte(0x50 + ver)
	}
	return hex.EncodeToString(append([]byte{op, byte(len(prog))}, prog...)), true
}

// --- dispatch ---------------------------------------------------------------------

// chainDispatch answers the chain methods; it runs under n.mu.
func (n *fakeNode) chainDispatch(method string, params []json.RawMessage) (any, int, string, bool) {
	c := n.chain()
	switch method {
	case "getblockchaininfo":
		return map[string]any{"blocks": n.height, "mediantime": c.mtp}, 0, "", true
	case "getblockhash":
		return fakeGenesisDisplay, 0, "", true
	case "getfeeexchangerates":
		return map[string]any{strings.Repeat("c8", 32): 100000000, m5USDX: 100000000}, 0, "", true
	case "estimatesmartfee":
		return map[string]any{"accepted": true, "rate": 100000000, "asset_atoms_per_kvb": 1000, "blocks": 6}, 0, "", true
	case "getmempoolinfo":
		return map[string]any{"mempoolminfee": 0.000001, "minrelaytxfee": 0.000001}, 0, "", true
	case "scantxoutset":
		var descs []string
		if len(params) > 1 {
			json.Unmarshal(params[1], &descs)
		}
		want := map[string]bool{}
		for _, d := range descs {
			if strings.HasPrefix(d, "raw(") && strings.HasSuffix(d, ")") {
				want[strings.ToLower(d[4:len(d)-1])] = true
			}
		}
		out := []map[string]any{}
		for _, u := range c.utxos {
			if u.spent || u.height == 0 || !want[u.spk] {
				continue
			}
			out = append(out, map[string]any{"txid": u.txid, "vout": u.vout, "scriptPubKey": u.spk,
				"amount": amount8(u.atoms), "asset": u.asset, "height": u.height})
		}
		return map[string]any{"success": true, "height": n.height, "unspents": out}, 0, "", true
	case "gettxout":
		var txid string
		var vout uint32
		json.Unmarshal(params[0], &txid)
		json.Unmarshal(params[1], &vout)
		u := c.utxos[fmt.Sprintf("%s:%d", txid, vout)]
		if u == nil || u.spent {
			return nil, 0, "", true
		}
		conf := int64(0)
		if u.height > 0 {
			conf = n.height - u.height + 1
		}
		return map[string]any{"confirmations": conf, "value": amount8(u.atoms), "asset": u.asset,
			"scriptPubKey": map[string]any{"hex": u.spk}}, 0, "", true
	case "testmempoolaccept":
		var list []string
		json.Unmarshal(params[0], &list)
		txid, reason := n.checkTx(list[0])
		if reason != "" {
			return []map[string]any{{"txid": txid, "allowed": false, "reject-reason": reason}}, 0, "", true
		}
		return []map[string]any{{"txid": txid, "allowed": true}}, 0, "", true
	case "sendrawtransaction":
		var h string
		json.Unmarshal(params[0], &h)
		c.sendCalls++
		txid, reason := n.checkTx(h)
		if reason == "txn-already-in-mempool" {
			return txid, 0, "", true
		}
		if reason != "" {
			return nil, -26, reason, true
		}
		tx, _ := decodeHexTx(h)
		for _, in := range tx.In {
			c.utxos[outpointKey(in.Prevout)].spent = true
		}
		for i, o := range tx.Out {
			if len(o.ScriptPubKey) == 0 {
				continue
			}
			v, _ := elements.ExplicitValueAmount(o.Value)
			c.utxos[fmt.Sprintf("%s:%d", txid, i)] = &fakeUTXO{txid: txid, vout: uint32(i),
				spk: hex.EncodeToString(o.ScriptPubKey), asset: displayAsset(o.Asset), atoms: v}
		}
		c.txs[txid] = h
		c.broadcasts = append(c.broadcasts, txid)
		return txid, 0, "", true
	}
	return nil, 0, "", false
}

func decodeHexTx(h string) (*elements.Tx, error) {
	raw, err := hex.DecodeString(h)
	if err != nil {
		return nil, err
	}
	return elements.DeserializeTx(raw)
}

func outpointKey(op elements.OutPoint) string {
	var d [32]byte
	for i := 0; i < 32; i++ {
		d[i] = op.Hash[31-i]
	}
	return fmt.Sprintf("%s:%d", hex.EncodeToString(d[:]), op.N)
}

func displayAsset(a []byte) string {
	if len(a) != 33 {
		return ""
	}
	d := make([]byte, 32)
	for i := 0; i < 32; i++ {
		d[i] = a[32-i]
	}
	return hex.EncodeToString(d)
}

// scriptPushes lists the data pushes of a script (opcodes 1..75), in order.
func scriptPushes(s []byte) [][]byte {
	var out [][]byte
	for i := 0; i < len(s); {
		op := s[i]
		i++
		if op >= 1 && op <= 75 {
			if i+int(op) > len(s) {
				break
			}
			out = append(out, s[i:i+int(op)])
			i += int(op)
		}
	}
	return out
}

func scriptNumLE(b []byte) uint64 {
	var v uint64
	for i := len(b) - 1; i >= 0; i-- {
		v = v<<8 | uint64(b[i])
	}
	return v
}

// checkTx is the fake mempool's verdict on a transaction: its txid and a
// reject reason, empty when it would be accepted.
func (n *fakeNode) checkTx(h string) (string, string) {
	c := n.chain()
	tx, err := decodeHexTx(h)
	if err != nil {
		return "", "TX decode failed"
	}
	txid := tx.TxID()
	if _, ok := c.txs[txid]; ok {
		return txid, "txn-already-in-mempool"
	}
	if c.rejectAll != "" {
		return txid, c.rejectAll
	}
	if len(tx.In) == 0 {
		return txid, "bad-txns-vin-empty"
	}
	nonFinalSeq := false
	spent := make([]*elements.SpentOutput, len(tx.In))
	prev := make([]*fakeUTXO, len(tx.In))
	for i, in := range tx.In {
		u := c.utxos[outpointKey(in.Prevout)]
		if u == nil || u.spent {
			return txid, "missing-inputs"
		}
		prev[i] = u
		a, _ := hex.DecodeString(u.asset)
		var a32 [32]byte
		copy(a32[:], a)
		spk, _ := hex.DecodeString(u.spk)
		spent[i] = &elements.SpentOutput{Asset: elements.ExplicitAsset(a32), Value: elements.ExplicitValue(u.atoms), ScriptPubKey: spk}
		if in.Sequence != 0xffffffff {
			nonFinalSeq = true
		}
	}
	if nonFinalSeq && tx.LockTime != 0 && tx.LockTime < locktimeThreshold && int64(tx.LockTime) >= n.height+1 {
		return txid, "non-final"
	}
	tx.NormalizeWitness()
	var genesis [32]byte
	gd, _ := hex.DecodeString(fakeGenesisDisplay)
	for i := 0; i < 32; i++ {
		genesis[i] = gd[31-i]
	}
	for i := range tx.In {
		spk, _ := hex.DecodeString(prev[i].spk)
		if len(spk) != 34 || spk[0] != 0x51 {
			continue // an ordinary coin of the fake wallet: assumed signed
		}
		w := tx.InWit[i].ScriptWitness
		if len(w) < 2 {
			return txid, fmt.Sprintf("input %d: witness program mismatch", i)
		}
		control, script := w[len(w)-1], w[len(w)-2]
		if len(control) < 33 || (len(control)-33)%32 != 0 || control[0]&0xfe != 0xc4 {
			return txid, fmt.Sprintf("input %d: bad control block", i)
		}
		node := elements.TapLeafHash(script)
		for p := 33; p < len(control); p += 32 {
			var sib [32]byte
			copy(sib[:], control[p:p+32])
			node = elements.TapBranchHash(node, sib)
		}
		var internal [32]byte
		copy(internal[:], control[1:33])
		ok, parity, err := elements.TweakPubKey(internal, node[:])
		if err != nil || !bytes.Equal(ok[:], spk[2:]) || parity != (control[0]&1 == 1) {
			return txid, fmt.Sprintf("input %d: Witness program hash mismatch", i)
		}
		var p32, p8 [][]byte
		for _, p := range scriptPushes(script) {
			switch len(p) {
			case 32:
				p32 = append(p32, p)
			case 8:
				p8 = append(p8, p)
			}
		}
		keyed := len(w) == 3
		// CHECKLOCKTIMEVERIFY: a leaf opening with a lock push followed by b1.
		if len(script) > 2 && script[0] >= 1 && script[0] <= 5 && len(script) > int(script[0])+1 && script[int(script[0])+1] == 0xb1 {
			lock := scriptNumLE(script[1 : 1+int(script[0])])
			if uint64(tx.LockTime) < lock || tx.In[i].Sequence == 0xffffffff {
				return txid, fmt.Sprintf("input %d: Locktime requirement not satisfied", i)
			}
		}
		if keyed {
			key := p32[len(p32)-1]
			sh, err := elements.TaprootSighash(tx, spent, elements.SighashDefault, genesis, i, script)
			if err != nil {
				return txid, err.Error()
			}
			pk, err1 := schnorr.ParsePubKey(key)
			sig, err2 := schnorr.ParseSignature(w[0])
			if err1 != nil || err2 != nil || !sig.Verify(sh[:], pk) {
				return txid, fmt.Sprintf("input %d: Invalid Schnorr signature", i)
			}
		}
		// The pinned output. 32-byte pushes: release [PAY_ASSET, ISSUER_SCRIPT, KEY],
		// cancel [REFUND_SCRIPT, KEY], refund [REFUND_SCRIPT]; the one 8-byte push is
		// MINIMUM for the release and FEE_CAP otherwise.
		if len(p8) != 1 {
			return txid, fmt.Sprintf("input %d: not an escrow leaf", i)
		}
		if keyed && len(p32) == 3 { // release
			o := tx.Out[0]
			v, explicit := elements.ExplicitValueAmount(o.Value)
			h := sha256.Sum256(o.ScriptPubKey)
			if !explicit || o.Asset[0] != 1 || !bytes.Equal(o.Asset[1:], p32[0]) || !bytes.Equal(h[:], p32[1]) ||
				len(o.Nonce) != 1 || o.Nonce[0] != 0 {
				return txid, fmt.Sprintf("input %d: Script failed an OP_EQUALVERIFY operation", i)
			}
			if v < binary.BigEndian.Uint64(p8[0]) {
				return txid, fmt.Sprintf("input %d: Script failed an OP_VERIFY operation", i)
			}
			continue
		}
		if i >= len(tx.Out) {
			return txid, fmt.Sprintf("input %d: no pinned output", i)
		}
		o := tx.Out[i]
		v, explicit := elements.ExplicitValueAmount(o.Value)
		h := sha256.Sum256(o.ScriptPubKey)
		if !explicit || displayAsset(o.Asset) != prev[i].asset || !bytes.Equal(h[:], p32[0]) ||
			len(o.Nonce) != 1 || o.Nonce[0] != 0 {
			return txid, fmt.Sprintf("input %d: Script failed an OP_EQUALVERIFY operation", i)
		}
		if v+binary.BigEndian.Uint64(p8[0]) < prev[i].atoms {
			return txid, fmt.Sprintf("input %d: Script failed an OP_VERIFY operation", i)
		}
	}
	return txid, ""
}
