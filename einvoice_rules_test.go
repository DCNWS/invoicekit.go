package einvoice

import (
	"encoding/json"
	"testing"
)

// Rules is the read side of the engine: what a build enforces, without a
// document to check.

func TestRulesMatchAValidationRun(t *testing.T) {
	all, err := Rules(SyntaxCII, Options{})
	if err != nil {
		t.Fatal(err)
	}
	de, err := Rules(SyntaxCII, Options{CIUS: CIUSXRechnung})
	if err != nil {
		t.Fatal(err)
	}
	if len(de) <= len(all) {
		t.Errorf("Options.CIUS changed nothing: %d rules, base %d", len(de), len(all))
	}
	for _, r := range de {
		if r.Syntax != SyntaxCII {
			t.Fatalf("rule %q claims syntax %q", r.ID, r.Syntax)
		}
		if r.ID == "" || r.Text == "" || r.Flag == "" {
			t.Fatalf("rule is not fully described: %+v", r)
		}
	}
}

func TestRulesAreNamedForJSON(t *testing.T) {
	rs, err := Rules(SyntaxUBL, Options{Profile: ProfilePeppol})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(rs)
	if err != nil {
		t.Fatal(err)
	}
	var back []map[string]any
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if len(back) != len(rs) {
		t.Fatalf("JSON round trip lost rules: %d, want %d", len(back), len(rs))
	}
	for _, key := range []string{"id", "syntax", "source", "flag", "text", "context", "test"} {
		if _, ok := back[0][key]; !ok {
			t.Errorf("JSON has no %q field: %v", key, back[0])
		}
	}
}
