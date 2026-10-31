package en16931

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestCiusCatalogue checks what the library advertises about the CIUS rule sets
// it carries, since callers pick them by name.
func TestCiusCatalogue(t *testing.T) {
	names := CiusNames()
	if len(names) == 0 {
		t.Fatal("no CIUS rule sets embedded")
	}
	var found bool
	for _, n := range names {
		if n == string(CIUSXRechnung) {
			found = true
		}
		for i := 1; i < len(names); i++ {
			if names[i-1] > names[i] {
				t.Fatalf("CiusNames is not sorted: %v", names)
			}
		}
		if v := CiusVersion(CIUS(n)); v == "" {
			t.Errorf("CiusVersion(%q) is empty", n)
		}
	}
	if !found {
		t.Fatalf("CiusNames() = %v, want it to contain %q", names, CIUSXRechnung)
	}
	if v := CiusVersion("nope"); v != "" {
		t.Errorf("CiusVersion of an unknown CIUS = %q, want empty", v)
	}
}

// TestCiusRuleSets parses and compiles every embedded CIUS rule set for both
// syntaxes. A rule set that does not load, or an expression the XPath engine
// cannot parse, is a build-time fact and must fail loudly here.
func TestCiusRuleSets(t *testing.T) {
	for _, sx := range []Syntax{SyntaxCII, SyntaxUBL} {
		s, err := CiusSchematron(CIUSXRechnung, sx)
		if err != nil {
			t.Fatalf("%s: %v", sx, err)
		}
		if s.Name != string(CIUSXRechnung) {
			t.Errorf("%s: Name = %q, want %q", sx, s.Name, CIUSXRechnung)
		}
		if bad := s.CompileErrors(); len(bad) > 0 {
			t.Errorf("%s: %d uncompilable expression(s), first: %s", sx, len(bad), bad[0])
		}
		rules, asserts := s.Stats()
		if rules < 20 || asserts < 40 {
			t.Errorf("%s: rules=%d asserts=%d, which looks too small for a CIUS", sx, rules, asserts)
		}
		ids := s.PhaseIDs()
		if len(ids) == 0 {
			t.Errorf("%s: the rule set declares no phases", sx)
		}
		want, err := CiusPhaseIDs(CIUSXRechnung, sx)
		if err != nil {
			t.Fatalf("%s: CiusPhaseIDs: %v", sx, err)
		}
		if strings.Join(want, ",") != strings.Join(ids, ",") {
			t.Errorf("CiusPhaseIDs = %v, want %v", want, ids)
		}
		t.Logf("%s: %d rules, %d asserts, phases %v, funcs %v", sx, rules, asserts, ids, s.Funcs)
	}
}

// TestCiusUnknown checks the error paths, which is what a caller that typoed a
// profile name hits.
func TestCiusUnknown(t *testing.T) {
	if _, err := CiusSchematron("peppol", SyntaxCII); err == nil || !strings.Contains(err.Error(), "xrechnung") {
		t.Errorf("unknown CIUS error = %v, want it to list what is available", err)
	}
	if _, err := CiusSchematron(CIUSXRechnung, "PDF"); err == nil || !strings.Contains(err.Error(), "PDF") {
		t.Errorf("unknown syntax error = %v, want it to name the syntax", err)
	}
	if _, err := Validate(ciusSample(t), Options{CIUS: "peppol"}); err == nil {
		t.Error("Validate with an unknown CIUS should fail rather than check the norm alone")
	}
}

// TestCiusCached checks that a rule set is compiled once per name and syntax:
// compiling is far more expensive than validating a document.
func TestCiusCached(t *testing.T) {
	a, err := CiusSchematron(CIUSXRechnung, SyntaxCII)
	if err != nil {
		t.Fatal(err)
	}
	b, err := CiusSchematron(CIUSXRechnung, SyntaxCII)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Error("CiusSchematron recompiled the rule set")
	}
	base, err := Schematron(SyntaxCII)
	if err != nil {
		t.Fatal(err)
	}
	if base == a {
		t.Error("the CIUS and EN 16931 rule sets share a cache entry")
	}
}

// TestCiusFindingsCarrySource checks the one thing a caller needs in order to
// tell the two rule sets apart: every finding says who spoke.
func TestCiusFindingsCarrySource(t *testing.T) {
	data := ciusSample(t)
	base, err := Validate(data, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range base.Findings {
		if f.Source != "en16931" {
			t.Errorf("%s: Source = %q, want en16931", f.ID, f.Source)
		}
	}
	both, err := Validate(data, Options{CIUS: CIUSXRechnung})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range both.Findings {
		if f.Source != string(CIUSXRechnung) && f.Source != "en16931" {
			t.Errorf("%s: Source = %q, want en16931 or xrechnung", f.ID, f.Source)
		}
	}
	// A CIUS may restrict the norm but never relax it, so its findings must sit
	// on top of the EN 16931 ones, never replace them.
	if missing := missingIDs(base.IDs(), both.IDs()); len(missing) > 0 {
		t.Errorf("validating with the CIUS lost the EN 16931 findings %v", missing)
	}
}

// TestXRechnungSample pins what the shipped XRechnung sample shows. The sample
// is EN 16931 clean; only the German rules have anything to say about it.
func TestXRechnungSample(t *testing.T) {
	data := ciusSample(t)
	base, err := Validate(data, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(base.Findings) > 0 {
		t.Fatalf("sample should be EN 16931 clean, got %v", base.IDs())
	}
	rep, err := Validate(data, Options{CIUS: CIUSXRechnung})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Internal != 0 {
		t.Fatalf("%d rule(s) could not be evaluated", rep.Internal)
	}
	if rep.Valid() {
		t.Fatal("sample should fail the German rules (it declares no XRechnung business process)")
	}
	if rep.CIUS != CIUSXRechnung {
		t.Errorf("Report.CIUS = %q, want %q", rep.CIUS, CIUSXRechnung)
	}
	counts := map[string]string{}
	for _, f := range rep.Findings {
		counts[f.ID] = f.Flag
	}
	for _, id := range []string{"BR-DE-21", "BR-DE-27", "BR-DE-28"} {
		if _, ok := counts[id]; !ok {
			t.Errorf("expected %s to fire, findings are %v", id, rep.IDs())
		}
	}
	// Upstream declares these three as warnings, not fatal errors: XRechnung
	// tolerates a foreign business process id but says so. The flags must come
	// through exactly as the rule set writes them.
	for _, id := range []string{"BR-DE-21", "BR-DE-27", "BR-DE-28"} {
		if counts[id] != "warning" {
			t.Errorf("%s flag = %q, want warning", id, counts[id])
		}
	}
	if rep.Fatal != 0 {
		t.Errorf("Fatal = %d, want 0: the sample only earns warnings", rep.Fatal)
	}
}

// TestCiusCorpus runs every CEF test document through EN 16931 plus XRechnung.
// The documents are German-neutral, so BR-DE findings are expected and mean
// nothing here; what must never happen is a rule the engine could not evaluate,
// which would mean our XPath subset cannot read a construct the XRechnung rule
// sets use.
func TestCiusCorpus(t *testing.T) {
	dir := refDir(t) + "/test/testfiles"
	var docs, de int
	for _, f := range glob(t, dir, "*.xml") {
		rep, err := Validate(read(t, f), Options{CIUS: CIUSXRechnung})
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		docs++
		if rep.Internal > 0 {
			t.Errorf("%s [%s]: %d rule(s) could not be evaluated", f, rep.Syntax, rep.Internal)
			for _, fi := range rep.Findings {
				if fi.Err != "" {
					t.Logf("   %s: %s", fi.ID, fi.Err)
				}
			}
		}
		for _, fi := range rep.Findings {
			if strings.HasPrefix(fi.ID, "BR-DE") || strings.HasPrefix(fi.ID, "BR-DEX") || strings.HasPrefix(fi.ID, "BR-TMP") {
				if fi.Source != string(CIUSXRechnung) {
					t.Errorf("%s: %s came from %q, want %q", f, fi.ID, fi.Source, CIUSXRechnung)
				}
				de++
			}
		}
	}
	if docs == 0 {
		t.Fatalf("no documents under %s", dir)
	}
	t.Logf("%d documents, %d CIUS findings", docs, de)
}

// xrefDir returns the checkout of the XRechnung Schematron repository. Its test
// instances are the only corpus that exercises the CIUS rules themselves, so
// they are worth a run even though they are written for the xmute instance
// generator and every one of them carries a mutation instruction. Override the
// location with EINVOICE_XRECHNUNG.
func xrefDir(t *testing.T) string {
	t.Helper()
	for _, name := range []string{
		"ref-xrechnung/xrechnung-schematron-v2.6.0-217c6cbf8ac9a99d780cefb4f044b2c2778d4744",
		"ref-xrechnung/xrechnung-schematron-master",
		"ref-xrepo/xrechnung-schematron-master",
	} {
		if d := corpusDir(t, "EINVOICE_XRECHNUNG", name); d != "" {
			return d
		}
	}
	t.Skipf("XRechnung instance corpus not available: set EINVOICE_XRECHNUNG to a KoSIT xrechnung-schematron checkout")
	return "" // unreachable: Skipf ends the test
}

// TestCiusXRechnungInstances replays every instance of the XRechnung test suite
// through the CIUS rule set. The instances are xmute sources rather than
// finished invoices, so which assertion each one is meant to trip is a property
// of the mutation that is applied to it, not of the file: what can be pinned
// here is that the rule set loads, that all 52 CII and 56 UBL assertions are
// readable, and that no document produces an unevaluated rule.
func TestCiusXRechnungInstances(t *testing.T) {
	root := filepath.Join(xrefDir(t), "test", "instances")
	var docs, internal int
	bySyntax := map[Syntax]int{}
	for _, sub := range []string{"cii", "ubl-inv", "ubl-cn"} {
		files := glob(t, filepath.Join(root, sub), "*.xml")
		if len(files) == 0 {
			t.Fatalf("no instances in %s/%s", root, sub)
		}
		for _, f := range files {
			rep, err := Validate(read(t, f), Options{CIUS: CIUSXRechnung})
			if err != nil {
				t.Errorf("%s: %v", f, err)
				continue
			}
			docs++
			bySyntax[rep.Syntax]++
			if rep.Internal > 0 {
				internal += rep.Internal
				for _, fi := range rep.Findings {
					if fi.Err != "" {
						t.Errorf("%s: %s [%s] could not be evaluated: %s", f, fi.ID, fi.Source, fi.Err)
					}
				}
			}
			for _, fi := range rep.Findings {
				if !strings.HasPrefix(fi.ID, "BR-DE") && !strings.HasPrefix(fi.ID, "BR-DEX") && !strings.HasPrefix(fi.ID, "BR-TMP") {
					continue
				}
				if fi.Source != string(CIUSXRechnung) {
					t.Errorf("%s: %s came from %q, want %q", f, fi.ID, fi.Source, CIUSXRechnung)
				}
			}
		}
	}
	if docs < 300 {
		t.Fatalf("only %d instances under %s", docs, root)
	}
	t.Logf("%d instances by syntax %v, %d unevaluated rules", docs, bySyntax, internal)
}

func missingIDs(want, got []string) []string {
	have := map[string]bool{}
	for _, g := range got {
		have[g] = true
	}
	var out []string
	for _, w := range want {
		if !have[w] {
			out = append(out, w)
		}
	}
	return out
}

func ciusSample(t *testing.T) []byte {
	t.Helper()
	return read(t, "../../testdata/samples/cii-xrechnung.xml")
}
