package en16931

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// peppolRefDir returns the checkout of the Peppol BIS Billing rule sets, used
// to check that the embedded bundle is byte-for-byte what OpenPEPPOL released.
// Override the location with EINVOICE_PEPPOL.
func peppolRefDir(t *testing.T) string {
	t.Helper()
	d := corpusDir(t, "EINVOICE_PEPPOL", "ref-peppol")
	if d == "" {
		t.Skipf("Peppol BIS rule sets not available: set EINVOICE_PEPPOL to a Peppol BIS 3.0 release checkout")
	}
	if _, err := os.Stat(filepath.Join(d, "rules", "sch")); err != nil {
		t.Skipf("no rules/sch in %s", d)
	}
	return d
}

// TestProfileCatalogue checks what the library advertises about the rule-set
// bundles it carries, since callers pick them by name.
func TestProfileCatalogue(t *testing.T) {
	names := ProfileNames()
	if len(names) == 0 {
		t.Fatal("no profiles embedded")
	}
	var found bool
	for i, n := range names {
		if n == string(ProfilePeppol) {
			found = true
		}
		if i > 0 && names[i-1] > names[i] {
			t.Fatalf("ProfileNames is not sorted: %v", names)
		}
		if v := ProfileVersion(Profile(n)); v == "" {
			t.Errorf("ProfileVersion(%q) is empty", n)
		}
	}
	if !found {
		t.Fatalf("ProfileNames() = %v, want it to contain %q", names, ProfilePeppol)
	}
	if v := ProfileVersion("nope"); v != "" {
		t.Errorf("ProfileVersion of an unknown profile = %q, want empty", v)
	}
}

// TestProfileRuleSets parses and compiles every artefact of every profile for
// both syntaxes. A rule set that does not load, or an expression the XPath
// engine cannot parse, is a build-time fact and must fail loudly here.
func TestProfileRuleSets(t *testing.T) {
	for _, p := range ProfileNames() {
		for _, sx := range []Syntax{SyntaxCII, SyntaxUBL} {
			sets, err := ProfileRuleSets(Profile(p), sx)
			if err != nil {
				t.Fatalf("%s %s: %v", p, sx, err)
			}
			if len(sets) < 2 {
				t.Fatalf("%s %s: %d artefact(s), want the norm plus the network rules", p, sx, len(sets))
			}
			var rules, asserts, funcs int
			for _, s := range sets {
				if bad := s.CompileErrors(); len(bad) > 0 {
					t.Errorf("%s %s [%s]: %d uncompilable expression(s), first: %s", p, sx, s.Name, len(bad), bad[0])
				}
				r, a := s.Stats()
				rules += r
				asserts += a
				funcs += len(s.Funcs)
			}
			if asserts < 200 {
				t.Errorf("%s %s: %d assertions across the bundle, which looks too small", p, sx, asserts)
			}
			if funcs == 0 {
				t.Errorf("%s %s: the bundle declares no extension functions, but Peppol's does", p, sx)
			}
			ids, err := ProfilePhaseIDs(Profile(p), sx)
			if err != nil {
				t.Fatalf("%s %s: ProfilePhaseIDs: %v", p, sx, err)
			}
			if len(ids) == 0 {
				t.Errorf("%s %s: the bundle's EN 16931 artefact declares no phases", p, sx)
			}
			t.Logf("%s %s: %d artefacts, %d rules, %d asserts, %d funcs, phases %v",
				p, sx, len(sets), rules, asserts, funcs, ids)
		}
	}
}

// TestProfileFunctions names the extension functions Peppol writes its numeric
// and identifier checks with. They are declared inside the Schematron as
// <xsl:function> elements, so the engine has to read them rather than assume a
// built-in: a missing one means the rules that use it silently stop working.
func TestProfileFunctions(t *testing.T) {
	want := []string{"u:slack", "u:gln", "u:mod11", "u:checkCF", "u:checkPIVA", "u:checkSEOrgnr", "u:abn"}
	for _, sx := range []Syntax{SyntaxCII, SyntaxUBL} {
		sets, err := ProfileRuleSets(ProfilePeppol, sx)
		if err != nil {
			t.Fatal(err)
		}
		have := map[string]bool{}
		for _, s := range sets {
			for _, f := range s.Funcs {
				have[f] = true
			}
		}
		for _, f := range want {
			if !have[f] {
				t.Errorf("%s: extension function %s is not declared by the bundle", sx, f)
			}
		}
	}
}

// TestProfileArtefactsUnmodified compares every embedded artefact against the
// released file, byte for byte. The licence allows them to be redistributed
// unmodified, so a bundle that has drifted is a licence problem as well as a
// correctness one.
func TestProfileArtefactsUnmodified(t *testing.T) {
	ref := filepath.Join(peppolRefDir(t), "rules", "sch")
	for _, p := range ProfileNames() {
		for _, sx := range []Syntax{SyntaxCII, SyntaxUBL} {
			for _, a := range profileArtefacts[Profile(p)][sx] {
				up, err := os.ReadFile(filepath.Join(ref, strings.TrimSuffix(a.file, ".gz")))
				if err != nil {
					t.Errorf("%s %s [%s]: no upstream artefact to compare against: %v", p, sx, a.name, err)
					continue
				}
				got := embeddedArtefact(t, a)
				if string(got) != string(up) {
					t.Errorf("%s %s [%s]: embedded artefact is %d bytes, upstream %d: the bundle has been modified",
						p, sx, a.name, len(got), len(up))
				}
			}
		}
	}
}

// embeddedArtefact returns the inflated bytes of one artefact, which is what
// the parser is handed.
func embeddedArtefact(t *testing.T, a artefact) []byte {
	t.Helper()
	raw, err := profileFS.ReadFile(peppolRoot + "/" + a.file)
	if err != nil {
		t.Fatal(err)
	}
	data, err := gunzip(a.file, raw)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestProfileSample runs the shipped UBL sample through the Peppol bundle. The
// point is not the finding list but the shape of a profile run: the base
// artefacts must not also have run, and every finding must say who spoke.
func TestProfileSample(t *testing.T) {
	data := read(t, "../../testdata/samples/ubl-min.xml")
	plain, err := Validate(data, Options{})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Validate(data, Options{Profile: ProfilePeppol})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Profile != ProfilePeppol {
		t.Errorf("Report.Profile = %q, want %q", rep.Profile, ProfilePeppol)
	}
	if rep.Internal != 0 {
		for _, f := range rep.Findings {
			if f.Err != "" {
				t.Errorf("%s [%s] could not be evaluated: %s", f.ID, f.Source, f.Err)
			}
		}
	}
	for _, f := range rep.Findings {
		if f.Source != "peppol" && f.Source != "peppol-cen" {
			t.Errorf("%s: Source = %q, want peppol or peppol-cen", f.ID, f.Source)
		}
	}
	// A profile carries its own copy of the norm, so the norms findings come
	// from the bundle rather than from the artefacts the plain run used.
	t.Logf("plain: %v; peppol: %d fatal, %d warn, ids %v",
		plain.IDs(), rep.Fatal, rep.Warn, rep.IDs())
}

// TestProfileUnknown checks the error paths a caller hits after typoing a name.
func TestProfileUnknown(t *testing.T) {
	if _, err := ProfileRuleSets("peppol-bis", SyntaxUBL); err == nil ||
		!strings.Contains(err.Error(), string(ProfilePeppol)) {
		t.Errorf("unknown profile error = %v, want it to list what is available", err)
	}
	if _, err := ProfileRuleSets(ProfilePeppol, "PDF"); err == nil ||
		!strings.Contains(err.Error(), "PDF") {
		t.Errorf("unknown syntax error = %v, want it to name the syntax", err)
	}
	data := read(t, "../../testdata/samples/ubl-min.xml")
	if _, err := Validate(data, Options{Profile: "peppol-bis"}); err == nil {
		t.Error("Validate with an unknown profile should fail rather than check the norm alone")
	}
}

// TestProfileCached checks that a bundle is compiled once per profile and
// syntax, and that a profile run does not share cache entries with the plain
// EN 16931 artefacts it replaces.
func TestProfileCached(t *testing.T) {
	a, err := ProfileRuleSets(ProfilePeppol, SyntaxUBL)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ProfileRuleSets(ProfilePeppol, SyntaxUBL)
	if err != nil {
		t.Fatal(err)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("artefact %d was recompiled", i)
		}
	}
	base, err := Schematron(SyntaxUBL)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range a {
		if s == base {
			t.Errorf("[%s]: the profile bundle shares a cache entry with the EN 16931 artefacts", s.Name)
		}
	}
}

// peppolRefUnitDirs lists the upstream unit test sets for the Peppol rules
// themselves. They are VEFA testSets: each <test> says which assertion must
// fire and which must not.
func peppolRefUnitDirs(t *testing.T) []string {
	t.Helper()
	root := filepath.Join(peppolRefDir(t), "rules")
	var out []string
	for _, d := range []string{"unit-UBL-PEPPOL", "unit-CII-PEPPOL"} {
		dir := filepath.Join(root, d)
		if len(glob(t, dir, "*.xml")) == 0 {
			t.Fatalf("no test sets in %s", dir)
		}
		out = append(out, dir)
	}
	return out
}

// TestPeppolUnitTests replays the OpenPEPPOL unit tests for the network rules
// through the embedded bundle. Both directions are asserted: a test payload
// must trip the assertion it is named for and must not trip the ones marked
// success, which is what stops a permissive engine from passing.
func TestPeppolUnitTests(t *testing.T) {
	var cases, bad, checked, mustfires int
	for _, d := range peppolRefUnitDirs(t) {
		for _, f := range glob(t, d, "*.xml") {
			n, b, chk, mf, err := runTestSet(t, f, read(t, f), Options{Profile: ProfilePeppol})
			if err != nil {
				t.Errorf("%s: %v", filepath.Base(f), err)
				continue
			}
			cases += n
			bad += b
			checked += chk
			mustfires += mf
		}
	}
	if cases == 0 {
		t.Fatal("no Peppol unit tests ran")
	}
	t.Logf("%d Peppol cases, %d must-not-fire + %d must-fire assertions checked, %d failures",
		cases, checked, mustfires, bad)
	if bad > 0 {
		t.Errorf("%d Peppol unit assertions failed", bad)
	}
}

// TestPeppolNationalUnits replays the country unit sets that ship inside the
// same bundle: the rules the profile carries for DE, DK, GR, IT, NL, NO and SE.
// They are part of the artefacts, so the engine has to read them too.
func TestPeppolNationalUnits(t *testing.T) {
	root := filepath.Join(peppolRefDir(t), "rules")
	dirs, err := filepath.Glob(filepath.Join(root, "unit-*"))
	if err != nil {
		t.Fatal(err)
	}
	var cases, bad, checked, mustfires int
	for _, d := range dirs {
		base := filepath.Base(d)
		if !strings.HasPrefix(base, "unit-") || strings.HasSuffix(base, "-PEPPOL") ||
			strings.HasSuffix(base, "-SBDH") {
			continue
		}
		files := glob(t, d, "*.xml")
		if len(files) == 0 {
			continue
		}
		var fileBad int
		for _, f := range files {
			n, b, chk, mf, err := runTestSet(t, f, read(t, f), Options{Profile: ProfilePeppol})
			if err != nil {
				t.Errorf("%s: %v", filepath.Base(f), err)
				continue
			}
			cases += n
			bad += b
			fileBad += b
			checked += chk
			mustfires += mf
		}
		if fileBad == len(files) && len(files) > 2 {
			t.Errorf("%s: every one of the %d test sets failed, which suggests the rules for that country are not in the bundle", base, len(files))
		}
	}
	t.Logf("%d national cases, %d must-not-fire + %d must-fire checked, %d failures",
		cases, checked, mustfires, bad)
}
