package einvoice_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dcnws/invoicekit.go"
	"github.com/dcnws/invoicekit.go/facturx"
	"github.com/dcnws/invoicekit.go/hybrid"
)

// The end-to-end tests in this file work with PDFs that another program
// produced. The unit tests build their own documents, which is fine for
// checking one rule at a time but proves nothing about whether a file from a
// real producer - cross-reference streams, compressed object streams, indirect
// stream lengths, a subset font - can be read, wrapped and audited. Fixtures
// under testdata were made with Ghostscript from testdata/invoice-page.ps.

// fixture reads a file from the repository's testdata directory.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return data
}

// modelXML renders the invoice model fixture the way the gen command does, so
// the envelope is tested against a document that is known to validate.
func modelXML(t *testing.T) []byte {
	t.Helper()
	var inv einvoice.Invoice
	if err := json.Unmarshal(fixture(t, "invoice-model.json"), &inv); err != nil {
		t.Fatalf("invoice model: %v", err)
	}
	doc, err := einvoice.Generate(einvoice.SyntaxCII, inv)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	return doc
}

// TestRealPDFCarriesTheInvoice wraps a genuine PDF/A-3b file and reads it back.
// The envelope must add nothing that a validator objects to, and the payload
// must come out byte for byte what went in.
func TestRealPDFCarriesTheInvoice(t *testing.T) {
	xmlDoc := modelXML(t)
	hybrid, err := facturx.Embed(fixture(t, "invoice-pdfa3.pdf"), xmlDoc, facturx.Options{
		Now:      time.Date(2026, 2, 17, 9, 30, 0, 0, time.UTC),
		Metadata: facturx.Metadata{Title: "Invoice WH-2025-0184", Author: "Acme Trading Ltd", Lang: "en-GB"},
	})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	doc, rep, err := facturx.Audit(hybrid)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	for _, f := range rep.Findings {
		if f.Severity == facturx.SeverityError {
			t.Errorf("%s: %s", f.Rule, f.Message)
		}
	}
	if !rep.OK() {
		t.Fatalf("audit failed:\n%v", rep)
	}
	if !bytes.Equal(doc.XML, xmlDoc) {
		t.Errorf("payload changed: %d bytes in, %d bytes out", len(xmlDoc), len(doc.XML))
	}
	if doc.Profile != doc.Declared {
		t.Errorf("envelope declares %s, the XML declares %s", doc.Profile, doc.Declared)
	}
	if doc.Pages != 1 {
		t.Errorf("%d pages, want 1", doc.Pages)
	}
	res, err := einvoice.Validate(doc.XML, einvoice.Options{Syntax: einvoice.SyntaxCII})
	if err != nil {
		t.Fatalf("validate extracted payload: %v", err)
	}
	if !res.Valid {
		t.Errorf("the payload no longer validates: %v", res.Schema)
	}
}

// TestRealUnconformantPDFIsCaught feeds in the same page rendered without any
// PDF/A constraints. Wrapping a file that cannot be PDF/A-3 never makes it one,
// so the audit has to name the defects a recipient's validator will hit.
func TestRealUnconformantPDFIsCaught(t *testing.T) {
	_, rep, err := facturx.Audit(fixture(t, "invoice-plain.pdf"))
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if rep.OK() {
		t.Fatal("an unembedded font and no output intent passed the audit")
	}
	want := map[string]bool{"pdfa.font": false, "pdfa.outputintent": false}
	for _, f := range rep.Findings {
		if f.Severity != facturx.SeverityError {
			continue
		}
		if _, ok := want[f.Rule]; ok {
			want[f.Rule] = true
		}
	}
	for rule, seen := range want {
		if !seen {
			t.Errorf("audit did not report %s:\n%v", rule, rep)
		}
	}
}

// TestVeraPDFAcceptsTheHybrid puts the wrapped invoice in front of veraPDF, the
// reference PDF/A validator, when one is installed. It is a JVM application that
// most build machines do not have, so the test skips rather than reporting a
// pass it did not earn. Point VERAPDF at the command or the jar to override the
// search.
func TestVeraPDFAcceptsTheHybrid(t *testing.T) {
	name, args := veraPDF()
	if name == "" {
		t.Skip("veraPDF is not installed")
	}
	hybrid, err := facturx.Embed(fixture(t, "invoice-pdfa3.pdf"), modelXML(t), facturx.Options{
		Now: time.Date(2026, 2, 17, 9, 30, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	path := filepath.Join(t.TempDir(), "invoice.pdf")
	if err := os.WriteFile(path, hybrid, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// One line per document: "PASS <file> <flavour>" or "FAIL <file>
	// <flavour>", with a zero exit status only when every document passed.
	out, err := exec.Command(name, append(args, path)...).CombinedOutput()
	text := strings.TrimSpace(string(out))
	t.Logf("veraPDF:\n%s", text)
	if err != nil || !strings.Contains(text, "PASS") {
		t.Errorf("veraPDF did not accept the document: %q (exit %v)", text, err)
	}
}

// veraPDF locates the validator: $VERAPDF (command or jar), then verapdf on
// PATH, then a jar in the working directory or the usual offline install under
// /opt/verapdf. An empty name means it is not here.
func veraPDF() (string, []string) {
	if v := os.Getenv("VERAPDF"); v != "" {
		if strings.HasSuffix(v, ".jar") {
			return "java", []string{"-jar", v, "--format", "text"}
		}
		return v, []string{"--format", "text"}
	}
	if p, err := exec.LookPath("verapdf"); err == nil {
		return p, []string{"--format", "text"}
	}
	for _, pattern := range []string{"verapdf*.jar", "/opt/verapdf/bin/cli-*.jar"} {
		if jars, _ := filepath.Glob(pattern); len(jars) > 0 {
			return "java", []string{"-jar", jars[0], "--format", "text"}
		}
	}
	return "", nil
}

// --- the invoice inside, checked against a rule set -------------------------

// hybridPDF wraps a payload in the PDF/A-3 fixture the way the embed command
// does, with a fixed timestamp so the same input always gives the same file.
func hybridPDF(t *testing.T, xmlDoc []byte, opts facturx.Options) []byte {
	t.Helper()
	opts.Now = time.Date(2026, 2, 17, 9, 30, 0, 0, time.UTC)
	hybrid, err := facturx.Embed(fixture(t, "invoice-pdfa3.pdf"), xmlDoc, opts)
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	return hybrid
}

// fired is the set of business-rule ids in a rules block, for tests that care
// which rules ran rather than how many times.
func fired(res *einvoice.Result) map[string]bool {
	out := make(map[string]bool, len(res.Findings))
	for _, f := range res.Findings {
		out[f.ID] = true
	}
	return out
}

// TestHybridChecksTheInvoiceInside is the round trip a recipient wants: one call
// on one file answers both questions - is the envelope acceptable, and does the
// invoice inside break any rule.
func TestHybridChecksTheInvoiceInside(t *testing.T) {
	res, err := hybrid.Check(hybridPDF(t, modelXML(t), facturx.Options{}), einvoice.Options{})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !res.OK {
		t.Errorf("the envelope should be clean, got %d finding(s): %v", len(res.Findings), res.Findings)
	}
	if res.Rules == nil {
		t.Fatal("no rules block: the invoice inside was never validated")
	}
	if res.Rules.Syntax != einvoice.SyntaxCII {
		t.Errorf("the payload was generated as CII, validated as %s", res.Rules.Syntax)
	}
	if !res.Rules.Valid {
		t.Errorf("the model invoice is a valid EN 16931 invoice, so the copy inside should be too: %v", res.Rules.Findings)
	}
}

// TestHybridRuleSetReachesTheInvoiceInside checks that the rule set a caller
// picks applies to the XML carried in the PDF, not only to loose XML files. The
// model invoice is a Factur-X document rather than an XRechnung, so the German
// rules must reject it even though the norm alone accepts it.
func TestHybridRuleSetReachesTheInvoiceInside(t *testing.T) {
	pdf := hybridPDF(t, modelXML(t), facturx.Options{})
	res, err := hybrid.Check(pdf, einvoice.Options{CIUS: einvoice.CIUSXRechnung})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if res.Rules == nil {
		t.Fatal("no rules block: the invoice inside was never validated")
	}
	if res.Rules.CIUS != einvoice.CIUSXRechnung {
		t.Errorf("the result should say which rule set ran, got %q", res.Rules.CIUS)
	}
	// The model invoice meets the norm but is a British B2B invoice: what
	// XRechnung wants on top is a buyer reference (BT-10), which it has never
	// carried, so that is the rule that proves the national pass ran.
	ids := fired(res.Rules)
	if !ids["BR-DE-15"] {
		t.Errorf("XRechnung rule BR-DE-15 did not fire, so the national rules did not run over the payload: %v", res.Rules.Findings)
	}
	if len(res.Rules.Findings) == 0 {
		t.Error("a rule set that adds requirements should add findings")
	}
	if !res.OK {
		t.Errorf("the envelope verdict must not move when a different rule set is chosen: %v", res.Findings)
	}
}

// TestHybridKeepsTheTwoVerdictsApart is the reason the envelope answer and the
// business-rule answer are separate fields: a document can be a perfectly
// formed Factur-X file carrying an arithmetically impossible invoice, and a
// recipient needs to be told which one is wrong.
func TestHybridKeepsTheTwoVerdictsApart(t *testing.T) {
	xmlDoc := modelXML(t)
	bad := bytes.Replace(xmlDoc,
		[]byte(`TaxTotalAmount currencyID="EUR">115.97`),
		[]byte(`TaxTotalAmount currencyID="EUR">100.00`), 1)
	if bytes.Equal(bad, xmlDoc) {
		t.Fatal("the model invoice no longer carries the tax total this test mutates")
	}
	pdf := hybridPDF(t, bad, facturx.Options{})

	for _, opt := range []einvoice.Options{{}, {CIUS: einvoice.CIUSXRechnung}} {
		res, err := hybrid.Check(pdf, opt)
		if err != nil {
			t.Fatalf("check (%v): %v", opt.CIUS, err)
		}
		if !res.OK {
			t.Errorf("the envelope is fine whatever the totals say: %v", res.Findings)
		}
		if res.Rules == nil || res.Rules.Valid {
			t.Errorf("%s: the broken total should make the rules block invalid", opt.CIUS)
			continue
		}
		ids := fired(res.Rules)
		for _, id := range []string{"BR-CO-14", "BR-CO-15"} {
			if !ids[id] {
				t.Errorf("%s: rule %s did not fire over the payload inside the PDF", opt.CIUS, id)
			}
		}
	}
}

// TestHybridHasNoOpinionOnRulesWithoutAnInvoice: a plain PDF still gets an
// envelope verdict, and no rules block, because there is nothing to validate.
func TestHybridHasNoOpinionOnRulesWithoutAnInvoice(t *testing.T) {
	res, err := hybrid.Check(fixture(t, "invoice-plain.pdf"), einvoice.Options{})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if res.Rules != nil {
		t.Errorf("a PDF carrying no invoice XML cannot have rule findings, got %v", res.Rules.Findings)
	}
	if res.OK {
		t.Error("a PDF with no invoice inside is not an acceptable Factur-X document")
	}
}

// TestHybridReportsAnUnreadablePayload keeps "the rule engine could not run"
// apart from "the rule engine found faults": a payload that will not parse is a
// warning beside the envelope findings, and no rules block, rather than a long
// list of rules that never had a chance to pass.
func TestHybridReportsAnUnreadablePayload(t *testing.T) {
	xmlDoc := modelXML(t)
	broken := xmlDoc[:len(xmlDoc)/2]
	pdf := hybridPDF(t, broken, facturx.Options{
		Profile: facturx.Profile{Flavor: facturx.FacturX, Level: facturx.EN16931},
	})
	res, err := hybrid.Check(pdf, einvoice.Options{})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if res.Rules != nil {
		t.Errorf("a payload that will not parse cannot produce rule findings, got %v", res.Rules.Findings)
	}
	var said bool
	for _, f := range res.Findings {
		if f.Rule == "fx.rules" {
			said = true
			if f.Severity == facturx.SeverityError {
				t.Errorf("an unreadable payload is not by itself grounds for rejecting the envelope: %v", f)
			}
		}
	}
	if !said {
		t.Errorf("nothing said about the invoice inside being unreadable: %v", res.Findings)
	}
}

// TestHybridRefusesARuleSetItDoesNotCarry keeps a misspelt rule set apart from a
// defect in the document: it is the caller's mistake, and it has to arrive as an
// error rather than as a finding about an invoice that was never at fault.
func TestHybridRefusesARuleSetItDoesNotCarry(t *testing.T) {
	pdf := hybridPDF(t, modelXML(t), facturx.Options{})
	_, err := hybrid.Check(pdf, einvoice.Options{CIUS: "xcyphexrechnung"})
	if err == nil {
		t.Fatal("an unknown rule set was accepted")
	}
	if !strings.Contains(err.Error(), "xrechnung") {
		t.Errorf("the error should say what is available: %v", err)
	}
}
