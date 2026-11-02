package einvoice

import (
	"strings"
	"testing"
)

// TestArtefacts checks the public view of the rule-set release the verdicts came
// from: one entry per syntax, each naming a version and a date.
func TestArtefacts(t *testing.T) {
	got := Artefacts()
	if len(got) != 2 {
		t.Fatalf("got %d artefacts, want one per syntax", len(got))
	}
	for _, a := range got {
		if a.Syntax != SyntaxCII && a.Syntax != SyntaxUBL {
			t.Errorf("artefact for unexpected syntax %q", a.Syntax)
		}
		if !strings.Contains(a.Version, ".") {
			t.Errorf("%s: version %q does not look like a version", a.Syntax, a.Version)
		}
		if len(a.Updated) != 10 {
			t.Errorf("%s: updated %q is not YYYY-MM-DD", a.Syntax, a.Updated)
		}
	}
}
