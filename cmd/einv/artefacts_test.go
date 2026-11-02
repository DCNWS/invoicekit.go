package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	einvoice "github.com/dcnws/invoicekit.go"
)

// The command exists so a verdict taken from a binary can name the artefacts
// that produced it, months later, without the source tree.
func TestWriteArtefactsTextNamesEveryRuleSet(t *testing.T) {
	var buf bytes.Buffer
	if err := writeArtefacts(&buf, false, einvoice.Options{}); err != nil {
		t.Fatalf("writeArtefacts: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"engine\t" + einvoice.Version,
		"EN 16931",
		string(einvoice.SyntaxCII),
		string(einvoice.SyntaxUBL),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	// One artefact per syntax, and the version the Commission stamped.
	if got := strings.Count(out, "1.3.17"); got != 2 {
		t.Errorf("rule-set version appears %d times, want once per syntax:\n%s", got, out)
	}
}

func TestWriteArtefactsJSONIsMachineReadable(t *testing.T) {
	var buf bytes.Buffer
	if err := writeArtefacts(&buf, true, einvoice.Options{CIUS: einvoice.CIUSXRechnung}); err != nil {
		t.Fatalf("writeArtefacts: %v", err)
	}
	var got struct {
		Engine   string              `json:"engine"`
		RuleSets []einvoice.Artefact `json:"rule_sets"`
		CIUS     map[string]string   `json:"cius"`
		Profiles map[string]string   `json:"profiles"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, buf.String())
	}
	if got.Engine != einvoice.Version {
		t.Errorf("engine = %q, want %q", got.Engine, einvoice.Version)
	}
	if len(got.RuleSets) != 2 {
		t.Fatalf("rule_sets = %d entries, want one per syntax:\n%s", len(got.RuleSets), buf.String())
	}
	if got.CIUS["xrechnung"] != einvoice.CiusVersion(einvoice.CIUSXRechnung) {
		t.Errorf("cius[xrechnung] = %q, want %q", got.CIUS["xrechnung"], einvoice.CiusVersion(einvoice.CIUSXRechnung))
	}
}
