package main

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"

	sa "github.com/ConcatenaLabs/sequentia-contracts/mirrors/go"

	"github.com/ConcatenaLabs/SeqPal/seqpald/elements"
)

// The subscription escrow covenant.
//
// A subscription on the Sequentia rail is paid to an address of its own,
// derived from the sequentia/subscription-escrow template of
// ConcatenaLabs/sequentia-contracts (docs/subscription-escrow.md there):
//
//	P2TR(NUMS, {{release, cancel}, {refund, H_TapData(SUBSCRIPTION)}})
//
// The internal key is NUMS, so nobody holds a key to the output itself, and
// only three things can happen to the coin:
//
//	release  the issuer's key, once CLOSE has passed, into a transaction whose
//	         output 0 pays the issuer's payout script at least MINIMUM of the
//	         payment asset. Every subscription of the offering names the same
//	         output 0, so one release spends them all.
//	cancel   the issuer's key, at any time, with output k returning the coin at
//	         input k to the subscriber's refund script, less at most FEE_CAP.
//	refund   anyone, no signature, once DEADLINE has passed, the same output.
//
// This platform holds no key to any of it. It derives the address, watches it
// from the chain without a wallet, builds the spends, and hands the issuer the
// TRANSACTION to sign the release or the cancel in its own wallet; the refund
// it builds and broadcasts itself, because nobody signs it.
//
// The descriptor file is the template's own, vendored at covenants/ and pinned
// by its template hash; the address arithmetic is the template's Go mirror. A
// descriptor whose hash differs from the pin is refused, so a changed template
// can never silently move where subscribers' money is paid.

//go:embed covenants/subscription_escrow.json
var escrowDescriptorJSON []byte

const (
	escrowTemplateName = "sequentia/subscription-escrow"
	escrowTemplateHash = "e602bafffda0dd1f2c19b3725fb4a84aa3106ceee88ada4ff6865b249ca254b4"
	// CHECKLOCKTIMEVERIFY reads a lock below this as a block height, at or
	// above it as a unix time.
	locktimeThreshold = 500000000
	// The sequence a covenant input carries: CHECKLOCKTIMEVERIFY needs a
	// non-final sequence, or the lock time is ignored.
	escrowSequence = 0xfffffffe
)

var escrowDesc struct {
	once sync.Once
	d    sa.Descriptor
	err  error
}

func escrowDescriptor() (sa.Descriptor, error) {
	escrowDesc.once.Do(func() {
		d, err := sa.ParseDescriptor(escrowDescriptorJSON)
		if err != nil {
			escrowDesc.err = fmt.Errorf("the escrow descriptor does not parse: %w", err)
			return
		}
		if d.TemplateHash != escrowTemplateHash {
			escrowDesc.err = fmt.Errorf("the escrow descriptor's template hash is %s, not the pinned %s",
				d.TemplateHash, escrowTemplateHash)
			return
		}
		escrowDesc.d = d
	})
	return escrowDesc.d, escrowDesc.err
}

// escrowParams are one subscription's parameters, in the forms this platform
// keeps them. Assets are display-order hex, scripts are the scriptPubKeys
// themselves (the covenant commits to their SHA256).
type escrowParams struct {
	PayAsset     string
	IssuerKey    string
	IssuerSPK    string
	Minimum      uint64
	Close        uint32
	RefundSPK    string
	Deadline     uint32
	FeeCap       uint64
	Subscription string // 64 hex
}

type escrowLeaf struct {
	Script  []byte
	Control []byte
}

// escrowInstance is a derived subscription escrow output.
type escrowInstance struct {
	SPK     []byte
	Address string
	Leaves  map[string]escrowLeaf
	Params  map[string]string // the template parameters, as the descriptor takes them
}

// escrowSubscriptionRef is the SUBSCRIPTION parameter: it gives each
// subscription its own address, so the chain itself tells them apart.
func escrowSubscriptionRef(issuanceID, subID string) string {
	h := sha256.Sum256([]byte("seqpal/subscription-escrow/v1\x00" + issuanceID + "\x00" + subID))
	return hex.EncodeToString(h[:])
}

func reverseHex(display string) (string, error) {
	b, err := hex.DecodeString(display)
	if err != nil || len(b) != 32 {
		return "", fmt.Errorf("%q is not a 32-byte hex id", display)
	}
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return hex.EncodeToString(b), nil
}

func sha256OfHex(h string) (string, error) {
	b, err := hex.DecodeString(h)
	if err != nil || len(b) == 0 {
		return "", fmt.Errorf("%q is not a script", h)
	}
	d := sha256.Sum256(b)
	return hex.EncodeToString(d[:]), nil
}

// escrowHRP is the bech32 prefix escrow addresses are written in on the chain
// this deployment runs: the descriptor's own chain table first, then
// SEQPALD_BECH32_HRP for a chain it does not list.
func escrowHRP(network string) string {
	if v := strings.TrimSpace(os.Getenv("SEQPALD_BECH32_HRP")); v != "" {
		return v
	}
	d, err := escrowDescriptor()
	if err != nil {
		return ""
	}
	for _, c := range d.Chains {
		if c.Name == network {
			return c.Bech32HRP
		}
	}
	return ""
}

// deriveEscrow computes one subscription's escrow output from its parameters.
func deriveEscrow(p escrowParams, network string) (*escrowInstance, error) {
	d, err := escrowDescriptor()
	if err != nil {
		return nil, err
	}
	asset, err := reverseHex(p.PayAsset)
	if err != nil {
		return nil, fmt.Errorf("payment asset: %w", err)
	}
	if !validXOnly(p.IssuerKey) {
		return nil, fmt.Errorf("the release key is not a 32-byte x-only key")
	}
	issuerScript, err := sha256OfHex(p.IssuerSPK)
	if err != nil {
		return nil, fmt.Errorf("issuer payout script: %w", err)
	}
	refundScript, err := sha256OfHex(p.RefundSPK)
	if err != nil {
		return nil, fmt.Errorf("refund script: %w", err)
	}
	if len(p.Subscription) != 64 || !isHexStr(p.Subscription) {
		return nil, fmt.Errorf("the subscription reference is not 32 bytes of hex")
	}
	params := map[string]string{
		"PAY_ASSET":     asset,
		"ISSUER_KEY":    strings.ToLower(p.IssuerKey),
		"ISSUER_SCRIPT": issuerScript,
		"MINIMUM":       fmt.Sprintf("%016x", p.Minimum),
		"CLOSE":         fmt.Sprintf("%08x", p.Close),
		"REFUND_SCRIPT": refundScript,
		"DEADLINE":      fmt.Sprintf("%08x", p.Deadline),
		"FEE_CAP":       fmt.Sprintf("%016x", p.FeeCap),
		"SUBSCRIPTION":  strings.ToLower(p.Subscription),
	}
	x, err := sa.DeriveTree(d, params, map[string]string{})
	if err != nil {
		return nil, err
	}
	spk, err := hex.DecodeString(x.ScriptPubKey)
	if err != nil {
		return nil, err
	}
	out := &escrowInstance{SPK: spk, Leaves: map[string]escrowLeaf{}, Params: params}
	for _, name := range []string{"release", "cancel", "refund"} {
		l, ok := x.Leaves[name]
		if !ok {
			return nil, fmt.Errorf("the escrow template has no %s leaf", name)
		}
		script, err := hex.DecodeString(l.Script)
		if err != nil {
			return nil, err
		}
		control, err := hex.DecodeString(l.ControlBlock)
		if err != nil {
			return nil, err
		}
		out.Leaves[name] = escrowLeaf{Script: script, Control: control}
	}
	if a := x.Address[network]; a != "" {
		out.Address = a
	} else if hrp := escrowHRP(network); hrp != "" {
		ok, _ := hex.DecodeString(x.OutputKey)
		out.Address = sa.SegwitV1Address(hrp, ok)
	} else {
		return nil, fmt.Errorf("no bech32 prefix is known for network %q: set SEQPALD_BECH32_HRP", network)
	}
	return out, nil
}

// --- building the spends --------------------------------------------------------

// escrowCoin is one coin at a subscription's escrow address.
type escrowCoin struct {
	Txid           string `json:"txid"`
	Vout           uint32 `json:"vout"`
	Atoms          uint64 `json:"atoms"`
	Asset          string `json:"asset"`
	Height         int64  `json:"height"`
	SubscriptionID string `json:"subscription_id"`
}

// escrowInput is a coin as an input of a built spend, with everything a
// wallet needs to check it and sign it: the leaf it is spent through, that
// leaf's control block (which the wallet checks commits to the coin's
// scriptPubKey), the key that must sign it, and the sighash.
type escrowInput struct {
	Vin            int    `json:"vin"`
	Txid           string `json:"txid"`
	Vout           uint32 `json:"vout"`
	SubscriptionID string `json:"subscription_id"`
	Asset          string `json:"asset"`
	Atoms          uint64 `json:"atoms"`
	SPK            string `json:"script_pubkey"`
	Leaf           string `json:"leaf_name"`
	Script         string `json:"leaf"`
	Control        string `json:"control"`
	XOnlyKey       string `json:"xonlykey,omitempty"`
	Sighash        string `json:"sighash,omitempty"`
}

// escrowOutput describes one output of a built spend in plain terms.
type escrowOutput struct {
	N       int    `json:"n"`
	Pays    string `json:"pays"` // issuer | platform | subscriber | network_fee
	Address string `json:"address,omitempty"`
	SPK     string `json:"script_pubkey,omitempty"`
	Asset   string `json:"asset"`
	Atoms   uint64 `json:"atoms"`
	SubID   string `json:"subscription_id,omitempty"`
}

func outpointOf(txid string, vout uint32) (elements.OutPoint, error) {
	b, err := hex.DecodeString(txid)
	if err != nil || len(b) != 32 {
		return elements.OutPoint{}, fmt.Errorf("%q is not a txid", txid)
	}
	var op elements.OutPoint
	for i := 0; i < 32; i++ {
		op.Hash[i] = b[31-i]
	}
	op.N = vout
	return op, nil
}

func explicitAsset(display string) ([]byte, error) {
	b, err := hex.DecodeString(display)
	if err != nil || len(b) != 32 {
		return nil, fmt.Errorf("%q is not an asset id", display)
	}
	var a [32]byte
	copy(a[:], b)
	return elements.ExplicitAsset(a), nil
}

// newTxOut is an explicit output with no nonce: what each leaf of the
// covenant requires of the output it pins.
func newTxOut(asset string, atoms uint64, spk []byte) (*elements.TxOut, error) {
	a, err := explicitAsset(asset)
	if err != nil {
		return nil, err
	}
	return &elements.TxOut{Asset: a, Value: elements.ExplicitValue(atoms), Nonce: elements.NullNonce(), ScriptPubKey: spk}, nil
}

// spentOutputs are the prevouts a taproot sighash commits to, in input order.
func spentOutputs(ins []escrowInput) ([]*elements.SpentOutput, error) {
	out := make([]*elements.SpentOutput, len(ins))
	for i, in := range ins {
		a, err := explicitAsset(in.Asset)
		if err != nil {
			return nil, err
		}
		spk, err := hex.DecodeString(in.SPK)
		if err != nil {
			return nil, err
		}
		out[i] = &elements.SpentOutput{Asset: a, Value: elements.ExplicitValue(in.Atoms), ScriptPubKey: spk}
	}
	return out, nil
}

// vsizeWithWitness is the virtual size the transaction will have once each
// input carries its witness: a 64-byte signature for a keyed leaf, then the
// leaf script and its control block. Explicit values are fixed-length, so the
// size does not change when the fee is filled in.
func vsizeWithWitness(tx *elements.Tx, ins []escrowInput) int64 {
	c := cloneTx(tx)
	c.InWit, c.OutWit = nil, nil
	base := int64(len(c.Serialize()))
	c.NormalizeWitness()
	for i, in := range ins {
		script, _ := hex.DecodeString(in.Script)
		control, _ := hex.DecodeString(in.Control)
		stack := [][]byte{script, control}
		if in.Leaf != "refund" {
			stack = append([][]byte{make([]byte, 64)}, stack...)
		}
		c.InWit[i].ScriptWitness = stack
	}
	total := int64(len(c.Serialize()))
	weight := base*3 + total
	return (weight + 3) / 4
}

func cloneTx(tx *elements.Tx) *elements.Tx {
	raw := tx.Serialize()
	c, err := elements.DeserializeTx(raw)
	if err != nil {
		panic("cloneTx: " + err.Error())
	}
	return c
}

// feeForVsize is the network fee in the fee asset's own atoms at a rate in
// atoms per 1000 vbytes, rounded up.
func feeForVsize(vsize int64, atomsPerKvB uint64) uint64 {
	f := (uint64(vsize)*atomsPerKvB + 999) / 1000
	if f == 0 {
		f = 1
	}
	return f
}

// escrowSighashes fills each keyed input's sighash: Elements taproot,
// script path, SIGHASH_DEFAULT, under the chain's genesis hash. It commits to
// every input, every output, the lock time and the leaf, so a signature made
// over it authorises this one transaction through this one leaf and nothing
// else: not another coin, not the other keyed leaf, not another chain.
func escrowSighashes(tx *elements.Tx, ins []escrowInput, genesis [32]byte) error {
	spent, err := spentOutputs(ins)
	if err != nil {
		return err
	}
	for i := range ins {
		if ins[i].Leaf == "refund" {
			continue
		}
		script, err := hex.DecodeString(ins[i].Script)
		if err != nil {
			return err
		}
		h, err := elements.TaprootSighash(cloneTx(tx), spent, elements.SighashDefault, genesis, i, script)
		if err != nil {
			return err
		}
		ins[i].Sighash = hex.EncodeToString(h[:])
	}
	return nil
}

// assembleEscrowWitness puts each input's witness in place: [sig, script,
// control] for a keyed leaf, [script, control] for the refund. Every
// signature is checked against the release key over the sighash this
// platform computed itself before it is placed, so nothing that does not
// verify is ever broadcast.
func assembleEscrowWitness(unsigned string, ins []escrowInput, sigs map[int]string, genesis [32]byte) (*elements.Tx, error) {
	raw, err := hex.DecodeString(unsigned)
	if err != nil {
		return nil, err
	}
	tx, err := elements.DeserializeTx(raw)
	if err != nil {
		return nil, err
	}
	if len(tx.In) != len(ins) {
		return nil, fmt.Errorf("the transaction has %d inputs and the spend records %d", len(tx.In), len(ins))
	}
	// Recompute rather than trust the stored sighashes.
	check := make([]escrowInput, len(ins))
	copy(check, ins)
	if err := escrowSighashes(tx, check, genesis); err != nil {
		return nil, err
	}
	tx.NormalizeWitness()
	for i, in := range check {
		script, _ := hex.DecodeString(in.Script)
		control, _ := hex.DecodeString(in.Control)
		if in.Leaf == "refund" {
			tx.InWit[i].ScriptWitness = [][]byte{script, control}
			continue
		}
		sigHex, ok := sigs[i]
		if !ok {
			return nil, fmt.Errorf("input %d has no signature", i)
		}
		if err := verifyBIP340(in.XOnlyKey, in.Sighash, sigHex); err != nil {
			return nil, fmt.Errorf("input %d: %v", i, err)
		}
		sig, _ := hex.DecodeString(sigHex)
		tx.InWit[i].ScriptWitness = [][]byte{sig, script, control}
	}
	return tx, nil
}

// verifyBIP340 checks a 64-byte signature over a 32-byte digest under an
// x-only key. A 65-byte signature (an explicit sighash byte) is refused: the
// sighash computed here is SIGHASH_DEFAULT and nothing else is accepted.
func verifyBIP340(xonly, digestHex, sigHex string) error {
	pk, err := hex.DecodeString(xonly)
	if err != nil || len(pk) != 32 {
		return fmt.Errorf("bad key")
	}
	digest, err := hex.DecodeString(digestHex)
	if err != nil || len(digest) != 32 {
		return fmt.Errorf("bad sighash")
	}
	sigb, err := hex.DecodeString(strings.TrimSpace(sigHex))
	if err != nil || len(sigb) != 64 {
		return fmt.Errorf("the signature is not 64 bytes of hex (SIGHASH_DEFAULT only)")
	}
	key, err := schnorr.ParsePubKey(pk)
	if err != nil {
		return fmt.Errorf("bad key: %v", err)
	}
	sig, err := schnorr.ParseSignature(sigb)
	if err != nil {
		return fmt.Errorf("bad signature: %v", err)
	}
	if !sig.Verify(digest, key) {
		return fmt.Errorf("the signature does not verify for the release key over this transaction")
	}
	return nil
}

// releaseBuild is the arithmetic of a release, kept beside the transaction so
// the issuer sees exactly where the money goes before signing.
type releaseBuild struct {
	Tx          *elements.Tx
	Inputs      []escrowInput
	Outputs     []escrowOutput
	Total       uint64
	EscrowFee   uint64
	NetworkFee  uint64
	ToIssuer    uint64
	FeePerKvB   uint64
	VSize       int64
	LockTime    uint32
	Minimum     uint64
	PayAsset    string
	IssuerAddr  string
	PlatformAdr string
}

// buildRelease builds the release of every coin given: output 0 the raise to
// the issuer, output 1 the escrow fee to the platform when there is one, and
// the network fee in the payment asset itself, out of the coins. The fee is
// never paid in any other asset, and in particular never in the policy asset.
func buildRelease(t *EscrowTerms, coins []escrowCoin, inst map[string]*escrowInstance,
	escrowFee uint64, platformSPK []byte, platformAddr string, atomsPerKvB uint64) (*releaseBuild, error) {
	return buildReleaseReturning(t, coins, inst, nil, escrowFee, platformSPK, platformAddr, atomsPerKvB)
}

// escrowExcess is what a subscriber paid beyond their subscription: the
// release returns it to them in an output of its own, because the release
// leaf pins output 0 alone and would otherwise let it go to the issuer.
type escrowExcess struct {
	SubscriptionID string
	RefundSPK      string
	Atoms          uint64
}

// buildReleaseReturning is buildRelease with each subscriber's excess paid
// back to them, after the platform's fee output.
func buildReleaseReturning(t *EscrowTerms, coins []escrowCoin, inst map[string]*escrowInstance, excess []escrowExcess,
	escrowFee uint64, platformSPK []byte, platformAddr string, atomsPerKvB uint64) (*releaseBuild, error) {
	if len(coins) == 0 {
		return nil, fmt.Errorf("there is nothing in escrow to release")
	}
	issuerSPK, err := hex.DecodeString(t.IssuerSPK)
	if err != nil {
		return nil, err
	}
	tx := &elements.Tx{Version: 2, LockTime: t.CloseLock}
	b := &releaseBuild{LockTime: t.CloseLock, Minimum: t.MinimumAtoms, PayAsset: t.PayAsset,
		IssuerAddr: t.IssuerAddress, PlatformAdr: platformAddr, FeePerKvB: atomsPerKvB}
	for i, c := range coins {
		if !strings.EqualFold(c.Asset, t.PayAsset) {
			return nil, fmt.Errorf("coin %s:%d is not in the payment asset", c.Txid, c.Vout)
		}
		e := inst[c.SubscriptionID]
		if e == nil {
			return nil, fmt.Errorf("no escrow instance for subscription %s", c.SubscriptionID)
		}
		op, err := outpointOf(c.Txid, c.Vout)
		if err != nil {
			return nil, err
		}
		tx.In = append(tx.In, &elements.TxIn{Prevout: op, Sequence: escrowSequence})
		l := e.Leaves["release"]
		b.Inputs = append(b.Inputs, escrowInput{Vin: i, Txid: c.Txid, Vout: c.Vout, SubscriptionID: c.SubscriptionID,
			Asset: c.Asset, Atoms: c.Atoms, SPK: hex.EncodeToString(e.SPK), Leaf: "release",
			Script: hex.EncodeToString(l.Script), Control: hex.EncodeToString(l.Control), XOnlyKey: t.ReleaseKey})
		b.Total += c.Atoms
	}
	// Placeholders first, to measure; explicit values are fixed-length.
	out0, err := newTxOut(t.PayAsset, 0, issuerSPK)
	if err != nil {
		return nil, err
	}
	tx.Out = append(tx.Out, out0)
	if escrowFee > 0 {
		if len(platformSPK) == 0 {
			return nil, fmt.Errorf("an escrow fee is due and no platform address is configured to receive it")
		}
		fo, _ := newTxOut(t.PayAsset, escrowFee, platformSPK)
		tx.Out = append(tx.Out, fo)
	}
	var returned uint64
	type excessOut struct {
		n int
		e escrowExcess
	}
	var excessOuts []excessOut
	for _, e := range excess {
		if e.Atoms == 0 {
			continue
		}
		spk, err := hex.DecodeString(e.RefundSPK)
		if err != nil || len(spk) == 0 {
			return nil, fmt.Errorf("no refund script for subscription %s", e.SubscriptionID)
		}
		o, err := newTxOut(t.PayAsset, e.Atoms, spk)
		if err != nil {
			return nil, err
		}
		excessOuts = append(excessOuts, excessOut{len(tx.Out), e})
		tx.Out = append(tx.Out, o)
		returned += e.Atoms
	}
	feeOut, _ := newTxOut(t.PayAsset, 0, nil)
	tx.Out = append(tx.Out, feeOut)
	b.VSize = vsizeWithWitness(tx, b.Inputs)
	b.NetworkFee = feeForVsize(b.VSize, atomsPerKvB)
	b.EscrowFee = escrowFee
	if b.Total < returned+escrowFee+b.NetworkFee {
		return nil, fmt.Errorf("the coins in escrow (%d) do not cover the escrow fee (%d) and the network fee (%d)",
			b.Total-returned, escrowFee, b.NetworkFee)
	}
	b.ToIssuer = b.Total - returned - escrowFee - b.NetworkFee
	if b.ToIssuer < t.MinimumAtoms {
		return nil, fmt.Errorf("the release would pay the issuer %d, below the offering's minimum of %d "+
			"(the coins in escrow total %d, less an escrow fee of %d and a network fee of %d)",
			b.ToIssuer, t.MinimumAtoms, b.Total, escrowFee, b.NetworkFee)
	}
	tx.Out[0].Value = elements.ExplicitValue(b.ToIssuer)
	tx.Out[len(tx.Out)-1].Value = elements.ExplicitValue(b.NetworkFee)
	b.Outputs = append(b.Outputs, escrowOutput{N: 0, Pays: "issuer", Address: t.IssuerAddress, SPK: t.IssuerSPK, Asset: t.PayAsset, Atoms: b.ToIssuer})
	if escrowFee > 0 {
		b.Outputs = append(b.Outputs, escrowOutput{N: 1, Pays: "platform", Address: platformAddr, SPK: hex.EncodeToString(platformSPK), Asset: t.PayAsset, Atoms: escrowFee})
	}
	for _, x := range excessOuts {
		b.Outputs = append(b.Outputs, escrowOutput{N: x.n, Pays: "subscriber", SPK: x.e.RefundSPK, Asset: t.PayAsset,
			Atoms: x.e.Atoms, SubID: x.e.SubscriptionID})
	}
	b.Outputs = append(b.Outputs, escrowOutput{N: len(tx.Out) - 1, Pays: "network_fee", Asset: t.PayAsset, Atoms: b.NetworkFee})
	b.Tx = tx
	return b, nil
}

// refundBuild is a cancel or a refund: coin k at input k back to its
// subscriber at output k, the network fee out of the coins, each keeping back
// at most FEE_CAP.
type refundBuild struct {
	Tx         *elements.Tx
	Inputs     []escrowInput
	Outputs    []escrowOutput
	NetworkFee uint64
	VSize      int64
	LockTime   uint32
}

func buildReturn(kind string, t *EscrowTerms, coins []escrowCoin, inst map[string]*escrowInstance,
	refundSPK map[string]string, atomsPerKvB uint64) (*refundBuild, error) {
	if kind != "cancel" && kind != "refund" {
		return nil, fmt.Errorf("unknown return kind %q", kind)
	}
	if len(coins) == 0 {
		return nil, fmt.Errorf("there is nothing in escrow to return")
	}
	tx := &elements.Tx{Version: 2}
	seq := uint32(0xffffffff)
	if kind == "refund" {
		tx.LockTime = t.DeadlineLock
		seq = escrowSequence
	}
	b := &refundBuild{LockTime: tx.LockTime}
	for i, c := range coins {
		if !strings.EqualFold(c.Asset, t.PayAsset) {
			return nil, fmt.Errorf("coin %s:%d is not in the payment asset", c.Txid, c.Vout)
		}
		e := inst[c.SubscriptionID]
		if e == nil {
			return nil, fmt.Errorf("no escrow instance for subscription %s", c.SubscriptionID)
		}
		op, err := outpointOf(c.Txid, c.Vout)
		if err != nil {
			return nil, err
		}
		tx.In = append(tx.In, &elements.TxIn{Prevout: op, Sequence: seq})
		l := e.Leaves[kind]
		in := escrowInput{Vin: i, Txid: c.Txid, Vout: c.Vout, SubscriptionID: c.SubscriptionID,
			Asset: c.Asset, Atoms: c.Atoms, SPK: hex.EncodeToString(e.SPK), Leaf: kind,
			Script: hex.EncodeToString(l.Script), Control: hex.EncodeToString(l.Control)}
		if kind == "cancel" {
			in.XOnlyKey = t.ReleaseKey
		}
		b.Inputs = append(b.Inputs, in)
		spk, err := hex.DecodeString(refundSPK[c.SubscriptionID])
		if err != nil || len(spk) == 0 {
			return nil, fmt.Errorf("no refund script for subscription %s", c.SubscriptionID)
		}
		o, err := newTxOut(c.Asset, c.Atoms, spk)
		if err != nil {
			return nil, err
		}
		tx.Out = append(tx.Out, o)
	}
	feeOut, _ := newTxOut(t.PayAsset, 0, nil)
	tx.Out = append(tx.Out, feeOut)
	b.VSize = vsizeWithWitness(tx, b.Inputs)
	b.NetworkFee = feeForVsize(b.VSize, atomsPerKvB)
	// Spread the fee over the coins, the remainder on the first ones, and hold
	// each share to the covenant's cap.
	n := uint64(len(coins))
	share, rem := b.NetworkFee/n, b.NetworkFee%n
	for i, c := range coins {
		keep := share
		if uint64(i) < rem {
			keep++
		}
		if keep > t.FeeCap {
			return nil, fmt.Errorf("the network fee (%d over %d coins) exceeds what each coin may keep back (%d)",
				b.NetworkFee, n, t.FeeCap)
		}
		if keep >= c.Atoms {
			return nil, fmt.Errorf("coin %s:%d (%d) does not cover its share of the network fee (%d)", c.Txid, c.Vout, c.Atoms, keep)
		}
		tx.Out[i].Value = elements.ExplicitValue(c.Atoms - keep)
		b.Outputs = append(b.Outputs, escrowOutput{N: i, Pays: "subscriber", SPK: refundSPK[c.SubscriptionID],
			Asset: c.Asset, Atoms: c.Atoms - keep, SubID: c.SubscriptionID})
	}
	tx.Out[len(tx.Out)-1].Value = elements.ExplicitValue(b.NetworkFee)
	b.Outputs = append(b.Outputs, escrowOutput{N: len(tx.Out) - 1, Pays: "network_fee", Asset: t.PayAsset, Atoms: b.NetworkFee})
	b.Tx = tx
	return b, nil
}

// lockReached reports whether a transaction locked at lock can be mined in
// the next block: a height lock once the tip has reached it (the next block is
// above it), a time lock once the median time past has passed it.
func lockReached(lock uint32, tip, mtp int64) bool {
	if lock < locktimeThreshold {
		return tip >= int64(lock)
	}
	return mtp > int64(lock)
}

func describeLock(lock uint32) string {
	if lock < locktimeThreshold {
		return fmt.Sprintf("Sequentia block %d", lock)
	}
	return fmt.Sprintf("unix time %d", lock)
}
