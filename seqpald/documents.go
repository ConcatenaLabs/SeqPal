package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"sort"
	"strings"
	"time"
)

// The document pipeline generates a deterministic, content-addressed set of
// legal artifacts from an issuance's terms and binds a manifest of them INSIDE
// the terms object, so terms_hash (committed on-chain via contract_hash) commits
// to the exact document bytes. Determinism is the whole game: a document's hash
// is sha256 of its canonical HTML bytes, so the generator embeds no timestamps,
// no random ids, and no map-iteration order. Regenerating from the same terms
// always yields the same hashes, which is what lets the deploy path rebind and
// verify without trusting anything the browser computed.
//
// PDF rendering (the house `soffice --headless --convert-to pdf` toolchain) is a
// display convenience layered on top: the content address is always over the
// canonical HTML, never the PDF (which embeds a production timestamp), so PDF
// availability never changes a hash. PDFs are rendered lazily on request and
// cached; where soffice is absent the canonical HTML is served and the PDF step
// is documented on the surface.

const (
	docSpec           = "openamp-document-v1"
	demoPlatformName  = "SeqPal LLC"
	demoPlatformNote  = "SeqPal LLC (a Próspera ZEDE entity)"
	issuanceRegime    = "Próspera (Roatán, Honduras) under the RFSA regime"
	simulatedDisclaim = "This is a testnet proof of concept. No real funds, no real securities, and no legal effect. " +
		"The identity verification, incorporation, regulator approval, fiat rails, and legal instruments referenced here " +
		"are labeled simulations; the on-chain asset, transfers, enforcement, register, and transparency log are real."
)

// docKinds is the canonical, ordered set of document kinds the generator emits.
// The manifest set is sorted by kind, so this order is only for readability.
var docKinds = []string{
	"offering-memorandum",
	"subscription-agreement",
	"operating-agreement",
	"escrow-terms",
	"kid-summary",
	"uk-investor-statement",
	"lift-artifact",
}

// bearerDocKinds is the document set for a bearer (freely tradable, supervised)
// issuance. The compiled-matrix artifacts that only describe transfer
// restrictions (the operating agreement's restricted register, the KID summary,
// the lift artifact) are omitted, because a bearer asset has no transfer
// restrictions; in their place the charter set is emitted: the articles of
// incorporation electing a ledger-securities framework and designating the
// on-chain share ledger, the freeze-power charter, the dual-state shareholder
// terms, and the risk-acceptance instrument.
var bearerDocKinds = []string{
	"offering-memorandum",
	"subscription-agreement",
	"escrow-terms",
	"uk-investor-statement",
	"articles-of-incorporation",
	"freeze-power-charter",
	"shareholder-terms",
	"risk-acceptance-instrument",
}

// GeneratedDoc is one produced artifact before it is stored.
type GeneratedDoc struct {
	Kind  string
	Title string
	HTML  []byte
	Hash  string
}

// docManifestEntry is one row of the manifest bound into terms.
type docManifestEntry struct {
	Hash  string `json:"hash"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
}

// docManifest is the object placed at terms.documents. manifest_hash is the
// sha256 of the canonical `set` array, so an auditor recomputes it from the
// terms document alone.
type docManifest struct {
	Spec         string             `json:"spec"`
	ManifestHash string             `json:"manifest_hash"`
	Set          []docManifestEntry `json:"set"`
}

// docContext is the resolved, deterministic input to the pure document builder.
type docContext struct {
	IssuerName     string
	OfferingName   string
	Ticker         string
	Structure      string // canonical
	Char           Characterization
	Jurisdictions  []jurisdictionRow
	EUCaps         []capRow
	Lockup         string
	RegS           string
	Price          string
	IssuerExternal bool // M9: this asset's issuer key is the entity's own key, held off the platform
	// Bearer marks the bearer enforcement election (a freely tradable,
	// consensus-supervised asset): the charter document set replaces the
	// matrix artifacts, and the shared documents drop their restricted-transfer
	// language. Resolved from terms.enforcement, falling back to the issuance
	// row's persisted election, so the browser's pre-deploy generation and the
	// deploy-time regeneration agree.
	Bearer bool
	// EntityJurisdiction fills the registered-office term in the articles of
	// incorporation when the issuance is bound to an entity record. Only bearer
	// documents read it, so non-bearer output is unaffected.
	EntityJurisdiction string
}

type jurisdictionRow struct {
	Code   string
	Access string
	Elig   []string
}

type capRow struct {
	Key   string
	Value int
}

// ensureDocuments generates the document set for an issuance's terms, stores it,
// and returns the terms with the manifest bound in. It is deterministic and
// idempotent: any client-supplied `documents` block is discarded and regenerated
// from the clean terms, so the returned terms always commit to documents that
// actually exist in the store. The deploy path calls this before it canonicalizes
// and hashes the terms, which is what makes terms_hash commit to the documents.
func (s *server) ensureDocuments(iss *Issuance, termsRaw json.RawMessage) (json.RawMessage, docManifest, error) {
	var m map[string]any
	if err := json.Unmarshal(rawOrEmpty(termsRaw), &m); err != nil {
		return nil, docManifest{}, fmt.Errorf("terms must be a JSON object")
	}
	if m == nil {
		m = map[string]any{}
	}
	delete(m, "documents") // regenerate deterministically; never trust a client binding

	ctx, err := s.docContextFor(iss, m)
	if err != nil {
		return nil, docManifest{}, err
	}
	docs := buildDocumentSet(ctx)

	now := time.Now().Unix()
	for _, d := range docs {
		if err := s.st.PutDocument(&StoredDoc{
			DocHash: d.Hash, IssuanceID: iss.ID, Kind: d.Kind, Title: d.Title,
			ContentType: "text/html; charset=utf-8", AlwaysPublic: false, Body: d.HTML, CreatedAt: now,
		}); err != nil {
			return nil, docManifest{}, fmt.Errorf("store document %s: %w", d.Kind, err)
		}
	}

	set := make([]docManifestEntry, 0, len(docs))
	for _, d := range docs {
		set = append(set, docManifestEntry{Hash: d.Hash, Kind: d.Kind, Title: d.Title})
	}
	sort.Slice(set, func(i, j int) bool { return set[i].Kind < set[j].Kind })
	canonSet, err := canonicalJSON(set)
	if err != nil {
		return nil, docManifest{}, err
	}
	man := docManifest{Spec: docSpec, ManifestHash: sha256Hex(canonSet), Set: set}
	m["documents"] = man

	bound, err := json.Marshal(m)
	if err != nil {
		return nil, docManifest{}, err
	}
	return bound, man, nil
}

// docContextFor resolves the deterministic document inputs from clean terms. It
// reuses the compiler's matrixTerms parser so the documents describe exactly the
// same matrix the policy compiler enforces.
func (s *server) docContextFor(iss *Issuance, cleanTerms map[string]any) (docContext, error) {
	raw, _ := json.Marshal(cleanTerms)
	var mt matrixTerms
	_ = json.Unmarshal(raw, &mt)

	// Fail closed (W-7): documents must not be generated for a structure the
	// characterization cannot classify.
	ch, err := characterize(structureName(iss, raw))
	if err != nil {
		return docContext{}, err
	}
	structure := ch.Structure

	issuerName := iss.Name
	entityJurisdiction := ""
	if iss.EntityID != "" {
		if e, _ := s.st.EntityByID(iss.EntityID); e != nil {
			if e.Name != "" {
				issuerName = e.Name
			}
			entityJurisdiction = strings.TrimSpace(e.Jurisdiction)
		}
	}

	// The bearer election: terms.enforcement when the terms name it (the SPA
	// always commits the election into the terms), else the issuance row's
	// persisted election (the deploy path sets it before generating documents).
	enforcement := strings.ToLower(strings.TrimSpace(iss.Enforcement))
	if v, ok := cleanTerms["enforcement"].(string); ok && strings.TrimSpace(v) != "" {
		enforcement = strings.ToLower(strings.TrimSpace(v))
	}

	jurs := make([]jurisdictionRow, 0, len(mt.Jurisdictions))
	for code, jt := range mt.Jurisdictions {
		nc := normalizeResidence(code)
		if nc == "" {
			nc = strings.ToUpper(strings.TrimSpace(code))
		}
		elig := append([]string(nil), jt.EligCategories...)
		sort.Strings(elig)
		jurs = append(jurs, jurisdictionRow{Code: nc, Access: strings.ToLower(strings.TrimSpace(jt.Access)), Elig: elig})
	}
	sort.Slice(jurs, func(i, j int) bool { return jurs[i].Code < jurs[j].Code })

	caps := make([]capRow, 0, len(mt.EUCaps))
	for k, v := range mt.EUCaps {
		caps = append(caps, capRow{Key: k, Value: v})
	}
	sort.Slice(caps, func(i, j int) bool { return caps[i].Key < caps[j].Key })

	price := ""
	if p, quote, ok := offeringPrice(raw); ok {
		price = fmt.Sprintf("Offering reference price: %s %s per token. This is a reference price, not a market quote.", trimFloat(p), html.EscapeString(quote))
	}

	return docContext{
		IssuerName:         issuerName,
		OfferingName:       iss.Name,
		Ticker:             iss.Ticker,
		Structure:          structure,
		Char:               ch,
		Jurisdictions:      jurs,
		EUCaps:             caps,
		Lockup:             lockupStatement(mt),
		RegS:               regSStatement(mt),
		Price:              price,
		IssuerExternal:     iss.IssuerExternal,
		Bearer:             enforcement == "bearer",
		EntityJurisdiction: entityJurisdiction,
	}, nil
}

func lockupStatement(mt matrixTerms) string {
	if mt.LockupHeight > 0 {
		return fmt.Sprintf("Transfers are locked until Sequentia block %d. The block height, not a calendar date, is binding.", mt.LockupHeight)
	}
	if mt.LockupDays > 0 {
		months := (mt.LockupDays + 15) / 30
		if months < 1 {
			months = 1
		}
		return fmt.Sprintf("Transfers are locked until a Sequentia block height fixed at issuance, approximately %d months after admission (about %d days at the documented block cadence). Block times on this testnet vary, so the height, not the date, is binding.", months, mt.LockupDays)
	}
	return "No transfer lockup is configured for this offering."
}

func regSStatement(mt matrixTerms) string {
	if mt.RegS == nil {
		return ""
	}
	prefix := strings.TrimSpace(mt.RegS.Prefix)
	if prefix == "" {
		prefix = "j:US"
	}
	if mt.RegS.UntilHeight > 0 {
		return fmt.Sprintf("Offshore purchasers under Regulation S (17 CFR 230.903) are subject to a distribution-compliance period. Transfers to holders carrying a %s category are refused by the policy server until Sequentia block %d.", prefix, mt.RegS.UntilHeight)
	}
	if mt.RegS.Days > 0 {
		return fmt.Sprintf("Offshore purchasers under Regulation S (17 CFR 230.903) are subject to a distribution-compliance period of approximately %d days, enforced as a Sequentia block height fixed at issuance. Transfers to holders carrying a %s category are refused by the policy server until that height.", mt.RegS.Days, prefix)
	}
	return ""
}

// buildDocumentSet is the pure generator: docContext in, sorted-by-kind set out.
// A bearer context selects the bearer charter set; everything else emits the
// standard set, byte-identical to what it always produced.
func buildDocumentSet(ctx docContext) []GeneratedDoc {
	builders := map[string]func(docContext) (string, string){
		"offering-memorandum":        docOfferingMemorandum,
		"subscription-agreement":     docSubscriptionAgreement,
		"operating-agreement":        docOperatingAgreement,
		"escrow-terms":               docEscrowTerms,
		"kid-summary":                docKIDSummary,
		"uk-investor-statement":      docUKInvestorStatement,
		"lift-artifact":              docLiftArtifact,
		"articles-of-incorporation":  docArticlesOfIncorporation,
		"freeze-power-charter":       docFreezePowerCharter,
		"shareholder-terms":          docShareholderTerms,
		"risk-acceptance-instrument": docRiskAcceptanceInstrument,
	}
	kinds := docKinds
	if ctx.Bearer {
		kinds = bearerDocKinds
	}
	out := make([]GeneratedDoc, 0, len(kinds))
	for _, kind := range kinds {
		title, body := builders[kind](ctx)
		out = append(out, GeneratedDoc{Kind: kind, Title: title, HTML: []byte(body), Hash: sha256Hex([]byte(body))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

// --- HTML building -----------------------------------------------------------

// docPage wraps a title and body in a self-contained, deterministic HTML page.
// The generator embeds no timestamp so the bytes are stable and content-address
// reproducibly.
func docPage(kind, title, body string) string {
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\">")
	b.WriteString("<meta name=\"document-spec\" content=\"" + docSpec + "\">")
	b.WriteString("<meta name=\"document-kind\" content=\"" + html.EscapeString(kind) + "\">")
	b.WriteString("<title>" + html.EscapeString(title) + "</title></head><body>")
	b.WriteString(body)
	b.WriteString("<hr><p class=\"disclaimer\"><strong>SIMULATED PROOF OF CONCEPT.</strong> " + html.EscapeString(simulatedDisclaim) + "</p>")
	b.WriteString("</body></html>\n")
	return b.String()
}

func heading(text string) string { return "<h2>" + html.EscapeString(text) + "</h2>" }
func para(text string) string    { return "<p>" + html.EscapeString(text) + "</p>" }

func riskFactors(items []string) string {
	var b strings.Builder
	b.WriteString("<ul>")
	for _, it := range items {
		b.WriteString("<li>" + html.EscapeString(it) + "</li>")
	}
	b.WriteString("</ul>")
	return b.String()
}

func matrixTable(ctx docContext) string {
	if len(ctx.Jurisdictions) == 0 {
		return para("No jurisdiction access rows are configured. Every jurisdiction not named here is excluded by default.")
	}
	var b strings.Builder
	b.WriteString("<table border=\"1\" cellpadding=\"4\"><thead><tr><th>Jurisdiction</th><th>Access</th><th>Eligibility categories</th></tr></thead><tbody>")
	for _, j := range ctx.Jurisdictions {
		elig := strings.Join(j.Elig, ", ")
		if elig == "" {
			elig = "structure default"
		}
		b.WriteString("<tr><td>" + html.EscapeString(j.Code) + "</td><td>" + html.EscapeString(j.Access) + "</td><td>" + html.EscapeString(elig) + "</td></tr>")
	}
	b.WriteString("</tbody></table>")
	return b.String()
}

// custodyRiskFactor states who holds the keys. The issuer key is always the issuing
// entity's own, held off the platform, so the platform never holds a key that can
// move a holder's position.
func custodyRiskFactor() string {
	return "Keys and control: the issuer key for this asset is the issuing entity's own key, held off the platform, so a " +
		"clawback requires the issuer's signature and the platform operator (" + demoPlatformName + ") cannot move a " +
		"holder's position on its own. The holder's half of the two-of-two policy co-signature is negative control: it can " +
		"refuse a transfer, not originate one. The platform's role in a transfer is the policy co-signature that enforces " +
		"the issuer's rules."
}

func structureRiskFactors(ctx docContext) []string {
	base := []string{
		custodyRiskFactor(),
		"Restricted transfer: this is a permissioned security. Every transfer between eligible holders is policy co-signed, " +
			"and an ineligible or out-of-window transfer is refused for the life of the asset. A refusal at settlement is an expected outcome.",
		"Finality: nothing is final at zero confirmations. The Sequentia network follows Bitcoin reorgs in real time by " +
			"design, so a state is only as final as its Bitcoin anchor is deep and may regress if that anchor is reorged.",
		"Confidentiality: the Sequentia network is transparent by default and confidentiality is a per-transfer choice. Any " +
			"holder may move this token confidentially in a given transfer, which hides that transfer's amount and asset from " +
			"outside observers only. The issuer and the registrar still see every holding and every transfer through the policy " +
			"server; it is not privacy from your registrar.",
		"Liquidity: a listing is not a market. The network provides a secondary market with access to Bitcoin liquidity, " +
			"but it does not create demand, underwrite, or make an illiquid asset liquid.",
		"Investor-jurisdiction law: the offering is subject to the law of every jurisdiction where it is made available. " +
			"The EU over-enforcement note applies: the on-chain per-category holder cap approximates the Prospectus " +
			"Regulation Article 1(4)(b) sub-150 exemption, which counts offerees rather than holders, so refusing the 150th " +
			"secondary transferee is a deliberate conservative approximation.",
		"Key loss: if you lose the key that controls your holding, recovery depends on the clawback-and-redeliver runbook, " +
			"whose trust implication is the custody conclusion above.",
	}
	switch ctx.Structure {
	case "equity-spv":
		base = append(base,
			"AIF characterization: "+ctx.Char.Analysis,
			"Concentration: a single-purpose holding vehicle concentrates risk in its held assets, and the holder is remote from the underlying.",
		)
	case "debt-yield":
		base = append(base,
			"AIF characterization: "+ctx.Char.Analysis,
			"Credit and default: yield is not guaranteed, and the credit and default risk of the underlying pool is borne by the holder.",
		)
	case "depository-receipt":
		base = append(base,
			"Depository receipt: "+ctx.Char.Analysis,
			"Withholding: a receipt mirroring a US-listed security carries dividend-equivalent withholding under chapter 3 and section 871(m), and a US-person exclusion.",
		)
	default:
		base = append(base,
			"Equity: "+ctx.Char.Analysis,
			"Dilution and subordination: equity is subordinate to debt and may be diluted by future issuance.",
		)
	}
	if ctx.Char.PRIIPs {
		base = append(base, "PRIIPs: a production offering of this structure to EU retail requires a PRIIPs-compliant key information document. The KID-style summary in this package is labeled as not being a PRIIPs KID.")
	}
	return base
}

// bearerRiskFactors is the risk-factor list for a bearer offering: the freeze
// power, finality, confidentiality, liquidity, key loss, and the regulatory
// position replace the restricted-transfer factors, which do not apply.
func bearerRiskFactors(ctx docContext) []string {
	return []string{
		"Court-ordered freeze: the issuing company holds a supervised-asset freeze power over this asset, exercisable only on a " +
			"binding order of a court of competent jurisdiction or of the RFSA. A frozen balance cannot move until the order is " +
			"lifted, and every freeze and lift is a signed public chain record beside the order document's fingerprint.",
		"Finality: nothing is final at zero confirmations. The Sequentia network follows Bitcoin reorgs in real time by " +
			"design, so a state is only as final as its Bitcoin anchor is deep and may regress if that anchor is reorged.",
		"Confidentiality: the Sequentia network is transparent by default and confidentiality is a per-transfer choice. A " +
			"confidential transfer hides that transfer's amount and asset from outside observers only; a holder claiming " +
			"corporate rights proves their holding to the transfer agent.",
		"Liquidity: free tradability is not a market. The network provides a secondary market with access to Bitcoin " +
			"liquidity, but it does not create demand, underwrite, or make an illiquid asset liquid.",
		"Key loss: this is a bearer instrument. If you lose the key that controls your holding there is no clawback path; " +
			"recovery is limited to what a binding order can direct through the supervised-asset freeze and reissue powers.",
		"Regulatory position: the offering is made on the issuer's signed acceptance that its company has no United States " +
			"operations, assets, or banking, and that regulators may object. The risk acceptance instrument in this document " +
			"set records that acceptance; the issuing company, not the platform, bears the stated risk.",
		ctx.Char.Instrument + ": " + ctx.Char.Analysis,
	}
}

func docOfferingMemorandum(ctx docContext) (string, string) {
	title := "Offering Memorandum: " + ctx.OfferingName + " (" + ctx.Ticker + ")"
	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>")
	b.WriteString(para("Issuer of record: " + ctx.IssuerName + ". Issuance jurisdiction: " + issuanceRegime + ". Registrar and transfer agent: " + demoPlatformNote + " (RFSA registrations are simulated in this proof of concept)."))
	if ctx.Price != "" {
		b.WriteString(para(ctx.Price))
	}
	b.WriteString(heading("Instrument characterization"))
	b.WriteString(para("Structure: " + ctx.Structure + ". Instrument: " + ctx.Char.Instrument + "."))
	b.WriteString(para(ctx.Char.Analysis))
	if ctx.Bearer {
		b.WriteString(heading("Transferability"))
		b.WriteString(para("This is a freely tradable token: anyone may hold and trade it, no eligibility matrix is enforced on chain, and no transfer requires the company's approval. The company has waived transfer approval in its articles of incorporation. Economic and transfer rights pass with the token; dividends and voting require registering as a holder of record, as the shareholder terms state."))
		b.WriteString(heading("Supervision"))
		b.WriteString(para("The asset is a supervised Sequentia asset. The company holds a freeze power exercisable only on a binding order of a court or of the RFSA, exercised as a signed public chain record beside the order's fingerprint. The freeze power charter in this document set states the powers, their limits, and the custody of the keys."))
		b.WriteString(heading("Risk factors"))
		b.WriteString(riskFactors(bearerRiskFactors(ctx)))
		return title, docPage("offering-memorandum", title, b.String())
	}
	b.WriteString(heading("Jurisdictional access"))
	b.WriteString(matrixTable(ctx))
	b.WriteString(para(ctx.Char.Marketing["eu"]))
	b.WriteString(para(ctx.Char.Marketing["uk"]))
	b.WriteString(para(ctx.Char.Marketing["us"]))
	b.WriteString(heading("Transfer restrictions"))
	b.WriteString(para(ctx.Lockup))
	if ctx.RegS != "" {
		b.WriteString(para(ctx.RegS))
	}
	b.WriteString(para("US 506(c) purchasers hold Rule 144 restricted securities: resale is limited to the Rule 144 holding period, Section 4(a)(7), or an offshore resale under Rule 904. Concurrent Regulation S and 506(c) tranches are distinct, non-integrated offerings under the Rule 152 framework."))
	b.WriteString(heading("Risk factors"))
	b.WriteString(riskFactors(structureRiskFactors(ctx)))
	return title, docPage("offering-memorandum", title, b.String())
}

func docSubscriptionAgreement(ctx docContext) (string, string) {
	title := "Subscription Agreement: " + ctx.OfferingName + " (" + ctx.Ticker + ")"
	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>")
	if ctx.Bearer {
		b.WriteString(para("The subscriber agrees to acquire tokens of " + ctx.Ticker + " issued by " + ctx.IssuerName + ". The token is freely tradable; corporate rights attach as stated in the shareholder terms."))
		b.WriteString(heading("Representations"))
		b.WriteString(riskFactors([]string{
			"The subscriber has read the offering memorandum, the shareholder terms, the freeze power charter, and the risk acceptance instrument.",
			"The subscriber acknowledges that the issuing company holds a freeze power over the asset, exercisable only on a binding order of a court or of the RFSA, and that every freeze and lift is a signed public chain record.",
			"The subscriber acknowledges that economic and transfer rights pass with the token, and that dividends and voting require registering as a holder of record through the transfer agent.",
			"The subscriber acknowledges the finality and reorg behavior stated in the offering memorandum, and that key loss has no clawback path.",
		}))
		b.WriteString(heading("Acknowledgment"))
		b.WriteString(para("The subscriber has read the offering memorandum and the risk factors."))
		return title, docPage("subscription-agreement", title, b.String())
	}
	b.WriteString(para("The subscriber agrees to acquire tokens of " + ctx.Ticker + " issued by " + ctx.IssuerName + ", subject to the eligibility and transfer restrictions enforced by the policy server for the life of the asset."))
	b.WriteString(heading("Representations"))
	b.WriteString(riskFactors([]string{
		"The subscriber is an eligible, registered holder whose SeqPal ID carries a category this asset admits.",
		"The subscriber acknowledges that this is a permissioned security and that transfers are policy co-signed between eligible holders, not free.",
		"The subscriber acknowledges the custody conclusion and the finality and reorg behavior stated in the offering memorandum.",
		"US purchasers under Rule 506(c) confirm verified accredited status and acknowledge Rule 144 restricted-securities resale limits.",
	}))
	b.WriteString(heading("Distribution compliance"))
	if ctx.RegS != "" {
		b.WriteString(para(ctx.RegS))
	}
	b.WriteString(para("Offshore distribution complies with Regulation S, 17 CFR 230.903. The offer is subject to the law of every jurisdiction where it is made available; the issuer, not the platform, is responsible for the lawfulness of the offer."))
	b.WriteString(heading("Lockup"))
	b.WriteString(para(ctx.Lockup))
	b.WriteString(heading("Acknowledgment"))
	b.WriteString(para("The subscriber has read the offering memorandum and the risk factors and, where the subscriber is EU retail admitted by lift, the KID-style summary and its PRIIPs label."))
	return title, docPage("subscription-agreement", title, b.String())
}

func docOperatingAgreement(ctx docContext) (string, string) {
	title := "Operating Agreement: " + ctx.IssuerName
	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>")
	b.WriteString(para("This agreement governs " + ctx.IssuerName + ", an entity issuing under " + issuanceRegime + "."))
	b.WriteString(heading("Dispositive register"))
	b.WriteString(para("The register of members is the on-chain holder set of the Sequentia network asset " + ctx.Ticker + ", as reported by the OpenAMP policy server. Under Próspera law, that on-chain register is dispositive: it, and not any off-chain copy, determines legal ownership. A transfer is effective only when the policy co-signature completes and the transaction confirms on the Sequentia network, which anchors to Bitcoin."))
	b.WriteString(heading("Transfer mechanics"))
	b.WriteString(para("Every transfer between eligible holders requires the policy co-signature. An ineligible, locked-up, or out-of-window transfer is refused and recorded in the public transparency log."))
	b.WriteString(heading("Amendments"))
	b.WriteString(para("Post-issuance changes to the policy rules are recorded as content-addressed rules-amendment artifacts anchored via the transparency log, so the on-chain commitment remains the genesis terms_hash plus the anchored amendment chain."))
	return title, docPage("operating-agreement", title, b.String())
}

func docEscrowTerms(ctx docContext) (string, string) {
	title := "Escrow Terms: " + ctx.OfferingName + " (" + ctx.Ticker + ")"
	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>")
	b.WriteString(para("Subscriptions settle through a per-offering segregated escrow enclave that receives the mint, so primary distribution flows from a scoped escrow rather than the issuer's personal account."))
	b.WriteString(heading("Non-custodial USDX commitment model"))
	b.WriteString(para("The production design for the USDX rail is non-custodial: the investor funds a pay-to-taproot address only they control, and funds are never in the operator's custody before settlement. This replaces any custodial-escrow superiority claim for the USDX rail."))
	b.WriteString(heading("Fee accrual"))
	b.WriteString(para("The platform escrow fee accrues at the moment a deposit confirms into escrow, computed on the deposited amount, and is due regardless of the offering's outcome: on release it is deducted from the amount paid to the issuer, and on a refund it is withheld from the amount returned to the investor."))
	b.WriteString(heading("Release and refund"))
	b.WriteString(para("Escrow releases only to registered ordinary payout addresses on the correct network, and refunds run through an explicit refund and expiry state machine. Fiat rails and some escrow mechanics are simulated in this proof of concept and are labeled on every money surface."))
	return title, docPage("escrow-terms", title, b.String())
}

func docKIDSummary(ctx docContext) (string, string) {
	title := "KID-style Summary: " + ctx.OfferingName + " (" + ctx.Ticker + ")"
	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>")
	b.WriteString("<p class=\"label\"><strong>KID-style summary. This is not a PRIIPs KID.</strong> Production offerings of SPV or DR structures to EU retail require a PRIIPs-compliant key information document.</p>")
	b.WriteString(heading("What this is"))
	b.WriteString(para(ctx.Char.Instrument + " issued by " + ctx.IssuerName + " (" + ctx.Structure + ")."))
	if ctx.Price != "" {
		b.WriteString(para(ctx.Price))
	}
	b.WriteString(heading("Risk and holding period"))
	b.WriteString(para(ctx.Lockup))
	b.WriteString(para("This is a restricted, illiquid security. You could lose your entire investment. A refusal at settlement is expected for a permissioned asset."))
	if ctx.Char.PRIIPs {
		b.WriteString(para("PRIIPs applies to this structure for EU retail; a compliant KID must be produced before any such offer in production."))
	}
	return title, docPage("kid-summary", title, b.String())
}

func docUKInvestorStatement(ctx docContext) (string, string) {
	title := "UK Statutory Investor Statement (SI 2024/301)"
	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>")
	b.WriteString(para("A UK resident must record a statutory investor statement before the offering renders, using the wording and thresholds in the Financial Services and Markets Act 2000 (Financial Promotion) (Amendment) Order 2024, SI 2024/301. The statement is valid for 12 months and is recorded on the SeqPal ID."))
	b.WriteString(heading("High net worth individual statement"))
	b.WriteString(para("I declare that I am a high net worth individual because at least one of the following applied in the last financial year: I had an annual income of GBP 100,000 or more; or I held net assets of GBP 250,000 or more, excluding my primary residence, any rights under a qualifying insurance contract, and certain pension benefits. I accept that the investments to which the promotions will relate may expose me to a significant risk of losing all of the money invested."))
	b.WriteString(heading("Self-certified sophisticated investor statement"))
	b.WriteString(para("I declare that I am a self-certified sophisticated investor because at least one of the statutory criteria applies to me, for example that I have made more than one investment in an unlisted company in the previous two years. I accept that the investments may expose me to a significant risk of losing all of the money invested."))
	if ctx.Char.UKGate == "cis-promotion" {
		b.WriteString(para("Because this offering is characterized as a collective investment scheme, UK promotion runs through FSMA section 238 and the Promotion of Collective Investment Schemes (Exemptions) Order rather than the FPO Articles 48 and 50A route, and the statement above is recorded on that basis."))
	}
	return title, docPage("uk-investor-statement", title, b.String())
}

func docLiftArtifact(ctx docContext) (string, string) {
	title := "Lift Artifact and Basis of Admission: " + ctx.Ticker
	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>")
	b.WriteString(para("This artifact records the basis on which a restricted jurisdiction admits retail investors to this offering (the lift), together with the jurisdiction, the stated basis, the supporting artifact hash, and the validity window. The highest-stakes decision in the flow, admitting retail in a restricted jurisdiction, is recorded here and logged rather than toggled silently."))
	b.WriteString(heading("Basis of admission"))
	if !ctx.Char.EURetailLift {
		b.WriteString(para("This structure is characterized as an alternative investment fund or collective investment scheme, so the EU retail lift is disabled: EU access is restricted to professional categories consistent with Article 42 NPPR, and no EU retail admission by lift is available for this offering."))
	} else {
		b.WriteString(para("Where a restricted jurisdiction is lifted to admit retail, an uploaded basis artifact (jurisdiction, basis, hash, validity) is bound into the terms and logged. EU retail admission additionally requires the PRIIPs position stated in the KID-style summary."))
	}
	b.WriteString(heading("Configured access"))
	b.WriteString(matrixTable(ctx))
	return title, docPage("lift-artifact", title, b.String())
}

// --- bearer charter documents ------------------------------------------------
//
// The four charter documents a bearer election adds. Terms shown in brackets
// are fillable fields completed at execution; everything else is generated
// deterministically from the issuance and its entity record, so the on-chain
// terms_hash commits to these exact bytes like every other document.

func docArticlesOfIncorporation(ctx docContext) (string, string) {
	title := "Articles of Incorporation: " + ctx.IssuerName
	regOffice := "[Registered Office]"
	if ctx.EntityJurisdiction != "" {
		regOffice = ctx.EntityJurisdiction
	}
	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>")
	b.WriteString(para("Articles of incorporation of " + ctx.IssuerName + ", an entity of Próspera (Roatán, Honduras). Registered office: " + regOffice + ". The company issues under " + issuanceRegime + "."))
	b.WriteString(heading("Article 1: Regulatory election"))
	b.WriteString(para("The incorporator elects that the shares of the company be issued as register-based uncertificated securities under [Ledger-Securities Framework], the ledger-securities framework the incorporator designates under the RFSA. The election follows the model of articles 973d to 973i of the Swiss Code of Obligations, register-based uncertificated securities, adapted as an election made under the RFSA: the shares exist as entries in a securities ledger whose technical design guarantees that only the holder of a ledger entry can dispose of the corresponding share, and the ledger's integrity is secured by the joint recording of entries."))
	b.WriteString(heading("Article 2: Share ledger designation"))
	b.WriteString(para("The share ledger of the company is the Sequentia network asset created by the issuance whose on-chain contract commits to the terms containing these articles. Each token of that asset is one entry in the share ledger. Legal title to a share passes with the on-chain transfer of the token that represents it, automatically and without further act of the company: no book entry, approval, or registration by the company is required for a transfer of title, and none has any effect on it."))
	b.WriteString(heading("Article 3: Transfer approval waived"))
	b.WriteString(para("The company waives, for the life of the asset, any right or duty to approve, register, or restrict secondary transfers of its tokens. The company will not maintain, and no person may require it to maintain, any transfer restriction, approval step, or eligibility condition on a secondary transfer. The only powers the company retains over outstanding tokens are those in the freeze power charter, exercisable solely on a binding order of a court or of the RFSA."))
	b.WriteString(heading("Article 4: Incorporator and board"))
	b.WriteString(para("Company name: " + ctx.IssuerName + ". Incorporator: [Incorporator]. Directors: [Directors]. Officers: [Officers]. These terms are completed from the company's entity record at execution; a term shown in brackets is a fillable field."))
	return title, docPage("articles-of-incorporation", title, b.String())
}

func docFreezePowerCharter(ctx docContext) (string, string) {
	title := "Freeze Power Charter: " + ctx.OfferingName + " (" + ctx.Ticker + ")"
	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>")
	b.WriteString(para("This charter states the supervised-asset powers " + ctx.IssuerName + " holds over " + ctx.Ticker + ", their sole trigger, the custody of the keys that exercise them, and their limits."))
	b.WriteString(heading("The freeze power"))
	b.WriteString(para("The company holds a freeze power over single-owner balances of the asset, exercisable only on a binding order of a court of competent jurisdiction or of the RFSA. The company does not freeze on its own initiative, on a counterparty's request, or on commercial grounds. The order is the sole trigger, and lifting a freeze requires the same: a binding order."))
	b.WriteString(heading("Two-key custody"))
	b.WriteString(para("The powers are held under a two-key arrangement. The operational key is held by the company and signs day-to-day exercises. The recovery key is held offline and has one power only: replacing the supervision keys if the operational key is lost or compromised. Recovery key custody: [Recovery Key Custody Option]."))
	b.WriteString(heading("The public record"))
	b.WriteString(para("Every freeze and every lift is a signed public record on the Sequentia network, recorded beside the fingerprint (hash) of the order document that authorized it. Anyone can enumerate the asset's freeze register from the chain and verify each record's signature; no freeze can be exercised or lifted invisibly."))
	b.WriteString(heading("The single-owner limitation"))
	b.WriteString(para("A freeze binds single-owner balances only. A balance held inside a shared contract, such as a payment channel, is out of a freeze's reach until it returns to a single-owner balance. This is stated plainly because it is parity, not a gap: on account-based chains a freeze reaches an account's own balance and likewise cannot reach value locked inside a shared contract."))
	b.WriteString(heading("The pause power"))
	b.WriteString(para("Where the pause capability is elected in the supervision descriptor committed in the asset id, the company may pause all transfers of the asset on the same condition as a freeze: a binding order of a court or of the RFSA, recorded the same public way, and lifted the same way. Where the capability is not elected it does not exist and can never be added, because the descriptor is committed in the asset id."))
	return title, docPage("freeze-power-charter", title, b.String())
}

func docShareholderTerms(ctx docContext) (string, string) {
	title := "Shareholder Terms: " + ctx.OfferingName + " (" + ctx.Ticker + ")"
	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>")
	b.WriteString(heading("The dual state"))
	b.WriteString(para("Anyone may hold and trade the token. Holding carries full economic and transfer rights: the token moves like any freely tradable asset, with no eligibility check and no approval by the company. Corporate rights, dividends and voting, attach only to a holder of record: a holder who has registered through the transfer agent's identity verification and presented a signed proof of holding over their on-chain positions at the record height."))
	b.WriteString(heading("Record dates"))
	b.WriteString(para("A corporate action fixes a record height, a Sequentia block height. The holder set for the action is the chain snapshot of the asset's outputs at that height, taken and published by the transfer agent and verifiable by anyone against the chain itself. A holder participates by proving key control over outputs in that snapshot; the proof binds the action, the outputs, and the use it is presented for."))
	b.WriteString(heading("Unclaimed distributions"))
	b.WriteString(para("A distribution that is not claimed by a holder of record is retained by the transfer agent for the benefit of the entitled holder. After [Escheat Period] years an unclaimed amount reverts to the company."))
	return title, docPage("shareholder-terms", title, b.String())
}

func docRiskAcceptanceInstrument(ctx docContext) (string, string) {
	title := "Risk Acceptance Instrument: " + ctx.OfferingName + " (" + ctx.Ticker + ")"
	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>")
	b.WriteString(para("This instrument records the risk acceptance the issuer of " + ctx.Ticker + " signs with its own key before a freely tradable deployment. The signature is a real cryptographic signature over the statements below, verified against the issuer's registered key and recorded with the issuance."))
	b.WriteString(heading("The attested statements"))
	b.WriteString(riskFactors([]string{
		"My company has no United States operations, assets, or banking.",
		"I accept in writing that United States regulators may object, and that this is my company’s risk.",
	}))
	b.WriteString(heading("Annual refresh"))
	b.WriteString(para("The attestation is valid for one year from signature and is refreshed by signing it again. An expired attestation blocks a new deployment under this election until it is refreshed."))
	b.WriteString(heading("Who bears the risk"))
	b.WriteString(para("The company, not the platform, bears the stated risk. " + demoPlatformName + " records the attestation and requires its presence before a freely tradable deployment; the election, the statements, and their consequences are the company's own."))
	return title, docPage("risk-acceptance-instrument", title, b.String())
}

// trimFloat renders a price without a trailing ".000000" noise while staying
// deterministic (fixed formatting, no locale).
func trimFloat(f float64) string {
	s := fmt.Sprintf("%.8f", f)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" {
		s = "0"
	}
	return s
}

// jsonCompact is a tiny helper used by callers that need a stable compact
// encoding for hashing a struct (never for canonical terms; use canonicalJSON
// for that).
func jsonCompact(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return bytes.TrimRight(buf.Bytes(), "\n")
}
