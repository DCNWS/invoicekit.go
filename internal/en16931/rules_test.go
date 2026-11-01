package en16931

import (
	"strings"
	"testing"
)

// Rules lists every assertion a run would apply, so a caller can publish, count
// or diff the rule set a build enforces without needing a document to check.

func TestRulesCoverTheWholeRuleSet(t *testing.T) {
	for _, want := range []struct {
		sx   Syntax
		want int
	}{
		{SyntaxCII, 810}, // the published EN16931-CII Schematron
		{SyntaxUBL, 982}, // the published EN16931-UBL Schematron
	} {
		got, err := Rules(want.sx, "", "")
		if err != nil {
			t.Fatalf("%s: %v", want.sx, err)
		}
		if len(got) != want.want {
			t.Errorf("%s: %d assertions, want %d", want.sx, len(got), want.want)
		}
	}
}

func TestRulesSayWhoSaidWhat(t *testing.T) {
	rs, err := Rules(SyntaxCII, "", "")
	if err != nil {
		t.Fatal(err)
	}
	br21 := findRule(t, rs, "BR-21")
	if br21.Source != "en16931" {
		t.Errorf("BR-21 source %q, want en16931", br21.Source)
	}
	if br21.Flag != "fatal" {
		t.Errorf("BR-21 flag %q, want fatal", br21.Flag)
	}
	if !strings.Contains(br21.Text, "[BR-21]") {
		t.Errorf("BR-21 text %q, want the published message", br21.Text)
	}
	if br21.Context == "" || br21.Test == "" {
		t.Errorf("BR-21 carries no context or test: %+v", br21)
	}
	if !hasPhase(br21, "EN16931-model-phase") {
		t.Errorf("BR-21 phases %v, want the model phase", br21.Phases)
	}
	// A syntax rule belongs to the syntax phase, which is what makes the two
	// halves of a verdict distinguishable in a report.
	sr := findRule(t, rs, "CII-SR-009")
	if !hasPhase(sr, "syntax_phase") {
		t.Errorf("CII-SR-009 phases %v, want the syntax phase", sr.Phases)
	}
}

func TestRulesAddTheCIUSAndSwapForAProfile(t *testing.T) {
	base, err := Rules(SyntaxCII, "", "")
	if err != nil {
		t.Fatal(err)
	}
	withDE, err := Rules(SyntaxCII, CIUSXRechnung, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(withDE) <= len(base) {
		t.Fatalf("xrechnung added nothing: %d rules, base %d", len(withDE), len(base))
	}
	de := findRule(t, withDE, "BR-DE-21")
	if de.Source != "xrechnung" {
		t.Errorf("BR-DE-21 source %q, want xrechnung", de.Source)
	}

	bis, err := Rules(SyntaxCII, "", ProfilePeppol)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range bis {
		if r.Source == "en16931" {
			t.Fatalf("profile left the base artefacts in the run: %+v", r)
		}
	}
	if findRule(t, bis, "BR-21") == nil {
		t.Error("the Peppol bundle does not carry BR-21")
	}
}

func TestRulesRefuseRuleSetsTheyDoNotCarry(t *testing.T) {
	if _, err := Rules(Syntax("DOC"), "", ""); err == nil {
		t.Error("unknown syntax accepted")
	}
	if _, err := Rules(SyntaxCII, CIUS("fr"), ""); err == nil {
		t.Error("unknown CIUS accepted")
	}
	if _, err := Rules(SyntaxCII, "", Profile("bis")); err == nil {
		t.Error("unknown profile accepted")
	}
}

// findRule returns the rule with an id, failing the test when it is absent.
func findRule(t *testing.T, rs []Rule, id string) *Rule {
	t.Helper()
	for i, r := range rs {
		if r.ID == id {
			return &rs[i]
		}
	}
	t.Fatalf("no rule %q in %d rules", id, len(rs))
	return nil
}

func hasPhase(r *Rule, id string) bool {
	for _, p := range r.Phases {
		if p == id {
			return true
		}
	}
	return false
}
