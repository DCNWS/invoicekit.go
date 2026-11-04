package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	einvoice "github.com/dcnws/invoicekit.go"
	"github.com/dcnws/invoicekit.go/hybrid"
)

// Fixtures the commands are driven with. They are the same files the README
// and the CI pipeline use, so a command that works in a test works in the field.
const (
	ciiSample = "../../testdata/samples/cii-xrechnung.xml"
	ublSample = "../../testdata/samples/ubl-peppol.xml"
	modelJSON = "../../testdata/invoice-model.json"
	hybridA3  = "../../testdata/hybrid-a3.pdf"
	hybridOld = "../../testdata/hybrid-plain.pdf"
	pdfa3PDF  = "../../testdata/invoice-pdfa3.pdf"
	plainPDF  = "../../testdata/invoice-plain.pdf"
)

// runCmd calls a command exactly as main does, with the output stream and the
// exit call redirected, and reports what the command wrote to stdout, the
// status it asked to exit with (-1 when it returned normally) and the error it
// returned. Recording the status instead of stopping the process is faithful:
// every exit call in a command is the last thing it does.
func runCmd(t *testing.T, cmd func([]string) error, args ...string) (string, int, error) {
	t.Helper()
	buf, code := seams(t)
	err := cmd(args)
	return buf.String(), *code, err
}

// seams redirects the output stream and the exit call for one test, and hands
// back what was written and the status that was asked for (-1 for none).
func seams(t *testing.T) (*strings.Builder, *int) {
	t.Helper()
	previous, previousExit := stdout, exit
	t.Cleanup(func() { stdout, exit = previous, previousExit })
	var buf strings.Builder
	code := -1
	stdout, exit = &buf, func(c int) { code = c }
	return &buf, &code
}

// stdinWith points the process's standard input at a file for one test, the
// way a pipe does in the shell.
func stdinWith(t *testing.T, path string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	previous := os.Stdin
	t.Cleanup(func() {
		os.Stdin = previous
		_ = f.Close()
	})
	os.Stdin = f
}

func TestRunValidateReportsAndSetsTheStatus(t *testing.T) {
	out, code, err := runCmd(t, runValidate, ciiSample)
	if err != nil {
		t.Fatalf("runValidate: %v", err)
	}
	if code != -1 {
		t.Errorf("a valid document exited with %d, want no exit call", code)
	}
	if !strings.Contains(out, "valid CII document") {
		t.Errorf("output was %q, want a valid verdict", out)
	}

	// The same file against a CIUS is a different verdict, and the status is
	// what a build script reads.
	out, code, err = runCmd(t, runValidate, "-cius", "xrechnung", ciiSample)
	if err != nil {
		t.Fatalf("runValidate -cius: %v", err)
	}
	if code != 1 {
		t.Errorf("an invalid document exited with %d, want 1", code)
	}
	if !strings.Contains(out, "INVALID") || !strings.Contains(out, "BR-DE-21") {
		t.Errorf("output was %q, want the breaches listed", out)
	}

	// The machine-readable form has to be parseable and carry the same verdict.
	out, code, err = runCmd(t, runValidate, "-json", "-max", "1", "-cius", "xrechnung", ciiSample)
	if err != nil || code != 1 {
		t.Fatalf("runValidate -json: err %v, exit %d", err, code)
	}
	var vs []verdict
	if err := json.Unmarshal([]byte(out), &vs); err != nil {
		t.Fatalf("the -json output does not parse: %v\n%s", err, out)
	}
	if len(vs) != 1 || vs[0].Valid || vs[0].Truncated != 2 || len(vs[0].Findings) != 1 {
		t.Errorf("verdicts were %+v, want one invalid verdict truncated to 1 finding", vs[0])
	}
	if vs[0].Name != ciiSample || vs[0].CIUS != einvoice.CIUSXRechnung {
		t.Errorf("verdict names %q / %q, want the file and the CIUS it was checked against",
			vs[0].Name, vs[0].CIUS)
	}
}

// A file that cannot be read is a verdict of its own, and the only sensible
// status for a batch of nothing but unreadable input.
// The text form has to carry the rule set it was checked against and say what
// it left out, or a support ticket cannot be reproduced.
func TestRunValidateTextNamesTheRuleSetAndTruncates(t *testing.T) {
	out, code, err := runCmd(t, runValidate, "-profile", "peppol", ublSample)
	if err != nil || code != -1 {
		t.Fatalf("runValidate -profile: err %v, exit %d", err, code)
	}
	if !strings.Contains(out, "peppol") {
		t.Errorf("verdict for a bundle does not name it:\n%s", out)
	}

	out, code, err = runCmd(t, runValidate, "-max", "1", "-cius", "xrechnung", ciiSample)
	if err != nil || code != 1 {
		t.Fatalf("runValidate -max: err %v, exit %d", err, code)
	}
	if !strings.Contains(out, "... and 2 more") {
		t.Errorf("output did not say what was left out:\n%s", out)
	}
}

// A file that reads but is not an invoice is the engine's problem to report,
// and it belongs inside the verdict like any other.
func TestRunValidateOfSomethingThatIsNotAnInvoice(t *testing.T) {
	out, code, err := runCmd(t, runValidate, "-syntax", "ubl", "main.go")
	if err != nil {
		t.Fatalf("runValidate: %v", err)
	}
	if code != 2 || !strings.Contains(out, "ERROR:") {
		t.Errorf("a Go file gave exit %d and output %q", code, out)
	}
	if _, code, err := runCmd(t, runValidate, "-syntax", "ubl", ublSample); err != nil || code != -1 {
		t.Fatalf("validate -syntax ubl: err %v, exit %d", err, code)
	}
}

func TestRunValidateUnreadableInput(t *testing.T) {
	out, code, err := runCmd(t, runValidate, "../../testdata/nope.xml")
	if err != nil {
		t.Fatalf("runValidate: %v", err)
	}
	if code != 2 {
		t.Errorf("unreadable input exited with %d, want 2", code)
	}
	if !strings.Contains(out, "ERROR:") {
		t.Errorf("output was %q, want an error line", out)
	}
}

func TestRunValidateReadsStdin(t *testing.T) {
	stdinWith(t, ciiSample)
	out, code, err := runCmd(t, runValidate, "-")
	if err != nil || code != -1 {
		t.Fatalf("runValidate -: err %v, exit %d", err, code)
	}
	if !strings.Contains(out, "-: valid CII document") {
		t.Errorf("output was %q, want the verdict for stdin", out)
	}
}

func TestRunValidateNeedsInput(t *testing.T) {
	if _, _, err := runCmd(t, runValidate); err == nil {
		t.Fatal("validate with no input returned no error")
	}
	if _, _, err := runCmd(t, runValidate, "-syntax", "zork"); err == nil {
		t.Fatal("validate with an unknown syntax returned no error")
	}
	if _, _, err := runCmd(t, runValidate, "-cius", "zork", ciiSample); err == nil {
		t.Fatal("validate with an unknown CIUS returned no error")
	}
}

func TestRunGenWritesTheDocument(t *testing.T) {
	out, code, err := runCmd(t, runGen, "-syntax", "cii", modelJSON)
	if err != nil || code != -1 {
		t.Fatalf("runGen: err %v, exit %d", err, code)
	}
	if !strings.Contains(out, "CrossIndustryInvoice") {
		t.Errorf("generated document was not CII: %.120s", out)
	}

	path := filepath.Join(t.TempDir(), "out.xml")
	out, code, err = runCmd(t, runGen, "-syntax", "ubl", "-o", path, modelJSON)
	if err != nil || code != -1 {
		t.Fatalf("runGen -o: err %v, exit %d", err, code)
	}
	if out != "" {
		t.Errorf("with -o the document still went to stdout: %.80s", out)
	}
	doc, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(doc), "<Invoice") {
		t.Errorf("generated document was not UBL: %.120s", doc)
	}
}

// gen validates what it wrote, because a generator that produces a document its
// own engine rejects is worse than no generator at all.
func TestGenRefusesADocumentItCannotVet(t *testing.T) {
	// A seller with no tax identifier generates cleanly and fails the rules on
	// the way out, which is exactly the case gen has to refuse.
	path := filepath.Join(t.TempDir(), "model.json")
	data, err := os.ReadFile(modelJSON)
	if err != nil {
		t.Fatal(err)
	}
	var model map[string]any
	if err := json.Unmarshal(data, &model); err != nil {
		t.Fatal(err)
	}
	seller, _ := model["seller"].(map[string]any)
	delete(seller, "taxIds")
	bad, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bad, 0o644); err != nil {
		t.Fatal(err)
	}

	out, code, err := runCmd(t, runGen, "-syntax", "ubl", path)
	if err != nil {
		t.Fatalf("runGen on the broken model: %v", err)
	}
	if code != 1 {
		t.Errorf("gen of an unusable document exited with %d, want 1", code)
	}
	if !strings.Contains(out, "<Invoice") {
		t.Errorf("gen wrote no document: %.80s", out)
	}

	// -no-validate is how a caller says the vetting is someone else's job.
	if _, code, err = runCmd(t, runGen, "-syntax", "ubl", "-no-validate", path); err != nil || code != -1 {
		t.Fatalf("runGen -no-validate: err %v, exit %d", err, code)
	}
}

func TestRunGenArgumentErrors(t *testing.T) {
	if _, _, err := runCmd(t, runGen); err == nil {
		t.Fatal("gen with no model returned no error")
	}
	if _, _, err := runCmd(t, runGen, modelJSON, modelJSON); err == nil {
		t.Fatal("gen with two models returned no error")
	}
	if _, _, err := runCmd(t, runGen, "-syntax", "zork", modelJSON); err == nil {
		t.Fatal("gen with an unknown syntax returned no error")
	}
	if _, _, err := runCmd(t, runGen, "-profile", "zork", modelJSON); err == nil {
		t.Fatal("gen with an unknown bundle returned no error")
	}
	if _, _, err := runCmd(t, runGen, "../../testdata/nope.json"); err == nil {
		t.Fatal("gen on a missing model returned no error")
	}
}

func TestRunParseRoundTripsADocument(t *testing.T) {
	out, code, err := runCmd(t, runParse, ciiSample)
	if err != nil || code != -1 {
		t.Fatalf("runParse: err %v, exit %d", err, code)
	}
	var inv einvoice.Invoice
	if err := json.Unmarshal([]byte(out), &inv); err != nil {
		t.Fatalf("the model does not parse: %v\n%.200s", err, out)
	}
	if inv.ID == "" || inv.Seller.Name == "" || len(inv.Lines) == 0 {
		t.Errorf("model lost something on the way in: id %q, seller %q, %d lines",
			inv.ID, inv.Seller.Name, len(inv.Lines))
	}
	if !strings.Contains(inv.Guideline, "en16931") {
		t.Errorf("guideline is %q, want the context the document declares", inv.Guideline)
	}

	path := filepath.Join(t.TempDir(), "model.json")
	if _, code, err = runCmd(t, runParse, "-o", path, ublSample); err != nil || code != -1 {
		t.Fatalf("runParse -o: err %v, exit %d", err, code)
	}
	if _, err := os.ReadFile(path); err != nil {
		t.Errorf("read %s: %v", path, err)
	}
}

func TestRunParseReadsStdin(t *testing.T) {
	stdinWith(t, ublSample)
	out, code, err := runCmd(t, runParse, "-")
	if err != nil || code != -1 {
		t.Fatalf("runParse -: err %v, exit %d", err, code)
	}
	var inv einvoice.Invoice
	if err := json.Unmarshal([]byte(out), &inv); err != nil || inv.ID == "" {
		t.Errorf("stdin did not become a model: %v, %.80s", err, out)
	}
}

func TestRunParseAndDocKindErrors(t *testing.T) {
	if _, _, err := runCmd(t, runParse); err == nil {
		t.Fatal("parse with no document returned no error")
	}
	if _, _, err := runCmd(t, runParse, "main.go"); err == nil {
		t.Fatal("parse on a Go file returned no error")
	}
	if got := docKind(einvoice.Invoice{TypeCode: "381"}); got != "credit note" {
		t.Errorf("docKind(381) = %q", got)
	}
	if got := docKind(einvoice.Invoice{TypeCode: "380"}); got != "invoice" {
		t.Errorf("docKind(380) = %q", got)
	}
}

func TestRunDetectNamesTheSyntax(t *testing.T) {
	for path, want := range map[string]string{ciiSample: "CII", ublSample: "UBL"} {
		out, code, err := runCmd(t, runDetect, path)
		if err != nil || code != -1 {
			t.Fatalf("runDetect %s: err %v, exit %d", path, err, code)
		}
		if !strings.HasSuffix(out, want+"\n") {
			t.Errorf("detect %s said %q, want %s", path, out, want)
		}
	}
	if _, _, err := runCmd(t, runDetect); err == nil {
		t.Fatal("detect with no file returned no error")
	}
	if _, _, err := runCmd(t, runDetect, "main.go"); err == nil {
		t.Fatal("detect on a Go file returned no error")
	}
	if _, _, err := runCmd(t, runDetect, "../../testdata/nope.xml"); err == nil {
		t.Fatal("detect on a missing file returned no error")
	}
}

func TestRunPhasesListsAPhaseSet(t *testing.T) {
	out, code, err := runCmd(t, runPhases)
	if err != nil || code != -1 {
		t.Fatalf("runPhases: err %v, exit %d", err, code)
	}
	for _, want := range []string{"EN16931-model-phase", "codelist_phase"} {
		if !strings.Contains(out, want) {
			t.Errorf("phases output does not mention %s:\n%s", want, out)
		}
	}
	if out, _, err = runCmd(t, runPhases, "ubl"); err != nil || !strings.HasPrefix(out, "UBL") {
		t.Fatalf("runPhases ubl: err %v, out %q", err, out)
	}
	if _, _, err := runCmd(t, runPhases, "zork"); err == nil {
		t.Fatal("phases with an unknown syntax returned no error")
	}
}

func TestRunRulesFiltersBySyntaxAndPhase(t *testing.T) {
	out, code, err := runCmd(t, runRules, "-syntax", "cii", "-phase", "codelist_phase")
	if err != nil || code != -1 {
		t.Fatalf("runRules: err %v, exit %d", err, code)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 10 {
		t.Fatalf("only %d cii codelist rules listed", len(lines))
	}
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if fields[0] != "CII" || !strings.Contains(fields[3], "codelist_phase") {
			t.Errorf("line %q survived a filter it should not have", line)
		}
	}

	out, code, err = runCmd(t, runRules, "-syntax", "ubl", "-json", "-cius", "xrechnung")
	if err != nil || code != -1 {
		t.Fatalf("runRules -json: err %v, exit %d", err, code)
	}
	var rules []einvoice.Rule
	if err := json.Unmarshal([]byte(out), &rules); err != nil {
		t.Fatalf("the rule list does not parse: %v", err)
	}
	var found bool
	for _, r := range rules {
		if r.Syntax != einvoice.SyntaxUBL {
			t.Errorf("rule %s has syntax %s in a ubl listing", r.ID, r.Syntax)
		}
		if strings.HasPrefix(r.ID, "BR-DE-") {
			found = true
		}
	}
	if !found {
		t.Error("a ubl listing with -cius xrechnung lists no BR-DE rule")
	}

	if _, _, err := runCmd(t, runRules, "-profile", "zork"); err == nil {
		t.Fatal("rules with an unknown bundle returned no error")
	}
	if _, _, err := runCmd(t, runRules, "-phase", "nope"); err != nil {
		t.Fatalf("rules with an unknown phase should list nothing, not fail: %v", err)
	}
	if _, _, err := runCmd(t, runRules, "-syntax", "zork"); err == nil {
		t.Fatal("rules with an unknown syntax returned no error")
	}
}

// The listing commands are what validate's flags are chosen from, so an
// unrecognised name has to say what was available.
func TestRunCiusAndProfileListAndDescribe(t *testing.T) {
	out, code, err := runCmd(t, runCius)
	if err != nil || code != -1 {
		t.Fatalf("runCius: err %v, exit %d", err, code)
	}
	if !strings.Contains(out, "xrechnung\t") {
		t.Errorf("cius listing was:\n%s", out)
	}
	out, code, err = runCmd(t, runCius, "XRechnung")
	if err != nil || code != -1 {
		t.Fatalf("runCius xrechnung: err %v, exit %d", err, code)
	}
	if !strings.Contains(out, "xrechnung (") || !strings.Contains(out, "CII") {
		t.Errorf("cius detail was:\n%s", out)
	}
	if _, _, err := runCmd(t, runCius, "zork"); err == nil || !strings.Contains(err.Error(), "available") {
		t.Fatalf("runCius zork: %v, want an error naming the options", err)
	}

	out, code, err = runCmd(t, runProfile)
	if err != nil || code != -1 {
		t.Fatalf("runProfile: err %v, exit %d", err, code)
	}
	if !strings.Contains(out, "peppol\t") {
		t.Errorf("profile listing was:\n%s", out)
	}
	out, code, err = runCmd(t, runProfile, "Peppol")
	if err != nil || code != -1 {
		t.Fatalf("runProfile peppol: err %v, exit %d", err, code)
	}
	if !strings.Contains(out, "peppol (") || !strings.Contains(out, "UBL") {
		t.Errorf("bundle detail was:\n%s", out)
	}
	if _, _, err := runCmd(t, runProfile, "zork"); err == nil {
		t.Fatal("profile with an unknown bundle returned no error")
	}
}

func TestRunArtefactsReportsTheRuleSets(t *testing.T) {
	out, code, err := runCmd(t, runArtefacts, "-json", "-cius", "xrechnung", "-profile", "peppol")
	if err != nil || code != -1 {
		t.Fatalf("runArtefacts: err %v, exit %d", err, code)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("the artefact report does not parse: %v\n%s", err, out)
	}
	if _, _, err := runCmd(t, runArtefacts, "-cius", "zork"); err == nil {
		t.Fatal("artefacts with an unknown CIUS returned no error")
	}
	// artefacts parses its flags with ContinueOnError, so main prints the
	// message rather than the flag package exiting under us.
	if _, _, err := runCmd(t, runArtefacts, "-zork"); err == nil {
		t.Fatal("artefacts with an unknown flag returned no error")
	}
}

func TestRunEmbedWrapsAndAudits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hybrid.pdf")
	out, code, err := runCmd(t, runEmbed, "-title", "Invoice", "-author", "W", "-subject", "S",
		"-keywords", "a,b", "-creator", "einv", "-lang", "en-GB",
		"-now", "2026-01-02T03:04:05Z", "-o", path, pdfa3PDF, ciiSample)
	if err != nil || code != -1 {
		t.Fatalf("runEmbed: err %v, exit %d, out %q", err, code, out)
	}
	doc, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.HasPrefix(string(doc), "%PDF") {
		t.Errorf("%s is not a PDF: %.40s", path, doc)
	}

	// A PDF whose fonts are not embedded cannot be made PDF/A-3 by wrapping it,
	// and saying so is the whole point of auditing.
	if _, code, err = runCmd(t, runEmbed, "-o", outPath(t), plainPDF, ciiSample); err != nil {
		t.Fatalf("runEmbed on a plain PDF: %v", err)
	} else if code != 1 {
		t.Errorf("embedding a plain PDF exited with %d, want 1", code)
	}

	// -force is how a caller says "I know, write it anyway".
	force := filepath.Join(t.TempDir(), "forced.pdf")
	if _, code, err = runCmd(t, runEmbed, "-force", "-o", force, plainPDF, ciiSample); err != nil || code != -1 {
		t.Fatalf("runEmbed -force: err %v, exit %d", err, code)
	}

	// With no -o the PDF goes to stdout, which is how it is piped.
	out, code, err = runCmd(t, runEmbed, pdfa3PDF, ciiSample)
	if err != nil || code != -1 {
		t.Fatalf("runEmbed to stdout: err %v, exit %d", err, code)
	}
	if !strings.HasPrefix(out, "%PDF") {
		t.Errorf("stdout did not get a PDF: %.40s", out)
	}
}

// outPath is a throwaway output path in a test's temporary directory.
func outPath(t *testing.T) string {
	return filepath.Join(t.TempDir(), "wrapped.pdf")
}

func TestRunEmbedArgumentErrors(t *testing.T) {
	if _, _, err := runCmd(t, runEmbed, pdfa3PDF); err == nil {
		t.Fatal("embed with one file returned no error")
	}
	if _, _, err := runCmd(t, runEmbed, "-now", "yesterday", pdfa3PDF, ciiSample); err == nil {
		t.Fatal("embed with a nonsense time returned no error")
	}
	if _, _, err := runCmd(t, runEmbed, "-profile", "zork", pdfa3PDF, ciiSample); err == nil {
		t.Fatal("embed with an unknown profile returned no error")
	}
	// The envelope cannot claim a profile the invoice inside contradicts.
	if _, _, err := runCmd(t, runEmbed, "-profile", "basic", "-o", outPath(t), pdfa3PDF, ciiSample); err == nil {
		t.Fatal("embed with a profile the XML contradicts returned no error")
	}
	if _, _, err := runCmd(t, runEmbed, "-attach", "../../testdata/nope.csv", pdfa3PDF, ciiSample); err == nil {
		t.Fatal("embed with a missing attachment returned no error")
	}
	if _, _, err := runCmd(t, runEmbed, "-conformance", "Z", pdfa3PDF, ciiSample); err == nil {
		t.Fatal("embed with an unknown conformance level returned no error")
	}
	if _, _, err := runCmd(t, runEmbed, pdfa3PDF, "main.go"); err == nil {
		t.Fatal("embed with a non-XML invoice returned no error")
	}
	if _, _, err := runCmd(t, runEmbed, "../../testdata/nope.pdf", ciiSample); err == nil {
		t.Fatal("embed with a missing PDF returned no error")
	}
}

func TestRunExtractPullsThePayload(t *testing.T) {
	out, code, err := runCmd(t, runExtract, "-info", hybridA3)
	if err != nil || code != -1 {
		t.Fatalf("runExtract: err %v, exit %d", err, code)
	}
	if !strings.Contains(out, "CrossIndustryInvoice") {
		t.Errorf("extracted payload was not the invoice: %.120s", out)
	}
	path := filepath.Join(t.TempDir(), "payload.xml")
	if _, code, err = runCmd(t, runExtract, "-o", path, hybridA3); err != nil || code != -1 {
		t.Fatalf("runExtract -o: err %v, exit %d", err, code)
	}
	doc, err := os.ReadFile(path)
	if err != nil || len(doc) == 0 {
		t.Errorf("read %s: %v", path, err)
	}
	if _, _, err := runCmd(t, runExtract); err == nil {
		t.Fatal("extract with no file returned no error")
	}
	if _, _, err := runCmd(t, runExtract, plainPDF); err == nil {
		t.Fatal("extract on a PDF with no invoice returned no error")
	}
}

func TestRunCheckAuditsHybrids(t *testing.T) {
	out, code, err := runCmd(t, runCheck, hybridA3)
	if err != nil || code != -1 {
		t.Fatalf("runCheck: err %v, exit %d", err, code)
	}
	for _, want := range []string{"envelope", "invoice inside: valid CII document"} {
		if !strings.Contains(out, want) {
			t.Errorf("check output does not mention %q:\n%s", want, out)
		}
	}

	// A defect in the PDF/A envelope is a failed audit, and the status says so.
	if _, code, err = runCmd(t, runCheck, hybridOld); err != nil || code != 1 {
		t.Fatalf("runCheck on a defective hybrid: err %v, exit %d", err, code)
	}

	// A file that is not a PDF at all is an error line, not a verdict.
	out, code, err = runCmd(t, runCheck, ciiSample)
	if err != nil || code != 2 {
		t.Fatalf("runCheck on a loose XML: err %v, exit %d, out %q", err, code, out)
	}
	if !strings.Contains(out, "ERROR:") {
		t.Errorf("check output was:\n%s", out)
	}

	out, code, err = runCmd(t, runCheck, "-json", hybridA3)
	if err != nil || code != -1 {
		t.Fatalf("runCheck -json: err %v, exit %d", err, code)
	}
	var results []hybrid.Result
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("the check report does not parse: %v", err)
	}
	if len(results) != 1 || results[0].Rules == nil || !results[0].Rules.Valid {
		t.Errorf("check report was %+v, want one valid verdict inside", results)
	}

	if _, _, err := runCmd(t, runCheck); err == nil {
		t.Fatal("check with no input returned no error")
	}
	if _, _, err := runCmd(t, runCheck, "-profile", "zork", hybridA3); err == nil {
		t.Fatal("check with an unknown bundle returned no error")
	}
}

// The rule-set flags are shared by every command that names a rule set, so a
// typo has to be caught before any file is opened.
func TestRuleSetOfReadsTheFlags(t *testing.T) {
	opt, err := ruleSetOf(" model , codelist ,, ", "XRechnung", "")
	if err != nil {
		t.Fatalf("ruleSetOf: %v", err)
	}
	if len(opt.Phases) != 2 || opt.Phases[0] != "model" || opt.Phases[1] != "codelist" {
		t.Errorf("phases were %q", opt.Phases)
	}
	if opt.CIUS != einvoice.CIUSXRechnung {
		t.Errorf("CIUS was %q", opt.CIUS)
	}
	if _, err := ruleSetOf("", "zork", ""); err == nil {
		t.Fatal("an unknown CIUS was accepted")
	}
	if _, err := ruleSetOf("", "", "zork"); err == nil {
		t.Fatal("an unknown bundle was accepted")
	}
	if opt, err = ruleSetOf("", "", "peppol"); err != nil || opt.Profile != einvoice.ProfilePeppol {
		t.Fatalf("ruleSetOf peppol: %v, %+v", err, opt)
	}
}

// syntaxOf accepts the names customers use for the same two grammars.
func TestSyntaxOfAcceptsAliases(t *testing.T) {
	for _, name := range []string{"cii", "UNCEFACT", "FacturX", "ZUGFeRD", "XRechnung"} {
		if sx, err := syntaxOf(name); err != nil || sx != einvoice.SyntaxCII {
			t.Errorf("syntaxOf(%q) = %q, %v", name, sx, err)
		}
	}
	for _, name := range []string{"ubl", "Peppol", "OASIS"} {
		if sx, err := syntaxOf(name); err != nil || sx != einvoice.SyntaxUBL {
			t.Errorf("syntaxOf(%q) = %q, %v", name, sx, err)
		}
	}
	if _, err := syntaxOf("zork"); err == nil {
		t.Fatal("an unknown syntax was accepted")
	}
}

func TestStringListRepeats(t *testing.T) {
	var s stringList
	if err := s.Set("a.csv"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("b.pdf"); err != nil {
		t.Fatal(err)
	}
	if s.String() != "a.csv,b.pdf" {
		t.Errorf("stringList held %q", s.String())
	}
}

func TestCheckOptionsAndReadInput(t *testing.T) {
	opt, err := checkOptions(" XRechnung ", "peppol")
	if err != nil {
		t.Fatalf("checkOptions: %v", err)
	}
	if opt.CIUS != einvoice.CIUSXRechnung || opt.Profile != einvoice.ProfilePeppol {
		t.Errorf("checkOptions gave %+v", opt)
	}
	if data, err := readInput(ciiSample); err != nil || len(data) == 0 {
		t.Errorf("readInput: %d bytes, %v", len(data), err)
	}
	if _, err := readInput("../../testdata/nope.xml"); err == nil {
		t.Error("readInput on a missing file returned no error")
	}
}

// failWriter stands in for a closed pipe: the first write fails, which is how
// the "the customer hung up" branch of every command gets exercised.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

// brokenPipe sends stdout to a writer that refuses everything.
func brokenPipe(t *testing.T) {
	t.Helper()
	previous, previousExit := stdout, exit
	t.Cleanup(func() { stdout, exit = previous, previousExit })
	stdout, exit = failWriter{}, func(int) {}
}

func TestCommandsReportAWritersRefusal(t *testing.T) {
	cases := []struct {
		name string
		cmd  func([]string) error
		args []string
	}{
		{"validate -json", runValidate, []string{"-json", ciiSample}},
		{"gen", runGen, []string{modelJSON}},
		{"parse", runParse, []string{ciiSample}},
		{"rules -json", runRules, []string{"-json", "-syntax", "cii"}},
		{"artefacts", runArtefacts, nil},
		{"extract", runExtract, []string{hybridA3}},
		{"embed", runEmbed, []string{pdfa3PDF, ciiSample}},
		{"check -json", runCheck, []string{"-json", hybridA3}},
	}
	for _, c := range cases {
		brokenPipe(t)
		if err := c.cmd(c.args); err == nil {
			t.Errorf("%s survived a writer that refused it", c.name)
		}
	}
}

// An unwritable -o is the caller's mistake, and the only way a command finds
// out is when the write fails.
func TestCommandsReportAnUnwritableOutputFile(t *testing.T) {
	cases := []struct {
		name string
		cmd  func([]string) error
		args []string
	}{
		{"gen", runGen, []string{"-syntax", "cii", "-o", "/nonexistent/dir/out.xml", modelJSON}},
		{"parse", runParse, []string{"-o", "/nonexistent/dir/out.json", ciiSample}},
		{"extract", runExtract, []string{"-o", "/nonexistent/dir/out.xml", hybridA3}},
		{"embed", runEmbed, []string{"-o", "/nonexistent/dir/out.pdf", pdfa3PDF, ciiSample}},
	}
	for _, c := range cases {
		if _, code, err := runCmd(t, c.cmd, c.args...); err == nil || code != -1 {
			t.Errorf("%s -o /nonexistent: err %v, exit %d, want an error", c.name, err, code)
		}
	}
}

func TestWriteArtefactsReportsARefusalToWrite(t *testing.T) {
	if err := writeArtefacts(failWriter{}, false, einvoice.Options{}); err == nil {
		t.Error("the text report survived a writer that refused it")
	}
	if err := writeArtefacts(failWriter{}, true, einvoice.Options{}); err == nil {
		t.Error("the JSON report survived a writer that refused it")
	}
}
