package main

import (
	"strings"
	"testing"

	einvoice "github.com/dcnws/invoicekit.go"
)

// ruleLine is the text form of the rule reference: a grep and an awk should
// both work on it, so one rule has to be one line even when the published text
// is not.

func TestRuleLineStaysOnOneLine(t *testing.T) {
	r := einvoice.Rule{
		Syntax: einvoice.SyntaxCII,
		ID:     "BR-CL-21",
		Flag:   "fatal",
		Phases: []string{"codelist_phase"},
		Text:   "[BR-CL-21]-Item standard identifier scheme identifier MUST belong\n\t      to the ISO 6523 ICD code list",
	}
	fields := strings.Split(ruleLine(r), "\t")
	if len(fields) != 5 {
		t.Fatalf("ruleLine has %d tab-separated fields, want 5: %q", len(fields), ruleLine(r))
	}
	want := []string{"CII", "BR-CL-21", "fatal", "codelist_phase",
		"[BR-CL-21]-Item standard identifier scheme identifier MUST belong to the ISO 6523 ICD code list"}
	for i, w := range want {
		if fields[i] != w {
			t.Errorf("field %d is %q, want %q", i, fields[i], w)
		}
	}
}

func TestRuleLineMarksWarningsAndNoPhases(t *testing.T) {
	line := ruleLine(einvoice.Rule{Syntax: einvoice.SyntaxUBL, ID: "UBL-XX-1", Flag: "warning", Text: "a soft rule"})
	fields := strings.Split(line, "\t")
	if len(fields) != 5 {
		t.Fatalf("ruleLine %q has %d fields, want 5", line, len(fields))
	}
	if strings.ContainsAny(line, "\n") {
		t.Errorf("ruleLine spans lines: %q", line)
	}
	if fields[2] != "warning" {
		t.Errorf("flag field %q, want warning", fields[2])
	}
	if fields[3] != "" {
		t.Errorf("phase field %q, want empty for a rule set with no phases", fields[3])
	}
}
