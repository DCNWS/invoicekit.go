package hybrid

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/dcnws/invoicekit.go"
	"github.com/dcnws/invoicekit.go/facturx"
	"github.com/dcnws/invoicekit.go/internal/pdf"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("../testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

// TestCheckReportsBothHalves is the reason this package exists: one pass over a
// hybrid PDF has to say something about the envelope and something about the
// invoice inside it, and the two verdicts must not be conflated.
func TestCheckReportsBothHalves(t *testing.T) {
	res, err := Check(fixture(t, "hybrid-a3.pdf"), einvoice.Options{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("envelope error = %q", res.Error)
	}
	if !res.OK {
		t.Errorf("envelope verdict = false, findings %v", ruleIDs(res.Findings))
	}
	if res.Envelope != "Factur-X EN 16931" || res.Declared != "Factur-X EN 16931" {
		t.Errorf("envelope %q, declared %q", res.Envelope, res.Declared)
	}
	if res.Payload != "factur-x.xml" {
		t.Errorf("payload = %q", res.Payload)
	}
	if res.Pages != 1 {
		t.Errorf("pages = %d", res.Pages)
	}
	if res.Rules == nil {
		t.Fatal("no rules verdict for a document that carries an invoice")
	}
	if res.Rules.Syntax != einvoice.SyntaxCII {
		t.Errorf("rules syntax = %q, want CII", res.Rules.Syntax)
	}
	if !res.Rules.Valid {
		t.Errorf("rules verdict = %+v, want valid", res.Rules)
	}
}

// TestEnvelopeAndRulesAreIndependent covers the case the doc comment promises:
// an invoice that passes every business rule inside an envelope a validator
// rejects. If one verdict moved with the other, hybrid checking would tell a
// recipient nothing about which half to fix.
func TestEnvelopeAndRulesAreIndependent(t *testing.T) {
	res, err := Check(fixture(t, "hybrid-plain.pdf"), einvoice.Options{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.OK {
		t.Error("this fixture's envelope is meant to fail")
	}
	if !hasFinding(res.Findings, "pdfa.font") {
		t.Errorf("findings %v, want the font embedding failure", ruleIDs(res.Findings))
	}
	if res.Rules == nil {
		t.Fatal("the invoice inside should still have been validated")
	}
	if !res.Rules.Valid {
		t.Errorf("rules = %+v, want valid inside a failing envelope", res.Rules)
	}
}

// TestRuleSetSelectionIsApplied checks the options reach the engine that runs
// on the payload, without disturbing the envelope verdict.
func TestRuleSetSelectionIsApplied(t *testing.T) {
	data := fixture(t, "hybrid-a3.pdf")
	plain, err := Check(data, einvoice.Options{})
	if err != nil {
		t.Fatal(err)
	}
	withCIUS, err := Check(data, einvoice.Options{CIUS: einvoice.CIUSXRechnung})
	if err != nil {
		t.Fatal(err)
	}
	if withCIUS.Rules == nil || withCIUS.Rules.CIUS != einvoice.CIUSXRechnung {
		t.Fatalf("rules after a CIUS request = %+v", withCIUS.Rules)
	}
	if withCIUS.Rules.Findings == nil {
		t.Error("Findings must never be nil in a rules verdict")
	}
	withProfile, err := Check(data, einvoice.Options{Profile: einvoice.ProfilePeppol})
	if err != nil {
		t.Fatal(err)
	}
	if withProfile.Rules == nil || withProfile.Rules.Profile != einvoice.ProfilePeppol {
		t.Fatalf("rules after a bundle request = %+v", withProfile.Rules)
	}
	if !plain.OK || !withCIUS.OK || !withProfile.OK {
		t.Error("the envelope verdict moved with the rule set")
	}
}

// TestPlainPDFGetsNoRulesOpinion: a PDF with no invoice in it gets an envelope
// verdict and silence about rules, and the wire form drops the block entirely
// rather than emitting a null a caller has to guard.
func TestPlainPDFGetsNoRulesOpinion(t *testing.T) {
	f := pdf.New("1.7")
	page := pdf.NewDict()
	page.Set("Type", pdf.Name("Page"))
	f.Add(page)
	data, err := f.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	res, err := Check(data, einvoice.Options{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.Rules != nil {
		t.Errorf("rules = %+v, want none for a document with no payload", res.Rules)
	}
	if !hasFinding(res.Findings, "fx.payload") {
		t.Errorf("findings %v, want the missing-payload failure", ruleIDs(res.Findings))
	}
	if res.OK {
		t.Error("a bare PDF must not pass as a hybrid document")
	}
	doc, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(doc), `"rules"`) {
		t.Errorf("JSON keeps an empty rules block: %s", doc)
	}
}

// TestUnreadablePayloadBecomesAnEnvelopeFinding: the rules never ran, and the
// honest answer is one envelope finding rather than a fabricated report.
func TestUnreadablePayloadBecomesAnEnvelopeFinding(t *testing.T) {
	rep := &facturx.Report{}
	rules := rulesInside(rep, []byte("<this is not an invoice"), einvoice.Options{})
	if rules != nil {
		t.Errorf("rules = %+v, want nil when the payload could not be read", rules)
	}
	if len(rep.Findings) != 1 {
		t.Fatalf("findings = %v, want exactly one", ruleIDs(rep.Findings))
	}
	f := rep.Findings[0]
	if f.Rule != "fx.rules" || f.Severity != facturx.SeverityWarning {
		t.Errorf("finding = %+v, want one fx.rules warning", f)
	}
	if !strings.Contains(f.Message, "no business rules ran") {
		t.Errorf("message = %q, want it to say the rules did not run", f.Message)
	}

	// No payload at all is not a fault to report twice: the envelope check
	// already says the PDF carries no invoice.
	quiet := &facturx.Report{}
	if got := rulesInside(quiet, nil, einvoice.Options{}); got != nil {
		t.Error("an absent payload should produce no rules verdict")
	}
	if len(quiet.Findings) != 0 {
		t.Errorf("findings = %v, want none", ruleIDs(quiet.Findings))
	}
}

// TestOptionsAreSettledBeforeTheDocumentIsRead: a misspelt rule set is the
// caller's mistake, and it has to be reported as such even for bytes that
// could not be parsed at all.
func TestOptionsAreSettledBeforeTheDocumentIsRead(t *testing.T) {
	if err := CheckOptions(einvoice.Options{CIUS: einvoice.CIUS("nope")}); err == nil || !strings.Contains(err.Error(), "unknown CIUS") {
		t.Errorf("CheckOptions(bad CIUS) = %v", err)
	}
	if err := CheckOptions(einvoice.Options{Profile: einvoice.Profile("nope")}); err == nil || !strings.Contains(err.Error(), "unknown rule-set bundle") {
		t.Errorf("CheckOptions(bad bundle) = %v", err)
	}
	if err := CheckOptions(einvoice.Options{}); err != nil {
		t.Errorf("CheckOptions(default) = %v", err)
	}
	_, err := Check([]byte("not a pdf"), einvoice.Options{CIUS: einvoice.CIUS("nope")})
	if err == nil || !strings.Contains(err.Error(), "unknown CIUS") {
		t.Errorf("Check(bad bytes, bad CIUS) = %v, want the option error to win", err)
	}
	if _, err := Check([]byte("not a pdf"), einvoice.Options{}); err == nil || !strings.Contains(err.Error(), "%PDF-") {
		t.Errorf("Check(bad bytes) = %v, want the parser's complaint", err)
	}
}

func ruleIDs(fs []facturx.Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Rule)
	}
	return out
}

func hasFinding(fs []facturx.Finding, rule string) bool {
	for _, f := range fs {
		if f.Rule == rule {
			return true
		}
	}
	return false
}
