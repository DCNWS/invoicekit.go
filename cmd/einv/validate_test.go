package main

import (
	"testing"

	"github.com/dcnws/invoicekit.go"
)

// The exit status is the part of `einv validate` a build script reads, and it
// has to survive a bad file sitting next to a good one.
func TestExitCodeForPrefersRuleBreaches(t *testing.T) {
	cases := []struct {
		name string
		all  []verdict
		want int
	}{
		{"all valid", []verdict{{Valid: true}}, 0},
		{"invalid", []verdict{{Valid: false, Fatal: 1}}, 1},
		{"unreadable", []verdict{{Error: "open x: no such file"}}, 2},
		{"both", []verdict{{Error: "unreadable"}, {Valid: false, Fatal: 1}}, 1},
	}
	for _, c := range cases {
		if got := exitCodeFor(c.all); got != c.want {
			t.Errorf("%s: exitCodeFor = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestVerdictTruncatesFindingsToMax(t *testing.T) {
	v := verdictOf("../../testdata/samples/cii-xrechnung.xml", einvoice.Options{CIUS: einvoice.CIUSXRechnung}, 1)
	if v.Error != "" {
		t.Fatalf("verdictOf: %s", v.Error)
	}
	if v.Truncated != 2 || len(v.Findings) != 1 {
		t.Errorf("got %d findings, want the -max limit of 1", len(v.Findings))
	}
}
