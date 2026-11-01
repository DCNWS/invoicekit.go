package sch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dcnws/invoicekit.go/internal/xp"
)

func load(t *testing.T, name string) *Schema {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "en16931", "schemas", name))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Parse(b)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return s
}

func TestParseCII(t *testing.T) {
	s := load(t, "en16931-cii.sch")
	if len(s.NS) != 6 {
		t.Errorf("ns: got %d, want 6 (%v)", len(s.NS), s.NS)
	}
	if len(s.Phases) != 3 {
		t.Errorf("phases: got %d, want 3", len(s.Phases))
	}
	if len(s.Patterns) != 3 {
		t.Errorf("patterns: got %d, want 3", len(s.Patterns))
	}
	rules, asserts := s.Stats()
	t.Logf("CII: %d rules, %d asserts, phases %v", rules, asserts, s.PhaseIDs())
	if rules != 115 || asserts != 810 {
		t.Errorf("counts: got %d rules/%d asserts, want 115/810", rules, asserts)
	}
}

func TestCompileRealSchemas(t *testing.T) {
	for _, name := range []string{"en16931-cii.sch", "en16931-ubl.sch"} {
		s := load(t, name)
		start := time.Now()
		err := s.Compile()
		el := time.Since(start)
		if bad := s.CompileErrors(); len(bad) > 0 {
			t.Errorf("%s: %d uncompilable expressions", name, len(bad))
			for _, e := range bad {
				t.Logf("  %s", e)
			}
		}
		rules, asserts := s.Stats()
		t.Logf("%s: compiled %d rules / %d asserts in %v (err=%v)", name, rules, asserts, el.Round(time.Millisecond), err)
	}
}

// TestUnphasedPatternAndRelativeContext pins the two Schematron semantics that
// the EN 16931 rule sets depend on and that are easy to get wrong: with no
// phase selected every pattern is active (including patterns no <phase>
// references), and a relative rule context is matched anywhere in the tree, not
// only as a child path of the root element.
func TestUnphasedPatternAndRelativeContext(t *testing.T) {
	src := `<?xml version="1.0"?>
<schema xmlns="http://purl.oclc.org/dsdl/schematron" query="xpath20">
  <ns prefix="t" uri="urn:test"/>
  <phase id="p1"><active pattern="mod"/></phase>
  <pattern id="mod">
    <rule context="t:Deep">
      <assert id="A-DEEP" flag="fatal" test="false">deep element flagged</assert>
    </rule>
  </pattern>
  <pattern id="unphased">
    <rule context="/t:Root">
      <assert id="A-ALL" flag="fatal" test="false">unphased pattern ran</assert>
    </rule>
  </pattern>
</schema>`
	s, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	d, err := xp.ParseXML(`<t:Root xmlns:t="urn:test"><t:Wrap><t:Deep>x</t:Deep></t:Wrap></t:Root>`)
	if err != nil {
		t.Fatal(err)
	}
	ids := func(fs []Finding) map[string]bool {
		m := map[string]bool{}
		for _, f := range fs {
			m[f.ID] = true
		}
		return m
	}
	all, err := s.Validate(d, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := ids(all)
	if !got["A-DEEP"] {
		t.Errorf("relative context t:Deep did not match at depth: findings %v", all)
	}
	if !got["A-ALL"] {
		t.Errorf("pattern referenced by no phase did not run with no phase selected")
	}
	scoped, err := s.Validate(d, []string{"p1"})
	if err != nil {
		t.Fatal(err)
	}
	if ids(scoped)["A-ALL"] {
		t.Errorf("phase p1 must not activate the unphased pattern")
	}
}
