// Package einvoice validates electronic invoices against the European norm
// EN 16931 in pure Go: no Java, no XSLT processor, no network calls.
//
// The official Schematron rule sets are embedded for both standard syntaxes —
// CI/UN-CEFACT (CII, used by Factur-X, ZUGFeRD and XRechnung) and OASIS UBL
// (used by Peppol BIS) — and are evaluated by a native XPath 2.0 subset
// engine. The rule sets are the ones published by the European Commission, so
// the verdicts match the reference validators.
//
// The typical call is:
//
//	res, err := einvoice.Validate(data, einvoice.Options{})
//	if err != nil { ... }        // the document is not an EN 16931 invoice
//	if !res.Valid { ... }        // res.Findings lists the broken rules
//
// Validation answers two separate questions. Does the document have the
// structure its syntax allows, checked against the official XML schema of that
// syntax; and do the EN 16931 business rules hold, checked against the official
// Schematron? A document is Valid only when both are clean, and the two kinds
// of problem are reported apart: structural problems in Result.Schema, business
// rule problems in Result.Findings.
//
// Even a fully valid document need not follow the rules of the national or
// network profile it claims to conform to. Pass a CIUS such as XRechnung in
// Options.CIUS to check the norm and that country's additions together, or a
// rule-set bundle such as Peppol BIS in Options.Profile to hear the verdict the
// network itself would give. Every Finding names the rule set it came from in
// Finding.Source.
package einvoice

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/dcnws/invoicekit.go/internal/en16931"
	"github.com/dcnws/invoicekit.go/internal/sch"
)

// Version is the release of this library, reported by the command line, the
// WebAssembly build and the HTTP service so that a verdict can always be traced
// back to the artefacts that produced it.
const Version = "0.2.13"

// Syntax identifies the concrete syntax of an invoice document.
type Syntax string

const (
	// SyntaxCII is the UN/CEFACT Cross Industry Invoice syntax, used by
	// Factur-X, ZUGFeRD and the German XRechnung.
	SyntaxCII Syntax = "CII"
	// SyntaxUBL is the OASIS UBL Invoice / CreditNote syntax, used by Peppol.
	SyntaxUBL Syntax = "UBL"
)

// CIUS names a rule set that customises EN 16931 for one country or network.
// EN 16931 calls this segment the CIUS — the Customisation Information User
// Segment: it may restrict the norm and add rules, but it may never relax it,
// so a CIUS document is checked against the EN 16931 rules and that rule set
// together. The name a document claims travels in BT-24, the business process
// type, and CiusVersion documents which upstream release each rule set came
// from.
type CIUS string

const (
	// CIUSXRechnung is the German XRechnung CIUS for public administration
	// invoicing (see xeinkauf.de). Its rules are the BR-DE, BR-DE-CVD,
	// BR-DEX and BR-TMP assertions.
	CIUSXRechnung CIUS = "xrechnung"
)

// Profile names a published rule-set bundle that a network validates against as
// a whole, for example Peppol BIS Billing 3.0. It differs from a CIUS in that
// the bundle ships its own copy of the EN 16931 assertions, so choosing a
// profile replaces the rule sets this library would otherwise run — versions
// included — rather than adding to them. That is what makes a verdict match the
// network's own validator. ProfileNames() lists the bundles that are embedded.
type Profile string

const (
	// ProfilePeppol is Peppol BIS Billing 3.0: the EN 16931 Schematrons as CEN
	// publishes them, plus the rules OpenPEPPOL adds (PEPPOL-EN16931-*,
	// PEPPOL-COMMON-*) and the country rules shipped in the same release
	// (DE-, DK-, GR-, IT-, NL-, NO-, SE-).
	ProfilePeppol Profile = "peppol"
)

// Options controls a validation run. The zero value validates a document of
// automatically detected syntax with every active rule phase.
type Options struct {
	// Syntax forces a syntax instead of detecting it from the document.
	Syntax Syntax
	// Phases restricts the Schematron phases to run. Known phase names can be
	// listed with Phases(). Empty means every phase the rule set activates by
	// default. A CIUS rule set always runs its own phases.
	Phases []string
	// CIUS adds a national or network rule set on top of EN 16931, for example
	// CIUSXRechnung. Empty checks the norm alone. CiusNames() lists the ones
	// this library carries.
	CIUS CIUS
	// Profile selects a published rule-set bundle such as ProfilePeppol, which
	// carries its own copy of the EN 16931 assertions and so replaces the rule
	// sets this library runs by default. A CIUS still runs on top of it.
	// ProfileNames() lists what is available.
	Profile Profile
}

// CheckRuleSets reports whether the rule sets named in opts are ones this
// library carries. It is worth calling before any document is touched: a
// misspelt rule set is the caller's mistake, and reporting it as a finding
// would send someone off to repair an invoice that was never the problem.
func CheckRuleSets(opt Options) error {
	if opt.CIUS != "" && CiusVersion(opt.CIUS) == "" {
		return fmt.Errorf("einvoice: unknown CIUS %q (available: %s)", opt.CIUS, strings.Join(CiusNames(), ", "))
	}
	if opt.Profile != "" && ProfileVersion(opt.Profile) == "" {
		return fmt.Errorf("einvoice: unknown rule-set bundle %q (available: %s)", opt.Profile, strings.Join(ProfileNames(), ", "))
	}
	return nil
}

// DecodeInvoiceModel reads one invoice model — the JSON a generator is given —
// from r. It is strict in the two ways that matter for an invoice. A key the
// model has no field for is refused by name: the author of that JSON believes the
// value is on the document, and it will not be, which is a worse way to find out
// than an invalid invoice three systems away. And a second JSON object after the
// first is refused, because a decoder that stops at the first document quietly
// turns a batch of invoices into one.
//
// Strictness is deliberate, not fussiness: a field the model does not carry is
// usually one the standard does not have either, and inventing one — a P.O. box,
// say — produces a document CII rejects outright (CII-DT-083) and UBL drops.
func DecodeInvoiceModel(r io.Reader) (Invoice, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var inv Invoice
	if err := dec.Decode(&inv); err != nil {
		return Invoice{}, modelDecodeError(err)
	}
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		return Invoice{}, fmt.Errorf("einvoice: invoice model: more than one document: a second invoice model follows the first")
	}
	return inv, nil
}

// modelHints covers the keys callers reach for when the standard does have
// something nearby, so the refusal says where the value goes instead of only
// saying it does not fit. Every entry is a claim about EN 16931, and each one
// was checked against the rule sets this repository ships: there is no payer
// party (UBL-CR-439 warns away a PayerParty on a mandate, and the buyer BG-7 is
// the payer), no P.O. box term (CII-DT-083 forbids ram:PostOfficeBox outright),
// the deliver-to address is BG-15, an account hangs off a party rather than off
// the invoice (BT-84, BT-91), and BT-20 is called Payment terms.
var modelHints = map[string]string{
	"payer":           "EN 16931 has no payer party: the buyer (BG-7) is the payer, and its account is buyerAccount",
	"payerParty":      "EN 16931 has no payer party: the buyer (BG-7) is the payer, and its account is buyerAccount",
	"postName":        "EN 16931 has no P.O. box term: CII-DT-083 forbids the element, so a box number goes in an address line",
	"postOfficeBox":   "EN 16931 has no P.O. box term: CII-DT-083 forbids the element, so a box number goes in an address line",
	"poBox":           "EN 16931 has no P.O. box term: CII-DT-083 forbids the element, so a box number goes in an address line",
	"shipTo":          "the address goods are delivered to is address inside delivery (BG-15)",
	"deliveryAddress": "the address goods are delivered to is address inside delivery (BG-15)",
	"iban":            "a bank account belongs to a party, not to the invoice: sellerAccount (BT-84) or buyerAccount (BT-91 under a mandate)",
	"bic":             "a bank account belongs to a party, not to the invoice: sellerAccount (BT-84) or buyerAccount (BT-91 under a mandate)",
	"paymentTerms":    "payment terms are a list of strings under terms (BT-20)",
	"vat":             "the VAT breakdown is a list under vatBreakdown (BG-23)",
}

// modelDecodeError turns the decoder's wording into an error that says what
// happened to an invoice: the JSON carried a key the model has no field for, so
// the value would never have reached the document. Where the standard has a
// term the caller was circling, the error names it, because the refusal on its
// own sends them back to the specification.
func modelDecodeError(err error) error {
	const unknown = `unknown field "`
	msg := err.Error()
	_, after, found := strings.Cut(msg, unknown)
	if !found {
		return fmt.Errorf("einvoice: invoice model: %w", err)
	}
	key, _, _ := strings.Cut(after, `"`)
	hint, ok := modelHints[key]
	if !ok {
		hint = "the model has no such field, so nothing would be rendered from it"
	}
	return fmt.Errorf("einvoice: invoice model: unknown field %q: %s", key, hint)
}

// Finding is one failed Schematron assertion.
type Finding struct {
	// ID is the rule identifier, e.g. "BR-02" or "BR-CO-15".
	ID string `json:"id"`
	// Source names the rule set the assertion belongs to, e.g. "en16931" or
	// "xrechnung". It is empty when only one rule set ran.
	Source string `json:"source,omitempty"`
	// Flag is the Schematron flag: "fatal" for business rule breaches, "warning"
	// and "information" for softer constraints. It is the value the rule set
	// itself declares, so a profile can add its own.
	Flag string `json:"flag,omitempty"`
	// Message is the human-readable description from the rule set.
	Message string `json:"message"`
	// Context is the XPath of the node the rule was applied to.
	Context string `json:"context,omitempty"`
	// Err is non-empty when the validator itself could not evaluate the rule;
	// it says nothing about the document.
	Err string `json:"error,omitempty"`
}

// Result is the outcome of validating one document.
type Result struct {
	// Syntax is the syntax the document was validated against.
	Syntax Syntax `json:"syntax"`
	// CIUS is the extra rule set that ran, empty when EN 16931 alone was
	// checked.
	CIUS CIUS `json:"cius,omitempty"`
	// Profile is the rule-set bundle that ran, empty when this library's own
	// EN 16931 artefacts were used. It matters because a bundle carries its own
	// copy of the norm, so the artefact versions behind the findings differ.
	Profile Profile `json:"profile,omitempty"`
	// Valid is true when the document broke no rule at all, i.e. when Fatal,
	// Warn and Internal are all zero.
	Valid bool `json:"valid"`
	// Findings lists every assertion that failed, in document order. It is an
	// empty slice, never nil, so the JSON shape is the same for valid and
	// invalid documents.
	Findings []Finding `json:"findings"`
	// Fatal counts failed business rules.
	Fatal int `json:"fatal"`
	// Warn counts failed non-fatal rules.
	Warn int `json:"warn"`
	// Internal counts rules the validator could not evaluate, which says
	// nothing about the document but makes the result untrustworthy.
	Internal int `json:"internal"`
	// Schema lists structural problems: an element the schema does not allow
	// in that place, a missing mandatory child, content repeated past its
	// maximum, elements where the schema expects text. These are kept apart
	// from Findings because they are not business rule ids. As with Findings,
	// it is an empty slice rather than nil, so a valid and an invalid document
	// encode to the same set of keys.
	Schema []SchemaFinding `json:"schema"`
}

// SchemaFinding describes one structural problem against the XML schema of the
// syntax. Location is the path to the element whose content is at fault.
type SchemaFinding struct {
	Location string `json:"location"`
	Message  string `json:"message"`
}

// Counts returns how many times each rule fired, keyed by rule identifier.
func (r *Result) Counts() map[string]int {
	m := make(map[string]int, len(r.Findings))
	for _, f := range r.Findings {
		m[f.ID]++
	}
	return m
}

// IDs returns the distinct rule ids that fired, in document order.
func (r *Result) IDs() []string {
	if r == nil {
		return nil
	}
	return r.rep().IDs()
}

func (r *Result) rep() *en16931.Report {
	rep := &en16931.Report{
		Syntax:   en16931.Syntax(r.Syntax),
		CIUS:     en16931.CIUS(r.CIUS),
		Profile:  en16931.Profile(r.Profile),
		Findings: findings(r.Findings),
		Fatal:    r.Fatal,
		Warn:     r.Warn,
		Internal: r.Internal,
	}
	return rep
}

func findings(fs []Finding) []sch.Finding {
	out := make([]sch.Finding, len(fs))
	for i, f := range fs {
		out[i] = sch.Finding{ID: f.ID, Source: f.Source, Flag: f.Flag, Message: f.Message, Context: f.Context, Err: f.Err}
	}
	return out
}

// Detect returns the syntax of an invoice document without validating it.
func Detect(data []byte) (Syntax, error) {
	sx, err := en16931.Detect(data)
	return Syntax(sx), err
}

// Validate checks a document against EN 16931, against a CIUS rule set as well
// when Options.CIUS names one, and against a network's own rule-set bundle when
// Options.Profile names one. An error is returned only when the input is not an
// EN 16931 invoice at all, or when a named CIUS or profile is not one this
// library carries; a document that simply breaks rules comes back as a Result
// full of findings.
func Validate(data []byte, opts Options) (*Result, error) {
	rep, err := en16931.Validate(data, en16931.Options{
		Syntax:  en16931.Syntax(opts.Syntax),
		Phases:  opts.Phases,
		CIUS:    en16931.CIUS(opts.CIUS),
		Profile: en16931.Profile(opts.Profile),
	})
	if err != nil {
		return nil, err
	}
	res := &Result{
		Findings: []Finding{},
		Schema:   []SchemaFinding{},
		Syntax:   Syntax(rep.Syntax),
		CIUS:     CIUS(rep.CIUS),
		Profile:  Profile(rep.Profile),
		Valid:    rep.Valid(),
		Fatal:    rep.Fatal,
		Warn:     rep.Warn,
		Internal: rep.Internal,
	}
	for _, f := range rep.Findings {
		res.Findings = append(res.Findings, Finding{
			ID:      f.ID,
			Source:  f.Source,
			Flag:    f.Flag,
			Message: f.Message,
			Context: f.Context,
			Err:     f.Err,
		})
	}
	for _, f := range rep.Schema {
		res.Schema = append(res.Schema, SchemaFinding{
			Location: f.Path,
			Message:  f.Message,
		})
	}
	return res, nil
}

// ValidateFile checks a document and reports every rule id that fired. It is a
// convenience wrapper for callers that only need the ids.
func ValidateFile(data []byte) ([]string, error) {
	res, err := Validate(data, Options{})
	if err != nil {
		return nil, err
	}
	return res.IDs(), nil
}

// Phases lists the Schematron phase names available for a syntax.
func Phases(sx Syntax) ([]string, error) {
	s, err := en16931.Schematron(en16931.Syntax(sx))
	if err != nil {
		return nil, err
	}
	return s.PhaseIDs(), nil
}

// CiusNames lists the CIUS rule sets this library carries, sorted. Each one can
// be passed as Options.CIUS.
func CiusNames() []string {
	names := en16931.CiusNames()
	out := make([]string, len(names))
	copy(out, names)
	return out
}

// CiusVersion reports which upstream release an embedded CIUS rule set came
// from, and the empty string for a name this library does not carry. The
// version is redistributed unmodified; see NOTICE.md.
func CiusVersion(c CIUS) string { return en16931.CiusVersion(en16931.CIUS(c)) }

// ProfileNames lists the rule-set bundles this library carries, sorted. Each
// one can be passed as Options.Profile.
func ProfileNames() []string {
	names := en16931.ProfileNames()
	out := make([]string, len(names))
	copy(out, names)
	return out
}

// ProfileVersion reports which upstream release an embedded rule-set bundle
// came from, and the empty string for a name this library does not carry. The
// bundle is redistributed unmodified; see NOTICE.md.
func ProfileVersion(p Profile) string { return en16931.ProfileVersion(en16931.Profile(p)) }

// ProfilePhases lists the Schematron phase ids of the EN 16931 artefact inside a
// rule-set bundle, for a syntax. Phases belong to the rule set that declares
// them, so this is separate from Phases(); Options.Phases never restricts the
// network's own artefacts.
func ProfilePhases(p Profile, sx Syntax) ([]string, error) {
	ids, err := en16931.ProfilePhaseIDs(en16931.Profile(p), en16931.Syntax(sx))
	if err != nil {
		return nil, err
	}
	out := make([]string, len(ids))
	copy(out, ids)
	return out, nil
}

// CiusPhases lists the Schematron phase ids of a CIUS rule set for a syntax.
// Phases belong to the rule set that declares them, so this is separate from
// Phases(); Options.Phases never restricts a CIUS.
func CiusPhases(c CIUS, sx Syntax) ([]string, error) {
	ids, err := en16931.CiusPhaseIDs(en16931.CIUS(c), en16931.Syntax(sx))
	if err != nil {
		return nil, err
	}
	out := make([]string, len(ids))
	copy(out, ids)
	return out, nil
}

// Rule is one assertion this library enforces, as published: its id, the rule
// set that states it, what it says is wrong, and the phases that activate it.
// Rules() returns them, which is how a caller publishes or diffs the rule set
// behind a version string instead of trusting a changelog.
type Rule struct {
	ID      string   `json:"id"`
	Syntax  Syntax   `json:"syntax"`
	Source  string   `json:"source"`
	Flag    string   `json:"flag"`
	Text    string   `json:"text"`
	Context string   `json:"context"`
	Test    string   `json:"test"`
	Phases  []string `json:"phases"`
}

// Rules lists every assertion a Validate call with these options would apply to
// a document of the given syntax, in the order the rule sets run and, within
// each, in document order. Options.Syntax is ignored, since the syntax is named
// directly, and Options.Phases is not applied: the list says what a build
// carries, and each rule states the phases it belongs to. A profile replaces the
// EN 16931 artefacts exactly as it does in Validate, so the list matches the
// verdict it produces.
func Rules(sx Syntax, opts Options) ([]Rule, error) {
	list, err := en16931.Rules(en16931.Syntax(sx), en16931.CIUS(opts.CIUS), en16931.Profile(opts.Profile))
	if err != nil {
		return nil, err
	}
	out := make([]Rule, 0, len(list))
	for _, r := range list {
		pr := Rule{
			ID:      r.ID,
			Syntax:  sx,
			Source:  r.Source,
			Flag:    r.Flag,
			Text:    r.Text,
			Context: r.Context,
			Test:    r.Test,
		}
		if len(r.Phases) > 0 {
			pr.Phases = append([]string(nil), r.Phases...)
		}
		out = append(out, pr)
	}
	return out, nil
}
