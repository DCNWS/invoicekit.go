package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	einvoice "github.com/dcnws/invoicekit.go"
)

// run is the whole command line. The table behind it is what an alias like
// `einv v` lives in, so both spellings have to reach the same function.
func TestRunDispatchesEveryName(t *testing.T) {
	buf, code := seams(t)
	if err := run([]string{"einv"}); !errors.Is(err, errUsage) {
		t.Errorf("no command at all gave %v, want the usage error", err)
	}
	if err := run([]string{"einv", "zork"}); !errors.Is(err, errUsage) {
		t.Errorf("an unknown command gave %v, want the usage error", err)
	}
	if err := run([]string{"einv", "help"}); err != nil {
		t.Errorf("help gave %v, want a clean exit", err)
	}
	if err := run([]string{"einv", "-V"}); err != nil {
		t.Fatalf("version gave %v", err)
	}
	if got := buf.String(); got != "einv "+version+"\n" {
		t.Errorf("version said %q, want %q", got, "einv "+version+"\n")
	}

	// An alias has to reach the same command, exit status included.
	if err := run([]string{"einv", "validate", "../../testdata/nope.xml"}); err != nil {
		t.Errorf("validate on a missing file gave %v, want the verdict inside", err)
	}
	if *code != 2 {
		t.Errorf("validate on a missing file exited with %d, want 2", *code)
	}
	*code = -1
	if err := run([]string{"einv", "v", ciiSample}); err != nil || *code != -1 {
		t.Errorf("`v` behaved differently from validate: err %v, exit %d", err, *code)
	}

	// A command line error is not a usage error: the message comes from the
	// command, and main prints it.
	if err := run([]string{"einv", "detect"}); err == nil || errors.Is(err, errUsage) {
		t.Errorf("detect with no file gave %v, want the command's own error", err)
	}
}

// printRules is the block `check` prints under a hybrid PDF. Every branch of it
// is a different thing a support desk needs to read.
func TestPrintRulesCoversEveryVerdictShape(t *testing.T) {
	buf, _ := seams(t)
	printRules(nil, 40)
	if buf.Len() != 0 {
		t.Errorf("no invoice inside printed %q, want nothing", buf.String())
	}

	valid := &einvoice.Result{Syntax: einvoice.SyntaxUBL, Profile: einvoice.ProfilePeppol,
		CIUS: einvoice.CIUSXRechnung, Valid: true}
	printRules(valid, 40)
	if got := buf.String(); !strings.Contains(got, "valid UBL peppol + xrechnung document") {
		t.Errorf("a valid result printed %q", got)
	}

	buf.Reset()
	bad := &einvoice.Result{
		Syntax: einvoice.SyntaxCII,
		Fatal:  2, Warn: 1,
		Schema: []einvoice.SchemaFinding{{Location: "/rsm:CrossIndustryInvoice", Message: "missing element"}},
		Findings: []einvoice.Finding{
			{ID: "BR-02", Message: "seller name missing", Context: "/ram:SellerTradeParty"},
			{ID: "BR-CO-25", Message: "no VAT breakdown", Flag: "warning"},
		},
	}
	printRules(bad, 1)
	got := buf.String()
	for _, want := range []string{
		"INVALID CII invoice (2 fatal, 1 warning, 1 structural)",
		"SCHEMA", "missing element at /rsm:CrossIndustryInvoice",
		"BR-02", "seller name missing at /ram:SellerTradeParty",
		"... and 1 more (use -max -1 to list all)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the report is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "BR-CO-25") {
		t.Errorf("-max 1 still printed the second finding:\n%s", got)
	}
}

// The artefact report is the second half of a verdict: it names the releases
// behind the findings. Rule sets have to appear in it in a stable order.
func TestWriteArtefactsIncludesTheSelectedRuleSets(t *testing.T) {
	opt := einvoice.Options{CIUS: einvoice.CIUSXRechnung, Profile: einvoice.ProfilePeppol}
	var buf strings.Builder
	if err := writeArtefacts(&buf, false, opt); err != nil {
		t.Fatalf("writeArtefacts: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"cius\txrechnung\t", "profile\tpeppol\t"} {
		if !strings.Contains(got, want) {
			t.Errorf("the report does not carry %q:\n%s", want, got)
		}
	}

	buf.Reset()
	if err := writeArtefacts(&buf, true, opt); err != nil {
		t.Fatalf("writeArtefacts -json: %v", err)
	}
	var report struct {
		Engine   string            `json:"engine"`
		CIUS     map[string]string `json:"cius"`
		Profiles map[string]string `json:"profiles"`
	}
	if err := json.Unmarshal([]byte(buf.String()), &report); err != nil {
		t.Fatalf("the JSON report does not parse: %v\n%s", err, buf.String())
	}
	if report.Engine == "" || report.CIUS["xrechnung"] == "" || report.Profiles["peppol"] == "" {
		t.Errorf("the JSON report is missing a version or a rule set: %s", buf.String())
	}
}
